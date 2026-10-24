package semantics

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestCaseKeepsLength checks that Upper and Lower map every code point to
// one, so that a string's length never changes (§5.8.9), and keep a byte
// that is not UTF-8 as it is.
func TestCaseKeepsLength(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if utf8.ValidRune(r) {
			b.WriteRune(r)
		}
	}
	all := b.String()
	for name, f := range map[string]func(string) string{"Upper": Upper, "Lower": Lower} {
		if got := f(all); Len(got) != Len(all) || !utf8.ValidString(got) {
			t.Errorf("%s of every code point has %d code points, not %d", name, Len(got), Len(all))
		}
		if got := f("a\xffb"); got != map[string]string{"Upper": "A\xffB", "Lower": "a\xffb"}[name] {
			t.Errorf("%s(%q) = %q", name, "a\xffb", got)
		}
	}
	if got := Lower("A\xffB"); got != "a\xffb" {
		t.Errorf("Lower(%q) = %q", "A\xffB", got)
	}
}
