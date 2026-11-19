-- The accounts' notifications (package notification, PRD FR-COMMENT-02).
-- notification is a tenant table, which a transaction of its workspace reads,
-- so an account's notifications are read one workspace at a time, the
-- workspaces from the account's memberships (auth_user_memberships). A
-- notification is shown while its comment is in live_comment, while its
-- project is not in the trash, and while its recipient reads the project, by
-- the role of their membership of its workspace, or of the membership's grant
-- on the project: member_roles are the roles of a membership that read a
-- project without a grant, and grant_roles those of a grant that read it, as
-- package role resolves them (role.Readers), which the queries take rather
-- than name, so that the roles are resolved in one place.

-- name: InsertMentions :many
-- InsertMentions adds a notification of the comment to each of user_ids,
-- with the ID of the same index of ids, of the mention of the user by the
-- actor, but for a user whom the comment has notified already, and returns
-- the users whom it notifies. It is made as the server's clock has it as the
-- batch applies, as InsertComment posts a comment, so that a notification of
-- a later batch is later.
INSERT INTO notification (id, workspace_id, project_id, user_id, comment_id, actor_id, kind, created_at)
SELECT n.id, @workspace_id, @project_id, n.user_id, @comment_id, sqlc.narg(actor_id), 'mention', clock_timestamp()
FROM (SELECT unnest(@ids::uuid[]) AS id, unnest(@user_ids::uuid[]) AS user_id) n
ON CONFLICT (workspace_id, comment_id, user_id) DO NOTHING
RETURNING user_id;

-- name: NotifyUsers :exec
-- NotifyUsers tells each of user_ids that their notifications changed, by a
-- notification of PostgreSQL's on the channel, whose payload is the user's
-- UUID, which the transaction sends once it commits, and never if it rolls
-- back.
SELECT pg_notify(@channel::text, u::text) FROM unnest(@user_ids::uuid[]) AS u;

-- name: NotifyCommentRecipients :exec
-- NotifyCommentRecipients tells each user whom the comment has notified that
-- their notifications changed, as NotifyUsers does, as once the comment is
-- deleted, which hides their notifications, or restored, which shows them
-- again.
SELECT pg_notify(@channel::text, n.user_id::text) FROM notification n
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.comment_id = @comment_id;

-- name: NotificationWorkspaces :many
-- NotificationWorkspaces returns the workspaces that the user is a member
-- of, guests' included, whose deletion is not pending, in the order of their
-- IDs. It reads the memberships through auth_user_memberships, which returns
-- them only to a transaction whose app.user_id is the user (§5.4).
SELECT m.workspace_id::uuid AS workspace_id
FROM auth_user_memberships(@user_id::uuid) m
JOIN workspace w ON w.id = m.workspace_id
WHERE w.status = 'active'
ORDER BY m.workspace_id;

-- name: Notifications :many
-- Notifications returns at most max_rows of the user's notifications of the
-- workspace that are shown, newest first, by when each was made, and by
-- their IDs where those are the same: from the first, unless after is set,
-- or else after the one made at after_at whose ID is after_id. Each has its
-- comment's thread, body, and thread's target, with the target's board and
-- node, as comment_thread_target reads them, its project's name, and the
-- display name of its actor, who mentioned the user, unless their account is
-- deleted.
SELECT n.id, n.project_id, n.comment_id, n.actor_id, n.created_at, n.read_at, c.thread_id, c.body,
    t.target_type::text AS target_type, t.target_id::uuid AS target_id, t.board_id AS target_board_id,
    t.node_id AS target_node_id, p.name AS project_name, a.display_name AS actor_name
FROM notification n
JOIN live_comment c ON c.workspace_id = n.workspace_id AND c.project_id = n.project_id AND c.id = n.comment_id
JOIN comment_thread_target t ON t.workspace_id = c.workspace_id AND t.project_id = c.project_id
    AND t.thread_id = c.thread_id
JOIN project p ON p.workspace_id = n.workspace_id AND p.id = n.project_id AND p.deleted_at IS NULL
JOIN membership m ON m.workspace_id = n.workspace_id AND m.user_id = n.user_id
LEFT JOIN project_grant g ON g.workspace_id = m.workspace_id AND g.membership_id = m.id
    AND g.project_id = n.project_id
LEFT JOIN "user" a ON a.id = n.actor_id
WHERE n.workspace_id = @workspace_id AND n.user_id = @user_id
    AND (g.role = ANY(@grant_roles::text[]) OR g.role IS NULL AND m.role = ANY(@member_roles::text[]))
    AND (NOT @after::boolean OR (n.created_at, n.id) < (@after_at::timestamptz, @after_id::uuid))
ORDER BY n.created_at DESC, n.id DESC
LIMIT @max_rows;

-- name: UnreadNotifications :one
-- UnreadNotifications returns how many of the user's notifications of the
-- workspace that are shown, as Notifications shows them, are unread.
SELECT count(*) FROM notification n
JOIN live_comment c ON c.workspace_id = n.workspace_id AND c.project_id = n.project_id AND c.id = n.comment_id
JOIN project p ON p.workspace_id = n.workspace_id AND p.id = n.project_id AND p.deleted_at IS NULL
JOIN membership m ON m.workspace_id = n.workspace_id AND m.user_id = n.user_id
LEFT JOIN project_grant g ON g.workspace_id = m.workspace_id AND g.membership_id = m.id
    AND g.project_id = n.project_id
WHERE n.workspace_id = @workspace_id AND n.user_id = @user_id AND n.read_at IS NULL
    AND (g.role = ANY(@grant_roles::text[]) OR g.role IS NULL AND m.role = ANY(@member_roles::text[]));

-- name: NotificationOf :one
-- NotificationOf returns the user's notification of the workspace whose ID
-- is id, shown or not, and no row if the user has no such notification
-- there.
SELECT id, created_at, read_at FROM notification
WHERE workspace_id = @workspace_id AND user_id = @user_id AND id = @id;

-- name: ReadNotifications :execrows
-- ReadNotifications marks read the user's notifications of the workspace
-- that are unread, shown or not, made at or before the one made at
-- through_at whose ID is through_id, by when each was made and by their IDs,
-- as Notifications orders them, so that one made after it stays unread, and
-- returns how many it marked.
UPDATE notification SET read_at = clock_timestamp()
WHERE workspace_id = @workspace_id AND user_id = @user_id AND read_at IS NULL
    AND (created_at, id) <= (@through_at::timestamptz, @through_id::uuid);

-- name: ReadNotification :execrows
-- ReadNotification marks read the user's notification of the workspace
-- whose ID is id, unless it is read already, and returns 1 if it marked it.
UPDATE notification SET read_at = clock_timestamp()
WHERE workspace_id = @workspace_id AND user_id = @user_id AND id = @id AND read_at IS NULL;
