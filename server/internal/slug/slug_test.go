package slug

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// form is the form of a slug that PRD FR-PROJ-02 AC4 gives.
var form = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// TestCheck checks the slugs that Check takes: groups of lowercase ASCII
// letters and digits joined by single hyphens, of at most 64 characters,
// and none of the names that Windows reserves for devices.
//
// Verifies: FR-PROJ-02/AC4
func TestCheck(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"a", "0", "harbour-intro", "con-board", "com10", "lpt00", "conin", strings.Repeat("a", 64),
		"a-b-c"} {
		if err := Check(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	for s, reason := range map[string]Reason{
		"": Format, "-": Format, "-a": Format, "a-": Format, "a--b": Format, "A": Format, "a_b": Format,
		"a b": Format, "ž": Format, "a.b": Format, "ｊｏ": Format, strings.Repeat("a", 65): Format,
		"con": Reserved, "prn": Reserved, "aux": Reserved, "nul": Reserved, "com0": Reserved, "com1": Reserved,
		"com9": Reserved, "lpt0": Reserved, "lpt1": Reserved, "lpt9": Reserved,
	} {
		var e *Error
		if err := Check(s); !errors.As(err, &e) || e.Reason != reason {
			t.Errorf("%q: %v, want %s", s, err, reason)
		}
	}
}

// TestDerive checks the slug derived from a name: its letters and digits in
// lowercase ASCII, without their accents, each run of other characters a
// hyphen, cut to 64 characters, the kind for a name without any, and the
// name's slug with the kind after it for a name that Windows reserves for a
// device. Every slug derived is one that Check takes.
//
// Verifies: FR-PROJ-02/AC4
func TestDerive(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"Harbour Intro":                "harbour-intro",
		"Elara — First Meeting":        "elara-first-meeting",
		"Tomáš Novák":                  "tomas-novak",
		"Příliš žluťoučký kůň":         "prilis-zlutoucky-kun",
		"Straße":                       "strasse",
		"  ...Pier!!  ":                "pier",
		"Act 2, scene 10":              "act-2-scene-10",
		"ＦＵＬＬ ｗｉｄｔｈ":                   "full-width",
		"最初の出会い":                       "board",
		"":                             "board",
		"---":                          "board",
		"CON":                          "con-board",
		"Com1":                         "com1-board",
		"LPT9":                         "lpt9-board",
		"COM0":                         "com0-board",
		"con 2":                        "con-2",
		strings.Repeat("ab ", 40):      strings.TrimRight(strings.Repeat("ab-", 22)[:64], "-"),
		strings.Repeat("x", 63) + " y": strings.Repeat("x", 63),
	} {
		got := Derive(name, "board")
		if got != want {
			t.Errorf("Derive(%q) = %q, want %q", name, got, want)
		}
		if !form.MatchString(got) || len(got) > MaxLength || Check(got) != nil {
			t.Errorf("Derive(%q) = %q, which is not a slug", name, got)
		}
	}
}

// TestWithSuffix checks that a numbered slug keeps its number and fits in 64
// characters, without a hyphen left before the number.
func TestWithSuffix(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ base, suffix, want string }{
		{"harbour", "-2", "harbour-2"},
		{strings.Repeat("a", 64), "-10", strings.Repeat("a", 61) + "-10"},
		{strings.Repeat("a", 62) + "-b", "-2", strings.Repeat("a", 62) + "-2"},
	} {
		if got := WithSuffix(c.base, c.suffix); got != c.want || Check(got) != nil {
			t.Errorf("WithSuffix(%q, %q) = %q, want %q", c.base, c.suffix, got, c.want)
		}
	}
}
