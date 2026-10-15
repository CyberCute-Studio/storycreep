package traceability

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/doccheck"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A checkout made without .gitattributes may have CRLF.
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

func fixturePRD(t *testing.T) doccheck.PRD {
	t.Helper()
	prd, problems := doccheck.ParsePRD("PRD.md", readFile(t, filepath.Join("testdata", "PRD.md")))
	if len(problems) > 0 {
		t.Fatalf("the fixture PRD should pass, got:%s", formatProblems(problems))
	}
	return prd
}

func formatProblems(problems []doccheck.Problem) string {
	var b strings.Builder
	for _, p := range problems {
		b.WriteString("\n\t" + p.String())
	}
	return b.String()
}

// TestSampleTestAndGaps is plan item 2's acceptance check: the report lists a
// tagged sample test, and every other acceptance criterion of the MVP is a
// gap.
//
// Verifies: NFR-Q-02
func TestSampleTestAndGaps(t *testing.T) {
	sample := Test{File: "server/sample_test.go", Line: 8, Name: "TestSample", Tags: []Tag{{ID: "FR-A-01/AC1", Line: 7}}}
	r, problems := Build(fixturePRD(t), []Test{sample}, "MVP")
	if len(problems) > 0 {
		t.Fatalf("unexpected problems:%s", formatProblems(problems))
	}
	want := []TestEntry{{TestRef: TestRef{File: "server/sample_test.go", Line: 8, Name: "TestSample"}, Verifies: []string{"FR-A-01/AC1"}}}
	if !reflect.DeepEqual(r.Tests, want) {
		t.Errorf("tests = %+v, want %+v", r.Tests, want)
	}
	if got := r.Requirements[0].Criteria[0].Tests; !reflect.DeepEqual(got, []TestRef{want[0].TestRef}) {
		t.Errorf("FR-A-01/AC1 tests = %+v, want the sample test", got)
	}
	if want := []string{"FR-A-01/AC2", "FR-A-02/AC1", "FR-A-02/AC2", "FR-A-03/AC1"}; !slices.Equal(r.Gaps, want) {
		t.Errorf("gaps = %q, want %q", r.Gaps, want)
	}
	if want := (Summary{Criteria: 5, Verified: 1, Gaps: 4, Tests: 1}); r.Summary != want {
		t.Errorf("summary = %+v, want %+v", r.Summary, want)
	}
	if line := r.SummaryLine(); line != "Traceability (MVP): 1 of 5 acceptance criteria verified (20.0%), 4 without a test. 1 tagged test." {
		t.Errorf("summary line = %q", line)
	}
	md := string(r.Markdown())
	for _, s := range []string{
		"1 of 5 acceptance criteria of the MVP has a test (20.0%), and 4 have none. 1 test is tagged.\n",
		"- FR-A-01/AC1: `server/sample_test.go:8` `TestSample`\n",
		"- FR-A-01 First: AC2\n",
		"- FR-A-02 Second, with `code`: AC1, AC2\n",
		"- FR-A-03 Third: AC1\n",
	} {
		if !strings.Contains(md, s) {
			t.Errorf("the Markdown report does not contain %q:\n%s", s, md)
		}
	}
}

// TestGenerate runs the whole report over a fixture repository with a tagged
// test in each language, and compares it with testdata/report.md.
//
// Verifies: NFR-Q-02
func TestGenerate(t *testing.T) {
	r, problems, err := Generate(filepath.Join("testdata", "PRD.md"), filepath.Join("testdata", "repo"), "MVP")
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("unexpected problems:%s", formatProblems(problems))
	}
	if want := []string{"FR-A-01/AC2", "FR-A-02/AC2"}; !slices.Equal(r.Gaps, want) {
		t.Errorf("gaps = %q, want %q (FR-A-02/AC2 has only skipped tests)", r.Gaps, want)
	}

	golden := filepath.Join("testdata", "report.md")
	got := r.Markdown()
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if want := readFile(t, golden); string(got) != want {
		t.Errorf("the Markdown report differs from %s (run go test -update to rewrite it):\n%s", golden, got)
	}
	if line := r.SummaryLine(); line != "Traceability (MVP): 3 of 5 acceptance criteria verified (60.0%), 2 without a test. 8 tagged tests." {
		t.Errorf("summary line = %q", line)
	}
}

// TestJSON checks the JSON form that the release gate reads: every list is
// present even when empty, and a skipped test is marked.
func TestJSON(t *testing.T) {
	r, _, err := Generate(filepath.Join("testdata", "PRD.md"), filepath.Join("testdata", "repo"), "MVP")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&back, r) {
		t.Errorf("the JSON report does not round-trip:\n%s", b)
	}
	for _, s := range []string{
		`"phase": "MVP"`,
		`"gaps": [`,
		`"id": "FR-C-01",`,
		`"inScope": false,`,
		`"tests": []`,
		`"skipped": true,`,
		`"verifies": [`,
	} {
		if !bytes.Contains(b, []byte(s)) {
			t.Errorf("the JSON report does not contain %s:\n%s", s, b)
		}
	}
}

func TestPhaseScope(t *testing.T) {
	prd := fixturePRD(t)
	tests := []struct {
		phase string
		gaps  []string
		scope string
	}{
		{"MVP", []string{"FR-A-01/AC1", "FR-A-01/AC2", "FR-A-02/AC1", "FR-A-02/AC2", "FR-A-03/AC1"}, "MVP"},
		{"v1", []string{"FR-A-01/AC1", "FR-A-01/AC2", "FR-A-02/AC1", "FR-A-02/AC2", "FR-A-03/AC1", "FR-B-01/AC1", "FR-B-01/AC2"}, "MVP and v1"},
		{"SaaS", []string{"FR-A-01/AC1", "FR-A-01/AC2", "FR-A-02/AC1", "FR-A-02/AC2", "FR-A-03/AC1", "FR-B-01/AC1", "FR-B-01/AC2", "FR-C-01/AC1"}, "MVP, v1, Collab, and SaaS"},
	}
	for _, tt := range tests {
		r, _ := Build(prd, nil, tt.phase)
		if !slices.Equal(r.Gaps, tt.gaps) {
			t.Errorf("phase %s: gaps = %q, want %q", tt.phase, r.Gaps, tt.gaps)
		}
		if got := r.scope(); got != tt.scope {
			t.Errorf("phase %s: scope = %q, want %q", tt.phase, got, tt.scope)
		}
	}
	// A requirement whose phase is unknown, which the PRD check reports, is
	// in no phase's scope.
	prd.Requirements = append(prd.Requirements, doccheck.Requirement{ID: "FR-D-01", Title: "Unplaced", Phase: "Beta", Criteria: []string{"AC1"}})
	r, _ := Build(prd, nil, "Later")
	if got := r.Requirements[len(r.Requirements)-1]; got.InScope || slices.Contains(r.Gaps, "FR-D-01/AC1") {
		t.Errorf("a requirement with an unknown phase is in scope: %+v, gaps %q", got, r.Gaps)
	}

	if _, _, err := Generate(filepath.Join("testdata", "PRD.md"), filepath.Join("testdata", "repo"), "Beta"); err == nil || !strings.Contains(err.Error(), `unknown phase "Beta"`) {
		t.Errorf("Generate with an unknown phase: err = %v", err)
	}
}

func TestBuildProblems(t *testing.T) {
	tagged := func(line int, ids ...string) Test {
		var tags []Tag
		for _, id := range ids {
			tags = append(tags, Tag{ID: id, Line: line - 1})
		}
		return Test{File: "x_test.go", Line: line, Name: "TestX", Tags: tags}
	}
	tests := []struct {
		test Test
		want string
	}{
		{tagged(10, "FR-Z-01/AC1"), "x_test.go:9: Verifies names FR-Z-01, which is not a requirement of the PRD"},
		{tagged(10, "FR-A-03/AC2"), "x_test.go:9: Verifies names FR-A-03/AC2, but FR-A-03 has no AC2"},
		{tagged(10, "NFR-Q-99"), "x_test.go:9: Verifies names NFR-Q-99, which is not a requirement of the PRD"},
		{tagged(10, "FR-A-01/AC1", "FR-A-01/AC1"), "x_test.go:9: TestX names FR-A-01/AC1 twice"},
	}
	for _, tt := range tests {
		_, problems := Build(fixturePRD(t), []Test{tt.test}, "MVP")
		if len(problems) != 1 || problems[0].String() != tt.want {
			t.Errorf("tags %+v: problems =%s\nwant %s", tt.test.Tags, formatProblems(problems), tt.want)
		}
	}

	_, problems, err := Generate(filepath.Join("testdata", "PRD.md"), t.TempDir(), "MVP")
	if err != nil || len(problems) > 0 {
		t.Errorf("an empty repository: problems =%s, err = %v", formatProblems(problems), err)
	}
}

// TestSingular checks the report's wording when a count is one.
func TestSingular(t *testing.T) {
	r := &Report{Phase: "MVP", Summary: Summary{Criteria: 1, Verified: 0, Gaps: 1, Tests: 0}}
	if line := r.SummaryLine(); line != "Traceability (MVP): 0 of 1 acceptance criterion verified (0.0%), 1 without a test. 0 tagged tests." {
		t.Errorf("summary line = %q", line)
	}
	if md, want := string(r.Markdown()), "0 of 1 acceptance criterion of the MVP have a test (0.0%), and 1 has none. 0 tests are tagged.\n"; !strings.Contains(md, want) {
		t.Errorf("the Markdown report does not contain %q:\n%s", want, md)
	}
}

func TestCodeSpan(t *testing.T) {
	tests := map[string]string{
		"TestX":         "`TestX`",
		"a `quoted` id": "`` a `quoted` id ``",
		"a `` b":        "``` a `` b ```",
		" padded":       "`  padded `",
		"a\n- b\r\nc":   "`a - b c`",
	}
	for in, want := range tests {
		if got := codeSpan(in); got != want {
			t.Errorf("codeSpan(%q) = %q, want %q", in, got, want)
		}
	}
}
