// Package role resolves the role that a user acts with (PRD §5.5): in a
// workspace, for the operations that the workspace authorises (W in §5.6),
// and on a project, for those that the project authorises (P), where a
// project grant replaces the workspace role. Both resolutions are pure
// functions of what the database holds about the user, so that every
// combination is tested without a database (FR-TEN-04 AC3), and the caller
// looks up their inputs: whether the user is an instance admin, the role of
// their membership of the workspace, and the role of the membership's grant
// on the project.
//
// A resolution that is None denies every operation, and the API answers it
// with 404 rather than 403, so that it does not reveal that the workspace or
// the project exists (FR-TEN-04 AC6). Both fail closed: a role that is not
// one of the model's, which the database's checks rule out, resolves to
// None.
package role

// Role is a role of the data model (PRD §5.3, FR-TEN-03), as the database
// holds it, or None.
type Role string

// The roles, from the most to the least that they allow, then Guest, which
// a workspace member holds who may act only on the projects granted to them,
// and None, which allows nothing.
const (
	Owner     Role = "owner"
	Admin     Role = "admin"
	Editor    Role = "editor"
	Commenter Role = "commenter"
	Viewer    Role = "viewer"
	Guest     Role = "guest"
	// None is the resolution of a user who may do nothing, and stands for an
	// absent membership or grant in the resolutions' inputs.
	None Role = "none"
)

// Memberships returns the roles that a membership of a workspace can have,
// and so an invitation can give: those that allow something, from the most
// to the least, and Guest.
func Memberships() []Role {
	return []Role{Owner, Admin, Editor, Commenter, Viewer, Guest}
}

// Grants returns the roles that a project grant can have (FR-TEN-04): those
// that allow something but owner, which is a role of the workspace alone,
// from the most to the least that they allow.
func Grants() []Role {
	return []Role{Admin, Editor, Commenter, Viewer}
}

// rank orders the roles that allow something: owner > admin > editor >
// commenter > viewer (§5.6). Guest, None, and any other value rank 0.
func rank(r Role) int {
	switch r {
	case Owner:
		return 5
	case Admin:
		return 4
	case Editor:
		return 3
	case Commenter:
		return 2
	case Viewer:
		return 1
	}
	return 0
}

// AtLeast reports whether r allows all that least does: whether r is least,
// or a role above it. Guest and None allow nothing themselves, so no role is
// at least either of them, and neither is at least any role.
func (r Role) AtLeast(least Role) bool {
	return rank(least) > 0 && rank(r) >= rank(least)
}

// Policy is how the instance resolves roles.
type Policy struct {
	// InstanceAdminAccess lets an instance admin act as the owner of every
	// workspace and project, whatever their memberships: the
	// InstanceAdminAccess capability, which only self-hosted mode can turn
	// on (config.Capabilities).
	InstanceAdminAccess bool
}

// Workspace is workspace_role(user, workspace) of §5.5: the role that a
// user acts with in a workspace, for the operations that the workspace
// authorises, which no project grant changes. instanceAdmin says whether the
// user is an instance admin, and membership is the role of their membership
// of the workspace, or None if they are not a member. A guest's role is
// None, because guests have no workspace-wide capability.
func (p Policy) Workspace(instanceAdmin bool, membership Role) Role {
	if instanceAdmin && p.InstanceAdminAccess {
		return Owner
	}
	if rank(membership) == 0 {
		// Not a member, a guest, or not a role.
		return None
	}
	return membership
}

// Effective is effective_role(user, project) of §5.5: the role that a user
// acts with on a project, for the operations that the project authorises.
// instanceAdmin and membership are as for Workspace, with the membership of
// the project's workspace, and grant is the role of that membership's grant
// on the project, or None if it has none. A grant replaces the membership's
// role, raising or lowering it, and is the only way that a guest acts on a
// project. A grant is never owner (FR-TEN-04), so one of any role that a
// grant cannot have resolves to None.
func (p Policy) Effective(instanceAdmin bool, membership, grant Role) Role {
	if instanceAdmin && p.InstanceAdminAccess {
		return Owner
	}
	if rank(membership) == 0 && membership != Guest {
		// Not a member, or not a role.
		return None
	}
	if grant != None {
		if grant == Owner || rank(grant) == 0 {
			return None
		}
		return grant
	}
	if membership == Guest {
		// Guests need a grant.
		return None
	}
	return membership
}
