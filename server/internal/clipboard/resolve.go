package clipboard

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// ClipboardReport is what a clipboard's content names that resolves to nothing
// at a board that it would be pasted onto (PRD FR-EDIT-07 AC1): each as the
// clipboard declares it, with what creating one in its place takes, once for
// each that a client would create, which it may create in the batch of the
// paste, before it, so that the paste resolves to it.
type ClipboardReport struct {
	Variables  []ClipboardVariable `json:"variables" nullable:"false" doc:"The variables that the clipboard declares that resolve to none at the board: neither the same variable, global or of the board, nor one of its key and scope, a global one or one of the board. Each is as the clipboard declares it, which a variable.create of the board's project, of the board for a board's variable, takes, with an ID of its own, and is listed once for its key and scope, the first that the clipboard declares, as the clipboard may declare variables of one key of several boards, which a text names, which each resolve to the one of that key of the board that it is pasted onto."`
	Components []MissingComponent  `json:"components" nullable:"false" doc:"The components that the clipboard declares that resolve to none in the board's project: neither the same component nor one of its key, of those that are not in the project's trash, each with the project's component type that a component.create in its place may take: the same type as its own, or else the one of its type's key, if the project has one. One without a type is created with a component_type.create of its type, as the clipboard declares it, once for each type, and with an attribute.create of that type of each attribute definition that the clipboard declares of it, once for each key."`
	Attributes []MissingAttribute  `json:"attributes" nullable:"false" doc:"The attribute definitions that the clipboard declares of a component, which the project's component type that a paste resolves it by does not have, neither the same definition nor one of its key: the type of the component that it resolves to, or the one that components lists for a component that resolves to none. Each is listed with that type, once for the type and its key, the first that the clipboard declares, as the components of a type share its definitions, which an attribute.create of the type, with an ID of its own, takes."`
	Speakers   []string            `json:"speakers" nullable:"false" doc:"The speakers of the clipboard's elements that it does not declare, as it declares no component that was in the trash where it was copied, that the board's project does not have, in its trash or not, each once: the elements are pasted without them. A speaker that the clipboard declares that resolves to none, which components lists, is pasted as it is where the project has it, in its trash or not, and else left out too."`
	Nodes      []string            `json:"nodes" nullable:"false" doc:"The nodes outside the clipboard that its jumpers target or its expressions name by node references, each once, that the board's project does not have, in its trash or not, or not on the board that a target names: a jumper whose target this lists is pasted without one, and a node reference names nothing"`
	Assets     []string            `json:"assets" nullable:"false" doc:"The cover images of the clipboard's elements that are not the board's project's assets, as those of another project are not, each once: the elements are pasted without them"`
}

// MissingComponent is a component of a clipboard that resolves to none
// (ClipboardReport).
type MissingComponent struct {
	Component       ClipboardComponent `json:"component" doc:"The component, as the clipboard declares it"`
	ComponentTypeID string             `json:"component_type_id,omitempty" doc:"The ID of the project's component type, not in its trash, which a component created in its place may be of: the component's own type, and else the one of the key of the component's type, if the project has one"`
}

// MissingAttribute is an attribute definition of a component of a clipboard
// that the component type that a paste resolves it by does not have: the type
// of the component that the clipboard's component resolves to, or the one
// that one created in its place may be of (ClipboardReport).
type MissingAttribute struct {
	ComponentTypeID string             `json:"component_type_id" doc:"The ID of the project's component type, not in its trash, that does not have the definition, which an attribute.create in its place is of"`
	Attribute       ClipboardAttribute `json:"attribute" doc:"The attribute definition, as the clipboard declares it"`
}

// destination is what a paste resolves a clipboard's declarations against: a
// board of a project, and what is there that isn't in the trash.
type destination struct {
	ws, project, board pgtype.UUID
	// names is what the board's expressions can name, or nothing, where the
	// clipboard declares nothing (loadDestination).
	names *expr.Project
	// boardID is the board's ID, in its canonical spelling.
	boardID string
	// globals and ofBoard are the IDs of the project's global variables and
	// of the board's, by their keys, and reach those of either by their IDs.
	globals, ofBoard map[string]string
	reach            map[string]bool
	// components are the IDs of the project's components by their keys, and
	// definitions the attribute definitions of each component's type, which
	// the components of the type share, by the component's ID.
	components  map[string]string
	definitions map[string][]expr.Attribute
	// types are the IDs of the project's component types by their keys, and
	// typeIDs those IDs; componentTypes are the IDs of the components' types
	// by the components' IDs, and ofType the attribute definitions of each
	// type that has some, by its ID.
	types          map[string]string
	typeIDs        map[string]bool
	componentTypes map[string]string
	ofType         map[string][]expr.Attribute
}

// loadDestination returns the board board of the project, of the workspace
// ws, as a paste of the clipboard c reads it, which q reads in a transaction
// of the workspace: what c's declarations resolve by, which it reads only
// where c declares something, as an expression of one that declares nothing
// names nothing there but nodes (expr.Relocate). The board is one of the
// project, not in its trash, which the caller has checked.
func loadDestination(ctx context.Context, q *queries.Queries, ws, project, board pgtype.UUID, c Clipboard) (
	*destination, error) {
	d := &destination{ws: ws, project: project, board: board, boardID: ids.Encode(ids.Board, board.Bytes),
		names: &expr.Project{}, globals: map[string]string{}, ofBoard: map[string]string{}, reach: map[string]bool{},
		components: map[string]string{}, definitions: map[string][]expr.Attribute{}, types: map[string]string{},
		typeIDs: map[string]bool{}}
	if len(c.Variables) == 0 && len(c.Components) == 0 {
		return d, nil
	}
	live, err := expression.Live(ctx, q, ws, project, nil)
	if err != nil {
		return nil, err
	}
	d.names, d.componentTypes, d.ofType = live.Project, live.ComponentTypes, live.Types
	for _, v := range live.Variables {
		switch v.Board {
		case "":
			d.globals[v.Key], d.reach[v.ID] = v.ID, true
		case d.boardID:
			d.ofBoard[v.Key], d.reach[v.ID] = v.ID, true
		}
	}
	for _, c := range live.Components {
		d.components[c.Key], d.definitions[c.ID] = c.ID, c.Attributes
	}
	types, err := q.ClipboardComponentTypes(ctx, queries.ClipboardComponentTypesParams{WorkspaceID: ws,
		ProjectID: project})
	if err != nil {
		return nil, fmt.Errorf("clipboard: read the project's component types: %w", err)
	}
	for _, t := range types {
		id := ids.Encode(ids.ComponentType, t.ID.Bytes)
		d.types[t.Key], d.typeIDs[id] = id, true
	}
	return d, nil
}

// resolution is what a clipboard's declarations resolve to at a board: the
// ID of the variable, the component, or the attribute definition, of the
// board's project, or "" for none, of each variable and component by its ID
// in the clipboard, and of each attribute definition by the IDs of the
// clipboard's component that declares it and of itself (expr.AttributeRef),
// each in its canonical spelling, since the components of a type share its
// definitions, but may resolve to components of different types, or one to
// none.
type resolution struct {
	variables, components, attributes map[string]string
}

// resolve returns what the declarations of the clipboard c resolve to at the
// board d (package doc): a variable, the same variable where the board
// reaches it, global or of the board, and else the one of its key and scope,
// a global one or one of the board; a component, the same component, and else
// the one of its key; and an attribute definition of a component that
// resolves, the same definition where it is of the type of the component that
// it resolves to, and else the one of its key of that type, of each component
// that declares it apart. Each that is in the project's trash is no entity
// that it resolves to.
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
		if _, ok := d.definitions[id]; !ok {
			to = d.components[k.Key]
		}
		r.components[id] = to
		// A component that resolves to none has no definitions here.
		definitions := d.definitions[to]
		for _, a := range k.Attributes {
			aid := canonical(ids.AttributeDef, a.ID)
			r.attributes[expr.AttributeRef(id, aid)] = definitionOf(definitions, aid, a.Key)
		}
	}
	return r
}

// definitionOf returns the ID of the attribute definition, among definitions,
// those of a component's type, that a declared definition whose ID is id and
// whose key is key resolves to: the same definition, and else the one of its
// key, or "" for none.
func definitionOf(definitions []expr.Attribute, id, key string) string {
	byKey := ""
	for _, a := range definitions {
		switch {
		case a.ID == id:
			return id
		case a.Key == key:
			byKey = a.ID
		}
	}
	return byKey
}

// typeOf returns the ID of the project's component type, not in its trash,
// that a component created in the place of one of the type typ, as a
// clipboard declares it, may be of: the same type, and else the one of its
// key, or "" for none.
func (d *destination) typeOf(typ ClipboardComponentType) string {
	if id := canonical(ids.ComponentType, typ.ID); d.typeIDs[id] {
		return id
	}
	return d.types[typ.Key]
}

// report returns what of the clipboard c's declarations resolves to nothing
// as r says, each once for what a client would create in its place, with the
// speakers that it does not declare, the nodes that its jumpers target, or
// that its expressions name, outside it, that the board's project lacks, and
// the covers that are not its assets (ClipboardReport), as speakers, targets,
// and covers read them.
func (d *destination) report(ctx context.Context, q *queries.Queries, c Clipboard, r resolution) (ClipboardReport,
	error) {
	out := ClipboardReport{Variables: []ClipboardVariable{}, Components: []MissingComponent{},
		Attributes: []MissingAttribute{}, Speakers: []string{}, Nodes: []string{}, Assets: []string{}}
	// What resolves to nothing is listed once for what a client creates in
	// its place: a variable once for its scope and key, and an attribute
	// definition once for the type that lacks it and its key, since a scope,
	// and a type, has one of a key.
	places := map[string]bool{}
	for _, v := range c.Variables {
		if place := v.Scope + " " + v.Key; r.variables[canonical(ids.Variable, v.ID)] == "" && !places[place] {
			places[place] = true
			out.Variables = append(out.Variables, v)
		}
	}
	for _, k := range c.Components {
		to := r.components[canonical(ids.Component, k.ID)]
		typ := d.componentTypes[to]
		if to == "" {
			typ = d.typeOf(k.Type)
			out.Components = append(out.Components, MissingComponent{Component: k, ComponentTypeID: typ})
		}
		// A component created in the place of one without a type is of a
		// type created with it, with each definition that it declares.
		if typ == "" {
			continue
		}
		for _, a := range k.Attributes {
			place := typ + " " + a.Key
			if definitionOf(d.ofType[typ], canonical(ids.AttributeDef, a.ID), a.Key) == "" && !places[place] {
				places[place] = true
				out.Attributes = append(out.Attributes, MissingAttribute{ComponentTypeID: typ, Attribute: a})
			}
		}
	}
	speakers, err := d.speakers(ctx, q, c)
	if err != nil {
		return ClipboardReport{}, err
	}
	// A speaker, a node, and an asset are listed once for their IDs in their
	// canonical spelling, as an ID is read without regard to case, which a
	// clipboard that a client wrote may have spelled two ways.
	listed := map[string]bool{}
	for _, n := range c.Nodes {
		id := n.SpeakerComponentID
		if id == "" {
			continue
		}
		if u := canonical(ids.Component, id); !listed[u] && !speakers[u] {
			if _, declared := r.components[u]; !declared {
				listed[u] = true
				out.Speakers = append(out.Speakers, id)
			}
		}
	}
	named, err := c.outsiders()
	if err != nil {
		return ClipboardReport{}, err
	}
	targets, err := d.targets(ctx, q, named)
	if err != nil {
		return ClipboardReport{}, err
	}
	for _, n := range named {
		if u := canonical(ids.Node, n.id); !targets[n.key()] && !listed[u] {
			listed[u] = true
			out.Nodes = append(out.Nodes, n.id)
		}
	}
	covers, err := d.covers(ctx, q, c)
	if err != nil {
		return ClipboardReport{}, err
	}
	for _, n := range c.Nodes {
		if id, u := n.CoverAssetID, canonical(ids.Asset, n.CoverAssetID); id != "" && !covers[u] && !listed[u] {
			listed[u] = true
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

// outsiders returns the nodes outside the clipboard that its content names,
// in its order: its jumpers' targets (outsideTargets), and then the nodes
// that its expressions name (nodeReferences), whose sources it parses once.
func (c Clipboard) outsiders() ([]outside, error) {
	refs, err := c.nodeReferences()
	if err != nil {
		return nil, err
	}
	return append(c.outsideTargets(), refs...), nil
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

// targets returns which of the nodes outside a clipboard that its content
// names, named (Clipboard.outsiders), the board's project has, in its trash or
// not, each known by its ID and, for a jumper's target, the board that the
// target names it on, which it is on (outside.key), and for a node reference
// by its ID and "@" alone.
func (d *destination) targets(ctx context.Context, q *queries.Queries, named []outside) (map[string]bool, error) {
	var list []pgtype.UUID
	for _, n := range named {
		if u, err := ids.Decode(ids.Node, n.id); err == nil {
			list = append(list, nullUUID(u))
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
// board's project's assets, by their IDs, in their canonical spelling, as
// node.create takes them, read in one query.
func (d *destination) covers(ctx context.Context, q *queries.Queries, c Clipboard) (map[string]bool, error) {
	of := make([]string, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		of = append(of, n.CoverAssetID)
	}
	return held(ids.Asset, of, func(list []pgtype.UUID) ([]pgtype.UUID, error) {
		rows, err := q.ClipboardCovers(ctx, queries.ClipboardCoversParams{WorkspaceID: d.ws, ProjectID: d.project,
			Ids: list})
		if err != nil {
			return nil, fmt.Errorf("clipboard: read the covers: %w", err)
		}
		return rows, nil
	})
}

// speakers returns which of the speakers of the clipboard c's elements are
// the board's project's components, in its trash or not, by their IDs, in
// their canonical spelling, as node.create takes them, read in one query.
func (d *destination) speakers(ctx context.Context, q *queries.Queries, c Clipboard) (map[string]bool, error) {
	of := make([]string, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		of = append(of, n.SpeakerComponentID)
	}
	return held(ids.Component, of, func(list []pgtype.UUID) ([]pgtype.UUID, error) {
		rows, err := q.ClipboardSpeakers(ctx, queries.ClipboardSpeakersParams{WorkspaceID: d.ws,
			ProjectID: d.project, Ids: list})
		if err != nil {
			return nil, fmt.Errorf("clipboard: read the speakers: %w", err)
		}
		return rows, nil
	})
}

// held returns which of the IDs of, of the kind, read returns the UUIDs of,
// by their IDs in their canonical spelling, each of which it reads once,
// leaving out "" and an ID that is not one of the kind (uuidsOf), and reading
// nothing for none.
func held(kind ids.Kind, of []string, read func(list []pgtype.UUID) ([]pgtype.UUID, error)) (map[string]bool,
	error) {
	list := uuidsOf(kind, slices.Values(of))
	out := map[string]bool{}
	if len(list) == 0 {
		return out, nil
	}
	rows, err := read(list)
	if err != nil {
		return nil, err
	}
	for _, u := range rows {
		out[ids.Encode(kind, u.Bytes)] = true
	}
	return out, nil
}
