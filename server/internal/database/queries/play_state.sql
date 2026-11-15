-- Play states (package playstate, PRD FR-PLAY-05). play_state is a tenant
-- table, which a transaction of its workspace reads. The commands of a
-- project apply one batch at a time, under the lock of the project's row, so
-- what these queries read of the project's play states stays as it is until
-- the batch changes it. What is not in the project's trash is read through
-- the live view, live_play_state (package trash).

-- name: InsertPlayState :exec
-- InsertPlayState adds a play state.
INSERT INTO play_state (id, workspace_id, project_id, name, state, names, created_by, updated_by)
VALUES (@id, @workspace_id, @project_id, @name, @state, @names, @created_by, @created_by);

-- name: PlayStateAny :one
-- PlayStateAny returns a play state of the project, whether or not it is in
-- the trash, and no row if the project has no such play state.
SELECT * FROM play_state
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: PlayState :one
-- PlayState returns a play state of the project that is not in the trash, and
-- no row if the project has no such play state, or it is.
SELECT sqlc.embed(live_play_state)
FROM live_play_state
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: PlayStatesByID :many
-- PlayStatesByID returns the play states of the project whose IDs are among
-- ids, whether or not they are in the trash.
SELECT * FROM play_state
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: PlayStates :many
-- PlayStates returns the project's play states that are not in the trash, in
-- the order of their IDs, after the play state after, or from the first if
-- after is null, each without its state, which a read of the play state
-- alone returns.
SELECT id, workspace_id, project_id, name, rev, created_by, updated_by, created_at, updated_at
FROM live_play_state
WHERE workspace_id = @workspace_id AND project_id = @project_id
    AND (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: PlayStateRevs :many
-- PlayStateRevs returns the revisions of the project's play states whose IDs
-- are among ids, those in the trash included, for the pipeline's check of the
-- revisions that a batch expects.
SELECT id, rev FROM play_state
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: RenamePlayState :one
-- RenamePlayState changes the play state's name, unless it has the name
-- already, and returns its new revision, or no row if it does.
UPDATE play_state SET name = @name, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND name <> @name
RETURNING rev;

-- name: UpdatePlayState :one
-- UpdatePlayState changes the play state's state and the names of its
-- values, unless it has both already, and returns its new revision, or no
-- row if it does.
UPDATE play_state SET state = @state, names = @names, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id
    AND (state <> @state OR names <> @names)
RETURNING rev;

-- name: TrashPlayState :one
-- TrashPlayState deletes the play state to the project's trash, unless it is
-- there already, and returns its new revision, or no row if it is.
UPDATE play_state SET deleted_at = now(), rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NULL
RETURNING rev;

-- name: RestorePlayState :one
-- RestorePlayState takes the play state out of the project's trash, unless it
-- is not there, and returns its new revision, or no row if it is not.
UPDATE play_state SET deleted_at = NULL, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NOT NULL
RETURNING rev;
