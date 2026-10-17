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
-- CompleteSetup marks first-run setup completed, with the signup policy and
-- the SMTP settings that it chose, which are all null if it chose none.
UPDATE instance SET setup_completed_at = now(), signup_policy = @signup_policy, smtp_host = @smtp_host,
    smtp_port = @smtp_port, smtp_security = @smtp_security, smtp_username = @smtp_username,
    smtp_password = @smtp_password, smtp_from = @smtp_from, updated_at = now();

-- name: InstanceSMTP :one
-- InstanceSMTP returns the SMTP settings that first-run setup chose (package
-- email), whose host is null if it chose none.
SELECT smtp_host, smtp_port, smtp_security, smtp_username, smtp_password, smtp_from FROM instance;

-- name: SetupCodeKey :one
-- SetupCodeKey returns the key that the setup code is made of.
SELECT key FROM instance_key WHERE name = 'setup_code';

-- name: InstanceAdminAccess :one
-- InstanceAdminAccess returns the InstanceAdminAccess capability that a
-- server of the instance last started with, or null if none has recorded it
-- (package workspace).
SELECT instance_admin_access FROM instance;

-- name: SetInstanceAdminAccess :exec
-- SetInstanceAdminAccess records the InstanceAdminAccess capability that a
-- server starts with.
UPDATE instance SET instance_admin_access = @enabled, updated_at = now();

-- name: InstanceAdmins :many
-- InstanceAdmins returns the accounts that are instance admins, whatever
-- their status, in the order of their IDs.
SELECT id FROM "user" WHERE is_instance_admin ORDER BY id;
