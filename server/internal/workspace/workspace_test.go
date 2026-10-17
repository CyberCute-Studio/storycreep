package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// newService returns a Service on a database of its own, and the
// database.
func newService(t *testing.T, policy role.Policy) (*Service, *dbtest.Database) {
	t.Helper()
	d := dbtest.New(t)
	return New(d.Open(t), Options{Policy: policy, PlanKey: "unlimited", DefaultLocale: language.English}), d
}

// addUser adds an active account with the display name, as the superuser,
// and returns its ID.
func addUser(t *testing.T, d *dbtest.Database, name string) ids.UUID {
	t.Helper()
	id := ids.New()
	if _, err := d.Admin(t).Exec(context.Background(),
		`INSERT INTO "user" (id, email, display_name) VALUES ($1, $2, $3)`, id, id.String()+"@example.com",
		name); err != nil {
		t.Fatal(err)
	}
	return id
}

// count returns the number that a query counts, as the superuser.
func count(t *testing.T, d *dbtest.Database, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := d.Admin(t).QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCheckSlug checks the form of a slug and the reserved words.
//
// Verifies: FR-TEN-01/AC3
func TestCheckSlug(t *testing.T) {
	t.Parallel()
	for _, slug := range []string{"a", "jo", "harbour-team", "team-2026", "0", strings.Repeat("a", 64),
		"admins", "con-board", "workspace"} {
		if err := CheckSlug(slug); err != nil {
			t.Errorf("%q: %v", slug, err)
		}
	}
	for slug, reason := range map[string]SlugReason{
		"": SlugFormat, "-": SlugFormat, "-a": SlugFormat, "a-": SlugFormat, "a--b": SlugFormat, "A": SlugFormat,
		"a_b": SlugFormat, "a b": SlugFormat, "ž": SlugFormat, "a.b": SlugFormat, strings.Repeat("a", 65): SlugFormat,
		"ｊｏ": SlugFormat, "admin": SlugReserved, "api": SlugReserved, "app": SlugReserved, "nul": SlugReserved,
		"com1": SlugReserved, "lpt9": SlugReserved, "workspaces": SlugReserved,
	} {
		var e *SlugError
		if err := CheckSlug(slug); !errors.As(err, &e) || e.Reason != reason {
			t.Errorf("%q: %v, want %s", slug, err, reason)
		}
	}
}

// TestBaseSlug checks the slug derived from a name: its letters and digits
// in lowercase ASCII, without accents, each run of anything else a hyphen,
// and "workspace" when that leaves nothing.
func TestBaseSlug(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"Jo":                       "jo",
		"Tomáš Novák":              "tomas-novak",
		"  --Harbour  Team--  ":    "harbour-team",
		"Ærøskøbing Straße":        "aeroskobing-strasse",
		"Łódź Đorđe Þór İstanbul":  "lodz-dorde-thor-istanbul",
		"Ｆｕｌｌ ｗｉｄｔｈ":               "full-width",
		"ﬁsh & chips":              "fish-chips",
		"山田 太郎":                    "workspace",
		"🦊":                        "workspace",
		"R2-D2 / C-3PO":            "r2-d2-c-3po",
		strings.Repeat("ab ", 40):  strings.Repeat("ab-", 21) + "a",
		strings.Repeat("abc ", 20): strings.TrimSuffix(strings.Repeat("abc-", 16), "-"),
		strings.Repeat("a", 70):    strings.Repeat("a", 64),
	} {
		got := baseSlug(name)
		if got != want {
			t.Errorf("baseSlug(%q) = %q, want %q", name, got, want)
		}
		if CheckSlug(got) != nil && got != "workspace" {
			t.Errorf("baseSlug(%q) = %q, which is not a valid slug", name, got)
		}
	}
}

// TestCandidates checks the slugs tried for a name: its own, unless it is
// reserved, then numbered ones, then random ones, each a valid slug of at
// most 64 characters.
func TestCandidates(t *testing.T) {
	t.Parallel()
	random := regexp.MustCompile(`^[a-z0-9-]+-[0-9a-z]{8}$`)
	for name, first := range map[string]string{"Jo": "jo", "Admin": "admin-2", strings.Repeat("x", 80): strings.Repeat("x", 64)} {
		var got []string
		for slug := range candidates(name) {
			got = append(got, slug)
		}
		if len(got) == 0 || got[0] != first {
			t.Errorf("%q: the first candidate is %v, want %q", name, got, first)
			continue
		}
		for i, slug := range got {
			if err := CheckSlug(slug); err != nil {
				t.Errorf("%q: the candidate %q: %v", name, slug, err)
			}
			if i >= len(got)-randomSlugs && !random.MatchString(slug) {
				t.Errorf("%q: the candidate %q has no random suffix", name, slug)
			}
		}
		if got[len(got)-randomSlugs-1] != withSuffix(baseSlug(name), fmt.Sprintf("-%d", numberedSlugs)) {
			t.Errorf("%q: the candidates are %v", name, got)
		}
	}
}

// TestCreateFallsBackToRandomSlugs checks that a workspace whose name's
// numbered slugs are all taken gets a slug with a random suffix.
func TestCreateFallsBackToRandomSlugs(t *testing.T) {
	t.Parallel()
	s, d := newService(t, role.Policy{})
	user := addUser(t, d, "Jo")
	var slugs []string
	for range numberedSlugs + 1 {
		w, err := s.Create(context.Background(), user, Settings{Name: "Jo"}, audit.Client{})
		if err != nil {
			t.Fatal(err)
		}
		slugs = append(slugs, w.Slug)
	}
	want := []string{"jo", "jo-2", "jo-3", "jo-4", "jo-5", "jo-6", "jo-7", "jo-8", "jo-9", "jo-10"}
	if !slices.Equal(slugs[:numberedSlugs], want) || !regexp.MustCompile(`^jo-[0-9a-z]{8}$`).MatchString(slugs[numberedSlugs]) {
		t.Errorf("the slugs are %v", slugs)
	}
}

// TestEnsurePersonalOnce checks that sign-ins at once give an account one
// personal workspace, which it owns, recorded once in the audit log, that
// later ones give it none, and that an account that is not active gets
// none.
func TestEnsurePersonalOnce(t *testing.T) {
	t.Parallel()
	s, d := newService(t, role.Policy{})
	user := addUser(t, d, "Žofie")
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { errs <- s.EnsurePersonal(ctx, user, audit.Client{}) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.EnsurePersonal(ctx, user, audit.Client{}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, d, `SELECT count(*) FROM membership WHERE user_id = $1 AND role = 'owner'`, user); n != 1 {
		t.Errorf("%d owned workspaces, want 1", n)
	}
	if n := count(t, d, `SELECT count(*) FROM workspace WHERE name = 'Žofie' AND slug = 'zofie'`); n != 1 {
		t.Errorf("%d workspaces named after the account, want 1", n)
	}
	if n := count(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'workspace.create'`); n != 1 {
		t.Errorf("%d entries of the creation, want 1", n)
	}

	suspended := addUser(t, d, "Suspended")
	if _, err := d.Admin(t).Exec(ctx, `UPDATE "user" SET status = 'suspended' WHERE id = $1`, suspended); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsurePersonal(ctx, suspended, audit.Client{}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, d, `SELECT count(*) FROM membership WHERE user_id = $1`, suspended); n != 0 {
		t.Errorf("a suspended account got %d workspaces", n)
	}
}

// TestEmailVerified checks that the verification of an account's email
// address gives it its personal workspace only where the Service requires a
// verified address, which its first sign-in could not give it without, and
// that elsewhere the workspace waits for the first sign-in (FR-TEN-01).
func TestEmailVerified(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, required := range []bool{false, true} {
		d := dbtest.New(t)
		s := New(d.Open(t), Options{PlanKey: "unlimited", DefaultLocale: language.English,
			RequireVerifiedEmail: required})
		user := addUser(t, d, "Jo")
		if err := s.EnsurePersonal(ctx, user, audit.Client{}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Admin(t).Exec(ctx, `UPDATE "user" SET email_verified_at = now() WHERE id = $1`,
			user); err != nil {
			t.Fatal(err)
		}
		if err := s.EmailVerified(ctx, user, audit.Client{}); err != nil {
			t.Fatal(err)
		}
		if n := count(t, d, `SELECT count(*) FROM membership WHERE user_id = $1`, user); n != 1 {
			t.Errorf("with a verified address required %v, the account has %d workspaces, want 1", required, n)
		}
		// An account that has not signed in yet gets its workspace from the
		// verification only where a verified address is required.
		other := addUser(t, d, "Ana")
		if _, err := d.Admin(t).Exec(ctx, `UPDATE "user" SET email_verified_at = now() WHERE id = $1`,
			other); err != nil {
			t.Fatal(err)
		}
		if err := s.EmailVerified(ctx, other, audit.Client{}); err != nil {
			t.Fatal(err)
		}
		want := 0
		if required {
			want = 1
		}
		if n := count(t, d, `SELECT count(*) FROM membership WHERE user_id = $1`, other); n != want {
			t.Errorf("with a verified address required %v, the verification gave %d workspaces, want %d", required,
				n, want)
		}
	}
}

// TestCreateRollsBack checks that a workspace whose slug is taken adds
// nothing, neither a workspace nor a membership nor an audit entry.
func TestCreateRollsBack(t *testing.T) {
	t.Parallel()
	s, d := newService(t, role.Policy{})
	user := addUser(t, d, "Jo")
	ctx := context.Background()
	if _, err := s.Create(ctx, user, Settings{Name: "Jo", Slug: "harbour"}, audit.Client{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, user, Settings{Name: "Jo", Slug: "harbour"}, audit.Client{}); !errors.Is(err,
		ErrSlugTaken) {
		t.Errorf("a taken slug: %v", err)
	}
	if n := count(t, d, `SELECT count(*) FROM workspace`); n != 1 {
		t.Errorf("%d workspaces, want 1", n)
	}
	if n := count(t, d, `SELECT count(*) FROM membership`) + count(t, d, `SELECT count(*) FROM audit_entry`); n != 2 {
		t.Errorf("%d memberships and audit entries, want those of the first workspace", n)
	}
}

// TestUpdateWaitsForRoleChange checks that a change of a workspace's
// settings reads its user's role with a lock, so that a change of the role
// that is in progress is waited for, and decides: an owner whom another
// transaction demotes to viewer while their change waits is refused, and
// the workspace stays as it was.
func TestUpdateWaitsForRoleChange(t *testing.T) {
	t.Parallel()
	s, d := newService(t, role.Policy{})
	user := addUser(t, d, "Jo")
	ctx := context.Background()
	w, err := s.Create(ctx, user, Settings{Name: "Jo"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := d.Admin(t).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE membership SET role = 'viewer' WHERE workspace_id = $1 AND user_id = $2`,
		w.ID, user); err != nil {
		t.Fatal(err)
	}
	name := "Renamed"
	updated := make(chan error, 1)
	go func() {
		_, _, err := s.Update(ctx, user, w.ID, Patch{Name: &name})
		updated <- err
	}()
	dbtest.AwaitWaiter(t, tx, updated, "the change of the settings")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-updated; !errors.Is(err, ErrInsufficientRole) {
		t.Errorf("the change of a demoted owner: %v, want ErrInsufficientRole", err)
	}
	if n := count(t, d, `SELECT count(*) FROM workspace WHERE name = 'Renamed'`); n != 0 {
		t.Errorf("the demoted owner renamed the workspace")
	}
}

// TestIsolation tries the operations of the membership, a tenant table,
// from another workspace's transaction, where row-level security must keep
// them from reading the membership's role or adding a member (NFR-SEC-01).
//
// Verifies: NFR-SEC-01
func TestIsolation(t *testing.T) {
	t.Parallel()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	ctx := context.Background()
	member := func(w *isolationtest.Workspace) ids.UUID {
		var id ids.UUID
		if err := f.Admin().QueryRow(ctx, `SELECT user_id FROM membership WHERE id = $1`,
			w.Rows["membership"]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	for _, lock := range []bool{false, true} {
		f.Check(t, isolationtest.Op{
			Name: fmt.Sprintf("the membership's role, locked %v", lock),
			Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID, error) {
				q := queries.New(tx)
				var err error
				if lock {
					_, err = q.LockMembershipRole(ctx, queries.LockMembershipRoleParams{
						WorkspaceID: nullUUID(target.ID), UserID: nullUUID(member(target))})
				} else {
					_, err = q.MembershipRole(ctx, queries.MembershipRoleParams{WorkspaceID: nullUUID(target.ID),
						UserID: nullUUID(member(target))})
				}
				if errors.Is(err, pgx.ErrNoRows) {
					return nil, nil
				}
				return []ids.UUID{target.Rows["membership"]}, err
			},
		})
	}
	f.Check(t, isolationtest.Op{
		Name:   "a new member",
		Denied: true,
		Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID, error) {
			user, err := isolationtest.NewUser(ctx, f.Admin())
			if err != nil {
				return nil, err
			}
			id := ids.New()
			return []ids.UUID{id}, queries.New(tx).InsertMembership(ctx, queries.InsertMembershipParams{
				ID: nullUUID(id), WorkspaceID: nullUUID(target.ID), UserID: nullUUID(user), Role: "viewer"})
		},
	})
}

// TestGrantKeepsAnOwner checks that Grant refuses to change the role of a
// workspace's last owner whose account is active, as an owner whose
// account's deletion is pending does not count, and changes it once another
// active owner exists.
func TestGrantKeepsAnOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d := newService(t, role.Policy{})
	owner, leaving, other := addUser(t, d, "Owner"), addUser(t, d, "Leaving"), addUser(t, d, "Other")
	w, err := s.Create(ctx, owner, Settings{Name: "Harbour"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if prev, err := s.Grant(ctx, w.ID, leaving, role.Owner); err != nil || prev != role.None {
		t.Fatalf("Grant a new owner = %s, %v", prev, err)
	}
	if _, err := d.Admin(t).Exec(ctx, `UPDATE "user" SET status = 'pending_deletion' WHERE id = $1`,
		leaving); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Grant(ctx, w.ID, owner, role.Admin); !errors.Is(err, ErrLastOwner) {
		t.Errorf("demote the last active owner: %v, want ErrLastOwner", err)
	}
	if _, err := s.Grant(ctx, w.ID, other, role.Owner); err != nil {
		t.Fatal(err)
	}
	if prev, err := s.Grant(ctx, w.ID, owner, role.Admin); err != nil || prev != role.Owner {
		t.Errorf("demote an owner of two = %s, %v", prev, err)
	}
	if _, err := s.Grant(ctx, ids.New(), other, role.Viewer); !errors.Is(err, ErrNotFound) {
		t.Errorf("Grant in no workspace: %v, want ErrNotFound", err)
	}
	if id, err := s.BySlug(ctx, w.Slug); err != nil || id != w.ID {
		t.Errorf("BySlug(%s) = %s, %v", w.Slug, id, err)
	}
	if _, err := s.BySlug(ctx, "nowhere"); !errors.Is(err, ErrNotFound) {
		t.Errorf("BySlug of no workspace: %v", err)
	}
}

// TestRemoveKeepsAnOwner checks that of two owners who each remove
// themselves at once, one is removed, and the other is told that they are
// the last owner, so that the workspace keeps one.
//
// Verifies: FR-TEN-02/AC2
func TestRemoveKeepsAnOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d := newService(t, role.Policy{})
	first, second := addUser(t, d, "First"), addUser(t, d, "Second")
	w, err := s.Create(ctx, first, Settings{Name: "Harbour"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Grant(ctx, w.ID, second, role.Owner); err != nil {
		t.Fatal(err)
	}
	members, _, err := s.Members(ctx, first, w.ID, ids.UUID{}, 10)
	if err != nil || len(members) != 2 {
		t.Fatalf("Members = %+v, %v", members, err)
	}
	results := make(chan error, len(members))
	var start sync.WaitGroup
	start.Add(1)
	for _, m := range members {
		go func() {
			start.Wait()
			results <- s.RemoveMember(ctx, m.UserID, w.ID, m.MembershipID, audit.Client{})
		}()
	}
	start.Done()
	var removed, refused int
	for range members {
		switch err := <-results; {
		case err == nil:
			removed++
		case errors.Is(err, ErrLastOwner):
			refused++
		default:
			t.Errorf("RemoveMember: %v", err)
		}
	}
	if removed != 1 || refused != 1 {
		t.Errorf("%d owners were removed and %d refused, want 1 and 1", removed, refused)
	}
	if n := count(t, d, `SELECT count(*) FROM membership WHERE workspace_id = $1 AND role = 'owner'`, w.ID); n != 1 {
		t.Errorf("the workspace has %d owners, want 1", n)
	}
}

// TestMembersIsolation tries the queries of a workspace's members from
// another workspace's transaction, where row-level security must keep them
// from reading, changing, or adding a membership (NFR-SEC-01).
//
// Verifies: NFR-SEC-01
func TestMembersIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	member := func(w *isolationtest.Workspace) ids.UUID {
		var id ids.UUID
		if err := f.Admin().QueryRow(ctx, `SELECT user_id FROM membership WHERE id = $1`,
			w.Rows["membership"]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	found := func(target *isolationtest.Workspace, err error) ([]ids.UUID, error) {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return []ids.UUID{target.Rows["membership"]}, err
	}
	for _, op := range []isolationtest.Op{
		{Name: "the members", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			rows, err := queries.New(tx).WorkspaceMembers(ctx, queries.WorkspaceMembersParams{
				WorkspaceID: nullUUID(target.ID), MaxRows: 10})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.ID.Bytes)
			}
			return out, err
		}},
		{Name: "lock a membership", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			_, err := queries.New(tx).LockMembershipByID(ctx, queries.LockMembershipByIDParams{
				WorkspaceID: nullUUID(target.ID), ID: nullUUID(target.Rows["membership"])})
			return found(target, err)
		}},
		{Name: "a user's membership", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			_, err := queries.New(tx).MembershipOf(ctx, queries.MembershipOfParams{WorkspaceID: nullUUID(target.ID),
				UserID: nullUUID(member(target))})
			return found(target, err)
		}},
		{Name: "remove a membership", Blind: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			return nil, queries.New(tx).DeleteMembership(ctx, queries.DeleteMembershipParams{
				WorkspaceID: nullUUID(target.ID), ID: nullUUID(target.Rows["membership"])})
		}},
		{Name: "join a workspace", Denied: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			user, err := isolationtest.NewUser(ctx, f.Admin())
			if err != nil {
				return nil, err
			}
			id := ids.New()
			_, err = queries.New(tx).JoinWorkspace(ctx, queries.JoinWorkspaceParams{ID: nullUUID(id),
				WorkspaceID: nullUUID(target.ID), UserID: nullUUID(user), Role: "viewer"})
			return []ids.UUID{id}, err
		}},
	} {
		f.Check(t, op)
	}
}
