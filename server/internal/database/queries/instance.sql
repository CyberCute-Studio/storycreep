-- First-run setup (package setup, PRD FR-ADMIN-02) and the signup policy that
-- it chose (package account, FR-AUTH-01). The instance table has exactly one
-- row, which spans workspaces and has no row-level security.

-- name: InstanceSetup :one
-- InstanceSetup returns whether first-run setup has completed, and the
-- signup policy that it chose, or null if it chose none.
SELECT (setup_completed_at IS NOT NULL)::boolean AS setup_completed, signup_policy FROM instance;

-- name: LockInstanceSetup :one
-- LockInstanceSetup is InstanceSetup, and locks the row until the
-- transaction ends, so that of two setups at once, the second waits for the
-- first, and then finds setup completed (FR-ADMIN-02 AC3).
SELECT (setup_completed_at IS NOT NULL)::boolean AS setup_completed, signup_policy FROM instance FOR UPDATE;

-- name: CompleteSetup :exec
-- CompleteSetup marks first-run setup completed, with the signup policy, the
-- SMTP settings, and the bucket of the assets' files that it chose, the
-- settings of each all null if it chose none.
UPDATE instance SET setup_completed_at = now(), signup_policy = @signup_policy, smtp_host = @smtp_host,
    smtp_port = @smtp_port, smtp_security = @smtp_security, smtp_username = @smtp_username,
    smtp_password = @smtp_password, smtp_from = @smtp_from, s3_host = @s3_host, s3_port = @s3_port,
    s3_security = @s3_security, s3_bucket = @s3_bucket, s3_region = @s3_region,
    s3_access_key_id = @s3_access_key_id, s3_secret_access_key = @s3_secret_access_key, updated_at = now();

-- name: InstanceSMTP :one
-- InstanceSMTP returns the SMTP settings that first-run setup chose (package
-- email), whose host is null if it chose none.
SELECT smtp_host, smtp_port, smtp_security, smtp_username, smtp_password, smtp_from FROM instance;

-- name: InstanceS3 :one
-- InstanceS3 returns whether first-run setup has completed, and the bucket
-- that it chose for the files of the assets (package asset), whose host is
-- null if it chose none.
SELECT (setup_completed_at IS NOT NULL)::boolean AS setup_completed, s3_host, s3_port, s3_security, s3_bucket,
    s3_region, s3_access_key_id, s3_secret_access_key
FROM instance;

-- name: DatabaseIdentity :one
-- DatabaseIdentity returns what tells the database apart from a copy of it,
-- which the orphan reconciliation of the assets' blobs names the store's
-- owner with (package asset): the system identifier of its server's cluster,
-- which initdb picks anew for each cluster, and the database's object ID,
-- which each database of a cluster has of its own. So a backup restored with
-- pg_restore, into another server or into another database of the same
-- one, has another identity, and a copy of the cluster's files, as a
-- snapshot of its volume, or a replica of it, has the same.
SELECT ((SELECT c.system_identifier FROM pg_catalog.pg_control_system() c)::text || '/' || d.oid::text)::text AS identity
FROM pg_catalog.pg_database d
WHERE d.datname = current_database();

-- name: SetupCodeKey :one
-- SetupCodeKey returns the key that the setup code is made of.
SELECT key FROM instance_key WHERE name = 'setup_code';
