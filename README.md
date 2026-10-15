# Storycreep

Storycreep is a self-hostable web application for designing branching game narratives, plus a Godot 4 addon that pulls the authored narrative into a game as deterministic JSON and runs it. It makes cross-conversation dependencies visible: every variable knows where it is written and read, and every board knows which other boards it depends on.

The product is specified in [`docs/PRD.md`](docs/PRD.md) and built in the order of [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md). It is in early development.

## Repository layout

| Path | Contents |
| --- | --- |
| `server/` | The Go module: the single `storycreep` binary and its internal packages. |
| `web/app/` | The editor, a Vite and React progressive web app. |
| `web/site/` | The public site and documentation, built with Astro. |
| `web/shared/` | Design tokens, i18n catalogs, and the generated API client and types. |
| `godot/` | The Godot addon (editor plugin and runtime) and an example game. |
| `schema/` | JSON Schemas, fixtures, the expression corpus, and the conformance suite. |
| `deploy/` | The Dockerfile, docker-compose files, and example configuration. |
| `docs/` | The PRD, the implementation plan, and contributor and operations documentation. |

The JavaScript packages are pnpm workspaces. See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the toolchain and the checks.

## Running Storycreep

Storycreep runs as one container image, `ghcr.io/cybercute-studio/storycreep`, with PostgreSQL. [`deploy/README.md`](deploy/README.md) explains how to run both with Docker Compose.

## Licence

Storycreep is licensed under the [GNU Affero General Public License v3.0 only](LICENSE) (AGPL-3.0-only). The Godot addon in `godot/addons/storycreep/` is licensed under the [MIT License](godot/addons/storycreep/LICENSE), so games that use it carry no AGPL obligations. Contributions require the [Contributor License Agreement](CLA.md).

Report security issues as described in [`SECURITY.md`](SECURITY.md).
