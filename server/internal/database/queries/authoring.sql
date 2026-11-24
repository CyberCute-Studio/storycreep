-- The authoring export (package authoring, PRD FR-DATA-01, plan item 83, note
-- N90). What it reads of a draft that the serializer reads too, as outputs,
-- connections, expressions, and attribute values, it reads with the
-- serializer's queries; these read what the published format does not hold,
-- through the live views, so that nothing in the trash is exported.

-- name: AuthoringProject :one
-- AuthoringProject returns what the archive holds of the project itself, and
-- no row if it is in the workspace's trash.
SELECT name, slug, description, settings FROM project
WHERE workspace_id = @workspace_id AND id = @id AND deleted_at IS NULL;

-- name: AuthoringEntry :one
-- AuthoringEntry says whether the board of the project's entry, and its node
-- on that board, are outside the trash, as the settings keep an entry that is
-- not, for a restore from the trash to mend, which no archive holds.
SELECT EXISTS (
        SELECT 1 FROM live_board b
        WHERE b.workspace_id = @workspace_id AND b.project_id = @project_id AND b.id = @board_id
    ) AS board_live,
    EXISTS (
        SELECT 1 FROM live_node n
        WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.id = @node_id
            AND n.board_id = @board_id
    ) AS node_live;

-- name: AuthoringFolders :many
-- AuthoringFolders returns the project's folders that are not in its trash.
SELECT id, kind, parent_id, name, "order" AS sort_key FROM live_folder
WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: AuthoringBoards :many
-- AuthoringBoards returns the project's boards that are not in its trash, each
-- with its entry node unless that is in the trash, which no archive holds.
SELECT b.id, b.folder_id, b.folder_kind, b."order" AS sort_key, b.name, b.slug, b.description, b.color,
    e.id AS entry_node_id
FROM live_board b
LEFT JOIN live_node e ON e.workspace_id = b.workspace_id AND e.project_id = b.project_id AND e.id = b.entry_node_id
WHERE b.workspace_id = @workspace_id AND b.project_id = @project_id ORDER BY b.id;

-- name: AuthoringVariables :many
-- AuthoringVariables returns the project's variables that are not in its
-- trash.
SELECT id, scope, board_id, folder_id, folder_kind, "order" AS sort_key, key, type, default_value, description,
    reset_on_enter
FROM live_variable WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: AuthoringComponentTypes :many
-- AuthoringComponentTypes returns the project's component types that are not
-- in its trash.
SELECT id, key, name, icon FROM live_component_type
WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: AuthoringComponents :many
-- AuthoringComponents returns the project's components that are not in its
-- trash, with the description that the published format leaves out.
SELECT id, component_type_id, folder_id, folder_kind, "order" AS sort_key, key, name, description, image_asset_id
FROM live_component WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: AuthoringNodes :many
-- AuthoringNodes returns the project's nodes that are not in its trash, their
-- geometry as the decimals that it is stored as, each with its speaker and a
-- jumper's target unless they are in the trash, which no archive holds. A
-- target is its board and its node together, as the table requires of a row.
SELECT n.id, n.board_id, n.type, coalesce(n.kind, '')::text AS kind, n.title, n.text,
    s.id AS speaker_component_id, n.cover_asset_id, n.color, n.tags, n.x::text AS x, n.y::text AS y,
    coalesce(n.width::text, '')::text AS width, coalesce(n.height::text, '')::text AS height, n.is_ending,
    t.board_id AS target_board_id, t.id AS target_node_id
FROM live_node n
LEFT JOIN live_component s ON s.workspace_id = n.workspace_id AND s.project_id = n.project_id
    AND s.id = n.speaker_component_id
LEFT JOIN live_node t ON t.workspace_id = n.workspace_id AND t.project_id = n.project_id
    AND t.id = n.target_node_id AND t.board_id = n.target_board_id
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id ORDER BY n.id;

-- name: AuthoringMutes :many
-- AuthoringMutes returns the project's validation mutes, each with its owner,
-- of which the export keeps those of owners outside the trash, and with who
-- muted it last.
SELECT node_id, connection_id, branch_output_id, variable_id, board_id, code, reason, updated_by
FROM validation_mute WHERE workspace_id = @workspace_id AND project_id = @project_id
ORDER BY code, coalesce(node_id, connection_id, branch_output_id, variable_id, board_id);

-- name: AuthoringComments :many
-- AuthoringComments returns the comments of the project's threads that are
-- shown, as Comments reads them: of each thread its root, deleted or not, and
-- its replies that are not deleted, by thread, the root first.
SELECT c.id, c.parent_id, c.thread_id, c.body, c.resolved_at, c.resolved_by, c.edited_at, c.deleted_at, c.created_by,
    c.created_at, t.target_type::text AS target_type, t.target_id::uuid AS target_id
FROM comment c
JOIN comment_thread_target t ON t.workspace_id = c.workspace_id AND t.project_id = c.project_id
    AND t.thread_id = c.thread_id
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id
    AND (c.deleted_at IS NULL OR c.parent_id IS NULL)
    AND EXISTS (SELECT 1 FROM live_comment l WHERE l.workspace_id = c.workspace_id AND l.project_id = c.project_id
        AND l.thread_id = c.thread_id)
ORDER BY c.thread_id, c.parent_id NULLS FIRST, c.created_at, c.id;

-- name: AuthoringPlayStates :many
-- AuthoringPlayStates returns the project's play states that are not in its
-- trash.
SELECT id, name, state, names, created_by, created_at FROM live_play_state
WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: AuthoringAssets :many
-- AuthoringAssets returns the project's assets, with the keys of their files.
SELECT id, sha256, mime, bytes, width, height, storage_key FROM asset
WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: AuthoringReleases :many
-- AuthoringReleases returns the project's ready snapshots, oldest first.
SELECT id, version, note, acknowledged_seq, acknowledged_warnings, schema_version, content_hash, source_seq, counts,
    validation_report, published_by, published_at, built_at
FROM snapshot WHERE workspace_id = @workspace_id AND project_id = @project_id AND status = 'ready' ORDER BY id;

-- name: AuthoringPeople :many
-- AuthoringPeople returns the display names of the users of ids, whom a full
-- archive names.
SELECT id, display_name FROM "user" WHERE id = ANY(@ids::uuid[]) ORDER BY id;

-- name: InsertAuthoringExport :one
-- InsertAuthoringExport adds an export that waits for its job, unless the user
-- has one of the project on its way, when it adds no row.
INSERT INTO authoring_export (id, workspace_id, project_id, requested_by, snapshot_id, profile, include_assets,
    include_releases, storage_key, expires_at)
VALUES (@id, @workspace_id, @project_id, @requested_by, @snapshot_id, @profile, @include_assets, @include_releases,
    @storage_key, @expires_at)
ON CONFLICT (workspace_id, project_id, requested_by) WHERE status IN ('queued', 'building') DO NOTHING
RETURNING *;

-- name: AuthoringExport :one
-- AuthoringExport returns the export of the project that the user asked for,
-- unless its time has passed, whether or not its job has deleted it yet.
SELECT * FROM authoring_export
WHERE workspace_id = @workspace_id AND project_id = @project_id AND requested_by = @requested_by AND id = @id
    AND expires_at > now();

-- name: AuthoringExports :many
-- AuthoringExports returns the user's exports of the project whose time has
-- not passed, newest first, at most row_limit of them, after the export of the
-- UUID after, or from the newest for the zero UUID.
SELECT * FROM authoring_export
WHERE workspace_id = @workspace_id AND project_id = @project_id AND requested_by = @requested_by
    AND expires_at > now()
    AND (@after::uuid = '00000000-0000-0000-0000-000000000000' OR id < @after::uuid)
ORDER BY id DESC LIMIT @row_limit;

-- name: AuthoringExportInProgress :one
-- AuthoringExportInProgress returns the ID of the user's export of the project
-- that is on its way and that its user can still read.
SELECT id FROM authoring_export
WHERE workspace_id = @workspace_id AND project_id = @project_id AND requested_by = @requested_by
    AND status IN ('queued', 'building') AND expires_at > now();

-- name: FailExpiredAuthoringExports :exec
-- FailExpiredAuthoringExports ends the user's export of the project that is on
-- its way past its time, which would otherwise keep another from being added
-- until its expiry job has run.
UPDATE authoring_export SET status = 'failed', finished_at = now()
WHERE workspace_id = @workspace_id AND project_id = @project_id AND requested_by = @requested_by
    AND status IN ('queued', 'building') AND expires_at <= now();

-- name: AuthoringExportOfJob :one
-- AuthoringExportOfJob returns the export of a job, whoever asked for it.
SELECT * FROM authoring_export WHERE workspace_id = @workspace_id AND id = @id;

-- name: StartAuthoringExport :execrows
-- StartAuthoringExport marks an export that waits as being built. An attempt
-- that follows one that failed finds it building already.
UPDATE authoring_export SET status = 'building'
WHERE workspace_id = @workspace_id AND id = @id AND status IN ('queued', 'building');

-- name: FinishAuthoringExport :execrows
-- FinishAuthoringExport makes an export that is being built ready.
UPDATE authoring_export SET status = 'ready', size_bytes = @size_bytes, asset_count = @asset_count, file_name = @file_name, finished_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND status = 'building';

-- name: FailAuthoringExport :one
-- FailAuthoringExport fails an export that has not ended, and returns the key
-- of its archive, or no row if it has ended.
UPDATE authoring_export SET status = 'failed', finished_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND status IN ('queued', 'building')
RETURNING storage_key;

-- name: DeleteAuthoringExport :execrows
-- DeleteAuthoringExport deletes an export whose time has passed.
DELETE FROM authoring_export WHERE workspace_id = @workspace_id AND id = @id;
