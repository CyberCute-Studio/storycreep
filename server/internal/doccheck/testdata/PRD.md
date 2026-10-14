# Fixture PRD

A minimal PRD in the shape of `docs/PRD.md`. The tests in `doccheck_test.go` break it one rule at a time.

## 4. Functional requirements

### 4.0 Conventions

A fenced example is never read as a requirement:

```
#### FR-Z-99 — Not a requirement
- **Priority:** Must · **Phase:** MVP · **Depends on:** FR-Q-01
```

### 4.1 Area A (FR-A)

#### FR-A-01 — First
- **Priority:** Must · **Phase:** MVP · **Depends on:** —
- **Acceptance criteria:** AC1 — It works.

#### FR-A-02 — Second
- **Priority:** Must · **Phase:** MVP · **Depends on:** FR-A-01

#### FR-A-03 — Third
- **Priority:** Must · **Phase:** MVP · **Depends on:** FR-A-01, FR-A-02

### 4.2 Area B (FR-B)

#### FR-B-01 — Polish
- **Priority:** Should · **Phase:** v1 · **Depends on:** FR-A-03

#### FR-B-02 — Hosted only
- **Priority:** Won't (MVP) · **Phase:** SaaS · **Depends on:** FR-B-01

## 9. Non-functional requirements

### 9.1 Performance

| ID | Target |
| --- | --- |
| NFR-PERF-01 | Fast |
| NFR-PERF-02 | Faster |
| NFR-PERF-03 | Not an exit criterion |

### 9.2 Accessibility

| ID | Requirement |
| --- | --- |
| NFR-A11Y-01 | Keyboard |
| NFR-A11Y-02 | Contrast |

## 11. Architecture decision records

### ADR-001 — A decision

## 12. Release plan

### 12.1 Phase 0 — MVP

**In scope.** FR-A-01…03.

**Exit criteria.**
1. Performance targets NFR-PERF-01…02 met on the reference devices.

### 12.2 Phase 1 — v1

**In scope.** FR-B-01 · keyboard remapping.

## Appendix B — Requirement index by phase

**MVP.** FR-A-01…03

**v1.** FR-B-01

**Hosted SaaS.** FR-B-02
