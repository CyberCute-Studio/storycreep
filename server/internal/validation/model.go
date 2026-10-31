package validation

import (
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// model is what the rules read of a project's draft (Load): the entities
// that are not in its trash, each with what a rule reads of it, and what the
// project's entry and its texts refer to, which may be in the trash, or
// gone.
type model struct {
	// project is the project's ID, and entry its entry, its board and its
	// node, each zero where it has none.
	project     ids.UUID
	entry       struct{ board, node ids.UUID }
	boards      []board
	nodes       []node
	outputs     []output
	connections []connection
	// expressions are those of the nodes, the connections, and the outputs
	// of the model.
	expressions []expression
	variables   []variable
	// texts are what the texts of the nodes, the connections, and the
	// components that are not in the trash refer to.
	texts []textRef
	// components are the components that are not in the trash, each with
	// its component type, and attributes the attribute definitions that are
	// not, so that an attribute of a component is one that its type defines.
	components map[ids.UUID]ids.UUID
	attributes map[ids.UUID]attribute
}

// attribute is an attribute definition: the component type that defines it,
// and whether it is runtime, which a value reference of a text that a
// runtime shows reads alone (FR-TEXT-02).
type attribute struct {
	typ     ids.UUID
	runtime bool
}

// The types of node.
const (
	element = "element"
	branch  = "branch"
	jumper  = "jumper"
)

// board is a board: its ID and its entry node, which may be in the trash, or
// zero for none.
type board struct {
	id, entry ids.UUID
}

// node is a node: its type, an element's kind, speaker, whether it is an
// ending, and whether its text is empty, and a jumper's target, zero for
// none.
type node struct {
	id, board  ids.UUID
	typ, kind  string
	speaker    ids.UUID
	ending     bool
	textEmpty  bool
	targetNode ids.UUID
}

// output is a branch's output.
type output struct {
	id, node, board ids.UUID
}

// connection is a connection: its board, the node that it leaves, by the
// port output (zero for an element's out), the node that it enters, and its
// label, as the database writes it, so that two labels that are the same
// document are the same bytes. The model holds the connections in the order
// of their ports, and each port's in its order.
type connection struct {
	id, board, from, output, to ids.UUID
	label                       string
}

// The fields that a finding may name (finding.field): an expression's, a
// node's text and speaker, a connection's label, a component's description,
// a board's entry node, the project's entry, and a jumper's target.
const (
	fieldOnEnter     = "on_enter"
	fieldOnExit      = "on_exit"
	fieldCondition   = "condition"
	fieldOnTraverse  = "on_traverse"
	fieldText        = "text"
	fieldLabel       = "label"
	fieldDescription = "description"
	fieldSpeaker     = "speaker_component_id"
	fieldEntryNode   = "entry_node_id"
	fieldEntry       = "entry"
	fieldTarget      = "target"
)

// expression is an expression of an owner that is not in the trash: whether
// it compiles, the code of its error if it does not, and, for the condition
// that compiles of a choice that shares its label with another choice of its
// element, which is no ending, its AST, which the rule of duplicate choices
// reads, of those choices alone (compared).
type expression struct {
	owner     ref
	field     string
	valid     bool
	errorCode string
	condition expr.Node
}

// variable is a variable: its key, the board of a board's variable, and
// whether a site that is not in the trash reads it, or writes it.
type variable struct {
	id, board     ids.UUID
	key           string
	read, written bool
}

// textRef is what a text refers to: a value reference's variable or
// attribute, or a mention's component (dependency.Use), with the text's
// owner, a node, a connection, or a component.
type textRef struct {
	owner ref
	use   dependency.Use
}

// ref is an entity, by its kind and its UUID; the zero ref is none.
type ref struct {
	kind ids.Kind
	id   ids.UUID
}

// finding is what a rule found: its code, the entity that it is about, the
// board and the node by which a client opens it, each zero where it has
// none, the field of the entity that it is about, if any, what else it names
// (target), and the message's arguments.
type finding struct {
	code        Code
	entity      ref
	board, node ids.UUID
	field       string
	// target is what a dangling reference refers to, with its attribute
	// definition for an attribute (attribute), a jumper's target, or the
	// connection that a choice or a connection repeats.
	target    ref
	attribute ids.UUID
	args      i18n.Args
}
