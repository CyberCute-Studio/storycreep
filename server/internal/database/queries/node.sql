-- Nodes (package node, PRD FR-EDIT-02, FR-EDIT-05, FR-EDIT-06): the
-- elements, branches, and jumpers of a project's boards. node is a tenant
-- table, which a transaction of its workspace reads. The commands of a
-- project apply one batch at a time, under the lock of the project's row, so
-- what these queries read of the project's nodes stays as it is until the
-- batch changes it. What is not in the project's trash is read through the
-- live view, live_node, which leaves out the nodes of a board in the trash too
-- (package trash).

-- name: InsertNode :exec
-- InsertNode adds a node.
INSERT INTO node (id, workspace_id, project_id, board_id, type, kind, title, text, speaker_component_id,
    cover_asset_id, color, tags, x, y, width, height, is_ending, target_board_id, target_node_id, created_by,
    updated_by)
VALUES (@id, @workspace_id, @project_id, @board_id, @type, @kind, @title, @text, @speaker_component_id,
    @cover_asset_id, @color, @tags, @x, @y, @width, @height, @is_ending, @target_board_id, @target_node_id,
    @created_by, @created_by);

-- name: DuplicateNode :execrows
-- DuplicateNode adds the node id, a copy of the project's node source_id on
-- the same board, at the position x, y, and returns how many it added: 1, or
-- 0 if the project has no such node.
INSERT INTO node (id, workspace_id, project_id, board_id, type, kind, title, text, speaker_component_id,
    cover_asset_id, color, tags, x, y, width, height, is_ending, target_board_id, target_node_id, created_by,
    updated_by)
SELECT @id::uuid, s.workspace_id, s.project_id, s.board_id, s.type, s.kind, s.title, s.text, s.speaker_component_id,
    s.cover_asset_id, s.color, s.tags, @x::numeric, @y::numeric, s.width, s.height, s.is_ending, s.target_board_id,
    s.target_node_id, sqlc.narg(created_by)::uuid, sqlc.narg(created_by)::uuid
FROM node s
WHERE s.workspace_id = @workspace_id AND s.project_id = @project_id AND s.id = @source_id;

-- name: NodeAny :one
-- NodeAny returns a node of the project, whether or not it is in the trash,
-- and whether its board is, which board_deleted says, and no row if the
-- project has no such node.
SELECT sqlc.embed(node), (b.deleted_at IS NOT NULL)::boolean AS board_deleted
FROM node
JOIN board b ON b.workspace_id = node.workspace_id AND b.project_id = node.project_id AND b.id = node.board_id
WHERE node.workspace_id = @workspace_id AND node.project_id = @project_id AND node.id = @id;

-- name: NodesByID :many
-- NodesByID returns the nodes of the project whose IDs are among ids, whether
-- or not they are in the trash, each with when its board was deleted to the
-- trash, as board_deleted_at, if it is there, which a node is in with it.
SELECT sqlc.embed(node), b.deleted_at AS board_deleted_at
FROM node
JOIN board b ON b.workspace_id = node.workspace_id AND b.project_id = node.project_id AND b.id = node.board_id
WHERE node.workspace_id = @workspace_id AND node.project_id = @project_id AND node.id = ANY(@ids::uuid[]);

-- name: NodeRevs :many
-- NodeRevs returns the revisions of the project's nodes whose IDs are among
-- ids, those in the trash included, for the pipeline's check of the
-- revisions that a batch expects.
SELECT id, rev FROM node
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: NodePositions :many
-- NodePositions returns the positions of the project's nodes whose IDs are
-- among ids, whether or not they are in the trash, each with whether it is,
-- itself or with its board, which a move reads before it moves them.
SELECT n.id, n.x, n.y, (n.deleted_at IS NOT NULL OR b.deleted_at IS NOT NULL)::boolean AS deleted
FROM node n
JOIN board b ON b.workspace_id = n.workspace_id AND b.project_id = n.project_id AND b.id = n.board_id
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.id = ANY(@ids::uuid[]);

-- name: MoveNodes :many
-- MoveNodes moves each of the project's nodes whose ID is among ids to the
-- position at the same index of xs and ys, unless it is there already, and
-- returns the ID and the new revision of each that it moved. The arrays have
-- one length, so their unnest calls, which PostgreSQL runs side by side, pair
-- their elements by index.
UPDATE node n SET x = m.x, y = m.y, rev = n.rev + 1, updated_at = now(), updated_by = @updated_by
FROM (SELECT unnest(@ids::uuid[]) AS id, unnest(@xs::numeric[]) AS x, unnest(@ys::numeric[]) AS y) AS m
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.id = m.id AND (n.x <> m.x OR n.y <> m.y)
RETURNING n.id, n.rev;

-- name: UpdateNode :one
-- UpdateNode sets every field of the node that a change of it changes,
-- unless each has the value given already, and returns its new revision, or
-- no row if every one has.
UPDATE node SET kind = @kind, title = @title, text = @text, speaker_component_id = @speaker_component_id,
    cover_asset_id = @cover_asset_id, color = @color, tags = @tags, width = @width, height = @height,
    is_ending = @is_ending, target_board_id = @target_board_id, target_node_id = @target_node_id, rev = rev + 1,
    updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id
    AND (kind IS DISTINCT FROM @kind OR title <> @title OR text IS DISTINCT FROM @text
        OR speaker_component_id IS DISTINCT FROM @speaker_component_id
        OR cover_asset_id IS DISTINCT FROM @cover_asset_id OR color <> @color OR tags <> @tags
        OR width IS DISTINCT FROM @width OR height IS DISTINCT FROM @height OR is_ending <> @is_ending
        OR target_board_id IS DISTINCT FROM @target_board_id OR target_node_id IS DISTINCT FROM @target_node_id)
RETURNING rev;

-- name: TrashNode :one
-- TrashNode deletes the node to the project's trash, unless it is there
-- already, and returns its new revision, or no row if it is.
UPDATE node SET deleted_at = now(), rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NULL
RETURNING rev;

-- name: RestoreNode :one
-- RestoreNode takes the node out of the project's trash, unless it is not
-- there, and returns its new revision, or no row if it is not.
UPDATE node SET deleted_at = NULL, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NOT NULL
RETURNING rev;

-- name: TrashNodes :many
-- TrashNodes deletes the project's nodes whose IDs are among ids to its
-- trash, as TrashNode deletes one, but for those that are there already, and
-- returns those that it deleted, with their new revisions, as node.delete_many
-- deletes a selection in one statement.
UPDATE node SET deleted_at = now(), rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]) AND deleted_at IS NULL
RETURNING id, rev;

-- name: RestoreNodes :many
-- RestoreNodes takes the project's nodes whose IDs are among ids out of its
-- trash, as RestoreNode takes one, but for those that are not there, and
-- returns those that it restored, with their new revisions, as
-- node.restore_many restores a selection in one statement.
UPDATE node SET deleted_at = NULL, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[])
    AND deleted_at IS NOT NULL
RETURNING id, rev;

-- name: Node :one
-- Node returns a node of the project that is not in the trash, and no row if
-- the project has no such node, or it is, itself or with its board.
SELECT sqlc.embed(live_node)
FROM live_node
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: BoardNodes :many
-- BoardNodes returns the nodes of the project's board board_id that are not
-- in the trash, in the order of their IDs, after the node after, or from the
-- first if after is null.
SELECT sqlc.embed(live_node)
FROM live_node
WHERE workspace_id = @workspace_id AND project_id = @project_id AND board_id = @board_id
    AND (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: NodeReferrers :many
-- NodeReferrers returns the jumpers of the project that are not in the
-- trash, on any of its boards, that target the node node_id ("referenced
-- by", FR-EDIT-05 AC2), in the order of their IDs, after the jumper after,
-- or from the first if after is null.
SELECT sqlc.embed(live_node)
FROM live_node
WHERE workspace_id = @workspace_id AND project_id = @project_id AND target_node_id = @node_id
    AND (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: BoardReferrers :many
-- BoardReferrers returns the jumpers of the project that are not in the
-- trash, on its boards other than board_id, that target a node of the board
-- board_id that is not deleted itself, which deleting the board leaves
-- dangling (FR-PROJ-02 AC2), in the order of their IDs, after the jumper
-- after, or from the first if after is null. A jumper whose target is deleted
-- already, or purged, dangles already, whatever becomes of the board.
SELECT sqlc.embed(live_node)
FROM live_node
WHERE live_node.workspace_id = @workspace_id AND live_node.project_id = @project_id
    AND live_node.target_board_id = @board_id AND live_node.board_id <> @board_id
    AND (sqlc.narg(after)::uuid IS NULL OR live_node.id > sqlc.narg(after)::uuid)
    AND EXISTS (SELECT 1 FROM node t WHERE t.workspace_id = live_node.workspace_id
        AND t.project_id = live_node.project_id AND t.id = live_node.target_node_id
        AND t.board_id = live_node.target_board_id AND t.deleted_at IS NULL)
ORDER BY live_node.id
LIMIT @max_rows;

-- name: BoardReferrerCount :one
-- BoardReferrerCount returns how many jumpers BoardReferrers returns in all.
SELECT count(*) FROM live_node
WHERE live_node.workspace_id = @workspace_id AND live_node.project_id = @project_id
    AND live_node.target_board_id = @board_id AND live_node.board_id <> @board_id
    AND EXISTS (SELECT 1 FROM node t WHERE t.workspace_id = live_node.workspace_id
        AND t.project_id = live_node.project_id AND t.id = live_node.target_node_id
        AND t.board_id = live_node.target_board_id AND t.deleted_at IS NULL);

-- name: BoardNodeCount :one
-- BoardNodeCount returns how many nodes of the board are not deleted
-- themselves: those that are in the trash with the board while it is there,
-- and come back with it, which the workspace's nodes count while it is not.
SELECT count(*) FROM node
WHERE workspace_id = @workspace_id AND project_id = @project_id AND board_id = @board_id AND deleted_at IS NULL;

-- name: ProjectNodeCount :one
-- ProjectNodeCount returns how many nodes the project has that are not in its
-- trash: what the project adds to its workspace's nodes while it is not in
-- the workspace's trash (package project).
SELECT count(*) FROM live_node
WHERE workspace_id = @workspace_id AND project_id = @project_id;

-- name: ProjectTags :many
-- ProjectTags returns each tag that a node of the project that is not in the
-- trash has, as it is written, with how many such nodes have it, in no
-- order: package node merges the spellings of a tag, and orders what it
-- suggests, itself.
SELECT t.tag::text AS tag, count(*)::bigint AS uses
FROM live_node n, unnest(n.tags) AS t (tag)
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id
GROUP BY t.tag;
