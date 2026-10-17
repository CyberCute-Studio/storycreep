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
- **Docker**, for the server's database tests, which run PostgreSQL in a container (PRD NFR-Q-01): Docker Desktop on Windows and macOS, Docker Engine on Linux.

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
| `gofmt -l .`, `go vet ./...`, and `go test ./...` in `server/` | The Go module. `gofmt -l .` must print nothing; `gofmt -w .` formats the code. The database tests need Docker, and `go test -short ./...` skips them. CI runs them on Linux against PostgreSQL 15, 16, and 17, and with `-short` on Windows and macOS, whose runners cannot run Linux containers. |
| `pnpm migrations:check` | No migration that is on the main branch was changed, deleted, or renamed (see [Database migrations](#database-migrations)). It needs the history of `origin/main`. |
| `pnpm api:check` | The API's OpenAPI document breaks no client of the one on the main branch (see [The API contract](#the-api-contract)). It needs the history of `origin/main`. |
| `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 diff` in `server/internal/database/` | The code generated from the SQL queries is up to date. |
| `pnpm docs:check` | The PRD dependency graph and acceptance criteria, and the plan's ordering, coverage, and reference rules. |
| `pnpm traceability` | The requirement traceability report (PRD NFR-Q-02): which tests verify each requirement, and which MVP acceptance criteria no test verifies yet. It writes `traceability-report/traceability.md` and `traceability.json`, and fails on a tag it cannot use (see below) or on a problem in the PRD. A criterion without a test does not fail the build until the MVP release gate (plan item 119). CI publishes the report as the `traceability-report` artifact. |
| `pnpm audit --audit-level high`, and `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` in `server/` | Known vulnerabilities in the dependencies. CI runs these in a separate job, on every pull request and weekly. |
| `go test ./internal/deploy` in `server/`, with an image built from `deploy/Dockerfile` | The container image and `deploy/docker-compose.yml` (see [The container image](#the-container-image)). Without an image, it checks only the compose files' contents. |

`pnpm docs:check` runs `go run ./cmd/doccheck` in `server/`, and `pnpm traceability` runs `go run ./cmd/traceability`. Both report every problem as `file:line: message`. Run `go run ./cmd/traceability` in `server/` to print the report to the terminal instead.

## The container image

`deploy/Dockerfile` builds the container image (PRD FR-ADMIN-01 AC4), and `deploy/docker-compose.yml` runs it with PostgreSQL (FR-ADMIN-07). [`deploy/README.md`](deploy/README.md) describes both, and how to run the development compose file. The tests in `server/internal/deploy` check them:

- `go test ./...` checks the compose files' contents, on every OS. The published compose file must pass every configuration key to the server, so a new key needs a line in its `environment`, in `deploy/.env.example`, and in `deploy/README.md`.
- The image's tests need Docker, and skip without an image to test. CI's image job runs them on a `linux/amd64` and a `linux/arm64` runner: it builds the image for both platforms into an OCI archive, whose platforms, user, and files `STORYCREEP_TEST_IMAGE_ARCHIVE` lets the tests check, and loads the runner's platform's image, which `STORYCREEP_TEST_IMAGE` names and the tests start `docker-compose.yml` with.

To run them locally, build both from the repository's root, with the archive outside the repository:

```sh
docker buildx build --platform linux/amd64,linux/arm64 -f deploy/Dockerfile --output type=oci,dest=/tmp/storycreep.tar .
docker build -f deploy/Dockerfile -t storycreep:test .
```

Docker Desktop builds both platforms as it is. Docker Engine may need a builder that can, which `docker buildx create --use --driver docker-container` creates. Then run, in `server/`:

```sh
STORYCREEP_TEST_IMAGE_ARCHIVE=/tmp/storycreep.tar STORYCREEP_TEST_IMAGE=storycreep:test go test ./internal/deploy
```

The compose test publishes the server on `127.0.0.1:7777`, the documented port, or on another port if something already listens there, and removes its containers and volumes when it is done. Every CI run on the main branch, after a push and weekly, publishes the image of its commit to `ghcr.io/cybercute-studio/storycreep`, once its checks pass, tagged `sha-<commit>`, and `main` unless `main` already has a newer commit, as when an older run is re-run. GitHub creates a new package as private, so after the first publish a maintainer makes the package public, once, in its settings on GitHub: until then, `docker-compose.yml` cannot pull the image without signing in.

## Tagging tests with requirements

Every test that proves an acceptance criterion names it, so that the traceability report can map the PRD to the tests. Write a line comment that starts with `Verifies:` directly above the test declaration, with no blank line between them. List acceptance criteria as `FR-…/ACn` and non-functional requirements as `NFR-…`, separated by commas. Spell every ID in full, so that searching for `FR-EDIT-02/AC3` finds every test of that criterion.

Go tests, benchmarks, and fuzz tests, and their subtests (`t.Run` or `b.Run` called on a `*testing.T` or `*testing.B` parameter), in `*_test.go` files. The report names a subtest as `go test` does, such as `TestMoveNode/keeps_connections` (each space character becomes `_`, and a name used again in the same test or subtest is numbered, as `keeps_connections#01`), with `*` for a name that is not a literal:

```go
// TestMoveNode checks that a moved node keeps its connections.
//
// Verifies: FR-EDIT-02/AC3, NFR-PERF-01
func TestMoveNode(t *testing.T) {
```

Vitest and Playwright `test` and `it` declarations with a literal title (a string with nothing added to it, and no `${…}`), in `*.test.ts` and `*.spec.ts` files, and Vitest `bench` declarations in `*.bench.ts` and `*.benchmark.ts` files (or `.tsx`, `.js`, and the other script extensions). A `bench` in a test file, or a `test` or `it` in a benchmark file, is not a test the report can name: Vitest's test run reads only test files, and `bench` runs only in its benchmark run:

```ts
// Verifies: FR-EDIT-02/AC3
test("a moved node keeps its connections", () => {
```

A declaration may carry the modifiers `only`, `skip`, `todo`, `fixme`, `fail`, `fails`, `concurrent`, `sequential`, `shuffle`, `serial`, and `parallel`, as in `test.describe.serial`, however Biome splits it across lines, and Vitest's `skipIf` and `runIf` with their condition, as in `test.runIf(isLinux)("…", …)`. A hook such as `test.beforeEach`, and a declaration with `each` or `for`, is not a test the report can name. A group (`describe`, `test.describe`, or Vitest's `suite`) runs nothing itself, so tag the tests in it; the report fails on a `Verifies:` comment above a group. The report names a test after the groups it is in, as Vitest does: `group > title`, with `*` for a group whose title is not a literal, and nothing for a group without a title, as Playwright's `test.describe(() => {…})`.

GUT tests in `test_*.gd` files:

```gdscript
# Verifies: FR-GODOT-06/AC1
func test_variable_lookup_is_scope_aware():
```

**Skipped tests.** A test declared with `skip`, `todo`, `fixme`, or `fail` (`fails` in Vitest), or with Vitest's option `skip`, `todo`, or `fails` set to `true` (as in `test("…", { skip: true }, …)`), or inside a group declared so whatever its title (as in `describe.skip.each`), does not count: it never runs, or it passes only while the behaviour is wrong. Nor does a test in which Playwright's `test.skip()`, `test.fixme()`, or `test.fail()` is called without arguments or with the condition `true`, as in `test.skip(true, "…")` (in its body, a hook, or a step), or a test inside a group or file in which one is. Nor does a Go test or subtest whose body calls `t.Skip`, `t.Skipf`, or `t.SkipNow` as one of its own statements (not inside an `if`, a loop, or a function literal), as in `t.Skip("flaky")`, or a subtest inside one.

A test skipped in any other way still counts, because the report does not run the code that skips it: `t.Skip` under a condition, as in `if testing.Short() { t.Skip() }`, or in a helper function; GUT's `pending`, and a GUT script that defines `should_skip_script` or sets `skip_script`; Vitest's `skipIf` and `runIf`, and an option set to anything but `true` (as in `{ skip: isCI }`); Playwright's `test.skip()`, `test.fixme()`, or `test.fail()` called with any other condition, as in `test.skip(isMobile, "…")`; and a Go build constraint that CI does not satisfy, as `//go:build integration`. So do not tag a test that CI skips.

**How the report reads the code.** It parses Go files and reads TypeScript, JavaScript, and GDScript files token by token, so a comment marker in a string is never taken for a comment, and a group's tests are the declarations inside its call. It cannot read JSX text that holds a quote, a backtick, `//`, `/*`, or a parenthesis without its partner, as in `<li>a) first</li>`. When that leaves a parenthesis it cannot pair, or a template literal or block comment it cannot close, in a test file that contains `Verifies:`, it fails and says so. It fails the same way on a GDScript multi-line string that never closes. Directories named `testdata`, `node_modules`, `vendor`, `dist`, or `coverage`, and hidden directories, are not scanned, and neither are Go files that the go tool ignores, whose name or directory name starts with `_` or `.`.

**When the report fails.** It fails on a `Verifies:` comment that names an unknown requirement or criterion, names a requirement without a criterion, has an entry that is not an ID (or an empty one, after an extra comma), names an ID twice for one test, is written in a block comment (or a GDScript multi-line string), is outside a test file, or is not directly above a test the report can name (a group is not one). It also fails on a Go file it cannot parse. In a test file, it also fails on:

- A `Verifies:` comment written after code on any line.
- A line comment that is meant as one but is not spelled `Verifies:`, such as `// verifies: FR-EDIT-02/AC3`, `// Verifies FR-EDIT-02/AC3`, `// Verifies — FR-EDIT-02/AC3`, a list item such as `// - Verifies: FR-EDIT-02/AC3`, or a typo such as `// Verifes: FR-EDIT-02/AC3`.
- A `Verifies:` comment above a commented-out test, as when a test is commented out but its tag is not: a comment line below the tag that starts with a test or group declaration, such as `// func TestOld(t *testing.T) {` or `// it("old", () => {`. The tag would otherwise belong to the test below the commented-out one. Comment out the tag with the test, or move it. A comment that mentions a declaration after other words, as in `// Each case runs in t.Run(…)`, is prose.
- A comment line that continues the ID list of the `Verifies:` line above it, such as `// and NFR-PERF-01`; start that line with `Verifies:` too. That includes a line that holds nothing but IDs and the commas or `and` between them, written as a list item or not, among the comment lines directly above a test or a group or around a `Verifies:` line, as where gofmt moves an indented continuation of a doc comment below a blank comment line.

[`docs/configuration.md`](docs/configuration.md) is generated from the key declarations in `server/internal/config/keys.go`, and `go test ./...` fails when it is out of date. After you add or change a configuration key, regenerate it by running `go test ./internal/config -run TestReference -update` in `server/`.

[`docs/error-codes.md`](docs/error-codes.md) is generated the same way, from the registry of error codes in `server/internal/api/errors.go`. After you add or change an error code, regenerate it by running `go test ./internal/api -run TestReference -update` in `server/`.

## Strings and translations

Every user-facing string is a message in a catalog, with a stable key, in ICU MessageFormat (PRD FR-I18N-01). English is the source language: its catalog has every message, and a translation that lacks one shows it in English.

| Catalog | Formatted by | Messages |
| --- | --- | --- |
| [`web/shared/i18n/`](web/shared/i18n/) | i18next with i18next-icu (`@storycreep/shared/i18n`) | The web app's and the public site's. |
| [`server/internal/i18n/catalog/`](server/internal/i18n/catalog/) | The server's own formatter (`server/internal/i18n`) | The server's: the messages of the API's errors, and later its emails. |

Each catalog is a directory with one JSON file per locale, named after its BCP 47 tag, as `en.json`, which holds the messages by key. Name a key after where the message is used, in names of letters, digits, `_`, and `-`, separated by dots, as `error.not_found`.

- **Web.** Show a message with i18next's `t`, called with the key as a string literal, as in `t("board.empty")` or `t("files.count", { count })`, or with react-i18next's `<Trans i18nKey="…">`. Do not name an argument after one of the options that i18next reads itself, such as `lng` or `ns`. `createI18n` makes an i18next instance in a locale, and `resolveLocale` picks the locale: the user's preference, then the browser's languages, then the instance's default, then English (FR-I18N-02). `formatNumber`, `formatDate`, `formatDateTime`, and `formatRelativeTime` format the rest for a locale.
- **Server.** Name a message with `i18n.K("error.not_found")`, with a string literal, in an `i18n.Message` with its arguments, and format it with the request's localizer, `i18n.FromContext`. The API negotiates each request's locale from its `Accept-Language` and `STORYCREEP_DEFAULT_LOCALE`, writes the error envelope's `message` in it, and names it in the `Content-Language` header. The server formats a subset of ICU MessageFormat, which `server/internal/i18n/messageformat.go` lists: arguments, numbers, plurals, selectordinals, and selects, but no dates or times yet. A catalog with anything else does not load, and the server's tests fail.

The vectors in [`schema/i18n/`](schema/i18n/) hold the two formatters to the same output for the same message, so that a translator writes one dialect of ICU for both.

**Extraction.** `pnpm i18n:extract` reads the keys that the code uses, in Go, TypeScript, JavaScript, and the site's Astro components, and rewrites the catalogs to hold exactly those keys, sorted (FR-I18N-01 AC3). The source catalog gains an empty message for a key it lacks, which it reports until you write the message, and every catalog loses the keys that the code no longer uses. It reads a key only from a call with a string literal, and reports any other call of `t` or `i18n.K`, such as one with a variable. While it cannot read every key, as then or when a file does not parse, it rewrites none of that code's catalogs, so that no message of a key it missed is deleted. `go test ./...` fails when a catalog is not as extraction writes it, and `pnpm test` when a web catalog's message does not parse, or a translation uses an argument that its English message does not.

**The lint rule.** A Biome plugin, [`web/shared/lint/hardcoded-text.grit`](web/shared/lint/hardcoded-text.grit), reports text written in the web's source instead of the catalog, and `pnpm check` runs it (FR-I18N-01 AC1): JSX text with a letter in it, a string shown as a JSX child, as in `{"Save"}` or `{done ? "Saved" : t("x")}`, and the value of an attribute that people read, such as `title`, `alt`, `aria-label`, or `placeholder`. Biome reads only the frontmatter of an Astro component, so the rule does not see the text of its template, which a review of the site's pages has to catch. Mark text that is rightly not translated, such as the product's name, with a comment that says why: `{/* biome-ignore lint/plugin: the product's name */}`.

## The API contract

The server's OpenAPI 3.1 document is generated from its huma operations (PRD FR-API-02), served at `/api/v1/openapi.json`, and committed as [`schema/api/v1/openapi.json`](schema/api/v1/openapi.json). The TypeScript types in `web/shared/src/api/v1.gen.ts` are generated from it, and the client in `web/shared/src/api` is typed by them, so nothing describes a request or a response by hand. `go test ./...` fails when either file is out of date, or when a route of the API is not in the document (FR-API-02 AC1). After you add or change an operation, regenerate both by running `go test ./internal/httpserver -run TestOpenAPIDocument -update` in `server/`.

- Register every operation with `api.Register`, which documents the error envelope as its default response. A test provokes errors from every route, and checks that each answer is the envelope that the document describes (FR-API-04 AC1). A route that none of its requests makes fail fails the test, which then needs a request of its own for it.
- `server/internal/apicontract` reads the document. It refuses a keyword or an object of OpenAPI that it does not know, and a keyword beside a `$ref` other than one that only annotates it, which is where huma writes the tags of a field whose type is a struct, such as `default` or `minProperties`. That way nothing in the document escapes the compatibility check. The TypeScript generator refuses what the client cannot express yet, such as a parameter that is not a string, number, or boolean, or a body that is not JSON. When a change needs one of them, teach the package the new case.

`pnpm api:check` compares the document with the one on the main branch, as CI does for every pull request, and fails on a change that breaks a client of `v1` (FR-API-01 AC3). Every request that `v1` accepted must still be accepted, and a response may hold only what `v1` said it would, except that it may gain properties and headers, which clients ignore, and errors may gain statuses, because every error has the envelope. An operation that took neither `If-None-Match` nor `If-Modified-Since` may also gain a 304, which only a request with one of them gets, as when it starts sending an `ETag`. Clients fail on enum values that they do not know, so a value added to a response's enum breaks them. `Breaking` in `server/internal/apicontract/compat.go` lists the rules.

Until the first release, no client depends on `v1`, so a pull request may break it if it says why. Add an `API-Break` trailer to the message of one of its commits, in the last paragraph, with any other trailers:

```text
Remove the commit field from /version

API-Break: nothing reads it, and no client depends on v1 before the first release
```

The check then lists the changes with the reason, and passes. From the first release, plan item 119 compares the document with the release's, and a breaking change needs a new version prefix, `/api/v2`.

## Database migrations

The migrations are SQL files in `server/internal/database/migrations/`, embedded in the binary and applied by [goose](https://github.com/pressly/goose). `storycreep migrate` applies them, as the superuser in `STORYCREEP_DATABASE_ADMIN_URL`, under an advisory lock (PRD FR-ADMIN-04).

- Name a new migration with the next version, as in `00002_workspaces.sql`. Versions count up from `00001` without a gap, and a test fails otherwise. CI tests a pull request merged with the main branch, so if another pull request that adds the same version is merged first, yours fails when its checks run again, and you renumber yours. Before you merge a pull request that adds a migration, run its checks again if the main branch has gained a migration since they last ran: once two migrations with one version are both on the main branch, neither can be renumbered, because a merged migration is never renamed.
- Migrations are forward-only: start the file with `-- +goose Up` and write no `-- +goose Down` section. Each migration runs in its own transaction, so that a failed one leaves no trace, and a test fails on `-- +goose NO TRANSACTION`. A migration is the same on every server, so a test also fails on `-- +goose ENVSUB ON`, which would fill it in from the environment.
- A migration on the main branch is never changed, deleted, or renamed, because no database that applied it would apply the new version. `pnpm migrations:check` fails a branch that does, and CI runs it on every pull request. To change the schema again, add a new migration.
- The superuser that runs the migrations owns the tables, which are in the `public` schema: migrations run with `search_path` set to `public` alone, whatever the admin URL, the login, or the database sets. Grant the server's privileges to the `storycreep_app` role in the migration that creates a table; the server's login has no other privileges, besides `CONNECT` on its database, which migrating grants it.
- A table whose rows belong to a workspace is a tenant table (PRD §5.4, NFR-SEC-01). Give it a non-null `workspace_id` with a foreign key to `workspace` and an index that starts with it, enable and force row-level security, and give it the `workspace_isolation` policy that `00004_tenancy.sql` gives the others. Grant `storycreep_maintenance` `SELECT` on it, as that migration does on the others, because the maintenance jobs read across workspaces. A row that refers to another row of its workspace does so through a foreign key that includes `workspace_id`, so that it cannot refer to another workspace's row. Then add the table to `Tables` in `server/internal/database/isolationtest`, with how to add a row, so that `TestTenantIsolation` tries to read, change, and add its rows from another workspace's transaction. `TestTenantTables` fails on a table that is not a tenant table done this way, unless it is one of the few that span workspaces, which it lists (FR-TEN-01 AC1). Try a repository's own operations with the harness's `Fixture.Check`, which points them at another workspace's rows, as a broken filter would.
- The four functions of the context bootstrap, in `00005_context_bootstrap.sql`, are the only way past row-level security that a request can reach, and the only `SECURITY DEFINER` functions: a test fails on another one, or on one declared in another migration (PRD §5.4).
- The `storycreep_maintenance` role bypasses row-level security, for the closed list of maintenance jobs in PRD §5.4. Only `server/internal/maintenance` may use it: a test fails when another package imports that package, or uses the configuration's `DatabaseMaintenanceURL` or package `database`'s helpers for the maintenance login outside the packages that provision the login. The role may read every tenant table, as above, and the accounts and workspaces; grant it what else a maintenance job needs, such as deleting rows, in the migration that adds the job.
- A view reads its tables with its owner's privileges, and row-level security never holds the superuser that owns it, so the server would see every workspace's rows through it. Create every view `WITH (security_invoker = true)`, so that it reads them with the privileges of whoever queries it, and no materialized view, which row-level security never applies to. A test fails otherwise.
- After you change a migration or a query in `server/internal/database/queries/`, run `go generate ./internal/database` in `server/` to regenerate the code that sqlc builds from them.
- The job queue's tables belong to [River](https://riverqueue.com), and come from River's own migrations, copied into ours (`00002_river_2_to_4.sql` and `00003_river_5_to_8.sql`, which say how). When you upgrade River and it adds a migration, copy the migration into a new migration of ours in the same way, and grant `storycreep_app` the privileges on any table it creates. `TestRiverSchema` in `server/internal/jobs` fails until the schema our migrations build is the one River's migrator builds, and `TestRiverPrivileges` until the grants are there.

Database tests get their own database from `dbtest.New(t)`, in `server/internal/database/dbtest`, which copies a template with the migrations applied, and connect to it as the superuser (`AdminURL`), the application login (`AppURL`), or the maintenance login (`MaintenanceURL`). A package whose tests use it runs them with `os.Exit(dbtest.Run(m))` in its `TestMain`, which keeps the libpq environment variables out of the tests and removes the PostgreSQL container when they are done. Two environment variables configure it: `STORYCREEP_TEST_POSTGRES_IMAGE` picks the image (the default is `postgres:17-alpine`), and `STORYCREEP_TEST_UPGRADE_FROM` names, as an absolute path, a directory of older migrations that the template is migrated with first. CI's upgrade test sets it to the main branch's migrations and runs the whole suite on the upgraded database (FR-ADMIN-04 AC2).

## Conventions

- Text files use LF line endings everywhere. `.gitattributes` enforces this, and `.editorconfig` sets the indentation: two spaces, except tabs for Go and GDScript.
- Every user-facing string is a message in a catalog (see [Strings and translations](#strings-and-translations)), and every new endpoint is in the OpenAPI document. The plan's standing checklist lists the rest.
- Register every route with `api.Register`, or `Mux.Handle` for a handler that is not a huma operation, or `Mux.HandlePage` for a page of the web interface, which the OpenAPI document leaves out, in `server/internal/api`, which records the capability the route requires. A test walks the routers and fails on a route that declares none (PRD NFR-SEC-02).
- A handler reports an error by returning an `*api.Error` with a code from the registry, which the client receives in the error envelope. Any other error is answered with `internal_error`, and logged with the request ID, so its text never reaches the client.
- Outside tests, only `server/internal/config` reads the environment or knows the deployment mode (`STORYCREEP_MODE`). Code that behaves differently in the two modes reads `config.Capabilities`. A test fails on non-test code in any other package that reads an environment variable, names `STORYCREEP_MODE`, or uses a mode value (PRD FR-ADMIN-01 AC3). Test files are exempt, because they set the mode up, and so are test-support packages: a package whose name ends in `test`, such as `dbtest`, and that only test files and other test-support packages import, so that it is not part of the binary.
- Secrets are held as `secret.Value` (`server/internal/secret`), which prints, logs, and encodes as `[REDACTED]`. Call `Reveal` only where the secret is used.
- Background work is a job of the queue in `server/internal/jobs` (PRD FR-JOBS-01). Register the worker of a kind of job with `jobs.Register`, and enqueue a job with `Queue.Enqueue` in the transaction of the change that needs it, so that the job runs only if the change commits. The queue's tables span workspaces and have no row-level security, so a job's arguments hold identifiers, never content, and its work reads and writes a workspace's rows in a transaction of that workspace, on the workers' pool that the job carries (`job.DB.InTx`). A job can run more than once, as when the instance running it is killed, so its work must be safe to repeat. When the server stops, it cancels the context of the jobs that have not finished in time, and a job whose work returns that context's error, or wraps it with `%w`, is attempted again without the attempt counting.
- Record each security- or tenancy-relevant event (PRD FR-TEN-07) with `audit.Record` (`server/internal/audit`), in the transaction of the change that it records, so that the entry commits only with the change. Register a new event's action in `server/internal/audit/events.go`, with whether it belongs to a workspace, the kind of entity that it is about, its results, and its metadata keys, which `Record` holds entries to, and record it in the pull request that adds the feature, with a test that produces it (FR-TEN-07 AC2). An entry names people by ID only, never by email address or display name (§9.6). An event that belongs to no workspace, such as a login, is recorded in a transaction without one, and an event that is a refusal, such as a failed login, changes nothing else, so its transaction still commits.

- The plans of the hosted service, their limits, and their feature flags live in one file, [`server/internal/entitlements/plans.json`](server/internal/entitlements/plans.json), which the server embeds and the pricing page reads (PRD FR-ENT-01). Its numbers are placeholders until the hosted service's prices are set, so no code or test names them: a test reads them from the file. A change that adds to a workspace's usage of a limit checks it once, with `entitlements.Check` in its transaction, at the limit's one enforcement site, which has a test that reaches the limit (FR-ENT-01 AC1), and its route answers the `*entitlements.LimitError` with `limitExceededError` (`server/internal/httpserver/entitlements.go`), which is 409 `limit_exceeded` with the limit's key, usage, and limit (FR-ENT-03 AC1). A limit that the plan file marks `inert_until` is checked by no code until the requirement that it names.

## Security issues

Do not report vulnerabilities in public issues. See [`SECURITY.md`](SECURITY.md).
