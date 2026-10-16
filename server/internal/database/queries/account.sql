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
-- UpdatePasswordHash replaces the hash of an account's password with one of
-- the same password, made with the current parameters (PRD NFR-SEC-03), if
-- the hash is still old_hash, so that a password that changed in the
-- meantime is not changed back.
UPDATE "user" SET password_hash = @new_hash, updated_at = now()
WHERE id = @id AND password_hash = @old_hash;

-- name: UserPasswordHash :one
-- UserPasswordHash returns the hash of an active account's password, which
-- changing the password checks the current password against.
SELECT password_hash FROM "user" WHERE id = @id AND status = 'active';
