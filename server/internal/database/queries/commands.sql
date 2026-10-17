-- name: ProjectWorkspace :one
-- ProjectWorkspace returns the workspace of the project, through the context
-- bootstrap (PRD §5.4), or null if there is no such project. A transaction
-- without a workspace calls it, to find the workspace that the request's
-- transaction then acts in.
SELECT route_project_workspace(@project_id::uuid)::uuid AS workspace_id;

-- name: ShareMembership :one
-- ShareMembership returns a user's membership of the workspace, and no row if
-- the user is not a member. It locks the membership FOR SHARE until the
-- transaction ends, so that a change of its role, or its removal, waits for
-- the batch of commands that the role authorised.
SELECT id, role FROM membership WHERE workspace_id = @workspace_id AND user_id = @user_id FOR SHARE;

-- name: ShareProjectGrant :one
-- ShareProjectGrant returns the role of the membership's grant on the
-- project (FR-TEN-04), and no row if it has none, and locks the grant FOR
-- SHARE until the transaction ends, as ShareMembership locks the membership.
SELECT role FROM project_grant
WHERE workspace_id = @workspace_id AND membership_id = @membership_id AND project_id = @project_id
FOR SHARE;

-- name: LockProjectSeq :one
-- LockProjectSeq returns the seq of the project's latest operation, and locks
-- the project's row until the transaction ends, so that the project's
-- batches of commands are applied one at a time, in the order of their seqs.
-- The lock is FOR NO KEY UPDATE, which lets the foreign keys of other rows
-- check the project meanwhile.
SELECT seq FROM project WHERE workspace_id = @workspace_id AND id = @id FOR NO KEY UPDATE;

-- name: SetProjectSeq :exec
-- SetProjectSeq sets the seq of the project's latest operation.
UPDATE project SET seq = @seq WHERE workspace_id = @workspace_id AND id = @id;

-- name: InsertOperation :exec
-- InsertOperation appends a batch of commands to the project's operation log.
INSERT INTO operation (id, workspace_id, project_id, seq, command_ids, actor_user_id, actor_token_id, type,
    payload, inverse, entity_refs)
VALUES (@id, @workspace_id, @project_id, @seq, @command_ids::uuid[], @actor_user_id, @actor_token_id, @type,
    @payload, @inverse, @entity_refs);

-- name: InsertAppliedCommands :exec
-- InsertAppliedCommands records the commands of an applied batch, with its
-- seq.
INSERT INTO applied_command (workspace_id, project_id, command_id, seq)
SELECT @workspace_id, @project_id, c.command_id, @seq
FROM unnest(@command_ids::uuid[]) AS c (command_id);
