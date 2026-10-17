-- Workspaces and their memberships (package workspace, PRD FR-TEN-01, §5.5).
-- The workspace table spans workspaces and has no row-level security, so its
-- queries name the workspace by ID; membership is a tenant table, which a
-- transaction of the workspace reads. A workspace whose deletion is pending
-- is found by none of them (FR-TEN-05).

-- name: InsertWorkspace :execrows
-- InsertWorkspace adds a workspace, unless another workspace has its slug,
-- which it reports as no row, without failing the transaction, so that the
-- caller can try another slug in it.
INSERT INTO workspace (id, name, slug, plan_key, settings)
VALUES (@id, @name, @slug, @plan_key, @settings)
ON CONFLICT (slug) DO NOTHING;

-- name: InsertMembership :exec
-- InsertMembership adds a user's membership of the workspace of the
-- transaction.
INSERT INTO membership (id, workspace_id, user_id, role) VALUES (@id, @workspace_id, @user_id, @role);

-- name: ClaimPersonalWorkspace :one
-- ClaimPersonalWorkspace marks that the active account's sign-in gives it its
-- personal workspace, and returns its display name, which names the
-- workspace. It returns no row if the account has had one already, or is not
-- active, or, if require_verified is set, has not verified its email address
-- (FR-AUTH-03). Of two sign-ins at once, the second waits for the first's
-- transaction, and then finds the mark set.
UPDATE "user" SET personal_workspace_created_at = now(), updated_at = now()
WHERE id = @id AND personal_workspace_created_at IS NULL AND status = 'active'
    AND (email_verified_at IS NOT NULL OR NOT @require_verified::boolean)
RETURNING display_name;

-- name: InstanceAdmin :one
-- InstanceAdmin returns whether the active account is an instance admin, and
-- no row if the account is not active.
SELECT is_instance_admin FROM "user" WHERE id = @id AND status = 'active';

-- name: Workspace :one
-- Workspace returns a workspace.
SELECT id, name, slug, settings, created_at, updated_at FROM workspace WHERE id = @id AND status = 'active';

-- name: WorkspacePlan :one
-- WorkspacePlan returns the plan of a workspace, which sets its limits and
-- features while entitlements are enforced (package entitlements, PRD
-- FR-ENT-01).
SELECT plan_key FROM workspace WHERE id = @id AND status = 'active';

-- name: LockWorkspace :one
-- LockWorkspace returns a workspace, and locks its row against changes until
-- the transaction ends, so that the transaction can change it from what it
-- read. The lock is FOR NO KEY UPDATE, which lets the foreign keys of the
-- workspace's other rows check the workspace meanwhile, so that adding a
-- member or a project to it waits only for a change of the slug, whose
-- unique index makes PostgreSQL lock the row FOR UPDATE.
SELECT id, name, slug, settings, created_at, updated_at FROM workspace WHERE id = @id AND status = 'active'
FOR NO KEY UPDATE;

-- name: MembershipRole :one
-- MembershipRole returns the role of a user's membership of the workspace,
-- and no row if the user is not a member.
SELECT role FROM membership WHERE workspace_id = @workspace_id AND user_id = @user_id;

-- name: LockMembershipRole :one
-- LockMembershipRole is MembershipRole, and locks the membership against
-- changes until the transaction ends, so that a change of the role, or the
-- membership's removal, waits for the operation that the role authorised.
SELECT role FROM membership WHERE workspace_id = @workspace_id AND user_id = @user_id FOR SHARE;

-- name: UpdateWorkspace :one
-- UpdateWorkspace replaces a workspace's name, slug, and default locale,
-- keeping the rest of its settings, and returns when it changed.
UPDATE workspace SET
    name = @name,
    slug = @slug,
    settings = jsonb_set(settings, '{default_locale}', to_jsonb(@default_locale::text)),
    updated_at = now()
WHERE id = @id
RETURNING updated_at;

-- name: UserWorkspaces :many
-- UserWorkspaces returns the workspaces that a user is a member of, with
-- each membership, in the order of the workspaces' IDs, after the workspace
-- after, or from the first if after is null. It reads the memberships
-- through auth_user_memberships, which returns them only to a transaction
-- whose app.user_id is the user (§5.4).
SELECT
    m.membership_id::uuid AS membership_id, m.role::text AS role,
    w.id, w.name, w.slug, w.settings, w.created_at, w.updated_at
FROM auth_user_memberships(@user_id::uuid) m
JOIN workspace w ON w.id = m.workspace_id
WHERE w.status = 'active'
    AND (sqlc.narg(after)::uuid IS NULL OR w.id > sqlc.narg(after)::uuid)
ORDER BY w.id
LIMIT @max_rows;

-- name: WorkspaceBySlug :one
-- WorkspaceBySlug returns the ID of the workspace with the slug, for the
-- commands of the operator (package cli).
SELECT id FROM workspace WHERE slug = @slug AND status = 'active';

-- name: LockMembership :one
-- LockMembership returns a user's membership of the workspace, and locks it
-- against changes until the transaction ends, and no row if the user is not
-- a member.
SELECT id, role FROM membership WHERE workspace_id = @workspace_id AND user_id = @user_id FOR UPDATE;

-- name: UpdateMembershipRole :exec
-- UpdateMembershipRole changes the role of a membership of the workspace.
UPDATE membership SET role = @role, updated_at = now() WHERE workspace_id = @workspace_id AND id = @id;

-- name: CountOwners :one
-- CountOwners returns how many owners the workspace has whose accounts are
-- active, which are the ones that count for the rule that a workspace keeps
-- an owner (PRD FR-TEN-02 AC2): an owner whose account's deletion is pending
-- does not (FR-GDPR-02 AC4).
SELECT count(*) FROM membership m JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id = @workspace_id AND m.role = 'owner' AND u.status = 'active';

-- name: WorkspaceMembers :many
-- WorkspaceMembers returns the memberships of the workspace, guests' included,
-- with each member's display name and email address, in the order of the
-- memberships' IDs, after the membership after, or from the first if after
-- is null.
SELECT m.id, m.user_id, m.role, m.joined_at, u.display_name, u.email::text AS email
FROM membership m
JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id = @workspace_id
    AND (sqlc.narg(after)::uuid IS NULL OR m.id > sqlc.narg(after)::uuid)
ORDER BY m.id
LIMIT @max_rows;

-- name: LockMembershipByID :one
-- LockMembershipByID returns the member and the role of a membership of the
-- workspace, and locks it against changes until the transaction ends, and no
-- row if the workspace has no such membership.
SELECT user_id, role FROM membership WHERE workspace_id = @workspace_id AND id = @id FOR UPDATE;

-- name: CountOtherOwners :one
-- CountOtherOwners returns how many owners the workspace has, besides the
-- membership id, whose accounts are active: the owners that would be left
-- for the rule that a workspace keeps an owner (PRD FR-TEN-02 AC2) if that
-- membership were no longer an owner's (CountOwners).
SELECT count(*) FROM membership m JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id = @workspace_id AND m.role = 'owner' AND u.status = 'active' AND m.id <> @id;

-- name: DeleteMembership :exec
-- DeleteMembership removes a membership of the workspace, and with it the
-- membership's project grants.
DELETE FROM membership WHERE workspace_id = @workspace_id AND id = @id;

-- name: JoinWorkspace :execrows
-- JoinWorkspace makes a user a member of the workspace with a role, as
-- accepting an invitation of the user invited_by does, unless the user is a
-- member already, which it reports as no row, without failing the
-- transaction.
INSERT INTO membership (id, workspace_id, user_id, role, invited_by)
VALUES (@id, @workspace_id, @user_id, @role, @invited_by)
ON CONFLICT (user_id, workspace_id) DO NOTHING;

-- name: MembershipOf :one
-- MembershipOf returns a user's membership of the workspace, and no row if
-- the user is not a member.
SELECT id, role FROM membership WHERE workspace_id = @workspace_id AND user_id = @user_id;
