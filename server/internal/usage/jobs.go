package usage

import (
	"context"
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

// How often the jobs run. The rollup keeps the deltas that a read of usage
// sums few, and the reconciliation corrects drift, which only a change that
// records no delta, as a cascade, causes.
const (
	rollupInterval         = 5 * time.Minute
	reconciliationInterval = time.Hour
)

// rollup is the job that folds every workspace's deltas into its counters
// (FR-JOBS-01, FR-ENT-02).
type rollup struct{}

func (rollup) Kind() string { return "usage_rollup" }

// reconciliation is the job that corrects every workspace's drift from a
// recount (FR-ENT-02 AC1).
type reconciliation struct{}

func (reconciliation) Kind() string { return "usage_reconciliation" }

// RegisterJobs registers the jobs of usage metering in r: the rollup, which
// runs every 5 minutes, and the reconciliation, which runs every hour, each
// starting workspaces' work for at most sweep.Budget, which reaches every
// workspace of all but the largest instances.
func RegisterJobs(r *jobs.Registry) {
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[rollup]) error {
		return RollUp(ctx, job.DB)
	})
	jobs.Periodic(r, rollupInterval, rollup{})
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[reconciliation]) error {
		_, err := Reconcile(ctx, job.DB)
		return err
	})
	jobs.Periodic(r, reconciliationInterval, reconciliation{})
}

// RollUp folds every workspace's deltas into its counters, each workspace's
// in a transaction of its own (§5.4), or those of the workspaces that it
// reaches in sweep.Budget, going on from one picked at random
// (sweep.Workspaces).
// In each, it deletes the deltas that it can see and adds their sum for each
// key to the key's counter, so that the workspace's usage stays the same in
// every snapshot, before and after the transaction commits. A delta whose
// change has not committed is invisible to it, and stays for the next run.
// Two runs that fold one workspace at once fold each delta once: the second
// waits for the first's delete, and finds the deltas gone. So it is safe to
// repeat.
func RollUp(ctx context.Context, db DB) error {
	return sweep.Workspaces(ctx, db, sweep.Within(sweep.Budget), func(workspaceID ids.UUID) error {
		return db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
			q := queries.New(tx)
			sums, err := q.FoldUsageDeltas(ctx, nullUUID(workspaceID))
			if err != nil {
				return fmt.Errorf("usage: fold the deltas of workspace %s: %w", workspaceID, err)
			}
			add := queries.AddUsageCountersParams{WorkspaceID: nullUUID(workspaceID)}
			for _, s := range sums {
				// Deltas that add up to nothing leave the counter as it is.
				if s.Total == 0 {
					continue
				}
				add.Ids = append(add.Ids, nullUUID(ids.New()))
				add.Keys = append(add.Keys, s.Key)
				add.Totals = append(add.Totals, s.Total)
			}
			if len(add.Keys) == 0 {
				return nil
			}
			if err := q.AddUsageCounters(ctx, add); err != nil {
				return fmt.Errorf("usage: add the deltas of workspace %s to its counters: %w", workspaceID, err)
			}
			return nil
		})
	})
}

// drifts are the queries that recount each key that a recount can count:
// each returns how far the workspace's usage of the key is from the count of
// what it counts. A later item adds its key's query with the rows that it
// counts. APIRequests has none, because no rows but its deltas record the
// requests that it counts.
var drifts = map[Key]func(q *queries.Queries, ctx context.Context, workspaceID pgtype.UUID) (int64, error){
	Projects: (*queries.Queries).ProjectDrift,
	Members:  (*queries.Queries).MemberDrift,
}

// Reconcile corrects every workspace's usage of each key that a recount can
// count (drifts), or that of the workspaces that it reaches in sweep.Budget,
// going on from one picked at random (sweep.Workspaces), each workspace's key in
// a transaction of its own (§5.4), and returns how many keys it corrected. In
// each, it takes the lock of the workspace's key, so that two runs never
// correct one drift twice, and reads the count and the usage in one
// statement, and so in one snapshot, in which every change that it sees has
// both its rows and its delta, and records the difference as a delta, which
// the next rollup folds, so that only the rollup job writes the counters. So
// it is safe to repeat.
func Reconcile(ctx context.Context, db DB) (int, error) {
	corrected := 0
	err := sweep.Workspaces(ctx, db, sweep.Within(sweep.Budget), func(workspaceID ids.UUID) error {
		for _, key := range keys {
			drift := drifts[key]
			if drift == nil {
				continue
			}
			var d int64
			err := db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
				q := queries.New(tx)
				if err := lock(ctx, q, workspaceID, key); err != nil {
					return err
				}
				var err error
				if d, err = drift(q, ctx, nullUUID(workspaceID)); err != nil {
					return fmt.Errorf("usage: recount %s of workspace %s: %w", key, workspaceID, err)
				}
				return Record(ctx, tx, workspaceID, key, d)
			})
			if err != nil {
				return err
			}
			if d != 0 {
				corrected++
			}
		}
		return nil
	})
	return corrected, err
}
