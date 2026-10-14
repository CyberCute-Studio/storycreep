package doccheck

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// idListTail matches what may follow the first ID of a compact list in prose:
// more numbers after "/" or ",", and ranges. Appended to a pattern for the
// first ID, it matches the whole list, which expandIDs then reads.
const idListTail = `(?:(?:/|\s*,\s*)\d+\b|\s*(?:…|\.\.|–)\s*\d+)*`

var (
	fullIDTokenRE = regexp.MustCompile(`^([A-Z]+-[A-Z0-9]+)-(\d+)(?:\s*(?:…|\.\.|–)\s*(\d+))?$`)
	bareIDTokenRE = regexp.MustCompile(`^(\d+)(?:\s*(?:…|\.\.|–)\s*(\d+))?$`)
)

// expandIDs expands a compact list of requirement IDs as the documents write
// them: "FR-TEN-01…05, 07" is FR-TEN-01 to FR-TEN-05 plus FR-TEN-07, and
// "NFR-PERF-01, 02, 18" and "FR-TEN-02/03/04" are three IDs each. A bare
// number continues the prefix of the entry before it, and a range is written
// with "…", "..", or "–".
func expandIDs(s string) ([]string, error) {
	var ids []string
	prefix := ""
	for _, tok := range strings.Split(strings.ReplaceAll(s, "/", ","), ",") {
		tok = strings.TrimSpace(tok)
		var from, to string
		if m := fullIDTokenRE.FindStringSubmatch(tok); m != nil {
			prefix, from, to = m[1], m[2], m[3]
		} else if m := bareIDTokenRE.FindStringSubmatch(tok); m != nil && prefix != "" {
			from, to = m[1], m[2]
		} else {
			return nil, fmt.Errorf("cannot read %q as a requirement ID", tok)
		}
		lo, err := strconv.Atoi(from)
		if err != nil {
			return nil, fmt.Errorf("cannot read %q as a requirement ID", tok)
		}
		hi := lo
		if to != "" {
			if hi, err = strconv.Atoi(to); err != nil || hi < lo {
				return nil, fmt.Errorf("cannot read %q as a range of requirement IDs", tok)
			}
		}
		for n := lo; n <= hi; n++ {
			ids = append(ids, fmt.Sprintf("%s-%0*d", prefix, len(from), n))
		}
	}
	return ids, nil
}

// itemNum is a plan item number such as 45 or 45a. A suffixed number sorts
// directly after its base number: 45 < 45a < 45b < 46 (plan rule 1).
type itemNum struct {
	n      int
	suffix string
}

var itemNumRE = regexp.MustCompile(`^(\d+)([a-z]?)$`)

func parseItemNum(s string) (itemNum, bool) {
	m := itemNumRE.FindStringSubmatch(s)
	if m == nil {
		return itemNum{}, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return itemNum{}, false
	}
	return itemNum{n: n, suffix: m[2]}, true
}

func (a itemNum) less(b itemNum) bool {
	if a.n != b.n {
		return a.n < b.n
	}
	return a.suffix < b.suffix
}

func (a itemNum) String() string {
	return strconv.Itoa(a.n) + a.suffix
}

// itemRef is a reference to one item, or to every item of a range such as
// "1–118".
type itemRef struct {
	lo, hi itemNum // equal unless rng
	rng    bool
	line   int
}

func (r itemRef) String() string {
	if r.rng {
		return r.lo.String() + "–" + r.hi.String()
	}
	return r.lo.String()
}

var itemTokenRE = regexp.MustCompile(`^(\d+[a-z]?)(?:\s*[–-]\s*(\d+[a-z]?))?$`)

// parseItems parses a comma-separated list of item numbers and ranges, as
// written in a Depends on field ("8, 12, 20" or "1–118").
func parseItems(s string, line int) ([]itemRef, error) {
	var refs []itemRef
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		m := itemTokenRE.FindStringSubmatch(tok)
		if m == nil {
			return nil, fmt.Errorf("cannot read %q as an item number or range", tok)
		}
		ref := itemRef{line: line}
		ref.lo, _ = parseItemNum(m[1])
		ref.hi = ref.lo
		if m[2] != "" {
			ref.hi, _ = parseItemNum(m[2])
			ref.rng = true
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

var (
	// itemWordRE finds the word that introduces item references in prose:
	// "item 27", "Items 33–36", "the item-32 validator".
	itemWordRE = regexp.MustCompile(`\b[Ii]tems?[ -]`)
	refHeadRE  = regexp.MustCompile(`^\s*(\d+[a-z]?)\b(?:\s*[–-]\s*(\d+[a-z]?)\b)?`)
	remarkRE   = regexp.MustCompile(`^\s*\([^)]*\)`)
	listSepRE  = regexp.MustCompile(`^(?:\s*,\s*(?:and\s+|or\s+)?|\s+and\s+|\s+or\s+)`)

	// bareRefRE finds a parenthesised list of item numbers, which item text
	// uses as a short reference: "token management (26)".
	bareRefRE = regexp.MustCompile(`\((\s*\d+[a-z]?(?:\s*[–-]\s*\d+[a-z]?)?(?:\s*,\s*(?:and\s+)?\d+[a-z]?(?:\s*[–-]\s*\d+[a-z]?)?)*\s*)\)`)
)

// scanRefs returns the item references in one line of prose. With bare, a
// parenthesised list of numbers also counts as a reference. Inline code is
// ignored.
func scanRefs(text string, line int, bare bool) []itemRef {
	text = stripCode(text)
	var refs []itemRef
	for _, loc := range itemWordRE.FindAllStringIndex(text, -1) {
		refs = append(refs, scanList(text[loc[1]:], line)...)
	}
	if bare {
		for _, m := range bareRefRE.FindAllStringSubmatch(text, -1) {
			if rs, err := parseItems(strings.ReplaceAll(m[1], "and ", ""), line); err == nil {
				refs = append(refs, rs...)
			}
		}
	}
	return refs
}

// scanList reads the list of item numbers at the start of s, stopping at the
// first word that is not part of it. Entries are numbers or ranges, separated
// by commas, "and", or "or", and each may carry a parenthesised remark, as in
// "80 (diff) and 83 (authoring export)".
func scanList(s string, line int) []itemRef {
	var refs []itemRef
	for {
		m := refHeadRE.FindStringSubmatchIndex(s)
		if m == nil {
			return refs
		}
		ref := itemRef{line: line}
		ref.lo, _ = parseItemNum(s[m[2]:m[3]])
		ref.hi = ref.lo
		if m[4] >= 0 {
			ref.hi, _ = parseItemNum(s[m[4]:m[5]])
			ref.rng = true
		}
		refs = append(refs, ref)
		s = s[m[1]:]
		if loc := remarkRE.FindStringIndex(s); loc != nil {
			s = s[loc[1]:]
		}
		loc := listSepRE.FindStringIndex(s)
		if loc == nil || !refHeadRE.MatchString(s[loc[1]:]) {
			return refs
		}
		s = s[loc[1]:]
	}
}
