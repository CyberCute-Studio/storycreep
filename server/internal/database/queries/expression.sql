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
-- it changed: 1, or 0 if it has. Its names (expression_name) stay as they
-- are until the batch compiles it (CompileExpressions,
-- IndexExpressionNames).
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
-- yet to compile, those that name a UUID among refs or a key among keys
-- (expression_name), those whose IDs are among ids
-- (BoardExpressionsToCompile), and, if names is true, every one that does not
-- compile but names something, which a change of what the project's
-- expressions can name may let compile, each once, in the order of their IDs,
-- each with what it is compiled from alone: its source, read in its context,
-- at its board, and whether the board is in the trash, whose expressions see
-- what it holds that is there with it alone (TrashedBoardVariables,
-- ExpressionNodes). Each of those is read by an index of its own, and the
-- expressions by their IDs, so that the read takes as long as what it
-- returns, rather than as the project's expressions, however many UUIDs and
-- keys the batch changed, as a board's deletion changes 2,000 nodes.
WITH found AS (
    SELECT id FROM expression
    WHERE workspace_id = @workspace_id AND project_id = @project_id AND ast IS NULL AND error IS NULL
    UNION
    SELECT expression_id FROM expression_name
    WHERE workspace_id = @workspace_id AND project_id = @project_id AND ref = ANY(@refs::uuid[])
    UNION
    SELECT expression_id FROM expression_name
    WHERE workspace_id = @workspace_id AND project_id = @project_id AND key = ANY(@keys::text[])
    UNION
    SELECT unnest(@ids::uuid[])
    UNION
    SELECT id FROM expression
    WHERE @names::boolean AND workspace_id = @workspace_id AND project_id = @project_id AND NOT is_valid
        AND (cardinality(refs) > 0 OR cardinality(keys) > 0)
)
SELECT e.id, e.board_id, e.context, e.source, EXISTS (SELECT 1 FROM board b WHERE b.workspace_id = e.workspace_id
        AND b.project_id = e.project_id AND b.id = e.board_id AND b.deleted_at IS NOT NULL)::boolean AS board_deleted
FROM found f
JOIN expression e ON e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.id = f.id
ORDER BY e.id;

-- name: BoardNames :many
-- BoardNames returns what the deletion of the project's boards board_ids to
-- the trash, or their restoration, changes of what the other boards'
-- expressions can name, each with its board: the UUIDs of their nodes, which
-- have no key, and the keys of their variables, which no other board's
-- expression resolves, that are not deleted to the trash themselves, which a
-- board's deletion takes out of it, and its restoration brings back.
SELECT n.board_id, n.id, NULL::text AS key FROM node n
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.board_id = ANY(@board_ids::uuid[])
    AND n.deleted_at IS NULL
UNION ALL
SELECT v.board_id, v.id, v.key::text FROM variable v
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id AND v.board_id = ANY(@board_ids::uuid[])
    AND v.deleted_at IS NULL;

-- name: BoardExpressionsToCompile :many
-- BoardExpressionsToCompile returns the names of the project's expressions
-- (expression_name) that are among refs, the nodes of the boards board_ids,
-- and those of the expressions that do not compile that are among keys, the
-- keys of the boards' variables (BoardNames), as one that compiles names its
-- own board's variables and the global ones alone, which no board's deletion
-- or restoration changes: each with its expression and the expression's
-- board, by which package expression keeps the expressions that name what a
-- board other than their own holds. A board's own expressions compile as
-- they did, as one of a board in the trash sees what the board holds that is
-- there with it alone, so the names of those of the boards are returned only
-- when there are several, whose expressions may name what another holds. The
-- names of the nodes are read by the index of their UUIDs alone, however many
-- the boards hold, and those of the keys by the expressions that do not
-- compile (expression_invalid), which name something.
SELECT n.expression_id, n.board_id, n.ref, n.key FROM expression_name n
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.ref = ANY(@refs::uuid[])
    AND (n.board_id <> ALL(@board_ids::uuid[]) OR cardinality(@board_ids::uuid[]) > 1)
UNION ALL
SELECT n.expression_id, n.board_id, n.ref, n.key FROM expression e
JOIN expression_name n ON n.workspace_id = e.workspace_id AND n.project_id = e.project_id AND n.expression_id = e.id
WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id AND NOT e.is_valid
    AND (cardinality(e.refs) > 0 OR cardinality(e.keys) > 0) AND n.key = ANY(@keys::text[])
    AND (n.board_id <> ALL(@board_ids::uuid[]) OR cardinality(@board_ids::uuid[]) > 1);

-- name: CompileExpressions :many
-- CompileExpressions sets what compiling the source of each of the project's
-- expressions that rows lists gives, a JSON array of objects of its id, its
-- ast and ast_version, or error, and its refs and keys, unless the
-- expression has it already, and returns each that it changed, with its
-- owner, whether the change is one that the API shows, of its AST, its
-- version, or its error, as shown, and whether its names (expression_name)
-- are to be written again, as renamed: those of an expression whose refs or
-- keys it changes, and of one that had yet to compile, whose source a
-- command wrote, which emptied its refs and its keys but kept its names. It
-- deletes those names, every one of each such expression, which
-- IndexExpressionNames then writes as its refs and keys are, by an index of
-- their expressions' IDs alone, rather than compare them with what the
-- expressions name, which would join them with what the batch compiled,
-- whose size the planner cannot tell. Every such expression is changed: one
-- that had yet to compile has neither an AST nor an error, which compiling
-- it gives.
WITH c AS (
    SELECT (r.value->>'id')::uuid AS id, NULLIF(r.value->'ast', 'null'::jsonb) AS ast,
        (r.value->>'ast_version')::integer AS ast_version, NULLIF(r.value->'error', 'null'::jsonb) AS error,
        ARRAY(SELECT jsonb_array_elements_text(r.value->'refs'))::uuid[] AS refs,
        ARRAY(SELECT jsonb_array_elements_text(r.value->'keys'))::text[] AS keys
    FROM jsonb_array_elements(@rows::jsonb) AS r
), old AS (
    SELECT e.id, (e.ast IS DISTINCT FROM c.ast OR e.ast_version IS DISTINCT FROM c.ast_version
            OR e.error IS DISTINCT FROM c.error)::boolean AS shown,
        (e.refs <> c.refs OR e.keys <> c.keys OR e.ast IS NULL AND e.error IS NULL)::boolean AS renamed
    FROM expression e JOIN c ON c.id = e.id
    WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id
), gone AS (
    DELETE FROM expression_name
    WHERE workspace_id = @workspace_id AND project_id = @project_id
        AND expression_id = ANY(ARRAY(SELECT id FROM old WHERE renamed))
)
UPDATE expression e SET ast = c.ast, ast_version = c.ast_version, is_valid = c.ast IS NOT NULL, error = c.error,
    refs = c.refs, keys = c.keys, updated_at = now()
FROM c JOIN old ON old.id = c.id
WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.id = c.id AND (old.shown OR old.renamed)
RETURNING e.id, e.node_id, e.connection_id, e.branch_output_id, old.shown, old.renamed;

-- name: IndexExpressionNames :exec
-- IndexExpressionNames writes the names (expression_name) of the project's
-- expressions whose IDs are among ids, whose names CompileExpressions has
-- just deleted, as their refs and keys are: a row of each UUID and of each
-- key, with the expression's board. So an expression's names are always
-- those of its refs and its keys once its batch has compiled it.
INSERT INTO expression_name (workspace_id, project_id, expression_id, board_id, ref, key)
SELECT e.workspace_id, e.project_id, e.id, e.board_id, r.ref, NULL FROM expression e, unnest(e.refs) AS r (ref)
WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.id = ANY(@ids::uuid[])
UNION ALL
SELECT e.workspace_id, e.project_id, e.id, e.board_id, NULL, k.key FROM expression e, unnest(e.keys) AS k (key)
WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.id = ANY(@ids::uuid[]);

-- name: ChangedNames :many
-- ChangedNames returns the UUIDs and the keys of what a change of the
-- project's variables variable_ids, components component_ids, attribute
-- definitions attribute_ids, and component types type_ids may change of what
-- the project's expressions name: each of those variables, components, and
-- definitions, and the definitions and the components of those types, in the
-- trash or not. What a board's deletion or restoration changes of it is
-- BoardNames'.
SELECT v.id, v.key::text AS key FROM variable v
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id AND v.id = ANY(@variable_ids::uuid[])
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

-- name: TrashedBoardVariables :many
-- TrashedBoardVariables returns the variables of the project's boards
-- board_ids, which are in its trash, that are there with their boards alone,
-- not deleted themselves, which the expressions of their own boards alone
-- can name, in the order of their IDs.
SELECT id, key, type, board_id FROM variable
WHERE workspace_id = @workspace_id AND project_id = @project_id AND board_id = ANY(@board_ids::uuid[])
    AND deleted_at IS NULL
ORDER BY id;

-- name: ExpressionNodes :many
-- ExpressionNodes returns those of the project's nodes whose IDs are among
-- ids that are not in its trash, which a node reference can name, and those
-- of its boards board_ids, which are in its trash, that are there with their
-- boards alone, not deleted themselves, which an expression of their own
-- board alone can name, each with its type, its board, and whether its board
-- is in the trash. It reads the nodes of board_ids only when there are any,
-- as there are only when the batch compiles an expression of a board in the
-- trash.
SELECT l.id, l.type, l.board_id, false AS board_deleted FROM live_node l
WHERE l.workspace_id = @workspace_id AND l.project_id = @project_id AND l.id = ANY(@ids::uuid[])
UNION ALL
SELECT n.id, n.type, n.board_id, true FROM node n
WHERE cardinality(@board_ids::uuid[]) > 0 AND n.workspace_id = @workspace_id AND n.project_id = @project_id
    AND n.id = ANY(@ids::uuid[]) AND n.board_id = ANY(@board_ids::uuid[]) AND n.deleted_at IS NULL;

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

-- name: NamingExpressions :many
-- NamingExpressions returns the project's expressions, in the trash or not,
-- that may name what a rename changes the key of, or what a deletion takes
-- away (package expression): those that name a UUID among refs or a key
-- among keys (expression_name), those that the batch has yet to compile,
-- whose names a command's new source may hold, of a source that holds one of
-- keys, as any source that names one does, and those among ids, which the undo
-- of a rename lists, each once; of the board board_id alone, unless it is
-- null. Each comes with what it is compiled from, its owner and field, the
-- node of its site, as a usage lists it, and whether its board is in the
-- trash, and whether its owner is outside the trash, as a usage list shows it
-- (live), in the order of their boards, their sites' nodes, and their IDs,
-- after the key after_board, after_node, after_id, which is all zero UUIDs
-- to read from the first, as no expression's is, and at most max_rows of
-- them, or every one if it is null, so that a page of a rename's preview
-- reads the sources of its own expressions alone. The names are read by an
-- index of each, so that the read takes as long as the expressions that name
-- what it looks for, which it orders before it returns those after the key,
-- however few, and the sources that the batch has yet to compile are matched
-- to the keys before anything compiles them.
WITH found AS (
    SELECT id FROM expression
    WHERE workspace_id = @workspace_id AND project_id = @project_id AND ast IS NULL AND error IS NULL
        AND EXISTS (SELECT 1 FROM unnest(@keys::text[]) AS k (needle) WHERE strpos(source, k.needle) > 0)
    UNION
    SELECT id FROM expression
    WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[])
    UNION
    SELECT expression_id FROM expression_name
    WHERE workspace_id = @workspace_id AND project_id = @project_id AND ref = ANY(@refs::uuid[])
    UNION
    SELECT expression_id FROM expression_name
    WHERE workspace_id = @workspace_id AND project_id = @project_id AND key = ANY(@keys::text[])
)
SELECT e.id, e.board_id, e.field, e.context, e.source, e.node_id, e.connection_id, e.branch_output_id,
    coalesce(e.node_id, c.from_node_id, o.node_id)::uuid AS site_node_id,
    (b.deleted_at IS NOT NULL)::boolean AS board_deleted,
    (coalesce(ln.id, lc.id, lo.id) IS NOT NULL)::boolean AS live
FROM found f
JOIN expression e ON e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.id = f.id
JOIN board b ON b.workspace_id = e.workspace_id AND b.project_id = e.project_id AND b.id = e.board_id
LEFT JOIN connection c ON c.workspace_id = e.workspace_id AND c.project_id = e.project_id AND c.id = e.connection_id
LEFT JOIN branch_output o ON o.workspace_id = e.workspace_id AND o.project_id = e.project_id
    AND o.id = e.branch_output_id
LEFT JOIN live_node ln ON ln.workspace_id = e.workspace_id AND ln.project_id = e.project_id AND ln.id = e.node_id
LEFT JOIN live_connection lc ON lc.workspace_id = e.workspace_id AND lc.project_id = e.project_id
    AND lc.id = e.connection_id
LEFT JOIN live_branch_output lo ON lo.workspace_id = e.workspace_id AND lo.project_id = e.project_id
    AND lo.id = e.branch_output_id
WHERE (sqlc.narg(board_id)::uuid IS NULL OR e.board_id = sqlc.narg(board_id)::uuid)
    AND (e.board_id, coalesce(e.node_id, c.from_node_id, o.node_id), e.id)
        > (@after_board::uuid, @after_node::uuid, @after_id::uuid)
ORDER BY e.board_id, site_node_id, e.id
LIMIT sqlc.narg(max_rows)::bigint;

-- name: SetExpressionSources :many
-- SetExpressionSources sets the source of each of the project's expressions
-- that rows lists, a JSON array of objects of its id and its source, which
-- the batch has yet to compile, unless it has the source already, as
-- SetExpressionSource does one, and returns those that it changed, with
-- their owners.
UPDATE expression e SET source = s.source, ast = NULL, ast_version = NULL, is_valid = false, error = NULL,
    refs = '{}', keys = '{}', updated_at = now()
FROM (SELECT (r.value->>'id')::uuid AS id, r.value->>'source' AS source FROM jsonb_array_elements(@rows::jsonb) AS r)
    AS s
WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.id = s.id AND e.source <> s.source
RETURNING e.id, e.node_id, e.connection_id, e.branch_output_id;

-- name: TrashedTypeComponents :many
-- TrashedTypeComponents returns the project's components in its trash whose
-- keys a component of the type component_type_id in its trash has, those of
-- other types among them, with their types, in the order of their IDs: those
-- whose attributes' names a change of a key of one of the type's definitions
-- rewrites (package expression), of keys that no other component has.
SELECT id, key, component_type_id FROM component c
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.deleted_at IS NOT NULL
    AND c.key IN (
        SELECT t.key FROM component t
        WHERE t.workspace_id = @workspace_id AND t.project_id = @project_id
            AND t.component_type_id = @component_type_id AND t.deleted_at IS NOT NULL
    )
ORDER BY c.id;

-- name: ExpressionTypeAttributes :many
-- ExpressionTypeAttributes returns the attribute definitions of the project's
-- component type component_type_id that are not deleted themselves, whether
-- or not the type is in the trash, which a component of the type would have
-- once it and the type are outside it, in their order.
SELECT id, key, type, is_runtime, enum_options FROM attribute_def
WHERE workspace_id = @workspace_id AND project_id = @project_id AND component_type_id = @component_type_id
    AND deleted_at IS NULL
ORDER BY "order", id;
