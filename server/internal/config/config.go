// Package config reads, validates, and describes the server's configuration
// (PRD FR-ADMIN-01).
//
// Configuration comes from STORYCREEP_* environment variables and an optional
// file of KEY=value lines (file.go). Every key is declared once, in keys.go,
// with its default and its validation, and docs/configuration.md is generated
// from those declarations (reference.go). Load reports every invalid or
// missing key at once, each by name.
//
// This package is the only one that reads the environment, and the only one
// that knows the deployment mode. The mode resolves into Capabilities (PRD
// §10.3), which is what mode-dependent code reads; a test enforces both rules
// (FR-ADMIN-01 AC3).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// Config is a validated configuration.
type Config struct {
	// PublicURL is the URL users open the instance at.
	PublicURL PublicURL
	// DatabaseURL is the database and the application's login.
	DatabaseURL DatabaseURL
	// DatabaseAdminURL is the superuser login that applies migrations. It is
	// unset if the configuration has none.
	DatabaseAdminURL DatabaseURL
	// DatabaseMaintenanceURL is the login of the maintenance jobs, which
	// bypass row-level security (PRD §5.4). It is unset if the configuration
	// has none.
	DatabaseMaintenanceURL DatabaseURL
	// HTTPAddr is the address the server listens on for HTTP.
	HTTPAddr string
	// TrustedProxies are the reverse proxies in front of the server, whose
	// X-Forwarded-For header names a request's client, or none.
	TrustedProxies []netip.Prefix
	// AdminAddr is the address of the admin port, which serves /metrics, or
	// "" if there is none.
	AdminAddr string
	// MetricsToken is the bearer token that /metrics requires when the
	// MetricsEndpoint capability is protected. It is unset if the
	// configuration has none.
	MetricsToken secret.Value
	// LogLevel is the lowest level that is logged.
	LogLevel slog.Level
	// DefaultLocale is the instance's default locale, which has a catalog.
	DefaultLocale language.Tag
	// SessionIdleTimeout is how long a session lasts unused, and
	// SessionLifetime how long it lasts at most (PRD FR-AUTH-02).
	SessionIdleTimeout, SessionLifetime time.Duration
	// Capabilities are the mode-dependent behaviours.
	Capabilities Capabilities
	// Setup is the first-run setup that the server completes unattended,
	// from the STORYCREEP_SETUP_ keys, or nil if the configuration has none
	// (PRD FR-ADMIN-02).
	Setup *UnattendedSetup
	// SMTP is the SMTP server that the instance sends its email through,
	// from STORYCREEP_SMTP_URL and STORYCREEP_SMTP_FROM, which overrides the
	// one that first-run setup chose, or nil if the configuration sets none
	// (PRD FR-AUTH-03).
	SMTP *SMTP

	mode     mode
	file     string
	entries  []Entry
	warnings []string
}

// UnattendedSetup is what unattended first-run setup completes setup with
// (PRD FR-ADMIN-02), as the configuration holds it. The configuration checks
// only that the keys it needs are set; package setup checks the values, as
// the setup API checks them, when the server starts, and for storycreep
// validate-config (setup.CheckUnattended).
type UnattendedSetup struct {
	// OwnerEmail, OwnerName, and OwnerPassword are the first owner's email
	// address, display name, and password.
	OwnerEmail, OwnerName string
	OwnerPassword         secret.Value
	// WorkspaceName is the first workspace's name, and WorkspaceSlug its
	// slug, or "" to derive one from the name.
	WorkspaceName, WorkspaceSlug string
	// Signup is the signup policy that setup records, or "" for closed.
	Signup SignupPolicy
	// SMTP is the SMTP server that setup records, or nil for none.
	SMTP *SMTP
}

// unattendedSetup returns c.Setup, which it creates first if it is nil.
func (c *Config) unattendedSetup() *UnattendedSetup {
	if c.Setup == nil {
		c.Setup = &UnattendedSetup{}
	}
	return c.Setup
}

// smtp returns c.SMTP, which it creates first if it is nil.
func (c *Config) smtp() *SMTP {
	if c.SMTP == nil {
		c.SMTP = &SMTP{}
	}
	return c.SMTP
}

// Entry is one key's effective value, for display. The value of a secret key
// is secret.Redacted.
type Entry struct {
	Key    string
	Value  string
	Source string
}

// The sources an Entry or a Problem names, besides a file location
// ("path:line").
const (
	SourceEnvironment = "environment"
	SourceDefault     = "default"
	SourceUnset       = "not set"
)

// Entries returns every key's effective value and where it came from, in the
// order of the reference.
func (c *Config) Entries() []Entry {
	return slices.Clone(c.entries)
}

// File returns the path of the configuration file that was read, or "" if
// there was none.
func (c *Config) File() string {
	return c.file
}

// Warnings describes settings that are valid but probably not what the
// operator meant, such as an environment variable with the STORYCREEP_ prefix
// that is not a configuration key.
func (c *Config) Warnings() []string {
	return slices.Clone(c.warnings)
}

// Options says where Load reads the configuration from.
type Options struct {
	// File is the path of the configuration file, as given with --config. If
	// it is empty, the path in STORYCREEP_CONFIG_FILE is used, if any.
	File string
	// Env is the environment that the STORYCREEP_* keys are read from. The
	// zero value is the process environment. The libpq variables, such as
	// PGPASSWORD and PGSSLMODE, that fill in what a database URL leaves out
	// are read from the process environment whatever Env is: pgx reads them,
	// and it takes no other environment.
	Env Environment
}

// Environment is a set of environment variables.
type Environment struct {
	lookup func(string) (string, bool)
	names  func() []string
	// foldCase is set when names are case-insensitive, as in the process
	// environment on Windows.
	foldCase bool
}

// MapEnvironment returns an environment holding vars, for tests.
func MapEnvironment(vars map[string]string) Environment {
	return Environment{
		lookup: func(name string) (string, bool) {
			v, ok := vars[name]
			return v, ok
		},
		names: func() []string { return slices.Collect(maps.Keys(vars)) },
	}
}

func processEnvironment() Environment {
	return Environment{
		lookup: os.LookupEnv,
		names: func() []string {
			var names []string
			for _, kv := range os.Environ() {
				// Windows lists per-drive directories as "=C:=C:\dir".
				if name, _, _ := strings.Cut(kv, "="); name != "" {
					names = append(names, name)
				}
			}
			return names
		},
		foldCase: runtime.GOOS == "windows",
	}
}

// get returns the value of a variable. An empty value counts as unset.
func (e Environment) get(name string) string {
	v, _ := e.lookup(name)
	return v
}

// Error reports every problem in a configuration.
type Error struct {
	Problems []Problem
	// Warnings are the warnings a valid configuration would have had, as
	// Config.Warnings. They often explain a problem: a misspelt variable
	// is what leaves a required key unset.
	Warnings []string
}

func (e *Error) Error() string {
	msgs := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		msgs[i] = p.String()
	}
	return "invalid configuration: " + strings.Join(msgs, "; ")
}

// Problem is one invalid or missing setting. Key names the offending key, or
// the --config flag, and is empty only for a line of the configuration file
// that names no key. Source says where the offending value came from: a file
// location ("path:line"), SourceEnvironment, or empty for a missing key.
type Problem struct {
	Key    string
	Source string
	Msg    string
}

func (p Problem) String() string {
	switch {
	case p.Key != "" && p.Source != "":
		return fmt.Sprintf("%s (%s): %s", p.Key, p.Source, p.Msg)
	case p.Key != "":
		return p.Key + ": " + p.Msg
	case p.Source != "":
		return p.Source + ": " + p.Msg
	}
	return p.Msg
}

// Load reads and validates the configuration. If it is invalid, the error is
// an *Error listing every problem.
func Load(opts Options) (*Config, error) {
	return load(keys, opts)
}

func load(keys []key, opts Options) (*Config, error) {
	env := opts.Env
	if env.lookup == nil {
		env = processEnvironment()
	}
	c := &Config{}
	var problems []Problem

	path, pathKey := opts.File, "--config"
	if path == "" {
		path, pathKey = env.get(fileKey), fileKey
	}
	var file map[string]fileValue
	// fileUnread is set when the file was named but could not be read, so
	// that a key reported missing may in fact be set in it.
	fileUnread := false
	if path != "" {
		c.file = path
		var fileProblems []Problem
		file, fileProblems = readFile(path, pathKey, keys)
		problems = append(problems, fileProblems...)
		fileUnread = file == nil
	}

	// set holds the keys that have a value, valid or not, for the groups'
	// check.
	set := map[string]bool{}
	for _, k := range keys {
		v, source := env.get(k.name), SourceEnvironment
		if v == "" {
			if fv, ok := file[k.name]; ok {
				v, source = fv.value, fmt.Sprintf("%s:%d", path, fv.line)
			}
		}
		if v == "" && k.def != nil {
			v, source = k.def(c.mode), SourceDefault
		}
		if v == "" {
			if k.required || k.requiredIn != "" && k.requiredIn == c.mode {
				problems = append(problems, Problem{Key: k.name, Msg: k.missingMessage(fileUnread)})
			}
			c.entries = append(c.entries, Entry{Key: k.name, Source: SourceUnset})
			continue
		}
		set[k.name] = true
		if err := k.set(c, v); err != nil {
			msg := err.Error()
			var hidden hiddenValueError
			switch {
			case k.secret:
				// Only an explanation parse wrote without the value is
				// shown. Any other may repeat it, even escaped or in part,
				// as the errors url.Parse returns do.
				if _, ok := err.(hiddenValueError); !ok {
					msg = "invalid; the reason is not shown because it may contain the secret value"
				}
			case !errors.As(err, &hidden):
				msg = fmt.Sprintf("%q: %s", v, msg)
			}
			if strings.TrimSpace(v) != v {
				// Only the file trims values, and a parser's explanation
				// rarely names the space.
				msg += "; the value starts or ends with white space, which is part of the value"
			}
			problems = append(problems, Problem{Key: k.name, Source: source, Msg: msg})
			continue
		}
		switch {
		case k.secret:
			v = secret.Redacted
		case k.effective != nil:
			v = k.effective(c)
		}
		c.entries = append(c.entries, Entry{Key: k.name, Value: v, Source: source})
	}
	problems = append(problems, groupProblems(keys, set, fileUnread)...)
	problems = append(problems, needsProblems(keys, set, fileUnread)...)
	c.warnings = unknownVariables(env, keys)

	if len(problems) > 0 {
		return nil, &Error{Problems: problems, Warnings: c.warnings}
	}
	return c, nil
}

// hiddenValueError is a parse error whose explanation does not contain the
// value. Load does not repeat the value after one, because the value may hold
// a credential, such as a password in a URL, although the key is not secret.
// For a secret key, it is the only parse error whose explanation Load shows.
type hiddenValueError struct{ error }

// set validates v, which is not empty, and stores it in c.
func (k key) set(c *Config, v string) error {
	if len(k.values) > 0 && !slices.Contains(k.values, v) {
		return errors.New("must be " + orList(k.values))
	}
	return k.parse(c, v)
}

// groupProblems reports the keys that a group needs but that are not set,
// in a group of which set holds a key.
func groupProblems(keys []key, set map[string]bool, fileUnread bool) []Problem {
	used := map[string]bool{}
	for _, k := range keys {
		used[k.group] = used[k.group] || k.group != "" && set[k.name]
	}
	var problems []Problem
	for _, k := range keys {
		if k.groupRequired && used[k.group] && !set[k.name] {
			msg := fmt.Sprintf("required with the other %s keys, which need it, but not set", k.group)
			if fileUnread {
				msg += " (the configuration file, which may set it, could not be read)"
			}
			problems = append(problems, Problem{Key: k.name, Msg: msg + "; set it, or unset them"})
		}
	}
	return problems
}

// needsProblems reports the keys that a key that is set needs, but that are
// not set, once each.
func needsProblems(keys []key, set map[string]bool, fileUnread bool) []Problem {
	var problems []Problem
	reported := map[string]bool{}
	for _, k := range keys {
		for _, n := range k.needs {
			if !set[k.name] || set[n] || reported[n] {
				continue
			}
			reported[n] = true
			msg := fmt.Sprintf("required with %s, which needs it, but not set", k.name)
			if fileUnread {
				msg += " (the configuration file, which may set it, could not be read)"
			}
			problems = append(problems, Problem{Key: n, Msg: msg + "; set it, or unset " + k.name})
		}
	}
	return problems
}

func (k key) missingMessage(fileUnread bool) string {
	msg := "required, but not set"
	if !k.required {
		msg = fmt.Sprintf("required in %s mode, but not set", k.requiredIn)
	}
	if fileUnread {
		msg += " (the configuration file, which may set it, could not be read)"
	}
	if k.example != "" {
		msg += "; set it to a value such as " + k.example
	}
	return msg
}

// unknownVariables warns about environment variables that look like
// configuration keys but are not. They are warnings rather than errors
// because the environment is shared: the Godot addon's headless pull, for
// one, reads STORYCREEP_TOKEN.
func unknownVariables(env Environment, keys []key) []string {
	known := map[string]bool{fileKey: true}
	for _, k := range keys {
		known[k.name] = true
	}
	var warnings []string
	for _, name := range env.names() {
		upper := strings.ToUpper(name)
		if !strings.HasPrefix(upper, "STORYCREEP_") {
			continue
		}
		if known[name] || env.foldCase && known[upper] {
			continue
		}
		w := fmt.Sprintf("%s is set in the environment but is not a configuration key, so it is ignored", name)
		if known[upper] {
			w += fmt.Sprintf("; key names are case-sensitive, did you mean %s?", upper)
		}
		warnings = append(warnings, w)
	}
	slices.Sort(warnings)
	return warnings
}

// orList joins values as "a, b, or c".
func orList(values []string) string {
	switch len(values) {
	case 1:
		return values[0]
	case 2:
		return values[0] + " or " + values[1]
	}
	return strings.Join(values[:len(values)-1], ", ") + ", or " + values[len(values)-1]
}
