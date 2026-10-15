package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
)

// embedded holds the migrations, in migrations/. They are forward-only:
// each file has an Up section and no Down section, and a migration that has
// reached the main branch is never changed or deleted (PRD FR-ADMIN-04), which
// cmd/migrationcheck enforces in CI.
//
//go:embed migrations/*.sql
var embedded embed.FS

// Migrations returns the embedded migrations, one .sql file each.
func Migrations() fs.FS {
	sub, err := fs.Sub(embedded, "migrations")
	if err != nil {
		panic(err) // the directory is embedded, so this cannot fail
	}
	return sub
}

// versionTable is the table in which goose records the applied migrations.
// goose creates it in the public schema, which is the only schema that the
// migrations' sessions search (migrate).
const versionTable = goose.DefaultTablename

// The keys of the session-level advisory locks that migrations run under, in
// the single-bigint key space of pg_advisory_lock. Every advisory lock the
// server takes has its key declared in this package, so that no two share one.
const (
	// migrationLock is held by Migrate's own session for the whole of
	// Migrate. It spells "storycrp" in ASCII.
	migrationLock int64 = 0x73746f7279637270
	// gooseLock is held by the session that goose applies the migrations
	// on, while it applies them (gooseLocker). It spells "storycrq".
	gooseLock int64 = 0x73746f7279637271
)

// timeoutsOff switches off the timeouts that the server, the database, or the
// login set and that would end a wait for an advisory lock early, or end an
// idle session and with it the locks it holds: for the session, or, if $1 is
// true, for the current transaction only. Each of them that the server has is
// switched off; PostgreSQL 17 added transaction_timeout.
const timeoutsOff = `SELECT set_config(name, '0', $1) FROM pg_settings
	WHERE name IN ('lock_timeout', 'statement_timeout', 'idle_session_timeout', 'transaction_timeout')`

// MigrateOptions configure Migrate.
type MigrateOptions struct {
	// Migrations are the migrations to apply, one .sql file each. Nil means
	// the embedded ones; tests apply other sets.
	Migrations fs.FS
	// Waiting, if set, is called when another session holds the migration
	// lock, before Migrate waits for it. The wait has no timeout, so it is
	// for saying why nothing happens meanwhile.
	Waiting func()
}

// MigrateResult says what Migrate did.
type MigrateResult struct {
	// Applied are the migrations that this call applied, in order. It is
	// empty if the database was up to date, which includes the case where
	// another instance applied them while this one waited for the lock.
	Applied []AppliedMigration
	// Version is the newest migration the database has applied afterwards.
	Version int64
}

// AppliedMigration is one migration that Migrate applied.
type AppliedMigration struct {
	Version  int64
	Name     string
	Duration time.Duration
}

// ErrNoAdminURL is returned by Migrate when STORYCREEP_DATABASE_ADMIN_URL is
// not set.
var ErrNoAdminURL = errors.New("STORYCREEP_DATABASE_ADMIN_URL is not set; applying migrations needs a superuser login")

// UnknownMigrationsError reports migrations that the database has applied but
// the server does not have, because a newer version of Storycreep migrated
// the database. The server refuses to run against it.
type UnknownMigrationsError struct {
	Versions []int64
}

func (e *UnknownMigrationsError) Error() string {
	return fmt.Sprintf("the database has applied migrations that this version of Storycreep does not have (%s); "+
		"a newer version migrated it, and migrations cannot be undone, so run that version or a newer one",
		joinVersions(e.Versions))
}

// MigrationError reports a migration that failed. Each migration runs in its
// own transaction, so the failed one left no trace, and the database stays at
// the version of the last one that succeeded.
type MigrationError struct {
	Version int64
	Name    string
	// DatabaseVersion is the version the database stays at.
	DatabaseVersion int64
	Err             error
}

func (e *MigrationError) Error() string {
	return fmt.Sprintf("migration %d (%s) failed, and the database stays at version %d: %v",
		e.Version, e.Name, e.DatabaseVersion, e.Err)
}

func (e *MigrationError) Unwrap() error { return e.Err }

// Migrate applies the pending migrations as the admin login, provisions the
// application login (provision), and then connects as the application login,
// as the server does, to check that the login works and that the database it
// reaches has the migrations (checkAppDatabase). All of it runs while Migrate
// holds the migration lock, so instances that migrate at the same time wait
// for each other, and the database ends up with each migration applied
// exactly once (PRD FR-ADMIN-04). The session that goose applies the
// migrations on holds a lock of its own while it does, so that they stay
// applied one instance at a time even if Migrate's session, and with it the
// migration lock, ends while goose is still at work. The admin login must be
// a superuser, because the migrations create roles.
//
// A failed migration is a *MigrationError: the migrations before it stay
// applied, and the application login is left as it was. A database that a
// newer version of Storycreep migrated is an *UnknownMigrationsError, and
// Migrate changes nothing then, nor when the application login has the name
// of one of the server's roles or PostgreSQL's reserved pg_ prefix
// (checkLoginName).
func Migrate(ctx context.Context, admin, app config.DatabaseURL, opts MigrateOptions) (MigrateResult, error) {
	res, db, err := migrate(ctx, admin, app, opts)
	if db != nil {
		db.Close()
	}
	return res, err
}

// migrate is Migrate, and also returns the application login's pool that
// checkAppDatabase opened, which Start keeps. The pool is nil if migrate
// returns an error.
func migrate(ctx context.Context, admin, app config.DatabaseURL, opts MigrateOptions) (MigrateResult, *DB, error) {
	if !admin.IsSet() {
		return MigrateResult{}, nil, ErrNoAdminURL
	}
	if !app.IsSet() {
		return MigrateResult{}, nil, errNoURL
	}
	// The name is checked before anything changes: the first migration makes
	// the server's roles unable to log in, so it would lock out a login that
	// had the name of one of them.
	if err := checkLoginName(app.User()); err != nil {
		return MigrateResult{}, nil, err
	}
	ms, err := loadMigrations(opts.Migrations)
	if err != nil {
		return MigrateResult{}, nil, err
	}
	// Both URLs were read as a pool's URL, as the application's is, so the
	// admin URL may carry the same pool parameters, which pgx alone would send
	// to PostgreSQL as settings.
	connConfig := admin.PoolConfig().ConnConfig
	setApplicationName(connConfig)
	// The server's objects live in the public schema, which the first
	// migration lets the server's roles use, so the migrations' sessions
	// search only it. A search_path from the URL, the login, or the database,
	// or a schema named after the admin login, which the default "$user"
	// finds first, would otherwise receive goose's version table and the
	// objects of the migrations, where the application login cannot reach
	// them. A setting sent at connection overrides those of the login and the
	// database.
	connConfig.RuntimeParams["search_path"] = "public"
	// The application login's password is the one its connections send: the
	// URL's, or else the one PGPASSWORD, a service file, or a password file
	// supplies.
	appPassword := app.Password()

	// The lock is held by this connection's session, so it is released when
	// the connection closes, however Migrate ends.
	conn, err := pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		return MigrateResult{}, nil, fmt.Errorf("connect to %s: %w", admin, err)
	}
	defer conn.Close(context.WithoutCancel(ctx))
	// The migrations run as the current user, which a role parameter in the
	// URL or a default of the login can make a role other than the login.
	var role string
	var super bool
	if err := conn.QueryRow(ctx, "SELECT current_user, rolsuper FROM pg_roles WHERE rolname = current_user").
		Scan(&role, &super); err != nil {
		return MigrateResult{}, nil, fmt.Errorf("check the admin login: %w", err)
	}
	switch {
	case !super && role != admin.User():
		return MigrateResult{}, nil, fmt.Errorf("the login %q in STORYCREEP_DATABASE_ADMIN_URL acts as the role %q, "+
			"which is not a superuser; applying migrations needs one, because they create the server's database roles",
			admin.User(), role)
	case !super:
		return MigrateResult{}, nil, fmt.Errorf("the login %q in STORYCREEP_DATABASE_ADMIN_URL is not a superuser; "+
			"applying migrations needs one, because they create the server's database roles", admin.User())
	}
	// This session waits for the lock for as long as another instance
	// migrates, and then sits idle while goose migrates on connections of its
	// own. Timeouts would end the wait early, or end the idle session and
	// with it the lock, so they are switched off for the session. The wait
	// runs in a transaction of its own, so PostgreSQL 17's
	// transaction_timeout would end it too.
	if _, err := conn.Exec(ctx, timeoutsOff, false); err != nil {
		return MigrateResult{}, nil, fmt.Errorf("prepare the session for the migration lock: %w", err)
	}
	// pg_advisory_lock waits for as long as another instance migrates. If ctx
	// ends first, pgx cancels the wait. The lock is tried first, so that the
	// caller can say that it waits.
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", migrationLock).Scan(&locked); err != nil {
		return MigrateResult{}, nil, fmt.Errorf("take the migration lock: %w", err)
	}
	if !locked {
		if opts.Waiting != nil {
			opts.Waiting()
		}
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLock); err != nil {
			return MigrateResult{}, nil, fmt.Errorf("take the migration lock: %w", err)
		}
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return MigrateResult{}, nil, err
	}
	if _, err := pending(ms, applied); err != nil {
		return MigrateResult{}, nil, err
	}

	// goose works on database/sql. Its connections are opened only while
	// this one holds the lock. goose applies the migrations on one of them,
	// which holds gooseLock meanwhile (gooseLocker), so that if this session
	// ends early, an instance that then takes the migration lock waits for
	// this goose to finish before it applies anything.
	gooseDB := stdlib.OpenDB(*connConfig)
	defer gooseDB.Close()
	var res MigrateResult
	res.Applied, err = runGoose(ctx, gooseDB, ms)
	// The version is read again on this connection, which a failed migration
	// leaves usable. If the connection failed too, as when the database
	// server restarted, the database had, when goose stopped, the migrations
	// it had before and the ones goose applied, because goose held gooseLock
	// throughout.
	after, verr := appliedVersions(context.WithoutCancel(ctx), conn)
	if verr == nil {
		applied = after
	}
	if len(applied) > 0 {
		res.Version = applied[len(applied)-1]
	}
	for _, m := range res.Applied {
		res.Version = max(res.Version, m.Version)
	}
	var merr *MigrationError
	if errors.As(err, &merr) {
		merr.DatabaseVersion = res.Version
	}
	switch {
	case err != nil:
		return res, nil, err
	case verr != nil:
		return res, nil, verr
	}
	if err := provision(ctx, conn, app.User(), appPassword); err != nil {
		return res, nil, err
	}
	db, err := checkAppDatabase(ctx, app, ms)
	if err != nil {
		return res, nil, err
	}
	return res, db, nil
}

// checkAppDatabase connects as the application login, as the server does,
// and returns the login's pool, or an error unless row-level security holds
// the login (Open) and the database it reaches has applied every migration
// in ms. The admin URL names the same database, but it can still reach
// another database of that name, such as one on another server.
func checkAppDatabase(ctx context.Context, app config.DatabaseURL, ms migrationSet) (*DB, error) {
	db, err := Open(ctx, app)
	if err != nil {
		return nil, err
	}
	p, err := db.pendingIn(ctx, ms)
	switch {
	case err != nil:
		db.Close()
		return nil, err
	case len(p) > 0:
		db.Close()
		return nil, fmt.Errorf("the database that STORYCREEP_DATABASE_URL connects to still has pending migrations "+
			"(%s) after migrating; STORYCREEP_DATABASE_ADMIN_URL must connect to the same database", joinVersions(p))
	}
	return db, nil
}

// migrationSet is a set of migrations.
type migrationSet struct {
	fsys fs.FS
	// versions are the migrations' versions, in ascending order.
	versions []int64
}

// loadMigrations lists the migrations in fsys, or the embedded ones if fsys
// is nil: the .sql files at its root, each named by its version, an
// underscore, and a description, as goose reads them.
func loadMigrations(fsys fs.FS) (migrationSet, error) {
	if fsys == nil {
		fsys = Migrations()
	}
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return migrationSet{}, fmt.Errorf("read the migrations: %w", err)
	}
	ms := migrationSet{fsys: fsys}
	for _, name := range names {
		v, err := goose.NumericComponent(name)
		if err != nil {
			return migrationSet{}, fmt.Errorf("read the migrations: %s: %w", name, err)
		}
		if slices.Contains(ms.versions, v) {
			return migrationSet{}, fmt.Errorf("read the migrations: two migrations have version %d", v)
		}
		ms.versions = append(ms.versions, v)
	}
	slices.Sort(ms.versions)
	return ms, nil
}

// runGoose applies the pending migrations of ms with goose, on db. It
// returns the migrations it applied, also when one fails.
func runGoose(ctx context.Context, db *sql.DB, ms migrationSet) ([]AppliedMigration, error) {
	p, err := goose.NewProvider(goose.DialectPostgres, db, ms.fsys,
		goose.WithDisableGlobalRegistry(true),
		goose.WithTableName(versionTable),
		goose.WithSessionLocker(gooseLocker{}),
	)
	if err != nil {
		return nil, fmt.Errorf("read the migrations: %w", err)
	}
	results, err := p.Up(ctx)
	var partial *goose.PartialError
	if errors.As(err, &partial) {
		results = partial.Applied
	}
	var applied []AppliedMigration
	for _, r := range results {
		applied = append(applied, AppliedMigration{
			Version:  r.Source.Version,
			Name:     path.Base(r.Source.Path),
			Duration: r.Duration,
		})
	}
	switch {
	case partial != nil && partial.Failed != nil:
		return applied, &MigrationError{
			Version: partial.Failed.Source.Version,
			Name:    path.Base(partial.Failed.Source.Path),
			Err:     partial.Err,
		}
	case err != nil:
		return applied, fmt.Errorf("apply the migrations: %w", err)
	}
	return applied, nil
}

// gooseLocker is goose's session locker. goose takes it on the connection
// that it then applies the migrations on, and releases it when it is done, so
// gooseLock is held by the session that migrates for as long as it migrates.
// The migration lock alone is held by another session, which can end while
// goose is still at work.
type gooseLocker struct{}

// SessionLock waits for gooseLock. Normally it is free, because Migrate holds
// the migration lock, but an instance whose session took the migration lock
// after another instance's session ended waits here until that instance's
// goose is done. The wait runs in a transaction that switches the timeouts
// off for itself alone, so the migrations keep the session's timeouts, and
// the lock, which belongs to the session, outlives the transaction.
func (gooseLocker) SessionLock(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err == nil {
		// After Commit, Rollback does nothing.
		defer func() { _ = tx.Rollback() }()
		_, err = tx.ExecContext(ctx, timeoutsOff, true)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, "SELECT pg_advisory_lock($1)", gooseLock)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return fmt.Errorf("take the lock for applying migrations: %w", err)
	}
	return nil
}

// SessionUnlock releases gooseLock.
func (gooseLocker) SessionUnlock(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", gooseLock); err != nil {
		return fmt.Errorf("release the lock for applying migrations: %w", err)
	}
	return nil
}

// appliedVersions returns the versions of the migrations the database has
// applied, in ascending order, or none if migrations never ran. It reads the
// version table in the public schema, whatever search_path the session has,
// so that the application login finds it there too.
func appliedVersions(ctx context.Context, q querier) ([]int64, error) {
	table := pgx.Identifier{"public", versionTable}.Sanitize()
	var exists bool
	if err := q.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil {
		return nil, fmt.Errorf("read the applied migrations: %w", err)
	}
	if !exists {
		return nil, nil
	}
	// goose adds a row each time it applies a migration, and its version 0
	// marks the table's creation.
	rows, err := q.Query(ctx, fmt.Sprintf(`
		SELECT version_id FROM (
			SELECT DISTINCT ON (version_id) version_id, is_applied FROM %s ORDER BY version_id, id DESC
		) latest
		WHERE is_applied AND version_id > 0
		ORDER BY version_id`, table))
	if err != nil {
		return nil, fmt.Errorf("read the applied migrations: %w", err)
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("read the applied migrations: %w", err)
	}
	return versions, nil
}

// pending returns the versions in ms that are not in applied, or an
// *UnknownMigrationsError if applied has versions that ms does not.
func pending(ms migrationSet, applied []int64) ([]int64, error) {
	var unknown []int64
	for _, v := range applied {
		if !slices.Contains(ms.versions, v) {
			unknown = append(unknown, v)
		}
	}
	if len(unknown) > 0 {
		return nil, &UnknownMigrationsError{Versions: unknown}
	}
	var p []int64
	for _, v := range ms.versions {
		if !slices.Contains(applied, v) {
			p = append(p, v)
		}
	}
	return p, nil
}

func joinVersions(vs []int64) string {
	s := make([]string, len(vs))
	for i, v := range vs {
		s[i] = fmt.Sprint(v)
	}
	return strings.Join(s, ", ")
}
