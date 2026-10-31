-- +goose Up

-- The UUIDs of the entities of the projects' content, each with its kind, so
-- that no two of them hold one UUID, whatever their kinds (PRD §5.2, §5.3,
-- plan note N57). A command takes the ID of each entity that it creates from
-- its client, and each kind's table keeps its own IDs apart from its
-- others' by its primary key, but nothing kept a node from taking a
-- connection's UUID, or an expression's, which the server makes, and which
-- the cursors of the usage lists show: the dependency index names its sites
-- by their UUIDs, and told such two apart by no more than their columns.
-- This table's primary key keeps them apart: a trigger of each kind's table
-- adds the UUIDs of the rows that a statement inserts, and the insert fails
-- on entity_id_pkey when one of them is here already, of any kind, in any
-- workspace, as the kind's own primary key fails for an ID of its own kind,
-- which the commands refuse at the payload's id alike; and a trigger deletes
-- the UUIDs of the rows that a statement deletes for good, as the trash's
-- purge does, so that an ID is free again once its entity is gone, as before.
-- An entity in the trash keeps its UUID. The rows hold no content, and are
-- derived from the content's tables, so the authoring format carries none of
-- them: an import inserts the content, whose triggers add its UUIDs.
CREATE TABLE entity_id (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	-- The table of the entity, whose name its kind is.
	kind text NOT NULL CHECK (kind IN ('folder', 'board', 'node', 'branch_output', 'connection', 'variable',
		'component_type', 'attribute_def', 'component', 'expression')),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE
);
-- The UUIDs of each project's content, which the cascade of a project's
-- deletion finds.
CREATE INDEX entity_id_project ON entity_id (workspace_id, project_id);

ALTER TABLE entity_id ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON entity_id TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The triggers run as whoever inserts or deletes the content, as the
-- application does, and the purge in its transactions of a workspace, so the
-- application adds and deletes the UUIDs, which it reads to delete them. The
-- cascade of a project's or a workspace's deletion runs as the tables' owner.
GRANT SELECT, INSERT, DELETE ON entity_id TO storycreep_app;
GRANT SELECT ON entity_id TO storycreep_maintenance;

-- The UUIDs of the content that the migrations before this one left. Two
-- entities of different kinds that hold one UUID already, which nothing
-- refused until now, keep it: one of them holds it here, and the other is
-- left as it is, so that the migration applies to every database, and
-- deleting the other leaves the UUID held. Only a database that a client gave
-- such IDs on purpose has any.
INSERT INTO entity_id (id, workspace_id, project_id, kind)
	SELECT id, workspace_id, project_id, 'folder' FROM folder
	UNION ALL SELECT id, workspace_id, project_id, 'board' FROM board
	UNION ALL SELECT id, workspace_id, project_id, 'node' FROM node
	UNION ALL SELECT id, workspace_id, project_id, 'branch_output' FROM branch_output
	UNION ALL SELECT id, workspace_id, project_id, 'connection' FROM connection
	UNION ALL SELECT id, workspace_id, project_id, 'variable' FROM variable
	UNION ALL SELECT id, workspace_id, project_id, 'component_type' FROM component_type
	UNION ALL SELECT id, workspace_id, project_id, 'attribute_def' FROM attribute_def
	UNION ALL SELECT id, workspace_id, project_id, 'component' FROM component
	UNION ALL SELECT id, workspace_id, project_id, 'expression' FROM expression
ON CONFLICT DO NOTHING;

-- entity_id_insert adds the UUIDs of the rows that a statement inserted into
-- the table of the kind of its trigger's argument, once per statement, as a
-- batch inserts many rows at once; a UUID that is here already fails the
-- statement on entity_id_pkey.
-- +goose StatementBegin
CREATE FUNCTION entity_id_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
	INSERT INTO public.entity_id (id, workspace_id, project_id, kind)
		SELECT n.id, n.workspace_id, n.project_id, TG_ARGV[0] FROM new_rows n;
	RETURN NULL;
END
$$;
-- +goose StatementEnd

-- entity_id_delete deletes the UUIDs of the rows that a statement deleted
-- from the table of the kind of its trigger's argument, but those that
-- another kind holds, as one of two entities that held a UUID before this
-- migration does.
-- +goose StatementBegin
CREATE FUNCTION entity_id_delete() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
	DELETE FROM public.entity_id e USING old_rows o WHERE e.id = o.id AND e.kind = TG_ARGV[0];
	RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER entity_id_insert AFTER INSERT ON folder REFERENCING NEW TABLE AS new_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_insert('folder');
CREATE TRIGGER entity_id_delete AFTER DELETE ON folder REFERENCING OLD TABLE AS old_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_delete('folder');
CREATE TRIGGER entity_id_insert AFTER INSERT ON board REFERENCING NEW TABLE AS new_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_insert('board');
CREATE TRIGGER entity_id_delete AFTER DELETE ON board REFERENCING OLD TABLE AS old_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_delete('board');
CREATE TRIGGER entity_id_insert AFTER INSERT ON node REFERENCING NEW TABLE AS new_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_insert('node');
CREATE TRIGGER entity_id_delete AFTER DELETE ON node REFERENCING OLD TABLE AS old_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_delete('node');
CREATE TRIGGER entity_id_insert AFTER INSERT ON branch_output REFERENCING NEW TABLE AS new_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_insert('branch_output');
CREATE TRIGGER entity_id_delete AFTER DELETE ON branch_output REFERENCING OLD TABLE AS old_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_delete('branch_output');
CREATE TRIGGER entity_id_insert AFTER INSERT ON connection REFERENCING NEW TABLE AS new_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_insert('connection');
CREATE TRIGGER entity_id_delete AFTER DELETE ON connection REFERENCING OLD TABLE AS old_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_delete('connection');
CREATE TRIGGER entity_id_insert AFTER INSERT ON variable REFERENCING NEW TABLE AS new_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_insert('variable');
CREATE TRIGGER entity_id_delete AFTER DELETE ON variable REFERENCING OLD TABLE AS old_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_delete('variable');
CREATE TRIGGER entity_id_insert AFTER INSERT ON component_type REFERENCING NEW TABLE AS new_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_insert('component_type');
CREATE TRIGGER entity_id_delete AFTER DELETE ON component_type REFERENCING OLD TABLE AS old_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_delete('component_type');
CREATE TRIGGER entity_id_insert AFTER INSERT ON attribute_def REFERENCING NEW TABLE AS new_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_insert('attribute_def');
CREATE TRIGGER entity_id_delete AFTER DELETE ON attribute_def REFERENCING OLD TABLE AS old_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_delete('attribute_def');
CREATE TRIGGER entity_id_insert AFTER INSERT ON component REFERENCING NEW TABLE AS new_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_insert('component');
CREATE TRIGGER entity_id_delete AFTER DELETE ON component REFERENCING OLD TABLE AS old_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_delete('component');
CREATE TRIGGER entity_id_insert AFTER INSERT ON expression REFERENCING NEW TABLE AS new_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_insert('expression');
CREATE TRIGGER entity_id_delete AFTER DELETE ON expression REFERENCING OLD TABLE AS old_rows
	FOR EACH STATEMENT EXECUTE FUNCTION entity_id_delete('expression');
