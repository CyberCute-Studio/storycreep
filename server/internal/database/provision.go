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
// It also makes the login the only role, besides the superusers, the
// database's owner, the roles the operator granted CONNECT, and the roles that
// inherit the privileges of one of them or of the login, that can connect to
// the database (restrictConnect).
//
// It refuses, and changes nothing, if the role exists and row-level security
// would not hold it (checkLogin), or if the server would log in with a
// password and the role's VALID UNTIL has passed. It never makes an existing
// role less privileged: an operator who pointed STORYCREEP_DATABASE_URL at a
// superuser should find out rather than lose the superuser. The login's name
// has passed checkLoginName.
func provision(ctx context.Context, conn *pgx.Conn, login string, password secret.Value) error {
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		ident := pgx.Identifier{login}.Sanitize()
		var canLogin, inherit, expired bool
		var stored *string
		err := tx.QueryRow(ctx, "SELECT rolcanlogin, rolinherit, rolpassword, coalesce(rolvaliduntil <= now(), false) "+
			"FROM pg_authid WHERE rolname = $1", login).Scan(&canLogin, &inherit, &stored, &expired)
		exists := !errors.Is(err, pgx.ErrNoRows)
		if err != nil && exists {
			return fmt.Errorf("provision the login %q: %w", login, err)
		}
		if exists {
			// Before any change, so that a superuser is left as it is.
			if err := checkLogin(ctx, tx, login); err != nil {
				return err
			}
			// PostgreSQL refuses an expired password as a wrong one, which
			// would leave the operator looking for a typo. The expiry is the
			// operator's policy, so it is reported rather than lifted. It
			// does not matter to a login that authenticates without a
			// password.
			if expired && password.IsSet() {
				return fmt.Errorf("the password of the login %q in STORYCREEP_DATABASE_URL has expired, because the "+
					"role's VALID UNTIL has passed, so the server could not log in with it; set a later time with "+
					"ALTER ROLE %s VALID UNTIL, or 'infinity' for none", login, ident)
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
		return restrictConnect(ctx, tx, login)
	})
}

// restrictConnect makes the login the only role, besides the superusers, the
// database's owner, which keeps the CONNECT that owners have by default, any
// role the operator granted CONNECT, and the roles that inherit the privileges
// of one of them or of the login, that can connect to the database: it
// revokes CONNECT from PUBLIC, which PostgreSQL grants it to by default, and
// grants it to the login. Roles belong to the cluster, so the application
// login of every other Storycreep database on the server is a member of
// storycreep_app too, and could otherwise connect to this database with the
// privileges that its migrations grant storycreep_app, and read and change
// its data. A migration could not grant the login CONNECT, because it does
// not know the login, and this runs every time Migrate does rather than once,
// because a database that a dump was restored into, or that CREATE DATABASE
// copied, has PostgreSQL's default privileges again.
func restrictConnect(ctx context.Context, tx pgx.Tx, login string) error {
	var database string
	var publicConnects, loginConnects bool
	if err := tx.QueryRow(ctx, "SELECT current_database(), has_database_privilege('public', current_database(), 'CONNECT')").
		Scan(&database, &publicConnects); err != nil {
		return fmt.Errorf("restrict who connects to the database: %w", err)
	}
	ident := pgx.Identifier{database}.Sanitize()
	if publicConnects {
		if _, err := tx.Exec(ctx, "REVOKE CONNECT ON DATABASE "+ident+" FROM PUBLIC"); err != nil {
			return fmt.Errorf("restrict who connects to the database: %w", err)
		}
	}
	// Read after the revocation, which may have taken the login's CONNECT
	// with it.
	if err := tx.QueryRow(ctx, "SELECT has_database_privilege($1::name, current_database(), 'CONNECT')", login).
		Scan(&loginConnects); err != nil {
		return fmt.Errorf("restrict who connects to the database: %w", err)
	}
	if !loginConnects {
		if _, err := tx.Exec(ctx, "GRANT CONNECT ON DATABASE "+ident+" TO "+pgx.Identifier{login}.Sanitize()); err != nil {
			return fmt.Errorf("grant CONNECT to the login %q: %w", login, err)
		}
	}
	return nil
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
