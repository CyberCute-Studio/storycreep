package ratelimit

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// testPolicy is the server's policy, but with small limits of the read
// bucket, in windows of an hour, which a test does not outlast.
func testPolicy() Policy {
	p := DefaultPolicy()
	p.Rates[Read] = Rate{Period: time.Hour, Limits: map[Kind]int{IP: 3, User: 2}}
	p.Rates[Auth] = Rate{Period: time.Hour, Limits: map[Kind]int{IP: 20, User: 10, Email: 2}}
	return p
}

// newDatabase returns a new database, once the hour that the test policy's
// windows count in, by the database's clock, which they count with, has a
// minute left at least, so that no window of the test policy ends during the
// test. It waits for the next hour otherwise.
func newDatabase(t *testing.T) *dbtest.Database {
	t.Helper()
	d := dbtest.New(t)
	d.AwaitWindow(t, time.Hour, time.Minute)
	return d
}

// setup returns a Limiter with testPolicy on a new database (newDatabase),
// the database's pool, and a connection to it as the superuser.
func setup(t *testing.T) (*Limiter, *database.DB, *pgx.Conn) {
	t.Helper()
	d := newDatabase(t)
	db := d.Open(t)
	return New(db, testPolicy()), db, d.Admin(t)
}

// twoServers returns two Limiters with testPolicy on a new database
// (newDatabase), each on a pool of its own, as two servers of an instance
// count, and a connection to the database as the superuser.
func twoServers(t *testing.T) (*Limiter, *Limiter, *pgx.Conn) {
	t.Helper()
	d := newDatabase(t)
	return New(d.Open(t), testPolicy()), New(d.Open(t), testPolicy()), d.Admin(t)
}

// exec runs sql as the superuser.
func exec(t *testing.T, admin *pgx.Conn, sql string, args ...any) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// count returns the number that a query counts, as the superuser.
func count(t *testing.T, admin *pgx.Conn, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestDefaultPolicy checks the server's limits, which docs/error-codes.md,
// docs/configuration.md, and the descriptions of the API's operations state:
// a lock after 10 failed sign-ins in a row, for a minute, doubling up to 15
// minutes, with failures forgotten after a day, 20 requests a minute of the
// auth bucket for an IP address, and 10 for an account and an email address,
// and 10 invitation emails an hour to an email address, which nothing else
// limits.
//
// Verifies: NFR-SEC-06
func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()
	if err := p.check(); err != nil {
		t.Fatal(err)
	}
	if want := (Lockout{Failures: 10, First: time.Minute, Max: 15 * time.Minute, Forget: 24 * time.Hour}); p.Lockout !=
		want {
		t.Errorf("the lockout is %+v, want %+v", p.Lockout, want)
	}
	auth := p.Rates[Auth]
	if auth.Period != time.Minute || auth.Limits[IP] != 20 || auth.Limits[User] != 10 || auth.Limits[Email] != 10 {
		t.Errorf("the auth bucket is %+v", auth)
	}
	invites := p.Rates[InviteEmail]
	if invites.Period != time.Hour || len(invites.Limits) != 1 || invites.Limits[Email] != 10 {
		t.Errorf("the invite_email bucket is %+v", invites)
	}
	for _, b := range []Bucket{Auth, Read, Write, Export, Connect} {
		r, ok := p.Rates[b]
		if !ok || r.Limits[IP] == 0 || r.Limits[User] == 0 {
			t.Errorf("the bucket %s does not limit IP addresses and users: %+v", b, r)
		}
	}
}

// TestPolicyCheck checks that New refuses a policy that cannot work.
func TestPolicyCheck(t *testing.T) {
	for name, change := range map[string]func(*Policy){
		"a period under a second":    func(p *Policy) { p.Rates[Read] = Rate{Period: time.Millisecond} },
		"a period of a fraction":     func(p *Policy) { p.Rates[Read] = Rate{Period: 1500 * time.Millisecond} },
		"a limit of none":            func(p *Policy) { p.Rates[Read] = Rate{Period: time.Minute, Limits: map[Kind]int{IP: 0}} },
		"a lock after one failure":   func(p *Policy) { p.Lockout.Failures = 1 },
		"a lock of no time":          func(p *Policy) { p.Lockout.First = 0 },
		"a longest lock too short":   func(p *Policy) { p.Lockout.Max = p.Lockout.First / 2 },
		"failures forgotten in lock": func(p *Policy) { p.Lockout.Forget = p.Lockout.Max },
	} {
		p := DefaultPolicy()
		change(&p)
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: New did not panic", name)
				}
			}()
			New(nil, p)
		}()
	}
}

// TestTake checks that a request counts against each of its keys, once
// against a key given twice, is refused once one of them has used up its
// limit, with the state of that key and how long to wait, and tells the
// client about the key with the fewest requests left, which is never an
// email address unless it refuses the request. The servers of an instance
// count in the same rows, and so do the spellings of one email address and
// the addresses of one IPv6 /64.
//
// Verifies: FR-API-04/AC3, FR-AUTH-05/AC2, NFR-SEC-06
func TestTake(t *testing.T) {
	t.Parallel()
	// other is a second server of the instance, on its own pool.
	l, other, _ := twoServers(t)
	ctx := context.Background()
	ip := ClientIP(netip.MustParseAddr("203.0.113.7"))
	jo, mo := UserID(ids.New()), UserID(ids.New())

	d, err := l.Take(ctx, Read, ip, jo)
	if err != nil || d.Limit != 2 || d.Remaining != 1 || d.Reset <= 0 || d.Reset > time.Hour {
		t.Fatalf("the first request: %+v, %v; want the user's limit of 2 with 1 left", d, err)
	}
	if d, err = other.Take(ctx, Read, ip, jo); err != nil || d.Limit != 2 || d.Remaining != 0 {
		t.Fatalf("the second request, on the other server: %+v, %v; want none left", d, err)
	}
	_, err = l.Take(ctx, Read, ip, jo)
	var limited *LimitError
	if !errors.As(err, &limited) || limited.Bucket != Read || limited.Decision.Limit != 2 ||
		limited.Decision.Remaining != 0 || limited.RetryAfter() <= 0 || limited.RetryAfter() > time.Hour {
		t.Fatalf("the third request: %v; want the user's limit used up", err)
	}
	// The IP address has made 3 requests, its limit, so another user's
	// request from it is refused for the address's sake.
	if _, err := other.Take(ctx, Read, ip, mo); !errors.As(err, &limited) || limited.Decision.Limit != 3 {
		t.Errorf("another user from the address: %v; want the address's limit used up", err)
	}
	// Each bucket counts on its own.
	if d, err := l.Take(ctx, Auth, ip, jo); err != nil || d.Limit != 10 || d.Remaining != 9 {
		t.Errorf("the auth bucket: %+v, %v; want the user's limit of 10 with 9 left", d, err)
	}
	// A key given twice counts once.
	twice := UserID(ids.New())
	if d, err := l.Take(ctx, Auth, twice, twice); err != nil || d.Limit != 10 || d.Remaining != 9 {
		t.Errorf("a key given twice: %+v, %v; want it counted once, with 9 left", d, err)
	}

	// The addresses of one /64 count together, and another /64 apart.
	near := ClientIP(netip.MustParseAddr("2001:db8:1:2::1"))
	far := ClientIP(netip.MustParseAddr("2001:db8:1:3::1"))
	if near != ClientIP(netip.MustParseAddr("2001:db8:1:2:ffff:ffff:ffff:ffff")) || near == far {
		t.Errorf("the keys of IPv6 addresses: %+v and %+v", near, far)
	}
	if ClientIP(netip.MustParseAddr("::ffff:203.0.113.7")) != ip {
		t.Error("an IPv4 address written as an IPv6 one is another key")
	}

	// An email address is hidden from the client, until it refuses a request,
	// and its spellings are one key.
	if d, err := l.Take(ctx, Auth, EmailAddress("Jo@Example.com")); err != nil || d != (Decision{}) {
		t.Errorf("an email address: %+v, %v; want nothing to tell", d, err)
	}
	if d, err := l.Take(ctx, Auth, ClientIP(netip.MustParseAddr("198.51.100.1")),
		EmailAddress("jo@example.COM")); err != nil || d.Limit != 20 || d.Remaining != 19 {
		t.Errorf("an email address with an IP address: %+v, %v; want the address's limit", d, err)
	}
	if _, err := l.Take(ctx, Auth, EmailAddress("JO@EXAMPLE.COM")); !errors.As(err, &limited) ||
		limited.Bucket != Auth || limited.Decision.Limit != 2 || limited.Decision.Remaining != 0 {
		t.Errorf("a third spelling of the address: %v; want its limit of 2 used up", err)
	}
	// An address that PostgreSQL cannot hold is still counted.
	if _, err := l.Take(ctx, Auth, EmailAddress("jo\x00@example.com")); err != nil {
		t.Errorf("an address with a NUL character: %v", err)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("taking an email address in the read bucket did not panic")
			}
		}()
		_, _ = l.Take(ctx, Read, EmailAddress("jo@example.com"))
	}()
}

// concurrently makes n calls of try at once, the even ones with first and the
// odd ones with second, and returns how many returned nil and how many
// returned an error that refused reports as a refusal. It fails the test on
// any other error.
func concurrently(t *testing.T, n int, first, second *Limiter, try func(*Limiter, int) error,
	refused func(error) bool) (allowed, refusals int) {
	t.Helper()
	var ok, no atomic.Int32
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			l := first
			if i%2 == 1 {
				l = second
			}
			switch err := try(l, i); {
			case err == nil:
				ok.Add(1)
			case refused(err):
				no.Add(1)
			default:
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a call failed: %v", err)
	}
	return int(ok.Load()), int(no.Load())
}

// TestTakeConcurrent checks that the requests of one key made at once, on
// two servers, are counted one by one, so that exactly the key's limit of
// them is allowed, and that requests that share keys, which they give in
// either order, do not deadlock, because each locks the keys' rows in the
// same order.
//
// Verifies: FR-AUTH-05/AC2, NFR-SEC-06
func TestTakeConcurrent(t *testing.T) {
	t.Parallel()
	l, other, _ := twoServers(t)
	ip, jo := ClientIP(netip.MustParseAddr("203.0.113.7")), UserID(ids.New())
	const requests = 12
	allowed, refused := concurrently(t, requests, l, other, func(s *Limiter, i int) error {
		keys := []Key{ip, jo}
		if i%4 >= 2 {
			keys = []Key{jo, ip}
		}
		_, err := s.Take(context.Background(), Read, keys...)
		return err
	}, func(err error) bool {
		var limited *LimitError
		return errors.As(err, &limited)
	})
	if allowed != 2 || refused != requests-2 {
		t.Errorf("%d requests allowed and %d refused, want the user's limit of 2 allowed", allowed, refused)
	}
}

// TestTakeNewWindow checks that a key whose window has ended counts from
// one again in its next window.
//
// Verifies: NFR-SEC-06
func TestTakeNewWindow(t *testing.T) {
	t.Parallel()
	l, _, admin := setup(t)
	ctx := context.Background()
	jo := UserID(ids.New())
	for range 2 {
		if _, err := l.Take(ctx, Read, jo); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.Take(ctx, Read, jo); err == nil {
		t.Fatal("the third request in the window was allowed")
	}
	exec(t, admin, `UPDATE rate_limit SET window_start = window_start - interval '1 hour',
		window_end = window_end - interval '1 hour'`)
	if d, err := l.Take(ctx, Read, jo); err != nil || d.Remaining != 1 {
		t.Errorf("the first request of the next window: %+v, %v; want 1 left", d, err)
	}
}

// TestTakeHoldsNoAddress checks that the counters hold no IP address, user
// ID, or email address as it is, but a keyed hash of each.
func TestTakeHoldsNoAddress(t *testing.T) {
	t.Parallel()
	l, _, admin := setup(t)
	id := ids.New()
	if _, err := l.Take(context.Background(), Auth, ClientIP(netip.MustParseAddr("203.0.113.7")), UserID(id),
		EmailAddress("jo@example.com")); err != nil {
		t.Fatal(err)
	}
	if n := count(t, admin, `SELECT count(*) FROM rate_limit WHERE bucket = 'auth'`); n != 3 {
		t.Fatalf("%d counters, want 3", n)
	}
	for _, v := range []string{"203.0.113.7", id.String(), "jo@example.com"} {
		if n := count(t, admin, `SELECT count(*) FROM rate_limit WHERE position(convert_to($1, 'UTF8') IN key) > 0`,
			v); n != 0 {
			t.Errorf("a counter holds %s", v)
		}
	}
}

// TestTakeCommitsWithoutFlush checks that the statement that counts a
// request also makes its transaction commit without waiting for the disk,
// so that it costs no round trip of its own. The test's database server
// waits for no commit at all, so the transaction asks it to wait first.
func TestTakeCommitsWithoutFlush(t *testing.T) {
	t.Parallel()
	_, db, _ := setup(t)
	ctx := context.Background()
	var setting string
	if err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL synchronous_commit = on"); err != nil {
			return err
		}
		if _, err := queries.New(tx).TakeRateLimit(ctx, queries.TakeRateLimitParams{Bucket: string(Read),
			Period: database.Interval(time.Hour), Keys: [][]byte{make([]byte, 32)}}); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT current_setting('synchronous_commit')").Scan(&setting)
	}); err != nil {
		t.Fatal(err)
	}
	if setting != "off" {
		t.Errorf("synchronous_commit is %s once a request is counted, want off", setting)
	}
}

// TestTakeTx checks that a request counted in a transaction of the caller's
// counts only if the transaction commits, and leaves the transaction's
// commit waiting for the disk, as the caller's other changes need. The
// test's database server waits for no commit at all, so the transaction asks
// it to wait first.
//
// Verifies: NFR-SEC-06
func TestTakeTx(t *testing.T) {
	t.Parallel()
	l, db, _ := setup(t)
	ctx := context.Background()
	jo := UserID(ids.New())
	rollBack := errors.New("roll back")
	var setting string
	err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL synchronous_commit = on"); err != nil {
			return err
		}
		if d, err := l.TakeTx(ctx, tx, Read, jo); err != nil || d.Limit != 2 || d.Remaining != 1 {
			t.Errorf("the request counted in the transaction: %+v, %v; want the user's limit of 2 with 1 left", d,
				err)
		}
		if err := tx.QueryRow(ctx, "SELECT current_setting('synchronous_commit')").Scan(&setting); err != nil {
			return err
		}
		return rollBack
	})
	if !errors.Is(err, rollBack) {
		t.Fatal(err)
	}
	if setting != "on" {
		t.Errorf("synchronous_commit is %s once a request is counted in the caller's transaction, want on", setting)
	}
	if d, err := l.Take(ctx, Read, jo); err != nil || d.Remaining != 1 {
		t.Errorf("a request after the transaction rolled back: %+v, %v; want 1 left, the other uncounted", d, err)
	}
	if err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		_, err := l.TakeTx(ctx, tx, Read, jo)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var limited *LimitError
	if _, err := l.Take(ctx, Read, jo); !errors.As(err, &limited) || limited.Decision.Limit != 2 {
		t.Errorf("a request after the transaction committed: %v; want the user's limit of 2 used up", err)
	}
}

// TestTurn checks that the requests that a key's limit refuses take turns in
// the windows that follow, as many in each as the limit allows, in the order
// in which they were refused, while each could be retried once the window
// ends.
//
// Verifies: NFR-SEC-06
func TestTurn(t *testing.T) {
	t.Parallel()
	l, _, _ := setup(t)
	ctx := context.Background()
	jo := UserID(ids.New())
	for range 2 {
		if _, err := l.Take(ctx, Read, jo); err != nil {
			t.Fatal(err)
		}
	}
	// The user's limit is 2 an hour, so the first two requests that it
	// refuses wait for this hour to end, the next two for the next, and the
	// fifth for the one after.
	for i, after := range []time.Duration{0, 0, time.Hour, time.Hour, 2 * time.Hour} {
		_, err := l.Take(ctx, Read, jo)
		var limited *LimitError
		if !errors.As(err, &limited) {
			t.Fatalf("request %d over the limit: %v; want it refused", i+1, err)
		}
		if turn := limited.Turn(); turn <= after || turn > after+time.Hour || limited.RetryAfter() > time.Hour {
			t.Errorf("request %d over the limit: its turn is in %v, and it may be retried in %v; want its turn "+
				"after %v and within an hour of then, and a retry within the hour", i+1, turn, limited.RetryAfter(),
				after)
		}
	}
}

// failLogins begins n sign-in attempts with email, none of which succeeds,
// and fails the test if one is refused.
func failLogins(t *testing.T, l *Limiter, email string, n int) {
	t.Helper()
	for i := range n {
		if _, err := l.BeginLogin(context.Background(), email); err != nil {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
}

// lockedFor returns how long the address of email is locked, as the
// superuser reads it, or zero.
func lockedFor(t *testing.T, admin *pgx.Conn) time.Duration {
	t.Helper()
	var seconds float64
	if err := admin.QueryRow(context.Background(), `SELECT coalesce(max(extract(epoch FROM locked_until - now())), 0)
		FROM login_attempt`).Scan(&seconds); err != nil {
		t.Fatal(err)
	}
	return time.Duration(seconds * float64(time.Second))
}

// TestLockout checks that the 10th failed sign-in in a row with an address
// locks it for a minute, during which the 11th is refused, whoever makes it
// and whatever its password, that the servers of an instance count the
// same failures, and that the spellings of an address are one address.
//
// Verifies: FR-AUTH-05/AC1, FR-AUTH-05/AC2
func TestLockout(t *testing.T) {
	t.Parallel()
	l, other, admin := twoServers(t)
	failLogins(t, l, "jo@example.com", 5)
	failLogins(t, other, "Jo@Example.com", 4)
	if lockedFor(t, admin) != 0 {
		t.Fatal("9 failures locked the address")
	}
	failLogins(t, l, "JO@example.com", 1)
	if d := lockedFor(t, admin); d < 50*time.Second || d > time.Minute {
		t.Fatalf("the 10th failure locks the address for %v, want a minute", d)
	}
	for _, s := range []*Limiter{l, other} {
		_, err := s.BeginLogin(context.Background(), "jo@example.com")
		var locked *LockedError
		if !errors.As(err, &locked) || locked.RetryAfter() < 50*time.Second || locked.RetryAfter() > time.Minute {
			t.Errorf("the 11th attempt: %v; want it locked for a minute", err)
		}
	}
	if n := count(t, admin, `SELECT failures FROM login_attempt`); n != 10 {
		t.Errorf("%d failures, want the 10 before the lock", n)
	}
	// Another address is not locked.
	failLogins(t, l, "mo@example.com", 1)
}

// TestLockoutConcurrent checks that sign-in attempts with an address made
// at once, on two servers, as an attacker makes them, are counted one by
// one: the 10 that reach the lock are let through, and every other one is
// refused.
//
// Verifies: FR-AUTH-05/AC1, FR-AUTH-05/AC2
func TestLockoutConcurrent(t *testing.T) {
	t.Parallel()
	l, other, admin := twoServers(t)
	const attempts = 16
	begun, refused := concurrently(t, attempts, l, other, func(s *Limiter, _ int) error {
		_, err := s.BeginLogin(context.Background(), "jo@example.com")
		return err
	}, func(err error) bool {
		var locked *LockedError
		return errors.As(err, &locked)
	})
	if begun != 10 || refused != attempts-10 {
		t.Errorf("%d attempts begun and %d refused, want 10 begun", begun, refused)
	}
	if n := count(t, admin, `SELECT failures FROM login_attempt`); n != 10 {
		t.Errorf("%d failures, want 10", n)
	}
}

// TestLockoutLongerThanADay checks that the wait of a lock of a day or more
// is the whole of it, although PostgreSQL writes the time left of it in days
// and the hours beyond them.
func TestLockoutLongerThanADay(t *testing.T) {
	t.Parallel()
	_, db, _ := setup(t)
	policy := testPolicy()
	policy.Lockout = Lockout{Failures: 2, First: 30 * time.Hour, Max: 30 * time.Hour, Forget: 48 * time.Hour}
	l := New(db, policy)
	failLogins(t, l, "jo@example.com", 2)
	_, err := l.BeginLogin(context.Background(), "jo@example.com")
	var locked *LockedError
	if !errors.As(err, &locked) || locked.RetryAfter() < 30*time.Hour-time.Minute || locked.RetryAfter() > 30*time.Hour {
		t.Errorf("the attempt after the lock: %v; want it locked for 30 hours", err)
	}
}

// TestLockoutBackoff checks that each failure after a lock ends locks the
// address for twice as long as the lock before, up to 15 minutes, and that
// failures more than a day apart count from 1 again.
//
// Verifies: FR-AUTH-05/AC1
func TestLockoutBackoff(t *testing.T) {
	t.Parallel()
	l, _, admin := setup(t)
	failLogins(t, l, "jo@example.com", 10)
	for _, want := range []time.Duration{2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute,
		15 * time.Minute} {
		exec(t, admin, `UPDATE login_attempt SET locked_until = now() - interval '1 second'`)
		failLogins(t, l, "jo@example.com", 1)
		if d := lockedFor(t, admin); d < want-10*time.Second || d > want {
			t.Errorf("the lock after it ends is %v, want %v", d, want)
		}
	}
	exec(t, admin, `UPDATE login_attempt SET locked_until = NULL, last_attempt_at = now() - interval '25 hours'`)
	failLogins(t, l, "jo@example.com", 1)
	if n := count(t, admin, `SELECT failures FROM login_attempt`); n != 1 || lockedFor(t, admin) != 0 {
		t.Errorf("a failure a day after the last: %d failures, locked for %v; want 1, unlocked", n,
			lockedFor(t, admin))
	}
}

// TestLoginSucceeded checks that a sign-in that succeeds forgets the
// failures of its address, in the transaction that it is told in, so that
// 9 more fail before the address is locked.
//
// Verifies: FR-AUTH-05/AC3
func TestLoginSucceeded(t *testing.T) {
	t.Parallel()
	l, db, admin := setup(t)
	ctx := context.Background()
	succeed := func(rollBack bool) {
		t.Helper()
		attempt, err := l.BeginLogin(ctx, "jo@example.com")
		if err != nil {
			t.Fatal(err)
		}
		errRollBack := errors.New("roll back")
		if err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
			if err := attempt.Succeeded(ctx, tx); err != nil {
				return err
			}
			if rollBack {
				return errRollBack
			}
			return nil
		}); err != nil && !errors.Is(err, errRollBack) {
			t.Fatal(err)
		}
	}
	failLogins(t, l, "jo@example.com", 8)
	// A sign-in whose session did not start still counts.
	succeed(true)
	if n := count(t, admin, `SELECT failures FROM login_attempt`); n != 9 {
		t.Fatalf("%d failures, want 9", n)
	}
	succeed(false)
	if n := count(t, admin, `SELECT count(*) FROM login_attempt`); n != 0 {
		t.Fatalf("%d attempts kept, want none", n)
	}
	failLogins(t, l, "jo@example.com", 9)
	if lockedFor(t, admin) != 0 {
		t.Error("9 failures after a success locked the address")
	}
	// An address that PostgreSQL cannot hold is counted too.
	failLogins(t, l, "jo\x00@example.com", 1)
}

// TestCleanUp checks that the cleanup deletes the counters whose window has
// ended and the attempts that are forgotten, and keeps the others.
func TestCleanUp(t *testing.T) {
	t.Parallel()
	l, db, admin := setup(t)
	ctx := context.Background()
	if _, err := l.Take(ctx, Read, UserID(ids.New())); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Take(ctx, Read, UserID(ids.New())); err != nil {
		t.Fatal(err)
	}
	failLogins(t, l, "jo@example.com", 1)
	failLogins(t, l, "mo@example.com", 1)
	exec(t, admin, `UPDATE rate_limit SET window_start = now() - interval '2 hours', window_end = now() - interval
		'1 hour' WHERE ctid = (SELECT min(ctid) FROM rate_limit)`)
	exec(t, admin, `UPDATE login_attempt SET last_attempt_at = now() - interval '25 hours'
		WHERE ctid = (SELECT min(ctid) FROM login_attempt)`)
	n, err := l.cleanUp(ctx, db)
	if err != nil || n != 2 {
		t.Fatalf("cleanUp = %d, %v; want 2 rows deleted", n, err)
	}
	if c, a := count(t, admin, `SELECT count(*) FROM rate_limit`), count(t, admin,
		`SELECT count(*) FROM login_attempt`); c != 1 || a != 1 {
		t.Errorf("%d counters and %d attempts kept, want 1 of each", c, a)
	}
}

// TestCleanUpSkipsHeldRows checks that the cleanup leaves alone a row that a
// request holds until it commits, as one does that counts a hit in a new
// window of an ended counter, or a sign-in with an address whose failures
// were forgotten: the cleanup neither waits for the request, with which it
// could deadlock, nor deletes the row once the request has revived it.
func TestCleanUpSkipsHeldRows(t *testing.T) {
	t.Parallel()
	l, db, admin := setup(t)
	ctx := context.Background()
	if _, err := l.Take(ctx, Read, UserID(ids.New())); err != nil {
		t.Fatal(err)
	}
	failLogins(t, l, "jo@example.com", 1)
	// waiting reports whether a transaction on the test's database waits for
	// a lock, as the cleanup would for the request's row. pg_locks holds the
	// locks of every database on the server, those of the tests that run
	// alongside this one too, and a wait for a row is a wait for the
	// transaction that holds it, which belongs to no database, so the waits
	// are told apart by the database of the session that waits.
	waiting := func() bool {
		var n int
		if err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
				WHERE NOT l.granted AND a.datname = current_database()`).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n > 0
	}
	for _, c := range []struct {
		what string
		// end makes the table's row ended or forgotten, revive is what the
		// request does to it, and revived counts the row once it is revived.
		end, revive, revived string
	}{
		{
			"a counter",
			`UPDATE rate_limit SET window_start = now() - interval '2 hours', window_end = now() - interval '1 hour'`,
			`UPDATE rate_limit SET window_start = now(), window_end = now() + interval '1 hour', hits = 1`,
			`SELECT count(*) FROM rate_limit WHERE window_end > now()`,
		},
		{
			"a sign-in attempt",
			`UPDATE login_attempt SET last_attempt_at = now() - interval '25 hours'`,
			`UPDATE login_attempt SET failures = 1, last_attempt_at = now()`,
			`SELECT count(*) FROM login_attempt WHERE last_attempt_at > now() - interval '1 hour'`,
		},
	} {
		func() {
			exec(t, admin, c.end)
			tx, err := admin.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, c.revive); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := l.cleanUp(ctx, db)
				done <- err
			}()
			var cleaned error
			finished := false
			for deadline := time.Now().Add(time.Minute); !finished; time.Sleep(10 * time.Millisecond) {
				select {
				case cleaned = <-done:
					finished = true
					continue
				default:
				}
				if waiting() {
					t.Errorf("%s: the cleanup waits for the request that holds it", c.what)
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s: the cleanup did not finish", c.what)
				}
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if !finished {
				cleaned = <-done
			}
			if cleaned != nil {
				t.Fatal(cleaned)
			}
			if n := count(t, admin, c.revived); n != 1 {
				t.Errorf("%s: the cleanup deleted the row that the request revived", c.what)
			}
		}()
	}
}
