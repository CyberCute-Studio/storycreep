package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// The kinds of a branch's output (FR-EDIT-04): the first conditional output
// of a branch is its if, the others its elseifs, and an else has no
// condition. They are the kinds that the published format names, which
// package published defines again, since its tests import this package, and
// checks to be these (TestOutputKinds), so that what the database stores is
// what a release writes.
const (
	OutputIf     = "if"
	OutputElseIf = "elseif"
	OutputElse   = "else"
)

// The types of command of a branch's output (PRD §5.7, FR-EDIT-04), each of
// which names the output that it is about by its ID, id, and applies to the
// output of the batch's project.
const (
	// CreateOutput creates an output of a branch, whose ID the client makes:
	// a conditional one, among the branch's conditional outputs, or its else.
	// Deleting it undoes it.
	CreateOutput commands.Type = "branch_output.create"
	// UpdateOutput changes a conditional output's condition. Changing it back
	// undoes it.
	UpdateOutput commands.Type = "branch_output.update"
	// ReorderOutput places a conditional output among its branch's, which
	// changes the order in which their conditions are evaluated (FR-EDIT-04
	// AC2), and the kinds of those whose places change from or to the first.
	// Placing it back undoes it.
	ReorderOutput commands.Type = "branch_output.reorder"
	// DeleteOutput deletes an output to the project's trash, unlisted, with
	// the connections that leave it, which needs to be confirmed while any
	// does (FR-EDIT-04 AC3), and RestoreOutput restores it, and them. Each
	// undoes the other.
	DeleteOutput  commands.Type = "branch_output.delete"
	RestoreOutput commands.Type = "branch_output.restore"
)

// MaxConnected is the most connections that a refusal of an output's
// deletion lists (ConnectedError).
const MaxConnected = 100

// BranchOutput is an output of a branch as the API shows it, and as an
// answer of revision_conflict shows it (commands.Entity.States).
type BranchOutput struct {
	ID        string                 `json:"id" doc:"The output's ID, which the connections that leave it name as their from_port"`
	NodeID    string                 `json:"node_id" doc:"The ID of the output's branch"`
	Kind      string                 `json:"kind" enum:"if,elseif,else" doc:"The output's kind, which follows its place among its branch's outputs that are not in the project's trash (PRD FR-EDIT-04): the first conditional output is the branch's if, the others its elseifs, and the else, which has no condition, comes after them"`
	Order     string                 `json:"order,omitempty" doc:"A conditional output's place among its branch's conditional outputs, which orders the evaluation of their conditions: a key of base-62 digits, 0-9, A-Z, and a-z, which orders them by its bytes, and those with the same key by their IDs without the prefix, bo_, compared by their bytes too. An else has none."`
	Condition *expression.Expression `json:"condition,omitempty" doc:"A conditional output's condition, which picks it, the first of its branch's whose condition holds (PRD §5.8.5). An else has none."`
	Rev       int64                  `json:"rev" doc:"The output's revision, which every change of it increments, of its kind among them, and which a change expects with expected_rev"`
	// DeletedAt is set while the output is in the project's trash, which only
	// an answer of revision_conflict shows.
	DeletedAt *time.Time `json:"deleted_at,omitempty" doc:"When the output went to the project's trash, if it is there: when it was deleted, or, if it was not, but its branch, or its board, is in the trash, with which it is in the trash too, when the first of them went there"`
	CreatedAt time.Time  `json:"created_at" doc:"When the output was created"`
	UpdatedAt time.Time  `json:"updated_at" doc:"When the output last changed"`
}

// outputOf returns the output o as the API shows it, without its condition.
func outputOf(o queries.BranchOutput) BranchOutput {
	return BranchOutput{ID: ids.Encode(ids.BranchOutput, o.ID.Bytes), NodeID: ids.Encode(ids.Node, o.NodeID.Bytes),
		Kind: o.Kind, Order: o.Order.String, Rev: o.Rev, DeletedAt: timeOf(o.DeletedAt), CreatedAt: o.CreatedAt.Time,
		UpdatedAt: o.UpdatedAt.Time}
}

// withConditions gives the outputs, rows of the project, their conditions.
func withConditions(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, rows []queries.BranchOutput,
	out []BranchOutput) error {
	list := make([]pgtype.UUID, len(rows))
	for i, o := range rows {
		list[i] = o.ID
	}
	conditions, err := expression.Of(ctx, q, ws, project, ids.BranchOutput, list)
	if err != nil {
		return err
	}
	for i, o := range rows {
		out[i].Condition = conditions[o.ID.Bytes][expression.Condition]
	}
	return nil
}

// nodeOutputs returns the outputs of the project's branches that are not
// deleted on their own, by the branches' UUIDs, each branch's in their order,
// with their conditions: those that are not in the trash, of a branch that is
// not, and those that are there with it alone, which come back with it, of a
// branch in the trash, as an answer of revision_conflict shows it.
func nodeOutputs(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, branches []pgtype.UUID) (
	map[ids.UUID][]BranchOutput, error) {
	out := map[ids.UUID][]BranchOutput{}
	if len(branches) == 0 {
		return out, nil
	}
	rows, err := q.NodeOutputs(ctx, queries.NodeOutputsParams{WorkspaceID: ws, ProjectID: project, NodeIds: branches})
	if err != nil {
		return nil, fmt.Errorf("node: read the branches' outputs: %w", err)
	}
	outputs := make([]BranchOutput, len(rows))
	for i, o := range rows {
		outputs[i] = outputOf(o)
	}
	if err := withConditions(ctx, q, ws, project, rows, outputs); err != nil {
		return nil, err
	}
	for i, o := range rows {
		out[o.NodeID.Bytes] = append(out[o.NodeID.Bytes], outputs[i])
	}
	return out, nil
}

// outputCreatePayload is CreateOutput's payload.
type outputCreatePayload struct {
	ID        string `json:"id" doc:"The new output's ID, a bo_ ID of a UUIDv7 that the client makes, which no entity of a project's content has, of any kind, in any workspace (PRD §5.2); one that one has is answered with 422 validation_failed"`
	NodeID    string `json:"node_id" doc:"The ID of the output's branch, a branch of the project that is not in its trash. Another type of node is answered with 422 validation_failed."`
	Else      bool   `json:"else,omitempty" doc:"Whether the output is the branch's else, which the flow takes when the condition of none of its other outputs holds (PRD FR-EDIT-04), which has neither a condition nor an order, and comes after them. A branch has one else at most, and one only while it has a conditional output, so an else for a branch that has one already, or that has no conditional output, is answered with 422 validation_failed. Without it, the output is a conditional one."`
	Condition string `json:"condition,omitempty" maxLength:"16384" doc:"A conditional output's condition, the source of a condition (PRD §5.8.5), of at most 16384 characters, which may not compile, and is then kept, marked invalid (PRD FR-EDIT-03 AC4). Without it, the empty source, which does not compile, until the condition is written. An else has none."`
	Order     string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"A conditional output's place among its branch's conditional outputs, which orders the evaluation of their conditions. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the outputs by its bytes, and those with the same key by their IDs without the prefix, bo_, compared by their bytes too: pick one between the keys of the outputs that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the output goes after the branch's last conditional output. An else has none."`
}

// outputUpdatePayload is UpdateOutput's payload. A field that it leaves out
// stays as it is.
type outputUpdatePayload struct {
	ID        string  `json:"id" doc:"The output's ID"`
	Condition *string `json:"condition,omitempty" maxLength:"16384" doc:"A conditional output's new condition, the source of a condition (PRD §5.8.5), of at most 16384 characters, which may be empty, and may not compile, and is then kept, marked invalid (PRD FR-EDIT-03 AC4). An else has none, and is answered with 422 validation_failed for one."`
}

// outputReorderPayload is ReorderOutput's payload.
type outputReorderPayload struct {
	ID    string `json:"id" doc:"The output's ID, a conditional output's: an else has no place among its branch's conditional outputs, and is answered with 422 validation_failed."`
	Order string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The output's place among its branch's conditional outputs, a key as branch_output.create's order is. Without it, the output goes after the branch's last conditional output, or stays where it is if it is the last already that is not in the trash."`
}

// outputDeletePayload is DeleteOutput's payload.
type outputDeletePayload struct {
	ID      string `json:"id" doc:"The output's ID. A branch's last conditional output that is not in the trash, while the branch has an else, which needs one, is answered with 422 validation_failed: delete the else first."`
	Confirm bool   `json:"confirm,omitempty" doc:"Whether to delete the output even though connections that are not in the project's trash leave it, which go to the trash with it, and come back with it (PRD FR-EDIT-04 AC3). Without it, an output that such connections leave is not deleted: the command is answered with 409 branch_output_connected, which lists them, for a client to ask to confirm before it sends the command again with confirm."`
}

// outputIDPayload is the payload of a command that needs nothing but its
// output's ID.
type outputIDPayload struct {
	ID string `json:"id" doc:"The output's ID"`
}

// Connected is a connection that leaves an output whose deletion is not
// confirmed, as a refusal of the deletion lists it.
type Connected struct {
	ID       string `json:"id"`
	ToNodeID string `json:"to_node_id"`
}

// ConnectedError is the reason of the refusal of an output's deletion that
// is not confirmed while connections that are not in the trash leave the
// output (FR-EDIT-04 AC3), which the API answers with
// branch_output_connected: Count of them, the first MaxConnected of which, in
// the order of their IDs, are Connections.
type ConnectedError struct {
	Connections []Connected
	Count       int64
}

func (e *ConnectedError) Error() string {
	return fmt.Sprintf("node: %d connections leave the output", e.Count)
}

// registerOutputs registers in r the branch's output as an entity of the
// pipeline, whose revisions a command expects as it does any entity's, and
// its commands, each of which changes the project's draft, so that an API
// token needs project:write for it (FR-AUTH-04), and its role "Edit nodes,
// connections, text, expressions" (§5.6), as a node's do. r has the node
// registered already.
func registerOutputs(r *commands.Registry) {
	commands.RegisterEntity(r, commands.Entity{
		Kind: ids.BranchOutput,
		Revs: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]int64, error) {
			rows, err := queries.New(tx).BranchOutputRevs(ctx, queries.BranchOutputRevsParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]int64, len(rows))
			for _, row := range rows {
				out[row.ID.Bytes] = row.Rev
			}
			return out, nil
		},
		States: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]any, error) {
			q := queries.New(tx)
			ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
			rows, err := q.BranchOutputsByID(ctx, queries.BranchOutputsByIDParams{WorkspaceID: ws, ProjectID: project,
				Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			list := make([]queries.BranchOutput, len(rows))
			outputs := make([]BranchOutput, len(rows))
			for i, row := range rows {
				list[i] = row.BranchOutput
				outputs[i] = outputOf(row.BranchOutput)
				// An output of a node or a board in the trash is in the trash
				// with it, which its state says.
				if outputs[i].DeletedAt == nil && row.EndsDeletedAt.Valid {
					outputs[i].DeletedAt = timeOf(row.EndsDeletedAt)
				}
			}
			if err := withConditions(ctx, q, ws, project, list, outputs); err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]any, len(rows))
			for i, row := range rows {
				out[row.BranchOutput.ID.Bytes] = outputs[i]
			}
			return out, nil
		},
	})

	output := []ids.Kind{ids.BranchOutput}
	op, sc := authz.EditContent, scope.ProjectWrite

	commands.Register(r, commands.Spec[outputCreatePayload]{
		// A command that creates an output of a branch refers to the branch,
		// so that a client may expect its revision too.
		Type: CreateOutput, Capability: op, Scope: sc, Touches: []ids.Kind{ids.BranchOutput, ids.Node},
		Check: func(p outputCreatePayload) input.FieldErrors {
			errs := newID(ids.BranchOutput, p.ID)
			errs = append(errs, entityID(ids.Node, "node_id", p.NodeID)...)
			if p.Else && p.Condition != "" {
				errs = append(errs, field("condition", "is set on an else, which has none")...)
			}
			if p.Else && p.Order != "" {
				errs = append(errs, field("order", "is set on an else, which comes after the branch's other "+
					"outputs")...)
			}
			return append(errs, field("condition", expression.CheckSource(p.Condition))...)
		},
		Inverse: func(_ context.Context, _ *commands.Tx, p outputCreatePayload) ([]commands.Step, error) {
			return []commands.Step{{Type: DeleteOutput, Payload: outputDeletePayload{ID: p.ID}}}, nil
		},
		Apply: applyCreateOutput,
	})

	commands.Register(r, commands.Spec[outputUpdatePayload]{
		Type: UpdateOutput, Capability: op, Scope: sc, Touches: output,
		Check: func(p outputUpdatePayload) input.FieldErrors {
			errs := entityID(ids.BranchOutput, "id", p.ID)
			if p.Condition != nil {
				errs = append(errs, field("condition", expression.CheckSource(*p.Condition))...)
			}
			return errs
		},
		// The inverse holds the condition that the update replaces, which
		// may be as long as a source is.
		UndoField: "condition",
		Inverse: func(ctx context.Context, tx *commands.Tx, p outputUpdatePayload) ([]commands.Step, error) {
			o, err := liveOutputOf(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			if p.Condition != nil {
				if err := conditional(o, "condition"); err != nil {
					return nil, err
				}
			}
			condition, err := expression.Back(ctx, queries.New(tx), o.WorkspaceID, o.ProjectID, ids.BranchOutput,
				o.ID, map[expression.Field]*string{expression.Condition: p.Condition})
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: UpdateOutput, Payload: outputUpdatePayload{ID: p.ID,
				Condition: condition[expression.Condition]}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p outputUpdatePayload) error {
			o, err := liveOutputOf(ctx, tx, p.ID)
			if err != nil || p.Condition == nil {
				return err
			}
			if err := conditional(o, "condition"); err != nil {
				return err
			}
			owner := expression.Owner{Kind: ids.BranchOutput, ID: o.ID, Board: o.BoardID}
			set, err := expression.Set(ctx, tx, owner, expression.Condition, p.Condition)
			if err != nil || !set {
				return err
			}
			// The condition is all that the command changes of the output.
			return changedKind(tx, ids.BranchOutput, o.ID)(expression.Touch(ctx, tx, owner))
		},
	})

	commands.Register(r, commands.Spec[outputReorderPayload]{
		Type: ReorderOutput, Capability: op, Scope: sc, Touches: output,
		Check: func(p outputReorderPayload) input.FieldErrors { return entityID(ids.BranchOutput, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p outputReorderPayload) ([]commands.Step, error) {
			o, err := liveOutputOf(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			if err := conditional(o, "id"); err != nil {
				return nil, err
			}
			return []commands.Step{{Type: ReorderOutput, Payload: outputReorderPayload{ID: p.ID,
				Order: o.Order.String}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p outputReorderPayload) error {
			o, err := liveOutputOf(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			if err := conditional(o, "id"); err != nil {
				return err
			}
			q := queries.New(tx)
			order, err := outputSortKey(ctx, q, o.WorkspaceID, o.ProjectID, o.NodeID, p.Order,
				&board.Child{ID: o.ID, Key: o.Order.String, Kind: ids.BranchOutput})
			if err != nil {
				return err
			}
			if err := changedKind(tx, ids.BranchOutput, o.ID)(q.ReorderBranchOutput(ctx,
				queries.ReorderBranchOutputParams{SortKey: pgtype.Text{String: order, Valid: true}, UpdatedBy: by(tx), WorkspaceID: o.WorkspaceID,
					ProjectID: o.ProjectID, ID: o.ID})); err != nil {
				return err
			}
			return rekind(ctx, tx, o.NodeID)
		},
	})

	commands.Register(r, commands.Spec[outputDeletePayload]{
		Type: DeleteOutput, Capability: op, Scope: sc, Touches: output,
		Check: func(p outputDeletePayload) input.FieldErrors { return entityID(ids.BranchOutput, "id", p.ID) },
		// Deleting an output that is in the trash already does nothing, and
		// its undo nothing either. One of a node or a board in the trash, in
		// the trash with it but not deleted itself, is refused, as every
		// command refuses it, because its undo, a restore, would be refused
		// until its node is restored.
		Inverse: func(ctx context.Context, tx *commands.Tx, p outputDeletePayload) ([]commands.Step, error) {
			row, err := anyOutput(ctx, tx, p.ID)
			if err != nil || row.BranchOutput.DeletedAt.Valid || row.NodeDeleted {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: RestoreOutput, Payload: outputIDPayload{ID: p.ID}}}, nil
		},
		Apply: applyDeleteOutput,
	})

	commands.Register(r, commands.Spec[outputIDPayload]{
		Type: RestoreOutput, Capability: op, Scope: sc, Touches: output,
		Check: func(p outputIDPayload) input.FieldErrors { return entityID(ids.BranchOutput, "id", p.ID) },
		// Restoring an output that is not in the trash does nothing, and its
		// undo nothing either. An output of a node or a board in the trash,
		// which is in the trash with it, is refused, since it comes back only
		// with its node. The undo of a restoration deletes the output again,
		// confirmed, as the connections that leave it came back with it.
		Inverse: func(ctx context.Context, tx *commands.Tx, p outputIDPayload) ([]commands.Step, error) {
			row, err := anyOutput(ctx, tx, p.ID)
			if err != nil || !row.BranchOutput.DeletedAt.Valid || row.NodeDeleted {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: DeleteOutput, Payload: outputDeletePayload{ID: p.ID, Confirm: true}}}, nil
		},
		Apply: applyRestoreOutput,
	})
}

// applyCreateOutput is CreateOutput's Apply.
func applyCreateOutput(ctx context.Context, tx *commands.Tx, p outputCreatePayload) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	id := decoded(ids.BranchOutput, p.ID)
	switch _, err := q.BranchOutputAny(ctx, queries.BranchOutputAnyParams{WorkspaceID: ws, ProjectID: project,
		ID: id}); {
	case err == nil:
		return refuseTakenOutputID()
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("node: read the output: %w", err)
	}
	n, err := liveNodeOf(ctx, tx, "node_id", p.NodeID)
	if err != nil {
		return err
	}
	if n.Type != TypeBranch {
		return commands.Refuse("node_id", fmt.Sprintf("is a node of the type %s, which has no outputs: only a "+
			"branch has them", n.Type), nil)
	}
	counts, err := q.NodeOutputCounts(ctx, queries.NodeOutputCountsParams{WorkspaceID: ws, ProjectID: project,
		NodeID: n.ID})
	if err != nil {
		return fmt.Errorf("node: count the branch's outputs: %w", err)
	}
	params := queries.InsertBranchOutputParams{ID: id, WorkspaceID: ws, ProjectID: project, BoardID: n.BoardID,
		NodeID: n.ID, CreatedBy: by(tx)}
	if p.Else {
		switch {
		case counts.ElseID.Valid:
			return commands.Refuse("else", "is set, but the branch has an else already, and has one at most", nil)
		case counts.Conditional == 0:
			return commands.Refuse("else", "is set, but the branch has no conditional output, which its else "+
				"comes after: create one first", nil)
		}
		params.Kind = OutputElse
	} else {
		if params.SortKey.String, err = outputSortKey(ctx, q, ws, project, n.ID, p.Order, nil); err != nil {
			return err
		}
		params.SortKey.Valid = true
		// The output is the branch's if when it comes first, and the others
		// are rekinded below, as they are by a reorder.
		params.Kind = OutputElseIf
		if first, err := firstConditional(ctx, q, ws, project, n.ID); err != nil {
			return err
		} else if first == nil || compareOutputs(params.SortKey.String, id, first.Order.String, first.ID) < 0 {
			params.Kind = OutputIf
		}
	}
	err = q.InsertBranchOutput(ctx, params)
	if database.IDTaken(err, "branch_output") {
		return commands.RefuseTakenID()
	}
	if err != nil {
		return fmt.Errorf("node: add the output: %w", err)
	}
	if err := tx.Changed(ids.BranchOutput, id.Bytes, 1); err != nil {
		return err
	}
	if !p.Else {
		if _, err := expression.Set(ctx, tx, expression.Owner{Kind: ids.BranchOutput, ID: id, Board: n.BoardID},
			expression.Condition, &p.Condition); err != nil {
			return err
		}
	}
	return rekind(ctx, tx, n.ID)
}

// applyDeleteOutput is DeleteOutput's Apply.
func applyDeleteOutput(ctx context.Context, tx *commands.Tx, p outputDeletePayload) error {
	row, err := anyOutput(ctx, tx, p.ID)
	switch {
	case err != nil:
		return err
	case row.BranchOutput.DeletedAt.Valid:
		return nil
	case row.NodeDeleted:
		return refuseOutputNodeInTrash(row)
	}
	o := row.BranchOutput
	q := queries.New(tx)
	if o.Kind != OutputElse {
		counts, err := q.NodeOutputCounts(ctx, queries.NodeOutputCountsParams{WorkspaceID: o.WorkspaceID,
			ProjectID: o.ProjectID, NodeID: o.NodeID})
		if err != nil {
			return fmt.Errorf("node: count the branch's outputs: %w", err)
		}
		if counts.Conditional == 1 && counts.ElseID.Valid {
			return commands.Refuse("id", "is the branch's last conditional output, which its else comes after: "+
				"delete the else first", nil)
		}
	}
	if !p.Confirm {
		rows, err := q.OutputConnections(ctx, queries.OutputConnectionsParams{WorkspaceID: o.WorkspaceID,
			ProjectID: o.ProjectID, NodeID: o.NodeID, OutputID: o.ID, MaxRows: MaxConnected})
		if err != nil {
			return fmt.Errorf("node: list the connections that leave the output: %w", err)
		}
		// The list holds them all unless it is full, and they are counted
		// then.
		count := int64(len(rows))
		if count == MaxConnected {
			if count, err = q.OutputConnectionCount(ctx, queries.OutputConnectionCountParams{
				WorkspaceID: o.WorkspaceID, ProjectID: o.ProjectID, NodeID: o.NodeID, OutputID: o.ID}); err != nil {
				return fmt.Errorf("node: count the connections that leave the output: %w", err)
			}
		}
		if count > 0 {
			connected := &ConnectedError{Connections: make([]Connected, len(rows)), Count: count}
			for i, c := range rows {
				connected.Connections[i] = Connected{ID: ids.Encode(ids.Connection, c.ID.Bytes),
					ToNodeID: ids.Encode(ids.Node, c.ToNodeID.Bytes)}
			}
			return commands.Refuse("confirm", fmt.Sprintf("is not set, but %d connections leave the output, which "+
				"its deletion deletes with it: confirm the deletion to delete them all the same", count), connected)
		}
	}
	// The connections that leave the output are in the trash with it, and
	// come back with it.
	if err := changedKind(tx, ids.BranchOutput, o.ID)(q.TrashBranchOutput(ctx, queries.TrashBranchOutputParams{
		UpdatedBy: by(tx), WorkspaceID: o.WorkspaceID, ProjectID: o.ProjectID, ID: o.ID})); err != nil {
		return err
	}
	return rekind(ctx, tx, o.NodeID)
}

// applyRestoreOutput is RestoreOutput's Apply.
func applyRestoreOutput(ctx context.Context, tx *commands.Tx, p outputIDPayload) error {
	row, err := anyOutput(ctx, tx, p.ID)
	switch {
	case err != nil:
		return err
	case row.BoardDeleted:
		return commands.Refuse("id", "is an output of a branch of a board in the project's trash, which comes back "+
			"only with its board: restore the board first", nil)
	case row.NodeDeleted:
		return commands.Refuse("id", "is an output of a branch in the project's trash, which comes back only with "+
			"its branch: restore the branch first", nil)
	case !row.BranchOutput.DeletedAt.Valid:
		return nil
	}
	o := row.BranchOutput
	q := queries.New(tx)
	if o.Kind == OutputElse {
		counts, err := q.NodeOutputCounts(ctx, queries.NodeOutputCountsParams{WorkspaceID: o.WorkspaceID,
			ProjectID: o.ProjectID, NodeID: o.NodeID})
		switch {
		case err != nil:
			return fmt.Errorf("node: count the branch's outputs: %w", err)
		case counts.ElseID.Valid:
			return commands.Refuse("id", "is an else of a branch that has another, and has one at most: delete "+
				"that one first", nil)
		case counts.Conditional == 0:
			return commands.Refuse("id", "is an else of a branch that has no conditional output, which its else "+
				"comes after: restore or create one first", nil)
		}
	}
	if err := changedKind(tx, ids.BranchOutput, o.ID)(q.RestoreBranchOutput(ctx, queries.RestoreBranchOutputParams{
		UpdatedBy: by(tx), WorkspaceID: o.WorkspaceID, ProjectID: o.ProjectID, ID: o.ID})); err != nil {
		return err
	}
	// The connections that leave it whose node at the other end is not in the
	// trash come back with it, and the batch's answer lists the others
	// (package connection, FR-PROJ-06 AC1).
	if err := tx.Restored(ids.BranchOutput, o.ID.Bytes); err != nil {
		return err
	}
	return rekind(ctx, tx, o.NodeID)
}

// rekind gives each conditional output of the branch node that is not in the
// trash the kind of its place, its first the branch's if and the others its
// elseifs, and reports those whose kinds it changed, in the order of their
// IDs.
func rekind(ctx context.Context, tx *commands.Tx, node pgtype.UUID) error {
	rows, err := queries.New(tx).RekindBranchOutputs(ctx, queries.RekindBranchOutputsParams{UpdatedBy: by(tx),
		WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), NodeID: node})
	if err != nil {
		return fmt.Errorf("node: give the branch's outputs their kinds: %w", err)
	}
	for _, row := range sortedRevs(rows) {
		if err := tx.Changed(ids.BranchOutput, row.ID.Bytes, row.Rev); err != nil {
			return err
		}
	}
	return nil
}

// sortedRevs returns rows in the order of their IDs.
func sortedRevs(rows []queries.RekindBranchOutputsRow) []queries.RekindBranchOutputsRow {
	out := slices.Clone(rows)
	slices.SortFunc(out, func(a, b queries.RekindBranchOutputsRow) int {
		return bytes.Compare(a.ID.Bytes[:], b.ID.Bytes[:])
	})
	return out
}

// firstConditional returns the first conditional output of the branch node,
// which is not in the trash, that is not in the trash either, or nil if it
// has none.
func firstConditional(ctx context.Context, q *queries.Queries, ws, project, node pgtype.UUID) (
	*queries.BranchOutput, error) {
	rows, err := q.NodeOutputs(ctx, queries.NodeOutputsParams{WorkspaceID: ws, ProjectID: project,
		NodeIds: []pgtype.UUID{node}})
	if err != nil {
		return nil, fmt.Errorf("node: read the branch's outputs: %w", err)
	}
	for i := range rows {
		if rows[i].Kind != OutputElse {
			return &rows[i], nil
		}
	}
	return nil, nil
}

// compareOutputs compares the places of two conditional outputs, by their
// orders, and then their IDs, as the database orders them, by their bytes.
func compareOutputs(orderA string, a pgtype.UUID, orderB string, b pgtype.UUID) int {
	if c := bytes.Compare([]byte(orderA), []byte(orderB)); c != 0 {
		return c
	}
	return bytes.Compare(a.Bytes[:], b.Bytes[:])
}

// outputSortKey returns the key that a conditional output of the branch node
// is placed at: the key given, if it is not "", or else one after the last
// conditional output's of the branch, as package connection places a
// connection after its port's last: those in the trash included, so that one
// restored later keeps its place, unless that key has no key after it.
// moving is the output that moves among them, or nil for a new one: it keeps
// its key if it comes after every other that is not in the trash.
func outputSortKey(ctx context.Context, q *queries.Queries, ws, project, node pgtype.UUID, given string,
	moving *board.Child) (string, error) {
	if given != "" {
		return given, nil
	}
	params := queries.LastBranchOutputSortKeysParams{WorkspaceID: ws, ProjectID: project, NodeID: node}
	if moving != nil {
		params.Moved = moving.ID
	}
	last, err := q.LastBranchOutputSortKeys(ctx, params)
	if err != nil {
		return "", fmt.Errorf("node: read the branch's last keys: %w", err)
	}
	return board.Place(board.Last{Key: last.Last, Live: last.LastLive, LiveID: last.LastLiveID}, moving,
		"the branch's last conditional output")
}

// conditional refuses the batch, at the payload's field at, if the output o
// is an else, which has neither a condition nor a place among its branch's
// conditional outputs.
func conditional(o queries.BranchOutput, at string) error {
	if o.Kind == OutputElse {
		if at == "condition" {
			return commands.Refuse(at, "is set on an else, which has none", nil)
		}
		return commands.Refuse(at, "is an else, which has no place among its branch's conditional outputs, and "+
			"comes after them", nil)
	}
	return nil
}

// refuseTakenOutputID returns the refusal, at id, of a command that creates
// an output with the ID of one of the project's that exists already, which
// the transaction sees; the insert refuses the ID of any other entity
// (commands.RefuseTakenID).
func refuseTakenOutputID() error {
	return commands.Refuse("id", "is the ID of a branch output that exists already", nil)
}

// anyOutput returns the output of the batch's project whose ID is id, an
// output's ID that its payload's Check checked, whether or not it is in the
// trash, with whether it is in the trash with its node or its board, or
// refuses the batch, at id, if the project has no such output.
func anyOutput(ctx context.Context, tx *commands.Tx, id string) (queries.BranchOutputAnyRow, error) {
	row, err := queries.New(tx).BranchOutputAny(ctx, queries.BranchOutputAnyParams{
		WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()),
		ID: decoded(ids.BranchOutput, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.BranchOutputAnyRow{}, commands.Refuse("id", "is not a branch output of the project", nil)
	case err != nil:
		return queries.BranchOutputAnyRow{}, fmt.Errorf("node: read the output: %w", err)
	}
	return row, nil
}

// liveOutputOf returns the output of the batch's project whose ID is id as
// anyOutput does, or refuses the batch if it is in the trash, where nothing
// but restoring it changes it, or its node or its board is, with which it
// comes back.
func liveOutputOf(ctx context.Context, tx *commands.Tx, id string) (queries.BranchOutput, error) {
	row, err := anyOutput(ctx, tx, id)
	switch {
	case err != nil:
		return queries.BranchOutput{}, err
	case row.BranchOutput.DeletedAt.Valid:
		return queries.BranchOutput{}, commands.Refuse("id", "is a branch output in the project's trash, which "+
			"only restoring it changes", nil)
	case row.NodeDeleted:
		return queries.BranchOutput{}, refuseOutputNodeInTrash(row)
	}
	return row.BranchOutput, nil
}

// refuseOutputNodeInTrash returns the refusal, at id, of a command that would
// change the output of row, of a branch or a board in the trash, which is in
// the trash with it, and which nothing changes until its branch, or its
// board, is restored.
func refuseOutputNodeInTrash(row queries.BranchOutputAnyRow) error {
	if row.BoardDeleted {
		return commands.Refuse("id", "is an output of a branch of a board in the project's trash, which comes back "+
			"with its board", nil)
	}
	return commands.Refuse("id", "is an output of a branch in the project's trash, which comes back with its "+
		"branch", nil)
}
