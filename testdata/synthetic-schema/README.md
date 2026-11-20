# The synthetic schema version

A test-only version of the published game format (PRD FR-DATA-03, §6.4, plan notes N2 and N84). Only version 1 of the format exists, but the criteria of a migration, of down-rendering, and of a pull between a server and an addon of different versions need two. This version is the second. It is defined only in test code and in this directory, which is outside the published `/schema` tree. No release build or packaged addon has it. No server serves it yet: only the Go tests that import `schemaversion/synthetic` read it, and the test server that serves it to the addon comes with its pull (plan item 101).

## Its number

It is numbered one above the newest real version, so it is 2 today. A real version never takes its number: before a real version 2 lands, the synthetic version becomes 3, and this directory's `published/v2` and `fixtures/v2` become `v3`. In Go, its number is `published.SchemaVersion + 1` (`synthetic.Number`), and `TestSyntheticVersion` fails until the directories match it. In TypeScript, `register.ts` names it as `SYNTHETIC_VERSION`, with the paths of its schemas.

## How it differs from version 1

It differs only in what its schemas take, not in what a runtime does with a snapshot. So a runtime reads its files as it reads version 1's, and registering it in an implementation means giving its number and its schemas:

| | Version 1 | The synthetic version |
| --- | --- | --- |
| Every file's `schemaVersion` | 1 | 2 |
| A node's `position` | `x` and `y` to hundredths | `x` and `y` in whole units, integers |
| A node's `title` | at most 200 characters | at most 400 characters |

- **The migration** from version 1 rounds each position that is not whole, half away from zero, as 1.5 to 2 and −0.05 to 0. It reports each one in the migration log with its value before and after, so it loses no value silently (FR-DATA-03 AC2).
- **The renderer** down to version 1 fails on a title longer than 200 characters, which version 1 cannot express, and names each such node. It never writes a title that version 1's schema refuses (AC3). Every whole position is a position of version 1.

## What is here

- `published/v2/`: the version's JSON Schemas, one for each file of a snapshot and one for an AST, as `/schema/published/v1` holds version 1's. Package `schemaversion/synthetic` generates them from version 1's (`Documents`). Each `$id` is the URL of its file here, the examples are left out, and the differences above are applied.
- `fixtures/v2/`: the version's fixture project, the migration of `/schema/fixtures/v1` to it. Its positions are whole, and its migration back from version 1's rendering of it reports nothing.
- `register.ts`: the TypeScript tests' registration hook. Importing it registers the version with play mode's loader (`registerSchemaVersion` of `web/shared/src/flow/versions.ts`). Only tests import it, and a test of `web/shared` fails if any other file of the web packages does. The Godot addon's hook joins it with plan item 94.

## The Go side

The version's migration, renderer, and validator are Go code in `server/internal/schemaversion/synthetic`, because a Go module compiles only the code under its own directory. Every file of that package needs the `synthetic_schema` build tag, so a build without the tag, as every release is, cannot import it. Importing it registers the version (`schemaversion.Register`, which exists only with the tag). The default test run has no tag, so most tests exercise the release configuration with version 1 as the newest. Only the tests of the two-version criteria run with the tag: those of the registry, and those of the exports of package `snapshot`, which render a snapshot stored at this version down to version 1 (PRD FR-SYNC-01 AC2):

```sh
go test -tags synthetic_schema ./internal/schemaversion/... ./internal/snapshot/...
```

Running those of `./internal/schemaversion/...` with `-update`, in `server/`, rewrites `published/v2/` and `fixtures/v2/` after a change of version 1's types or of the migration. CI runs them on every operating system, and the database's with each PostgreSQL version.
