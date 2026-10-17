package entitlements_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
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

	// A plan file without the unlimited plan, which Load never returns, is an
	// error rather than a plan that allows nothing.
	s = entitlements.New(entitlements.Options{Plans: &entitlements.PlanFile{}})
	if _, err := s.Enabled(ctx, nil, ws, entitlements.SSO); err == nil ||
		!strings.Contains(err.Error(), "no unlimited plan") {
		t.Errorf("Enabled with a plan file without the unlimited plan = %v, want an error that says so", err)
	}
}

// TestCheckAddingNothing checks that a change that adds nothing to a limit,
// or takes from it, passes Check without a read of the usage, even where the
// workspace is past its quota already, so that it keeps what it has.
func TestCheckAddingNothing(t *testing.T) {
	ctx := context.Background()
	ws := ids.New()
	m := &meter{usage: map[entitlements.LimitKey]int64{entitlements.Members: 10}}
	caps := entitlements.Caps{entitlements.Members: 3}
	for _, s := range []*entitlements.Service{
		entitlements.New(entitlements.Options{Caps: caps, Meter: m}),
		// Without a meter too, which a quota that is a number needs otherwise.
		entitlements.New(entitlements.Options{Caps: caps}),
	} {
		for _, adding := range []int64{0, -1} {
			if err := s.Check(ctx, nil, ws, entitlements.Members, adding); err != nil {
				t.Errorf("Check(members, %d) past the cap = %v, want nil", adding, err)
			}
		}
	}
	if m.reads != 0 {
		t.Errorf("a check of adding nothing read the usage %d times", m.reads)
	}
	// The Service keeps its own copy of the caps it was given.
	s := entitlements.New(entitlements.Options{Caps: caps, Meter: m})
	caps[entitlements.Members] = 100
	if q, err := s.Limit(ctx, nil, ws, entitlements.Members); err != nil || q != entitlements.AtMost(3) {
		t.Errorf("Limit(members) after the caps given changed = %v, %v; want the cap of 3", q, err)
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

// quotaCase is a plan and a limit whose quota is a number, max, or has ok
// false where the plan file has no such case.
type quotaCase struct {
	plan string
	key  entitlements.LimitKey
	max  int64
	ok   bool
}

// quotaCases returns the first plan and limit of the plan file whose quota
// is a number that another plan exceeds, which upgrading would raise, and the
// first whose quota is a number that no other plan exceeds. The unlimited
// plan is neither, nor is it another plan: no workspace upgrades to it.
func quotaCases(plans *entitlements.PlanFile) (raisable, top quotaCase) {
	for _, p := range plans.Plans() {
		if p.Key == entitlements.Unlimited {
			continue
		}
		for _, key := range entitlements.LimitKeys() {
			n, limited := p.Quota(key).Max()
			if !limited {
				continue
			}
			exceeded := slices.ContainsFunc(plans.Plans(), func(o entitlements.Plan) bool {
				m, limited := o.Quota(key).Max()
				return o.Key != entitlements.Unlimited && (!limited || m > n)
			})
			c := quotaCase{plan: p.Key, key: key, max: n, ok: true}
			switch {
			case exceeded && !raisable.ok:
				raisable = c
			case !exceeded && !top.ok:
				top = c
			}
		}
	}
	return raisable, top
}

// TestEnforcedPlans checks that, while entitlements are enforced, as in
// hosted mode, each workspace has the plan that its row names, with the
// quotas and features that the plan file gives it, and usage reads against
// them, capped by the instance's caps, and that a refused change links to the
// upgrade page only where upgrading would raise its plan's limit.
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
				// A usage read carries the plan's quota, and the soft warning
				// at a quota that is a number, but not well below it, nor at
				// any usage of a quota that is not (FR-ENT-03).
				type usageCase struct {
					used  int64
					warns bool
				}
				cases := []usageCase{{used: 1 << 40}}
				if n, limited := p.Quota(key).Max(); limited {
					cases = []usageCase{{used: n, warns: true}}
					if n > 0 {
						cases = append(cases, usageCase{used: 0})
					}
				}
				for _, c := range cases {
					m.usage = map[entitlements.LimitKey]int64{key: c.used}
					want := entitlements.Usage{Key: key, Used: c.used, Quota: p.Quota(key), Warning: c.warns}
					if got, err := s.Usage(ctx, tx, ws, key); err != nil || got != want {
						t.Errorf("%s: Usage(%s) at %d = %+v, %v; want %+v", p.Key, key, c.used, got, err, want)
					}
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

	// A workspace at a plan's quota of a limit that another plan allows more
	// of may upgrade, unless the instance offers no upgrade, and one at a
	// quota that no plan exceeds may not. The plan file's numbers are
	// placeholders, so the cases come from it, and one that it lacks is
	// skipped.
	noUpgrade := entitlements.New(entitlements.Options{Enforce: true, Meter: m})
	raisable, top := quotaCases(plans)
	if raisable.ok {
		m.usage = map[entitlements.LimitKey]int64{raisable.key: raisable.max}
		ws := addWorkspace(t, d, raisable.plan)
		inTx(t, db, ws, func(tx pgx.Tx) error {
			e := limitError(t, s.Check(ctx, tx, ws, raisable.key, 1))
			if *e != (entitlements.LimitError{Key: raisable.key, Usage: raisable.max, Limit: raisable.max,
				UpgradeURL: testUpgradeURL}) {
				t.Errorf("Check(%s) on the %s plan = %+v, want an upgrade link", raisable.key, raisable.plan, e)
			}
			if e := limitError(t, noUpgrade.Check(ctx, tx, ws, raisable.key, 1)); e.UpgradeURL != "" {
				t.Errorf("an instance without an upgrade page links to %q", e.UpgradeURL)
			}
			return nil
		})
	} else {
		t.Log("no plan has a quota that another plan exceeds, so no refusal links to the upgrade page")
	}
	if top.ok {
		m.usage = map[entitlements.LimitKey]int64{top.key: top.max}
		ws := addWorkspace(t, d, top.plan)
		inTx(t, db, ws, func(tx pgx.Tx) error {
			e := limitError(t, s.Check(ctx, tx, ws, top.key, 1))
			if e.UpgradeURL != "" || e.Usage != top.max || e.Limit != top.max {
				t.Errorf("Check(%s) on the %s plan, which no plan exceeds, = %+v, want no upgrade link", top.key,
					top.plan, e)
			}
			return nil
		})
	} else {
		t.Log("every quota that is a number has a plan that exceeds it, so every refusal links to the upgrade page")
	}

	// The instance's caps apply on top of the plan, and a refusal that the
	// cap makes offers no upgrade. The team plan, as every paid plan, does
	// not limit members, which it bills for (TestPlanFileShape).
	capped := entitlements.New(entitlements.Options{Enforce: true, Meter: m, UpgradeURL: testUpgradeURL,
		Caps: entitlements.Caps{entitlements.Members: 1}})
	m.usage = map[entitlements.LimitKey]int64{entitlements.Members: 1}
	ws := addWorkspace(t, d, "team")
	inTx(t, db, ws, func(tx pgx.Tx) error {
		if q, err := capped.Limit(ctx, tx, ws, entitlements.Members); err != nil || q != entitlements.AtMost(1) {
			t.Errorf("Limit(members) of a capped team workspace = %v, %v; want the cap", q, err)
		}
		if e := limitError(t, capped.Check(ctx, tx, ws, entitlements.Members, 1)); e.UpgradeURL != "" {
			t.Errorf("a refusal by the cap links to %q", e.UpgradeURL)
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
