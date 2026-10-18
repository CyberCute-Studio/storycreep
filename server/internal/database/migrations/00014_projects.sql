-- +goose Up

-- The project lifecycle (PRD FR-PROJ-01, §5.3): the columns of the project
-- that the tenancy migration left a shell of (00004). A project is created
-- by package project, which checks the workspace's projects limit (FR-ENT-01),
-- and every later change of it is a command of the pipeline (package
-- commands), which increments its rev (§5.7, ADR-006): renaming, describing,
-- changing its settings, archiving, unarchiving, deleting it to the trash,
-- and restoring it.
--
-- An archived project is read-only: the pipeline refuses every command of it
-- but those that unarchive, delete, or restore it (FR-PROJ-01 AC2). A deleted
-- project is in the trash until hard_delete_after, 30 days after its
-- deletion, and the API answers it with 404, as a project that does not
-- exist, except to restore it (AC3). Once hard_delete_after has passed, the
-- purge job deletes its row, and every row that refers to it with it, by the
-- cascade of their foreign keys to it, which every table with a project_id has
-- (a test of package database checks that). Neither an archived project nor
-- a deleted one counts towards the workspace's projects (FR-PROJ-01).
ALTER TABLE project
	-- Each new column that is NOT NULL takes its default for the rows that
	-- exist, and keeps it, but name, which the server always sets, and which
	-- has no default once they have theirs.
	ADD COLUMN name text NOT NULL DEFAULT 'Project',
	ADD COLUMN description text NOT NULL DEFAULT '',
	-- The project's settings (package project): its default locale, the
	-- least role that may publish its snapshots (publish_min_role,
	-- FR-SNAP-01 AC3), and its entry board and node.
	ADD COLUMN settings jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(settings) = 'object'),
	-- Incremented by every command that changes the project.
	ADD COLUMN rev bigint NOT NULL DEFAULT 1 CHECK (rev >= 1),
	ADD COLUMN archived_at timestamptz,
	ADD COLUMN deleted_at timestamptz,
	ADD COLUMN hard_delete_after timestamptz,
	-- The users who created the project and changed it last, or null once
	-- their accounts are deleted.
	ADD COLUMN created_by uuid REFERENCES "user" ON DELETE SET NULL,
	ADD COLUMN updated_by uuid REFERENCES "user" ON DELETE SET NULL,
	-- A project in the trash has both, and any other neither.
	ADD CONSTRAINT project_trash CHECK ((deleted_at IS NULL) = (hard_delete_after IS NULL));
ALTER TABLE project ALTER COLUMN name DROP DEFAULT;
CREATE INDEX project_created_by ON project (created_by);
CREATE INDEX project_updated_by ON project (updated_by);

-- The projects that exist already count towards their workspaces' projects
-- (FR-ENT-01), as each that is created from now on does: each workspace's are
-- recorded as one delta, which the first rollup folds, with an ID made as
-- 00013 makes one. None is archived or deleted yet.
INSERT INTO usage_delta (id, workspace_id, key, delta)
SELECT
	encode(set_bit(set_bit(overlay(uuid_send(gen_random_uuid())
		PLACING substring(int8send(floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint) FROM 3)
		FROM 1 FOR 6), 52, 1), 53, 1), 'hex')::uuid,
	workspace_id, 'projects', count(*)
FROM project
GROUP BY workspace_id;
