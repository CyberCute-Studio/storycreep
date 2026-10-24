package semantics

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

var update = flag.Bool("update", false, "rewrite schema/unicode/tables.json and tables.go from the files of "+
	"schema/unicode/ucd")

// The files of the Unicode Character Database that the tables are generated
// from, in schema/unicode/ucd, of the version ucdVersion, and the SHA-256 of
// each as unicode.org publishes it, which TestTables checks, so that a file
// that an editor or a checkout changed, as one whose line endings it
// changed, fails rather than generating other tables. Moving to another
// version of Unicode is replacing the files with that version's, as
// schema/unicode/README.md says, and changing these.
const (
	ucdVersion = "18.0.0"
	// ucdUnicodeData is UnicodeData.txt, whose fields 12 and 13 are a code
	// point's Simple_Uppercase_Mapping and Simple_Lowercase_Mapping.
	ucdUnicodeData = "0736451de439ae7baf1425136617da495e09ee5afbe6e394374db7009ea08950"
	// ucdPropList is PropList.txt, which lists the code points of
	// White_Space.
	ucdPropList = "f438f532e8737bb8a2702126cdf9c4af5e357c58c7acf9d9eb2fc7c1a1d955d6"
)

var (
	unicodeDir = filepath.Join("..", "..", "..", "schema", "unicode")
	tablesFile = filepath.Join(unicodeDir, "tables.json")
	docsDir    = filepath.Join("..", "..", "..", "docs")
)

// versionStatements are the places that state the version of Unicode of the
// tables, each a file and a pattern whose group is the version, which
// TestTables checks are ucdVersion, so that moving to another version
// changes all of them.
var versionStatements = []struct {
	file    string
	pattern *regexp.Regexp
}{
	{filepath.Join(unicodeDir, "README.md"), regexp.MustCompile(`The tables are of Unicode (\d+\.\d+\.\d+)`)},
	{filepath.Join(unicodeDir, "README.md"), regexp.MustCompile(`unicode\.org/Public/(\d+\.\d+\.\d+)/`)},
	{filepath.Join(unicodeDir, "README.md"), regexp.MustCompile(`"unicodeVersion": "(\d+\.\d+\.\d+)"`)},
	{filepath.Join(unicodeDir, "README.md"), regexp.MustCompile(`The tables of this version, Unicode (\d+\.\d+\.\d+)`)},
	{filepath.Join(docsDir, "PRD.md"), regexp.MustCompile(`one pinned Unicode version, Unicode (\d+\.\d+\.\d+)`)},
	{filepath.Join(docsDir, "IMPLEMENTATION_PLAN.md"), regexp.MustCompile(`tables, of Unicode (\d+\.\d+\.\d+)`)},
}

// generated are the tables as the files of the UCD give them, and the code
// points that UnicodeData.txt assigns, by code point, which
// TestTablesAgainstGo reads.
type generated struct {
	upper, lower []caseMapping
	whiteSpace   []span
	assigned     []bool
}

// TestTables generates the tables that Upper, Lower, and Trim read from the
// files of the UCD in schema/unicode/ucd, and fails when
// schema/unicode/tables.json, which the TypeScript and GDScript evaluators
// read, or tables.go, which this package compiles, is not what it generates:
// run go test ./internal/semantics -run TestTables -update, in server/, to
// rewrite both, and run the tests again, which then compile the new tables.
// It also fails when a file of the UCD is not the one that unicode.org
// publishes for ucdVersion, or when a place of versionStatements, in the
// README, the PRD, or the plan, does not state it, or states another
// version.
func TestTables(t *testing.T) {
	g := readUCD(t)
	files := map[string][]byte{tablesFile: g.json(), "tables.go": g.goSource(t)}
	for path, want := range files {
		if *update {
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is not what the files of schema/unicode/ucd generate; run go test ./internal/semantics "+
				"-run TestTables -update", path)
		}
	}
	if UnicodeVersion != ucdVersion {
		t.Errorf("tables.go is of Unicode %s, and the files of schema/unicode/ucd of %s; run go test "+
			"./internal/semantics -run TestTables -update", UnicodeVersion, ucdVersion)
	}
	for _, s := range versionStatements {
		text, err := os.ReadFile(s.file)
		if err != nil {
			t.Fatal(err)
		}
		stated := s.pattern.FindAllSubmatch(text, -1)
		if len(stated) == 0 {
			t.Errorf("%s does not state the version of Unicode of the tables as %s", s.file, s.pattern)
		}
		for _, m := range stated {
			if string(m[1]) != ucdVersion {
				t.Errorf("%s says %q, and the tables are of Unicode %s", s.file, m[0], ucdVersion)
			}
		}
	}
}

// readUCD reads the files of the UCD, checks each against its SHA-256, and
// returns the tables that they give.
func readUCD(t *testing.T) generated {
	t.Helper()
	read := func(name, sum string) string {
		data, err := os.ReadFile(filepath.Join(unicodeDir, "ucd", name))
		if err != nil {
			t.Fatal(err)
		}
		if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != sum {
			t.Fatalf("schema/unicode/ucd/%s has the SHA-256 %x, not %s, that of the file that unicode.org "+
				"publishes for Unicode %s", name, got, sum, ucdVersion)
		}
		return string(data)
	}
	g := generated{assigned: make([]bool, unicode.MaxRune+1)}
	unicodeData, propList := read("UnicodeData.txt", ucdUnicodeData), read("PropList.txt", ucdPropList)
	if header := "# PropList-" + ucdVersion + ".txt\n"; !strings.HasPrefix(propList, header) {
		t.Fatalf("schema/unicode/ucd/PropList.txt does not start with %q", header)
	}
	// The first code point of the range of code points that a line of
	// <…, First> starts, as <CJK Ideograph, First>, and its line of <…,
	// Last> ends.
	first := rune(-1)
	for n, line := range strings.Split(strings.TrimSuffix(unicodeData, "\n"), "\n") {
		fields := strings.Split(line, ";")
		if len(fields) != 15 {
			t.Fatalf("UnicodeData.txt:%d has %d fields, not 15", n+1, len(fields))
		}
		from := codePoint(t, fields[0])
		switch {
		case strings.HasSuffix(fields[1], "First>"):
			first = from
		case strings.HasSuffix(fields[1], "Last>"):
			if first < 0 || first > from {
				t.Fatalf("UnicodeData.txt:%d ends a range that no line started", n+1)
			}
			for r := first; r <= from; r++ {
				g.assigned[r] = true
			}
			first = -1
		default:
			g.assigned[from] = true
		}
		for _, m := range []struct {
			table *[]caseMapping
			field string
		}{{&g.upper, fields[12]}, {&g.lower, fields[13]}} {
			if m.field == "" {
				continue
			}
			// A simple mapping is one code point, never the code point
			// itself, and no line of a range of code points, as
			// <CJK Ideograph, First>, has one.
			to := codePoint(t, m.field)
			if to == from || strings.HasSuffix(fields[1], "First>") || strings.HasSuffix(fields[1], "Last>") {
				t.Fatalf("UnicodeData.txt:%d maps U+%04X to U+%04X", n+1, from, to)
			}
			*m.table = append(*m.table, caseMapping{from, to})
		}
	}
	for n, line := range strings.Split(propList, "\n") {
		line, _, _ = strings.Cut(line, "#")
		codes, property, ok := strings.Cut(line, ";")
		if !ok || strings.TrimSpace(property) != "White_Space" {
			continue
		}
		first, last, isRange := strings.Cut(strings.TrimSpace(codes), "..")
		s := span{codePoint(t, first), codePoint(t, first)}
		if isRange {
			s.last = codePoint(t, last)
		}
		if s.last < s.first {
			t.Fatalf("PropList.txt:%d has the range %s", n+1, codes)
		}
		g.whiteSpace = append(g.whiteSpace, s)
	}
	for _, table := range [][]caseMapping{g.upper, g.lower} {
		slices.SortFunc(table, func(a, b caseMapping) int { return cmp.Compare(a.from, b.from) })
		for i := 1; i < len(table); i++ {
			if table[i].from == table[i-1].from {
				t.Fatalf("UnicodeData.txt maps U+%04X twice", table[i].from)
			}
		}
	}
	// The ranges of White_Space, sorted, and joined where they meet, as
	// U+2028 and U+2029, which PropList.txt lists apart.
	slices.SortFunc(g.whiteSpace, func(a, b span) int { return cmp.Compare(a.first, b.first) })
	var joined []span
	for _, s := range g.whiteSpace {
		if n := len(joined); n > 0 && s.first <= joined[n-1].last+1 {
			joined[n-1].last = max(joined[n-1].last, s.last)
			continue
		}
		joined = append(joined, s)
	}
	g.whiteSpace = joined
	if len(g.upper) == 0 || len(g.lower) == 0 || len(g.whiteSpace) == 0 {
		t.Fatalf("the files of the UCD give %d uppercase and %d lowercase mappings and %d ranges of White_Space",
			len(g.upper), len(g.lower), len(g.whiteSpace))
	}
	return g
}

// codePoint returns the code point that s writes in hexadecimal, as the
// UCD's files write one.
func codePoint(t *testing.T, s string) rune {
	t.Helper()
	n, err := strconv.ParseUint(strings.TrimSpace(s), 16, 32)
	if err != nil || n > unicode.MaxRune {
		t.Fatalf("%q is not a code point", s)
	}
	return rune(n)
}

// json returns the tables as schema/unicode/tables.json holds them, which
// its README describes: each mapping and each range on a line of its own, so
// that a new version of Unicode changes the lines of what it changes.
func (g generated) json() []byte {
	var b bytes.Buffer
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"unicodeVersion\": %q,\n", ucdVersion)
	pairs := func(name string, pairs [][2]rune, last bool) {
		fmt.Fprintf(&b, "  %q: [\n", name)
		for i, p := range pairs {
			fmt.Fprintf(&b, "    [%d, %d]", p[0], p[1])
			if i < len(pairs)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString("  ]")
		if !last {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	pairs("lower", mappingPairs(g.lower), false)
	pairs("upper", mappingPairs(g.upper), false)
	pairs("whiteSpace", spanPairs(g.whiteSpace), true)
	b.WriteString("}\n")
	return b.Bytes()
}

// mappingPairs returns each mapping of table as its two code points.
func mappingPairs(table []caseMapping) [][2]rune {
	pairs := make([][2]rune, len(table))
	for i, m := range table {
		pairs[i] = [2]rune{m.from, m.to}
	}
	return pairs
}

// spanPairs returns each range of spans as its first and last code points.
func spanPairs(spans []span) [][2]rune {
	pairs := make([][2]rune, len(spans))
	for i, s := range spans {
		pairs[i] = [2]rune{s.first, s.last}
	}
	return pairs
}

// goSource returns tables.go, which holds the tables for this package.
func (g generated) goSource(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString(`// Code generated by TestTables from the files of schema/unicode/ucd. DO NOT EDIT.

package semantics

// UnicodeVersion is the version of Unicode whose simple case mappings and
// White_Space Upper, Lower, Trim, and IsWhiteSpace read (PRD §5.8.9): that of
// the files of schema/unicode/ucd, which the tables are generated from, as
// those of schema/unicode/tables.json are for the other evaluators. A new
// version of Unicode is a new ast_version, of upper, lower, and trim.
`)
	fmt.Fprintf(&b, "const UnicodeVersion = %q\n", ucdVersion)
	table := func(doc, name string, pairs [][2]rune, typ string) {
		fmt.Fprintf(&b, "\n%svar %s = []%s{\n", doc, name, typ)
		for _, p := range pairs {
			fmt.Fprintf(&b, "\t{0x%04X, 0x%04X},\n", p[0], p[1])
		}
		b.WriteString("}\n")
	}
	table("// upperTable maps each code point that has a simple uppercase mapping\n"+
		"// (UnicodeData.txt's Simple_Uppercase_Mapping) to it, in the order of the\n"+
		"// code points.\n", "upperTable", mappingPairs(g.upper), "caseMapping")
	table("// lowerTable maps each code point that has a simple lowercase mapping\n"+
		"// (UnicodeData.txt's Simple_Lowercase_Mapping) to it, in the order of the\n"+
		"// code points.\n", "lowerTable", mappingPairs(g.lower), "caseMapping")
	table("// whiteSpace holds the ranges of the code points of White_Space\n"+
		"// (PropList.txt), in their order, those that meet joined.\n", "whiteSpace", spanPairs(g.whiteSpace), "span")
	src, err := format.Source(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// TestTablesAgainstGo compares the tables with package unicode's, which are
// of the version of Unicode that unicode.Version names, so that a mistake in
// reading the UCD's files shows: a code point's simple mappings and whether
// it is of White_Space are the same in both, but where Unicode assigned the
// code point, or the one that it maps to, in a version after the older of
// the two, which Unicode's stability policy for case pairs allows, whichever
// of the two is the older. U+0264 LATIN SMALL LETTER RAMS HORN, which had no
// uppercase in Unicode 15.0, maps to U+A7CB LATIN CAPITAL LETTER RAMS HORN,
// which Unicode 16.0 assigned. A code point is assigned in the older version
// when both assign it: package unicode's tables, and UnicodeData.txt, which
// lists every code point that its version assigns.
func TestTablesAgainstGo(t *testing.T) {
	t.Parallel()
	ours := readUCD(t).assigned
	// Whether both assign r: whether UnicodeData.txt lists it, and it is of
	// one of package unicode's general categories but Cn, the unassigned code
	// points, or C, which holds Cn.
	assigned := func(r rune) bool {
		if !ours[r] {
			return false
		}
		for name, table := range unicode.Categories {
			if name != "Cn" && name != "C" && unicode.Is(table, r) {
				return true
			}
		}
		return false
	}
	differ := 0
	for r := rune(0); r <= unicode.MaxRune; r++ {
		upper, _ := caseOf(upperTable, r)
		lower, _ := caseOf(lowerTable, r)
		for _, c := range []struct {
			name         string
			ours, theirs rune
		}{
			{"uppercase", upper, unicode.ToUpper(r)},
			{"lowercase", lower, unicode.ToLower(r)},
		} {
			if c.ours != c.theirs {
				differ++
				if assigned(r) && assigned(c.ours) && assigned(c.theirs) {
					t.Errorf("U+%04X's %s is U+%04X, and package unicode's (Unicode %s) is U+%04X, though it "+
						"has assigned all three", r, c.name, c.ours, unicode.Version, c.theirs)
				}
			}
		}
		if IsWhiteSpace(r) != unicode.Is(unicode.White_Space, r) && assigned(r) {
			t.Errorf("U+%04X is of White_Space: %t, and of package unicode's (Unicode %s): %t", r,
				IsWhiteSpace(r), unicode.Version, unicode.Is(unicode.White_Space, r))
		}
	}
	t.Logf("%d mappings differ from those of package unicode, of Unicode %s", differ, unicode.Version)
	if UnicodeVersion != unicode.Version && Upper("ɤ") == "ɤ" {
		t.Errorf("upper(U+0264) is U+0264, though Unicode 16.0 maps it to U+A7CB")
	}
}
