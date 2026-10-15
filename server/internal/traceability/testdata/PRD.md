# Fixture PRD

A minimal PRD in the shape of `docs/PRD.md`, for the traceability tests.

## 4. Functional requirements

#### FR-A-01 — First
- **Priority:** Must · **Phase:** MVP · **Depends on:** —
- **Acceptance criteria:**
  - AC1 — It works.
  - AC2 — It works twice.

#### FR-A-02 — Second, with `code`
- **Priority:** Must · **Phase:** MVP · **Depends on:** FR-A-01
- **Acceptance criteria:** AC1 — It is inline. AC2 — It is inline too.

#### FR-A-03 — Third
- **Priority:** Must · **Phase:** MVP · **Depends on:** FR-A-01
- **Acceptance criteria:**
  - AC1 — It is played.

#### FR-B-01 — Polish
- **Priority:** Should · **Phase:** v1 · **Depends on:** FR-A-03
- **Acceptance criteria:**
  - AC1 — It shines.
  - AC2 — It shines brighter.

#### FR-C-01 — Hosted only
- **Priority:** Won't (MVP) · **Phase:** SaaS · **Depends on:** FR-B-01
- **Acceptance criteria:**
  - AC1 — It is hosted.

## 9. Non-functional requirements

| ID | Requirement |
| --- | --- |
| NFR-PERF-01 | Fast |
| NFR-Q-02 | Traced |

## 12. Release plan

### 12.1 Phase 0 — MVP

**In scope.** FR-A-01…03.

**Exit criteria.**
1. Performance target NFR-PERF-01 met.

### 12.2 Phase 1 — v1

**In scope.** FR-B-01.

## Appendix B — Requirement index by phase

**MVP.** FR-A-01…03

**v1.** FR-B-01

**Hosted SaaS.** FR-C-01
