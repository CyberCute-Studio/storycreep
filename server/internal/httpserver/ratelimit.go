package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
)

// Rate limits on the wire (PRD FR-API-04, NFR-SEC-06). An operation that is
// limited counts each request in its bucket (package ratelimit) for the
// client's IP address, and for its signed-in user, once the authenticator has
// found one, and for the API token that it is made with, if it is, before it
// reads the request's body. Its answers to the requests
// that it counts, but not to those that the router's middleware, the
// authenticator, or a guard refused before, say how much of the limit is
// left in the RateLimit headers of draft-ietf-httpapi-ratelimit-headers:
// RateLimit-Limit, how many requests the key may make in a window,
// RateLimit-Remaining, how many more, and RateLimit-Reset, how many seconds
// until the window ends, of the key with the fewest requests left. A request
// over the limit is answered with 429 rate_limited, and Retry-After says how
// many seconds to wait (FR-API-04 AC3).

// The headers of a rate limit, which package api names, so that an
// operation's error replaces those that rateLimit set before it.
const (
	limitHeader     = api.RateLimitLimitHeader
	remainingHeader = api.RateLimitRemainingHeader
	resetHeader     = api.RateLimitResetHeader
)

// RateLimitOutput documents, in an operation's answer, the headers that its
// rate limit sets (rateLimit). The middleware sets them itself, so the
// fields stay nil, and huma writes no header for a nil field. Its name is
// exported so that huma reads it where it is embedded.
type RateLimitOutput struct {
	RateLimitLimit     *int `header:"RateLimit-Limit" nullable:"false" doc:"How many requests of the operation's kind the client may make in a window of time, for its IP address or its account, whichever has the fewest left"`
	RateLimitRemaining *int `header:"RateLimit-Remaining" nullable:"false" doc:"How many more it may make in the current window"`
	RateLimitReset     *int `header:"RateLimit-Reset" nullable:"false" doc:"How many seconds until the current window ends, and the limit is whole again"`
}

// authLimitDoc says, in the description of an operation that the auth bucket
// limits, what its rate limit does.
const authLimitDoc = " Signing in, registering, changing a password, completing first-run setup, asking for a link " +
	"that resets a password or verifies an email address, using one, an instance admin's taking one as text, " +
	"inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an " +
	"invitation's link, and making an API token share a rate limit of 20 requests a minute for each IP address. " +
	"Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 " +
	"a minute for each email address, so that each counts against the other for the same address, and changing a " +
	"password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a " +
	"link as text, inviting someone, sending an invitation anew, accepting one, and making an API token share one " +
	"of 10 a minute for each account. The " +
	"RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over " +
	"a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the " +
	"limit that it is over, that of the email address included, and whose Retry-After header says how many seconds " +
	"to wait."

// rateLimit returns middleware for an operation, for its huma.Operation's
// Middlewares, that counts each request in bucket, for the client's IP
// address, and for the signed-in user if the authenticator found one, which
// it has before this middleware runs, and for the API token of a request made
// with one, and sets the RateLimit headers of the answer, or answers a
// request over the limit with rate_limited.
func rateLimit(limits *ratelimit.Limiter, bucket ratelimit.Bucket) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		keys := []ratelimit.Key{ratelimit.ClientIP(clientFrom(ctx.Context()).IP)}
		if cur, err := currentFrom(ctx.Context()); err == nil {
			keys = append(keys, ratelimit.UserID(cur.userID))
			if cur.token != nil {
				keys = append(keys, ratelimit.TokenID(cur.token.ID))
			}
		}
		d, err := limits.Take(ctx.Context(), bucket, keys...)
		if err != nil {
			r, w := humachi.Unwrap(ctx)
			api.WriteError(w, r, limitError(err))
			return
		}
		for name, values := range decisionHeaders(d) {
			ctx.SetHeader(name, values[0])
		}
		next(ctx)
	}
}

// limitError returns the error that the API answers err with: rate_limited,
// with Retry-After and the RateLimit headers of the key that refused the
// request, for a *ratelimit.LimitError, and with Retry-After for a
// *ratelimit.LockedError, or err itself if it is neither.
func limitError(err error) error {
	var limited *ratelimit.LimitError
	var locked *ratelimit.LockedError
	switch {
	case errors.As(err, &limited):
		h := decisionHeaders(limited.Decision)
		h.Set(api.RetryAfterHeader, seconds(limited.RetryAfter()))
		return huma.ErrorWithHeaders(&api.Error{Code: api.CodeRateLimited,
			Details: map[string]any{"reason": "rate", "bucket": limited.Bucket}}, h)
	case errors.As(err, &locked):
		return huma.ErrorWithHeaders(&api.Error{Code: api.CodeRateLimited,
			Message: i18n.Message{Key: i18n.K("error.rate_limited.locked")},
			Details: map[string]any{"reason": "locked"}}, http.Header{api.RetryAfterHeader: {seconds(locked.RetryAfter())}})
	}
	return err
}

// decisionHeaders returns the RateLimit headers that report d, or none if d
// reports no key.
func decisionHeaders(d ratelimit.Decision) http.Header {
	h := http.Header{}
	if d.Limit == 0 {
		return h
	}
	h.Set(limitHeader, strconv.Itoa(d.Limit))
	h.Set(remainingHeader, strconv.Itoa(d.Remaining))
	h.Set(resetHeader, seconds(d.Reset))
	return h
}

// seconds returns d in whole seconds, rounded up, and at least 1, so that a
// client that waits that long finds the limit reset.
func seconds(d time.Duration) string {
	return strconv.FormatInt(max(1, int64((d+time.Second-1)/time.Second)), 10)
}

type clientKey struct{}

// withClient gives each request's context the client that the sessions, the
// audit log, and the rate limits record (clientAddr), and its User-Agent
// header.
func withClient(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := account.Client{IP: clientAddr(r, trusted), UserAgent: r.Header.Get("User-Agent")}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientKey{}, c)))
		})
	}
}

// clientFrom returns the client of a request's context.
func clientFrom(ctx context.Context) account.Client {
	c, _ := ctx.Value(clientKey{}).(account.Client)
	return c
}

// clientAddr returns the address of the client that made r: the address that
// the connection comes from, unless that is one of the trusted proxies
// (STORYCREEP_TRUSTED_PROXIES), whose X-Forwarded-For header it believes
// then. Each proxy appends to that header the address that it received the
// request from, so the client is the rightmost address in it that is not a
// trusted proxy, or the leftmost, if every one is. An entry that is not an
// address ends the search: the trusted proxy to its right wrote it, and is
// taken for the client. The address is zero if the connection's is unknown.
func clientAddr(r *http.Request, trusted []netip.Prefix) netip.Addr {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	addr := ap.Addr().Unmap().WithZone("")
	if !trustedProxy(addr, trusted) {
		return addr
	}
	// A header given on several lines is one list (RFC 9110 §5.3).
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := forwardedAddr(hops[i])
		if !ok {
			break
		}
		addr = hop
		if !trustedProxy(addr, trusted) {
			break
		}
	}
	return addr
}

// forwardedAddr returns the address of an entry of X-Forwarded-For, which
// is an IP address, with or without a port, as some proxies write it.
func forwardedAddr(entry string) (netip.Addr, bool) {
	entry = strings.TrimSpace(entry)
	addr, err := netip.ParseAddr(entry)
	if err != nil {
		ap, err := netip.ParseAddrPort(entry)
		if err != nil {
			return netip.Addr{}, false
		}
		addr = ap.Addr()
	}
	return addr.Unmap().WithZone(""), true
}

// trustedProxy reports whether addr is one of the trusted proxies.
func trustedProxy(addr netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
