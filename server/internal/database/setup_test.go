package database_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

// setupMigration is the migration that adds the instance's row.
const setupMigration = "00009_setup.sql"

// migrationsBefore returns the embedded migrations that come before name.
func migrationsBefore(t *testing.T, name string) fs.FS {
	t.Helper()
	m := fstest.MapFS{}
	err := fs.WalkDir(database.Migrations(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p >= name {
			return err
		}
		data, err := fs.ReadFile(database.Migrations(), p)
		m[p] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// setupCompleted reports whether the first-run setup of the database that
// conn is connected to has completed.
func setupCompleted(t *testing.T, conn *pgx.Conn) bool {
	t.Helper()
	var completed bool
	if err := conn.QueryRow(context.Background(),
		"SELECT setup_completed_at IS NOT NULL FROM instance").Scan(&completed); err != nil {
		t.Fatal(err)
	}
	return completed
}

// TestSetupMigration checks the instance's row that the setup migration
// adds: on a database without accounts, first-run setup is pending, and a
// database that had accounts before the migration counts as set up, so that
// whoever reaches it first cannot complete its setup and make themselves its
// instance admin.
func TestSetupMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if setupCompleted(t, dbtest.New(t).Admin(t)) {
		t.Error("a new database's setup has completed")
	}

	d := dbtest.Empty(t)
	before := migrationsBefore(t, setupMigration)
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{Migrations: before}); err != nil {
		t.Fatal(err)
	}
	admin := d.Admin(t)
	if _, err := admin.Exec(ctx, `INSERT INTO "user" (id, email, display_name)
		VALUES ('0192f5c4-0000-7000-8000-000000000001', 'jo@example.com', 'Jo')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{}); err != nil {
		t.Fatal(err)
	}
	if !setupCompleted(t, admin) {
		t.Error("the setup of a database that had accounts before the migration is pending")
	}
	var rows int
	var policy *string
	if err := admin.QueryRow(ctx, "SELECT count(*) OVER (), signup_policy FROM instance").Scan(&rows,
		&policy); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || policy != nil {
		t.Errorf("%d rows, and the signup policy %v; want one row, and no policy", rows, policy)
	}
}

// TestInstancePrivileges checks that the application may read the instance's
// row and complete setup in it, but may neither add a row nor delete the
// one, so that setup never looks pending again once it has completed.
func TestInstancePrivileges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	db := d.Open(t)
	for _, stmt := range []string{
		"INSERT INTO instance (singleton) VALUES (true)",
		"DELETE FROM instance",
		"UPDATE instance SET singleton = true",
		"TRUNCATE instance",
		"UPDATE instance_key SET key = sha256(key) WHERE name = 'setup_code'",
	} {
		err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, stmt)
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: %v, want permission denied", stmt, err)
		}
	}
	err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "SELECT * FROM instance FOR UPDATE")
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE instance SET setup_completed_at = now(), signup_policy = 'open'")
		}
		return err
	})
	if err != nil {
		t.Errorf("completing setup: %v", err)
	}
	if !setupCompleted(t, d.Admin(t)) {
		t.Error("setup is pending after the application completed it")
	}
	var key int
	if err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT octet_length(key) FROM instance_key WHERE name = 'setup_code'").Scan(&key)
	}); err != nil || key != 32 {
		t.Errorf("the setup code's key: %d bytes, %v; want 32", key, err)
	}
}
