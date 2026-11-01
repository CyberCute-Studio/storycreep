package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Operation is a batch of a project's operation log as the other sessions of
// the project are told of it (PRD FR-RT-01, FR-RT-02): its seq, its commands,
// in order, as its client sent them, the entities that it changed, with
// their revisions after it, and when it was applied. It leaves out the
// batch's inverse, which only the batch's own answer holds (Result.Undo), and
// who applied it.
type Operation struct {
	Seq       int64
	Commands  []LoggedCommand
	Revisions []Revision
	CreatedAt time.Time
}

// LoggedCommand is a command of an Operation: its ID, its type, and its
// payload, as the pipeline validated it.
type LoggedCommand struct {
	ID      ids.UUID
	Type    Type
	Payload json.RawMessage
}

// ReadLog returns, in tx, a transaction of the project's workspace, the
// project's operations whose seqs come after after, in the order of their
// seqs: the first of them whatever its size, and each that follows the one
// before it, of the next seq, at most maxRows in all, while those before it
// take less than maxBytes as JSON, which is what the caller holds of them,
// however small the log keeps them, compressed, so that a page of the log is
// bounded however large its batches are, and holds one at least. The seqs
// of a project commit in order, without a gap (plan note N30), so the
// operations are those that follow after, unless the log no longer holds
// them, as once compaction (PRD FR-GDPR-03) has dropped them, which the
// caller tells by the first seq: a page ends before a seq that the log no
// longer holds, and the next starts after it. It returns none if no
// operation comes after after, or the project has none.
func ReadLog(ctx context.Context, tx pgx.Tx, workspaceID, projectID ids.UUID, after int64, maxRows int,
	maxBytes int64) ([]Operation, error) {
	rows, err := queries.New(tx).OperationsAfter(ctx, queries.OperationsAfterParams{WorkspaceID: nullUUID(workspaceID),
		ProjectID: nullUUID(projectID), After: after, MaxRows: int32(min(maxRows, 1<<30)), MaxBytes: maxBytes})
	if err != nil {
		return nil, fmt.Errorf("commands: read the operation log: %w", err)
	}
	out := make([]Operation, len(rows))
	for i, row := range rows {
		var cmds []logCommand
		if err := json.Unmarshal(row.Payload, &cmds); err != nil {
			return nil, fmt.Errorf("commands: decode the payload of the operation of seq %d: %w", row.Seq, err)
		}
		var refs []logRef
		if err := json.Unmarshal(row.EntityRefs, &refs); err != nil {
			return nil, fmt.Errorf("commands: decode the entity refs of the operation of seq %d: %w", row.Seq, err)
		}
		op := Operation{Seq: row.Seq, Commands: make([]LoggedCommand, len(cmds)), Revisions: make([]Revision, len(refs)),
			CreatedAt: row.CreatedAt.Time}
		for j, c := range cmds {
			id, err := ids.ParseUUID(c.CommandID)
			if err != nil {
				return nil, fmt.Errorf("commands: the operation of seq %d: a command's ID: %w", row.Seq, err)
			}
			op.Commands[j] = LoggedCommand{ID: id, Type: c.Type, Payload: c.Payload}
		}
		for j, r := range refs {
			ref, err := parseRef(r.Kind, r.ID)
			if err != nil {
				return nil, fmt.Errorf("commands: the operation of seq %d: an entity ref: %w", row.Seq, err)
			}
			op.Revisions[j] = Revision{Ref: ref, Rev: r.Rev}
		}
		out[i] = op
	}
	return out, nil
}

// LogFollows reports, in tx, a transaction of the project's workspace,
// whether the project's operation log holds the operation that follows the
// seq after, of the seq after+1, which it no longer does once compaction (PRD
// FR-GDPR-03) has dropped it, without reading what it holds. It reports
// false too when no operation follows after.
func LogFollows(ctx context.Context, tx pgx.Tx, workspaceID, projectID ids.UUID, after int64) (bool, error) {
	seq, err := queries.New(tx).FirstOperationAfter(ctx, queries.FirstOperationAfterParams{
		WorkspaceID: nullUUID(workspaceID), ProjectID: nullUUID(projectID), After: after})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("commands: read the operation log: %w", err)
	}
	return seq == after+1, nil
}

// ProjectSeqs returns, in tx, a transaction of the workspace workspaceID,
// the seq of the latest operation of each of the projects projectIDs that
// the workspace has, 0 before its first, as of the statement's start,
// without locking the projects, by their IDs, and none of a project that the
// workspace does not have, as once the project is purged. A project in the
// trash still has its seq.
func ProjectSeqs(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, projectIDs []ids.UUID) (map[ids.UUID]int64,
	error) {
	params := queries.ProjectSeqsParams{WorkspaceID: nullUUID(workspaceID), Ids: make([]pgtype.UUID, len(projectIDs))}
	for i, id := range projectIDs {
		params.Ids[i] = nullUUID(id)
	}
	rows, err := queries.New(tx).ProjectSeqs(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("commands: read the projects' seqs: %w", err)
	}
	out := make(map[ids.UUID]int64, len(rows))
	for _, row := range rows {
		out[ids.UUID(row.ID.Bytes)] = row.Seq
	}
	return out, nil
}
