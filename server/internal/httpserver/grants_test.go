package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// grantsPath returns the path of the grants on the project, grantPath the
// path of the membership's grant on it, and memberGrantsPath the path of the
// grants of the membership of the workspace ws.
func grantsPath(project string) string              { return projectPath(project) + "/grants" }
func grantPath(project, membership string) string   { return grantsPath(project) + "/" + membership }
func memberGrantsPath(ws, membership string) string { return memberPath(ws, membership) + "/grants" }

// setGrant gives the membership the role on the project, as si.
func setGrant(t *testing.T, h http.Handler, si signedIn, project, membership, role string) response {
	t.Helper()
	return call(h, http.MethodPut, grantPath(project, membership), jsonBody(t, map[string]string{"role": role}),
		si.header(true))
}

// grantFrom returns the grant of a response, which must be 200.
func grantFrom(t *testing.T, res response) grantBody {
	t.Helper()
	var g grantBody
	if res.status != http.StatusOK || json.Unmarshal(res.body, &g) != nil {
		t.Fatalf("%d %s, want 200 and a grant", res.status, res.body)
	}
	return g
}

// grantsAt returns the grants of the list at path, every page of it, which
// si reads, as "project membership role".
func grantsAt(t *testing.T, h http.Handler, si signedIn, path string) []string {
	t.Helper()
	var out []string
	next := path + "?limit=1"
	for range 10 {
		res := call(h, http.MethodGet, next, nil, si.header(false))
		var page struct {
			Items      []grantBody `json:"items"`
			NextCursor *string     `json:"next_cursor"`
		}
		if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
			t.Fatalf("list %s: %d %s", next, res.status, res.body)
		}
		for _, g := range page.Items {
			out = append(out, g.ProjectID+" "+g.MembershipID+" "+g.Role)
		}
		if page.NextCursor == nil {
			return out
		}
		next = path + "?limit=1&cursor=" + url.QueryEscape(*page.NextCursor)
	}
	t.Fatalf("list %s: more than 10 pages", path)
	return nil
}

// grantAudit returns the audit entries of the workspace ws whose subject is
// the membership, in the order of their IDs, as "action project role
// previous_role by actor".
func grantAudit(t *testing.T, d *dbtest.Database, ws, membership string) []string {
	t.Helper()
	rows, err := d.Admin(t).Query(context.Background(), `SELECT action, coalesce(metadata->>'project_id', ''),
		coalesce(metadata->>'role', ''), coalesce(metadata->>'previous_role', ''), coalesce(actor_user_id::text, '')
		FROM audit_entry WHERE workspace_id = $1 AND subject_id = $2 AND action LIKE 'project_grant.%' ORDER BY id`,
		decode(t, ids.Workspace, ws), decode(t, ids.Membership, membership))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var action, project, role, previous, actor string
		if err := rows.Scan(&action, &project, &role, &previous, &actor); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %s %s %s by %s", action, project, role, previous, actor))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestProjectGrants checks a guest's grants through the API: a guest sees
// only the projects that an owner grants them, with the grant's role, and is
// answered with 404 on the others, and once the grant is removed, on that
// one too. Setting a grant adds it or changes its role, and one with the
// role already changes nothing; an archived project's grants change too,
// and those of a project in the trash are kept, unlisted, until it is
// restored. Each change is in the audit log, and removing a membership
// removes its grants.
//
// Verifies: FR-TEN-04/AC2, FR-TEN-07/AC2
func TestProjectGrants(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	harbour := createProject(t, s.main, owner, ws, "Harbour")
	lighthouse := createProject(t, s.main, owner, ws, "Lighthouse")
	guest := newNamedAccount(t, s.main, "gu@example.com", "Gu")
	join(t, d, guest, ws, "guest")
	guests := membershipOf(t, membersOf(t, s.main, owner, ws), guest)

	if got := projectIDs(t, s.main, guest, projectsPath(ws)); len(got) != 0 {
		t.Errorf("a guest without grants lists %v", got)
	}
	wantCode(t, "a guest without a grant reads a project", call(s.main, http.MethodGet, projectPath(harbour.ID), nil,
		guest.header(false)), http.StatusNotFound, api.CodeNotFound)

	g := grantFrom(t, setGrant(t, s.main, owner, harbour.ID, guests, "editor"))
	if g.ProjectID != harbour.ID || g.MembershipID != guests || g.UserID != guest.body.UserID || g.Role != "editor" ||
		g.CreatedAt.IsZero() || !g.UpdatedAt.Equal(g.CreatedAt) {
		t.Errorf("the new grant is %+v", g)
	}
	if got := projectIDs(t, s.main, guest, projectsPath(ws)); !slices.Equal(got, []string{harbour.ID}) {
		t.Errorf("the guest lists %v, want only the granted project", got)
	}
	if got := projectFrom(t, call(s.main, http.MethodGet, projectPath(harbour.ID), nil, guest.header(false)),
		http.StatusOK); got.Role != "editor" {
		t.Errorf("the guest's role on the granted project is %q, want editor", got.Role)
	}
	wantCode(t, "a guest reads a project not granted to them", call(s.main, http.MethodGet,
		projectPath(lighthouse.ID), nil, guest.header(false)), http.StatusNotFound, api.CodeNotFound)

	if again := grantFrom(t, setGrant(t, s.main, owner, harbour.ID, guests, "editor")); again != g {
		t.Errorf("the grant set again with its role is %+v, want %+v", again, g)
	}
	if changed := grantFrom(t, setGrant(t, s.main, owner, harbour.ID, guests, "viewer")); changed.Role != "viewer" ||
		!changed.CreatedAt.Equal(g.CreatedAt) || !changed.UpdatedAt.After(g.UpdatedAt) {
		t.Errorf("the changed grant is %+v, from %+v", changed, g)
	}
	projectFrom(t, call(s.main, http.MethodPost, projectPath(harbour.ID)+"/archive", nil, owner.header(true)),
		http.StatusOK)
	if changed := grantFrom(t, setGrant(t, s.main, owner, harbour.ID, guests, "commenter")); changed.Role !=
		"commenter" {
		t.Errorf("the grant on the archived project is %+v", changed)
	}
	grantFrom(t, setGrant(t, s.main, owner, lighthouse.ID, guests, "admin"))
	want := []string{harbour.ID + " " + guests + " commenter"}
	if got := grantsAt(t, s.main, owner, grantsPath(harbour.ID)); !slices.Equal(got, want) {
		t.Errorf("the project's grants are %q, want %q", got, want)
	}
	if got := grantsAt(t, s.main, owner, memberGrantsPath(ws, guests)); !slices.Equal(got,
		append(want, lighthouse.ID+" "+guests+" admin")) {
		t.Errorf("the member's grants are %q", got)
	}

	// The trash keeps the grant, but nothing finds it there.
	if res := call(s.main, http.MethodDelete, projectPath(lighthouse.ID), nil, owner.header(true)); res.status !=
		http.StatusNoContent {
		t.Fatalf("delete the project: %d %s", res.status, res.body)
	}
	if got := grantsAt(t, s.main, owner, memberGrantsPath(ws, guests)); !slices.Equal(got, want) {
		t.Errorf("the member's grants with one project in the trash are %q, want %q", got, want)
	}
	for what, res := range map[string]response{
		"list":   call(s.main, http.MethodGet, grantsPath(lighthouse.ID), nil, owner.header(false)),
		"set":    setGrant(t, s.main, owner, lighthouse.ID, guests, "viewer"),
		"remove": call(s.main, http.MethodDelete, grantPath(lighthouse.ID, guests), nil, owner.header(true)),
	} {
		wantCode(t, what+" the grants of a project in the trash", res, http.StatusNotFound, api.CodeNotFound)
	}
	projectFrom(t, call(s.main, http.MethodPost, projectPath(lighthouse.ID)+"/restore", nil, owner.header(true)),
		http.StatusOK)
	if got := projectFrom(t, call(s.main, http.MethodGet, projectPath(lighthouse.ID), nil, guest.header(false)),
		http.StatusOK); got.Role != "admin" {
		t.Errorf("the guest's role on the restored project is %q, want admin", got.Role)
	}

	if res := call(s.main, http.MethodDelete, grantPath(harbour.ID, guests), nil, owner.header(true)); res.status !=
		http.StatusNoContent {
		t.Fatalf("remove the grant: %d %s", res.status, res.body)
	}
	wantCode(t, "a guest reads a project whose grant was removed", call(s.main, http.MethodGet,
		projectPath(harbour.ID), nil, guest.header(false)), http.StatusNotFound, api.CodeNotFound)
	if got := projectIDs(t, s.main, guest, projectsPath(ws)); !slices.Equal(got, []string{lighthouse.ID}) {
		t.Errorf("the guest lists %v, want only the project still granted", got)
	}
	wantCode(t, "remove a grant that is gone", call(s.main, http.MethodDelete, grantPath(harbour.ID, guests), nil,
		owner.header(true)), http.StatusNotFound, api.CodeNotFound)

	actor := decode(t, ids.User, owner.body.UserID).String()
	h, l := decode(t, ids.Project, harbour.ID).String(), decode(t, ids.Project, lighthouse.ID).String()
	if got, want := grantAudit(t, d, ws, guests), []string{
		"project_grant.create " + h + " editor  by " + actor,
		"project_grant.change " + h + " viewer editor by " + actor,
		"project_grant.change " + h + " commenter viewer by " + actor,
		"project_grant.create " + l + " admin  by " + actor,
		"project_grant.remove " + h + " commenter  by " + actor,
	}; !slices.Equal(got, want) {
		t.Errorf("the grants' audit entries are\n%q, want\n%q", got, want)
	}

	if res := call(s.main, http.MethodDelete, memberPath(ws, guests), nil, owner.header(true)); res.status !=
		http.StatusNoContent {
		t.Fatalf("remove the guest: %d %s", res.status, res.body)
	}
	if n := countRows(t, d, `SELECT count(*) FROM project_grant WHERE membership_id = $1`,
		decode(t, ids.Membership, guests)); n != 0 {
		t.Errorf("the removed membership keeps %d grants", n)
	}
}

// TestGrantLowersOneProject checks that a grant changes its member's role on
// its project alone: an editor lowered to viewer on one project is still an
// editor on another, and an admin lowered to viewer on one may no longer
// change its settings, but still changes another's, and still does what
// their role in the workspace allows, as managing the grant that lowered
// them, which a grant never changes.
//
// Verifies: FR-TEN-04/AC1
func TestGrantLowersOneProject(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	x := createProject(t, s.main, owner, ws, "X")
	y := createProject(t, s.main, owner, ws, "Y")
	editor := newNamedAccount(t, s.main, "ed@example.com", "Ed")
	join(t, d, editor, ws, "editor")
	admin := newNamedAccount(t, s.main, "ad@example.com", "Ad")
	join(t, d, admin, ws, "admin")
	members := membersOf(t, s.main, owner, ws)
	editors, admins := membershipOf(t, members, editor), membershipOf(t, members, admin)
	grantFrom(t, setGrant(t, s.main, owner, x.ID, editors, "viewer"))
	grantFrom(t, setGrant(t, s.main, owner, x.ID, admins, "viewer"))

	for _, c := range []struct {
		who          string
		si           signedIn
		roleX, roleY string
	}{{"the editor", editor, "viewer", "editor"}, {"the admin", admin, "viewer", "admin"}} {
		for id, want := range map[string]string{x.ID: c.roleX, y.ID: c.roleY} {
			if got := projectFrom(t, call(s.main, http.MethodGet, projectPath(id), nil, c.si.header(false)),
				http.StatusOK); got.Role != want {
				t.Errorf("%s's role on %s is %q, want %q", c.who, got.Name, got.Role, want)
			}
		}
		res := call(s.main, http.MethodGet, projectsPath(ws), nil, c.si.header(false))
		var page struct {
			Items []projectBody `json:"items"`
		}
		if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
			t.Fatalf("list the projects: %d %s", res.status, res.body)
		}
		for _, p := range page.Items {
			if want := map[string]string{x.ID: c.roleX, y.ID: c.roleY}[p.ID]; p.Role != want {
				t.Errorf("%s lists %s with the role %q, want %q", c.who, p.Name, p.Role, want)
			}
		}
	}

	wantCode(t, "an admin lowered to viewer renames the project", call(s.main, http.MethodPatch, projectPath(x.ID),
		jsonBody(t, map[string]string{"name": "Renamed"}), admin.header(true)), http.StatusForbidden,
		api.CodeInsufficientRole)
	if got := projectFrom(t, call(s.main, http.MethodPatch, projectPath(y.ID),
		jsonBody(t, map[string]string{"name": "Renamed"}), admin.header(true)), http.StatusOK); got.Name != "Renamed" {
		t.Errorf("the admin renames the other project to %q", got.Name)
	}
	// Managing grants is the workspace's operation, which the admin's role
	// there authorises, whatever their grant on the project.
	if res := call(s.main, http.MethodDelete, grantPath(x.ID, admins), nil, admin.header(true)); res.status !=
		http.StatusNoContent {
		t.Fatalf("the admin removes their own grant: %d %s", res.status, res.body)
	}
	if got := projectFrom(t, call(s.main, http.MethodGet, projectPath(x.ID), nil, admin.header(false)),
		http.StatusOK); got.Role != "admin" {
		t.Errorf("without the grant, the admin's role on the project is %q", got.Role)
	}
}

// TestGrantRole checks that a grant with the role owner is refused with 400
// owner_not_grantable, naming the field, and one with a role that is no
// role of a grant with 422, and that neither adds a grant, while a grant of
// a membership that the workspace does not have, or of a project that does
// not exist, is answered with 404.
//
// Verifies: FR-TEN-04/AC5
func TestGrantRole(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	editor := newNamedAccount(t, s.main, "ed@example.com", "Ed")
	join(t, d, editor, ws, "editor")
	editors := membershipOf(t, membersOf(t, s.main, owner, ws), editor)

	res := setGrant(t, s.main, owner, p.ID, editors, "owner")
	wantCode(t, "a grant as owner", res, http.StatusBadRequest, api.CodeOwnerNotGrantable)
	if problems, _ := res.details(t)["errors"].([]any); len(problems) != 1 ||
		problems[0].(map[string]any)["location"] != "body.role" {
		t.Errorf("a grant as owner: the problems are %v, want one at body.role", problems)
	}
	for _, r := range []string{"guest", "none", ""} {
		wantCode(t, "a grant as "+r, setGrant(t, s.main, owner, p.ID, editors, r), http.StatusUnprocessableEntity,
			api.CodeValidationFailed)
	}
	if n := countRows(t, d, `SELECT count(*) FROM project_grant`); n != 0 {
		t.Errorf("the refused grants added %d", n)
	}
	var otherMembership string
	for _, w := range workspacesOf(t, s.main, editor) {
		if w.ID != ws {
			otherMembership = w.MembershipID
		}
	}
	for what, res := range map[string]response{
		"of a membership that the workspace does not have": setGrant(t, s.main, owner, p.ID,
			ids.Encode(ids.Membership, ids.New()), "viewer"),
		"of another workspace's membership": setGrant(t, s.main, owner, p.ID, otherMembership, "viewer"),
		"on a project that does not exist": setGrant(t, s.main, owner, ids.Encode(ids.Project, ids.New()), editors,
			"viewer"),
		"of another workspace's member": call(s.main, http.MethodGet, memberGrantsPath(ws, otherMembership), nil,
			owner.header(false)),
	} {
		wantCode(t, "a grant "+what, res, http.StatusNotFound, api.CodeNotFound)
	}
}

// TestOwnerGrant checks that only an owner sets or removes an owner's grant,
// which replaces the owner's role on the project, and that an admin is
// answered with 403 owner_required and changes nothing, while an owner who
// lowers their own role on a project acts there with the grant's role, and
// still removes the grant, which the workspace's role authorises.
func TestOwnerGrant(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	admin := newNamedAccount(t, s.main, "ad@example.com", "Ad")
	join(t, d, admin, ws, "admin")
	members := membersOf(t, s.main, owner, ws)
	owners, admins := membershipOf(t, members, owner), membershipOf(t, members, admin)

	wantCode(t, "an admin grants an owner a role", setGrant(t, s.main, admin, p.ID, owners, "viewer"),
		http.StatusForbidden, api.CodeOwnerRequired)
	grantFrom(t, setGrant(t, s.main, owner, p.ID, owners, "viewer"))
	if got := projectFrom(t, call(s.main, http.MethodGet, projectPath(p.ID), nil, owner.header(false)),
		http.StatusOK); got.Role != "viewer" {
		t.Errorf("the owner's role on the project is %q, want the grant's", got.Role)
	}
	wantCode(t, "an owner lowered to viewer renames the project", call(s.main, http.MethodPatch, projectPath(p.ID),
		jsonBody(t, map[string]string{"name": "Renamed"}), owner.header(true)), http.StatusForbidden,
		api.CodeInsufficientRole)
	for what, res := range map[string]response{
		"changes an owner's grant": setGrant(t, s.main, admin, p.ID, owners, "admin"),
		"removes an owner's grant": call(s.main, http.MethodDelete, grantPath(p.ID, owners), nil, admin.header(true)),
	} {
		wantCode(t, "an admin "+what, res, http.StatusForbidden, api.CodeOwnerRequired)
	}
	if got := grantsAt(t, s.main, admin, grantsPath(p.ID)); !slices.Equal(got,
		[]string{p.ID + " " + owners + " viewer"}) {
		t.Errorf("after the refused changes, the grants are %q", got)
	}
	// A member who is made owner keeps their grant, which only an owner may
	// change from then on.
	editor := newNamedAccount(t, s.main, "ed@example.com", "Ed")
	join(t, d, editor, ws, "editor")
	editors := membershipOf(t, membersOf(t, s.main, owner, ws), editor)
	grantFrom(t, setGrant(t, s.main, admin, p.ID, editors, "commenter"))
	memberFrom(t, changeRole(t, s.main, owner, ws, editors, "owner"))
	wantCode(t, "an admin changes the grant of a member made owner", setGrant(t, s.main, admin, p.ID, editors,
		"viewer"), http.StatusForbidden, api.CodeOwnerRequired)
	if g := grantFrom(t, setGrant(t, s.main, owner, p.ID, editors, "viewer")); g.Role != "viewer" {
		t.Errorf("an owner changes another owner's grant to %+v", g)
	}
	if g := grantFrom(t, setGrant(t, s.main, admin, p.ID, admins, "viewer")); g.Role != "viewer" {
		t.Errorf("an admin lowers their own role on the project to %+v", g)
	}
	if res := call(s.main, http.MethodDelete, grantPath(p.ID, owners), nil, owner.header(true)); res.status !=
		http.StatusNoContent {
		t.Fatalf("the owner removes their own grant: %d %s", res.status, res.body)
	}
	if got := projectFrom(t, call(s.main, http.MethodGet, projectPath(p.ID), nil, owner.header(false)),
		http.StatusOK); got.Role != "owner" {
		t.Errorf("without the grant, the owner's role on the project is %q", got.Role)
	}
}
