# Storycreep — Implementation Plan

| Field | Value |
| --- | --- |
| Plan version | 1.16 |
| Date | 2026-10-17 |
| Source | [`PRD.md`](PRD.md) version 1.13 (2026-10-17) |
| Scope | Phase 0 — MVP (§12.1), then Phase 1 — v1 (§12.2) |
| Status | Living document |

This is the ordered list of pull requests that implements the PRD. Each numbered item is one PR, and the list order is the implementation order. An item depends only on items with smaller numbers.

The PRD is the contract. This plan only decides how the PRD is cut into PRs and in what order. If the plan and the PRD disagree, the PRD wins until it is amended.

---

## How to use and edit this document

### Item format

```
### N. Title
**Status:** todo | in progress | done (#PR link) | dropped · **Depends on:** earlier item numbers
**PRD:** requirement IDs and sections this PR implements (a slice is named in parentheses)
- Scope bullets: what the PR contains.
**Done when:** the acceptance criteria that must have passing, traceability-tagged tests in this PR.
```

`Depends on` lists only direct prerequisites. Transitive ones are implied.

The PR that implements an item updates the item's `Status` itself. Once the PR is open and has a number, push a commit to it that sets the status to `done` with a link to the PR, such as `done ([#12](https://github.com/CyberCute-Studio/storycreep/pull/12))`. The status then changes on the main branch exactly when the PR merges, and a PR that is closed without merging leaves it unchanged. Do not leave the status for a follow-up after the merge, because nothing reminds anyone to make it.

### Slices

Many FRs span the server, the web app, and the Godot addon. The PRD's own package plan (§12.6) splits them the same way: the server narrative model (P4) lands before the canvas (P8), even though FR-EDIT-02 depends on FR-EDIT-01. This plan names a slice after the FR, for example "FR-EDIT-02 (server)".

The PRD dependency graph (§4.0) is honoured **slice-wise**: before a PR lands, every part of every prerequisite FR that the PR actually uses must already be merged. The coverage matrix at the end lists every PR that touches each FR. The last of them completes the FR.

### Rules for every edit

Any change to this document (adding, removing, splitting, merging, or reordering items, or changing scope) must keep all of the following true in the same edit:

1. **Ordering.** Every number in `Depends on` is smaller than the item's own number. A suffixed number (rule 6) sorts directly after its base number: 45 < 45a < 45b < 46.
2. **PRD dependencies.** For every FR slice an item implements, the PRD's `Depends on` FRs are delivered, at least in the parts used, by items in its dependency closure. Re-check this whenever scope moves between items.
3. **Coverage.** Every FR in Appendix B of the PRD for MVP and v1, and every non-FR item in §12.2, appears in the coverage matrix with at least one item. Every performance target in the §12.1 exit criteria (NFR-PERF-01…15) appears in the performance table with the item that builds its measurement. Both tables are updated in the same edit.
4. **Phase scope.** MVP scope is fixed by PRD §12.1. Moving an MVP requirement to a later phase needs a PRD amendment first. Pulling a v1 item into the MVP is allowed and is recorded in the change log.
5. **PRD first.** If implementation shows that a requirement is wrong or unbuildable, amend the PRD (with a document-history row) before changing this plan. Do not quietly diverge here.
6. **Numbering.** Item numbers are stable. A new item takes the number of the item it follows plus a letter suffix (45a, then 45b), so no existing number or reference changes. A removed item stays in place with status `dropped` and a one-line reason, and nothing may depend on it. A full renumbering is allowed only as an edit of its own that changes nothing else. Item 1's plan check fails on any reference (in `Depends on`, in the text, or in the matrices) to a number that does not exist or to a dropped item.
7. **Change log.** Every edit adds a row to the change log at the end, except an item's status update made by the PR that implements it (item format). Leaving those out keeps PRs that land in parallel from conflicting over the change log.

Rules 1, 3, and 6 are checked mechanically in CI (item 1). The coverage matrix is derived from the **PRD:** lines: an item touches an FR exactly when its PRD line names it, so a change to a PRD line and the matching matrix row land together.

### Standing checklist for every PR

These restate the PRD's standing criteria (§4.0, SC1–SC5) together with the plan-level obligations that later items depend on:

- **SC1–SC5:** mutations go through the command pipeline and are authorised. Tenant data is workspace-scoped and RLS-protected. Strings are externalised. New endpoints are in OpenAPI and have a real-PostgreSQL integration test. There are no outbound network calls.
- **Plan status:** the PR sets its item's status to `done` with a link to itself, in a commit pushed once the PR is open (item format).
- **Traceability:** every test that proves an acceptance criterion is tagged with its ID, such as `FR-EDIT-02/AC3` (item 2).
- **Commands:** a new command type is registered with capability, limit keys, touched entity kinds, and an inverse, or it is marked non-undoable. It joins the undo property test (item 37).
- **Permissions:** a new operation from PRD §5.6 adds its rows to the permission-matrix test (item 20). A workspace-scoped operation also joins the guest test of FR-TEN-04 AC4 (item 25), and a mutating route joins the viewer test of FR-TEN-03 AC3. Item 117 checks that both tests cover every MVP operation.
- **Routes:** a new route declares its capability and is classified as token-capable (with scope) or session-only (item 27).
- **Tables:** a new tenant table has `workspace_id`, RLS, and an isolation test (item 11). It is covered by project hard deletion (item 24) and workspace hard deletion (item 110 onward). It is registered with trash if users can delete it (item 31). If it holds authoring data, it is registered in both directions of the authoring format, export (item 83) and import (item 84), in the same PR; project duplication (item 85) and workspace export (item 86) reuse those registrations. Item 84's registry test fails on a table registered in only one direction.
- **Audit:** events listed in FR-TEN-07 that the PR produces are emitted in the same transaction (item 12).
- **Docs:** user-visible behaviour updates the docs (item 109 onward).

---

## Phase 0 — MVP

### Milestone A — Foundation

### 1. Monorepo scaffold, CI matrix, and project policies
**Status:** done ([#1](https://github.com/CyberCute-Studio/storycreep/pull/1)) · **Depends on:** —
**PRD:** §10.7, §9.9, NFR-Q-07, ADR-011, NFR-SEC-16 (partial), NFR-SEC-17, §4.0 documentation check
- Repository layout from §10.7: `/server` (Go module), `/web/app`, `/web/site`, `/web/shared` (pnpm workspaces, §10.9), `/godot`, `/schema`, `/deploy`, `/docs`.
- Biome for both bundles, `.editorconfig`, `.gitattributes` forcing LF.
- GitHub Actions on `ubuntu-latest`, `windows-11-arm`, and `macos-latest`. No x86-only or WSL-only tools.
- Licences: AGPL-3.0-only at the root, MIT under `/godot/addons/storycreep`. `CONTRIBUTING.md` with the CLA explanation and CLA bot configuration. `SECURITY.md` with a 90-day disclosure policy.
- Docs check in CI: every PRD "Depends on" field references a known ID and the graph is acyclic (§4.0). Plan check: every `Depends on` here is smaller than its item number (rule 1), every MVP and v1 FR in PRD Appendix B appears in the coverage matrix, the matrix equals the one derived from the items' **PRD:** lines, every NFR-PERF-01…15 target has an owning item in the performance table, and every item reference resolves to an existing, non-dropped item (rule 6).
- `pnpm audit` and `govulncheck` in CI (NFR-SEC-16). Scanning is completed in item 117.

**Done when:** CI is green on all three OSes. The docs and plan checks fail on a fixture with a cycle, an unknown ID, a forward dependency, a missing FR, a missing performance target, a matrix row that disagrees with the item lines, or a reference to a missing or dropped item.

### 2. Requirement traceability report
**Status:** done ([#4](https://github.com/CyberCute-Studio/storycreep/pull/4)) · **Depends on:** 1
**PRD:** NFR-Q-02, M-Q-01
- A tagging convention for Go tests, Vitest, Playwright, and GUT that names `FR-…/ACn` and `NFR-…` IDs.
- A generator that maps requirement → tests and lists the acceptance criteria of the current phase that no test covers. It is published as a CI artifact.
- Report-only until item 119, which makes a gap fail the build.

**Done when:** the report lists a tagged sample test and shows every other MVP AC as a gap.

### 3. Configuration, capabilities, logging, and CLI frame
**Status:** done ([#3](https://github.com/CyberCute-Studio/storycreep/pull/3)) · **Depends on:** 1
**PRD:** FR-ADMIN-01 (AC1, AC3), §10.3, FR-ADMIN-06 (frame), NFR-OBS-01, NFR-SEC-14 (redaction)
- Configuration from environment variables plus an optional file. Every key is documented with its default and validated at startup.
- `STORYCREEP_MODE` is resolved once into the capabilities struct (§10.3). A lint test asserts it is read in exactly one package.
- `STORYCREEP_PUBLIC_URL` is parsed here, because cookies (item 14) and HSTS depend on it.
- `log/slog` JSON logging with request, workspace, and user fields. A secret type that always redacts.
- A single binary with a subcommand frame, `--help`, non-zero exit codes, and `validate-config`.

**Done when:** FR-ADMIN-01 AC1 and AC3 pass, and `validate-config` reports the offending key.

### 4. Identifier encoding (Go and TypeScript)
**Status:** done ([#2](https://github.com/CyberCute-Studio/storycreep/pull/2)) · **Depends on:** 1
**PRD:** §5.2, ADR-021
- UUIDv7 generation. Prefixed Crockford base32 encode and decode, with prefix validation per entity type.
- The same utility in Go and in `/web/shared`, tested against shared vectors in `/schema/ids/`.

**Done when:** both implementations pass the shared vectors. A wrong prefix is a typed decode error (the API maps it to 400 in item 6).

### 5. PostgreSQL foundation
**Status:** done ([#6](https://github.com/CyberCute-Studio/storycreep/pull/6)) · **Depends on:** 3
**PRD:** FR-ADMIN-04 (AC2, pre-release form), FR-ADMIN-06 (`migrate`), ADR-001, ADR-002, NFR-Q-01, §9.9
- `pgx/v5` pool and `sqlc` setup. `goose` migrations embedded in the binary and applied under an advisory lock, by the `migrate` subcommand and by the startup step that `serve` runs. Item 6 adds `serve` and its `--no-auto-migrate`.
- Database roles: migration owner, application role (not a table owner), owner of the `SECURITY DEFINER` functions, and `storycreep_maintenance` (`BYPASSRLS`). Migrations run as the superuser in `STORYCREEP_DATABASE_ADMIN_URL` and provision the application login of `STORYCREEP_DATABASE_URL`, and the server refuses an application login that row-level security would not hold (N16).
- Request transaction helper that sets `SET LOCAL app.workspace_id` and `app.user_id`.
- `testcontainers-go` harness running on PostgreSQL 15, 16, and 17.
- An upgrade-test job that seeds a database from the main branch's migrations, applies the pull request's migrations, and runs the test suite (FR-ADMIN-04 AC2 before the first release). Item 119 switches the seed to the previous release.
- A migration-immutability check: a pull request that changes or deletes a migration file already on the main branch fails CI.

**Done when:** migrations run once under the advisory lock on all three PostgreSQL versions, and FR-ADMIN-04 AC2 passes in its pre-release form, including the immutability check failing on a fixture that edits a merged migration. AC1 and AC3 need the readiness endpoint and land in item 6.

### 6. HTTP server skeleton and operational endpoints
**Status:** done ([#7](https://github.com/CyberCute-Studio/storycreep/pull/7)) · **Depends on:** 4, 5
**PRD:** FR-ADMIN-05, FR-ADMIN-04 (AC1, AC3), FR-ADMIN-06 (`serve`), FR-API-01 (AC2), FR-API-04 (envelope, pagination), NFR-SEC-02, NFR-SEC-07, NFR-SEC-09, FR-PUB-02 (AC4), §10.5
- `chi` and `huma` v2 with the `serve` subcommand. Request IDs. `serve` runs item 5's database startup step, which applies pending migrations unless `--no-auto-migrate` is given.
- Error envelope and a registry of error codes. Display messages are wired to i18n in item 10.
- Strict JSON decoding, body-size and depth limits, keyset-cursor pagination helper, and an `ETag` helper.
- Security headers: CSP, `nosniff`, Referrer-Policy, `frame-ancestors 'none'`, and HSTS in hosted mode.
- Every route declares a capability. A test fails on any route that does not.
- `/healthz`, `/readyz` (503 while migrations are pending), `/version`, and `/metrics` (protected in hosted mode). `X-Robots-Tag: noindex` on `/app/*`.

**Done when:** FR-ADMIN-05 AC1 and AC3, FR-ADMIN-04 AC1 and AC3 (on all three PostgreSQL versions), FR-API-01 AC2, FR-API-04 AC2, and FR-PUB-02 AC4 pass.

### 7. API contract tooling
**Status:** done ([#9](https://github.com/CyberCute-Studio/storycreep/pull/9)) · **Depends on:** 6
**PRD:** FR-API-02 (tooling), FR-API-01 (AC3, pre-release form), FR-API-04 (AC1)
- OpenAPI 3.1 generated from the handlers, served at `/api/v1/openapi.json`, and committed. Drift fails CI.
- A compatibility gate that compares the document against the one on the main branch, with a per-PR override that requires a recorded reason (FR-API-01 AC3 before the first release). Item 119 switches the baseline to the last release's document and removes the override.
- TypeScript client and types generated into `/web/shared`.
- An envelope contract test over every route.

**Done when:** FR-API-02 AC1 and FR-API-04 AC1 pass, FR-API-01 AC3 passes in its pre-release form (the gate fails on a fixture that removes a response field), and the generated client builds.

### 8. Background job queue
**Status:** done ([#8](https://github.com/CyberCute-Studio/storycreep/pull/8)) · **Depends on:** 5
**PRD:** FR-JOBS-01, ADR-008
- River with transactional enqueue, retries with backoff, periodic and unique jobs, and a dead-letter state.
- Workers in-process by default, disabled with `--no-inline-workers`. Queue metrics.

**Done when:** FR-JOBS-01 AC1–AC4 pass.

### 9. Container image and docker-compose
**Status:** done ([#10](https://github.com/CyberCute-Studio/storycreep/pull/10)) · **Depends on:** 6, 8
**PRD:** FR-ADMIN-01 (AC4), FR-ADMIN-06 (`healthcheck`), FR-ADMIN-07 (AC1, AC2), FR-PUB-01 (AC3, placeholders), FR-PUB-04 (`/`, placeholders), §10.8
- Multi-stage, non-root image with no build toolchain, built for `linux/amd64` and `linux/arm64`, and its health check (N18). Static assets are embedded (placeholders until items 53 and 108), and `/` serves the site's placeholder or the tool's, as `STORYCREEP_PUBLIC_SITE` says.
- `deploy/docker-compose.yml` with exactly two services, named volumes for the database and assets, healthchecks, and documented environment variables, including both database URLs (N16). A development compose file.

**Done when:** FR-ADMIN-01 AC4 and FR-ADMIN-07 AC1–AC2 pass. The compose smoke test (AC3) comes in item 118.

### 10. i18n foundation
**Status:** done ([#11](https://github.com/CyberCute-Studio/storycreep/pull/11)) · **Depends on:** 6
**PRD:** FR-I18N-01, FR-I18N-02 (server), ADR-024
- Catalogs in `/web/shared/i18n` with i18next and ICU, shared by both bundles.
- A server-side catalog for display messages and email templates, with ICU plurals for English and Czech rules.
- Reproducible catalog extraction. A lint rule against hardcoded user-facing literals.
- Locale resolution: user preference → `Accept-Language` → instance default → English. Locale-aware formatting helpers.

**Done when:** FR-I18N-01 AC1–AC3 and FR-I18N-02 AC2 pass.

### Milestone B — Identity and tenancy

### 11. Tenancy schema, RLS, context bootstrap, and maintenance role
**Status:** done ([#12](https://github.com/CyberCute-Studio/storycreep/pull/12)) · **Depends on:** 5
**PRD:** §5.3 (identity and tenancy), §5.4, FR-TEN-01 (AC1), NFR-SEC-01, ADR-002
- Tables: `user`, `workspace`, `membership`, `project_grant`, `invite`, `session`, `api_token`, `audit_entry`, plus a `project` shell needed by `route_project_workspace`.
- `FORCE ROW LEVEL SECURITY` policies on every tenant table.
- The four `SECURITY DEFINER` functions in one migration, plus a test that the database's definer set equals that list.
- The `internal/maintenance` package with its own pool, and how that pool logs in, including the `CONNECT` on the database that migrating revokes from `PUBLIC` (N16, N20). A lint test that no other package imports it. A test that only `storycreep_maintenance` has `BYPASSRLS`.
- A reusable cross-tenant isolation harness that runs with a deliberately broken repository filter.

**Done when:** FR-TEN-01 AC1 and the isolation harness pass, and both §5.4 tests pass.

### 12. Audit log recording
**Status:** done ([#14](https://github.com/CyberCute-Studio/storycreep/pull/14)) · **Depends on:** 11
**PRD:** FR-TEN-07 (AC1, AC3), §9.6
- An audit writer that runs in the caller's transaction, and a registry of event types.
- Grants reject `UPDATE` and `DELETE` from the application role.
- Actors are recorded by ID only. A failed login for an unknown email records a keyed hash of the address, with a key that migrating generates (N21).
- Events that belong to no workspace, such as a login, are recorded without one, from a transaction without one (N21).
- Each later PR emits its own events (standing checklist). AC2 coverage is verified in item 117.

**Done when:** FR-TEN-07 AC1 and AC3 pass.

### 13. Accounts and password policy
**Status:** done ([#15](https://github.com/CyberCute-Studio/storycreep/pull/15)) · **Depends on:** 6, 10, 12
**PRD:** FR-AUTH-01, NFR-SEC-03
- `POST /api/v1/auth/register` under the signup policy: `closed` (self-hosted default), `invite`, or `open`; always `open` in hosted mode. Item 17 adds the guard that refuses registration until first-run setup completes.
- argon2id with documented parameters, rehashed at next login when parameters change. Minimum 12 characters, checked against a bundled list of the 10k most common passwords.
- Generic responses so accounts cannot be enumerated.

**Done when:** FR-AUTH-01 AC1–AC4 pass.

### 14. Sessions and CSRF
**Status:** done ([#16](https://github.com/CyberCute-Studio/storycreep/pull/16)) · **Depends on:** 8, 13
**PRD:** FR-AUTH-02 (AC1–AC5, AC7), NFR-SEC-04
- Opaque, hashed sessions of at least 128 bits. The cookie name and `Secure` flag are derived from `STORYCREEP_PUBLIC_URL`. Validity is never cached.
- Idle and absolute lifetimes. Session list and revocation. A password change revokes other sessions. Rotation on privilege change.
- CSRF protection on non-GET requests. Hosted mode refuses an `http://` base URL.
- Session cleanup job. Audit events for login success and failure and for session revocation.

**Done when:** FR-AUTH-02 AC1–AC5 and AC7 pass. AC6 needs the UI and lands in item 54.

### 15. Rate limiting and brute-force protection
**Status:** done ([#18](https://github.com/CyberCute-Studio/storycreep/pull/18)) · **Depends on:** 14
**PRD:** FR-AUTH-05, NFR-SEC-06, FR-API-04 (AC3)
- PostgreSQL-backed counters. Buckets for auth, reads, writes, exports, and WebSocket connects, keyed by IP and user, with their limits (N23). Item 26 adds the token key to the same buckets, and item 27 attaches them to every route.
- The client's IP address behind a reverse proxy, from the `X-Forwarded-For` header of a trusted proxy (N22).
- Exponential backoff and a temporary lock after 10 consecutive failures (N23). `RateLimit-*` and `Retry-After` headers.

**Done when:** FR-AUTH-05 AC1–AC3 (AC2 across two processes) and FR-API-04 AC3 pass.

### 16. Workspaces and role resolution
**Status:** done ([#17](https://github.com/CyberCute-Studio/storycreep/pull/17)) · **Depends on:** 12, 14
**PRD:** FR-TEN-01 (AC2, AC3), §5.5, FR-TEN-04 (AC3, AC6 for the resolution functions)
- Workspace create, rename, and settings (name, slug, default locale, retention placeholders, N24). The creator becomes owner. Slugs are validated against a reserved-word list.
- A personal workspace on first login (N24). Membership listing through `auth_user_memberships` and switching between workspaces.
- `workspace_role()` and `effective_role()` as pure functions, unit-tested over every combination: instance admin, guest, and absent membership. The instance admin's access is the `InstanceAdminAccess` capability (N24).
- A resolved role of `none` returns 404.

**Done when:** FR-TEN-01 AC2–AC3 and the unit tests of FR-TEN-04 AC3 and AC6 pass.

### 17. First-run setup and user-admin CLI
**Status:** done ([#19](https://github.com/CyberCute-Studio/storycreep/pull/19)) · **Depends on:** 16
**PRD:** FR-ADMIN-02 (all but the SMTP and storage steps), FR-ADMIN-06 (`create-user`, `grant-role`)
- A one-time setup API: first owner, first workspace, signup policy. The same setup can run unattended from environment variables. The chosen policy is stored with the instance, and `STORYCREEP_SIGNUP` overrides it (N25). The SMTP and storage steps land with the settings they configure, in items 18 and 29 (N25).
- Completing setup through the API needs a setup code, which the server logs while setup is pending (N25).
- A transaction guard against creating two owners. The setup route returns 404 permanently once setup completes.
- Until setup completes, `POST /api/v1/auth/register` (item 13) returns 403 `setup_required` under every signup policy, so a self-registered account can never disable setup.
- Instance-admin flag on users: the setup owner is an instance admin, and `grant-role` grants and revokes it.

**Done when:** FR-ADMIN-02 AC1–AC4 pass and both CLI commands work with `--help`.

### 18. Email delivery, verification, and password reset
**Status:** done ([#20](https://github.com/CyberCute-Studio/storycreep/pull/20)) · **Depends on:** 17
**PRD:** FR-AUTH-03, FR-ADMIN-02 (SMTP step), FR-ADMIN-06 (`reset-password`)
- Optional SMTP mailer with templates from the catalog, whose server `STORYCREEP_SMTP_URL` and `STORYCREEP_SMTP_FROM` set, and hosted mode requires (N26). Links are emailed by a job.
- The SMTP step of first-run setup, through the setup API and unattended, with the settings that the mailer reads (N25, N26).
- Single-use, expiring tokens, stored as hashes: 24 h for verification, 1 h for reset. Reset tokens are invalidated when the password changes by any route, and a reset signs the account out everywhere.
- Without SMTP, links are surfaced through an API readable only by instance admins, which issues a link on demand, and through `storycreep reset-password` (N26). The "email not configured" banner flag, `GET /api/v1/instance`.
- Hosted mode requires verification before a workspace can be created, the personal one included (N26).

**Done when:** FR-AUTH-03 AC1–AC5 pass. Invite links are covered in item 19, and the instance-admin screen in item 114.

### 19. Invitations and membership
**Status:** done ([#21](https://github.com/CyberCute-Studio/storycreep/pull/21)) · **Depends on:** 18
**PRD:** FR-TEN-02, FR-AUTH-03 (invite links), FR-TEN-03 (AC4 for invitations and member removal)
- Email and link invites with a role and a 14-day expiry. Revoke and resend. In no-SMTP mode, the copyable link goes to the inviting admin, and so does a link invite's in either mode (N27).
- Invite, member list, and member-removal routes. Accepting an invite creates or links an account: a signed-in user accepts with the link, and someone without an account registers with it, under the `invite` and `open` signup policies (N27). Members can be removed. The last owner cannot be removed or demoted.
- Only an owner invites someone as owner, resends such an invite, or removes an owner, and an admin gets 403 `owner_required` (§5.6, N27).
- Audit events for the invite lifecycle and membership changes.

**Done when:** FR-TEN-02 AC1 and AC3 pass, AC2 over removal, and FR-TEN-03 AC4 over invites and removal. Item 20 adds AC2's demotion through its role-change route.

### 20. Workspace roles and permission-matrix harness
**Status:** done ([#22](https://github.com/CyberCute-Studio/storycreep/pull/22)) · **Depends on:** 19
**PRD:** FR-TEN-03, §5.6, FR-TEN-02 (AC2 for demotion, and the invitations that a role change revokes)
- Role-change operations and their routes. Admins manage members and roles up to `admin`. Granting `owner` by role change, and demoting an owner, require `owner` and otherwise return 403 `owner_required` (§5.6), as inviting an owner and removing one do from item 19 (N27). A role change revokes the open invitations that the member made or was last given the link of as text whose role the new role may not give, as a removal does from item 19: every one, for a demotion from `admin` or `owner` to a role that cannot manage members, and those as `owner`, for an owner made `admin` (N27, N28). A service-layer authorisation package. A lint test that no handler writes to a repository directly.
- A table-driven test over every (role × operation) pair in §5.6 for the operations that exist so far, with the two member-management rows of §5.6 as separate operations. Later PRs add their rows (standing checklist).
- A generic test that a `viewer` gets 403 on mutating routes and 200 on reads.

**Done when:** FR-TEN-03 AC1 and AC3 pass for the current operation set, and AC2 and AC4 pass. Item 117 completes AC1 and AC3 over every MVP operation and mutating route.

### Milestone C — Content core

### 21. Entitlement layer and limit errors
**Status:** todo · **Depends on:** 16
**PRD:** FR-ENT-01, FR-ENT-03 (server), ADR-010
- One declarative plan file with plans `unlimited`, `free`, `team`, and `studio`. It defines exactly the keys from ADR-010 (limits `members`, `projects`, `storage_bytes`, `snapshot_retention`; flags `collaboration`, `sso`, `audit_export`), with placeholder values. `snapshot_retention` is marked inert until FR-SNAP-06 (N9). Boards, nodes, and API requests are metered (item 23) but have no limit key (N14).
- A single `Check` / `Limit` API. Self-hosted mode resolves to unlimited, with optional instance caps.
- `409 limit_exceeded` carries the key, usage, and limit, plus an upgrade link in hosted mode only. Usage reads carry an 80% soft-warning flag.

**Done when:** FR-ENT-01 AC2–AC4 and FR-ENT-03 AC1 pass. Each enforcement site adds its own FR-ENT-01 AC1 test as it lands; item 117 completes AC1 with a check over every key in the plan file.

### 22. Command pipeline core
**Status:** todo · **Depends on:** 8, 12, 20, 21
**PRD:** §5.7, §10.2, §10.4, ADR-004, ADR-006, FR-API-03 (atomicity, revisions)
- A command registry: name, payload type, required capability, limit keys, inverse, and touched entity kinds.
- Pipeline steps: strict payload validation → authorisation (role × capability; token scopes are added in item 26) → entitlements → apply in one transaction.
- Per-entity `rev` and `expected_rev`, with `revision_conflict` returning the current state.
- An `operation` row with the next project `seq`, the inverse, and `entity_refs`. One `applied_command` row per command.
- Transactional job enqueue. A post-commit `NOTIFY` hook (no-op until item 52). Audit writes in the same transaction. Command throughput and conflict metrics.

**Done when:** FR-API-03 AC2 (single batch) and AC3 pass at the service level.

### 23. Usage metering
**Status:** todo · **Depends on:** 22
**PRD:** FR-ENT-02, FR-ENT-01 (`members` limit), §5.7
- `usage_delta` rows inserted in the command transaction, and `usage_counter`.
- A rollup job using `DELETE … RETURNING`, and a reconciliation job.
- Exact mode under an advisory lock for `projects`, `members`, and `snapshots`. Approximate counting for `boards`, `nodes`, and `storage_bytes`; of these, only `storage_bytes` is a limit (item 29).
- A usage endpoint for workspace admins.
- The `members` limit, enforced at one site: the service function that creates a membership. Every path that adds a member calls it: invite acceptance (item 19), `storycreep grant-role` (item 17), and workspace creation. Creating an invite does not check the limit; the invite dialog shows current usage instead. Items 17 and 19 predate the entitlement layer, so the check is added here.

**Done when:** FR-ENT-02 AC1, AC2, AC4, and AC5 pass, and the FR-ENT-01 AC1 test for `members` reaches the limit through both invite acceptance and `grant-role`. AC3 creates nodes and lands in item 35.

### 24. Project lifecycle
**Status:** todo · **Depends on:** 23
**PRD:** FR-PROJ-01 (AC2, AC3), FR-SNAP-01 (AC3 and AC6, setting only)
- Project routes and commands: create, rename, describe, archive, unarchive, and soft delete with 30-day restore. A hard-delete job. The `projects` limit.
- An archived-project guard in the pipeline returns `409 project_archived`.
- Project settings: entry board and node, `publish_min_role` (default `editor`; changing it requires `admin` and is audited), and default locale.
- Paginated project list. Audit events for project create and delete. Duplication arrives in item 85.

**Done when:** FR-PROJ-01 AC2–AC3 pass and the `publish_min_role` change is audited.

### 25. Project grants
**Status:** todo · **Depends on:** 20, 24
**PRD:** FR-TEN-04
- Grant routes and CRUD, authorised by the workspace role. An `owner` grant returns 400.
- Guest semantics. Grants apply only to project-scoped operations.
- The FR-TEN-04 AC4 guest test, table-driven over the workspace-scoped operations. Later PRs add their operations to it (standing checklist).

**Done when:** FR-TEN-04 AC1–AC3, AC5, and AC6 pass, and AC4 passes for the workspace-scoped operations that exist so far. Item 117 completes AC4 once token management (26), import (84), and the remaining workspace operations have joined.

### 26. API tokens and scopes
**Status:** todo · **Depends on:** 15, 25
**PRD:** FR-AUTH-04, NFR-SEC-05, FR-ADMIN-06 (`create-token`)
- Tokens with the `scp_` prefix, stored as SHA-256, shown once. Scoped to a workspace and optionally narrowed to a project, which returns 404 for other projects. Optional expiry.
- The scope table with its implications. Intersection with the owner's current role on every request. `last_used_at` written at most once per minute.
- `Authorization: Bearer` transport, and the token key on the rate-limit buckets of item 15. Token routes. Audit events for create, revoke, and use-denied.

**Done when:** FR-AUTH-04 AC1–AC5 pass for the current routes. AC7 needs the snapshot, draft, and authoring exports and lands in item 83.

### 27. REST surface: token classification and limits
**Status:** todo · **Depends on:** 7, 26
**PRD:** FR-API-01 (AC1), FR-AUTH-04 (AC6), FR-API-04
- A route-enumeration gate: every route is either token-capable with a declared scope or session-only (`403 session_required`). This becomes a permanent CI check.
- The read, write, and export buckets of item 15 are attached to every route through its declaration, so no route can skip them. RFC 3339 timestamps. `ETag` / `If-None-Match` on large reads.
- No new resource routes. The workspace, member, invite, project, grant, token, and usage routes land with their features in items 16–26, which is why those items can claim HTTP-level criteria. This item classifies all of them under the gate.

**Done when:** FR-API-01 AC1 and FR-AUTH-04 AC6 pass. FR-API-04 AC1–AC3 hold across the current routes.

### 28. Command endpoint, idempotency, and multi-batch
**Status:** todo · **Depends on:** 27
**PRD:** FR-API-03
- `POST /api/v1/projects/{id}/commands` in single-batch and `{"batches": [...]}` forms.
- `idempotency_record` kept for at least 24 h, with a pruning job. Fallback to `applied_command` returning `already_applied`. `422 idempotency_mismatch`.
- The idempotency check runs before entitlements.

**Done when:** FR-API-03 AC1–AC6 pass.

### 29. Blob storage and asset uploads
**Status:** todo · **Depends on:** 8, 23
**PRD:** FR-ASSET-01 (AC1, AC2), FR-ASSET-02, FR-ADMIN-02 (storage step), NFR-SEC-10, ADR-009
- The storage step of first-run setup, through the setup API and unattended, with the settings that the drivers read (N25).
- A driver interface with local-disk and S3 drivers, and one shared test suite (S3 via MinIO in CI). Content-addressed keys `{workspaceId}/{sha256}.{ext}`. In the container image, the local driver's directory is `/var/lib/storycreep/assets`, where `deploy/docker-compose.yml` mounts the asset volume (N18).
- Serving through access-checked local routes and signed S3 URLs. The access check runs before any read.
- Upload pipeline: content sniffing, size limit, SVG sanitised or rejected, storage quota returns 413. `CSP: sandbox` and `nosniff` on served assets.
- A thumbnail job and an orphan-reconciliation job. Storage usage deltas.

**Done when:** FR-ASSET-01 AC1–AC2 and FR-ASSET-02 AC1–AC4 pass. AC3 (deleting a workspace deletes its blobs) is in item 110.

### 30. Boards and folders
**Status:** todo · **Depends on:** 24
**PRD:** FR-PROJ-02 (AC1, AC3, AC4)
- `folder` entities of kind `board`, `variable`, and `component`, with tree nesting and drag-reorder order.
- Board commands: create, rename, describe, colour, move, delete. The entry-node field (set in item 35). `boards` usage deltas (metered, not limited).
- Slug derivation and collision handling. File-name-safe slug rules and reserved device names, rejected with `slug_invalid`.

**Done when:** FR-PROJ-02 AC1, AC3, and AC4 pass.

### 31. Trash and restore framework
**Status:** todo · **Depends on:** 30
**PRD:** FR-PROJ-06 (framework, boards)
- `deleted_at` semantics and shared "live rows" query helpers, so trashed rows are excluded from search, exports, and snapshots.
- A project trash list, a restore-in-place command with a reference-reconnection hook, and a purge job (30 days, configurable).
- Boards are registered here. Items 33–36 register their entities.

**Done when:** board restore works and FR-PROJ-06 AC2 holds through the shared helpers.

### Milestone D — Narrative model (server)

### 32. Rich-text document model
**Status:** todo · **Depends on:** 4, 6
**PRD:** FR-TEXT-01 (AC1, validator), ADR-020, NFR-SEC-07, NFR-SEC-08
- A constrained ProseMirror schema. Marks: bold, italic, strike, code, link. Nodes: paragraph, lists, hard break, a value-reference atom, and a mention atom.
- A Go validator that rejects unknown marks and nodes and enforces depth limits. A published rich-text JSON Schema. Link URL allowlist.
- Derivations: published `segments`, plain text, preview, word count, and a stable text hash.

**Done when:** the validator accepts the published schema's examples and rejects unknown marks, unknown nodes, and over-deep documents in unit tests. No endpoint stores rich text yet, so FR-TEXT-01 AC1 passes at the API boundary in item 36.

### 33. Variables
**Status:** todo · **Depends on:** 31
**PRD:** FR-VAR-01 (AC1, AC2), FR-VAR-02 (data model), FR-PROJ-06 (trash registration)
- `variable` with key (identifier grammar and reserved words from §5.8.1), type, typed default, description, folder, `global` or `board` scope, and `reset_on_enter` (default `false`).
- Keys unique per scope. Commands: create, update, move, delete. Guarded deletion arrives in item 47. Registered with trash.

**Done when:** FR-VAR-01 AC1–AC2 pass.

### 34. Component types and components
**Status:** todo · **Depends on:** 29, 31, 32
**PRD:** FR-COMP-01, FR-COMP-02 (AC1, AC3), FR-PROJ-06 (trash registration)
- Default types seeded per project: Character, Item, Location, Faction, Custom.
- Ordered attribute definitions with a `runtime` flag. Adding an attribute is one command and fills the default on every component. Changing an attribute's type needs an explicit conversion choice and reports values that cannot convert.
- Components with an auto-derived key, name, type, image asset, rich-text description, attribute values, and folders. List, search, and filter by type through the API. Registered with trash.

**Done when:** FR-COMP-01 AC1–AC3 and FR-COMP-02 AC1 and AC3 pass.

### 35. Nodes: elements and jumpers
**Status:** todo · **Depends on:** 32, 34
**PRD:** FR-EDIT-02 (server: AC1, AC2, AC4), FR-EDIT-05 (server), FR-EDIT-06 (server), FR-PROJ-02 (AC2, warning half), FR-ENT-02 (AC3), FR-PROJ-06 (trash registration)
- `node` of type `element`, `branch`, or `jumper` (branch outputs arrive in item 43). Fields: kind, title, rich text, speaker, cover asset, palette colour, tags (with project-scoped autocomplete), position as `numeric(12,2)`, size, `is_ending`.
- Commands: create, update, batched move (one command), delete, duplicate. Multi-select operations are sent as batch commands.
- Jumper targets and a "referenced by" query. Setting a board's entry node.
- Deleting a board lists the referencing jumpers and needs confirmation. Its references stay dangling, never dropped.
- `nodes` usage deltas. Registered with trash.

**Done when:** FR-EDIT-02 AC1, AC2, and AC4, FR-EDIT-05 AC2, the warn-and-confirm half of FR-PROJ-02 AC2 (references stay dangling; validation reports them in item 48), and FR-ENT-02 AC3 (16 sessions creating nodes across two projects) pass.

### 36. Connections
**Status:** todo · **Depends on:** 35
**PRD:** FR-EDIT-03 (server: AC2), FR-EDIT-02 (AC3), FR-PROJ-06 (AC1), FR-TEXT-01 (AC1)
- Create from the output port, reconnect either end, delete, and reorder. Reordering changes only `order`. Rich-text labels.
- Every endpoint that stores rich text (node bodies from item 35, connection labels here, component descriptions from item 34) validates it with the item-32 validator.
- Self-connections and parallel connections are allowed (validation flags them later).
- Deleting a node deletes its connections in the same transaction, and the inverse restores both. Restoring a node from trash reconnects its connections where both endpoints exist.

**Done when:** FR-EDIT-03 AC2, FR-EDIT-02 AC3, FR-PROJ-06 AC1, and FR-TEXT-01 AC1 (a document with an unknown mark or node is rejected by each of the three endpoints) pass. FR-EDIT-03 AC1 needs play mode and the serializer and lands in items 71 and 76.

### 37. Undo/redo contract
**Status:** todo · **Depends on:** 28, 36
**PRD:** FR-EDIT-08 (AC1, AC2, AC4), NFR-Q-05
- A registry test that every command type has an inverse or is explicitly non-undoable.
- Undo and redo are sent as inverse commands with expected revisions. They are refused with an explanation, and no partial change, when they would overwrite newer work.
- A property test: apply N random commands, undo N, and the state equals the original.

**Done when:** FR-EDIT-08 AC1, AC2, and AC4 pass.

### Milestone E — Expression engine

### 38. Unicode tables and numeric/string semantics
**Status:** todo · **Depends on:** 1
**PRD:** §5.8.9
- A generator that builds the pinned simple case mapping and `White_Space` tables from the Unicode Character Database into `/schema/unicode/`.
- Go primitives: safe-integer arithmetic with the multiplication pre-check, overflow and non-finite handling, the exact `to_int` / `to_float` grammars, correctly rounded parsing, shortest round-trip float formatting, rounding half away from zero, and `-0.0` normalisation.
- Numeric and string cases are seeded into `/schema/expr-corpus/`.

**Done when:** every §5.8.9 corpus case listed in the PRD passes in Go.

### 39. Expression lexer, parser, and AST encoding
**Status:** todo · **Depends on:** 4, 38
**PRD:** §5.8.1, §5.8.2, §5.8.8, FR-EXPR-02 (parser), FR-EXPR-03 (AC3)
- A lexer and parser for the grammar in §5.8.2, including word operators, script terminators, comments, and `node("nd_…")` literals.
- Errors with stable codes and exact character ranges.
- Deterministic AST JSON with the node kinds `lit`, `var`, `attr`, `node_ref`, `unary`, `binary`, `call`, `assign`, and `script`.
- The corpus format (input, context, expected AST or error) with a Go runner.

**Done when:** FR-EXPR-03 AC3 passes and the corpus runs in CI.

### 40. Name resolution, type checking, and sandbox limits
**Status:** todo · **Depends on:** 33, 35, 39
**PRD:** FR-EXPR-02 (server: AC2–AC4), FR-EXPR-04 (AC2, AC3), FR-VAR-02 (AC1 resolution, AC2), FR-COMP-03 (AC2), §5.8.3–§5.8.6
- Resolution order: board → global → component. Explicit `global.` / `board.` / `cmp.` forms. Identifiers resolve to IDs, with near-match suggestions.
- Typing and widening rules. Purity (`condition_must_be_pure`). `signal()` allowed only in scripts with a literal name. `node_ref_jumper`. Non-runtime attributes rejected. Cross-board board-variable references rejected.
- The built-in table records each function's introducing `ast_version`. A helper computes a snapshot's `astVersion`.
- Sandbox limits from §5.8.6 with specific error codes. The corpus grows to at least 200 cases.

**Done when:** the listed ACs pass, with only the resolution half of FR-VAR-02 AC1 (the editor display lands in item 67), and the corpus has at least 200 cases passing in Go.

### 41. Published format types and JSON Schemas
**Status:** todo · **Depends on:** 32, 39
**PRD:** FR-DATA-04 (AC2, AC3), §6.1, §6.3, §6.5, §8.5
- Go types for the published game format: manifest with the `boards` index, `project/*.json`, board files, segments, and the AST. Also the save-state format.
- JSON Schemas generated into `/schema` with versioned `$id`. Drift fails CI. `null` is forbidden everywhere. TypeScript types generated from the schemas.
- Items 80 (diff) and 83 (authoring export) add their schemas through the same generator.

**Done when:** FR-DATA-04 AC2–AC3 pass and the §6.5 examples validate.

### 42. Reference evaluator, flow semantics, and conformance suite (Go)
**Status:** todo · **Depends on:** 40, 41
**PRD:** §5.8.4–§5.8.7, §8.6, FR-PLAY-06 (Go side: AC1, AC3), FR-EXPR-04 (AC1, Go)
- An AST evaluator with the built-ins, a step budget, and runtime errors that yield the type's zero value.
- A flow engine that follows the normative order in §5.8.5: on-exit and on-traverse, transparent jumpers, board entry with `reset_on_enter`, flow-end reasons, `start`, `goto`, `reset`, and visit counts.
- A scenario format in `/schema/conformance/` whose fixtures use the published format from item 41, a Go runner, and at least 30 scenarios covering the FR-PLAY-06 AC1 list.

**Done when:** the Go suite passes and the scenario format is documented in the repository.

### 43. Expressions on content: conditions, scripts, branches, runtime attributes
**Status:** todo · **Depends on:** 37, 42
**PRD:** FR-EXPR-03 (expression entity), FR-EDIT-04 (server: AC3), FR-EDIT-03 (AC4), FR-COMP-03 (data), §5.3 `expression`
- The `expression` entity: source, AST, `ast_version`, `is_valid`, error, and an owner back-reference.
- Connection conditions and on-traverse scripts. Node on-enter and on-exit scripts. Runtime attributes readable and writable from expressions.
- Branch nodes with ordered `if` / `elseif` / `else` outputs. Removing an output that has a connection deletes it in the same command.
- An invalid expression is saved but marked invalid. Expressions are recompiled when the entities they reference change.

**Done when:** FR-EDIT-04 AC3 and FR-EDIT-03 AC4 (save-and-mark half) pass, and stored ASTs reference variables and components by ID. FR-EXPR-03 AC2 and FR-EDIT-04 AC2 are checked against published bytes in item 76.

### 44. TypeScript expression and flow engine
**Status:** todo · **Depends on:** 42
**PRD:** FR-EXPR-02 (AC1), FR-EXPR-04 (AC1, TypeScript), FR-PLAY-06 (TypeScript side), §5.8.9
- A `/web/shared` package with the lexer, parser, type checker, evaluator, and flow engine. It uses the same Unicode tables and code-point string semantics (no UTF-16 `.length`, no `Math.round`).
- Vitest runs the expression corpus and the conformance suite.

**Done when:** FR-EXPR-02 AC1 passes (the same corpus in Go and TypeScript) and the TypeScript conformance runner is green.

### 45. Fixture generator and server benchmarks
**Status:** todo · **Depends on:** 43
**PRD:** NFR-Q-06, §9.1 (server metrics), NFR-PERF-07, 09, 10, 12, 13
- A seeded, deterministic generator for the `small`, `medium`, `large`, and `dense` fixtures. It builds them through commands.
- A Go benchmark suite on fixed CI hardware that fails on a server regression of more than 20%. Each item adds its benchmarks as its features land.

**Done when:** the fixtures generate reproducibly and the benchmark job runs.

### Milestone F — Analysis services

### 46. Dependency index and usage queries
**Status:** todo · **Depends on:** 45
**PRD:** FR-DEP-01 (AC1, AC2, AC4 usage-list half), FR-COMP-04 (server, AC2), FR-TEXT-02 (AC1, server), FR-TEXT-03 (AC1, server)
- `expression_ref` maintained in the command transaction, covering value references and mentions in text. Attributes are keyed by the (component, attribute definition) pair.
- `GET …/variables/{vid}/usages` returns reads and writes separately. A component usage query.

**Done when:** FR-DEP-01 AC1 and AC2, the usage-list half of FR-DEP-01 AC4 (Elara's list has no site from board B, and `elara.mood` is not a usage of `elara.trust`), and FR-COMP-04 AC2 pass. The no-edge half of AC4 lands with the graph in item 49.

### 47. Safe rename and guarded deletion
**Status:** todo · **Depends on:** 46
**PRD:** FR-VAR-03, FR-VAR-04, FR-COMP-02 (AC2), FR-COMP-04 (AC1), FR-EDIT-06 (AC2), FR-TEXT-02 (AC2)
- Variable and component-key rename as one atomic command. Source is rewritten from the AST using the shortest form that re-resolves to the same ID. A preview endpoint lists the qualified rewrites separately.
- A used variable or component cannot be deleted (409 with usage details). Forced deletion is allowed for roles that can publish; it marks expressions invalid.

**Done when:** FR-VAR-03 AC1–AC4, FR-VAR-04 AC1–AC2, FR-COMP-04 AC1, FR-EDIT-06 AC2, FR-COMP-02 AC2 (renaming the display name keeps the key; a key change is a refactor with FR-VAR-03 semantics), and the blocking half of FR-TEXT-02 AC2 (deleting a variable referenced only from text returns 409) pass. The dangling-reference half lands in item 48.

### 48. Validation engine
**Status:** todo · **Depends on:** 46
**PRD:** FR-DEP-03 (server: AC1, AC2, AC4), FR-PROJ-02 (AC2, validation half), FR-TEXT-02 (AC2, dangling half)
- Every rule code in the FR-DEP-03 table, with severity, message, and an entity address.
- Muting per entity with a recorded reason. Muted findings are listed separately.

**Done when:** each rule has a positive and a negative fixture, validation of `large` takes under 3 s, a jumper left dangling by a board deletion is a validation error (FR-PROJ-02 AC2), and a dangling value reference in text, seeded directly in the database, is a validation error (FR-TEXT-02 AC2).

### 49. Cross-board dependency graph API
**Status:** todo · **Depends on:** 46
**PRD:** FR-DEP-02 (server: AC2, AC5), FR-DEP-01 (AC4 no-edge half)
- Edges of four kinds: variable, attribute, jumper, and `visit`. Edge detail returns the contributing triples and pairs. Board inbound and outbound counts. Filters.

**Done when:** FR-DEP-02 AC2 and AC5 pass, and the FR-DEP-01 AC4 fixture produces no A→B edge. AC1 needs the sample project and lands in item 85.

### 50. Project search
**Status:** todo · **Depends on:** 45
**PRD:** FR-PROJ-03 (server)
- PostgreSQL full-text search with `unaccent`, maintained in the command transaction plus a maintenance job.
- Type filters, highlighted snippets, and permission-respecting results. Comments are indexed in item 74.

**Done when:** FR-PROJ-03 AC1–AC4 pass.

### 51. Clipboard payload and paste
**Status:** todo · **Depends on:** 46
**PRD:** FR-EDIT-07 (server: AC1 report, AC3)
- A documented, versioned clipboard JSON structure.
- A paste command that assigns new IDs, keeps references that resolve in the destination, and reports the ones that do not, with "create missing" support.

**Done when:** FR-EDIT-07 AC3 passes and the unresolved-reference report is returned.

### Milestone G — Real-time server

### 52. WebSocket hub and LISTEN/NOTIFY fanout
**Status:** todo · **Depends on:** 26, 28
**PRD:** FR-RT-01, FR-RT-02 (server: AC2), ADR-005, NFR-PERF-17
- `GET /api/v1/ws` authenticated by cookie or token. Subscriptions require read access, without revealing whether a project exists.
- `since={seq}` resume or `resync_required`. Heartbeats.
- One dedicated `LISTEN` connection per process. Notifications carry only `(project, seq)`, and the hub fetches operations from the log.

**Done when:** FR-RT-01 AC1–AC4 and FR-RT-02 AC2 pass, and the benchmark of item 45 reports NFR-PERF-17.

### Milestone H — Web app foundation

### 53. Web app foundation
**Status:** todo · **Depends on:** 7, 10
**PRD:** §10.6, FR-API-02 (AC2), FR-I18N-01 (app), FR-EDIT-09 (AC2), NFR-A11Y-01, NFR-A11Y-08, NFR-OBS-06, ADR-012
- Vite and React 19, TanStack Router and Query, the generated API client, Tailwind v4 with shared design tokens, and Radix primitives.
- Served by the Go binary at `/app/*` with CSP nonces.
- Vitest, Playwright (desktop and tablet-touch profiles), and axe in CI.
- A static check that no CSS `:hover` rule is the only trigger for an affordance. A local error console that never transmits.

**Done when:** FR-API-02 AC2 and FR-EDIT-09 AC2 pass, and axe runs on the shell.

### 54. Auth and account screens
**Status:** todo · **Depends on:** 18, 26, 53
**PRD:** FR-AUTH-02 (AC6), FR-ADMIN-02 (UI), FR-AUTH-04 (UI), FR-I18N-02 (AC1), NFR-SEC-15
- Screens: login, logout, first-run setup, signup, verification, password reset, and invite acceptance. The verification and reset screens read the link's token from the URL's fragment (N26), and so does invite acceptance, from `/app/invite`, which shows the invitation, and signs in or registers with it before it accepts (N27).
- Session list and revocation. API token management. Locale preference that applies without reload.
- The plain-HTTP warning on non-loopback addresses.

**Done when:** FR-AUTH-02 AC6 (plain HTTP on a LAN address, and a TLS proxy) and FR-I18N-02 AC1 pass.

### 55. Workspace, member, and project management UI
**Status:** todo · **Depends on:** 25, 31, 54
**PRD:** FR-TEN-02/03/04 (UI), FR-PROJ-01/02/06 (UI), FR-ENT-03 (UI, AC2)
- Workspace switcher (hidden when the user has one workspace) and settings. Members, invites with copyable links, roles, and project grants (N27).
- Project list, create, archive, and delete. Board tree with folders and drag-and-drop. Trash.
- Usage against limits, specific limit messages, and no upgrade prompts in self-hosted mode.

**Done when:** FR-ENT-03 AC2 passes and the management flows have Playwright coverage.

### 56. Client document store, command client, and remote operations
**Status:** todo · **Depends on:** 37, 52, 55
**PRD:** §10.6, §7.10, FR-EDIT-08 (AC3), FR-RT-02 (AC1, AC3), FR-OFF-03 (AC2 by design), NFR-PERF-08
- A Zustand project store that loads entities with paging.
- One shared TypeScript layer that applies commands to local state. Offline reuses it unchanged (FR-OFF-03 AC2).
- Optimistic apply with `command_id` and `expected_rev`. Conflict handling as in §7.10 item 1.
- The undo/redo stack, persisted so that at least the last 50 steps survive a reload.
- WebSocket client with reconnect, backoff, and jitter. Remote operations apply without disturbing the selection, viewport, or an unrelated field being edited.
- A two-session latency harness: commit in one browser session, measure until the change is applied in the other.

**Done when:** FR-EDIT-08 AC3 and FR-RT-02 AC1 (two server processes) and AC3 pass, and the latency harness reports NFR-PERF-08.

### 57. Rich-text editor component
**Status:** todo · **Depends on:** 32, 56
**PRD:** FR-TEXT-01 (AC2), ADR-020
- TipTap v3 with the constrained schema from item 32, client-side schema validation, and paste sanitising (Word and Google Docs) down to the supported subset.
- Reused by node bodies, labels, component descriptions, and comments.

**Done when:** FR-TEXT-01 AC2 passes.

### 58. Variables and components panels
**Status:** todo · **Depends on:** 47, 57
**PRD:** FR-VAR-01 (AC3), FR-VAR-02 (AC5), FR-VAR-03/04 (UI), FR-COMP-01/02 (UI), FR-COMP-04 (UI)
- A variable panel with a type-appropriate default editor, scope, `reset_on_enter` with its persistence explained in plain language, and folders.
- Component-type and component panels.
- Usage lists that open from a variable or component and list each site with its board and node. Rename preview. Guarded-deletion dialogs. Panels become sheets below 900 px width.
- No canvas exists yet: item 61 turns usage sites into jump-to links, and item 67 opens the panel from expressions (FR-DEP-01 AC3).

**Done when:** FR-VAR-01 AC3 (1,000 variables on the reference tablet) and FR-VAR-02 AC5 pass.

### Milestone I — Canvas

### 59. PixiJS renderer, navigation, culling, and LOD
**Status:** todo · **Depends on:** 45, 56
**PRD:** FR-EDIT-01, FR-EDIT-11, ADR-013, NFR-PERF-01/02/04/05/18, NFR-TOUCH-03/07/08, NFR-A11Y-07
- PixiJS v8 board rendering. Pan and zoom by mouse, wheel, trackpad, touch, and keyboard. Zoom range 10–400%, fit to content, zoom to selection, minimap.
- Culling, batching, and level of detail (simplified blocks below 40%). A Canvas 2D fallback with reduced limits.
- Pull-to-refresh suppressed over the canvas. `prefers-reduced-motion` respected.
- A canvas benchmark harness and a scripted checklist for the reference tablet. The harness also measures initial project load to the first rendered board (`medium` over LAN, `large` with boards loaded on demand).

**Done when:** FR-EDIT-01 AC1–AC4 and FR-EDIT-11 AC1–AC2 pass, and the harness reports NFR-PERF-01, 02, 04, 05, and 18.

### 60. Last-opened state and authenticated landing
**Status:** todo · **Depends on:** 59
**PRD:** FR-PROJ-04, FR-PUB-05
- Last project, board, viewport, and selection stored per user and device. Stale state never blocks startup.
- Server-side redirect of `/` and `/login` to the last board, the project list, or workspace creation.

**Done when:** FR-PROJ-04 AC1–AC2 and FR-PUB-05 AC1–AC4 pass.

### 61. Node editing on the canvas
**Status:** todo · **Depends on:** 58, 59
**PRD:** FR-EDIT-02 (UI), FR-EDIT-06 (UI), FR-EDIT-08 (UI), FR-TEXT-01 (AC3), FR-EDIT-09 (AC3), FR-COMP-04 (UI, jump-to links), NFR-TOUCH-04/05, NFR-PERF-06
- Create, select (single, additive, rubber band), move with 8 px snap, resize, delete, duplicate. Multi-select align, distribute, colour, tag, and delete.
- Node inspector: title, kind, speaker picker, cover upload, tags, `is_ending`. Tag filter dims non-matching nodes.
- A rich-text DOM overlay synchronised with pan and zoom, kept visible above the on-screen keyboard.
- Undo/redo controls. An 8 px drag threshold, and an undo affordance after a bulk move.
- Usage sites in the panels of item 58 become jump-to links that open the node on the canvas.
- The canvas harness of item 59 measures local edit to visible result.

**Done when:** FR-EDIT-06 AC1, FR-TEXT-01 AC3, and FR-EDIT-09 AC3 pass, jump-to links have UI tests, and the harness reports NFR-PERF-06.

### 62. Connections, branch outputs, and jumpers on the canvas
**Status:** todo · **Depends on:** 43, 61
**PRD:** FR-EDIT-03 (UI: AC3), FR-EDIT-04 (UI), FR-EDIT-05 (UI: AC1 broken rendering)
- Port handles that enlarge on touch. Drag to connect, reconnect, reorder choices. Dropping on empty canvas offers "create node here and connect", and cancelling leaves nothing behind.
- Branch output ports with add, remove, and reorder.
- Jumper rendering, including the broken state. One-tap navigation to the target. "Referenced by" on target nodes.

**Done when:** FR-EDIT-03 AC3 passes. Branch-output editing, jumper navigation, and the broken-jumper rendering of FR-EDIT-05 AC1 have UI tests. The validation and publish-blocking parts of FR-EDIT-05 AC1 land in item 78.

### 63. Touch interaction model and canvas accessibility tree
**Status:** todo · **Depends on:** 62
**PRD:** FR-EDIT-09 (AC2 enforced), NFR-TOUCH-01/02/06, NFR-A11Y-03/04
- Long-press (400 ms) context menus, targets of at least 44 px, no interaction that needs hover or right-click, panels as sheets.
- An accessibility tree of nodes and connections. Selection changes are announced through an ARIA live region.

**Done when:** touch targets and long-press have Playwright touch tests, and axe and accessibility-tree tests pass. The full FR-EDIT-09 AC1 suite is in item 67.

### 64. Clipboard UI
**Status:** todo · **Depends on:** 51, 62
**PRD:** FR-EDIT-07 (UI: AC1, AC2)
- Copy, cut, paste, and duplicate across boards and across projects in the same workspace. A dialog lists unresolved references and offers to create them.

**Done when:** FR-EDIT-07 AC1–AC2 pass.

### 65. Search UI, command palette, and shortcuts
**Status:** todo · **Depends on:** 50, 61
**PRD:** FR-EDIT-10, FR-PROJ-03 (UI)
- Project search with type filters and jump to result.
- `Ctrl/Cmd+K` palette. Every action has a command ID shared by the palette and the shortcut table. Help overlay. Remapping arrives in item 129.

**Done when:** FR-EDIT-10 AC1–AC2 pass.

### Milestone J — Writing surfaces

### 66. Value references and component mentions in text
**Status:** todo · **Depends on:** 46, 61
**PRD:** FR-TEXT-02, FR-TEXT-03
- `{` autocomplete inserts value-reference chips with format specifiers. `@` inserts component mentions. Display follows renames, and stored IDs never change.

**Done when:** FR-TEXT-02 AC1 (UI) and FR-TEXT-03 AC1 pass.

### 67. Expression editor and guided picker
**Status:** todo · **Depends on:** 44, 58, 63
**PRD:** FR-EXPR-01, FR-EDIT-09 (AC1), FR-DEP-01 (AC3), FR-VAR-02 (AC1, editor)
- Single-line and multi-line editors with highlighting, scope-aware autocomplete (with type, description, and where the variable is written), and inline errors at parser ranges.
- Each resolved identifier shows which variable it resolved to, including its scope. From any identifier, the variable's usage panel (item 58) opens in at most two interactions.
- A localised plain-language rendering generated from the AST.
- A guided picker (variable → operator → value) that stores the same expression. Inline "create variable" from an unknown identifier.
- The full touch-emulation suite: create node, edit text, create connection, set condition, delete node, undo.

**Done when:** FR-EXPR-01 AC1–AC4, FR-EDIT-09 AC1, FR-DEP-01 AC3, and the editor half of FR-VAR-02 AC1 (a bare key that exists in both scopes is shown as board-scoped) pass.

### 68. Script / outline view
**Status:** todo · **Depends on:** 66, 67
**PRD:** FR-TEXT-04, NFR-A11Y-02
- A screenplay-like linear view in deterministic traversal order, with the order rule documented in the UI.
- Writes through the same commands as the canvas. "Add node after" and "add choice". Keyboard-only and screen-reader operable.

**Done when:** FR-TEXT-04 AC1–AC4 pass.

### 69. Writing statistics
**Status:** todo · **Depends on:** 66
**PRD:** FR-TEXT-06
- Word and character counts per node, board, and project, plus counts of nodes, choices, and conditions. Updated incrementally, without rescanning the project.

**Done when:** FR-TEXT-06 AC1 passes.

### 70. Validation panel and dependency view
**Status:** todo · **Depends on:** 48, 49, 67
**PRD:** FR-DEP-03 (UI: AC3, AC4), FR-DEP-02 (UI: AC3, AC4), NFR-PERF-11
- A findings panel: click opens the right editor. Mute with a reason. A muted list.
- Board graph view with edge inspection, filters, cycles rendered without errors, and board-header badges.
- The canvas harness measures dependency-view render time for 60 boards.

**Done when:** FR-DEP-03 AC3–AC4 and FR-DEP-02 AC3–AC4 pass, and the harness reports NFR-PERF-11.

### Milestone K — Play mode

### 71. Play mode core
**Status:** todo · **Depends on:** 44, 62
**PRD:** FR-PLAY-01, FR-PLAY-06 (play-mode side), FR-TEXT-02 (AC3, play mode), FR-EDIT-03 (AC1, play mode), FR-EDIT-04 (AC1, play mode), FR-EDIT-05 (AC3), FR-COMP-03 (AC1, play mode), FR-VAR-02 (AC3, AC4, play mode)
- A TypeScript projection of the client draft store into the published format of item 41, using the generated types. It includes rich-text segment derivation that matches item 32. Play mode therefore runs on the same data model that the conformance suite checks, with no server round trip. Item 89 cross-checks this projection against the Go serializer of item 76.
- Client-side play on the TypeScript flow engine. Start from any node or a board entry. Resolved text and speaker, choices, jumpers across boards, branches, and scripts.
- Canvas highlights for the current node and the traversed path. A strict-mode error panel.

**Done when:** FR-PLAY-01 AC1–AC3, FR-EDIT-05 AC3, and the play-mode halves of FR-EDIT-03 AC1, FR-EDIT-04 AC1 (`dead_end`), FR-COMP-03 AC1, and FR-VAR-02 AC3–AC4 pass, the in-app engine passes the conformance suite, and projected drafts validate against the published JSON Schemas.

### 72. Variable inspector, hidden choices, and history
**Status:** todo · **Depends on:** 46, 71
**PRD:** FR-PLAY-02, FR-PLAY-03, FR-PLAY-04
- A live-editable inspector with a filter and last-step highlights.
- An author toggle that shows hidden choices with the failing sub-expression.
- History, step back that restores exact state, reset, and text export of the history.

**Done when:** FR-PLAY-02 AC1–AC3, FR-PLAY-03 AC1–AC2, and FR-PLAY-04 AC1–AC2 pass.

### 73. Saved play states
**Status:** todo · **Depends on:** 72
**PRD:** FR-PLAY-05
- The `play_state` entity and commands, scoped to the project and visible to the workspace. Loading reports dropped values.

**Done when:** FR-PLAY-05 AC1–AC2 pass.

### Milestone L — Comments

### 74. Threaded comments
**Status:** todo · **Depends on:** 50, 61
**PRD:** FR-COMMENT-01, FR-PROJ-03 (comments indexed)
- Comments on nodes, connections, boards, and variables. Replies, edit, delete own, resolve and unresolve.
- Canvas indicators. A project comment list with filters.
- Comments are soft-deleted and restored with their node. They are added to search.

**Done when:** FR-COMMENT-01 AC1–AC3 pass.

### 75. Mentions and notification centre
**Status:** todo · **Depends on:** 56, 74
**PRD:** FR-COMMENT-02 (MVP part)
- `@member` mentions are validated against project access. In-app notifications go over the real-time channel. A notification centre with read state. Email arrives in item 130.

**Done when:** FR-COMMENT-02 AC1–AC2 pass.

### Milestone M — Snapshots, sync server, and data portability

### 76. Deterministic serializer
**Status:** todo · **Depends on:** 41, 47
**PRD:** §6.2, §6.3, FR-SNAP-03 (AC1, AC3), FR-EXPR-03 (AC1, AC2), FR-TEXT-03 (AC2), FR-EDIT-03 (AC1, exported JSON), FR-EDIT-04 (AC2), R4
- Serialises the draft into the published layout: sorted keys, no HTML escaping, number formatting rules, positions rounded to 2 decimals, semantic array order, omitted optional fields, and no personal data.
- Manifest with the `boards` index, per-file SHA-256, and content hash. Mentions resolved to names plus a machine-readable reference.
- Byte-equality tests across runs and on all three CI operating systems. Property tests for determinism.
- No dependency on the canvas or play mode (§12.6 puts P11 after P6 and P5 only). The cross-check against the TypeScript projection of item 71 runs in item 89.

**Done when:** FR-SNAP-03 AC1 and AC3, FR-EXPR-03 AC1 and AC2 (a rename changes only the debug `name` fields), FR-TEXT-03 AC2 (a mention exports as the component's name plus its ID), FR-EDIT-03 AC1 and FR-EDIT-04 AC2 (exported order matches the author's order, and reordering outputs changes evaluation order in the conformance runner) pass, with identical bytes on Linux, Windows ARM64, and macOS.

### 77. Schema versioning and migration framework
**Status:** todo · **Depends on:** 76
**PRD:** FR-DATA-03, §6.4, FR-SYNC-07 (server range)
- Pure forward migrations of stored content. Down-rendering to the previous schema version. A migration log stored with the project.
- Fixture projects per published schema version in `/schema/fixtures/vN/`.
- The server declares its supported schema range. Only schema version 1 exists in the MVP, so a test-only synthetic version exercises the machinery (FR-DATA-03). It is numbered one above the newest real version (2 today) and is renumbered before a real version takes that number. Its migration, renderer, and fixtures live in a repository test-data directory outside `/schema`, shared by the Go, TypeScript, and GDScript tests.
- No implementation compiles the synthetic version in. Each one has a schema-version registry that test code extends: Go through a `synthetic_schema` build tag, TypeScript and GDScript through a test-only registration hook that lives in the test-data directory. The default Go test run does not set the tag, so most tests exercise the release configuration with version 1 as current; only the tests of the two-version criteria (N2) run with it.
- Release checks: a test asserts that the release binary's supported range is exactly the real versions, and item 107 checks the same for the packaged addon.

**Done when:** FR-DATA-03 AC1–AC3 pass.

### 78. Snapshot publish and build
**Status:** todo · **Depends on:** 48, 77
**PRD:** FR-SNAP-01, FR-DEP-04, FR-DATA-04 (AC1), FR-EDIT-05 (AC1, publish gate), FR-EDIT-03 (AC4, publish gate), §7.3
- Publish request: pre-check validation, acknowledged warnings, and `acknowledged_seq`. `publish_min_role` enforced.
- A build job in one `REPEATABLE READ` transaction that records `source_seq`, re-validates, serialises, and validates against the schema. It ends `ready` or `failed` with `validation_failed` or `validation_changed`.
- Immutability through a trigger plus revoked grants. A version index that excludes `failed` rows. A proposed patch bump. Audit of publish.

**Done when:** FR-SNAP-01 AC1–AC7, FR-DEP-04 AC1–AC4 (including the race test), FR-DATA-04 AC1, FR-EDIT-05 AC1 (a broken jumper is a validation error and blocks publishing), and the publishing half of FR-EDIT-03 AC4 (a connection whose condition fails to parse blocks publishing) pass.

### 79. Snapshot list, retrieval, and export endpoints
**Status:** todo · **Depends on:** 78
**PRD:** FR-SNAP-02 (server), FR-SYNC-01
- Snapshot list and metadata. Zip download of the published layout.
- `…/snapshots/{id}/export?schema=n`, with down-rendering and `schema_unsupported` carrying `min` and `max`.
- `…/draft/export`, always marked as draft. Scope enforcement: `snapshot:read` vs `project:read`.

**Done when:** FR-SYNC-01 AC1–AC4 pass, AC2 against the synthetic schema version. FR-SNAP-02 AC1 compares with an addon pull and lands in item 99.

### 80. Entity diff service, diff corpus, and diff endpoint
**Status:** todo · **Depends on:** 79
**PRD:** FR-SNAP-04, FR-SYNC-02, §7.4, FR-DATA-04 (diff schema)
- An entity diff over published file sets: added, removed, modified, moved, and cosmetic. Field before and after values. Word-level text diffs. A flag for whether an expression's referenced entities changed.
- The diff JSON Schema. `/schema/diff-corpus` with pairs of file sets and their expected diffs.
- `POST /projects/{id}/diff` with truncation that keeps summary counts.

**Done when:** FR-SNAP-04 AC1–AC4 (Go side) and FR-SYNC-02 AC1–AC2 pass.

### 81. Releases UI
**Status:** todo · **Depends on:** 70, 80
**PRD:** FR-SNAP-01/02/04 (UI), FR-DEP-04 (UI), §7.3 versioning
- Publish dialog: validation report, warning acknowledgement, required note, and a proposed version with a major-bump suggestion based on the diff.
- Release list with build status. Read-only snapshot viewing that cannot emit commands. Zip download. Web diff view.

**Done when:** FR-SNAP-02 AC2 passes and the J4 flow has Playwright coverage.

### 82. Text export
**Status:** todo · **Depends on:** 68, 79
**PRD:** FR-TEXT-05
- CSV (RFC 4180, UTF-8 with BOM, formula-injection guard) and JSON exports for a project or a board, from a snapshot or the draft. Columns as specified. A stable text hash computed over the original text.

**Done when:** FR-TEXT-05 AC1–AC4 pass.

### 83. Authoring export
**Status:** todo · **Depends on:** 29, 73, 74, 79
**PRD:** FR-DATA-01 (export: AC5, AC6), FR-DATA-04 (authoring schema), FR-EDIT-06 (AC3), FR-AUTH-04 (AC7)
- A zip export built by a job, with a documented format and schema version. Contents: draft or snapshot, comments, play states, an `annotations` section (empty until item 127), the release history with files, and optional assets, including node cover images.
- The archive schema defines the annotation item from its first version, with the §5.3 `annotation` fields (ID, board, kind `note` or `frame`, geometry, text), so a v1 archive validates against it (N4).
- Full and content-only profiles, in the dialog and as an API parameter. Personal-data notices in the dialog, the API docs, and the archive README. Archive size shown, with a warning when it exceeds the instance's import limit.
- Requires the `export:read` token scope.

**Done when:** FR-DATA-01 AC5–AC6, FR-EDIT-06 AC3, and FR-AUTH-04 AC7 (now that the snapshot, draft, and authoring exports all exist) pass, and the archive validates against its schema.

### 84. Project import
**Status:** todo · **Depends on:** 83
**PRD:** FR-DATA-01 (import), NFR-SEC-13, §5.2 ID policy, FR-ADMIN-06 (`export`, `import`)
- Resumable chunked upload to blob storage, limited by `STORYCREEP_IMPORT_MAX_BYTES`. Zip-bomb and path-traversal guards.
- An ID-collision check through the maintenance path (N20), returning one boolean, then `preserve` or `remap`.
- Expressions recompiled from source. Imported releases verified: file hashes, content hash, schema validity, references, ASTs, and `astVersion`. All or nothing.
- An "imported user" identity. Create-project authorisation and the `projects` limit. Audit and rate limits. CLI `export` and `import`.
- A section the server cannot import, such as a non-empty `annotations` section before item 127, refuses the archive and names the section. Test builds can declare an extra section that no importer handles, which item 127 uses for AC12 once annotations import.
- One section registry shared by export and import. A registry test fails on a table registered in only one direction (standing checklist).

**Done when:** FR-DATA-01 AC1–AC4 and AC7–AC12 pass, AC2 and AC3 against the synthetic schema version, and the registry test passes.

### 85. Sample project and project duplication
**Status:** todo · **Depends on:** 49, 84
**PRD:** FR-PROJ-05, FR-PROJ-01 (AC1), FR-DEP-02 (AC1), FR-SNAP-03 (AC2)
- *The Lighthouse Keeper* stored as an export archive in the repository: 3 boards, about 40 nodes, 6 variables, 4 components, cross-board gating, one quest, and at least one deliberate warning. One-click import.
- Duplication implemented as an internal `remap` copy with `duplicated_from`.
- The dependency-view fixture graph of the sample.

**Done when:** FR-PROJ-05 AC1–AC2, FR-PROJ-01 AC1, FR-DEP-02 AC1, and FR-SNAP-03 AC2 pass.

### 86. Workspace export
**Status:** todo · **Depends on:** 84
**PRD:** FR-DATA-02
- An owner-only job that exports all projects, the member list, comments, the audit log, and assets. A time-limited, authenticated download. A documented JSON format.

**Done when:** FR-DATA-02 AC1–AC3 pass.

### 87. Personal data export
**Status:** todo · **Depends on:** 86
**PRD:** FR-GDPR-01, §9.6
- A self-service job that produces an archive following the "In export" column of the §9.6 inventory. Credentials appear as metadata only. Other actors appear as role labels.

**Done when:** FR-GDPR-01 AC1–AC4 pass.

### Milestone N — Offline and PWA

### 88. Installable PWA shell
**Status:** todo · **Depends on:** 60
**PRD:** FR-OFF-01
- Web app manifest and icons. A Workbox shell cache versioned per build, with a reload prompt that does not break an edit in progress. No stale API caching.
- Online-only mode in insecure contexts, with the reason shown and no service-worker errors.

**Done when:** FR-OFF-01 AC1–AC4 pass.

### 89. Client-side analysis engine and projection cross-check
**Status:** todo · **Depends on:** 44, 48, 49, 71, 76
**PRD:** FR-OFF-05 (prerequisite), §6.2 (projection cross-check, N10)
- TypeScript ports of the dependency index, the validation rules, and the dependency graph.
- Cross-checked against the Go services on generated fixtures in CI.
- The play-mode projection of item 71 is cross-checked against the Go serializer of item 76: both produce the same published entities for the generated fixtures. This is the first item where both exist.

**Done when:** TypeScript and Go produce identical findings and graphs for `small` and `medium`, and the projection cross-check is green.

### 90. Offline pinning and local entity store
**Status:** todo · **Depends on:** 56, 71, 88
**PRD:** FR-OFF-02, NFR-PERF-03
- *Available offline* toggle. The last opened project is pinned automatically. A full download into IndexedDB, kept current from incoming operations.
- A storage panel. `navigator.storage.persist()`. Quota failures reported with no partial cache.
- The canvas harness measures time to interactive when opening a cached pinned project.

**Done when:** FR-OFF-02 AC1–AC3 pass, and the harness reports NFR-PERF-03.

### 91. Offline editing queue and sync status
**Status:** todo · **Depends on:** 90
**PRD:** FR-OFF-03, FR-OFF-06
- A durable command queue in IndexedDB using the shared command layer from item 56. Undo and redo work offline.
- Server-only actions are disabled with a reason.
- A four-state indicator and a panel listing pending commands, with JSON export.

**Done when:** FR-OFF-03 AC1–AC4 and FR-OFF-06 AC1–AC2 pass.

### 92. Reconnect replay and conflict review queue
**Status:** todo · **Depends on:** 91
**PRD:** FR-OFF-04, ADR-007
- Replay by local unit. Multi-batch only for units with disjoint entity sets. Units that touch a conflicted entity are held behind it.
- A persistent review queue: keep-mine (new `command_id`, current revision, `supersedes`), keep-theirs, side by side, and "target deleted".
- The queue shrinks only through resolution or a successful apply.

**Done when:** FR-OFF-04 AC1–AC7 pass.

### 93. Offline play mode, validation, and dependency view
**Status:** todo · **Depends on:** 70, 71, 89, 90
**PRD:** FR-OFF-05
- Play mode, the validation panel, and the dependency view run on local data through the item-89 engine.

**Done when:** FR-OFF-05 AC1 passes (parity with the server for the same content).

### Milestone O — Godot addon

### 94. Addon scaffold, GUT, and headless CI matrix
**Status:** todo · **Depends on:** 1, 10
**PRD:** FR-GODOT-10 (AC1–AC3), §8.1, ADR-022, FR-I18N-01 (addon)
- `addons/storycreep/` with an MIT licence, the editor plugin, and `runtime/` separated so exported builds carry only the runtime.
- Addon message catalog. GUT. A headless CI matrix over every Godot minor from 4.3 to the newest stable, with the newest pre-release allowed to fail.
- An older-than-minimum warning that disables network actions. An untested-newer notice that keeps working.

**Done when:** FR-GODOT-10 AC1–AC3 pass.

### 95. Connection settings and token handling
**Status:** todo · **Depends on:** 26, 79, 94
**PRD:** FR-GODOT-01, NFR-SEC-18, §8.3
- `res://storycreep/connection.json` with no secrets. Tokens in `EditorSettings` under `storycreep/tokens/<instance key>/<project ID>`, with a `default` fallback.
- An async HTTP layer tested against the API contract fixtures. TLS validation. Plain HTTP allowed only to private addresses unless explicitly opted in.
- Dock connection tab showing the user and workspace.

**Done when:** FR-GODOT-01 AC1–AC5 pass.

### 96. Releases browser and explicit-pull guarantees
**Status:** todo · **Depends on:** 95
**PRD:** FR-GODOT-02, FR-SYNC-03
- Workspaces, projects, and releases, with the local pin and update availability. Async with cancel.
- Opt-in, metadata-only update checks. No auto-download and no auto-apply.

**Done when:** FR-GODOT-02 AC1–AC2 and FR-SYNC-03 AC1–AC2 pass. AC3 is in item 99.

### 97. Local snapshot cache and modification detection
**Status:** todo · **Depends on:** 96
**PRD:** FR-SYNC-08, FR-SYNC-04
- Cache at `user://storycreep/cache/{projectId}/{snapshotId}/` with retention and "clear cache". The pinned snapshot is never pruned.
- Archive checks (path rules and hashes) before anything is kept. Local files re-hashed against the manifest. Optional backup to `user://storycreep/backup/{timestamp}/`.

**Done when:** FR-SYNC-08 AC1–AC2 and FR-SYNC-04 AC1–AC3 pass.

### 98. GDScript entity diff and changes view
**Status:** todo · **Depends on:** 80, 97
**PRD:** FR-GODOT-03 (diff: AC2, AC4, AC7), FR-SNAP-04 (AC4, GDScript side)
- Local diff between `res://storycreep/` and the cached target, run off the main thread.
- A virtualised changes tree grouped by board, with filters, word-level text diffs, and a locally-modified section. Apply stays disabled until the diff for that target has been shown.

**Done when:** FR-GODOT-03 AC2, AC4, and AC7 pass (identical output on the shared diff corpus).

### 99. Planned atomic apply and crash recovery
**Status:** todo · **Depends on:** 98
**PRD:** FR-GODOT-03 (apply: AC1, AC3, AC5, AC6, AC8), FR-SYNC-03 (AC3), FR-SNAP-02 (AC1), §7.5
- Path rules. Preflight. Stage in `.apply/new/`. A checksummed plan. Swaps with the manifest last. The `committed` marker. Idempotent cleanup.
- Recovery at addon start that reconciles against the filesystem. A blocking "incomplete apply" state.
- Deletion limited to the current manifest's files. A confirmation that names the target version. A filesystem rescan.

**Done when:** FR-GODOT-03 AC1 (full fault-injection matrix), AC3, AC5, AC6, and AC8, FR-SYNC-03 AC3, and FR-SNAP-02 AC1 (the web zip download matches the files the addon applies for the same snapshot and schema version) pass.

### 100. Validate and repair
**Status:** todo · **Depends on:** 99
**PRD:** FR-GODOT-04
- Checks: hashes, schemas, schema support, cross-file references (including that every `boards` index file appears in `files`), unmanaged files, `null` values, unknown constructs, and the export-preset filter.
- Repair re-pulls the pinned version. A status tab.

**Done when:** FR-GODOT-04 AC1–AC3 pass.

### 101. Rollback, draft pulls, and schema negotiation
**Status:** todo · **Depends on:** 77, 100
**PRD:** FR-SYNC-05, FR-SYNC-06, FR-SYNC-07 (AC1, AC2, AC4), FR-EXPR-04 (AC4, pull side), §7.6, §7.8
- Version history with rollback through the same flow. Works offline with a warm cache.
- "Pull working draft" (amber, its own confirmation, `Pin` disabled, persistent indicator). Policy-pin checks.
- Negotiates the highest common schema version. Refuses an unsupported `astVersion` before any write. A labelled file-level fallback when no entity diff is possible.

**Done when:** FR-SYNC-05 AC1–AC3, FR-SYNC-06 AC1, AC2, and AC4, FR-SYNC-07 AC1, AC2, and AC4 (against a test server that serves the synthetic schema version), and FR-EXPR-04 AC4 (pull) pass.

### 102. Runtime loader
**Status:** todo · **Depends on:** 99
**PRD:** FR-GODOT-05, FR-SYNC-07 (AC3), FR-SYNC-06 (AC3), FR-EXPR-04 (AC4, load side), §8.4.1, NFR-PERF-14
- `StorycreepProject` with lazy board loading through the `boards` index and parsed-board caching. Works in packed exports. No `res://` writes.
- Schema and `astVersion` checks, a check of every `call` name, and number typing by declaration at load. One warning per load for draft data.
- A GUT benchmark of manifest load and per-board load on the `medium` fixture, run in CI and on the reference Android device by the checklist of item 119.

**Done when:** FR-GODOT-05 AC1–AC4, FR-SYNC-07 AC3, FR-SYNC-06 AC3, and FR-EXPR-04 AC4 (load) pass, and the benchmark reports NFR-PERF-14.

### 103. GDScript evaluator and runtime flow API
**Status:** todo · **Depends on:** 42, 102
**PRD:** FR-GODOT-06, FR-PLAY-06 (GDScript side, completes it), FR-EXPR-04 (AC1, GDScript), FR-COMP-03 (AC1), FR-VAR-02 (AC3, AC4), FR-TEXT-02 (AC3), FR-EDIT-04 (AC1, Godot runtime), NFR-PERF-15
- An AST evaluator with the §5.8.9 semantics (own float parsing where needed, the Unicode tables).
- `StorycreepRuntime`, `StorycreepNode`, and `StorycreepChoice` as in §8.4, with scope-aware variable addressing and every signal in §8.4.3.
- Conformance and corpus runners under every Godot minor in CI.
- A GUT benchmark of `advance` and `choose` that also asserts no parsed expression is allocated per step.

**Done when:** FR-GODOT-06 AC1–AC5 and FR-PLAY-06 AC2 pass across Go, TypeScript, and GDScript, and the Godot halves of FR-EDIT-04 AC1 (`dead_end`), FR-COMP-03 AC1, FR-VAR-02 AC3–AC4, and FR-TEXT-02 AC3 pass. The benchmark reports NFR-PERF-15.

### 104. Runtime diagnostics and strict mode
**Status:** todo · **Depends on:** 103
**PRD:** FR-GODOT-08
- `runtime_error` with stable codes, messages, and context. Documented non-strict fallbacks. Halting in strict mode. Each error printed once.

**Done when:** FR-GODOT-08 AC1–AC3 pass.

### 105. Narrative save and load
**Status:** todo · **Depends on:** 103
**PRD:** FR-GODOT-07, FR-COMP-03 (AC3), §8.5
- `save_state()` and `load_state()` with dropped, defaulted, retyped, and cursor-fallback reporting. `stateVersion` and project checks. Numbers normalised by declaration.
- Runtime attributes saved by component ID and attribute-definition ID, never by key.

**Done when:** FR-GODOT-07 AC1–AC4 pass, including the JSON round-trip, and FR-COMP-03 AC3 passes (a save survives renaming the component and attribute keys).

### 106. Example project and GUT runtime suite
**Status:** todo · **Depends on:** 85, 104, 105
**PRD:** FR-GODOT-09
- `/godot/example`: a dialogue UI with text, speaker, choice buttons, and save/load, driven by a published snapshot of the sample project. It runs after cloning with no configuration.

**Done when:** FR-GODOT-09 AC1–AC2 pass.

### 107. Godot version policy and addon packaging
**Status:** todo · **Depends on:** 106
**PRD:** FR-GODOT-10 (AC4), §8.1, §8.7
- A release zip, Asset Library metadata, and the declared tested range. A release checklist that fails when a stable Godot minor older than 30 days is missing from the matrix.
- A packaging check that the zip contains no file from the test-data directory of item 77 and that the packaged addon declares `supported_schema.max` equal to the newest real schema version (FR-DATA-03).

**Done when:** FR-GODOT-10 AC4 passes, an addon zip is produced by CI, and the packaging check fails on a fixture zip that includes the synthetic version's registration hook.

### Milestone P — Public presentation and documentation

### 108. Public site, SEO, and presentation toggle
**Status:** todo · **Depends on:** 53, 60
**PRD:** FR-PUB-01, FR-PUB-02, FR-PUB-04, NFR-SEO-01..06, NFR-PERF-16, FR-ADMIN-01 (AC2)
- An Astro static site built from the shared tokens and embedded in the Go binary: `/`, `/features`, `/self-hosting`, `/changelog` (generated from release-note files in the repository; the feed arrives in item 122), and legal templates. No app JavaScript. Outbound links configurable at build time.
- Per-page metadata, JSON-LD, `sitemap.xml`, `robots.txt`. Lighthouse CI.
- `STORYCREEP_PUBLIC_SITE` on and off. When off, `/` serves the login screen.

**Done when:** FR-PUB-01 AC1–AC5, FR-PUB-02 AC1–AC2 and AC3 for `/`, FR-PUB-04 AC1–AC3, and FR-ADMIN-01 AC2 (both modes) pass. The docs-page half of FR-PUB-02 AC3 lands with the docs in item 109.

### 109. Docs pipeline and generated references
**Status:** todo · **Depends on:** 7, 40, 41, 108
**PRD:** FR-PUB-06 (AC1–AC3), FR-PUB-02 (docs pages)
- `/docs` with MDX, Pagefind search, and versioning per release.
- Generated references with drift gates: the expression language from the built-in table and grammar, the JSON Schemas, and the REST API from OpenAPI.
- Docs pages join the SEO build tests and Lighthouse CI.

**Done when:** FR-PUB-06 AC1–AC3 pass, FR-PUB-02 AC1 holds for the docs pages, and FR-PUB-02 AC3 passes on a representative docs page.

### Milestone Q — Administration, privacy, and hardening

### 110. Workspace deletion
**Status:** todo · **Depends on:** 75, 87
**PRD:** FR-TEN-05, FR-ASSET-01 (AC3)
- Soft deletion that blocks access immediately, cancellable during the grace period. A hard-delete job through the maintenance path (N20) that removes every table except `audit_entry`, plus all blobs.
- The AC2 test seeds every tenant table that exists at this point, including notifications (item 75), export and import records (items 83–86), and personal-data exports (item 87). Later tenant tables join it (standing checklist).

**Done when:** FR-TEN-05 AC1–AC3 and FR-ASSET-01 AC3 pass.

### 111. Account deletion
**Status:** todo · **Depends on:** 110
**PRD:** FR-GDPR-02
- A sole-owner precondition that returns 409 `sole_owner`. Before v1's ownership transfer exists, the owner hands over by promoting another member to `owner` (item 20) or by deleting the workspace. An owner whose deletion is pending is not counted as an owner, both here and in the last-owner rule of item 19. A soft delete with a cancel link signed so it works without logging in. Hard deletion through the maintenance path (N20) to a tombstone identity, with pseudonymous audit actors.

**Done when:** FR-GDPR-02 AC1–AC4 pass, including the two-owner fixture of AC4.

### 112. Retention and operation-log compaction
**Status:** todo · **Depends on:** 28, 110
**PRD:** FR-GDPR-03, FR-TEN-07 (retention job)
- Configurable retention for the operation log (compaction with history summaries), the audit log (maintenance path, N20), trash, and idempotency records. Retention runs are logged.
- `applied_command` is never touched.

**Done when:** FR-GDPR-03 AC1–AC3 pass.

### 113. Backup, restore, doctor, and support bundle
**Status:** todo · **Depends on:** 84
**PRD:** FR-DATA-05, FR-ADMIN-06 (`backup`, `restore`, `doctor`, AC1), NFR-BAK-01, NFR-OBS-05, R13
- `storycreep backup` produces a consistent online archive of the database plus assets. `restore` refuses a non-empty instance without a flag.
- `doctor` checks the database, migrations, storage, SMTP, and disk. A redacted support bundle. The time of the last backup is recorded for the admin warning.
- This adds the last CLI subcommands, so a test runs every subcommand of FR-ADMIN-06 with `--help` and with a failing input.

**Done when:** FR-DATA-05 AC1–AC3 (the seeded instance includes published snapshots) and FR-ADMIN-06 AC1–AC2 pass, and the CI drill runs.

### 114. Instance admin screens and job visibility
**Status:** todo · **Depends on:** 55, 83, 111, 113
**PRD:** FR-ADMIN-03, FR-JOBS-02, FR-AUTH-03 (AC5, screen)
- Version, migrations, queue health, storage, a no-backup warning, user and workspace lists with suspend and delete, an email test, redacted configuration, and reset and verification links, which item 18's API issues on demand (N26). The instance-wide counts come through the maintenance path (N20).
- A jobs screen with retry and cancel. Users see the status of jobs they triggered.

**Done when:** FR-ADMIN-03 AC1–AC3 and FR-JOBS-02 AC1–AC2 pass.

### 115. Observability completion
**Status:** todo · **Depends on:** 52, 78
**PRD:** FR-ADMIN-05 (AC2), NFR-OBS-02, NFR-OBS-03, NFR-OBS-04
- The full metric set, including WebSocket broadcast lag, snapshot build duration, pool saturation, and offline-replay conflicts.
- OpenTelemetry tracing, off by default. An opt-in error-reporting hook that sends nothing unless the admin configures it.

**Done when:** FR-ADMIN-05 AC2 passes.

### 116. MVP documentation content
**Status:** todo · **Depends on:** 107, 109, 113
**PRD:** FR-PUB-06 (content), NFR-SEC-15 (TLS and Caddy docs), NFR-A11Y-02 statement, ADR-011 licensing FAQ, §9.1 scale wording
- Getting started, self-hosting (including the Caddy variant), backup and restore, the narrative model, the Godot addon guide, a licensing FAQ, an accessibility statement, and "tested up to" scale wording.

**Done when:** a new reader can reach a first published snapshot from the docs alone. This is exercised in item 118.

### 117. Security and privacy hardening
**Status:** todo · **Depends on:** 112, 114
**PRD:** §9.5 (all), FR-TEN-07 (AC2), FR-ENT-01 (AC1), FR-TEN-03 (AC1, AC3), FR-TEN-04 (AC4), SC5, ADR-026
- A complete cross-tenant suite over every table. Route-capability and scope enumeration rechecked. A log grep for seeded secrets.
- `govulncheck`, `pnpm audit`, `osv-scanner`, and container scanning. A test that no unexpected outbound connections are made.
- The audit event coverage check. A §9.5 checklist review recorded in `docs/`.
- An entitlement check over every key in the plan file: each active limit has exactly one enforcement site with a test, and no enforcement site passes the inert `snapshot_retention` to `Check` or `Limit`.
- A permission-coverage check: the permission-matrix test (item 20) has rows for every §5.6 operation that exists in the MVP, the viewer test covers every mutating route, and the guest test of FR-TEN-04 AC4 (item 25) covers every MVP workspace-scoped operation, including managing other members' tokens (26), import (84), workspace deletion (110), and retention settings (112). v1 operations join in their own items.

**Done when:** every NFR-SEC row has a test or a recorded review, and FR-TEN-07 AC2, FR-ENT-01 AC1, FR-TEN-03 AC1 and AC3, and FR-TEN-04 AC4 pass over the full MVP operation set.

### 118. End-to-end journeys and clean-host smoke test
**Status:** todo · **Depends on:** 9, 72, 75, 81, 92, 93, 101, 107, 108, 116, 117
**PRD:** NFR-Q-03, §3 J1 (self-hosted), J2–J7, FR-ADMIN-07 (AC3)
- Playwright journeys on desktop, tablet-touch, and offline profiles. A Godot-side script for J5 and J6.
- The compose smoke test: sign in, create a project, publish, export.
- The documentation walkthrough of item 116: a clean host reaches a first published snapshot by following the docs only (§12.1 exit criterion 5).

**Done when:** all journeys, FR-ADMIN-07 AC3, and the documentation walkthrough pass in CI.

### 119. MVP release pipeline and exit review
**Status:** todo · **Depends on:** 1–118
**PRD:** §12.1 exit criteria, FR-ADMIN-04 (AC2, release seed), FR-API-01 (AC3, release baseline), NFR-Q-02 (gate), NFR-PERF-01..15
- Release workflow: multi-arch images tagged with the release, which `deploy/docker-compose.yml` then runs instead of `main` (N18), the addon zip, a changelog, versioned docs, the traceability report, and the OpenAPI compatibility baseline.
- The traceability report becomes a hard gate. The upgrade-test job from item 5 switches its seed from the main branch to the previous release once this release exists, and the OpenAPI gate from item 7 switches its baseline to this release's document and drops the pre-release override.
- The reference-device performance checklist (tablet and Android phone) and the exit-criteria review. Every NFR-PERF-01…15 target is already measured by its owning item (see the performance table); this item records the results on the reference devices.

**Done when:** every §12.1 exit criterion is checked off with evidence.

---

## Phase 1 — v1

Each Phase 1 item depends on 119 (the MVP release) as a phase gate, plus any listed technical prerequisites. To pull an item forward, replace 119 with its true prerequisites and record that in the change log.

### 120. Czech catalog for the tool, server, and addon
**Status:** todo · **Depends on:** 119
**PRD:** FR-I18N-03 (tool, server messages and emails, addon)
- A complete Czech catalog. A contributor translation workflow. A coverage report. Missing keys fall back visibly in development and silently in production.
- Screenshot tests that catch layout breakage from longer strings.

**Done when:** FR-I18N-03 AC1–AC2 pass for the tool.

### 121. Czech marketing and docs routes
**Status:** todo · **Depends on:** 120
**PRD:** FR-PUB-08, FR-I18N-03 (marketing and key docs)
- `/cs/*` pages, reciprocal `hreflang` including `x-default`, a path-preserving language switcher, and English fallback with no empty pages.

**Done when:** FR-PUB-08 AC1–AC2 pass and the §12.2 coverage target of at least 98% is reached.

### 122. Changelog Atom feed
**Status:** todo · **Depends on:** 119
**PRD:** FR-PUB-09
- An Atom feed from the release notes behind `/changelog` (item 108). A tagged release appears after the next site build with no manual editing. Security releases appear in it (NFR-SEC-17).

**Done when:** FR-PUB-09 AC1–AC2 pass.

### 123. Two-factor authentication (TOTP)
**Status:** todo · **Depends on:** 119
**PRD:** FR-AUTH-06, §9.6 (credential metadata), FR-AUTH-04 (session-only endpoint), FR-GDPR-01 (AC3, TOTP)
- Enrolment that requires proving one code. Hashed, single-use recovery codes. A workspace policy that forces members into enrolment.
- 2FA metadata in the personal-data export. Never the secret.

**Done when:** FR-AUTH-06 AC1–AC3 pass and the FR-GDPR-01 AC3 seeding covers TOTP.

### 124. Ownership transfer
**Status:** todo · **Depends on:** 119
**PRD:** FR-TEN-06
- An owner-confirmed transfer that always leaves at least one owner. The billing-responsibility hook (inert until FR-ENT-04).

**Done when:** FR-TEN-06 AC1 passes.

### 125. Audit log UI and export
**Status:** todo · **Depends on:** 119
**PRD:** FR-TEN-08, FR-TEN-04 (AC4, audit log)
- Admin browsing with filters (actor, action, date range, project) and keyset pagination. Streaming CSV and JSON Lines export.
- Audit-log access joins the permission-matrix test and the guest test of FR-TEN-04 AC4.

**Done when:** FR-TEN-08 AC1–AC2 pass, and a guest with an `admin` grant receives 403 on the audit log.

### 126. Variable organisation
**Status:** todo · **Depends on:** 119
**PRD:** FR-VAR-05
- Folders and namespaces, bulk description editing, and sorting by usage count.

**Done when:** FR-VAR-05 AC1 passes.

### 127. Group frames and sticky notes
**Status:** todo · **Depends on:** 119
**PRD:** FR-EDIT-12, FR-DATA-01 (annotations: AC1, AC5, AC12), §5.3 `annotation`
- The `annotation` entity and its commands. Frames that move their contents in one command. Notes.
- Written to the authoring export's `annotations` section and restored by import (item 84). Never in published files. Registered with workspace hard deletion and trash.

**Done when:** FR-EDIT-12 AC1–AC3 pass, and FR-DATA-01 AC1 and AC5 pass with annotations in the archive. FR-DATA-01 AC12 keeps passing: its fixture switches to the test-only section of item 84, as the PRD prescribes, and a separate fixture shows that an archive with a frame now imports.

### 128. Auto-layout
**Status:** todo · **Depends on:** 119
**PRD:** FR-EDIT-13
- Deterministic layered layout for "tidy selection" and "tidy board", as one undoable command.

**Done when:** FR-EDIT-13 AC1–AC2 pass.

### 129. Keyboard shortcut remapping
**Status:** todo · **Depends on:** 119
**PRD:** §12.2 "keyboard remapping", FR-EDIT-10
- A per-user shortcut map over the command IDs, with conflict detection against browser-reserved combinations.

**Done when:** remapped shortcuts persist and FR-EDIT-10 AC2 still holds.

### 130. Email notifications for mentions
**Status:** todo · **Depends on:** 119
**PRD:** §12.2 "email notifications for mentions", FR-COMMENT-02
- Mention emails when SMTP is configured, with a per-user preference and localised templates.

**Done when:** a mention produces one email when SMTP is configured and none when it is not.

### 131. API deprecation policy
**Status:** todo · **Depends on:** 119
**PRD:** FR-API-05
- A deprecation marker in OpenAPI, `Deprecation` and `Sunset` headers, and a support window of at least two minor releases. Deprecations are listed in the release notes behind `/changelog` (item 108).

**Done when:** FR-API-05 AC1–AC2 pass.

### 132. Restore draft from a snapshot
**Status:** todo · **Depends on:** 127
**PRD:** FR-SNAP-05
- A diff preview with confirmation. One undoable operation. IDs kept where they match. Comments and annotations kept where their targets survive.

**Done when:** FR-SNAP-05 AC1–AC3 pass.

### 133. "Why is this gated?" explanation
**Status:** todo · **Depends on:** 119
**PRD:** FR-DEP-05
- A panel that lists the conditions along a path from the board entry and the write sites of each variable, and names the unsatisfiable condition or the absence of any path.

**Done when:** FR-DEP-05 AC1–AC2 pass.

### 134. Host functions declared by the game
**Status:** todo · **Depends on:** 119
**PRD:** FR-EXPR-05, FR-EXPR-04 (`ast_version`), §6.3 `settings.json`, §8.4.2 `register_function`
- Declarations in the project. Type checking in Go and TypeScript. Published in `settings.json`.
- Play-mode stub values persisted with the play state. Godot `register_function`, with an unregistered call returning the zero value plus `runtime_error`. Conformance scenarios.

**Done when:** FR-EXPR-05 AC1–AC2 pass in all three implementations.

### 135. Shareable playtest links
**Status:** todo · **Depends on:** 119
**PRD:** FR-PLAY-07
- Expiring, revocable, optionally password-protected links to a snapshot's play data. A feedback box that creates comments.

**Done when:** FR-PLAY-07 AC1–AC2 pass.

### 136. Headless CLI pull
**Status:** todo · **Depends on:** 119
**PRD:** FR-GODOT-11
- `addons/storycreep/cli/pull.gd` with `--version` and `--token-env`. Non-zero exit codes. Hash and policy-pin verification. No prompts.

**Done when:** FR-GODOT-11 AC1–AC2 pass.

### 137. Interactive demo sandbox
**Status:** todo · **Depends on:** 119
**PRD:** FR-PUB-03
- `/demo` lazily loads the real editor on the sample project in a separate local-store namespace, with a no-op sync transport.
- Server-only actions are disabled with reasons. Resume or start over. Clearing removes everything.

**Done when:** FR-PUB-03 AC1–AC5 pass.

### 138. Docs expansion and zero-to-Godot tutorial
**Status:** todo · **Depends on:** 121, 134, 136, 137
**PRD:** §12.2 "docs expansion and tutorials"
- A tutorial from zero to a running Godot scene. Docs for every v1 feature.

**Done when:** the tutorial is verified end to end in CI or a recorded walkthrough.

### 139. v1 release and exit review
**Status:** todo · **Depends on:** 120–138
**PRD:** §12.2 exit criteria
- Czech coverage of at least 98%. `/demo` live, where a visitor can create a gated choice and play it with no account. The tutorial is published. The traceability report shows no v1 gaps.

**Done when:** every §12.2 exit criterion is checked off with evidence.

---

## Coverage matrix

The PRs that implement each requirement are listed in order. The last one completes the requirement. Items in parentheses are later extensions that do not block completion.

| Requirement | PRs | Complete at |
| --- | --- | --- |
| FR-PUB-01 | 9, 108 | 108 |
| FR-PUB-02 | 6, 108, 109 | 109 |
| FR-PUB-03 | 137 | 137 |
| FR-PUB-04 | 9, 108 | 108 |
| FR-PUB-05 | 60 | 60 |
| FR-PUB-06 | 109, 116 | 116 |
| FR-PUB-08 | 121 | 121 |
| FR-PUB-09 | 122 | 122 |
| FR-AUTH-01 | 13 | 13 |
| FR-AUTH-02 | 14, 54 | 54 |
| FR-AUTH-03 | 18, 19, 114 | 114 |
| FR-AUTH-04 | 26, 27, 54, 83 (123) | 83 |
| FR-AUTH-05 | 15 | 15 |
| FR-AUTH-06 | 123 | 123 |
| FR-TEN-01 | 11, 16 | 16 |
| FR-TEN-02 | 19, 20, 55 | 55 |
| FR-TEN-03 | 19, 20, 55, 117 | 117 |
| FR-TEN-04 | 16, 25, 55, 117 (125) | 117 |
| FR-TEN-05 | 110 | 110 |
| FR-TEN-06 | 124 | 124 |
| FR-TEN-07 | 12, 112, 117 | 117 |
| FR-TEN-08 | 125 | 125 |
| FR-PROJ-01 | 24, 55, 85 | 85 |
| FR-PROJ-02 | 30, 35, 48, 55 | 55 |
| FR-PROJ-03 | 50, 65, 74 | 74 |
| FR-PROJ-04 | 60 | 60 |
| FR-PROJ-05 | 85 | 85 |
| FR-PROJ-06 | 31, 33, 34, 35, 36, 55 | 55 |
| FR-EDIT-01 | 59 | 59 |
| FR-EDIT-02 | 35, 36, 61 | 61 |
| FR-EDIT-03 | 36, 43, 62, 71, 76, 78 | 78 |
| FR-EDIT-04 | 43, 62, 71, 76, 103 | 103 |
| FR-EDIT-05 | 35, 62, 71, 78 | 78 |
| FR-EDIT-06 | 35, 47, 61, 83 | 83 |
| FR-EDIT-07 | 51, 64 | 64 |
| FR-EDIT-08 | 37, 56, 61 | 61 |
| FR-EDIT-09 | 53, 61, 63, 67 | 67 |
| FR-EDIT-10 | 65 (129) | 65 |
| FR-EDIT-11 | 59 | 59 |
| FR-EDIT-12 | 127 | 127 |
| FR-EDIT-13 | 128 | 128 |
| FR-TEXT-01 | 32, 36, 57, 61 | 61 |
| FR-TEXT-02 | 46, 47, 48, 66, 71, 103 | 103 |
| FR-TEXT-03 | 46, 66, 76 | 76 |
| FR-TEXT-04 | 68 | 68 |
| FR-TEXT-05 | 82 | 82 |
| FR-TEXT-06 | 69 | 69 |
| FR-COMP-01 | 34, 58 | 58 |
| FR-COMP-02 | 34, 47, 58 | 58 |
| FR-COMP-03 | 40, 43, 71, 103, 105 | 105 |
| FR-COMP-04 | 46, 47, 58, 61 | 61 |
| FR-VAR-01 | 33, 58 | 58 |
| FR-VAR-02 | 33, 40, 58, 67, 71, 103 | 103 |
| FR-VAR-03 | 47, 58 | 58 |
| FR-VAR-04 | 47, 58 | 58 |
| FR-VAR-05 | 126 | 126 |
| FR-EXPR-01 | 67 | 67 |
| FR-EXPR-02 | 39, 40, 44 | 44 |
| FR-EXPR-03 | 39, 43, 76 | 76 |
| FR-EXPR-04 | 40, 42, 44, 101, 102, 103 (134) | 103 |
| FR-EXPR-05 | 134 | 134 |
| FR-DEP-01 | 46, 49, 67 | 67 |
| FR-DEP-02 | 49, 70, 85 | 85 |
| FR-DEP-03 | 48, 70 | 70 |
| FR-DEP-04 | 78, 81 | 81 |
| FR-DEP-05 | 133 | 133 |
| FR-PLAY-01 | 71 | 71 |
| FR-PLAY-02 | 72 | 72 |
| FR-PLAY-03 | 72 | 72 |
| FR-PLAY-04 | 72 | 72 |
| FR-PLAY-05 | 73 | 73 |
| FR-PLAY-06 | 42, 44, 71, 103 | 103 |
| FR-PLAY-07 | 135 | 135 |
| FR-COMMENT-01 | 74 | 74 |
| FR-COMMENT-02 | 75 (130) | 75 |
| FR-SNAP-01 | 24, 78, 81 | 81 |
| FR-SNAP-02 | 79, 81, 99 | 99 |
| FR-SNAP-03 | 76, 85 | 85 |
| FR-SNAP-04 | 80, 81, 98 | 98 |
| FR-SNAP-05 | 132 | 132 |
| FR-SYNC-01 | 79 | 79 |
| FR-SYNC-02 | 80 | 80 |
| FR-SYNC-03 | 96, 99 | 99 |
| FR-SYNC-04 | 97 | 97 |
| FR-SYNC-05 | 101 | 101 |
| FR-SYNC-06 | 101, 102 | 102 |
| FR-SYNC-07 | 77, 101, 102 | 102 |
| FR-SYNC-08 | 97 | 97 |
| FR-GODOT-01 | 95 | 95 |
| FR-GODOT-02 | 96 | 96 |
| FR-GODOT-03 | 98, 99 | 99 |
| FR-GODOT-04 | 100 | 100 |
| FR-GODOT-05 | 102 | 102 |
| FR-GODOT-06 | 103 | 103 |
| FR-GODOT-07 | 105 | 105 |
| FR-GODOT-08 | 104 | 104 |
| FR-GODOT-09 | 106 | 106 |
| FR-GODOT-10 | 94, 107 | 107 |
| FR-GODOT-11 | 136 | 136 |
| FR-API-01 | 6, 7, 27, 119 | 119 |
| FR-API-02 | 7, 53 | 53 |
| FR-API-03 | 22, 28 | 28 |
| FR-API-04 | 6, 7, 15, 27 | 27 |
| FR-API-05 | 131 | 131 |
| FR-RT-01 | 52 | 52 |
| FR-RT-02 | 52, 56 | 56 |
| FR-OFF-01 | 88 | 88 |
| FR-OFF-02 | 90 | 90 |
| FR-OFF-03 | 56, 91 | 91 |
| FR-OFF-04 | 92 | 92 |
| FR-OFF-05 | 89, 93 | 93 |
| FR-OFF-06 | 91 | 91 |
| FR-ASSET-01 | 29, 110 | 110 |
| FR-ASSET-02 | 29 | 29 |
| FR-JOBS-01 | 8 | 8 |
| FR-JOBS-02 | 114 | 114 |
| FR-DATA-01 | 83, 84 (127) | 84 |
| FR-DATA-02 | 86 | 86 |
| FR-DATA-03 | 77 | 77 |
| FR-DATA-04 | 41, 78, 80, 83 | 83 |
| FR-DATA-05 | 113 | 113 |
| FR-ADMIN-01 | 3, 9, 108 | 108 |
| FR-ADMIN-02 | 17, 18, 29, 54 | 54 |
| FR-ADMIN-03 | 114 | 114 |
| FR-ADMIN-04 | 5, 6, 119 | 119 |
| FR-ADMIN-05 | 6, 115 | 115 |
| FR-ADMIN-06 | 3, 5, 6, 9, 17, 18, 26, 84, 113 | 113 |
| FR-ADMIN-07 | 9, 118 | 118 |
| FR-ENT-01 | 21, 23, 117 | 117 |
| FR-ENT-02 | 23, 35 | 35 |
| FR-ENT-03 | 21, 55 | 55 |
| FR-I18N-01 | 10, 53, 94 | 94 |
| FR-I18N-02 | 10, 54 | 54 |
| FR-I18N-03 | 120, 121 | 121 |
| FR-GDPR-01 | 87 (123) | 87 |
| FR-GDPR-02 | 111 | 111 |
| FR-GDPR-03 | 112 | 112 |
| §12.2 email notifications for mentions | 130 | 130 |
| §12.2 keyboard remapping | 129 | 129 |
| §12.2 docs expansion and tutorials | 138 | 138 |

Requirements outside MVP and v1 (FR-RT-03/04, FR-TEN-09, FR-PUB-07, FR-AUTH-07, FR-SNAP-06, FR-ENT-04, FR-GDPR-04/05, FR-DEP-06, FR-TEXT-07, FR-SYNC-09) are not planned here.

### Performance targets

§12.1 exit criterion 3 requires NFR-PERF-01…15. Each target is measured by the item that builds its benchmark or harness, so a miss shows up when the feature lands, not at the release review. Item 119 records the results on the reference devices. NFR-PERF-16 to 18 are listed for completeness.

| Target | Measured by |
| --- | --- |
| NFR-PERF-01, 02, 18 (canvas frame rate and memory) | 59 |
| NFR-PERF-03 (cached pinned project, time to interactive) | 90 |
| NFR-PERF-04, 05 (initial project load) | 59 |
| NFR-PERF-06 (local edit to visible result) | 61 |
| NFR-PERF-07, 09, 10, 12, 13 (server benchmarks) | 45, with each feature's benchmark added as it lands |
| NFR-PERF-08 (change visible in another session) | 56 |
| NFR-PERF-11 (dependency view render) | 70 |
| NFR-PERF-14 (Godot manifest and board load) | 102 |
| NFR-PERF-15 (Godot runtime step) | 103 |
| NFR-PERF-16 (marketing page LCP) | 108 |
| NFR-PERF-17 (idle WebSocket memory) | 52 |

---

## Alignment notes

These record where the PRD left a choice open, or where this plan reads the PRD in a specific way. An open note must be resolved, by confirming it or by amending the PRD, before the item that relies on it starts. New notes are added here as implementation raises them.

| # | Note | Status | Affects |
| --- | --- | --- | --- |
| N1 | The PRD named no JS package manager or workspace tool. | Decided: pnpm workspaces. Recorded in PRD 1.5 §10.9. | 1 |
| N2 | Only `schemaVersion` 1 exists in the MVP, but FR-DATA-03 AC1/AC3, FR-DATA-01 AC2/AC3, FR-SYNC-01 AC2, and FR-SYNC-07 AC1/AC2/AC4 need two versions. | Decided: a test-only synthetic version, numbered one above the newest real version and renumbered before a real version takes that number. Its fixtures live outside the published `/schema` tree, it is served only by test servers, and a build tag keeps it out of release binaries. Recorded in PRD 1.5 FR-DATA-03 and §6.4. | 77, 79, 84, 101 |
| N3 | Ownership transfer (FR-TEN-06) is v1, but FR-GDPR-02 (MVP) requires a sole owner to hand over before deleting their account. The PRD did not say who may grant `owner`. | Decided: only an `owner` may grant `owner`, or demote or remove an owner; admins manage roles up to `admin`. §5.6 has a separate row for this, and FR-TEN-03 AC4 tests it. The MVP handover is promoting another member, tested by FR-GDPR-02 AC4. Recorded in PRD 1.5. | 20, 111 |
| N4 | The authoring export (MVP) includes canvas annotations, which are created only by FR-EDIT-12 (v1). | Decided: the `annotations` section is reserved from the first format version and stays empty in the MVP. A server that cannot import a section's content refuses the archive and names the section (FR-DATA-01 AC12), so an MVP server never drops annotations from a v1 archive. FR-EDIT-12 AC3 adds export and import of annotations. Recorded in PRD 1.5. | 83, 84, 127 |
| N5 | Journey J4 step 3 cited FR-SNAP-05 (restore draft, v1) for the snapshot list and download link. | Fixed: PRD 1.5 cites FR-SNAP-02. | 81, 118 |
| N6 | §1.4 listed FR-API-01…06 (no FR-API-06 exists) and FR-GODOT-01…10 (omitting FR-GODOT-11). | Fixed: PRD 1.5 lists FR-API-01…05 and FR-GODOT-01…11. | — |
| N7 | FR-PUB-01 (MVP) lists `/changelog`, while FR-PUB-09 (v1) built it from release notes with a feed. | Decided: `/changelog` is generated from release notes in the MVP, tested by FR-PUB-01 AC5; FR-PUB-09 adds only the feed. NFR-SEC-17 announces security releases in the changelog, and in the feed from v1. FR-API-05 deprecations go in the release notes and do not need the feed. Recorded in PRD 1.5. | 108, 122, 131 |
| N8 | FR-ADMIN-04 AC2 (upgrade from the previous release) cannot run before a first release exists, but the traceability gate needs every MVP criterion to pass. | Decided: until the first release, the upgrade test seeds from the main branch's migrations (item 5); item 119 switches the seed to the previous release. Recorded in PRD 1.5 FR-ADMIN-04. | 5, 119 |
| N9 | FR-ENT-01 AC1 requires one enforcement site per limit, but `snapshot_retention` is enforced only by FR-SNAP-06 (SaaS). | Decided: a limit whose enforcing requirement is in a later phase is marked inert in the plan file and read by no code until then. Recorded in PRD 1.5 FR-ENT-01. | 21, 117 |
| N10 | Play mode (FR-PLAY-01 AC1) must run client-side on the published format, but the serializer is server-side. | Decided: item 71 adds a TypeScript projection of the draft into the published format, and item 89 cross-checks it against the Go serializer of item 76, so the serializer does not wait for the canvas (§12.6). No PRD change. | 71, 76, 89 |
| N11 | FR-API-01 AC3 (OpenAPI compared against the previous release) cannot run before a first release, the same problem as N8. | Decided: until the first release, the gate compares against the main branch, with a recorded per-PR override because no client depends on `v1` yet; item 119 switches the baseline. Recorded in PRD 1.5 FR-API-01. | 7, 119 |
| N12 | An owner in the 30-day account-deletion grace period still counted as an owner, so two owners could both delete their accounts and leave a workspace with none. FR-TEN-02 AC2 tested removal only. | Decided: a pending-deletion owner does not count, for the sole-owner check and the last-owner rule; FR-TEN-02 AC2 covers demotion. Recorded in PRD 1.5 FR-GDPR-02 and FR-TEN-02. | 19, 111 |
| N13 | With `STORYCREEP_SIGNUP=open`, a self-registered first user disabled first-run setup. | Decided: registration is refused with `setup_required` until setup completes, and the setup route closes when setup completes, not when a user exists. Recorded in PRD 1.5 FR-ADMIN-02 AC4 and FR-AUTH-01. | 13, 17 |
| N14 | FR-ENT-01 listed `nodes per project` and `API requests per minute` as limits, which ADR-010 does not, and the plan enforced an unlisted `boards` limit. | Decided: the plan file holds exactly the ADR-010 keys. Boards, nodes, and API requests are metered only; request rates are bounded by the operational rate limits. An inert limit is one no enforcement site checks; reading the plan file as data is allowed. Recorded in PRD 1.5 FR-ENT-01 and FR-ENT-02. | 21, 23, 30, 117 |
| N15 | FR-DATA-01 AC12 was defined against "a server without FR-EDIT-12", which no longer exists after item 127, and the MVP schema did not define annotation items. | Decided: the first archive schema defines the annotation item, and after item 127 the AC12 fixture uses a test-only section that no importer handles. Recorded in PRD 1.5 FR-DATA-01. | 83, 84, 127 |
| N16 | Migrations need a superuser, because they create the `BYPASSRLS` maintenance role, but the application must connect as a non-superuser (§5.4). The PRD did not say where each login's credentials come from. | Decided: two URLs. `STORYCREEP_DATABASE_URL` is the application's login and `STORYCREEP_DATABASE_ADMIN_URL` the superuser that applies migrations. Migrating creates or updates the application login from its URL, with the password the server logs in with, sending a SCRAM verifier rather than the password, and grants it `storycreep_app`. Because roles belong to the cluster, every Storycreep database on a server grants its privileges to the same `storycreep_app`, so migrating also revokes `CONNECT` on the database from `PUBLIC` and from `storycreep_app`, grants it to the application login, and to the admin login if the revocation took its `CONNECT`, and fails if `PUBLIC` or `storycreep_app` can still connect, which keeps the login of one Storycreep database out of another. A server that does not apply migrations itself refuses a migrated database that `PUBLIC` or `storycreep_app` can connect to again, as after a dump is restored into a new database. It then connects as that login and fails unless the database it reaches has the migrations. The admin URL must name the same database, and its login must not be, or act as, one of the server's roles, which the first migration makes unable to log in. The server refuses an application login that is a superuser, has `BYPASSRLS`, `CREATEROLE`, or `REPLICATION`, or owns the database or objects in it, directly or through a role, or that is a member of `storycreep_definer`, `storycreep_maintenance`, or one of the predefined roles that give access to the database server's files and programs (`pg_execute_server_program`, `pg_read_server_files`, `pg_write_server_files`). Migrating also refuses an application login named after one of the server's roles or with PostgreSQL's reserved `pg_` prefix, and, when the server logs in with a password, one whose `VALID UNTIL` has passed. How the maintenance pool logs in, including its grant of `CONNECT`, is left to item 11. Recorded in PRD 1.6 §5.4 and §10.8. | 5, 6, 9, 11 |
| N17 | PRD §10.3 serves `/metrics` "unauthenticated on the admin port" in self-hosted mode and "protected" in hosted mode, without saying where the admin port is or what protects it. NFR-SEC-09 sends HSTS "in hosted mode", and FR-ADMIN-04 AC3 does not say how an instance whose startup migration failed refuses traffic. | Decided: the admin port is a second listener, `STORYCREEP_ADMIN_ADDR` (`127.0.0.1:9090` by default, or `off`), that serves only `/metrics`. Protected means a bearer token, `STORYCREEP_METRICS_TOKEN`, and setting the token protects `/metrics` in self-hosted mode too. Without a token, hosted mode serves no metrics and opens no admin port, which FR-ADMIN-05 AC3 allows ("disabled or protected"). HSTS is sent whenever `STORYCREEP_PUBLIC_URL` is https, which hosted mode requires, as the `Secure` cookie attribute follows the URL (NFR-SEC-04). An instance whose startup migration fails closes its listeners and exits with status 1; until its migrations are applied, `/readyz` and the API answer 503. No PRD change. | 6, 9, 115 |
| N18 | FR-ADMIN-01 AC4 allows no build toolchain in the image, and FR-ADMIN-07 asks for a health check of the server, which an image without a shell or an HTTP client cannot run. FR-ADMIN-07 AC1 allows no configuration beyond a database password, but the server needs two database logins (N16) and a public URL. AC2 publishes images before any release exists. | Decided: the image is the binary on distroless `static`, running as the non-root user 65532, and the binary gains a `healthcheck` subcommand, which requests `/readyz`, because FR-ADMIN-06's list of subcommands is a minimum. The compose file's one required setting is `POSTGRES_PASSWORD`, which both logins share through `PGPASSWORD`. It publishes the server on `127.0.0.1:7777`, with `http://localhost:7777` as the public URL, for a reverse proxy on the same host that terminates TLS (§10.8). Until the first release, every CI run on the main branch, after a push and weekly, publishes `ghcr.io/cybercute-studio/storycreep` from its commit, tagged `sha-<commit>`, and `main` unless `main` has a newer commit already, and the compose file runs `main`; item 119 adds release tags and points the compose file at the release. The compose file mounts the asset volume at `/var/lib/storycreep/assets`, which item 29's local driver uses in the image. No PRD change. | 9, 29, 119 |
| N19 | FR-I18N-02 resolves a locale from "user preference → `Accept-Language` → instance default → English" without saying where the instance default is set, and FR-I18N-01 localises "API error messages intended for display" without saying which parts of the error envelope those are. Nor does it say whether the server and the web app format ICU messages alike. | Decided: the instance default is the configuration key `STORYCREEP_DEFAULT_LOCALE` (`en` by default), which accepts only a locale that the server has a catalog for. The envelope's `message` is localised, and the response's `Content-Language` names its language; `details`, including the validation messages in `details.errors`, is for programs and stays in English. The server formats a subset of ICU MessageFormat (no dates or times yet), held to the web app's i18next-icu by shared vectors in `/schema/i18n`, and the web app formats dates and relative times with `Intl`. No PRD change. | 10, 18, 54, 120 |
| N20 | N16 left to item 11 how the maintenance pool logs in. PostgreSQL does not pass `BYPASSRLS` on to the members of a role, and the application login must not be a member of `storycreep_maintenance` (§5.4), so the pool needs a login of its own that acts as the role with `SET ROLE`. | Decided: a third database URL, `STORYCREEP_DATABASE_MAINTENANCE_URL`, optional until the first maintenance job needs it, which is the first of items 84, 110, 111, 112, and 114 to land, and which then makes the server require it. When it is set, migrating provisions its login as N16 provisions the application's: it creates or updates it with the password it logs in with, sent as a SCRAM verifier, grants it `storycreep_maintenance` and `CONNECT` on the database, and refuses a login named after the server's roles or with the `pg_` prefix, one that is the application login or the admin login, and one with more privileges than the role gives it: a superuser, a role with `BYPASSRLS` itself, `CREATEROLE`, or `REPLICATION`, an owner, or a member of `storycreep_app`, `storycreep_definer`, a role that gives access to the database server's files and programs, or `pg_write_all_data`. Both logins are refused as members of `pg_write_all_data`, which may change the rows of every table past the migrations' grants, on which the application's lack of `DELETE` on `user` and `workspace`, and of `UPDATE` and `DELETE` on `audit_entry`, rests. It then connects as the login and fails unless it can act as the role and reaches a migrated database. Every transaction of the pool runs `SET LOCAL ROLE storycreep_maintenance`. Because the maintenance logins of all Storycreep databases on a server are members of `storycreep_maintenance`, migrating revokes `CONNECT` from all of the server's roles, not only `storycreep_app`, and a server that does not migrate refuses a database that any of them can connect to. The compose files set the URL to the login `storycreep_maintainer`, which shares `POSTGRES_PASSWORD` through `PGPASSWORD` (N18). Recorded in PRD 1.7 §5.4 and §10.8. | 11, 84, 110, 111, 112, 114 |
| N21 | FR-TEN-07 lists events that belong to no workspace, such as a login, a session revocation, and an account's deletion, and §5.4 asks each maintenance run, some of which span workspaces, for an audit entry, but FR-TEN-01 AC1 made `audit_entry.workspace_id` non-null under row-level security, so none of them could be recorded. §9.6 asks for a keyed hash of a failed login's unknown address without saying where the key comes from, and FR-ADMIN-07 AC1 allows no configuration beyond a database password. | Decided: `audit_entry.workspace_id` is null for an event that belongs to no workspace. A policy of its own lets the application add such an entry only from a transaction without a workspace, and no policy lets it read one yet; item 87 adds what the personal-data export needs, and the maintenance jobs read and delete them past row-level security. The hash is HMAC-SHA256 of the address in lower case, as `citext` compares it, with a random 32-byte key that the migration generates once for the database and keeps in a new cross-workspace table, `instance_key`, which the application may read but not change. Nothing needs configuring, and a backup and its restore keep the key with the hashes; a copy of the whole database holds both, which the PRD records. Recorded in PRD 1.8 FR-TEN-01, FR-TEN-07, §5.3, §5.4, and §9.6. | 12, 14, 87, 111, 112 |
| N22 | NFR-SEC-06 and FR-AUTH-05 limit requests per IP address, and FR-AUTH-02 lists each session's IP address, but the documented deployments put a reverse proxy in front of the server: the compose file publishes on `127.0.0.1:7777` for a proxy on the same host (N18), and hosted mode runs behind a load balancer (§10.8). Every request's connection then comes from the proxy, so all clients would share one address's limits, and one client could use up the limit of signing in for everyone. | Decided: a configuration key, `STORYCREEP_TRUSTED_PROXIES`, lists the addresses and CIDR prefixes that proxies connect from, none by default. A request whose connection comes from one of them is taken to come from the rightmost address in its `X-Forwarded-For` header that is not a trusted proxy, and any other request from its connection's address. That address is what the per-IP limits count, an IPv6 address by its /64, and what sessions and the audit log record. `Forwarded` (RFC 7239) is not read, because the proxies in common use set `X-Forwarded-For`. The compose file passes the key through, and the deploy README recommends Docker's default range of network addresses for a proxy that reaches the published port or the compose network. No PRD change. | 14, 15 |
| N23 | FR-AUTH-05 asks for exponential backoff and a temporary account lock after 10 consecutive failed sign-ins without saying how long a lock lasts or what backs off, and NFR-SEC-06 names the buckets of the rate limits without their limits. | Decided: each sign-in with an email address counts as a failure until it succeeds, which forgets the failures (FR-AUTH-05 AC3). The 10th in a row locks the address for a minute, and each failure after a lock ends locks it for twice as long as the lock before, up to 15 minutes; failures are forgotten 24 hours after the last one. The lock belongs to the address, folded to lower case as `citext` compares it, whether or not an account has it, so that a lock does not tell whether one does (FR-AUTH-01). A sign-in with a locked address is refused before its password is checked, with 429 `rate_limited` and `details.reason` `locked`, and is audited as a failed login with that reason. The limits are constants in the code. A minute: auth 20 per IP address, 10 per user, and 10 per email address that a registration names (signing in is limited by the lock of its address instead); reads 1,200 per IP address and 600 per user; writes 600 and 300; WebSocket connects 60 and 30. An hour: exports 60 and 30, and invitation emails 10 per mailbox, whose addresses that differ only in a +tag, in how their domain is written, or, at Gmail, in dots, count as one, from all workspaces together, which the email job counts (item 19, N27). A bucket counts fixed windows from the Unix epoch, in PostgreSQL with the database's clock, and holds its keys as HMAC-SHA256 with a key that migrating generates in `instance_key` (N21). Item 15 attaches the auth bucket to signing in, registering, and changing a password; items 18 and 26 attach it to password reset and token creation, item 26 adds the token key, and items 27 and 52 attach the other buckets. No PRD change, but for the bucket of invitation emails, which PRD 1.12 adds to NFR-SEC-06 (N27). | 15, 18, 19, 26, 27, 52 |
| N24 | PRD §5.5 resolves an instance admin to `owner` of every workspace and project when "mode = self-hosted and instance_admin_access enabled", but nothing defined that switch, and no code outside the configuration may read the mode (FR-ADMIN-01 AC3). FR-TEN-01 gives every user a personal workspace on first login without saying whether an account that already belongs to a workspace by then, as an invited one does, gets one too. Item 16's "retention placeholders" overlap item 112, which makes retention configurable. | Decided: the switch is the capability `InstanceAdminAccess`, from a new key, `STORYCREEP_INSTANCE_ADMIN_ACCESS`, `off` by default and an error in hosted mode, so that an operator opts in. Every account gets a personal workspace once, at its first sign-in, whatever it belongs to by then, named after its display name. Item 16 reserves a `retention` object in a workspace's stored settings and shows it nowhere; item 112 adds the retention settings to the API together with the jobs that apply them, so that the API never states a retention that nothing applies. Recorded in PRD 1.9 §5.5 and §10.3. | 16, 17, 112, 114 |
| N25 | FR-ADMIN-02 has first-run setup choose the signup policy, but §10.3 resolved `Signup` once at startup from configuration, so a choice made on a running instance had nowhere to go. Setup's optional SMTP and storage steps would configure settings that nothing reads before items 18 and 29. Until setup completes, whoever first reaches a new instance could complete it and make themselves its administrator. And every account gets a personal workspace at its first sign-in (N24), so the setup owner would get one besides the workspace that setup creates. | Decided: setup stores the signup policy that it chooses, `closed` by default, in a new cross-workspace table, `instance`, whose one row also records when setup completed, and `STORYCREEP_SIGNUP` overrides it for as long as it is set: without the key, the self-hosted `Signup` capability is `setup`, and registering reads the stored policy. Completing setup through `POST /api/v1/setup` needs a setup code, 80 bits of a key that migrating generates in `instance_key`, which the server logs while setup is pending. Unattended setup, from the `STORYCREEP_SETUP_` keys, needs none, and runs before the server is ready. The setup owner is an instance admin, and the setup workspace is the owner's personal workspace. A database that has accounts when it is upgraded counts as set up. The SMTP step lands in item 18 and the storage step in item 29, with the settings that they configure. Recorded in PRD 1.10 FR-ADMIN-02, FR-AUTH-01, §5.3, and §10.3. | 17, 18, 29 |
| N26 | FR-AUTH-03 surfaces links as text to instance admins without saying how they read them, and FR-ADMIN-02's SMTP step had no settings to record, while hosted mode must always send email (`EmailRequired`). A link whose secret is stored so that an admin can read it later could be used by whoever reads the database, and a reset request that sends its email itself would take longer for an address of an account (FR-AUTH-03 AC3). | Decided: the SMTP server that setup chooses is stored in the `instance` row, its password as it is, because the mailer must send it; `STORYCREEP_SMTP_URL` and `STORYCREEP_SMTP_FROM` override it while they are set, hosted mode requires them, and unattended setup records the server of `STORYCREEP_SETUP_SMTP_URL` and `STORYCREEP_SETUP_SMTP_FROM`. A link's token is stored as the SHA-256 hash of its secret, in a new cross-workspace table, `email_token`, and an emailed link's secret is made by the job that sends it, so that neither the request nor the job's arguments hold it, and the link lasts its lifetime from then, as its email says, while the job sends nothing for a request older than that lifetime. A reset request, and a registration on an instance that sends email, adds a token and a job whether or not the address has an account, and the job deletes a token without one. The job emails an account one link of a kind in 10 minutes at most: it sends nothing for a link of an account that was emailed another of that kind in the last 10 minutes which still works, as `email_token.sent_at` tells, and it locks the account first, so that jobs of one account's links that run at once see each other's emails. The request still adds its token and its job and answers 202, so that it does the same work whatever the address (FR-AUTH-03 AC3), and the earlier email's link keeps working, while a reset request every few seconds sends six emails an hour at most rather than the 600 that the address's rate limit lets through. An email that no attempt could deliver, because the SMTP server refuses its recipient's mailbox for good (a reply whose enhanced status code is 5.1.1 to 5.1.4, 5.1.6, 5.1.10, or 5.2.1), or the recipient's local part, or a domain without an ASCII form, needs SMTPUTF8, which the server lacks, cancels its job rather than retry it for a day (`jobs.Cancel`), and a cancelled job is in the dead-letter state, as a discarded one is, for 7 days (FR-JOBS-01 AC3); a full mailbox, a refusal of a relay or of the sender that a server gives in its reply to RCPT, as Postfix does by default, and a refusal without an enhanced status code, which cannot tell a mailbox from a relay or a sender, as Exim's `550 relay not permitted`, are retried, because the mailbox may empty and the operator can mend the settings. Without SMTP, a request for an emailed link answers 403 `email_not_configured` for every address, and an instance admin takes a new link with `POST /api/v1/admin/password-reset-links` or `/api/v1/admin/verification-links`, which answer 403 `email_configured` on an instance that sends email; `storycreep reset-password` prints one either way, in hosted mode too, where it is the only copyable link. `GET /api/v1/instance` is the banner flag. A workspace needs a verified address exactly when `EmailRequired` is on, and the verification gives the personal workspace that the first sign-in could not. A reset revokes every session of the account. Links open `/app/reset-password` and `/app/verify-email` with the token in the URL's fragment, which a browser does not send to the server. Recorded in PRD 1.11 FR-AUTH-03, §5.3, and §9.6. | 18, 54, 114 |
| N27 | FR-TEN-02 left open whether the `closed` signup policy lets an invitation create an account, how someone without an account accepts one, what resending does, what an emailed invitation says, and who sees the member list. Item 20 alone required `owner` for inviting an owner and removing one, while item 19 adds both operations. | Decided: under `closed`, an invitation creates no account, so only an account that exists already accepts one; under `invite` and `open`, `POST /api/v1/invites/register` registers an account with the link and accepts the invitation in the transaction that adds it, answering 202 alike whether or not the address has an account (FR-AUTH-01 AC1), which then stays as it was, with the invitation open. A signed-in user accepts with `POST /api/v1/invites/accept`, which answers the account that accepted an invitation with its membership again, so a client registers, signs in, and accepts either way. An invitation to an address is emailed where the instance sends email, its secret made by the job that sends it, as N26's links are, and its 14 days start then; otherwise its link goes as text to the admin who makes or resends it, and a link invitation's always does. The email names the instance and the role, never the workspace or the inviting user, whose names their users chose, so that no account can put text of its own in email that the instance sends to any address; `POST /api/v1/invites/lookup` shows the workspace to the link's holder. Every member with a role in the workspace, which a guest's is not (§5.5), lists the members and their roles; only owners and admins see their email addresses and the invitations. An address has one open invitation of a workspace, one neither accepted nor revoked, expired or not (409 `invite_exists`). Resending gives a new link and 14 days more, and the old link stops working at once. A workspace emails an address one invitation in 10 minutes at most, as N26 emails an account one link of a kind: the job of an invitation made or resent sooner after the workspace last emailed the address is due once those 10 minutes have passed, and sends the latest link, so that resending again and again, or revoking and inviting again, cannot flood an inbox at the 10 a minute that the `auth` bucket lets an account make. Nor can an account that owns many workspaces, or invites many addresses of one mailbox: the instance emails a mailbox 10 invitations at most in each clock hour (UTC), from all workspaces together, counted in a new bucket of the rate limits, `invite_email`, keyed by the mailbox alone (N23): the address without a +tag, with its domain in the ASCII form that the instance sends to, without the dot that a full stop at its end leaves, so that the spellings of a domain, as in full-width letters or full stops, which the instance delivers to one domain, are one, and, at Gmail, whose gmail.com and googlemail.com are one, without the dots that Gmail ignores, so that jo+1@example.com, jo+2@example.com, and so on, which most mail services deliver to one inbox, share one limit. The limit is above the 6 that one workspace can send an address in an hour. The email's job counts it once the job knows that it has an email to send, unless an earlier attempt of the job gave the invitation its link, which only an attempt that counted the email does, so that the retries of an email that an SMTP server that is down refused, or that the server's stop cut off, do not use up the mailbox's hour, while an attempt that failed before its count leaves the email to be counted by the next; a retry in a later hour than its count sends the email past that hour's 10, as once an SMTP server that was down for hours takes email again. The job of an email over the limit sends nothing and ends, cancelled with the limit as its error, so that the job's row and metrics tell the operator why: the invitation stays without a link, as one still to be emailed, and its admins send it anew later, which the maintainer chose over putting the email off in a queue that the invitation would hold a turn in. Refusing the request instead would tell one workspace that others invited the address. A revoked invitation keeps its row, so that its link answers 410. Removing a member revokes the workspace's open invitations that the member made, those whose link the member was last given as text, by making or resending them, which `invite.link_holder` records, and those to the member's address, recording each revocation with the removal's actor, so that no link that the member holds, as an admin holds a link invitation's, lets them back in. A demotion that takes away the right to manage members does the same from item 20, which adds role changes; until then, only the operator's `grant-role` demotes. Item 19 answers 403 `owner_required` to an admin who invites an owner, resends such an invitation, or removes an owner, and item 20 adds role changes; item 19 tests FR-TEN-02 AC2 over removal, and item 20 over demotion. Making and resending invitations, and reading, accepting, and registering with a link, share the `auth` bucket (N23). Recorded in PRD 1.12 FR-AUTH-01, FR-AUTH-03, FR-TEN-02, NFR-SEC-06, §5.3, and §5.6. | 19, 20, 54, 55 |
| N28 | FR-TEN-02 and N27 said what removing a member revokes, and that a demotion that takes away the right to manage members does the same, but not what a role change does to the invitations that the member made or holds the link of whose role the member may still give, nor to those as `owner` of an owner made `admin`, who could still let in an owner with them. | Decided: a role change, by `PATCH /api/v1/workspaces/{workspace_id}/members/{membership_id}` or the operator's `grant-role`, revokes the open invitations, expired or not, that the member made or was last given the link of as text whose role the new role may not give: every one, for a role below `admin`, and those as `owner`, for `admin`, recording each with the change's actor, or none for the operator's, and the membership. It revokes them before it changes the membership, as a removal does before it deletes one, so that an acceptance by the member at that moment is refused at once rather than deadlock with it. A change to the role that the membership has already changes and records nothing, and an admin is refused any change of an owner's membership with `owner_required`, as making a member owner is. The route answers with the member, with their email address. The service-layer authorisation package is `internal/authz`, which holds §5.6 for the operations that exist, with the two rows that manage members as two operations, and from which every service checks a role; listing the members gets a capability of its own. The viewer test of FR-TEN-03 AC3 reads its reads as those that §5.6 lets a viewer make: a viewer's list of the invitations, which §5.6 shows only to owners and admins (N27), is answered with 403 `insufficient_role`, as every change is. Recorded in PRD 1.13 FR-TEN-02 and §5.3. | 20 |

---

## Change log

| Plan version | Date | Change |
| --- | --- | --- |
| 1.0 | 2026-10-14 | Initial plan derived from PRD 1.4: 119 MVP items and 20 v1 items, with the coverage matrix and alignment notes N1–N8. |
| 1.1 | 2026-10-14 | Resolved N1–N7 with the maintainer and moved to PRD 1.5. Items 1, 20, 77, 108, 111, and 122 updated to match. Item 122 is now the Atom feed only, because `/changelog` generation moved to item 108. No change to ordering or dependencies. |
| 1.2 | 2026-10-14 | Fixes from a review of plan 1.1 and PRD 1.5, with the PRD 1.5 amendments extended to match. No items added, removed, or renumbered. **Dependencies:** item 119 depends on every MVP item (1–118). Item 118 gains 9, 72, 75, 81, 92, 101, and 116. Item 76 gains 47 and 71; item 90 gains 71; item 110 now depends on 75 and 87; item 113 on 84; item 114 gains 83; item 131 drops 122. **Criteria moved to the item where their prerequisites exist:** FR-ADMIN-04 AC1/AC3 to 6; FR-ENT-02 AC3 to 35; FR-AUTH-04 AC7 to 83; FR-EDIT-03 AC1 to 71 and 76; FR-EDIT-04 AC2 and FR-EXPR-03 AC2 to 76; the no-edge half of FR-DEP-01 AC4 to 49; FR-SNAP-02 AC1 to 99; the docs half of FR-PUB-02 AC3 to 109. **Criteria that had no item:** FR-EDIT-04 AC1 (71, 103), FR-EDIT-05 AC1 (62, 78) and AC3 (71), FR-EDIT-06 AC3 (83), FR-COMP-03 AC3 (105), FR-ADMIN-06 AC1 (113), FR-ENT-01 AC1 (117), and the new PRD criteria FR-TEN-03 AC4 (20), FR-GDPR-02 AC4 (111), FR-PUB-01 AC5 (108), FR-DATA-01 AC12 (84), and FR-EDIT-12 AC3 (127). **Other:** the TypeScript draft projection for play mode (71, N10); the synthetic schema version's numbering and location (77, N2); the `members` limit (23); `pnpm audit` (1, 117). Notes N2–N4, N7, and N8 updated, and N9–N10 added. The coverage matrix is now derived from the items' PRD lines, and item 1 checks that they agree. |
| 1.3 | 2026-10-14 | Fixes from a second review of plan 1.2 and PRD 1.5, with the PRD 1.5 amendments extended to match. No items added, removed, or renumbered. **Rules:** item numbers are now stable (suffixed inserts, `dropped` instead of removal), and item 1 checks references (rule 6). A performance table assigns each NFR-PERF target to the item that measures it, and item 1 checks it (rule 3). **Dependencies:** item 76 drops 71, so the serializer no longer waits for the canvas; the projection cross-check moves to item 89, which gains 71 and 76. **Criteria moved to the item where their prerequisites exist:** FR-TEXT-01 AC1 at the API boundary to 36; FR-DEP-01 AC3 to 67, with usage jump-to links in 61; FR-TEN-03 AC1/AC3 and FR-TEN-04 AC4 are completed by 117 over the full MVP operation set, with the audit-log case in 125. **Criteria halves that had no item:** FR-EDIT-03 AC4 publish gate (78), FR-PROJ-02 AC2 and FR-TEXT-02 AC2 validation halves (48), FR-COMP-02 AC2 and the FR-TEXT-02 AC2 blocking half (47), FR-TEXT-03 AC2 (76), the editor half of FR-VAR-02 AC1 (67). **Scope ownership:** resource routes land with their features (16–26); item 27 only gates and attaches the rate-limit buckets built in 15 and keyed by token in 26. **Entitlements:** one `members` enforcement site, including `grant-role` (23); the plan file holds exactly the ADR-010 keys and the `boards` limit is dropped (21, 30, N14). **New PRD criteria and decisions:** FR-API-01 AC3 pre-release form (7, 119, N11); pending-deletion owners (111, N12); registration refused before setup (17, N13); migration immutability (5); the annotation item shape and the durable AC12 fixture (83, 84, 127, N15); the synthetic schema version is injected by tests and checked out of the addon zip (77, 107); authoring tables register with export and import together (standing checklist, 84). |
| 1.4 | 2026-10-15 | **Status upkeep:** the PR that implements an item now sets the item's status to `done` with a link to itself, in a commit pushed once the PR is open, and that status update needs no change log row (item format, rule 7, standing checklist). Items 1–4 are marked done with links to their PRs, which merged with the status still `todo`. No items added, removed, or renumbered. |
| 1.5 | 2026-10-15 | Decisions from implementing item 5, with PRD 1.6. **Database logins:** alignment note N16 records the two database URLs and the provisioned application login; items 5 and 9 name them, and item 11 the maintenance pool's login that N16 leaves to it. **Startup migration:** `--no-auto-migrate` moves from item 5 to item 6, because it is a flag of `serve`, which item 6 adds; item 5 provides the startup step that `serve` runs. **Coverage:** items 5 and 6 name the FR-ADMIN-06 subcommands they add, `migrate` and `serve`, in their PRD lines and the coverage matrix. No items added, removed, or renumbered, and no dependency changed. |
| 1.6 | 2026-10-15 | Decisions from implementing item 6: alignment note N17 records the admin port and the token that protects `/metrics`, HSTS whenever the public URL is https, and how an instance whose startup migration failed refuses traffic. No items added, removed, or renumbered, and no dependency changed. |
| 1.7 | 2026-10-15 | Decisions from implementing item 9: alignment note N18 records the image's base and health check, the compose file's one required setting and its published port, and the image's tags before the first release. Item 9 names the FR-ADMIN-06 subcommand it adds, `healthcheck`, in its PRD line and the coverage matrix, and FR-PUB-01 and FR-PUB-04, for the embedded placeholder pages and the `/` that `STORYCREEP_PUBLIC_SITE` chooses between them. Items 29 and 119 name what N18 leaves to them: the asset directory in the image, and the release tag that the compose file runs. No items added, removed, or renumbered, and no dependency changed. |
| 1.8 | 2026-10-15 | Decisions from implementing item 10: alignment note N19 records where the instance's default locale is set, which parts of the error envelope are localised, and the part of ICU MessageFormat that the server formats. No items added, removed, or renumbered, and no dependency changed. |
| 1.9 | 2026-10-15 | Decisions from implementing item 11, with PRD 1.7: alignment note N20 records how the maintenance pool logs in, as a third database URL that migrating provisions, and that the first maintenance job makes required, and that neither the maintenance login nor the application login may be a member of `pg_write_all_data`. Items 11, 84, 110, 111, 112, and 114 cite N20. No items added, removed, or renumbered, and no dependency changed. |
| 1.10 | 2026-10-16 | Decisions from implementing item 12, with PRD 1.8: alignment note N21 records how the audit log records the events that belong to no workspace, with a null `workspace_id`, and where the key of the email hash comes from, a table of keys that migrating generates. Item 12 cites N21. No items added, removed, or renumbered, and no dependency changed. |
| 1.11 | 2026-10-16 | Decisions from implementing item 15: alignment note N22 records how the server tells a client's IP address behind a reverse proxy, with a new configuration key, `STORYCREEP_TRUSTED_PROXIES`, and N23 the lock after failed sign-ins and the limits of the rate limits' buckets. Item 15 cites both. No items added, removed, or renumbered, and no dependency changed. |
| 1.12 | 2026-10-16 | Decisions from implementing item 16, with PRD 1.9: alignment note N24 records the capability that lets an instance admin act as owner, `InstanceAdminAccess`, which an operator turns on with a new key and hosted mode refuses, that every account gets a personal workspace once, at its first sign-in, and that item 112, not item 16, adds the retention settings to the API. Item 16 cites N24. No items added, removed, or renumbered, and no dependency changed. |
| 1.13 | 2026-10-16 | Decisions from implementing item 17, with PRD 1.10: alignment note N25 records where the signup policy that first-run setup chooses is stored, and that `STORYCREEP_SIGNUP` overrides it, the setup code that completing setup through the API needs, and that the setup workspace is the owner's personal workspace. Setup's SMTP and storage steps move from item 17 to items 18 and 29, whose PRD lines, and the coverage matrix, gain the FR-ADMIN-02 slices. Items 17, 18, and 29 cite N25. No items added, removed, or renumbered, and no dependency changed. |
| 1.14 | 2026-10-16 | Decisions from implementing item 18, with PRD 1.11: alignment note N26 records where the SMTP server that setup chooses is stored, and the configuration keys that override it, that a link's token is stored as a hash, which an emailed link's job adds, that an instance admin takes a new link on demand without SMTP, and the operator's `storycreep reset-password` in either mode, the banner flag's route, that a workspace needs a verified address when `EmailRequired` is on, that an account is emailed one link of a kind in 10 minutes at most, and that an email that no attempt could deliver ends its job. Items 18, 54, and 114 cite N26. No items added, removed, or renumbered, and no dependency changed. |
| 1.15 | 2026-10-17 | Decisions from implementing item 19, with PRD 1.12: alignment note N27 records that an invitation creates no account under the `closed` signup policy, and accepts itself in the registration that its link makes under `invite` and `open`, that a link invitation's link always goes to its admin, what an emailed invitation says, who sees the member list, that an address has one open invitation of a workspace, what resending does, that a workspace emails an address one invitation in 10 minutes at most, and the instance a mailbox 10 an hour from all workspaces together, in a new rate-limit bucket (N23), that removing a member revokes the open invitations that the member made, those whose link the member was last given as text, and those to the member's address, and that item 19 requires `owner` for inviting an owner and removing one, which item 20 requires for role changes, where a demotion revokes the invitations that a removal would. Item 19's PRD line names FR-TEN-03 (AC4 for invitations and member removal), and the coverage matrix gains 19 for FR-TEN-03; item 19 tests FR-TEN-02 AC2 over removal, and item 20 over demotion. Items 19, 20, 54, and 55 cite N27, and N23 lists the limit of the new `invite_email` bucket. No items added, removed, or renumbered, and no dependency changed. |
| 1.16 | 2026-10-17 | Decisions from implementing item 20, with PRD 1.13: alignment note N28 records that a role change, by the API or the operator's `grant-role`, revokes the open invitations that the member made or was last given the link of as text whose role the new role may not give, those as `owner` of an owner made `admin` included, before it changes the membership, that a change to the role that a membership has already changes nothing, that an admin is refused any change of an owner's membership, that `internal/authz` is the service-layer authorisation package, and that the viewer test of FR-TEN-03 AC3 answers a viewer's list of the invitations, which §5.6 keeps from viewers, with 403. Item 20's PRD line names FR-TEN-02 (AC2 for demotion, and the invitations that a role change revokes), its scope cites N28, and the coverage matrix gains 20 for FR-TEN-02. No items added, removed, or renumbered, and no dependency changed. |
