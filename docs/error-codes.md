<!-- Generated from server/internal/api. Do not edit; run
     "go test ./internal/api -run TestReference -update" in server/ instead. -->

# Error codes

Every response with a 4xx or 5xx status has the same body, the error envelope
(PRD FR-API-04):

```json
{
  "error": {
    "code": "validation_failed",
    "message": "The request is invalid.",
    "details": {
      "errors": [{"location": "body.colour", "message": "unexpected property"}]
    },
    "request_id": "0199e2c4-5b7a-7d43-9f0e-3c1d2a8b6e51"
  }
}
```

- `code` is one of the codes below. A code keeps its meaning and its
  status once it is released, so clients branch on it.
- `message` says what went wrong, for people. It may change, and is
  not for programs to read. It is in the language that the request's
  `Accept-Language` header asks for, if the server has a catalog for it,
  or else in the instance's default language, `STORYCREEP_DEFAULT_LOCALE`
  (PRD FR-I18N-02), and the response's `Content-Language` header names
  the language. The messages below are the English ones.
- `details` is an object whose keys the code decides, or is absent. It
  is for programs, so the messages in it, as in `details.errors`, are
  in English.
- `request_id` is the request's ID, which is also in the
  `X-Request-ID` response header and in every line the server logs
  about the request.

## Codes

| Code | Status | Message | When |
| --- | --- | --- | --- |
| `invalid_request` | 400 | The request is malformed. | The request body is not valid JSON, or not one JSON value; it has a key twice in one object, text that is not UTF-8, or values nested more than 256 deep; or a required body is missing. `details.errors` lists each problem with its `location`, such as `body`. |
| `invalid_id` | 400 | An identifier is invalid. | An identifier does not decode, or is the identifier of another type of entity, such as a board's identifier where a node's is expected. `details.reason` says why: `missing_prefix`, `unknown_prefix`, `wrong_prefix`, `character`, `length`, or `overflow`, as in `schema/ids/README.md`. |
| `invalid_cursor` | 400 | The pagination cursor is invalid. | The `cursor` query parameter is not a cursor that the same list returned as `next_cursor`. |
| `unauthenticated` | 401 | Authentication is required. | The request has no credentials, or credentials that are wrong, such as a bearer token for `/metrics` that is not `STORYCREEP_METRICS_TOKEN`. The `WWW-Authenticate` header says which scheme the route takes. |
| `not_found` | 404 | The resource does not exist. | No route matches the path, or the resource it names does not exist. |
| `method_not_allowed` | 405 | The method is not allowed for this resource. | A route matches the path, but not the method. The `Allow` header lists the methods it takes. |
| `request_timeout` | 408 | The request body did not arrive in time. | The client did not send the whole request body within the route's time limit. |
| `request_too_large` | 413 | The request body is too large. | The request body is larger than the route accepts, 5 MB unless the route says otherwise (PRD NFR-SEC-07). `details.max_bytes` is the limit. |
| `unsupported_media_type` | 415 | The request body is not in a supported format. | The `Content-Type` of the request body is neither `application/json` nor a type ending in `+json`, such as `application/merge-patch+json`. A body sent without a `Content-Type` is read as JSON. |
| `validation_failed` | 422 | The request is invalid. | A parameter or the body does not match the route's schema: a field that the schema does not have, which is never ignored (PRD FR-API-01 AC2), a missing field, a value of the wrong type, or one out of range. `details.errors` lists each problem with its `location`, such as `body.name` or `query.limit`, and a `message`. |
| `internal_error` | 500 | An internal error occurred. | The server failed. Its log has the cause, under the envelope's `request_id`. |
| `not_ready` | 503 | The server is not ready. | The server is starting, its database has pending migrations, or it is stopping. `/readyz` answers with it then too, and also while the server cannot reach its database, which the API's routes report as `internal_error` instead. `details.reason` says which: `starting`, `migrations_pending`, `stopping`, or, from `/readyz` only, `database_unreachable`. A `Retry-After` header suggests when to try again. |
