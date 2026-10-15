package gitexec

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRun checks that Run passes its input to git and returns its output,
// and that an error says what git ran; and that ForkPoint's error says how to
// fetch the history it needs. The tests of migrationcheck and apicontract run
// both against real repositories.
func TestRun(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
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
