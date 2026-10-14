package config

import (
	"errors"
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

// TestMissingRequiredKey covers FR-ADMIN-01/AC1 for a missing key.
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

// TestInvalidValues covers FR-ADMIN-01/AC1 for invalid values: each problem
// names the key, where the value came from, and the value.
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
		}
		seen[k.name] = true
		if i == 0 && k.name != "STORYCREEP_MODE" {
			t.Errorf("the first key is %s; STORYCREEP_MODE must come first, because other defaults depend on it", k.name)
		}
	}
}

func TestUnknownVariables(t *testing.T) {
	env := MapEnvironment(map[string]string{
		"STORYCREEP_PUBLIC_URL":  testURL,
		"STORYCREEP_SINGUP":      "open",
		"STORYCREEP_TOKEN":       "scp_x",
		"storycreep_log_level":   "debug",
		"STORYCREEP_CONFIG_FILE": "",
		"PATH":                   "/usr/bin",
	})
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

	// Where names are case-insensitive, the lowercase key is read, so it is
	// not reported.
	env.foldCase = true
	c, err = Load(Options{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Warnings(); len(got) != 2 {
		t.Errorf("warnings = %q, want two", got)
	}
}

// TestSecretKeys covers NFR-SEC-14 for configuration: a secret key's value is
// redacted in the entries and never repeated in a problem.
func TestSecretKeys(t *testing.T) {
	const value = "s3cr3t-value-that-must-not-leak"
	var got secret.Value
	testKeys := []key{{
		name:   "STORYCREEP_TEST_PASSWORD",
		secret: true,
		parse: func(c *Config, v string) error {
			if strings.HasPrefix(v, "bad") {
				return errors.New("too weak")
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

	_, err = load(testKeys, Options{Env: MapEnvironment(map[string]string{"STORYCREEP_TEST_PASSWORD": "bad" + value})})
	if err == nil || strings.Contains(err.Error(), value) || !strings.Contains(err.Error(), "STORYCREEP_TEST_PASSWORD") {
		t.Errorf("error %v should name the key without the value", err)
	}
}
