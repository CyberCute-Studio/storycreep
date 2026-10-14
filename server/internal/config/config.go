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
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// Config is a validated configuration.
type Config struct {
	// PublicURL is the URL users open the instance at.
	PublicURL PublicURL
	// LogLevel is the lowest level that is logged.
	LogLevel slog.Level
	// Capabilities are the mode-dependent behaviours.
	Capabilities Capabilities

	mode     mode
	file     string
	entries  []Entry
	warnings []string
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
	// Env is the environment. The zero value is the process environment.
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
		names: func() []string {
			var names []string
			for name := range vars {
				names = append(names, name)
			}
			return names
		},
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
	if path != "" {
		c.file = path
		var fileProblems []Problem
		file, fileProblems = readFile(path, pathKey, keys)
		problems = append(problems, fileProblems...)
	}

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
			if k.required {
				problems = append(problems, Problem{Key: k.name, Msg: k.missingMessage()})
			}
			c.entries = append(c.entries, Entry{Key: k.name, Source: SourceUnset})
			continue
		}
		if err := k.set(c, v); err != nil {
			msg := err.Error()
			if !k.secret {
				msg = fmt.Sprintf("%q: %s", v, msg)
			}
			problems = append(problems, Problem{Key: k.name, Source: source, Msg: msg})
			continue
		}
		if k.secret {
			v = secret.Redacted
		}
		c.entries = append(c.entries, Entry{Key: k.name, Value: v, Source: source})
	}
	c.warnings = unknownVariables(env, keys)

	if len(problems) > 0 {
		return nil, &Error{Problems: problems}
	}
	return c, nil
}

// set validates v, which is not empty, and stores it in c.
func (k key) set(c *Config, v string) error {
	if len(k.values) > 0 && !slices.Contains(k.values, v) {
		return errors.New("must be " + orList(k.values))
	}
	return k.parse(c, v)
}

func (k key) missingMessage() string {
	if k.example != "" {
		return fmt.Sprintf("required, but not set; set it to a value such as %s", k.example)
	}
	return "required, but not set"
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
