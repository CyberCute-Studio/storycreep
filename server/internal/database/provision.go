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

// MaintenanceRole is the role that bypasses row-level security for the closed
// list of maintenance jobs (PRD §5.4). The maintenance login is granted it,
// and acts as it with SET ROLE, because PostgreSQL does not pass BYPASSRLS on
// to the members of a role. The migrations grant it what the jobs may do.
const MaintenanceRole = "storycreep_maintenance"

// reservedRoles are the roles the migrations create. None of them can be the
// application login or the maintenance login, or the admin login or the role
// it acts as (migrate).
var reservedRoles = append([]string{appRole}, privilegedRoles...)

// serverLogin is a login that migrating provisions: the application login, or
// the maintenance login.
type serverLogin struct {
	// key is the configuration key of the URL that names the login, which
	// messages name.
	key  string
	name string
	// password is the password the login's connections send, or unset if
	// they send none.
	password secret.Value
	// role is the server's role that the login is granted.
	role string
	// rules are what the login must not be (checkLogin).
	rules *loginRules
}

// appLogin returns the application login that the URL key names, as login
// with password.
func appLogin(login string, password secret.Value) serverLogin {
	return serverLogin{key: appURLKey, name: login, password: password, role: appRole, rules: appLoginRules}
}

// maintenanceLogin returns the maintenance login, as login with password.
func maintenanceLogin(login string, password secret.Value) serverLogin {
	return serverLogin{key: maintenanceURLKey, name: login, password: password, role: MaintenanceRole,
		rules: maintenanceLoginRules}
}

// provision makes each of the logins a login of the server: a role that can
// log in, has its password if one is set, and is granted its role of the
// server, of whose privileges it inherits. It creates a role that does not
// exist. It changes nothing that is already so, because roles belong to the
// cluster, and an ALTER ROLE that another database runs at the same time
// would conflict.
//
// It also limits who can connect to the database to the superusers, the admin
// login, and the roles that are, or inherit the privileges of, the logins,
// the database's owner, or a role other than the server's that the operator
// granted CONNECT (restrictConnect).
//
// It refuses, and changes nothing, if a login is something its rules forbid
// once it has its role's privileges (checkLogin), or if the server would log
// in with a password and the role's VALID UNTIL has passed. The checks run at
// the end of the transaction that makes the changes, which a refusal rolls
// back, so that they see the login as the server would use it, including the
// privileges that its role was given. It never makes an existing role less
// privileged: an operator who pointed STORYCREEP_DATABASE_URL at a superuser
// should find out rather than lose the superuser. The logins' names have
// passed checkLoginName, and differ.
func provision(ctx context.Context, conn *pgx.Conn, logins ...serverLogin) error {
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		var names []string
		for _, l := range logins {
			if err := provisionLogin(ctx, tx, l); err != nil {
				return err
			}
			names = append(names, l.name)
		}
		return restrictConnect(ctx, tx, names)
	})
}

// provisionLogin is provision for one login, in its transaction, without
// restricting who connects.
func provisionLogin(ctx context.Context, tx pgx.Tx, l serverLogin) error {
	ident := pgx.Identifier{l.name}.Sanitize()
	var canLogin, inherit, expired bool
	var stored *string
	err := tx.QueryRow(ctx, "SELECT rolcanlogin, rolinherit, rolpassword, coalesce(rolvaliduntil <= now(), false) "+
		"FROM pg_authid WHERE rolname = $1", l.name).Scan(&canLogin, &inherit, &stored, &expired)
	exists := !errors.Is(err, pgx.ErrNoRows)
	if err != nil && exists {
		return fmt.Errorf("provision the login %q: %w", l.name, err)
	}

	var changes []string
	if !canLogin {
		changes = append(changes, "LOGIN")
	}
	if exists && !inherit {
		changes = append(changes, "INHERIT")
	}
	if l.password.IsSet() && (stored == nil || !scramMatches(*stored, l.password.Reveal())) {
		// The iteration count is the one PostgreSQL would use for the
		// password itself: scram_iterations on PostgreSQL 16 and later,
		// which an operator may have raised, and its default before.
		var iterations int
		if err := tx.QueryRow(ctx, "SELECT coalesce(current_setting('scram_iterations', true)::int, $1)",
			scramIterations).Scan(&iterations); err != nil {
			return fmt.Errorf("provision the login %q: %w", l.name, err)
		}
		verifier, err := scramVerifier(l.password.Reveal(), iterations)
		if err != nil {
			return fmt.Errorf("provision the login %q: %w", l.name, err)
		}
		changes = append(changes, "PASSWORD "+quoteLiteral(verifier))
	}
	switch {
	case !exists:
		if _, err := tx.Exec(ctx, "CREATE ROLE "+ident+" "+strings.Join(changes, " ")); err != nil {
			return fmt.Errorf("create the login %q: %w", l.name, err)
		}
	case len(changes) > 0:
		if _, err := tx.Exec(ctx, "ALTER ROLE "+ident+" "+strings.Join(changes, " ")); err != nil {
			return fmt.Errorf("update the login %q: %w", l.name, err)
		}
	}
	// Whether the login uses its role's privileges is read after the changes
	// above, which may have just made it inherit.
	var usable bool
	var version int
	if err := tx.QueryRow(ctx, "SELECT pg_has_role($1::name, $2::name, 'USAGE'), "+
		"current_setting('server_version_num')::int", l.name, l.role).Scan(&usable, &version); err != nil {
		return fmt.Errorf("provision the login %q: %w", l.name, err)
	}
	if !usable {
		grant := "GRANT " + pgx.Identifier{l.role}.Sanitize() + " TO " + ident
		if version >= 160000 {
			// PostgreSQL 16 and later record inheritance on each grant,
			// taken from the member's INHERIT when the grant was made, so a
			// grant to a login that was NOINHERIT then still does not
			// inherit. Granting again with the option corrects it.
			grant += " WITH INHERIT TRUE"
		}
		if _, err := tx.Exec(ctx, grant); err != nil {
			return fmt.Errorf("grant %s to the login %q: %w", l.role, l.name, err)
		}
	}
	if err := checkLogin(ctx, tx, l.rules, l.name); err != nil {
		return err
	}
	// PostgreSQL refuses an expired password as a wrong one, which would
	// leave the operator looking for a typo. The expiry is the operator's
	// policy, so it is reported rather than lifted. It does not matter to a
	// login that authenticates without a password.
	if expired && l.password.IsSet() {
		return fmt.Errorf("the password of the login %q in %s has expired, because the role's VALID UNTIL has "+
			"passed, so the server could not log in with it; set a later time with ALTER ROLE %s VALID UNTIL, or "+
			"'infinity' for none", l.name, l.key, ident)
	}
	return nil
}

// restrictConnect limits who can connect to the database to the superusers,
// the admin login, and the roles that are, or inherit the privileges of, the
// logins, the database's owner, which keeps the CONNECT that owners have by
// default, or a role other than the server's that the operator granted
// CONNECT. It revokes CONNECT from PUBLIC, which PostgreSQL grants it to by
// default, and from the server's roles, and grants it to each login. Roles
// belong to the cluster, so the application login of every other Storycreep
// database on the server is a member of storycreep_app too, and its
// maintenance login of storycreep_maintenance, and either could otherwise
// connect to this database with the privileges that its migrations grant
// that role, and read and change its data. A migration could not grant the
// logins CONNECT, because it does not know them, and this runs every time
// Migrate does rather than once, because a database that a dump was restored
// into, or that CREATE DATABASE copied, has PostgreSQL's default privileges
// again.
//
// The admin login, the session user of tx, is granted CONNECT too if the
// revocation took it. A superuser always connects, but an admin login that
// acts as a superuser through a role is not one itself, and it does not
// inherit the superuser's power to connect, so it would otherwise be unable
// to migrate again.
//
// The revocation is made as the database's owner, so it leaves a grant that
// another role made, which a role with the grant option can, and a CONNECT
// that one of the server's roles has through a role it is a member of. It
// returns an error if PUBLIC or one of the server's roles can still connect
// after it.
func restrictConnect(ctx context.Context, tx pgx.Tx, logins []string) error {
	database, who, err := openConnect(ctx, tx)
	if err != nil {
		return fmt.Errorf("restrict who connects to the database: %w", err)
	}
	ident := pgx.Identifier{database}.Sanitize()
	if who != "" {
		revoke := []string{"PUBLIC"}
		for _, r := range reservedRoles {
			revoke = append(revoke, pgx.Identifier{r}.Sanitize())
		}
		if _, err := tx.Exec(ctx, "REVOKE CONNECT ON DATABASE "+ident+" FROM "+strings.Join(revoke, ", ")); err != nil {
			return fmt.Errorf("restrict who connects to the database: %w", err)
		}
		if _, who, err = openConnect(ctx, tx); err != nil {
			return fmt.Errorf("restrict who connects to the database: %w", err)
		}
		if who != "" {
			return fmt.Errorf("%s can still connect to the database %q after migrating revoked its CONNECT, because a "+
				"role other than the database's owner granted it, or it has CONNECT through another role; remove that "+
				"grant, so that the logins of the other Storycreep databases on the server cannot connect",
				who, database)
		}
	}
	// Read after the revocation, which may have taken the CONNECT of any of
	// the logins with it.
	var admin string
	var adminConnects bool
	if err := tx.QueryRow(ctx, "SELECT session_user, has_database_privilege(session_user, current_database(), 'CONNECT')").
		Scan(&admin, &adminConnects); err != nil {
		return fmt.Errorf("restrict who connects to the database: %w", err)
	}
	for _, role := range append(slices.Clone(logins), admin) {
		connects := adminConnects
		if role != admin {
			if err := tx.QueryRow(ctx, "SELECT has_database_privilege($1::name, current_database(), 'CONNECT')", role).
				Scan(&connects); err != nil {
				return fmt.Errorf("restrict who connects to the database: %w", err)
			}
		}
		if connects {
			continue
		}
		if _, err := tx.Exec(ctx, "GRANT CONNECT ON DATABASE "+ident+" TO "+pgx.Identifier{role}.Sanitize()); err != nil {
			return fmt.Errorf("grant CONNECT to the login %q: %w", role, err)
		}
	}
	return nil
}

// openConnect returns the name of the current database, and PUBLIC or the
// first of the server's roles that can connect to it, or "" if none can.
// Every role has PUBLIC's CONNECT, so PUBLIC is named if it can. A role of
// the server that does not exist, as on a database server that a dump was
// restored into, cannot connect.
func openConnect(ctx context.Context, q querier) (database, who string, err error) {
	var public bool
	var role *string
	if err := q.QueryRow(ctx, `SELECT current_database(), has_database_privilege('public', current_database(), 'CONNECT'),
		(SELECT rolname FROM pg_roles WHERE rolname = ANY ($1::name[])
			AND has_database_privilege(oid, current_database(), 'CONNECT')
			ORDER BY array_position($1::name[], rolname) LIMIT 1)`, reservedRoles).
		Scan(&database, &public, &role); err != nil {
		return "", "", err
	}
	switch {
	case public:
		return database, "PUBLIC", nil
	case role != nil:
		return database, *role, nil
	}
	return database, "", nil
}

// checkConnectRestricted returns an error if PUBLIC or one of the server's
// roles can connect to the database, which migrating revokes
// (restrictConnect). A database that a dump was restored into, or that
// CREATE DATABASE copied, has PostgreSQL's default privileges again, which let
// every role connect, including the logins of the other Storycreep databases
// on the server, until migrating restricts them again.
func checkConnectRestricted(ctx context.Context, q querier) error {
	database, who, err := openConnect(ctx, q)
	if err != nil {
		return fmt.Errorf("check who connects to the database: %w", err)
	}
	if who != "" {
		return fmt.Errorf("%s can connect to the database %q, as after a dump is restored into a new database, so the "+
			"logins of the other Storycreep databases on the server could connect to it too; run storycreep "+
			"migrate, which revokes that CONNECT", who, database)
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

// checkLoginName returns an error if login, which the URL key names, is the
// name of one of the server's roles, or has the pg_ prefix, which PostgreSQL
// reserves for its predefined roles. None of them can be the application
// login or the maintenance login.
func checkLoginName(key, login string) error {
	if slices.Contains(reservedRoles, login) || strings.HasPrefix(login, "pg_") {
		return fmt.Errorf("%s connects as %q, a role that is reserved for the server's own use; give the server a "+
			"login of its own, such as storycreep", key, login)
	}
	return nil
}

// quoteLiteral returns s as an SQL string literal. CREATE ROLE and ALTER ROLE
// take no parameters, so the verifier is written into the statement.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
