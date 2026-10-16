package api

import (
	"encoding/json"
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

// errorResponse is the response that documents the error envelope, which
// answers every error of every operation (PRD FR-API-04), the header that
// names the language of its message (PRD FR-I18N-02), the header that says
// when to try again, and the headers of a rate limit, which every answer of
// an operation that a rate limit counts has, an error's too.
func (m *Mux) errorResponse() *huma.Response {
	schema := m.api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[ErrorEnvelope](), true, "ErrorEnvelope")
	return &huma.Response{
		Description: "An error, in the error envelope; docs/error-codes.md lists the codes",
		Headers: map[string]*huma.Header{
			"Content-Language": {Schema: &huma.Schema{Type: huma.TypeString,
				Description: "The language of the envelope's message, as a BCP 47 tag, such as en"}},
			RetryAfterHeader: {Schema: &huma.Schema{Type: huma.TypeInteger,
				Description: "How many seconds to wait before trying again, with 429 rate_limited and 503 not_ready"}},
			RateLimitLimitHeader: {Schema: &huma.Schema{Type: huma.TypeInteger,
				Description: "On an operation that a rate limit counts, how many requests of its kind the client may " +
					"make in a window of time, for its IP address or its account, whichever has the fewest left, " +
					"or, with 429 rate_limited, for the one that used up its limit, which may be the email address " +
					"that the request names"}},
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
