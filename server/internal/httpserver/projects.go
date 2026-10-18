package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// projectBody is a project, with the role that the signed-in user acts with
// on it.
type projectBody struct {
	project.Project
	Role string `json:"role" enum:"owner,admin,editor,commenter,viewer" doc:"The role that the signed-in user acts with on the project: their grant on it, or else their role in its workspace, or owner for an instance admin when STORYCREEP_INSTANCE_ADMIN_ACCESS is on (PRD §5.5)"`
}

func newProjectBody(p project.Visible) projectBody {
	return projectBody{Project: p.Project, Role: string(p.Role)}
}

type projectOutput struct {
	Body projectBody
}

type projectsInput struct {
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	api.PageParams
}

type projectsOutput struct {
	Body api.Page[projectBody]
}

type trashOutput struct {
	Body api.Page[project.Project]
}

// createProjectBody is the body of POST
// /api/v1/workspaces/{workspace_id}/projects.
type createProjectBody struct {
	Name          string `json:"name" maxLength:"100" doc:"The project's name, of 1 to 100 characters, not all of them blank, and without control characters, bidirectional controls, and line separators among them. White space around it is removed."`
	Description   string `json:"description,omitempty" maxLength:"2000" doc:"The project's description, of at most 2000 characters, without control characters but line feeds and tabs. White space around it is removed, and a line break written as CR LF, or as a CR alone, is kept as a line feed. Without it, the project has none."`
	DefaultLocale string `json:"default_locale,omitempty" maxLength:"64" doc:"The locale of the project's text, as a BCP 47 tag of a language, such as en or cs-CZ, which the project keeps in its canonical form. Without it, the workspace's default locale."`
}

type createProjectInput struct {
	CSRFInput
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	Body        createProjectBody
}

type createdProjectOutput struct {
	Location string `header:"Location" doc:"The project's URL"`
	Body     projectBody
}

type projectInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
}

type changeProjectInput struct {
	CSRFInput
	ProjectID string `path:"project_id" doc:"The project's ID"`
}

// tokenChangeProjectInput is changeProjectInput of a route that takes an API
// token, which needs no CSRF token.
type tokenChangeProjectInput struct {
	SessionCSRFInput
	ProjectID string `path:"project_id" doc:"The project's ID"`
}

// updateProjectBody is the body of PATCH /api/v1/projects/{project_id}. A
// field that it leaves out, or sets to null, which huma takes for a field
// left out, stays as it is.
type updateProjectBody struct {
	Name           *string               `json:"name,omitempty" maxLength:"100" doc:"The project's new name, as for POST /api/v1/workspaces/{workspace_id}/projects"`
	Description    *string               `json:"description,omitempty" maxLength:"2000" doc:"The project's new description, as for POST /api/v1/workspaces/{workspace_id}/projects; empty for none"`
	DefaultLocale  *string               `json:"default_locale,omitempty" maxLength:"64" doc:"The new locale of the project's text, as a BCP 47 tag of a language"`
	PublishMinRole *string               `json:"publish_min_role,omitempty" enum:"editor,admin" doc:"The least role that may publish the project's snapshots: editor, or admin to keep editors from publishing (PRD FR-SNAP-01 AC3). A change is recorded in the audit log."`
	Entry          *project.ProjectEntry `json:"entry,omitempty" doc:"The project's new entry, which replaces its entry: an object without board_id removes it. node_id needs board_id. Neither the board nor the node needs to exist yet."`
	ExpectedRev    int64                 `json:"expected_rev,omitempty" minimum:"1" doc:"The project's revision that the change was made against. If the project has another, the answer is 409 revision_conflict, and nothing changes. Without it, the change is made whatever the revision. A body that names no field to change changes nothing, so it is answered with the project, whatever its revision."`
}

type updateProjectInput struct {
	SessionCSRFInput
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Body      updateProjectBody
}

// projectNotFoundDoc says, in the description of an operation on a project,
// when the operation answers that the project does not exist.
const projectNotFoundDoc = " A project that does not exist, is in the trash, or on which the signed-in user's role " +
	"is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is " +
	"answered with 404 not_found, so that the answer does not tell whether it exists."

// archivedDoc says, in the description of an operation that changes a
// project, that an archived project refuses it.
const archivedDoc = " An archived project is read-only, and the change is answered with 409 project_archived."

// registerProjects registers the routes of projects (PRD FR-PROJ-01).
func registerProjects(m *api.Mux, projects *project.Service) {
	api.Register(m, api.ListProjects, huma.Operation{
		OperationID: "listProjects",
		Method:      http.MethodGet,
		Path:        "/api/v1/workspaces/{workspace_id}/projects",
		Summary:     "List a workspace's projects",
		Description: "Lists the workspace's projects that are not in its trash, archived ones included, in the order " +
			"of their IDs, oldest first, each with the role that the signed-in user acts with on it. A guest is " +
			"shown only the projects granted to them. A workspace that does not exist, or of which the signed-in " +
			"user is neither a member nor an instance admin who acts as its owner, as with " +
			"STORYCREEP_INSTANCE_ADMIN_ACCESS on, is answered with 404 not_found, so that the answer does not tell " +
			"whether it exists. So is a workspace other than the one that the request's API token belongs to, and a " +
			"token narrowed to a project is shown that project alone (PRD FR-AUTH-04 AC3)." +
			tokenAuthDoc(scope.SnapshotRead),
		Tags: []string{"Projects"},
	}, func(ctx context.Context, in *projectsInput) (*projectsOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		ws, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := projects.List(ctx, cur.actor(ctx), ws, after, in.Fetch())
		if err != nil {
			return nil, projectError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(p project.Visible) idKey { return projectKey(p.Project) })
		if err != nil {
			return nil, err
		}
		out := &projectsOutput{Body: api.Page[projectBody]{Items: make([]projectBody, len(page.Items)),
			NextCursor: page.NextCursor}}
		for i, p := range page.Items {
			out.Body.Items[i] = newProjectBody(p)
		}
		return out, nil
	}, api.TokenScope(scope.SnapshotRead))

	api.Register(m, api.ManageProjects, huma.Operation{
		OperationID: "createProject",
		Method:      http.MethodPost,
		Path:        "/api/v1/workspaces/{workspace_id}/projects",
		Summary:     "Create a project",
		Description: "Creates a project in the workspace. Only an owner or an admin of the workspace may, and anyone " +
			"else with a role in it is answered with 403 insufficient_role." + notFoundDoc + " A name, a " +
			"description, or a default locale that its field does not allow is answered with 422 " +
			"validation_failed, and a workspace that has as many projects as its plan allows with 409 " +
			"limit_exceeded; archived projects and those in the trash do not count." + csrfDoc,
		Tags:          []string{"Projects"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createProjectInput) (*createdProjectOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		ws, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		p, err := projects.Create(ctx, cur.session.UserID, ws, project.Settings{Name: in.Body.Name,
			Description: in.Body.Description, DefaultLocale: in.Body.DefaultLocale}, clientFrom(ctx))
		if err != nil {
			return nil, projectError(err)
		}
		return &createdProjectOutput{Location: "/api/v1/projects/" + p.ID, Body: newProjectBody(p)}, nil
	}, api.SessionOnly())

	api.Register(m, api.ManageProjects, huma.Operation{
		OperationID: "listTrashedProjects",
		Method:      http.MethodGet,
		Path:        "/api/v1/workspaces/{workspace_id}/trash/projects",
		Summary:     "List the projects in a workspace's trash",
		Description: "Lists the workspace's projects that were deleted and can still be restored, in the order of " +
			"their IDs, oldest first. A deleted project stays in the trash for 30 days, until its " +
			"hard_delete_after, and is then deleted for good, with all of its content. Only an owner or an admin of " +
			"the workspace may list them, and anyone else with a role in it is answered with 403 " +
			"insufficient_role." + notFoundDoc + sessionAuthDoc,
		Tags: []string{"Projects"},
	}, func(ctx context.Context, in *projectsInput) (*trashOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		ws, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := projects.Trash(ctx, cur.session.UserID, ws, after, in.Fetch())
		if err != nil {
			return nil, projectError(err)
		}
		page, err := api.NewPage(in.PageParams, list, projectKey)
		if err != nil {
			return nil, err
		}
		return &trashOutput{Body: page}, nil
	}, api.SessionOnly())

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getProject",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}",
		Summary:     "Get a project",
		Description: "Returns a project, with the role that the signed-in user acts with on it." +
			projectNotFoundDoc + tokenNotFoundDoc + tokenAuthDoc(scope.SnapshotRead),
		Tags: []string{"Projects"},
	}, func(ctx context.Context, in *projectInput) (*projectOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		p, err := projects.Get(ctx, actor, id)
		if err != nil {
			return nil, projectError(err)
		}
		return &projectOutput{Body: newProjectBody(p)}, nil
	}, api.TokenScope(scope.SnapshotRead))

	api.Register(m, api.ChangeProjectSettings, huma.Operation{
		OperationID: "updateProject",
		Method:      http.MethodPatch,
		Path:        "/api/v1/projects/{project_id}",
		Summary:     "Change a project's name, description, or settings",
		Description: "Changes a project's name, description, default locale, publish_min_role, or entry, in one " +
			"batch of the project's commands; a field that the body leaves out, or sets to null, stays as it is. " +
			"Only an owner or an admin of the project may, and anyone else with a role on it is answered with 403 " +
			"insufficient_role." + projectNotFoundDoc + tokenNotFoundDoc + " A field whose value it does not " +
			"allow is answered with 422 validation_failed." + archivedDoc + tokenChangeDoc(scope.ProjectWrite),
		Tags: []string{"Projects"},
	}, func(ctx context.Context, in *updateProjectInput) (*projectOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		p, err := projects.Update(ctx, actor, id, project.Patch{Name: in.Body.Name, Description: in.Body.Description,
			DefaultLocale: in.Body.DefaultLocale, PublishMinRole: in.Body.PublishMinRole, Entry: in.Body.Entry,
			ExpectedRev: in.Body.ExpectedRev})
		if err != nil {
			return nil, projectError(err)
		}
		return &projectOutput{Body: newProjectBody(p)}, nil
	}, api.TokenScope(scope.ProjectWrite))

	api.Register(m, api.ChangeProjectSettings, huma.Operation{
		OperationID: "archiveProject",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects/{project_id}/archive",
		Summary:     "Archive a project",
		Description: "Archives a project, which makes it read-only: every change of it but unarchiving, deleting, " +
			"and restoring it is answered with 409 project_archived (PRD FR-PROJ-01 AC2). An archived project does " +
			"not count towards the workspace's projects. Only an owner or an admin of the project may, and anyone " +
			"else with a role on it is answered with 403 insufficient_role." + projectNotFoundDoc + tokenNotFoundDoc +
			" A project that is archived already is answered with 409 project_archived." +
			tokenChangeDoc(scope.ProjectWrite),
		Tags: []string{"Projects"},
	}, func(ctx context.Context, in *tokenChangeProjectInput) (*projectOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		p, err := projects.Archive(ctx, actor, id)
		if err != nil {
			return nil, projectError(err)
		}
		return &projectOutput{Body: newProjectBody(p)}, nil
	}, api.TokenScope(scope.ProjectWrite))

	api.Register(m, api.ChangeProjectSettings, huma.Operation{
		OperationID: "unarchiveProject",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects/{project_id}/unarchive",
		Summary:     "Unarchive a project",
		Description: "Unarchives a project, which can then be changed again, and counts towards the workspace's " +
			"projects again: a workspace that has as many projects as its plan allows is answered with 409 " +
			"limit_exceeded. A project that is not archived stays as it is. Only an owner or an admin of the " +
			"project may, and anyone else with a role on it is answered with 403 insufficient_role." +
			projectNotFoundDoc + tokenNotFoundDoc + tokenChangeDoc(scope.ProjectWrite),
		Tags: []string{"Projects"},
	}, func(ctx context.Context, in *tokenChangeProjectInput) (*projectOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		p, err := projects.Unarchive(ctx, actor, id)
		if err != nil {
			return nil, projectError(err)
		}
		return &projectOutput{Body: newProjectBody(p)}, nil
	}, api.TokenScope(scope.ProjectWrite))

	api.Register(m, api.ManageProjects, huma.Operation{
		OperationID: "deleteProject",
		Method:      http.MethodDelete,
		Path:        "/api/v1/projects/{project_id}",
		Summary:     "Delete a project to the trash",
		Description: "Deletes a project to its workspace's trash, archived or not, where it stays for 30 days, and " +
			"POST /api/v1/projects/{project_id}/restore restores it (PRD FR-PROJ-01 AC3). Then it is deleted for " +
			"good, with all of its content. Meanwhile, every route of the project but that one answers it with 404 " +
			"not_found, and it does not count towards the workspace's projects. Only an owner or an admin of the " +
			"workspace may, and anyone else with a role on the project is answered with 403 insufficient_role." +
			projectNotFoundDoc + csrfDoc,
		Tags:          []string{"Projects"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *changeProjectInput) (*struct{}, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		if err := projects.Delete(ctx, actor, id); err != nil {
			return nil, projectError(err)
		}
		return &struct{}{}, nil
	}, api.SessionOnly())

	api.Register(m, api.ManageProjects, huma.Operation{
		OperationID: "restoreProject",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects/{project_id}/restore",
		Summary:     "Restore a project from the trash",
		Description: "Restores a project from its workspace's trash, as it was when it was deleted, within 30 days " +
			"of its deletion; after that, it is answered with 404 not_found. A project that is not in the trash " +
			"stays as it is. A restored project that is not archived counts towards the workspace's projects " +
			"again: a workspace that has as many projects as its plan allows is answered with 409 limit_exceeded. " +
			"Only an owner or an admin of the workspace may, and anyone else with a role on the project is " +
			"answered with 403 insufficient_role. A project that does not exist, or on which the signed-in user's " +
			"role is none, is answered with 404 not_found." + csrfDoc,
		Tags: []string{"Projects"},
	}, func(ctx context.Context, in *changeProjectInput) (*projectOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		p, err := projects.Restore(ctx, actor, id)
		if err != nil {
			return nil, projectError(err)
		}
		return &projectOutput{Body: newProjectBody(p)}, nil
	}, api.SessionOnly())
}

// projectKey returns the key that the lists of projects are paged by: the
// project's UUID, in the form that cursorID reads, rather than its ID as the
// API writes it.
func projectKey(p project.Project) idKey {
	// The ID is the service's encoding of the project's UUID, which decodes.
	id, _ := ids.Decode(ids.Project, p.ID)
	return idKey{ID: id.String()}
}

// projectActor returns the project that the path names as projectID, and
// the user who makes the request, with the API token that they make it with,
// if any, as the actor of its commands and its reads.
func projectActor(ctx context.Context, projectID string) (ids.UUID, commands.Actor, error) {
	cur, err := currentFrom(ctx)
	if err != nil {
		return ids.UUID{}, commands.Actor{}, err
	}
	id, err := ids.Decode(ids.Project, projectID)
	if err != nil {
		return ids.UUID{}, commands.Actor{}, err
	}
	return id, cur.actor(ctx), nil
}

// projectError returns the error that the API answers err with, an error of
// package project or of the pipeline that applies its commands, which it
// answers as every route that applies commands does (pipelineError), or err
// itself if it is none of them.
func projectError(err error) error {
	var fields input.FieldErrors
	var invalid *commands.InvalidError
	switch {
	case errors.Is(err, project.ErrNotFound):
		return api.NewError(api.CodeNotFound)
	case errors.As(err, &fields):
		return fieldErrors(fields)
	case errors.As(err, &invalid):
		// The routes check what they send the pipeline as its commands do,
		// so a batch that it refuses as invalid is a request's that the
		// checks let past: its body is not valid all the same.
		problems := make([]api.Problem, len(invalid.Problems))
		for i, p := range invalid.Problems {
			problems[i] = api.Problem{Location: "body", Message: p.Message}
		}
		return &api.Error{Code: api.CodeValidationFailed, Details: map[string]any{"errors": problems}}
	}
	return pipelineError(err)
}
