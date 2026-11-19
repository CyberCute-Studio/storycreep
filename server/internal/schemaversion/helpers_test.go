package schemaversion_test

import (
	"flag"
	"fmt"
	"io/fs"
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
	flag.Parse()
	if regenerate != nil {
		if err := regenerate(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Exit(dbtest.Run(m))
}

// regenerate, which the tests of the synthetic version set with the
// synthetic_schema build tag, rewrites the files that the tests compare with
// what they generate where -update asks it to, before any test reads them, so
// that no test reads them as they were (synthetic_test.go).
var regenerate func() error

// root is the repository's root, from this package's directory.
var root = filepath.Join("..", "..", "..")

// readTree returns every file under dir, a directory from the repository's
// root, by its path from dir, with forward slashes, or fails the test.
func readTree(t *testing.T, dir string) schemaversion.Files {
	t.Helper()
	files, err := readFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// readFiles returns every file under dir, as readTree does.
func readFiles(dir string) (schemaversion.Files, error) {
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
	return files, err
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
