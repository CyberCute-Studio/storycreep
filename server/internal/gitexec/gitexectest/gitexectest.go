// Package gitexectest gives the tests of the checks that read the history of
// the main branch (packages migrationcheck and apicontract) a git repository
// of their own. Only tests import it, so it is not part of the server.
package gitexectest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Repo returns a new git repository in a temporary directory, whose branch
// is main, and a function that runs git in it, as the user Test, and fails
// the test if git fails. git, there and in the code under test, reads no
// configuration of the user's or the system's, so that the test does not
// depend on it; git config --global writes to a file of the test's own. The
// test is skipped if git is not installed.
func Repo(t *testing.T) (string, func(args ...string) string) {
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
