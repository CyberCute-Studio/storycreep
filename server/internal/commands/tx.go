package commands

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
)

// Tx is the transaction that a batch is applied in, as the functions of a
// command and of an entity see it: the queries' DBTX, which runs statements
// in it, with the batch's actor and project, and what the pipeline does in
// it for the commands. Everything that a command does through it commits
// exactly when the batch does. It has no method that commits or rolls back,
// which is the pipeline's to do once every command has applied, and a
// command runs no statement through Exec that ends the transaction either,
// as COMMIT or ROLLBACK.
type Tx struct {
	tx        pgx.Tx
	pipeline  *Pipeline
	actor     Actor
	project   ids.UUID
	workspace ids.UUID
	// current is the command that the pipeline applies, or nil outside a
	// command's Apply, as while it reads the revisions or computes an
	// inverse, which only read, so that only Apply reports changes.
	current *spec
	// changed are the entities that the batch's commands changed, with their
	// latest revisions, in the order of their first change, and at their
	// indexes. revs are the revisions that the batch has seen of each entity,
	// read by the pipeline or reported by a command.
	changed []Revision
	at      map[Ref]int
	revs    map[Ref]int64
}

// Exec runs a statement in the batch's transaction.
func (t *Tx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.tx.Exec(ctx, sql, args...)
}

// Query runs a query in the batch's transaction.
func (t *Tx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.tx.Query(ctx, sql, args...)
}

// QueryRow runs a query that returns at most one row in the batch's
// transaction.
func (t *Tx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.tx.QueryRow(ctx, sql, args...)
}

// Actor returns who applies the batch.
func (t *Tx) Actor() Actor {
	return t.actor
}

// ProjectID returns the project that the batch is applied to.
func (t *Tx) ProjectID() ids.UUID {
	return t.project
}

// WorkspaceID returns the workspace of the batch's project, which the
// transaction is scoped to (database.Scope).
func (t *Tx) WorkspaceID() ids.UUID {
	return t.workspace
}

// Changed reports that the command changed the entity of the kind with the
// ID, which now has the revision rev, so that the operation's entity_refs,
// and the batch's Result, name it with that revision. An entity that several
// commands of the batch change is named once, with its latest revision. It
// returns an error, which the command returns to refuse the batch, unless it
// is called from the command's Apply, which alone changes anything (not from
// an Inverse, nor from an Entity's functions), the command touches the kind
// (Spec.Touches), and rev is above every revision of the entity that the
// batch has seen, as it is when the command incremented it.
func (t *Tx) Changed(kind ids.Kind, id ids.UUID, rev int64) error {
	ref := Ref{Kind: kind, ID: id}
	switch {
	case t.current == nil:
		return errors.New("commands: Changed is called outside a command's Apply")
	case !slices.Contains(t.current.touches, kind):
		return fmt.Errorf("commands: %s changed a %s, which it does not touch", t.current.typ, kind)
	case rev < 1:
		return fmt.Errorf("commands: %s gave %s the revision %d, but an entity's first revision is 1", t.current.typ,
			encodeRef(ref), rev)
	}
	if seen, ok := t.revs[ref]; ok && rev <= seen {
		return fmt.Errorf("commands: %s gave %s the revision %d, but it had %d already", t.current.typ,
			encodeRef(ref), rev, seen)
	}
	t.revs[ref] = rev
	if i, ok := t.at[ref]; ok {
		t.changed[i].Rev = rev
		return nil
	}
	t.at[ref] = len(t.changed)
	t.changed = append(t.changed, Revision{Ref: ref, Rev: rev})
	return nil
}

// Enqueue enqueues a job in the batch's transaction (package jobs), so that
// the job exists, and runs, only if the batch commits. It returns an error if
// the pipeline has no job queue.
func (t *Tx) Enqueue(ctx context.Context, args jobs.Args, opts *jobs.EnqueueOptions) (jobs.Enqueued, error) {
	if t.pipeline.jobs == nil {
		return jobs.Enqueued{}, fmt.Errorf("commands: the pipeline has no job queue to enqueue %s in", args.Kind())
	}
	return t.pipeline.jobs.Enqueue(ctx, t.tx, args, opts)
}

// Audit records e in the audit log in the batch's transaction (package
// audit), so that the entry commits exactly when the batch does (PRD
// FR-TEN-07 AC3), as an event of the batch's workspace whose actor is the
// batch's: its WorkspaceID, ActorUserID, ActorTokenID, IP, and UserAgent are
// the batch's, whatever e holds.
func (t *Tx) Audit(ctx context.Context, e audit.Entry) error {
	e.WorkspaceID = t.workspace
	e.ActorUserID, e.ActorTokenID = t.actor.UserID, t.actor.TokenID
	e.IP, e.UserAgent = t.actor.Client.IP, t.actor.Client.UserAgent
	return audit.Record(ctx, t.tx, e)
}
