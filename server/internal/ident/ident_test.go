package ident_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
)

// TestCheck checks the identifier grammar of §5.8.1: an ASCII letter or an
// underscore, then ASCII letters, digits, and underscores, of at most 64
// characters, compared case by case, and never one of the language's words.
//
// Verifies: FR-VAR-01/AC1
func TestCheck(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"met_elara", "_", "_9", "a", "Harbour2", "True", "GLOBAL", "elseif_", "cmp",
		strings.Repeat("a", ident.MaxLength)} {
		if err := ident.Check(s); err != nil {
			t.Errorf("%q was refused: %v", s, err)
		}
	}
	for s, want := range map[string]ident.Reason{
		"":                                     ident.Format,
		"9lives":                               ident.Format,
		"met-elara":                            ident.Format,
		"met elara":                            ident.Format,
		"met.elara":                            ident.Format,
		"mét":                                  ident.Format,
		"ｍet":                                  ident.Format,
		"met​elara":                            ident.Format,
		"met_elara\n":                          ident.Format,
		strings.Repeat("a", ident.MaxLength+1): ident.Format,
		"true":                                 ident.Reserved,
		"false":                                ident.Reserved,
		"and":                                  ident.Reserved,
		"or":                                   ident.Reserved,
		"not":                                  ident.Reserved,
		"global":                               ident.Reserved,
		"board":                                ident.Reserved,
		"null":                                 ident.Reserved,
		"if":                                   ident.Reserved,
		"else":                                 ident.Reserved,
		"elseif":                               ident.Reserved,
	} {
		var e *ident.Error
		if err := ident.Check(s); !errors.As(err, &e) || e.Reason != want {
			t.Errorf("%q: %v, want %s", s, err, want)
		}
	}
	// Every reserved word is refused as such, so that a word added to the list
	// is checked too.
	for _, w := range ident.ReservedWords() {
		var e *ident.Error
		if err := ident.Check(w); !errors.As(err, &e) || e.Reason != ident.Reserved {
			t.Errorf("the reserved word %q: %v", w, err)
		}
	}
}
