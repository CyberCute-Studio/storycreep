package entitlements_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

const testUpgradeURL = "https://storycreep.example.com/pricing"

// meter is a Meter whose usage is a fixed amount of each limit, which counts
// its reads.
type meter struct {
	usage map[entitlements.LimitKey]int64
	reads int
	err   error
}

func (m *meter) Usage(_ context.Context, _ pgx.Tx, _ ids.UUID, key entitlements.LimitKey) (int64, error) {
	m.reads++
	return m.usage[key], m.err
}

// limitError returns err as a *entitlements.LimitError, or fails the test.
func limitError(t *testing.T, err error) *entitlements.LimitError {
	t.Helper()
	var e *entitlements.LimitError
	if !errors.As(err, &e) {
		t.Fatalf("got %v, want a *entitlements.LimitError", err)
	}
	return e
}

// TestNotEnforcedIsUnlimited checks that, while entitlements are not
// enforced, as in self-hosted mode, every workspace has the unlimited plan:
// no limit, every feature, and no read of the database or of usage, unless
// the instance caps a limit, which then applies.
//
// Verifies: FR-ENT-01/AC2
func TestNotEnforcedIsUnlimited(t *testing.T) {
	ctx := context.Background()
	m := &meter{usage: map[entitlements.LimitKey]int64{entitlements.Members: 1 << 40}}
	// No transaction: an unenforced plan reads nothing.
	var tx pgx.Tx
	ws := ids.New()
	s := entitlements.New(entitlements.Options{Meter: m, UpgradeURL: testUpgradeURL})
	if s.NewWorkspacePlan() != entitlements.Unlimited {
		t.Errorf("a new workspace has the plan %s, want unlimited", s.NewWorkspacePlan())
	}
	for _, key := range entitlements.LimitKeys() {
		if q, err := s.Limit(ctx, tx, ws, key); err != nil || q != entitlements.NoLimit {
			t.Errorf("Limit(%s) = %v, %v; want no limit", key, q, err)
		}
		if err := s.Check(ctx, tx, ws, key, 1<<40); err != nil {
			t.Errorf("Check(%s) = %v", key, err)
		}
	}
	for _, f := range entitlements.Default().Features() {
		if ok, err := s.Enabled(ctx, tx, ws, f); err != nil || !ok {
			t.Errorf("Enabled(%s) = %v, %v; want true", f, ok, err)
		}
	}
	if m.reads != 0 {
		t.Errorf("an unlimited check read the usage %d times", m.reads)
	}

	// The instance's caps apply to the limits that they name.
	s = entitlements.New(entitlements.Options{Meter: m, UpgradeURL: testUpgradeURL,
		Caps: entitlements.Caps{entitlements.Members: 5, entitlements.Projects: 10}})
	if q, err := s.Limit(ctx, tx, ws, entitlements.Members); err != nil || q != entitlements.AtMost(5) {
		t.Errorf("Limit(members) = %v, %v; want the cap of 5", q, err)
	}
	if q, err := s.Limit(ctx, tx, ws, entitlements.StorageBytes); err != nil || q != entitlements.NoLimit {
		t.Errorf("Limit(storage_bytes) = %v, %v; want no limit", q, err)
	}
	m.usage = map[entitlements.LimitKey]int64{entitlements.Members: 5, entitlements.Projects: 7}
	// The cap, not the plan, refuses the change, so no upgrade would help.
	if e := limitError(t, s.Check(ctx, tx, ws, entitlements.Members, 1)); *e != (entitlements.LimitError{
		Key: entitlements.Members, Usage: 5, Limit: 5}) {
		t.Errorf("Check(members) = %+v, want usage 5, limit 5, and no upgrade link", e)
	}
	if err := s.Check(ctx, tx, ws, entitlements.Projects, 3); err != nil {
		t.Errorf("Check(projects, 3) at 7 of 10 = %v", err)
	}
	if e := limitError(t, s.Check(ctx, tx, ws, entitlements.Projects, 4)); e.Usage != 7 || e.Limit != 10 {
		t.Errorf("Check(projects, 4) at 7 of 10 = %+v", e)
	}
	for _, f := range entitlements.Default().Features() {
		if ok, err := s.Enabled(ctx, tx, ws, f); err != nil || !ok {
			t.Errorf("with caps, Enabled(%s) = %v, %v; want true", f, ok, err)
		}
	}
}

// TestUsage checks that a usage read carries the soft warning from 80% of a
// quota that is a number, and never for one that is not (FR-ENT-03).
func TestUsage(t *testing.T) {
	ctx := context.Background()
	ws := ids.New()
	m := &meter{usage: map[entitlements.LimitKey]int64{entitlements.Members: 7, entitlements.Projects: 8,
		entitlements.StorageBytes: 1 << 50}}
	s := entitlements.New(entitlements.Options{Meter: m,
		Caps: entitlements.Caps{entitlements.Members: 10, entitlements.Projects: 10}})
	for _, tt := range []struct {
		key  entitlements.LimitKey
		want entitlements.Usage
	}{
		{entitlements.Members, entitlements.Usage{Key: entitlements.Members, Used: 7,
			Quota: entitlements.AtMost(10)}},
		{entitlements.Projects, entitlements.Usage{Key: entitlements.Projects, Used: 8,
			Quota: entitlements.AtMost(10), Warning: true}},
		{entitlements.StorageBytes, entitlements.Usage{Key: entitlements.StorageBytes, Used: 1 << 50,
			Quota: entitlements.NoLimit}},
	} {
		if got, err := s.Usage(ctx, nil, ws, tt.key); err != nil || got != tt.want {
			t.Errorf("Usage(%s) = %+v, %v; want %+v", tt.key, got, err, tt.want)
		}
	}
}

// TestRefusedKeys checks that the inert limit, which no code checks until
// FR-SNAP-06 (FR-ENT-01 AC1), and keys that the plan file does not define,
// are refused, and that a limit with a quota is not checked without a meter
// that reads its usage.
func TestRefusedKeys(t *testing.T) {
	ctx := context.Background()
	ws := ids.New()
	s := entitlements.New(entitlements.Options{Meter: &meter{}})
	inert := entitlements.LimitKey("snapshot_retention")
	if _, err := s.Limit(ctx, nil, ws, inert); !errors.Is(err, entitlements.ErrInert) {
		t.Errorf("Limit(snapshot_retention) = %v, want ErrInert", err)
	}
	if _, err := s.Usage(ctx, nil, ws, inert); !errors.Is(err, entitlements.ErrInert) {
		t.Errorf("Usage(snapshot_retention) = %v, want ErrInert", err)
	}
	if err := s.Check(ctx, nil, ws, inert, 1); !errors.Is(err, entitlements.ErrInert) {
		t.Errorf("Check(snapshot_retention) = %v, want ErrInert", err)
	}
	if err := s.Check(ctx, nil, ws, "boards", 1); err == nil || errors.Is(err, entitlements.ErrInert) {
		t.Errorf("Check(boards) = %v, want an error", err)
	}
	if _, err := s.Enabled(ctx, nil, ws, "themes"); err == nil {
		t.Errorf("Enabled(themes) = nil error")
	}

	s = entitlements.New(entitlements.Options{Caps: entitlements.Caps{entitlements.Members: 3}})
	if err := s.Check(ctx, nil, ws, entitlements.Members, 1); err == nil {
		t.Errorf("Check without a meter = nil, want an error")
	}
	if err := s.Check(ctx, nil, ws, entitlements.Projects, 1); err != nil {
		t.Errorf("Check of an unlimited limit without a meter = %v, want nil", err)
	}
	failing := &meter{err: errors.New("the counter is unreadable")}
	s = entitlements.New(entitlements.Options{Caps: entitlements.Caps{entitlements.Members: 3}, Meter: failing})
	if err := s.Check(ctx, nil, ws, entitlements.Members, 1); err == nil ||
		!errors.Is(err, failing.err) {
		t.Errorf("Check with a failing meter = %v, want its error", err)
	}
}

// addWorkspace adds a workspace with the plan to the database, and returns
// its ID.
func addWorkspace(t *testing.T, d *dbtest.Database, plan string) ids.UUID {
	t.Helper()
	id := ids.New()
	if _, err := d.Admin(t).Exec(context.Background(),
		"INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', $2, $3)",
		id, fmt.Sprintf("harbour-%x", id[10:]), plan); err != nil {
		t.Fatal(err)
	}
	return id
}

// inTx runs fn in a transaction of the workspace.
func inTx(t *testing.T, db *database.DB, ws ids.UUID, fn func(tx pgx.Tx) error) {
	t.Helper()
	if err := db.InTx(context.Background(), database.Scope{WorkspaceID: ws}, fn); err != nil {
		t.Fatal(err)
	}
}

// TestEnforcedPlans checks that, while entitlements are enforced, as in
// hosted mode, each workspace has the plan that its row names, with the
// quotas and features that the plan file gives it, capped by the instance's
// caps, and that a refused change links to the upgrade page only where
// upgrading would raise its plan's limit.
//
// Verifies: FR-ENT-01/AC3
func TestEnforcedPlans(t *testing.T) {
	d := dbtest.New(t)
	db := d.Open(t)
	ctx := context.Background()
	plans := entitlements.Default()
	m := &meter{}
	s := entitlements.New(entitlements.Options{Enforce: true, Meter: m, UpgradeURL: testUpgradeURL})
	if s.NewWorkspacePlan() != entitlements.Free {
		t.Errorf("a new workspace has the plan %s, want free", s.NewWorkspacePlan())
	}
	for _, p := range plans.Plans() {
		ws := addWorkspace(t, d, p.Key)
		inTx(t, db, ws, func(tx pgx.Tx) error {
			for _, key := range entitlements.LimitKeys() {
				if q, err := s.Limit(ctx, tx, ws, key); err != nil || q != p.Quota(key) {
					t.Errorf("%s: Limit(%s) = %v, %v; want the plan file's %v", p.Key, key, q, err, p.Quota(key))
				}
			}
			for _, f := range plans.Features() {
				if ok, err := s.Enabled(ctx, tx, ws, f); err != nil || ok != p.Has(f) {
					t.Errorf("%s: Enabled(%s) = %v, %v; want the plan file's %v", p.Key, f, ok, err, p.Has(f))
				}
			}
			return nil
		})
	}

	// A workspace at the free plan's limit of members may upgrade to a plan
	// that has more, and one at the studio plan's limit of storage, the
	// largest, may not.
	free, _ := plans.Plan(entitlements.Free)
	members, _ := free.Quota(entitlements.Members).Max()
	studio, _ := plans.Plan("studio")
	storage, limited := studio.Quota(entitlements.StorageBytes).Max()
	if !limited {
		t.Fatalf("the studio plan does not limit storage, which this test needs")
	}
	m.usage = map[entitlements.LimitKey]int64{entitlements.Members: members, entitlements.StorageBytes: storage}
	ws := addWorkspace(t, d, entitlements.Free)
	inTx(t, db, ws, func(tx pgx.Tx) error {
		e := limitError(t, s.Check(ctx, tx, ws, entitlements.Members, 1))
		if *e != (entitlements.LimitError{Key: entitlements.Members, Usage: members, Limit: members,
			UpgradeURL: testUpgradeURL}) {
			t.Errorf("Check(members) on the free plan = %+v", e)
		}
		return nil
	})
	ws = addWorkspace(t, d, "studio")
	inTx(t, db, ws, func(tx pgx.Tx) error {
		e := limitError(t, s.Check(ctx, tx, ws, entitlements.StorageBytes, 1))
		if e.UpgradeURL != "" || e.Usage != storage || e.Limit != storage {
			t.Errorf("Check(storage_bytes) on the studio plan = %+v, want no upgrade link", e)
		}
		return nil
	})

	// The instance's caps apply on top of the plan, and a refusal that the
	// cap makes offers no upgrade, nor does one where the instance offers
	// none.
	capped := entitlements.New(entitlements.Options{Enforce: true, Meter: m, UpgradeURL: testUpgradeURL,
		Caps: entitlements.Caps{entitlements.Members: 1}})
	noUpgrade := entitlements.New(entitlements.Options{Enforce: true, Meter: m})
	m.usage = map[entitlements.LimitKey]int64{entitlements.Members: 1}
	ws = addWorkspace(t, d, "team")
	inTx(t, db, ws, func(tx pgx.Tx) error {
		if q, err := capped.Limit(ctx, tx, ws, entitlements.Members); err != nil || q != entitlements.AtMost(1) {
			t.Errorf("Limit(members) of a capped team workspace = %v, %v; want the cap", q, err)
		}
		if e := limitError(t, capped.Check(ctx, tx, ws, entitlements.Members, 1)); e.UpgradeURL != "" {
			t.Errorf("a refusal by the cap links to %q", e.UpgradeURL)
		}
		return nil
	})
	ws = addWorkspace(t, d, entitlements.Free)
	inTx(t, db, ws, func(tx pgx.Tx) error {
		if e := limitError(t, noUpgrade.Check(ctx, tx, ws, entitlements.Members, 1)); e.UpgradeURL != "" {
			t.Errorf("an instance without an upgrade page links to %q", e.UpgradeURL)
		}
		return nil
	})

	// A plan that the file does not define, as one that a newer server
	// wrote, is an error rather than a guess.
	ws = addWorkspace(t, d, "enterprise")
	if err := db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		_, err := s.Limit(ctx, tx, ws, entitlements.Projects)
		return err
	}); err == nil {
		t.Errorf("Limit for a workspace of an unknown plan = nil error")
	}
	// So is a workspace that does not exist.
	if err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		_, err := s.Limit(ctx, tx, ids.New(), entitlements.Projects)
		return err
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("Limit for no workspace = %v, want no rows", err)
	}
}
