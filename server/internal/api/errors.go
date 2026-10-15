package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/logging"
)

// Code is a stable error code of the error envelope (PRD FR-API-04, §10.5).
// Clients branch on the code, never on the message, so a code, once released,
// keeps its meaning and its status.
type Code string

// The error codes. Each is registered in codes with its status, and
// docs/error-codes.md is generated from the registry.
const (
	CodeInvalidRequest       Code = "invalid_request"
	CodeInvalidID            Code = "invalid_id"
	CodeInvalidCursor        Code = "invalid_cursor"
	CodeUnauthenticated      Code = "unauthenticated"
	CodeNotFound             Code = "not_found"
	CodeMethodNotAllowed     Code = "method_not_allowed"
	CodeRequestTimeout       Code = "request_timeout"
	CodeRequestTooLarge      Code = "request_too_large"
	CodeUnsupportedMediaType Code = "unsupported_media_type"
	CodeValidationFailed     Code = "validation_failed"
	CodeInternal             Code = "internal_error"
	CodeNotReady             Code = "not_ready"
)

// codeSpec registers a code.
type codeSpec struct {
	code   Code
	status int
	// message is the envelope's message for the code. Display messages
	// move to the i18n catalog in plan item 10.
	message string
	// doc says when the code is returned, in Markdown, for the reference.
	doc string
}

// codes is the registry of error codes, in the order of the reference.
var codes = []codeSpec{
	{CodeInvalidRequest, http.StatusBadRequest, "The request is malformed.",
		"The request body is not valid JSON, or not one JSON value; it has a key twice in one object, " +
			"text that is not UTF-8, or values nested more than " + fmt.Sprint(maxJSONDepth) + " deep; or a " +
			"required body is missing. `details.errors` lists each problem with its `location`, such as `body`."},
	{CodeInvalidID, http.StatusBadRequest, "An identifier is invalid.",
		"An identifier does not decode, or is the identifier of another type of entity, such as a board's " +
			"identifier where a node's is expected. `details.reason` says why: `missing_prefix`, `unknown_prefix`, " +
			"`wrong_prefix`, `character`, `length`, or `overflow`, as in `schema/ids/README.md`."},
	{CodeInvalidCursor, http.StatusBadRequest, "The pagination cursor is invalid.",
		"The `cursor` query parameter is not a cursor that the same list returned as `next_cursor`."},
	{CodeUnauthenticated, http.StatusUnauthorized, "Authentication is required.",
		"The request has no credentials, or credentials that are wrong, such as a bearer token for " +
			"`/metrics` that is not `STORYCREEP_METRICS_TOKEN`. The `WWW-Authenticate` header says which " +
			"scheme the route takes."},
	{CodeNotFound, http.StatusNotFound, "The resource does not exist.",
		"No route matches the path, or the resource it names does not exist."},
	{CodeMethodNotAllowed, http.StatusMethodNotAllowed, "The method is not allowed for this resource.",
		"A route matches the path, but not the method. The `Allow` header lists the methods it takes."},
	{CodeRequestTimeout, http.StatusRequestTimeout, "The request body did not arrive in time.",
		"The client did not send the whole request body within the route's time limit."},
	{CodeRequestTooLarge, http.StatusRequestEntityTooLarge, "The request body is too large.",
		"The request body is larger than the route accepts, 5 MB unless the route says otherwise " +
			"(PRD NFR-SEC-07). `details.max_bytes` is the limit."},
	{CodeUnsupportedMediaType, http.StatusUnsupportedMediaType, "The request body is not in a supported format.",
		"The `Content-Type` of the request body is not `application/json`."},
	{CodeValidationFailed, http.StatusUnprocessableEntity, "The request is invalid.",
		"A parameter or the body does not match the route's schema: a field that the schema does not have, " +
			"which is never ignored (PRD FR-API-01 AC2), a missing field, a value of the wrong type, or one out of " +
			"range. `details.errors` lists each problem with its `location`, such as `body.name` or `query.limit`, " +
			"and a `message`."},
	{CodeInternal, http.StatusInternalServerError, "An internal error occurred.",
		"The server failed. Its log has the cause, under the envelope's `request_id`."},
	{CodeNotReady, http.StatusServiceUnavailable, "The server is not ready.",
		"The server is starting, its database has pending migrations, it is stopping, or it cannot reach its " +
			"database. `/readyz` answers with it too, and `details.reason` says which: `starting`, " +
			"`migrations_pending`, `stopping`, or `database_unreachable`. A `Retry-After` header suggests when " +
			"to try again."},
}

// spec returns the registration of c. It panics on a code that is not
// registered, which a test rules out for the constants.
func spec(c Code) codeSpec {
	for _, s := range codes {
		if s.code == c {
			return s
		}
	}
	panic(fmt.Sprintf("api: unregistered error code %q", c))
}

// Status returns the HTTP status of the code.
func (c Code) Status() int {
	return spec(c).status
}

// Error is an error that the API reports in its error envelope. A handler
// returns one to answer with a 4xx or 5xx status. Any other error a handler
// returns is answered with internal_error, and logged; an *ids.DecodeError is
// answered with invalid_id.
type Error struct {
	Code Code
	// Message replaces the code's message, if it is set.
	Message string
	// Details are the envelope's details, which must encode as a JSON
	// object. They are for programs, so their keys are stable.
	Details map[string]any
}

// NewError returns an error with the code's message and no details.
func NewError(c Code) *Error {
	return &Error{Code: c}
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" {
		msg = spec(e.Code).message
	}
	return string(e.Code) + ": " + msg
}

// ErrorEnvelope is the body of every 4xx and 5xx response:
// {"error": {"code", "message", "details", "request_id"}} (PRD FR-API-04).
// It is what the API writes for an error, so it is huma's error type too.
type ErrorEnvelope struct {
	Body ErrorBody `json:"error"`

	status int
}

// ErrorBody is the envelope's object.
type ErrorBody struct {
	Code    Code           `json:"code" doc:"A stable error code; docs/error-codes.md lists them"`
	Message string         `json:"message" doc:"What went wrong, for people"`
	Details map[string]any `json:"details,omitempty" doc:"More about the error, for programs; the code decides the keys"`
	// RequestID is filled in when the envelope is written.
	RequestID string `json:"request_id" doc:"The request's ID, which the server's log records it under"`
}

func (e *ErrorEnvelope) Error() string {
	return string(e.Body.Code) + ": " + e.Body.Message
}

// GetStatus returns the HTTP status, for huma.
func (e *ErrorEnvelope) GetStatus() int {
	return e.status
}

// ContentType makes huma write the envelope as plain JSON, instead of the
// application/problem+json of its own error type.
func (e *ErrorEnvelope) ContentType(string) string {
	return "application/json"
}

func envelopeFor(c Code, message string, details map[string]any) *ErrorEnvelope {
	s := spec(c)
	if message == "" {
		message = s.message
	}
	return &ErrorEnvelope{Body: ErrorBody{Code: c, Message: message, Details: details}, status: s.status}
}

// toEnvelope returns the envelope that answers err, which a handler
// returned. An error that is not one the API reports is logged with the
// request, and answered with internal_error, so that its text never reaches
// the client.
func toEnvelope(ctx context.Context, err error) *ErrorEnvelope {
	var env *ErrorEnvelope
	var apiErr *Error
	var idErr *ids.DecodeError
	switch {
	case errors.As(err, &env):
		return env
	case errors.As(err, &apiErr):
		return envelopeFor(apiErr.Code, apiErr.Message, apiErr.Details)
	case errors.As(err, &idErr):
		return envelopeFor(CodeInvalidID, strings.TrimPrefix(idErr.Error(), "ids: "),
			map[string]any{"reason": idErr.Reason})
	}
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		// The client went away, and the handler stopped. Nothing failed, and
		// nothing will read the answer.
		loggerFrom(ctx).DebugContext(ctx, "the request ended early", "error", err)
	} else {
		loggerFrom(ctx).ErrorContext(ctx, "the request failed", "error", err)
	}
	return envelopeFor(CodeInternal, "", nil)
}

// statusCodes maps the statuses that huma answers with itself to codes.
var statusCodes = map[int]Code{
	http.StatusBadRequest:            CodeInvalidRequest,
	http.StatusUnauthorized:          CodeUnauthenticated,
	http.StatusNotFound:              CodeNotFound,
	http.StatusMethodNotAllowed:      CodeMethodNotAllowed,
	http.StatusRequestTimeout:        CodeRequestTimeout,
	http.StatusRequestEntityTooLarge: CodeRequestTooLarge,
	http.StatusUnsupportedMediaType:  CodeUnsupportedMediaType,
	http.StatusUnprocessableEntity:   CodeValidationFailed,
	http.StatusInternalServerError:   CodeInternal,
}

// fromHuma is huma.NewErrorWithContext: it turns an error that huma reports
// itself, such as a body that fails validation, into the envelope. op is the
// operation, or nil when huma gives none.
func fromHuma(ctx context.Context, op *huma.Operation, status int, msg string, errs []error) *ErrorEnvelope {
	if status == http.StatusNotModified {
		// huma writes no body for it, so it is not an error response.
		return &ErrorEnvelope{status: status}
	}
	c, ok := statusCodes[status]
	if !ok {
		// Registration asks for an example with status 0.
		if status != 0 {
			loggerFrom(ctx).ErrorContext(ctx, "huma answered with a status that has no error code",
				"status", status, "message", msg)
		}
		c = CodeInternal
	}
	var details map[string]any
	switch c {
	case CodeInvalidRequest, CodeValidationFailed:
		details = map[string]any{"errors": errorDetails(msg, errs)}
	case CodeRequestTooLarge:
		if op != nil && op.MaxBodyBytes > 0 {
			// Register adds the byte that huma's limit takes away.
			details = map[string]any{"max_bytes": op.MaxBodyBytes - 1}
		}
	case CodeInternal:
		loggerFrom(ctx).ErrorContext(ctx, "the request failed", "error", errors.Join(append([]error{errors.New(msg)},
			errs...)...))
	}
	return envelopeFor(c, "", details)
}

// Problem is one entry of details.errors.
type Problem struct {
	// Location is where the problem is, such as body.name or query.limit.
	Location string `json:"location,omitempty"`
	Message  string `json:"message"`
}

// errorDetails lists huma's problems. huma also records the offending value,
// which is left out: it may be a password, and it may be large.
func errorDetails(msg string, errs []error) []Problem {
	var out []Problem
	for _, err := range errs {
		if err == nil {
			continue
		}
		var d huma.ErrorDetailer
		if errors.As(err, &d) {
			detail := d.ErrorDetail()
			out = append(out, Problem{Location: detail.Location, Message: detail.Message})
			continue
		}
		out = append(out, Problem{Message: err.Error()})
	}
	if len(out) == 0 {
		out = append(out, Problem{Message: msg})
	}
	return out
}

// WriteError writes the envelope that answers err, as toEnvelope builds it,
// for a handler that is not a huma operation.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	writeEnvelope(w, r, toEnvelope(r.Context(), err))
}

func writeEnvelope(w http.ResponseWriter, r *http.Request, env *ErrorEnvelope) {
	env.Body.RequestID = logging.RequestID(r.Context())
	b, err := json.Marshal(env)
	if err != nil {
		// Details that do not encode are a bug in the code that set them.
		loggerFrom(r.Context()).ErrorContext(r.Context(), "encode an error envelope", "error", err)
		env = envelopeFor(CodeInternal, "", nil)
		env.Body.RequestID = logging.RequestID(r.Context())
		b, _ = json.Marshal(env)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(env.status)
	_, _ = w.Write(append(b, '\n'))
}

type loggerKey struct{}

// withLogger returns a context whose errors are logged to log.
func withLogger(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, log)
}

// loggerFrom returns the logger of the context's Mux, or one that discards.
func loggerFrom(ctx context.Context) *slog.Logger {
	if log, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return log
	}
	return slog.New(slog.DiscardHandler)
}
