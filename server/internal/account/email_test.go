package account

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/email"
	"github.com/CyberCute-Studio/storycreep/server/internal/email/emailtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// testPublicURL is the instance's URL in the tests of links.
var testPublicURL = func() config.PublicURL {
	c, err := config.Load(config.Options{Env: config.MapEnvironment(map[string]string{
		"STORYCREEP_PUBLIC_URL": "https://storycreep.example.com", "STORYCREEP_DATABASE_URL": "postgres://u:p@db/d"})})
	if err != nil {
		panic(err)
	}
	return c.PublicURL
}()

// queueSpy records the jobs that a Service enqueues, instead of enqueueing
// them.
type queueSpy struct {
	mu   sync.Mutex
	jobs []jobs.Args
}

func (q *queueSpy) Enqueue(_ context.Context, _ pgx.Tx, args jobs.Args, _ *jobs.EnqueueOptions) (jobs.Enqueued,
	error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, args)
	return jobs.Enqueued{}, nil
}

// take returns the tokens of the email jobs that were enqueued since the
// last call, and forgets them.
func (q *queueSpy) take(t *testing.T) []ids.UUID {
	t.Helper()
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []ids.UUID
	for _, a := range q.jobs {
		link, ok := a.(emailLink)
		if !ok {
			t.Fatalf("an unexpected job %T", a)
		}
		id, err := ids.ParseUUID(link.Token)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	q.jobs = nil
	return out
}

// mailer is a Service that emails links through an SMTP server for the
// test, with what the test reads them with.
type mailer struct {
	*Service
	db     DB
	admin  *pgx.Conn
	queue  *queueSpy
	server *emailtest.Server
}

// withEmail returns a Service, on a new database whose first-run setup has
// completed, that sends its email through a new SMTP server, or none if
// send is false.
func withEmail(t *testing.T, send bool) *mailer {
	t.Helper()
	d := dbtest.New(t)
	d.MarkSetUp(t)
	db := d.Open(t)
	m := &mailer{db: db, admin: d.Admin(t), queue: &queueSpy{}}
	opts := options(db, config.SignupOpen)
	opts.Queue, opts.PublicURL = m.queue, testPublicURL
	if send {
		m.server = emailtest.Start(t, emailtest.Options{})
		settings := m.server.Settings(config.SMTPNone, "Storycreep <noreply@example.com>")
		opts.Email = email.New(db, email.Options{SMTP: &settings})
	}
	m.Service = New(db, opts)
	return m
}

// sendAll runs the email jobs that were enqueued, and returns the emails
// that they sent, in order.
func (m *mailer) sendAll(t *testing.T) []emailtest.Message {
	t.Helper()
	before := len(m.server.Messages())
	for _, id := range m.queue.take(t) {
		if err := m.sendLink(context.Background(), m.db, id); err != nil {
			t.Fatal(err)
		}
	}
	return m.server.Messages()[before:]
}

// TestRegisterEmailsVerification checks that registering on an instance that
// sends email emails the new account a link that verifies its address, once,
// which works for 24 hours, and that registering an address that has an
// account emails nothing, after it has done the same work: it adds a token
// without the account, and a job, which deletes the token (FR-AUTH-01 AC1).
func TestRegisterEmailsVerification(t *testing.T) {
	t.Parallel()
	m := withEmail(t, true)
	ctx := context.Background()
	id := register(t, m.Service, m.admin, "jo@example.com")
	sent := m.sendAll(t)
	if len(sent) != 1 || sent[0].To[0] != "jo@example.com" {
		t.Fatalf("sent %+v, want one email to jo@example.com", sent)
	}
	body := sent[0].Body(t)
	if !strings.Contains(body, "https://storycreep.example.com/app/verify-email#token=") ||
		!strings.Contains(body, "24 hours") || !strings.HasPrefix(body, "Hello Jo,") {
		t.Errorf("the email says %q", body)
	}
	if n := count(t, m.admin, `SELECT count(*) FROM email_token WHERE user_id = $1 AND kind = 'email_verification'
		AND expires_at BETWEEN now() + interval '23 hours 59 minutes' AND now() + interval '24 hours'`, id); n != 1 {
		t.Errorf("%d verification tokens that last 24 hours, want 1", n)
	}
	token := secret.New(sent[0].Token(t))
	if err := m.VerifyEmail(ctx, token, testClient); err != nil {
		t.Fatal(err)
	}
	if !accounts(t, m.admin)[0].emailVerified {
		t.Error("the address is not verified")
	}
	if err := m.VerifyEmail(ctx, token, testClient); !errors.Is(err, ErrTokenUsed) {
		t.Errorf("a second use: %v, want ErrTokenUsed", err)
	}
	if n := count(t, m.admin, `SELECT count(*) FROM audit_entry WHERE action = 'auth.email_verify'
		AND subject_id = $1 AND actor_user_id = $1 AND workspace_id IS NULL`, id); n != 1 {
		t.Errorf("%d audit entries of the verification, want 1", n)
	}

	before := count(t, m.admin, "SELECT count(*) FROM email_token")
	if err := m.Register(ctx, Registration{Email: "JO@example.com", DisplayName: "Mallory",
		Password: secret.New(testPassword)}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, m.admin, "SELECT count(*) FROM email_token WHERE user_id IS NULL"); n != 1 ||
		count(t, m.admin, "SELECT count(*) FROM email_token") != before+1 {
		t.Errorf("registering an address that has an account added %d tokens without an account, want 1", n)
	}
	if sent := m.sendAll(t); len(sent) != 0 {
		t.Errorf("registering an address that has an account sent %d emails", len(sent))
	}
	if n := count(t, m.admin, "SELECT count(*) FROM email_token WHERE user_id IS NULL"); n != 0 {
		t.Errorf("the job left %d tokens without an account", n)
	}
	// Asking again for a verified address sends nothing either.
	if err := m.RequestVerification(ctx, id); err != nil {
		t.Fatal(err)
	}
	if jobs := m.queue.take(t); len(jobs) != 0 {
		t.Errorf("asking to verify a verified address enqueued %d emails", len(jobs))
	}
}

// TestVerifyEmailVerifiedAlready checks that a second link of an address,
// which the first verified, is used without another entry in the audit log,
// because it verifies nothing.
func TestVerifyEmailVerifiedAlready(t *testing.T) {
	t.Parallel()
	m := withEmail(t, true)
	ctx := context.Background()
	jo := register(t, m.Service, m.admin, "jo@example.com")
	if err := m.RequestVerification(ctx, jo); err != nil {
		t.Fatal(err)
	}
	sent := m.sendAll(t)
	if len(sent) != 2 {
		t.Fatalf("sent %d emails, want 2", len(sent))
	}
	for _, msg := range sent {
		if err := m.VerifyEmail(ctx, secret.New(msg.Token(t)), testClient); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.VerifyEmail(ctx, secret.New(sent[1].Token(t)), testClient); !errors.Is(err, ErrTokenUsed) {
		t.Errorf("the second link again: %v, want ErrTokenUsed", err)
	}
	if n := count(t, m.admin, `SELECT count(*) FROM audit_entry WHERE action = 'auth.email_verify'
		AND subject_id = $1`, jo); n != 1 {
		t.Errorf("%d audit entries of the verification, want 1", n)
	}
}

// TestPasswordResetRequestSameWork checks that a request to reset the
// password of an address that belongs to no account does the same work as
// one for an address that does, adding one token and one job, and that the
// job deletes its token, and sends nothing, while the other's emails the
// link (FR-AUTH-03 AC3).
func TestPasswordResetRequestSameWork(t *testing.T) {
	t.Parallel()
	m := withEmail(t, true)
	ctx := context.Background()
	register(t, m.Service, m.admin, "jo@example.com")
	m.queue.take(t)
	for _, address := range []string{"JO@example.com", "nobody@example.com", "not an address\x00"} {
		before := count(t, m.admin, "SELECT count(*) FROM email_token")
		if err := m.RequestPasswordReset(ctx, address); err != nil {
			t.Fatalf("%q: %v", address, err)
		}
		if n := count(t, m.admin, "SELECT count(*) FROM email_token") - before; n != 1 {
			t.Errorf("%q added %d tokens, want 1", address, n)
		}
		jobs := m.queue.take(t)
		if len(jobs) != 1 {
			t.Fatalf("%q enqueued %d jobs, want 1", address, len(jobs))
		}
		if err := m.sendLink(ctx, m.db, jobs[0]); err != nil {
			t.Fatal(err)
		}
	}
	sent := m.server.Messages()
	if len(sent) != 1 || sent[0].To[0] != "jo@example.com" {
		t.Fatalf("sent %d emails, want one to jo@example.com", len(sent))
	}
	if !strings.Contains(sent[0].Body(t), "https://storycreep.example.com/app/reset-password#token=") {
		t.Errorf("the email says %q", sent[0].Body(t))
	}
	if n := count(t, m.admin, "SELECT count(*) FROM email_token WHERE user_id IS NULL"); n != 0 {
		t.Errorf("%d tokens of requests without an account are left", n)
	}
}

// TestResetPassword checks that a reset link replaces the password, once,
// signs the account out everywhere, and stops the account's other reset
// links from working, and that a password that breaks the policy leaves the
// link as it was (FR-AUTH-03 AC2, AC4).
func TestResetPassword(t *testing.T) {
	t.Parallel()
	m := withEmail(t, true)
	ctx := context.Background()
	id := register(t, m.Service, m.admin, "jo@example.com")
	m.queue.take(t)
	session := login(t, m.Service, "jo@example.com")
	for range 2 {
		if err := m.RequestPasswordReset(ctx, "jo@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	sent := m.sendAll(t)
	if len(sent) != 2 {
		t.Fatalf("sent %d emails, want 2", len(sent))
	}
	first, second := secret.New(sent[0].Token(t)), secret.New(sent[1].Token(t))
	var weak *PasswordError
	if err := m.ResetPassword(ctx, first, secret.New("short"), testClient); !errors.As(err, &weak) {
		t.Fatalf("a weak password: %v, want a *PasswordError", err)
	}
	replacement := secret.New("an entirely new passphrase")
	if err := m.ResetPassword(ctx, first, replacement, testClient); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Session(ctx, session.Secret); !errors.Is(err, ErrNoSession) {
		t.Errorf("the session that signed in before the reset: %v, want ErrNoSession", err)
	}
	if got, err := signsIn(ctx, m.Service, "jo@example.com", replacement); err != nil || got != id {
		t.Errorf("signing in with the new password: %v", err)
	}
	if err := m.ResetPassword(ctx, first, secret.New("yet another passphrase"), testClient); !errors.Is(err,
		ErrTokenUsed) {
		t.Errorf("the link again: %v, want ErrTokenUsed", err)
	}
	if err := m.ResetPassword(ctx, second, secret.New("yet another passphrase"), testClient); !errors.Is(err,
		ErrTokenInvalid) {
		t.Errorf("the other link: %v, want ErrTokenInvalid", err)
	}
	for action, want := range map[string]int{"auth.password_reset": 1, "auth.session_revoke": 1} {
		if n := count(t, m.admin, `SELECT count(*) FROM audit_entry WHERE action = $1 AND actor_user_id = $2`,
			action, id); n != want {
			t.Errorf("%d audit entries of %s, want %d", n, action, want)
		}
	}
	if n := count(t, m.admin, `SELECT count(*) FROM audit_entry WHERE action = 'auth.session_revoke'
		AND metadata->>'reason' = 'password_reset'`); n != 1 {
		t.Errorf("%d revocations with the reason password_reset, want 1", n)
	}
}

// TestChangePasswordInvalidatesResetLinks checks that changing the password
// stops the account's reset links from working (FR-AUTH-03 AC4).
func TestChangePasswordInvalidatesResetLinks(t *testing.T) {
	t.Parallel()
	m := withEmail(t, true)
	ctx := context.Background()
	register(t, m.Service, m.admin, "jo@example.com")
	m.queue.take(t)
	if err := m.RequestPasswordReset(ctx, "jo@example.com"); err != nil {
		t.Fatal(err)
	}
	token := secret.New(m.sendAll(t)[0].Token(t))
	session := login(t, m.Service, "jo@example.com")
	if _, err := m.ChangePassword(ctx, session.Session, secret.New(testPassword),
		secret.New("an entirely new passphrase"), testClient); err != nil {
		t.Fatal(err)
	}
	if err := m.ResetPassword(ctx, token, secret.New("yet another passphrase"), testClient); !errors.Is(err,
		ErrTokenInvalid) {
		t.Errorf("the link after the change: %v, want ErrTokenInvalid", err)
	}
}

// TestSendLinkAgain checks that an attempt of the email job that is made
// again, as after one that failed, sends a new link, which replaces the
// first one.
func TestSendLinkAgain(t *testing.T) {
	t.Parallel()
	m := withEmail(t, true)
	ctx := context.Background()
	register(t, m.Service, m.admin, "jo@example.com")
	jobs := m.queue.take(t)
	for range 2 {
		if err := m.sendLink(ctx, m.db, jobs[0]); err != nil {
			t.Fatal(err)
		}
	}
	sent := m.server.Messages()
	if len(sent) != 2 || sent[0].Token(t) == sent[1].Token(t) {
		t.Fatalf("sent %d emails, want 2 with different links", len(sent))
	}
	if err := m.VerifyEmail(ctx, secret.New(sent[0].Token(t)), testClient); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("the first link: %v, want ErrTokenInvalid", err)
	}
	if err := m.VerifyEmail(ctx, secret.New(sent[1].Token(t)), testClient); err != nil {
		t.Errorf("the second link: %v", err)
	}
}

// TestSendLinkReadsTheAccount checks that the email job writes in the
// account's preferred locale, if it has one, rather than the request's, and
// sends no verification for an address that was verified since the link was
// asked for.
func TestSendLinkReadsTheAccount(t *testing.T) {
	t.Parallel()
	m := withEmail(t, true)
	ctx := context.Background()
	id := register(t, m.Service, m.admin, "jo@example.com")
	if err := m.RequestPasswordReset(ctx, "jo@example.com"); err != nil {
		t.Fatal(err)
	}
	tokens := m.queue.take(t)
	if len(tokens) != 2 {
		t.Fatalf("enqueued %d emails, want the verification and the reset", len(tokens))
	}
	exec(t, m.admin, `UPDATE "user" SET locale = 'de-AT' WHERE id = $1`, id)
	var locales []string
	if err := m.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		for _, token := range tokens {
			row, err := queries.New(tx).LockEmailTokenToSend(ctx, nullUUID(token))
			if err != nil {
				return err
			}
			locales = append(locales, row.Locale.String)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(locales, []string{"de-AT", "de-AT"}) {
		t.Errorf("the emails' locales are %q, want the account's", locales)
	}

	exec(t, m.admin, `UPDATE "user" SET email_verified_at = now() WHERE id = $1`, id)
	for _, token := range tokens {
		if err := m.sendLink(ctx, m.db, token); err != nil {
			t.Fatal(err)
		}
	}
	sent := m.server.Messages()
	if len(sent) != 1 || !strings.Contains(sent[0].Body(t), "/app/reset-password#token=") {
		t.Errorf("sent %d emails, want only the reset link", len(sent))
	}
}

// TestLinksRefused checks the links that cannot be used: one that has
// expired, which the email job sends no email for either, one of another
// kind, one of an account that is no longer active, one whose address is no
// longer the account's, and one that is not a token.
func TestLinksRefused(t *testing.T) {
	t.Parallel()
	m := withEmail(t, true)
	ctx := context.Background()
	jo := register(t, m.Service, m.admin, "jo@example.com")
	ana := register(t, m.Service, m.admin, "ana@example.com")
	kim := register(t, m.Service, m.admin, "kim@example.com")
	sent := m.sendAll(t)
	if len(sent) != 3 {
		t.Fatalf("sent %d emails, want 3", len(sent))
	}
	tokens := map[string]secret.Value{}
	for _, msg := range sent {
		tokens[msg.To[0]] = secret.New(msg.Token(t))
	}
	exec(t, m.admin, "UPDATE email_token SET expires_at = now() WHERE user_id = $1", jo)
	if err := m.VerifyEmail(ctx, tokens["jo@example.com"], testClient); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("an expired link: %v, want ErrTokenExpired", err)
	}
	if err := m.ResetPassword(ctx, tokens["ana@example.com"], secret.New("an entirely new passphrase"),
		testClient); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("a verification link that resets: %v, want ErrTokenInvalid", err)
	}
	exec(t, m.admin, `UPDATE "user" SET status = 'suspended' WHERE id = $1`, ana)
	if err := m.VerifyEmail(ctx, tokens["ana@example.com"], testClient); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("a suspended account's link: %v, want ErrTokenInvalid", err)
	}
	exec(t, m.admin, `UPDATE "user" SET email = 'kim@example.org' WHERE id = $1`, kim)
	if err := m.VerifyEmail(ctx, tokens["kim@example.com"], testClient); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("a link of a former address: %v, want ErrTokenInvalid", err)
	}
	for _, v := range []string{"", "x", strings.Repeat("A", 43)} {
		if err := m.VerifyEmail(ctx, secret.New(v), testClient); !errors.Is(err, ErrTokenInvalid) {
			t.Errorf("the token %q: %v, want ErrTokenInvalid", v, err)
		}
	}
	if n := count(t, m.admin, `SELECT count(*) FROM "user" WHERE email_verified_at IS NOT NULL`); n != 0 {
		t.Errorf("%d addresses are verified", n)
	}

	// The email job sends nothing for an expired token.
	if err := m.RequestVerification(ctx, jo); err != nil {
		t.Fatal(err)
	}
	exec(t, m.admin, "UPDATE email_token SET expires_at = now() WHERE user_id = $1", jo)
	if sent := m.sendAll(t); len(sent) != 0 {
		t.Errorf("sent %d emails for an expired link", len(sent))
	}
}

// TestNoEmail checks an instance that sends no email: it refuses to email
// links, registering enqueues nothing, and an instance admin, but no one
// else, takes the links as text, which work (FR-AUTH-03 AC1).
func TestNoEmail(t *testing.T) {
	t.Parallel()
	m := withEmail(t, false)
	ctx := context.Background()
	jo := register(t, m.Service, m.admin, "jo@example.com")
	admin := register(t, m.Service, m.admin, "admin@example.com")
	exec(t, m.admin, `UPDATE "user" SET is_instance_admin = true WHERE id = $1`, admin)
	if jobs := m.queue.take(t); len(jobs) != 0 {
		t.Errorf("registering enqueued %d emails", len(jobs))
	}
	if err := m.RequestPasswordReset(ctx, "jo@example.com"); !errors.Is(err, ErrEmailNotConfigured) {
		t.Errorf("RequestPasswordReset: %v, want ErrEmailNotConfigured", err)
	}
	if err := m.RequestVerification(ctx, jo); !errors.Is(err, ErrEmailNotConfigured) {
		t.Errorf("RequestVerification: %v, want ErrEmailNotConfigured", err)
	}
	if n := count(t, m.admin, "SELECT count(*) FROM email_token"); n != 0 {
		t.Errorf("%d tokens, want none", n)
	}
	if _, err := m.IssueLink(ctx, jo, LinkPasswordReset, "admin@example.com", testClient); !errors.Is(err,
		ErrNotInstanceAdmin) {
		t.Errorf("a link for someone who is not an instance admin: %v, want ErrNotInstanceAdmin", err)
	}
	if _, err := m.IssueLink(ctx, admin, LinkPasswordReset, "nobody@example.com", testClient); !errors.Is(err,
		ErrNoAccount) {
		t.Errorf("a link for an address of no account: %v, want ErrNoAccount", err)
	}
	verification, err := m.IssueLink(ctx, admin, LinkVerification, "JO@example.com", testClient)
	if err != nil {
		t.Fatal(err)
	}
	reset, err := m.IssueLink(ctx, admin, LinkPasswordReset, "jo@example.com", testClient)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(reset.URL.Reveal(), "https://storycreep.example.com/app/reset-password#token=") ||
		!strings.HasPrefix(verification.URL.Reveal(), "https://storycreep.example.com/app/verify-email#token=") {
		t.Errorf("the links are %s and %s", reset.URL.Reveal(), verification.URL.Reveal())
	}
	if s := fmt.Sprint(reset); strings.Contains(s, "#token=") {
		t.Errorf("a Link prints its secret: %s", s)
	}
	tokenOf := func(l Link) secret.Value {
		_, token, _ := strings.Cut(l.URL.Reveal(), "#token=")
		return secret.New(token)
	}
	if err := m.VerifyEmail(ctx, tokenOf(verification), testClient); err != nil {
		t.Errorf("the verification link: %v", err)
	}
	if err := m.ResetPassword(ctx, tokenOf(reset), secret.New("an entirely new passphrase"), testClient); err != nil {
		t.Errorf("the reset link: %v", err)
	}
	if n := count(t, m.admin, `SELECT count(*) FROM audit_entry WHERE action = 'auth.link_issue' AND actor_user_id = $1
		AND subject_id = $2`, admin, jo); n != 2 {
		t.Errorf("%d audit entries of the links' issue, want 2", n)
	}
}

// TestIssueLinkWithEmail checks that an instance that sends email gives no
// instance admin a link as text, while the operator's storycreep
// reset-password takes one all the same.
func TestIssueLinkWithEmail(t *testing.T) {
	t.Parallel()
	m := withEmail(t, true)
	ctx := context.Background()
	admin := register(t, m.Service, m.admin, "admin@example.com")
	exec(t, m.admin, `UPDATE "user" SET is_instance_admin = true WHERE id = $1`, admin)
	if _, err := m.IssueLink(ctx, admin, LinkPasswordReset, "admin@example.com", testClient); !errors.Is(err,
		ErrEmailConfigured) {
		t.Errorf("IssueLink: %v, want ErrEmailConfigured", err)
	}
	link, err := IssueResetLink(ctx, m.db, testPublicURL, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, m.admin, `SELECT count(*) FROM email_token WHERE user_id = $1 AND kind = 'password_reset'
		AND token_hash IS NOT NULL AND expires_at <= now() + interval '1 hour'`, admin); n != 1 ||
		link.URL.Reveal() == "" {
		t.Errorf("%d reset tokens that last an hour, and the link %q", n, link.URL.Reveal())
	}
	if n := count(t, m.admin, `SELECT count(*) FROM audit_entry WHERE action = 'auth.link_issue'
		AND actor_user_id IS NULL AND metadata->>'kind' = 'password_reset'`); n != 1 {
		t.Errorf("%d audit entries of the operator's link, want 1", n)
	}
}

// TestCleanUpEmailTokens checks that the cleanup job deletes the tokens that
// have expired, used or not, and keeps the others.
func TestCleanUpEmailTokens(t *testing.T) {
	t.Parallel()
	m := withEmail(t, true)
	ctx := context.Background()
	jo := register(t, m.Service, m.admin, "jo@example.com")
	register(t, m.Service, m.admin, "ana@example.com")
	m.queue.take(t)
	exec(t, m.admin, "UPDATE email_token SET expires_at = now() - interval '1 second', used_at = now() WHERE user_id = $1",
		jo)
	n, err := cleanUpEmailTokens(ctx, m.db)
	if err != nil || n != 1 {
		t.Errorf("cleanUpEmailTokens() = %d, %v; want 1", n, err)
	}
	if n := count(t, m.admin, "SELECT count(*) FROM email_token"); n != 1 {
		t.Errorf("%d tokens are left, want 1", n)
	}
}
