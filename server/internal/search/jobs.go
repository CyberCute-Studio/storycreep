package search

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

// reindexInterval is how often the job of the index runs, besides once as
// the server starts, so that the documents that a new version of the server
// lacks are written soon after it starts, and those that a run left are
// written within this long.
const reindexInterval = time.Hour

// reindexRows is how many documents the job writes in a transaction at
// most, so that a transaction, which holds its project's lock, keeps the
// project's batches waiting for about as long however many documents the
// project lacks. A test makes it smaller, to write a fixture's documents in
// several transactions.
var reindexRows int32 = 1000

// reindex is the job of the index (FR-JOBS-01's search index maintenance,
// FR-PROJ-03).
type reindex struct{}

func (reindex) Kind() string { return "search_reindex" }

// reindexPause is how long the job of the index waits before it runs again
// after a run that stopped once its time ran out while it wrote documents.
const reindexPause = time.Second

// RegisterJobs registers the job of the index in r, which runs as the server
// starts and then every hour, and again right away, after reindexPause, while
// its runs stop once their time runs out while they write documents (work),
// so that the documents of content that predates them, or of an older
// Version, are written by one run after another, rather than a run's worth
// of them, sweep.Budget's, an hour.
func RegisterJobs(r *jobs.Registry) {
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[reindex]) error {
		return work(ctx, job.DB, sweep.Within(sweep.Budget))
	})
	jobs.Periodic(r, reindexInterval, reindex{})
}

// work runs the job of the index once, as Reindex does, for as long as more
// reports that the run has time left, and returns an error of jobs.Snooze,
// which runs the job again after reindexPause without counting the attempt,
// if more reports that the time has run out once it has written documents,
// as there are likely more to write; or else the run's errors. A run that
// writes no document, as one of an instance whose projects are all indexed,
// waits for the job's next period, however long it takes.
func work(ctx context.Context, db sweep.DB, more func() bool) error {
	n, err := reindexWithin(ctx, db, more)
	if err == nil && n > 0 && !more() {
		return jobs.Snooze(reindexPause)
	}
	return err
}

// Reindex writes the document of each node, connection, variable, and
// component, in the trash or not, that has none of the current Version or a
// newer one, of each project that it has not indexed whole of the version or
// a newer one, and records that it has once it finds none lacking
// (search_state), so that its later runs pass over the project, as an older
// version's job passes over a project that a newer one has indexed, during a
// rolling upgrade. It works on each workspace in transactions of
// the workspace (§5.4), as package sweep runs them, each of one project,
// which it locks as a batch of the project's commands does, so that no
// batch changes an entity whose document it writes meanwhile, and which
// writes reindexRows documents at most. It skips a project whose lock a
// transaction holds, as a batch does, which its next run indexes, goes on
// past a project whose index fails, and returns the errors of all of them,
// and stops once it has run for sweep.Budget, which leaves the projects that
// it does not reach for its next run. So it is safe to repeat. It returns
// how many documents it wrote.
func Reindex(ctx context.Context, db sweep.DB) (int64, error) {
	return reindexWithin(ctx, db, sweep.Within(sweep.Budget))
}

// reindexWithin is Reindex, for as long as more reports that the run has
// time left.
func reindexWithin(ctx context.Context, db sweep.DB, more func() bool) (int64, error) {
	var written int64
	err := sweep.Workspaces(ctx, db, more, func(workspaceID ids.UUID) error {
		n, err := reindexWorkspace(ctx, db, more, workspaceID)
		written += n
		return err
	})
	return written, err
}

// reindexWorkspace indexes, as Reindex does, each project of the workspace
// workspaceID that it has not indexed whole of the current Version, for as
// long as more reports that the run has time left, and returns how many
// documents it wrote. It goes on past a project whose index fails, and
// returns the errors of all of them, but stops at once when ctx ends.
func reindexWorkspace(ctx context.Context, db sweep.DB, more func() bool, workspaceID ids.UUID) (int64, error) {
	var projects []pgtype.UUID
	err := db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		var err error
		projects, err = queries.New(tx).ProjectsToIndex(ctx, queries.ProjectsToIndexParams{
			WorkspaceID: nullUUID(workspaceID), Version: Version})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("search: find the projects of workspace %s to index: %w", workspaceID, err)
	}
	var written int64
	var errs []error
	for _, p := range projects {
		for more() {
			if err := ctx.Err(); err != nil {
				return written, errors.Join(append(errs, err)...)
			}
			n, done, err := reindexProject(ctx, db, workspaceID, p.Bytes)
			if err != nil {
				errs = append(errs, fmt.Errorf("search: index project %s: %w", ids.UUID(p.Bytes), err))
				break
			}
			written += int64(n)
			if done {
				break
			}
		}
	}
	return written, errors.Join(errs...)
}

// reindexProject writes, in a transaction of the workspace, at most
// reindexRows of the documents that the project's entities lack of the
// current Version, or records that the project lacks none, and returns how
// many it wrote, and whether the project needs no further transaction:
// because it lacked none, or a transaction holds its lock, which a later run
// waits out, or it is gone.
func reindexProject(ctx context.Context, db sweep.DB, workspaceID, projectID ids.UUID) (int, bool, error) {
	var n int
	var done bool
	err := db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		n, done = 0, false
		q := queries.New(tx)
		ws, project := nullUUID(workspaceID), nullUUID(projectID)
		_, err := q.LockProjectToIndex(ctx, queries.LockProjectToIndexParams{WorkspaceID: ws, ID: project})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			done = true
			return nil
		case err != nil:
			return fmt.Errorf("lock the project: %w", err)
		}
		stale, err := q.UnindexedSearchEntities(ctx, queries.UnindexedSearchEntitiesParams{WorkspaceID: ws,
			ProjectID: project, Version: Version, MaxRows: reindexRows})
		if err != nil {
			return fmt.Errorf("find the entities without a document: %w", err)
		}
		if len(stale) == 0 {
			done = true
			if err := q.SetSearchState(ctx, queries.SetSearchStateParams{WorkspaceID: ws, ProjectID: project,
				Version: Version}); err != nil {
				return fmt.Errorf("record the project's index: %w", err)
			}
			return nil
		}
		var sel selection
		for _, s := range stale {
			sel.add(idKind(Kind(s.Kind)), s.ID.Bytes)
		}
		if n, err = index(ctx, q, workspaceID, projectID, sel); err != nil {
			return err
		}
		// The entities are those that the transaction found, under the
		// project's lock, so each has its document now; a run that wrote
		// none of them would find them again until its time ran out.
		if n < len(stale) {
			return fmt.Errorf("wrote %d of the documents of %d entities without one", n, len(stale))
		}
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return n, done, nil
}
