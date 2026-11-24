package authoring

import (
	"encoding/json"
	"reflect"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/published"
)

// The workspace export (PRD FR-DATA-02, plan item 86, note N93) is a zip of a
// whole workspace: each of its projects outside the trash as an authoring
// export of its own, nested in it, its members, its audit log, and its own
// assets, in a format with a version of its own, which
// schema/workspace/README.md describes.
const (
	// WorkspaceFormat names the format, in the archive's manifest.
	WorkspaceFormat = "storycreep.workspace"
	// WorkspaceVersion is the version of the format that the server writes,
	// which a change that a reader could misread increments.
	WorkspaceVersion = 1
	// WorkspaceManifestPath is the path of the archive's manifest.
	WorkspaceManifestPath = "workspace.json"
	// WorkspaceMembersPath is the path of the members' file.
	WorkspaceMembersPath = "members.json"
	// WorkspaceAuditPath is the path of the audit log's file.
	WorkspaceAuditPath = "audit-log.json"
	// WorkspaceAssetsPath is the path of the index of the workspace's own
	// assets, whose files are beside it, under assets/.
	WorkspaceAssetsPath = "assets/index.json"
	// workspaceProjectsDir holds each project's archive, as <its ID>.zip.
	workspaceProjectsDir = "projects/"
)

// WorkspaceManifest is the archive's workspace.json, which says what it holds.
type WorkspaceManifest struct {
	Format                 string                  `json:"format" enum:"storycreep.workspace" doc:"The format's name."`
	FormatVersion          int                     `json:"formatVersion" enum:"1" doc:"The version of the format."`
	AuthoringFormatVersion int                     `json:"authoringFormatVersion" enum:"1" doc:"The version of the authoring format (schema/authoring) of each project's archive, which the project import takes as it is."`
	Generator              published.GeneratorInfo `json:"generator" doc:"What wrote the archive."`
	ExportedAt             time.Time               `json:"exportedAt" doc:"When the archive's reading of the workspace began. Each project's archive says when its own content was read, as each is read as of a moment of its own."`
	Workspace              ManifestWorkspace       `json:"workspace" doc:"The workspace."`
	PersonalData           bool                    `json:"personalData" enum:"true" doc:"Whether the archive holds personal data, which it always does: the members' display names and email addresses, the audit log's IP addresses and user agents, and each project's comments and the names of the people involved."`
	Projects               []WorkspaceProject      `json:"projects" nullable:"false" doc:"Each project that the archive holds, in the order of their IDs: every project of the workspace outside its trash as the archive came to read it, archived ones too."`
	TrashedProjects        []TrashedProject        `json:"trashedProjects" nullable:"false" doc:"The projects that the archive does not hold, in the order of their IDs: those in the workspace's trash as the archive listed the projects, and those that went there, or were deleted for good, after that and before the archive came to read them. One that went there once its reading had begun is among the projects, as that reading found it."`
	Sections               []ManifestSection       `json:"sections" nullable:"false" doc:"The archive's JSON files beside this one: the members, the audit log, and the workspace's own assets."`
}

// ManifestWorkspace names the workspace of an archive.
type ManifestWorkspace struct {
	ID        string    `json:"id" doc:"The workspace's ID."`
	Name      string    `json:"name" doc:"The workspace's name."`
	Slug      string    `json:"slug" doc:"The workspace's slug."`
	Settings  Value     `json:"settings" doc:"The workspace's settings as it keeps them, such as its default_locale."`
	CreatedAt time.Time `json:"createdAt" doc:"When the workspace was created."`
}

// WorkspaceProject is a project that a workspace's archive holds.
type WorkspaceProject struct {
	ID        string `json:"id" doc:"The project's ID."`
	Name      string `json:"name" doc:"The project's name."`
	Slug      string `json:"slug" doc:"The project's slug."`
	Archived  bool   `json:"archived" doc:"Whether the project is archived, and so read-only."`
	Path      string `json:"path" doc:"The path of the project's archive: a full authoring export of its draft, with its comments, play states, release history, and assets, in the format of schema/authoring, which the project import takes as it is."`
	SizeBytes int64  `json:"sizeBytes" minimum:"0" doc:"The size of the project's archive."`
	SHA256    string `json:"sha256" pattern:"^[0-9a-f]{64}$" doc:"The SHA-256 of the project's archive."`
}

// TrashedProject is a project of the workspace that its archive does not hold.
type TrashedProject struct {
	ID        string     `json:"id" doc:"The project's ID."`
	Name      string     `json:"name" doc:"The project's name."`
	Slug      string     `json:"slug" doc:"The project's slug."`
	DeletedAt *time.Time `json:"deletedAt,omitempty" doc:"When it went to the trash, if it was there as the archive listed the projects. Without it, it went there, or was deleted for good, after that and before the archive came to read it."`
}

// WorkspaceMember is a member of the workspace, as members.json holds them.
type WorkspaceMember struct {
	MembershipID string        `json:"membershipId" doc:"The membership's ID."`
	UserID       string        `json:"userId" doc:"The user's ID."`
	DisplayName  string        `json:"displayName" doc:"Their display name."`
	Email        string        `json:"email" doc:"Their email address."`
	Role         string        `json:"role" enum:"owner,admin,editor,commenter,viewer,guest" doc:"Their role in the workspace."`
	JoinedAt     time.Time     `json:"joinedAt" doc:"When they joined the workspace."`
	Grants       []MemberGrant `json:"grants" nullable:"false" doc:"Their grants on the workspace's projects, each of which replaces their role on its project, in the order of the projects' IDs."`
}

// MemberGrant is a member's role on one project (PRD FR-TEN-04).
type MemberGrant struct {
	ProjectID string `json:"projectId" doc:"The project's ID."`
	Role      string `json:"role" enum:"admin,editor,commenter,viewer" doc:"The member's role on the project."`
}

// WorkspaceAuditEntry is an entry of the workspace's audit log (PRD
// FR-TEN-07), as audit-log.json holds them.
type WorkspaceAuditEntry struct {
	ID           string    `json:"id" doc:"The entry's ID, a UUID."`
	Action       string    `json:"action" doc:"What was done, such as project.create."`
	ActorUserID  string    `json:"actorUserId,omitempty" doc:"Who did it, if a user did."`
	ActorTokenID string    `json:"actorTokenId,omitempty" doc:"The API token that they did it with, if they used one."`
	SubjectKind  string    `json:"subjectKind,omitempty" doc:"The kind of what it was done to, such as project."`
	SubjectID    string    `json:"subjectId,omitempty" doc:"The ID of what it was done to, of its kind, or a UUID of a kind that has no prefix."`
	Result       string    `json:"result" doc:"How it ended, such as success or denied."`
	IP           string    `json:"ip,omitempty" doc:"The IP address that it was asked from."`
	UserAgent    string    `json:"userAgent,omitempty" doc:"The user agent that asked for it."`
	RequestID    string    `json:"requestId,omitempty" doc:"The ID of the request that did it."`
	Metadata     Value     `json:"metadata" doc:"What else the entry says, by its action, which never holds a name or an email address."`
	CreatedAt    time.Time `json:"createdAt" doc:"When it was done."`
}

// WorkspaceSchemaPath is the path of the format's JSON Schema, from the
// repository's root.
const WorkspaceSchemaPath = "schema/workspace/v1/archive.schema.json"

// WorkspaceSchemaID is the schema's $id, which names its version.
const WorkspaceSchemaID = "https://raw.githubusercontent.com/CyberCute-Studio/storycreep/main/" + WorkspaceSchemaPath

// WorkspaceSchema returns the format's JSON Schema, generated from its types:
// of an object of an archive's JSON files, by their paths. The archives of the
// projects and the files of the assets are not JSON of this format: each
// project's validates against the authoring format's schema.
func WorkspaceSchema() []byte {
	r := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	properties := map[string]any{
		WorkspaceManifestPath: map[string]any{"$ref": r.Schema(reflect.TypeFor[WorkspaceManifest](), true, "").Ref},
		WorkspaceMembersPath: map[string]any{
			"$ref": r.Schema(reflect.TypeFor[Items[WorkspaceMember]](), true, "").Ref},
		WorkspaceAuditPath: map[string]any{
			"$ref": r.Schema(reflect.TypeFor[Items[WorkspaceAuditEntry]](), true, "").Ref},
		WorkspaceAssetsPath: map[string]any{"$ref": r.Schema(reflect.TypeFor[Items[Asset]](), true, "").Ref},
	}
	required := []string{WorkspaceManifestPath, WorkspaceMembersPath, WorkspaceAuditPath, WorkspaceAssetsPath}
	slices.Sort(required)
	doc := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     WorkspaceSchemaID,
		"title":   "Storycreep workspace export, version 1",
		"description": "The JSON files of an export of a whole Storycreep workspace (PRD FR-DATA-02), by their paths in " +
			"its zip. Each project is an authoring export of its own, under projects/, which schema/authoring " +
			"defines. schema/workspace/README.md describes the format.",
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
		"$defs":                r.Map(),
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		panic("authoring: the workspace schema is not JSON: " + err.Error())
	}
	return append(data, '\n')
}
