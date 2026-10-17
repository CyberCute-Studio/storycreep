package invite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/email"
	"github.com/CyberCute-Studio/storycreep/server/internal/email/emailtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// testPublicURL is the instance's URL in the tests.
var testPublicURL = func() config.PublicURL {
	c, err := config.Load(config.Options{Env: config.MapEnvironment(map[string]string{
		"STORYCREEP_PUBLIC_URL": "https://storycreep.example.com", "STORYCREEP_DATABASE_URL": "postgres://u:p@db/d"})})
	if err != nil {
		panic(err)
	}
	return c.PublicURL
}()

// queueSpy records the jobs that a Service enqueues, instead of enqueueing
// them.
type queueSpy struct {
	mu   sync.Mutex
	jobs []jobs.Args
}

func (q *queueSpy) Enqueue(_ context.Context, _ pgx.Tx, args jobs.Args, _ *jobs.EnqueueOptions) (jobs.Enqueued,
	error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, args)
	return jobs.Enqueued{}, nil
}

// fixture is a Service on a database of its own, with a workspace whose
// owner is owner.
type fixture struct {
	*Service
	d         *dbtest.Database
	db        *database.DB
	workspace ids.UUID
	owner     ids.UUID
	queue     *queueSpy
}

// newFixture returns a fixture, whose Service sends its email through
// mailer, if it is set, and else none.
func newFixture(t *testing.T, mailer *email.Service) *fixture {
	t.Helper()
	d := dbtest.New(t)
	db := d.Open(t)
	workspaces := workspace.New(db, workspace.Options{PlanKey: "unlimited", DefaultLocale: language.English})
	accounts := account.New(db, account.Options{IdleTimeout: time.Hour, Lifetime: time.Hour,
		Limits: ratelimit.New(db, ratelimit.DefaultPolicy())})
	f := &fixture{d: d, db: db, queue: &queueSpy{}}
	f.Service = New(db, Options{Workspaces: workspaces, Accounts: accounts, Email: mailer, Queue: f.queue,
		PublicURL: testPublicURL})
	f.owner = f.addUser(t, "owner@example.com")
	w, err := workspaces.Create(context.Background(), f.owner, workspace.Settings{Name: "Harbour"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	f.workspace = w.ID
	return f
}

// addUser adds an active account with the address, as the superuser, and
// returns its ID.
func (f *fixture) addUser(t *testing.T, address string) ids.UUID {
	t.Helper()
	id := ids.New()
	if _, err := f.d.Admin(t).Exec(context.Background(),
		`INSERT INTO "user" (id, email, display_name) VALUES ($1, $2, 'User')`, id, address); err != nil {
		t.Fatal(err)
	}
	return id
}

// count returns the number that a query counts, as the superuser.
func (f *fixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.d.Admin(t).QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestAcceptOnce checks that of two users who accept one link invitation at
// once, one joins the workspace, and the other is told that it has been
// accepted, and that the one who joined is answered with the membership
// again.
func TestAcceptOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, nil)
	inv, err := f.Create(ctx, f.owner, f.workspace, Request{Role: role.Editor}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	_, token, _ := strings.Cut(inv.URL.Reveal(), "#token=")
	users := []ids.UUID{f.addUser(t, "a@example.com"), f.addUser(t, "b@example.com")}
	results := make(chan error, len(users))
	var start sync.WaitGroup
	start.Add(1)
	for _, u := range users {
		go func() {
			start.Wait()
			_, err := f.Accept(ctx, u, secret.New(token), audit.Client{})
			results <- err
		}()
	}
	start.Done()
	var joined, refused int
	for range users {
		switch err := <-results; {
		case err == nil:
			joined++
		case errors.Is(err, ErrAccepted):
			refused++
		default:
			t.Errorf("accept: %v", err)
		}
	}
	if joined != 1 || refused != 1 {
		t.Errorf("%d users joined and %d were refused, want 1 and 1", joined, refused)
	}
	if n := f.count(t, "SELECT count(*) FROM membership WHERE workspace_id = $1 AND role = 'editor'",
		f.workspace); n != 1 {
		t.Errorf("%d editors joined", n)
	}
	var acceptor ids.UUID
	if err := f.d.Admin(t).QueryRow(ctx, "SELECT accepted_by FROM invite").Scan(&acceptor); err != nil {
		t.Fatal(err)
	}
	m, err := f.Accept(ctx, acceptor, secret.New(token), audit.Client{})
	if err != nil || m.Role != role.Editor || m.Workspace.ID != f.workspace {
		t.Errorf("accept again: %+v, %v", m, err)
	}
}

// TestSendSkipsClosedInvites checks the job that emails an invitation: it
// emails an open invitation to its address, in the workspace's default
// locale, gives it the hash of the email's link, and makes it expire 14 days
// from then, and sends nothing for an invitation that has been revoked or
// accepted, or has expired, or once the instance sends no email.
func TestSendSkipsClosedInvites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := emailtest.Start(t, emailtest.Options{})
	settings := server.Settings(config.SMTPNone, "Storycreep <noreply@example.com>")
	f := newFixture(t, email.New(nil, email.Options{SMTP: &settings, Hostname: "storycreep.example.com"}))
	add := func(address, state string) ids.UUID {
		id := ids.New()
		if _, err := f.d.Admin(t).Exec(ctx, `INSERT INTO invite (id, workspace_id, email, role, expires_at)
			VALUES ($1, $2, $3, 'viewer', now() + interval '1 hour')`, id, f.workspace, address); err != nil {
			t.Fatal(err)
		}
		if state != "" {
			if _, err := f.d.Admin(t).Exec(ctx, "UPDATE invite SET "+state+" WHERE id = $1", id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	for i, state := range []string{"revoked_at = now()", "expires_at = now() - interval '1 second'",
		"accepted_at = now()"} {
		id := add(fmt.Sprintf("closed%d@example.com", i), state)
		if err := f.send(ctx, f.db, f.workspace, id); err != nil {
			t.Errorf("%s: %v", state, err)
		}
	}
	if got := server.Messages(); len(got) != 0 {
		t.Errorf("%d emails for closed invitations", len(got))
	}
	open := add("hana@example.com", "")
	if err := f.send(ctx, f.db, f.workspace, open); err != nil {
		t.Fatal(err)
	}
	got := server.Wait(t, 1, 10*time.Second)
	if len(got[0].To) != 1 || got[0].To[0] != "hana@example.com" ||
		!strings.Contains(got[0].Body(t), "https://storycreep.example.com/app/invite#token=") ||
		!strings.Contains(got[0].Body(t), "as a viewer") {
		t.Errorf("the email is to %v: %s", got[0].To, got[0].Body(t))
	}
	if n := f.count(t, `SELECT count(*) FROM invite WHERE id = $1 AND token_hash IS NOT NULL AND sent_at IS NOT NULL
		AND expires_at > now() + interval '13 days'`, open); n != 1 {
		t.Errorf("the emailed invitation has no hash, or its expiry did not move")
	}
	if p, err := f.Lookup(ctx, secret.New(got[0].Token(t))); err != nil || p.Email != "hana@example.com" ||
		p.WorkspaceName != "Harbour" {
		t.Errorf("look the emailed invitation up: %+v, %v", p, err)
	}

	quiet := newFixture(t, nil)
	id := ids.New()
	if _, err := quiet.d.Admin(t).Exec(ctx, `INSERT INTO invite (id, workspace_id, email, role, expires_at)
		VALUES ($1, $2, 'jo@example.com', 'viewer', now() + interval '1 hour')`, id, quiet.workspace); err != nil {
		t.Fatal(err)
	}
	if err := quiet.send(ctx, quiet.db, quiet.workspace, id); err != nil {
		t.Errorf("send without email: %v", err)
	}
	if n := quiet.count(t, "SELECT count(*) FROM invite WHERE token_hash IS NOT NULL"); n != 0 {
		t.Errorf("an instance without email gave an invitation a link")
	}
}

// TestIsolation tries the queries of the invitations from another
// workspace's transaction, where row-level security must keep them from
// reading, changing, or adding any of its invitations or memberships
// (NFR-SEC-01).
//
// Verifies: NFR-SEC-01
func TestIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	// The fixture's invitations are link invitations; the queries of an
	// invitation to an address need one.
	for _, w := range []*isolationtest.Workspace{f.A, f.B} {
		if _, err := f.Admin().Exec(ctx, "UPDATE invite SET email = $1 WHERE id = $2",
			w.Rows["invite"].String()+"@example.com", w.Rows["invite"]); err != nil {
			t.Fatal(err)
		}
	}
	hash := func(w *isolationtest.Workspace) []byte {
		var h []byte
		if err := f.Admin().QueryRow(ctx, "SELECT token_hash FROM invite WHERE id = $1",
			w.Rows["invite"]).Scan(&h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	member := func(w *isolationtest.Workspace) string {
		var email string
		if err := f.Admin().QueryRow(ctx, `SELECT u.email::text FROM membership m JOIN "user" u ON u.id = m.user_id
			WHERE m.id = $1`, w.Rows["membership"]).Scan(&email); err != nil {
			t.Fatal(err)
		}
		return email
	}
	found := func(target *isolationtest.Workspace, err error) ([]ids.UUID, error) {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return []ids.UUID{target.Rows["invite"]}, err
	}
	key := func(target *isolationtest.Workspace) (ws, id [16]byte) {
		return target.ID, target.Rows["invite"]
	}
	for _, op := range []isolationtest.Op{
		{Name: "lock an invitation", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			ws, id := key(target)
			_, err := queries.New(tx).LockInvite(ctx, queries.LockInviteParams{WorkspaceID: nullUUID(ws),
				ID: nullUUID(id)})
			return found(target, err)
		}},
		{Name: "lock an invitation by its hash", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			_, err := queries.New(tx).LockInviteByHash(ctx, queries.LockInviteByHashParams{
				WorkspaceID: nullUUID(target.ID), TokenHash: hash(target)})
			return found(target, err)
		}},
		{Name: "lock an invitation to send", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, id := key(target)
			_, err := queries.New(tx).LockInviteToSend(ctx, queries.LockInviteToSendParams{WorkspaceID: nullUUID(ws),
				ID: nullUUID(id)})
			return found(target, err)
		}},
		{Name: "the open invitations", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			rows, err := queries.New(tx).OpenInvites(ctx, queries.OpenInvitesParams{WorkspaceID: nullUUID(target.ID),
				MaxRows: 10})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.ID.Bytes)
			}
			return out, err
		}},
		{Name: "whether an address is a member's", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			is, err := queries.New(tx).AddressIsMember(ctx, queries.AddressIsMemberParams{
				WorkspaceID: nullUUID(target.ID), Email: member(target)})
			if is {
				return []ids.UUID{target.Rows["membership"]}, err
			}
			return nil, err
		}},
		{Name: "renew an invitation", Blind: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, id := key(target)
			_, err := queries.New(tx).RenewInvite(ctx, queries.RenewInviteParams{TokenHash: isolationtest.TokenHash(),
				Lifetime: database.Interval(Lifetime), WorkspaceID: nullUUID(ws), ID: nullUUID(id)})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return nil, err
		}},
		{Name: "revoke an invitation", Blind: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, id := key(target)
			return nil, queries.New(tx).RevokeInvite(ctx, queries.RevokeInviteParams{WorkspaceID: nullUUID(ws),
				ID: nullUUID(id)})
		}},
		{Name: "accept an invitation", Blind: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, id := key(target)
			return nil, queries.New(tx).AcceptInvite(ctx, queries.AcceptInviteParams{AcceptedBy: nullUUID(target.UserID),
				WorkspaceID: nullUUID(ws), ID: nullUUID(id)})
		}},
		{Name: "give an invitation its hash", Blind: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, id := key(target)
			return nil, queries.New(tx).SetInviteHash(ctx, queries.SetInviteHashParams{
				TokenHash: isolationtest.TokenHash(), Lifetime: database.Interval(Lifetime), WorkspaceID: nullUUID(ws),
				ID: nullUUID(id)})
		}},
		{Name: "a new invitation", Denied: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			id := ids.New()
			_, err := queries.New(tx).InsertInvite(ctx, queries.InsertInviteParams{ID: nullUUID(id),
				WorkspaceID: nullUUID(target.ID), Role: "viewer", TokenHash: isolationtest.TokenHash(),
				Lifetime: database.Interval(Lifetime), CreatedBy: nullUUID(target.UserID)})
			return []ids.UUID{id}, err
		}},
	} {
		f.Check(t, op)
	}
}

// TestEmailNamesNoWorkspace checks the email of an invitation: in the
// locale it is asked in, with the role, and the link's lifetime, but nothing
// that a user chose.
func TestEmailNamesNoWorkspace(t *testing.T) {
	t.Parallel()
	msg := inviteEmail(testPublicURL, "en", "owner", secret.New("abc"), "hana@example.com")
	if msg.To.Address != "hana@example.com" || msg.To.Name != "" ||
		msg.Subject != "You are invited to a Storycreep workspace" ||
		!strings.Contains(msg.Body, "storycreep.example.com as an owner") ||
		!strings.Contains(msg.Body, "within 14 days") ||
		!strings.Contains(msg.Body, "https://storycreep.example.com/app/invite#token=abc") {
		t.Errorf("the email is %+v", msg)
	}
	if other := inviteEmail(testPublicURL, "not a tag", "guest", secret.New("abc"), "hana@example.com"); !strings.Contains(
		other.Body, "as a guest") {
		t.Errorf("the email in an unknown locale is %s", other.Body)
	}
}
