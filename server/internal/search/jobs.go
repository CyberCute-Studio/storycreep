package search

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

// reindexLockWait is how long the job waits for a project's lock at most, as
// a batch of the project's commands holds it while it applies, which takes
// far less, so that the job writes the documents of a project that its users
// edit, rather than passing over it until its next period, an hour later; it
// passes over a project whose lock a transaction holds for longer, as one
// that runs on and on would hold it. A test makes it shorter, to pass over a
// project whose lock it holds.
var reindexLockWait = 5 * time.Second

// reindex is the job of the index (FR-JOBS-01's search index maintenance,
// FR-PROJ-03).
type reindex struct{}

func (reindex) Kind() string { return "search_reindex" }

// reindexPause is how long the job of the index waits before it runs again
// after a run that stopped once its time ran out while it indexed projects.
const reindexPause = time.Second

// RegisterJobs registers the job of the index in r, which runs as the server
// starts and then every hour, and again right away, after reindexPause, while
// its runs stop once their time runs out while they index projects
// (sweep.Fill.Work), so that the documents of content that predates them, or
// of an older Version, are written by one run after another, rather than a
// run's worth of them, sweep.Budget's, an hour. The job warns in log, or
// nowhere for nil, of the errors of a run that it runs again after, which the
// job itself does not keep.
func RegisterJobs(r *jobs.Registry, log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[reindex]) error {
		return fill().Work(ctx, job.DB, sweep.Within(sweep.Budget), log)
	})
	jobs.Periodic(r, reindexInterval, reindex{})
}

// Reindex writes the document of each node, connection, variable, and
// component, in the trash or not, that has none of the current Version or a
// newer one, of each project that it has not indexed whole of the version or
// a newer one, and records that it has once it finds none lacking
// (search_state), so that its later runs pass over the project, as
// sweep.Fill fills the projects: in transactions of reindexRows documents at
// most, each under the project's lock, for which it waits reindexLockWait at
// most, and for sweep.Budget, which leaves the projects that it does not
// reach for its next run. So it is safe to repeat. It returns how many
// documents it wrote.
func Reindex(ctx context.Context, db sweep.DB) (int64, error) {
	done, err := fill().Run(ctx, db, sweep.Within(sweep.Budget))
	return done.Written, err
}

// fill is the job of the index, as package sweep runs it, with the bounds
// that reindexRows and reindexLockWait hold as it is called.
func fill() sweep.Fill {
	return sweep.Fill{
		Name: "search", Verb: "index", Pause: reindexPause, LockWait: reindexLockWait,
		Warning: "the job of the search index runs again soon, past what it failed to index",
		Projects: func(ctx context.Context, q *queries.Queries, workspaceID ids.UUID) ([]pgtype.UUID, error) {
			return q.ProjectsToIndex(ctx, queries.ProjectsToIndexParams{WorkspaceID: nullUUID(workspaceID),
				Version: Version})
		},
		Project: reindexProject,
	}
}

// reindexProject writes, in the transaction q of the workspace workspaceID,
// which holds the lock of the project projectID, at most reindexRows of the
// documents that the project's entities lack of the current Version, or
// records that the project lacks none, and returns how many it wrote, and
// whether it recorded that (sweep.Fill.Project).
func reindexProject(ctx context.Context, q *queries.Queries, workspaceID, projectID ids.UUID) (int, bool, error) {
	ws, project := nullUUID(workspaceID), nullUUID(projectID)
	stale, err := q.UnindexedSearchEntities(ctx, queries.UnindexedSearchEntitiesParams{WorkspaceID: ws,
		ProjectID: project, Version: Version, MaxRows: reindexRows})
	if err != nil {
		return 0, false, fmt.Errorf("find the entities without a document: %w", err)
	}
	if len(stale) == 0 {
		if err := q.SetSearchState(ctx, queries.SetSearchStateParams{WorkspaceID: ws, ProjectID: project,
			Version: Version}); err != nil {
			return 0, false, fmt.Errorf("record the project's index: %w", err)
		}
		return 0, true, nil
	}
	var sel selection
	for _, s := range stale {
		sel.add(idKind(Kind(s.Kind)), s.ID.Bytes)
	}
	n, err := index(ctx, q, workspaceID, projectID, sel)
	if err != nil {
		return 0, false, err
	}
	// The entities are those that the transaction found, under the project's
	// lock, so each has its document now; a run that wrote none of them would
	// find them again until its time ran out.
	if n < len(stale) {
		return 0, false, fmt.Errorf("wrote %d of the documents of %d entities without one", n, len(stale))
	}
	return n, false, nil
}
