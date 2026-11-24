-- The workspace exports (PRD FR-DATA-02, plan item 86, note N93), and what
-- their archive reads of a workspace beside its projects' authoring exports.

-- name: WorkspaceExportWorkspace :one
-- WorkspaceExportWorkspace returns what the archive holds of the workspace
-- itself, and when its transaction began, by the database's clock, which the
-- audit log's entries are of, and before its first statement took its
-- snapshot.
SELECT id, name, slug, settings, created_at, now()::timestamptz AS read_at FROM workspace WHERE id = @id;

-- name: WorkspaceExportProjects :many
-- WorkspaceExportProjects returns the workspace's projects, those in its trash
-- too, which the archive names without holding them, in the order of their
-- IDs.
SELECT id, name, slug, archived_at, deleted_at FROM project WHERE workspace_id = @workspace_id ORDER BY id;

-- name: WorkspaceExportMembers :many
-- WorkspaceExportMembers returns the memberships of the workspace, guests'
-- included, with each member's display name and email address, in the order of
-- the memberships' IDs.
SELECT m.id, m.user_id, m.role, m.joined_at, u.display_name, u.email::text AS email
FROM membership m
JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id = @workspace_id
ORDER BY m.id;

-- name: WorkspaceExportGrants :many
-- WorkspaceExportGrants returns the grants of the workspace's members on its
-- projects, in the order of their memberships' IDs and then of their projects'.
SELECT membership_id, project_id, role FROM project_grant WHERE workspace_id = @workspace_id
ORDER BY membership_id, project_id;

-- name: WorkspaceExportAuditEntries :many
-- WorkspaceExportAuditEntries returns at most row_limit of the workspace's
-- audit entries made by the time until, in the order in which they were made,
-- after the entry that was made at after_at with the ID after_id.
SELECT id, actor_user_id, actor_token_id, action, subject_kind, subject_id, result, ip, user_agent,
    request_id, metadata, created_at
FROM audit_entry
WHERE workspace_id = @workspace_id AND created_at <= @until::timestamptz
    AND (created_at, id) > (@after_at::timestamptz, @after_id::uuid)
ORDER BY created_at, id LIMIT @row_limit;

-- name: WorkspaceExportAssets :many
-- WorkspaceExportAssets returns the assets of the workspace itself, which
-- belong to none of its projects, with the keys of their files.
SELECT id, sha256, mime, bytes, width, height, storage_key FROM asset
WHERE workspace_id = @workspace_id AND project_id IS NULL ORDER BY id;

-- name: InsertWorkspaceExport :one
-- InsertWorkspaceExport adds an export that waits for its job, unless the user
-- has one of the workspace on its way, when it adds no row.
INSERT INTO workspace_export (id, workspace_id, requested_by, storage_key, expires_at)
VALUES (@id, @workspace_id, @requested_by, @storage_key, @expires_at)
ON CONFLICT (workspace_id, requested_by) WHERE status IN ('queued', 'building') DO NOTHING
RETURNING *;

-- name: WorkspaceExport :one
-- WorkspaceExport returns the export of the workspace that the user asked for,
-- unless its time has passed, whether or not its job has deleted it yet.
SELECT * FROM workspace_export
WHERE workspace_id = @workspace_id AND requested_by = @requested_by AND id = @id AND expires_at > now();

-- name: WorkspaceExports :many
-- WorkspaceExports returns the user's exports of the workspace whose time has
-- not passed, newest first, at most row_limit of them, after the export of the
-- UUID after, or from the newest for the zero UUID.
SELECT * FROM workspace_export
WHERE workspace_id = @workspace_id AND requested_by = @requested_by AND expires_at > now()
    AND (@after::uuid = '00000000-0000-0000-0000-000000000000' OR id < @after::uuid)
ORDER BY id DESC LIMIT @row_limit;

-- name: WorkspaceExportInProgress :one
-- WorkspaceExportInProgress returns the ID of the user's export of the
-- workspace that is on its way and that its user can still read.
SELECT id FROM workspace_export
WHERE workspace_id = @workspace_id AND requested_by = @requested_by AND status IN ('queued', 'building')
    AND expires_at > now();

-- name: FailExpiredWorkspaceExports :exec
-- FailExpiredWorkspaceExports ends the user's export of the workspace that is
-- on its way past its time, which would otherwise keep another from being
-- added until its expiry job has run.
UPDATE workspace_export SET status = 'failed', finished_at = now()
WHERE workspace_id = @workspace_id AND requested_by = @requested_by AND status IN ('queued', 'building')
    AND expires_at <= now();

-- name: WorkspaceExportOfJob :one
-- WorkspaceExportOfJob returns the export of a job, whoever asked for it.
SELECT * FROM workspace_export WHERE workspace_id = @workspace_id AND id = @id;

-- name: StartWorkspaceExport :execrows
-- StartWorkspaceExport marks an export that waits as being built. An attempt
-- that follows one that failed finds it building already.
UPDATE workspace_export SET status = 'building'
WHERE workspace_id = @workspace_id AND id = @id AND status IN ('queued', 'building');

-- name: FinishWorkspaceExport :execrows
-- FinishWorkspaceExport makes an export that is being built ready.
UPDATE workspace_export SET status = 'ready', size_bytes = @size_bytes, project_count = @project_count,
    file_name = @file_name, finished_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND status = 'building';

-- name: FailWorkspaceExport :one
-- FailWorkspaceExport fails an export that has not ended, and returns the key
-- of its archive, or no row if it has ended.
UPDATE workspace_export SET status = 'failed', finished_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND status IN ('queued', 'building')
RETURNING storage_key;

-- name: DeleteWorkspaceExport :execrows
-- DeleteWorkspaceExport deletes an export whose time has passed.
DELETE FROM workspace_export WHERE workspace_id = @workspace_id AND id = @id;
