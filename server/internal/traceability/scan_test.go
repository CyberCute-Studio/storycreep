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
				"// TestX does a thing.",
				"//",
				"// Verifies: FR-A-01/AC1, NFR-Q-02.",
				"func TestX(t *testing.T) {}",
			),
			want: []Test{{Line: 4, Name: "TestX", Tags: tags(3, "FR-A-01/AC1", "NFR-Q-02")}},
		},
		{
			name: "Go test with two Verifies lines",
			file: "x_test.go",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				"// Verifies: FR-A-01/AC2",
				"func TestX(t *testing.T) {}",
			),
			want: []Test{{Line: 3, Name: "TestX", Tags: append(tags(1, "FR-A-01/AC1"), tags(2, "FR-A-01/AC2")...)}},
		},
		{
			name: "Go benchmark, fuzz test, and the bare Test name",
			file: "x_test.go",
			src: lines(
				"// Verifies: NFR-PERF-01",
				"func BenchmarkX(b *testing.B) {}",
				"// Verifies: FR-A-01/AC1",
				"func FuzzX(f *testing.F) {}",
				"// Verifies: FR-A-01/AC2",
				"func Test(t *testing.T) {}",
			),
			want: []Test{
				{Line: 2, Name: "BenchmarkX", Tags: tags(1, "NFR-PERF-01")},
				{Line: 4, Name: "FuzzX", Tags: tags(3, "FR-A-01/AC1")},
				{Line: 6, Name: "Test", Tags: tags(5, "FR-A-01/AC2")},
			},
		},
		{
			name: "Go subtests",
			file: "x_test.go",
			src: lines(
				"func TestTable(t *testing.T) {",
				"\t// Verifies: FR-A-01/AC1",
				`	t.Run("a case", func(t *testing.T) {})`,
				"\t// Verifies: FR-A-01/AC2",
				"\tt.Run(`raw`, func(t *testing.T) {})",
				"}",
			),
			want: []Test{
				{Line: 3, Name: "TestTable/a_case", Tags: tags(2, "FR-A-01/AC1")},
				{Line: 5, Name: "TestTable/raw", Tags: tags(4, "FR-A-01/AC2")},
			},
		},
		{
			name: "nested Go subtests",
			file: "x_test.go",
			src: lines(
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
				{Line: 4, Name: "TestTable/outer/inner_case", Tags: tags(3, "FR-A-01/AC1")},
				{Line: 7, Name: "TestTable/sibling", Tags: tags(6, "FR-A-01/AC2")},
				{Line: 10, Name: "TestNext", Tags: tags(9, "NFR-PERF-01")},
			},
		},
		{
			name: "Go subtest outside a test function",
			file: "x_test.go",
			src: lines(
				"func TestA(t *testing.T) {}",
				"func (s *suite) run(t *testing.T) {",
				"\t// Verifies: FR-A-01/AC1",
				`	t.Run("a case", func(t *testing.T) {})`,
				"}",
			),
			problems: []string{"x_test.go:3: a Verifies comment that is not directly above a test declaration"},
		},
		{
			name: "Go TestMain and a function that is not a test",
			file: "x_test.go",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				"func TestMain(m *testing.M) {}",
				"// Verifies: FR-A-01/AC1",
				"func Testable() {}",
			),
			problems: []string{
				"x_test.go:1: a Verifies comment that is not directly above a test declaration",
				"x_test.go:3: a Verifies comment that is not directly above a test declaration",
			},
		},
		{
			name: "blank line between the comment and the test",
			file: "x_test.go",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				"",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{"x_test.go:1: a Verifies comment that is not directly above a test declaration"},
		},
		{
			name: "comment at the end of the file",
			file: "x_test.go",
			src:  "// Verifies: FR-A-01/AC1\n",
			problems: []string{
				"x_test.go:1: a Verifies comment that is not directly above a test declaration",
			},
		},
		{
			name: "Verifies comment after the declaration",
			file: "x_test.go",
			src: lines(
				"func TestX(t *testing.T) { // Verifies: FR-A-01/AC1",
				"}",
				"func TestY(t *testing.T) { // verifies FR-A-01/AC2",
				"}",
				"func TestZ(t *testing.T) { // A note, not a tag.",
				"}",
			),
			problems: []string{
				"x_test.go:1: a Verifies comment after the test declaration (write it on its own line directly above the test)",
				"x_test.go:3: a Verifies comment after the test declaration (write it on its own line directly above the test)",
			},
		},
		{
			name: "Verifies list continued on the next line",
			file: "x_test.go",
			src: lines(
				"// FR-A-02/AC2 is tested in y_test.go.",
				"func TestW(t *testing.T) {}",
				"// Verifies: FR-A-01/AC1, FR-A-01/AC2",
				"// FR-A-02/AC1, NFR-PERF-01",
				"func TestX(t *testing.T) {}",
			),
			want:     []Test{{Line: 5, Name: "TestX", Tags: tags(3, "FR-A-01/AC1", "FR-A-01/AC2")}},
			problems: []string{`x_test.go:4: cannot read "FR-A-02/AC1, NFR-PERF-01" as a Verifies comment (write "Verifies:" and the IDs)`},
		},
		{
			name: "requirement without an acceptance criterion",
			file: "x_test.go",
			src: lines(
				"// Verifies: FR-A-01",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{"x_test.go:1: Verifies names FR-A-01 without an acceptance criterion (write FR-A-01/AC1)"},
		},
		{
			name: "unreadable entry among readable ones",
			file: "x_test.go",
			src: lines(
				"// Verifies: FR-A-01/AC1, the first one, fr-a-01/ac2",
				"func TestX(t *testing.T) {}",
			),
			want: []Test{{Line: 2, Name: "TestX", Tags: tags(1, "FR-A-01/AC1")}},
			problems: []string{
				`x_test.go:1: cannot read "the first one" in a Verifies comment as a requirement (write FR-…/ACn or NFR-…)`,
				`x_test.go:1: cannot read "fr-a-01/ac2" in a Verifies comment as a requirement (write FR-…/ACn or NFR-…)`,
			},
		},
		{
			name: "Verifies comment naming nothing",
			file: "x_test.go",
			src: lines(
				"// Verifies:",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{"x_test.go:1: a Verifies comment that names no requirement"},
		},
		{
			name:    "Verifies comment outside a test file",
			file:    "x.go",
			notTest: true,
			src: lines(
				"// Verifies: FR-A-01/AC1",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{"x.go:1: a Verifies comment outside a test file (tests are in *_test.go, *.test.ts, *.spec.ts, *.bench.ts, or test_*.gd files)"},
		},
		{
			name:    "misspelled Verifies outside a test file, which is prose",
			file:    "x.go",
			notTest: true,
			src: lines(
				"// verifies FR-A-01 input rules",
				"func f() {}",
			),
		},
		{
			name: "Verifies in a block comment",
			file: "x_test.go",
			src: lines(
				"/*",
				" * Verifies: FR-A-01/AC1",
				" */",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{"x_test.go:2: a Verifies comment in a block comment (write it as a // line comment directly above the test)"},
		},
		{
			name: "Verifies in a Go block comment without stars",
			file: "x_test.go",
			src: lines(
				"/*",
				"Verifies: FR-A-01/AC1",
				"*/",
				"func TestX(t *testing.T) {}",
				"/* Verifies: FR-A-01/AC2",
				"*/",
				"func TestY(t *testing.T) {}",
			),
			problems: []string{
				"x_test.go:2: a Verifies comment in a block comment (write it as a // line comment directly above the test)",
				"x_test.go:5: a Verifies comment in a block comment (write it as a // line comment directly above the test)",
			},
		},
		{
			name: "block comment that opens after code",
			file: "x_test.go",
			src: lines(
				"var x = 1 /* a note",
				" * Verifies: FR-A-01/AC1",
				" */",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{"x_test.go:2: a Verifies comment in a block comment (write it as a // line comment directly above the test)"},
		},
		{
			name: "block comment between the comment and the test",
			file: "x_test.go",
			src: lines(
				"// Verifies: FR-A-01/AC1",
				"/* A note. */",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{"x_test.go:1: a Verifies comment that is not directly above a test declaration"},
		},
		{
			name: "a glob and a Verifies field are code",
			file: "x_test.go",
			src: lines(
				"func TestA(t *testing.T) {",
				`	_, _ = filepath.Glob("testdata/*.go")`,
				"\tentry := TestEntry{",
				"\t\tVerifies: []string{},",
				"\t}",
				"\t// Verifies: FR-A-01/AC1",
				`	t.Run("after", func(t *testing.T) {})`,
				"}",
			),
			want: []Test{{Line: 7, Name: "TestA/after", Tags: tags(6, "FR-A-01/AC1")}},
		},
		{
			name: "misspelled Verifies comment",
			file: "x_test.go",
			src: lines(
				"// verifies: FR-A-01/AC1",
				"// Verifies FR-A-01/AC2",
				"// Verified: NFR-PERF-01",
				"// Verifies — FR-A-01/AC1",
				"// @verifies (NFR-PERF-01)",
				"// Verifies that a moved node keeps its connections.",
				"func TestX(t *testing.T) {}",
			),
			problems: []string{
				`x_test.go:1: cannot read "verifies: FR-A-01/AC1" as a Verifies comment (write "Verifies:" and the IDs)`,
				`x_test.go:2: cannot read "Verifies FR-A-01/AC2" as a Verifies comment (write "Verifies:" and the IDs)`,
				`x_test.go:3: cannot read "Verified: NFR-PERF-01" as a Verifies comment (write "Verifies:" and the IDs)`,
				`x_test.go:4: cannot read "Verifies — FR-A-01/AC1" as a Verifies comment (write "Verifies:" and the IDs)`,
				`x_test.go:5: cannot read "@verifies (NFR-PERF-01)" as a Verifies comment (write "Verifies:" and the IDs)`,
			},
		},
		{
			name: "CRLF line endings and a byte order mark",
			file: "x_test.go",
			src:  "\xef\xbb\xbf// Verifies: FR-A-01/AC1\r\nfunc TestX(t *testing.T) {}\r\n",
			want: []Test{{Line: 2, Name: "TestX", Tags: tags(1, "FR-A-01/AC1")}},
		},
		{
			name: "comment that only mentions Verifies",
			file: "x_test.go",
			src: lines(
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
				"// Verifies: NFR-PERF-01",
				`bench("speed", () => {});`,
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
				{Line: 14, Name: "speed", Tags: tags(13, "NFR-PERF-01")},
				{Line: 16, Name: `a "quoted" \ title`, Tags: tags(15, "FR-A-01/AC1")},
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
				{Line: 3, Name: "inside", Skipped: true, Tags: tags(2, "FR-A-01/AC1")},
				{Line: 7, Name: "deeper", Skipped: true, Tags: tags(6, "FR-A-01/AC2")},
				{Line: 11, Name: "after", Tags: tags(10, "FR-A-01/AC1")},
				{Line: 14, Name: "next", Tags: tags(13, "FR-A-01/AC2")},
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
				{Line: 6, Name: "inside", Skipped: true, Tags: tags(5, "FR-A-01/AC1")},
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
				{Line: 3, Name: "inside", Skipped: true, Tags: tags(2, "FR-A-01/AC1")},
				{Line: 6, Name: "next", Tags: tags(5, "FR-A-01/AC2")},
			},
		},
		{
			name: "Verifies comment after a Vitest declaration",
			file: "x.test.ts",
			src: lines(
				`test("x // y", () => {}); // Verifies: FR-A-01/AC1`,
				`it("loads https://example.com", () => {});`,
			),
			problems: []string{"x.test.ts:1: a Verifies comment after the test declaration (write it on its own line directly above the test)"},
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
			name: "Vitest benchmark file",
			file: "x.bench.ts",
			src: lines(
				"// Verifies: NFR-PERF-01",
				`bench("speed", () => {});`,
			),
			want: []Test{{Line: 2, Name: "speed", Tags: tags(1, "NFR-PERF-01")}},
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
			),
			problems: []string{
				"x.test.tsx:1: a Verifies comment that is not directly above a test declaration",
				"x.test.tsx:3: a Verifies comment that is not directly above a test declaration",
				"x.test.tsx:5: a Verifies comment that is not directly above a test declaration",
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
			name: "Verifies comment after a GUT declaration",
			file: "test_x.gd",
			src: lines(
				"func test_one(): # Verifies: FR-A-01/AC1",
				"\tpass",
			),
			problems: []string{"test_x.gd:1: a Verifies comment after the test declaration (write it on its own line directly above the test)"},
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
	tagged := lines("// Verifies: FR-A-01/AC1", "func TestX(t *testing.T) {}", "")
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
	want := []Test{{File: "server/internal/a/a_test.go", Line: 2, Name: "TestX", Tags: tags(1, "FR-A-01/AC1")}}
	if !reflect.DeepEqual(got, want) || len(problems) > 0 {
		t.Errorf("Scan = %+v, problems:%s\nwant %+v", got, formatProblems(problems), want)
	}

	if _, _, err := Scan(filepath.Join(root, "missing")); err == nil {
		t.Error("Scan of a missing directory should fail")
	}
}
