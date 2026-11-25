package personaldata

import "github.com/CyberCute-Studio/storycreep/server/internal/audit"

// The labels that stand for an actor who is not the user, in an audit entry
// about the user (PRD §9.6): the role that the action needs, never who it was,
// as their ID, IP address, and user agent are their own personal data.
const (
	// LabelWorkspaceAdmin is an owner or an admin of the workspace, who
	// changes a member's role or grants, revokes their API tokens, or removes
	// them.
	LabelWorkspaceAdmin = "a workspace admin"
	// LabelInstanceAdmin is an instance admin, or the instance's operator,
	// who acts through the command line, without an actor.
	LabelInstanceAdmin = "an instance administrator"
)

// labels are how an entry about the user names its actor, by its action: one
// who is not the user, and none, as of the operator, through the command line.
// An empty label names no actor, as for a failed sign-in with the user's
// address, which anyone may have made.
type labels struct{ other, none string }

// actorLabels are the labels of every action of the audit log, which
// TestEveryActionIsLabelled holds the registry to. An action that is about the
// user only as its actor, as a project's creation, has the labels that its
// actor's role would have, since no entry of it is about the user otherwise.
var actorLabels = map[audit.Action]labels{
	// A sign-in's actor is the user who signed in, and a failed one has none.
	audit.ActionLogin:          {},
	audit.ActionSessionRevoke:  {},
	audit.ActionPasswordChange: {},
	audit.ActionPasswordReset:  {},
	audit.ActionEmailVerify:    {},
	// A link that an instance admin was given, or the operator's
	// reset-password.
	audit.ActionLinkIssue:       {other: LabelInstanceAdmin, none: LabelInstanceAdmin},
	audit.ActionWorkspaceCreate: {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	// The operator's grant-role makes a member, and changes a member's role,
	// as an owner or an admin of the workspace does.
	audit.ActionMembershipCreate:     {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionMembershipRoleChange: {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionMembershipRemove:     {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionInviteCreate:         {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionInviteResend:         {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionInviteRevoke:         {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionInviteAccept:         {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionProjectCreate:        {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionProjectDelete:        {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionProjectRestore:       {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionProjectPurge:         {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionProjectPublishPolicyChange: {other: LabelWorkspaceAdmin,
		none: LabelInstanceAdmin},
	audit.ActionSnapshotPublish:    {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionProjectExport:      {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionWorkspaceExport:    {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionProjectImport:      {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionProjectDuplicate:   {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionProjectGrantCreate: {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionProjectGrantChange: {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionProjectGrantRemove: {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	// The operator's create-token makes a token for its owner, and an owner or
	// an admin of the workspace revokes another member's.
	audit.ActionTokenCreate: {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	audit.ActionTokenRevoke: {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	// A refused use's actor is the token's owner.
	audit.ActionTokenUseDenied: {other: LabelWorkspaceAdmin, none: LabelInstanceAdmin},
	// The operator's create-user and grant-role.
	audit.ActionInstanceAdminGrant:  {other: LabelInstanceAdmin, none: LabelInstanceAdmin},
	audit.ActionInstanceAdminRevoke: {other: LabelInstanceAdmin, none: LabelInstanceAdmin},
	audit.ActionPersonalDataExport:  {},
}

// ActorLabel returns the label that stands for the actor of an entry of
// action about the user whose actor is someone else, or nobody if hasActor is
// false, or "" for an entry that names no actor. An action that the registry
// no longer has, which no released action is, names none.
func ActorLabel(action string, hasActor bool) string {
	l := actorLabels[audit.Action(action)]
	if hasActor {
		return l.other
	}
	return l.none
}
