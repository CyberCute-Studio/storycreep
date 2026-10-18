package audit

// Action names a type of event in the audit log, as its entries record it.
// An action, once released, keeps its meaning, because exports of the log
// name it (PRD FR-TEN-08).
type Action string

// The actions. Each is registered in events. The PR that adds a feature
// whose events FR-TEN-07 lists registers them here, and records them (plan
// item 12, standing checklist).
const (
	// ActionLogin is an attempt to sign in with an email address and a
	// password (FR-AUTH-02). It belongs to no workspace, because an account
	// signs in to all of its workspaces at once. Its subject is the account
	// that the address belongs to, if any, and a failed attempt for an
	// address that belongs to no account records the address as its keyed
	// hash instead (Entry.AttemptedEmail). A successful one names the
	// session that it started in its metadata's session_id, and a failed
	// one says why in its metadata's reason: invalid_credentials, or
	// locked, when too many failures in a row had locked the address, and
	// the password was not checked (FR-AUTH-05).
	ActionLogin Action = "auth.login"
	// ActionSessionRevoke is the end of a session before its time
	// (FR-AUTH-02): its user signed out of it, revoked it, or changed the
	// account's password, which revokes the account's other sessions, or
	// reset it through a link, which revokes all of them (FR-AUTH-03). Its
	// metadata's reason says which: sign_out, revoked, password_changed, or
	// password_reset. It belongs to no workspace, as a login does.
	ActionSessionRevoke Action = "auth.session_revoke"
	// ActionPasswordChange is an attempt by a signed-in user to change the
	// account's password, which fails when the current password given is
	// not the account's. It belongs to no workspace.
	ActionPasswordChange Action = "auth.password_change"
	// ActionPasswordReset is the replacement of an account's password
	// through a link that resets it (FR-AUTH-03), which signs the account
	// out everywhere. Its actor is the account, whose link it was. It
	// belongs to no workspace.
	ActionPasswordReset Action = "auth.password_reset"
	// ActionEmailVerify is the verification of an account's email address
	// through a link (FR-AUTH-03). Its actor is the account. It belongs to
	// no workspace.
	ActionEmailVerify Action = "auth.email_verify"
	// ActionLinkIssue is the issue of a link that resets an account's
	// password or verifies its email address, as text, on an instance that
	// sends no email (FR-AUTH-03): to an instance admin, who is its actor,
	// or to the operator's storycreep reset-password, without an actor. Its
	// subject is the account, and its metadata's kind says which link:
	// password_reset or email_verification. It belongs to no workspace.
	ActionLinkIssue Action = "auth.link_issue"
	// ActionWorkspaceCreate is the creation of a workspace (FR-TEN-01), whose
	// creator becomes its owner: by a user, or by the first sign-in of an
	// account, which gives it its personal workspace, as its metadata's
	// personal says. It belongs to the workspace that it creates, which is
	// its subject. The workspace that first-run setup creates is its owner's
	// personal workspace (FR-ADMIN-02).
	ActionWorkspaceCreate Action = "workspace.create"
	// ActionMembershipCreate is a user's becoming a member of a workspace,
	// as the operator's storycreep grant-role makes them (FR-ADMIN-06), or
	// their acceptance of an invitation, which its metadata's invite_id names
	// (FR-TEN-02), with the role that its metadata's role names. It belongs
	// to that workspace, and its subject is the membership. Its actor is the
	// user who accepted the invitation, and an entry without an actor is the
	// operator's, who acts through the command line.
	ActionMembershipCreate Action = "membership.create"
	// ActionMembershipRoleChange is a change of a membership's role, from
	// its metadata's previous_role to its role, by an owner or admin of the
	// workspace, its actor (FR-TEN-03), or by the operator's grant-role,
	// without one. It revokes the workspace's open invitations that the
	// member made, or whose link the member was last given as text, whose
	// role the new role may not give (ActionInviteRevoke). It belongs to the
	// membership's workspace, and its subject is the membership.
	ActionMembershipRoleChange Action = "membership.role_change"
	// ActionMembershipRemove is the removal of a member from a workspace by
	// one of its owners or admins (FR-TEN-02), its actor, which ends the
	// membership and its project grants, and revokes the workspace's open
	// invitations that the member made, whose link the member was last given
	// as text, or that are to the member's address (ActionInviteRevoke). It
	// belongs to the workspace, its subject is the membership, its metadata's
	// user_id names the member, and its role the role that the membership
	// had.
	ActionMembershipRemove Action = "membership.remove"
	// ActionInviteCreate is the invitation of an address, or of whoever holds
	// a link, to join a workspace with the role that its metadata's role
	// names (FR-TEN-02), by an owner or admin of the workspace, its actor. Its
	// metadata's kind says which: email or link, and its emailed whether the
	// instance emails the invitation's link, rather than give it to the actor
	// as text. It belongs to the workspace, and its subject is the
	// invitation. An entry never holds the invited address (§9.6).
	ActionInviteCreate Action = "invite.create"
	// ActionInviteResend is the renewal of an invitation by an owner or admin
	// of its workspace, which gives it a new link, in place of the old one,
	// and 14 days more, with the metadata of ActionInviteCreate.
	ActionInviteResend Action = "invite.resend"
	// ActionInviteRevoke is the revocation of an invitation by an owner or
	// admin of its workspace, after which its link is refused (FR-TEN-02
	// AC1). Its metadata's role and kind are the invitation's. An invitation
	// that the removal of a member revokes, because the member made it, was
	// last given its link as text, or has the address that it invites, has
	// the removal's actor, and its metadata's membership_id names the removed
	// membership (ActionMembershipRemove); one that a change of a member's
	// role revokes has the change's actor, or none for the operator's, and
	// its membership_id names the membership (ActionMembershipRoleChange).
	ActionInviteRevoke Action = "invite.revoke"
	// ActionInviteAccept is the acceptance of an invitation by the user who
	// accepts it, its actor, who joins the workspace (ActionMembershipCreate)
	// with the membership that its metadata's membership_id names. Its
	// metadata's role and kind are the invitation's, and its new_account
	// says whether the acceptance created the user's account.
	ActionInviteAccept Action = "invite.accept"
	// ActionProjectCreate is the creation of a project by an owner or admin
	// of its workspace, its actor (FR-PROJ-01). It belongs to the workspace,
	// and its subject is the project.
	ActionProjectCreate Action = "project.create"
	// ActionProjectDelete is the deletion of a project to its workspace's
	// trash by an owner or admin of the workspace, its actor, from which it
	// can be restored until it is purged (FR-PROJ-01 AC3), and
	// ActionProjectRestore is its restoration. Each belongs to the workspace,
	// and its subject is the project.
	ActionProjectDelete  Action = "project.delete"
	ActionProjectRestore Action = "project.restore"
	// ActionProjectPurge is the hard deletion of a project whose time in the
	// trash has passed, with every row of its content, by the purge job,
	// without an actor. It belongs to the workspace, and its subject is the
	// project, which no longer exists.
	ActionProjectPurge Action = "project.purge"
	// ActionProjectPublishPolicyChange is a change of the least role that may
	// publish a project's snapshots, from its metadata's
	// previous_publish_min_role to its publish_min_role, by an owner or admin
	// of the project, its actor (FR-SNAP-01 AC6). It belongs to the
	// project's workspace, and its subject is the project.
	ActionProjectPublishPolicyChange Action = "project.publish_policy_change"
	// ActionProjectGrantCreate is a member's new grant on a project, with the
	// role that its metadata's role names, which replaces the member's role
	// in the workspace on that project, by an owner or admin of the
	// workspace, its actor (FR-TEN-04). ActionProjectGrantChange is a change
	// of a grant's role, from its metadata's previous_role to its role, and
	// ActionProjectGrantRemove is a grant's removal, after which the member
	// acts on the project with their role in the workspace again, with the
	// grant's role as its role. Each belongs to the workspace, its subject is
	// the membership, and its metadata's project_id names the project. The
	// grants that the removal of a membership, or the purge of a project,
	// deletes are recorded by that (ActionMembershipRemove,
	// ActionProjectPurge).
	ActionProjectGrantCreate Action = "project_grant.create"
	ActionProjectGrantChange Action = "project_grant.change"
	ActionProjectGrantRemove Action = "project_grant.remove"
	// ActionTokenCreate is the creation of an API token (FR-AUTH-04), by its
	// owner, its actor, or by the operator's storycreep create-token, without
	// an actor, for the owner that its metadata's user_id names. Its
	// metadata's project_id names the project that it is narrowed to, if
	// any, its scopes the scopes that it holds, and its expires_at when it
	// expires, if it does. It belongs to the token's workspace, and its
	// subject is the token. An entry never holds the token's name, which its
	// owner wrote, nor its secret.
	ActionTokenCreate Action = "token.create"
	// ActionTokenRevoke is the revocation of an API token, after which every
	// request made with it is refused: by its owner, or by an owner or admin
	// of its workspace, its actor, or by the removal of its owner's
	// membership, whose actor it has, and whose membership its metadata's
	// membership_id names (ActionMembershipRemove). Its metadata's user_id
	// names the token's owner, and its project_id the project that it is
	// narrowed to, if any.
	ActionTokenRevoke Action = "token.revoke"
	// ActionTokenUseDenied is a request made with an API token that is
	// refused, though the token is one: its actor is the token, and its
	// owner, and its metadata's reason says why: revoked, expired,
	// owner_inactive, when its owner's account is not active, owner_not_member,
	// when its owner is no longer a member of its workspace, session_required,
	// on a route that takes no token, or insufficient_scope, on one that
	// needs a scope that it does not hold, which its scope names (NFR-SEC-05).
	// It belongs to the token's workspace, and its subject is the token. A
	// token's refusals are recorded once a minute at most, the first of each
	// minute, so that a client that repeats a refused request does not flood
	// the log (package apitoken). A request with a secret that is no token's
	// is recorded nowhere, because it belongs to no workspace that the server
	// knows of.
	ActionTokenUseDenied Action = "token.use_denied"
	// ActionInstanceAdminGrant makes an account an instance admin, as the
	// operator's storycreep create-user --instance-admin and grant-role do,
	// and ActionInstanceAdminRevoke makes it no longer one, as grant-role
	// --revoke does. Each belongs to no workspace, and its subject is the
	// account.
	ActionInstanceAdminGrant  Action = "instance.admin_grant"
	ActionInstanceAdminRevoke Action = "instance.admin_revoke"
)

// Result is how an event ended.
type Result string

// The results.
const (
	// ResultSuccess is an action that happened.
	ResultSuccess Result = "success"
	// ResultFailure is an action that was attempted but did not happen, as
	// a login with a wrong password does not.
	ResultFailure Result = "failure"
	// ResultDenied is an action that was refused for want of a permission,
	// as the use of a revoked API token is (FR-TEN-07).
	ResultDenied Result = "denied"
)

// eventSpec registers a type of event: what Record allows its entries to
// hold.
type eventSpec struct {
	action Action
	// inWorkspace says that each event belongs to a workspace. An event that
	// belongs to none, such as a login, is recorded without one, in a
	// transaction without one.
	inWorkspace bool
	// subject is the kind of entity that the event is about, as the name of
	// the table that the subject's ID is a row of, or "" for an event
	// without a subject. An entry of an event with a subject may still lack
	// one, as a failed login for an address of no account does.
	subject string
	// results are the results that the event can have.
	results []Result
	// metadata are the keys that an entry's metadata may hold.
	metadata []string
	// attemptedEmail says that a failed event may record an email address
	// that belongs to no account, as its keyed hash (Entry.AttemptedEmail).
	attemptedEmail bool
}

// events is the registry of event types.
var events = []eventSpec{
	{action: ActionLogin, subject: "user", results: []Result{ResultSuccess, ResultFailure},
		metadata: []string{"session_id", "reason"}, attemptedEmail: true},
	{action: ActionSessionRevoke, subject: "session", results: []Result{ResultSuccess}, metadata: []string{"reason"}},
	{action: ActionPasswordChange, subject: "user", results: []Result{ResultSuccess, ResultFailure}},
	{action: ActionPasswordReset, subject: "user", results: []Result{ResultSuccess}},
	{action: ActionEmailVerify, subject: "user", results: []Result{ResultSuccess}},
	{action: ActionLinkIssue, subject: "user", results: []Result{ResultSuccess}, metadata: []string{"kind"}},
	{action: ActionWorkspaceCreate, inWorkspace: true, subject: "workspace", results: []Result{ResultSuccess},
		metadata: []string{"personal"}},
	{action: ActionMembershipCreate, inWorkspace: true, subject: "membership", results: []Result{ResultSuccess},
		metadata: []string{"role", "invite_id"}},
	{action: ActionMembershipRoleChange, inWorkspace: true, subject: "membership", results: []Result{ResultSuccess},
		metadata: []string{"role", "previous_role"}},
	{action: ActionMembershipRemove, inWorkspace: true, subject: "membership", results: []Result{ResultSuccess},
		metadata: []string{"role", "user_id"}},
	{action: ActionInviteCreate, inWorkspace: true, subject: "invite", results: []Result{ResultSuccess},
		metadata: []string{"role", "kind", "emailed"}},
	{action: ActionInviteResend, inWorkspace: true, subject: "invite", results: []Result{ResultSuccess},
		metadata: []string{"role", "kind", "emailed"}},
	{action: ActionInviteRevoke, inWorkspace: true, subject: "invite", results: []Result{ResultSuccess},
		metadata: []string{"role", "kind", "membership_id"}},
	{action: ActionInviteAccept, inWorkspace: true, subject: "invite", results: []Result{ResultSuccess},
		metadata: []string{"role", "kind", "membership_id", "new_account"}},
	{action: ActionProjectCreate, inWorkspace: true, subject: "project", results: []Result{ResultSuccess}},
	{action: ActionProjectDelete, inWorkspace: true, subject: "project", results: []Result{ResultSuccess}},
	{action: ActionProjectRestore, inWorkspace: true, subject: "project", results: []Result{ResultSuccess}},
	{action: ActionProjectPurge, inWorkspace: true, subject: "project", results: []Result{ResultSuccess}},
	{action: ActionProjectPublishPolicyChange, inWorkspace: true, subject: "project", results: []Result{ResultSuccess},
		metadata: []string{"publish_min_role", "previous_publish_min_role"}},
	{action: ActionProjectGrantCreate, inWorkspace: true, subject: "membership", results: []Result{ResultSuccess},
		metadata: []string{"project_id", "role"}},
	{action: ActionProjectGrantChange, inWorkspace: true, subject: "membership", results: []Result{ResultSuccess},
		metadata: []string{"project_id", "role", "previous_role"}},
	{action: ActionProjectGrantRemove, inWorkspace: true, subject: "membership", results: []Result{ResultSuccess},
		metadata: []string{"project_id", "role"}},
	{action: ActionTokenCreate, inWorkspace: true, subject: "api_token", results: []Result{ResultSuccess},
		metadata: []string{"user_id", "project_id", "scopes", "expires_at"}},
	{action: ActionTokenRevoke, inWorkspace: true, subject: "api_token", results: []Result{ResultSuccess},
		metadata: []string{"user_id", "project_id", "membership_id"}},
	{action: ActionTokenUseDenied, inWorkspace: true, subject: "api_token", results: []Result{ResultDenied},
		metadata: []string{"reason", "scope"}},
	{action: ActionInstanceAdminGrant, subject: "user", results: []Result{ResultSuccess}},
	{action: ActionInstanceAdminRevoke, subject: "user", results: []Result{ResultSuccess}},
}

// lookup returns the registration of a, and false if a is not registered.
func lookup(a Action) (eventSpec, bool) {
	for _, s := range events {
		if s.action == a {
			return s, true
		}
	}
	return eventSpec{}, false
}
