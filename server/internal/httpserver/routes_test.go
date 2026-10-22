package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

// tokenRoutes are the routes that take an API token, each with the scope
// that the token must hold (PRD FR-AUTH-04, plan note N35): the reads of the
// workspaces and their projects with snapshot:read, so that a token that sees
// only releases still finds them, and the changes of a project's settings
// and its batches of commands, which change its draft, with project:write.
// Every other route that needs a signed-in user is
// session-only. A change of this list changes what a leaked token can do, so
// the test makes it here as well as where the route is registered.
var tokenRoutes = map[api.Route]scope.Scope{
	{Method: http.MethodGet, Pattern: "/api/v1/workspaces"}:                         scope.SnapshotRead,
	{Method: http.MethodGet, Pattern: "/api/v1/workspaces/{workspace_id}"}:          scope.SnapshotRead,
	{Method: http.MethodGet, Pattern: "/api/v1/workspaces/{workspace_id}/projects"}: scope.SnapshotRead,
	{Method: http.MethodGet, Pattern: "/api/v1/projects/{project_id}"}:              scope.SnapshotRead,
	{Method: http.MethodPatch, Pattern: "/api/v1/projects/{project_id}"}:            scope.ProjectWrite,
	{Method: http.MethodPost, Pattern: "/api/v1/projects/{project_id}/archive"}:     scope.ProjectWrite,
	{Method: http.MethodPost, Pattern: "/api/v1/projects/{project_id}/unarchive"}:   scope.ProjectWrite,
	{Method: http.MethodPost, Pattern: "/api/v1/projects/{project_id}/commands"}:    scope.ProjectWrite,
	// A project's assets are its draft's content: reading them needs
	// project:read, and uploading one project:write.
	{Method: http.MethodPost, Pattern: assetsPath}:                          scope.ProjectWrite,
	{Method: http.MethodGet, Pattern: assetsPath + "/{asset_id}"}:           scope.ProjectRead,
	{Method: http.MethodGet, Pattern: assetsPath + "/{asset_id}/content"}:   scope.ProjectRead,
	{Method: http.MethodGet, Pattern: assetsPath + "/{asset_id}/thumbnail"}: scope.ProjectRead,
	// So are its boards and folders, which change through its commands.
	{Method: http.MethodGet, Pattern: "/api/v1/projects/{project_id}/boards"}:            scope.ProjectRead,
	{Method: http.MethodGet, Pattern: "/api/v1/projects/{project_id}/boards/{board_id}"}: scope.ProjectRead,
	{Method: http.MethodGet, Pattern: "/api/v1/projects/{project_id}/folders"}:           scope.ProjectRead,
}

// operationOf returns the operation of the route r in the OpenAPI document,
// or nil if the document has none, as for a page.
func operationOf(doc *huma.OpenAPI, r api.Route) *huma.Operation {
	item := doc.Paths[r.Pattern]
	if item == nil {
		return nil
	}
	return api.Operations(item)[r.Method]
}

// TestRouteClassification checks that every route is classified (PRD
// FR-AUTH-04 AC6): a route that needs no credentials takes neither an API
// token nor a session alone, and every other route either takes a token,
// with the scope that tokenRoutes lists, or is session-only, and says so in
// its description. A token that holds every scope is refused on each
// session-only route with 403 session_required, before the route reads its
// parameters, and the audit log records its first refusal alone.
//
// Verifies: FR-AUTH-04/AC6
func TestRouteClassification(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	tokens, sessionOnly := f.s.main.TokenScopes(), f.s.main.SessionOnlyRoutes()
	if !maps.Equal(tokens, tokenRoutes) {
		t.Errorf("the routes that take a token are %v, want %v", tokens, tokenRoutes)
	}
	doc := f.s.main.OpenAPI()
	authenticated := 0
	for r, c := range f.s.main.Routes() {
		needed, token := tokens[r]
		switch {
		case !c.Authenticated() && (token || sessionOnly[r]):
			t.Errorf("%s requires %s, which needs no credentials, and is classified as taking a token (%t) or "+
				"session-only (%t)", r, c, token, sessionOnly[r])
			continue
		case !c.Authenticated():
			continue
		case token == sessionOnly[r]:
			t.Errorf("%s requires %s, and takes a token (%t) and is session-only (%t)", r, c, token, sessionOnly[r])
			continue
		}
		authenticated++
		op := operationOf(doc, r)
		switch {
		case op == nil:
			t.Errorf("%s is not in the OpenAPI document", r)
		case token && !strings.Contains(op.Description, tokenAuthDoc(needed)):
			t.Errorf("the description of %s does not say that it takes a token with %s: %q", r, needed,
				op.Description)
		case sessionOnly[r] && !strings.Contains(op.Description, sessionAuthDoc):
			t.Errorf("the description of %s does not say that it is session-only: %q", r, op.Description)
		}
	}
	if authenticated != len(tokens)+len(sessionOnly) {
		t.Errorf("%d routes need credentials, %d take a token, and %d are session-only", authenticated,
			len(tokens), len(sessionOnly))
	}

	every := makeToken(t, f.s.main, f.as["owner"], workspaceTokensPath(f.ws), scope.Names(scope.All())...)
	for r := range sessionOnly {
		// The token is refused before the route reads its parameters.
		res := call(f.s.main, r.Method, routeParam.ReplaceAllString(r.Pattern, "1"), nil, bearer(every.Secret))
		wantCode(t, r.String()+" with a token of every scope", res, http.StatusForbidden, api.CodeSessionRequired)
	}
	if len(sessionOnly) < 30 {
		t.Errorf("the test tried %d session-only routes", len(sessionOnly))
	}
	if got := f.tokenAudit(t, every.ID); !slices.Equal(got, []string{"token.create success",
		"token.use_denied denied session_required"}) {
		t.Errorf("the audit log of the token of every scope, refused %d times, is %q, want its creation and its "+
			"first refusal", len(sessionOnly), got)
	}
}

// credentialRefusal reports whether res refuses its request for its
// credentials: for none, or ones that are not valid, a missing CSRF token, a
// token on a session-only route, or a token without the route's scope.
func credentialRefusal(res response) bool {
	switch res.code() {
	case api.CodeUnauthenticated, api.CodeCSRFFailed, api.CodeSessionRequired, api.CodeInsufficientScope:
		return true
	}
	return res.status == http.StatusUnauthorized
}

// TestEveryRouteReachable checks that every route that needs a signed-in
// user is reachable with a session cookie, and every route that takes an
// API token also with a token that holds exactly the scope that the route
// declares (PRD FR-API-01 AC1): its request is not refused for its
// credentials, whatever else its answer says. Each request with a session
// is made with a session of its own, so that signing out, or revoking the
// other sessions, ends no session that a later request is made with.
//
// Verifies: FR-API-01/AC1
func TestEveryRouteReachable(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	params := routeParams(t, f)
	params["session_id"] = ids.Encode(ids.Session, ids.New())
	var routes []api.Route
	for r, c := range f.s.main.Routes() {
		if c.Authenticated() {
			routes = append(routes, r)
		}
	}
	reached := 0
	for _, c := range callsOf(t, f, params, routes) {
		si := signedInFrom(t, call(f.s.main, http.MethodPost, loginPath, credentials(t, "admin@example.com",
			testPassword), nil))
		if res := call(f.s.main, c.route.Method, c.path, c.body, si.header(true)); credentialRefusal(res) {
			t.Errorf("%s with a session: %d %s", c.route, res.status, res.body)
		}
		reached++
	}
	if reached < 40 {
		t.Errorf("the test reached %d routes with a session", reached)
	}

	// One of the requests revoked the owner's other sessions, so the owner
	// signs in again.
	owner := signedInFrom(t, call(f.s.main, http.MethodPost, loginPath, credentials(t, "admin@example.com",
		testPassword), nil))
	f.as["owner"] = owner
	held := map[scope.Scope]CreatedAPITokenBody{}
	for _, c := range tokenRouteCalls(t, f, routeParams(t, f)) {
		needed := f.s.main.TokenScopes()[c.route]
		tok, ok := held[needed]
		if !ok {
			tok = makeToken(t, f.s.main, owner, workspaceTokensPath(f.ws), string(needed))
			held[needed] = tok
		}
		if res := call(f.s.main, c.route.Method, c.path, c.body, bearer(tok.Secret)); credentialRefusal(res) ||
			res.status >= 300 {
			t.Errorf("%s with a %s token: %d %s", c.route, needed, res.status, res.body)
		}
	}
}

// guardedFirst are the routes whose guards refuse their requests, in the
// configuration of the roles fixture, before their rate limit counts them,
// with the code that they refuse them with: the instance sends no email, its
// first-run setup has completed, and the project of an upload is not one.
var guardedFirst = map[api.Route]api.Code{
	{Method: http.MethodPost, Pattern: "/api/v1/auth/password-reset"}:     api.CodeEmailNotConfigured,
	{Method: http.MethodPost, Pattern: "/api/v1/auth/email-verification"}: api.CodeEmailNotConfigured,
	{Method: http.MethodPost, Pattern: "/api/v1/setup"}:                   api.CodeNotFound,
	// An upload's guard checks the project before the route reads the file,
	// and the walk names it with an ID that is not one.
	{Method: http.MethodPost, Pattern: assetsPath}: api.CodeInvalidID,
}

// authRoutes are the routes that count in the auth bucket (plan notes N23,
// N35), each of which authLimitDoc names, as the description of every one of
// them says: signing in and the other requests about credentials, and
// making, listing, and revoking API tokens, in which no request made with a
// token counts, so that a token whose requests use up its owner's limits of
// reading and of changing something cannot keep its owner from finding and
// revoking it (TestTokenCannotLockOutItsOwner). Signing out and the routes of
// sessions count in their methods' buckets, so that the sign-ins of the other
// users of an IP address, and the account's other requests about
// credentials, do not use up their limits. A change of this list changes what
// authLimitDoc must say.
var authRoutes = map[api.Route]bool{
	{Method: http.MethodPost, Pattern: "/api/v1/auth/login"}:                                           true,
	{Method: http.MethodPost, Pattern: "/api/v1/auth/register"}:                                        true,
	{Method: http.MethodPost, Pattern: "/api/v1/auth/password"}:                                        true,
	{Method: http.MethodPost, Pattern: "/api/v1/setup"}:                                                true,
	{Method: http.MethodPost, Pattern: "/api/v1/auth/password-reset"}:                                  true,
	{Method: http.MethodPost, Pattern: "/api/v1/auth/password-reset/confirm"}:                          true,
	{Method: http.MethodPost, Pattern: "/api/v1/auth/email-verification"}:                              true,
	{Method: http.MethodPost, Pattern: "/api/v1/auth/verify-email"}:                                    true,
	{Method: http.MethodPost, Pattern: "/api/v1/admin/password-reset-links"}:                           true,
	{Method: http.MethodPost, Pattern: "/api/v1/admin/verification-links"}:                             true,
	{Method: http.MethodPost, Pattern: "/api/v1/workspaces/{workspace_id}/invites"}:                    true,
	{Method: http.MethodPost, Pattern: "/api/v1/workspaces/{workspace_id}/invites/{invite_id}/resend"}: true,
	{Method: http.MethodPost, Pattern: "/api/v1/invites/lookup"}:                                       true,
	{Method: http.MethodPost, Pattern: "/api/v1/invites/accept"}:                                       true,
	{Method: http.MethodPost, Pattern: "/api/v1/invites/register"}:                                     true,
	{Method: http.MethodPost, Pattern: "/api/v1/workspaces/{workspace_id}/tokens"}:                     true,
	{Method: http.MethodPost, Pattern: "/api/v1/projects/{project_id}/tokens"}:                         true,
	{Method: http.MethodGet, Pattern: "/api/v1/workspaces/{workspace_id}/tokens"}:                      true,
	{Method: http.MethodGet, Pattern: "/api/v1/projects/{project_id}/tokens"}:                          true,
	{Method: http.MethodDelete, Pattern: "/api/v1/workspaces/{workspace_id}/tokens/{token_id}"}:        true,
	{Method: http.MethodDelete, Pattern: "/api/v1/projects/{project_id}/tokens/{token_id}"}:            true,
}

// TestEveryRouteRateLimited checks that a rate limit counts every route of
// the API (PRD FR-API-04, NFR-SEC-06): each counts in the auth bucket if it
// is one of authRoutes, and otherwise in the read bucket if it is a GET and
// in the write bucket if it is not, and only the health and readiness
// probes, /version, and the pages count in none. The description of each
// route of the auth bucket says what its limit is (authLimitDoc), as the
// server's policy has it. With a policy that allows each key one request a
// minute, and an owner who has made theirs of each bucket from a client's
// address, which uses up the limits of both, every route answers 429
// rate_limited, with its bucket in details.bucket, and Retry-After (FR-API-04
// AC3): a public route the client's address, and a route that needs a
// signed-in user the owner, from an address that has made no request, so that
// a route that does not count its requests against their user fails. Only
// the routes whose guards refuse the request before the limit counts it
// answer otherwise (guardedFirst), and an upload that its guard lets through,
// to a project of the owner's, is refused by the limit too.
//
// Verifies: FR-API-04/AC3, NFR-SEC-06
func TestEveryRouteRateLimited(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	buckets := f.s.main.Buckets()
	doc := f.s.main.OpenAPI()
	authDoc := authLimitDoc(ratelimit.DefaultPolicy().Rates[ratelimit.Auth])
	var unlimited []string
	for r := range f.s.main.Routes() {
		b, ok := buckets[r]
		want := api.BucketWrite
		switch {
		case authRoutes[r]:
			want = api.BucketAuth
		case r.Method == http.MethodGet:
			want = api.BucketRead
		}
		switch {
		case !ok:
			if !f.s.main.IsPage(r) {
				unlimited = append(unlimited, r.String())
			}
		case b != want:
			t.Errorf("%s counts in the %s bucket, want %s", r, b, want)
		case b != api.BucketAuth:
		case operationOf(doc, r) == nil:
			t.Errorf("%s is not in the OpenAPI document", r)
		case !strings.Contains(operationOf(doc, r).Description, authDoc):
			t.Errorf("the description of %s does not say what the auth bucket's limit is", r)
		}
	}
	slices.Sort(unlimited)
	if want := []string{"GET /healthz", "GET /readyz", "GET /version"}; !slices.Equal(unlimited, want) {
		t.Errorf("no rate limit counts %q, want only %q and the pages", unlimited, want)
	}
	for r := range authRoutes {
		if _, ok := f.s.main.Routes()[r]; !ok {
			t.Errorf("authRoutes lists %s, which the server does not have", r)
		}
	}

	// A project that an upload may name, whose guard lets it through, which
	// the fixture adds to the database rather than through the API, whose
	// request would count in the owner's write bucket in the window of the
	// requests below, which the policy then lets make one request each.
	project := f.project(t, "")
	once := ratelimit.DefaultPolicy()
	for b, r := range once.Rates {
		for k := range r.Limits {
			r.Limits[k] = 1
		}
		once.Rates[b] = r
	}
	f.s.limits = ratelimit.New(readyDB{f.s.ready}, once)
	owner := f.as["owner"]
	client := newClient()
	// The requests must fall in one window of a minute.
	f.d.AwaitWindow(t, time.Minute, 30*time.Second)
	// The owner makes the one request of each bucket that the policy allows,
	// from the client's address, which counts it too.
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/session"},
		{http.MethodPatch, workspacesPath + "/" + f.ws},
		{http.MethodGet, workspaceTokensPath(f.ws)},
	} {
		if res := send(f.s.main, r.method, r.path, []byte("{}"), "", with(owner.header(true), client)); res.status ==
			http.StatusTooManyRequests {
			t.Fatalf("%s %s, the first request: %d %s", r.method, r.path, res.status, res.body)
		}
	}
	routes := f.s.main.Routes()
	for r, b := range buckets {
		header := client
		if routes[r].Authenticated() {
			header = with(owner.header(true), newClient())
		}
		res := send(f.s.main, r.Method, routeParam.ReplaceAllString(r.Pattern, "1"), nil, "", header)
		if code, ok := guardedFirst[r]; ok {
			wantCode(t, r.String()+", which a guard refuses first", res, code.Status(), code)
			continue
		}
		if res.status != http.StatusTooManyRequests || res.code() != api.CodeRateLimited ||
			res.details(t)["bucket"] != string(b) || headerInt(res, "Retry-After") < 1 ||
			headerInt(res, limitHeader) != 1 {
			t.Errorf("%s over the limit of the %s bucket: %d %s %v", r, b, res.status, res.body, res.header)
		}
	}
	// The walk's upload names no project, which its guard refuses before the
	// limit counts it; an upload that the guard lets through is counted, and
	// refused over the limit, before it waits for a slot (api.AfterLimit), or
	// its file is read.
	if res := uploadFile(f.s.main, owner, project, []byte("file"), "application/octet-stream"); res.status !=
		http.StatusTooManyRequests || res.code() != api.CodeRateLimited {
		t.Errorf("an upload to the owner's project over the limit: %d %s", res.status, res.body)
	}
}

// TestAPIRequestsMetered checks that a workspace's usage counts the requests
// that its API tokens make (PRD FR-ENT-02, plan note N31), in its
// api_requests, which its admins read through the API: each request that a
// token is accepted for adds one, whatever its answer, and a request that a
// token is refused for, or that is made with a session, adds none. The count
// is the same before and after the rollup job folds it, and a token of
// another workspace counts in its own.
//
// Verifies: FR-ENT-02/AC2
func TestAPIRequestsMetered(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Lighthouse")
	tok := makeToken(t, s.main, owner, workspaceTokensPath(ws), "snapshot:read")
	used := func(ws string) float64 {
		t.Helper()
		return readUsage(t, s.main, owner, ws)["api_requests"]["used"].(float64)
	}
	if n := used(ws); n != 0 {
		t.Fatalf("a new workspace has %v API requests", n)
	}
	for range 3 {
		projectFrom(t, call(s.main, http.MethodGet, projectPath(p.ID), nil, bearer(tok.Secret)), http.StatusOK)
	}
	// A project that does not exist is the operation's answer, after the
	// token was accepted.
	wantCode(t, "a project that does not exist", call(s.main, http.MethodGet,
		projectPath(ids.Encode(ids.Project, ids.New())), nil, bearer(tok.Secret)), http.StatusNotFound,
		api.CodeNotFound)
	// None of these counts: the token is refused, or the request is not made
	// with it.
	wantCode(t, "a change with a snapshot:read token", call(s.main, http.MethodPatch, projectPath(p.ID),
		jsonBody(t, map[string]string{"name": "Harbour"}), bearer(tok.Secret)), http.StatusForbidden,
		api.CodeInsufficientScope)
	wantCode(t, "a session-only route with the token", call(s.main, http.MethodGet, usagePath(ws), nil,
		bearer(tok.Secret)), http.StatusForbidden, api.CodeSessionRequired)
	wantCode(t, "a token that is not one", call(s.main, http.MethodGet, projectPath(p.ID), nil,
		bearer("scp_nothing")), http.StatusUnauthorized, api.CodeUnauthenticated)
	projectFrom(t, call(s.main, http.MethodGet, projectPath(p.ID), nil, owner.header(false)), http.StatusOK)
	if n := used(ws); n != 4 {
		t.Errorf("the workspace has %v API requests before the rollup, want 4", n)
	}
	if err := usage.RollUp(context.Background(), readyDB{s.ready}); err != nil {
		t.Fatal(err)
	}
	if n := used(ws); n != 4 {
		t.Errorf("the workspace has %v API requests after the rollup, want 4", n)
	}

	var other membershipBody
	if err := json.Unmarshal(call(s.main, http.MethodPost, workspacesPath, jsonBody(t,
		map[string]string{"name": "Other", "slug": "other"}), owner.header(true)).body, &other); err != nil {
		t.Fatal(err)
	}
	elsewhere := makeToken(t, s.main, owner, workspaceTokensPath(other.ID), "snapshot:read")
	if got := itemIDs(t, s.main, bearer(elsewhere.Secret), workspacesPath); !slices.Equal(got, []string{other.ID}) {
		t.Errorf("the other workspace's token lists %q", got)
	}
	if a, b := used(ws), used(other.ID); a != 4 || b != 1 {
		t.Errorf("the workspaces have %v and %v API requests, want 4 and 1", a, b)
	}
}

// TestTimestamps checks that every timestamp of the API is written in RFC
// 3339 (PRD FR-API-01): each property whose name ends in _at is a string of
// the format date-time, which JSON Schema defines as RFC 3339's, and which a
// time.Time is encoded as, and so is every other property of that format,
// as hard_delete_after.
func TestTimestamps(t *testing.T) {
	t.Parallel()
	s := newServer(loadConfig(t, nil), slog.New(slog.DiscardHandler))
	doc := s.main.OpenAPI()
	found := 0
	for name, schema := range doc.Components.Schemas.Map() {
		for prop, p := range schema.Properties {
			timestamp := strings.HasSuffix(prop, "_at")
			for p.Ref != "" {
				p = doc.Components.Schemas.SchemaFromRef(p.Ref)
			}
			isTime := p.Format == "date-time"
			if timestamp && !isTime || isTime && p.Type != huma.TypeString {
				t.Errorf("%s.%s has the type %s and the format %q", name, prop, p.Type, p.Format)
			}
			if isTime {
				found++
			}
		}
	}
	if found < 10 {
		t.Errorf("the document has %d timestamps", found)
	}
}
