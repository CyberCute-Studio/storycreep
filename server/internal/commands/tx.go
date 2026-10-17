package commands

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
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
	// usage is what the batch's commands add to each key of the workspace's
	// usage, and usageKeys the keys in the order of their first report.
	// checked is what the batch's commands declared that they add to each
	// limit (Spec.Limits), which is what the pipeline checked the limit for.
	usage     map[usage.Key]int64
	usageKeys []usage.Key
	checked   map[entitlements.LimitKey]int64
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

// AddUsage reports that the command adds delta to the workspace's usage of
// key (package usage, PRD FR-ENT-02), or takes -delta from it, as creating a
// node adds 1 to nodes, and deleting a board with its nodes takes them. Once
// every command of the batch has applied, the pipeline records what they add
// to each key together, as one delta, in the batch's transaction, so that it
// commits exactly when the batch does. It returns an error, which the command
// returns to refuse the batch, unless it is called from the command's Apply,
// or if it adds to a key that names a limit of the plans that the command
// does not declare (Spec.Limits), so that the pipeline checked it before the
// batch applied (FR-ENT-01). The pipeline checked the limit for what the
// batch's commands declared that they add to it (Limit.Adds), so it refuses
// a batch whose commands add more to it than that (recordUsage).
func (t *Tx) AddUsage(key usage.Key, delta int64) error {
	switch {
	case t.current == nil:
		return errors.New("commands: AddUsage is called outside a command's Apply")
	case !key.Known():
		return fmt.Errorf("commands: %s adds to %q, which is not a key of usage", t.current.typ, key)
	case delta > 0 && slices.Contains(entitlements.LimitKeys(), entitlements.LimitKey(key)) &&
		!slices.Contains(t.current.limits, entitlements.LimitKey(key)):
		return fmt.Errorf("commands: %s adds to %s, a limit of the plans that it does not declare, so nothing "+
			"checked it", t.current.typ, key)
	}
	sum, seen := t.usage[key]
	if delta > 0 && sum > math.MaxInt64-delta || delta < 0 && sum < math.MinInt64-delta {
		return fmt.Errorf("commands: what the batch adds to %s does not fit in 64 bits", key)
	}
	if !seen {
		t.usageKeys = append(t.usageKeys, key)
	}
	t.usage[key] = sum + delta
	return nil
}

// recordUsage records what the batch's commands added to each key of the
// workspace's usage (AddUsage), in the order of the keys' first reports, as
// one delta of each key, unless they add up to nothing. It refuses the batch
// if its commands added more to a limit than they declared (Spec.Limits),
// which is all that the pipeline checked the limit for, as with a command
// whose Limit.Adds says 0, which Check allows without reading the usage, let
// alone holding the lock of a key counted exactly (FR-ENT-01).
func (t *Tx) recordUsage(ctx context.Context) error {
	for _, key := range t.usageKeys {
		limit := entitlements.LimitKey(key)
		if added := t.usage[key]; added > 0 && slices.Contains(entitlements.LimitKeys(), limit) &&
			added > t.checked[limit] {
			return fmt.Errorf("commands: the batch adds %d to %s, but its commands declared %d, which is all that "+
				"the limit was checked for", added, key, t.checked[limit])
		}
		if err := usage.Record(ctx, t.tx, t.workspace, key, t.usage[key]); err != nil {
			return fmt.Errorf("commands: %w", err)
		}
	}
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
