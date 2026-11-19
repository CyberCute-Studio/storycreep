package schemaversion_test

import (
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// update rewrites the files that the tests compare with what they generate:
// the synthetic version's schemas and its fixture, with the synthetic_schema
// build tag (go test -tags synthetic_schema ./internal/schemaversion/...
// -update).
var update = flag.Bool("update", false, "rewrite the synthetic version's schemas and fixture")

// root is the repository's root, from this package's directory.
var root = filepath.Join("..", "..", "..")

// readTree returns every file under dir, a directory from the repository's
// root, by its path from dir, with forward slashes.
func readTree(t *testing.T, dir string) schemaversion.Files {
	t.Helper()
	files := schemaversion.Files{}
	base := filepath.Join(root, filepath.FromSlash(dir))
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)], err = os.ReadFile(p)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// writeTree replaces the files under dir, a directory from the repository's
// root, with files.
func writeTree(t *testing.T, dir string, files schemaversion.Files) {
	t.Helper()
	base := filepath.Join(root, filepath.FromSlash(dir))
	if err := os.RemoveAll(base); err != nil {
		t.Fatal(err)
	}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		file := filepath.Join(base, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, files[p], 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// realVersions returns the real versions of the published format, the
// oldest first: those whose JSON Schemas /schema/published holds, in a
// directory v<N> of each.
func realVersions(t *testing.T) []int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "schema", "published"))
	if err != nil {
		t.Fatal(err)
	}
	versionDir := regexp.MustCompile(`^v([1-9][0-9]*)$`)
	var out []int
	for _, e := range entries {
		if m := versionDir.FindStringSubmatch(e.Name()); e.IsDir() && m != nil {
			n, _ := strconv.Atoi(m[1])
			out = append(out, n)
		}
	}
	slices.Sort(out)
	if len(out) == 0 {
		t.Fatal("/schema/published holds no version")
	}
	return out
}

// fixtureDir returns the directory, from the repository's root, of the
// fixture of the version n: /schema/fixtures/v<n> of a real version, and the
// synthetic version's in testdata/synthetic-schema (PRD §6.4).
func fixtureDir(t *testing.T, n int) string {
	t.Helper()
	if slices.Contains(realVersions(t), n) {
		return fmt.Sprintf("schema/fixtures/v%d", n)
	}
	return fmt.Sprintf("testdata/synthetic-schema/fixtures/v%d", n)
}

// edited returns a copy of f in which edit has changed the file at path, as
// a tree, its manifest sealed again (schemaversion.Seal), or fails the test.
func edited(t *testing.T, f schemaversion.Files, path string, edit func(map[string]any)) schemaversion.Files {
	t.Helper()
	out, err := schemaversion.Edit(f, func(p string, file map[string]any) error {
		if p == path {
			edit(file)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if out, err = schemaversion.Seal(out); err != nil {
		t.Fatal(err)
	}
	return out
}

// nodeOf returns the node id of a board's file, as a tree, or fails the test.
func nodeOf(t *testing.T, board map[string]any, id string) map[string]any {
	t.Helper()
	for _, n := range board["nodes"].([]any) {
		if n := n.(map[string]any); n["id"] == id {
			return n
		}
	}
	t.Fatalf("the board has no node %s", id)
	return nil
}
