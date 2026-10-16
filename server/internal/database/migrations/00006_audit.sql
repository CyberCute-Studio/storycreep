-- +goose Up

-- Audit log recording (PRD FR-TEN-07, §9.6): entries for the events that
-- belong to no workspace, and the key of the hash that stands in an entry for
-- an email address that belongs to no account. Package audit writes the
-- entries.

-- Some events belong to no workspace: a login, a session's revocation, an
-- account's deletion, and a maintenance run across workspaces (§5.4). Their
-- entries have no workspace_id. The workspace_isolation policy (00004) never
-- matches such a row, because a null workspace_id equals no workspace's ID, so
-- this policy lets a transaction that has no workspace add them. No policy
-- lets the application read them yet; the personal-data export (FR-GDPR-01)
-- adds what it needs. A transaction of a workspace still adds only that
-- workspace's entries, and none without a workspace, so that each event is
-- filed under the workspace it happened in. The maintenance jobs bypass
-- row-level security, and read these entries with the rest, as the audit
-- retention job must to delete them.
ALTER TABLE audit_entry ALTER COLUMN workspace_id DROP NOT NULL;
CREATE POLICY instance_entries ON audit_entry FOR INSERT TO storycreep_app
	WITH CHECK (workspace_id IS NULL AND NULLIF(current_setting('app.workspace_id', true), '') IS NULL);

-- The keys that the server keeps for itself, one per use, by name. Each is
-- generated once, by the migration that adds it, so that nothing needs to be
-- configured for it, and stays in the database, so that it outlives a backup
-- and its restore with the rows that depend on it. The table spans
-- workspaces, and has no row-level security (§5.4). The application may read
-- the keys, but not change them, and the maintenance jobs need none.
CREATE TABLE instance_key (
	name text PRIMARY KEY,
	key bytea NOT NULL CHECK (octet_length(key) = 32),
	created_at timestamptz NOT NULL DEFAULT now()
);
GRANT SELECT ON instance_key TO storycreep_app;

-- The key of the keyed hash (HMAC-SHA256) that an audit entry of a failed
-- login records instead of an attempted email address that belongs to no
-- account (§9.6). Its 32 bytes are the SHA-256 hash of two random UUIDs,
-- which gen_random_uuid draws from PostgreSQL's cryptographically strong
-- random source, as pgcrypto's gen_random_bytes does: 244 random bits,
-- without an extension.
INSERT INTO instance_key (name, key)
	VALUES ('audit_email', sha256(uuid_send(gen_random_uuid()) || uuid_send(gen_random_uuid())));
