package expr

import (
	"errors"
	"slices"
	"testing"
	"unicode/utf8"
)

// FuzzParse checks Parse on any source, in either context, from the corpus's
// sources on: it never panics; its error is one of its codes, over characters
// of the source; and its AST encodes, the same bytes each time, with every
// node's range within the source.
func FuzzParse(f *testing.F) {
	_, sources := readSyntaxCorpus(f)
	for _, s := range sources {
		f.Add(s)
	}
	f.Add("x = a\n and b // c\r\n(signal(\"\\uD83D\\uDE00\"))")
	f.Fuzz(func(t *testing.T, source string) {
		length := utf8.RuneCountInString(source)
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
			var check func(Node)
			check = func(n Node) {
				if r := n.Range(); r.Start < 0 || r.Start > r.End || r.End > length {
					t.Fatalf("Parse(%q, %s) has a %s at %v, beyond the source's %d characters", source, ctx,
						n.Kind(), r, length)
				}
				for _, c := range children(n) {
					check(c)
				}
			}
			check(n)
		}
	})
}
