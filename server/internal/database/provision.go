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
// application login, or the admin login or the role it acts as (migrate).
var reservedRoles = append([]string{appRole}, privilegedRoles...)

// provision makes the role login, on the admin connection conn, into the
// application login: a role that can log in, has password if it is set, and
// is granted storycreep_app so that it inherits that role's privileges. It
// creates the role if it does not exist. It changes nothing that is already
// so, because roles belong to the cluster, and an ALTER ROLE that another
// database runs at the same time would conflict.
//
// It also limits who can connect to the database to the superusers, the admin
// login, and the roles that are, or inherit the privileges of, the login, the
// database's owner, or a role other than storycreep_app that the operator
// granted CONNECT (restrictConnect).
//
// It refuses, and changes nothing, if row-level security would not hold the
// login once it has storycreep_app's privileges (checkLogin), or if the server
// would log in with a password and the role's VALID UNTIL has passed. The
// checks run at the end of the transaction that makes the changes, which a
// refusal rolls back, so that they see the login as the server would use it,
// including the privileges that storycreep_app was given. It never makes an
// existing role less privileged: an operator who pointed
// STORYCREEP_DATABASE_URL at a superuser should find out rather than lose the
// superuser. The login's name has passed checkLoginName.
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

		var changes []string
		if !canLogin {
			changes = append(changes, "LOGIN")
		}
		if exists && !inherit {
			changes = append(changes, "INHERIT")
		}
		if password.IsSet() && (stored == nil || !scramMatches(*stored, password.Reveal())) {
			// The iteration count is the one PostgreSQL would use for the
			// password itself: scram_iterations on PostgreSQL 16 and later,
			// which an operator may have raised, and its default before.
			var iterations int
			if err := tx.QueryRow(ctx, "SELECT coalesce(current_setting('scram_iterations', true)::int, $1)",
				scramIterations).Scan(&iterations); err != nil {
				return fmt.Errorf("provision the login %q: %w", login, err)
			}
			verifier, err := scramVerifier(password.Reveal(), iterations)
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
		if err := checkLogin(ctx, tx, login); err != nil {
			return err
		}
		// PostgreSQL refuses an expired password as a wrong one, which would
		// leave the operator looking for a typo. The expiry is the operator's
		// policy, so it is reported rather than lifted. It does not matter to
		// a login that authenticates without a password.
		if expired && password.IsSet() {
			return fmt.Errorf("the password of the login %q in STORYCREEP_DATABASE_URL has expired, because the "+
				"role's VALID UNTIL has passed, so the server could not log in with it; set a later time with "+
				"ALTER ROLE %s VALID UNTIL, or 'infinity' for none", login, ident)
		}
		return restrictConnect(ctx, tx, login)
	})
}

// restrictConnect limits who can connect to the database to the superusers,
// the admin login, and the roles that are, or inherit the privileges of, the
// login, the database's owner, which keeps the CONNECT that owners have by
// default, or a role other than storycreep_app that the operator granted
// CONNECT. It revokes CONNECT from PUBLIC, which PostgreSQL grants it to by
// default, and from storycreep_app, and grants it to the login. Roles belong
// to the cluster, so the application login of every other Storycreep
// database on the server is a member of storycreep_app too, and could
// otherwise connect to this database with the privileges that its migrations
// grant storycreep_app, and read and change its data. A migration could not
// grant the login CONNECT, because it does not know the login, and this runs
// every time Migrate does rather than once, because a database that a dump
// was restored into, or that CREATE DATABASE copied, has PostgreSQL's default
// privileges again.
//
// The admin login, the session user of tx, is granted CONNECT too if the
// revocation took it. A superuser always connects, but an admin login that
// acts as a superuser through a role is not one itself, and it does not
// inherit the superuser's power to connect, so it would otherwise be unable
// to migrate again.
//
// The revocation is made as the database's owner, so it leaves a grant that
// another role made, which a role with the grant option can, and a CONNECT
// that storycreep_app has through a role it is a member of. It returns an
// error if PUBLIC or storycreep_app can still connect after it.
func restrictConnect(ctx context.Context, tx pgx.Tx, login string) error {
	database, who, err := openConnect(ctx, tx)
	if err != nil {
		return fmt.Errorf("restrict who connects to the database: %w", err)
	}
	ident := pgx.Identifier{database}.Sanitize()
	if who != "" {
		if _, err := tx.Exec(ctx, "REVOKE CONNECT ON DATABASE "+ident+" FROM PUBLIC, "+
			pgx.Identifier{appRole}.Sanitize()); err != nil {
			return fmt.Errorf("restrict who connects to the database: %w", err)
		}
		if _, who, err = openConnect(ctx, tx); err != nil {
			return fmt.Errorf("restrict who connects to the database: %w", err)
		}
		if who != "" {
			return fmt.Errorf("%s can still connect to the database %q after migrating revoked its CONNECT, because a "+
				"role other than the database's owner granted it, or it has CONNECT through another role; remove that "+
				"grant, so that the application logins of the other Storycreep databases on the server cannot connect",
				who, database)
		}
	}
	// Read after the revocation, which may have taken the CONNECT of either
	// login with it.
	var admin string
	var loginConnects, adminConnects bool
	if err := tx.QueryRow(ctx, "SELECT session_user, has_database_privilege($1::name, current_database(), 'CONNECT'), "+
		"has_database_privilege(session_user, current_database(), 'CONNECT')", login).
		Scan(&admin, &loginConnects, &adminConnects); err != nil {
		return fmt.Errorf("restrict who connects to the database: %w", err)
	}
	for _, r := range []struct {
		role     string
		connects bool
	}{{login, loginConnects}, {admin, adminConnects}} {
		if r.connects {
			continue
		}
		if _, err := tx.Exec(ctx, "GRANT CONNECT ON DATABASE "+ident+" TO "+pgx.Identifier{r.role}.Sanitize()); err != nil {
			return fmt.Errorf("grant CONNECT to the login %q: %w", r.role, err)
		}
	}
	return nil
}

// openConnect returns the name of the current database, and PUBLIC or
// storycreep_app if that role can connect to it, or "" if neither can. Every
// role has PUBLIC's CONNECT, so PUBLIC is named if both can.
func openConnect(ctx context.Context, q querier) (database, who string, err error) {
	var public, app bool
	if err := q.QueryRow(ctx, "SELECT current_database(), has_database_privilege('public', current_database(), 'CONNECT'), "+
		"has_database_privilege($1::name, current_database(), 'CONNECT')", appRole).Scan(&database, &public, &app); err != nil {
		return "", "", err
	}
	switch {
	case public:
		return database, "PUBLIC", nil
	case app:
		return database, appRole, nil
	}
	return database, "", nil
}

// checkConnectRestricted returns an error if PUBLIC or storycreep_app can
// connect to the database, which migrating revokes (restrictConnect). A
// database that a dump was restored into, or that CREATE DATABASE copied, has
// PostgreSQL's default privileges again, which let every role connect,
// including the application logins of the other Storycreep databases on the
// server, until migrating restricts them again.
func checkConnectRestricted(ctx context.Context, q querier) error {
	database, who, err := openConnect(ctx, q)
	if err != nil {
		return fmt.Errorf("check who connects to the database: %w", err)
	}
	if who != "" {
		return fmt.Errorf("%s can connect to the database %q, as after a dump is restored into a new database, so the "+
			"application logins of the other Storycreep databases on the server could connect to it too; run "+
			"storycreep migrate, which revokes that CONNECT", who, database)
	}
	return nil
}

// checkServerRoles returns an error unless the server's roles exist, which the
// first migration creates. Roles belong to the database server rather than to
// the database, so a dump of the database restored into another server
// arrives with its migrations applied but without the roles. The restore
// could not grant them the privileges that the migrations gave them either,
// and migrating again would not, because those migrations are applied, so the
// error says to create the roles and restore the dump again.
func checkServerRoles(ctx context.Context, q querier) error {
	rows, err := q.Query(ctx, "SELECT r FROM unnest($1::name[]) AS r "+
		"WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = r) ORDER BY r", reservedRoles)
	if err != nil {
		return fmt.Errorf("check the server's roles: %w", err)
	}
	missing, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("check the server's roles: %w", err)
	}
	if len(missing) > 0 {
		return fmt.Errorf("the database has applied migrations, but the database server lacks roles that the first "+
			"migration creates (%s), as when a dump of the database is restored into another server: roles belong "+
			"to the server, so the dump has none, and the restore could not grant them the privileges that the "+
			"migrations gave them, which migrating again does not grant; create the roles on this server, with "+
			"pg_dumpall --roles-only on the old one, or by running storycreep migrate with both database URLs naming "+
			"an empty database here, and then restore the dump again", strings.Join(missing, ", "))
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
