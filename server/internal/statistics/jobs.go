package statistics

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

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
// the search's job does (search's reindexLockWait), as sweep.Fill.LockWait
// says, so that the job counts the texts of a project that its users edit,
// rather than passing over it until its next period, an hour later. A test
// makes it shorter, to pass over a project whose lock it holds.
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
// (sweep.Fill.Work), so that the texts of content that predates them, or of
// an older Version, are counted by one run after another, as the search's job
// indexes its documents. The job warns in log, or nowhere for nil, of the
// errors of a run that it runs again after, which the job itself does not
// keep.
func RegisterJobs(r *jobs.Registry, log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[countJob]) error {
		return fill().Work(ctx, job.DB, sweep.Within(sweep.Budget), log)
	})
	jobs.Periodic(r, countInterval, countJob{})
}

// CountProjects writes the row of the statistics of each node and connection,
// in the trash or not, that has none of the current Version or a newer one,
// of each project that it has not counted whole of the version or a newer
// one, and records that it has once it finds none lacking
// (text_statistic_state), so that its later runs pass over the project, as
// sweep.Fill fills the projects: in transactions of countRows texts at most,
// each under the project's lock, for which it waits countLockWait at most,
// and for sweep.Budget, which leaves the projects that it does not reach for
// its next run. So it is safe to repeat. It returns how many texts it
// counted.
func CountProjects(ctx context.Context, db sweep.DB) (int64, error) {
	done, err := fill().Run(ctx, db, sweep.Within(sweep.Budget))
	return done.Written, err
}

// fill is the job of the statistics, as package sweep runs it, with the
// bounds that countRows and countLockWait hold as it is called.
func fill() sweep.Fill {
	return sweep.Fill{
		Name: "statistics", Verb: "count", Pause: countPause, LockWait: countLockWait,
		Warning: "the job of the writing statistics runs again soon, past what it failed to count",
		Projects: func(ctx context.Context, q *queries.Queries, workspaceID ids.UUID) ([]pgtype.UUID, error) {
			return q.ProjectsToCount(ctx, queries.ProjectsToCountParams{WorkspaceID: nullUUID(workspaceID),
				Version: Version})
		},
		Project: countProject,
	}
}

// countProject counts, in the transaction q of the workspace workspaceID,
// which holds the lock of the project projectID, at most countRows of the
// texts of the project that have no row of the current Version, or records
// that the project lacks none, and returns how many it counted, and whether
// it recorded that (sweep.Fill.Project).
func countProject(ctx context.Context, q *queries.Queries, workspaceID, projectID ids.UUID) (int, bool, error) {
	ws, project := nullUUID(workspaceID), nullUUID(projectID)
	stale, err := q.UncountedTexts(ctx, queries.UncountedTextsParams{WorkspaceID: ws, ProjectID: project,
		Version: Version, MaxRows: countRows})
	if err != nil {
		return 0, false, fmt.Errorf("find the texts without a row: %w", err)
	}
	if len(stale) == 0 {
		if err := q.SetTextStatisticState(ctx, queries.SetTextStatisticStateParams{WorkspaceID: ws,
			ProjectID: project, Version: Version}); err != nil {
			return 0, false, fmt.Errorf("record the project's statistics: %w", err)
		}
		return 0, true, nil
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
	n, err := count(ctx, q, workspaceID, projectID, sel)
	if err != nil {
		return 0, false, err
	}
	// The texts are those that the transaction found, under the project's
	// lock, so each has its row now; a run that wrote none of them would
	// find them again until its time ran out.
	if n < len(stale) {
		return 0, false, fmt.Errorf("counted %d of the %d texts without a row", n, len(stale))
	}
	return n, false, nil
}
