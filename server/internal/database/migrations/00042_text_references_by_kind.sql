-- +goose Up

-- The rows of the index of what the texts name (expression_ref, plan notes
-- N53 and N96) of the nodes, the connections, and the components that share
-- their UUIDs with owners of texts of another kind in their projects, as a
-- client may choose the IDs of each. Until plan item 89, the derivation of
-- the index (package dependency) took the texts of such owners that one batch
-- changed for those of one site, and wrote what the one's text names as the
-- other's, so each such owner's rows are written anew here, of what its own
-- text names, as migration 00027 wrote them, and as the derivation writes them
-- now, by each owner's kind and UUID. The rows of the other texts, and those
-- of the expressions, stay as they are.
--
-- public_uuid decodes a public ID, as migration 00027's does: a prefix, an
-- underscore, and the UUID's 128 bits in 26 characters of Crockford's base32,
-- the most significant first, each in its canonical spelling, in upper case,
-- as every text holds it (package richtext).
CREATE FUNCTION pg_temp.public_uuid(id text) RETURNS uuid LANGUAGE sql IMMUTABLE STRICT AS $$
	SELECT string_agg(to_hex(substring(b.bits FROM 4 * i + 1 FOR 4)::bit(4)::integer), '' ORDER BY i)::uuid
	FROM (SELECT substring(string_agg((strpos('0123456789ABCDEFGHJKMNPQRSTVWXYZ', t.c) - 1)::bit(5)::text, ''
			ORDER BY t.n) FROM 3) AS bits
		FROM regexp_split_to_table(substring(id FROM '_(.*)$'), '') WITH ORDINALITY AS t (c, n)) b,
		generate_series(0, 31) AS i
$$;

-- The rows of the texts of the owners that share a UUID, which a node, a
-- connection, and a component of one project may.
WITH owners AS (
	SELECT workspace_id, project_id, id FROM node
	UNION ALL
	SELECT workspace_id, project_id, id FROM connection
	UNION ALL
	SELECT workspace_id, project_id, id FROM component
), shared AS (
	SELECT workspace_id, project_id, id FROM owners GROUP BY workspace_id, project_id, id HAVING count(*) > 1
)
DELETE FROM expression_ref r
USING shared s
WHERE r.workspace_id = s.workspace_id AND r.project_id = s.project_id AND r.expression_id IS NULL
	AND coalesce(r.node_id, r.connection_id, r.component_id) = s.id;

-- What the text of each such owner names: the variable, or the component and
-- the attribute, of each value reference, and the component of each mention,
-- read.
WITH owners AS (
	SELECT workspace_id, project_id, id FROM node
	UNION ALL
	SELECT workspace_id, project_id, id FROM connection
	UNION ALL
	SELECT workspace_id, project_id, id FROM component
), shared AS (
	SELECT workspace_id, project_id, id FROM owners GROUP BY workspace_id, project_id, id HAVING count(*) > 1
), named AS (
	SELECT n.workspace_id, n.project_id, n.id AS node_id, NULL::uuid AS connection_id, NULL::uuid AS component_id,
		n.board_id, r.inline
	FROM node n
	JOIN shared s ON s.workspace_id = n.workspace_id AND s.project_id = n.project_id AND s.id = n.id,
		jsonb_path_query(n.text, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
	WHERE n.text IS NOT NULL
	UNION ALL
	SELECT c.workspace_id, c.project_id, NULL, c.id, NULL, c.board_id, r.inline
	FROM connection c
	JOIN shared s ON s.workspace_id = c.workspace_id AND s.project_id = c.project_id AND s.id = c.id,
		jsonb_path_query(c.label, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
	UNION ALL
	SELECT m.workspace_id, m.project_id, NULL, NULL, m.id, NULL, r.inline
	FROM component m
	JOIN shared s ON s.workspace_id = m.workspace_id AND s.project_id = m.project_id AND s.id = m.id,
		jsonb_path_query(m.description, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
)
INSERT INTO expression_ref (workspace_id, project_id, node_id, connection_id, component_id, board_id, target_kind,
	target_id, attribute_def_id, access)
SELECT DISTINCT workspace_id, project_id, node_id, connection_id, component_id, board_id,
	CASE WHEN inline->>'type' = 'mention' THEN 'component'
		WHEN inline->'attrs' ? 'attribute' THEN 'attribute' ELSE 'variable' END,
	pg_temp.public_uuid(coalesce(inline->'attrs'->>'id', inline->'attrs'->>'ref')),
	pg_temp.public_uuid(inline->'attrs'->>'attribute'), 'read'
FROM named;

DROP FUNCTION pg_temp.public_uuid(text);
