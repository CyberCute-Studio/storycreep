package gitexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/gitexec/gitexectest"
)

// TestRun checks that Run passes its input to git and returns its output,
// and that an error says what git ran; and that ForkPoint's error says how to
// fetch the history it needs. The tests of migrationcheck and apicontract run
// both against real repositories.
func TestRun(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	gitexectest.Isolate(t)
	dir := t.TempDir()
	// git does not look for a repository above dir.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	ctx := context.Background()

	out, err := Run(ctx, dir, "abc", "hash-object", "--stdin")
	if err != nil || strings.TrimSpace(out) != "f2ba8f84ab5c1bce84a7b441cb1959cfc7093b7f" {
		t.Errorf("hash-object of abc: %q, %v", out, err)
	}
	if _, err := Run(ctx, dir, "", "rev-parse", "HEAD"); err == nil ||
		!strings.HasPrefix(err.Error(), "git rev-parse HEAD: ") {
		t.Errorf("outside a repository: %v, want an error that names the command", err)
	}
	if _, err := ForkPoint(ctx, dir, "main"); err == nil || !strings.Contains(err.Error(), "git fetch --unshallow") {
		t.Errorf("ForkPoint outside a repository: %v, want an error that says how to fetch the history", err)
	}
}

// TestRepoIgnoresHookVariables checks that a test repository, and Run in it,
// ignore the repository and index that git names to its hooks, as when the
// tests run from a pre-commit hook of git commit -a.
func TestRepoIgnoresHookVariables(t *testing.T) {
	outer := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(outer, "repo.git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(outer, "index.lock"))
	repo, git := gitexectest.Repo(t)
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "A commit")
	head := strings.TrimSpace(git("rev-parse", "HEAD"))
	if fork, err := ForkPoint(context.Background(), repo, "main"); err != nil || fork != head {
		t.Errorf("ForkPoint = %q, %v; want %s", fork, err, head)
	}
	entries, err := os.ReadDir(outer)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("git wrote %v into the directory that the hook's variables name", entries)
	}
}
