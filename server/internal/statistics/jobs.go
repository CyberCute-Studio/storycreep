package statistics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/sweep"
)

// countInterval is how often the job of the statistics runs, besides once
// as the server starts, so that the rows that a new version of the server
// lacks are written soon after it starts, and those that a run left are
// written within this long.
const countInterval = time.Hour

// countRows is how many texts the job counts in a transaction at most, so
// that a transaction, which holds its project's lock, keeps the project's
// batches waiting for about as long however many texts the project lacks the
// rows of. A test makes it smaller, to count a fixture's texts in several
// transactions.
var countRows int32 = 1000

// countLockWait is how long the job waits for a project's lock at most, as
// the search's job does (search's reindexLockWait): a batch of the project's
// commands holds it while it applies, which takes far less, so that the job
// counts the texts of a project that its users edit, rather than passing
// over it until its next period, an hour later. A test makes it shorter, to
// pass over a project whose lock it holds.
var countLockWait = 5 * time.Second

// countJob is the job of the statistics (FR-TEXT-06), which counts the texts
// that have no row of the current Version.
type countJob struct{}

func (countJob) Kind() string { return "text_statistics" }

// countPause is how long the job of the statistics waits before it runs
// again after a run that stopped once its time ran out while it counted
// projects.
const countPause = time.Second

// RegisterJobs registers the job of the statistics in r, which runs as the
// server starts and then every hour, and again right away, after countPause,
// while its runs stop once their time runs out while they count projects
// (work), so that the texts of content that predates them, or of an older
// Version, are counted by one run after another, as the search's job indexes
// its documents. The job warns in log, or nowhere for nil, of the errors of a
// run that it runs again after, which the job itself does not keep.
func RegisterJobs(r *jobs.Registry, log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[countJob]) error {
		return work(ctx, job.DB, sweep.Within(sweep.Budget), log)
	})
	jobs.Periodic(r, countInterval, countJob{})
}

// work runs the job of the statistics once, as CountProjects does, for as
// long as more reports that the run has time left, and returns an error of
// jobs.Snooze, which runs the job again after countPause without counting the
// attempt, if more reports that the time has run out once the run has
// counted texts, or recorded projects that lack none, as there are likely
// more to count; or else the run's errors, warning in log of those of a run
// that it snoozes after, as the search's job does.
func work(ctx context.Context, db sweep.DB, more func() bool, log *slog.Logger) error {
	done, err := countWithin(ctx, db, more)
	if (done.counted > 0 || done.recorded > 0) && !more() {
		if err != nil {
			log.WarnContext(ctx, "the job of the writing statistics runs again soon, past what it failed to count",
				"error", err)
		}
		return jobs.Snooze(countPause)
	}
	return err
}

// progress is what a run of the job of the statistics did: how many texts it
// counted, and of how many projects it recorded that they lack none
// (text_statistic_state).
type progress struct {
	counted, recorded int64
}

// add adds what another part of the run did to p.
func (p *progress) add(q progress) {
	p.counted += q.counted
	p.recorded += q.recorded
}

// CountProjects writes the row of the statistics of each node and connection,
// in the trash or not, that has none of the current Version or a newer one,
// of each project that it has not counted whole of the version or a newer
// one, and records that it has once it finds none lacking
// (text_statistic_state), so that its later runs pass over the project, as an
// older version's job passes over a project that a newer one has counted,
// during a rolling upgrade. It works on each workspace in transactions of the
// workspace (§5.4), as package sweep runs them, each of one project, which it
// locks as a batch of the project's commands does, so that no batch changes
// a text that it counts meanwhile, and which counts countRows texts at most.
// It waits for a project's lock while a batch holds it, for countLockWait at
// most, and skips a project whose lock a transaction holds for longer, which
// its next run counts, and one whose time in the trash has passed, which the
// purge deletes, goes on past a project whose count fails, and returns the
// errors of all of them, and stops once it has run for sweep.Budget, which
// leaves the projects that it does not reach for its next run. So it is safe
// to repeat. It returns how many texts it counted.
func CountProjects(ctx context.Context, db sweep.DB) (int64, error) {
	done, err := countWithin(ctx, db, sweep.Within(sweep.Budget))
	return done.counted, err
}

// countWithin is CountProjects, for as long as more reports that the run has
// time left, and returns what it did.
func countWithin(ctx context.Context, db sweep.DB, more func() bool) (progress, error) {
	var done progress
	err := sweep.Workspaces(ctx, db, more, func(workspaceID ids.UUID) error {
		p, err := countWorkspace(ctx, db, more, workspaceID)
		done.add(p)
		return err
	})
	return done, err
}

// countWorkspace counts, as CountProjects does, the texts of each project of
// the workspace workspaceID that it has not counted whole of the current
// Version, for as long as more reports that the run has time left, and
// returns what it did. It goes on past a project whose count fails, and
// returns the errors of all of them, but stops at once when ctx ends.
func countWorkspace(ctx context.Context, db sweep.DB, more func() bool, workspaceID ids.UUID) (progress, error) {
	var projects []pgtype.UUID
	err := db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		var err error
		projects, err = queries.New(tx).ProjectsToCount(ctx, queries.ProjectsToCountParams{
			WorkspaceID: nullUUID(workspaceID), Version: Version})
		return err
	})
	if err != nil {
		return progress{}, fmt.Errorf("statistics: find the projects of workspace %s to count: %w", workspaceID, err)
	}
	var done progress
	var errs []error
	for _, p := range projects {
		for more() {
			if err := ctx.Err(); err != nil {
				return done, errors.Join(append(errs, err)...)
			}
			n, recorded, last, err := countProject(ctx, db, workspaceID, p.Bytes)
			if err != nil {
				errs = append(errs, fmt.Errorf("statistics: count project %s: %w", ids.UUID(p.Bytes), err))
				break
			}
			done.counted += int64(n)
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

// countProject counts, in a transaction of the workspace, at most countRows
// of the texts of the project that have no row of the current Version, or
// records that the project lacks none, and returns how many it counted,
// whether it recorded that, and whether the project needs no further
// transaction: because it lacked none, or a transaction holds its lock for
// longer than countLockWait, which a later run waits out, or it is gone.
func countProject(ctx context.Context, db sweep.DB, workspaceID, projectID ids.UUID) (n int, recorded,
	done bool, err error) {
	err = db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		n, recorded, done = 0, false, false
		q := queries.New(tx)
		ws, project := nullUUID(workspaceID), nullUUID(projectID)
		// The search's job's queries of the project's lock, which are of the
		// project's row alone, and wait for a batch alike.
		if err := q.SetIndexLockWait(ctx, int32(countLockWait.Milliseconds())); err != nil {
			return fmt.Errorf("bound the wait for the project's lock: %w", err)
		}
		_, err := q.LockProjectToIndex(ctx, queries.LockProjectToIndexParams{WorkspaceID: ws, ID: project})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			done = true
			return nil
		case lockNotAvailable(err):
			return errLocked
		case err != nil:
			return fmt.Errorf("lock the project: %w", err)
		}
		stale, err := q.UncountedTexts(ctx, queries.UncountedTextsParams{WorkspaceID: ws, ProjectID: project,
			Version: Version, MaxRows: countRows})
		if err != nil {
			return fmt.Errorf("find the texts without a row: %w", err)
		}
		if len(stale) == 0 {
			if err := q.SetTextStatisticState(ctx, queries.SetTextStatisticStateParams{WorkspaceID: ws,
				ProjectID: project, Version: Version}); err != nil {
				return fmt.Errorf("record the project's statistics: %w", err)
			}
			recorded, done = true, true
			return nil
		}
		var sel selection
		for _, s := range stale {
			switch Kind(s.Kind) {
			case KindNode:
				sel.add(ids.Node, s.ID)
			case KindConnection:
				sel.add(ids.Connection, s.ID)
			}
		}
		if n, err = count(ctx, q, workspaceID, projectID, sel); err != nil {
			return err
		}
		// The texts are those that the transaction found, under the
		// project's lock, so each has its row now; a run that wrote none of
		// them would find them again until its time ran out.
		if n < len(stale) {
			return fmt.Errorf("counted %d of the %d texts without a row", n, len(stale))
		}
		return nil
	})
	switch {
	case errors.Is(err, errLocked):
		return 0, false, true, nil
	case err != nil:
		return 0, false, false, err
	}
	return n, recorded, done, nil
}

// errLocked is what countProject's transaction ends with when a transaction
// holds the project's lock for longer than countLockWait.
var errLocked = errors.New("statistics: a transaction holds the project's lock")

// lockNotAvailable reports whether err is PostgreSQL's lock_not_available,
// which a statement that waited for a lock for lock_timeout fails with
// (queries.SetIndexLockWait).
func lockNotAvailable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "55P03"
}
