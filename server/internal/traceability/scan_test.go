package traceability

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The sources below are written one line per string, so that no line of this
// file starts with a Verifies comment: the repository's own report scans it.
func lines(ls ...string) string {
	return strings.Join(ls, "\n")
}

func tags(line int, ids ...string) []Tag {
	var ts []Tag
	for _, id := range ids {
		ts = append(ts, Tag{ID: id, Line: line})
	}
	return ts
}

// Verifies: NFR-Q-02
func TestScanFile(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		notTest  bool // the file is not a test file
		src      string
		want     []Test
		problems []string
	}{
		// Go.
		{
			name: "Go test with a doc comment",
			file: "x_test.go",
			src: lines(
				"package x",
				"// TestX does a thing.",
				"//",
				"// Verifies: FR-A-01/AC1, NFR-Q-02.",
				"func TestX(t *testing.T) {}",
			),
			want: []Test{{Line: 5, Name: "TestX", Tags: tags(4, "FR-A-01/AC1", "NFR-Q-02")}},
		},
		{
			name: "Go test with two Verifies lines",
			file: "x_test.go",
			src: lines(
				"package x",
				"// Verifies: FR-A-01/AC1",
				"// Verifies: FR-A-01/AC2",
				"func TestX(t *testing.T) {}",
			),
			want: []Test{{Line: 4, Name: "TestX", Tags: append(tags(2, "FR-A-01/AC1"), tags(3, "FR-A-01/AC2")...)}},
		},
		{
			name: "Go benchmark, fuzz test, and the bare Test name",
			file: "x_test.go",
			src: lines(
				"package x",
				"// Verifies: NFR-PERF-01",
				"func BenchmarkX(b *testing.B) {}",
				"// Verifies: FR-A-01/AC1",
				"func FuzzX(f *testing.F) {}",
				"// Verifies: FR-A-01/AC2",
				"func Test(t *testing.T) {}",
			),
			want: []Test{
				{Line: 3, Name: "BenchmarkX", Tags: tags(2, "NFR-PERF-01")},
				{Line: 5, Name: "FuzzX", Tags: tags(4, "FR-A-01/AC1")},
				{Line: 7, Name: "Test", Tags: tags(6, "FR-A-01/AC2")},
			},
		},
		{
			name: "Go subtests",
			file: "x_test.go",
			src: lines(
				"package x",
				"func TestTable(t *testing.T) {",
				"\t// Verifies: FR-A-01/AC1",
				`	t.Run("a case", func(t *testing.T) {})`,
				"\t// Verifies: FR-A-01/AC2",
				"\tt.Run(`raw`, func(t *testing.T) {})",
				"}",
			),
			want: []Test{
				{Line: 4, Name: "TestTable/a_case", Tags: tags(3, "FR-A-01/AC1")},
				{Line: 6, Name: "TestTable/raw", Tags: tags(5, "FR-A-01/AC2")},
			},
		},
		{
			name: "nested Go subtests",
			file: "x_test.go",
			src: lines(
				"package x",
				"func TestTable(t *testing.T) {",
				`	t.Run("outer", func(t *testing.T) {`,
				"\t\t// Verifies: FR-A-01/AC1",
				`		t.Run("inner case", func(t *testing.T) {})`,
				"\t})",
				"\t// Verifies: FR-A-01/AC2",
				`	t.Run("sibling", func(t *testing.T) {})`,
				"}",
				"// Verifies: NFR-PERF-01",
				"func TestNext(t *testing.T) {}",
			),
			want: []Test{
				{Line: 5, Name: "TestTable/outer/inner_case", Tags: tags(4, "FR-A-01/AC1")},
				{Line: 8, Name: "TestTable/sibling", Tags: tags(7, "FR-A-01/AC2")},
				{Line: 11, Name: "TestNext", Tags: tags(10, "NFR-PERF-01")},
			},
		},
		{
			name: "Go subtest outside a test function",
			file: "x_test.go",
			src: lines(
				"package x",
				"func TestA(t *testing.T) {}",
				"func (s *suite) run(t *testing.T) {",
				"\t// Verifies: FR-A-01/AC1",
				`	t.Run("a case", func(t *testing.T) {})`,
				"}",
				"func TestB(t *testing.T) {}",
				"",
				"var helper = func(t *testing.T) {",
				"\t// Verifies: FR-A-01/AC1",
				`	t.Run("a case", func(t *testing.T) {})`,
				"}",
				"func TestC(",
				"\tt *testing.T,",
				") {",
				"\t// Verifies: FR-A-01/AC2",
				`	t.Run("c case", func(t *testing.T) {})`,
				"}",
			),
			want: []Test{{Line: 17, Name: "TestC/c_case", Tags: tags(16, "FR-A-01/AC2")}},
			problems: []string{
				"x_test.go:4: a Verifies comment that is not directly above a test declaration",
				"x_test.go:10: a Verifies comment that is not directly above a test declaration",
			},
		},
		{
			name: "Go raw strings, whose lines are text",
			file: "x_test.go",
			src: lines(
				"package x",
				"func TestRaw(t *testing.T) {",
				`	t.Run("outer", func(t *testing.T) {`,
				"\t\tsrc := `",
				"func helper() {}",
				"// Verifies: FR-A-01/AC1",
				"func TestFixture(t *testing.T) {}",
				"`",
				"\t\tquote, tick := '\"', \"`\"",
				"\t\t// Verifies: FR-A-01/AC2",
				`		t.Run("inner", func(t *testing.T) {})`,
				"\t})",
				"}",
			),
			want: []Test{{Line: 11, Name: "TestRaw/outer/inner", Tags: tags(10, "FR-A-01/AC2")}},
		},
		{
			name: "Go subtest names with other space and non-printable characters",
			file: "x_test.go",
			src: lines(
				"package x",
				"func TestNames(t *testing.T) {",
				"\t// Verifies: FR-A-01/AC1",
				`	t.Run("tab\there, line\nbreak and\x01bell", func(t *testing.T) {})`,
				"}",
			),
			want: []Test{{Line: 4, Name: `TestNames/tab_here,_line_break_and\x01bell`, Tags: tags(3, "FR-A-01/AC1")}},
		},
		{
			name: "Go line directives, which do not move what the scan reads",
			file: "x_test.go",
			src: lines(
				"package x",
				"//line generated.tmpl:100",
				"// Verifies: FR-A-01/AC1",
				"func TestX(t *testing.T) {",
				"\t/*line generated.tmpl:1:500*/ x := 1 // a note",
				"\t// Verifies: FR-A-01/AC2",
				`	t.Run("case", func(t *testing.T) {})`,
				"}",
			),
			want: []Test{
				{Line: 4, Name: "TestX", Tags: tags(3, "FR-A-01/AC1")},
				{Line: 7, Name: "TestX/case", Tags: tags(6, "FR-A-01/AC2")},
			},
		},
		{
			name: "Go file that does not parse after a line directive",
			file: "x_test.go",
			src: lines(
				"package x",
				"//line generated.tmpl:100",
				"func TestX(t *testing.T) {",
			),
			problems: []string{"x_test.go:3: cannot parse the file as Go: expected '}', found 'EOF'"},
		},
		{
			name: "Go subtest in a subtest without a literal name",
			file: "x_test.go",
			src: lines(
				"package x",
				"func TestTable(t *testing.T) {",
				"\tfor _, tt := range cases {",
				"\t\tt.Run(tt.name, func(t *testing.T) {",
				"\t\t\t// Verifies: FR-A-01/AC1",
				`			t.Run("inner", func(t *testing.T) {})`,
				"\t\t})",
				"\t}",
				"}",
			),
			want: []Test{{Line: 6, Name: "TestTable/*/inner", Tags: tags(5, "FR-A-01/AC1")}},
		},
		{
			name: "Run calls that do not start a subtest",
			file: "x_test.go",
			src: lines(
				"package x",
				"func TestCLI(t *testing.T) {",
				"\t// Verifies: FR-A-01/AC1",
				"\tif err := app.Run(ctx, args); err != nil {",
				"\t\tt.Fatal(err)",
				"\t}",
				`	t.Run("outer", func(st *testing.T) {`,
				"\t\t// Verifies: FR-A-01/AC2",
				`		st.Run("inner", func(*testing.T) {})`,
				"\t})",
				"\tapp.Run(ctx, func(app *testing.T) {})",
				"}",
				"func BenchmarkX(b *testing.B) {",
				"\t// Verifies: NFR-PERF-01",
				`	b.Run("small", func(b *testing.B) {})`,
				"}",
			),
			want: []Test{
				{Line: 9, Name: "TestCLI/outer/inner", Tags: tags(8, "FR-A-01/AC2")},
				{Line: 15, Name: "BenchmarkX/small", Tags: tags(14, "NFR-PERF-01")},
			},
			problems: []string{"x_test.go:3: a Verifies comment that is not directly above a test declaration"},
		},
		{
			name: "Go TestMain and a function that is not a test",
			file: "x_test.go",
			src: lines(
				"package x",
				"// Verifies: FR-A-01/AC1",
				"func TestMain(m *testing.M) {}",
				"// Verifies: FR-A-01/AC1",
				"func Testable() {}",
				"// Verifies: FR-A-01/AC1",
				"func Testé(t *testing.T) {}",
			),
			problems: []string{
				"x_test.go:2: a Verifies comment that is not directly above a test declaration",
				"x_test.go:4: a Verifies comment that is not directly above a test declaration",
				"x_test.go:6: a Verifies comment that is not directly above a test declaration",
			},
		},
		{
			name: "Go test names after go test's rule",
			file: "x_test.go",
			src: lines(
				"package x",
				"// Verifies: FR-A-01/AC1",
				"func TestÜber(t *testing.T) {}",
				"// Verifies: FR-A-01/AC2",
				"func Test_x(t *testing.T) {}",
			),
			want: []Test{
				{Line: 3, Name: "TestÜber", Tags: tags(2, "FR-A-01/AC1")},
				{Line: 5, Name: "Test_x", Tags: tags(4, "FR-A-01/AC2")},
			},
		},
		{
			name: "blank line between the comment and the test",
			file: "x_test.go",
			src: lines(
				"package x",
				"// Verifies: FR-A-01/AC1",
				"",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{"x_test.go:2: a Verifies comment that is not directly above a test declaration"},
		},
		{
			name: "comment at the end of the file",
			file: "x_test.go",
			src:  "package x\n// Verifies: FR-A-01/AC1\n",
			problems: []string{
				"x_test.go:2: a Verifies comment that is not directly above a test declaration",
			},
		},
		{
			name: "Verifies comment after code",
			file: "x_test.go",
			src: lines(
				"package x",
				"func TestX(t *testing.T) { // Verifies: FR-A-01/AC1",
				"}",
				"func TestY(t *testing.T) { // verifies FR-A-01/AC2",
				"}",
				"func TestZ(t *testing.T) { // A note, not a tag.",
				"\tif got != want { // Verifies: FR-A-01/AC2",
				`		t.Errorf("got %v // Verifies: FR-A-01/AC2", got)`,
				"\t}",
				"\tsrc := `a /* raw ` + \"string // Verifies: FR-A-01/AC2\"",
				"}",
			),
			problems: []string{
				"x_test.go:2: a Verifies comment after code (write it on its own line directly above the test)",
				"x_test.go:4: a Verifies comment after code (write it on its own line directly above the test)",
				"x_test.go:7: a Verifies comment after code (write it on its own line directly above the test)",
			},
		},
		{
			name: "Verifies list continued on the next line",
			file: "x_test.go",
			src: lines(
				"package x",
				"// FR-A-02/AC2 is tested in y_test.go.",
				"func TestW(t *testing.T) {}",
				"// Verifies: FR-A-01/AC1, FR-A-01/AC2",
				"// FR-A-02/AC1, NFR-PERF-01",
				"func TestX(t *testing.T) {}",
			),
			want:     []Test{{Line: 6, Name: "TestX", Tags: tags(4, "FR-A-01/AC1", "FR-A-01/AC2")}},
			problems: []string{`x_test.go:5: cannot read "FR-A-02/AC1, NFR-PERF-01" as a Verifies comment (write "Verifies:" and the IDs)`},
		},
		{
			name: "Verifies list continued over two lines",
			file: "x_test.go",
			src: lines(
				"package x",
				"// Verifies: FR-A-01/AC1",
				"// FR-A-01/AC2,",
				"// NFR-PERF-01",
				"func TestX(t *testing.T) {}",
			),
			want: []Test{{Line: 5, Name: "TestX", Tags: tags(2, "FR-A-01/AC1")}},
			problems: []string{
				`x_test.go:3: cannot read "FR-A-01/AC2," as a Verifies comment (write "Verifies:" and the IDs)`,
				`x_test.go:4: cannot read "NFR-PERF-01" as a Verifies comment (write "Verifies:" and the IDs)`,
			},
		},
		{
			name: "Verifies list continued as gofmt writes an indented continuation",
			file: "x_test.go",
			src: lines(
				"package x",
				"// TestX does a thing.",
				"//",
				"// Verifies: FR-A-01/AC1, FR-A-01/AC2",
				"//",
				"//\tFR-A-02/AC1",
				"func TestX(t *testing.T) {}",
			),
			want:     []Test{{Line: 7, Name: "TestX", Tags: tags(4, "FR-A-01/AC1", "FR-A-01/AC2")}},
			problems: []string{`x_test.go:6: cannot read "FR-A-02/AC1" as a Verifies comment (write "Verifies:" and the IDs)`},
		},
		{
			name: "Verifies list continued as list items",
			file: "x.test.ts",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				"// - FR-A-01/AC2",
				"//   * NFR-PERF-01.",
				`test("x", () => {});`,
			),
			want: []Test{{Line: 4, Name: "x", Tags: tags(1, "FR-A-01/AC1")}},
			problems: []string{
				`x.test.ts:2: cannot read "- FR-A-01/AC2" as a Verifies comment (write "Verifies:" and the IDs)`,
				`x.test.ts:3: cannot read "* NFR-PERF-01." as a Verifies comment (write "Verifies:" and the IDs)`,
			},
		},
		{
			name: "IDs without Verifies above a test, and apart from one",
			file: "x_test.go",
			src: lines(
				"package x",
				"// FR-A-01/AC1, FR-A-01/AC2",
				"func TestX(t *testing.T) {}",
				"",
				"// FR-A-02",
				"",
				"// TestY is in the FR-A-02 section above.",
				"func TestY(t *testing.T) {}",
			),
			problems: []string{`x_test.go:2: cannot read "FR-A-01/AC1, FR-A-01/AC2" as a Verifies comment (write "Verifies:" and the IDs)`},
		},
		{
			name: "prose that starts with an ID, not on the line after a Verifies comment",
			file: "x_test.go",
			src: lines(
				"// FR-A-02/AC1 is tested in y_test.go.",
				"package x",
				"// Verifies: FR-A-01/AC1",
				"//",
				"// FR-A-02/AC1 is tested in y_test.go.",
				"func TestX(t *testing.T) {}",
			),
			want: []Test{{Line: 6, Name: "TestX", Tags: tags(3, "FR-A-01/AC1")}},
		},
		{
			name: "requirement without an acceptance criterion",
			file: "x_test.go",
			src: lines(
				"package x",
				"// Verifies: FR-A-01",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{"x_test.go:2: Verifies names FR-A-01 without an acceptance criterion (write FR-A-01/AC1)"},
		},
		{
			name: "unreadable entry among readable ones",
			file: "x_test.go",
			src: lines(
				"package x",
				"// Verifies: FR-A-01/AC1, the first one, fr-a-01/ac2",
				"func TestX(t *testing.T) {}",
			),
			want: []Test{{Line: 3, Name: "TestX", Tags: tags(2, "FR-A-01/AC1")}},
			problems: []string{
				`x_test.go:2: cannot read "the first one" in a Verifies comment as a requirement (write FR-…/ACn or NFR-…)`,
				`x_test.go:2: cannot read "fr-a-01/ac2" in a Verifies comment as a requirement (write FR-…/ACn or NFR-…)`,
			},
		},
		{
			name: "Verifies comment naming nothing",
			file: "x_test.go",
			src: lines(
				"package x",
				"// Verifies:",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{"x_test.go:2: a Verifies comment that names no requirement"},
		},
		{
			name:    "Verifies comment outside a test file",
			file:    "x.go",
			notTest: true,
			src: lines(
				"package x",
				"// Verifies: FR-A-01/AC1",
				"func TestX(t *testing.T) {}",
				"var x = 1 // Verifies: FR-A-01/AC2",
			),
			problems: []string{
				"x.go:2: a Verifies comment outside a test file (tests are in *_test.go, *.test.ts, *.spec.ts, *.bench.ts, or test_*.gd files)",
				"x.go:4: a Verifies comment outside a test file (tests are in *_test.go, *.test.ts, *.spec.ts, *.bench.ts, or test_*.gd files)",
			},
		},
		{
			name:    "Verifies comments in a script that is not a test file",
			file:    "helpers.tsx",
			notTest: true,
			src: lines(
				"/* Verifies: FR-A-01/AC1 */",
				"// Verifies: FR-A-01/AC2",
				`export const x = "(";`,
			),
			problems: []string{
				"helpers.tsx:1: a Verifies comment in a block comment (write it as a // line comment directly above the test)",
				"helpers.tsx:2: a Verifies comment outside a test file (tests are in *_test.go, *.test.ts, *.spec.ts, *.bench.ts, or test_*.gd files)",
			},
		},
		{
			name:    "Verifies comment in a GDScript file that is not a test file",
			file:    "helper.gd",
			notTest: true,
			src: lines(
				"# Verifies: FR-A-01/AC1",
				"func test_x():",
			),
			problems: []string{"helper.gd:1: a Verifies comment outside a test file (tests are in *_test.go, *.test.ts, *.spec.ts, *.bench.ts, or test_*.gd files)"},
		},
		{
			name:    "misspelled Verifies outside a test file, which is prose",
			file:    "x.go",
			notTest: true,
			src: lines(
				"package x",
				"// verifies FR-A-01 input rules",
				"func f() {}",
			),
		},
		{
			name: "Verifies in a block comment",
			file: "x_test.go",
			src: lines(
				"package x",
				"/*",
				" * Verifies: FR-A-01/AC1",
				" */",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{"x_test.go:3: a Verifies comment in a block comment (write it as a // line comment directly above the test)"},
		},
		{
			name: "Verifies in a Go block comment without stars",
			file: "x_test.go",
			src: lines(
				"package x",
				"/*",
				"Verifies: FR-A-01/AC1",
				"*/",
				"func TestX(t *testing.T) {}",
				"/* Verifies: FR-A-01/AC2",
				"*/",
				"func TestY(t *testing.T) {}",
			),
			problems: []string{
				"x_test.go:3: a Verifies comment in a block comment (write it as a // line comment directly above the test)",
				"x_test.go:6: a Verifies comment in a block comment (write it as a // line comment directly above the test)",
			},
		},
		{
			name: "block comment that opens after code",
			file: "x_test.go",
			src: lines(
				"package x",
				"var x = 1 /* a note",
				" * Verifies: FR-A-01/AC1",
				" */",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{"x_test.go:3: a Verifies comment in a block comment (write it as a // line comment directly above the test)"},
		},
		{
			name: "block comment between the comment and the test",
			file: "x_test.go",
			src: lines(
				"package x",
				"// Verifies: FR-A-01/AC1",
				"/* A note. */",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{"x_test.go:2: a Verifies comment that is not directly above a test declaration"},
		},
		{
			name: "a glob and a Verifies field are code",
			file: "x_test.go",
			src: lines(
				"package x",
				"func TestA(t *testing.T) {",
				`	_, _ = filepath.Glob("testdata/*.go")`,
				"\tentry := TestEntry{",
				"\t\tVerifies: []string{},",
				"\t}",
				"\t// Verifies: FR-A-01/AC1",
				`	t.Run("after", func(t *testing.T) {})`,
				"}",
			),
			want: []Test{{Line: 8, Name: "TestA/after", Tags: tags(7, "FR-A-01/AC1")}},
		},
		{
			name: "misspelled Verifies comment",
			file: "x_test.go",
			src: lines(
				"package x",
				"// verifies: FR-A-01/AC1",
				"// Verifies FR-A-01/AC2",
				"// Verified: NFR-PERF-01",
				"// Verifies — FR-A-01/AC1",
				"// @verifies (NFR-PERF-01)",
				"// Verifies that a moved node keeps its connections.",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{
				`x_test.go:2: cannot read "verifies: FR-A-01/AC1" as a Verifies comment (write "Verifies:" and the IDs)`,
				`x_test.go:3: cannot read "Verifies FR-A-01/AC2" as a Verifies comment (write "Verifies:" and the IDs)`,
				`x_test.go:4: cannot read "Verified: NFR-PERF-01" as a Verifies comment (write "Verifies:" and the IDs)`,
				`x_test.go:5: cannot read "Verifies — FR-A-01/AC1" as a Verifies comment (write "Verifies:" and the IDs)`,
				`x_test.go:6: cannot read "@verifies (NFR-PERF-01)" as a Verifies comment (write "Verifies:" and the IDs)`,
			},
		},
		{
			name: "Verifies comment with a typo",
			file: "x_test.go",
			src: lines(
				"package x",
				"// Verifes: FR-A-01/AC1",
				"// Verfies: FR-A-01/AC2",
				"// VERIFEIS FR-A-01/AC1",
				"// Verification: NFR-PERF-01",
				"// Covers FR-A-01/AC1, which the test above verifies.",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{
				`x_test.go:2: cannot read "Verifes: FR-A-01/AC1" as a Verifies comment (write "Verifies:" and the IDs)`,
				`x_test.go:3: cannot read "Verfies: FR-A-01/AC2" as a Verifies comment (write "Verifies:" and the IDs)`,
				`x_test.go:4: cannot read "VERIFEIS FR-A-01/AC1" as a Verifies comment (write "Verifies:" and the IDs)`,
				`x_test.go:5: cannot read "Verification: NFR-PERF-01" as a Verifies comment (write "Verifies:" and the IDs)`,
			},
		},
		{
			name: "CRLF line endings and a byte order mark",
			file: "x_test.go",
			src:  "\xef\xbb\xbfpackage x\r\n// Verifies: FR-A-01/AC1\r\nfunc TestX(t *testing.T) {}\r\n",
			want: []Test{{Line: 3, Name: "TestX", Tags: tags(2, "FR-A-01/AC1")}},
		},
		{
			name: "comment that only mentions Verifies",
			file: "x_test.go",
			src: lines(
				"package x",
				"// It Verifies: FR-A-01/AC1",
				"func TestX(t *testing.T) {}",
			),
		},

		// Vitest and Playwright.
		{
			name: "Vitest and Playwright declarations",
			file: "x.test.ts",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				`test("double", () => {});`,
				"// Verifies: FR-A-01/AC1",
				`  it('single', () => {});`,
				"// Verifies: FR-A-01/AC1",
				"test(`template`, () => {});",
				"// Verifies: FR-A-01/AC1",
				`test.describe.serial("group", () => {});`,
				"// Verifies: FR-A-01/AC1",
				`it.only("focused", () => {});`,
				"// Verifies: FR-A-01/AC1",
				`describe("suite", () => {});`,
				"// Verifies: FR-A-01/AC1",
				`test("a \"quoted\" \\ title", () => {});`,
			),
			want: []Test{
				{Line: 2, Name: "double", Tags: tags(1, "FR-A-01/AC1")},
				{Line: 4, Name: "single", Tags: tags(3, "FR-A-01/AC1")},
				{Line: 6, Name: "template", Tags: tags(5, "FR-A-01/AC1")},
				{Line: 8, Name: "group", Tags: tags(7, "FR-A-01/AC1")},
				{Line: 10, Name: "focused", Tags: tags(9, "FR-A-01/AC1")},
				{Line: 12, Name: "suite", Tags: tags(11, "FR-A-01/AC1")},
				{Line: 14, Name: `a "quoted" \ title`, Tags: tags(13, "FR-A-01/AC1")},
			},
		},
		{
			name: "escapes in titles",
			file: "x.test.ts",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				`it("caf\u00e9 \x41\u{1F600}\uD83D\uDE00 \r\0", () => {});`,
				"// Verifies: FR-A-01/AC2",
				"it(`a\\` tick \\u{e9}`, () => {});",
				"// Verifies: FR-A-01/AC1",
				`it("one \`,
				`line", () => {});`,
			),
			want: []Test{
				{Line: 2, Name: "café A\U0001F600\U0001F600 \r\x00", Tags: tags(1, "FR-A-01/AC1")},
				{Line: 4, Name: "a` tick é", Tags: tags(3, "FR-A-01/AC2")},
				{Line: 6, Name: "one line", Tags: tags(5, "FR-A-01/AC1")},
			},
		},
		{
			name: "anonymous Playwright group",
			file: "x.spec.ts",
			src: lines(
				"test.describe(() => {",
				"  // Verifies: FR-A-01/AC1",
				`  test("inside", async () => {});`,
				"});",
				"test.describe.skip(async () => {",
				"  // Verifies: FR-A-01/AC2",
				`  test("inside a skipped one", async () => {});`,
				"});",
			),
			want: []Test{
				{Line: 3, Name: "inside", Tags: tags(2, "FR-A-01/AC1")},
				{Line: 7, Name: "inside a skipped one", Skipped: true, Tags: tags(6, "FR-A-01/AC2")},
			},
		},
		{
			name: "skipped Vitest and Playwright tests",
			file: "x.spec.ts",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				`it.skip("skipped", () => {});`,
				"// Verifies: FR-A-01/AC1",
				`test.todo("to do");`,
				"// Verifies: FR-A-01/AC1",
				`test.fixme("to fix", async () => {});`,
				"// Verifies: FR-A-01/AC1",
				`test.describe.skip("skipped group", () => {});`,
			),
			want: []Test{
				{Line: 2, Name: "skipped", Skipped: true, Tags: tags(1, "FR-A-01/AC1")},
				{Line: 4, Name: "to do", Skipped: true, Tags: tags(3, "FR-A-01/AC1")},
				{Line: 6, Name: "to fix", Skipped: true, Tags: tags(5, "FR-A-01/AC1")},
				{Line: 8, Name: "skipped group", Skipped: true, Tags: tags(7, "FR-A-01/AC1")},
			},
		},
		{
			name: "tests inside a skipped group",
			file: "x.test.ts",
			src: lines(
				`describe.skip("group", () => {`,
				"  // Verifies: FR-A-01/AC1",
				`  it("inside", () => {});`,
				"",
				`  describe("nested", () => {`,
				"    // Verifies: FR-A-01/AC2",
				`    it("deeper", () => {});`,
				"  });",
				"});",
				"// Verifies: FR-A-01/AC1",
				`it("after", () => {});`,
				`it.todo("one line");`,
				"// Verifies: FR-A-01/AC2",
				`it("next", () => {});`,
			),
			want: []Test{
				{Line: 3, Name: "group > inside", Skipped: true, Tags: tags(2, "FR-A-01/AC1")},
				{Line: 7, Name: "group > nested > deeper", Skipped: true, Tags: tags(6, "FR-A-01/AC2")},
				{Line: 11, Name: "after", Tags: tags(10, "FR-A-01/AC1")},
				{Line: 14, Name: "next", Tags: tags(13, "FR-A-01/AC2")},
			},
		},
		{
			name: "groups whose tests are all skipped",
			file: "x.spec.ts",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				`describe("to do", () => {`,
				`  it.todo("keeps connections");`,
				`  it.skip("keeps metadata", () => {});`,
				`  describe("nested", () => {`,
				`    it.fails("is a known bug", () => {});`,
				"  });",
				`  describe.skip("skipped", () => {`,
				`    it("never runs", () => {});`,
				"  });",
				"  beforeEach(() => {});",
				"});",
				"// Verifies: FR-A-01/AC2",
				`test.describe("one test runs", () => {`,
				`  test.fixme("to fix", async () => {});`,
				`  test.describe("nested", () => {`,
				`    test("runs", async () => {});`,
				"  });",
				"});",
				"// Verifies: FR-A-01/AC2",
				`describe("tests a helper declares", () => {`,
				"  storeContract(makeStore);",
				"});",
				"// Verifies: FR-A-01/AC2",
				`describe("tests in a loop and a table", () => {`,
				`  it.skip("not this one", () => {});`,
				"  for (const c of cases) {",
				"    it(c.name, () => {});",
				"  }",
				`  it.each([1, 2])("case %i", () => {});`,
				"});",
				"// Verifies: FR-A-01/AC1",
				`test.describe("a runtime skip", () => {`,
				`  test.skip(({ browserName }) => browserName === "webkit", "a reason");`,
				"  // Verifies: FR-A-01/AC2",
				`  test("runs where it is not skipped", async () => {`,
				`    test.skip(isMobile, "not on mobile");`,
				"  });",
				"});",
			),
			want: []Test{
				{Line: 2, Name: "to do", Skipped: true, Tags: tags(1, "FR-A-01/AC1")},
				{Line: 14, Name: "one test runs", Tags: tags(13, "FR-A-01/AC2")},
				{Line: 21, Name: "tests a helper declares", Tags: tags(20, "FR-A-01/AC2")},
				{Line: 25, Name: "tests in a loop and a table", Tags: tags(24, "FR-A-01/AC2")},
				{Line: 33, Name: "a runtime skip", Tags: tags(32, "FR-A-01/AC1")},
				{Line: 36, Name: "a runtime skip > runs where it is not skipped", Tags: tags(35, "FR-A-01/AC2")},
			},
		},
		{
			name: "Vitest declarations with a condition",
			file: "x.test.ts",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				`describe.skipIf(process.env.CI)("group", () => {`,
				"  // Verifies: FR-A-01/AC2",
				`  test.runIf(isLinux())("on Linux", () => {});`,
				"});",
				"// Verifies: FR-A-01/AC1",
				"test.skipIf(",
				"  isWindows,",
				`)("not on Windows", () => {});`,
				"// Verifies: FR-A-01/AC2",
				`it.concurrent.skipIf(isWindows)("concurrently", () => {});`,
				`describe.skip("skipped", () => {`,
				"  // Verifies: FR-A-01/AC2",
				`  it.runIf(isLinux)("inside", () => {});`,
				"});",
			),
			want: []Test{
				{Line: 2, Name: "group", Tags: tags(1, "FR-A-01/AC1")},
				{Line: 4, Name: "group > on Linux", Tags: tags(3, "FR-A-01/AC2")},
				{Line: 7, Name: "not on Windows", Tags: tags(6, "FR-A-01/AC1")},
				{Line: 11, Name: "concurrently", Tags: tags(10, "FR-A-01/AC2")},
				{Line: 14, Name: "skipped > inside", Skipped: true, Tags: tags(13, "FR-A-01/AC2")},
			},
		},
		{
			name: "Vitest options that skip a declaration",
			file: "x.test.ts",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				`test("skipped by an option", { skip: true }, () => {});`,
				"// Verifies: FR-A-01/AC1",
				`it("to do", { todo: true });`,
				"// Verifies: FR-A-01/AC1",
				`test("a known bug", { timeout: 100, fails: true, }, () => {});`,
				"// Verifies: FR-A-01/AC1",
				`test("options last", () => {}, { skip: true });`,
				`describe("skipped group", { skip: true }, () => {`,
				"  // Verifies: FR-A-01/AC2",
				`  it("inside", () => {});`,
				"});",
				"// Verifies: FR-A-01/AC2",
				`test("skipped on CI", { skip: isCI }, () => {});`,
				"// Verifies: FR-A-01/AC2",
				`test("not skipped", { retry: 3, skip: false }, () => {});`,
				"// Verifies: FR-A-01/AC2",
				`test("an object in the body", ({ skip }) => {`,
				"  run({ skip: true });",
				"  const options = { todo: true };",
				"});",
				"// Verifies: FR-A-01/AC2",
				`test("an option that is an expression", { skip: true && isCI }, () => {});`,
			),
			want: []Test{
				{Line: 2, Name: "skipped by an option", Skipped: true, Tags: tags(1, "FR-A-01/AC1")},
				{Line: 4, Name: "to do", Skipped: true, Tags: tags(3, "FR-A-01/AC1")},
				{Line: 6, Name: "a known bug", Skipped: true, Tags: tags(5, "FR-A-01/AC1")},
				{Line: 8, Name: "options last", Skipped: true, Tags: tags(7, "FR-A-01/AC1")},
				{Line: 11, Name: "skipped group > inside", Skipped: true, Tags: tags(10, "FR-A-01/AC2")},
				{Line: 14, Name: "skipped on CI", Tags: tags(13, "FR-A-01/AC2")},
				{Line: 16, Name: "not skipped", Tags: tags(15, "FR-A-01/AC2")},
				{Line: 18, Name: "an object in the body", Tags: tags(17, "FR-A-01/AC2")},
				{Line: 23, Name: "an option that is an expression", Tags: tags(22, "FR-A-01/AC2")},
			},
		},
		{
			name: "template literal in a skipped group",
			file: "x.test.ts",
			src: lines(
				`describe.skip("group", () => {`,
				"  const src = `",
				"text at the margin",
				"`;",
				"  // Verifies: FR-A-01/AC1",
				`  it("inside", () => {});`,
				"});",
				"// Verifies: FR-A-01/AC2",
				`it("after", () => {});`,
			),
			want: []Test{
				{Line: 6, Name: "group > inside", Skipped: true, Tags: tags(5, "FR-A-01/AC1")},
				{Line: 9, Name: "after", Tags: tags(8, "FR-A-01/AC2")},
			},
		},
		{
			name: "Vitest suite, which is describe",
			file: "x.test.ts",
			src: lines(
				`suite.skip("group", () => {`,
				"  // Verifies: FR-A-01/AC1",
				`  it("inside", () => {});`,
				"});",
				"// Verifies: FR-A-01/AC2",
				`suite("next", () => {});`,
			),
			want: []Test{
				{Line: 3, Name: "group > inside", Skipped: true, Tags: tags(2, "FR-A-01/AC1")},
				{Line: 6, Name: "next", Tags: tags(5, "FR-A-01/AC2")},
			},
		},
		{
			name: "Verifies comment after Vitest code",
			file: "x.test.ts",
			src: lines(
				`test("x // y", () => {}); // Verifies: FR-A-01/AC1`,
				`it("loads https://example.com", () => {`,
				"  expect(1).toBe(1); // Verifies: FR-A-01/AC2",
				"  expect(`a ${'//'} b`).toBe(\"// Verifies: FR-A-01/AC2\");",
				"});",
			),
			problems: []string{
				"x.test.ts:1: a Verifies comment after code (write it on its own line directly above the test)",
				"x.test.ts:3: a Verifies comment after code (write it on its own line directly above the test)",
			},
		},
		{
			name: "expected failures, hooks, and modifier chains",
			file: "x.spec.ts",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				`test.fail("known bug", async () => {});`,
				"// Verifies: FR-A-01/AC1",
				`it.fails("known bug too", () => {});`,
				"// Verifies: FR-A-01/AC1",
				`test.beforeEach("log in", async () => {});`,
				"// Verifies: FR-A-01/AC1",
				`test.describe.parallel.only("focused group", () => {});`,
			),
			want: []Test{
				{Line: 2, Name: "known bug", Skipped: true, Tags: tags(1, "FR-A-01/AC1")},
				{Line: 4, Name: "known bug too", Skipped: true, Tags: tags(3, "FR-A-01/AC1")},
				{Line: 8, Name: "focused group", Tags: tags(7, "FR-A-01/AC1")},
			},
			problems: []string{"x.spec.ts:5: a Verifies comment that is not directly above a test declaration"},
		},
		{
			name: "declarations that Biome splits across lines",
			file: "x.spec.ts",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				"test.describe",
				`  .serial("group", () => {`,
				"    // Verifies: FR-A-01/AC2",
				`    test("inside", async () => {});`,
				"  });",
				"test.describe",
				`  .skip("skipped group", () => {`,
				"    // Verifies: FR-A-01/AC1",
				`    test("skipped inside", async () => {});`,
				"  });",
				"// Verifies: FR-A-01/AC2",
				"test.fail(",
				`  "a known bug whose title is long",`,
				"  async () => {},",
				");",
				"// Verifies: FR-A-01/AC2",
				"it(",
				`  "a test whose title is long",`,
				"  () => {},",
				");",
			),
			want: []Test{
				{Line: 2, Name: "group", Tags: tags(1, "FR-A-01/AC1")},
				{Line: 5, Name: "group > inside", Tags: tags(4, "FR-A-01/AC2")},
				{Line: 10, Name: "skipped group > skipped inside", Skipped: true, Tags: tags(9, "FR-A-01/AC1")},
				{Line: 13, Name: "a known bug whose title is long", Skipped: true, Tags: tags(12, "FR-A-01/AC2")},
				{Line: 18, Name: "a test whose title is long", Tags: tags(17, "FR-A-01/AC2")},
			},
		},
		{
			name: "skipped groups without a literal title",
			file: "x.test.ts",
			src: lines(
				`describe.skip.each([1, 2])("case %i", () => {`,
				"  // Verifies: FR-A-01/AC1",
				`  it("inside", () => {});`,
				"});",
				"describe.skip(Widget.name, () => {",
				"  // Verifies: FR-A-01/AC1",
				`  it("inside too", () => {});`,
				"});",
				`test.describe("group", () => {`,
				`  test.skip(({ browserName }) => browserName === "webkit", "a reason");`,
				"  // Verifies: FR-A-01/AC2",
				`  test("runs where it is not skipped", async () => {});`,
				"});",
			),
			want: []Test{
				{Line: 3, Name: "* > inside", Skipped: true, Tags: tags(2, "FR-A-01/AC1")},
				{Line: 7, Name: "* > inside too", Skipped: true, Tags: tags(6, "FR-A-01/AC1")},
				{Line: 12, Name: "group > runs where it is not skipped", Tags: tags(11, "FR-A-01/AC2")},
			},
		},
		{
			name: "backticks in strings and comments of a skipped group",
			file: "x.test.ts",
			src: lines(
				`describe.skip("group", () => {`,
				"  const tick = \"`\";",
				"  const md = `a \\` and a \"`;",
				"});",
				"// Verifies: FR-A-01/AC1",
				`it("after", () => {});`,
				`describe.skip("another group", () => {`,
				"  press(); // the ` key",
				"});",
				"// Verifies: FR-A-01/AC2",
				`it("after another", () => {});`,
			),
			want: []Test{
				{Line: 6, Name: "after", Tags: tags(5, "FR-A-01/AC1")},
				{Line: 11, Name: "after another", Tags: tags(10, "FR-A-01/AC2")},
			},
		},
		{
			name: "Vitest benchmark file",
			file: "x.bench.ts",
			src: lines(
				"// Verifies: NFR-PERF-01",
				`bench("speed", () => {});`,
				`describe("group", () => {`,
				"  // Verifies: NFR-PERF-01",
				`  bench("grouped", () => {});`,
				"});",
				"// Verifies: FR-A-01/AC1",
				`test("never run by the test run", () => {});`,
				"// Verifies: NFR-PERF-01",
				`bench.skip("skipped", () => {});`,
				"// Verifies: NFR-PERF-01",
				`bench.todo("to do");`,
				`describe.skip("skipped group", () => {`,
				"  // Verifies: NFR-PERF-01",
				`  bench.only("in a skipped group", () => {});`,
				"});",
			),
			want: []Test{
				{Line: 2, Name: "speed", Tags: tags(1, "NFR-PERF-01")},
				{Line: 5, Name: "group > grouped", Tags: tags(4, "NFR-PERF-01")},
				{Line: 10, Name: "skipped", Skipped: true, Tags: tags(9, "NFR-PERF-01")},
				{Line: 12, Name: "to do", Skipped: true, Tags: tags(11, "NFR-PERF-01")},
				{Line: 15, Name: "skipped group > in a skipped group", Skipped: true, Tags: tags(14, "NFR-PERF-01")},
			},
			problems: []string{"x.bench.ts:7: a Verifies comment that is not directly above a test declaration"},
		},
		{
			name: "Vitest benchmark in a test file",
			file: "x.test.ts",
			src: lines(
				"// Verifies: NFR-PERF-01",
				`bench("only in the benchmark run", () => {});`,
			),
			problems: []string{"x.test.ts:1: a Verifies comment that is not directly above a test declaration"},
		},
		{
			name: "declarations the scan cannot name",
			file: "x.test.tsx",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				`test.each([1, 2])("case %i", (n) => {});`,
				"// Verifies: FR-A-01/AC1",
				"test(title, () => {});",
				"// Verifies: FR-A-01/AC1",
				"expect(1).toBe(1);",
				"// Verifies: FR-A-01/AC1",
				`it("handles " + kind, () => {});`,
			),
			problems: []string{
				"x.test.tsx:1: a Verifies comment that is not directly above a test declaration",
				"x.test.tsx:3: a Verifies comment that is not directly above a test declaration",
				"x.test.tsx:5: a Verifies comment that is not directly above a test declaration",
				"x.test.tsx:7: a Verifies comment that is not directly above a test declaration",
			},
		},
		{
			name: "groups with a tagged template table",
			file: "x.test.ts",
			src: lines(
				"describe.skip.each`",
				"  a    | b",
				"  ${1} | ${`two ${2}`}",
				"`(\"adds $a\", ({ a, b }) => {",
				"  // Verifies: FR-A-01/AC1",
				`  it("inside", () => {});`,
				"});",
				"describe.each`",
				"  a",
				"  1",
				"`(\"group\", () => {",
				"  // Verifies: FR-A-01/AC2",
				`  it("in a table group", () => {});`,
				"});",
				"// Verifies: FR-A-01/AC1",
				`it("after", () => {});`,
			),
			want: []Test{
				{Line: 6, Name: "* > inside", Skipped: true, Tags: tags(5, "FR-A-01/AC1")},
				{Line: 13, Name: "* > in a table group", Tags: tags(12, "FR-A-01/AC2")},
				{Line: 16, Name: "after", Tags: tags(15, "FR-A-01/AC1")},
			},
		},
		{
			name: "JSDoc comment",
			file: "x.test.ts",
			src: lines(
				"/** Verifies: FR-A-01/AC1 */",
				`test("x", () => {});`,
			),
			problems: []string{"x.test.ts:1: a Verifies comment in a block comment (write it as a // line comment directly above the test)"},
		},

		{
			name: "regular expressions, nested templates, and division",
			file: "x.test.ts",
			src: lines(
				"const half = total / 2; // a note",
				"expect(s).toMatch(/it's \\/\\/ a `tick` \"/);",
				"const t = `a ${x ? `b ${y}` : { c: \"`\" }.c} d`;",
				"const u = `${/`/.source}`;",
				"const v = `${x} and ${/`/.source}`;",
				"// Verifies: FR-A-01/AC1",
				`it("after them", () => {});`,
			),
			want: []Test{{Line: 7, Name: "after them", Tags: tags(6, "FR-A-01/AC1")}},
		},
		{
			name: "division after a non-null assertion",
			file: "x.test.ts",
			src: lines(
				"const ratio = total! / count; // it's `odd",
				"const width = el.clientWidth! / 2; // Verifies: FR-A-01/AC2",
				"if (!/^a'/.test(s)) {}",
				"// Verifies: FR-A-01/AC1",
				`it("after it", () => {});`,
			),
			want:     []Test{{Line: 5, Name: "after it", Tags: tags(4, "FR-A-01/AC1")}},
			problems: []string{"x.test.ts:2: a Verifies comment after code (write it on its own line directly above the test)"},
		},
		{
			name: "slash and a backslash before a line break",
			file: "x.test.tsx",
			src: lines(
				`describe.skip("group", () => {`,
				`  render(<p>a, /b\`,
				"  </p>);",
				"});",
				"// Verifies: FR-A-01/AC1",
				`it("after it", () => {});`,
			),
			want: []Test{{Line: 6, Name: "after it", Tags: tags(5, "FR-A-01/AC1")}},
		},
		{
			name: "JSX text with an apostrophe",
			file: "x.test.tsx",
			src: lines(
				`describe.skip("group", () => {`,
				`  it("renders", () => {`,
				"    render(<p>Don't panic</p>);",
				"  });",
				"});",
				"// Verifies: FR-A-01/AC1",
				`it("after it", () => {});`,
			),
			want: []Test{{Line: 7, Name: "after it", Tags: tags(6, "FR-A-01/AC1")}},
		},
		{
			name: "parentheses that cannot be paired",
			file: "x.test.tsx",
			src: lines(
				`describe.skip("group", () => {`,
				`  it("links", () => {`,
				"    render(<p>See https://example.com</p>);",
				"  });",
				"});",
				"// Verifies: FR-A-01/AC1",
				`it("after it", () => {});`,
			),
			want:     []Test{{Line: 7, Name: "group > after it", Skipped: true, Tags: tags(6, "FR-A-01/AC1")}},
			problems: []string{`x.test.tsx:1: cannot pair this parenthesis, so the scan cannot tell which groups the tests below it are in (a parenthesis without its partner, a quote, or "//" in JSX text can cause this)`},
		},
		{
			name: "JSX closing tags, which are not regular expressions",
			file: "x.test.tsx",
			src: lines(
				`describe.skip("group", () => {`,
				`  it("renders", () => {`,
				"    render(<p>Total <b>$5</b> (incl. <i>tax</i>)</p>);",
				"    render(<p><b>x</b>(<i>y</i>)</p>);",
				"  });",
				"});",
				"// Verifies: FR-A-01/AC1",
				`it("after it", () => {});`,
				"let i = 0;",
				"const half = i++ / 2 + `a/b`;",
				"const back = i-- / 2 + `c/d`;",
				"// Verifies: FR-A-01/AC2",
				`it("after the division", () => {});`,
			),
			want: []Test{
				{Line: 8, Name: "after it", Tags: tags(7, "FR-A-01/AC1")},
				{Line: 13, Name: "after the division", Tags: tags(12, "FR-A-01/AC2")},
			},
		},
		{
			name: "JSX text with a parenthesis without its partner",
			file: "x.test.tsx",
			src: lines(
				`describe("group", () => {`,
				`  it("lists", () => {`,
				"    render(<li>a) first</li>);",
				"  });",
				"});",
				"// Verifies: FR-A-01/AC1",
				`it("after it", () => {});`,
			),
			want:     []Test{{Line: 7, Name: "after it", Tags: tags(6, "FR-A-01/AC1")}},
			problems: []string{`x.test.tsx:5: cannot pair this parenthesis, so the scan cannot tell which groups the tests below it are in (a parenthesis without its partner, a quote, or "//" in JSX text can cause this)`},
		},
		{
			name: "JSX text with a backtick",
			file: "x.test.tsx",
			src: lines(
				`it("renders a tick", () => {`,
				"  render(<kbd>`</kbd>);",
				"});",
				"// Verifies: FR-A-01/AC1",
				`it("after it", () => {});`,
			),
			problems: []string{`x.test.tsx:2: cannot find the end of the template literal or block comment that starts here, so the scan cannot read the code after it (a backtick or "/*" in JSX text can cause this)`},
		},
		{
			name: "JSX text that opens a block comment",
			file: "x.test.tsx",
			src: lines(
				`it("renders a glob", () => {`,
				"  render(<p>/*.ts</p>);",
				"});",
				"// Verifies: FR-A-01/AC1",
				`it("after it", () => {});`,
			),
			problems: []string{`x.test.tsx:2: cannot find the end of the template literal or block comment that starts here, so the scan cannot read the code after it (a backtick or "/*" in JSX text can cause this)`},
		},
		{
			name: "functions named like declarations",
			file: "x.test.ts",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				"function test(name: string) {}",
				"// Verifies: FR-A-01/AC1",
				`page.test("not a test");`,
			),
			problems: []string{
				"x.test.ts:1: a Verifies comment that is not directly above a test declaration",
				"x.test.ts:3: a Verifies comment that is not directly above a test declaration",
			},
		},
		{
			name: "Go file that does not parse",
			file: "x_test.go",
			src: lines(
				"package x",
				"func TestX(t *testing.T) {",
			),
			problems: []string{"x_test.go:2: cannot parse the file as Go: expected '}', found 'EOF'"},
		},

		// GUT.
		{
			name: "GUT tests",
			file: "test_x.gd",
			src: lines(
				"extends GutTest",
				"## Verifies: FR-A-01/AC1",
				"func test_one():",
				"\tpass",
				"class TestInner:",
				"\textends GutTest",
				"\t# Verifies: FR-A-01/AC2",
				"\tfunc test_two():",
				"\t\tpass",
			),
			want: []Test{
				{Line: 3, Name: "test_one", Tags: tags(2, "FR-A-01/AC1")},
				{Line: 8, Name: "test_two", Tags: tags(7, "FR-A-01/AC2")},
			},
		},
		{
			name: "GDScript multi-line strings",
			file: "test_x.gd",
			src: lines(
				`"""`,
				"Verifies: FR-A-01/AC1",
				`"""`,
				"func test_one():",
				`	var s = """`,
				"	text",
				`	"""`,
				`# A comment that mentions """.`,
				"# Verifies: FR-A-01/AC2",
				"func test_two():",
			),
			want:     []Test{{Line: 10, Name: "test_two", Tags: tags(9, "FR-A-01/AC2")}},
			problems: []string{"test_x.gd:2: a Verifies comment in a block comment (write it as a # line comment directly above the test)"},
		},
		{
			name: "GDScript multi-line strings in single quotes",
			file: "test_x.gd",
			src: lines(
				"'''",
				"Verifies: FR-A-01/AC1",
				"'''",
				"func test_one():",
				`	var s = '''a """ and a \''' and`,
				"# Verifies: FR-A-01/AC1",
				"func test_hidden():",
				"'''",
				"# Verifies: FR-A-01/AC2",
				"func test_two():",
			),
			want:     []Test{{Line: 10, Name: "test_two", Tags: tags(9, "FR-A-01/AC2")}},
			problems: []string{"test_x.gd:2: a Verifies comment in a block comment (write it as a # line comment directly above the test)"},
		},
		{
			name: "GDScript multi-line string that never closes",
			file: "test_x.gd",
			src: lines(
				"func test_one():",
				`	var s = """never closed`,
				"# Verifies: FR-A-01/AC1",
				"func test_two():",
			),
			problems: []string{"test_x.gd:2: cannot find the end of the multi-line string that starts here, so the scan cannot read the code after it"},
		},
		{
			name: "GDScript string continued on the next line",
			file: "test_x.gd",
			src: lines(
				"func helper():",
				"\tvar s = \"a\\",
				"b\"",
				"# Verifies: FR-A-01/AC1",
				"func test_x():",
				"\tpass",
			),
			want: []Test{{Line: 5, Name: "test_x", Tags: tags(4, "FR-A-01/AC1")}},
		},
		{
			name: "Verifies comment after GDScript code",
			file: "test_x.gd",
			src: lines(
				"func test_one(): # Verifies: FR-A-01/AC1",
				"\tassert_eq(text, \"# Verifies: FR-A-01/AC2\") # verifies FR-A-01/AC2",
			),
			problems: []string{
				"test_x.gd:1: a Verifies comment after code (write it on its own line directly above the test)",
				"test_x.gd:2: a Verifies comment after code (write it on its own line directly above the test)",
			},
		},
		{
			name: "GUT helper that is not a test",
			file: "test_x.gd",
			src: lines(
				"# Verifies: FR-A-01/AC1",
				"func before_each():",
			),
			problems: []string{"test_x.gd:1: a Verifies comment that is not directly above a test declaration"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lang := languageOf(tt.file)
			if lang == nil {
				t.Fatalf("no language for %s", tt.file)
			}
			if got := lang.isTest(tt.file); got == tt.notTest {
				t.Fatalf("isTest(%s) = %v", tt.file, got)
			}
			got, problems := scanFile(tt.file, tt.src, lang, !tt.notTest)
			for i := range tt.want {
				tt.want[i].File = tt.file
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tests =\n%+v\nwant\n%+v", got, tt.want)
			}
			var msgs []string
			for _, p := range problems {
				msgs = append(msgs, p.String())
			}
			if !slices.Equal(msgs, tt.problems) {
				t.Errorf("problems =%s\nwant\n\t%s", formatProblems(problems), strings.Join(tt.problems, "\n\t"))
			}
		})
	}
}

// TestScanLeavesOut checks which directories and files the scan reads, and
// that it reports paths with forward slashes on every OS.
func TestScanLeavesOut(t *testing.T) {
	root := t.TempDir()
	tagged := lines("package x", "// Verifies: FR-A-01/AC1", "func TestX(t *testing.T) {}", "")
	write := func(path, src string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("server/internal/a/a_test.go", tagged)
	for _, dir := range []string{"node_modules/dep", "web/app/node_modules", "server/testdata", "vendor", "web/app/dist", "coverage", ".git", "godot/.godot"} {
		write(dir+"/x_test.go", tagged)
	}
	write("docs/notes.md", tagged)      // not a source file
	write("web/app/x.test.css", tagged) // not a source file

	// The root is scanned even when its own name would be left out.
	t.Chdir(root)
	got, problems, err := Scan(".")
	if err != nil {
		t.Fatal(err)
	}
	want := []Test{{File: "server/internal/a/a_test.go", Line: 3, Name: "TestX", Tags: tags(2, "FR-A-01/AC1")}}
	if !reflect.DeepEqual(got, want) || len(problems) > 0 {
		t.Errorf("Scan = %+v, problems:%s\nwant %+v", got, formatProblems(problems), want)
	}

	if _, _, err := Scan(filepath.Join(root, "missing")); err == nil {
		t.Error("Scan of a missing directory should fail")
	}
}
