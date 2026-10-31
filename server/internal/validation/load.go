package validation

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
)

// draft is what Load reads of a project: the model that the rules read, the
// project's operation sequence as of the read, and its mutes.
type draft struct {
	model *model
	seq   int64
	mutes map[muteKey]mute
}

// muteKey is a mute's owner and its code.
type muteKey struct {
	owner ids.UUID
	code  Code
}

// mute is a mute's reason, and who muted it last, and when; by is zero once
// their account is deleted.
type mute struct {
	reason string
	by     ids.UUID
	at     time.Time
}

// load reads the project projectID of the workspace ws in tx, a transaction
// of the workspace that reads as of one moment, so that its reads agree with
// one another, as the project's state after a batch: one read of each kind
// of its rows, whatever the rules read of them.
func load(ctx context.Context, tx pgx.Tx, ws, projectID ids.UUID) (*draft, error) {
	q := queries.New(tx)
	w, p := pgUUID(ws), pgUUID(projectID)
	d := &draft{model: &model{project: projectID, components: map[ids.UUID]ids.UUID{},
		attributes: map[ids.UUID]attribute{}}, mutes: map[muteKey]mute{}}
	m := d.model
	proj, err := q.ValidationProject(ctx, queries.ValidationProjectParams{WorkspaceID: w, ID: p})
	if err != nil {
		return nil, fmt.Errorf("validation: read the project: %w", err)
	}
	d.seq = proj.Seq
	if m.entry.board, m.entry.node, err = project.EntryOf(proj.Settings); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}
	boards, err := q.ValidationBoards(ctx, queries.ValidationBoardsParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return nil, fmt.Errorf("validation: read the boards: %w", err)
	}
	for _, b := range boards {
		m.boards = append(m.boards, board{id: b.ID.Bytes, entry: b.EntryNodeID.Bytes})
	}
	nodes, err := q.ValidationNodes(ctx, queries.ValidationNodesParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return nil, fmt.Errorf("validation: read the nodes: %w", err)
	}
	live := map[ids.UUID]bool{}
	for _, n := range nodes {
		live[n.ID.Bytes] = true
		m.nodes = append(m.nodes, node{id: n.ID.Bytes, board: n.BoardID.Bytes, typ: n.Type, kind: n.Kind,
			speaker: n.SpeakerComponentID.Bytes, ending: n.IsEnding, textEmpty: n.TextEmpty,
			targetNode: n.TargetNodeID.Bytes})
	}
	outputs, err := q.ValidationOutputs(ctx, queries.ValidationOutputsParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return nil, fmt.Errorf("validation: read the outputs: %w", err)
	}
	for _, o := range outputs {
		live[o.ID.Bytes] = true
		m.outputs = append(m.outputs, output{id: o.ID.Bytes, node: o.NodeID.Bytes, board: o.BoardID.Bytes})
	}
	connections, err := q.ValidationConnections(ctx, queries.ValidationConnectionsParams{WorkspaceID: w,
		ProjectID: p})
	if err != nil {
		return nil, fmt.Errorf("validation: read the connections: %w", err)
	}
	for _, c := range connections {
		live[c.ID.Bytes] = true
		m.connections = append(m.connections, connection{id: c.ID.Bytes, board: c.BoardID.Bytes,
			from: c.FromNodeID.Bytes, output: c.FromOutputID.Bytes, to: c.ToNodeID.Bytes, label: string(c.Label)})
	}
	at, err := d.loadExpressions(ctx, q, w, p, live, compared(m.connections))
	if err != nil {
		return nil, err
	}
	components, err := q.ValidationComponents(ctx, queries.ValidationComponentsParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return nil, fmt.Errorf("validation: read the components: %w", err)
	}
	for _, c := range components {
		m.components[c.ID.Bytes] = c.ComponentTypeID.Bytes
		live[c.ID.Bytes] = true
	}
	attributes, err := q.ValidationAttributeDefs(ctx, queries.ValidationAttributeDefsParams{WorkspaceID: w,
		ProjectID: p})
	if err != nil {
		return nil, fmt.Errorf("validation: read the attribute definitions: %w", err)
	}
	for _, a := range attributes {
		m.attributes[a.ID.Bytes] = attribute{typ: a.ComponentTypeID.Bytes, runtime: a.IsRuntime}
	}
	if err := d.loadTexts(ctx, q, w, p, live, at); err != nil {
		return nil, err
	}
	mutes, err := q.ValidationMutes(ctx, queries.ValidationMutesParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return nil, fmt.Errorf("validation: read the mutes: %w", err)
	}
	for _, mu := range mutes {
		d.mutes[muteKey{mu.OwnerID.Bytes, Code(mu.Code)}] = mute{reason: mu.Reason, by: mu.UpdatedBy.Bytes,
			at: mu.UpdatedAt.Time}
	}
	return d, nil
}

// compared returns the connections whose conditions the rule of duplicate
// choices may compare: the choices of an element, which leave it by its out,
// each of which shares its label with another choice of the element, as the
// rule groups them (choicesOf, sameLabel). The conditions of the others are
// never compared, so they are not decoded.
func compared(connections []connection) map[ids.UUID]bool {
	out := map[ids.UUID]bool{}
	for _, choices := range choicesOf(connections) {
		for _, same := range sameLabel(choices) {
			for _, c := range same {
				out[c.id] = true
			}
		}
	}
	return out
}

// loadExpressions reads the expressions of the owners that live holds, the
// nodes, the connections, and the outputs that are not in the trash, the AST
// of the condition of each connection that compared holds, and the
// variables, each with whether such an expression reads it, or writes it. It
// returns the places of the variables among the model's, by their IDs.
func (d *draft) loadExpressions(ctx context.Context, q *queries.Queries, w, p pgtype.UUID,
	live, compared map[ids.UUID]bool) (map[ids.UUID]int, error) {
	m := d.model
	rows, err := q.ValidationExpressions(ctx, queries.ValidationExpressionsParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return nil, fmt.Errorf("validation: read the expressions: %w", err)
	}
	// The expressions of owners outside the trash, which are sites of the
	// variables' usages.
	sites := map[ids.UUID]bool{}
	for _, r := range rows {
		var owner ref
		switch {
		case r.NodeID.Valid:
			owner = ref{ids.Node, r.NodeID.Bytes}
		case r.ConnectionID.Valid:
			owner = ref{ids.Connection, r.ConnectionID.Bytes}
		default:
			owner = ref{ids.BranchOutput, r.BranchOutputID.Bytes}
		}
		if !live[owner.id] {
			continue
		}
		sites[r.ID.Bytes] = true
		e := expression{owner: owner, field: r.Field, valid: r.IsValid, errorCode: r.ErrorCode}
		if r.IsValid && r.ConditionAst != nil && compared[owner.id] {
			if e.condition, err = expr.Decode(r.ConditionAst, expr.ContextCondition); err != nil {
				return nil, fmt.Errorf("validation: read the condition of %s: %w",
					ids.Encode(ids.Connection, owner.id), err)
			}
		}
		m.expressions = append(m.expressions, e)
	}
	vars, err := q.ValidationVariables(ctx, queries.ValidationVariablesParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return nil, fmt.Errorf("validation: read the variables: %w", err)
	}
	at := make(map[ids.UUID]int, len(vars))
	for i, v := range vars {
		at[v.ID.Bytes] = i
		m.variables = append(m.variables, variable{id: v.ID.Bytes, board: v.BoardID.Bytes, key: v.Key})
	}
	uses, err := q.ValidationVariableUses(ctx, queries.ValidationVariableUsesParams{WorkspaceID: w, ProjectID: p})
	if err != nil {
		return nil, fmt.Errorf("validation: read what the expressions read and write: %w", err)
	}
	for _, u := range uses {
		i, ok := at[u.TargetID.Bytes]
		if !ok || !sites[u.ExpressionID.Bytes] {
			continue
		}
		if dependency.Access(u.Access) == dependency.Write {
			m.variables[i].written = true
		} else {
			m.variables[i].read = true
		}
	}
	return at, nil
}

// loadTexts reads what the texts of the nodes, the connections, and the
// components that live holds refer to, each of which reads the variables
// that its value references show, whose places among the model's at holds.
func (d *draft) loadTexts(ctx context.Context, q *queries.Queries, w, p pgtype.UUID, live map[ids.UUID]bool,
	at map[ids.UUID]int) error {
	m := d.model
	rows, err := q.ValidationTextReferences(ctx, queries.ValidationTextReferencesParams{WorkspaceID: w,
		ProjectID: p})
	if err != nil {
		return fmt.Errorf("validation: read what the texts refer to: %w", err)
	}
	for _, r := range rows {
		var owner ref
		switch {
		case r.NodeID.Valid:
			owner = ref{ids.Node, r.NodeID.Bytes}
		case r.ConnectionID.Valid:
			owner = ref{ids.Connection, r.ConnectionID.Bytes}
		default:
			owner = ref{ids.Component, r.ComponentID.Bytes}
		}
		if !live[owner.id] {
			continue
		}
		u, err := dependency.InlineUse(r.Inline)
		if err != nil {
			return fmt.Errorf("validation: %w", err)
		}
		m.texts = append(m.texts, textRef{owner: owner, use: u})
		if i, ok := at[u.Target]; ok && u.Kind == dependency.Variable {
			m.variables[i].read = true
		}
	}
	return nil
}

// pgUUID returns id as a uuid column.
func pgUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
