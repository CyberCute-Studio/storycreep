-- The index of what a project's content reads and writes (package
-- dependency, PRD FR-DEP-01, FR-COMP-04, §5.3, plan note N53): a row of each
-- thing that a site names, an expression or the rich text of a node, a
-- connection, or a component, and how the site uses it. expression_ref is a
-- tenant table, which a transaction of its workspace reads. A batch's
-- transaction replaces a site's rows, once every command of the batch has
-- applied, under the lock of the project's row: it deletes those that the
-- site no longer names (DeleteReferences), and adds those that it did not
-- name before (InsertReferences), so that a site that names what it named,
-- as a text whose edit kept its references, writes nothing.

-- name: InsertReferences :exec
-- InsertReferences adds the rows of the index that rows lists, a JSON array
-- of objects of each row's columns, each UUID in its text form, and a column
-- that is null left out, but those that the index holds already, which it
-- leaves as they are.
INSERT INTO expression_ref (workspace_id, project_id, expression_id, node_id, connection_id, component_id, board_id,
    target_kind, target_id, attribute_def_id, access)
SELECT @workspace_id::uuid, @project_id::uuid, (r.value->>'expression_id')::uuid, (r.value->>'node_id')::uuid,
    (r.value->>'connection_id')::uuid, (r.value->>'component_id')::uuid, (r.value->>'board_id')::uuid,
    r.value->>'target_kind', (r.value->>'target_id')::uuid, (r.value->>'attribute_def_id')::uuid, r.value->>'access'
FROM jsonb_array_elements(@rows::jsonb) AS r
ON CONFLICT DO NOTHING;

-- name: DeleteReferences :exec
-- DeleteReferences deletes the rows of the index of the project's sites
-- whose IDs are among expression_ids, node_ids, connection_ids, and
-- component_ids, the expressions and the texts of the nodes, the
-- connections, and the components, but those that rows lists, as
-- InsertReferences reads it: the rows that the sites no longer name, to
-- which InsertReferences then adds what they name now. A column that is
-- null is compared as the zero UUID, which no site and no target is, so
-- that the rows are compared by equalities that a hash join compares,
-- however many the batch replaces.
WITH kept AS (
    SELECT coalesce((r.value->>'expression_id')::uuid, '00000000-0000-0000-0000-000000000000') AS expression_id,
        coalesce((r.value->>'node_id')::uuid, '00000000-0000-0000-0000-000000000000') AS node_id,
        coalesce((r.value->>'connection_id')::uuid, '00000000-0000-0000-0000-000000000000') AS connection_id,
        coalesce((r.value->>'component_id')::uuid, '00000000-0000-0000-0000-000000000000') AS component_id,
        r.value->>'target_kind' AS target_kind, (r.value->>'target_id')::uuid AS target_id,
        coalesce((r.value->>'attribute_def_id')::uuid, '00000000-0000-0000-0000-000000000000') AS attribute_def_id,
        r.value->>'access' AS access
    FROM jsonb_array_elements(@rows::jsonb) AS r
)
DELETE FROM expression_ref x
WHERE x.workspace_id = @workspace_id AND x.project_id = @project_id
    AND (x.expression_id = ANY(@expression_ids::uuid[]) OR x.node_id = ANY(@node_ids::uuid[])
        OR x.connection_id = ANY(@connection_ids::uuid[]) OR x.component_id = ANY(@component_ids::uuid[]))
    AND NOT EXISTS (
        SELECT FROM kept k
        WHERE k.expression_id = coalesce(x.expression_id, '00000000-0000-0000-0000-000000000000')
            AND k.node_id = coalesce(x.node_id, '00000000-0000-0000-0000-000000000000')
            AND k.connection_id = coalesce(x.connection_id, '00000000-0000-0000-0000-000000000000')
            AND k.component_id = coalesce(x.component_id, '00000000-0000-0000-0000-000000000000')
            AND k.target_kind = x.target_kind AND k.target_id = x.target_id
            AND k.attribute_def_id = coalesce(x.attribute_def_id, '00000000-0000-0000-0000-000000000000')
            AND k.access = x.access
    );

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
), page AS (
    -- The page's sites, of which alone the boards' names and the nodes'
    -- titles are read, however many sites use the target.
    SELECT s.*, coalesce(s.board_id, '00000000-0000-0000-0000-000000000000') AS board_key,
        coalesce(s.node_id, '00000000-0000-0000-0000-000000000000') AS node_key,
        coalesce(s.attribute_def_id, '00000000-0000-0000-0000-000000000000') AS attribute_key
    FROM sites s
    WHERE (coalesce(s.board_id, '00000000-0000-0000-0000-000000000000'),
            coalesce(s.node_id, '00000000-0000-0000-0000-000000000000'), s.site_id,
            coalesce(s.attribute_def_id, '00000000-0000-0000-0000-000000000000'))
        > (@after_board::uuid, @after_node::uuid, @after_site::uuid, @after_attribute::uuid)
    ORDER BY board_key, node_key, s.site_id, attribute_key
    LIMIT @max_rows
)
SELECT p.site, p.board_id, b.name AS board_name, p.node_id, nn.title AS node_title, p.connection_id,
    p.branch_output_id, p.component_id, p.attribute_def_id, p.site_id
FROM page p
LEFT JOIN board b ON b.workspace_id = @workspace_id AND b.project_id = @project_id AND b.id = p.board_id
LEFT JOIN node nn ON nn.workspace_id = @workspace_id AND nn.project_id = @project_id AND nn.id = p.node_id
ORDER BY p.board_key, p.node_key, p.site_id, p.attribute_key;

-- name: TextReferrers :many
-- TextReferrers returns the texts of the project, of its nodes, connections,
-- and components, in the trash or not, whose rows of the index name the
-- target, of a kind among target_kinds, each once.
SELECT DISTINCT node_id, connection_id, component_id FROM expression_ref
WHERE workspace_id = @workspace_id AND project_id = @project_id AND target_id = @target_id
    AND target_kind = ANY(@target_kinds::text[]) AND expression_id IS NULL;

-- name: LiveTextOwners :many
-- LiveTextOwners returns those of the project's nodes among node_ids,
-- connections among connection_ids, and components among component_ids that
-- are not in its trash, as a usage list shows their texts, each with its
-- board, none for a component, and the node of its site: a node's own, and
-- the node that a connection leaves.
SELECT n.id, n.board_id::uuid AS board_id, n.id AS site_node_id FROM live_node n
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.id = ANY(@node_ids::uuid[])
UNION ALL
SELECT c.id, c.board_id, c.from_node_id FROM live_connection c
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.id = ANY(@connection_ids::uuid[])
UNION ALL
SELECT m.id, NULL, NULL FROM live_component m
WHERE m.workspace_id = @workspace_id AND m.project_id = @project_id AND m.id = ANY(@component_ids::uuid[]);

-- name: SiteNames :many
-- SiteNames returns the names of the project's boards among board_ids and the
-- titles of its nodes among node_ids, in the trash or not, by their IDs, as
-- the sites of a usage show them.
SELECT b.id, b.name AS label FROM board b
WHERE b.workspace_id = @workspace_id AND b.project_id = @project_id AND b.id = ANY(@board_ids::uuid[])
UNION ALL
SELECT n.id, n.title FROM node n
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.id = ANY(@node_ids::uuid[]);

-- The graph of the dependencies between a project's boards (PRD FR-DEP-02,
-- plan note N56), which the index's rows of the sites that are not in the
-- trash give, and the jumpers: an edge of the kind variable or attribute
-- from a board that writes a variable, or a component's attribute, to
-- another that reads it, of each pair of a site of the one that writes it
-- and a site of the other that reads it; an edge of the kind visit from the
-- board of a node to another one of whose expressions reads whether it was
-- visited, of each such expression; and an edge of the kind jumper from the
-- board of a jumper to another that its target is on, of each jumper. No
-- edge joins a board to itself. The graph's reads take no parameter but the
-- project, and package dependency pairs and filters what they return, since
-- a prepared statement's generic plan cannot tell a filter that is null from
-- one that is not: it planned the pairs of the large fixture's 10,000 sites
-- as if each filter kept one row, by nested loops of 12 s. The reads of an
-- edge's contributions may take filters that are null, since the edge's
-- boards, its kind, and the accesses bound their rows first.

-- name: GraphBoards :many
-- GraphBoards returns the project's boards that are not in its trash, the
-- graph's nodes, each with its name, in the order of their IDs.
SELECT id, name FROM live_board WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY id;

-- name: GraphReferences :many
-- GraphReferences returns how many sites of each board of the project read,
-- or write, each variable and each component's attribute, and read whether
-- each node was visited, with the node's board, of the sites and the nodes
-- that are not in its trash: the rows of the index of those kinds of the
-- sites on its boards, grouped by their boards, their targets, their
-- attributes' definitions, and their accesses. A component's description
-- belongs to no board, and a mention is no dependency.
WITH live_site AS (
    -- The sites of the project that are not in its trash, each by its kind
    -- and its ID: the expressions whose owners are not, and the nodes and
    -- the connections, whose texts are sites by their IDs. A site is matched
    -- by its kind too, since an expression, a node, and a connection may
    -- share a UUID in a database whose client gave a node or a connection
    -- another's before migration 00029 refused it (PRD §5.2), so that a site
    -- in the trash is not live by another kind's live entity.
    SELECT 'expression' AS kind, e.id FROM expression e
    JOIN live_node n ON n.workspace_id = e.workspace_id AND n.project_id = e.project_id AND n.id = e.node_id
    WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id
    UNION ALL
    SELECT 'expression', e.id FROM expression e
    JOIN live_connection c ON c.workspace_id = e.workspace_id AND c.project_id = e.project_id
        AND c.id = e.connection_id
    WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id
    UNION ALL
    SELECT 'expression', e.id FROM expression e
    JOIN live_branch_output o ON o.workspace_id = e.workspace_id AND o.project_id = e.project_id
        AND o.id = e.branch_output_id
    WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id
    UNION ALL
    SELECT 'node', n.id FROM live_node n WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id
    UNION ALL
    SELECT 'connection', c.id FROM live_connection c
    WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id
)
SELECT r.board_id::uuid AS board_id, r.target_kind, r.target_id, r.attribute_def_id, r.access,
    v.board_id AS visited_board_id, count(*)::bigint AS sites
FROM expression_ref r
LEFT JOIN live_node v ON r.target_kind = 'node' AND v.workspace_id = r.workspace_id AND v.project_id = r.project_id
    AND v.id = r.target_id
WHERE r.workspace_id = @workspace_id AND r.project_id = @project_id AND r.board_id IS NOT NULL
    AND r.target_kind IN ('variable', 'attribute', 'node') AND (r.target_kind <> 'node' OR v.id IS NOT NULL)
    AND (CASE WHEN r.expression_id IS NOT NULL THEN 'expression' WHEN r.node_id IS NOT NULL THEN 'node'
        ELSE 'connection' END, coalesce(r.expression_id, r.node_id, r.connection_id))
        IN (SELECT kind, id FROM live_site)
GROUP BY r.board_id, r.target_kind, r.target_id, r.attribute_def_id, r.access, v.board_id;

-- name: GraphJumpers :many
-- GraphJumpers returns how many jumpers of each board of the project target
-- nodes of each other board, of the jumpers and the targets that are not in
-- its trash.
SELECT j.board_id AS from_board_id, t.board_id AS to_board_id, count(*)::bigint AS jumpers
FROM live_node j
JOIN live_node t ON t.workspace_id = j.workspace_id AND t.project_id = j.project_id AND t.id = j.target_node_id
WHERE j.workspace_id = @workspace_id AND j.project_id = @project_id AND j.type = 'jumper' AND t.board_id <> j.board_id
GROUP BY j.board_id, t.board_id;

-- name: ValueContributions :many
-- ValueContributions returns at most max_rows of the contributions to the
-- edge of the kind target_kind, variable or attribute, from the board
-- from_board_id to the board to_board_id, another: each pair of a site of
-- the one that writes a variable, or a component's attribute, and a site of
-- the other that reads it, of the sites that are not in the project's trash,
-- of the target_id and the attribute_def_id, if they are not null. Each site
-- comes with its node, as a usage's does: an expression's owner's node, a
-- connection's the node that it leaves, and an output's its branch; and a
-- text's owner's, a connection's the node that it leaves too. They are in
-- the order of their targets, their attributes' definitions, the zero UUID
-- for a variable's, the nodes and the sites that write them, and the nodes
-- and the sites that read them, a site being an expression, or a text's
-- owner, by its UUID and then its type, since two sites of one node may
-- share a UUID, as a node's text and the label of a connection that leaves
-- it do if a client chose the connection's UUID to be the node's before
-- migration 00029 refused it (PRD §5.2), after the key whose parts are
-- after_target, after_attribute, after_from_node, after_from_site,
-- after_from_site_type, after_to_node, after_to_site, and after_to_site_type,
-- which is all zero UUIDs and empty types for the first page, as no
-- contribution is.
WITH refs AS (
    SELECT r.* FROM expression_ref r
    WHERE r.workspace_id = @workspace_id AND r.project_id = @project_id AND r.target_kind = @target_kind
        AND (r.board_id = @from_board_id AND r.access = 'write' OR r.board_id = @to_board_id AND r.access = 'read')
        AND (sqlc.narg(target_id)::uuid IS NULL OR r.target_id = sqlc.narg(target_id)::uuid)
        AND (sqlc.narg(attribute_def_id)::uuid IS NULL OR r.attribute_def_id = sqlc.narg(attribute_def_id)::uuid)
), sites AS (
    SELECT r.board_id, r.access, r.target_id, r.attribute_def_id,
        coalesce(r.attribute_def_id, '00000000-0000-0000-0000-000000000000') AS attribute_key, e.id AS site_id,
        (CASE WHEN e.node_id IS NOT NULL THEN 'node_' || e.field WHEN e.connection_id IS NOT NULL
            THEN 'connection_' || e.field ELSE 'branch_output_condition' END)::text AS site,
        coalesce(n.id, c.from_node_id, o.node_id) AS node_id, c.id AS connection_id, o.id AS branch_output_id
    FROM refs r
    JOIN expression e ON e.workspace_id = r.workspace_id AND e.id = r.expression_id
    LEFT JOIN live_node n ON n.workspace_id = e.workspace_id AND n.project_id = e.project_id AND n.id = e.node_id
    LEFT JOIN live_connection c ON c.workspace_id = e.workspace_id AND c.project_id = e.project_id
        AND c.id = e.connection_id
    LEFT JOIN live_branch_output o ON o.workspace_id = e.workspace_id AND o.project_id = e.project_id
        AND o.id = e.branch_output_id
    WHERE coalesce(n.id, c.id, o.id) IS NOT NULL
    UNION ALL
    SELECT r.board_id, r.access, r.target_id, r.attribute_def_id,
        coalesce(r.attribute_def_id, '00000000-0000-0000-0000-000000000000'), n.id, 'node_text', n.id, NULL, NULL
    FROM refs r JOIN live_node n ON n.workspace_id = r.workspace_id AND n.project_id = r.project_id AND n.id = r.node_id
    UNION ALL
    SELECT r.board_id, r.access, r.target_id, r.attribute_def_id,
        coalesce(r.attribute_def_id, '00000000-0000-0000-0000-000000000000'), c.id, 'connection_label',
        c.from_node_id, c.id, NULL
    FROM refs r JOIN live_connection c ON c.workspace_id = r.workspace_id AND c.project_id = r.project_id
        AND c.id = r.connection_id
), page AS (
    -- The page's contributions, of which alone the nodes' titles are read,
    -- however many contribute to the edge.
    SELECT w.target_id, w.attribute_def_id, w.attribute_key, w.site AS from_site, w.node_id AS from_node_id,
        w.site_id AS from_site_id, w.connection_id AS from_connection_id,
        w.branch_output_id AS from_branch_output_id, r.site AS to_site, r.node_id AS to_node_id,
        r.site_id AS to_site_id, r.connection_id AS to_connection_id, r.branch_output_id AS to_branch_output_id
    FROM sites w
    JOIN sites r ON r.target_id = w.target_id AND r.attribute_key = w.attribute_key
    WHERE w.board_id = @from_board_id AND w.access = 'write' AND r.board_id = @to_board_id AND r.access = 'read'
        AND (w.target_id, w.attribute_key, w.node_id, w.site_id, w.site, r.node_id, r.site_id, r.site)
            > (@after_target::uuid, @after_attribute::uuid, @after_from_node::uuid, @after_from_site::uuid,
                @after_from_site_type::text, @after_to_node::uuid, @after_to_site::uuid, @after_to_site_type::text)
    ORDER BY w.target_id, w.attribute_key, w.node_id, w.site_id, w.site, r.node_id, r.site_id, r.site
    LIMIT @max_rows
)
SELECT p.target_id::uuid AS target_id, p.attribute_def_id, p.from_site, p.from_node_id::uuid AS from_node_id,
    fn.title AS from_node_title, p.from_site_id::uuid AS from_site_id, p.from_connection_id, p.from_branch_output_id,
    p.to_site, p.to_node_id::uuid AS to_node_id, tn.title AS to_node_title, p.to_site_id::uuid AS to_site_id,
    p.to_connection_id, p.to_branch_output_id
FROM page p
JOIN node fn ON fn.workspace_id = @workspace_id AND fn.project_id = @project_id AND fn.id = p.from_node_id
JOIN node tn ON tn.workspace_id = @workspace_id AND tn.project_id = @project_id AND tn.id = p.to_node_id
ORDER BY p.target_id, p.attribute_key, p.from_node_id, p.from_site_id, p.from_site, p.to_node_id, p.to_site_id,
    p.to_site;

-- name: VisitContributions :many
-- VisitContributions returns at most max_rows of the contributions to the
-- edge of the kind visit from the board from_board_id to the board
-- to_board_id, another: each pair of a node of the one and an expression of
-- the other that reads whether it was visited, of the nodes and the
-- expressions that are not in the project's trash, an expression with its
-- node, as a usage's is. Only an expression names a node. They are in the
-- order of the nodes visited, and the nodes and the expressions that read
-- them, after the key of ValueContributions, whose target, node, and site
-- of the board from_board_id are each the visited node's ID, and whose
-- attribute is the zero UUID, but for the sites' types, since an
-- expression's UUID is its own.
WITH page AS (
    SELECT v.id AS visited_id, e.id AS site_id,
        (CASE WHEN e.node_id IS NOT NULL THEN 'node_' || e.field WHEN e.connection_id IS NOT NULL
            THEN 'connection_' || e.field ELSE 'branch_output_condition' END)::text AS site,
        coalesce(n.id, c.from_node_id, o.node_id) AS node_id, c.id AS connection_id, o.id AS branch_output_id
    FROM expression_ref r
    JOIN live_node v ON v.workspace_id = r.workspace_id AND v.project_id = r.project_id AND v.id = r.target_id
    JOIN expression e ON e.workspace_id = r.workspace_id AND e.id = r.expression_id
    LEFT JOIN live_node n ON n.workspace_id = e.workspace_id AND n.project_id = e.project_id AND n.id = e.node_id
    LEFT JOIN live_connection c ON c.workspace_id = e.workspace_id AND c.project_id = e.project_id
        AND c.id = e.connection_id
    LEFT JOIN live_branch_output o ON o.workspace_id = e.workspace_id AND o.project_id = e.project_id
        AND o.id = e.branch_output_id
    WHERE r.workspace_id = @workspace_id AND r.project_id = @project_id AND r.target_kind = 'node'
        AND r.board_id = @to_board_id AND v.board_id = @from_board_id AND coalesce(n.id, c.id, o.id) IS NOT NULL
        AND (v.id, '00000000-0000-0000-0000-000000000000'::uuid, v.id, v.id, coalesce(n.id, c.from_node_id, o.node_id),
            e.id) > (@after_target::uuid, @after_attribute::uuid, @after_from_node::uuid, @after_from_site::uuid,
                @after_to_node::uuid, @after_to_site::uuid)
    ORDER BY v.id, coalesce(n.id, c.from_node_id, o.node_id), e.id
    LIMIT @max_rows
)
SELECT p.visited_id::uuid AS visited_id, vn.title AS visited_title, p.site, p.node_id::uuid AS node_id,
    tn.title AS node_title, p.site_id::uuid AS site_id, p.connection_id, p.branch_output_id
FROM page p
JOIN node vn ON vn.workspace_id = @workspace_id AND vn.project_id = @project_id AND vn.id = p.visited_id
JOIN node tn ON tn.workspace_id = @workspace_id AND tn.project_id = @project_id AND tn.id = p.node_id
ORDER BY p.visited_id, p.node_id, p.site_id;

-- name: JumperContributions :many
-- JumperContributions returns at most max_rows of the contributions to the
-- edge of the kind jumper from the board from_board_id to the board
-- to_board_id, another: the jumpers of the one whose targets are nodes of
-- the other, of the jumpers and the targets that are not in the project's
-- trash, each with its target. They are in the order of the jumpers, after
-- the key of ValueContributions, whose target and attribute are the zero
-- UUID, whose node and site of the board from_board_id are each the
-- jumper's ID, and whose node and site of the board to_board_id are each
-- its target's, but for the sites' types, since a jumper's UUID is its own.
SELECT j.id AS jumper_id, j.title AS jumper_title, t.id AS target_id, t.title AS target_title
FROM live_node j
JOIN live_node t ON t.workspace_id = j.workspace_id AND t.project_id = j.project_id AND t.id = j.target_node_id
WHERE j.workspace_id = @workspace_id AND j.project_id = @project_id AND j.board_id = @from_board_id
    AND j.type = 'jumper' AND t.board_id = @to_board_id
    AND ('00000000-0000-0000-0000-000000000000'::uuid, '00000000-0000-0000-0000-000000000000'::uuid, j.id, j.id,
        t.id, t.id) > (@after_target::uuid, @after_attribute::uuid, @after_from_node::uuid, @after_from_site::uuid,
            @after_to_node::uuid, @after_to_site::uuid)
ORDER BY j.id
LIMIT @max_rows;
