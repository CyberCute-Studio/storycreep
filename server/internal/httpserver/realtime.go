package httpserver

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"golang.org/x/net/http/httpguts"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/apitoken"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/realtime"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// The real-time channel on the wire (PRD FR-RT-01, FR-RT-02, plan note N59):
// GET /api/v1/ws upgrades to a WebSocket, authenticated as any other
// request of the API is, by the session cookie or an API token that holds
// project:read, and counted in the rate limit of connects, before it
// upgrades, and the hub (package realtime) serves it from then on. A socket
// opened with the session cookie must come from a page of the tool's own
// origin, as its Origin header says, because a browser sends the cookie with
// a page of another site's WebSocket too, which would read the user's
// projects otherwise; one opened with a token needs no such check, because
// no page sends an Authorization header with a WebSocket.

// realtimePath is where the real-time channel is.
const realtimePath = "/api/v1/ws"

// connectionsRetryAfter is what the answer to a user who has
// realtime.MaxConnectionsPerUser sockets open tells them to wait, in seconds:
// as long as a socket takes to find that a tab that held it has gone, by a
// heartbeat that it is not answered.
const connectionsRetryAfter = "60"

// registerRealtime registers GET /api/v1/ws, which the hub serves.
func registerRealtime(m *api.Mux, s *server, public config.PublicURL) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID:   "openRealtimeSocket",
		Method:        http.MethodGet,
		Path:          realtimePath,
		DefaultStatus: http.StatusSwitchingProtocols,
		Summary:       "Open the real-time channel",
		Description: "Upgrades to a WebSocket (RFC 6455), over which the client subscribes to the projects that it " +
			"reads, and is sent each batch of commands that commits to them, from every server of the instance, " +
			"in the order of their seqs (PRD FR-RT-01, FR-RT-02). The operation log is the source of truth, and the " +
			"socket only a way to be told of it sooner: a client that connects again subscribes with the seq of " +
			"the latest operation that it has, and is sent those that it missed, or told to reload the project " +
			"(resync_required) when the log no longer has them, or more than 10,000 follow. The messages are JSON, " +
			"each in a text message of its own, as schema/realtime/v1/messages.schema.json describes them: a " +
			"client sends {\"type\": \"subscribe\", \"project_id\", \"since\"} and {\"type\": \"unsubscribe\", " +
			"\"project_id\"}, at most 4 KiB each; a subscription is answered with subscribed, resync_required, or " +
			"subscription_failed, whose code is not_found for a project that does not exist and one that the user " +
			"cannot read alike, and an unsubscription with unsubscribed. The client is sent operation messages, " +
			"unsubscribed once the project can no longer be read, and a heartbeat every 25 seconds, beside a " +
			"ping, which a client that hears nothing for a minute takes for a lost connection. A socket " +
			"subscribes to 100 projects at most, each subscription counts in the " +
			"rate limit of reads, and a user has 50 sockets open to a server at most, past which the upgrade is " +
			"answered with 429 rate_limited, whose details.reason is connections. The socket's session or token, " +
			"and the user's access to each project, are checked again before each page of operations that it is " +
			"sent; once the session ends, or the token no longer holds, the socket closes with 4001. It closes " +
			"with 1001 as the server stops, and 1011 when the server cannot read the database, after either of " +
			"which a client connects again, with exponential backoff and jitter, and subscribes with the seqs " +
			"that it has. A request that does not upgrade is answered with 426 upgrade_required. A socket opened " +
			"with the session cookie whose Origin header names neither STORYCREEP_PUBLIC_URL's origin nor the " +
			"request's host by that URL's scheme is refused with 403 csrf_failed." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Real time"},
		Responses: map[string]*huma.Response{
			"101": {Description: "Switching Protocols: the connection is a WebSocket from now on."},
		},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			r, w := humachi.Unwrap(hctx)
			if err := upgradeError(r); err != nil {
				// An Upgrade header goes with the connection option upgrade,
				// so that no proxy forwards it (RFC 9110 §7.8).
				w.Header().Set("Upgrade", "websocket")
				w.Header().Set("Connection", "Upgrade")
				w.Header().Set("Sec-WebSocket-Version", "13")
				api.WriteError(w, r, err)
				return
			}
			if cur.token == nil && !sameOrigin(r, public) {
				api.WriteError(w, r, api.NewError(api.CodeCSRFFailed))
				return
			}
			release, err := s.hub.Admit(cur.userID)
			if err != nil {
				api.WriteError(w, r, admitError(err))
				return
			}
			defer release()
			// The checks above are those that Accept makes, and its check of
			// the origin, sameOrigin's, which only a socket of the session
			// cookie needs.
			ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				// Accept has answered the request.
				return
			}
			s.hub.Serve(ctx, ws, s.socketClient(ctx, cur, r))
		}}, nil
	}, api.TokenScope(scope.ProjectRead), api.RateLimit(api.BucketConnect))
}

// upgradeError returns upgrade_required for a request that does not upgrade
// to a WebSocket of version 13 (RFC 6455 §4.2.1), or nil for one that does.
func upgradeError(r *http.Request) error {
	key, err := base64.StdEncoding.DecodeString(r.Header.Get("Sec-WebSocket-Key"))
	if r.Method != http.MethodGet || !httpguts.HeaderValuesContainsToken(r.Header.Values("Connection"), "upgrade") ||
		!httpguts.HeaderValuesContainsToken(r.Header.Values("Upgrade"), "websocket") ||
		r.Header.Get("Sec-WebSocket-Version") != "13" || err != nil || len(key) != 16 {
		return api.NewError(api.CodeUpgradeRequired)
	}
	return nil
}

// sameOrigin reports whether r, a socket's upgrade, comes from a page of the
// tool, or from no page at all: its Origin header, which a browser sends
// with every WebSocket, is missing, or names the origin of the public URL,
// or the host that r was made to, as one that a proxy forwards whose public
// URL another name of the host is, by the public URL's scheme, which r's
// is not known by behind a proxy that ends TLS. A page of the host by
// another scheme is another origin than the tool's, as one of plain HTTP
// that a network injects, whose socket a browser that takes it for the same
// site sends the cookie of the tool's HTTPS with.
func sameOrigin(r *http.Request, public config.PublicURL) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Scheme+"://"+u.Host, public.String()) ||
		strings.EqualFold(u.Scheme+"://"+u.Host, public.URL().Scheme+"://"+r.Host)
}

// admitError returns the error that the API answers a refusal of the hub's
// Admit with: 429 rate_limited, whose details.reason is connections, for a
// user who has too many sockets open, and 503 not_ready once the hub has
// shut down, as the server stops.
func admitError(err error) error {
	switch {
	case errors.Is(err, realtime.ErrTooManyConnections):
		return huma.ErrorWithHeaders(&api.Error{Code: api.CodeRateLimited,
			Details: map[string]any{"reason": "connections", "bucket": api.BucketConnect}},
			http.Header{api.RetryAfterHeader: {connectionsRetryAfter}})
	case errors.Is(err, realtime.ErrClosed):
		return huma.ErrorWithHeaders(&api.Error{Code: api.CodeNotReady,
			Message: i18n.Message{Key: i18n.K("error.not_ready.stopping")}, Details: map[string]any{"reason": "stopping"}},
			http.Header{api.RetryAfterHeader: {retryAfter}})
	}
	return err
}

// socketClient returns the client of a socket that cur opened with r: its
// Check checks r's credential again as the authenticator checked it, the
// session of its cookie, or its API token, with project:read, whose use it
// records as a request's, and its Limit counts a subscription in the read
// bucket of the rate limits, as a read of the API counts, against the
// client's IP address, the user, and the token.
func (s *server) socketClient(ctx context.Context, cur current, r *http.Request) realtime.Client {
	client := clientFrom(ctx)
	token, _ := bearerToken(r)
	return realtime.Client{
		UserID: cur.userID,
		Check: func(ctx context.Context) (commands.Actor, error) {
			if cur.token != nil {
				cred, err := s.tokens.Authenticate(ctx, token, scope.ProjectRead, client)
				switch {
				case errors.Is(err, apitoken.ErrInvalid), errors.Is(err, apitoken.ErrInsufficientScope),
					errors.Is(err, apitoken.ErrSessionRequired):
					return commands.Actor{}, realtime.ErrUnauthenticated
				case err != nil:
					return commands.Actor{}, err
				}
				return commands.Actor{UserID: cred.UserID, Token: &cred.Token, Client: client}, nil
			}
			session, err := s.accounts.Session(ctx, cur.secret)
			switch {
			case errors.Is(err, account.ErrNoSession):
				return commands.Actor{}, realtime.ErrUnauthenticated
			case err != nil:
				return commands.Actor{}, err
			}
			return commands.Actor{UserID: session.UserID, Client: client}, nil
		},
		Limit: func(ctx context.Context, actor commands.Actor) error {
			keys := []ratelimit.Key{ratelimit.ClientIP(client.IP), ratelimit.UserID(actor.UserID)}
			if actor.Token != nil {
				keys = append(keys, ratelimit.TokenID(actor.Token.ID))
			}
			_, err := s.limits.Take(ctx, ratelimit.Read, keys...)
			var limited *ratelimit.LimitError
			if errors.As(err, &limited) {
				return &realtime.LimitedError{RetryAfter: limited.RetryAfter()}
			}
			return err
		},
	}
}
