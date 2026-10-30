-- The index of what a project's content reads and writes (package
-- dependency, PRD FR-DEP-01, FR-COMP-04, §5.3, plan note N53): a row of each
-- thing that a site names, an expression or the rich text of a node, a
-- connection, or a component, and how the site uses it. expression_ref is a
-- tenant table, which a transaction of its workspace reads. A batch's
-- transaction replaces a site's rows, once every command of the batch has
-- applied, under the lock of the project's row.

-- name: InsertReferences :exec
-- InsertReferences adds the rows of the index that rows lists, a JSON array
-- of objects of each row's columns, each UUID in its text form, and a column
-- that is null left out.
INSERT INTO expression_ref (workspace_id, project_id, expression_id, node_id, connection_id, component_id, board_id,
    target_kind, target_id, attribute_def_id, access)
SELECT @workspace_id::uuid, @project_id::uuid, (r.value->>'expression_id')::uuid, (r.value->>'node_id')::uuid,
    (r.value->>'connection_id')::uuid, (r.value->>'component_id')::uuid, (r.value->>'board_id')::uuid,
    r.value->>'target_kind', (r.value->>'target_id')::uuid, (r.value->>'attribute_def_id')::uuid, r.value->>'access'
FROM jsonb_array_elements(@rows::jsonb) AS r;

-- name: DeleteExpressionReferences :exec
-- DeleteExpressionReferences deletes the rows of the index of the project's
-- expressions whose IDs are among ids, which their compilation then writes
-- again.
DELETE FROM expression_ref
WHERE workspace_id = @workspace_id AND project_id = @project_id AND expression_id = ANY(@ids::uuid[]);

-- name: TextReferences :many
-- TextReferences returns the value references and the mentions of the
-- texts of the project's nodes whose IDs are among node_ids, connections
-- among connection_ids, and components among component_ids, in the trash or
-- not: each inline node's JSON, as the text holds it, with the text's owner
-- and the owner's board, or none for a component. A jumper's or a branch's
-- node has no text. jsonpath's strict .** reaches each node of a text once,
-- however deeply its lists nest, and an inline node is the only node of a
-- text with one of those types.
SELECT n.id AS node_id, NULL::uuid AS connection_id, NULL::uuid AS component_id, n.board_id::uuid AS board_id,
    r.inline::jsonb AS inline
FROM node n, jsonb_path_query(n.text, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.id = ANY(@node_ids::uuid[])
    AND n.text IS NOT NULL
UNION ALL
SELECT NULL, c.id, NULL, c.board_id, r.inline
FROM connection c, jsonb_path_query(c.label, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.id = ANY(@connection_ids::uuid[])
UNION ALL
SELECT NULL, NULL, m.id, NULL, r.inline
FROM component m,
    jsonb_path_query(m.description, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
WHERE m.workspace_id = @workspace_id AND m.project_id = @project_id AND m.id = ANY(@component_ids::uuid[]);

-- name: DeleteTextReferences :exec
-- DeleteTextReferences deletes the rows of the index of the texts of the
-- project's nodes whose IDs are among node_ids, connections among
-- connection_ids, and components among component_ids, which the batch then
-- writes again.
DELETE FROM expression_ref
WHERE workspace_id = @workspace_id AND project_id = @project_id
    AND (node_id = ANY(@node_ids::uuid[]) OR connection_id = ANY(@connection_ids::uuid[])
        OR component_id = ANY(@component_ids::uuid[]));

-- name: Usages :many
-- Usages returns at most max_rows of the sites of the project that are not
-- in its trash that use the target: those whose rows of the index name it,
-- of the kind target_kind, with the access, or, if speaker is true, the
-- nodes whose speaker it is, a component. Each comes with its board and its
-- node, as the panel of a variable's or a component's usages shows it: an
-- expression's owner's board, and its node, a connection's the node that it
-- leaves, and an output's its branch; a text's owner's, a connection's the
-- node that it leaves too; and none for a component's description. They are
-- in the order of their boards, their nodes, the sites themselves (an
-- expression, or a text's or a speaker's owner), and the attributes that
-- they name, of which a component's site may name several, each of which
-- none of them holds counted as the zero UUID, after the key after_board,
-- after_node, after_site, after_attribute, which is all zero UUIDs for the
-- first page, as no site is.
WITH refs AS (
    SELECT * FROM expression_ref
    WHERE NOT @speaker::boolean AND workspace_id = @workspace_id AND project_id = @project_id
        AND target_id = @target_id AND target_kind = @target_kind AND access = @access
), sites AS (
    SELECT r.attribute_def_id, e.id AS site_id, e.board_id,
        (CASE WHEN e.node_id IS NOT NULL THEN 'node_' || e.field WHEN e.connection_id IS NOT NULL
            THEN 'connection_' || e.field ELSE 'branch_output_condition' END)::text AS site,
        coalesce(n.id, c.from_node_id, o.node_id) AS node_id, c.id AS connection_id, o.id AS branch_output_id,
        NULL::uuid AS component_id
    FROM refs r
    JOIN expression e ON e.workspace_id = r.workspace_id AND e.id = r.expression_id
    LEFT JOIN live_node n ON n.workspace_id = e.workspace_id AND n.project_id = e.project_id AND n.id = e.node_id
    LEFT JOIN live_connection c ON c.workspace_id = e.workspace_id AND c.project_id = e.project_id
        AND c.id = e.connection_id
    LEFT JOIN live_branch_output o ON o.workspace_id = e.workspace_id AND o.project_id = e.project_id
        AND o.id = e.branch_output_id
    WHERE coalesce(n.id, c.id, o.id) IS NOT NULL
    UNION ALL
    SELECT r.attribute_def_id, n.id, n.board_id, 'node_text', n.id, NULL, NULL, NULL
    FROM refs r JOIN live_node n ON n.workspace_id = r.workspace_id AND n.project_id = r.project_id AND n.id = r.node_id
    UNION ALL
    SELECT r.attribute_def_id, c.id, c.board_id, 'connection_label', c.from_node_id, c.id, NULL, NULL
    FROM refs r JOIN live_connection c ON c.workspace_id = r.workspace_id AND c.project_id = r.project_id
        AND c.id = r.connection_id
    UNION ALL
    SELECT r.attribute_def_id, m.id, NULL, 'component_description', NULL, NULL, NULL, m.id
    FROM refs r JOIN live_component m ON m.workspace_id = r.workspace_id AND m.project_id = r.project_id
        AND m.id = r.component_id
    UNION ALL
    SELECT NULL, n.id, n.board_id, 'node_speaker', n.id, NULL, NULL, NULL
    FROM live_node n
    WHERE @speaker::boolean AND n.workspace_id = @workspace_id AND n.project_id = @project_id
        AND n.speaker_component_id = @target_id
)
SELECT s.site, s.board_id, b.name AS board_name, s.node_id, nn.title AS node_title, s.connection_id,
    s.branch_output_id, s.component_id, s.attribute_def_id, s.site_id
FROM sites s
LEFT JOIN board b ON b.workspace_id = @workspace_id AND b.project_id = @project_id AND b.id = s.board_id
LEFT JOIN node nn ON nn.workspace_id = @workspace_id AND nn.project_id = @project_id AND nn.id = s.node_id
WHERE (coalesce(s.board_id, '00000000-0000-0000-0000-000000000000'),
        coalesce(s.node_id, '00000000-0000-0000-0000-000000000000'), s.site_id,
        coalesce(s.attribute_def_id, '00000000-0000-0000-0000-000000000000'))
    > (@after_board::uuid, @after_node::uuid, @after_site::uuid, @after_attribute::uuid)
ORDER BY coalesce(s.board_id, '00000000-0000-0000-0000-000000000000'),
    coalesce(s.node_id, '00000000-0000-0000-0000-000000000000'), s.site_id,
    coalesce(s.attribute_def_id, '00000000-0000-0000-0000-000000000000')
LIMIT @max_rows;
