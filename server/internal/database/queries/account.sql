-- name: InsertUser :execrows
-- InsertUser adds an account (package account), unless the address has one
-- already, as citext compares addresses: then it adds nothing and changes
-- nothing, and reports no row, so that registering an address that has an
-- account leaves the account as it was (PRD FR-AUTH-01 AC1).
INSERT INTO "user" (id, email, password_hash, display_name)
VALUES (@id, @email, @password_hash, @display_name)
ON CONFLICT (email) DO NOTHING;

-- name: UserCredentials :one
-- UserCredentials returns what signing in checks of the account that the
-- address belongs to (package account).
SELECT id, password_hash, status FROM "user" WHERE email = @email;

-- name: UpdatePasswordHash :execrows
-- UpdatePasswordHash replaces the hash of an account's password with
-- new_hash, if the hash is still old_hash, which the caller checked a
-- password against, so that a password that changed in the meantime is not
-- changed back. Signing in uses it to hash the same password again with the
-- current parameters (PRD NFR-SEC-03), and changing the password to store
-- the new one.
UPDATE "user" SET password_hash = @new_hash, updated_at = now()
WHERE id = @id AND password_hash = @old_hash;

-- name: UserPasswordHash :one
-- UserPasswordHash returns the hash of an active account's password, which
-- changing the password checks the current password against.
SELECT password_hash FROM "user" WHERE id = @id AND status = 'active';

-- name: LockPasswordHash :one
-- LockPasswordHash returns the hash of an active account's password, and
-- locks the account's row against changes until the transaction ends. A
-- change of the password, which updates the row, then waits for the
-- transaction, or the transaction for the change, and sees what the change
-- did. Signing in starts a session in such a transaction, and only while the
-- hash is still the one that it checked the password against, so that the
-- revocation of the other sessions that changing the password does sees
-- every session that the old password started (PRD FR-AUTH-02 AC4).
SELECT password_hash FROM "user" WHERE id = @id AND status = 'active' FOR SHARE;
