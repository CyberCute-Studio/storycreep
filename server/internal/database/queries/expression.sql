-- Expressions (package expression, PRD §5.3, §5.8, plan note N51): the
-- conditions and the scripts of a project's nodes, connections, and branch
-- outputs, each of one owner's field. expression is a tenant table, which a
-- transaction of its workspace reads. A command writes an expression's
-- source alone, and the pipeline compiles it once every command of the batch
-- has applied (CompileExpressions), under the lock of the project's row, so
-- what these queries read stays as it is until the batch changes it.

-- name: OwnerExpressions :many
-- OwnerExpressions returns the expressions of the project's nodes whose IDs
-- are among node_ids, connections among connection_ids, and outputs among
-- output_ids, in the trash or not, in the order of their owners and fields.
SELECT * FROM expression
WHERE workspace_id = @workspace_id AND project_id = @project_id
    AND (node_id = ANY(@node_ids::uuid[]) OR connection_id = ANY(@connection_ids::uuid[])
        OR branch_output_id = ANY(@output_ids::uuid[]))
ORDER BY node_id, connection_id, branch_output_id, field;

-- name: InsertExpression :exec
-- InsertExpression adds the expression of an owner's field, of the source,
-- which the batch has yet to compile.
INSERT INTO expression (workspace_id, project_id, board_id, node_id, connection_id, branch_output_id, field, context,
    source)
VALUES (@workspace_id, @project_id, @board_id, sqlc.narg(node_id)::uuid, sqlc.narg(connection_id)::uuid,
    sqlc.narg(branch_output_id)::uuid, @field, @context, @source);

-- name: SetExpressionSource :execrows
-- SetExpressionSource sets the source of the expression id, which the batch
-- has yet to compile, unless it has the source already, and returns how many
-- it changed: 1, or 0 if it has.
UPDATE expression SET source = @source, ast = NULL, ast_version = NULL, is_valid = false, error = NULL, refs = '{}',
    keys = '{}', updated_at = now()
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND source <> @source;

-- name: DeleteExpression :execrows
-- DeleteExpression deletes the expression id, which its owner's field no
-- longer has, and returns how many it deleted.
DELETE FROM expression WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: CopyNodeExpressions :execrows
-- CopyNodeExpressions gives the node node_id, a copy of the node source_id,
-- the expressions of source_id, each of the same source, which the batch has
-- yet to compile, and returns how many it gave it.
INSERT INTO expression (workspace_id, project_id, board_id, node_id, field, context, source)
SELECT s.workspace_id, s.project_id, s.board_id, @node_id::uuid, s.field, s.context, s.source
FROM expression s
WHERE s.workspace_id = @workspace_id AND s.project_id = @project_id AND s.node_id = @source_id;

-- name: ExpressionsToCompile :many
-- ExpressionsToCompile returns the project's expressions that the batch has
-- yet to compile, those whose refs or keys are among refs or keys, and, if
-- names is true, every one that does not compile but names something, which
-- a change of what the project's expressions can name may let compile, in
-- the order of their IDs, each with what it is compiled from alone: its
-- source, read in its context, at its board.
SELECT id, board_id, context, source FROM expression
WHERE workspace_id = @workspace_id AND project_id = @project_id
    AND (ast IS NULL AND error IS NULL OR refs && @refs::uuid[] OR keys && @keys::text[]
        OR @names::boolean AND NOT is_valid AND (cardinality(refs) > 0 OR cardinality(keys) > 0))
ORDER BY id;

-- name: CompileExpressions :many
-- CompileExpressions sets what compiling the source of each of the project's
-- expressions that rows lists gives, a JSON array of objects of its id, its
-- ast and ast_version, or error, and its refs and keys, unless the
-- expression has it already, and returns the owner of each that it changed,
-- with whether the change is one that the API shows, of its AST, its
-- version, or its error, as shown.
WITH c AS (
    SELECT (r.value->>'id')::uuid AS id, NULLIF(r.value->'ast', 'null'::jsonb) AS ast,
        (r.value->>'ast_version')::integer AS ast_version, NULLIF(r.value->'error', 'null'::jsonb) AS error,
        ARRAY(SELECT jsonb_array_elements_text(r.value->'refs'))::uuid[] AS refs,
        ARRAY(SELECT jsonb_array_elements_text(r.value->'keys'))::text[] AS keys
    FROM jsonb_array_elements(@rows::jsonb) AS r
), old AS (
    SELECT e.id, (e.ast IS DISTINCT FROM c.ast OR e.ast_version IS DISTINCT FROM c.ast_version
        OR e.error IS DISTINCT FROM c.error)::boolean AS shown
    FROM expression e JOIN c ON c.id = e.id
    WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id
)
UPDATE expression e SET ast = c.ast, ast_version = c.ast_version, is_valid = c.ast IS NOT NULL, error = c.error,
    refs = c.refs, keys = c.keys, updated_at = now()
FROM c JOIN old ON old.id = c.id
WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.id = c.id
    AND (old.shown OR e.refs <> c.refs OR e.keys <> c.keys)
RETURNING e.node_id, e.connection_id, e.branch_output_id, old.shown;

-- name: ChangedNames :many
-- ChangedNames returns the UUIDs, and the keys, of what a change of the
-- project's variables variable_ids, components component_ids, attribute
-- definitions attribute_ids, component types type_ids, and boards board_ids
-- may change of what the project's expressions name: each of those
-- variables, components, and definitions, the definitions and the components
-- of those types, and the variables and the nodes, which have no key, of
-- those boards, in the trash or not.
SELECT n.id, NULL::text AS key FROM node n
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.board_id = ANY(@board_ids::uuid[])
UNION ALL
SELECT v.id, v.key::text FROM variable v
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id
    AND (v.id = ANY(@variable_ids::uuid[]) OR v.board_id = ANY(@board_ids::uuid[]))
UNION ALL
SELECT c.id, c.key::text FROM component c
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id
    AND (c.id = ANY(@component_ids::uuid[]) OR c.component_type_id = ANY(@type_ids::uuid[]))
UNION ALL
SELECT a.id, a.key::text FROM attribute_def a
WHERE a.workspace_id = @workspace_id AND a.project_id = @project_id
    AND (a.id = ANY(@attribute_ids::uuid[]) OR a.component_type_id = ANY(@type_ids::uuid[]));

-- name: ExpressionVariables :many
-- ExpressionVariables returns the project's variables that are not in its
-- trash, which its expressions can name, in the order of their IDs.
SELECT id, key, type, board_id FROM live_variable
WHERE workspace_id = @workspace_id AND project_id = @project_id
ORDER BY id;

-- name: ExpressionComponents :many
-- ExpressionComponents returns the project's components that are not in its
-- trash, whose runtime attributes its expressions can read and write, in the
-- order of their IDs.
SELECT id, key, component_type_id FROM live_component
WHERE workspace_id = @workspace_id AND project_id = @project_id
ORDER BY id;

-- name: ExpressionAttributes :many
-- ExpressionAttributes returns the attribute definitions of the project's
-- component types that are not in its trash, by their types, each type's in
-- their order.
SELECT id, component_type_id, key, type, is_runtime, enum_options FROM live_attribute_def
WHERE workspace_id = @workspace_id AND project_id = @project_id
ORDER BY component_type_id, "order", id;

-- name: ExpressionNodes :many
-- ExpressionNodes returns those of the project's nodes whose IDs are among
-- ids that are not in its trash, which a node reference can name, each with
-- its type.
SELECT id, type FROM live_node
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: TouchNodes :many
-- TouchNodes increments the revisions of the project's nodes whose IDs are
-- among ids, whose expressions changed, but for those deleted to the trash
-- themselves, which nothing changes meanwhile, so that the undo of their
-- deletion, which expects the revision that it left, still applies, and
-- which their restoration gives a new revision; one that is in the trash
-- only with its board is changed, as no deletion of its own expects its
-- revision, and its board's restoration gives it none. It returns those
-- that it changed.
UPDATE node SET rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]) AND deleted_at IS NULL
RETURNING id, rev;

-- name: TouchConnections :many
-- TouchConnections increments the revisions of the project's connections
-- whose IDs are among ids, whose expressions changed, but for those deleted
-- to the trash themselves, as TouchNodes does, and returns those it changed.
UPDATE connection SET rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]) AND deleted_at IS NULL
RETURNING id, rev;

-- name: TouchBranchOutputs :many
-- TouchBranchOutputs increments the revisions of the project's branch
-- outputs whose IDs are among ids, whose expressions changed, but for those
-- deleted to the trash themselves, as TouchNodes does, and returns those it
-- changed.
UPDATE branch_output SET rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]) AND deleted_at IS NULL
RETURNING id, rev;
