-- The personal-data exports (PRD FR-GDPR-01, plan item 87, note N94), and what
-- their archive reads of the account itself, through the application's login:
-- the account, its sessions, and its email links, which belong to no workspace.
-- What it reads of the workspaces, package maintenance reads.

-- name: PersonalExportAccount :one
-- PersonalExportAccount returns what the archive holds of the account, with
-- whether it has a password and when that was last set, never the password's
-- hash, and when the transaction began, by the database's clock, which the
-- audit log's entries are of; or no row unless the account is active, as no
-- one else can read the export, and its user can no longer sign in.
SELECT id, email::text AS email, display_name, locale, status, email_verified_at, is_instance_admin, created_at,
    (password_hash IS NOT NULL)::boolean AS password_set, password_changed_at, now()::timestamptz AS read_at
FROM "user" WHERE id = @id AND status = 'active';

-- name: PersonalExportSessions :many
-- PersonalExportSessions returns the account's sessions that have not been
-- deleted, ended ones too, in the order of their IDs, never the hashes of
-- their secrets.
SELECT id, ip, user_agent, created_at, last_seen_at, expires_at, revoked_at FROM session
WHERE user_id = @user_id ORDER BY id;

-- name: PersonalExportEmailLinks :many
-- PersonalExportEmailLinks returns the account's email links that have not
-- been deleted, in the order of their IDs: what kind each is and when it was
-- made, sent, used, and expires, never the hash of its secret, nor the
-- address or the locale that it was sent to (PRD §9.6).
SELECT kind, created_at, sent_at, expires_at, used_at FROM email_token WHERE user_id = @user_id ORDER BY id;

-- name: InsertPersonalDataExport :one
-- InsertPersonalDataExport adds an export that waits for its job, unless the
-- user has one on its way, when it adds no row.
INSERT INTO personal_data_export (id, user_id, storage_key, expires_at)
VALUES (@id, @user_id, @storage_key, @expires_at)
ON CONFLICT (user_id) WHERE status IN ('queued', 'building') DO NOTHING
RETURNING *;

-- name: PersonalDataExport :one
-- PersonalDataExport returns the user's export, unless its time has passed,
-- whether or not its job has deleted it yet.
SELECT * FROM personal_data_export WHERE user_id = @user_id AND id = @id AND expires_at > now();

-- name: PersonalDataExports :many
-- PersonalDataExports returns the user's exports whose time has not passed,
-- newest first, at most row_limit of them, after the export of the UUID
-- after, or from the newest for the zero UUID.
SELECT * FROM personal_data_export
WHERE user_id = @user_id AND expires_at > now()
    AND (@after::uuid = '00000000-0000-0000-0000-000000000000' OR id < @after::uuid)
ORDER BY id DESC LIMIT @row_limit;

-- name: PersonalDataExportInProgress :one
-- PersonalDataExportInProgress returns the ID of the user's export that is on
-- its way and that the user can still read.
SELECT id FROM personal_data_export
WHERE user_id = @user_id AND status IN ('queued', 'building') AND expires_at > now();

-- name: FailExpiredPersonalDataExports :exec
-- FailExpiredPersonalDataExports ends the user's export that is on its way
-- past its time, which would otherwise keep another from being added until
-- its expiry job has run.
UPDATE personal_data_export SET status = 'failed', finished_at = now()
WHERE user_id = @user_id AND status IN ('queued', 'building') AND expires_at <= now();

-- name: PersonalDataExportOfJob :one
-- PersonalDataExportOfJob returns the export of a job, whatever its time.
SELECT * FROM personal_data_export WHERE user_id = @user_id AND id = @id;

-- name: StartPersonalDataExport :execrows
-- StartPersonalDataExport marks an export that waits as being built. An
-- attempt that follows one that failed finds it building already.
UPDATE personal_data_export SET status = 'building'
WHERE user_id = @user_id AND id = @id AND status IN ('queued', 'building');

-- name: FinishPersonalDataExport :execrows
-- FinishPersonalDataExport makes an export that is being built ready.
UPDATE personal_data_export SET status = 'ready', size_bytes = @size_bytes, file_name = @file_name,
    finished_at = now()
WHERE user_id = @user_id AND id = @id AND status = 'building';

-- name: FailPersonalDataExport :one
-- FailPersonalDataExport fails an export that has not ended, and returns the
-- key of its archive, or no row if it has ended.
UPDATE personal_data_export SET status = 'failed', finished_at = now()
WHERE user_id = @user_id AND id = @id AND status IN ('queued', 'building')
RETURNING storage_key;

-- name: DeletePersonalDataExport :execrows
-- DeletePersonalDataExport deletes an export whose time has passed.
DELETE FROM personal_data_export WHERE user_id = @user_id AND id = @id;
