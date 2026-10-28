-- +goose Up

-- Expressions on a board's content (PRD FR-EXPR-03, FR-EDIT-03, FR-EDIT-04,
-- §5.3, plan note N51): a branch's outputs, the ports that its connections
-- leave it by, and the expressions of the nodes, the connections, and the
-- outputs, which package expression keeps. Every change of them is a command
-- of the pipeline (package commands).

-- An output of a branch (FR-EDIT-04), which package node keeps: a
-- conditional output, whose condition picks it, or the branch's else, which
-- the flow takes when no condition holds. The conditional outputs are ordered
-- by their order, a key of package orderkey, then by their IDs, so that
-- moving one changes it alone, and the else, which has no order, comes after
-- them. Its kind follows its place: the first conditional output of the
-- branch that is not in the trash is its if, and the others its elseifs,
-- which the commands keep as they place an output. A branch has at most one
-- else, and one only while it has a conditional output, which the commands
-- check. A deleted output is in the project's trash, unlisted, with
-- deleted_at, and the connections that leave it are in the trash with it,
-- as an output of a node in the trash is with its node, and with its board.
-- The purge of a node deletes its outputs, by the cascade of their foreign
-- key, and the purge of an output its connections and its condition.
CREATE TABLE branch_output (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	board_id uuid NOT NULL,
	node_id uuid NOT NULL,
	kind text NOT NULL CHECK (kind IN ('if', 'elseif', 'else')),
	"order" text COLLATE "C" CHECK ("order" ~ '^[0-9A-Za-z]*[1-9A-Za-z]$' AND length("order") <= 128),
	-- Incremented by every command that changes the output, and by every
	-- change of its kind that a command makes of another output's place.
	rev bigint NOT NULL DEFAULT 1 CHECK (rev >= 1),
	deleted_at timestamptz,
	-- The users who created the output and changed it last, or null once
	-- their accounts are deleted.
	created_by uuid REFERENCES "user" ON DELETE SET NULL,
	updated_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	UNIQUE (workspace_id, project_id, id),
	-- The keys by which a connection leaves the output of its node, and an
	-- expression is the condition of the output of its board.
	UNIQUE (workspace_id, project_id, node_id, id),
	UNIQUE (workspace_id, project_id, board_id, id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, board_id, node_id) REFERENCES node (workspace_id, project_id, board_id, id)
		ON DELETE CASCADE,
	-- Only a conditional output has a place among the others.
	CHECK ((kind = 'else') = ("order" IS NULL))
);
-- A branch's outputs, in their order.
CREATE INDEX branch_output_node ON branch_output (workspace_id, project_id, node_id, "order", id);
CREATE INDEX branch_output_created_by ON branch_output (created_by);
CREATE INDEX branch_output_updated_by ON branch_output (updated_by);
-- The outputs of each project's trash, by when they were deleted, which its
-- purge reads, oldest first.
CREATE INDEX branch_output_trash ON branch_output (workspace_id, project_id, deleted_at) WHERE deleted_at IS NOT NULL;

ALTER TABLE branch_output ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON branch_output TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The outputs that are not in the project's trash: neither deleted
-- themselves, nor of a node that is, nor of a board that is (package trash).
CREATE VIEW live_branch_output WITH (security_invoker = true) AS
	SELECT o.* FROM branch_output o
	WHERE o.deleted_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM node n WHERE n.workspace_id = o.workspace_id AND n.project_id = o.project_id
			AND n.id = o.node_id AND n.deleted_at IS NOT NULL)
		AND NOT EXISTS (SELECT 1 FROM board b WHERE b.workspace_id = o.workspace_id AND b.project_id = o.project_id
			AND b.id = o.board_id AND b.deleted_at IS NOT NULL);

-- The application adds the rows and changes them, deletes an output to the
-- trash by a change of its deleted_at, and for good in the trash's purge.
GRANT SELECT, INSERT, UPDATE, DELETE ON branch_output TO storycreep_app;
GRANT SELECT ON branch_output TO storycreep_maintenance;
GRANT SELECT ON live_branch_output TO storycreep_app, storycreep_maintenance;

-- A connection leaves an element by its port out, which from_output_id
-- leaves null, and a branch by one of the branch's outputs, from_output_id,
-- whose foreign key holds it to be an output of the node that the
-- connection leaves, and deletes the connection with it. The connections
-- that leave a port are ordered among themselves.
ALTER TABLE connection ADD COLUMN from_output_id uuid;
ALTER TABLE connection ADD CONSTRAINT connection_from_output FOREIGN KEY (workspace_id, project_id, from_node_id,
	from_output_id) REFERENCES branch_output (workspace_id, project_id, node_id, id) ON DELETE CASCADE;
-- The key by which an expression is the condition or the script of a
-- connection of its board.
ALTER TABLE connection ADD CONSTRAINT connection_board_connection UNIQUE (workspace_id, project_id, board_id, id);
-- The connections that leave a node, by its ports, each port's in their
-- order, which a new one is placed after, and the cascade of an output's
-- purge, or of a node's, finds them by.
DROP INDEX connection_from;
CREATE INDEX connection_from ON connection (workspace_id, project_id, from_node_id, from_output_id, "order", id);

-- The connections that are not in the project's trash: neither deleted
-- themselves, nor of a node that is, nor of an output that is, nor of a
-- board that is (package trash). It holds the column that the table gains.
CREATE OR REPLACE VIEW live_connection WITH (security_invoker = true) AS
	SELECT c.* FROM connection c
	WHERE c.deleted_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM node n WHERE n.workspace_id = c.workspace_id AND n.project_id = c.project_id
			AND n.id IN (c.from_node_id, c.to_node_id) AND n.deleted_at IS NOT NULL)
		AND NOT EXISTS (SELECT 1 FROM branch_output o WHERE o.workspace_id = c.workspace_id
			AND o.project_id = c.project_id AND o.id = c.from_output_id AND o.deleted_at IS NOT NULL)
		AND NOT EXISTS (SELECT 1 FROM board b WHERE b.workspace_id = c.workspace_id AND b.project_id = c.project_id
			AND b.id = c.board_id AND b.deleted_at IS NOT NULL);

-- An expression (§5.3, §5.8): a condition or a script of its owner, a node,
-- a connection, or a branch's output, exactly one of which it names, at one
-- of the owner's fields, as the source that its author wrote, which a
-- command sets, and what compiling the source gives, against what the
-- project's expressions can name at the owner's board (package expr): its
-- AST, whose names are the IDs of what they name, and the AST's version, if
-- it compiles, or else the first error of the source, and the IDs and the
-- keys that the source names, which a change of what they name compiles it
-- again by (package expression). A command writes the source alone, with
-- neither an AST nor an error, and the pipeline compiles it once every
-- command of its batch has applied, so that no expression is ever stored
-- without one. An expression is part of its owner, without a rev or a
-- deleted_at of its own: a change of it is a change of its owner, which is
-- in the trash with it, and whose purge deletes it, by the cascade of its
-- foreign key.
CREATE TABLE expression (
	id uuid PRIMARY KEY DEFAULT uuid_v7(),
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	-- The board of the owner, at which the source's names resolve.
	board_id uuid NOT NULL,
	node_id uuid,
	connection_id uuid,
	branch_output_id uuid,
	field text NOT NULL CHECK (field IN ('on_enter', 'on_exit', 'condition', 'on_traverse')),
	context text NOT NULL CHECK (context IN ('condition', 'script')),
	-- At most 16,384 characters, as the commands' payloads take (package
	-- expression, MaxSourceLength): change both at once.
	source text NOT NULL CHECK (length(source) <= 16384),
	ast jsonb CHECK (jsonb_typeof(ast) = 'object'),
	ast_version integer CHECK (ast_version >= 1),
	is_valid boolean NOT NULL DEFAULT false,
	error jsonb CHECK (jsonb_typeof(error) = 'object'),
	-- The UUIDs of the variables, the components, the attribute definitions,
	-- and the nodes that the source names, and the keys that it names them
	-- by, by which a change of them finds it.
	refs uuid[] NOT NULL DEFAULT '{}',
	keys text[] NOT NULL DEFAULT '{}',
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	UNIQUE (workspace_id, project_id, id),
	-- An owner has one expression at each of its fields. These keys are the
	-- indexes by which its reads find its expressions, and so does the
	-- cascade of its purge, whose foreign key names its board too, which the
	-- scan of the index filters by.
	UNIQUE (workspace_id, project_id, node_id, field),
	UNIQUE (workspace_id, project_id, connection_id, field),
	UNIQUE (workspace_id, project_id, branch_output_id, field),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, board_id, node_id) REFERENCES node (workspace_id, project_id, board_id, id)
		ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, board_id, connection_id)
		REFERENCES connection (workspace_id, project_id, board_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, board_id, branch_output_id)
		REFERENCES branch_output (workspace_id, project_id, board_id, id) ON DELETE CASCADE,
	CHECK (num_nonnulls(node_id, connection_id, branch_output_id) = 1),
	-- A node has its on-enter and on-exit scripts, a connection its condition
	-- and its on-traverse script, and an output its condition (§5.8.5).
	CHECK (CASE
		WHEN node_id IS NOT NULL THEN field IN ('on_enter', 'on_exit')
		WHEN connection_id IS NOT NULL THEN field IN ('condition', 'on_traverse')
		ELSE field = 'condition'
	END),
	CHECK (context = CASE field WHEN 'condition' THEN 'condition' ELSE 'script' END),
	-- A compiled source has an AST and its version, or an error, and one that
	-- the pipeline has yet to compile neither.
	CHECK (is_valid = (ast IS NOT NULL)),
	CHECK ((ast IS NULL) = (ast_version IS NULL)),
	CHECK (ast IS NULL OR error IS NULL),
	CHECK (array_position(refs, NULL) IS NULL AND array_position(keys, NULL) IS NULL)
);
-- The expressions that name what a batch changes, which it compiles again.
CREATE INDEX expression_refs ON expression USING gin (refs);
CREATE INDEX expression_keys ON expression USING gin (keys);
-- The expressions that a batch has yet to compile, and those that do not
-- compile but name something, which a change of what the project's
-- expressions can name may let compile.
CREATE INDEX expression_pending ON expression (workspace_id, project_id) WHERE ast IS NULL AND error IS NULL;
CREATE INDEX expression_invalid ON expression (workspace_id, project_id)
	WHERE NOT is_valid AND (cardinality(refs) > 0 OR cardinality(keys) > 0);

ALTER TABLE expression ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON expression TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The application adds an expression, compiles it, and deletes it when its
-- owner's field loses it, and the cascade of its owner's purge deletes it.
GRANT SELECT, INSERT, UPDATE, DELETE ON expression TO storycreep_app;
GRANT SELECT ON expression TO storycreep_maintenance;
