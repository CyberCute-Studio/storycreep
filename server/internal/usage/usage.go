// Package usage meters how much of each thing that the plans meter a
// workspace uses (PRD FR-ENT-02, §5.7, ADR-010): its projects, boards, nodes,
// members, stored bytes, snapshots, and the requests that its API tokens
// make. The entitlement layer checks the
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
// counted exactly: reading one through Meter, as only the check of its limit
// does, takes a transaction-scoped advisory lock of the workspace's key
// first, which serializes the changes that check the key's limit, so that
// two cannot both pass the check against the same usage. No limit of the
// plans checks snapshots yet, so nothing takes its lock until one does. The
// others are counted approximately, without a lock, so
// that the changes that add to them often, as nodes, never wait for each
// other (AC3); of these, only storage_bytes is a limit, which a burst of
// changes that run at once may pass by what they add together. Plan limits
// are commercial, not safety limits, so that is accepted.
//
// The jobs work on one workspace at a time, in a transaction of that
// workspace, as package sweep runs them (§5.4), for as long as a run has time
// for, from a workspace picked at random, so that the workspaces that one run
// does not reach, another does.
package usage

import (
	"context"
	"fmt"
	"hash/fnv"
	"slices"

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

// The keys.
const (
	// Projects is how many projects a workspace has that are neither archived
	// nor in its trash, which alone count towards its projects limit (PRD
	// FR-PROJ-01, package project). It is counted exactly.
	Projects Key = "projects"
	// Boards is how many boards a workspace's projects have.
	Boards Key = "boards"
	// Nodes is how many nodes a workspace's projects have.
	Nodes Key = "nodes"
	// Members is how many memberships a workspace has, guests' included. It
	// is counted exactly.
	Members Key = "members"
	// StorageBytes is how many bytes a workspace's stored files take up:
	// each blob of its assets once, however many assets share it, but not
	// the thumbnails that the server derives (package asset).
	StorageBytes Key = "storage_bytes"
	// Snapshots is how many snapshots a workspace's projects have. It is
	// counted exactly.
	Snapshots Key = "snapshots"
	// APIRequests is how many requests the workspace's API tokens have made
	// (plan note N31): each request that a token is accepted for adds one,
	// in the transaction that accepts it (package apitoken). It is a total,
	// which no recount can correct, because nothing but its deltas records
	// the requests, so reconciliation leaves it alone, and no limit of the
	// plans checks it (ADR-010).
	APIRequests Key = "api_requests"
)

// keys are the keys, in the order that the API lists them.
var keys = []Key{Projects, Boards, Nodes, Members, StorageBytes, Snapshots, APIRequests}

// Keys returns the keys, in the order that the API lists them.
func Keys() []Key {
	return slices.Clone(keys)
}

// Known reports whether k is one of the keys.
func (k Key) Known() bool {
	return slices.Contains(keys, k)
}

// Exact reports whether k is counted exactly, under a lock of the workspace's
// key that the check of its limit takes (Meter), rather than approximately.
func (k Key) Exact() bool {
	return k == Projects || k == Members || k == Snapshots
}

// Record records that a change adds delta to the workspace's usage of key,
// or takes -delta from it, in tx, the change's transaction, which is the
// workspace's. Adding nothing records nothing.
func Record(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, key Key, delta int64) error {
	return record(ctx, tx, workspaceID, key, delta, false)
}

// RecordUnflushed records a delta as Record does, and makes tx commit without
// waiting for the disk, as a rate limit's count does (package ratelimit), so
// that a crash of the database may lose the last few hundred milliseconds of
// such commits, the delta with whatever else tx writes. It is for a key that
// is counted approximately, whose deltas come with requests that would each
// wait for the disk otherwise, in a transaction that writes nothing else that
// must outlive such a crash, as the API requests that package apitoken
// records as it accepts a token. It refuses a key that is counted exactly
// (Exact), whose limit's check needs every delta.
func RecordUnflushed(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, key Key, delta int64) error {
	if key.Exact() {
		return fmt.Errorf("usage: %s is counted exactly, so its deltas wait for the disk", key)
	}
	return record(ctx, tx, workspaceID, key, delta, true)
}

// record records a delta for Record, or, if unflushed is set, for
// RecordUnflushed.
func record(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, key Key, delta int64, unflushed bool) error {
	if !key.Known() {
		return fmt.Errorf("usage: %q is not a key", key)
	}
	if delta == 0 {
		return nil
	}
	q := queries.New(tx)
	id, ws := nullUUID(ids.New()), nullUUID(workspaceID)
	var err error
	if unflushed {
		err = q.InsertUnflushedUsageDelta(ctx, queries.InsertUnflushedUsageDeltaParams{ID: id, WorkspaceID: ws,
			Key: string(key), Delta: delta})
	} else {
		err = q.InsertUsageDelta(ctx, queries.InsertUsageDeltaParams{ID: id, WorkspaceID: ws, Key: string(key),
			Delta: delta})
	}
	if err != nil {
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

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
