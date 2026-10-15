package api

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite docs/error-codes.md from the registry")

var referencePath = filepath.Join("..", "..", "..", "docs", "error-codes.md")

// TestReference checks that docs/error-codes.md documents every error code
// with its status (PRD FR-API-04). Run it with -update to regenerate the
// file.
func TestReference(t *testing.T) {
	want := Reference()
	if *update {
		if err := os.WriteFile(referencePath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatal(err)
	}
	// A checkout made without .gitattributes may have CRLF.
	if got := strings.ReplaceAll(string(b), "\r\n", "\n"); got != want {
		t.Errorf("%s is out of date; run \"go test ./internal/api -run TestReference -update\" in server/", referencePath)
	}
}

func TestReferenceContents(t *testing.T) {
	ref := Reference()
	for _, s := range codes {
		if !strings.Contains(ref, "| `"+string(s.code)+"` |") {
			t.Errorf("the reference has no row for %s", s.code)
		}
	}
	for _, want := range []string{
		"| `validation_failed` | 422 | The request is invalid. |",
		"| `not_ready` | 503 | The server is not ready. |",
	} {
		if !strings.Contains(ref, want) {
			t.Errorf("the reference does not contain %q", want)
		}
	}
}
