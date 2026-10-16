-- The rate limits and the locks of email addresses after failed sign-ins
-- (package ratelimit, PRD FR-AUTH-05, NFR-SEC-06). The queries compare with
-- the database's clock, which every server shares, so that the servers of an
-- instance count the same windows and locks.

-- name: RateLimitKey :one
-- RateLimitKey returns the key of the hash that the rate limits' tables hold
-- instead of an IP address, an account's ID, or an email address.
SELECT key FROM instance_key WHERE name = 'rate_limit';

-- name: FoldEmail :one
-- FoldEmail returns an email address in lower case, as citext compares
-- addresses, so that two spellings that the database takes for one address
-- count as one.
SELECT lower(@email::text)::text;

-- name: CommitWithoutFlush :exec
-- CommitWithoutFlush makes the transaction that it runs in commit without
-- waiting for its commit to be flushed to disk (synchronous_commit off), so
-- that the transaction holds the locks of its rows no longer than it runs. A
-- crash of the database may then lose the last few hundred milliseconds of
-- such commits, which for a rate limit's counters is a few requests that go
-- uncounted.
SELECT set_config('synchronous_commit', 'off', true);

-- name: TakeRateLimit :many
-- TakeRateLimit counts a hit of each key in a bucket, in the bucket's window
-- of length period that holds now, counted from the Unix epoch. A key whose
-- window has ended since its last hit starts the count of its new window. It
-- returns each key's hits in the window, and when the window ends. The keys
-- are taken in the order given, which the caller sorts, so that two requests
-- that share keys lock their rows in the same order and cannot deadlock.
INSERT INTO rate_limit AS r (bucket, key, window_start, window_end, hits)
SELECT
    @bucket::text,
    k.key,
    date_bin(@period::interval, now(), timestamptz '1970-01-01 00:00:00+00'),
    date_bin(@period::interval, now(), timestamptz '1970-01-01 00:00:00+00') + @period::interval,
    1
FROM unnest(@keys::bytea[]) WITH ORDINALITY AS k(key, n)
ORDER BY k.n
ON CONFLICT (bucket, key) DO UPDATE SET
    hits = CASE WHEN r.window_start < excluded.window_start THEN 1 ELSE r.hits + 1 END,
    window_start = greatest(r.window_start, excluded.window_start),
    window_end = greatest(r.window_end, excluded.window_end)
RETURNING r.key, r.hits, r.window_end, now()::timestamptz AS now;

-- name: BeginLoginAttempt :one
-- BeginLoginAttempt counts a sign-in attempt with an email address as a
-- failure, until ClearLoginAttempts says that it succeeded, unless the
-- address is locked. Attempts that follow the last one by more than forget
-- are counted from 1 again. The attempt that makes failures reach threshold,
-- which is at least 2, locks the address for first_lock, and each one after
-- it, once that lock ends, for twice as long as the one before, up to
-- max_lock. It returns the failures counted, and no row while the address is
-- locked. (sqlc misreads a parameter that an operator follows, such as
-- @first_lock::interval * 2, as one name, hence the parentheses.)
INSERT INTO login_attempt AS a (key, failures, last_attempt_at)
VALUES (@key, 1, now())
ON CONFLICT (key) DO UPDATE SET
    failures = CASE WHEN a.last_attempt_at > now() - @forget::interval THEN a.failures + 1 ELSE 1 END,
    last_attempt_at = now(),
    locked_until = CASE
        WHEN a.last_attempt_at <= now() - @forget::interval OR a.failures + 1 < @threshold::integer THEN NULL
        WHEN a.failures + 1 - @threshold::integer >= 20
            OR (@first_lock::interval) * power(2, a.failures + 1 - @threshold::integer) >= @max_lock::interval
            THEN now() + @max_lock::interval
        ELSE now() + (@first_lock::interval) * power(2, a.failures + 1 - @threshold::integer)
    END
WHERE a.locked_until IS NULL OR a.locked_until <= now()
RETURNING a.failures;

-- name: LoginLockedFor :one
-- LoginLockedFor returns how much longer an email address is locked, which
-- is zero or less once its lock has ended, or null if it has none.
SELECT (locked_until - now())::interval AS remaining FROM login_attempt WHERE key = @key;

-- name: ClearLoginAttempts :exec
-- ClearLoginAttempts forgets the attempts with an email address, which a
-- successful sign-in does, so that its failures count from 1 again
-- (FR-AUTH-05 AC3).
DELETE FROM login_attempt WHERE key = @key;

-- name: DeleteEndedRateLimits :execrows
-- DeleteEndedRateLimits deletes at most max_rows of the counters whose
-- window has ended, which count nothing any more. The rate limit cleanup job
-- runs it until it deletes fewer.
DELETE FROM rate_limit WHERE (bucket, key) IN (
    SELECT e.bucket, e.key FROM rate_limit e WHERE e.window_end <= now() LIMIT @max_rows
);

-- name: DeleteForgottenLoginAttempts :execrows
-- DeleteForgottenLoginAttempts deletes at most max_rows of the attempts
-- whose last one was more than forget ago, which BeginLoginAttempt would
-- count from 1 again, and whose lock has ended.
DELETE FROM login_attempt WHERE key IN (
    SELECT e.key FROM login_attempt e
    WHERE e.last_attempt_at <= now() - @forget::interval
        AND (e.locked_until IS NULL OR e.locked_until <= now())
    LIMIT @max_rows
);
