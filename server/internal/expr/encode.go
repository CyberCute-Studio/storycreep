package expr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
// ±(2^53 − 1), a float that is not finite, a string that is not UTF-8, an
// operator or a literal's type that the language does not have, or an
// assignment to anything but a variable or an attribute, or a script
// statement that is neither an assignment nor a call.
func Encode(n Node) ([]byte, error) {
	w, err := wire(n)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(w); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// The nodes as their JSON holds them, each struct's fields in alphabetical
// order, which encoding/json writes them in, and each child the wire form of
// its node.
type (
	wireLit struct {
		Kind  Kind   `json:"kind"`
		Type  string `json:"type"`
		Value any    `json:"value"`
	}
	wireVar struct {
		Kind Kind   `json:"kind"`
		Name string `json:"name"`
		Ref  string `json:"ref,omitempty"`
	}
	wireAttr struct {
		Attribute string `json:"attribute,omitempty"`
		Kind      Kind   `json:"kind"`
		Name      string `json:"name"`
		Ref       string `json:"ref,omitempty"`
	}
	wireNodeRef struct {
		Kind Kind   `json:"kind"`
		Ref  string `json:"ref"`
	}
	wireUnary struct {
		Kind    Kind    `json:"kind"`
		Op      UnaryOp `json:"op"`
		Operand any     `json:"operand"`
	}
	wireBinary struct {
		Kind  Kind     `json:"kind"`
		Left  any      `json:"left"`
		Op    BinaryOp `json:"op"`
		Right any      `json:"right"`
	}
	wireCall struct {
		Args     []any  `json:"args"`
		Function string `json:"function"`
		Kind     Kind   `json:"kind"`
	}
	wireAssign struct {
		Kind   Kind     `json:"kind"`
		Op     AssignOp `json:"op"`
		Target any      `json:"target"`
		Value  any      `json:"value"`
	}
	wireScript struct {
		Kind       Kind  `json:"kind"`
		Statements []any `json:"statements"`
	}
)

// The operators that the language has: those that the parser reads.
var (
	unaryOps  = map[UnaryOp]bool{OpNot: true, OpNeg: true}
	binaryOps = func() map[BinaryOp]bool {
		ops := make(map[BinaryOp]bool)
		for _, level := range levels {
			for _, op := range level {
				ops[op] = true
			}
		}
		return ops
	}()
	assignOpSet = func() map[AssignOp]bool {
		ops := make(map[AssignOp]bool)
		for _, op := range assignOps {
			ops[op] = true
		}
		return ops
	}()
)

// errNil is a nil node.
var errNil = errors.New("expr: a node is nil")

// wire returns n's wire form, or why n is not a node of an AST.
func wire(n Node) (any, error) {
	switch n := n.(type) {
	case *Lit:
		if n == nil {
			return nil, errNil
		}
		return wireLiteral(n)
	case *Var:
		if n == nil {
			return nil, errNil
		}
		return wireVar{Kind: KindVar, Name: n.Name, Ref: n.Ref}, nil
	case *Attr:
		if n == nil {
			return nil, errNil
		}
		return wireAttr{Attribute: n.Attribute, Kind: KindAttr, Name: n.Name, Ref: n.Ref}, nil
	case *NodeRef:
		if n == nil {
			return nil, errNil
		}
		return wireNodeRef{Kind: KindNodeRef, Ref: n.Ref}, nil
	case *Unary:
		if n == nil {
			return nil, errNil
		}
		if !unaryOps[n.Op] {
			return nil, fmt.Errorf("expr: %q is no unary operator", n.Op)
		}
		operand, err := wire(n.Operand)
		return wireUnary{Kind: KindUnary, Op: n.Op, Operand: operand}, err
	case *Binary:
		if n == nil {
			return nil, errNil
		}
		if !binaryOps[n.Op] {
			return nil, fmt.Errorf("expr: %q is no binary operator", n.Op)
		}
		left, err := wire(n.Left)
		if err != nil {
			return nil, err
		}
		right, err := wire(n.Right)
		return wireBinary{Kind: KindBinary, Left: left, Op: n.Op, Right: right}, err
	case *Call:
		if n == nil {
			return nil, errNil
		}
		args, err := wireAll(n.Args)
		return wireCall{Args: args, Function: n.Function, Kind: KindCall}, err
	case *Assign:
		if n == nil {
			return nil, errNil
		}
		if !assignOpSet[n.Op] {
			return nil, fmt.Errorf("expr: %q is no assignment operator", n.Op)
		}
		switch n.Target.(type) {
		case *Var, *Attr:
		default:
			return nil, errors.New("expr: an assignment's target is neither a variable nor an attribute")
		}
		target, err := wire(n.Target)
		if err != nil {
			return nil, err
		}
		v, err := wire(n.Value)
		return wireAssign{Kind: KindAssign, Op: n.Op, Target: target, Value: v}, err
	case *Script:
		if n == nil {
			return nil, errNil
		}
		for _, s := range n.Statements {
			switch s.(type) {
			case *Assign, *Call:
			default:
				return nil, errors.New("expr: a script's statement is neither an assignment nor a call")
			}
		}
		statements, err := wireAll(n.Statements)
		return wireScript{Kind: KindScript, Statements: statements}, err
	}
	return nil, errNil
}

// wireAll returns the wire forms of nodes, as an array, empty for none.
func wireAll(nodes []Node) ([]any, error) {
	out := make([]any, len(nodes))
	for i, n := range nodes {
		w, err := wire(n)
		if err != nil {
			return nil, err
		}
		out[i] = w
	}
	return out, nil
}

// wireLiteral returns a literal's wire form, its value of its type, or why it
// is no value of the language.
func wireLiteral(n *Lit) (any, error) {
	w := wireLit{Kind: KindLit, Type: n.Type}
	switch n.Type {
	case value.Bool:
		w.Value = n.Bool
	case value.Int:
		if n.Int < -semantics.MaxInt || n.Int > semantics.MaxInt {
			return nil, fmt.Errorf("expr: the int %d is beyond ±%d", n.Int, int64(semantics.MaxInt))
		}
		w.Value = n.Int
	case value.Float:
		if math.IsNaN(n.Float) || math.IsInf(n.Float, 0) {
			return nil, fmt.Errorf("expr: the float %v is not finite", n.Float)
		}
		f := n.Float
		if f == 0 {
			// -0 is 0 (§5.8.9), which encoding/json would write as -0.
			f = 0
		}
		w.Value = f
	case value.String:
		if !utf8.ValidString(n.Str) {
			return nil, errors.New("expr: a string is not UTF-8")
		}
		w.Value = n.Str
	default:
		return nil, fmt.Errorf("expr: %q is no type of a literal", n.Type)
	}
	return w, nil
}
