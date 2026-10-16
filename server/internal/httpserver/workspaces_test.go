package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

const workspacesPath = "/api/v1/workspaces"

// workspacePage is a page of GET /api/v1/workspaces.
type workspacePage struct {
	Items      []membershipBody `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

// workspacesOf returns the first page of the workspaces of si's account.
func workspacesOf(t *testing.T, h http.Handler, si signedIn) []membershipBody {
	t.Helper()
	res := send(h, http.MethodGet, workspacesPath, nil, "", si.header(false))
	var page workspacePage
	if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
		t.Fatalf("list the workspaces: %d %s", res.status, res.body)
	}
	return page.Items
}

// personalWorkspace returns the personal workspace of si's account, which
// must belong to no other workspace.
func personalWorkspace(t *testing.T, h http.Handler, si signedIn) membershipBody {
	t.Helper()
	got := workspacesOf(t, h, si)
	if len(got) != 1 {
		t.Fatalf("the account has %d workspaces, want its personal one: %+v", len(got), got)
	}
	return got[0]
}

// workspaceFrom returns the workspace of a response with status.
func workspaceFrom(t *testing.T, res response, status int) workspaceBody {
	t.Helper()
	var w workspaceBody
	if res.status != status || json.Unmarshal(res.body, &w) != nil {
		t.Fatalf("%d %s, want %d and a workspace", res.status, res.body, status)
	}
	return w
}

// jsonBody returns v as JSON.
func jsonBody(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// newNamedAccount registers an account with a display name and
// testPassword, and signs it in.
func newNamedAccount(t *testing.T, h http.Handler, email, name string) signedIn {
	t.Helper()
	if res := send(h, http.MethodPost, registerPath, registration(t, email, name, testPassword), "",
		nil); res.status != http.StatusAccepted {
		t.Fatalf("register %s: %d %s", email, res.status, res.body)
	}
	return signIn(t, h, email)
}

// decode returns the UUID of a public ID of kind.
func decode(t *testing.T, kind ids.Kind, id string) ids.UUID {
	t.Helper()
	u, err := ids.Decode(kind, id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// join makes si's account a member of the workspace with role, as the
// superuser.
func join(t *testing.T, d *dbtest.Database, si signedIn, workspaceID, role string) {
	t.Helper()
	if _, err := d.Admin(t).Exec(context.Background(),
		"INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, $4)", ids.New(),
		decode(t, ids.Workspace, workspaceID), decode(t, ids.User, si.body.UserID), role); err != nil {
		t.Fatal(err)
	}
}

// TestPersonalWorkspace checks that an account's first sign-in gives it a
// personal workspace that it owns, named after its display name, with a slug
// derived from it, and that later sign-ins give it no other, nor one to an
// account that belonged to a workspace before it first signed in.
func TestPersonalWorkspace(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, openSignup)
	newAccount(t, s.main, "jo@example.com")
	si := signedInFrom(t, send(s.main, http.MethodPost, loginPath, credentials(t, "jo@example.com", testPassword),
		"", http.Header{"Accept-Language": {"en-GB"}}))
	signIn(t, s.main, "jo@example.com")
	signIn(t, s.main, "jo@example.com")
	w := personalWorkspace(t, s.main, si)
	if w.Name != "Jo" || w.Slug != "jo" || w.Role != "owner" || w.DefaultLocale != "en" ||
		!strings.HasPrefix(w.ID, "ws_") || !strings.HasPrefix(w.MembershipID, "mem_") {
		t.Errorf("the personal workspace is %+v", w)
	}
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry
		WHERE action = 'workspace.create' AND result = 'success' AND metadata->>'personal' = 'true'
			AND workspace_id = subject_id AND actor_user_id IS NOT NULL`); n != 1 {
		t.Errorf("%d entries of the personal workspace's creation, want 1", n)
	}

	// Another Jo gets the next slug, and a name in another script a slug of
	// its own.
	mo := newNamedAccount(t, s.main, "mo@example.com", "Jo")
	if w := personalWorkspace(t, s.main, mo); w.Name != "Jo" || w.Slug != "jo-2" {
		t.Errorf("the second Jo's workspace is %+v", w)
	}
	for name, slug := range map[string]string{"Tomáš Novák": "tomas-novak", "山田 太郎": "workspace",
		"Admin": "admin-2"} {
		si := newNamedAccount(t, s.main, url.QueryEscape(name)+"@example.com", name)
		if w := personalWorkspace(t, s.main, si); w.Name != name || w.Slug != slug {
			t.Errorf("%s's workspace is %+v, want the slug %s", name, w, slug)
		}
	}

	// An account that is a member of a workspace before its first sign-in
	// still gets a personal one (plan note N22).
	if res := send(s.main, http.MethodPost, registerPath, registration(t, "ann@example.com", "Ann", testPassword), "",
		nil); res.status != http.StatusAccepted {
		t.Fatalf("register: %d %s", res.status, res.body)
	}
	if _, err := d.Admin(t).Exec(context.Background(), `INSERT INTO membership (id, workspace_id, user_id, role)
		SELECT $1, $2, id, 'editor' FROM "user" WHERE email = 'ann@example.com'`, ids.New(),
		decode(t, ids.Workspace, w.ID)); err != nil {
		t.Fatal(err)
	}
	ann := signIn(t, s.main, "ann@example.com")
	got := workspacesOf(t, s.main, ann)
	if len(got) != 2 || got[0].Role != "owner" && got[1].Role != "owner" {
		t.Errorf("the invited account's workspaces are %+v, want Jo's and its own", got)
	}
}

// TestCreateWorkspace checks that creating a workspace makes the user who
// creates it its owner, in the database and in the audit log, and that the
// workspace is theirs to read.
//
// Verifies: FR-TEN-01/AC2, FR-TEN-07/AC2
func TestCreateWorkspace(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, openSignup)
	newAccount(t, s.main, "jo@example.com")
	si := signIn(t, s.main, "jo@example.com")
	res := send(s.main, http.MethodPost, workspacesPath, jsonBody(t, map[string]string{
		"name": "  Harbour Team ", "slug": "harbour", "default_locale": "en-US",
	}), "", si.header(true))
	created := workspaceFrom(t, res, http.StatusCreated)
	if created.Name != "Harbour Team" || created.Slug != "harbour" || created.DefaultLocale != "en" ||
		created.Role != "owner" || res.header.Get("Location") != workspacesPath+"/"+created.ID {
		t.Errorf("created %+v, Location %q", created, res.header.Get("Location"))
	}
	got := workspaceFrom(t, send(s.main, http.MethodGet, workspacesPath+"/"+created.ID, nil, "", si.header(false)),
		http.StatusOK)
	if got != created {
		t.Errorf("GET returns %+v, want %+v", got, created)
	}
	if n := countRows(t, d, `SELECT count(*) FROM membership m JOIN "user" u ON u.id = m.user_id
		WHERE m.workspace_id = $1 AND u.email = 'jo@example.com' AND m.role = 'owner'`,
		decode(t, ids.Workspace, created.ID)); n != 1 {
		t.Errorf("the creator has %d owner memberships of the workspace, want 1", n)
	}
	if n := countRows(t, d, `SELECT count(*) FROM membership WHERE workspace_id = $1`,
		decode(t, ids.Workspace, created.ID)); n != 1 {
		t.Errorf("the workspace has %d members, want its creator", n)
	}
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry
		WHERE action = 'workspace.create' AND workspace_id = $1 AND subject_id = $1 AND subject_kind = 'workspace'
			AND metadata->>'personal' = 'false' AND user_agent IS NULL AND host(ip) = '192.0.2.1'`,
		decode(t, ids.Workspace, created.ID)); n != 1 {
		t.Errorf("%d entries of the workspace's creation, want 1", n)
	}

	// Without a slug, it is derived from the name; without a locale, the
	// request's is the workspace's.
	derived := workspaceFrom(t, send(s.main, http.MethodPost, workspacesPath, jsonBody(t, map[string]string{
		"name": "Harbour",
	}), "", with(si.header(true), http.Header{"Accept-Language": {"cs, en;q=0.5"}})), http.StatusCreated)
	if derived.Slug != "harbour-2" || derived.DefaultLocale != "en" {
		t.Errorf("derived %+v", derived)
	}
	if got := workspacesOf(t, s.main, si); len(got) != 3 {
		t.Errorf("the account has %d workspaces, want its personal one and the two it created", len(got))
	}
}

// with returns h with the headers of more added.
func with(h, more http.Header) http.Header {
	out := maps.Clone(h)
	maps.Copy(out, more)
	return out
}

// TestWorkspaceSlugs checks that a slug is unique on the instance, and that
// one of the wrong form or a reserved word is refused, as a workspace is
// created and as its slug changes, and that nothing changes then.
//
// Verifies: FR-TEN-01/AC3
func TestWorkspaceSlugs(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, openSignup)
	newAccount(t, s.main, "jo@example.com")
	si := signIn(t, s.main, "jo@example.com")
	mine := workspaceFrom(t, send(s.main, http.MethodPost, workspacesPath, jsonBody(t, map[string]string{
		"name": "Mine", "slug": "mine",
	}), "", si.header(true)), http.StatusCreated)
	mo := newNamedAccount(t, s.main, "mo@example.com", "Mo")
	before := countRows(t, d, `SELECT count(*) FROM workspace`)

	for _, c := range []struct {
		slug   string
		code   api.Code
		reason string
	}{
		{"mine", api.CodeSlugTaken, ""},
		{"jo", api.CodeSlugTaken, ""},
		{"admin", api.CodeSlugInvalid, "reserved"},
		{"api", api.CodeSlugInvalid, "reserved"},
		{"con", api.CodeSlugInvalid, "reserved"},
		{"lpt1", api.CodeSlugInvalid, "reserved"},
		{"Mine", api.CodeSlugInvalid, "format"},
		{"my_team", api.CodeSlugInvalid, "format"},
		{"my--team", api.CodeSlugInvalid, "format"},
		{"-team", api.CodeSlugInvalid, "format"},
		{"team-", api.CodeSlugInvalid, "format"},
		{"tým", api.CodeSlugInvalid, "format"},
		{"my team", api.CodeSlugInvalid, "format"},
		{strings.Repeat("a", 65), api.CodeSlugInvalid, "format"},
	} {
		for _, r := range []struct {
			method, path string
		}{
			{http.MethodPost, workspacesPath},
			{http.MethodPatch, workspacesPath + "/" + mine.ID},
		} {
			body := map[string]string{"name": "Team", "slug": c.slug}
			res := send(s.main, r.method, r.path, jsonBody(t, body), "", si.header(true))
			if c.slug == "mine" && r.method == http.MethodPatch {
				// The workspace's own slug.
				if res.status != http.StatusOK {
					t.Errorf("%s %q: %d %s", r.method, c.slug, res.status, res.body)
				}
				continue
			}
			if res.code() != c.code || res.status != c.code.Status() {
				t.Errorf("%s %q: %d %s, want %s", r.method, c.slug, res.status, res.body, c.code)
				continue
			}
			if c.reason != "" && res.details(t)["reason"] != c.reason {
				t.Errorf("%s %q: the reason is %v, want %s", r.method, c.slug, res.details(t)["reason"], c.reason)
			}
		}
	}
	if n := countRows(t, d, `SELECT count(*) FROM workspace`); n != before {
		t.Errorf("%d workspaces, want %d", n, before)
	}
	got := workspaceFrom(t, send(s.main, http.MethodGet, workspacesPath+"/"+mine.ID, nil, "", si.header(false)),
		http.StatusOK)
	if got.Slug != "mine" || got.Name != "Team" {
		t.Errorf("the workspace is %+v, want its slug unchanged", got)
	}

	// A slug that is free is the workspace's, and its old one is free for
	// another.
	if w := workspaceFrom(t, send(s.main, http.MethodPatch, workspacesPath+"/"+mine.ID, jsonBody(t, map[string]string{
		"slug": "ours-2026",
	}), "", si.header(true)), http.StatusOK); w.Slug != "ours-2026" || !w.UpdatedAt.After(mine.UpdatedAt) {
		t.Errorf("after the change, %+v", w)
	}
	if w := workspaceFrom(t, send(s.main, http.MethodPost, workspacesPath, jsonBody(t, map[string]string{
		"name": "Mine", "slug": "mine",
	}), "", mo.header(true)), http.StatusCreated); w.Slug != "mine" {
		t.Errorf("the freed slug: %+v", w)
	}
}

// TestWorkspaceFields checks that a name or a default locale that is not
// valid is refused with validation_failed, and that a change that leaves
// out a field leaves it as it is.
func TestWorkspaceFields(t *testing.T) {
	t.Parallel()
	s, _ := readyServer(t, openSignup)
	newAccount(t, s.main, "jo@example.com")
	si := signIn(t, s.main, "jo@example.com")
	w := personalWorkspace(t, s.main, si)
	for _, c := range []struct {
		body     map[string]any
		location string
	}{
		{map[string]any{"name": "   "}, "body.name"},
		{map[string]any{"name": "​"}, "body.name"},
		{map[string]any{"name": "Jo‮oJ"}, "body.name"},
		{map[string]any{"name": strings.Repeat("ž", 101)}, "body.name"},
		{map[string]any{"name": "Team", "default_locale": "fr"}, "body.default_locale"},
		{map[string]any{"name": "Team", "default_locale": "not a tag"}, "body.default_locale"},
	} {
		for _, r := range []struct{ method, path string }{
			{http.MethodPost, workspacesPath},
			{http.MethodPatch, workspacesPath + "/" + w.ID},
		} {
			res := send(s.main, r.method, r.path, jsonBody(t, c.body), "", si.header(true))
			if res.status != http.StatusUnprocessableEntity || res.code() != api.CodeValidationFailed ||
				!strings.Contains(string(res.body), `"location":"`+c.location+`"`) {
				t.Errorf("%s %v: %d %s, want validation_failed at %s", r.method, c.body, res.status, res.body,
					c.location)
			}
		}
	}
	got := workspaceFrom(t, send(s.main, http.MethodPatch, workspacesPath+"/"+w.ID, jsonBody(t, map[string]any{
		"name": " Harbour ",
	}), "", si.header(true)), http.StatusOK)
	if got.Name != "Harbour" || got.Slug != w.Slug || got.DefaultLocale != w.DefaultLocale || got.Role != "owner" {
		t.Errorf("after renaming, %+v", got)
	}
	for _, body := range []string{`{}`, `{"name": null, "slug": null, "default_locale": null}`} {
		same := workspaceFrom(t, send(s.main, http.MethodPatch, workspacesPath+"/"+w.ID, []byte(body), "",
			si.header(true)), http.StatusOK)
		if same != got {
			t.Errorf("the change %s gives %+v, want %+v", body, same, got)
		}
	}
}

// TestWorkspaceRoles checks who may read a workspace and change its
// settings: every role but guest reads it, and only owner and admin change
// it; anyone else with a role is refused with insufficient_role, and nothing
// changes.
func TestWorkspaceRoles(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, openSignup)
	newAccount(t, s.main, "jo@example.com")
	owner := signIn(t, s.main, "jo@example.com")
	w := personalWorkspace(t, s.main, owner)
	for i, role := range []string{"admin", "editor", "commenter", "viewer"} {
		si := newNamedAccount(t, s.main, fmt.Sprintf("%s@example.com", role), "Member")
		join(t, d, si, w.ID, role)
		got := workspaceFrom(t, send(s.main, http.MethodGet, workspacesPath+"/"+w.ID, nil, "", si.header(false)),
			http.StatusOK)
		if got.Role != role || got.ID != w.ID {
			t.Errorf("%s reads %+v", role, got)
		}
		name := fmt.Sprintf("Renamed %d", i)
		res := send(s.main, http.MethodPatch, workspacesPath+"/"+w.ID, jsonBody(t, map[string]string{"name": name}),
			"", si.header(true))
		switch role {
		case "admin":
			if got := workspaceFrom(t, res, http.StatusOK); got.Name != name || got.Role != "admin" {
				t.Errorf("admin renames to %+v", got)
			}
		default:
			if res.status != http.StatusForbidden || res.code() != api.CodeInsufficientRole {
				t.Errorf("%s renames: %d %s, want insufficient_role", role, res.status, res.body)
			}
			if n := countRows(t, d, `SELECT count(*) FROM workspace WHERE name = $1`, name); n != 0 {
				t.Errorf("%s renamed the workspace", role)
			}
		}
	}
}

// TestWorkspaceNotFound checks that a user whose role in a workspace is
// none, as one who is not a member, or is a guest, is answered with 404 on
// every workspace route, as for a workspace that does not exist, never 403
// or 500, and that an instance admin is such a user unless
// STORYCREEP_INSTANCE_ADMIN_ACCESS is on.
//
// Verifies: FR-TEN-04/AC6
func TestWorkspaceNotFound(t *testing.T) {
	t.Parallel()
	for _, access := range []string{"off", "on"} {
		env := maps.Clone(openSignup)
		env["STORYCREEP_INSTANCE_ADMIN_ACCESS"] = access
		s, d := readyServer(t, env)
		newAccount(t, s.main, "jo@example.com")
		owner := signIn(t, s.main, "jo@example.com")
		w := personalWorkspace(t, s.main, owner)
		stranger := newNamedAccount(t, s.main, "stranger@example.com", "Stranger")
		guest := newNamedAccount(t, s.main, "guest@example.com", "Guest")
		join(t, d, guest, w.ID, "guest")
		admin := newNamedAccount(t, s.main, "admin@example.com", "Admin")
		if _, err := d.Admin(t).Exec(context.Background(), `UPDATE "user" SET is_instance_admin = true WHERE id = $1`,
			decode(t, ids.User, admin.body.UserID)); err != nil {
			t.Fatal(err)
		}
		missing := ids.Encode(ids.Workspace, ids.New())

		type attempt struct {
			who  string
			si   signedIn
			path string
		}
		attempts := []attempt{
			{"a user who is not a member", stranger, w.ID},
			{"a guest", guest, w.ID},
			{"the owner, of a workspace that does not exist", owner, missing},
		}
		if access == "off" {
			attempts = append(attempts, attempt{"an instance admin", admin, w.ID})
		}
		for _, a := range attempts {
			for _, r := range []struct {
				method string
				body   []byte
			}{
				{http.MethodGet, nil},
				{http.MethodPatch, []byte(`{"name": "Taken"}`)},
				{http.MethodPatch, []byte(`{"name": ""}`)},
			} {
				res := send(s.main, r.method, workspacesPath+"/"+a.path, r.body, "", a.si.header(true))
				if res.status != http.StatusNotFound || res.code() != api.CodeNotFound {
					t.Errorf("access %s, %s, %s %s: %d %s, want not_found", access, a.who, r.method, r.body,
						res.status, res.body)
				}
			}
		}
		if n := countRows(t, d, `SELECT count(*) FROM workspace WHERE name = 'Taken'`); n != 0 {
			t.Errorf("access %s: a workspace was renamed", access)
		}
		// A guest's membership is listed, as the guest acts on the projects
		// granted to them there.
		if got := workspacesOf(t, s.main, guest); len(got) != 2 || got[0].Role != "guest" {
			t.Errorf("access %s: the guest's workspaces are %+v", access, got)
		}

		if access == "on" {
			got := workspaceFrom(t, send(s.main, http.MethodGet, workspacesPath+"/"+w.ID, nil, "",
				admin.header(false)), http.StatusOK)
			if got.Role != "owner" {
				t.Errorf("an instance admin with access reads %+v", got)
			}
			if got := workspaceFrom(t, send(s.main, http.MethodPatch, workspacesPath+"/"+w.ID,
				[]byte(`{"name": "Rescued"}`), "", admin.header(true)), http.StatusOK); got.Name != "Rescued" {
				t.Errorf("an instance admin with access renames to %+v", got)
			}
			if got := workspacesOf(t, s.main, admin); len(got) != 1 {
				t.Errorf("an instance admin lists %d workspaces, want only its own", len(got))
			}
		}
	}
}

// TestListWorkspaces checks that the list of an account's workspaces holds
// each of them once, in pages, oldest first, and none of another account's
// or of one whose deletion is pending.
func TestListWorkspaces(t *testing.T) {
	t.Parallel()
	s, d := readyServer(t, openSignup)
	newAccount(t, s.main, "jo@example.com")
	si := signIn(t, s.main, "jo@example.com")
	want := []string{personalWorkspace(t, s.main, si).ID}
	for i := range 4 {
		w := workspaceFrom(t, send(s.main, http.MethodPost, workspacesPath, jsonBody(t, map[string]string{
			"name": fmt.Sprintf("Team %d", i),
		}), "", si.header(true)), http.StatusCreated)
		want = append(want, w.ID)
	}
	newNamedAccount(t, s.main, "mo@example.com", "Mo")
	if _, err := d.Admin(t).Exec(context.Background(),
		`UPDATE workspace SET status = 'pending_deletion', deleted_at = now() WHERE id = $1`,
		decode(t, ids.Workspace, want[2])); err != nil {
		t.Fatal(err)
	}
	want = append(want[:2], want[3:]...)

	var got []string
	path := workspacesPath + "?limit=2"
	for range 10 {
		res := send(s.main, http.MethodGet, path, nil, "", si.header(false))
		var page workspacePage
		if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
			t.Fatalf("%s: %d %s", path, res.status, res.body)
		}
		for _, w := range page.Items {
			got = append(got, w.ID)
			if w.Role != "owner" {
				t.Errorf("%s has the role %s", w.Name, w.Role)
			}
		}
		if page.NextCursor == nil {
			break
		}
		path = workspacesPath + "?limit=2&cursor=" + url.QueryEscape(*page.NextCursor)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the pages list %v, want %v", got, want)
	}
	if res := send(s.main, http.MethodGet, workspacesPath+"/"+want[0], nil, "", nil); res.status !=
		http.StatusUnauthorized {
		t.Errorf("without a session: %d %s", res.status, res.body)
	}
	if res := send(s.main, http.MethodGet, workspacesPath+"/"+si.body.UserID, nil, "",
		si.header(false)); res.code() != api.CodeInvalidID {
		t.Errorf("a user's ID for a workspace's: %d %s", res.status, res.body)
	}
}
