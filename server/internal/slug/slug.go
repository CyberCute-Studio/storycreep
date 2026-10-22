// Package slug derives and checks slugs: the human-facing keys of a
// workspace, a project, and a board (PRD §5.2), which are safe in a URL and
// as a file name on every platform, because a board's slug names its file in
// the game project (FR-PROJ-02 AC4, §6.3). A slug is groups of lowercase
// ASCII letters and digits joined by single hyphens, [a-z0-9]+(-[a-z0-9]+)*,
// of at most MaxLength characters, and never a name that Windows reserves for
// a device, as con or com1, which no file may have there whatever its
// extension.
package slug

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// MaxLength is the most characters that a slug may have.
const MaxLength = 64

// Reason says why a slug is not valid.
type Reason string

const (
	// Format is a slug that is not groups of lowercase ASCII letters and
	// digits joined by single hyphens, of at most MaxLength characters.
	Format Reason = "format"
	// Reserved is a slug that is a reserved word: a name that Windows
	// reserves for a device, or, for a workspace, one of the words that the
	// product keeps for itself.
	Reserved Reason = "reserved"
)

// Error is a slug that is not valid.
type Error struct {
	Reason Reason
}

func (e *Error) Error() string {
	if e.Reason == Reserved {
		return "slug: the slug is a reserved word"
	}
	return fmt.Sprintf("slug: a slug is groups of lowercase letters and digits joined by single hyphens, of at "+
		"most %d characters", MaxLength)
}

// Problem says what is wrong with the slug, in English, for programs and
// logs, as the message of a field's problem.
func (e *Error) Problem() string {
	if e.Reason == Reserved {
		return "is a reserved word, such as a name that Windows reserves for a device"
	}
	return fmt.Sprintf("is not groups of lowercase letters and digits joined by single hyphens, of at most %d "+
		"characters", MaxLength)
}

// devices are the names that Windows reserves for devices (FR-PROJ-02 AC4),
// as Microsoft's rules for naming files list them, com0 and lpt0 among them,
// so that a tool that follows those rules takes every board's file. The
// rules' com and lpt with a superscript digit are not slugs.
var devices = []string{
	"aux", "con", "nul", "prn",
	"com0", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
	"lpt0", "lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9",
}

// Devices returns the names that Windows reserves for devices, which no slug
// may be, in the order of the database's checks.
func Devices() []string {
	return slices.Clone(devices)
}

// Device reports whether s is a name that Windows reserves for a device.
func Device(s string) bool {
	return slices.Contains(devices, s)
}

// Check returns an *Error if s is not a slug: Format if it is not well
// formed (WellFormed), and Reserved if it is a name that Windows reserves
// for a device.
func Check(s string) error {
	if !WellFormed(s) {
		return &Error{Reason: Format}
	}
	if Device(s) {
		return &Error{Reason: Reserved}
	}
	return nil
}

// WellFormed reports whether s has the form of a slug, which the database
// checks too: groups of lowercase ASCII letters and digits joined by single
// hyphens, of at most MaxLength characters.
func WellFormed(s string) bool {
	if s == "" || len(s) > MaxLength || s[0] == '-' || s[len(s)-1] == '-' || strings.Contains(s, "--") {
		return false
	}
	for _, c := range []byte(s) {
		if !letterOrDigit(c) && c != '-' {
			return false
		}
	}
	return true
}

// letterOrDigit reports whether c is a lowercase ASCII letter or a digit.
func letterOrDigit(c byte) bool {
	return 'a' <= c && c <= 'z' || '0' <= c && c <= '9'
}

// folded are the lowercase letters that Base spells in ASCII although
// Unicode does not decompose them into an ASCII letter and marks.
var folded = map[rune]string{
	'ß': "ss", 'æ': "ae", 'œ': "oe", 'ø': "o", 'ł': "l", 'đ': "d", 'ð': "d", 'þ': "th", 'ı': "i", 'ħ': "h",
}

// Base returns the slug of the name: its letters and digits in lowercase
// ASCII, without their accents, as tomas-novak for Tomáš Novák, each run of
// other characters a hyphen, cut to MaxLength characters at most, or "" if
// that leaves nothing, as for a name in a script other than Latin, for which
// the caller has a word of its own, such as board. The slug may be a name
// that Windows reserves for a device (Device), which the caller turns into
// another.
func Base(name string) string {
	var b strings.Builder
	gap := false
	add := func(s string) {
		if gap && b.Len() > 0 {
			b.WriteByte('-')
		}
		gap = false
		b.WriteString(s)
	}
	for _, r := range norm.NFKD.String(name) {
		r = unicode.ToLower(r)
		switch {
		case unicode.Is(unicode.Mn, r):
			// An accent of the letter before it.
		case r < unicode.MaxASCII && letterOrDigit(byte(r)):
			add(string(r))
		case folded[r] != "":
			add(folded[r])
		default:
			gap = true
		}
	}
	return strings.TrimRight(b.String()[:min(b.Len(), MaxLength)], "-")
}

// Derive returns the slug of a thing of the kind named name, which a
// project and a board are given when nobody types one (FR-PROJ-02 AC4): the
// name's slug (Base), or the kind, as board, if that is empty, and that with
// a hyphen and the kind, as con-board, if it is a name that Windows reserves
// for a device. kind is a slug, of at most a few characters.
func Derive(name, kind string) string {
	s := Base(name)
	switch {
	case s == "":
		return kind
	case Device(s):
		// A device's name has at most 4 characters, so the slug fits.
		return s + "-" + kind
	}
	return s
}

// WithSuffix returns base with suffix, which starts with a hyphen, cut so
// that the slug has at most MaxLength characters, as harbour-2 for harbour.
func WithSuffix(base, suffix string) string {
	return strings.TrimRight(base[:min(len(base), MaxLength-len(suffix))], "-") + suffix
}
