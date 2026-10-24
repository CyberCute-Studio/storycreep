package expr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
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
// Encode fails on a node that is not one of an AST: a nil one, an int beyond
// ±(2^53 − 1), a float that is not finite, a string that is not UTF-8, a
// literal's or a name's, an operator or a literal's type that the language
// does not have, or a node of a kind that its place in the AST cannot be of:
// an AST is an expression or a script, a script's statements are assignments
// and calls, an assignment's target is a variable or an attribute, and every
// other child of a node is an expression, neither an assignment nor a script.
//
// Encode writes an AST of any depth without recursing, as deep as a long
// chain of binary operators makes one, which Parse reads though the AST depth
// of §5.8.6 refuses it, so that no AST that Parse returns can exhaust the
// stack, which no program recovers from.
func Encode(n Node) ([]byte, error) {
	e := &encoder{}
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

// fits reports whether n, which is not nil, can be at the place at.
func fits(n Node, at place) bool {
	switch n.(type) {
	case *Script:
		return at == atRoot
	case *Assign:
		return at == atStatement
	case *Call:
		return at != atTarget
	case *Var, *Attr:
		return at != atStatement
	}
	return at == atRoot || at == atOperand
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

// errNil is a nil node.
var errNil = errors.New("expr: a node is nil")

// isNil reports whether n is nil, or a nil pointer, which every kind of node
// is a pointer to.
func isNil(n Node) bool {
	return n == nil || reflect.ValueOf(n).IsNil()
}

// node writes n, which is at the place at, but for its children, which it
// adds, with what follows each, to the parts that the encoder has yet to
// write, or returns why n is not a node of an AST there. Each object's keys
// are in alphabetical order.
func (e *encoder) node(n Node, at place) error {
	if isNil(n) {
		return errNil
	}
	if !fits(n, at) {
		return fmt.Errorf("expr: a node of the kind %s cannot be %s", n.Kind(), places[at])
	}
	switch n := n.(type) {
	case *Lit:
		return e.literal(n)
	case *Var:
		name, err := e.quoted(n.Name)
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
		name, err := e.quoted(n.Name)
		if err != nil {
			return err
		}
		e.b.WriteString(`"kind":"attr","name":` + name)
		if err := e.optional("ref", n.Ref); err != nil {
			return err
		}
		e.b.WriteByte('}')
	case *NodeRef:
		ref, err := e.quoted(n.Ref)
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
		function, err := e.quoted(n.Function)
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
	var v any
	switch n.Type {
	case value.Bool:
		v = n.Bool
	case value.Int:
		if n.Int < -semantics.MaxInt || n.Int > semantics.MaxInt {
			return fmt.Errorf("expr: the int %d is beyond ±%d", n.Int, int64(semantics.MaxInt))
		}
		v = n.Int
	case value.Float:
		if math.IsNaN(n.Float) || math.IsInf(n.Float, 0) {
			return fmt.Errorf("expr: the float %v is not finite", n.Float)
		}
		f := n.Float
		if f == 0 {
			// -0 is 0 (§5.8.9), which encoding/json would write as -0.
			f = 0
		}
		v = f
	case value.String:
		if !utf8.ValidString(n.Str) {
			return errNotUTF8
		}
		v = n.Str
	default:
		return fmt.Errorf("expr: %q is no type of a literal", n.Type)
	}
	j, err := e.jsonOf(v)
	if err != nil {
		return err
	}
	e.b.WriteString(`{"kind":"lit","type":"` + n.Type + `","value":` + j + "}")
	return nil
}

// errNotUTF8 is a string that is not UTF-8, which encoding/json would write
// with U+FFFD in place of each byte that is not.
var errNotUTF8 = errors.New("expr: a string is not UTF-8")

// quoted returns the JSON of the string s, or errNotUTF8.
func (e *encoder) quoted(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", errNotUTF8
	}
	return e.jsonOf(s)
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
