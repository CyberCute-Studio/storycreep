-- Project grants (package project, PRD FR-TEN-04, §5.5): a membership's role
-- on one project of its workspace, which replaces the membership's role
-- there. project_grant is a tenant table, which a transaction of its
-- workspace reads. ProjectGrantRole, which the role resolutions read, is in
-- commands.sql.

-- name: LockMembershipOf :one
-- LockMembershipOf is MembershipOf, and locks the membership FOR SHARE until
-- the transaction ends, so that a change of its role, or its removal, waits
-- for the operation that its role authorised.
SELECT id, role FROM membership WHERE workspace_id = @workspace_id AND user_id = @user_id FOR SHARE;

-- name: ProjectGrant :one
-- ProjectGrant returns the membership's grant on the project, with the
-- member, and no row if it has none.
SELECT sqlc.embed(g), m.user_id
FROM project_grant g
JOIN membership m ON m.workspace_id = g.workspace_id AND m.id = g.membership_id
WHERE g.workspace_id = @workspace_id AND g.membership_id = @membership_id AND g.project_id = @project_id;

-- name: ProjectGrants :many
-- ProjectGrants returns the grants on the project, each with its member, in
-- the order of their memberships' IDs, after the membership after, or from
-- the first if after is null.
SELECT sqlc.embed(g), m.user_id
FROM project_grant g
JOIN membership m ON m.workspace_id = g.workspace_id AND m.id = g.membership_id
WHERE g.workspace_id = @workspace_id AND g.project_id = @project_id
    AND (sqlc.narg(after)::uuid IS NULL OR g.membership_id > sqlc.narg(after)::uuid)
ORDER BY g.membership_id
LIMIT @max_rows;

-- name: MembershipGrants :many
-- MembershipGrants returns the membership's grants on the workspace's
-- projects that are not in the trash, each with the member, in the order of
-- the projects' IDs, after the project after, or from the first if after is
-- null.
SELECT sqlc.embed(g), m.user_id
FROM project_grant g
JOIN membership m ON m.workspace_id = g.workspace_id AND m.id = g.membership_id
JOIN project p ON p.workspace_id = g.workspace_id AND p.id = g.project_id AND p.deleted_at IS NULL
WHERE g.workspace_id = @workspace_id AND g.membership_id = @membership_id
    AND (sqlc.narg(after)::uuid IS NULL OR g.project_id > sqlc.narg(after)::uuid)
ORDER BY g.project_id
LIMIT @max_rows;

-- name: SetProjectGrant :one
-- SetProjectGrant gives the membership the role on the project: it adds the
-- membership's grant there, with the ID id, or changes the role of the grant
-- that it has, and returns the grant.
INSERT INTO project_grant (id, workspace_id, membership_id, project_id, role)
VALUES (@id, @workspace_id, @membership_id, @project_id, @role)
ON CONFLICT (workspace_id, membership_id, project_id) DO UPDATE SET role = EXCLUDED.role, updated_at = now()
RETURNING *;

-- name: DeleteProjectGrant :one
-- DeleteProjectGrant removes the membership's grant on the project, and
-- returns its role, or no row if it has none.
DELETE FROM project_grant
WHERE workspace_id = @workspace_id AND membership_id = @membership_id AND project_id = @project_id
RETURNING role;
