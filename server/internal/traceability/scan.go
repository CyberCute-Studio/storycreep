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
	File string // slash-separated, relative to the scanned root
	Line int    // the declaration's line
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

// language describes the test files of one language: how a line comment
// starts, how a block comment starts and ends, which files hold tests, and
// how a test is declared.
type language struct {
	comment               string
	blockOpen, blockClose string
	isTest                func(name string) bool
	// decl returns the name of the test declared on line, if any, and
	// whether it verifies nothing (see Test.Skipped). enclosing is the
	// top-level function the line is in, for Go subtests.
	decl func(line, enclosing string) (name string, skipped, ok bool)
}

var (
	goFuncRE     = regexp.MustCompile(`^func\s+(\w+)\s*\(`)
	goTestNameRE = regexp.MustCompile(`^(?:Test|Benchmark|Fuzz)(?:[A-Z0-9_]\w*)?$`)
	goSubtestRE  = regexp.MustCompile(`^\s+\w+\.Run\(\s*("(?:[^"\\]|\\.)*"|` + "`[^`]*`" + `)`)

	// A Vitest or Playwright declaration: test("…"), it.skip("…"),
	// test.describe("…"), bench("…"). Its first argument must be a string
	// literal, and its modifiers must be jsModifiers.
	jsTestRE = regexp.MustCompile(`^\s*(test|it|describe|bench)((?:\.[A-Za-z]+)*)\s*\(\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|` + "`((?:[^`\\\\]|\\\\.)*)`" + `)`)
	// Vitest runs tests from *.test.ts and *.spec.ts files, and benchmarks
	// only from *.bench.ts and *.benchmark.ts files.
	jsFileRE = regexp.MustCompile(`\.(?:test|spec|bench|benchmark)\.[cm]?[jt]sx?$`)
	jsSrcRE  = regexp.MustCompile(`\.[cm]?[jt]sx?$`)

	gdTestRE = regexp.MustCompile(`^\s*func\s+(test_\w*)\s*\(`)

	// starTagRE finds a Verifies line of a block comment that the scan did
	// not see open, because it opened after code on its first line.
	starTagRE = regexp.MustCompile(`^\*+\s*Verifies:`)

	// nearTagRE finds a line comment that is meant as a Verifies comment but
	// is not spelled "Verifies:", such as "verifies: FR-…" or "Verifies
	// NFR-…".
	nearTagRE = regexp.MustCompile(`(?i)^verif(?:y|ies|ied)\b\s*:?\s*N?FR-`)
)

// jsModifiers are the modifiers a Vitest or Playwright declaration may carry,
// as in it.only or test.describe.serial, and whether the test then verifies
// nothing: it never runs, or it is expected to fail. Any other member, such
// as test.beforeEach or test.step, is not a test.
var jsModifiers = map[string]bool{
	"describe":   false,
	"only":       false,
	"concurrent": false,
	"sequential": false,
	"shuffle":    false,
	"serial":     false,
	"parallel":   false,
	"skip":       true,
	"todo":       true,
	"fixme":      true,
	"fail":       true, // Playwright: the test passes only while it fails
	"fails":      true, // Vitest: the same
}

var (
	goLang = &language{
		comment:    "//",
		blockOpen:  "/*",
		blockClose: "*/",
		isTest:     func(name string) bool { return strings.HasSuffix(name, "_test.go") },
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
		comment:    "//",
		blockOpen:  "/*",
		blockClose: "*/",
		isTest:     jsFileRE.MatchString,
		decl: func(line, _ string) (string, bool, bool) {
			m := jsTestRE.FindStringSubmatch(line)
			if m == nil {
				return "", false, false
			}
			skipped := false
			for _, mod := range strings.Split(m[2], ".")[1:] {
				skip, ok := jsModifiers[mod]
				if !ok {
					return "", false, false
				}
				skipped = skipped || skip
			}
			return jsUnescaper.Replace(m[3] + m[4] + m[5]), skipped, true
		},
	}
	// jsUnescaper reads the escapes a test title commonly holds.
	jsUnescaper = strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\'`, `'`, "\\`", "`")
	gdLang      = &language{
		comment: "#",
		// GDScript has no block comments, but a bare multi-line string serves
		// as one, so a Verifies line in a multi-line string is reported.
		blockOpen:  `"""`,
		blockClose: `"""`,
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
//
// A test inside a group declared with skip, todo, fixme, or fail verifies
// nothing either. The group's body is the lines below it that are indented
// deeper than its declaration, as Biome formats them.
func scanFile(file, src string, lang *language, isTest bool) ([]Test, []doccheck.Problem) {
	var tests []Test
	var problems []doccheck.Problem
	report := func(line int, format string, args ...any) {
		problems = append(problems, doccheck.Problem{File: file, Line: line, Msg: fmt.Sprintf(format, args...)})
	}
	blockTag := func(line int) {
		report(line, "a Verifies comment in a block comment (write it as a %s line comment directly above the test)", lang.comment)
	}
	var pending []Tag // the tags of the comment block above the current line
	// unattached reports pending tags that no test declaration follows.
	unattached := func() {
		if pending != nil {
			report(pending[0].Line, "a Verifies comment that is not directly above a test declaration")
			pending = nil
		}
	}
	src = strings.TrimPrefix(strings.ReplaceAll(src, "\r\n", "\n"), utf8BOM)
	enclosing := ""
	inBlock := false // in a block comment that opened on an earlier line
	skipIndent := -1 // the indentation of the skipped group the line is in
	for i, line := range strings.Split(src, "\n") {
		n := i + 1
		trimmed := strings.TrimSpace(line)
		if skipIndent >= 0 && trimmed != "" && !inBlock && indentation(line) <= skipIndent {
			skipIndent = -1
		}
		isBlock := false
		if inBlock || !strings.HasPrefix(trimmed, lang.comment) {
			isBlock, inBlock = lang.blockLine(trimmed, inBlock)
		}
		if isBlock {
			text := strings.TrimPrefix(trimmed, lang.blockOpen)
			if strings.HasPrefix(strings.TrimSpace(strings.TrimLeft(text, "*")), "Verifies:") {
				blockTag(n)
			}
			unattached()
			continue
		}
		if text, ok := lineComment(trimmed, lang.comment); ok {
			rest, ok := strings.CutPrefix(text, "Verifies:")
			if !ok {
				if isTest && nearTagRE.MatchString(text) {
					report(n, "cannot read %q as a Verifies comment (write \"Verifies:\" and the IDs)", text)
				}
				continue
			}
			if !isTest {
				report(n, "a Verifies comment outside a test file (tests are in *_test.go, *.test.ts, *.spec.ts, *.bench.ts, or test_*.gd files)")
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
		if starTagRE.MatchString(trimmed) {
			blockTag(n)
		}
		if name, skipped, ok := lang.decl(line, enclosing); ok {
			skipped = skipped || skipIndent >= 0
			if pending != nil {
				tests = append(tests, Test{File: file, Line: n, Name: name, Skipped: skipped, Tags: pending})
				pending = nil
			}
			if skipped && skipIndent < 0 {
				skipIndent = indentation(line)
			}
		}
		unattached()
		// A top-level function, or a method, which encloses no test.
		if lang == goLang && strings.HasPrefix(line, "func") {
			enclosing = ""
			if m := goFuncRE.FindStringSubmatch(line); m != nil {
				enclosing = m[1]
			}
		}
	}
	unattached()
	return tests, problems
}

// blockLine reports whether a line, given whether a block comment is open
// before it, is part of a block comment, and whether one is open after it.
// A Go or TypeScript block comment counts only when a line starts with "/*",
// so that "/*" in a string such as a glob never hides the lines below it.
// GDScript's multi-line strings open and close with the same `"""`, and are
// followed through the code so that the line that closes one is not read as
// opening another.
func (l *language) blockLine(trimmed string, open bool) (isBlock, openAfter bool) {
	if l.blockOpen == l.blockClose {
		toggles := strings.Count(trimmed, l.blockOpen)%2 == 1
		return open || strings.HasPrefix(trimmed, l.blockOpen), open != toggles
	}
	if !open {
		rest, ok := strings.CutPrefix(trimmed, l.blockOpen)
		if !ok {
			return false, false
		}
		trimmed = rest
	}
	return true, !strings.Contains(trimmed, l.blockClose)
}

// indentation returns the number of spaces and tabs a line starts with.
func indentation(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
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
