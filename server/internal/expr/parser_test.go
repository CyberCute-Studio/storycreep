package expr

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
)

// children returns n's children, in the order of the source.
func children(n Node) []Node {
	switch n := n.(type) {
	case *Unary:
		return []Node{n.Operand}
	case *Binary:
		return []Node{n.Left, n.Right}
	case *Call:
		return n.Args
	case *Assign:
		return []Node{n.Target, n.Value}
	case *Script:
		return n.Statements
	}
	return nil
}

// ranges returns the kind and the range of each node of n, n first, each
// node before its children.
func ranges(n Node) []string {
	out := []string{fmt.Sprintf("%s %d-%d", n.Kind(), n.Range().Start, n.Range().End)}
	for _, c := range children(n) {
		out = append(out, ranges(c)...)
	}
	return out
}

// TestRanges checks where each node is in its source, in code points: from its
// first token to its last, with the parentheses around its operands but not
// those around itself, so that an error that names an operand can cover it as
// it is written, and a name's range is the name's alone, which a rename
// rewrites.
func TestRanges(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		ctx    Context
		source string
		want   []string
	}{
		{ContextCondition, "(a + b) * -c",
			[]string{"binary 0-12", "binary 1-6", "var 1-2", "var 5-6", "unary 10-12", "var 11-12"}},
		{ContextCondition, "elara.trust >= cmp.mira.trust",
			[]string{"binary 0-29", "attr 0-11", "attr 15-29"}},
		{ContextCondition, `"é" == f(x)`, []string{"binary 0-11", "lit 0-3", "call 7-11", "var 9-10"}},
		{ContextCondition, "(x)", []string{"var 1-2"}},
		{ContextCondition, `visited(node("nd_01HQ8ZB1T4V7K2MC9XRPW5NDYF"))`,
			[]string{"call 0-46", "node_ref 8-45"}},
		{ContextCondition, "global . gold", []string{"var 0-13"}},
		{ContextScript, "x += 1\n signal(\"a\")",
			[]string{"script 0-19", "assign 0-6", "var 0-1", "lit 5-6", "call 8-19", "lit 15-18"}},
		{ContextScript, "  // none\n", []string{"script 0-10"}},
	} {
		n, err := Parse(c.source, c.ctx)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.source, err)
			continue
		}
		if got := ranges(n); strings.Join(got, ", ") != strings.Join(c.want, ", ") {
			t.Errorf("Parse(%q) has the ranges %v; want %v", c.source, got, c.want)
		}
	}
}

// TestNames checks the parts of the names that Parse reads, which name
// resolution resolves.
func TestNames(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		source string
		want   Node
	}{
		{"met_elara", &Var{Name: "met_elara", Key: "met_elara"}},
		{"global.met_elara", &Var{Name: "global.met_elara", Scope: ScopeGlobal, Key: "met_elara"}},
		{"board.met_elara", &Var{Name: "board.met_elara", Scope: ScopeBoard, Key: "met_elara"}},
		{"elara.trust", &Attr{Name: "elara.trust", ComponentKey: "elara", AttributeKey: "trust"}},
		{"cmp.elara.trust", &Attr{Name: "cmp.elara.trust", Explicit: true, ComponentKey: "elara",
			AttributeKey: "trust"}},
	} {
		n, err := Parse(c.source, ContextCondition)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.source, err)
			continue
		}
		switch n := n.(type) {
		case *Var:
			n.Src = Range{}
		case *Attr:
			n.Src = Range{}
		}
		if fmt.Sprintf("%#v", n) != fmt.Sprintf("%#v", c.want) {
			t.Errorf("Parse(%q) = %#v; want %#v", c.source, n, c.want)
		}
	}
}

// TestReservedWords checks that no reserved word of §5.8.1 is read as a name:
// each is ReservedWord after a dot, and alone is no variable.
func TestReservedWords(t *testing.T) {
	t.Parallel()
	for _, w := range ident.ReservedWords() {
		source := "x." + w
		_, err := Parse(source, ContextCondition)
		want := &Error{Code: ReservedWord, Range: Range{2, len(source)}}
		if fmt.Sprint(err) != fmt.Sprint(want) {
			t.Errorf("Parse(%q) = %v; want %v", source, err, want)
		}
		if n, _ := Parse(w, ContextCondition); n != nil && n.Kind() == KindVar {
			t.Errorf("Parse(%q) is a variable", w)
		}
	}
}

// TestFirstError checks that Parse reports the first error that it meets from
// left to right, though a later token cannot be read either.
func TestFirstError(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		ctx    Context
		source string
		want   *Error
	}{
		{ContextCondition, `x + + "\q"`, &Error{Code: UnexpectedToken, Range: Range{4, 5}}},
		{ContextCondition, `"\q" + +`, &Error{Code: InvalidEscape, Range: Range{1, 3}}},
		{ContextCondition, "a.b.c.null", &Error{Code: ReservedWord, Range: Range{6, 10}}},
		{ContextCondition, `f(1 +, "\q")`, &Error{Code: UnexpectedToken, Range: Range{5, 6}}},
		{ContextCondition, `x == "a` + "\x00", &Error{Code: InvalidCharacter, Range: Range{7, 8}}},
		// The line break after + is white space, so that the statement is
		// x = 1 + y, which the second = cannot follow.
		{ContextScript, "x = 1 +\ny = @", &Error{Code: UnexpectedToken, Range: Range{10, 11}}},
		{ContextScript, `x = node("bd_01HQ8ZAX6P3R9T1K7MWVND2CQJ") @`,
			&Error{Code: InvalidNodeRef, Range: Range{9, 40}}},
	} {
		_, err := Parse(c.source, c.ctx)
		if fmt.Sprint(err) != fmt.Sprint(c.want) {
			t.Errorf("Parse(%q) = %v; want %v", c.source, err, c.want)
		}
	}
}

// TestNotUTF8 checks that a byte that is not UTF-8 is InvalidCharacter
// wherever it is, one character long, as a NUL is, right after a backslash
// too, which the corpus, a file of UTF-8, cannot hold.
func TestNotUTF8(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		source string
		want   Range
	}{
		{"x == \xff", Range{5, 6}},
		{"\"é\xffb\"", Range{2, 3}},
		{"x // \xff", Range{5, 6}},
		{"\"\\\xff\"", Range{2, 3}},
	} {
		_, err := Parse(c.source, ContextCondition)
		want := &Error{Code: InvalidCharacter, Range: c.want}
		if fmt.Sprint(err) != fmt.Sprint(want) {
			t.Errorf("Parse(%q) = %v; want %v", c.source, err, want)
		}
	}
}

// TestStringLength checks that a string literal's length is its value's, in
// code points: an escape is the one character that it stands for.
func TestStringLength(t *testing.T) {
	t.Parallel()
	for _, n := range []int{MaxStringLength, MaxStringLength + 1} {
		source := `"` + strings.Repeat(`\n`, n) + `"`
		got, err := Parse(source, ContextCondition)
		if n <= MaxStringLength {
			if lit, ok := got.(*Lit); err != nil || !ok || lit.Str != strings.Repeat("\n", n) {
				t.Errorf("a string literal of %d escapes = %v, %v; want its value", n, encoded(got), err)
			}
			continue
		}
		want := &Error{Code: StringTooLong, Range: Range{0, len(source)}}
		if fmt.Sprint(err) != fmt.Sprint(want) {
			t.Errorf("a string literal of %d escapes = %v; want %v", n, err, want)
		}
	}
}

// TestDeepSource checks that a source nested past MaxDepth stops at the
// parenthesis that opens the first level too many, however deep it goes, and
// that a long run of binary operators, which nest without parentheses, is read
// without recursing per operator.
func TestDeepSource(t *testing.T) {
	t.Parallel()
	_, err := Parse(strings.Repeat("(", 1_000_000), ContextCondition)
	want := &Error{Code: ASTTooDeep, Range: Range{MaxDepth, MaxDepth + 1}}
	if fmt.Sprint(err) != fmt.Sprint(want) {
		t.Errorf("a million parentheses = %v; want %v", err, want)
	}
	source := "1" + strings.Repeat(" + 1", 100_000)
	n, err := Parse(source, ContextCondition)
	if err != nil || n.Kind() != KindBinary || n.Range() != (Range{0, len(source)}) {
		t.Errorf("a sum of 100,001 ones = %v, %v; want a binary over the whole source", n, err)
	}
}

// TestEarlyError checks that a source that Parse stops in early costs no more
// than what it read of it, however long the rest of it is, since the lexer
// reads tokens as the parser asks for them, rather than all of them first. It
// does not run in parallel, so that what other tests allocate is not counted.
func TestEarlyError(t *testing.T) {
	for _, c := range []struct {
		source string
		want   *Error
	}{
		{") " + strings.Repeat("a ", 1<<20), &Error{Code: UnexpectedToken, Range: Range{0, 1}}},
		{strings.Repeat("(", 2<<20), &Error{Code: ASTTooDeep, Range: Range{MaxDepth, MaxDepth + 1}}},
	} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := Parse(c.source, ContextCondition)
		runtime.ReadMemStats(&after)
		if fmt.Sprint(err) != fmt.Sprint(c.want) {
			t.Errorf("Parse(%.20q…) = %v; want %v", c.source, err, c.want)
		}
		if n := after.TotalAlloc - before.TotalAlloc; n > 64<<10 {
			t.Errorf("Parse(%.20q…) allocated %d bytes, of a source of %d; want at most 64 KiB", c.source, n,
				len(c.source))
		}
	}
}

// TestUnknownContext checks that Parse panics on a context that is none.
func TestUnknownContext(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("Parse of the context text did not panic")
		}
	}()
	_, _ = Parse("x", "text")
}

// TestConditionMustBePure checks that every assignment operator in a condition
// is refused with condition_must_be_pure at the operator, wherever the
// assignment is, and that the same assignment is a statement of a script.
//
// Verifies: FR-EXPR-02/AC3
func TestConditionMustBePure(t *testing.T) {
	t.Parallel()
	for op := range strings.SplitSeq("= += -= *= /= %=", " ") {
		for _, form := range []string{"x %s 1", "met_elara and (x %s 1)", "f(x %s 1)", "a == b %s c",
			"elara.trust %s 3"} {
			source := fmt.Sprintf(form, op)
			at := strings.LastIndex(source, op)
			want := &Error{Code: ConditionMustBePure, Range: Range{at, at + len(op)}}
			if _, err := Parse(source, ContextCondition); fmt.Sprint(err) != fmt.Sprint(want) {
				t.Errorf("Parse(%q) as a condition = %v; want %v", source, err, want)
			}
		}
		source := "x " + op + " 1"
		n, err := Parse(source, ContextScript)
		if s, ok := n.(*Script); err != nil || !ok || len(s.Statements) != 1 ||
			s.Statements[0].(*Assign).Op != AssignOp(op) {
			t.Errorf("Parse(%q) as a script = %v, %v; want one assignment", source, encoded(n), err)
		}
	}
}
