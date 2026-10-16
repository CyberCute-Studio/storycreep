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
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
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
	// pages are the patterns that HandlePage registered.
	pages map[string]bool
	// authenticate is MuxOptions.Authenticate.
	authenticate Authenticator
}

// MuxOptions configure NewMux.
type MuxOptions struct {
	// Log receives the errors of requests, such as a handler's error that
	// is answered with internal_error. Nil discards them.
	Log *slog.Logger
	// Middleware wraps every request, the first outermost, including the
	// requests that no route matches.
	Middleware []func(http.Handler) http.Handler
	// Locales are the catalogs that error envelopes' messages come from,
	// i18n.Default() if nil, and DefaultLocale is the instance's default
	// locale, which a request whose Accept-Language matches no catalog gets
	// (PRD FR-I18N-02).
	Locales       *i18n.Bundle
	DefaultLocale language.Tag
	// Authenticate finds the caller of each operation whose capability
	// needs an authenticated caller (Capability.Authenticated). Register
	// panics on such an operation if it is nil.
	Authenticate Authenticator
}

// Authenticator finds who makes a request of an operation whose capability
// needs an authenticated caller, before the operation reads the request's
// parameters or body, as Guard checks. It returns the context that the
// operation runs with, which says who the caller is, or the error that the
// request is answered with, as Register answers a handler's error, such as
// unauthenticated for a request without credentials.
type Authenticator func(ctx huma.Context) (context.Context, error)

// NewMux returns a Mux without routes.
func NewMux(opts MuxOptions) *Mux {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	locales := opts.Locales
	if locales == nil {
		locales = i18n.Default()
	}
	m := &Mux{router: chi.NewRouter(), routes: map[Route]Capability{}, pages: map[string]bool{},
		authenticate: opts.Authenticate}
	m.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(withLogger(r.Context(), log)))
		})
	})
	m.router.Use(negotiateLocale(locales, opts.DefaultLocale))
	m.router.Use(limitBodyRead(defaultBodyReadTimeout))
	m.router.Use(opts.Middleware...)
	// A HEAD request is answered as a GET without the body, as health
	// checkers expect.
	m.router.Use(middleware.GetHead)
	m.router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, NewError(CodeNotFound))
	})
	m.router.MethodNotAllowed(m.methodNotAllowed)

	config := huma.DefaultConfig("Storycreep", "v1")
	// huma serves no routes of its own, which would declare no capability,
	// and adds no $schema to bodies. The server serves the OpenAPI document
	// as an operation of its own (package httpserver), and Document renders
	// it.
	config.OpenAPIPath, config.DocsPath, config.SchemasPath = "", "", ""
	config.CreateHooks = nil
	config.Formats = map[string]huma.Format{"application/json": jsonFormat, "json": jsonFormat}
	config.DefaultFormat = "application/json"
	config.Transformers = []huma.Transformer{fillEnvelope}
	m.api = humachi.New(m.router, config)
	m.api.UseMiddleware(replaceHeaders)
	return m
}

// limitBodyRead gives a request that has a body d to send it, from when its
// handler starts. A route that does not read the body, such as /healthz or a
// path that no route matches, still waits for it, because net/http drains it
// before it answers, so without a deadline a client that sends a body slowly
// would hold the connection forever: the server's own timeouts end only at
// the headers. An operation that reads its body sets its own deadline in
// place of this one (Register), and net/http clears it once the body has
// been read. A request without a body gets none, as it needs none.
func limitBodyRead(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody {
				// A ResponseWriter without a connection, as in a test, takes
				// no deadline, and needs none.
				_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(d))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// replacedHeaders are the headers that an operation's error replaces, rather
// than adds to (replaceHeaders): Cache-Control, which a cache reads as one
// header, in which no-store wins, and the headers of a rate limit, which
// state one key's limit, and which the limit's middleware sets before the
// operation, and the operation's own limit may replace (package httpserver).
var replacedHeaders = map[string]bool{
	"Cache-Control":       true,
	"Ratelimit-Limit":     true,
	"Ratelimit-Remaining": true,
	"Ratelimit-Reset":     true,
	"Retry-After":         true,
}

// replaceHeaders is huma middleware that makes a header of replacedHeaders
// that an operation returns with an error, as NotModified returns
// Cache-Control, replace the one that middleware set before it, such as
// httpserver's Cache-Control: no-store. huma adds an error's headers to those
// already set.
func replaceHeaders(ctx huma.Context, next func(huma.Context)) {
	next(&replacingContext{wrapped: ctx})
}

// wrapped is huma.Context under a name that, embedded, does not clash with
// the interface's method Context.
type wrapped = huma.Context

// replacingContext is a huma.Context whose first value appended of each of
// replacedHeaders replaces the one already set. Those appended after it add
// to it.
type replacingContext struct {
	wrapped
	// replaced are the headers whose first value has been appended.
	replaced map[string]bool
}

func (c *replacingContext) AppendHeader(name, value string) {
	if name = http.CanonicalHeaderKey(name); replacedHeaders[name] && !c.replaced[name] {
		if c.replaced == nil {
			c.replaced = map[string]bool{}
		}
		c.replaced[name] = true
		c.SetHeader(name, value)
		return
	}
	c.wrapped.AppendHeader(name, value)
}

// Unwrap returns the context that c wraps, as humachi.Unwrap expects of a
// context that middleware wraps.
func (c *replacingContext) Unwrap() huma.Context {
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
// writes its own errors, with WriteError. It must limit how much it reads of
// the request body itself; the body must arrive within a minute, as for
// Register, unless the handler sets another read deadline. It panics if c
// needs an authenticated caller, whom only Register finds.
func (m *Mux) Handle(c Capability, method, pattern string, h http.Handler) {
	if c.Authenticated() {
		panic(fmt.Sprintf("api: %s %s requires %s, which needs an authenticated caller; register it with Register",
			method, pattern, c))
	}
	m.declare(method, pattern, c)
	m.router.Method(method, pattern, h)
}

// HandlePage registers h for GET requests to pattern, which serve a page of
// the web interface, or a file of one, rather than an operation of the API.
// A page requires no credentials, because it holds no user's data: the
// interface fetches that from the API. The OpenAPI document does not
// describe pages, and IsPage tells which routes they are. h writes its own
// errors, with WriteError.
func (m *Mux) HandlePage(pattern string, h http.Handler) {
	m.Handle(Public, http.MethodGet, pattern, h)
	m.pages[pattern] = true
}

// IsPage reports whether r is the route of a page, which HandlePage
// registered.
func (m *Mux) IsPage(r Route) bool {
	return r.Method == http.MethodGet && m.pages[r.Pattern]
}

// Register registers a huma operation that requires c. Its request body is
// limited to DefaultMaxBodyBytes, unless op.MaxBodyBytes sets another limit,
// or a negative one for none, and must arrive within a minute, unless
// op.BodyReadTimeout says otherwise. An error that handler returns is
// answered in the envelope, as Error describes, and the operation's default
// response in the OpenAPI document is the envelope, unless op sets one.
//
// If c needs an authenticated caller, the Mux's Authenticator runs before
// the operation's own middleware, and handler runs with the context that it
// returns. A route therefore cannot declare such a capability and forget to
// authenticate its caller. Register panics if the Mux has no Authenticator
// then.
func Register[I, O any](m *Mux, c Capability, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	if c.Authenticated() {
		if m.authenticate == nil {
			panic(fmt.Sprintf("api: %s %s requires %s, which needs an authenticated caller, and the Mux has no "+
				"Authenticator", op.Method, op.Path, c))
		}
		op.Middlewares = append(huma.Middlewares{authenticateWith(m.authenticate)}, op.Middlewares...)
	}
	m.declare(op.Method, op.Path, c)
	// huma documents the envelope as the default response only for an
	// operation that documents no other response than its success, so an
	// operation that does, such as one that answers 304 too, would have its
	// errors undocumented (PRD FR-API-04).
	if op.Responses == nil {
		op.Responses = map[string]*huma.Response{}
	}
	if op.Responses["default"] == nil {
		op.Responses["default"] = m.errorResponse()
	}
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

// Guard returns middleware for an operation, for its huma.Operation's
// Middlewares, that answers a request with the error that check returns, as
// Register answers a handler's error, before the operation reads the
// request's parameters or body, so that it refuses the request whatever they
// hold. If check returns nil, the operation goes on.
func Guard(check func(context.Context) error) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if err := check(ctx.Context()); err != nil {
			r, w := humachi.Unwrap(ctx)
			WriteError(w, r, err)
			return
		}
		next(ctx)
	}
}

// authenticateWith returns middleware for an operation that runs it with the
// context that authenticate returns, or answers the request with its error,
// as Guard does.
func authenticateWith(authenticate Authenticator) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		authenticated, err := authenticate(ctx)
		if err != nil {
			r, w := humachi.Unwrap(ctx)
			WriteError(w, r, err)
			return
		}
		next(huma.WithContext(ctx, authenticated))
	}
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

// routingPath returns the path that chi routes r by.
func routingPath(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePath != "" {
		return rctx.RoutePath
	}
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}
	if r.URL.Path == "" {
		return "/"
	}
	return r.URL.Path
}

// RoutePattern returns the pattern of the route that r was routed to, such as
// /api/v1/projects/{id}, or "" if no route matches it. A request that
// middleware answered before chi routed it, as the readiness gate of package
// httpserver does, gets the pattern of the route that would have answered it,
// so that it is logged and counted under its route.
func RoutePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return ""
	}
	if pattern := rctx.RoutePattern(); pattern != "" || rctx.Routes == nil {
		return pattern
	}
	path := routingPath(r)
	pattern := rctx.Routes.Find(chi.NewRouteContext(), r.Method, path)
	if pattern == "" && r.Method == http.MethodHead {
		// GetHead answers HEAD wherever GET is routed.
		pattern = rctx.Routes.Find(chi.NewRouteContext(), http.MethodGet, path)
	}
	return pattern
}

// methodNotAllowed answers a request whose path a route matches, but not its
// method, with the methods that the path takes in the Allow header (RFC 9110
// §15.5.6). chi also sends here a request with a method that it does not
// route at all, such as PROPFIND, whatever its path, so a path that no route
// matches is answered with not_found, as for any other method.
func (m *Mux) methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	path := routingPath(r)
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
	if len(allow) == 0 {
		WriteError(w, r, NewError(CodeNotFound))
		return
	}
	w.Header().Set("Allow", strings.Join(allow, ", "))
	WriteError(w, r, NewError(CodeMethodNotAllowed))
}

// fillEnvelope is a huma transformer that fills in a copy of an envelope that
// huma writes, as forRequest does, with the request's ID and the message in
// the request's locale, and sets Content-Language and Vary. huma calls it
// before it writes the headers.
func fillEnvelope(ctx huma.Context, _ string, v any) (any, error) {
	env, ok := v.(*ErrorEnvelope)
	if !ok || env.status == http.StatusNotModified {
		return v, nil
	}
	env, locale := env.forRequest(ctx.Context())
	ctx.SetHeader("Content-Language", locale.String())
	ctx.AppendHeader("Vary", "Accept-Language")
	return env, nil
}

// negotiateLocale returns middleware that gives each request's context the
// localizer of the locale that its Accept-Language and the instance's
// default negotiate in locales (PRD FR-I18N-02), which error envelopes are
// written in.
func negotiateLocale(locales *i18n.Bundle, instanceDefault language.Tag) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// A header given on several lines is one list (RFC 9110 §5.3).
			accept := strings.Join(r.Header.Values("Accept-Language"), ",")
			l := locales.Localizer(locales.Negotiate("", accept, instanceDefault))
			next.ServeHTTP(w, r.WithContext(i18n.WithLocalizer(r.Context(), l)))
		})
	}
}
