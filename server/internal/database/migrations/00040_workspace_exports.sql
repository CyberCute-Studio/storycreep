-- +goose Up

-- The workspace exports (PRD FR-DATA-02, plan item 86, note N93), which
-- package authoring keeps: a zip of a whole workspace, of each of its projects
-- outside the trash as a full authoring export of the draft, its members, its
-- audit log, and its assets, which a job builds, and which the owner who asked
-- for it alone downloads, for seven days, while they are still an owner. The
-- archive holds the members' email addresses and the audit log's IP addresses
-- and user agents, so nobody else reads its row or its file.
--
-- Its file is a blob of the assets' store, under a key that no other row
-- shares, which the row names from the moment it is added, before the job
-- stores anything there, so that the orphan reconciliation, which reads the
-- key through ReferencedBlobs, never deletes the file of a row, and deletes
-- the file of a row that is gone, as with its user's deletion.
CREATE TABLE workspace_export (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	-- Who asked for the export, who alone reads it. The export goes with
	-- their account.
	requested_by uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
	status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'building', 'ready', 'failed')),
	storage_key text NOT NULL,
	-- The archive's size, how many projects it holds, and the name that it is
	-- downloaded under, of a ready export alone.
	size_bytes bigint CHECK (size_bytes >= 0),
	project_count integer CHECK (project_count >= 0),
	file_name text,
	created_at timestamptz NOT NULL DEFAULT now(),
	-- When its build ended, ready or failed.
	finished_at timestamptz,
	-- When the export is deleted, with its file.
	expires_at timestamptz NOT NULL,
	CHECK ((status = 'ready') = (size_bytes IS NOT NULL)),
	CHECK ((status = 'ready') = (project_count IS NOT NULL)),
	CHECK ((status = 'ready') = (file_name IS NOT NULL)),
	CHECK ((status IN ('ready', 'failed')) = (finished_at IS NOT NULL)),
	UNIQUE (workspace_id, id),
	UNIQUE (workspace_id, storage_key)
);
-- A user has at most one export of a workspace on its way, as each reads the
-- workspace whole.
CREATE UNIQUE INDEX workspace_export_running ON workspace_export (workspace_id, requested_by)
	WHERE status IN ('queued', 'building');
-- A user's exports of a workspace, newest first, by their IDs.
CREATE INDEX workspace_export_list ON workspace_export (workspace_id, requested_by, id DESC);
CREATE INDEX workspace_export_requested_by ON workspace_export (requested_by);

ALTER TABLE workspace_export ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON workspace_export TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

GRANT SELECT, INSERT, DELETE ON workspace_export TO storycreep_app;
GRANT UPDATE (status, size_bytes, project_count, file_name, finished_at) ON workspace_export TO storycreep_app;
GRANT SELECT ON workspace_export TO storycreep_maintenance;
