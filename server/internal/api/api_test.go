package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/logging"
)

// TestMain runs the tests with dbtest.Run, for the pagination test, which
// runs against PostgreSQL.
func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

const testRequestID = "req-test-1"

// testMux returns a Mux whose requests have the ID testRequestID, and the
// buffer its log is written to.
func testMux(t *testing.T) (*Mux, *bytes.Buffer) {
	t.Helper()
	var logged bytes.Buffer
	m := NewMux(MuxOptions{
		Log: logging.New(&logged, slog.LevelDebug),
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

// TestHandlerErrors checks how errors that handlers return are answered.
func TestHandlerErrors(t *testing.T) {
	m, logged := testMux(t)
	var fail error
	Register(m, Public, huma.Operation{OperationID: "fail", Method: http.MethodGet, Path: "/fail"},
		func(context.Context, *struct{}) (*itemOutput, error) { return nil, fail })

	fail = &Error{Code: CodeNotFound, Message: "No such item.", Details: map[string]any{"item": "x"}}
	res := serve(m, http.MethodGet, "/fail", "", nil)
	if b := res.envelope(t); b.Code != CodeNotFound || b.Message != "No such item." || b.Details["item"] != "x" {
		t.Errorf("an *Error: %s", res.body)
	}

	_, fail = ids.Decode(ids.Node, ids.Encode(ids.Board, ids.New()))
	res = serve(m, http.MethodGet, "/fail", "", nil)
	if b := res.envelope(t); b.Code != CodeInvalidID || b.Details["reason"] != string(ids.WrongPrefix) ||
		!strings.Contains(b.Message, "type board") {
		t.Errorf("an *ids.DecodeError: %s", res.body)
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
	m := NewMux(MuxOptions{Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
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
	want := map[Route]Capability{{http.MethodPost, "/items"}: Public, {http.MethodGet, "/metrics"}: ScrapeMetrics}
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
		case s.message == "" || s.doc == "" || !strings.HasSuffix(s.message, "."):
			t.Errorf("%s needs a message, ending with a full stop, and a doc", s.code)
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
	m := NewMux(MuxOptions{Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
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
