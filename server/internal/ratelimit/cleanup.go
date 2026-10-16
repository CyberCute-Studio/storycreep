package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
)

// cleanupBatch is how many rows the rate limit cleanup job deletes in a
// transaction.
const cleanupBatch = 1000

// cleanup is the job that deletes the counters whose window has ended, and
// the sign-in attempts that are forgotten (FR-JOBS-01), which count nothing
// any more.
type cleanup struct{}

func (cleanup) Kind() string { return "rate_limit_cleanup" }

// RegisterJobs registers the jobs of the rate limits in r: the rate limit
// cleanup, which runs every hour.
func (l *Limiter) RegisterJobs(r *jobs.Registry) {
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[cleanup]) error {
		_, err := l.cleanUp(ctx, job.DB)
		return err
	})
	jobs.Periodic(r, cleanupInterval, cleanup{})
}

// cleanupInterval is how often the rate limit cleanup job runs.
const cleanupInterval = time.Hour

// cleanUp deletes the counters whose window has ended and the attempts that
// are forgotten, in batches, each in a transaction of its own, and returns
// how many rows it deleted. Deleting them changes no limit, so it is safe to
// repeat.
func (l *Limiter) cleanUp(ctx context.Context, db DB) (int64, error) {
	var total int64
	for _, deletion := range []struct {
		// what names the rows that del deletes, for its error.
		what string
		del  func(*queries.Queries) (int64, error)
	}{
		{"the counters whose window has ended", func(q *queries.Queries) (int64, error) {
			return q.DeleteEndedRateLimits(ctx, cleanupBatch)
		}},
		{"the forgotten sign-in attempts", func(q *queries.Queries) (int64, error) {
			return q.DeleteForgottenLoginAttempts(ctx, queries.DeleteForgottenLoginAttemptsParams{
				Forget: interval(l.policy.Lockout.Forget), MaxRows: cleanupBatch})
		}},
	} {
		for {
			var n int64
			err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
				var err error
				n, err = deletion.del(queries.New(tx))
				return err
			})
			if err != nil {
				return total, fmt.Errorf("ratelimit: delete %s: %w", deletion.what, err)
			}
			total += n
			if n < cleanupBatch {
				break
			}
		}
	}
	return total, nil
}
