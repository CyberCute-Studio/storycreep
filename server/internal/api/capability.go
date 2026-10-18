package api

// Capability is what a caller must hold to use a route (PRD NFR-SEC-02).
// Every route declares one when it is registered, and a test walks the
// routers and fails on a route that did not. A route of a workspace declares
// the capability of the rows of the permission matrix (§5.6) that cover it,
// whose role the service checks (package authz), or, as reading the
// workspace, which no row covers, one of its own, and the capabilities of
// the later rows join as the routes that need them are added.
type Capability string

const (
	// Public routes need no credentials: the health and version endpoints,
	// registering an account, and signing in.
	Public Capability = "public"
	// ScrapeMetrics is reading /metrics. Whether it needs a credential
	// depends on the MetricsEndpoint capability of the configuration.
	ScrapeMetrics Capability = "metrics.scrape"
	// OwnAccount is the caller's own account: who is signed in, the
	// account's sessions and password, and signing out. It needs a signed-in
	// caller, whom the Mux's Authenticator finds.
	OwnAccount Capability = "account.own"
	// CreateWorkspace is creating a workspace, which any signed-in user may
	// do, becoming its owner (PRD FR-TEN-01).
	CreateWorkspace Capability = "workspace.create"
	// ReadWorkspace is reading a workspace, which needs a role in it other
	// than none (§5.5).
	ReadWorkspace Capability = "workspace.read"
	// ListMembers is listing a workspace's members and their roles, which
	// needs a role in it other than none (§5.6).
	ListMembers Capability = "workspace.members.list"
	// ManageWorkspaceSettings is changing a workspace's settings, and reading
	// its usage, which its settings show, which needs owner or admin (§5.6).
	ManageWorkspaceSettings Capability = "workspace.settings"
	// ManageMembers is inviting people to a workspace, its invitations,
	// removing its members, and changing their roles, which needs owner or
	// admin, and owner for granting the owner role, by a role change or an
	// invitation, and for demoting or removing an owner (§5.6).
	ManageMembers Capability = "workspace.members"
	// ListProjects is listing a workspace's projects, which needs a
	// membership of it: a guest is shown only the projects granted to them
	// (§5.5, FR-TEN-04 AC2).
	ListProjects Capability = "workspace.projects.list"
	// ManageProjects is creating a workspace's projects, deleting them to its
	// trash, listing its trash, and restoring them, which needs owner or admin
	// in the workspace (§5.6 "Create/delete projects").
	ManageProjects Capability = "workspace.projects"
	// ManageProjectGrants is listing, setting, and removing the members'
	// grants on a workspace's projects, which needs owner or admin in the
	// workspace, and owner for a grant of an owner (§5.6 "Manage project
	// grants", FR-TEN-04).
	ManageProjectGrants Capability = "workspace.grants"
	// ReadProject is reading a project, which needs an effective role on it
	// other than none (§5.5).
	ReadProject Capability = "project.read"
	// ChangeProjectSettings is changing a project's name, description, and
	// settings, and archiving and unarchiving it, which needs owner or admin
	// as the effective role on the project (§5.6).
	ChangeProjectSettings Capability = "project.settings"
	// ManageInstance is the instance-admin area (PRD FR-ADMIN-03), which
	// needs a signed-in instance admin, whom the service checks (package
	// account).
	ManageInstance Capability = "instance.manage"
)

// capabilities are the declared capabilities, each with whether it needs an
// authenticated caller (Authenticated).
var capabilities = map[Capability]bool{
	Public:        false,
	ScrapeMetrics: false,
	OwnAccount:    true,
	// The role that a workspace's capabilities need is resolved in the
	// service, from the authenticated user (package workspace), and package
	// authz decides what it allows.
	CreateWorkspace:         true,
	ReadWorkspace:           true,
	ListMembers:             true,
	ManageWorkspaceSettings: true,
	ManageMembers:           true,
	ListProjects:            true,
	ManageProjects:          true,
	ManageProjectGrants:     true,
	ReadProject:             true,
	ChangeProjectSettings:   true,
	ManageInstance:          true,
}

// Known reports whether c is a declared capability.
func (c Capability) Known() bool {
	_, ok := capabilities[c]
	return ok
}

// Authenticated reports whether a route that requires c needs an
// authenticated caller, whom the Mux's Authenticator finds before the route's
// operation runs (Register). ScrapeMetrics does not: /metrics checks its
// bearer token itself.
func (c Capability) Authenticated() bool {
	return capabilities[c]
}
