package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

type boardNodesInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	BoardID   string `path:"board_id" doc:"The board's ID"`
	api.PageParams
}

type nodesOutput struct {
	Body api.Page[node.Node]
}

type nodeInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	NodeID    string `path:"node_id" doc:"The node's ID"`
}

type nodeOutput struct {
	Body node.Node
}

type nodeReferrersInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	NodeID    string `path:"node_id" doc:"The node's ID"`
	api.PageParams
}

type tagsInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Prefix    string `query:"prefix" maxLength:"50" doc:"What the tags start with, compared without regard to case, of at most 50 characters, without control characters. Without it, every tag."`
	Limit     int    `query:"limit" minimum:"1" maximum:"100" default:"20" doc:"How many tags the answer holds at most"`
}

type tagsBody struct {
	Items []node.Tag `json:"items" nullable:"false" doc:"The tags, the most used first, then in the order of their folded spellings"`
}

type tagsOutput struct {
	Body tagsBody
}

// nodeChangesDoc says, in the description of an operation that reads nodes,
// how they are changed.
const nodeChangesDoc = " They change through the project's command endpoint, POST " +
	"/api/v1/projects/{project_id}/commands, whose commands of the type node.* create, change, move, duplicate, " +
	"delete, and restore them."

// registerNodes registers the routes that read a project's nodes (PRD
// FR-EDIT-02, FR-EDIT-05, FR-EDIT-06), which change through the command
// endpoint (registerCommands).
func registerNodes(m *api.Mux, nodes *node.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listBoardNodes",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/boards/{board_id}/nodes",
		Summary:     "List a board's nodes",
		Description: "Lists the board's nodes that are not in the project's trash, each whole, an element with its " +
			"text, in the order of their IDs, oldest first: what the board's canvas shows." + nodeChangesDoc +
			projectNotFoundDoc + tokenNotFoundDoc + " A board that the project does not have, or that is in its " +
			"trash, is answered with 404 not_found." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Nodes"},
	}, func(ctx context.Context, in *boardNodesInput) (*nodesOutput, error) {
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
		list, err := nodes.BoardNodes(ctx, actor, id, boardID, after, in.Fetch())
		if err != nil {
			return nil, nodeError(err)
		}
		return nodePage(in.PageParams, list)
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getNode",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/nodes/{node_id}",
		Summary:     "Get a node",
		Description: "Returns a node of the project, an element with its text." + nodeChangesDoc +
			projectNotFoundDoc + tokenNotFoundDoc + " A node that the project does not have, or that is in its " +
			"trash, itself or with its board, is answered with 404 not_found." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Nodes"},
	}, func(ctx context.Context, in *nodeInput) (*nodeOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		nodeID, err := ids.Decode(ids.Node, in.NodeID)
		if err != nil {
			return nil, err
		}
		n, err := nodes.Get(ctx, actor, id, nodeID)
		if err != nil {
			return nil, nodeError(err)
		}
		return &nodeOutput{Body: n}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listNodeReferrers",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/nodes/{node_id}/referenced-by",
		Summary:     "List the jumpers that target a node",
		Description: "Lists the jumpers of the project that target the node, on any of its boards, the node's own " +
			"among them, that are not in its trash, in the order of their IDs, oldest first: what the node is " +
			"referenced by (PRD FR-EDIT-05 AC2)." + nodeChangesDoc + projectNotFoundDoc + tokenNotFoundDoc +
			" A node that the project does not have, or that is in its trash, itself or with its board, is " +
			"answered with 404 not_found." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Nodes"},
	}, func(ctx context.Context, in *nodeReferrersInput) (*nodesOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		nodeID, err := ids.Decode(ids.Node, in.NodeID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := nodes.ReferencedBy(ctx, actor, id, nodeID, after, in.Fetch())
		if err != nil {
			return nil, nodeError(err)
		}
		return nodePage(in.PageParams, list)
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listBoardReferrers",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/boards/{board_id}/referenced-by",
		Summary:     "List the jumpers that target a board's nodes",
		Description: "Lists the jumpers on the project's other boards, which are not in its trash, that target a " +
			"node of the board that is not in its trash either, in the order of their IDs, oldest first: those " +
			"that deleting the board would leave dangling, which a client warns of before it deletes the board, " +
			"as the command board.delete, which refuses an unconfirmed deletion of such a board with 409 " +
			"board_referenced, asks it to (PRD FR-PROJ-02 AC2). A jumper whose target is deleted already, or " +
			"purged, dangles already, and is not listed." + nodeChangesDoc + projectNotFoundDoc + tokenNotFoundDoc + " A board that the " +
			"project does not have, or that is in its trash, is answered with 404 not_found." +
			tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Nodes"},
	}, func(ctx context.Context, in *boardNodesInput) (*nodesOutput, error) {
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
		list, err := nodes.BoardReferencedBy(ctx, actor, id, boardID, after, in.Fetch())
		if err != nil {
			return nil, nodeError(err)
		}
		return nodePage(in.PageParams, list)
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listTags",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/tags",
		Summary:     "Suggest a project's tags",
		Description: "Lists the tags of the project's nodes that are not in its trash that start with the prefix, " +
			"compared without regard to case, as tags are, each once, as the most of the nodes that have it write " +
			"it, with how many nodes have it, the most used first, so that a client suggests the spelling that " +
			"the project uses as a tag is typed (PRD FR-EDIT-06)." + nodeChangesDoc + projectNotFoundDoc +
			tokenNotFoundDoc + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Nodes"},
	}, func(ctx context.Context, in *tagsInput) (*tagsOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		list, err := nodes.Tags(ctx, actor, id, in.Prefix, in.Limit)
		if err != nil {
			return nil, nodeError(err)
		}
		return &tagsOutput{Body: tagsBody{Items: list}}, nil
	}, api.TokenScope(scope.ProjectRead))
}

// nodePage returns the page of the nodes list, which a query read with
// p.Fetch() as its limit.
func nodePage(p api.PageParams, list []node.Node) (*nodesOutput, error) {
	page, err := api.NewPage(p, list, func(n node.Node) idKey { return uuidKey(ids.Node, n.ID) })
	if err != nil {
		return nil, err
	}
	return &nodesOutput{Body: page}, nil
}

// nodeError returns the error that the API answers err with, an error of
// package node, of a board that it reads the nodes of, or of the project
// that it reads them in (projectError), or err itself if it is none of them.
// A problem of a query's parameter is answered at the query.
func nodeError(err error) error {
	var fields input.FieldErrors
	switch {
	case errors.Is(err, node.ErrNotFound), errors.Is(err, board.ErrNotFound):
		return api.NewError(api.CodeNotFound)
	case errors.As(err, &fields):
		problems := make([]api.Problem, len(fields))
		for i, f := range fields {
			problems[i] = api.Problem{Location: "query." + f.Field, Message: f.Message}
		}
		return &api.Error{Code: api.CodeValidationFailed, Details: map[string]any{"errors": problems}}
	}
	return projectError(err)
}
