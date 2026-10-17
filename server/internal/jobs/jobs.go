// Package jobs is the server's background job queue (PRD FR-JOBS-01,
// ADR-008). It runs on River, which keeps the jobs in River's tables, created
// by the migrations 00002 and 00003.
//
// A job is enqueued in the transaction of the change that needs it (Enqueue),
// so a job enqueued by a transaction that rolls back never exists, and never
// runs (FR-JOBS-01 AC1). A worker takes jobs with SELECT … FOR UPDATE SKIP
// LOCKED and marks them running in the same statement, so however many
// instances run workers, each job runs on one of them (AC2). A job whose work
// returns an error is attempted again after a wait that doubles with each
// failure (backoff), until it has had its attempts, 25 unless it says
// otherwise. Then it is discarded: it is not attempted again, and it stays in
// the database for 7 days with the error of each attempt. Work that meets a
// failure that no attempt would mend ends its job at once instead, which is
// then cancelled (Cancel), and stays as long, with its error. Discarded and
// cancelled are the dead-letter states (AC3). The server runs the workers in
// its own process, unless it is told not to, so that a deployment can run
// them on instances of their own (AC4).
//
// Delivery is at least once. An instance that stops without finishing a job,
// as when its process is killed, leaves the job running in the database, and
// River's rescuer attempts it again an hour later, so a job's work must be
// safe to repeat. A job also has a minute to run before its context is
// cancelled, which counts as a failed attempt.
//
// River's tables span workspaces and have no row-level security (PRD §5.4),
// so a job's arguments hold identifiers, never content, and its work reads and
// writes a workspace's rows in a transaction of that workspace, on the
// workers' pool that the job carries (Job.DB, database.DB.InTx).
package jobs

import (
	"cmp"
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
)

// schema is where River's tables are: the public schema, where the migrations
// create every table. River names it in its queries, so that a search_path
// that the URL, the login, or the database sets does not hide them.
const schema = "public"

// concurrency is how many jobs an instance's workers run at once.
const concurrency = 4

// poolSize is the size of the workers' pool: a connection for each job that
// runs (Job.DB), one that River holds for LISTEN, and two for River's own
// queries, which take jobs, finish them, and, on the leader, maintain the
// queue.
const poolSize = concurrency + 3

// startTimeout is how long Start waits for the database to answer before it
// starts the workers.
const startTimeout = 10 * time.Second

// hardStopTimeout is how long Stop waits for the jobs to return once it has
// cancelled them.
const hardStopTimeout = 5 * time.Second

// deadLetterRetention is how long River keeps a job in a dead-letter state,
// discarded or cancelled, with its errors, before it deletes it. It is
// River's default for a discarded job, while River's default for a cancelled
// one is a day.
const deadLetterRetention = 7 * 24 * time.Hour

// Args are the arguments of a kind of job. They are stored in the job's row
// as JSON, so they are a struct whose exported fields encoding/json encodes,
// and they hold identifiers rather than content.
type Args interface {
	// Kind names the kind of job, which the jobs of that kind are stored
	// with, so it must not change once jobs of the kind exist. It is
	// lowercase, with words separated by underscores, as snapshot_build.
	Kind() string
}

// Job is a job that a worker runs.
type Job[T Args] struct {
	ID int64
	// Attempt counts the attempts at the job, this one included.
	Attempt int
	// MaxAttempts is how many attempts the job has before it is discarded.
	MaxAttempts int
	Args        T
	// DB is the workers' pool, which the work reads and writes the database
	// on: a workspace's rows in a transaction of that workspace (InTx). The
	// pool has a connection for each job that runs, so the work uses one
	// connection at a time.
	DB *database.DB
}

// workersDB is the context key under which the queue's middleware hands a
// job's work the workers' pool.
type workersDB struct{}

// Registry holds the kinds of job that the server runs, and its periodic
// jobs. Register a kind before Open.
type Registry struct {
	workers *river.Workers
	kinds   []string
	// periodic are the periodic jobs, and periodicKinds their kinds, which
	// River identifies them by.
	periodic      []*river.PeriodicJob
	periodicKinds []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{workers: river.NewWorkers()}
}

// Register makes work the worker of the jobs of kind T. A job is done when
// work returns nil. When work returns an error or panics, the attempt fails,
// and the job is attempted again later, or discarded if that was its last
// attempt. The exception is a stopping server that cancels ctx (Queue.Stop):
// work that returns ctx's error then, or an error that wraps it, is attempted
// again later without the attempt counting, so return the error with %w. The
// other exception is an error of Cancel, which ends the job without another
// attempt. Register panics if kind T has a worker already.
func Register[T Args](r *Registry, work func(ctx context.Context, job *Job[T]) error) {
	var args T
	river.AddWorker(r.workers, river.WorkFunc(func(ctx context.Context, j *river.Job[T]) error {
		db, _ := ctx.Value(workersDB{}).(*database.DB)
		return work(ctx, &Job[T]{ID: j.ID, Attempt: j.Attempt, MaxAttempts: j.MaxAttempts, Args: j.Args, DB: db})
	}))
	r.kinds = append(r.kinds, args.Kind())
}

// Cancel returns err as the error of work that ends its job: work that
// returns it, or an error that wraps it, fails its attempt, and the job is
// cancelled rather than attempted again, however many attempts it has left.
// A cancelled job is in the dead-letter state, as a discarded one is
// (FR-JOBS-01 AC3): it stays in the database for 7 days (deadLetterRetention)
// with the errors of its attempts, err the last of them, and the
// storycreep_jobs gauge counts it, while storycreep_job_attempts_total counts
// its attempt as cancelled. It is for a failure that no attempt would mend,
// such as an email to an address that the SMTP server refuses for good.
func Cancel(err error) error {
	return cancelError{err}
}

// cancelError is an error of Cancel. It wraps River's own, which cancels the
// job, but reads as err alone, which the job keeps.
type cancelError struct{ err error }

func (e cancelError) Error() string { return e.err.Error() }

func (e cancelError) Unwrap() error { return river.JobCancel(e.err) }

// Periodic makes the server enqueue a job with args every interval, which
// is at least a second. The instance that River elects as the leader among
// those that run workers enqueues it: once when it becomes the leader, and
// then every interval. So it is enqueued once per interval while the leader
// stays, and once more each time another instance takes over. The job is
// unique, so it is not enqueued while the last one still waits or runs.
// Periodic panics unless args's kind has a worker, and if the kind has a
// periodic job already.
func Periodic(r *Registry, interval time.Duration, args Args) {
	kind := args.Kind()
	switch {
	case !slices.Contains(r.kinds, kind):
		panic(fmt.Sprintf("jobs: the periodic job %s has no worker; Register one first", kind))
	case slices.Contains(r.periodicKinds, kind):
		panic(fmt.Sprintf("jobs: the kind %s has a periodic job already", kind))
	case interval < time.Second:
		panic(fmt.Sprintf("jobs: the periodic job %s runs every %s, more often than once a second", kind, interval))
	}
	opts := (&EnqueueOptions{Unique: true}).river()
	r.periodic = append(r.periodic, river.NewPeriodicJob(river.PeriodicInterval(interval),
		func() (river.JobArgs, *river.InsertOpts) { return args, opts },
		&river.PeriodicJobOpts{ID: kind, RunOnStart: true}))
	r.periodicKinds = append(r.periodicKinds, kind)
}

// EnqueueOptions are the options of a job.
type EnqueueOptions struct {
	// At, if set, is when the job is due: it does not run before then.
	At time.Time
	// MaxAttempts is how many attempts the job has before it is discarded.
	// Zero means 25.
	MaxAttempts int
	// Unique makes Enqueue enqueue nothing while a job of the same kind with
	// equal arguments waits, runs, or waits to be attempted again. Once that
	// job has completed, been cancelled, or been discarded, an equal job can
	// be enqueued.
	Unique bool
}

// uniqueStates are the states of a job that keep an equal unique job from
// being enqueued: those of a job that waits or runs. River requires all of
// them but retryable in any list of states, and refuses to enqueue a job
// whose list lacks one. Its default list has completed too, which would keep
// an equal job from being enqueued until River deletes the completed one.
var uniqueStates = []rivertype.JobState{
	rivertype.JobStateAvailable,
	rivertype.JobStatePending,
	rivertype.JobStateRetryable,
	rivertype.JobStateRunning,
	rivertype.JobStateScheduled,
}

func (o *EnqueueOptions) river() *river.InsertOpts {
	if o == nil {
		return nil
	}
	opts := &river.InsertOpts{ScheduledAt: o.At, MaxAttempts: o.MaxAttempts}
	if o.Unique {
		opts.UniqueOpts = river.UniqueOpts{ByArgs: true, ByState: uniqueStates}
	}
	return opts
}

// Enqueued says what Enqueue did.
type Enqueued struct {
	// ID is the job's ID, or, if Duplicate is set, the ID of the job that the
	// new one would have duplicated.
	ID int64
	// Duplicate reports that the job is unique, and that an equal job waits
	// or runs, so Enqueue enqueued nothing.
	Duplicate bool
}

// Options configure Open.
type Options struct {
	// Registry holds the kinds of job that the queue can enqueue and run.
	// Nil means none.
	Registry *Registry
	// Work makes the queue run the jobs in this process, once Start is
	// called. Without it, the queue only enqueues them, for the instances
	// that run workers.
	Work bool
	// Log receives River's log. Nil discards it.
	Log *slog.Logger

	// Tests make the queue faster with these. Zero means the defaults.
	retryPolicy  river.ClientRetryPolicy
	pollInterval time.Duration
	hardStop     time.Duration
	testOnly     bool
}

// Queue is the job queue.
type Queue struct {
	client *river.Client[pgx.Tx]
	log    *slog.Logger
	// work is set if the queue runs jobs, and kinds are the kinds it has
	// workers for.
	work  bool
	kinds []string
	// workers is the workers' pool, or nil if the queue runs no jobs.
	workers *database.DB
	metrics *metrics
	// hardStop is how long Stop waits for the jobs once it has cancelled
	// them.
	hardStop time.Duration

	mu sync.Mutex
	// started is set while the workers run. stuck is set if they did not
	// stop, because jobs did not return when they were cancelled. stopped is
	// set once Stop has been called, after which Start does nothing.
	started, stuck, stopped bool
	// eventsDone is closed when the metrics have read the last event.
	eventsDone chan struct{}
}

// Open returns the job queue in the database that db, the application login's
// pool, connects to. Enqueue uses the transaction it is given, whatever pool
// that is from, and the queue's metrics count the jobs on db. If opts.Work is
// set and the registry has kinds of job, Open also opens a pool of the login
// for the workers, from the URL app, which River and the jobs' work (Job.DB)
// use, with connections enough that the workers never wait for the request
// pool's or make requests wait for theirs, and Start runs the workers.
func Open(ctx context.Context, db *database.DB, app config.DatabaseURL, opts Options) (*Queue, error) {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	reg := opts.Registry
	if reg == nil {
		reg = NewRegistry()
	}
	q := &Queue{log: log, work: opts.Work, kinds: slices.Clone(reg.kinds), hardStop: opts.hardStop}
	if q.hardStop <= 0 {
		q.hardStop = hardStopTimeout
	}
	retry := opts.retryPolicy
	if retry == nil {
		retry = backoff{first: firstRetry, max: maxRetry}
	}
	cfg := &river.Config{
		ID:          clientID(),
		Logger:      log,
		Schema:      schema,
		Workers:     reg.workers,
		RetryPolicy: retry,
		// River's reindexer rebuilds River's indexes with REINDEX, which only
		// their owner can run, and the server's login owns no table, so it
		// is given none. A schedule that never runs would switch it off too,
		// but River logs the time of its next run, which would be past the
		// year 9999 that the JSON log can encode.
		ReindexerIndexNames: []string{},
		// A cancelled job is in the dead-letter state too (Cancel), so it is
		// kept as long as a discarded one.
		CancelledJobRetentionPeriod: deadLetterRetention,
		DiscardedJobRetentionPeriod: deadLetterRetention,
		TestOnly:                    opts.testOnly,
	}
	pool := db.Pool()
	if q.runsJobs() {
		workers, err := database.OpenWith(ctx, app, func(c *pgxpool.Config) {
			c.MaxConns = poolSize
			c.MinConns = min(c.MinConns, poolSize)
			c.MinIdleConns = min(c.MinIdleConns, poolSize)
		})
		if err != nil {
			return nil, fmt.Errorf("open the job workers' pool: %w", err)
		}
		q.workers, pool = workers, workers.Pool()
		cfg.Queues = map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: concurrency}}
		cfg.PeriodicJobs = reg.periodic
		// The jobs' work receives the workers' pool through its context,
		// which Register's worker hands it in Job.DB.
		cfg.Middleware = []rivertype.Middleware{river.WorkerMiddlewareFunc(
			func(ctx context.Context, _ *rivertype.JobRow, doInner func(context.Context) error) error {
				return doInner(context.WithValue(ctx, workersDB{}, workers))
			})}
		if opts.pollInterval > 0 {
			cfg.FetchPollInterval = opts.pollInterval
			cfg.FetchCooldown = min(opts.pollInterval, river.FetchCooldownDefault)
		}
	}
	client, err := river.NewClient(riverpgxv5.New(pool), cfg)
	if err != nil {
		q.Close()
		return nil, fmt.Errorf("open the job queue: %w", err)
	}
	q.client = client
	// The count runs on the request pool, so that a scrape does not wait for
	// the workers' connections, which poolSize leaves none of for it.
	q.metrics = newMetrics(db.Pool(), q.kinds)
	return q, nil
}

// clientID returns the name of this process's River client, which River
// records in the jobs it attempts and elects the leader by: the host name and
// a random suffix. River's default, the host name and the time to the
// microsecond, is the same for two processes that start at once on a host, and
// two clients with one name would both take themselves for the leader.
func clientID() string {
	host, _ := os.Hostname()
	host = strings.ReplaceAll(cmp.Or(host, "unknown_host"), ".", "_")
	if len(host) > 60 {
		host = host[:60]
	}
	return host + "_" + strings.ToLower(rand.Text()[:10])
}

// runsJobs reports whether the queue runs jobs in this process: if it is to,
// and has kinds of job to run. River refuses to start without any.
func (q *Queue) runsJobs() bool {
	return q.work && len(q.kinds) > 0
}

// Enqueue enqueues a job with args in tx, the transaction of the change that
// needs the job, so that the job exists, and runs, only if tx commits. opts may
// be nil. Enqueue returns an error if args's kind has no worker.
func (q *Queue) Enqueue(ctx context.Context, tx pgx.Tx, args Args, opts *EnqueueOptions) (Enqueued, error) {
	res, err := q.client.InsertTx(ctx, tx, args, opts.river())
	if err != nil {
		return Enqueued{}, fmt.Errorf("enqueue a job of kind %s: %w", args.Kind(), err)
	}
	return Enqueued{ID: res.Job.ID, Duplicate: res.UniqueSkippedAsDuplicate}, nil
}

// Start starts the workers, if the queue runs jobs in this process, and logs
// whether it does. They run until Stop, whatever ctx does. Start does nothing
// once Stop has been called. It returns an error if the workers cannot start,
// as when the database does not answer within 10 seconds, or before ctx
// ends.
func (q *Queue) Start(ctx context.Context) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case q.started || q.stopped:
		return nil
	case !q.work:
		q.log.InfoContext(ctx, "the job workers are off in this process, so the instances that run them do its jobs")
		return nil
	case len(q.kinds) == 0:
		q.log.InfoContext(ctx, "the job workers do not start, because no kinds of job are registered")
		return nil
	}
	// River checks that the database answers before it starts, on the
	// context it runs under, which nothing cancels, so a database that does
	// not answer would hold Start, and Stop behind it, for as long as the
	// connection takes to fail. The check is made here first, on ctx, and
	// River's then has a connection that has just answered.
	pingCtx, cancelPing := context.WithTimeout(ctx, startTimeout)
	err := q.workers.Ping(pingCtx)
	cancelPing()
	if err != nil {
		return fmt.Errorf("start the job workers: the database does not answer: %w", err)
	}
	// The buffer holds the events of more jobs than River finishes in one
	// batch, so that the metrics miss none while they catch up.
	events, cancel := q.client.SubscribeConfig(&river.SubscribeConfig{
		ChanSize: eventBuffer,
		Kinds: []river.EventKind{river.EventKindJobCompleted, river.EventKindJobFailed,
			river.EventKindJobCancelled, river.EventKindJobSnoozed, river.EventKindJobInterrupted},
	})
	// River stops the workers when its context ends, but they stop when the
	// server does, which calls Stop.
	if err := q.client.Start(context.WithoutCancel(ctx)); err != nil {
		cancel()
		return fmt.Errorf("start the job workers: %w", err)
	}
	q.started = true
	q.eventsDone = make(chan struct{})
	go func() {
		defer close(q.eventsDone)
		q.metrics.observe(events)
	}()
	q.log.InfoContext(ctx, "the job workers started", "workers", concurrency, "kinds", q.kinds)
	return nil
}

// Stop stops the workers, if they run, and keeps Start from starting them
// afterwards. The workers take no more jobs, and the jobs they run have until
// ctx ends to finish. Then Stop cancels the jobs' contexts, and waits up to 5
// seconds more for them to return. A job whose work returns the context's
// error because of that is attempted again later, without counting the
// attempt (Register). Stop returns an error if jobs still have not returned.
// Their rows stay running until River's rescuer takes them for stuck, an hour
// later.
func (q *Queue) Stop(ctx context.Context) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stopped = true
	if !q.started {
		return nil
	}
	err := q.client.Stop(ctx)
	if err != nil {
		q.log.WarnContext(ctx, "the jobs in progress did not finish in time, so they are cancelled")
		hardCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), q.hardStop)
		defer cancel()
		err = q.client.StopAndCancel(hardCtx)
	}
	if err != nil {
		q.stuck = true
		return fmt.Errorf("stop the job workers: jobs did not return when they were cancelled: %w", err)
	}
	q.started = false
	// River closes the events' channel when it stops.
	<-q.eventsDone
	return nil
}

// Close closes the workers' pool. Call it after Stop. If jobs did not stop,
// Close does not wait for them to return their connections.
func (q *Queue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case q.workers == nil:
	case q.stuck:
		go q.workers.Close()
	default:
		q.workers.Close()
	}
}

// Collector returns the queue's metrics (PRD FR-ADMIN-05, NFR-OBS-02).
func (q *Queue) Collector() prometheus.Collector {
	return q.metrics
}
