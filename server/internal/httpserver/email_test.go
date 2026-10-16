package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/email/emailtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
)

const (
	instancePath          = "/api/v1/instance"
	passwordResetPath     = "/api/v1/auth/password-reset"
	resetConfirmPath      = "/api/v1/auth/password-reset/confirm"
	verifyEmailPath       = "/api/v1/auth/verify-email"
	emailVerificationPath = "/api/v1/auth/email-verification"
	resetLinksPath        = "/api/v1/admin/password-reset-links"
	verificationLinksPath = "/api/v1/admin/verification-links"
)

// smtpEnv returns the configuration of an instance that sends its email
// through server.
func smtpEnv(server *emailtest.Server) map[string]string {
	return map[string]string{"STORYCREEP_SMTP_URL": server.URL(), "STORYCREEP_SMTP_FROM": "noreply@example.com"}
}

// startWorkers runs the jobs of s on its database d, as an instance's
// workers do, until the test ends.
func startWorkers(t *testing.T, s *server, d *dbtest.Database) {
	t.Helper()
	ctx := context.Background()
	q, err := jobs.Open(ctx, d.Open(t), d.AppURL, jobs.Options{Registry: s.serverJobs(), Work: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := q.Stop(stopCtx); err != nil {
			t.Error(err)
		}
		q.Close()
	})
}

// emailJobs returns how many jobs that email a link the database d holds,
// whatever their state.
func emailJobs(t *testing.T, d *dbtest.Database) int {
	t.Helper()
	return countRows(t, d, "SELECT count(*) FROM river_job WHERE kind = 'email_link'")
}

// waitForEmailJobs waits until every job that emails a link that the
// database d holds is in state, as completed or cancelled.
func waitForEmailJobs(t *testing.T, d *dbtest.Database, state string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if countRows(t, d, "SELECT count(*) FROM river_job WHERE kind = 'email_link' AND state::text <> $1",
			state) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the jobs that email links are not all %s", state)
		}
	}
}

// tokenBody returns the JSON body of a request that uses the link token,
// with a new password if password is not empty.
func tokenBody(t *testing.T, token, password string) []byte {
	t.Helper()
	body := map[string]string{"token": token}
	if password != "" {
		body["new_password"] = password
	}
	return jsonBody(t, body)
}

// emailBody returns the JSON body of a request that names an email address.
func emailBody(t *testing.T, email string) []byte {
	t.Helper()
	return jsonBody(t, map[string]string{"email": email})
}

// takeLink takes a link of an account as text, as the instance admin admin,
// from path, and returns its token.
func takeLink(t *testing.T, s *server, admin signedIn, path, email string) string {
	t.Helper()
	res := send(s.main, http.MethodPost, path, emailBody(t, email), "", admin.header(true))
	var link linkBody
	if res.status != http.StatusCreated || json.Unmarshal(res.body, &link) != nil {
		t.Fatalf("POST %s: %d %s, want 201", path, res.status, res.body)
	}
	if time.Until(link.ExpiresAt) <= 0 {
		t.Errorf("the link has expired already: %s", res.body)
	}
	_, token, ok := strings.Cut(link.URL, "#token=")
	if !ok || !strings.HasPrefix(link.URL, "http://localhost:8080/app/") {
		t.Fatalf("the link is %q", link.URL)
	}
	return token
}

// setUpWithAdmin returns a server with the configuration of env, whose
// first-run setup made admin@example.com its owner, an instance admin, with
// open signup, signed in.
func setUpWithAdmin(t *testing.T, env map[string]string) (*server, *dbtest.Database, signedIn) {
	t.Helper()
	s, d := pendingServer(t, env)
	completeSetup(t, s, "admin@example.com", map[string]any{"signup_policy": "open"})
	return s, d, signIn(t, s.main, "admin@example.com")
}

// TestInstanceSaysWhetherEmailIsConfigured checks the flag that tells pages
// that email is not configured: it follows STORYCREEP_SMTP_URL, and else the
// SMTP server that first-run setup chose.
func TestInstanceSaysWhetherEmailIsConfigured(t *testing.T) {
	t.Parallel()
	check := func(s *server, want string) {
		t.Helper()
		res := send(s.main, http.MethodGet, instancePath, nil, "", nil)
		if res.status != http.StatusOK || strings.TrimSpace(string(res.body)) != want {
			t.Errorf("GET %s: %d %s, want %s", instancePath, res.status, res.body, want)
		}
	}
	none, _ := readyServer(t, nil)
	check(none, `{"email_configured":false}`)
	fixed, _ := readyServer(t, map[string]string{"STORYCREEP_SMTP_URL": "smtp://smtp.example.com",
		"STORYCREEP_SMTP_FROM": "noreply@example.com"})
	check(fixed, `{"email_configured":true}`)

	chosen, d := pendingServer(t, nil)
	check(chosen, `{"email_configured":false}`)
	completeSetup(t, chosen, "jo@example.com", map[string]any{"smtp": map[string]any{"host": "SMTP.example.com",
		"port": 2525, "security": "tls", "username": "jo", "password": "pw", "from": "Storycreep <noreply@example.com>"}})
	check(chosen, `{"email_configured":true}`)
	var settings string
	if err := d.Admin(t).QueryRow(context.Background(), `SELECT format('%s %s %s %s %s %s', smtp_host, smtp_port,
		smtp_security, smtp_username, smtp_password, smtp_from) FROM instance`).Scan(&settings); err != nil {
		t.Fatal(err)
	}
	if settings != "smtp.example.com 2525 tls jo pw Storycreep <noreply@example.com>" {
		t.Errorf("setup recorded %q", settings)
	}
}

// TestNoSMTPLinks checks an instance that sends no email: registering and
// asking for links reach no email code, as no job that emails a link is
// enqueued, and asking for a link is refused, the same for any address,
// while an instance admin takes a link that resets a password, and one that
// verifies an address, which the account's user completes.
//
// Verifies: FR-AUTH-03/AC1
func TestNoSMTPLinks(t *testing.T) {
	t.Parallel()
	s, d, admin := setUpWithAdmin(t, nil)
	jo := newNamedAccount(t, s.main, "jo@example.com", "Jo")
	for _, email := range []string{"jo@example.com", "nobody@example.com"} {
		res := send(s.main, http.MethodPost, passwordResetPath, emailBody(t, email), "", nil)
		if res.status != http.StatusForbidden || res.code() != api.CodeEmailNotConfigured {
			t.Errorf("POST %s for %s: %d %s, want 403 email_not_configured", passwordResetPath, email, res.status,
				res.body)
		}
	}
	res := send(s.main, http.MethodPost, emailVerificationPath, nil, "", jo.header(true))
	if res.status != http.StatusForbidden || res.code() != api.CodeEmailNotConfigured {
		t.Errorf("POST %s: %d %s, want 403 email_not_configured", emailVerificationPath, res.status, res.body)
	}

	verify := takeLink(t, s, admin, verificationLinksPath, "JO@example.com")
	if res := send(s.main, http.MethodPost, verifyEmailPath, tokenBody(t, verify, ""), "", nil); res.status !=
		http.StatusNoContent {
		t.Errorf("verify with the link: %d %s", res.status, res.body)
	}
	reset := takeLink(t, s, admin, resetLinksPath, "jo@example.com")
	const replacement = "an entirely new passphrase"
	if res := send(s.main, http.MethodPost, resetConfirmPath, tokenBody(t, reset, replacement), "", nil); res.status !=
		http.StatusNoContent {
		t.Fatalf("reset with the link: %d %s", res.status, res.body)
	}
	if res := send(s.main, http.MethodGet, sessionPath, nil, "", jo.header(false)); res.status !=
		http.StatusUnauthorized {
		t.Errorf("the session from before the reset: %d %s, want 401", res.status, res.body)
	}
	if res := send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com", replacement), "",
		nil); res.status != http.StatusOK {
		t.Errorf("sign in with the new password: %d %s", res.status, res.body)
	}
	if n := emailJobs(t, d); n != 0 {
		t.Errorf("%d jobs that email a link, want none", n)
	}
	if n := countRows(t, d, `SELECT count(*) FROM "user" WHERE email = 'jo@example.com'
		AND email_verified_at IS NOT NULL`); n != 1 {
		t.Error("the address is not verified")
	}
	// The audit log records each step with its client, in no workspace.
	for action, want := range map[string]int{"auth.link_issue": 2, "auth.email_verify": 1, "auth.password_reset": 1,
		"auth.session_revoke": 1} {
		if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = $1 AND workspace_id IS NULL
			AND host(ip) = '192.0.2.1' AND request_id IS NOT NULL`, action); n != want {
			t.Errorf("%d audit entries of %s with the client, want %d", n, action, want)
		}
	}
}

// TestLinksAreUsedOnce checks that a link that resets a password, and one
// that verifies an address, each work once, and that using one again is
// answered with 400 token_used, as is one that has expired with
// token_expired, and a token that is not one with token_invalid.
//
// Verifies: FR-AUTH-03/AC2
func TestLinksAreUsedOnce(t *testing.T) {
	t.Parallel()
	s, d, admin := setUpWithAdmin(t, nil)
	newAccount(t, s.main, "jo@example.com")
	for _, c := range []struct {
		path, links, password string
	}{
		{resetConfirmPath, resetLinksPath, "an entirely new passphrase"},
		{verifyEmailPath, verificationLinksPath, ""},
	} {
		token := takeLink(t, s, admin, c.links, "jo@example.com")
		if res := send(s.main, http.MethodPost, c.path, tokenBody(t, token, c.password), "", nil); res.status !=
			http.StatusNoContent {
			t.Fatalf("POST %s: %d %s, want 204", c.path, res.status, res.body)
		}
		res := send(s.main, http.MethodPost, c.path, tokenBody(t, token, c.password), "", nil)
		if res.status != http.StatusBadRequest || res.code() != api.CodeTokenUsed {
			t.Errorf("POST %s again: %d %s, want 400 token_used", c.path, res.status, res.body)
		}
	}
	expired := takeLink(t, s, admin, resetLinksPath, "jo@example.com")
	if _, err := d.Admin(t).Exec(context.Background(), "UPDATE email_token SET expires_at = now() WHERE used_at IS NULL"); err != nil {
		t.Fatal(err)
	}
	res := send(s.main, http.MethodPost, resetConfirmPath, tokenBody(t, expired, "yet another passphrase"), "", nil)
	if res.status != http.StatusBadRequest || res.code() != api.CodeTokenExpired {
		t.Errorf("an expired link: %d %s, want 400 token_expired", res.status, res.body)
	}
	res = send(s.main, http.MethodPost, verifyEmailPath, tokenBody(t, "not-a-token", ""), "", nil)
	if res.status != http.StatusBadRequest || res.code() != api.CodeTokenInvalid {
		t.Errorf("a token that is not one: %d %s, want 400 token_invalid", res.status, res.body)
	}
}

// TestResetRequestHidesAccounts checks that asking for a link that resets
// the password of an address of no account answers exactly as for an
// address of an account, and does the same work: it adds one token and one
// job, which emails the link later, so that neither answer waits for the
// SMTP server, and their times do not differ by sending an email.
//
// Verifies: FR-AUTH-03/AC3
func TestResetRequestHidesAccounts(t *testing.T) {
	t.Parallel()
	server := emailtest.Start(t, emailtest.Options{})
	env := smtpEnv(server)
	env["STORYCREEP_SIGNUP"] = "open"
	s, d := readyServer(t, env)
	newAccount(t, s.main, "jo@example.com")
	header := http.Header{"Accept-Language": {"en"}}
	var answers []response
	for _, email := range []string{"jo@example.com", "nobody@example.com"} {
		tokens, jobs := countRows(t, d, "SELECT count(*) FROM email_token"), emailJobs(t, d)
		res := send(s.main, http.MethodPost, passwordResetPath, emailBody(t, email), "", header)
		if res.status != http.StatusAccepted || len(res.body) != 0 {
			t.Fatalf("%s: %d %s, want 202", email, res.status, res.body)
		}
		if n := countRows(t, d, "SELECT count(*) FROM email_token") - tokens; n != 1 {
			t.Errorf("%s added %d tokens, want 1", email, n)
		}
		if n := emailJobs(t, d) - jobs; n != 1 {
			t.Errorf("%s enqueued %d jobs, want 1", email, n)
		}
		res.header.Del(requestIDHeader)
		dropCounts(res.header)
		answers = append(answers, res)
	}
	if !maps.EqualFunc(answers[0].header, answers[1].header, slices.Equal) {
		t.Errorf("the headers differ: %v and %v", answers[0].header, answers[1].header)
	}
	if n := len(server.Messages()); n != 0 {
		t.Errorf("the requests sent %d emails themselves", n)
	}
}

// TestPasswordChangeInvalidatesResetLinks checks that a link that resets
// the password stops working once the password changes by another route:
// changing it, or another reset link.
//
// Verifies: FR-AUTH-03/AC4
func TestPasswordChangeInvalidatesResetLinks(t *testing.T) {
	t.Parallel()
	s, _, admin := setUpWithAdmin(t, nil)
	jo := newNamedAccount(t, s.main, "jo@example.com", "Jo")
	stale := takeLink(t, s, admin, resetLinksPath, "jo@example.com")
	res := send(s.main, http.MethodPost, passwordPath, passwordChange(t, testPassword, "an entirely new passphrase"), "",
		jo.header(true))
	if res.status != http.StatusOK {
		t.Fatalf("change the password: %d %s", res.status, res.body)
	}
	res = send(s.main, http.MethodPost, resetConfirmPath, tokenBody(t, stale, "yet another passphrase"), "", nil)
	if res.status != http.StatusBadRequest || res.code() != api.CodeTokenInvalid {
		t.Errorf("a link from before the change: %d %s, want 400 token_invalid", res.status, res.body)
	}

	first := takeLink(t, s, admin, resetLinksPath, "jo@example.com")
	second := takeLink(t, s, admin, resetLinksPath, "jo@example.com")
	if res := send(s.main, http.MethodPost, resetConfirmPath, tokenBody(t, second, "yet another passphrase"), "",
		nil); res.status != http.StatusNoContent {
		t.Fatalf("reset with the second link: %d %s", res.status, res.body)
	}
	res = send(s.main, http.MethodPost, resetConfirmPath, tokenBody(t, first, "and one passphrase more"), "", nil)
	if res.status != http.StatusBadRequest || res.code() != api.CodeTokenInvalid {
		t.Errorf("the first link after the reset: %d %s, want 400 token_invalid", res.status, res.body)
	}
}

// TestWorkspaceAdminSeesNoResetLink checks, on an instance that sends no
// email, that the admin of a workspace W2 who is not an instance admin
// cannot take a link that resets the password of a member of W2, who owns
// W1, and that no answer that the admin gets holds the token of the link
// that an instance admin took for the member.
//
// Verifies: FR-AUTH-03/AC5
func TestWorkspaceAdminSeesNoResetLink(t *testing.T) {
	t.Parallel()
	s, d, instanceAdmin := setUpWithAdmin(t, nil)
	newNamedAccount(t, s.main, "member@example.com", "Member")
	admin := newNamedAccount(t, s.main, "admin2@example.com", "Admin")
	owner := newNamedAccount(t, s.main, "owner2@example.com", "Owner")
	res := send(s.main, http.MethodPost, workspacesPath, jsonBody(t, map[string]string{"name": "W2"}), "",
		owner.header(true))
	if res.status != http.StatusCreated {
		t.Fatalf("create W2: %d %s", res.status, res.body)
	}
	var w2 workspaceBody
	if err := json.Unmarshal(res.body, &w2); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for email, r := range map[string]role.Role{"admin2@example.com": role.Admin, "member@example.com": role.Editor} {
		var id ids.UUID
		if err := d.Admin(t).QueryRow(ctx, `SELECT id FROM "user" WHERE email = $1`, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.workspaces.Grant(ctx, decode(t, ids.Workspace, w2.ID), id, r); err != nil {
			t.Fatal(err)
		}
	}
	// The member owns W1, the personal workspace of the member's first
	// sign-in.
	if n := countRows(t, d, `SELECT count(*) FROM membership m JOIN "user" u ON u.id = m.user_id
		WHERE u.email = 'member@example.com' AND m.role = 'owner'`); n != 1 {
		t.Fatalf("the member owns %d workspaces, want 1", n)
	}

	token := takeLink(t, s, instanceAdmin, resetLinksPath, "member@example.com")
	var answers []response
	for _, r := range []struct {
		method, path string
		body         []byte
	}{
		{http.MethodPost, passwordResetPath, emailBody(t, "member@example.com")},
		{http.MethodPost, resetLinksPath, emailBody(t, "member@example.com")},
		{http.MethodPost, verificationLinksPath, emailBody(t, "member@example.com")},
		{http.MethodGet, sessionPath, nil},
		{http.MethodGet, workspacesPath, nil},
		{http.MethodGet, workspacesPath + "/" + w2.ID, nil},
		{http.MethodGet, instancePath, nil},
	} {
		res := send(s.main, r.method, r.path, r.body, "", admin.header(true))
		answers = append(answers, res)
		switch r.path {
		case passwordResetPath:
			if res.code() != api.CodeEmailNotConfigured {
				t.Errorf("POST %s: %d %s, want 403 email_not_configured", r.path, res.status, res.body)
			}
		case resetLinksPath, verificationLinksPath:
			if res.status != http.StatusForbidden || res.code() != api.CodeInstanceAdminRequired {
				t.Errorf("POST %s: %d %s, want 403 instance_admin_required", r.path, res.status, res.body)
			}
		}
	}
	for _, res := range answers {
		var headers bytes.Buffer
		_ = res.header.Write(&headers)
		if bytes.Contains(res.body, []byte(token)) || bytes.Contains(headers.Bytes(), []byte(token)) {
			t.Errorf("an answer to the workspace's admin holds the reset token: %s", res.body)
		}
	}
	if n := countRows(t, d, `SELECT count(*) FROM email_token t JOIN "user" u ON u.id = t.user_id
		WHERE u.email = 'member@example.com'`); n != 1 {
		t.Errorf("the member has %d links, want the instance admin's one", n)
	}
}

// TestAdminLinksRefused checks the answers of the routes that issue links as
// text: 403 email_configured on an instance that sends email, and 404 for an
// address of no active account.
func TestAdminLinksRefused(t *testing.T) {
	t.Parallel()
	s, _, admin := setUpWithAdmin(t, map[string]string{"STORYCREEP_SMTP_URL": "smtp://smtp.example.com",
		"STORYCREEP_SMTP_FROM": "noreply@example.com"})
	for _, path := range []string{resetLinksPath, verificationLinksPath} {
		res := send(s.main, http.MethodPost, path, emailBody(t, "admin@example.com"), "", admin.header(true))
		if res.status != http.StatusForbidden || res.code() != api.CodeEmailConfigured {
			t.Errorf("POST %s: %d %s, want 403 email_configured", path, res.status, res.body)
		}
	}
	s, _, admin = setUpWithAdmin(t, nil)
	res := send(s.main, http.MethodPost, resetLinksPath, emailBody(t, "nobody@example.com"), "", admin.header(true))
	if res.status != http.StatusNotFound || res.code() != api.CodeNotFound {
		t.Errorf("an address of no account: %d %s, want 404", res.status, res.body)
	}
	res = send(s.main, http.MethodPost, resetLinksPath, emailBody(t, "admin@example.com"), "", admin.header(false))
	if res.status != http.StatusForbidden || res.code() != api.CodeCSRFFailed {
		t.Errorf("without the CSRF token: %d %s, want 403 csrf_failed", res.status, res.body)
	}
}

// TestEmailedReset follows a link that resets a password from the request
// to the email that the instance's job queue sends, and its use.
func TestEmailedReset(t *testing.T) {
	t.Parallel()
	server := emailtest.Start(t, emailtest.Options{})
	env := smtpEnv(server)
	env["STORYCREEP_SIGNUP"] = "open"
	s, d := readyServer(t, env)
	startWorkers(t, s, d)
	newAccount(t, s.main, "jo@example.com")
	server.Wait(t, 1, 30*time.Second)
	res := send(s.main, http.MethodPost, passwordResetPath, emailBody(t, "JO@example.com"), "",
		http.Header{"Accept-Language": {"en"}})
	if res.status != http.StatusAccepted {
		t.Fatalf("POST %s: %d %s", passwordResetPath, res.status, res.body)
	}
	// The jobs run at once, so the verification that registering emailed
	// may arrive after the reset.
	var reset emailtest.Message
	for _, m := range server.Wait(t, 2, 30*time.Second) {
		if strings.Contains(m.Body(t), "/app/reset-password#token=") {
			reset = m
		}
	}
	if reset.Data == nil || reset.Header(t).Get("Subject") != "Reset your Storycreep password" ||
		!strings.Contains(reset.Body(t), "http://localhost:8080/app/reset-password#token=") {
		t.Fatalf("no email holds the reset link: %+v", server.Messages())
	}
	const replacement = "an entirely new passphrase"
	if res := send(s.main, http.MethodPost, resetConfirmPath, tokenBody(t, reset.Token(t), replacement), "",
		nil); res.status != http.StatusNoContent {
		t.Fatalf("reset with the emailed link: %d %s", res.status, res.body)
	}
	if res := send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com", replacement), "",
		nil); res.status != http.StatusOK {
		t.Errorf("sign in with the new password: %d %s", res.status, res.body)
	}
}

// TestRefusedAddressEndsTheJob checks that the job of a link whose email the
// SMTP server refuses for good, as for an address that does not exist, is
// cancelled after its first attempt, rather than attempted again for a day,
// and keeps the error, which does not quote the address.
func TestRefusedAddressEndsTheJob(t *testing.T) {
	t.Parallel()
	server := emailtest.Start(t, emailtest.Options{RefuseRecipients: 550})
	env := smtpEnv(server)
	env["STORYCREEP_SIGNUP"] = "open"
	s, d := readyServer(t, env)
	startWorkers(t, s, d)
	newAccount(t, s.main, "nobody@example.com")
	waitForEmailJobs(t, d, "cancelled")
	var attempt int
	var errs string
	if err := d.Admin(t).QueryRow(context.Background(),
		"SELECT attempt, errors::text FROM river_job WHERE kind = 'email_link'").Scan(&attempt, &errs); err != nil {
		t.Fatal(err)
	}
	if attempt != 1 || !strings.Contains(errs, "refuses the recipient: 550") ||
		strings.Contains(errs, "nobody@example.com") {
		t.Errorf("the job was attempted %d times, with the errors %s; want once, with the refusal", attempt, errs)
	}
}

// TestHostedVerificationRequired checks that in hosted mode, which always
// sends email, a user who has not verified their address gets no personal
// workspace at sign-in, and cannot create a workspace, whatever the request
// holds, until they verify it with the link that registering emailed, which
// gives them the personal workspace.
func TestHostedVerificationRequired(t *testing.T) {
	t.Parallel()
	server := emailtest.Start(t, emailtest.Options{})
	env := smtpEnv(server)
	env["STORYCREEP_MODE"], env["STORYCREEP_PUBLIC_URL"] = "hosted", "https://storycreep.example.com"
	s, d := readyServer(t, env)
	startWorkers(t, s, d)
	jo := newNamedAccount(t, s.main, "jo@example.com", "Jo")
	if n := countRows(t, d, "SELECT count(*) FROM workspace"); n != 0 {
		t.Errorf("an unverified account's sign-in created %d workspaces", n)
	}
	// The refusal comes first, whatever the request holds, even a body that
	// its schema refuses.
	for _, body := range []map[string]string{{"name": "Harbour"}, {}} {
		res := send(s.main, http.MethodPost, workspacesPath, jsonBody(t, body), "", jo.header(true))
		if res.status != http.StatusForbidden || res.code() != api.CodeEmailUnverified {
			t.Errorf("create a workspace with %v before verifying: %d %s, want 403 email_unverified", body,
				res.status, res.body)
		}
	}
	// Another link asked for at once is answered as always, but not emailed,
	// because the first one's email has just been sent.
	if res := send(s.main, http.MethodPost, emailVerificationPath, nil, "", jo.header(true)); res.status !=
		http.StatusAccepted {
		t.Errorf("ask for another link: %d %s, want 202", res.status, res.body)
	}
	waitForEmailJobs(t, d, "completed")
	sent := server.Messages()
	if len(sent) != 1 || !strings.Contains(sent[0].Body(t), "https://storycreep.example.com/app/verify-email#token=") {
		t.Fatalf("sent %d emails, want the one of the registration: %+v", len(sent), sent)
	}
	if res := send(s.main, http.MethodPost, verifyEmailPath, tokenBody(t, sent[0].Token(t), ""), "", nil); res.status !=
		http.StatusNoContent {
		t.Fatalf("verify: %d %s", res.status, res.body)
	}
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'workspace.create'
		AND metadata->>'personal' = 'true'`); n != 1 {
		t.Errorf("the verification created %d personal workspaces, want 1", n)
	}
	res := send(s.main, http.MethodPost, workspacesPath, jsonBody(t, map[string]string{"name": "Harbour"}), "",
		jo.header(true))
	if res.status != http.StatusCreated {
		t.Errorf("create a workspace once verified: %d %s, want 201", res.status, res.body)
	}
}

// TestVerificationOutlivesWorkspaceFailure checks, in hosted mode, that a
// verification whose personal workspace cannot be created is answered as
// made, because the address is verified and the link used, and that the
// account's next sign-in creates the workspace.
func TestVerificationOutlivesWorkspaceFailure(t *testing.T) {
	t.Parallel()
	server := emailtest.Start(t, emailtest.Options{})
	env := smtpEnv(server)
	env["STORYCREEP_MODE"], env["STORYCREEP_PUBLIC_URL"] = "hosted", "https://storycreep.example.com"
	s, d := readyServer(t, env)
	startWorkers(t, s, d)
	newAccount(t, s.main, "jo@example.com")
	sent := server.Wait(t, 1, 30*time.Second)
	ctx := context.Background()
	refuse := `CREATE FUNCTION refuse_workspace() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'no workspace'; END $$;
		CREATE TRIGGER refuse_workspace BEFORE INSERT ON workspace FOR EACH ROW EXECUTE FUNCTION refuse_workspace()`
	if _, err := d.Admin(t).Exec(ctx, refuse); err != nil {
		t.Fatal(err)
	}
	if res := send(s.main, http.MethodPost, verifyEmailPath, tokenBody(t, sent[0].Token(t), ""), "", nil); res.status !=
		http.StatusNoContent {
		t.Fatalf("verify while no workspace can be created: %d %s, want 204", res.status, res.body)
	}
	if n := countRows(t, d, `SELECT count(*) FROM "user" WHERE email_verified_at IS NOT NULL
		AND personal_workspace_created_at IS NULL`); n != 1 {
		t.Errorf("%d verified accounts without a personal workspace, want 1", n)
	}
	if _, err := d.Admin(t).Exec(ctx, "DROP TRIGGER refuse_workspace ON workspace"); err != nil {
		t.Fatal(err)
	}
	signIn(t, s.main, "jo@example.com")
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'workspace.create'
		AND metadata->>'personal' = 'true'`); n != 1 {
		t.Errorf("the sign-in after the verification created %d personal workspaces, want 1", n)
	}
}
