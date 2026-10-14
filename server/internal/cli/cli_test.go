package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
)

const testURL = "https://storycreep.example.com"

func run(t *testing.T, env map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, &out, &errOut, config.MapEnvironment(env))
	return code, out.String(), errOut.String()
}

func validEnv() map[string]string {
	return map[string]string{"STORYCREEP_PUBLIC_URL": testURL}
}

func TestTopLevelHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-help"}, {"-h"}, {"help"}} {
		code, stdout, stderr := run(t, nil, args...)
		if code != ExitOK || stderr != "" {
			t.Errorf("%v: exit %d, stderr %q; want %d and nothing", args, code, stderr, ExitOK)
		}
		for _, want := range []string{"Usage:", "validate-config", "Exit status:"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("%v: the usage does not contain %q:\n%s", args, want, stdout)
			}
		}
	}

	code, stdout, stderr := run(t, nil)
	if code != ExitUsage || stdout != "" || !strings.Contains(stderr, "Commands:") {
		t.Errorf("no arguments: exit %d, stdout %q, stderr %q; want the usage on stderr and %d", code, stdout, stderr, ExitUsage)
	}
}

// TestCommandHelp covers FR-ADMIN-06/AC1 for --help: every subcommand
// describes itself and its flags.
func TestCommandHelp(t *testing.T) {
	for _, c := range commands {
		for _, args := range [][]string{{c.name, "--help"}, {c.name, "-h"}, {"help", c.name}} {
			code, stdout, stderr := run(t, nil, args...)
			if code != ExitOK || stderr != "" {
				t.Errorf("%v: exit %d, stderr %q; want %d and nothing", args, code, stderr, ExitOK)
			}
			if !strings.HasPrefix(stdout, "Usage: storycreep "+c.name) {
				t.Errorf("%v: help should start with the synopsis:\n%s", args, stdout)
			}
			if c.loadsConfig && !strings.Contains(stdout, "-config path") {
				t.Errorf("%v: help should list the --config flag:\n%s", args, stdout)
			}
		}
	}
}

// TestUsageErrors covers FR-ADMIN-06/AC1 for exit statuses: a wrong command
// line exits with ExitUsage and says what is wrong.
func TestUsageErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"nope"}, `storycreep: unknown command "nope"`},
		{[]string{"--config", "x"}, "storycreep: unknown flag --config; flags go after the command"},
		{[]string{"validate-config", "--nope"}, "storycreep validate-config: flag provided but not defined: -nope"},
		{[]string{"validate-config", "--config"}, "storycreep validate-config: flag needs an argument: -config"},
		{[]string{"validate-config", "extra"}, `storycreep validate-config: unexpected argument "extra"`},
		{[]string{"help", "nope"}, `storycreep help: unknown command "nope"`},
		{[]string{"help", "a", "b"}, "storycreep help: expected at most one command name"},
	}
	for _, tt := range tests {
		// The configuration is invalid too: a usage error is reported first.
		code, stdout, stderr := run(t, nil, tt.args...)
		if code != ExitUsage || stdout != "" {
			t.Errorf("%v: exit %d, stdout %q; want %d and nothing", tt.args, code, stdout, ExitUsage)
		}
		if !strings.HasPrefix(stderr, tt.want+"\n") || !strings.Contains(stderr, "--help") {
			t.Errorf("%v: stderr %q, want %q and a pointer to --help", tt.args, stderr, tt.want)
		}
	}
}

// fakeCommand replaces the commands with one that loads the configuration
// and records whether it ran.
func fakeCommand(t *testing.T) *bool {
	t.Helper()
	ran := new(bool)
	saved := commands
	t.Cleanup(func() { commands = saved })
	commands = []*command{{
		name:        "fake",
		summary:     "A command that loads the configuration",
		help:        "Loads the configuration.",
		loadsConfig: true,
		run: func(ctx context.Context, inv *invocation) error {
			*ran = inv.cfg != nil
			return nil
		},
	}}
	return ran
}

// TestInvalidConfigStopsCommands covers FR-ADMIN-01/AC1: a command that needs
// the configuration does not run if a key is missing or invalid; it exits
// with ExitFailure and names every offending key.
func TestInvalidConfigStopsCommands(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		keys []string
	}{
		{"missing required key", map[string]string{}, []string{"STORYCREEP_PUBLIC_URL"}},
		{"invalid key", map[string]string{"STORYCREEP_PUBLIC_URL": testURL, "STORYCREEP_MODE": "staging"}, []string{"STORYCREEP_MODE"}},
		{"several", map[string]string{"STORYCREEP_SIGNUP": "public"}, []string{"STORYCREEP_PUBLIC_URL", "STORYCREEP_SIGNUP"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran := fakeCommand(t)
			code, stdout, stderr := run(t, tt.env, "fake")
			if code != ExitFailure || stdout != "" || *ran {
				t.Errorf("exit %d, stdout %q, ran %v; want %d, nothing, and not run", code, stdout, *ran, ExitFailure)
			}
			for _, k := range tt.keys {
				if !strings.Contains(stderr, "\n  "+k+": ") && !strings.Contains(stderr, "\n  "+k+" (") {
					t.Errorf("stderr does not name %s on a line of its own:\n%s", k, stderr)
				}
			}
		})
	}

	ran := fakeCommand(t)
	if code, _, stderr := run(t, validEnv(), "fake"); code != ExitOK || !*ran {
		t.Errorf("with a valid configuration: exit %d, ran %v, stderr %q", code, *ran, stderr)
	}
}

func TestValidateConfig(t *testing.T) {
	file := filepath.Join(t.TempDir(), "storycreep.env")
	if err := os.WriteFile(file, []byte("STORYCREEP_SIGNUP=invite\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := validEnv()
	env["STORYCREEP_SINGUP"] = "open"
	code, stdout, stderr := run(t, env, "validate-config", "--config", file)
	if code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"The configuration is valid.\nConfiguration file: " + file + "\n",
		"\nKEY ",
		"\nSTORYCREEP_MODE         self-hosted ",
		"\nSTORYCREEP_PUBLIC_URL   " + testURL + "  environment\n",
		"\nSTORYCREEP_SIGNUP       invite ",
		file + ":1\n",
		"\nCAPABILITY ",
		"\nSignup               invite\n",
		"\nEnforceEntitlements  off\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "warning: STORYCREEP_SINGUP is set in the environment but is not a configuration key") {
		t.Errorf("stderr should warn about the unknown variable: %q", stderr)
	}
}
