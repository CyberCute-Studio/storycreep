# Schema

Everything that must agree between Go, TypeScript, and GDScript: the published JSON Schemas, fixtures for each schema version, the expression corpus, and the conformance suite (PRD §10.7, §6, §8.6). CI generates or verifies each of them against the implementations.

- [`api/v1/openapi.json`](api/v1/openapi.json) is the OpenAPI document of the REST API, which the server generates from its routes and serves at `/api/v1/openapi.json`. The TypeScript client's types are generated from it; see "The API contract" in [`CONTRIBUTING.md`](../CONTRIBUTING.md).
- [`ids/`](ids/) holds the vectors of the identifier encoding.
