-- Variables (package variable, PRD FR-VAR-01, FR-VAR-02). variable is a
-- tenant table, which a transaction of its workspace reads. The commands of a
-- project apply one batch at a time, under the lock of the project's row, so
-- what these queries read of the project's variables stays as it is until the
-- batch changes it. What is not in the project's trash, neither a deleted
-- variable nor one of a board in the trash, is read through the live view,
-- live_variable (package trash).

-- name: InsertVariable :exec
-- InsertVariable adds a variable.
INSERT INTO variable (id, workspace_id, project_id, scope, board_id, folder_id, "order", key, type, default_value,
    description, reset_on_enter, created_by, updated_by)
VALUES (@id, @workspace_id, @project_id, @scope, sqlc.narg(board_id), sqlc.narg(folder_id), @sort_key, @key, @type,
    @default_value, @description, @reset_on_enter, @created_by, @created_by);

-- name: VariableAny :one
-- VariableAny returns a variable of the project, whether or not it is in the
-- trash, and whether its board is, which board_deleted says, and no row if
-- the project has no such variable.
SELECT sqlc.embed(variable), (b.deleted_at IS NOT NULL)::boolean AS board_deleted
FROM variable
LEFT JOIN board b ON b.workspace_id = variable.workspace_id AND b.project_id = variable.project_id
    AND b.id = variable.board_id
WHERE variable.workspace_id = @workspace_id AND variable.project_id = @project_id AND variable.id = @id;

-- name: Variable :one
-- Variable returns a variable of the project that is not in the trash, and no
-- row if the project has no such variable, or it is.
SELECT sqlc.embed(live_variable)
FROM live_variable
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: VariablesByID :many
-- VariablesByID returns the variables of the project whose IDs are among ids,
-- whether or not they are in the trash, each with when its board was deleted
-- to the trash, as board_deleted_at, if it is there, which a variable is in
-- with it.
SELECT sqlc.embed(variable), b.deleted_at AS board_deleted_at
FROM variable
LEFT JOIN board b ON b.workspace_id = variable.workspace_id AND b.project_id = variable.project_id
    AND b.id = variable.board_id
WHERE variable.workspace_id = @workspace_id AND variable.project_id = @project_id
    AND variable.id = ANY(@ids::uuid[]);

-- name: Variables :many
-- Variables returns the project's variables that are not in the trash, in the
-- order of their IDs, after the variable after, or from the first if after is
-- null.
SELECT sqlc.embed(live_variable)
FROM live_variable
WHERE workspace_id = @workspace_id AND project_id = @project_id
    AND (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: VariableRevs :many
-- VariableRevs returns the revisions of the project's variables whose IDs are
-- among ids, those in the trash included, for the pipeline's check of the
-- revisions that a batch expects.
SELECT id, rev FROM variable
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: VariableWithKey :one
-- VariableWithKey returns the ID of the project's variable that is not deleted
-- whose key is key, among its global variables if board_id is null, and
-- otherwise among the board board_id's, those of a board in the trash
-- included, which keep their keys, and no row if none has it. Each branch
-- reads the index of one scope's keys.
SELECT v.id FROM variable v
WHERE sqlc.narg(board_id)::uuid IS NULL AND v.workspace_id = @workspace_id AND v.project_id = @project_id
    AND v.scope = 'global' AND v.deleted_at IS NULL AND v.key = @key
UNION ALL
SELECT v.id FROM variable v
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id AND v.scope = 'board'
    AND v.board_id = sqlc.narg(board_id)::uuid AND v.deleted_at IS NULL AND v.key = @key
LIMIT 1;

-- name: BoardVariableFolders :many
-- BoardVariableFolders returns the folders that are deleted of the board's
-- variables that are not deleted themselves, which restoring the board from
-- the trash brings back with it (package board), so that those folders, and
-- the deleted ones above them, come back too. A folder that is not deleted
-- has none deleted above it, since a folder is deleted only once it holds
-- nothing that is not, so restoring the board restores nothing for it.
SELECT DISTINCT v.folder_id::uuid AS folder_id FROM variable v
JOIN folder f ON f.workspace_id = v.workspace_id AND f.project_id = v.project_id AND f.id = v.folder_id
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id AND v.board_id = @board_id
    AND v.deleted_at IS NULL AND f.deleted_at IS NOT NULL
ORDER BY folder_id;

-- name: UpdateVariableKey :one
-- UpdateVariableKey changes the variable's key, unless it has the key
-- already, and returns its new revision, or no row if it does.
UPDATE variable SET key = @key, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND key <> @key
RETURNING rev;

-- name: UpdateVariableDescription :one
-- UpdateVariableDescription changes the variable's description, unless it
-- has the description already, and returns its new revision, or no row if it
-- does.
UPDATE variable SET description = @description, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND description <> @description
RETURNING rev;

-- name: UpdateVariableValue :one
-- UpdateVariableValue changes the variable's type and its default, unless it
-- has both already, and returns its new revision, or no row if it does.
UPDATE variable SET type = @type, default_value = @default_value, rev = rev + 1, updated_at = now(),
    updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id
    AND (type <> @type OR default_value <> @default_value)
RETURNING rev;

-- name: UpdateVariableScope :one
-- UpdateVariableScope makes the variable global, if board_id is null, or the
-- board board_id's, unless it is already, and returns its new revision, or no
-- row if it is.
UPDATE variable SET scope = @scope, board_id = sqlc.narg(board_id), rev = rev + 1, updated_at = now(),
    updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id
    AND board_id IS DISTINCT FROM sqlc.narg(board_id)
RETURNING rev;

-- name: UpdateVariableResetOnEnter :one
-- UpdateVariableResetOnEnter sets or unsets the variable's reset_on_enter,
-- unless it is so already, and returns its new revision, or no row if it is.
UPDATE variable SET reset_on_enter = @reset_on_enter, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND reset_on_enter <> @reset_on_enter
RETURNING rev;

-- name: MoveVariable :one
-- MoveVariable moves the variable into the folder folder_id, or to the top if
-- it is null, at the order sort_key, unless it is there already, and returns
-- its new revision, or no row if it is.
UPDATE variable SET folder_id = sqlc.narg(folder_id), "order" = @sort_key, rev = rev + 1, updated_at = now(),
    updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id
    AND (folder_id IS DISTINCT FROM sqlc.narg(folder_id) OR "order" <> @sort_key)
RETURNING rev;

-- name: TrashVariable :one
-- TrashVariable deletes the variable to the project's trash, unless it is
-- there already, and returns its new revision, or no row if it is.
UPDATE variable SET deleted_at = now(), rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NULL
RETURNING rev;

-- name: RestoreVariable :one
-- RestoreVariable takes the variable out of the project's trash, unless it is
-- not there, and returns its new revision, or no row if it is not.
UPDATE variable SET deleted_at = NULL, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NOT NULL
RETURNING rev;
