-- +goose Up

-- The operation log (PRD §5.7, FR-API-03, ADR-004): the project's sequence of
-- the batches of commands applied to it, each with the inverse that undoes
-- it and the entities that it changed, and the record of every command of an
-- applied batch, which keeps a command that was applied recognised for as
-- long as its project exists. Package commands, the command pipeline, is the
-- only writer of both, in the transaction of the batch that it applies.

-- The seq of the project's latest operation, or 0 before its first. The
-- pipeline locks the project's row before it applies a batch and sets this
-- to the batch's seq in the batch's transaction, so that a project's batches
-- are applied one at a time and its seqs commit in order, without a gap: a
-- reader that has seen one seq has seen every seq before it.
ALTER TABLE project ADD COLUMN seq bigint NOT NULL DEFAULT 0 CHECK (seq >= 0);

-- One row per applied batch, which is one logical undo step (§5.7). The log
-- is appended to and never changed, so the application may add and read its
-- rows but neither change nor delete them; a project's hard deletion deletes
-- them with the project, and the migration that adds compaction (FR-GDPR-03),
-- which is not among the maintenance role's jobs (§5.4), grants what it needs.
CREATE TABLE operation (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	seq bigint NOT NULL CHECK (seq > 0),
	-- Every command of the batch, in order.
	command_ids uuid[] NOT NULL CHECK (cardinality(command_ids) > 0),
	-- The user who applied the batch, or null once the account is deleted.
	actor_user_id uuid REFERENCES "user" ON DELETE SET NULL,
	-- The API token that the user applied it with, if any (FR-AUTH-04). Like
	-- the audit log's, it has no foreign key, so that it outlives the token.
	actor_token_id uuid,
	-- The type of the batch's command, as node.create, or batch for a batch
	-- of several commands, which no command's type is.
	type text NOT NULL,
	-- The batch's commands, in order, each with its command_id, type, and
	-- payload.
	payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'array'),
	-- The commands that undo the batch, in the order to apply them, or null if
	-- one of its commands cannot be undone.
	inverse jsonb CHECK (jsonb_typeof(inverse) = 'array'),
	-- The entities that the batch changed, each with its kind, its ID, and its
	-- revision after the batch.
	entity_refs jsonb NOT NULL CHECK (jsonb_typeof(entity_refs) = 'array'),
	created_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, project_id, seq),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX operation_actor_user ON operation (actor_user_id);

-- One row per command of an applied batch (FR-API-03). A command's ID is the
-- client's, unique within the project that it was sent to, so that it is
-- recognised there, and an ID that a client reuses in another project, or
-- that a client of another workspace sends, neither collides with nor reveals
-- this one. The rows are kept for as long as the project exists, and neither
-- compaction nor pruning touches them (FR-GDPR-03 AC3), so the application
-- may add and read them only. They need no foreign key to the operation,
-- which compaction may delete.
CREATE TABLE applied_command (
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	command_id uuid NOT NULL,
	-- The seq of the batch that applied the command.
	seq bigint NOT NULL CHECK (seq > 0),
	created_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (workspace_id, project_id, command_id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE operation ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON operation TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

ALTER TABLE applied_command ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON applied_command TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

GRANT SELECT, INSERT ON operation, applied_command TO storycreep_app;
GRANT SELECT ON operation, applied_command TO storycreep_maintenance;
