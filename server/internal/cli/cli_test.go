package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

const (
	testURL         = "https://storycreep.example.com"
	testDatabaseURL = "postgres://storycreep:pw-s3cr3t@db:5432/storycreep"
)

// TestMain keeps the libpq environment variables and the user's password
// file, which the configuration reads the database URLs with, out of the
// tests, so that a developer's PGSERVICE, for one, does not make the
// configurations here invalid.
func TestMain(m *testing.M) {
	dbtest.IsolateLibpqEnvironment()
	os.Exit(m.Run())
}

func run(t *testing.T, env map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, &out, &errOut, config.MapEnvironment(env))
	return code, out.String(), errOut.String()
}

func validEnv() map[string]string {
	return map[string]string{"STORYCREEP_PUBLIC_URL": testURL, "STORYCREEP_DATABASE_URL": testDatabaseURL}
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

// TestCommandHelp checks that every subcommand has --help, which describes
// the command and its flags.
//
// Verifies: FR-ADMIN-06/AC1
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

	// help is listed as a command too, so it has its own help.
	for _, args := range [][]string{{"help", "--help"}, {"help", "-h"}, {"help", "help"}} {
		code, stdout, stderr := run(t, nil, args...)
		if code != ExitOK || stderr != "" || !strings.HasPrefix(stdout, "Usage: storycreep help [command]\n") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q; want %d and the help of help", args, code, stdout, stderr, ExitOK)
		}
	}
}

// TestUsageErrors checks that a wrong command line exits with ExitUsage and
// says what is wrong.
//
// Verifies: FR-ADMIN-06/AC1
func TestUsageErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"nope"}, `storycreep: unknown command "nope"`},
		{[]string{"--config", "x"}, "storycreep: unknown flag --config; flags go after the command"},
		{[]string{"validate-config", "--nope"}, "storycreep validate-config: flag provided but not defined: -nope"},
		{[]string{"validate-config", "--config"}, "storycreep validate-config: flag needs an argument: -config"},
		{[]string{"validate-config", "--config", ""}, "storycreep validate-config: the -config path is empty"},
		{[]string{"validate-config", "--config="}, "storycreep validate-config: the -config path is empty"},
		{[]string{"validate-config", "extra"}, `storycreep validate-config: unexpected argument "extra"`},
		{[]string{"help", "nope"}, `storycreep help: unknown command "nope"`},
		{[]string{"help", "a", "b"}, "storycreep help: expected at most one command name"},
		{[]string{"help", "--nope"}, "storycreep help: unknown flag --nope"},
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

// TestInvalidConfigStopsCommands checks that a command that needs the
// configuration does not run if a key is missing or invalid; it exits with
// ExitFailure and names every offending key.
//
// Verifies: FR-ADMIN-01/AC1
func TestInvalidConfigStopsCommands(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		keys []string
	}{
		{"missing required key", map[string]string{"STORYCREEP_DATABASE_URL": testDatabaseURL}, []string{"STORYCREEP_PUBLIC_URL"}},
		{"invalid key", map[string]string{"STORYCREEP_PUBLIC_URL": testURL, "STORYCREEP_DATABASE_URL": testDatabaseURL,
			"STORYCREEP_MODE": "staging"}, []string{"STORYCREEP_MODE"}},
		{"several", map[string]string{"STORYCREEP_SIGNUP": "public"},
			[]string{"STORYCREEP_PUBLIC_URL", "STORYCREEP_SIGNUP", "STORYCREEP_DATABASE_URL"}},
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

	// A misspelt variable explains a missing key, so its warning is shown
	// with the problems.
	code, _, stderr := run(t, map[string]string{"STORYCREEP_PUBLIC_ULR": testURL, "STORYCREEP_DATABASE_URL": testDatabaseURL}, "fake")
	if code != ExitFailure || !strings.Contains(stderr, "warning: STORYCREEP_PUBLIC_ULR is set in the environment") {
		t.Errorf("exit %d, stderr %q; want %d and a warning about STORYCREEP_PUBLIC_ULR", code, stderr, ExitFailure)
	}
}

// TestFrame covers the parts of the frame that later commands use: their own
// flags, positional arguments, and the exit status of a returned error.
func TestFrame(t *testing.T) {
	saved := commands
	t.Cleanup(func() { commands = saved })
	var got []string
	commands = []*command{{
		name:    "greet",
		summary: "Greet someone",
		args:    "<name>",
		help:    "Greets someone.",
		flags: func(fs *flag.FlagSet) {
			fs.Bool("loud", false, "greet loudly")
		},
		run: func(ctx context.Context, inv *invocation) error {
			got = inv.args
			switch {
			case len(inv.args) != 1:
				return usageError{"expected one name"}
			case inv.args[0] == "nobody":
				return errors.New("there is nobody to greet")
			}
			return nil
		},
	}}

	tests := []struct {
		args   []string
		code   int
		stderr string
	}{
		{[]string{"greet", "--loud", "ada"}, ExitOK, ""},
		{[]string{"greet"}, ExitUsage, "storycreep greet: expected one name\nRun 'storycreep greet --help' for usage.\n"},
		{[]string{"greet", "nobody"}, ExitFailure, "storycreep greet: there is nobody to greet\n"},
	}
	for _, tt := range tests {
		code, _, stderr := run(t, nil, tt.args...)
		if code != tt.code || stderr != tt.stderr {
			t.Errorf("%v: exit %d, stderr %q; want %d and %q", tt.args, code, stderr, tt.code, tt.stderr)
		}
	}
	if !slices.Equal(got, []string{"nobody"}) {
		t.Errorf("the command ran with %q, want the last arguments", got)
	}

	code, stdout, _ := run(t, nil, "greet", "--help")
	if code != ExitOK || !strings.HasPrefix(stdout, "Usage: storycreep greet [flags] <name>\n") || !strings.Contains(stdout, "-loud") {
		t.Errorf("help: exit %d, stdout %q; want the synopsis and the -loud flag", code, stdout)
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
		`^The configuration is valid\.\nConfiguration file: ` + regexp.QuoteMeta(file) + `\n`,
		`\nKEY  `,
		`\nSTORYCREEP_MODE +self-hosted +default\n`,
		`\nSTORYCREEP_PUBLIC_URL +` + regexp.QuoteMeta(testURL) + ` +environment\n`,
		`\nSTORYCREEP_SIGNUP +invite +` + regexp.QuoteMeta(file) + `:1\n`,
		// The password is redacted.
		`\nSTORYCREEP_DATABASE_URL +postgres://storycreep:\[REDACTED\]@db:5432/storycreep +environment\n`,
		`\nSTORYCREEP_DATABASE_ADMIN_URL +not set\n`,
		`\nCAPABILITY  `,
		`\nSignup +invite\n`,
		`\nEnforceEntitlements +off\n`,
	} {
		if !regexp.MustCompile(want).MatchString(stdout) {
			t.Errorf("stdout does not match %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "pw-s3cr3t") {
		t.Errorf("stdout shows the database password:\n%s", stdout)
	}
	if !strings.Contains(stderr, "warning: STORYCREEP_SINGUP is set in the environment but is not a configuration key") {
		t.Errorf("stderr should warn about the unknown variable: %q", stderr)
	}
}
