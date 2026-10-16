package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

// WorkspaceFields are the fields of a workspace, as the API shows it. Its
// name is exported so that huma reads it where it is embedded.
type WorkspaceFields struct {
	ID            string    `json:"id" doc:"The workspace's ID"`
	Name          string    `json:"name" doc:"The workspace's name"`
	Slug          string    `json:"slug" doc:"The workspace's slug, which is unique on the instance"`
	DefaultLocale string    `json:"default_locale" doc:"The workspace's default locale, as a BCP 47 tag of a locale that the server has a catalog for"`
	CreatedAt     time.Time `json:"created_at" doc:"When the workspace was created"`
	UpdatedAt     time.Time `json:"updated_at" doc:"When its name, slug, or settings last changed"`
}

func newWorkspaceFields(w workspace.Workspace) WorkspaceFields {
	return WorkspaceFields{ID: ids.Encode(ids.Workspace, w.ID), Name: w.Name, Slug: w.Slug,
		DefaultLocale: w.DefaultLocale.String(), CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt}
}

// workspaceBody is a workspace, with the role that the signed-in user acts
// with in it.
type workspaceBody struct {
	WorkspaceFields
	Role string `json:"role" enum:"owner,admin,editor,commenter,viewer" doc:"The role that the signed-in user acts with in the workspace: their membership's, or owner for an instance admin when STORYCREEP_INSTANCE_ADMIN_ACCESS is on"`
}

func newWorkspaceBody(w workspace.Workspace, r role.Role) workspaceBody {
	return workspaceBody{WorkspaceFields: newWorkspaceFields(w), Role: string(r)}
}

// membershipBody is a workspace that the signed-in user is a member of.
type membershipBody struct {
	WorkspaceFields
	MembershipID string `json:"membership_id" doc:"The ID of the signed-in user's membership of the workspace"`
	Role         string `json:"role" enum:"owner,admin,editor,commenter,viewer,guest" doc:"The role of the signed-in user's membership. A guest acts only on the projects granted to them, and GET /api/v1/workspaces/{workspace_id} answers them with 404 not_found."`
}

type workspaceOutput struct {
	Body workspaceBody
}

type workspacesInput struct {
	api.PageParams
}

type workspacesOutput struct {
	Body api.Page[membershipBody]
}

// createWorkspaceBody is the body of POST /api/v1/workspaces.
type createWorkspaceBody struct {
	Name          string `json:"name" maxLength:"100" doc:"The workspace's name, of 1 to 100 characters, not all of them blank, and without control characters, bidirectional controls and line separators among them. White space around it is removed."`
	Slug          string `json:"slug,omitempty" doc:"The workspace's slug: groups of lowercase ASCII letters and digits joined by single hyphens, of at most 64 characters, and not a reserved word, or the answer is 422 slug_invalid; if another workspace has it, the answer is 409 slug_taken. Without it, the slug is derived from the name: its letters and digits in lowercase ASCII, without accents, each run of other characters a hyphen, cut to 64 characters, or workspace if the name has none. If another workspace has that slug, or it is a reserved word, -2, -3, and so on up to -10 is added, and after that a random suffix of 8 lowercase letters and digits."`
	DefaultLocale string `json:"default_locale,omitempty" maxLength:"64" doc:"The workspace's default locale, as a BCP 47 tag of a locale that the server has a catalog for. Without it, the locale of the request: the one that its Accept-Language prefers among those, or else STORYCREEP_DEFAULT_LOCALE."`
}

type createWorkspaceInput struct {
	CSRFInput
	Body createWorkspaceBody
}

type createdWorkspaceOutput struct {
	Location string `header:"Location" doc:"The workspace's URL"`
	Body     workspaceBody
}

type workspaceInput struct {
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
}

// updateWorkspaceBody is the body of PATCH /api/v1/workspaces/{workspace_id}.
// A field that it leaves out, or sets to null, which huma takes for a field
// left out, stays as it is.
type updateWorkspaceBody struct {
	Name          *string `json:"name,omitempty" maxLength:"100" doc:"The workspace's new name, as for POST /api/v1/workspaces"`
	Slug          *string `json:"slug,omitempty" doc:"The workspace's new slug, as for POST /api/v1/workspaces, with the same errors. A slug that the workspace has already is kept, as a reserved word that a later release added to the list is."`
	DefaultLocale *string `json:"default_locale,omitempty" maxLength:"64" doc:"The workspace's new default locale, a BCP 47 tag of a locale that the server has a catalog for"`
}

type updateWorkspaceInput struct {
	CSRFInput
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	Body        updateWorkspaceBody
}

// notFoundDoc says, in the description of an operation on a workspace, when
// the operation answers that the workspace does not exist.
const notFoundDoc = " A workspace that does not exist, or in which the signed-in user's role is none, as it is for " +
	"a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell " +
	"whether it exists."

// registerWorkspaces registers the routes of workspaces (PRD FR-TEN-01,
// §5.5).
func registerWorkspaces(m *api.Mux, workspaces *workspace.Service) {
	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "listWorkspaces",
		Method:      http.MethodGet,
		Path:        "/api/v1/workspaces",
		Summary:     "List the account's workspaces",
		Description: "Lists the workspaces that the signed-in user is a member of, in the order of their IDs, " +
			"oldest first, each with the role of the user's membership, guest included. A client switches " +
			"between them by naming one by its ID in the routes that it calls: the server keeps no current " +
			"workspace. Every account has one at least from its first sign-in, which gives it its personal " +
			"workspace." + sessionAuthDoc,
		Tags: []string{"Workspaces"},
	}, func(ctx context.Context, in *workspacesInput) (*workspacesOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		memberships, err := workspaces.List(ctx, cur.session.UserID, after, in.Fetch())
		if err != nil {
			return nil, err
		}
		page, err := api.NewPage(in.PageParams, memberships, func(m workspace.Membership) idKey {
			return idKey{ID: m.Workspace.ID.String()}
		})
		if err != nil {
			return nil, err
		}
		out := &workspacesOutput{Body: api.Page[membershipBody]{Items: make([]membershipBody, len(page.Items)),
			NextCursor: page.NextCursor}}
		for i, m := range page.Items {
			out.Body.Items[i] = membershipBody{WorkspaceFields: newWorkspaceFields(m.Workspace),
				MembershipID: ids.Encode(ids.Membership, m.ID), Role: string(m.Role)}
		}
		return out, nil
	})

	api.Register(m, api.CreateWorkspace, huma.Operation{
		OperationID: "createWorkspace",
		Method:      http.MethodPost,
		Path:        "/api/v1/workspaces",
		Summary:     "Create a workspace",
		Description: "Creates a workspace, whose owner the signed-in user becomes. Where the instance always sends " +
			"email, as hosted mode does, a user who has not verified their email address is answered with 403 " +
			"email_unverified, whatever the request holds. A name or a default locale that its field does not " +
			"allow is answered with 422 validation_failed." + csrfDoc,
		Tags:          []string{"Workspaces"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createWorkspaceInput) (*createdWorkspaceOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		w, err := workspaces.Create(ctx, cur.session.UserID, workspace.Settings{Name: in.Body.Name,
			Slug: in.Body.Slug, DefaultLocale: in.Body.DefaultLocale}, clientFrom(ctx))
		if err != nil {
			return nil, workspaceError(err)
		}
		body := newWorkspaceBody(w, role.Owner)
		return &createdWorkspaceOutput{Location: "/api/v1/workspaces/" + body.ID, Body: body}, nil
	})

	api.Register(m, api.ReadWorkspace, huma.Operation{
		OperationID: "getWorkspace",
		Method:      http.MethodGet,
		Path:        "/api/v1/workspaces/{workspace_id}",
		Summary:     "Get a workspace",
		Description: "Returns a workspace, with the role that the signed-in user acts with in it." + notFoundDoc +
			sessionAuthDoc,
		Tags: []string{"Workspaces"},
	}, func(ctx context.Context, in *workspaceInput) (*workspaceOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		w, r, err := workspaces.Get(ctx, cur.session.UserID, id)
		if err != nil {
			return nil, workspaceError(err)
		}
		return &workspaceOutput{Body: newWorkspaceBody(w, r)}, nil
	})

	api.Register(m, api.ManageWorkspaceSettings, huma.Operation{
		OperationID: "updateWorkspace",
		Method:      http.MethodPatch,
		Path:        "/api/v1/workspaces/{workspace_id}",
		Summary:     "Change a workspace's settings",
		Description: "Changes a workspace's name, slug, or default locale; a field that the body leaves out, or " +
			"sets to null, stays as it is. Only an owner or an admin of the workspace may, and anyone else with a " +
			"role in it is answered with 403 insufficient_role." + notFoundDoc + " A name or a default locale that " +
			"its field does not allow is answered with 422 validation_failed." + csrfDoc,
		Tags: []string{"Workspaces"},
	}, func(ctx context.Context, in *updateWorkspaceInput) (*workspaceOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		w, r, err := workspaces.Update(ctx, cur.session.UserID, id, workspace.Patch{Name: in.Body.Name,
			Slug: in.Body.Slug, DefaultLocale: in.Body.DefaultLocale})
		if err != nil {
			return nil, workspaceError(err)
		}
		return &workspaceOutput{Body: newWorkspaceBody(w, r)}, nil
	})
}

// workspaceError returns the error that the API answers err with, an error
// of package workspace, or err itself if it is none of them.
func workspaceError(err error) error {
	var fields input.FieldErrors
	var slug *workspace.SlugError
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		return api.NewError(api.CodeNotFound)
	case errors.Is(err, workspace.ErrInsufficientRole):
		return api.NewError(api.CodeInsufficientRole)
	case errors.Is(err, workspace.ErrEmailUnverified):
		return api.NewError(api.CodeEmailUnverified)
	case errors.Is(err, workspace.ErrSlugTaken):
		return slugTakenError("body.slug")
	case errors.As(err, &slug):
		return slugInvalidError(slug, "body.slug")
	case errors.As(err, &fields):
		return fieldErrors(fields)
	}
	return err
}

// slugTakenError returns the error that the API answers a slug that another
// workspace has with, for the slug at location, such as body.slug.
func slugTakenError(location string) *api.Error {
	return &api.Error{Code: api.CodeSlugTaken, Details: map[string]any{
		"errors": []api.Problem{{Location: location, Message: "is another workspace's slug"}},
	}}
}

// slugInvalidError returns the error that the API answers slug with, for the
// slug at location, such as body.slug.
func slugInvalidError(slug *workspace.SlugError, location string) *api.Error {
	message := "is not groups of lowercase letters and digits joined by single hyphens, of at most 64 characters"
	if slug.Reason == workspace.SlugReserved {
		message = "is a reserved word"
	}
	return &api.Error{Code: api.CodeSlugInvalid, Details: map[string]any{
		"reason": slug.Reason,
		"errors": []api.Problem{{Location: location, Message: message}},
	}}
}

// fieldErrors returns the error that the API answers fields of a request's
// body with.
func fieldErrors(fields input.FieldErrors) *api.Error {
	problems := make([]api.Problem, len(fields))
	for i, f := range fields {
		problems[i] = api.Problem{Location: "body." + f.Field, Message: f.Message}
	}
	return &api.Error{Code: api.CodeValidationFailed, Details: map[string]any{"errors": problems}}
}
