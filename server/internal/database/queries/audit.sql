-- name: InsertAuditEntry :exec
-- InsertAuditEntry appends an entry to the audit log (package audit). It has
-- no RETURNING clause, because the application may add an entry that belongs
-- to no workspace but not read it back.
INSERT INTO audit_entry (
    id, workspace_id, actor_user_id, actor_token_id, action, subject_kind, subject_id, result, ip, user_agent,
    request_id, metadata
) VALUES (
    @id, @workspace_id, @actor_user_id, @actor_token_id, @action, @subject_kind, @subject_id, @result, @ip,
    @user_agent, @request_id, @metadata
);

-- name: AuditEmailHashInput :one
-- AuditEmailHashInput returns the key of the hash that an audit entry records
-- instead of an email address (package audit), and the address in lower
-- case, as citext compares addresses, so that two spellings that the database
-- takes for one address have one hash.
SELECT key, lower(@email::text)::text AS email FROM instance_key WHERE name = 'audit_email';
