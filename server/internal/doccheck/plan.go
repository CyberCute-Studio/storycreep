package doccheck

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

type item struct {
	num        itemNum
	line       int
	phase      string // the phase code of the enclosing "## Phase N — …" heading
	status     string // todo, in progress, done, or dropped
	statusLine int
	depends    []itemRef
	prd        string // the text of the **PRD:** line
	prdLine    int
}

// matrixRow is a row of the coverage matrix. The PRs written in parentheses
// are later-phase extensions, listed in later.
type matrixRow struct {
	line       int
	key        string
	regular    []itemNum
	later      []itemNum
	completeAt itemNum
}

// perfRow is a row of the performance table.
type perfRow struct {
	line    int
	targets []string
	owners  []itemRef
}

type plan struct {
	file       string
	items      []*item
	byNum      map[itemNum]*item
	matrix     []matrixRow
	matrixLine int // the coverage matrix heading; 0 if there is none
	perf       []perfRow
	perfLine   int // the performance table heading; 0 if there is none
	refs       []itemRef
}

var (
	phaseHeadingRE = regexp.MustCompile(`^Phase\s+\d+\s+—\s+(\S+)`)
	itemHeadingRE  = regexp.MustCompile(`^(\d+[a-z]?)\.\s+\S`)
	statusFieldRE  = regexp.MustCompile(`^\*\*Status:\*\*\s*(.*?)\s*·\s*\*\*Depends on:\*\*\s*(.*?)\s*$`)
	statusRE       = regexp.MustCompile(`^(?:(todo|in progress)|(done|dropped)(?:\s.*)?)$`)
	prdFieldRE     = regexp.MustCompile(`^\*\*PRD:\*\*\s*(.*?)\s*$`)
	parenRE        = regexp.MustCompile(`\(([^)]*)\)`)

	// The requirement references of a **PRD:** line, in the compact forms
	// the plan uses: "FR-TEN-02/03/04" and "NFR-SEO-01..06" name three and six
	// IDs. The leading group keeps "NFR-…" from matching as "FR-…".
	frRefRE  = regexp.MustCompile(`(?:^|[^A-Za-z0-9-])(FR-[A-Z0-9]+-\d+(?:/\d+)*(?:\s*(?:…|\.\.)\s*\d+)?)`)
	nfrRefRE = regexp.MustCompile(`(?:^|[^A-Za-z0-9-])(NFR-[A-Z0-9]+-\d+(?:/\d+)*(?:\s*(?:…|\.\.)\s*\d+)?)`)
	adrRefRE = regexp.MustCompile(`(?:^|[^A-Za-z0-9-])(ADR-\d+)`)
	v1RefRE  = regexp.MustCompile(`§12\.2\s+"([^"]+)"`)
)

func parsePlan(file, src string, r *reporter) *plan {
	d := newDocument(src)
	pl := &plan{file: file, byNum: map[itemNum]*item{}}
	inItem := map[int]bool{}
	var changeLog section
	phase := ""
	for _, s := range d.sections() {
		switch s.level {
		case 2:
			phase = ""
			if m := phaseHeadingRE.FindStringSubmatch(s.text); m != nil {
				phase = m[1]
				if phaseRank(phase) < 0 {
					r.add(file, s.line, "unknown phase %q (want one of %s)", phase, strings.Join(phases, ", "))
				}
			}
			switch s.text {
			case "Coverage matrix":
				pl.parseMatrix(d, s, r)
			case "Alignment notes":
				pl.parseNotes(d, s, r)
			case "Change log":
				changeLog = s
			}
		case 3:
			if s.text == "Performance targets" {
				pl.parsePerf(d, s, r)
			}
			if itemHeadingRE.MatchString(s.text) {
				pl.parseItem(d, s, phase, r)
				for n := s.start; n < s.end; n++ {
					inItem[n] = true
				}
			}
		}
	}
	// The change log is history: it may name items that were dropped since.
	for n := 1; n <= len(d.lines); n++ {
		if changeLog.line != 0 && n >= changeLog.line && n < changeLog.end {
			continue
		}
		pl.refs = append(pl.refs, scanRefs(d.line(n), n, inItem[n])...)
	}
	return pl
}

func (pl *plan) parseItem(d *document, s section, phase string, r *reporter) {
	num, _ := parseItemNum(itemHeadingRE.FindStringSubmatch(s.text)[1])
	if prev, dup := pl.byNum[num]; dup {
		r.add(pl.file, s.line, "item %s is defined twice (also on line %d)", num, prev.line)
		return
	}
	if phase == "" {
		r.add(pl.file, s.line, "item %s is not under a \"## Phase N — …\" heading", num)
	}
	if n := len(pl.items); n > 0 && !pl.items[n-1].num.less(num) {
		r.add(pl.file, s.line, "item %s comes after item %s; items are listed in ascending order", num, pl.items[n-1].num)
	}
	it := &item{num: num, line: s.line, phase: phase}
	pl.items = append(pl.items, it)
	pl.byNum[num] = it
	for n := s.start; n < s.end; n++ {
		l := d.line(n)
		if m := prdFieldRE.FindStringSubmatch(l); m != nil && it.prdLine == 0 {
			it.prd, it.prdLine = m[1], n
		}
		m := statusFieldRE.FindStringSubmatch(l)
		if m == nil || it.statusLine != 0 {
			continue
		}
		it.statusLine = n
		if sm := statusRE.FindStringSubmatch(m[1]); sm != nil {
			it.status = sm[1] + sm[2]
		} else {
			r.add(pl.file, n, "item %s has unknown status %q (want todo, in progress, done, or dropped)", num, m[1])
		}
		if m[2] != "—" {
			refs, err := parseItems(m[2], n)
			if err != nil {
				r.add(pl.file, n, "item %s: %v in its Depends on field", num, err)
			}
			it.depends = refs
		}
	}
	if it.statusLine == 0 {
		r.add(pl.file, s.line, "item %s has no \"**Status:** … · **Depends on:** …\" line", num)
	}
	if it.prdLine == 0 {
		r.add(pl.file, s.line, "item %s has no \"**PRD:**\" line", num)
	}
}

func (pl *plan) parseMatrix(d *document, s section, r *reporter) {
	pl.matrixLine = s.line
	rows, ok := d.table(s.start, s.end)
	if !ok {
		r.add(pl.file, s.line, "the coverage matrix section has no table")
		return
	}
	for _, row := range rows {
		if len(row.cells) != 3 {
			r.add(pl.file, row.line, "coverage matrix row has %d cells, want 3", len(row.cells))
			continue
		}
		key := row.cells[0]
		regular, later, err := parsePRs(row.cells[1])
		if err != nil {
			r.add(pl.file, row.line, "coverage matrix row %s: %v", key, err)
			continue
		}
		complete, ok := parseItemNum(row.cells[2])
		if !ok {
			r.add(pl.file, row.line, "coverage matrix row %s: cannot read %q as an item number", key, row.cells[2])
			continue
		}
		pl.matrix = append(pl.matrix, matrixRow{line: row.line, key: key, regular: regular, later: later, completeAt: complete})
		for _, n := range slices.Concat(regular, later, []itemNum{complete}) {
			pl.refs = append(pl.refs, itemRef{lo: n, hi: n, line: row.line})
		}
	}
}

// parsePRs parses the PRs cell of a coverage matrix row, such as
// "26, 27, 54, 83 (123)", where the parenthesised items are later extensions.
func parsePRs(s string) (regular, later []itemNum, err error) {
	for _, m := range parenRE.FindAllStringSubmatch(s, -1) {
		nums, err := parseNums(m[1])
		if err != nil {
			return nil, nil, err
		}
		later = append(later, nums...)
	}
	if rest := strings.TrimSpace(parenRE.ReplaceAllString(s, "")); rest != "" {
		if regular, err = parseNums(rest); err != nil {
			return nil, nil, err
		}
	}
	return regular, later, nil
}

func parseNums(s string) ([]itemNum, error) {
	var nums []itemNum
	for _, tok := range strings.Split(s, ",") {
		n, ok := parseItemNum(strings.TrimSpace(tok))
		if !ok {
			return nil, fmt.Errorf("cannot read %q as an item number", strings.TrimSpace(tok))
		}
		nums = append(nums, n)
	}
	return nums, nil
}

func (pl *plan) parsePerf(d *document, s section, r *reporter) {
	pl.perfLine = s.line
	rows, ok := d.table(s.start, s.end)
	if !ok {
		r.add(pl.file, s.line, "the performance targets section has no table")
		return
	}
	for _, row := range rows {
		if len(row.cells) != 2 {
			r.add(pl.file, row.line, "performance table row has %d cells, want 2", len(row.cells))
			continue
		}
		targets, err := expandIDs(strings.TrimSpace(parenRE.ReplaceAllString(row.cells[0], "")))
		if err != nil {
			r.add(pl.file, row.line, "performance table: %v", err)
			continue
		}
		owners := scanList(row.cells[1], row.line)
		pl.perf = append(pl.perf, perfRow{line: row.line, targets: targets, owners: owners})
		pl.refs = append(pl.refs, owners...)
	}
}

func (pl *plan) parseNotes(d *document, s section, r *reporter) {
	rows, ok := d.table(s.start, s.end)
	if !ok {
		return
	}
	for _, row := range rows {
		affects := row.cells[len(row.cells)-1]
		if affects == "—" || affects == "" {
			continue
		}
		refs, err := parseItems(affects, row.line)
		if err != nil {
			r.add(pl.file, row.line, "alignment note: %v in its Affects column", err)
			continue
		}
		pl.refs = append(pl.refs, refs...)
	}
}

func checkPlan(p *prd, pl *plan, r *reporter) {
	if len(pl.items) == 0 {
		r.add(pl.file, 1, "no items found")
		return
	}
	for _, it := range pl.items {
		for _, dep := range it.depends {
			pl.checkRef(dep, r)
			if !dep.hi.less(it.num) {
				r.add(pl.file, dep.line, "item %s depends on %s, which is not an earlier item (rule 1)", it.num, dep)
			}
		}
	}
	for _, ref := range pl.refs {
		pl.checkRef(ref, r)
	}
	pl.checkMatrix(p, r)
	pl.checkPerf(p, r)
}

// checkRef reports a reference to an item that does not exist or is dropped
// (rule 6). A range must start and end at live items; the dropped items
// inside it are skipped.
func (pl *plan) checkRef(ref itemRef, r *reporter) {
	ends := []itemNum{ref.lo}
	if ref.rng {
		ends = append(ends, ref.hi)
		if !ref.lo.less(ref.hi) {
			r.add(pl.file, ref.line, "range %s does not run from a lower to a higher item", ref)
		}
	}
	for _, n := range ends {
		switch it := pl.byNum[n]; {
		case it == nil:
			r.add(pl.file, ref.line, "reference to item %s, which does not exist (rule 6)", n)
		case it.status == "dropped":
			r.add(pl.file, ref.line, "reference to item %s, which is dropped (rule 6)", n)
		}
	}
}

// coverageKeys returns the coverage matrix keys that an item's PRD line names:
// MVP and v1 FR IDs, and §12.2 scope items written as §12.2 "name". It
// reports references to requirements the PRD does not define or does not
// place in MVP or v1.
func (pl *plan) coverageKeys(p *prd, it *item, r *reporter) []string {
	text := stripCode(it.prd)
	var keys []string
	seen := map[string]bool{}
	for _, id := range pl.expandRefs(frRefRE, text, it, r) {
		if seen[id] {
			continue
		}
		seen[id] = true
		switch phase := p.scope[id]; {
		case p.reqs[id] == nil:
			r.add(pl.file, it.prdLine, "item %s names %s, which is not a requirement of the PRD", it.num, id)
		case phase != "MVP" && phase != "v1":
			r.add(pl.file, it.prdLine, "item %s names %s, which PRD Appendix B does not place in MVP or v1; the plan covers only those phases", it.num, id)
		default:
			keys = append(keys, id)
		}
	}
	for _, m := range v1RefRE.FindAllStringSubmatch(text, -1) {
		key := "§12.2 " + m[1]
		if seen[key] {
			continue
		}
		seen[key] = true
		if !p.v1Extras[m[1]] {
			r.add(pl.file, it.prdLine, "item %s names §12.2 %q, which is not in the PRD's §12.2 scope", it.num, m[1])
			continue
		}
		keys = append(keys, key)
	}
	for _, id := range pl.expandRefs(nfrRefRE, text, it, r) {
		if !p.nfrs[id] {
			r.add(pl.file, it.prdLine, "item %s names %s, which is not a requirement of the PRD", it.num, id)
		}
	}
	for _, m := range adrRefRE.FindAllStringSubmatch(text, -1) {
		if !p.adrs[m[1]] {
			r.add(pl.file, it.prdLine, "item %s names %s, which is not a decision record of the PRD", it.num, m[1])
		}
	}
	return keys
}

// expandRefs returns the IDs that re's matches in text name, expanding the
// compact forms. It reports a match it cannot read.
func (pl *plan) expandRefs(re *regexp.Regexp, text string, it *item, r *reporter) []string {
	var ids []string
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		expanded, err := expandIDs(strings.ReplaceAll(m[1], "/", ", "))
		if err != nil {
			r.add(pl.file, it.prdLine, "item %s: %v", it.num, err)
			continue
		}
		ids = append(ids, expanded...)
	}
	return ids
}

// keyPhase returns the phase of a coverage matrix key, or "" if the PRD does
// not place it in MVP or v1.
func (p *prd) keyPhase(key string) string {
	if extra, ok := strings.CutPrefix(key, "§12.2 "); ok {
		if p.v1Extras[extra] {
			return "v1"
		}
		return ""
	}
	if phase := p.scope[key]; p.reqs[key] != nil && (phase == "MVP" || phase == "v1") {
		return phase
	}
	return ""
}

// checkMatrix checks rule 3: the coverage matrix has a row for every MVP and
// v1 requirement, and each row equals the one derived from the items' PRD
// lines. An item touches a requirement exactly when its PRD line names it; an
// item of a later phase than the requirement is a parenthesised extension,
// and the last other item completes it.
func (pl *plan) checkMatrix(p *prd, r *reporter) {
	if pl.matrixLine == 0 {
		r.add(pl.file, 1, "no \"## Coverage matrix\" section")
		return
	}
	derived := map[string][]*item{}
	for _, it := range pl.items {
		if it.status == "dropped" {
			continue
		}
		for _, key := range pl.coverageKeys(p, it, r) {
			derived[key] = append(derived[key], it)
		}
	}

	rows := map[string]matrixRow{}
	for _, row := range pl.matrix {
		if prev, dup := rows[row.key]; dup {
			r.add(pl.file, row.line, "the coverage matrix has a second row for %s (first on line %d)", row.key, prev.line)
			continue
		}
		rows[row.key] = row
		phase := p.keyPhase(row.key)
		its := derived[row.key]
		switch {
		case phase == "":
			r.add(pl.file, row.line, "coverage matrix row %s is not an MVP or v1 requirement of the PRD", row.key)
			continue
		case len(its) == 0:
			r.add(pl.file, row.line, "coverage matrix row %s: no item's PRD line names it", row.key)
			continue
		}
		var regular, later []itemNum
		for _, it := range its {
			if phaseRank(it.phase) > phaseRank(phase) {
				later = append(later, it.num)
			} else {
				regular = append(regular, it.num)
			}
		}
		if !slices.Equal(row.regular, regular) || !slices.Equal(row.later, later) {
			r.add(pl.file, row.line, "coverage matrix row %s lists %q, but the items' PRD lines give %q",
				row.key, formatPRs(row.regular, row.later), formatPRs(regular, later))
		}
		if len(regular) == 0 {
			r.add(pl.file, row.line, "coverage matrix row %s: no %s item names it, so nothing completes it", row.key, phase)
		} else if last := regular[len(regular)-1]; row.completeAt != last {
			r.add(pl.file, row.line, "coverage matrix row %s: Complete at is %s, but the last item naming it in its own phase is %s", row.key, row.completeAt, last)
		}
	}

	var required []string
	for _, id := range p.ids {
		if p.keyPhase(id) != "" {
			required = append(required, id)
		}
	}
	for _, extra := range slices.Sorted(maps.Keys(p.v1Extras)) {
		required = append(required, "§12.2 "+extra)
	}
	for _, key := range required {
		if _, ok := rows[key]; ok {
			continue
		}
		msg := fmt.Sprintf("%s (%s) has no row in the coverage matrix (rule 3)", key, p.keyPhase(key))
		if its := derived[key]; len(its) > 0 {
			nums := make([]string, len(its))
			for i, it := range its {
				nums[i] = it.num.String()
			}
			msg += "; items naming it: " + strings.Join(nums, ", ")
		}
		r.add(pl.file, pl.matrixLine, "%s", msg)
	}
}

func formatPRs(regular, later []itemNum) string {
	join := func(nums []itemNum) string {
		s := make([]string, len(nums))
		for i, n := range nums {
			s[i] = n.String()
		}
		return strings.Join(s, ", ")
	}
	switch {
	case len(later) == 0:
		return join(regular)
	case len(regular) == 0:
		return "(" + join(later) + ")"
	}
	return join(regular) + " (" + join(later) + ")"
}

// checkPerf checks rule 3 for the performance table: every NFR-PERF target
// that the §12.1 exit criteria name has a row with at least one item.
func (pl *plan) checkPerf(p *prd, r *reporter) {
	if pl.perfLine == 0 {
		r.add(pl.file, 1, "no \"### Performance targets\" section")
		return
	}
	rowOf := map[string]perfRow{}
	for _, row := range pl.perf {
		if len(row.owners) == 0 {
			r.add(pl.file, row.line, "performance table row %s names no measuring item", strings.Join(row.targets, ", "))
		}
		for _, t := range row.targets {
			if !p.nfrs[t] {
				r.add(pl.file, row.line, "the performance table names %s, which is not a requirement of the PRD", t)
			}
			if prev, dup := rowOf[t]; dup {
				r.add(pl.file, row.line, "%s has a second row in the performance table (first on line %d)", t, prev.line)
				continue
			}
			rowOf[t] = row
		}
	}
	for _, t := range p.perfTargets {
		if _, ok := rowOf[t]; !ok {
			r.add(pl.file, pl.perfLine, "%s, a target of the §12.1 exit criteria, has no measuring item in the performance table (rule 3)", t)
		}
	}
}
