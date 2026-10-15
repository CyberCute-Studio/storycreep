package doccheck

import (
	"regexp"
	"strings"
)

// document is a Markdown file split into lines. Lines inside fenced code
// blocks are blanked, so examples in them are never read as content.
type document struct {
	lines []string // lines[i] holds line i+1
}

func newDocument(src string) *document {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	fence := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case fence != "":
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			lines[i] = ""
		case strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"):
			fence = t[:3]
			lines[i] = ""
		}
	}
	return &document{lines: lines}
}

// column returns the width of s, the start of a line, as Markdown counts it:
// a tab advances to the next multiple of 4.
func column(s string) int {
	w := 0
	for _, c := range s {
		if c == '\t' {
			w += 4 - w%4
		} else {
			w++
		}
	}
	return w
}

// line returns line n, counting from 1.
func (d *document) line(n int) string {
	return d.lines[n-1]
}

// section is a heading and its body: the lines after it up to the next
// heading of the same or a higher level.
type section struct {
	level      int
	text       string
	line       int // the heading's line
	start, end int // the body's lines, end exclusive
}

var headingRE = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*$`)

// sections returns the section of every heading, in document order.
func (d *document) sections() []section {
	var ss []section
	for i, l := range d.lines {
		if m := headingRE.FindStringSubmatch(l); m != nil {
			ss = append(ss, section{level: len(m[1]), text: m[2], line: i + 1, start: i + 2})
		}
	}
	for i := range ss {
		ss[i].end = len(d.lines) + 1
		for _, next := range ss[i+1:] {
			if next.level <= ss[i].level {
				ss[i].end = next.line
				break
			}
		}
	}
	return ss
}

// findSection returns the first section with the given heading level whose
// heading text starts with prefix.
func (d *document) findSection(level int, prefix string) (section, bool) {
	for _, s := range d.sections() {
		if s.level == level && strings.HasPrefix(s.text, prefix) {
			return s, true
		}
	}
	return section{}, false
}

// tableRow is one data row of a Markdown table.
type tableRow struct {
	line  int
	cells []string
}

var tableSeparatorRE = regexp.MustCompile(`^\s*\|(\s*:?-+:?\s*\|)+\s*$`)

// table returns the data rows of the first table in the lines [start, end),
// without its header and separator rows. ok is false when there is no table.
func (d *document) table(start, end int) (rows []tableRow, ok bool) {
	n := start
	for n < end && !isTableLine(d.line(n)) {
		n++
	}
	if n+1 >= end || !tableSeparatorRE.MatchString(d.line(n+1)) {
		return nil, false
	}
	for n += 2; n < end && isTableLine(d.line(n)); n++ {
		rows = append(rows, tableRow{line: n, cells: splitCells(d.line(n))})
	}
	return rows, true
}

func isTableLine(l string) bool {
	return strings.HasPrefix(strings.TrimSpace(l), "|")
}

func splitCells(l string) []string {
	l = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(l), "|"), "|")
	cells := strings.Split(l, "|")
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	return cells
}

var codeSpanRE = regexp.MustCompile("`[^`]*`")

// stripCode removes inline code spans, whose contents are never references.
func stripCode(s string) string {
	return codeSpanRE.ReplaceAllString(s, " ")
}
