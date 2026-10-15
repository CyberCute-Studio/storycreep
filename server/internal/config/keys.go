package config

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
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
		name: "STORYCREEP_DEFAULT_LOCALE",
		doc: "The instance's default locale, as a BCP 47 language tag (PRD FR-I18N-02). The server writes its " +
			"messages, such as those of the API's errors, in the locale that the request's `Accept-Language` header " +
			"prefers among those that the server has a catalog for, or else in this one. A tag with a region, such " +
			"as `en-GB`, selects the catalog of that region if the server has one, and else the catalog of its " +
			"language in the same script, so `zh-TW`, which is written in Traditional Chinese, would not select " +
			"a `zh` catalog of Simplified Chinese. Only a locale that the server has a catalog for is accepted, so that a mistake is found when " +
			"the server starts.",
		valuesDoc: "A locale that the server has a catalog for: " + localeNames("`"),
		def:       constant(i18n.Source.String()),
		parse: func(c *Config, v string) error {
			tag, err := language.Parse(v)
			if err != nil {
				return errors.New("not a BCP 47 language tag, such as en")
			}
			locale, ok := i18n.Default().Match(tag)
			if !ok {
				return fmt.Errorf("the server has no catalog for that locale, only for %s", localeNames(""))
			}
			c.DefaultLocale = locale
			return nil
		},
		effective: func(c *Config) string { return c.DefaultLocale.String() },
	},
	{
		name: "STORYCREEP_DATABASE_URL",
		doc: "The PostgreSQL database, and the login the server connects to it as: " +
			"`postgres://user:password@host:port/database`, with the scheme (`postgres` or `postgresql`) in " +
			"lowercase and characters such as `@`, `:`, `/`, `?`, and `#` in the password percent-encoded. The " +
			"database server must run PostgreSQL 15 or newer. The login must be an ordinary role with a name of " +
			"its own: migrations refuse one named `storycreep_app`, `storycreep_definer`, or " +
			"`storycreep_maintenance`, the roles they create, or starting with `pg_`, which PostgreSQL reserves. " +
			"The server refuses to use one that is a superuser, has `BYPASSRLS`, `CREATEROLE`, or `REPLICATION`, " +
			"or owns the database or objects in it, or that is a member of a role that does, of one of the " +
			"server's privileged roles, or of `pg_execute_server_program`, `pg_read_server_files`, or " +
			"`pg_write_server_files`, which give access to the database server's files and programs, because " +
			"row-level security would not hold it. Migrations create the login if it does not exist, set its " +
			"password to the one the server logs in with (this URL's, or else the one `PGPASSWORD`, a service " +
			"file, or a password file supplies), and grant it the `storycreep_app` role. When the server logs in " +
			"with a password, they refuse a login whose `VALID UNTIL` has passed, which PostgreSQL would refuse as " +
			"if the password were wrong. They also revoke `CONNECT` on the database from `PUBLIC` and from " +
			"`storycreep_app`, and grant it to the login, and to the admin login if that took its `CONNECT`, so " +
			"that no other role can connect unless it is a superuser, or it is, or inherits the privileges of, the " +
			"login, the database's owner, or a role granted `CONNECT`, as a monitoring role may need to be. They " +
			"fail if `PUBLIC` or `storycreep_app` can still connect afterwards, as through a grant that a role other " +
			"than the database's owner made, and a server that does not apply migrations itself refuses a migrated " +
			"database that they can connect to again, as after a dump is restored into a new database, until " +
			"migrations run again. A login belongs to the whole PostgreSQL server, not to one database, and " +
			"so does the `storycreep_app` role, which every Storycreep database on the server grants its " +
			"privileges to. So give each Storycreep database on a server a login of its own, which can connect " +
			"only to its own database: a shared login would reach all of them, and migrating one would change the " +
			"password the others log in with. The pool parameters, such as `pool_max_conns`, configure the pool " +
			"that serves requests. A server that runs the background jobs opens a second pool of the login for " +
			"them, configured the same way but with at most 7 connections, whatever `pool_max_conns` says. Query " +
			"parameters, such as `sslmode`, are read as libpq reads them, " +
			"with an `=` in a value percent-encoded as `%3D`, and the libpq environment variables, such as " +
			"`PGSSLMODE`, fill in settings the URL leaves out. A space anywhere in the URL is written as `%20`. " +
			"The URL is checked as the server's connections read it, so a setting they could not use, such as an " +
			"unknown `sslmode`, a certificate file that cannot be read, or a libpq parameter that they do not " +
			"support, such as `gssencmode` or `keepalives`, is an error. Where the URL is shown, its " +
			"password is `" + secret.Redacted + "`, and so is the value of every query parameter except the libpq " +
			"and pgx settings that hold no secret, such as `sslmode`, and `application_name`, `search_path`, " +
			"`timezone`, and `client_encoding`.",
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
			"different login from `STORYCREEP_DATABASE_URL`, and the same database. Migrations refuse a login " +
			"that is, or acts as, `storycreep_app`, `storycreep_definer`, or `storycreep_maintenance`, because " +
			"they make those roles unable to log in and take `SUPERUSER` away from them. Migrations create the " +
			"server's tables in the `public` schema, whatever `search_path` this URL, the login, or the database " +
			"sets. It is shown redacted in the same way.",
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
		name: "STORYCREEP_DATABASE_MAINTENANCE_URL",
		doc: "The login of the maintenance jobs, for the same database, written like `STORYCREEP_DATABASE_URL`. " +
			"The jobs that must see the rows of every workspace, or of workspaces that no longer exist, such as " +
			"hard deletion and the audit log's retention, work as the `storycreep_maintenance` role, which " +
			"bypasses row-level security. This login acts as that role, in transactions of its own, which no " +
			"request uses. It must name a login different from `STORYCREEP_DATABASE_URL` and " +
			"`STORYCREEP_DATABASE_ADMIN_URL`, and the same database. When it is set, migrations create the login " +
			"if it does not exist, set its password as they set the application login's, grant it the " +
			"`storycreep_maintenance` role, and grant it `CONNECT` on the database, and they refuse a login " +
			"named like the server's roles, or that has more privileges than `storycreep_maintenance` gives it: " +
			"one that is a superuser, has `BYPASSRLS` itself, `CREATEROLE`, or `REPLICATION`, owns the database " +
			"or objects in it, or is a member of a role that does, of `storycreep_app` or " +
			"`storycreep_definer`, or of one of the roles that give access to the database server's files and " +
			"programs. Give each Storycreep database on a server a maintenance login of its own, as for " +
			"`STORYCREEP_DATABASE_URL`. Without it, migrations leave the maintenance login alone. It is shown " +
			"redacted in the same way.",
		valuesDoc: "A `postgres://` URL",
		parse: func(c *Config, v string) error {
			d, err := ParseDatabaseURL(v)
			if err != nil {
				return err
			}
			for _, other := range []struct {
				key string
				url DatabaseURL
			}{{"STORYCREEP_DATABASE_URL", c.DatabaseURL}, {"STORYCREEP_DATABASE_ADMIN_URL", c.DatabaseAdminURL}} {
				if other.url.IsSet() && d.User() == other.url.User() {
					return hiddenValueError{fmt.Errorf("names the same login as %s; the maintenance jobs need a "+
						"login of their own, because they bypass row-level security", other.key)}
				}
			}
			if c.DatabaseURL.IsSet() && d.Database() != c.DatabaseURL.Database() {
				return hiddenValueError{errors.New("names a different database from STORYCREEP_DATABASE_URL; " +
					"the maintenance jobs work on the database the server uses, so name the same one")}
			}
			c.DatabaseMaintenanceURL = d
			return nil
		},
		effective: func(c *Config) string { return c.DatabaseMaintenanceURL.String() },
	},
	{
		name: "STORYCREEP_HTTP_ADDR",
		doc: "The address the server listens on for HTTP: `host:port`, or `:port` for every network interface. " +
			"The host is an IP address or a host name, and the port a number. It serves the API, the health " +
			"endpoints `/healthz`, `/readyz`, and `/version`, and everything else except `/metrics`. Behind a " +
			"reverse proxy, the proxy forwards to this address, and users open `STORYCREEP_PUBLIC_URL`.",
		valuesDoc: "`host:port` or `:port`",
		def:       constant(":8080"),
		parse: func(c *Config, v string) error {
			if err := checkListenAddr(v); err != nil {
				return err
			}
			c.HTTPAddr = v
			return nil
		},
	},
	{
		name: "STORYCREEP_ADMIN_ADDR",
		doc: "The address of the admin port, which serves only `/metrics`, in Prometheus's text format, written " +
			"like `STORYCREEP_HTTP_ADDR`, or `off` for no admin port. Its port must differ from the port of " +
			"`STORYCREEP_HTTP_ADDR`, unless both name a host, and different ones. The default listens on the " +
			"loopback interface only, so that only the machine itself can reach it; in a container, set it to " +
			"`:9090` to let a Prometheus on the container's network scrape it. How `/metrics` answers depends on the " +
			"`MetricsEndpoint` [capability](#capabilities): `unauthenticated` serves it to anyone who can reach " +
			"the port, and `protected` only to a request with `STORYCREEP_METRICS_TOKEN` as its bearer token. " +
			"With `protected` and no token set, there are no metrics, and the admin port is not opened.",
		valuesDoc: "`host:port`, `:port`, or `off`",
		def:       constant("127.0.0.1:9090"),
		parse: func(c *Config, v string) error {
			if v == "off" {
				return nil
			}
			if err := checkListenAddr(v); err != nil {
				if errors.Is(err, errListenForm) {
					return errors.New("must be host:port, :port for every network interface, or off for no admin " +
						"port, with an IPv6 address in brackets")
				}
				return err
			}
			// An invalid STORYCREEP_HTTP_ADDR leaves HTTPAddr empty, and is
			// reported on its own.
			if c.HTTPAddr != "" && listenersOverlap(v, c.HTTPAddr) {
				return errors.New("listens on the port of STORYCREEP_HTTP_ADDR, on the same network interface or on " +
					"every one; the admin port needs an address of its own, such as another port, or off")
			}
			c.AdminAddr = v
			return nil
		},
	},
	{
		name: "STORYCREEP_METRICS_TOKEN",
		doc: "The bearer token that a scraper sends to `/metrics` on the admin port, in an " +
			"`Authorization: Bearer` header, when the `MetricsEndpoint` capability is `protected`. Setting it " +
			"also makes the capability `protected` in self-hosted mode. It is at least 16 characters of " +
			"letters, digits, and `-._~+/`, optionally followed by `=` characters, as RFC 6750 allows; " +
			"`openssl rand -hex 32` makes one. Hosted mode protects `/metrics` whether or not it is set, so " +
			"without it, hosted mode serves no metrics.",
		valuesDoc: "A token of at least 16 characters",
		secret:    true,
		parse: func(c *Config, v string) error {
			if err := checkBearerToken(v); err != nil {
				return err
			}
			c.MetricsToken = secret.New(v)
			c.Capabilities.MetricsEndpoint = MetricsProtected
			return nil
		},
		capability:      "MetricsEndpoint",
		capabilityLimit: "`protected` when set",
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

// localeNames lists the locales that the server has a catalog for, each
// between quote, separated by commas.
func localeNames(quote string) string {
	var names []string
	for _, tag := range i18n.Default().Locales() {
		names = append(names, quote+tag.String()+quote)
	}
	return strings.Join(names, ", ")
}
