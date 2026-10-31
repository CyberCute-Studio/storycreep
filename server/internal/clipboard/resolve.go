package clipboard

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// ClipboardReport is what a clipboard's content names that resolves to nothing at a
// board that it would be pasted onto (PRD FR-EDIT-07 AC1): each as the
// clipboard declares it, with what creating one in its place takes, which a
// client may create in the batch of the paste, before it, so that the paste
// resolves to it.
type ClipboardReport struct {
	Variables  []ClipboardVariable `json:"variables" nullable:"false" doc:"The variables that the clipboard declares that resolve to none at the board: neither the same variable, global or of the board, nor one of its key and scope, a global one or one of the board. Each is as the clipboard declares it, which a variable.create of the board's project, of the board for a board's variable, takes, with an ID of its own."`
	Components []MissingComponent  `json:"components" nullable:"false" doc:"The components that the clipboard declares that resolve to none in the board's project: neither the same component nor one of its key, of those that are not in the project's trash, each with the project's component type of the key of its type's, if it has one, which a component.create takes"`
	Attributes []MissingAttribute  `json:"attributes" nullable:"false" doc:"The attribute definitions that the clipboard declares of a component that resolves, which the type of the component that it resolves to does not have, neither the same definition nor one of its key, each with that component"`
	Nodes      []string            `json:"nodes" nullable:"false" doc:"The nodes outside the clipboard that its jumpers target or its expressions name by node references, each once, that the board's project does not have, in its trash or not, or not on the board that a target names: a jumper whose target this lists is pasted without one, and a node reference names nothing"`
	Assets     []string            `json:"assets" nullable:"false" doc:"The cover images of the clipboard's elements that are not the board's project's assets, as those of another project are not, each once: the elements are pasted without them"`
}

// MissingComponent is a component of a clipboard that resolves to none
// (ClipboardReport).
type MissingComponent struct {
	Component       ClipboardComponent `json:"component" doc:"The component, as the clipboard declares it"`
	ComponentTypeID string             `json:"component_type_id,omitempty" doc:"The ID of the project's component type, not in its trash, of the key of the component's type, which a component created in its place may be of, if the project has one"`
}

// MissingAttribute is an attribute definition of a component of a clipboard
// that the component that it resolves to does not have (ClipboardReport).
type MissingAttribute struct {
	ComponentID string             `json:"component_id" doc:"The ID of the project's component that the clipboard's component resolves to"`
	Attribute   ClipboardAttribute `json:"attribute" doc:"The attribute definition, as the clipboard declares it"`
}

// destination is what a paste resolves a clipboard's declarations against: a
// board of a project, and what is there that isn't in the trash.
type destination struct {
	ws, project, board pgtype.UUID
	// names is what the board's expressions can name.
	names *expr.Project
	// boardID is the board's ID, in its canonical spelling.
	boardID string
	// globals and ofBoard are the IDs of the project's global variables and
	// of the board's, by their keys, and reach those of either by their IDs.
	globals, ofBoard map[string]string
	reach            map[string]bool
	// components are the IDs of the project's components by their keys, and
	// typeOf each component's type's, by its ID.
	components map[string]string
	typeOf     map[string]string
	// attributes are the IDs of each type's attribute definitions, by the
	// type's ID and then their keys, and attributeType each definition's
	// type's, by its ID. types are the types' IDs, by their keys.
	attributes    map[string]map[string]string
	attributeType map[string]string
	types         map[string]string
}

// loadDestination returns the board board of the project, of the workspace
// ws, as a paste reads it, which q reads in a transaction of the workspace.
// The board is one of the project, not in its trash, which the caller has
// checked.
func loadDestination(ctx context.Context, q *queries.Queries, ws, project, board pgtype.UUID) (*destination, error) {
	d := &destination{ws: ws, project: project, board: board, boardID: ids.Encode(ids.Board, board.Bytes),
		globals: map[string]string{}, ofBoard: map[string]string{}, reach: map[string]bool{},
		components: map[string]string{}, typeOf: map[string]string{}, attributes: map[string]map[string]string{},
		attributeType: map[string]string{}, types: map[string]string{}}
	var err error
	if d.names, err = expression.Live(ctx, q, ws, project, nil); err != nil {
		return nil, err
	}
	variables, err := q.ExpressionVariables(ctx, queries.ExpressionVariablesParams{WorkspaceID: ws, ProjectID: project})
	if err != nil {
		return nil, fmt.Errorf("clipboard: read the project's variables: %w", err)
	}
	for _, v := range variables {
		id := ids.Encode(ids.Variable, v.ID.Bytes)
		switch {
		case !v.BoardID.Valid:
			d.globals[v.Key], d.reach[id] = id, true
		case v.BoardID == board:
			d.ofBoard[v.Key], d.reach[id] = id, true
		}
	}
	components, err := q.ExpressionComponents(ctx, queries.ExpressionComponentsParams{WorkspaceID: ws,
		ProjectID: project})
	if err != nil {
		return nil, fmt.Errorf("clipboard: read the project's components: %w", err)
	}
	for _, c := range components {
		id := ids.Encode(ids.Component, c.ID.Bytes)
		d.components[c.Key], d.typeOf[id] = id, ids.Encode(ids.ComponentType, c.ComponentTypeID.Bytes)
	}
	attributes, err := q.ExpressionAttributes(ctx, queries.ExpressionAttributesParams{WorkspaceID: ws,
		ProjectID: project})
	if err != nil {
		return nil, fmt.Errorf("clipboard: read the project's attribute definitions: %w", err)
	}
	for _, a := range attributes {
		id, typ := ids.Encode(ids.AttributeDef, a.ID.Bytes), ids.Encode(ids.ComponentType, a.ComponentTypeID.Bytes)
		if d.attributes[typ] == nil {
			d.attributes[typ] = map[string]string{}
		}
		d.attributes[typ][a.Key], d.attributeType[id] = id, typ
	}
	types, err := q.ClipboardComponentTypes(ctx, queries.ClipboardComponentTypesParams{WorkspaceID: ws,
		ProjectID: project})
	if err != nil {
		return nil, fmt.Errorf("clipboard: read the project's component types: %w", err)
	}
	for _, t := range types {
		d.types[t.Key] = ids.Encode(ids.ComponentType, t.ID.Bytes)
	}
	return d, nil
}

// resolution is what a clipboard's declarations resolve to at a board, each
// by its ID in the clipboard, in its canonical spelling: the ID of the
// variable, the component, or the attribute definition, of the board's
// project, or "" for none.
type resolution struct {
	variables, components, attributes map[string]string
}

// resolve returns what the declarations of the clipboard c resolve to at the
// board d (package doc): a variable, the same variable where the board
// reaches it, global or of the board, and else the one of its key and scope,
// a global one or one of the board; a component, the same component, and else
// the one of its key; and an attribute definition of a component that
// resolves, the same definition where it is of the type of the component that
// it resolves to, and else the one of its key of that type. Each that is in
// the project's trash is no entity that it resolves to.
func (d *destination) resolve(c Clipboard) resolution {
	r := resolution{variables: map[string]string{}, components: map[string]string{},
		attributes: map[string]string{}}
	for _, v := range c.Variables {
		id := canonical(ids.Variable, v.ID)
		switch {
		case d.reach[id]:
			r.variables[id] = id
		case v.Scope == "board":
			r.variables[id] = d.ofBoard[v.Key]
		default:
			r.variables[id] = d.globals[v.Key]
		}
	}
	for _, k := range c.Components {
		id := canonical(ids.Component, k.ID)
		to := id
		if _, ok := d.typeOf[id]; !ok {
			to = d.components[k.Key]
		}
		r.components[id] = to
		typ := d.typeOf[to]
		for _, a := range k.Attributes {
			aid := canonical(ids.AttributeDef, a.ID)
			switch {
			case to == "":
				r.attributes[aid] = ""
			case d.attributeType[aid] == typ:
				r.attributes[aid] = aid
			default:
				r.attributes[aid] = d.attributes[typ][a.Key]
			}
		}
	}
	return r
}

// report returns what of the clipboard c's declarations resolves to nothing
// as r says, with the nodes that its jumpers target, or that its
// expressions name, outside it, that the board's project lacks, and the
// covers that are not its assets (ClipboardReport), as nodesOutside and missingAssets
// read them.
func (d *destination) report(ctx context.Context, q *queries.Queries, c Clipboard, r resolution) (ClipboardReport, error) {
	out := ClipboardReport{Variables: []ClipboardVariable{}, Components: []MissingComponent{}, Attributes: []MissingAttribute{},
		Nodes: []string{}, Assets: []string{}}
	for _, v := range c.Variables {
		if r.variables[canonical(ids.Variable, v.ID)] == "" {
			out.Variables = append(out.Variables, v)
		}
	}
	for _, k := range c.Components {
		to := r.components[canonical(ids.Component, k.ID)]
		if to == "" {
			out.Components = append(out.Components, MissingComponent{Component: k,
				ComponentTypeID: d.types[k.Type.Key]})
			continue
		}
		for _, a := range k.Attributes {
			if r.attributes[canonical(ids.AttributeDef, a.ID)] == "" {
				out.Attributes = append(out.Attributes, MissingAttribute{ComponentID: to, Attribute: a})
			}
		}
	}
	targets, err := d.targets(ctx, q, c)
	if err != nil {
		return ClipboardReport{}, err
	}
	refs, err := c.nodeReferences()
	if err != nil {
		return ClipboardReport{}, err
	}
	listed := map[string]bool{}
	for _, n := range append(c.outsideTargets(), refs...) {
		if !targets[n.key()] && !listed[n.id] {
			listed[n.id] = true
			out.Nodes = append(out.Nodes, n.id)
		}
	}
	covers, err := d.covers(ctx, q, c)
	if err != nil {
		return ClipboardReport{}, err
	}
	for _, n := range c.Nodes {
		if id := n.CoverAssetID; id != "" && !covers[canonical(ids.Asset, id)] && !listed[id] {
			listed[id] = true
			out.Assets = append(out.Assets, id)
		}
	}
	return out, nil
}

// outside is a node outside a clipboard that its content names: its ID, as
// the clipboard writes it, and the board that a jumper's target names it on,
// or "" for a node reference, which names a node of any board.
type outside struct {
	id, board string
}

// key returns what the node is known by among those that targets found.
func (o outside) key() string {
	return canonical(ids.Node, o.id) + "@" + canonical(ids.Board, o.board)
}

// outsideTargets returns the targets of the clipboard's jumpers that are not
// its nodes, in its order.
func (c Clipboard) outsideTargets() []outside {
	in := c.nodeIDs()
	var out []outside
	for _, n := range c.Nodes {
		if t := n.Target; t != nil && t.NodeID != "" && !in[canonical(ids.Node, t.NodeID)] {
			out = append(out, outside{id: t.NodeID, board: t.BoardID})
		}
	}
	return out
}

// nodeReferences returns the nodes that the clipboard's expressions name by
// node references that are not its nodes, in its order.
func (c Clipboard) nodeReferences() ([]outside, error) {
	in := c.nodeIDs()
	var out []outside
	err := c.eachSource(func(_ string, source string, ctx expr.Context) error {
		named, err := expr.Names(source, ctx, &expr.Project{}, "")
		for _, n := range named {
			if n.Node != "" && !in[n.Node] {
				out = append(out, outside{id: n.Node})
			}
		}
		return err
	})
	return out, err
}

// nodeIDs returns the IDs of the clipboard's nodes, in their canonical
// spelling.
func (c Clipboard) nodeIDs() map[string]bool {
	in := make(map[string]bool, len(c.Nodes))
	for _, n := range c.Nodes {
		in[canonical(ids.Node, n.ID)] = true
	}
	return in
}

// eachSource calls f with each source of the clipboard's expressions, with
// where it is in the clipboard and its context, in the clipboard's order, and
// returns the first error that f returns.
func (c Clipboard) eachSource(f func(at, source string, ctx expr.Context) error) error {
	call := func(at, source string, field expression.Field) error {
		if source == "" {
			return nil
		}
		return f(at, source, field.Context())
	}
	for i, n := range c.Nodes {
		at := fmt.Sprintf("nodes[%d]", i)
		if err := call(at+".on_enter", n.OnEnter, expression.OnEnter); err != nil {
			return err
		}
		if err := call(at+".on_exit", n.OnExit, expression.OnExit); err != nil {
			return err
		}
		for j, o := range n.Outputs {
			if err := call(fmt.Sprintf("%s.outputs[%d].condition", at, j), o.Condition,
				expression.Condition); err != nil {
				return err
			}
		}
	}
	for i, cn := range c.Connections {
		at := fmt.Sprintf("connections[%d]", i)
		if err := call(at+".condition", cn.Condition, expression.Condition); err != nil {
			return err
		}
		if err := call(at+".on_traverse", cn.OnTraverse, expression.OnTraverse); err != nil {
			return err
		}
	}
	return nil
}

// targets returns which of the nodes outside the clipboard c that its content
// names the board's project has, in its trash or not, each known by its ID
// and, for a jumper's target, the board that the target names it on, which
// it is on (outside.key), and for a node reference by its ID and "@" alone.
func (d *destination) targets(ctx context.Context, q *queries.Queries, c Clipboard) (map[string]bool, error) {
	refs, err := c.nodeReferences()
	if err != nil {
		return nil, err
	}
	var list []pgtype.UUID
	for _, n := range append(c.outsideTargets(), refs...) {
		if u, err := ids.Decode(ids.Node, n.id); err == nil {
			list = append(list, pgtype.UUID{Bytes: u, Valid: true})
		}
	}
	out := map[string]bool{}
	if len(list) == 0 {
		return out, nil
	}
	rows, err := q.NodesByID(ctx, queries.NodesByIDParams{WorkspaceID: d.ws, ProjectID: d.project, Ids: list})
	if err != nil {
		return nil, fmt.Errorf("clipboard: read the nodes outside the clipboard: %w", err)
	}
	for _, row := range rows {
		id := ids.Encode(ids.Node, row.Node.ID.Bytes)
		out[id+"@"+ids.Encode(ids.Board, row.Node.BoardID.Bytes)] = true
		out[id+"@"] = true
	}
	return out, nil
}

// covers returns which of the covers of the clipboard c's elements are the
// board's project's assets, by their IDs, in their canonical spelling.
func (d *destination) covers(ctx context.Context, q *queries.Queries, c Clipboard) (map[string]bool, error) {
	out := map[string]bool{}
	for _, n := range c.Nodes {
		if n.CoverAssetID == "" {
			continue
		}
		u, err := ids.Decode(ids.Asset, n.CoverAssetID)
		if err != nil {
			continue
		}
		id := ids.Encode(ids.Asset, u)
		if _, seen := out[id]; seen {
			continue
		}
		_, err = q.Asset(ctx, queries.AssetParams{WorkspaceID: d.ws, ProjectID: d.project,
			ID: pgtype.UUID{Bytes: u, Valid: true}})
		switch {
		case err == nil:
			out[id] = true
		case isNoRows(err):
			out[id] = false
		default:
			return nil, fmt.Errorf("clipboard: read a cover: %w", err)
		}
	}
	return out, nil
}
