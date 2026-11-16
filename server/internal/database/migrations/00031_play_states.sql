-- +goose Up

-- The saved play states (PRD FR-PLAY-05, §5.3, plan note N80), which package
-- playstate keeps: named states of play mode's flow, as "after act 1" or
-- "romance path", saved by a project's authors, which every member who reads
-- the project sees and loads as a starting point. Every change of a play
-- state is a command of the pipeline (package commands), which increments its
-- rev (§5.7, ADR-006), so that the page's undo takes a change back as it
-- takes any (FR-EDIT-08).

-- A play state of a project: its name, which people are shown, and its
-- state, a save state of the project's flow (§8.5, schema/save-state/v1), as
-- a runtime's save_state() returns it, an object whose values and cursor are
-- by ID, never by key, so that a state saved before the draft changed loads
-- with what play mode reads of what changed since (FR-PLAY-05 AC2), and the
-- names of the state's values, as play mode named them when it saved the
-- state, a variable by its key and an attribute as its component's key and
-- its own, which play mode names a value that a load drops by, once its
-- entity is deleted and has no name of its own. Its name need not be unique:
-- the state is told apart by who saved it and when. A deleted play state is
-- in the project's trash, unlisted, with deleted_at, from which the undo of
-- its deletion restores it, and which purges it once 30 days have passed
-- (package trash).
CREATE TABLE play_state (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	-- At most 100 characters, as the commands' payloads take (package
	-- playstate, MaxNameLength): change both at once.
	name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
	state jsonb NOT NULL CHECK (jsonb_typeof(state) = 'object'),
	names jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(names) = 'object'),
	-- Incremented by every command that changes the play state.
	rev bigint NOT NULL DEFAULT 1 CHECK (rev >= 1),
	deleted_at timestamptz,
	-- The users who saved the play state and changed it last, or null once
	-- their accounts are deleted.
	created_by uuid REFERENCES "user" ON DELETE SET NULL,
	updated_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE
);
-- A project's play states, by their IDs, which its list reads in their order.
CREATE INDEX play_state_project ON play_state (workspace_id, project_id, id);
CREATE INDEX play_state_created_by ON play_state (created_by);
CREATE INDEX play_state_updated_by ON play_state (updated_by);
-- The play states of each project's trash, by when they were deleted, which
-- the trash's purge reads, oldest first.
CREATE INDEX play_state_trash ON play_state (workspace_id, project_id, deleted_at) WHERE deleted_at IS NOT NULL;

ALTER TABLE play_state ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON play_state TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The play states that are not in the project's trash, which everything that
-- reads them reads them through.
CREATE VIEW live_play_state WITH (security_invoker = true) AS
	SELECT p.* FROM play_state p WHERE p.deleted_at IS NULL;

-- The application adds the rows and changes them, deletes a play state to
-- the trash by a change of its deleted_at, and for good in the trash's purge.
GRANT SELECT, INSERT, UPDATE, DELETE ON play_state TO storycreep_app;
GRANT SELECT ON play_state TO storycreep_maintenance;
GRANT SELECT ON live_play_state TO storycreep_app, storycreep_maintenance;
