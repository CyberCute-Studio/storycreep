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
| `MetricsEndpoint` | `unauthenticated` | `protected` | — |
| `InstanceAdminUI` | `on` | `operator-only` | — |
| `SSO` | `off` | `plan-feature` | — |
