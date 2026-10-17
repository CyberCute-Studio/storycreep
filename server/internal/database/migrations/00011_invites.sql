-- +goose Up

-- Invitations (PRD FR-TEN-02, FR-AUTH-03): what the invite table needs to
-- email an invitation's link, to tell who accepted it, and to keep an
-- address to one open invitation of a workspace. Package invite creates,
-- emails, and accepts the invitations.

-- An invitation that is emailed gets its secret from the job that sends the
-- email, as an emailed link of email_token does (00010), so that neither the
-- request nor the job's arguments, which River's tables keep, hold it: its
-- token_hash is null until then, and again from when it is sent anew until
-- that job sends it. Only an invitation to an address is emailed; a link
-- invitation, which whoever holds its link may accept, always has its secret
-- from the request that makes it, whose admin takes its link as text.
ALTER TABLE invite
	ALTER COLUMN token_hash DROP NOT NULL,
	ADD CONSTRAINT invite_link_has_secret CHECK (token_hash IS NOT NULL OR email IS NOT NULL),
	-- When the job last emailed a link of the invitation, which then lasts
	-- its 14 days from then, or null if no job has, or its email could not be
	-- sent, or the invitation's link has been taken as text since. Sending
	-- the invitation anew to be emailed keeps it, so that the next email to
	-- the address waits 10 minutes after it.
	ADD COLUMN sent_at timestamptz,
	-- The request that last asked for the invitation's link to be emailed,
	-- by making it or sending it anew, whose job alone emails it, so that
	-- the jobs of the requests before it, which may still wait or be
	-- retried, as while the SMTP server is down, send nothing; or null if
	-- the request that gave the invitation the link that it has gave that
	-- link as text. It is an identifier that the request makes and the job's
	-- arguments hold, and grants nothing.
	ADD COLUMN email_request uuid,
	-- The account that accepted the invitation, which accepting it again
	-- answers as done, or null until it is accepted, or once the account is
	-- deleted.
	ADD COLUMN accepted_by uuid REFERENCES "user" ON DELETE SET NULL,
	ADD CONSTRAINT invite_accepted_by_when_accepted CHECK (accepted_by IS NULL OR accepted_at IS NOT NULL),
	-- The account that the invitation's link was last given to as text, by
	-- the request that made the invitation or sent it anew, or null if no
	-- request gave the link that it has, as when a job emailed it, or once
	-- the account is deleted. Removing that account's membership revokes the
	-- invitation, as it does the invitations that the account made, so that
	-- no link that the account holds lets it back in.
	ADD COLUMN link_holder uuid REFERENCES "user" ON DELETE SET NULL;
CREATE INDEX invite_accepted_by ON invite (accepted_by);
CREATE INDEX invite_link_holder ON invite (link_holder);

-- An address has one open invitation of a workspace at most: one that has
-- been neither accepted nor revoked, whether or not it has expired, which
-- its admin sends anew rather than invite the address again.
CREATE UNIQUE INDEX invite_open_email ON invite (workspace_id, email)
	WHERE email IS NOT NULL AND accepted_at IS NULL AND revoked_at IS NULL;
