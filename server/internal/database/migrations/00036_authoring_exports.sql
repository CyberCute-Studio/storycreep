-- +goose Up

-- The authoring exports (PRD FR-DATA-01, plan item 83, note N90), which package
-- authoring keeps: a zip of a project's authoring data, of its draft or of one
-- of its releases, which a job builds, and which the user who asked for it
-- alone downloads, for seven days. The archive can hold comments and the names
-- of their authors, so nobody else reads its row or its file.
--
-- Its file is a blob of the assets' store, under a key that no other row
-- shares, which the row names from the moment it is added, before the job
-- stores anything there, so that the orphan reconciliation, which reads the
-- key through ReferencedBlobs, never deletes the file of a row, and deletes
-- the file of a row that is gone, as with its project's purge.
CREATE TABLE authoring_export (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	-- Who asked for the export, who alone reads it. The export goes with
	-- their account, as it may hold what they alone were given.
	requested_by uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
	-- The release that the archive holds as its content, or null for the
	-- draft.
	snapshot_id uuid,
	-- full holds comments, play states, and annotations, and the names of
	-- who wrote and published them; content holds none of them.
	profile text NOT NULL CHECK (profile IN ('full', 'content')),
	include_assets boolean NOT NULL,
	include_releases boolean NOT NULL,
	status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'building', 'ready', 'failed')),
	storage_key text NOT NULL,
	-- The archive's size and the name that it is downloaded under, of a
	-- ready export alone.
	size_bytes bigint CHECK (size_bytes >= 0),
	file_name text,
	created_at timestamptz NOT NULL DEFAULT now(),
	-- When its build ended, ready or failed.
	finished_at timestamptz,
	-- When the export is deleted, with its file.
	expires_at timestamptz NOT NULL,
	CHECK ((status = 'ready') = (size_bytes IS NOT NULL)),
	CHECK ((status = 'ready') = (file_name IS NOT NULL)),
	CHECK ((status IN ('ready', 'failed')) = (finished_at IS NOT NULL)),
	UNIQUE (workspace_id, id),
	UNIQUE (workspace_id, storage_key),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, snapshot_id) REFERENCES snapshot (workspace_id, project_id, id)
		ON DELETE CASCADE
);
-- A user has at most one export of a project on its way, as each reads the
-- project whole.
CREATE UNIQUE INDEX authoring_export_running ON authoring_export (workspace_id, project_id, requested_by)
	WHERE status IN ('queued', 'building');
-- A user's exports of a project, newest first, by their IDs.
CREATE INDEX authoring_export_list ON authoring_export (workspace_id, project_id, requested_by, id DESC);
CREATE INDEX authoring_export_requested_by ON authoring_export (requested_by);
CREATE INDEX authoring_export_snapshot ON authoring_export (workspace_id, project_id, snapshot_id);

ALTER TABLE authoring_export ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON authoring_export TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

GRANT SELECT, INSERT, DELETE ON authoring_export TO storycreep_app;
GRANT UPDATE (status, size_bytes, file_name, finished_at) ON authoring_export TO storycreep_app;
GRANT SELECT ON authoring_export TO storycreep_maintenance;
