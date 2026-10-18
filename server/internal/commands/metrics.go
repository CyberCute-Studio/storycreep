package commands

import (
	"errors"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
)

// The results of a batch, as the metrics label them.
const (
	resultApplied  = "applied"
	resultInvalid  = "invalid"
	resultNotFound = "not_found"
	resultDenied   = "denied"
	// resultOverLimit is a batch past a limit of the workspace's plan, which
	// the API answers with limit_exceeded.
	resultOverLimit        = "over_limit"
	resultRevisionConflict = "revision_conflict"
	// resultArchived is a batch of an archived project that changes more than
	// an archived project allows, which the API answers with
	// project_archived.
	resultArchived = "archived"
	// resultRejected is a batch that a command refused, or that failed,
	// which applied nothing.
	resultRejected = "rejected"
)

// results are the results of a batch, which the metrics start at zero.
var results = []string{resultApplied, resultInvalid, resultNotFound, resultDenied, resultOverLimit,
	resultRevisionConflict, resultArchived, resultRejected}

// metrics are the pipeline's Prometheus metrics: the command throughput and
// the conflict rate (PRD NFR-OBS-02). They label commands by their type,
// which only a registered type can be, so that no client adds series.
type metrics struct {
	batches   *prometheus.CounterVec
	commands  *prometheus.CounterVec
	conflicts *prometheus.CounterVec
	duration  *prometheus.HistogramVec
}

func newMetrics(types []Type) *metrics {
	m := &metrics{
		batches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "storycreep_command_batches_total",
			Help: "Batches of commands that this instance's pipeline finished, by result: applied; invalid, refused " +
				"before the database was read; not_found, for a project that does not exist, is in the trash, whose " +
				"workspace is being deleted, or that the actor has no role on; denied, by the actor's role; " +
				"over_limit, past a limit of the workspace's plan; revision_conflict, made against a revision that an " +
				"entity no longer has; archived, a change of an archived project, which is read-only; or rejected, " +
				"refused by a command or failed. Only an applied batch changes anything.",
		}, []string{"result"}),
		commands: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "storycreep_commands_applied_total",
			Help: "Commands that this instance's pipeline applied, by type.",
		}, []string{"type"}),
		conflicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "storycreep_command_revision_conflicts_total",
			Help: "Batches of commands that this instance's pipeline refused with revision_conflict, by the types of " +
				"the commands that expect a revision that an entity no longer has: a batch counts once for each.",
		}, []string{"type"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "storycreep_command_batch_duration_seconds",
			Help:    "How long this instance's pipeline took to finish batches of commands, by result.",
			Buckets: prometheus.ExponentialBuckets(0.001, 4, 9),
		}, []string{"result"}),
	}
	// The series start at zero, so that a rate of them is defined before
	// the first batch.
	for _, r := range results {
		m.batches.WithLabelValues(r)
	}
	for _, t := range types {
		m.commands.WithLabelValues(string(t))
		m.conflicts.WithLabelValues(string(t))
	}
	return m
}

func (m *metrics) Describe(ch chan<- *prometheus.Desc) {
	m.batches.Describe(ch)
	m.commands.Describe(ch)
	m.conflicts.Describe(ch)
	m.duration.Describe(ch)
}

func (m *metrics) Collect(ch chan<- prometheus.Metric) {
	m.batches.Collect(ch)
	m.commands.Collect(ch)
	m.conflicts.Collect(ch)
	m.duration.Collect(ch)
}

// observe counts a batch that took elapsed and ended with err, whose
// commands, if it decoded, were cmds.
func (m *metrics) observe(cmds []decoded, err error, elapsed time.Duration) {
	r := result(err)
	m.batches.WithLabelValues(r).Inc()
	m.duration.WithLabelValues(r).Observe(elapsed.Seconds())
	var conflict *RevisionConflictError
	switch {
	case err == nil:
		for _, c := range cmds {
			m.commands.WithLabelValues(string(c.spec.typ)).Inc()
		}
	case errors.As(err, &conflict):
		// A batch counts once for each type of command that expects a stale
		// revision.
		var types []Type
		for _, c := range conflict.Conflicts {
			if !slices.Contains(types, c.Type) {
				types = append(types, c.Type)
				m.conflicts.WithLabelValues(string(c.Type)).Inc()
			}
		}
	}
}

// result returns the label of the result of a batch that ended with err.
func result(err error) string {
	var invalid *InvalidError
	var limit *entitlements.LimitError
	var conflict *RevisionConflictError
	switch {
	case err == nil:
		return resultApplied
	case errors.As(err, &invalid):
		return resultInvalid
	case errors.Is(err, ErrNotFound):
		return resultNotFound
	case errors.Is(err, authz.ErrInsufficientRole), errors.Is(err, authz.ErrOwnerRequired):
		return resultDenied
	case errors.As(err, &limit):
		return resultOverLimit
	case errors.As(err, &conflict):
		return resultRevisionConflict
	case errors.Is(err, ErrProjectArchived):
		return resultArchived
	}
	return resultRejected
}
