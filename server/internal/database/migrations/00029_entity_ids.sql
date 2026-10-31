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

-- Another instance of the server may serve while this one migrates (PRD
-- FR-ADMIN-04), so no session changes the content's tables from here until
-- the migration commits: a row that one committed after the backfill's
-- snapshot, and before its table's trigger, would hold no UUID here, and a
-- row that one purged then would keep its UUID held for good. The locks are
-- taken in the order in which those sessions take theirs, so that none of
-- them holds a lock that this one waits for while it waits for one that this
-- one holds, which would deadlock the two and fail one of them. First the
-- workspaces', in the mode that CREATE TABLE takes below of a table that its
-- foreign keys refer to, which waits for a workspace's insert or update, and
-- lets the sessions that lock a workspace's row or check a foreign key go
-- on. Then the projects', in EXCLUSIVE mode, which waits for each session
-- that has locked a project's row, as a batch of commands and the trash's
-- purge do before they change the content (LockProjectSeq,
-- LockProjectToPurge), or written one, as a project's creation does before
-- it seeds its content, and its purge before the cascade deletes it, and
-- keeps new ones from starting. Only then the content's, in the mode that
-- CREATE TRIGGER takes below, which no session that changes the content
-- holds by then. A table of the content locked before the projects' would
-- wait for a batch that had written it, which would then wait for this one
-- to set its project's seq, or to write a table locked before it, as a batch
-- that creates a node and then a folder does.
LOCK TABLE workspace IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE project IN EXCLUSIVE MODE;
LOCK TABLE folder, board, node, branch_output, connection, variable, component_type, attribute_def, component,
	expression IN SHARE ROW EXCLUSIVE MODE;

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

-- The triggers run as the role of the statement that inserts or deletes the
-- content, and those of the rows that a foreign key's cascade deletes do
-- too, though the cascade deletes the rows as the tables' owner: they fire
-- with the statement that deleted the row that the foreign key refers to.
-- That role is the application's, whose commands, and the trash's and the
-- projects' purges, in their transactions of a workspace, add and delete the
-- UUIDs, which it reads to delete them. A job that deletes content as another
-- role, even only by the cascade of a workspace's deletion, as the
-- maintenance path's will (plan item 110), needs DELETE on this table too,
-- which its migration grants.
GRANT SELECT, INSERT, DELETE ON entity_id TO storycreep_app;
GRANT SELECT ON entity_id TO storycreep_maintenance;

-- The UUIDs of the content that the migrations before this one left, a kind
-- at a time. Two entities of different kinds that hold one UUID already,
-- which nothing refused until now, keep it: the one whose kind comes first
-- here holds it, and the other is left as it is, so that the migration
-- applies to every database. Deleting the other leaves the UUID held, but
-- deleting the one that holds it frees it, though the other has it still,
-- since no row here names the other. Only a database that a client gave such
-- IDs on purpose has any, and the others are the rows of the content whose
-- UUIDs this table holds of another kind.
INSERT INTO entity_id (id, workspace_id, project_id, kind)
	SELECT id, workspace_id, project_id, 'folder' FROM folder ON CONFLICT DO NOTHING;
INSERT INTO entity_id (id, workspace_id, project_id, kind)
	SELECT id, workspace_id, project_id, 'board' FROM board ON CONFLICT DO NOTHING;
INSERT INTO entity_id (id, workspace_id, project_id, kind)
	SELECT id, workspace_id, project_id, 'node' FROM node ON CONFLICT DO NOTHING;
INSERT INTO entity_id (id, workspace_id, project_id, kind)
	SELECT id, workspace_id, project_id, 'branch_output' FROM branch_output ON CONFLICT DO NOTHING;
INSERT INTO entity_id (id, workspace_id, project_id, kind)
	SELECT id, workspace_id, project_id, 'connection' FROM connection ON CONFLICT DO NOTHING;
INSERT INTO entity_id (id, workspace_id, project_id, kind)
	SELECT id, workspace_id, project_id, 'variable' FROM variable ON CONFLICT DO NOTHING;
INSERT INTO entity_id (id, workspace_id, project_id, kind)
	SELECT id, workspace_id, project_id, 'component_type' FROM component_type ON CONFLICT DO NOTHING;
INSERT INTO entity_id (id, workspace_id, project_id, kind)
	SELECT id, workspace_id, project_id, 'attribute_def' FROM attribute_def ON CONFLICT DO NOTHING;
INSERT INTO entity_id (id, workspace_id, project_id, kind)
	SELECT id, workspace_id, project_id, 'component' FROM component ON CONFLICT DO NOTHING;
INSERT INTO entity_id (id, workspace_id, project_id, kind)
	SELECT id, workspace_id, project_id, 'expression' FROM expression ON CONFLICT DO NOTHING;

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
-- migration does. The rows of a deletion's cascades are among those of its
-- statement, so that a statement fires each table's trigger once. It looks
-- each UUID up by entity_id's primary key, however many rows the statement
-- deleted: a session keeps the plan of the function's statement, and the
-- planner reads the size of old_rows as the session first runs it, and
-- again only once an analysis of entity_id has it plan anew, so a join of
-- old_rows would keep the plan made for the first statement's rows,
-- which, after a purge of thousands, as of a project or a board, is
-- a hash join that reads the whole of entity_id, or of the workspace's part
-- of it, for each later statement of the session that deletes a row, as
-- the trash's purge does while it holds its project's lock.
-- +goose StatementBegin
CREATE FUNCTION entity_id_delete() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
	DELETE FROM public.entity_id e WHERE e.id = ANY (ARRAY(SELECT o.id FROM old_rows o)) AND e.kind = TG_ARGV[0];
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
