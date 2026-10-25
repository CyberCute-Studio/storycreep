package expr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// ErrDecode is Decode's error for JSON that is not the AST of a published
// expression.
var ErrDecode = errors.New("expr: not the JSON of a published AST")

// The patterns of the names that a published AST writes, which its schema
// holds them to too.
var (
	identifierName = regexp.MustCompile(identifierPattern)
	attributeName  = regexp.MustCompile(attributePattern)
)

// Decode reads the AST that data holds as the published format holds one
// (§5.8.8, §6.5): the JSON that Encode writes of an AST whose names Check has
// resolved, of a condition when ctx is ContextCondition, an expression, and
// of a script when ctx is ContextScript, a script. Encode writes the AST that
// it returns as data's canonical form.
//
// It reads what the schema of ExpressionSchema or ScriptSchema takes, and
// nothing else, and ignores a key that an object of the AST does not define,
// as a consumer does (§6.4), so that it fails, with an error that wraps
// ErrDecode, on: JSON that is not one value, or that holds null anywhere, as
// the published format never does (§6.2), or a string that escapes a lone
// surrogate; a node of an unknown kind, or with an unknown operator or a
// literal of an unknown type, as a consumer fails loudly on an unknown enum
// value (§6.4); a node at a place that its kind cannot be at, as Encode
// refuses it; a field that is missing or of another type than its kind's; a
// literal's value that is not of its type, as an int that is not integral or
// is beyond ±(2^53 − 1), though one written as 2.0 is the int 2 (§5.8.9), a
// float beyond a double's range, or a string of more than MaxStringLength
// characters or that holds a NUL; a name that is not a key, or a ref that is
// not an ID of its kind in its canonical spelling; a call of more than
// MaxArguments arguments; a script of more than MaxStatements statements; and
// an AST of more than MaxNodes nodes, or deeper than MaxDepth, its root 1
// deep, which no AST that Check accepts is, so that Decode never recurses
// deeper than that. It fails on a ctx that is neither context with an error
// that wraps ErrContext.
func Decode(data []byte, ctx Context) (Node, error) {
	if ctx != ContextCondition && ctx != ContextScript {
		return nil, fmt.Errorf("%w: %q", ErrContext, ctx)
	}
	if err := checkJSON(data); err != nil {
		return nil, err
	}
	d := &decoder{}
	n, err := d.node(json.RawMessage(data), atRoot, 1)
	if err != nil {
		return nil, err
	}
	if (n.Kind() == KindScript) != (ctx == ContextScript) {
		return nil, fmt.Errorf("%w: a %s's AST is a script exactly when its context is", ErrDecode, ctx)
	}
	return n, nil
}

// checkJSON returns an error that wraps ErrDecode when data is not one JSON
// value, holds null anywhere, or escapes a lone surrogate.
func checkJSON(data []byte) error {
	if !json.Valid(data) {
		return fmt.Errorf("%w: it is not one JSON value", ErrDecode)
	}
	if value.EscapesLoneSurrogate(data) {
		return fmt.Errorf("%w: a string escapes a lone surrogate", ErrDecode)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			// json.Valid read data whole, so the only error is its end.
			return nil
		}
		if tok == nil {
			return fmt.Errorf("%w: it holds null", ErrDecode)
		}
	}
}

// decoder reads an AST's nodes, counting them.
type decoder struct {
	nodes int
}

// fields returns the object raw as its keys' values, or an error.
func fields(raw json.RawMessage, what string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("%w: %s is not an object", ErrDecode, what)
	}
	return obj, nil
}

// field reads the required field key of a node of the kind kind into v.
func field(obj map[string]json.RawMessage, kind Kind, key string, v any) error {
	raw, ok := obj[key]
	if !ok {
		return fmt.Errorf("%w: a %s node has no %s", ErrDecode, kind, key)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%w: the %s of a %s node is of another type than its kind's", ErrDecode, key, kind)
	}
	return nil
}

// id reads the required field key of a node of the kind kind, an ID of an
// entity of the kind k in its canonical spelling.
func id(obj map[string]json.RawMessage, kind Kind, key string, k ids.Kind) (string, error) {
	var s string
	if err := field(obj, kind, key, &s); err != nil {
		return "", err
	}
	if !canonical(k, s) {
		return "", fmt.Errorf("%w: the %s of a %s node is not the ID of a %s in its canonical spelling", ErrDecode,
			key, kind, k)
	}
	return s, nil
}

// node reads the node raw, at the place at and the depth depth.
func (d *decoder) node(raw json.RawMessage, at place, depth int) (Node, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("%w: the AST is deeper than %d", ErrDecode, MaxDepth)
	}
	if d.nodes++; d.nodes > MaxNodes {
		return nil, fmt.Errorf("%w: the AST has more than %d nodes", ErrDecode, MaxNodes)
	}
	obj, err := fields(raw, "a node")
	if err != nil {
		return nil, err
	}
	var kind Kind
	if err := field(obj, "", "kind", &kind); err != nil {
		return nil, fmt.Errorf("%w: a node has no kind, or one that is not a string", ErrDecode)
	}
	if _, ok := kindVersions[kind]; !ok {
		return nil, fmt.Errorf("%w: %q is no kind of node", ErrDecode, kind)
	}
	var n Node
	switch kind {
	case KindLit:
		n, err = literal(obj)
	case KindVar:
		n, err = variable(obj)
	case KindAttr:
		n, err = attribute(obj)
	case KindNodeRef:
		var ref string
		ref, err = id(obj, kind, "ref", ids.Node)
		n = &NodeRef{Ref: ref}
	case KindUnary:
		n, err = d.unary(obj, depth)
	case KindBinary:
		n, err = d.binary(obj, depth)
	case KindCall:
		n, err = d.call(obj, depth)
	case KindAssign:
		n, err = d.assign(obj, depth)
	case KindScript:
		n, err = d.script(obj, depth)
	}
	if err != nil {
		return nil, err
	}
	if err := check(n, at); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	return n, nil
}

// literal reads a literal, whose value is read as its type says, never by
// its JSON form (§5.8.9), as package value reads a stored value.
func literal(obj map[string]json.RawMessage) (Node, error) {
	var typ string
	if err := field(obj, KindLit, "type", &typ); err != nil {
		return nil, err
	}
	raw, ok := obj["value"]
	if !ok {
		return nil, fmt.Errorf("%w: a lit node has no value", ErrDecode)
	}
	n := &Lit{Type: typ}
	var dst any
	switch typ {
	case value.Bool:
		dst = &n.Bool
	case value.Int:
		dst = &n.Int
	case value.Float:
		dst = &n.Float
	case value.String:
		dst = &n.Str
	default:
		return nil, fmt.Errorf("%w: %q is no type of a literal", ErrDecode, typ)
	}
	// Read takes an int written as 2.0, reads an int's decimal exactly, and
	// refuses a number beyond a double's range or written with an exponent
	// beyond ±value.MaxExponent, which no canonical form has.
	v, problem := value.FromJSON(raw).Read(typ, value.Checks{})
	if problem != "" || json.Unmarshal(v.JSON(), dst) != nil {
		return nil, fmt.Errorf("%w: the value of a lit node of the type %s %s", ErrDecode, typ, problem)
	}
	if _, err := literalValue(n); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	return n, nil
}

// variable reads a variable, by its ref and its name, its key.
func variable(obj map[string]json.RawMessage) (Node, error) {
	ref, err := id(obj, KindVar, "ref", ids.Variable)
	if err != nil {
		return nil, err
	}
	var name string
	if err := field(obj, KindVar, "name", &name); err != nil {
		return nil, err
	}
	if len(name) > MaxIdentifierLength || !identifierName.MatchString(name) {
		return nil, fmt.Errorf("%w: the name of a var node is not a key", ErrDecode)
	}
	return &Var{Name: name, Key: name, Ref: ref}, nil
}

// attribute reads a component's attribute, by its ref, its attribute, and
// its name, <component_key>.<attribute_key>.
func attribute(obj map[string]json.RawMessage) (Node, error) {
	ref, err := id(obj, KindAttr, "ref", ids.Component)
	if err != nil {
		return nil, err
	}
	attr, err := id(obj, KindAttr, "attribute", ids.AttributeDef)
	if err != nil {
		return nil, err
	}
	var name string
	if err := field(obj, KindAttr, "name", &name); err != nil {
		return nil, err
	}
	component, attribute, _ := strings.Cut(name, ".")
	if !attributeName.MatchString(name) || len(component) > MaxIdentifierLength ||
		len(attribute) > MaxIdentifierLength {
		return nil, fmt.Errorf("%w: the name of an attr node is not two keys joined by a dot", ErrDecode)
	}
	return &Attr{Name: name, ComponentKey: component, AttributeKey: attribute, Ref: ref, Attribute: attr}, nil
}

// operand reads the field key of a node of the kind kind, a node at the place
// at, one deeper than the node.
func (d *decoder) operand(obj map[string]json.RawMessage, kind Kind, key string, at place, depth int) (Node, error) {
	raw, ok := obj[key]
	if !ok {
		return nil, fmt.Errorf("%w: a %s node has no %s", ErrDecode, kind, key)
	}
	return d.node(raw, at, depth+1)
}

// list reads the field key of a node of the kind kind, an array of at most
// most nodes at the place at, one deeper than the node.
func (d *decoder) list(obj map[string]json.RawMessage, kind Kind, key string, at place, depth, most int) ([]Node, error) {
	var raws []json.RawMessage
	if err := field(obj, kind, key, &raws); err != nil {
		return nil, err
	}
	if len(raws) > most {
		return nil, fmt.Errorf("%w: a %s node has more than %d %s", ErrDecode, kind, most, key)
	}
	nodes := make([]Node, len(raws))
	for i, raw := range raws {
		n, err := d.node(raw, at, depth+1)
		if err != nil {
			return nil, err
		}
		nodes[i] = n
	}
	return nodes, nil
}

func (d *decoder) unary(obj map[string]json.RawMessage, depth int) (Node, error) {
	var op UnaryOp
	if err := field(obj, KindUnary, "op", &op); err != nil {
		return nil, err
	}
	if _, ok := unaryVersions[op]; !ok {
		return nil, fmt.Errorf("%w: %q is no unary operator", ErrDecode, op)
	}
	operand, err := d.operand(obj, KindUnary, "operand", atOperand, depth)
	if err != nil {
		return nil, err
	}
	return &Unary{Op: op, Operand: operand}, nil
}

func (d *decoder) binary(obj map[string]json.RawMessage, depth int) (Node, error) {
	var op BinaryOp
	if err := field(obj, KindBinary, "op", &op); err != nil {
		return nil, err
	}
	if _, ok := binaryVersions[op]; !ok {
		return nil, fmt.Errorf("%w: %q is no binary operator", ErrDecode, op)
	}
	left, err := d.operand(obj, KindBinary, "left", atOperand, depth)
	if err != nil {
		return nil, err
	}
	right, err := d.operand(obj, KindBinary, "right", atOperand, depth)
	if err != nil {
		return nil, err
	}
	return &Binary{Op: op, Left: left, Right: right}, nil
}

func (d *decoder) call(obj map[string]json.RawMessage, depth int) (Node, error) {
	var function string
	if err := field(obj, KindCall, "function", &function); err != nil {
		return nil, err
	}
	if len(function) > MaxIdentifierLength || !identifierName.MatchString(function) {
		return nil, fmt.Errorf("%w: the function of a call node is not a name", ErrDecode)
	}
	args, err := d.list(obj, KindCall, "args", atOperand, depth, MaxArguments)
	if err != nil {
		return nil, err
	}
	return &Call{Function: function, Args: args}, nil
}

func (d *decoder) assign(obj map[string]json.RawMessage, depth int) (Node, error) {
	var op AssignOp
	if err := field(obj, KindAssign, "op", &op); err != nil {
		return nil, err
	}
	if _, ok := assignVersions[op]; !ok {
		return nil, fmt.Errorf("%w: %q is no assignment operator", ErrDecode, op)
	}
	target, err := d.operand(obj, KindAssign, "target", atTarget, depth)
	if err != nil {
		return nil, err
	}
	val, err := d.operand(obj, KindAssign, "value", atOperand, depth)
	if err != nil {
		return nil, err
	}
	return &Assign{Op: op, Target: target, Value: val}, nil
}

func (d *decoder) script(obj map[string]json.RawMessage, depth int) (Node, error) {
	statements, err := d.list(obj, KindScript, "statements", atStatement, depth, MaxStatements)
	if err != nil {
		return nil, err
	}
	return &Script{Statements: statements}, nil
}
