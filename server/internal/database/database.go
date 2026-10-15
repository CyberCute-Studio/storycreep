// Package database is the server's access to PostgreSQL (PRD ADR-001, ADR-002):
// the embedded migrations and the advisory locks they run under, the database
// roles and the application's login, the application's connection pool, and
// the request transaction that sets the row-level security context.
//
// Two logins are involved. The admin login, a superuser from
// STORYCREEP_DATABASE_ADMIN_URL, applies the migrations and owns every table.
// The application login, from STORYCREEP_DATABASE_URL, does everything else.
// It is an ordinary role, granted storycreep_app, that row-level security
// applies to: Migrate provisions it, and Open refuses a login that row-level
// security would not hold.
package database

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// applicationName is what the server's connections report in
// pg_stat_activity, unless the URL sets application_name, or PGAPPNAME or a
// service file does.
const applicationName = "storycreep"

// errNoURL is returned by Open and Migrate when STORYCREEP_DATABASE_URL is not
// set.
var errNoURL = errors.New("STORYCREEP_DATABASE_URL is not set")

// DB is the application login's connection pool.
type DB struct {
	pool *pgxpool.Pool
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
// that does, of one of the server's privileged roles, or of one of
// PostgreSQL's roles that give access to the database server's files and
// programs, directly or through other roles. If it is, the error is an
// *UnsafeLoginError.
func Open(ctx context.Context, app config.DatabaseURL) (*DB, error) {
	if !app.IsSet() {
		return nil, errNoURL
	}
	cfg := app.PoolConfig()
	setApplicationName(cfg.ConnConfig)
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
	if err := checkLogin(ctx, pool, login); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{pool: pool}, nil
}

// Close closes the pool, waiting for the connections in use to be returned.
func (db *DB) Close() {
	db.pool.Close()
}

// setApplicationName names the connections, unless the URL, PGAPPNAME, or a
// service file already does.
func setApplicationName(c *pgx.ConnConfig) {
	if _, ok := c.RuntimeParams["application_name"]; !ok {
		c.RuntimeParams["application_name"] = applicationName
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
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
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

// Pending returns the versions of the embedded migrations that the database
// has not applied, in ascending order. If the database has applied a
// migration that the server does not have, because a newer version of
// Storycreep migrated it, the error is an *UnknownMigrationsError.
func (db *DB) Pending(ctx context.Context) ([]int64, error) {
	ms, err := loadMigrations(nil)
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
}

// Start is the database step of the server's startup (PRD FR-ADMIN-04). With
// AutoMigrate, it applies the pending migrations under the migration lock,
// which makes instances that start together safe, and logs that it waits if
// another session holds the lock. It provisions the application login, and
// returns the application login's pool that Migrate opened to check that the
// migrations reached the database the login connects to.
// Without AutoMigrate, it opens the application login's pool, and a pending
// migration is logged as a warning and is for "storycreep migrate" to apply,
// while the server reports that it is not ready.
func Start(ctx context.Context, cfg *config.Config, opts StartOptions) (*DB, error) {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if opts.AutoMigrate {
		res, db, err := migrate(ctx, cfg.DatabaseAdminURL, cfg.DatabaseURL, MigrateOptions{Waiting: func() {
			log.InfoContext(ctx, "waiting for the database migration lock, which another session holds")
		}})
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
	p, err := db.Pending(ctx)
	switch {
	case err != nil:
		db.Close()
		return nil, err
	case len(p) > 0:
		log.WarnContext(ctx, "the database has pending migrations; run storycreep migrate to apply them",
			"pending", p)
	}
	return db, nil
}

// UnsafeLoginError reports a login that row-level security would not hold,
// so the server refuses to use it as the application login.
type UnsafeLoginError struct {
	Login string
	// Reasons say why, one per role whose privileges the login has.
	Reasons []string
}

func (e *UnsafeLoginError) Error() string {
	return fmt.Sprintf("the login %q in STORYCREEP_DATABASE_URL must be an ordinary role that row-level security "+
		"applies to, but %s", e.Login, strings.Join(e.Reasons, "; "))
}

// privilegedRoles are the roles the migrations create for the server's
// privileged work. The application login must not have them (checkLogin).
var privilegedRoles = []string{"storycreep_definer", "storycreep_maintenance"}

// serverAccessRoles are PostgreSQL's predefined roles that read or write
// files on the database server, or run programs there, as the operating
// system user that runs PostgreSQL. PostgreSQL warns that each can be turned
// into superuser access, so the application login must not have them either
// (checkLogin).
var serverAccessRoles = []string{"pg_execute_server_program", "pg_read_server_files", "pg_write_server_files"}

// unsafeRolesQuery lists the roles whose privileges the login $1 has, itself
// included, that row-level security would not hold: a superuser, a role with
// BYPASSRLS, a role with CREATEROLE (which on PostgreSQL 15 can grant itself
// any role that is not a superuser, such as storycreep_maintenance), a role
// with REPLICATION (which can stream a copy of the whole cluster), a role
// that owns the current database or objects in it, the privileged roles $2,
// and the server access roles $3. An owner can switch row-level security
// off, the owner of the database can drop it, the owner of a schema can drop
// what is in it, and the owner of a type, a collation, an operator class, or
// an extension can drop with CASCADE the columns and indexes that use it, in
// tables it does not own.
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
SELECT r.rolname, r.rolsuper, r.rolbypassrls, r.rolcreaterole, r.rolreplication, r.owns_database, r.owns,
	r.rolname = ANY ($3::name[]) AS server_access
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
	AND (r.rolsuper OR r.rolbypassrls OR r.rolcreaterole OR r.rolreplication OR r.owns_database OR r.owns
		OR r.rolname = ANY ($2::name[]) OR r.rolname = ANY ($3::name[]))
ORDER BY r.rolname <> $1::name, r.rolname`

// checkLogin returns an *UnsafeLoginError if row-level security would not
// hold the login.
func checkLogin(ctx context.Context, q querier, login string) error {
	rows, err := q.Query(ctx, unsafeRolesQuery, login, privilegedRoles, serverAccessRoles)
	if err != nil {
		return fmt.Errorf("check the login %q: %w", login, err)
	}
	defer rows.Close()
	var reasons []string
	for rows.Next() {
		var role string
		var super, bypass, createRole, replication, ownsDatabase, owns, serverAccess bool
		if err := rows.Scan(&role, &super, &bypass, &createRole, &replication, &ownsDatabase, &owns,
			&serverAccess); err != nil {
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
		}
		if len(what) == 0 {
			what = append(what, "is reserved for the server's privileged work")
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
		return &UnsafeLoginError{Login: login, Reasons: reasons}
	}
	return nil
}

// querier is what pgx.Conn, pgxpool.Pool, and pgx.Tx have in common for
// reading.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
