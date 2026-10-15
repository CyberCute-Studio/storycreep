package i18n

import (
	"math"
	"strings"
	"testing"

	"golang.org/x/text/language"
)

// nbsp is the no-break space, which Czech groups digits with.
var nbsp = string(rune(0xa0))

func formatString(t *testing.T, tag language.Tag, msg string, args Args) (string, error) {
	t.Helper()
	nodes, err := parseMessage(msg)
	if err != nil {
		t.Fatalf("parsing %q: %v", msg, err)
	}
	var b strings.Builder
	err = format(&b, newLocale(tag), nodes, args, nil)
	return b.String(), err
}

// TestUnsupported checks that the server refuses the syntax it cannot format
// yet, which intl-messageformat accepts, so that a catalog with it fails to
// load rather than render differently on the server.
func TestUnsupported(t *testing.T) {
	for _, msg := range []string{
		"{d, date}",
		"{d, date, short}",
		"{t, time, ::hhmm}",
		"{n, number, percent}",
		"{n, number, currency}",
		"{n, number, ::compact-short}",
		"{n, spellout}",
	} {
		if _, err := parseMessage(msg); err == nil {
			t.Errorf("parsing %q succeeded, want an error", msg)
		}
	}
}

func TestArgumentTypes(t *testing.T) {
	tests := []struct {
		msg  string
		args Args
		want string
	}{
		{"{n}", Args{"n": int64(1) << 60}, "1152921504606846976"},
		{"{n}", Args{"n": uint8(7)}, "7"},
		{"{n}", Args{"n": float32(0.1)}, "0.1"},
		{"{n}", Args{"n": 1e-7}, "1e-7"},
		{"{n}", Args{"n": 0.000001}, "0.000001"},
		{"{n}", Args{"n": math.Copysign(0, -1)}, "0"},
		{"{n}", Args{"n": math.Inf(-1)}, "-Infinity"},
		{"{n, number}", Args{"n": int64(1) << 60}, "1,152,921,504,606,846,976"},
		{"{n, number}", Args{"n": 999.9996}, "1,000"},
		{"{n, number}", Args{"n": 0.0004}, "0"},
		{"{n, select, 2 {two} other {other}}", Args{"n": int32(2)}, "two"},
		{"{n, plural, =1 {one exactly} other {#}}", Args{"n": 1.0}, "one exactly"},
		{"{n, plural, one {} other {#}}", Args{"n": 1}, ""},
		// NaN and the infinities, as intl-messageformat formats them: in
		// Intl.NumberFormat's symbols, and in a plural's other option.
		{"{n, number}", Args{"n": math.NaN()}, "NaN"},
		{"{n, number, integer}", Args{"n": math.Inf(-1)}, "-∞"},
		{"{n, plural, one {# one} other {# other}}", Args{"n": math.Inf(1)}, "∞ other"},
		{"{n, plural, offset:1 one {# one} other {# other}}", Args{"n": math.Inf(-1)}, "-∞ other"},
		{"{n, selectordinal, one {#st} other {#th}}", Args{"n": math.NaN()}, "NaNth"},
	}
	for _, tt := range tests {
		got, err := formatString(t, language.English, tt.msg, tt.args)
		if err != nil || got != tt.want {
			t.Errorf("%q with %v = %q, %v, want %q", tt.msg, tt.args, got, err, tt.want)
		}
	}
}

func TestFormatErrors(t *testing.T) {
	for _, tt := range []struct {
		msg  string
		args Args
	}{
		{"{name}", nil},
		{"{name}", Args{"other": "x"}},
		{"{name}", Args{"name": true}},
		{"{n, number}", Args{"n": "12"}},
		{"{n, plural, other {#}}", Args{"n": "1"}},
		{"{g, select, other {x}}", Args{"g": []string{"a"}}},
	} {
		if got, err := formatString(t, language.English, tt.msg, tt.args); err == nil {
			t.Errorf("%q with %v = %q, want an error", tt.msg, tt.args, got)
		}
	}
}

func TestNumber(t *testing.T) {
	en, cs := Default().Localizer(language.English), newTestBundle(t).Localizer(language.Czech)
	for _, tt := range []struct {
		l    *Localizer
		v    any
		want string
	}{
		{en, 1234.5678, "1,234.568"},
		{en, -0.5, "-0.5"},
		{en, -0.0004, "-0"},
		{en, 1e21, "1,000,000,000,000,000,000,000"},
		{en, math.NaN(), "NaN"},
		{en, math.Inf(1), "∞"},
		{en, math.Inf(-1), "-∞"},
		{en, "12", "12"},
		{cs, 1234.5678, "1" + nbsp + "234,568"},
		{cs, uint64(1) << 63, strings.Join([]string{"9", "223", "372", "036", "854", "775", "808"}, nbsp)},
	} {
		if got := tt.l.Number(tt.v); got != tt.want {
			t.Errorf("Number(%v) in %s = %q, want %q", tt.v, tt.l.Locale(), got, tt.want)
		}
	}
}
