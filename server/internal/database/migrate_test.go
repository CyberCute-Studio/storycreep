package database_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

// withMigrations returns the embedded migrations plus extra, which maps file
// names to their SQL after the -- +goose Up line.
func withMigrations(t *testing.T, extra map[string]string) fs.FS {
	t.Helper()
	m := fstest.MapFS{}
	err := fs.WalkDir(database.Migrations(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(database.Migrations(), p)
		m[p] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, sql := range extra {
		m[name] = &fstest.MapFile{Data: []byte("-- +goose Up\n" + sql + "\n")}
	}
	return m
}

func embeddedVersions(t *testing.T) []int64 {
	t.Helper()
	names, err := fs.Glob(database.Migrations(), "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	vs := make([]int64, len(names))
	for i := range names {
		vs[i] = int64(i + 1)
	}
	return vs
}

func versionsOf(applied []database.AppliedMigration) []int64 {
	var vs []int64
	for _, m := range applied {
		vs = append(vs, m.Version)
	}
	return vs
}

func TestMigrateFreshDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	want := embeddedVersions(t)

	res, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := versionsOf(res.Applied); !slices.Equal(got, want) || res.Version != want[len(want)-1] {
		t.Errorf("applied %v up to version %d, want %v", got, res.Version, want)
	}
	if res.Applied[0].Name != "00001_roles.sql" {
		t.Errorf("the first migration is named %q", res.Applied[0].Name)
	}

	// Applying again does nothing. The admin URL may carry the pool
	// parameters that the application's URL accepts.
	admin, err := config.ParseDatabaseURL(d.AdminURL.Reveal() + "&pool_max_conns=2")
	if err != nil {
		t.Fatal(err)
	}
	res, err = database.Migrate(ctx, admin, d.AppURL, database.MigrateOptions{})
	if err != nil || len(res.Applied) != 0 || res.Version != want[len(want)-1] {
		t.Errorf("the second run applied %v up to version %d, %v; want nothing", res.Applied, res.Version, err)
	}
	if p, err := d.Open(t).Pending(ctx); err != nil || len(p) != 0 {
		t.Errorf("Pending() = %v, %v; want none", p, err)
	}
}

// TestConcurrentMigrate starts several migrations of one database at once,
// as instances starting together do, and checks that each migration ran
// exactly once (PRD FR-ADMIN-04). A slow migration keeps the first run busy
// while the others start.
func TestConcurrentMigrate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	fsys := withMigrations(t, map[string]string{
		"90001_count.sql": "CREATE TABLE migration_runs (n int);\nINSERT INTO migration_runs VALUES (1);\nSELECT pg_sleep(1);",
	})

	const instances = 4
	results := make([]database.MigrateResult, instances)
	errs := make([]error, instances)
	var wg sync.WaitGroup
	for i := range instances {
		wg.Go(func() {
			results[i], errs[i] = database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{Migrations: fsys})
		})
	}
	wg.Wait()

	ran := 0
	for i := range instances {
		if errs[i] != nil {
			t.Errorf("instance %d: %v", i, errs[i])
			continue
		}
		if len(results[i].Applied) > 0 {
			ran++
		}
		if results[i].Version != 90001 {
			t.Errorf("instance %d: version %d, want 90001", i, results[i].Version)
		}
	}
	if ran != 1 {
		t.Errorf("%d instances applied migrations, want exactly 1", ran)
	}
	admin := d.Admin(t)
	var runs, duplicates int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM migration_runs").Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM (
		SELECT version_id FROM goose_db_version GROUP BY version_id HAVING count(*) > 1) d`).Scan(&duplicates); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || duplicates != 0 {
		t.Errorf("the counting migration ran %d times, and %d versions were recorded twice; want 1 and 0", runs, duplicates)
	}
}

// waitForLockWaiter waits until a session of conn's database waits for an
// advisory lock, as Migrate does for the migration lock.
func waitForLockWaiter(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var waiting bool
		if err := conn.QueryRow(context.Background(), `SELECT EXISTS (SELECT FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted
				AND database = (SELECT oid FROM pg_database WHERE datname = current_database()))`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no session waits for the migration lock")
		}
	}
}

// TestMigrateWaitsForTheLock holds the migration lock and checks that
// Migrate waits for it, and that a cancelled wait gives up.
func TestMigrateWaitsForTheLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	holder := d.Admin(t)
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", database.MigrationLock); err != nil {
		t.Fatal(err)
	}

	// The wait is cancelled once it has started, however long connecting
	// took.
	short, cancel := context.WithCancel(ctx)
	defer cancel()
	cancelled := make(chan error, 1)
	go func() {
		_, err := database.Migrate(short, d.AdminURL, d.AppURL, database.MigrateOptions{})
		cancelled <- err
	}()
	waitForLockWaiter(t, holder)
	cancel()
	if err := <-cancelled; err == nil || !strings.Contains(err.Error(), "migration lock") {
		t.Errorf("with the lock held and the wait cancelled, Migrate returned %v; want a failure to take the lock", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("Migrate finished while another session held the lock: %v", err)
	case <-time.After(time.Second):
	}
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.MigrationLock); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Migrate failed after the lock was released: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Migrate did not finish after the lock was released")
	}
}

// TestMigrateLockIgnoresSessionTimeouts checks that timeouts set for the
// database's sessions neither end Migrate's wait for the migration lock nor
// end the idle session that holds the lock while goose migrates on
// connections of its own, which would release the lock to another instance.
// Each timeout is shorter than the wait.
func TestMigrateLockIgnoresSessionTimeouts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	ident := pgx.Identifier{d.Name}.Sanitize()
	admin := d.Admin(t)
	timeouts := map[string]string{
		"lock_timeout":         "100ms",
		"statement_timeout":    "200ms",
		"idle_session_timeout": "300ms",
		// PostgreSQL 17 and later only.
		"transaction_timeout": "400ms",
	}
	for name, value := range timeouts {
		var exists bool
		if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_settings WHERE name = $1)", name).
			Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			continue
		}
		if _, err := admin.Exec(ctx, "ALTER DATABASE "+ident+" SET "+name+" = '"+value+"'"); err != nil {
			t.Fatal(err)
		}
	}
	// noTimeouts switches the timeouts off for the session, or with local
	// for the transaction, as the holder and the slow migration need.
	noTimeouts := func(local bool) string {
		return fmt.Sprintf(`SELECT set_config(name, '0', %t) FROM pg_settings
			WHERE name IN ('lock_timeout', 'statement_timeout', 'idle_session_timeout', 'transaction_timeout');`, local)
	}
	// The holder connects after the settings, and keeps its own session.
	holder := d.Admin(t)
	if _, err := holder.Exec(ctx, noTimeouts(false)); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", database.MigrationLock); err != nil {
		t.Fatal(err)
	}
	slow := withMigrations(t, map[string]string{"90001_slow.sql": noTimeouts(true) + "\nSELECT pg_sleep(1);"})
	done := make(chan error, 1)
	go func() {
		_, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{Migrations: slow})
		done <- err
	}()
	waitForLockWaiter(t, holder)
	select {
	case err := <-done:
		t.Fatalf("Migrate stopped waiting for the lock: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.MigrationLock); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Migrate failed while it held the lock: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Migrate did not finish after the lock was released")
	}
}

// TestFailedMigration checks that a failed migration leaves the database at
// the last migration that succeeded, with no trace of the failed one, and
// that a fixed migration then applies.
func TestFailedMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	good := map[string]string{"90001_good.sql": "CREATE TABLE good (x int);"}
	bad := maps.Clone(good)
	bad["90002_bad.sql"] = "CREATE TABLE half (x int);\nSELECT 1/0;"

	res, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{Migrations: withMigrations(t, bad)})
	var merr *database.MigrationError
	if !errors.As(err, &merr) {
		t.Fatalf("Migrate returned %v, want a *MigrationError", err)
	}
	if merr.Version != 90002 || merr.Name != "90002_bad.sql" || merr.DatabaseVersion != 90001 || res.Version != 90001 ||
		!strings.Contains(err.Error(), "division by zero") || !strings.Contains(err.Error(), "stays at version 90001") {
		t.Errorf("error %v (version %d, name %q, database version %d), result version %d", err, merr.Version,
			merr.Name, merr.DatabaseVersion, res.Version)
	}
	if got := versionsOf(res.Applied); got[len(got)-1] != 90001 {
		t.Errorf("applied %v, want the migrations up to 90001", got)
	}
	admin := d.Admin(t)
	var good1, half bool
	if err := admin.QueryRow(ctx, "SELECT to_regclass('good') IS NOT NULL, to_regclass('half') IS NOT NULL").
		Scan(&good1, &half); err != nil {
		t.Fatal(err)
	}
	if !good1 || half {
		t.Errorf("table good exists: %v, table half exists: %v; want the failed migration rolled back", good1, half)
	}

	fixed := maps.Clone(good)
	fixed["90002_bad.sql"] = "CREATE TABLE half (x int);"
	res, err = database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{Migrations: withMigrations(t, fixed)})
	if err != nil || !slices.Equal(versionsOf(res.Applied), []int64{90002}) {
		t.Errorf("the fixed migration: applied %v, %v; want 90002", res.Applied, err)
	}
}

// TestFailedMigrationWithoutTheLockSession checks that a failed migration
// reports the version the database stays at also when the session that holds
// the migration lock ends with it, as when the database server restarts.
func TestFailedMigrationWithoutTheLockSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	fsys := withMigrations(t, map[string]string{
		"90001_good.sql": "CREATE TABLE good (x int);",
		// Migrate's session is the only one that holds an advisory lock in
		// the database. The call waits until the session has ended.
		"90002_bad.sql": "SELECT pg_terminate_backend(pid, 10000) FROM pg_locks\n" +
			"WHERE locktype = 'advisory' AND granted AND database = (SELECT oid FROM pg_database WHERE datname = current_database());\n" +
			"SELECT 1/0;",
	})

	res, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{Migrations: fsys})
	var merr *database.MigrationError
	if !errors.As(err, &merr) || merr.Version != 90002 || merr.DatabaseVersion != 90001 || res.Version != 90001 ||
		!strings.Contains(err.Error(), "stays at version 90001") {
		t.Errorf("Migrate returned %v, version %d; want a *MigrationError for 90002 that leaves the database at 90001",
			err, res.Version)
	}
}

// TestNewerDatabase checks that a database migrated by a newer version of
// Storycreep is refused, by Migrate and by Pending, instead of being run
// against.
func TestNewerDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	newer := withMigrations(t, map[string]string{"90001_newer.sql": "CREATE TABLE newer (x int);"})
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{Migrations: newer}); err != nil {
		t.Fatal(err)
	}

	_, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{})
	var uerr *database.UnknownMigrationsError
	if !errors.As(err, &uerr) || !slices.Equal(uerr.Versions, []int64{90001}) || !strings.Contains(err.Error(), "newer") {
		t.Errorf("Migrate returned %v, want an *UnknownMigrationsError for 90001", err)
	}
	if _, err := d.Open(t).Pending(ctx); !errors.As(err, &uerr) {
		t.Errorf("Pending returned %v, want an *UnknownMigrationsError", err)
	}
}

func TestMigrateNeedsTheAdminLogin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)

	if _, err := database.Migrate(ctx, config.DatabaseURL{}, d.AppURL, database.MigrateOptions{}); !errors.Is(err, database.ErrNoAdminURL) {
		t.Errorf("without an admin URL: %v, want ErrNoAdminURL", err)
	}

	// An ordinary login could not create the roles, and could not even
	// grant most of what the migrations grant: PostgreSQL only warns then.
	plain := d.NewLogin(t, "")
	_, err := database.Migrate(ctx, plain, d.AppURL, database.MigrateOptions{})
	if err == nil || !strings.Contains(err.Error(), "is not a superuser") || !strings.Contains(err.Error(), plain.User()) {
		t.Errorf("with an ordinary admin login: %v, want a refusal naming it", err)
	}
	var exists bool
	if err := d.Admin(t).QueryRow(ctx, "SELECT to_regclass('goose_db_version') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Errorf("the refused run created the version table")
	}
}

// schemaOf describes the public schema of the database: its columns,
// indexes, row-level security policies, and privileges.
func schemaOf(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	var s string
	err := conn.QueryRow(context.Background(), `
		SELECT string_agg(x, E'\n' ORDER BY x) FROM (
			SELECT format('column %s.%s %s %s %s', table_name, column_name, data_type, is_nullable, column_default) AS x
			FROM information_schema.columns WHERE table_schema = 'public'
			UNION ALL
			SELECT format('privilege %s on %s: %s', grantee, table_name, privilege_type)
			FROM information_schema.table_privileges WHERE table_schema = 'public'
			UNION ALL
			SELECT format('index %s', indexdef) FROM pg_indexes WHERE schemaname = 'public'
			UNION ALL
			SELECT format('policy %s on %s: %s %s %s', policyname, tablename, cmd, qual, with_check)
			FROM pg_policies WHERE schemaname = 'public'
			UNION ALL
			SELECT format('rls %s %s %s', relname, relrowsecurity, relforcerowsecurity)
			FROM pg_class WHERE relnamespace = 'public'::regnamespace AND relkind = 'r'
			UNION ALL
			SELECT format('schema %s', nspacl) FROM pg_namespace WHERE nspname = 'public'
		) s`).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestUpgradeMatchesFreshInstall upgrades a database from a seed, an earlier
// set of migrations, as CI's upgrade test does with the main branch's, and
// checks that it applies only the newer migrations and ends up with the
// schema a fresh install of the same set has.
//
// Verifies: FR-ADMIN-04/AC2
func TestUpgradeMatchesFreshInstall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	newer := withMigrations(t, map[string]string{
		"90001_items.sql": "CREATE TABLE items (workspace_id uuid NOT NULL, id uuid PRIMARY KEY);\n" +
			"ALTER TABLE items ENABLE ROW LEVEL SECURITY;\nALTER TABLE items FORCE ROW LEVEL SECURITY;\n" +
			"CREATE POLICY items_workspace ON items " +
			"USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);\n" +
			"GRANT SELECT, INSERT ON items TO storycreep_app;",
		"90002_item_names.sql": "ALTER TABLE items ADD COLUMN name text NOT NULL DEFAULT '';\n" +
			"CREATE INDEX items_by_workspace ON items (workspace_id, name);",
	})

	upgraded := dbtest.Empty(t)
	if _, err := database.Migrate(ctx, upgraded.AdminURL, upgraded.AppURL, database.MigrateOptions{}); err != nil {
		t.Fatal(err)
	}
	res, err := database.Migrate(ctx, upgraded.AdminURL, upgraded.AppURL, database.MigrateOptions{Migrations: newer})
	if err != nil {
		t.Fatal(err)
	}
	if got := versionsOf(res.Applied); !slices.Equal(got, []int64{90001, 90002}) {
		t.Errorf("the upgrade applied %v, want only the newer migrations", got)
	}

	fresh := dbtest.Empty(t)
	if _, err := database.Migrate(ctx, fresh.AdminURL, fresh.AppURL, database.MigrateOptions{Migrations: newer}); err != nil {
		t.Fatal(err)
	}
	if u, f := schemaOf(t, upgraded.Admin(t)), schemaOf(t, fresh.Admin(t)); u != f {
		t.Errorf("the upgraded schema differs from a fresh install's:\nupgraded:\n%s\nfresh:\n%s", u, f)
	} else if !strings.Contains(u, "policy items_workspace") {
		t.Errorf("the schema description misses the policy:\n%s", u)
	}
}

// TestRoleMigrationCorrectsRoles checks that the first migration corrects the
// attributes of server roles that exist already with the wrong ones, as roles
// another database in the cluster created could. It changes roles that every
// database of the test server shares, so it does not run in parallel.
func TestRoleMigrationCorrectsRoles(t *testing.T) {
	ctx := context.Background()
	d := dbtest.Empty(t)
	admin := d.Admin(t)
	restore := func() {
		for _, stmt := range []string{
			"ALTER ROLE storycreep_app NOLOGIN NOBYPASSRLS NOCREATEDB",
			"ALTER ROLE storycreep_maintenance NOLOGIN BYPASSRLS",
		} {
			if _, err := admin.Exec(ctx, stmt); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(restore)
	for _, stmt := range []string{
		"ALTER ROLE storycreep_app LOGIN BYPASSRLS CREATEDB",
		"ALTER ROLE storycreep_maintenance NOBYPASSRLS",
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{}); err != nil {
		t.Fatal(err)
	}
	checkRoles(t, admin)
}

// checkRoles checks the attributes of the server's roles.
func checkRoles(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	rows, err := conn.Query(context.Background(), `
		SELECT rolname, rolcanlogin, rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls
		FROM pg_roles WHERE rolname LIKE 'storycreep\_%' AND rolname <> $1 ORDER BY rolname`, dbtest.AppLogin)
	if err != nil {
		t.Fatal(err)
	}
	type role struct {
		name                                             string
		login, super, createDB, createRole, repl, bypass bool
	}
	roles, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (role, error) {
		var x role
		err := r.Scan(&x.name, &x.login, &x.super, &x.createDB, &x.createRole, &x.repl, &x.bypass)
		return x, err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []role{
		{name: "storycreep_app"},
		{name: "storycreep_definer"},
		{name: "storycreep_maintenance", bypass: true},
	}
	if len(roles) != len(want) {
		t.Fatalf("roles = %+v, want %+v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Errorf("role %+v, want %+v", roles[i], want[i])
		}
	}
}
