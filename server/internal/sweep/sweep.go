// Package sweep runs a periodic job's work over every workspace, one
// workspace at a time, as the jobs that work on each workspace's rows in a
// transaction of that workspace do (PRD §5.4): the rollup and the
// reconciliation of usage (package usage), and the purge of the projects
// whose time in the trash has passed (package project). It lists the
// workspaces from the workspace table, which has no row-level security, and
// never takes the maintenance path.
//
// A job has a minute to run (package jobs), which an instance with many
// workspaces would need more than to work on all of them, and whose end would
// fail the run, and attempt it again from the start, so that no run would
// reach the last ones. So a run stops starting workspaces once it has run for
// Budget, and goes on from a workspace picked at random, past the last to the
// first, so that the workspaces that one run does not reach, another does.
package sweep

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Budget is how long a run of a job starts workspaces' work for. It is well
// inside the minute that a job has.
const Budget = 40 * time.Second

// page is how many workspaces' IDs a run reads at once.
const page = 1000

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Within returns a function that reports whether budget has not passed since
// Within was called, which a run's work may ask too, to stop a workspace's
// work that has many parts once the run has no time left.
func Within(budget time.Duration) func() bool {
	end := time.Now().Add(budget)
	return func() bool { return time.Now().Before(end) }
}

// Workspaces calls fn with the ID of each workspace, whatever its status, for
// as long as more reports that the run has time left, as Within(Budget)
// does: in the order of their IDs, from the one after a workspace picked at
// random to the last, and then from the first to the one picked, so that it
// calls fn once for every workspace if more lets it, and a run that ends
// earlier leaves others than the last run did. It goes on past a workspace
// that fn fails for, and returns the errors of all of them, but stops at once
// when ctx ends.
func Workspaces(ctx context.Context, db DB, more func() bool, fn func(workspaceID ids.UUID) error) error {
	return workspaces(ctx, db, page, more, fn)
}

// workspaces is Workspaces, reading the IDs pageSize at a time.
func workspaces(ctx context.Context, db DB, pageSize int32, more func() bool,
	fn func(workspaceID ids.UUID) error) error {
	var start pgtype.UUID
	err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		if start, err = queries.New(tx).RandomWorkspaceID(ctx); errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	switch {
	case err != nil:
		return fmt.Errorf("sweep: pick the workspace to start at: %w", err)
	case !start.Valid:
		// The instance has no workspace.
		return nil
	}
	var errs []error
	for _, span := range []queries.WorkspaceIDsParams{{After: start}, {Through: start}} {
		span.MaxRows = pageSize
		for {
			var found []pgtype.UUID
			err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
				var err error
				found, err = queries.New(tx).WorkspaceIDs(ctx, span)
				return err
			})
			if err != nil {
				return errors.Join(append(errs, fmt.Errorf("sweep: list the workspaces: %w", err))...)
			}
			for _, id := range found {
				if err := ctx.Err(); err != nil {
					return errors.Join(append(errs, err)...)
				}
				if !more() {
					return errors.Join(errs...)
				}
				if err := fn(id.Bytes); err != nil {
					errs = append(errs, err)
				}
			}
			if len(found) < int(pageSize) {
				break
			}
			span.After = found[len(found)-1]
		}
	}
	return errors.Join(errs...)
}
