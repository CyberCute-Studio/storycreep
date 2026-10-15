package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
)

// phase is where the server is in its life.
type phase int32

const (
	// phaseStarting is the database step of startup.
	phaseStarting phase = iota
	// phaseMigrationsPending means that the server started without
	// auto-migration, and the database has migrations to apply.
	phaseMigrationsPending
	// phaseReady means that the server serves.
	phaseReady
	// phaseStopping means that the server is shutting down.
	phaseStopping
)

// pingTimeout is how long /readyz waits for the database to answer.
const pingTimeout = 2 * time.Second

// retryAfter is the Retry-After of a not_ready answer, in seconds.
const retryAfter = "5"

// readiness says whether the server can serve (PRD FR-ADMIN-05): /readyz
// reports it, and the API refuses requests until it can.
type readiness struct {
	log   *slog.Logger
	phase atomic.Int32
	// db is the database, once the database step is done.
	db atomic.Pointer[database.DB]
	// unreachable is set while the database does not answer /readyz, so that
	// only the changes are logged.
	unreachable atomic.Bool
}

func (r *readiness) current() phase {
	return phase(r.phase.Load())
}

func (r *readiness) setPhase(p phase) {
	r.phase.Store(int32(p))
}

// notReady returns the error that answers a request while the server cannot
// serve, or nil if it can. It checks that the database answers only if
// pingDB is set, as for /readyz; the API's requests find out for themselves.
func (r *readiness) notReady(ctx context.Context, pingDB bool) error {
	var reason, msg string
	switch r.current() {
	case phaseStarting:
		reason, msg = "starting", "The server is starting."
	case phaseMigrationsPending:
		reason, msg = "migrations_pending", "The database has pending migrations, which storycreep migrate applies."
	case phaseStopping:
		reason, msg = "stopping", "The server is stopping."
	case phaseReady:
		if !pingDB {
			return nil
		}
		ctx, cancel := context.WithTimeout(ctx, pingTimeout)
		defer cancel()
		if err := r.db.Load().Ping(ctx); err != nil {
			if !r.unreachable.Swap(true) {
				r.log.WarnContext(ctx, "the database does not answer, so the server reports that it is not ready",
					"error", err)
			}
			reason, msg = "database_unreachable", "The server cannot reach its database."
			break
		}
		if r.unreachable.Swap(false) {
			r.log.InfoContext(ctx, "the database answers again, so the server reports that it is ready")
		}
		return nil
	}
	return huma.ErrorWithHeaders(
		&api.Error{Code: api.CodeNotReady, Message: msg, Details: map[string]any{"reason": reason}},
		http.Header{"Retry-After": {retryAfter}})
}

// waitForMigrations checks every interval whether the database has applied
// the pending migrations, and makes the server ready once it has. It returns
// then, or when ctx ends.
func (r *readiness) waitForMigrations(ctx context.Context, db *database.DB, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	var lastErr string
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pending, err := db.Pending(ctx)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			// One record per kind of failure, not one per check.
			if msg := err.Error(); msg != lastErr {
				lastErr = msg
				r.log.WarnContext(ctx, "the server could not check whether the pending migrations have been "+
					"applied, and tries again", "error", err)
			}
		case len(pending) == 0:
			if r.phase.CompareAndSwap(int32(phaseMigrationsPending), int32(phaseReady)) {
				r.log.InfoContext(ctx, "ready, now that the pending migrations have been applied")
			}
			return
		}
	}
}

// isAPI reports whether path is the API's, which needs the database.
func isAPI(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// gate answers the API's requests with not_ready, and its Retry-After, until
// the server can serve.
func (r *readiness) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if isAPI(req.URL.Path) {
			if err := r.notReady(req.Context(), false); err != nil {
				api.WriteError(w, req, err)
				return
			}
		}
		next.ServeHTTP(w, req)
	})
}
