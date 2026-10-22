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
		"| `STORYCREEP_SIGNUP` | `closed`, `invite`, `open` | Not set |",
		"| `STORYCREEP_SETUP_OWNER_PASSWORD` | A password | Not set |",
		"| `Signup` | `setup` | `open` | `STORYCREEP_SIGNUP` (self-hosted only) |",
		"| `STORYCREEP_CONFIG_FILE` | A file path | Not set |",
		"| `PublicSite` | `off` | `on` | `STORYCREEP_PUBLIC_SITE` |",
		"| `BlobDriver` | `setup` | `s3` | `STORYCREEP_ASSET_STORAGE` |",
	} {
		if !strings.Contains(ref, want) {
			t.Errorf("the reference does not contain %q", want)
		}
	}
}

// TestVariables checks that Variables names every variable that the
// reference documents, each once, and STORYCREEP_CONFIG_FILE last.
func TestVariables(t *testing.T) {
	names := Variables()
	if len(names) != len(keys)+1 || names[len(names)-1] != fileKey {
		t.Fatalf("Variables() = %q, want the %d keys and %s", names, len(keys), fileKey)
	}
	ref := Reference()
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Errorf("Variables() names %s twice", name)
		}
		seen[name] = true
		if !strings.Contains(ref, "| `"+name+"` |") {
			t.Errorf("the reference has no row for %s", name)
		}
	}
}
