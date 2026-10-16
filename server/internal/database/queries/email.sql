-- The tokens of the links that verify an email address and reset a password
-- (package account, PRD FR-AUTH-03). The email_token table spans workspaces
-- and has no row-level security. A token is used once, while it has not
-- expired, and only by an account that is still active.

-- name: InsertEmailToken :one
-- InsertEmailToken adds a token of an account, which expires after lifetime,
-- with the SHA-256 hash of its secret, or with none, which the job that
-- emails its link adds.
INSERT INTO email_token (id, kind, user_id, email, locale, token_hash, expires_at)
VALUES (@id, @kind, @user_id, @email, @locale, @token_hash, now() + @lifetime::interval)
RETURNING expires_at;

-- name: InsertPasswordResetRequest :exec
-- InsertPasswordResetRequest adds the token of a request to reset the
-- password of the active account that the address belongs to, or, if it
-- belongs to none, a token without an account, which the email job deletes,
-- so that the request does the same work either way (FR-AUTH-03 AC3).
INSERT INTO email_token (id, kind, user_id, locale, expires_at)
SELECT @id, 'password_reset', (SELECT u.id FROM "user" u WHERE u.email = @email AND u.status = 'active'), @locale,
    now() + @lifetime::interval;

-- name: LockEmailTokenToSend :one
-- LockEmailTokenToSend returns what the email of a token's link needs, if
-- the token has neither been used nor expired, and its account is active,
-- and, for a verification, still has the address that it verifies, which is
-- not verified yet. The email's locale is the account's preferred one, or
-- else the one of the request that asked for the link. It locks the token,
-- so that the job that sends it and a use of it run one after the other.
SELECT t.kind, COALESCE(u.locale, t.locale) AS locale, t.expires_at, u.id AS user_id, u.email, u.display_name
FROM email_token t
JOIN "user" u ON u.id = t.user_id
WHERE t.id = @id
    AND t.used_at IS NULL
    AND t.expires_at > now()
    AND u.status = 'active'
    AND (t.kind <> 'email_verification' OR t.email = u.email AND u.email_verified_at IS NULL)
FOR UPDATE OF t;

-- name: SetEmailTokenHash :exec
-- SetEmailTokenHash gives a token the SHA-256 hash of its secret, which the
-- email that the job sends holds, in place of the hash of any secret that an
-- earlier attempt of the job sent.
UPDATE email_token SET token_hash = @token_hash, updated_at = now() WHERE id = @id;

-- name: DeleteUnclaimedToken :exec
-- DeleteUnclaimedToken deletes the token of a request to reset the password
-- of an address that belongs to no active account, which the email job
-- sends no email for.
DELETE FROM email_token WHERE id = @id AND user_id IS NULL;

-- name: LockEmailToken :one
-- LockEmailToken returns the token whose secret has the SHA-256 hash
-- token_hash, whether it has been used or has expired, whether its account
-- is active, and, for a verification, whether the address that it verifies
-- is still the account's, and locks the token, so that of two uses at once,
-- the second waits for the first, and then finds it used (FR-AUTH-03 AC2).
SELECT
    t.id, t.kind, t.user_id, t.email,
    (t.used_at IS NOT NULL)::boolean AS used,
    (t.expires_at <= now())::boolean AS expired,
    (u.status = 'active')::boolean AS active,
    (t.email IS NOT DISTINCT FROM u.email)::boolean AS same_email
FROM email_token t
JOIN "user" u ON u.id = t.user_id
WHERE t.token_hash = @token_hash
FOR UPDATE OF t;

-- name: UseEmailToken :exec
-- UseEmailToken marks a token used. The table keeps it until it expires, so
-- that a second use is told that it was used.
UPDATE email_token SET used_at = now(), updated_at = now() WHERE id = @id;

-- name: DeleteResetTokens :exec
-- DeleteResetTokens deletes the password-reset tokens of an account that
-- have not been used, which a change of its password invalidates, by
-- whatever route it changes (FR-AUTH-03 AC4). Using one is then answered as
-- using a token that does not exist.
DELETE FROM email_token WHERE user_id = @user_id AND kind = 'password_reset' AND used_at IS NULL;

-- name: VerifyEmail :execrows
-- VerifyEmail records that the active account's address, which must still
-- be the one that the token verifies, is verified, unless it was already.
UPDATE "user" SET email_verified_at = now(), updated_at = now()
WHERE id = @id AND email = @email AND status = 'active' AND email_verified_at IS NULL;

-- name: SetPasswordHash :execrows
-- SetPasswordHash replaces the hash of the active account's password, as
-- resetting the password does, whatever the hash was.
UPDATE "user" SET password_hash = @password_hash, updated_at = now() WHERE id = @id AND status = 'active';

-- name: UserEmail :one
-- UserEmail returns the address of an active account, and whether it is
-- verified.
SELECT email, (email_verified_at IS NOT NULL)::boolean AS verified FROM "user" WHERE id = @id AND status = 'active';

-- name: DeleteExpiredEmailTokens :execrows
-- DeleteExpiredEmailTokens deletes at most max_rows of the tokens that have
-- expired, used or not. The token cleanup job runs it until it deletes
-- fewer.
DELETE FROM email_token WHERE id IN (
    SELECT e.id FROM email_token e WHERE e.expires_at <= now() LIMIT @max_rows
);
