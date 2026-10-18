package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/sweep"
)

// RecordRetention is how long a batch's idempotency record is kept at least
// (PRD FR-API-03 AC4, FR-GDPR-03): for as long, a batch sent again is
// answered as it was, refused or applied, and after that, a batch whose
// commands were applied is answered as already applied, and one that was
// refused is checked anew.
const RecordRetention = 24 * time.Hour

// pruneInterval is how often the pruning job runs, so that a record is
// pruned within this long once it is older than RecordRetention.
const pruneInterval = time.Hour

// pruneRows is about how many rows of records the pruning job deletes in a
// transaction: it stops at the batch that reaches it, whose rows it deletes
// whole, so that a transaction takes about as long however many commands the
// batches have, as many as an importer's (PRD FR-DATA-01).
const pruneRows = 10000

// prune is the job that prunes the idempotency records (FR-JOBS-01).
type prune struct{}

func (prune) Kind() string { return "idempotency_prune" }

// RegisterJobs registers the pruning job in r, which runs every hour.
func RegisterJobs(r *jobs.Registry) {
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[prune]) error {
		_, err := Prune(ctx, job.DB)
		return err
	})
	jobs.Periodic(r, pruneInterval, prune{})
}

// Prune deletes the idempotency records of every workspace's batches that
// are older than RecordRetention, each batch's whole, and returns how many
// batches' records it deleted (FR-API-03 AC4). It works on each workspace in
// transactions of the workspace (§5.4), about pruneRows rows at a time, as
// package sweep runs them, and stops once it has run for sweep.Budget, which
// leaves the records that it does not reach for its next run. It skips a
// record that a batch of commands holds, as one that deletes it, which a
// later run prunes. A command that was applied stays recognised by its row of
// applied_command, which nothing prunes, so pruning changes no answer but
// that of a batch sent again, and it is safe to repeat.
func Prune(ctx context.Context, db sweep.DB) (int64, error) {
	more := sweep.Within(sweep.Budget)
	var pruned int64
	err := sweep.Workspaces(ctx, db, more, func(workspaceID ids.UUID) error {
		for more() {
			var n queries.PruneBatchRecordsRow
			err := db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
				var err error
				n, err = queries.New(tx).PruneBatchRecords(ctx, queries.PruneBatchRecordsParams{
					WorkspaceID: nullUUID(workspaceID), Retention: database.Interval(RecordRetention),
					MaxRows: pruneRows})
				return err
			})
			if err != nil {
				return fmt.Errorf("commands: prune the idempotency records of workspace %s: %w", workspaceID, err)
			}
			pruned += n.Batches
			// A transaction that stops short of pruneRows found no more but
			// those that batches held, which the next run prunes.
			if n.Rows < pruneRows {
				return nil
			}
		}
		return nil
	})
	return pruned, err
}
