package database

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// appRole is the role whose privileges the application login has. The
// migrations grant it what the server may do.
const appRole = "storycreep_app"

// reservedRoles are the roles the migrations create. None of them can be the
// application login.
var reservedRoles = []string{appRole, "storycreep_definer", "storycreep_maintenance"}

// provision makes the role login, on the admin connection conn, into the
// application login: a role that can log in, inherits privileges, has
// password if it is set, and is granted storycreep_app. It creates the role
// if it does not exist. It changes nothing that is already so, because roles
// belong to the cluster, and an ALTER ROLE that another database runs at the
// same time would conflict.
//
// It refuses, and changes nothing, if the role exists and row-level security
// would not hold it (checkLogin). It never makes an existing role less
// privileged: an operator who pointed STORYCREEP_DATABASE_URL at a superuser
// should find out rather than lose the superuser.
func provision(ctx context.Context, conn *pgx.Conn, login string, password secret.Value) error {
	if isReservedRole(login) {
		return fmt.Errorf("STORYCREEP_DATABASE_URL connects as %q, a role that is reserved for the server's own "+
			"use; give the server a login of its own, such as storycreep", login)
	}
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		ident := pgx.Identifier{login}.Sanitize()
		var canLogin, inherit, member bool
		var stored *string
		err := tx.QueryRow(ctx, `
			SELECT u.rolcanlogin, u.rolinherit, u.rolpassword, EXISTS (
				SELECT FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.roleid
				WHERE m.member = u.oid AND r.rolname = $2
			)
			FROM pg_authid u
			WHERE u.rolname = $1`, login, appRole).Scan(&canLogin, &inherit, &stored, &member)
		exists := !errors.Is(err, pgx.ErrNoRows)
		if err != nil && exists {
			return fmt.Errorf("provision the login %q: %w", login, err)
		}
		if exists {
			// Before any change, so that a superuser is left as it is.
			if err := checkLogin(ctx, tx, login); err != nil {
				return err
			}
		}

		var changes []string
		if !canLogin {
			changes = append(changes, "LOGIN")
		}
		if exists && !inherit {
			changes = append(changes, "INHERIT")
		}
		if password.IsSet() && (stored == nil || !scramMatches(*stored, password.Reveal())) {
			verifier, err := scramVerifier(password.Reveal())
			if err != nil {
				return fmt.Errorf("provision the login %q: %w", login, err)
			}
			changes = append(changes, "PASSWORD "+quoteLiteral(verifier))
		}
		switch {
		case !exists:
			if _, err := tx.Exec(ctx, "CREATE ROLE "+ident+" "+strings.Join(changes, " ")); err != nil {
				return fmt.Errorf("create the login %q: %w", login, err)
			}
		case len(changes) > 0:
			if _, err := tx.Exec(ctx, "ALTER ROLE "+ident+" "+strings.Join(changes, " ")); err != nil {
				return fmt.Errorf("update the login %q: %w", login, err)
			}
		}
		if !member {
			if _, err := tx.Exec(ctx, "GRANT "+pgx.Identifier{appRole}.Sanitize()+" TO "+ident); err != nil {
				return fmt.Errorf("grant %s to the login %q: %w", appRole, login, err)
			}
		}
		return nil
	})
}

func isReservedRole(name string) bool {
	// PostgreSQL reserves the pg_ prefix for its predefined roles.
	return slices.Contains(reservedRoles, name) || strings.HasPrefix(name, "pg_")
}

// quoteLiteral returns s as an SQL string literal. CREATE ROLE and ALTER ROLE
// take no parameters, so the verifier is written into the statement.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
