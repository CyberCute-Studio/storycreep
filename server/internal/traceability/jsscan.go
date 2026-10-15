package traceability

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/CyberCute-Studio/storycreep/server/internal/doccheck"
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
// arguments, the call skips that test or group, or makes it an expected
// failure, so it verifies nothing.
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
// test.fail() without arguments. A group is skipped too when it runs no test:
// everything declared directly in it, its tests and groups, is skipped, as in
// describe("…", () => { describe.todo("…") }).
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
		// test.fail() without arguments, which skips the test or group it is
		// called in.
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
				c.decl, c.unconditional = false, len(args) == 0
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
	// test.skip() without arguments skips the test or group it is called in,
	// also from a hook or a step, or every test in the file when it is
	// called outside them.
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
	// runs[k] is set when calls[k] is a declaration that runs a test: a test
	// that is not skipped, or a group that is not skipped and in which a
	// declaration runs. A group in which the scan sees no declaration, as one
	// whose tests a helper function declares, runs. The declarations in a
	// call come after it, so they are decided first.
	runs := make([]bool, len(calls))
	holds := make([]bool, len(calls))  // a declaration is in calls[k]
	inRuns := make([]bool, len(calls)) // a declaration in calls[k] runs
	for k := len(calls) - 1; k >= 0; k-- {
		c := calls[k]
		if !c.decl {
			continue
		}
		runs[k] = !skippedAt[k] && (!c.group || !holds[k] || inRuns[k])
		if p := parent[k]; p >= 0 {
			holds[p] = true
			inRuns[p] = inRuns[p] || runs[k]
		}
	}
	for k, c := range calls {
		if !c.decl {
			continue
		}
		d := decl{line: toks[c.head].line, unnamed: !c.named, skipped: !runs[k]}
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

// jsRegexAfter are the keywords after which "/" starts a regular expression
// literal rather than a division.
var jsRegexAfter = map[string]bool{
	"return": true, "typeof": true, "instanceof": true, "in": true, "of": true, "new": true,
	"delete": true, "void": true, "throw": true, "case": true, "do": true, "else": true,
	"yield": true, "await": true,
}

// jsUnescape returns the value of the text of a string or template literal:
// its escapes, as in "caf\u00e9", replaced by the characters they stand for,
// and its line continuations removed.
func jsUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch c := s[i]; c {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'v':
			b.WriteByte('\v')
		case '0':
			b.WriteByte(0)
		case '\n':
			// A line continuation.
		case 'x', 'u':
			r, n := jsCodePoint(s[i:])
			if n == 0 {
				b.WriteByte(c) // not an escape after all
				continue
			}
			b.WriteRune(r)
			i += n - 1
		default:
			// Any other character, such as a quote, a backslash, or a
			// backtick, stands for itself.
			b.WriteByte(c)
		}
	}
	return b.String()
}

// jsCodePoint reads the character of an escape after its backslash: xHH,
// uHHHH (with the low surrogate that follows a high one), or u{H…}. n is the
// length of the escape, or 0 if it is not one.
func jsCodePoint(s string) (r rune, n int) {
	hex := func(h string) (rune, bool) {
		v, err := strconv.ParseUint(h, 16, 32)
		return rune(v), err == nil && v <= unicode.MaxRune
	}
	switch {
	case s[0] == 'x' && len(s) >= 3:
		if v, ok := hex(s[1:3]); ok {
			return v, 3
		}
	case strings.HasPrefix(s, "u{"):
		if end := strings.IndexByte(s, '}'); end > 2 {
			if v, ok := hex(s[2:end]); ok {
				return v, end + 1
			}
		}
	case s[0] == 'u' && len(s) >= 5:
		v, ok := hex(s[1:5])
		if !ok {
			break
		}
		if utf16.IsSurrogate(v) && len(s) >= 11 && s[5:7] == `\u` {
			if lo, ok := hex(s[7:11]); ok {
				if r := utf16.DecodeRune(v, lo); r != unicode.ReplacementChar {
					return r, 11
				}
			}
		}
		return v, 5
	}
	return 0, 0
}

// lexJS splits JavaScript or TypeScript source into tokens and comments. It
// reads string, template, and regular expression literals, so that comment
// markers and quotes inside them are not taken for code. A quoted string
// ends at the end of its line at the latest, so that a quote it cannot pair,
// such as an apostrophe in JSX text, confuses at most that line. unclosed is
// the line of a template literal or block comment that the source ends in,
// as one opened by a backtick in JSX text does, or 0.
func lexJS(src string) (toks []jsToken, comments []comment, unclosed int) {
	line := 1
	code := false // a token or a block comment is on the line before i
	depth := 0    // the depth of "{" nesting
	// The substitutions the tokenizer is in, innermost last: the depth of
	// "{" nesting at their "${", and the line of their template literal.
	type substitution struct{ depth, line int }
	var subst []substitution
	emit := func(kind byte, text string, at int) {
		toks = append(toks, jsToken{kind: kind, text: text, line: at})
		code = true
	}
	// template reads a template literal from i, after its "`" (first) or
	// after the "}" of a substitution, up to its closing "`" or its next
	// "${". It returns the index after what it read.
	template := func(i int, first bool, startLine int) int {
		start := i
		for i < len(src) {
			switch {
			case src[i] == '\\':
				if i+1 < len(src) && src[i+1] == '\n' {
					line++
				}
				i += 2
				continue
			case src[i] == '`':
				if first {
					emit('s', jsUnescape(src[start:i]), startLine)
				} else {
					emit('e', "", line)
				}
				return i + 1
			case strings.HasPrefix(src[i:], "${"):
				if first {
					emit('t', "", startLine)
				} else {
					emit('m', "", line)
				}
				subst = append(subst, substitution{depth: depth, line: startLine})
				return i + 2
			case src[i] == '\n':
				line++
			}
			i++
		}
		if first {
			emit('t', "", startLine)
		}
		unclosed = startLine
		return i
	}

	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\n':
			line++
			code = false
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			i++
		case strings.HasPrefix(src[i:], "//"):
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			comments = append(comments, comment{line: line, text: lineCommentText(src[i:i+end], "//"), ownLine: !code})
			i += end
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				end = len(src)
				unclosed = line
			} else {
				end += i + 4
			}
			text := src[i:end]
			comments = append(comments, blockComment(text, line, "/*", "*/")...)
			line += strings.Count(text, "\n")
			code = true
			i = end
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(src) && src[j] != c && src[j] != '\n' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(src) || src[j] != c {
				// Not a string: a quote that nothing on its line closes, such as
				// the apostrophe in the JSX text "Don't".
				emit('(', string(c), line)
				i++
				continue
			}
			emit('s', jsUnescape(src[i+1:j]), line)
			line += strings.Count(src[i:j], "\n") // escaped line breaks
			i = j + 1
		case c == '`':
			i = template(i+1, true, line)
		case c == '}' && len(subst) > 0 && depth == subst[len(subst)-1].depth:
			startLine := subst[len(subst)-1].line
			subst = subst[:len(subst)-1]
			i = template(i+1, false, startLine)
		case c == '/' && jsRegexAllowed(toks):
			j, inClass := i+1, false
			for j < len(src) && src[j] != '\n' && (inClass || src[j] != '/') {
				switch src[j] {
				case '\\':
					// An escape, but not of a line break, which ends the line
					// and with it the literal.
					if j+1 < len(src) && src[j+1] != '\n' {
						j++
					}
				case '[':
					inClass = true
				case ']':
					inClass = false
				}
				j++
			}
			if j >= len(src) || src[j] != '/' {
				emit('(', "/", line) // not a regular expression after all
				i++
				continue
			}
			for j++; j < len(src) && isIdentByte(src[j]); j++ {
			}
			emit('r', src[i:j], line)
			i = j
		case isIdentByte(c) && !(c >= '0' && c <= '9'):
			j := i
			for j < len(src) && isIdentByte(src[j]) {
				j++
			}
			emit('i', src[i:j], line)
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && (isIdentByte(src[j]) || src[j] == '.') {
				j++
			}
			emit('n', src[i:j], line)
			i = j
		default:
			switch c {
			case '{':
				depth++
			case '}':
				depth--
			}
			emit('(', string(c), line)
			i++
		}
	}
	if unclosed == 0 && len(subst) > 0 {
		unclosed = subst[0].line
	}
	return toks, comments, unclosed
}

// jsRegexAllowed reports whether a "/" after toks starts a regular
// expression literal: it does where an expression may start, as after "(",
// an operator, "return", or the "${" of a substitution, and not after an
// operand. Nor does it after "}", nor after "<", where "/" closes a JSX tag
// ("</b>"), nor after a postfix "++" or "--", nor after TypeScript's
// non-null assertion, as in "total! / 2", where it divides.
func jsRegexAllowed(toks []jsToken) bool {
	if len(toks) == 0 {
		return true
	}
	t := toks[len(toks)-1]
	switch {
	case t.is('(', "}"), t.is('(', "<"):
		return false
	case t.is('(', "+"), t.is('(', "-"):
		return len(toks) < 2 || toks[len(toks)-2] != t
	case t.is('(', "!"):
		return len(toks) < 2 || !endsOperand(toks[len(toks)-2])
	}
	return !endsOperand(t)
}

// endsOperand reports whether an operand can end with t: an identifier that
// is not a keyword such as "return", a literal, or ")" or "]".
func endsOperand(t jsToken) bool {
	switch t.kind {
	case 'i':
		return !jsRegexAfter[t.text]
	case 'n', 's', 'e', 'r':
		return true
	case '(':
		return t.text == ")" || t.text == "]"
	}
	return false // 't' or 'm', the start of a substitution
}

// isIdentByte reports whether c can be part of an identifier. Bytes of
// non-ASCII characters count, so that such identifiers stay whole.
func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 ||
		c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
