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
// after a run that stopped once its time ran out while it indexed projects.
const reindexPause = time.Second

// RegisterJobs registers the job of the index in r, which runs as the server
// starts and then every hour, and again right away, after reindexPause, while
// its runs stop once their time runs out while they index projects (work),
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
// if more reports that the time has run out once the run has written
// documents, or recorded projects that lack none, as there are likely more
// to index; or else the run's errors. It snoozes so even if the index of
// some project failed, whose errors would otherwise have the job attempted
// again only after the retries' growing waits, and discarded after its last
// attempt, while the other projects' documents wait: such a project, which
// has no state recorded, fails again in each later run, and the first that
// has the time to reach every project returns its errors. A run that
// neither writes a document nor records a project, as one of an instance
// whose projects are all indexed, waits for the job's next period, however
// long it takes.
func work(ctx context.Context, db sweep.DB, more func() bool) error {
	done, err := reindexWithin(ctx, db, more)
	if (done.written > 0 || done.recorded > 0) && !more() {
		return jobs.Snooze(reindexPause)
	}
	return err
}

// progress is what a run of the job of the index did: how many documents it
// wrote, and of how many projects it recorded that they lack none
// (search_state).
type progress struct {
	written, recorded int64
}

// add adds what another part of the run did to p.
func (p *progress) add(q progress) {
	p.written += q.written
	p.recorded += q.recorded
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
// transaction holds, as a batch does, which its next run indexes, and one
// whose time in the trash has passed, which the purge deletes, goes on
// past a project whose index fails, and returns the errors of all of them,
// and stops once it has run for sweep.Budget, which leaves the projects that
// it does not reach for its next run. So it is safe to repeat. It returns
// how many documents it wrote.
func Reindex(ctx context.Context, db sweep.DB) (int64, error) {
	done, err := reindexWithin(ctx, db, sweep.Within(sweep.Budget))
	return done.written, err
}

// reindexWithin is Reindex, for as long as more reports that the run has
// time left, and returns what it did.
func reindexWithin(ctx context.Context, db sweep.DB, more func() bool) (progress, error) {
	var done progress
	err := sweep.Workspaces(ctx, db, more, func(workspaceID ids.UUID) error {
		p, err := reindexWorkspace(ctx, db, more, workspaceID)
		done.add(p)
		return err
	})
	return done, err
}

// reindexWorkspace indexes, as Reindex does, each project of the workspace
// workspaceID that it has not indexed whole of the current Version, for as
// long as more reports that the run has time left, and returns what it did.
// It goes on past a project whose index fails, and returns the errors of all
// of them, but stops at once when ctx ends.
func reindexWorkspace(ctx context.Context, db sweep.DB, more func() bool, workspaceID ids.UUID) (progress, error) {
	var projects []pgtype.UUID
	err := db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		var err error
		projects, err = queries.New(tx).ProjectsToIndex(ctx, queries.ProjectsToIndexParams{
			WorkspaceID: nullUUID(workspaceID), Version: Version})
		return err
	})
	if err != nil {
		return progress{}, fmt.Errorf("search: find the projects of workspace %s to index: %w", workspaceID, err)
	}
	var done progress
	var errs []error
	for _, p := range projects {
		for more() {
			if err := ctx.Err(); err != nil {
				return done, errors.Join(append(errs, err)...)
			}
			n, recorded, last, err := reindexProject(ctx, db, workspaceID, p.Bytes)
			if err != nil {
				errs = append(errs, fmt.Errorf("search: index project %s: %w", ids.UUID(p.Bytes), err))
				break
			}
			done.written += int64(n)
			if recorded {
				done.recorded++
			}
			if last {
				break
			}
		}
	}
	return done, errors.Join(errs...)
}

// reindexProject writes, in a transaction of the workspace, at most
// reindexRows of the documents that the project's entities lack of the
// current Version, or records that the project lacks none, and returns how
// many it wrote, whether it recorded that, and whether the project needs no
// further transaction: because it lacked none, or a transaction holds its
// lock, which a later run waits out, or it is gone.
func reindexProject(ctx context.Context, db sweep.DB, workspaceID, projectID ids.UUID) (n int, recorded,
	done bool, err error) {
	err = db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		n, recorded, done = 0, false, false
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
			if err := q.SetSearchState(ctx, queries.SetSearchStateParams{WorkspaceID: ws, ProjectID: project,
				Version: Version}); err != nil {
				return fmt.Errorf("record the project's index: %w", err)
			}
			recorded, done = true, true
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
		return 0, false, false, err
	}
	return n, recorded, done, nil
}
