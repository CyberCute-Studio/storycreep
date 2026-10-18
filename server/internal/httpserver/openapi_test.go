package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/apicontract"
)

var update = flag.Bool("update", false, "rewrite the OpenAPI document and the TypeScript client's types")

var (
	documentPath   = filepath.Join("..", "..", "..", "schema", "api", "v1", "openapi.json")
	typeScriptPath = filepath.Join("..", "..", "..", "web", "shared", "src", "api", "v1.gen.ts")
)

const typeScriptHeader = `// Generated from schema/api/v1/openapi.json. Do not edit; run
// "go test ./internal/httpserver -run TestOpenAPIDocument -update" in server/
// instead.
`

// generated returns the OpenAPI document of the server's routes, and the
// TypeScript types generated from it.
func generated(t *testing.T) (doc, ts []byte) {
	t.Helper()
	s := newServer(loadConfig(t, nil), slog.New(slog.DiscardHandler))
	doc, err := s.main.Document()
	if err != nil {
		t.Fatal(err)
	}
	d, err := apicontract.Load(doc)
	if err != nil {
		t.Fatal(err)
	}
	ts, err = apicontract.TypeScript(d, typeScriptHeader)
	if err != nil {
		t.Fatal(err)
	}
	return doc, ts
}

// TestOpenAPIDocument checks that the committed OpenAPI document, and the
// TypeScript client's types, are the ones generated from the server's
// routes (PRD FR-API-02 AC1). Run it with -update to regenerate them.
//
// Verifies: FR-API-02/AC1
func TestOpenAPIDocument(t *testing.T) {
	doc, ts := generated(t)
	files := map[string][]byte{documentPath: doc, typeScriptPath: ts}
	if *update {
		for path, want := range files {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	for _, path := range slices.Sorted(maps.Keys(files)) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// A checkout made without .gitattributes may have CRLF.
		if got := bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")); !bytes.Equal(got, files[path]) {
			t.Errorf("%s is out of date; run \"go test ./internal/httpserver -run TestOpenAPIDocument -update\" "+
				"in server/", path)
		}
	}
}

// TestOpenAPIDocumentsEveryRoute checks that the document describes every
// route of the API, and nothing else (PRD FR-API-02 AC1). The admin port's
// /metrics, and the pages of the web interface, are not part of the API.
//
// Verifies: FR-API-02/AC1
func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	s := newServer(loadConfig(t, nil), slog.New(slog.DiscardHandler))
	documented := map[api.Route]bool{}
	for path, item := range s.main.OpenAPI().Paths {
		for method := range api.Operations(item) {
			documented[api.Route{Method: method, Pattern: path}] = true
		}
	}
	err := s.main.Walk(func(r api.Route) error {
		if s.main.IsPage(r) {
			return nil
		}
		if !documented[r] {
			t.Errorf("%s is not in the OpenAPI document; register it with api.Register, or add its operation "+
				"with Mux.OpenAPI().AddOperation", r)
		}
		delete(documented, r)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for r := range documented {
		t.Errorf("the OpenAPI document describes %s, which the server does not route", r)
	}
}

// TestOpenAPIDocumentServed checks /api/v1/openapi.json: it answers with the
// document, and its ETag lets a client revalidate its copy. Its requests
// count in the read bucket, as every operation's of the API, so the server
// has a database.
func TestOpenAPIDocumentServed(t *testing.T) {
	t.Parallel()
	s, _ := pendingServer(t, nil)
	want, err := s.main.Document()
	if err != nil {
		t.Fatal(err)
	}
	res := do(s.main, http.MethodGet, openAPIPath, nil)
	if res.status != http.StatusOK || !bytes.Equal(res.body, want) {
		t.Fatalf("%d %.200s", res.status, res.body)
	}
	etag := res.header.Get("ETag")
	for k, v := range map[string]string{
		"Content-Type":  "application/json",
		"ETag":          api.ETag(want),
		"Cache-Control": api.CacheRevalidate,
	} {
		if res.header.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, res.header.Get(k), v)
		}
	}
	res = do(s.main, http.MethodGet, openAPIPath, http.Header{"If-None-Match": {etag}})
	if res.status != http.StatusNotModified || len(res.body) != 0 || res.header.Get("ETag") != etag ||
		res.header.Get("Cache-Control") != api.CacheRevalidate {
		t.Errorf("If-None-Match with the ETag: %d %q, headers %v", res.status, res.body, res.header)
	}
	res = do(s.main, http.MethodGet, openAPIPath, http.Header{"If-None-Match": {`"stale"`}})
	if res.status != http.StatusOK || !bytes.Equal(res.body, want) {
		t.Errorf("If-None-Match with another ETag: %d", res.status)
	}
}

// TestOpenAPIDocumentIsTheSameEverywhere checks that the document does not
// depend on the configuration, so that the committed one is every server's.
func TestOpenAPIDocumentIsTheSameEverywhere(t *testing.T) {
	want, _ := generated(t)
	for name, env := range map[string]map[string]string{
		"hosted": {"STORYCREEP_MODE": "hosted", "STORYCREEP_PUBLIC_URL": "https://storycreep.example.com",
			"STORYCREEP_METRICS_TOKEN": testMetricsToken},
		"admin port off": {"STORYCREEP_ADMIN_ADDR": "off"},
	} {
		s := newServer(loadConfig(t, env), slog.New(slog.DiscardHandler))
		got, err := s.main.Document()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: the OpenAPI document differs from the default configuration's", name)
		}
	}
}

// provocation is a request that a route should refuse.
type provocation struct {
	name        string
	method      string
	path        string
	header      http.Header
	body        []byte
	contentType string
	phase       phase
}

var routeParam = regexp.MustCompile(`\{[^{}]*\}`)

// validValue returns a value that a parameter's schema accepts.
func validValue(s *huma.Schema) string {
	switch {
	case s == nil:
		return "1"
	case len(s.Enum) > 0:
		return fmt.Sprint(s.Enum[0])
	case s.Type == "boolean":
		return "true"
	case s.Type == "integer" || s.Type == "number":
		if s.Minimum != nil {
			return strconv.FormatFloat(*s.Minimum, 'f', -1, 64)
		}
		return "1"
	}
	return "x"
}

// provoke returns requests that the route r should refuse: one with a
// method that its path does not take, and its own request, without
// credentials or a body, while the server starts and once it is ready. For
// an operation, it adds bodies that it rejects, and a value of the wrong
// type for each parameter that is not a string. Each request has a valid
// value for every other parameter, so that it fails for its own reason.
func provoke(r api.Route, methods map[string]bool, op *huma.Operation) []provocation {
	var params []*huma.Param
	if op != nil {
		params = op.Parameters
	}
	// at returns a request of the route, whose parameters have valid values,
	// or the values that invalid gives them.
	at := func(name, method string, ph phase, invalid map[string]string) provocation {
		pr := provocation{name: name, method: method, phase: ph, header: http.Header{}}
		path, query := r.Pattern, url.Values{}
		for _, p := range params {
			v, ok := invalid[p.In+" "+p.Name]
			if !ok {
				v = validValue(p.Schema)
			}
			switch p.In {
			case "path":
				path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(v))
			case "query":
				query.Set(p.Name, v)
			case "header":
				pr.header.Set(p.Name, v)
			}
		}
		// A route that is not an operation documents no parameters.
		pr.path = routeParam.ReplaceAllString(path, "1")
		if len(query) > 0 {
			pr.path += "?" + query.Encode()
		}
		return pr
	}

	var out []provocation
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if !methods[m] {
			out = append(out, at("method "+m, m, phaseReady, nil))
			break
		}
	}
	out = append(out, at("while starting", r.Method, phaseStarting, nil))
	// /readyz pings the database once the server is ready, and this test has
	// none; its answer while starting is checked instead.
	if r.Pattern != "/readyz" {
		out = append(out, at("without credentials or a body", r.Method, phaseReady, nil))
	}
	if op != nil && op.RequestBody != nil {
		bodies := []struct {
			name, contentType string
			body              []byte
		}{
			{"malformed body", "application/json", []byte("{")},
			{"unknown field", "application/json", []byte(`{"zz_unknown_field": 1}`)},
			{"unsupported media type", "text/plain", []byte("x")},
		}
		if op.MaxBodyBytes > 0 {
			// Register adds a byte to the limit, so this is a byte too many.
			bodies = append(bodies, struct {
				name, contentType string
				body              []byte
			}{"body too large", "application/json", bytes.Repeat([]byte(" "), int(op.MaxBodyBytes))})
		}
		for _, b := range bodies {
			pr := at(b.name, r.Method, phaseReady, nil)
			pr.body, pr.contentType = b.body, b.contentType
			out = append(out, pr)
		}
	}
	for _, p := range params {
		if p.Schema == nil || !slices.Contains([]string{"integer", "number", "boolean"}, p.Schema.Type) {
			continue
		}
		out = append(out, at(fmt.Sprintf("%s parameter %s not of type %s", p.In, p.Name, p.Schema.Type),
			r.Method, phaseReady, map[string]string{p.In + " " + p.Name: "x"}))
	}
	return out
}

// documentedError returns the schema that op documents for an error with
// status: its own, its range's, or the default response's.
func documentedError(op *huma.Operation, status int) (*huma.Schema, error) {
	s := strconv.Itoa(status)
	for _, key := range []string{s, s[:1] + "XX", "default"} {
		r := op.Responses[key]
		if r == nil {
			continue
		}
		mt := r.Content["application/json"]
		if mt == nil || mt.Schema == nil {
			return nil, fmt.Errorf("the response %s documents no application/json body", key)
		}
		return mt.Schema, nil
	}
	return nil, fmt.Errorf("documents no response for %d", status)
}

// TestErrorEnvelopeContract provokes errors from every route of both
// listeners, and checks that every answer with a 4xx or 5xx status is the
// error envelope that the OpenAPI document describes for the operation, with
// a registered code of that status, and the request's ID (PRD FR-API-04
// AC1). A route that is not in the document, as /metrics, is checked against
// the envelope's schema. Each route must fail at least once; a new kind of
// route that none of the provocations makes fail needs one of its own.
//
// An operation with a body and parameters, which the server does not have
// yet, joins the routes, so that the provocations for them are tested too.
//
// Verifies: FR-API-04/AC1
func TestErrorEnvelopeContract(t *testing.T) {
	s := newServer(loadConfig(t, map[string]string{"STORYCREEP_METRICS_TOKEN": testMetricsToken}),
		slog.New(slog.DiscardHandler))
	type contractInput struct {
		ID    int `path:"id"`
		Limit int `query:"limit" minimum:"1" maximum:"200" required:"true"`
		Body  struct {
			Name string `json:"name"`
		}
	}
	api.Register(s.main, api.Public, huma.Operation{OperationID: "createContractThing", Method: http.MethodPost,
		Path: "/api/v1/contract-things/{id}"}, func(context.Context, *contractInput) (*statusOutput, error) {
		return &statusOutput{Body: statusBody{Status: "ok"}}, nil
	})

	doc := s.main.OpenAPI()
	registry := doc.Components.Schemas
	envelope := &huma.Schema{Ref: "#/components/schemas/ErrorEnvelope"}
	if registry.SchemaFromRef(envelope.Ref) == nil {
		t.Fatal("the document has no ErrorEnvelope schema")
	}

	for name, m := range map[string]*api.Mux{"main": s.main, "admin": s.admin} {
		methods := map[string]map[string]bool{}
		var routes []api.Route
		if err := m.Walk(func(r api.Route) error {
			routes = append(routes, r)
			if methods[r.Pattern] == nil {
				methods[r.Pattern] = map[string]bool{}
			}
			methods[r.Pattern][r.Method] = true
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, r := range routes {
			var op *huma.Operation
			if name == "main" {
				if item := doc.Paths[r.Pattern]; item != nil {
					op = api.Operations(item)[r.Method]
				}
			}
			failed := 0
			for _, p := range provoke(r, methods[r.Pattern], op) {
				s.ready.setPhase(p.phase)
				var body io.Reader
				if p.body != nil {
					body = bytes.NewReader(p.body)
				}
				req := httptest.NewRequest(p.method, p.path, body)
				maps.Copy(req.Header, p.header)
				if p.contentType != "" {
					req.Header.Set("Content-Type", p.contentType)
				}
				w := httptest.NewRecorder()
				m.ServeHTTP(w, req)
				if w.Code < 400 {
					continue
				}
				failed++
				where := fmt.Sprintf("%s %s, %s: %d", name, r, p.name, w.Code)
				schema := envelope
				if op != nil {
					var err error
					if schema, err = documentedError(op, w.Code); err != nil {
						t.Errorf("%s: the operation %v", where, err)
						continue
					}
				}
				checkEnvelope(t, where, registry, schema, w)
			}
			if failed == 0 {
				t.Errorf("%s %s: no request failed, so its errors are unchecked; add a provocation for it", name, r)
			}
		}
	}
}

// checkEnvelope checks that a recorded response is the envelope that schema
// describes, with a registered code of its status and the request's ID.
func checkEnvelope(t *testing.T, where string, registry huma.Registry, schema *huma.Schema,
	w *httptest.ResponseRecorder) {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s: Content-Type %q, want application/json", where, ct)
	}
	var v any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Errorf("%s: the body is not JSON: %v\n%s", where, err, w.Body)
		return
	}
	res := &huma.ValidateResult{}
	huma.Validate(registry, schema, huma.NewPathBuffer([]byte{}, 0), huma.ModeReadFromServer, v, res)
	for _, err := range res.Errors {
		t.Errorf("%s: the body does not match the documented envelope: %v\n%s", where, err, w.Body)
	}
	var env struct {
		Error api.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Errorf("%s: %v", where, err)
		return
	}
	switch c := env.Error.Code; {
	case !c.Known():
		t.Errorf("%s: the code %q is not registered", where, c)
	case c.Status() != w.Code:
		t.Errorf("%s: the code %s has the status %d", where, c, c.Status())
	}
	if id := w.Header().Get(requestIDHeader); id == "" || env.Error.RequestID != id {
		t.Errorf("%s: request_id %q, and X-Request-ID %q", where, env.Error.RequestID, id)
	}
}
