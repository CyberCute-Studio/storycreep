# Schema

Everything that must agree between Go, TypeScript, and GDScript: the published JSON Schemas, fixtures for each schema version, the expression corpus, and the conformance suite (PRD §10.7, §6, §8.6). CI generates or verifies each of them against the implementations.

- [`api/v1/openapi.json`](api/v1/openapi.json) is the OpenAPI document of the REST API, which the server generates from its routes and serves at `/api/v1/openapi.json`. The TypeScript client's types are generated from it; see "The API contract" in [`CONTRIBUTING.md`](../CONTRIBUTING.md).
- [`conformance/`](conformance/) holds the conformance suite: snapshots of the published format, and scenarios of a runtime's flow through them, the events that each call emits and what a game sees after it, which every runtime runs (PRD §8.6, FR-PLAY-06).
- [`expr-corpus/`](expr-corpus/) holds the expression corpus: cases of the expression language's operations, as of its numbers and strings, which every evaluator runs, and of its parser, the AST or the error that each source gives, which every parser runs.
- [`ids/`](ids/) holds the vectors of the identifier encoding.
- [`published/`](published/) holds the JSON Schemas of the published game format, one of each file of a snapshot and one of an expression's AST, by the format's version, which the server generates from its Go types, and the web app's TypeScript types are generated from (PRD §6, FR-DATA-04).
- [`i18n/`](i18n/) holds the vectors of the message format, which the web app's i18next and the server's formatter both run.
- [`save-state/`](save-state/) holds the JSON Schema of the save state that a runtime saves and loads (PRD §8.5), which the server generates alike.
- [`rich-text/`](rich-text/) holds the JSON Schema of the rich-text documents of node bodies, connection labels, and component descriptions, which the server generates, and the vectors of their validation and of the segments, plain text, preview, word count, and hash derived from them.
- [`unicode/`](unicode/) holds the tables of Unicode that the expression language's `upper`, `lower`, and `trim` read, which the server generates from the files of the Unicode Character Database that it holds too.

The plans of the hosted service, which the server enforces and the pricing page shows, are not here: they live in [`server/internal/entitlements/plans.json`](../server/internal/entitlements/plans.json), because the server embeds the file, and a Go module embeds only the files under its own directory (PRD FR-ENT-01 AC3).
