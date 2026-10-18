package api

import (
	"encoding/json"
	"maps"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// OpenAPI returns the OpenAPI document of the Mux's huma operations. A route
// registered with Handle is not in it.
func (m *Mux) OpenAPI() *huma.OpenAPI {
	return m.api.OpenAPI()
}

// Document returns the OpenAPI document of the Mux's operations in the form
// that the server serves and the repository commits (PRD FR-API-02): JSON,
// indented by two spaces, with a newline at the end. It is the same for the
// same operations, because huma writes the keys of every object in order.
func (m *Mux) Document() ([]byte, error) {
	b, err := json.MarshalIndent(m.api.OpenAPI(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// rateLimitHeaders documents the headers of a rate limit on the responses of
// an operation that one counts, but its default response, whose own
// documentation of them says more (errorResponse): every response to a
// request that the limit counted and allowed has them.
func rateLimitHeaders() map[string]*huma.Header {
	header := func(description string) *huma.Header {
		return &huma.Header{Schema: &huma.Schema{Type: huma.TypeInteger, Format: "int64", Description: description}}
	}
	return map[string]*huma.Header{
		RateLimitLimitHeader: header("How many requests of the operation's kind the client may make in a window " +
			"of time, for its IP address, its account, or the API token that it makes the request with, whichever " +
			"has the fewest left"),
		RateLimitRemainingHeader: header("How many more it may make in the current window"),
		RateLimitResetHeader:     header("How many seconds until the current window ends, and the limit is whole again"),
	}
}

// Operations returns the operations of a path of an OpenAPI document, by
// method, without the methods that the path has none of.
func Operations(item *huma.PathItem) map[string]*huma.Operation {
	all := map[string]*huma.Operation{
		http.MethodGet: item.Get, http.MethodPut: item.Put, http.MethodPost: item.Post,
		http.MethodDelete: item.Delete, http.MethodOptions: item.Options, http.MethodHead: item.Head,
		http.MethodPatch: item.Patch, http.MethodTrace: item.Trace,
	}
	maps.DeleteFunc(all, func(_ string, op *huma.Operation) bool { return op == nil })
	return all
}

// documentRateLimit documents the headers of a rate limit on every response
// of the route's operation in the OpenAPI document but its default one, which
// documents them already (errorResponse), unless the operation's output
// documents them itself.
func (m *Mux) documentRateLimit(r Route) {
	item := m.api.OpenAPI().Paths[r.Pattern]
	if item == nil {
		// A hidden operation is not in the document.
		return
	}
	op := Operations(item)[r.Method]
	if op == nil {
		return
	}
	for status, res := range op.Responses {
		if status == "default" {
			continue
		}
		if res.Headers == nil {
			res.Headers = map[string]*huma.Header{}
		}
		for name, h := range rateLimitHeaders() {
			if res.Headers[name] == nil {
				res.Headers[name] = h
			}
		}
	}
}

// errorResponse is the response that documents the error envelope, which
// answers every error of every operation (PRD FR-API-04), the header that
// names the language of its message (PRD FR-I18N-02), the header that says
// when to try again, and the headers of a rate limit. An operation that a
// rate limit counts, as every operation of the API is, has them on its
// errors too, once the limit has counted the request, but not on the answer
// to a request refused before then, as the router's middleware, the
// authenticator, and an operation's guards refuse it, nor on the
// internal_error of a count that failed.
func (m *Mux) errorResponse() *huma.Response {
	schema := m.api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[ErrorEnvelope](), true, "ErrorEnvelope")
	return &huma.Response{
		Description: "An error, in the error envelope; docs/error-codes.md lists the codes",
		Headers: map[string]*huma.Header{
			"Content-Language": {Schema: &huma.Schema{Type: huma.TypeString,
				Description: "The language of the envelope's message, as a BCP 47 tag, such as en"}},
			WWWAuthenticateHeader: {Schema: &huma.Schema{Type: huma.TypeString,
				Description: "The challenge of the Bearer scheme (RFC 6750 §3): with 401 unauthenticated from a " +
					"route that takes an API token, and to a request whose API token is not valid, with " +
					"error=\"invalid_token\", and with 403 insufficient_scope, with error=\"insufficient_scope\" " +
					"and the scope that the route needs"}},
			RetryAfterHeader: {Schema: &huma.Schema{Type: huma.TypeInteger,
				Description: "How many seconds to wait before trying again, with 429 rate_limited and 503 not_ready"}},
			RateLimitLimitHeader: {Schema: &huma.Schema{Type: huma.TypeInteger,
				Description: "On an operation that a rate limit counts, as every operation of the API is, how many " +
					"requests of its kind the client may make in a window of time, for its IP address, its account, " +
					"or the API token that it makes the request with, whichever has the fewest left, or, with 429 " +
					"rate_limited whose details.reason is rate, for the one that used up its limit, which may be the " +
					"email address that the request names. With details.reason locked, no limit is used up: the " +
					"email address is locked, and the headers report the IP address or the account as on any other " +
					"answer. An answer to a request refused before the rate limit counts it has none of the " +
					"RateLimit headers: one without valid credentials, a CSRF token, or an API token's scope where " +
					"the operation needs them, one that the signup policy or another check of the operation refuses " +
					"before it counts, one made while the server is not ready, and one whose count failed, with " +
					"internal_error"}},
			RateLimitRemainingHeader: {Schema: &huma.Schema{Type: huma.TypeInteger,
				Description: "On an operation that a rate limit counts, how many more requests of its kind the client " +
					"may make in the current window, for the same key as RateLimit-Limit"}},
			RateLimitResetHeader: {Schema: &huma.Schema{Type: huma.TypeInteger,
				Description: "On an operation that a rate limit counts, how many seconds until the current window " +
					"ends, and the limit is whole again"}},
		},
		Content: map[string]*huma.MediaType{"application/json": {Schema: schema}},
	}
}
