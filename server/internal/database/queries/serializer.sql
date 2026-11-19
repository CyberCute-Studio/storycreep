-- The reads of the serializer of a project's draft into the published game
-- format (package serializer, PRD §6, plan item 76). The serializer reads the
-- project in one transaction that reads as of one moment, as a snapshot's
-- build does (§7.3), so that its reads agree with one another; each reads
-- every row of its kind of the project that is not in its trash, through
-- the live views, with only what the published format holds of it, and so
-- never who made or changed it, nor when (§6.2). Each reads its rows in the
-- order of their IDs, which the serializer does not rely on: it orders what
-- it writes itself.

-- name: SerializerProject :one
-- SerializerProject returns the project's name, slug, and settings, whose
-- entry the serializer reads, and no row if the workspace has no such
-- project, or it is in the workspace's trash, as queries.Project, whose
-- content the live views then need not leave out.
SELECT name, slug, settings FROM project WHERE workspace_id = @workspace_id AND id = @id AND deleted_at IS NULL;

-- name: SerializerBoards :many
-- SerializerBoards returns the project's boards that are not in its trash,
-- each with its slug, its name, and its entry node, if it has one, in the
-- trash or not.
SELECT id, slug, name, entry_node_id FROM live_board
WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: SerializerNodes :many
-- SerializerNodes returns the project's nodes that are not in its trash,
-- each with what the published format holds of it, its position in
-- hundredths of a unit, as it is stored to 2 decimal places, which a bigint
-- holds exactly.
SELECT id, board_id, type, coalesce(kind, '')::text AS kind, title, text, speaker_component_id, cover_asset_id,
    tags, (x * 100)::bigint AS x, (y * 100)::bigint AS y, is_ending, target_board_id, target_node_id
FROM live_node WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: SerializerOutputs :many
-- SerializerOutputs returns the project's branch outputs that are not in its
-- trash, each with its branch, its kind, and a conditional output's order.
SELECT id, node_id, kind, coalesce("order", '')::text AS sort_key FROM live_branch_output
WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: SerializerConnections :many
-- SerializerConnections returns the project's connections that are not in
-- its trash, each with its board, its ends, the output that it leaves, if it
-- leaves a branch, its order among its port's connections, and its label.
SELECT id, board_id, from_node_id, from_output_id, to_node_id, "order" AS sort_key, label FROM live_connection
WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: SerializerExpressions :many
-- SerializerExpressions returns the expressions of the project's nodes,
-- connections, and branch outputs that are not in its trash, each with its
-- owner and its field, its source, and its AST if it compiles, or the code of
-- its error if it does not; the serializer passes over those of owners that
-- it did not read all the same.
SELECT e.node_id, e.connection_id, e.branch_output_id, e.field, e.source, e.ast,
    coalesce(e.error ->> 'code', '')::text AS error_code
FROM expression e
WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id
    AND (EXISTS (SELECT 1 FROM live_node n WHERE n.workspace_id = e.workspace_id AND n.id = e.node_id)
        OR EXISTS (SELECT 1 FROM live_connection c WHERE c.workspace_id = e.workspace_id AND c.id = e.connection_id)
        OR EXISTS (SELECT 1 FROM live_branch_output o WHERE o.workspace_id = e.workspace_id
            AND o.id = e.branch_output_id))
ORDER BY e.id;

-- name: SerializerVariables :many
-- SerializerVariables returns the project's variables that are not in its
-- trash, each with its declaration.
SELECT id, key, scope, board_id, type, default_value, description, reset_on_enter FROM live_variable
WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: SerializerComponentTypes :many
-- SerializerComponentTypes returns the project's component types that are
-- not in their trash, each with its key and its name.
SELECT id, key, name FROM live_component_type
WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: SerializerAttributeDefs :many
-- SerializerAttributeDefs returns the project's attribute definitions that
-- are not in its trash, each with its type's ID, its order among its type's,
-- and its declaration.
SELECT id, component_type_id, "order" AS sort_key, key, name, type, enum_options, default_value, is_runtime
FROM live_attribute_def WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: SerializerComponents :many
-- SerializerComponents returns the project's components that are not in its
-- trash, each with its type, its key, its name, and its image. Its
-- description is for its authors, and the published format does not hold it.
SELECT id, component_type_id, key, name, image_asset_id FROM live_component
WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: SerializerAttributeValues :many
-- SerializerAttributeValues returns the values of the attributes of the
-- project's components that are not in its trash, each with its component
-- and its attribute definition.
SELECT v.component_id, v.attribute_def_id, v.value FROM attribute_value v
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id
    AND EXISTS (SELECT 1 FROM live_component c WHERE c.workspace_id = v.workspace_id
        AND c.project_id = v.project_id AND c.id = v.component_id)
ORDER BY v.component_id, v.attribute_def_id;
