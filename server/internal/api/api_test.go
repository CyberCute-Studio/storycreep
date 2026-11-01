package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/logging"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// TestMain runs the tests with dbtest.Run, for the pagination test, which
// runs against PostgreSQL.
func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

const testRequestID = "req-test-1"

// noLimit is a RateLimiter that counts nothing, for the tests of what a
// rate limit does not change.
type noLimit struct{}

func (noLimit) Take(huma.Context, Bucket) error { return nil }
func (noLimit) Doc(Bucket, Callers) string      { return "" }

// testMux returns a Mux whose requests have the ID testRequestID, and the
// buffer its log is written to. Its RateLimiter counts nothing.
func testMux(t *testing.T) (*Mux, *bytes.Buffer) {
	t.Helper()
	var logged bytes.Buffer
	m := NewMux(MuxOptions{
		Log:       logging.New(&logged, slog.LevelDebug),
		RateLimit: noLimit{},
		Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(logging.WithRequestID(r.Context(), testRequestID)))
			})
		}},
	})
	return m, &logged
}

// response is a response that serve recorded.
type response struct {
	status int
	header http.Header
	body   []byte
}

func serve(m http.Handler, method, path, contentType string, body []byte) response {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	return response{status: w.Code, header: w.Header(), body: w.Body.Bytes()}
}

// envelope decodes the response as the error envelope, strictly, and checks
// that it is one: it has the code's status, a code, a message, and the
// request's ID.
func (r response) envelope(t *testing.T) ErrorBody {
	t.Helper()
	var env struct {
		Error ErrorBody `json:"error"`
	}
	d := json.NewDecoder(bytes.NewReader(r.body))
	d.DisallowUnknownFields()
	if err := d.Decode(&env); err != nil {
		t.Fatalf("the body is not the error envelope: %v\n%s", err, r.body)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	b := env.Error
	if b.Message == "" || b.RequestID != testRequestID {
		t.Errorf("envelope %+v: want a message and the request ID %q", b, testRequestID)
	}
	if b.Code.Status() != r.status {
		t.Errorf("status %d with code %s, whose status is %d", r.status, b.Code, b.Code.Status())
	}
	return b
}

// problems returns details.errors of an envelope.
func problems(t *testing.T, b ErrorBody) []Problem {
	t.Helper()
	raw, err := json.Marshal(b.Details["errors"])
	if err != nil {
		t.Fatal(err)
	}
	var ps []Problem
	if err := json.Unmarshal(raw, &ps); err != nil || len(ps) == 0 {
		t.Fatalf("details.errors = %s, want a list of problems", raw)
	}
	return ps
}

type itemBody struct {
	Name  string `json:"name"`
	Count int    `json:"count,omitempty"`
}

type itemInput struct {
	Body itemBody
}

type itemOutput struct {
	Body itemBody
}

// itemMux returns a Mux with POST /items, which echoes an item, and accepts
// bodies of up to maxBytes.
func itemMux(t *testing.T, maxBytes int64) *Mux {
	t.Helper()
	m, _ := testMux(t)
	Register(m, Public, huma.Operation{
		OperationID:  "createItem",
		Method:       http.MethodPost,
		Path:         "/items",
		MaxBodyBytes: maxBytes,
	}, func(_ context.Context, in *itemInput) (*itemOutput, error) {
		return &itemOutput{Body: in.Body}, nil
	})
	return m
}

// TestStrictDecoding checks that a request body with a field that the
// operation does not have is rejected, naming the field, and so are bodies
// that encoding/json would read loosely.
//
// Verifies: FR-API-01/AC2
func TestStrictDecoding(t *testing.T) {
	m := itemMux(t, 0)

	res := serve(m, http.MethodPost, "/items", "application/json", []byte(`{"name":"a","count":2}`))
	if res.status != http.StatusOK || !strings.Contains(string(res.body), `"name":"a"`) {
		t.Fatalf("a valid body: %d %s", res.status, res.body)
	}

	tests := []struct {
		name, body string
		code       Code
		location   string
		message    string
	}{
		{"unknown field", `{"name":"a","colour":"red"}`, CodeValidationFailed, "body.colour", "unexpected property"},
		// encoding/json matches field names regardless of case.
		{"field in another case", `{"Name":"a"}`, CodeValidationFailed, "body.Name", "unexpected property"},
		{"nested unknown field", `{"name":"a","count":1,"extra":{"x":1}}`, CodeValidationFailed, "body.extra",
			"unexpected property"},
		{"missing field", `{"count":1}`, CodeValidationFailed, "body", "name"},
		{"wrong type", `{"name":1}`, CodeValidationFailed, "body.name", "expected string"},
		{"duplicate key", `{"name":"a","name":"b"}`, CodeInvalidRequest, "body",
			`body.name: the key "name" appears more than once`},
		{"second value", `{"name":"a"} {"name":"b"}`, CodeInvalidRequest, "body", "more after its JSON value"},
		{"invalid UTF-8", "{\"name\":\"\xff\"}", CodeInvalidRequest, "body", "not valid UTF-8"},
		{"syntax error", `{"name":`, CodeInvalidRequest, "body", "not valid JSON"},
		{"missing body", ``, CodeInvalidRequest, "body", "request body is required"},
		{"not an object", `[1]`, CodeValidationFailed, "body", "expected object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := serve(m, http.MethodPost, "/items", "application/json", []byte(tt.body))
			b := res.envelope(t)
			if b.Code != tt.code {
				t.Fatalf("code %s, want %s: %s", b.Code, tt.code, res.body)
			}
			found := false
			for _, p := range problems(t, b) {
				found = found || p.Location == tt.location && strings.Contains(p.Message, tt.message)
			}
			if !found {
				t.Errorf("no problem at %s that says %q: %s", tt.location, tt.message, res.body)
			}
		})
	}

	// A value that the body holds is never repeated, because it may be a
	// password.
	res = serve(m, http.MethodPost, "/items", "application/json", []byte(`{"name":1,"password":"s3cr3t-val"}`))
	if res.envelope(t).Code != CodeValidationFailed || strings.Contains(string(res.body), "s3cr3t-val") {
		t.Errorf("the answer repeats a value of the body: %s", res.body)
	}

	res = serve(m, http.MethodPost, "/items", "text/plain", []byte(`{"name":"a"}`))
	if b := res.envelope(t); b.Code != CodeUnsupportedMediaType {
		t.Errorf("a text/plain body: code %s, want %s", b.Code, CodeUnsupportedMediaType)
	}
	// A +json type is JSON too, and so is a body without a Content-Type, as
	// docs/error-codes.md says.
	for _, ct := range []string{"application/merge-patch+json", "application/json; charset=utf-8", ""} {
		if res := serve(m, http.MethodPost, "/items", ct, []byte(`{"name":"a"}`)); res.status != http.StatusOK {
			t.Errorf("a body sent as %q: %d %s", ct, res.status, res.body)
		}
	}
}

// TestBodyReadError checks that a body that cannot be read is answered with
// internal_error, and logged as a failure only if the client is still there.
func TestBodyReadError(t *testing.T) {
	for _, gone := range []bool{false, true} {
		m, logged := testMux(t)
		Register(m, Public, huma.Operation{OperationID: "createItem", Method: http.MethodPost, Path: "/items"},
			func(_ context.Context, in *itemInput) (*itemOutput, error) { return &itemOutput{Body: in.Body}, nil })
		ctx, cancel := context.WithCancel(context.Background())
		if gone {
			// net/http ends the request's context when the connection fails.
			cancel()
		}
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/items", iotest.ErrReader(io.ErrUnexpectedEOF))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		cancel()
		res := response{status: w.Code, header: w.Header(), body: w.Body.Bytes()}
		if b := res.envelope(t); b.Code != CodeInternal {
			t.Errorf("client gone %v: code %s, want %s", gone, b.Code, CodeInternal)
		}
		failed := strings.Contains(logged.String(), `"msg":"the request failed"`)
		early := strings.Contains(logged.String(), `"msg":"the request ended early"`)
		if failed == gone || early != gone {
			t.Errorf("client gone %v: the log is\n%s", gone, logged)
		}
	}
}

// TestDepthLimit checks that a body nested deeper than maxJSONDepth is
// rejected before it is decoded.
//
// Verifies: NFR-SEC-07
func TestDepthLimit(t *testing.T) {
	nested := func(depth int) string {
		return `{"name":"a","x":` + strings.Repeat("[", depth-1) + strings.Repeat("]", depth-1) + "}"
	}
	if err := checkJSON([]byte(nested(maxJSONDepth))); err != nil {
		t.Errorf("a body nested %d deep: %v", maxJSONDepth, err)
	}
	err := checkJSON([]byte(nested(maxJSONDepth + 1)))
	if err == nil || !strings.Contains(err.Error(), "nest more than 256 deep") {
		t.Errorf("a body nested %d deep: %v, want an error", maxJSONDepth+1, err)
	}
	res := serve(itemMux(t, 0), http.MethodPost, "/items", "application/json", []byte(nested(maxJSONDepth+1)))
	if b := res.envelope(t); b.Code != CodeInvalidRequest {
		t.Errorf("code %s, want %s", b.Code, CodeInvalidRequest)
	}
}

// TestBodyLimit checks that a body larger than the route's limit is rejected
// with the limit, and that one of exactly the limit is not, by default and
// with a route's own limit.
//
// Verifies: NFR-SEC-07
func TestBodyLimit(t *testing.T) {
	for _, tt := range []struct {
		maxBodyBytes, limit int64
	}{
		{64, 64},
		{0, DefaultMaxBodyBytes},
	} {
		m := itemMux(t, tt.maxBodyBytes)
		// body returns a valid body of n bytes.
		body := func(n int64) []byte {
			b := []byte(`{"name":"`)
			b = append(b, bytes.Repeat([]byte("a"), int(n)-len(b)-2)...)
			return append(b, `"}`...)
		}
		if res := serve(m, http.MethodPost, "/items", "application/json", body(tt.limit)); res.status != http.StatusOK {
			t.Errorf("limit %d: a body of the limit: %d %s", tt.limit, res.status, res.body)
		}
		res := serve(m, http.MethodPost, "/items", "application/json", body(tt.limit+1))
		b := res.envelope(t)
		if b.Code != CodeRequestTooLarge || b.Details["max_bytes"] != float64(tt.limit) {
			t.Errorf("limit %d: a body over the limit: code %s, details %v", tt.limit, b.Code, b.Details)
		}
	}
}

// TestUnreadBodyDeadline checks that a request body that the route does not
// read must still arrive in time, because net/http waits for it before it
// answers, and that a body that is read in time leaves the request as it was.
func TestUnreadBodyDeadline(t *testing.T) {
	const deadline = 100 * time.Millisecond
	srv := httptest.NewServer(limitBodyRead(deadline)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/read" {
			// Reading the body ends the deadline, so the handler may take
			// longer than it, with its context intact.
			if b, err := io.ReadAll(r.Body); err != nil || string(b) != "0123456789" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			time.Sleep(3 * deadline)
			if r.Context().Err() != nil {
				w.WriteHeader(http.StatusConflict)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	defer srv.Close()

	send := func(path, length, body string) *http.Response {
		t.Helper()
		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: test\r\nContent-Length: %s\r\n\r\n%s", path, length, body)
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		res, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("POST %s: no answer: %v", path, err)
		}
		res.Body.Close()
		return res
	}
	// Ten of the hundred bytes that the request says its body has. The rest
	// cannot be told from the next request, so the connection is closed.
	start := time.Now()
	if res := send("/ignore", "100", "0123456789"); res.StatusCode != http.StatusNoContent || !res.Close ||
		time.Since(start) > 10*time.Second {
		t.Errorf("an unread body that does not arrive: %d, close %v, after %v", res.StatusCode, res.Close,
			time.Since(start))
	}
	if res := send("/read", "10", "0123456789"); res.StatusCode != http.StatusNoContent || res.Close {
		t.Errorf("a body read in time: %d, close %v", res.StatusCode, res.Close)
	}
}

// TestHandlerErrors checks how errors that handlers return are answered.
func TestHandlerErrors(t *testing.T) {
	m, logged := testMux(t)
	var fail error
	Register(m, Public, huma.Operation{OperationID: "fail", Method: http.MethodGet, Path: "/fail"},
		func(context.Context, *struct{}) (*itemOutput, error) { return nil, fail })

	fail = &Error{Code: CodeNotReady, Message: i18n.Message{Key: i18n.K("error.not_ready.stopping")},
		Details: map[string]any{"reason": "stopping"}}
	res := serve(m, http.MethodGet, "/fail", "", nil)
	if b := res.envelope(t); b.Code != CodeNotReady || b.Message != "The server is stopping." ||
		b.Details["reason"] != "stopping" {
		t.Errorf("an *Error: %s", res.body)
	}
	if got := fail.Error(); got != "not_ready: The server is stopping." {
		t.Errorf("Error() = %q", got)
	}

	_, fail = ids.Decode(ids.Node, ids.Encode(ids.Board, ids.New()))
	res = serve(m, http.MethodGet, "/fail", "", nil)
	if b := res.envelope(t); b.Code != CodeInvalidID || b.Details["reason"] != string(ids.WrongPrefix) ||
		b.Message != "The identifier's prefix bd_ is for type board, not node." {
		t.Errorf("an *ids.DecodeError: %s", res.body)
	}
	// Every reason has a message of its own, with its arguments filled in.
	seen := map[string]bool{}
	for _, id := range []string{"x", "zz_1", "nd_I", "nd_1", "nd_ZZZZZZZZZZZZZZZZZZZZZZZZZZ"} {
		_, fail = ids.Decode(ids.Node, id)
		res = serve(m, http.MethodGet, "/fail", "", nil)
		b := res.envelope(t)
		if b.Code != CodeInvalidID || b.Message == "An identifier is invalid." || seen[b.Message] ||
			strings.HasPrefix(b.Message, "error.") || strings.ContainsAny(b.Message, "{}") {
			t.Errorf("the identifier %s: %s", id, res.body)
		}
		seen[b.Message] = true
	}
	if !seen["The part of the identifier after its prefix is not 26 characters long."] {
		t.Errorf("no message names the length of an identifier's body: %v", seen)
	}

	fail = errors.New("connect to db:5432: password authentication failed")
	res = serve(m, http.MethodGet, "/fail", "", nil)
	if b := res.envelope(t); b.Code != CodeInternal || strings.Contains(string(res.body), "password") {
		t.Errorf("another error: %s", res.body)
	}
	if !strings.Contains(logged.String(), "password authentication failed") ||
		!strings.Contains(logged.String(), `"request_id":"`+testRequestID+`"`) {
		t.Errorf("the error is not logged with the request:\n%s", logged)
	}

	// An envelope that a handler returns for every request, such as a
	// package-level error, is answered with the request's ID but not changed,
	// or concurrent requests would write their IDs into it.
	shared := huma.Error404NotFound("")
	fail = shared
	m.Handle(Public, http.MethodGet, "/shared", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, shared)
	}))
	for _, path := range []string{"/fail", "/shared"} {
		res = serve(m, http.MethodGet, path, "", nil)
		if b := res.envelope(t); b.Code != CodeNotFound || shared.(*ErrorEnvelope).Body.RequestID != "" {
			t.Errorf("%s: %s, and the shared envelope has the request ID %q", path, res.body,
				shared.(*ErrorEnvelope).Body.RequestID)
		}
	}
}

// TestGuard checks that an operation's Guard answers with its check's error,
// in the envelope and with the error's headers, before the operation reads
// the request's body, so whatever the body holds, and that the request goes
// on to the operation when the check returns nil.
func TestGuard(t *testing.T) {
	m, _ := testMux(t)
	var refuse error
	ran := false
	Register(m, Public, huma.Operation{
		OperationID:  "createItem",
		Method:       http.MethodPost,
		Path:         "/items",
		MaxBodyBytes: 64,
		Middlewares:  huma.Middlewares{Guard(func(context.Context) error { return refuse })},
	}, func(_ context.Context, in *itemInput) (*itemOutput, error) {
		ran = true
		return &itemOutput{Body: in.Body}, nil
	})

	refuse = huma.ErrorWithHeaders(NewError(CodeUnauthenticated), http.Header{"WWW-Authenticate": {"Bearer"}})
	for _, c := range []struct{ name, contentType, body string }{
		{"a valid body", "application/json", `{"name":"a"}`},
		{"no body", "application/json", ""},
		{"malformed JSON", "application/json", `{"name":`},
		{"an unknown field", "application/json", `{"colour":"red"}`},
		{"a body too large", "application/json", strings.Repeat(" ", 65)},
		{"another media type", "text/plain", `{"name":"a"}`},
	} {
		res := serve(m, http.MethodPost, "/items", c.contentType, []byte(c.body))
		if b := res.envelope(t); b.Code != CodeUnauthenticated || res.header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: %d %s, WWW-Authenticate %q", c.name, res.status, res.body,
				res.header.Get("WWW-Authenticate"))
		}
	}
	if ran {
		t.Error("the operation ran although its guard refused the request")
	}

	refuse = nil
	if res := serve(m, http.MethodPost, "/items", "application/json", []byte(`{"name":"a"}`)); res.status !=
		http.StatusOK || !ran {
		t.Errorf("a request that the guard lets through: %d %s, the operation ran: %t", res.status, res.body, ran)
	}

	// GuardRequest reads the parameters of the request's path.
	Register(m, Public, huma.Operation{
		OperationID: "getItem",
		Method:      http.MethodGet,
		Path:        "/items/{id}",
		Middlewares: huma.Middlewares{GuardRequest(func(ctx huma.Context) error {
			if ctx.Param("id") == "locked" {
				return NewError(CodeNotFound)
			}
			return nil
		})},
	}, func(context.Context, *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return &struct{}{}, nil
	})
	if res := serve(m, http.MethodGet, "/items/locked", "", nil); res.envelope(t).Code != CodeNotFound {
		t.Errorf("an item that the guard refuses: %d %s", res.status, res.body)
	}
	if res := serve(m, http.MethodGet, "/items/open", "", nil); res.status >= 400 {
		t.Errorf("an item that the guard lets through: %d %s", res.status, res.body)
	}
}

type callerKey struct{}

// TestAuthenticate checks that an operation whose capability needs an
// authenticated caller runs the Mux's Authenticator before its own
// middleware, and before it reads the request's body, so that a refusal
// comes whatever the body holds, and that the operation runs with the
// context that the Authenticator returns. The Authenticator is told the
// scope that the route takes an API token with, or none for a session-only
// route, and the Mux lists the routes that take one. A public operation does
// not run it, and a route that needs a caller cannot be registered where
// nothing finds one, nor can one that needs none take a token.
func TestAuthenticate(t *testing.T) {
	var refuse error
	authenticated := 0
	var told []scope.Scope
	m, _ := testMux(t)
	m.authenticate = func(ctx huma.Context, token scope.Scope) (context.Context, error) {
		authenticated++
		told = append(told, token)
		if refuse != nil {
			return nil, refuse
		}
		return context.WithValue(ctx.Context(), callerKey{}, "jo"), nil
	}
	var order []string
	caller := ""
	Register(m, OwnAccount, huma.Operation{
		OperationID:  "createOwnItem",
		Method:       http.MethodPost,
		Path:         "/own/items",
		MaxBodyBytes: 64,
		Middlewares: huma.Middlewares{func(ctx huma.Context, next func(huma.Context)) {
			order = append(order, "own middleware")
			next(ctx)
		}},
	}, func(ctx context.Context, in *itemInput) (*itemOutput, error) {
		caller, _ = ctx.Value(callerKey{}).(string)
		return &itemOutput{Body: in.Body}, nil
	}, SessionOnly())
	Register(m, Public, huma.Operation{OperationID: "createItem", Method: http.MethodPost, Path: "/items"},
		func(_ context.Context, in *itemInput) (*itemOutput, error) { return &itemOutput{Body: in.Body}, nil })
	Register(m, ReadProject, huma.Operation{OperationID: "getItem", Method: http.MethodGet, Path: "/projects/items"},
		func(context.Context, *struct{}) (*itemOutput, error) { return &itemOutput{}, nil },
		TokenScope(scope.ProjectRead))

	refuse = huma.ErrorWithHeaders(NewError(CodeUnauthenticated), http.Header{"Set-Cookie": {"a=; Max-Age=0"}})
	for _, body := range []string{`{"name":"a"}`, ``, `{"name":`, `{"colour":"red"}`, strings.Repeat(" ", 65)} {
		res := serve(m, http.MethodPost, "/own/items", "application/json", []byte(body))
		if b := res.envelope(t); b.Code != CodeUnauthenticated || res.header.Get("Set-Cookie") != "a=; Max-Age=0" {
			t.Errorf("%q: %d %s, Set-Cookie %q", body, res.status, res.body, res.header.Get("Set-Cookie"))
		}
	}
	if caller != "" || len(order) != 0 {
		t.Errorf("a refused request reached the operation (%q) or its middleware (%v)", caller, order)
	}

	refuse = nil
	if res := serve(m, http.MethodPost, "/own/items", "application/json", []byte(`{"name":"a"}`)); res.status !=
		http.StatusOK || caller != "jo" || len(order) != 1 {
		t.Errorf("an authenticated request: %d %s, caller %q, middleware %v", res.status, res.body, caller, order)
	}
	before := authenticated
	if res := serve(m, http.MethodPost, "/items", "application/json", []byte(`{"name":"a"}`)); res.status !=
		http.StatusOK || authenticated != before {
		t.Errorf("a public operation: %d, and it ran the Authenticator", res.status)
	}
	told = nil
	serve(m, http.MethodGet, "/projects/items", "", nil)
	serve(m, http.MethodPost, "/own/items", "application/json", []byte(`{"name":"a"}`))
	if !slices.Equal(told, []scope.Scope{scope.ProjectRead, ""}) {
		t.Errorf("the Authenticator was told the scopes %q, want project:read and none", told)
	}
	if got, want := m.TokenScopes(), map[Route]scope.Scope{{http.MethodGet, "/projects/items"}: scope.ProjectRead}; !maps.Equal(got, want) {
		t.Errorf("TokenScopes() = %v, want %v", got, want)
	}
	if got, want := m.SessionOnlyRoutes(), map[Route]bool{{http.MethodPost, "/own/items"}: true}; !maps.Equal(got, want) {
		t.Errorf("SessionOnlyRoutes() = %v, want %v", got, want)
	}

	for name, register := range map[string]func(){
		"an operation without an Authenticator": func() {
			Register(NewMux(MuxOptions{RateLimit: noLimit{}}), OwnAccount, huma.Operation{OperationID: "x",
				Method: http.MethodGet, Path: "/x"}, func(context.Context, *struct{}) (*itemOutput, error) {
				return nil, nil
			}, SessionOnly())
		},
		"an operation that says neither whether it takes a token nor that it is session-only": func() {
			Register(m, OwnAccount, huma.Operation{OperationID: "w", Method: http.MethodGet, Path: "/w"},
				func(context.Context, *struct{}) (*itemOutput, error) { return nil, nil })
		},
		"a session-only operation that takes a token": func() {
			Register(m, ReadProject, huma.Operation{OperationID: "v", Method: http.MethodGet, Path: "/v"},
				func(context.Context, *struct{}) (*itemOutput, error) { return nil, nil },
				TokenScope(scope.ProjectRead), SessionOnly())
		},
		"a public operation that is session-only": func() {
			Register(m, Public, huma.Operation{OperationID: "u", Method: http.MethodGet, Path: "/u"},
				func(context.Context, *struct{}) (*itemOutput, error) { return nil, nil }, SessionOnly())
		},
		"a handler": func() { m.Handle(OwnAccount, http.MethodGet, "/own", http.NotFoundHandler()) },
		"a public operation that takes a token": func() {
			Register(m, Public, huma.Operation{OperationID: "y", Method: http.MethodGet, Path: "/y"},
				func(context.Context, *struct{}) (*itemOutput, error) { return nil, nil },
				TokenScope(scope.ProjectRead))
		},
		"an operation that takes a token with an unknown scope": func() {
			Register(m, ReadProject, huma.Operation{OperationID: "z", Method: http.MethodGet, Path: "/z"},
				func(context.Context, *struct{}) (*itemOutput, error) { return nil, nil }, TokenScope("project:all"))
		},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("registering %s did not panic", name)
				}
			}()
			register()
		}()
	}
}

// recordingLimiter is a RateLimiter that records the bucket of each request
// that it counts, with the caller that the Authenticator found, and, if
// order is set, its turn in order, and refuses every request while refuse is
// set.
type recordingLimiter struct {
	taken  []string
	order  *[]string
	refuse error
}

func (l *recordingLimiter) Take(ctx huma.Context, b Bucket) error {
	caller, _ := ctx.Context().Value(callerKey{}).(string)
	l.taken = append(l.taken, string(b)+" "+caller)
	if l.order != nil {
		*l.order = append(*l.order, "limit")
	}
	if l.refuse != nil {
		return l.refuse
	}
	ctx.SetHeader(RateLimitRemainingHeader, "7")
	return nil
}

func (l *recordingLimiter) Doc(b Bucket, c Callers) string {
	return fmt.Sprintf(" Counted as %s for %d.", b, c)
}

// TestRateLimit checks that every operation counts in the bucket that its
// declaration says, or that its method does, once the Authenticator and the
// operation's own middleware have let its request through, before the
// operation runs; that the bucket's limit is in its description and its
// headers in its responses; that only a public route outside /api/ may
// count in none; and that no route that takes an API token counts in the
// auth bucket.
func TestRateLimit(t *testing.T) {
	var order []string
	limiter := &recordingLimiter{order: &order}
	m := NewMux(MuxOptions{RateLimit: limiter, Authenticate: func(ctx huma.Context, _ scope.Scope) (context.Context,
		error) {
		order = append(order, "authenticate")
		return context.WithValue(ctx.Context(), callerKey{}, "jo"), nil
	}})
	ran := 0
	handler := func(context.Context, *struct{}) (*itemOutput, error) {
		ran++
		order = append(order, "operation")
		return &itemOutput{}, nil
	}
	own := huma.Middlewares{func(ctx huma.Context, next func(huma.Context)) {
		order = append(order, "own middleware")
		next(ctx)
	}}
	Register(m, Public, huma.Operation{OperationID: "getThing", Method: http.MethodGet, Path: "/api/things",
		Description: "Reads a thing.", Middlewares: own}, handler)
	Register(m, OwnAccount, huma.Operation{OperationID: "putThing", Method: http.MethodPut, Path: "/api/things",
		Middlewares: own}, handler, SessionOnly())
	Register(m, Public, huma.Operation{OperationID: "login", Method: http.MethodPost, Path: "/api/login"}, handler,
		RateLimit(BucketAuth))
	Register(m, Public, huma.Operation{OperationID: "probe", Method: http.MethodGet, Path: "/probe"}, handler,
		Unlimited())
	// Middleware that may keep a request waiting runs once the limit has
	// counted it.
	waits := func(ctx huma.Context, next func(huma.Context)) {
		order = append(order, "after the limit")
		next(ctx)
	}
	Register(m, OwnAccount, huma.Operation{OperationID: "postThing", Method: http.MethodPost, Path: "/api/things",
		Middlewares: own}, handler, SessionOnly(), AfterLimit(waits))

	want := map[Route]Bucket{
		{http.MethodGet, "/api/things"}:  BucketRead,
		{http.MethodPut, "/api/things"}:  BucketWrite,
		{http.MethodPost, "/api/things"}: BucketWrite,
		{http.MethodPost, "/api/login"}:  BucketAuth,
	}
	if got := m.Buckets(); !maps.Equal(got, want) {
		t.Errorf("Buckets() = %v, want %v", got, want)
	}
	for _, c := range []struct{ method, path, taken string }{
		{http.MethodGet, "/api/things", "read "},
		{http.MethodHead, "/api/things", "read "},
		{http.MethodPut, "/api/things", "write jo"},
		{http.MethodPost, "/api/login", "auth "},
	} {
		limiter.taken, order = nil, nil
		res := serve(m, c.method, c.path, "", nil)
		if res.status != http.StatusOK || !slices.Equal(limiter.taken, []string{c.taken}) ||
			res.header.Get(RateLimitRemainingHeader) != "7" {
			t.Errorf("%s %s: %d, counted %q, want %q, RateLimit-Remaining %q", c.method, c.path, res.status,
				limiter.taken, c.taken, res.header.Get(RateLimitRemainingHeader))
		}
	}
	for _, c := range []struct {
		method string
		want   []string
	}{
		{http.MethodGet, []string{"own middleware", "limit", "operation"}},
		{http.MethodPut, []string{"authenticate", "own middleware", "limit", "operation"}},
		{http.MethodPost, []string{"authenticate", "own middleware", "limit", "after the limit", "operation"}},
	} {
		order = nil
		serve(m, c.method, "/api/things", "", nil)
		if !slices.Equal(order, c.want) {
			t.Errorf("%s /api/things ran %q, want %q", c.method, order, c.want)
		}
	}
	limiter.taken = nil
	if res := serve(m, http.MethodGet, "/probe", "", nil); res.status != http.StatusOK || len(limiter.taken) != 0 {
		t.Errorf("the unlimited route: %d, counted %q", res.status, limiter.taken)
	}

	limiter.refuse = huma.ErrorWithHeaders(NewError(CodeRateLimited), http.Header{RetryAfterHeader: {"9"}})
	ran = 0
	for _, path := range []string{"/api/things", "/api/login"} {
		method := http.MethodGet
		if path == "/api/login" {
			method = http.MethodPost
		}
		res := serve(m, method, path, "", nil)
		if res.status != http.StatusTooManyRequests || res.header.Get(RetryAfterHeader) != "9" {
			t.Errorf("%s %s over its limit: %d %s %v", method, path, res.status, res.body, res.header)
		}
	}
	if ran != 0 {
		t.Errorf("%d requests over the limit reached their operation", ran)
	}
	order = nil
	if res := serve(m, http.MethodPost, "/api/things", "", nil); res.status != http.StatusTooManyRequests ||
		slices.Contains(order, "after the limit") {
		t.Errorf("a request over the limit: %d, ran %q; want 429 before the middleware after the limit", res.status,
			order)
	}

	doc := m.OpenAPI()
	get := doc.Paths["/api/things"].Get
	if get.Description != "Reads a thing. Counted as read for 0." {
		t.Errorf("the description is %q", get.Description)
	}
	if d := doc.Paths["/api/things"].Put.Description; d != "Counted as write for 1." {
		t.Errorf("the description of an operation without one is %q", d)
	}
	for _, name := range []string{RateLimitLimitHeader, RateLimitRemainingHeader, RateLimitResetHeader} {
		if h := get.Responses["200"].Headers[name]; h == nil || h.Schema.Type != huma.TypeInteger {
			t.Errorf("the 200 response does not document %s", name)
		}
	}
	if d := doc.Paths["/probe"].Get; len(d.Responses["200"].Headers) != 0 || d.Description != "" {
		t.Errorf("the unlimited operation documents %v, %q", d.Responses["200"].Headers, d.Description)
	}

	for name, register := range map[string]func(){
		"an operation of a Mux without a RateLimiter": func() {
			Register(NewMux(MuxOptions{}), Public, huma.Operation{OperationID: "a", Method: http.MethodGet,
				Path: "/a"}, handler)
		},
		"an unlimited operation of the API": func() {
			Register(m, Public, huma.Operation{OperationID: "b", Method: http.MethodGet, Path: "/api/b"}, handler,
				Unlimited())
		},
		"an unlimited operation that needs credentials": func() {
			Register(m, OwnAccount, huma.Operation{OperationID: "c", Method: http.MethodGet, Path: "/c"}, handler,
				Unlimited(), SessionOnly())
		},
		"an unlimited operation in a bucket": func() {
			Register(m, Public, huma.Operation{OperationID: "d", Method: http.MethodGet, Path: "/d"}, handler,
				Unlimited(), RateLimit(BucketRead))
		},
		"an operation in an unknown bucket": func() {
			Register(m, Public, huma.Operation{OperationID: "e", Method: http.MethodGet, Path: "/api/e"}, handler,
				RateLimit("stream"))
		},
		// A token's requests count against its owner too, so none counts in
		// the bucket of the requests that revoke it.
		"an operation that takes a token in the auth bucket": func() {
			Register(m, ReadProject, huma.Operation{OperationID: "f", Method: http.MethodGet, Path: "/api/f"}, handler,
				TokenScope(scope.ProjectRead), RateLimit(BucketAuth))
		},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("registering %s did not panic", name)
				}
			}()
			register()
		}()
	}
}

// TestRoutingErrors checks the answers to a path no route matches and to a
// method no route at the path takes.
func TestRoutingErrors(t *testing.T) {
	m := itemMux(t, 0)
	res := serve(m, http.MethodGet, "/nowhere", "", nil)
	if b := res.envelope(t); b.Code != CodeNotFound {
		t.Errorf("an unknown path: code %s", b.Code)
	}
	// chi sends a method it does not route, such as BREW, to the
	// method-not-allowed handler whatever the path.
	for _, method := range []string{http.MethodDelete, "BREW"} {
		res = serve(m, method, "/items", "", nil)
		if b := res.envelope(t); b.Code != CodeMethodNotAllowed || res.header.Get("Allow") != "POST" {
			t.Errorf("%s /items: code %s, Allow %q", method, b.Code, res.header.Get("Allow"))
		}
		res = serve(m, method, "/nowhere", "", nil)
		if b := res.envelope(t); b.Code != CodeNotFound || res.header.Get("Allow") != "" {
			t.Errorf("%s /nowhere: code %s, Allow %q", method, b.Code, res.header.Get("Allow"))
		}
	}
}

// TestRoutePattern checks that a request has the pattern of the route that
// answers it, or would have if middleware had not answered it first.
func TestRoutePattern(t *testing.T) {
	var refuse bool
	got := map[string]string{}
	m := NewMux(MuxOptions{RateLimit: noLimit{}, Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if refuse {
				WriteError(w, r, NewError(CodeNotReady))
			} else {
				next.ServeHTTP(w, r)
			}
			got[r.Method+" "+r.URL.Path] = RoutePattern(r)
		})
	}}})
	Register(m, Public, huma.Operation{OperationID: "getThing", Method: http.MethodGet, Path: "/things/{id}"},
		func(context.Context, *struct {
			ID string `path:"id"`
		}) (*itemOutput, error) {
			return &itemOutput{}, nil
		})
	want := map[string]string{
		"GET /things/1":  "/things/{id}",
		"HEAD /things/1": "/things/{id}",
		"POST /things/1": "",
		"GET /nowhere":   "",
	}
	for _, refuse = range []bool{false, true} {
		for req := range want {
			method, path, _ := strings.Cut(req, " ")
			serve(m, method, path, "", nil)
		}
		for req, pattern := range want {
			if got[req] != pattern {
				t.Errorf("refused %v: %s has the route %q, want %q", refuse, req, got[req], pattern)
			}
		}
	}
}

func TestDeclarations(t *testing.T) {
	m := itemMux(t, 0)
	m.Handle(ScrapeMetrics, http.MethodGet, "/metrics", http.NotFoundHandler())
	m.HandlePage("/app/*", http.NotFoundHandler())
	want := map[Route]Capability{
		{http.MethodPost, "/items"}:  Public,
		{http.MethodGet, "/metrics"}: ScrapeMetrics,
		{http.MethodGet, "/app/*"}:   Public,
	}
	got := m.Routes()
	if len(got) != len(want) {
		t.Errorf("Routes() = %v, want %v", got, want)
	}
	for r, c := range want {
		if got[r] != c {
			t.Errorf("Routes()[%s] = %q, want %q", r, got[r], c)
		}
	}
	var walked []Route
	if err := m.Walk(func(r Route) error { walked = append(walked, r); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(walked) != len(want) {
		t.Errorf("Walk visited %v, want %v", walked, want)
	}
	for r := range want {
		if page := r.Pattern == "/app/*"; m.IsPage(r) != page {
			t.Errorf("IsPage(%s) = %t, want %t", r, !page, page)
		}
	}
	if m.IsPage(Route{http.MethodPost, "/app/*"}) {
		t.Error("IsPage is true for a method that the page does not take")
	}

	// Walk finds a route that bypassed the Mux, which declares nothing.
	huma.Get(m.api, "/bypass", func(context.Context, *struct{}) (*itemOutput, error) { return nil, nil })
	found := false
	_ = m.Walk(func(r Route) error {
		if _, ok := m.Routes()[r]; !ok {
			found = found || r == Route{http.MethodGet, "/bypass"}
		}
		return nil
	})
	if !found {
		t.Error("Walk does not find the route registered without a capability")
	}

	for name, register := range map[string]func(){
		"an unknown capability": func() { m.Handle("project.fly", http.MethodGet, "/fly", http.NotFoundHandler()) },
		"no capability":         func() { m.Handle("", http.MethodGet, "/none", http.NotFoundHandler()) },
		"a route twice":         func() { m.Handle(Public, http.MethodGet, "/metrics", http.NotFoundHandler()) },
		// A rate limit counts every route of the API, which only Register
		// attaches.
		"a handler of the API": func() { m.Handle(Public, http.MethodGet, "/api/raw", http.NotFoundHandler()) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("registering %s did not panic", name)
				}
			}()
			register()
		}()
	}
}

func TestCodes(t *testing.T) {
	seen := map[Code]bool{}
	for _, s := range codes {
		switch {
		case seen[s.code]:
			t.Errorf("%s is registered twice", s.code)
		case s.status < 400 || s.status > 599:
			t.Errorf("%s has the status %d, which is not an error", s.code, s.status)
		case s.doc == "" || !strings.HasSuffix(english(s.code, i18n.Message{}), "."):
			t.Errorf("%s needs a message in the catalog, ending with a full stop, and a doc", s.code)
		}
		seen[s.code] = true
	}
	// Every status that huma answers with has a code of that status.
	for status, c := range statusCodes {
		if c.Status() != status {
			t.Errorf("huma's status %d maps to %s, whose status is %d", status, c, c.Status())
		}
	}
}

func TestETag(t *testing.T) {
	a, b := ETag([]byte("one")), ETag([]byte("two"))
	if a == b || a != ETag([]byte("one")) || !strings.HasPrefix(a, `"`) || !strings.HasSuffix(a, `"`) {
		t.Errorf("ETag: %s, %s", a, b)
	}
	for _, tt := range []struct {
		header string
		match  bool
	}{
		{a, true},
		{"W/" + a, true},
		{b + ", " + a, true},
		{" " + b + " ,W/" + a + " ", true},
		{"*", true},
		{b, false},
		{"", false},
		{strings.Trim(a, `"`), false},
	} {
		if got := IfNoneMatch(tt.header, a); got != tt.match {
			t.Errorf("IfNoneMatch(%q, %s) = %v, want %v", tt.header, a, got, tt.match)
		}
	}

	// As in httpserver, every response is no-store unless its operation says
	// otherwise.
	m := NewMux(MuxOptions{RateLimit: noLimit{}, Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})
	}}})
	type input struct {
		IfNoneMatch string `header:"If-None-Match"`
	}
	type output struct {
		ETag         string `header:"ETag"`
		CacheControl string `header:"Cache-Control"`
		Body         itemBody
	}
	Register(m, Public, huma.Operation{OperationID: "getItem", Method: http.MethodGet, Path: "/item"},
		func(_ context.Context, in *input) (*output, error) {
			if IfNoneMatch(in.IfNoneMatch, a) {
				return nil, NotModified(a)
			}
			return &output{ETag: a, CacheControl: CacheRevalidate, Body: itemBody{Name: "one"}}, nil
		})
	r := httptest.NewRequest(http.MethodGet, "/item", nil)
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if cc := w.Header().Values("Cache-Control"); w.Code != http.StatusOK || w.Header().Get("ETag") != a ||
		!slices.Equal(cc, []string{CacheRevalidate}) {
		t.Fatalf("GET: %d, ETag %q, Cache-Control %q", w.Code, w.Header().Get("ETag"), cc)
	}
	// The 304 must not say no-store, or the browser drops the response it
	// revalidated.
	r.Header.Set("If-None-Match", a)
	w = httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if cc := w.Header().Values("Cache-Control"); w.Code != http.StatusNotModified || w.Header().Get("ETag") != a ||
		w.Body.Len() != 0 || !slices.Equal(cc, []string{CacheRevalidate}) {
		t.Errorf("GET with If-None-Match: %d, ETag %q, Cache-Control %q, body %q", w.Code, w.Header().Get("ETag"), cc,
			w.Body)
	}

	// A handler that is not a huma operation answers the same with
	// WriteError.
	m.Handle(Public, http.MethodGet, "/raw", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, NotModified(a))
	}))
	w = httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/raw", nil))
	if cc := w.Header().Values("Cache-Control"); w.Code != http.StatusNotModified || w.Header().Get("ETag") != a ||
		w.Body.Len() != 0 || !slices.Equal(cc, []string{CacheRevalidate}) {
		t.Errorf("WriteError(NotModified): %d, ETag %q, Cache-Control %q, body %q", w.Code, w.Header().Get("ETag"), cc,
			w.Body)
	}
}

// TestNotModifiedResponse checks that the documented 304 has the headers
// that NotModified sends, and no others.
func TestNotModifiedResponse(t *testing.T) {
	var he huma.HeadersError
	if !errors.As(NotModified(`"a"`), &he) {
		t.Fatal("NotModified has no headers")
	}
	var sent, documented []string
	for name := range he.GetHeaders() {
		sent = append(sent, name)
	}
	for name, h := range NotModifiedResponse().Headers {
		documented = append(documented, name)
		if h.Schema == nil || h.Schema.Type != huma.TypeString {
			t.Errorf("the header %s is not documented as a string", name)
		}
	}
	slices.Sort(sent)
	slices.Sort(documented)
	if !slices.Equal(sent, documented) {
		t.Errorf("NotModified sends %q, and NotModifiedResponse documents %q", sent, documented)
	}
}

func TestCursor(t *testing.T) {
	type key struct {
		Name string `json:"n"`
		ID   int    `json:"i"`
	}
	c, err := EncodeCursor(key{"a", 7})
	if err != nil {
		t.Fatal(err)
	}
	if k, err := DecodeCursor[key](c); err != nil || k != (key{"a", 7}) {
		t.Errorf("DecodeCursor(EncodeCursor(k)) = %v, %v", k, err)
	}
	// A time, as a list's sort key often holds, decodes from the cursor it
	// encodes to, whatever its zone and fraction of a second.
	type timeKey struct {
		T time.Time `json:"t"`
	}
	for _, tm := range []time.Time{
		time.Date(2026, 10, 15, 12, 0, 0, 120000000, time.FixedZone("", 2*3600)),
		time.Date(2026, 3, 29, 1, 30, 0, 1, time.Local),
		time.Now(),
	} {
		c, err := EncodeCursor(timeKey{tm})
		if err != nil {
			t.Fatal(err)
		}
		if k, err := DecodeCursor[timeKey](c); err != nil || !k.T.Equal(tm) {
			t.Errorf("DecodeCursor(EncodeCursor(%v)) = %v, %v", tm, k.T, err)
		}
	}
	bad := []string{"not base64!", strings.Repeat("a", 2000)}
	// JSON that encoding/json would decode into a key, but that EncodeCursor
	// never writes: a missing field would be compared as its zero value.
	for _, raw := range []string{
		`{"n":"a","i":7}x`,
		`{"n":"a","i":7,"x":1}`,
		`{"n":1,"i":7}`,
		`null`,
		`{}`,
		`{"n":"a"}`,
		`{"N":"a","I":7}`,
		`{"i":7,"n":"a"}`,
		`{"n": "a", "i": 7}`,
	} {
		bad = append(bad, base64.RawURLEncoding.EncodeToString([]byte(raw)))
	}
	for _, bad := range bad {
		_, err := DecodeCursor[key](bad)
		var apiErr *Error
		if !errors.As(err, &apiErr) || apiErr.Code != CodeInvalidCursor {
			t.Errorf("DecodeCursor(%.20q) = %v, want invalid_cursor", bad, err)
		}
	}

	rows := []int{1, 2, 3, 4}
	page, err := NewPage(PageParams{Limit: 3}, rows, func(r int) key { return key{ID: r} })
	if err != nil || !slices.Equal(page.Items, []int{1, 2, 3}) || page.NextCursor == nil {
		t.Fatalf("NewPage of 4 rows with limit 3 = %+v, %v", page, err)
	}
	if k, _ := DecodeCursor[key](*page.NextCursor); k.ID != 3 {
		t.Errorf("the next cursor holds %v, want the third row's key", k)
	}
	page, _ = NewPage(PageParams{Limit: 3}, rows[:3], func(r int) key { return key{ID: r} })
	if page.NextCursor != nil {
		t.Errorf("the last page has a next cursor")
	}
	page, _ = NewPage(PageParams{}, []int(nil), func(r int) key { return key{ID: r} })
	if b, _ := json.Marshal(page); string(b) != `{"items":[],"next_cursor":null}` {
		t.Errorf("an empty page encodes as %s", b)
	}
}

func TestPageParams(t *testing.T) {
	m, _ := testMux(t)
	type input struct {
		PageParams
	}
	type output struct {
		Body struct {
			Limit int `json:"limit"`
		}
	}
	Register(m, Public, huma.Operation{OperationID: "listThings", Method: http.MethodGet, Path: "/things"},
		func(_ context.Context, in *input) (*output, error) {
			out := &output{}
			out.Body.Limit = in.Fetch() - 1
			return out, nil
		})
	for _, tt := range []struct {
		query string
		limit int
	}{{"", DefaultPageLimit}, {"?limit=1", 1}, {"?limit=200", MaxPageLimit}} {
		res := serve(m, http.MethodGet, "/things"+tt.query, "", nil)
		var body struct{ Limit int }
		if err := json.Unmarshal(res.body, &body); err != nil || body.Limit != tt.limit {
			t.Errorf("%q: %d %s, want the limit %d", tt.query, res.status, res.body, tt.limit)
		}
	}
	for _, query := range []string{"?limit=0", "?limit=201", "?limit=x"} {
		res := serve(m, http.MethodGet, "/things"+query, "", nil)
		if b := res.envelope(t); b.Code != CodeValidationFailed || problems(t, b)[0].Location != "query.limit" {
			t.Errorf("%q: %s", query, res.body)
		}
	}
}
