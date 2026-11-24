package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/asset"
	"github.com/CyberCute-Studio/storycreep/server/internal/authoring"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// duplicateProjectBody is the body of POST
// /api/v1/projects/{project_id}/duplicate.
type duplicateProjectBody struct {
	Name string `json:"name" maxLength:"100" doc:"The copy's name, as a new project's for POST /api/v1/workspaces/{workspace_id}/projects: of 1 to 100 characters, not all of them blank, and without control characters, bidirectional controls, and line separators among them. White space around it is removed. The copy's slug is derived from it."`
}

type duplicateProjectInput struct {
	CSRFInput
	ProjectID string `path:"project_id" doc:"The ID of the project to duplicate"`
	Body      duplicateProjectBody
}

type sampleProjectInput struct {
	CSRFInput
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
}

// copySlots is how many copies of projects a process makes at once, of which
// each user's is one at a time (slots): a copy is an import of the project's
// archive, which holds as much memory as an import's job, which runs one at a
// time in each process (jobs.Serial), and holds a connection of the requests'
// pool for as long as its transaction takes, so that copies of large projects
// asked for at once neither run the server out of memory nor take every
// connection from the other requests. Two, so that one long copy does not
// keep every other user's waiting. A copy that waits for its turn waits
// before its request's body is read (holdSlot).
const copySlots = 2

// registerDuplicates registers the routes that create a project from content
// that the server has already (PRD FR-PROJ-01, FR-PROJ-05, plan note N92): a
// copy of a project, and the sample project. Each creates a project, so it is
// the workspace's owners' and admins' and takes a session alone, as creating
// and importing a project do (§5.6), and each counts in the bucket of exports
// and imports, as it is one. A copy waits for one of copySlots; the sample,
// whose archive is small, waits for nothing.
func registerDuplicates(m *api.Mux, imports *authoring.Service) {
	copies := newSlots(copySlots)
	api.Register(m, api.ManageProjects, huma.Operation{
		OperationID: "duplicateProject",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects/{project_id}/duplicate",
		Summary:     "Duplicate a project",
		Description: "Copies the project into a new project of its workspace, with fresh IDs, and answers 201 with " +
			"the copy, whose duplicated_from names the project (PRD FR-PROJ-01). The copy holds the project's draft " +
			"as a content-only authoring export of it with its assets holds it: its folders, boards, variables, " +
			"component types and components, nodes, connections, muted findings, and assets, outside the trash, and " +
			"not its comments, play states, or releases. It is made whole or not at all, in the request. An archived " +
			"project may be duplicated, and its copy is not archived. Only an owner or an admin of the workspace may, " +
			"and anyone else with a role on the project is answered with 403 insufficient_role. A name that its field " +
			"does not allow is answered with 422 validation_failed, a workspace that has as many projects as its plan " +
			"allows, or no room for the copy's assets, with 409 limit_exceeded or storage_limit_exceeded, and a " +
			"project that the copy's import refuses, as one of more assets than an import takes, with 409 " +
			"duplicate_refused. Until the instance's first-run setup has completed, the answer is 403 setup_required, " +
			"unless STORYCREEP_ASSET_STORAGE names where files are stored." + projectNotFoundDoc + csrfDoc,
		Tags:          []string{"Projects"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *duplicateProjectInput) (*createdProjectOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		p, err := imports.Duplicate(ctx, actor, id, in.Body.Name)
		if err != nil {
			return nil, duplicateError(err)
		}
		return &createdProjectOutput{Location: "/api/v1/projects/" + p.ID, Body: newProjectBody(p)}, nil
	}, api.SessionOnly(), api.RateLimit(api.BucketExport), api.AfterLimit(holdSlot(copies)))

	api.Register(m, api.ManageProjects, huma.Operation{
		OperationID: "importSampleProject",
		Method:      http.MethodPost,
		Path:        "/api/v1/workspaces/{workspace_id}/sample-project",
		Summary:     "Open the sample project",
		Description: "Imports the sample project, The Lighthouse Keeper, as a new project of the workspace, with " +
			"fresh IDs, and answers 201 with it (PRD FR-PROJ-05). The sample is an authoring export that the server " +
			"holds: three boards of a short story, with variables, components, a quest, gating across boards, and a " +
			"warning of the validation to look at. Each request adds another project. Only an owner or an admin of " +
			"the workspace may, and anyone else with a role in it is answered with 403 insufficient_role; a " +
			"workspace that has as many projects as its plan allows is answered with 409 limit_exceeded." +
			notFoundDoc + csrfDoc,
		Tags:          []string{"Projects"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *sampleProjectInput) (*createdProjectOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		ws, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		p, err := imports.ImportSample(ctx, cur.actor(ctx), ws)
		if err != nil {
			return nil, projectError(err)
		}
		return &createdProjectOutput{Location: "/api/v1/projects/" + p.ID, Body: newProjectBody(p)}, nil
	}, api.SessionOnly(), api.RateLimit(api.BucketExport))
}

// duplicateError returns the error that the API answers err with, an error
// of authoring.Service.Duplicate.
func duplicateError(err error) error {
	var refused *authoring.DuplicateRefusedError
	switch {
	case errors.As(err, &refused):
		f := refused.Failure
		failure := map[string]any{"code": f.Code}
		for k, v := range map[string]string{"section": f.Section, "check": f.Check, "detail": f.Detail} {
			if v != "" {
				failure[k] = v
			}
		}
		return &api.Error{Code: api.CodeDuplicateRefused, Details: map[string]any{"failure": failure}}
	case errors.Is(err, asset.ErrSetupRequired):
		return api.NewError(api.CodeSetupRequired)
	}
	return projectError(err)
}
