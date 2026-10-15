// Package traceability maps the PRD's requirements to the tests that verify
// them, and lists the acceptance criteria of the current phase that no test
// verifies (PRD NFR-Q-02, M-Q-01).
//
// A test names what it verifies in a line comment directly above its
// declaration, as a comma-separated list of acceptance criteria and NFRs:
//
//	// Verifies: FR-EDIT-02/AC3, NFR-PERF-01
//	func TestMoveNode(t *testing.T) {
//
// The same comment tags a Go subtest (t.Run), a Vitest or Playwright test
// (test, it, or bench with a literal title, but not a describe or suite
// group, which runs nothing itself), and a GUT test (func test_…, with "#"
// comments). The scan parses Go files and tokenizes
// TypeScript, JavaScript, and GDScript files, so a comment marker in a string
// is never taken for a comment, and a declaration's groups are the calls it
// is written in. A criterion counts as verified when a test names it that is
// not skipped (see Test.Skipped): one that is not declared with skip, todo,
// fixme, or fail (or with Vitest's option skip, todo, or fails set to true),
// does not call Playwright's test.skip(), test.fixme(), or test.fail()
// without arguments or with the condition true, does not call t.Skip,
// t.Skipf, or t.SkipNow as a statement of its own body in Go, and is not
// inside a group, file, or test that does one of these.
package traceability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/doccheck"
)

// Report is the traceability report for one phase. Its JSON form is what the
// release gate (plan item 119) reads.
type Report struct {
	// Phase is the current phase. Its acceptance criteria and those of the
	// phases before it are in scope.
	Phase        string        `json:"phase"`
	Summary      Summary       `json:"summary"`
	Requirements []Requirement `json:"requirements"`
	NFRs         []NFR         `json:"nfrs"`
	Gaps         []string      `json:"gaps"` // in-scope criteria without a test, such as "FR-PUB-01/AC1"
	Tests        []TestEntry   `json:"tests"`
}

// Summary counts the in-scope acceptance criteria (metric M-Q-01).
type Summary struct {
	Criteria int `json:"criteria"`
	Verified int `json:"verified"`
	Gaps     int `json:"gaps"`
	Tests    int `json:"tests"` // tagged tests, skipped ones included
}

// Requirement is a functional requirement and the tests of each of its
// acceptance criteria.
type Requirement struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Phase    string      `json:"phase"`
	InScope  bool        `json:"inScope"`
	Criteria []Criterion `json:"criteria"`
}

// Criterion is an acceptance criterion and the tests that name it.
type Criterion struct {
	ID    string    `json:"id"` // "AC1"
	Tests []TestRef `json:"tests"`
}

// verified reports whether a test that is not skipped names the criterion.
func (c Criterion) verified() bool {
	return len(ran(c.Tests)) > 0
}

// NFR is a non-functional requirement and the tests that name it.
type NFR struct {
	ID    string    `json:"id"`
	Tests []TestRef `json:"tests"`
}

// TestRef locates a test.
type TestRef struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Name    string `json:"name"`
	Skipped bool   `json:"skipped,omitempty"` // it verifies nothing (see Test.Skipped)
}

// TestEntry is a tagged test and the IDs it names.
type TestEntry struct {
	TestRef
	Verifies []string `json:"verifies"`
}

// Generate reads the PRD at prdPath, scans root for tagged tests, and builds
// the report for phase. Problems are in the PRD or in the tags; an error
// means the phase is unknown or a file could not be read.
func Generate(prdPath, root, phase string) (*Report, []doccheck.Problem, error) {
	if !slices.Contains(doccheck.Phases(), phase) {
		return nil, nil, fmt.Errorf("unknown phase %q (want one of %s)", phase, strings.Join(doccheck.Phases(), ", "))
	}
	src, err := os.ReadFile(prdPath)
	if err != nil {
		return nil, nil, err
	}
	prd, problems := doccheck.ParsePRD(prdPath, string(src))
	tests, scanProblems, err := Scan(root)
	if err != nil {
		return nil, nil, err
	}
	r, buildProblems := Build(prd, tests, phase)
	return r, slices.Concat(problems, scanProblems, buildProblems), nil
}

// Build maps the tests to the requirements of prd for phase, one of
// doccheck.Phases. Problems are tags that name a requirement or criterion the
// PRD does not define, or that a test names twice.
func Build(prd doccheck.PRD, tests []Test, phase string) (*Report, []doccheck.Problem) {
	var problems []doccheck.Problem
	phases := doccheck.Phases()
	rank := slices.Index(phases, phase)
	r := &Report{
		Phase:        phase,
		Requirements: []Requirement{},
		NFRs:         []NFR{},
		Gaps:         []string{},
		Tests:        []TestEntry{},
	}

	// Where each ID's tests go: an index into Requirements and Criteria, or
	// into NFRs.
	type slot struct{ req, crit, nfr int }
	slots := map[string]slot{}
	reqs := map[string]bool{}
	for i, req := range prd.Requirements {
		// A requirement whose phase is unknown, which the PRD check reports,
		// is in no phase's scope.
		reqRank := slices.Index(phases, req.Phase)
		entry := Requirement{
			ID:       req.ID,
			Title:    req.Title,
			Phase:    req.Phase,
			InScope:  reqRank >= 0 && reqRank <= rank,
			Criteria: []Criterion{},
		}
		for j, c := range req.Criteria {
			entry.Criteria = append(entry.Criteria, Criterion{ID: c, Tests: []TestRef{}})
			slots[req.ID+"/"+c] = slot{req: i, crit: j, nfr: -1}
		}
		r.Requirements = append(r.Requirements, entry)
		reqs[req.ID] = true
	}
	for i, id := range prd.NFRs {
		r.NFRs = append(r.NFRs, NFR{ID: id, Tests: []TestRef{}})
		slots[id] = slot{nfr: i}
	}

	for _, t := range tests {
		ref := TestRef{File: t.File, Line: t.Line, Name: t.Name, Skipped: t.Skipped}
		entry := TestEntry{TestRef: ref, Verifies: []string{}}
		for _, tag := range t.Tags {
			if slices.Contains(entry.Verifies, tag.ID) {
				problems = append(problems, doccheck.Problem{File: t.File, Line: tag.Line, Msg: fmt.Sprintf("%s names %s twice", t.Name, tag.ID)})
				continue
			}
			entry.Verifies = append(entry.Verifies, tag.ID)
			s, ok := slots[tag.ID]
			switch {
			case !ok:
				problems = append(problems, doccheck.Problem{File: t.File, Line: tag.Line, Msg: unknownTag(tag.ID, reqs)})
			case s.nfr >= 0:
				r.NFRs[s.nfr].Tests = append(r.NFRs[s.nfr].Tests, ref)
			default:
				c := &r.Requirements[s.req].Criteria[s.crit]
				c.Tests = append(c.Tests, ref)
			}
		}
		r.Tests = append(r.Tests, entry)
	}

	for _, req := range r.Requirements {
		if !req.InScope {
			continue
		}
		for _, c := range req.Criteria {
			r.Summary.Criteria++
			if c.verified() {
				r.Summary.Verified++
			} else {
				r.Gaps = append(r.Gaps, req.ID+"/"+c.ID)
			}
		}
	}
	r.Summary.Gaps = len(r.Gaps)
	r.Summary.Tests = len(r.Tests)
	return r, problems
}

// unknownTag explains why a tag names nothing the PRD defines.
func unknownTag(id string, reqs map[string]bool) string {
	if fr, ac, ok := strings.Cut(id, "/"); ok {
		if reqs[fr] {
			return fmt.Sprintf("Verifies names %s, but %s has no %s", id, fr, ac)
		}
		return fmt.Sprintf("Verifies names %s, which is not a requirement of the PRD", fr)
	}
	return fmt.Sprintf("Verifies names %s, which is not a requirement of the PRD", id)
}

// scope names the in-scope phases: "MVP", "MVP and v1", "MVP, v1, and Collab".
func (r *Report) scope() string {
	phases := doccheck.Phases()
	i := slices.Index(phases, r.Phase)
	if i < 0 {
		return r.Phase
	}
	phases = phases[:i+1]
	switch len(phases) {
	case 1:
		return phases[0]
	case 2:
		return phases[0] + " and " + phases[1]
	}
	return strings.Join(phases[:len(phases)-1], ", ") + ", and " + phases[len(phases)-1]
}

// SummaryLine is a one-line summary of the report.
func (r *Report) SummaryLine() string {
	return fmt.Sprintf("Traceability (%s): %d of %s verified (%s), %d without a test. %s.",
		r.scope(), r.Summary.Verified, count(r.Summary.Criteria, "acceptance criterion", "acceptance criteria"),
		percent(r.Summary.Verified, r.Summary.Criteria), r.Summary.Gaps, count(r.Summary.Tests, "tagged test", "tagged tests"))
}

// count writes n with the singular or plural noun: "1 test", "2 tests".
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// verb returns the singular verb for a count of one, and the plural otherwise.
func verb(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func percent(n, of int) string {
	if of == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(of))
}

// JSON returns the report as indented JSON.
func (r *Report) JSON() ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Markdown returns the report as a Markdown document.
func (r *Report) Markdown() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Requirement traceability\n\n")
	fmt.Fprintf(&b, "%d of %s of the %s %s a test (%s), and %d %s none. %s %s tagged.\n",
		r.Summary.Verified, count(r.Summary.Criteria, "acceptance criterion", "acceptance criteria"), r.scope(),
		verb(r.Summary.Verified, "has", "have"), percent(r.Summary.Verified, r.Summary.Criteria),
		r.Summary.Gaps, verb(r.Summary.Gaps, "has", "have"),
		count(r.Summary.Tests, "test", "tests"), verb(r.Summary.Tests, "is", "are"))

	fmt.Fprintf(&b, "\n## Acceptance criteria without a test\n\n")
	if r.Summary.Gaps == 0 {
		fmt.Fprintf(&b, "None.\n")
	}
	for _, req := range r.Requirements {
		var gaps []string
		for _, c := range req.Criteria {
			if req.InScope && !c.verified() {
				gaps = append(gaps, c.ID)
			}
		}
		if len(gaps) > 0 {
			fmt.Fprintf(&b, "- %s %s: %s\n", req.ID, req.Title, strings.Join(gaps, ", "))
		}
	}

	fmt.Fprintf(&b, "\n## Verified acceptance criteria\n\n")
	found := false
	for _, req := range r.Requirements {
		for _, c := range req.Criteria {
			tests := ran(c.Tests)
			if len(tests) == 0 {
				continue
			}
			found = true
			later := ""
			if !req.InScope {
				later = " (" + req.Phase + ")"
			}
			fmt.Fprintf(&b, "- %s/%s%s: %s\n", req.ID, c.ID, later, refs(tests))
		}
	}
	if !found {
		fmt.Fprintf(&b, "None.\n")
	}

	fmt.Fprintf(&b, "\n## Verified non-functional requirements\n\n")
	found = false
	for _, n := range r.NFRs {
		if tests := ran(n.Tests); len(tests) > 0 {
			found = true
			fmt.Fprintf(&b, "- %s: %s\n", n.ID, refs(tests))
		}
	}
	if !found {
		fmt.Fprintf(&b, "None.\n")
	}

	var skipped []TestEntry
	for _, t := range r.Tests {
		if t.Skipped {
			skipped = append(skipped, t)
		}
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "\n## Skipped tests\n\nThese tests verify nothing: each is declared with skip, todo, fixme, or fail (or with Vitest's option skip, todo, or fails set to true); calls Playwright's test.skip(), test.fixme(), or test.fail() without arguments or with the condition true; calls t.Skip, t.Skipf, or t.SkipNow as a statement of its own body, in Go; or is inside a group, file, or test that does one of these.\n\n")
		for _, t := range skipped {
			fmt.Fprintf(&b, "- %s: %s\n", ref(t.TestRef), strings.Join(t.Verifies, ", "))
		}
	}
	return b.Bytes()
}

// ran returns the tests that are not skipped.
func ran(tests []TestRef) []TestRef {
	var out []TestRef
	for _, t := range tests {
		if !t.Skipped {
			out = append(out, t)
		}
	}
	return out
}

func refs(tests []TestRef) string {
	parts := make([]string, len(tests))
	for i, t := range tests {
		parts[i] = ref(t)
	}
	return strings.Join(parts, "; ")
}

func ref(t TestRef) string {
	return codeSpan(fmt.Sprintf("%s:%d", t.File, t.Line)) + " " + codeSpan(t.Name)
}

// codeSpan writes s as a Markdown code span, with a fence longer than any run
// of backticks in s. A line break, as in a test title with "\n", is written
// as a space, as a code span shows it, so that it cannot end the list item
// the span is in.
func codeSpan(s string) string {
	s = lineBreaks.Replace(s)
	longest, run := 0, 0
	for _, c := range s {
		if c == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	if longest > 0 || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

var lineBreaks = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")
