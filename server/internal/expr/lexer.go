package expr

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/semantics"
)

// tokenKind is the kind of a token.
type tokenKind uint8

const (
	tEOF   tokenKind = iota
	tError           // a token that the lexer could not read, whose error is in err
	// tNewline is a line break that ends a statement of a script (lex).
	tNewline
	tSemicolon
	tIdent
	tInt
	tFloat
	tString
	tTrue
	tFalse
	tGlobal
	tBoard
	tReserved // a reserved word that no rule of the grammar reads: null, if, else, or elseif
	tLParen
	tRParen
	tComma
	tDot
	tOr  // || or or
	tAnd // && or and
	tNot // ! or not
	tEq
	tNe
	tLt
	tLe
	tGt
	tGe
	tPlus
	tMinus
	tStar
	tSlash
	tPercent
	tAssign
	tAddAssign
	tSubAssign
	tMulAssign
	tDivAssign
	tModAssign
)

// token is a token of a source.
type token struct {
	kind tokenKind
	Range
	// text is a word as written, or a string literal's value.
	text string
	// keyword is set for a reserved word (ident.ReservedWords), as true or
	// and is, though not for the && that and stands for.
	keyword bool
	i       int64   // an int literal's value
	f       float64 // a float literal's value
	err     *Error  // a tError's error
}

// errorToken returns a tError of the error code over r.
func errorToken(code Code, r Range) token {
	return token{kind: tError, Range: r, err: &Error{Code: code, Range: r}}
}

// lex splits src into tokens, the last of which is a tEOF, read as the source
// of ctx. It stops at the first token that it cannot read, a tError, and ends
// the tokens there, since the parser stops there at the latest.
//
// In a script, a line break outside parentheses after a name, a literal, or a
// ), the tokens that an expression ends with, ends the statement before it,
// and lex writes it as a tNewline: one for each run of line breaks, comments,
// and white space. Every other line break, and every line break of a
// condition, is white space, so that an expression can go on on the next line
// after an operator, a comma, or an opening parenthesis.
func lex(src string, ctx Context) []token {
	l := &lexer{src: src}
	var toks []token
	prev := tEOF // the kind of the last token, or tEOF before the first
	depth := 0   // how many parentheses are open
	for {
		brk := -1 // where the first line break since the last token is, or -1
	space:
		for {
			r, n := l.peek()
			switch {
			case r == ' ' || r == '\t':
				l.step(n)
			case r == '\n' || r == '\r':
				if brk < 0 {
					brk = l.at
				}
				l.step(n)
			case r == '/' && strings.HasPrefix(l.src[l.i:], "//"):
				if t, ok := l.comment(); !ok {
					return append(toks, t, token{kind: tEOF, Range: Range{t.End, t.End}})
				}
			default:
				break space
			}
		}
		if brk >= 0 && ctx == ContextScript && depth == 0 && endsExpression(prev) {
			toks = append(toks, token{kind: tNewline, Range: Range{brk, brk + 1}})
			prev = tNewline
		}
		if l.i == len(l.src) {
			return append(toks, token{kind: tEOF, Range: Range{l.at, l.at}})
		}
		t := l.token()
		toks = append(toks, t)
		switch t.kind {
		case tError:
			return append(toks, token{kind: tEOF, Range: Range{t.End, t.End}})
		case tLParen:
			depth++
		case tRParen:
			depth = max(depth-1, 0)
		}
		prev = t.kind
	}
}

// endsExpression reports whether an expression can end with a token of kind
// k: a name, a literal, or a ).
func endsExpression(k tokenKind) bool {
	switch k {
	case tIdent, tInt, tFloat, tString, tTrue, tFalse, tRParen:
		return true
	}
	return false
}

// lexer reads a source's characters, counting them in code points.
type lexer struct {
	src string
	i   int // the byte offset of the next character
	at  int // the code point offset of the next character
}

// peek returns the next character and its length in bytes, or -1 and 0 at the
// end of the source. A byte that is not UTF-8 is utf8.RuneError of length 1.
func (l *lexer) peek() (rune, int) {
	if l.i == len(l.src) {
		return -1, 0
	}
	return utf8.DecodeRuneInString(l.src[l.i:])
}

// step steps past the next character, of n bytes.
func (l *lexer) step(n int) {
	l.i += n
	l.at++
}

// forbidden reports whether a character that peek returned, of n bytes, is one
// that no text holds wherever it is: a NUL, which PostgreSQL's text and jsonb
// cannot hold, or a byte that is not UTF-8.
func forbidden(r rune, n int) bool {
	return r == 0 || r == utf8.RuneError && n == 1
}

// comment steps past a comment, from its // to the line break or the end of
// the source after it, and returns ok. A forbidden character in it is
// InvalidCharacter, which it returns with ok false.
func (l *lexer) comment() (t token, ok bool) {
	l.step(1)
	l.step(1)
	for {
		r, n := l.peek()
		switch {
		case r == -1 || r == '\n' || r == '\r':
			return token{}, true
		case forbidden(r, n):
			return errorToken(InvalidCharacter, Range{l.at, l.at + 1}), false
		}
		l.step(n)
	}
}

// token reads the token that starts at the next character, which is not white
// space nor a comment's //.
func (l *lexer) token() token {
	start := l.at
	r, n := l.peek()
	switch {
	case isWordStart(r):
		return l.word()
	case '0' <= r && r <= '9':
		return l.number()
	case r == '"':
		return l.string()
	}
	l.step(n)
	one := func(k tokenKind) token {
		return token{kind: k, Range: Range{start, l.at}}
	}
	// two returns a token of kind long if the next character is next, which it
	// steps past, and otherwise one of kind short.
	two := func(next byte, long, short tokenKind) token {
		if l.i < len(l.src) && l.src[l.i] == next {
			l.step(1)
			return one(long)
		}
		return one(short)
	}
	switch r {
	case '(':
		return one(tLParen)
	case ')':
		return one(tRParen)
	case ',':
		return one(tComma)
	case '.':
		return one(tDot)
	case ';':
		return one(tSemicolon)
	case '+':
		return two('=', tAddAssign, tPlus)
	case '-':
		return two('=', tSubAssign, tMinus)
	case '*':
		return two('=', tMulAssign, tStar)
	case '/':
		return two('=', tDivAssign, tSlash)
	case '%':
		return two('=', tModAssign, tPercent)
	case '=':
		return two('=', tEq, tAssign)
	case '!':
		return two('=', tNe, tNot)
	case '<':
		return two('=', tLe, tLt)
	case '>':
		return two('=', tGe, tGt)
	case '&', '|':
		// Only doubled: && and ||.
		if l.i < len(l.src) && rune(l.src[l.i]) == r {
			l.step(1)
			if r == '&' {
				return one(tAnd)
			}
			return one(tOr)
		}
	}
	return errorToken(InvalidCharacter, Range{start, start + 1})
}

// isWordStart reports whether r starts an identifier: an ASCII letter or an
// underscore.
func isWordStart(r rune) bool {
	return 'A' <= r && r <= 'Z' || 'a' <= r && r <= 'z' || r == '_'
}

// isWordByte reports whether c is a character of an identifier after its
// first: an ASCII letter, digit, or underscore.
func isWordByte(c byte) bool {
	return isWordStart(rune(c)) || '0' <= c && c <= '9'
}

// word reads an identifier, [A-Za-z_][A-Za-z0-9_]*, or the reserved word of
// that form.
func (l *lexer) word() token {
	start, from := l.at, l.i
	for l.i < len(l.src) && isWordByte(l.src[l.i]) {
		l.step(1)
	}
	t := token{Range: Range{start, l.at}, text: l.src[from:l.i], keyword: true}
	if len(t.text) > MaxIdentifierLength {
		return errorToken(IdentifierTooLong, t.Range)
	}
	switch t.text {
	case "true":
		t.kind = tTrue
	case "false":
		t.kind = tFalse
	case "and":
		t.kind = tAnd
	case "or":
		t.kind = tOr
	case "not":
		t.kind = tNot
	case "global":
		t.kind = tGlobal
	case "board":
		t.kind = tBoard
	default:
		t.kind, t.keyword = tIdent, false
		if ident.IsReserved(t.text) {
			t.kind, t.keyword = tReserved, true
		}
	}
	return t
}

// number reads a number: the run of letters, digits, underscores, and points
// that starts with a digit, with a sign after an e or an E of it, which is an
// integer literal, [0-9]+, or a float literal,
// [0-9]+\.[0-9]+([eE][+-]?[0-9]+)?, or else InvalidNumber, as 1., 1e5, 3abc,
// and 1_000 are, whole.
func (l *lexer) number() token {
	start, from := l.at, l.i
	for l.i < len(l.src) {
		c := l.src[l.i]
		sign := (c == '+' || c == '-') && (l.src[l.i-1] == 'e' || l.src[l.i-1] == 'E')
		if !isWordByte(c) && c != '.' && !sign {
			break
		}
		l.step(1)
	}
	text, r := l.src[from:l.i], Range{start, l.at}
	switch {
	case isDigits(text):
		// ParseInt reads every run of digits, and reports only one beyond
		// ±MaxInt.
		n, problem := semantics.ParseInt(text)
		if problem != "" {
			return errorToken(IntegerOutOfRange, r)
		}
		return token{kind: tInt, Range: r, i: n}
	case isFloatLiteral(text):
		// ParseFloat reads every float literal, as to_float reads one, and
		// reports only one beyond a double's range.
		f, problem := semantics.ParseFloat(text)
		if problem != "" {
			return errorToken(FloatOutOfRange, r)
		}
		return token{kind: tFloat, Range: r, f: f}
	}
	return errorToken(InvalidNumber, r)
}

// isDigits reports whether s is one or more ASCII digits.
func isDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// isFloatLiteral reports whether s is a float literal,
// [0-9]+\.[0-9]+([eE][+-]?[0-9]+)?: digits on both sides of a point, and an
// exponent or none.
func isFloatLiteral(s string) bool {
	mantissa, exponent, hasExponent := s, "", false
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa, exponent, hasExponent = s[:i], s[i+1:], true
		if exponent != "" && (exponent[0] == '+' || exponent[0] == '-') {
			exponent = exponent[1:]
		}
	}
	whole, fraction, point := strings.Cut(mantissa, ".")
	return point && isDigits(whole) && isDigits(fraction) && (!hasExponent || isDigits(exponent))
}

// string reads a string literal, from its opening quote to its closing one,
// and its value: its characters, its escapes the characters that they stand
// for. A string that its line or the source ends in is UnterminatedString, up
// to that end; a bad escape is InvalidEscape; a forbidden character is
// InvalidCharacter, each at the first that the string has, from left to
// right. A string read whole whose value is longer than MaxStringLength code
// points is StringTooLong, whole.
func (l *lexer) string() token {
	start := l.at
	l.step(1)
	var b strings.Builder
	length := 0
	for {
		r, n := l.peek()
		switch {
		case r == -1 || r == '\n' || r == '\r':
			return errorToken(UnterminatedString, Range{start, l.at})
		case r == '"':
			l.step(n)
			if length > MaxStringLength {
				return errorToken(StringTooLong, Range{start, l.at})
			}
			return token{kind: tString, Range: Range{start, l.at}, text: b.String()}
		case r == '\\':
			var err *Error
			if r, err = l.escape(start); err != nil {
				return token{kind: tError, Range: err.Range, err: err}
			}
		case forbidden(r, n):
			return errorToken(InvalidCharacter, Range{l.at, l.at + 1})
		default:
			l.step(n)
		}
		// A string too long is read to its end, for its range, but its
		// value no further.
		if length++; length <= MaxStringLength {
			b.WriteRune(r)
		}
	}
}

// escape reads the escape at the next character, a backslash in the string
// literal that opens at open, and returns the character that it stands for.
// The range of an InvalidEscape is the backslash and the character after it,
// or, after \u, the hexadecimal digits that follow, up to four, so that \u12
// of \u12G4 is, and \uD800, a high surrogate without a low one's escape after
// it, is.
func (l *lexer) escape(open int) (rune, *Error) {
	at := l.at
	l.step(1)
	r, n := l.peek()
	switch r {
	case -1, '\n', '\r':
		return 0, &Error{Code: UnterminatedString, Range: Range{open, l.at}}
	case '"', '\\':
		l.step(n)
		return r, nil
	case 'n':
		l.step(n)
		return '\n', nil
	case 't':
		l.step(n)
		return '\t', nil
	case 'r':
		l.step(n)
		return '\r', nil
	case 'u':
		l.step(n)
		u, ok := l.hex4()
		switch {
		case !ok || u == 0 || utf16.IsSurrogate(u) && u >= 0xDC00:
		case utf16.IsSurrogate(u):
			if low, ok := l.lowSurrogate(); ok {
				return utf16.DecodeRune(u, low), nil
			}
		default:
			return u, nil
		}
		return 0, &Error{Code: InvalidEscape, Range: Range{at, l.at}}
	}
	l.step(n)
	return 0, &Error{Code: InvalidEscape, Range: Range{at, l.at}}
}

// hex4 steps past the hexadecimal digits at the next character, up to four,
// and returns their value, and whether there are four.
func (l *lexer) hex4() (rune, bool) {
	var u rune
	for k := range 4 {
		v, ok := hexDigit(l.src, l.i)
		if !ok {
			return 0, false
		}
		u = u<<4 | v
		l.step(1)
		if k == 3 {
			return u, true
		}
	}
	return 0, false
}

// lowSurrogate steps past a \uXXXX of a low surrogate at the next character,
// and returns the surrogate, or returns ok false, stepping past nothing, if
// there is none.
func (l *lexer) lowSurrogate() (rune, bool) {
	if !strings.HasPrefix(l.src[l.i:], `\u`) {
		return 0, false
	}
	var u rune
	for k := range 4 {
		v, ok := hexDigit(l.src, l.i+2+k)
		if !ok {
			return 0, false
		}
		u = u<<4 | v
	}
	if u < 0xDC00 || u >= 0xE000 {
		return 0, false
	}
	for range 6 {
		l.step(1)
	}
	return u, true
}

// hexDigit returns the value of the hexadecimal digit, in either case, at
// byte i of s, and whether there is one.
func hexDigit(s string, i int) (rune, bool) {
	if i >= len(s) {
		return 0, false
	}
	switch c := rune(s[i]); {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
