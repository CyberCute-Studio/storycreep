-- A project's trash (package trash, PRD FR-PROJ-06): the rows of its content
-- with deleted_at, which the live views leave out (migration 00019). The
-- tables whose rows the trash lists are its boards' and its variables', and,
-- as they are added, its nodes' and components'; its folders' rows go to the
-- trash too, but are not listed. Each such table has a branch here, in the
-- list and in the purge, which package trash's tests hold it to. A row that
-- belongs to a board, as a board's variable, is in the trash with its board
-- too, which lists the board alone, and is purged with it, by the cascade of
-- its foreign key to the board.

-- name: TrashItems :many
-- TrashItems returns the entities in the project's trash that it lists, the
-- most recently deleted first, then by their kinds and their IDs, after the
-- entity whose deletion, kind, and ID are after_deleted_at, after_kind, and
-- after_id, or from the first if after_deleted_at is null, each with its
-- kind, its ID, its name, a variable's key, and when it was deleted. The
-- bound on deleted_at
-- alone, which the infinite time leaves open for the first page, lets the
-- index of each table's trash start where the page does, rather than read
-- every row that the pages before it held.
SELECT t.kind::text AS kind, t.id::uuid AS id, t.name::text AS name, t.deleted_at::timestamptz AS deleted_at
FROM (
    SELECT 'board' AS kind, b.id, b.name, b.deleted_at FROM board b
    WHERE b.workspace_id = @workspace_id AND b.project_id = @project_id AND b.deleted_at IS NOT NULL
    UNION ALL
    SELECT 'variable' AS kind, v.id, v.key::text, v.deleted_at FROM variable v
    WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id AND v.deleted_at IS NOT NULL
) t
WHERE t.deleted_at <= coalesce(sqlc.narg(after_deleted_at)::timestamptz, 'infinity'::timestamptz)
    AND (sqlc.narg(after_deleted_at)::timestamptz IS NULL
        OR t.deleted_at < sqlc.narg(after_deleted_at)::timestamptz
        OR (t.kind, t.id) > (@after_kind::text, @after_id::uuid))
ORDER BY t.deleted_at DESC, t.kind, t.id
LIMIT @max_rows;

-- name: ProjectsToPurge :many
-- ProjectsToPurge returns the workspace's projects whose trash holds a row
-- that has been there for retention, which the purge deletes, in the order
-- of their IDs. It looks for each project's oldest rows in the index of
-- each table's trash, rather than read every row of the workspace's trash.
SELECT p.id FROM project p
WHERE p.workspace_id = @workspace_id AND (
    EXISTS (SELECT 1 FROM board b WHERE b.workspace_id = p.workspace_id AND b.project_id = p.id
        AND b.deleted_at <= now() - @retention::interval)
    OR EXISTS (SELECT 1 FROM variable v WHERE v.workspace_id = p.workspace_id AND v.project_id = p.id
        AND v.deleted_at <= now() - @retention::interval)
    OR EXISTS (SELECT 1 FROM folder f WHERE f.workspace_id = p.workspace_id AND f.project_id = p.id
        AND f.deleted_at <= now() - @retention::interval))
ORDER BY p.id;

-- name: LockProjectToPurge :one
-- LockProjectToPurge locks the project's row as a batch of its commands
-- does (LockProjectSeq), so that the purge changes nothing that a batch has
-- read, and returns its ID, or no row if the workspace has no such project,
-- its time in the workspace's trash has passed, which the purge of projects
-- deletes it for, or a transaction holds the lock, as a batch does while it
-- applies, which a later run of the purge waits out.
SELECT id FROM project
WHERE workspace_id = @workspace_id AND id = @id AND (hard_delete_after IS NULL OR hard_delete_after > now())
FOR NO KEY UPDATE SKIP LOCKED;

-- name: PurgeBoards :execrows
-- PurgeBoards deletes at most max_rows of the project's boards that have
-- been in its trash for retention, those that have been there longest
-- first, and returns how many it deleted.
DELETE FROM board d
WHERE d.workspace_id = @workspace_id AND d.project_id = @project_id AND d.id IN (
    SELECT b.id FROM board b
    WHERE b.workspace_id = @workspace_id AND b.project_id = @project_id
        AND b.deleted_at <= now() - @retention::interval
    ORDER BY b.deleted_at, b.id
    LIMIT @max_rows
);

-- name: PurgeVariables :execrows
-- PurgeVariables deletes at most max_rows of the project's variables that
-- have been in its trash for retention, those that have been there longest
-- first, and returns how many it deleted.
DELETE FROM variable d
WHERE d.workspace_id = @workspace_id AND d.project_id = @project_id AND d.id IN (
    SELECT v.id FROM variable v
    WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id
        AND v.deleted_at <= now() - @retention::interval
    ORDER BY v.deleted_at, v.id
    LIMIT @max_rows
);

-- name: PurgeFolders :execrows
-- PurgeFolders deletes at most max_rows of the project's folders that have
-- been deleted for retention and hold nothing, neither a folder, nor a
-- board, nor a variable, in the trash or not, those deleted longest ago
-- first, and returns how many it deleted. A folder deleted later than what it
-- holds holds it no longer once the purge has deleted that, which the
-- folder's own purge then waits for: a later statement deletes it. A later
-- table whose rows go into a folder, as the components' (plan item 34), adds
-- its rows here, as it does to FolderHolds, which TestFolderChildren holds it
-- to.
DELETE FROM folder d
WHERE d.workspace_id = @workspace_id AND d.project_id = @project_id AND d.id IN (
    SELECT f.id FROM folder f
    WHERE f.workspace_id = @workspace_id AND f.project_id = @project_id
        AND f.deleted_at <= now() - @retention::interval
        AND NOT EXISTS (SELECT 1 FROM folder c WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id
            AND c.parent_id = f.id)
        AND NOT EXISTS (SELECT 1 FROM board b WHERE b.workspace_id = @workspace_id AND b.project_id = @project_id
            AND b.folder_id = f.id)
        AND NOT EXISTS (SELECT 1 FROM variable v WHERE v.workspace_id = @workspace_id
            AND v.project_id = @project_id AND v.folder_id = f.id)
    ORDER BY f.deleted_at, f.id
    LIMIT @max_rows
);
