package expr

import (
	"math"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/semantics"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// TestEncodeCanonical checks the AST's canonical form, byte by byte: the ASTs
// of the example in PRD §6.5, once resolved, as that example writes them,
// without white space; each kind's keys in order; each number and string as
// §6.2 writes it; and one form for each AST however its source is written, of
// whichever operators' spellings, white space, comments, and parentheses.
//
// Verifies: FR-EXPR-03/AC3
func TestEncodeCanonical(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		n    Node
		want string
	}{
		{&Binary{Op: OpEq, Left: &Var{Name: "met_elara", Ref: "var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"},
			Right: &Lit{Type: value.Bool, Bool: true}},
			`{"kind":"binary","left":{"kind":"var","name":"met_elara","ref":"var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"},` +
				`"op":"eq","right":{"kind":"lit","type":"bool","value":true}}`},
		{&Binary{Op: OpGte, Left: &Attr{Name: "elara.trust", Ref: "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF",
			Attribute: "atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF"}, Right: &Lit{Type: value.Int, Int: 3}},
			`{"kind":"binary","left":{"attribute":"atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF","kind":"attr",` +
				`"name":"elara.trust","ref":"cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF"},"op":"gte",` +
				`"right":{"kind":"lit","type":"int","value":3}}`},
		{&Script{Statements: []Node{&Assign{Op: OpAddAssign,
			Target: &Var{Name: "harbour_rumours", Ref: "var_01HQ8Z5C2K8W1D5J9NTQZR4YBE"},
			Value:  &Lit{Type: value.Int, Int: 1}}}},
			`{"kind":"script","statements":[{"kind":"assign","op":"+=","target":{"kind":"var",` +
				`"name":"harbour_rumours","ref":"var_01HQ8Z5C2K8W1D5J9NTQZR4YBE"},` +
				`"value":{"kind":"lit","type":"int","value":1}}]}`},
		{&Unary{Op: OpNeg, Operand: &Lit{Type: value.Float, Float: 0.5}},
			`{"kind":"unary","op":"neg","operand":{"kind":"lit","type":"float","value":0.5}}`},
		{&Call{Function: "visited", Args: []Node{&NodeRef{Ref: "nd_01HQ8ZB1T4V7K2MC9XRPW5NDYF"}}},
			`{"args":[{"kind":"node_ref","ref":"nd_01HQ8ZB1T4V7K2MC9XRPW5NDYF"}],"function":"visited","kind":"call"}`},
		{&Call{Function: "visited"}, `{"args":[],"function":"visited","kind":"call"}`},
		{&Script{}, `{"kind":"script","statements":[]}`},
		{&Attr{Name: "elara.trust"}, `{"kind":"attr","name":"elara.trust"}`},
	} {
		if got := encoded(c.n); got != c.want {
			t.Errorf("Encode(%#v) = %s; want %s", c.n, got, c.want)
		}
	}

	for _, c := range []struct {
		f    float64
		want string
	}{
		{2, "2"}, {0.1, "0.1"}, {-1.5, "-1.5"}, {1e9, "1000000000"}, {123456789.125, "123456789.125"},
		{1e-6, "0.000001"}, {1e-7, "1e-7"}, {-2.5e-10, "-2.5e-10"}, {9.999999999999999e20, "999999999999999900000"},
		{1e21, "1e+21"}, {1.7976931348623157e308, "1.7976931348623157e+308"}, {5e-324, "5e-324"},
		{math.Copysign(0, -1), "0"},
	} {
		want := `{"kind":"lit","type":"float","value":` + c.want + `}`
		if got := encoded(&Lit{Type: value.Float, Float: c.f}); got != want {
			t.Errorf("Encode of the float %v = %s; want %s", c.f, got, want)
		}
	}
	for _, c := range []struct {
		n    int64
		want string
	}{{semantics.MaxInt, "9007199254740991"}, {-semantics.MaxInt, "-9007199254740991"}, {0, "0"}} {
		want := `{"kind":"lit","type":"int","value":` + c.want + `}`
		if got := encoded(&Lit{Type: value.Int, Int: c.n}); got != want {
			t.Errorf("Encode of the int %d = %s; want %s", c.n, got, want)
		}
	}
	for _, c := range []struct{ s, want string }{
		{"<a & b/>", `"<a & b/>"`},
		{`"\`, `"\"\\"`},
		{"\b\f\n\r\t\x01\x1f\x7f", `"\b\f\n\r\t\u0001\u001f` + "\x7f" + `"`},
		{"\u2028\u2029", `"\u2028\u2029"`},
		{"é😀", `"é😀"`},
	} {
		want := `{"kind":"lit","type":"string","value":` + c.want + `}`
		if got := encoded(&Lit{Type: value.String, Str: c.s}); got != want {
			t.Errorf("Encode of the string %q = %s; want %s", c.s, got, want)
		}
	}

	for _, c := range []struct {
		ctx     Context
		sources []string
	}{
		{ContextCondition, []string{"a and not b", "a && !b", "(a)&&(!b)", "a // first\n  && ! b", "((a and (not b)))"}},
		{ContextCondition, []string{"x == 2.0", "x == 2.00", "x==20.0e-1", "(x) == (2.0)"}},
		{ContextCondition, []string{"elara.trust", "elara . trust", "(elara.trust)"}},
		{ContextCondition, []string{`visited(node("nd_01HQ8ZB1T4V7K2MC9XRPW5NDYF"))`,
			`visited (node ("ND_01hq8zb1t4v7k2mc9xrpw5ndyf"))`}},
		{ContextCondition, []string{`s == "é"`, `s == "\u00e9"`, `s == "\u00E9"`}},
		{ContextScript, []string{"x += 1", "x+=1;", "\n x += 1 // why\n", "x += 01", ";;x += (1);"}},
	} {
		var first string
		for i, source := range c.sources {
			n, err := Parse(source, c.ctx)
			if err != nil {
				t.Errorf("Parse(%q): %v", source, err)
				continue
			}
			got := encoded(n)
			if again := encoded(n); again != got {
				t.Errorf("Encode of %q is %s, then %s", source, got, again)
			}
			if i == 0 {
				first = got
			} else if got != first {
				t.Errorf("Encode of %q = %s; want %s, as of %q", source, got, first, c.sources[0])
			}
		}
	}
}

// TestEncodeRefuses checks that Encode refuses a node that is not one of an
// AST, rather than write what no AST is: of what is no value, operator, or
// string of the language, without the name, the node reference, or the
// function that it needs, or of a kind that its place cannot be of.
func TestEncodeRefuses(t *testing.T) {
	t.Parallel()
	lit := &Lit{Type: value.Int, Int: 1}
	x := &Var{Name: "x"}
	assign := &Assign{Op: OpAssign, Target: x, Value: lit}
	script := func(statements ...Node) *Script { return &Script{Statements: statements} }
	for _, n := range []Node{
		nil,
		(*Lit)(nil),
		&Binary{Op: OpAdd, Left: lit},
		&Binary{Op: "pow", Left: lit, Right: lit},
		&Unary{Op: "plus", Operand: lit},
		&Lit{Type: value.Int, Int: semantics.MaxInt + 1},
		&Lit{Type: value.Float, Float: math.Inf(1)},
		&Lit{Type: value.Float, Float: math.NaN()},
		&Lit{Type: value.String, Str: "\xff"},
		&Lit{Type: value.String, Str: "a\x00b"},
		&Lit{Type: value.Enum},
		&Var{Name: "x\xff"},
		&Var{Name: "x\x00"},
		&Var{},
		&Attr{},
		&NodeRef{},
		&Call{},
		&Attr{Name: "a.b", Ref: "cmp_\x00"},
		&Var{Name: "x", Ref: "var_\xff"},
		&Attr{Name: "a.b", Attribute: "atr_\xff"},
		&NodeRef{Ref: "nd_\xff"},
		&Call{Function: "f\xff"},
		script(&Assign{Op: OpAssign, Target: lit, Value: lit}),
		script(&Assign{Op: OpAssign, Target: &Call{Function: "f"}, Value: lit}),
		script(&Assign{Op: "^=", Target: x, Value: lit}),
		script(&Assign{Op: OpAssign, Target: x, Value: assign}),
		script(lit),
		script(x),
		script(script()),
		assign,
		&Binary{Op: OpAdd, Left: assign, Right: lit},
		&Call{Function: "f", Args: []Node{nil}},
		&Call{Function: "f", Args: []Node{script()}},
		&Unary{Op: OpNot, Operand: script(assign)},
	} {
		if b, err := Encode(n); err == nil {
			t.Errorf("Encode(%#v) = %s; want an error", n, b)
		}
	}
}

// TestEncodeDeep checks that Encode writes an AST far deeper than the stack
// that it is given would hold were it to recurse once per level, as a sum of
// 100,001 ones is, so that no AST that a program builds can exhaust the stack.
// It does not run in parallel, since the stack's limit is the program's.
func TestEncodeDeep(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(1 << 20))
	var n Node = &Lit{Type: value.Int, Int: 1}
	for range 100_000 {
		n = &Binary{Op: OpAdd, Left: n, Right: &Lit{Type: value.Int, Int: 1}}
	}
	b, err := Encode(n)
	one := `{"kind":"lit","type":"int","value":1}`
	want := strings.Repeat(`{"kind":"binary","left":`, 100_000) + one +
		strings.Repeat(`,"op":"add","right":`+one+"}", 100_000)
	if err != nil || string(b) != want {
		t.Errorf("Encode of a sum of 100,001 ones = %.80s…, %v; want %.80s…", b, err, want)
	}
}
