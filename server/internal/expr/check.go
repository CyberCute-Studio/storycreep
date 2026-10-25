package expr

import (
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// The limits that Check checks of an AST (§5.8.6), with MaxDepth and
// MaxNodes, which Parse checks of a source too.
const (
	// MaxStatements is the most statements of a script.
	MaxStatements = 64
	// MaxArguments is the most arguments of a call.
	MaxArguments = 8
	// MaxSignals is the most calls of signal in a script.
	MaxSignals = 8
)

// ErrAST is Check's error for what no AST that Parse returns holds: a nil
// node, a node of a type that is none of the nine kinds', a node at a place
// that its kind cannot be at, as an expression where a statement is, a node
// that the AST holds twice, an operator or a literal's type that the
// language does not have, a literal's value that Encode refuses, as an int
// beyond ±(2^53 − 1) or a string that holds a NUL, a variable's Scope that
// is none of ScopeNone, ScopeGlobal, and ScopeBoard, or an AST of the other
// context than the one that Check is given, which is its caller's mistake
// rather than the source's.
var ErrAST = errors.New("expr: not an AST of the language")

// ErrBoard is Check's error for a board that is not "" or a board's ID in its
// canonical spelling.
var ErrBoard = errors.New("expr: not a board's ID")

// Compile reads source as the source of a condition or a script, as ctx says
// (Parse), and checks its AST against the project p, as an expression of the
// board whose ID is board (Check). It returns the AST, its names resolved, or
// the first error of the source: the parser's, or else the checker's.
func Compile(source string, ctx Context, p *Project, board string) (Node, error) {
	n, err := Parse(source, ctx)
	if err != nil {
		return nil, err
	}
	if err := Check(n, ctx, p, board); err != nil {
		return nil, err
	}
	return n, nil
}

// Check checks n, the AST of a condition or a script, as ctx says, against
// the project p, as an expression of the board whose ID is board, or of none
// for "" (PRD §5.8.3–§5.8.6). It returns the first error that it meets, an
// *Error, or nil once it has resolved every name of n: it sets each
// variable's Ref, and each attribute's Ref and Attribute, to their entities'
// IDs, and their Name to their keys, as the AST's JSON writes them (§5.8.8).
// It changes nothing of n when it returns an error. A nil p names nothing.
//
// Check meets errors in two passes. The first reads n against the sandbox's
// limits (§5.8.6), each node before its children, and the children in order:
// at most MaxNodes nodes, none deeper than MaxDepth, the root 1 deep, at most
// MaxStatements statements of a script, MaxArguments arguments of a call,
// and MaxSignals calls of signal in a script. The second resolves names and
// checks types and purity in order from left to right: each operand is read
// whole, and then checked against its place, before the next is read, and a
// call's function is resolved, and its place and its number of arguments
// checked, before its arguments are read. schema/expr-corpus/README.md says
// which error each of them is, and which comes first.
//
// Check returns an error that wraps ErrContext for a ctx that is neither
// ContextCondition nor ContextScript, ErrBoard for a board that is not a
// board's ID, and ErrAST for an n that holds what ErrAST lists, which the
// first pass meets at its node, in its order, as it meets the limits'
// errors, so that an n that holds both gives the one that comes first: a
// script of more than MaxStatements statements gives TooManyStatements, over
// its first statement beyond them, before any other of its statements is
// read. It does not check how a key, a node's ID, or a function is spelt,
// which it resolves against p and the built-ins, nor the nodes' ranges.
// Every AST that it accepts, Encode writes. It reads at most MaxNodes + 1
// nodes of n, and recurses at most MaxDepth deep, whatever n is.
func Check(n Node, ctx Context, p *Project, board string) error {
	if ctx != ContextCondition && ctx != ContextScript {
		return fmt.Errorf("%w: %q", ErrContext, ctx)
	}
	if board != "" && !ids.IsCanonical(ids.Board, board) {
		return fmt.Errorf("%w: %q", ErrBoard, board)
	}
	if p == nil {
		p = &Project{}
	}
	if _, script := n.(*Script); script != (ctx == ContextScript) {
		return fmt.Errorf("%w: a %s's AST is a script exactly when its context is", ErrAST, ctx)
	}
	if err := limits(n, ctx); err != nil {
		return err
	}
	c := &checker{ctx: ctx, p: p, board: board}
	if err := c.root(n); err != nil {
		return err
	}
	for _, r := range c.resolved {
		switch n := r.n.(type) {
		case *Var:
			n.Name, n.Ref = r.name, r.ref
		case *Attr:
			n.Name, n.Ref, n.Attribute = r.name, r.ref, r.attribute
		}
	}
	return nil
}

// limits reads n against the sandbox's limits (Check), and checks that n
// holds nothing that ErrAST lists, each node, at its place, as it reaches it.
func limits(n Node, ctx Context) error {
	type visit struct {
		n     Node
		at    place
		depth int
	}
	stack := []visit{{n, atRoot, 1}}
	seen := make(map[Node]bool)
	nodes, signals := 0, 0
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if err := check(v.n, v.at); err != nil {
			return fmt.Errorf("%w: %w", ErrAST, err)
		}
		if seen[v.n] {
			return fmt.Errorf("%w: %w", ErrAST, errTwice)
		}
		seen[v.n] = true
		if nodes++; nodes > MaxNodes {
			return &Error{Code: ASTTooLarge, Range: v.n.Range()}
		}
		if v.depth > MaxDepth {
			return &Error{Code: ASTTooDeep, Range: v.n.Range()}
		}
		switch n := v.n.(type) {
		case *Lit:
			if _, err := literalValue(n); err != nil {
				return fmt.Errorf("%w: %w", ErrAST, err)
			}
		case *Var:
			if n.Scope != ScopeNone && n.Scope != ScopeGlobal && n.Scope != ScopeBoard {
				return fmt.Errorf("%w: %q is no scope of a variable", ErrAST, n.Scope)
			}
		case *Unary:
			if !unaryOps[n.Op] {
				return fmt.Errorf("%w: %q is no unary operator", ErrAST, n.Op)
			}
		case *Binary:
			if !binaryOps[n.Op] {
				return fmt.Errorf("%w: %q is no binary operator", ErrAST, n.Op)
			}
		case *Call:
			if len(n.Args) > MaxArguments {
				return beyond(TooManyArguments, n.Args[MaxArguments], atOperand)
			}
			if n.Function == "signal" && ctx == ContextScript {
				if signals++; signals > MaxSignals {
					return &Error{Code: TooManySignals, Range: n.Src}
				}
			}
		case *Assign:
			if !assignOpSet[n.Op] {
				return fmt.Errorf("%w: %q is no assignment operator", ErrAST, n.Op)
			}
		case *Script:
			if len(n.Statements) > MaxStatements {
				return beyond(TooManyStatements, n.Statements[MaxStatements], atStatement)
			}
		}
		// The children are read in order, the first of them next.
		from := len(stack)
		eachChild(v.n, func(c Node, at place) { stack = append(stack, visit{c, at, v.depth + 1}) })
		slices.Reverse(stack[from:])
	}
	return nil
}

// beyond returns the error code over n, the statement or the argument beyond
// a limit, at the place at, or one that wraps ErrAST if n cannot be there.
func beyond(code Code, n Node, at place) error {
	if err := check(n, at); err != nil {
		return fmt.Errorf("%w: %w", ErrAST, err)
	}
	return &Error{Code: code, Range: n.Range()}
}

// checker is Check's second pass, which resolves names and checks types and
// purity, of an AST that the first pass, limits, has read, so that it is a
// tree of at most MaxNodes nodes, at most MaxDepth deep, of the language's
// kinds at their places.
type checker struct {
	ctx   Context
	p     *Project
	board string
	// resolved are what the checker sets of the AST's names, which Check
	// does once it has met no error.
	resolved []resolution
}

// resolution is what Check sets of n, a *Var or an *Attr: its Name, its Ref,
// and an *Attr's Attribute.
type resolution struct {
	n                    Node
	name, ref, attribute string
}

// root checks the AST n: a script's statements, or a condition's expression,
// which is a bool.
func (c *checker) root(n Node) error {
	if s, ok := n.(*Script); ok {
		for _, st := range s.Statements {
			var err error
			switch st := st.(type) {
			case *Assign:
				err = c.assign(st)
			case *Call:
				_, err = c.call(st, true)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	_, err := c.operand(n, tyBool)
	return err
}

// mismatch returns the TypeMismatch of the operand n, of the type got, at a
// place that takes the types want.
func mismatch(n Node, got, want typeSet) *Error {
	return &Error{Code: TypeMismatch, Range: n.Range(), Type: got.name(), Expected: want.names()}
}

// operand checks n, and then that its type is one of want, and returns it.
func (c *checker) operand(n Node, want typeSet) (typeSet, error) {
	t, err := c.expression(n)
	if err != nil {
		return 0, err
	}
	if t&want == 0 {
		return 0, mismatch(n, t, want)
	}
	return t, nil
}

// expression checks the expression n, and returns its type.
func (c *checker) expression(n Node) (typeSet, error) {
	switch n := n.(type) {
	case *Lit:
		return typeOf(n.Type), nil
	case *Var:
		v, err := c.variable(n)
		if err != nil {
			return 0, err
		}
		return typeOf(v.Type), nil
	case *Attr:
		a, err := c.attribute(n)
		if err != nil {
			return 0, err
		}
		return typeOf(a.Type), nil
	case *NodeRef:
		return tyNode, c.nodeRef(n)
	case *Unary:
		if n.Op == OpNot {
			return c.operand(n.Operand, tyBool)
		}
		return c.operand(n.Operand, tyNumber)
	case *Binary:
		return c.binary(n)
	case *Call:
		return c.call(n, false)
	}
	return 0, nil
}

// binary checks the binary operation n, its left operand and then its right,
// and returns its type (§5.8.3). The left operand's type decides what the
// right one may be, so that the right one is the operand that a TypeMismatch
// names when the two do not go together, as the 1 of "a" + 1.
func (c *checker) binary(n *Binary) (typeSet, error) {
	switch n.Op {
	case OpOr, OpAnd:
		if _, err := c.operand(n.Left, tyBool); err != nil {
			return 0, err
		}
		_, err := c.operand(n.Right, tyBool)
		return tyBool, err
	case OpAdd:
		l, err := c.operand(n.Left, tyNumber|tyString)
		if err != nil {
			return 0, err
		}
		if l == tyString {
			_, err := c.operand(n.Right, tyString)
			return tyString, err
		}
		r, err := c.operand(n.Right, tyNumber)
		return widened(l, r), err
	case OpSub, OpMul, OpDiv:
		l, err := c.operand(n.Left, tyNumber)
		if err != nil {
			return 0, err
		}
		r, err := c.operand(n.Right, tyNumber)
		return widened(l, r), err
	case OpMod:
		if _, err := c.operand(n.Left, tyInt); err != nil {
			return 0, err
		}
		_, err := c.operand(n.Right, tyInt)
		return tyInt, err
	case OpEq, OpNe:
		l, err := c.operand(n.Left, tyValue)
		if err != nil {
			return 0, err
		}
		_, err = c.operand(n.Right, comparedWith(l))
		return tyBool, err
	}
	// <, <=, >, >=.
	l, err := c.operand(n.Left, tyNumber|tyString)
	if err != nil {
		return 0, err
	}
	_, err = c.operand(n.Right, comparedWith(l))
	return tyBool, err
}

// comparedWith returns the types that a value of the type t is compared with:
// numbers with numbers, after widening, and any other with its own type.
func comparedWith(t typeSet) typeSet {
	if t&tyNumber != 0 {
		return tyNumber
	}
	return t
}

// call checks the call n, a script's statement if statement is set, and
// otherwise an operand, and returns its type: its function first, the
// function's purity at its place, and its number of arguments, and then each
// argument in order.
func (c *checker) call(n *Call, statement bool) (typeSet, error) {
	b, ok := builtins[n.Function]
	if !ok {
		name := Range{n.Src.Start, n.Src.Start + utf8.RuneCountInString(n.Function)}
		return 0, &Error{Code: UnknownFunction, Range: name, Suggestions: near(n.Function, c.functions(statement))}
	}
	switch {
	case b.effect && c.ctx == ContextCondition:
		return 0, &Error{Code: ConditionMustBePure, Range: n.Src}
	case b.effect && !statement:
		return 0, &Error{Code: SignalNotStatement, Range: n.Src}
	case !b.effect && statement:
		return 0, &Error{Code: InvalidStatement, Range: n.Src}
	}
	if len(n.Args) < len(b.params)-b.optional || len(n.Args) > len(b.params) {
		return 0, &Error{Code: WrongArgumentCount, Range: n.Src}
	}
	types := make([]typeSet, len(n.Args))
	for i, a := range n.Args {
		if b.effect && i == 0 {
			// A signal's name, which is known before the script runs.
			if l, ok := a.(*Lit); !ok || l.Type != value.String || l.Str == "" {
				return 0, &Error{Code: InvalidSignalName, Range: a.Range()}
			}
			continue
		}
		t, err := c.operand(a, b.params[i])
		if err != nil {
			return 0, err
		}
		types[i] = t
	}
	switch {
	case b.effect:
		// A statement, which has no value.
		return 0, nil
	case b.result == 0:
		return widened(types...), nil
	}
	return b.result, nil
}

// functions returns the names of the built-ins that a call can be of at its
// place, which an UnknownFunction suggests: one that has an effect as a
// script's statement, and one that has none anywhere else.
func (c *checker) functions(statement bool) []string {
	var names []string
	for name, b := range builtins {
		if b.effect == (statement && c.ctx == ContextScript) {
			names = append(names, name)
		}
	}
	return names
}

// assign checks the assignment n, its target and then its value (§5.8.3): the
// value of = is of the target's type, or an int for a float, and op= is
// checked as op of the target and the value is, its result of the target's
// type. An enum attribute is assigned only with = and a string literal of one
// of its options.
func (c *checker) assign(n *Assign) error {
	var target typeSet
	var enum *Attribute
	switch t := n.Target.(type) {
	case *Var:
		v, err := c.variable(t)
		if err != nil {
			return err
		}
		target = typeOf(v.Type)
	case *Attr:
		a, err := c.attribute(t)
		if err != nil {
			return err
		}
		target = typeOf(a.Type)
		if a.Type == value.Enum {
			enum = a
		}
	}
	if enum != nil {
		if _, err := c.expression(n.Value); err != nil {
			return err
		}
		if l, ok := n.Value.(*Lit); n.Op != OpAssign || !ok || l.Type != value.String ||
			!slices.Contains(enum.Options, l.Str) {
			return &Error{Code: InvalidEnumValue, Range: n.Value.Range()}
		}
		return nil
	}
	if takes := targets[n.Op]; target&takes == 0 {
		return mismatch(n.Target, target, takes)
	}
	_, err := c.operand(n.Value, accepts(target))
	return err
}

// targets are the types of the targets that each assignment operator takes:
// any value's of =, those that its operator takes of op=.
var targets = map[AssignOp]typeSet{
	OpAssign:    tyValue,
	OpAddAssign: tyNumber | tyString,
	OpSubAssign: tyNumber,
	OpMulAssign: tyNumber,
	OpDivAssign: tyNumber,
	OpModAssign: tyInt,
}

// resolve has Check set the name of n, a *Var or an *Attr, to the entity of
// the IDs ref and attribute, of the name name, once it has met no error.
func (c *checker) resolve(n Node, name, ref, attribute string) {
	c.resolved = append(c.resolved, resolution{n, name, ref, attribute})
}

// variable resolves the variable n (§5.8.3): global.<key> names a global
// variable, board.<key> one of the board, and a bare key the board's, a
// global one, or else a component, which is no value.
func (c *checker) variable(n *Var) (*Variable, error) {
	var v *Variable
	switch n.Scope {
	case ScopeGlobal:
		v = c.p.global(n.Key)
	case ScopeBoard:
		v = c.p.ofBoard(c.board, n.Key)
	default:
		v = c.p.ofBoard(c.board, n.Key)
		if v == nil {
			v = c.p.global(n.Key)
		}
		if v == nil && c.p.component(n.Key) != nil {
			return nil, &Error{Code: ComponentNotValue, Range: n.Src}
		}
	}
	if v != nil {
		c.resolve(n, v.Key, v.ID, "")
		return v, nil
	}
	if n.Scope != ScopeGlobal && c.p.elsewhere(c.board, n.Key) {
		return nil, &Error{Code: BoardVariableOutOfScope, Range: n.Src}
	}
	var candidates []string
	if n.Scope != ScopeGlobal {
		for key := range c.p.boards[c.board] {
			candidates = append(candidates, key)
		}
	}
	if n.Scope != ScopeBoard {
		for key := range c.p.globals {
			candidates = append(candidates, key)
		}
	}
	return nil, &Error{Code: UnknownIdentifier, Range: n.Src, Suggestions: written(n.Scope, near(n.Key, candidates))}
}

// written returns the keys as names written with the qualifier scope.
func written(scope Scope, keys []string) []string {
	if scope == ScopeNone {
		return keys
	}
	var names []string
	for _, k := range keys {
		names = append(names, string(scope)+"."+k)
	}
	return names
}

// shadowed reports whether a variable of the board or a global one has the
// key, which a bare key, and <key>.<attribute>, names rather than a
// component of the key.
func (c *checker) shadowed(key string) bool {
	return c.p.ofBoard(c.board, key) != nil || c.p.global(key) != nil
}

// attribute resolves the attribute n, which is a runtime one (§5.8.3,
// FR-COMP-03): <component>.<attribute> names the attribute of the component
// unless a variable has the component's key, as cmp.<component>.<attribute>
// names it whatever has.
func (c *checker) attribute(n *Attr) (*Attribute, error) {
	// qualifier is how the name is written before the component's key.
	qualifier := ""
	if n.Explicit {
		qualifier = "cmp."
	}
	if !n.Explicit && c.shadowed(n.ComponentKey) {
		var suggestions []string
		if a := c.p.component(n.ComponentKey).runtime(n.AttributeKey); a != nil {
			suggestions = []string{"cmp." + n.ComponentKey + "." + n.AttributeKey}
		}
		return nil, &Error{Code: VariableNotComponent, Range: n.Src, Suggestions: suggestions}
	}
	comp := c.p.component(n.ComponentKey)
	if comp == nil {
		var candidates []string
		for key := range c.p.components {
			if n.Explicit || !c.shadowed(key) {
				candidates = append(candidates, key)
			}
		}
		suggestions := near(n.ComponentKey, candidates)
		for i, k := range suggestions {
			suggestions[i] = qualifier + k + "." + n.AttributeKey
		}
		return nil, &Error{Code: UnknownIdentifier, Range: n.Src, Suggestions: suggestions}
	}
	a := comp.attributes[n.AttributeKey]
	if a == nil {
		var candidates []string
		for _, a := range comp.Attributes {
			if a.Runtime {
				candidates = append(candidates, a.Key)
			}
		}
		suggestions := near(n.AttributeKey, candidates)
		for i, k := range suggestions {
			suggestions[i] = qualifier + n.ComponentKey + "." + k
		}
		return nil, &Error{Code: UnknownAttribute, Range: n.Src, Suggestions: suggestions}
	}
	if !a.Runtime {
		return nil, &Error{Code: AttributeNotRuntime, Range: n.Src}
	}
	c.resolve(n, comp.Key+"."+a.Key, comp.ID, a.ID)
	return a, nil
}

// runtime returns the runtime attribute of the key of c, or nil, as of a nil
// c.
func (c *component) runtime(key string) *Attribute {
	if c == nil {
		return nil
	}
	if a := c.attributes[key]; a != nil && a.Runtime {
		return a
	}
	return nil
}

// nodeRef checks the node reference n: to a node that the project has, which
// is not a jumper (§5.8.5).
func (c *checker) nodeRef(n *NodeRef) error {
	jumper, ok := c.p.node(n.Ref)
	switch {
	case !ok:
		return &Error{Code: UnknownNode, Range: n.Src}
	case jumper:
		return &Error{Code: NodeRefJumper, Range: n.Src}
	}
	return nil
}
