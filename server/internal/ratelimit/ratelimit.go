// Package ratelimit limits how often clients make requests, and locks an
// email address out of signing in after too many failed sign-ins in a row
// (PRD FR-AUTH-05, NFR-SEC-06, FR-API-04).
//
// A rate limit counts the requests of a kind, a bucket (auth, read, write,
// export, or connect), separately for each key that makes them: the
// client's IP address, the signed-in user, or the email address that a
// request names. Each bucket counts in fixed windows of time, of the same
// length for every key, and allows each kind of key a number of requests in
// a window (Policy). A request is allowed only if none of its keys has used
// up its limit, and counts against all of them either way.
//
// The counters are rows in PostgreSQL, and every server of an instance
// counts in the same rows, with the database's clock, so the limits hold
// however requests are spread across servers (FR-AUTH-05 AC2). They hold the
// keys as a keyed hash, so that they hold no IP address or email address as
// it is.
//
// Signing in with an email address counts as a failure until it succeeds,
// which forgets the failures (FR-AUTH-05 AC3). The attempt that makes
// Lockout.Failures in a row locks the address for Lockout.First, and each
// failure after a lock ends locks it for twice as long as the lock before,
// up to Lockout.Max: an exponential backoff. While the address is locked,
// every sign-in with it is refused before its password is checked, even a
// right one (AC1). An address is counted whether or not it belongs to an
// account, so that a lock does not tell whether it does (FR-AUTH-01).
package ratelimit

import (
	"bytes"
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Bucket is a kind of request that is limited on its own.
type Bucket string

// The buckets (NFR-SEC-06).
const (
	// Auth is signing in, registering, changing a password, and, as the
	// plan's later items add them, resetting a password and creating an
	// API token (FR-AUTH-05).
	Auth Bucket = "auth"
	// Read is a request that reads.
	Read Bucket = "read"
	// Write is a request that changes something.
	Write Bucket = "write"
	// Export is a request for an export.
	Export Bucket = "export"
	// Connect is the opening of a WebSocket connection.
	Connect Bucket = "connect"
)

// Kind is what a key counts the requests of.
type Kind string

// The kinds of key.
const (
	// IP counts a client's IP address, and an IPv6 address's /64 prefix,
	// which one user typically has all of.
	IP Kind = "ip"
	// User counts a signed-in user.
	User Kind = "user"
	// Email counts an email address that a registration names, whether or
	// not it belongs to an account. Signing in does not count it: the lock
	// of an address after failed sign-ins limits those (BeginLogin).
	Email Kind = "email"
)

// Key is what a request counts against in a bucket.
type Key struct {
	kind  Kind
	value string
	// hidden says that the key's counter is not the client's own business,
	// so that a Decision reports it only when it refuses a request: the
	// count of an email address says how often anyone used the address.
	hidden bool
}

// ClientIP returns the key of a client's IP address, which counts an IPv6
// address by its /64 prefix, and every client whose address is unknown, the
// zero Addr, as one.
func ClientIP(addr netip.Addr) Key {
	addr = addr.Unmap().WithZone("")
	value := "unknown"
	switch {
	case addr.Is6():
		p, _ := addr.Prefix(64)
		value = p.String()
	case addr.IsValid():
		value = addr.String()
	}
	return Key{kind: IP, value: value}
}

// UserID returns the key of a signed-in user.
func UserID(id ids.UUID) Key {
	return Key{kind: User, value: id.String()}
}

// EmailAddress returns the key of an email address, as package account
// holds it (account's canonical form), which the database folds to lower
// case, as it compares addresses, so that two spellings of one address are
// one key.
func EmailAddress(email string) Key {
	return Key{kind: Email, value: email, hidden: true}
}

// Rate is how many requests a bucket allows each kind of key in a window.
type Rate struct {
	// Period is the length of the bucket's windows, which start at the
	// multiples of Period since the Unix epoch.
	Period time.Duration
	// Limits are how many requests each kind of key may make in a window. A
	// key of a kind without a limit cannot be taken in the bucket.
	Limits map[Kind]int
}

// Lockout is when failed sign-ins lock an email address.
type Lockout struct {
	// Failures is how many sign-ins in a row may fail before the address is
	// locked, at least 2.
	Failures int
	// First is how long the first lock lasts, and Max how long a lock lasts
	// at most, however many failures follow.
	First, Max time.Duration
	// Forget is how long after the last attempt the failures are forgotten,
	// longer than Max, so that a lock does not reset the count.
	Forget time.Duration
}

// Policy is the limits of a Limiter.
type Policy struct {
	Rates   map[Bucket]Rate
	Lockout Lockout
}

// DefaultPolicy returns the server's limits. A request from an address that
// many people share, such as an office's, counts against one IP address, so
// each bucket allows an IP address twice as much as one user.
func DefaultPolicy() Policy {
	return Policy{
		Rates: map[Bucket]Rate{
			Auth:    {Period: time.Minute, Limits: map[Kind]int{IP: 20, User: 10, Email: 10}},
			Read:    {Period: time.Minute, Limits: map[Kind]int{IP: 1200, User: 600}},
			Write:   {Period: time.Minute, Limits: map[Kind]int{IP: 600, User: 300}},
			Export:  {Period: time.Hour, Limits: map[Kind]int{IP: 60, User: 30}},
			Connect: {Period: time.Minute, Limits: map[Kind]int{IP: 60, User: 30}},
		},
		Lockout: Lockout{Failures: 10, First: time.Minute, Max: 15 * time.Minute, Forget: 24 * time.Hour},
	}
}

// check returns what is wrong with p, or nil.
func (p Policy) check() error {
	for b, r := range p.Rates {
		if r.Period < time.Second || r.Period%time.Second != 0 {
			return fmt.Errorf("the bucket %s has the period %v, which is not a whole number of seconds", b, r.Period)
		}
		for k, n := range r.Limits {
			if n < 1 {
				return fmt.Errorf("the bucket %s allows a key of %s %d requests", b, k, n)
			}
		}
	}
	l := p.Lockout
	switch {
	case l.Failures < 2:
		return fmt.Errorf("the lockout follows %d failures, fewer than 2", l.Failures)
	case l.First <= 0 || l.Max < l.First:
		return fmt.Errorf("the lockout lasts from %v to %v", l.First, l.Max)
	case l.Forget <= l.Max:
		return fmt.Errorf("the lockout forgets failures after %v, no longer than its longest lock, %v", l.Forget,
			l.Max)
	}
	return nil
}

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Limiter counts requests and sign-in attempts in the database.
type Limiter struct {
	db     DB
	policy Policy

	mu sync.Mutex
	// secret is the key of the hash of the keys, which the database holds,
	// once it is read. Migrating generates it once, and nothing changes it.
	secret []byte
}

// New returns a Limiter that counts in db, with policy. It panics if the
// policy is not valid, which is a mistake in the code that calls it.
func New(db DB, policy Policy) *Limiter {
	if err := policy.check(); err != nil {
		panic("ratelimit: " + err.Error())
	}
	return &Limiter{db: db, policy: policy}
}

// Decision is the state of the key that a request is told about in its
// RateLimit headers.
type Decision struct {
	// Limit is how many requests the key may make in a window, and zero if
	// the request is told about no key.
	Limit int
	// Remaining is how many more the key may make in this window.
	Remaining int
	// Reset is how long until the window ends, and the key may make Limit
	// requests again.
	Reset time.Duration
}

// LimitError is the error of a request that a bucket refuses, because one
// of its keys has used up its limit in the current window.
type LimitError struct {
	Bucket Bucket
	// Decision is the state of the key that refused it.
	Decision Decision
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("ratelimit: the %s limit of %d requests is used up for %v", e.Bucket, e.Decision.Limit,
		e.Decision.Reset)
}

// RetryAfter is how long until the request would be allowed.
func (e *LimitError) RetryAfter() time.Duration {
	return e.Decision.Reset
}

// LockedError is the error of a sign-in with an email address that is
// locked, after too many failed sign-ins in a row.
type LockedError struct {
	// Wait is how long the address stays locked.
	Wait time.Duration
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("ratelimit: the email address is locked for %v after failed sign-ins", e.Wait)
}

// RetryAfter is how long until a sign-in with the address is allowed.
func (e *LockedError) RetryAfter() time.Duration {
	return e.Wait
}

// counted is a key that Take counts, with its hash and its limit.
type counted struct {
	Key
	hash  []byte
	limit int
}

// Take counts a request in bucket against each of keys, which must each be
// of a kind that the bucket limits, once even against a key given twice, and
// returns the state of the key that the request is told about: the one,
// among the keys that are not hidden, that has the fewest requests left, or
// none if every key is hidden. If one of the keys has used up its limit,
// Take returns a *LimitError, with the state of that key, whether or not it
// is hidden. It panics on a bucket or a key that the policy does not limit,
// which is a mistake in the code that calls it.
func (l *Limiter) Take(ctx context.Context, bucket Bucket, keys ...Key) (Decision, error) {
	rate, ok := l.policy.Rates[bucket]
	if !ok {
		panic(fmt.Sprintf("ratelimit: the bucket %q has no rate", bucket))
	}
	cs := make([]counted, len(keys))
	for i, k := range keys {
		limit, ok := rate.Limits[k.kind]
		if !ok {
			panic(fmt.Sprintf("ratelimit: the bucket %s does not limit a key of %s", bucket, k.kind))
		}
		cs[i] = counted{Key: k, limit: limit}
	}
	if len(cs) == 0 {
		return Decision{}, nil
	}
	var rows []queries.TakeRateLimitRow
	err := l.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		for i := range cs {
			var err error
			if cs[i].hash, err = l.hash(ctx, q, cs[i].Key); err != nil {
				return err
			}
		}
		// Two requests that share keys lock their rows in the same order. A
		// key given twice counts once, because one statement cannot change a
		// row twice.
		slices.SortFunc(cs, func(a, b counted) int { return bytes.Compare(a.hash, b.hash) })
		cs = slices.CompactFunc(cs, func(a, b counted) bool { return bytes.Equal(a.hash, b.hash) })
		hashes := make([][]byte, len(cs))
		for i, c := range cs {
			hashes[i] = c.hash
		}
		// Every request of a key counts in the key's one row, so the requests
		// of a key wait for each other's commits. The commit need not wait for
		// the disk: a crash of the database would lose only a few requests'
		// counts, and a limit lets a few more requests through then.
		if err := q.CommitWithoutFlush(ctx); err != nil {
			return fmt.Errorf("ratelimit: commit without waiting for the disk: %w", err)
		}
		var err error
		rows, err = q.TakeRateLimit(ctx, queries.TakeRateLimitParams{Bucket: string(bucket),
			Period: interval(rate.Period), Keys: hashes})
		if err != nil {
			return fmt.Errorf("ratelimit: count the request: %w", err)
		}
		return nil
	})
	if err != nil {
		return Decision{}, err
	}
	var told, refused *Decision
	for _, c := range cs {
		i := slices.IndexFunc(rows, func(r queries.TakeRateLimitRow) bool { return bytes.Equal(r.Key, c.hash) })
		if i < 0 {
			return Decision{}, errors.New("ratelimit: a key was not counted")
		}
		d := Decision{Limit: c.limit, Remaining: max(0, c.limit-int(rows[i].Hits)),
			Reset: rows[i].WindowEnd.Time.Sub(rows[i].Now.Time)}
		if int(rows[i].Hits) > c.limit && (refused == nil || d.Reset > refused.Reset) {
			refused = &d
		}
		if !c.hidden && (told == nil || cmp.Or(cmp.Compare(d.Remaining, told.Remaining),
			cmp.Compare(told.Reset, d.Reset)) < 0) {
			told = &d
		}
	}
	if refused != nil {
		return *refused, &LimitError{Bucket: bucket, Decision: *refused}
	}
	if told == nil {
		return Decision{}, nil
	}
	return *told, nil
}

// Attempt is a sign-in attempt, which counts as a failure until Succeeded
// says otherwise.
type Attempt struct {
	hash []byte
}

// BeginLogin counts a sign-in attempt with email, as package account holds
// it (EmailAddress), as a failure, and returns the attempt, unless the
// address is locked, when it returns a *LockedError, and counts nothing. The
// attempt that makes the failures in a row reach Lockout.Failures locks the
// address, as do those after it, each once the lock before it ends.
func (l *Limiter) BeginLogin(ctx context.Context, email string) (Attempt, error) {
	lockout := l.policy.Lockout
	var attempt Attempt
	var wait time.Duration
	err := l.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		hash, err := l.hash(ctx, q, EmailAddress(email))
		if err != nil {
			return err
		}
		_, err = q.BeginLoginAttempt(ctx, queries.BeginLoginAttemptParams{Key: hash, Forget: interval(lockout.Forget),
			Threshold: int32(lockout.Failures), FirstLock: interval(lockout.First), MaxLock: interval(lockout.Max)})
		switch {
		case err == nil:
			attempt.hash = hash
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("ratelimit: count the sign-in attempt: %w", err)
		}
		// The address is locked.
		remaining, err := q.LoginLockedFor(ctx, hash)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("ratelimit: read the lock: %w", err)
		}
		// A lock that another server's sign-in ended or cleared meanwhile
		// still refuses this one, which may try again at once.
		wait = time.Second
		if remaining.Valid {
			wait = max(wait, duration(remaining))
		}
		return nil
	})
	switch {
	case err != nil:
		return Attempt{}, err
	case attempt.hash == nil:
		return Attempt{}, &LockedError{Wait: wait}
	}
	return attempt, nil
}

// Succeeded forgets the failures of the attempt's address, in tx, which
// starts the session of the sign-in, so that its failures count from 1
// again (FR-AUTH-05 AC3).
func (a Attempt) Succeeded(ctx context.Context, tx pgx.Tx) error {
	if err := queries.New(tx).ClearLoginAttempts(ctx, a.hash); err != nil {
		return fmt.Errorf("ratelimit: forget the failed sign-ins: %w", err)
	}
	return nil
}

// hash returns the keyed hash that the database holds of k: HMAC-SHA256,
// with the rate_limit key of the database, of the key's kind and value. An
// email address is folded to lower case by the database first, as it
// compares addresses, except one with a NUL character, which no text in
// PostgreSQL can hold, and which no account has, which Go folds instead.
func (l *Limiter) hash(ctx context.Context, q *queries.Queries, k Key) ([]byte, error) {
	secret, err := l.key(ctx, q)
	if err != nil {
		return nil, err
	}
	value := k.value
	if k.kind == Email {
		if strings.ContainsRune(value, 0) {
			value = strings.ToLower(value)
		} else if value, err = q.FoldEmail(ctx, value); err != nil {
			return nil, fmt.Errorf("ratelimit: fold the email address: %w", err)
		}
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(k.kind))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return mac.Sum(nil), nil
}

// key returns the key of the hash, which it reads from the database the
// first time. It reads it without holding l.mu, so that a request whose read
// is slow keeps no other request waiting on the mutex, past the end of that
// request's context; requests that read it at once each read the same key.
func (l *Limiter) key(ctx context.Context, q *queries.Queries) ([]byte, error) {
	l.mu.Lock()
	secret := l.secret
	l.mu.Unlock()
	if secret != nil {
		return secret, nil
	}
	secret, err := q.RateLimitKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("ratelimit: read the key of the hash: %w", err)
	}
	l.mu.Lock()
	l.secret = secret
	l.mu.Unlock()
	return secret, nil
}

// interval returns d as an interval column.
func interval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}

// duration returns the length of an interval that subtracting two
// timestamps gives, which PostgreSQL writes as days, of 24 hours each, and
// the time beyond them, but never as months.
func duration(i pgtype.Interval) time.Duration {
	return time.Duration(i.Days)*24*time.Hour + time.Duration(i.Microseconds)*time.Microsecond
}
