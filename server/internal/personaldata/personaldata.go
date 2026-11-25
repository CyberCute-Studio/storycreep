// Package personaldata is the inventory of the personal data that an instance
// holds of an account (PRD §9.6), which the personal-data export holds as the
// inventory says (FR-GDPR-01, plan note N94): which file of the archive holds
// each of its rows (Inventory), where every table that refers to an account
// keeps the reference and whether the export holds the rows that refer to the
// user who asks for it (References), what of the account's own row it holds
// (AccountColumns), and how it names someone else who acted on the user
// (ActorLabel).
//
// Package authoring builds the archive, and reads the account's own rows, its
// sessions, and its email links, which belong to no workspace, through the
// application's login. The user's rows of the workspaces, whether or not they
// are still a member of each, are read through the maintenance path (§5.4),
// since no transaction of the application sees more than one workspace:
// package maintenance implements Reader, which this package declares with the
// types of what it reads, so that neither package imports the other.
package personaldata

// The archive's files, by their paths in the zip, which package authoring's
// format describes (schema/personal-data).
const (
	// ManifestPath is the archive's manifest.
	ManifestPath = "personal-data.json"
	// ReadmePath is the archive's README, which says what it holds and what it
	// leaves out.
	ReadmePath = "README.md"
	// AccountPath holds the account and its credentials' metadata.
	AccountPath = "account.json"
	// EmailLinksPath holds the account's email links' metadata.
	EmailLinksPath = "email-links.json"
	// SessionsPath holds the account's sessions.
	SessionsPath = "sessions.json"
	// TokensPath holds the account's API tokens.
	TokensPath = "api-tokens.json"
	// MembershipsPath holds the account's memberships, with their grants.
	MembershipsPath = "memberships.json"
	// InvitesPath holds the invitations to the account's address, and those
	// that it accepted.
	InvitesPath = "invites.json"
	// ActedPath holds the audit entries whose actor is the user.
	ActedPath = "audit-log/acted.json"
	// ActedOnPath holds the audit entries about the user whose actor is
	// someone else, or nobody.
	ActedOnPath = "audit-log/acted-on.json"
	// CommentsPath holds the comments that the user wrote.
	CommentsPath = "comments.json"
	// NotificationsPath holds the user's notifications.
	NotificationsPath = "notifications.json"
	// AttributionPath holds the references to what the user created, last
	// changed, uploaded, published, or resolved.
	AttributionPath = "attribution.json"
	// IdentitiesPath holds the imported identities that stand for the user in
	// other workspaces.
	IdentitiesPath = "imported-identities.json"
)

// Row is a row of the inventory of PRD §9.6, with the files of the archive
// that hold what its "In export" column says.
type Row struct {
	// Data is the row's first column, as the PRD names it.
	Data string
	// Paths are the archive's files that hold it.
	Paths []string
}

// Inventory is the inventory of PRD §9.6, row by row, in its order, which
// TestInventoryIsThePRDs holds to the PRD's table.
var Inventory = []Row{
	{"Account", []string{AccountPath}},
	{"Credentials", []string{AccountPath}},
	{"Email links", []string{EmailLinksPath}},
	{"Sessions", []string{SessionsPath}},
	{"API tokens", []string{TokensPath}},
	{"Memberships and grants", []string{MembershipsPath}},
	{"Audit entries about the user", []string{ActedPath, ActedOnPath}},
	{"Comments", []string{CommentsPath}},
	{"Notifications", []string{NotificationsPath}},
	{"Authored content attribution", []string{AttributionPath}},
	{"Invites addressed to the user's email", []string{InvitesPath}},
	{"Imported identities", []string{IdentitiesPath}},
	// The README points to the payment provider's own export: no instance
	// holds billing data itself.
	{"Billing (hosted)", []string{ReadmePath}},
}

// UserReference is a column that refers to an account, as a foreign key to
// "user", and what the export holds of the rows whose column refers to the
// user who asks for it.
type UserReference struct {
	Table, Column string
	// Path is the archive's file that holds the rows, or "" for a column
	// whose rows the export leaves out.
	Path string
	// Why says why the export leaves the rows out, for a column without a
	// Path.
	Why string
}

// Why the export leaves out the rows of some columns.
const (
	whyOthers = "the row is another person's, which names the user only as who did something for them: "
	whyKept   = "a request that is kept for 7 days, and that the audit log records, whose entry the archive holds"
)

// References are the columns of the schema that refer to an account, every
// foreign key to "user", which TestEveryUserReferenceIsAccounted holds the
// schema to: a new one joins them, with the file that holds its rows, or why
// the export leaves them out. The columns of attribution, who created, last
// changed, uploaded, published, or resolved something, are Attributions,
// which attribution.json holds, and which are not listed here again.
var References = []UserReference{
	{Table: "session", Column: "user_id", Path: SessionsPath},
	{Table: "email_token", Column: "user_id", Path: EmailLinksPath},
	{Table: "api_token", Column: "user_id", Path: TokensPath},
	{Table: "membership", Column: "user_id", Path: MembershipsPath},
	{Table: "invite", Column: "accepted_by", Path: InvitesPath},
	{Table: "comment", Column: "created_by", Path: CommentsPath},
	{Table: "notification", Column: "user_id", Path: NotificationsPath},
	{Table: "membership", Column: "invited_by", Why: whyOthers + "the membership of a member whom the user " +
		"invited, which the audit log's entries of the invitation, of which the user is the actor, record"},
	{Table: "invite", Column: "created_by", Why: whyOthers + "an invitation that the user made, to someone " +
		"else's address, which the audit log's entries of the invitation, of which the user is the actor, record"},
	{Table: "invite", Column: "link_holder", Why: whyOthers + "an invitation whose link the user was given to " +
		"pass on, which the audit log's entries of the invitation, of which the user is the actor, record"},
	{Table: "notification", Column: "actor_id", Why: whyOthers + "the notification of a member whom the user " +
		"mentioned, whose comment comments.json holds"},
	{Table: "operation", Column: "actor_user_id", Why: "the history of the changes of a project's content, " +
		"which belongs to its workspace and is compacted (FR-GDPR-03); attribution.json holds who created and " +
		"last changed each entity"},
	{Table: "authoring_export", Column: "requested_by", Why: whyKept},
	{Table: "workspace_export", Column: "requested_by", Why: whyKept},
	{Table: "project_import", Column: "requested_by", Why: whyKept},
	{Table: "personal_data_export", Column: "user_id", Why: whyKept},
	{Table: "imported_user", Column: "user_id", Why: "the account of an imported identity, which stands for " +
		"someone whom an archive names and signs in nowhere, and is never an account that asks for an export; " +
		"imported-identities.json holds the identities that stand for the user, by their source_user_id"},
}

// Attribution is a column of a table of a project's content that names who
// created, last changed, uploaded, published, or resolved each row, which
// attribution.json holds a reference to, without the content itself, which
// belongs to the workspace (PRD §9.6).
type Attribution struct {
	Table, Column string
	// Relation is what the user did to the row, as the archive names it:
	// created, updated (last changed), uploaded, published, or resolved.
	Relation string
	// At is the column of when they did.
	At string
	// Project is the column of the row's project, which is the row's own ID
	// for a project.
	Project string
}

// Attributions are the columns of attribution, each a foreign key to "user",
// which TestEveryUserReferenceIsAccounted counts among the references. A row
// of the trash is held too, as it still names the user. Each table's rows are
// held under the table's name, as entity, with the ID of the kind of the same
// name (package ids), or as a UUID where there is no such kind.
var Attributions = []Attribution{
	{Table: "project", Column: "created_by", Relation: "created", At: "created_at", Project: "id"},
	{Table: "project", Column: "updated_by", Relation: "updated", At: "updated_at", Project: "id"},
	{Table: "folder", Column: "created_by", Relation: "created", At: "created_at", Project: "project_id"},
	{Table: "folder", Column: "updated_by", Relation: "updated", At: "updated_at", Project: "project_id"},
	{Table: "board", Column: "created_by", Relation: "created", At: "created_at", Project: "project_id"},
	{Table: "board", Column: "updated_by", Relation: "updated", At: "updated_at", Project: "project_id"},
	{Table: "variable", Column: "created_by", Relation: "created", At: "created_at", Project: "project_id"},
	{Table: "variable", Column: "updated_by", Relation: "updated", At: "updated_at", Project: "project_id"},
	{Table: "component_type", Column: "created_by", Relation: "created", At: "created_at", Project: "project_id"},
	{Table: "component_type", Column: "updated_by", Relation: "updated", At: "updated_at", Project: "project_id"},
	{Table: "attribute_def", Column: "created_by", Relation: "created", At: "created_at", Project: "project_id"},
	{Table: "attribute_def", Column: "updated_by", Relation: "updated", At: "updated_at", Project: "project_id"},
	{Table: "component", Column: "created_by", Relation: "created", At: "created_at", Project: "project_id"},
	{Table: "component", Column: "updated_by", Relation: "updated", At: "updated_at", Project: "project_id"},
	{Table: "node", Column: "created_by", Relation: "created", At: "created_at", Project: "project_id"},
	{Table: "node", Column: "updated_by", Relation: "updated", At: "updated_at", Project: "project_id"},
	{Table: "branch_output", Column: "created_by", Relation: "created", At: "created_at", Project: "project_id"},
	{Table: "branch_output", Column: "updated_by", Relation: "updated", At: "updated_at", Project: "project_id"},
	{Table: "connection", Column: "created_by", Relation: "created", At: "created_at", Project: "project_id"},
	{Table: "connection", Column: "updated_by", Relation: "updated", At: "updated_at", Project: "project_id"},
	{Table: "validation_mute", Column: "created_by", Relation: "created", At: "created_at", Project: "project_id"},
	{Table: "validation_mute", Column: "updated_by", Relation: "updated", At: "updated_at", Project: "project_id"},
	{Table: "play_state", Column: "created_by", Relation: "created", At: "created_at", Project: "project_id"},
	{Table: "play_state", Column: "updated_by", Relation: "updated", At: "updated_at", Project: "project_id"},
	{Table: "asset", Column: "uploaded_by", Relation: "uploaded", At: "created_at", Project: "project_id"},
	{Table: "snapshot", Column: "published_by", Relation: "published", At: "published_at", Project: "project_id"},
	{Table: "comment", Column: "resolved_by", Relation: "resolved", At: "resolved_at", Project: "project_id"},
}

// Relations are the relations of Attributions, which the format's enum lists.
var Relations = []string{"created", "updated", "uploaded", "published", "resolved"}

// AccountColumn is a column of the account's own row, "user", and what the
// export holds of it.
type AccountColumn struct {
	Column string
	// Held says that account.json holds the column's value, or, for
	// Credential, what it says of it.
	Held bool
	// Why says why the export leaves the column out, or what it holds of a
	// credential in its place.
	Why string
}

// AccountColumns are the columns of "user", which
// TestEveryAccountColumnIsAccounted holds the schema to: a new one joins them,
// held, or with why it is not, as a credential's hash, of which the export
// holds metadata alone (PRD §9.6, FR-GDPR-01 AC3).
var AccountColumns = []AccountColumn{
	{Column: "id", Held: true},
	{Column: "email", Held: true},
	{Column: "display_name", Held: true},
	{Column: "locale", Held: true},
	{Column: "status", Held: true},
	{Column: "email_verified_at", Held: true},
	{Column: "is_instance_admin", Held: true},
	{Column: "created_at", Held: true},
	{Column: "password_changed_at", Held: true},
	{Column: "password_hash", Why: "a credential, of which account.json says only whether it is set, and when it " +
		"was last set (password_changed_at)"},
	{Column: "updated_at", Why: "when the row last changed, of any of its columns, which says nothing of the user " +
		"beside the values that account.json holds"},
	{Column: "personal_workspace_created_at", Why: "when the account's first sign-in gave it its personal " +
		"workspace, whose creation the audit log records as the user's (workspace.create)"},
}
