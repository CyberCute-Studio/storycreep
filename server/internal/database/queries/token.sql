-- API tokens (package apitoken, PRD FR-AUTH-04, NFR-SEC-05). api_token is a
-- tenant table, which a transaction of its workspace reads, but for the
-- lookup of a token by the hash of its secret, which a request makes before
-- it knows the token's workspace, through the context bootstrap (ResolveToken,
-- §5.4).

-- name: ResolveToken :one
-- ResolveToken returns the token whose secret has the SHA-256 hash
-- token_hash, with its workspace and its owner, whether or not it is revoked
-- or expired, through the context bootstrap, which a transaction calls
-- before it knows the workspace, and no row if there is none, or if its
-- workspace's deletion is pending, which blocks every access to it at once
-- (FR-TEN-05 AC1). The workspace table has no row-level security, so the
-- transaction reads its status.
SELECT r.token_id::uuid AS token_id, r.workspace_id::uuid AS workspace_id, r.user_id::uuid AS user_id
FROM auth_resolve_token(@token_hash::bytea) r
JOIN workspace w ON w.id = r.workspace_id AND w.status = 'active';

-- name: TokenForUse :one
-- TokenForUse returns what a request made with the token checks of it, in a
-- transaction of its workspace: its project and scopes, whether it is
-- revoked or expired, whether its last use was recorded more than a minute
-- ago, so that TouchToken records this one, and whether its last refusal
-- was, so that DenyToken records this one if it is refused, and its owner's
-- status, whether the owner is an instance admin, and the role of the
-- owner's membership of the workspace, null if they are not a member.
SELECT
    t.project_id, t.scopes,
    (t.revoked_at IS NOT NULL)::boolean AS revoked,
    (t.expires_at IS NOT NULL AND t.expires_at <= now())::boolean AS expired,
    (t.last_used_at IS NULL OR t.last_used_at <= now() - interval '1 minute')::boolean AS stale,
    (t.denied_at IS NULL OR t.denied_at <= now() - interval '1 minute')::boolean AS denial_stale,
    u.status AS owner_status, u.is_instance_admin,
    m.role AS member_role
FROM api_token t
JOIN "user" u ON u.id = t.user_id
LEFT JOIN membership m ON m.workspace_id = t.workspace_id AND m.user_id = t.user_id
WHERE t.workspace_id = @workspace_id AND t.id = @id;

-- name: TouchToken :exec
-- TouchToken records that the token is used now, unless its last use was
-- recorded less than a minute ago, or it has been revoked in the meantime,
-- so that a token is written at most once a minute however often it is used
-- (FR-AUTH-04 AC5). Two requests that both read it as stale write it once:
-- the second waits for the first's row lock, and finds the condition no
-- longer true.
UPDATE api_token SET last_used_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND revoked_at IS NULL
    AND (last_used_at IS NULL OR last_used_at <= now() - interval '1 minute');

-- name: DenyToken :execrows
-- DenyToken records that a request made with the token is refused now,
-- unless a refusal was recorded less than a minute ago, and returns 1 if it
-- records this one, which the audit log then records too, and 0 if not, so
-- that a token adds at most one refusal a minute to the audit log, however
-- often a client repeats a refused request (FR-TEN-07). Two requests that
-- both read the last refusal as stale record one: the second waits for the
-- first's row lock, and finds the condition no longer true.
UPDATE api_token SET denied_at = now()
WHERE workspace_id = @workspace_id AND id = @id
    AND (denied_at IS NULL OR denied_at <= now() - interval '1 minute');

-- name: InsertToken :one
-- InsertToken adds a token, and returns when it was created and when it
-- expires, as the database holds it: to the microsecond.
INSERT INTO api_token (id, workspace_id, user_id, project_id, name, token_hash, scopes, expires_at)
VALUES (@id, @workspace_id, @user_id, sqlc.narg(project_id), @name, @token_hash, @scopes, sqlc.narg(expires_at))
RETURNING created_at, expires_at;

-- name: WorkspaceTokens :many
-- WorkspaceTokens returns the workspace's tokens of the whole workspace that
-- are not revoked, expired ones included, those of the user user_id alone
-- unless it is null, in the order of their IDs, after the token after, or
-- from the first if after is null, without the hashes of their secrets,
-- which nothing that lists them needs.
SELECT id, workspace_id, user_id, project_id, name, scopes, expires_at, last_used_at, created_at FROM api_token
WHERE workspace_id = @workspace_id AND project_id IS NULL AND revoked_at IS NULL
    AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
    AND (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: ProjectTokens :many
-- ProjectTokens returns the tokens narrowed to the project that are not
-- revoked, expired ones included, those of the user user_id alone unless it
-- is null, in the order of their IDs, after the token after, or from the
-- first if after is null, as WorkspaceTokens returns them.
SELECT id, workspace_id, user_id, project_id, name, scopes, expires_at, last_used_at, created_at FROM api_token
WHERE workspace_id = @workspace_id AND project_id = @project_id AND revoked_at IS NULL
    AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
    AND (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: LockToken :one
-- LockToken returns the token that is not revoked, narrowed to the project
-- project_id, or of the whole workspace if it is null, with its owner, and
-- locks it until the transaction ends, or no row if the workspace has no such
-- token.
SELECT id, workspace_id, user_id, project_id FROM api_token
WHERE workspace_id = @workspace_id AND id = @id AND revoked_at IS NULL
    AND project_id IS NOT DISTINCT FROM sqlc.narg(project_id)::uuid
FOR UPDATE;

-- name: RevokeToken :exec
-- RevokeToken revokes the token, after which every request made with it is
-- refused.
UPDATE api_token SET revoked_at = now(), updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND revoked_at IS NULL;

-- name: RevokeUserTokens :many
-- RevokeUserTokens revokes the user's tokens of the workspace that are not
-- revoked, as removing their membership does, and returns them.
UPDATE api_token SET revoked_at = now(), updated_at = now()
WHERE workspace_id = @workspace_id AND user_id = @user_id AND revoked_at IS NULL
RETURNING id, project_id;
