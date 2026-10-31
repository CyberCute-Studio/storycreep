-- +goose Up

-- Project search (PRD FR-PROJ-03, plan note N57), which package search keeps
-- and answers: PostgreSQL's full-text search over a document of each node,
-- connection, variable, and component of a project, compared without regard
-- to case or diacritics.

-- Diacritics are compared away with the unaccent extension's dictionary,
-- which the search configuration below names in the public schema. CREATE
-- EXTENSION IF NOT EXISTS keeps an extension that the database has in another
-- schema already, as a template or a hosting provider can give it, and the
-- configuration would then fail with an error that the dictionary does not
-- exist, right after a CREATE EXTENSION that succeeded. So the migration
-- refuses such a database with an error that says how to move the extension,
-- as 00004_tenancy.sql does for citext.
-- +goose StatementBegin
DO $$
DECLARE
	ext_schema name;
BEGIN
	SELECT n.nspname INTO ext_schema
	FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace
	WHERE e.extname = 'unaccent';
	IF ext_schema <> 'public' THEN
		RAISE EXCEPTION USING MESSAGE = format('the database has the unaccent extension in the schema %I, where '
			|| 'Storycreep does not find it; move it to the public schema with ALTER EXTENSION unaccent SET SCHEMA '
			|| 'public, as a superuser, and migrate again', ext_schema);
	END IF;
END
$$;
-- +goose StatementEnd
CREATE EXTENSION IF NOT EXISTS unaccent WITH SCHEMA public;

-- The configuration that the documents and the queries are read with: the
-- simple configuration, which lowercases each word and keeps it whole, since
-- a project's text may be of any language and PostgreSQL has no stemmer for
-- many, Czech among them, with the words that may hold letters other than
-- ASCII's passed through unaccent first, so that "Žluťoučký" is read as
-- "zlutoucky" (FR-PROJ-03 AC4). A query matches the start of a word (package
-- search), which finds most of a word's inflections without a stemmer.
CREATE TEXT SEARCH CONFIGURATION public.storycreep_search (COPY = pg_catalog.simple);
ALTER TEXT SEARCH CONFIGURATION public.storycreep_search
	ALTER MAPPING FOR word, numword, hword, numhword, hword_part, hword_numpart
	WITH public.unaccent, pg_catalog.simple;

-- The document of a node, a connection, a variable, or a component of a
-- project, which the search reads: its title, a node's, a variable's key, or
-- a component's name, and its body, a node's text, a connection's label, a
-- variable's description, or a component's key, description, and values of
-- text, as package search builds them, and the vector of their words, the
-- title's of weight A and the body's of weight B, so that a match in a title
-- ranks above one in a body. Each entity has its document whether it is in
-- the trash or not, and the search reads only those of the entities that are
-- not, so that the trash changes no document. The pipeline's derivation
-- writes the documents of what a batch changes, in the batch's transaction,
-- and the job of the index (search_reindex) those of the entities that have
-- none of the current version, which package search names: the content that
-- is there already when this migration applies, and every document again
-- once a version of the server changes what a document holds. Two entities
-- of different kinds may share a UUID, so a document is of its kind and its
-- entity's UUID, which no other document of the project is of, and its
-- entity's own column, which the kind and the UUID give, holds the foreign
-- key that deletes it with its entity, by the cascade of the entity's purge.
CREATE TABLE search_entry (
	id uuid PRIMARY KEY DEFAULT uuid_v7(),
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	kind text NOT NULL CHECK (kind IN ('node', 'connection', 'variable', 'component')),
	entity_id uuid NOT NULL,
	node_id uuid GENERATED ALWAYS AS (CASE WHEN kind = 'node' THEN entity_id END) STORED,
	connection_id uuid GENERATED ALWAYS AS (CASE WHEN kind = 'connection' THEN entity_id END) STORED,
	variable_id uuid GENERATED ALWAYS AS (CASE WHEN kind = 'variable' THEN entity_id END) STORED,
	component_id uuid GENERATED ALWAYS AS (CASE WHEN kind = 'component' THEN entity_id END) STORED,
	-- At most as long as package search makes them (MaxTitleLength,
	-- MaxBodyLength), so that a document's vector stays below the megabyte
	-- that a tsvector holds at most: change both at once.
	title text NOT NULL CHECK (length(title) <= 200),
	body text NOT NULL CHECK (length(body) <= 100000),
	vector tsvector NOT NULL GENERATED ALWAYS AS (
		setweight(to_tsvector('public.storycreep_search'::regconfig, title), 'A')
		|| setweight(to_tsvector('public.storycreep_search'::regconfig, body), 'B')
	) STORED,
	-- The version of what a document holds that the server wrote it of
	-- (search.Version), or 0 for a document that a migration marked to be
	-- written again, as one that changes the search configuration does.
	version integer NOT NULL CHECK (version >= 0),
	UNIQUE (workspace_id, project_id, kind, entity_id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, node_id) REFERENCES node (workspace_id, project_id, id)
		ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, connection_id) REFERENCES connection (workspace_id, project_id, id)
		ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, variable_id) REFERENCES variable (workspace_id, project_id, id)
		ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, component_id) REFERENCES component (workspace_id, project_id, id)
		ON DELETE CASCADE
);
-- The words of the documents, which a search looks its query's words up in.
CREATE INDEX search_entry_vector ON search_entry USING gin (vector);
-- The cascades of the entities' purges.
CREATE INDEX search_entry_node ON search_entry (workspace_id, project_id, node_id) WHERE node_id IS NOT NULL;
CREATE INDEX search_entry_connection ON search_entry (workspace_id, project_id, connection_id)
	WHERE connection_id IS NOT NULL;
CREATE INDEX search_entry_variable ON search_entry (workspace_id, project_id, variable_id)
	WHERE variable_id IS NOT NULL;
CREATE INDEX search_entry_component ON search_entry (workspace_id, project_id, component_id)
	WHERE component_id IS NOT NULL;

-- The version of what a document holds of which the job of the index has
-- written the document of every entity of a project that had none, so that
-- its later runs pass over the project until a version of the server changes
-- what a document holds. A project without a row has not been indexed whole
-- yet, as one that this migration finds, whose documents the job writes.
CREATE TABLE search_state (
	id uuid PRIMARY KEY DEFAULT uuid_v7(),
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	version integer NOT NULL CHECK (version >= 1),
	UNIQUE (workspace_id, project_id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE search_entry ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON search_entry TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);
ALTER TABLE search_state ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON search_state TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The application writes the documents and the projects' versions, and the
-- cascades of the entities' and the projects' purges delete them.
GRANT SELECT, INSERT, UPDATE, DELETE ON search_entry, search_state TO storycreep_app;
GRANT SELECT ON search_entry, search_state TO storycreep_maintenance;
