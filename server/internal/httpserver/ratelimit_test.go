package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

// clients numbers the client addresses that the tests forward.
var clients atomic.Int32

// newClient returns the header of a request that a trusted proxy forwards
// for a client address that no test has used yet, so that the rate limits
// of the addresses that other requests come from count nothing of it.
func newClient() http.Header {
	n := clients.Add(1)
	return http.Header{"X-Forwarded-For": {fmt.Sprintf("10.%d.%d.%d", n>>16&0xff, n>>8&0xff, n&0xff)}}
}

// with returns the headers of h and of more, which win.
func with(h http.Header, more http.Header) http.Header {
	out := maps.Clone(h)
	maps.Copy(out, more)
	return out
}

// send sends a request to the instance, as JSON, with the header.
func (inst *instance) send(t *testing.T, method, path string, body []byte, header http.Header) response {
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

// twoInstances runs two servers on one new database, with the configuration
// of env, behind a proxy on the loopback interface that they trust, and
// returns them and the database.
func twoInstances(t *testing.T, env map[string]string) (*instance, *instance, *dbtest.Database) {
	t.Helper()
	d := dbtest.New(t)
	vars := map[string]string{
		"STORYCREEP_DATABASE_URL":       d.AppURL.Reveal(),
		"STORYCREEP_DATABASE_ADMIN_URL": d.AdminURL.Reveal(),
		"STORYCREEP_TRUSTED_PROXIES":    "127.0.0.1",
	}
	maps.Copy(vars, env)
	cfg := loadConfig(t, vars)
	first, second := startInstance(t, cfg, Options{AutoMigrate: true}), startInstance(t, cfg, Options{AutoMigrate: true})
	first.waitFor(t, "/readyz", http.StatusOK)
	second.waitFor(t, "/readyz", http.StatusOK)
	return first, second, d
}

// headerInt returns the number in a response's header, or -1.
func headerInt(res response, name string) int {
	n, err := strconv.Atoi(res.header.Get(name))
	if err != nil {
		return -1
	}
	return n
}

// TestAuthRateLimit runs two servers on one database, and checks that the
// requests of signing in and registering from one IP address, spread across
// both, share the address's limit of 20 a minute: each answer says how many
// are left, in its RateLimit headers, and the 21st request is answered with
// 429 rate_limited and the seconds to wait, on either server, while another
// address is not limited.
//
// Verifies: FR-API-04/AC3, FR-AUTH-05/AC2, NFR-SEC-06
func TestAuthRateLimit(t *testing.T) {
	t.Parallel()
	first, second, _ := twoInstances(t, openSignup)
	// A registration without fields, which counts, and fails without
	// hashing a password.
	invalid := []byte(`{}`)
	var client http.Header
	for {
		// The 21 requests must fall in one window of a minute.
		client = newClient()
		res := first.send(t, http.MethodPost, registerPath, invalid, client)
		if res.status != http.StatusUnprocessableEntity || headerInt(res, limitHeader) != 20 ||
			headerInt(res, remainingHeader) != 19 {
			t.Fatalf("the first request: %d %s %v", res.status, res.body, res.header)
		}
		reset := headerInt(res, resetHeader)
		if reset < 1 || reset > 60 {
			t.Fatalf("RateLimit-Reset is %d, want 1 to 60", reset)
		}
		if reset >= 20 {
			break
		}
		time.Sleep(time.Duration(reset+1) * time.Second)
	}
	for i := 2; i <= 20; i++ {
		inst, path, body := first, registerPath, invalid
		if i%2 == 0 {
			inst, path, body = second, loginPath, credentials(t, fmt.Sprintf("nobody%d@example.com", i), testPassword)
		}
		res := inst.send(t, http.MethodPost, path, body, client)
		if res.status == http.StatusTooManyRequests || headerInt(res, limitHeader) != 20 ||
			headerInt(res, remainingHeader) != 20-i {
			t.Fatalf("request %d: %d %s %v; want %d left", i, res.status, res.body, res.header, 20-i)
		}
	}
	for _, inst := range []*instance{second, first} {
		res := inst.send(t, http.MethodPost, loginPath, credentials(t, "jo@example.com", testPassword), client)
		reset := headerInt(res, resetHeader)
		if res.status != http.StatusTooManyRequests || res.code() != api.CodeRateLimited ||
			res.details(t)["reason"] != "rate" || res.details(t)["bucket"] != "auth" ||
			headerInt(res, limitHeader) != 20 || headerInt(res, remainingHeader) != 0 || reset < 1 || reset > 60 ||
			headerInt(res, "Retry-After") != reset {
			t.Errorf("over the limit: %d %s %v", res.status, res.body, res.header)
		}
	}
	if res := second.send(t, http.MethodPost, registerPath, invalid, newClient()); res.status !=
		http.StatusUnprocessableEntity || headerInt(res, remainingHeader) != 19 {
		t.Errorf("another address: %d %s %v", res.status, res.body, res.header)
	}
}

// TestLoginLockout runs two servers on one database, and checks that after
// 10 failed sign-ins in a row with an address, spread across both and made
// from as many IP addresses, the 11th is refused with 429 rate_limited and
// the seconds to wait, even with the right password, and with the RateLimit
// headers of its IP address, whose limit it has not used up, that an address
// of no account is locked alike, so that the answer does not tell them apart,
// and that the audit log records each refusal as a failed login.
//
// Verifies: FR-AUTH-05/AC1, FR-AUTH-05/AC2, FR-TEN-07/AC2
func TestLoginLockout(t *testing.T) {
	t.Parallel()
	first, second, d := twoInstances(t, openSignup)
	if res := first.send(t, http.MethodPost, registerPath, registration(t, "jo@example.com", "Jo", testPassword),
		newClient()); res.status != http.StatusAccepted {
		t.Fatalf("register: %d %s", res.status, res.body)
	}
	english := http.Header{"Accept-Language": {"en"}}
	var refusals []response
	for _, email := range []string{"jo@example.com", "nobody@example.com"} {
		for i := range 10 {
			inst := first
			if i%2 == 1 {
				inst = second
			}
			if res := inst.send(t, http.MethodPost, loginPath, credentials(t, email, "not the password at all"),
				newClient()); res.status != http.StatusUnauthorized || res.code() != api.CodeInvalidCredentials {
				t.Fatalf("%s, failure %d: %d %s", email, i+1, res.status, res.body)
			}
		}
		for _, inst := range []*instance{first, second} {
			res := inst.send(t, http.MethodPost, loginPath, credentials(t, email, testPassword),
				with(newClient(), english))
			if retry := headerInt(res, "Retry-After"); res.status != http.StatusTooManyRequests ||
				res.code() != api.CodeRateLimited || res.details(t)["reason"] != "locked" || retry < 1 || retry > 60 ||
				sessionCookieOf(t, res.header) != nil {
				t.Fatalf("%s, the right password after 10 failures: %d %s %v", email, res.status, res.body, res.header)
			}
			// No limit is used up: the RateLimit headers report the IP
			// address's, as on any other answer.
			if headerInt(res, limitHeader) != 20 || headerInt(res, remainingHeader) != 19 {
				t.Errorf("%s, the RateLimit headers of a locked sign-in: %v; want the IP address's 20, with 19 left",
					email, res.header)
			}
			refusals = append(refusals, res)
		}
	}
	var bodies []string
	for _, res := range refusals {
		var env struct {
			Error api.ErrorBody `json:"error"`
		}
		if err := json.Unmarshal(res.body, &env); err != nil {
			t.Fatal(err)
		}
		env.Error.RequestID = ""
		b, _ := json.Marshal(env)
		bodies = append(bodies, string(b))
	}
	const want = `{"error":{"code":"rate_limited","message":"Too many failed sign-ins with this email address. ` +
		`Try again later.","details":{"reason":"locked"},"request_id":""}}`
	for i, b := range bodies {
		if b != want {
			t.Errorf("refusal %d is %s, want %s", i+1, b, want)
		}
	}
	if n := countRows(t, d, `SELECT count(*) FROM session`); n != 0 {
		t.Errorf("%d sessions, want none", n)
	}
	for reason, want := range map[string]int{"invalid_credentials": 20, "locked": 4} {
		if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'auth.login' AND result = 'failure'
			AND metadata->>'reason' = $1`, reason); n != want {
			t.Errorf("%d failed logins recorded for %s, want %d", n, reason, want)
		}
	}
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry a JOIN "user" u ON u.id = a.subject_id
		WHERE a.metadata->>'reason' = 'locked' AND u.email = 'jo@example.com'`); n != 2 {
		t.Errorf("%d refusals recorded for the account, want 2", n)
	}
}

// TestLoginSuccessForgetsFailures checks that a successful sign-in forgets
// the failures before it, so that 9 more failures do not lock the address,
// which 10 would.
//
// Verifies: FR-AUTH-05/AC3
func TestLoginSuccessForgetsFailures(t *testing.T) {
	t.Parallel()
	s, _ := readyServer(t, map[string]string{"STORYCREEP_SIGNUP": "open", "STORYCREEP_TRUSTED_PROXIES": "192.0.2.1"})
	newAccount(t, s.main, "jo@example.com")
	for round := range 2 {
		for i := range 9 {
			if res := send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com", "a wrong password"),
				"", newClient()); res.status != http.StatusUnauthorized {
				t.Fatalf("round %d, failure %d: %d %s", round+1, i+1, res.status, res.body)
			}
		}
		signedInFrom(t, send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com", testPassword), "",
			newClient()))
	}
	if res := send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com", "a wrong password"), "",
		newClient()); res.status != http.StatusUnauthorized {
		t.Errorf("a failure after the success: %d %s", res.status, res.body)
	}
}

// TestRegisterAddressLimit checks that an email address may be registered
// 10 times a minute, in any spelling, whether or not it has an account,
// and that the answers say how much of the IP address's limit is left, not
// the email address's.
//
// Verifies: NFR-SEC-06
func TestRegisterAddressLimit(t *testing.T) {
	t.Parallel()
	s, _ := readyServer(t, openSignup)
	for i := range 10 {
		email := "jo@example.com"
		if i%2 == 1 {
			email = "JO@Example.com"
		}
		res := send(s.main, http.MethodPost, registerPath, registration(t, email, "Jo", testPassword), "", nil)
		if res.status != http.StatusAccepted || headerInt(res, limitHeader) != 20 ||
			headerInt(res, remainingHeader) != 19-i {
			t.Fatalf("registration %d: %d %s %v", i+1, res.status, res.body, res.header)
		}
	}
	res := send(s.main, http.MethodPost, registerPath, registration(t, "jo@example.com", "Jo", testPassword), "",
		nil)
	if res.status != http.StatusTooManyRequests || res.code() != api.CodeRateLimited ||
		res.details(t)["reason"] != "rate" || headerInt(res, limitHeader) != 10 ||
		headerInt(res, remainingHeader) != 0 || headerInt(res, "Retry-After") < 1 {
		t.Errorf("the 11th registration: %d %s %v", res.status, res.body, res.header)
	}
	if res := send(s.main, http.MethodPost, registerPath, registration(t, "mo@example.com", "Mo", testPassword), "",
		nil); res.status != http.StatusAccepted {
		t.Errorf("another address: %d %s", res.status, res.body)
	}
}

// TestPasswordChangeRateLimit checks that an account may try to change its
// password 10 times a minute, from any IP address, and that the answers say
// how much of the account's limit is left.
//
// Verifies: NFR-SEC-06
func TestPasswordChangeRateLimit(t *testing.T) {
	t.Parallel()
	s, _ := readyServer(t, map[string]string{"STORYCREEP_SIGNUP": "open", "STORYCREEP_TRUSTED_PROXIES": "192.0.2.1"})
	newAccount(t, s.main, "jo@example.com")
	si := signIn(t, s.main, "jo@example.com")
	wrong := passwordChange(t, "not the password at all", "a brand new passphrase")
	for i := range 10 {
		res := send(s.main, http.MethodPost, passwordPath, wrong, "", with(si.header(true), newClient()))
		if res.status != http.StatusForbidden || headerInt(res, limitHeader) != 10 ||
			headerInt(res, remainingHeader) != 9-i {
			t.Fatalf("attempt %d: %d %s %v", i+1, res.status, res.body, res.header)
		}
	}
	res := send(s.main, http.MethodPost, passwordPath, passwordChange(t, testPassword, "a brand new passphrase"), "",
		with(si.header(true), newClient()))
	if res.status != http.StatusTooManyRequests || res.code() != api.CodeRateLimited ||
		res.details(t)["bucket"] != "auth" || headerInt(res, "Retry-After") < 1 {
		t.Errorf("the 11th attempt: %d %s %v", res.status, res.body, res.header)
	}
}

// TestClientAddr checks which address a request is taken to come from: the
// connection's, unless a trusted proxy makes it, and then the rightmost
// address of X-Forwarded-For that is not a trusted proxy.
func TestClientAddr(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/8")}
	for _, c := range []struct {
		remote  string
		forward []string
		want    string
	}{
		{"203.0.113.7:4000", nil, "203.0.113.7"},
		{"203.0.113.7:4000", []string{"198.51.100.1"}, "203.0.113.7"},
		{"[::ffff:203.0.113.7]:4000", nil, "203.0.113.7"},
		{"10.0.0.2:4000", nil, "10.0.0.2"},
		{"10.0.0.2:4000", []string{"198.51.100.1"}, "198.51.100.1"},
		{"10.0.0.2:4000", []string{"192.0.2.66, 198.51.100.1, 10.0.0.3"}, "198.51.100.1"},
		{"10.0.0.2:4000", []string{"192.0.2.66", "198.51.100.1 , 10.0.0.3"}, "198.51.100.1"},
		{"10.0.0.2:4000", []string{"10.0.0.4, 10.0.0.3"}, "10.0.0.4"},
		{"10.0.0.2:4000", []string{"198.51.100.1:5000"}, "198.51.100.1"},
		{"10.0.0.2:4000", []string{"[2001:db8::1]:5000"}, "2001:db8::1"},
		{"[fd00::2]:4000", []string{"2001:db8::1"}, "2001:db8::1"},
		{"10.0.0.2:4000", []string{"198.51.100.1, unknown"}, "10.0.0.2"},
		{"10.0.0.2:4000", []string{"unknown, 10.0.0.3"}, "10.0.0.3"},
		{"10.0.0.2:4000", []string{""}, "10.0.0.2"},
		{"pipe", nil, "invalid IP"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		for _, v := range c.forward {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := clientAddr(r, trusted).String(); got != c.want {
			t.Errorf("%s with %q: %s, want %s", c.remote, c.forward, got, c.want)
		}
	}
}

// TestForwardedClientRecorded checks that a session and the audit log
// record the client's address that a trusted proxy forwards, and the
// connection's address when no trusted proxy makes the request.
func TestForwardedClientRecorded(t *testing.T) {
	t.Parallel()
	for trusted, want := range map[string]string{"192.0.2.0/24": "198.51.100.4", "": "192.0.2.1"} {
		s, d := readyServer(t, map[string]string{"STORYCREEP_SIGNUP": "open", "STORYCREEP_TRUSTED_PROXIES": trusted})
		newAccount(t, s.main, "jo@example.com")
		forwarded := http.Header{"X-Forwarded-For": {"203.0.113.9, 198.51.100.4"}}
		si := signedInFrom(t, send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com", testPassword),
			"", forwarded))
		if si.body.Session.IP != want {
			t.Errorf("trusting %q: the session's IP is %q, want %s", trusted, si.body.Session.IP, want)
		}
		if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'auth.login' AND host(ip) = $1`,
			want); n != 1 {
			t.Errorf("trusting %q: %d logins recorded from %s, want 1", trusted, n, want)
		}
	}
}

// TestRateLimitCleanupJob checks that a server that runs the job workers
// deletes the counters whose window has ended, and the sign-in attempts
// that are forgotten, with the server's own jobs, and keeps the others.
func TestRateLimitCleanupJob(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	admin := d.Admin(t)
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO rate_limit (bucket, key, window_start, window_end, hits) VALUES
			('auth', sha256('ended'), now() - interval '2 minutes', now() - interval '1 minute', 3),
			('auth', sha256('current'), now() - interval '1 second', now() + interval '1 hour', 3);
		INSERT INTO login_attempt (key, failures, last_attempt_at) VALUES
			(sha256('forgotten'), 3, now() - interval '25 hours'),
			(sha256('recent'), 3, now() - interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	inst := startInstance(t, dbConfig(t, d), Options{AutoMigrate: true, InlineWorkers: true})
	inst.waitFor(t, "/readyz", http.StatusOK)
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(50 * time.Millisecond) {
		if countRows(t, d, `SELECT count(*) FROM rate_limit`)+countRows(t, d, `SELECT count(*) FROM login_attempt`) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ended rows are still there; the log:\n%s", inst.log)
		}
	}
	if n := countRows(t, d, `SELECT count(*) FROM rate_limit WHERE key = sha256('current')`) +
		countRows(t, d, `SELECT count(*) FROM login_attempt WHERE key = sha256('recent')`); n != 2 {
		t.Errorf("%d of the live rows kept, want both", n)
	}
}
