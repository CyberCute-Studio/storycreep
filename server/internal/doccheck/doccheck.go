// Package doccheck checks the PRD and the implementation plan against the
// rules they state about themselves.
//
// For the PRD (§4.0), every "Depends on" field names known requirements, the
// dependency graph is acyclic, and Appendix B agrees with each requirement's
// Phase field. For the plan, it checks rule 1 (dependencies point to earlier
// items), rule 3 (the coverage matrix covers the MVP and v1 scope, agrees with
// the items' PRD lines, and every §12.1 performance target has a measuring
// item), and rule 6 (every item reference resolves to a live item).
package doccheck

import (
	"cmp"
	"fmt"
	"os"
	"slices"
)

// Problem is one rule violation, located at a line of a checked document.
type Problem struct {
	File string
	Line int
	Msg  string
}

func (p Problem) String() string {
	return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Msg)
}

// CheckFiles reads the PRD and the plan and returns every problem found in
// them. An error means a file could not be read.
func CheckFiles(prdPath, planPath string) ([]Problem, error) {
	prdSrc, err := os.ReadFile(prdPath)
	if err != nil {
		return nil, err
	}
	planSrc, err := os.ReadFile(planPath)
	if err != nil {
		return nil, err
	}
	return Check(prdPath, string(prdSrc), planPath, string(planSrc)), nil
}

// Check checks a PRD and a plan given as text. The file names are used only
// to locate problems. Problems are sorted by file and line.
func Check(prdFile, prdSrc, planFile, planSrc string) []Problem {
	var r reporter
	p := parsePRD(prdFile, prdSrc, &r)
	checkPRD(p, &r)
	pl := parsePlan(planFile, planSrc, &r)
	checkPlan(p, pl, &r)
	slices.SortStableFunc(r.problems, func(a, b Problem) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line))
	})
	return r.problems
}

type reporter struct {
	problems []Problem
}

func (r *reporter) add(file string, line int, format string, args ...any) {
	r.problems = append(r.problems, Problem{File: file, Line: line, Msg: fmt.Sprintf(format, args...)})
}
