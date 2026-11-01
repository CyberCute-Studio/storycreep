package clipboard

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/orderkey"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// Paste pastes a clipboard onto a board of the batch's project (PRD
// FR-EDIT-07): it creates its nodes, their outputs, and the connections
// between them, with the IDs that the client makes, as node.create,
// branch_output.create, and connection.create do (commands.Tx.ApplyPart),
// with what they refer to resolved at the board (package doc). Deleting the
// nodes that it created to the trash, with their outputs and connections, as
// one node.delete_many, undoes it.
const Paste commands.Type = "clipboard.paste"

// pastePayload is Paste's payload.
type pastePayload struct {
	BoardID   string            `json:"board_id" doc:"The ID of the board that the clipboard is pasted onto, which is not in the project's trash"`
	Offset    node.NodePosition `json:"offset" doc:"What is added to each pasted node's position, as the clipboard holds it, so that the nodes keep their places relative to each other (PRD FR-EDIT-07 AC2): {\"x\": 0, \"y\": 0} pastes them where they were copied from. A node whose position is then beyond the bounds of a position is answered with 422 validation_failed at its position."`
	Clipboard Clipboard         `json:"clipboard" doc:"The clipboard (PRD FR-EDIT-07 AC3), as POST /api/v1/projects/{project_id}/clipboard/copy answers with it, of this project or of another of the workspace's, which a server answers with 422 validation_failed where it is not one that it reads"`
	IDs       map[string]string `json:"ids" doc:"The new ID of each node, output, and connection of the clipboard, by its ID in the clipboard: an ID of the same kind, of a UUIDv7 that the client makes, which no entity of the instance has (PRD §5.2), as nd_, bo_, and cn_ IDs that a node.create, a branch_output.create, and a connection.create would take, each once"`
}

// Register registers in r the type of command that pastes a clipboard. It
// needs the role "Edit nodes, connections, text, expressions" (§5.6), as the
// commands of what it creates do, and project:write of an API token. r has
// node.create, node.update, branch_output.create, connection.create, and
// node.delete_many registered once the registry makes a pipeline
// (node.Register, connection.Register).
func Register(r *commands.Registry) {
	commands.Register(r, commands.Spec[pastePayload]{
		Type: Paste, Capability: authz.EditContent, Scope: scope.ProjectWrite,
		Touches: []ids.Kind{ids.Node, ids.Board, ids.Component, ids.BranchOutput, ids.Connection},
		Check:   checkPaste,
		Inverse: func(_ context.Context, _ *commands.Tx, p pastePayload) ([]commands.Step, error) {
			nodes := make([]string, len(p.Clipboard.Nodes))
			for i, n := range p.Clipboard.Nodes {
				nodes[i] = p.IDs[n.ID]
			}
			return []commands.Step{{Type: node.DeleteMany, Payload: node.ManyPayload{IDs: nodes}}}, nil
		},
		Apply: applyPaste,
	})
}

// checkPaste returns what is wrong with Paste's payload p that its schema
// cannot say: its board's ID, its offset, its clipboard (Check), at
// clipboard, and its new IDs, which are one of each node, output, and
// connection of the clipboard, of their kinds, of UUIDv7s, and no two alike.
func checkPaste(p pastePayload) input.FieldErrors {
	errs := entityID(ids.Board, "board_id", p.BoardID)
	errs = append(errs, node.CheckPosition("offset", p.Offset)...)
	for _, e := range Check(p.Clipboard) {
		e.Field = clipboardField(e.Field)
		errs = append(errs, e)
	}
	if len(errs) > 0 {
		return errs
	}
	want := map[string]ids.Kind{}
	var order []string
	for _, n := range p.Clipboard.Nodes {
		want[n.ID] = ids.Node
		order = append(order, n.ID)
		for _, o := range n.Outputs {
			want[o.ID] = ids.BranchOutput
			order = append(order, o.ID)
		}
	}
	for _, c := range p.Clipboard.Connections {
		want[c.ID] = ids.Connection
		order = append(order, c.ID)
	}
	first := map[ids.UUID]string{}
	for _, old := range order {
		at := "ids." + old
		id, ok := p.IDs[old]
		if !ok {
			errs = append(errs, input.FieldError{Field: "ids", Message: "lacks the new ID of " + old})
			continue
		}
		u, err := ids.Decode(want[old], id)
		switch {
		case err != nil:
			errs = append(errs, input.FieldError{Field: at, Message: fmt.Sprintf("is not a %s's ID: %v", want[old],
				err)})
		case !u.IsV7():
			errs = append(errs, input.FieldError{Field: at, Message: "is not the ID of a UUIDv7, which the ID of " +
				"every entity is"})
		default:
			if other, dup := first[u]; dup {
				errs = append(errs, input.FieldError{Field: at, Message: "is the new ID of " + other + " too"})
			}
			first[u] = old
		}
	}
	for _, old := range slices.Sorted(maps.Keys(p.IDs)) {
		if _, ok := want[old]; !ok {
			errs = append(errs, input.FieldError{Field: "ids." + old, Message: "is the new ID of nothing that the " +
				"clipboard holds: its key is no ID of its nodes, outputs, or connections"})
		}
	}
	return errs
}

// applyPaste is Paste's Apply.
func applyPaste(ctx context.Context, tx *commands.Tx, p pastePayload) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	b, _ := ids.Decode(ids.Board, p.BoardID)
	board := nullUUID(b)
	switch _, err := q.Board(ctx, queries.BoardParams{WorkspaceID: ws, ProjectID: project, ID: board}); {
	case errors.Is(err, pgx.ErrNoRows):
		return commands.Refuse("board_id", "is not a board of the project, or is one in its trash", nil)
	case err != nil:
		return fmt.Errorf("clipboard: read the board: %w", err)
	}
	c := p.Clipboard
	d, err := loadDestination(ctx, q, ws, project, board, c)
	if err != nil {
		return err
	}
	pl, err := d.plan(ctx, q, c, p.IDs)
	if err != nil {
		return err
	}
	// A jumper that targets a node of the clipboard, which may be created
	// after it, gets its target once every node is.
	var targets []int
	for i, n := range c.Nodes {
		at := fmt.Sprintf("clipboard.nodes[%d]", i)
		payload, later, err := pl.node(at, n, p.BoardID, p.Offset)
		if err != nil {
			return err
		}
		if later {
			targets = append(targets, i)
		}
		if err := tx.ApplyPart(ctx, node.Create, payload, pl.fields(at, n.ID)); err != nil {
			return err
		}
	}
	for _, i := range targets {
		n, at := c.Nodes[i], fmt.Sprintf("clipboard.nodes[%d]", i)
		if err := tx.ApplyPart(ctx, node.Update, node.UpdatePayload{ID: p.IDs[n.ID], Target: &node.NodeTarget{
			BoardID: p.BoardID, NodeID: pl.moves.Nodes[canonical(ids.Node, n.Target.NodeID)]}},
			pl.fields(at, n.ID)); err != nil {
			return err
		}
	}
	for i, n := range c.Nodes {
		// The branch is new, and its outputs are the ones that the paste
		// creates, which it places itself, in their order, so that creating
		// each reads none of the others (orderkey.Spread).
		orders := orderkey.Spread(len(n.Outputs))
		for j, o := range n.Outputs {
			at := fmt.Sprintf("clipboard.nodes[%d].outputs[%d]", i, j)
			payload := node.CreateOutputPayload{ID: p.IDs[o.ID], NodeID: p.IDs[n.ID], Else: o.Else}
			if !o.Else {
				payload.Order = orders[j]
				if payload.Condition, err = pl.source(at+".condition", o.Condition, expr.ContextCondition); err != nil {
					return err
				}
			}
			fields := pl.fields(at, o.ID)
			if err := tx.ApplyPart(ctx, node.CreateOutput, payload, func(f string) string {
				if f == "node_id" {
					return fmt.Sprintf("clipboard.nodes[%d].id", i)
				}
				return fields(f)
			}); err != nil {
				return err
			}
		}
	}
	orders := portOrders(c.Connections)
	for i, cn := range c.Connections {
		at := fmt.Sprintf("clipboard.connections[%d]", i)
		payload, err := pl.connection(at, cn, orders[i])
		if err != nil {
			return err
		}
		if err := tx.ApplyPart(ctx, connection.Create, payload, pl.fields(at, cn.ID)); err != nil {
			return err
		}
	}
	return nil
}

// plan is how a paste writes what it pastes of a clipboard at a board: what
// the clipboard's declarations resolve to there, and the moves of the
// expressions' names, the projects that they are read in, the new IDs, the
// jumpers' targets outside the clipboard and the speakers that the board's
// project has, and the covers that are its assets.
type plan struct {
	d        *destination
	r        resolution
	from     *expr.Project
	fromAt   string
	moves    expr.Moves
	ids      map[string]string
	targets  map[string]bool
	speakers map[string]bool
	covers   map[string]bool
}

// plan returns the plan of a paste of the clipboard c, with the new IDs ids,
// at the board d.
func (d *destination) plan(ctx context.Context, q *queries.Queries, c Clipboard, newIDs map[string]string) (*plan,
	error) {
	from, err := c.names()
	if err != nil {
		return nil, fmt.Errorf("clipboard: %w", err)
	}
	pl := &plan{d: d, r: d.resolve(c), from: from, fromAt: canonical(ids.Board, c.BoardID), ids: newIDs}
	nodes := map[string]string{}
	for _, n := range c.Nodes {
		nodes[canonical(ids.Node, n.ID)] = canonical(ids.Node, newIDs[n.ID])
	}
	pl.moves = expr.Moves{Variables: pl.r.variables, Components: pl.r.components, Attributes: pl.r.attributes,
		Nodes: nodes}
	// A node reference to a node outside the clipboard is written as it is,
	// whatever the project has, so the targets alone are read.
	if pl.targets, err = d.targets(ctx, q, c.outsideTargets()); err != nil {
		return nil, err
	}
	if pl.speakers, err = d.speakers(ctx, q, c); err != nil {
		return nil, err
	}
	if pl.covers, err = d.covers(ctx, q, c); err != nil {
		return nil, err
	}
	return pl, nil
}

// fields returns the field of the paste's payload that each field of the
// payload of the part that creates the clipboard's entity old, at at, comes
// from: its id from its new ID's, at ids, its order, which the paste gives it
// (orderkey.Spread), from the entity, whose place among its port's or its
// branch's the clipboard holds, as no field of it, and every other from the
// entity's own field of the same name, which is where the part's problem is.
func (pl *plan) fields(at, old string) func(string) string {
	return func(f string) string {
		switch f {
		case "id":
			return "ids." + old
		case "board_id":
			return "board_id"
		case "", "order":
			return at
		}
		return at + "." + f
	}
}

// node returns the payload of the node.create of the clipboard's node n, at
// at, onto the board boardID, its position moved by offset, and whether n is
// a jumper whose target is a node of the clipboard, which the payload leaves
// out, as the target's copy may be created after the jumper, and which a
// node.update gives it once every node is (applyPaste).
func (pl *plan) node(at string, n ClipboardNode, boardID string, offset node.NodePosition) (node.CreatePayload,
	bool, error) {
	position, errs := node.Shifted(n.Position, offset)
	if len(errs) > 0 {
		return node.CreatePayload{}, false, commands.Refuse(at+".position."+errs[0].Field, errs[0].Message, nil)
	}
	p := node.CreatePayload{ID: pl.ids[n.ID], BoardID: boardID, Type: n.Type, Kind: n.Kind, Title: n.Title,
		Color: n.Color, Tags: n.Tags, Position: position, Size: n.Size, IsEnding: n.IsEnding}
	if len(n.Text) > 0 {
		text, err := pl.text(n.Text)
		if err != nil {
			return node.CreatePayload{}, false, err
		}
		p.Text = text
	}
	// A speaker that resolves to nothing, or that the clipboard does not
	// declare, as one in the trash where it was copied, is kept where the
	// project has it, in its trash or not, as a text's reference is.
	if id := canonical(ids.Component, n.SpeakerComponentID); id != "" {
		switch to := pl.r.components[id]; {
		case to != "":
			p.SpeakerComponentID = to
		case pl.speakers[id]:
			p.SpeakerComponentID = id
		}
	}
	if id := n.CoverAssetID; id != "" && pl.covers[canonical(ids.Asset, id)] {
		p.CoverAssetID = id
	}
	later := false
	if t := n.Target; t != nil && t.NodeID != "" {
		_, later = pl.moves.Nodes[canonical(ids.Node, t.NodeID)]
		if !later && pl.targets[outside{id: t.NodeID, board: t.BoardID}.key()] {
			p.Target = &node.NodeTarget{BoardID: t.BoardID, NodeID: t.NodeID}
		}
	}
	for _, s := range []struct {
		source, name string
		field        *string
	}{{n.OnEnter, "on_enter", &p.OnEnter}, {n.OnExit, "on_exit", &p.OnExit}} {
		if s.source == "" {
			continue
		}
		source, err := pl.source(at+"."+s.name, s.source, expr.ContextScript)
		if err != nil {
			return node.CreatePayload{}, false, err
		}
		*s.field = source
	}
	return p, later, nil
}

// portOrders returns the place of each of the clipboard's connections cs among
// those that leave its port, in the order of cs, which holds each port's
// connections in their order: the ports are new, and their connections the
// ones that the paste creates, which it places itself, so that creating each
// reads none of the others (orderkey.Spread).
func portOrders(cs []ClipboardConnection) []string {
	port := func(c ClipboardConnection) string {
		if c.FromPort == "" || c.FromPort == portOut {
			return canonical(ids.Node, c.FromNodeID) + " " + portOut
		}
		return canonical(ids.Node, c.FromNodeID) + " " + canonical(ids.BranchOutput, c.FromPort)
	}
	counts := map[string]int{}
	for _, c := range cs {
		counts[port(c)]++
	}
	keys := make(map[string][]string, len(counts))
	for p, n := range counts {
		keys[p] = orderkey.Spread(n)
	}
	out := make([]string, len(cs))
	for i, c := range cs {
		p := port(c)
		out[i], keys[p] = keys[p][0], keys[p][1:]
	}
	return out
}

// connection returns the payload of the connection.create of the
// clipboard's connection c, at at, at the place order among the connections
// that leave its port.
func (pl *plan) connection(at string, c ClipboardConnection, order string) (connection.CreatePayload, error) {
	p := connection.CreatePayload{ID: pl.ids[c.ID], FromNodeID: pl.ids[c.FromNodeID], ToNodeID: pl.ids[c.ToNodeID],
		Order: order}
	if c.FromPort != "" && c.FromPort != portOut {
		p.FromPort = pl.ids[c.FromPort]
	}
	if len(c.Label) > 0 {
		label, err := pl.text(c.Label)
		if err != nil {
			return connection.CreatePayload{}, err
		}
		p.Label = label
	}
	for _, s := range []struct {
		source, name string
		field        *string
		ctx          expr.Context
	}{{c.Condition, "condition", &p.Condition, expr.ContextCondition},
		{c.OnTraverse, "on_traverse", &p.OnTraverse, expr.ContextScript}} {
		if s.source == "" {
			continue
		}
		source, err := pl.source(at+"."+s.name, s.source, s.ctx)
		if err != nil {
			return connection.CreatePayload{}, err
		}
		*s.field = source
	}
	return p, nil
}

// source returns the source of an expression of the clipboard, at the field
// at of the paste's payload, of the context ctx, as it is pasted: with each
// name that names one of its declarations naming what that resolves to, and
// each node reference to one of its nodes the node's copy (expr.Relocate). It
// refuses the batch, at at, if that source is longer than an expression's
// source may be, as a name of what resolves to nothing, which is written
// explicitly, may make it, before the part that writes it refuses it at a
// field that is not.
func (pl *plan) source(at, source string, ctx expr.Context) (string, error) {
	out, err := expr.Relocate(source, ctx, pl.from, pl.fromAt, pl.d.names, pl.d.boardID, pl.moves)
	if err != nil {
		return "", fmt.Errorf("clipboard: %w", err)
	}
	if expression.CheckSource(out) != "" {
		return "", commands.Refuse(at, fmt.Sprintf("would have %d characters once the paste writes its names as "+
			"they name what they resolve to at the board, those of what resolves to nothing explicitly, more than "+
			"the %d of an expression's source: shorten it, or create first what its names name, which the report "+
			"lists", utf8.RuneCountInString(out), expression.MaxSourceLength), nil)
	}
	return out, nil
}

// text returns the text t of the clipboard, which Check took, as it is
// pasted: with each value reference and mention of one of its declarations
// naming what that resolves to, and the others as they are.
func (pl *plan) text(t Text) (*richtext.Doc, error) {
	doc, err := richtext.Parse(t)
	if err != nil {
		return nil, fmt.Errorf("clipboard: a text that Check took: %w", err)
	}
	for in := range doc.Inlines() {
		switch in := in.(type) {
		case *richtext.Mention:
			if to := pl.r.components[in.Component]; to != "" {
				in.Component = to
			}
		case *richtext.Value:
			if in.Attribute == "" {
				if to := pl.r.variables[in.Ref]; to != "" {
					in.Ref = to
				}
				continue
			}
			to, attribute := pl.r.components[in.Ref], pl.r.attributes[expr.AttributeRef(in.Ref, in.Attribute)]
			if to != "" && attribute != "" {
				in.Ref, in.Attribute = to, attribute
			}
		}
	}
	return doc, nil
}

// nullUUID returns id as a uuid column.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
