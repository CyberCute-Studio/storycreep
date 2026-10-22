package httpserver

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/trash"
)

type projectTrashInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	api.PageParams
}

type projectTrashOutput struct {
	Body api.Page[trashItemBody]
}

// trashItemBody is an entry of a project's trash, as the API lists it: a
// trash.Item, whose fields and their documentation it has, under a name of
// the API's document that says what it is.
type trashItemBody trash.Item

// trashKey is the key that the list of a project's trash is paged by: an
// entry's deletion, in UTC, its kind, and its UUID, as the list orders them.
type trashKey struct {
	DeletedAt time.Time `json:"deleted_at"`
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
}

// registerTrash registers the route that lists a project's trash (PRD
// FR-PROJ-06), from which the command endpoint's commands of each kind
// restore what it holds.
func registerTrash(m *api.Mux, items *trash.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listTrash",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/trash",
		Summary:     "List a project's trash",
		Description: "Lists what is in the project's trash, the most recently deleted first: its boards, and, once " +
			"they exist, its nodes, variables, and components, each with its kind. Each stays in the trash for " +
			strconv.Itoa(trash.Days) + " days from its deletion, until its purge_after, when a purge that runs every " +
			"hour deletes it for good; until then, the command of its kind restores it where it was, as " +
			"board.restore with its ID, through POST /api/v1/projects/{project_id}/commands. A deleted folder is " +
			"not listed: restoring a board restores the deleted folders above it, and the purge deletes a deleted " +
			"folder once its " + strconv.Itoa(trash.Days) + " days have passed and it holds nothing. Nothing in the " +
			"trash is in the project's other reads." +
			projectNotFoundDoc + tokenNotFoundDoc + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Trash"},
	}, func(ctx context.Context, in *projectTrashInput) (*projectTrashOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		after, err := trashCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := items.List(ctx, actor, id, after, in.Fetch())
		if err != nil {
			return nil, projectError(err)
		}
		bodies := make([]trashItemBody, len(list))
		for i, it := range list {
			bodies[i] = trashItemBody(it)
		}
		page, err := api.NewPage(in.PageParams, bodies, func(it trashItemBody) trashKey {
			after := trash.Item(it).After()
			return trashKey{DeletedAt: after.DeletedAt.UTC(), Kind: after.Kind, ID: after.ID.String()}
		})
		if err != nil {
			return nil, err
		}
		return &projectTrashOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))
}

// trashCursor returns where the page of the trash's list that cursor, a
// next_cursor of it, starts, or the zero trash.Cursor for the empty cursor
// of the first page. A cursor that holds no entry of the trash, or not in
// the form that the server writes, is answered with invalid_cursor.
func trashCursor(cursor string) (trash.Cursor, error) {
	if cursor == "" {
		return trash.Cursor{}, nil
	}
	key, err := api.DecodeCursor[trashKey](cursor)
	if err != nil {
		return trash.Cursor{}, err
	}
	id, err := ids.ParseUUID(key.ID)
	if err != nil || id.String() != key.ID || key.DeletedAt.IsZero() || key.DeletedAt.Location() != time.UTC ||
		!trash.IsKind(key.Kind) {
		return trash.Cursor{}, api.NewError(api.CodeInvalidCursor)
	}
	return trash.Cursor{DeletedAt: key.DeletedAt, Kind: key.Kind, ID: id}, nil
}
