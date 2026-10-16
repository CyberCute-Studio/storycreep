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
-- CompleteSetup marks first-run setup completed, with the signup policy that
-- it chose.
UPDATE instance SET setup_completed_at = now(), signup_policy = @signup_policy, updated_at = now();

-- name: SetupCodeKey :one
-- SetupCodeKey returns the key that the setup code is made of.
SELECT key FROM instance_key WHERE name = 'setup_code';
