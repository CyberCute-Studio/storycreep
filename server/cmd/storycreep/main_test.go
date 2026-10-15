package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runMainEnv makes the test binary run main instead of the tests, so that the
// tests below can start the real program as a subprocess.
const runMainEnv = "GO_WANT_STORYCREEP_MAIN"

// testDatabaseURL is a valid STORYCREEP_DATABASE_URL. Nothing connects to it.
const testDatabaseURL = "postgres://storycreep:pw@db:5432/storycreep"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// start runs the program with args and an environment holding only vars plus
// the process environment without its STORYCREEP_ variables, and returns the
// exit status and output.
func start(t *testing.T, vars map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "STORYCREEP_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, runMainEnv+"=1")
	for k, v := range vars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		t.Fatal(err)
	}
	return code, out.String(), errOut.String()
}

// TestStartWithInvalidConfig checks end to end that the binary exits
// non-zero, naming the key, when a required key is missing or a key is
// invalid.
//
// Verifies: FR-ADMIN-01/AC1
func TestStartWithInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		vars map[string]string
		key  string
	}{
		{"missing", map[string]string{"STORYCREEP_DATABASE_URL": testDatabaseURL}, "STORYCREEP_PUBLIC_URL"},
		{"invalid", map[string]string{"STORYCREEP_PUBLIC_URL": "https://example.com", "STORYCREEP_DATABASE_URL": testDatabaseURL,
			"STORYCREEP_MODE": "staging"}, "STORYCREEP_MODE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := start(t, tt.vars, "validate-config")
			if code != 1 || stdout != "" {
				t.Errorf("exit %d, stdout %q; want 1 and nothing", code, stdout)
			}
			if !strings.Contains(stderr, tt.key) {
				t.Errorf("stderr does not name %s: %q", tt.key, stderr)
			}
		})
	}
}

func TestExitStatus(t *testing.T) {
	tests := []struct {
		args []string
		code int
	}{
		{[]string{"validate-config"}, 0},
		{[]string{"--help"}, 0},
		{[]string{"validate-config", "--help"}, 0},
		{nil, 2},
		{[]string{"nope"}, 2},
	}
	for _, tt := range tests {
		code, _, stderr := start(t, map[string]string{
			"STORYCREEP_PUBLIC_URL":   "https://example.com",
			"STORYCREEP_DATABASE_URL": testDatabaseURL,
		}, tt.args...)
		if code != tt.code {
			t.Errorf("%v: exit %d, want %d; stderr %q", tt.args, code, tt.code, stderr)
		}
	}
}
