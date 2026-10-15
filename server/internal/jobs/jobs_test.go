package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

// TestMain runs the tests with dbtest.Run, which keeps the libpq environment
// variables out of them, and stops PostgreSQL when they are done.
func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// recordArgs are the arguments of a test job that its worker records.
type recordArgs struct {
	N int `json:"n"`
}

func (recordArgs) Kind() string { return "test_record" }

// failArgs are the arguments of a test job whose worker fails.
type failArgs struct{}

func (failArgs) Kind() string { return "test_fail" }

// testRetry is a retry policy whose waits are short enough for a test.
var testRetry = backoff{first: 100 * time.Millisecond, max: time.Second}

// open opens a queue on the database d, as an instance of the server does,
// with a pool of its own, and a fast retry policy and polling. If work is set,
// it starts the workers. The queue is stopped and closed when the test ends.
func open(t *testing.T, d *dbtest.Database, reg *Registry, work bool) (*Queue, *database.DB) {
	t.Helper()
	db := d.Open(t)
	q, err := Open(context.Background(), db, d.AppURL, Options{
		Registry:     reg,
		Work:         work,
		retryPolicy:  testRetry,
		pollInterval: 50 * time.Millisecond,
		testOnly:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// A test that fails leaves jobs running that wait for it, which
		// Stop cancels after the timeout.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := q.Stop(ctx); err != nil {
			t.Error(err)
		}
		q.Close()
	})
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return q, db
}

// enqueue enqueues a job with args in a transaction of its own, which
// commits.
func enqueue(t *testing.T, q *Queue, db *database.DB, args Args, opts *EnqueueOptions) Enqueued {
	t.Helper()
	var e Enqueued
	err := db.InTx(context.Background(), database.Scope{}, func(tx pgx.Tx) error {
		var err error
		e, err = q.Enqueue(context.Background(), tx, args, opts)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// waitForState waits until the job id is in state, and returns its row.
func waitForState(t *testing.T, q *Queue, id int64, state rivertype.JobState) *rivertype.JobRow {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(20 * time.Millisecond) {
		job, err := q.client.JobGet(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == state {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %d is %s, want %s", id, job.State, state)
		}
	}
}

// receive returns the next value from ch, or fails the test after a minute.
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(time.Minute):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}

// TestRolledBackJobNeverRuns checks that a job enqueued in a transaction that
// rolls back never runs, and does not exist: a job enqueued after it, in a
// transaction that commits, runs alone.
//
// Verifies: FR-JOBS-01/AC1
func TestRolledBackJobNeverRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	ran := make(chan int, 10)
	reg := NewRegistry()
	Register(reg, func(_ context.Context, job *Job[recordArgs]) error {
		ran <- job.Args.N
		return nil
	})
	q, db := open(t, d, reg, true)

	errRollback := errors.New("roll back")
	var rolledBack Enqueued
	err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		if rolledBack, err = q.Enqueue(ctx, tx, recordArgs{N: 1}, nil); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("InTx returned %v, want the rollback", err)
	}
	committed := enqueue(t, q, db, recordArgs{N: 2}, nil)
	if n := receive(t, ran); n != 2 {
		t.Errorf("the job of the rolled-back transaction ran")
	}
	waitForState(t, q, committed.ID, rivertype.JobStateCompleted)
	if _, err := q.client.JobGet(ctx, rolledBack.ID); !errors.Is(err, river.ErrNotFound) {
		t.Errorf("JobGet of the rolled-back job returned %v, want river.ErrNotFound", err)
	}
	select {
	case n := <-ran:
		t.Errorf("job %d ran again", n)
	default:
	}
}

// TestInstancesRunEachJobOnce runs workers on two instances, each with pools
// of its own, as two server processes have, and checks that of many jobs
// enqueued at once, each runs exactly once, while both instances run jobs.
//
// Verifies: FR-JOBS-01/AC2
func TestInstancesRunEachJobOnce(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	var mu sync.Mutex
	runs := map[int64][]string{}
	instance := func(name string) (*Queue, *database.DB) {
		reg := NewRegistry()
		Register(reg, func(_ context.Context, job *Job[recordArgs]) error {
			mu.Lock()
			runs[job.ID] = append(runs[job.ID], name)
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			return nil
		})
		return open(t, d, reg, true)
	}
	a, db := instance("a")
	instance("b")

	const n = 100
	ids := make([]int64, n)
	err := db.InTx(context.Background(), database.Scope{}, func(tx pgx.Tx) error {
		for i := range ids {
			e, err := a.Enqueue(context.Background(), tx, recordArgs{N: i}, nil)
			if err != nil {
				return err
			}
			ids[i] = e.ID
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if job := waitForState(t, a, id, rivertype.JobStateCompleted); job.Attempt != 1 {
			t.Errorf("job %d was attempted %d times", id, job.Attempt)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	perInstance := map[string]int{}
	for _, id := range ids {
		if len(runs[id]) != 1 {
			t.Errorf("job %d ran on %v, want one instance once", id, runs[id])
		}
		for _, name := range runs[id] {
			perInstance[name]++
		}
	}
	if perInstance["a"] == 0 || perInstance["b"] == 0 {
		t.Errorf("the instances ran %v jobs, want both to run some", perInstance)
	}
}

// TestFailingJobIsDiscarded checks that a job that keeps failing is attempted
// as many times as it may, with the retry policy's growing waits between the
// attempts, and is then discarded with the error of each attempt.
//
// Verifies: FR-JOBS-01/AC3
func TestFailingJobIsDiscarded(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	reg := NewRegistry()
	Register(reg, func(_ context.Context, job *Job[failArgs]) error {
		return fmt.Errorf("attempt %d of %d failed", job.Attempt, job.MaxAttempts)
	})
	q, db := open(t, d, reg, true)

	e := enqueue(t, q, db, failArgs{}, &EnqueueOptions{MaxAttempts: 3})
	job := waitForState(t, q, e.ID, rivertype.JobStateDiscarded)
	if job.Attempt != 3 || len(job.Errors) != 3 {
		t.Fatalf("the job was attempted %d times, with %d errors; want 3 of each", job.Attempt, len(job.Errors))
	}
	for i, ae := range job.Errors {
		if want := fmt.Sprintf("attempt %d of 3 failed", i+1); ae.Error != want || ae.Attempt != i+1 {
			t.Errorf("error %d is %q of attempt %d, want %q", i, ae.Error, ae.Attempt, want)
		}
		if i > 0 {
			// The wait after the nth failure is at least 90% of
			// first × 2ⁿ⁻¹.
			gap := ae.At.Sub(job.Errors[i-1].At)
			if least := time.Duration(float64(testRetry.first) * 0.9 * float64(int(1)<<(i-1))); gap < least {
				t.Errorf("attempt %d came %s after attempt %d, want at least %s", i+1, gap, i, least)
			}
		}
	}
}

// TestBackoff checks the retry policy: the wait doubles with each failure,
// from a second up to an hour, give or take 10%, and the failures are counted
// by the job's errors.
//
// Verifies: FR-JOBS-01/AC3
func TestBackoff(t *testing.T) {
	t.Parallel()
	b := backoff{first: firstRetry, max: maxRetry}
	for n, want := range map[int]time.Duration{
		1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 12: 2048 * time.Second, 13: time.Hour,
		25: time.Hour, 2000: time.Hour,
	} {
		for range 100 {
			if got := b.wait(n); got < want*9/10 || got > want*11/10 {
				t.Errorf("the wait after failure %d is %s, want %s give or take 10%%", n, got, want)
				break
			}
		}
	}

	job := &rivertype.JobRow{Attempt: 5, Errors: make([]rivertype.AttemptError, 2)}
	start := time.Now()
	next := b.NextRetry(job)
	if wait := next.Sub(start); wait < 4*time.Second*9/10 || wait > 4*time.Second*11/10+time.Second {
		t.Errorf("NextRetry after a job's third failure is %s away, want about 4s", wait)
	}
}

// TestUniqueJobs checks that a unique job is not enqueued while an equal one
// waits or runs, but is once that one has completed, and that a job with
// other arguments is.
func TestUniqueJobs(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	started := make(chan int64, 10)
	release := make(chan struct{})
	reg := NewRegistry()
	Register(reg, func(ctx context.Context, job *Job[recordArgs]) error {
		started <- job.ID
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	q, db := open(t, d, reg, true)
	unique := &EnqueueOptions{Unique: true}

	// Enqueued in one transaction, before any worker can take the first.
	var first, waiting Enqueued
	err := db.InTx(context.Background(), database.Scope{}, func(tx pgx.Tx) error {
		var err error
		if first, err = q.Enqueue(context.Background(), tx, recordArgs{N: 1}, unique); err == nil {
			waiting, err = q.Enqueue(context.Background(), tx, recordArgs{N: 1}, unique)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Duplicate || !waiting.Duplicate || waiting.ID != first.ID {
		t.Errorf("enqueued %+v, then %+v while it waited; want the second to be its duplicate", first, waiting)
	}

	if id := receive(t, started); id != first.ID {
		t.Fatalf("job %d started, want %d", id, first.ID)
	}
	if running := enqueue(t, q, db, recordArgs{N: 1}, unique); !running.Duplicate || running.ID != first.ID {
		t.Errorf("enqueued %+v while the job ran, want its duplicate", running)
	}
	other := enqueue(t, q, db, recordArgs{N: 2}, unique)
	if other.Duplicate {
		t.Errorf("a job with other arguments is a duplicate")
	}
	close(release)
	waitForState(t, q, first.ID, rivertype.JobStateCompleted)
	waitForState(t, q, other.ID, rivertype.JobStateCompleted)
	if again := enqueue(t, q, db, recordArgs{N: 1}, unique); again.Duplicate {
		t.Errorf("enqueued %+v after the equal job completed, want a new job", again)
	}
}

// TestScheduledJob checks that a job scheduled for later does not run before
// it is due, and runs once it is.
func TestScheduledJob(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	ran := make(chan time.Time, 1)
	reg := NewRegistry()
	Register(reg, func(context.Context, *Job[recordArgs]) error {
		ran <- time.Now()
		return nil
	})
	q, db := open(t, d, reg, true)

	// River's scheduler makes a job available shortly before it is due, but
	// its workers take it only once it is.
	at := time.Now().Add(2 * time.Second)
	e := enqueue(t, q, db, recordArgs{}, &EnqueueOptions{At: at})
	if job, err := q.client.JobGet(context.Background(), e.ID); err != nil || !job.ScheduledAt.Equal(at.UTC()) &&
		job.ScheduledAt.Sub(at).Abs() > time.Millisecond {
		t.Errorf("the job is %v, %v; want it due at %s", job, err, at)
	}
	if when := receive(t, ran); when.Before(at) {
		t.Errorf("the job ran %s before it was due", at.Sub(when))
	}
}

// TestPeriodicJob runs workers with a periodic job on two instances, and
// checks that the job runs about once per interval: only the leader enqueues
// it.
func TestPeriodicJob(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	ran := make(chan int64, 100)
	instance := func() *Queue {
		reg := NewRegistry()
		Register(reg, func(_ context.Context, job *Job[recordArgs]) error {
			ran <- job.ID
			return nil
		})
		Periodic(reg, time.Second, recordArgs{})
		q, _ := open(t, d, reg, true)
		return q
	}
	a := instance()
	instance()

	var ids []int64
	for len(ids) < 4 {
		ids = append(ids, receive(t, ran))
	}
	var created []time.Time
	for _, id := range ids {
		created = append(created, waitForState(t, a, id, rivertype.JobStateCompleted).CreatedAt)
	}
	slices.SortFunc(created, time.Time.Compare)
	for i := 1; i < len(created); i++ {
		if gap := created[i].Sub(created[i-1]); gap < time.Second/2 {
			t.Errorf("periodic jobs were enqueued %s apart, want about a second: %v", gap, created)
		}
	}
}

func TestRegistryPanics(t *testing.T) {
	t.Parallel()
	for name, f := range map[string]func(){
		"periodic without a worker": func() { Periodic(NewRegistry(), time.Minute, recordArgs{}) },
		"periodic too often": func() {
			reg := NewRegistry()
			Register(reg, func(context.Context, *Job[recordArgs]) error { return nil })
			Periodic(reg, time.Millisecond, recordArgs{})
		},
		"two workers of a kind": func() {
			reg := NewRegistry()
			Register(reg, func(context.Context, *Job[recordArgs]) error { return nil })
			Register(reg, func(context.Context, *Job[recordArgs]) error { return nil })
		},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			f()
		}()
	}
}

// TestEnqueueUnknownKind checks that a job of a kind without a worker is not
// enqueued, by an instance that runs no workers too.
func TestEnqueueUnknownKind(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	reg := NewRegistry()
	Register(reg, func(context.Context, *Job[recordArgs]) error { return nil })
	q, db := open(t, d, reg, false)
	err := db.InTx(context.Background(), database.Scope{}, func(tx pgx.Tx) error {
		_, err := q.Enqueue(context.Background(), tx, failArgs{}, nil)
		return err
	})
	if err == nil {
		t.Error("a job of a kind without a worker was enqueued")
	}
	if e := enqueue(t, q, db, recordArgs{}, nil); e.ID == 0 {
		t.Error("the job has no ID")
	}
}
