package database_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// TestMain runs the tests with dbtest.Run, which keeps the libpq environment
// variables and the user's password file out of them. pgx fills in from them
// what a URL leaves out, so a developer's PGPASSWORD would take the place of
// the password file that TestProvisionPasswordFromFile names, and PGAPPNAME
// the name that TestApplicationName expects.
func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// TestRolesAfterMigrating checks the roles and privileges the migrations
// leave: the server's roles have only the attributes they need, none of them
// or the application login owns anything, and the application login has only
// storycreep_app among them.
func TestRolesAfterMigrating(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	checkRoles(t, admin)

	var owners []string
	rows, err := admin.Query(ctx, `
		SELECT DISTINCT r.rolname FROM pg_roles r
		WHERE (r.rolname LIKE 'storycreep\_%' OR r.rolname = $1)
			AND (EXISTS (SELECT FROM pg_class WHERE relowner = r.oid)
				OR EXISTS (SELECT FROM pg_proc WHERE proowner = r.oid)
				OR EXISTS (SELECT FROM pg_namespace WHERE nspowner = r.oid))`, dbtest.AppLogin)
	if err == nil {
		owners, err = pgx.CollectRows(rows, pgx.RowTo[string])
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) > 0 {
		t.Errorf("these roles own objects: %v", owners)
	}
	for role, want := range map[string]bool{
		"storycreep_app": true, "storycreep_definer": false, "storycreep_maintenance": false,
	} {
		if got := isMember(t, admin, dbtest.AppLogin, role); got != want {
			t.Errorf("the application login is a member of %s: %v, want %v", role, got, want)
		}
	}

	// The application login reads the applied migrations, and nothing else
	// of the version table.
	var read, write bool
	if err := admin.QueryRow(ctx, `SELECT has_table_privilege($1, 'goose_db_version', 'SELECT'),
		has_table_privilege($1, 'goose_db_version', 'INSERT, UPDATE, DELETE, TRUNCATE')`, dbtest.AppLogin).
		Scan(&read, &write); err != nil {
		t.Fatal(err)
	}
	if !read || write {
		t.Errorf("on goose_db_version the application login can read: %v, write: %v; want only read", read, write)
	}
	var create bool
	if err := admin.QueryRow(ctx, "SELECT has_schema_privilege($1, 'public', 'CREATE')", dbtest.AppLogin).
		Scan(&create); err != nil {
		t.Fatal(err)
	}
	if create {
		t.Errorf("the application login can create objects in the public schema")
	}
}

// ownerRightsViews lists the views and materialized views of the database's
// own schemas that read their tables with their owner's privileges: a view
// does unless it was created with security_invoker, and a materialized view
// always does.
func ownerRightsViews(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	rows, err := conn.Query(context.Background(), `
		SELECT c.oid::regclass::text FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname <> 'information_schema' AND n.nspname NOT LIKE 'pg\_%'
			AND (c.relkind = 'm' OR c.relkind = 'v' AND NOT EXISTS (
				SELECT FROM pg_options_to_table(c.reloptions)
				WHERE option_name = 'security_invoker' AND option_value::bool))
		ORDER BY 1`)
	var views []string
	if err == nil {
		views, err = pgx.CollectRows(rows, pgx.RowTo[string])
	}
	if err != nil {
		t.Fatal(err)
	}
	return views
}

// TestViewsKeepRowLevelSecurity checks that the migrations create no view
// that row-level security would not hold the application login to. The
// superuser that runs the migrations owns what they create, and row-level
// security never holds a superuser, so a view that reads its tables with its
// owner's privileges would show the server every workspace's rows. It checks
// that such views are found, too.
func TestViewsKeepRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	if views := ownerRightsViews(t, admin); len(views) > 0 {
		t.Errorf("the migrations create views that read their tables as the superuser: %v; create each view "+
			"WITH (security_invoker = true), and no materialized view", views)
	}

	if _, err := admin.Exec(ctx, `CREATE VIEW owner_rights AS SELECT 1 AS x;
		CREATE VIEW invoker_rights WITH (security_invoker = true) AS SELECT 1 AS x;
		CREATE MATERIALIZED VIEW materialized AS SELECT 1 AS x`); err != nil {
		t.Fatal(err)
	}
	if got, want := ownerRightsViews(t, admin), []string{"materialized", "owner_rights"}; !slices.Equal(got, want) {
		t.Errorf("the check finds %v, want %v", got, want)
	}
}

// TestOpenRefusesTheAdmin checks that Open refuses a superuser, the likeliest
// mistake: the admin URL given as the application's.
func TestOpenRefusesTheAdmin(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	_, err := database.Open(context.Background(), d.AdminURL)
	var uerr *database.UnsafeLoginError
	if !errors.As(err, &uerr) || uerr.Login != "postgres" || len(uerr.Reasons) != 1 ||
		!strings.Contains(err.Error(), `the login "postgres" in STORYCREEP_DATABASE_URL`) ||
		!strings.Contains(err.Error(), "it is a superuser") {
		t.Errorf("Open returned %v, want an *UnsafeLoginError for the superuser alone", err)
	}

	// A role parameter makes the session's current user an ordinary role,
	// but the session can still SET ROLE back to the superuser.
	withRole, err := config.ParseDatabaseURL(d.AdminURL.Reveal() + "&role=" + dbtest.AppLogin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Open(context.Background(), withRole); !errors.As(err, &uerr) || uerr.Login != "postgres" {
		t.Errorf("Open with a role parameter returned %v, want an *UnsafeLoginError for the superuser", err)
	}

	if _, err := database.Open(context.Background(), config.DatabaseURL{}); err == nil {
		t.Errorf("Open without a URL succeeded")
	}
}

// TestCheckServerVersion checks that a database server older than PostgreSQL
// 15, which Storycreep does not support (PRD ADR-001), is refused with an
// error that names its version, and that newer ones are not. Open and Migrate
// check every server they connect to, which the other tests run on 15 and
// newer.
func TestCheckServerVersion(t *testing.T) {
	err := database.CheckServerVersion(140012, "14.12 (Debian 14.12-1.pgdg120+1)")
	if err == nil || !strings.Contains(err.Error(), "runs PostgreSQL 14.12 (Debian") ||
		!strings.Contains(err.Error(), "needs PostgreSQL 15 or newer") {
		t.Errorf("PostgreSQL 14: %v, want a refusal naming the version", err)
	}
	for _, num := range []int{150000, 160004, 170002, 180001} {
		if err := database.CheckServerVersion(num, "a supported version"); err != nil {
			t.Errorf("server_version_num %d: %v", num, err)
		}
	}
}

// TestApplicationName checks that the server's connections report themselves
// as storycreep, as pg_stat_activity shows them, unless the URL names them
// otherwise.
func TestApplicationName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	named, err := config.ParseDatabaseURL(d.AppURL.Reveal() + "&application_name=custom")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		url  config.DatabaseURL
		want string
	}{{d.AppURL, "storycreep"}, {named, "custom"}} {
		db, err := database.Open(ctx, tt.url)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		err = db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT current_setting('application_name')").Scan(&got)
		})
		db.Close()
		if err != nil || got != tt.want {
			t.Errorf("%s: application_name %q, %v; want %q", tt.url, got, err, tt.want)
		}
	}
}

// TestObjectsThatAreNotOwnership checks that the temporary objects a session
// of the application login holds do not make the login unsafe, so that one
// instance's session cannot keep another from starting, and that neither do
// the large objects it creates, its default privileges, or a user mapping
// for it, which give it no power over other objects.
func TestObjectsThatAreNotOwnership(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	session := dbtest.Connect(t, d.AppURL)
	if _, err := session.Exec(ctx, "CREATE TEMP TABLE scratch (x int PRIMARY KEY, y text); "+
		"CREATE TEMP VIEW scratch_view AS SELECT x FROM scratch; "+
		"CREATE FUNCTION pg_temp.scratch() RETURNS int LANGUAGE sql AS 'SELECT 1'; "+
		"CREATE TYPE pg_temp.scratch_kind AS ENUM ('a'); "+
		"SELECT lo_create(0)"); err != nil {
		t.Fatal(err)
	}
	login := pgx.Identifier{dbtest.AppLogin}.Sanitize()
	if _, err := d.Admin(t).Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+login+" GRANT SELECT ON TABLES TO storycreep_app; "+
		"CREATE FOREIGN DATA WRAPPER scratch_wrapper; CREATE SERVER scratch_server FOREIGN DATA WRAPPER scratch_wrapper; "+
		"CREATE USER MAPPING FOR "+login+" SERVER scratch_server"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{}); err != nil {
		t.Errorf("Migrate: %v", err)
	}
	d.Open(t)
}

// singleConnection returns the application URL of d with a pool of one
// connection, so that consecutive transactions share it.
func singleConnection(t *testing.T, d *dbtest.Database) config.DatabaseURL {
	t.Helper()
	u, err := config.ParseDatabaseURL(d.AppURL.Reveal() + "&pool_max_conns=1")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestInTx checks the request transaction: the scope is visible inside it as
// the settings that row-level security policies read, and gone after it, on
// the same connection.
func TestInTx(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	db, err := database.Open(ctx, singleConnection(t, d))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	scope := database.Scope{WorkspaceID: ids.New(), UserID: ids.New()}
	settings := func(tx pgx.Tx) (workspace, user string) {
		t.Helper()
		if err := tx.QueryRow(ctx, `SELECT coalesce(current_setting('app.workspace_id', true), ''),
			coalesce(current_setting('app.user_id', true), '')`).Scan(&workspace, &user); err != nil {
			t.Fatal(err)
		}
		return workspace, user
	}
	err = db.InTx(ctx, scope, func(tx pgx.Tx) error {
		if w, u := settings(tx); w != scope.WorkspaceID.String() || u != scope.UserID.String() {
			t.Errorf("inside the transaction: workspace %q, user %q; want %s and %s", w, u, scope.WorkspaceID, scope.UserID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		if w, u := settings(tx); w != "" || u != "" {
			t.Errorf("in the next transaction without a scope: workspace %q, user %q; want neither", w, u)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.InTx(ctx, database.Scope{UserID: scope.UserID}, func(tx pgx.Tx) error {
		if w, u := settings(tx); w != "" || u != scope.UserID.String() {
			t.Errorf("with only a user: workspace %q, user %q", w, u)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestInTxRollsBack checks that the request transaction rolls back when its
// function returns an error or panics, and commits otherwise.
func TestInTxRollsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	if _, err := d.Admin(t).Exec(ctx, "CREATE TABLE probe (x int); GRANT SELECT, INSERT ON probe TO storycreep_app"); err != nil {
		t.Fatal(err)
	}
	db := d.Open(t)
	insert := func(x int) func(tx pgx.Tx) error {
		return func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "INSERT INTO probe VALUES ($1)", x)
			return err
		}
	}
	failure := errors.New("failure")

	if err := db.InTx(ctx, database.Scope{}, insert(1)); err != nil {
		t.Fatal(err)
	}
	err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		if err := insert(2)(tx); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Errorf("InTx returned %v, want the function's error", err)
	}
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Errorf("recovered %v, want the function's panic", r)
			}
		}()
		_ = db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
			if err := insert(3)(tx); err != nil {
				return err
			}
			panic("boom")
		})
	}()

	var got []int
	err = db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT x FROM probe ORDER BY x")
		if err == nil {
			got, err = pgx.CollectRows(rows, pgx.RowTo[int])
		}
		return err
	})
	if err != nil || len(got) != 1 || got[0] != 1 {
		t.Errorf("the table holds %v, %v; want only the committed row 1", got, err)
	}
}

// TestInTxRowLevelSecurity checks that the request transaction's scope is what
// row-level security holds the application login to: under a policy written
// as Scope describes, a transaction sees only its workspace's rows, none
// without a workspace, and cannot add a row of another workspace.
func TestInTxRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	a, b := ids.New(), ids.New()
	// The superuser that adds the rows is not held by row-level security.
	if _, err := d.Admin(t).Exec(ctx, `CREATE TABLE items (workspace_id uuid NOT NULL, name text NOT NULL);
		ALTER TABLE items ENABLE ROW LEVEL SECURITY;
		ALTER TABLE items FORCE ROW LEVEL SECURITY;
		CREATE POLICY items_workspace ON items
			USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);
		GRANT SELECT, INSERT ON items TO storycreep_app;
		INSERT INTO items VALUES ('`+a.String()+`', 'a1'), ('`+a.String()+`', 'a2'), ('`+b.String()+`', 'b1')`); err != nil {
		t.Fatal(err)
	}
	db := d.Open(t)

	for _, tt := range []struct {
		scope database.Scope
		want  []string
	}{
		{database.Scope{WorkspaceID: a, UserID: ids.New()}, []string{"a1", "a2"}},
		{database.Scope{WorkspaceID: b}, []string{"b1"}},
		{database.Scope{UserID: ids.New()}, nil},
	} {
		var got []string
		err := db.InTx(ctx, tt.scope, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, "SELECT name FROM items ORDER BY name")
			if err == nil {
				got, err = pgx.CollectRows(rows, pgx.RowTo[string])
			}
			return err
		})
		if err != nil || !slices.Equal(got, tt.want) {
			t.Errorf("in workspace %s: rows %v, %v; want %v", tt.scope.WorkspaceID, got, err, tt.want)
		}
	}

	err := db.InTx(ctx, database.Scope{WorkspaceID: a}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO items VALUES ($1::uuid, 'b2')", b.String())
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Errorf("adding a row of another workspace returned %v, want a row-level security violation", err)
	}
}

func TestStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("auto-migrate", func(t *testing.T) {
		t.Parallel()
		d := dbtest.Empty(t)
		var logged bytes.Buffer
		db, err := database.Start(ctx, &config.Config{DatabaseURL: d.AppURL, DatabaseAdminURL: d.AdminURL},
			database.StartOptions{AutoMigrate: true, Log: slog.New(slog.NewJSONHandler(&logged, nil))})
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if p, err := db.Pending(ctx); err != nil || len(p) != 0 {
			t.Errorf("Pending() = %v, %v; want none", p, err)
		}
		if !strings.Contains(logged.String(), `"msg":"applied a database migration","version":1,"name":"00001_roles.sql"`) ||
			!strings.Contains(logged.String(), `"msg":"the database is up to date"`) {
			t.Errorf("the log does not report the migration:\n%s", logged.String())
		}
	})

	t.Run("without auto-migrate", func(t *testing.T) {
		t.Parallel()
		d := dbtest.Empty(t)
		var logged bytes.Buffer
		db, err := database.Start(ctx, &config.Config{DatabaseURL: d.AppURL, DatabaseAdminURL: d.AdminURL},
			database.StartOptions{Log: slog.New(slog.NewJSONHandler(&logged, nil))})
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		p, err := db.Pending(ctx)
		if err != nil || !slices.Equal(p, embeddedVersions(t)) {
			t.Errorf("Pending() = %v, %v; want every migration", p, err)
		}
		if !strings.Contains(logged.String(), `"level":"WARN","msg":"the database has pending migrations`) {
			t.Errorf("the log does not warn about the pending migrations:\n%s", logged.String())
		}
	})

	// Without auto-migrate, Start refuses a database that a newer version
	// of Storycreep migrated, as Migrate does, rather than serving it.
	t.Run("without auto-migrate on a newer database", func(t *testing.T) {
		t.Parallel()
		d := dbtest.Empty(t)
		newer := withMigrations(t, map[string]string{"90001_newer.sql": "CREATE TABLE newer (x int);"})
		if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{Migrations: newer}); err != nil {
			t.Fatal(err)
		}
		_, err := database.Start(ctx, &config.Config{DatabaseURL: d.AppURL}, database.StartOptions{})
		var uerr *database.UnknownMigrationsError
		if !errors.As(err, &uerr) || !slices.Equal(uerr.Versions, []int64{90001}) {
			t.Errorf("Start returned %v, want an *UnknownMigrationsError for 90001", err)
		}
	})

	// Without auto-migrate, Start refuses a migrated database that every
	// role can connect to, as after a dump is restored into a new database,
	// and a copy of the template is one, until migrating restricts it again.
	t.Run("without auto-migrate on a database that PUBLIC can connect to", func(t *testing.T) {
		t.Parallel()
		d := dbtest.New(t)
		cfg := &config.Config{DatabaseURL: d.AppURL}
		_, err := database.Start(ctx, cfg, database.StartOptions{})
		if err == nil || !strings.Contains(err.Error(), "PUBLIC can connect to the database") ||
			!strings.Contains(err.Error(), "run storycreep migrate") {
			t.Fatalf("Start returned %v, want a refusal of the database that PUBLIC can connect to", err)
		}
		if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{}); err != nil {
			t.Fatal(err)
		}
		db, err := database.Start(ctx, cfg, database.StartOptions{})
		if err != nil {
			t.Fatalf("Start after migrating: %v", err)
		}
		db.Close()

		if _, err := d.Admin(t).Exec(ctx, "GRANT CONNECT ON DATABASE "+pgx.Identifier{d.Name}.Sanitize()+
			" TO storycreep_app"); err != nil {
			t.Fatal(err)
		}
		_, err = database.Start(ctx, cfg, database.StartOptions{})
		if err == nil || !strings.Contains(err.Error(), "storycreep_app can connect to the database") {
			t.Errorf("Start returned %v, want a refusal of the database that storycreep_app can connect to", err)
		}
	})

	t.Run("auto-migrate waits for the lock", func(t *testing.T) {
		t.Parallel()
		d := dbtest.Empty(t)
		holder := d.Admin(t)
		if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", database.MigrationLock); err != nil {
			t.Fatal(err)
		}
		var logged bytes.Buffer
		done := make(chan error, 1)
		go func() {
			db, err := database.Start(ctx, &config.Config{DatabaseURL: d.AppURL, DatabaseAdminURL: d.AdminURL},
				database.StartOptions{AutoMigrate: true, Log: slog.New(slog.NewJSONHandler(&logged, nil))})
			if err == nil {
				db.Close()
			}
			done <- err
		}()
		waitForLockWaiter(t, holder)
		if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.MigrationLock); err != nil {
			t.Fatal(err)
		}
		// The log is read once Start has returned, so it has finished writing.
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(logged.String(), `"msg":"waiting for the database migration lock, which another session holds"`) {
			t.Errorf("the log does not say that Start waited for the lock:\n%s", logged.String())
		}
	})

	t.Run("auto-migrate of another database", func(t *testing.T) {
		t.Parallel()
		app, other := dbtest.Empty(t), dbtest.Empty(t)
		_, err := database.Start(ctx, &config.Config{DatabaseURL: app.AppURL, DatabaseAdminURL: other.AdminURL},
			database.StartOptions{AutoMigrate: true})
		if err == nil || !strings.Contains(err.Error(), "still has pending migrations") {
			t.Errorf("Start returned %v, want a refusal of the unmigrated database", err)
		}
	})

	t.Run("auto-migrate without the admin URL", func(t *testing.T) {
		t.Parallel()
		d := dbtest.Empty(t)
		_, err := database.Start(ctx, &config.Config{DatabaseURL: d.AppURL}, database.StartOptions{AutoMigrate: true})
		if !errors.Is(err, database.ErrNoAdminURL) {
			t.Errorf("Start returned %v, want ErrNoAdminURL", err)
		}
	})
}
