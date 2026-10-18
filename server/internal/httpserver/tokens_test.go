package httpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// The paths of a workspace's tokens, and of a project's, and of one of each.
func workspaceTokensPath(ws string) string          { return workspacesPath + "/" + ws + "/tokens" }
func workspaceTokenPath(ws, token string) string    { return workspaceTokensPath(ws) + "/" + token }
func projectTokensPath(project string) string       { return projectPath(project) + "/tokens" }
func projectTokenPath(project, token string) string { return projectTokensPath(project) + "/" + token }

// tokenRequest returns the body of a request that makes a token with the
// scopes.
func tokenRequest(t *testing.T, scopes ...string) []byte {
	t.Helper()
	return jsonBody(t, map[string]any{"name": "CI", "scopes": scopes})
}

// createdToken returns the token that res, the answer to a request that made
// one, holds, or fails the test if it holds none.
func createdToken(t *testing.T, res response) CreatedAPITokenBody {
	t.Helper()
	if res.status != http.StatusCreated {
		t.Fatalf("making a token: %d %s", res.status, res.body)
	}
	var b CreatedAPITokenBody
	if err := json.Unmarshal(res.body, &b); err != nil {
		t.Fatal(err)
	}
	return b
}

// makeToken makes a token at path, the tokens of a workspace or of a project,
// as si, with the scopes, and returns it with its secret.
func makeToken(t *testing.T, h http.Handler, si signedIn, path string, scopes ...string) CreatedAPITokenBody {
	t.Helper()
	return createdToken(t, call(h, http.MethodPost, path, tokenRequest(t, scopes...), si.header(true)))
}

// bearer returns the header of a request made with the token whose secret is
// secret.
func bearer(secret string) http.Header {
	return http.Header{"Authorization": {"Bearer " + secret}}
}

// token adds a token of the fixture's workspace for the account of the role
// r, narrowed to the project unless it is "", as the superuser, and returns
// its ID.
func (f *rolesFixture) token(t *testing.T, r, project string) string {
	t.Helper()
	return f.tokenFor(t, decode(t, ids.User, f.as[r].body.UserID), project)
}

// otherToken adds a member of the workspace, an editor, and a token of
// theirs, narrowed to the project unless it is "", as the superuser, and
// returns the token's ID.
func (f *rolesFixture) otherToken(t *testing.T, project string) string {
	t.Helper()
	var user ids.UUID
	if err := f.admin.QueryRow(context.Background(), "SELECT user_id FROM membership WHERE id = $1",
		decode(t, ids.Membership, f.member(t, "editor"))).Scan(&user); err != nil {
		t.Fatal(err)
	}
	return f.tokenFor(t, user, project)
}

// tokenFor adds a token of the fixture's workspace for the user, narrowed to
// the project unless it is "", as the superuser, and returns its ID.
func (f *rolesFixture) tokenFor(t *testing.T, user ids.UUID, project string) string {
	t.Helper()
	var p *ids.UUID
	if project != "" {
		id := decode(t, ids.Project, project)
		p = &id
	}
	id := ids.New()
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO api_token
		(id, workspace_id, user_id, project_id, name, token_hash, scopes) VALUES ($1, $2, $3, $4, 'CI', $5,
		ARRAY['project:read'])`, id, decode(t, ids.Workspace, f.ws), user, p, isolationtest.TokenHash()); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.APIToken, id)
}

// count returns the number that the query, run as the superuser, counts.
func (f *rolesFixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.admin.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// tokenAudit returns the audit entries about the token, in order, each as
// its action, its result, and its metadata's reason, if it has one.
func (f *rolesFixture) tokenAudit(t *testing.T, token string) []string {
	t.Helper()
	rows, err := f.admin.Query(context.Background(), `SELECT concat_ws(' ', action, result, metadata->>'reason')
		FROM audit_entry WHERE subject_id = $1 ORDER BY created_at, id`, decode(t, ids.APIToken, token))
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestTokenSecretShownOnce checks that making a token answers with its
// secret, which starts with scp_, that the database holds only the SHA-256
// hash of its random part, and that no other answer holds it: neither the
// lists of tokens, nor any route of the workspace or of a project that
// reads, asked by the token's owner or with the token itself, nor the audit
// log.
//
// Verifies: FR-AUTH-04/AC1, NFR-SEC-05
func TestTokenSecretShownOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newRolesFixture(t)
	owner := f.as["owner"]
	p := f.project(t, "")
	made := []CreatedAPITokenBody{
		makeToken(t, f.s.main, owner, workspaceTokensPath(f.ws), "project:write"),
		makeToken(t, f.s.main, owner, projectTokensPath(p), "project:read"),
	}
	if made[0].Secret == made[1].Secret {
		t.Error("two tokens have one secret")
	}
	for _, tok := range made {
		random, ok := strings.CutPrefix(tok.Secret, "scp_")
		raw, err := base64.RawURLEncoding.DecodeString(random)
		if !ok || err != nil || len(raw) != 32 || !strings.HasPrefix(tok.ID, "tok_") {
			t.Fatalf("the token %+v has a secret that is not scp_ and 32 random bytes", tok)
		}
		want := sha256.Sum256(raw)
		var hash []byte
		if err := f.admin.QueryRow(ctx, "SELECT token_hash FROM api_token WHERE id = $1",
			decode(t, ids.APIToken, tok.ID)).Scan(&hash); err != nil || !bytes.Equal(hash, want[:]) {
			t.Errorf("the token %s is held as %x, %v, want the SHA-256 hash of its secret, %x", tok.ID, hash, err,
				want)
		}
		if n := f.count(t, "SELECT count(*) FROM api_token t WHERE strpos(t::text, $1) > 0", random); n != 0 {
			t.Errorf("%d rows of api_token hold the secret of %s", n, tok.ID)
		}
		if n := f.count(t, "SELECT count(*) FROM audit_entry a WHERE strpos(a::text, $1) > 0", random); n != 0 {
			t.Errorf("%d audit entries hold the secret of %s", n, tok.ID)
		}
	}
	params := routeParams(t, f)
	params["project_id"] = p
	reads := 0
	for _, c := range routeCalls(t, f, params) {
		if c.route.Method != http.MethodGet {
			continue
		}
		for _, h := range []http.Header{owner.header(false), bearer(made[0].Secret)} {
			reads++
			res := call(f.s.main, http.MethodGet, c.path, nil, h)
			for _, tok := range made {
				if bytes.Contains(res.body, []byte(strings.TrimPrefix(tok.Secret, "scp_"))) {
					t.Errorf("%s answers with the secret of %s: %s", c.route, tok.ID, res.body)
				}
			}
		}
	}
	if reads < 10 {
		t.Errorf("the test read %d routes", reads)
	}
}

// TestTokenScopes walks every route that takes an API token
// (api.TokenScope) with a token of each scope alone, made by the
// workspace's owner, whose role allows everything: a token whose scope
// grants the route's, itself or by an implication, reaches the operation,
// and every other is refused with 403 insufficient_scope, which names the
// route's scope, as a project:read token is on every route that changes
// something. A token that holds every scope is refused with 403
// session_required on every route that takes none. Each refusal is in the
// audit log, with its reason.
//
// Verifies: FR-AUTH-04/AC2
func TestTokenScopes(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner := f.as["owner"]
	tokenRoutes := f.s.main.TokenScopes()
	writes := 0
	for _, held := range scope.All() {
		tok := makeToken(t, f.s.main, owner, workspaceTokensPath(f.ws), string(held))
		// Each scope acts on a project of its own, so that the changes of
		// one do not change what the next finds.
		params := routeParams(t, f)
		walked := 0
		for _, c := range routeCalls(t, f, params) {
			needed, ok := tokenRoutes[c.route]
			if !ok {
				continue
			}
			walked++
			res := call(f.s.main, c.route.Method, c.path, c.body, bearer(tok.Secret))
			where := fmt.Sprintf("%s with a %s token", c.route, held)
			if scope.Grants([]scope.Scope{held}, needed) {
				if res.status >= 300 {
					t.Errorf("%s: %d %s, want it done", where, res.status, res.body)
				}
				continue
			}
			wantCode(t, where, res, http.StatusForbidden, api.CodeInsufficientScope)
			if got := res.details(t)["scope"]; got != string(needed) {
				t.Errorf("%s: details.scope is %v, want %s", where, got, needed)
			}
			if held == scope.ProjectRead && c.route.Method != http.MethodGet {
				writes++
			}
		}
		if walked < 4 {
			t.Errorf("the walk with a %s token tried %d routes", held, walked)
		}
	}
	if writes < 3 {
		t.Errorf("a project:read token was refused %d routes that change something", writes)
	}
	if n := f.count(t, "SELECT count(*) FROM audit_entry WHERE action = 'token.use_denied' "+
		"AND metadata->>'reason' = 'insufficient_scope' AND result = 'denied'"); n < 10 {
		t.Errorf("the audit log has %d refusals for want of a scope", n)
	}

	every := makeToken(t, f.s.main, owner, workspaceTokensPath(f.ws), "snapshot:read", "project:read",
		"project:write", "snapshot:write", "export:read")
	sessionOnly := 0
	for r, c := range f.s.main.Routes() {
		if _, ok := tokenRoutes[r]; ok || !c.Authenticated() {
			continue
		}
		sessionOnly++
		// The token is refused before the route reads its parameters.
		res := call(f.s.main, r.Method, routeParam.ReplaceAllString(r.Pattern, "1"), nil, bearer(every.Secret))
		wantCode(t, r.String()+" with a token of every scope", res, http.StatusForbidden, api.CodeSessionRequired)
	}
	if sessionOnly < 20 {
		t.Errorf("the test tried %d session-only routes", sessionOnly)
	}
	if got := f.tokenAudit(t, every.ID); len(got) != sessionOnly+1 || got[1] != "token.use_denied denied "+
		"session_required" {
		t.Errorf("the audit log of the token of every scope is %q, want its creation and %d refusals", got,
			sessionOnly)
	}
}

// TestTokenNarrowedToProject checks that a token narrowed to a project acts
// on it, and answers every other project of its workspace with 404, as one
// that does not exist, and that a token of one workspace answers a project
// of another with 404, although its owner is a member of both.
//
// Verifies: FR-AUTH-04/AC3
func TestTokenNarrowedToProject(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner := f.as["owner"]
	a, b := f.project(t, ""), f.project(t, "")
	narrowed := makeToken(t, f.s.main, owner, projectTokensPath(a), "project:write")
	if narrowed.ProjectID != a {
		t.Errorf("the token is narrowed to %q, want %s", narrowed.ProjectID, a)
	}
	if got := projectFrom(t, call(f.s.main, http.MethodGet, projectPath(a), nil, bearer(narrowed.Secret)),
		http.StatusOK); got.ID != a || got.Role != "owner" {
		t.Errorf("the narrowed token reads %+v", got)
	}
	projectFrom(t, call(f.s.main, http.MethodPatch, projectPath(a), jsonBody(t, map[string]string{"name": "Harbour"}),
		bearer(narrowed.Secret)), http.StatusOK)
	for _, c := range []struct {
		method, path string
		body         []byte
	}{
		{http.MethodGet, projectPath(b), nil},
		{http.MethodPatch, projectPath(b), jsonBody(t, map[string]string{"name": "Harbour"})},
		{http.MethodPost, projectPath(b) + "/archive", nil},
		{http.MethodPost, projectPath(f.project(t, "archived")) + "/unarchive", nil},
	} {
		wantCode(t, c.method+" another project with the narrowed token",
			call(f.s.main, c.method, c.path, c.body, bearer(narrowed.Secret)), http.StatusNotFound, api.CodeNotFound)
	}
	// The owner's session reads b, which exists.
	projectFrom(t, call(f.s.main, http.MethodGet, projectPath(b), nil, owner.header(false)), http.StatusOK)

	var other membershipBody
	if err := json.Unmarshal(call(f.s.main, http.MethodPost, workspacesPath, jsonBody(t,
		map[string]string{"name": "Other", "slug": "other"}), owner.header(true)).body, &other); err != nil {
		t.Fatal(err)
	}
	c := createProject(t, f.s.main, owner, other.ID, "Elsewhere")
	wide := makeToken(t, f.s.main, owner, workspaceTokensPath(f.ws), "project:write")
	projectFrom(t, call(f.s.main, http.MethodGet, projectPath(b), nil, bearer(wide.Secret)), http.StatusOK)
	wantCode(t, "another workspace's project with a token of the workspace",
		call(f.s.main, http.MethodGet, projectPath(c.ID), nil, bearer(wide.Secret)), http.StatusNotFound,
		api.CodeNotFound)
	wantCode(t, "archiving another workspace's project with a token of the workspace",
		call(f.s.main, http.MethodPost, projectPath(c.ID)+"/archive", nil, bearer(wide.Secret)), http.StatusNotFound,
		api.CodeNotFound)
	if got := projectFrom(t, call(f.s.main, http.MethodGet, projectPath(c.ID), nil, owner.header(false)),
		http.StatusOK); got.ArchivedAt != nil {
		t.Error("a token of another workspace archived the project")
	}
}

// TestTokenFailsWithOwner checks that a token never does more than its
// owner's role allows at the time of use: removing the owner's membership
// revokes their tokens, which fail at the very next request, with 401 and
// the WWW-Authenticate header of an invalid token, and do not work again if
// the owner rejoins; a demoted owner's token reads, but no longer changes; a
// token whose owner is no longer a member, or whose account is not active,
// fails too. The audit log records the revocations, and each refused use.
//
// Verifies: FR-AUTH-04/AC4, FR-TEN-07/AC2
func TestTokenFailsWithOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newRolesFixture(t)
	owner, editor, admin := f.as["owner"], f.as["editor"], f.as["admin"]
	p := f.project(t, "")
	read := makeToken(t, f.s.main, editor, workspaceTokensPath(f.ws), "project:read")
	narrowed := makeToken(t, f.s.main, editor, projectTokensPath(p), "project:read")
	projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, bearer(read.Secret)), http.StatusOK)

	if res := call(f.s.main, http.MethodDelete, memberPath(f.ws, f.membership(t, "editor")), nil,
		owner.header(true)); res.status != http.StatusNoContent {
		t.Fatalf("removing the editor: %d %s", res.status, res.body)
	}
	for _, tok := range []CreatedAPITokenBody{read, narrowed} {
		res := call(f.s.main, http.MethodGet, projectPath(p), nil, bearer(tok.Secret))
		wantCode(t, "the removed member's token", res, http.StatusUnauthorized, api.CodeUnauthenticated)
		if h := res.header.Get("WWW-Authenticate"); !strings.Contains(h, `error="invalid_token"`) {
			t.Errorf("the refusal's WWW-Authenticate is %q", h)
		}
		want := []string{"token.create success", "token.revoke success", "token.use_denied denied revoked"}
		if got := f.tokenAudit(t, tok.ID); !slices.Equal(got, want) {
			t.Errorf("the audit log of %s is %q, want %q", tok.ID, got, want)
		}
	}
	if n := f.count(t, "SELECT count(*) FROM audit_entry WHERE action = 'token.revoke' AND actor_user_id = $1 "+
		"AND metadata->>'membership_id' IS NOT NULL", decode(t, ids.User, owner.body.UserID)); n != 2 {
		t.Errorf("%d revocations name the removal's actor and the membership, want 2", n)
	}
	join(t, f.d, editor, f.ws, "editor")
	wantCode(t, "the token of a member who rejoined", call(f.s.main, http.MethodGet, projectPath(p), nil,
		bearer(read.Secret)), http.StatusUnauthorized, api.CodeUnauthenticated)

	write := makeToken(t, f.s.main, admin, workspaceTokensPath(f.ws), "project:write")
	projectFrom(t, call(f.s.main, http.MethodPost, projectPath(p)+"/archive", nil, bearer(write.Secret)),
		http.StatusOK)
	if res := changeRole(t, f.s.main, owner, f.ws, f.membership(t, "admin"), "viewer"); res.status != http.StatusOK {
		t.Fatalf("demoting the admin: %d %s", res.status, res.body)
	}
	wantCode(t, "the demoted admin's token", call(f.s.main, http.MethodPost, projectPath(p)+"/unarchive", nil,
		bearer(write.Secret)), http.StatusForbidden, api.CodeInsufficientRole)
	if got := projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, bearer(write.Secret)),
		http.StatusOK); got.Role != "viewer" {
		t.Errorf("the demoted admin's token reads the project as %s", got.Role)
	}

	// A membership that disappears without its removal, which revokes the
	// tokens, as it could by a path that a later change adds, and an
	// account that is not active fail all the same.
	commenter := f.tokenWithSecret(t, f.as["commenter"])
	if _, err := f.admin.Exec(ctx, "DELETE FROM membership WHERE id = $1",
		decode(t, ids.Membership, f.membership(t, "commenter"))); err != nil {
		t.Fatal(err)
	}
	viewerToken := f.tokenWithSecret(t, f.as["viewer"])
	if _, err := f.admin.Exec(ctx, `UPDATE "user" SET status = 'suspended' WHERE id = $1`,
		decode(t, ids.User, f.as["viewer"].body.UserID)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		token  CreatedAPITokenBody
		reason string
	}{{commenter, "owner_not_member"}, {viewerToken, "owner_inactive"}} {
		wantCode(t, "a token whose owner is "+c.reason, call(f.s.main, http.MethodGet, projectPath(p), nil,
			bearer(c.token.Secret)), http.StatusUnauthorized, api.CodeUnauthenticated)
		if got := f.tokenAudit(t, c.token.ID); len(got) == 0 || got[len(got)-1] != "token.use_denied denied "+c.reason {
			t.Errorf("the audit log of the token whose owner is %s is %q", c.reason, got)
		}
	}
}

// tokenWithSecret makes a project:read token of the workspace for si's
// account, through the API, whatever its role, which the superuser raises to
// editor while it does, and returns it with its secret.
func (f *rolesFixture) tokenWithSecret(t *testing.T, si signedIn) CreatedAPITokenBody {
	t.Helper()
	ctx := context.Background()
	user := decode(t, ids.User, si.body.UserID)
	var previous string
	if err := f.admin.QueryRow(ctx, `UPDATE membership m SET role = 'editor' FROM membership old
		WHERE m.id = old.id AND m.workspace_id = $1 AND m.user_id = $2 RETURNING old.role`,
		decode(t, ids.Workspace, f.ws), user).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	tok := makeToken(t, f.s.main, si, workspaceTokensPath(f.ws), "project:read")
	if _, err := f.admin.Exec(ctx, "UPDATE membership SET role = $3 WHERE workspace_id = $1 AND user_id = $2",
		decode(t, ids.Workspace, f.ws), user, previous); err != nil {
		t.Fatal(err)
	}
	return tok
}

// TestTokenLastUsed checks that a token records its last use, as the lists
// of tokens show it, and writes it at most once a minute however often it is
// used: a second request within the minute leaves it as the first wrote it,
// and the first request after a minute writes it again.
//
// Verifies: FR-AUTH-04/AC5
func TestTokenLastUsed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newRolesFixture(t)
	owner := f.as["owner"]
	p := f.project(t, "")
	tok := makeToken(t, f.s.main, owner, workspaceTokensPath(f.ws), "project:read")
	id := decode(t, ids.APIToken, tok.ID)
	lastUsed := func() *time.Time {
		t.Helper()
		var at *time.Time
		if err := f.admin.QueryRow(ctx, "SELECT last_used_at FROM api_token WHERE id = $1", id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	if tok.LastUsedAt != nil || lastUsed() != nil {
		t.Fatal("a new token has been used")
	}
	read := func() {
		t.Helper()
		projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, bearer(tok.Secret)), http.StatusOK)
	}
	read()
	first := lastUsed()
	if first == nil {
		t.Fatal("the token's use was not recorded")
	}
	for range 5 {
		read()
	}
	if again := lastUsed(); !again.Equal(*first) {
		t.Errorf("uses within the minute moved last_used_at from %v to %v", first, again)
	}
	if _, err := f.admin.Exec(ctx, "UPDATE api_token SET last_used_at = last_used_at - interval '61 seconds' "+
		"WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	read()
	if later := lastUsed(); !later.After(*first) {
		t.Errorf("a use a minute later left last_used_at at %v, after %v", later, first)
	}
	var page struct {
		Items []APITokenBody `json:"items"`
	}
	if err := json.Unmarshal(call(f.s.main, http.MethodGet, workspaceTokensPath(f.ws), nil, owner.header(false)).body,
		&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].LastUsedAt == nil || !page.Items[0].LastUsedAt.Equal(*lastUsed()) {
		t.Errorf("the list shows %+v, want the token with its last use", page.Items)
	}
}

// tokenIDs returns the IDs of the tokens on the first page of the list at
// path, which si reads, and which must answer 200.
func tokenIDs(t *testing.T, h http.Handler, si signedIn, path string) []string {
	t.Helper()
	res := call(h, http.MethodGet, path, nil, si.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("listing %s: %d %s", path, res.status, res.body)
	}
	var page struct {
		Items []APITokenBody `json:"items"`
	}
	if err := json.Unmarshal(res.body, &page); err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(page.Items))
	for i, tok := range page.Items {
		if tok.Name != "CI" || len(tok.Scopes) == 0 {
			t.Errorf("the list shows %+v", tok)
		}
		out[i] = tok.ID
	}
	return out
}

// TestTokenLifecycle checks what making, listing, and revoking tokens does
// (NFR-SEC-05): a token holds its scopes once each, in the order of the
// PRD's table, with an optional expiry, which its making, the lists, and the
// audit log give alike, as the database holds it, and after which it fails;
// the lists show the workspace's owners and admins every member's tokens,
// and an editor, or a guest with an editor's grant on a project, their own;
// revoking one makes it fail at once, and leaves it off the lists, and only
// an owner or an admin revokes another member's. A field that its schema or
// the service refuses is answered with 422, and a request that sends no API
// token in its Authorization header is made with its session.
//
// Verifies: NFR-SEC-05, FR-TEN-07/AC2
func TestTokenLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newRolesFixture(t)
	f.grants = map[string]string{"guest": "editor"}
	owner, admin, editor, guest := f.as["owner"], f.as["admin"], f.as["editor"], f.as["guest"]
	p := f.project(t, "")

	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	res := call(f.s.main, http.MethodPost, workspaceTokensPath(f.ws), jsonBody(t, map[string]any{
		"name": "  CI  ", "scopes": []string{"export:read", "project:read", "export:read"}, "expires_at": expires,
	}), editor.header(true))
	tok := createdToken(t, res)
	if tok.Name != "CI" || !slices.Equal(tok.Scopes, []string{"project:read", "export:read"}) || tok.ExpiresAt == nil ||
		!tok.ExpiresAt.Equal(expires) || tok.ProjectID != "" || tok.UserID != editor.body.UserID ||
		tok.WorkspaceID != f.ws {
		t.Errorf("the token is %+v", tok)
	}
	if res.header.Get("RateLimit-Limit") == "" {
		t.Error("making a token is not rate-limited")
	}
	for _, c := range []struct {
		body     map[string]any
		location string
	}{
		{map[string]any{"name": " ", "scopes": []string{"project:read"}}, "body.name"},
		{map[string]any{"name": "CI", "scopes": []string{}}, "body.scopes"},
		{map[string]any{"name": "CI", "scopes": []string{"project:admin"}}, "body.scopes[0]"},
		{map[string]any{"name": "CI", "scopes": []string{"project:read"},
			"expires_at": time.Now().Add(-time.Minute)}, "body.expires_at"},
	} {
		res := call(f.s.main, http.MethodPost, workspaceTokensPath(f.ws), jsonBody(t, c.body), editor.header(true))
		wantProblem(t, fmt.Sprintf("making a token with %v", c.body), res, c.location)
	}

	ownerToken := makeToken(t, f.s.main, owner, workspaceTokensPath(f.ws), "snapshot:read")
	ownerNarrowed := makeToken(t, f.s.main, owner, projectTokensPath(p), "project:read")
	guestToken := makeToken(t, f.s.main, guest, projectTokensPath(p), "project:write")
	for _, c := range []struct {
		who  string
		si   signedIn
		path string
		want []string
	}{
		{"the owner", owner, workspaceTokensPath(f.ws), []string{tok.ID, ownerToken.ID}},
		{"an admin", admin, workspaceTokensPath(f.ws), []string{tok.ID, ownerToken.ID}},
		{"an editor", editor, workspaceTokensPath(f.ws), []string{tok.ID}},
		{"the owner", owner, projectTokensPath(p), []string{ownerNarrowed.ID, guestToken.ID}},
		{"an editor", editor, projectTokensPath(p), nil},
		{"a guest with an editor grant", guest, projectTokensPath(p), []string{guestToken.ID}},
	} {
		if got := tokenIDs(t, f.s.main, c.si, c.path); !slices.Equal(got, c.want) && len(got)+len(c.want) > 0 {
			t.Errorf("%s lists %s as %v, want %v", c.who, c.path, got, c.want)
		}
	}
	wantCode(t, "a guest's list of the workspace's tokens", call(f.s.main, http.MethodGet, workspaceTokensPath(f.ws),
		nil, guest.header(false)), http.StatusNotFound, api.CodeNotFound)
	// The guest's grant allows reading, but not changing the settings.
	projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, bearer(guestToken.Secret)), http.StatusOK)
	wantCode(t, "the guest's token changing the settings", call(f.s.main, http.MethodPatch, projectPath(p),
		jsonBody(t, map[string]string{"name": "Pier"}), bearer(guestToken.Secret)), http.StatusForbidden,
		api.CodeInsufficientRole)

	for _, c := range []struct {
		who    string
		si     signedIn
		path   string
		status int
		code   api.Code
	}{
		{"an editor revoking the owner's token", editor, workspaceTokenPath(f.ws, ownerToken.ID),
			http.StatusForbidden, api.CodeInsufficientRole},
		{"a guest revoking the owner's token of the project", guest, projectTokenPath(p, ownerNarrowed.ID),
			http.StatusForbidden, api.CodeInsufficientRole},
		{"revoking a token narrowed to a project as one of the workspace", owner,
			workspaceTokenPath(f.ws, ownerNarrowed.ID), http.StatusNotFound, api.CodeNotFound},
		{"revoking a token of the workspace as one of a project", owner, projectTokenPath(p, ownerToken.ID),
			http.StatusNotFound, api.CodeNotFound},
	} {
		wantCode(t, c.who, call(f.s.main, http.MethodDelete, c.path, nil, c.si.header(true)), c.status, c.code)
	}
	for _, c := range []struct {
		si    signedIn
		path  string
		token CreatedAPITokenBody
	}{
		{admin, workspaceTokenPath(f.ws, ownerToken.ID), ownerToken},
		{guest, projectTokenPath(p, guestToken.ID), guestToken},
		{owner, projectTokenPath(p, ownerNarrowed.ID), ownerNarrowed},
	} {
		if res := call(f.s.main, http.MethodDelete, c.path, nil, c.si.header(true)); res.status != http.StatusNoContent {
			t.Fatalf("revoking %s: %d %s", c.token.ID, res.status, res.body)
		}
		wantCode(t, "a revoked token", call(f.s.main, http.MethodGet, projectPath(p), nil, bearer(c.token.Secret)),
			http.StatusUnauthorized, api.CodeUnauthenticated)
		wantCode(t, "revoking a token again", call(f.s.main, http.MethodDelete, c.path, nil, c.si.header(true)),
			http.StatusNotFound, api.CodeNotFound)
		if got := f.tokenAudit(t, c.token.ID); !slices.Equal(got, []string{"token.create success",
			"token.revoke success", "token.use_denied denied revoked"}) {
			t.Errorf("the audit log of %s is %q", c.token.ID, got)
		}
	}
	if got := tokenIDs(t, f.s.main, owner, workspaceTokensPath(f.ws)); !slices.Equal(got, []string{tok.ID}) {
		t.Errorf("the owner lists %v once a token is revoked", got)
	}

	projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, bearer(tok.Secret)), http.StatusOK)
	if _, err := f.admin.Exec(ctx, "UPDATE api_token SET expires_at = now() - interval '1 second' WHERE id = $1",
		decode(t, ids.APIToken, tok.ID)); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "an expired token", call(f.s.main, http.MethodGet, projectPath(p), nil, bearer(tok.Secret)),
		http.StatusUnauthorized, api.CodeUnauthenticated)
	if got := f.tokenAudit(t, tok.ID); got[len(got)-1] != "token.use_denied denied expired" {
		t.Errorf("the audit log of the expired token is %q", got)
	}

	for _, h := range []http.Header{
		{"Authorization": {"Bearer scp_not-a-token"}},
		{"Authorization": {"bearer " + strings.Replace(tok.Secret, "scp_", "scp_x", 1)}},
	} {
		res := call(f.s.main, http.MethodGet, projectPath(p), nil, with(h, owner.header(false)))
		wantCode(t, fmt.Sprintf("%v and a session", h), res, http.StatusUnauthorized, api.CodeUnauthenticated)
		if !strings.Contains(res.header.Get("WWW-Authenticate"), "invalid_token") {
			t.Errorf("%v: WWW-Authenticate %q", h, res.header.Get("WWW-Authenticate"))
		}
	}
	// A credential that is no API token, as a proxy's basic authentication,
	// leaves the request to its session.
	for _, h := range []http.Header{
		{"Authorization": {"Basic YWRtaW46c2VjcmV0"}},
		{"Authorization": {"Bearer some-proxy-token"}},
	} {
		projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, with(h, owner.header(false))),
			http.StatusOK)
		wantCode(t, fmt.Sprintf("%v without a session", h), call(f.s.main, http.MethodGet, projectPath(p), nil, h),
			http.StatusUnauthorized, api.CodeUnauthenticated)
	}
	// A token needs no CSRF token, and a session still does.
	write := makeToken(t, f.s.main, owner, workspaceTokensPath(f.ws), "project:write")
	projectFrom(t, call(f.s.main, http.MethodPatch, projectPath(p), jsonBody(t, map[string]string{"name": "Quay"}),
		bearer(write.Secret)), http.StatusOK)
	wantCode(t, "a session's change without its CSRF token", call(f.s.main, http.MethodPatch, projectPath(p),
		jsonBody(t, map[string]string{"name": "Quay"}), owner.header(false)), http.StatusForbidden, api.CodeCSRFFailed)

	// An expiry finer than the database's microseconds, in another offset,
	// is answered as the database holds it, as the list and the audit log
	// show it.
	fine := time.Now().Add(time.Hour).Truncate(time.Second).Add(123456789 * time.Nanosecond).
		In(time.FixedZone("", 2*60*60))
	precise := createdToken(t, call(f.s.main, http.MethodPost, workspaceTokensPath(f.ws), jsonBody(t, map[string]any{
		"name": "CI", "scopes": []string{"snapshot:read"}, "expires_at": fine}), owner.header(true)))
	stored := fine.Truncate(time.Microsecond)
	if precise.ExpiresAt == nil || !precise.ExpiresAt.Equal(stored) {
		t.Errorf("the token made to expire at %v expires at %v, want %v", fine, precise.ExpiresAt, stored)
	}
	var page struct {
		Items []APITokenBody `json:"items"`
	}
	if err := json.Unmarshal(call(f.s.main, http.MethodGet, workspaceTokensPath(f.ws), nil, owner.header(false)).body,
		&page); err != nil {
		t.Fatal(err)
	}
	for _, listed := range page.Items {
		if listed.ID == precise.ID && (listed.ExpiresAt == nil || !listed.ExpiresAt.Equal(*precise.ExpiresAt)) {
			t.Errorf("the list shows the token to expire at %v, and making it %v", listed.ExpiresAt,
				precise.ExpiresAt)
		}
	}
	if n := f.count(t, "SELECT count(*) FROM audit_entry WHERE action = 'token.create' AND subject_id = $1 "+
		"AND metadata->>'expires_at' = $2", decode(t, ids.APIToken, precise.ID),
		stored.UTC().Format(time.RFC3339Nano)); n != 1 {
		t.Errorf("%d entries of the token's making record its expiry as %s", n, stored.UTC().Format(time.RFC3339Nano))
	}
}

// TestTokenRateLimit checks that a request made with an API token counts
// against the token, as well as against its owner and the client's IP
// address (NFR-SEC-06): on a route with a rate limit, here a test's own, of
// a bucket that allows a token two requests, a token's third is refused with
// 429 rate_limited, while its owner's session, and another token of theirs,
// still make requests, whose answers report the limit of the key with the
// fewest left. The server's buckets allow a token as much as a user.
//
// Verifies: NFR-SEC-05
func TestTokenRateLimit(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Lighthouse")
	policy := ratelimit.DefaultPolicy()
	policy.Rates[ratelimit.Read] = ratelimit.Rate{Period: time.Hour,
		Limits: map[ratelimit.Kind]int{ratelimit.IP: 100, ratelimit.User: 100, ratelimit.Token: 2}}
	limits := ratelimit.New(readyDB{s.ready}, policy)
	api.Register(s.main, api.ReadProject, huma.Operation{OperationID: "readLimited", Method: http.MethodGet,
		Path: "/api/v1/projects/{project_id}/limited", Middlewares: huma.Middlewares{rateLimit(limits, ratelimit.Read)}},
		func(context.Context, *projectInput) (*statusOutput, error) {
			return &statusOutput{Body: statusBody{Status: "ok"}}, nil
		}, api.TokenScope(scope.ProjectRead))
	path := projectPath(p.ID) + "/limited"
	first := makeToken(t, s.main, owner, workspaceTokensPath(ws), "project:read")
	second := makeToken(t, s.main, owner, workspaceTokensPath(ws), "project:read")
	// The requests must fall in one window of an hour.
	d.AwaitWindow(t, time.Hour, time.Minute)
	for i := range 2 {
		res := call(s.main, http.MethodGet, path, nil, bearer(first.Secret))
		if res.status != http.StatusOK || headerInt(res, limitHeader) != 2 || headerInt(res, remainingHeader) != 1-i {
			t.Fatalf("the token's request %d: %d %s %v", i+1, res.status, res.body, res.header)
		}
	}
	res := call(s.main, http.MethodGet, path, nil, bearer(first.Secret))
	if res.status != http.StatusTooManyRequests || res.code() != api.CodeRateLimited ||
		res.details(t)["bucket"] != "read" || headerInt(res, "Retry-After") < 1 {
		t.Errorf("the token's third request: %d %s %v", res.status, res.body, res.header)
	}
	if res := call(s.main, http.MethodGet, path, nil, bearer(second.Secret)); res.status != http.StatusOK ||
		headerInt(res, remainingHeader) != 1 {
		t.Errorf("the owner's other token: %d %s %v", res.status, res.body, res.header)
	}
	// The owner made 4 requests, and a session counts against no token.
	if res := call(s.main, http.MethodGet, path, nil, owner.header(false)); res.status != http.StatusOK ||
		headerInt(res, limitHeader) != 100 || headerInt(res, remainingHeader) != 95 {
		t.Errorf("the owner's session: %d %s %v", res.status, res.body, res.header)
	}
	for b, r := range ratelimit.DefaultPolicy().Rates {
		if n := r.Limits[ratelimit.User]; n != 0 && r.Limits[ratelimit.Token] != n {
			t.Errorf("the %s bucket allows a token %d requests, and a user %d", b, r.Limits[ratelimit.Token], n)
		}
	}
}

// TestTokenCreationRateLimit checks that making API tokens shares the auth
// bucket's limit of 10 a minute for each account, from any IP address
// (plan note N23), and that the 11th is refused with 429 rate_limited.
//
// Verifies: NFR-SEC-06
func TestTokenCreationRateLimit(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Lighthouse")
	// The 11 requests must fall in one window of a minute.
	awaitWindow(t, d, windowNeed)
	for i := range 10 {
		path := workspaceTokensPath(ws)
		if i%2 == 1 {
			path = projectTokensPath(p.ID)
		}
		res := call(s.main, http.MethodPost, path, tokenRequest(t, "snapshot:read"), owner.header(true))
		if res.status != http.StatusCreated || headerInt(res, limitHeader) != 10 || headerInt(res, remainingHeader) !=
			9-i {
			t.Fatalf("token %d: %d %s %v", i+1, res.status, res.body, res.header)
		}
	}
	res := call(s.main, http.MethodPost, workspaceTokensPath(ws), tokenRequest(t, "snapshot:read"),
		owner.header(true))
	if res.status != http.StatusTooManyRequests || res.code() != api.CodeRateLimited ||
		res.details(t)["bucket"] != "auth" {
		t.Errorf("the 11th token: %d %s %v", res.status, res.body, res.header)
	}
}
