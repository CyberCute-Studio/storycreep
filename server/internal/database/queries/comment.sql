-- Threaded comments (package comment, PRD FR-COMMENT-01). comment is a tenant
-- table, which a transaction of its workspace reads. The commands of a
-- project apply one batch at a time, under the lock of the project's row, so
-- what these queries read of the project's comments stays as it is until the
-- batch changes it. What is shown is read through the live view,
-- live_comment, which leaves out the comments that are deleted and those of a
-- thread whose target is in the project's trash (package trash): a thread is
-- shown while the view holds one of its comments, with its root, deleted or
-- not, and the replies that the view holds. A thread's target is its root's,
-- as the view comment_thread_target reads it, with the target's board and
-- node, as the reads of the API name them, and whether it is outside the
-- trash.

-- name: InsertComment :exec
-- InsertComment adds a comment: a root, with its target, one of node_id,
-- connection_id, board_id, and variable_id, which starts its thread now, or a
-- reply, with its parent, of the thread that its parent started.
INSERT INTO comment (id, workspace_id, project_id, parent_id, node_id, connection_id, board_id, variable_id, body,
    created_by, thread_at)
VALUES (@id, @workspace_id, @project_id, sqlc.narg(parent_id), sqlc.narg(node_id), sqlc.narg(connection_id),
    sqlc.narg(board_id), sqlc.narg(variable_id), @body, @created_by,
    coalesce((SELECT p.thread_at FROM comment p WHERE p.workspace_id = @workspace_id AND p.project_id = @project_id
        AND p.id = sqlc.narg(parent_id)), now()));

-- name: CommentAny :one
-- CommentAny returns a comment of the project, whether or not it is deleted,
-- and no row if the project has no such comment, with whether its thread's
-- target is outside the trash, as the live views read it, and whether a
-- comment of its thread, the root or a reply, is not deleted, which together
-- say whether its thread is shown.
SELECT sqlc.embed(c), t.target_live::boolean AS target_live,
    EXISTS (SELECT 1 FROM comment k WHERE k.workspace_id = c.workspace_id AND k.project_id = c.project_id
        AND k.thread_id = c.thread_id AND k.deleted_at IS NULL) AS thread_kept
FROM comment c
JOIN comment_thread_target t ON t.workspace_id = c.workspace_id AND t.project_id = c.project_id
    AND t.thread_id = c.thread_id
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.id = @id;

-- name: CommentTarget :one
-- CommentTarget returns whether the project has the entity that is the one
-- of node_id, connection_id, board_id, and variable_id that is not null, in
-- the trash or not, and whether it is outside the trash, as its live view
-- reads it, and as comment_thread_target reads it of a thread's target.
SELECT
    (EXISTS (SELECT 1 FROM node n WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id
            AND n.id = sqlc.narg(node_id))
        OR EXISTS (SELECT 1 FROM connection n WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id
            AND n.id = sqlc.narg(connection_id))
        OR EXISTS (SELECT 1 FROM board n WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id
            AND n.id = sqlc.narg(board_id))
        OR EXISTS (SELECT 1 FROM variable n WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id
            AND n.id = sqlc.narg(variable_id)))::boolean AS found,
    (EXISTS (SELECT 1 FROM live_node n WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id
            AND n.id = sqlc.narg(node_id))
        OR EXISTS (SELECT 1 FROM live_connection n WHERE n.workspace_id = @workspace_id
            AND n.project_id = @project_id AND n.id = sqlc.narg(connection_id))
        OR EXISTS (SELECT 1 FROM live_board n WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id
            AND n.id = sqlc.narg(board_id))
        OR EXISTS (SELECT 1 FROM live_variable n WHERE n.workspace_id = @workspace_id
            AND n.project_id = @project_id AND n.id = sqlc.narg(variable_id)))::boolean AS live;

-- name: CommentRevs :many
-- CommentRevs returns the revisions of the project's comments whose IDs are
-- among ids, those deleted included, for the pipeline's check of the
-- revisions that a batch expects.
SELECT id, rev FROM comment
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: CommentsByID :many
-- CommentsByID returns the project's comments whose IDs are among ids,
-- whether or not they are deleted, or their threads shown, each with its
-- thread's target, its board and its node, with the board's name and the
-- node's title, and the display names of its author and of whoever resolved
-- its thread, as the API shows them.
SELECT c.id, c.project_id, c.thread_id, c.parent_id, c.body, c.rev, c.resolved_at, c.resolved_by, c.edited_at,
    c.deleted_at, c.created_by, c.created_at, c.updated_at, c.thread_at, t.target_type::text AS target_type,
    t.target_id::uuid AS target_id, b.id AS target_board_id, b.name AS board_name, tn.id AS target_node_id,
    tn.title AS node_title, a.display_name AS author_name, rb.display_name AS resolver_name
FROM comment c
JOIN comment_thread_target t ON t.workspace_id = c.workspace_id AND t.project_id = c.project_id
    AND t.thread_id = c.thread_id
LEFT JOIN board b ON b.workspace_id = t.workspace_id AND b.project_id = t.project_id AND b.id = t.board_id
LEFT JOIN node tn ON tn.workspace_id = t.workspace_id AND tn.project_id = t.project_id AND tn.id = t.node_id
LEFT JOIN "user" a ON a.id = c.created_by
LEFT JOIN "user" rb ON rb.id = c.resolved_by
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.id = ANY(@ids::uuid[]);

-- name: Comments :many
-- Comments returns at most max_rows of the comments of the project's threads
-- that are shown, as CommentsByID returns them, in the order of their
-- threads, newest first, by when each was started, as the server's clock has
-- it rather than the IDs that the clients made, and within a thread its root
-- first, then its replies, oldest first, by when each was posted, in the
-- order of their IDs where those are the same: from the first, unless after
-- is set, or else after the comment of the thread started at after_thread_at
-- whose ID is after_thread, a reply if after_reply is set, posted at
-- after_at, whose ID is after_id. Of a thread, it returns its root, deleted
-- or not, which the API shows as deleted, and its replies that are not
-- deleted. It keeps the threads that are open, for the status open, or
-- resolved, for resolved, or both, for the empty status; that author, unless
-- it is null, wrote a comment of that is not deleted; whose target is the
-- board board, or a node, a connection, or a variable of it, unless it is
-- null; and whose target is the entity of the kind target_type and the ID
-- target_id, unless target_type is empty.
SELECT c.id, c.project_id, c.thread_id, c.parent_id, c.body, c.rev, c.resolved_at, c.resolved_by, c.edited_at,
    c.deleted_at, c.created_by, c.created_at, c.updated_at, c.thread_at, t.target_type::text AS target_type,
    t.target_id::uuid AS target_id, b.id AS target_board_id, b.name AS board_name, tn.id AS target_node_id,
    tn.title AS node_title, a.display_name AS author_name, rb.display_name AS resolver_name
FROM comment c
JOIN comment_thread_target t ON t.workspace_id = c.workspace_id AND t.project_id = c.project_id
    AND t.thread_id = c.thread_id
LEFT JOIN board b ON b.workspace_id = t.workspace_id AND b.project_id = t.project_id AND b.id = t.board_id
LEFT JOIN node tn ON tn.workspace_id = t.workspace_id AND tn.project_id = t.project_id AND tn.id = t.node_id
LEFT JOIN "user" a ON a.id = c.created_by
LEFT JOIN "user" rb ON rb.id = c.resolved_by
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id
    AND (c.deleted_at IS NULL OR c.parent_id IS NULL)
    AND EXISTS (SELECT 1 FROM live_comment l WHERE l.workspace_id = c.workspace_id AND l.project_id = c.project_id
        AND l.thread_id = c.thread_id)
    AND (@status::text = '' OR (@status::text = 'open') = (t.resolved_at IS NULL))
    AND (sqlc.narg(author)::uuid IS NULL OR EXISTS (SELECT 1 FROM live_comment l
        WHERE l.workspace_id = c.workspace_id AND l.project_id = c.project_id AND l.thread_id = c.thread_id
            AND l.created_by = sqlc.narg(author)::uuid))
    AND (sqlc.narg(board)::uuid IS NULL OR t.board_id = sqlc.narg(board)::uuid)
    AND (@target_type::text = ''
        OR t.target_type = @target_type::text AND t.target_id = sqlc.narg(target_id)::uuid)
    AND (NOT @after::boolean
        OR (c.thread_at, c.thread_id) < (@after_thread_at::timestamptz, @after_thread::uuid)
        OR c.thread_id = @after_thread::uuid AND ((c.parent_id IS NOT NULL) > @after_reply::boolean
            OR (c.parent_id IS NOT NULL) = @after_reply::boolean
                AND (c.created_at, c.id) > (@after_at::timestamptz, @after_id::uuid)))
ORDER BY c.thread_at DESC, c.thread_id DESC, c.parent_id IS NOT NULL, c.created_at, c.id
LIMIT @max_rows;

-- name: CommentCounts :many
-- CommentCounts returns, of each target of a thread of the project that is
-- shown, how many of its threads are open, and how many resolved, at most
-- max_rows of them, in the order of the targets' kinds and their IDs: from
-- the first, unless after is set, or else after the target of the kind
-- after_type and the ID after_id. It keeps the targets that are the board
-- board, or a node, a connection, or a variable of it, unless it is null, as
-- Comments does.
SELECT t.target_type::text AS target_type, t.target_id::uuid AS target_id,
    count(*) FILTER (WHERE t.resolved_at IS NULL) AS open,
    count(*) FILTER (WHERE t.resolved_at IS NOT NULL) AS resolved
FROM comment_thread_target t
WHERE t.workspace_id = @workspace_id AND t.project_id = @project_id
    AND EXISTS (SELECT 1 FROM live_comment l WHERE l.workspace_id = t.workspace_id AND l.project_id = t.project_id
        AND l.thread_id = t.thread_id)
    AND (sqlc.narg(board)::uuid IS NULL OR t.board_id = sqlc.narg(board)::uuid)
    AND (NOT @after::boolean OR (t.target_type, t.target_id) > (@after_type::text, @after_id::uuid))
GROUP BY t.target_type, t.target_id
ORDER BY t.target_type, t.target_id
LIMIT @max_rows;

-- name: CommentAuthors :many
-- CommentAuthors returns at most max_rows of the users who wrote a comment of
-- the project that live_comment holds, with their display names, in the
-- order of their IDs, after the user after, or from the first if after is
-- null.
SELECT u.id, u.display_name FROM "user" u
WHERE u.id IN (SELECT l.created_by FROM live_comment l WHERE l.workspace_id = @workspace_id
        AND l.project_id = @project_id AND l.created_by IS NOT NULL)
    AND (sqlc.narg(after)::uuid IS NULL OR u.id > sqlc.narg(after)::uuid)
ORDER BY u.id
LIMIT @max_rows;

-- name: EditComment :one
-- EditComment changes the comment's body, unless it has the body already,
-- and records when it was edited, and returns its new revision, or no row if
-- it has the body.
UPDATE comment SET body = @body, edited_at = now(), updated_at = now(), rev = rev + 1
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND body <> @body
RETURNING rev;

-- name: TrashComment :one
-- TrashComment deletes the comment to the project's trash, unless it is
-- there already, and returns its new revision, or no row if it is. It leaves
-- when the comment last changed, and when it was edited, as they were, as
-- RestoreComment does, since neither changes its body: a comment restored by
-- the undo of its deletion is shown as it was before.
UPDATE comment SET deleted_at = now(), rev = rev + 1
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NULL
RETURNING rev;

-- name: RestoreComment :one
-- RestoreComment takes the comment out of the project's trash, unless it is
-- not there, and returns its new revision, or no row if it is not.
UPDATE comment SET deleted_at = NULL, rev = rev + 1
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NOT NULL
RETURNING rev;

-- name: ResolveComment :one
-- ResolveComment resolves the thread of the root, as resolved_by, unless it
-- is resolved already, and returns the root's new revision, or no row if it
-- is.
UPDATE comment SET resolved_at = now(), resolved_by = @resolved_by, rev = rev + 1
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND parent_id IS NULL
    AND resolved_at IS NULL
RETURNING rev;

-- name: UnresolveComment :one
-- UnresolveComment opens the thread of the root again, unless it is open
-- already, and returns the root's new revision, or no row if it is.
UPDATE comment SET resolved_at = NULL, resolved_by = NULL, rev = rev + 1
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND resolved_at IS NOT NULL
RETURNING rev;
