package api

// Capability is what a caller must hold to use a route (PRD NFR-SEC-02).
// Every route declares one when it is registered, and a test walks the
// routers and fails on a route that did not. The capabilities of PRD §5.6
// join as the routes that need them are added (plan item 20).
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
	// ManageWorkspaceSettings is changing a workspace's settings, which
	// needs owner or admin (§5.6).
	ManageWorkspaceSettings Capability = "workspace.settings"
	// ManageMembers is inviting people to a workspace, its invitations, and
	// removing its members, which needs owner or admin, and owner for an
	// invitation as owner and for removing an owner (§5.6).
	ManageMembers Capability = "workspace.members"
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
	// service, from the authenticated user (package workspace).
	CreateWorkspace:         true,
	ReadWorkspace:           true,
	ManageWorkspaceSettings: true,
	ManageMembers:           true,
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
