-- +goose Up

-- Nodes (PRD FR-EDIT-02, FR-EDIT-05, FR-EDIT-06, §5.3, plan note N43): the
-- elements, branches, and jumpers that a board's narrative is made of, which
-- package node keeps. Every change of a node is a command of the pipeline
-- (package commands), which increments its rev (§5.7, ADR-006), but the
-- removal of a speaker that the trash's purge deletes, which increments it
-- too.

-- Whether tags are the tags of a node: at most 32, each of 1 to 50
-- characters. The commands check, too, that no two are alike but for their
-- case, as Go folds it (package node), which this check does not, since the
-- database's collation may fold them otherwise.
CREATE FUNCTION node_tags_valid(tags text[]) RETURNS boolean
	LANGUAGE sql IMMUTABLE PARALLEL SAFE
	RETURN cardinality(tags) <= 32 AND array_position(tags, NULL) IS NULL
		AND NOT EXISTS (SELECT FROM unnest(tags) t WHERE length(t) NOT BETWEEN 1 AND 50);

-- A node of a board: an element, a branch, or a jumper, as its type says,
-- which is fixed once the node is created. An element is a line of dialogue
-- or of narration, as its kind says, with a rich-text document in its
-- canonical form (package richtext), a speaker, a component of the project,
-- and a cover, an asset of the project, and may end the flow (is_ending). A
-- branch picks the output whose condition holds, which plan item 43 adds. A
-- jumper continues the flow at its target, a node of the project and that
-- node's board, which no foreign key holds, so that a target that is deleted,
-- or purged, stays as it was, dangling, which the validation reports (FR-EDIT-05
-- AC1, FR-PROJ-02 AC2), rather than being dropped. Every node has a title,
-- which may be empty, a colour of the palette that boards have, tags, and a
-- position on its board's canvas, in units of the canvas, held to 2 decimal
-- places (FR-EDIT-02 AC4), and a size, a width and a height alike, or none,
-- which the canvas then lays out by itself. A deleted node is in the
-- project's trash, with deleted_at; a node of a board in the trash is in the
-- trash with it, and comes back with it. The purge of a board deletes its
-- nodes, by the cascade of their foreign key to it.
CREATE TABLE node (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	board_id uuid NOT NULL,
	type text NOT NULL CHECK (type IN ('element', 'branch', 'jumper')),
	kind text CHECK (kind IN ('dialogue', 'narration')),
	-- At most 200 characters, as the commands' payloads take (package node,
	-- MaxTitleLength), so that the undo of a change of one, which writes the
	-- stored one back, holds none that the payload's schema refuses.
	title text NOT NULL DEFAULT '' CHECK (length(title) <= 200),
	text jsonb CHECK (jsonb_typeof(text) = 'object'),
	speaker_component_id uuid,
	cover_asset_id uuid,
	color text NOT NULL DEFAULT 'none'
		CHECK (color IN ('none', 'red', 'orange', 'yellow', 'green', 'teal', 'blue', 'purple', 'pink', 'gray')),
	tags text[] NOT NULL DEFAULT '{}' CHECK (node_tags_valid(tags)),
	x numeric(12, 2) NOT NULL CHECK (abs(x) <= 1000000000),
	y numeric(12, 2) NOT NULL CHECK (abs(y) <= 1000000000),
	width numeric(12, 2) CHECK (width BETWEEN 1 AND 100000),
	height numeric(12, 2) CHECK (height BETWEEN 1 AND 100000),
	is_ending boolean NOT NULL DEFAULT false,
	target_board_id uuid,
	target_node_id uuid,
	-- Incremented by every change of the node: by each command that changes
	-- it, and by the purge of its speaker.
	rev bigint NOT NULL DEFAULT 1 CHECK (rev >= 1),
	deleted_at timestamptz,
	-- The users who created the node and changed it last, or null once their
	-- accounts are deleted.
	created_by uuid REFERENCES "user" ON DELETE SET NULL,
	updated_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	UNIQUE (workspace_id, project_id, id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, board_id) REFERENCES board (workspace_id, project_id, id)
		ON DELETE CASCADE,
	-- A speaker or a cover that is deleted for good leaves the node without
	-- one. The trash's purge of a component unsets it first, and increments
	-- the node's revision, as every change of a node does (PurgeComponents);
	-- an asset is deleted only with its project, whose nodes go with it.
	FOREIGN KEY (workspace_id, project_id, speaker_component_id) REFERENCES component (workspace_id, project_id, id)
		ON DELETE SET NULL (speaker_component_id),
	FOREIGN KEY (workspace_id, project_id, cover_asset_id) REFERENCES asset (workspace_id, project_id, id)
		ON DELETE SET NULL (cover_asset_id),
	-- What only an element has, and only a jumper.
	CHECK ((type = 'element') = (kind IS NOT NULL)),
	CHECK ((type = 'element') = (text IS NOT NULL)),
	CHECK (type = 'element' OR speaker_component_id IS NULL AND cover_asset_id IS NULL AND NOT is_ending),
	CHECK (type = 'jumper' OR target_node_id IS NULL),
	CHECK ((target_board_id IS NULL) = (target_node_id IS NULL)),
	CHECK ((width IS NULL) = (height IS NULL))
);
-- A board's nodes, which its canvas reads, in the order of their IDs.
CREATE INDEX node_board ON node (workspace_id, project_id, board_id, id);
-- The jumpers that target a node ("referenced by", FR-EDIT-05 AC2), and those
-- that target a board's nodes, which its deletion leaves dangling (FR-PROJ-02
-- AC2).
CREATE INDEX node_target ON node (workspace_id, project_id, target_node_id, id) WHERE target_node_id IS NOT NULL;
CREATE INDEX node_target_board ON node (workspace_id, project_id, target_board_id, id)
	WHERE target_board_id IS NOT NULL;
CREATE INDEX node_speaker ON node (workspace_id, project_id, speaker_component_id)
	WHERE speaker_component_id IS NOT NULL;
CREATE INDEX node_cover ON node (workspace_id, project_id, cover_asset_id) WHERE cover_asset_id IS NOT NULL;
CREATE INDEX node_created_by ON node (created_by);
CREATE INDEX node_updated_by ON node (updated_by);
-- The nodes of each project's trash, by when they were deleted, which the
-- trash's list reads, newest first, and its purge, oldest first.
CREATE INDEX node_trash ON node (workspace_id, project_id, deleted_at) WHERE deleted_at IS NOT NULL;

ALTER TABLE node ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON node TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The nodes that are not in the project's trash: neither deleted themselves,
-- nor of a board that is (package trash), which everything that reads the
-- project's content reads them through.
CREATE VIEW live_node WITH (security_invoker = true) AS
	SELECT n.* FROM node n
	WHERE n.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM board b WHERE b.workspace_id = n.workspace_id
		AND b.project_id = n.project_id AND b.id = n.board_id AND b.deleted_at IS NOT NULL);

-- The application adds the rows and changes them, deletes a node to the trash
-- by a change of its deleted_at, and for good in the trash's purge.
GRANT SELECT, INSERT, UPDATE, DELETE ON node TO storycreep_app;
GRANT SELECT ON node TO storycreep_maintenance;
GRANT SELECT ON live_node TO storycreep_app, storycreep_maintenance;
