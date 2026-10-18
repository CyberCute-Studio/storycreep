package workspace

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

// newCappedService returns a Service whose instance caps every workspace's
// members at members, as STORYCREEP_WORKSPACE_LIMITS does, on a database of
// its own, and the database.
func newCappedService(t *testing.T, members int64) (*Service, *dbtest.Database) {
	t.Helper()
	d := dbtest.New(t)
	return New(d.Open(t), Options{PlanKey: entitlements.Unlimited, DefaultLocale: language.English,
		Entitlements: entitlements.New(entitlements.Options{Caps: entitlements.Caps{entitlements.Members: members},
			Meter: usage.Meter{}})}), d
}

// join makes the user a member of the workspace with the role, as accepting
// an invitation does.
func (s *Service) join(t *testing.T, ws, user ids.UUID, r role.Role) error {
	t.Helper()
	ctx := context.Background()
	return s.db.InTx(ctx, database.Scope{WorkspaceID: ws, UserID: user}, func(tx pgx.Tx) error {
		_, err := s.Join(ctx, tx, ws, user, r, ids.UUID{}, ids.New(), audit.Client{})
		return err
	})
}

// wantLimit fails the test unless err is the members limit's, reached at
// limit by a workspace that uses that many.
func wantLimit(t *testing.T, what string, err error, limit int64) {
	t.Helper()
	var l *entitlements.LimitError
	if !errors.As(err, &l) || l.Key != entitlements.Members || l.Usage != limit || l.Limit != limit {
		t.Errorf("%s: %v, want the members limit of %d reached at %d", what, err, limit, limit)
	}
}

// TestMembersLimit checks that every path that adds a member stops at the
// workspace's members limit, with an error that names the limit and the
// usage, and adds nothing then: the operator's Grant and an invitation's
// acceptance (Join). A role change adds no member, nor does an acceptance by
// a member, who is told so, and a removal makes room again.
//
// Verifies: FR-ENT-01/AC1
func TestMembersLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d := newCappedService(t, 3)
	owner := addUser(t, d, "Owner")
	w, err := s.Create(ctx, owner, Settings{Name: "Harbour"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	second, third := addUser(t, d, "Second"), addUser(t, d, "Third")
	if _, err := s.Grant(ctx, w.ID, second, role.Editor); err != nil {
		t.Fatal(err)
	}
	if err := s.join(t, w.ID, third, role.Viewer); err != nil {
		t.Fatal(err)
	}
	fourth := addUser(t, d, "Fourth")
	_, err = s.Grant(ctx, w.ID, fourth, role.Editor)
	wantLimit(t, "Grant past the limit", err, 3)
	wantLimit(t, "Join past the limit", s.join(t, w.ID, fourth, role.Viewer), 3)
	if n := count(t, d, "SELECT count(*) FROM membership WHERE workspace_id = $1", w.ID); n != 3 {
		t.Errorf("the workspace has %d members, want 3", n)
	}
	if _, err := s.Grant(ctx, w.ID, second, role.Admin); err != nil {
		t.Errorf("a role change at the limit: %v", err)
	}
	if err := s.join(t, w.ID, third, role.Editor); !errors.Is(err, ErrAlreadyMember) {
		t.Errorf("a member's acceptance at the limit: %v, want ErrAlreadyMember", err)
	}

	ms, err := s.Members(ctx, owner, w.ID, ids.UUID{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.UserID == third {
			if err := s.RemoveMember(ctx, owner, w.ID, m.MembershipID, audit.Client{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.join(t, w.ID, fourth, role.Viewer); err != nil {
		t.Errorf("Join once a member has left: %v", err)
	}
}

// TestMembersLimitTakesTurns checks that two members added at once to a
// workspace with room for one are not both let in: the second waits for the
// first, and then counts it (FR-ENT-02, exact counting). They accept
// invitations (Join), which, unlike Grant, lock no row that the other needs,
// so only the lock that the members limit's check holds makes them take
// turns, and the first stays open until the second waits for that lock. A
// user who accepts twice at once is told the second time that they are a
// member already, rather than that the workspace, which their first
// acceptance filled, is full.
//
// Verifies: FR-ENT-01/AC1
func TestMembersLimitTakesTurns(t *testing.T) {
	t.Parallel()
	for _, sameUser := range []bool{false, true} {
		ctx := context.Background()
		s, d := newCappedService(t, 2)
		w, err := s.Create(ctx, addUser(t, d, "Owner"), Settings{Name: "Harbour"}, audit.Client{})
		if err != nil {
			t.Fatal(err)
		}
		first, second := addUser(t, d, "First"), addUser(t, d, "Second")
		if sameUser {
			second = first
		}
		joined, stop, firstDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		release := sync.OnceFunc(func() { close(stop) })
		t.Cleanup(release)
		go func() {
			firstDone <- s.db.InTx(ctx, database.Scope{WorkspaceID: w.ID, UserID: first}, func(tx pgx.Tx) error {
				if _, err := s.Join(ctx, tx, w.ID, first, role.Editor, ids.UUID{}, ids.New(),
					audit.Client{}); err != nil {
					return err
				}
				close(joined)
				<-stop
				return nil
			})
		}()
		select {
		case <-joined:
		case err := <-firstDone:
			t.Fatalf("the first acceptance: %v", err)
		}
		// waits returns how many sessions wait for a lock of usage in the
		// test's own database, of the many that pg_locks shows, one for each
		// test that runs at once.
		admin := d.Admin(t)
		waits := func() int {
			var n int
			if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND "+
				"NOT granted AND classid = $1 AND database = (SELECT oid FROM pg_database WHERE datname = "+
				"current_database())", database.UsageLock).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		secondDone := make(chan error, 1)
		go func() { secondDone <- s.join(t, w.ID, second, role.Editor) }()
		for deadline := time.Now().Add(time.Minute); waits() == 0; time.Sleep(20 * time.Millisecond) {
			select {
			case err := <-secondDone:
				t.Fatalf("the second acceptance (same user: %v) did not wait for the first, which is still open: %v",
					sameUser, err)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("the second acceptance (same user: %v) never waits for the first", sameUser)
			}
		}
		release()
		if err := <-firstDone; err != nil {
			t.Fatal(err)
		}
		if err := <-secondDone; sameUser && !errors.Is(err, ErrAlreadyMember) {
			t.Errorf("the same user's second acceptance, once the first has committed: %v, want ErrAlreadyMember",
				err)
		} else if !sameUser {
			wantLimit(t, "the second acceptance, once the first has committed", err, 2)
		}
		if n := count(t, d, "SELECT count(*) FROM membership WHERE workspace_id = $1", w.ID); n != 2 {
			t.Errorf("the workspace has %d members, want 2", n)
		}
	}
}

// TestUsage checks that a workspace's owners and admins read its usage of
// every key, with the quota of each that names a limit and the soft warning
// from 80% of it, and that the other roles are refused: an editor with
// ErrInsufficientRole, and a guest and a user who is not a member with
// ErrNotFound.
func TestUsage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d := newCappedService(t, 5)
	owner := addUser(t, d, "Owner")
	w, err := s.Create(ctx, owner, Settings{Name: "Harbour"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	users := map[role.Role]ids.UUID{}
	for _, r := range []role.Role{role.Admin, role.Editor, role.Guest} {
		users[r] = addUser(t, d, string(r))
		if _, err := s.Grant(ctx, w.ID, users[r], r); err != nil {
			t.Fatal(err)
		}
	}
	for _, who := range []ids.UUID{owner, users[role.Admin]} {
		got, err := s.Usage(ctx, who, w.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(usage.Keys()) {
			t.Fatalf("Usage gives %d keys, want %d", len(got), len(usage.Keys()))
		}
		for i, u := range got {
			want := Usage{Key: usage.Keys()[i]}
			switch want.Key {
			case usage.Members:
				// Four of five is 80%.
				want = Usage{Key: usage.Members, Used: 4, Limited: true, Quota: entitlements.AtMost(5), Warning: true}
			case usage.Projects, usage.StorageBytes:
				want.Limited, want.Quota = true, entitlements.NoLimit
			}
			if u != want {
				t.Errorf("Usage gives %+v, want %+v", u, want)
			}
		}
	}
	if _, err := s.Usage(ctx, users[role.Editor], w.ID); !errors.Is(err, ErrInsufficientRole) {
		t.Errorf("an editor's Usage: %v, want ErrInsufficientRole", err)
	}
	for _, who := range []ids.UUID{users[role.Guest], addUser(t, d, "Stranger")} {
		if _, err := s.Usage(ctx, who, w.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("Usage of a guest or a stranger: %v, want ErrNotFound", err)
		}
	}
}
