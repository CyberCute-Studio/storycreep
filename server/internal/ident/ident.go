// Package ident checks the identifiers of the expression language (PRD
// §5.8.1): the keys of a project's variables (FR-VAR-01 AC1), which its
// expressions name them by, and, as they are added, the other names that an
// expression reads. An identifier is an ASCII letter or an underscore, then
// ASCII letters, digits, and underscores, [A-Za-z_][A-Za-z0-9_]*, of at most
// MaxLength characters, compared case by case, and never a word of the
// language itself, as true or global, which the expression would read as
// that word rather than as the name.
package ident

import (
	"fmt"
	"slices"
)

// MaxLength is the most characters that an identifier may have.
const MaxLength = 64

// Reason says why an identifier is not valid.
type Reason string

const (
	// Format is an identifier that is not an ASCII letter or an underscore,
	// then ASCII letters, digits, and underscores, of at most MaxLength
	// characters.
	Format Reason = "format"
	// Reserved is an identifier that is a word of the expression language
	// (ReservedWords).
	Reserved Reason = "reserved"
)

// Error is an identifier that is not valid.
type Error struct {
	Reason Reason
}

func (e *Error) Error() string {
	return "ident: the identifier " + e.Problem()
}

// Problem says what is wrong with the identifier, in English, for programs
// and logs, as the message of a field's problem.
func (e *Error) Problem() string {
	if e.Reason == Reserved {
		return "is a reserved word of the expression language, which no key may be"
	}
	return fmt.Sprintf("is not an ASCII letter or an underscore, then ASCII letters, digits, and underscores, of "+
		"at most %d characters", MaxLength)
}

// reserved are the words of the expression language (§5.8.1), which no
// identifier may be. Identifiers are compared case by case, so True is not
// one of them.
var reserved = []string{"true", "false", "and", "or", "not", "global", "board", "null", "if", "else", "elseif"}

// ReservedWords returns the words of the expression language, which no
// identifier may be, in the order of §5.8.1, as a copy that the caller may
// change.
func ReservedWords() []string {
	return slices.Clone(reserved)
}

// Check returns nil if s is an identifier, and otherwise an *Error that says
// why it is not: Format for one that is not of the form, and Reserved for a
// word of the language.
func Check(s string) error {
	if len(s) == 0 || len(s) > MaxLength {
		return &Error{Reason: Format}
	}
	for i := range len(s) {
		c := s[i]
		letter := 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || c == '_'
		if !letter && (i == 0 || c < '0' || c > '9') {
			return &Error{Reason: Format}
		}
	}
	if slices.Contains(reserved, s) {
		return &Error{Reason: Reserved}
	}
	return nil
}
