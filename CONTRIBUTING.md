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
| `pnpm docs:check` | The PRD dependency graph and acceptance criteria, and the plan's ordering, coverage, and reference rules. |
| `pnpm traceability` | The requirement traceability report (PRD NFR-Q-02): which tests verify each requirement, and which MVP acceptance criteria no test verifies yet. It writes `traceability-report/traceability.md` and `traceability.json`, and fails on a tag it cannot use (see below). A criterion without a test does not fail the build until the MVP release gate (plan item 119). CI publishes the report as the `traceability-report` artifact. |
| `pnpm audit --audit-level high`, and `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` in `server/` | Known vulnerabilities in the dependencies. CI runs these in a separate job, on every pull request and weekly. |

`pnpm docs:check` runs `go run ./cmd/doccheck` in `server/`, and `pnpm traceability` runs `go run ./cmd/traceability`. Both report every problem as `file:line: message`. Run `go run ./cmd/traceability` in `server/` to print the report to the terminal instead.

## Tagging tests with requirements

Every test that proves an acceptance criterion names it, so that the traceability report can map the PRD to the tests. Write a line comment that starts with `Verifies:` directly above the test declaration, with no blank line between them. List acceptance criteria as `FR-…/ACn` and non-functional requirements as `NFR-…`, separated by commas. Spell every ID in full, so that searching for `FR-EDIT-02/AC3` finds every test of that criterion.

Go tests, benchmarks, and fuzz tests, and their subtests (`t.Run` or `b.Run` called on a `*testing.T` or `*testing.B` parameter), in `*_test.go` files. The report names a subtest as `go test` does, such as `TestMoveNode/keeps_connections` (each space character becomes `_`), with `*` for a name that is not a literal:

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

**Skipped tests.** A test declared with `skip`, `todo`, `fixme`, or `fail` (`fails` in Vitest), or with Vitest's option `skip`, `todo`, or `fails` set to `true` (as in `test("…", { skip: true }, …)`), or inside a group declared so whatever its title (as in `describe.skip.each`), does not count: it never runs, or it passes only while the behaviour is wrong. Nor does a test in which Playwright's `test.skip()`, `test.fixme()`, or `test.fail()` is called without arguments (in its body, a hook, or a step), or a test inside a group or file in which one is.

A test skipped in any other way still counts, because the report does not run the code that skips it: `t.Skip`; GUT's `pending`, and a GUT script that defines `should_skip_script` or sets `skip_script`; Vitest's `skipIf` and `runIf`, and an option set to anything but `true` (as in `{ skip: isCI }`); and Playwright's `test.skip()`, `test.fixme()`, or `test.fail()` called with a condition, as in `test.skip(isMobile, "…")`. So do not tag a test that CI skips.

**How the report reads the code.** It parses Go files and reads TypeScript, JavaScript, and GDScript files token by token, so a comment marker in a string is never taken for a comment, and a group's tests are the declarations inside its call. It cannot read JSX text that holds a quote, a backtick, `//`, `/*`, or a parenthesis without its partner, as in `<li>a) first</li>`. When that leaves a parenthesis it cannot pair, or a template literal or block comment it cannot close, in a test file that contains `Verifies:`, it fails and says so. It fails the same way on a GDScript multi-line string that never closes. Directories named `testdata`, `node_modules`, `vendor`, `dist`, or `coverage`, and hidden directories, are not scanned.

**When the report fails.** It fails on a `Verifies:` comment that names an unknown requirement or criterion, names a requirement without a criterion, names an ID twice for one test, is written in a block comment (or a GDScript multi-line string), is outside a test file, or is not directly above a test the report can name (a group is not one). It also fails on a Go file it cannot parse. In a test file, it also fails on:

- A `Verifies:` comment written after code on any line.
- A line comment that is meant as one but is not spelled `Verifies:`, such as `// verifies: FR-EDIT-02/AC3`, `// Verifies FR-EDIT-02/AC3`, `// Verifies — FR-EDIT-02/AC3`, or a typo such as `// Verifes: FR-EDIT-02/AC3`.
- A comment line that continues the ID list of the `Verifies:` line above it, such as `// and NFR-PERF-01`; start that line with `Verifies:` too. That includes a line that holds nothing but IDs and the commas or `and` between them, written as a list item or not, among the comment lines directly above a test or around a `Verifies:` line, as where gofmt moves an indented continuation of a doc comment below a blank comment line.

## Conventions

- Text files use LF line endings everywhere. `.gitattributes` enforces this, and `.editorconfig` sets the indentation: two spaces, except tabs for Go and GDScript.
- Every user-facing string goes through the i18n layer, and every new endpoint is in the OpenAPI document. The plan's standing checklist lists the rest.

## Security issues

Do not report vulnerabilities in public issues. See [`SECURITY.md`](SECURITY.md).
