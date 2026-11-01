// Package database is the server's access to PostgreSQL (PRD ADR-001, ADR-002):
// the embedded migrations and the advisory locks they run under, the database
// roles and the application's login, the application's connection pool, and
// the request transaction that sets the row-level security context.
//
// Three logins are involved. The admin login, a superuser from
// STORYCREEP_DATABASE_ADMIN_URL, applies the migrations and owns every table.
// The application login, from STORYCREEP_DATABASE_URL, does everything else
// but the maintenance jobs. It is an ordinary role, granted storycreep_app,
// that row-level security applies to: Migrate provisions it, and Open refuses
// a login that row-level security would not hold. The maintenance login, from
// STORYCREEP_DATABASE_MAINTENANCE_URL, is the maintenance jobs' (package
// maintenance): an ordinary role, granted storycreep_maintenance, which it
// acts as to bypass row-level security. Migrate provisions it too when the
// URL is set.
package database

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// applicationName is what the server's connections report in
// pg_stat_activity, unless the URL sets application_name, or PGAPPNAME or a
// service file does.
const applicationName = "storycreep"

// MaintenanceApplicationName is what the maintenance login's connections
// report in the same way: those of the maintenance path's pool (package
// maintenance), and the one that migrating checks the login on.
const MaintenanceApplicationName = "storycreep-maintenance"

// errNoURL is returned by Open and Migrate when STORYCREEP_DATABASE_URL is not
// set.
var errNoURL = errors.New("STORYCREEP_DATABASE_URL is not set")

// DB is the application login's connection pool.
type DB struct {
	pool *pgxpool.Pool
	// migrations are the migrations that Pending compares the database with,
	// or nil for the embedded ones.
	migrations fs.FS
}

// minServerVersion is the oldest PostgreSQL that Storycreep supports (PRD
// ADR-001), as server_version_num gives it.
const minServerVersion = 150000

// serverVersionColumns read the database server's version, as
// server_version_num and as server_version, for checkServerVersion.
const serverVersionColumns = "current_setting('server_version_num')::int, current_setting('server_version')"

// checkServerVersion returns an error if the database server, whose
// server_version_num is num and whose server_version is version, runs a
// PostgreSQL older than Storycreep supports. The migrations may use what
// PostgreSQL 15 added, such as views WITH (security_invoker), and an older
// server would fail on the first of them with an error that does not say
// why.
func checkServerVersion(num int, version string) error {
	if num < minServerVersion {
		return fmt.Errorf("the database server runs PostgreSQL %s, but Storycreep needs PostgreSQL 15 or newer", version)
	}
	return nil
}

// Open connects to the database as the application login. It returns an
// error unless the database server runs PostgreSQL 15 or newer
// (checkServerVersion), and checks that row-level security holds the login:
// the login must not be a superuser, have BYPASSRLS, CREATEROLE, or
// REPLICATION, own the database or objects in it, or be a member of a role
// that does, of one of the server's privileged roles, of one of PostgreSQL's
// roles that give access to the database server's files and programs, or of
// pg_write_all_data, which may change the rows of every table past the
// migrations' grants, directly or through other roles. If it is, the error is
// an *UnsafeLoginError.
func Open(ctx context.Context, app config.DatabaseURL) (*DB, error) {
	return OpenWith(ctx, app, nil)
}

// OpenWith is Open, with the pool's configuration, which the URL's pool
// parameters set, changed by configure first, if it is not nil. The job
// queue's workers open a pool of their own with it, sized for them (package
// jobs).
func OpenWith(ctx context.Context, app config.DatabaseURL, configure func(*pgxpool.Config)) (*DB, error) {
	if !app.IsSet() {
		return nil, errNoURL
	}
	cfg := app.PoolConfig()
	setApplicationName(cfg.ConnConfig, applicationName)
	if configure != nil {
		configure(cfg)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", app, err)
	}
	// The check is of the session user, which is the login itself. The
	// current user can be another role, set by a role parameter in the URL
	// or by a default of the login, but the session can still SET ROLE to
	// any role the login has.
	var login, version string
	var versionNum int
	if err := pool.QueryRow(ctx, "SELECT session_user, "+serverVersionColumns).
		Scan(&login, &versionNum, &version); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to %s: %w", app, err)
	}
	if err := checkServerVersion(versionNum, version); err != nil {
		pool.Close()
		return nil, err
	}
	if err := checkLogin(ctx, pool, appLoginRules, login); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{pool: pool}, nil
}

// Close closes the pool, waiting for the connections in use to be returned.
func (db *DB) Close() {
	db.pool.Close()
}

// Ping checks that the database answers, on one of the pool's connections.
func (db *DB) Ping(ctx context.Context) error {
	return db.pool.Ping(ctx)
}

// Pool returns the pool, for the job queue (package jobs), whose library runs
// its own queries on it, in River's tables, which have no row-level security
// (PRD §5.4). Row-level security holds the pool's connections as it holds
// every connection of the login, so work on a workspace's rows still goes
// through InTx.
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

// Dedicated returns a connection of the application login that the pool no
// longer holds, for work that keeps a connection for as long as it runs, as
// the real-time hub's LISTEN does (package realtime, PRD ADR-005), rather
// than one transaction at a time: the pool opens another in its place, so
// that the connection takes none of the pool's from its requests. The caller
// closes it. Its scope is unset, as every connection's is outside InTx, so
// row-level security holds it to no workspace's rows. Its session has no
// idle_session_timeout, which the server, the database, or the login may
// set, and which would end it as it waits, idle, as LISTEN waits for its
// notifications, as the migration lock's session has none either.
func (db *DB) Dedicated(ctx context.Context) (*pgx.Conn, error) {
	c, err := db.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire a connection: %w", err)
	}
	conn := c.Hijack()
	if _, err := conn.Exec(ctx, "SET idle_session_timeout = 0"); err != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("switch off the idle session timeout: %w", err)
	}
	return conn, nil
}

// Notify sends payload on the channel of PostgreSQL's LISTEN and NOTIFY, on
// one of the pool's connections, outside a transaction, so that the
// listeners of every server of the instance are told of it at once (package
// realtime). It reads and writes no table.
func (db *DB) Notify(ctx context.Context, channel, payload string) error {
	if _, err := db.pool.Exec(ctx, "SELECT pg_notify($1, $2)", channel, payload); err != nil {
		return fmt.Errorf("notify %s: %w", channel, err)
	}
	return nil
}

// setApplicationName names the connections name, unless the URL, PGAPPNAME,
// or a service file already does.
func setApplicationName(c *pgx.ConnConfig, name string) {
	if _, ok := c.RuntimeParams["application_name"]; !ok {
		c.RuntimeParams["application_name"] = name
	}
}

// Scope is who a request transaction acts for. Row-level security policies
// compare against it through the settings app.workspace_id and app.user_id,
// which hold the IDs as UUIDs (PRD §5.4). A zero ID sets its setting to the
// empty string, so a policy must read the setting as
//
//	NULLIF(current_setting('app.workspace_id', true), '')::uuid
//
// which is null, and matches no row, when the transaction has no workspace.
type Scope struct {
	WorkspaceID ids.UUID
	UserID      ids.UUID
}

// InTx runs fn in a transaction of the application login, with scope set for
// row-level security as SET LOCAL would set it: until the transaction ends,
// so that the next transaction on the same connection starts without it. The
// transaction commits if fn returns nil, and rolls back if fn returns an
// error or panics.
func (db *DB) InTx(ctx context.Context, scope Scope, fn func(tx pgx.Tx) error) error {
	return db.inTx(ctx, scope, pgx.TxOptions{}, fn)
}

// InSnapshot runs fn as InTx does, in a transaction that only reads, and that
// reads the database as of one moment, that of its first statement, which
// sets the scope (REPEATABLE READ, READ ONLY): for a read of several
// statements that must agree with one another, as a read a page at a time,
// without locking what it reads, which a change then commits to meanwhile
// without the read seeing it.
func (db *DB) InSnapshot(ctx context.Context, scope Scope, fn func(tx pgx.Tx) error) error {
	return db.inTx(ctx, scope, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, fn)
}

// inTx runs fn as InTx does, in a transaction of the options opts.
func (db *DB) inTx(ctx context.Context, scope Scope, opts pgx.TxOptions, fn func(tx pgx.Tx) error) error {
	return pgx.BeginTxFunc(ctx, db.pool, opts, func(tx pgx.Tx) error {
		err := queries.New(tx).SetScope(ctx, queries.SetScopeParams{
			WorkspaceID: settingValue(scope.WorkspaceID),
			UserID:      settingValue(scope.UserID),
		})
		if err != nil {
			return fmt.Errorf("set the request scope: %w", err)
		}
		return fn(tx)
	})
}

func settingValue(id ids.UUID) string {
	if id == (ids.UUID{}) {
		return ""
	}
	return id.String()
}

// Interval returns d as an interval parameter of the queries, in
// microseconds, without days or months, which PostgreSQL adds to a
// timestamptz as the same length of time whatever the calendar.
func Interval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}

// Pending returns the versions of the embedded migrations, or of the
// StartOptions.Migrations that Start opened the DB with, that the database
// has not applied, in ascending order. If the database has applied a
// migration that the server does not have, because a newer version of
// Storycreep migrated it, the error is an *UnknownMigrationsError.
func (db *DB) Pending(ctx context.Context) ([]int64, error) {
	ms, err := loadMigrations(db.migrations)
	if err != nil {
		return nil, err
	}
	return db.pendingIn(ctx, ms)
}

// pendingIn is Pending for the migrations ms.
func (db *DB) pendingIn(ctx context.Context, ms migrationSet) ([]int64, error) {
	applied, err := appliedVersions(ctx, db.pool)
	if err != nil {
		return nil, err
	}
	return pending(ms, applied)
}

// StartOptions configure Start.
type StartOptions struct {
	// AutoMigrate makes Start apply the pending migrations before it opens
	// the pool. Without it, Start only reports them.
	AutoMigrate bool
	// Log receives what Start did. Nil discards it.
	Log *slog.Logger
	// Migrations are the migrations to apply, or to compare the database
	// with, as MigrateOptions.Migrations. The DB that Start returns compares
	// with them in Pending too.
	Migrations fs.FS
}

// Start is the database step of the server's startup (PRD FR-ADMIN-04). With
// AutoMigrate, it applies the pending migrations under the migration lock,
// which makes instances that start together safe, and logs that it waits if
// another session holds the lock. It provisions the application login, and
// the maintenance login if the configuration has one, and returns the
// application login's pool that Migrate opened to check that the migrations
// reached the database the login connects to.
// Without AutoMigrate, it opens the application login's pool, and a pending
// migration is logged as a warning and is for "storycreep migrate" to apply,
// while the server reports that it is not ready. It refuses a database that
// migrating has set up but whose server lacks the server's roles, as after a
// dump of it is restored into another server (checkServerRoles), or that
// PUBLIC or one of the server's roles can connect to again, as after a dump is
// restored into a new database, because nothing else would revoke that
// CONNECT until "storycreep migrate" runs (checkConnectRestricted).
func Start(ctx context.Context, cfg *config.Config, opts StartOptions) (*DB, error) {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if opts.AutoMigrate {
		res, db, err := migrate(ctx, cfg.DatabaseAdminURL, cfg.DatabaseURL, MigrateOptions{
			Migrations:  opts.Migrations,
			Maintenance: cfg.DatabaseMaintenanceURL,
			Waiting: func() {
				log.InfoContext(ctx, "waiting for the database migration lock, which another session holds")
			},
		})
		// The migrations applied before a failed one stay applied, so they
		// are logged too.
		for _, m := range res.Applied {
			log.InfoContext(ctx, "applied a database migration", "version", m.Version, "name", m.Name,
				"duration", m.Duration)
		}
		if err != nil {
			return nil, err
		}
		log.InfoContext(ctx, "the database is up to date", "version", res.Version)
		return db, nil
	}
	db, err := Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	ms, err := loadMigrations(opts.Migrations)
	var p []int64
	if err == nil {
		db.migrations = ms.fsys
		p, err = db.pendingIn(ctx, ms)
	}
	// A database that has applied a migration was set up by migrating, which
	// created the server's roles and restricted who can connect to it. One
	// that has applied none has no data yet.
	if err == nil && len(p) < len(ms.versions) {
		err = checkServerRoles(ctx, db.pool)
	}
	if err == nil && len(p) < len(ms.versions) {
		err = checkConnectRestricted(ctx, db.pool)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	if len(p) > 0 {
		log.WarnContext(ctx, "the database has pending migrations; run storycreep migrate to apply them",
			"pending", p)
	}
	return db, nil
}

// The configuration keys of the URLs of the server's logins, which messages
// name.
const (
	appURLKey         = "STORYCREEP_DATABASE_URL"
	adminURLKey       = "STORYCREEP_DATABASE_ADMIN_URL"
	maintenanceURLKey = "STORYCREEP_DATABASE_MAINTENANCE_URL"
)

// UnsafeLoginError reports a login that is something its rules forbid
// (checkLogin): an application login that row-level security would not hold,
// or a maintenance login that could do more than bypass row-level security
// as storycreep_maintenance, so the server refuses to use it.
type UnsafeLoginError struct {
	// Key is the configuration key of the URL that names the login.
	Key   string
	Login string
	// Reasons say why, one per role whose privileges the login has.
	Reasons []string

	want string
}

func (e *UnsafeLoginError) Error() string {
	return fmt.Sprintf("the login %q in %s must be %s, but %s", e.Login, e.Key, e.want, strings.Join(e.Reasons, "; "))
}

// privilegedRoles are the roles the migrations create for the server's
// privileged work. The application login must not have them (checkLogin).
var privilegedRoles = []string{definerRole, MaintenanceRole}

// loginRules say what one of the server's logins must not be (checkLogin), or
// be named (checkLoginName).
type loginRules struct {
	// key is the configuration key of the URL that names the login.
	key string
	// example is a name that the login could have, which the deployment
	// files give it, for checkLoginName to suggest.
	example string
	// want says what the login must be, for UnsafeLoginError.
	want string
	// forbidden are the server's roles that the login must not have.
	forbidden []string
	// allowed is the role of the server that the login acts as to bypass
	// row-level security, whose BYPASSRLS does not count against it, or ""
	// for none. Its other attributes, and what it owns, count as any role's
	// do.
	allowed string
}

// appLoginRules are the application login's: row-level security must hold
// it, so it must not have the server's privileged roles.
var appLoginRules = &loginRules{
	key:       appURLKey,
	example:   "storycreep",
	want:      "an ordinary role that row-level security applies to",
	forbidden: privilegedRoles,
}

// maintenanceLoginRules are the maintenance login's. It acts as
// storycreep_maintenance, which bypasses row-level security, but it must
// have no other privileges than that role's: not the attributes or roles that
// would make the application login unsafe, nor the application's role or the
// context bootstrap's, and it must not bypass row-level security itself,
// because storycreep_maintenance is to be the only role that does (PRD §5.4).
// Only that role's BYPASSRLS is let through: the first migration gives it
// nothing else, but nothing checks the role again afterwards, and if it had
// been given CREATEROLE or SUPERUSER since, the login would act with it.
var maintenanceLoginRules = &loginRules{
	key:       maintenanceURLKey,
	example:   "storycreep_maintainer",
	want:      "an ordinary role that bypasses row-level security only by acting as " + MaintenanceRole,
	forbidden: []string{appRole, definerRole},
	allowed:   MaintenanceRole,
}

// serverAccessRoles are PostgreSQL's predefined roles that read or write
// files on the database server, or run programs there, as the operating
// system user that runs PostgreSQL. PostgreSQL warns that each can be turned
// into superuser access, so the application login must not have them either
// (checkLogin).
var serverAccessRoles = []string{"pg_execute_server_program", "pg_read_server_files", "pg_write_server_files"}

// writeAllDataRoles are PostgreSQL's predefined roles that may add, change,
// and delete the rows of every table, as if granted INSERT, UPDATE, and DELETE
// on it. Row-level security still holds them, but what the server's logins
// may do rests on the migrations' grants too: the application may delete
// neither a user nor a workspace, whose cascades reach every workspace's rows
// past row-level security, and may neither change nor delete an audit entry
// (FR-TEN-07 AC1), and the maintenance login may do only what
// storycreep_maintenance is granted. So neither login may have them
// (checkLogin).
var writeAllDataRoles = []string{"pg_write_all_data"}

// unsafeRolesQuery lists the roles whose privileges the login $1 has, itself
// included, that row-level security would not hold, or that would let the
// login past the migrations' grants: a superuser, a role with BYPASSRLS, a
// role with CREATEROLE (which on PostgreSQL 15 can grant itself any role that
// is not a superuser, such as storycreep_maintenance), a role with
// REPLICATION (which can stream a copy of the whole cluster), a role that
// owns the current database or objects in it, the server's roles $2 that the
// login must not have, the server access roles $3, and the roles $5 that may
// change every table's rows. The BYPASSRLS of the role $4, which the login may
// act as, does not count, if $4 is not empty, but the role's other attributes
// do. An owner can switch row-level security off, the owner of the database
// can drop it, the owner of a schema can drop what is in it, and the owner of
// a type, a collation, an operator class, or an extension can drop with
// CASCADE the columns and indexes that use it, in tables it does not own.
//
// PostgreSQL records each object's owner in pg_shdepend, except where the
// owner is one of the roles that initdb created, whose OIDs are below 16384,
// such as pg_monitor. What those roles own, except the superusers, which count
// already, is read from the catalog of each kind of object that has an owner.
// By default, only pg_database_owner owns anything: the public schema. The
// database's owner is read from pg_database, because pg_shdepend records it
// with the cluster's objects. PostgreSQL makes the database's owner, and with
// it the roles that are members of the owner, a member of pg_database_owner.
// That role's row is left out, because the row of the role that owns the
// database says the same more plainly.
//
// Temporary objects do not count: they belong to the session that made them,
// and any session of the login may hold some while another instance starts.
// Neither do large objects, which any role may create and which give no
// power over other objects, default privileges, which own nothing, or user
// mappings, which PostgreSQL records as owned by the role they map but which
// only hold how it logs in to a foreign server.
const unsafeRolesQuery = `
SELECT r.rolname, r.rolsuper, r.rolbypassrls AND r.rolname <> $4::name AS bypass, r.rolcreaterole, r.rolreplication,
	r.owns_database, r.owns, r.rolname = ANY ($3::name[]) AS server_access, r.rolname = ANY ($5::name[]) AS writes_all
FROM (
	SELECT rolname, oid, rolsuper, rolbypassrls, rolcreaterole, rolreplication,
		EXISTS (SELECT FROM pg_database WHERE datname = current_database() AND datdba = pg_roles.oid)
			AS owns_database,
		EXISTS (
			SELECT FROM pg_shdepend d, pg_identify_object(d.classid, d.objid, d.objsubid) o
			WHERE d.refclassid = 'pg_authid'::regclass AND d.refobjid = pg_roles.oid AND d.deptype = 'o'
				AND d.dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
				AND d.classid NOT IN ('pg_largeobject'::regclass, 'pg_default_acl'::regclass, 'pg_user_mapping'::regclass)
				-- An object that a session dropped since has no identity.
				AND o.identity IS NOT NULL
				AND (o.schema IS NULL OR (o.schema NOT LIKE 'pg\_temp%' AND o.schema NOT LIKE 'pg\_toast\_temp%'))
		)
			OR NOT pg_roles.rolsuper AND pg_roles.oid < 16384 AND EXISTS (
				SELECT FROM (
					SELECT oid FROM pg_namespace WHERE nspowner = pg_roles.oid
					UNION ALL SELECT relnamespace FROM pg_class WHERE relowner = pg_roles.oid
					UNION ALL SELECT typnamespace FROM pg_type WHERE typowner = pg_roles.oid
					UNION ALL SELECT pronamespace FROM pg_proc WHERE proowner = pg_roles.oid
					UNION ALL SELECT collnamespace FROM pg_collation WHERE collowner = pg_roles.oid
					UNION ALL SELECT connamespace FROM pg_conversion WHERE conowner = pg_roles.oid
					UNION ALL SELECT oprnamespace FROM pg_operator WHERE oprowner = pg_roles.oid
					UNION ALL SELECT opcnamespace FROM pg_opclass WHERE opcowner = pg_roles.oid
					UNION ALL SELECT opfnamespace FROM pg_opfamily WHERE opfowner = pg_roles.oid
					UNION ALL SELECT cfgnamespace FROM pg_ts_config WHERE cfgowner = pg_roles.oid
					UNION ALL SELECT dictnamespace FROM pg_ts_dict WHERE dictowner = pg_roles.oid
					UNION ALL SELECT stxnamespace FROM pg_statistic_ext WHERE stxowner = pg_roles.oid
					UNION ALL SELECT NULL FROM pg_language WHERE lanowner = pg_roles.oid
					UNION ALL SELECT NULL FROM pg_extension WHERE extowner = pg_roles.oid
					UNION ALL SELECT NULL FROM pg_foreign_data_wrapper WHERE fdwowner = pg_roles.oid
					UNION ALL SELECT NULL FROM pg_foreign_server WHERE srvowner = pg_roles.oid
					UNION ALL SELECT NULL FROM pg_event_trigger WHERE evtowner = pg_roles.oid
					UNION ALL SELECT NULL FROM pg_publication WHERE pubowner = pg_roles.oid
					UNION ALL SELECT NULL FROM pg_subscription WHERE subowner = pg_roles.oid
						AND subdbid = (SELECT oid FROM pg_database WHERE datname = current_database())
				) owned (namespace)
				WHERE NOT EXISTS (
					SELECT FROM pg_namespace
					WHERE oid = owned.namespace AND (nspname LIKE 'pg\_temp%' OR nspname LIKE 'pg\_toast\_temp%')
				)
			)
			AS owns
	FROM pg_roles
) r
WHERE pg_has_role($1::name, r.oid, 'MEMBER')
	AND r.rolname <> 'pg_database_owner'
	AND (r.rolsuper OR (r.rolbypassrls AND r.rolname <> $4::name) OR r.rolcreaterole OR r.rolreplication
		OR r.owns_database OR r.owns OR r.rolname = ANY ($2::name[]) OR r.rolname = ANY ($3::name[])
		OR r.rolname = ANY ($5::name[]))
ORDER BY r.rolname <> $1::name, r.rolname`

// checkLogin returns an *UnsafeLoginError if the login is something that
// rules forbid.
func checkLogin(ctx context.Context, q querier, rules *loginRules, login string) error {
	rows, err := q.Query(ctx, unsafeRolesQuery, login, rules.forbidden, serverAccessRoles, rules.allowed,
		writeAllDataRoles)
	if err != nil {
		return fmt.Errorf("check the login %q: %w", login, err)
	}
	defer rows.Close()
	var reasons []string
	for rows.Next() {
		var role string
		var super, bypass, createRole, replication, ownsDatabase, owns, serverAccess, writesAll bool
		if err := rows.Scan(&role, &super, &bypass, &createRole, &replication, &ownsDatabase, &owns,
			&serverAccess, &writesAll); err != nil {
			return fmt.Errorf("check the login %q: %w", login, err)
		}
		var what []string
		if super {
			// A superuser has every privilege, and usually every attribute
			// too, so the others say nothing more.
			what = append(what, "is a superuser")
		} else {
			if bypass {
				what = append(what, "has BYPASSRLS")
			}
			if createRole {
				what = append(what, "has CREATEROLE")
			}
			if replication {
				what = append(what, "has REPLICATION")
			}
			if ownsDatabase {
				what = append(what, "owns the database")
			}
			if owns {
				what = append(what, "owns objects in the database")
			}
			if serverAccess {
				what = append(what, "gives access to the database server's files and programs")
			}
			if writesAll {
				what = append(what, "may change the rows of every table, past what the migrations grant")
			}
		}
		if len(what) == 0 {
			what = append(what, "is reserved for the server's other work")
		}
		desc := strings.Join(what, " and ")
		if role == login {
			reasons = append(reasons, "it "+desc)
			if super {
				// PostgreSQL counts a superuser as a member of every
				// role, so the other rows say nothing more.
				break
			}
		} else {
			reasons = append(reasons, fmt.Sprintf("it is a member of %q, which %s", role, desc))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("check the login %q: %w", login, err)
	}
	if len(reasons) > 0 {
		return &UnsafeLoginError{Key: rules.key, Login: login, Reasons: reasons, want: rules.want}
	}
	return nil
}

// CheckMaintenanceLogin returns an *UnsafeLoginError if the session user of
// tx, a transaction of the maintenance login, could do more than bypass
// row-level security as storycreep_maintenance (maintenanceLoginRules).
func CheckMaintenanceLogin(ctx context.Context, tx pgx.Tx) error {
	var login string
	if err := tx.QueryRow(ctx, "SELECT session_user").Scan(&login); err != nil {
		return fmt.Errorf("check the login in %s: %w", maintenanceURLKey, err)
	}
	return checkLogin(ctx, tx, maintenanceLoginRules, login)
}

// SetMaintenanceRole makes tx, a transaction of the maintenance login, act as
// storycreep_maintenance until it ends, as SET LOCAL ROLE does, so that it
// bypasses row-level security (PRD §5.4). It returns an error if the login
// cannot, because migrating has not granted it the role, or if the statement
// fails otherwise, as when ctx ends.
func SetMaintenanceRole(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{MaintenanceRole}.Sanitize())
	var pgErr *pgconn.PgError
	switch {
	// insufficient_privilege: the login may not SET ROLE to the role.
	case errors.As(err, &pgErr) && pgErr.Code == "42501":
		return fmt.Errorf("the login in %s cannot act as %s, which storycreep migrate grants it when %s is "+
			"set: %w", maintenanceURLKey, MaintenanceRole, maintenanceURLKey, err)
	case err != nil:
		return fmt.Errorf("act as %s: %w", MaintenanceRole, err)
	}
	return nil
}

// UniqueViolation reports whether err is a violation of the unique constraint
// named constraint, as by an insert or an update that gives a row a key that
// another row has, which its caller answers as a key that is taken.
func UniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	// unique_violation.
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

// querier is what pgx.Conn, pgxpool.Pool, and pgx.Tx have in common for
// reading.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
