package httpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

const registerPath = "/api/v1/auth/register"

// readyServer returns a ready server with the configuration of env, on a new
// database, and the database.
func readyServer(t *testing.T, env map[string]string) (*server, *dbtest.Database) {
	t.Helper()
	d := dbtest.New(t)
	s := newServer(loadConfig(t, env), slog.New(slog.DiscardHandler))
	s.ready.db.Store(d.Open(t))
	s.ready.setPhase(phaseReady)
	return s, d
}

// send sends a request with body, as JSON unless contentType says
// otherwise, and the header.
func send(h http.Handler, method, path string, body []byte, contentType string, header http.Header) response {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	maps.Copy(r.Header, header)
	if contentType == "" {
		contentType = "application/json"
	}
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return response{status: w.Code, header: w.Header(), body: w.Body.Bytes()}
}

// dropCounts removes from h the RateLimit headers that count the client's
// requests, which change from one request to the next, whatever the request
// holds.
func dropCounts(h http.Header) {
	h.Del(remainingHeader)
	h.Del(resetHeader)
}

// registration returns the JSON body of a registration.
func registration(t *testing.T, email, displayName, password string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{"email": email, "display_name": displayName, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// countAccounts returns how many accounts the database has.
func countAccounts(t *testing.T, d *dbtest.Database) int {
	t.Helper()
	var n int
	if err := d.Admin(t).QueryRow(context.Background(), `SELECT count(*) FROM "user"`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestRegisterHidesExistingAccounts checks, in hosted mode, that registering
// an address that has an account answers exactly as registering a new one
// does, and creates no second account, nor changes the first.
//
// Verifies: FR-AUTH-01/AC1
func TestRegisterHidesExistingAccounts(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, map[string]string{"STORYCREEP_MODE": "hosted",
		"STORYCREEP_PUBLIC_URL": "https://storycreep.example.com"})
	header := http.Header{"Accept-Language": {"en"}}
	created := send(s.main, http.MethodPost, registerPath,
		registration(t, "jo@example.com", "Jo", "correct horse battery staple"), "", header)
	if created.status != http.StatusAccepted || len(created.body) != 0 {
		t.Fatalf("a new address: %d %s", created.status, created.body)
	}
	var before string
	admin := d.Admin(t)
	if err := admin.QueryRow(context.Background(), `SELECT "user"::text FROM "user"`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	again := send(s.main, http.MethodPost, registerPath,
		registration(t, "JO@example.com", "Mallory", "another long password"), "", header)
	if again.status != created.status || !bytes.Equal(again.body, created.body) {
		t.Errorf("an address that has an account: %d %s, and a new one: %d %s", again.status, again.body,
			created.status, created.body)
	}
	for _, h := range []http.Header{created.header, again.header} {
		h.Del(requestIDHeader)
		dropCounts(h)
	}
	if !maps.EqualFunc(created.header, again.header, slices.Equal) {
		t.Errorf("the headers differ: %v and %v", created.header, again.header)
	}
	var after string
	if err := admin.QueryRow(context.Background(), `SELECT "user"::text FROM "user"`).Scan(&after); err != nil {
		t.Fatalf("the accounts after the second registration: %v", err)
	}
	if after != before {
		t.Errorf("the account was %s, and is %s", before, after)
	}
}

// TestRegisterWeakPassword checks that a password that is too short, or one
// of the most common passwords, is refused with an error of the password
// field, which says why, and that no account is created.
//
// Verifies: FR-AUTH-01/AC2
func TestRegisterWeakPassword(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, map[string]string{"STORYCREEP_SIGNUP": "open"})
	for _, c := range []struct {
		password, reason, message string
	}{
		{"elevenchars", "too_short", "The password must have at least 12 characters."},
		{"Unbelievable", "common", "The password is one of the most common passwords."},
	} {
		res := send(s.main, http.MethodPost, registerPath, registration(t, "jo@example.com", "Jo", c.password), "",
			http.Header{"Accept-Language": {"en"}})
		var env struct {
			Error struct {
				Code    api.Code `json:"code"`
				Message string   `json:"message"`
				Details struct {
					Reason string        `json:"reason"`
					Errors []api.Problem `json:"errors"`
				} `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(res.body, &env); err != nil {
			t.Fatalf("%q: %v: %s", c.password, err, res.body)
		}
		e := env.Error
		if res.status != http.StatusUnprocessableEntity || e.Code != api.CodeWeakPassword ||
			e.Message != c.message || e.Details.Reason != c.reason || len(e.Details.Errors) != 1 ||
			e.Details.Errors[0].Location != "body.password" || e.Details.Errors[0].Message == "" {
			t.Errorf("%q: %d %s", c.password, res.status, res.body)
		}
		if strings.Contains(string(res.body), c.password) {
			t.Errorf("%q: the answer repeats the password: %s", c.password, res.body)
		}
	}
	if n := countAccounts(t, d); n != 0 {
		t.Errorf("%d accounts, want none", n)
	}
}

// TestRegisterInvalidFields checks that an email address or a display name
// that is not valid is refused with validation_failed, at its field, and
// that no account is created.
func TestRegisterInvalidFields(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, map[string]string{"STORYCREEP_SIGNUP": "open"})
	for _, c := range []struct {
		email, displayName, location string
	}{
		{"Jo <jo@example.com>", "Jo", "body.email"},
		{"jo", "Jo", "body.email"},
		{"jo@example.com", "  ", "body.display_name"},
		{"jo@example.com", "Jo\u0007", "body.display_name"},
		{"jo@example.com", strings.Repeat("x", 101), "body.display_name"},
	} {
		res := send(s.main, http.MethodPost, registerPath,
			registration(t, c.email, c.displayName, "correct horse battery staple"), "", nil)
		var locations []string
		if errs, ok := res.details(t)["errors"].([]any); ok {
			for _, e := range errs {
				if m, ok := e.(map[string]any); ok {
					locations = append(locations, m["location"].(string))
				}
			}
		}
		if res.status != http.StatusUnprocessableEntity || res.code() != api.CodeValidationFailed ||
			!slices.Contains(locations, c.location) {
			t.Errorf("%q, %q: %d %s; want validation_failed at %s", c.email, c.displayName, res.status, res.body,
				c.location)
		}
	}
	if n := countAccounts(t, d); n != 0 {
		t.Errorf("%d accounts, want none", n)
	}
}

// TestRegisterInternationalAddress checks that the route takes the addresses
// outside ASCII that the account package takes, so that the format check of
// its schema, which runs first, refuses none of them, and that the document
// gives the field the format of such addresses, idn-email.
func TestRegisterInternationalAddress(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, map[string]string{"STORYCREEP_SIGNUP": "open"})
	addresses := []string{"žofie@example.com", "jo@bücher.example", "用户@例子.广告"}
	for _, email := range addresses {
		res := send(s.main, http.MethodPost, registerPath,
			registration(t, email, "Jo", "correct horse battery staple"), "", nil)
		if res.status != http.StatusAccepted {
			t.Errorf("%q: %d %s", email, res.status, res.body)
		}
	}
	if n := countAccounts(t, d); n != len(addresses) {
		t.Errorf("%d accounts, want %d", n, len(addresses))
	}
	schema := s.main.OpenAPI().Components.Schemas.Map()["RegisterBody"]
	if schema == nil || schema.Properties["email"] == nil || schema.Properties["email"].Format != "idn-email" {
		t.Error("the document does not give the email field the format idn-email")
	}
}

// TestRegisterRefusedByPolicy checks that with the closed signup policy,
// self-hosted mode's default, registration answers 403 signup_closed, and
// with the invite policy 403 invite_required, whatever the request holds,
// and creates no account.
//
// Verifies: FR-AUTH-01/AC3
func TestRegisterRefusedByPolicy(t *testing.T) {
	t.Parallel()
	valid := registration(t, "jo@example.com", "Jo", "correct horse battery staple")
	payloads := []struct {
		name, contentType string
		body              []byte
	}{
		{"a valid registration", "", valid},
		{"no body", "", nil},
		{"malformed JSON", "", []byte("{")},
		{"an unknown field", "", []byte(`{"email": "jo@example.com", "role": "owner"}`)},
		{"a weak password", "", registration(t, "jo@example.com", "Jo", "short")},
		{"an invalid address", "", registration(t, "jo", "Jo", "correct horse battery staple")},
		{"another media type", "text/plain", valid},
		{"a body too large", "", bytes.Repeat([]byte(" "), api.DefaultMaxBodyBytes+1)},
	}
	for _, c := range []struct {
		env  map[string]string
		want api.Code
	}{
		{nil, api.CodeSignupClosed},
		{map[string]string{"STORYCREEP_SIGNUP": "closed"}, api.CodeSignupClosed},
		{map[string]string{"STORYCREEP_SIGNUP": "invite"}, api.CodeInviteRequired},
	} {
		s, d := readyServer(t, c.env)
		for _, p := range payloads {
			res := send(s.main, http.MethodPost, registerPath, p.body, p.contentType, nil)
			if res.status != http.StatusForbidden || res.code() != c.want {
				t.Errorf("%v, %s: %d %s; want 403 %s", c.env, p.name, res.status, res.body, c.want)
			}
		}
		if n := countAccounts(t, d); n != 0 {
			t.Errorf("%v: %d accounts, want none", c.env, n)
		}
	}
}

// TestNoEndpointReturnsPasswordHash registers an account, checks that its
// password's hash is an argon2id hash, and then requests every route of both
// listeners, the registration's again included, and checks that no answer
// holds the hash, its salt, or its key, in its body or its headers. Each
// request is made with a new session of the account, and its CSRF token, so
// that a route that needs a signed-in user answers as it does for one, and
// with the metrics token. Each sign-in, and the request made with its
// session, comes from a client address of its own, which a trusted proxy
// forwards, so that the sign-ins, one for each route, do not use up the rate
// limit of one address, however many routes there are. A route that later
// items add is requested too, so an item that adds a route that answers only
// with other credentials, or with a body of its own, requests it so here.
//
// Verifies: FR-AUTH-01/AC4
func TestNoEndpointReturnsPasswordHash(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, map[string]string{"STORYCREEP_SIGNUP": "open",
		"STORYCREEP_METRICS_TOKEN": testMetricsToken, "STORYCREEP_TRUSTED_PROXIES": "192.0.2.1"})
	body := registration(t, "jo@example.com", "Jo", "correct horse battery staple")
	if res := send(s.main, http.MethodPost, registerPath, body, "", nil); res.status != http.StatusAccepted {
		t.Fatalf("register: %d %s", res.status, res.body)
	}
	var hash string
	if err := d.Admin(t).QueryRow(context.Background(), `SELECT password_hash FROM "user"`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		t.Fatalf("the password's hash %q is not an argon2id hash", hash)
	}
	secrets := map[string]string{"the hash": hash, "the salt": parts[4], "the key": parts[5]}
	for _, name := range []string{"salt", "key"} {
		raw, err := base64.RawStdEncoding.DecodeString(secrets["the "+name])
		if err != nil {
			t.Fatal(err)
		}
		secrets["the "+name+" in hexadecimal"] = hex.EncodeToString(raw)
		secrets["the "+name+" in padded base64"] = base64.StdEncoding.EncodeToString(raw)
		secrets["the "+name+" in URL base64"] = base64.RawURLEncoding.EncodeToString(raw)
		secrets["the raw "+name] = string(raw)
	}

	// The bodies of the routes that take another body than a registration's,
	// so that they answer as they do when they succeed, or, for a password
	// change, as they do without changing the hash.
	bodies := map[string][]byte{
		loginPath:    credentials(t, "jo@example.com", "correct horse battery staple"),
		passwordPath: passwordChange(t, "not the password at all", "a brand new passphrase"),
	}
	requested := 0
	for name, m := range map[string]*api.Mux{"main": s.main, "admin": s.admin} {
		var routes []api.Route
		if err := m.Walk(func(r api.Route) error {
			routes = append(routes, r)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, r := range routes {
			path := routeParam.ReplaceAllString(r.Pattern, "1")
			var reqBody []byte
			if r.Method != http.MethodGet {
				reqBody = body
				if b, ok := bodies[r.Pattern]; ok {
					reqBody = b
				}
			}
			client := newClient()
			header := signedInFrom(t, send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com",
				testPassword), "", client)).header(true)
			header.Set("Authorization", "Bearer "+testMetricsToken)
			res := send(m, r.Method, path, reqBody, "", with(header, client))
			requested++
			for what, v := range secrets {
				if bytes.Contains(res.body, []byte(v)) {
					t.Errorf("%s %s answers with %s in its body", name, r, what)
				}
				for k, values := range res.header {
					for _, hv := range values {
						if strings.Contains(hv, v) {
							t.Errorf("%s %s answers with %s in its %s header", name, r, what, k)
						}
					}
				}
			}
		}
	}
	if requested < 2 {
		t.Errorf("only %d routes were requested", requested)
	}
}

// TestRegisterBodyLimits checks that the limits that the OpenAPI document
// gives a registration's fields are the account package's.
func TestRegisterBodyLimits(t *testing.T) {
	s := newServer(loadConfig(t, nil), slog.New(slog.DiscardHandler))
	schema := s.main.OpenAPI().Components.Schemas.Map()["RegisterBody"]
	if schema == nil {
		t.Fatal("the document has no RegisterBody")
	}
	for field, want := range map[string]int{
		"email":        account.MaxEmailLength,
		"display_name": account.MaxDisplayNameLength,
		"password":     account.MaxPasswordLength,
	} {
		p := schema.Properties[field]
		if p == nil || p.MaxLength == nil || *p.MaxLength != want {
			t.Errorf("%s: the document's maxLength is not %d", field, want)
		}
	}
}
