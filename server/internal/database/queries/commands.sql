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

-- name: ProjectInTrash :one
-- ProjectInTrash returns whether the project is in the trash (FR-PROJ-01),
-- and no row if it does not exist, its time in the trash has passed, or its
-- workspace's deletion is pending (FR-TEN-05 AC1), which a batch of commands
-- reads, in one statement, before it authorises the actor, so that a deletion
-- of the workspace requested since the batch found the project refuses the
-- batch, whatever the actor's role would allow, and a project in the trash is
-- not found by a batch that may not apply to it, whoever sends it. It locks
-- nothing.
SELECT (p.deleted_at IS NOT NULL)::boolean AS deleted
FROM project p
JOIN workspace w ON w.id = p.workspace_id AND w.status = 'active'
WHERE p.workspace_id = @workspace_id AND p.id = @id
    AND (p.hard_delete_after IS NULL OR p.hard_delete_after > now());

-- name: LockProjectSeq :one
-- LockProjectSeq returns the seq of the project's latest operation, and
-- whether it is archived and in the trash, as ProjectInTrash does, and locks
-- the project's row until the transaction ends, so that the project's
-- batches of commands are applied one at a time, in the order of their seqs.
-- The lock is FOR NO KEY UPDATE, which lets the foreign keys of other rows
-- check the project meanwhile.
SELECT seq, (archived_at IS NOT NULL)::boolean AS archived, (deleted_at IS NOT NULL)::boolean AS deleted
FROM project
WHERE workspace_id = @workspace_id AND id = @id AND (hard_delete_after IS NULL OR hard_delete_after > now())
FOR NO KEY UPDATE;

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

-- name: BatchRecord :one
-- BatchRecord returns the idempotency record of the batch whose first
-- command is command_id (FR-API-03): the commands of the batch, in order,
-- and what it was answered with, and no row if the command is no batch's
-- first, or has no record, as when its record has been pruned.
SELECT command_ids::uuid[] AS command_ids, response::jsonb AS response
FROM idempotency_record
WHERE workspace_id = @workspace_id AND project_id = @project_id AND command_id = @command_id
    AND command_id = batch_command_id;

-- name: AppliedCommands :many
-- AppliedCommands returns those of the commands that were applied, each with
-- the seq of the batch that applied it.
SELECT command_id, seq FROM applied_command
WHERE workspace_id = @workspace_id AND project_id = @project_id AND command_id = ANY(@command_ids::uuid[]);

-- name: DeleteBatchRecords :exec
-- DeleteBatchRecords deletes the idempotency records of the batches that
-- any of the commands is in, whole: the row of each batch's first command,
-- whose deletion deletes the batch's other rows. A batch whose commands are
-- applied anew no longer answers as the batch that they were refused in.
DELETE FROM idempotency_record d
WHERE d.workspace_id = @workspace_id AND d.project_id = @project_id AND d.command_id IN (
    SELECT r.batch_command_id FROM idempotency_record r
    WHERE r.workspace_id = @workspace_id AND r.project_id = @project_id AND r.command_id = ANY(@command_ids::uuid[]));

-- name: InsertBatchRecord :exec
-- InsertBatchRecord records what the batch of the commands, in order, was
-- answered with: a row for each command, which points to the first's, which
-- holds the commands and the answer.
INSERT INTO idempotency_record (workspace_id, project_id, command_id, batch_command_id, command_ids, response)
SELECT @workspace_id, @project_id, c.command_id, (@command_ids::uuid[])[1],
    CASE WHEN c.n = 1 THEN @command_ids::uuid[] END, CASE WHEN c.n = 1 THEN @response::jsonb END
FROM unnest(@command_ids::uuid[]) WITH ORDINALITY AS c (command_id, n);

-- name: OperationEntityRefs :many
-- OperationEntityRefs returns the entities that the project's operations of
-- the seqs changed, as their entity_refs hold them, in the order of the
-- seqs.
SELECT entity_refs FROM operation
WHERE workspace_id = @workspace_id AND project_id = @project_id AND seq = ANY(@seqs::bigint[])
ORDER BY seq;

-- name: PruneBatchRecords :execrows
-- PruneBatchRecords deletes at most max_rows of the workspace's batches'
-- idempotency records that are older than retention, each whole, by the row
-- of its first command, and returns how many batches it deleted. It skips a
-- record that a batch of commands holds, as one that deletes it, which a
-- later run deletes.
DELETE FROM idempotency_record d
WHERE d.workspace_id = @workspace_id AND (d.project_id, d.command_id) IN (
    SELECT r.project_id, r.command_id FROM idempotency_record r
    WHERE r.workspace_id = @workspace_id AND r.command_id = r.batch_command_id
        AND r.created_at < now() - @retention::interval
    LIMIT @max_rows
    FOR UPDATE SKIP LOCKED);
