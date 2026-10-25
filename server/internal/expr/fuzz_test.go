package expr

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"unicode/utf8"
)

// FuzzParse checks Parse on any source, in either context, from the corpus's
// sources on: it never panics; its error is one of its codes, over characters
// of the source; and its AST encodes, the same bytes each time, with every
// node's range within its parent's, after its elder sibling's, and over the
// node as it is written, so that the characters of the range, read as a
// condition, are the same node, but for an assignment's and a script's, which
// no condition is.
func FuzzParse(f *testing.F) {
	_, sources := readSyntaxCorpus(f)
	for _, s := range sources {
		f.Add(s)
	}
	f.Add("x = a\n and b // c\r\n(signal(\"\\uD83D\\uDE00\"))")
	f.Add("-(a) + (-(b)) * f((c), -(d))")
	f.Fuzz(func(t *testing.T, source string) {
		// offsets are the byte offsets of the source's characters, as Parse
		// counts them, a byte that is not UTF-8 one, and of its end.
		var offsets []int
		for i := range source {
			offsets = append(offsets, i)
		}
		offsets = append(offsets, len(source))
		length := len(offsets) - 1
		for _, ctx := range []Context{ContextCondition, ContextScript} {
			n, err := Parse(source, ctx)
			if err != nil {
				var e *Error
				if !errors.As(err, &e) || !slices.Contains(syntaxCodes, e.Code) || e.Start < 0 || e.Start > e.End ||
					e.End > length {
					t.Fatalf("Parse(%q, %s) = %v; want an error of a code, within the source's %d characters",
						source, ctx, err, length)
				}
				continue
			}
			b, err := Encode(n)
			if err != nil {
				t.Fatalf("Encode(Parse(%q, %s)): %v", source, ctx, err)
			}
			if again, _ := Parse(source, ctx); encoded(again) != string(b) {
				t.Fatalf("Parse(%q, %s) encodes as %s, then %s", source, ctx, b, encoded(again))
			}
			// check checks n, whose range is to be within the range within,
			// and its children.
			var check func(n Node, within Range)
			check = func(n Node, within Range) {
				r := n.Range()
				if r.Start < within.Start || r.Start > r.End || r.End > within.End {
					t.Fatalf("Parse(%q, %s) has a %s at %v, beyond %v, of its parent after its elder sibling, or "+
						"of the source", source, ctx, n.Kind(), r, within)
				}
				if k := n.Kind(); k != KindAssign && k != KindScript {
					written := source[offsets[r.Start]:offsets[r.End]]
					if again, err := Parse(written, ContextCondition); err != nil || encoded(again) != encoded(n) {
						t.Fatalf("Parse(%q, %s) has a %s at %v, %q, which reads as %s, %v; want %s", source, ctx, k, r,
							written, encoded(again), err, encoded(n))
					}
				}
				from := r.Start
				for _, c := range children(n) {
					check(c, Range{from, r.End})
					from = c.Range().End
				}
			}
			check(n, Range{0, length})
		}
	})
}

// FuzzCompile checks Compile on any source, in either context, on each board
// of the corpus's project and on none, from the corpus's sources on: it never
// panics; its error is an *Error of one of the parser's or the checker's
// codes, over characters of the source; and its AST encodes, resolves every
// name, is the same once Check reads it again, and is of an ast_version of
// the language.
func FuzzCompile(f *testing.F) {
	p, cases, sources := readCompileCorpus(f)
	_, more := readSyntaxCorpus(f)
	for _, s := range append(sources, more...) {
		f.Add(s)
	}
	boards := []string{""}
	for _, c := range cases {
		if !slices.Contains(boards, c.Board) {
			boards = append(boards, c.Board)
		}
	}
	f.Fuzz(func(t *testing.T, source string) {
		// The characters of the source, as Parse counts them, a byte that is
		// not UTF-8 one.
		length := utf8.RuneCountInString(source)
		for _, ctx := range []Context{ContextCondition, ContextScript} {
			for _, board := range boards {
				n, err := Compile(source, ctx, p, board)
				if err != nil {
					var e *Error
					if !errors.As(err, &e) ||
						!slices.Contains(syntaxCodes, e.Code) && !slices.Contains(compileCodes, e.Code) ||
						e.Start < 0 || e.Start > e.End || e.End > length {
						t.Fatalf("Compile(%q, %s, %q) = %v; want an error of a code, within the source's %d "+
							"characters", source, ctx, board, err, length)
					}
					continue
				}
				b, err := Encode(n)
				if err != nil {
					t.Fatalf("Encode(Compile(%q, %s, %q)): %v", source, ctx, board, err)
				}
				var tree any
				if err := json.Unmarshal(b, &tree); err != nil || !fullyResolved(tree) {
					t.Fatalf("Compile(%q, %s, %q) = %s, which leaves a name unresolved", source, ctx, board, b)
				}
				if err := Check(n, ctx, p, board); err != nil || encoded(n) != string(b) {
					t.Fatalf("Check of Compile(%q, %s, %q) = %v, %s; want nil, %s", source, ctx, board, err,
						encoded(n), b)
				}
				if v := VersionOf(n); v < 1 || v > ASTVersion {
					t.Fatalf("VersionOf(Compile(%q, %s, %q)) = %d", source, ctx, board, v)
				}
			}
		}
	})
}
