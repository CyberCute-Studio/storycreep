-- +goose Up

-- The rows of the index of what the texts name (expression_ref, plan notes
-- N53 and N96) that the derivation of the index (package dependency) wrote
-- wrongly until plan item 89, of the nodes, the connections, and the
-- components that shared their UUIDs with owners of texts of another kind in
-- their projects, as a client may choose the IDs of each. It took the texts
-- of such owners that one batch changed for those of one site, and wrote what
-- all of them named as the rows of the first of them that named something,
-- and none as the others'. So each owner's rows are what its own text names;
-- or that and what the texts of the owners that shared its UUID named then;
-- or none; and they stay so once those owners are purged, as one in the trash
-- is after 30 days, and share its UUID no more. An owner's rows are then what
-- its text names exactly when they are as many as the different things that
-- it names, and the rows of each owner whose rows are not are written anew
-- here, of what its own text names, as migration 00027 wrote them, and as the
-- derivation writes them now, by each owner's kind and UUID. The rows of the
-- other texts, and those of the expressions, stay as they are, and only the
-- texts of the owners whose rows are written anew have their IDs decoded.
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

-- The owners of texts whose rows are not as many as the different things
-- that their texts name, each by its kind and its UUID: the variable, or the
-- component and the attribute, of each value reference, and the component of
-- each mention, by their public IDs, which a text holds in their canonical
-- spellings alone, so that two of them name the same thing exactly when they
-- are the same strings.
CREATE TEMP TABLE misindexed AS
WITH named AS (
	SELECT n.workspace_id, n.project_id, 'node' AS kind, n.id, r.inline
	FROM node n, jsonb_path_query(n.text, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
	WHERE n.text IS NOT NULL
	UNION ALL
	SELECT c.workspace_id, c.project_id, 'connection', c.id, r.inline
	FROM connection c,
		jsonb_path_query(c.label, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
	UNION ALL
	SELECT m.workspace_id, m.project_id, 'component', m.id, r.inline
	FROM component m,
		jsonb_path_query(m.description, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
), names AS (
	SELECT workspace_id, project_id, kind, id,
		count(DISTINCT (CASE WHEN inline->>'type' = 'mention' THEN 'component'
				WHEN inline->'attrs' ? 'attribute' THEN 'attribute' ELSE 'variable' END,
			coalesce(inline->'attrs'->>'id', inline->'attrs'->>'ref', ''),
			coalesce(inline->'attrs'->>'attribute', ''))) AS n
	FROM named
	GROUP BY workspace_id, project_id, kind, id
), indexed AS (
	SELECT workspace_id, project_id,
		CASE WHEN node_id IS NOT NULL THEN 'node' WHEN connection_id IS NOT NULL THEN 'connection'
			ELSE 'component' END AS kind,
		coalesce(node_id, connection_id, component_id) AS id, count(*) AS n
	FROM expression_ref
	WHERE expression_id IS NULL
	GROUP BY 1, 2, 3, 4
)
SELECT workspace_id, project_id, kind, id
FROM names FULL JOIN indexed USING (workspace_id, project_id, kind, id)
WHERE names.n IS DISTINCT FROM indexed.n;

-- The rows of each such owner.
DELETE FROM expression_ref r
USING misindexed w
WHERE r.workspace_id = w.workspace_id AND r.project_id = w.project_id AND r.expression_id IS NULL
	AND coalesce(r.node_id, r.connection_id, r.component_id) = w.id
	AND CASE WHEN r.node_id IS NOT NULL THEN 'node' WHEN r.connection_id IS NOT NULL THEN 'connection'
		ELSE 'component' END = w.kind;

-- What the text of each such owner names: the variable, or the component and
-- the attribute, of each value reference, and the component of each mention,
-- read.
WITH named AS (
	SELECT n.workspace_id, n.project_id, n.id AS node_id, NULL::uuid AS connection_id, NULL::uuid AS component_id,
		n.board_id, r.inline
	FROM node n
	JOIN misindexed w ON w.workspace_id = n.workspace_id AND w.project_id = n.project_id AND w.kind = 'node'
		AND w.id = n.id,
		jsonb_path_query(n.text, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
	WHERE n.text IS NOT NULL
	UNION ALL
	SELECT c.workspace_id, c.project_id, NULL, c.id, NULL, c.board_id, r.inline
	FROM connection c
	JOIN misindexed w ON w.workspace_id = c.workspace_id AND w.project_id = c.project_id AND w.kind = 'connection'
		AND w.id = c.id,
		jsonb_path_query(c.label, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
	UNION ALL
	SELECT m.workspace_id, m.project_id, NULL, NULL, m.id, NULL, r.inline
	FROM component m
	JOIN misindexed w ON w.workspace_id = m.workspace_id AND w.project_id = m.project_id AND w.kind = 'component'
		AND w.id = m.id,
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

DROP TABLE misindexed;
DROP FUNCTION pg_temp.public_uuid(text);
