package input

import (
	"strings"
	"testing"
)

// TestCheckText checks that a text of several lines may be empty and hold
// line feeds and tabs, but no other control character, bidirectional
// control, or line separator, and no more characters than its limit, counted
// as characters rather than bytes.
func TestCheckText(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		text string
		ok   bool
	}{
		{"", true},
		{"Elara waits\n\tat the harbour.", true},
		{strings.Repeat("ž", 40), true},
		{strings.Repeat("ž", 41), false},
		{"carriage\rreturn", false},
		{"bell\a", false},
		{"nul\x00", false},
		{"reversed ‮etxet", false},
		{"line separator", false},
	} {
		if got := CheckText(c.text, 40); (got == "") != c.ok {
			t.Errorf("CheckText(%q, 40) = %q, want ok %v", c.text, got, c.ok)
		}
	}
}
