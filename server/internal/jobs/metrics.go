package jobs

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// eventBuffer is how many events of finished jobs River holds for the metrics
// to read. River finishes up to 5,000 jobs in a batch.
const eventBuffer = 10_000

// countTimeout is how long a scrape waits for the database to count the jobs.
const countTimeout = 2 * time.Second

// otherKind is the kind label of a job of a kind that the queue has no worker
// for. Labelling such jobs by their kind would let the database's rows add
// series without end.
const otherKind = "other"

// Outcomes of an attempt at a job, the outcome label of attempts.
const (
	outcomeCompleted   = "completed"
	outcomeFailed      = "failed"
	outcomeDiscarded   = "discarded"
	outcomeCancelled   = "cancelled"
	outcomeSnoozed     = "snoozed"
	outcomeInterrupted = "interrupted"
)

// countedStates are the states that the jobs gauge counts, by River's names
// for them: those of the jobs that wait or run, and discarded, the
// dead-letter state.
var countedStates = []string{
	string(rivertype.JobStatePending),
	string(rivertype.JobStateScheduled),
	string(rivertype.JobStateAvailable),
	string(rivertype.JobStateRunning),
	string(rivertype.JobStateRetryable),
	string(rivertype.JobStateDiscarded),
}

// metrics are the job queue's Prometheus metrics: the attempts that this
// instance's workers finished, and how long they took, and the jobs in the
// database, which every instance counts alike.
type metrics struct {
	pool     *pgxpool.Pool
	kinds    []string
	attempts *prometheus.CounterVec
	duration *prometheus.HistogramVec
	jobs     *prometheus.Desc
}

func newMetrics(pool *pgxpool.Pool, kinds []string) *metrics {
	return &metrics{
		pool:  pool,
		kinds: kinds,
		attempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "storycreep_job_attempts_total",
			Help: "Attempts at jobs that this instance's workers finished, by kind and outcome: completed; failed, " +
				"to be attempted again; discarded, failed for the last time, which is the dead-letter state; " +
				"cancelled; snoozed; or interrupted by the instance stopping, to be attempted again.",
		}, []string{"kind", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "storycreep_job_duration_seconds",
			Help:    "How long the attempts at jobs that this instance's workers finished took, by kind.",
			Buckets: prometheus.ExponentialBuckets(0.01, 4, 9),
		}, []string{"kind"}),
		jobs: prometheus.NewDesc("storycreep_jobs",
			"Jobs in the database, by queue and state: pending, scheduled, available, running, retryable, which "+
				"failed and wait to be attempted again, and discarded, which failed for the last time and stay "+
				"for 7 days. Every instance reports the same counts.",
			[]string{"queue", "state"}, nil),
	}
}

func (m *metrics) Describe(ch chan<- *prometheus.Desc) {
	m.attempts.Describe(ch)
	m.duration.Describe(ch)
	ch <- m.jobs
}

func (m *metrics) Collect(ch chan<- prometheus.Metric) {
	m.attempts.Collect(ch)
	m.duration.Collect(ch)
	counts, err := m.count()
	if err != nil {
		// A server without auto-migration serves its metrics while River's
		// migrations are still pending.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			return
		}
		ch <- prometheus.NewInvalidMetric(m.jobs, err)
		return
	}
	for key, n := range counts {
		ch <- prometheus.MustNewConstMetric(m.jobs, prometheus.GaugeValue, float64(n), key.queue, key.state)
	}
}

type queueState struct{ queue, state string }

// count counts the jobs in countedStates, by queue and state. Every state of
// the default queue is counted, so that a state without jobs is a zero rather
// than a series that comes and goes.
func (m *metrics) count() (map[queueState]int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), countTimeout)
	defer cancel()
	// The states are compared as River's enum, so that River's index on
	// (state, queue, …) finds the rows.
	rows, err := m.pool.Query(ctx, "SELECT queue, state::text, count(*) FROM "+
		pgx.Identifier{schema, "river_job"}.Sanitize()+" WHERE state = ANY ($1::text[]::"+
		pgx.Identifier{schema, "river_job_state"}.Sanitize()+"[]) GROUP BY 1, 2", countedStates)
	if err != nil {
		return nil, err
	}
	counts := map[queueState]int64{}
	for _, s := range countedStates {
		counts[queueState{river.QueueDefault, s}] = 0
	}
	var key queueState
	var n int64
	_, err = pgx.ForEachRow(rows, []any{&key.queue, &key.state, &n}, func() error {
		counts[key] = n
		return nil
	})
	return counts, err
}

// observe counts the finished attempts that River reports in events, until
// River closes the channel.
func (m *metrics) observe(events <-chan *river.Event) {
	for e := range events {
		kind := e.Job.Kind
		if !slices.Contains(m.kinds, kind) {
			kind = otherKind
		}
		var outcome string
		switch e.Kind {
		case river.EventKindJobCompleted:
			outcome = outcomeCompleted
		case river.EventKindJobFailed:
			outcome = outcomeFailed
			if e.Job.State == rivertype.JobStateDiscarded {
				outcome = outcomeDiscarded
			}
		case river.EventKindJobCancelled:
			outcome = outcomeCancelled
		case river.EventKindJobSnoozed:
			outcome = outcomeSnoozed
		case river.EventKindJobInterrupted:
			outcome = outcomeInterrupted
		default:
			continue
		}
		m.attempts.WithLabelValues(kind, outcome).Inc()
		if e.JobStats != nil {
			m.duration.WithLabelValues(kind).Observe(e.JobStats.RunDuration.Seconds())
		}
	}
}
