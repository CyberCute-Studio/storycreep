package input

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var update = flag.Bool("update", false, "rewrite schema/unicode/fold.json from the case folding of FoldCase")

// foldFile is the table of the case folding that FoldCase does, by which the
// TypeScript runtime's loader compares a node's tags (web/shared/src/flow).
var foldFile = filepath.Join("..", "..", "..", "schema", "unicode", "fold.json")

// foldTable returns schema/unicode/fold.json as the case folding of FoldCase
// gives it, which the file's README describes: the version of Unicode of the
// tables of golang.org/x/text/cases that the Go of the tests builds it with,
// and each code point that the folding changes, with the code points that it
// folds it to, on a line of its own, in the order of the code points.
func foldTable() []byte {
	fold := cases.Fold()
	var b bytes.Buffer
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"unicodeVersion\": %q,\n", cases.UnicodeVersion)
	b.WriteString("  \"fold\": [\n")
	first := true
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			// A surrogate, which no string of UTF-8 holds.
			continue
		}
		s := string(r)
		folded := fold.String(s)
		if folded == s {
			continue
		}
		if !first {
			b.WriteString(",\n")
		}
		first = false
		fmt.Fprintf(&b, "    [%d", r)
		for _, c := range folded {
			fmt.Fprintf(&b, ", %d", c)
		}
		b.WriteString("]")
	}
	b.WriteString("\n  ]\n}\n")
	return b.Bytes()
}

// TestFoldTable fails when schema/unicode/fold.json, by which the TypeScript
// runtime compares a node's tags as FoldCase does, is not the case folding
// that FoldCase does, as when a newer golang.org/x/text, or a newer Go, with
// which x/text builds the tables of another version of Unicode, changes it:
// run go test ./internal/input -run TestFoldTable -update, in server/, to
// rewrite it. It also checks that FoldCase is what the TypeScript runtime
// computes of the table, the name in form C, each of its code points folded
// alone, and the result in form C again, which holds as long as the folding
// reads no code point's neighbours, as x/text's does not.
func TestFoldTable(t *testing.T) {
	want := foldTable()
	if *update {
		if err := os.WriteFile(foldFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(foldFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not the case folding of FoldCase; run go test ./internal/input -run TestFoldTable -update",
			foldFile)
	}
	fold := cases.Fold()
	for _, name := range []string{
		"Quest", "QUEST", "Straße", "STRASSE", "ẞ", "ΟΔΟΣ", "οδος", "ς", "ΐ", "\u03aa\u0301",
		"é", "e\u0301", "kır", "KIR",
		"İstanbul", "ǅ", "Ꭰꭰ", "ﬃ", "ŉ", "Ⅻ", "𐐀𐐨", "",
	} {
		var b strings.Builder
		for _, r := range norm.NFC.String(name) {
			b.WriteString(fold.String(string(r)))
		}
		if got, want := norm.NFC.String(b.String()), FoldCase(name); got != want {
			t.Errorf("FoldCase(%q) = %q, but folding each of its code points alone gives %q", name, want, got)
		}
	}
}
