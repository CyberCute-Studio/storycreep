-- +goose Up

-- The writing statistics (PRD FR-TEXT-06, plan note N76), which package
-- statistics keeps and answers: the words and the characters of each text of
-- a project, an element's text or a connection's label, as package richtext
-- counts them (WordCount, CharacterCount), with the names of what the text
-- mentions and shows, so that the statistics and the text export count with
-- one implementation (FR-TEXT-05 AC3). A read adds them up, of the nodes and
-- the connections that are not in the trash, by board and for the project,
-- with the counts of the nodes, the choices, and the conditions, which it
-- reads of the content itself, so that a text is counted once, when it
-- changes, rather than at each read: the statistics follow an edit at once,
-- without a rescan of the project (AC1).

-- The words and the characters of a node's text, or of a connection's label,
-- of the project: 0 of each for a branch or a jumper, which has no text. Each
-- node and each connection has its row whether it is in the trash or not,
-- and a read adds up those of the entities that are not, so that the trash
-- changes no row. The pipeline's derivation writes the rows of the texts
-- that a batch changes, in the batch's transaction, and of the texts that
-- name what it renames, whose names they count, and the job of the
-- statistics (text_statistics) the rows of the entities that have none of
-- the current version, which package statistics names: of the content that is
-- there already when this migration applies, of a text that names what the
-- trash's purge deleted, which it marks to be counted again, and of every
-- text again once a version of the server changes how a text is counted. Two
-- entities of different kinds may share a UUID, so a row is of its kind and
-- its entity's UUID, and its entity's own column, which the kind and the UUID
-- give, holds the foreign key that deletes it with its entity, by the
-- cascade of the entity's purge, as search_entry's does.
CREATE TABLE text_statistic (
	id uuid PRIMARY KEY DEFAULT uuid_v7(),
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	kind text NOT NULL CHECK (kind IN ('node', 'connection')),
	entity_id uuid NOT NULL,
	node_id uuid GENERATED ALWAYS AS (CASE WHEN kind = 'node' THEN entity_id END) STORED,
	connection_id uuid GENERATED ALWAYS AS (CASE WHEN kind = 'connection' THEN entity_id END) STORED,
	words integer NOT NULL CHECK (words >= 0),
	characters integer NOT NULL CHECK (characters >= 0),
	-- The version of how a text is counted that the server wrote the row of
	-- (statistics.Version), or 0 for a row that is to be counted again, as
	-- one of a text that names what the trash's purge deleted.
	version integer NOT NULL CHECK (version >= 0),
	UNIQUE (workspace_id, project_id, kind, entity_id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, node_id) REFERENCES node (workspace_id, project_id, id)
		ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, connection_id) REFERENCES connection (workspace_id, project_id, id)
		ON DELETE CASCADE
);
-- A read finds the rows of the project's nodes and connections by the unique
-- index above. These are the indexes of the cascades of the entities' purges.
CREATE INDEX text_statistic_node ON text_statistic (workspace_id, project_id, node_id) WHERE node_id IS NOT NULL;
CREATE INDEX text_statistic_connection ON text_statistic (workspace_id, project_id, connection_id)
	WHERE connection_id IS NOT NULL;

-- The version of how a text is counted of which the job of the statistics
-- has written the row of every node and connection of a project that had
-- none, so that its later runs pass over the project until a version of the
-- server changes how a text is counted. A project without a row has not been
-- counted whole yet, as one that this migration finds, or one whose purge
-- marked a text to be counted again, or a replica of an older version of the
-- server has written a row of it since a newer one recorded it, as during a
-- rolling upgrade, which deletes the row (UpsertTextStatistics), so that the
-- newer job writes it again; a read says that the statistics of such a
-- project are not complete.
CREATE TABLE text_statistic_state (
	id uuid PRIMARY KEY DEFAULT uuid_v7(),
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	version integer NOT NULL CHECK (version >= 1),
	UNIQUE (workspace_id, project_id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE text_statistic ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON text_statistic TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);
ALTER TABLE text_statistic_state ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON text_statistic_state TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The application writes the rows and the projects' versions, and the
-- cascades of the entities' and the projects' purges delete them.
GRANT SELECT, INSERT, UPDATE, DELETE ON text_statistic, text_statistic_state TO storycreep_app;
GRANT SELECT ON text_statistic, text_statistic_state TO storycreep_maintenance;
