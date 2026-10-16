package setup

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

// TestMain runs the tests with dbtest.Run, which keeps the libpq environment
// variables and the user's password file out of them.
func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

const testPassword = "correct horse battery staple"

var testClient = audit.Client{IP: netip.MustParseAddr("192.0.2.7"), UserAgent: "Test/1.0"}

// newTestService returns a Service with the Signup capability signup, on a
// new database, and the database.
func newTestService(t *testing.T, signup config.SignupPolicy) (*Service, *dbtest.Database) {
	t.Helper()
	d := dbtest.New(t)
	db := d.Open(t)
	return New(db, Options{
		Signup:        signup,
		DefaultLocale: language.English,
		Workspaces:    workspace.New(db, workspace.Options{PlanKey: "unlimited", DefaultLocale: language.English}),
		Hasher:        account.NewHasher(),
	}), d
}

// request returns a valid request of setup, with the setup code of s.
func request(t *testing.T, s *Service, email string) Request {
	t.Helper()
	code, err := s.Code(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return Request{Code: code, Owner: account.Registration{Email: email, DisplayName: "Jo Novak",
		Password: secret.New(testPassword)}, WorkspaceName: "Harbour Studio"}
}

// count returns the number that a query counts, as the superuser.
func count(t *testing.T, d *dbtest.Database, sql string) int {
	t.Helper()
	var n int
	if err := d.Admin(t).QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCode checks the form of the setup code, which every Service of a
// database gives alike, and how a typed code is read.
func TestCode(t *testing.T) {
	ones := []byte{0x08, 0x42, 0x10, 0x84, 0x21, 0x08, 0x42, 0x10, 0x84, 0x21}
	for _, c := range []struct {
		key  []byte
		want string
	}{
		{make([]byte, 32), "0000-0000-0000-0000"},
		{slices.Repeat([]byte{0xff}, 32), "ZZZZ-ZZZZ-ZZZZ-ZZZZ"},
		{append(ones, make([]byte, 22)...), "1111-1111-1111-1111"},
	} {
		if got := codeOf(c.key); got != c.want {
			t.Errorf("codeOf(% x) = %s, want %s", c.key[:codeBytes], got, c.want)
		}
	}
	for in, want := range map[string]string{
		"7K2M-Q9XD-4RTB-H8VW":   "7K2MQ9XD4RTBH8VW",
		"7k2m q9xd 4rtb h8vw":   "7K2MQ9XD4RTBH8VW",
		" 7k2mq9xd-4rtbh8vw ":   "7K2MQ9XD4RTBH8VW",
		"O0-Il":                 "0011",
		"7K2M-Q9XD-4RTB-H8VW-X": "7K2MQ9XD4RTBH8VWX",
	} {
		if got := normalizeCode(in); got != want {
			t.Errorf("normalizeCode(%q) = %q, want %q", in, got, want)
		}
	}

	s, _ := newTestService(t, config.SignupSetup)
	code, err := s.Code(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Code(context.Background())
	if err != nil || again != code || len(code) != 19 || strings.Trim(code, codeAlphabet+"-") != "" {
		t.Errorf("the codes %q and %q, %v; want one code of 16 characters of %s in groups of four", code,
			again, err, codeAlphabet)
	}
	if err := s.checkCode(context.Background(), strings.ToLower(strings.ReplaceAll(code, "-", ""))); err != nil {
		t.Errorf("the code in lower case, without hyphens: %v", err)
	}
}

// TestComplete checks what completing setup creates: the owner's account,
// an instance admin, whose personal workspace is the first workspace, which
// the owner owns, with the instance's default locale; the audit entry of
// the workspace's creation; and the signup policy, and that setup then has
// completed, and does not complete again.
func TestComplete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d := newTestService(t, config.SignupSetup)
	st, err := s.Status(ctx)
	if err != nil || st != (Status{Signup: config.SignupClosed}) {
		t.Errorf("Status before setup = %+v, %v; want pending, with no fixed policy", st, err)
	}
	req := request(t, s, " Jo@Example.com ")
	req.Signup = config.SignupInvite
	res, err := s.Complete(ctx, req, testClient)
	if err != nil {
		t.Fatal(err)
	}
	if res.Workspace.Name != "Harbour Studio" || res.Workspace.Slug != "harbour-studio" ||
		res.Workspace.DefaultLocale != language.English {
		t.Errorf("the workspace is %+v", res.Workspace)
	}

	var email, name, status, role, signup, metadata, ip string
	var admin, personal, actor bool
	err = d.Admin(t).QueryRow(ctx, `
		SELECT u.email::text, u.display_name, u.status, u.is_instance_admin,
			u.personal_workspace_created_at IS NOT NULL, m.role, i.signup_policy, a.metadata::text, host(a.ip),
			a.actor_user_id = u.id AND a.subject_id = m.workspace_id AND a.workspace_id = m.workspace_id
		FROM "user" u
		JOIN membership m ON m.user_id = u.id
		JOIN audit_entry a ON a.action = 'workspace.create'
		CROSS JOIN instance i
		WHERE u.id = $1 AND m.workspace_id = $2 AND i.setup_completed_at IS NOT NULL`, res.OwnerID,
		res.Workspace.ID).Scan(&email, &name, &status, &admin, &personal, &role, &signup, &metadata, &ip, &actor)
	if err != nil {
		t.Fatal(err)
	}
	if email != "Jo@Example.com" || name != "Jo Novak" || status != "active" || !admin || !personal {
		t.Errorf("the owner is %s, %q, %s, instance admin %v, personal workspace given %v", email, name, status,
			admin, personal)
	}
	if role != "owner" || signup != "invite" {
		t.Errorf("the owner's role is %s, and the signup policy %s", role, signup)
	}
	if metadata != `{"personal": true}` || ip != "192.0.2.7" || !actor {
		t.Errorf("the audit entry has the metadata %s, the IP address %s, and its owner as actor %v", metadata, ip,
			actor)
	}
	if st, err := s.Status(ctx); err != nil || !st.Completed {
		t.Errorf("Status after setup = %+v, %v", st, err)
	}
	if _, err := s.Complete(ctx, request(t, s, "mallory@example.com"), testClient); !errors.Is(err, ErrCompleted) {
		t.Errorf("a second setup: %v, want ErrCompleted", err)
	}
	if n := count(t, d, `SELECT count(*) FROM "user"`); n != 1 {
		t.Errorf("%d accounts after the second setup, want 1", n)
	}
}

// TestStatusSignup checks that Status says whether the configuration sets
// the signup policy, and which.
func TestStatusSignup(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t).Open(t)
	for _, c := range []struct {
		signup config.SignupPolicy
		want   Status
	}{
		{config.SignupSetup, Status{Signup: config.SignupClosed}},
		{config.SignupOpen, Status{SignupFixed: true, Signup: config.SignupOpen}},
		{config.SignupInvite, Status{SignupFixed: true, Signup: config.SignupInvite}},
		{config.SignupClosed, Status{SignupFixed: true, Signup: config.SignupClosed}},
	} {
		s := New(db, Options{Signup: c.signup})
		if got, err := s.Status(context.Background()); err != nil || got != c.want {
			t.Errorf("%s: Status = %+v, %v; want %+v", c.signup, got, err, c.want)
		}
	}
}

// TestCompleteRefuses checks that a setup with a wrong code, or a request
// that is not valid, changes nothing, and what the error names.
func TestCompleteRefuses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d := newTestService(t, config.SignupSetup)
	valid := request(t, s, "jo@example.com")
	if _, err := account.Create(ctx, d.Open(t), account.NewHasher(), account.Registration{Email: "taken@example.com",
		DisplayName: "Taken", Password: secret.New(testPassword)}, false); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		change func(*Request)
		check  func(error) bool
	}{
		{"a wrong code", func(r *Request) { r.Code = "0000-0000-0000-0000" },
			func(err error) bool { return errors.Is(err, ErrInvalidCode) }},
		{"no code", func(r *Request) { r.Code = "" }, func(err error) bool { return errors.Is(err, ErrInvalidCode) }},
		{"the code and more", func(r *Request) { r.Code += "0" },
			func(err error) bool { return errors.Is(err, ErrInvalidCode) }},
		{"fields", func(r *Request) {
			r.Owner.Email, r.Owner.DisplayName, r.WorkspaceName, r.Signup = "jo", " ", "", "everyone"
		}, fieldsAre("owner.email", "owner.display_name", "workspace.name", "signup_policy")},
		{"a reserved slug", func(r *Request) { r.WorkspaceSlug = "admin" }, func(err error) bool {
			var slug *workspace.SlugError
			return errors.As(err, &slug) && slug.Reason == workspace.SlugReserved
		}},
		{"a short password", func(r *Request) { r.Owner.Password = secret.New("short") }, func(err error) bool {
			var weak *account.PasswordError
			return errors.As(err, &weak) && weak.Reason == account.PasswordTooShort
		}},
		{"an address that has an account", func(r *Request) { r.Owner.Email = "TAKEN@example.com" },
			fieldsAre("owner.email")},
	} {
		req := valid
		c.change(&req)
		if _, err := s.Complete(ctx, req, testClient); !c.check(err) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if st, err := s.Status(ctx); err != nil || st.Completed {
		t.Errorf("Status = %+v, %v; want setup pending", st, err)
	}
	if n := count(t, d, `SELECT count(*) FROM "user"`) + count(t, d, "SELECT count(*) FROM workspace"); n != 1 {
		t.Errorf("%d accounts and workspaces, want only the account that was there", n)
	}
}

// fieldsAre returns a check that an error is input.FieldErrors of exactly
// the fields.
func fieldsAre(fields ...string) func(error) bool {
	return func(err error) bool {
		var fe input.FieldErrors
		if !errors.As(err, &fe) {
			return false
		}
		var got []string
		for _, f := range fe {
			got = append(got, f.Field)
		}
		return slices.Equal(got, fields)
	}
}

// TestConcurrentSetup checks that of setups at once, each with an owner of
// its own, exactly one completes, and the others find setup completed, so
// that the instance has one owner.
//
// Verifies: FR-ADMIN-02/AC3
func TestConcurrentSetup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d := newTestService(t, config.SignupSetup)
	const setups = 6
	reqs := make([]Request, setups)
	for i := range reqs {
		reqs[i] = request(t, s, string(rune('a'+i))+"@example.com")
		reqs[i].WorkspaceName = "Studio " + string(rune('A'+i))
	}
	errs := make([]error, setups)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range setups {
		wg.Go(func() {
			<-start
			// complete skips the check before the transaction, so that the
			// setups meet at the lock of the instance's row.
			_, errs[i] = s.complete(ctx, reqs[i], testClient)
		})
	}
	close(start)
	wg.Wait()
	completed := 0
	for i, err := range errs {
		switch {
		case err == nil:
			completed++
		case !errors.Is(err, ErrCompleted):
			t.Errorf("setup %d: %v", i, err)
		}
	}
	if completed != 1 {
		t.Errorf("%d setups completed, want 1", completed)
	}
	for sql, want := range map[string]int{
		`SELECT count(*) FROM "user"`:                          1,
		`SELECT count(*) FROM "user" WHERE is_instance_admin`:  1,
		"SELECT count(*) FROM workspace":                       1,
		"SELECT count(*) FROM membership WHERE role = 'owner'": 1,
	} {
		if n := count(t, d, sql); n != want {
			t.Errorf("%s: %d, want %d", sql, n, want)
		}
	}
}

// unattended returns a valid configuration of unattended setup.
func unattended() *config.UnattendedSetup {
	return &config.UnattendedSetup{OwnerEmail: "jo@example.com", OwnerName: "Jo Novak",
		OwnerPassword: secret.New(testPassword), WorkspaceName: "Harbour Studio"}
}

// TestUnattended checks that unattended setup completes setup without a
// code, once, and that it names the key of each value that it refuses, and
// then changes nothing, and that CheckUnattended refuses the same values,
// without the database.
func TestUnattended(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d := newTestService(t, config.SignupSetup)
	for _, c := range []struct {
		name   string
		change func(*config.UnattendedSetup)
		want   string
	}{
		{"fields", func(u *config.UnattendedSetup) {
			u.OwnerEmail, u.OwnerName, u.WorkspaceName, u.Signup = "jo", " ", " ", "everyone"
		}, "STORYCREEP_SETUP_OWNER_EMAIL: is not an email address; STORYCREEP_SETUP_OWNER_NAME: is empty; " +
			"STORYCREEP_SETUP_WORKSPACE_NAME: is empty; STORYCREEP_SETUP_SIGNUP: is not closed, invite, or open"},
		{"a slug", func(u *config.UnattendedSetup) { u.WorkspaceSlug = "Harbour Studio" },
			"STORYCREEP_SETUP_WORKSPACE_SLUG: a slug is groups of lowercase letters and digits"},
		{"a reserved slug", func(u *config.UnattendedSetup) { u.WorkspaceSlug = "api" },
			"STORYCREEP_SETUP_WORKSPACE_SLUG: the slug is a reserved word"},
		{"a common password", func(u *config.UnattendedSetup) { u.OwnerPassword = secret.New("Unbelievable") },
			"STORYCREEP_SETUP_OWNER_PASSWORD: is one of the most common passwords"},
		{"a short password", func(u *config.UnattendedSetup) { u.OwnerPassword = secret.New("short") },
			"STORYCREEP_SETUP_OWNER_PASSWORD: has fewer than 12 characters"},
		{"a long password", func(u *config.UnattendedSetup) {
			u.OwnerPassword = secret.New(strings.Repeat("ž", account.MaxPasswordLength+1))
		}, "STORYCREEP_SETUP_OWNER_PASSWORD: is longer than 256 characters"},
	} {
		u := unattended()
		c.change(u)
		done, err := s.Unattended(ctx, u)
		var cerr *ConfigError
		if done || !errors.As(err, &cerr) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, %v; want a *ConfigError with %q", c.name, done, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), testPassword) {
			t.Errorf("%s: the error shows a password: %v", c.name, err)
		}
		// validate-config checks the values as unattended setup does.
		if cerr := CheckUnattended(u, language.English); err == nil || cerr == nil || cerr.Error() != err.Error() {
			t.Errorf("%s: CheckUnattended = %v, want %v", c.name, cerr, err)
		}
	}
	if err := CheckUnattended(unattended(), language.English); err != nil {
		t.Errorf("CheckUnattended of a valid configuration = %v", err)
	}
	if n := count(t, d, `SELECT count(*) FROM "user"`); n != 0 {
		t.Fatalf("%d accounts after the refused setups", n)
	}

	u := unattended()
	u.WorkspaceSlug, u.Signup = "harbour", config.SignupOpen
	if done, err := s.Unattended(ctx, u); !done || err != nil {
		t.Fatalf("Unattended = %v, %v", done, err)
	}
	var slug, signup string
	if err := d.Admin(t).QueryRow(ctx, "SELECT w.slug, i.signup_policy FROM workspace w, instance i").Scan(&slug,
		&signup); err != nil {
		t.Fatal(err)
	}
	if slug != "harbour" || signup != "open" {
		t.Errorf("the slug %s, and the policy %s", slug, signup)
	}
	if done, err := s.Unattended(ctx, unattended()); done || err != nil {
		t.Errorf("Unattended after setup = %v, %v; want false and no error", done, err)
	}
}
