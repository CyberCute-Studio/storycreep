package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/blob/blobtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/logging"
)

// TestMain runs the tests with dbtest.Run, which keeps the libpq environment
// variables out of them, and stops PostgreSQL when they are done, and MinIO,
// if a test started it.
func TestMain(m *testing.M) {
	code := dbtest.Run(m)
	blobtest.Stop()
	os.Exit(code)
}

const testMetricsToken = "test-metrics-token-0123456789"

// testDatabaseURL is a valid STORYCREEP_DATABASE_URL for the tests that
// connect to no database.
const testDatabaseURL = "postgres://storycreep:pw@db:5432/storycreep"

// testSMTPURL and testSMTPFrom are an SMTP server that no test reaches, for
// the configurations of hosted mode, which requires one.
const (
	testSMTPURL  = "smtp://smtp.example.com:587"
	testSMTPFrom = "Storycreep <noreply@example.com>"
)

// testS3URL is a bucket that no test reaches, for the configurations of
// hosted mode, which requires one.
const testS3URL = "s3://test-key:test-secret@s3.example.com/storycreep"

// loadConfig returns the configuration of env, with a public URL and a
// database URL unless env sets them, a directory of the test's own for the
// assets' files unless env names a storage, as an empty value does to leave
// it unset, and, in hosted mode, an SMTP server and a bucket.
func loadConfig(t testing.TB, env map[string]string) *config.Config {
	t.Helper()
	vars := map[string]string{
		"STORYCREEP_PUBLIC_URL":    "http://localhost:8080",
		"STORYCREEP_DATABASE_URL":  testDatabaseURL,
		"STORYCREEP_ASSET_STORAGE": t.TempDir(),
	}
	if env["STORYCREEP_MODE"] == "hosted" {
		vars["STORYCREEP_SMTP_URL"], vars["STORYCREEP_SMTP_FROM"] = testSMTPURL, testSMTPFrom
		vars["STORYCREEP_ASSET_STORAGE"] = testS3URL
	}
	maps.Copy(vars, env)
	cfg, err := config.Load(config.Options{Env: config.MapEnvironment(vars)})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// logBuffer collects a log that goroutines write while a test reads it.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// records returns the records whose message is msg.
func (b *logBuffer) records(t *testing.T, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(b.String()) {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("not a JSON record: %q", line)
		}
		if r["msg"] == msg {
			out = append(out, r)
		}
	}
	return out
}

func newTestLog() (*slog.Logger, *logBuffer) {
	var b logBuffer
	return logging.New(&b, slog.LevelDebug), &b
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func do(h http.Handler, method, path string, header http.Header) response {
	r := httptest.NewRequest(method, path, nil)
	maps.Copy(r.Header, header)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return response{status: w.Code, header: w.Header(), body: w.Body.Bytes()}
}

// code returns the error code of an envelope, or "" if the body is not one.
func (r response) code() api.Code {
	var env struct {
		Error api.ErrorBody `json:"error"`
	}
	if json.Unmarshal(r.body, &env) != nil {
		return ""
	}
	return env.Error.Code
}

// details returns the details of an envelope.
func (r response) details(t *testing.T) map[string]any {
	t.Helper()
	var env struct {
		Error api.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(r.body, &env); err != nil {
		t.Fatalf("not an envelope: %s", r.body)
	}
	return env.Error.Details
}

// TestRoutesDeclareCapabilities walks the routers of both listeners and
// checks that every route they have declares a capability that exists.
//
// Verifies: NFR-SEC-02
func TestRoutesDeclareCapabilities(t *testing.T) {
	s := newServer(loadConfig(t, nil), slog.New(slog.DiscardHandler))
	want := map[string][]string{
		"main": {"DELETE /api/v1/auth/sessions", "DELETE /api/v1/auth/sessions/{session_id}",
			"DELETE /api/v1/projects/{project_id}", "DELETE /api/v1/projects/{project_id}/grants/{membership_id}",
			"DELETE /api/v1/projects/{project_id}/tokens/{token_id}",
			"DELETE /api/v1/workspaces/{workspace_id}/invites/{invite_id}",
			"DELETE /api/v1/workspaces/{workspace_id}/members/{membership_id}",
			"DELETE /api/v1/workspaces/{workspace_id}/tokens/{token_id}", "GET /", "GET /api/v1/auth/session",
			"GET /api/v1/auth/sessions", "GET /api/v1/instance", "GET /api/v1/openapi.json",
			"GET /api/v1/projects/{project_id}", "GET /api/v1/projects/{project_id}/assets/{asset_id}",
			"GET /api/v1/projects/{project_id}/assets/{asset_id}/content",
			"GET /api/v1/projects/{project_id}/assets/{asset_id}/thumbnail", "GET /api/v1/projects/{project_id}/boards",
			"GET /api/v1/projects/{project_id}/boards/{board_id}",
			"GET /api/v1/projects/{project_id}/boards/{board_id}/connections",
			"GET /api/v1/projects/{project_id}/boards/{board_id}/nodes",
			"GET /api/v1/projects/{project_id}/boards/{board_id}/referenced-by",
			"GET /api/v1/projects/{project_id}/component-types",
			"GET /api/v1/projects/{project_id}/component-types/{component_type_id}",
			"GET /api/v1/projects/{project_id}/component-types/{component_type_id}/attributes/{attribute_id}/conversion",
			"GET /api/v1/projects/{project_id}/components", "GET /api/v1/projects/{project_id}/components/{component_id}",
			"GET /api/v1/projects/{project_id}/connections/{connection_id}",
			"GET /api/v1/projects/{project_id}/folders",
			"GET /api/v1/projects/{project_id}/grants",
			"GET /api/v1/projects/{project_id}/nodes/{node_id}",
			"GET /api/v1/projects/{project_id}/nodes/{node_id}/referenced-by",
			"GET /api/v1/projects/{project_id}/tags",
			"GET /api/v1/projects/{project_id}/tokens", "GET /api/v1/projects/{project_id}/trash",
			"GET /api/v1/projects/{project_id}/variables", "GET /api/v1/projects/{project_id}/variables/{variable_id}",
			"GET /api/v1/setup",
			"GET /api/v1/workspaces",
			"GET /api/v1/workspaces/{workspace_id}", "GET /api/v1/workspaces/{workspace_id}/invites",
			"GET /api/v1/workspaces/{workspace_id}/members",
			"GET /api/v1/workspaces/{workspace_id}/members/{membership_id}/grants",
			"GET /api/v1/workspaces/{workspace_id}/projects", "GET /api/v1/workspaces/{workspace_id}/tokens",
			"GET /api/v1/workspaces/{workspace_id}/trash/projects", "GET /api/v1/workspaces/{workspace_id}/usage",
			"GET /app", "GET /app/*", "GET /healthz", "GET /readyz", "GET /version",
			"PATCH /api/v1/projects/{project_id}", "PATCH /api/v1/workspaces/{workspace_id}",
			"PATCH /api/v1/workspaces/{workspace_id}/members/{membership_id}", "POST /api/v1/admin/password-reset-links",
			"POST /api/v1/admin/verification-links", "POST /api/v1/auth/email-verification", "POST /api/v1/auth/login",
			"POST /api/v1/auth/logout", "POST /api/v1/auth/password", "POST /api/v1/auth/password-reset",
			"POST /api/v1/auth/password-reset/confirm", "POST /api/v1/auth/register", "POST /api/v1/auth/verify-email",
			"POST /api/v1/invites/accept", "POST /api/v1/invites/lookup", "POST /api/v1/invites/register",
			"POST /api/v1/projects/{project_id}/archive", "POST /api/v1/projects/{project_id}/assets",
			"POST /api/v1/projects/{project_id}/commands",
			"POST /api/v1/projects/{project_id}/restore",
			"POST /api/v1/projects/{project_id}/tokens", "POST /api/v1/projects/{project_id}/unarchive",
			"POST /api/v1/setup", "POST /api/v1/workspaces", "POST /api/v1/workspaces/{workspace_id}/invites",
			"POST /api/v1/workspaces/{workspace_id}/invites/{invite_id}/resend",
			"POST /api/v1/workspaces/{workspace_id}/projects", "POST /api/v1/workspaces/{workspace_id}/tokens",
			"PUT /api/v1/projects/{project_id}/grants/{membership_id}"},
		"admin": {"GET /metrics"},
	}
	for name, m := range map[string]*api.Mux{"main": s.main, "admin": s.admin} {
		declared := m.Routes()
		var walked []string
		err := m.Walk(func(r api.Route) error {
			walked = append(walked, r.String())
			if c, ok := declared[r]; !ok || !c.Known() {
				t.Errorf("%s: %s declares no capability", name, r)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(walked)
		if !slices.Equal(walked, want[name]) {
			t.Errorf("%s has the routes %q, want %q", name, walked, want[name])
		}
	}
}

// TestSecurityHeaders checks the headers of PRD NFR-SEC-09 on responses of
// every kind, and HSTS exactly when the public URL is https.
//
// Verifies: NFR-SEC-09
func TestSecurityHeaders(t *testing.T) {
	for _, publicURL := range []string{"http://localhost:8080", "https://storycreep.example.com"} {
		s := newServer(loadConfig(t, map[string]string{"STORYCREEP_PUBLIC_URL": publicURL}), slog.New(slog.DiscardHandler))
		s.ready.setPhase(phaseReady)
		for _, path := range []string{"/healthz", "/nowhere", "/api/v1/nowhere"} {
			h := do(s.main, http.MethodGet, path, nil).header
			csp := h.Get("Content-Security-Policy")
			for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
				if !strings.Contains(csp, want) {
					t.Errorf("%s: the CSP %q lacks %q", path, csp, want)
				}
			}
			if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
				t.Errorf("%s: the CSP %q allows inline or evaluated scripts", path, csp)
			}
			for k, v := range map[string]string{
				"X-Content-Type-Options": "nosniff",
				"Referrer-Policy":        "strict-origin-when-cross-origin",
				"X-Frame-Options":        "DENY",
				"Cache-Control":          "no-store",
			} {
				if h.Get(k) != v {
					t.Errorf("%s: %s = %q, want %q", path, k, h.Get(k), v)
				}
			}
			hsts := h.Get("Strict-Transport-Security")
			if https := strings.HasPrefix(publicURL, "https:"); https && hsts != hstsPolicy || !https && hsts != "" {
				t.Errorf("%s with the public URL %s: Strict-Transport-Security = %q", path, publicURL, hsts)
			}
		}
	}
	// Hosted mode requires an https public URL, so it always sends HSTS.
	s := newServer(loadConfig(t, map[string]string{"STORYCREEP_MODE": "hosted",
		"STORYCREEP_PUBLIC_URL": "https://storycreep.example.com"}), slog.New(slog.DiscardHandler))
	if h := do(s.main, http.MethodGet, "/healthz", nil).header; h.Get("Strict-Transport-Security") != hstsPolicy {
		t.Errorf("hosted mode: Strict-Transport-Security = %q", h.Get("Strict-Transport-Security"))
	}
}

// TestNoindexApp checks that every response under /app tells search engines
// not to index it, and that other responses do not.
//
// Verifies: FR-PUB-02/AC4
func TestNoindexApp(t *testing.T) {
	s := newServer(loadConfig(t, nil), slog.New(slog.DiscardHandler))
	for path, want := range map[string]string{
		"/app":              "noindex",
		"/app/":             "noindex",
		"/app/projects/x":   "noindex",
		"/app/index.html?q": "noindex",
		"/healthz":          "",
		"/apps":             "",
		"/application":      "",
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			if got := do(s.main, method, path, nil).header.Get("X-Robots-Tag"); got != want {
				t.Errorf("%s %s: X-Robots-Tag = %q, want %q", method, path, got, want)
			}
		}
	}
}

// TestMetricsEndpoint checks how /metrics answers in each mode: open on the
// admin port in self-hosted mode, unless a token is set, and protected by the
// token in hosted mode, where without a token there are no metrics.
//
// Verifies: FR-ADMIN-05/AC3
func TestMetricsEndpoint(t *testing.T) {
	hosted := map[string]string{"STORYCREEP_MODE": "hosted", "STORYCREEP_PUBLIC_URL": "https://storycreep.example.com"}

	t.Run("hosted without a token", func(t *testing.T) {
		s := newServer(loadConfig(t, hosted), slog.New(slog.DiscardHandler))
		if s.admin != nil || !strings.Contains(s.metricsOff, "STORYCREEP_METRICS_TOKEN is not set") {
			t.Errorf("hosted mode without a token serves metrics (%q)", s.metricsOff)
		}
		if res := do(s.main, http.MethodGet, "/metrics", nil); res.status != http.StatusNotFound {
			t.Errorf("the main port answers /metrics with %d", res.status)
		}
	})

	withToken := maps.Clone(hosted)
	withToken["STORYCREEP_METRICS_TOKEN"] = testMetricsToken
	selfHostedWithToken := map[string]string{"STORYCREEP_METRICS_TOKEN": testMetricsToken}
	for name, env := range map[string]map[string]string{"hosted": withToken, "self-hosted with a token": selfHostedWithToken} {
		t.Run(name, func(t *testing.T) {
			s := newServer(loadConfig(t, env), slog.New(slog.DiscardHandler))
			for _, auth := range []string{"", "Bearer wrong-token-0123456789", "Basic " + testMetricsToken, "Bearer"} {
				res := do(s.admin, http.MethodGet, "/metrics", http.Header{"Authorization": {auth}})
				if res.status != http.StatusUnauthorized || res.code() != api.CodeUnauthenticated ||
					!strings.HasPrefix(res.header.Get("WWW-Authenticate"), "Bearer") {
					t.Errorf("Authorization %q: %d %s", auth, res.status, res.body)
				}
			}
			res := do(s.admin, http.MethodGet, "/metrics", http.Header{"Authorization": {"bearer " + testMetricsToken}})
			if res.status != http.StatusOK || !bytes.Contains(res.body, []byte("go_goroutines")) {
				t.Errorf("the right token: %d %.200s", res.status, res.body)
			}
		})
	}

	t.Run("self-hosted", func(t *testing.T) {
		s := newServer(loadConfig(t, nil), slog.New(slog.DiscardHandler))
		s.ready.setPhase(phaseReady)
		do(s.main, http.MethodGet, "/healthz", nil)
		res := do(s.admin, http.MethodGet, "/metrics", nil)
		if res.status != http.StatusOK {
			t.Fatalf("%d %s", res.status, res.body)
		}
		for _, want := range []string{
			`storycreep_http_requests_total{code="200",method="GET",route="/healthz"} 1`,
			`storycreep_http_request_duration_seconds_count{method="GET",route="/healthz"} 1`,
		} {
			if !bytes.Contains(res.body, []byte(want)) {
				t.Errorf("the metrics lack %s", want)
			}
		}
		if res := do(s.main, http.MethodGet, "/metrics", nil); res.status != http.StatusNotFound {
			t.Errorf("the main port answers /metrics with %d", res.status)
		}
	})

	t.Run("admin port off", func(t *testing.T) {
		s := newServer(loadConfig(t, map[string]string{"STORYCREEP_ADMIN_ADDR": "off"}), slog.New(slog.DiscardHandler))
		if s.admin != nil {
			t.Error("the admin port serves metrics while it is off")
		}
	})
}

// TestMetricsLabels checks that neither unknown paths nor unknown methods
// add series to the metrics.
func TestMetricsLabels(t *testing.T) {
	s := newServer(loadConfig(t, nil), slog.New(slog.DiscardHandler))
	do(s.main, http.MethodGet, "/random/one", nil)
	do(s.main, "BREW", "/random/two", nil)
	body := string(do(s.admin, http.MethodGet, "/metrics", nil).body)
	if strings.Contains(body, "/random") || strings.Contains(body, "BREW") ||
		!strings.Contains(body, `code="404",method="GET",route="unmatched"`) ||
		!strings.Contains(body, `method="other",route="unmatched"`) {
		t.Errorf("the metrics label requests by path or method:\n%s", body)
	}
}

func TestRequestID(t *testing.T) {
	log, logged := newTestLog()
	s := newServer(loadConfig(t, nil), log)
	s.ready.setPhase(phaseReady)

	res := do(s.main, http.MethodGet, "/nowhere", nil)
	id := res.header.Get("X-Request-ID")
	var env struct{ Error api.ErrorBody }
	if err := json.Unmarshal(res.body, &env); err != nil || id == "" || env.Error.RequestID != id {
		t.Errorf("X-Request-ID %q, envelope %s: want the same new ID in both", id, res.body)
	}
	if rs := logged.records(t, "request"); len(rs) != 1 || rs[0]["request_id"] != id || rs[0]["status"] != float64(404) ||
		rs[0]["route"] != "" || rs[0]["path"] != "/nowhere" {
		t.Errorf("the request is not logged with its ID: %v", rs)
	}

	// A proxy's ID is kept, unless it is not one that the server accepts.
	for given, kept := range map[string]bool{
		"0199e2c4-5b7a-7d43-9f0e-3c1d2a8b6e51": true,
		"edge:abc_DEF.1":                       true,
		"two words":                            false,
		strings.Repeat("a", 129):               false,
		`"quoted"`:                             false,
	} {
		got := do(s.main, http.MethodGet, "/healthz", http.Header{"X-Request-Id": {given}}).header.Get("X-Request-ID")
		if (got == given) != kept || got == "" {
			t.Errorf("X-Request-ID %q came back as %q", given, got)
		}
	}
}

func TestPanicIsAnswered(t *testing.T) {
	log, logged := newTestLog()
	s := newServer(loadConfig(t, nil), log)
	// No rate limit counts the route, which would need the database.
	api.Register(s.main, api.Public, huma.Operation{OperationID: "panic", Method: http.MethodGet, Path: "/panic"},
		func(context.Context, *struct{}) (*statusOutput, error) { panic("the handler broke") }, api.Unlimited())
	res := do(s.main, http.MethodGet, "/panic", nil)
	if res.status != http.StatusInternalServerError || res.code() != api.CodeInternal {
		t.Errorf("a panic is answered with %d %s", res.status, res.body)
	}
	if rs := logged.records(t, "a handler panicked"); len(rs) != 1 || rs[0]["panic"] != "the handler broke" ||
		!strings.Contains(rs[0]["stack"].(string), "goroutine") {
		t.Errorf("the panic is not logged with its stack: %v", rs)
	}
	if rs := logged.records(t, "request"); len(rs) != 1 || rs[0]["status"] != float64(500) {
		t.Errorf("the request is not logged as a 500: %v", rs)
	}
}

// TestAbortedResponseIsObserved checks that a request whose handler panics
// after it sent the status, which ends the connection, is still logged and
// counted.
func TestAbortedResponseIsObserved(t *testing.T) {
	log, logged := newTestLog()
	s := newServer(loadConfig(t, nil), log)
	s.main.Handle(api.Public, http.MethodGet, "/half", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("the handler broke halfway")
	}))
	func() {
		defer func() {
			if v := recover(); v != http.ErrAbortHandler {
				t.Errorf("the response ends with the panic %v, want http.ErrAbortHandler", v)
			}
		}()
		do(s.main, http.MethodGet, "/half", nil)
	}()
	if rs := logged.records(t, "request"); len(rs) != 1 || rs[0]["status"] != float64(200) || rs[0]["aborted"] != true ||
		rs[0]["level"] != "WARN" {
		t.Errorf("the aborted request is not logged as one: %v", rs)
	}
	body := string(do(s.admin, http.MethodGet, "/metrics", nil).body)
	if !strings.Contains(body, `storycreep_http_requests_total{code="200",method="GET",route="/half"} 1`) {
		t.Errorf("the aborted request is not counted:\n%s", body)
	}
}

// TestNotReadyWhileStopping checks that a server that is stopping reports
// that it is not ready and refuses the API's requests, each with one
// Retry-After, while /healthz still answers.
func TestNotReadyWhileStopping(t *testing.T) {
	s := newServer(loadConfig(t, nil), slog.New(slog.DiscardHandler))
	s.ready.setPhase(phaseStopping)
	for _, path := range []string{"/readyz", "/api/v1/nowhere"} {
		res := do(s.main, http.MethodGet, path, nil)
		if res.status != http.StatusServiceUnavailable || res.code() != api.CodeNotReady ||
			res.details(t)["reason"] != "stopping" || !slices.Equal(res.header.Values("Retry-After"), []string{retryAfter}) {
			t.Errorf("%s while stopping: %d %s, Retry-After %q", path, res.status, res.body,
				res.header.Values("Retry-After"))
		}
	}
	if res := do(s.main, http.MethodGet, "/healthz", nil); res.status != http.StatusOK {
		t.Errorf("/healthz while stopping: %d %s", res.status, res.body)
	}
}

// TestRefusedRequestsKeepTheirRoute checks that a request that the readiness
// gate refuses, before it is routed, is logged and counted under the route it
// was for, not as one that no route matches.
func TestRefusedRequestsKeepTheirRoute(t *testing.T) {
	log, logged := newTestLog()
	s := newServer(loadConfig(t, nil), log)
	api.Register(s.main, api.Public, huma.Operation{OperationID: "getThing", Method: http.MethodGet,
		Path: "/api/v1/things/{id}"}, func(context.Context, *struct {
		ID string `path:"id"`
	}) (*statusOutput, error) {
		return &statusOutput{Body: statusBody{Status: "ok"}}, nil
	})
	s.ready.setPhase(phaseStarting)
	if res := do(s.main, http.MethodGet, "/api/v1/things/1", nil); res.status != http.StatusServiceUnavailable {
		t.Fatalf("the API answers %d while starting, want 503", res.status)
	}
	if rs := logged.records(t, "request"); len(rs) != 1 || rs[0]["route"] != "/api/v1/things/{id}" {
		t.Errorf("the refused request is not logged with its route: %v", rs)
	}
	body := string(do(s.admin, http.MethodGet, "/metrics", nil).body)
	if !strings.Contains(body, `storycreep_http_requests_total{code="503",method="GET",route="/api/v1/things/{id}"} 1`) {
		t.Errorf("the refused request is not counted under its route:\n%s", body)
	}
}

func TestOperationalEndpoints(t *testing.T) {
	s := newServer(loadConfig(t, nil), slog.New(slog.DiscardHandler))
	if res := do(s.main, http.MethodGet, "/healthz", nil); res.status != http.StatusOK ||
		strings.TrimSpace(string(res.body)) != `{"status":"ok"}` {
		t.Errorf("/healthz: %d %s", res.status, res.body)
	}
	// net/http drops the body of a HEAD response, which a recorder keeps.
	srv := httptest.NewServer(s.main)
	defer srv.Close()
	head, err := http.Head(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	if head.StatusCode != http.StatusOK {
		t.Errorf("HEAD /healthz: %d", head.StatusCode)
	}
	res := do(s.main, http.MethodGet, "/version", nil)
	var v struct {
		Version   string `json:"version"`
		GoVersion string `json:"go_version"`
	}
	if err := json.Unmarshal(res.body, &v); err != nil || res.status != http.StatusOK || v.Version == "" ||
		!strings.HasPrefix(v.GoVersion, "go") {
		t.Errorf("/version: %d %s", res.status, res.body)
	}
	res = do(s.main, http.MethodPost, "/healthz", nil)
	if res.status != http.StatusMethodNotAllowed || res.header.Get("Allow") != "GET, HEAD" {
		t.Errorf("POST /healthz: %d, Allow %q", res.status, res.header.Get("Allow"))
	}
}
