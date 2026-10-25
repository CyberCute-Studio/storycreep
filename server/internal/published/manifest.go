package published

import (
	"encoding/json"
)

// Manifest is a snapshot's manifest, storycreep.json (§6.3, §6.5): what the
// snapshot is, its boards, and the hash of each of its files.
type Manifest struct {
	ASTVersion Int             `json:"astVersion" minimum:"1" maximum:"9007199254740991" doc:"The highest ast_version among the constructs that the snapshot's expressions use, 1 for none (FR-EXPR-04). A runtime that supports a lower one refuses the snapshot (PRD §6.4)."`
	Boards     []ManifestBoard `json:"boards" doc:"Every board, sorted by ID, by which a runtime finds a board's file without reading any other board's (PRD §6.5)."`
	Counts     Counts          `json:"counts"`
	Entry      *Entry          `json:"entry,omitempty" doc:"Where the flow starts: the project's entry node, of its entry board. Without it, the project has none."`
	Files      map[string]Hash `json:"files" keyPattern:"^(?:project/(?:settings|variables|component_types|components)\\.json|boards/[a-z0-9]+(?:-[a-z0-9]+)*\\.json|assets/[01234567][0123456789ABCDEFGHJKMNPQRSTVWXYZ]{25}\\.(?:png|jpg|webp|gif|svg))$" doc:"The hash of each of the snapshot's files but the manifest, by its path, which uses forward slashes."`
	Generator  GeneratorInfo   `json:"generator"`
	Project    ProjectInfo     `json:"project"`
	// SchemaVersion is SchemaVersion.
	SchemaVersion Int      `json:"schemaVersion" const:"1" doc:"The version of the published format, 1."`
	Snapshot      Snapshot `json:"snapshot"`
}

// JSONSchemaDoc describes the manifest (schemagen.Documented).
func (Manifest) JSONSchemaDoc() string {
	return "A snapshot's manifest, storycreep.json: what the snapshot is, its boards, and the hash of each of " +
		"its files (PRD §6.3, §6.5)."
}

// MarshalJSON writes the manifest, its boards and its files [] and {} when it
// has none, or fails on what Decode would refuse of it, as a snapshot that
// is not set (marshalFile).
func (m Manifest) MarshalJSON() ([]byte, error) {
	type plain Manifest
	return marshalFile[Manifest](ManifestPath, plain(m))
}

// UnmarshalJSON reads a manifest, whose snapshot is a release's or a draft's.
func (m *Manifest) UnmarshalJSON(data []byte) error {
	type plain Manifest
	var w struct {
		plain
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*m = Manifest(w.plain)
	if w.Snapshot == nil {
		return nil
	}
	s, err := unmarshalVariant(w.Snapshot, "channel", map[string]func() Snapshot{
		ChannelRelease: func() Snapshot { return &ReleaseSnapshot{} },
		ChannelDraft:   func() Snapshot { return &DraftSnapshot{} },
	})
	m.Snapshot = s
	return err
}

// ManifestBoard is a board of the manifest's index.
type ManifestBoard struct {
	EntryNodeID string `json:"entryNodeId,omitempty" id:"node" doc:"The board's entry node, where the flow enters the board when it starts there. Without it, the board has none."`
	File        string `json:"file" pattern:"^boards/[a-z0-9]+(?:-[a-z0-9]+)*\\.json$" doc:"The path of the board's file, boards/ and its slug and .json, which the manifest's files list too."`
	ID          string `json:"id" id:"board"`
	Name        string `json:"name"`
	Slug        string `json:"slug" pattern:"^[a-z0-9]+(?:-[a-z0-9]+)*$" maxLength:"64"`
}

// JSONSchemaDoc describes a board of the index (schemagen.Documented).
func (ManifestBoard) JSONSchemaDoc() string {
	return "A board of the manifest's index: its ID, slug, name, entry node, and file."
}

// Counts are how many of each entity a snapshot holds.
type Counts struct {
	Boards      Int `json:"boards" minimum:"0" maximum:"9007199254740991"`
	Components  Int `json:"components" minimum:"0" maximum:"9007199254740991"`
	Connections Int `json:"connections" minimum:"0" maximum:"9007199254740991"`
	Nodes       Int `json:"nodes" minimum:"0" maximum:"9007199254740991" doc:"The nodes of every type: elements, branches, and jumpers."`
	Variables   Int `json:"variables" minimum:"0" maximum:"9007199254740991" doc:"The variables, global and of each board."`
}

// JSONSchemaDoc describes the counts (schemagen.Documented).
func (Counts) JSONSchemaDoc() string {
	return "How many of each entity the snapshot holds."
}

// Entry is where the flow starts: a node, and its board.
type Entry struct {
	BoardID string `json:"boardId" id:"board"`
	NodeID  string `json:"nodeId" id:"node" doc:"A node of the board."`
}

// JSONSchemaDoc describes the entry (schemagen.Documented).
func (Entry) JSONSchemaDoc() string {
	return "Where the flow starts: a node, and its board."
}

// GeneratorInfo is what wrote a snapshot.
type GeneratorInfo struct {
	Name    string `json:"name" minLength:"1" doc:"What wrote the snapshot, storycreep for the server."`
	Version string `json:"version" minLength:"1" doc:"Its version."`
}

// JSONSchemaDoc describes the generator (schemagen.Documented).
func (GeneratorInfo) JSONSchemaDoc() string {
	return "What wrote the snapshot."
}

// ProjectInfo is a snapshot's project.
type ProjectInfo struct {
	ID   string `json:"id" id:"project"`
	Name string `json:"name"`
	Slug string `json:"slug" pattern:"^[a-z0-9]+(?:-[a-z0-9]+)*$" maxLength:"64"`
}

// JSONSchemaDoc describes the project (schemagen.Documented).
func (ProjectInfo) JSONSchemaDoc() string {
	return "The snapshot's project."
}

// The channels of a snapshot (§7.8).
const (
	ChannelRelease = "release"
	ChannelDraft   = "draft"
)

// Snapshot is what a manifest's snapshot is: a *ReleaseSnapshot, a published
// release, or a *DraftSnapshot, the working draft as it was when it was
// pulled (§7.8).
type Snapshot interface {
	channel() string
}

// ReleaseSnapshot is a published release.
type ReleaseSnapshot struct {
	ContentHash Hash      `json:"contentHash" doc:"The hash of the snapshot's content."`
	ID          string    `json:"id" id:"snapshot"`
	Note        string    `json:"note" doc:"The note that the release was published with."`
	PublishedAt Timestamp `json:"publishedAt"`
	Version     string    `json:"version" minLength:"1" doc:"The release's version, which its author chose."`
}

// DraftSnapshot is the working draft, as it was when it was pulled, which has
// neither an ID nor a version (§7.8).
type DraftSnapshot struct {
	ContentHash Hash      `json:"contentHash" doc:"The hash of the draft's content when it was pulled."`
	PulledAt    Timestamp `json:"pulledAt"`
}

func (*ReleaseSnapshot) channel() string { return ChannelRelease }
func (*DraftSnapshot) channel() string   { return ChannelDraft }

// JSONSchemaDoc describes a release (schemagen.Documented).
func (ReleaseSnapshot) JSONSchemaDoc() string {
	return "A published release."
}

// JSONSchemaDoc describes a draft (schemagen.Documented).
func (DraftSnapshot) JSONSchemaDoc() string {
	return "The working draft, as it was when it was pulled, which has neither an ID nor a version (PRD §7.8)."
}

// MarshalJSON writes the release with its channel.
func (s ReleaseSnapshot) MarshalJSON() ([]byte, error) {
	type plain ReleaseSnapshot
	return marshalVariant("channel", ChannelRelease, plain(s))
}

// MarshalJSON writes the draft with its channel.
func (s DraftSnapshot) MarshalJSON() ([]byte, error) {
	type plain DraftSnapshot
	return marshalVariant("channel", ChannelDraft, plain(s))
}
