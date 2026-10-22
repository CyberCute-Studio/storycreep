-- Boards and folders (package board, PRD FR-PROJ-02, §5.3). folder and board
-- are tenant tables, which a transaction of their workspace reads. The
-- commands of a project apply one batch at a time, under the lock of the
-- project's row, so what these queries read of the project's boards and
-- folders stays as it is until the batch changes it.

-- name: InsertFolder :exec
-- InsertFolder adds a folder.
INSERT INTO folder (id, workspace_id, project_id, kind, parent_id, name, "order", created_by, updated_by)
VALUES (@id, @workspace_id, @project_id, @kind, sqlc.narg(parent_id), @name, @sort_key, @created_by, @created_by);

-- name: FolderAny :one
-- FolderAny returns a folder of the project, whether or not it is deleted,
-- and no row if the project has no such folder.
SELECT sqlc.embed(folder)
FROM folder
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: FoldersByID :many
-- FoldersByID returns the folders of the project whose IDs are among ids,
-- whether or not they are deleted.
SELECT sqlc.embed(folder)
FROM folder
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: Folders :many
-- Folders returns the project's folders that are not deleted, of the kind
-- kind, or of every kind if it is null, in the order of their IDs, after the
-- folder after, or from the first if after is null.
SELECT sqlc.embed(folder)
FROM folder
WHERE workspace_id = @workspace_id AND project_id = @project_id AND deleted_at IS NULL
    AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
    AND (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: FolderRevs :many
-- FolderRevs returns the revisions of the project's folders whose IDs are
-- among ids, deleted ones included, for the pipeline's check of the
-- revisions that a batch expects.
SELECT id, rev FROM folder
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: FolderAncestors :many
-- FolderAncestors returns the folder id of the project and each folder above
-- it, from it to the folder at the top, each with whether it is deleted, or
-- no row if the project has no such folder. A tree of folders has no cycle,
-- which the moves of folders keep it from, and the walk stops at one all the
-- same.
WITH RECURSIVE up (id, parent_id, deleted, depth) AS (
    SELECT f.id, f.parent_id, f.deleted_at IS NOT NULL, 0
    FROM folder f
    WHERE f.workspace_id = @workspace_id AND f.project_id = @project_id AND f.id = @id
    UNION ALL
    SELECT f.id, f.parent_id, f.deleted_at IS NOT NULL, up.depth + 1
    FROM folder f
    JOIN up ON f.id = up.parent_id
    WHERE f.workspace_id = @workspace_id AND f.project_id = @project_id
) CYCLE id SET is_cycle USING path
SELECT up.id, up.deleted::boolean AS deleted FROM up WHERE NOT up.is_cycle ORDER BY up.depth;

-- name: FolderHolds :one
-- FolderHolds returns whether the folder id of the project holds a folder or
-- a board that is not deleted. A later table whose rows go into a folder, as
-- the variables and the components (plan items 34 and 36), adds its rows
-- here, which TestFolderChildren holds it to.
SELECT (
    EXISTS (SELECT 1 FROM folder f WHERE f.workspace_id = @workspace_id AND f.project_id = @project_id
        AND f.parent_id = @id AND f.deleted_at IS NULL)
    OR EXISTS (SELECT 1 FROM board b WHERE b.workspace_id = @workspace_id AND b.project_id = @project_id
        AND b.folder_id = @id AND b.deleted_at IS NULL)
)::boolean AS holds;

-- name: LastSortKeys :one
-- LastSortKeys returns the greatest order of the project's children of the
-- folder parent_id of the kind kind, or of those at the top if parent_id is
-- null, but for the child moved, if it is not null, which moves there: its
-- folders, and, for a folder of boards, its boards, deleted ones included, so
-- that one restored later keeps its place, as last, and the greatest of
-- those that are not deleted as last_live, each an empty string if there is
-- none.
SELECT coalesce(max(c.sort_key), '')::text AS last,
    coalesce(max(c.sort_key) FILTER (WHERE NOT c.deleted), '')::text AS last_live
FROM (
    SELECT f."order" AS sort_key, f.deleted_at IS NOT NULL AS deleted FROM folder f
    WHERE f.workspace_id = @workspace_id AND f.project_id = @project_id AND f.kind = @kind
        AND (f.parent_id = sqlc.narg(parent_id)::uuid OR sqlc.narg(parent_id)::uuid IS NULL AND f.parent_id IS NULL)
        AND f.id IS DISTINCT FROM sqlc.narg(moved)::uuid
    UNION ALL
    SELECT b."order", b.deleted_at IS NOT NULL FROM board b
    WHERE @kind::text = 'board' AND b.workspace_id = @workspace_id AND b.project_id = @project_id
        AND (b.folder_id = sqlc.narg(parent_id)::uuid OR sqlc.narg(parent_id)::uuid IS NULL AND b.folder_id IS NULL)
        AND b.id IS DISTINCT FROM sqlc.narg(moved)::uuid
) c;

-- name: UpdateFolderName :one
-- UpdateFolderName renames the folder, unless it has the name already, and
-- returns its new revision, or no row if it does.
UPDATE folder SET name = @name, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND name <> @name
RETURNING rev;

-- name: MoveFolder :one
-- MoveFolder moves the folder into the folder parent_id, or to the top if it
-- is null, at the order sort_key, unless it is there already, and returns its
-- new revision, or no row if it is.
UPDATE folder SET parent_id = sqlc.narg(parent_id), "order" = @sort_key, rev = rev + 1, updated_at = now(),
    updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id
    AND (parent_id IS DISTINCT FROM sqlc.narg(parent_id) OR "order" <> @sort_key)
RETURNING rev;

-- name: TrashFolder :one
-- TrashFolder deletes the folder, unless it is deleted already, and returns
-- its new revision, or no row if it is.
UPDATE folder SET deleted_at = now(), rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NULL
RETURNING rev;

-- name: RestoreFolder :one
-- RestoreFolder restores the deleted folder, unless it is not deleted, and
-- returns its new revision, or no row if it is not.
UPDATE folder SET deleted_at = NULL, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NOT NULL
RETURNING rev;

-- name: InsertBoard :exec
-- InsertBoard adds a board.
INSERT INTO board (id, workspace_id, project_id, folder_id, "order", name, slug, description, color, created_by,
    updated_by)
VALUES (@id, @workspace_id, @project_id, sqlc.narg(folder_id), @sort_key, @name, @slug, @description, @color,
    @created_by, @created_by);

-- name: BoardAny :one
-- BoardAny returns a board of the project, whether or not it is in the
-- trash, and no row if the project has no such board.
SELECT sqlc.embed(board)
FROM board
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: Board :one
-- Board returns a board of the project that is not in the trash, and no row
-- if the project has no such board, or it is.
SELECT sqlc.embed(board)
FROM board
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NULL;

-- name: BoardsByID :many
-- BoardsByID returns the boards of the project whose IDs are among ids,
-- whether or not they are in the trash.
SELECT sqlc.embed(board)
FROM board
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: Boards :many
-- Boards returns the project's boards that are not in the trash, in the
-- order of their IDs, after the board after, or from the first if after is
-- null.
SELECT sqlc.embed(board)
FROM board
WHERE workspace_id = @workspace_id AND project_id = @project_id AND deleted_at IS NULL
    AND (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: BoardRevs :many
-- BoardRevs returns the revisions of the project's boards whose IDs are
-- among ids, those in the trash included, for the pipeline's check of the
-- revisions that a batch expects.
SELECT id, rev FROM board
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: ProjectBoardCount :one
-- ProjectBoardCount returns how many boards the project has that are not in
-- its trash: what the project adds to its workspace's boards while it is not
-- in the workspace's trash (package project).
SELECT count(*) FROM board
WHERE workspace_id = @workspace_id AND project_id = @project_id AND deleted_at IS NULL;

-- name: BoardSlugsFrom :many
-- BoardSlugsFrom returns the slugs of the project's boards, those in the
-- trash included, whose slugs are unique within the project too, that start
-- with stem: the slugs from stem up to stem with a tilde after it, which
-- every slug that starts with stem sorts before, because a slug's characters
-- sort before the tilde in the bytes that board's slugs are compared by, so
-- that the index of their uniqueness finds them, rather than every slug of
-- the project.
SELECT slug FROM board
WHERE workspace_id = @workspace_id AND project_id = @project_id
    AND slug >= @stem::text AND slug < @stem::text || '~';

-- name: BoardWithSlug :one
-- BoardWithSlug returns the ID of the project's board whose slug is slug,
-- whether or not it is in the trash, and no row if none has it.
SELECT id FROM board WHERE workspace_id = @workspace_id AND project_id = @project_id AND slug = @slug;

-- name: UpdateBoardName :one
-- UpdateBoardName renames the board, unless it has the name already, and
-- returns its new revision, or no row if it does. Its slug stays as it is
-- (FR-PROJ-02 AC1).
UPDATE board SET name = @name, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND name <> @name
RETURNING rev;

-- name: UpdateBoardDescription :one
-- UpdateBoardDescription changes the board's description, unless it has the
-- description already, and returns its new revision, or no row if it does.
UPDATE board SET description = @description, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND description <> @description
RETURNING rev;

-- name: UpdateBoardColor :one
-- UpdateBoardColor changes the board's colour, unless it has the colour
-- already, and returns its new revision, or no row if it does.
UPDATE board SET color = @color, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND color <> @color
RETURNING rev;

-- name: UpdateBoardSlug :one
-- UpdateBoardSlug changes the board's slug, unless it has the slug already,
-- and returns its new revision, or no row if it does.
UPDATE board SET slug = @slug, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND slug <> @slug
RETURNING rev;

-- name: MoveBoard :one
-- MoveBoard moves the board into the folder folder_id, or to the top if it
-- is null, at the order sort_key, unless it is there already, and returns its
-- new revision, or no row if it is.
UPDATE board SET folder_id = sqlc.narg(folder_id), "order" = @sort_key, rev = rev + 1, updated_at = now(),
    updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id
    AND (folder_id IS DISTINCT FROM sqlc.narg(folder_id) OR "order" <> @sort_key)
RETURNING rev;

-- name: TrashBoard :one
-- TrashBoard deletes the board to the project's trash, unless it is there
-- already, and returns its new revision, or no row if it is.
UPDATE board SET deleted_at = now(), rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NULL
RETURNING rev;

-- name: RestoreBoard :one
-- RestoreBoard takes the board out of the project's trash, unless it is not
-- there, and returns its new revision, or no row if it is not.
UPDATE board SET deleted_at = NULL, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NOT NULL
RETURNING rev;
