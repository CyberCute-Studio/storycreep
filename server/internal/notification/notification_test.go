package notification_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/notification"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// TestNotificationIsolation tries the package's queries from another
// workspace's transaction, where row-level security must keep them from
// reading, adding, or changing its notifications (NFR-SEC-01): the centre's
// page and its count of the unread, a notification by its ID, marking one
// read and all read, and the notifications of a comment, which its
// recipients are told of. Each workspace's notification is of its comment, by
// its user, whom the harness makes a reader of the project.
//
// Verifies: NFR-SEC-01
func TestNotificationIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	uuid := func(id ids.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
	var memberRoles, grantRoles []string
	ms, gs := role.Readers()
	for _, r := range ms {
		memberRoles = append(memberRoles, string(r))
	}
	for _, r := range gs {
		grantRoles = append(grantRoles, string(r))
	}
	// recipient is the user of each workspace's notification, a member of it.
	recipient := map[*isolationtest.Workspace]ids.UUID{}
	for _, w := range []*isolationtest.Workspace{f.A, f.B} {
		var user ids.UUID
		if err := f.Admin().QueryRow(ctx, "SELECT user_id FROM notification WHERE id = $1",
			w.Rows["notification"]).Scan(&user); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Admin().Exec(ctx, "INSERT INTO membership (id, workspace_id, user_id, role) VALUES "+
			"($1, $2, $3, 'viewer')", ids.New(), w.ID, user); err != nil {
			t.Fatal(err)
		}
		recipient[w] = user
	}
	for _, op := range []isolationtest.Op{
		{Name: "a recipient's notifications", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			rows, err := queries.New(tx).Notifications(ctx, queries.NotificationsParams{WorkspaceID: uuid(target.ID),
				UserID: uuid(recipient[target]), GrantRoles: grantRoles, MemberRoles: memberRoles,
				AfterAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, AfterID: uuid(ids.UUID{}), MaxRows: 100})
			var found []ids.UUID
			for _, r := range rows {
				found = append(found, r.ID.Bytes)
			}
			return found, err
		}},
		{Name: "the count of a recipient's unread notifications", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			n, err := queries.New(tx).UnreadNotifications(ctx, queries.UnreadNotificationsParams{
				WorkspaceID: uuid(target.ID), UserID: uuid(recipient[target]), GrantRoles: grantRoles,
				MemberRoles: memberRoles})
			if n == 0 {
				return nil, err
			}
			return []ids.UUID{target.Rows["notification"]}, err
		}},
		{Name: "a notification by its ID", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			row, err := queries.New(tx).NotificationOf(ctx, queries.NotificationOfParams{WorkspaceID: uuid(target.ID),
				UserID: uuid(recipient[target]), ID: uuid(target.Rows["notification"])})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return []ids.UUID{row.ID.Bytes}, err
		}},
		{Name: "add a notification", Denied: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			id := ids.New()
			_, err := queries.New(tx).InsertMentions(ctx, queries.InsertMentionsParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), CommentID: uuid(target.Rows["comment"]),
				ActorID: uuid(target.UserID), Ids: []pgtype.UUID{uuid(id)}, UserIds: []pgtype.UUID{uuid(target.UserID)}})
			return []ids.UUID{id}, err
		}},
		{Name: "mark a notification read", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			n, err := queries.New(tx).ReadNotification(ctx, queries.ReadNotificationParams{
				WorkspaceID: uuid(target.ID), UserID: uuid(recipient[target]), ID: uuid(target.Rows["notification"])})
			if n == 0 {
				return nil, err
			}
			return []ids.UUID{target.Rows["notification"]}, err
		}},
		{Name: "mark a recipient's notifications read", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			n, err := queries.New(tx).ReadNotifications(ctx, queries.ReadNotificationsParams{
				WorkspaceID: uuid(target.ID), UserID: uuid(recipient[target]),
				ThroughAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
				ThroughID: uuid(ids.UUID{})})
			if n == 0 {
				return nil, err
			}
			return []ids.UUID{target.Rows["notification"]}, err
		}},
	} {
		f.Check(t, op)
	}
}

// failingDB runs the transactions of db, but fails each of the workspace
// fail, as a workspace whose transaction times out would.
type failingDB struct {
	notification.DB
	fail ids.UUID
}

var errWorkspaceFailed = errors.New("the workspace's transaction failed")

func (d failingDB) InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error {
	if scope.WorkspaceID == d.fail {
		return errWorkspaceFailed
	}
	return d.DB.InTx(ctx, scope, fn)
}

// TestReadThroughFailsPartWay checks that Mark all as read, whose workspaces'
// marks commit each on their own, still tells the account's sessions of the
// marks of the workspaces before one whose marks failed, as it returns that
// workspace's error, so that their other sessions read their notifications
// again rather than keep showing those unread.
//
// Verifies: FR-COMMENT-02/AC2
func TestReadThroughFailsPartWay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	f := isolationtest.New(t, d, isolationtest.Tables)
	user, err := isolationtest.NewUser(ctx, f.Admin())
	if err != nil {
		t.Fatal(err)
	}
	// The account's notification in each of two workspaces, of each one's
	// comment; the workspaces are read in the order of their IDs.
	first, second := f.A, f.B
	if bytes.Compare(second.ID[:], first.ID[:]) < 0 {
		first, second = second, first
	}
	notified := map[*isolationtest.Workspace]ids.UUID{}
	for _, w := range []*isolationtest.Workspace{first, second} {
		if _, err := f.Admin().Exec(ctx, "INSERT INTO membership (id, workspace_id, user_id, role) VALUES "+
			"($1, $2, $3, 'viewer')", ids.New(), w.ID, user); err != nil {
			t.Fatal(err)
		}
		id := ids.New()
		if _, err := f.Admin().Exec(ctx, "INSERT INTO notification (id, workspace_id, project_id, user_id, "+
			"comment_id, actor_id, kind) VALUES ($1, $2, $3, $4, $5, $6, 'mention')", id, w.ID, w.Rows["project"],
			user, w.Rows["comment"], w.UserID); err != nil {
			t.Fatal(err)
		}
		notified[w] = id
	}
	listen := d.Admin(t)
	if _, err := listen.Exec(ctx, "LISTEN "+notification.Channel); err != nil {
		t.Fatal(err)
	}

	// Through the first workspace's notification, whose workspace is the
	// only one that finding it reads; the second's marks fail.
	svc := notification.New(failingDB{DB: f.DB, fail: second.ID})
	if err := svc.ReadThrough(ctx, user, notified[first]); !errors.Is(err, errWorkspaceFailed) {
		t.Fatalf("ReadThrough with the second workspace failing: %v, want its error", err)
	}
	var read bool
	if err := f.Admin().QueryRow(ctx, "SELECT read_at IS NOT NULL FROM notification WHERE id = $1",
		notified[first]).Scan(&read); err != nil {
		t.Fatal(err)
	}
	if !read {
		t.Fatal("the first workspace's notification is unread, want its mark committed")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	n, err := listen.WaitForNotification(waitCtx)
	if err != nil {
		t.Fatalf("no notification of the account's sessions of the marks that committed: %v", err)
	}
	if n.Channel != notification.Channel || n.Payload != user.String() {
		t.Errorf("notified %q on %q, want %q on %q", n.Payload, n.Channel, user, notification.Channel)
	}
}
