-- name: ProjectWorkspace :one
-- ProjectWorkspace returns the workspace of the project, through the context
-- bootstrap (PRD §5.4), and no row if there is no such project, or if its
-- workspace's deletion is pending, which blocks every access to it at once
-- (FR-TEN-05 AC1), as it does to the workspaces that the queries of package
-- workspace find. A transaction without a workspace calls it, to find the
-- workspace that the request's transaction then acts in; the workspace table
-- has no row-level security, so the transaction reads its status.
SELECT w.id FROM workspace w
WHERE w.id = route_project_workspace(@project_id::uuid) AND w.status = 'active';

-- name: ProjectGrantRole :one
-- ProjectGrantRole returns the role of the membership's grant on the project
-- (FR-TEN-04), and no row if it has none. It locks nothing, as MembershipOf,
-- which a batch of commands reads the membership with, does not (package
-- commands says why).
SELECT role FROM project_grant
WHERE workspace_id = @workspace_id AND membership_id = @membership_id AND project_id = @project_id;

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
