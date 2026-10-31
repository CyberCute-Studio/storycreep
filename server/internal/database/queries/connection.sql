-- Connections (package connection, PRD FR-EDIT-03): the arrows of a
-- project's boards, each from a node of its board to a node of its board.
-- connection is a tenant table, which a transaction of its workspace reads.
-- The commands of a project apply one batch at a time, under the lock of the
-- project's row, so what these queries read of the project's connections
-- stays as it is until the batch changes it. What is not in the project's
-- trash is read through the live view, live_connection, which leaves out the
-- connections of a node, of a branch's output, or of a board in the trash too
-- (package trash).

-- name: InsertConnection :exec
-- InsertConnection adds a connection, from the port out of its node, if
-- from_output_id is null, or else from that output of its node.
INSERT INTO connection (id, workspace_id, project_id, board_id, from_node_id, from_output_id, to_node_id, "order",
    label, created_by, updated_by)
VALUES (@id, @workspace_id, @project_id, @board_id, @from_node_id, sqlc.narg(from_output_id)::uuid, @to_node_id,
    @sort_key, @label, @created_by, @created_by);

-- name: ConnectionAny :one
-- ConnectionAny returns a connection of the project, whether or not it is in
-- the trash, and whether it is in the trash with one of its nodes, the
-- output that it leaves, or its board, which ends_deleted says, with one of
-- its nodes, deleted on their own, which nodes_deleted says, with the output,
-- which output_deleted says, and with its board, which board_deleted says,
-- and no row if the project has no such connection.
SELECT sqlc.embed(connection),
    (f.deleted_at IS NOT NULL OR t.deleted_at IS NOT NULL OR o.deleted_at IS NOT NULL
        OR b.deleted_at IS NOT NULL)::boolean AS ends_deleted,
    (f.deleted_at IS NOT NULL OR t.deleted_at IS NOT NULL)::boolean AS nodes_deleted,
    (o.deleted_at IS NOT NULL)::boolean AS output_deleted,
    (b.deleted_at IS NOT NULL)::boolean AS board_deleted
FROM connection
JOIN node f ON f.workspace_id = connection.workspace_id AND f.project_id = connection.project_id
    AND f.id = connection.from_node_id
JOIN node t ON t.workspace_id = connection.workspace_id AND t.project_id = connection.project_id
    AND t.id = connection.to_node_id
LEFT JOIN branch_output o ON o.workspace_id = connection.workspace_id AND o.project_id = connection.project_id
    AND o.id = connection.from_output_id
JOIN board b ON b.workspace_id = connection.workspace_id AND b.project_id = connection.project_id
    AND b.id = connection.board_id
WHERE connection.workspace_id = @workspace_id AND connection.project_id = @project_id AND connection.id = @id;

-- name: ConnectionsByID :many
-- ConnectionsByID returns the connections of the project whose IDs are among
-- ids, whether or not they are in the trash, each with when the first of its
-- nodes, the output that it leaves, and its board that is in the trash went
-- there, as ends_deleted_at, if one is, which the connection is in the trash
-- with.
SELECT sqlc.embed(connection), LEAST(f.deleted_at, t.deleted_at, o.deleted_at, b.deleted_at)::timestamptz
    AS ends_deleted_at
FROM connection
JOIN node f ON f.workspace_id = connection.workspace_id AND f.project_id = connection.project_id
    AND f.id = connection.from_node_id
JOIN node t ON t.workspace_id = connection.workspace_id AND t.project_id = connection.project_id
    AND t.id = connection.to_node_id
LEFT JOIN branch_output o ON o.workspace_id = connection.workspace_id AND o.project_id = connection.project_id
    AND o.id = connection.from_output_id
JOIN board b ON b.workspace_id = connection.workspace_id AND b.project_id = connection.project_id
    AND b.id = connection.board_id
WHERE connection.workspace_id = @workspace_id AND connection.project_id = @project_id
    AND connection.id = ANY(@ids::uuid[]);

-- name: ConnectionRevs :many
-- ConnectionRevs returns the revisions of the project's connections whose IDs
-- are among ids, those in the trash included, for the pipeline's check of the
-- revisions that a batch expects.
SELECT id, rev FROM connection
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: LastConnectionSortKeys :one
-- LastConnectionSortKeys returns the greatest order of the connections that
-- leave the node from_node_id by its port, the output from_output_id, or
-- out if it is null, but for the connection moved, if it is not null, which
-- moves among them, those in the trash included, so that one restored later
-- keeps its place, as last, or an empty string if there is none, and the
-- last of those that are not in the trash, neither deleted themselves nor
-- with the node that they enter, in their order, by their orders and then
-- their IDs, as last_live, with its ID as last_live_id, or an empty string
-- and null if there is none. The node and the port that they leave are ones
-- that are not in the trash, which a connection is placed after the others
-- of.
WITH c (id, sort_key, deleted) AS (
    SELECT c.id, c."order", c.deleted_at IS NOT NULL OR t.deleted_at IS NOT NULL FROM connection c
    JOIN node t ON t.workspace_id = c.workspace_id AND t.project_id = c.project_id AND t.id = c.to_node_id
    WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.from_node_id = @from_node_id
        AND c.from_output_id IS NOT DISTINCT FROM sqlc.narg(from_output_id)::uuid
        AND c.id IS DISTINCT FROM sqlc.narg(moved)::uuid
), live AS (
    SELECT c.id, c.sort_key FROM c WHERE NOT c.deleted ORDER BY c.sort_key DESC, c.id DESC LIMIT 1
)
SELECT coalesce((SELECT max(c.sort_key) FROM c), '')::text AS last,
    coalesce((SELECT live.sort_key FROM live), '')::text AS last_live,
    (SELECT live.id FROM live)::uuid AS last_live_id;

-- name: UpdateConnectionLabel :one
-- UpdateConnectionLabel sets the connection's label, unless it has the label
-- already, and returns its new revision, or no row if it does.
UPDATE connection SET label = @label, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND label <> @label
RETURNING rev;

-- name: ReconnectConnection :one
-- ReconnectConnection sets the node and the port that the connection
-- leaves, the node that it enters, and its order, unless it has each of them
-- already, and returns its new revision, or no row if it does.
UPDATE connection SET from_node_id = @from_node_id, from_output_id = sqlc.narg(from_output_id)::uuid,
    to_node_id = @to_node_id, "order" = @sort_key, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id
    AND (from_node_id <> @from_node_id OR from_output_id IS DISTINCT FROM sqlc.narg(from_output_id)::uuid
        OR to_node_id <> @to_node_id OR "order" <> @sort_key)
RETURNING rev;

-- name: ReorderConnection :one
-- ReorderConnection sets the connection's order alone, unless it has the
-- order already, and returns its new revision, or no row if it does.
UPDATE connection SET "order" = @sort_key, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND "order" <> @sort_key
RETURNING rev;

-- name: TrashConnection :one
-- TrashConnection deletes the connection to the project's trash, unless it is
-- there already, and returns its new revision, or no row if it is.
UPDATE connection SET deleted_at = now(), rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NULL
RETURNING rev;

-- name: RestoreConnection :one
-- RestoreConnection takes the connection out of the project's trash, unless
-- it is not there, and returns its new revision, or no row if it is not.
UPDATE connection SET deleted_at = NULL, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NOT NULL
RETURNING rev;

-- name: Connection :one
-- Connection returns a connection of the project that is not in the trash,
-- and no row if the project has no such connection, or it is, itself or with
-- one of its nodes or its board.
SELECT sqlc.embed(live_connection)
FROM live_connection
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: BoardConnections :many
-- BoardConnections returns the connections of the project's board board_id
-- that are not in the trash, in the order of their IDs, after the connection
-- after, or from the first if after is null.
SELECT sqlc.embed(live_connection)
FROM live_connection
WHERE workspace_id = @workspace_id AND project_id = @project_id AND board_id = @board_id
    AND (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: UnreconnectedConnections :many
-- UnreconnectedConnections returns the connections, not deleted themselves,
-- of the project's nodes whose IDs are among ids, which are not in the trash
-- themselves, nor with their board, that are in the trash still with the
-- node at their other end, or with the output that they leave, in the order
-- of their IDs, each with what it waits for: the node that it leaves, the
-- output, and the node that it enters, each if it is in the trash, or else
-- null: those that the restoration of the nodes ids could not bring back,
-- which come back once what they wait for does (FR-PROJ-06 AC1). Both of a
-- connection's nodes are of its board, so a node of a connection is in the
-- trash only by its own deletion, and so is an output of a node that is not.
SELECT c.id, (CASE WHEN f.deleted_at IS NOT NULL THEN c.from_node_id END)::uuid AS from_waiting,
    (CASE WHEN o.deleted_at IS NOT NULL THEN c.from_output_id END)::uuid AS output_waiting,
    (CASE WHEN t.deleted_at IS NOT NULL THEN c.to_node_id END)::uuid AS to_waiting
FROM connection c
JOIN node f ON f.workspace_id = c.workspace_id AND f.project_id = c.project_id AND f.id = c.from_node_id
JOIN node t ON t.workspace_id = c.workspace_id AND t.project_id = c.project_id AND t.id = c.to_node_id
LEFT JOIN branch_output o ON o.workspace_id = c.workspace_id AND o.project_id = c.project_id
    AND o.id = c.from_output_id
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.deleted_at IS NULL
    AND (c.from_node_id = ANY(@ids::uuid[]) AND f.deleted_at IS NULL
        OR c.to_node_id = ANY(@ids::uuid[]) AND t.deleted_at IS NULL)
    AND (f.deleted_at IS NOT NULL OR t.deleted_at IS NOT NULL OR o.deleted_at IS NOT NULL)
    AND NOT EXISTS (SELECT 1 FROM board b WHERE b.workspace_id = c.workspace_id AND b.project_id = c.project_id
        AND b.id = c.board_id AND b.deleted_at IS NOT NULL)
ORDER BY c.id;

-- name: BoardUnreconnectedConnections :many
-- BoardUnreconnectedConnections returns the connections, not deleted
-- themselves, of the project's boards whose IDs are among ids, which are not
-- in the trash, of which a node is not in the trash, and which are in the
-- trash still with the node at the other end, or with the output that they
-- leave, by its own deletion, each with what it waits for, as
-- UnreconnectedConnections returns it, in the order of their IDs: those that
-- the restoration of the boards ids could not bring back with the nodes that
-- came back with them, which come back once what they wait for does
-- (FR-PROJ-06 AC1). One whose nodes are both in the trash, as a connection
-- that enters the node that it leaves in the trash is, has neither of them
-- back, and is left out; an output of a node in the trash is in the trash
-- with it, and is waited for only if it is deleted itself.
SELECT c.id, (CASE WHEN f.deleted_at IS NOT NULL THEN c.from_node_id END)::uuid AS from_waiting,
    (CASE WHEN o.deleted_at IS NOT NULL THEN c.from_output_id END)::uuid AS output_waiting,
    (CASE WHEN t.deleted_at IS NOT NULL THEN c.to_node_id END)::uuid AS to_waiting
FROM connection c
JOIN node f ON f.workspace_id = c.workspace_id AND f.project_id = c.project_id AND f.id = c.from_node_id
JOIN node t ON t.workspace_id = c.workspace_id AND t.project_id = c.project_id AND t.id = c.to_node_id
LEFT JOIN branch_output o ON o.workspace_id = c.workspace_id AND o.project_id = c.project_id
    AND o.id = c.from_output_id
JOIN board b ON b.workspace_id = c.workspace_id AND b.project_id = c.project_id AND b.id = c.board_id
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.board_id = ANY(@ids::uuid[])
    AND c.deleted_at IS NULL AND b.deleted_at IS NULL AND (f.deleted_at IS NULL OR t.deleted_at IS NULL)
    AND (f.deleted_at IS NOT NULL OR t.deleted_at IS NOT NULL OR o.deleted_at IS NOT NULL)
ORDER BY c.id;

-- name: OutputUnreconnectedConnections :many
-- OutputUnreconnectedConnections returns the connections, not deleted
-- themselves, that leave the project's outputs whose IDs are among ids,
-- which are not in the trash, themselves or with their nodes or their
-- boards, whose node at the other end is in the trash, each with what it
-- waits for, as UnreconnectedConnections returns it, in the order of their
-- IDs: those that the restoration of the outputs ids could not bring back,
-- which come back once that node does (FR-PROJ-06 AC1).
SELECT c.id, NULL::uuid AS from_waiting, NULL::uuid AS output_waiting, c.to_node_id AS to_waiting
FROM connection c
JOIN live_branch_output o ON o.workspace_id = c.workspace_id AND o.project_id = c.project_id
    AND o.id = c.from_output_id AND o.node_id = c.from_node_id
JOIN node t ON t.workspace_id = c.workspace_id AND t.project_id = c.project_id AND t.id = c.to_node_id
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.from_output_id = ANY(@ids::uuid[])
    AND c.deleted_at IS NULL AND t.deleted_at IS NOT NULL
ORDER BY c.id;

-- name: ConnectionsAmong :many
-- ConnectionsAmong returns the connections of the project that are not in
-- the trash both of whose nodes are among node_ids, in the order of the ports
-- that they leave, and of the choices of each port: by the IDs of the nodes
-- that they leave, then of the outputs, out before them, then by their
-- orders and their IDs.
SELECT sqlc.embed(live_connection)
FROM live_connection
WHERE workspace_id = @workspace_id AND project_id = @project_id
    AND from_node_id = ANY(@node_ids::uuid[]) AND to_node_id = ANY(@node_ids::uuid[])
ORDER BY from_node_id, from_output_id NULLS FIRST, "order", id;
