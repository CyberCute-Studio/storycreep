-- +goose Up

-- The names of the project's expressions (plan note N51): one row of each
-- UUID in an expression's refs, as ref, and of each key in its keys, as key,
-- which package expression writes as it compiles the expression's source
-- (CompileExpressions, IndexExpressionNames), and the cascade of the
-- expression's deletion deletes.
-- It is how a batch finds the expressions that name what it changed, which it
-- compiles again: by a scan of an index that reads the rows of each of those
-- UUIDs and keys alone, however many they are, where the indexes of refs and
-- keys that it replaces, of GIN, were passed over for a scan of every
-- expression of the project once the batch changed more than a few, each of
-- whose refs was compared with each UUID, as a board's deletion of 2,000
-- nodes in a project of 16,000 expressions took 90 ms to find the 5 that
-- named them. The expression's refs and keys stay, as what its compilation
-- last gave, which a compilation compares with what it gives. Each row holds
-- the expression's board too, which never changes, as an expression's owner
-- never leaves its board, so that a board's deletion, which compiles again
-- the expressions of the other boards that name its nodes alone, tells them
-- from its own by the names alone.
CREATE TABLE expression_name (
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	expression_id uuid NOT NULL,
	board_id uuid NOT NULL,
	ref uuid,
	key text COLLATE "C",
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	-- The key names the expression by its workspace and its ID alone, whose
	-- index the check of each row that a batch adds reads, rather than with
	-- its project too, which the indexes of the project's expressions by
	-- their owners begin with as well: before the statistics of the table
	-- were gathered, the planner could not tell those indexes apart, and read
	-- every expression of the project to check each row, so that a batch
	-- that compiled 2,000 expressions in a project of 6,000 took 2 s to check
	-- their names.
	FOREIGN KEY (workspace_id, expression_id) REFERENCES expression (workspace_id, id) ON DELETE CASCADE,
	CHECK (num_nonnulls(ref, key) = 1)
);
-- The expressions that name each UUID, and each key, which a batch reads by
-- what it changed, and which keep an expression from naming one twice.
CREATE UNIQUE INDEX expression_name_ref ON expression_name (workspace_id, project_id, ref, expression_id)
	WHERE ref IS NOT NULL;
CREATE UNIQUE INDEX expression_name_key ON expression_name (workspace_id, project_id, key, expression_id)
	WHERE key IS NOT NULL;
-- The names of each expression, which its compilation replaces, and the
-- cascade of its deletion deletes.
CREATE INDEX expression_name_expression ON expression_name (workspace_id, expression_id);

INSERT INTO expression_name (workspace_id, project_id, expression_id, board_id, ref)
SELECT e.workspace_id, e.project_id, e.id, e.board_id, r.ref FROM expression e, unnest(e.refs) AS r (ref);
INSERT INTO expression_name (workspace_id, project_id, expression_id, board_id, key)
SELECT e.workspace_id, e.project_id, e.id, e.board_id, k.key FROM expression e, unnest(e.keys) AS k (key);

DROP INDEX expression_refs, expression_keys;

ALTER TABLE expression_name ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON expression_name TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The application deletes an expression's names as it compiles it again, and
-- adds them anew, and the cascade of its deletion deletes them.
GRANT SELECT, INSERT, DELETE ON expression_name TO storycreep_app;
GRANT SELECT ON expression_name TO storycreep_maintenance;
