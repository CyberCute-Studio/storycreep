# Fixture plan

A minimal plan in the shape of `docs/IMPLEMENTATION_PLAN.md`, consistent with `PRD.md` next to it.

## How to use and edit this document

### Item format

```
### N. Title
**Status:** todo | in progress | done (#PR link) | dropped · **Depends on:** earlier item numbers
**PRD:** FR-Q-01
```

### Rules for every edit

1. **Ordering.** A suffixed number sorts directly after its base number: 45 < 45a < 45b < 46.
2. **Numbering.** Item 1's check reads these rules.

---

## Phase 0 — MVP

### Milestone A — Foundation

### 1. Scaffold
**Status:** done (#1) · **Depends on:** —
**PRD:** FR-A-01, NFR-A11Y-01/02, ADR-001, §9.9
- A reference inside code, `item 999`, is ignored.

**Done when:** it builds.

### 2. Second
**Status:** in progress · **Depends on:** 1
**PRD:** FR-A-02, FR-A-01 (AC2)
- Builds on item 1, and on items 2a (later) and 4.

### 2a. Inserted
**Status:** todo · **Depends on:** 1, 2
**PRD:** FR-A-02
- Extends the parser of item-2.

### 3. Dropped
**Status:** dropped (merged into 2a) · **Depends on:** 1
**PRD:** FR-A-03

### 4. Fourth
**Status:** todo · **Depends on:** 1–2a
**PRD:** FR-A-03, NFR-PERF-01..02
- Completes the work of items 2–2a (see the parser (2a)).

## Phase 1 — v1

### 5. Later
**Status:** todo · **Depends on:** 1–4
**PRD:** FR-B-01, FR-A-03 (extension), §12.2 "keyboard remapping"

---

## Coverage matrix

| Requirement | PRs | Complete at |
| --- | --- | --- |
| FR-A-01 | 1, 2 | 2 |
| FR-A-02 | 2, 2a | 2a |
| FR-A-03 | 4 (5) | 4 |
| FR-B-01 | 5 | 5 |
| §12.2 keyboard remapping | 5 | 5 |

### Performance targets

| Target | Measured by |
| --- | --- |
| NFR-PERF-01, 02 (fixture targets) | 4, with more benchmarks later |
| NFR-PERF-03 (not an exit criterion) | 5 |

## Alignment notes

| # | Note | Status | Affects |
| --- | --- | --- | --- |
| N1 | Decided while writing item 2. | Decided | 2, 4 |
| N2 | Nothing to change. | Fixed | — |

## Change log

| Plan version | Date | Change |
| --- | --- | --- |
| 1.0 | 2026-10-14 | Item 3 was dropped and its scope moved to item 2a. Item 77 never existed. |
