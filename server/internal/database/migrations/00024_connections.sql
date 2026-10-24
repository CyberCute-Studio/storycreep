-- +goose Up

-- Connections (PRD FR-EDIT-03, §5.3, plan note N44): the arrows of a board's
-- flow, each from a node of the board to a node of the board, which package
-- connection keeps. Every change of a connection is a command of the
-- pipeline (package commands), which increments its rev (§5.7, ADR-006).

-- A connection's nodes are of its board, which their foreign keys hold
-- through this key of a node's board and ID. Its index reads a board's nodes
-- in the order of their IDs, as the index that it replaces did.
ALTER TABLE node ADD CONSTRAINT node_board_node UNIQUE (workspace_id, project_id, board_id, id);
DROP INDEX node_board;

-- A connection of a board: from a node of the board, by its port out, which
-- every connection leaves until a branch's outputs, its ports, come (plan
-- item 43), to a node of the board, the same node or another, by any number
-- of connections, which the validation reports (FR-DEP-03). It sits among the
-- connections that leave its node, the choices that the node offers, ordered
-- by its order, a key of package orderkey, then by its ID, so that moving one
-- changes it alone (FR-EDIT-03 AC2), and has a label, a rich-text document in
-- its canonical form (package richtext), an empty paragraph for none. A
-- connection deleted on its own is in the project's trash, unlisted, with
-- deleted_at. One of a node in the trash is in the trash with it, and with
-- its board, without a change of its own, and comes back once neither of its
-- nodes is in the trash (FR-PROJ-06 AC1). The purge of a node or of a board
-- deletes its connections, by the cascade of their foreign keys.
CREATE TABLE connection (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	board_id uuid NOT NULL,
	from_node_id uuid NOT NULL,
	to_node_id uuid NOT NULL,
	"order" text COLLATE "C" NOT NULL CHECK ("order" ~ '^[0-9A-Za-z]*[1-9A-Za-z]$' AND length("order") <= 128),
	label jsonb NOT NULL CHECK (jsonb_typeof(label) = 'object'),
	-- Incremented by every command that changes the connection.
	rev bigint NOT NULL DEFAULT 1 CHECK (rev >= 1),
	deleted_at timestamptz,
	-- The users who created the connection and changed it last, or null once
	-- their accounts are deleted.
	created_by uuid REFERENCES "user" ON DELETE SET NULL,
	updated_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	UNIQUE (workspace_id, project_id, id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, board_id, from_node_id)
		REFERENCES node (workspace_id, project_id, board_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, board_id, to_node_id)
		REFERENCES node (workspace_id, project_id, board_id, id) ON DELETE CASCADE
);
-- A board's connections, which its canvas reads, in the order of their IDs.
CREATE INDEX connection_board ON connection (workspace_id, project_id, board_id, id);
-- The connections that leave a node, in their order, which a new one is
-- placed after, and those that enter it; the cascade of a node's purge finds
-- them by these too.
CREATE INDEX connection_from ON connection (workspace_id, project_id, from_node_id, "order", id);
CREATE INDEX connection_to ON connection (workspace_id, project_id, to_node_id);
CREATE INDEX connection_created_by ON connection (created_by);
CREATE INDEX connection_updated_by ON connection (updated_by);
-- The connections of each project's trash, by when they were deleted, which
-- its purge reads, oldest first.
CREATE INDEX connection_trash ON connection (workspace_id, project_id, deleted_at) WHERE deleted_at IS NOT NULL;

ALTER TABLE connection ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON connection TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The connections that are not in the project's trash: neither deleted
-- themselves, nor of a node that is, nor of a board that is (package trash),
-- which everything that reads the project's content reads them through. Both
-- of a connection's nodes are of its board, which their foreign keys hold.
CREATE VIEW live_connection WITH (security_invoker = true) AS
	SELECT c.* FROM connection c
	WHERE c.deleted_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM node n WHERE n.workspace_id = c.workspace_id AND n.project_id = c.project_id
			AND n.id IN (c.from_node_id, c.to_node_id) AND n.deleted_at IS NOT NULL)
		AND NOT EXISTS (SELECT 1 FROM board b WHERE b.workspace_id = c.workspace_id AND b.project_id = c.project_id
			AND b.id = c.board_id AND b.deleted_at IS NOT NULL);

-- The application adds the rows and changes them, deletes a connection to the
-- trash by a change of its deleted_at, and for good in the trash's purge.
GRANT SELECT, INSERT, UPDATE, DELETE ON connection TO storycreep_app;
GRANT SELECT ON connection TO storycreep_maintenance;
GRANT SELECT ON live_connection TO storycreep_app, storycreep_maintenance;
