package tsgen

import (
	"strings"
	"testing"
)

func TestTypeName(t *testing.T) {
	for name, want := range map[string]bool{
		"Board": true, "$x": true, "_a1": true, "1a": false, "a-b": false, "": false, "string": false,
		"Record": false, "Client": false,
	} {
		if got := TypeName(name, "Client"); got != want {
			t.Errorf("TypeName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestPropertyName(t *testing.T) {
	for name, want := range map[string]string{
		"id": "id", "boards/a.json": `"boards/a.json"`, "a<b": `"a<b"`, `say "hi"`: `"say \"hi\""`,
	} {
		if got := PropertyName(name); got != want {
			t.Errorf("PropertyName(%q) = %s, want %s", name, got, want)
		}
	}
}

func TestComment(t *testing.T) {
	for _, tt := range []struct {
		text       string
		deprecated bool
		want       string
	}{
		{"", false, ""},
		{"  ", true, "  /** @deprecated */\n"},
		{"One line. */ not the end", false, "  /** One line. *\\/ not the end */\n"},
		{"First.\n\nThird.  ", true, "  /**\n   * First.\n   *\n   * Third.\n   * @deprecated\n   */\n"},
	} {
		var b strings.Builder
		Comment(&b, 1, tt.text, tt.deprecated)
		if b.String() != tt.want {
			t.Errorf("Comment(%q, %v) = %q, want %q", tt.text, tt.deprecated, b.String(), tt.want)
		}
	}
}
