package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// The operator's commands (PRD FR-ADMIN-06): storycreep create-user and
// grant-role, which act on accounts whatever the signup policy, and for
// whoever runs them, so that nothing but those commands may call these
// functions. The audit log records what they change without an actor: the
// operator acts through the command line, with the server's database login.

var (
	// ErrNoAccount is returned for an email address that belongs to no
	// account.
	ErrNoAccount = errors.New("account: the email address belongs to no account")
	// ErrNotActive is returned for an account that is not active, as a
	// suspended one, or one whose deletion is pending (FR-GDPR-02).
	ErrNotActive = errors.New("account: the account is not active")
)

// Create adds an account with r, for storycreep create-user: whatever the
// signup policy, and whether or not first-run setup has completed, as
// Prepare checks it, and an instance admin if instanceAdmin is set, which
// the audit log records. It returns the account's ID, or what Prepare
// returns, or ErrEmailTaken if the address has an account already, and adds
// nothing then.
func Create(ctx context.Context, db DB, h *Hasher, r Registration, instanceAdmin bool) (ids.UUID, error) {
	a, err := Prepare(ctx, h, r)
	if err != nil {
		return ids.UUID{}, err
	}
	a.InstanceAdmin = instanceAdmin
	err = db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		if err := Insert(ctx, tx, a); err != nil || !instanceAdmin {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: audit.ActionInstanceAdminGrant, SubjectID: a.ID,
			Result: audit.ResultSuccess})
	})
	if err != nil {
		return ids.UUID{}, err
	}
	return a.ID, nil
}

// Find returns the ID of the active account that the email address belongs
// to, in its canonical form, for storycreep grant-role. It returns
// ErrNoAccount if the address belongs to no account, and ErrNotActive if its
// account is not active.
func Find(ctx context.Context, db DB, email string) (ids.UUID, error) {
	var id ids.UUID
	err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		row, err := find(ctx, tx, email)
		id = row.ID.Bytes
		return err
	})
	return id, err
}

// find returns the active account that the email address belongs to, as
// Find does.
func find(ctx context.Context, tx pgx.Tx, email string) (queries.UserByEmailRow, error) {
	row, err := lookup(ctx, tx, email)
	if err == nil && row.Status != "active" {
		return row, ErrNotActive
	}
	return row, err
}

// lookup returns the account that the email address belongs to, whatever
// its status, or ErrNoAccount if it belongs to none.
func lookup(ctx context.Context, tx pgx.Tx, email string) (queries.UserByEmailRow, error) {
	email = canonicalEmail(email)
	if checkEmail(email) != "" {
		// No account has an address that registration would refuse.
		return queries.UserByEmailRow{}, ErrNoAccount
	}
	row, err := queries.New(tx).UserByEmail(ctx, email)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return row, ErrNoAccount
	case err != nil:
		return row, fmt.Errorf("account: read the account: %w", err)
	}
	return row, nil
}

// SetInstanceAdmin makes the active account that the email address belongs
// to an instance admin, if admin is set, or the account no longer one,
// whatever its status, for storycreep grant-role, and records that in the
// audit log. Revoking does not need an active account, so that the operator
// can take the role from a suspended account, which would otherwise get it
// back with its status. It reports whether that changed the account, which
// it does not if the account was so already, and returns the errors of
// Find, but ErrNotActive only to make an account an instance admin.
func SetInstanceAdmin(ctx context.Context, db DB, email string, admin bool) (bool, error) {
	changed := false
	err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		look := lookup
		if admin {
			look = find
		}
		row, err := look(ctx, tx, email)
		if err != nil {
			return err
		}
		n, err := queries.New(tx).SetInstanceAdmin(ctx, queries.SetInstanceAdminParams{IsInstanceAdmin: admin,
			ID: pgtype.UUID{Bytes: row.ID.Bytes, Valid: true}})
		if err != nil {
			return fmt.Errorf("account: change the account: %w", err)
		}
		if changed = n == 1; !changed {
			return nil
		}
		action := audit.ActionInstanceAdminGrant
		if !admin {
			action = audit.ActionInstanceAdminRevoke
		}
		return audit.Record(ctx, tx, audit.Entry{Action: action, SubjectID: row.ID.Bytes,
			Result: audit.ResultSuccess})
	})
	return changed, err
}
