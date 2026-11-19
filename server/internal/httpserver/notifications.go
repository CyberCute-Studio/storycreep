package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/notification"
)

type notificationsInput struct {
	api.PageParams
}

type notificationsOutput struct {
	Body api.Page[notification.NotificationBody]
}

// unreadBody is the body of GET /api/v1/notifications/unread.
type unreadBody struct {
	Unread int64 `json:"unread" minimum:"0" doc:"How many of the account's notifications that are shown are unread, across its workspaces"`
}

type unreadOutput struct {
	Body unreadBody
}

type readNotificationInput struct {
	CSRFInput
	NotificationID string `path:"notification_id" doc:"The notification's ID"`
}

// readAllBody is the body of POST /api/v1/notifications/read-all.
type readAllBody struct {
	Through string `json:"through" doc:"The ID of the newest notification that the client shows, as the first of the list's first page: it and every notification of the account made before it are marked read, so that one that arrived after the client read the list stays unread, but for one of another project's batch that applied before through's and committed after the list was read"`
}

type readAllInput struct {
	CSRFInput
	Body readAllBody
}

// notificationKey is the key of the notification centre that its pages'
// cursors hold (notification.Key): when the notification was made, in UTC,
// as the comments' key holds a time (commentKey), and its UUID in its text
// form.
type notificationKey struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

// notificationsDoc says, in the description of an operation of the
// notification centre, which notifications it holds, and how they change.
const notificationsDoc = " The centre is the account's, across every workspace that it is a member of, guests' " +
	"included, and holds a notification of each comment that mentions the account (PRD FR-COMMENT-02), once, " +
	"made as the comment is posted or an edit adds the mention, never by the account's own comments. A " +
	"notification is shown while its comment is shown, neither deleted nor of a thread whose target is in the " +
	"project's trash, while its project is not in the trash, and while the account reads the project, and comes " +
	"back as it was, read or not, once all of that holds again. Each change of the account's notifications, a new " +
	"one, an edit of a comment that it has one of, which changes its preview, a deletion or a restoration of such " +
	"a comment, and a change of their read state, sends " +
	"{\"type\": \"notifications\"} to each socket of the account's sessions of GET /api/v1/ws, which reads them " +
	"again."

// registerNotifications registers the routes of the signed-in account's
// notification centre (PRD FR-COMMENT-02), which take a session alone
// (FR-AUTH-04): what tells the account of the comments that mention it.
func registerNotifications(m *api.Mux, notifications *notification.Service) {
	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "listNotifications",
		Method:      http.MethodGet,
		Path:        "/api/v1/notifications",
		Summary:     "List the signed-in account's notifications",
		Description: "Lists the signed-in account's notifications that are shown, newest first, by when each was " +
			"made, as the server's clock has it, a page at a time across the account's workspaces. Each names the " +
			"comment that mentions the account, its thread, and the thread's target, with the target's board and " +
			"node, as the list of a project's comments names them, by which a client opens it, its project, who " +
			"mentioned the account, and a preview of the comment's body as it is now, with each member that it " +
			"mentions written as @ and their display name, and whether, and when, the account read it." +
			notificationsDoc + sessionAuthDoc,
		Tags: []string{"Notifications"},
	}, func(ctx context.Context, in *notificationsInput) (*notificationsOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		after, err := notificationCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := notifications.List(ctx, cur.userID, after, in.Fetch())
		if err != nil {
			return nil, err
		}
		page, err := api.NewPage(in.PageParams, list, func(n notification.NotificationBody) notificationKey {
			k := n.Key()
			return notificationKey{At: k.At.UTC(), ID: k.ID.String()}
		})
		if err != nil {
			return nil, err
		}
		return &notificationsOutput{Body: page}, nil
	}, api.SessionOnly())

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "countUnreadNotifications",
		Method:      http.MethodGet,
		Path:        "/api/v1/notifications/unread",
		Summary:     "Count the signed-in account's unread notifications",
		Description: "Returns how many of the signed-in account's notifications that are shown are unread, across " +
			"its workspaces, as a client's badge of the centre shows them." + notificationsDoc + sessionAuthDoc,
		Tags: []string{"Notifications"},
	}, func(ctx context.Context, _ *struct{}) (*unreadOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		n, err := notifications.Unread(ctx, cur.userID)
		if err != nil {
			return nil, err
		}
		return &unreadOutput{Body: unreadBody{Unread: n}}, nil
	}, api.SessionOnly())

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "readNotification",
		Method:      http.MethodPost,
		Path:        "/api/v1/notifications/{notification_id}/read",
		Summary:     "Mark a notification read",
		Description: "Marks one of the signed-in account's notifications read, shown or not, which a notification " +
			"read already stays as it is. A notification that the account does not have, in any of its " +
			"workspaces, is answered with 404 not_found." + notificationsDoc + csrfDoc,
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *readNotificationInput) (*struct{}, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Notification, in.NotificationID)
		if err != nil {
			return nil, err
		}
		return nil, notificationError(notifications.Read(ctx, cur.userID, id))
	}, api.SessionOnly())

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "readAllNotifications",
		Method:      http.MethodPost,
		Path:        "/api/v1/notifications/read-all",
		Summary:     "Mark the signed-in account's notifications read",
		Description: "Marks read the signed-in account's notifications that are unread, shown or not, in every " +
			"workspace of the account, that were made at or before the notification through, by when each was made " +
			"and by their IDs, as the list orders them, so that one that arrived after the client read the list " +
			"stays unread. A notification is made as the batch that makes it applies, as the server's clock has it, " +
			"so one of a batch of another project that applied before through's and committed only after the client " +
			"read the list is made before through, and is marked read too. A through that is not one of the account's notifications, in any of its workspaces, is " +
			"answered with 404 not_found, and marks nothing; one that does not decode as a notification's ID with " +
			"400 invalid_id." + notificationsDoc + csrfDoc,
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *readAllInput) (*struct{}, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		through, err := ids.Decode(ids.Notification, in.Body.Through)
		if err != nil {
			return nil, err
		}
		return nil, notificationError(notifications.ReadThrough(ctx, cur.userID, through))
	}, api.SessionOnly())
}

// notificationError returns the error that the API answers err with, an
// error of package notification, or err itself if it is none of them.
func notificationError(err error) error {
	if errors.Is(err, notification.ErrNotFound) {
		return api.NewError(api.CodeNotFound)
	}
	return err
}

// notificationCursor returns the key that cursor, a next_cursor of the
// notification centre, holds, or the zero key, before every notification,
// for the empty cursor of the first page. A cursor that holds no key, or not
// in the form that the server writes, its UUID in its canonical text and its
// time in UTC, is answered with invalid_cursor.
func notificationCursor(cursor string) (notification.Key, error) {
	if cursor == "" {
		return notification.Key{}, nil
	}
	key, err := api.DecodeCursor[notificationKey](cursor)
	if err != nil {
		return notification.Key{}, err
	}
	if key.At.IsZero() || key.At.Location() != time.UTC {
		return notification.Key{}, api.NewError(api.CodeInvalidCursor)
	}
	out := notification.Key{At: key.At}
	if err := cursorUUIDs(cursorUUID{key.ID, &out.ID}); err != nil {
		return notification.Key{}, err
	}
	return out, nil
}
