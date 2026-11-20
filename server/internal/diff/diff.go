// Package diff compares two snapshots of the published game format entity by
// entity, rather than file by file or line by line, so that the result reads
// as a list of changes (PRD §7.4, FR-SNAP-04, plan item 80, note N87): Compare
// reads the files of each, as a game's res://storycreep/ holds them, at one
// version of the format, and returns the Diff of the entities that one has
// and the other does not, those whose fields differ, with each field's value
// on either side, and those that changed boards, grouped by board, with the
// counts of each kind. The web app's diff view and other clients read it from
// POST /api/v1/projects/{project_id}/diff (FR-SYNC-02), which Truncate sizes
// for display, and the Godot addon computes the same diff of the files that
// it holds (§7.5): the corpus of /schema/diff-corpus holds both to the same
// output (AC4). Schema is its JSON Schema, which SchemaPath holds.
package diff

// What happened to an entity, the change of an EntityChange (§7.4).
const (
	// Added is an entity that only the to side has.
	Added = "added"
	// Removed is an entity that only the from side has.
	Removed = "removed"
	// Modified is an entity that both sides have, on the same board, whose
	// fields differ.
	Modified = "modified"
	// Moved is a node or a connection that both sides have, on different
	// boards, whose fields may differ too.
	Moved = "moved"
)

// The kinds of the entities that a diff compares, the kind of an
// EntityChange, and the keys of the summary's counts of each (KindCounts).
const (
	KindVariable      = "variable"
	KindComponentType = "component_type"
	KindAttribute     = "attribute"
	KindComponent     = "component"
	KindBoard         = "board"
	KindNode          = "node"
	KindBranchOutput  = "branch_output"
	KindConnection    = "connection"
	KindProject       = "project"
)

// The operations of a run of a word-level text diff (WordRun).
const (
	Equal  = "equal"
	Delete = "delete"
	Insert = "insert"
)

// The kinds of a side of a diff (DiffSide).
const (
	SideSnapshot = "snapshot"
	SideDraft    = "draft"
)

// Diff is the entity-level diff of two snapshots' files, the from side and the
// to side, at one version of the published format (§7.4).
type Diff struct {
	Boards        []BoardChanges `json:"boards" nullable:"false" doc:"Each board that changed, or whose nodes, branch outputs, or connections changed, sorted by the board's ID, [] for none."`
	From          DiffSide       `json:"from"`
	Project       ProjectChanges `json:"project"`
	SchemaVersion int            `json:"schemaVersion" minimum:"1" doc:"The version of the published game format, its schemaVersion, that both sides were compared at."`
	Summary       DiffSummary    `json:"summary"`
	To            DiffSide       `json:"to"`
	Truncated     bool           `json:"truncated" doc:"Whether changes were left out to size the diff for display, which the omitted counts of the project and of each board count. The summary counts every change all the same (PRD FR-SYNC-02 AC2)."`
}

// DiffSide is a side of a diff, as its manifest's snapshot has it: a release,
// or the draft as it was when it was read (§7.8).
type DiffSide struct {
	ContentHash string `json:"contentHash" pattern:"^sha256:[0-9a-f]{64}$" doc:"The hash of the side's content, as its manifest's snapshot has it (PRD §7.3)."`
	ID          string `json:"id,omitempty" doc:"The release's ID. The draft has none."`
	Kind        string `json:"kind" enum:"snapshot,draft" doc:"A release, snapshot, or the draft."`
	Version     string `json:"version,omitempty" doc:"The release's version. The draft has none."`
}

// ProjectChanges are the changes of the project's own fields and of what its
// project files hold: its variables, global and of each board, its component
// types and their attribute definitions, and its components (§6.3).
type ProjectChanges struct {
	Attributes     []EntityChange `json:"attributes" nullable:"false" doc:"The changes of the component types' attribute definitions, sorted by ID, [] for none."`
	ComponentTypes []EntityChange `json:"componentTypes" nullable:"false" doc:"The changes of the component types, sorted by ID, [] for none."`
	Components     []EntityChange `json:"components" nullable:"false" doc:"The changes of the components, sorted by ID, [] for none."`
	Fields         []FieldChange  `json:"fields" nullable:"false" doc:"The changes of the project's own fields, its name and slug, as its manifest has them, and its entry and signals, as its settings have them, sorted by field, [] for none."`
	Omitted        int            `json:"omitted" minimum:"0" doc:"How many changes of the project's variables, component types, attribute definitions, and components were left out to size the diff, 0 for none."`
	Variables      []EntityChange `json:"variables" nullable:"false" doc:"The changes of the variables, global and of each board, sorted by ID, [] for none."`
}

// BoardChanges are the changes of a board: of the board itself, and of its
// nodes, branch outputs, and connections.
type BoardChanges struct {
	Board   BoardRef       `json:"board" doc:"The board, as the to side has it, or as the from side has it for a board that the to side does not have."`
	Change  string         `json:"change,omitempty" enum:"added,removed,modified" doc:"What happened to the board itself: added or removed, with every node, branch output, and connection of it, or modified, with the changes of its fields. Without it, the board itself did not change, and only what it holds did."`
	Changes []EntityChange `json:"changes" nullable:"false" doc:"The changes of what the board holds: its nodes, then their branch outputs, then its connections, each kind sorted by ID, [] for none. A node or a connection that was added, modified, or moved is listed on the board that the to side has it on, and one that was removed on the board that the from side had it on."`
	Fields  []FieldChange  `json:"fields,omitempty" nullable:"false" doc:"The changes of the board's fields, its entryNodeId, name, and slug, sorted by field, of a board that was modified."`
	Omitted int            `json:"omitted" minimum:"0" doc:"How many changes of what the board holds were left out to size the diff, 0 for none."`
}

// BoardRef names a board.
type BoardRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// EntityChange is what happened to an entity. It names the entity by what
// its kind shows of it, as the to side has it, or as the from side has it for
// one that was removed: a variable by its key and type; a component type by
// its key and name; an attribute definition by its component type's ID, its
// key, and its name; a component by its key and name; a node by its type, its
// title, and an element's text's preview, where it has them; a branch output
// by its branch's ID; and a connection by the nodes that it leaves and enters
// and its label's plain text, where it has one.
type EntityChange struct {
	Branch        string        `json:"branch,omitempty" doc:"A branch output's branch, a node's ID."`
	Change        string        `json:"change" enum:"added,removed,modified,moved" doc:"What happened to the entity: added, which only the to side has; removed, which only the from side has; modified, which both sides have, whose fields differ; or moved, a node or a connection that the two sides have on different boards, whose fields may differ too (PRD §7.4)."`
	ComponentType string        `json:"componentType,omitempty" doc:"An attribute definition's component type's ID."`
	Cosmetic      bool          `json:"cosmetic,omitempty" doc:"Set on a modified node whose position alone changed, which a view collapses by default (PRD §7.4)."`
	Fields        []FieldChange `json:"fields,omitempty" nullable:"false" doc:"The changes of the entity's fields, sorted by field, and the values of a component's attributes by the attribute's ID, of an entity that was modified, or moved with fields that differ too. An entity that was added or removed lists none."`
	From          string        `json:"from,omitempty" doc:"The node that a connection leaves."`
	FromBoard     *BoardRef     `json:"fromBoard,omitempty" doc:"The board that a moved node or connection was on, as the from side has it."`
	ID            string        `json:"id" doc:"The entity's ID."`
	Key           string        `json:"key,omitempty" doc:"A variable's, a component type's, an attribute definition's, or a component's key."`
	Kind          string        `json:"kind" enum:"variable,component_type,attribute,component,node,branch_output,connection" doc:"The entity's kind."`
	Label         string        `json:"label,omitempty" doc:"A connection's label's plain text, where it has one."`
	Name          string        `json:"name,omitempty" doc:"A component type's, an attribute definition's, or a component's name."`
	Preview       string        `json:"preview,omitempty" doc:"An element's text's preview, where it has a text."`
	Title         string        `json:"title,omitempty" doc:"A node's title, where it has one."`
	To            string        `json:"to,omitempty" doc:"The node that a connection enters."`
	ToBoard       *BoardRef     `json:"toBoard,omitempty" doc:"The board that a moved node or connection is on, as the to side has it."`
	Type          string        `json:"type,omitempty" doc:"A variable's type, or a node's: element, branch, or jumper."`
}

// FieldChange is a field whose value differs between the two sides, with its
// value on each, as its file holds it, which a field that a side does not
// have leaves out: but a rich text's, an element's text or a connection's
// label, which is its plain text, with a word-level diff of the two, and an
// expression's, a condition or a script, which is its source, with whether
// the set of the entities that it names changed (§7.4).
type FieldChange struct {
	After       any       `json:"after,omitempty" doc:"The field's value on the to side, as its file holds it, or a rich text's plain text, or an expression's source. Without it, the to side does not have the field."`
	Attribute   string    `json:"attribute,omitempty" doc:"Of a component's attributes, the attribute definition's ID whose value this is."`
	Before      any       `json:"before,omitempty" doc:"The field's value on the from side, as after has it. Without it, the from side does not have the field."`
	Field       string    `json:"field" doc:"The field's name, as its file has it, as key or condition; componentType and order for an attribute definition's component type and its place among the type's, from 0; and branch for a branch output's branch."`
	RefsChanged *bool     `json:"refsChanged,omitempty" doc:"Of an expression, whether the variables, the components' attributes, and the nodes that it names differ between the two sides."`
	Words       []WordRun `json:"words,omitempty" nullable:"false" doc:"Of a rich text, the word-level diff of its plain texts, before into after, in order."`

	// texts are a rich text's plain texts, of a change that compareFiles
	// made, until fill computes their Words.
	texts *plainTexts
}

// plainTexts are the plain texts of a rich text's change, before and after,
// an absent text's empty.
type plainTexts struct {
	before, after string
}

// fill computes the Words of c's plain texts, if it holds them.
func (c *FieldChange) fill() {
	if c.texts != nil {
		c.Words = Words(c.texts.before, c.texts.after)
		c.texts = nil
	}
}

// fill computes the Words of each of e's fields' changes that holds plain
// texts.
func (e *EntityChange) fill() {
	for i := range e.Fields {
		e.Fields[i].fill()
	}
}

// fill computes the Words of every change of d that holds plain texts.
func (d *Diff) fill() {
	for _, l := range [][]EntityChange{d.Project.Variables, d.Project.ComponentTypes, d.Project.Attributes,
		d.Project.Components} {
		for i := range l {
			l[i].fill()
		}
	}
	for _, b := range d.Boards {
		for i := range b.Changes {
			b.Changes[i].fill()
		}
	}
}

// WordRun is a run of a word-level text diff: text that the two sides share,
// that the from side has and the to side does not, or the reverse. The equal
// and delete runs, in order, make the before text, and the equal and insert
// runs the after text.
type WordRun struct {
	Op   string `json:"op" enum:"equal,delete,insert" doc:"Whether the text is in both sides, equal; in the before side alone, delete; or in the after side alone, insert."`
	Text string `json:"text" minLength:"1"`
}

// DiffSummary counts every change of the diff, of every kind, however
// Truncate sized it (PRD FR-SYNC-02 AC2): each added, removed, and moved
// entity, each modified entity that is cosmetic, and each that is not,
// modified, and the project once when its own fields changed.
type DiffSummary struct {
	Added    int        `json:"added" minimum:"0"`
	Cosmetic int        `json:"cosmetic" minimum:"0"`
	Kinds    KindCounts `json:"kinds"`
	Modified int        `json:"modified" minimum:"0"`
	Moved    int        `json:"moved" minimum:"0"`
	Removed  int        `json:"removed" minimum:"0"`
}

// KindCounts are the counts of the changes of each kind of entity.
type KindCounts struct {
	Attribute     ChangeCounts `json:"attribute"`
	Board         ChangeCounts `json:"board"`
	BranchOutput  ChangeCounts `json:"branch_output"`
	Component     ChangeCounts `json:"component"`
	ComponentType ChangeCounts `json:"component_type"`
	Connection    ChangeCounts `json:"connection"`
	Node          ChangeCounts `json:"node"`
	Project       ChangeCounts `json:"project"`
	Variable      ChangeCounts `json:"variable"`
}

// ChangeCounts are the counts of the changes of one kind of entity.
type ChangeCounts struct {
	Added    int `json:"added" minimum:"0"`
	Cosmetic int `json:"cosmetic" minimum:"0"`
	Modified int `json:"modified" minimum:"0"`
	Moved    int `json:"moved" minimum:"0"`
	Removed  int `json:"removed" minimum:"0"`
}

// of returns the counts of the kind k, which is one of the kinds' constants.
func (k *KindCounts) of(kind string) *ChangeCounts {
	switch kind {
	case KindAttribute:
		return &k.Attribute
	case KindBoard:
		return &k.Board
	case KindBranchOutput:
		return &k.BranchOutput
	case KindComponent:
		return &k.Component
	case KindComponentType:
		return &k.ComponentType
	case KindConnection:
		return &k.Connection
	case KindNode:
		return &k.Node
	case KindProject:
		return &k.Project
	case KindVariable:
		return &k.Variable
	}
	panic("diff: no kind " + kind)
}

// count counts a change of the kind kind, what happened to it, cosmetic or
// not, in s.
func (s *DiffSummary) count(kind, change string, cosmetic bool) {
	k := s.Kinds.of(kind)
	switch {
	case change == Added:
		s.Added++
		k.Added++
	case change == Removed:
		s.Removed++
		k.Removed++
	case change == Moved:
		s.Moved++
		k.Moved++
	case cosmetic:
		s.Cosmetic++
		k.Cosmetic++
	default:
		s.Modified++
		k.Modified++
	}
}
