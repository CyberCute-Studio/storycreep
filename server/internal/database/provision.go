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
var reservedRoles = append([]string{appRole}, privilegedRoles...)

// provision makes the role login, on the admin connection conn, into the
// application login: a role that can log in, has password if it is set, and
// is granted storycreep_app so that it inherits that role's privileges. It
// creates the role if it does not exist. It changes nothing that is already
// so, because roles belong to the cluster, and an ALTER ROLE that another
// database runs at the same time would conflict.
//
// It refuses, and changes nothing, if the role exists and row-level security
// would not hold it (checkLogin). It never makes an existing role less
// privileged: an operator who pointed STORYCREEP_DATABASE_URL at a superuser
// should find out rather than lose the superuser. The login's name has passed
// checkLoginName.
func provision(ctx context.Context, conn *pgx.Conn, login string, password secret.Value) error {
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		ident := pgx.Identifier{login}.Sanitize()
		var canLogin, inherit bool
		var stored *string
		err := tx.QueryRow(ctx, "SELECT rolcanlogin, rolinherit, rolpassword FROM pg_authid WHERE rolname = $1", login).
			Scan(&canLogin, &inherit, &stored)
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
		// Whether the login uses storycreep_app's privileges is read after the
		// changes above, which may have just made it inherit.
		var usable bool
		var version int
		if err := tx.QueryRow(ctx, "SELECT pg_has_role($1::name, $2::name, 'USAGE'), "+
			"current_setting('server_version_num')::int", login, appRole).Scan(&usable, &version); err != nil {
			return fmt.Errorf("provision the login %q: %w", login, err)
		}
		if !usable {
			grant := "GRANT " + pgx.Identifier{appRole}.Sanitize() + " TO " + ident
			if version >= 160000 {
				// PostgreSQL 16 and later record inheritance on each grant,
				// taken from the member's INHERIT when the grant was made, so
				// a grant to a login that was NOINHERIT then still does not
				// inherit. Granting again with the option corrects it.
				grant += " WITH INHERIT TRUE"
			}
			if _, err := tx.Exec(ctx, grant); err != nil {
				return fmt.Errorf("grant %s to the login %q: %w", appRole, login, err)
			}
		}
		return nil
	})
}

// checkLoginName returns an error if login is the name of one of the server's
// roles, or has the pg_ prefix, which PostgreSQL reserves for its predefined
// roles. None of them can be the application login.
func checkLoginName(login string) error {
	if slices.Contains(reservedRoles, login) || strings.HasPrefix(login, "pg_") {
		return fmt.Errorf("STORYCREEP_DATABASE_URL connects as %q, a role that is reserved for the server's own "+
			"use; give the server a login of its own, such as storycreep", login)
	}
	return nil
}

// quoteLiteral returns s as an SQL string literal. CREATE ROLE and ALTER ROLE
// take no parameters, so the verifier is written into the statement.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
