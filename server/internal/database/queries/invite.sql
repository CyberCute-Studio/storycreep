-- Invitations to a workspace (package invite, PRD FR-TEN-02). invite is a
-- tenant table, which a transaction of the workspace reads and writes. A
-- request that holds an invitation's link finds its workspace first, through
-- auth_resolve_invite (§5.4), and then reads the invitation in a transaction
-- of that workspace. An invitation is open until it is accepted or revoked,
-- and works while it is open and has not expired.
--
-- sent_at is when the job last emailed a link of the invitation. It stays
-- when the invitation is sent anew to be emailed again, so that the next
-- email waits for the resend interval after it (InviteEmailDue), but the
-- invitation's admins see it only while the link that the invitation has is
-- the one that was emailed then: while its token_hash is set.
--
-- link_holder is the account that the request that made the invitation or
-- sent it anew gave its link to as text, while the invitation has that link,
-- so that removing the account's membership revokes the invitation, as does
-- a change of its role, or the end of its instance admin access, that leaves
-- it a role that may not give the invitation's (RevokeMemberInvites).
--
-- email_request is the request that last asked for the invitation's link to
-- be emailed, whose job alone emails it (LockInviteToSend), so that a job of
-- an earlier request, which a later one superseded, sends nothing, however
-- often it is retried.

-- name: InsertInvite :one
-- InsertInvite adds an invitation that expires after lifetime: to the
-- address email, or a link invitation without one, with the SHA-256 hash of
-- its secret, whose link the account link_holder is given as text, or
-- without one, which the job of the request email_request that emails its
-- link adds. It adds nothing, and returns no row, if the address has an open
-- invitation of the workspace already, without failing the transaction.
INSERT INTO invite (id, workspace_id, email, role, token_hash, expires_at, created_by, link_holder, email_request)
VALUES (@id, @workspace_id, sqlc.narg(email), @role, sqlc.narg(token_hash), now() + @lifetime::interval,
    @created_by, sqlc.narg(link_holder), sqlc.narg(email_request))
ON CONFLICT (workspace_id, email) WHERE email IS NOT NULL AND accepted_at IS NULL AND revoked_at IS NULL
DO NOTHING
RETURNING expires_at, created_at;

-- name: AddressIsMember :one
-- AddressIsMember returns whether the account that the address belongs to
-- is a member of the workspace.
SELECT EXISTS (
    SELECT 1 FROM membership m JOIN "user" u ON u.id = m.user_id
    WHERE m.workspace_id = @workspace_id AND u.email = @email
);

-- name: OpenInvites :many
-- OpenInvites returns the invitations of the workspace that are open, expired
-- or not, in the order of their IDs, after the invitation after, or from the
-- first if after is null, each with when the job emailed the link that it
-- has, or null if no email holds that link.
SELECT
    id, email, role, expires_at, (expires_at <= now())::boolean AS expired,
    (CASE WHEN token_hash IS NOT NULL THEN sent_at END)::timestamptz AS sent_at, created_by, created_at
FROM invite
WHERE workspace_id = @workspace_id AND accepted_at IS NULL AND revoked_at IS NULL
    AND (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: LockInvite :one
-- LockInvite returns an invitation of the workspace, with whether it has
-- been accepted or revoked, and locks it against changes until the
-- transaction ends, so that a change of it and its acceptance run one after
-- the other.
SELECT
    id, email, role, created_by, created_at,
    (accepted_at IS NOT NULL)::boolean AS accepted,
    (revoked_at IS NOT NULL)::boolean AS revoked
FROM invite
WHERE workspace_id = @workspace_id AND id = @id
FOR UPDATE;

-- name: RenewInvite :one
-- RenewInvite gives an invitation a new secret, whose SHA-256 hash is
-- token_hash, and whose link the account link_holder is given as text, or
-- none until the job of the request email_request that emails its link adds
-- one, in place of the one that its link held, which no longer works, and
-- makes it expire lifetime from now. It keeps when the job last emailed the
-- invitation for the email to come, and forgets it for a link given as text.
-- The jobs of earlier requests to email it send nothing from then on. It
-- returns when the invitation expires.
UPDATE invite SET token_hash = sqlc.narg(token_hash)::bytea,
    sent_at = CASE WHEN sqlc.narg(token_hash)::bytea IS NULL THEN sent_at END,
    link_holder = sqlc.narg(link_holder)::uuid, email_request = sqlc.narg(email_request)::uuid,
    expires_at = now() + @lifetime::interval, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id
RETURNING expires_at;

-- name: InviteEmailDue :one
-- InviteEmailDue returns when the workspace may next email an invitation to
-- the address email: resend_interval after the job last emailed one of the
-- workspace's invitations to it, revoked and accepted ones included, and no
-- row if that time has passed, or no job has emailed one.
SELECT (max(sent_at) + @resend_interval::interval)::timestamptz AS due
FROM invite
WHERE workspace_id = @workspace_id AND email = @email
HAVING max(sent_at) + @resend_interval::interval > now();

-- name: RevokeInvite :exec
-- RevokeInvite revokes an invitation, which accepting then answers as
-- revoked (FR-TEN-02 AC1). The invitation keeps its secret's hash, so that
-- its link is still told apart from one that does not exist.
UPDATE invite SET revoked_at = now(), updated_at = now() WHERE workspace_id = @workspace_id AND id = @id;

-- name: RevokeMemberInvites :many
-- RevokeMemberInvites revokes the open invitations of the workspace, expired
-- or not, whose role is one of roles, that the user user_id made, whose link
-- the user was last given as text (link_holder), as by sending one anew, or,
-- if addressed is set, that are to the user's address, as citext compares
-- them, and returns them. The removal of the user's membership revokes them
-- all, those to the address included, so that no link that the user holds
-- lets them back in, a change of its role those of the roles that the new
-- role may not give, and the end of the user's instance admin access, in
-- every workspace, those of the roles that the user's membership may not
-- give (package workspace).
UPDATE invite SET revoked_at = now(), updated_at = now()
WHERE workspace_id = @workspace_id AND accepted_at IS NULL AND revoked_at IS NULL
    AND role = ANY(@roles::text[])
    AND (created_by = @user_id OR link_holder = @user_id
        OR (@addressed::boolean AND email = (SELECT u.email FROM "user" u WHERE u.id = @user_id)))
RETURNING id, email, role;

-- name: LockInviteByHash :one
-- LockInviteByHash returns the invitation of the workspace whose secret has
-- the SHA-256 hash token_hash, if its workspace is active, with the
-- workspace's name, whether the invitation has been accepted, by whom,
-- revoked, or has expired, and whether it may be accepted by the account
-- with the address email: whether it is a link invitation, or is to that
-- address, as citext compares them. It locks the invitation, so that of two
-- acceptances at once, the second waits for the first, and then finds it
-- accepted.
SELECT
    i.id, i.email, i.role, i.expires_at, i.created_by, i.accepted_by, w.name AS workspace_name,
    (i.accepted_at IS NOT NULL)::boolean AS accepted,
    (i.revoked_at IS NOT NULL)::boolean AS revoked,
    (i.expires_at <= now())::boolean AS expired,
    COALESCE(i.email IS NULL OR i.email = sqlc.narg(email)::citext, false)::boolean AS email_matches
FROM invite i
JOIN workspace w ON w.id = i.workspace_id AND w.status = 'active'
WHERE i.workspace_id = @workspace_id AND i.token_hash = @token_hash
FOR UPDATE OF i;

-- name: AcceptInvite :exec
-- AcceptInvite records that the account accepted_by accepted an invitation.
UPDATE invite SET accepted_at = now(), accepted_by = @accepted_by, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id;

-- name: LockInviteToSend :one
-- LockInviteToSend returns what the email of an invitation's link needs, if
-- the invitation is to an address, is open, its workspace is active, it has
-- not expired, which an invitation that was asked to be emailed longer ago
-- than its lifetime has, and the request email_request is the one that last
-- asked for it to be emailed, so that the job of a request that a later one
-- superseded sends nothing, while the job of the latest request is due once
-- the workspace may email the address again (InviteEmailDue): the address,
-- the role, the locale that the email is written in, the preferred locale
-- of the account that the address belongs to, if it has one, and else the
-- workspace's default locale, as a BCP 47 tag, or null if neither has one,
-- and whether the invitation has a link: one that an earlier attempt of the
-- job of email_request gave it (SetInviteHash), since a request to email an
-- invitation leaves it without one. It locks the invitation, so that the job
-- that sends it and a use of it, or its being sent anew, run one after the
-- other.
SELECT
    i.email::text AS email, i.role,
    COALESCE(u.locale, w.settings->>'default_locale') AS locale,
    (i.token_hash IS NOT NULL)::boolean AS linked
FROM invite i
JOIN workspace w ON w.id = i.workspace_id AND w.status = 'active'
LEFT JOIN "user" u ON u.email = i.email
WHERE i.workspace_id = @workspace_id AND i.id = @id AND i.email_request = @email_request
    AND i.email IS NOT NULL AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > now()
FOR UPDATE OF i;

-- name: SetInviteHash :exec
-- SetInviteHash gives an invitation the SHA-256 hash of its secret, which the
-- email that the job sends holds, in place of the hash of any secret that an
-- earlier attempt of the job sent, or that a link given as text held, records
-- that the job emails it now, and makes it expire lifetime from now, so that
-- the link lasts as long as the email says, however late the job sends it.
UPDATE invite SET token_hash = @token_hash, sent_at = now(), link_holder = NULL,
    expires_at = now() + @lifetime::interval, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id;

-- name: UnmarkInviteSent :exec
-- UnmarkInviteSent records that the email of the invitation's link whose
-- secret has the SHA-256 hash token_hash was not sent after all, unless the
-- invitation has another link by now.
UPDATE invite SET sent_at = NULL, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND token_hash = @token_hash;

-- name: ResolveInvite :one
-- ResolveInvite returns the workspace of the invitation whose secret has the
-- SHA-256 hash token_hash, whatever its state, through auth_resolve_invite,
-- which a transaction calls before it knows the workspace (§5.4), or no row
-- if there is none.
SELECT r.workspace_id::uuid AS workspace_id FROM auth_resolve_invite(@token_hash::bytea) r;
