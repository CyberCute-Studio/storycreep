// Package doccheck checks the PRD and the implementation plan against the
// rules they state about themselves.
//
// For the PRD (§4.0), every "Depends on" field names known requirements, the
// dependency graph is acyclic, every requirement's acceptance criteria are
// numbered AC1 to ACn, and Appendix B agrees with each requirement's Phase
// field. For the plan, it checks rule 1 (dependencies point to earlier items),
// rule 3 (the coverage matrix covers the MVP and v1 scope, agrees with the
// items' PRD lines, and every §12.1 performance target has a measuring item),
// and rule 6 (every item reference resolves to a live item).
//
// ParsePRD gives other tools, such as the traceability report, the
// requirements and acceptance criteria as this package reads them.
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

// Requirement is a functional requirement as other tools read it from the
// PRD.
type Requirement struct {
	ID       string
	Title    string
	Phase    string   // the phase code Appendix B lists it under
	Criteria []string // its acceptance criteria, "AC1" to "ACn"
}

// PRD is what other tools read from the PRD: its functional requirements in
// document order, and the IDs of its non-functional requirements.
type PRD struct {
	Requirements []Requirement
	NFRs         []string
}

// ParsePRD reads a PRD given as text and checks it alone, as Check does. The
// file name is used only to locate problems. The PRD it returns is complete
// only when there are no problems.
func ParsePRD(file, src string) (PRD, []Problem) {
	var r reporter
	p := parsePRD(file, src, &r)
	checkPRD(p, &r)
	slices.SortStableFunc(r.problems, func(a, b Problem) int { return cmp.Compare(a.Line, b.Line) })
	out := PRD{NFRs: slices.Clone(p.nfrIDs)}
	for _, id := range p.ids {
		req := p.reqs[id]
		phase := p.scope[id]
		if phase == "" {
			phase = req.phase
		}
		out.Requirements = append(out.Requirements, Requirement{
			ID:       id,
			Title:    req.title,
			Phase:    phase,
			Criteria: req.criterionIDs(),
		})
	}
	return out, r.problems
}

// Phases returns the PRD's phase codes in delivery order (§4.0).
func Phases() []string {
	return slices.Clone(phases)
}

type reporter struct {
	problems []Problem
}

func (r *reporter) add(file string, line int, format string, args ...any) {
	r.problems = append(r.problems, Problem{File: file, Line: line, Msg: fmt.Sprintf(format, args...)})
}
