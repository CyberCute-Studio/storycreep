package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/riverqueue/river/rivertype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

// gather returns the value of each sample of the registry's metrics, by the
// metric's name and its labels, as name{label="value",…}; a histogram's value
// is its count of observations.
func gather(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{}
	for _, f := range families {
		for _, m := range f.GetMetric() {
			key := f.GetName() + "{"
			for i, l := range m.GetLabel() {
				if i > 0 {
					key += ","
				}
				key += l.GetName() + `="` + l.GetValue() + `"`
			}
			key += "}"
			switch f.GetType() {
			case dto.MetricType_COUNTER:
				values[key] = m.GetCounter().GetValue()
			case dto.MetricType_GAUGE:
				values[key] = m.GetGauge().GetValue()
			case dto.MetricType_HISTOGRAM:
				values[key] = float64(m.GetHistogram().GetSampleCount())
			}
		}
	}
	return values
}

// TestMetrics checks the queue's metrics: the attempts that the workers
// finished, by kind and outcome, how long they took, and the jobs in the
// database, by queue and state, the dead-letter states, discarded and
// cancelled, included.
func TestMetrics(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	reg := NewRegistry()
	Register(reg, func(context.Context, *Job[recordArgs]) error { return nil })
	Register(reg, func(context.Context, *Job[failArgs]) error { return errors.New("fails") })
	Register(reg, func(context.Context, *Job[cancelArgs]) error { return Cancel(errors.New("never works")) })
	q, db := open(t, d, reg, true)
	metrics := prometheus.NewRegistry()
	metrics.MustRegister(q.Collector())

	done := enqueue(t, q, db, recordArgs{}, nil)
	failed := enqueue(t, q, db, failArgs{}, &EnqueueOptions{MaxAttempts: 2})
	cancelled := enqueue(t, q, db, cancelArgs{}, nil)
	enqueue(t, q, db, recordArgs{N: 1}, &EnqueueOptions{At: time.Now().Add(time.Hour)})
	waitForState(t, q, done.ID, rivertype.JobStateCompleted)
	waitForState(t, q, failed.ID, rivertype.JobStateDiscarded)
	waitForState(t, q, cancelled.ID, rivertype.JobStateCancelled)

	want := map[string]float64{
		`storycreep_job_attempts_total{kind="test_record",outcome="completed"}`: 1,
		`storycreep_job_attempts_total{kind="test_fail",outcome="failed"}`:      1,
		`storycreep_job_attempts_total{kind="test_fail",outcome="discarded"}`:   1,
		`storycreep_job_attempts_total{kind="test_cancel",outcome="cancelled"}`: 1,
		`storycreep_job_duration_seconds{kind="test_record"}`:                   1,
		`storycreep_job_duration_seconds{kind="test_fail"}`:                     2,
		`storycreep_jobs{queue="default",state="available"}`:                    0,
		`storycreep_jobs{queue="default",state="running"}`:                      0,
		`storycreep_jobs{queue="default",state="scheduled"}`:                    1,
		`storycreep_jobs{queue="default",state="discarded"}`:                    1,
		`storycreep_jobs{queue="default",state="cancelled"}`:                    1,
	}
	// The workers report the attempts after the jobs' rows change.
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(20 * time.Millisecond) {
		got := gather(t, metrics)
		var wrong []string
		for k, v := range want {
			if got[k] != v {
				wrong = append(wrong, k)
			}
		}
		if len(wrong) == 0 {
			break
		}
		if time.Now().After(deadline) {
			for _, k := range wrong {
				t.Errorf("%s is %v, want %v", k, got[k], want[k])
			}
			break
		}
	}
}

// TestMetricsBeforeMigrations checks that the jobs gauge is left out, and
// the scrape does not fail, while River's tables do not exist yet, as while
// the migrations of a server without auto-migration are pending.
func TestMetricsBeforeMigrations(t *testing.T) {
	t.Parallel()
	d := dbtest.Empty(t)
	q, err := Open(context.Background(), d.Open(t), d.AppURL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	metrics := prometheus.NewRegistry()
	metrics.MustRegister(q.Collector())
	if got, err := metrics.Gather(); err != nil || len(got) != 0 {
		t.Errorf("Gather() = %v, %v; want no metrics and no error", got, err)
	}
}
