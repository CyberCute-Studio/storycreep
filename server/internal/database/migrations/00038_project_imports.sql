-- +goose Up

-- Project import (PRD FR-DATA-01, NFR-SEC-13, plan item 84, note N91), which
-- package authoring keeps: an archive of the authoring format, uploaded in
-- chunks to the assets' store, which a job checks whole and imports as a new
-- project of the workspace, all or nothing.

-- An account that stands for someone whom an imported archive names and who
-- is not a member of the workspace that it is imported into (FR-DATA-01): it
-- carries the display name from the archive, so that a comment keeps who wrote
-- it, and no one signs in as it, since every sign-in and session reads an
-- account whose status is active.
ALTER TABLE "user" DROP CONSTRAINT user_status_check,
	ADD CONSTRAINT user_status_check
		CHECK (status IN ('active', 'suspended', 'pending_deletion', 'deleted', 'imported'));

-- The imported identities of a workspace, one for each person and display
-- name that its archives have named, so that importing the same archive again
-- adds no more of them. A workspace's identities are its own, so that nothing
-- of one workspace's archives shows in another's.
CREATE TABLE imported_user (
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	-- The user's ID in the archive, which is no account of this instance's
	-- that the workspace can see.
	source_user_id uuid NOT NULL,
	display_name text NOT NULL,
	user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
	created_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (workspace_id, source_user_id, display_name),
	UNIQUE (workspace_id, user_id)
);
CREATE INDEX imported_user_user ON imported_user (user_id);

ALTER TABLE imported_user ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON imported_user TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

GRANT SELECT, INSERT ON imported_user TO storycreep_app;
GRANT SELECT ON imported_user TO storycreep_maintenance;

-- An import, from the first chunk of its upload to its report, which only the
-- user who asked for it reads. It names the project that it created in a
-- column that is not project_id, as it is no row of that project's: it outlives
-- the project's purge, and is deleted when it expires.
CREATE TABLE project_import (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	requested_by uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
	-- uploading takes chunks; queued waits for the job; importing is the job
	-- at work; ready and failed are its ends.
	status text NOT NULL DEFAULT 'uploading'
		CHECK (status IN ('uploading', 'queued', 'importing', 'ready', 'failed')),
	file_name text NOT NULL CHECK (char_length(file_name) BETWEEN 1 AND 255),
	size_bytes bigint NOT NULL CHECK (size_bytes > 0),
	chunk_bytes integer NOT NULL CHECK (chunk_bytes > 0),
	-- auto keeps the archive's IDs unless one is in use on the instance, and
	-- remap gives everything fresh IDs (§5.2).
	id_policy text NOT NULL CHECK (id_policy IN ('auto', 'remap')),
	-- The policy that the import applied, of a ready one alone.
	applied_policy text CHECK (applied_policy IN ('preserve', 'remap')),
	created_project_id uuid,
	-- How far the job is, for the client to show: what it is doing, and how
	-- much of it is done.
	phase text NOT NULL DEFAULT 'upload',
	progress_done bigint NOT NULL DEFAULT 0 CHECK (progress_done >= 0),
	progress_total bigint NOT NULL DEFAULT 0 CHECK (progress_total >= 0),
	-- What a ready import reports, and why a failed one was refused.
	report jsonb CHECK (jsonb_typeof(report) = 'object'),
	failure jsonb CHECK (jsonb_typeof(failure) = 'object'),
	created_at timestamptz NOT NULL DEFAULT now(),
	finished_at timestamptz,
	-- When the import is deleted, with what is left of its upload.
	expires_at timestamptz NOT NULL,
	CHECK ((status = 'ready') = (applied_policy IS NOT NULL)),
	CHECK ((status = 'ready') = (report IS NOT NULL)),
	CHECK ((status = 'failed') = (failure IS NOT NULL)),
	CHECK ((status IN ('ready', 'failed')) = (finished_at IS NOT NULL)),
	UNIQUE (workspace_id, id),
	FOREIGN KEY (workspace_id, created_project_id) REFERENCES project (workspace_id, id)
		ON DELETE SET NULL (created_project_id)
);
-- A user has at most one import of a workspace on its way, which they resume.
CREATE UNIQUE INDEX project_import_running ON project_import (workspace_id, requested_by)
	WHERE status IN ('uploading', 'queued', 'importing');
-- A user's imports of a workspace, newest first, by their IDs.
CREATE INDEX project_import_list ON project_import (workspace_id, requested_by, id DESC);
CREATE INDEX project_import_requested_by ON project_import (requested_by);
CREATE INDEX project_import_project ON project_import (workspace_id, created_project_id);

ALTER TABLE project_import ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON project_import TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

GRANT SELECT, INSERT, DELETE ON project_import TO storycreep_app;
GRANT UPDATE (status, applied_policy, created_project_id, phase, progress_done, progress_total, report, failure,
	finished_at) ON project_import TO storycreep_app;
GRANT SELECT ON project_import TO storycreep_maintenance;

-- A chunk of an import's upload, which is a blob of the assets' store under a
-- key of its own, which the orphan reconciliation reads through
-- ReferencedBlobs, so that it keeps the chunks of an upload that is on its way
-- and deletes those of one that is gone.
CREATE TABLE project_import_chunk (
	workspace_id uuid NOT NULL,
	import_id uuid NOT NULL,
	chunk_index integer NOT NULL CHECK (chunk_index >= 0),
	bytes integer NOT NULL CHECK (bytes > 0),
	storage_key text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (workspace_id, import_id, chunk_index),
	UNIQUE (workspace_id, storage_key),
	FOREIGN KEY (workspace_id, import_id) REFERENCES project_import (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE project_import_chunk ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON project_import_chunk TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

GRANT SELECT, INSERT, DELETE ON project_import_chunk TO storycreep_app;
GRANT UPDATE (bytes) ON project_import_chunk TO storycreep_app;
GRANT SELECT ON project_import_chunk TO storycreep_maintenance;
