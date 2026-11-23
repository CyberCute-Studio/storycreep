-- name: InsertProjectImport :one
-- InsertProjectImport adds an import whose upload is about to begin, unless
-- the user has one of the workspace on its way, when it adds no row.
INSERT INTO project_import (id, workspace_id, requested_by, file_name, size_bytes, chunk_bytes, id_policy, expires_at)
VALUES (@id, @workspace_id, @requested_by, @file_name, @size_bytes, @chunk_bytes, @id_policy, @expires_at)
ON CONFLICT (workspace_id, requested_by) WHERE status IN ('uploading', 'queued', 'importing') DO NOTHING
RETURNING *;

-- name: ProjectImportInProgress :one
-- ProjectImportInProgress returns the user's import of the workspace that is
-- on its way, and has not passed its time.
SELECT id FROM project_import
WHERE workspace_id = @workspace_id AND requested_by = @requested_by
    AND status IN ('uploading', 'queued', 'importing') AND expires_at > now();

-- name: FailExpiredProjectImports :exec
-- FailExpiredProjectImports ends the user's import of the workspace that is on
-- its way past its time, which no read shows, so that it holds the way no
-- longer. Its expiry job still deletes it, and its chunks.
UPDATE project_import
SET status = 'failed', failure = '{"code": "expired"}', finished_at = now()
WHERE workspace_id = @workspace_id AND requested_by = @requested_by
    AND status IN ('uploading', 'queued', 'importing') AND expires_at <= now();

-- name: ProjectImport :one
-- ProjectImport returns the import that the user asked for, unless it has
-- passed its time.
SELECT * FROM project_import
WHERE workspace_id = @workspace_id AND requested_by = @requested_by AND id = @id AND expires_at > now();

-- name: ProjectImportOfJob :one
-- ProjectImportOfJob returns an import for its jobs, whoever asked for it.
SELECT * FROM project_import WHERE workspace_id = @workspace_id AND id = @id;

-- name: ProjectImports :many
-- ProjectImports lists the user's imports of the workspace that have not
-- passed their time, newest first, after the import after, or from the newest
-- for the zero UUID.
SELECT * FROM project_import
WHERE workspace_id = @workspace_id AND requested_by = @requested_by AND expires_at > now()
    AND (@after::uuid = '00000000-0000-0000-0000-000000000000' OR id < @after)
ORDER BY id DESC
LIMIT @row_limit;

-- name: ProjectImportChunks :many
-- ProjectImportChunks returns the chunks of the import that have been stored,
-- in their order.
SELECT chunk_index, bytes, storage_key FROM project_import_chunk
WHERE workspace_id = @workspace_id AND import_id = @import_id
ORDER BY chunk_index;

-- name: LockProjectImportUpload :one
-- LockProjectImportUpload returns the status of the import that the user
-- asked for, locked, so that it stays uploading until the chunk is recorded.
SELECT status FROM project_import
WHERE workspace_id = @workspace_id AND requested_by = @requested_by AND id = @id AND expires_at > now()
FOR UPDATE;

-- name: UpsertProjectImportChunk :exec
-- UpsertProjectImportChunk records that the chunk is stored, or that it was
-- stored again, as a resumed upload may send it.
INSERT INTO project_import_chunk (workspace_id, import_id, chunk_index, bytes, storage_key)
VALUES (@workspace_id, @import_id, @chunk_index, @bytes, @storage_key)
ON CONFLICT (workspace_id, import_id, chunk_index) DO UPDATE SET bytes = excluded.bytes;

-- name: DeleteProjectImportChunks :many
-- DeleteProjectImportChunks deletes the rows of the import's chunks, and
-- returns their keys, whose blobs the caller deletes.
DELETE FROM project_import_chunk
WHERE workspace_id = @workspace_id AND import_id = @import_id
RETURNING storage_key;

-- name: QueueProjectImport :execrows
-- QueueProjectImport hands a whole upload to its job.
UPDATE project_import SET status = 'queued', phase = 'queued', progress_done = 0, progress_total = 0
WHERE workspace_id = @workspace_id AND requested_by = @requested_by AND id = @id AND status = 'uploading';

-- name: StartProjectImport :execrows
-- StartProjectImport marks a queued import as the job's, or one that an
-- attempt before this one left importing.
UPDATE project_import SET status = 'importing', phase = 'checking', progress_done = 0, progress_total = 0
WHERE workspace_id = @workspace_id AND id = @id AND status IN ('queued', 'importing');

-- name: SetProjectImportProgress :exec
-- SetProjectImportProgress records how far the job is.
UPDATE project_import SET phase = @phase, progress_done = @progress_done, progress_total = @progress_total
WHERE workspace_id = @workspace_id AND id = @id AND status = 'importing';

-- name: FinishProjectImport :execrows
-- FinishProjectImport makes the import ready, in the transaction that created
-- its project, and changes nothing of one that has ended.
UPDATE project_import
SET status = 'ready', phase = 'done', applied_policy = @applied_policy, created_project_id = @created_project_id,
    report = @report, finished_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND status = 'importing';

-- name: FailProjectImport :execrows
-- FailProjectImport fails the import, with why, and changes nothing of one
-- that has ended.
UPDATE project_import SET status = 'failed', phase = 'failed', failure = @failure, finished_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND status IN ('uploading', 'queued', 'importing');

-- name: DeleteProjectImport :execrows
-- DeleteProjectImport deletes the import, and its chunks' rows with it.
DELETE FROM project_import WHERE workspace_id = @workspace_id AND id = @id;

-- name: WorkspaceMembersAmong :many
-- WorkspaceMembersAmong returns those of the users who are members of the
-- workspace with an active account, whom an imported archive's names keep.
SELECT m.user_id FROM membership m JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id = @workspace_id AND m.user_id = ANY(@user_ids::uuid[]) AND u.status = 'active';

-- name: ImportedUsers :many
-- ImportedUsers returns the workspace's imported identities of the source
-- users.
SELECT source_user_id, display_name, user_id FROM imported_user
WHERE workspace_id = @workspace_id AND source_user_id = ANY(@source_ids::uuid[]);

-- name: InsertImportedAccount :exec
-- InsertImportedAccount adds the account of an imported identity, which no one
-- can sign in as: it is not active and has no password.
INSERT INTO "user" (id, email, display_name, status) VALUES (@id, @email, @display_name, 'imported');

-- name: InsertImportedUser :exec
-- InsertImportedUser records the workspace's imported identity of a source
-- user with a display name.
INSERT INTO imported_user (workspace_id, source_user_id, display_name, user_id)
VALUES (@workspace_id, @source_user_id, @display_name, @user_id);

-- name: InsertImportedComment :exec
-- InsertImportedComment adds a comment of an imported archive as it was, with
-- who wrote it and when. Its thread's first comment comes first.
INSERT INTO comment (id, workspace_id, project_id, parent_id, thread_id, thread_at, node_id, connection_id, board_id,
    variable_id, body, resolved_at, resolved_by, edited_at, deleted_at, created_by, created_at, updated_at)
VALUES (@id, @workspace_id, @project_id, @parent_id, @thread_id, @thread_at, @node_id, @connection_id, @board_id,
    @variable_id, @body, @resolved_at, @resolved_by, @edited_at, @deleted_at, @created_by, @created_at, @created_at);

-- name: InsertImportedPlayState :exec
-- InsertImportedPlayState adds a play state of an imported archive as it was
-- saved.
INSERT INTO play_state (id, workspace_id, project_id, name, state, names, created_by, updated_by, created_at,
    updated_at)
VALUES (@id, @workspace_id, @project_id, @name, @state, @names, @created_by, @created_by, @created_at, @created_at);

-- name: InsertImportedMute :exec
-- InsertImportedMute adds a muted finding of an imported archive, with who
-- muted it.
INSERT INTO validation_mute (workspace_id, project_id, node_id, connection_id, branch_output_id, variable_id,
    board_id, code, reason, created_by, updated_by)
VALUES (@workspace_id, @project_id, @node_id, @connection_id, @branch_output_id, @variable_id, @board_id, @code,
    @reason, @muted_by, @muted_by);

-- name: InsertImportedSnapshot :exec
-- InsertImportedSnapshot adds a release of an imported archive, building, as
-- every snapshot is added, with when it was published.
INSERT INTO snapshot (id, workspace_id, project_id, version, version_major, version_minor, version_patch, note,
    acknowledged_seq, acknowledged_warnings, published_by, published_at)
VALUES (@id, @workspace_id, @project_id, @version, @version_major, @version_minor, @version_patch, @note,
    @acknowledged_seq, @acknowledged_warnings, @published_by, @published_at);

-- name: FinishImportedSnapshot :execrows
-- FinishImportedSnapshot makes an imported release ready, once its files are
-- added, with what its build wrote and when.
UPDATE snapshot
SET status = 'ready', source_seq = @source_seq, validation_report = @validation_report,
    schema_version = @schema_version, content_hash = @content_hash, counts = @counts, built_at = @built_at
WHERE workspace_id = @workspace_id AND id = @id AND status = 'building';
