package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
)

// The permission matrix on the wire (PRD §5.6, FR-TEN-03): every operation
// that exists so far, tried by a member of each role, and a guest and a
// user who is not a member, whose role in a workspace is none (§5.5).

// matrixRoles are the roles that the permission tests act with: the columns
// of §5.6, then a guest and a user who is not a member.
var matrixRoles = []string{"owner", "admin", "editor", "commenter", "viewer", "guest", "none"}

// rolesFixture is a workspace with a signed-in account of each of
// matrixRoles, the owner's from first-run setup.
type rolesFixture struct {
	s *server
	d *dbtest.Database
	// admin is a connection to the database as the superuser, which the
	// fixture's helpers share, rather than each open one that lasts as long
	// as the test.
	admin *pgx.Conn
	ws    string
	as    map[string]signedIn
	// grants are the grants that each project that the fixture adds
	// (project) gives the accounts of matrixRoles: the role of the grant of
	// each account's membership, by the account's role in the workspace, as
	// {"guest": "admin"}.
	grants map[string]string
}

// newRolesFixture returns a server whose workspace has an account of each of
// matrixRoles. The tests of the roles try every operation as each account,
// many of which, as making, listing, and revoking tokens and inviting
// someone, count in the auth bucket, whose limit of 10 a minute for each
// account would refuse them, so the server's limiter allows each account
// 1,000 of them a minute; the tests of the limits use servers of their own,
// or a policy of their own.
func newRolesFixture(t *testing.T) *rolesFixture {
	t.Helper()
	s, d, owner, ws := inviteServer(t, "open", nil)
	policy := ratelimit.DefaultPolicy()
	policy.Rates[ratelimit.Auth].Limits[ratelimit.User] = 1000
	s.limits = ratelimit.New(readyDB{s.ready}, policy)
	f := &rolesFixture{s: s, d: d, admin: d.Admin(t), ws: ws, as: map[string]signedIn{"owner": owner}}
	for _, r := range matrixRoles[1:] {
		// Not admin@example.com, which is the owner's.
		email := "as-" + r + "@example.com"
		// Each request comes from a client of its own (call), so that the
		// rate limit of one address does not count them all.
		if res := call(s.main, http.MethodPost, registerPath, registration(t, email, strings.ToUpper(r[:1])+r[1:],
			testPassword), nil); res.status != http.StatusAccepted {
			t.Fatalf("register %s: %d %s", email, res.status, res.body)
		}
		si := signedInFrom(t, call(s.main, http.MethodPost, loginPath, credentials(t, email, testPassword), nil))
		if r != "none" {
			join(t, d, si, ws, r)
		}
		f.as[r] = si
	}
	return f
}

// member adds a member of the workspace with the role, as the superuser, and
// returns the ID of the membership.
func (f *rolesFixture) member(t *testing.T, role string) string {
	t.Helper()
	ctx := context.Background()
	admin := f.admin
	user, id := ids.New(), ids.New()
	if _, err := admin.Exec(ctx, `INSERT INTO "user" (id, email, display_name) VALUES ($1, $2, 'Member')`, user,
		user.String()+"@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, $4)`,
		id, decode(t, ids.Workspace, f.ws), user, role); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.Membership, id)
}

// invite adds an open link invitation of the workspace with the role, which
// the owner made and holds the link of, as the superuser, and returns its ID.
func (f *rolesFixture) invite(t *testing.T, role string) string {
	t.Helper()
	id := ids.New()
	owner := decode(t, ids.User, f.as["owner"].body.UserID)
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO invite
		(id, workspace_id, role, token_hash, expires_at, created_by, link_holder)
		VALUES ($1, $2, $3, $4, now() + interval '14 days', $5, $5)`, id, decode(t, ids.Workspace, f.ws), role,
		isolationtest.TokenHash(), owner); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.Invite, id)
}

// project adds a project to the workspace, as the superuser, archived or in
// the trash as state says, if it is "archived" or "deleted", with the
// fixture's grants on it, and returns its ID.
func (f *rolesFixture) project(t *testing.T, state string) string {
	t.Helper()
	id := ids.New()
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO project
		(id, workspace_id, name, settings, archived_at, deleted_at, hard_delete_after)
		VALUES ($1, $2, 'Lighthouse', '{"default_locale": "en", "publish_min_role": "editor", "entry": {}}',
			CASE WHEN $3 = 'archived' THEN now() END, CASE WHEN $3 = 'deleted' THEN now() END,
			CASE WHEN $3 = 'deleted' THEN now() + interval '30 days' END)`,
		id, decode(t, ids.Workspace, f.ws), state); err != nil {
		t.Fatal(err)
	}
	p := ids.Encode(ids.Project, id)
	for r, g := range f.grants {
		f.grant(t, f.membership(t, r), p, g)
	}
	return p
}

// membership returns the ID of the membership of the fixture's account of
// the role r.
func (f *rolesFixture) membership(t *testing.T, r string) string {
	t.Helper()
	var id ids.UUID
	if err := f.admin.QueryRow(context.Background(), `SELECT id FROM membership
		WHERE workspace_id = $1 AND user_id = $2`, decode(t, ids.Workspace, f.ws),
		decode(t, ids.User, f.as[r].body.UserID)).Scan(&id); err != nil {
		t.Fatalf("the membership of the %s: %v", r, err)
	}
	return ids.Encode(ids.Membership, id)
}

// grant gives the membership the role on the project, as the superuser.
func (f *rolesFixture) grant(t *testing.T, membership, project, role string) {
	t.Helper()
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO project_grant
		(id, workspace_id, membership_id, project_id, role) VALUES ($1, $2, $3, $4, $5)`, ids.New(),
		decode(t, ids.Workspace, f.ws), decode(t, ids.Membership, membership), decode(t, ids.Project, project),
		role); err != nil {
		t.Fatal(err)
	}
}

// grantedMember adds a member of the workspace with the role, as the
// superuser, with a grant of grant on a project that it adds, and returns
// the IDs of the project and of the membership.
func (f *rolesFixture) grantedMember(t *testing.T, role, grant string) (project, membership string) {
	t.Helper()
	project, membership = f.project(t, ""), f.member(t, role)
	f.grant(t, membership, project, grant)
	return project, membership
}

// memberPath returns the path of the membership of the workspace ws.
func memberPath(ws, membership string) string { return membersPath(ws) + "/" + membership }

// changeRole changes the role of the membership of the workspace ws to role,
// as si.
func changeRole(t *testing.T, h http.Handler, si signedIn, ws, membership, role string) response {
	t.Helper()
	return call(h, http.MethodPatch, memberPath(ws, membership), jsonBody(t, map[string]string{"role": role}),
		si.header(true))
}

// addresses returns how many of the members on the first page that res
// answered with have an email address, and how many members it has.
func addresses(t *testing.T, res response) (shown, all int) {
	t.Helper()
	var page struct {
		Items []memberBody `json:"items"`
	}
	if err := json.Unmarshal(res.body, &page); err != nil {
		t.Fatalf("the members: %v\n%s", err, res.body)
	}
	for _, m := range page.Items {
		if m.Email != "" {
			shown++
		}
	}
	return shown, len(page.Items)
}

// matrixOp is an operation of the API that a row of §5.6 allows.
type matrixOp struct {
	name string
	// do tries the operation as si, on rows of its own that it adds, and
	// returns the answer.
	do func(t *testing.T, f *rolesFixture, si signedIn) response
	// success is the status that the operation answers with.
	success int
	// addresses says that the operation is seeing the members' email
	// addresses, which a role that the row does not allow is refused by
	// being shown the member list without them, rather than with 403.
	addresses bool
	// project says that the operation is a route of a project that it adds
	// (rolesFixture.project), on which the fixture's grants give a guest a
	// role, so that the project's routes no longer answer them with 404, as
	// a route of the workspace does.
	project bool
}

// permissionMatrix is §5.6, for the operations that exist so far: each row,
// named as §5.6 names it, with its explicit allow (✓) or deny (✗) for each
// of its columns, owner, admin, editor, commenter, and viewer, and the
// operations of the API that the row covers. A row resolved by the workspace
// role (W) refuses a guest and a user who is not a member every operation
// with 404, which does not tell them that the workspace exists, and so does
// a row resolved by the effective role on the project (P), because the
// fixture's guest has no grant on its projects (§5.5). The two rows that
// manage members are separate: an admin
// does all of the first, and is refused the second with 403 owner_required
// (FR-TEN-03 AC4). Each row names its operation of package authz, and a
// later operation of §5.6 adds its row here, or TestPermissionMatrix fails
// (plan item 20, standing checklist).
var permissionMatrix = []struct {
	row     string
	op      authz.Operation
	allowed [5]bool
	ops     []matrixOp
}{
	{
		row:     "List members and their roles",
		op:      authz.ListMembers,
		allowed: [5]bool{true, true, true, true, true},
		ops: []matrixOp{{name: "list the members", success: http.StatusOK,
			do: func(t *testing.T, f *rolesFixture, si signedIn) response {
				return call(f.s.main, http.MethodGet, membersPath(f.ws), nil, si.header(false))
			}}},
	},
	{
		row: "Invite/remove members, see the invitations and members' email addresses, change roles, for roles " +
			"up to admin",
		op:      authz.ManageMembers,
		allowed: [5]bool{true, true, false, false, false},
		ops: []matrixOp{
			{name: "invite as admin", success: http.StatusCreated,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, invitesPath(f.ws),
						jsonBody(t, map[string]string{"role": "admin"}), si.header(true))
				}},
			{name: "list the invitations", success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, invitesPath(f.ws), nil, si.header(false))
				}},
			{name: "send an invitation anew", success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, invitesPath(f.ws)+"/"+f.invite(t, "editor")+"/resend", nil,
						si.header(true))
				}},
			{name: "revoke an invitation", success: http.StatusNoContent,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodDelete, invitesPath(f.ws)+"/"+f.invite(t, "viewer"), nil,
						si.header(true))
				}},
			{name: "see the members' email addresses", success: http.StatusOK, addresses: true,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, membersPath(f.ws), nil, si.header(false))
				}},
			{name: "remove a member", success: http.StatusNoContent,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodDelete, memberPath(f.ws, f.member(t, "editor")), nil,
						si.header(true))
				}},
			{name: "make a member admin", success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return changeRole(t, f.s.main, si, f.ws, f.member(t, "editor"), "admin")
				}},
			{name: "make an admin a viewer", success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return changeRole(t, f.s.main, si, f.ws, f.member(t, "admin"), "viewer")
				}},
		},
	},
	{
		row:     "Grant the owner role (by role change or invite), demote or remove an owner",
		op:      authz.ManageOwners,
		allowed: [5]bool{true, false, false, false, false},
		ops: []matrixOp{
			{name: "invite as owner", success: http.StatusCreated,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, invitesPath(f.ws),
						jsonBody(t, map[string]string{"role": "owner"}), si.header(true))
				}},
			{name: "send an invitation as owner anew", success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, invitesPath(f.ws)+"/"+f.invite(t, "owner")+"/resend", nil,
						si.header(true))
				}},
			{name: "make a member owner", success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return changeRole(t, f.s.main, si, f.ws, f.member(t, "editor"), "owner")
				}},
			{name: "demote an owner", success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return changeRole(t, f.s.main, si, f.ws, f.member(t, "owner"), "admin")
				}},
			{name: "remove an owner", success: http.StatusNoContent,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodDelete, memberPath(f.ws, f.member(t, "owner")), nil,
						si.header(true))
				}},
			// A grant replaces the owner's role on its project, which
			// demotes them there.
			{name: "give an owner a grant", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return setGrant(t, f.s.main, si, f.project(t, ""), f.member(t, "owner"), "viewer")
				}},
			{name: "change an owner's grant", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p, m := f.grantedMember(t, "owner", "viewer")
					return setGrant(t, f.s.main, si, p, m, "admin")
				}},
			{name: "remove an owner's grant", project: true, success: http.StatusNoContent,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p, m := f.grantedMember(t, "owner", "viewer")
					return call(f.s.main, http.MethodDelete, grantPath(p, m), nil, si.header(true))
				}},
		},
	},
	{
		row:     "Workspace settings, retention, 2FA policy",
		op:      authz.ManageWorkspaceSettings,
		allowed: [5]bool{true, true, false, false, false},
		ops: []matrixOp{
			{name: "rename the workspace", success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPatch, workspacesPath+"/"+f.ws,
						jsonBody(t, map[string]string{"name": "Harbour"}), si.header(true))
				}},
			// The settings show the workspace's usage against its plan's
			// limits (FR-ENT-02 AC2).
			{name: "read the usage", success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, usagePath(f.ws), nil, si.header(false))
				}},
		},
	},
	{
		row:     "Create/delete projects",
		op:      authz.ManageProjects,
		allowed: [5]bool{true, true, false, false, false},
		ops: []matrixOp{
			{name: "create a project", success: http.StatusCreated,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, projectsPath(f.ws),
						jsonBody(t, map[string]string{"name": "Lighthouse"}), si.header(true))
				}},
			{name: "list the trash", success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, trashPath(f.ws), nil, si.header(false))
				}},
			{name: "delete a project", project: true, success: http.StatusNoContent,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodDelete, projectPath(f.project(t, "")), nil, si.header(true))
				}},
			{name: "restore a project", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, projectPath(f.project(t, "deleted"))+"/restore", nil,
						si.header(true))
				}},
			{name: "delete a project with a batch of commands", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, commandsPath(f.project(t, "")),
						commandBatch(t, wireCommand{Type: "project.delete"}), si.header(true))
				}},
		},
	},
	{
		row:     "Manage project grants",
		op:      authz.ManageProjectGrants,
		allowed: [5]bool{true, true, false, false, false},
		ops: []matrixOp{
			{name: "list a project's grants", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p, _ := f.grantedMember(t, "guest", "editor")
					return call(f.s.main, http.MethodGet, grantsPath(p), nil, si.header(false))
				}},
			{name: "list a member's grants", success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					_, m := f.grantedMember(t, "guest", "editor")
					return call(f.s.main, http.MethodGet, memberGrantsPath(f.ws, m), nil, si.header(false))
				}},
			{name: "give a guest a grant", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return setGrant(t, f.s.main, si, f.project(t, ""), f.member(t, "guest"), "editor")
				}},
			{name: "raise a viewer to admin on a project", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return setGrant(t, f.s.main, si, f.project(t, ""), f.member(t, "viewer"), "admin")
				}},
			{name: "change a grant", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p, m := f.grantedMember(t, "editor", "viewer")
					return setGrant(t, f.s.main, si, p, m, "commenter")
				}},
			{name: "remove a grant", project: true, success: http.StatusNoContent,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p, m := f.grantedMember(t, "admin", "viewer")
					return call(f.s.main, http.MethodDelete, grantPath(p, m), nil, si.header(true))
				}},
		},
	},
	{
		row:     "Read project content, run play mode, view dependencies",
		op:      authz.ReadProject,
		allowed: [5]bool{true, true, true, true, true},
		ops: []matrixOp{
			{name: "read a project", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, projectPath(f.project(t, "")), nil, si.header(false))
				}},
			{name: "list a project's boards", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, boardsPath(f.project(t, "")), nil, si.header(false))
				}},
			{name: "read a board", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodGet, boardPath(p, f.board(t, p)), nil, si.header(false))
				}},
			{name: "list a project's folders", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, foldersPath(f.project(t, "")), nil, si.header(false))
				}},
			{name: "list a project's trash", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, projectTrashPath(f.project(t, "")), nil, si.header(false))
				}},
			{name: "list a project's variables", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, variablesPath(f.project(t, "")), nil, si.header(false))
				}},
			{name: "read a variable", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodGet, variablePath(p, f.variable(t, p)), nil, si.header(false))
				}},
			{name: "list a project's component types", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, componentTypesPath(f.project(t, "")), nil, si.header(false))
				}},
			{name: "read a component type", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodGet, componentTypePath(p, f.componentType(t, p)), nil,
						si.header(false))
				}},
			{name: "list the values that a retype would not convert", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					ct := f.componentType(t, p)
					return call(f.s.main, http.MethodGet, conversionPath(p, ct, f.attribute(t, p, ct))+"?type=bool", nil,
						si.header(false))
				}},
			{name: "list a project's components", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, componentsPath(f.project(t, "")), nil, si.header(false))
				}},
			{name: "read a component", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodGet, componentPath(p, f.component(t, p, f.componentType(t, p))), nil,
						si.header(false))
				}},
			{name: "list a board's nodes", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodGet, boardNodesPath(p, f.board(t, p)), nil, si.header(false))
				}},
			{name: "read a node", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodGet, nodePath(p, f.node(t, p)), nil, si.header(false))
				}},
			{name: "list the jumpers that target a node", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodGet, nodePath(p, f.node(t, p))+"/referenced-by", nil, si.header(false))
				}},
			{name: "list the jumpers that target a board's nodes", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodGet, boardPath(p, f.board(t, p))+"/referenced-by", nil,
						si.header(false))
				}},
			{name: "suggest a project's tags", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, tagsPath(f.project(t, ""))+"?prefix=q", nil, si.header(false))
				}},
			{name: "list a board's connections", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodGet, boardConnectionsPath(p, f.board(t, p)), nil, si.header(false))
				}},
			{name: "read a connection", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodGet, connectionPath(p, f.connection(t, p)), nil, si.header(false))
				}},
		},
	},
	{
		row:     "Create/rename/archive boards",
		op:      authz.ManageBoards,
		allowed: [5]bool{true, true, true, false, false},
		ops: []matrixOp{
			{name: "create a board", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, commandsPath(f.project(t, "")), commandBatch(t,
						wireCommand{Type: "board.create", Payload: map[string]string{
							"id": ids.Encode(ids.Board, ids.New()), "name": "Pier"}}), si.header(true))
				}},
			{name: "rename a board", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "board.rename", Payload: map[string]string{"id": f.board(t, p), "name": "Docks"}}),
						si.header(true))
				}},
			{name: "delete a board", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "board.delete", Payload: map[string]string{"id": f.board(t, p)}}), si.header(true))
				}},
			{name: "set a board's entry node", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					b := f.board(t, p)
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "board.set_entry_node", Payload: map[string]string{"id": b, "node_id": f.boardNode(t, p, b)}}),
						si.header(true))
				}},
		},
	},
	{
		row:     "Edit nodes, connections, text, expressions",
		op:      authz.EditContent,
		allowed: [5]bool{true, true, true, false, false},
		ops: []matrixOp{
			{name: "create a node", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "node.create", Payload: map[string]any{"id": newNodeID(), "board_id": f.board(t, p),
							"type": "element", "position": map[string]int{"x": 0, "y": 0}}}), si.header(true))
				}},
			{name: "change a node", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "node.update", Payload: map[string]any{"id": f.node(t, p), "title": "Greeting"}}),
						si.header(true))
				}},
			{name: "move a node", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "node.move", Payload: map[string]any{"nodes": []map[string]any{{"id": f.node(t, p),
							"position": map[string]int{"x": 8, "y": 8}}}}}), si.header(true))
				}},
			{name: "delete a node", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "node.delete", Payload: map[string]string{"id": f.node(t, p)}}), si.header(true))
				}},
			{name: "create a connection", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					n := f.node(t, p)
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "connection.create", Payload: map[string]any{"id": newConnectionID(), "from_node_id": n,
							"to_node_id": n}}), si.header(true))
				}},
			{name: "change a connection's label", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "connection.update", Payload: map[string]any{"id": f.connection(t, p),
							"label": paragraph("Ask about the lighthouse")}}), si.header(true))
				}},
			{name: "reconnect a connection", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					c, b := f.boardConnection(t, p)
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "connection.reconnect", Payload: map[string]any{"id": c,
							"to_node_id": f.boardNode(t, p, b)}}), si.header(true))
				}},
			{name: "reorder a connection", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "connection.reorder", Payload: map[string]any{"id": f.connection(t, p), "order": "W"}}),
						si.header(true))
				}},
			{name: "delete a connection", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "connection.delete", Payload: map[string]string{"id": f.connection(t, p)}}), si.header(true))
				}},
			{name: "restore a connection", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "connection.restore", Payload: map[string]string{"id": f.deletedConnection(t, p)}}),
						si.header(true))
				}},
		},
	},
	{
		row:     "Create, rename, move, and delete folders",
		op:      authz.ManageFolders,
		allowed: [5]bool{true, true, true, false, false},
		ops: []matrixOp{
			{name: "create a folder of variables", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, commandsPath(f.project(t, "")), commandBatch(t,
						wireCommand{Type: "folder.create", Payload: map[string]string{
							"id": ids.Encode(ids.Folder, ids.New()), "kind": "variable", "name": "Quests"}}),
						si.header(true))
				}},
			{name: "delete a folder of boards", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "folder.delete", Payload: map[string]string{"id": f.folder(t, p)}}), si.header(true))
				}},
		},
	},
	{
		row:     "Manage variables, components, component types",
		op:      authz.ManageVariablesAndComponents,
		allowed: [5]bool{true, true, true, false, false},
		ops: []matrixOp{
			{name: "create a variable", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, commandsPath(f.project(t, "")), commandBatch(t,
						wireCommand{Type: "variable.create", Payload: map[string]string{
							"id": newVariableID(), "key": "met_elara", "type": "bool", "scope": "global"}}),
						si.header(true))
				}},
			{name: "retype a variable", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "variable.retype", Payload: map[string]string{"id": f.variable(t, p), "type": "string"}}),
						si.header(true))
				}},
			{name: "delete a variable", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "variable.delete", Payload: map[string]string{"id": f.variable(t, p)}}), si.header(true))
				}},
			{name: "create a component type", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, commandsPath(f.project(t, "")), commandBatch(t,
						wireCommand{Type: "component_type.create", Payload: map[string]string{
							"id": newComponentTypeID(), "name": "Ship"}}), si.header(true))
				}},
			{name: "add an attribute definition", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "attribute.create", Payload: map[string]string{"id": newAttributeID(),
							"component_type_id": f.componentType(t, p), "name": "Mood", "type": "string"}}),
						si.header(true))
				}},
			{name: "retype an attribute definition", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "attribute.retype", Payload: map[string]string{
							"id": f.attribute(t, p, f.componentType(t, p)), "type": "float", "conversion": "convert"}}),
						si.header(true))
				}},
			{name: "create a component", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type: "component.create", Payload: map[string]string{"id": newComponentID(),
							"component_type_id": f.componentType(t, p), "name": "Elara"}}), si.header(true))
				}},
			{name: "delete a component", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
						Type:    "component.delete",
						Payload: map[string]string{"id": f.component(t, p, f.componentType(t, p))}}), si.header(true))
				}},
		},
	},
	{
		row:     "Upload images (assets)",
		op:      authz.UploadAssets,
		allowed: [5]bool{true, true, true, false, false},
		ops: []matrixOp{{name: "upload an image", project: true, success: http.StatusCreated,
			do: func(t *testing.T, f *rolesFixture, si signedIn) response {
				return call(f.s.main, http.MethodPost, assetPath(f.project(t, ""), ""), testPNG(t, 2, 2),
					si.header(true))
			}}},
	},
	{
		row:     "Change project settings, including publish_min_role",
		op:      authz.ChangeProjectSettings,
		allowed: [5]bool{true, true, false, false, false},
		ops: []matrixOp{
			{name: "rename a project", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPatch, projectPath(f.project(t, "")),
						jsonBody(t, map[string]string{"name": "Harbour"}), si.header(true))
				}},
			{name: "raise publish_min_role", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPatch, projectPath(f.project(t, "")),
						jsonBody(t, map[string]string{"publish_min_role": "admin"}), si.header(true))
				}},
			{name: "archive a project", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, projectPath(f.project(t, ""))+"/archive", nil,
						si.header(true))
				}},
			{name: "unarchive a project", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, projectPath(f.project(t, "archived"))+"/unarchive", nil,
						si.header(true))
				}},
			{name: "rename a project with a batch of commands", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, commandsPath(f.project(t, "")),
						commandBatch(t, wireCommand{Type: "project.rename", Payload: map[string]string{"name": "Pier"}}),
						si.header(true))
				}},
		},
	},
	{
		row:     "Manage own API tokens narrowed to this project",
		op:      authz.ManageOwnProjectTokens,
		allowed: [5]bool{true, true, true, false, false},
		ops: []matrixOp{
			{name: "make a token narrowed to a project", project: true, success: http.StatusCreated,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, projectTokensPath(f.project(t, "")),
						tokenRequest(t, "project:read"), si.header(true))
				}},
			{name: "list a project's tokens", project: true, success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, projectTokensPath(f.project(t, "")), nil, si.header(false))
				}},
			{name: "revoke one's own token narrowed to a project", project: true, success: http.StatusNoContent,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					token := f.tokenFor(t, decode(t, ids.User, si.body.UserID), p)
					return call(f.s.main, http.MethodDelete, projectTokenPath(p, token), nil, si.header(true))
				}},
		},
	},
	{
		row:     "Manage own workspace-wide API tokens",
		op:      authz.ManageOwnWorkspaceTokens,
		allowed: [5]bool{true, true, true, false, false},
		ops: []matrixOp{
			{name: "make a token of the workspace", success: http.StatusCreated,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodPost, workspaceTokensPath(f.ws), tokenRequest(t, "snapshot:read"),
						si.header(true))
				}},
			{name: "list the workspace's tokens", success: http.StatusOK,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodGet, workspaceTokensPath(f.ws), nil, si.header(false))
				}},
			{name: "revoke one's own token of the workspace", success: http.StatusNoContent,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					token := f.tokenFor(t, decode(t, ids.User, si.body.UserID), "")
					return call(f.s.main, http.MethodDelete, workspaceTokenPath(f.ws, token), nil, si.header(true))
				}},
		},
	},
	{
		row:     "Manage API tokens (others')",
		op:      authz.ManageTokens,
		allowed: [5]bool{true, true, false, false, false},
		ops: []matrixOp{
			{name: "revoke another member's token of the workspace", success: http.StatusNoContent,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					return call(f.s.main, http.MethodDelete, workspaceTokenPath(f.ws, f.otherToken(t, "")), nil,
						si.header(true))
				}},
			// A grant on the project shows it, but authorises nothing of the
			// workspace (FR-TEN-04 AC4).
			{name: "revoke another member's token narrowed to a project", project: true,
				success: http.StatusNoContent,
				do: func(t *testing.T, f *rolesFixture, si signedIn) response {
					p := f.project(t, "")
					return call(f.s.main, http.MethodDelete, projectTokenPath(p, f.otherToken(t, p)), nil,
						si.header(true))
				}},
		},
	},
}

// TestPermissionMatrix checks that permissionMatrix has a row for each
// operation of package authz, tries every operation of the API in it as each
// of matrixRoles, and checks that the role that its row of §5.6 allows does
// it, and that every other role is refused: with 403 insufficient_role, or
// owner_required for an admin, or with 404 for a guest and a user who is not
// a member.
//
// Verifies: FR-TEN-03/AC1
func TestPermissionMatrix(t *testing.T) {
	t.Parallel()
	var named []authz.Operation
	for _, row := range permissionMatrix {
		named = append(named, row.op)
	}
	slices.Sort(named)
	if want := authz.Operations(); !slices.Equal(named, want) {
		t.Errorf("the rows are those of %v, want one of each operation of package authz, %v", named, want)
	}
	f := newRolesFixture(t)
	for _, row := range permissionMatrix {
		for _, op := range row.ops {
			for i, r := range matrixRoles {
				res := op.do(t, f, f.as[r])
				where := fmt.Sprintf("%s: %s, as %s", row.row, op.name, r)
				status, code := http.StatusForbidden, api.CodeInsufficientRole
				switch {
				case r == "guest" || r == "none":
					status, code = http.StatusNotFound, api.CodeNotFound
				case r == "admin":
					code = api.CodeOwnerRequired
				}
				wantAnswer(t, where, op, res, i < len(row.allowed) && row.allowed[i], status, code)
			}
		}
	}
}

// wantAnswer checks res, the answer of the operation op, which where names:
// op's success if allowed is set, and otherwise its refusal with status and
// code, or, for seeing the members' email addresses, which a role is refused
// with 403 by being shown the member list without them, that list.
func wantAnswer(t *testing.T, where string, op matrixOp, res response, allowed bool, status int, code api.Code) {
	t.Helper()
	switch {
	case allowed:
		shown, all := 0, 0
		if op.addresses && res.status == op.success {
			shown, all = addresses(t, res)
		}
		if res.status != op.success || shown != all {
			t.Errorf("%s: %d %s, want %d", where, res.status, res.body, op.success)
		}
	case op.addresses && status == http.StatusForbidden:
		if shown, _ := addresses(t, res); res.status != http.StatusOK || shown != 0 {
			t.Errorf("%s: %d, with %d addresses, want 200 without any", where, res.status, shown)
		}
	default:
		wantCode(t, where, res, status, code)
	}
}

// TestGrantAuthorizesNoWorkspaceOperation tries every operation of
// permissionMatrix as a guest and as a viewer, each with an admin grant on
// every project of the workspace, and checks that the grants give them the
// operations of those projects that §5.6 gives an admin, and nothing of the
// workspace: every operation that the workspace authorises (W) answers them
// as it would without a grant, the viewer as its column of the row says, and
// the guest, whose role in the workspace is none (§5.5), with 404, as the
// workspace's routes answer every user whose role there is none, but for the
// routes of a project, which the grant shows to them, which answer 403
// insufficient_role. A later operation of the workspace joins this test with
// its row of permissionMatrix (standing checklist).
//
// Verifies: FR-TEN-04/AC4
func TestGrantAuthorizesNoWorkspaceOperation(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	f.grants = map[string]string{"guest": "admin", "viewer": "admin"}
	x := f.project(t, "")
	for _, r := range []string{"guest", "viewer"} {
		if got := projectFrom(t, call(f.s.main, http.MethodGet, projectPath(x), nil, f.as[r].header(false)),
			http.StatusOK); got.Role != "admin" {
			t.Errorf("the %s's role on the granted project is %q, want admin", r, got.Role)
		}
	}
	workspaceOps := 0
	for _, row := range permissionMatrix {
		for _, op := range row.ops {
			for _, r := range []string{"guest", "viewer"} {
				res := op.do(t, f, f.as[r])
				where := fmt.Sprintf("%s: %s, as a %s with admin grants", row.row, op.name, r)
				if row.op.Scope() == authz.Project {
					wantAnswer(t, where, op, res, row.allowed[1], http.StatusForbidden, api.CodeInsufficientRole)
					continue
				}
				workspaceOps++
				status, code := http.StatusForbidden, api.CodeInsufficientRole
				if r == "guest" && !op.project {
					status, code = http.StatusNotFound, api.CodeNotFound
				}
				wantAnswer(t, where, op, res, r == "viewer" && row.allowed[4], status, code)
			}
		}
	}
	// Guard against a matrix that silently has no operation of the workspace.
	if workspaceOps < 20 {
		t.Errorf("the test tried %d operations of the workspace", workspaceOps)
	}
}

// viewerReads are the capabilities of the routes of a workspace, each with
// whether a viewer may use it to read (§5.6). Reading the workspace and its
// member list needs only a role in it; seeing the invitations belongs to the
// row of managing members. A route of a workspace whose capability is not
// here fails TestViewerReadsOnly, so that it says which it is.
var viewerReads = map[api.Capability]bool{
	api.ReadWorkspace:           true,
	api.ListMembers:             true,
	api.ManageMembers:           false,
	api.ManageWorkspaceSettings: false,
	api.ListProjects:            true,
	api.ManageProjects:          false,
	api.ManageProjectGrants:     false,
	api.ReadProject:             true,
	api.ChangeProjectSettings:   false,
	// Listing tokens is managing them, which needs editor at least.
	api.ManageWorkspaceTokens: false,
	api.ManageProjectTokens:   false,
}

// sampleValue returns a value that the schema s accepts, with registry for
// the schemas that it refers to: an object with its required properties, an
// enum's first value, an email address, and so on, so that a request's body
// passes validation and reaches the operation.
func sampleValue(t *testing.T, registry huma.Registry, s *huma.Schema) any {
	t.Helper()
	if ref := s.Ref; ref != "" {
		if s = registry.SchemaFromRef(ref); s == nil {
			t.Fatalf("no schema for %s", ref)
		}
	}
	switch {
	case len(s.Enum) > 0:
		return s.Enum[0]
	case s.Type == "object":
		v := map[string]any{}
		for _, name := range s.Required {
			v[name] = sampleValue(t, registry, s.Properties[name])
		}
		return v
	case s.Type == "string" && (s.Format == "email" || s.Format == "idn-email"):
		return "sample@example.com"
	case s.Type == "string":
		return "Sample"
	case s.Type == "integer" || s.Type == "number":
		if s.Minimum != nil {
			return *s.Minimum
		}
		return 1
	case s.Type == "boolean":
		return true
	case s.Type == "array" && s.MinItems != nil && *s.MinItems > 0 && s.Items != nil:
		v := make([]any, *s.MinItems)
		for i := range v {
			v[i] = sampleValue(t, registry, s.Items)
		}
		return v
	case s.Type == "array":
		return []any{}
	}
	t.Fatalf("no sample value for a schema of type %q", s.Type)
	return nil
}

// sampleBodies are the bodies of the routes whose schema sampleValue cannot
// sample, each of which reaches the operation, as a sample does: the command
// endpoint's, whose body is one of several schemas, of commands whose
// payloads a sample of the schema would not make valid, and an upload's,
// which is a file. The command endpoint's sample is a batch of a new
// command, which unarchives the project, so that it is answered the same
// whether a route walked before it archived the project, and needs owner or
// admin, as the project's settings do. The upload's is a small PNG image.
var sampleBodies = map[api.Route]func(t *testing.T) []byte{
	{Method: http.MethodPost, Pattern: "/api/v1/projects/{project_id}/commands"}: func(t *testing.T) []byte {
		return jsonBody(t, map[string]any{"commands": []any{map[string]any{"command_id": ids.New().String(),
			"type": "project.unarchive", "payload": map[string]any{}}}})
	},
	{Method: http.MethodPost, Pattern: assetsPath}: func(t *testing.T) []byte { return testPNG(t, 4, 3) },
}

// routeCall is a request of a route of a workspace or of a project, with a
// valid value for each of its parameters and a body that its schema
// accepts, so that it reaches the service.
type routeCall struct {
	route      api.Route
	capability api.Capability
	path       string
	body       []byte
}

// routeCalls returns a call of every route of a workspace, whose role
// authorises it (§5.6 W), and of a project, whose effective role authorises
// it (P), of the fixture's server, in the order of the routes, with the
// values of params for the parameters of their paths. It fails the test on
// a route with a parameter that params has no value for, so that the route
// joins the tests that walk them.
func routeCalls(t *testing.T, f *rolesFixture, params map[string]string) []routeCall {
	t.Helper()
	var routes []api.Route
	for r := range f.s.main.Routes() {
		if strings.Contains(r.Pattern, "{workspace_id}") || strings.Contains(r.Pattern, "{project_id}") {
			routes = append(routes, r)
		}
	}
	return callsOf(t, f, params, routes)
}

// tokenRouteCalls returns a call of every route that takes an API token
// (api.TokenScope), as routeCalls does, those without a parameter of a
// workspace or a project included, such as the list of workspaces.
func tokenRouteCalls(t *testing.T, f *rolesFixture, params map[string]string) []routeCall {
	t.Helper()
	return callsOf(t, f, params, slices.Collect(maps.Keys(f.s.main.TokenScopes())))
}

// callsOf returns a call of each of routes, in their order, but the routes
// that archive the project last, with the values of params for the
// parameters of their paths, as routeCalls describes.
func callsOf(t *testing.T, f *rolesFixture, params map[string]string, routes []api.Route) []routeCall {
	t.Helper()
	doc := f.s.main.OpenAPI()
	// Archiving the project makes it read-only, so that every route walked
	// after it that changes the project would refuse the change, as an
	// upload does: it comes last.
	archives := func(r api.Route) bool { return strings.HasSuffix(r.Pattern, "/archive") }
	slices.SortFunc(routes, func(a, b api.Route) int {
		if archives(a) != archives(b) {
			if archives(a) {
				return 1
			}
			return -1
		}
		return strings.Compare(a.String(), b.String())
	})
	var out []routeCall
	for _, r := range routes {
		item := doc.Paths[r.Pattern]
		if item == nil || api.Operations(item)[r.Method] == nil {
			t.Errorf("%s is not in the OpenAPI document", r)
			continue
		}
		op := api.Operations(item)[r.Method]
		path, query := r.Pattern, url.Values{}
		for _, p := range op.Parameters {
			switch p.In {
			case "path":
				v, ok := params[p.Name]
				if !ok {
					t.Errorf("%s: the test has no value for the parameter %s; add one to params", r, p.Name)
					continue
				}
				path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(v))
			case "query":
				if p.Required {
					query.Set(p.Name, validValue(p.Schema))
				}
			}
		}
		if len(query) > 0 {
			path += "?" + query.Encode()
		}
		var body []byte
		if sample, ok := sampleBodies[r]; ok {
			body = sample(t)
		} else if op.RequestBody != nil {
			mt := op.RequestBody.Content["application/json"]
			if mt == nil || mt.Schema == nil {
				t.Errorf("%s takes a body that is not JSON", r)
				continue
			}
			body = jsonBody(t, sampleValue(t, doc.Components.Schemas, mt.Schema))
		}
		out = append(out, routeCall{route: r, capability: f.s.main.Routes()[r], path: path, body: body})
	}
	return out
}

// routeParams returns a value for each parameter of the paths of the
// fixture's routes: its workspace, and a membership, an invitation, a
// project, the owner's API token narrowed to the project, an asset of the
// project, which the owner uploads, and a board, a variable, a component
// type with an int attribute definition, a component of it, a node, and a
// connection, of the project, which the fixture adds.
func routeParams(t *testing.T, f *rolesFixture) map[string]string {
	t.Helper()
	p := f.project(t, "")
	ct := f.componentType(t, p)
	return map[string]string{
		"workspace_id":      f.ws,
		"membership_id":     f.member(t, "editor"),
		"invite_id":         f.invite(t, "editor"),
		"project_id":        p,
		"token_id":          f.token(t, "owner", p),
		"asset_id":          upload(t, f.s.main, f.as["owner"], p, testPNG(t, 5, 5), http.StatusCreated).ID,
		"board_id":          f.board(t, p),
		"variable_id":       f.variable(t, p),
		"component_type_id": ct,
		"attribute_id":      f.attribute(t, p, ct),
		"component_id":      f.component(t, p, ct),
		"node_id":           f.node(t, p),
		"connection_id":     f.connection(t, p),
	}
}

// auditEntriesOf returns how many entries of the fixture's workspace's audit
// log si's account is the actor of.
func auditEntriesOf(t *testing.T, f *rolesFixture, si signedIn) int {
	t.Helper()
	return countRows(t, f.d, `SELECT count(*) FROM audit_entry WHERE actor_user_id = $1::uuid
		AND workspace_id = $2::uuid`, decode(t, ids.User, si.body.UserID).String(),
		decode(t, ids.Workspace, f.ws).String())
}

// TestViewerReadsOnly walks every route of a workspace and of a project
// (routeCalls), and calls it as a viewer. Each route that changes something
// is refused with 403 insufficient_role, and each that reads answers 200,
// unless §5.6 keeps the read from viewers too (viewerReads), and nothing
// that the viewer did is in the audit log. A route with a parameter that the
// test has no value for fails, so that the route joins the test.
//
// Verifies: FR-TEN-03/AC3
func TestViewerReadsOnly(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	viewer := f.as["viewer"]
	var mutating, reads int
	for _, c := range routeCalls(t, f, routeParams(t, f)) {
		r := c.route
		res := call(f.s.main, r.Method, c.path, c.body, viewer.header(r.Method != http.MethodGet))
		if r.Method != http.MethodGet {
			mutating++
			wantCode(t, "a viewer's "+r.String(), res, http.StatusForbidden, api.CodeInsufficientRole)
			continue
		}
		reads++
		allowed, ok := viewerReads[c.capability]
		switch {
		case !ok:
			t.Errorf("%s requires %s, which viewerReads does not name; add it", r, c.capability)
		case allowed && res.status != http.StatusOK:
			t.Errorf("a viewer's %s: %d %s, want 200", r, res.status, res.body)
		case !allowed:
			wantCode(t, "a viewer's "+r.String(), res, http.StatusForbidden, api.CodeInsufficientRole)
		}
	}
	// Guard against a walk that silently finds nothing.
	if mutating < 6 || reads < 3 {
		t.Errorf("the test tried %d routes that change something and %d that read", mutating, reads)
	}
	if n := auditEntriesOf(t, f, viewer); n != 0 {
		t.Errorf("the workspace's audit log has %d entries of the viewer, want none", n)
	}
}

// TestNoRoleNotFound walks every route of a workspace and of a project
// (routeCalls), and calls it as a signed-in user who is not a member of the
// workspace, and as a guest without a grant, whose role in it is none
// (§5.5): each route answers them with 404 not_found, never 403 or 500, so
// that it does not tell that the workspace or the project exists, but for
// the guest's list of the workspace's projects, which a membership allows,
// and which shows them none. Nothing that either did is in the audit log.
//
// Verifies: FR-TEN-04/AC6
func TestNoRoleNotFound(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	calls := routeCalls(t, f, routeParams(t, f))
	if len(calls) < 9 {
		t.Errorf("the test tried %d routes", len(calls))
	}
	for _, who := range []string{"none", "guest"} {
		si := f.as[who]
		for _, c := range calls {
			r := c.route
			res := call(f.s.main, r.Method, c.path, c.body, si.header(r.Method != http.MethodGet))
			where := fmt.Sprintf("%s, as %s", r, who)
			if who == "guest" && c.capability == api.ListProjects {
				if got := projectIDs(t, f.s.main, si, c.path); len(got) != 0 {
					t.Errorf("%s: lists %v, want none", where, got)
				}
				continue
			}
			wantCode(t, where, res, http.StatusNotFound, api.CodeNotFound)
		}
		if n := auditEntriesOf(t, f, si); n != 0 {
			t.Errorf("the workspace's audit log has %d entries of the %s, want none", n, who)
		}
	}
}
