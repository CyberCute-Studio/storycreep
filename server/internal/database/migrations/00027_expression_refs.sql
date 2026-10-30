-- +goose Up

-- The index of what a project's content reads and writes (PRD FR-DEP-01,
-- FR-COMP-04, FR-TEXT-02, FR-TEXT-03, §5.3, plan note N53), which package
-- dependency keeps and answers the usage queries from: a row of each thing
-- that a site names, and how it uses it. A site is an expression of the
-- project, or the rich text of a node, a connection, or a component: a
-- node's body, a connection's label, or a component's description.
--
-- An expression's rows are what its AST names, which package expression
-- writes as it compiles the expression (dependency.IndexExpressions): each
-- variable and each runtime attribute that it reads or writes, an
-- assignment's target written, and read too when the assignment is one of
-- +=, -=, *=, or /=, which reads the value that it updates, and each node
-- that it names in a node reference, read. An expression that does not
-- compile has no AST, and so no rows: what it names it names by its keys
-- alone, which resolve to nothing.
--
-- A text's rows are what its value references and mentions name, read:
-- each variable or runtime attribute that a value reference shows, and each
-- component that a mention shows by its name, which the derivation that
-- package dependency registers with the pipeline writes once every command
-- of a batch has applied, for each node, connection, and component whose
-- text the batch may have changed. A text names what it names by its ID,
-- whatever becomes of it, so its rows stay while what it names is in the
-- trash, or gone.
--
-- An attribute is named by the pair of its component, target_id, and its
-- definition, attribute_def_id, so that elara.trust and mira.trust, of one
-- definition, are different targets, as are elara.trust and elara.mood.
-- The rows of a site are its own whether it is in the trash or not, and the
-- usage queries pass over a site in the trash, as everything that reads the
-- content does, so that neither a deletion to the trash nor a restoration
-- changes them; the purge of a site deletes them, by the cascade of its
-- foreign key.
CREATE TABLE expression_ref (
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	-- The site, exactly one of them: an expression, or the text of a node, a
	-- connection, or a component.
	expression_id uuid,
	node_id uuid,
	connection_id uuid,
	component_id uuid,
	-- The board of the site, which never changes: an expression's, a node's,
	-- or a connection's, and none for a component's description.
	board_id uuid,
	-- What the site names: a variable, an attribute of a component, by the
	-- component and the definition, a node, or a component that a text
	-- mentions.
	target_kind text NOT NULL CHECK (target_kind IN ('variable', 'attribute', 'node', 'component')),
	target_id uuid NOT NULL,
	attribute_def_id uuid,
	access text NOT NULL CHECK (access IN ('read', 'write')),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, expression_id) REFERENCES expression (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, board_id, node_id) REFERENCES node (workspace_id, project_id, board_id, id)
		ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, board_id, connection_id)
		REFERENCES connection (workspace_id, project_id, board_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, component_id) REFERENCES component (workspace_id, project_id, id)
		ON DELETE CASCADE,
	CHECK (num_nonnulls(expression_id, node_id, connection_id, component_id) = 1),
	CHECK ((board_id IS NULL) = (component_id IS NOT NULL)),
	CHECK ((target_kind = 'attribute') = (attribute_def_id IS NOT NULL)),
	-- Only an expression writes, and only a variable or an attribute; only an
	-- expression names a node, and only a text mentions a component.
	CHECK (access = 'read' OR expression_id IS NOT NULL AND target_kind IN ('variable', 'attribute')),
	CHECK (target_kind <> 'node' OR expression_id IS NOT NULL),
	CHECK (target_kind <> 'component' OR expression_id IS NULL),
	-- A site names a target in a way once, however often its source or its
	-- text names it. The key is the index by which the rows of an expression
	-- are replaced, and the cascade of its deletion finds them.
	UNIQUE NULLS NOT DISTINCT (workspace_id, expression_id, node_id, connection_id, component_id, target_kind,
		target_id, attribute_def_id, access)
);
-- The sites that name each target, by which the usage queries read them.
CREATE INDEX expression_ref_target ON expression_ref (workspace_id, project_id, target_id, access);
-- The rows of each text, which the derivation replaces, and the cascade of
-- its owner's purge deletes.
CREATE INDEX expression_ref_node ON expression_ref (workspace_id, node_id) WHERE node_id IS NOT NULL;
CREATE INDEX expression_ref_connection ON expression_ref (workspace_id, connection_id) WHERE connection_id IS NOT NULL;
CREATE INDEX expression_ref_component ON expression_ref (workspace_id, component_id) WHERE component_id IS NOT NULL;

ALTER TABLE expression_ref ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON expression_ref TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The application replaces a site's rows, by deleting them and adding them
-- anew, and the cascades of the sites' purges delete them.
GRANT SELECT, INSERT, DELETE ON expression_ref TO storycreep_app;
GRANT SELECT ON expression_ref TO storycreep_maintenance;

-- The rows of the content that the migrations before this one left, as
-- package dependency gives them. ASTs and texts name what they name by
-- public IDs (PRD §5.2), a prefix, an underscore, and the UUID's 128 bits in
-- 26 characters of Crockford's base32, the most significant first, which
-- public_uuid decodes: the 130 bits of the characters, less the two zero
-- bits that lead them, four at a time. Every ID that the content holds is
-- one in its canonical spelling, in upper case, as the commands wrote it.
CREATE FUNCTION pg_temp.public_uuid(id text) RETURNS uuid LANGUAGE sql IMMUTABLE STRICT AS $$
	SELECT string_agg(to_hex(substring(b.bits FROM 4 * i + 1 FOR 4)::bit(4)::integer), '' ORDER BY i)::uuid
	FROM (SELECT substring(string_agg((strpos('0123456789ABCDEFGHJKMNPQRSTVWXYZ', t.c) - 1)::bit(5)::text, ''
			ORDER BY t.n) FROM 3) AS bits
		FROM regexp_split_to_table(substring(id FROM '_(.*)$'), '') WITH ORDINALITY AS t (c, n)) b,
		generate_series(0, 31) AS i
$$;

-- What each expression that compiles names, each AST node of a name with
-- how the expression uses it: a condition reads every name that it holds; a
-- script writes the target of each of its assignments, and reads it too
-- when the assignment updates it, and reads every name in an assignment's
-- value and in a call's arguments. jsonpath's strict .** reaches the node
-- that it starts at and each node below it once, and a key that an AST
-- lacks, as a script without statements, finds nothing, silently.
WITH named AS (
	SELECT e.workspace_id, e.project_id, e.id, e.board_id, n.node, 'read' AS access
	FROM expression e,
		jsonb_path_query(e.ast, 'strict $.**?(@.kind == "var" || @.kind == "attr" || @.kind == "node_ref")', '{}',
			true) AS n (node)
	WHERE e.ast IS NOT NULL AND e.context = 'condition'
	UNION ALL
	SELECT e.workspace_id, e.project_id, e.id, e.board_id, n.node, 'write' FROM expression e,
		jsonb_path_query(e.ast, 'strict $.statements[*]?(@.kind == "assign").target', '{}', true) AS n (node)
	WHERE e.ast IS NOT NULL AND e.context = 'script'
	UNION ALL
	SELECT e.workspace_id, e.project_id, e.id, e.board_id, n.node, 'read' FROM expression e,
		jsonb_path_query(e.ast, 'strict $.statements[*]?(@.kind == "assign" && @.op != "=").target', '{}',
			true) AS n (node)
	WHERE e.ast IS NOT NULL AND e.context = 'script'
	UNION ALL
	SELECT e.workspace_id, e.project_id, e.id, e.board_id, n.node, 'read' FROM expression e,
		jsonb_path_query(e.ast, 'strict $.statements[*]?(@.kind == "assign").value.**?(@.kind == "var" || '
			'@.kind == "attr" || @.kind == "node_ref")', '{}', true) AS n (node)
	WHERE e.ast IS NOT NULL AND e.context = 'script'
	UNION ALL
	SELECT e.workspace_id, e.project_id, e.id, e.board_id, n.node, 'read' FROM expression e,
		jsonb_path_query(e.ast, 'strict $.statements[*]?(@.kind == "call").args[*].**?(@.kind == "var" || '
			'@.kind == "attr" || @.kind == "node_ref")', '{}', true) AS n (node)
	WHERE e.ast IS NOT NULL AND e.context = 'script'
)
INSERT INTO expression_ref (workspace_id, project_id, expression_id, board_id, target_kind, target_id,
	attribute_def_id, access)
SELECT DISTINCT workspace_id, project_id, id, board_id,
	CASE node->>'kind' WHEN 'var' THEN 'variable' WHEN 'attr' THEN 'attribute' ELSE 'node' END,
	pg_temp.public_uuid(node->>'ref'), pg_temp.public_uuid(node->>'attribute'), access
FROM named;

-- What each text names: the variable, or the component and the attribute,
-- of each value reference, and the component of each mention, read.
WITH named AS (
	SELECT n.workspace_id, n.project_id, n.id AS node_id, NULL::uuid AS connection_id, NULL::uuid AS component_id,
		n.board_id, r.inline
	FROM node n, jsonb_path_query(n.text, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
	WHERE n.text IS NOT NULL
	UNION ALL
	SELECT c.workspace_id, c.project_id, NULL, c.id, NULL, c.board_id, r.inline
	FROM connection c,
		jsonb_path_query(c.label, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
	UNION ALL
	SELECT c.workspace_id, c.project_id, NULL, NULL, c.id, NULL, r.inline
	FROM component c,
		jsonb_path_query(c.description, 'strict $.**?(@.type == "valueRef" || @.type == "mention")') AS r (inline)
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
