-- +goose Up

-- Variables (PRD FR-VAR-01, FR-VAR-02, §5.3, plan note N41): the named,
-- typed state of a project's narrative, which its expressions read and write,
-- and which package variable keeps. Every change of a variable is a command
-- of the pipeline (package commands), which increments its rev (§5.7,
-- ADR-006).

-- What the foreign key of a board's variables refers to, which keeps a
-- variable's board in its project. It replaces the index of a project's
-- boards by their IDs, which its own index serves as that one did.
ALTER TABLE board ADD CONSTRAINT board_workspace_id_project_id_id_key UNIQUE (workspace_id, project_id, id);
DROP INDEX board_project;

-- A variable of a project: global, which every board reads, or of a board,
-- which only that board's expressions read (FR-VAR-02), whose board_id is
-- set exactly then. Its key is an identifier of the expression language
-- (§5.8.1, package ident), compared by its bytes, as identifiers are case by
-- case, and unique among the project's global variables, or among its
-- board's, that are not in the trash: a key is free again once its variable
-- is deleted, and restoring that variable is refused while another of its
-- scope has the key. A variable of a board in the trash is in the trash with
-- it, holds its key all the same, and comes back with it; nothing adds a
-- variable to a board in the trash. Its default is a JSON value of its type: a
-- boolean for bool, an integer within ±(2^53 − 1) for int, which every
-- evaluator represents exactly (§5.8.9), a number for float, which jsonb
-- holds finite, and a string of at most 4,096 characters, as the language's
-- string literals are, for string. reset_on_enter, which resets the variable
-- to its default as the flow enters its board, is set only on a board's
-- variable. A variable sits in a folder of variables, or at the top, ordered
-- among the folder's children by its order, a key of package orderkey, as a
-- board is among a folder of boards'. A deleted variable is in the project's
-- trash, with deleted_at. The purge of a board deletes its variables, which
-- are in the trash with it, by the cascade of their foreign key to it.
CREATE TABLE variable (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	scope text NOT NULL CHECK (scope IN ('global', 'board')),
	board_id uuid,
	folder_id uuid,
	-- The kind of the variable's folder, which the foreign key to the folder
	-- holds to folders of variables.
	folder_kind text NOT NULL DEFAULT 'variable' CHECK (folder_kind = 'variable'),
	"order" text COLLATE "C" NOT NULL CHECK ("order" ~ '^[0-9A-Za-z]*[1-9A-Za-z]$' AND length("order") <= 128),
	key text COLLATE "C" NOT NULL CHECK (
		key ~ '^[A-Za-z_][A-Za-z0-9_]*$' AND length(key) <= 64
		AND key NOT IN ('true', 'false', 'and', 'or', 'not', 'global', 'board', 'null', 'if', 'else', 'elseif')
	),
	type text NOT NULL CHECK (type IN ('bool', 'int', 'float', 'string')),
	default_value jsonb NOT NULL,
	description text NOT NULL DEFAULT '',
	reset_on_enter boolean NOT NULL DEFAULT false,
	-- Incremented by every command that changes the variable.
	rev bigint NOT NULL DEFAULT 1 CHECK (rev >= 1),
	deleted_at timestamptz,
	-- The users who created the variable and changed it last, or null once
	-- their accounts are deleted.
	created_by uuid REFERENCES "user" ON DELETE SET NULL,
	updated_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, board_id) REFERENCES board (workspace_id, project_id, id)
		ON DELETE CASCADE,
	-- A folder that is deleted for good leaves its variables at the top.
	FOREIGN KEY (workspace_id, project_id, folder_kind, folder_id)
		REFERENCES folder (workspace_id, project_id, kind, id) ON DELETE SET NULL (folder_id),
	CHECK ((scope = 'board') = (board_id IS NOT NULL)),
	CHECK (scope = 'board' OR NOT reset_on_enter),
	-- Each branch reads the default as a number only once it is one.
	CHECK (CASE type
		WHEN 'bool' THEN jsonb_typeof(default_value) = 'boolean'
		WHEN 'string' THEN jsonb_typeof(default_value) = 'string' AND length(default_value #>> '{}') <= 4096
		WHEN 'int' THEN CASE WHEN jsonb_typeof(default_value) = 'number' THEN
			(default_value #>> '{}')::numeric = trunc((default_value #>> '{}')::numeric)
			AND abs((default_value #>> '{}')::numeric) <= 9007199254740991 ELSE false END
		WHEN 'float' THEN CASE WHEN jsonb_typeof(default_value) = 'number' THEN
			abs((default_value #>> '{}')::numeric) <= 1.7976931348623157e308 ELSE false END
		ELSE false
	END)
);
-- The keys of the variables that are not in the trash, unique within each
-- scope, which a variable's key is checked against.
CREATE UNIQUE INDEX variable_global_key ON variable (workspace_id, project_id, key)
	WHERE scope = 'global' AND deleted_at IS NULL;
CREATE UNIQUE INDEX variable_board_key ON variable (workspace_id, project_id, board_id, key)
	WHERE scope = 'board' AND deleted_at IS NULL;
CREATE INDEX variable_board ON variable (workspace_id, project_id, board_id);
CREATE INDEX variable_folder ON variable (workspace_id, project_id, folder_id);
CREATE INDEX variable_project ON variable (workspace_id, project_id, id);
CREATE INDEX variable_created_by ON variable (created_by);
CREATE INDEX variable_updated_by ON variable (updated_by);
-- The variables of each project's trash, by when they were deleted, which
-- the trash's list reads, newest first, and its purge, oldest first.
CREATE INDEX variable_trash ON variable (workspace_id, project_id, deleted_at) WHERE deleted_at IS NOT NULL;

ALTER TABLE variable ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON variable TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The variables that are not in the project's trash: neither deleted
-- themselves, nor of a board that is (package trash), which everything that
-- reads the project's content reads them through.
CREATE VIEW live_variable WITH (security_invoker = true) AS
	SELECT v.* FROM variable v
	WHERE v.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM board b WHERE b.workspace_id = v.workspace_id
		AND b.project_id = v.project_id AND b.id = v.board_id AND b.deleted_at IS NOT NULL);

-- The application adds the rows and changes them, deletes a variable to the
-- trash by a change of its deleted_at, and for good in the trash's purge.
GRANT SELECT, INSERT, UPDATE, DELETE ON variable TO storycreep_app;
GRANT SELECT ON variable TO storycreep_maintenance;
GRANT SELECT ON live_variable TO storycreep_app, storycreep_maintenance;
