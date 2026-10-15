package config

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

const testURL = "https://storycreep.example.com"

func mustLoad(t *testing.T, env map[string]string) *Config {
	t.Helper()
	c, err := Load(Options{Env: MapEnvironment(env)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

// loadProblems loads a configuration that must be invalid and returns its
// problems.
func loadProblems(t *testing.T, opts Options) []Problem {
	t.Helper()
	c, err := Load(opts)
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("Load returned %v, %v; want an *Error", c, err)
	}
	if c != nil {
		t.Errorf("Load returned a configuration together with an error")
	}
	return cerr.Problems
}

func problemKeys(problems []Problem) []string {
	var keys []string
	for _, p := range problems {
		keys = append(keys, p.Key)
	}
	return keys
}

func entry(t *testing.T, c *Config, key string) Entry {
	t.Helper()
	for _, e := range c.Entries() {
		if e.Key == key {
			return e
		}
	}
	t.Fatalf("no entry for %s", key)
	return Entry{}
}

func TestDefaults(t *testing.T) {
	c := mustLoad(t, map[string]string{"STORYCREEP_PUBLIC_URL": testURL})
	if c.PublicURL.String() != testURL || !c.PublicURL.HTTPS() {
		t.Errorf("PublicURL = %v", c.PublicURL)
	}
	if c.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", c.LogLevel)
	}
	if c.Capabilities != modeCapabilities(modeSelfHosted) {
		t.Errorf("Capabilities = %+v, want the self-hosted defaults", c.Capabilities)
	}
	if c.File() != "" || len(c.Warnings()) != 0 {
		t.Errorf("File() = %q, Warnings() = %q; want neither", c.File(), c.Warnings())
	}
	want := []Entry{
		{"STORYCREEP_MODE", "self-hosted", SourceDefault},
		{"STORYCREEP_PUBLIC_URL", testURL, SourceEnvironment},
		{"STORYCREEP_PUBLIC_SITE", "off", SourceDefault},
		{"STORYCREEP_SIGNUP", "closed", SourceDefault},
		{"STORYCREEP_LOG_LEVEL", "info", SourceDefault},
	}
	if got := c.Entries(); !slices.Equal(got, want) {
		t.Errorf("Entries() =\n%v\nwant\n%v", got, want)
	}
}

// TestEffectiveValue checks that the entries show the public URL as the
// server uses it, not as it was written.
func TestEffectiveValue(t *testing.T) {
	c := mustLoad(t, map[string]string{"STORYCREEP_PUBLIC_URL": "HTTPS://Storycreep.Example.COM:443/"})
	if e := entry(t, c, "STORYCREEP_PUBLIC_URL"); e.Value != testURL {
		t.Errorf("entry value = %q, want %q", e.Value, testURL)
	}
}

// TestCapabilities checks the table of PRD §10.3 and how the keys refine it.
func TestCapabilities(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want Capabilities
	}{
		{
			name: "self-hosted",
			env:  map[string]string{"STORYCREEP_MODE": "self-hosted"},
			want: Capabilities{
				PublicSite: false, Signup: SignupClosed, EnforceEntitlements: false, Billing: false,
				EmailRequired: false, BlobDriver: BlobLocal, MetricsEndpoint: MetricsUnauthenticated,
				InstanceAdminUI: InstanceAdminOn, SSO: SSOOff,
			},
		},
		{
			name: "hosted",
			env:  map[string]string{"STORYCREEP_MODE": "hosted"},
			want: Capabilities{
				PublicSite: true, Signup: SignupOpen, EnforceEntitlements: true, Billing: true,
				EmailRequired: true, BlobDriver: BlobS3, MetricsEndpoint: MetricsProtected,
				InstanceAdminUI: InstanceAdminOperatorOnly, SSO: SSOPlanFeature,
			},
		},
		{
			name: "self-hosted with the public site and invitations",
			env:  map[string]string{"STORYCREEP_PUBLIC_SITE": "on", "STORYCREEP_SIGNUP": "invite"},
			want: func() Capabilities {
				c := modeCapabilities(modeSelfHosted)
				c.PublicSite, c.Signup = true, SignupInvite
				return c
			}(),
		},
		{
			name: "self-hosted with open signup",
			env:  map[string]string{"STORYCREEP_SIGNUP": "open"},
			want: func() Capabilities {
				c := modeCapabilities(modeSelfHosted)
				c.Signup = SignupOpen
				return c
			}(),
		},
		{
			name: "hosted without the public site",
			env:  map[string]string{"STORYCREEP_MODE": "hosted", "STORYCREEP_PUBLIC_SITE": "off", "STORYCREEP_SIGNUP": "open"},
			want: func() Capabilities {
				c := modeCapabilities(modeHosted)
				c.PublicSite = false
				return c
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.env["STORYCREEP_PUBLIC_URL"] = testURL
			if got := mustLoad(t, tt.env).Capabilities; got != tt.want {
				t.Errorf("Capabilities =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestDescribeListsEveryCapability(t *testing.T) {
	typ := reflect.TypeFor[Capabilities]()
	got := modeCapabilities(modeHosted).Describe()
	if len(got) != typ.NumField() {
		t.Fatalf("Describe lists %d capabilities, the struct has %d fields", len(got), typ.NumField())
	}
	for i, c := range got {
		if c.Name != typ.Field(i).Name {
			t.Errorf("Describe()[%d] is %s, want %s", i, c.Name, typ.Field(i).Name)
		}
	}
}

// TestMissingRequiredKey checks that a missing required key is a problem
// that names it.
//
// Verifies: FR-ADMIN-01/AC1
func TestMissingRequiredKey(t *testing.T) {
	for _, env := range []map[string]string{{}, {"STORYCREEP_PUBLIC_URL": ""}} {
		problems := loadProblems(t, Options{Env: MapEnvironment(env)})
		if len(problems) != 1 || problems[0].Key != "STORYCREEP_PUBLIC_URL" {
			t.Fatalf("problems = %v, want one for STORYCREEP_PUBLIC_URL", problems)
		}
		if msg := problems[0].String(); !strings.Contains(msg, "required") || !strings.Contains(msg, testURL) {
			t.Errorf("message %q should say the key is required and show an example", msg)
		}
	}
}

// TestInvalidValues checks that each invalid value is a problem that names
// the key, where the value came from, and the value.
//
// Verifies: FR-ADMIN-01/AC1
func TestInvalidValues(t *testing.T) {
	tests := []struct {
		key, value, want string
	}{
		{"STORYCREEP_MODE", "staging", "must be self-hosted or hosted"},
		{"STORYCREEP_MODE", "Hosted", "must be self-hosted or hosted"},
		{"STORYCREEP_PUBLIC_URL", "storycreep.example.com", "must start with http:// or https://"},
		{"STORYCREEP_PUBLIC_SITE", "yes", "must be on or off"},
		{"STORYCREEP_SIGNUP", "public", "must be closed, invite, or open"},
		{"STORYCREEP_LOG_LEVEL", "verbose", "must be debug, info, warn, or error"},
		// Only the file trims a value, so the space is pointed out.
		{"STORYCREEP_SIGNUP", "invite ", "starts or ends with white space"},
		{"STORYCREEP_PUBLIC_URL", " " + testURL, "starts or ends with white space"},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			env := map[string]string{"STORYCREEP_PUBLIC_URL": testURL, tt.key: tt.value}
			problems := loadProblems(t, Options{Env: MapEnvironment(env)})
			if len(problems) != 1 {
				t.Fatalf("problems = %v, want one", problems)
			}
			p := problems[0]
			if p.Key != tt.key || p.Source != SourceEnvironment {
				t.Errorf("problem names %s from %s, want %s from the environment", p.Key, p.Source, tt.key)
			}
			if !strings.Contains(p.Msg, `"`+tt.value+`"`) || !strings.Contains(p.Msg, tt.want) {
				t.Errorf("message %q should quote the value and say %q", p.Msg, tt.want)
			}
		})
	}
}

func TestEveryProblemIsReported(t *testing.T) {
	env := map[string]string{
		"STORYCREEP_MODE":      "staging",
		"STORYCREEP_SIGNUP":    "public",
		"STORYCREEP_LOG_LEVEL": "verbose",
	}
	got := problemKeys(loadProblems(t, Options{Env: MapEnvironment(env)}))
	want := []string{"STORYCREEP_MODE", "STORYCREEP_PUBLIC_URL", "STORYCREEP_SIGNUP", "STORYCREEP_LOG_LEVEL"}
	if !slices.Equal(got, want) {
		t.Errorf("problems name %v, want %v", got, want)
	}
}

// TestCredentialsInPublicURL checks that a public URL that carries a
// password is rejected without repeating the password.
//
// Verifies: NFR-SEC-14
func TestCredentialsInPublicURL(t *testing.T) {
	const password = "pw-s3cr3t"
	for _, v := range []string{
		"https://admin:" + password + "@storycreep.example.com",
		"https://admin:" + password + " x@storycreep.example.com",
		"https://admin:%zz" + password + "@storycreep.example.com",
		"https://" + password + "@storycreep.example.com/path",
		"ftp://admin:" + password + "@storycreep.example.com",
		"admin:" + password + "@storycreep.example.com",
	} {
		problems := loadProblems(t, Options{Env: MapEnvironment(map[string]string{"STORYCREEP_PUBLIC_URL": v})})
		if len(problems) != 1 || problems[0].Key != "STORYCREEP_PUBLIC_URL" {
			t.Errorf("%q: problems = %v, want one for STORYCREEP_PUBLIC_URL", v, problems)
			continue
		}
		if msg := problems[0].String(); strings.Contains(msg, password) || strings.Contains(msg, "zz") {
			t.Errorf("%q: the problem repeats the password: %s", v, msg)
		}
	}
}

// TestWarningsOnError checks that an invalid configuration keeps its
// warnings, because a misspelt variable is often why a key is missing.
func TestWarningsOnError(t *testing.T) {
	_, err := Load(Options{Env: MapEnvironment(map[string]string{"STORYCREEP_PUBLIC_ULR": testURL})})
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("Load returned %v, want an *Error", err)
	}
	if len(cerr.Warnings) != 1 || !strings.HasPrefix(cerr.Warnings[0], "STORYCREEP_PUBLIC_ULR ") {
		t.Errorf("warnings = %q, want one about STORYCREEP_PUBLIC_ULR", cerr.Warnings)
	}
	if strings.Contains(cerr.Error(), "STORYCREEP_PUBLIC_ULR") {
		t.Errorf("Error() should list only the problems: %s", cerr)
	}
}

func TestHostedRules(t *testing.T) {
	tests := []struct {
		name, key string
		env       map[string]string
	}{
		{"plain HTTP", "STORYCREEP_PUBLIC_URL", map[string]string{"STORYCREEP_PUBLIC_URL": "http://storycreep.example.com"}},
		{"closed signup", "STORYCREEP_SIGNUP", map[string]string{"STORYCREEP_PUBLIC_URL": testURL, "STORYCREEP_SIGNUP": "closed"}},
		{"invite signup", "STORYCREEP_SIGNUP", map[string]string{"STORYCREEP_PUBLIC_URL": testURL, "STORYCREEP_SIGNUP": "invite"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.env["STORYCREEP_MODE"] = "hosted"
			problems := loadProblems(t, Options{Env: MapEnvironment(tt.env)})
			if len(problems) != 1 || problems[0].Key != tt.key || !strings.Contains(problems[0].Msg, "hosted mode") {
				t.Errorf("problems = %v, want one for %s explaining the hosted-mode rule", problems, tt.key)
			}
		})
	}

	// The same values are valid in self-hosted mode.
	c := mustLoad(t, map[string]string{"STORYCREEP_PUBLIC_URL": "http://192.168.1.20:8080", "STORYCREEP_SIGNUP": "closed"})
	if c.PublicURL.HTTPS() {
		t.Errorf("an http URL is reported as https")
	}
}

func TestLogLevels(t *testing.T) {
	for v, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError,
	} {
		c := mustLoad(t, map[string]string{"STORYCREEP_PUBLIC_URL": testURL, "STORYCREEP_LOG_LEVEL": v})
		if c.LogLevel != want {
			t.Errorf("STORYCREEP_LOG_LEVEL=%s gives %v, want %v", v, c.LogLevel, want)
		}
	}
}

// TestDefaultsAreValid checks that every documented default passes its own
// validation in both modes.
func TestDefaultsAreValid(t *testing.T) {
	for _, m := range []mode{modeSelfHosted, modeHosted} {
		c := &Config{mode: m, Capabilities: modeCapabilities(m)}
		for _, k := range keys {
			if k.def == nil {
				continue
			}
			v := k.def(m)
			if v == "" {
				t.Errorf("%s: the default for %s mode is empty; leave def nil for a key without a default", k.name, m)
				continue
			}
			if err := k.set(c, v); err != nil {
				t.Errorf("%s: the default %q is invalid in %s mode: %v", k.name, v, m, err)
			}
		}
	}
}

func TestKeyDeclarations(t *testing.T) {
	seen := map[string]bool{}
	for i, k := range keys {
		switch {
		case !strings.HasPrefix(k.name, "STORYCREEP_") || !isKeyName(k.name):
			t.Errorf("%q is not a STORYCREEP_ key name", k.name)
		case seen[k.name] || k.name == fileKey:
			t.Errorf("%s is declared twice", k.name)
		case k.doc == "" || k.parse == nil:
			t.Errorf("%s needs a doc and a parse function", k.name)
		case len(k.values) == 0 && k.valuesDoc == "":
			t.Errorf("%s needs values or valuesDoc for the reference", k.name)
		case k.required && k.def != nil:
			t.Errorf("%s is required and has a default", k.name)
		case k.required && k.example == "":
			t.Errorf("%s is required but has no example for the error message", k.name)
		case k.capability == "" && k.capabilityLimit != "":
			t.Errorf("%s has a capabilityLimit but no capability", k.name)
		}
		if _, ok := reflect.TypeFor[Capabilities]().FieldByName(k.capability); k.capability != "" && !ok {
			t.Errorf("%s changes %s, which is not a capability", k.name, k.capability)
		}
		seen[k.name] = true
		if i == 0 && k.name != "STORYCREEP_MODE" {
			t.Errorf("the first key is %s; STORYCREEP_MODE must come first, because other defaults depend on it", k.name)
		}
	}
}

func TestUnknownVariables(t *testing.T) {
	vars := map[string]string{
		"STORYCREEP_PUBLIC_URL":  testURL,
		"STORYCREEP_SINGUP":      "open",
		"STORYCREEP_TOKEN":       "scp_x",
		"storycreep_log_level":   "debug",
		"STORYCREEP_CONFIG_FILE": "",
		"PATH":                   "/usr/bin",
	}
	env := MapEnvironment(vars)
	c, err := Load(Options{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	got := c.Warnings()
	if len(got) != 3 {
		t.Fatalf("warnings = %q, want three", got)
	}
	for i, want := range []string{"STORYCREEP_SINGUP", "STORYCREEP_TOKEN", "storycreep_log_level"} {
		if !strings.HasPrefix(got[i], want+" ") {
			t.Errorf("warning %d = %q, want it about %s", i, got[i], want)
		}
	}
	if !strings.Contains(got[2], "did you mean STORYCREEP_LOG_LEVEL") {
		t.Errorf("the warning about a lowercase key should suggest the right name: %q", got[2])
	}
	if c.LogLevel != slog.LevelInfo {
		t.Errorf("a lowercase key was read in a case-sensitive environment")
	}

	// Where names are case-insensitive, as in the process environment on
	// Windows, the lowercase key is read, so it is not reported.
	env.foldCase = true
	env.lookup = func(name string) (string, bool) {
		for k, v := range vars {
			if strings.EqualFold(k, name) {
				return v, true
			}
		}
		return "", false
	}
	c, err = Load(Options{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Warnings(); len(got) != 2 {
		t.Errorf("warnings = %q, want two", got)
	}
	if c.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v; the lowercase key was not read in a case-insensitive environment", c.LogLevel)
	}
}

// TestSecretKeys checks that a secret key's value is redacted in the entries
// and never repeated in a problem, even escaped.
//
// Verifies: NFR-SEC-14
func TestSecretKeys(t *testing.T) {
	// The quote and the backslash make %q escape the value, so that an echo
	// of it does not contain it verbatim; the tail is in every form.
	const value, tail = `s3cr3t"val\ue-that-must-not-leak`, "that-must-not-leak"
	var got secret.Value
	testKeys := []key{{
		name:   "STORYCREEP_TEST_PASSWORD",
		secret: true,
		parse: func(c *Config, v string) error {
			switch {
			case strings.HasPrefix(v, "weak"):
				return hiddenValueError{errors.New("too weak")}
			case strings.HasPrefix(v, "short"):
				return errors.New("too short")
			case strings.HasPrefix(v, "echo"):
				// A parse function that breaks the rule, as a wrapped
				// error such as url.Parse's would.
				return fmt.Errorf("cannot parse %q", v)
			}
			got = secret.New(v)
			return nil
		},
	}}

	c, err := load(testKeys, Options{Env: MapEnvironment(map[string]string{"STORYCREEP_TEST_PASSWORD": value})})
	if err != nil {
		t.Fatal(err)
	}
	if got.Reveal() != value {
		t.Errorf("the secret was not stored")
	}
	if e := c.Entries()[0]; e.Value != secret.Redacted {
		t.Errorf("entry value = %q, want %q", e.Value, secret.Redacted)
	}

	// Only an explanation that parse vouches for is shown.
	for _, tt := range []struct{ value, reason string }{
		{"weak" + value, "too weak"},
		{"short" + value, "the reason is not shown"},
		{"echo" + value, "the reason is not shown"},
	} {
		_, err = load(testKeys, Options{Env: MapEnvironment(map[string]string{"STORYCREEP_TEST_PASSWORD": tt.value})})
		if err == nil || strings.Contains(err.Error(), tail) || !strings.Contains(err.Error(), "STORYCREEP_TEST_PASSWORD") ||
			!strings.Contains(err.Error(), tt.reason) {
			t.Errorf("error %v should name the key and say %q without the value", err, tt.reason)
		}
	}
}
