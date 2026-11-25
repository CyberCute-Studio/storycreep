-- +goose Up

-- The personal-data exports (PRD FR-GDPR-01, §9.6, plan item 87, note N94),
-- which package authoring keeps: a zip of what the instance holds of one user,
-- as the inventory of §9.6 says, from every workspace, which a job builds,
-- reading the user's rows of the workspaces through the maintenance path
-- (§5.4), and which that user alone downloads, for seven days.

-- When the account's password was last set, which the export holds as the
-- password's metadata, never the hash (§9.6), or null for an account without
-- one. An account's password set before this column was is taken to be set
-- when the audit log last recorded its change or its reset, or else when the
-- account was made, as registering, setup, the operator's create-user, and an
-- invitation's new account set one then.
ALTER TABLE "user" ADD COLUMN password_changed_at timestamptz;

-- The audit entries of each actor, and of each subject, in the order in which
-- they were made, which the export reads a page at a time (package
-- maintenance), and the entries whose metadata names a user, which an
-- account's memberships that are gone and its API tokens are found by: the log
-- holds every workspace's entries, and its one index leads with the
-- workspace.
CREATE INDEX audit_entry_actor ON audit_entry (actor_user_id, created_at, id) WHERE actor_user_id IS NOT NULL;
CREATE INDEX audit_entry_subject ON audit_entry (subject_id, created_at, id) WHERE subject_id IS NOT NULL;
CREATE INDEX audit_entry_user_metadata ON audit_entry ((metadata ->> 'user_id')) WHERE metadata ? 'user_id';

UPDATE "user" u SET password_changed_at = coalesce(
	(SELECT max(e.created_at) FROM audit_entry e
		WHERE e.subject_id = u.id AND e.subject_kind = 'user' AND e.result = 'success'
			AND e.action IN ('auth.password_change', 'auth.password_reset')),
	u.created_at)
WHERE u.password_hash IS NOT NULL;

-- The invitations to an address, of every workspace, which the export of the
-- address's account reads.
CREATE INDEX invite_email ON invite (email) WHERE email IS NOT NULL;

-- The imported identities that stand for a person, of every workspace, which
-- the export of that person's account reads, and the account's hard deletion
-- will delete (plan item 111).
CREATE INDEX imported_user_source ON imported_user (source_user_id);

-- A recipient's notifications of every workspace, in the order of their IDs,
-- which the export of the recipient's account reads a page at a time, and
-- which the cascades of the accounts' deletions read as they read the index
-- of the recipient alone that it replaces: other members make a user's
-- notifications, as many as they mention the user, so a page must not read
-- all of those after it.
DROP INDEX notification_user;
CREATE INDEX notification_user ON notification (user_id, id);

-- A personal-data export. It is an account's, of no workspace, as a session
-- is, so the table spans workspaces and has no row-level security (§5.4): the
-- service reads only the rows of the user who is signed in.
--
-- Its file is a blob of the assets' store, under a key of its own below
-- personal-data/, which no workspace's orphan reconciliation lists: the build
-- deletes the file of an export that is gone, the expiry job deletes the file
-- before the row, or after the row is gone, and the account's hard deletion
-- deletes the files first, then the rows, by the cascade of the account's
-- row, or itself if it keeps the row as a tombstone (plan item 111).
CREATE TABLE personal_data_export (
	id uuid PRIMARY KEY,
	-- Who asked for the export, who alone reads it, and whom it is of.
	user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
	status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'building', 'ready', 'failed')),
	storage_key text NOT NULL UNIQUE,
	-- The archive's size, and the name that it is downloaded under, of a ready
	-- export alone.
	size_bytes bigint CHECK (size_bytes >= 0),
	file_name text,
	created_at timestamptz NOT NULL DEFAULT now(),
	-- When its build ended, ready or failed.
	finished_at timestamptz,
	-- When the export is deleted, with its file.
	expires_at timestamptz NOT NULL,
	CHECK ((status = 'ready') = (size_bytes IS NOT NULL)),
	CHECK ((status = 'ready') = (file_name IS NOT NULL)),
	CHECK ((status IN ('ready', 'failed')) = (finished_at IS NOT NULL))
);
-- A user has at most one export on its way, as each reads all of the user's
-- data.
CREATE UNIQUE INDEX personal_data_export_running ON personal_data_export (user_id)
	WHERE status IN ('queued', 'building');
-- A user's exports, newest first, by their IDs, which the account's deletion
-- also finds its rows by.
CREATE INDEX personal_data_export_list ON personal_data_export (user_id, id DESC);

GRANT SELECT, INSERT, DELETE ON personal_data_export TO storycreep_app;
GRANT UPDATE (status, size_bytes, file_name, finished_at) ON personal_data_export TO storycreep_app;
-- The account's hard deletion reads the keys of its exports' files.
GRANT SELECT ON personal_data_export TO storycreep_maintenance;
