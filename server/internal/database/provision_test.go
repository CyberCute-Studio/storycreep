package database_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// canConnect reports whether url can log in.
func canConnect(t *testing.T, url config.DatabaseURL) bool {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), url.Reveal())
	if err != nil {
		return false
	}
	conn.Close(context.Background())
	return true
}

func storedPassword(t *testing.T, admin *pgx.Conn, login string) string {
	t.Helper()
	var stored *string
	if err := admin.QueryRow(context.Background(), "SELECT rolpassword FROM pg_authid WHERE rolname = $1", login).
		Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == nil {
		return ""
	}
	return *stored
}

// isMember reports whether login is a direct member of role. pg_has_role
// would count a superuser as a member of every role.
func isMember(t *testing.T, admin *pgx.Conn, login, role string) bool {
	t.Helper()
	var member bool
	if err := admin.QueryRow(context.Background(), `SELECT EXISTS (
		SELECT FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.roleid JOIN pg_roles u ON u.oid = m.member
		WHERE u.rolname = $1 AND r.rolname = $2)`, login, role).Scan(&member); err != nil {
		t.Fatal(err)
	}
	return member
}

// TestProvisionCreatesTheLogin checks that migrating creates the application
// login from STORYCREEP_DATABASE_URL, with its password, and updates the
// password when the URL's changes, without sending the password itself.
func TestProvisionCreatesTheLogin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	login := d.FreshLogin(t)
	app := d.URL(t, login, "first-password")

	if _, err := database.Migrate(ctx, d.AdminURL, app, database.MigrateOptions{}); err != nil {
		t.Fatal(err)
	}
	if !canConnect(t, app) || !isMember(t, admin, login, "storycreep_app") {
		t.Fatalf("the login cannot connect, or is not a member of storycreep_app")
	}
	stored := storedPassword(t, admin, login)
	if !strings.HasPrefix(stored, "SCRAM-SHA-256$") {
		t.Errorf("the stored password is %q, want a SCRAM verifier", stored)
	}
	if _, err := database.Open(ctx, app); err != nil {
		t.Errorf("Open: %v", err)
	}

	// Unchanged, the login is left alone: the verifier keeps its salt.
	if _, err := database.Migrate(ctx, d.AdminURL, app, database.MigrateOptions{}); err != nil {
		t.Fatal(err)
	}
	if again := storedPassword(t, admin, login); again != stored {
		t.Errorf("an unchanged password was written again")
	}

	// A new password replaces the old one.
	rotated := d.URL(t, login, "second-password")
	if _, err := database.Migrate(ctx, d.AdminURL, rotated, database.MigrateOptions{}); err != nil {
		t.Fatal(err)
	}
	if !canConnect(t, rotated) || canConnect(t, app) {
		t.Errorf("after the rotation, the new password should work and the old one not")
	}
}

// TestProvisionPasswords checks that the verifiers the server writes match
// what PostgreSQL computes from the same passwords, and that clients log in
// with them, including passwords that SASLprep changes or rejects.
func TestProvisionPasswords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	for _, password := range []string{
		"plain",
		"päss wörd",
		// SASLprep maps a no-break space to a space, and rejects a control
		// character, which is then used as it is.
		"no break",
		"bell\x07",
		"quote ' and \\ backslash",
	} {
		login := d.FreshLogin(t)
		app := d.URL(t, login, password)
		if _, err := database.Migrate(ctx, d.AdminURL, app, database.MigrateOptions{}); err != nil {
			t.Fatalf("%q: %v", password, err)
		}
		if !canConnect(t, app) {
			t.Errorf("%q: the login cannot connect with its password", password)
		}

		// PostgreSQL's own verifier of the password matches it too.
		if _, err := admin.Exec(ctx, "ALTER ROLE "+pgx.Identifier{login}.Sanitize()+" PASSWORD "+
			database.QuoteLiteral(password)); err != nil {
			t.Fatal(err)
		}
		if stored := storedPassword(t, admin, login); !database.ScramMatches(stored, password) {
			t.Errorf("%q: PostgreSQL's verifier %q does not match the password", password, stored)
		}
	}
}

// TestProvisionPasswordFromFile checks that a login whose URL has no password
// is given the one its connections take from a password file, as they would
// from PGPASSWORD, so that the server can log in with it.
func TestProvisionPasswordFromFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	login := d.FreshLogin(t)
	passfile := filepath.Join(t.TempDir(), "pgpass")
	if err := os.WriteFile(passfile, []byte("*:*:*:"+login+":from-the-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// QueryEscape writes a space as +, which libpq reads as a +.
	escaped := strings.ReplaceAll(url.QueryEscape(passfile), "+", "%20")
	app, err := config.ParseDatabaseURL(d.URL(t, login, "").Reveal() + "&passfile=" + escaped)
	if err != nil {
		t.Fatal(err)
	}
	// The URL has no password, which would be shown redacted, so its
	// connections send the file's.
	if strings.Contains(app.String(), secret.Redacted) || app.Password().Reveal() != "from-the-file" {
		t.Fatalf("the URL %s has a password, or its connections would not send the file's", app)
	}

	if _, err := database.Migrate(ctx, d.AdminURL, app, database.MigrateOptions{}); err != nil {
		t.Fatal(err)
	}
	if !canConnect(t, d.URL(t, login, "from-the-file")) {
		t.Errorf("the login cannot connect with the password from the file")
	}
	db, err := database.Open(ctx, app)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.Close()
}

// TestProvisionFixesTheLogin checks that an existing login that cannot log in
// or does not inherit privileges is corrected, including one that was granted
// storycreep_app while it did not inherit, which on PostgreSQL 16 and later
// leaves that grant without inheritance.
func TestProvisionFixesTheLogin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	for _, granted := range []bool{false, true} {
		app := d.NewLogin(t, "")
		ident := pgx.Identifier{app.User()}.Sanitize()
		if _, err := admin.Exec(ctx, "ALTER ROLE "+ident+" NOLOGIN NOINHERIT"); err != nil {
			t.Fatal(err)
		}
		if granted {
			if _, err := admin.Exec(ctx, "GRANT storycreep_app TO "+ident); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := database.Migrate(ctx, d.AdminURL, app, database.MigrateOptions{}); err != nil {
			t.Fatal(err)
		}
		var canLogin, inherit, usable bool
		if err := admin.QueryRow(ctx, `SELECT rolcanlogin, rolinherit, pg_has_role(rolname, 'storycreep_app', 'USAGE')
			FROM pg_roles WHERE rolname = $1`, app.User()).Scan(&canLogin, &inherit, &usable); err != nil {
			t.Fatal(err)
		}
		if !canLogin || !inherit || !usable || !canConnect(t, app) {
			t.Errorf("granted before: %v; login %v, inherit %v, has the privileges of storycreep_app %v; "+
				"want all three, and a working connection", granted, canLogin, inherit, usable)
		}
	}
}

// TestProvisionRefusesUnsafeLogins checks that migrating refuses to make the
// application login of a role that row-level security would not hold, and
// leaves the role as it was.
func TestProvisionRefusesUnsafeLogins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	for _, tt := range []struct {
		name string
		// setup makes the role ident, named name, unsafe.
		setup func(ident, name string) string
		want  string
	}{
		{"superuser", func(r, _ string) string { return "ALTER ROLE " + r + " SUPERUSER" }, "it is a superuser"},
		{"BYPASSRLS", func(r, _ string) string { return "ALTER ROLE " + r + " BYPASSRLS" }, "it has BYPASSRLS"},
		// On PostgreSQL 15, CREATEROLE can grant itself storycreep_maintenance.
		{"CREATEROLE", func(r, _ string) string { return "ALTER ROLE " + r + " CREATEROLE" }, "it has CREATEROLE"},
		{"REPLICATION", func(r, _ string) string { return "ALTER ROLE " + r + " REPLICATION" }, "it has REPLICATION"},
		{"member of a superuser", func(r, _ string) string { return "GRANT postgres TO " + r },
			`it is a member of "postgres", which is a superuser`},
		{"member of maintenance", func(r, _ string) string { return "GRANT storycreep_maintenance TO " + r },
			`it is a member of "storycreep_maintenance", which has BYPASSRLS`},
		{"member of definer", func(r, _ string) string { return "GRANT storycreep_definer TO " + r },
			`it is a member of "storycreep_definer", which is reserved`},
		// Each of these reaches the superuser through the server's files or
		// programs, such as by running psql there.
		{"member of pg_execute_server_program", func(r, _ string) string { return "GRANT pg_execute_server_program TO " + r },
			`it is a member of "pg_execute_server_program", which gives access to the database server's files`},
		{"member of pg_read_server_files", func(r, _ string) string { return "GRANT pg_read_server_files TO " + r },
			`it is a member of "pg_read_server_files", which gives access`},
		{"member of pg_write_server_files", func(r, _ string) string { return "GRANT pg_write_server_files TO " + r },
			`it is a member of "pg_write_server_files", which gives access`},
		{"table owner", func(r, n string) string {
			return "CREATE TABLE owned_" + n + " (x int); ALTER TABLE owned_" + n + " OWNER TO " + r
		}, "it owns objects in the database"},
		// The owner of a type or a collation can drop, with CASCADE, the
		// columns that use it in tables that it does not own.
		{"type owner", func(r, n string) string {
			return "CREATE TYPE owned_" + n + " AS ENUM ('a'); ALTER TYPE owned_" + n + " OWNER TO " + r
		}, "it owns objects in the database"},
		{"collation owner", func(r, n string) string {
			return `CREATE COLLATION owned_` + n + ` FROM "C"; ALTER COLLATION owned_` + n + " OWNER TO " + r
		}, "it owns objects in the database"},
		// The database's owner is the likeliest owner of all, as after
		// CREATE DATABASE storycreep OWNER storycreep, so the reason says
		// that it owns the database.
		{"database owner", func(r, _ string) string { return "ALTER DATABASE " + d.Name + " OWNER TO " + r },
			"it owns the database"},
		{"member of the database owner", func(r, n string) string {
			return "CREATE ROLE owner_" + n + "; GRANT owner_" + n + " TO " + r + "; ALTER DATABASE " + d.Name +
				" OWNER TO owner_" + n
		}, `", which owns the database`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app := d.NewLogin(t, "")
			if _, err := admin.Exec(ctx, tt.setup(pgx.Identifier{app.User()}.Sanitize(), app.User())); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				// A role that owns the database cannot be dropped.
				if _, err := admin.Exec(ctx, "ALTER DATABASE "+d.Name+" OWNER TO postgres; "+
					"DROP ROLE IF EXISTS owner_"+app.User()); err != nil {
					t.Error(err)
				}
			})
			before := storedPassword(t, admin, app.User())

			_, err := database.Migrate(ctx, d.AdminURL, d.URL(t, app.User(), "another-password"), database.MigrateOptions{})
			var uerr *database.UnsafeLoginError
			if !errors.As(err, &uerr) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Migrate returned %v, want an *UnsafeLoginError saying %q", err, tt.want)
			}
			// PostgreSQL makes the database's owner a member of
			// pg_database_owner, which the reasons leave out.
			if strings.Contains(err.Error(), "pg_database_owner") {
				t.Errorf("Migrate returned %v, which names pg_database_owner", err)
			}
			if isMember(t, admin, app.User(), "storycreep_app") {
				t.Errorf("the refused login was granted storycreep_app")
			}
			if after := storedPassword(t, admin, app.User()); after != before {
				t.Errorf("the refused login's password was changed")
			}
			// Open refuses it too.
			if _, err := database.Open(ctx, app); !errors.As(err, &uerr) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Open returned %v, want an *UnsafeLoginError saying %q", err, tt.want)
			}
		})
	}
}

// TestProvisionChecksTheGrantedRole checks that migrating refuses a login that
// the privileges of storycreep_app make unsafe, and leaves no trace of it: the
// check sees the login as the server would use it, after storycreep_app is
// granted, in the transaction that a refusal rolls back. It changes
// storycreep_app, which every database of the test server shares, so it does
// not run in parallel.
func TestProvisionChecksTheGrantedRole(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "REVOKE pg_read_server_files FROM storycreep_app"); err != nil {
			t.Error(err)
		}
	})
	if _, err := admin.Exec(ctx, "GRANT pg_read_server_files TO storycreep_app"); err != nil {
		t.Fatal(err)
	}
	login := d.FreshLogin(t)

	_, err := database.Migrate(ctx, d.AdminURL, d.URL(t, login, "pw"), database.MigrateOptions{})
	var uerr *database.UnsafeLoginError
	if !errors.As(err, &uerr) || !strings.Contains(err.Error(), `it is a member of "pg_read_server_files"`) {
		t.Fatalf("Migrate returned %v, want an *UnsafeLoginError naming pg_read_server_files", err)
	}
	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = $1)", login).
		Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Errorf("the refused login was created")
	}
}

// TestProvisionRefusesAnExpiredPassword checks that migrating refuses a login
// whose VALID UNTIL has passed, saying why, and leaves it as it was, where
// PostgreSQL would refuse the server's connections as if the password were
// wrong. A VALID UNTIL that has not passed is the operator's to keep.
func TestProvisionRefusesAnExpiredPassword(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	app := d.NewLogin(t, "VALID UNTIL '2000-01-01'")
	before := storedPassword(t, admin, app.User())

	_, err := database.Migrate(ctx, d.AdminURL, d.URL(t, app.User(), "another-password"), database.MigrateOptions{})
	if err == nil || !strings.Contains(err.Error(), "VALID UNTIL has passed") || !strings.Contains(err.Error(), app.User()) {
		t.Fatalf("Migrate returned %v, want a refusal of the expired password", err)
	}
	if isMember(t, admin, app.User(), "storycreep_app") || storedPassword(t, admin, app.User()) != before {
		t.Errorf("the refused login was changed")
	}

	if _, err := admin.Exec(ctx, "ALTER ROLE "+pgx.Identifier{app.User()}.Sanitize()+" VALID UNTIL '2999-01-01'"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Migrate(ctx, d.AdminURL, app, database.MigrateOptions{}); err != nil || !canConnect(t, app) {
		t.Errorf("with a VALID UNTIL in the future: %v; want the login provisioned and connecting", err)
	}
	var validUntil string
	if err := admin.QueryRow(ctx, "SELECT rolvaliduntil::date::text FROM pg_authid WHERE rolname = $1", app.User()).
		Scan(&validUntil); err != nil {
		t.Fatal(err)
	}
	if validUntil != "2999-01-01" {
		t.Errorf("the login's VALID UNTIL is %s, want it kept", validUntil)
	}
}

// TestProvisionRestrictsConnect checks that the application login of one
// Storycreep database cannot connect to another on the same server. Both
// logins are granted storycreep_app, a role of the whole server, so the
// login of either would otherwise reach the other's data with that role's
// privileges there.
func TestProvisionRestrictsConnect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, b := dbtest.New(t), dbtest.New(t)
	loginA, loginB := a.FreshLogin(t), b.FreshLogin(t)
	appA, appB := a.URL(t, loginA, "a-password"), b.URL(t, loginB, "b-password")
	for _, tt := range []struct {
		d   *dbtest.Database
		app config.DatabaseURL
	}{{a, appA}, {b, appB}} {
		if _, err := database.Migrate(ctx, tt.d.AdminURL, tt.app, database.MigrateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	if !canConnect(t, appA) || !canConnect(t, appB) {
		t.Fatalf("a login cannot connect to its own database")
	}
	if canConnect(t, a.URL(t, loginB, "b-password")) || canConnect(t, b.URL(t, loginA, "a-password")) {
		t.Errorf("the login of one database can connect to the other")
	}
	var publicConnects bool
	if err := a.Admin(t).QueryRow(ctx, "SELECT has_database_privilege('public', current_database(), 'CONNECT')").
		Scan(&publicConnects); err != nil {
		t.Fatal(err)
	}
	if publicConnects {
		t.Errorf("PUBLIC can still connect to the database")
	}

	// Another role connects once the operator grants it CONNECT, as a
	// monitoring role may need to, and migrating again keeps both that grant
	// and the login connecting.
	monitor := a.NewLogin(t, "")
	if canConnect(t, monitor) {
		t.Errorf("a role without CONNECT can connect")
	}
	if _, err := a.Admin(t).Exec(ctx, "GRANT CONNECT ON DATABASE "+pgx.Identifier{a.Name}.Sanitize()+" TO "+
		pgx.Identifier{monitor.User()}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Migrate(ctx, a.AdminURL, appA, database.MigrateOptions{}); err != nil || !canConnect(t, appA) {
		t.Errorf("migrating again: %v; want the login still connecting", err)
	}
	if !canConnect(t, monitor) {
		t.Errorf("after migrating again, the role granted CONNECT cannot connect")
	}

	// Every Storycreep database's login is a member of storycreep_app, so a
	// CONNECT granted to it is revoked again.
	aIdent := pgx.Identifier{a.Name}.Sanitize()
	if _, err := a.Admin(t).Exec(ctx, "GRANT CONNECT ON DATABASE "+aIdent+" TO storycreep_app"); err != nil {
		t.Fatal(err)
	}
	if !canConnect(t, a.URL(t, loginB, "b-password")) {
		t.Fatalf("with CONNECT granted to storycreep_app, the login of the other database cannot connect")
	}
	if _, err := database.Migrate(ctx, a.AdminURL, appA, database.MigrateOptions{}); err != nil || !canConnect(t, appA) {
		t.Errorf("migrating with CONNECT granted to storycreep_app: %v; want the login still connecting", err)
	}
	if canConnect(t, a.URL(t, loginB, "b-password")) {
		t.Errorf("after migrating, the login of the other database can still connect through storycreep_app")
	}

	// The revocation, made as the database's owner, leaves a grant that
	// another role made, so migrating fails then.
	if _, err := a.Admin(t).Exec(ctx, "GRANT CONNECT ON DATABASE "+aIdent+" TO "+
		pgx.Identifier{monitor.User()}.Sanitize()+" WITH GRANT OPTION"); err != nil {
		t.Fatal(err)
	}
	if _, err := dbtest.Connect(t, monitor).Exec(ctx, "GRANT CONNECT ON DATABASE "+aIdent+" TO PUBLIC"); err != nil {
		t.Fatal(err)
	}
	_, err := database.Migrate(ctx, a.AdminURL, appA, database.MigrateOptions{})
	if err == nil || !strings.Contains(err.Error(), "PUBLIC can still connect to the database") {
		t.Errorf("migrating with CONNECT granted to PUBLIC by another role: %v, want a refusal", err)
	}
}

// TestProvisionRefusesReservedRoles checks that migrating refuses an
// application login named after one of the server's roles, or with
// PostgreSQL's reserved prefix, before it applies any migration: the first
// migration would make a login with the name of one of the server's roles
// unable to log in.
func TestProvisionRefusesReservedRoles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	for _, login := range []string{"storycreep_app", "storycreep_maintenance", "storycreep_definer", "pg_monitor"} {
		_, err := database.Migrate(ctx, d.AdminURL, d.URL(t, login, "pw"), database.MigrateOptions{})
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("%s: Migrate returned %v, want a refusal", login, err)
		}
	}
	var exists bool
	if err := d.Admin(t).QueryRow(ctx, "SELECT to_regclass('goose_db_version') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Errorf("a refused run applied migrations")
	}
}
