# Deploy

The container image's Dockerfile, the published `docker-compose.yml`, the development compose file, and example configuration (PRD §10.7, §10.8).

The server is configured with `STORYCREEP_*` environment variables and an optional file. [`docs/configuration.md`](../docs/configuration.md) lists every key with its default, and `storycreep validate-config` checks a configuration without starting the server.

## Running Storycreep with Docker Compose

[`docker-compose.yml`](docker-compose.yml) runs two services: `app`, the Storycreep server, and `db`, PostgreSQL 17. It needs Docker Engine 25 or newer with Docker Compose v2, and nothing else on the machine.

1. Put `docker-compose.yml` and [`.env.example`](.env.example) in a directory of their own.
2. Copy `.env.example` to `.env` in the same directory, and set `POSTGRES_PASSWORD` in it. `openssl rand -hex 32` makes a good one.
3. Start it:

   ```sh
   docker compose up -d
   ```

4. Open <http://localhost:7777>.

On its first start, the server applies the database migrations, and creates its own database login, `storycreep`, with the same password. `docker compose ps` shows both services as `healthy` once the server is ready to serve.

The data lives in two named volumes: `db`, the database, and `assets`, mounted at `/var/lib/storycreep/assets`, for the files that users upload. `docker compose down` keeps them; `docker compose down -v` deletes them, and with them everything stored in Storycreep.

The compose file names its Compose project `storycreep`, and the containers and volumes are named after the project, whatever directory the file is in. So a second copy of the directory on the same machine runs the same instance: its `docker compose up -d` recreates the first copy's containers on the same volumes, and its `docker compose down -v` deletes them. To run a second instance, such as one to try a new version on, set `COMPOSE_PROJECT_NAME` to a name of its own in its `.env` before its first start.

### Settings

`.env.example` describes every setting the compose file takes. Besides `POSTGRES_PASSWORD`, they all have defaults:

| Variable | Default | What it sets |
| --- | --- | --- |
| `STORYCREEP_IMAGE` | `ghcr.io/cybercute-studio/storycreep:main` | The image to run. |
| `STORYCREEP_PUBLISH` | `127.0.0.1:7777` | Where Docker publishes the server's HTTP port, as `[address:]port`. The default reaches it from this machine only. |
| `STORYCREEP_PUBLIC_URL` | `http://localhost:7777` | The URL users open Storycreep at. |
| `STORYCREEP_MODE`, `STORYCREEP_PUBLIC_SITE`, `STORYCREEP_SIGNUP`, `STORYCREEP_LOG_LEVEL`, `STORYCREEP_ADMIN_ADDR`, `STORYCREEP_METRICS_TOKEN` | The server's | The server keys of the same names, which [`docs/configuration.md`](../docs/configuration.md) describes. |
| `COMPOSE_PROJECT_NAME` | `storycreep` | The Compose project's name, which the containers' and volumes' names start with. Another name is another instance, with volumes of its own, so set it before the first start. |

The compose file sets the database URLs itself. The two logins in them take their password from `POSTGRES_PASSWORD`, through `PGPASSWORD`, so that a password with characters such as `@` or `/` needs no escaping.

### Behind a reverse proxy

The server speaks plain HTTP, and expects a reverse proxy in front of it to terminate TLS. Run the proxy on the same machine, forward it to `127.0.0.1:7777`, and set `STORYCREEP_PUBLIC_URL` to the proxy's `https` URL, such as `https://storycreep.example.com`.

To reach the server from other machines without a proxy, set `STORYCREEP_PUBLISH=0.0.0.0:7777` and `STORYCREEP_PUBLIC_URL` to the URL they use. Their traffic is then unencrypted, and a port that Docker publishes is open even where a host firewall, such as ufw, would block it.

### Metrics

The server serves `/metrics` on an admin port, which listens inside the container only by default. Set `STORYCREEP_ADMIN_ADDR=:9090` to let a Prometheus on the compose network scrape `app:9090`, and `STORYCREEP_METRICS_TOKEN` to require a bearer token from it.

### Changing the database password

The database takes `POSTGRES_PASSWORD` when it is created, and ignores it afterwards. To change the password later, set the new one in the database, then in `.env`, and restart the server, which updates its own login's password at startup:

```sh
docker compose exec db psql -U postgres -c '\password postgres'
docker compose up -d
```

## Upgrading

The server applies the migrations a new version brings when it starts, under a lock. Migrations only move forward, so back up first: the way back to the previous version is restoring the backup.

1. Back up the database and the assets:

   ```sh
   docker compose exec -T db pg_dump -U postgres -Fc storycreep > storycreep.dump
   docker run --rm -v storycreep_assets:/assets:ro -v "$PWD":/backup alpine tar -czf /backup/assets.tar.gz -C /assets .
   ```

2. Pull the new image and restart the server:

   ```sh
   docker compose pull app
   docker compose up -d
   ```

3. Check that both services are `healthy` with `docker compose ps`. `docker compose logs app` lists the migrations that were applied.

The volume names start with the compose project's name, `storycreep`, or the one that `COMPOSE_PROJECT_NAME` sets. A `storycreep backup` command, and a tested restore procedure, are planned (PRD FR-DATA-05).

## The image

The image is published as `ghcr.io/cybercute-studio/storycreep`, for `linux/amd64` and `linux/arm64`, from every commit on the main branch, tagged `main` and `sha-<commit>`. Release tags start with the first release.

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

[`docker-compose.dev.yml`](docker-compose.dev.yml) runs PostgreSQL with its port published on this machine, and the server, built from the source tree, rebuilding and restarting it whenever its sources change. From the repository's root:

```sh
docker compose -f deploy/docker-compose.dev.yml up --watch
```

The server is at <http://localhost:8080>, its metrics at <http://localhost:9090/metrics>, and the database at `localhost:5432`, with the password `storycreep-dev`. Never use this file for a deployment.

To run the server natively, start only the database with `docker compose -f deploy/docker-compose.dev.yml up -d db`, and run the server in `server/` with:

```sh
export STORYCREEP_DATABASE_URL=postgres://storycreep:storycreep-dev@localhost:5432/storycreep
export STORYCREEP_DATABASE_ADMIN_URL=postgres://postgres:storycreep-dev@localhost:5432/storycreep
export STORYCREEP_PUBLIC_URL=http://localhost:8080
go run ./cmd/storycreep serve
```
