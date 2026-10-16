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
| `unauthenticated` | 401 | Authentication is required. | The request has no credentials, or credentials that are wrong. A route that needs a signed-in user answers with it to a request without the session cookie that `POST /api/v1/auth/login` sets, or with the cookie of a session that has ended: signed out, revoked, unused for `STORYCREEP_SESSION_IDLE_TIMEOUT`, past `STORYCREEP_SESSION_LIFETIME`, or of an account that can no longer sign in. The answer then clears the cookie. `/metrics` answers with it to a request without `STORYCREEP_METRICS_TOKEN` as its bearer token, and its `WWW-Authenticate` header names the scheme. |
| `invalid_credentials` | 401 | The email address or the password is wrong. | `POST /api/v1/auth/login` was given an email address that belongs to no account, a password that is not the account's, or the address of an account that cannot sign in, such as a suspended one. The answer is the same for each, so that it does not tell whether an address has an account (PRD FR-AUTH-01). |
| `csrf_failed` | 403 | The request lacks the session's CSRF token, or came from another origin. | The request changes something, with a method other than `GET`, `HEAD`, or `OPTIONS` (PRD FR-AUTH-02 AC5). Either it was made to a route that needs a signed-in user with the session cookie but without the session's CSRF token, which `POST /api/v1/auth/login` and `GET /api/v1/auth/session` return as `csrf_token`, in its `X-CSRF-Token` header, or a browser sent it, to any route, signing in included, from another site or from another origin of the same site, as its `Sec-Fetch-Site` header says, whatever its credentials. Browsers send that header only to `https` URLs and to loopback addresses, such as `localhost`, so with a plain-HTTP `STORYCREEP_PUBLIC_URL` of another host only the CSRF token is checked, on the routes that need a signed-in user. |
| `wrong_password` | 403 | The current password is wrong. | `POST /api/v1/auth/password` was given a current password that is not the account's, so the password stays as it was. |
| `signup_closed` | 403 | Registration is closed on this server. | The instance's signup policy, `STORYCREEP_SIGNUP`, is `closed`, so `POST /api/v1/auth/register` creates no account, whatever the request holds (PRD FR-AUTH-01 AC3). |
| `invite_required` | 403 | Registration on this server needs an invitation. | The instance's signup policy, `STORYCREEP_SIGNUP`, is `invite`, so only an invitation creates an account, and `POST /api/v1/auth/register` creates none, whatever the request holds. |
| `not_found` | 404 | The resource does not exist. | No route matches the path, or the resource it names does not exist. |
| `method_not_allowed` | 405 | The method is not allowed for this resource. | A route matches the path, but not the method. The `Allow` header lists the methods it takes. |
| `request_timeout` | 408 | The request body did not arrive in time. | The client did not send the whole request body within the route's time limit. |
| `request_too_large` | 413 | The request body is too large. | The request body is larger than the route accepts, 5 MB unless the route says otherwise (PRD NFR-SEC-07). `details.max_bytes` is the limit. |
| `unsupported_media_type` | 415 | The request body is not in a supported format. | The `Content-Type` of the request body is neither `application/json` nor a type ending in `+json`, such as `application/merge-patch+json`. A body sent without a `Content-Type` is read as JSON. |
| `validation_failed` | 422 | The request is invalid. | A parameter or the body does not match the route's schema: a field that the schema does not have, which is never ignored (PRD FR-API-01 AC2), a missing field, a value of the wrong type, or one out of range; or a field's value is not one that the route takes, such as a registration's email address that is not one. `details.errors` lists each problem with its `location`, such as `body.name` or `query.limit`, and a `message`. |
| `weak_password` | 422 | The password is too weak. | The password breaks the password policy (PRD FR-AUTH-01 AC2). `details.reason` says how: `too_short`, with fewer than 12 characters, counted as Unicode code points once the password is in NFKC, or `common`, one of the 10,000 most common passwords, compared without regard to case. `details.errors` names the field, with its `location` and a `message`, as for `validation_failed`. |
| `rate_limited` | 429 | Too many requests. Try again later. | The client made too many requests (PRD FR-API-04 AC3, NFR-SEC-06), or signing in with an email address failed too often in a row (FR-AUTH-05). The `Retry-After` header says how many seconds to wait. `details.reason` says which: `rate`, when the requests of a kind, its `details.bucket`, used up their limit for the client's IP address, its account, or the email address that they name, until the `RateLimit-Reset` header's seconds have passed; or `locked`, when 10 sign-ins in a row with the email address failed, whether or not it belongs to an account, which locks it for a minute, and each further failure, once a lock ends, for twice as long as the lock before, up to 15 minutes. While the address is locked, `POST /api/v1/auth/login` refuses every sign-in with it, even with the right password, until one succeeds after the lock. The failures are forgotten a day after the last one. `details.bucket` is `auth` for signing in, registering, and changing a password. |
| `internal_error` | 500 | An internal error occurred. | The server failed. Its log has the cause, under the envelope's `request_id`. |
| `not_ready` | 503 | The server is not ready. | The server is starting, its database has pending migrations, or it is stopping. `/readyz` answers with it then too, and also while the server cannot reach its database, which the API's routes report as `internal_error` instead. `details.reason` says which: `starting`, `migrations_pending`, `stopping`, or, from `/readyz` only, `database_unreachable`. A `Retry-After` header suggests when to try again. |
