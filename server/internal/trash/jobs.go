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

// purgeRows is how many boards, nodes, branch outputs, connections,
// variables, components, attribute definitions, component types, and folders
// the purge deletes in a transaction at most, so that a transaction, which holds its project's lock,
// keeps the project's batches waiting for about as long however much of its
// trash expires at once. The rows that refer to one of them, as a board's
// nodes and variables, a node's outputs, connections, and expressions, an
// output's connections and condition, a component's values, or a
// definition's values, one for each component of its type, go with it by the
// cascade of their foreign keys, uncounted, so that it is purged whole, in one
// transaction, and a restoration between two of the purge's transactions
// never brings it back without some of them, as a definition whose restore
// would give its default to each component that it found without a value.
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
// foreign keys deletes, as their nodes and variables, then the nodes, with
// their connections, the connections, the variables, the components, with
// their values, leaving the elements whose speaker one of them is without
// one, at a new revision, and the attribute definitions, with theirs, then
// the component types that hold no component, in the trash or not, with their
// definitions, and then the deleted folders that hold nothing, in the trash
// or not, once those that they held are purged. It returns how many of them
// it deleted, but for those that a cascade deleted. It works on each
// workspace in transactions of the workspace (§5.4), as package sweep runs
// them, each of one project, which it locks as a batch of the project's
// commands does, so that the batches see none of its deletions while they
// apply (plan note N30), and which deletes purgeRows of them at most. It
// skips a project whose lock a transaction holds, as a batch does, which its
// next run purges, goes on past a project whose purge fails, so that a
// project whose purge keeps failing keeps no other's trash from its purge,
// and returns the errors of all of them, and stops once it has run for
// sweep.Budget, which leaves the projects that it does not reach for its next
// run. So it is safe to repeat. It records no operation: an undo that would
// restore a purged entity finds that it no longer exists (§5.7).
func Purge(ctx context.Context, db sweep.DB) (int64, error) {
	more := sweep.Within(sweep.Budget)
	retention := database.Interval(Retention)
	var purged int64
	err := sweep.Workspaces(ctx, db, more, func(workspaceID ids.UUID) error {
		n, err := purgeWorkspace(ctx, db, more, workspaceID, retention)
		purged += n
		return err
	})
	return purged, err
}

// purgeWorkspace purges, as Purge does, the trash of each project of the
// workspace workspaceID whose trash holds a row that has been there for
// retention, for as long as more reports that the run has time left, and
// returns how many rows it deleted. It goes on past a project whose purge
// fails, and returns the errors of all of them, but stops at once when ctx
// ends.
func purgeWorkspace(ctx context.Context, db sweep.DB, more func() bool, workspaceID ids.UUID,
	retention pgtype.Interval) (int64, error) {
	var projects []pgtype.UUID
	err := db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		var err error
		projects, err = queries.New(tx).ProjectsToPurge(ctx, queries.ProjectsToPurgeParams{
			WorkspaceID: nullUUID(workspaceID), Retention: retention})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("trash: find the projects of workspace %s whose trash to purge: %w", workspaceID, err)
	}
	var purged int64
	var errs []error
	for _, p := range projects {
		for more() {
			if err := ctx.Err(); err != nil {
				return purged, errors.Join(append(errs, err)...)
			}
			n, err := purgeProject(ctx, db, workspaceID, p, retention)
			if err != nil {
				errs = append(errs, fmt.Errorf("trash: purge the trash of project %s: %w", ids.UUID(p.Bytes), err))
				break
			}
			purged += n
			// A transaction that deletes fewer rows than it may found no
			// more, or found the project locked.
			if n < purgeRows {
				break
			}
		}
	}
	return purged, errors.Join(errs...)
}

// purgeProject deletes, in a transaction of the workspace, at most purgeRows
// of the boards, the nodes, the branch outputs, the connections, the
// variables, the components, the attribute definitions, the folders, and the
// component types of the project that have been in its trash for retention,
// the boards, the nodes, the outputs, the connections, the variables, and the
// components first, which may leave
// a folder or a type empty for the purge of the folders and of the types that
// follows, and returns how many it deleted, or none if a transaction holds the
// project's lock.
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
		nodes, err := q.PurgeNodes(ctx, queries.PurgeNodesParams{WorkspaceID: ws, ProjectID: project,
			Retention: retention, MaxRows: int32(purgeRows - n)})
		if err != nil {
			return fmt.Errorf("purge the nodes: %w", err)
		}
		n += nodes
		outputs, err := q.PurgeBranchOutputs(ctx, queries.PurgeBranchOutputsParams{WorkspaceID: ws,
			ProjectID: project, Retention: retention, MaxRows: int32(purgeRows - n)})
		if err != nil {
			return fmt.Errorf("purge the branch outputs: %w", err)
		}
		n += outputs
		connections, err := q.PurgeConnections(ctx, queries.PurgeConnectionsParams{WorkspaceID: ws,
			ProjectID: project, Retention: retention, MaxRows: int32(purgeRows - n)})
		if err != nil {
			return fmt.Errorf("purge the connections: %w", err)
		}
		n += connections
		variables, err := q.PurgeVariables(ctx, queries.PurgeVariablesParams{WorkspaceID: ws, ProjectID: project,
			Retention: retention, MaxRows: int32(purgeRows - n)})
		if err != nil {
			return fmt.Errorf("purge the variables: %w", err)
		}
		n += variables
		components, err := q.PurgeComponents(ctx, queries.PurgeComponentsParams{WorkspaceID: ws,
			ProjectID: project, Retention: retention, MaxRows: int32(purgeRows - n)})
		if err != nil {
			return fmt.Errorf("purge the components: %w", err)
		}
		n += components
		attributes, err := q.PurgeAttributeDefs(ctx, queries.PurgeAttributeDefsParams{WorkspaceID: ws,
			ProjectID: project, Retention: retention, MaxRows: int32(purgeRows - n)})
		if err != nil {
			return fmt.Errorf("purge the attribute definitions: %w", err)
		}
		n += attributes
		types, err := q.PurgeComponentTypes(ctx, queries.PurgeComponentTypesParams{WorkspaceID: ws,
			ProjectID: project, Retention: retention, MaxRows: int32(purgeRows - n)})
		if err != nil {
			return fmt.Errorf("purge the component types: %w", err)
		}
		n += types
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
