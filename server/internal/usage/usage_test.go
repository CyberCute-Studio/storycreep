package usage_test

import (
	"context"
	"io/fs"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// fixture is a database of the test's own, with the workspaces' service,
// which adds and removes members as the server does, recording each in the
// workspace's usage.
type fixture struct {
	d   *dbtest.Database
	db  *database.DB
	svc *workspace.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d := dbtest.New(t)
	db := d.Open(t)
	return &fixture{d: d, db: db, svc: workspace.New(db, workspace.Options{PlanKey: entitlements.Unlimited,
		DefaultLocale: language.English, Entitlements: entitlements.New(entitlements.Options{Meter: usage.Meter{}})})}
}

// user adds an active account, as the superuser, and returns its ID.
func (f *fixture) user(t *testing.T) ids.UUID {
	t.Helper()
	id, err := isolationtest.NewUser(context.Background(), f.d.Admin(t))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// workspace creates a workspace with members members, its owner the first,
// and returns its ID and its owner's.
func (f *fixture) workspace(t *testing.T, members int) (id, owner ids.UUID) {
	t.Helper()
	ctx := context.Background()
	owner = f.user(t)
	w, err := f.svc.Create(ctx, owner, workspace.Settings{Name: "Harbour"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	for range members - 1 {
		if _, err := f.svc.Grant(ctx, w.ID, f.user(t), role.Editor); err != nil {
			t.Fatal(err)
		}
	}
	return w.ID, owner
}

// used returns the workspace's usage of key, read in a transaction of the
// workspace.
func (f *fixture) used(t *testing.T, ws ids.UUID, key usage.Key) int64 {
	t.Helper()
	var used map[usage.Key]int64
	if err := f.db.InTx(context.Background(), database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		var err error
		used, err = usage.Read(context.Background(), tx, ws)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return used[key]
}

// count returns what the query counts, as the superuser.
func (f *fixture) count(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := f.d.Admin(t).QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// members returns how many memberships the workspace has: a full recount of
// its members.
func (f *fixture) members(t *testing.T, ws ids.UUID) int64 {
	t.Helper()
	return f.count(t, "SELECT count(*) FROM membership WHERE workspace_id = $1", ws)
}

// counter returns the value of the workspace's counter of key, or -1 if it
// has none.
func (f *fixture) counter(t *testing.T, ws ids.UUID, key usage.Key) int64 {
	t.Helper()
	return f.count(t, "SELECT coalesce((SELECT value FROM usage_counter WHERE workspace_id = $1 AND key = $2), -1)",
		ws, string(key))
}

// pending returns how many of the workspace's deltas no rollup has folded.
func (f *fixture) pending(t *testing.T, ws ids.UUID) int64 {
	t.Helper()
	return f.count(t, "SELECT count(*) FROM usage_delta WHERE workspace_id = $1", ws)
}

// rollUp runs the rollup job's work.
func (f *fixture) rollUp(t *testing.T) {
	t.Helper()
	if err := usage.RollUp(context.Background(), f.db); err != nil {
		t.Fatal(err)
	}
}

// TestRollUpEqualsRecount checks that the usage of members, read before a
// rollup, from the counter and the pending deltas, is a full recount of the
// workspace's memberships, and that after the rollup the counter alone is,
// with no delta left, while another workspace's usage is its own.
//
// Verifies: FR-ENT-02/AC4
func TestRollUpEqualsRecount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	a, owner := f.workspace(t, 4)
	b, _ := f.workspace(t, 2)
	// A removal takes a member away.
	gone := f.user(t)
	if _, err := f.svc.Grant(ctx, a, gone, role.Viewer); err != nil {
		t.Fatal(err)
	}
	ms, err := f.svc.Members(ctx, owner, a, ids.UUID{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.UserID == gone {
			if err := f.svc.RemoveMember(ctx, owner, a, m.MembershipID, audit.Client{}); err != nil {
				t.Fatal(err)
			}
		}
	}

	for _, ws := range []ids.UUID{a, b} {
		recount := f.members(t, ws)
		if got := f.used(t, ws, usage.Members); got != recount {
			t.Errorf("before the rollup, the usage of members is %d, want the recount, %d", got, recount)
		}
		if f.counter(t, ws, usage.Members) != -1 || f.pending(t, ws) == 0 {
			t.Errorf("before the rollup, a counter exists, or no delta is pending")
		}
	}
	f.rollUp(t)
	for _, ws := range []ids.UUID{a, b} {
		recount := f.members(t, ws)
		if got := f.counter(t, ws, usage.Members); got != recount {
			t.Errorf("after the rollup, the counter of members is %d, want the recount, %d", got, recount)
		}
		if n := f.pending(t, ws); n != 0 {
			t.Errorf("after the rollup, %d deltas are pending, want none", n)
		}
		if got := f.used(t, ws, usage.Members); got != recount {
			t.Errorf("after the rollup, the usage of members is %d, want the recount, %d", got, recount)
		}
		for _, key := range usage.Keys() {
			if got := f.used(t, ws, key); key != usage.Members && got != 0 {
				t.Errorf("the usage of %s is %d, want 0", key, got)
			}
		}
	}
	// Folding nothing changes nothing.
	f.rollUp(t)
	if got, recount := f.counter(t, a, usage.Members), f.members(t, a); got != recount {
		t.Errorf("after a second rollup, the counter of members is %d, want %d", got, recount)
	}
}

// open runs fn in a transaction of the workspace that stays open until the
// returned release is called, and returns release and a channel that
// receives the transaction's error once it has ended. It fails the test if
// fn fails.
func (f *fixture) open(t *testing.T, ws ids.UUID, fn func(tx pgx.Tx) error) (release func(), done <-chan error) {
	t.Helper()
	ran, stop, end := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		end <- f.db.InTx(context.Background(), database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
			if err := fn(tx); err != nil {
				return err
			}
			close(ran)
			<-stop
			return nil
		})
	}()
	select {
	case <-ran:
	case err := <-end:
		t.Fatalf("the open transaction ended at once: %v", err)
	}
	var once bool
	release = func() {
		if !once {
			once = true
			close(stop)
		}
	}
	t.Cleanup(release)
	return release, end
}

// TestRollUpLeavesOpenDeltas checks that a rollup that runs and commits
// while a change that recorded a delta is still open neither waits for the
// change nor folds its delta, which the next rollup folds, and that the
// delta is counted once all along: not before the change commits, as
// pending once it has, and in the counter after the next rollup.
//
// Verifies: FR-ENT-02/AC5
func TestRollUpLeavesOpenDeltas(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ws, _ := f.workspace(t, 3)
	f.rollUp(t)
	before := f.used(t, ws, usage.Members)

	release, done := f.open(t, ws, func(tx pgx.Tx) error {
		return usage.Record(context.Background(), tx, ws, usage.Members, 1)
	})
	// The rollup must not wait for the open change, so a deadline that a
	// wait would pass fails it.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := usage.RollUp(ctx, f.db); err != nil {
		t.Fatalf("the rollup, while a change is open: %v", err)
	}
	if got := f.used(t, ws, usage.Members); got != before {
		t.Errorf("while the change is open, the usage is %d, want %d", got, before)
	}
	if got := f.counter(t, ws, usage.Members); got != before {
		t.Errorf("the rollup that ran while the change was open left the counter at %d, want %d", got, before)
	}

	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := f.used(t, ws, usage.Members); got != before+1 {
		t.Errorf("once the change commits, the usage is %d, want %d", got, before+1)
	}
	if got, n := f.counter(t, ws, usage.Members), f.pending(t, ws); got != before || n != 1 {
		t.Errorf("once the change commits, the counter is %d with %d deltas pending, want %d with 1", got, n, before)
	}

	f.rollUp(t)
	if got, n := f.counter(t, ws, usage.Members), f.pending(t, ws); got != before+1 || n != 0 {
		t.Errorf("after the next rollup, the counter is %d with %d deltas pending, want %d with none", got, n,
			before+1)
	}
	if got := f.used(t, ws, usage.Members); got != before+1 {
		t.Errorf("after the next rollup, the usage is %d, want %d", got, before+1)
	}
}

// TestReconcileCorrectsDrift checks that reconciliation corrects a
// workspace's usage of members that was deliberately corrupted, in its
// counter, with a delta that no change made, and by a membership deleted
// without one, as a cascade deletes it, to a recount, with a delta that the
// next rollup folds; that it leaves alone a workspace whose usage is right;
// and that it corrects nothing when run again.
//
// Verifies: FR-ENT-02/AC1
func TestReconcileCorrectsDrift(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	a, owner := f.workspace(t, 4)
	b, _ := f.workspace(t, 2)
	f.rollUp(t)
	admin := f.d.Admin(t)
	for _, corrupt := range []struct {
		sql  string
		args []any
	}{
		{"UPDATE usage_counter SET value = 40 WHERE workspace_id = $1 AND key = 'members'", []any{a}},
		{"INSERT INTO usage_delta (id, workspace_id, key, delta) VALUES ($1, $2, 'members', 7)", []any{ids.New(), a}},
		{"DELETE FROM membership WHERE id = (SELECT id FROM membership WHERE workspace_id = $1 AND user_id <> $2 " +
			"LIMIT 1)", []any{a, owner}},
	} {
		if _, err := admin.Exec(ctx, corrupt.sql, corrupt.args...); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.used(t, a, usage.Members); got == f.members(t, a) {
		t.Fatalf("the corrupted usage is %d, which is the recount", got)
	}

	corrected, err := usage.Reconcile(ctx, f.db)
	if err != nil {
		t.Fatal(err)
	}
	if corrected != 1 {
		t.Errorf("reconciliation corrected %d keys, want 1", corrected)
	}
	for _, ws := range []ids.UUID{a, b} {
		if got, recount := f.used(t, ws, usage.Members), f.members(t, ws); got != recount {
			t.Errorf("after reconciliation, the usage of members is %d, want the recount, %d", got, recount)
		}
	}
	f.rollUp(t)
	if got, recount := f.counter(t, a, usage.Members), f.members(t, a); got != recount || f.pending(t, a) != 0 {
		t.Errorf("after the rollup, the counter is %d with %d deltas pending, want the recount, %d, with none", got,
			f.pending(t, a), recount)
	}
	if corrected, err := usage.Reconcile(ctx, f.db); err != nil || corrected != 0 {
		t.Errorf("a second reconciliation corrected %d keys, %v; want none", corrected, err)
	}
}

// advisoryWaits returns how many sessions wait for an advisory lock of
// usage.
func (f *fixture) advisoryWaits(t *testing.T) int64 {
	t.Helper()
	return f.count(t, "SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted AND classid = $1",
		database.UsageLock)
}

// advisoryLocks returns how many advisory locks of usage are held.
func (f *fixture) advisoryLocks(t *testing.T) int64 {
	t.Helper()
	return f.count(t, "SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND granted AND classid = $1",
		database.UsageLock)
}

// meter reads the usage of key through the meter in tx.
func meter(ctx context.Context, tx pgx.Tx, ws ids.UUID, key entitlements.LimitKey) error {
	_, err := usage.Meter{}.Usage(ctx, tx, ws, key)
	return err
}

// TestExactKeysTakeTurns checks that reading a key that is counted exactly
// through the meter, as checking its limit does, holds the workspace's key
// until the transaction ends, so that another check of the key waits for it,
// while reading one that is counted approximately takes no lock, so that the
// changes that add to it never wait for each other (FR-ENT-02).
func TestExactKeysTakeTurns(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ws, _ := f.workspace(t, 1)
	ctx := context.Background()

	release, done := f.open(t, ws, func(tx pgx.Tx) error { return meter(ctx, tx, ws, entitlements.Members) })
	second := make(chan error, 1)
	go func() {
		second <- f.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
			return meter(ctx, tx, ws, entitlements.Members)
		})
	}()
	for deadline := time.Now().Add(time.Minute); f.advisoryWaits(t) == 0; time.Sleep(20 * time.Millisecond) {
		select {
		case err := <-second:
			t.Fatalf("a second check of members did not wait for the first: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("a second check of members never waits for the first")
		}
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}

	release, done = f.open(t, ws, func(tx pgx.Tx) error { return meter(ctx, tx, ws, entitlements.StorageBytes) })
	if n := f.advisoryLocks(t); n != 0 {
		t.Errorf("reading storage_bytes holds %d locks of usage, want none", n)
	}
	other, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := f.db.InTx(other, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		return meter(other, tx, ws, entitlements.StorageBytes)
	}); err != nil {
		t.Errorf("a second read of storage_bytes, while the first is open: %v", err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestRecordAndRead checks that Record refuses a key that is not one and
// records nothing for no change, that Read gives every key, 0 for one
// without a counter or a delta, and leaves out a key that this release does
// not know, and that the meter refuses a limit that is not metered.
func TestRecordAndRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	ws, _ := f.workspace(t, 2)
	if _, err := f.d.Admin(t).Exec(ctx, `INSERT INTO usage_delta (id, workspace_id, key, delta)
		VALUES ($1, $2, 'retired_things', 5)`, ids.New(), ws); err != nil {
		t.Fatal(err)
	}
	pending := f.pending(t, ws)
	err := f.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		if err := usage.Record(ctx, tx, ws, "retired_things", 1); err == nil {
			t.Error("Record of a key that is not one succeeded")
		}
		if err := usage.Record(ctx, tx, ws, usage.Nodes, 0); err != nil {
			t.Errorf("Record of no change: %v", err)
		}
		if _, err := (usage.Meter{}).Usage(ctx, tx, ws, "snapshot_retention"); err == nil {
			t.Error("the meter read snapshot_retention, which nothing meters")
		}
		used, err := usage.Read(ctx, tx, ws)
		if err != nil {
			return err
		}
		if len(used) != len(usage.Keys()) {
			t.Errorf("Read gives %d keys, want %d: %v", len(used), len(usage.Keys()), used)
		}
		for _, key := range usage.Keys() {
			want := int64(0)
			if key == usage.Members {
				want = 2
			}
			if got, ok := used[key]; !ok || got != want {
				t.Errorf("Read gives %s %d (%v), want %d", key, got, ok, want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := f.pending(t, ws); n != pending {
		t.Errorf("recording no change added %d deltas", n-pending)
	}
	for _, k := range usage.Keys() {
		if exact := k == usage.Projects || k == usage.Members || k == usage.Snapshots; k.Exact() != exact {
			t.Errorf("%s: Exact is %v, want %v", k, k.Exact(), exact)
		}
	}
}

// TestIsolation tries the queries of usage from another workspace's
// transaction, where row-level security must keep them from reading,
// folding, or adding another workspace's usage (NFR-SEC-01).
//
// Verifies: NFR-SEC-01
func TestIsolation(t *testing.T) {
	t.Parallel()
	fx := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	ws := func(target *isolationtest.Workspace) queries.KeyUsageParams {
		return queries.KeyUsageParams{WorkspaceID: uuid(target.ID), Key: string(usage.Members)}
	}
	// What a query read of the target's usage, which the fixture seeded: a
	// counter, a delta of members, and a membership.
	seeded := func(target *isolationtest.Workspace) []ids.UUID {
		return []ids.UUID{target.Rows["usage_counter"], target.Rows["usage_delta"], target.Rows["membership"]}
	}
	for _, op := range []isolationtest.Op{
		{Name: "the usage of a key", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			used, err := queries.New(tx).KeyUsage(ctx, ws(target))
			if err != nil || used == 0 {
				return nil, err
			}
			return seeded(target), nil
		}},
		{Name: "the usage of every key", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			rows, err := queries.New(tx).WorkspaceUsage(ctx, uuid(target.ID))
			if err != nil || len(rows) == 0 {
				return nil, err
			}
			return seeded(target), nil
		}},
		{Name: "the drift of members", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			drift, err := queries.New(tx).MemberDrift(ctx, uuid(target.ID))
			if err != nil || drift == 0 {
				return nil, err
			}
			return seeded(target), nil
		}},
		{Name: "a fold of the deltas", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			sums, err := queries.New(tx).FoldUsageDeltas(ctx, uuid(target.ID))
			if err != nil || len(sums) == 0 {
				return nil, err
			}
			return []ids.UUID{target.Rows["usage_delta"]}, nil
		}},
		{Name: "a delta", Denied: true, Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			id := ids.New()
			return []ids.UUID{id}, queries.New(tx).InsertUsageDelta(ctx, queries.InsertUsageDeltaParams{ID: uuid(id),
				WorkspaceID: uuid(target.ID), Key: string(usage.Nodes), Delta: 1})
		}},
		{Name: "a counter", Denied: true, Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			id := ids.New()
			return []ids.UUID{id}, queries.New(tx).AddUsageCounters(ctx, queries.AddUsageCountersParams{
				Ids: []pgtype.UUID{uuid(id)}, WorkspaceID: uuid(target.ID), Keys: []string{string(usage.Nodes)},
				Totals: []int64{1}})
		}},
	} {
		fx.Check(t, op)
	}
}

// TestMigrationCountsMembers checks that the migration that adds usage
// records the memberships that a database has already, guests' included, as
// each workspace's usage of members, with an ID that is a UUIDv7.
func TestMigrationCountsMembers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	before, all := fstest.MapFS{}, database.Migrations()
	names, err := fs.Glob(all, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name >= "00013" {
			continue
		}
		data, err := fs.ReadFile(all, name)
		if err != nil {
			t.Fatal(err)
		}
		before[name] = &fstest.MapFile{Data: data}
	}
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{Migrations: before}); err != nil {
		t.Fatal(err)
	}
	admin := d.Admin(t)
	counts := map[ids.UUID]int{}
	for _, n := range []int{3, 1} {
		ws := ids.New()
		if _, err := admin.Exec(ctx, "INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', $2, "+
			"'unlimited')", ws, "harbour-"+ws.String()); err != nil {
			t.Fatal(err)
		}
		for i := range n {
			user, err := isolationtest.NewUser(ctx, admin)
			if err != nil {
				t.Fatal(err)
			}
			r := "guest"
			if i == 0 {
				r = "owner"
			}
			if _, err := admin.Exec(ctx, "INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, $4)",
				ids.New(), ws, user, r); err != nil {
				t.Fatal(err)
			}
		}
		counts[ws] = n
	}
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{}); err != nil {
		t.Fatal(err)
	}
	rows, err := admin.Query(ctx, "SELECT id, workspace_id, key, delta FROM usage_delta")
	if err != nil {
		t.Fatal(err)
	}
	type delta struct {
		ID, Workspace ids.UUID
		Key           string
		Delta         int64
	}
	got, err := pgx.CollectRows(rows, pgx.RowToStructByPos[delta])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(counts) {
		t.Fatalf("the migration recorded %d deltas, want one of each of the %d workspaces", len(got), len(counts))
	}
	for _, g := range got {
		if g.Key != string(usage.Members) || g.Delta != int64(counts[g.Workspace]) {
			t.Errorf("the migration recorded %s %d for a workspace with %d members", g.Key, g.Delta, counts[g.Workspace])
		}
		if g.ID[6]>>4 != 7 || g.ID[8]>>6 != 2 {
			t.Errorf("the delta's ID %s is not a UUIDv7", g.ID)
		}
		if ms := time.UnixMilli(int64(g.ID[0])<<40 | int64(g.ID[1])<<32 | int64(g.ID[2])<<24 | int64(g.ID[3])<<16 |
			int64(g.ID[4])<<8 | int64(g.ID[5])); time.Since(ms).Abs() > time.Hour {
			t.Errorf("the delta's ID %s has the time %s", g.ID, ms)
		}
	}
}

// uuid returns id as a uuid column.
func uuid(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
