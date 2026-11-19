-- +goose Up

-- The migration log of the projects (PRD FR-DATA-03 AC2, §6.4, plan note
-- N84), which package schemaversion keeps: what a migration of a project's
-- stored content, a snapshot's files, to the next version of the published
-- format could not carry forward as it was, since that version cannot hold it,
-- so that a migration never loses a value silently. A snapshot's step from a
-- version is logged once, with what it reported, which may be nothing, in the
-- transaction that stores what the migration wrote: the server stores content
-- at the current version (§6.4), so the snapshot then holds no files of the
-- step's version for a later run to migrate again.
--
-- A row is of a snapshot of its project, which plan item 78 stores, and which
-- that item's migration gives the foreign key to its snapshot that deletes
-- the row with it. The log is no part of the project's content, which no
-- command changes and the trash does not list; the purges of its project and
-- of its workspace delete it by the cascades. It holds nothing of anyone's
-- account: an entry names a file, an entity, a field, and the field's values,
-- of a snapshot, which holds no personal data (§6.2).
CREATE TABLE schema_migration_log (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	snapshot_id uuid NOT NULL,
	-- The step: from a version of the published format to the next.
	from_version integer NOT NULL CHECK (from_version >= 1),
	to_version integer NOT NULL CHECK (to_version = from_version + 1),
	-- What the step reported, as package schemaversion's entries write it:
	-- each value that it did not carry forward as it was, with its file, its
	-- entity, its field, and its value before the step and after it, which an
	-- entry of a dropped value has none of.
	entries jsonb NOT NULL CHECK (jsonb_typeof(entries) = 'array'),
	-- When the step was logged, as the server's clock has it.
	created_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	-- A snapshot's step from a version is logged once. The project's log is
	-- read in this order, each snapshot's steps the oldest first.
	UNIQUE (workspace_id, project_id, snapshot_id, from_version),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE schema_migration_log ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON schema_migration_log TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The application logs the steps, and reads them; the cascades alone delete
-- them.
GRANT SELECT, INSERT ON schema_migration_log TO storycreep_app;
GRANT SELECT ON schema_migration_log TO storycreep_maintenance;
