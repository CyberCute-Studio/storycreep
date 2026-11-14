package sweep

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
)

// Fill is a job that writes the rows that the entities of each project lack
// of the current version of what it writes, as the jobs of the search's
// index (package search) and of the writing statistics (package statistics)
// write those of the content that predates them, or of an older version, and
// records of a project that it lacks none, so that its later runs pass over
// the project, as an older version's job passes over a project that a newer
// one has filled, during a rolling upgrade. It works on each workspace in
// transactions of the workspace (§5.4), as Workspaces runs them, each of one
// project, which it locks as a batch of the project's commands does, so that
// no batch changes an entity whose row it writes meanwhile, and which writes
// a bounded number of rows at most. It waits for a project's lock while a
// batch holds it, for LockWait at most, and skips a project whose lock a
// transaction holds for longer, which its next run fills, and one whose time
// in the trash has passed, which the purge deletes, goes on past a project
// whose fill fails, and returns the errors of all of them, and stops once
// more reports that the run has no time left, which leaves the projects that
// it does not reach for its next run. So it is safe to repeat.
type Fill struct {
	// Name names the package of the job in its errors, as search, and Verb
	// what it does to a project, as index.
	Name, Verb string
	// Warning is what Work warns of the errors of a run that it snoozes
	// after with.
	Warning string
	// Pause is how long the job waits before it runs again after a run that
	// stopped once its time ran out while it filled projects (Work).
	Pause time.Duration
	// LockWait is how long a transaction waits for a project's lock at
	// most: a batch of the project's commands holds it while it applies,
	// which takes far less, so that the job fills a project that its users
	// edit, rather than passing over it until its next period; it passes
	// over a project whose lock a transaction holds for longer, as one that
	// runs on and on would hold it.
	LockWait time.Duration
	// Projects returns, in the transaction q of the workspace workspaceID,
	// the workspace's projects that the job has not filled whole of its
	// version, or of a newer one, in the order of their IDs, but for those
	// whose time in the trash has passed.
	Projects func(ctx context.Context, q *queries.Queries, workspaceID ids.UUID) ([]pgtype.UUID, error)
	// Project writes, in the transaction q of the workspace workspaceID,
	// which holds the lock of the project projectID, a bounded number of the
	// rows that the project's entities lack at most, or records that they
	// lack none, and returns how many it wrote, and whether it recorded that.
	Project func(ctx context.Context, q *queries.Queries, workspaceID, projectID ids.UUID) (n int, recorded bool,
		err error)
}

// Progress is what a run of a Fill did: how many rows it wrote, and of how
// many projects it recorded that they lack none.
type Progress struct {
	Written, Recorded int64
}

// add adds what another part of the run did to p.
func (p *Progress) add(q Progress) {
	p.Written += q.Written
	p.Recorded += q.Recorded
}

// Work runs f once, as Run does, for as long as more reports that the run
// has time left, and returns an error of jobs.Snooze, which runs the job
// again after f.Pause without counting the attempt, if more reports that the
// time has run out once the run has written rows, or recorded projects that
// lack none, as there are likely more to write; or else the run's errors. It
// snoozes so even if the fill of some project failed, whose errors would
// otherwise have the job attempted again only after the retries' growing
// waits, and discarded after its last attempt, while the other projects'
// rows wait: such a project, which has no state recorded, fails again in
// each later run, and the first that has the time to reach every project
// returns its errors. Since a snoozed attempt keeps no error, it warns in log
// of the errors of a run that it snoozes after, so that a failure shows
// while a backlog is written too. A run that neither writes a row nor
// records a project, as one of an instance whose projects are all filled,
// waits for the job's next period, however long it takes.
func (f Fill) Work(ctx context.Context, db DB, more func() bool, log *slog.Logger) error {
	done, err := f.Run(ctx, db, more)
	if (done.Written > 0 || done.Recorded > 0) && !more() {
		if err != nil {
			log.WarnContext(ctx, f.Warning, "error", err)
		}
		return jobs.Snooze(f.Pause)
	}
	return err
}

// Run fills, as Fill says, the projects of every workspace, as Workspaces
// reaches them, for as long as more reports that the run has time left, and
// returns what it did.
func (f Fill) Run(ctx context.Context, db DB, more func() bool) (Progress, error) {
	var done Progress
	err := Workspaces(ctx, db, more, func(workspaceID ids.UUID) error {
		p, err := f.workspace(ctx, db, more, workspaceID)
		done.add(p)
		return err
	})
	return done, err
}

// workspace fills each project of the workspace workspaceID that the job has
// not filled whole, for as long as more reports that the run has time left,
// and returns what it did. It goes on past a project whose fill fails, and
// returns the errors of all of them, but stops at once when ctx ends.
func (f Fill) workspace(ctx context.Context, db DB, more func() bool, workspaceID ids.UUID) (Progress, error) {
	var projects []pgtype.UUID
	err := db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		var err error
		projects, err = f.Projects(ctx, queries.New(tx), workspaceID)
		return err
	})
	if err != nil {
		return Progress{}, fmt.Errorf("%s: find the projects of workspace %s to %s: %w", f.Name, workspaceID, f.Verb,
			err)
	}
	var done Progress
	var errs []error
	for _, p := range projects {
		for more() {
			if err := ctx.Err(); err != nil {
				return done, errors.Join(append(errs, err)...)
			}
			n, recorded, last, err := f.project(ctx, db, workspaceID, p.Bytes)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %s project %s: %w", f.Name, f.Verb, ids.UUID(p.Bytes), err))
				break
			}
			done.Written += int64(n)
			if recorded {
				done.Recorded++
			}
			if last {
				break
			}
		}
	}
	return done, errors.Join(errs...)
}

// project fills, in a transaction of the workspace, the project projectID
// once it holds its lock (Fill.Project), and returns how many rows it wrote,
// whether it recorded that the project lacks none, and whether the project
// needs no further transaction: because it lacked none, or a transaction
// holds its lock for longer than f.LockWait, which a later run waits out, or
// it is gone.
func (f Fill) project(ctx context.Context, db DB, workspaceID, projectID ids.UUID) (n int, recorded, done bool,
	err error) {
	err = db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		n, recorded, done = 0, false, false
		q := queries.New(tx)
		ws := pgtype.UUID{Bytes: workspaceID, Valid: true}
		if err := q.SetIndexLockWait(ctx, int32(f.LockWait.Milliseconds())); err != nil {
			return fmt.Errorf("bound the wait for the project's lock: %w", err)
		}
		_, err := q.LockProjectToIndex(ctx, queries.LockProjectToIndexParams{WorkspaceID: ws,
			ID: pgtype.UUID{Bytes: projectID, Valid: true}})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			done = true
			return nil
		case database.LockNotAvailable(err):
			return errLocked
		case err != nil:
			return fmt.Errorf("lock the project: %w", err)
		}
		n, recorded, err = f.Project(ctx, q, workspaceID, projectID)
		done = recorded
		return err
	})
	switch {
	case errors.Is(err, errLocked):
		return 0, false, true, nil
	case err != nil:
		return 0, false, false, err
	}
	return n, recorded, done, nil
}

// errLocked is what a Fill's transaction ends with when a transaction holds
// the project's lock for longer than its LockWait.
var errLocked = errors.New("sweep: a transaction holds the project's lock")
