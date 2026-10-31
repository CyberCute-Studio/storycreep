// Package httpserver is the server that "storycreep serve" runs (PRD
// FR-ADMIN-05, FR-ADMIN-06): it listens for HTTP, takes the database step of
// startup, which applies the pending migrations unless told not to
// (FR-ADMIN-04), and serves until its context ends.
//
// It listens before the database step, so that /healthz answers while the
// migrations run and /readyz answers 503 until the server can serve: until
// the database step is done, and, without auto-migration, until the pending
// migrations are applied by "storycreep migrate". Until then, the API answers
// not_ready too. If the database step fails, as when a migration fails, Run
// closes the listeners and returns the error, so the instance serves no
// traffic, and the database stays at the last migration that succeeded
// (FR-ADMIN-04 AC3).
//
// Before the server is ready, it takes the setup step (PRD FR-ADMIN-02):
// with the configuration's STORYCREEP_SETUP_ keys, it completes first-run
// setup unattended if it is pending, and without them, it logs the setup
// code while setup is pending (setupStep). A configuration that unattended
// setup refuses stops the server, as a failed database step does.
//
// Once the server is ready, it runs the job queue's workers (PRD FR-JOBS-01,
// package jobs), unless Options.InlineWorkers is off, so that other instances
// run them. Not being able to start them stops the server, as a failed
// database step does.
//
// The admin port, a second listener, serves /metrics, with or without a
// bearer token as the MetricsEndpoint capability says. Every route declares
// the capability it requires (NFR-SEC-02), and every response carries the
// security headers of NFR-SEC-09.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/search"
	"github.com/CyberCute-Studio/storycreep/server/internal/trash"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/version"
)

// Timeouts of the HTTP servers. A request's body has a time limit of its own
// (package api).
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 2 * time.Minute
	// shutdownTimeout is how long Run waits for the requests in progress
	// when it stops. It is shorter than the 30 seconds that Kubernetes waits
	// by default before it kills a container. Docker and Docker Compose wait
	// only 10 seconds by default, so a deployment with them sets a longer
	// grace period, such as a compose file's stop_grace_period. It is longer
	// than the 15 seconds that the orphan reconciliation of the assets waits
	// for a deletion that it sent (package asset), which sends none once the
	// server stops (jobs.Stopping), so that those deletions end before the
	// jobs are cancelled and the process exits.
	shutdownTimeout = 20 * time.Second
	// defaultPollInterval is how often a server without auto-migration
	// checks whether the pending migrations have been applied.
	defaultPollInterval = 5 * time.Second
)

// ErrNoAdminURL is returned by Run when auto-migration is on but
// STORYCREEP_DATABASE_ADMIN_URL, which applying migrations needs, is not set.
var ErrNoAdminURL = errors.New("STORYCREEP_DATABASE_ADMIN_URL is not set, and the server applies the pending " +
	"migrations at startup with it; set it, or start the server with --no-auto-migrate and apply the migrations " +
	"with storycreep migrate")

// Options configure Run.
type Options struct {
	// AutoMigrate makes the server apply the pending migrations at startup,
	// which is what serve does unless --no-auto-migrate is given.
	AutoMigrate bool
	// Log receives the server's log. Nil discards it.
	Log *slog.Logger
	// Listener, if set, is where the server listens, instead of
	// STORYCREEP_HTTP_ADDR. AdminListener is the same for the admin port,
	// and Run closes it unasked if the configuration has no metrics to
	// serve. Tests listen on ports of their own.
	Listener, AdminListener net.Listener
	// Migrations are the migrations of the database step, as
	// database.StartOptions.Migrations. Nil means the embedded ones.
	Migrations fs.FS
	// PollInterval is how often a server without auto-migration checks
	// whether the pending migrations have been applied. Zero means every 5
	// seconds.
	PollInterval time.Duration
	// InlineWorkers makes the server run the job queue's workers in its own
	// process once it is ready, which is what serve does unless
	// --no-inline-workers is given.
	InlineWorkers bool
	// Jobs holds the kinds of job that the server enqueues and runs. Nil
	// means the server's own: the session cleanup, the cleanup of the email
	// links' tokens, and the job that emails a link (package account), the
	// rate limit cleanup (package ratelimit), the rollup and the
	// reconciliation of usage (package usage), the purge of the projects in
	// the trash (package project), and the pruning of the command batches'
	// idempotency records (package commands), and the thumbnails of the
	// assets and the reconciliation of their blobs (package asset). The plan
	// items that add jobs register them in serverJobs.
	Jobs *jobs.Registry
}

// Run runs the server until ctx ends, and then stops it gracefully: it stops
// accepting connections and taking jobs, and waits up to 20 seconds for the
// requests and the jobs in progress, and then up to 5 seconds more for the
// jobs, once it has cancelled them (jobs.Queue.Stop). It returns an error if
// the server cannot listen, if the database step fails, which leaves the
// database as database.Start describes, or if the job workers cannot start.
// A ctx that ends during the database step stops the server without an
// error.
func Run(ctx context.Context, cfg *config.Config, opts Options) error {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if opts.AutoMigrate && !cfg.DatabaseAdminURL.IsSet() {
		closeListeners(opts.Listener, opts.AdminListener)
		return ErrNoAdminURL
	}
	s := newServer(cfg, log)
	// The store of the assets' files is closed last, once the requests and
	// the jobs that read it have stopped.
	defer func() { _ = s.assets.Storage().Close() }()

	ln, adminLn := opts.Listener, opts.AdminListener
	if s.admin == nil && adminLn != nil {
		_ = adminLn.Close()
		adminLn = nil
	}
	var err error
	if ln == nil {
		if ln, err = net.Listen("tcp", cfg.HTTPAddr); err != nil {
			closeListeners(adminLn)
			return fmt.Errorf("listen on STORYCREEP_HTTP_ADDR: %w", err)
		}
	}
	if s.admin != nil && adminLn == nil {
		if adminLn, err = net.Listen("tcp", cfg.AdminAddr); err != nil {
			closeListeners(ln)
			return fmt.Errorf("listen on STORYCREEP_ADMIN_ADDR: %w", err)
		}
	}

	errorLog := slog.NewLogLogger(log.Handler(), slog.LevelWarn)
	servers := []*http.Server{{
		Handler:           s.main,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          errorLog,
	}}
	listeners := []net.Listener{ln}
	if s.admin != nil {
		servers = append(servers, &http.Server{
			Handler:           s.admin,
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
			ErrorLog:          errorLog,
		})
		listeners = append(listeners, adminLn)
	}
	serveErr := make(chan error, len(servers))
	for i, srv := range servers {
		go func() {
			if err := srv.Serve(listeners[i]); !errors.Is(err, http.ErrServerClosed) {
				serveErr <- err
			}
		}()
	}

	info := version.Get()
	attrs := []any{"address", ln.Addr().String(), "version", info.Version, "commit", info.Commit}
	if adminLn != nil {
		attrs = append(attrs, "admin_address", adminLn.Addr().String())
	} else {
		attrs = append(attrs, "metrics", s.metricsOff)
	}
	log.InfoContext(ctx, "listening", attrs...)

	// stop stops the servers and the job workers together, so that each has
	// the whole time to finish its requests or jobs in progress. Once the
	// queue is stopped, its workers do not start (jobs.Queue.Start).
	var queue *jobs.Queue
	stop := func() {
		s.ready.setPhase(phaseStopping)
		log.InfoContext(ctx, "stopping")
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		var wg sync.WaitGroup
		for i, srv := range servers {
			wg.Go(func() {
				if err := srv.Shutdown(shutdownCtx); err != nil {
					log.WarnContext(ctx, "the requests in progress did not finish in time, so their connections are "+
						"closed", "address", listeners[i].Addr().String(), "error", err)
					_ = srv.Close()
				}
			})
		}
		if queue != nil {
			wg.Go(func() {
				if err := queue.Stop(shutdownCtx); err != nil {
					log.ErrorContext(ctx, "the job workers did not stop", "error", err)
				}
			})
		}
		wg.Wait()
	}

	db, err := database.Start(ctx, cfg, database.StartOptions{
		AutoMigrate: opts.AutoMigrate,
		Log:         log,
		Migrations:  opts.Migrations,
	})
	if err != nil {
		stop()
		if ctx.Err() != nil {
			log.InfoContext(ctx, "stopped before the database step finished", "error", err)
			return nil
		}
		log.ErrorContext(ctx, "the database step of startup failed, so the server does not serve", "error", err)
		return err
	}
	defer db.Close()
	s.ready.db.Store(db)

	// The queue enqueues jobs from the start, and its workers start once the
	// server is ready, when River's tables exist.
	registry := opts.Jobs
	if registry == nil {
		registry = s.serverJobs()
	}
	queue, err = jobs.Open(ctx, db, cfg.DatabaseURL, jobs.Options{
		Registry: registry,
		Work:     opts.InlineWorkers,
		Log:      log,
	})
	// From here on, a ctx that ends while the server starts stops it as it
	// does at any other time, and the log says so.
	if err != nil {
		stop()
		if ctx.Err() != nil {
			log.InfoContext(ctx, "stopped")
			return nil
		}
		log.ErrorContext(ctx, "the job queue could not open, so the server does not serve", "error", err)
		return err
	}
	defer queue.Close()
	s.queue.queue.Store(queue)
	s.metrics.registry.MustRegister(queue.Collector())
	workerErr, setupErr := make(chan error, 1), make(chan error, 1)

	var wg sync.WaitGroup
	pollCtx, stopPolling := context.WithCancel(ctx)
	defer wg.Wait()
	defer stopPolling()
	// With auto-migration, Start has checked that the database has every
	// migration, so only a server without it asks.
	var pending []int64
	if !opts.AutoMigrate {
		pending, err = db.Pending(ctx)
	}
	switch {
	case err != nil:
		stop()
		if ctx.Err() != nil {
			log.InfoContext(ctx, "stopped")
			return nil
		}
		log.ErrorContext(ctx, "the pending migrations could not be read, so the server does not serve", "error", err)
		return err
	case len(pending) == 0:
		if err := s.setupStep(ctx); err != nil {
			stop()
			if ctx.Err() != nil {
				log.InfoContext(ctx, "stopped")
				return nil
			}
			log.ErrorContext(ctx, "the setup step of startup failed, so the server does not serve", "error", err)
			return err
		}
		s.ready.setPhase(phaseReady)
		log.InfoContext(ctx, "ready")
		if err := queue.Start(ctx); err != nil {
			stop()
			if ctx.Err() != nil {
				log.InfoContext(ctx, "stopped")
				return nil
			}
			log.ErrorContext(ctx, "the job workers could not start, so the server stops", "error", err)
			return err
		}
	default:
		s.ready.setPhase(phaseMigrationsPending)
		interval := opts.PollInterval
		if interval <= 0 {
			interval = defaultPollInterval
		}
		wg.Go(func() {
			if !s.ready.waitForMigrations(pollCtx, db, interval) {
				return
			}
			if err := s.setupStep(pollCtx); err != nil {
				if pollCtx.Err() == nil {
					setupErr <- err
				}
				return
			}
			if s.ready.migrationsApplied(pollCtx) {
				if err := queue.Start(ctx); err != nil && ctx.Err() == nil {
					workerErr <- err
				}
			}
		})
	}

	select {
	case <-ctx.Done():
		stop()
		log.InfoContext(ctx, "stopped")
		return nil
	case err := <-serveErr:
		stop()
		return fmt.Errorf("serve HTTP: %w", err)
	case err := <-workerErr:
		stop()
		log.ErrorContext(ctx, "the job workers could not start, so the server stops", "error", err)
		return err
	case err := <-setupErr:
		stop()
		log.ErrorContext(ctx, "the setup step of startup failed, so the server stops", "error", err)
		return err
	}
}

// The messages of the setup step's records.
const (
	setupPendingMessage = "first-run setup is pending: complete it with this setup code, which POST " + setupPath +
		" asks for, or restart the server with the STORYCREEP_SETUP_ keys set"
	setupUnattendedMessage = "completed first-run setup unattended, with the STORYCREEP_SETUP_ keys"
	setupIgnoredMessage    = "first-run setup has completed, so the server ignores the STORYCREEP_SETUP_ keys; " +
		"remove them from the configuration, STORYCREEP_SETUP_OWNER_PASSWORD above all"
)

// setupStep is the setup step of startup (PRD FR-ADMIN-02), which the server
// takes once the database has every migration applied, before it is ready.
// With the configuration's STORYCREEP_SETUP_ keys, it completes first-run
// setup unattended, if setup is pending, and returns a *setup.ConfigError if
// setup refuses a value of the keys. Without them, it logs the setup code,
// which completing setup through the API asks for, while setup is pending,
// at every start, so that the operator finds it in the log of the latest.
// It writes that record as a warning whatever STORYCREEP_LOG_LEVEL is,
// because without the code, setup cannot complete through the API: a level
// of error, which drops the other warnings, would otherwise leave the
// operator nowhere to read it.
func (s *server) setupStep(ctx context.Context) error {
	if s.unattended != nil {
		done, err := s.setups.Unattended(ctx, s.unattended)
		switch {
		case err != nil:
			return err
		case done:
			s.log.InfoContext(ctx, setupUnattendedMessage)
		default:
			s.log.WarnContext(ctx, setupIgnoredMessage)
		}
		return nil
	}
	st, err := s.setups.Status(ctx)
	if err != nil || st.Completed {
		return err
	}
	code, err := s.setups.Code(ctx)
	if err != nil {
		return err
	}
	// The handler writes a record that it is given whatever its level, which
	// only the logger checks, as WarnContext would.
	r := slog.NewRecord(time.Now(), slog.LevelWarn, setupPendingMessage, 0)
	r.AddAttrs(slog.String("setup_code", code))
	// As the logger does, the server goes on if the log cannot be written.
	_ = s.log.Handler().Handle(ctx, r)
	return nil
}

// serverJobs returns the registry of the server's own jobs.
func (s *server) serverJobs() *jobs.Registry {
	r := jobs.NewRegistry()
	s.accounts.RegisterJobs(r)
	s.invites.RegisterJobs(r)
	s.limits.RegisterJobs(r)
	usage.RegisterJobs(r)
	project.RegisterJobs(r)
	trash.RegisterJobs(r)
	search.RegisterJobs(r)
	commands.RegisterJobs(r)
	s.assets.RegisterJobs(r)
	return r
}

func closeListeners(ls ...net.Listener) {
	for _, l := range ls {
		if l != nil {
			_ = l.Close()
		}
	}
}
