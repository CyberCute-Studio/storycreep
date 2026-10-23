package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

type variablesInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	api.PageParams
}

type variablesOutput struct {
	Body api.Page[variable.Variable]
}

type variableInput struct {
	ProjectID  string `path:"project_id" doc:"The project's ID"`
	VariableID string `path:"variable_id" doc:"The variable's ID"`
}

type variableOutput struct {
	Body variable.Variable
}

// variableChangesDoc says, in the description of an operation that reads
// variables, how they are changed.
const variableChangesDoc = " They change through the project's command endpoint, POST " +
	"/api/v1/projects/{project_id}/commands, whose commands of the type variable.* create, rename, describe, " +
	"retype, rescope, move, delete, and restore them, and change their defaults and reset_on_enter."

// registerVariables registers the routes that read a project's variables
// (PRD FR-VAR-01, FR-VAR-02), which change through the command endpoint
// (registerCommands).
func registerVariables(m *api.Mux, variables *variable.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listVariables",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/variables",
		Summary:     "List a project's variables",
		Description: "Lists the project's variables that are not in its trash, global ones and those of its boards, " +
			"but not those of a board in the trash, which are in the trash with it, in the order of their IDs, oldest " +
			"first. Each names its folder and its place among the folder's children, from which a client builds the " +
			"tree of folders of variables (GET /api/v1/projects/{project_id}/folders?kind=variable)." +
			variableChangesDoc + projectNotFoundDoc + tokenNotFoundDoc + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Variables"},
	}, func(ctx context.Context, in *variablesInput) (*variablesOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := variables.List(ctx, actor, id, after, in.Fetch())
		if err != nil {
			return nil, variableError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(v variable.Variable) idKey {
			return uuidKey(ids.Variable, v.ID)
		})
		if err != nil {
			return nil, err
		}
		return &variablesOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getVariable",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/variables/{variable_id}",
		Summary:     "Get a variable",
		Description: "Returns a variable of the project." + variableChangesDoc + projectNotFoundDoc + tokenNotFoundDoc +
			" A variable that the project does not have, or that is in its trash, as one of a board in the trash is, " +
			"is answered with 404 not_found." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Variables"},
	}, func(ctx context.Context, in *variableInput) (*variableOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		variableID, err := ids.Decode(ids.Variable, in.VariableID)
		if err != nil {
			return nil, err
		}
		v, err := variables.Get(ctx, actor, id, variableID)
		if err != nil {
			return nil, variableError(err)
		}
		return &variableOutput{Body: v}, nil
	}, api.TokenScope(scope.ProjectRead))
}

// variableError returns the error that the API answers err with, an error of
// package variable or of the project that it reads the variables of
// (projectError), or err itself if it is none of them.
func variableError(err error) error {
	if errors.Is(err, variable.ErrNotFound) {
		return api.NewError(api.CodeNotFound)
	}
	return projectError(err)
}
