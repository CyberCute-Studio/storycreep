package expr

import (
	"errors"
	"slices"
	"testing"
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
				if !errors.As(err, &e) || !slices.Contains(codes, e.Code) || e.Start < 0 || e.Start > e.End ||
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
