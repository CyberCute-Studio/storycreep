package account

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

const testPassword = "correct horse battery staple"

var testClient = Client{IP: netip.MustParseAddr("192.0.2.7"), UserAgent: "Test/1.0"}

// register registers an account with testPassword and returns its ID.
func register(t *testing.T, s *Service, admin *pgx.Conn, email string) ids.UUID {
	t.Helper()
	err := s.Register(context.Background(), Registration{Email: email, DisplayName: "Jo",
		Password: secret.New(testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	var id ids.UUID
	if err := admin.QueryRow(context.Background(), `SELECT id FROM "user" WHERE email = $1`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// login signs the account of email in with testPassword.
func login(t *testing.T, s *Service, email string) Issued {
	t.Helper()
	issued, err := s.Login(context.Background(), email, secret.New(testPassword), testClient)
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	return issued
}

// exec runs a statement as the superuser.
func exec(t *testing.T, admin *pgx.Conn, sql string, args ...any) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// count returns the number that a query counts, as the superuser.
func count(t *testing.T, admin *pgx.Conn, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestLogin checks that signing in starts a session for the account, whose
// secret is 32 random bytes that only the caller gets: the database holds
// the SHA-256 hash of the secret and nothing else of it, and two sessions
// have different secrets. The session records the client.
//
// Verifies: FR-AUTH-02/AC3
func TestLogin(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	id := register(t, s, admin, "jo@example.com")
	first, second := login(t, s, "JO@example.com"), login(t, s, "jo@example.com")
	for _, issued := range []Issued{first, second} {
		if issued.UserID != id || issued.IP != testClient.IP || issued.UserAgent != testClient.UserAgent {
			t.Errorf("the session is %+v, want one of %s from the client", issued.Session, id)
		}
		if got := issued.EndsAt.Sub(issued.CreatedAt); got != testLifetime {
			t.Errorf("the session lasts %v, want %v", got, testLifetime)
		}
		raw, err := base64.RawURLEncoding.Strict().DecodeString(issued.Secret.Reveal())
		if err != nil || len(raw) != 32 {
			t.Fatalf("the secret %q is not 32 bytes in base64url: %v", issued.Secret.Reveal(), err)
		}
		var hash []byte
		var row string
		if err := admin.QueryRow(context.Background(), `SELECT token_hash, session::text FROM session WHERE id = $1`,
			issued.ID).Scan(&hash, &row); err != nil {
			t.Fatal(err)
		}
		if sum := sha256.Sum256(raw); !bytes.Equal(hash, sum[:]) {
			t.Errorf("the database holds %x, not the secret's hash", hash)
		}
		for _, v := range []string{issued.Secret.Reveal(), hex.EncodeToString(raw), base64.StdEncoding.EncodeToString(raw)} {
			if strings.Contains(row, v) {
				t.Errorf("the session's row holds the secret: %s", row)
			}
		}
	}
	if first.Secret.Reveal() == second.Secret.Reveal() || first.ID == second.ID {
		t.Error("two sessions have the same secret or ID")
	}
	got, err := s.Session(context.Background(), first.Secret)
	if err != nil || got.ID != first.ID || got.UserID != id {
		t.Errorf("Session = %+v, %v; want the first session", got, err)
	}
}

// TestLoginRefused checks that a wrong password, an address of no account,
// an address that is not one, and an account that cannot sign in, are all
// refused with ErrInvalidCredentials, and start no session.
func TestLoginRefused(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	register(t, s, admin, "jo@example.com")
	suspended := register(t, s, admin, "mo@example.com")
	exec(t, admin, `UPDATE "user" SET status = 'suspended' WHERE id = $1`, suspended)
	for _, c := range []struct{ email, password string }{
		{"jo@example.com", "not the password at all"},
		{"nobody@example.com", testPassword},
		{"jo\x00@example.com", testPassword},
		{"", testPassword},
		{"mo@example.com", testPassword},
	} {
		_, err := s.Login(context.Background(), c.email, secret.New(c.password), testClient)
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%q: %v, want ErrInvalidCredentials", c.email, err)
		}
	}
	if n := count(t, admin, `SELECT count(*) FROM session`); n != 0 {
		t.Errorf("%d sessions, want none", n)
	}
}

// TestSessionEnds checks that a session is no longer found once it has
// been revoked, when it has been unused for the idle timeout, when the
// lifetime has passed since it started, whichever of the lifetime then and
// now is shorter, and while its account cannot sign in, and that a secret
// that is not one finds none.
func TestSessionEnds(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	id := register(t, s, admin, "jo@example.com")
	for _, c := range []struct {
		name, sql string
	}{
		{"revoked", `UPDATE session SET revoked_at = now() WHERE id = $1`},
		{"idle", `UPDATE session SET last_seen_at = now() - interval '30 days 1 second' WHERE id = $1`},
		{"past the lifetime", `UPDATE session SET created_at = now() - interval '90 days 1 second',
			last_seen_at = now() WHERE id = $1`},
		{"past its expiry", `UPDATE session SET expires_at = now() - interval '1 second' WHERE id = $1`},
		{"of a suspended account", `UPDATE "user" SET status = 'suspended' WHERE id =
			(SELECT user_id FROM session WHERE id = $1)`},
	} {
		exec(t, admin, `UPDATE "user" SET status = 'active' WHERE id = $1`, id)
		issued := login(t, s, "jo@example.com")
		exec(t, admin, c.sql, issued.ID)
		if _, err := s.Session(context.Background(), issued.Secret); !errors.Is(err, ErrNoSession) {
			t.Errorf("%s: %v, want ErrNoSession", c.name, err)
		}
	}
	exec(t, admin, `UPDATE "user" SET status = 'active' WHERE id = $1`, id)

	// A shorter lifetime ends the sessions that started under a longer one.
	issued := login(t, s, "jo@example.com")
	exec(t, admin, `UPDATE session SET created_at = now() - interval '2 hours' WHERE id = $1`, issued.ID)
	shorter := newService(s.db, Options{Signup: config.SignupOpen, IdleTimeout: testIdle, Lifetime: time.Hour},
		s.hasher)
	if _, err := shorter.Session(context.Background(), issued.Secret); !errors.Is(err, ErrNoSession) {
		t.Errorf("a session older than a shorter lifetime: %v, want ErrNoSession", err)
	}
	if _, err := s.Session(context.Background(), issued.Secret); err != nil {
		t.Errorf("the same session under the lifetime it started with: %v", err)
	}

	for _, v := range []string{"", "x", issued.Secret.Reveal() + "A", strings.Repeat("A", 43),
		base64.RawURLEncoding.EncodeToString(make([]byte, 16))} {
		if _, err := s.Session(context.Background(), secret.New(v)); !errors.Is(err, ErrNoSession) {
			t.Errorf("the secret %q: %v, want ErrNoSession", v, err)
		}
	}
}

// TestSessionUse checks that finding a session records its use only when
// the last one it recorded is more than a minute old, so that requests do
// not write to the database each time, and that the idle end follows it.
func TestSessionUse(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	register(t, s, admin, "jo@example.com")
	issued := login(t, s, "jo@example.com")
	exec(t, admin, `UPDATE session SET last_seen_at = now() - interval '30 seconds' WHERE id = $1`, issued.ID)
	var before time.Time
	if err := admin.QueryRow(context.Background(), `SELECT last_seen_at FROM session WHERE id = $1`,
		issued.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, err := s.Session(context.Background(), issued.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastSeenAt.Equal(before) || !got.IdleEndsAt.Equal(before.Add(testIdle)) {
		t.Errorf("a use 30 seconds ago: last seen %v, idle end %v; want %v and 30 days later", got.LastSeenAt,
			got.IdleEndsAt, before)
	}
	exec(t, admin, `UPDATE session SET last_seen_at = now() - interval '2 minutes' WHERE id = $1`, issued.ID)
	if got, err = s.Session(context.Background(), issued.Secret); err != nil {
		t.Fatal(err)
	}
	if n := count(t, admin, `SELECT count(*) FROM session WHERE id = $1 AND last_seen_at > now() - interval '1 minute'`,
		issued.ID); n != 1 || time.Since(got.LastSeenAt) > time.Minute {
		t.Errorf("a use 2 minutes ago is not recorded again: last seen %v", got.LastSeenAt)
	}
}

// TestSessionsAndRevocation checks the list of an account's sessions,
// newest first and in pages, without other accounts' sessions or ended
// ones, and the revocation of one session, which another account cannot
// revoke, and of all the others.
func TestSessionsAndRevocation(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	jo := register(t, s, admin, "jo@example.com")
	register(t, s, admin, "mo@example.com")
	var sessions []Issued
	for range 3 {
		sessions = append(sessions, login(t, s, "jo@example.com"))
	}
	ended := login(t, s, "jo@example.com")
	exec(t, admin, `UPDATE session SET revoked_at = now() WHERE id = $1`, ended.ID)
	mo := login(t, s, "mo@example.com")
	ctx := context.Background()

	var listed []ids.UUID
	before := ids.UUID{}
	for {
		page, err := s.Sessions(ctx, jo, before, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range page {
			listed = append(listed, p.ID)
		}
		if len(page) < 2 {
			break
		}
		before = page[len(page)-1].ID
	}
	want := []ids.UUID{sessions[2].ID, sessions[1].ID, sessions[0].ID}
	if len(listed) != len(want) || listed[0] != want[0] || listed[1] != want[1] || listed[2] != want[2] {
		t.Errorf("the sessions listed are %v, want %v", listed, want)
	}

	if err := s.RevokeSession(ctx, jo, mo.ID, testClient); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("revoking another account's session: %v, want ErrSessionNotFound", err)
	}
	if err := s.RevokeSession(ctx, jo, ended.ID, testClient); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("revoking an ended session: %v, want ErrSessionNotFound", err)
	}
	if err := s.RevokeSession(ctx, jo, sessions[0].ID, testClient); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, sessions[0].Secret); !errors.Is(err, ErrNoSession) {
		t.Errorf("a revoked session is still found: %v", err)
	}
	n, err := s.RevokeOtherSessions(ctx, sessions[2].Session, testClient)
	if err != nil || n != 1 {
		t.Errorf("RevokeOtherSessions = %d, %v; want 1", n, err)
	}
	if _, err := s.Session(ctx, sessions[1].Secret); !errors.Is(err, ErrNoSession) {
		t.Errorf("another session is still found: %v", err)
	}
	for _, kept := range []Issued{sessions[2], mo} {
		if _, err := s.Session(ctx, kept.Secret); err != nil {
			t.Errorf("the session that asked, or another account's, ended: %v", err)
		}
	}
	if n := count(t, admin, `SELECT count(*) FROM audit_entry WHERE action = 'auth.session_revoke'
		AND metadata->>'reason' = 'revoked' AND actor_user_id = $1`, jo); n != 2 {
		t.Errorf("%d revocations are recorded, want 2", n)
	}
}

// TestLogout checks that signing out deletes the session.
func TestLogout(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	register(t, s, admin, "jo@example.com")
	issued := login(t, s, "jo@example.com")
	if err := s.Logout(context.Background(), issued.Session, testClient); err != nil {
		t.Fatal(err)
	}
	if n := count(t, admin, `SELECT count(*) FROM session WHERE id = $1`, issued.ID); n != 0 {
		t.Error("the session's row is still there")
	}
	// Signing out of a session that is gone already changes nothing.
	if err := s.Logout(context.Background(), issued.Session, testClient); err != nil {
		t.Errorf("signing out again: %v", err)
	}
	if n := count(t, admin, `SELECT count(*) FROM audit_entry WHERE action = 'auth.session_revoke'
		AND metadata->>'reason' = 'sign_out' AND subject_id = $1`, issued.ID); n != 1 {
		t.Errorf("%d sign-outs are recorded, want 1", n)
	}
}

// TestChangePassword checks that a password change needs the current
// password and a new one that the policy allows, and that it revokes the
// account's other sessions, and gives the session that asked a new secret,
// in which the old one no longer finds it.
func TestChangePassword(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	jo := register(t, s, admin, "jo@example.com")
	register(t, s, admin, "mo@example.com")
	current, other := login(t, s, "jo@example.com"), login(t, s, "jo@example.com")
	mo := login(t, s, "mo@example.com")
	ctx := context.Background()
	replacement := secret.New("a brand new passphrase")

	_, err := s.ChangePassword(ctx, current.Session, secret.New("not the password at all"), replacement, testClient)
	if !errors.Is(err, ErrWrongPassword) {
		t.Errorf("a wrong current password: %v, want ErrWrongPassword", err)
	}
	var weak *PasswordError
	if _, err := s.ChangePassword(ctx, current.Session, secret.New(testPassword), secret.New("short"),
		testClient); !errors.As(err, &weak) {
		t.Errorf("a weak new password: %v, want a *PasswordError", err)
	}
	if _, err := s.Session(ctx, other.Secret); err != nil {
		t.Fatalf("a refused change revoked a session: %v", err)
	}

	issued, err := s.ChangePassword(ctx, current.Session, secret.New(testPassword), replacement, testClient)
	if err != nil {
		t.Fatal(err)
	}
	if issued.ID != current.ID || issued.Secret.Reveal() == current.Secret.Reveal() {
		t.Errorf("the session after the change is %v with the same secret: %t", issued.ID,
			issued.Secret.Reveal() == current.Secret.Reveal())
	}
	for name, v := range map[string]secret.Value{"the old secret": current.Secret, "the other session": other.Secret} {
		if _, err := s.Session(ctx, v); !errors.Is(err, ErrNoSession) {
			t.Errorf("%s is still found: %v", name, err)
		}
	}
	for name, v := range map[string]secret.Value{"the new secret": issued.Secret, "another account's": mo.Secret} {
		if _, err := s.Session(ctx, v); err != nil {
			t.Errorf("%s is not found: %v", name, err)
		}
	}
	if _, err := signsIn(ctx, s, "jo@example.com", replacement); err != nil {
		t.Errorf("the new password does not sign in: %v", err)
	}
	if _, err := signsIn(ctx, s, "jo@example.com", secret.New(testPassword)); !errors.Is(err,
		ErrInvalidCredentials) {
		t.Errorf("the old password: %v, want ErrInvalidCredentials", err)
	}
	for _, c := range []struct {
		sql  string
		want int
	}{
		{`SELECT count(*) FROM audit_entry WHERE action = 'auth.password_change' AND result = 'failure'`, 1},
		{`SELECT count(*) FROM audit_entry WHERE action = 'auth.password_change' AND result = 'success'`, 1},
		{`SELECT count(*) FROM audit_entry WHERE action = 'auth.session_revoke'
			AND metadata->>'reason' = 'password_changed'`, 1},
	} {
		if n := count(t, admin, c.sql+` AND actor_user_id = $1`, jo); n != c.want {
			t.Errorf("%s: %d, want %d", c.sql, n, c.want)
		}
	}

	// A session that has ended changes nothing.
	exec(t, admin, `UPDATE session SET revoked_at = now() WHERE id = $1`, issued.ID)
	if _, err := s.ChangePassword(ctx, issued.Session, replacement, secret.New(testPassword),
		testClient); !errors.Is(err, ErrNoSession) {
		t.Errorf("a change from an ended session: %v, want ErrNoSession", err)
	}
	if _, err := signsIn(ctx, s, "jo@example.com", replacement); err != nil {
		t.Errorf("the change from an ended session changed the password: %v", err)
	}
}

// TestLoginRacesPasswordChange checks that a sign-in that checked the old
// password before the password changed starts no session afterwards, which
// the change's revocation of the other sessions would have missed, nor one
// for an account that stopped being active meanwhile, and that it starts
// one while the account still holds the hash that it checked.
//
// Verifies: FR-AUTH-02/AC4
func TestLoginRacesPasswordChange(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	id := register(t, s, admin, "jo@example.com")
	ctx := context.Background()
	check := func() string {
		t.Helper()
		got, _, hash, err := s.authenticate(ctx, "jo@example.com", secret.New(testPassword))
		if err != nil || got != id || hash == "" {
			t.Fatalf("authenticate = %v, %q, %v; want the account and its hash", got, hash, err)
		}
		return hash
	}

	if _, err := s.startSession(ctx, id, check(), testClient); err != nil {
		t.Errorf("a sign-in whose hash the account still holds: %v", err)
	}
	stale := check()
	changer := login(t, s, "jo@example.com")
	if _, err := s.ChangePassword(ctx, changer.Session, secret.New(testPassword), secret.New("a brand new passphrase"),
		testClient); err != nil {
		t.Fatal(err)
	}
	if _, err := s.startSession(ctx, id, stale, testClient); !errors.Is(err, errCredentialsChanged) {
		t.Errorf("a sign-in that checked the old password: %v, want errCredentialsChanged", err)
	}
	if n := count(t, admin, `SELECT count(*) FROM session WHERE revoked_at IS NULL`); n != 1 {
		t.Errorf("%d active sessions, want only the one that changed the password", n)
	}

	other := register(t, s, admin, "mo@example.com")
	_, _, hash, err := s.authenticate(ctx, "mo@example.com", secret.New(testPassword))
	if err != nil {
		t.Fatal(err)
	}
	exec(t, admin, `UPDATE "user" SET status = 'suspended' WHERE id = $1`, other)
	if _, err := s.startSession(ctx, other, hash, testClient); !errors.Is(err, errCredentialsChanged) {
		t.Errorf("a sign-in of an account suspended meanwhile: %v, want errCredentialsChanged", err)
	}
	if n := count(t, admin, `SELECT count(*) FROM session WHERE user_id = $1`, other); n != 0 {
		t.Errorf("the suspended account has %d sessions, want none", n)
	}
}

// TestPasswordChangeWaitsForSignIn checks that a password change that comes
// while a sign-in's transaction holds the lock on the account
// (startSession) waits for that transaction, and then revokes the session
// that it started with the others.
//
// Verifies: FR-AUTH-02/AC4
func TestPasswordChangeWaitsForSignIn(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	id := register(t, s, admin, "jo@example.com")
	changer := login(t, s, "jo@example.com")
	ctx := context.Background()

	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := queries.New(tx).LockPasswordHash(ctx, nullUUID(id)); err != nil {
		t.Fatal(err)
	}
	changed := make(chan error, 1)
	go func() {
		_, err := s.ChangePassword(ctx, changer.Session, secret.New(testPassword),
			secret.New("a brand new passphrase"), testClient)
		changed <- err
	}()
	// The change has checked the current password, hashed the new one, and
	// waits for the lock once a transaction waits for this one.
	awaitWaiter(t, tx, changed, "the change")
	issued, err := s.insertSession(ctx, tx, id, testClient)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-changed; err != nil {
		t.Fatalf("the change: %v", err)
	}
	if _, err := s.Session(ctx, issued.Secret); !errors.Is(err, ErrNoSession) {
		t.Errorf("the session that the sign-in started while the password changed: %v, want ErrNoSession", err)
	}
}

// awaitWaiter returns once another transaction waits for a lock that tx
// holds, and fails the test if done receives first, because what, which
// should wait, did not, or if nothing waits within a minute.
func awaitWaiter(t *testing.T, tx pgx.Tx, done <-chan error, what string) {
	t.Helper()
	const waiters = `SELECT count(*) FROM pg_locks WHERE NOT granted AND locktype = 'transactionid'
		AND transactionid::text = pg_current_xact_id()::text`
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(20 * time.Millisecond) {
		var waiting int
		if err := tx.QueryRow(context.Background(), waiters).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("%s did not wait for the lock: %v", what, err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never waits for the lock", what)
		}
	}
}

// TestRevokeOtherSessionsWaitsForPasswordChange checks that revoking the
// account's other sessions waits while a transaction holds the account's row,
// as a password change does from its update on, before it revokes the
// sessions itself and gives its own a new secret, so that the two run one
// after the other rather than each lock sessions that the other waits for.
func TestRevokeOtherSessionsWaitsForPasswordChange(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	id := register(t, s, admin, "jo@example.com")
	current, other := login(t, s, "jo@example.com"), login(t, s, "jo@example.com")
	ctx := context.Background()

	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE "user" SET updated_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	revoked := make(chan error, 1)
	go func() {
		_, err := s.RevokeOtherSessions(ctx, current.Session, testClient)
		revoked <- err
	}()
	awaitWaiter(t, tx, revoked, "the revocation")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatalf("the revocation: %v", err)
	}
	if _, err := s.Session(ctx, other.Secret); !errors.Is(err, ErrNoSession) {
		t.Errorf("the other session: %v, want ErrNoSession", err)
	}
}

// hookDB is a DB that calls before with the number of each transaction that
// it begins, counted from 1, just before it begins it.
type hookDB struct {
	DB
	before func(n int)
	n      int
}

func (h *hookDB) InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error {
	h.n++
	h.before(h.n)
	return h.DB.InTx(ctx, scope, fn)
}

// TestChangePasswordRacesHashChange checks a password change during which
// the hash of the account's password changes, after the change checked the
// current password against it: when another sign-in hashed the same
// password again (NFR-SEC-03), the change still goes through, and when
// another change replaced the password, it is refused as the wrong password,
// recorded so, and changes nothing.
func TestChangePasswordRacesHashChange(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	ctx := context.Background()
	replacement := secret.New("a brand new passphrase")
	for _, c := range []struct {
		name, email, stored string
		changes             bool
	}{
		{"hashed again", "jo@example.com", testPassword, true},
		{"changed", "mo@example.com", "another passphrase entirely", false},
	} {
		id := register(t, s, admin, c.email)
		current, other := login(t, s, c.email), login(t, s, c.email)
		stored, err := s.hasher.Hash(ctx, secret.New(c.stored))
		if err != nil {
			t.Fatal(err)
		}
		// The change's first transaction reads the hash, and its second
		// replaces it, once the current password matched it.
		db := &hookDB{DB: s.db, before: func(n int) {
			if n == 2 {
				exec(t, admin, `UPDATE "user" SET password_hash = $1 WHERE id = $2`, stored, id)
			}
		}}
		racing := newService(db, Options{Signup: config.SignupOpen, IdleTimeout: testIdle, Lifetime: testLifetime},
			s.hasher)
		issued, err := racing.ChangePassword(ctx, current.Session, secret.New(testPassword), replacement, testClient)
		failures := count(t, admin, `SELECT count(*) FROM audit_entry WHERE action = 'auth.password_change'
			AND result = 'failure' AND actor_user_id = $1`, id)
		_, otherErr := s.Session(ctx, other.Secret)
		if c.changes {
			if err != nil || issued.ID != current.ID {
				t.Fatalf("%s: ChangePassword = %v, %v; want the session with a new secret", c.name, issued.ID, err)
			}
			if _, err := signsIn(ctx, s, c.email, replacement); err != nil {
				t.Errorf("%s: the new password does not sign in: %v", c.name, err)
			}
			if !errors.Is(otherErr, ErrNoSession) || failures != 0 {
				t.Errorf("%s: the other session: %v; %d failures recorded, want none", c.name, otherErr, failures)
			}
			continue
		}
		if !errors.Is(err, ErrWrongPassword) {
			t.Errorf("%s: ChangePassword = %v, want ErrWrongPassword", c.name, err)
		}
		if _, err := signsIn(ctx, s, c.email, secret.New(c.stored)); err != nil {
			t.Errorf("%s: the password that replaced the old one does not sign in: %v", c.name, err)
		}
		if otherErr != nil || failures != 1 {
			t.Errorf("%s: the other session: %v; %d failures recorded, want 1", c.name, otherErr, failures)
		}
	}
}

// TestRotate checks that a session's new secret finds it, and the old one
// no longer does.
func TestRotate(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	register(t, s, admin, "jo@example.com")
	issued := login(t, s, "jo@example.com")
	rotated, err := s.Rotate(context.Background(), issued.Session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(context.Background(), issued.Secret); !errors.Is(err, ErrNoSession) {
		t.Errorf("the old secret: %v, want ErrNoSession", err)
	}
	if got, err := s.Session(context.Background(), rotated.Secret); err != nil || got.ID != issued.ID ||
		!got.EndsAt.Equal(issued.EndsAt) {
		t.Errorf("the new secret finds %+v, %v; want the session, with its end", got, err)
	}
	if err := s.Logout(context.Background(), issued.Session, testClient); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rotate(context.Background(), issued.Session); !errors.Is(err, ErrNoSession) {
		t.Errorf("rotating an ended session: %v, want ErrNoSession", err)
	}
}

// TestCleanUpSessions checks that the session cleanup deletes the sessions
// that have ended, however many, and keeps the active ones.
func TestCleanUpSessions(t *testing.T) {
	t.Parallel()
	s, admin := setup(t, config.SignupOpen)
	id := register(t, s, admin, "jo@example.com")
	active := login(t, s, "jo@example.com")
	// More ended sessions than one batch deletes, of each way to end.
	exec(t, admin, `INSERT INTO session (id, user_id, token_hash, created_at, last_seen_at, expires_at, revoked_at)
		SELECT gen_random_uuid(), $1, sha256(int4send(n)),
			now() - CASE WHEN n % 4 = 1 THEN interval '91 days' ELSE interval '1 day' END,
			now() - CASE WHEN n % 4 = 2 THEN interval '31 days' ELSE interval '1 hour' END,
			now() + CASE WHEN n % 4 = 3 THEN interval '-1 second' ELSE interval '1 day' END,
			CASE WHEN n % 4 = 0 THEN now() END
		FROM generate_series(1, 2500) AS n`, id)
	n, err := s.cleanUpSessions(context.Background(), s.db)
	if err != nil || n != 2500 {
		t.Errorf("cleanUpSessions = %d, %v; want 2500", n, err)
	}
	if left := count(t, admin, `SELECT count(*) FROM session`); left != 1 {
		t.Errorf("%d sessions are left, want the active one", left)
	}
	if _, err := s.Session(context.Background(), active.Secret); err != nil {
		t.Errorf("the active session ended: %v", err)
	}
}
