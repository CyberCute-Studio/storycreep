package doccheck

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	// The edits below are written with LF; a checkout made without
	// .gitattributes may have CRLF.
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// edit replaces old with new in a fixture. old must occur exactly once, so a
// test cannot pass because its edit silently stopped applying.
type edit struct{ old, new string }

func applyEdits(t *testing.T, src string, edits []edit) string {
	t.Helper()
	for _, e := range edits {
		if n := strings.Count(src, e.old); n != 1 {
			t.Fatalf("edit %q matches %d times, want 1", e.old, n)
		}
		src = strings.Replace(src, e.old, e.new, 1)
	}
	return src
}

func formatProblems(problems []Problem) string {
	var b strings.Builder
	for _, p := range problems {
		b.WriteString("\n\t" + p.String())
	}
	return b.String()
}

func TestFixturesAreConsistent(t *testing.T) {
	problems := Check("PRD.md", readFixture(t, "PRD.md"), "PLAN.md", readFixture(t, "PLAN.md"))
	if len(problems) > 0 {
		t.Errorf("the fixtures should pass, got:%s", formatProblems(problems))
	}
}

// TestCheckFails breaks the consistent fixtures one rule at a time and expects
// the matching problem.
func TestCheckFails(t *testing.T) {
	tests := []struct {
		name      string
		prd, plan []edit
		file      string // the file the problem is reported in
		want      string // a substring of the problem's message
	}{
		// PRD §4.0.
		{
			name: "dependency cycle",
			prd:  []edit{{"**Phase:** MVP · **Depends on:** —", "**Phase:** MVP · **Depends on:** FR-A-03"}},
			file: "PRD.md",
			want: "dependency cycle: FR-A-01 → FR-A-03 → FR-A-01",
		},
		{
			name: "requirement depending on itself",
			prd:  []edit{{"**Phase:** v1 · **Depends on:** FR-A-03", "**Phase:** v1 · **Depends on:** FR-B-01"}},
			file: "PRD.md",
			want: "dependency cycle: FR-B-01 → FR-B-01",
		},
		{
			name: "dependency on an unknown ID",
			prd:  []edit{{"**Depends on:** FR-A-01\n", "**Depends on:** FR-A-01, FR-Q-07\n"}},
			file: "PRD.md",
			want: "FR-A-02 depends on FR-Q-07, which is not a requirement of this PRD",
		},
		{
			name: "unreadable dependency",
			prd:  []edit{{"**Depends on:** FR-A-01\n", "**Depends on:** the first one\n"}},
			file: "PRD.md",
			want: `cannot read "the first one" in its Depends on field`,
		},
		{
			name: "requirement without a Depends on field",
			prd:  []edit{{"**Phase:** SaaS · **Depends on:** FR-B-01", "**Phase:** SaaS"}},
			file: "PRD.md",
			want: "FR-B-02 has no Depends on field",
		},
		{
			name: "Phase field disagreeing with Appendix B",
			prd:  []edit{{"**Phase:** v1 · **Depends on:** FR-A-03", "**Phase:** MVP · **Depends on:** FR-A-03"}},
			file: "PRD.md",
			want: "FR-B-01 has Phase MVP, but Appendix B lists it under v1",
		},
		{
			name: "requirement missing from Appendix B",
			prd:  []edit{{"**Hosted SaaS.** FR-B-02", ""}},
			file: "PRD.md",
			want: "FR-B-02 is not listed in Appendix B",
		},
		{
			name: "exit criteria without performance targets",
			prd:  []edit{{"NFR-PERF-01…02 met", "met"}},
			file: "PRD.md",
			want: "the §12.1 exit criteria name no NFR-PERF targets",
		},
		{
			name: "exit criteria naming an unknown target in a comma list",
			prd:  []edit{{"NFR-PERF-01…02 met", "NFR-PERF-01, 02, 04 met"}},
			file: "PRD.md",
			want: "the §12.1 exit criteria name NFR-PERF-04, which is not defined",
		},
		{
			name: "exit criteria naming an unknown target in a list joined by and",
			prd:  []edit{{"NFR-PERF-01…02 met", "NFR-PERF-01, 02, and 04 met"}},
			file: "PRD.md",
			want: "the §12.1 exit criteria name NFR-PERF-04, which is not defined",
		},
		{
			name: "exit criteria with a range written with three dots",
			prd:  []edit{{"NFR-PERF-01…02 met", "NFR-PERF-01...02 met"}},
			file: "PRD.md",
			want: `§12.1 exit criteria: cannot read "NFR-PERF-01...02" as a requirement ID`,
		},
		{
			name: "requirement without acceptance criteria",
			prd:  []edit{{"- **Acceptance criteria:**\n  - AC1 — It is hosted.\n", ""}},
			file: "PRD.md",
			want: "FR-B-02 has no Acceptance criteria field",
		},
		{
			name: "Acceptance criteria field listing nothing",
			prd:  []edit{{"  - AC1 — It is hosted.\n", ""}},
			file: "PRD.md",
			want: "FR-B-02 lists no acceptance criteria",
		},
		{
			name: "requirement with a second Acceptance criteria field",
			prd:  []edit{{"  - AC1 — It is hosted.\n", "  - AC1 — It is hosted.\n- **Acceptance criteria:** AC2 — Again.\n"}},
			file: "PRD.md",
			want: "FR-B-02 has a second Acceptance criteria field (first on line 41)",
		},
		{
			name: "unreadable acceptance criterion",
			prd:  []edit{{"  - AC1 — It is hosted.\n", "  - AC1 — It is hosted.\n  - AC 2 — It is hosted well.\n"}},
			file: "PRD.md",
			want: `FR-B-02: cannot read "AC 2 — It is hosted well." as an acceptance criterion`,
		},
		{
			name: "unreadable inline acceptance criteria",
			prd:  []edit{{"AC1 — It shines. AC2 — It still works.", "It shines and still works."}},
			file: "PRD.md",
			want: `FR-B-01: cannot read "It shines and still works." as an acceptance criterion`,
		},
		{
			name: "inline acceptance criterion with an en dash",
			prd:  []edit{{"AC2 — It still works.", "AC2 – It still works."}},
			file: "PRD.md",
			want: `FR-B-01: cannot read "AC2 –" as an acceptance criterion (write "ACn — …")`,
		},
		{
			name: "inline acceptance criterion with a colon",
			prd:  []edit{{"AC2 — It still works.", "AC2: It still works."}},
			file: "PRD.md",
			want: `FR-B-01: cannot read "AC2:" as an acceptance criterion`,
		},
		{
			name: "inline acceptance criterion with a no-break space",
			prd:  []edit{{"AC2 — It still works.", "AC2\u00a0— It still works."}},
			file: "PRD.md",
			want: "FR-B-01: cannot read \"AC2\\u00a0—\" as an acceptance criterion",
		},
		{
			name: "inline acceptance criterion in bold",
			prd:  []edit{{"AC2 — It still works.", "**AC2** — It still works."}},
			file: "PRD.md",
			want: `FR-B-01: cannot read "AC2** —" as an acceptance criterion`,
		},
		{
			name: "acceptance criterion in parentheses in a bullet",
			prd:  []edit{{"  - AC1 — It works too.\n", "  - AC1 — It works too (AC3 — and thrice).\n"}},
			file: "PRD.md",
			want: `FR-A-02: cannot read "AC3 —" as an acceptance criterion (write each criterion as its own "- ACn — …" bullet)`,
		},
		{
			name: "inline acceptance criterion without a space after the dash",
			prd:  []edit{{"AC2 — It still works.", "AC2 —It still works."}},
			file: "PRD.md",
			want: `FR-B-01: cannot read "AC2 —" as an acceptance criterion`,
		},
		{
			name: "acceptance criterion in a star bullet",
			prd:  []edit{{"  - AC1 — It is hosted.\n", "  - AC1 — It is hosted.\n  * AC2 — It is starred.\n"}},
			file: "PRD.md",
			want: `FR-B-02: cannot read "* AC2 — It is starred." as an acceptance criterion (write it as a "- ACn — …" bullet)`,
		},
		{
			name: "acceptance criterion in a numbered item",
			prd:  []edit{{"  - AC1 — It is hosted.\n", "  - AC1 — It is hosted.\n\n  1. It is numbered.\n"}},
			file: "PRD.md",
			want: `FR-B-02: cannot read "1. It is numbered." as an acceptance criterion`,
		},
		{
			name: "acceptance criterion without a bullet",
			prd:  []edit{{"  - AC1 — It is hosted.\n", "  - AC1 — It is hosted.\n  AC2 — It has no bullet.\n"}},
			file: "PRD.md",
			want: `FR-B-02: cannot read "AC2 — It has no bullet." as an acceptance criterion`,
		},
		{
			name: "acceptance criterion bullet at the margin",
			prd:  []edit{{"  - AC1 — It works too.\n", "  - AC1 — It works too.\n- AC3 — At the margin.\n"}},
			file: "PRD.md",
			want: `FR-A-02: cannot read "- AC3 — At the margin." as an acceptance criterion (indent it as a "  - ACn — …" bullet under the field)`,
		},
		{
			name: "acceptance criterion text at the margin",
			prd:  []edit{{"  - AC1 — It works too.\n", "  - AC1 — It works too.\nAC3 — A lazy continuation line.\n"}},
			file: "PRD.md",
			want: `FR-A-02: cannot read "AC3 — A lazy continuation line." as an acceptance criterion`,
		},
		{
			name: "acceptance criterion before the Acceptance criteria field",
			prd:  []edit{{"- **Acceptance criteria:**\n  - AC2 — It is listed out of order.\n", "  - AC3 — Before the field.\n- **Acceptance criteria:**\n  - AC2 — It is listed out of order.\n"}},
			file: "PRD.md",
			want: `FR-A-02: cannot read "- AC3 — Before the field." as an acceptance criterion, because it comes before the Acceptance criteria field`,
		},
		{
			name: "acceptance criterion in text before the Acceptance criteria field",
			prd:  []edit{{"- **Acceptance criteria:**\n  - AC2 — It is listed out of order.\n", "It works. AC3 — It works before the field.\n- **Acceptance criteria:**\n  - AC2 — It is listed out of order.\n"}},
			file: "PRD.md",
			want: `FR-A-02: cannot read "It works. AC3 — It works before the field." as an acceptance criterion, because it comes before the Acceptance criteria field`,
		},
		{
			name: "acceptance criterion after a line of text at the margin",
			prd:  []edit{{"  - AC2 — It is listed out of order.\n", "  - AC2 — It is listed out of order,\nwrapped at the margin.\n"}},
			file: "PRD.md",
			want: `FR-A-02: cannot read "- AC1 — It works too." as an acceptance criterion, because the list of acceptance criteria ends on line 26`,
		},
		{
			name: "inline acceptance criteria wrapped onto a line at the margin",
			prd:  []edit{{"AC1 — It shines. AC2 — It still works.", "AC1 — It shines. AC2 — It still\nworks. AC3 — It is wrapped."}},
			file: "PRD.md",
			want: `FR-B-01: cannot read "works. AC3 — It is wrapped." as an acceptance criterion (indent it`,
		},
		{
			name: "acceptance criterion in text at the margin after the list",
			prd:  []edit{{"  - AC2 — It is listed out of order.\n", "  - AC2 — It is listed out of order,\nwrapped at the margin,\nand again. AC3 — It is wrapped.\n"}},
			file: "PRD.md",
			want: `FR-A-02: cannot read "and again. AC3 — It is wrapped." as an acceptance criterion, because the list of acceptance criteria ends on line 26`,
		},
		{
			name: "two acceptance criteria in one bullet",
			prd:  []edit{{"  - AC1 — It works too.\n", "  - AC1 — It works too. AC3 — It works thrice.\n"}},
			file: "PRD.md",
			want: `FR-A-02: cannot read "AC3 —" as an acceptance criterion (write each criterion as its own "- ACn — …" bullet)`,
		},
		{
			name: "inline acceptance criterion on a wrapped line",
			prd:  []edit{{"AC1 — It shines. AC2 — It still works.", "AC1 — It shines.\n  AC2 — It still works."}},
			file: "PRD.md",
			want: `FR-B-01: cannot read "AC2 — It still works." as an acceptance criterion`,
		},
		{
			name: "acceptance criterion numbered from zero",
			prd:  []edit{{"  - AC1 — It is hosted.\n", "  - AC0 — It is hosted.\n"}},
			file: "PRD.md",
			want: `FR-B-02: cannot read "AC0 — It is hosted." as an acceptance criterion`,
		},
		{
			name: "acceptance criterion listed twice",
			prd:  []edit{{"  - AC1 — It works too.\n", "  - AC1 — It works too.\n  - AC2 — Twice.\n"}},
			file: "PRD.md",
			want: "FR-A-02 lists AC2 twice (also on line 25)",
		},
		{
			name: "inline acceptance criterion listed twice",
			prd:  []edit{{"AC2 — It still works.", "AC1 — It still works."}},
			file: "PRD.md",
			want: "FR-B-01 lists AC1 twice (also on line 37)",
		},
		{
			name: "gap in the acceptance criteria",
			prd:  []edit{{"  - AC1 — It works too.\n", "  - AC4 — It works too.\n"}},
			file: "PRD.md",
			want: "FR-A-02 has no AC1, although its criteria run to AC4",
		},
		{
			name: "requirement with a second Depends on field",
			prd:  []edit{{"**Depends on:** FR-A-01\n", "**Depends on:** FR-A-01\n- **Depends on:** —\n"}},
			file: "PRD.md",
			want: "FR-A-02 has a second Depends on field",
		},
		{
			name: "Appendix B listing an unknown ID",
			prd:  []edit{{"**Hosted SaaS.** FR-B-02", "**Hosted SaaS.** FR-B-02, 03"}},
			file: "PRD.md",
			want: "Appendix B lists FR-B-03, which is not a requirement of this PRD",
		},
		{
			name: "Appendix B listing a requirement under two phases",
			prd:  []edit{{"**v1.** FR-B-01", "**v1.** FR-B-01 · FR-A-03"}},
			file: "PRD.md",
			want: "Appendix B lists FR-A-03 under both MVP and v1",
		},
		{
			name: "unknown Phase field",
			prd:  []edit{{"**Phase:** SaaS · **Depends on:** FR-B-01", "**Phase:** Hosted · **Depends on:** FR-B-01"}},
			file: "PRD.md",
			want: `FR-B-02 has unknown phase "Hosted"`,
		},
		{
			name: "Appendix B with an unknown phase",
			prd:  []edit{{"**Hosted SaaS.** FR-B-02", "**Hosted.** FR-B-02"}},
			file: "PRD.md",
			want: `Appendix B: unknown phase "Hosted"`,
		},
		{
			name: "Appendix B with an entry that is not a requirement ID",
			prd:  []edit{{"**v1.** FR-B-01", "**v1.** FR-B-01 · keyboard remapping"}},
			file: "PRD.md",
			want: `Appendix B: "keyboard remapping" is not a requirement ID`,
		},

		// Plan rule 1.
		{
			name: "forward dependency",
			plan: []edit{{"**Status:** in progress · **Depends on:** 1", "**Status:** in progress · **Depends on:** 1, 4"}},
			file: "PLAN.md",
			want: "item 2 depends on 4, which is not an earlier item (rule 1)",
		},
		{
			name: "suffixed item depending on itself",
			plan: []edit{{"**Status:** todo · **Depends on:** 1, 2\n", "**Status:** todo · **Depends on:** 1, 2a\n"}},
			file: "PLAN.md",
			want: "item 2a depends on 2a, which is not an earlier item (rule 1)",
		},
		{
			name: "dependency range reaching forward",
			plan: []edit{{"**Depends on:** 1–4", "**Depends on:** 1–5"}},
			file: "PLAN.md",
			want: "item 5 depends on 1–5, which is not an earlier item (rule 1)",
		},
		{
			name: "unreadable item dependency",
			plan: []edit{{"**Status:** in progress · **Depends on:** 1", "**Status:** in progress · **Depends on:** 1 (the scaffold)"}},
			file: "PLAN.md",
			want: `item 2: cannot read "1 (the scaffold)" as an item number or range in its Depends on field`,
		},

		// Plan rule 3.
		{
			name: "FR missing from the coverage matrix",
			plan: []edit{
				{"**PRD:** FR-B-01, FR-A-03 (extension)", "**PRD:** FR-A-03 (extension)"},
				{"| FR-B-01 | 5 | 5 |\n", ""},
			},
			file: "PLAN.md",
			want: "FR-B-01 (v1) has no row in the coverage matrix (rule 3)",
		},
		{
			name: "§12.2 item missing from the coverage matrix",
			plan: []edit{
				{`, §12.2 "keyboard remapping"`, ""},
				{"| §12.2 keyboard remapping | 5 | 5 |\n", ""},
			},
			file: "PLAN.md",
			want: "§12.2 keyboard remapping (v1) has no row in the coverage matrix (rule 3)",
		},
		{
			name: "requirement named by items but without a row",
			plan: []edit{{"| FR-A-02 | 2, 2a | 2a |\n", ""}},
			file: "PLAN.md",
			want: "FR-A-02 (MVP) has no row in the coverage matrix (rule 3); items naming it: 2, 2a",
		},
		{
			name: "matrix row missing an item",
			plan: []edit{{"| FR-A-01 | 1, 2 | 2 |", "| FR-A-01 | 1 | 1 |"}},
			file: "PLAN.md",
			want: `coverage matrix row FR-A-01 lists "1", but the items' PRD lines give "1, 2"`,
		},
		{
			name: "matrix row missing an item that names it in a comma list",
			plan: []edit{{"**PRD:** FR-A-02\n", "**PRD:** FR-A-02, 03\n"}},
			file: "PLAN.md",
			want: `coverage matrix row FR-A-03 lists "4 (5)", but the items' PRD lines give "2a, 4 (5)"`,
		},
		{
			name: "matrix row missing an item that names it in a range",
			plan: []edit{{"**PRD:** FR-A-02\n", "**PRD:** FR-A-02–03\n"}},
			file: "PLAN.md",
			want: `coverage matrix row FR-A-03 lists "4 (5)", but the items' PRD lines give "2a, 4 (5)"`,
		},
		{
			name: "matrix row missing an item that names it in a list joined by and",
			plan: []edit{{"**PRD:** FR-A-02\n", "**PRD:** FR-A-02 and 03\n"}},
			file: "PLAN.md",
			want: `coverage matrix row FR-A-03 lists "4 (5)", but the items' PRD lines give "2a, 4 (5)"`,
		},
		{
			name: "PRD line with a range written with three dots",
			plan: []edit{{"**PRD:** FR-A-02\n", "**PRD:** FR-A-02...03\n"}},
			file: "PLAN.md",
			want: `item 2a: cannot read "FR-A-02...03" as a requirement ID`,
		},
		{
			name: "PRD line with a range that repeats the prefix",
			plan: []edit{{"**PRD:** FR-A-02\n", "**PRD:** FR-A-02…FR-A-03\n"}},
			file: "PLAN.md",
			want: `item 2a: cannot read "FR-A-02…FR-A-03" as a requirement ID`,
		},
		{
			name: "PRD line with a range written with to",
			plan: []edit{{"**PRD:** FR-A-02\n", "**PRD:** FR-A-02 to 03\n"}},
			file: "PLAN.md",
			want: `item 2a: cannot read "FR-A-02 to 03" as a requirement ID`,
		},
		{
			name: "duplicate coverage matrix row",
			plan: []edit{{"| FR-B-01 | 5 | 5 |\n", "| FR-B-01 | 5 | 5 |\n| FR-B-01 | 5 | 5 |\n"}},
			file: "PLAN.md",
			want: "the coverage matrix has a second row for FR-B-01",
		},
		{
			name: "coverage matrix row with a missing cell",
			plan: []edit{{"| FR-B-01 | 5 | 5 |\n", "| FR-B-01 | 5 |\n"}},
			file: "PLAN.md",
			want: "coverage matrix row has 2 cells, want 3",
		},
		{
			name: "coverage matrix row with an unreadable Complete at",
			plan: []edit{{"| FR-B-01 | 5 | 5 |\n", "| FR-B-01 | 5 | five |\n"}},
			file: "PLAN.md",
			want: `coverage matrix row FR-B-01: cannot read "five" as an item number`,
		},
		{
			name: "PRD line checked without a coverage matrix",
			plan: []edit{
				{"## Coverage matrix\n", "## Coverage\n"},
				{"**PRD:** FR-A-02\n", "**PRD:** FR-A-02, FR-C-01\n"},
			},
			file: "PLAN.md",
			want: "item 2a names FR-C-01, which is not a requirement of the PRD",
		},
		{
			name: "later-phase item not in parentheses",
			plan: []edit{{"| FR-A-03 | 4 (5) | 4 |", "| FR-A-03 | 4, 5 | 5 |"}},
			file: "PLAN.md",
			want: `coverage matrix row FR-A-03 lists "4, 5", but the items' PRD lines give "4 (5)"`,
		},
		{
			name: "wrong Complete at",
			plan: []edit{{"| FR-A-02 | 2, 2a | 2a |", "| FR-A-02 | 2, 2a | 2 |"}},
			file: "PLAN.md",
			want: "coverage matrix row FR-A-02: Complete at is 2, but the last item naming it in its own phase is 2a",
		},
		{
			name: "matrix row that no item names",
			plan: []edit{
				{"**PRD:** FR-A-01, NFR-A11Y-01/02", "**PRD:** NFR-A11Y-01/02"},
				{"**PRD:** FR-A-02, FR-A-01 (AC2)", "**PRD:** FR-A-02"},
			},
			file: "PLAN.md",
			want: "coverage matrix row FR-A-01: no item's PRD line names it",
		},
		{
			name: "matrix row for a requirement outside MVP and v1",
			plan: []edit{{"| FR-B-01 | 5 | 5 |\n", "| FR-B-01 | 5 | 5 |\n| FR-B-02 | 5 | 5 |\n"}},
			file: "PLAN.md",
			want: "coverage matrix row FR-B-02 is not an MVP or v1 requirement of the PRD",
		},
		{
			name: "PRD line naming a requirement outside MVP and v1",
			plan: []edit{{"**PRD:** FR-B-01, FR-A-03", "**PRD:** FR-B-01, FR-B-02, FR-A-03"}},
			file: "PLAN.md",
			want: "item 5 names FR-B-02, which PRD Appendix B does not place in MVP or v1",
		},
		{
			name: "PRD line naming an unknown FR",
			plan: []edit{{"**PRD:** FR-A-02\n", "**PRD:** FR-A-02, FR-C-01\n"}},
			file: "PLAN.md",
			want: "item 2a names FR-C-01, which is not a requirement of the PRD",
		},
		{
			name: "PRD line naming an unknown NFR in a slash list",
			plan: []edit{{"NFR-A11Y-01/02", "NFR-A11Y-01/03"}},
			file: "PLAN.md",
			want: "item 1 names NFR-A11Y-03, which is not a requirement of the PRD",
		},
		{
			name: "PRD line naming an unknown NFR in a comma list",
			plan: []edit{{"NFR-PERF-01..02", "NFR-PERF-01, 04"}},
			file: "PLAN.md",
			want: "item 4 names NFR-PERF-04, which is not a requirement of the PRD",
		},
		{
			name: "PRD line naming an unknown ADR",
			plan: []edit{{"ADR-001", "ADR-002"}},
			file: "PLAN.md",
			want: "item 1 names ADR-002, which is not a decision record of the PRD",
		},
		{
			name: "PRD line naming an unknown §12.2 item",
			plan: []edit{{`§12.2 "keyboard remapping"`, `§12.2 "keyboard remapping", §12.2 "dark mode"`}},
			file: "PLAN.md",
			want: `item 5 names §12.2 "dark mode", which is not in the PRD's §12.2 scope`,
		},
		{
			name: "performance target without a row",
			plan: []edit{{"| NFR-PERF-01, 02 (fixture targets) |", "| NFR-PERF-01 (fixture targets) |"}},
			file: "PLAN.md",
			want: "NFR-PERF-02, a target of the §12.1 exit criteria, has no measuring item in the performance table (rule 3)",
		},
		{
			name: "performance table naming an unknown target",
			plan: []edit{{"| NFR-PERF-03 (not an exit criterion) |", "| NFR-PERF-03, 04 (not an exit criterion) |"}},
			file: "PLAN.md",
			want: "the performance table names NFR-PERF-04, which is not a requirement of the PRD",
		},
		{
			name: "performance target with two rows",
			plan: []edit{{"| NFR-PERF-03 (not an exit criterion) | 5 |\n", "| NFR-PERF-03 (not an exit criterion) | 5 |\n| NFR-PERF-02 (again) | 5 |\n"}},
			file: "PLAN.md",
			want: "NFR-PERF-02 has a second row in the performance table",
		},
		{
			name: "performance row without an item",
			plan: []edit{{"| 4, with more benchmarks later |", "| with more benchmarks later |"}},
			file: "PLAN.md",
			want: "performance table row NFR-PERF-01, NFR-PERF-02 names no measuring item",
		},

		// Plan rule 6.
		{
			name: "performance row naming a missing item",
			plan: []edit{{"| 4, with more benchmarks later |", "| 8, with more benchmarks later |"}},
			file: "PLAN.md",
			want: "reference to item 8, which does not exist (rule 6)",
		},
		{
			name: "text reference to a missing item",
			plan: []edit{{"items 2a (later) and 4.", "items 2a (later) and 9."}},
			file: "PLAN.md",
			want: "reference to item 9, which does not exist (rule 6)",
		},
		{
			name: "parenthesised reference to a dropped item",
			plan: []edit{{"(see the parser (2a))", "(see the parser (3))"}},
			file: "PLAN.md",
			want: "reference to item 3, which is dropped (rule 6)",
		},
		{
			name: "parenthesised list joined by and naming a dropped item",
			plan: []edit{{"(see the parser (2a))", "(see the parsers (2 and 3))"}},
			file: "PLAN.md",
			want: "reference to item 3, which is dropped (rule 6)",
		},
		{
			name: "text dependency on a dropped item",
			plan: []edit{{"depends on 4 (the last MVP item)", "depends on 3 (the last MVP item)"}},
			file: "PLAN.md",
			want: "reference to item 3, which is dropped (rule 6)",
		},
		{
			name: "text range written with to ending at a dropped item",
			plan: []edit{{"the work of items 2–2a", "the work of items 1 to 3"}},
			file: "PLAN.md",
			want: "reference to item 3, which is dropped (rule 6)",
		},
		{
			name: "dependency on a dropped item",
			plan: []edit{{"**Depends on:** 1–2a", "**Depends on:** 1, 3"}},
			file: "PLAN.md",
			want: "reference to item 3, which is dropped (rule 6)",
		},
		{
			name: "dependency range ending at a dropped item",
			plan: []edit{{"**Depends on:** 1–2a", "**Depends on:** 1–3"}},
			file: "PLAN.md",
			want: "reference to item 3, which is dropped (rule 6)",
		},
		{
			name: "coverage matrix naming a dropped item",
			plan: []edit{{"| FR-A-03 | 4 (5) | 4 |", "| FR-A-03 | 3, 4 (5) | 4 |"}},
			file: "PLAN.md",
			want: "reference to item 3, which is dropped (rule 6)",
		},
		{
			name: "coverage matrix naming a missing item",
			plan: []edit{{"| FR-B-01 | 5 | 5 |", "| FR-B-01 | 5 | 6 |"}},
			file: "PLAN.md",
			want: "reference to item 6, which does not exist (rule 6)",
		},
		{
			name: "alignment note affecting a missing item",
			plan: []edit{{"| Decided | 2, 4 |", "| Decided | 2, 7 |"}},
			file: "PLAN.md",
			want: "reference to item 7, which does not exist (rule 6)",
		},
		{
			name: "unreadable alignment note",
			plan: []edit{{"| Decided | 2, 4 |", "| Decided | 2, see N2 |"}},
			file: "PLAN.md",
			want: `alignment note: cannot read "see N2" as an item number or range in its Affects column`,
		},
		{
			name: "text range running backwards",
			plan: []edit{{"the work of items 2–2a", "the work of items 2a–2"}},
			file: "PLAN.md",
			want: "range 2a–2 does not run from a lower to a higher item",
		},

		// Plan item format.
		{
			name: "unknown status",
			plan: []edit{{"**Status:** todo · **Depends on:** 1, 2\n", "**Status:** pending · **Depends on:** 1, 2\n"}},
			file: "PLAN.md",
			want: `item 2a has unknown status "pending"`,
		},
		{
			name: "item outside a phase",
			plan: []edit{{"## Phase 0 — MVP\n", "## Foundation\n"}},
			file: "PLAN.md",
			want: `item 1 is not under a "## Phase N — …" heading`,
		},
		{
			name: "unknown plan phase",
			plan: []edit{{"## Phase 1 — v1\n", "## Phase 1 — Polish\n"}},
			file: "PLAN.md",
			want: `unknown phase "Polish"`,
		},
		{
			name: "duplicate item number",
			plan: []edit{{"### 2a. Inserted", "### 2. Inserted"}},
			file: "PLAN.md",
			want: "item 2 is defined twice",
		},
		{
			name: "items out of order",
			plan: []edit{{"### 2a. Inserted", "### 1a. Inserted"}},
			file: "PLAN.md",
			want: "item 1a comes after item 2; items are listed in ascending order",
		},
		{
			name: "item without a PRD line",
			plan: []edit{{"**PRD:** FR-A-02\n- Extends", "- Extends"}},
			file: "PLAN.md",
			want: `item 2a has no "**PRD:**" line`,
		},
		{
			name: "item with a second PRD line",
			plan: []edit{{"**PRD:** FR-A-02\n- Extends", "**PRD:** FR-A-02\n**PRD:** FR-A-03\n- Extends"}},
			file: "PLAN.md",
			want: `item 2a has a second "**PRD:**" line (first on line 40)`,
		},
		{
			name: "item without a Status line",
			plan: []edit{{"**Status:** todo · **Depends on:** 1–4\n", ""}},
			file: "PLAN.md",
			want: `item 5 has no "**Status:** … · **Depends on:** …" line`,
		},
		{
			name: "item with a second Status line",
			plan: []edit{{"**Status:** todo · **Depends on:** 1–4\n", "**Status:** todo · **Depends on:** 1–4\n**Status:** todo · **Depends on:** 1\n"}},
			file: "PLAN.md",
			want: `item 5 has a second "**Status:** … · **Depends on:** …" line`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prdSrc := applyEdits(t, readFixture(t, "PRD.md"), tt.prd)
			planSrc := applyEdits(t, readFixture(t, "PLAN.md"), tt.plan)
			problems := Check("PRD.md", prdSrc, "PLAN.md", planSrc)
			if !slices.ContainsFunc(problems, func(p Problem) bool {
				return p.File == tt.file && strings.Contains(p.Msg, tt.want)
			}) {
				t.Errorf("want a problem in %s containing %q, got:%s", tt.file, tt.want, formatProblems(problems))
			}
		})
	}
}

func TestParsePRD(t *testing.T) {
	got, problems := ParsePRD("PRD.md", readFixture(t, "PRD.md"))
	if len(problems) > 0 {
		t.Fatalf("the fixture should pass, got:%s", formatProblems(problems))
	}
	want := PRD{
		Requirements: []Requirement{
			{ID: "FR-A-01", Title: "First", Phase: "MVP", Criteria: []string{"AC1"}},
			{ID: "FR-A-02", Title: "Second", Phase: "MVP", Criteria: []string{"AC1", "AC2"}},
			{ID: "FR-A-03", Title: "Third", Phase: "MVP", Criteria: []string{"AC1"}},
			{ID: "FR-B-01", Title: "Polish", Phase: "v1", Criteria: []string{"AC1", "AC2"}},
			{ID: "FR-B-02", Title: "Hosted only", Phase: "SaaS", Criteria: []string{"AC1"}},
		},
		NFRs: []string{"NFR-PERF-01", "NFR-PERF-02", "NFR-PERF-03", "NFR-A11Y-01", "NFR-A11Y-02"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParsePRD =\n%+v\nwant\n%+v", got, want)
	}

	// A blank line, as in a loose list, or a criterion's text wrapped onto
	// an indented line does not end the list.
	loose := applyEdits(t, readFixture(t, "PRD.md"), []edit{{
		"  - AC1 — It works too.\n",
		"  - AC1 — It works\n    too.\n\n  - AC3 — It follows a blank line.\n",
	}})
	got, problems = ParsePRD("PRD.md", loose)
	if len(problems) > 0 {
		t.Fatalf("a loose list should pass, got:%s", formatProblems(problems))
	}
	if want := []string{"AC1", "AC2", "AC3"}; !slices.Equal(got.Requirements[1].Criteria, want) {
		t.Errorf("FR-A-02 criteria in a loose list = %q, want %q", got.Requirements[1].Criteria, want)
	}

	// An inline label is read wherever it is, not only after a space.
	for _, inline := range []string{
		"AC1 — It shines.AC2 — It still works.",
		"AC1 — It shines (AC2 — It still works).",
		"AC1 — It shines.\u00a0AC2 — It still works.",
		"AC1 — AC2 — It still works.",
	} {
		got, problems = ParsePRD("PRD.md", applyEdits(t, readFixture(t, "PRD.md"), []edit{{"AC1 — It shines. AC2 — It still works.", inline}}))
		if len(problems) > 0 {
			t.Errorf("%q: unexpected problems:%s", inline, formatProblems(problems))
		}
		if want := []string{"AC1", "AC2"}; !slices.Equal(got.Requirements[3].Criteria, want) {
			t.Errorf("%q: FR-B-01 criteria = %q, want %q", inline, got.Requirements[3].Criteria, want)
		}
	}

	// A criterion at the margin is reported, and the list goes on after it.
	margin := applyEdits(t, readFixture(t, "PRD.md"), []edit{{
		"  - AC1 — It works too.\n",
		"  - AC1 — It works too.\n- AC3 — At the margin.\n  - AC4 — Indented again.\n",
	}})
	got, problems = ParsePRD("PRD.md", margin)
	if want := []string{"AC1", "AC2", "AC4"}; !slices.Equal(got.Requirements[1].Criteria, want) {
		t.Errorf("FR-A-02 criteria around one at the margin = %q, want %q", got.Requirements[1].Criteria, want)
	}
	if len(problems) != 2 || problems[0].Msg != "FR-A-02 has no AC3, although its criteria run to AC4" || !strings.Contains(problems[1].Msg, `"- AC3 — At the margin."`) {
		t.Errorf("a criterion at the margin: problems =%s", formatProblems(problems))
	}

	_, problems = ParsePRD("PRD.md", applyEdits(t, readFixture(t, "PRD.md"), []edit{{"**Hosted SaaS.** FR-B-02", ""}}))
	if !slices.ContainsFunc(problems, func(p Problem) bool { return p.Msg == "FR-B-02 is not listed in Appendix B" }) {
		t.Errorf("ParsePRD should check the PRD, got:%s", formatProblems(problems))
	}
}

// TestProblemsAreStable checks that a problem is reported once and that
// problems on one line keep the order of the document.
func TestProblemsAreStable(t *testing.T) {
	prdSrc := applyEdits(t, readFixture(t, "PRD.md"), []edit{{"**Hosted SaaS.** FR-B-02", "**Hosted SaaS.** FR-B-02, 05, 03, 04"}})
	planSrc := applyEdits(t, readFixture(t, "PLAN.md"), []edit{{"| FR-A-03 | 4 (5) | 4 |", "| FR-A-03 | 3, 4 (5) | 3 |"}})
	var listed []string
	dropped := 0
	for _, p := range Check("PRD.md", prdSrc, "PLAN.md", planSrc) {
		if rest, ok := strings.CutPrefix(p.Msg, "Appendix B lists "); ok {
			id, _, _ := strings.Cut(rest, ",")
			listed = append(listed, id)
		}
		if p.Msg == "reference to item 3, which is dropped (rule 6)" {
			dropped++
		}
	}
	if want := []string{"FR-B-05", "FR-B-03", "FR-B-04"}; !slices.Equal(listed, want) {
		t.Errorf("Appendix B problems name %q, want %q", listed, want)
	}
	if dropped != 1 {
		t.Errorf("the dropped item in the coverage matrix row is reported %d times, want 1", dropped)
	}
}

func TestScanRefs(t *testing.T) {
	tests := []struct {
		text string
		bare bool
		want []string
	}{
		{"Builds on item 1, and on items 2a (later) and 4.", false, []string{"1", "2a", "4"}},
		{"Items 33–36 register their entities.", false, []string{"33–36"}},
		{"Routes land in items 16 to 26.", false, []string{"16–26"}},
		{"Item 27 attaches them to every route.", false, []string{"27"}},
		{"Items 80 (diff) and 83 (authoring export) add the rest.", false, []string{"80", "83"}},
		{"See item 5 or 6.", false, []string{"5", "6"}},
		{"Item 119, which makes a gap fail the build.", false, []string{"119"}},
		{"The item-32 validator.", false, []string{"32"}},
		{"Item 1's plan check.", false, []string{"1"}},
		{"Each Phase 1 item depends on 119 (the MVP release).", false, []string{"119"}},
		{"**Status:** todo · **Depends on:** 1, 2", false, nil},
		{"The item 1st is not a reference.", false, nil},
		{"A reference in code, `item 999`, is ignored.", false, nil},
		{"Token management (26), import (84), and deletion (110).", true, []string{"26", "84", "110"}},
		{"Token management (26).", false, nil},
		{"Routes land with their features (16–26).", true, []string{"16–26"}},
		{"The exporters (80 and 83), or (26, and 84).", true, []string{"80", "83", "26", "84"}},
		{"Position as `numeric(12,2)` and (2 today).", true, nil},
	}
	for _, tt := range tests {
		var got []string
		for _, r := range scanRefs(tt.text, 1, tt.bare) {
			got = append(got, r.String())
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("scanRefs(%q, bare=%v) = %q, want %q", tt.text, tt.bare, got, tt.want)
		}
	}
}

func TestExpandIDs(t *testing.T) {
	tests := []struct {
		list string
		want []string
	}{
		{"FR-PUB-01", []string{"FR-PUB-01"}},
		{"FR-TEN-01…03, 07", []string{"FR-TEN-01", "FR-TEN-02", "FR-TEN-03", "FR-TEN-07"}},
		{"NFR-PERF-01, 02, 18", []string{"NFR-PERF-01", "NFR-PERF-02", "NFR-PERF-18"}},
		{"NFR-SEO-01..03", []string{"NFR-SEO-01", "NFR-SEO-02", "NFR-SEO-03"}},
		{"NFR-A11Y-03, FR-RT-01", []string{"NFR-A11Y-03", "FR-RT-01"}},
		{"FR-TEN-02/03, 05–06", []string{"FR-TEN-02", "FR-TEN-03", "FR-TEN-05", "FR-TEN-06"}},
		{"NFR-PERF-01, 02, and 18", []string{"NFR-PERF-01", "NFR-PERF-02", "NFR-PERF-18"}},
		{"FR-TEN-01 and 03…04", []string{"FR-TEN-01", "FR-TEN-03", "FR-TEN-04"}},
	}
	for _, tt := range tests {
		got, err := expandIDs(tt.list)
		if err != nil || !slices.Equal(got, tt.want) {
			t.Errorf("expandIDs(%q) = %q, %v; want %q", tt.list, got, err, tt.want)
		}
	}
	for _, bad := range []string{
		"07", "FR-TEN-05…01", "keyboard remapping", "FR-TEN-01, ",
		"FR-TEN-01...05", "FR-TEN-01…FR-TEN-05", "FR-TEN-01 to 05", "FR-TEN-01-05",
		"FR-TEN-01, and",
	} {
		if got, err := expandIDs(bad); err == nil {
			t.Errorf("expandIDs(%q) = %q, want an error", bad, got)
		}
	}
}

func TestItemNumOrder(t *testing.T) {
	order := []string{"45", "45a", "45b", "46", "100"}
	for i := 1; i < len(order); i++ {
		a, _ := parseItemNum(order[i-1])
		b, _ := parseItemNum(order[i])
		if !a.less(b) || b.less(a) {
			t.Errorf("want %s < %s", a, b)
		}
	}
}
