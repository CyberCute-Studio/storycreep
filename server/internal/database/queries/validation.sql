-- The reads of the validation of a project's content, and the writes of its
-- mutes (package validation, PRD FR-DEP-03, plan note N55). The validation
-- reads the project in one transaction that reads as of one moment, so that
-- its reads agree with one another; each reads every row of its kind of the
-- project, and the validation passes over what is in the trash by the IDs of
-- what is not, which the reads of the live views give.

-- name: ValidationProject :one
-- ValidationProject returns the project's operation sequence, which the
-- report names as the state of the draft that it validated, and its
-- settings, whose entry the validation checks.
SELECT seq, settings FROM project WHERE workspace_id = @workspace_id AND id = @id;

-- name: ValidationBoards :many
-- ValidationBoards returns the project's boards that are not in its trash,
-- each with its entry node, if it has one, in the trash or not.
SELECT id, entry_node_id FROM live_board
WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: ValidationNodes :many
-- ValidationNodes returns the project's nodes that are not in its trash,
-- each with what the rules read of it, an element's text only as whether it
-- is empty: one empty paragraph, as the editor's empty document is, which
-- has no segments (package richtext).
SELECT id, board_id, type, coalesce(kind, '')::text AS kind, speaker_component_id, is_ending, target_node_id,
    coalesce(text = '{"type": "doc", "content": [{"type": "paragraph"}]}'::jsonb, false)::boolean AS text_empty
FROM live_node WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: ValidationOutputs :many
-- ValidationOutputs returns the project's branch outputs that are not in its
-- trash, each with its branch and its board.
SELECT id, node_id, board_id FROM live_branch_output
WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: ValidationConnections :many
-- ValidationConnections returns the project's connections that are not in
-- its trash, each with its ends, the port that it leaves, and its label, in
-- the order of their ports, and of each port's connections among themselves.
SELECT id, board_id, from_node_id, from_output_id, to_node_id, label FROM live_connection
WHERE workspace_id = @workspace_id AND project_id = @project_id
ORDER BY from_node_id, from_output_id NULLS FIRST, "order", id;

-- name: ValidationExpressions :many
-- ValidationExpressions returns the project's expressions, in the trash or
-- not, each with its owner, whether it compiles, the code of its error if it
-- does not, and the AST of a connection's condition that compiles, which the
-- rule of duplicate choices reads.
SELECT id, node_id, connection_id, branch_output_id, field, is_valid,
    coalesce(error ->> 'code', '')::text AS error_code,
    (CASE WHEN connection_id IS NOT NULL AND field = 'condition' THEN ast END)::jsonb AS condition_ast
FROM expression WHERE workspace_id = @workspace_id AND project_id = @project_id;

-- name: ValidationVariableUses :many
-- ValidationVariableUses returns the rows of the index of the variables that
-- the project's expressions read and write, in the trash or not, each with
-- its expression (package dependency).
SELECT expression_id::uuid AS expression_id, target_id, access FROM expression_ref
WHERE workspace_id = @workspace_id AND project_id = @project_id AND expression_id IS NOT NULL
    AND target_kind = 'variable';

-- name: ValidationTextReferences :many
-- ValidationTextReferences returns the value references and the mentions of
-- the texts of the project's nodes, connections, and components, in the
-- trash or not, read from the texts themselves, as TextReferences reads
-- them, each inline node's JSON with the text's owner: what a text refers
-- to, whether or not the index holds it.
SELECT n.id AS node_id, NULL::uuid AS connection_id, NULL::uuid AS component_id, r.inline::jsonb AS inline
FROM node n, jsonb_path_query(n.text, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.text IS NOT NULL
UNION ALL
SELECT NULL, c.id, NULL, r.inline
FROM connection c, jsonb_path_query(c.label, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id
UNION ALL
SELECT NULL, NULL, m.id, r.inline
FROM component m,
    jsonb_path_query(m.description, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
WHERE m.workspace_id = @workspace_id AND m.project_id = @project_id;

-- name: ValidationVariables :many
-- ValidationVariables returns the project's variables that are not in its
-- trash, each with its key, which the messages name it by, and the board of
-- a board's variable.
SELECT id, key, board_id FROM live_variable WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: ValidationComponents :many
-- ValidationComponents returns the IDs of the project's components that are
-- not in its trash, each with its type.
SELECT id, component_type_id FROM live_component WHERE workspace_id = @workspace_id AND project_id = @project_id;

-- name: ValidationAttributeDefs :many
-- ValidationAttributeDefs returns the IDs of the project's attribute
-- definitions that are not in its trash, each with the type that defines
-- it, whose components alone have it.
SELECT id, component_type_id FROM live_attribute_def WHERE workspace_id = @workspace_id AND project_id = @project_id;

-- name: ValidationMutes :many
-- ValidationMutes returns the project's mutes, those of owners in the trash
-- among them, each with its owner, its code, its reason, and who muted it
-- last, and when.
SELECT coalesce(node_id, connection_id, branch_output_id, variable_id, board_id)::uuid AS owner_id, code, reason,
    updated_by, updated_at
FROM validation_mute WHERE workspace_id = @workspace_id AND project_id = @project_id;

-- name: MuteOwnerLive :one
-- MuteOwnerLive returns whether the project has the entity id of the kind
-- outside its trash: a node, a connection, a branch output, a variable, or a
-- board.
SELECT (CASE @kind::text
    WHEN 'node' THEN EXISTS (SELECT 1 FROM live_node l
        WHERE l.workspace_id = @workspace_id AND l.project_id = @project_id AND l.id = @id)
    WHEN 'connection' THEN EXISTS (SELECT 1 FROM live_connection l
        WHERE l.workspace_id = @workspace_id AND l.project_id = @project_id AND l.id = @id)
    WHEN 'branch_output' THEN EXISTS (SELECT 1 FROM live_branch_output l
        WHERE l.workspace_id = @workspace_id AND l.project_id = @project_id AND l.id = @id)
    WHEN 'variable' THEN EXISTS (SELECT 1 FROM live_variable l
        WHERE l.workspace_id = @workspace_id AND l.project_id = @project_id AND l.id = @id)
    WHEN 'board' THEN EXISTS (SELECT 1 FROM live_board l
        WHERE l.workspace_id = @workspace_id AND l.project_id = @project_id AND l.id = @id)
    ELSE false
END)::boolean AS live;

-- name: ValidationMute :one
-- ValidationMute returns the reason of the mute of the code on the owner,
-- of any kind.
SELECT reason FROM validation_mute
WHERE workspace_id = @workspace_id AND project_id = @project_id
    AND coalesce(node_id, connection_id, branch_output_id, variable_id, board_id) = @owner_id AND code = @code;

-- name: UpsertValidationMute :execrows
-- UpsertValidationMute mutes the code on the owner, the one of node_id,
-- connection_id, branch_output_id, variable_id, and board_id that is not
-- null, with the reason, or changes the reason of its mute, unless it has the
-- reason already, and returns how many mutes it added or changed: none for
-- one that it found as it is.
INSERT INTO validation_mute (workspace_id, project_id, node_id, connection_id, branch_output_id, variable_id,
    board_id, code, reason, created_by, updated_by)
VALUES (@workspace_id, @project_id, @node_id, @connection_id, @branch_output_id, @variable_id, @board_id, @code,
    @reason, @updated_by, @updated_by)
ON CONFLICT (workspace_id, project_id, (coalesce(node_id, connection_id, branch_output_id, variable_id, board_id)),
    code)
DO UPDATE SET reason = excluded.reason, updated_by = excluded.updated_by, updated_at = now()
WHERE validation_mute.reason <> excluded.reason;

-- name: DeleteValidationMute :execrows
-- DeleteValidationMute unmutes the code on the owner, of any kind, and
-- returns how many mutes it deleted: none for an owner without one.
DELETE FROM validation_mute
WHERE workspace_id = @workspace_id AND project_id = @project_id
    AND coalesce(node_id, connection_id, branch_output_id, variable_id, board_id) = @owner_id AND code = @code;

-- name: TouchMuteOwner :one
-- TouchMuteOwner increments the revision of the project's entity id of the
-- kind, whose mute changed, as a change of it, and returns the new
-- revision. Each statement of the query runs, and only the one of the kind
-- matches a row.
WITH n AS (
    UPDATE node SET rev = rev + 1, updated_at = now(), updated_by = @updated_by::uuid
    WHERE @kind::text = 'node' AND workspace_id = @workspace_id::uuid AND project_id = @project_id::uuid
        AND id = @id::uuid
    RETURNING rev
), c AS (
    UPDATE connection SET rev = rev + 1, updated_at = now(), updated_by = @updated_by::uuid
    WHERE @kind::text = 'connection' AND workspace_id = @workspace_id::uuid AND project_id = @project_id::uuid
        AND id = @id::uuid
    RETURNING rev
), o AS (
    UPDATE branch_output SET rev = rev + 1, updated_at = now(), updated_by = @updated_by::uuid
    WHERE @kind::text = 'branch_output' AND workspace_id = @workspace_id::uuid AND project_id = @project_id::uuid
        AND id = @id::uuid
    RETURNING rev
), v AS (
    UPDATE variable SET rev = rev + 1, updated_at = now(), updated_by = @updated_by::uuid
    WHERE @kind::text = 'variable' AND workspace_id = @workspace_id::uuid AND project_id = @project_id::uuid
        AND id = @id::uuid
    RETURNING rev
), b AS (
    UPDATE board SET rev = rev + 1, updated_at = now(), updated_by = @updated_by::uuid
    WHERE @kind::text = 'board' AND workspace_id = @workspace_id::uuid AND project_id = @project_id::uuid
        AND id = @id::uuid
    RETURNING rev
)
SELECT rev FROM n UNION ALL SELECT rev FROM c UNION ALL SELECT rev FROM o UNION ALL SELECT rev FROM v
UNION ALL SELECT rev FROM b;
