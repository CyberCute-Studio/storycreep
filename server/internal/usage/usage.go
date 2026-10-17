// Package usage meters how much of each thing that the plans meter a
// workspace uses (PRD FR-ENT-02, §5.7, ADR-010): its projects, boards, nodes,
// members, stored bytes, and snapshots. The entitlement layer checks the
// plan's limits against it (package entitlements, through Meter), and the
// API shows it to the workspace's admins.
//
// A change records what it adds to a key, or takes from it, with Record, as
// a row of usage_delta in the change's own transaction, so that the delta
// commits exactly when the change does, and it never updates a counter in
// place, so that changes that run at once in one workspace wait for no row of
// usage. A workspace's usage of a key is its counter's value plus the sum of
// its deltas still present, read in one statement, and so in one snapshot.
// The rollup job (RollUp) folds the deltas into the counters: in one
// transaction, it deletes the deltas that it can see and adds their sum to
// the counters. A delta whose change has not committed yet is invisible to
// that delete, so it stays, and the next run folds it, and every delta is
// counted exactly once, in the counter or as pending (FR-ENT-02 AC4, AC5).
// The reconciliation job (Reconcile) corrects the drift of a key from a
// recount of what it counts, as after a cascade that deleted rows without a
// delta, with a delta of the difference, so that the rollup job stays the
// only writer of the counters (AC1).
//
// The keys that are created rarely, projects, members, and snapshots, are
// counted exactly: reading one through Meter takes a transaction-scoped
// advisory lock of the workspace's key first, which serializes the changes
// that check the key's limit, so that two cannot both pass the check against
// the same usage. The others are counted approximately, without a lock, so
// that the changes that add to them often, as nodes, never wait for each
// other (AC3); of these, only storage_bytes is a limit, which a burst of
// changes that run at once may pass by what they add together. Plan limits
// are commercial, not safety limits, so that is accepted.
//
// The jobs work on one workspace at a time, in a transaction of that
// workspace, listing the workspaces from the workspace table, which has no
// row-level security (§5.4). A job has a minute to run (package jobs), which
// an instance with many workspaces would need more than to work on all of
// them, so a run stops starting workspaces once it has run for passBudget,
// and goes on from a workspace picked at random, past the last to the first,
// so that the workspaces that one run does not reach, another does.
package usage

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Key names a thing that the plans meter. A key that names a limit of the
// plans is spelled as the plan file spells the limit (entitlements.LimitKey).
type Key string

// The keys. API requests are metered with API tokens (plan item 27).
const (
	// Projects is how many projects a workspace has. It is counted exactly.
	Projects Key = "projects"
	// Boards is how many boards a workspace's projects have.
	Boards Key = "boards"
	// Nodes is how many nodes a workspace's projects have.
	Nodes Key = "nodes"
	// Members is how many memberships a workspace has, guests' included. It
	// is counted exactly.
	Members Key = "members"
	// StorageBytes is how many bytes a workspace's stored files take up.
	StorageBytes Key = "storage_bytes"
	// Snapshots is how many snapshots a workspace's projects have. It is
	// counted exactly.
	Snapshots Key = "snapshots"
)

// keys are the keys, in the order that the API lists them.
var keys = []Key{Projects, Boards, Nodes, Members, StorageBytes, Snapshots}

// Keys returns the keys, in the order that the API lists them.
func Keys() []Key {
	return slices.Clone(keys)
}

// Known reports whether k is one of the keys.
func (k Key) Known() bool {
	return slices.Contains(keys, k)
}

// Exact reports whether k is counted exactly, under a lock of the workspace's
// key, rather than approximately.
func (k Key) Exact() bool {
	return k == Projects || k == Members || k == Snapshots
}

// Record records that a change adds delta to the workspace's usage of key,
// or takes -delta from it, in tx, the change's transaction, which is the
// workspace's. Adding nothing records nothing.
func Record(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, key Key, delta int64) error {
	if !key.Known() {
		return fmt.Errorf("usage: %q is not a key", key)
	}
	if delta == 0 {
		return nil
	}
	if err := queries.New(tx).InsertUsageDelta(ctx, queries.InsertUsageDeltaParams{ID: nullUUID(ids.New()),
		WorkspaceID: nullUUID(workspaceID), Key: string(key), Delta: delta}); err != nil {
		return fmt.Errorf("usage: record the change of %s: %w", key, err)
	}
	return nil
}

// Read returns the workspace's usage of every key, in tx, a transaction of
// the workspace, read in one statement. It takes no lock.
func Read(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID) (map[Key]int64, error) {
	rows, err := queries.New(tx).WorkspaceUsage(ctx, nullUUID(workspaceID))
	if err != nil {
		return nil, fmt.Errorf("usage: read the usage: %w", err)
	}
	used := make(map[Key]int64, len(keys))
	for _, k := range keys {
		used[k] = 0
	}
	for _, r := range rows {
		// A key that this release does not know, as one that a newer release
		// recorded, is not shown.
		if k := Key(r.Key); k.Known() {
			used[k] = r.Used
		}
	}
	return used, nil
}

// Meter reads the usage that the entitlement layer checks a limit against
// (entitlements.Meter). For a key that is counted exactly, it first takes the
// lock of the workspace's key until tx ends, so that the change that checks
// the limit in tx and the others that check it at once in the workspace take
// turns, each reading the usage that the ones before it left.
type Meter struct{}

var _ entitlements.Meter = Meter{}

// Usage returns the workspace's usage of the limit key, in tx, a transaction
// of the workspace, under the lock of the workspace's key if it is counted
// exactly.
func (Meter) Usage(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, key entitlements.LimitKey) (int64,
	error) {
	k := Key(key)
	if !k.Known() {
		return 0, fmt.Errorf("usage: the limit %s is not a key that is metered", key)
	}
	q := queries.New(tx)
	if k.Exact() {
		if err := lock(ctx, q, workspaceID, k); err != nil {
			return 0, err
		}
	}
	used, err := q.KeyUsage(ctx, queries.KeyUsageParams{WorkspaceID: nullUUID(workspaceID), Key: string(k)})
	if err != nil {
		return 0, fmt.Errorf("usage: read the usage of %s: %w", k, err)
	}
	return used, nil
}

// lock takes the advisory lock of the workspace's key until the transaction
// of q ends. Its second integer is a hash of the workspace and the key, so
// two workspaces' keys may share one, which only makes their changes take
// turns.
func lock(ctx context.Context, q *queries.Queries, workspaceID ids.UUID, key Key) error {
	h := fnv.New32a()
	h.Write(workspaceID[:])
	h.Write([]byte(key))
	if err := q.LockUsage(ctx, queries.LockUsageParams{Class: database.UsageLock,
		Object: int32(h.Sum32())}); err != nil {
		return fmt.Errorf("usage: lock the usage of %s: %w", key, err)
	}
	return nil
}

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// workspacePage is how many workspaces' IDs the jobs read at once
// (eachWorkspace).
const workspacePage = 1000

// passBudget is how long a run of a job of usage starts workspaces' work for.
// It is well inside the minute that a job has (package jobs), whose end would
// fail the run, and attempt it again from the start, so that on an instance
// with more workspaces than a minute's work, no run would reach the last
// ones.
const passBudget = 40 * time.Second

// within returns a function that reports whether budget has not passed since
// within was called.
func within(budget time.Duration) func() bool {
	end := time.Now().Add(budget)
	return func() bool { return time.Now().Before(end) }
}

// eachWorkspace calls fn with the ID of each workspace, whatever its status,
// for as long as more reports that the run has time left, reading the IDs
// pageSize at a time: in the order of their IDs, from the one after a
// workspace picked at random to the last, and then from the first to the one
// picked, so that it calls fn once for every workspace if more lets it, and a
// run that ends earlier leaves others than the last run did. It goes on past
// a workspace that fn fails for, and returns the errors of all of them, but
// stops at once when ctx ends.
func eachWorkspace(ctx context.Context, db DB, pageSize int32, more func() bool,
	fn func(workspaceID ids.UUID) error) error {
	var start pgtype.UUID
	err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		if start, err = queries.New(tx).RandomWorkspaceID(ctx); errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	switch {
	case err != nil:
		return fmt.Errorf("usage: pick the workspace to start at: %w", err)
	case !start.Valid:
		// The instance has no workspace.
		return nil
	}
	var errs []error
	for _, span := range []queries.WorkspaceIDsParams{{After: start}, {Through: start}} {
		span.MaxRows = pageSize
		for {
			var page []pgtype.UUID
			err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
				var err error
				page, err = queries.New(tx).WorkspaceIDs(ctx, span)
				return err
			})
			if err != nil {
				return errors.Join(append(errs, fmt.Errorf("usage: list the workspaces: %w", err))...)
			}
			for _, id := range page {
				if err := ctx.Err(); err != nil {
					return errors.Join(append(errs, err)...)
				}
				if !more() {
					return errors.Join(errs...)
				}
				if err := fn(id.Bytes); err != nil {
					errs = append(errs, err)
				}
			}
			if len(page) < int(pageSize) {
				break
			}
			span.After = page[len(page)-1]
		}
	}
	return errors.Join(errs...)
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
