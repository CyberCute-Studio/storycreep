-- Projects (package project, PRD FR-PROJ-01, §5.3). project is a tenant
-- table, which a transaction of its workspace reads. A deleted project is in
-- the trash until its hard_delete_after, and none of these queries finds one
-- after that, as though the purge job had deleted it already.

-- name: InsertProject :one
-- InsertProject adds a project, and returns its timestamps.
INSERT INTO project (id, workspace_id, name, description, settings, created_by, updated_by)
VALUES (@id, @workspace_id, @name, @description, @settings, @created_by, @created_by)
RETURNING created_at, updated_at;

-- name: Project :one
-- Project returns a project that is not in the trash, and no row if the
-- workspace has no such project, or it is.
SELECT sqlc.embed(project)
FROM project
WHERE workspace_id = @workspace_id AND id = @id AND deleted_at IS NULL;

-- name: ProjectAny :one
-- ProjectAny returns a project whether or not it is in the trash, but no row
-- once its time in the trash has passed, for the commands of the pipeline,
-- which applies some of them to a project in the trash.
SELECT sqlc.embed(project)
FROM project
WHERE workspace_id = @workspace_id AND id = @id AND (hard_delete_after IS NULL OR hard_delete_after > now());

-- name: Projects :many
-- Projects returns the workspace's projects that are not in the trash, in
-- the order of their IDs, after the project after, or from the first if after
-- is null, each with the role of the grant of the membership membership_id
-- on it, if it has one. If granted_only is set, as for a guest, who sees only
-- the projects granted to them (FR-TEN-04 AC2), it returns only those.
SELECT sqlc.embed(p), g.role AS grant_role
FROM project p
LEFT JOIN project_grant g
    ON g.workspace_id = p.workspace_id AND g.project_id = p.id AND g.membership_id = sqlc.narg(membership_id)::uuid
WHERE p.workspace_id = @workspace_id AND p.deleted_at IS NULL
    AND (sqlc.narg(after)::uuid IS NULL OR p.id > sqlc.narg(after)::uuid)
    AND (NOT @granted_only::boolean OR g.role IS NOT NULL)
ORDER BY p.id
LIMIT @max_rows;

-- name: TrashedProjects :many
-- TrashedProjects returns the workspace's projects in the trash that can
-- still be restored, in the order of their IDs, after the project after, or
-- from the first if after is null.
SELECT sqlc.embed(project)
FROM project
WHERE workspace_id = @workspace_id AND deleted_at IS NOT NULL AND hard_delete_after > now()
    AND (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: ProjectRevs :many
-- ProjectRevs returns the revision of the project id, if it is among ids, for
-- the pipeline's check of the revisions that a batch of the project expects.
SELECT id, rev FROM project
WHERE workspace_id = @workspace_id AND id = @id AND id = ANY(@ids::uuid[])
    AND (hard_delete_after IS NULL OR hard_delete_after > now());

-- name: UpdateProjectName :one
-- UpdateProjectName renames the project, unless it has the name already, and
-- returns its new revision, or no row if it does.
UPDATE project SET name = @name, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND id = @id AND name <> @name
RETURNING rev;

-- name: UpdateProjectDescription :one
-- UpdateProjectDescription changes the project's description, unless it has
-- the description already, and returns its new revision, or no row if it
-- does.
UPDATE project SET description = @description, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND id = @id AND description <> @description
RETURNING rev;

-- name: UpdateProjectSettings :one
-- UpdateProjectSettings replaces the project's settings, unless they are
-- those already, and returns its new revision, or no row if they are.
UPDATE project SET settings = @settings, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND id = @id AND settings <> @settings
RETURNING rev;

-- name: ArchiveProject :one
-- ArchiveProject archives the project, unless it is archived already, and
-- returns its new revision, or no row if it is.
UPDATE project SET archived_at = now(), rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND id = @id AND archived_at IS NULL
RETURNING rev;

-- name: UnarchiveProject :one
-- UnarchiveProject unarchives the project, unless it is not archived, and
-- returns its new revision, or no row if it is not.
UPDATE project SET archived_at = NULL, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND id = @id AND archived_at IS NOT NULL
RETURNING rev;

-- name: TrashProject :one
-- TrashProject deletes the project to the trash, where it stays for
-- trash_days days, until hard_delete_after, unless it is there already, and
-- returns its new revision, or no row if it is.
UPDATE project SET deleted_at = now(), hard_delete_after = now() + make_interval(days => @trash_days::integer),
    rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND id = @id AND deleted_at IS NULL
RETURNING rev;

-- name: RestoreProject :one
-- RestoreProject takes the project out of the trash, unless it is not there,
-- and returns its new revision, or no row if it is not.
UPDATE project SET deleted_at = NULL, hard_delete_after = NULL, rev = rev + 1, updated_at = now(),
    updated_by = @updated_by
WHERE workspace_id = @workspace_id AND id = @id AND deleted_at IS NOT NULL
RETURNING rev;

-- name: PurgeProject :one
-- PurgeProject deletes one of the workspace's projects whose time in the
-- trash has passed, the one whose time passed first, and returns its ID, or
-- no row if there is none. Deleting a project deletes every row that refers
-- to it, by the cascade of their foreign keys. It skips a project whose row a
-- transaction has locked, as a batch of commands of it does while it holds
-- the project's lock (LockProjectSeq), which a later run purges.
DELETE FROM project d
WHERE d.workspace_id = @workspace_id AND d.id = (
    SELECT p.id FROM project p
    WHERE p.workspace_id = @workspace_id AND p.deleted_at IS NOT NULL AND p.hard_delete_after <= now()
    ORDER BY p.hard_delete_after, p.id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING d.id;
