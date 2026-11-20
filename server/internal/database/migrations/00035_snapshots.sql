-- +goose Up

-- The snapshots of the projects, their releases (PRD FR-SNAP-01, FR-DEP-04,
-- §5.3, §7.3, plan note N85), which package snapshot keeps. Publishing a
-- project's draft adds a snapshot that is building, of the version and the
-- note that its author chose, with the project's operation sequence that the
-- author validated (acknowledged_seq) and the warnings that they acknowledged,
-- and enqueues the job that builds it. The job reads the draft as of one
-- moment, whose sequence it records (source_seq), validates it again, and
-- serializes it into the published layout (package serializer). The snapshot
-- then becomes ready, with its files, or failed, with the code of why, and
-- never changes again (FR-SNAP-01 AC1): the grants below let the application
-- set only what a build sets, and the triggers refuse to change a snapshot
-- that is no longer building, or to add a file to one, for everyone. A failed
-- snapshot holds no files, and is kept, with the validation report that its
-- build stored, as the record of the attempt (AC4).
--
-- A snapshot holds nothing of anyone's account but IDs: who published it
-- (published_by), which the API resolves to a display name, or to the
-- tombstone once the account is deleted, when the cascade sets it null, the
-- one change of a built snapshot that the trigger lets through; and, in its
-- stored report, who muted each muted finding, whom the API no longer names
-- once their account is deleted (package snapshot, Report). Its files hold no
-- personal data (§6.2). The application deletes no snapshot: a project's
-- purge and its workspace's delete theirs by the cascades, which run with the
-- privileges of the table's owner, and the retention of FR-SNAP-06 is a later
-- phase's, which grants itself what it deletes with.
CREATE TABLE snapshot (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	-- The version, MAJOR.MINOR.PATCH, each a number from 0 to 999,999,999
	-- without a leading zero, as package snapshot reads it (ParseVersion), and
	-- its numbers, which the versions are compared by.
	version text NOT NULL CHECK (version ~ '^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$'),
	version_major integer NOT NULL CHECK (version_major >= 0),
	version_minor integer NOT NULL CHECK (version_minor >= 0),
	version_patch integer NOT NULL CHECK (version_patch >= 0),
	-- The changelog note, of 1 to 2,000 characters, as the publish request
	-- takes (package snapshot, MaxNoteLength): change both at once.
	note text NOT NULL CHECK (length(note) BETWEEN 1 AND 2000),
	status text NOT NULL DEFAULT 'building' CHECK (status IN ('building', 'ready', 'failed')),
	-- Why a failed snapshot failed: the build's validation found an error
	-- (validation_failed), or a warning that its author did not acknowledge
	-- in a draft that changed since they validated it (validation_changed),
	-- or the build itself failed (build_failed).
	failure_code text CHECK (failure_code IN ('validation_failed', 'validation_changed', 'build_failed')),
	-- The project's operation sequence of the report that the author
	-- validated, and the warnings that they acknowledged, of those of the
	-- draft that the pre-check validated, each once, as the report names
	-- them, which the build compares its own report with.
	acknowledged_seq bigint NOT NULL CHECK (acknowledged_seq >= 0),
	acknowledged_warnings jsonb NOT NULL CHECK (jsonb_typeof(acknowledged_warnings) = 'array'),
	-- What the build read, and wrote: the project's operation sequence as of
	-- the draft that it read (source_seq), which its report and its files are
	-- of (FR-DEP-04 AC4), and its report, which a failed build of a draft that
	-- it read keeps too; and, of a ready snapshot alone, the version of the
	-- published format, the content hash, and the manifest's entity counts.
	source_seq bigint CHECK (source_seq >= 0),
	validation_report jsonb CHECK (jsonb_typeof(validation_report) = 'object'),
	schema_version integer CHECK (schema_version >= 1),
	content_hash text CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
	counts jsonb CHECK (jsonb_typeof(counts) = 'object'),
	-- Who published the snapshot, or null once their account is deleted.
	published_by uuid REFERENCES "user" ON DELETE SET NULL,
	-- When the snapshot was published, which its manifest names, to the
	-- second, and when its build ended, ready or failed.
	published_at timestamptz NOT NULL DEFAULT now(),
	built_at timestamptz,
	CHECK (version = version_major || '.' || version_minor || '.' || version_patch),
	CHECK ((status = 'failed') = (failure_code IS NOT NULL)),
	CHECK ((status = 'building') = (built_at IS NULL)),
	CHECK ((status = 'ready') = (content_hash IS NOT NULL)),
	CHECK ((content_hash IS NULL) = (schema_version IS NULL) AND (content_hash IS NULL) = (counts IS NULL)),
	CHECK (status <> 'ready' OR (source_seq IS NOT NULL AND validation_report IS NOT NULL)),
	CHECK (status <> 'building' OR (source_seq IS NULL AND validation_report IS NULL)),
	UNIQUE (workspace_id, id),
	-- A project's snapshots, by their IDs, which its list reads in their
	-- order, and which a step of the migration log names its snapshot by.
	UNIQUE (workspace_id, project_id, id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE
);
-- A project's versions are unique among its snapshots that are building or
-- ready, and a failed one does not reserve its version (FR-SNAP-01 AC4). A
-- version's numbers are the version, so this is the version's uniqueness, by
-- which package snapshot also finds a project's newest version, which a new
-- one must be greater than, and which the recount of the workspace's
-- snapshots reads.
CREATE UNIQUE INDEX snapshot_version ON snapshot (workspace_id, project_id, version_major, version_minor,
	version_patch) WHERE status <> 'failed';
CREATE INDEX snapshot_published_by ON snapshot (published_by);

ALTER TABLE snapshot ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON snapshot TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- A snapshot is added building, and only its build changes it, once, to
-- ready or failed. This refuses to add one that is not building, and to
-- change one that is not, for everyone, but the cascade of its publisher's
-- account deletion, which sets published_by null and changes nothing else.
-- +goose StatementBegin
CREATE FUNCTION snapshot_stays_built() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
	IF TG_OP = 'UPDATE' AND NEW.published_by IS NULL
		AND to_jsonb(NEW) - 'published_by' = to_jsonb(OLD) - 'published_by' THEN
		RETURN NEW;
	END IF;
	RAISE EXCEPTION 'a snapshot is added building, and never changes once it is ready or failed'
		USING ERRCODE = 'insufficient_privilege';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER snapshot_added_building BEFORE INSERT ON snapshot
	FOR EACH ROW WHEN (NEW.status <> 'building') EXECUTE FUNCTION snapshot_stays_built();
CREATE TRIGGER snapshot_stays_built BEFORE UPDATE ON snapshot
	FOR EACH ROW WHEN (OLD.status <> 'building') EXECUTE FUNCTION snapshot_stays_built();

-- The application adds a snapshot, and its build sets what it read and
-- wrote, and nothing else; it changes no snapshot's identity, and deletes
-- none.
GRANT SELECT, INSERT ON snapshot TO storycreep_app;
GRANT UPDATE (status, failure_code, source_seq, validation_report, schema_version, content_hash, counts, built_at)
	ON snapshot TO storycreep_app;
GRANT SELECT ON snapshot TO storycreep_maintenance;

-- A ready snapshot's files, in the published layout (§6.3): each by its path,
-- with its SHA-256, as the manifest's files names it, and its bytes, which
-- the build stores in the transaction that makes the snapshot ready, so that
-- a failed build leaves none (FR-SNAP-01 AC5). A file is added to a snapshot
-- that is building alone, and never changes.
CREATE TABLE snapshot_file (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	snapshot_id uuid NOT NULL,
	path text NOT NULL CHECK (length(path) BETWEEN 1 AND 255),
	sha256 text NOT NULL CHECK (sha256 ~ '^sha256:[0-9a-f]{64}$'),
	bytes bytea NOT NULL,
	UNIQUE (workspace_id, snapshot_id, path),
	FOREIGN KEY (workspace_id, snapshot_id) REFERENCES snapshot (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE snapshot_file ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON snapshot_file TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- This refuses to add a file to a snapshot that is not building, which the
-- transaction sees through its own row-level security, so that a file goes
-- only to a snapshot of its workspace, and to change a file, for everyone.
-- +goose StatementBegin
CREATE FUNCTION snapshot_file_while_building() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
	IF TG_OP = 'INSERT' AND EXISTS (SELECT FROM snapshot s
		WHERE s.workspace_id = NEW.workspace_id AND s.id = NEW.snapshot_id AND s.status = 'building') THEN
		RETURN NEW;
	END IF;
	RAISE EXCEPTION 'a file is added to a snapshot that is building alone, and never changes'
		USING ERRCODE = 'insufficient_privilege';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER snapshot_file_while_building BEFORE INSERT OR UPDATE ON snapshot_file
	FOR EACH ROW EXECUTE FUNCTION snapshot_file_while_building();

GRANT SELECT, INSERT ON snapshot_file TO storycreep_app;
GRANT SELECT ON snapshot_file TO storycreep_maintenance;

-- A step of the migration log is of a snapshot of its project (migration
-- 00034, plan note N84), which deletes it with the snapshot. Its unique key
-- of the project, the snapshot, and the version leads with the columns of
-- this key, which so needs no index of its own.
ALTER TABLE schema_migration_log ADD CONSTRAINT schema_migration_log_snapshot_fkey
	FOREIGN KEY (workspace_id, project_id, snapshot_id) REFERENCES snapshot (workspace_id, project_id, id)
	ON DELETE CASCADE;
