-- +goose Up

-- Usage metering (PRD FR-ENT-02, §5.7, ADR-010): how much of each thing that
-- the plans meter a workspace uses, which the entitlement layer checks the
-- plan's limits against (FR-ENT-01). Package usage is the only writer of
-- both tables.
--
-- A change records what it adds to or takes from a key as a row of
-- usage_delta, in its own transaction, and never updates a counter, so that
-- changes that run at once in one workspace contend on no row for it. A
-- workspace's usage of a key is its counter's value plus the sum of the
-- deltas still present, read in one statement, and so in one snapshot. A
-- periodic rollup job folds the deltas into the counter: in one transaction,
-- it deletes the deltas that it can see and adds their sum to the counter. A
-- delta whose change has not committed is invisible to that delete, so it
-- stays, and a later run folds it. There is no high-water mark: deltas do not
-- commit in the order of their IDs, so "every delta still present is pending"
-- is the only rule that cannot miss one that commits late (§5.3).
--
-- IDs are UUIDv7s, which the server generates (§5.2), so no column has a
-- default for them. The keys are package usage's, which a later release may
-- add to, so the database checks only their form.

-- The usage of a key that the rollup job has folded. Only the rollup job
-- writes it.
CREATE TABLE usage_counter (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	key text NOT NULL CHECK (key COLLATE "C" ~ '^[a-z]+(_[a-z]+)*$'),
	value bigint NOT NULL,
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, key)
);

-- What a change added to a key, or took from it, which no rollup has folded
-- yet. A row is added once and never changed, and only the rollup job, which
-- folds it, deletes it.
CREATE TABLE usage_delta (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	key text NOT NULL CHECK (key COLLATE "C" ~ '^[a-z]+(_[a-z]+)*$'),
	delta bigint NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX usage_delta_workspace ON usage_delta (workspace_id, key);

ALTER TABLE usage_counter ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON usage_counter TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

ALTER TABLE usage_delta ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON usage_delta TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The rollup job runs as the application, in a transaction of each
-- workspace (§5.4), so the application adds and changes counters, and
-- deletes the deltas that it folds, but never changes a delta.
GRANT SELECT, INSERT, UPDATE ON usage_counter TO storycreep_app;
GRANT SELECT, INSERT, DELETE ON usage_delta TO storycreep_app;
GRANT SELECT ON usage_counter, usage_delta TO storycreep_maintenance;

-- Every membership that exists already counts towards its workspace's
-- members (FR-ENT-01), guests' included, as each that is added from now on
-- does: each workspace's are recorded as one delta, as if they had been added
-- one by one, which the first rollup folds. The ID is a UUIDv7 of the
-- migration's time, made as package ids makes one: the 48 bits of the Unix
-- time in milliseconds, then the version, 7, and random bits, where
-- gen_random_uuid's version 4 has its variant already.
INSERT INTO usage_delta (id, workspace_id, key, delta)
SELECT
	encode(set_bit(set_bit(overlay(uuid_send(gen_random_uuid())
		PLACING substring(int8send(floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint) FROM 3)
		FROM 1 FOR 6), 52, 1), 53, 1), 'hex')::uuid,
	workspace_id, 'members', count(*)
FROM membership
GROUP BY workspace_id;
