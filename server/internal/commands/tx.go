package commands

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
	// own are the entities that the command that applies changed, in the
	// order of its first change of each, and owned the same as a set, which
	// the pipeline empties before each command's Apply, and expecting the
	// kinds of entity that the steps of the command's inverse touch, each
	// once, or none for a batch that cannot be undone: the command's undo
	// expects the revisions of those of its entities that are of these kinds
	// (plan item 37).
	own       []Ref
	owned     map[Ref]struct{}
	expecting []ids.Kind
	// undoBytes is how many bytes the inverses that the pipeline has computed
	// of the batch's commands take, as the operation log keeps them: the
	// payloads of their steps (LogBytes), and the revisions that they expect
	// (ExpectedRevBytes), which Changed counts as the commands report them,
	// and the pipeline refuses a command for once it is more than maxUndo
	// (UndoRoom). undoSteps is how many steps they have, which the pipeline
	// refuses a command for once it is more than maxUndoSteps.
	undoBytes, maxUndo      int
	undoSteps, maxUndoSteps int
	// usage is what the batch's commands add to each key of the workspace's
	// usage, and usageKeys the keys in the order of their first report.
	// checked is what the batch's commands declared that they add to each
	// limit (Spec.Limits), which is what the pipeline checked the limit for.
	usage     map[usage.Key]int64
	usageKeys []usage.Key
	checked   map[entitlements.LimitKey]int64
	// counts are what the batch's commands add to the workspace's usage once
	// every command of the batch has applied (AddUsageOnceApplied), in the
	// order of their reports, and trashed says that the batch's project was in
	// the workspace's trash before the batch, which a batch applies to only if
	// a command of it restores the project (Spec.Restores).
	counts  []usageCount
	trashed bool
	// restored are the entities that the batch's commands restored from the
	// project's trash (Restored), by their kinds, each once, in the order of
	// their first report, and reported the same entities as a set, which
	// keeps each once however many the batch restores.
	restored map[ids.Kind][]ids.UUID
	reported map[Ref]struct{}
}

// usageCount is what the command spec adds to the workspace's usage of key
// once every command of its batch has applied: what count returns then
// (AddUsageOnceApplied).
type usageCount struct {
	spec  *spec
	key   usage.Key
	count func(ctx context.Context) (int64, error)
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

// UndoRoom returns how many bytes the payloads of the steps of the inverse
// that the pipeline computes may take, as the operation log writes them
// (LogBytes), before they take the batch's undo, with the inverses of its
// commands before and the revisions that those expect, past the pipeline's
// bound (Options.MaxUndoBytes), which the pipeline refuses the command for
// once the inverse returns, or once the command's Apply reports a change of
// an entity whose revision its own undo expects past it (Changed). An Inverse
// whose steps hold a part of the project that grows with it, as the values
// that an attribute's retype loses, reads it to stop with ErrUndoTooLarge
// once what it holds takes more, rather than hold all of it only to be
// refused.
func (t *Tx) UndoRoom() int {
	return t.maxUndo - t.undoBytes
}

// Changed reports that the command changed the entity of the kind with the
// ID, which now has the revision rev, so that the operation's entity_refs,
// and the batch's Result, name it with that revision. An entity that several
// commands of the batch change is named once, with its latest revision. It
// returns an error, which the command returns to refuse the batch, unless it
// is called from the command's Apply, which alone changes anything (not from
// an Inverse, nor from an Entity's functions), the command touches the kind
// (Spec.Touches), and rev is above every revision of the entity that the
// batch has seen, as it is when the command incremented it; and it returns
// ErrUndoTooLarge, which the pipeline refuses the command for as one whose
// inverse is too large, once the revision of the entity, which the command's
// undo expects, takes the batch's undo past the pipeline's bound
// (Options.MaxUndoBytes), so that a command that changes more entities than
// its undo could expect stops there, rather than change all of them only to
// be refused.
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
	if _, ok := t.owned[ref]; !ok {
		t.owned[ref] = struct{}{}
		t.own = append(t.own, ref)
		if slices.Contains(t.expecting, kind) {
			if t.undoBytes += ExpectedRevBytes(ref); t.undoBytes > t.maxUndo {
				return ErrUndoTooLarge
			}
		}
	}
	if i, ok := t.at[ref]; ok {
		t.changed[i].Rev = rev
		return nil
	}
	t.at[ref] = len(t.changed)
	t.changed = append(t.changed, Revision{Ref: ref, Rev: rev})
	return nil
}

// Restored reports that the command restored the entity of the kind with the
// ID from the project's trash, as node.restore does a node, and board.restore
// a board, so that once every command of the batch has applied, the pipeline
// asks the reconnections of the kinds that refer to the kind
// (RegisterReconnection) which of what refers to it are still not
// reconnected (Result.NotReconnected). An entity that several commands of the
// batch restore is asked about once. It returns an error, which the command
// returns to refuse the batch, unless it is called from the command's Apply,
// and the command touches the kind (Spec.Touches), as Changed does.
func (t *Tx) Restored(kind ids.Kind, id ids.UUID) error {
	switch {
	case t.current == nil:
		return errors.New("commands: Restored is called outside a command's Apply")
	case !slices.Contains(t.current.touches, kind):
		return fmt.Errorf("commands: %s restored a %s, which it does not touch", t.current.typ, kind)
	}
	ref := Ref{Kind: kind, ID: id}
	if _, ok := t.reported[ref]; ok {
		return nil
	}
	if t.restored == nil {
		t.restored, t.reported = map[ids.Kind][]ids.UUID{}, map[Ref]struct{}{}
	}
	t.reported[ref] = struct{}{}
	t.restored[kind] = append(t.restored[kind], id)
	return nil
}

// notReconnected returns what refers to the entities that the batch's
// commands restored (Restored) that the batch could not reconnect, once every
// command of the batch has applied, as each reconnection of a kind that
// refers to a restored kind finds it (Reconnection.Left): in the order of the
// restored kinds, and of the reconnections of each kind's referrers, as they
// were registered, each entity once, as the first reconnection that leaves it
// leaves it, so that a connection of a node that a batch restores with the
// node's board is listed once. It is never nil, as the Result of a batch
// answered from its record is not.
func (t *Tx) notReconnected(ctx context.Context) ([]Unreconnected, error) {
	out := []Unreconnected{}
	listed := map[Ref]struct{}{}
	for _, kind := range slices.Sorted(maps.Keys(t.restored)) {
		for _, rc := range t.pipeline.registry.reconnections {
			if rc.Restored != kind {
				continue
			}
			left, err := rc.Left(ctx, t, t.restored[kind])
			if err != nil {
				return nil, fmt.Errorf("commands: what the restored %s entities leave of %s: %w", kind, rc.Kind, err)
			}
			for _, u := range left {
				if u.Kind != rc.Kind {
					return nil, fmt.Errorf("commands: the reconnection of %s to %s left %s, which is not of its kind",
						rc.Kind, kind, encodeRef(u.Ref))
				}
				if _, ok := listed[u.Ref]; ok {
					continue
				}
				listed[u.Ref] = struct{}{}
				out = append(out, u)
			}
		}
	}
	return out, nil
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
	case delta > 0 && entitlements.LimitKey(key).Checked() &&
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

// AddUsageOnceApplied reports that the command adds to the workspace's usage
// of key what count returns once every command of the batch has applied, as
// AddUsage adds a delta, for a delta that depends on what the batch's later
// commands do: as deleting a project takes its boards from the workspace's,
// among them those that a later command of the batch creates or restores,
// whose own deltas add them, so that the batch's deltas add up to the same
// whatever the order of its commands. The pipeline calls count in the batch's
// transaction, after the last command's Apply, and adds what it returns as
// the command's AddUsage would, which may refuse the batch then. A batch of a
// project in the trash, which applies only because a command of it restores
// the project (Spec.Restores), adds to a key that such a command counts so
// what the counts return alone, and what its commands added to the key with
// AddUsage counts for nothing: the project added nothing to the key before
// the batch, as it was in the trash, and its restoration counts what the
// project adds to it once the batch has applied, which those deltas, of
// changes that they count as of a project not in the trash, are part of
// already. It returns an error, which the command returns to refuse the
// batch, unless it is called from the command's Apply.
func (t *Tx) AddUsageOnceApplied(key usage.Key, count func(ctx context.Context) (int64, error)) error {
	if t.current == nil {
		return errors.New("commands: AddUsageOnceApplied is called outside a command's Apply")
	}
	t.counts = append(t.counts, usageCount{spec: t.current, key: key, count: count})
	return nil
}

// addCounts adds to the workspace's usage what each count that the batch's
// commands reported (AddUsageOnceApplied) returns, once every command of the
// batch has applied, as the command that reported it would add it, and, in a
// batch of a project in the trash, drops what the commands added with
// AddUsage to each key that a command that restores the project counts so.
func (t *Tx) addCounts(ctx context.Context) error {
	if t.trashed {
		for _, c := range t.counts {
			// A key that no command added to has no sum, which AddUsage below
			// takes for its first report.
			if _, added := t.usage[c.key]; added && c.spec.restores {
				t.usage[c.key] = 0
			}
		}
	}
	for _, c := range t.counts {
		delta, err := c.count(ctx)
		if err != nil {
			return fmt.Errorf("commands: what %s adds to %s: %w", c.spec.typ, c.key, err)
		}
		t.current = c.spec
		err = t.AddUsage(c.key, delta)
		t.current = nil
		if err != nil {
			return err
		}
	}
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
		if added := t.usage[key]; added > 0 && limit.Checked() && added > t.checked[limit] {
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
	e.ActorUserID, e.ActorTokenID = t.actor.UserID, t.actor.Token.TokenID()
	e.IP, e.UserAgent = t.actor.Client.IP, t.actor.Client.UserAgent
	return audit.Record(ctx, t.tx, e)
}
