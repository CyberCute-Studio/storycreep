// Package api is the toolkit of the server's HTTP API (PRD §10.5, FR-API-01,
// FR-API-04): a router whose every route declares the capability it requires
// (NFR-SEC-02), huma operations whose bodies are decoded strictly and limited
// in size (FR-API-01 AC2, NFR-SEC-07), the error envelope and its registry of
// codes, keyset pagination, and entity tags.
//
// Errors reach the client only as the envelope
// {"error": {"code", "message", "details", "request_id"}}, whether a handler
// returns them, huma reports them, or no route matches. A handler that
// returns any other error is answered with internal_error, and the error is
// logged with the request ID instead.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/CyberCute-Studio/storycreep/server/internal/logging"
)

// DefaultMaxBodyBytes is the largest request body a route accepts unless it
// says otherwise (PRD NFR-SEC-07).
const DefaultMaxBodyBytes = 5 << 20

// defaultBodyReadTimeout is how long a route waits for its request body
// unless it says otherwise. huma's own default, 5 seconds, would cut off a
// body of a few megabytes on a slow connection.
const defaultBodyReadTimeout = time.Minute

func init() {
	// A field whose name differs from the schema's only in case is unknown,
	// and a required field written so is missing, rather than taken for the
	// schema's field (PRD FR-API-01 AC2).
	huma.ValidateStrictCasing = true
	// huma builds every error it answers with through these, so the API
	// answers with the envelope everywhere.
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		return fromHuma(context.Background(), nil, status, msg, errs)
	}
	huma.NewErrorWithContext = func(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
		if ctx == nil {
			return fromHuma(context.Background(), nil, status, msg, errs)
		}
		return fromHuma(ctx.Context(), ctx.Operation(), status, msg, errs)
	}
}

// Route is a route as chi writes it: a method and a path pattern.
type Route struct {
	Method, Pattern string
}

func (r Route) String() string {
	return r.Method + " " + r.Pattern
}

// Mux is a router whose routes each declare the capability they require. It
// answers a request that no route matches with not_found, and one whose
// method no route at the path takes with method_not_allowed, both in the
// envelope.
type Mux struct {
	router *chi.Mux
	api    huma.API
	routes map[Route]Capability
}

// MuxOptions configure NewMux.
type MuxOptions struct {
	// Log receives the errors of requests, such as a handler's error that
	// is answered with internal_error. Nil discards them.
	Log *slog.Logger
	// Middleware wraps every request, the first outermost, including the
	// requests that no route matches.
	Middleware []func(http.Handler) http.Handler
}

// NewMux returns a Mux without routes.
func NewMux(opts MuxOptions) *Mux {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	m := &Mux{router: chi.NewRouter(), routes: map[Route]Capability{}}
	m.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(withLogger(r.Context(), log)))
		})
	})
	m.router.Use(opts.Middleware...)
	// A HEAD request is answered as a GET without the body, as health
	// checkers expect.
	m.router.Use(middleware.GetHead)
	m.router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, NewError(CodeNotFound))
	})
	m.router.MethodNotAllowed(m.methodNotAllowed)

	config := huma.DefaultConfig("Storycreep", "v1")
	// The OpenAPI document and its routes come with plan item 7. Until then
	// huma serves nothing of its own, and adds no $schema to bodies.
	config.OpenAPIPath, config.DocsPath, config.SchemasPath = "", "", ""
	config.CreateHooks = nil
	config.Formats = map[string]huma.Format{"application/json": jsonFormat, "json": jsonFormat}
	config.DefaultFormat = "application/json"
	config.Transformers = []huma.Transformer{fillRequestID}
	m.api = humachi.New(m.router, config)
	m.api.UseMiddleware(replaceCacheControl)
	return m
}

// replaceCacheControl is huma middleware that makes a Cache-Control that an
// operation returns with an error, as NotModified does, replace the one that
// the router's middleware set before it, such as httpserver's no-store. huma
// adds an error's headers to those already set, and a cache reads two
// Cache-Control headers as one, in which no-store wins.
func replaceCacheControl(ctx huma.Context, next func(huma.Context)) {
	next(&cacheControlContext{wrapped: ctx})
}

// wrapped is huma.Context under a name that, embedded, does not clash with
// the interface's method Context.
type wrapped = huma.Context

// cacheControlContext is a huma.Context whose first Cache-Control appended
// replaces the one already set. Those appended after it add to it.
type cacheControlContext struct {
	wrapped
	replaced bool
}

func (c *cacheControlContext) AppendHeader(name, value string) {
	if !c.replaced && http.CanonicalHeaderKey(name) == "Cache-Control" {
		c.replaced = true
		c.SetHeader(name, value)
		return
	}
	c.wrapped.AppendHeader(name, value)
}

// Unwrap returns the context that c wraps, as humachi.Unwrap expects of a
// context that middleware wraps.
func (c *cacheControlContext) Unwrap() huma.Context {
	return c.wrapped
}

func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.router.ServeHTTP(w, r)
}

// declare records that the route requires c. It panics if c is not a
// declared capability or the route is declared already, which are mistakes
// in the code that registers it.
func (m *Mux) declare(method, pattern string, c Capability) {
	if !c.Known() {
		panic(fmt.Sprintf("api: %s %s declares the unknown capability %q", method, pattern, c))
	}
	route := Route{method, pattern}
	if _, ok := m.routes[route]; ok {
		panic(fmt.Sprintf("api: %s is registered twice", route))
	}
	m.routes[route] = c
}

// Handle registers a handler that is not a huma operation, such as one that
// writes Prometheus's text format, for the route, which requires c. It
// writes its own errors, with WriteError. It must limit what it reads of the
// request body itself.
func (m *Mux) Handle(c Capability, method, pattern string, h http.Handler) {
	m.declare(method, pattern, c)
	m.router.Method(method, pattern, h)
}

// Register registers a huma operation that requires c. Its request body is
// limited to DefaultMaxBodyBytes, unless op.MaxBodyBytes sets another limit,
// or a negative one for none, and must arrive within a minute, unless
// op.BodyReadTimeout says otherwise. An error that handler returns is
// answered in the envelope, as Error describes.
func Register[I, O any](m *Mux, c Capability, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	m.declare(op.Method, op.Path, c)
	if op.MaxBodyBytes == 0 {
		op.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if op.MaxBodyBytes > 0 {
		// huma rejects a body of exactly MaxBodyBytes bytes.
		op.MaxBodyBytes++
	}
	if op.BodyReadTimeout == 0 {
		op.BodyReadTimeout = defaultBodyReadTimeout
	}
	huma.Register(m.api, op, func(ctx context.Context, in *I) (*O, error) {
		out, err := handler(ctx, in)
		if err != nil {
			env := toEnvelope(ctx, err)
			// Headers that the handler added with huma.ErrorWithHeaders,
			// such as NotModified's ETag, stay with the answer.
			var he huma.HeadersError
			if errors.As(err, &he) {
				return nil, huma.ErrorWithHeaders(env, he.GetHeaders())
			}
			return nil, env
		}
		return out, nil
	})
}

// Routes returns every route registered, with the capability it requires.
func (m *Mux) Routes() map[Route]Capability {
	return maps.Clone(m.routes)
}

// Walk calls fn for every route that the router has, whether or not it was
// registered through the Mux, so that a test can find a route that declares
// no capability (PRD NFR-SEC-02).
func (m *Mux) Walk(fn func(Route) error) error {
	return chi.Walk(m.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		return fn(Route{method, route})
	})
}

// Methods are the methods that routes take, in the order of the Allow header
// that methodNotAllowed writes.
var Methods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	http.MethodOptions,
}

// methodNotAllowed answers a request whose path a route matches, but not its
// method, with the methods that the path takes in the Allow header (RFC 9110
// §15.5.6).
func (m *Mux) methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	// The path is the one chi routed by.
	path := r.URL.RawPath
	if path == "" {
		path = r.URL.Path
	}
	if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePath != "" {
		path = rctx.RoutePath
	}
	var allow []string
	for _, method := range Methods {
		// GetHead answers HEAD wherever GET is routed.
		match := method
		if method == http.MethodHead && !m.router.Match(chi.NewRouteContext(), method, path) {
			match = http.MethodGet
		}
		if m.router.Match(chi.NewRouteContext(), match, path) {
			allow = append(allow, method)
		}
	}
	if len(allow) > 0 {
		w.Header().Set("Allow", strings.Join(allow, ", "))
	}
	WriteError(w, r, NewError(CodeMethodNotAllowed))
}

// fillRequestID is a huma transformer that puts the request's ID into a copy
// of an envelope that huma writes.
func fillRequestID(ctx huma.Context, _ string, v any) (any, error) {
	if env, ok := v.(*ErrorEnvelope); ok {
		return env.withRequestID(logging.RequestID(ctx.Context())), nil
	}
	return v, nil
}
