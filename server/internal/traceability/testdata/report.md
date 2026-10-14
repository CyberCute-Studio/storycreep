# Requirement traceability

3 of 5 acceptance criteria of the MVP have a test (60.0%), and 2 have none. 7 tests are tagged.

## Acceptance criteria without a test

- FR-A-01 First: AC2
- FR-A-02 Second, with `code`: AC2

## Verified acceptance criteria

- FR-A-01/AC1: `server/sample_test.go:8` `TestSample`; `web/app/e2e/sample.spec.ts:4` `a playwright group`
- FR-A-02/AC1: `web/app/src/sample.test.ts:4` `a vitest test`
- FR-A-03/AC1: `godot/test/test_sample.gd:5` `test_sample`
- FR-B-01/AC1 (v1): `server/sample_test.go:14` `TestTable/polished_case`

## Verified non-functional requirements

- NFR-PERF-01: `server/sample_test.go:18` `BenchmarkSample`
- NFR-Q-02: `web/app/src/sample.test.ts:4` `a vitest test`

## Skipped tests

These tests verify nothing: each is declared with skip, todo, fixme, or fail, or inside a group declared so, or is a group whose tests all are.

- `web/app/src/sample.test.ts:9` `a skipped test`: FR-A-02/AC2
