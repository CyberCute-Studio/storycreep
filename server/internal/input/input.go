// Package input checks the text that people enter for the services to keep,
// such as an account's display name or a workspace's name, which other
// people are then shown, and reports what is wrong with it as FieldErrors,
// which the API answers with validation_failed.
package input

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FieldError is a field of an input that is not valid.
type FieldError struct {
	// Field is the field's name in the API, such as display_name.
	Field string
	// Message says what is wrong with it, in English, for programs and
	// logs.
	Message string
}

// FieldErrors are the fields of an input that are not valid.
type FieldErrors []FieldError

func (e FieldErrors) Error() string {
	var b strings.Builder
	b.WriteString("input: not valid:")
	for _, f := range e {
		fmt.Fprintf(&b, " %s %s;", f.Field, f.Message)
	}
	return strings.TrimSuffix(b.String(), ";")
}

// CheckName says what is wrong with a name that people are shown, such as a
// display name, or returns "". A name has a character that is printed
// (Printed), so that it is not blank, at most maxChars characters, and no
// control character (IsControl), so that it cannot change how the text
// around it is shown. The caller removes the white space around it first.
func CheckName(name string, maxChars int) string {
	switch n := utf8.RuneCountInString(name); {
	case n == 0 || strings.IndexFunc(name, Printed) < 0:
		return "is empty"
	case n > maxChars:
		return fmt.Sprintf("is longer than %d characters", maxChars)
	case strings.IndexFunc(name, IsControl) >= 0:
		return "contains a control character"
	}
	return ""
}

// Printed reports whether r is printed: a letter, a mark, a number,
// punctuation, or a symbol, rather than a space, a control character, a
// format character, such as U+200B ZERO WIDTH SPACE, or another character
// that shows nothing. Those are the characters that Unicode ignores by
// default (Default_Ignorable_Code_Point) although their category is a mark's
// or a letter's, such as U+FE0F VARIATION SELECTOR-16, U+034F COMBINING
// GRAPHEME JOINER, and U+3164 HANGUL FILLER, and the blank characters that
// it does not ignore (blank).
func Printed(r rune) bool {
	return unicode.IsGraphic(r) && !unicode.IsSpace(r) &&
		!unicode.In(r, unicode.Other_Default_Ignorable_Code_Point, unicode.Variation_Selector, blank)
}

// blank holds the characters that show nothing although Unicode neither
// ignores them by default nor counts them as spaces, because they stand for
// an empty place in a script's own writing: U+2800 BRAILLE PATTERN BLANK, a
// symbol without dots, U+13441 EGYPTIAN HIEROGLYPH FULL BLANK and U+13442
// EGYPTIAN HIEROGLYPH HALF BLANK, which are letters, U+16FE4 KHITAN SMALL
// SCRIPT FILLER, a mark, and U+1D159 MUSICAL SYMBOL NULL NOTEHEAD, a symbol.
var blank = &unicode.RangeTable{
	R16: []unicode.Range16{{Lo: 0x2800, Hi: 0x2800, Stride: 1}},
	R32: []unicode.Range32{
		{Lo: 0x13441, Hi: 0x13442, Stride: 1},
		{Lo: 0x16FE4, Hi: 0x16FE4, Stride: 1},
		{Lo: 0x1D159, Hi: 0x1D159, Stride: 1},
	},
}

// IsControl reports whether r is a control character of a name: one of
// category Cc, a bidirectional control, such as U+202E, which reverses the
// text after it, or a line or paragraph separator. Other format characters
// are allowed, because names need some of them, as U+200D ZERO WIDTH JOINER
// joins the emoji of a family, and U+200C ZERO WIDTH NON-JOINER writes
// Persian.
func IsControl(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Bidi_Control, unicode.Zl, unicode.Zp)
}
