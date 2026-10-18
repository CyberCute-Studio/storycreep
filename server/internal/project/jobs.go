package project

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/sweep"
)

// purgeInterval is how often the purge job runs. A project in the trash is
// purged within this long once its time there has passed, and until then
// nothing finds it, as though it were purged already.
const purgeInterval = time.Hour

// purge is the job that purges the projects whose time in the trash has
// passed (FR-JOBS-01, FR-PROJ-01 AC3).
type purge struct{}

func (purge) Kind() string { return "project_purge" }

// RegisterJobs registers the purge job in r, which runs every hour.
func RegisterJobs(r *jobs.Registry) {
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[purge]) error {
		_, err := Purge(ctx, job.DB)
		return err
	})
	jobs.Periodic(r, purgeInterval, purge{})
}

// Purge deletes every project whose time in the trash has passed, with every
// row of its content, which the cascade of their foreign keys to the project
// deletes, and records each deletion in the audit log, without an actor, and
// returns how many projects it deleted. It works on each workspace in
// transactions of the workspace (§5.4), one for each project, so that a
// project's purge commits whole, as package sweep runs them, and stops once
// it has run for sweep.Budget, which leaves the projects that it does not
// reach for its next run. It skips a project whose row a transaction has
// locked, as a batch of commands of the project holds it, which the next run
// purges. So it is safe to repeat.
func Purge(ctx context.Context, db sweep.DB) (int, error) {
	more := sweep.Within(sweep.Budget)
	purged := 0
	err := sweep.Workspaces(ctx, db, more, func(workspaceID ids.UUID) error {
		for more() {
			var done bool
			err := db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
				id, err := queries.New(tx).PurgeProject(ctx, nullUUID(workspaceID))
				switch {
				case errors.Is(err, pgx.ErrNoRows):
					done = true
					return nil
				case err != nil:
					return fmt.Errorf("project: purge a project of workspace %s: %w", workspaceID, err)
				}
				return audit.Record(ctx, tx, audit.Entry{Action: audit.ActionProjectPurge, WorkspaceID: workspaceID,
					SubjectID: id.Bytes, Result: audit.ResultSuccess})
			})
			if err != nil || done {
				return err
			}
			purged++
		}
		return nil
	})
	return purged, err
}
