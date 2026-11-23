package authoring

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/orderkey"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

// importedTables are the tables that an import writes, each through the
// commands of its entity or its own writer, which the section registry's test
// holds to the tables of the registry's sections, so that a table of authoring
// data is registered in both directions (plan item 84, standing checklist).
var importedTables = map[string]bool{
	"project": true, "folder": true, "board": true, "variable": true, "component_type": true, "attribute_def": true,
	"component": true, "attribute_value": true, "node": true, "branch_output": true, "connection": true,
	"expression": true, "validation_mute": true, "comment": true, "play_state": true, "snapshot": true,
	"snapshot_file": true, "asset": true,
}

// archivedSettings are the settings that a draft's project section holds.
type archivedSettings struct {
	DefaultLocale  string               `json:"default_locale"`
	PublishMinRole string               `json:"publish_min_role"`
	Entry          project.ProjectEntry `json:"entry"`
}

// settings decodes the project's settings of the archive.
func (c *archived) settings() (archivedSettings, error) {
	var out archivedSettings
	if c.project == nil || len(c.project.Settings) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(c.project.Settings, &out); err != nil {
		return out, &ImportError{Code: FailContentInvalid, Section: "project", Check: "settings", Detail: err.Error()}
	}
	return out, nil
}

// buildFromRelease fills the draft's sections from the files of the release
// that an archive of a release holds, at the version of package published's
// types: what a release has of the draft, since it keeps no folders, no
// descriptions of components, and none of the other data that only a draft
// holds (FR-DATA-01, plan note N90).
func (c *archived) buildFromRelease(files schemaversion.Files) error {
	decode := func(path string, v any) error {
		data, ok := files[path]
		if !ok {
			return &ImportError{Code: FailReleaseInvalid, Version: c.manifest.Source.Version, Check: "missing_file",
				Detail: path}
		}
		if err := published.Decode(data, v); err != nil {
			return &ImportError{Code: FailReleaseInvalid, Version: c.manifest.Source.Version, Check: "schema",
				Detail: fmt.Sprintf("%s: %v", path, err)}
		}
		return nil
	}
	var m published.Manifest
	if err := decode(published.ManifestPath, &m); err != nil {
		return err
	}
	var set published.Settings
	if err := decode(published.SettingsPath, &set); err != nil {
		return err
	}
	settings := archivedSettings{PublishMinRole: project.PublishEditor}
	if set.Entry != nil {
		settings.Entry = project.ProjectEntry{BoardID: set.Entry.BoardID, NodeID: set.Entry.NodeID}
	}
	doc, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	c.project = &Project{Name: m.Project.Name, Slug: m.Project.Slug, Settings: Value(doc)}
	c.boards, c.nodes, c.outputs, c.connections, c.expressions = nil, nil, nil, nil, nil
	boardOrders := orderkey.Spread(len(m.Boards))
	source := func(owner, id string, field expression.Field, src string) {
		c.expressions = append(c.expressions, Expression{OwnerType: owner, OwnerID: id, Field: string(field), Source: src})
	}
	script := func(owner, id string, field expression.Field, s *published.Script) {
		if s != nil {
			source(owner, id, field, s.Source)
		}
	}
	for i, ib := range m.Boards {
		var f published.Board
		if err := decode(ib.File, &f); err != nil {
			return err
		}
		c.boards = append(c.boards, Board{ID: ib.ID, Order: boardOrders[i], Name: ib.Name, Slug: ib.Slug,
			Color: "none", EntryNodeID: ib.EntryNodeID})
		for _, n := range f.Nodes {
			out, err := c.releaseNode(ib.ID, n, script, source)
			if err != nil {
				return err
			}
			c.nodes = append(c.nodes, out)
		}
		conns := slices.Clone(f.Connections)
		slices.SortStableFunc(conns, func(a, b published.Connection) int {
			return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.FromPort, b.FromPort),
				cmp.Compare(int64(a.Order), int64(b.Order)))
		})
		orders := orderkey.Spread(len(conns))
		for j, cn := range conns {
			out := Connection{ID: cn.ID, BoardID: ib.ID, FromNodeID: cn.From, ToNodeID: cn.To, Order: orders[j]}
			if cn.FromPort != "out" {
				out.FromOutputID = cn.FromPort
			}
			if cn.Label != nil {
				if out.Label, err = textOf(cn.Label.Segments); err != nil {
					return c.releaseText(cn.ID, err)
				}
			}
			if cn.Condition != nil {
				source("connection", cn.ID, expression.Condition, cn.Condition.Source)
			}
			script("connection", cn.ID, expression.OnTraverse, cn.OnTraverse)
			c.connections = append(c.connections, out)
		}
	}
	var variables published.Variables
	if err := decode(published.VariablesPath, &variables); err != nil {
		return err
	}
	varOrders := orderkey.Spread(len(variables.Variables))
	c.variables = make([]Variable, len(variables.Variables))
	for i, v := range variables.Variables {
		def, err := json.Marshal(v.Default)
		if err != nil {
			return err
		}
		c.variables[i] = Variable{ID: v.ID, Scope: v.Scope, BoardID: v.BoardID, Order: varOrders[i], Key: v.Key,
			Type: v.Type, Default: Value(def), Description: v.Description, ResetOnEnter: v.ResetOnEnter}
	}
	var types published.ComponentTypes
	if err := decode(published.ComponentTypesPath, &types); err != nil {
		return err
	}
	c.types = make([]ComponentType, len(types.ComponentTypes))
	for i, t := range types.ComponentTypes {
		orders := orderkey.Spread(len(t.Attributes))
		attrs := make([]AttributeDef, len(t.Attributes))
		for j, a := range t.Attributes {
			def, err := json.Marshal(a.Default)
			if err != nil {
				return err
			}
			attrs[j] = AttributeDef{ID: a.ID, Order: orders[j], Key: a.Key, Name: a.Name, Type: a.Type,
				EnumOptions: a.Options, Default: Value(def), IsRuntime: a.Runtime}
		}
		c.types[i] = ComponentType{ID: t.ID, Key: t.Key, Name: t.Name, Attributes: attrs}
	}
	var components published.Components
	if err := decode(published.ComponentsPath, &components); err != nil {
		return err
	}
	compOrders := orderkey.Spread(len(components.Components))
	c.components = make([]Component, len(components.Components))
	for i, x := range components.Components {
		values := make(map[string]Value, len(x.Attributes))
		for def, v := range x.Attributes {
			raw, err := json.Marshal(v)
			if err != nil {
				return err
			}
			values[def] = Value(raw)
		}
		c.components[i] = Component{ID: x.ID, ComponentTypeID: x.ComponentTypeID, Order: compOrders[i], Key: x.Key,
			Name: x.Name, ImageAssetID: x.Image, Values: values}
	}
	return nil
}

// releaseText is the failure of reading a text of the release's files.
func (c *archived) releaseText(id string, err error) error {
	return &ImportError{Code: FailReleaseInvalid, Version: c.manifest.Source.Version, Check: "text",
		Detail: fmt.Sprintf("%s: %v", id, err)}
}

// releaseNode returns the draft's node of a node of a release's board, and
// adds what its expressions and its outputs are.
func (c *archived) releaseNode(boardID string, n published.Node,
	script func(owner, id string, field expression.Field, s *published.Script),
	source func(owner, id string, field expression.Field, src string)) (Node, error) {
	coord := func(x float64) Decimal { return Decimal(strconv.FormatFloat(x, 'f', 2, 64)) }
	switch n := n.(type) {
	case *published.Element:
		out := Node{ID: n.ID, BoardID: boardID, Type: published.NodeElement, Kind: n.Kind, Title: n.Title,
			SpeakerID: n.Speaker, CoverAssetID: n.Cover, Color: "none", Tags: n.Tags, X: coord(n.Position.X),
			Y: coord(n.Position.Y), IsEnding: n.IsEnding}
		if out.Tags == nil {
			out.Tags = []string{}
		}
		if n.Text != nil {
			var err error
			if out.Text, err = textOf(n.Text.Segments); err != nil {
				return out, c.releaseText(n.ID, err)
			}
		}
		script("node", n.ID, expression.OnEnter, n.OnEnter)
		script("node", n.ID, expression.OnExit, n.OnExit)
		return out, nil
	case *published.Branch:
		out := Node{ID: n.ID, BoardID: boardID, Type: published.NodeBranch, Title: n.Title, Color: "none",
			Tags: []string{}, X: coord(n.Position.X), Y: coord(n.Position.Y)}
		script("node", n.ID, expression.OnEnter, n.OnEnter)
		script("node", n.ID, expression.OnExit, n.OnExit)
		outputs := slices.Clone(n.Outputs)
		slices.SortStableFunc(outputs, func(a, b published.BranchOutput) int {
			return cmp.Compare(int64(a.Order), int64(b.Order))
		})
		orders := orderkey.Spread(len(outputs))
		for j, o := range outputs {
			item := BranchOutput{ID: o.ID, NodeID: n.ID, Kind: o.Kind}
			if o.Kind != published.OutputElse {
				item.Order = orders[j]
				if o.Condition != nil {
					source("branch_output", o.ID, expression.Condition, o.Condition.Source)
				}
			}
			c.outputs = append(c.outputs, item)
		}
		return out, nil
	case *published.Jumper:
		out := Node{ID: n.ID, BoardID: boardID, Type: published.NodeJumper, Title: n.Title, Color: "none",
			Tags: []string{}, X: coord(n.Position.X), Y: coord(n.Position.Y)}
		if n.Target != nil {
			out.TargetBoardID, out.TargetNodeID = n.Target.BoardID, n.Target.NodeID
		}
		return out, nil
	}
	return Node{}, &ImportError{Code: FailReleaseInvalid, Version: c.manifest.Source.Version, Check: "schema",
		Detail: "a node of a kind that the format does not have"}
}

// textOf returns the canonical document of a text's segments.
func textOf(segs richtext.Segments) (Text, error) {
	doc, err := richtext.FromSegments(segs)
	if err != nil || doc == nil {
		return nil, err
	}
	data, err := json.Marshal(*doc)
	if err != nil {
		return nil, err
	}
	return Text(data), nil
}

// parts applies an archive's draft through the commands that make each entity,
// so that it meets their schemas and their Check, and what they check of the
// project (PRD §10.4). Its values are stored ones, which the text rules that a
// value typed in meets may refuse (commands.Spec.CheckNew), as a project keeps
// a value that a newer rule would refuse, and its backup has to restore it. A
// refusal names the section and the entity.
type parts struct {
	ctx context.Context
	tx  *commands.Tx
}

func (p parts) apply(section, id string, typ commands.Type, payload any) error {
	err := p.tx.ApplyPart(p.ctx, typ, payload, nil)
	if err == nil {
		return nil
	}
	if field, message, ok := commands.PartRefusal(err); ok {
		return &ImportError{Code: FailContentInvalid, Section: section, Check: field,
			Detail: fmt.Sprintf("%s: %s %s", id, field, message),
			unique: errors.Is(commands.PartReason(err), commands.ErrIDTaken)}
	}
	return fmt.Errorf("authoring: apply %s of the section %s: %w", id, section, err)
}

// docOf parses a rich text of the archive, as commands read it, or returns nil
// for one that it does not hold.
func docOf(section, id string, t Text) (*richtext.Doc, error) {
	if len(t) == 0 || string(t) == "null" {
		return nil, nil
	}
	doc, err := richtext.Parse(t)
	if err != nil {
		return nil, &ImportError{Code: FailContentInvalid, Section: section, Check: "text",
			Detail: fmt.Sprintf("%s: %v", id, err)}
	}
	return doc, nil
}

// expressionKey names an expression of the archive by its owner and field.
type expressionKey struct{ owner, field string }

// applyDraft creates the draft's entities in an order in which each finds what
// it refers to: folders from the top down, boards, variables, component types
// and their definitions, components, nodes, branch outputs, and connections,
// and then what refers back: jumpers' targets, boards' entry nodes, and the
// project's settings. Each expression's source goes with its owner, and the
// pipeline's derivation compiles it once all of them are in, so the ASTs of an
// archive are never read (FR-DATA-01).
func applyDraft(ctx context.Context, t *commands.Tx, c *archived) error {
	p := parts{ctx: ctx, tx: t}
	sources := make(map[expressionKey]string, len(c.expressions))
	for _, e := range c.expressions {
		k := expressionKey{e.OwnerID, e.Field}
		if _, dup := sources[k]; dup {
			return &ImportError{Code: FailContentInvalid, Section: "expressions", Check: "duplicate",
				Detail: fmt.Sprintf("%s has two %s expressions", e.OwnerID, e.Field)}
		}
		sources[k] = e.Source
	}
	used := make(map[expressionKey]bool, len(sources))
	take := func(id string, f expression.Field) string {
		k := expressionKey{id, string(f)}
		s, ok := sources[k]
		if ok {
			used[k] = true
		}
		return s
	}
	folders, err := foldersInOrder(c.folders)
	if err != nil {
		return err
	}
	for _, f := range folders {
		if err := p.apply("folders", f.ID, board.CreateFolder, board.CreateFolderPayload{ID: f.ID, Kind: f.Kind,
			Name: f.Name, ParentID: f.ParentID, Order: f.Order}); err != nil {
			return err
		}
	}
	for _, b := range c.boards {
		if err := p.apply("boards", b.ID, board.Create, board.CreatePayload{ID: b.ID, Name: b.Name, Slug: b.Slug,
			Description: b.Description, Color: b.Color, FolderID: b.FolderID, Order: b.Order}); err != nil {
			return err
		}
	}
	for _, v := range c.variables {
		payload := variable.CreatePayload{ID: v.ID, Key: v.Key, Type: v.Type, Scope: v.Scope, BoardID: v.BoardID,
			Description: v.Description, ResetOnEnter: v.ResetOnEnter, FolderID: v.FolderID, Order: v.Order}
		if err := decodeValue("variables", v.ID, v.Default, &payload.Default); err != nil {
			return err
		}
		if err := p.apply("variables", v.ID, variable.Create, payload); err != nil {
			return err
		}
	}
	for _, ct := range c.types {
		if err := p.apply("component_types", ct.ID, component.CreateType, component.CreateTypePayload{ID: ct.ID,
			Name: ct.Name, Key: ct.Key, Icon: ct.Icon}); err != nil {
			return err
		}
		for _, a := range ct.Attributes {
			payload := component.CreateAttributePayload{ID: a.ID, ComponentTypeID: ct.ID, Name: a.Name, Key: a.Key,
				Type: a.Type, Options: a.EnumOptions, Runtime: a.IsRuntime, Order: a.Order}
			if err := decodeValue("component_types", a.ID, a.Default, &payload.Default); err != nil {
				return err
			}
			if err := p.apply("component_types", a.ID, component.CreateAttribute, payload); err != nil {
				return err
			}
		}
	}
	for _, x := range c.components {
		payload := component.CreatePayload{ID: x.ID, ComponentTypeID: x.ComponentTypeID, Name: x.Name, Key: x.Key,
			ImageAssetID: x.ImageAssetID, FolderID: x.FolderID, Order: x.Order}
		if payload.Description, err = docOf("components", x.ID, x.Description); err != nil {
			return err
		}
		if len(x.Values) > 0 {
			payload.Attributes = make(map[string]value.Value, len(x.Values))
			for def, raw := range x.Values {
				var v value.Value
				if err := decodeValue("components", x.ID, raw, &v); err != nil {
					return err
				}
				payload.Attributes[def] = v
			}
		}
		if err := p.apply("components", x.ID, component.Create, payload); err != nil {
			return err
		}
	}
	for _, n := range c.nodes {
		payload, err := nodePayload(n)
		if err != nil {
			return err
		}
		payload.OnEnter, payload.OnExit = take(n.ID, expression.OnEnter), take(n.ID, expression.OnExit)
		if err := p.apply("nodes", n.ID, node.Create, payload); err != nil {
			return err
		}
	}
	// Each else after the conditional outputs, which it needs one of: an
	// archive lists outputs by ID, and an else can be older than all that its
	// branch has left.
	for _, elses := range []bool{false, true} {
		for _, o := range c.outputs {
			if (o.Kind == node.OutputElse) != elses {
				continue
			}
			payload := node.CreateOutputPayload{ID: o.ID, NodeID: o.NodeID, Else: elses, Order: o.Order}
			if !elses {
				payload.Condition = take(o.ID, expression.Condition)
			}
			if err := p.apply("branch_outputs", o.ID, node.CreateOutput, payload); err != nil {
				return err
			}
		}
	}
	for _, cn := range c.connections {
		payload := connection.CreatePayload{ID: cn.ID, FromNodeID: cn.FromNodeID, FromPort: cn.FromOutputID,
			ToNodeID: cn.ToNodeID, Condition: take(cn.ID, expression.Condition),
			OnTraverse: take(cn.ID, expression.OnTraverse), Order: cn.Order}
		if payload.Label, err = docOf("connections", cn.ID, cn.Label); err != nil {
			return err
		}
		if err := p.apply("connections", cn.ID, connection.Create, payload); err != nil {
			return err
		}
	}
	for _, e := range c.expressions {
		if !used[expressionKey{e.OwnerID, e.Field}] {
			return &ImportError{Code: FailContentInvalid, Section: "expressions", Check: "owner",
				Detail: fmt.Sprintf("the %s expression of %s has no owner that holds one", e.Field, e.OwnerID)}
		}
	}
	for _, n := range c.nodes {
		if n.TargetBoardID == "" && n.TargetNodeID == "" {
			continue
		}
		if err := p.apply("nodes", n.ID, node.Update, node.UpdatePayload{ID: n.ID,
			Target: &node.NodeTarget{BoardID: n.TargetBoardID, NodeID: n.TargetNodeID}}); err != nil {
			return err
		}
	}
	for _, b := range c.boards {
		if b.EntryNodeID == "" {
			continue
		}
		if err := p.apply("boards", b.ID, node.SetBoardEntry, node.EntryPayload{ID: b.ID,
			NodeID: b.EntryNodeID}); err != nil {
			return err
		}
	}
	set, err := c.settings()
	if err != nil {
		return err
	}
	payload := project.SettingsPayload{}
	apply := false
	if set.PublishMinRole != "" && set.PublishMinRole != project.PublishEditor {
		payload.PublishMinRole, apply = &set.PublishMinRole, true
	}
	if set.Entry != (project.ProjectEntry{}) {
		payload.Entry, apply = &set.Entry, true
	}
	if apply {
		return p.apply("project", c.manifest.Project.ID, project.UpdateSettings, payload)
	}
	return nil
}

// decodeValue reads a stored JSON value into v, the value that its command
// takes.
func decodeValue(section, id string, raw Value, v any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return &ImportError{Code: FailContentInvalid, Section: section, Check: "value",
			Detail: fmt.Sprintf("%s: %v", id, err)}
	}
	return nil
}

// nodePayload returns the command's payload of a node of the archive, without
// its jumper's target, which may be a node that is created after it, and its
// expressions.
func nodePayload(n Node) (node.CreatePayload, error) {
	p := node.CreatePayload{ID: n.ID, BoardID: n.BoardID, Type: n.Type, Kind: n.Kind, Title: n.Title,
		SpeakerComponentID: n.SpeakerID, CoverAssetID: n.CoverAssetID, Color: n.Color, Tags: n.Tags,
		IsEnding: n.IsEnding}
	var err error
	if p.Text, err = docOf("nodes", n.ID, n.Text); err != nil {
		return p, err
	}
	if err := decodeDecimal(n.ID, n.X, &p.Position.X); err != nil {
		return p, err
	}
	if err := decodeDecimal(n.ID, n.Y, &p.Position.Y); err != nil {
		return p, err
	}
	if n.Width != nil || n.Height != nil {
		p.Size = &node.NodeSize{}
		if n.Width != nil {
			if err := decodeDecimal(n.ID, *n.Width, &p.Size.Width); err != nil {
				return p, err
			}
		}
		if n.Height != nil {
			if err := decodeDecimal(n.ID, *n.Height, &p.Size.Height); err != nil {
				return p, err
			}
		}
	}
	return p, nil
}

// decodeDecimal reads a number of a node's geometry into what its command
// takes.
func decodeDecimal(id string, d Decimal, v any) error {
	if err := json.Unmarshal([]byte(d), v); err != nil {
		return &ImportError{Code: FailContentInvalid, Section: "nodes", Check: "geometry",
			Detail: fmt.Sprintf("%s: %v", id, err)}
	}
	return nil
}

// foldersInOrder returns the folders with each after its parent, and refuses
// a parent that the archive does not hold, or a cycle. It climbs each chain
// with a list of its own rather than by recursion, as an archive can hold a
// chain of millions, which would overflow a goroutine's stack, and that ends
// the process. How deep a folder may be is the command's to refuse.
func foldersInOrder(list []Folder) ([]Folder, error) {
	const (
		climbing = 1
		placed   = 2
	)
	byID := make(map[string]int, len(list))
	for i, f := range list {
		byID[f.ID] = i
	}
	out := make([]Folder, 0, len(list))
	state := make([]uint8, len(list))
	var chain []int
	for start := range list {
		chain = chain[:0]
		for at := start; state[at] != placed; {
			f := list[at]
			if state[at] == climbing {
				return nil, &ImportError{Code: FailContentInvalid, Section: "folders", Check: "cycle",
					Detail: fmt.Sprintf("the folder %s is inside itself", f.ID)}
			}
			state[at] = climbing
			chain = append(chain, at)
			if f.ParentID == "" {
				break
			}
			parent, ok := byID[f.ParentID]
			if !ok {
				return nil, &ImportError{Code: FailContentInvalid, Section: "folders", Check: "parent",
					Detail: fmt.Sprintf("the folder %s has a parent that the archive does not hold", f.ID)}
			}
			at = parent
		}
		for _, at := range slices.Backward(chain) {
			state[at] = placed
			out = append(out, list[at])
		}
	}
	return out, nil
}

// errNotImported marks an import that the archive refused, as opposed to one
// that the server failed at, for the job.
func isRefusal(err error) (*ImportError, bool) {
	var refused *ImportError
	if errors.As(err, &refused) {
		return refused, true
	}
	return nil, false
}
