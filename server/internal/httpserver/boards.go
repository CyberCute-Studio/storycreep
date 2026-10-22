package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

type boardsInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	api.PageParams
}

type boardsOutput struct {
	Body api.Page[board.Board]
}

type boardInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	BoardID   string `path:"board_id" doc:"The board's ID"`
}

type boardOutput struct {
	Body board.Board
}

type foldersInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Kind      string `query:"kind" enum:"board,variable,component" doc:"What the folders hold: boards, variables, or components. Without it, folders of every kind."`
	api.PageParams
}

type foldersOutput struct {
	Body api.Page[board.Folder]
}

// boardChangesDoc says, in the description of an operation that reads boards
// or folders, how they are changed.
const boardChangesDoc = " They change through the project's command endpoint, POST /api/v1/projects/{project_id}/commands, " +
	"whose commands of the types board.* and folder.* create, rename, move, delete, and restore them."

// registerBoards registers the routes that read a project's boards and
// folders (PRD FR-PROJ-02), which change through the command endpoint
// (registerCommands).
func registerBoards(m *api.Mux, boards *board.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listBoards",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/boards",
		Summary:     "List a project's boards",
		Description: "Lists the project's boards that are not in its trash, in the order of their IDs, oldest first. " +
			"Each names its folder and its place among the folder's children, from which a client builds the tree of " +
			"folders (GET /api/v1/projects/{project_id}/folders)." + boardChangesDoc + projectNotFoundDoc +
			tokenNotFoundDoc + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Boards"},
	}, func(ctx context.Context, in *boardsInput) (*boardsOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := boards.List(ctx, actor, id, after, in.Fetch())
		if err != nil {
			return nil, boardError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(b board.Board) idKey { return uuidKey(ids.Board, b.ID) })
		if err != nil {
			return nil, err
		}
		return &boardsOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getBoard",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/boards/{board_id}",
		Summary:     "Get a board",
		Description: "Returns a board of the project." + boardChangesDoc + projectNotFoundDoc + tokenNotFoundDoc +
			" A board that the project does not have, or that is in its trash, is answered with 404 not_found." +
			tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Boards"},
	}, func(ctx context.Context, in *boardInput) (*boardOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		boardID, err := ids.Decode(ids.Board, in.BoardID)
		if err != nil {
			return nil, err
		}
		b, err := boards.Get(ctx, actor, id, boardID)
		if err != nil {
			return nil, boardError(err)
		}
		return &boardOutput{Body: b}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listFolders",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/folders",
		Summary:     "List a project's folders",
		Description: "Lists the project's folders that are not deleted, of boards, variables, and components, or of " +
			"the kind that kind names, in the order of their IDs, oldest first. Each names its parent and its place " +
			"among the parent's children, from which a client builds each kind's tree." + boardChangesDoc +
			projectNotFoundDoc + tokenNotFoundDoc + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Boards"},
	}, func(ctx context.Context, in *foldersInput) (*foldersOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := boards.Folders(ctx, actor, id, in.Kind, after, in.Fetch())
		if err != nil {
			return nil, boardError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(f board.Folder) idKey { return uuidKey(ids.Folder, f.ID) })
		if err != nil {
			return nil, err
		}
		return &foldersOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))
}

// uuidKey returns the key that a list of entities of the kind is paged by:
// the UUID of the entity whose ID is id, in the form that cursorID reads,
// rather than its ID as the API writes it.
func uuidKey(kind ids.Kind, id string) idKey {
	// The ID is the service's encoding of the entity's UUID, which decodes.
	u, _ := ids.Decode(kind, id)
	return idKey{ID: u.String()}
}

// boardError returns the error that the API answers err with, an error of
// package board or of the project that it reads the boards of (projectError),
// or err itself if it is none of them.
func boardError(err error) error {
	if errors.Is(err, board.ErrNotFound) {
		return api.NewError(api.CodeNotFound)
	}
	return projectError(err)
}
