// Package api is the toolkit of the server's HTTP API (PRD §10.5, FR-API-01,
// FR-API-04): a router whose every route declares the capability it requires
// (NFR-SEC-02), whether it takes an API token, and with which scope, or a
// session alone (FR-AUTH-04), and the rate limit that counts its requests
// (NFR-SEC-06), huma operations whose bodies are decoded strictly and limited
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
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
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
	// tokens are the routes that take an API token, each with the scope
	// that the token must hold (TokenScope), and sessionOnly those that
	// take a session alone (SessionOnly).
	tokens      map[Route]scope.Scope
	sessionOnly map[Route]bool
	// buckets are the routes that a rate limit counts, each with the bucket
	// that counts its requests (RateLimit).
	buckets map[Route]Bucket
	// pages are the patterns that HandlePage registered.
	pages map[string]bool
	// authenticate is MuxOptions.Authenticate, and limiter is
	// MuxOptions.RateLimit.
	authenticate Authenticator
	limiter      RateLimiter
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
	// RateLimit counts the requests of each operation in the bucket of its
	// route (PRD FR-API-04, NFR-SEC-06). Register panics on an operation
	// that a rate limit counts if it is nil, as every operation of the API
	// is (Unlimited).
	RateLimit RateLimiter
}

// Authenticator finds who makes a request of an operation whose capability
// needs an authenticated caller, before the operation reads the request's
// parameters or body, as Guard checks. token is the scope that the route
// takes an API token with (TokenScope), or "" for a route that is
// session-only (SessionOnly), which the Authenticator refuses a token on
// (PRD FR-AUTH-04). It returns
// the context that the operation runs with, which says who the caller is, or
// the error that the request is answered with, as Register answers a
// handler's error, such as unauthenticated for a request without
// credentials.
type Authenticator func(ctx huma.Context, token scope.Scope) (context.Context, error)

// RateLimiter counts the requests of the operations in the buckets of their
// routes (MuxOptions.RateLimit).
type RateLimiter interface {
	// Take counts a request of an operation whose route counts in b, once
	// the Authenticator, if the operation needs one, has found who makes
	// it, and the operation's own middleware, as its guards, has let it
	// through, before the operation reads its parameters or body. It sets
	// the RateLimit headers of the answer on ctx, or returns the error that
	// the request is answered with, as Register answers a handler's error,
	// such as rate_limited.
	Take(ctx huma.Context, b Bucket) error
	// Doc returns what the description of an operation whose route counts
	// in b, and which callers may make requests of, says of the limit, which
	// Register appends to it, so that every operation's description says
	// what its own limit is.
	Doc(b Bucket, callers Callers) string
}

// Callers are who may make the requests of a route, which a rate limit counts
// against their keys: a request of a public route is counted against its IP
// address alone, one of a session-only route also against its user, and one
// of a route that takes an API token also against the token.
type Callers int

// The callers of the routes.
const (
	// Anyone may make the requests of a public route.
	Anyone Callers = iota
	// Sessions may make those of a session-only route (SessionOnly).
	Sessions
	// Tokens, besides sessions, may make those of a route that takes an API
	// token (TokenScope).
	Tokens
)

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
	m := &Mux{router: chi.NewRouter(), routes: map[Route]Capability{}, tokens: map[Route]scope.Scope{},
		sessionOnly: map[Route]bool{}, buckets: map[Route]Bucket{}, pages: map[string]bool{},
		authenticate: opts.Authenticate, limiter: opts.RateLimit}
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

// The headers of a rate limit (PRD FR-API-04, NFR-SEC-06), which package
// httpserver sets, an operation's error replaces (replacedHeaders), and the
// error response documents (errorResponse): RateLimit-Limit, -Remaining, and
// -Reset, of draft-ietf-httpapi-ratelimit-headers, and Retry-After, which
// also answers not_ready.
const (
	RateLimitLimitHeader     = "RateLimit-Limit"
	RateLimitRemainingHeader = "RateLimit-Remaining"
	RateLimitResetHeader     = "RateLimit-Reset"
	RetryAfterHeader         = "Retry-After"
)

// WWWAuthenticateHeader names the scheme of the credentials that an answer
// of 401 unauthenticated wants (RFC 9110 §11.6.1), which package httpserver
// sets, as the challenge of the Bearer scheme (RFC 6750 §3), on a route that
// takes an API token, for a token that is not valid, and for one without the
// route's scope, and the error response documents (errorResponse).
const WWWAuthenticateHeader = "WWW-Authenticate"

// replacedHeaders are the headers that an operation's error replaces, rather
// than adds to (replaceHeaders), by their canonical names: Cache-Control,
// which a cache reads as one header, in which no-store wins, and the headers
// of a rate limit, which state one key's limit, and which the limit's
// middleware sets before the operation, and the operation's own limit may
// replace (package httpserver).
var replacedHeaders = map[string]bool{
	"Cache-Control": true,
	http.CanonicalHeaderKey(RateLimitLimitHeader):     true,
	http.CanonicalHeaderKey(RateLimitRemainingHeader): true,
	http.CanonicalHeaderKey(RateLimitResetHeader):     true,
	http.CanonicalHeaderKey(RetryAfterHeader):         true,
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
// Register, unless the handler sets another read deadline. No rate limit
// counts its requests, which only Register's operations count, so a route of
// the API, under /api/, is an operation. It panics if c needs an
// authenticated caller, whom only Register finds, and on a pattern under
// /api/, whose requests a rate limit must count.
func (m *Mux) Handle(c Capability, method, pattern string, h http.Handler) {
	switch {
	case c.Authenticated():
		panic(fmt.Sprintf("api: %s %s requires %s, which needs an authenticated caller; register it with Register",
			method, pattern, c))
	case IsAPIPath(pattern):
		panic(fmt.Sprintf("api: %s %s is a route of the API, which a rate limit counts; register it with Register",
			method, pattern))
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

// Option is an option of a route that Register registers.
type Option func(*routeOptions)

// routeOptions are what a route's Options set.
type routeOptions struct {
	token       scope.Scope
	sessionOnly bool
	bucket      Bucket
	unlimited   bool
}

// TokenScope says that a route takes an API token too, besides a session,
// and that the token must hold the scope s, or a scope that implies it (PRD
// FR-AUTH-04): the Mux's Authenticator is told s. Register panics on
// TokenScope for a capability that needs no authenticated caller, with a
// value that is not a scope, or beside RateLimit(BucketAuth).
func TokenScope(s scope.Scope) Option {
	return func(o *routeOptions) { o.token = s }
}

// SessionOnly says that a route takes a session alone, as the account and
// credential endpoints do, and that the Authenticator refuses an API token
// there, whatever its scopes, with 403 session_required (PRD FR-AUTH-04).
// A route whose capability needs an authenticated caller declares either
// TokenScope or SessionOnly, and Register panics on one that declares
// neither or both, so that no route takes a token, or refuses one, because
// its declaration left something out (FR-AUTH-04 AC6). It panics on
// SessionOnly for a capability that needs no authenticated caller.
func SessionOnly() Option {
	return func(o *routeOptions) { o.sessionOnly = true }
}

// Bucket is the rate limit that counts the requests of a route (PRD
// FR-API-04, NFR-SEC-06). A route's requests count in one bucket, which its
// declaration says (RateLimit), and the RateLimiter of the Mux knows how many
// requests each bucket allows.
type Bucket string

// The buckets of the routes. The WebSocket connects of NFR-SEC-06 have one of
// their own, which is no route's that Register registers.
const (
	// BucketAuth is signing in, registering, and the other requests about
	// credentials that FR-AUTH-05 limits most, among them listing and
	// revoking API tokens. No route that takes an API token counts in it
	// (Register), so that a token's requests, which count against its owner
	// too, cannot use up the owner's limit of the requests that find and
	// revoke the token, after which no limit counts them: the Authenticator
	// refuses a revoked token before the limit counts its request.
	BucketAuth Bucket = "auth"
	// BucketRead is a request that reads, which a GET or HEAD route counts
	// in unless it says otherwise.
	BucketRead Bucket = "read"
	// BucketWrite is a request that changes something, which a route of
	// every other method counts in unless it says otherwise.
	BucketWrite Bucket = "write"
	// BucketExport is a request for an export.
	BucketExport Bucket = "export"
)

// Known reports whether b is a bucket of the routes.
func (b Bucket) Known() bool {
	switch b {
	case BucketAuth, BucketRead, BucketWrite, BucketExport:
		return true
	}
	return false
}

// RateLimit says that a route's requests count in the bucket b, rather than
// in BucketRead, for a GET or HEAD route, or in BucketWrite, for any other,
// in which they count otherwise. Register panics on a value that is not a
// bucket, and on BucketAuth for a route that takes an API token
// (TokenScope).
func RateLimit(b Bucket) Option {
	return func(o *routeOptions) { o.bucket = b }
}

// Unlimited says that no rate limit counts a route's requests, for a route
// outside the API, under /api/, that answers without the database, as the
// health and readiness probes must while the database is being migrated:
// the rate limits count in the database (package ratelimit). Every route of
// the API counts in a bucket, so Register panics on Unlimited for a path
// under /api/, for a capability that needs credentials, and beside
// RateLimit.
func Unlimited() Option {
	return func(o *routeOptions) { o.unlimited = true }
}

// IsAPIPath reports whether path is the API's, under /api/, whose every
// route a rate limit counts, and which needs the database.
func IsAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
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
// returns, with the scope of the route's TokenScope, or none for a route
// that is SessionOnly, one of which opts must say. A route therefore cannot
// declare such a capability and forget to authenticate its caller, or to
// say whether it takes an API token. Register panics if the Mux has no
// Authenticator then.
//
// Unless opts say that the route is Unlimited, the Mux's RateLimiter counts
// each request in the route's bucket (RateLimit) after the operation's own
// middleware, the operation's description ends with what the RateLimiter's
// Doc says of the limit, and each of its responses in the OpenAPI document
// but the default documents the RateLimit headers. Register panics if the
// Mux has no RateLimiter then.
func Register[I, O any](m *Mux, c Capability, op huma.Operation, handler func(context.Context, *I) (*O, error),
	opts ...Option) {
	var o routeOptions
	for _, opt := range opts {
		opt(&o)
	}
	route := Route{op.Method, op.Path}
	switch {
	case o.token != "" && !c.Authenticated():
		panic(fmt.Sprintf("api: %s requires %s, which needs no authenticated caller, so it takes no API token",
			route, c))
	case o.token != "" && !o.token.Known():
		panic(fmt.Sprintf("api: %s takes an API token with the unknown scope %q", route, o.token))
	case o.sessionOnly && !c.Authenticated():
		panic(fmt.Sprintf("api: %s requires %s, which needs no authenticated caller, so it is not session-only",
			route, c))
	case o.sessionOnly && o.token != "":
		panic(fmt.Sprintf("api: %s is session-only and takes an API token", route))
	case c.Authenticated() && !o.sessionOnly && o.token == "":
		panic(fmt.Sprintf("api: %s requires %s, which needs an authenticated caller, and says neither the scope "+
			"that it takes an API token with (TokenScope) nor that it is session-only (SessionOnly)", route, c))
	case o.unlimited && o.bucket != "":
		panic(fmt.Sprintf("api: %s is unlimited and counts in the %s bucket", route, o.bucket))
	case o.unlimited && (IsAPIPath(op.Path) || c != Public):
		panic(fmt.Sprintf("api: %s requires %s, and only a public route outside /api/ may be unlimited", route, c))
	case o.bucket != "" && !o.bucket.Known():
		panic(fmt.Sprintf("api: %s counts in the unknown bucket %q", route, o.bucket))
	case o.token != "" && o.bucket == BucketAuth:
		panic(fmt.Sprintf("api: %s takes an API token and counts in the %s bucket, which no token's request may "+
			"count in, so that a token cannot use up its owner's limit of the requests that revoke it", route,
			BucketAuth))
	case !o.unlimited && m.limiter == nil:
		panic(fmt.Sprintf("api: %s counts in a rate limit, and the Mux has no RateLimiter", route))
	}
	if c.Authenticated() {
		if m.authenticate == nil {
			panic(fmt.Sprintf("api: %s requires %s, which needs an authenticated caller, and the Mux has no "+
				"Authenticator", route, c))
		}
		op.Middlewares = append(huma.Middlewares{authenticateWith(m.authenticate, o.token)}, op.Middlewares...)
	}
	bucket := o.bucket
	if bucket == "" && !o.unlimited {
		bucket = BucketWrite
		if op.Method == http.MethodGet || op.Method == http.MethodHead {
			bucket = BucketRead
		}
	}
	if bucket != "" {
		callers := Anyone
		switch {
		case o.token != "":
			callers = Tokens
		case o.sessionOnly:
			callers = Sessions
		}
		// A new slice, so that the limit is not appended to the backing
		// array of middleware that another operation shares.
		op.Middlewares = append(slices.Clip(op.Middlewares), limitWith(m.limiter, bucket))
		op.Description = strings.TrimSpace(op.Description + m.limiter.Doc(bucket, callers))
	}
	m.declare(op.Method, op.Path, c)
	switch {
	case o.token != "":
		m.tokens[route] = o.token
	case o.sessionOnly:
		m.sessionOnly[route] = true
	}
	if bucket != "" {
		m.buckets[route] = bucket
	}
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
	if bucket != "" {
		m.documentRateLimit(route)
	}
}

// limitWith returns middleware for an operation that counts each request in
// bucket b with limiter, or answers the request with its error, as Guard
// does.
func limitWith(limiter RateLimiter, b Bucket) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if err := limiter.Take(ctx, b); err != nil {
			r, w := humachi.Unwrap(ctx)
			WriteError(w, r, err)
			return
		}
		next(ctx)
	}
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
// context that authenticate returns, given the scope that the route takes an
// API token with, or answers the request with its error, as Guard does.
func authenticateWith(authenticate Authenticator, token scope.Scope) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		authenticated, err := authenticate(ctx, token)
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

// TokenScopes returns the routes that take an API token, each with the scope
// that the token must hold (TokenScope).
func (m *Mux) TokenScopes() map[Route]scope.Scope {
	return maps.Clone(m.tokens)
}

// SessionOnlyRoutes returns the routes that take a session alone
// (SessionOnly). Every route whose capability needs an authenticated caller
// is one of them or one of TokenScopes.
func (m *Mux) SessionOnlyRoutes() map[Route]bool {
	return maps.Clone(m.sessionOnly)
}

// Buckets returns the routes that a rate limit counts, each with the bucket
// that counts its requests. Every route that Register registers is one of
// them, but those that are Unlimited.
func (m *Mux) Buckets() map[Route]Bucket {
	return maps.Clone(m.buckets)
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
