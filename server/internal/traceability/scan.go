package traceability

import (
	"cmp"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/doccheck"
)

// Test is a test declaration with a Verifies comment.
type Test struct {
	File string // slash-separated, relative to the scanned root
	Line int    // the declaration's line
	// Name is the test's name as its runner reports it: "TestX/case" for a
	// Go subtest, "group > title" for a Vitest or Playwright test in a group.
	Name string
	// Skipped is set for a test that verifies nothing: it is declared with
	// skip, todo, fixme, or fail (or with Vitest's option skip, todo, or fails
	// set to true), or inside a group declared so, or it calls Playwright's
	// test.skip(), test.fixme(), or test.fail() without arguments or is inside
	// a group or file that does.
	Skipped bool
	Tags    []Tag
}

// Tag is one ID named by a Verifies comment: "FR-EDIT-02/AC3" or
// "NFR-PERF-01".
type Tag struct {
	ID   string
	Line int // the comment's line
}

// language reads the source files of one language.
type language struct {
	comment string // the line comment marker
	isTest  func(name string) bool
	// read returns the comments of a source file and, for a test file, its
	// test declarations. Problems are parts of the file it cannot read.
	read func(file, src string, isTest bool) source
	// declares reports whether text, the commented-out code of a test file,
	// declares a test or a group.
	declares func(file, text string) bool
}

// source is what the scan needs from a source file, whatever its language.
type source struct {
	comments []comment // in line order
	decls    []decl    // in line order
	problems []doccheck.Problem
}

// comment is one line of a comment.
type comment struct {
	line int
	// text is the line's text without the comment markers: "//" or "#" for a
	// line comment, and "/*", "*/", and a leading "*" for a block comment.
	text  string
	block bool // a line of a block comment, or of a GDScript multi-line string that serves as one
	// ownLine is set for a line comment with nothing but white space before
	// it on its line.
	ownLine bool
}

// decl is a test declaration.
type decl struct {
	line    int // the line the declaration starts on
	name    string
	skipped bool
	// unnamed is set for a declaration the scan cannot name, as a test whose
	// title is not a literal or a table of tests.
	unnamed bool
	// group is set for a group of tests, as a describe block. A group runs
	// nothing itself, so a Verifies comment belongs on the tests in it.
	group bool
}

// languageOf returns the language of a source file, or nil for a file the
// scan does not read.
func languageOf(name string) *language {
	switch {
	case strings.HasSuffix(name, ".go"):
		return goLang
	case jsSrcRE.MatchString(name):
		return jsLang
	case strings.HasSuffix(name, ".gd"):
		return gdLang
	}
	return nil
}

// skipDir reports whether the scan leaves out a directory: dependencies,
// build output, fixtures, and hidden directories such as .git and .godot.
func skipDir(name string) bool {
	switch name {
	case "node_modules", "testdata", "vendor", "dist", "coverage":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// goIgnored reports whether the go tool ignores a Go file, given by its
// slash-separated path below the scanned root: its name, or the name of a
// directory it is in, starts with "_" or ".". The go tool never builds such a
// file, so its tests never run.
func goIgnored(rel string) bool {
	for _, name := range strings.Split(rel, "/") {
		if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
			return true
		}
	}
	return false
}

// Scan finds the tagged tests under root, in path and line order. Problems
// are Verifies comments it cannot read or attach to a test. An error means a
// directory or file could not be read.
func Scan(root string) ([]Test, []doccheck.Problem, error) {
	var tests []Test
	var problems []doccheck.Problem
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		lang := languageOf(d.Name())
		if lang == nil || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if lang == goLang && goIgnored(rel) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		ts, ps := scanFile(rel, string(src), lang, lang.isTest(d.Name()))
		tests = append(tests, ts...)
		problems = append(problems, ps...)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	slices.SortStableFunc(tests, func(a, b Test) int {
		return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line))
	})
	return tests, problems, nil
}

// utf8BOM is the byte order mark that some editors write at the start of a
// file.
const utf8BOM = "\xef\xbb\xbf"

// idPattern matches a requirement ID as a comment might name it: "FR-…",
// "FR-…/ACn", or "NFR-…".
const idPattern = `N?FR-[A-Z0-9]+-\d+(?:/AC\d+)?`

const (
	// listAnd matches a word that joins IDs in a list: "and" or "&".
	listAnd = `(?:(?i:and)|&)`
	// listLead matches what may come before the first ID on a line that
	// continues a list: a list marker, as "- " or "1. ", and then "and", as
	// in "and NFR-PERF-01".
	listLead = `^(?:(?:[-*+]|\d+[.)])\s+)?(?:` + listAnd + `\s+)?`
)

var (
	// nearTagRE finds a line comment that starts with a word and then names a
	// requirement, as "verifies: FR-…", "Verifies NFR-…", "Verifies — FR-…",
	// "@verifies (FR-…)", or "Verifes: FR-…" do. nearTag decides whether the
	// word is meant as "Verifies".
	nearTagRE = regexp.MustCompile(`(?i)^@?([a-z]+)[^a-z0-9]*n?fr-`)

	// continuedTagRE finds a comment line that continues the ID list of the
	// Verifies comment on the line above it: "FR-A-01/AC2 …", "- FR-…", or
	// "and FR-…".
	continuedTagRE = regexp.MustCompile(listLead + `N?FR-[A-Z0-9]+-\d+`)

	// idListRE finds a comment line that holds nothing but requirement IDs
	// and the commas or "and" between them, optionally as a list item:
	// "FR-A-01/AC2, NFR-PERF-01", "- FR-A-01/AC2", "and NFR-PERF-01.", or
	// "FR-A-01/AC2 and FR-A-01/AC3".
	idListRE = regexp.MustCompile(listLead + idPattern +
		`(?:(?:\s*,\s*(?:` + listAnd + `\s+)?|\s+` + listAnd + `\s+)` + idPattern + `)*\s*[,.]?$`)
)

// nearTag reports whether a line comment is meant as a Verifies comment but
// is not spelled "Verifies:": its first word is a form of "verify", such as
// "verifies" or "Verified", or a typo of "Verifies", such as "Verifes", and
// a requirement ID follows it.
func nearTag(text string) bool {
	m := nearTagRE.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	w := strings.ToLower(m[1])
	return strings.HasPrefix(w, "verif") || editDistance(w, "verifies") <= 2
}

// editDistance returns the Levenshtein distance between two ASCII words: the
// number of characters to insert, delete, or replace to turn one into the
// other.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			replace := prev[j-1]
			if a[i-1] != b[j-1] {
				replace++
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, replace)
		}
		prev = cur
	}
	return prev[len(b)]
}

// scanFile finds the tagged tests in one source file. A Verifies comment is
// a line comment on its own line; together with the line comments on the
// lines directly above and below it, it belongs to the test declared on the
// next line, unless the comment lines below it hold a test declaration that
// is commented out, whose tag it is.
func scanFile(file, src string, lang *language, isTest bool) ([]Test, []doccheck.Problem) {
	src = strings.TrimPrefix(strings.ReplaceAll(src, "\r\n", "\n"), utf8BOM)
	s := lang.read(file, src, isTest)
	problems := s.problems
	report := func(line int, format string, args ...any) {
		problems = append(problems, doccheck.Problem{File: file, Line: line, Msg: fmt.Sprintf(format, args...)})
	}
	declAt := map[int]decl{}
	for _, d := range s.decls {
		if _, ok := declAt[d.line]; !ok {
			declAt[d.line] = d
		}
	}

	var tests []Test
	var pending []Tag     // the tags of the current run of line comments
	var idLines []comment // the lines of the run that hold nothing but IDs
	var below []string    // the text of the lines of the run below its first Verifies line
	runTagged := false    // the run has a Verifies line
	last := 0             // the line of the last comment in the run
	tagEnd := 0           // the line of the last Verifies line, or of a line that continues its list
	flush := func() {
		d, above := declAt[last+1]
		// A test that is commented out below its Verifies comment, as when
		// the test is commented out but its tag is not, leaves the tag above
		// whatever test comes next, which it does not verify.
		commentedOut := pending != nil && above && len(below) > 0 && lang.declares(file, strings.Join(below, "\n"))
		// A line that holds nothing but IDs, in a run with a Verifies line or
		// above a test, is meant as part of a Verifies comment: it continues
		// the ID list as a list item, after "and", or below a blank comment
		// line, where gofmt moves an indented continuation of a doc comment,
		// or it is meant as one.
		if runTagged || above {
			for _, c := range idLines {
				report(c.line, "cannot read %q as a Verifies comment (write \"Verifies:\" and the IDs)", c.text)
			}
		}
		switch {
		case pending == nil:
		case commentedOut:
			report(pending[0].Line, "a Verifies comment above a commented-out test (keep it directly above the test it verifies, or comment it out with the test)")
		case above && d.group:
			report(pending[0].Line, "a Verifies comment above a group (put it on the tests in the group, which are what runs)")
		case above && d.unnamed:
			report(pending[0].Line, "a Verifies comment above a declaration that the report cannot name (give it a literal title, without ${…}, each, or for)")
		case above:
			tests = append(tests, Test{File: file, Line: d.line, Name: d.name, Skipped: d.skipped, Tags: pending})
		default:
			report(pending[0].Line, "a Verifies comment that is not directly above a test declaration")
		}
		pending, idLines, below, runTagged = nil, nil, nil, false
	}
	for _, c := range s.comments {
		rest, tagged := strings.CutPrefix(c.text, "Verifies:")
		switch {
		case c.block:
			if tagged {
				report(c.line, "a Verifies comment in a block comment (write it as a %s line comment directly above the test)", lang.comment)
			}
		case tagged && !isTest:
			report(c.line, "a Verifies comment outside a test file (tests are in *_test.go, *.test.ts, *.spec.ts, *.bench.ts, or test_*.gd files)")
		case !c.ownLine:
			if isTest && (tagged || nearTag(c.text)) {
				report(c.line, "a Verifies comment after code (write it on its own line directly above the test)")
			}
		default:
			if c.line != last+1 {
				flush()
			}
			last = c.line
			if isTest && runTagged && !tagged {
				below = append(below, c.text)
			}
			switch {
			case !isTest:
				// Prose in a file that holds no tests is not read for tags.
			case tagged:
				ids, errs := parseTags(rest)
				for _, err := range errs {
					report(c.line, "%s", err)
				}
				for _, id := range ids {
					pending = append(pending, Tag{ID: id, Line: c.line})
				}
				runTagged = true
				tagEnd = c.line
			case nearTag(c.text):
				report(c.line, "cannot read %q as a Verifies comment (write \"Verifies:\" and the IDs)", c.text)
			case tagEnd > 0 && c.line == tagEnd+1 && continuedTagRE.MatchString(c.text):
				report(c.line, "cannot read %q as a Verifies comment (write \"Verifies:\" and the IDs)", c.text)
				tagEnd = c.line
			case idListRE.MatchString(c.text):
				idLines = append(idLines, c)
			}
		}
	}
	flush()
	slices.SortStableFunc(problems, func(a, b doccheck.Problem) int { return cmp.Compare(a.Line, b.Line) })
	return tests, problems
}

// blockComment splits the text of a block comment that starts on line into
// its lines, without the comment markers and a leading "*" on each line.
func blockComment(text string, line int, open, close string) []comment {
	text = strings.TrimSuffix(strings.TrimPrefix(text, open), close)
	var cs []comment
	for i, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if open == "/*" {
			l = strings.TrimSpace(strings.TrimLeft(l, "*"))
		}
		cs = append(cs, comment{line: line + i, text: l, block: true})
	}
	return cs
}

// lineCommentText returns the text of a line comment, without its marker.
func lineCommentText(c, marker string) string {
	return strings.TrimSpace(strings.TrimLeft(c, marker[:1]))
}

var (
	criterionTagRE = regexp.MustCompile(`^FR-[A-Z0-9]+-\d+/AC[1-9]\d*$`)
	nfrTagRE       = regexp.MustCompile(`^NFR-[A-Z0-9]+-\d+$`)
	frTagRE        = regexp.MustCompile(`^FR-[A-Z0-9]+-\d+$`)
)

// parseTags reads the IDs of a Verifies comment: a comma-separated list of
// acceptance criteria and NFRs, optionally ending with a period. It returns
// the IDs it can read, and a message for each entry it cannot.
func parseTags(s string) (ids, errs []string) {
	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	if s == "" {
		return nil, []string{"a Verifies comment that names no requirement"}
	}
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		switch {
		case tok == "":
			errs = append(errs, "an empty entry in a Verifies comment (remove the extra comma)")
		case criterionTagRE.MatchString(tok), nfrTagRE.MatchString(tok):
			ids = append(ids, tok)
		case frTagRE.MatchString(tok):
			errs = append(errs, fmt.Sprintf("Verifies names %s without an acceptance criterion (write %s/AC1)", tok, tok))
		default:
			errs = append(errs, fmt.Sprintf("cannot read %q in a Verifies comment as a requirement (write FR-…/ACn or NFR-…)", tok))
		}
	}
	return ids, errs
}
