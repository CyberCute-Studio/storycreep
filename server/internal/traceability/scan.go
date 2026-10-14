package traceability

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/doccheck"
)

// Test is a test declaration with a Verifies comment.
type Test struct {
	File    string // slash-separated, relative to the scanned root
	Line    int    // the declaration's line
	Name    string
	Skipped bool // declared with skip, todo, or fixme, so it never runs
	Tags    []Tag
}

// Tag is one ID named by a Verifies comment: "FR-EDIT-02/AC3" or
// "NFR-PERF-01".
type Tag struct {
	ID   string
	Line int // the comment's line
}

// language describes the test files of one language: how a line comment
// starts, which files hold tests, and how a test is declared.
type language struct {
	comment string
	isTest  func(name string) bool
	// decl returns the name of the test declared on line, if any. enclosing
	// is the top-level function the line is in, for Go subtests.
	decl func(line, enclosing string) (name string, skipped, ok bool)
}

var (
	goFuncRE     = regexp.MustCompile(`^func\s+(\w+)\s*\(`)
	goTestNameRE = regexp.MustCompile(`^(?:Test|Benchmark|Fuzz)(?:[A-Z0-9_]\w*)?$`)
	goSubtestRE  = regexp.MustCompile(`^\s+\w+\.Run\(\s*("(?:[^"\\]|\\.)*"|` + "`[^`]*`" + `)`)

	// A Vitest or Playwright declaration: test("…"), it.skip("…"),
	// test.describe("…"), bench("…"). Its first argument must be a string
	// literal.
	jsTestRE = regexp.MustCompile(`^\s*(test|it|describe|bench)((?:\.[A-Za-z]+)*)\s*\(\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|` + "`((?:[^`\\\\]|\\\\.)*)`" + `)`)
	jsFileRE = regexp.MustCompile(`\.(?:test|spec)\.[cm]?[jt]sx?$`)
	jsSrcRE  = regexp.MustCompile(`\.[cm]?[jt]sx?$`)

	gdTestRE = regexp.MustCompile(`^\s*func\s+(test_\w*)\s*\(`)

	// blockTagRE finds a Verifies line in a block comment, which the scan
	// does not read.
	blockTagRE = regexp.MustCompile(`^(?:/\*+|\*)\s*Verifies:`)
)

var (
	goLang = &language{
		comment: "//",
		isTest:  func(name string) bool { return strings.HasSuffix(name, "_test.go") },
		decl: func(line, enclosing string) (string, bool, bool) {
			if m := goFuncRE.FindStringSubmatch(line); m != nil {
				return m[1], false, isGoTest(m[1])
			}
			m := goSubtestRE.FindStringSubmatch(line)
			if m == nil || !isGoTest(enclosing) {
				return "", false, false
			}
			sub, err := strconv.Unquote(m[1])
			if err != nil {
				return "", false, false
			}
			// go test prints a subtest's spaces as underscores.
			return enclosing + "/" + strings.ReplaceAll(sub, " ", "_"), false, true
		},
	}
	jsLang = &language{
		comment: "//",
		isTest:  jsFileRE.MatchString,
		decl: func(line, _ string) (string, bool, bool) {
			m := jsTestRE.FindStringSubmatch(line)
			if m == nil {
				return "", false, false
			}
			skipped := false
			for _, mod := range strings.Split(m[2], ".") {
				skipped = skipped || mod == "skip" || mod == "todo" || mod == "fixme"
			}
			return jsUnescaper.Replace(m[3] + m[4] + m[5]), skipped, true
		},
	}
	// jsUnescaper reads the escapes a test title commonly holds.
	jsUnescaper = strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\'`, `'`, "\\`", "`")
	gdLang      = &language{
		comment: "#",
		isTest: func(name string) bool {
			return strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".gd")
		},
		decl: func(line, _ string) (string, bool, bool) {
			if m := gdTestRE.FindStringSubmatch(line); m != nil {
				return m[1], false, true
			}
			return "", false, false
		},
	}
)

// isGoTest reports whether a function name declares a Go test, benchmark,
// or fuzz test.
func isGoTest(name string) bool {
	return goTestNameRE.MatchString(name) && name != "TestMain"
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
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		return a.Line - b.Line
	})
	return tests, problems, nil
}

// utf8BOM is the byte order mark that some editors write at the start of a
// file.
const utf8BOM = "\xef\xbb\xbf"

// scanFile finds the tagged tests in one source file. A Verifies comment
// belongs to the test declared on the line directly below its comment block.
func scanFile(file, src string, lang *language, isTest bool) ([]Test, []doccheck.Problem) {
	var tests []Test
	var problems []doccheck.Problem
	report := func(line int, format string, args ...any) {
		problems = append(problems, doccheck.Problem{File: file, Line: line, Msg: fmt.Sprintf(format, args...)})
	}
	src = strings.TrimPrefix(strings.ReplaceAll(src, "\r\n", "\n"), utf8BOM)
	var pending []Tag // the tags of the comment block above the current line
	enclosing := ""
	for i, line := range strings.Split(src, "\n") {
		n := i + 1
		trimmed := strings.TrimSpace(line)
		if text, ok := lineComment(trimmed, lang.comment); ok {
			rest, ok := strings.CutPrefix(text, "Verifies:")
			if !ok {
				continue
			}
			if !isTest {
				report(n, "a Verifies comment outside a test file (tests are in *_test.go, *.test.ts, *.spec.ts, or test_*.gd files)")
				continue
			}
			ids, errs := parseTags(rest)
			for _, err := range errs {
				report(n, "%s", err)
			}
			for _, id := range ids {
				pending = append(pending, Tag{ID: id, Line: n})
			}
			continue
		}
		if blockTagRE.MatchString(trimmed) {
			report(n, "a Verifies comment in a block comment (write it as a %s line comment directly above the test)", lang.comment)
		}
		if pending != nil {
			if name, skipped, ok := lang.decl(line, enclosing); ok {
				tests = append(tests, Test{File: file, Line: n, Name: name, Skipped: skipped, Tags: pending})
			} else {
				report(pending[0].Line, "a Verifies comment that is not directly above a test declaration")
			}
			pending = nil
		}
		// A top-level function, or a method, which encloses no test.
		if lang == goLang && strings.HasPrefix(line, "func") {
			enclosing = ""
			if m := goFuncRE.FindStringSubmatch(line); m != nil {
				enclosing = m[1]
			}
		}
	}
	if pending != nil {
		report(pending[0].Line, "a Verifies comment that is not directly above a test declaration")
	}
	return tests, problems
}

// lineComment returns the text of a line comment, without its marker.
func lineComment(trimmed, marker string) (string, bool) {
	if !strings.HasPrefix(trimmed, marker) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimLeft(trimmed, marker[:1])), true
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
