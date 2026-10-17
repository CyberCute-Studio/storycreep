package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
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
	s  *server
	d  *dbtest.Database
	ws string
	as map[string]signedIn
}

// newRolesFixture returns a server whose workspace has an account of each of
// matrixRoles.
func newRolesFixture(t *testing.T) *rolesFixture {
	t.Helper()
	s, d, owner, ws := inviteServer(t, "open", nil)
	f := &rolesFixture{s: s, d: d, ws: ws, as: map[string]signedIn{"owner": owner}}
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
	admin := f.d.Admin(t)
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
	if _, err := f.d.Admin(t).Exec(context.Background(), `INSERT INTO invite
		(id, workspace_id, role, token_hash, expires_at, created_by, link_holder)
		VALUES ($1, $2, $3, $4, now() + interval '14 days', $5, $5)`, id, decode(t, ids.Workspace, f.ws), role,
		isolationtest.TokenHash(), owner); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.Invite, id)
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
}

// permissionMatrix is §5.6, for the operations that exist so far: each row,
// named as §5.6 names it, with its explicit allow (✓) or deny (✗) for each
// of its columns, owner, admin, editor, commenter, and viewer, and the
// operations of the API that the row covers. Every row is resolved by the
// workspace role (W), so a guest and a user who is not a member are refused
// every operation with 404, which does not tell them that the workspace
// exists (§5.5). The two rows that manage members are separate: an admin
// does all of the first, and is refused the second with 403 owner_required
// (FR-TEN-03 AC4). A later operation of §5.6 adds its row here (plan item
// 20, standing checklist).
var permissionMatrix = []struct {
	row     string
	allowed [5]bool
	ops     []matrixOp
}{
	{
		row:     "List members and their roles",
		allowed: [5]bool{true, true, true, true, true},
		ops: []matrixOp{{name: "list the members", success: http.StatusOK,
			do: func(t *testing.T, f *rolesFixture, si signedIn) response {
				return call(f.s.main, http.MethodGet, membersPath(f.ws), nil, si.header(false))
			}}},
	},
	{
		row: "Invite/remove members, see the invitations and members' email addresses, change roles, for roles " +
			"up to admin",
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
		},
	},
	{
		row:     "Workspace settings, retention, 2FA policy",
		allowed: [5]bool{true, true, false, false, false},
		ops: []matrixOp{{name: "rename the workspace", success: http.StatusOK,
			do: func(t *testing.T, f *rolesFixture, si signedIn) response {
				return call(f.s.main, http.MethodPatch, workspacesPath+"/"+f.ws,
					jsonBody(t, map[string]string{"name": "Harbour"}), si.header(true))
			}}},
	},
}

// TestPermissionMatrix tries every operation of permissionMatrix as each of
// matrixRoles, and checks that the role that its row of §5.6 allows does it,
// and that every other role is refused: with 403 insufficient_role, or
// owner_required for an admin, or with 404 for a guest and a user who is not
// a member.
//
// Verifies: FR-TEN-03/AC1
func TestPermissionMatrix(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	for _, row := range permissionMatrix {
		for _, op := range row.ops {
			for i, r := range matrixRoles {
				res := op.do(t, f, f.as[r])
				where := fmt.Sprintf("%s: %s, as %s", row.row, op.name, r)
				if i < len(row.allowed) && row.allowed[i] {
					shown, all := 0, 0
					if op.addresses && res.status == op.success {
						shown, all = addresses(t, res)
					}
					if res.status != op.success || shown != all {
						t.Errorf("%s: %d %s, want %d", where, res.status, res.body, op.success)
					}
					continue
				}
				status, code := http.StatusForbidden, api.CodeInsufficientRole
				switch {
				case r == "guest" || r == "none":
					status, code = http.StatusNotFound, api.CodeNotFound
				case r == "admin":
					code = api.CodeOwnerRequired
				}
				if op.addresses && status == http.StatusForbidden {
					// The role lists the members, without their addresses.
					if shown, _ := addresses(t, res); res.status != http.StatusOK || shown != 0 {
						t.Errorf("%s: %d, with %d addresses, want 200 without any", where, res.status, shown)
					}
					continue
				}
				wantCode(t, where, res, status, code)
			}
		}
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
}

// sampleValue returns a value that the schema s accepts, with registry for
// the schemas that it refers to: an object with its required properties, an
// enum's first value, an email address, and so on, so that a request's body
// passes validation and reaches the operation.
func sampleValue(t *testing.T, registry huma.Registry, s *huma.Schema) any {
	t.Helper()
	if s.Ref != "" {
		if s = registry.SchemaFromRef(s.Ref); s == nil {
			t.Fatalf("no schema for %s", s.Ref)
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
	case s.Type == "array":
		return []any{}
	}
	t.Fatalf("no sample value for a schema of type %q", s.Type)
	return nil
}

// TestViewerReadsOnly walks every route of a workspace, whose role authorises
// it (§5.6 W), and calls it as a viewer, with a valid value for each of its
// parameters and a body that its schema accepts, so that the request
// reaches the service. Each route that changes something is refused with 403
// insufficient_role, and each that reads answers 200, unless §5.6 keeps the
// read from viewers too (viewerReads), and nothing that the viewer did is in
// the audit log. A route with a parameter that the test has no value for
// fails, so that the route joins the test.
//
// Verifies: FR-TEN-03/AC3
func TestViewerReadsOnly(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	viewer := f.as["viewer"]
	params := map[string]string{
		"workspace_id":  f.ws,
		"membership_id": f.member(t, "editor"),
		"invite_id":     f.invite(t, "editor"),
	}
	doc := f.s.main.OpenAPI()
	var routes []api.Route
	for r := range f.s.main.Routes() {
		if strings.Contains(r.Pattern, "{workspace_id}") {
			routes = append(routes, r)
		}
	}
	slices.SortFunc(routes, func(a, b api.Route) int { return strings.Compare(a.String(), b.String()) })
	var mutating, reads int
	for _, r := range routes {
		c := f.s.main.Routes()[r]
		item := doc.Paths[r.Pattern]
		if item == nil || operationsOf(item)[r.Method] == nil {
			t.Errorf("%s is not in the OpenAPI document", r)
			continue
		}
		op := operationsOf(item)[r.Method]
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
		if op.RequestBody != nil {
			mt := op.RequestBody.Content["application/json"]
			if mt == nil || mt.Schema == nil {
				t.Errorf("%s takes a body that is not JSON", r)
				continue
			}
			body = jsonBody(t, sampleValue(t, doc.Components.Schemas, mt.Schema))
		}
		res := call(f.s.main, r.Method, path, body, viewer.header(r.Method != http.MethodGet))
		if r.Method != http.MethodGet {
			mutating++
			wantCode(t, "a viewer's "+r.String(), res, http.StatusForbidden, api.CodeInsufficientRole)
			continue
		}
		reads++
		allowed, ok := viewerReads[c]
		switch {
		case !ok:
			t.Errorf("%s requires %s, which viewerReads does not name; add it", r, c)
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
	if n := countRows(t, f.d, `SELECT count(*) FROM audit_entry WHERE actor_user_id = $1::uuid
		AND workspace_id = $2::uuid`, decode(t, ids.User, viewer.body.UserID).String(),
		decode(t, ids.Workspace, f.ws).String()); n != 0 {
		t.Errorf("the workspace's audit log has %d entries of the viewer, want none", n)
	}
}
