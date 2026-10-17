package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "storycreep.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFile(t *testing.T) {
	path := writeFile(t, string(rune(0xFEFF))+"# Storycreep\r\n"+
		"\r\n"+
		"STORYCREEP_PUBLIC_URL=https://storycreep.example.com\r\n"+
		"  STORYCREEP_SIGNUP = \"invite\"  \r\n"+
		"STORYCREEP_PUBLIC_SITE='on'\n"+
		"   # indented comment\n"+
		"STORYCREEP_LOG_LEVEL=\n"+
		"STORYCREEP_MODE=self-hosted")
	c, err := Load(Options{File: path, Env: MapEnvironment(map[string]string{"STORYCREEP_DATABASE_URL": testDatabaseURL})})
	if err != nil {
		t.Fatal(err)
	}
	if c.File() != path {
		t.Errorf("File() = %q, want %q", c.File(), path)
	}
	want := []Entry{
		{"STORYCREEP_MODE", "self-hosted", path + ":8"},
		{"STORYCREEP_PUBLIC_URL", testURL, path + ":3"},
		{"STORYCREEP_PUBLIC_SITE", "on", path + ":5"},
		{"STORYCREEP_SIGNUP", "invite", path + ":4"},
		{"STORYCREEP_INSTANCE_ADMIN_ACCESS", "off", SourceDefault},
		{"STORYCREEP_WORKSPACE_LIMITS", "", SourceUnset},
		{"STORYCREEP_SMTP_URL", "", SourceUnset},
		{"STORYCREEP_SMTP_FROM", "", SourceUnset},
		{"STORYCREEP_SETUP_OWNER_EMAIL", "", SourceUnset},
		{"STORYCREEP_SETUP_OWNER_NAME", "", SourceUnset},
		{"STORYCREEP_SETUP_OWNER_PASSWORD", "", SourceUnset},
		{"STORYCREEP_SETUP_WORKSPACE_NAME", "", SourceUnset},
		{"STORYCREEP_SETUP_WORKSPACE_SLUG", "", SourceUnset},
		{"STORYCREEP_SETUP_SIGNUP", "", SourceUnset},
		{"STORYCREEP_SETUP_SMTP_URL", "", SourceUnset},
		{"STORYCREEP_SETUP_SMTP_FROM", "", SourceUnset},
		{"STORYCREEP_SESSION_IDLE_TIMEOUT", "720h", SourceDefault},
		{"STORYCREEP_SESSION_LIFETIME", "2160h", SourceDefault},
		{"STORYCREEP_DEFAULT_LOCALE", "en", SourceDefault},
		{"STORYCREEP_DATABASE_URL", testDatabaseShown, SourceEnvironment},
		{"STORYCREEP_DATABASE_ADMIN_URL", "", SourceUnset},
		{"STORYCREEP_DATABASE_MAINTENANCE_URL", "", SourceUnset},
		{"STORYCREEP_HTTP_ADDR", ":8080", SourceDefault},
		{"STORYCREEP_TRUSTED_PROXIES", "", SourceUnset},
		{"STORYCREEP_ADMIN_ADDR", "127.0.0.1:9090", SourceDefault},
		{"STORYCREEP_METRICS_TOKEN", "", SourceUnset},
		// An empty value counts as unset, so the default applies.
		{"STORYCREEP_LOG_LEVEL", "info", SourceDefault},
	}
	if got := c.Entries(); !slices.Equal(got, want) {
		t.Errorf("Entries() =\n%v\nwant\n%v", got, want)
	}
}

func TestFilePrecedence(t *testing.T) {
	path := writeFile(t, "STORYCREEP_PUBLIC_URL=https://from-file.example.com\nSTORYCREEP_SIGNUP=invite\n")

	// The environment wins over the file, and an empty variable is unset.
	c, err := Load(Options{File: path, Env: MapEnvironment(map[string]string{
		"STORYCREEP_PUBLIC_URL":   "https://from-env.example.com",
		"STORYCREEP_SIGNUP":       "",
		"STORYCREEP_DATABASE_URL": testDatabaseURL,
	})})
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicURL.String() != "https://from-env.example.com" || c.Capabilities.Signup != SignupInvite {
		t.Errorf("got %s and %s, want the URL from the environment and the policy from the file",
			c.PublicURL, c.Capabilities.Signup)
	}

	// STORYCREEP_CONFIG_FILE names the file, and the option wins over it.
	other := writeFile(t, "STORYCREEP_PUBLIC_URL=https://other.example.com\n")
	for _, tt := range []struct{ option, env, want string }{
		{"", path, "https://from-file.example.com"},
		{other, path, "https://other.example.com"},
	} {
		c, err := Load(Options{File: tt.option, Env: MapEnvironment(map[string]string{
			fileKey:                   tt.env,
			"STORYCREEP_DATABASE_URL": testDatabaseURL,
		})})
		if err != nil {
			t.Fatal(err)
		}
		if c.PublicURL.String() != tt.want {
			t.Errorf("option %q, %s=%q: got %s, want %s", tt.option, fileKey, tt.env, c.PublicURL, tt.want)
		}
	}
}

// TestFileProblems checks the problems in a configuration file: each names
// the key and the line, and none repeats a value.
//
// Verifies: FR-ADMIN-01/AC1
func TestFileProblems(t *testing.T) {
	// Text that might be a secret: a value, and a word that looks like a key.
	const secretish, secretWord = "hunter2-correct-horse", "hunter2horse"
	path := writeFile(t, strings.Join([]string{
		"STORYCREEP_PUBLIC_URL=https://storycreep.example.com", // 1
		"STORYCREEP_SINGUP=open",                               // 2
		"STORYCREEP_PUBLIC_URL=https://again.example.com",      // 3
		secretish,         // 4
		"=" + secretish,   // 5
		secretWord + "=x", // 6
		"STORYCREEP_PUBLIC_URL " + secretish + "=x", // 7
		"STORYCREEP_CONFIG_FILE=/etc/other.env",     // 8
		"STORYCREEP_MODE=staging",                   // 9
		"export STORYCREEP_SIGNUP=open",             // 10
		"storycreep_signup=invite",                  // 11
	}, "\n"))
	problems := loadProblems(t, Options{File: path, Env: MapEnvironment(map[string]string{"STORYCREEP_DATABASE_URL": testDatabaseURL})})

	at := func(line int) string { return fmt.Sprintf("%s:%d", path, line) }
	want := []Problem{
		{Key: "STORYCREEP_SINGUP", Source: at(2), Msg: "not a configuration key"},
		{Key: "STORYCREEP_PUBLIC_URL", Source: at(3), Msg: "set more than once; first set on line 1"},
		{Source: at(4), Msg: "expected KEY=value"},
		{Source: at(5), Msg: "expected KEY=value"},
		{Source: at(6), Msg: "not a configuration key; configuration keys start with STORYCREEP_"},
		{Source: at(7), Msg: "expected KEY=value"},
		{Key: fileKey, Source: at(8), Msg: "names the configuration file, so it cannot be set inside it"},
		{Source: at(10), Msg: "expected KEY=value"},
		{Key: "storycreep_signup", Source: at(11),
			Msg: "not a configuration key; key names are case-sensitive, did you mean STORYCREEP_SIGNUP?"},
		{Key: "STORYCREEP_MODE", Source: at(9), Msg: `"staging": must be self-hosted or hosted`},
	}
	if !slices.Equal(problems, want) {
		t.Errorf("problems =\n%v\nwant\n%v", problems, want)
	}
	for _, p := range problems {
		if strings.Contains(p.String(), secretish) || strings.Contains(p.String(), secretWord) {
			t.Errorf("problem repeats a line: %s", p)
		}
	}
}

func TestUnreadableFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.env")
	for _, tt := range []struct {
		opts Options
		key  string
	}{
		{Options{File: missing, Env: MapEnvironment(withRequired(nil))}, "--config"},
		{Options{Env: MapEnvironment(withRequired(map[string]string{fileKey: missing}))}, fileKey},
	} {
		problems := loadProblems(t, tt.opts)
		if len(problems) != 1 || problems[0].Key != tt.key || !strings.Contains(problems[0].Msg, "cannot read the configuration file") {
			t.Errorf("problems = %v, want one naming %s", problems, tt.key)
		}
	}

	// A required key may be set in the file that could not be read, and the
	// problem says so.
	problems := loadProblems(t, Options{File: missing, Env: MapEnvironment(nil)})
	if len(problems) != 3 || problems[1].Key != "STORYCREEP_PUBLIC_URL" || problems[2].Key != "STORYCREEP_DATABASE_URL" {
		t.Fatalf("problems = %v, want the unread file and the two missing keys", problems)
	}
	for _, p := range problems[1:] {
		if !strings.Contains(p.Msg, "the configuration file, which may set it, could not be read") {
			t.Errorf("problem %v should point at the unread file", p)
		}
	}
}

// TestFileEncoding checks that a file that is not UTF-8 is reported as such,
// instead of as a KEY=value error on every line.
func TestFileEncoding(t *testing.T) {
	utf16 := func(s string, bom bool) string {
		var b []byte
		if bom {
			b = append(b, 0xFF, 0xFE)
		}
		for _, r := range s {
			b = append(b, byte(r), 0)
		}
		return string(b)
	}
	line := "STORYCREEP_SIGNUP=invite\r\n"
	for _, tt := range []struct{ name, content, want string }{
		{"UTF-16 with a byte order mark", utf16(line, true), "it looks like UTF-16"},
		{"UTF-16 without a byte order mark", utf16(line, false), "it looks like UTF-16"},
		{"Latin-1", "STORYCREEP_SIGNUP=invit\xe9\n", "is not UTF-8 text"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, tt.content)
			problems := loadProblems(t, Options{File: path, Env: MapEnvironment(withRequired(nil))})
			if len(problems) != 1 || problems[0].Key != "--config" || problems[0].Source != path ||
				!strings.Contains(problems[0].Msg, tt.want) || !strings.Contains(problems[0].Msg, "Save it as UTF-8") {
				t.Errorf("problems = %v, want one saying %q", problems, tt.want)
			}
		})
	}
}
