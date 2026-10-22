package trash

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
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/sweep"
)

// purgeInterval is how often the purge job runs, so that an entity is purged
// within this long once its time in the trash has passed.
const purgeInterval = time.Hour

// purgeRows is how many rows the purge deletes in a transaction at most, so
// that a transaction, which holds its project's lock, keeps the project's
// batches waiting for about as long however much of its trash expires at
// once.
const purgeRows = 1000

// purge is the job that purges the projects' trash (FR-JOBS-01, FR-PROJ-06).
type purge struct{}

func (purge) Kind() string { return "trash_purge" }

// RegisterJobs registers the purge job in r, which runs every hour.
func RegisterJobs(r *jobs.Registry) {
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[purge]) error {
		_, err := Purge(ctx, job.DB)
		return err
	})
	jobs.Periodic(r, purgeInterval, purge{})
}

// Purge deletes for good what has been in its project's trash for Retention:
// the boards, with every row that refers to them, which the cascade of their
// foreign keys deletes, and then the deleted folders that hold nothing, in
// the trash or not, once those that they held are purged. It returns how
// many boards and folders it deleted. It works on each workspace in
// transactions of the workspace (§5.4), as package sweep runs them, each of
// one project, which it locks as a batch of the project's commands does, so
// that the batches see none of its deletions while they apply (plan note
// N30), and which deletes purgeRows rows at most. It skips a project whose
// lock a transaction holds, as a batch does, which its next run purges, and
// stops once it has run for sweep.Budget, which leaves the projects that it
// does not reach for its next run. So it is safe to repeat. It records no
// operation: an undo that would restore a purged entity finds that it no
// longer exists (§5.7).
func Purge(ctx context.Context, db sweep.DB) (int64, error) {
	more := sweep.Within(sweep.Budget)
	retention := database.Interval(Retention)
	var purged int64
	err := sweep.Workspaces(ctx, db, more, func(workspaceID ids.UUID) error {
		var projects []pgtype.UUID
		err := db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
			var err error
			projects, err = queries.New(tx).ProjectsToPurge(ctx, queries.ProjectsToPurgeParams{
				WorkspaceID: nullUUID(workspaceID), Retention: retention})
			return err
		})
		if err != nil {
			return fmt.Errorf("trash: find the projects of workspace %s whose trash to purge: %w", workspaceID, err)
		}
		for _, p := range projects {
			for more() {
				n, err := purgeProject(ctx, db, workspaceID, p, retention)
				if err != nil {
					return fmt.Errorf("trash: purge the trash of project %s: %w", ids.UUID(p.Bytes), err)
				}
				purged += n
				// A transaction that deletes fewer rows than it may found no
				// more, or found the project locked.
				if n < purgeRows {
					break
				}
			}
		}
		return nil
	})
	return purged, err
}

// purgeProject deletes, in a transaction of the workspace, at most purgeRows
// of the boards and the folders of the project that have been in its trash
// for retention, the boards first, which may leave a folder empty for the
// purge of the folders that follows, and returns how many it deleted, or
// none if a transaction holds the project's lock.
func purgeProject(ctx context.Context, db sweep.DB, workspaceID ids.UUID, project pgtype.UUID,
	retention pgtype.Interval) (int64, error) {
	var n int64
	err := db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		n = 0
		q := queries.New(tx)
		ws := nullUUID(workspaceID)
		_, err := q.LockProjectToPurge(ctx, queries.LockProjectToPurgeParams{WorkspaceID: ws, ID: project})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("lock the project: %w", err)
		}
		if n, err = q.PurgeBoards(ctx, queries.PurgeBoardsParams{WorkspaceID: ws, ProjectID: project,
			Retention: retention, MaxRows: purgeRows}); err != nil {
			return fmt.Errorf("purge the boards: %w", err)
		}
		// Each statement deletes the folders that hold nothing as it starts,
		// so a folder whose last child it deletes is deleted by the next.
		for n < purgeRows {
			folders, err := q.PurgeFolders(ctx, queries.PurgeFoldersParams{WorkspaceID: ws, ProjectID: project,
				Retention: retention, MaxRows: int32(purgeRows - n)})
			if err != nil {
				return fmt.Errorf("purge the folders: %w", err)
			}
			if folders == 0 {
				break
			}
			n += folders
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}
