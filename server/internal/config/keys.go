package config

import (
	"errors"
	"log/slog"
)

// fileKey names the configuration file. It is read from the environment only,
// before the file, so it is not one of the keys below.
const fileKey = "STORYCREEP_CONFIG_FILE"

// key declares one configuration key. The declarations below are the single
// source for validation, defaults, and the generated reference.
type key struct {
	name string
	// doc describes the key in the reference, in Markdown.
	doc string
	// values lists the accepted values of an enumerated key. Any other value
	// is rejected before parse is called, and the reference lists them.
	values []string
	// valuesDoc describes the accepted values of a key that is not
	// enumerated, in Markdown.
	valuesDoc string
	// example is a valid value, shown when a required key is missing.
	example  string
	required bool
	// secret keys are redacted wherever the configuration is displayed, and
	// their values are never repeated in an error.
	secret bool
	// def returns the default for a mode, or "" if there is none. A key whose
	// default depends on the mode must come after STORYCREEP_MODE.
	def func(m mode) string
	// parse stores a value in c, or explains why it is invalid. The value is
	// never empty. The explanation must not repeat the value: Load adds it
	// for keys that are not secret.
	parse func(c *Config, v string) error
}

func constant(v string) func(mode) string {
	return func(mode) string { return v }
}

// mode is the deployment mode, STORYCREEP_MODE. It is unexported, like its
// values, so that no code outside this package can branch on it: what differs
// between the modes is in Capabilities.
type mode string

const (
	modeSelfHosted mode = "self-hosted"
	modeHosted     mode = "hosted"
)

var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// keys are the configuration keys, in the order of the reference.
var keys = []key{
	{
		name: "STORYCREEP_MODE",
		doc: "The deployment mode: `self-hosted` for an instance run by its own operator, `hosted` for the " +
			"operated multi-tenant service. It decides the [capabilities](#capabilities), and no other code reads it.",
		values: []string{string(modeSelfHosted), string(modeHosted)},
		def:    constant(string(modeSelfHosted)),
		parse: func(c *Config, v string) error {
			c.mode = mode(v)
			c.Capabilities = modeCapabilities(c.mode)
			return nil
		},
	},
	{
		name: "STORYCREEP_PUBLIC_URL",
		doc: "The URL users open Storycreep at: `http` or `https`, a host name, and an optional port, with no path. " +
			"Behind a reverse proxy that terminates TLS, it is the proxy's `https` URL, not the address the server " +
			"listens on. Hosted mode requires `https`.",
		valuesDoc: "An `http` or `https` URL",
		example:   "https://storycreep.example.com",
		required:  true,
		parse: func(c *Config, v string) error {
			u, err := parsePublicURL(v)
			if err != nil {
				return err
			}
			if c.mode == modeHosted && !u.HTTPS() {
				return errors.New("hosted mode requires an https URL")
			}
			c.PublicURL = u
			return nil
		},
	},
	{
		name:   "STORYCREEP_PUBLIC_SITE",
		doc:    "Whether the instance serves the public site: the marketing pages and the documentation.",
		values: []string{"on", "off"},
		def:    func(m mode) string { return onOff(modeCapabilities(m).PublicSite) },
		parse: func(c *Config, v string) error {
			c.Capabilities.PublicSite = v == "on"
			return nil
		},
	},
	{
		name: "STORYCREEP_SIGNUP",
		doc: "Who can register an account: `closed` (no one), `invite` (people holding an invitation), or " +
			"`open` (anyone). Hosted mode always uses `open`, and another value is an error there.",
		values: []string{string(SignupClosed), string(SignupInvite), string(SignupOpen)},
		def:    func(m mode) string { return string(modeCapabilities(m).Signup) },
		parse: func(c *Config, v string) error {
			if c.mode == modeHosted && SignupPolicy(v) != SignupOpen {
				return errors.New("hosted mode always uses open signup; remove the setting or set it to open")
			}
			c.Capabilities.Signup = SignupPolicy(v)
			return nil
		},
	},
	{
		name:   "STORYCREEP_LOG_LEVEL",
		doc:    "The lowest level that is logged.",
		values: []string{"debug", "info", "warn", "error"},
		def:    constant("info"),
		parse: func(c *Config, v string) error {
			c.LogLevel = logLevels[v]
			return nil
		},
	},
}
