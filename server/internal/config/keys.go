package config

import (
	"errors"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
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
	// for keys that are not secret, unless the error is a hiddenValueError.
	// For a secret key, Load shows the explanation only if parse returns a
	// hiddenValueError, which it does when the reason helps the operator and
	// cannot contain the value.
	parse func(c *Config, v string) error
	// effective returns the value in effect, for display, when parse
	// normalizes it. Without it, the value is shown as it was set.
	effective func(c *Config) string
	// capability names the field of Capabilities that the key changes, if
	// it changes one, and capabilityLimit says when it can, such as
	// "self-hosted only". The reference's capability table shows both.
	capability, capabilityLimit string
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
		doc: "The URL users open Storycreep at: `http` or `https`, a host name or IP address, and an optional port, " +
			"with no path. Behind a reverse proxy that terminates TLS, it is the proxy's `https` URL, not the address " +
			"the server listens on. Write an internationalized domain name in its `xn--` (Punycode) form, and an IPv4 " +
			"address as four decimal numbers. Hosted mode requires `https`.",
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
		effective: func(c *Config) string { return c.PublicURL.String() },
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
		capability: "PublicSite",
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
		capability:      "Signup",
		capabilityLimit: "self-hosted only",
	},
	{
		name: "STORYCREEP_DATABASE_URL",
		doc: "The PostgreSQL database, and the login the server connects to it as: " +
			"`postgres://user:password@host:port/database`, with the scheme (`postgres` or `postgresql`) in " +
			"lowercase and characters such as `@`, `:`, `/`, `?`, and `#` in the password percent-encoded. The " +
			"login must be an ordinary role. The server refuses to use one that is a superuser, has `BYPASSRLS`, " +
			"`CREATEROLE`, or `REPLICATION`, or owns objects in the database, or that is a member of a role that " +
			"does, of one of the server's privileged roles, or of `pg_execute_server_program`, " +
			"`pg_read_server_files`, or `pg_write_server_files`, which give access to the database server's files " +
			"and programs, because row-level security would not hold it. Migrations create the login if it does " +
			"not exist, set its password to the one the server logs in with (this URL's, or else the one " +
			"`PGPASSWORD`, a service file, or a password file supplies), and grant it the `storycreep_app` role. " +
			"Query parameters, such as `sslmode`, are read as libpq reads them, with an `=` in a value " +
			"percent-encoded as `%3D`, and the libpq environment variables, such as `PGSSLMODE`, fill in settings " +
			"the URL leaves out. A space anywhere in the URL is written as `%20`. The URL is checked as the " +
			"server's connections read it, so a setting they could not use, such as an unknown `sslmode` or a " +
			"certificate file that cannot be read, is an error. Where the URL is shown, its password is `" + secret.Redacted +
			"`, and so is the value of every query parameter except the libpq and pgx settings that hold no secret, " +
			"such as `sslmode`, and `application_name`, `search_path`, `timezone`, and `client_encoding`.",
		valuesDoc: "A `postgres://` URL",
		example:   "postgres://storycreep:password@db:5432/storycreep",
		required:  true,
		parse: func(c *Config, v string) error {
			d, err := ParseDatabaseURL(v)
			if err != nil {
				return err
			}
			c.DatabaseURL = d
			return nil
		},
		effective: func(c *Config) string { return c.DatabaseURL.String() },
	},
	{
		name: "STORYCREEP_DATABASE_ADMIN_URL",
		doc: "A PostgreSQL superuser login for the same database, written like `STORYCREEP_DATABASE_URL`. Only " +
			"applying migrations uses it, as `storycreep migrate` does. Migrations need a superuser because they " +
			"create the server's database roles, and one of them bypasses row-level security. It must name a " +
			"different login from `STORYCREEP_DATABASE_URL`, and the same database. It is shown redacted in the same " +
			"way.",
		valuesDoc: "A `postgres://` URL",
		parse: func(c *Config, v string) error {
			d, err := ParseDatabaseURL(v)
			if err != nil {
				return err
			}
			if c.DatabaseURL.IsSet() && d.User() == c.DatabaseURL.User() {
				return hiddenValueError{errors.New("names the same login as STORYCREEP_DATABASE_URL; migrations " +
					"need a superuser and the server an ordinary login, so they must be different logins")}
			}
			// A superuser URL often names the postgres database, which would
			// migrate that database and leave the server's unmigrated.
			if c.DatabaseURL.IsSet() && d.Database() != c.DatabaseURL.Database() {
				return hiddenValueError{errors.New("names a different database from STORYCREEP_DATABASE_URL; " +
					"migrations must be applied to the database the server uses, so name the same one")}
			}
			c.DatabaseAdminURL = d
			return nil
		},
		effective: func(c *Config) string { return c.DatabaseAdminURL.String() },
	},
	{
		name:   "STORYCREEP_LOG_LEVEL",
		doc:    "The lowest level that is logged.",
		values: []string{"debug", "info", "warn", "error"},
		def:    constant("info"),
		// slog.Level reads the names in any case; values admits only these.
		parse: func(c *Config, v string) error {
			return c.LogLevel.UnmarshalText([]byte(v))
		},
	},
}
