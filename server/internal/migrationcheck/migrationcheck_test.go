package migrationcheck

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func files(kv ...string) fstest.MapFS {
	m := fstest.MapFS{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = &fstest.MapFile{Data: []byte(kv[i+1])}
	}
	return m
}

func names(problems []Problem) []string {
	var ns []string
	for _, p := range problems {
		ns = append(ns, p.Name)
	}
	return ns
}

// TestCompare checks the rule on fixtures: a change may add migrations, and
// fails if it edits, deletes, or renames one that is on the base.
//
// Verifies: FR-ADMIN-04/AC2
func TestCompare(t *testing.T) {
	base := files("00001_a.sql", "-- +goose Up\nSELECT 1;\n", "00002_b.sql", "-- +goose Up\nSELECT 2;\n",
		"README.md", "notes")
	tests := []struct {
		name string
		head fstest.MapFS
		want []string
		msg  string
	}{
		{"unchanged", files("00001_a.sql", "-- +goose Up\nSELECT 1;\n", "00002_b.sql", "-- +goose Up\nSELECT 2;\n"), nil, ""},
		{"added", files("00001_a.sql", "-- +goose Up\nSELECT 1;\n", "00002_b.sql", "-- +goose Up\nSELECT 2;\n",
			"00003_c.sql", "-- +goose Up\nSELECT 3;\n"), nil, ""},
		{"edited", files("00001_a.sql", "-- +goose Up\nSELECT 1;\n", "00002_b.sql", "-- +goose Up\nSELECT 22;\n"),
			[]string{"00002_b.sql"}, "was changed"},
		{"line endings changed", files("00001_a.sql", "-- +goose Up\r\nSELECT 1;\r\n",
			"00002_b.sql", "-- +goose Up\nSELECT 2;\n"), []string{"00001_a.sql"}, "was changed"},
		{"deleted", files("00002_b.sql", "-- +goose Up\nSELECT 2;\n"), []string{"00001_a.sql"}, "deleted or renamed"},
		{"renamed", files("00001_a_renamed.sql", "-- +goose Up\nSELECT 1;\n", "00002_b.sql", "-- +goose Up\nSELECT 2;\n"),
			[]string{"00001_a.sql"}, "deleted or renamed"},
	}
	for _, tt := range tests {
		problems, err := Compare(base, tt.head)
		if err != nil {
			t.Fatal(err)
		}
		if got := names(problems); !slices.Equal(got, tt.want) {
			t.Errorf("%s: problems for %v, want %v", tt.name, got, tt.want)
			continue
		}
		for _, p := range problems {
			if !strings.Contains(p.String(), tt.msg) {
				t.Errorf("%s: %q should say %q", tt.name, p, tt.msg)
			}
		}
	}
}

// gitRepo returns a new git repository in a temporary directory, and a
// function that runs git in it. git reads no configuration of the user's or
// the system's, so that the test does not depend on it.
func gitRepo(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com"},
			args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q", "-b", "main")
	return dir, run
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCheckEditedMigration runs the check against a git repository whose
// branch edits a migration that is on main, as CI runs it against a pull
// request, and checks that it fails.
//
// Verifies: FR-ADMIN-04/AC2
func TestCheckEditedMigration(t *testing.T) {
	repo, git := gitRepo(t)
	dir := filepath.Join(repo, "server", "migrations")
	write(t, filepath.Join(dir, "00001_roles.sql"), "-- +goose Up\nCREATE ROLE a;\n")
	git("add", ".")
	git("commit", "-q", "-m", "first migration")

	// The branch forks, and main moves on with a migration of its own,
	// which the branch does not have yet.
	git("switch", "-q", "-c", "feature")
	git("switch", "-q", "main")
	write(t, filepath.Join(dir, "00002_on_main.sql"), "-- +goose Up\nSELECT 2;\n")
	git("add", ".")
	git("commit", "-q", "-m", "second migration")
	git("switch", "-q", "feature")

	ctx := context.Background()
	res, err := Check(ctx, dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if res.Checked != 1 || len(res.Problems) != 0 {
		t.Errorf("a branch that only lacks main's newer migration: %+v, want one migration checked and no problem", res)
	}

	// Adding a migration is allowed.
	write(t, filepath.Join(dir, "00002_feature.sql"), "-- +goose Up\nSELECT 3;\n")
	if res, err := Check(ctx, dir, "main"); err != nil || len(res.Problems) != 0 {
		t.Errorf("a new migration: %+v, %v; want no problem", res, err)
	}

	// Editing the merged one fails, committed or not.
	write(t, filepath.Join(dir, "00001_roles.sql"), "-- +goose Up\nCREATE ROLE b;\n")
	res, err = Check(ctx, dir, "main")
	if err != nil || len(res.Problems) != 1 || res.Problems[0].Name != "00001_roles.sql" {
		t.Errorf("an edited migration: %+v, %v; want a problem for 00001_roles.sql", res, err)
	}
	git("commit", "-q", "-am", "edit the merged migration")
	if res, err := Check(ctx, dir, "main"); err != nil || len(res.Problems) != 1 {
		t.Errorf("a committed edit: %+v, %v; want a problem", res, err)
	}

	// So does deleting it.
	git("rm", "-q", filepath.Join("server", "migrations", "00001_roles.sql"))
	if res, err := Check(ctx, dir, "main"); err != nil || len(res.Problems) != 1 ||
		!strings.Contains(res.Problems[0].Msg, "deleted") {
		t.Errorf("a deleted migration: %+v, %v; want a problem", res, err)
	}

	// Deleting every migration deletes the directory, because git keeps no
	// empty one, and the check says to restore them.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(ctx, dir, "main"); err == nil || !strings.Contains(err.Error(), "restore") {
		t.Errorf("without the migrations directory: %v, want an error saying to restore the migrations", err)
	}
}

func TestCheckBeforeTheFirstMigration(t *testing.T) {
	repo, git := gitRepo(t)
	write(t, filepath.Join(repo, "README.md"), "x")
	git("add", ".")
	git("commit", "-q", "-m", "no migrations yet")
	git("switch", "-q", "-c", "feature")
	dir := filepath.Join(repo, "server", "migrations")
	write(t, filepath.Join(dir, "00001_roles.sql"), "-- +goose Up\nSELECT 1;\n")

	res, err := Check(context.Background(), dir, "main")
	if err != nil || res.Checked != 0 || len(res.Problems) != 0 {
		t.Errorf("Check = %+v, %v; want nothing to check", res, err)
	}
	if _, err := Check(context.Background(), dir, "no-such-branch"); err == nil {
		t.Errorf("Check against a missing branch succeeded")
	}
}
