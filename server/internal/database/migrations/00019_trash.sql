-- +goose Up

-- A project's trash (PRD FR-PROJ-06), which package trash lists and purges.
-- A row of the project's content with deleted_at is in the trash: deleting
-- it to the trash sets deleted_at, and restoring it clears it. Nothing but
-- its restoration changes it there, and nothing that reads the project's
-- content finds it, neither the API's reads nor, as they are added, the
-- search, the exports, and the snapshots (AC2), which read the content
-- through the live views below rather than the tables. Once it has been in
-- the trash for 30 days, the purge deletes it for good. A project that is
-- itself in its workspace's trash is not found at all (package project), so
-- the views need not leave out its rows.
--
-- Each table whose rows go to the trash has a live view, named live_ and
-- the table's name, of the table's columns, in the table's order, over the
-- rows that are not in the trash, and is listed and purged by package
-- trash, which its tests hold every such table to. The views read the
-- tables as the user that reads the views (security_invoker), so that
-- row-level security holds them to the transaction's workspace as it holds
-- the tables.

-- A folder is deleted, to the trash, only once it holds nothing that is not
-- in the trash, and is not listed there: restoring a board restores the
-- deleted folders above it (package board).
CREATE VIEW live_folder WITH (security_invoker = true) AS
	SELECT * FROM folder WHERE deleted_at IS NULL;

CREATE VIEW live_board WITH (security_invoker = true) AS
	SELECT * FROM board WHERE deleted_at IS NULL;

GRANT SELECT ON live_folder, live_board TO storycreep_app, storycreep_maintenance;

-- The rows of each project's trash, by when they were deleted, which the
-- trash's list reads, newest first, and its purge, oldest first.
CREATE INDEX folder_trash ON folder (workspace_id, project_id, deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX board_trash ON board (workspace_id, project_id, deleted_at) WHERE deleted_at IS NOT NULL;

-- The purge deletes the boards and the folders whose time in the trash has
-- passed, in transactions of their workspace, as the application.
GRANT DELETE ON folder, board TO storycreep_app;
