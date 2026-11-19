-- +goose Up

-- The notifications of the accounts (PRD FR-COMMENT-02, §5.3, plan item 75),
-- which package notification keeps: a member of a project whom a comment
-- mentions (richtext.Member), as "@Tomas, does the lighthouse exist yet?", is
-- told of it in the notification centre of their account, across every
-- workspace that they are a member of, and in their open sessions over the
-- real-time channel (FR-RT-01). Only mentions notify: a reply, or a thread's
-- resolution, notifies nobody.
--
-- A notification is of one comment and one recipient, whom the comment's
-- commands notify as the comment first mentions them, as it is posted or an
-- edit adds the mention, never twice, and never its author (package comment).
-- It is shown while its comment is shown, as live_comment holds it, while the
-- recipient reads the project, by a membership of its workspace and its
-- grant, and while the project is not in the trash, and is back as it was,
-- read or not, once all of that holds again; an edit that takes the mention
-- out leaves it. It is no part of the project's content, which no command
-- changes and the trash does not list: the purges of its comment, of the
-- comment's thread's target, of its project, and of its workspace delete it
-- by the cascades, and so does the deletion of the recipient's account, while
-- that of its actor, who mentioned the recipient, leaves it without one.
CREATE TABLE notification (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	-- The recipient.
	user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
	comment_id uuid NOT NULL,
	-- Who mentioned the recipient, the comment's author, or null once their
	-- account is deleted.
	actor_id uuid REFERENCES "user" ON DELETE SET NULL,
	-- What the notification tells of: a mention, the one kind so far.
	kind text NOT NULL CHECK (kind = 'mention'),
	-- When the comment mentioned the recipient, as the server's clock has it
	-- as the batch applies, by which the centre orders its notifications,
	-- newest first.
	created_at timestamptz NOT NULL DEFAULT now(),
	-- When the recipient read it, or null while it is unread.
	read_at timestamptz,
	UNIQUE (workspace_id, id),
	-- A comment notifies each recipient once.
	UNIQUE (workspace_id, comment_id, user_id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, comment_id) REFERENCES comment (workspace_id, project_id, id)
		ON DELETE CASCADE
);
-- A recipient's notifications of a workspace, newest first, which the centre
-- reads a page at a time.
CREATE INDEX notification_inbox ON notification (workspace_id, user_id, created_at, id);
-- A recipient's unread notifications of a workspace, which the centre's count
-- reads.
CREATE INDEX notification_unread ON notification (workspace_id, user_id) WHERE read_at IS NULL;
-- The cascades of the accounts' deletions.
CREATE INDEX notification_user ON notification (user_id);
CREATE INDEX notification_actor ON notification (actor_id);
-- The notifications of each comment, which its deletion and its restoration
-- tell their recipients of, and the cascade of the comments' purges.
CREATE INDEX notification_comment ON notification (workspace_id, project_id, comment_id);

ALTER TABLE notification ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON notification TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The application adds the rows, as a comment mentions a member, and marks
-- them read; the cascades alone delete them.
GRANT SELECT, INSERT, UPDATE ON notification TO storycreep_app;
GRANT SELECT ON notification TO storycreep_maintenance;
