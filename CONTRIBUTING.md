# Contributing to Storycreep

Thank you for helping. This page covers how the project is organised, the contributor licence agreement, and how to build and check your change.

## How the work is organised

- [`docs/PRD.md`](docs/PRD.md) is the contract. Every functional requirement has a stable ID (such as `FR-EDIT-02`) and acceptance criteria. If the code and the PRD disagree, the PRD wins until it is amended.
- [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md) cuts the PRD into an ordered list of pull requests. Its standing checklist applies to every pull request.
- If a requirement turns out to be wrong or unbuildable, amend the PRD first, with a row in its document history, and then the plan. The plan's "Rules for every edit" say what each edit must keep true.

## Contributor License Agreement

Storycreep is licensed under AGPL-3.0-only, except the Godot addon in `godot/addons/storycreep/`, which is MIT so that games can include it freely. Before your first pull request can be merged, you sign the [Contributor License Agreement](CLA.md).

**Why.** The CLA lets the maintainer offer Storycreep under other terms later, such as a commercial licence alongside the AGPL, without having to find and ask every past contributor (PRD ADR-011). Without it, a licence change would need the consent of everyone who ever contributed.

**What it does not do.** You keep the copyright in your work. The CLA is a licence grant, not a transfer of ownership. Your contribution is still published under the project's licence.

**How to sign.** When you open your first pull request, the CLA bot comments on it with a link to the agreement. To sign, post this comment on the pull request:

> I have read the CLA Document and I hereby sign the CLA

The bot records your GitHub user name and user ID, the ID of your comment, the pull request number, and the date in the `cla-signatures` branch, where they are public. You sign once; later pull requests pass automatically. If the check does not update, comment `recheck`.

## Development setup

The required tools run natively on Windows ARM64, macOS, and Linux. No tool in the workflow may need x86 emulation or WSL (PRD §9.9), and CI checks this by running on `ubuntu-latest`, `windows-11-arm`, and `macos-latest`.

- **Go** 1.26 or newer.
- **Node.js** 24 (see `.nvmrc`).
- **pnpm** at the version pinned in `package.json`. With Node.js 24, `corepack enable pnpm` installs it.

Then install the JavaScript dependencies:

```sh
pnpm install
```

## Checks

These are the checks CI runs. Run them before you push.

| Command | What it checks |
| --- | --- |
| `pnpm check` | Biome lint and format for the web packages and root configuration. `pnpm fix` applies the fixes. |
| `pnpm typecheck` | TypeScript in every web package that has a `typecheck` script. |
| `pnpm test` | Tests in every web package that has a `test` script. |
| `gofmt -l .`, `go vet ./...`, and `go test ./...` in `server/` | The Go module. `gofmt -l .` must print nothing; `gofmt -w .` formats the code. |
| `pnpm docs:check` | The PRD dependency graph and the plan's ordering, coverage, and reference rules. |
| `pnpm audit --audit-level high`, and `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` in `server/` | Known vulnerabilities in the dependencies. CI runs these in a separate job, on every pull request and weekly. |

`pnpm docs:check` runs `go run ./cmd/doccheck` in `server/`. It reports every problem as `file:line: message`.

[`docs/configuration.md`](docs/configuration.md) is generated from the key declarations in `server/internal/config/keys.go`, and `go test ./...` fails when it is out of date. After you add or change a configuration key, regenerate it by running `go test ./internal/config -run TestReference -update` in `server/`.

## Conventions

- Text files use LF line endings everywhere. `.gitattributes` enforces this, and `.editorconfig` sets the indentation: two spaces, except tabs for Go and GDScript.
- Every user-facing string goes through the i18n layer, and every new endpoint is in the OpenAPI document. The plan's standing checklist lists the rest.
- Only `server/internal/config` reads the environment or knows the deployment mode (`STORYCREEP_MODE`). Code that behaves differently in the two modes reads `config.Capabilities`. A test fails on any other package that reads an environment variable, names `STORYCREEP_MODE`, or compares against a mode value (PRD FR-ADMIN-01 AC3).
- Secrets are held as `secret.Value` (`server/internal/secret`), which prints, logs, and encodes as `[REDACTED]`. Call `Reveal` only where the secret is used.

## Security issues

Do not report vulnerabilities in public issues. See [`SECURITY.md`](SECURITY.md).
