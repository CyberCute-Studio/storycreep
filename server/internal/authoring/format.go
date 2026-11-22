// Package authoring builds the authoring export of a project (PRD FR-DATA-01,
// FR-DATA-04, plan item 83, note N90): a zip of its authoring data, of its
// draft or of one of its releases, with its comments, play states, and
// annotations, its release history, and its assets, in a documented format
// that has a schema version of its own, which schema/authoring/README.md
// describes. A job builds the archive into the assets' store, and the user
// who asked for it alone downloads it, until it expires.
//
// What the archive holds of a project is listed once, by the section registry
// (Sections), which plan item 84's import reads too, so that a table of
// authoring data cannot be exported and not imported, or the reverse.
package authoring

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

const (
	// Format names the format, in the archive's manifest.
	Format = "storycreep.authoring"
	// Version is the version of the format that the server writes, which a
	// change that an importer could misread increments.
	Version = 1
	// ManifestPath is the path of the archive's manifest.
	ManifestPath = "authoring.json"
	// ReadmePath is the path of the archive's README.
	ReadmePath = "README.md"
)

// Profile says how much of a project's collaboration an archive holds
// (FR-DATA-01 AC5).
type Profile string

const (
	// ProfileFull holds the comments, the play states, and the annotations,
	// and the names of the people involved, so it holds personal data (AC6).
	ProfileFull Profile = "full"
	// ProfileContent holds none of them, and names nobody.
	ProfileContent Profile = "content"
)

// Value is a JSON value of the project's that the archive holds as it is
// stored, as a variable's default.
type Value json.RawMessage

// MarshalJSON writes the value as it is.
func (v Value) MarshalJSON() ([]byte, error) {
	if len(v) == 0 {
		return []byte("null"), nil
	}
	return v, nil
}

// UnmarshalJSON keeps a copy of the value.
func (v *Value) UnmarshalJSON(data []byte) error {
	*v = bytes.Clone(data)
	return nil
}

// Schema takes any JSON value.
func (Value) Schema(huma.Registry) *huma.Schema { return &huma.Schema{} }

// Text is a rich-text document, in its canonical form, as it is stored
// (package richtext), which the archive holds as it is.
type Text json.RawMessage

// MarshalJSON writes the document as it is.
func (t Text) MarshalJSON() ([]byte, error) {
	if len(t) == 0 {
		return []byte("null"), nil
	}
	return t, nil
}

// UnmarshalJSON keeps a copy of the document.
func (t *Text) UnmarshalJSON(data []byte) error {
	*t = bytes.Clone(data)
	return nil
}

// Schema is the rich-text document's.
func (Text) Schema(r huma.Registry) *huma.Schema { return richtext.Doc{}.Schema(r) }

// Decimal is a number of a node's geometry, as the decimal that it is stored
// as, so that it is held exactly.
type Decimal string

// MarshalJSON writes the decimal as a JSON number.
func (d Decimal) MarshalJSON() ([]byte, error) {
	if !json.Valid([]byte(d)) {
		return nil, fmt.Errorf("authoring: %q is not a number", string(d))
	}
	return []byte(d), nil
}

// UnmarshalJSON reads a JSON number.
func (d *Decimal) UnmarshalJSON(data []byte) error {
	*d = Decimal(data)
	return nil
}

// Schema is a number's.
func (Decimal) Schema(huma.Registry) *huma.Schema { return &huma.Schema{Type: huma.TypeNumber} }

// Items is the content of a section's file: its items, in a stable order.
type Items[T any] struct {
	Items []T `json:"items" nullable:"false" doc:"The section's items."`
}

// Manifest is the archive's authoring.json, which says what it holds.
type Manifest struct {
	Format                 string                  `json:"format" enum:"storycreep.authoring" doc:"The format's name."`
	FormatVersion          int                     `json:"formatVersion" enum:"1" doc:"The version of the format, which an importer of an older one refuses."`
	PublishedSchemaVersion int                     `json:"publishedSchemaVersion" minimum:"1" doc:"The version of the published game format that the server wrote when it made the archive. Each release in the archive names its own."`
	Generator              published.GeneratorInfo `json:"generator" doc:"What wrote the archive."`
	ExportedAt             time.Time               `json:"exportedAt" doc:"When the archive's content was read."`
	Project                ManifestProject         `json:"project" doc:"The project."`
	Source                 Source                  `json:"source" doc:"What the archive's content is of."`
	Profile                Profile                 `json:"profile" enum:"full,content" doc:"full holds the comments, the play states, and the annotations, and names the people involved; content holds none of them."`
	PersonalData           bool                    `json:"personalData" doc:"Whether the archive holds personal data, as a full archive does: the display names of the people who wrote, resolved, or are mentioned in its comments, saved its play states, muted its findings, or published its releases."`
	IncludesAssets         bool                    `json:"includesAssets" doc:"Whether the archive holds the project's assets, under assets/."`
	IncludesReleases       bool                    `json:"includesReleases" doc:"Whether the archive holds the project's release history, under releases/."`
	Sections               []ManifestSection       `json:"sections" nullable:"false" doc:"Each section that the archive holds. A section that it does not list is not in it."`
	LeftOut                LeftOut                 `json:"leftOut" doc:"What an archive of a release leaves out, as it is about what that release does not hold."`
}

// ManifestProject names the project of an archive.
type ManifestProject struct {
	ID   string `json:"id" doc:"The project's ID."`
	Name string `json:"name" doc:"The project's name."`
	Slug string `json:"slug" doc:"The project's slug."`
}

// Source says what an archive's content is of.
type Source struct {
	Kind        string `json:"kind" enum:"draft,release" doc:"draft for the project's draft, under draft/, or release for one of its releases, whose files are under release/."`
	SnapshotID  string `json:"snapshotId,omitempty" doc:"The release's ID."`
	Version     string `json:"version,omitempty" doc:"The release's version."`
	ContentHash string `json:"contentHash,omitempty" doc:"The release's content hash."`
}

// ManifestSection describes one section's file.
type ManifestSection struct {
	Name   string `json:"name" doc:"The section's name."`
	Path   string `json:"path" doc:"The path of its file."`
	Items  int    `json:"items" minimum:"0" doc:"How many items it holds."`
	SHA256 string `json:"sha256" pattern:"^[0-9a-f]{64}$" doc:"The SHA-256 of its file."`
}

// LeftOut counts what an archive of a release does not hold of the project's
// comments and play states: those about what the release does not have.
type LeftOut struct {
	Comments   int `json:"comments" minimum:"0" doc:"The comments left out, as their threads' targets are not in the release."`
	PlayStates int `json:"playStates" minimum:"0" doc:"The play states left out, as the node that they are at is not in the release."`
}

// Project is the project's own data, of an archive of its draft.
type Project struct {
	Name        string `json:"name" doc:"The project's name."`
	Slug        string `json:"slug" doc:"The project's slug."`
	Description string `json:"description" doc:"The project's description."`
	Settings    Value  `json:"settings" doc:"The project's settings."`
}

// Folder is a folder of boards, variables, or components.
type Folder struct {
	ID       string `json:"id" doc:"The folder's ID."`
	Kind     string `json:"kind" doc:"What the folder holds."`
	ParentID string `json:"parentId,omitempty" doc:"The folder that holds it, if any."`
	Name     string `json:"name" doc:"The folder's name."`
	Order    string `json:"order" doc:"Its order among its siblings."`
}

// Board is a board.
type Board struct {
	ID          string `json:"id" doc:"The board's ID."`
	FolderID    string `json:"folderId,omitempty" doc:"Its folder, if any."`
	Order       string `json:"order" doc:"Its order among its folder's children."`
	Name        string `json:"name" doc:"The board's name."`
	Slug        string `json:"slug" doc:"The board's slug."`
	Description string `json:"description" doc:"The board's description."`
	Color       string `json:"color" doc:"The board's colour."`
	EntryNodeID string `json:"entryNodeId,omitempty" doc:"Its entry node, if any."`
}

// Variable is a variable's declaration.
type Variable struct {
	ID           string `json:"id" doc:"The variable's ID."`
	Scope        string `json:"scope" doc:"global, or board."`
	BoardID      string `json:"boardId,omitempty" doc:"The board of a board's variable."`
	FolderID     string `json:"folderId,omitempty" doc:"Its folder, if any."`
	Order        string `json:"order" doc:"Its order among its folder's children."`
	Key          string `json:"key" doc:"The key that expressions name it by."`
	Type         string `json:"type" doc:"Its type."`
	Default      Value  `json:"default" doc:"Its default value, of its type."`
	Description  string `json:"description" doc:"Its description."`
	ResetOnEnter bool   `json:"resetOnEnter" doc:"Whether entering its board resets it."`
}

// ComponentType is a component type, with its attribute definitions.
type ComponentType struct {
	ID         string         `json:"id" doc:"The type's ID."`
	Key        string         `json:"key" doc:"The type's key."`
	Name       string         `json:"name" doc:"The type's name."`
	Icon       string         `json:"icon" doc:"The type's icon."`
	Attributes []AttributeDef `json:"attributes" nullable:"false" doc:"Its attribute definitions, in their order."`
}

// AttributeDef is an attribute definition of a component type.
type AttributeDef struct {
	ID          string   `json:"id" doc:"The definition's ID."`
	Order       string   `json:"order" doc:"Its order among its type's."`
	Key         string   `json:"key" doc:"Its key."`
	Name        string   `json:"name" doc:"Its name."`
	Type        string   `json:"type" doc:"Its type."`
	EnumOptions []string `json:"enumOptions,omitempty" doc:"The options of an enum."`
	Default     Value    `json:"default" doc:"Its default value."`
	IsRuntime   bool     `json:"isRuntime" doc:"Whether expressions read and write it."`
}

// Component is a component, with its values.
type Component struct {
	ID              string           `json:"id" doc:"The component's ID."`
	ComponentTypeID string           `json:"componentTypeId" doc:"Its type."`
	FolderID        string           `json:"folderId,omitempty" doc:"Its folder, if any."`
	Order           string           `json:"order" doc:"Its order among its folder's children."`
	Key             string           `json:"key" doc:"Its key."`
	Name            string           `json:"name" doc:"Its name."`
	Description     Text             `json:"description,omitempty" doc:"Its description, for its authors, which the published format leaves out."`
	ImageAssetID    string           `json:"imageAssetId,omitempty" doc:"Its image, an asset."`
	Values          map[string]Value `json:"values" nullable:"false" doc:"Its value of each attribute, by the definition's ID."`
}

// Node is a node of a board.
type Node struct {
	ID            string   `json:"id" doc:"The node's ID."`
	BoardID       string   `json:"boardId" doc:"Its board."`
	Type          string   `json:"type" doc:"element, branch, or jumper."`
	Kind          string   `json:"kind,omitempty" doc:"An element's kind."`
	Title         string   `json:"title" doc:"Its title."`
	Text          Text     `json:"text,omitempty" doc:"An element's text."`
	SpeakerID     string   `json:"speakerId,omitempty" doc:"An element's speaker, a component."`
	CoverAssetID  string   `json:"coverAssetId,omitempty" doc:"An element's cover image, an asset, whose file is under assets/ when the archive holds assets (FR-EDIT-06 AC3)."`
	Color         string   `json:"color" doc:"Its colour."`
	Tags          []string `json:"tags" nullable:"false" doc:"Its tags."`
	X             Decimal  `json:"x" doc:"Its position's x."`
	Y             Decimal  `json:"y" doc:"Its position's y."`
	Width         *Decimal `json:"width,omitempty" doc:"Its width, if it has a size."`
	Height        *Decimal `json:"height,omitempty" doc:"Its height, if it has a size."`
	IsEnding      bool     `json:"isEnding" doc:"Whether an element is an ending."`
	TargetBoardID string   `json:"targetBoardId,omitempty" doc:"A jumper's target board."`
	TargetNodeID  string   `json:"targetNodeId,omitempty" doc:"A jumper's target node."`
}

// BranchOutput is an output of a branch.
type BranchOutput struct {
	ID     string `json:"id" doc:"The output's ID."`
	NodeID string `json:"nodeId" doc:"Its branch."`
	Kind   string `json:"kind" doc:"if, elseif, or else."`
	Order  string `json:"order,omitempty" doc:"A conditional output's order among its branch's."`
}

// Connection is a connection of a board.
type Connection struct {
	ID           string `json:"id" doc:"The connection's ID."`
	BoardID      string `json:"boardId" doc:"Its board."`
	FromNodeID   string `json:"fromNodeId" doc:"The node that it leaves."`
	FromOutputID string `json:"fromOutputId,omitempty" doc:"The branch output that it leaves, if any."`
	ToNodeID     string `json:"toNodeId" doc:"The node that it enters."`
	Order        string `json:"order" doc:"Its order among its port's connections."`
	Label        Text   `json:"label,omitempty" doc:"Its label."`
}

// Expression is the source of an expression of a node, a connection, or a
// branch output. An import compiles it again, so the archive holds no AST of
// the draft's (FR-DATA-01).
type Expression struct {
	OwnerType string `json:"ownerType" enum:"node,connection,branch_output" doc:"The kind of its owner."`
	OwnerID   string `json:"ownerId" doc:"Its owner."`
	Field     string `json:"field" doc:"The owner's field that it is."`
	Source    string `json:"source" doc:"Its source, as its author wrote it."`
}

// ValidationMute is a warning or an info that the project's authors muted.
type ValidationMute struct {
	OwnerType string `json:"ownerType" enum:"node,connection,branch_output,variable,board" doc:"The kind of entity that the finding is about."`
	OwnerID   string `json:"ownerId" doc:"That entity."`
	Code      string `json:"code" doc:"The finding's code."`
	Reason    string `json:"reason" doc:"Why it is muted."`
	MutedBy   string `json:"mutedBy,omitempty" doc:"The user who muted it last, of a full archive alone, whom people.json names."`
}

// Comment is a comment of a thread.
type Comment struct {
	ID         string     `json:"id" doc:"The comment's ID."`
	ThreadID   string     `json:"threadId" doc:"Its thread, its root's ID."`
	ParentID   string     `json:"parentId,omitempty" doc:"A reply's root."`
	TargetType string     `json:"targetType,omitempty" doc:"What a root's thread is about: node, connection, board, or variable."`
	TargetID   string     `json:"targetId,omitempty" doc:"The ID of what a root's thread is about."`
	Body       Text       `json:"body,omitempty" doc:"Its body. A root that its author deleted while its thread has replies has none."`
	Deleted    bool       `json:"deleted,omitempty" doc:"Whether it is a root that its author deleted."`
	AuthorID   string     `json:"authorId,omitempty" doc:"Its author, whom people.json names, unless their account is deleted."`
	CreatedAt  time.Time  `json:"createdAt" doc:"When it was posted."`
	EditedAt   *time.Time `json:"editedAt,omitempty" doc:"When its body last changed."`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty" doc:"When a root's thread was resolved."`
	ResolvedBy string     `json:"resolvedBy,omitempty" doc:"Who resolved it, whom people.json names."`
}

// Person is someone whom a full archive names.
type Person struct {
	ID          string `json:"id" doc:"The user's ID."`
	DisplayName string `json:"displayName" doc:"Their display name, as it was when the archive was made."`
}

// PlayState is a saved state of play mode's flow.
type PlayState struct {
	ID        string    `json:"id" doc:"The play state's ID."`
	Name      string    `json:"name" doc:"Its name."`
	State     Value     `json:"state" doc:"The save state, as schema/save-state defines it."`
	Names     Value     `json:"names" doc:"The names of the state's values, as play mode named them when it saved the state."`
	CreatedBy string    `json:"createdBy,omitempty" doc:"Who saved it, whom people.json names."`
	CreatedAt time.Time `json:"createdAt" doc:"When it was saved."`
}

// Annotation is a canvas annotation, the §5.3 annotation: a frame or a note
// of a board (FR-EDIT-12). The section is empty until plan item 127, and its
// shape is defined from the format's first version, so that an archive that
// holds one validates on a server that cannot import it, which then refuses
// it and names the section (FR-DATA-01 AC12).
type Annotation struct {
	ID       string   `json:"id" minLength:"1" doc:"The annotation's ID."`
	BoardID  string   `json:"boardId" minLength:"1" doc:"Its board."`
	Kind     string   `json:"kind" enum:"note,frame" doc:"note for a free-floating note, or frame for a labelled frame that moves what it contains."`
	Geometry Geometry `json:"geometry" doc:"Where it is on its board's canvas."`
	Text     Text     `json:"text,omitempty" doc:"A note's text, or a frame's label."`
}

// Geometry is an annotation's place on its board's canvas.
type Geometry struct {
	X      float64 `json:"x" doc:"Its left edge."`
	Y      float64 `json:"y" doc:"Its top edge."`
	Width  float64 `json:"width" minimum:"0" doc:"Its width."`
	Height float64 `json:"height" minimum:"0" doc:"Its height."`
}

// Release is a ready snapshot of the release history, whose files are under
// Path, byte for byte as its build stored them. AcknowledgedWarnings names
// warnings by their entities and no person, so a content-only archive holds it.
type Release struct {
	ID                   string    `json:"id" doc:"The snapshot's ID."`
	Version              string    `json:"version" doc:"Its version."`
	Note                 string    `json:"note" doc:"Its changelog note."`
	AcknowledgedSeq      int64     `json:"acknowledgedSeq" minimum:"0" doc:"The project's operation sequence of the validation report that its author acknowledged when they published it."`
	AcknowledgedWarnings Value     `json:"acknowledgedWarnings" doc:"The warnings of that report that its author acknowledged, each by its code, its entity, its field, and its target."`
	SchemaVersion        int       `json:"schemaVersion" minimum:"1" doc:"The version of the published format of its files."`
	ContentHash          string    `json:"contentHash" doc:"Its content hash."`
	SourceSeq            int64     `json:"sourceSeq" minimum:"0" doc:"The project's operation sequence as of the draft that it was built from."`
	Counts               Value     `json:"counts" doc:"Its manifest's entity counts."`
	ValidationReport     Value     `json:"validationReport" doc:"The validation report that its build stored."`
	PublishedBy          string    `json:"publishedBy,omitempty" doc:"Who published it, of a full archive alone, whom people.json names."`
	PublishedAt          time.Time `json:"publishedAt" doc:"When it was published."`
	BuiltAt              time.Time `json:"builtAt" doc:"When its build ended."`
	Path                 string    `json:"path" doc:"The directory of its files in the archive."`
}

// Asset is an asset of the project, whose file is at Path.
type Asset struct {
	ID     string `json:"id" doc:"The asset's ID."`
	Path   string `json:"path" doc:"The path of its file in the archive."`
	SHA256 string `json:"sha256" pattern:"^[0-9a-f]{64}$" doc:"The SHA-256 of its file."`
	Mime   string `json:"mime" doc:"Its media type."`
	Bytes  int64  `json:"bytes" minimum:"0" doc:"The size of its file."`
	Width  *int32 `json:"width,omitempty" doc:"An image's width, in pixels."`
	Height *int32 `json:"height,omitempty" doc:"An image's height, in pixels."`
}
