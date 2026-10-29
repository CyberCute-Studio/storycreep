package expr

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
)

// children returns n's children, in the order of the source.
func children(n Node) []Node {
	var out []Node
	eachChild(n, func(c Node, _ place) { out = append(out, c) })
	return out
}

// ranges returns the kind and the range of each node of n, as Walk gives
// them: n first, each node before its children.
func ranges(n Node) []string {
	var out []string
	Walk(n, func(n Node) { out = append(out, fmt.Sprintf("%s %d-%d", n.Kind(), n.Range().Start, n.Range().End)) })
	return out
}

// TestRanges checks where each node is in its source, in code points: from its
// first token to its last, with the parentheses around its operands but not
// those around itself, so that an error that names an operand can cover it as
// it is written, and a name's range is the name's alone, which a rename
// rewrites. It checks too that Walk gives every node of each kind that has
// children, each before its children, and those in the order of the source.
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

// TestLongSource checks the errors of long sources, which TestSourceCost
// holds to what they cost: of a qualified name of a million parts, read to its
// last part, over all of them, and of sources that Parse stops in early.
func TestLongSource(t *testing.T) {
	t.Parallel()
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
		if _, err := Parse(c.source, ContextCondition); fmt.Sprint(err) != fmt.Sprint(c.want) {
			t.Errorf("Parse(%.20q…) = %v; want %v", c.source, err, c.want)
		}
	}
}

// TestSourceCost checks that no source, however long, costs Parse more than
// its AST may hold (PRD §5.8.6), of every token and every form of the
// grammar, so that a change that makes any of them cost more fails here
// rather than waiting to be found: each source of some 2 MiB, which repeats a
// token or a form, or holds an error at its start, in its middle, or at its
// end, read in either context, allocates at most 256 KiB, more than an AST of
// MaxNodes nodes and its error take, and the values of the string literals
// with escapes that the parser reads, each allocated once, at its length,
// since a string's value without an escape is the source's characters, and a
// float literal is read from its first 800 digits. Each source also gives
// the error that it is meant to, or an AST, so that none stops before what it
// repeats. The test does not run in parallel, so that what other tests
// allocate is not counted, and takes the least of up to three runs, a second
// and a third only when one is over the budget, so that what the runtime
// allocates meanwhile is not counted either, while the test, which reads 90
// sources of 2 MiB in either context, takes no longer than one run of each.
func TestSourceCost(t *testing.T) {
	const size = 2 << 20
	bs := string(rune(92)) // a backslash
	maxString := func(char string) string { return `"` + strings.Repeat(char, MaxStringLength) + `"` }
	ref := `node("nd_01HQ8ZB1T4V7K2MC9XRPW5NDYF")`
	type codes struct{ condition, script Code }
	var (
		ast        = codes{"", InvalidStatement} // an expression's AST, which is no statement
		both       = func(c Code) codes { return codes{c, c} }
		tooLarge   = both(ASTTooLarge)
		unexpected = both(UnexpectedToken)
	)
	// Each source is prefix, unit as many times as size holds it beside
	// them, and suffix, of whose string literals each that has an escape has
	// a value of escaped bytes.
	shapes := []struct {
		name                 string
		prefix, unit, suffix string
		want                 codes
		escaped              int
	}{
		{"an identifier", "", "a", "", both(IdentifierTooLong), 0},
		{"names", "", "a ", "", unexpected, 0},
		{"a qualified name", "", "a.", "a", both(InvalidName), 0},
		{"a qualified name of spaced dots", "", "a . ", "a", both(InvalidName), 0},
		{"a global name", "global.", "a.", "a", both(InvalidName), 0},
		{"a cmp name", "cmp.", "a.", "a", both(InvalidName), 0},
		{"a name that ends in a reserved word", "", "a.", "null", both(ReservedWord), 0},
		{"reserved words", "", "null ", "", both(ReservedWord), 0},
		{"qualifiers", "", "global ", "", both(ReservedWord), 0},
		{"nested calls", "", "f(", "", both(ASTTooDeep), 0},
		{"a sum of calls", "f()", "+f()", "", tooLarge, 0},
		{"arguments", "f(", "1,", "1)", tooLarge, 0},
		{"arguments of names", "f(", "a,", "a)", tooLarge, 0},
		{"empty arguments", "f(", ",", "", unexpected, 0},
		{"nested node references", "", "node(", "", both(ASTTooDeep), 0},
		{"a sum of node references", ref, "+" + ref, "", tooLarge, 0},
		{"a node reference of a long string", `node("`, "a", `")`, both(StringTooLong), 0},
		{"a node reference of a long string of escapes", `node("`, bs + "n", `")`, both(StringTooLong), 0},
		{"node references of names", "", "node(a)+", "1", both(InvalidNodeRef), 0},
		{"a string", `"`, "a", `"`, both(StringTooLong), 0},
		{"a string without its closing quote", `"`, "a", "", both(UnterminatedString), 0},
		{"a string of escapes", `"`, bs + "n", `"`, both(StringTooLong), 0},
		{"a string of \\u escapes", `"`, bs + "u00e9", `"`, both(StringTooLong), 0},
		{"a string of a surrogate pair's escapes", `"`, bs + "uD83D" + bs + "uDE00", `"`, both(StringTooLong), 0},
		{"a string of é", `"`, "é", `"`, both(StringTooLong), 0},
		{"a string with a bad escape at its end", `"`, "a", bs + `q"`, both(InvalidEscape), 0},
		{"a string with a NUL at its end", `"`, "a", "\x00\"", both(InvalidCharacter), 0},
		{"a sum of strings", maxString("a"), "+" + maxString("a"), "", tooLarge, 0},
		{"a sum of strings of 😀", maxString("😀"), "+" + maxString("😀"), "", ast, 0},
		{"a sum of strings of escapes", maxString(bs + "n"), "+" + maxString(bs+"n"), "", ast, MaxStringLength},
		{"a sum of strings of \\u escapes", maxString(bs + "u00e9"), "+" + maxString(bs+"u00e9"), "", ast,
			2 * MaxStringLength},
		{"an integer", "", "1", "", both(IntegerOutOfRange), 0},
		{"an integer of leading zeros", "", "0", "1", ast, 0},
		{"a sum of integers", "1", "+1", "", tooLarge, 0},
		{"a float of many digits after its point", "1.", "1", "", ast, 0},
		{"a float of many zeros after its point", "0.", "0", "1", ast, 0},
		{"a float of many digits before its point", "", "1", ".5", both(FloatOutOfRange), 0},
		{"a float of many digits and an exponent that brings it back", "", "1", ".5e-2097150", ast, 0},
		{"a float of many leading zeros", "", "0", ".5", ast, 0},
		{"a float of many trailing zeros", "1.", "0", "", ast, 0},
		{"a float of many digits and an E", "1.", "1", "E5", ast, 0},
		{"a float of a long exponent", "1.5e", "1", "", both(FloatOutOfRange), 0},
		{"a float of a long exponent of zeros", "1.5e", "0", "1", ast, 0},
		{"a sum of floats", "1.5", "+1.5", "", tooLarge, 0},
		{"a number of letters", "1", "a", "", both(InvalidNumber), 0},
		{"a number of points", "1", ".", "", both(InvalidNumber), 0},
		{"a comment", "// ", "a", "", codes{UnexpectedEnd, ""}, 0},
		{"comments", "", "// a\n", "x", ast, 0},
		{"comments and a NUL", "", "// a\n", "// \x00", both(InvalidCharacter), 0},
		{"spaces", "", " ", "x", ast, 0},
		{"tabs", "", "\t", "x", ast, 0},
		{"line breaks", "", "\n", "x", ast, 0},
		{"CRs and LFs", "", "\r\n", "x", ast, 0},
		{"semicolons", "", ";", "", codes{UnexpectedToken, ""}, 0},
		{"semicolons and a call", "", ";", "f()", codes{UnexpectedToken, ""}, 0},
		{"blank lines between calls", "f()", "\n", "f()", codes{UnexpectedToken, ""}, 0},
		{"calls on lines", "", "f()\n", "", codes{UnexpectedToken, ASTTooLarge}, 0},
		{"negations", "", "-", "1", unexpected, 0},
		{"nots", "", "not ", "x", unexpected, 0},
		{"a sum of negations", "", "-1+", "1", tooLarge, 0},
		{"||", "1", "||1", "", tooLarge, 0},
		{"&&", "1", "&&1", "", tooLarge, 0},
		{"or", "1", " or 1", "", tooLarge, 0},
		{"and", "1", " and 1", "", tooLarge, 0},
		{"==", "1", "==1", "", tooLarge, 0},
		{"!=", "1", "!=1", "", tooLarge, 0},
		{"<", "1", "<1", "", tooLarge, 0},
		{"<=", "1", "<=1", "", tooLarge, 0},
		{">", "1", ">1", "", tooLarge, 0},
		{">=", "1", ">=1", "", tooLarge, 0},
		{"+", "1", "+1", "", tooLarge, 0},
		{"-", "1", "-1", "", tooLarge, 0},
		{"*", "1", "*1", "", tooLarge, 0},
		{"/", "1", "/1", "", tooLarge, 0},
		{"%", "1", "%1", "", tooLarge, 0},
		{"a sum of groups", "(1)", "+(1)", "", tooLarge, 0},
		{"a sum of nested groups", "", "((1))+", "1", tooLarge, 0},
		{"opening parentheses", "", "(", "", both(ASTTooDeep), 0},
		{"closing parentheses", "", ")", "", unexpected, 0},
		{"assignments", "", "x = 1;", "", codes{ConditionMustBePure, ASTTooLarge}, 0},
		{"assignments on lines", "", "x += 1\n", "", codes{ConditionMustBePure, ASTTooLarge}, 0},
		{"assignments to attributes", "", "a.b = 1\n", "", codes{ConditionMustBePure, ASTTooLarge}, 0},
		{"call statements", "", "f(x)\n", "", codes{UnexpectedToken, ASTTooLarge}, 0},
		{"an assignment of a sum", "x = ", "1+", "1", codes{ConditionMustBePure, ASTTooLarge}, 0},
		{"an error at the start", "@", "a ", "", both(InvalidCharacter), 0},
		{"an error in the middle", strings.Repeat(" ", size/2) + "@", " ", "", both(InvalidCharacter), 0},
		{"an error at the end", "", " ", "@", both(InvalidCharacter), 0},
		{"characters that are not ASCII", "", "é", "", both(InvalidCharacter), 0},
		{"bytes that are not UTF-8", "", "\xff", "", both(InvalidCharacter), 0},
		{"NULs", "", "\x00", "", both(InvalidCharacter), 0},
	}
	for _, s := range shapes {
		n := (size - len(s.prefix) - len(s.suffix)) / len(s.unit)
		source := s.prefix + strings.Repeat(s.unit, n) + s.suffix
		budget := uint64(256<<10 + s.escaped*(1+n))
		for ctx, want := range map[Context]Code{ContextCondition: s.want.condition, ContextScript: s.want.script} {
			least := ^uint64(0)
			var err error
			for range 3 {
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				_, err = Parse(source, ctx)
				runtime.ReadMemStats(&after)
				if least = min(least, after.TotalAlloc-before.TotalAlloc); least <= budget {
					break
				}
			}
			var got Code
			if e, ok := err.(*Error); ok {
				got = e.Code
			}
			if err != nil && got == "" || got != want {
				t.Errorf("Parse of %s as a %s = %v; want the code %q", s.name, ctx, err, want)
			}
			if least > budget {
				t.Errorf("Parse of %s, %d bytes, as a %s allocated %d bytes; want at most %d", s.name, len(source), ctx,
					least, budget)
			}
		}
	}
}

// TestLineBreakEnds checks that a line break that ends a statement ends it
// before the lexer reads anything after it, the token after it however long,
// and that the lexer reads the line breaks, the comments, and the white space
// after it as white space of the token after it; and that the lexer counts
// the parentheses that are open by the tokens that it has read, whatever the
// parser has read of them, so that a line break within them is white space.
func TestLineBreakEnds(t *testing.T) {
	t.Parallel()
	l := &lexer{src: "x // a\n\n  \"" + strings.Repeat("a", 1<<20), ctx: ContextScript}
	if k := l.next().kind; k != tIdent {
		t.Fatalf("the first token is of the kind %d; want an identifier", k)
	}
	if t2 := l.next(); t2.kind != tNewline || t2.Range != (Range{6, 7}) || l.i != 7 {
		t.Errorf("the second token is of the kind %d at %v, with the lexer at byte %d; want a line break at 6 to 7, "+
			"with the lexer at byte 7, right after it", t2.kind, t2.Range, l.i)
	}
	if t3 := l.next(); t3.kind != tError || t3.err.Code != UnterminatedString || t3.Start != 10 {
		t.Errorf("the third token is of the kind %d at %v; want the unterminated string at 10", t3.kind, t3.Range)
	}
	l = &lexer{src: "f((x)\ny)\nz", ctx: ContextScript}
	var kinds []tokenKind
	for len(kinds) == 0 || kinds[len(kinds)-1] != tEOF {
		kinds = append(kinds, l.next().kind)
	}
	want := []tokenKind{tIdent, tLParen, tLParen, tIdent, tRParen, tIdent, tRParen, tNewline, tIdent, tEOF}
	if !slices.Equal(kinds, want) {
		t.Errorf("the tokens of f((x), a line break, y), a line break, and z are of the kinds %v; want %v", kinds, want)
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
