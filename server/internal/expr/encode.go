package expr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/semantics"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// Encode returns the canonical form of the AST n (§5.8.8, §6.2), the same
// bytes for the same AST, whatever source it was read from (FR-EXPR-03 AC3):
// its JSON without white space, each object's keys sorted, an int as a plain
// decimal, a float as the shortest decimal that reads back as it, as
// JavaScript's JSON.stringify writes one, in positional notation from 1e-6 to
// below 1e21, without a point when it is integral, and otherwise with an
// exponent, as 1e+21 and 1e-7, never -0; and a string with ", \, and the
// control characters escaped, as \b, \f, \n, \r, \t, or \u00XX, and U+2028
// and U+2029, as Go's encoding/json writes them, and nothing else escaped, <,
// >, &, and / among them. A field that is not set, as the ref of a name that
// is not resolved, is left out.
//
// Encode fails on a node that is not one of an AST: a nil one, one of a type
// that is none of the nine kinds', as one of another package that embeds
// one, an int beyond ±(2^53 − 1), a float that is not finite, a string
// literal of more than MaxStringLength characters, as no string value is
// (§5.8.1), a string, a literal's or any other, that is not UTF-8 or that
// holds a NUL, which no text holds, a name, a node reference, or a function
// that is empty, an operator or a literal's type that the language does not
// have, a node that the AST holds twice, as each node of a cycle is, which no
// tree holds, or a node of a kind that its place in the AST cannot be of: an
// AST is an expression or a script, a script's statements are assignments
// and calls, an assignment's target is a variable or an attribute, and every
// other child of a node is an expression, neither an assignment nor a
// script. It does not check how a name, a node reference, or a function that
// is not empty is spelt, which name resolution checks against what it names
// (§5.8.3, plan item 40), nor the other limits of §5.8.6, which item 40
// checks of an AST.
//
// Encode writes an AST of any depth without recursing, so that no AST that a
// program builds, however deep, can exhaust the stack, which no program
// recovers from, and refuses a node that it has reached already, so that
// nodes that a program links in a cycle cannot make it write without end, nor
// nodes that it shares make it write more nodes than they are, as a node
// whose operands are both one node of n levels would make it write 2^n.
func Encode(n Node) ([]byte, error) {
	e := &encoder{seen: make(map[Node]bool)}
	e.enc = json.NewEncoder(&e.encoded)
	e.enc.SetEscapeHTML(false)
	e.stack = append(e.stack, part{node: n, place: atRoot})
	for len(e.stack) > 0 {
		p := e.stack[len(e.stack)-1]
		e.stack = e.stack[:len(e.stack)-1]
		if p.text != "" {
			e.b.WriteString(p.text)
			continue
		}
		if err := e.node(p.node, p.place); err != nil {
			return nil, err
		}
	}
	return e.b.Bytes(), nil
}

// place is where a node is in an AST, which decides the kinds of node that it
// can be of.
type place uint8

const (
	// atRoot is the AST itself: an expression or a script.
	atRoot place = iota
	// atOperand is an expression's place: an operand, a call's argument, or
	// an assignment's value.
	atOperand
	// atStatement is a script's statement: an assignment or a call.
	atStatement
	// atTarget is an assignment's target: a variable or an attribute.
	atTarget
)

// places name each place, for Encode's error.
var places = [...]string{
	atRoot:      "an AST",
	atOperand:   "an operand, an argument, or an assignment's value",
	atStatement: "a script's statement",
	atTarget:    "an assignment's target",
}

// part is a part of an AST's canonical form that Encode has yet to write: a
// node, at its place, or, when it is set, text.
type part struct {
	node  Node
	place place
	text  string
}

// text returns the part that is the text s, which is not empty.
func text(s string) part {
	return part{text: s}
}

// encoder writes an AST's canonical form from a stack of the parts that it
// has yet to write, the next on top, rather than by recursing.
type encoder struct {
	b     bytes.Buffer
	stack []part
	// seen holds the nodes that the encoder has reached, each of which an
	// AST holds once.
	seen map[Node]bool
	// enc writes a scalar's JSON to encoded.
	enc     *json.Encoder
	encoded bytes.Buffer
}

// push adds parts to those that the encoder has yet to write, so that it
// writes them next, in their order.
func (e *encoder) push(parts ...part) {
	for i := len(parts) - 1; i >= 0; i-- {
		e.stack = append(e.stack, parts[i])
	}
}

// pushList adds the nodes, each at the place at, to the parts that the
// encoder has yet to write, as the elements of an array, separated by commas,
// which closing follows.
func (e *encoder) pushList(nodes []Node, at place, closing string) {
	e.stack = append(e.stack, text(closing))
	for i := len(nodes) - 1; i >= 0; i-- {
		e.stack = append(e.stack, part{node: nodes[i], place: at})
		if i > 0 {
			e.stack = append(e.stack, text(","))
		}
	}
}

// The operators that the language has: those that the parser reads.
var (
	unaryOps  = map[UnaryOp]bool{OpNot: true, OpNeg: true}
	binaryOps = func() map[BinaryOp]bool {
		ops := make(map[BinaryOp]bool)
		for _, level := range levels {
			for _, op := range level {
				if op != "" {
					ops[op] = true
				}
			}
		}
		return ops
	}()
	assignOpSet = func() map[AssignOp]bool {
		ops := make(map[AssignOp]bool)
		for _, op := range assignOps {
			if op != "" {
				ops[op] = true
			}
		}
		return ops
	}()
)

// errNil is a nil node, and errTwice one that the AST holds twice.
var (
	errNil   = errors.New("expr: a node is nil")
	errTwice = errors.New("expr: a node is in the AST twice")
)

// check returns nil if n is a node of one of the kinds that can be at the
// place at, and otherwise why it is not: errNil for a node that is nil, or a
// nil pointer, which every kind of node is a pointer to, an error for one of
// a type that is none of theirs, as one of another package that embeds one
// of them, which has the kind's methods but neither its fields nor its JSON,
// and an error for one of a kind that the place cannot be of. It tells each
// kind's nil pointer, and where each kind can be, in one switch over the
// kinds, as eachChild tells each kind's children, at their places, in one,
// which Check, VersionOf, and Walk read an AST by; node's switch, which
// writes each kind's children with what comes between them, lists them once
// more.
func check(n Node, at place) error {
	var isNil, fits bool
	expression := at == atRoot || at == atOperand
	switch n := n.(type) {
	case *Lit:
		isNil, fits = n == nil, expression
	case *Var:
		isNil, fits = n == nil, at != atStatement
	case *Attr:
		isNil, fits = n == nil, at != atStatement
	case *NodeRef:
		isNil, fits = n == nil, expression
	case *Unary:
		isNil, fits = n == nil, expression
	case *Binary:
		isNil, fits = n == nil, expression
	case *Call:
		isNil, fits = n == nil, at != atTarget
	case *Assign:
		isNil, fits = n == nil, at == atStatement
	case *Script:
		isNil, fits = n == nil, at == atRoot
	case nil:
		isNil = true
	default:
		return fmt.Errorf("expr: a node of the type %T is of no kind of an AST", n)
	}
	switch {
	case isNil:
		return errNil
	case !fits:
		return fmt.Errorf("expr: a node of the kind %s cannot be %s", n.Kind(), places[at])
	}
	return nil
}

// eachChild calls f with each of n's children, at its place, in the order of
// the source: a script's statements, a call's arguments, a unary operator's
// operand, a binary operator's left operand and then its right, and an
// assignment's target and then its value. A nil n, a leaf, and a node of a
// type that is none of the kinds' have none.
func eachChild(n Node, f func(child Node, at place)) {
	switch n := n.(type) {
	case *Unary:
		if n != nil {
			f(n.Operand, atOperand)
		}
	case *Binary:
		if n != nil {
			f(n.Left, atOperand)
			f(n.Right, atOperand)
		}
	case *Call:
		if n != nil {
			for _, a := range n.Args {
				f(a, atOperand)
			}
		}
	case *Assign:
		if n != nil {
			f(n.Target, atTarget)
			f(n.Value, atOperand)
		}
	case *Script:
		if n != nil {
			for _, s := range n.Statements {
				f(s, atStatement)
			}
		}
	}
}

// Walk calls f with n and with each node under it, each node before its
// children, and the children in the order of the source, as eachChild tells
// them, so that a package that reads every node of an AST, as package
// expression reads the names and the node references of a source, finds the
// children of each kind that has them by this package's one switch, and so
// those of a kind that a later ast_version adds. It recurses as deep as the
// AST is, which Parse and Check bound by MaxDepth. A nil n has no node.
func Walk(n Node, f func(Node)) {
	if n == nil {
		return
	}
	f(n)
	eachChild(n, func(c Node, _ place) { Walk(c, f) })
}

// node writes n, which is at the place at, but for its children, which it
// adds, with what follows each, to the parts that the encoder has yet to
// write, or returns why n is not a node of an AST there. Each object's keys
// are in alphabetical order.
func (e *encoder) node(n Node, at place) error {
	if err := check(n, at); err != nil {
		return err
	}
	if e.seen[n] {
		return errTwice
	}
	e.seen[n] = true
	switch n := n.(type) {
	case *Lit:
		return e.literal(n)
	case *Var:
		name, err := e.required(n.Name)
		if err != nil {
			return err
		}
		e.b.WriteString(`{"kind":"var","name":` + name)
		if err := e.optional("ref", n.Ref); err != nil {
			return err
		}
		e.b.WriteByte('}')
	case *Attr:
		e.b.WriteByte('{')
		if n.Attribute != "" {
			attribute, err := e.quoted(n.Attribute)
			if err != nil {
				return err
			}
			e.b.WriteString(`"attribute":` + attribute + ",")
		}
		name, err := e.required(n.Name)
		if err != nil {
			return err
		}
		e.b.WriteString(`"kind":"attr","name":` + name)
		if err := e.optional("ref", n.Ref); err != nil {
			return err
		}
		e.b.WriteByte('}')
	case *NodeRef:
		ref, err := e.required(n.Ref)
		if err != nil {
			return err
		}
		e.b.WriteString(`{"kind":"node_ref","ref":` + ref + "}")
	case *Unary:
		if !unaryOps[n.Op] {
			return fmt.Errorf("expr: %q is no unary operator", n.Op)
		}
		e.b.WriteString(`{"kind":"unary","op":"` + string(n.Op) + `","operand":`)
		e.push(part{node: n.Operand, place: atOperand}, text("}"))
	case *Binary:
		if !binaryOps[n.Op] {
			return fmt.Errorf("expr: %q is no binary operator", n.Op)
		}
		e.b.WriteString(`{"kind":"binary","left":`)
		e.push(part{node: n.Left, place: atOperand}, text(`,"op":"`+string(n.Op)+`","right":`),
			part{node: n.Right, place: atOperand}, text("}"))
	case *Call:
		function, err := e.required(n.Function)
		if err != nil {
			return err
		}
		e.b.WriteString(`{"args":[`)
		e.pushList(n.Args, atOperand, `],"function":`+function+`,"kind":"call"}`)
	case *Assign:
		if !assignOpSet[n.Op] {
			return fmt.Errorf("expr: %q is no assignment operator", n.Op)
		}
		e.b.WriteString(`{"kind":"assign","op":"` + string(n.Op) + `","target":`)
		e.push(part{node: n.Target, place: atTarget}, text(`,"value":`), part{node: n.Value, place: atOperand},
			text("}"))
	case *Script:
		e.b.WriteString(`{"kind":"script","statements":[`)
		e.pushList(n.Statements, atStatement, "]}")
	}
	return nil
}

// optional writes the field key of the string s, after a comma, unless s is
// "", a field that is not set, which it leaves out.
func (e *encoder) optional(key, s string) error {
	if s == "" {
		return nil
	}
	q, err := e.quoted(s)
	if err != nil {
		return err
	}
	e.b.WriteString(`,"` + key + `":` + q)
	return nil
}

// literal writes a literal, its value of its type, or returns why it is no
// value of the language.
func (e *encoder) literal(n *Lit) error {
	v, err := literalValue(n)
	if err != nil {
		return err
	}
	j, err := e.jsonOf(v)
	if err != nil {
		return err
	}
	e.b.WriteString(`{"kind":"lit","type":"` + n.Type + `","value":` + j + "}")
	return nil
}

// literalValue returns the value of the literal n, a bool, an int64, a
// float64, or a string, as its JSON writes it, or why it is no value of the
// language: a type that no literal is of, an int beyond ±(2^53 − 1), a float
// that is not finite, or a string that is not UTF-8, that holds a NUL, or
// that is longer than MaxStringLength characters. Encode and Check both refuse
// a literal by it, so that every AST that Check accepts, Encode writes.
func literalValue(n *Lit) (any, error) {
	switch n.Type {
	case value.Bool:
		return n.Bool, nil
	case value.Int:
		if n.Int < -semantics.MaxInt || n.Int > semantics.MaxInt {
			return nil, fmt.Errorf("expr: the int %d is beyond ±%d", n.Int, int64(semantics.MaxInt))
		}
		return n.Int, nil
	case value.Float:
		if math.IsNaN(n.Float) || math.IsInf(n.Float, 0) {
			return nil, fmt.Errorf("expr: the float %v is not finite", n.Float)
		}
		f := n.Float
		if f == 0 {
			// -0 is 0 (§5.8.9), which encoding/json would write as -0.
			f = 0
		}
		return f, nil
	case value.String:
		if err := checkText(n.Str); err != nil {
			return nil, err
		}
		if utf8.RuneCountInString(n.Str) > MaxStringLength {
			return nil, fmt.Errorf("expr: a string literal is longer than %d characters", MaxStringLength)
		}
		return n.Str, nil
	}
	return nil, fmt.Errorf("expr: %q is no type of a literal", n.Type)
}

// The strings that no AST holds: one that is not UTF-8, which encoding/json
// would write with U+FFFD in place of each byte that is not, one that holds a
// NUL, which PostgreSQL's text and jsonb cannot hold, and an empty one where a
// node needs one.
var (
	errNotUTF8 = errors.New("expr: a string is not UTF-8")
	errNUL     = errors.New("expr: a string holds a NUL")
	errEmpty   = errors.New("expr: a name, a node reference, or a function is empty")
)

// checkText returns errNotUTF8 or errNUL for a string that is not UTF-8 or
// that holds a NUL, and otherwise nil.
func checkText(s string) error {
	switch {
	case !utf8.ValidString(s):
		return errNotUTF8
	case strings.IndexByte(s, 0) >= 0:
		return errNUL
	}
	return nil
}

// quoted returns the JSON of the string s, or the error of checkText.
func (e *encoder) quoted(s string) (string, error) {
	if err := checkText(s); err != nil {
		return "", err
	}
	return e.jsonOf(s)
}

// required returns the JSON of the string s, a field that a node needs, or
// errEmpty if s is "", or the error of checkText.
func (e *encoder) required(s string) (string, error) {
	if s == "" {
		return "", errEmpty
	}
	return e.quoted(s)
}

// jsonOf returns the JSON of v, a bool, an int64, a float64, or a string, as
// encoding/json writes it, without escaping <, >, and &.
func (e *encoder) jsonOf(v any) (string, error) {
	e.encoded.Reset()
	if err := e.enc.Encode(v); err != nil {
		return "", err
	}
	return string(bytes.TrimSuffix(e.encoded.Bytes(), []byte("\n"))), nil
}
