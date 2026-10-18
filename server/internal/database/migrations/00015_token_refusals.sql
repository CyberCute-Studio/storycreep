-- +goose Up

-- When the audit log last recorded a refusal of a request made with the API
-- token (PRD FR-TEN-07, NFR-SEC-05), or null if it never did. Package apitoken
-- records a token's refusals at most once a minute, as it writes the token's
-- last use (last_used_at, FR-AUTH-04 AC5), so that a client that repeats a
-- refused request, as a CI job left with a revoked token, or one that calls a
-- session-only route in a loop, adds an entry a minute to the workspace's
-- audit log rather than one a request.
ALTER TABLE api_token ADD COLUMN denied_at timestamptz;
