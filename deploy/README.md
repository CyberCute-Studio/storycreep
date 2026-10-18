# Deploy

The container image's Dockerfile, the published `docker-compose.yml`, the development compose file, and example configuration (PRD §10.7, §10.8).

The server is configured with `STORYCREEP_*` environment variables and an optional file. [`docs/configuration.md`](../docs/configuration.md) lists every key with its default, and `storycreep validate-config` checks a configuration without starting the server.

## Running Storycreep with Docker Compose

[`docker-compose.yml`](docker-compose.yml) runs two services: `app`, the Storycreep server, and `db`, PostgreSQL 17. It needs Docker Engine 25 or newer and Docker Compose 2.20.2 or newer (the `docker compose` command), and nothing else on the machine.

1. Put `docker-compose.yml` and [`.env.example`](.env.example) in a directory of their own.
2. Copy `.env.example` to `.env` in the same directory, and set `POSTGRES_PASSWORD` in it. `openssl rand -hex 32` makes a good one.
3. Start it:

   ```sh
   docker compose up -d
   ```

4. Complete first-run setup, as [First-run setup](#first-run-setup) says, and open <http://localhost:7777>.

On its first start, the server applies the database migrations, and creates its own database logins with the same password: `storycreep`, which it serves requests as, and `storycreep_maintainer`, which its maintenance jobs, such as hard deletion, connect as. `docker compose ps` shows both services as `healthy` once the server is ready to serve.

The data lives in two named volumes: `db`, the database, and `assets`, mounted at `/var/lib/storycreep/assets`, for the files that users upload. `docker compose down` keeps them; `docker compose down -v` deletes them, and with them everything stored in Storycreep.

The compose file names its Compose project `storycreep`, and the containers and volumes are named after the project, whatever directory the file is in. So a second copy of the directory on the same machine runs the same instance: its `docker compose up -d` recreates the first copy's containers on the same volumes, and its `docker compose down -v` deletes them. To run a second instance, such as one to try a new version on, set `COMPOSE_PROJECT_NAME` to a name of its own in its `.env` before its first start, and `STORYCREEP_PUBLISH` to a port of its own, such as `127.0.0.1:7778`, with `STORYCREEP_PUBLIC_URL` to match: the first instance has `127.0.0.1:7777`, so the second one's server would not start.

### Settings

`.env.example` describes every setting the compose file takes. Besides `POSTGRES_PASSWORD`, they all have defaults:

| Variable | Default | What it sets |
| --- | --- | --- |
| `STORYCREEP_IMAGE` | `ghcr.io/cybercute-studio/storycreep:main` | The image to run. |
| `STORYCREEP_PUBLISH` | `127.0.0.1:7777` | Where Docker publishes the server's HTTP port, as `[address:]port`. The default reaches it from this machine only. A port without an address, such as `8000`, publishes it on every network interface, as `0.0.0.0:8000` does, so to change only the port, keep the address, as in `127.0.0.1:8000`. Another port is another URL, so set `STORYCREEP_PUBLIC_URL` with it, as in `http://localhost:8000`. |
| `STORYCREEP_PUBLIC_URL` | `http://localhost:7777` | The URL users open Storycreep at. |
| `STORYCREEP_TRUSTED_PROXIES` | Not set | The addresses that a reverse proxy in front of the server connects from, whose `X-Forwarded-For` header the server believes. See [Behind a reverse proxy](#behind-a-reverse-proxy). |
| `STORYCREEP_WORKSPACE_LIMITS` | Not set | Caps on what each workspace may use, as limits and whole numbers joined by `=`, separated by commas, such as `members=20, projects=50`, of `members`, `projects`, and `storage_bytes`. Without it, no workspace has a limit. A workspace that has as many members as its `members` cap allows, guests included, lets no one else in: accepting an invitation, `storycreep grant-role`, and registering with a link invitation's link are refused, while registering with the link of an invitation to an address adds the account alone, which accepts the invitation once a member has left. `projects` and `storage_bytes` limit nothing yet, as [`docs/configuration.md`](../docs/configuration.md) says. |
| `STORYCREEP_SMTP_URL`, `STORYCREEP_SMTP_FROM` | Not set | The SMTP server that the instance sends its email through, and the address that the email comes from. See [Email](#email). |
| `STORYCREEP_SETUP_OWNER_EMAIL`, `STORYCREEP_SETUP_OWNER_NAME`, `STORYCREEP_SETUP_OWNER_PASSWORD`, `STORYCREEP_SETUP_WORKSPACE_NAME`, `STORYCREEP_SETUP_WORKSPACE_SLUG`, `STORYCREEP_SETUP_SIGNUP`, `STORYCREEP_SETUP_SMTP_URL`, `STORYCREEP_SETUP_SMTP_FROM` | Not set | Unattended first-run setup. See [First-run setup](#first-run-setup). |
| `STORYCREEP_MODE`, `STORYCREEP_PUBLIC_SITE`, `STORYCREEP_SIGNUP`, `STORYCREEP_INSTANCE_ADMIN_ACCESS`, `STORYCREEP_SESSION_IDLE_TIMEOUT`, `STORYCREEP_SESSION_LIFETIME`, `STORYCREEP_DEFAULT_LOCALE`, `STORYCREEP_LOG_LEVEL`, `STORYCREEP_ADMIN_ADDR`, `STORYCREEP_METRICS_TOKEN` | The server's | The server keys of the same names, which [`docs/configuration.md`](../docs/configuration.md) describes, as it does `STORYCREEP_TRUSTED_PROXIES`, `STORYCREEP_WORKSPACE_LIMITS`, the `STORYCREEP_SMTP_` keys, and the `STORYCREEP_SETUP_` keys. |
| `COMPOSE_PROJECT_NAME` | `storycreep` | The Compose project's name, which the containers' and volumes' names start with. Another name is another instance, with volumes of its own, so set it before the first start. |

The compose file sets the database URLs itself. The three logins in them take their password from `POSTGRES_PASSWORD`, through `PGPASSWORD`, so that a password with characters such as `@` or `/` needs no escaping.

### First-run setup

A new instance has no accounts. Until its first-run setup has completed, nobody can register one, whatever `STORYCREEP_SIGNUP` says. Setup creates the first owner, who is an instance admin, and the first workspace, which is the owner's, and records who can register afterwards: nobody (`closed`, the default), people holding an invitation (`invite`), or anyone (`open`). `STORYCREEP_SIGNUP` overrides that choice for as long as it is set. Setup can also record the SMTP server that the instance sends its email through (see [Email](#email)), which `STORYCREEP_SMTP_URL` overrides in the same way.

Completing setup asks for a setup code, so that whoever reaches a new instance first cannot make themselves its administrator. While setup is pending, the server writes the code to its log at every start:

```sh
docker compose logs app | grep setup_code
```

The web interface has no setup screen yet, so complete setup through the API, from the machine that runs the server. Write the request to a file, `setup.json`, with the code, the owner's account, the first workspace's name, and, if you like, who can register:

```json
{
  "setup_code": "7K2M-Q9XD-4RTB-H8VW",
  "owner": {"email": "jo@example.com", "display_name": "Jo Novak", "password": "at least 12 characters"},
  "workspace": {"name": "Harbour Studio"},
  "signup_policy": "closed",
  "smtp": {"host": "smtp.example.com", "port": 587, "security": "starttls", "username": "storycreep",
           "password": "the SMTP password", "from": "Storycreep <noreply@example.com>"}
}
```

and send it:

```sh
curl -H 'Content-Type: application/json' --data-binary @setup.json http://localhost:7777/api/v1/setup
```

Leave out `smtp` for an instance that sends no email. The answer is `201`, with the owner's account ID and the workspace, or an error that names what to change. Then delete `setup.json`, which holds the passwords. The API's OpenAPI document, at `/api/v1/openapi.json`, describes `POST /api/v1/setup` in full.

To complete setup without anyone visiting the instance, set `STORYCREEP_SETUP_OWNER_EMAIL`, `STORYCREEP_SETUP_OWNER_NAME`, `STORYCREEP_SETUP_OWNER_PASSWORD`, and `STORYCREEP_SETUP_WORKSPACE_NAME` in `.env` before the first start, and optionally `STORYCREEP_SETUP_WORKSPACE_SLUG`, `STORYCREEP_SETUP_SIGNUP`, and the SMTP server, with `STORYCREEP_SETUP_SMTP_URL` and `STORYCREEP_SETUP_SMTP_FROM`, which are written as `STORYCREEP_SMTP_URL` and `STORYCREEP_SMTP_FROM` are. The server then completes setup itself, as the setup API would, and needs no code. A value that setup refuses, such as a password of fewer than 12 characters, stops the server, and its log names the setting. Once setup has completed, the server ignores these settings, so remove them from `.env`, the passwords above all, and run `docker compose up -d` again.

### Email

The instance emails the links that verify an account's email address and reset its password, which last 24 hours and an hour, through the SMTP server that first-run setup chose, or that `STORYCREEP_SMTP_URL` and `STORYCREEP_SMTP_FROM` set, which override it:

```sh
STORYCREEP_SMTP_URL=smtp://username:password@smtp.example.com:587
STORYCREEP_SMTP_FROM=Storycreep <noreply@example.com>
```

`smtp://` connects with STARTTLS, which the server must offer, on port 587 unless the URL names another port, and `smtps://` with TLS from the start, on port 465 unless it names another. For a relay on the same machine or network that speaks neither, add `?security=none`, as in `smtp://relay:25?security=none`; the instance then signs in to it only on `localhost`, `127.0.0.1`, or `::1`, so that a password never crosses a network in the clear. Percent-encode the username and the password, as `%40` for `@`.

Without an SMTP server, the instance sends no email, and says so to its users. Registering then sends nothing, and an account's user who has forgotten their password asks an instance admin, who takes a link that resets it with `POST /api/v1/admin/password-reset-links`, and gives it to the user alone, as text. On the machine that runs the server, the operator can print one instead:

```sh
docker compose exec app storycreep reset-password jo@example.com
```

### API tokens

Programs, such as a CI job that pulls a project's releases, use API tokens, which a member of a workspace makes for themselves through the API, or the operator with `storycreep create-token`, for a member of a workspace by its slug:

```sh
docker compose exec app storycreep create-token --scopes snapshot:read jo@example.com harbour "CI releases"
```

It prints the token's secret alone, once: the database keeps only its hash. A program sends it as `Authorization: Bearer scp_…`, so a reverse proxy in front of the server must pass that header on, as Caddy, Traefik, and nginx do unasked. A token acts only with the scopes that it holds, and never does more than its member's role allows at the time; removing the member revokes it. With any scope but `export:read`, it lists its workspace and the projects that it acts on, and reads them; the routes of accounts, members, and the administration of a workspace take a session alone, and answer a token with 403 `session_required`. Its requests count against its own rate limits and its member's, and in its workspace's usage, as `api_requests`.

### Behind a reverse proxy

The server speaks plain HTTP, and expects a reverse proxy in front of it to terminate TLS. Run the proxy on the same machine, forward it to `127.0.0.1:7777`, and set `STORYCREEP_PUBLIC_URL` to the proxy's `https` URL, such as `https://storycreep.example.com`.

A proxy that runs in a container cannot reach `127.0.0.1:7777`, because its `127.0.0.1` is its own container's. Connect it to the compose project's network, `storycreep_default`, or `<name>_default` for a project that `COMPOSE_PROJECT_NAME` names, as an external network of the proxy's own compose file, for example, and forward it to `app:8080`.

Either way, every request then reaches the server from the proxy, so set `STORYCREEP_TRUSTED_PROXIES` to tell the server to take the client's address from the `X-Forwarded-For` header that the proxy sets, as Caddy and Traefik do unasked, and nginx with `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`. Without it, all clients share the rate limits of the proxy's address, which count every request of the API: one client that uses up the limit of signing in, 20 requests a minute, keeps everyone from signing in until the minute ends, one busy client, such as a CI job, that uses up the address's limit of reading or of changing something does the same to everyone's such requests, and the sessions that users list show the proxy's address. The proxy's connections come from the compose project's network: from its gateway for a proxy on the same machine, and from the proxy's container for one on the network. Docker gives the networks that it creates addresses in `172.16.0.0/12`, and then in `192.168.0.0/16`, unless its daemon is configured otherwise, and Compose may give the network other addresses each time it creates it, as after `docker compose down`, so set the range rather than one address:

```sh
STORYCREEP_TRUSTED_PROXIES=172.16.0.0/12
```

`docker network inspect storycreep_default --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}'` prints the network's addresses, to check that the range holds them. Whoever connects from a trusted address can claim any address, so trust only addresses that nothing but the proxy connects from: with the default `STORYCREEP_PUBLISH`, only programs on this machine reach the server. Nor may the range hold the addresses of the clients themselves, as the proxy sees them, because the server skips over every trusted address in `X-Forwarded-For`, a client's as well as a proxy's, and takes the one to its left, which the client wrote. If the users' own network or VPN has addresses in `172.16.0.0/12`, such as `172.20.0.0/16`, any of them could claim any address and slip past the rate limits of an IP address, so trust only the subnet that the command above prints, and set it again whenever Compose creates the network anew.

To reach the server from other machines without a proxy, set `STORYCREEP_PUBLISH=0.0.0.0:7777` and `STORYCREEP_PUBLIC_URL` to the URL they use, and leave `STORYCREEP_TRUSTED_PROXIES` unset. Their traffic is then unencrypted, and a port that Docker publishes is open even where a host firewall, such as ufw, would block it.

### Metrics

The server serves `/metrics` on an admin port, which listens inside the container only by default. Set `STORYCREEP_ADMIN_ADDR=:9090` to let a Prometheus on the compose network scrape `app:9090`, and `STORYCREEP_METRICS_TOKEN` to require a bearer token from it.

### Changing the database password

The database takes `POSTGRES_PASSWORD` when it is created, and ignores it afterwards. To change the password later, set the new one in the database, then in `.env`, and restart the server, which updates its own logins' passwords at startup:

```sh
docker compose exec db psql -U postgres -c '\password postgres'
docker compose up -d
```

## Upgrading

The server applies the migrations a new version brings when it starts, under a lock. Migrations only move forward, so back up first: the way back to the previous version is restoring the backup.

1. Back up the database and the assets:

   ```sh
   docker compose exec db pg_dump -U postgres -Fc -f /tmp/storycreep.dump storycreep
   docker compose cp db:/tmp/storycreep.dump storycreep.dump
   docker compose exec db rm /tmp/storycreep.dump
   docker run --rm --volumes-from "$(docker compose ps --all --quiet app):ro" -v "$PWD":/backup alpine tar -czf /backup/assets.tar.gz -C /var/lib/storycreep/assets .
   ```

   `pg_dump` writes the backup inside the database's container, and `docker compose cp` copies it out as it is. Redirecting `pg_dump`'s output into a file with `>` would not work in every shell: Windows PowerShell 5.1 rewrites the bytes it redirects, which leaves a backup that cannot be restored. `--volumes-from` gives the archiving container the volumes of this instance's server, read-only, whatever the project is named.

2. Pull the new image and restart the server:

   ```sh
   docker compose pull app
   docker compose up -d
   ```

3. Check that both services are `healthy` with `docker compose ps`. `docker compose logs app` lists the migrations that were applied.

A `storycreep backup` command, and a tested restore procedure, are planned (PRD FR-DATA-05).

## The image

The image is published as `ghcr.io/cybercute-studio/storycreep`, for `linux/amd64` and `linux/arm64`, from the newest commit on the main branch each time CI passes there, after a push and weekly, tagged `main` and `sha-<commit>`. `main` never moves back to an older commit, whose server would refuse a database that a newer one has migrated. The weekly build picks up its base images' updates. Release tags start with the first release.

It holds the `storycreep` binary, with the web interface embedded in it, on a [distroless](https://github.com/GoogleContainerTools/distroless) base: there is no shell, package manager, or build toolchain in it, and it runs as the non-root user 65532 (PRD FR-ADMIN-01 AC4). It runs `storycreep serve` unless given another command, as in `docker run --rm ghcr.io/cybercute-studio/storycreep:main --help`. Its health check runs `storycreep healthcheck`, which requests `/readyz`. The health check runs without arguments, so name a configuration file with `STORYCREEP_CONFIG_FILE`, which it reads as `serve` does, rather than with `serve --config`, which it does not see.

To build it, from the repository's root:

```sh
docker build -f deploy/Dockerfile -t storycreep .
```

The binary is cross-compiled, so building for both platforms needs no emulation:

```sh
docker buildx build --platform linux/amd64,linux/arm64 -f deploy/Dockerfile .
```

The build arguments `STORYCREEP_VERSION`, `STORYCREEP_COMMIT`, and `STORYCREEP_COMMIT_TIME` set what `/version` reports, because the build has no Git checkout to read them from.

## Development

[`dev/docker-compose.yml`](dev/docker-compose.yml) runs PostgreSQL with its port published on this machine, and the server, built from the source tree, rebuilding and restarting it whenever its sources change. It needs Docker Compose 2.32.0 or newer. From the repository's root:

```sh
docker compose -f deploy/dev/docker-compose.yml up --watch
```

The server is at <http://localhost:8080>, its metrics at <http://localhost:9090/metrics>, and the database at `localhost:5432`, with the password `storycreep-dev`. Never use this file for a deployment. It has a directory of its own, so that it reads the `.env` there, if there is one, and not the `.env` of an instance run from `deploy/`, whose `COMPOSE_PROJECT_NAME` would make the development project that instance's.

To run the server natively, start only the database with `docker compose -f deploy/dev/docker-compose.yml up -d db`, and run the server in `server/` with:

```sh
export STORYCREEP_DATABASE_URL=postgres://storycreep:storycreep-dev@localhost:5432/storycreep
export STORYCREEP_DATABASE_ADMIN_URL=postgres://postgres:storycreep-dev@localhost:5432/storycreep
export STORYCREEP_DATABASE_MAINTENANCE_URL=postgres://storycreep_maintainer:storycreep-dev@localhost:5432/storycreep
export STORYCREEP_PUBLIC_URL=http://localhost:8080
go run ./cmd/storycreep serve
```
