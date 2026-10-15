package httpserver

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/logging"
)

// requestIDHeader carries a request's ID, in the request from a proxy that
// assigned one, and in every response.
const requestIDHeader = "X-Request-ID"

// maxRequestIDLength bounds the request IDs taken from a request.
const maxRequestIDLength = 128

// requestID gives each request an ID (PRD FR-ADMIN-05): the one in its
// X-Request-ID header, if it has one of at most 128 letters, digits, and
// -_.: characters, and otherwise a new UUIDv7. A proxy in front of the
// server sets the header so that its log and the server's share the ID, but
// the server cannot tell a proxy's header from a client's, so the ID is only
// a label to find the request's records by, never proof of where the request
// came from or that no other request has it. The ID is in the response's
// X-Request-ID header, in every record logged about the request, and in its
// error envelope.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if !validRequestID(id) {
			id = ids.New().String()
		}
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(logging.WithRequestID(r.Context(), id)))
	})
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLength {
		return false
	}
	for _, c := range []byte(id) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.:", c) >= 0) {
			return false
		}
	}
	return true
}

// quietRoutes are the routes that probes and scrapers request every few
// seconds. Their requests are logged at debug level, so that they do not
// drown the others.
var quietRoutes = map[string]bool{"/healthz": true, "/readyz": true, "/metrics": true}

// observe logs each request when it ends (PRD NFR-OBS-01), with its method,
// route, path, status, size, and duration, and counts it in m, if m is set.
// The path is logged without its query, which may hold what a user searched
// for. A request whose response was aborted with a panic, as recoverPanics
// does once the status is sent, is logged and counted too, with the status
// that was sent, or 0 if none was, and the log says that it was aborted.
func (s *server) observe(m *metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				aborted := recover()
				s.logRequest(m, r, ww, time.Since(start), aborted != nil)
				if aborted != nil {
					// net/http ends the connection, which tells the client
					// that the response is broken.
					panic(aborted)
				}
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

// logRequest logs and counts a request that observe watched.
func (s *server) logRequest(m *metrics, r *http.Request, ww middleware.WrapResponseWriter, elapsed time.Duration,
	aborted bool) {
	status := ww.Status()
	if status == 0 && !aborted {
		// The handler wrote nothing, which net/http answers with 200.
		status = http.StatusOK
	}
	route := ""
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		route = rctx.RoutePattern()
	}
	if m != nil {
		m.observe(r.Method, route, status, elapsed)
	}
	level := slog.LevelInfo
	switch {
	case aborted:
		level = slog.LevelWarn
	case quietRoutes[route]:
		level = slog.LevelDebug
	}
	attrs := []slog.Attr{
		slog.String("method", r.Method),
		slog.String("route", route),
		slog.String("path", r.URL.Path),
		slog.Int("status", status),
		slog.Int("bytes", ww.BytesWritten()),
		slog.Duration("duration", elapsed),
	}
	if aborted {
		attrs = append(attrs, slog.Bool("aborted", true))
	}
	s.log.LogAttrs(r.Context(), level, "request", attrs...)
}

// recoverPanics answers a request whose handler panicked with internal_error,
// and logs the panic with its stack, so that one bad request does not end
// the server or go unexplained.
func (s *server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				// net/http's way to abort a response, which it handles.
				panic(v)
			}
			s.log.ErrorContext(r.Context(), "a handler panicked", "panic", v, "stack", string(debug.Stack()))
			if ww, ok := w.(middleware.WrapResponseWriter); ok && ww.Status() != 0 {
				// The status is sent, so the answer cannot change; ending the
				// connection tells the client that the response is broken.
				panic(http.ErrAbortHandler)
			}
			api.WriteError(w, r, api.NewError(api.CodeInternal))
		}()
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy allows the page's own scripts, styles, images, and
// connections, and no inline script, plugin, or framing (PRD NFR-SEC-09).
// The tool's bundle (plan item 53) adds nonces where an inline script is
// unavoidable.
const contentSecurityPolicy = "default-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; " +
	"object-src 'none'"

// hstsPolicy keeps browsers on HTTPS for a year, on the public URL's host
// name only, without asking to be preloaded. It leaves out includeSubDomains,
// because a self-hosted instance's host may have subdomains that are other
// services, without HTTPS, which browsers would refuse to load for a year.
const hstsPolicy = "max-age=31536000"

// securityHeaders sets the security headers of PRD NFR-SEC-09 on every
// response, and Strict-Transport-Security if hsts is set, which it is when
// the public URL is https, as it always is in hosted mode (NFR-SEC-15).
// Responses are not stored by caches unless their handler says otherwise,
// because most of them hold a user's data.
func securityHeaders(hsts bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", contentSecurityPolicy)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Cache-Control", "no-store")
			if hsts {
				h.Set("Strict-Transport-Security", hstsPolicy)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// noindexApp keeps search engines from indexing the tool: every response
// under /app carries X-Robots-Tag: noindex (PRD FR-PUB-02 AC4).
func noindexApp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; p == "/app" || strings.HasPrefix(p, "/app/") {
			w.Header().Set("X-Robots-Tag", "noindex")
		}
		next.ServeHTTP(w, r)
	})
}
