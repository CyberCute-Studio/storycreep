package input

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// maxNonStarters is how many non-starters in a row golang.org/x/text's form C
// takes: before a code point that would make more, it inserts cgj, as
// Unicode's Stream-Safe Text Format does (UAX #15), which the platform's form
// C of a browser does not.
const maxNonStarters = 30

// cgj is U+034F COMBINING GRAPHEME JOINER, which x/text's form C inserts
// after maxNonStarters non-starters in a row.
const cgj = '\u034f'

// acute is U+0301 COMBINING ACUTE ACCENT, which x/text's form C counts as one
// non-starter at its start and one at its end, by which nonStarters measures
// how many it counts every other code point as.
const acute = "\u0301"

// inert reports whether Unicode's normalization form C, as
// golang.org/x/text/unicode/norm puts a string in it, leaves r as it is, and
// never joins it with, nor reorders it around, the code points beside it, as
// of every code point that the version of Unicode of its tables does not
// assign. Form C counts an inert code point as no non-starter at either end,
// since its quick-check flags, which hold its count at its end, are clear, so
// that one ends every row of non-starters.
func inert(r rune) bool {
	p := norm.NFC.PropertiesString(string(r))
	return p.BoundaryBefore() && p.BoundaryAfter()
}

// counts are how many non-starters x/text's form C counts a code point as,
// at its start (lead) and at its end (trail), as the Stream-Safe Text Format
// counts those of its compatibility decomposition: a code point that it
// counts as any at its start is a non-starter, and one that it counts as none
// there a starter, which ends the row before it.
type counts struct{ lead, trail int }

// nonStarters returns the counts of every code point that is not inert, but
// for those of none at either end. It measures them by where form C inserts a
// CGJ, since x/text does not export them: after a code point and
// maxNonStarters+1 acutes, before the acute that makes one too many, so that
// trail acutes and one more come after the CGJ; and, after "a" and
// maxNonStarters acutes, before the code point when it counts any at its
// start, where x/text counts as many as at its end, which it checks.
func nonStarters(t *testing.T) map[rune]counts {
	t.Helper()
	joiner := string(cgj)
	joins := func(n int, s string) bool {
		return strings.Contains(norm.NFC.String("a"+strings.Repeat(acute, n)+s), joiner)
	}
	m := make(map[rune]counts)
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if !utf8.ValidRune(r) || inert(r) {
			continue
		}
		s := string(r)
		out := norm.NFC.String(s + strings.Repeat(acute, maxNonStarters+1))
		i := strings.LastIndex(out, joiner)
		if i < 0 {
			t.Fatalf("form C of %U and %d acutes inserts no CGJ", r, maxNonStarters+1)
		}
		after := out[i+len(joiner):]
		c := counts{trail: utf8.RuneCountInString(after) - 1}
		if after != strings.Repeat(acute, c.trail+1) {
			t.Fatalf("form C of %U and %d acutes is %+q after its CGJ", r, maxNonStarters+1, after)
		}
		if joins(maxNonStarters, s) {
			c.lead = c.trail
			if c.trail == 0 || joins(maxNonStarters-c.trail, s) || !joins(maxNonStarters-c.trail+1, s) {
				t.Fatalf("x/text's form C counts %U as other non-starters at its start than at its end", r)
			}
		}
		if c != (counts{}) {
			m[r] = c
		}
	}
	return m
}

// folds returns what the folding of FoldCase, x/text's, folds each code point
// that it changes to, which fold.json's fold lists.
func folds() map[rune]string {
	fold := cases.Fold()
	m := make(map[rune]string)
	for r := rune(0); r <= utf8.MaxRune; r++ {
		// A surrogate, which no string of UTF-8 holds, is skipped.
		if !utf8.ValidRune(r) {
			continue
		}
		s := string(r)
		if f := fold.String(s); f != s {
			m[r] = f
		}
	}
	return m
}

// The Hangul syllables and their jamo, which Unicode decomposes and composes
// by an algorithm (§3.12) rather than by the tables of fold.json: a syllable
// is a leading consonant (L), a vowel (V), and, but for the first of every
// tCount syllables, a trailing consonant (T).
const (
	sBase  = 0xac00
	lBase  = 0x1100
	vBase  = 0x1161
	tBase  = 0x11a7
	lCount = 19
	vCount = 21
	tCount = 28
	nCount = vCount * tCount
	sCount = lCount * nCount
)

// nfcTables are what fold.json holds of x/text's form C, by which the
// TypeScript runtime puts a run of code points in it rather than by the
// platform's form C, which is of another version of Unicode, older, as on a
// tablet whose system has not been updated, or newer: the canonical combining
// class of each code point whose class is not 0 (ccc), the canonical
// decomposition, form D, of each code point that form D changes but the
// Hangul syllables (decompositions), and the code point that form C joins
// each pair of two into but the Hangul syllables (composites).
type nfcTables struct {
	ccc            map[rune]uint8
	decompositions map[rune][]rune
	composites     map[[2]rune]rune
}

// newNFCTables returns x/text's nfcTables. It takes the pair that form C
// joins into a code point to be what form C makes of all but the last code
// point of its decomposition and that last one, as form C joins the code
// points of a decomposition from its first, and checks that form C joins the
// two into it, of every code point that form C leaves as it is and form D
// does not.
func newNFCTables(t *testing.T) nfcTables {
	t.Helper()
	n := nfcTables{ccc: map[rune]uint8{}, decompositions: map[rune][]rune{}, composites: map[[2]rune]rune{}}
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		s := string(r)
		if c := norm.NFC.PropertiesString(s).CCC(); c != 0 {
			n.ccc[r] = c
		}
		if r >= sBase && r < sBase+sCount {
			continue
		}
		d := []rune(norm.NFD.String(s))
		if string(d) == s {
			continue
		}
		n.decompositions[r] = d
		if len(d) < 2 || norm.NFC.String(s) != s {
			continue
		}
		first := []rune(norm.NFC.String(string(d[:len(d)-1])))
		if len(first) != 1 || norm.NFC.String(string(first)+string(d[len(d)-1])) != s {
			t.Fatalf("form C does not join %U from the form C of all but the last code point of its decomposition, "+
				"%U, and that last one", r, d)
		}
		n.composites[[2]rune{first[0], d[len(d)-1]}] = r
	}
	return n
}

// compose returns the code point that form C joins first and second into, if
// it joins them, by the algorithm of the Hangul syllables or by composites.
func (n nfcTables) compose(first, second rune) (rune, bool) {
	if first >= lBase && first < lBase+lCount && second >= vBase && second < vBase+vCount {
		return sBase + ((first-lBase)*vCount+second-vBase)*tCount, true
	}
	if first >= sBase && first < sBase+sCount && (first-sBase)%tCount == 0 && second > tBase && second < tBase+tCount {
		return first + second - tBase, true
	}
	c, ok := n.composites[[2]rune{first, second}]
	return c, ok
}

// nfc returns s in Unicode's normalization form C by the tables, as the
// TypeScript runtime puts a run of code points between two CGJs in it
// (UAX #15): each code point decomposed, the non-starters of each row of them
// then in the order of their combining classes, kept where two are of one
// class, and each code point then joined, from the first, with the last
// starter before it, as long as nothing between the two that is left is a
// starter or of its class or a greater one, which a starter joins only with
// the starter just before it.
func (n nfcTables) nfc(s string) string {
	var d []rune
	for _, r := range s {
		if h := r - sBase; h >= 0 && h < sCount {
			d = append(d, lBase+h/nCount, vBase+h%nCount/tCount)
			if h%tCount != 0 {
				d = append(d, tBase+h%tCount)
			}
		} else if to, ok := n.decompositions[r]; ok {
			d = append(d, to...)
		} else {
			d = append(d, r)
		}
	}
	for i := 1; i < len(d); i++ {
		c := n.ccc[d[i]]
		for j := i; c != 0 && j > 0 && n.ccc[d[j-1]] > c; j-- {
			d[j-1], d[j] = d[j], d[j-1]
		}
	}
	var out []rune
	// starter is where in out the last starter is, and last the class of the
	// last code point that out holds after it, 0 when it holds none, and 256
	// when out starts with no starter, so that nothing joins with its first.
	starter, last := 0, 0
	for i, r := range d {
		c := int(n.ccc[r])
		if i == 0 {
			if c != 0 {
				last = 256
			}
			out = append(out, r)
			continue
		}
		if j, ok := n.compose(out[starter], r); ok && (last == 0 || last < c) {
			out[starter] = j
			continue
		}
		if c == 0 {
			starter = len(out)
		}
		last = c
		out = append(out, r)
	}
	return string(out)
}

// check is an input of fold.json's checks: its name, the string, and its
// FoldCase.
type check struct{ name, s, folded string }

// neighbours are what checks put after a code point that the folding changes
// or that is not inert: the letters and the marks whose neighbours a folding
// or form C might read, and nothing.
var neighbours = []string{"a", "\u0307", "\u0345", "\u03a3", "\u03c2", "\u0130", "\uac00", "\u11a8", ""}

// checks returns the inputs of fold.json's checks, as its README says that a
// runtime builds them: every, every code point in order; beside, every code
// point that the folding changes or that is not inert, beside each of
// neighbours; and streams, every code point that is not inert after rows of
// 30, 29, and 28 acutes, and before one of 30, so that form C inserts a CGJ
// before it, or before an acute after it, exactly where its counts say; and
// FoldCase of each. fs is what folds returns.
func checks(fs map[rune]string) []check {
	var every, beside, streams strings.Builder
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		s := string(r)
		every.WriteString(s)
		if _, ok := fs[r]; ok || !inert(r) {
			for _, c := range neighbours {
				beside.WriteString(s)
				beside.WriteString(c)
			}
		}
		if !inert(r) {
			for _, row := range []struct {
				before int
				after  int
			}{{maxNonStarters, maxNonStarters}, {maxNonStarters - 1, 0}, {maxNonStarters - 2, 0}} {
				streams.WriteString("a")
				streams.WriteString(strings.Repeat(acute, row.before))
				streams.WriteString(s)
				streams.WriteString(strings.Repeat(acute, row.after))
			}
		}
	}
	var cs []check
	for _, c := range []check{{name: "every", s: every.String()}, {name: "beside", s: beside.String()},
		{name: "streams", s: streams.String()}} {
		c.folded = FoldCase(c.s)
		cs = append(cs, c)
	}
	return cs
}

// foldTable returns schema/unicode/fold.json as the case folding of FoldCase
// gives it, which the file's README describes: the version of Unicode of the
// tables of golang.org/x/text/cases and golang.org/x/text/unicode/norm that
// the Go of the tests builds it with; each code point that the folding
// changes, with the code points that it folds it to, on a line of its own, in
// the order of the code points; the ranges of the code points that are not
// inert, each on a line of its own, in their order; the ranges of the code
// points of each counts, ns, other than none at either end, each with its
// counts, on a line of its own, in their order; the ranges of the code points
// of each canonical combining class other than 0, each with the class, on a
// line of its own, in their order; each code point that form D changes but
// the Hangul syllables, with its decomposition, on a line of its own, in the
// order of the code points; each code point that form C joins two into but
// the Hangul syllables, with the two, on a line of its own, in the order of
// the code points; and the SHA-256 of FoldCase of each of the checks, cs, in
// their order. fs is what folds returns, and n what newNFCTables returns.
func foldTable(fs map[rune]string, ns map[rune]counts, n nfcTables, cs []check) []byte {
	var b bytes.Buffer
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"unicodeVersion\": %q,\n", cases.UnicodeVersion)
	b.WriteString("  \"fold\": [\n")
	first := true
	for r := rune(0); r <= utf8.MaxRune; r++ {
		folded, ok := fs[r]
		if !ok {
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
	b.WriteString("\n  ],\n")
	b.WriteString("  \"nonStarters\": [\n")
	first = true
	for r := rune(0); r <= utf8.MaxRune; r++ {
		c, ok := ns[r]
		if !ok {
			continue
		}
		last := r
		for last < utf8.MaxRune && ns[last+1] == c {
			last++
		}
		if !first {
			b.WriteString(",\n")
		}
		first = false
		fmt.Fprintf(&b, "    [%d, %d, %d, %d]", r, last, c.lead, c.trail)
		r = last
	}
	b.WriteString("\n  ],\n")
	b.WriteString("  \"ccc\": [\n")
	first = true
	for r := rune(0); r <= utf8.MaxRune; r++ {
		c, ok := n.ccc[r]
		if !ok {
			continue
		}
		last := r
		for last < utf8.MaxRune && n.ccc[last+1] == c {
			last++
		}
		if !first {
			b.WriteString(",\n")
		}
		first = false
		fmt.Fprintf(&b, "    [%d, %d, %d]", r, last, c)
		r = last
	}
	b.WriteString("\n  ],\n")
	b.WriteString("  \"decompositions\": [\n")
	first = true
	for r := rune(0); r <= utf8.MaxRune; r++ {
		d, ok := n.decompositions[r]
		if !ok {
			continue
		}
		if !first {
			b.WriteString(",\n")
		}
		first = false
		fmt.Fprintf(&b, "    [%d", r)
		for _, c := range d {
			fmt.Fprintf(&b, ", %d", c)
		}
		b.WriteString("]")
	}
	b.WriteString("\n  ],\n")
	b.WriteString("  \"compositions\": [\n")
	pairs := make(map[rune][2]rune, len(n.composites))
	for pair, c := range n.composites {
		pairs[c] = pair
	}
	first = true
	for r := rune(0); r <= utf8.MaxRune; r++ {
		pair, ok := pairs[r]
		if !ok {
			continue
		}
		if !first {
			b.WriteString(",\n")
		}
		first = false
		fmt.Fprintf(&b, "    [%d, %d, %d]", r, pair[0], pair[1])
	}
	b.WriteString("\n  ],\n")
	b.WriteString("  \"checks\": {\n")
	for i, c := range cs {
		sum := sha256.Sum256([]byte(c.folded))
		fmt.Fprintf(&b, "    %q: \"sha256:%s\"", c.name, hex.EncodeToString(sum[:]))
		if i < len(cs)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  }\n}\n")
	return b.Bytes()
}

// formC returns s in form C as the TypeScript runtime puts a tag in it, by
// the tables of fold.json: a CGJ before each code point that would make more
// than maxNonStarters non-starters in a row, by the counts of ns, as x/text
// inserts one; each run of the code points that are not inert between them
// then in form C by the tables, n, which insert no CGJ, which it checks give
// what x/text's form C gives of the run; and each other code point as it is.
func formC(t *testing.T, s string, ns map[rune]counts, n nfcTables) string {
	t.Helper()
	var b, run strings.Builder
	flush := func() {
		if run.Len() == 0 {
			return
		}
		c := n.nfc(run.String())
		if want := norm.NFC.String(run.String()); c != want {
			t.Fatalf("form C of %+.40q is %+.40q, and by fold.json's tables, which insert no CGJ, %+.40q",
				run.String(), want, c)
		}
		b.WriteString(c)
		run.Reset()
	}
	// How many non-starters in a row run ends with.
	row := 0
	for _, r := range s {
		if inert(r) {
			flush()
			b.WriteRune(r)
			row = 0
			continue
		}
		switch c := ns[r]; {
		case c.lead == 0:
			row = c.trail
		case row+c.lead > maxNonStarters:
			flush()
			b.WriteRune(cgj)
			row = c.lead
		default:
			row += c.lead
		}
		run.WriteRune(r)
	}
	flush()
	return b.String()
}

// TestFoldTable fails when schema/unicode/fold.json, by which the TypeScript
// runtime compares a node's tags as FoldCase does, is not the case folding
// that FoldCase does, the code points that its form C does not leave alone,
// how many non-starters it counts each as, its tables of form C, or the
// SHA-256 of FoldCase of its checks, as when a newer golang.org/x/text, or a
// newer Go, with which x/text builds the tables of another version of
// Unicode, changes them: run go test ./internal/input -run TestFoldTable
// -update, in server/, to rewrite it. It also checks that FoldCase is what
// the TypeScript runtime computes of the table: the name in form C (formC),
// each of its code points then folded alone, and the result in form C again,
// which holds as long as the folding reads no code point's neighbours, as
// x/text's does not, of the checks and of names whose tags a node may have;
// and that the tables of form C give x/text's form C of every code point that
// is not inert, and of each pair that they join. With -short, it checks only the
// version of Unicode that fold.json states, since it measures form C of every
// code point that is not inert, and folds every code point, which go test
// ./... does, as the continuous integration's database job runs it.
func TestFoldTable(t *testing.T) {
	if norm.Version != cases.UnicodeVersion {
		t.Fatalf("x/text's form C is of Unicode %s, and its case folding of %s, though fold.json states one version",
			norm.Version, cases.UnicodeVersion)
	}
	if !*update {
		data, err := os.ReadFile(foldFile)
		if err != nil {
			t.Fatal(err)
		}
		var head struct {
			UnicodeVersion string `json:"unicodeVersion"`
		}
		if err := json.Unmarshal(data, &head); err != nil {
			t.Fatal(err)
		}
		if head.UnicodeVersion != cases.UnicodeVersion {
			t.Fatalf("%s is of Unicode %s, and x/text's tables of %s; run go test ./internal/input -run TestFoldTable "+
				"-update", foldFile, head.UnicodeVersion, cases.UnicodeVersion)
		}
		if testing.Short() {
			t.Skip("skipped with -short beyond the version of Unicode: go test ./... checks fold.json")
		}
	}
	fs := folds()
	ns := nonStarters(t)
	n := newNFCTables(t)
	cs := checks(fs)
	want := foldTable(fs, ns, n, cs)
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
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if s := string(r); utf8.ValidRune(r) && !inert(r) && n.nfc(s) != norm.NFC.String(s) {
			t.Errorf("form C of %U by fold.json's tables is %+q, and x/text's %+q", r, n.nfc(s), norm.NFC.String(s))
		}
	}
	for pair, c := range n.composites {
		if got := n.nfc(string(pair[:])); got != string(c) {
			t.Errorf("form C of %U by fold.json's tables is %+q, not %U", pair, got, c)
		}
	}
	tsFold := func(name string) string {
		var b strings.Builder
		for _, r := range formC(t, name, ns, n) {
			if f, ok := fs[r]; ok {
				b.WriteString(f)
			} else {
				b.WriteRune(r)
			}
		}
		return formC(t, b.String(), ns, n)
	}
	for _, c := range cs {
		if got := tsFold(c.s); got != c.folded {
			t.Errorf("FoldCase of the check %s is not what folding each of its code points alone gives", c.name)
		}
	}
	for _, name := range []string{
		"Quest", "QUEST", "Straße", "STRASSE", "ẞ", "ΟΔΟΣ", "οδος", "ς", "ΐ", "\u03aa\u0301",
		"é", "e\u0301", "kır", "KIR", "\U00016d68", "\U00016d67\U00016d67", "\uac00\u11a8",
		"İstanbul", "ǅ", "Ꭰꭰ", "ﬃ", "ŉ", "Ⅻ", "𐐀𐐨", "",
		"a" + strings.Repeat("\u0323", 16) + strings.Repeat(acute, 16),
		"a" + strings.Repeat(acute, 16) + strings.Repeat("\u0323", 16),
		"\uac01" + strings.Repeat(acute, 29), "a" + strings.Repeat(acute, 30) + "\uff9e",
		"\u1f80" + strings.Repeat("\u0345", 40),
	} {
		if got, want := tsFold(name), FoldCase(name); got != want {
			t.Errorf("FoldCase(%.40q) = %.40q, but folding each of its code points alone gives %.40q", name, want,
				got)
		}
	}
}
