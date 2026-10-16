-- The tokens of the links that verify an email address and reset a password
-- (package account, PRD FR-AUTH-03). The email_token table spans workspaces
-- and has no row-level security. A token is used once, while it has not
-- expired, and only by an account that is still active.

-- name: InsertEmailToken :one
-- InsertEmailToken adds a token that expires after lifetime, of an account,
-- or of none, which the job that emails its link deletes, with the SHA-256
-- hash of its secret, or with none, which that job adds, when the link's
-- lifetime starts again (SetEmailTokenHash).
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

-- name: LockEmailTokenAccount :exec
-- LockEmailTokenAccount locks the account of the token id, if it has one, as
-- the job that emails the token's link does before it locks the token, so
-- that two jobs of one account's links run one after the other, and the
-- second finds the first's email (LockEmailTokenToSend). Whatever locks an
-- account's row and its tokens locks the row first, so that none waits for
-- another that waits for it.
SELECT u.id FROM "user" u JOIN email_token t ON t.user_id = u.id WHERE t.id = @id FOR NO KEY UPDATE OF u;

-- name: LockEmailTokenAccountByHash :exec
-- LockEmailTokenAccountByHash locks the account of the token whose secret
-- has the SHA-256 hash token_hash, if there is one, as using a link that
-- verifies an address does before it locks the token
-- (LockEmailTokenAccount).
SELECT u.id FROM "user" u JOIN email_token t ON t.user_id = u.id WHERE t.token_hash = @token_hash
FOR NO KEY UPDATE OF u;

-- name: LockEmailTokenToSend :one
-- LockEmailTokenToSend returns what the email of a token's link needs, if
-- the token has not been used, was asked for less than the lifetime of its
-- kind of link ago, password_reset_lifetime or verification_lifetime, and
-- its account is active, and, for a verification, still has the address
-- that it verifies, which is not verified yet, and if the account was
-- emailed no other link of the kind in the last resend_interval that still
-- works: one that has not been used or expired, and, for a verification,
-- verifies the account's address. The email's locale is the account's
-- preferred one, or else the one of the request that asked for the link. It
-- locks the token, so that the job that sends it and a use of it run one
-- after the other.
SELECT t.kind, COALESCE(u.locale, t.locale) AS locale, u.email
FROM email_token t
JOIN "user" u ON u.id = t.user_id
WHERE t.id = @id
    AND t.used_at IS NULL
    AND t.created_at + CASE t.kind WHEN 'password_reset' THEN @password_reset_lifetime::interval
        ELSE @verification_lifetime::interval END > now()
    AND u.status = 'active'
    AND (t.kind <> 'email_verification' OR t.email = u.email AND u.email_verified_at IS NULL)
    AND NOT EXISTS (
        SELECT 1 FROM email_token o
        WHERE o.user_id = t.user_id AND o.kind = t.kind AND o.id <> t.id
            AND o.sent_at > now() - @resend_interval::interval
            AND o.used_at IS NULL
            AND o.expires_at > now()
            AND (o.kind <> 'email_verification' OR o.email = u.email)
    )
FOR UPDATE OF t;

-- name: SetEmailTokenHash :exec
-- SetEmailTokenHash gives a token the SHA-256 hash of its secret, which the
-- email that the job sends holds, in place of the hash of any secret that an
-- earlier attempt of the job sent, records that the job made the email then,
-- and makes the token expire lifetime from now, so that the link lasts as
-- long as the email says, however late the job sends it.
UPDATE email_token SET token_hash = @token_hash, sent_at = now(), expires_at = now() + @lifetime::interval,
    updated_at = now()
WHERE id = @id;

-- name: DeleteUnclaimedToken :exec
-- DeleteUnclaimedToken deletes a token without an account, which the email
-- job sends no email for: that of a request to reset the password of an
-- address that belongs to no active account, or of a registration of an
-- address that has an account already.
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
