-- +goose Up

-- First-run setup (PRD FR-ADMIN-02): whether it has completed, the signup
-- policy that it chose, and the code that it asks for. Package setup
-- completes it.

-- The instance's own row, which spans workspaces and has no row-level
-- security (§5.4). It always has exactly one row, which this migration adds:
-- the application may read it and change its columns, but neither add nor
-- delete a row, and the trigger below refuses to clear setup_completed_at,
-- so that setup never looks pending again once it completed.
CREATE TABLE instance (
	singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
	-- When first-run setup completed, or null until it does. Until then,
	-- registration is refused whatever the signup policy (FR-ADMIN-02 AC4),
	-- and setup is open; afterwards, the setup route answers 404 (AC1).
	-- Setup locks this row, so that two setups at once complete one (AC3).
	setup_completed_at timestamptz,
	-- The signup policy that setup chose (FR-AUTH-01), which applies unless
	-- STORYCREEP_SIGNUP sets one, or null for none, which is closed.
	signup_policy text CHECK (signup_policy IN ('closed', 'invite', 'open')),
	updated_at timestamptz NOT NULL DEFAULT now()
);
GRANT SELECT, UPDATE (setup_completed_at, signup_policy, updated_at) ON instance TO storycreep_app;

-- Completing setup sets setup_completed_at, which the application must be
-- able to change, so the grant alone cannot keep setup completed: this
-- trigger refuses to clear it again, for everyone, so that whoever holds the
-- setup code, which never changes, cannot complete setup a second time.
-- +goose StatementBegin
CREATE FUNCTION instance_setup_stays_completed() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
	RAISE EXCEPTION 'first-run setup has completed, and stays completed'
		USING ERRCODE = 'insufficient_privilege';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER setup_stays_completed BEFORE UPDATE OF setup_completed_at ON instance
	FOR EACH ROW WHEN (OLD.setup_completed_at IS NOT NULL AND NEW.setup_completed_at IS NULL)
	EXECUTE FUNCTION instance_setup_stays_completed();

-- A database that has accounts already was in use before setup existed, and
-- counts as set up, so that whoever reaches it first cannot make themselves
-- its instance admin. It chose no signup policy, so STORYCREEP_SIGNUP, or
-- else closed, applies, as it did before.
INSERT INTO instance (setup_completed_at)
	SELECT CASE WHEN EXISTS (SELECT FROM "user") THEN now() END;

-- The key that the setup code is made of, which completing setup through the
-- API asks for, so that only who can read the server's log, where the
-- server writes the code while setup is pending, can complete it. It is made
-- as the audit_email key is (00006).
INSERT INTO instance_key (name, key)
	VALUES ('setup_code', sha256(uuid_send(gen_random_uuid()) || uuid_send(gen_random_uuid())));
