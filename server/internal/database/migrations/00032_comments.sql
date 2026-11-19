-- +goose Up

-- Threaded comments (PRD FR-COMMENT-01, §5.3, plan item 74), which package
-- comment keeps: comments that a project's members leave on its nodes,
-- connections, boards, and variables, with replies, which every member who
-- reads the project sees, and which an owner, an admin, an editor, or a
-- commenter of the project writes, replies to, resolves, and unresolves, and
-- the author alone edits and deletes. Every change of a comment is a command
-- of the pipeline (package commands), which increments its rev (§5.7,
-- ADR-006). Comments are no part of the story: the published format, the
-- exports, the dependency index, the validation, and the writing statistics
-- hold none of them (FR-COMMENT-01 AC3).

-- A comment of a project: the first comment of a thread, its root, which
-- names the thread's target, exactly one of a node, a connection, a board, or
-- a variable, and holds whether the thread is resolved, and by whom; or a
-- reply, whose parent is the root of its thread, one level deep, and which
-- names no target and no resolution of its own. Its body is a rich-text
-- document (package richtext), of formatting and links alone, without value
-- references or mentions, which the commands refuse, so that a comment names
-- nothing that the dependency index would read. A comment deleted by its
-- author is in the project's trash, unlisted, with deleted_at, from which the
-- undo of its deletion restores it, and which purges it once 30 days have
-- passed (package trash), but for a root of which a reply is not deleted, or
-- deleted for less, which is shown as deleted in its thread meanwhile. A
-- thread is in the trash with its target too, and so with the board of a
-- node, a connection, or a board's variable, or with a node of a connection,
-- as the live view reads it, though nothing writes its comments as the target
-- goes to the trash or comes back from it; and it is deleted with its target
-- by the cascades of the foreign keys, by the target's purge.
CREATE TABLE comment (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	-- A reply's root, or null for a root.
	parent_id uuid,
	-- The comment's thread, its root's ID.
	thread_id uuid GENERATED ALWAYS AS (coalesce(parent_id, id)) STORED,
	-- When the comment's thread was started, its root's created_at, by which
	-- a list orders the threads, newest first, as the server's clock has it
	-- rather than the client's, which made the IDs.
	thread_at timestamptz NOT NULL DEFAULT now(),
	-- The thread's target, of a root alone.
	node_id uuid,
	connection_id uuid,
	board_id uuid,
	variable_id uuid,
	-- The comment's body, a rich-text document in its canonical form, as the
	-- commands store it (richtext.Doc).
	body jsonb NOT NULL CHECK (jsonb_typeof(body) = 'object'),
	-- Incremented by every command that changes the comment.
	rev bigint NOT NULL DEFAULT 1 CHECK (rev >= 1),
	-- When a root's thread was resolved, and by whom, or null while it is
	-- open; the user is null once their account is deleted.
	resolved_at timestamptz,
	resolved_by uuid REFERENCES "user" ON DELETE SET NULL,
	-- When the comment's body last changed after it was posted, or null if
	-- it never did.
	edited_at timestamptz,
	deleted_at timestamptz,
	-- The comment's author, or null once their account is deleted, after
	-- which nobody edits or deletes it.
	created_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	UNIQUE (workspace_id, project_id, id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, parent_id) REFERENCES comment (workspace_id, project_id, id)
		ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, node_id) REFERENCES node (workspace_id, project_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, connection_id) REFERENCES connection (workspace_id, project_id, id)
		ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, board_id) REFERENCES board (workspace_id, project_id, id)
		ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, variable_id) REFERENCES variable (workspace_id, project_id, id)
		ON DELETE CASCADE,
	-- A root names one target, and a reply none, nor a resolution, which is
	-- its thread's, as the root holds it.
	CHECK (CASE WHEN parent_id IS NULL THEN num_nonnulls(node_id, connection_id, board_id, variable_id) = 1
		ELSE num_nonnulls(node_id, connection_id, board_id, variable_id, resolved_at, resolved_by) = 0 END),
	CHECK (resolved_at IS NOT NULL OR resolved_by IS NULL),
	-- A root starts its thread.
	CHECK (parent_id IS NOT NULL OR thread_at = created_at)
);
-- A project's comments in the order of their threads, which its list reads.
CREATE INDEX comment_order ON comment (workspace_id, project_id, thread_at, thread_id);
-- The comments of each thread, which the reads of whether a thread is shown
-- read.
CREATE INDEX comment_thread ON comment (workspace_id, project_id, thread_id, id);
-- The replies of each root, which the purge of a root reads.
CREATE INDEX comment_parent ON comment (workspace_id, project_id, parent_id) WHERE parent_id IS NOT NULL;
-- The threads of each target, which the reads of a target's threads read,
-- and the cascades of the targets' purges, and of the accounts' deletions.
CREATE INDEX comment_node ON comment (workspace_id, project_id, node_id) WHERE node_id IS NOT NULL;
CREATE INDEX comment_connection ON comment (workspace_id, project_id, connection_id) WHERE connection_id IS NOT NULL;
CREATE INDEX comment_board ON comment (workspace_id, project_id, board_id) WHERE board_id IS NOT NULL;
CREATE INDEX comment_variable ON comment (workspace_id, project_id, variable_id) WHERE variable_id IS NOT NULL;
CREATE INDEX comment_created_by ON comment (created_by);
CREATE INDEX comment_resolved_by ON comment (resolved_by);
-- The comments of each project's trash, by when they were deleted, which the
-- trash's purge reads, oldest first.
CREATE INDEX comment_trash ON comment (workspace_id, project_id, deleted_at) WHERE deleted_at IS NOT NULL;

ALTER TABLE comment ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON comment TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- Each thread of a project's comments, by its root, whether or not it is
-- deleted, with its resolution and its target, which everything that reads a
-- thread's target reads it through, so that a new kind of target is a branch
-- of this view alone: the target's kind and ID; its board, a node's, a
-- connection's, a board variable's, or the board itself, or none of a global
-- variable; its node, a node's own or the node that a connection leaves, by
-- which a client opens it, as the reads of the API and the search's results
-- name them; and whether it is outside the project's trash, nor there with
-- what is, as its live view reads it.
-- Each kind of target is a branch of its own, which a root's CHECK puts it in
-- exactly one of, and which reads whether its target is live by a join of
-- the target's live view rather than a subquery of it: PostgreSQL runs no
-- plan that reads a correlated subquery in a parallel worker, so such a
-- subquery would keep every read that joins this view, as the search,
-- whose other kinds' documents it reads in parallel, from running in
-- parallel at all (BenchmarkSearch), and the branches leave the planner
-- fewer tables to join at once.
CREATE VIEW comment_thread_target WITH (security_invoker = true) AS
	SELECT r.workspace_id, r.project_id, r.id AS thread_id, r.resolved_at, 'node'::text AS target_type,
		r.node_id AS target_id, n.board_id, r.node_id AS node_id, l.id IS NOT NULL AS target_live
	FROM comment r
	JOIN node n ON n.workspace_id = r.workspace_id AND n.project_id = r.project_id AND n.id = r.node_id
	LEFT JOIN live_node l ON l.workspace_id = r.workspace_id AND l.project_id = r.project_id AND l.id = r.node_id
	WHERE r.parent_id IS NULL
	UNION ALL
	SELECT r.workspace_id, r.project_id, r.id, r.resolved_at, 'connection'::text, r.connection_id, cn.board_id,
		cn.from_node_id, l.id IS NOT NULL
	FROM comment r
	JOIN connection cn ON cn.workspace_id = r.workspace_id AND cn.project_id = r.project_id
		AND cn.id = r.connection_id
	LEFT JOIN live_connection l ON l.workspace_id = r.workspace_id AND l.project_id = r.project_id
		AND l.id = r.connection_id
	WHERE r.parent_id IS NULL
	UNION ALL
	SELECT r.workspace_id, r.project_id, r.id, r.resolved_at, 'board'::text, r.board_id, r.board_id, NULL::uuid,
		l.id IS NOT NULL
	FROM comment r
	LEFT JOIN live_board l ON l.workspace_id = r.workspace_id AND l.project_id = r.project_id AND l.id = r.board_id
	WHERE r.parent_id IS NULL AND r.board_id IS NOT NULL
	UNION ALL
	SELECT r.workspace_id, r.project_id, r.id, r.resolved_at, 'variable'::text, r.variable_id, v.board_id,
		NULL::uuid, l.id IS NOT NULL
	FROM comment r
	JOIN variable v ON v.workspace_id = r.workspace_id AND v.project_id = r.project_id AND v.id = r.variable_id
	LEFT JOIN live_variable l ON l.workspace_id = r.workspace_id AND l.project_id = r.project_id
		AND l.id = r.variable_id
	WHERE r.parent_id IS NULL;

-- The comments that are not deleted and whose thread's target is not in the
-- project's trash, nor with it, which everything that reads them reads them
-- through: a root's own target, and a reply's through its root, whether or
-- not the root is deleted. A thread is shown while this view holds one of
-- its comments.
CREATE VIEW live_comment WITH (security_invoker = true) AS
	SELECT c.* FROM comment c
	JOIN comment_thread_target t ON t.workspace_id = c.workspace_id AND t.project_id = c.project_id
		AND t.thread_id = c.thread_id
	WHERE c.deleted_at IS NULL AND t.target_live;

-- The application adds the rows and changes them, deletes a comment to the
-- trash by a change of its deleted_at, and for good in the trash's purge.
GRANT SELECT, INSERT, UPDATE, DELETE ON comment TO storycreep_app;
GRANT SELECT ON comment TO storycreep_maintenance;
GRANT SELECT ON comment_thread_target, live_comment TO storycreep_app, storycreep_maintenance;

-- Project search finds comments too (FR-PROJ-03): a document of each comment,
-- whose body is the plain text of the comment's body, without a title, in the
-- trash or not, which the search reads of the comments of live_comment alone.
-- The documents of the comments are written by the pipeline's derivation as
-- the comments are written, and the job of the index writes every document
-- again of the version of the server that adds them (package search).
ALTER TABLE search_entry DROP CONSTRAINT search_entry_kind_check,
	ADD CONSTRAINT search_entry_kind_check CHECK (kind IN ('node', 'connection', 'variable', 'component', 'comment')),
	ADD COLUMN comment_id uuid GENERATED ALWAYS AS (CASE WHEN kind = 'comment' THEN entity_id END) STORED,
	ADD FOREIGN KEY (workspace_id, project_id, comment_id) REFERENCES comment (workspace_id, project_id, id)
		ON DELETE CASCADE;
-- The cascade of the comments' purges.
CREATE INDEX search_entry_comment ON search_entry (workspace_id, project_id, comment_id) WHERE comment_id IS NOT NULL;
