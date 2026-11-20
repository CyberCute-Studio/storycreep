-- The snapshots of the projects, their releases (package snapshot, PRD
-- FR-SNAP-01, §7.3). snapshot and snapshot_file are tenant tables, which a
-- transaction of their workspace reads. A snapshot is added building, and its
-- build makes it ready or failed, once, after which nothing changes it
-- (migration 00035).

-- name: LockSnapshots :exec
-- LockSnapshots takes the transaction-scoped advisory lock of a project's
-- snapshots, in the two-integer key space of pg_advisory_xact_lock, which
-- serializes the publications of the project until their transactions end, so
-- that two cannot both take a version that is greater than the newest.
SELECT pg_advisory_xact_lock(@class::integer, @object::integer);

-- name: NewestSnapshotVersion :one
-- NewestSnapshotVersion returns the greatest version of the project's
-- snapshots that are building or ready, or no row if it has none.
SELECT version FROM snapshot
WHERE workspace_id = @workspace_id AND project_id = @project_id AND status <> 'failed'
ORDER BY version_major DESC, version_minor DESC, version_patch DESC
LIMIT 1;

-- name: NewestSnapshotID :one
-- NewestSnapshotID returns the greatest ID of the project's snapshots, of
-- every status, or no row if it has none, after which a new snapshot's ID is
-- made (ids.NewAfter), under the lock of the project's snapshots, so that
-- their IDs, by which Snapshots lists them, follow the order in which they
-- were published, whatever the clocks of the servers that published them.
SELECT id FROM snapshot WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id DESC LIMIT 1;

-- name: InsertSnapshot :exec
-- InsertSnapshot adds a snapshot of a project, building, which Snapshot
-- then reads as the API shows it.
INSERT INTO snapshot (id, workspace_id, project_id, version, version_major, version_minor, version_patch, note,
    acknowledged_seq, acknowledged_warnings, published_by)
VALUES (@id, @workspace_id, @project_id, @version, @version_major, @version_minor, @version_patch, @note,
    @acknowledged_seq, @acknowledged_warnings, @published_by);

-- name: Snapshot :one
-- Snapshot returns a snapshot of the project, building, ready, or failed,
-- but for its acknowledged warnings and its report, which the API shows
-- nothing of with it, and which may be megabytes of a large draft's, with
-- the display name of its publisher as it is now (PRD §6.2), which a
-- snapshot whose publisher's account is deleted has none of: Snapshots
-- returns the same columns.
SELECT s.id, s.project_id, s.version, s.note, s.status, s.failure_code, s.acknowledged_seq, s.source_seq,
    s.schema_version, s.content_hash, s.counts, s.published_by, s.published_at, s.built_at,
    u.display_name AS published_by_name
FROM snapshot s LEFT JOIN "user" u ON u.id = s.published_by
WHERE s.workspace_id = @workspace_id AND s.project_id = @project_id AND s.id = @id;

-- name: Snapshots :many
-- Snapshots returns a page of the project's snapshots, as Snapshot returns
-- each, newest first, by their IDs, which follow the order in which they
-- were published (NewestSnapshotID), those of the status alone, or of every status for a null
-- one, after the snapshot of the ID after in that order, or from the newest
-- for a null one (PRD FR-SNAP-02). The key (workspace_id, project_id, id)
-- reads them in that order.
SELECT s.id, s.project_id, s.version, s.note, s.status, s.failure_code, s.acknowledged_seq, s.source_seq,
    s.schema_version, s.content_hash, s.counts, s.published_by, s.published_at, s.built_at,
    u.display_name AS published_by_name
FROM snapshot s LEFT JOIN "user" u ON u.id = s.published_by
WHERE s.workspace_id = @workspace_id AND s.project_id = @project_id
    AND (sqlc.narg(status)::text IS NULL OR s.status = sqlc.narg(status)::text)
    AND (sqlc.narg(after)::uuid IS NULL OR s.id < sqlc.narg(after)::uuid)
ORDER BY s.id DESC
LIMIT @row_limit;

-- name: SnapshotFiles :many
-- SnapshotFiles returns the files of a snapshot of the workspace, each by its
-- path, with its bytes, in no order, which a ready snapshot has, and a
-- building or failed one has none of: an export keeps them by their paths,
-- and its zip orders them by the paths' bytes (snapshot.Export.Zip), which a
-- collation's order may not be.
SELECT path, bytes FROM snapshot_file
WHERE workspace_id = @workspace_id AND snapshot_id = @snapshot_id;

-- name: SnapshotReport :one
-- SnapshotReport returns the report that the build of a snapshot of the
-- project stored, null for a snapshot that holds none.
SELECT validation_report FROM snapshot
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: SnapshotToRead :one
-- SnapshotToRead returns a snapshot of the workspace, of any project, which
-- its build reads in the transaction that reads the draft, which only reads.
SELECT * FROM snapshot WHERE workspace_id = @workspace_id AND id = @id;

-- name: SnapshotToBuild :one
-- SnapshotToBuild returns the status of a snapshot of the workspace, of any
-- project, and locks it until the transaction ends, so that one build at a
-- time finishes it.
SELECT status FROM snapshot WHERE workspace_id = @workspace_id AND id = @id FOR UPDATE;

-- name: FinishSnapshot :execrows
-- FinishSnapshot makes a snapshot that is building ready, with what its build
-- read and wrote, and changes nothing of one that is not.
UPDATE snapshot
SET status = 'ready', source_seq = @source_seq, validation_report = @validation_report,
    schema_version = @schema_version, content_hash = @content_hash, counts = @counts, built_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND status = 'building';

-- name: FailSnapshot :execrows
-- FailSnapshot makes a snapshot that is building failed, with the code of
-- why, and what its build read, if it read the draft, and changes nothing of
-- one that is not.
UPDATE snapshot
SET status = 'failed', failure_code = @failure_code, source_seq = sqlc.narg(source_seq),
    validation_report = sqlc.narg(validation_report), built_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND status = 'building';

-- name: InsertSnapshotFiles :exec
-- InsertSnapshotFiles adds the files of a snapshot that is building, each
-- with its ID, its path, its hash, and its bytes, in one statement: the
-- arrays, of one length, are read in step, the i-th of each a file's.
INSERT INTO snapshot_file (id, workspace_id, snapshot_id, path, sha256, bytes)
SELECT unnest(@ids::uuid[]), @workspace_id::uuid, @snapshot_id::uuid, unnest(@paths::text[]),
    unnest(@hashes::text[]), unnest(@contents::bytea[]);

-- name: UsersWithAccounts :many
-- UsersWithAccounts returns the users among user_ids that have an account,
-- whom a snapshot's stored report names as those who muted its findings, and
-- no longer names once their account is deleted, as the draft's report does
-- not, whose mutes the deletion's cascade takes their author from.
SELECT id FROM "user" WHERE id = ANY(@user_ids::uuid[]);

-- name: SnapshotDrift :one
-- SnapshotDrift returns how many snapshots the workspace's projects have that
-- are building or ready, which alone count towards its snapshots, a failed
-- one being the record of an attempt, less its usage of snapshots: what
-- reconciliation adds to its usage so that the usage is the count again, as
-- after a project's purge, whose cascade deletes its snapshots without a
-- delta.
SELECT (
    (SELECT count(*) FROM snapshot s WHERE s.workspace_id = @workspace_id AND s.status <> 'failed')
    - coalesce((SELECT c.value FROM usage_counter c WHERE c.workspace_id = @workspace_id AND c.key = 'snapshots'),
        0)
    - coalesce((SELECT sum(d.delta) FROM usage_delta d WHERE d.workspace_id = @workspace_id AND d.key = 'snapshots'),
        0)
)::bigint AS drift;
