-- The sessions of signed-in users (package account, PRD FR-AUTH-02). A
-- session is active while it is not revoked, its absolute end has not
-- passed, and it has been used within the idle timeout. Its absolute end is
-- the earlier of expires_at, which signing in set from the lifetime then in
-- force, and its start plus the lifetime in force now, so that a shorter
-- lifetime applies to the sessions that exist already. The queries take the
-- idle timeout and the lifetime in force as @idle and @lifetime, and compare
-- with the database's clock, which every server shares.

-- name: InsertSession :one
-- InsertSession adds a session of a user who has just signed in, which ends
-- after the lifetime at the latest.
INSERT INTO session (id, user_id, token_hash, user_agent, ip, expires_at)
VALUES (@id, @user_id, @token_hash, @user_agent, @ip, now() + @lifetime::interval)
RETURNING created_at, last_seen_at, expires_at;

-- name: ActiveSession :one
-- ActiveSession returns the session whose secret has the SHA-256 hash
-- token_hash, if it is active and its account is too. It is one lookup by
-- the hash's unique index, which every request made with a session makes,
-- because no server caches whether a session is valid (FR-AUTH-02 AC1).
-- stale says that the session's last use was recorded more than a minute
-- ago, so that TouchSession records this one.
SELECT
    s.id, s.user_id, s.created_at, s.last_seen_at, s.ip, s.user_agent,
    LEAST(s.expires_at, s.created_at + @lifetime::interval)::timestamptz AS ends_at,
    (s.last_seen_at < now() - interval '1 minute')::boolean AS stale
FROM session s
JOIN "user" u ON u.id = s.user_id
WHERE s.token_hash = @token_hash
    AND u.status = 'active'
    AND s.revoked_at IS NULL
    AND s.expires_at > now()
    AND s.created_at + @lifetime::interval > now()
    AND s.last_seen_at + @idle::interval > now();

-- name: TouchSession :one
-- TouchSession records that a session is used now, unless it has been
-- revoked in the meantime.
UPDATE session SET last_seen_at = now()
WHERE id = @id AND revoked_at IS NULL
RETURNING last_seen_at;

-- name: UserSessions :many
-- UserSessions returns a user's active sessions, newest first, from the
-- one after the session before, or from the newest if before is null. A
-- session's ID is a UUIDv7, which sorts by when the session started.
SELECT
    s.id, s.created_at, s.last_seen_at, s.ip, s.user_agent,
    LEAST(s.expires_at, s.created_at + @lifetime::interval)::timestamptz AS ends_at
FROM session s
WHERE s.user_id = @user_id
    AND (sqlc.narg(before)::uuid IS NULL OR s.id < sqlc.narg(before)::uuid)
    AND s.revoked_at IS NULL
    AND s.expires_at > now()
    AND s.created_at + @lifetime::interval > now()
    AND s.last_seen_at + @idle::interval > now()
ORDER BY s.id DESC
LIMIT @max_rows;

-- name: RevokeSession :execrows
-- RevokeSession revokes a user's active session.
UPDATE session s SET revoked_at = now(), updated_at = now()
WHERE s.id = @id
    AND s.user_id = @user_id
    AND s.revoked_at IS NULL
    AND s.expires_at > now()
    AND s.created_at + @lifetime::interval > now()
    AND s.last_seen_at + @idle::interval > now();

-- name: RevokeOtherSessions :many
-- RevokeOtherSessions revokes every active session of a user but the one
-- to keep, and returns their IDs.
UPDATE session s SET revoked_at = now(), updated_at = now()
WHERE s.user_id = @user_id
    AND s.id <> @keep
    AND s.revoked_at IS NULL
    AND s.expires_at > now()
    AND s.created_at + @lifetime::interval > now()
    AND s.last_seen_at + @idle::interval > now()
RETURNING s.id;

-- name: RevokeUserSessions :many
-- RevokeUserSessions revokes every active session of a user, as resetting
-- the password does, and returns their IDs.
UPDATE session s SET revoked_at = now(), updated_at = now()
WHERE s.user_id = @user_id
    AND s.revoked_at IS NULL
    AND s.expires_at > now()
    AND s.created_at + @lifetime::interval > now()
    AND s.last_seen_at + @idle::interval > now()
RETURNING s.id;

-- name: RotateSession :execrows
-- RotateSession gives a user's active session a new secret, whose SHA-256
-- hash is token_hash, so that the old secret no longer finds it. The
-- session keeps its start, and so its absolute end.
UPDATE session s SET token_hash = @token_hash, updated_at = now()
WHERE s.id = @id
    AND s.user_id = @user_id
    AND s.revoked_at IS NULL
    AND s.expires_at > now()
    AND s.created_at + @lifetime::interval > now()
    AND s.last_seen_at + @idle::interval > now();

-- name: DeleteSession :execrows
-- DeleteSession deletes a session, which signing out does (FR-AUTH-02 AC2).
DELETE FROM session WHERE id = @id;

-- name: DeleteEndedSessions :execrows
-- DeleteEndedSessions deletes at most max_rows of the sessions that have
-- ended: revoked, past their absolute end, or unused for the idle timeout.
-- The session cleanup job runs it until it deletes fewer.
DELETE FROM session WHERE id IN (
    SELECT e.id FROM session e
    WHERE e.revoked_at IS NOT NULL
        OR e.expires_at <= now()
        OR e.created_at + @lifetime::interval <= now()
        OR e.last_seen_at + @idle::interval <= now()
    LIMIT @max_rows
);
