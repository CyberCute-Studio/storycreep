package semantics

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Len returns the number of code points of s, as len counts them (§5.8.4,
// §5.8.9), not its bytes, as Go's len does, nor its UTF-16 code units, as
// JavaScript's length does: len("😀") is 1.
func Len(s string) int {
	return utf8.RuneCountInString(s)
}

// Concat returns a + b, the strings one after the other, as + of two strings
// does (§5.8.3), or StringTooLong and "" when that has more than
// MaxStringLength code points, so that no script, however many of its
// statements double a string, builds one longer than a string literal may be,
// nor one that a runtime's memory cannot hold: a script of 64 statements that
// each double a string would otherwise build one of 2^64 times its length. It
// counts code points, as Len does, so that "😀", one code point, two UTF-16
// code units, and four bytes, counts as one. Of strings of at most
// MaxStringLength bytes together, which have at most as many code points, it
// counts none.
func Concat(a, b string) (string, RuntimeError) {
	if len(a)+len(b) > MaxStringLength && Len(a)+Len(b) > MaxStringLength {
		return "", StringTooLong
	}
	return a + b, ""
}

// Compare returns -1, 0, or +1 as a is before, equal to, or after b, in the
// lexicographic order of their code points, as <, <=, >, and >= compare
// strings (§5.8.3, §5.8.9), which the order of their UTF-8 bytes is: the
// string of U+FFFF is before "😀", U+1F600, though JavaScript's < on UTF-16
// code units puts "😀" first, comparing its high surrogate, U+D83D, with
// U+FFFF. Neither string is normalised, so "é", U+00E9, and e followed by
// U+0301 COMBINING ACUTE ACCENT are not equal.
func Compare(a, b string) int {
	return strings.Compare(a, b)
}

// Contains, StartsWith, and EndsWith report whether s holds sub, starts with
// prefix, or ends with suffix, as contains, starts_with, and ends_with do
// (§5.8.4), comparing code points: since UTF-8 holds no code point's bytes
// within another's, comparing the bytes is comparing the code points.
// Neither string is normalised, as Compare says.
func Contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

// StartsWith reports whether s starts with prefix, as Contains says.
func StartsWith(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}

// EndsWith reports whether s ends with suffix, as Contains says.
func EndsWith(s, suffix string) bool {
	return strings.HasSuffix(s, suffix)
}

// Upper returns s with each code point mapped to its simple uppercase
// mapping, as upper does (§5.8.4, §5.8.9), from the tables of
// UnicodeVersion rather than from package unicode's, which are of the
// version of Unicode that the Go toolchain has. A simple mapping maps one
// code point to one, so the result has as many code points as s: upper("ß")
// is "ß", where the full mapping, which strings.ToUpper does not use either,
// would be "SS". It maps no code point by locale: upper("i") is "I", in
// Turkish too.
func Upper(s string) string {
	return mapCase(s, upperTable, upperASCII)
}

// Lower returns s with each code point mapped to its simple lowercase
// mapping, as lower does, as Upper says: lower("İ") is "i", one code point,
// and lower("Σ") is "σ", wherever it is in a word.
func Lower(s string) string {
	return mapCase(s, lowerTable, lowerASCII)
}

// upperASCII and lowerASCII hold what upperTable and lowerTable map each
// ASCII code point to, itself when they do not map it, by code point, so
// that mapCase looks those up without a search.
var (
	upperASCII = asciiOf(upperTable)
	lowerASCII = asciiOf(lowerTable)
)

// asciiOf returns what table maps each ASCII code point to, as upperASCII
// holds it.
func asciiOf(table []caseMapping) *[utf8.RuneSelf]rune {
	var a [utf8.RuneSelf]rune
	for r := range a {
		a[r], _ = caseOf(table, rune(r))
	}
	return &a
}

// mapCase returns s with each code point that table maps mapped, ascii
// being what table maps the ASCII code points to, and s itself when it maps
// none of them. A byte of s that is not UTF-8, which no string of the
// language holds, is kept as it is.
func mapCase(s string, table []caseMapping, ascii *[utf8.RuneSelf]rune) string {
	mapRune := func(r rune) (rune, bool) {
		if r < utf8.RuneSelf {
			return ascii[r], ascii[r] != r
		}
		return caseOf(table, r)
	}
	// A byte that is not UTF-8 is read as U+FFFD, which no table maps.
	for first, r := range s {
		if _, mapped := mapRune(r); !mapped {
			continue
		}
		var b strings.Builder
		b.Grow(len(s))
		b.WriteString(s[:first])
		for i := first; i < len(s); {
			r, size := utf8.DecodeRuneInString(s[i:])
			if to, mapped := mapRune(r); mapped {
				b.WriteRune(to)
			} else {
				b.WriteString(s[i : i+size])
			}
			i += size
		}
		return b.String()
	}
	return s
}

// caseOf returns the code point that table maps r to, and whether it maps r.
func caseOf(table []caseMapping, r rune) (rune, bool) {
	i, found := slices.BinarySearchFunc(table, r, func(m caseMapping, r rune) int {
		return cmp.Compare(m.from, r)
	})
	if !found {
		return r, false
	}
	return table[i].to, true
}

// Trim returns s without the code points of White_Space, of the tables of
// UnicodeVersion, that it starts or ends with, as trim does (§5.8.4,
// §5.8.9). White_Space holds no U+FEFF ZERO WIDTH NO-BREAK SPACE, which
// JavaScript's trim removes, no U+200B ZERO WIDTH SPACE, and no U+180E
// MONGOLIAN VOWEL SEPARATOR, which Unicode took out of it in 6.3.
func Trim(s string) string {
	return strings.TrimFunc(s, IsWhiteSpace)
}

// IsWhiteSpace reports whether r is a code point of White_Space, of the
// tables of UnicodeVersion. The ranges are in their order, so it stops at
// the first that ends at r or after it.
func IsWhiteSpace(r rune) bool {
	for _, s := range whiteSpace {
		if r <= s.last {
			return s.first <= r
		}
	}
	return false
}

// A caseMapping maps the code point from to the code point to, as a line of
// UnicodeData.txt maps a code point to its simple uppercase or lowercase
// mapping. A table of them is in the order of from.
type caseMapping struct {
	from, to rune
}

// A span is the code points from first to last, both of them included.
type span struct {
	first, last rune
}
