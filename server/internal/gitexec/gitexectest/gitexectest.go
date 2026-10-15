// Package gitexectest gives the tests of the checks that read the history of
// the main branch (packages migrationcheck and apicontract) a git repository
// of their own. Only tests import it, so it is not part of the server.
package gitexectest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Isolate makes git, in the test and in the code under test, depend on
// nothing of the user's or the system's. It unsets every GIT_ variable for
// the test, such as GIT_DIR and GIT_INDEX_FILE, which git exports to its
// hooks, so that a test run from a hook acts on no repository but its own,
// and GIT_CONFIG_PARAMETERS, which carries an outer git's -c options. It
// gives git no system configuration, and an empty global one of the test's
// own, which git config --global writes to.
func Isolate(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			continue
		}
		// Setenv restores the variable when the test ends.
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// Repo returns a new git repository in a temporary directory, whose branch
// is main, and a function that runs git in it, as the user Test, and fails
// the test if git fails. git, there and in the code under test, is isolated
// as Isolate says. The test is skipped if git is not installed.
func Repo(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	Isolate(t)
	dir := t.TempDir()
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
