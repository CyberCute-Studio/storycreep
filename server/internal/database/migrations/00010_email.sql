-- +goose Up

-- Email (PRD FR-AUTH-03, FR-ADMIN-02): the SMTP settings that first-run
-- setup records, and the tokens of the links that verify an email address
-- and reset a password. Package email sends the email, and package account
-- issues and checks the tokens.

-- The SMTP settings that first-run setup chose (FR-ADMIN-02), which the
-- mailer reads unless STORYCREEP_SMTP_URL sets its own, or none, when the
-- instance sends no email. The password is kept as it is, because the
-- mailer must send it to the SMTP server; the application login, which may
-- read it, could send email with it anyway. Either every setting of a
-- server is set, or none: the username and the password are optional, and
-- a password needs a username.
ALTER TABLE instance
	ADD COLUMN smtp_host text,
	ADD COLUMN smtp_port integer CHECK (smtp_port BETWEEN 1 AND 65535),
	ADD COLUMN smtp_security text CHECK (smtp_security IN ('tls', 'starttls', 'none')),
	ADD COLUMN smtp_username text,
	ADD COLUMN smtp_password text,
	ADD COLUMN smtp_from text,
	ADD CONSTRAINT instance_smtp_complete CHECK (
		(smtp_host IS NULL) = (smtp_port IS NULL)
		AND (smtp_host IS NULL) = (smtp_security IS NULL)
		AND (smtp_host IS NULL) = (smtp_from IS NULL)
		AND (smtp_username IS NULL OR smtp_host IS NOT NULL)
		AND (smtp_password IS NULL OR smtp_username IS NOT NULL)
	);
GRANT UPDATE (smtp_host, smtp_port, smtp_security, smtp_username, smtp_password, smtp_from) ON instance
	TO storycreep_app;

-- The tokens of the links that verify an account's email address, for 24
-- hours, and reset its password, for an hour (FR-AUTH-03). A token is used
-- once (AC2), and the table keeps a used one until it expires, so that
-- using it again is answered with token_used. The table spans workspaces,
-- because an account does, and has no row-level security (§5.4).
--
-- A token's secret is in its link alone, and the table holds its SHA-256
-- hash, as the session table does. A link that is emailed gets its secret
-- from the job that sends the email, so that neither the request nor the
-- job's arguments, which River's tables keep, hold it: token_hash is null
-- until then. The link's lifetime starts then too, so that it lasts as long
-- as the email says, however late the job sends it, while the job sends
-- nothing for a request older than that lifetime. A link that an instance
-- admin or the operator takes as text has its secret at once.
--
-- The job emails an account one link of a kind in 10 minutes at most: it
-- sends nothing for a token of an account that was emailed another link of
-- that kind that still works in that time, as sent_at tells, so that asking
-- again and again cannot flood the address's inbox.
--
-- A request to reset the password of an address that belongs to no active
-- account adds a row too, without an account, which the email job deletes,
-- so that the request does the same work whether or not the address has an
-- account (AC3), and so does a registration of an address that has an
-- account already, which adds no account (FR-AUTH-01 AC1).
CREATE TABLE email_token (
	id uuid PRIMARY KEY,
	kind text NOT NULL CHECK (kind IN ('email_verification', 'password_reset')),
	user_id uuid REFERENCES "user" ON DELETE CASCADE,
	-- The address that a verification verifies, which must still be the
	-- account's when the link is used.
	email citext,
	-- The locale that the email is written in: the request's, as a BCP 47
	-- tag.
	locale text,
	token_hash bytea UNIQUE CHECK (octet_length(token_hash) = 32),
	-- When the job last made the link's email, and gave the token the hash
	-- of its secret, or null if it never did, as for a link taken as text.
	sent_at timestamptz,
	expires_at timestamptz NOT NULL,
	used_at timestamptz,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	CHECK (kind <> 'email_verification' OR email IS NOT NULL)
);
CREATE INDEX email_token_user_id ON email_token (user_id);
CREATE INDEX email_token_expires_at ON email_token (expires_at);

-- No maintenance job reads the tokens, so storycreep_maintenance may not.
GRANT SELECT, INSERT, UPDATE, DELETE ON email_token TO storycreep_app;
