package clipboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// Options configure a Service.
type Options struct {
	// Projects authorises the reads (project.Service.ReadSnapshot).
	Projects *project.Service
}

// Service copies a project's nodes as a clipboard, and reports what of one
// resolves to nothing at a board of a project.
type Service struct {
	db       project.SnapshotDB
	projects *project.Service
}

// New returns a Service that reads db, with opts. It panics if opts.Projects
// is nil.
func New(db project.SnapshotDB, opts Options) *Service {
	if opts.Projects == nil {
		panic("clipboard: a Service needs the projects' service that authorises its reads")
	}
	return &Service{db: db, projects: opts.Projects}
}

// Copy returns the clipboard of the nodes of the project projectID whose IDs
// are nodeIDs, as the API writes them, at least one and at most MaxNodes,
// each once, of one board, which are not in the trash, themselves or with
// their board (PRD FR-EDIT-07): the nodes, in the order of their IDs, with
// their outputs that are not in the trash, the connections between them that
// are not either, in the order of their ports and of each port's choices, and
// a declaration of each variable, component, and attribute definition that is
// not in the trash that their texts, expressions, and speakers name, read as
// of one moment. It returns the errors of project.Service.Get first, then the
// problems of nodeIDs, as an input.FieldErrors at node_ids[i], or at node_ids
// for a selection whose clipboard would pass a clipboard's bounds (MaxBytes
// and the others), which a paste could not take.
func (s *Service) Copy(ctx context.Context, actor commands.Actor, projectID ids.UUID, nodeIDs []string) (Clipboard,
	error) {
	var out Clipboard
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		var err error
		out, err = copyNodes(ctx, queries.New(tx), nullUUID(workspaceID), nullUUID(projectID), nodeIDs)
		return err
	})
	return out, err
}

// copyNodes is Copy's read, by q, in a transaction of the workspace ws.
func copyNodes(ctx context.Context, q *queries.Queries, ws, projectID pgtype.UUID, nodeIDs []string) (Clipboard,
	error) {
	switch {
	case len(nodeIDs) == 0:
		return Clipboard{}, input.FieldErrors{{Field: "node_ids", Message: "holds no node, where a clipboard " +
			"holds at least one"}}
	case len(nodeIDs) > MaxNodes:
		return Clipboard{}, input.FieldErrors{{Field: "node_ids", Message: fmt.Sprintf("holds more than %d nodes",
			MaxNodes)}}
	}
	var problems input.FieldErrors
	list := make([]pgtype.UUID, 0, len(nodeIDs))
	at := map[ids.UUID]int{}
	for i, id := range nodeIDs {
		u, err := ids.Decode(ids.Node, id)
		switch {
		case err != nil:
			problems = append(problems, input.FieldError{Field: fmt.Sprintf("node_ids[%d]", i),
				Message: fmt.Sprintf("is not a node's ID: %v", err)})
			continue
		}
		if j, dup := at[u]; dup {
			problems = append(problems, input.FieldError{Field: fmt.Sprintf("node_ids[%d]", i),
				Message: fmt.Sprintf("is the ID of node_ids[%d] too", j)})
			continue
		}
		at[u] = i
		list = append(list, nullUUID(u))
	}
	if len(problems) > 0 {
		return Clipboard{}, problems
	}
	nodes, err := node.Live(ctx, q, ws, projectID, list)
	if err != nil {
		return Clipboard{}, err
	}
	found := map[ids.UUID]node.Node{}
	for _, n := range nodes {
		u, _ := ids.Decode(ids.Node, n.ID)
		found[u] = n
	}
	board := found[list[0].Bytes].BoardID
	for i, id := range list {
		n, ok := found[id.Bytes]
		switch {
		case !ok:
			problems = append(problems, input.FieldError{Field: fmt.Sprintf("node_ids[%d]", i),
				Message: "is not a node of the project, or is one in its trash"})
		case board != "" && n.BoardID != board:
			problems = append(problems, input.FieldError{Field: fmt.Sprintf("node_ids[%d]", i),
				Message: "is a node of another board than node_ids[0], which a clipboard holds the nodes of one " +
					"board of"})
		}
	}
	if len(problems) > 0 {
		return Clipboard{}, problems
	}
	connections, err := connection.Among(ctx, q, ws, projectID, list)
	if err != nil {
		return Clipboard{}, err
	}
	c := Clipboard{Format: Format, Version: Version, ProjectID: ids.Encode(ids.Project, projectID.Bytes),
		BoardID: nodes[0].BoardID, Nodes: make([]ClipboardNode, len(nodes)),
		Connections: make([]ClipboardConnection, len(connections)), Variables: []ClipboardVariable{},
		Components: []ClipboardComponent{}}
	for i, n := range nodes {
		if c.Nodes[i], err = clipboardNode(n); err != nil {
			return Clipboard{}, err
		}
	}
	for i, cn := range connections {
		if c.Connections[i], err = clipboardConnection(cn); err != nil {
			return Clipboard{}, err
		}
	}
	if err := declare(ctx, q, ws, projectID, &c); err != nil {
		return Clipboard{}, err
	}
	place := func(j int) int {
		u, _ := ids.Decode(ids.Node, c.Nodes[j].ID)
		return at[u]
	}
	if problems := c.bounds(place); len(problems) > 0 {
		return Clipboard{}, problems
	}
	return c, nil
}

// bounds returns what of the copy c passes a clipboard's bounds, which its
// schema says too, so that a paste would refuse it, each at node_ids, or at
// node_ids[i] of a branch of more outputs than a clipboard holds of one,
// c.Nodes[j], whose place in node_ids is place(j): more connections between
// its nodes, more variables or components declared, or more attribute
// definitions declared of one component, than a clipboard holds, or more than
// MaxBytes bytes of JSON, with the new IDs that a paste of it gives
// (pasteBytes).
func (c Clipboard) bounds(place func(j int) int) input.FieldErrors {
	var errs input.FieldErrors
	add := func(at, msg string) { errs = append(errs, input.FieldError{Field: at, Message: msg}) }
	for j, n := range c.Nodes {
		if len(n.Outputs) > MaxOutputs {
			add(fmt.Sprintf("node_ids[%d]", place(j)), fmt.Sprintf("is a branch of %d outputs, more than the %d "+
				"that a clipboard holds of one", len(n.Outputs), MaxOutputs))
		}
	}
	for _, b := range []struct {
		what   string
		n, max int
	}{{"connections between them", len(c.Connections), MaxConnections},
		{"variables that their content names", len(c.Variables), MaxVariables},
		{"components that their content names", len(c.Components), MaxComponents}} {
		if b.n > b.max {
			add("node_ids", fmt.Sprintf("are nodes of %d %s, more than the %d that a clipboard holds", b.n, b.what,
				b.max))
		}
	}
	for _, k := range c.Components {
		if len(k.Attributes) > MaxAttributes {
			add("node_ids", fmt.Sprintf("are nodes whose content names %d attributes of the component %s, more "+
				"than the %d that a clipboard holds of one", len(k.Attributes), k.Key, MaxAttributes))
		}
	}
	if len(errs) > 0 {
		return errs
	}
	if n, err := c.pasteBytes(); err == nil && n > MaxBytes {
		add("node_ids", fmt.Sprintf("are nodes whose clipboard, with the new IDs that a paste of it gives, is %d "+
			"bytes of JSON, more than the %d that a paste takes: copy fewer of them", n, MaxBytes))
	}
	return errs
}

// pasteBytes returns how many bytes of JSON a paste's command holds of the
// clipboard c (MaxBytes): c, as encoding/json writes it, which it counts
// without keeping it, and the new ID of each of its nodes, outputs, and
// connections by its ID, which the paste's ids hold, each as long as the ID
// that it replaces, an ID of the same kind, which c holds in its canonical
// spelling, as Copy writes it.
func (c Clipboard) pasteBytes() (int, error) {
	var n counter
	if err := json.NewEncoder(&n).Encode(c); err != nil {
		return 0, err
	}
	// Encode ends the JSON with a line feed, which Marshal does not write.
	total := int(n) - 1
	entry := func(id string) { total += 2*len(id) + len(`"":"",`) }
	for _, nd := range c.Nodes {
		entry(nd.ID)
		for _, o := range nd.Outputs {
			entry(o.ID)
		}
	}
	for _, cn := range c.Connections {
		entry(cn.ID)
	}
	return total, nil
}

// counter is a writer that counts the bytes written to it, and keeps none of
// them.
type counter int

// Write counts p.
func (n *counter) Write(p []byte) (int, error) {
	*n += counter(len(p))
	return len(p), nil
}

// clipboardNode returns the node n as a clipboard holds it.
func clipboardNode(n node.Node) (ClipboardNode, error) {
	out := ClipboardNode{ID: n.ID, Type: n.Type, Kind: n.Kind, Title: n.Title, SpeakerComponentID: n.SpeakerComponentID,
		CoverAssetID: n.CoverAssetID, Color: n.Color, Tags: n.Tags, Position: n.Position, Size: n.Size,
		IsEnding: n.IsEnding, Target: n.Target, OnEnter: sourceOf(n.OnEnter), OnExit: sourceOf(n.OnExit)}
	if len(out.Tags) == 0 {
		out.Tags = nil
	}
	if n.Text != nil {
		var err error
		if out.Text, err = textOf(*n.Text); err != nil {
			return ClipboardNode{}, err
		}
	}
	for _, o := range n.Outputs {
		out.Outputs = append(out.Outputs, ClipboardOutput{ID: o.ID, Else: o.Kind == "else",
			Condition: sourceOf(o.Condition)})
	}
	return out, nil
}

// clipboardConnection returns the connection c as a clipboard holds it.
func clipboardConnection(c connection.Connection) (ClipboardConnection, error) {
	label, err := textOf(c.Label)
	if err != nil {
		return ClipboardConnection{}, err
	}
	out := ClipboardConnection{ID: c.ID, FromNodeID: c.FromNodeID, FromPort: c.FromPort, ToNodeID: c.ToNodeID,
		Label: label, Condition: sourceOf(c.Condition), OnTraverse: sourceOf(c.OnTraverse)}
	if out.FromPort == portOut {
		out.FromPort = ""
	}
	return out, nil
}

// sourceOf returns the source of the expression e, or "" for none.
func sourceOf(e *expression.Expression) string {
	if e == nil {
		return ""
	}
	return e.Source
}

// declare gives the clipboard c the declarations of the variables, the
// components, and the attribute definitions of the project, not in its trash,
// that its texts, its expressions' names at its board, and its speakers name.
func declare(ctx context.Context, q *queries.Queries, ws, projectID pgtype.UUID, c *Clipboard) error {
	live, err := expression.Live(ctx, q, ws, projectID, nil)
	if err != nil {
		return err
	}
	variables, components := map[string]bool{}, map[string]bool{}
	attributes := map[string]bool{} // by their components' IDs and their own (expr.AttributeRef)
	use := func(variable, component, attribute string) {
		switch {
		case variable != "":
			variables[variable] = true
		case component != "":
			components[component] = true
			if attribute != "" {
				attributes[expr.AttributeRef(component, attribute)] = true
			}
		}
	}
	if err := c.eachSource(func(_ string, source string, ctx expr.Context) error {
		named, err := expr.Names(source, ctx, live.Project, c.BoardID)
		for _, n := range named {
			use(n.Variable, n.Component, n.Attribute)
		}
		return err
	}); err != nil {
		return err
	}
	texts := make([]Text, 0, len(c.Nodes)+len(c.Connections))
	for _, n := range c.Nodes {
		use("", n.SpeakerComponentID, "")
		texts = append(texts, n.Text)
	}
	for _, cn := range c.Connections {
		texts = append(texts, cn.Label)
	}
	for _, t := range texts {
		if len(t) == 0 {
			continue
		}
		doc, err := richtext.Parse(t)
		if err != nil {
			return fmt.Errorf("clipboard: a text of the project: %w", err)
		}
		for in := range doc.Inlines() {
			switch in := in.(type) {
			case *richtext.Mention:
				use("", in.Component, "")
			case *richtext.Value:
				if in.Attribute == "" {
					use(in.Ref, "", "")
				} else {
					use("", in.Ref, in.Attribute)
				}
			}
		}
	}
	if c.Variables, err = declareVariables(ctx, q, ws, projectID, variables); err != nil {
		return err
	}
	c.Components, err = declareComponents(ctx, q, ws, projectID, live.Components, components, attributes)
	return err
}

// uuidsOf returns the UUIDs of the IDs of the kind, keys of set, in their
// order, leaving out those that are no such IDs.
func uuidsOf(kind ids.Kind, set map[string]bool) []pgtype.UUID {
	var out []pgtype.UUID
	for id := range set {
		if u, err := ids.Decode(kind, id); err == nil {
			out = append(out, nullUUID(u))
		}
	}
	slices.SortFunc(out, func(a, b pgtype.UUID) int { return bytes.Compare(a.Bytes[:], b.Bytes[:]) })
	return out
}

// declareVariables returns the declarations of the project's variables, not
// in its trash, whose IDs are keys of used, in the order of their IDs.
func declareVariables(ctx context.Context, q *queries.Queries, ws, projectID pgtype.UUID, used map[string]bool) (
	[]ClipboardVariable, error) {
	out := []ClipboardVariable{}
	list := uuidsOf(ids.Variable, used)
	if len(list) == 0 {
		return out, nil
	}
	rows, err := q.ClipboardVariables(ctx, queries.ClipboardVariablesParams{WorkspaceID: ws, ProjectID: projectID,
		Ids: list})
	if err != nil {
		return nil, fmt.Errorf("clipboard: read the variables: %w", err)
	}
	for _, r := range rows {
		v := ClipboardVariable{ID: ids.Encode(ids.Variable, r.ID.Bytes), Key: r.Key, Scope: r.Scope, Type: r.Type,
			Description: r.Description, ResetOnEnter: r.ResetOnEnter}
		if err := json.Unmarshal(r.DefaultValue, &v.Default); err != nil {
			return nil, fmt.Errorf("clipboard: the default of a variable: %w", err)
		}
		// The default as variable.create writes it, as jsonb need not have
		// kept it.
		if def, problem := v.Default.Read(r.Type, value.Checks{}); problem == "" {
			v.Default = def
		}
		if r.BoardID.Valid {
			v.BoardID = ids.Encode(ids.Board, r.BoardID.Bytes)
		}
		out = append(out, v)
	}
	return out, nil
}

// declareComponents returns the declarations of the project's components,
// not in its trash, whose IDs are keys of used, in the order of their IDs,
// each with the attribute definitions of its type, not in the trash, as live,
// the project's components that are not in its trash, holds them, whose IDs,
// with the component's, are keys of attributes (expr.AttributeRef), in their
// order, with their names and defaults (declareAttributes).
func declareComponents(ctx context.Context, q *queries.Queries, ws, projectID pgtype.UUID, live []expr.Component,
	used, attributes map[string]bool) ([]ClipboardComponent, error) {
	out := []ClipboardComponent{}
	list := uuidsOf(ids.Component, used)
	if len(list) == 0 {
		return out, nil
	}
	rows, err := q.ClipboardComponents(ctx, queries.ClipboardComponentsParams{WorkspaceID: ws,
		ProjectID: projectID, Ids: list})
	if err != nil {
		return nil, fmt.Errorf("clipboard: read the components: %w", err)
	}
	definitions := make(map[string][]expr.Attribute, len(list))
	named := map[string]bool{}
	for _, k := range live {
		if !used[k.ID] {
			continue
		}
		definitions[k.ID] = k.Attributes
		for _, a := range k.Attributes {
			if attributes[expr.AttributeRef(k.ID, a.ID)] {
				named[a.ID] = true
			}
		}
	}
	declared, err := declareAttributes(ctx, q, ws, projectID, named)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		k := ClipboardComponent{ID: ids.Encode(ids.Component, r.ID.Bytes), Key: r.Key, Name: r.Name,
			Type: ClipboardComponentType{ID: ids.Encode(ids.ComponentType, r.TypeID.Bytes), Key: r.TypeKey,
				Name: r.TypeName},
			Attributes: []ClipboardAttribute{}}
		for _, a := range definitions[k.ID] {
			if !attributes[expr.AttributeRef(k.ID, a.ID)] {
				continue
			}
			d := ClipboardAttribute{ID: a.ID, Key: a.Key, Name: declared[a.ID].Name, Type: a.Type,
				Runtime: a.Runtime, Options: a.Options, Default: declared[a.ID].Default}
			// The default as attribute.create writes it, as jsonb need not
			// have kept it.
			if def, problem := d.Default.Read(d.Type, value.Checks{}, d.Options...); problem == "" {
				d.Default = def
			}
			k.Attributes = append(k.Attributes, d)
		}
		out = append(out, k)
	}
	return out, nil
}

// declareAttributes returns the names and the defaults of the project's
// attribute definitions, not in its trash, whose IDs are keys of named, by
// their IDs, as a clipboard declares them, with what creating one in their
// place takes that an expression does not read.
func declareAttributes(ctx context.Context, q *queries.Queries, ws, projectID pgtype.UUID,
	named map[string]bool) (map[string]ClipboardAttribute, error) {
	out := map[string]ClipboardAttribute{}
	list := uuidsOf(ids.AttributeDef, named)
	if len(list) == 0 {
		return out, nil
	}
	rows, err := q.ClipboardAttributes(ctx, queries.ClipboardAttributesParams{WorkspaceID: ws,
		ProjectID: projectID, Ids: list})
	if err != nil {
		return nil, fmt.Errorf("clipboard: read the attribute definitions: %w", err)
	}
	for _, r := range rows {
		a := ClipboardAttribute{Name: r.Name}
		if err := json.Unmarshal(r.DefaultValue, &a.Default); err != nil {
			return nil, fmt.Errorf("clipboard: the default of an attribute definition: %w", err)
		}
		out[ids.Encode(ids.AttributeDef, r.ID.Bytes)] = a
	}
	return out, nil
}

// Resolve returns what of the clipboard c resolves to nothing at the board
// boardID of the project projectID (ClipboardReport), as a paste of it there
// would resolve it, read as of one moment. It returns the errors of
// project.Service.Get first, then the problems of c (Check), each at clipboard
// and its field, and of boardID, at board_id, as an input.FieldErrors: one that
// is no board of the project, or one in its trash.
func (s *Service) Resolve(ctx context.Context, actor commands.Actor, projectID ids.UUID, boardID string,
	c Clipboard) (ClipboardReport, error) {
	var out ClipboardReport
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		problems := entityID(ids.Board, "board_id", boardID)
		for _, e := range Check(c) {
			e.Field = clipboardField(e.Field)
			problems = append(problems, e)
		}
		if len(problems) > 0 {
			return problems
		}
		q := queries.New(tx)
		ws, project := nullUUID(workspaceID), nullUUID(projectID)
		b, _ := ids.Decode(ids.Board, boardID)
		if _, err := q.Board(ctx, queries.BoardParams{WorkspaceID: ws, ProjectID: project,
			ID: nullUUID(b)}); errors.Is(err, pgx.ErrNoRows) {
			return input.FieldErrors{{Field: "board_id", Message: "is not a board of the project, or is one in " +
				"its trash"}}
		} else if err != nil {
			return fmt.Errorf("clipboard: read the board: %w", err)
		}
		d, err := loadDestination(ctx, q, ws, project, nullUUID(b))
		if err != nil {
			return err
		}
		out, err = d.report(ctx, q, c, d.resolve(c))
		return err
	})
	return out, err
}
