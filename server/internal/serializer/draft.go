package serializer

import (
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// Draft is a project's draft as the serializer reads it (Load): the project,
// and its entities that are not in its trash, each with what the published
// format holds of it and nothing else, so neither who made or changed it,
// nor when (PRD §6.2). Every ID is a public one, in its canonical spelling,
// and "" is none. Serialize reads the slices in any order, and writes the
// same bytes whatever it is (FR-SNAP-03): a row of one board, node, or
// output that is not among the draft's is passed over, as the trash keeps
// what it holds out of every read.
type Draft struct {
	Project        Project
	Boards         []Board
	Nodes          []Node
	Outputs        []Output
	Connections    []Connection
	Variables      []Variable
	ComponentTypes []ComponentType
	Attributes     []Attribute
	Components     []Component
}

// Project is the draft's project: its ID, name, and slug, which the
// manifest names it by, and its entry, a node, or a board, whose entry node
// is the project's, or neither.
type Project struct {
	ID, Name, Slug        string
	EntryBoard, EntryNode string
}

// Board is a board: its slug, which names its file, its name, and its entry
// node, which may be gone.
type Board struct {
	ID, Slug, Name string
	EntryNode      string
}

// Node is a node of a board: an element, a branch, or a jumper, as Type
// says (published.NodeElement, NodeBranch, NodeJumper).
type Node struct {
	ID, Board string
	Type      string
	// Kind is an element's, dialogue or narration.
	Kind  string
	Title string
	// Text is an element's text, nil or an empty paragraph for none.
	Text *richtext.Doc
	// Speaker and Cover are an element's speaker, a component, which may be
	// gone, and its cover, an asset.
	Speaker, Cover string
	Tags           []string
	// X and Y are the node's position, in hundredths of a unit of its
	// board's canvas, as it is kept (FR-EDIT-02 AC4).
	X, Y     int64
	IsEnding bool
	// TargetBoard and TargetNode are a jumper's target, which may be gone.
	TargetBoard, TargetNode string
	// OnEnter and OnExit are an element's or a branch's scripts, nil for
	// none.
	OnEnter, OnExit *Expression
}

// Output is an output of a branch: if, elseif, or else, as it is stored,
// though Serialize gives each conditional one the kind of its place
// (FR-EDIT-04), with a conditional one's order among its branch's, a key of
// package orderkey, and its condition.
type Output struct {
	ID, Node  string
	Kind      string
	Order     string
	Condition *Expression
}

// Connection is a connection of a board, from the node From, by its port
// out where FromOutput is "", or else by its output FromOutput, to the node
// To, with its order among its port's connections, a key of package
// orderkey, its label, nil or an empty paragraph for none, and its
// condition and its on-traverse script, nil for none.
type Connection struct {
	ID, Board            string
	From, FromOutput, To string
	Order                string
	Label                *richtext.Doc
	Condition            *Expression
	OnTraverse           *Expression
}

// Expression is an expression of its owner's field: its source, and its
// AST, as the published format holds one, its names resolved to the IDs of
// what they name, or nil where the source does not compile, with the code
// of its first error, which "" stands for where none is known.
type Expression struct {
	Source string
	AST    expr.Node
	Error  string
}

// Variable is a variable's declaration: global, or of the board Board, as
// Scope says, with its default of its type.
type Variable struct {
	ID, Key, Scope, Board string
	Type                  string
	Default               value.Value
	Description           string
	ResetOnEnter          bool
}

// ComponentType is a component type.
type ComponentType struct {
	ID, Key, Name string
}

// Attribute is an attribute definition of the component type ComponentType,
// with its order among the type's, a key of package orderkey, and its
// declaration.
type Attribute struct {
	ID, ComponentType string
	Order             string
	Key, Name, Type   string
	Options           []string
	Default           value.Value
	Runtime           bool
}

// Component is a component of the type ComponentType, with its values of the
// type's attribute definitions, by their IDs: a definition without one has
// its default.
type Component struct {
	ID, ComponentType string
	Key, Name         string
	Image             string
	Values            map[string]value.Value
}
