package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
)

// Rate limits on the wire (PRD FR-API-04, NFR-SEC-06). Every operation of
// the API counts each request in the bucket that its route declares (package
// api), read for one that reads, write for one that changes something, or
// auth or export for one that says so, for the client's IP address, and for
// its signed-in user, once the authenticator has found one, and for the API
// token that it is made with, if it is, before it reads the request's body.
// Only the health and readiness probes, /version, and the pages of the web
// interface count in none, because they answer without the database, in which
// the limits count. An operation's answers to the requests that it counts,
// but not to those that the router's middleware, the authenticator, or a
// guard refused before, say how much of the limit is left in the RateLimit
// headers of draft-ietf-httpapi-ratelimit-headers: RateLimit-Limit, how many
// requests the key may make in a window, RateLimit-Remaining, how many more,
// and RateLimit-Reset, how many seconds until the window ends, of the key
// with the fewest requests left. A request over the limit is answered with
// 429 rate_limited, and Retry-After says how many seconds to wait (FR-API-04
// AC3).

// The headers of a rate limit, which package api names, so that an
// operation's error replaces those that the limiter set before it.
const (
	limitHeader     = api.RateLimitLimitHeader
	remainingHeader = api.RateLimitRemainingHeader
	resetHeader     = api.RateLimitResetHeader
)

// authLimitDoc says, in the description of an operation that the auth bucket
// limits, what its rate limit does, as the auth bucket's rate r of the policy
// says, so that the description changes with the limits, as bucketDoc's do.
func authLimitDoc(r ratelimit.Rate) string {
	// The descriptions are in the source locale, as bucketDoc's.
	digits := i18n.Default().Localizer(i18n.Source).Number
	window := per(r.Period)
	return fmt.Sprintf(" Signing in, registering, changing a password, completing first-run setup, asking for a "+
		"link that resets a password or verifies an email address, using one, an instance admin's taking one as "+
		"text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with "+
		"an invitation's link, and making, listing, or revoking an API token share a rate limit of %s requests %s "+
		"for each IP address. Registering, with an invitation or without, and asking for a link that resets a "+
		"password also share one of %s %s for each email address, so that each counts against the other for the "+
		"same address, and changing a password, asking for a link that verifies the signed-in account's email "+
		"address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, "+
		"accepting one, and making, listing, or revoking an API token share one of %s %s for each account. No "+
		"request made with an API token counts against these, so that the requests of a token, which count against "+
		"its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts "+
		"them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a "+
		"request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit "+
		"headers report the limit that it is over, that of the email address included, and whose Retry-After "+
		"header says how many seconds to wait.", digits(r.Limits[ratelimit.IP]), window,
		digits(r.Limits[ratelimit.Email]), window, digits(r.Limits[ratelimit.User]), window)
}

// bucketDoc says, in the description of an operation whose requests count in
// the bucket b, other than auth, and which callers may make, what its rate
// limit is, as the rate r of the policy says: the limits of the keys that its
// requests count against.
func bucketDoc(b ratelimit.Bucket, r ratelimit.Rate, callers api.Callers) string {
	what := map[ratelimit.Bucket]string{ratelimit.Read: "Requests that read", ratelimit.Write: "Requests that " +
		"change something", ratelimit.Export: "Exports"}[b]
	// The descriptions are in the source locale, which writes 1200 as 1,200.
	digits := i18n.Default().Localizer(i18n.Source).Number
	ip, user, token := digits(r.Limits[ratelimit.IP]), digits(r.Limits[ratelimit.User]),
		digits(r.Limits[ratelimit.Token])
	var limits, left string
	switch callers {
	case api.Anyone:
		limits, left = ip+" "+per(r.Period)+" for each IP address", "of the IP address"
	case api.Sessions:
		limits, left = ip+" "+per(r.Period)+" for each IP address, and "+user+" for each account",
			"of whichever of the two has the fewest requests left"
	default:
		limits, left = ip+" "+per(r.Period)+" for each IP address, "+user+" for each account, and "+token+
			" for each API token, whose requests count against its owner's account too",
			"of whichever of them has the fewest requests left"
	}
	return fmt.Sprintf(" %s share a rate limit of %s. The RateLimit headers say how much is left of the limit %s, "+
		"and a request over a limit is answered with 429 rate_limited, whose details.bucket is %s, and whose "+
		"Retry-After header says how many seconds to wait.", what, limits, left, b)
}

// per returns how often a window of length d comes, as a description says
// it: "a minute" or "an hour".
func per(d time.Duration) string {
	switch d {
	case time.Minute:
		return "a minute"
	case time.Hour:
		return "an hour"
	}
	return "every " + d.String()
}

// limiter is the API's RateLimiter (api.MuxOptions): it counts each request
// of an operation in the bucket of its route with the server's Limiter, for
// the client's IP address, for the signed-in user if the authenticator found
// one, which it has before the limiter counts, and for the API token of a
// request made with one, and sets the RateLimit headers of the answer, or
// answers a request over the limit with rate_limited. A bucket of package
// api is the bucket of package ratelimit of the same name, whose rate in the
// server's Limiter Doc finds when the route is registered. It reads the
// server's Limiter at each request, so that a test can give the server one
// with another policy before it makes requests.
type limiter struct {
	s *server
}

var _ api.RateLimiter = limiter{}

func (l limiter) Take(ctx huma.Context, b api.Bucket) error {
	bucket := ratelimit.Bucket(b)
	keys := []ratelimit.Key{ratelimit.ClientIP(clientFrom(ctx.Context()).IP)}
	if cur, err := currentFrom(ctx.Context()); err == nil {
		keys = append(keys, ratelimit.UserID(cur.userID))
		if cur.token != nil {
			keys = append(keys, ratelimit.TokenID(cur.token.ID))
		}
	}
	d, err := l.s.limits.Take(ctx.Context(), bucket, keys...)
	if err != nil {
		return limitError(err)
	}
	for name, values := range decisionHeaders(d) {
		ctx.SetHeader(name, values[0])
	}
	return nil
}

// Doc says what the server's Limiter limits the bucket b to, which newServer
// gives it before it registers the routes, the auth bucket's included. It
// panics on a bucket that the Limiter has no rate of, so that a route that
// counts in one fails when it is registered rather than at its first request.
func (l limiter) Doc(b api.Bucket, callers api.Callers) string {
	bucket := ratelimit.Bucket(b)
	rate, ok := l.s.limits.Rate(bucket)
	switch {
	case !ok:
		panic(fmt.Sprintf("httpserver: the bucket %q has no rate limit", b))
	case b == api.BucketAuth:
		return authLimitDoc(rate)
	}
	return bucketDoc(bucket, rate, callers)
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
