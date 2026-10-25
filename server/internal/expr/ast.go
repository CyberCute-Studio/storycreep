package expr

// Kind is the kind of an AST's node, as its JSON's kind names it (§5.8.8).
type Kind string

// The kinds of node.
const (
	KindLit     Kind = "lit"
	KindVar     Kind = "var"
	KindAttr    Kind = "attr"
	KindNodeRef Kind = "node_ref"
	KindUnary   Kind = "unary"
	KindBinary  Kind = "binary"
	KindCall    Kind = "call"
	KindAssign  Kind = "assign"
	KindScript  Kind = "script"
)

// Range is where in a source something is: the offsets of its first
// character and of the character after its last, counted in code points from
// 0, which an editor that counts in UTF-16 code units or bytes converts. An
// empty range is a place between two characters, before the one at Start.
type Range struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Node is a node of an AST: a *Lit, *Var, *Attr, *NodeRef, *Unary, *Binary,
// *Call, *Assign, or *Script.
type Node interface {
	// Kind returns the node's kind.
	Kind() Kind
	// Range returns where the node is in the source that Parse read it
	// from: from its first token to its last, with the parentheses around
	// its operands, but not those around itself; a *Script's is the whole
	// source, the white space and the comments around its statements
	// included, whether it has any statement or none.
	Range() Range
	node()
}

// Lit is a literal: true or false, an int, a float, or a string.
type Lit struct {
	// Type is the literal's type, value.Bool, value.Int, value.Float, or
	// value.String, whose value is in Bool, Int, Float, or Str.
	Type  string
	Bool  bool
	Int   int64
	Float float64
	Str   string
	Src   Range
}

// Scope is the qualifier that a source writes a variable's key after: none,
// global, or board (§5.8.3).
type Scope string

// The qualifiers of a variable's key.
const (
	ScopeNone   Scope = ""
	ScopeGlobal Scope = "global"
	ScopeBoard  Scope = "board"
)

// Var is a variable, by its key, as global.<key>, or as board.<key>.
type Var struct {
	// Name is the variable's name as the AST's JSON writes it: as the source
	// wrote it, with its qualifier, until name resolution writes the
	// variable's key there.
	Name  string
	Scope Scope
	Key   string
	// Ref is the variable's ID, which name resolution sets.
	Ref string
	Src Range
}

// Attr is a component's attribute, as <component>.<attribute> or as
// cmp.<component>.<attribute>.
type Attr struct {
	// Name is the attribute's name as the AST's JSON writes it: as the source
	// wrote it, cmp. and all, until name resolution writes
	// <component>.<attribute> there, of their keys.
	Name string
	// Explicit is set for the form that starts with cmp.
	Explicit     bool
	ComponentKey string
	AttributeKey string
	// Ref is the component's ID, and Attribute its attribute definition's,
	// which name resolution sets.
	Ref       string
	Attribute string
	Src       Range
}

// NodeRef is a node reference literal, node("nd_…") (§5.8.4).
type NodeRef struct {
	// Ref is the node's ID, in its canonical spelling (ids.Encode).
	Ref string
	Src Range
}

// UnaryOp is the operator of a Unary.
type UnaryOp string

// The unary operators: !, or not, and -.
const (
	OpNot UnaryOp = "not"
	OpNeg UnaryOp = "neg"
)

// Unary is a unary operator applied to its operand.
type Unary struct {
	Op      UnaryOp
	Operand Node
	Src     Range
}

// BinaryOp is the operator of a Binary.
type BinaryOp string

// The binary operators, each with its symbol in the source; and and or are
// written as words too.
const (
	OpOr  BinaryOp = "or"  // ||
	OpAnd BinaryOp = "and" // &&
	OpEq  BinaryOp = "eq"  // ==
	OpNe  BinaryOp = "ne"  // !=
	OpLt  BinaryOp = "lt"  // <
	OpLte BinaryOp = "lte" // <=
	OpGt  BinaryOp = "gt"  // >
	OpGte BinaryOp = "gte" // >=
	OpAdd BinaryOp = "add" // +
	OpSub BinaryOp = "sub" // -
	OpMul BinaryOp = "mul" // *
	OpDiv BinaryOp = "div" // /
	OpMod BinaryOp = "mod" // %
)

// Binary is a binary operator applied to its operands.
type Binary struct {
	Op    BinaryOp
	Left  Node
	Right Node
	Src   Range
}

// Call is a call of a built-in function, by its name, with its arguments in
// order.
type Call struct {
	Function string
	Args     []Node
	Src      Range
}

// AssignOp is the operator of an Assign, as the source writes it.
type AssignOp string

// The assignment operators.
const (
	OpAssign    AssignOp = "="
	OpAddAssign AssignOp = "+="
	OpSubAssign AssignOp = "-="
	OpMulAssign AssignOp = "*="
	OpDivAssign AssignOp = "/="
	OpModAssign AssignOp = "%="
)

// Assign is an assignment, a statement of a script, to a *Var or an *Attr.
type Assign struct {
	Op     AssignOp
	Target Node
	Value  Node
	Src    Range
}

// Script is a script's statements, each an *Assign or a *Call, in order.
type Script struct {
	Statements []Node
	// Src is the whole source.
	Src Range
}

func (*Lit) Kind() Kind     { return KindLit }
func (*Var) Kind() Kind     { return KindVar }
func (*Attr) Kind() Kind    { return KindAttr }
func (*NodeRef) Kind() Kind { return KindNodeRef }
func (*Unary) Kind() Kind   { return KindUnary }
func (*Binary) Kind() Kind  { return KindBinary }
func (*Call) Kind() Kind    { return KindCall }
func (*Assign) Kind() Kind  { return KindAssign }
func (*Script) Kind() Kind  { return KindScript }

func (n *Lit) Range() Range     { return n.Src }
func (n *Var) Range() Range     { return n.Src }
func (n *Attr) Range() Range    { return n.Src }
func (n *NodeRef) Range() Range { return n.Src }
func (n *Unary) Range() Range   { return n.Src }
func (n *Binary) Range() Range  { return n.Src }
func (n *Call) Range() Range    { return n.Src }
func (n *Assign) Range() Range  { return n.Src }
func (n *Script) Range() Range  { return n.Src }

func (*Lit) node()     {}
func (*Var) node()     {}
func (*Attr) node()    {}
func (*NodeRef) node() {}
func (*Unary) node()   {}
func (*Binary) node()  {}
func (*Call) node()    {}
func (*Assign) node()  {}
func (*Script) node()  {}
