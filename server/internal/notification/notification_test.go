package notification_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
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
