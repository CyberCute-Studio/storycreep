package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/language"
)

// vectors is schema/i18n/vectors.json, shared with the web app's i18next. See
// schema/i18n/README.md.
type vectors struct {
	Format []struct {
		Note    string `json:"note"`
		Locale  string `json:"locale"`
		Message string `json:"message"`
		Args    Args   `json:"args"`
		Output  string `json:"output"`
	} `json:"format"`
	Invalid []struct {
		Note    string `json:"note"`
		Message string `json:"message"`
	} `json:"invalid"`
}

func readVectors(t *testing.T) vectors {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "schema", "i18n", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	// A misspelt key would otherwise leave a field empty and weaken a vector
	// without failing it.
	dec.DisallowUnknownFields()
	var v vectors
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if len(v.Format) == 0 || len(v.Invalid) == 0 {
		t.Fatal("the vectors are empty")
	}
	return v
}

// TestVectors checks that the server renders every message of the shared
// vectors as the web app's i18next does, in English and Czech, including
// their plural rules, and refuses the same invalid messages.
//
// Verifies: FR-I18N-01/AC2
func TestVectors(t *testing.T) {
	v := readVectors(t)
	locales := map[string]*locale{}
	for _, tt := range v.Format {
		t.Run(tt.Note, func(t *testing.T) {
			loc := locales[tt.Locale]
			if loc == nil {
				tag, err := language.Parse(tt.Locale)
				if err != nil {
					t.Fatal(err)
				}
				loc = newLocale(tag)
				locales[tt.Locale] = loc
			}
			nodes, err := parseMessage(tt.Message)
			if err != nil {
				t.Fatalf("parsing %q: %v", tt.Message, err)
			}
			var b strings.Builder
			if err := format(&b, loc, nodes, tt.Args, nil); err != nil {
				t.Fatalf("formatting %q: %v", tt.Message, err)
			}
			if got := b.String(); got != tt.Output {
				t.Errorf("%q with %v in %s = %q, want %q", tt.Message, tt.Args, tt.Locale, got, tt.Output)
			}
		})
	}
	for _, tt := range v.Invalid {
		t.Run(tt.Note, func(t *testing.T) {
			if _, err := parseMessage(tt.Message); err == nil {
				t.Errorf("parsing %q succeeded, want an error", tt.Message)
			}
		})
	}
}
