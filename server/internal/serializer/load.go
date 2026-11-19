package serializer

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// Load reads the draft of the project projectID of the workspace ws in tx, a
// transaction of the workspace that reads as of one moment, as a snapshot's
// build reads it (PRD §7.3, FR-SNAP-01 AC7), so that its reads agree with
// one another: one read of each kind of the project's rows that are not in
// its trash, with what the published format holds of each, each text read
// with richtext.Parse and each AST with expr.Decode, as they are stored.
func Load(ctx context.Context, tx pgx.Tx, ws, projectID ids.UUID) (*Draft, error) {
	q := queries.New(tx)
	w, p := pgUUID(ws), pgUUID(projectID)
	proj, err := q.SerializerProject(ctx, queries.SerializerProjectParams{WorkspaceID: w, ID: p})
	if err != nil {
		return nil, fmt.Errorf("serializer: read the project: %w", err)
	}
	entryBoard, entryNode, err := project.EntryOf(proj.Settings)
	if err != nil {
		return nil, fmt.Errorf("serializer: %w", err)
	}
	d := &Draft{Project: Project{ID: ids.Encode(ids.Project, projectID), Name: proj.Name, Slug: proj.Slug,
		EntryBoard: encodeID(ids.Board, entryBoard), EntryNode: encodeID(ids.Node, entryNode)}}
	if err := d.loadBoards(ctx, q, w, p); err != nil {
		return nil, err
	}
	if err := d.loadExpressions(ctx, q, w, p); err != nil {
		return nil, err
	}
	if err := d.loadDeclarations(ctx, q, w, p); err != nil {
		return nil, err
	}
	return d, nil
}

// loadBoards reads the boards, the nodes, the outputs, and the connections.
func (d *Draft) loadBoards(ctx context.Context, q *queries.Queries, w, p pgtype.UUID) error {
	boards, err := q.SerializerBoards(ctx, queries.SerializerBoardsParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return fmt.Errorf("serializer: read the boards: %w", err)
	}
	for _, b := range boards {
		d.Boards = append(d.Boards, Board{ID: ids.Encode(ids.Board, b.ID.Bytes), Slug: b.Slug, Name: b.Name,
			EntryNode: encode(ids.Node, b.EntryNodeID)})
	}
	nodes, err := q.SerializerNodes(ctx, queries.SerializerNodesParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return fmt.Errorf("serializer: read the nodes: %w", err)
	}
	for _, n := range nodes {
		node := Node{ID: ids.Encode(ids.Node, n.ID.Bytes), Board: ids.Encode(ids.Board, n.BoardID.Bytes),
			Type: n.Type, Kind: n.Kind, Title: n.Title, Speaker: encode(ids.Component, n.SpeakerComponentID),
			Cover: encode(ids.Asset, n.CoverAssetID), Tags: n.Tags, X: n.X, Y: n.Y, IsEnding: n.IsEnding,
			TargetBoard: encode(ids.Board, n.TargetBoardID), TargetNode: encode(ids.Node, n.TargetNodeID)}
		if n.Text != nil {
			if node.Text, err = richtext.Parse(n.Text); err != nil {
				return fmt.Errorf("serializer: read the text of %s: %w", node.ID, err)
			}
		}
		d.Nodes = append(d.Nodes, node)
	}
	outputs, err := q.SerializerOutputs(ctx, queries.SerializerOutputsParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return fmt.Errorf("serializer: read the branches' outputs: %w", err)
	}
	for _, o := range outputs {
		d.Outputs = append(d.Outputs, Output{ID: ids.Encode(ids.BranchOutput, o.ID.Bytes),
			Node: ids.Encode(ids.Node, o.NodeID.Bytes), Kind: o.Kind, Order: o.SortKey})
	}
	connections, err := q.SerializerConnections(ctx, queries.SerializerConnectionsParams{WorkspaceID: w,
		ProjectID: p})
	if err != nil {
		return fmt.Errorf("serializer: read the connections: %w", err)
	}
	for _, c := range connections {
		conn := Connection{ID: ids.Encode(ids.Connection, c.ID.Bytes), Board: ids.Encode(ids.Board, c.BoardID.Bytes),
			From: ids.Encode(ids.Node, c.FromNodeID.Bytes), FromOutput: encode(ids.BranchOutput, c.FromOutputID),
			To: ids.Encode(ids.Node, c.ToNodeID.Bytes), Order: c.SortKey}
		if conn.Label, err = richtext.Parse(c.Label); err != nil {
			return fmt.Errorf("serializer: read the label of %s: %w", conn.ID, err)
		}
		d.Connections = append(d.Connections, conn)
	}
	return nil
}

// loadExpressions reads the expressions of the nodes, the outputs, and the
// connections that loadBoards read, each where its owner's field holds it,
// passing over those of owners that it did not read, which are in the
// trash.
func (d *Draft) loadExpressions(ctx context.Context, q *queries.Queries, w, p pgtype.UUID) error {
	nodes := make(map[string]*Node, len(d.Nodes))
	for i := range d.Nodes {
		nodes[d.Nodes[i].ID] = &d.Nodes[i]
	}
	outputs := make(map[string]*Output, len(d.Outputs))
	for i := range d.Outputs {
		outputs[d.Outputs[i].ID] = &d.Outputs[i]
	}
	connections := make(map[string]*Connection, len(d.Connections))
	for i := range d.Connections {
		connections[d.Connections[i].ID] = &d.Connections[i]
	}
	rows, err := q.SerializerExpressions(ctx, queries.SerializerExpressionsParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return fmt.Errorf("serializer: read the expressions: %w", err)
	}
	for _, r := range rows {
		var field **Expression
		switch {
		case r.NodeID.Valid:
			n := nodes[ids.Encode(ids.Node, r.NodeID.Bytes)]
			switch {
			case n == nil:
			case r.Field == "on_enter":
				field = &n.OnEnter
			case r.Field == "on_exit":
				field = &n.OnExit
			}
		case r.ConnectionID.Valid:
			c := connections[ids.Encode(ids.Connection, r.ConnectionID.Bytes)]
			switch {
			case c == nil:
			case r.Field == "condition":
				field = &c.Condition
			case r.Field == "on_traverse":
				field = &c.OnTraverse
			}
		case r.BranchOutputID.Valid:
			if o := outputs[ids.Encode(ids.BranchOutput, r.BranchOutputID.Bytes)]; o != nil && r.Field == "condition" {
				field = &o.Condition
			}
		}
		if field == nil {
			continue
		}
		e := &Expression{Source: r.Source, Error: r.ErrorCode}
		if r.Ast != nil {
			ctx := expr.ContextScript
			if r.Field == "condition" {
				ctx = expr.ContextCondition
			}
			if e.AST, err = expr.Decode(r.Ast, ctx); err != nil {
				return fmt.Errorf("serializer: read the AST of the %s of an expression's owner: %w", r.Field, err)
			}
		}
		*field = e
	}
	return nil
}

// loadDeclarations reads the variables, the component types, their
// attribute definitions, and the components, with their values.
func (d *Draft) loadDeclarations(ctx context.Context, q *queries.Queries, w, p pgtype.UUID) error {
	vars, err := q.SerializerVariables(ctx, queries.SerializerVariablesParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return fmt.Errorf("serializer: read the variables: %w", err)
	}
	for _, v := range vars {
		d.Variables = append(d.Variables, Variable{ID: ids.Encode(ids.Variable, v.ID.Bytes), Key: v.Key,
			Scope: v.Scope, Board: encode(ids.Board, v.BoardID), Type: v.Type, Default: value.FromJSON(v.DefaultValue),
			Description: v.Description, ResetOnEnter: v.ResetOnEnter})
	}
	types, err := q.SerializerComponentTypes(ctx, queries.SerializerComponentTypesParams{WorkspaceID: w,
		ProjectID: p})
	if err != nil {
		return fmt.Errorf("serializer: read the component types: %w", err)
	}
	for _, t := range types {
		d.ComponentTypes = append(d.ComponentTypes, ComponentType{ID: ids.Encode(ids.ComponentType, t.ID.Bytes),
			Key: t.Key, Name: t.Name})
	}
	defs, err := q.SerializerAttributeDefs(ctx, queries.SerializerAttributeDefsParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return fmt.Errorf("serializer: read the attribute definitions: %w", err)
	}
	for _, a := range defs {
		d.Attributes = append(d.Attributes, Attribute{ID: ids.Encode(ids.AttributeDef, a.ID.Bytes),
			ComponentType: ids.Encode(ids.ComponentType, a.ComponentTypeID.Bytes), Order: a.SortKey, Key: a.Key,
			Name: a.Name, Type: a.Type, Options: a.EnumOptions, Default: value.FromJSON(a.DefaultValue),
			Runtime: a.IsRuntime})
	}
	components, err := q.SerializerComponents(ctx, queries.SerializerComponentsParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return fmt.Errorf("serializer: read the components: %w", err)
	}
	for _, c := range components {
		d.Components = append(d.Components, Component{ID: ids.Encode(ids.Component, c.ID.Bytes),
			ComponentType: ids.Encode(ids.ComponentType, c.ComponentTypeID.Bytes), Key: c.Key, Name: c.Name,
			Image: encode(ids.Asset, c.ImageAssetID), Values: map[string]value.Value{}})
	}
	byID := make(map[string]*Component, len(d.Components))
	for i := range d.Components {
		byID[d.Components[i].ID] = &d.Components[i]
	}
	values, err := q.SerializerAttributeValues(ctx, queries.SerializerAttributeValuesParams{WorkspaceID: w,
		ProjectID: p})
	if err != nil {
		return fmt.Errorf("serializer: read the components' values: %w", err)
	}
	for _, v := range values {
		if c := byID[ids.Encode(ids.Component, v.ComponentID.Bytes)]; c != nil {
			c.Values[ids.Encode(ids.AttributeDef, v.AttributeDefID.Bytes)] = value.FromJSON(v.Value)
		}
	}
	return nil
}

// pgUUID returns id as a uuid column.
func pgUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// encode returns the public ID of the kind k of the column u, or "" if it is
// null.
func encode(k ids.Kind, u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return ids.Encode(k, u.Bytes)
}

// encodeID returns the public ID of the kind k of id, or "" if it is zero,
// as none is.
func encodeID(k ids.Kind, id ids.UUID) string {
	if id == (ids.UUID{}) {
		return ""
	}
	return ids.Encode(k, id)
}
