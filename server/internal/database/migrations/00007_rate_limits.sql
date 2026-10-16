-- +goose Up

-- Rate limits and the lock of an email address after failed sign-ins (PRD
-- FR-AUTH-05, NFR-SEC-06). Package ratelimit keeps both. They are counted in
-- PostgreSQL, so that every server of an instance counts the same requests,
-- and the limits hold however requests are spread across servers (FR-AUTH-05
-- AC2).
--
-- Both tables span workspaces, and have no row-level security (§5.4): a
-- counter belongs to an IP address, an account, or an email address, not to
-- a workspace. Neither holds any of these as it is, but as a keyed hash
-- (HMAC-SHA256) with the rate_limit key below, so that the tables hold no
-- address, of a client or of mail, that someone who reads them could tell,
-- such as an address that a sign-in mistyped.

-- The counters of the rate limits, one per bucket and key, each for a fixed
-- window of time: the hits of the key in the bucket since window_start,
-- which ends at window_end. A hit after the window starts the count of the
-- next one. The rate limit cleanup job deletes the rows whose window has
-- ended, which count nothing any more.
CREATE TABLE rate_limit (
	bucket text NOT NULL,
	key bytea NOT NULL CHECK (octet_length(key) = 32),
	window_start timestamptz NOT NULL,
	window_end timestamptz NOT NULL CHECK (window_end > window_start),
	hits integer NOT NULL CHECK (hits > 0),
	PRIMARY KEY (bucket, key)
);
CREATE INDEX rate_limit_window_end ON rate_limit (window_end);

-- The sign-in attempts with an email address since its last successful one,
-- counted as failures until one succeeds, which deletes the row: failures
-- attempts, the last at last_attempt_at, and the address is locked until
-- locked_until after too many. The address is the key, whether or not it
-- belongs to an account, so that a lock does not tell whether it does. The
-- rate limit cleanup job deletes the rows whose attempts are forgotten.
CREATE TABLE login_attempt (
	key bytea PRIMARY KEY CHECK (octet_length(key) = 32),
	failures integer NOT NULL CHECK (failures > 0),
	last_attempt_at timestamptz NOT NULL,
	locked_until timestamptz
);
CREATE INDEX login_attempt_last_attempt_at ON login_attempt (last_attempt_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON rate_limit, login_attempt TO storycreep_app;

-- The key of the keyed hash that the two tables hold instead of an IP
-- address, an account's ID, or an email address, made as the audit_email
-- key is (00006).
INSERT INTO instance_key (name, key)
	VALUES ('rate_limit', sha256(uuid_send(gen_random_uuid()) || uuid_send(gen_random_uuid())));
