package httpserver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/apitoken"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// Sessions on the wire (PRD FR-AUTH-02, NFR-SEC-04). Signing in sets a
// cookie that holds the session's secret, HttpOnly, so that no script reads
// it, and SameSite=Lax, so that a browser does not send it with a request
// that another site makes, other than following a link. Its name and whether
// it is Secure follow STORYCREEP_PUBLIC_URL, not the scheme of the request,
// which a proxy that terminates TLS forwards as plain HTTP: with an https
// URL it is __Host-sc_session, Secure, for the whole host and nothing else,
// and with an http URL, which only self-hosted mode allows, sc_session,
// which a browser then sends over plain HTTP.
//
// A request made with the cookie that changes something, with a method other
// than GET, HEAD, or OPTIONS, also needs the session's CSRF token in its
// X-CSRF-Token header (AC5). The token is an HMAC of the session's secret,
// which a page of another site can neither read nor compute: signing in and
// GET /api/v1/auth/session return it, and only to the page of the session.
// The server keeps nothing for it, and it changes when the session's secret
// does. A browser also tells the server where a request comes from, in its
// Sec-Fetch-Site header, and the server refuses every such request that
// comes from another site, or from another origin of the same site, signing
// in included (refuseCrossSite). Browsers
// send that header only to an https URL or to a loopback address such as
// localhost, so with a plain-HTTP public URL of another host, such as a LAN
// address, the CSRF token alone protects the requests made with a session,
// and nothing stops another site from signing a browser in.

// The names of the session cookie: with an https public URL, and with an
// http one.
const (
	secureCookieName = "__Host-sc_session"
	plainCookieName  = "sc_session"
)

// csrfHeader is the header of a request's CSRF token.
const csrfHeader = "X-CSRF-Token"

// csrfLabel is what the CSRF token is the HMAC of, with the session's
// secret as the key, so that the token is no other value derived from the
// secret, such as the hash that the database holds.
const csrfLabel = "storycreep session csrf token"

// sessionCookie is the session cookie of the public URL.
type sessionCookie struct {
	name   string
	secure bool
}

func newSessionCookie(u config.PublicURL) sessionCookie {
	if u.HTTPS() {
		return sessionCookie{name: secureCookieName, secure: true}
	}
	return sessionCookie{name: plainCookieName}
}

// set returns the Set-Cookie header that gives the client the session's
// secret, until the session ends at the latest.
func (c sessionCookie) set(issued account.Issued) string {
	return c.cookie(issued.Secret.Reveal(), issued.EndsAt).String()
}

// clear returns the Set-Cookie header that removes the cookie.
func (c sessionCookie) clear() string {
	return c.cookie("", time.Time{}).String()
}

// cookie returns the cookie with value, which expires at expires, or is
// removed if expires is zero.
func (c sessionCookie) cookie(value string, expires time.Time) *http.Cookie {
	ck := &http.Cookie{Name: c.name, Value: value, Path: "/", Secure: c.secure, HttpOnly: true,
		SameSite: http.SameSiteLaxMode}
	if expires.IsZero() {
		ck.MaxAge = -1
		return ck
	}
	ck.Expires = expires.UTC()
	ck.MaxAge = max(1, int(time.Until(expires)/time.Second))
	return ck
}

// read returns the secret in r's cookie, and false if it has none.
func (c sessionCookie) read(r *http.Request) (secret.Value, bool) {
	ck, err := r.Cookie(c.name)
	if err != nil || ck.Value == "" {
		return secret.Value{}, false
	}
	return secret.New(ck.Value), true
}

// csrfToken returns the CSRF token of the session whose secret is s.
func csrfToken(s secret.Value) string {
	mac := hmac.New(sha256.New, []byte(s.Reveal()))
	mac.Write([]byte(csrfLabel))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// validCSRF reports whether token is the CSRF token of the session whose
// secret is s, in a time that does not depend on how much of it matches.
func validCSRF(token string, s secret.Value) bool {
	return hmac.Equal([]byte(token), []byte(csrfToken(s)))
}

// safeMethod reports whether a request with method changes nothing, so that
// it needs no CSRF token.
func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// refuseCrossSite answers with csrf_failed a request that changes something,
// as safeMethod tells, and that a browser says comes from another site, or
// from another origin of the same site, in its Sec-Fetch-Site header,
// whatever credentials it has. The tool's own pages make their requests from
// its origin, and nothing else that runs in a browser has a reason to change
// anything, so this stops another site from making a browser sign in to an
// account of its choosing, which no session's CSRF token protects, as well as
// from acting with a session. A request without the header goes on, and the
// CSRF token of its session protects it: one from a program, from an older
// browser, or to a plain-HTTP URL whose host is not a loopback address, to
// which browsers do not send the header.
func refuseCrossSite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if site := r.Header.Get("Sec-Fetch-Site"); !safeMethod(r.Method) && (site == "cross-site" ||
			site == "same-site") {
			api.WriteError(w, r, api.NewError(api.CodeCSRFFailed))
			return
		}
		next.ServeHTTP(w, r)
	})
}

type currentKey struct{}

// current is who makes a request: the user, and the session that they make
// it with, and its secret, or else the API token that they make it with. A
// route that takes no token (api.TokenScope) always has the session, because
// the authenticator refuses a token there.
type current struct {
	userID  ids.UUID
	session account.Session
	secret  secret.Value
	// token is the API token of a request made with one, and what it
	// restricts the user to, or nil for a request made with a session.
	token *scope.Token
}

// actor returns the user of the request as the actor of the commands that it
// applies, and of the reads that a token is held to.
func (c current) actor(ctx context.Context) commands.Actor {
	return commands.Actor{UserID: c.userID, Token: c.token, Client: clientFrom(ctx)}
}

// currentFrom returns who makes a request, from its context, which the
// authenticator put there, or unauthenticated if it has none, which only a
// route that does not need an authenticated caller could have.
func currentFrom(ctx context.Context) (current, error) {
	c, ok := ctx.Value(currentKey{}).(current)
	if !ok {
		return current{}, api.NewError(api.CodeUnauthenticated)
	}
	return c, nil
}

// authenticator returns the Authenticator of the API (api.MuxOptions). A
// request whose Authorization header holds an API token (bearerToken) is
// made with the token, whatever cookie it has, and tokens checks it against
// the scope that the route takes a token with, or refuses it on a route that
// takes none (PRD FR-AUTH-04). It needs no CSRF token, because no browser
// sends an Authorization header that a page of another site chose. Any
// other request is made with its session: the authenticator finds the
// session that its cookie holds the secret of, and, for a request that
// changes something, checks its CSRF token first, so that a request without
// one is refused before the database is asked. A request without the
// cookie, or with one of a session that has ended, is answered with
// unauthenticated, and the answer clears the cookie in the second case. On a
// route that takes a token, every answer of unauthenticated and of
// insufficient_scope carries the challenge of the Bearer scheme that RFC 6750
// §3 gives it, so that a client of the API learns what a token needs there.
func authenticator(accounts *account.Service, tokens *apitoken.Service, cookie sessionCookie) api.Authenticator {
	return func(hctx huma.Context, needed scope.Scope) (context.Context, error) {
		r, _ := humachi.Unwrap(hctx)
		ctx := hctx.Context()
		if value, ok := bearerToken(r); ok {
			cred, err := tokens.Authenticate(ctx, value, needed, clientFrom(ctx))
			switch {
			case errors.Is(err, apitoken.ErrInvalid):
				return nil, unauthenticated(needed, `error="invalid_token"`, nil)
			case errors.Is(err, apitoken.ErrSessionRequired):
				return nil, api.NewError(api.CodeSessionRequired)
			case errors.Is(err, apitoken.ErrInsufficientScope):
				return nil, insufficientScope(needed)
			case err != nil:
				return nil, err
			}
			return context.WithValue(ctx, currentKey{}, current{userID: cred.UserID, token: &cred.Token}), nil
		}
		value, ok := cookie.read(r)
		if !ok {
			return nil, unauthenticated(needed, "", nil)
		}
		if !safeMethod(r.Method) && !validCSRF(r.Header.Get(csrfHeader), value) {
			return nil, api.NewError(api.CodeCSRFFailed)
		}
		session, err := accounts.Session(ctx, value)
		if errors.Is(err, account.ErrNoSession) {
			return nil, signedOut(needed, cookie)
		}
		if err != nil {
			return nil, err
		}
		return context.WithValue(ctx, currentKey{}, current{userID: session.UserID, session: session,
			secret: value}), nil
	}
}

// bearerToken returns the API token that r's Authorization header holds, as
// the credential of the Bearer scheme (RFC 6750 §2.1), and false if it holds
// none. A credential that does not start with the prefix of a token's secret
// is no API token, and is left alone, so that a proxy in front of the server
// that authenticates its users with a header of its own, as basic
// authentication, keeps working with the session cookie.
func bearerToken(r *http.Request) (secret.Value, bool) {
	credential, ok := bearerCredential(r)
	if !ok || !strings.HasPrefix(credential, apitoken.Prefix) {
		return secret.Value{}, false
	}
	return secret.New(credential), true
}

// bearerChallenge returns the challenge of the Bearer scheme (RFC 6750 §3)
// that an answer of the API carries in its WWW-Authenticate header, with the
// attributes attrs after the realm, if there are any.
func bearerChallenge(attrs string) string {
	if attrs == "" {
		return `Bearer realm="storycreep"`
	}
	return `Bearer realm="storycreep", ` + attrs
}

// unauthenticated returns unauthenticated, with the headers h, if any, and,
// on a route that takes an API token (needed is not ""), the challenge of the
// Bearer scheme, with the attributes attrs (bearerChallenge): RFC 6750 §3
// gives one to the answer to a request without a token as to one whose
// token is not valid (error="invalid_token"). A session-only route asks for
// no token, because it takes none, but answers one that is not valid with
// error="invalid_token" all the same.
func unauthenticated(needed scope.Scope, attrs string, h http.Header) error {
	if needed != "" || attrs != "" {
		if h == nil {
			h = http.Header{}
		}
		h.Set(api.WWWAuthenticateHeader, bearerChallenge(attrs))
	}
	if h == nil {
		return api.NewError(api.CodeUnauthenticated)
	}
	return huma.ErrorWithHeaders(api.NewError(api.CodeUnauthenticated), h)
}

// insufficientScope returns insufficient_scope, for a request made with an
// API token that does not hold needed, with the challenge of the Bearer
// scheme that RFC 6750 §3.1 gives it, which names the scope too.
func insufficientScope(needed scope.Scope) error {
	return huma.ErrorWithHeaders(&api.Error{Code: api.CodeInsufficientScope, Details: map[string]any{"scope": needed}},
		http.Header{api.WWWAuthenticateHeader: {bearerChallenge(`error="insufficient_scope", scope="` +
			string(needed) + `"`)}})
}

// signedOut returns unauthenticated, with the header that clears the
// cookie of a session that has ended, on a route that takes an API token
// that holds needed, or "" for one that takes none, as unauthenticated does.
func signedOut(needed scope.Scope, cookie sessionCookie) error {
	return unauthenticated(needed, "", http.Header{"Set-Cookie": {cookie.clear()}})
}

// sessionBody is a session, as the API shows it.
type sessionBody struct {
	ID            string    `json:"id" doc:"The session's ID"`
	CreatedAt     time.Time `json:"created_at" doc:"When its user signed in"`
	LastSeenAt    time.Time `json:"last_seen_at" doc:"When it was last used, to the minute"`
	ExpiresAt     time.Time `json:"expires_at" doc:"When it ends at the latest, however often it is used: STORYCREEP_SESSION_LIFETIME after its user signed in, or the lifetime in force when its user signed in, if that was shorter"`
	IdleExpiresAt time.Time `json:"idle_expires_at" doc:"When it ends unless it is used before: STORYCREEP_SESSION_IDLE_TIMEOUT after its last use, or at expires_at if that is sooner"`
	IP            string    `json:"ip,omitempty" doc:"The IP address that its user signed in from: the address that the connection came from, or, if that is one of STORYCREEP_TRUSTED_PROXIES, the client's address that the proxies forwarded"`
	UserAgent     string    `json:"user_agent,omitempty" doc:"The User-Agent header of the client that its user signed in with"`
	Current       bool      `json:"current" doc:"Whether it is the session of the request"`
}

// newSessionBody returns s as the API shows it to a request made with the
// session currentID.
func newSessionBody(s account.Session, currentID ids.UUID) sessionBody {
	b := sessionBody{
		ID:            ids.Encode(ids.Session, s.ID),
		CreatedAt:     s.CreatedAt,
		LastSeenAt:    s.LastSeenAt,
		ExpiresAt:     s.EndsAt,
		IdleExpiresAt: s.IdleEndsAt,
		UserAgent:     s.UserAgent,
		Current:       s.ID == currentID,
	}
	if s.IP.IsValid() {
		b.IP = s.IP.String()
	}
	return b
}

// signedInBody is the session that a request is made with, or that signing
// in started.
type signedInBody struct {
	UserID    string      `json:"user_id" doc:"The ID of the account that is signed in"`
	Session   sessionBody `json:"session" doc:"The session"`
	CSRFToken string      `json:"csrf_token" doc:"The session's CSRF token: every request made with the session that changes something, with a method other than GET, HEAD, or OPTIONS, sends it in its X-CSRF-Token header, or is answered with 403 csrf_failed. It stays the same for as long as the session does, until changing the password gives the session a new one."`
}

func newSignedInBody(s account.Session, value secret.Value) signedInBody {
	return signedInBody{UserID: ids.Encode(ids.User, s.UserID), Session: newSessionBody(s, s.ID),
		CSRFToken: csrfToken(value)}
}

// signedInOutput answers a request that starts a session or gives it a new
// secret, which are rate-limited.
type signedInOutput struct {
	SetCookie string `header:"Set-Cookie" doc:"The session cookie, which holds the session's secret: __Host-sc_session, Secure, when STORYCREEP_PUBLIC_URL is https, and sc_session otherwise, in either case HttpOnly, SameSite=Lax, for the path /, and expiring when the session ends at the latest"`
	Body      signedInBody
}

type sessionOutput struct {
	Body signedInBody
}

// clearedOutput is an answer of 204 that may clear the session cookie.
type clearedOutput struct {
	SetCookie string `header:"Set-Cookie" doc:"Clears the session cookie, when the session of the request has ended"`
}

// CSRFInput is the CSRF token of a request that changes something. Its
// name is exported so that huma reads it where it is embedded.
type CSRFInput struct {
	CSRFToken string `header:"X-CSRF-Token" required:"true" doc:"The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed"`
}

// loginBody is the body of POST /api/v1/auth/login.
type loginBody struct {
	Email    string `json:"email" maxLength:"254" doc:"The account's email address, in any case"`
	Password string `json:"password" maxLength:"256" doc:"The account's password"`
}

type loginInput struct {
	Body loginBody
}

type sessionsInput struct {
	api.PageParams
}

// idKey is the key that a list in the order of its items' IDs is paged by,
// as the lists of sessions and of workspaces are.
type idKey struct {
	ID string `json:"id"`
}

// cursorID returns the ID that cursor, a next_cursor of a list paged by
// idKey, holds, or the zero UUID for the empty cursor of the first page. A
// cursor that holds no ID, or not in the form that the server writes, is
// answered with invalid_cursor.
func cursorID(cursor string) (ids.UUID, error) {
	if cursor == "" {
		return ids.UUID{}, nil
	}
	key, err := api.DecodeCursor[idKey](cursor)
	if err != nil {
		return ids.UUID{}, err
	}
	id, err := ids.ParseUUID(key.ID)
	if err != nil || id.String() != key.ID {
		return ids.UUID{}, api.NewError(api.CodeInvalidCursor)
	}
	return id, nil
}

type sessionsOutput struct {
	Body api.Page[sessionBody]
}

type revokeSessionInput struct {
	CSRFInput
	SessionID string `path:"session_id" doc:"The session's ID"`
}

// revokedBody is the body of DELETE /api/v1/auth/sessions.
type revokedBody struct {
	Revoked int `json:"revoked" doc:"How many sessions were revoked"`
}

type revokedOutput struct {
	Body revokedBody
}

// passwordBody is the body of POST /api/v1/auth/password.
type passwordBody struct {
	CurrentPassword string `json:"current_password" maxLength:"256" doc:"The account's password"`
	NewPassword     string `json:"new_password" maxLength:"256" doc:"The new password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password"`
}

type passwordInput struct {
	CSRFInput
	Body passwordBody
}

// sessionAuthDoc says, in the description of an operation that is
// session-only (api.SessionOnly), what the operation needs of a request.
const sessionAuthDoc = " It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the " +
	"answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is " +
	"answered with 403 session_required (PRD FR-AUTH-04)."

// csrfDoc says what an operation that is session-only and changes something
// needs of a request.
const csrfDoc = sessionAuthDoc + " It changes something, so it also needs the session's CSRF token in the " +
	"X-CSRF-Token header, or the answer is 403 csrf_failed."

// registerSessions registers the routes of signing in and of the sessions
// (PRD FR-AUTH-02).
func registerSessions(m *api.Mux, accounts *account.Service, cookie sessionCookie) {
	api.Register(m, api.Public, huma.Operation{
		OperationID: "login",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/login",
		Summary:     "Sign in",
		Description: "Signs in with an account's email address and password, and starts a session, whose secret " +
			"the Set-Cookie header gives the client, and whose CSRF token the body holds. The account's first " +
			"sign-in also gives it its personal workspace, named after its display name, which it owns; " +
			"GET /api/v1/workspaces lists it. Where the instance always sends email, as hosted mode does, that is " +
			"the first sign-in once the account's email address is verified, or the verification itself. An " +
			"address of no account, a wrong password, and an account that cannot sign in, such as a suspended one, " +
			"are all answered with 401 invalid_credentials, after the same work, so that the answer does not tell " +
			"them apart. A request that a browser's Sec-Fetch-Site header says comes from another site, or from " +
			"another origin of the same site, is answered with 403 csrf_failed; browsers send that header only to " +
			"https URLs and to loopback addresses, such as localhost. After 10 failed sign-ins in a row with an " +
			"email address, whether or not it belongs to an account, the address is locked for a minute, and each " +
			"further failure, once a lock ends, locks it for twice as long as the lock before, up to 15 minutes: " +
			"while it is locked, every sign-in with it is answered with 429 rate_limited, whose details.reason is " +
			"locked, even with the right password. A successful sign-in forgets the failures, and so do 24 hours " +
			"without one.",
		Tags: []string{"Authentication"},
	}, func(ctx context.Context, in *loginInput) (*signedInOutput, error) {
		issued, err := accounts.Login(ctx, in.Body.Email, secret.New(in.Body.Password), clientFrom(ctx))
		if errors.Is(err, account.ErrInvalidCredentials) {
			return nil, api.NewError(api.CodeInvalidCredentials)
		}
		if err != nil {
			return nil, limitError(err)
		}
		return &signedInOutput{SetCookie: cookie.set(issued), Body: newSignedInBody(issued.Session, issued.Secret)}, nil
	}, api.RateLimit(api.BucketAuth))

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "getSession",
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/session",
		Summary:     "Get the session of the request",
		Description: "Returns the signed-in account's ID, the session of the request, and its CSRF token, which a " +
			"page that the browser loads again reads here." + sessionAuthDoc,
		Tags: []string{"Authentication"},
	}, func(ctx context.Context, _ *struct{}) (*sessionOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		return &sessionOutput{Body: newSignedInBody(cur.session, cur.secret)}, nil
	}, api.SessionOnly())

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "logout",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/logout",
		Summary:     "Sign out",
		Description: "Ends the session of the request: deletes it, so that its secret is no longer valid on any " +
			"server, and clears its cookie." + csrfDoc,
		Tags:          []string{"Authentication"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *CSRFInput) (*clearedOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		if err := accounts.Logout(ctx, cur.session, clientFrom(ctx)); err != nil {
			return nil, err
		}
		return &clearedOutput{SetCookie: cookie.clear()}, nil
	}, api.RateLimit(api.BucketAuth), api.SessionOnly())

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "listSessions",
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/sessions",
		Summary:     "List the account's sessions",
		Description: "Lists the signed-in account's active sessions, newest first, each with the IP address and " +
			"user agent that it started with, and when it was last used." + sessionAuthDoc,
		Tags: []string{"Authentication"},
	}, func(ctx context.Context, in *sessionsInput) (*sessionsOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		before, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		sessions, err := accounts.Sessions(ctx, cur.session.UserID, before, in.Fetch())
		if err != nil {
			return nil, err
		}
		page, err := api.NewPage(in.PageParams, sessions, func(s account.Session) idKey {
			return idKey{ID: s.ID.String()}
		})
		if err != nil {
			return nil, err
		}
		out := &sessionsOutput{Body: api.Page[sessionBody]{Items: make([]sessionBody, len(page.Items)),
			NextCursor: page.NextCursor}}
		for i, s := range page.Items {
			out.Body.Items[i] = newSessionBody(s, cur.session.ID)
		}
		return out, nil
	}, api.RateLimit(api.BucketAuth), api.SessionOnly())

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "revokeSession",
		Method:      http.MethodDelete,
		Path:        "/api/v1/auth/sessions/{session_id}",
		Summary:     "Revoke a session",
		Description: "Revokes one of the signed-in account's active sessions, which is then no longer valid on " +
			"any server. Revoking the session of the request signs it out, and the answer clears its cookie. A " +
			"session that is not an active session of the account is answered with 404 not_found." + csrfDoc,
		Tags:          []string{"Authentication"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *revokeSessionInput) (*clearedOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Session, in.SessionID)
		if err != nil {
			return nil, err
		}
		err = accounts.RevokeSession(ctx, cur.session.UserID, id, clientFrom(ctx))
		if errors.Is(err, account.ErrSessionNotFound) {
			return nil, api.NewError(api.CodeNotFound)
		}
		if err != nil {
			return nil, err
		}
		out := &clearedOutput{}
		if id == cur.session.ID {
			out.SetCookie = cookie.clear()
		}
		return out, nil
	}, api.RateLimit(api.BucketAuth), api.SessionOnly())

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "revokeOtherSessions",
		Method:      http.MethodDelete,
		Path:        "/api/v1/auth/sessions",
		Summary:     "Revoke the account's other sessions",
		Description: "Revokes every active session of the signed-in account but the session of the request, which " +
			"signs the account out everywhere else. POST /api/v1/auth/logout ends the session of the request." +
			csrfDoc,
		Tags: []string{"Authentication"},
	}, func(ctx context.Context, _ *CSRFInput) (*revokedOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		n, err := accounts.RevokeOtherSessions(ctx, cur.session, clientFrom(ctx))
		switch {
		case errors.Is(err, account.ErrNoSession):
			// The request has no session to keep, as one made without a
			// session cookie.
			return nil, api.NewError(api.CodeUnauthenticated)
		case err != nil:
			return nil, err
		}
		return &revokedOutput{Body: revokedBody{Revoked: n}}, nil
	}, api.RateLimit(api.BucketAuth), api.SessionOnly())

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "changePassword",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/password",
		Summary:     "Change the account's password",
		Description: "Changes the signed-in account's password, if current_password is the account's, or the " +
			"answer is 403 wrong_password. The new password must have at least 12 characters, counted as Unicode " +
			"code points once it is in NFKC, and must not be one of the 10,000 most common passwords, or the " +
			"answer is 422 weak_password. Changing it revokes every other session of the account, stops the " +
			"account's links that reset its password from working, and gives the session of the request a new " +
			"secret, in the Set-Cookie header, and a new CSRF token, in the body, so that the old ones are no " +
			"longer valid." + csrfDoc,
		Tags: []string{"Authentication"},
	}, func(ctx context.Context, in *passwordInput) (*signedInOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		issued, err := accounts.ChangePassword(ctx, cur.session, secret.New(in.Body.CurrentPassword),
			secret.New(in.Body.NewPassword), clientFrom(ctx))
		var weak *account.PasswordError
		switch {
		case errors.Is(err, account.ErrWrongPassword):
			return nil, &api.Error{Code: api.CodeWrongPassword, Details: map[string]any{
				"errors": []api.Problem{{Location: "body.current_password", Message: "is not the account's password"}},
			}}
		case errors.Is(err, account.ErrNoSession):
			// The route takes no token.
			return nil, signedOut("", cookie)
		case errors.As(err, &weak):
			return nil, weakPasswordError(weak, "body.new_password")
		case err != nil:
			return nil, err
		}
		return &signedInOutput{SetCookie: cookie.set(issued), Body: newSignedInBody(issued.Session, issued.Secret)}, nil
	}, api.RateLimit(api.BucketAuth), api.SessionOnly())
}
