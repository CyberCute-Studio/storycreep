<!-- Generated from server/internal/config. Do not edit; run
     "go test ./internal/config -run TestReference -update" in server/ instead. -->

# Configuration reference

Storycreep reads its configuration from environment variables and an optional
configuration file. It validates every key when it starts, and if any key is
missing or invalid it exits with a non-zero status and a message naming each
one. To check a configuration without starting anything, run:

```sh
storycreep validate-config
```

It prints each key's effective value and where the value came from, with
secrets redacted, followed by the capabilities they resolve to.

## Where values come from

For each key, the first of these that is set wins:

1. The environment variable of the same name.
2. The configuration file.
3. The key's default.

An empty value counts as unset. A required key without a value is an error.

An environment variable that starts with `STORYCREEP_` but is not a key
below is reported as a warning and otherwise ignored.

## The configuration file

The file is optional. Name it with `--config <path>` or with the
`STORYCREEP_CONFIG_FILE` environment variable; the flag wins. It is UTF-8
text of `KEY=value` lines with the key names below:

```sh
# /etc/storycreep/storycreep.env
STORYCREEP_PUBLIC_URL=https://storycreep.example.com
STORYCREEP_SIGNUP=invite
```

- Blank lines and lines starting with `#` are ignored.
- Whitespace around the key and the value is ignored.
- A value wrapped in a matching pair of `"` or `'` is unwrapped.
- Nothing is unescaped or expanded, and a `#` after a value is part of
  the value.
- Every key must be one of the keys below and appear at most once.
  `STORYCREEP_CONFIG_FILE` cannot be set in the file.

This format is simpler than the environment files of Docker Compose
(`env_file`) and systemd (`EnvironmentFile`). A file written for either is
read the same way here only if it has no `export` in front of a key, no comment
after a value, no escape sequence, and no variable reference such as
`${HOME}`. systemd also ignores lines that start with `;` and lines without
an `=`, which are errors here.

## Keys

| Key | Values | Default | Description |
| --- | --- | --- | --- |
| `STORYCREEP_MODE` | `self-hosted`, `hosted` | `self-hosted` | The deployment mode: `self-hosted` for an instance run by its own operator, `hosted` for the operated multi-tenant service. It decides the [capabilities](#capabilities), and no other code reads it. |
| `STORYCREEP_PUBLIC_URL` | An `http` or `https` URL | Required | The URL users open Storycreep at: `http` or `https`, a host name or IP address, and an optional port, with no path. Behind a reverse proxy that terminates TLS, it is the proxy's `https` URL, not the address the server listens on. Write an internationalized domain name in its `xn--` (Punycode) form, and an IPv4 address as four decimal numbers. Hosted mode requires `https`. Example: `https://storycreep.example.com`. |
| `STORYCREEP_PUBLIC_SITE` | `on`, `off` | `off` (self-hosted), `on` (hosted) | Whether the instance serves the public site: the marketing pages and the documentation. |
| `STORYCREEP_SIGNUP` | `closed`, `invite`, `open` | `closed` (self-hosted), `open` (hosted) | Who can register an account: `closed` (no one), `invite` (people holding an invitation), or `open` (anyone). Hosted mode always uses `open`, and another value is an error there. |
| `STORYCREEP_DEFAULT_LOCALE` | A locale that the server has a catalog for: `en` | `en` | The instance's default locale, as a BCP 47 language tag (PRD FR-I18N-02). The server writes its messages, such as those of the API's errors, in the locale that the request's `Accept-Language` header prefers among those that the server has a catalog for, or else in this one. A tag with a region, such as `en-GB`, selects the catalog of that region if the server has one, and else the catalog of its language. Only a locale that the server has a catalog for is accepted, so that a mistake is found when the server starts. |
| `STORYCREEP_DATABASE_URL` | A `postgres://` URL | Required | The PostgreSQL database, and the login the server connects to it as: `postgres://user:password@host:port/database`, with the scheme (`postgres` or `postgresql`) in lowercase and characters such as `@`, `:`, `/`, `?`, and `#` in the password percent-encoded. The database server must run PostgreSQL 15 or newer. The login must be an ordinary role with a name of its own: migrations refuse one named `storycreep_app`, `storycreep_definer`, or `storycreep_maintenance`, the roles they create, or starting with `pg_`, which PostgreSQL reserves. The server refuses to use one that is a superuser, has `BYPASSRLS`, `CREATEROLE`, or `REPLICATION`, or owns the database or objects in it, or that is a member of a role that does, of one of the server's privileged roles, or of `pg_execute_server_program`, `pg_read_server_files`, or `pg_write_server_files`, which give access to the database server's files and programs, because row-level security would not hold it. Migrations create the login if it does not exist, set its password to the one the server logs in with (this URL's, or else the one `PGPASSWORD`, a service file, or a password file supplies), and grant it the `storycreep_app` role. When the server logs in with a password, they refuse a login whose `VALID UNTIL` has passed, which PostgreSQL would refuse as if the password were wrong. They also revoke `CONNECT` on the database from `PUBLIC` and from `storycreep_app`, and grant it to the login, and to the admin login if that took its `CONNECT`, so that no other role can connect unless it is a superuser, or it is, or inherits the privileges of, the login, the database's owner, or a role granted `CONNECT`, as a monitoring role may need to be. They fail if `PUBLIC` or `storycreep_app` can still connect afterwards, as through a grant that a role other than the database's owner made, and a server that does not apply migrations itself refuses a migrated database that they can connect to again, as after a dump is restored into a new database, until migrations run again. A login belongs to the whole PostgreSQL server, not to one database, and so does the `storycreep_app` role, which every Storycreep database on the server grants its privileges to. So give each Storycreep database on a server a login of its own, which can connect only to its own database: a shared login would reach all of them, and migrating one would change the password the others log in with. The pool parameters, such as `pool_max_conns`, configure the pool that serves requests. A server that runs the background jobs opens a second pool of the login for them, configured the same way but with at most 7 connections, whatever `pool_max_conns` says. Query parameters, such as `sslmode`, are read as libpq reads them, with an `=` in a value percent-encoded as `%3D`, and the libpq environment variables, such as `PGSSLMODE`, fill in settings the URL leaves out. A space anywhere in the URL is written as `%20`. The URL is checked as the server's connections read it, so a setting they could not use, such as an unknown `sslmode`, a certificate file that cannot be read, or a libpq parameter that they do not support, such as `gssencmode` or `keepalives`, is an error. Where the URL is shown, its password is `[REDACTED]`, and so is the value of every query parameter except the libpq and pgx settings that hold no secret, such as `sslmode`, and `application_name`, `search_path`, `timezone`, and `client_encoding`. Example: `postgres://storycreep:password@db:5432/storycreep`. |
| `STORYCREEP_DATABASE_ADMIN_URL` | A `postgres://` URL | Not set | A PostgreSQL superuser login for the same database, written like `STORYCREEP_DATABASE_URL`. Only applying migrations uses it, as `storycreep migrate` does. Migrations need a superuser because they create the server's database roles, and one of them bypasses row-level security. It must name a different login from `STORYCREEP_DATABASE_URL`, and the same database. Migrations refuse a login that is, or acts as, `storycreep_app`, `storycreep_definer`, or `storycreep_maintenance`, because they make those roles unable to log in and take `SUPERUSER` away from them. Migrations create the server's tables in the `public` schema, whatever `search_path` this URL, the login, or the database sets. It is shown redacted in the same way. |
| `STORYCREEP_HTTP_ADDR` | `host:port` or `:port` | `:8080` | The address the server listens on for HTTP: `host:port`, or `:port` for every network interface. The host is an IP address or a host name, and the port a number. It serves the API, the health endpoints `/healthz`, `/readyz`, and `/version`, and everything else except `/metrics`. Behind a reverse proxy, the proxy forwards to this address, and users open `STORYCREEP_PUBLIC_URL`. |
| `STORYCREEP_ADMIN_ADDR` | `host:port`, `:port`, or `off` | `127.0.0.1:9090` | The address of the admin port, which serves only `/metrics`, in Prometheus's text format, written like `STORYCREEP_HTTP_ADDR`, or `off` for no admin port. Its port must differ from the port of `STORYCREEP_HTTP_ADDR`, unless both name a host, and different ones. The default listens on the loopback interface only, so that only the machine itself can reach it; in a container, set it to `:9090` to let a Prometheus on the container's network scrape it. How `/metrics` answers depends on the `MetricsEndpoint` [capability](#capabilities): `unauthenticated` serves it to anyone who can reach the port, and `protected` only to a request with `STORYCREEP_METRICS_TOKEN` as its bearer token. With `protected` and no token set, there are no metrics, and the admin port is not opened. |
| `STORYCREEP_METRICS_TOKEN` | A token of at least 16 characters | Not set | The bearer token that a scraper sends to `/metrics` on the admin port, in an `Authorization: Bearer` header, when the `MetricsEndpoint` capability is `protected`. Setting it also makes the capability `protected` in self-hosted mode. It is at least 16 characters of letters, digits, and `-._~+/`, optionally followed by `=` characters, as RFC 6750 allows; `openssl rand -hex 32` makes one. Hosted mode protects `/metrics` whether or not it is set, so without it, hosted mode serves no metrics. Secret: shown as `[REDACTED]`. |
| `STORYCREEP_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` | The lowest level that is logged. |
| `STORYCREEP_CONFIG_FILE` | A file path | Not set | The [configuration file](#the-configuration-file) to read. It is read from the environment only, and `--config` overrides it. |

## Capabilities

`STORYCREEP_MODE` resolves into the capabilities below (PRD §10.3). Code
that behaves differently in the two modes reads a capability, never the mode.
A capability with a key in the last column starts from the mode's value and
can be changed with that key.

| Capability | `self-hosted` | `hosted` | Changed by |
| --- | --- | --- | --- |
| `PublicSite` | `off` | `on` | `STORYCREEP_PUBLIC_SITE` |
| `Signup` | `closed` | `open` | `STORYCREEP_SIGNUP` (self-hosted only) |
| `EnforceEntitlements` | `off` | `on` | — |
| `Billing` | `off` | `on` | — |
| `EmailRequired` | `off` | `on` | — |
| `BlobDriver` | `local` | `s3` | — |
| `MetricsEndpoint` | `unauthenticated` | `protected` | `STORYCREEP_METRICS_TOKEN` (`protected` when set) |
| `InstanceAdminUI` | `on` | `operator-only` | — |
| `SSO` | `off` | `plan-feature` | — |
