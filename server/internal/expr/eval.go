package expr

import (
	"cmp"
	"math"
	"strconv"

	"github.com/CyberCute-Studio/storycreep/server/internal/semantics"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// Value is a value of the language as an evaluator holds it (PRD §5.8.3): a
// bool, an int, a float, or a string, by its Type, value.Bool, value.Int,
// value.Float, or value.String, whose value is in Bool, Int, Float, or Str, as
// a literal's is. An int is within ±(2^53 − 1), a float finite and never -0,
// and a string of code points, as every value of the language is (§5.8.9).
// The zero Value is none, of no type.
type Value struct {
	Type  string
	Bool  bool
	Int   int64
	Float float64
	Str   string
}

// BoolValue, IntValue, FloatValue, and StringValue return the value of
// their type.
func BoolValue(b bool) Value { return Value{Type: value.Bool, Bool: b} }

// IntValue returns the int n.
func IntValue(n int64) Value { return Value{Type: value.Int, Int: n} }

// FloatValue returns the float f.
func FloatValue(f float64) Value { return Value{Type: value.Float, Float: f} }

// StringValue returns the string s.
func StringValue(s string) Value { return Value{Type: value.String, Str: s} }

// String returns v as to_string writes it (§5.8.9): an int as a plain
// decimal, a float as its shortest round-tripping decimal, a bool as true or
// false, and a string as it is.
func (v Value) String() string {
	switch v.Type {
	case value.Bool:
		return strconv.FormatBool(v.Bool)
	case value.Int:
		return semantics.FormatInt(v.Int)
	case value.Float:
		return semantics.FormatFloat(v.Float)
	}
	return v.Str
}

// float returns v, a number, as a float, an int widened exactly, as an int
// within ±(2^53 − 1) is.
func (v Value) float() float64 {
	if v.Type == value.Int {
		return float64(v.Int)
	}
	return v.Float
}

// Env is the state that an evaluation reads and changes (PRD §5.8.5): the
// values of the variables and of the components' runtime attributes, by their
// IDs, of their types, an enum's as a string, and how many times the flow has
// entered each node, and what it reports to: the signals that a script emits,
// and the runtime errors that an evaluation meets (§5.8.7). A runtime
// implements it, as package flow's does.
type Env interface {
	// Variable returns the value of the variable whose ID is id.
	Variable(id string) Value
	// SetVariable sets it to v, of its type.
	SetVariable(id string, v Value)
	// Attribute returns the value of the runtime attribute whose definition's
	// ID is attribute of the component whose ID is component.
	Attribute(component, attribute string) Value
	// SetAttribute sets it to v, of its type, an enum's one of its options.
	SetAttribute(component, attribute string, v Value)
	// Visits returns how many times the flow has entered the node whose ID
	// is node, 0 for none.
	Visits(node string) int64
	// Signal reports the signal of the name name that a script emits, with
	// its value, or with nil for none (§5.8.4).
	Signal(name string, value *Value)
	// Error reports the runtime error code, which the evaluation goes on
	// after, but for StepBudgetExceeded.
	Error(code semantics.RuntimeError)
}

// MaxSteps is the evaluation step budget of an expression (§5.8.6): the most
// steps that Condition and Run take of one AST, each node of it that they
// reach being one, whether they evaluate it or, as an assignment's target,
// read it, but for an operand of && or || that is not evaluated, since the
// other decided the result. No AST that Check accepts comes near it: one of at
// most MaxNodes nodes, without loops, reaches each of its nodes once.
const MaxSteps = 10_000

// StepBudgetExceeded is the runtime error of an evaluation that reaches a
// node beyond MaxSteps steps, of which the evaluation evaluates nothing more.
const StepBudgetExceeded semantics.RuntimeError = "step_budget_exceeded"

// Condition evaluates n, the AST of a condition, against env, and returns its
// value (§5.8.5): false when its evaluation exceeds MaxSteps, as the zero
// value of a bool, which it reports as StepBudgetExceeded. An operation that
// fails reports its runtime error, as a division by zero, and yields the zero
// value of its result's type, which the evaluation goes on with, so that
// 1 / 0 + 5 is 5 (§5.8.7). && and || evaluate their right operand only when
// the left one does not decide the result.
//
// n is an AST that Check or CheckResolved accepted as a condition's, its
// names resolved. Of any other, what Condition returns is not defined, but it
// returns, without a panic, once it has taken at most MaxSteps steps: a node
// that is nil, or a nil pointer of a kind, as (*Lit)(nil), is none.
func Condition(n Node, env Env) bool {
	e := &evaluator{env: env}
	defer e.stop()
	return e.eval(n).Bool
}

// Run runs s, the AST of a script, against env (§5.8.5): its statements in
// order, each assignment of its value, as Condition evaluates one, to its
// target, and each signal reported. An assignment's value is of its target's
// type: an int is widened for a float, and op= is op of the target's value
// and the assignment's, as a + b is for += (§5.8.3). When the evaluation
// exceeds MaxSteps, Run reports StepBudgetExceeded, and the statement that
// it exceeded them in, and those after it, have no effect.
//
// s is an AST that Check or CheckResolved accepted as a script's, as
// Condition says.
func Run(s *Script, env Env) {
	e := &evaluator{env: env}
	defer e.stop()
	if s == nil {
		return
	}
	e.step()
	for _, st := range s.Statements {
		switch st := st.(type) {
		case *Assign:
			if st != nil {
				e.assign(st)
			}
		case *Call:
			e.eval(st)
		}
	}
}

// isNil reports whether n is nil, or a nil pointer of a kind of node, which
// check tells first of any node.
func isNil(n Node) bool {
	return check(n, atRoot) == errNil
}

// evaluator evaluates an AST against its env, counting its steps.
type evaluator struct {
	env   Env
	steps int
}

// exceeded is the panic with which an evaluation stops once it has taken
// MaxSteps steps, which stop recovers.
type exceeded struct{}

// step counts a step of the evaluation, and stops it beyond MaxSteps.
func (e *evaluator) step() {
	if e.steps++; e.steps > MaxSteps {
		panic(exceeded{})
	}
}

// stop recovers from an evaluation that exceeded its steps, which it reports,
// and passes any other panic on.
func (e *evaluator) stop() {
	if r := recover(); r != nil {
		if _, ok := r.(exceeded); !ok {
			panic(r)
		}
		e.env.Error(StepBudgetExceeded)
	}
}

// report reports the runtime error code, if it is one.
func (e *evaluator) report(code semantics.RuntimeError) {
	if code != "" {
		e.env.Error(code)
	}
}

// intResult returns the value of an operation of an int result, having
// reported its runtime error, and floatResult of a float result.
func (e *evaluator) intResult(n int64, code semantics.RuntimeError) Value {
	e.report(code)
	return IntValue(n)
}

func (e *evaluator) floatResult(f float64, code semantics.RuntimeError) Value {
	e.report(code)
	return FloatValue(f)
}

// eval evaluates the expression n and returns its value, or none of a node
// that is no expression, nil or a nil pointer among them.
func (e *evaluator) eval(n Node) Value {
	if isNil(n) {
		return Value{}
	}
	e.step()
	switch n := n.(type) {
	case *Lit:
		return Value{Type: n.Type, Bool: n.Bool, Int: n.Int, Float: n.Float, Str: n.Str}
	case *Var:
		return e.env.Variable(n.Ref)
	case *Attr:
		return e.env.Attribute(n.Ref, n.Attribute)
	case *Unary:
		v := e.eval(n.Operand)
		switch {
		case n.Op == OpNot:
			return BoolValue(!v.Bool)
		case v.Type == value.Int:
			return e.intResult(semantics.NegInt(v.Int))
		}
		return e.floatResult(semantics.NegFloat(v.float()))
	case *Binary:
		return e.binary(n)
	case *Call:
		b, ok := builtins[n.Function]
		f := evaluations[n.Function]
		if !ok || f == nil || !b.takes(len(n.Args)) {
			return Value{}
		}
		return f(e, n.Args)
	}
	return Value{}
}

// binary evaluates the binary operation n, its left operand first, as the
// types of its operands say (§5.8.3): an int beside a float is widened, + of
// two strings concatenates them, and && and || short-circuit.
func (e *evaluator) binary(n *Binary) Value {
	l := e.eval(n.Left)
	switch n.Op {
	case OpAnd:
		if !l.Bool {
			return BoolValue(false)
		}
		return BoolValue(e.eval(n.Right).Bool)
	case OpOr:
		if l.Bool {
			return BoolValue(true)
		}
		return BoolValue(e.eval(n.Right).Bool)
	}
	r := e.eval(n.Right)
	switch n.Op {
	case OpEq:
		return BoolValue(order(l, r) == 0)
	case OpNe:
		return BoolValue(order(l, r) != 0)
	case OpLt:
		return BoolValue(order(l, r) < 0)
	case OpLte:
		return BoolValue(order(l, r) <= 0)
	case OpGt:
		return BoolValue(order(l, r) > 0)
	case OpGte:
		return BoolValue(order(l, r) >= 0)
	}
	return e.arithmetic(n.Op, l, r)
}

// order returns -1, 0, or +1 as l is less than, equal to, or greater than r,
// two values that the language compares: numbers by their values, an int
// beside a float widened, strings by their code points (semantics.Compare),
// and bools, which only == and != compare, false before true.
func order(l, r Value) int {
	switch {
	case l.Type == value.Int && r.Type == value.Int:
		return cmp.Compare(l.Int, r.Int)
	case l.Type == value.String:
		return semantics.Compare(l.Str, r.Str)
	case l.Type == value.Bool:
		switch {
		case l.Bool == r.Bool:
			return 0
		case r.Bool:
			return -1
		}
		return 1
	}
	return cmp.Compare(l.float(), r.float())
}

// arithmetic applies the arithmetic operator op, +, -, *, /, or %, to l and
// r: of two ints an int, of a string + a string their concatenation, and
// otherwise a float, an int widened, each by its operation of package
// semantics, which reports its runtime error and yields its zero value
// (§5.8.9).
func (e *evaluator) arithmetic(op BinaryOp, l, r Value) Value {
	if l.Type == value.String {
		s, code := semantics.Concat(l.Str, r.Str)
		e.report(code)
		return StringValue(s)
	}
	if l.Type == value.Int && r.Type == value.Int {
		switch op {
		case OpAdd:
			return e.intResult(semantics.AddInt(l.Int, r.Int))
		case OpSub:
			return e.intResult(semantics.SubInt(l.Int, r.Int))
		case OpMul:
			return e.intResult(semantics.MulInt(l.Int, r.Int))
		case OpDiv:
			return e.intResult(semantics.DivInt(l.Int, r.Int))
		}
		return e.intResult(semantics.ModInt(l.Int, r.Int))
	}
	x, y := l.float(), r.float()
	switch op {
	case OpAdd:
		return e.floatResult(semantics.AddFloat(x, y))
	case OpSub:
		return e.floatResult(semantics.SubFloat(x, y))
	case OpMul:
		return e.floatResult(semantics.MulFloat(x, y))
	}
	return e.floatResult(semantics.DivFloat(x, y))
}

// assignOperators are the binary operators of each op=.
var assignOperators = map[AssignOp]BinaryOp{
	OpAddAssign: OpAdd, OpSubAssign: OpSub, OpMulAssign: OpMul, OpDivAssign: OpDiv, OpModAssign: OpMod,
}

// assign runs the assignment a: it reads its target, evaluates its value,
// and sets the target to the value, or, of op=, to op of the two, of the
// target's type, an int widened for a float (§5.8.3).
func (e *evaluator) assign(a *Assign) {
	e.step()
	if isNil(a.Target) {
		return
	}
	var target Value
	switch t := a.Target.(type) {
	case *Var:
		e.step()
		target = e.env.Variable(t.Ref)
	case *Attr:
		e.step()
		target = e.env.Attribute(t.Ref, t.Attribute)
	default:
		return
	}
	v := e.eval(a.Value)
	if op, ok := assignOperators[a.Op]; ok {
		v = e.arithmetic(op, target, v)
	}
	if target.Type == value.Float && v.Type == value.Int {
		v = FloatValue(v.float())
	}
	switch t := a.Target.(type) {
	case *Var:
		e.env.SetVariable(t.Ref, v)
	case *Attr:
		e.env.SetAttribute(t.Ref, t.Attribute, v)
	}
}

// numbers returns the widened type's values of args, numbers: ints of ints,
// and otherwise floats, each int widened, as min, max, and clamp take them.
func (e *evaluator) numbers(args []Node) ([]Value, bool) {
	vs := make([]Value, len(args))
	ints := true
	for i, a := range args {
		vs[i] = e.eval(a)
		ints = ints && vs[i].Type == value.Int
	}
	if !ints {
		for i, v := range vs {
			vs[i] = FloatValue(v.float())
		}
	}
	return vs, ints
}

// pick returns the value of min or max, of the widened type of its two
// arguments: the one that ints picks of two ints, or floats of two floats.
func (e *evaluator) pick(args []Node, ints func(a, b int64) int64, floats func(a, b float64) float64) Value {
	vs, isInt := e.numbers(args)
	if isInt {
		return IntValue(ints(vs[0].Int, vs[1].Int))
	}
	return FloatValue(floats(vs[0].Float, vs[1].Float))
}

// rounding returns the value of floor, ceil, or round of a number: an int is
// itself, and a float rounded by f, which reports IntegerOverflow beyond
// ±(2^53 − 1).
func (e *evaluator) rounding(args []Node, f func(float64) (int64, semantics.RuntimeError)) Value {
	v := e.eval(args[0])
	if v.Type == value.Int {
		return v
	}
	return e.intResult(f(v.Float))
}

// text returns the string of args[i].
func (e *evaluator) text(args []Node, i int) string {
	return e.eval(args[i]).Str
}

// node returns the ID of the node that args[0] references, a node reference,
// which it counts as a step, or "" of anything else.
func (e *evaluator) node(args []Node) string {
	e.step()
	if r, ok := args[0].(*NodeRef); ok && r != nil {
		return r.Ref
	}
	return ""
}

// evaluation evaluates a call of a built-in, of the arguments that Check
// accepted of a call of it, their number among them.
type evaluation func(e *evaluator, args []Node) Value

// evaluations are the built-ins' evaluations (§5.8.4), by their names, one
// of each of builtins, which TestEveryBuiltinEvaluates checks. init sets
// them, since each reaches the evaluator, which reads builtins, so that
// builtins' initialization would otherwise depend on itself.
var evaluations map[string]evaluation

func init() {
	text := func(f func(string) string) evaluation {
		return func(e *evaluator, args []Node) Value { return StringValue(f(e.text(args, 0))) }
	}
	predicate := func(f func(s, t string) bool) evaluation {
		return func(e *evaluator, args []Node) Value {
			s := e.text(args, 0)
			return BoolValue(f(s, e.text(args, 1)))
		}
	}
	evaluations = map[string]evaluation{
		"min": func(e *evaluator, args []Node) Value {
			return e.pick(args, func(a, b int64) int64 { return min(a, b) },
				func(a, b float64) float64 { return min(a, b) })
		},
		"max": func(e *evaluator, args []Node) Value {
			return e.pick(args, func(a, b int64) int64 { return max(a, b) },
				func(a, b float64) float64 { return max(a, b) })
		},
		"abs": func(e *evaluator, args []Node) Value {
			v := e.eval(args[0])
			if v.Type == value.Int {
				return IntValue(max(v.Int, -v.Int))
			}
			return FloatValue(math.Abs(v.float()))
		},
		"floor": func(e *evaluator, args []Node) Value { return e.rounding(args, semantics.Floor) },
		"ceil":  func(e *evaluator, args []Node) Value { return e.rounding(args, semantics.Ceil) },
		"round": func(e *evaluator, args []Node) Value { return e.rounding(args, semantics.Round) },
		// clamp is min(max(x, lo), hi), so that it is hi when lo is greater
		// than hi.
		"clamp": func(e *evaluator, args []Node) Value {
			vs, ints := e.numbers(args)
			if ints {
				return IntValue(min(max(vs[0].Int, vs[1].Int), vs[2].Int))
			}
			return FloatValue(min(max(vs[0].Float, vs[1].Float), vs[2].Float))
		},
		"to_int": func(e *evaluator, args []Node) Value {
			v := e.eval(args[0])
			switch v.Type {
			case value.Int:
				return v
			case value.String:
				return e.intResult(semantics.ParseInt(v.Str))
			}
			return e.intResult(semantics.Trunc(v.Float))
		},
		"to_float": func(e *evaluator, args []Node) Value {
			v := e.eval(args[0])
			if v.Type == value.String {
				return e.floatResult(semantics.ParseFloat(v.Str))
			}
			return FloatValue(v.float())
		},
		"to_string":   func(e *evaluator, args []Node) Value { return StringValue(e.eval(args[0]).String()) },
		"len":         func(e *evaluator, args []Node) Value { return IntValue(int64(semantics.Len(e.text(args, 0)))) },
		"contains":    predicate(semantics.Contains),
		"starts_with": predicate(semantics.StartsWith),
		"ends_with":   predicate(semantics.EndsWith),
		"lower":       text(semantics.Lower),
		"upper":       text(semantics.Upper),
		"trim":        text(semantics.Trim),
		"visited":     func(e *evaluator, args []Node) Value { return BoolValue(e.env.Visits(e.node(args)) > 0) },
		"visits":      func(e *evaluator, args []Node) Value { return IntValue(e.env.Visits(e.node(args))) },
		// signal reports the signal of its first argument's name, a string
		// literal, with its second argument's value, if it has one, and is
		// no value itself.
		"signal": func(e *evaluator, args []Node) Value {
			name := e.text(args, 0)
			if len(args) == 1 {
				e.env.Signal(name, nil)
				return Value{}
			}
			v := e.eval(args[1])
			e.env.Signal(name, &v)
			return Value{}
		},
	}
}
