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
	// skip, todo, fixme, or fail, or inside a group declared so.
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
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		ts, ps := scanFile(filepath.ToSlash(rel), string(src), lang, lang.isTest(d.Name()))
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

var (
	// nearTagRE finds a line comment that is meant as a Verifies comment but
	// is not spelled "Verifies:", such as "verifies: FR-…", "Verifies
	// NFR-…", "Verifies — FR-…", or "@verifies (FR-…)".
	nearTagRE = regexp.MustCompile(`(?i)^@?verif(?:y|ies|ied)\b[^A-Za-z0-9]*N?FR-`)

	// continuedTagRE finds a comment line that continues the ID list of the
	// Verifies comment above it.
	continuedTagRE = regexp.MustCompile(`^N?FR-[A-Z0-9]+-\d+`)
)

// scanFile finds the tagged tests in one source file. A Verifies comment is
// a line comment on its own line; together with the line comments on the
// lines directly above and below it, it belongs to the test declared on the
// next line.
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
	var pending []Tag // the tags of the current run of line comments
	last := 0         // the line of the last comment in the run
	flush := func() {
		if pending == nil {
			return
		}
		if d, ok := declAt[last+1]; ok {
			tests = append(tests, Test{File: file, Line: d.line, Name: d.name, Skipped: d.skipped, Tags: pending})
		} else {
			report(pending[0].Line, "a Verifies comment that is not directly above a test declaration")
		}
		pending = nil
	}
	for _, c := range s.comments {
		rest, tagged := strings.CutPrefix(c.text, "Verifies:")
		switch {
		case c.block:
			if tagged {
				report(c.line, "a Verifies comment in a block comment (write it as a %s line comment directly above the test)", lang.comment)
			}
		case !c.ownLine:
			if isTest && (tagged || nearTagRE.MatchString(c.text)) {
				report(c.line, "a Verifies comment after code (write it on its own line directly above the test)")
			}
		default:
			if c.line != last+1 {
				flush()
			}
			last = c.line
			switch {
			case tagged && !isTest:
				report(c.line, "a Verifies comment outside a test file (tests are in *_test.go, *.test.ts, *.spec.ts, *.bench.ts, or test_*.gd files)")
			case tagged:
				ids, errs := parseTags(rest)
				for _, err := range errs {
					report(c.line, "%s", err)
				}
				for _, id := range ids {
					pending = append(pending, Tag{ID: id, Line: c.line})
				}
			case isTest && (nearTagRE.MatchString(c.text) || pending != nil && continuedTagRE.MatchString(c.text)):
				report(c.line, "cannot read %q as a Verifies comment (write \"Verifies:\" and the IDs)", c.text)
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
