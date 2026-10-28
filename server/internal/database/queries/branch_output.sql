-- Branch outputs (package node, PRD FR-EDIT-04, plan note N51): the outputs
-- of a project's branches, each a conditional output or a branch's else.
-- branch_output is a tenant table, which a transaction of its workspace
-- reads. The commands of a project apply one batch at a time, under the lock
-- of the project's row, so what these queries read of the project's outputs
-- stays as it is until the batch changes it. What is not in the project's
-- trash is read through the live view, live_branch_output, which leaves out
-- the outputs of a node or a board in the trash too (package trash).

-- name: InsertBranchOutput :exec
-- InsertBranchOutput adds an output of a branch: a conditional one, of the
-- kind and the order given, or its else, without an order.
INSERT INTO branch_output (id, workspace_id, project_id, board_id, node_id, kind, "order", created_by, updated_by)
VALUES (@id, @workspace_id, @project_id, @board_id, @node_id, @kind, sqlc.narg(sort_key)::text, @created_by,
    @created_by);

-- name: BranchOutputAny :one
-- BranchOutputAny returns an output of the project, whether or not it is in
-- the trash, and whether it is in the trash with its node or its board,
-- which node_deleted says, and with its board, which board_deleted says, and
-- no row if the project has no such output.
SELECT sqlc.embed(branch_output),
    (n.deleted_at IS NOT NULL OR b.deleted_at IS NOT NULL)::boolean AS node_deleted,
    (b.deleted_at IS NOT NULL)::boolean AS board_deleted
FROM branch_output
JOIN node n ON n.workspace_id = branch_output.workspace_id AND n.project_id = branch_output.project_id
    AND n.id = branch_output.node_id
JOIN board b ON b.workspace_id = branch_output.workspace_id AND b.project_id = branch_output.project_id
    AND b.id = branch_output.board_id
WHERE branch_output.workspace_id = @workspace_id AND branch_output.project_id = @project_id
    AND branch_output.id = @id;

-- name: BranchOutputsByID :many
-- BranchOutputsByID returns the outputs of the project whose IDs are among
-- ids, whether or not they are in the trash, each with when the first of its
-- node and its board that is in the trash went there, as ends_deleted_at, if
-- one is, which the output is in the trash with.
SELECT sqlc.embed(branch_output), LEAST(n.deleted_at, b.deleted_at)::timestamptz AS ends_deleted_at
FROM branch_output
JOIN node n ON n.workspace_id = branch_output.workspace_id AND n.project_id = branch_output.project_id
    AND n.id = branch_output.node_id
JOIN board b ON b.workspace_id = branch_output.workspace_id AND b.project_id = branch_output.project_id
    AND b.id = branch_output.board_id
WHERE branch_output.workspace_id = @workspace_id AND branch_output.project_id = @project_id
    AND branch_output.id = ANY(@ids::uuid[]);

-- name: BranchOutputRevs :many
-- BranchOutputRevs returns the revisions of the project's outputs whose IDs
-- are among ids, those in the trash included, for the pipeline's check of
-- the revisions that a batch expects.
SELECT id, rev FROM branch_output
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: NodeOutputs :many
-- NodeOutputs returns the outputs that are not in the trash of the project's
-- nodes whose IDs are among node_ids, by their nodes, each node's in their
-- order: its conditional outputs by their orders and then their IDs, and its
-- else after them.
SELECT * FROM live_branch_output
WHERE workspace_id = @workspace_id AND project_id = @project_id AND node_id = ANY(@node_ids::uuid[])
ORDER BY node_id, kind = 'else', "order", id;

-- name: NodeOutputCounts :one
-- NodeOutputCounts returns how many conditional outputs the project's node
-- node_id has that are not in the trash, and the ID of its else that is not,
-- or null if it has none.
SELECT count(*) FILTER (WHERE kind <> 'else')::bigint AS conditional,
    (array_agg(id) FILTER (WHERE kind = 'else'))[1]::uuid AS else_id
FROM live_branch_output
WHERE workspace_id = @workspace_id AND project_id = @project_id AND node_id = @node_id;

-- name: LastBranchOutputSortKeys :one
-- LastBranchOutputSortKeys returns the greatest order of the conditional
-- outputs of the node node_id, but for the output moved, if it is not null,
-- which moves among them, those in the trash included, so that one restored
-- later keeps its place, as last, or an empty string if there is none, and
-- the last of those that are not in the trash, in their order, by their
-- orders and then their IDs, as last_live, with its ID as last_live_id, or
-- an empty string and null if there is none. The node is one that is not in
-- the trash, which an output is placed after the others of.
WITH o (id, sort_key, deleted) AS (
    SELECT o.id, o."order", o.deleted_at IS NOT NULL FROM branch_output o
    WHERE o.workspace_id = @workspace_id AND o.project_id = @project_id AND o.node_id = @node_id
        AND o.kind <> 'else' AND o.id IS DISTINCT FROM sqlc.narg(moved)::uuid
), live AS (
    SELECT o.id, o.sort_key FROM o WHERE NOT o.deleted ORDER BY o.sort_key DESC, o.id DESC LIMIT 1
)
SELECT coalesce((SELECT max(o.sort_key) FROM o), '')::text AS last,
    coalesce((SELECT live.sort_key FROM live), '')::text AS last_live,
    (SELECT live.id FROM live)::uuid AS last_live_id;

-- name: ReorderBranchOutput :one
-- ReorderBranchOutput sets the output's order alone, unless it has the order
-- already, and returns its new revision, or no row if it does.
UPDATE branch_output SET "order" = @sort_key, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND "order" <> @sort_key
RETURNING rev;

-- name: RekindBranchOutputs :many
-- RekindBranchOutputs gives each conditional output of the project's node
-- node_id that is not in the trash the kind of its place: the first, by
-- their orders and then their IDs, its if, and the others its elseifs,
-- unless it has it already, and returns those that it changed, with their
-- new revisions.
WITH placed AS (
    SELECT o.id, (CASE WHEN row_number() OVER (ORDER BY o."order", o.id) = 1 THEN 'if' ELSE 'elseif' END)::text
        AS kind
    FROM live_branch_output o
    WHERE o.workspace_id = @workspace_id AND o.project_id = @project_id AND o.node_id = @node_id
        AND o.kind <> 'else'
)
UPDATE branch_output b SET kind = placed.kind, rev = b.rev + 1, updated_at = now(), updated_by = @updated_by
FROM placed
WHERE b.workspace_id = @workspace_id AND b.project_id = @project_id AND b.id = placed.id AND b.kind <> placed.kind
RETURNING b.id, b.rev;

-- name: TrashBranchOutput :one
-- TrashBranchOutput deletes the output to the project's trash, unless it is
-- there already, and returns its new revision, or no row if it is.
UPDATE branch_output SET deleted_at = now(), rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NULL
RETURNING rev;

-- name: RestoreBranchOutput :one
-- RestoreBranchOutput takes the output out of the project's trash, unless it
-- is not there, and returns its new revision, or no row if it is not.
UPDATE branch_output SET deleted_at = NULL, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NOT NULL
RETURNING rev;

-- name: OutputConnections :many
-- OutputConnections returns at most max_rows of the connections that leave
-- the project's output output_id of the node node_id that are not in the
-- trash, in the order of their IDs, each with the node that it enters.
SELECT id, to_node_id FROM live_connection
WHERE workspace_id = @workspace_id AND project_id = @project_id AND from_node_id = @node_id
    AND from_output_id = @output_id
ORDER BY id
LIMIT @max_rows;

-- name: OutputConnectionCount :one
-- OutputConnectionCount returns how many connections that are not in the
-- trash leave the project's output output_id of the node node_id.
SELECT count(*) FROM live_connection
WHERE workspace_id = @workspace_id AND project_id = @project_id AND from_node_id = @node_id
    AND from_output_id = @output_id;
