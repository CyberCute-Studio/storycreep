// Package jslex splits JavaScript and TypeScript source into tokens and
// comments, well enough for the repository's tools to find calls and their
// literal arguments without a parser: the traceability report finds test
// declarations with it, and catalog extraction finds message keys. It reads
// string, template, and regular expression literals, so that comment markers,
// quotes, and code inside them are not taken for code, but it does not parse
// JSX, whose text it reads as code.
package jslex

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// The kinds of Token.
const (
	Ident  byte = 'i' // an identifier or keyword
	String byte = 's' // a string literal, or a template literal without substitutions
	// TemplateStart, TemplateMiddle, and TemplateEnd are the parts of a
	// template literal with substitutions: its start, its text between two
	// substitutions, and its end. The tokens of the substitutions are between
	// them.
	TemplateStart  byte = 't'
	TemplateMiddle byte = 'm'
	TemplateEnd    byte = 'e'
	Number         byte = 'n'
	Regexp         byte = 'r' // a regular expression literal
	Punct          byte = '(' // punctuation, one character per token
)

// Token is a token of JavaScript or TypeScript source.
type Token struct {
	Kind byte
	// Text is the identifier, the punctuation, the number or regular
	// expression as written, or the value of a string: its escapes replaced
	// by the characters they stand for. It is empty for the parts of a
	// template literal with substitutions.
	Text string
	Line int // the line the token starts on, from 1
}

// Is reports whether t is of kind and has the text text.
func (t Token) Is(kind byte, text string) bool {
	return t.Kind == kind && t.Text == text
}

// Comment is a comment, as written, with its markers.
type Comment struct {
	Line  int // the line it starts on
	Text  string
	Block bool // a /* */ comment, rather than a // one
	// OwnLine is set for a line comment with nothing but white space before
	// it on its line.
	OwnLine bool
}

// Lex splits src into tokens and comments. A quoted string ends at the end of
// its line at the latest, so that a quote it cannot pair, such as an
// apostrophe in JSX text, confuses at most that line. unclosed is the line of
// a template literal or block comment that the source ends in, as one opened
// by a backtick in JSX text does, or 0.
func Lex(src string) (toks []Token, comments []Comment, unclosed int) {
	line := 1
	code := false // a token or a block comment is on the line before i
	depth := 0    // the depth of "{" nesting
	// The substitutions the tokenizer is in, innermost last: the depth of
	// "{" nesting at their "${", and the line of their template literal.
	type substitution struct{ depth, line int }
	var subst []substitution
	emit := func(kind byte, text string, at int) {
		toks = append(toks, Token{Kind: kind, Text: text, Line: at})
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
					emit(String, unescape(src[start:i]), startLine)
				} else {
					emit(TemplateEnd, "", line)
				}
				return i + 1
			case strings.HasPrefix(src[i:], "${"):
				if first {
					emit(TemplateStart, "", startLine)
				} else {
					emit(TemplateMiddle, "", line)
				}
				subst = append(subst, substitution{depth: depth, line: startLine})
				return i + 2
			case src[i] == '\n':
				line++
			}
			i++
		}
		if first {
			emit(TemplateStart, "", startLine)
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
			comments = append(comments, Comment{Line: line, Text: src[i : i+end], OwnLine: !code})
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
			comments = append(comments, Comment{Line: line, Text: text, Block: true})
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
				emit(Punct, string(c), line)
				i++
				continue
			}
			emit(String, unescape(src[i+1:j]), line)
			line += strings.Count(src[i:j], "\n") // escaped line breaks
			i = j + 1
		case c == '`':
			i = template(i+1, true, line)
		case c == '}' && len(subst) > 0 && depth == subst[len(subst)-1].depth:
			startLine := subst[len(subst)-1].line
			subst = subst[:len(subst)-1]
			i = template(i+1, false, startLine)
		case c == '/' && regexAllowed(toks):
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
				emit(Punct, "/", line) // not a regular expression after all
				i++
				continue
			}
			for j++; j < len(src) && isIdentByte(src[j]); j++ {
			}
			emit(Regexp, src[i:j], line)
			i = j
		case isIdentByte(c) && !(c >= '0' && c <= '9'):
			j := i
			for j < len(src) && isIdentByte(src[j]) {
				j++
			}
			emit(Ident, src[i:j], line)
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && (isIdentByte(src[j]) || src[j] == '.') {
				j++
			}
			emit(Number, src[i:j], line)
			i = j
		default:
			switch c {
			case '{':
				depth++
			case '}':
				depth--
			}
			emit(Punct, string(c), line)
			i++
		}
	}
	if unclosed == 0 && len(subst) > 0 {
		unclosed = subst[0].line
	}
	return toks, comments, unclosed
}

// regexAfter are the keywords after which "/" starts a regular expression
// literal rather than a division.
var regexAfter = map[string]bool{
	"return": true, "typeof": true, "instanceof": true, "in": true, "of": true, "new": true,
	"delete": true, "void": true, "throw": true, "case": true, "do": true, "else": true,
	"yield": true, "await": true,
}

// regexAllowed reports whether a "/" after toks starts a regular expression
// literal: it does where an expression may start, as after "(", an operator,
// "return", or the "${" of a substitution, and not after an operand. Nor does
// it after "}", nor after "<", where "/" closes a JSX tag ("</b>"), nor after a
// postfix "++" or "--", nor after TypeScript's non-null assertion, as in
// "total! / 2", where it divides.
func regexAllowed(toks []Token) bool {
	if len(toks) == 0 {
		return true
	}
	t := toks[len(toks)-1]
	switch {
	case t.Is(Punct, "}"), t.Is(Punct, "<"):
		return false
	case t.Is(Punct, "+"), t.Is(Punct, "-"):
		return len(toks) < 2 || toks[len(toks)-2] != t
	case t.Is(Punct, "!"):
		return len(toks) < 2 || !endsOperand(toks[len(toks)-2])
	}
	return !endsOperand(t)
}

// endsOperand reports whether an operand can end with t: an identifier that
// is not a keyword such as "return", a literal, or ")" or "]".
func endsOperand(t Token) bool {
	switch t.Kind {
	case Ident:
		return !regexAfter[t.Text]
	case Number, String, TemplateEnd, Regexp:
		return true
	case Punct:
		return t.Text == ")" || t.Text == "]"
	}
	return false // the start of a substitution
}

// isIdentByte reports whether c can be part of an identifier. Bytes of
// non-ASCII characters count, so that such identifiers stay whole.
func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 ||
		c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// unescape returns the value of the text of a string or template literal:
// its escapes, as in "café", replaced by the characters they stand for,
// and its line continuations removed.
func unescape(s string) string {
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
			r, n := codePoint(s[i:])
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

// codePoint reads the character of an escape after its backslash: xHH, uHHHH
// (with the low surrogate that follows a high one), or u{H…}. n is the length
// of the escape, or 0 if it is not one.
func codePoint(s string) (r rune, n int) {
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
