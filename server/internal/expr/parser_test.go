package expr

import (
	"errors"
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

// TestNotUTF8 checks, of a byte that is not UTF-8, which the corpus, a file of
// UTF-8, cannot hold, that it is InvalidCharacter wherever it is, one
// character long, as a NUL is, right after a backslash too, but for one in
// place of a \u's digits, which it ends, as a NUL does, so that the \u is
// InvalidEscape first.
func TestNotUTF8(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		source string
		want   *Error
	}{
		{"x == \xff", &Error{Code: InvalidCharacter, Range: Range{5, 6}}},
		{"\"é\xffb\"", &Error{Code: InvalidCharacter, Range: Range{2, 3}}},
		{"x // \xff", &Error{Code: InvalidCharacter, Range: Range{5, 6}}},
		{"\"\\\xff\"", &Error{Code: InvalidCharacter, Range: Range{2, 3}}},
		{"\"\\u1\xff\"", &Error{Code: InvalidEscape, Range: Range{1, 4}}},
	} {
		_, err := Parse(c.source, ContextCondition)
		if fmt.Sprint(err) != fmt.Sprint(c.want) {
			t.Errorf("Parse(%q) = %v; want %v", c.source, err, c.want)
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
// parenthesis that opens the first level too many, however deep it goes, a
// node reference's among them.
func TestDeepSource(t *testing.T) {
	t.Parallel()
	_, err := Parse(strings.Repeat("(", 1_000_000), ContextCondition)
	want := &Error{Code: ASTTooDeep, Range: Range{MaxDepth, MaxDepth + 1}}
	if fmt.Sprint(err) != fmt.Sprint(want) {
		t.Errorf("a million parentheses = %v; want %v", err, want)
	}
	_, err = Parse(strings.Repeat("node(", 1_000), ContextCondition)
	want = &Error{Code: ASTTooDeep, Range: Range{5*MaxDepth + 4, 5*MaxDepth + 5}}
	if fmt.Sprint(err) != fmt.Sprint(want) {
		t.Errorf("a thousand node( = %v; want %v", err, want)
	}
}

// TestNodeCount checks that an AST of MaxNodes nodes is read, and that the
// token that makes one more is ASTTooLarge, at it, whichever node it makes, a
// script's own node counted, however long the source goes on after it.
func TestNodeCount(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		ctx    Context
		source string
		want   *Error
	}{
		// 256 ones and 255 +, and a not: 512 nodes.
		{ContextCondition, "not (1" + strings.Repeat("+1", 255) + ")", nil},
		// The 257th one, at 512, is the 513th node.
		{ContextCondition, "1" + strings.Repeat("+1", 100_000), &Error{Code: ASTTooLarge, Range: Range{512, 513}}},
		// A call and its 511 arguments: 512 nodes.
		{ContextCondition, "f(" + strings.Repeat("1,", 510) + "1)", nil},
		// The call's 512th argument, at 1024, is the 513th node.
		{ContextCondition, "f(" + strings.Repeat("1,", 511) + "1)", &Error{Code: ASTTooLarge, Range: Range{1024, 1025}}},
		// The script, and 170 statements of three nodes each: 511 nodes.
		{ContextScript, strings.Repeat("x = 1\n", 170), nil},
		// The = of the 171st statement, at 1022, is the 513th node.
		{ContextScript, strings.Repeat("x = 1\n", 1_000), &Error{Code: ASTTooLarge, Range: Range{1022, 1023}}},
		// The script, a call, and 510 ones are 512 nodes, so that the - after
		// them, at 1022, is the 513th.
		{ContextScript, "f(" + strings.Repeat("1,", 510) + "-1)", &Error{Code: ASTTooLarge, Range: Range{1022, 1023}}},
	} {
		n, err := Parse(c.source, c.ctx)
		if c.want == nil {
			if err != nil {
				t.Errorf("Parse(%.20q…, %s): %v", c.source, c.ctx, err)
			}
			continue
		}
		if fmt.Sprint(err) != fmt.Sprint(c.want) {
			t.Errorf("Parse(%.20q…, %s) = %v, %v; want %v", c.source, c.ctx, encoded(n), err, c.want)
		}
	}
}

// TestLongSource checks that a long source costs no more than what Parse
// keeps of it: one that Parse stops in early no more than what it read of it,
// since the lexer reads tokens as the parser asks for them, rather than all of
// them first, and a qualified name of a million parts no more than one of
// three, since the parser counts a name's parts rather than keeping them. It
// does not run in parallel, so that what other tests allocate is not counted.
func TestLongSource(t *testing.T) {
	name := strings.Repeat("a.", 1<<20)
	for _, c := range []struct {
		source string
		want   *Error
	}{
		{") " + strings.Repeat("a ", 1<<20), &Error{Code: UnexpectedToken, Range: Range{0, 1}}},
		{strings.Repeat("(", 2<<20), &Error{Code: ASTTooDeep, Range: Range{MaxDepth, MaxDepth + 1}}},
		{"1" + strings.Repeat("+1", 1<<20), &Error{Code: ASTTooLarge, Range: Range{512, 513}}},
		{name + "a", &Error{Code: InvalidName, Range: Range{0, len(name) + 1}}},
		{"global." + name + "a", &Error{Code: InvalidName, Range: Range{0, len(name) + 8}}},
		{name + "null", &Error{Code: ReservedWord, Range: Range{len(name), len(name) + 4}}},
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

// TestLineBreakEnds checks that a line break that ends a statement ends it
// before the lexer reads the token after it, however long that token is.
func TestLineBreakEnds(t *testing.T) {
	t.Parallel()
	l := &lexer{src: "x // a\n\n  \"" + strings.Repeat("a", 1<<20), ctx: ContextScript}
	if k := l.next().kind; k != tIdent {
		t.Fatalf("the first token is of the kind %d; want an identifier", k)
	}
	if t2 := l.next(); t2.kind != tNewline || t2.Range != (Range{6, 7}) || l.i != 10 {
		t.Errorf("the second token is of the kind %d at %v, with the lexer at byte %d; want a line break at 6 to 7, "+
			"with the lexer at byte 10, before the string", t2.kind, t2.Range, l.i)
	}
	if k := l.next().kind; k != tError {
		t.Errorf("the third token is of the kind %d; want the unterminated string", k)
	}
}

// TestUnknownContext checks that Parse refuses a context that is none with
// ErrContext, which is no error of the source.
func TestUnknownContext(t *testing.T) {
	t.Parallel()
	n, err := Parse("x", "text")
	var e *Error
	if n != nil || !errors.Is(err, ErrContext) || errors.As(err, &e) {
		t.Errorf("Parse of the context text = %v, %v; want ErrContext alone", n, err)
	}
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
