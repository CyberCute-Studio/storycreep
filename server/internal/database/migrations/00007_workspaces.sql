-- +goose Up

-- Workspaces (PRD FR-TEN-01, §5.5): the form of a workspace's slug, and the
-- mark of the personal workspace that an account's first sign-in gives it.
-- Package workspace creates and changes workspaces, and resolves the role
-- that a user acts with in one.

-- A workspace's slug is safe in a URL and as a file name on every platform,
-- as a board's is (FR-PROJ-02 AC4): groups of lowercase ASCII letters and
-- digits joined by single hyphens, at most 64 characters. Package workspace
-- also refuses the reserved words (FR-TEN-01 AC3), whose list a release may
-- extend, so the database checks only the form. The collation C keeps the
-- ranges to ASCII whatever the database's collation is.
ALTER TABLE workspace ADD CONSTRAINT workspace_slug_form
	CHECK (slug COLLATE "C" ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 64);

-- When the account's first sign-in gave it its personal workspace
-- (FR-TEN-01), or null until then. A sign-in gives one only while this is
-- null, and sets it in the transaction that adds the workspace, so that an
-- account gets a personal workspace once, whatever workspaces it belongs to
-- by then, and whatever later becomes of that one.
ALTER TABLE "user" ADD COLUMN personal_workspace_created_at timestamptz;
