package traceability

import (
	"regexp"
	"slices"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/doccheck"
	"github.com/CyberCute-Studio/storycreep/server/internal/jslex"
)

var (
	// Vitest runs tests from *.test.ts and *.spec.ts files, and benchmarks
	// only from *.bench.ts and *.benchmark.ts files. Playwright runs
	// *.spec.ts and *.test.ts files.
	jsFileRE      = regexp.MustCompile(`\.(?:test|spec|bench|benchmark)\.[cm]?[jt]sx?$`)
	jsBenchFileRE = regexp.MustCompile(`\.(?:bench|benchmark)\.[cm]?[jt]sx?$`)
	jsSrcRE       = regexp.MustCompile(`\.[cm]?[jt]sx?$`)
)

var jsLang = &language{
	comment: "//",
	isTest:  jsFileRE.MatchString,
	read:    readJS,
	declares: func(file, text string) bool {
		return slices.ContainsFunc(readJS(file, text, true).decls, func(d decl) bool { return d.first })
	},
}

// jsTestHeads and jsBenchHeads are the functions that declare a test or a
// group in a test file and in a benchmark file: test("…"), it("…"),
// describe("…"), and suite("…") (Vitest's describe), and bench("…"), with
// modifiers as in test.describe.serial("…"). Vitest's test run reads only
// test files, and bench("…") runs only in its benchmark run.
var (
	jsTestHeads  = map[string]bool{"test": true, "it": true, "describe": true, "suite": true}
	jsBenchHeads = map[string]bool{"bench": true, "describe": true, "suite": true}
)

// jsModifiers are the modifiers a declaration may carry, and whether the test
// or group then verifies nothing: it never runs, or it passes only while it
// fails. A declaration with any other member, such as test.beforeEach,
// test.step, or test.each, is not a test the scan can name.
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

// jsConditions are Vitest's modifiers that take a condition before the
// declaration's own call, as in test.runIf(isLinux)("…", …). The test still
// counts, because the scan cannot tell whether the condition holds.
var jsConditions = map[string]bool{"skipIf": true, "runIf": true}

// jsTables are the modifiers of a declaration that runs once for each row of
// a table, as in test.each([…])("…", …). It declares tests, but the scan
// cannot name them.
var jsTables = map[string]bool{"each": true, "for": true}

// jsSkipOptions are Vitest's options that, set to true, make a declaration
// verify nothing, as the modifiers of the same names do: test("…", { skip:
// true }, …).
var jsSkipOptions = map[string]bool{"skip": true, "todo": true, "fails": true}

// jsAnnotations are Playwright's modifiers that also annotate the test or
// group they are called in, rather than declare one: test.skip(),
// test.skip(isMobile, "…"), or test.fixme(({ browserName }) => …). Without
// arguments, or with the condition true, as in test.skip(true, "…"), the
// call skips that test or group, or makes it an expected failure, so it
// verifies nothing.
var jsAnnotations = map[string]bool{"skip": true, "fixme": true, "fail": true}

// readJS reads a JavaScript or TypeScript file. Its test declarations are the
// calls of jsTestHeads, or of jsBenchHeads in a benchmark file, with a string
// literal title, named after the groups they are in as Vitest names them:
// "group > title". A group without a literal title is "*", and a group
// without a title, as Playwright's test.describe(() => …), adds nothing to
// the name. A declaration without a literal title is returned unnamed. A test
// is skipped when it, or a group it is in, is declared with a modifier that
// verifies nothing, whatever the group's arguments, as in
// describe.skip.each([…])("…", …) or describe.skip.each`…`("…", …), with an
// option that does, as in test("…", { skip: true }, …), or when it, a group
// it is in, or the file calls Playwright's test.skip(), test.fixme(), or
// test.fail() without arguments or with the condition true. A group is
// returned as one, so that a Verifies comment above it is reported: the tests
// in it are what runs. A declaration is marked first when it is the first
// code on its line.
func readJS(file, src string, isTest bool) source {
	// A file that is not a test file matters only for the Verifies comments
	// in it.
	if !isTest && !strings.Contains(src, "Verifies:") {
		return source{}
	}
	toks, comments, unclosed := lexJS(src)
	s := source{comments: comments}
	if !isTest {
		return s
	}
	heads := jsTestHeads
	if jsBenchFileRE.MatchString(file) {
		heads = jsBenchHeads
	}

	// match maps each "(" to its ")"; an unclosed one runs to the end.
	match := map[int]int{}
	var open []int
	unpaired := 0 // the line of the first parenthesis without a partner
	for i, t := range toks {
		switch {
		case t.is('(', "("):
			open = append(open, i)
		case t.is('(', ")") && len(open) > 0:
			match[open[len(open)-1]] = i
			open = open[:len(open)-1]
		case t.is('(', ")") && unpaired == 0:
			unpaired = t.line
		}
	}
	for _, i := range open {
		match[i] = len(toks) - 1
	}
	if len(open) > 0 && (unpaired == 0 || toks[open[0]].line < unpaired) {
		unpaired = toks[open[0]].line
	}
	// A literal or comment that never closes hides the code after it, and
	// without paired parentheses the groups the tests are in are unknown, and
	// so are their names and whether a skipped group holds them. That matters
	// only in a file with Verifies comments. The source is searched, not the
	// comments, because the comments after such a literal are hidden too.
	if strings.Contains(src, "Verifies:") {
		switch {
		case unclosed > 0:
			s.problems = append(s.problems, doccheck.Problem{File: file, Line: unclosed,
				Msg: "cannot find the end of the template literal or block comment that starts here, so the scan cannot read the code after it (a backtick or \"/*\" in JSX text can cause this)"})
		case unpaired > 0:
			s.problems = append(s.problems, doccheck.Problem{File: file, Line: unpaired,
				Msg: "cannot pair this parenthesis, so the scan cannot tell which groups the tests below it are in (a parenthesis without its partner, a quote, or \"//\" in JSX text can cause this)"})
		}
	}

	// A call of a declaration: the tokens of its arguments, from its first
	// "(" to the ")" of its last call (as in describe.each(…)(…)).
	type call struct {
		head     int // the head's token
		from, to int
		name     string
		named    bool // a declaration the scan can name
		skipped  bool
		// anonymous is set for a group without a title, as Playwright's
		// test.describe(() => …), which adds nothing to its tests' names.
		anonymous bool
		group     bool // a describe or suite group, or a call on one, as test.describe.configure(…)
		// decl is set for the declaration of a test, tests, or a group,
		// named or not, rather than of a hook, a step, a setting, or an
		// annotation.
		decl bool
		// unconditional is set for Playwright's test.skip(), test.fixme(), or
		// test.fail() without arguments or with the condition true, which
		// skips the test or group it is called in.
		unconditional bool
	}
	var calls []call
	for i, t := range toks {
		if t.kind != 'i' || !heads[t.text] {
			continue
		}
		if i > 0 && (toks[i-1].is('(', ".") || toks[i-1].is('i', "function")) {
			continue
		}
		j := i + 1
		known, declares, skipped, conditional := true, true, false, false
		group := t.text == "describe" || t.text == "suite"
		var mods []string // the modifiers, without conditions
		for j+1 < len(toks) && toks[j].is('(', ".") && toks[j+1].kind == 'i' {
			m := toks[j+1].text
			j += 2
			if jsConditions[m] {
				// The condition's call comes before the declaration's.
				if j < len(toks) && toks[j].is('(', "(") {
					j = match[j] + 1
				}
				conditional = true
				continue
			}
			mods = append(mods, m)
			skip, ok := jsModifiers[m]
			known = known && ok
			declares = declares && (ok || jsTables[m])
			skipped = skipped || skip
			group = group || m == "describe"
		}
		// A template literal after the modifiers is the table of a tagged
		// template, as in describe.each`a | b`("…", …), and the call follows it.
		table := j < len(toks) && (toks[j].kind == 's' || toks[j].kind == 't')
		if table {
			j = afterTemplate(toks, j)
		}
		if j >= len(toks) || !toks[j].is('(', "(") {
			continue
		}
		c := call{head: i, from: j, to: match[j], skipped: skipped, name: "*", group: group, decl: declares}
		for c.to+1 < len(toks) && toks[c.to+1].is('(', "(") {
			c.to = match[c.to+1]
		}
		if t.text == "test" && !group && !table && !conditional && len(mods) == 1 && jsAnnotations[mods[0]] && c.to == match[c.from] {
			if args := callArgs(toks, c.from, c.to); annotates(toks, args, match) {
				// A condition that is the literal true, as in test.skip(true,
				// "…"), holds wherever the test runs.
				c.decl = false
				c.unconditional = len(args) == 0 ||
					(toks[args[0]].is('i', "true") && (args[0]+1 == c.to || toks[args[0]+1].is('(', ",")))
			}
		}
		c.skipped = c.skipped || skippedByOption(toks, c.from, c.to)
		// The title is a literal only when nothing is added to it, as in
		// "handles " + name.
		if !table && j+2 < len(toks) && toks[j+1].kind == 's' && (toks[j+2].is('(', ",") || toks[j+2].is('(', ")")) {
			c.name = toks[j+1].text
			c.named = known
		}
		c.anonymous = !table && isFunctionAt(toks, j+1, match)
		calls = append(calls, c)
	}

	// inside reports whether c is declared inside the call of g.
	inside := func(g, c call) bool { return g.from < c.head && c.head <= g.to }
	// parent[k] is the innermost declaration that calls[k] is inside, or -1.
	// The calls are in source order, so it is the last one before calls[k]
	// that holds it.
	parent := make([]int, len(calls))
	for k, c := range calls {
		parent[k] = -1
		for g := range k {
			if calls[g].decl && inside(calls[g], c) {
				parent[k] = g
			}
		}
	}
	// test.skip() without arguments, or with the condition true, skips the
	// test or group it is called in, also from a hook or a step, or every
	// test in the file when it is called outside them.
	fileSkipped := false
	for k, c := range calls {
		switch {
		case !c.unconditional:
		case parent[k] >= 0:
			calls[parent[k]].skipped = true
		default:
			fileSkipped = true
		}
	}
	// skippedAt[k] is set when calls[k], or a group it is in, is declared to
	// verify nothing.
	skippedAt := make([]bool, len(calls))
	for k, c := range calls {
		skippedAt[k] = c.skipped || fileSkipped || parent[k] >= 0 && skippedAt[parent[k]]
	}
	for k, c := range calls {
		if !c.decl {
			continue
		}
		d := decl{
			line:    toks[c.head].line,
			group:   c.group,
			unnamed: !c.named && !c.group,
			skipped: skippedAt[k],
			first:   c.head == 0 || toks[c.head-1].line < toks[c.head].line,
		}
		if c.named {
			var path []string
			for _, g := range calls {
				if inside(g, c) && !g.anonymous {
					path = append(path, g.name)
				}
			}
			d.name = strings.Join(append(path, c.name), " > ")
		}
		s.decls = append(s.decls, d)
	}
	return s
}

// callArgs returns the index of the first token of each argument of the call
// whose parentheses are toks[open] and toks[close].
func callArgs(toks []jsToken, open, close int) []int {
	var args []int
	depth := 0 // brackets of any kind, and template literals
	for k := open + 1; k < close; k++ {
		if depth == 0 && (k == open+1 || toks[k-1].is('(', ",")) {
			args = append(args, k)
		}
		switch t := toks[k]; {
		case t.kind == 't', t.kind == '(' && (t.text == "(" || t.text == "[" || t.text == "{"):
			depth++
		case t.kind == 'e', t.kind == '(' && (t.text == ")" || t.text == "]" || t.text == "}"):
			depth--
		}
	}
	return args
}

// annotates reports whether the arguments of a call of test.skip,
// test.fixme, or test.fail are those of Playwright's annotation rather than
// of a declaration: none; a callback, as in test.fixme(({ browserName }) =>
// …); or a condition and a description, as in test.skip(isMobile, "…"). A
// declaration's first argument is its title, so it is a string or template
// literal, or a function follows it, as in test.skip(title, async () => …).
func annotates(toks []jsToken, args []int, match map[int]int) bool {
	if len(args) == 0 || isFunctionAt(toks, args[0], match) {
		return true
	}
	if k := toks[args[0]].kind; k == 's' || k == 't' {
		return false
	}
	for _, a := range args[1:] {
		if isFunctionAt(toks, a, match) {
			return false
		}
	}
	return true
}

// jsToken is a token of JavaScript or TypeScript source.
type jsToken struct {
	// kind is 'i' for an identifier or keyword, 's' for a string literal or
	// a template literal without substitutions, 't' for the start of a
	// template literal with them, 'm' for its text between two substitutions,
	// and 'e' for its end (the tokens of the substitutions are between), 'n'
	// for a number, 'r' for a regular expression literal, and '(' for
	// punctuation.
	kind byte
	text string // the identifier, the punctuation, or the string's value
	line int
}

func (t jsToken) is(kind byte, text string) bool {
	return t.kind == kind && t.text == text
}

// isFunctionAt reports whether a function expression starts at toks[k]:
// "function", "async", or an arrow function's parameters, "(…) =>". match
// maps each "(" to its ")".
func isFunctionAt(toks []jsToken, k int, match map[int]int) bool {
	if k >= len(toks) {
		return false
	}
	if toks[k].is('i', "function") || toks[k].is('i', "async") {
		return true
	}
	if !toks[k].is('(', "(") {
		return false
	}
	end := match[k]
	return end+2 < len(toks) && toks[end+1].is('(', "=") && toks[end+2].is('(', ">")
}

// skippedByOption reports whether the arguments of a declaration, the tokens
// from its first "(" to the ")" of its last call, include an options object
// that sets one of jsSkipOptions to true, as Vitest's test("…", { skip: true
// }, …) or describe("…", { todo: true }, …). An option set to anything else,
// as { skip: isCI }, is a condition the scan cannot evaluate, like skipIf. An
// object that is not itself an argument, as one in the test's body, is not
// an options object.
func skippedByOption(toks []jsToken, from, to int) bool {
	depth := 0 // the nesting of brackets of any kind, the call's own included
	options := false
	for k := from; k <= to; k++ {
		t := toks[k]
		if t.kind != '(' {
			continue
		}
		switch t.text {
		case "(", "[", "{":
			if t.text == "{" && depth == 1 && (toks[k-1].is('(', "(") || toks[k-1].is('(', ",")) {
				options = true
			}
			depth++
		case ")", "]", "}":
			depth--
			if depth <= 1 {
				options = false
			}
		case ":":
			key := toks[k-1]
			if options && depth == 2 && (key.kind == 'i' || key.kind == 's') && jsSkipOptions[key.text] &&
				(toks[k-2].is('(', "{") || toks[k-2].is('(', ",")) &&
				k+2 <= to && toks[k+1].is('i', "true") && (toks[k+2].is('(', ",") || toks[k+2].is('(', "}")) {
				return true
			}
		}
	}
	return false
}

// afterTemplate returns the index of the token after the template literal
// that starts at toks[i].
func afterTemplate(toks []jsToken, i int) int {
	depth := 0
	for ; i < len(toks); i++ {
		switch toks[i].kind {
		case 't':
			depth++
		case 'e':
			depth--
		}
		if depth == 0 {
			return i + 1
		}
	}
	return i
}

// lexJS splits JavaScript or TypeScript source into tokens and comments, as
// jslex.Lex does, with each line of a comment as the scan reads it.
func lexJS(src string) (toks []jsToken, comments []comment, unclosed int) {
	lexed, lexedComments, unclosed := jslex.Lex(src)
	toks = make([]jsToken, len(lexed))
	for i, t := range lexed {
		toks[i] = jsToken{kind: t.Kind, text: t.Text, line: t.Line}
	}
	for _, c := range lexedComments {
		if c.Block {
			comments = append(comments, blockComment(c.Text, c.Line, "/*", "*/")...)
		} else {
			comments = append(comments, comment{line: c.Line, text: lineCommentText(c.Text, "//"), ownLine: c.OwnLine})
		}
	}
	return toks, comments, unclosed
}

// isIdentByte reports whether c can be part of an identifier. Bytes of
// non-ASCII characters count, so that such identifiers stay whole.
func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 ||
		c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
