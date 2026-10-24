package authz

import (
	"errors"
	"slices"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/role"
)

// roles are the columns of §5.6, then guest, none, and a value that is no
// role, which allow nothing.
var roles = []role.Role{role.Owner, role.Admin, role.Editor, role.Commenter, role.Viewer, role.Guest, role.None,
	"superuser"}

// TestMatrix checks every (role × operation) pair of the matrix against §5.6,
// written out row by row with an explicit allow (✓) or deny (✗) for each of
// its columns, and that guest, none, and a value that is no role are denied
// every operation. A row for each operation of the matrix must be here, so
// that an operation added without one fails. The routes are checked against
// the same rows by TestPermissionMatrix in package httpserver.
//
// Verifies: FR-TEN-03/AC1
func TestMatrix(t *testing.T) {
	t.Parallel()
	const y, n = true, false
	// want[op] is §5.6's row: owner, admin, editor, commenter, viewer, and
	// scopes[op] its Resolved by column.
	want := map[Operation][5]bool{
		// List members and their roles (W).
		ListMembers: {y, y, y, y, y},
		// Invite/remove members, see the invitations and members' email
		// addresses, change roles, for roles up to admin (W).
		ManageMembers: {y, y, n, n, n},
		// Grant the owner role (by role change or invite), demote or remove
		// an owner (W).
		ManageOwners: {y, n, n, n, n},
		// Workspace settings, retention, 2FA policy (W).
		ManageWorkspaceSettings: {y, y, n, n, n},
		// Create/delete projects (W).
		ManageProjects: {y, y, n, n, n},
		// Manage project grants (W).
		ManageProjectGrants: {y, y, n, n, n},
		// Read project content, run play mode, view dependencies (P).
		ReadProject: {y, y, y, y, y},
		// Upload images (assets) (P).
		UploadAssets: {y, y, y, n, n},
		// Change project settings, including publish_min_role (P).
		ChangeProjectSettings: {y, y, n, n, n},
		// Create/rename/archive boards (P).
		ManageBoards: {y, y, y, n, n},
		// Create, rename, move, and delete folders (P).
		ManageFolders: {y, y, y, n, n},
		// Edit nodes, connections, text, expressions (P).
		EditContent: {y, y, y, n, n},
		// Manage variables, components, component types (P).
		ManageVariablesAndComponents: {y, y, y, n, n},
		// Manage own API tokens narrowed to this project (P).
		ManageOwnProjectTokens: {y, y, y, n, n},
		// Manage own workspace-wide API tokens (W).
		ManageOwnWorkspaceTokens: {y, y, y, n, n},
		// Manage API tokens (others') (W).
		ManageTokens: {y, y, n, n, n},
	}
	scopes := map[Operation]Scope{ReadProject: Project, UploadAssets: Project, ChangeProjectSettings: Project,
		ManageBoards: Project, ManageFolders: Project, EditContent: Project, ManageVariablesAndComponents: Project,
		ManageOwnProjectTokens: Project}
	for _, op := range Operations() {
		row, ok := want[op]
		if !ok {
			t.Errorf("%s has no row of §5.6 in the test", op)
			continue
		}
		scope := Workspace
		if s, ok := scopes[op]; ok {
			scope = s
		}
		if op.Scope() != scope {
			t.Errorf("%s is resolved by %q, want %q", op, op.Scope(), scope)
		}
		for i, r := range roles {
			allowed := i < len(row) && row[i]
			if got := Allows(r, op); got != allowed {
				t.Errorf("Allows(%q, %s) = %v, want %v", r, op, got, allowed)
			}
			var refusal error
			switch {
			case allowed:
			case op == ManageOwners:
				refusal = ErrOwnerRequired
			default:
				refusal = ErrInsufficientRole
			}
			if err := Check(r, op); !errors.Is(err, refusal) {
				t.Errorf("Check(%q, %s) = %v, want %v", r, op, err, refusal)
			}
		}
	}
	if len(want) != len(Operations()) {
		t.Errorf("the test has %d rows, and the matrix %d operations", len(want), len(Operations()))
	}
	if Operation("project.publish").Known() || Allows(role.Owner, "project.publish") ||
		!errors.Is(Check(role.Owner, "project.publish"), ErrInsufficientRole) {
		t.Errorf("an operation that is not in the matrix is allowed")
	}
}

// TestCheckGrant checks who may give a member or an invitation each role:
// owners every role, admins every role but owner, refused with
// ErrOwnerRequired, and the roles below admin none, refused with
// ErrInsufficientRole whatever they would give; and that Ungrantable lists
// the roles that a role may not give.
//
// Verifies: FR-TEN-03/AC4
func TestCheckGrant(t *testing.T) {
	t.Parallel()
	for _, r := range roles {
		var ungrantable []role.Role
		for _, granted := range role.Memberships() {
			err := CheckGrant(r, granted)
			var want error
			switch {
			case r == role.Owner:
			case r == role.Admin && granted == role.Owner:
				want = ErrOwnerRequired
			case r == role.Admin:
			default:
				want = ErrInsufficientRole
			}
			if !errors.Is(err, want) {
				t.Errorf("CheckGrant(%q, %s) = %v, want %v", r, granted, err, want)
			}
			if err != nil {
				ungrantable = append(ungrantable, granted)
			}
		}
		if got := Ungrantable(r); !slices.Equal(got, ungrantable) {
			t.Errorf("Ungrantable(%q) = %v, want %v", r, got, ungrantable)
		}
	}
	if got := Ungrantable(role.Admin); !slices.Equal(got, []role.Role{role.Owner}) {
		t.Errorf("Ungrantable(admin) = %v, want owner alone", got)
	}
}
