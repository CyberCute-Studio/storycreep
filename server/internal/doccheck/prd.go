package doccheck

import (
	"cmp"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// phases lists the PRD's phases in delivery order, by the codes its Phase
// fields use (PRD §4.0).
var phases = []string{"MVP", "v1", "Collab", "SaaS", "Later"}

func phaseRank(phase string) int {
	return slices.Index(phases, phase)
}

// appendixPhases maps the labels of PRD Appendix B to phase codes.
var appendixPhases = map[string]string{
	"MVP":           "MVP",
	"v1":            "v1",
	"Collaboration": "Collab",
	"Hosted SaaS":   "SaaS",
	"Later":         "Later",
}

type requirement struct {
	id          string
	line        int
	phase       string
	depends     []string
	dependsLine int
}

type prd struct {
	file        string
	reqs        map[string]*requirement
	ids         []string // requirement IDs in document order
	nfrs        map[string]bool
	adrs        map[string]bool
	scope       map[string]string // FR ID → phase code, from Appendix B
	scopeLine   map[string]int
	v1Extras    map[string]bool // the items of the §12.2 scope that are not FRs
	perfTargets []string        // the NFR-PERF IDs named by the §12.1 exit criteria
	perfLine    int
}

var (
	reqHeadingRE   = regexp.MustCompile(`^(FR-[A-Z0-9]+-\d+)\b`)
	adrHeadingRE   = regexp.MustCompile(`^(ADR-\d+)\b`)
	nfrRowRE       = regexp.MustCompile(`^\|\s*(NFR-[A-Z0-9]+-\d+)\s*\|`)
	phaseFieldRE   = regexp.MustCompile(`\*\*Phase:\*\*\s*([^·*]+?)\s*(?:·|$)`)
	dependsFieldRE = regexp.MustCompile(`\*\*Depends on:\*\*\s*(.*?)\s*$`)
	frIDRE         = regexp.MustCompile(`^FR-[A-Z0-9]+-\d+$`)
	scopeLineRE    = regexp.MustCompile(`^\*\*([^*]+?)\.\*\*\s*(.*)$`)
	perfRefRE      = regexp.MustCompile(`NFR-PERF-\d+(?:\s*(?:…|\.\.)\s*\d+)?`)
)

func parsePRD(file, src string, r *reporter) *prd {
	d := newDocument(src)
	p := &prd{
		file:      file,
		reqs:      map[string]*requirement{},
		nfrs:      map[string]bool{},
		adrs:      map[string]bool{},
		scope:     map[string]string{},
		scopeLine: map[string]int{},
		v1Extras:  map[string]bool{},
	}
	for _, s := range d.sections() {
		if m := reqHeadingRE.FindStringSubmatch(s.text); m != nil && s.level == 4 {
			p.parseRequirement(d, s, m[1], r)
		}
		if m := adrHeadingRE.FindStringSubmatch(s.text); m != nil && s.level == 3 {
			p.adrs[m[1]] = true
		}
	}
	for _, l := range d.lines {
		if m := nfrRowRE.FindStringSubmatch(l); m != nil {
			p.nfrs[m[1]] = true
		}
	}
	p.parseAppendixB(d, r)
	p.parseV1Scope(d, r)
	p.parsePerfTargets(d, r)
	return p
}

func (p *prd) parseRequirement(d *document, s section, id string, r *reporter) {
	if prev, dup := p.reqs[id]; dup {
		r.add(p.file, s.line, "%s is defined twice (also on line %d)", id, prev.line)
		return
	}
	req := &requirement{id: id, line: s.line}
	p.reqs[id] = req
	p.ids = append(p.ids, id)
	for n := s.start; n < s.end; n++ {
		l := d.line(n)
		if m := phaseFieldRE.FindStringSubmatch(l); m != nil && req.phase == "" {
			req.phase = m[1]
		}
		m := dependsFieldRE.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if req.dependsLine != 0 {
			r.add(p.file, n, "%s has a second Depends on field (first on line %d)", id, req.dependsLine)
			continue
		}
		req.dependsLine = n
		if m[1] == "—" {
			continue
		}
		for _, tok := range strings.Split(m[1], ",") {
			tok = strings.TrimSpace(tok)
			if !frIDRE.MatchString(tok) {
				r.add(p.file, n, "%s: cannot read %q in its Depends on field as a requirement ID (write \"—\" for none)", id, tok)
				continue
			}
			req.depends = append(req.depends, tok)
		}
	}
	switch {
	case req.phase == "":
		r.add(p.file, s.line, "%s has no Phase field", id)
	case phaseRank(req.phase) < 0:
		r.add(p.file, s.line, "%s has unknown phase %q (want one of %s)", id, req.phase, strings.Join(phases, ", "))
	}
	if req.dependsLine == 0 {
		r.add(p.file, s.line, "%s has no Depends on field", id)
	}
}

// splitScope splits a scope list such as Appendix B's "FR-PUB-01, 02 ·
// FR-AUTH-01…05" into requirement IDs and the entries that are not FRs.
func splitScope(s string) (ids, others []string, err error) {
	for _, seg := range strings.Split(s, "·") {
		seg = strings.TrimSuffix(strings.TrimSpace(seg), ".")
		switch {
		case seg == "":
		case strings.HasPrefix(seg, "FR-"):
			expanded, err := expandIDs(seg)
			if err != nil {
				return nil, nil, err
			}
			ids = append(ids, expanded...)
		default:
			others = append(others, seg)
		}
	}
	return ids, others, nil
}

func (p *prd) parseAppendixB(d *document, r *reporter) {
	s, ok := d.findSection(2, "Appendix B")
	if !ok {
		r.add(p.file, 1, "no \"## Appendix B\" section, so the requirements' phases are unknown")
		return
	}
	for n := s.start; n < s.end; n++ {
		m := scopeLineRE.FindStringSubmatch(d.line(n))
		if m == nil {
			continue
		}
		phase, ok := appendixPhases[m[1]]
		if !ok {
			r.add(p.file, n, "Appendix B: unknown phase %q", m[1])
			continue
		}
		ids, others, err := splitScope(m[2])
		if err != nil {
			r.add(p.file, n, "Appendix B: %v", err)
			continue
		}
		for _, o := range others {
			r.add(p.file, n, "Appendix B: %q is not a requirement ID", o)
		}
		for _, id := range ids {
			if prev, dup := p.scope[id]; dup {
				r.add(p.file, n, "Appendix B lists %s under both %s and %s", id, prev, phase)
				continue
			}
			p.scope[id] = phase
			p.scopeLine[id] = n
		}
	}
	if len(p.scope) == 0 {
		r.add(p.file, s.line, "Appendix B lists no requirements")
	}
}

func (p *prd) parseV1Scope(d *document, r *reporter) {
	s, ok := d.findSection(3, "12.2 ")
	if !ok {
		r.add(p.file, 1, "no \"### 12.2\" section, so the v1 scope is unknown")
		return
	}
	for n := s.start; n < s.end; n++ {
		rest, ok := strings.CutPrefix(d.line(n), "**In scope.**")
		if !ok {
			continue
		}
		_, others, err := splitScope(rest)
		if err != nil {
			r.add(p.file, n, "§12.2 scope: %v", err)
		}
		for _, o := range others {
			p.v1Extras[o] = true
		}
		return
	}
	r.add(p.file, s.line, "§12.2 has no \"**In scope.**\" line")
}

func (p *prd) parsePerfTargets(d *document, r *reporter) {
	s, ok := d.findSection(3, "12.1 ")
	if !ok {
		r.add(p.file, 1, "no \"### 12.1\" section, so the MVP performance targets are unknown")
		return
	}
	n := s.start
	for n < s.end && !strings.HasPrefix(d.line(n), "**Exit criteria.**") {
		n++
	}
	for ; n < s.end; n++ {
		for _, ref := range perfRefRE.FindAllString(d.line(n), -1) {
			ids, err := expandIDs(ref)
			if err != nil {
				r.add(p.file, n, "§12.1 exit criteria: %v", err)
				continue
			}
			p.perfTargets = append(p.perfTargets, ids...)
			if p.perfLine == 0 {
				p.perfLine = n
			}
		}
	}
	if len(p.perfTargets) == 0 {
		r.add(p.file, s.line, "the §12.1 exit criteria name no NFR-PERF targets")
	}
}

func checkPRD(p *prd, r *reporter) {
	for _, id := range p.ids {
		req := p.reqs[id]
		for _, dep := range req.depends {
			if p.reqs[dep] == nil {
				r.add(p.file, req.dependsLine, "%s depends on %s, which is not a requirement of this PRD", id, dep)
			}
		}
	}
	for _, c := range p.cycles() {
		r.add(p.file, p.reqs[c[0]].dependsLine, "dependency cycle: %s", strings.Join(append(c, c[0]), " → "))
	}

	for _, id := range p.ids {
		req := p.reqs[id]
		phase, listed := p.scope[id]
		switch {
		case !listed:
			r.add(p.file, req.line, "%s is not listed in Appendix B", id)
		case req.phase != "" && phase != req.phase:
			r.add(p.file, req.line, "%s has Phase %s, but Appendix B lists it under %s", id, req.phase, phase)
		}
	}
	listed := slices.SortedFunc(maps.Keys(p.scopeLine), func(a, b string) int {
		return cmp.Compare(p.scopeLine[a], p.scopeLine[b])
	})
	for _, id := range listed {
		if p.reqs[id] == nil {
			r.add(p.file, p.scopeLine[id], "Appendix B lists %s, which is not a requirement of this PRD", id)
		}
	}
	for _, t := range p.perfTargets {
		if !p.nfrs[t] {
			r.add(p.file, p.perfLine, "the §12.1 exit criteria name %s, which is not defined", t)
		}
	}
}

// cycles returns dependency cycles among the requirements, each rotated to
// start at its smallest ID. Every cyclic part of the graph yields at least one.
func (p *prd) cycles() [][]string {
	const (
		unvisited = iota
		onStack
		done
	)
	state := map[string]int{}
	var stack []string
	var found [][]string
	seen := map[string]bool{}
	var visit func(id string)
	visit = func(id string) {
		state[id] = onStack
		stack = append(stack, id)
		for _, dep := range p.reqs[id].depends {
			if p.reqs[dep] == nil {
				continue
			}
			switch state[dep] {
			case unvisited:
				visit(dep)
			case onStack:
				c := slices.Clone(stack[slices.Index(stack, dep):])
				least := slices.Index(c, slices.Min(c))
				c = slices.Concat(c[least:], c[:least])
				if key := strings.Join(c, " "); !seen[key] {
					seen[key] = true
					found = append(found, c)
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = done
	}
	for _, id := range p.ids {
		if state[id] == unvisited {
			visit(id)
		}
	}
	return found
}
