-- name: InsertUser :execrows
-- InsertUser adds an account (package account), unless the address has one
-- already, as citext compares addresses: then it adds nothing and changes
-- nothing, and reports no row, so that registering an address that has an
-- account leaves the account as it was (PRD FR-AUTH-01 AC1). An account that
-- first-run setup adds is an instance admin, and its personal workspace is
-- the one that setup creates, which personal_workspace marks as given
-- (FR-ADMIN-02).
INSERT INTO "user" (id, email, password_hash, display_name, is_instance_admin, personal_workspace_created_at)
VALUES (@id, @email, @password_hash, @display_name, @is_instance_admin,
    CASE WHEN @personal_workspace::boolean THEN now() END)
ON CONFLICT (email) DO NOTHING;

-- name: UserByEmail :one
-- UserByEmail returns the account that an address belongs to, whatever its
-- status, for the commands of the operator (package cli).
SELECT id, status FROM "user" WHERE email = @email;

-- name: SetInstanceAdmin :execrows
-- SetInstanceAdmin makes an active account an instance admin, or an account
-- of any status no longer one, and reports no row if it was so already, or
-- is not active and is to become one.
UPDATE "user" SET is_instance_admin = @is_instance_admin, updated_at = now()
WHERE id = @id AND (status = 'active' OR NOT @is_instance_admin::boolean)
    AND is_instance_admin <> @is_instance_admin;

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

-- name: LockUser :exec
-- LockUser locks an account's row against changes until the transaction
-- ends, as LockPasswordHash does, whatever the account's status. Revoking
-- the account's other sessions takes this lock first, so that it and a
-- change of the password, which revokes them too, run one after the other,
-- and cannot deadlock over the sessions' rows (FR-AUTH-02).
SELECT id FROM "user" WHERE id = @id FOR SHARE;
