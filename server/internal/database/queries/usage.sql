-- Usage metering (package usage, PRD FR-ENT-02, §5.7). A workspace's usage of
-- a key is its counter's value plus the sum of its deltas still present, each
-- query of it reading both in one statement, and so in one snapshot, in a
-- transaction of the workspace. Only the rollup job writes the counters.

-- name: InsertUsageDelta :exec
-- InsertUsageDelta records what a change adds to the workspace's usage of a
-- key, or takes from it.
INSERT INTO usage_delta (id, workspace_id, key, delta) VALUES (@id, @workspace_id, @key, @delta);

-- name: InsertUnflushedUsageDelta :exec
-- InsertUnflushedUsageDelta records a delta as InsertUsageDelta does, and
-- makes the transaction that it runs in commit without waiting for its commit
-- to be flushed to disk (synchronous_commit off), as TakeRateLimit does, for a
-- delta that a crash of the database may lose, with the last few hundred
-- milliseconds of such commits. It changes the setting in no_flush, whose one
-- row the insert reads, so that it spends no round trip on it.
WITH no_flush AS (
    SELECT set_config('synchronous_commit', 'off', true)
)
INSERT INTO usage_delta (id, workspace_id, key, delta)
SELECT @id::uuid, @workspace_id::uuid, @key::text, @delta::bigint
FROM no_flush;

-- name: LockUsage :exec
-- LockUsage takes the transaction-scoped advisory lock of the workspace's key,
-- in the two-integer key space of pg_advisory_xact_lock, which the lock of
-- migrations, in the single-bigint key space, never meets. It serializes the
-- changes of a key that is counted exactly until their transactions end.
SELECT pg_advisory_xact_lock(@class::integer, @object::integer);

-- name: KeyUsage :one
-- KeyUsage returns the workspace's usage of a key.
SELECT (
    coalesce((SELECT c.value FROM usage_counter c WHERE c.workspace_id = @workspace_id AND c.key = @key), 0)
    + coalesce((SELECT sum(d.delta) FROM usage_delta d WHERE d.workspace_id = @workspace_id AND d.key = @key), 0)
)::bigint AS used;

-- name: WorkspaceUsage :many
-- WorkspaceUsage returns the workspace's usage of each key that it has a
-- counter or a delta of.
SELECT u.key, sum(u.value)::bigint AS used
FROM (
    SELECT c.key, c.value FROM usage_counter c WHERE c.workspace_id = @workspace_id
    UNION ALL
    SELECT d.key, d.delta FROM usage_delta d WHERE d.workspace_id = @workspace_id
) u
GROUP BY u.key
ORDER BY u.key;

-- name: FoldUsageDeltas :many
-- FoldUsageDeltas deletes the workspace's deltas that the statement can see,
-- which are those that committed before it began, and returns their sum for
-- each key, which the rollup job adds to the counters in the same
-- transaction. A delta whose change commits meanwhile is not among them, and
-- stays for a later run.
WITH folded AS (
    DELETE FROM usage_delta WHERE workspace_id = @workspace_id RETURNING key, delta
)
SELECT f.key, sum(f.delta)::bigint AS total FROM folded f GROUP BY f.key ORDER BY f.key;

-- name: AddUsageCounters :exec
-- AddUsageCounters adds each total to the workspace's counter of its key,
-- creating the counter, with the ID given beside the key, where the workspace
-- has none. The three arrays have the same length, so the set-returning
-- unnests in the select list run in step.
INSERT INTO usage_counter (id, workspace_id, key, value)
SELECT unnest(@ids::uuid[]), @workspace_id, unnest(@keys::text[]), unnest(@totals::bigint[])
ON CONFLICT (workspace_id, key) DO UPDATE
SET value = usage_counter.value + excluded.value, updated_at = now();

-- name: MemberDrift :one
-- MemberDrift returns how many memberships the workspace has, guests'
-- included, less its usage of members: what reconciliation adds to its usage
-- so that the usage is the count again.
SELECT (
    (SELECT count(*) FROM membership m WHERE m.workspace_id = @workspace_id)
    - coalesce((SELECT c.value FROM usage_counter c WHERE c.workspace_id = @workspace_id AND c.key = 'members'), 0)
    - coalesce((SELECT sum(d.delta) FROM usage_delta d WHERE d.workspace_id = @workspace_id AND d.key = 'members'),
        0)
)::bigint AS drift;

-- name: ProjectDrift :one
-- ProjectDrift returns how many of the workspace's projects count towards its
-- projects, those that are neither archived nor in the trash (FR-PROJ-01),
-- less its usage of projects: what reconciliation adds to its usage so that
-- the usage is the count again.
SELECT (
    (SELECT count(*) FROM project p
        WHERE p.workspace_id = @workspace_id AND p.archived_at IS NULL AND p.deleted_at IS NULL)
    - coalesce((SELECT c.value FROM usage_counter c WHERE c.workspace_id = @workspace_id AND c.key = 'projects'), 0)
    - coalesce((SELECT sum(d.delta) FROM usage_delta d WHERE d.workspace_id = @workspace_id AND d.key = 'projects'),
        0)
)::bigint AS drift;

-- name: BoardDrift :one
-- BoardDrift returns how many boards the workspace's projects have that are
-- in no trash, neither their project's nor, with their project, the
-- workspace's, less its usage of boards: what reconciliation adds to its
-- usage so that the usage is the count again.
SELECT (
    (SELECT count(*) FROM board b JOIN project p ON p.workspace_id = b.workspace_id AND p.id = b.project_id
        WHERE b.workspace_id = @workspace_id AND b.deleted_at IS NULL AND p.deleted_at IS NULL)
    - coalesce((SELECT c.value FROM usage_counter c WHERE c.workspace_id = @workspace_id AND c.key = 'boards'), 0)
    - coalesce((SELECT sum(d.delta) FROM usage_delta d WHERE d.workspace_id = @workspace_id AND d.key = 'boards'),
        0)
)::bigint AS drift;

-- name: WorkspaceIDs :many
-- WorkspaceIDs returns the IDs of the workspaces after the workspace after,
-- or from the first if after is null, up to and including the workspace
-- through, or to the last if through is null, in order, whatever their
-- status, for the jobs that work on each workspace in a transaction of its
-- own (§5.4). The workspace table has no row-level security.
SELECT id FROM workspace
WHERE (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
    AND (sqlc.narg(through)::uuid IS NULL OR id <= sqlc.narg(through)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: RandomWorkspaceID :one
-- RandomWorkspaceID returns the ID of a workspace picked at random, whatever
-- its status, and no row if there is none, which the jobs that work on each
-- workspace start their run at, so that a run that ends before it reaches
-- every workspace leaves others than the last run did.
SELECT id FROM workspace ORDER BY random() LIMIT 1;
