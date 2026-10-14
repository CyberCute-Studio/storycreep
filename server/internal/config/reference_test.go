package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite docs/configuration.md from the key declarations")

var referencePath = filepath.Join("..", "..", "..", "docs", "configuration.md")

// TestReference checks that docs/configuration.md documents every key with
// its current default (FR-ADMIN-01). Run it with -update to regenerate the
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
		t.Errorf("%s is out of date; run \"go test ./internal/config -run TestReference -update\" in server/", referencePath)
	}
}

func TestReferenceContents(t *testing.T) {
	ref := Reference()
	for _, k := range keys {
		if !strings.Contains(ref, "| `"+k.name+"` |") {
			t.Errorf("the reference has no row for %s", k.name)
		}
	}
	for _, want := range []string{
		"| `STORYCREEP_MODE` | `self-hosted`, `hosted` | `self-hosted` |",
		"| `STORYCREEP_PUBLIC_URL` | An `http` or `https` URL | Required |",
		"| `STORYCREEP_PUBLIC_SITE` | `on`, `off` | `off` (self-hosted), `on` (hosted) |",
		"| `STORYCREEP_SIGNUP` | `closed`, `invite`, `open` | `closed` (self-hosted), `open` (hosted) |",
		"| `STORYCREEP_CONFIG_FILE` | A file path | Not set |",
		"| `PublicSite` | `off` | `on` | `STORYCREEP_PUBLIC_SITE` |",
		"| `BlobDriver` | `local` | `s3` | — |",
	} {
		if !strings.Contains(ref, want) {
			t.Errorf("the reference does not contain %q", want)
		}
	}
}
