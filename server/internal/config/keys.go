package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
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
	// group names the keys that configure one thing together, such as
	// unattended setup. If any key of a group is set, every key of the group
	// that is groupRequired must be set too.
	group         string
	groupRequired bool
	// needs names the keys that must be set too when this one is, such as
	// the address that email is sent from with the server that sends it.
	needs []string
	// requiredIn is the mode that requires the key, as hosted mode, which
	// always sends email, requires an SMTP server, or "" if none does. A
	// required key is required in both.
	requiredIn mode
}

// setupGroup is the group of the keys of unattended first-run setup.
const setupGroup = "STORYCREEP_SETUP_"

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
			"`open` (anyone). Not set, self-hosted mode uses the policy that first-run setup chose (PRD " +
			"FR-ADMIN-02), or `closed` if setup chose none, which the `Signup` capability shows as `setup`; set, " +
			"it overrides that choice for as long as it is set. Until setup has completed, nobody can register, " +
			"whatever the policy. Hosted mode always uses `open`, and another value is an error there.",
		values: []string{string(SignupClosed), string(SignupInvite), string(SignupOpen)},
		// Without the key, the capability is the mode's: the policy that
		// setup chose, or open in hosted mode.
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
		name: "STORYCREEP_INSTANCE_ADMIN_ACCESS",
		doc: "Whether an instance admin acts as the owner of every workspace and project, without being a member " +
			"(PRD §5.5): `on` lets them read and manage any workspace, as when its last owner has left, and `off` " +
			"resolves their role by their memberships, like anyone else's. Hosted mode, where instance admin is " +
			"restricted to operator accounts, refuses `on`.",
		values: []string{"on", "off"},
		def:    func(m mode) string { return onOff(modeCapabilities(m).InstanceAdminAccess) },
		parse: func(c *Config, v string) error {
			if c.mode == modeHosted && v == "on" {
				return errors.New("hosted mode never lets an instance admin act as a workspace's owner; remove the " +
					"setting or set it to off")
			}
			c.Capabilities.InstanceAdminAccess = v == "on"
			return nil
		},
		capability:      "InstanceAdminAccess",
		capabilityLimit: "self-hosted only",
	},
	{
		name: "STORYCREEP_WORKSPACE_LIMITS",
		doc: "Caps on what each workspace may use (PRD FR-ENT-01). Self-hosted mode gives every workspace the " +
			"unlimited plan, so without this key no workspace has a limit. Each entry is a limit and a whole number " +
			"from 1 to 9007199254740991, joined by `=`: `members`, the most members that a workspace may have, " +
			"`projects`, the most projects, and `storage_bytes`, the most bytes that its stored files may take up. A " +
			"limit that the key does not name stays unlimited. A change that would take a workspace past a cap is " +
			"refused with 409 `limit_exceeded`, whose details name the limit, the workspace's usage, and the cap, " +
			"and which offers no upgrade. For `members`, which counts guests too, that is accepting an invitation " +
			"to a workspace that has as many members as the cap allows, `storycreep grant-role`, which then " +
			"fails, and registering with the link of a link invitation, which any address may register with; " +
			"registering with the link of an invitation to an address adds the account, which accepts the " +
			"invitation once a member has left. Inviting someone is not such a change, because it adds no member. " +
			"`projects` and `storage_bytes` limit nothing yet: each applies once the change that adds to it checks " +
			"it, creating a project and uploading a file. A workspace that is past a cap already, as after the cap " +
			"is lowered, keeps what it has, but cannot add more. Hosted mode, where each workspace's plan sets its " +
			"limits, refuses the key.",
		valuesDoc: "Limits and whole numbers joined by `=`, separated by commas, such as `members=20, projects=50`",
		parse: func(c *Config, v string) error {
			if c.mode == modeHosted {
				return errors.New("hosted mode limits each workspace by its plan; remove the setting")
			}
			caps, err := entitlements.ParseCaps(v)
			if err != nil {
				return err
			}
			c.WorkspaceLimits = caps
			return nil
		},
		effective: func(c *Config) string { return c.WorkspaceLimits.String() },
	},
	{
		name: "STORYCREEP_SMTP_URL",
		doc: "The SMTP server that the instance sends its email through (PRD FR-AUTH-03): the links that verify " +
			"an email address and reset a password. `smtp://` connects with STARTTLS, which the server must " +
			"offer, on port 587 unless the URL names another port; `smtp://` with `?security=none` sends without " +
			"TLS, for a relay on the same host or network, and then cannot sign in to it unless its host is " +
			"`localhost`, `127.0.0.1`, or `::1`; and `smtps://` uses TLS from the start, on port 465 unless the URL " +
			"names another. A username and a password in the URL, percent-encoded, sign in with PLAIN or LOGIN. With " +
			"`STORYCREEP_SMTP_FROM`, which it needs, it overrides the SMTP settings that first-run setup chose " +
			"(PRD FR-ADMIN-02), for as long as it is set. When neither sets a server, the instance sends no " +
			"email, says so to its users, and an instance admin gives them the links as text instead " +
			"(`POST /api/v1/admin/password-reset-links`, or `storycreep reset-password`). Hosted mode always " +
			"sends email (the `EmailRequired` [capability](#capabilities)), so it requires this key. Where the " +
			"URL is shown, its password is `" + secret.Redacted + "`.",
		valuesDoc:  "An `smtp://` or `smtps://` URL",
		example:    "smtp://apikey:password@smtp.example.com:587",
		needs:      []string{"STORYCREEP_SMTP_FROM"},
		requiredIn: modeHosted,
		parse: func(c *Config, v string) error {
			s, err := ParseSMTPURL(v)
			if err != nil {
				return err
			}
			s.From = c.smtp().From
			*c.SMTP = s
			return nil
		},
		effective: func(c *Config) string { return c.SMTP.URL() },
	},
	{
		name: "STORYCREEP_SMTP_FROM",
		doc: "The address that the instance's email comes from, alone or with a name, with " +
			"`STORYCREEP_SMTP_URL`, which needs it. The SMTP server must let the instance send from it.",
		valuesDoc:  "An email address, alone or with a name",
		example:    "Storycreep <noreply@example.com>",
		needs:      []string{"STORYCREEP_SMTP_URL"},
		requiredIn: modeHosted,
		parse: func(c *Config, v string) error {
			if _, msg := ParseSMTPFrom(v); msg != "" {
				return errors.New(msg)
			}
			c.smtp().From = v
			return nil
		},
	},
	{
		name: "STORYCREEP_SETUP_OWNER_EMAIL",
		doc: "The email address of the first owner, for unattended first-run setup (PRD FR-ADMIN-02). With " +
			"`STORYCREEP_SETUP_OWNER_NAME`, `STORYCREEP_SETUP_OWNER_PASSWORD`, and `STORYCREEP_SETUP_WORKSPACE_NAME`, " +
			"which it needs, it makes the server complete first-run setup itself when it starts on a database " +
			"whose setup is pending, instead of waiting for someone to complete it with the setup code that it " +
			"logs. Unattended setup makes the same changes as the setup API: it creates the owner's account, " +
			"which is an instance admin, and the first workspace, which the owner owns and which is the owner's " +
			"personal workspace, and records the signup policy of `STORYCREEP_SETUP_SIGNUP`. A value that the " +
			"setup API would refuse, such as an address that is not one or a weak password, stops the server " +
			"with an error that names its key, and `storycreep validate-config` reports it too. Once setup has " +
			"completed, either way, the server ignores these keys.",
		valuesDoc:     "An email address",
		example:       "jo@example.com",
		group:         setupGroup,
		groupRequired: true,
		parse: func(c *Config, v string) error {
			c.unattendedSetup().OwnerEmail = v
			return nil
		},
	},
	{
		name: "STORYCREEP_SETUP_OWNER_NAME",
		doc: "The display name of the first owner, for unattended first-run setup, which other people see: 1 to " +
			"100 characters, not all of them blank, and without control characters.",
		valuesDoc:     "A display name",
		example:       "Jo Novak",
		group:         setupGroup,
		groupRequired: true,
		parse: func(c *Config, v string) error {
			c.unattendedSetup().OwnerName = v
			return nil
		},
	},
	{
		name: "STORYCREEP_SETUP_OWNER_PASSWORD",
		doc: "The password of the first owner, for unattended first-run setup: at least 12 characters, and not " +
			"one of the 10,000 most common passwords. The server reads it only while setup is pending, so remove " +
			"it from the configuration once setup has completed.",
		valuesDoc:     "A password",
		secret:        true,
		group:         setupGroup,
		groupRequired: true,
		parse: func(c *Config, v string) error {
			c.unattendedSetup().OwnerPassword = secret.New(v)
			return nil
		},
	},
	{
		name: "STORYCREEP_SETUP_WORKSPACE_NAME",
		doc: "The name of the first workspace, for unattended first-run setup: 1 to 100 characters, not all of " +
			"them blank, and without control characters. Its default locale is `STORYCREEP_DEFAULT_LOCALE`.",
		valuesDoc:     "A workspace name",
		example:       "Harbour Studio",
		group:         setupGroup,
		groupRequired: true,
		parse: func(c *Config, v string) error {
			c.unattendedSetup().WorkspaceName = v
			return nil
		},
	},
	{
		name: "STORYCREEP_SETUP_WORKSPACE_SLUG",
		doc: "The slug of the first workspace, for unattended first-run setup: groups of lowercase ASCII letters " +
			"and digits joined by single hyphens, of at most 64 characters, and not a reserved word. Not set, it " +
			"is derived from `STORYCREEP_SETUP_WORKSPACE_NAME`, as the setup API derives it.",
		valuesDoc: "A slug, such as `harbour-studio`",
		group:     setupGroup,
		parse: func(c *Config, v string) error {
			c.unattendedSetup().WorkspaceSlug = v
			return nil
		},
	},
	{
		name: "STORYCREEP_SETUP_SIGNUP",
		doc: "The signup policy that unattended first-run setup records, as the setup API's `signup_policy` " +
			"does: `closed` if it is not set. `STORYCREEP_SIGNUP` overrides it for as long as that is set.",
		values: []string{string(SignupClosed), string(SignupInvite), string(SignupOpen)},
		group:  setupGroup,
		parse: func(c *Config, v string) error {
			c.unattendedSetup().Signup = SignupPolicy(v)
			return nil
		},
	},
	{
		name: "STORYCREEP_SETUP_SMTP_URL",
		doc: "The SMTP server that unattended first-run setup records, written like `STORYCREEP_SMTP_URL`, as " +
			"the setup API's `smtp` does, with `STORYCREEP_SETUP_SMTP_FROM`, which it needs. Not set, setup " +
			"records none, and the instance sends no email unless `STORYCREEP_SMTP_URL` is set, which overrides " +
			"the recorded server for as long as it is set. The server reads it only while setup is pending, so " +
			"remove it from the configuration once setup has completed. Where it is shown, its password is `" +
			secret.Redacted + "`.",
		valuesDoc: "An `smtp://` or `smtps://` URL",
		group:     setupGroup,
		needs:     []string{"STORYCREEP_SETUP_SMTP_FROM"},
		parse: func(c *Config, v string) error {
			s, err := ParseSMTPURL(v)
			if err != nil {
				return err
			}
			u := c.unattendedSetup()
			if u.SMTP != nil {
				s.From = u.SMTP.From
			}
			u.SMTP = &s
			return nil
		},
		effective: func(c *Config) string { return c.Setup.SMTP.URL() },
	},
	{
		name: "STORYCREEP_SETUP_SMTP_FROM",
		doc: "The address that the email of the SMTP server of `STORYCREEP_SETUP_SMTP_URL`, which needs it, " +
			"comes from, written like `STORYCREEP_SMTP_FROM`.",
		valuesDoc: "An email address, alone or with a name",
		group:     setupGroup,
		needs:     []string{"STORYCREEP_SETUP_SMTP_URL"},
		parse: func(c *Config, v string) error {
			if _, msg := ParseSMTPFrom(v); msg != "" {
				return errors.New(msg)
			}
			u := c.unattendedSetup()
			if u.SMTP == nil {
				u.SMTP = &SMTP{}
			}
			u.SMTP.From = v
			return nil
		},
	},
	{
		name: "STORYCREEP_SESSION_IDLE_TIMEOUT",
		doc: "How long a session lasts without being used before it ends, and its user must sign in again " +
			"(PRD FR-AUTH-02). A request made with the session counts as using it, although the server records " +
			"the use at most once a minute. A change applies to the sessions that exist already, too.",
		valuesDoc: sessionDurationDoc,
		def:       constant("720h"),
		parse: func(c *Config, v string) error {
			d, err := parseSessionDuration(v)
			c.SessionIdleTimeout = d
			return err
		},
	},
	{
		name: "STORYCREEP_SESSION_LIFETIME",
		doc: "How long a session lasts at most, from when its user signed in, however often it is used (PRD " +
			"FR-AUTH-02). The session's cookie expires then too. A shorter value applies to the sessions that exist " +
			"already, too, but a longer one only to those that start after the change, whose cookies are made " +
			"to last that long.",
		valuesDoc: sessionDurationDoc,
		def:       constant("2160h"),
		parse: func(c *Config, v string) error {
			d, err := parseSessionDuration(v)
			c.SessionLifetime = d
			return err
		},
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
			"row-level security would not hold it, or of `pg_write_all_data`, which may change the rows of every " +
			"table past what the migrations grant. Migrations create the login if it does not exist, set its " +
			"password to the one the server logs in with (this URL's, or else the one `PGPASSWORD`, a service " +
			"file, or a password file supplies), and grant it the `storycreep_app` role. When the server logs in " +
			"with a password, they refuse a login whose `VALID UNTIL` has passed, which PostgreSQL would refuse as " +
			"if the password were wrong. They also revoke `CONNECT` on the database from `PUBLIC` and from the " +
			"server's roles, `storycreep_app`, `storycreep_definer`, and `storycreep_maintenance`, and grant it to " +
			"the login, to the maintenance login of `STORYCREEP_DATABASE_MAINTENANCE_URL` if that is set, and to " +
			"the admin login if that took its `CONNECT`, so that no other role can connect unless it is a " +
			"superuser, or it is, or inherits the privileges of, one of those logins, the database's owner, or a " +
			"role granted `CONNECT`, as a monitoring role may need to be. They fail if `PUBLIC` or one of the " +
			"server's roles can still connect afterwards, as through a grant that a role other than the database's " +
			"owner made, and a server that does not apply migrations itself refuses a migrated database that they " +
			"can connect to again, as after a dump is restored into a new database, until migrations run again. " +
			"A login belongs to the whole PostgreSQL server, not to one database, and " +
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
			"named like one of the server's roles, or starting with `pg_`, or that could do more than bypass " +
			"row-level security as `storycreep_maintenance`: one that is a superuser, has `BYPASSRLS` itself, " +
			"`CREATEROLE`, or `REPLICATION`, owns the database or objects in it, or is a member of a role that " +
			"does, `storycreep_maintenance` included but for its own `BYPASSRLS`, of `storycreep_app` or " +
			"`storycreep_definer`, of one of the roles that give access to the database server's files and " +
			"programs, or of `pg_write_all_data`. Give each Storycreep database on a server a maintenance login of " +
			"its own, as for `STORYCREEP_DATABASE_URL`. The `storycreep_maintenance` role belongs to the whole " +
			"PostgreSQL server, and so does its `BYPASSRLS`, so the login could also bypass row-level security in " +
			"another database on the server that it can connect to, as PostgreSQL lets every role do by default: " +
			"on a server shared with other applications, revoke `CONNECT` on their databases from `PUBLIC`. " +
			"Without this URL, migrations leave the maintenance login alone. It is shown redacted in the same way.",
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
		name: "STORYCREEP_TRUSTED_PROXIES",
		doc: "The reverse proxies in front of the server, whose `X-Forwarded-For` header it believes. The server " +
			"takes as a request's client the address that its connection comes from, unless that is one of " +
			"these: then it takes the rightmost address in the request's `X-Forwarded-For` header that is not one " +
			"of these, because each proxy appends the address that it received the request from, or the leftmost " +
			"address if every one is. An entry that is not an IP address, such as `unknown`, ends the search, " +
			"because what lies to its left cannot be checked, and the listed proxy to its right, which wrote it, is " +
			"taken for the client. The client's address is what the rate limits of an IP address count (PRD " +
			"NFR-SEC-06), and what a session and the audit log record. Without this setting, behind a reverse " +
			"proxy, every request seems to come from the proxy, so all clients share the rate limits of one " +
			"address, and one client that uses up the limit of signing in refuses everybody's sign-ins until it " +
			"resets. List only the addresses that the proxies " +
			"connect from, as the server sees them, because whoever connects from a listed address can claim to " +
			"be any client. Nor may a listed range hold a client's address, as the proxies see it, such as the " +
			"addresses of a local network or a VPN that uses the same range: the server skips over every listed " +
			"address in the header, a client's as well as a proxy's, so that client too can claim to be any " +
			"client. The proxy must set the header or append to it, as nginx's " +
			"`$proxy_add_x_forwarded_for` does, and Caddy and Traefik do unasked. A proxy on the same machine " +
			"as a container connects to it from the gateway of the container's network, such as `172.18.0.1`, " +
			"which `docker network inspect` shows, and a proxy in a container from that container's address.",
		valuesDoc: "IP addresses and CIDR prefixes, separated by commas, such as `172.18.0.1` or " +
			"`10.0.0.0/8, 192.168.1.7`",
		parse: func(c *Config, v string) error {
			proxies, err := parseProxies(v)
			c.TrustedProxies = proxies
			return err
		},
		effective: func(c *Config) string { return proxiesString(c.TrustedProxies) },
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
		name: "STORYCREEP_LOG_LEVEL",
		doc: "The lowest level that is logged. The record that gives the setup code, which the server writes " +
			"as a warning at each start while first-run setup is pending (PRD FR-ADMIN-02), is logged whatever " +
			"the level, because setup cannot complete through the API without the code.",
		values: []string{"debug", "info", "warn", "error"},
		def:    constant("info"),
		// slog.Level reads the names in any case; values admits only these.
		parse: func(c *Config, v string) error {
			return c.LogLevel.UnmarshalText([]byte(v))
		},
	},
}

// The bounds of a session's idle timeout and lifetime.
const (
	minSessionDuration = 5 * time.Minute
	maxSessionDuration = 365 * 24 * time.Hour
)

// sessionDurationDoc describes the values of the session keys.
const sessionDurationDoc = "A duration from `5m` to `8760h` (a year), in hours, minutes, and seconds, such as `720h` or " +
	"`1h30m`"

// parseSessionDuration parses the value of a session key.
func parseSessionDuration(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	switch {
	case err != nil:
		return 0, errors.New("not a duration, such as 720h for 30 days")
	case d < minSessionDuration || d > maxSessionDuration:
		return 0, errors.New("must be from 5m to 8760h")
	}
	return d, nil
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
