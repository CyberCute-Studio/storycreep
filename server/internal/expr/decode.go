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
// The AST's names are resolved as the JSON holds them: a variable is its key
// and its ref, without the scope that its source qualified it with, as
// global.x, and an attribute its keys and its refs, without the cmp. that
// its source may have named it with, neither of which the JSON holds. So
// Check, which resolves the names of an AST that Parse returns by what its
// source wrote, is not for it: it would resolve a variable anew by its key,
// as a bare name, which may name another variable, of the current board,
// rather than the global one that the ref names, and refuse an attribute
// that cmp. named whose component's key a variable's shadows. What reads a
// decoded AST against a project's declarations reads its names by their
// refs.
//
// It reads what the schema of ExpressionSchema or ScriptSchema takes, and
// nothing else, and ignores a key that an object of the AST does not define,
// as a consumer does (§6.4), so that it fails, with an error that wraps
// ErrDecode, on: JSON that is not one value, or not UTF-8, or that holds null
// anywhere, as the published format never does (§6.2), or a string that
// escapes a lone surrogate (value.CheckJSON); a node of an unknown kind, or
// with an unknown operator or a literal of an unknown type, as a consumer
// fails loudly on an unknown enum value (§6.4); a node at a place that its
// kind cannot be at, as Encode refuses it; a field that is missing or of
// another type than its kind's; a literal's value that is not of its type,
// as an int that is not integral or is beyond ±(2^53 − 1), though one
// written as 2.0 is the int 2 (§5.8.9), a float beyond
// ±1.7976931348623157e+308, the greatest double's shortest form, as it is
// written, though a double reads one up to half a unit in the last place
// beyond the greatest double as it (value.FloatBeyondSchema), or a string of
// more than MaxStringLength characters or that holds a NUL; a
// variable's name or a call's function that is not an identifier of at most
// MaxIdentifierLength characters, as the schema's pattern takes it, a
// reserved word included, an attribute's name that is not two such
// identifiers joined by a dot, or a ref that is not an ID of its kind in its
// canonical spelling; a call of more than MaxArguments arguments; and a
// script of more than MaxStatements statements. Beyond the schema, which
// cannot say how a number is written nor how large an AST is, it fails on a
// number written with an exponent beyond ±value.MaxExponent, as 0e1000,
// which is 0, but which no file holds, a literal's or one of a key that it
// ignores, as value.CheckJSON refuses it, and on an AST of more than
// MaxNodes nodes, or deeper than MaxDepth, its
// root 1 deep, which no AST that Check accepts is, so that Decode never
// recurses deeper than that. It fails on a ctx that is neither context with
// an error that wraps ErrContext.
func Decode(data []byte, ctx Context) (Node, error) {
	if ctx != ContextCondition && ctx != ContextScript {
		return nil, fmt.Errorf("%w: %q", ErrContext, ctx)
	}
	if problem := value.CheckJSON(data); problem != "" {
		return nil, fmt.Errorf("%w: it %s", ErrDecode, problem)
	}
	// One pass reads data whole, numbers as they are written, which the
	// decoder walks, so that no node's JSON is read again for each node
	// above it.
	var tree any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	d := &decoder{}
	n, err := d.node(tree, atRoot, 1)
	if err != nil {
		return nil, err
	}
	if (n.Kind() == KindScript) != (ctx == ContextScript) {
		return nil, fmt.Errorf("%w: a %s's AST is a script exactly when its context is", ErrDecode, ctx)
	}
	return n, nil
}

// decoder reads an AST's nodes, counting them.
type decoder struct {
	nodes int
}

// object is a node's object as Decode reads it, its keys' values: an object
// as an object, an array as an []any, a number as a json.Number, a string as
// a string, and a boolean as a bool.
type object = map[string]any

// field returns the required field key of a node of the kind kind, of the
// Go type T that Decode reads its JSON's type as.
func field[T any](obj object, kind Kind, key string) (T, error) {
	var zero T
	v, ok := obj[key]
	if !ok {
		return zero, fmt.Errorf("%w: a %s node has no %s", ErrDecode, kind, key)
	}
	t, ok := v.(T)
	if !ok {
		return zero, fmt.Errorf("%w: the %s of a %s node is of another type than its kind's", ErrDecode, key, kind)
	}
	return t, nil
}

// id reads the required field key of a node of the kind kind, an ID of an
// entity of the kind k in its canonical spelling.
func id(obj object, kind Kind, key string, k ids.Kind) (string, error) {
	s, err := field[string](obj, kind, key)
	if err != nil {
		return "", err
	}
	if !ids.IsCanonical(k, s) {
		return "", fmt.Errorf("%w: the %s of a %s node is not the ID of a %s in its canonical spelling", ErrDecode,
			key, kind, k)
	}
	return s, nil
}

// node reads the node v, at the place at and the depth depth.
func (d *decoder) node(v any, at place, depth int) (Node, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("%w: the AST is deeper than %d", ErrDecode, MaxDepth)
	}
	if d.nodes++; d.nodes > MaxNodes {
		return nil, fmt.Errorf("%w: the AST has more than %d nodes", ErrDecode, MaxNodes)
	}
	obj, ok := v.(object)
	if !ok {
		return nil, fmt.Errorf("%w: a node is not an object", ErrDecode)
	}
	k, err := field[string](obj, "", "kind")
	if err != nil {
		return nil, fmt.Errorf("%w: a node has no kind, or one that is not a string", ErrDecode)
	}
	kind := Kind(k)
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
func literal(obj object) (Node, error) {
	typ, err := field[string](obj, KindLit, "type")
	if err != nil {
		return nil, err
	}
	x, ok := obj["value"]
	if !ok {
		return nil, fmt.Errorf("%w: a lit node has no value", ErrDecode)
	}
	// The value's JSON, a number as it is written, which Read reads by the
	// literal's type.
	raw, err := json.Marshal(x)
	if err != nil {
		return nil, fmt.Errorf("%w: the value of a lit node: %w", ErrDecode, err)
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
	// beyond ±value.MaxExponent, which no canonical form has. The schema
	// refuses a float beyond the greatest double's shortest form, as it is
	// written, which Read reads as the greatest double.
	v, problem := value.FromJSON(raw).Read(typ, value.Checks{})
	if problem == "" && typ == value.Float && value.FloatBeyondSchema(raw) {
		problem = "is beyond ±1.7976931348623157e+308, the greatest double's shortest form, as it is written"
	}
	if problem != "" || json.Unmarshal(v.JSON(), dst) != nil {
		return nil, fmt.Errorf("%w: the value of a lit node of the type %s %s", ErrDecode, typ, problem)
	}
	if _, err := literalValue(n); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	return n, nil
}

// variable reads a variable, by its ref and its name, its key.
func variable(obj object) (Node, error) {
	ref, err := id(obj, KindVar, "ref", ids.Variable)
	if err != nil {
		return nil, err
	}
	name, err := field[string](obj, KindVar, "name")
	if err != nil {
		return nil, err
	}
	if len(name) > MaxIdentifierLength || !identifierName.MatchString(name) {
		return nil, fmt.Errorf("%w: the name of a var node is not an identifier of at most %d characters", ErrDecode,
			MaxIdentifierLength)
	}
	return &Var{Name: name, Key: name, Ref: ref}, nil
}

// attribute reads a component's attribute, by its ref, its attribute, and
// its name, <component_key>.<attribute_key>.
func attribute(obj object) (Node, error) {
	ref, err := id(obj, KindAttr, "ref", ids.Component)
	if err != nil {
		return nil, err
	}
	attr, err := id(obj, KindAttr, "attribute", ids.AttributeDef)
	if err != nil {
		return nil, err
	}
	name, err := field[string](obj, KindAttr, "name")
	if err != nil {
		return nil, err
	}
	// The pattern bounds each key to MaxIdentifierLength characters, as the
	// schema does.
	if !attributeName.MatchString(name) {
		return nil, fmt.Errorf("%w: the name of an attr node is not two keys joined by a dot", ErrDecode)
	}
	component, attribute, _ := strings.Cut(name, ".")
	return &Attr{Name: name, ComponentKey: component, AttributeKey: attribute, Ref: ref, Attribute: attr}, nil
}

// operand reads the field key of a node of the kind kind, a node at the place
// at, one deeper than the node.
func (d *decoder) operand(obj object, kind Kind, key string, at place, depth int) (Node, error) {
	v, ok := obj[key]
	if !ok {
		return nil, fmt.Errorf("%w: a %s node has no %s", ErrDecode, kind, key)
	}
	return d.node(v, at, depth+1)
}

// list reads the field key of a node of the kind kind, an array of at most
// most nodes at the place at, one deeper than the node.
func (d *decoder) list(obj object, kind Kind, key string, at place, depth, most int) ([]Node, error) {
	items, err := field[[]any](obj, kind, key)
	if err != nil {
		return nil, err
	}
	if len(items) > most {
		return nil, fmt.Errorf("%w: a %s node has more than %d %s", ErrDecode, kind, most, key)
	}
	nodes := make([]Node, len(items))
	for i, item := range items {
		n, err := d.node(item, at, depth+1)
		if err != nil {
			return nil, err
		}
		nodes[i] = n
	}
	return nodes, nil
}

func (d *decoder) unary(obj object, depth int) (Node, error) {
	s, err := field[string](obj, KindUnary, "op")
	if err != nil {
		return nil, err
	}
	op := UnaryOp(s)
	if _, ok := unaryVersions[op]; !ok {
		return nil, fmt.Errorf("%w: %q is no unary operator", ErrDecode, op)
	}
	operand, err := d.operand(obj, KindUnary, "operand", atOperand, depth)
	if err != nil {
		return nil, err
	}
	return &Unary{Op: op, Operand: operand}, nil
}

func (d *decoder) binary(obj object, depth int) (Node, error) {
	s, err := field[string](obj, KindBinary, "op")
	if err != nil {
		return nil, err
	}
	op := BinaryOp(s)
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

func (d *decoder) call(obj object, depth int) (Node, error) {
	function, err := field[string](obj, KindCall, "function")
	if err != nil {
		return nil, err
	}
	if len(function) > MaxIdentifierLength || !identifierName.MatchString(function) {
		return nil, fmt.Errorf("%w: the function of a call node is not an identifier of at most %d characters",
			ErrDecode, MaxIdentifierLength)
	}
	args, err := d.list(obj, KindCall, "args", atOperand, depth, MaxArguments)
	if err != nil {
		return nil, err
	}
	return &Call{Function: function, Args: args}, nil
}

func (d *decoder) assign(obj object, depth int) (Node, error) {
	s, err := field[string](obj, KindAssign, "op")
	if err != nil {
		return nil, err
	}
	op := AssignOp(s)
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

func (d *decoder) script(obj object, depth int) (Node, error) {
	statements, err := d.list(obj, KindScript, "statements", atStatement, depth, MaxStatements)
	if err != nil {
		return nil, err
	}
	return &Script{Statements: statements}, nil
}
