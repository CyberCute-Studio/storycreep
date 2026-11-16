package httpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/comment"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

type commentsInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Status    string `query:"status" enum:"open,resolved" doc:"Whether the threads listed are open or resolved. Without it, both."`
	Author    string `query:"author" doc:"The ID of a user: only the threads in which they wrote a comment that is not deleted are listed, as GET /api/v1/projects/{project_id}/comments/authors lists them. Without it, every thread."`
	Board     string `query:"board" doc:"The ID of a board: only the threads on the board, and on its nodes, its connections, and its variables, are listed. Without it, those of every board, and of the global variables."`
	Target    string `query:"target" doc:"The ID of a node, a connection, a board, or a variable: only the threads on it are listed, of a board those on the board itself, and not on its nodes. Another kind of ID is answered with 400 invalid_id. Without it, those of every target."`
	api.PageParams
}

type commentsOutput struct {
	Body api.Page[comment.CommentBody]
}

type commentCountsInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Board     string `query:"board" doc:"The ID of a board: only the board, and its nodes, its connections, and its variables, are counted. Without it, every target of the project."`
	api.PageParams
}

type commentCountsOutput struct {
	Body api.Page[comment.CommentCount]
}

type commentAuthorsInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	api.PageParams
}

type commentAuthorsOutput struct {
	Body api.Page[comment.CommentAuthor]
}

// commentKey is the key of a list of comments that its pages' cursors hold
// (comment.Key): when its thread was started, and when it was posted, in
// UTC, as the trash's key holds a time (trashKey), its thread's UUID and its
// own in their text form, and whether it is a reply.
type commentKey struct {
	ThreadAt time.Time `json:"thread_at"`
	Thread   string    `json:"thread"`
	Reply    bool      `json:"reply"`
	At       time.Time `json:"at"`
	ID       string    `json:"id"`
}

// commentCountKey is the key of a list of the counts of threads that its
// pages' cursors hold (comment.CountKey): the target's kind, and its UUID in
// its text form.
type commentCountKey struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// commentsDoc says, in the description of an operation that reads comments,
// which threads are shown, and how they change.
const commentsDoc = " A thread is shown while one of its comments is not deleted and its target is not in the " +
	"project's trash, nor there with what is, as a node with its board, or a connection with its node, and comes " +
	"back as it was once the target is restored (PRD FR-COMMENT-01 AC2). Comments change through the project's " +
	"command endpoint, POST /api/v1/projects/{project_id}/commands, whose commands of the type comment.* post, " +
	"edit, delete, and restore comments, and resolve and unresolve threads, which an owner, an admin, an editor, " +
	"or a commenter of the project may send, but an edit, a deletion, or a restoration of a comment its author " +
	"alone."

// registerComments registers the routes that read a project's threaded
// comments (PRD FR-COMMENT-01), which change through the command endpoint
// (registerCommands).
func registerComments(m *api.Mux, comments *comment.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listComments",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/comments",
		Summary:     "List a project's comments",
		Description: "Lists the comments of the project's threads that are shown and that the filters keep, thread " +
			"by thread, the newest thread first, by when its first comment was posted, and within a thread its " +
			"first comment, then its replies, oldest first, by when each was posted, as the server's clock has it " +
			"rather than the times of the IDs that the clients made (PRD FR-COMMENT-01). Each comment names its " +
			"thread's target, and the target's board and node, by which a client opens it, and a thread's first " +
			"comment whether the thread is resolved. A thread's first comment that is deleted while a reply of the " +
			"thread is not is listed as deleted, without its body and its author, and a deleted reply is not " +
			"listed." + commentsDoc + projectNotFoundDoc + tokenNotFoundDoc +
			" An author, a board, or a target whose ID does not decode, or is of another kind, is answered with 400 " +
			"invalid_id, and one that the project does not have keeps nothing." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Comments"},
	}, func(ctx context.Context, in *commentsInput) (*commentsOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		f := comment.Filter{Status: comment.Status(in.Status)}
		if f.Author, err = optionalID(ids.User, in.Author); err != nil {
			return nil, err
		}
		if f.Board, err = optionalID(ids.Board, in.Board); err != nil {
			return nil, err
		}
		if f.Target, err = commentTarget(in.Target); err != nil {
			return nil, err
		}
		after, err := commentCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := comments.List(ctx, actor, id, f, after, in.Fetch())
		if err != nil {
			return nil, projectError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(c comment.CommentBody) commentKey {
			k := c.Key()
			return commentKey{ThreadAt: k.ThreadAt.UTC(), Thread: k.Thread.String(), Reply: k.Reply, At: k.At.UTC(),
				ID: k.ID.String()}
		})
		if err != nil {
			return nil, err
		}
		return &commentsOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "countComments",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/comments/counts",
		Summary:     "Count the threads of a project's comments",
		Description: "Lists each node, connection, board, and variable of the project that a thread that is shown " +
			"is on, with how many of its threads are open and how many resolved, in the order of the targets' kinds " +
			"and IDs (PRD FR-COMMENT-01), as the canvas shows a node's, a connection's, or a board's, and the " +
			"variables' panel a variable's." + commentsDoc + projectNotFoundDoc + tokenNotFoundDoc + " A board " +
			"whose ID does not decode is answered with 400 invalid_id, and one that the project does not have keeps " +
			"nothing." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Comments"},
	}, func(ctx context.Context, in *commentCountsInput) (*commentCountsOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		board, err := optionalID(ids.Board, in.Board)
		if err != nil {
			return nil, err
		}
		after, err := commentCountCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := comments.Counts(ctx, actor, id, board, after, in.Fetch())
		if err != nil {
			return nil, projectError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(c comment.CommentCount) commentCountKey {
			k := c.Key()
			return commentCountKey{Type: string(k.Type), ID: k.ID.String()}
		})
		if err != nil {
			return nil, err
		}
		return &commentCountsOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listCommentAuthors",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/comments/authors",
		Summary:     "List the authors of a project's comments",
		Description: "Lists the users who wrote a comment of the project that is not deleted, of a thread that is " +
			"shown, each with their display name, in the order of their IDs (PRD FR-COMMENT-01), by which the list " +
			"of the project's comments is filtered, as a member who may not list the workspace's members, as a " +
			"guest, sees them too." + commentsDoc + projectNotFoundDoc + tokenNotFoundDoc +
			tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Comments"},
	}, func(ctx context.Context, in *commentAuthorsInput) (*commentAuthorsOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := comments.Authors(ctx, actor, id, after, in.Fetch())
		if err != nil {
			return nil, projectError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(a comment.CommentAuthor) idKey {
			return uuidKey(ids.User, a.ID)
		})
		if err != nil {
			return nil, err
		}
		return &commentAuthorsOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))
}

// commentTarget returns the target that s, the ID of a node, a connection, a
// board, or a variable, names, or none for the empty s. An ID that does not
// decode, or is of another kind, is answered with invalid_id, the latter
// with the reason wrong_prefix and a message that names the kinds that a
// target is of.
func commentTarget(s string) (comment.Target, error) {
	if s == "" {
		return comment.Target{}, nil
	}
	kind, u, err := ids.DecodeAny(s)
	if err != nil {
		return comment.Target{}, err
	}
	t := comment.TargetOf(kind)
	if t == "" {
		return comment.Target{}, &api.Error{Code: api.CodeInvalidID,
			Message: i18n.Message{Key: i18n.K("error.invalid_id.not_target"),
				Args: i18n.Args{"prefix": kind.Prefix(), "got": kind.String()}},
			Details: map[string]any{"reason": ids.WrongPrefix}}
	}
	return comment.Target{Type: t, ID: u}, nil
}

// commentCursor returns the key that cursor, a next_cursor of a list of
// comments, holds, or the zero key, before every comment, for the empty
// cursor of the first page. A cursor that holds no key, or not in the form
// that the server writes, each UUID in its canonical text and each time in
// UTC, is answered with invalid_cursor.
func commentCursor(cursor string) (comment.Key, error) {
	if cursor == "" {
		return comment.Key{}, nil
	}
	key, err := api.DecodeCursor[commentKey](cursor)
	if err != nil {
		return comment.Key{}, err
	}
	for _, at := range []time.Time{key.ThreadAt, key.At} {
		if at.IsZero() || at.Location() != time.UTC {
			return comment.Key{}, api.NewError(api.CodeInvalidCursor)
		}
	}
	out := comment.Key{ThreadAt: key.ThreadAt, Reply: key.Reply, At: key.At}
	if err := cursorUUIDs(cursorUUID{key.Thread, &out.Thread}, cursorUUID{key.ID, &out.ID}); err != nil {
		return comment.Key{}, err
	}
	return out, nil
}

// commentCountCursor returns the key that cursor, a next_cursor of a list of
// the counts of threads, holds, or the zero key, before every target, for the
// empty cursor of the first page. A cursor that holds no key, or not in the
// form that the server writes, a kind of target and a UUID in its canonical
// text, is answered with invalid_cursor.
func commentCountCursor(cursor string) (comment.CountKey, error) {
	if cursor == "" {
		return comment.CountKey{}, nil
	}
	key, err := api.DecodeCursor[commentCountKey](cursor)
	if err != nil {
		return comment.CountKey{}, err
	}
	out := comment.CountKey{Type: comment.TargetType(key.Type)}
	if out.Type.Kind() == 0 {
		return comment.CountKey{}, api.NewError(api.CodeInvalidCursor)
	}
	if err := cursorUUIDs(cursorUUID{key.ID, &out.ID}); err != nil {
		return comment.CountKey{}, err
	}
	return out, nil
}
