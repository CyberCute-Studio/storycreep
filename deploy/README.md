# Deploy

The container image's Dockerfile, the published `docker-compose.yml`, the development compose file, and example configuration (PRD §10.7, §10.8).

The server is configured with `STORYCREEP_*` environment variables and an optional file. [`docs/configuration.md`](../docs/configuration.md) lists every key with its default, and `storycreep validate-config` checks a configuration without starting the server.
