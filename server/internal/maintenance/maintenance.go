// Package maintenance is the maintenance path (PRD §5.4, ADR-002): the pool
// of the storycreep_maintenance role, which bypasses row-level security, for
// the closed list of jobs that must see the rows of every workspace, or of
// workspaces that no longer exist:
//
//   - the audit retention job (FR-TEN-07, FR-TEN-05 AC2);
//   - workspace and account hard deletion (FR-TEN-05, FR-GDPR-02);
//   - the import ID-collision check, which returns one boolean (§5.2);
//   - the instance-wide aggregates of the instance admin screens, which
//     return counts, never content (FR-ADMIN-03).
//
// Each of them is written in this package, which keeps the pool to itself:
// DB runs nothing for its callers but those jobs, and a test fails when a
// package other than this one imports it (TestOnlyMaintenanceImportsIt). The
// request path never uses it.
//
// The pool logs in as the maintenance login of
// STORYCREEP_DATABASE_MAINTENANCE_URL, which migrating provisions
// (database.Migrate). PostgreSQL does not pass BYPASSRLS on to the members of
// a role, so each of the pool's transactions acts as storycreep_maintenance,
// as SET LOCAL ROLE makes it, and the login has no other privileges than that
// role gives it.
package maintenance

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
)

// applicationName is what the pool's connections report in pg_stat_activity,
// unless the URL sets application_name, or PGAPPNAME or a service file does.
const applicationName = "storycreep-maintenance"

// ErrNoURL is returned by Open when STORYCREEP_DATABASE_MAINTENANCE_URL is not
// set.
var ErrNoURL = errors.New("STORYCREEP_DATABASE_MAINTENANCE_URL is not set")

// DB is the maintenance login's connection pool.
type DB struct {
	pool *pgxpool.Pool
}

// Open connects to the database as the maintenance login. It returns an
// *database.UnsafeLoginError if the login has more privileges than
// storycreep_maintenance gives it (database.CheckMaintenanceLogin), and an
// error if it cannot act as that role, because migrating did not grant it.
func Open(ctx context.Context, url config.DatabaseURL) (*DB, error) {
	if !url.IsSet() {
		return nil, ErrNoURL
	}
	cfg := url.PoolConfig()
	if _, ok := cfg.ConnConfig.RuntimeParams["application_name"]; !ok {
		cfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err == nil {
		err = pool.Ping(ctx)
		if err != nil {
			pool.Close()
		}
	}
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", url, err)
	}
	db := &DB{pool: pool}
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := database.CheckMaintenanceLogin(ctx, tx); err != nil {
			return err
		}
		return database.SetMaintenanceRole(ctx, tx)
	}); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}

// Close closes the pool, waiting for the connections in use to be returned.
func (db *DB) Close() {
	db.pool.Close()
}

// inTx runs fn in a transaction that acts as storycreep_maintenance, and so
// sees and changes the rows of every workspace. The transaction commits if fn
// returns nil, and rolls back if fn returns an error or panics. Every query of
// the maintenance jobs runs in one, because their privileges are the role's.
func (db *DB) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := database.SetMaintenanceRole(ctx, tx); err != nil {
			return err
		}
		return fn(tx)
	})
}
