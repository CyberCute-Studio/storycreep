// Package notification keeps the accounts' notifications (PRD FR-COMMENT-02):
// what tells a member of a project that a comment mentions them, as "@Tomas,
// does the lighthouse exist yet?", in the notification centre of their
// account and in their open sessions. Mentions alone notify: a reply, or a
// thread's resolution, notifies nobody.
//
// A comment's commands make the notifications (package comment), in the
// batch's transaction, through Mentioned, and CommentEdited of an edit: one
// of the comment and each member whom it first mentions, as it is posted or
// an edit adds the mention, never twice, and never its author, who mentions
// themselves. Each change of an account's notifications, a new one, an edit
// of a comment that has some, which changes their preview, a deletion or a
// restoration of one, which hides them or shows them again, and a change of
// their read state, sends a notification of PostgreSQL's on Channel, of the
// account, once its transaction commits, which the real-time hub of every
// server process hears (package realtime), and tells the account's sessions'
// sockets of, which read the notifications again (FR-COMMENT-02 AC2).
//
// The centre is one per account, across every workspace that the account is
// a member of, guests' included (Service): a workspace's rows are read in a
// transaction of their own, as row-level security has them, the workspaces
// from the account's memberships, whose lookup the workspace switcher reads
// too (auth_user_memberships). A notification is shown while its comment is
// shown (live_comment: neither deleted nor of a thread whose target is in the
// trash), while its project is not in the trash, and while its recipient
// reads the project, by the role of their membership of its workspace or of
// the membership's grant (role.Readers), and is back as it was, read or not,
// once all of that holds again: nothing but marking it read, which takes a
// hidden one too (Read, ReadThrough), writes it meanwhile. Its routes take a
// session alone (FR-AUTH-04).
package notification

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
)

// Channel is the channel of PostgreSQL's LISTEN and NOTIFY on which a change
// of an account's notifications is told of, with the account's UUID as the
// payload, 36 bytes, once the transaction that made it commits, and which
// every real-time hub LISTENs on (package realtime).
const Channel = "storycreep_notifications"

// Kind is what a notification tells of.
type Kind string

// The kinds of notification. A new kind is a value of the check of
// notification.kind, in a migration of its own, and of the enum of
// NotificationBody.Kind, which breaks a client of v1, and says what makes
// one, and of what.
const (
	// Mention tells of a comment that mentions the recipient.
	Mention Kind = "mention"
)

// ErrNotFound is returned for a notification that the account does not have
// in any of its workspaces.
var ErrNotFound = errors.New("notification: the account has no such notification")

// Mentioned notifies each of users, the members whom the comment commentID
// of the project projectID, of the workspace workspaceID, mentions, of the
// mention by actor, the comment's author, in db, the batch's transaction
// that posted or edited the comment, but for actor, who mentions themselves,
// and those whom the comment has notified already, and tells each that it
// notifies of it on Channel, once the transaction commits. The comment's
// commands have checked that each may be mentioned (package comment).
func Mentioned(ctx context.Context, db queries.DBTX, workspaceID, projectID, commentID, actor ids.UUID,
	users []ids.UUID) error {
	q := queries.New(db)
	notified, err := insertMentions(ctx, q, workspaceID, projectID, commentID, actor, users)
	if err != nil {
		return err
	}
	return notify(ctx, q, notified)
}

// CommentEdited notifies each of added, the members whom an edit of the
// comment commentID of the project projectID, of the workspace workspaceID,
// mentions anew, of the mention by actor, as Mentioned does, in db, the
// batch's transaction, and tells each user whom the comment has notified,
// those whom it notifies now among them, that their notifications changed,
// as CommentChanged does, once, since the edit changes the preview of each
// of the comment's notifications.
func CommentEdited(ctx context.Context, db queries.DBTX, workspaceID, projectID, commentID, actor ids.UUID,
	added []ids.UUID) error {
	if _, err := insertMentions(ctx, queries.New(db), workspaceID, projectID, commentID, actor, added); err != nil {
		return err
	}
	return CommentChanged(ctx, db, workspaceID, projectID, commentID)
}

// insertMentions adds a notification of the comment commentID to each of
// users, but for actor and those whom the comment has notified already, each
// once, in q's transaction, and returns the users whom it notifies.
func insertMentions(ctx context.Context, q *queries.Queries, workspaceID, projectID, commentID, actor ids.UUID,
	users []ids.UUID) ([]pgtype.UUID, error) {
	var newIDs, recipients []pgtype.UUID
	seen := map[ids.UUID]bool{actor: true}
	for _, u := range users {
		if seen[u] {
			continue
		}
		seen[u] = true
		newIDs, recipients = append(newIDs, nullUUID(ids.New())), append(recipients, nullUUID(u))
	}
	if len(recipients) == 0 {
		return nil, nil
	}
	notified, err := q.InsertMentions(ctx, queries.InsertMentionsParams{WorkspaceID: nullUUID(workspaceID),
		ProjectID: nullUUID(projectID), CommentID: nullUUID(commentID), ActorID: nullUUID(actor), Ids: newIDs,
		UserIds: recipients})
	if err != nil {
		return nil, fmt.Errorf("notification: notify the mentioned members: %w", err)
	}
	return notified, nil
}

// CommentChanged tells each user whom the comment commentID of the project
// projectID, of the workspace workspaceID, has notified that their
// notifications changed, on Channel, once db, the batch's transaction,
// commits, as once the comment is edited, which changes their preview,
// deleted, which hides them, or restored, which shows them again, so that
// their sessions read them again.
func CommentChanged(ctx context.Context, db queries.DBTX, workspaceID, projectID, commentID ids.UUID) error {
	if err := queries.New(db).NotifyCommentRecipients(ctx, queries.NotifyCommentRecipientsParams{Channel: Channel,
		WorkspaceID: nullUUID(workspaceID), ProjectID: nullUUID(projectID), CommentID: nullUUID(commentID)}); err != nil {
		return fmt.Errorf("notification: tell the comment's recipients: %w", err)
	}
	return nil
}

// notify tells each of users that their notifications changed, on Channel,
// once q's transaction commits, unless users is empty.
func notify(ctx context.Context, q *queries.Queries, users []pgtype.UUID) error {
	if len(users) == 0 {
		return nil
	}
	if err := q.NotifyUsers(ctx, queries.NotifyUsersParams{Channel: Channel, UserIds: users}); err != nil {
		return fmt.Errorf("notification: tell the recipients: %w", err)
	}
	return nil
}

// NotificationProject is the project of a notification, as the centre names
// it.
type NotificationProject struct {
	ID   string `json:"id" doc:"The project's ID"`
	Name string `json:"name" doc:"The project's name"`
}

// NotificationActor is who made a notification, as the centre names them:
// who mentioned the recipient.
type NotificationActor struct {
	ID          string `json:"id" doc:"The user's ID"`
	DisplayName string `json:"display_name" doc:"The user's display name"`
}

// NotificationBody is a notification as the API shows it: what it tells of,
// with the comment that it is of, which a client opens as the list of a
// project's comments names it, by its thread's target and the target's
// board and node.
type NotificationBody struct {
	ID          string              `json:"id" doc:"The notification's ID"`
	Kind        Kind                `json:"kind" enum:"mention" doc:"What the notification tells of: mention, a comment that mentions the account"`
	CreatedAt   time.Time           `json:"created_at" doc:"When the comment mentioned the account: when it was posted, or edited to mention it"`
	ReadAt      *time.Time          `json:"read_at,omitempty" doc:"When the account marked the notification read, if it did. An unread one has none."`
	WorkspaceID string              `json:"workspace_id" doc:"The ID of the workspace of the comment's project"`
	Project     NotificationProject `json:"project" doc:"The comment's project"`
	CommentID   string              `json:"comment_id" doc:"The ID of the comment that mentions the account"`
	ThreadID    string              `json:"thread_id" doc:"The ID of the comment's thread, its first comment's"`
	// TargetType, TargetID, BoardID, and NodeID name the thread's target as
	// the list of a project's comments names it (comment.CommentBody), whose
	// enum TestTargetEnums holds this one to.
	TargetType string `json:"target_type" enum:"node,connection,board,variable" doc:"The kind of the thread's target: a node, a connection, a board, or a variable"`
	TargetID   string `json:"target_id" doc:"The ID of the thread's target"`
	BoardID    string `json:"board_id,omitempty" doc:"The ID of the target's board: a node's, a connection's, or a board variable's, or the board itself. A global variable has none."`
	NodeID     string `json:"node_id,omitempty" doc:"The ID of the node that a client opens the target by: a node's own, or the node that a connection leaves. A board and a variable have none."`
	// Actor is nil once the actor's account is deleted.
	Actor   *NotificationActor `json:"actor,omitempty" doc:"Who mentioned the account, the comment's author, unless their account is deleted"`
	Preview string             `json:"preview" doc:"The preview of the comment's body as it is now, as a rich text's preview is derived (PRD FR-TEXT-01), with each member that it mentions written as @ and the member's display name as it is now, or as @… once the member's account is deleted"`
	key     Key
}

// Key is where a notification is in the centre (NotificationBody.Key), by
// which a page of it says where the next one starts: when it was made, as the
// server's clock has it, and its UUID. The zero Key is before every
// notification.
type Key struct {
	At time.Time
	ID ids.UUID
}

// IsZero reports whether k is the zero Key, before every notification.
func (k Key) IsZero() bool { return k.At.IsZero() && k.ID == ids.UUID{} }

// Key returns where n is in its account's centre.
func (n NotificationBody) Key() Key { return n.key }

// before reports whether a is before b in the centre, newest first, by when
// each was made, and by their UUIDs where those are the same.
func before(a, b Key) bool {
	if !a.At.Equal(b.At) {
		return a.At.After(b.At)
	}
	return string(a.ID[:]) > string(b.ID[:])
}

// targetKinds are the kinds of a thread's target, as comment_thread_target
// names them, with the kind of their IDs, as package comment has them
// (comment.TargetType), which a test holds them to.
var targetKinds = map[string]ids.Kind{"node": ids.Node, "connection": ids.Connection, "board": ids.Board,
	"variable": ids.Variable}

// TargetKind returns the kind of the IDs of the targets of the kind t, as
// comment_thread_target names it, or zero for a kind that is not a target's.
func TargetKind(t string) ids.Kind { return targetKinds[t] }

// memberRoles are the roles of a membership that read a project without a
// grant, and grantRoles those of a grant that read it, as the queries take
// them (role.ReaderNames).
var memberRoles, grantRoles = role.ReaderNames()

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Service reads the accounts' notifications, and marks them read.
type Service struct {
	db DB
}

// New returns a Service that keeps the notifications in db.
func New(db DB) *Service {
	return &Service{db: db}
}

// workspaces returns the workspaces that the user userID is a member of,
// whose deletion is not pending, in the order of their IDs.
func (s *Service) workspaces(ctx context.Context, userID ids.UUID) ([]ids.UUID, error) {
	var out []ids.UUID
	err := s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		rows, err := queries.New(tx).NotificationWorkspaces(ctx, nullUUID(userID))
		if err != nil {
			return fmt.Errorf("notification: read the account's workspaces: %w", err)
		}
		for _, r := range rows {
			out = append(out, r.Bytes)
		}
		return nil
	})
	return out, err
}

// each runs fn in a transaction of each of the workspaces of the user
// userID, in turn, as the user, until fn returns an error or stop.
func (s *Service) each(ctx context.Context, userID ids.UUID,
	fn func(q *queries.Queries, workspaceID ids.UUID) (stop bool, err error)) error {
	workspaces, err := s.workspaces(ctx, userID)
	if err != nil {
		return err
	}
	for _, ws := range workspaces {
		var stop bool
		err := s.db.InTx(ctx, database.Scope{WorkspaceID: ws, UserID: userID}, func(tx pgx.Tx) error {
			var err error
			stop, err = fn(queries.New(tx), ws)
			return err
		})
		if err != nil || stop {
			return err
		}
	}
	return nil
}

// List returns at most limit of the notifications of the user userID that
// are shown, across their workspaces, newest first, by when each was made,
// as the server's clock has it, and by their IDs where those are the same,
// UUIDv7s that the server makes: the first, if after is zero, or else those
// after it. It reads each workspace in a transaction of its own, for the
// first limit after after, and merges them, and reads the comments' bodies
// of those that it returns alone. Each has its comment's preview, with the
// display names of the members that the comment mentions as they are now
// (NotificationBody.Preview).
func (s *Service) List(ctx context.Context, userID ids.UUID, after Key, limit int) ([]NotificationBody, error) {
	type listed struct {
		ws  ids.UUID
		row queries.NotificationsRow
		key Key
	}
	var all []listed
	err := s.each(ctx, userID, func(q *queries.Queries, ws ids.UUID) (bool, error) {
		page, err := q.Notifications(ctx, queries.NotificationsParams{WorkspaceID: nullUUID(ws),
			UserID: nullUUID(userID), GrantRoles: grantRoles, MemberRoles: memberRoles, After: !after.IsZero(),
			AfterAt: pgtype.Timestamptz{Time: after.At, Valid: true}, AfterID: pgtype.UUID{Bytes: after.ID, Valid: true},
			MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return false, fmt.Errorf("notification: list the notifications: %w", err)
		}
		for _, r := range page {
			all = append(all, listed{ws: ws, row: r, key: Key{At: r.CreatedAt.Time, ID: r.ID.Bytes}})
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(all, func(a, b listed) int {
		switch {
		case before(a.key, b.key):
			return -1
		case before(b.key, a.key):
			return 1
		}
		return 0
	})
	all = all[:min(len(all), limit)]
	out := make([]NotificationBody, len(all))
	docs := make([]*richtext.Doc, len(all))
	for i, l := range all {
		if out[i], docs[i], err = bodyOf(l.ws, l.row); err != nil {
			return nil, err
		}
	}
	if err := s.preview(ctx, userID, out, docs); err != nil {
		return nil, err
	}
	return out, nil
}

// preview writes the preview of each of out, of the comment's body of the
// same index of docs, with the display names of the members that the bodies
// mention, read in one query, as they are now.
func (s *Service) preview(ctx context.Context, userID ids.UUID, out []NotificationBody, docs []*richtext.Doc) error {
	var users []string
	seen := map[string]bool{}
	for _, doc := range docs {
		for _, m := range doc.Members() {
			if !seen[m.User] {
				seen[m.User] = true
				users = append(users, m.User)
			}
		}
	}
	names := map[string]string{}
	if len(users) > 0 {
		err := s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
			var err error
			names, err = MemberNames(ctx, queries.New(tx), users)
			return err
		})
		if err != nil {
			return err
		}
	}
	for i, doc := range docs {
		named := doc.WithMembers(func(user string) string {
			if name, ok := names[user]; ok {
				return "@" + name
			}
			return formerMember
		})
		out[i].Preview = richtext.Preview(richtext.PlainText(named.Segments(richtext.NoNames{})))
	}
	return nil
}

// MemberNames returns the display names of the users whose IDs, as the API
// writes them (ids.User), are users, by those IDs, read in one query in q's
// transaction, as a comment's mentions show them and a notification's
// preview writes them (package comment, Service.List): a user whose account
// is deleted, or an ID that is not a user's, has none.
func MemberNames(ctx context.Context, q *queries.Queries, users []string) (map[string]string, error) {
	uuids := make([]pgtype.UUID, 0, len(users))
	for _, user := range users {
		if u, err := ids.Decode(ids.User, user); err == nil {
			uuids = append(uuids, nullUUID(u))
		}
	}
	names := map[string]string{}
	if len(uuids) == 0 {
		return names, nil
	}
	rows, err := q.MentionedUsers(ctx, uuids)
	if err != nil {
		return nil, fmt.Errorf("notification: read the mentioned members' names: %w", err)
	}
	for _, r := range rows {
		names[ids.Encode(ids.User, r.ID.Bytes)] = r.DisplayName
	}
	return names, nil
}

// formerMember is how a preview writes a mention of a member whose account
// is deleted, whose name is gone: @ and an ellipsis, which no language reads
// otherwise, as a client writes a former member in its own words where it
// shows the comment itself.
const formerMember = "@…"

// bodyOf returns the notification r of the workspace ws as the API shows it,
// but for its preview, with its comment's body, or an error if the body is
// not a document that the commands store, or its target's kind is not one.
func bodyOf(ws ids.UUID, r queries.NotificationsRow) (NotificationBody, *richtext.Doc, error) {
	kind := TargetKind(r.TargetType)
	if kind == 0 {
		return NotificationBody{}, nil, fmt.Errorf("notification: the comment %s is on a target of the kind %q",
			ids.UUID(r.CommentID.Bytes), r.TargetType)
	}
	doc, err := richtext.Parse(r.Body)
	if err != nil {
		return NotificationBody{}, nil, fmt.Errorf("notification: the body of the comment %s: %w",
			ids.UUID(r.CommentID.Bytes), err)
	}
	n := NotificationBody{ID: ids.Encode(ids.Notification, r.ID.Bytes), Kind: Mention, CreatedAt: r.CreatedAt.Time,
		WorkspaceID: ids.Encode(ids.Workspace, ws), CommentID: ids.Encode(ids.Comment, r.CommentID.Bytes),
		ThreadID: ids.Encode(ids.Comment, r.ThreadID.Bytes), TargetType: r.TargetType,
		TargetID: ids.Encode(kind, r.TargetID.Bytes), key: Key{At: r.CreatedAt.Time, ID: r.ID.Bytes},
		Project: NotificationProject{ID: ids.Encode(ids.Project, r.ProjectID.Bytes), Name: r.ProjectName}}
	if r.ReadAt.Valid {
		n.ReadAt = &r.ReadAt.Time
	}
	if r.TargetBoardID.Valid {
		n.BoardID = ids.Encode(ids.Board, r.TargetBoardID.Bytes)
	}
	if r.TargetNodeID.Valid {
		n.NodeID = ids.Encode(ids.Node, r.TargetNodeID.Bytes)
	}
	if r.ActorID.Valid {
		n.Actor = &NotificationActor{ID: ids.Encode(ids.User, r.ActorID.Bytes), DisplayName: r.ActorName.String}
	}
	return n, doc, nil
}

// Unread returns how many of the notifications of the user userID that are
// shown, as List shows them, are unread, across their workspaces.
func (s *Service) Unread(ctx context.Context, userID ids.UUID) (int64, error) {
	var n int64
	err := s.each(ctx, userID, func(q *queries.Queries, ws ids.UUID) (bool, error) {
		c, err := q.UnreadNotifications(ctx, queries.UnreadNotificationsParams{WorkspaceID: nullUUID(ws),
			UserID: nullUUID(userID), GrantRoles: grantRoles, MemberRoles: memberRoles})
		if err != nil {
			return false, fmt.Errorf("notification: count the unread notifications: %w", err)
		}
		n += c
		return false, nil
	})
	return n, err
}

// Read marks read the notification id of the user userID, shown or not,
// unless it is read already, and tells the user's sessions of it (Channel),
// or returns ErrNotFound if the user has no such notification in any of
// their workspaces.
func (s *Service) Read(ctx context.Context, userID, id ids.UUID) error {
	found := false
	err := s.each(ctx, userID, func(q *queries.Queries, ws ids.UUID) (bool, error) {
		marked, err := q.ReadNotification(ctx, queries.ReadNotificationParams{WorkspaceID: nullUUID(ws),
			UserID: nullUUID(userID), ID: nullUUID(id)})
		if err != nil {
			return false, fmt.Errorf("notification: mark the notification read: %w", err)
		}
		if marked > 0 {
			found = true
			return true, notify(ctx, q, []pgtype.UUID{nullUUID(userID)})
		}
		_, err = q.NotificationOf(ctx, queries.NotificationOfParams{WorkspaceID: nullUUID(ws),
			UserID: nullUUID(userID), ID: nullUUID(id)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return false, nil
		case err != nil:
			return false, fmt.Errorf("notification: read the notification: %w", err)
		}
		found = true
		return true, nil
	})
	if err == nil && !found {
		return ErrNotFound
	}
	return err
}

// ReadThrough marks read the notifications of the user userID that are
// unread, shown or not, in every workspace of theirs, made at or before the
// notification through, by when each was made and by their IDs, as List
// orders them, so that one that was made after it, as after the page that a
// client shows was read, stays unread, and tells the user's sessions of it
// (Channel) once every workspace's are marked, rather than as the marks of
// each workspace commit, which would have them read the centre half marked,
// as often as it has workspaces. A notification is made as its batch applies
// (Mentioned), and a project's batches apply one at a time, so one of
// another project's batch that applied before through's and committed only
// after the page was read is made before through, and is marked read too. It
// returns ErrNotFound, and marks nothing, if the user has no notification
// through in any of their workspaces.
func (s *Service) ReadThrough(ctx context.Context, userID, through ids.UUID) error {
	var at pgtype.Timestamptz
	err := s.each(ctx, userID, func(q *queries.Queries, ws ids.UUID) (bool, error) {
		n, err := q.NotificationOf(ctx, queries.NotificationOfParams{WorkspaceID: nullUUID(ws),
			UserID: nullUUID(userID), ID: nullUUID(through)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return false, nil
		case err != nil:
			return false, fmt.Errorf("notification: read the notification: %w", err)
		}
		at = n.CreatedAt
		return true, nil
	})
	switch {
	case err != nil:
		return err
	case !at.Valid:
		return ErrNotFound
	}
	marked := false
	err = s.each(ctx, userID, func(q *queries.Queries, ws ids.UUID) (bool, error) {
		n, err := q.ReadNotifications(ctx, queries.ReadNotificationsParams{WorkspaceID: nullUUID(ws),
			UserID: nullUUID(userID), ThroughAt: at, ThroughID: nullUUID(through)})
		if err != nil {
			return false, fmt.Errorf("notification: mark the notifications read: %w", err)
		}
		marked = marked || n > 0
		return false, nil
	})
	if err != nil || !marked {
		return err
	}
	return s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		return notify(ctx, queries.New(tx), []pgtype.UUID{nullUUID(userID)})
	})
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
