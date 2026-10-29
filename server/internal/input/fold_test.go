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

// inert reports whether Unicode's normalization form C, as
// golang.org/x/text/unicode/norm puts a string in it, leaves r as it is, and
// never joins it with, nor reorders it around, the code points beside it, as
// of every code point that the version of Unicode of its tables does not
// assign.
func inert(r rune) bool {
	p := norm.NFC.PropertiesString(string(r))
	return p.BoundaryBefore() && p.BoundaryAfter()
}

// foldTable returns schema/unicode/fold.json as the case folding of FoldCase
// gives it, which the file's README describes: the version of Unicode of the
// tables of golang.org/x/text/cases and golang.org/x/text/unicode/norm that
// the Go of the tests builds it with; each code point that the folding
// changes, with the code points that it folds it to, on a line of its own, in
// the order of the code points; and the ranges of the code points that are
// not inert, each on a line of its own, in their order.
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
	b.WriteString("\n  ],\n")
	b.WriteString("  \"nfc\": [\n")
	first = true
	for r := rune(0); r <= utf8.MaxRune; r++ {
		// A surrogate, which no string of UTF-8 holds, is in no range.
		if !utf8.ValidRune(r) || inert(r) {
			continue
		}
		last := r
		for last < utf8.MaxRune && utf8.ValidRune(last+1) && !inert(last+1) {
			last++
		}
		if !first {
			b.WriteString(",\n")
		}
		first = false
		fmt.Fprintf(&b, "    [%d, %d]", r, last)
		r = last
	}
	b.WriteString("\n  ]\n}\n")
	return b.Bytes()
}

// formC returns s in form C as the TypeScript runtime puts a tag in it, of
// the ranges of fold.json: each run of the code points that are not inert in
// form C, and each other code point as it is.
func formC(s string) string {
	var b, run strings.Builder
	for _, r := range s {
		if inert(r) {
			b.WriteString(norm.NFC.String(run.String()))
			run.Reset()
			b.WriteRune(r)
			continue
		}
		run.WriteRune(r)
	}
	b.WriteString(norm.NFC.String(run.String()))
	return b.String()
}

// TestFoldTable fails when schema/unicode/fold.json, by which the TypeScript
// runtime compares a node's tags as FoldCase does, is not the case folding
// that FoldCase does, nor the code points that its form C does not leave
// alone, as when a newer golang.org/x/text, or a newer Go, with which x/text
// builds the tables of another version of Unicode, changes them: run go test
// ./internal/input -run TestFoldTable -update, in server/, to rewrite it. It
// also checks that FoldCase is what the TypeScript runtime computes of the
// table: the name in form C, each run of its code points that are not inert
// in form C put in it apart and every other code point left as it is, each
// of its code points then folded alone, and the result in form C again,
// which holds as long as the folding reads no code point's neighbours, as
// x/text's does not, of every code point beside the next one and the one
// before it, and of every code point that the folding changes or that is
// not inert beside the letters and the marks whose neighbours a folding or
// form C might read.
func TestFoldTable(t *testing.T) {
	if norm.Version != cases.UnicodeVersion {
		t.Fatalf("x/text's form C is of Unicode %s, and its case folding of %s, though fold.json states one version",
			norm.Version, cases.UnicodeVersion)
	}
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
	var every, beside strings.Builder
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		every.WriteRune(r)
		if s := string(r); fold.String(s) != s || !inert(r) {
			for _, c := range []string{"a", "\u0307", "\u0345", "\u03a3", "\u03c2", "\u0130", "\uac00", "\u11a8", ""} {
				beside.WriteString(s)
				beside.WriteString(c)
			}
		}
	}
	for _, name := range []string{
		every.String(), beside.String(),
		"Quest", "QUEST", "Straße", "STRASSE", "ẞ", "ΟΔΟΣ", "οδος", "ς", "ΐ", "\u03aa\u0301",
		"é", "e\u0301", "kır", "KIR", "\U00016d68", "\U00016d67\U00016d67", "\uac00\u11a8",
		"İstanbul", "ǅ", "Ꭰꭰ", "ﬃ", "ŉ", "Ⅻ", "𐐀𐐨", "",
	} {
		var b strings.Builder
		for _, r := range formC(name) {
			b.WriteString(fold.String(string(r)))
		}
		if got, want := formC(b.String()), FoldCase(name); got != want {
			t.Errorf("FoldCase(%.40q) = %.40q, but folding each of its code points alone gives %.40q", name, want,
				got)
		}
	}
}
