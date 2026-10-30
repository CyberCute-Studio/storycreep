package httpserver

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
)

type validationInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
}

type validationOutput struct {
	Body validation.Report
}

// registerValidation registers the route that validates a project's draft
// (PRD FR-DEP-03), whose findings' mutes the command endpoint changes
// (registerCommands).
func registerValidation(m *api.Mux, validations *validation.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "validateProject",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/validation",
		Summary:     "Validate a project",
		Description: "Validates the project's draft, as of one moment, and answers with its report (PRD FR-DEP-03): " +
			"every finding of the rules, each with its code, its severity, a message in the request's locale, and " +
			"the entity that it is about, with its board and its node, by which a client opens its editor (AC3). " +
			"An error blocks publishing (PRD FR-DEP-04), a warning needs an acknowledgement to publish, and an " +
			"info neither. A warning or an info may be muted on its entity, with a reason, by the commands " +
			"validation.mute and validation.unmute, and a muted finding is listed apart, in muted, with its mute " +
			"(AC4); an error is never muted. The report is one document, which the server computes anew for each " +
			"request, rather than a list of pages, so that its findings are all of one state of the draft, which " +
			"its seq names, as publishing stores it." + projectNotFoundDoc + tokenNotFoundDoc +
			tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Validation"},
	}, func(ctx context.Context, in *validationInput) (*validationOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		report, err := validations.Report(ctx, actor, id)
		if err != nil {
			return nil, projectError(err)
		}
		return &validationOutput{Body: report}, nil
	}, api.TokenScope(scope.ProjectRead))
}
