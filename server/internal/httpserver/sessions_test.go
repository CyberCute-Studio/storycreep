package httpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

const (
	loginPath    = "/api/v1/auth/login"
	logoutPath   = "/api/v1/auth/logout"
	sessionPath  = "/api/v1/auth/session"
	sessionsPath = "/api/v1/auth/sessions"
	passwordPath = "/api/v1/auth/password"
)

const testPassword = "correct horse battery staple"

// csrfKey is csrfHeader as a key of an http.Header, which Get finds it by.
var csrfKey = http.CanonicalHeaderKey(csrfHeader)

// openSignup is the configuration of a server that anyone can register on.
var openSignup = map[string]string{"STORYCREEP_SIGNUP": "open"}

// signedIn is a session that signing in started.
type signedIn struct {
	cookie *http.Cookie
	body   signedInBody
}

// header returns the headers of a request made with the session: its
// cookie, and its CSRF token unless csrf is false.
func (si signedIn) header(csrf bool) http.Header {
	h := http.Header{"Cookie": {si.cookie.Name + "=" + si.cookie.Value}}
	if csrf {
		h.Set(csrfHeader, si.body.CSRFToken)
	}
	return h
}

// newAccount registers an account with testPassword on h.
func newAccount(t *testing.T, h http.Handler, email string) {
	t.Helper()
	if res := send(h, http.MethodPost, registerPath, registration(t, email, "Jo", testPassword), "",
		nil); res.status != http.StatusAccepted {
		t.Fatalf("register %s: %d %s", email, res.status, res.body)
	}
}

// credentials returns the JSON body of a sign-in.
func credentials(t *testing.T, email, password string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// sessionCookieOf returns the session cookie that a response sets, or nil.
func sessionCookieOf(t *testing.T, h http.Header) *http.Cookie {
	t.Helper()
	for _, v := range h.Values("Set-Cookie") {
		ck, err := http.ParseSetCookie(v)
		if err != nil {
			t.Fatalf("Set-Cookie %q: %v", v, err)
		}
		if ck.Name == plainCookieName || ck.Name == secureCookieName {
			return ck
		}
	}
	return nil
}

// signedInFrom returns the session that a response of 200 started or gave a
// new secret.
func signedInFrom(t *testing.T, res response) signedIn {
	t.Helper()
	if res.status != http.StatusOK {
		t.Fatalf("%d %s", res.status, res.body)
	}
	si := signedIn{cookie: sessionCookieOf(t, res.header)}
	if si.cookie == nil || si.cookie.Value == "" {
		t.Fatalf("the answer sets no session cookie: %v", res.header)
	}
	if err := json.Unmarshal(res.body, &si.body); err != nil {
		t.Fatal(err)
	}
	return si
}

// signIn signs in to h with email and testPassword.
func signIn(t *testing.T, h http.Handler, email string) signedIn {
	t.Helper()
	return signedInFrom(t, send(h, http.MethodPost, loginPath, credentials(t, email, testPassword), "", nil))
}

// passwordChange returns the JSON body of a password change.
func passwordChange(t *testing.T, current, replacement string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{"current_password": current, "new_password": replacement})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// clears reports whether a response clears the session cookie.
func clears(t *testing.T, h http.Header) bool {
	t.Helper()
	ck := sessionCookieOf(t, h)
	return ck != nil && ck.Value == "" && ck.MaxAge < 0
}

// countRows returns the number that a query counts, as the superuser.
func countRows(t *testing.T, d *dbtest.Database, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := d.Admin(t).QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSessionCookie checks the cookie that signing in sets: HttpOnly,
// SameSite=Lax, for the path / and the host alone, lasting the session's
// lifetime, and __Host-sc_session and Secure exactly when the public URL is
// https, whatever the scheme of the request, as behind a proxy that
// terminates TLS. The session's secret is 32 bytes, and the cookie signs the
// request in.
//
// Verifies: NFR-SEC-04
func TestSessionCookie(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		env    map[string]string
		name   string
		secure bool
	}{
		{map[string]string{"STORYCREEP_PUBLIC_URL": "http://192.168.1.20:8080"}, plainCookieName, false},
		{map[string]string{"STORYCREEP_PUBLIC_URL": "https://storycreep.example.com"}, secureCookieName, true},
		{map[string]string{"STORYCREEP_MODE": "hosted", "STORYCREEP_PUBLIC_URL": "https://storycreep.example.com"},
			secureCookieName, true},
	} {
		env := maps.Clone(c.env)
		env["STORYCREEP_SIGNUP"] = "open"
		if c.env["STORYCREEP_MODE"] == "hosted" {
			delete(env, "STORYCREEP_SIGNUP")
		}
		s, _ := readyServer(t, env)
		newAccount(t, s.main, "jo@example.com")
		si := signIn(t, s.main, "jo@example.com")
		ck := si.cookie
		lifetime := int((90 * 24 * time.Hour) / time.Second)
		if ck.Name != c.name || ck.Secure != c.secure || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode ||
			ck.Path != "/" || ck.Domain != "" || ck.MaxAge < lifetime-60 || ck.MaxAge > lifetime {
			t.Errorf("%v: the cookie is %s", c.env, ck)
		}
		if raw, err := base64.RawURLEncoding.Strict().DecodeString(ck.Value); err != nil || len(raw) != 32 {
			t.Errorf("%v: the cookie's value is not 32 bytes in base64url: %v", c.env, err)
		}
		res := send(s.main, http.MethodGet, sessionPath, nil, "", si.header(false))
		var got signedInBody
		if res.status != http.StatusOK || json.Unmarshal(res.body, &got) != nil || got.UserID != si.body.UserID ||
			got.Session.ID != si.body.Session.ID || !got.Session.Current || got.CSRFToken != si.body.CSRFToken {
			t.Errorf("%v: the session of the cookie: %d %s; want %+v", c.env, res.status, res.body, si.body)
		}
		if !strings.HasPrefix(got.UserID, "usr_") || !strings.HasPrefix(got.Session.ID, "ses_") {
			t.Errorf("%v: the IDs %q and %q are not public IDs", c.env, got.UserID, got.Session.ID)
		}
	}
}

// TestRevocationReachesEveryServer runs two servers on one database, and
// checks that a session that one revokes is refused by the other at the
// very next request, because no server caches whether a session is valid.
//
// Verifies: FR-AUTH-02/AC1
func TestRevocationReachesEveryServer(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	cfg := loadConfig(t, map[string]string{
		"STORYCREEP_DATABASE_URL":       d.AppURL.Reveal(),
		"STORYCREEP_DATABASE_ADMIN_URL": d.AdminURL.Reveal(),
		"STORYCREEP_SIGNUP":             "open",
	})
	first, second := startInstance(t, cfg, Options{AutoMigrate: true}), startInstance(t, cfg, Options{AutoMigrate: true})
	first.waitFor(t, "/readyz", http.StatusOK)
	second.waitFor(t, "/readyz", http.StatusOK)

	do := func(inst *instance, method, path string, body []byte, header http.Header) response {
		t.Helper()
		req, err := http.NewRequest(method, inst.url+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		maps.Copy(req.Header, header)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response{status: res.StatusCode, header: res.Header, body: b}
	}
	if res := do(first, http.MethodPost, registerPath, registration(t, "jo@example.com", "Jo", testPassword),
		nil); res.status != http.StatusAccepted {
		t.Fatalf("register: %d %s", res.status, res.body)
	}
	login := func() signedIn {
		return signedInFrom(t, do(first, http.MethodPost, loginPath, credentials(t, "jo@example.com", testPassword),
			nil))
	}
	revoker, revoked := login(), login()
	for _, inst := range []*instance{first, second} {
		if res := do(inst, http.MethodGet, sessionPath, nil, revoked.header(false)); res.status != http.StatusOK {
			t.Fatalf("before the revocation: %d %s", res.status, res.body)
		}
	}
	if res := do(first, http.MethodDelete, sessionsPath+"/"+revoked.body.Session.ID, nil,
		revoker.header(true)); res.status != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", res.status, res.body)
	}
	for name, inst := range map[string]*instance{"the other server": second, "the server that revoked it": first} {
		if res := do(inst, http.MethodGet, sessionPath, nil, revoked.header(false)); res.status !=
			http.StatusUnauthorized || res.code() != api.CodeUnauthenticated || !clears(t, res.header) {
			t.Errorf("%s: %d %s, Set-Cookie %q", name, res.status, res.body, res.header.Values("Set-Cookie"))
		}
	}
	if res := do(second, http.MethodGet, sessionPath, nil, revoker.header(false)); res.status != http.StatusOK {
		t.Errorf("the session that revoked it: %d %s", res.status, res.body)
	}
}

// TestLogout checks that signing out, which needs the session's CSRF
// token, deletes the session's row and clears the cookie, after which the
// cookie signs nothing in.
//
// Verifies: FR-AUTH-02/AC2
func TestLogout(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, openSignup)
	newAccount(t, s.main, "jo@example.com")
	si := signIn(t, s.main, "jo@example.com")
	other := signIn(t, s.main, "jo@example.com")
	if res := send(s.main, http.MethodPost, logoutPath, nil, "", si.header(false)); res.status !=
		http.StatusForbidden || res.code() != api.CodeCSRFFailed {
		t.Errorf("without the CSRF token: %d %s", res.status, res.body)
	}
	res := send(s.main, http.MethodPost, logoutPath, nil, "", si.header(true))
	if res.status != http.StatusNoContent || len(res.body) != 0 || !clears(t, res.header) {
		t.Fatalf("sign out: %d %s, Set-Cookie %q", res.status, res.body, res.header.Values("Set-Cookie"))
	}
	ck := sessionCookieOf(t, res.header)
	if ck.Path != "/" || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode {
		t.Errorf("the cookie that clears it is %s, which a browser does not match to it", ck)
	}
	if n := countRows(t, d, `SELECT count(*) FROM session`); n != 1 {
		t.Errorf("%d sessions, want only the other one", n)
	}
	if res := send(s.main, http.MethodGet, sessionPath, nil, "", si.header(false)); res.status !=
		http.StatusUnauthorized {
		t.Errorf("after signing out: %d %s", res.status, res.body)
	}
	if res := send(s.main, http.MethodGet, sessionPath, nil, "", other.header(false)); res.status != http.StatusOK {
		t.Errorf("the other session: %d %s", res.status, res.body)
	}
}

// TestPasswordChangeRevokesOtherSessions checks that changing the password,
// which needs the current one, revokes every other session of the account
// and gives the session that changed it a new secret and CSRF token, so that
// its old cookie signs nothing in, and that the new password signs in and
// the old one does not.
//
// Verifies: FR-AUTH-02/AC4, NFR-SEC-04
func TestPasswordChangeRevokesOtherSessions(t *testing.T) {
	t.Parallel()
	s, _ := readyServer(t, openSignup)
	newAccount(t, s.main, "jo@example.com")
	newAccount(t, s.main, "mo@example.com")
	changer := signIn(t, s.main, "jo@example.com")
	others := []signedIn{signIn(t, s.main, "jo@example.com"), signIn(t, s.main, "jo@example.com")}
	mo := signIn(t, s.main, "mo@example.com")
	const replacement = "a brand new passphrase"

	res := send(s.main, http.MethodPost, passwordPath, passwordChange(t, "not the password at all", replacement), "",
		changer.header(true))
	if res.status != http.StatusForbidden || res.code() != api.CodeWrongPassword ||
		!strings.Contains(string(res.body), `"location":"body.current_password"`) {
		t.Errorf("a wrong current password: %d %s", res.status, res.body)
	}
	res = send(s.main, http.MethodPost, passwordPath, passwordChange(t, testPassword, "short"), "",
		changer.header(true))
	if res.status != http.StatusUnprocessableEntity || res.code() != api.CodeWeakPassword ||
		!strings.Contains(string(res.body), `"location":"body.new_password"`) {
		t.Errorf("a weak new password: %d %s", res.status, res.body)
	}

	changed := signedInFrom(t, send(s.main, http.MethodPost, passwordPath, passwordChange(t, testPassword,
		replacement), "", changer.header(true)))
	if changed.cookie.Value == changer.cookie.Value || changed.body.CSRFToken == changer.body.CSRFToken ||
		changed.body.Session.ID != changer.body.Session.ID {
		t.Errorf("the session after the change: %+v, before: %+v", changed.body, changer.body)
	}
	for name, si := range map[string]signedIn{"the old cookie": changer, "another session": others[0],
		"a third session": others[1]} {
		if res := send(s.main, http.MethodGet, sessionPath, nil, "", si.header(false)); res.status !=
			http.StatusUnauthorized || !clears(t, res.header) {
			t.Errorf("%s: %d %s", name, res.status, res.body)
		}
	}
	for name, si := range map[string]signedIn{"the new cookie": changed, "another account's": mo} {
		if res := send(s.main, http.MethodGet, sessionPath, nil, "", si.header(false)); res.status != http.StatusOK {
			t.Errorf("%s: %d %s", name, res.status, res.body)
		}
	}
	if res := send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com", testPassword), "",
		nil); res.status != http.StatusUnauthorized || res.code() != api.CodeInvalidCredentials {
		t.Errorf("the old password: %d %s", res.status, res.body)
	}
	if res := send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com", replacement), "",
		nil); res.status != http.StatusOK {
		t.Errorf("the new password: %d %s", res.status, res.body)
	}
}

// TestCSRF checks that every route that changes something with a session
// refuses a request with the session's cookie but without its CSRF token,
// with another token, or with the token of another session, and a request
// that a browser sends from another site, as a page of another origin does,
// with or without the token, and that it then changes nothing. Routes that
// read need no token, and signing in from another site is refused too.
//
// Verifies: FR-AUTH-02/AC5
func TestCSRF(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, openSignup)
	newAccount(t, s.main, "jo@example.com")
	si := signIn(t, s.main, "jo@example.com")
	other := signIn(t, s.main, "jo@example.com")
	routes := []struct {
		method, path string
		body         []byte
	}{
		{http.MethodPost, logoutPath, nil},
		{http.MethodDelete, sessionsPath, nil},
		{http.MethodDelete, sessionsPath + "/" + other.body.Session.ID, nil},
		{http.MethodPost, passwordPath, passwordChange(t, testPassword, "a brand new passphrase")},
	}
	attempts := []struct {
		name   string
		header http.Header
	}{
		{"without the token", si.header(false)},
		{"with another token", http.Header{"Cookie": si.header(false)["Cookie"], csrfKey: {"x" + si.body.CSRFToken}}},
		{"with another session's token", http.Header{"Cookie": si.header(false)["Cookie"],
			csrfKey: {other.body.CSRFToken}}},
		{"from another origin, without the token", http.Header{"Cookie": si.header(false)["Cookie"],
			"Origin": {"https://evil.example"}, "Sec-Fetch-Site": {"cross-site"}}},
		{"from another site, with the token", http.Header{"Cookie": si.header(false)["Cookie"],
			csrfKey: {si.body.CSRFToken}, "Sec-Fetch-Site": {"cross-site"}}},
		{"from another origin of the site, with the token", http.Header{"Cookie": si.header(false)["Cookie"],
			csrfKey: {si.body.CSRFToken}, "Sec-Fetch-Site": {"same-site"}}},
	}
	for _, r := range routes {
		for _, a := range attempts {
			res := send(s.main, r.method, r.path, r.body, "", a.header)
			if res.status != http.StatusForbidden || res.code() != api.CodeCSRFFailed {
				t.Errorf("%s %s %s: %d %s", r.method, r.path, a.name, res.status, res.body)
			}
		}
	}
	if n := countRows(t, d, `SELECT count(*) FROM session WHERE revoked_at IS NULL`); n != 2 {
		t.Errorf("%d active sessions, want both", n)
	}
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action <> 'auth.login'`); n != 0 {
		t.Errorf("%d entries of other events than sign-ins, want none", n)
	}

	for _, path := range []string{sessionPath, sessionsPath} {
		if res := send(s.main, http.MethodGet, path, nil, "", si.header(false)); res.status != http.StatusOK {
			t.Errorf("GET %s without the token: %d %s", path, res.status, res.body)
		}
	}
	before := countRows(t, d, `SELECT count(*) FROM session`)
	res := send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com", testPassword), "",
		http.Header{"Sec-Fetch-Site": {"cross-site"}})
	if res.status != http.StatusForbidden || res.code() != api.CodeCSRFFailed || sessionCookieOf(t, res.header) != nil {
		t.Errorf("signing in from another site: %d %s", res.status, res.body)
	}
	if n := countRows(t, d, `SELECT count(*) FROM session`); n != before {
		t.Errorf("signing in from another site started a session")
	}
	signedInFrom(t, send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com", testPassword), "",
		http.Header{"Sec-Fetch-Site": {"same-origin"}}))
	if res := send(s.main, http.MethodPost, logoutPath, nil, "", http.Header{"Cookie": si.header(false)["Cookie"],
		csrfKey: {si.body.CSRFToken}, "Sec-Fetch-Site": {"same-origin"}}); res.status != http.StatusNoContent {
		t.Errorf("signing out from the tool's own page: %d %s", res.status, res.body)
	}
}

// TestSessionList checks the list of the account's sessions, in pages, and
// the revocation of one session, of the session of the request, which signs
// it out, and of all the others.
func TestSessionList(t *testing.T) {
	t.Parallel()
	s, _ := readyServer(t, openSignup)
	newAccount(t, s.main, "jo@example.com")
	newAccount(t, s.main, "mo@example.com")
	first := signIn(t, s.main, "jo@example.com")
	second := signIn(t, s.main, "jo@example.com")
	current := signIn(t, s.main, "jo@example.com")
	mo := signIn(t, s.main, "mo@example.com")

	var listed []sessionBody
	path := sessionsPath + "?limit=2"
	for {
		res := send(s.main, http.MethodGet, path, nil, "", http.Header{"Cookie": current.header(false)["Cookie"],
			"User-Agent": {"Lister/2.0"}})
		var page api.Page[sessionBody]
		if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
			t.Fatalf("%s: %d %s", path, res.status, res.body)
		}
		listed = append(listed, page.Items...)
		if page.NextCursor == nil {
			break
		}
		path = sessionsPath + "?limit=2&cursor=" + url.QueryEscape(*page.NextCursor)
	}
	var got []string
	for _, b := range listed {
		got = append(got, b.ID)
		if b.Current != (b.ID == current.body.Session.ID) || b.IP != "192.0.2.1" || b.UserAgent != "" ||
			b.CreatedAt.IsZero() || b.ExpiresAt.Before(b.IdleExpiresAt) || !b.ExpiresAt.After(b.CreatedAt) {
			t.Errorf("the session %+v", b)
		}
	}
	want := []string{current.body.Session.ID, second.body.Session.ID, first.body.Session.ID}
	if !slices.Equal(got, want) {
		t.Errorf("the sessions listed are %v, want %v", got, want)
	}
	if res := send(s.main, http.MethodGet, sessionsPath+"?cursor=x", nil, "", current.header(false)); res.status !=
		http.StatusBadRequest || res.code() != api.CodeInvalidCursor {
		t.Errorf("an invalid cursor: %d %s", res.status, res.body)
	}

	for _, c := range []struct {
		name, id string
		status   int
		code     api.Code
	}{
		{"another account's session", mo.body.Session.ID, http.StatusNotFound, api.CodeNotFound},
		{"a malformed ID", "ses_x", http.StatusBadRequest, api.CodeInvalidID},
		{"an ID of another kind", current.body.UserID, http.StatusBadRequest, api.CodeInvalidID},
	} {
		res := send(s.main, http.MethodDelete, sessionsPath+"/"+c.id, nil, "", current.header(true))
		if res.status != c.status || res.code() != c.code {
			t.Errorf("revoke %s: %d %s", c.name, res.status, res.body)
		}
	}
	res := send(s.main, http.MethodDelete, sessionsPath+"/"+first.body.Session.ID, nil, "", current.header(true))
	if res.status != http.StatusNoContent || sessionCookieOf(t, res.header) != nil {
		t.Errorf("revoke another session: %d %s, Set-Cookie %q", res.status, res.body, res.header.Values("Set-Cookie"))
	}
	res = send(s.main, http.MethodDelete, sessionsPath, nil, "", current.header(true))
	if res.status != http.StatusOK || string(bytes.TrimSpace(res.body)) != `{"revoked":1}` {
		t.Errorf("revoke the other sessions: %d %s", res.status, res.body)
	}
	for name, si := range map[string]signedIn{"the first": first, "the second": second} {
		if res := send(s.main, http.MethodGet, sessionPath, nil, "", si.header(false)); res.status !=
			http.StatusUnauthorized {
			t.Errorf("%s session: %d %s", name, res.status, res.body)
		}
	}
	if res := send(s.main, http.MethodGet, sessionPath, nil, "", mo.header(false)); res.status != http.StatusOK {
		t.Errorf("another account's session: %d %s", res.status, res.body)
	}
	res = send(s.main, http.MethodDelete, sessionsPath+"/"+current.body.Session.ID, nil, "", current.header(true))
	if res.status != http.StatusNoContent || !clears(t, res.header) {
		t.Errorf("revoke the session of the request: %d %s, Set-Cookie %q", res.status, res.body,
			res.header.Values("Set-Cookie"))
	}
	if res := send(s.main, http.MethodGet, sessionPath, nil, "", current.header(false)); res.status !=
		http.StatusUnauthorized {
		t.Errorf("the revoked session of the request: %d %s", res.status, res.body)
	}
}

// TestSessionRequired checks that a route that needs a session answers a
// request without the cookie, or with one that is not a session's, with
// unauthenticated, and clears a cookie that it does not take.
func TestSessionRequired(t *testing.T) {
	t.Parallel()
	s, _ := readyServer(t, nil)
	for _, c := range []struct {
		name   string
		header http.Header
		clears bool
	}{
		{"no cookie", nil, false},
		{"an empty cookie", http.Header{"Cookie": {plainCookieName + "="}}, false},
		{"another server's cookie", http.Header{"Cookie": {secureCookieName + "=" + strings.Repeat("A", 43)}}, false},
		{"a cookie of no session", http.Header{"Cookie": {plainCookieName + "=" + strings.Repeat("A", 43)}}, true},
		{"a malformed cookie", http.Header{"Cookie": {plainCookieName + "=x"}}, true},
	} {
		res := send(s.main, http.MethodGet, sessionPath, nil, "", c.header)
		if res.status != http.StatusUnauthorized || res.code() != api.CodeUnauthenticated ||
			clears(t, res.header) != c.clears {
			t.Errorf("%s: %d %s, Set-Cookie %q", c.name, res.status, res.body, res.header.Values("Set-Cookie"))
		}
	}
}

// TestLoginAnswersAlike checks that a wrong password, an address of no
// account, an address that is not one, and an account that cannot sign in
// are answered alike, so that the answer does not tell whether an address
// has an account, and that none starts a session.
func TestLoginAnswersAlike(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, openSignup)
	newAccount(t, s.main, "jo@example.com")
	newAccount(t, s.main, "mo@example.com")
	if _, err := d.Admin(t).Exec(context.Background(),
		`UPDATE "user" SET status = 'suspended' WHERE email = 'mo@example.com'`); err != nil {
		t.Fatal(err)
	}
	var first *response
	for _, c := range []struct{ email, password string }{
		{"jo@example.com", "not the password at all"},
		{"nobody@example.com", testPassword},
		{"jo\x00@example.com", testPassword},
		{"mo@example.com", testPassword},
	} {
		res := send(s.main, http.MethodPost, loginPath, credentials(t, c.email, c.password), "",
			http.Header{"Accept-Language": {"en"}})
		res.header.Del(requestIDHeader)
		var env struct {
			Error api.ErrorBody `json:"error"`
		}
		if err := json.Unmarshal(res.body, &env); err != nil {
			t.Fatal(err)
		}
		env.Error.RequestID = ""
		if res.status != http.StatusUnauthorized || env.Error.Code != api.CodeInvalidCredentials {
			t.Errorf("%q: %d %s", c.email, res.status, res.body)
		}
		res.body, _ = json.Marshal(env)
		if first == nil {
			first = &res
		} else if !bytes.Equal(res.body, first.body) || !maps.EqualFunc(res.header, first.header, slices.Equal) {
			t.Errorf("%q is answered with %s %v, and a wrong password with %s %v", c.email, res.body, res.header,
				first.body, first.header)
		}
	}
	if n := countRows(t, d, `SELECT count(*) FROM session`); n != 0 {
		t.Errorf("%d sessions, want none", n)
	}
}

// TestSessionAuditEvents checks the audit entries of signing in, failing to,
// signing out, revoking a session, and changing the password, with the
// actor, the subject, the client, and the request's ID, and none with a
// workspace.
//
// Verifies: FR-TEN-07/AC2
func TestSessionAuditEvents(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, openSignup)
	newAccount(t, s.main, "jo@example.com")
	client := http.Header{"User-Agent": {"Auditor/1.0"}, http.CanonicalHeaderKey(requestIDHeader): {"audit-test"}}
	send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com", "not the password at all"), "", client)
	send(s.main, http.MethodPost, loginPath, credentials(t, "nobody@example.com", testPassword), "", client)
	si := signedInFrom(t, send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com", testPassword), "",
		client))
	other := signIn(t, s.main, "jo@example.com")
	third := signIn(t, s.main, "jo@example.com")
	with := func(h http.Header) http.Header {
		out := maps.Clone(client)
		maps.Copy(out, h)
		return out
	}
	send(s.main, http.MethodDelete, sessionsPath+"/"+other.body.Session.ID, nil, "", with(si.header(true)))
	send(s.main, http.MethodPost, passwordPath, passwordChange(t, "not the password at all", "a brand new passphrase"),
		"", with(si.header(true)))
	si = signedInFrom(t, send(s.main, http.MethodPost, passwordPath, passwordChange(t, testPassword,
		"a brand new passphrase"), "", with(si.header(true))))
	send(s.main, http.MethodPost, logoutPath, nil, "", with(si.header(true)))

	rows, err := d.Admin(t).Query(context.Background(), `
		SELECT action, result, coalesce(subject_kind, ''), coalesce(metadata->>'reason', ''),
			actor_user_id IS NOT NULL, metadata ? 'email_hmac', metadata ? 'session_id',
			workspace_id IS NULL AND host(ip) = '192.0.2.1' AND user_agent = 'Auditor/1.0'
				AND request_id = 'audit-test'
		FROM audit_entry WHERE request_id = 'audit-test' OR user_agent = 'Auditor/1.0'`)
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		action, result, subject, reason string
		actor, emailHash, session, ok   bool
	}
	var got []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.action, &e.result, &e.subject, &e.reason, &e.actor, &e.emailHash, &e.session,
			&e.ok); err != nil {
			t.Fatal(err)
		}
		got = append(got, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []entry{
		{"auth.login", "failure", "user", "", false, false, false, true},
		{"auth.login", "failure", "", "", false, true, false, true},
		{"auth.login", "success", "user", "", true, false, true, true},
		{"auth.session_revoke", "success", "session", "revoked", true, false, false, true},
		{"auth.password_change", "failure", "user", "", true, false, false, true},
		{"auth.session_revoke", "success", "session", "password_changed", true, false, false, true},
		{"auth.password_change", "success", "user", "", true, false, false, true},
		{"auth.session_revoke", "success", "session", "sign_out", true, false, false, true},
	}
	// Entries of one transaction may be in any order, because their IDs
	// sort by the millisecond only.
	byText := func(a, b entry) int { return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)) }
	slices.SortFunc(got, byText)
	slices.SortFunc(want, byText)
	if !slices.Equal(got, want) {
		t.Errorf("the audit entries are\n%v\nwant\n%v", got, want)
	}
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'auth.session_revoke' AND subject_id = $1`,
		mustSessionID(t, third)); n != 1 {
		t.Errorf("the third session's revocation is recorded %d times, want once", n)
	}
}

// mustSessionID returns the UUID of a signed-in session's ID.
func mustSessionID(t *testing.T, si signedIn) string {
	t.Helper()
	id, err := ids.Decode(ids.Session, si.body.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

// TestSessionCleanupJob checks that a server that runs the job workers
// deletes the sessions that have ended, with the server's own jobs, and
// keeps the active ones.
func TestSessionCleanupJob(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	admin := d.Admin(t)
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO "user" (id, email, display_name) VALUES ('0192f5c4-0000-7000-8000-000000000001', 'jo@example.com', 'Jo');
		INSERT INTO session (id, user_id, token_hash, expires_at, revoked_at) VALUES
			('0192f5c4-0000-7000-8000-000000000002', '0192f5c4-0000-7000-8000-000000000001', sha256('ended'),
				now() + interval '1 day', now()),
			('0192f5c4-0000-7000-8000-000000000003', '0192f5c4-0000-7000-8000-000000000001', sha256('active'),
				now() + interval '1 day', NULL)`); err != nil {
		t.Fatal(err)
	}
	inst := startInstance(t, dbConfig(t, d), Options{AutoMigrate: true, InlineWorkers: true})
	inst.waitFor(t, "/readyz", http.StatusOK)
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(50 * time.Millisecond) {
		if countRows(t, d, `SELECT count(*) FROM session WHERE revoked_at IS NOT NULL`) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ended session is still there; the log:\n%s", inst.log)
		}
	}
	if n := countRows(t, d, `SELECT count(*) FROM session`); n != 1 {
		t.Errorf("%d sessions, want the active one", n)
	}
}
