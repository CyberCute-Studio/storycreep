package expr

import (
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// TypeNode is the type of a node reference (§5.8.4), which is no value of
// the language: only visited and visits take one, and a TypeMismatch names
// it as an operand's type.
const TypeNode = "node"

// typeSet is a set of types: those of the language's values, bool, int,
// float, and string, and TypeNode. The type of an expression is a set of one.
type typeSet uint8

const (
	tyBool typeSet = 1 << iota
	tyInt
	tyFloat
	tyString
	tyNode

	// tyNumber are the numbers' types, an int widened where a float is read.
	tyNumber = tyInt | tyFloat
	// tyValue are the types of the language's values.
	tyValue = tyBool | tyInt | tyFloat | tyString
)

// typeNames name each type, in the order that an Error's Expected lists them.
var typeNames = [...]struct {
	t    typeSet
	name string
}{
	{tyBool, value.Bool}, {tyInt, value.Int}, {tyFloat, value.Float}, {tyString, value.String},
	{tyNode, TypeNode},
}

// names returns the names of the types of s, in the order of typeNames.
func (s typeSet) names() []string {
	var names []string
	for _, t := range typeNames {
		if s&t.t != 0 {
			names = append(names, t.name)
		}
	}
	return names
}

// name returns the name of the one type of s.
func (s typeSet) name() string {
	for _, t := range typeNames {
		if s == t.t {
			return t.name
		}
	}
	return ""
}

// typeOf returns the type that an expression reads a variable's or an
// attribute's value of the type t as: a value.Enum as a string, as its
// options are. A value.Text, which no expression reads, is a string too.
func typeOf(t string) typeSet {
	switch t {
	case value.Bool:
		return tyBool
	case value.Int:
		return tyInt
	case value.Float:
		return tyFloat
	}
	return tyString
}

// accepts returns the types that a place of the type t takes: its own, and
// an int where a float is read, which is widened (§5.8.3).
func accepts(t typeSet) typeSet {
	if t == tyFloat {
		return tyNumber
	}
	return t
}

// widened returns the type of an arithmetic operation, or of min, max, abs,
// or clamp, of numbers of the types ts: an int of ints, and otherwise a float,
// each int widened.
func widened(ts ...typeSet) typeSet {
	for _, t := range ts {
		if t != tyInt {
			return tyFloat
		}
	}
	return tyInt
}

// builtin is a built-in function (§5.8.4).
type builtin struct {
	// params are the types that each parameter takes.
	params []typeSet
	// optional is how many of the last params a call may leave out.
	optional int
	// result is the type of the call's value, or 0 for the widened type of
	// its arguments', as min's.
	result typeSet
	// effect is set for a function that has an effect, signal, which only a
	// script's statement calls; a call of any other is pure.
	effect bool
	// version is the ast_version that introduced the function, or last
	// changed what it does (FR-EXPR-04): a change of the pinned Unicode
	// tables changes lower's, upper's, and trim's.
	version int
}

// builtins are the built-in functions, by their names (§5.8.4). A signal's
// first parameter, its name, is a string literal of a character or more,
// which Check reads rather than its type.
var builtins = map[string]builtin{
	"min":         {params: []typeSet{tyNumber, tyNumber}, version: 1},
	"max":         {params: []typeSet{tyNumber, tyNumber}, version: 1},
	"abs":         {params: []typeSet{tyNumber}, version: 1},
	"floor":       {params: []typeSet{tyNumber}, result: tyInt, version: 1},
	"ceil":        {params: []typeSet{tyNumber}, result: tyInt, version: 1},
	"round":       {params: []typeSet{tyNumber}, result: tyInt, version: 1},
	"clamp":       {params: []typeSet{tyNumber, tyNumber, tyNumber}, version: 1},
	"to_int":      {params: []typeSet{tyNumber | tyString}, result: tyInt, version: 1},
	"to_float":    {params: []typeSet{tyNumber | tyString}, result: tyFloat, version: 1},
	"to_string":   {params: []typeSet{tyValue}, result: tyString, version: 1},
	"len":         {params: []typeSet{tyString}, result: tyInt, version: 1},
	"contains":    {params: []typeSet{tyString, tyString}, result: tyBool, version: 1},
	"starts_with": {params: []typeSet{tyString, tyString}, result: tyBool, version: 1},
	"ends_with":   {params: []typeSet{tyString, tyString}, result: tyBool, version: 1},
	"lower":       {params: []typeSet{tyString}, result: tyString, version: 1},
	"upper":       {params: []typeSet{tyString}, result: tyString, version: 1},
	"trim":        {params: []typeSet{tyString}, result: tyString, version: 1},
	"visited":     {params: []typeSet{tyNode}, result: tyBool, version: 1},
	"visits":      {params: []typeSet{tyNode}, result: tyInt, version: 1},
	"signal":      {params: []typeSet{tyString, tyValue}, optional: 1, effect: true, version: 1},
}

// ASTVersion is the ast_version of this package's language (FR-EXPR-04): the
// highest of its constructs', the newest that Check accepts and that a
// runtime must support to evaluate every AST that it accepts.
const ASTVersion = 1

// The ast_version that introduced each node kind and operator, or last
// changed what it does (FR-EXPR-04).
var (
	kindVersions = map[Kind]int{
		KindLit: 1, KindVar: 1, KindAttr: 1, KindNodeRef: 1, KindUnary: 1, KindBinary: 1, KindCall: 1,
		KindAssign: 1, KindScript: 1,
	}
	unaryVersions  = map[UnaryOp]int{OpNot: 1, OpNeg: 1}
	binaryVersions = map[BinaryOp]int{
		OpOr: 1, OpAnd: 1, OpEq: 1, OpNe: 1, OpLt: 1, OpLte: 1, OpGt: 1, OpGte: 1, OpAdd: 1, OpSub: 1,
		OpMul: 1, OpDiv: 1, OpMod: 1,
	}
	assignVersions = map[AssignOp]int{
		OpAssign: 1, OpAddAssign: 1, OpSubAssign: 1, OpMulAssign: 1, OpDivAssign: 1, OpModAssign: 1,
	}
)

// VersionOf returns the ast_version of the ASTs, as a snapshot's manifest
// declares it of its expressions' ASTs, and as an expression stores it of its
// own (FR-EXPR-04, PRD §6.4): the highest of the node kinds, the operators,
// and the built-ins that they use, or 1, the first, for none, so that a
// project that uses no construct of a newer version stays loadable by an
// older runtime. It reads ASTs that Check accepted, of constructs that this
// package knows; a node that is none of an AST's at its place, as a nil one
// or one of a type of another package, counts for nothing, nor do its
// children, and a node that it reaches twice once, so that it never reads
// without end.
func VersionOf(asts ...Node) int {
	type visit struct {
		n  Node
		at place
	}
	v := 1
	seen := make(map[Node]bool)
	var stack []visit
	for _, n := range asts {
		stack = append(stack, visit{n, atRoot})
	}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		// check comes first, since a type of another package may be one that
		// a map cannot take as a key.
		if check(top.n, top.at) != nil || seen[top.n] {
			continue
		}
		seen[top.n] = true
		v = max(v, versionOf(top.n))
		eachChild(top.n, func(c Node, at place) { stack = append(stack, visit{c, at}) })
	}
	return v
}

// versionOf returns the ast_version of n's own construct, a node that is not
// nil: of its kind, and of its operator or its function, if it has one.
func versionOf(n Node) int {
	v := kindVersions[n.Kind()]
	switch n := n.(type) {
	case *Unary:
		v = max(v, unaryVersions[n.Op])
	case *Binary:
		v = max(v, binaryVersions[n.Op])
	case *Assign:
		v = max(v, assignVersions[n.Op])
	case *Call:
		v = max(v, builtins[n.Function].version)
	}
	return v
}
