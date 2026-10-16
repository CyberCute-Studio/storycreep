package account

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// TestMain runs the tests with dbtest.Run, which keeps the libpq environment
// variables and the user's password file out of them.
func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// stored is an account as the superuser reads it.
type stored struct {
	id                       ids.UUID
	email, displayName       string
	passwordHash             pgtype.Text
	status                   string
	emailVerified, instAdmin bool
}

// accounts returns every account, as the superuser reads them, in the order
// they were added.
func accounts(t *testing.T, admin *pgx.Conn) []stored {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT id, email::text, display_name, password_hash, status, email_verified_at IS NOT NULL, is_instance_admin
		FROM "user" ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (stored, error) {
		var s stored
		err := r.Scan(&s.id, &s.email, &s.displayName, &s.passwordHash, &s.status, &s.emailVerified, &s.instAdmin)
		return s, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The sessions' idle timeout and lifetime in the tests, the defaults.
const (
	testIdle     = 30 * 24 * time.Hour
	testLifetime = 90 * 24 * time.Hour
)

// options returns the options of a Service in the tests, under the signup
// policy, with the server's limits, counted in db.
func options(db DB, signup config.SignupPolicy) Options {
	return Options{Signup: signup, IdleTimeout: testIdle, Lifetime: testLifetime,
		Limits: ratelimit.New(db, ratelimit.DefaultPolicy())}
}

// setup returns a Service under the signup policy, on a new database whose
// first-run setup has completed, and a connection to the database as the
// superuser.
func setup(t *testing.T, signup config.SignupPolicy) (*Service, *pgx.Conn) {
	t.Helper()
	d := dbtest.New(t)
	d.MarkSetUp(t)
	db := d.Open(t)
	return New(db, options(db, signup)), d.Admin(t)
}

// signsIn returns the ID of the account that email and password sign in to,
// as authenticate checks them, without starting a session.
func signsIn(ctx context.Context, s *Service, email string, password secret.Value) (ids.UUID, error) {
	id, _, _, err := s.authenticate(ctx, email, password)
	return id, err
}

// TestRegister checks the account that a registration creates: its address
// and display name without the white space around them, a hash of its
// password with the current parameters, active, and neither verified nor an
// instance admin.
func TestRegister(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	password := secret.New("correct horse battery staple")
	err := s.Register(context.Background(), Registration{Email: "  Jo@Example.com ", DisplayName: " Jo Doe\t",
		Password: password})
	if err != nil {
		t.Fatal(err)
	}
	got := accounts(t, admin)
	if len(got) != 1 {
		t.Fatalf("%d accounts, want 1", len(got))
	}
	a := got[0]
	if a.email != "Jo@Example.com" || a.displayName != "Jo Doe" || a.status != "active" || a.emailVerified ||
		a.instAdmin {
		t.Errorf("the account is %+v", a)
	}
	if !strings.HasPrefix(a.passwordHash.String, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Errorf("the password's hash is %q", a.passwordHash.String)
	}
	if match, _, err := s.hasher.Verify(context.Background(), a.passwordHash.String, password); err != nil || !match {
		t.Errorf("the hash does not match the password: %v", err)
	}
}

// TestRegisterExistingAddress checks that registering an address that has an
// account, in any case, succeeds as registering a new one does, and leaves
// the account as it was: no second account, and the same password and
// display name.
//
// Verifies: FR-AUTH-01/AC1
func TestRegisterExistingAddress(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	ctx := context.Background()
	first := Registration{Email: "jo@example.com", DisplayName: "Jo", Password: secret.New("correct horse battery")}
	if err := s.Register(ctx, first); err != nil {
		t.Fatal(err)
	}
	before := accounts(t, admin)
	for _, email := range []string{"jo@example.com", "JO@EXAMPLE.COM", " Jo@example.com"} {
		err := s.Register(ctx, Registration{Email: email, DisplayName: "Mallory",
			Password: secret.New("another long password")})
		if err != nil {
			t.Errorf("registering %q again: %v, want nil", email, err)
		}
	}
	if after := accounts(t, admin); !slices.Equal(after, before) {
		t.Errorf("the accounts were %+v, and are %+v", before, after)
	}
	if _, err := signsIn(ctx, s, "jo@example.com", first.Password); err != nil {
		t.Errorf("the first password no longer signs in: %v", err)
	}
}

// TestRegisterComposition checks that an email address is one address however
// its characters are composed: an account holds it in NFC, registering it
// composed otherwise adds no second account, and either form signs in.
func TestRegisterComposition(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	ctx := context.Background()
	const composed, decomposed = "\u017Eofie@example.com", "z\u030Cofie@example.com"
	password := secret.New("correct horse battery staple")
	if err := s.Register(ctx, Registration{Email: decomposed, DisplayName: "Žofie", Password: password}); err != nil {
		t.Fatal(err)
	}
	if err := s.Register(ctx, Registration{Email: " " + composed, DisplayName: "Mallory",
		Password: secret.New("another long password")}); err != nil {
		t.Fatal(err)
	}
	if got := accounts(t, admin); len(got) != 1 || got[0].email != composed || got[0].displayName != "Žofie" {
		t.Errorf("the accounts are %+v, want one, of %q", got, composed)
	}
	for _, email := range []string{composed, decomposed} {
		if _, err := signsIn(ctx, s, email, password); err != nil {
			t.Errorf("signing in as %q: %v", email, err)
		}
	}
}

// TestRegisterPolicy checks that only the open policy lets a registration
// in, and that the others refuse it before they look at it, so that they
// refuse even one that is not valid.
func TestRegisterPolicy(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	d.MarkSetUp(t)
	db, admin := d.Open(t), d.Admin(t)
	for _, c := range []struct {
		signup config.SignupPolicy
		want   error
	}{
		{config.SignupClosed, ErrSignupClosed},
		{config.SignupInvite, ErrInviteRequired},
		{"", ErrSignupClosed},
	} {
		s := New(db, options(db, c.signup))
		if err := s.CheckSignup(context.Background()); !errors.Is(err, c.want) {
			t.Errorf("%q: CheckSignup = %v, want %v", c.signup, err, c.want)
		}
		for _, r := range []Registration{
			{Email: "jo@example.com", DisplayName: "Jo", Password: secret.New("correct horse battery staple")},
			{},
		} {
			if err := s.Register(context.Background(), r); !errors.Is(err, c.want) {
				t.Errorf("%q: Register = %v, want %v", c.signup, err, c.want)
			}
		}
	}
	open := New(db, options(db, config.SignupOpen))
	if err := open.CheckSignup(context.Background()); err != nil {
		t.Errorf("open: CheckSignup = %v", err)
	}
	if got := accounts(t, admin); len(got) != 0 {
		t.Errorf("%d accounts, want none", len(got))
	}
}

// TestRegisterRefuses checks that a registration whose address or display
// name is not valid, or whose password breaks the policy, adds no account,
// and that the error names each field that is not valid.
func TestRegisterRefuses(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	good := secret.New("correct horse battery staple")
	for _, c := range []struct {
		name   string
		r      Registration
		fields []string
		reason PasswordReason
	}{
		{"no address", Registration{Email: " ", DisplayName: "Jo", Password: good}, []string{"email"}, ""},
		{"a name and an address", Registration{Email: "Jo <jo@example.com>", DisplayName: "Jo", Password: good},
			[]string{"email"}, ""},
		{"angle brackets", Registration{Email: "<jo@example.com>", DisplayName: "Jo", Password: good},
			[]string{"email"}, ""},
		{"no domain", Registration{Email: "jo", DisplayName: "Jo", Password: good}, []string{"email"}, ""},
		{"two addresses", Registration{Email: "jo@example.com, al@example.com", DisplayName: "Jo", Password: good},
			[]string{"email"}, ""},
		{"a quoted local part", Registration{Email: `"jo doe"@example.com`, DisplayName: "Jo", Password: good},
			[]string{"email"}, ""},
		{"an address too long", Registration{Email: strings.Repeat("a", 64) + "@" + strings.Repeat("b", 186) +
			".com", DisplayName: "Jo", Password: good}, []string{"email"}, ""},
		{"no display name", Registration{Email: "jo@example.com", DisplayName: " \t ", Password: good},
			[]string{"display_name"}, ""},
		{"a display name too long", Registration{Email: "jo@example.com", DisplayName: strings.Repeat("ž", 101),
			Password: good}, []string{"display_name"}, ""},
		{"a control character", Registration{Email: "jo@example.com", DisplayName: "Jo\nDoe", Password: good},
			[]string{"display_name"}, ""},
		{"a bidirectional override", Registration{Email: "jo@example.com", DisplayName: "‮nimdA",
			Password: good}, []string{"display_name"}, ""},
		{"a bidirectional isolate", Registration{Email: "jo@example.com", DisplayName: "Jo⁧Doe",
			Password: good}, []string{"display_name"}, ""},
		{"a line separator", Registration{Email: "jo@example.com", DisplayName: "Jo Doe", Password: good},
			[]string{"display_name"}, ""},
		{"a display name that is not printed", Registration{Email: "jo@example.com", DisplayName: "​​",
			Password: good}, []string{"display_name"}, ""},
		{"a zero width space in the address", Registration{Email: "jo​@example.com", DisplayName: "Jo",
			Password: good}, []string{"email"}, ""},
		{"a control character in the address", Registration{Email: "jo\u0085x@example.com", DisplayName: "Jo",
			Password: good}, []string{"email"}, ""},
		{"a no-break space in the address", Registration{Email: "jo x@example.com", DisplayName: "Jo",
			Password: good}, []string{"email"}, ""},
		{"a bidirectional override in the address", Registration{Email: "jo@exa‮mple.com", DisplayName: "Jo",
			Password: good}, []string{"email"}, ""},
		{"a variation selector in the address", Registration{Email: "jo\uFE0F@example.com", DisplayName: "Jo",
			Password: good}, []string{"email"}, ""},
		{"a combining grapheme joiner in the address", Registration{Email: "jo\u034F@example.com",
			DisplayName: "Jo", Password: good}, []string{"email"}, ""},
		{"a Hangul filler in the address", Registration{Email: "jo@exam\u3164ple.com", DisplayName: "Jo",
			Password: good}, []string{"email"}, ""},
		{"a display name of Hangul fillers", Registration{Email: "jo@example.com", DisplayName: "\u3164\u3164",
			Password: good}, []string{"display_name"}, ""},
		{"a display name of marks that show nothing", Registration{Email: "jo@example.com", DisplayName: "\uFE0F\u034F",
			Password: good}, []string{"display_name"}, ""},
		{"a display name of Braille blanks", Registration{Email: "jo@example.com", DisplayName: "\u2800",
			Password: good}, []string{"display_name"}, ""},
		{"a Braille blank in the address", Registration{Email: "jo\u2800@example.com", DisplayName: "Jo",
			Password: good}, []string{"email"}, ""},
		{"a null notehead in the address", Registration{Email: "jo\U0001D159@example.com", DisplayName: "Jo",
			Password: good}, []string{"email"}, ""},
		{"an Egyptian blank in the address", Registration{Email: "jo@exam\U00013441ple.com", DisplayName: "Jo",
			Password: good}, []string{"email"}, ""},
		{"a Khitan filler in the address", Registration{Email: "jo\U00016FE4@example.com", DisplayName: "Jo",
			Password: good}, []string{"email"}, ""},
		{"a display name of other blanks", Registration{Email: "jo@example.com",
			DisplayName: "\U00013441\U00013442 \U0001D159\U00016FE4", Password: good}, []string{"display_name"}, ""},
		{"both fields", Registration{Email: "jo", DisplayName: "", Password: secret.New("short")},
			[]string{"email", "display_name"}, ""},
		{"a short password", Registration{Email: "jo@example.com", DisplayName: "Jo", Password: secret.New("short")},
			nil, PasswordTooShort},
		{"a common password", Registration{Email: "jo@example.com", DisplayName: "Jo",
			Password: secret.New("Unbelievable")}, nil, PasswordCommon},
	} {
		err := s.Register(context.Background(), c.r)
		var fields input.FieldErrors
		var weak *PasswordError
		switch {
		case c.fields != nil:
			var names []string
			if errors.As(err, &fields) {
				for _, f := range fields {
					names = append(names, f.Field)
				}
			}
			if !slices.Equal(names, c.fields) {
				t.Errorf("%s: %v, want errors of the fields %q", c.name, err, c.fields)
			}
		case !errors.As(err, &weak) || weak.Reason != c.reason:
			t.Errorf("%s: %v, want the password's reason %s", c.name, err, c.reason)
		}
	}
	if s.Register(context.Background(), Registration{Email: strings.Repeat("a", 64) + "@" + strings.Repeat("b", 185) +
		".com", DisplayName: strings.Repeat("ž", 100), Password: good}) != nil {
		t.Error("an address of 254 bytes and a display name of 100 characters are refused")
	}
	if got := accounts(t, admin); len(got) != 1 {
		t.Errorf("%d accounts, want only the last one", len(got))
	}
}

// TestFieldsOutsideASCII checks that the checks of a registration's fields
// take addresses and names outside ASCII, with the format characters that
// names need, as the joiner in an emoji family and the non-joiner of Persian,
// and the variation selector that shows a heart as an emoji.
func TestFieldsOutsideASCII(t *testing.T) {
	t.Parallel()
	for _, email := range []string{"žofie@example.com", "jo@bücher.example", "用户@例子.广告"} {
		if msg := checkEmail(email); msg != "" {
			t.Errorf("the address %q %s", email, msg)
		}
	}
	for _, name := range []string{"Žofie Nováková", "\U0001F468‍\U0001F469‍\U0001F467 Doe",
		"می‌خواهم", "יוסף Jo", "Jó", "Jo \u2764\uFE0F"} {
		if msg := checkDisplayName(name); msg != "" {
			t.Errorf("the display name %q %s", name, msg)
		}
	}
}

// addAccount adds an account with the address, the hash of its password, and
// the status, and returns its ID.
func addAccount(t *testing.T, admin *pgx.Conn, email string, hash pgtype.Text, status string) ids.UUID {
	t.Helper()
	id := ids.New()
	if _, err := admin.Exec(context.Background(), `INSERT INTO "user" (id, email, display_name, password_hash, status)
		VALUES ($1, $2, 'Jo', $3, $4)`, id, email, hash, status); err != nil {
		t.Fatal(err)
	}
	return id
}

// passwordHash returns the hash of the account's password.
func passwordHash(t *testing.T, admin *pgx.Conn, id ids.UUID) string {
	t.Helper()
	var hash string
	if err := admin.QueryRow(context.Background(), `SELECT password_hash FROM "user" WHERE id = $1`, id).
		Scan(&hash); err != nil {
		t.Fatal(err)
	}
	return hash
}

// TestAuthenticate checks that only an active account's own password signs
// in, with its address in any case, and that every other case is the same
// error.
func TestAuthenticate(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	ctx := context.Background()
	password := secret.New("correct horse battery staple")
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	valid := pgtype.Text{String: hash, Valid: true}
	active := addAccount(t, admin, "jo@example.com", valid, "active")
	addAccount(t, admin, "suspended@example.com", valid, "suspended")
	addAccount(t, admin, "leaving@example.com", valid, "pending_deletion")
	addAccount(t, admin, "deleted@example.com", valid, "deleted")
	addAccount(t, admin, "nopassword@example.com", pgtype.Text{}, "active")

	for _, email := range []string{"jo@example.com", "JO@example.COM", " jo@example.com "} {
		if id, err := signsIn(ctx, s, email, password); err != nil || id != active {
			t.Errorf("%q: %v, %v; want %v", email, id, err, active)
		}
	}
	for _, c := range []struct{ email, password string }{
		{"jo@example.com", "correct horse battery stapler"},
		{"jo@example.com", ""},
		{"nobody@example.com", "correct horse battery staple"},
		{"suspended@example.com", "correct horse battery staple"},
		{"leaving@example.com", "correct horse battery staple"},
		{"deleted@example.com", "correct horse battery staple"},
		{"nopassword@example.com", "correct horse battery staple"},
		{"nopassword@example.com", ""},
	} {
		if id, err := signsIn(ctx, s, c.email, secret.New(c.password)); !errors.Is(err, ErrInvalidCredentials) ||
			id != (ids.UUID{}) {
			t.Errorf("%q with %q: %v, %v; want ErrInvalidCredentials", c.email, c.password, id, err)
		}
	}
	if got := passwordHash(t, admin, active); got != hash {
		t.Errorf("signing in changed a current hash to %q", got)
	}
}

// TestAuthenticateRehashes checks that signing in replaces a hash made with
// old parameters with one made with the current parameters, which the same
// password matches, and that a wrong password changes nothing.
//
// Verifies: NFR-SEC-03
func TestAuthenticateRehashes(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	ctx := context.Background()
	password := secret.New("correct horse battery staple")
	old, err := newHasher(cheapParams, 1).Hash(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	id := addAccount(t, admin, "jo@example.com", pgtype.Text{String: old, Valid: true}, "active")

	if _, err := signsIn(ctx, s, "jo@example.com", secret.New("a wrong password")); !errors.Is(err,
		ErrInvalidCredentials) {
		t.Fatalf("a wrong password: %v", err)
	}
	if got := passwordHash(t, admin, id); got != old {
		t.Fatalf("a wrong password changed the hash to %q", got)
	}
	if got, err := signsIn(ctx, s, "jo@example.com", password); err != nil || got != id {
		t.Fatalf("the right password: %v, %v", got, err)
	}
	rehashed := passwordHash(t, admin, id)
	if !strings.HasPrefix(rehashed, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("the hash is still %q", rehashed)
	}
	if match, rehash, err := s.hasher.Verify(ctx, rehashed, password); err != nil || !match || rehash {
		t.Errorf("the new hash: match %t, rehash %t, error %v", match, rehash, err)
	}
	if _, err := signsIn(ctx, s, "jo@example.com", password); err != nil {
		t.Fatal(err)
	}
	if got := passwordHash(t, admin, id); got != rehashed {
		t.Errorf("signing in again changed the current hash to %q", got)
	}
}

// TestRehashKeepsANewerPassword checks that replacing an old hash leaves the
// account's password alone if it changed after the sign-in read it.
func TestRehashKeepsANewerPassword(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	ctx := context.Background()
	id := addAccount(t, admin, "jo@example.com", pgtype.Text{String: "old", Valid: true}, "active")
	if _, err := admin.Exec(ctx, `UPDATE "user" SET password_hash = 'newer' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	stored, err := s.rehash(ctx, pgtype.UUID{Bytes: id, Valid: true}, "old", secret.New("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	if stored != "old" {
		t.Errorf("rehash says it stored %q, want old, as it stored nothing", stored)
	}
	if got := passwordHash(t, admin, id); got != "newer" {
		t.Errorf("the hash is %q, want the newer one", got)
	}
}

// TestAuthenticateMalformedHash checks that a stored hash that is not an
// argon2id hash is an error of the server, rather than a wrong password.
func TestAuthenticateMalformedHash(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	addAccount(t, admin, "jo@example.com", pgtype.Text{String: "$2a$10$bcrypt", Valid: true}, "active")
	_, err := signsIn(context.Background(), s, "jo@example.com", secret.New("correct horse battery staple"))
	if !errors.Is(err, errMalformedHash) {
		t.Errorf("%v, want errMalformedHash", err)
	}
}

// TestCheckSignupSetup checks that until first-run setup has completed,
// CheckSignup and Register refuse every registration with ErrSetupRequired,
// whatever the policy, and that afterwards the capability SignupSetup
// follows the policy that setup chose, or closed if it chose none, while a
// policy that the configuration sets overrides it.
//
// Verifies: FR-ADMIN-02/AC4
func TestCheckSignupSetup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	db, admin := d.Open(t), d.Admin(t)
	for _, p := range []config.SignupPolicy{config.SignupOpen, config.SignupInvite, config.SignupClosed,
		config.SignupSetup} {
		s := New(db, options(db, p))
		if err := s.CheckSignup(ctx); !errors.Is(err, ErrSetupRequired) {
			t.Errorf("%s before setup: CheckSignup = %v", p, err)
		}
		err := s.Register(ctx, Registration{Email: "jo@example.com", DisplayName: "Jo",
			Password: secret.New("correct horse battery staple")})
		if !errors.Is(err, ErrSetupRequired) {
			t.Errorf("%s before setup: Register = %v", p, err)
		}
	}
	if got := accounts(t, admin); len(got) != 0 {
		t.Fatalf("%d accounts before setup, want none", len(got))
	}
	for _, c := range []struct {
		capability, chosen config.SignupPolicy
		want               error
	}{
		{config.SignupSetup, "", ErrSignupClosed},
		{config.SignupSetup, config.SignupOpen, nil},
		{config.SignupSetup, config.SignupInvite, ErrInviteRequired},
		{config.SignupClosed, config.SignupOpen, ErrSignupClosed},
		{config.SignupOpen, config.SignupClosed, nil},
	} {
		chosen := any(nil)
		if c.chosen != "" {
			chosen = string(c.chosen)
		}
		if _, err := admin.Exec(ctx, "UPDATE instance SET setup_completed_at = now(), signup_policy = $1",
			chosen); err != nil {
			t.Fatal(err)
		}
		if err := New(db, options(db, c.capability)).CheckSignup(ctx); !errors.Is(err, c.want) {
			t.Errorf("%s with %q chosen: CheckSignup = %v, want %v", c.capability, c.chosen, err, c.want)
		}
	}
}
