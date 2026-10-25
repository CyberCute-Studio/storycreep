package expr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/semantics"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// testEnv is an Env of maps, which records what an evaluation reports.
type testEnv struct {
	vars    map[string]Value
	attrs   map[[2]string]Value
	visits  map[string]int64
	signals []string
	errors  []semantics.RuntimeError
}

func newTestEnv() *testEnv {
	return &testEnv{vars: make(map[string]Value), attrs: make(map[[2]string]Value), visits: make(map[string]int64)}
}

func (e *testEnv) Variable(id string) Value          { return e.vars[id] }
func (e *testEnv) SetVariable(id string, v Value)    { e.vars[id] = v }
func (e *testEnv) Attribute(c, a string) Value       { return e.attrs[[2]string{c, a}] }
func (e *testEnv) SetAttribute(c, a string, v Value) { e.attrs[[2]string{c, a}] = v }
func (e *testEnv) Visits(node string) int64          { return e.visits[node] }
func (e *testEnv) Error(code semantics.RuntimeError) { e.errors = append(e.errors, code) }
func (e *testEnv) Signal(name string, v *Value) {
	if v == nil {
		e.signals = append(e.signals, name)
		return
	}
	e.signals = append(e.signals, name+"="+v.Type+":"+v.String())
}

// envOf returns a testEnv of testProject's values: trust 2, met true, name
// "Ann", rate 0.5, the harbour's trust 7, scratch 0, elara's trust 1 and
// mood calm, and one visit of nodeEntry.
func envOf() *testEnv {
	e := newTestEnv()
	e.vars[varTrust] = IntValue(2)
	e.vars[varMet] = BoolValue(true)
	e.vars[varName] = StringValue("Ann")
	e.vars[varRate] = FloatValue(0.5)
	e.vars[varBoardTrst] = IntValue(7)
	e.vars[varScratch] = IntValue(0)
	e.attrs[[2]string{cmpElara, atrTrust}] = IntValue(1)
	e.attrs[[2]string{cmpElara, atrMood}] = StringValue("calm")
	e.visits[nodeEntry] = 1
	return e
}

// compiled returns the AST of source, compiled against testProject at the
// harbour, or fails the test.
func compiled(t *testing.T, source string, ctx Context) Node {
	t.Helper()
	n, err := Compile(source, ctx, testProject(t), boardHarbour)
	if err != nil {
		t.Fatalf("%q: %v", source, err)
	}
	return n
}

// TestEveryBuiltinEvaluates checks that every built-in has an evaluation in
// Go, and that nothing else has one, so that Check accepts no call that the
// evaluator cannot evaluate (PRD FR-EXPR-04): the conformance suite's
// TestBuiltinsCovered checks that each is called in a scenario.
//
// Verifies: FR-EXPR-04/AC1
func TestEveryBuiltinEvaluates(t *testing.T) {
	t.Parallel()
	if got, want := slices.Sorted(maps.Keys(evaluations)), slices.Sorted(maps.Keys(builtins)); !slices.Equal(got,
		want) {
		t.Errorf("evaluations are of %v; want the built-ins, %v", got, want)
	}
}

// TestCondition checks conditions of each kind of operand and operator,
// names read by their refs: the harbour's trust, which shadows the global
// one, an attribute, an enum read as a string, and visits.
func TestCondition(t *testing.T) {
	t.Parallel()
	for source, want := range map[string]bool{
		"trust == 7":                                      true,
		"global.trust == 2":                               true,
		"elara.trust < trust":                             true,
		`elara.mood == "calm" and met`:                    true,
		`name + "!" == "Ann!"`:                            true,
		"rate * 2 == 1":                                   true,
		"rate < 1 and not (rate > 1)":                     true,
		`"a" < "b" && "😀" > "\uFFFF"`:                     true,
		`visited(node("` + nodeEntry + `"))`:              true,
		`visits(node("` + nodeEntry + `")) == 1 or false`: true,
		"to_int(rate) == 0 && to_string(rate) == \"0.5\"": true,
		"min(1, 2.5) == 1.0 && max(3, 2) == 3":            true,
		"clamp(trust, 0, 5) == 5 && abs(-trust) == 7":     true,
	} {
		env := envOf()
		if got := Condition(compiled(t, source, ContextCondition), env); got != want || len(env.errors) > 0 {
			t.Errorf("Condition(%q) = %v, errors %v; want %v", source, got, env.errors, want)
		}
	}
}

// TestShortCircuit checks that && and || evaluate their right operand only
// when the left one does not decide the result, so that a runtime error of
// the right one is reported only then.
func TestShortCircuit(t *testing.T) {
	t.Parallel()
	for source, want := range map[string]struct {
		value  bool
		errors int
	}{
		"false && 1 / 0 == 0": {false, 0},
		"true || 1 / 0 == 0":  {true, 0},
		"true && 1 / 0 == 0":  {true, 1},
		"false || 1 / 0 == 1": {false, 1},
	} {
		env := envOf()
		got := Condition(compiled(t, source, ContextCondition), env)
		if got != want.value || len(env.errors) != want.errors {
			t.Errorf("Condition(%q) = %v, errors %v; want %v and %d errors", source, got, env.errors,
				want.value, want.errors)
		}
	}
}

// TestRuntimeErrorsGoOn checks that an operation that fails yields the zero
// value of its result's type, which the evaluation goes on with, and that
// each failure is reported (PRD §5.8.7).
func TestRuntimeErrorsGoOn(t *testing.T) {
	t.Parallel()
	env := envOf()
	Run(compiled(t, `trust = 1 / 0 + 5
global.trust = to_int("3abc") + 4294967297 * 4294967296
rate = rate / 0.0`, ContextScript).(*Script), env)
	want := map[string]Value{varBoardTrst: IntValue(5), varTrust: IntValue(0), varRate: FloatValue(0)}
	for id, v := range want {
		if env.vars[id] != v {
			t.Errorf("%s = %v; want %v", id, env.vars[id], v)
		}
	}
	if want := []semantics.RuntimeError{semantics.DivisionByZero, semantics.ConversionFailed,
		semantics.IntegerOverflow, semantics.DivisionByZero}; !slices.Equal(env.errors, want) {
		t.Errorf("errors %v; want %v", env.errors, want)
	}
}

// TestAssignments checks that an assignment's value is of its target's type:
// an int widened for a float, op= of the target's value and the
// assignment's, and an enum attribute set to one of its options.
func TestAssignments(t *testing.T) {
	t.Parallel()
	env := envOf()
	Run(compiled(t, `rate = 3
global.trust %= 2
trust *= -1
trust -= 1
elara.trust += 4
elara.mood = "angry"
name += " Lee"
rate /= 2`, ContextScript).(*Script), env)
	for id, want := range map[string]Value{
		varRate: FloatValue(1.5), varTrust: IntValue(0), varBoardTrst: IntValue(-8), varName: StringValue("Ann Lee"),
	} {
		if got := env.vars[id]; got != want {
			t.Errorf("%s = %#v; want %#v", id, got, want)
		}
	}
	if got := env.attrs[[2]string{cmpElara, atrTrust}]; got != IntValue(5) {
		t.Errorf("elara.trust = %#v; want 5", got)
	}
	if got := env.attrs[[2]string{cmpElara, atrMood}]; got != StringValue("angry") {
		t.Errorf("elara.mood = %#v; want angry", got)
	}
	if len(env.errors) > 0 {
		t.Errorf("errors %v; want none", env.errors)
	}
}

// TestSignals checks that a script reports its signals in order, each with
// its value, if it has one, evaluated where the signal is.
func TestSignals(t *testing.T) {
	t.Parallel()
	env := envOf()
	Run(compiled(t, `signal("lit"); trust = 3; signal("count", trust * 2); signal("ratio", rate)`,
		ContextScript).(*Script), env)
	if want := []string{"lit", "count=int:6", "ratio=float:0.5"}; !slices.Equal(env.signals, want) {
		t.Errorf("signals %v; want %v", env.signals, want)
	}
}

// chain returns a sum of n ones, an AST of 2n - 1 nodes that Check refuses
// beyond MaxNodes, built without Parse.
func chain(n int) Node {
	var sum Node = &Lit{Type: value.Int, Int: 1}
	for range n - 1 {
		sum = &Binary{Op: OpAdd, Left: sum, Right: &Lit{Type: value.Int, Int: 1}}
	}
	return sum
}

// TestStepBudget checks that an evaluation of more than MaxSteps steps,
// which no AST that Check accepts takes, reports StepBudgetExceeded and
// evaluates nothing more: a condition is false, and the statement that a
// script exceeds the budget in has no effect, nor have those after it.
func TestStepBudget(t *testing.T) {
	t.Parallel()
	// MaxSteps steps exactly: the comparison, the sum of MaxSteps - 3 nodes,
	// and the negation and its operand; and one step more: the comparison,
	// the sum of MaxSteps - 1 nodes, and its other operand.
	exact := &Binary{Op: OpGte, Left: chain(MaxSteps/2 - 1),
		Right: &Unary{Op: OpNeg, Operand: &Lit{Type: value.Int}}}
	env := envOf()
	if !Condition(exact, env) || len(env.errors) > 0 {
		t.Errorf("a condition of MaxSteps steps is false, or errs: %v", env.errors)
	}
	over := &Binary{Op: OpGte, Left: chain(MaxSteps / 2), Right: &Lit{Type: value.Int}}
	env = envOf()
	if Condition(over, env) || !slices.Equal(env.errors, []semantics.RuntimeError{StepBudgetExceeded}) {
		t.Errorf("a condition beyond MaxSteps steps is true, or reports %v", env.errors)
	}
	target := func(id string) *Var { return &Var{Ref: id} }
	env = envOf()
	Run(&Script{Statements: []Node{
		&Assign{Op: OpAssign, Target: target(varTrust), Value: &Lit{Type: value.Int, Int: 40}},
		&Assign{Op: OpAssign, Target: target(varBoardTrst), Value: chain(MaxSteps / 2)},
		&Assign{Op: OpAssign, Target: target(varScratch), Value: &Lit{Type: value.Int, Int: 9}},
	}}, env)
	if env.vars[varTrust] != IntValue(40) || env.vars[varBoardTrst] != IntValue(7) ||
		env.vars[varScratch] != IntValue(0) {
		t.Errorf("after a script beyond MaxSteps steps, trust is %v, the harbour's %v, scratch %v; want 40, 7, 0",
			env.vars[varTrust], env.vars[varBoardTrst], env.vars[varScratch])
	}
	if !slices.Equal(env.errors, []semantics.RuntimeError{StepBudgetExceeded}) {
		t.Errorf("errors %v; want %s", env.errors, StepBudgetExceeded)
	}
}

// TestNoPanic checks that the evaluator returns, without a panic, of ASTs
// that no check accepts: nil nodes, operands of the wrong types, and calls of
// another number of arguments or of an unknown function.
func TestNoPanic(t *testing.T) {
	t.Parallel()
	for _, n := range []Node{
		nil,
		&Binary{Op: OpAdd},
		&Unary{Op: OpNeg, Operand: &Lit{Type: value.String, Str: "x"}},
		&Call{Function: "min", Args: []Node{&Lit{Type: value.Int}}},
		&Call{Function: "visits", Args: []Node{&Lit{Type: value.Int}}},
		&Call{Function: "nothing"},
		&NodeRef{Ref: nodeEntry},
		&Script{Statements: []Node{nil, &Assign{Op: OpAssign, Target: &Lit{}}}},
		(*Lit)(nil),
		&Binary{Op: OpEq, Left: (*Lit)(nil), Right: (*Var)(nil)},
		&Unary{Op: OpNot, Operand: (*Attr)(nil)},
		&Call{Function: "abs", Args: []Node{(*Call)(nil)}},
		&Call{Function: "visits", Args: []Node{(*NodeRef)(nil)}},
	} {
		Condition(n, envOf())
	}
	Run(&Script{Statements: []Node{nil, &Assign{Op: OpAddAssign, Target: &Var{Ref: varTrust}}}}, envOf())
	Run(&Script{Statements: []Node{(*Assign)(nil), (*Call)(nil), &Assign{Op: OpAssign, Target: (*Var)(nil)},
		&Assign{Op: OpAssign, Target: (*Attr)(nil), Value: (*Binary)(nil)}}}, envOf())
	Run((*Script)(nil), envOf())
	Run(nil, envOf())
}

// TestCheckResolved checks that CheckResolved reads a name by its refs, as
// Decode returns it, not anew by its keys: a global variable whose key the
// board's variable has too is the global one, an attribute that cmp. named is
// the component's though a variable has its key, and a ref that names nothing
// at the board is refused.
func TestCheckResolved(t *testing.T) {
	t.Parallel()
	p := testProject(t)
	decoded := func(source string, ctx Context, board string) Node {
		t.Helper()
		data, err := Encode(compiled(t, source, ctx))
		if err != nil {
			t.Fatal(err)
		}
		n, err := Decode(data, ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	// global.trust, at the harbour, whose own trust a bare key would name.
	if err := CheckResolved(decoded("global.trust == 2", ContextCondition, boardHarbour), ContextCondition, p,
		boardHarbour); err != nil {
		t.Errorf("global.trust: %v", err)
	}
	n := decoded("global.trust == 2", ContextCondition, boardHarbour)
	if err := Check(n, ContextCondition, p, boardHarbour); err != nil {
		t.Fatal(err)
	}
	if got := n.(*Binary).Left.(*Var).Ref; got != varBoardTrst {
		t.Fatalf("Check of a decoded global.trust names %s, not the harbour's trust, which CheckResolved avoids",
			got)
	}
	for _, c := range []struct {
		source string
		ctx    Context
		board  string
		code   Code
	}{
		{"trust == 7", ContextCondition, boardElara, BoardVariableOutOfScope},
		{"trust == 7", ContextCondition, "", BoardVariableOutOfScope},
		{"elara.trust = 1", ContextScript, boardElara, ""},
		{"trust = 1", ContextScript, boardHarbour, ""},
	} {
		n := decoded(c.source, c.ctx, boardHarbour)
		if got := codeOf(CheckResolved(n, c.ctx, p, c.board)); got != c.code {
			t.Errorf("CheckResolved(%q) at %q: %q; want %q", c.source, c.board, got, c.code)
		}
	}
	for _, c := range []struct {
		n    Node
		code Code
	}{
		{&Binary{Op: OpEq, Left: &Var{Ref: testID(ids.Variable, 99)}, Right: &Lit{Type: value.Int}}, UnknownIdentifier},
		{&Binary{Op: OpEq, Left: &Attr{Ref: varTrust, Attribute: atrTrust}, Right: &Lit{Type: value.Int}},
			UnknownIdentifier},
		{&Binary{Op: OpEq, Left: &Attr{Ref: cmpElara, Attribute: atrMood + "0"}, Right: &Lit{Type: value.Int}},
			UnknownAttribute},
		{&Binary{Op: OpEq, Left: &Attr{Ref: cmpElara, Attribute: atrAge}, Right: &Lit{Type: value.Int}},
			AttributeNotRuntime},
		{&Binary{Op: OpEq, Left: &Var{Ref: varName}, Right: &Lit{Type: value.Int}}, TypeMismatch},
		{&Call{Function: "visited", Args: []Node{&NodeRef{Ref: nodeJumper}}}, NodeRefJumper},
	} {
		if got := codeOf(CheckResolved(c.n, ContextCondition, p, boardHarbour)); got != c.code {
			t.Errorf("CheckResolved(%s): %q; want %q", encoded(c.n), got, c.code)
		}
	}
}

// TestCheckResolvedCorpus checks that CheckResolved accepts the AST of every
// case of the checker's corpus that compiles, written as the published format
// holds it and read back, at the case's board: what the server publishes, a
// runtime's loader accepts.
func TestCheckResolvedCorpus(t *testing.T) {
	t.Parallel()
	p, cases, sources := readCompileCorpus(t)
	n := 0
	for i, c := range cases {
		if c.AST == nil {
			continue
		}
		ast, err := Compile(sources[i], c.Context, p, c.Board)
		if err != nil {
			t.Fatalf("%s: %v", c.Note, err)
		}
		data, err := Encode(ast)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode(data, c.Context)
		if err != nil {
			t.Fatalf("%s: %v", c.Note, err)
		}
		if err := CheckResolved(decoded, c.Context, p, c.Board); err != nil {
			t.Errorf("%s: CheckResolved: %v", c.Note, err)
		}
		n++
	}
	if n == 0 {
		t.Error("the corpus has no case that compiles")
	}
}

// operationCase is a case of the expression corpus's files of operations
// (schema/expr-corpus/README.md).
type operationCase struct {
	Note     string                 `json:"note"`
	Op       string                 `json:"op"`
	Args     []corpusValue          `json:"args"`
	Decimals *int                   `json:"decimals"`
	Result   corpusValue            `json:"result"`
	Error    semantics.RuntimeError `json:"error"`
}

// corpusValue is a typed value as the corpus writes it.
type corpusValue struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

// read returns the value that v writes, as its type says, a float read as
// to_float reads a string, as the corpus's README says a runner reads it.
func (v corpusValue) read() (Value, error) {
	switch v.Type {
	case value.Bool:
		var b bool
		err := json.Unmarshal(v.Value, &b)
		return BoolValue(b), err
	case value.Int:
		n, err := strconv.ParseInt(string(v.Value), 10, 64)
		return IntValue(n), err
	case value.Float:
		f, problem := semantics.ParseFloat(string(v.Value))
		if problem != "" {
			return Value{}, errors.New(string(problem))
		}
		return FloatValue(f), nil
	case value.String:
		var s string
		err := json.Unmarshal(v.Value, &s)
		return StringValue(s), err
	}
	return Value{}, fmt.Errorf("%q is no type", v.Type)
}

// operators are the AST's operators of the corpus's ops.
var operators = map[string]BinaryOp{
	"+": OpAdd, "-": OpSub, "*": OpMul, "/": OpDiv, "%": OpMod, "==": OpEq, "!=": OpNe, "<": OpLt, "<=": OpLte,
	">": OpGt, ">=": OpGte,
}

// TestEvaluatorCorpus runs every case of the expression corpus's files of
// operations through the evaluator, as the corpus's README says each
// evaluator does once it has one: each op is an AST of its operator, or a
// call of its built-in, of literals of the case's arguments, and its result
// and its runtime error are compared exactly, a float by its bits. A case of
// interpolate, which a text value reference is rather than an expression,
// package flow runs as it renders a text.
//
// Verifies: FR-EXPR-04/AC1
func TestEvaluatorCorpus(t *testing.T) {
	t.Parallel()
	ran := 0
	for _, name := range []string{"numbers.json", "strings.json", "formatting.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "expr-corpus", name))
		if err != nil {
			t.Fatal(err)
		}
		var file struct {
			Cases []operationCase `json:"cases"`
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&file); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, c := range file.Cases {
			if c.Op == "interpolate" {
				continue
			}
			where := name + ": " + c.Note
			args := make([]Node, len(c.Args))
			for i, a := range c.Args {
				v, err := a.read()
				if err != nil {
					t.Fatalf("%s: %v", where, err)
				}
				args[i] = &Lit{Type: v.Type, Bool: v.Bool, Int: v.Int, Float: v.Float, Str: v.Str}
			}
			want, err := c.Result.read()
			if err != nil {
				t.Fatalf("%s: %v", where, err)
			}
			var n Node
			switch op, ok := operators[c.Op]; {
			case c.Op == "-" && len(args) == 1:
				n = &Unary{Op: OpNeg, Operand: args[0]}
			case ok && len(args) == 2:
				n = &Binary{Op: op, Left: args[0], Right: args[1]}
			default:
				n = &Call{Function: c.Op, Args: args}
			}
			env := newTestEnv()
			e := &evaluator{env: env}
			got := e.eval(n)
			var wantErrors []semantics.RuntimeError
			if c.Error != "" {
				wantErrors = []semantics.RuntimeError{c.Error}
			}
			if got.Type != want.Type || got.Bool != want.Bool || got.Int != want.Int || got.Str != want.Str ||
				math.Float64bits(got.Float) != math.Float64bits(want.Float) || !slices.Equal(env.errors, wantErrors) {
				t.Errorf("%s: %s = %#v, %v; want %#v, %v", where, c.Op, got, env.errors, want, wantErrors)
			}
			ran++
		}
	}
	if ran == 0 {
		t.Error("the corpus has no case of an operation")
	}
}
