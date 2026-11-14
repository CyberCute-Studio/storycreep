package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/statistics"
)

type projectStatisticsInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
}

type projectStatisticsOutput struct {
	Body *statistics.ProjectStatistics
}

type nodeStatisticsInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	NodeID    string `path:"node_id" doc:"The node's ID"`
}

type nodeStatisticsOutput struct {
	Body *statistics.NodeStatistics
}

// statisticsDoc says, in the descriptions of the statistics' operations,
// what they count, and of which content.
const statisticsDoc = " A text is an element's text or the label of a connection, whose words and characters are " +
	"counted as PRD FR-TEXT-05 and FR-TEXT-06 define them, a mention as its component's name and a value " +
	"reference as its name in braces, once, as it changes, rather than at each read, so that the statistics hold " +
	"an edit as soon as its batch is answered (FR-TEXT-06 AC1). They are of what is not in the project's trash, " +
	"and of the project's draft as of one moment, its seq, so that a batch that commits meanwhile is in them " +
	"whole or not at all."

// registerStatistics registers the routes that read the writing statistics of
// a project's content (PRD FR-TEXT-06), which the command endpoint keeps as
// it changes the content (registerCommands).
func registerStatistics(m *api.Mux, stats *statistics.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getProjectStatistics",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/statistics",
		Summary:     "Get a project's writing statistics",
		Description: "Returns the writing statistics of the project and of each of its boards: how many words and " +
			"characters their texts have, and how many nodes, choices, and conditions they have, the boards' " +
			"adding up to the project's." + statisticsDoc + projectNotFoundDoc + tokenNotFoundDoc +
			tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Statistics"},
	}, func(ctx context.Context, in *projectStatisticsInput) (*projectStatisticsOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		out, err := stats.Project(ctx, actor, id)
		if err != nil {
			return nil, projectError(err)
		}
		return &projectStatisticsOutput{Body: out}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getNodeStatistics",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/nodes/{node_id}/statistics",
		Summary:     "Get a node's writing statistics",
		Description: "Returns the writing statistics of the node: how many words and characters its text and the " +
			"labels of the connections that leave it have, and how many of those connections are choices, and " +
			"how many conditions they and the node's outputs have, which add up, over a board's nodes, to the " +
			"board's statistics (GET /api/v1/projects/{project_id}/statistics)." + statisticsDoc +
			projectNotFoundDoc + tokenNotFoundDoc + " A node that the project does not have, or that is in its " +
			"trash, itself or with its board, is answered with 404 not_found." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Statistics"},
	}, func(ctx context.Context, in *nodeStatisticsInput) (*nodeStatisticsOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		nodeID, err := ids.Decode(ids.Node, in.NodeID)
		if err != nil {
			return nil, err
		}
		out, err := stats.Node(ctx, actor, id, nodeID)
		switch {
		case errors.Is(err, statistics.ErrNotFound):
			return nil, api.NewError(api.CodeNotFound)
		case err != nil:
			return nil, projectError(err)
		}
		return &nodeStatisticsOutput{Body: out}, nil
	}, api.TokenScope(scope.ProjectRead))
}
