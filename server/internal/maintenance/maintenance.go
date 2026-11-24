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
// package other than this one imports it, or uses the names through which it
// could open such a pool of its own (TestOnlyMaintenanceImportsIt). The
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
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// ErrNoURL is returned by Open when STORYCREEP_DATABASE_MAINTENANCE_URL is not
// set.
var ErrNoURL = errors.New("STORYCREEP_DATABASE_MAINTENANCE_URL is not set")

// DB is the maintenance login's connection pool.
type DB struct {
	pool *pgxpool.Pool
}

// Open connects to the database as the maintenance login. It returns an
// *database.UnsafeLoginError if the login could do more than bypass
// row-level security as storycreep_maintenance
// (database.CheckMaintenanceLogin), and an error if it cannot act as that
// role, because migrating did not grant it.
func Open(ctx context.Context, url config.DatabaseURL) (*DB, error) {
	if !url.IsSet() {
		return nil, ErrNoURL
	}
	cfg := url.PoolConfig()
	// The connections report themselves in pg_stat_activity as the
	// maintenance login's, unless the URL, PGAPPNAME, or a service file
	// names them otherwise.
	if _, ok := cfg.ConnConfig.RuntimeParams["application_name"]; !ok {
		cfg.ConnConfig.RuntimeParams["application_name"] = database.MaintenanceApplicationName
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

// OpenConfigured is Open of the instance's maintenance login, for the server's
// startup and the command line, which keep the URL to this package.
func OpenConfigured(ctx context.Context, cfg *config.Config) (*DB, error) {
	return Open(ctx, cfg.DatabaseMaintenanceURL)
}

// IDTables are the tables whose IDs an authoring archive holds, of every
// workspace, which the import's collision check reads (PRD §5.2).
var IDTables = []string{"project", "folder", "board", "variable", "component_type", "attribute_def", "component",
	"node", "branch_output", "connection", "comment", "play_state", "snapshot", "asset"}

// idBatch is how many IDs one statement of the collision check asks about.
const idBatch = 10_000

// AnyIDInUse says whether any of list is the ID of a row of IDTables, in any
// workspace: one boolean, which names neither the ID nor the workspace, and is
// all that the import's report may tell (PRD §5.2).
func (db *DB) AnyIDInUse(ctx context.Context, list []ids.UUID) (bool, error) {
	var query strings.Builder
	query.WriteString("SELECT ")
	for i, table := range IDTables {
		if i > 0 {
			query.WriteString(" OR ")
		}
		fmt.Fprintf(&query, "EXISTS (SELECT FROM %s WHERE id = ANY($1::uuid[]))", pgx.Identifier{table}.Sanitize())
	}
	found := false
	err := db.inTx(ctx, func(tx pgx.Tx) error {
		for batch := range slices.Chunk(list, idBatch) {
			args := make([]pgtype.UUID, len(batch))
			for i, id := range batch {
				args[i] = pgtype.UUID{Bytes: id, Valid: true}
			}
			if err := tx.QueryRow(ctx, query.String(), args).Scan(&found); err != nil {
				return fmt.Errorf("maintenance: check the IDs: %w", err)
			}
			if found {
				return nil
			}
		}
		return nil
	})
	return found, err
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
