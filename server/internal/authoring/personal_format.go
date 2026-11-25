package authoring

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/personaldata"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
)

// The personal-data export (PRD FR-GDPR-01, §9.6, plan item 87, note N94) is
// a zip of what the instance holds of one account, as the inventory of §9.6
// says (package personaldata), in a format with a version of its own, which
// schema/personal-data/README.md describes.
const (
	// PersonalFormat names the format, in the archive's manifest.
	PersonalFormat = "storycreep.personal-data"
	// PersonalVersion is the version of the format that the server writes,
	// which a change that a reader could misread increments.
	PersonalVersion = 1
)

// PersonalManifest is the archive's personal-data.json, which says what it
// holds.
type PersonalManifest struct {
	Format        string                  `json:"format" enum:"storycreep.personal-data" doc:"The format's name."`
	FormatVersion int                     `json:"formatVersion" enum:"1" doc:"The version of the format."`
	Generator     published.GeneratorInfo `json:"generator" doc:"What wrote the archive."`
	ExportedAt    time.Time               `json:"exportedAt" doc:"When the archive's reading began, by the database's clock. Each section is read after it, as of a moment of its own, and the audit log's entries up to it."`
	UserID        string                  `json:"userId" doc:"The ID of the account that the archive is of, whose user asked for it."`
	PersonalData  bool                    `json:"personalData" enum:"true" doc:"Whether the archive holds personal data, which it always does: all of it is the user's."`
	Sections      []ManifestSection       `json:"sections" nullable:"false" doc:"The archive's JSON files beside this one, each with how many items it holds and its SHA-256."`
}

// PersonalAccount is the archive's account.json: the account, and the
// metadata of its credentials, never a hash or a secret.
type PersonalAccount struct {
	ID              string              `json:"id" doc:"The account's ID."`
	Email           string              `json:"email" doc:"The account's email address."`
	DisplayName     string              `json:"displayName" doc:"The account's display name."`
	Locale          string              `json:"locale,omitempty" doc:"The locale that the user prefers, as a BCP 47 tag, if they chose one."`
	Status          string              `json:"status" enum:"active" doc:"The account's status: an archive is only made of an active account."`
	EmailVerifiedAt *time.Time          `json:"emailVerifiedAt,omitempty" doc:"When the email address was verified, if it was."`
	InstanceAdmin   bool                `json:"instanceAdmin" doc:"Whether the account is an instance admin."`
	CreatedAt       time.Time           `json:"createdAt" doc:"When the account was created."`
	Credentials     PersonalCredentials `json:"credentials" doc:"What the account's credentials are, never a hash or a secret of one."`
}

// PersonalCredentials is the metadata of an account's credentials (PRD §9.6).
type PersonalCredentials struct {
	PasswordSet       bool       `json:"passwordSet" doc:"Whether the account has a password."`
	PasswordChangedAt *time.Time `json:"passwordChangedAt,omitempty" doc:"When the password was last set, if it is."`
}

// PersonalEmailLink is the metadata of an email link of the account's, which
// verifies its address or resets its password, never its secret's hash.
type PersonalEmailLink struct {
	Kind      string     `json:"kind" enum:"email_verification,password_reset" doc:"What the link does."`
	CreatedAt time.Time  `json:"createdAt" doc:"When it was asked for."`
	SentAt    *time.Time `json:"sentAt,omitempty" doc:"When it was last emailed, if it was."`
	ExpiresAt time.Time  `json:"expiresAt" doc:"When it expires."`
	UsedAt    *time.Time `json:"usedAt,omitempty" doc:"When it was used, if it was."`
}

// PersonalSession is a session of the account's, never its secret's hash.
type PersonalSession struct {
	ID         string     `json:"id" doc:"The session's ID."`
	IP         string     `json:"ip,omitempty" doc:"The IP address that it was started from."`
	UserAgent  string     `json:"userAgent,omitempty" doc:"The user agent that started it."`
	CreatedAt  time.Time  `json:"createdAt" doc:"When it was started, by signing in."`
	LastSeenAt time.Time  `json:"lastSeenAt" doc:"When it was last used, to a minute."`
	ExpiresAt  time.Time  `json:"expiresAt" doc:"When it expires, at the latest."`
	RevokedAt  *time.Time `json:"revokedAt,omitempty" doc:"When it was ended before its time, if it was."`
}

// PersonalToken is an API token of the account's, never its secret's hash.
type PersonalToken struct {
	ID          string     `json:"id" doc:"The token's ID."`
	Name        string     `json:"name" doc:"The token's name, which its user gave it."`
	WorkspaceID string     `json:"workspaceId" doc:"The token's workspace."`
	ProjectID   string     `json:"projectId,omitempty" doc:"The one project that the token is narrowed to, if any."`
	Scopes      []string   `json:"scopes" nullable:"false" doc:"The token's scopes."`
	CreatedAt   time.Time  `json:"createdAt" doc:"When it was created."`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty" doc:"When it was last used, if it was."`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty" doc:"When it expires, if it does."`
	RevokedAt   *time.Time `json:"revokedAt,omitempty" doc:"When it was revoked, if it was."`
}

// PersonalMembership is a membership of the account's in a workspace.
type PersonalMembership struct {
	MembershipID  string        `json:"membershipId" doc:"The membership's ID."`
	WorkspaceID   string        `json:"workspaceId" doc:"The workspace's ID."`
	WorkspaceName string        `json:"workspaceName" doc:"The workspace's name."`
	Role          string        `json:"role" enum:"owner,admin,editor,commenter,viewer,guest" doc:"The user's role in the workspace."`
	JoinedAt      time.Time     `json:"joinedAt" doc:"When the user joined the workspace."`
	Grants        []MemberGrant `json:"grants" nullable:"false" doc:"The user's grants on the workspace's projects, each of which replaces their role on its project, in the order of the projects' IDs."`
}

// PersonalInvite is an invitation to the account's address, or one that the
// user accepted, never its secret's hash, nor who made it.
type PersonalInvite struct {
	ID            string     `json:"id" doc:"The invitation's ID."`
	WorkspaceID   string     `json:"workspaceId" doc:"The workspace that it invites to."`
	WorkspaceName string     `json:"workspaceName" doc:"The workspace's name."`
	Kind          string     `json:"kind" enum:"email,link" doc:"email for an invitation to the user's address, or link for one to whoever held its link, which the user accepted."`
	Role          string     `json:"role" enum:"owner,admin,editor,commenter,viewer,guest" doc:"The role that it gives."`
	Status        string     `json:"status" enum:"open,accepted,revoked,expired" doc:"Where it was as the archive's reading began."`
	CreatedAt     time.Time  `json:"createdAt" doc:"When it was made."`
	ExpiresAt     time.Time  `json:"expiresAt" doc:"When its link expires."`
	AcceptedAt    *time.Time `json:"acceptedAt,omitempty" doc:"When it was accepted, if it was."`
	RevokedAt     *time.Time `json:"revokedAt,omitempty" doc:"When it was revoked, if it was."`
}

// PersonalActedEntry is an entry of the audit log (PRD FR-TEN-07) whose actor
// is the user, as audit-log/acted.json holds it: the whole entry.
type PersonalActedEntry struct {
	ID           string    `json:"id" doc:"The entry's ID, a UUID."`
	WorkspaceID  string    `json:"workspaceId,omitempty" doc:"The workspace that it was done in, for an action that belongs to one."`
	Action       string    `json:"action" doc:"What was done, such as auth.login."`
	ActorUserID  string    `json:"actorUserId" doc:"Who did it: the user."`
	ActorTokenID string    `json:"actorTokenId,omitempty" doc:"The user's API token that they did it with, if they used one."`
	SubjectKind  string    `json:"subjectKind,omitempty" doc:"The kind of what it was done to, such as project."`
	SubjectID    string    `json:"subjectId,omitempty" doc:"The ID of what it was done to, of its kind, or a UUID of a kind that has no prefix."`
	Result       string    `json:"result" doc:"How it ended, such as success or denied."`
	IP           string    `json:"ip,omitempty" doc:"The IP address that it was asked from."`
	UserAgent    string    `json:"userAgent,omitempty" doc:"The user agent that asked for it."`
	RequestID    string    `json:"requestId,omitempty" doc:"The ID of the request that did it."`
	Metadata     Value     `json:"metadata" doc:"What else the entry says, by its action, which never holds a name or an email address."`
	CreatedAt    time.Time `json:"createdAt" doc:"When it was done."`
}

// PersonalActedOnEntry is an entry of the audit log about the user whose
// actor is someone else, or nobody, as audit-log/acted-on.json holds it: what
// was done, how it ended, and when, and the actor as the role that the action
// needs, never who it was, nor their IP address or user agent, which are
// their own personal data (PRD §9.6).
type PersonalActedOnEntry struct {
	Action    string    `json:"action" doc:"What was done, such as membership.remove."`
	Result    string    `json:"result" doc:"How it ended, such as success."`
	Actor     string    `json:"actor,omitempty" enum:"a workspace admin,an instance administrator" doc:"Who did it, as the role that the action needs: a workspace admin, an owner or an admin of the workspace, or an instance administrator, an instance admin or the instance's operator. Without it, nobody is known to have done it, as for a failed sign-in with the user's address, which anyone may have tried."`
	CreatedAt time.Time `json:"createdAt" doc:"When it was done."`
}

// PersonalComment is a comment that the user wrote, in the trash too.
type PersonalComment struct {
	ID          string         `json:"id" doc:"The comment's ID."`
	WorkspaceID string         `json:"workspaceId" doc:"Its workspace."`
	ProjectID   string         `json:"projectId" doc:"Its project."`
	ThreadID    string         `json:"threadId" doc:"Its thread's ID, the ID of the thread's first comment."`
	ParentID    string         `json:"parentId,omitempty" doc:"The first comment of the thread, for a reply."`
	Target      PersonalTarget `json:"target" doc:"What the thread is about."`
	Body        Text           `json:"body" doc:"The comment's text, a rich-text document."`
	CreatedAt   time.Time      `json:"createdAt" doc:"When it was posted."`
	EditedAt    *time.Time     `json:"editedAt,omitempty" doc:"When its text last changed after it was posted, if it did."`
	DeletedAt   *time.Time     `json:"deletedAt,omitempty" doc:"When the user deleted it, if they did: it is in its project's trash until it is purged."`
	ResolvedAt  *time.Time     `json:"resolvedAt,omitempty" doc:"When its thread was resolved, for a thread's first comment, if it is."`
}

// PersonalTarget is what a comment's thread is about.
type PersonalTarget struct {
	Kind string `json:"kind" enum:"node,connection,board,variable" doc:"The kind of what the thread is about."`
	ID   string `json:"id" doc:"Its ID."`
}

// PersonalNotification is a notification of the user's, never who made it.
type PersonalNotification struct {
	ID          string     `json:"id" doc:"The notification's ID."`
	WorkspaceID string     `json:"workspaceId" doc:"Its workspace."`
	ProjectID   string     `json:"projectId" doc:"Its comment's project."`
	CommentID   string     `json:"commentId" doc:"The comment that it tells of."`
	Kind        string     `json:"kind" enum:"mention" doc:"What it tells of: that the comment mentions the user."`
	CreatedAt   time.Time  `json:"createdAt" doc:"When the comment mentioned the user."`
	ReadAt      *time.Time `json:"readAt,omitempty" doc:"When the user read it, if they did."`
}

// PersonalReference is a reference to an entity of a project's content that
// the user created, last changed, uploaded, published, or resolved, without
// the content itself, which belongs to the workspace (PRD §9.6).
type PersonalReference struct {
	Entity      string    `json:"entity" enum:"project,folder,board,variable,component_type,attribute_def,component,node,branch_output,connection,validation_mute,play_state,asset,snapshot,comment" doc:"The kind of the entity, as the table of its rows is named, such as node."`
	ID          string    `json:"id" doc:"Its ID, of its kind, or a UUID of a kind that has no prefix."`
	WorkspaceID string    `json:"workspaceId" doc:"Its workspace."`
	ProjectID   string    `json:"projectId,omitempty" doc:"Its project, which a workspace's own asset has none of."`
	Relation    string    `json:"relation" enum:"created,updated,uploaded,published,resolved" doc:"What the user did to it: created it, changed it last (updated), uploaded it, published it, or resolved it, a comment's thread."`
	At          time.Time `json:"at" doc:"When the user did."`
}

// PersonalIdentity is an imported identity of a workspace that stands for the
// user, which an import of an archive that named the user made (FR-DATA-01).
type PersonalIdentity struct {
	WorkspaceID string    `json:"workspaceId" doc:"The workspace that the archive was imported into."`
	DisplayName string    `json:"displayName" doc:"The display name that the archive gave the user."`
	CreatedAt   time.Time `json:"createdAt" doc:"When the import made it."`
}

// PersonalSchemaPath is the path of the format's JSON Schema, from the
// repository's root.
const PersonalSchemaPath = "schema/personal-data/v1/archive.schema.json"

// PersonalSchemaID is the schema's $id, which names its version.
const PersonalSchemaID = "https://raw.githubusercontent.com/CyberCute-Studio/storycreep/main/" + PersonalSchemaPath

// personalFiles are the archive's JSON files, each with the type of its
// content.
var personalFiles = []struct {
	path string
	typ  reflect.Type
}{
	{personaldata.ManifestPath, reflect.TypeFor[PersonalManifest]()},
	{personaldata.AccountPath, reflect.TypeFor[PersonalAccount]()},
	{personaldata.EmailLinksPath, reflect.TypeFor[Items[PersonalEmailLink]]()},
	{personaldata.SessionsPath, reflect.TypeFor[Items[PersonalSession]]()},
	{personaldata.TokensPath, reflect.TypeFor[Items[PersonalToken]]()},
	{personaldata.MembershipsPath, reflect.TypeFor[Items[PersonalMembership]]()},
	{personaldata.InvitesPath, reflect.TypeFor[Items[PersonalInvite]]()},
	{personaldata.ActedPath, reflect.TypeFor[Items[PersonalActedEntry]]()},
	{personaldata.ActedOnPath, reflect.TypeFor[Items[PersonalActedOnEntry]]()},
	{personaldata.CommentsPath, reflect.TypeFor[Items[PersonalComment]]()},
	{personaldata.NotificationsPath, reflect.TypeFor[Items[PersonalNotification]]()},
	{personaldata.AttributionPath, reflect.TypeFor[Items[PersonalReference]]()},
	{personaldata.IdentitiesPath, reflect.TypeFor[Items[PersonalIdentity]]()},
}

// PersonalSchema returns the format's JSON Schema, generated from its types:
// of an object of an archive's JSON files, by their paths.
func PersonalSchema() []byte {
	r := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	properties := map[string]any{}
	required := make([]string, 0, len(personalFiles))
	for _, f := range personalFiles {
		properties[f.path] = map[string]any{"$ref": r.Schema(f.typ, true, "").Ref}
		required = append(required, f.path)
	}
	slices.Sort(required)
	doc := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     PersonalSchemaID,
		"title":   "Storycreep personal-data export, version 1",
		"description": "The JSON files of an export of what a Storycreep instance holds of one account (PRD " +
			"FR-GDPR-01, §9.6), by their paths in its zip. schema/personal-data/README.md describes the format.",
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
		"$defs":                r.Map(),
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		panic("authoring: the personal-data schema is not JSON: " + err.Error())
	}
	return append(data, '\n')
}

// personalReadme returns the archive's README, which says what it holds, that
// it holds personal data, and what of the instance's data about the user it
// leaves out, and why.
func personalReadme(m PersonalManifest) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("# Storycreep personal-data export")
	line("")
	line("This archive holds what this Storycreep instance keeps of the account %s, in version %d of the", m.UserID,
		m.FormatVersion)
	line("format %s, which `personal-data.json` describes and", m.Format)
	line("`schema/personal-data/v1/archive.schema.json` of the Storycreep repository defines. Its reading began at")
	line("%s.", m.ExportedAt.Format(time.RFC3339))
	line("")
	line("## Personal data")
	line("")
	line("**This archive contains personal data**: yours, as the account's user, such as your email address, the")
	line("IP addresses and user agents that you signed in and acted from, and the comments that you wrote. Store it")
	line("safely, and share it only with people who may see that data. It holds no password, no hash of one, and")
	line("no secret of a session, an API token, or a link, only when each was made and used.")
	line("")
	line("## What it holds")
	line("")
	line("From every workspace of the instance, whether or not you are still a member of it:")
	line("")
	for _, s := range m.Sections {
		line("- `%s`: %s, %d item(s)", s.Path, s.Name, s.Items)
	}
	line("")
	line("`audit-log/acted.json` holds each entry of the audit log whose actor is you, whole. `audit-log/acted-on.json`")
	line("holds each entry about you, your account, your memberships, or your API tokens whose actor is someone else,")
	line("or nobody: what was done, how it ended, and when, and who did it only as the role that it needs, %q or", personaldata.LabelWorkspaceAdmin)
	line("%q, never who they were, as their ID, IP address, and user agent are their own personal", personaldata.LabelInstanceAdmin)
	line("data. A failed sign-in with your address names nobody, as anyone may have tried it. The audit log holds")
	line("each entry for as long as the instance's retention keeps it.")
	line("")
	line("`attribution.json` names what you created, last changed, uploaded, published, or resolved in the projects")
	line("of the workspaces, by its kind and its ID, without its content, which belongs to its workspace.")
	line("")
	line("## What it leaves out")
	line("")
	line("- The history of the changes of each project's content (its operation log), which belongs to its workspace,")
	line("  and of which `attribution.json` holds who created and last changed each entity.")
	line("- Other people's rows that name you only as who did something for them: the memberships of the members")
	line("  whom you invited, the invitations that you made, and the notifications of the members whom you")
	line("  mentioned. The audit log's entries of what you did, in `audit-log/acted.json`, record them.")
	line("- The requests for exports and imports, which are kept for 7 days, and which the audit log records.")
	line("- Billing data: this instance keeps none. On the hosted service, the payment provider holds it, and its own")
	line("  export gives it.")
	return b.String()
}
