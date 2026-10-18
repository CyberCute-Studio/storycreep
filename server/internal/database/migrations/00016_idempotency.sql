-- +goose Up

-- The idempotency records of the command endpoint (PRD FR-API-03): the
-- answer to each batch of commands that the pipeline applied, or refused
-- for a reason that the batch and the project's state decide, kept for at
-- least 24 hours, so that the batch sent again, as when its answer was lost,
-- is answered as it was the first time and never applied twice. Package
-- commands, the command pipeline, writes them in the batch's transaction,
-- under the project's lock, and its pruning job deletes them once they are
-- older than that; a command that was applied stays recognised after that
-- by its row of applied_command, which nothing prunes.
--
-- A batch has a row for each of its commands, and every one of them points
-- to the row of the batch's first command, which holds the batch's answer
-- once, however many commands the batch has: the commands of the batch, in
-- order, and the answer. Deleting that row deletes the batch's other rows,
-- so a batch's record is never kept in part.
CREATE TABLE idempotency_record (
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	command_id uuid NOT NULL,
	-- The first command of the batch, whose row holds its answer.
	batch_command_id uuid NOT NULL,
	-- On the row of the batch's first command alone: every command of the
	-- batch, in order, and what the batch was answered with.
	command_ids uuid[],
	response jsonb,
	created_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (workspace_id, project_id, command_id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, batch_command_id)
		REFERENCES idempotency_record (workspace_id, project_id, command_id) ON DELETE CASCADE,
	CHECK ((command_id = batch_command_id) = (response IS NOT NULL)),
	CHECK ((response IS NULL) = (command_ids IS NULL)),
	CHECK (command_ids IS NULL OR (cardinality(command_ids) > 0 AND command_ids[1] = command_id)),
	CHECK (response IS NULL OR jsonb_typeof(response) = 'object')
);
-- The cascade from the row of a batch's first command finds the batch's
-- other rows by it.
CREATE INDEX idempotency_record_batch ON idempotency_record (workspace_id, project_id, batch_command_id);
-- The pruning job finds a workspace's oldest batches by it.
CREATE INDEX idempotency_record_created ON idempotency_record (workspace_id, created_at)
	WHERE command_id = batch_command_id;

ALTER TABLE idempotency_record ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON idempotency_record TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The application adds the records, reads them, and deletes them, as a
-- batch that is applied anew deletes the record of a refused batch that its
-- commands were in, but never changes one. The pruning job skips the records
-- that a batch holds, with SELECT … FOR UPDATE SKIP LOCKED, so that it never
-- waits for a batch, which PostgreSQL allows only with the privilege to
-- update a column of the table: created_at, which says only when the record
-- may be pruned.
GRANT SELECT, INSERT, DELETE ON idempotency_record TO storycreep_app;
GRANT UPDATE (created_at) ON idempotency_record TO storycreep_app;
GRANT SELECT ON idempotency_record TO storycreep_maintenance;
