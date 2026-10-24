package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

type boardConnectionsInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	BoardID   string `path:"board_id" doc:"The board's ID"`
	api.PageParams
}

type connectionsOutput struct {
	Body api.Page[connection.Connection]
}

type connectionInput struct {
	ProjectID    string `path:"project_id" doc:"The project's ID"`
	ConnectionID string `path:"connection_id" doc:"The connection's ID"`
}

type connectionOutput struct {
	Body connection.Connection
}

// connectionChangesDoc says, in the description of an operation that reads
// connections, how they are changed.
const connectionChangesDoc = " They change through the project's command endpoint, POST " +
	"/api/v1/projects/{project_id}/commands, whose commands of the type connection.* create, relabel, reconnect, " +
	"reorder, delete, and restore them."

// registerConnections registers the routes that read a project's
// connections (PRD FR-EDIT-03), which change through the command endpoint
// (registerCommands).
func registerConnections(m *api.Mux, connections *connection.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listBoardConnections",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/boards/{board_id}/connections",
		Summary:     "List a board's connections",
		Description: "Lists the board's connections that are not in the project's trash, each with its label, in the " +
			"order of their IDs, oldest first: the arrows that the board's canvas shows between its nodes. A " +
			"connection of a node in the trash is in the trash with it, and is not listed until the node is " +
			"restored. The connections that leave a node are the choices that it offers, in the order of their " +
			"order, then of their IDs without the prefix, cn_, each compared by its bytes." + connectionChangesDoc +
			projectNotFoundDoc + tokenNotFoundDoc + " A board that the project does not have, or that is in its " +
			"trash, is answered with 404 not_found." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Connections"},
	}, func(ctx context.Context, in *boardConnectionsInput) (*connectionsOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		boardID, err := ids.Decode(ids.Board, in.BoardID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := connections.BoardConnections(ctx, actor, id, boardID, after, in.Fetch())
		if err != nil {
			return nil, connectionError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(c connection.Connection) idKey {
			return uuidKey(ids.Connection, c.ID)
		})
		if err != nil {
			return nil, err
		}
		return &connectionsOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getConnection",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/connections/{connection_id}",
		Summary:     "Get a connection",
		Description: "Returns a connection of the project, with its label." + connectionChangesDoc +
			projectNotFoundDoc + tokenNotFoundDoc + " A connection that the project does not have, or that is in " +
			"its trash, itself or with one of its nodes or its board, is answered with 404 not_found." +
			tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Connections"},
	}, func(ctx context.Context, in *connectionInput) (*connectionOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		connectionID, err := ids.Decode(ids.Connection, in.ConnectionID)
		if err != nil {
			return nil, err
		}
		c, err := connections.Get(ctx, actor, id, connectionID)
		if err != nil {
			return nil, connectionError(err)
		}
		return &connectionOutput{Body: c}, nil
	}, api.TokenScope(scope.ProjectRead))
}

// connectionError returns the error that the API answers err with, an error
// of package connection, of a board that it reads the connections of, or of
// the project that it reads them in (projectError), or err itself if it is
// none of them.
func connectionError(err error) error {
	if errors.Is(err, connection.ErrNotFound) || errors.Is(err, board.ErrNotFound) {
		return api.NewError(api.CodeNotFound)
	}
	return projectError(err)
}
