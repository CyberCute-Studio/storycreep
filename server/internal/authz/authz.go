// Package authz decides what a role allows (PRD §5.6): it holds the
// permission matrix, each operation with the least role that it needs, and
// every service authorises its operations here, so that the matrix is
// written once, in the service layer, and never in a handler (FR-TEN-03
// AC2).
//
// An operation is authorised by the role that a function of package role
// resolves (§5.5), as its Scope says: a workspace-scoped operation (W) by
// the user's role in the workspace, which no project grant changes, and a
// project-scoped one (P) by their effective role on the project. A service
// answers a resolved role of none with 404 before it asks (FR-TEN-04 AC6),
// and none allows nothing here either, nor does guest, nor a value that is
// not a role, so that a check fails closed.
//
// The matrix holds the operations that exist so far. A later operation of
// §5.6 joins it with the requirement that adds it, and adds its rows to the
// permission-matrix test of package httpserver (plan item 20, standing
// checklist).
package authz

import (
	"errors"
	"slices"

	"github.com/CyberCute-Studio/storycreep/server/internal/role"
)

// Operation is an operation of the permission matrix (§5.6).
type Operation string

// The operations. The two rows of §5.6 that manage a workspace's members are
// two operations: ManageMembers, and ManageOwners, which an operation that
// grants the owner role, or changes or removes an owner's membership, needs
// on top of it.
const (
	// ListMembers is listing a workspace's members and their roles (W).
	ListMembers Operation = "workspace.members.list"
	// ManageMembers is inviting and removing a workspace's members, seeing
	// its invitations and its members' email addresses, and changing the
	// members' roles, for the roles up to admin (W).
	ManageMembers Operation = "workspace.members.manage"
	// ManageOwners is granting the owner role, by a role change or an
	// invitation, and demoting or removing an owner (W), which setting or
	// removing an owner's grant on a project is too, because a grant replaces
	// the owner's role there.
	ManageOwners Operation = "workspace.owners.manage"
	// ManageWorkspaceSettings is changing a workspace's settings (W): its
	// name, slug, and default locale, and later its retention and 2FA policy,
	// and reading its usage against its plan's limits, which its settings
	// show (FR-ENT-02 AC2).
	ManageWorkspaceSettings Operation = "workspace.settings"
	// ManageProjects is creating a workspace's projects, deleting them to
	// its trash, listing its trash, and restoring them from it (W, "Create/
	// delete projects").
	ManageProjects Operation = "workspace.projects"
	// ManageProjectGrants is listing, setting, and removing the members'
	// grants on a workspace's projects (W, FR-TEN-04), which the workspace
	// role authorises, never a grant.
	ManageProjectGrants Operation = "workspace.grants"
	// ReadProject is reading a project's content (P), which every role that
	// allows anything allows.
	ReadProject Operation = "project.read"
	// UploadAssets is uploading an image to a project (P, "Upload images
	// (assets)", PRD FR-ASSET-02), which its content, as a node's cover or a
	// component's portrait, then refers to.
	UploadAssets Operation = "project.assets.upload"
	// ChangeProjectSettings is changing a project's settings (P): its name,
	// slug, description, default locale, entry board and node, and
	// publish_min_role (FR-SNAP-01 AC3), and archiving and unarchiving it.
	ChangeProjectSettings Operation = "project.settings"
	// ManageBoards is creating a project's boards, and renaming,
	// describing, colouring, moving, deleting, and restoring them, and
	// changing their slugs (P, "Create/rename/archive boards", FR-PROJ-02).
	ManageBoards Operation = "project.boards"
	// ManageFolders is creating, renaming, moving, deleting, and restoring a
	// project's folders, of boards, variables, and components alike (P,
	// "Create, rename, move, and delete folders", FR-PROJ-02).
	ManageFolders Operation = "project.folders"
	// EditContent is creating, changing, moving, duplicating, deleting, and
	// restoring a project's nodes (P, "Edit nodes, connections, text,
	// expressions", FR-EDIT-02), which its connections (plan item 36), the
	// text of either, and their expressions join.
	EditContent Operation = "project.content"
	// ManageVariablesAndComponents is creating a project's variables, and
	// renaming, describing, retyping, rescoping, moving, deleting, and
	// restoring them, and changing their defaults (P, "Manage variables,
	// components, component types", FR-VAR-01), which the components and
	// their types (plan item 34) join.
	ManageVariablesAndComponents Operation = "project.variables"
	// PublishSnapshot is publishing a snapshot of a project (P, "Publish a
	// snapshot", FR-SNAP-01), which an editor may do unless the project's
	// publish_min_role is admin (FR-SNAP-01 AC3), as CheckPublish says, and
	// which deleting a variable or a component that the project's content
	// uses, all the same, needs as well (FR-VAR-04).
	PublishSnapshot Operation = "project.publish"
	// ManageOwnProjectTokens is creating, listing, and revoking one's own API
	// tokens narrowed to a project (P, "Manage own API tokens narrowed to
	// this project", FR-AUTH-04).
	ManageOwnProjectTokens Operation = "project.tokens.own"
	// ManageOwnWorkspaceTokens is creating, listing, and revoking one's own
	// API tokens of the whole workspace (W, "Manage own workspace-wide API
	// tokens").
	ManageOwnWorkspaceTokens Operation = "workspace.tokens.own"
	// ManageTokens is listing and revoking the other members' API tokens,
	// those of the whole workspace and those narrowed to a project alike (W,
	// "Manage API tokens (others')"), which no project grant authorises
	// (FR-TEN-04 AC4).
	ManageTokens Operation = "workspace.tokens"
)

// Scope is what an operation acts on, which says which role authorises it
// (§5.5, §5.6).
type Scope string

// The scopes.
const (
	// Workspace is a workspace-scoped operation (W), authorised by the user's
	// role in the workspace (role.Policy.Workspace).
	Workspace Scope = "workspace"
	// Project is a project-scoped operation (P), authorised by the user's
	// effective role on the project (role.Policy.Effective).
	Project Scope = "project"
)

// The errors that refuse a role an operation.
var (
	// ErrInsufficientRole is returned for an operation that the user's role
	// does not allow.
	ErrInsufficientRole = errors.New("authz: the user's role does not allow it")
	// ErrOwnerRequired is returned for an operation that only an owner of the
	// workspace may do (ManageOwners). It reaches only an admin (FR-TEN-03
	// AC4), because the operations that need ManageOwners need ManageMembers
	// first, which refuses the roles below admin with ErrInsufficientRole
	// (CheckGrant).
	ErrOwnerRequired = errors.New("authz: only an owner of the workspace may do it")
)

// rule is an operation's row of the matrix.
type rule struct {
	scope Scope
	// least is the least role that allows the operation. The roles above it
	// allow it too, as §5.6 orders them (role.Role.AtLeast).
	least role.Role
	// denied is the error that a role that does not allow the operation is
	// refused with.
	denied error
}

// matrix is §5.6, for the operations that exist so far.
var matrix = map[Operation]rule{
	ListMembers:                  {scope: Workspace, least: role.Viewer, denied: ErrInsufficientRole},
	ManageMembers:                {scope: Workspace, least: role.Admin, denied: ErrInsufficientRole},
	ManageOwners:                 {scope: Workspace, least: role.Owner, denied: ErrOwnerRequired},
	ManageWorkspaceSettings:      {scope: Workspace, least: role.Admin, denied: ErrInsufficientRole},
	ManageProjects:               {scope: Workspace, least: role.Admin, denied: ErrInsufficientRole},
	ManageProjectGrants:          {scope: Workspace, least: role.Admin, denied: ErrInsufficientRole},
	ReadProject:                  {scope: Project, least: role.Viewer, denied: ErrInsufficientRole},
	UploadAssets:                 {scope: Project, least: role.Editor, denied: ErrInsufficientRole},
	ChangeProjectSettings:        {scope: Project, least: role.Admin, denied: ErrInsufficientRole},
	ManageBoards:                 {scope: Project, least: role.Editor, denied: ErrInsufficientRole},
	ManageFolders:                {scope: Project, least: role.Editor, denied: ErrInsufficientRole},
	EditContent:                  {scope: Project, least: role.Editor, denied: ErrInsufficientRole},
	ManageVariablesAndComponents: {scope: Project, least: role.Editor, denied: ErrInsufficientRole},
	PublishSnapshot:              {scope: Project, least: role.Editor, denied: ErrInsufficientRole},
	ManageOwnProjectTokens:       {scope: Project, least: role.Editor, denied: ErrInsufficientRole},
	// The workspace's own tokens and the others' are rows of their own.
	ManageOwnWorkspaceTokens: {scope: Workspace, least: role.Editor, denied: ErrInsufficientRole},
	ManageTokens:             {scope: Workspace, least: role.Admin, denied: ErrInsufficientRole},
}

// Operations returns the operations of the matrix, in order of their names.
func Operations() []Operation {
	ops := make([]Operation, 0, len(matrix))
	for op := range matrix {
		ops = append(ops, op)
	}
	slices.Sort(ops)
	return ops
}

// Known reports whether op is an operation of the matrix.
func (op Operation) Known() bool {
	_, ok := matrix[op]
	return ok
}

// Scope returns what op acts on, or "" if op is not an operation of the
// matrix.
func (op Operation) Scope() Scope {
	return matrix[op].scope
}

// Allows reports whether the role r allows the operation op. Guest, none,
// and a value that is not a role allow nothing, and nothing allows an
// operation that is not in the matrix.
func Allows(r role.Role, op Operation) bool {
	rule, ok := matrix[op]
	return ok && r.AtLeast(rule.least)
}

// Check returns nil if the role r allows the operation op, and the error that
// refuses it otherwise: ErrOwnerRequired for ManageOwners, and
// ErrInsufficientRole for the others, and for an operation that is not in the
// matrix.
func Check(r role.Role, op Operation) error {
	if Allows(r, op) {
		return nil
	}
	if rule, ok := matrix[op]; ok {
		return rule.denied
	}
	return ErrInsufficientRole
}

// CheckPublish returns nil if the role r allows publishing a snapshot of a
// project whose publish_min_role is least, editor or admin (PublishSnapshot,
// FR-SNAP-01 AC3), and ErrInsufficientRole otherwise: the matrix's row, and
// least, which raises the row's editor to admin, so that a least that is not
// a role, or none, allows nothing.
func CheckPublish(r, least role.Role) error {
	if err := Check(r, PublishSnapshot); err != nil {
		return err
	}
	if !r.AtLeast(least) {
		return ErrInsufficientRole
	}
	return nil
}

// CheckGrant returns nil if the role r allows giving a member, or an
// invitation, the role granted, and the error that refuses it otherwise:
// granting any role needs ManageMembers, and granting owner ManageOwners as
// well, so that an admin who grants owner is refused with ErrOwnerRequired,
// and a role below admin with ErrInsufficientRole whatever it grants.
func CheckGrant(r, granted role.Role) error {
	if err := Check(r, ManageMembers); err != nil {
		return err
	}
	if granted == role.Owner {
		return Check(r, ManageOwners)
	}
	return nil
}

// Ungrantable returns the roles of a membership (role.Memberships) that the
// role r may not grant (CheckGrant): every one for a role that does not
// manage members, owner for admin, and none for owner.
func Ungrantable(r role.Role) []role.Role {
	var out []role.Role
	for _, granted := range role.Memberships() {
		if CheckGrant(r, granted) != nil {
			out = append(out, granted)
		}
	}
	return out
}
