package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
)

// Project grants on the wire (PRD FR-TEN-04): the members' grants on a
// project, which its workspace's owners and admins list, set, and remove,
// and a member's grants on the workspace's projects. A grant has no ID of
// its own: a membership has one grant on a project at most, which the
// project's ID and the membership's name.

// grantBody is a membership's grant on a project.
type grantBody struct {
	ProjectID    string    `json:"project_id" doc:"The ID of the project"`
	MembershipID string    `json:"membership_id" doc:"The ID of the membership that has the grant"`
	UserID       string    `json:"user_id" doc:"The ID of the member's account"`
	Role         string    `json:"role" enum:"admin,editor,commenter,viewer" doc:"The member's role on the project, which replaces their role in the workspace there, raising or lowering it. A guest acts only on the projects that they have a grant on."`
	CreatedAt    time.Time `json:"created_at" doc:"When the grant was given"`
	UpdatedAt    time.Time `json:"updated_at" doc:"When the grant was given, or its role last changed"`
}

func newGrantBody(g project.Grant) grantBody {
	return grantBody{ProjectID: ids.Encode(ids.Project, g.ProjectID),
		MembershipID: ids.Encode(ids.Membership, g.MembershipID), UserID: ids.Encode(ids.User, g.UserID),
		Role: string(g.Role), CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
}

type grantsOutput struct {
	Body api.Page[grantBody]
}

type grantOutput struct {
	Body grantBody
}

type projectGrantsInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	api.PageParams
}

type memberGrantsInput struct {
	WorkspaceID  string `path:"workspace_id" doc:"The workspace's ID"`
	MembershipID string `path:"membership_id" doc:"The ID of the membership whose grants to list"`
	api.PageParams
}

// setGrantBody is the body of PUT
// /api/v1/projects/{project_id}/grants/{membership_id}.
type setGrantBody struct {
	// owner is in the enum so that it reaches the service, which answers it
	// with 400 owner_not_grantable (FR-TEN-04 AC5), rather than huma, which
	// would answer it with 422 validation_failed.
	Role string `json:"role" enum:"owner,admin,editor,commenter,viewer" doc:"The member's role on the project: admin, editor, commenter, or viewer. owner is a role of the workspace alone, and is answered with 400 owner_not_grantable."`
}

type setGrantInput struct {
	CSRFInput
	ProjectID    string `path:"project_id" doc:"The project's ID"`
	MembershipID string `path:"membership_id" doc:"The ID of the membership of the project's workspace whose grant to set"`
	Body         setGrantBody
}

type removeGrantInput struct {
	CSRFInput
	ProjectID    string `path:"project_id" doc:"The project's ID"`
	MembershipID string `path:"membership_id" doc:"The ID of the membership whose grant to remove"`
}

// manageGrantsDoc says, in the description of an operation on a project's
// grants, who may: the workspace's owners and admins, whatever their grants,
// and nobody whom a grant alone makes an admin of the project.
const manageGrantsDoc = " Only an owner or an admin of the project's workspace may, and anyone else with a role on " +
	"the project, as a viewer or a guest with an admin grant on it, is answered with 403 insufficient_role: a " +
	"grant never authorises an operation of the workspace (PRD FR-TEN-04 AC4)." + projectNotFoundDoc

// ownerGrantDoc says, in the description of an operation that changes a
// grant, that only an owner changes an owner's.
const ownerGrantDoc = " Only an owner sets or removes an owner's grant, which replaces their role on the project, and " +
	"an admin is answered with 403 owner_required."

// registerGrants registers the routes of project grants (PRD FR-TEN-04).
func registerGrants(m *api.Mux, projects *project.Service) {
	api.Register(m, api.ManageProjectGrants, huma.Operation{
		OperationID: "listProjectGrants",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/grants",
		Summary:     "List a project's grants",
		Description: "Lists the members' grants on a project, in the order of their memberships' IDs, oldest first. " +
			"A member with a grant acts on the project with its role instead of their role in the workspace, and a " +
			"guest acts only on the projects that they have one on." + manageGrantsDoc + sessionAuthDoc,
		Tags: []string{"Grants"},
	}, func(ctx context.Context, in *projectGrantsInput) (*grantsOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Project, in.ProjectID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := projects.Grants(ctx, cur.session.UserID, id, after, in.Fetch())
		if err != nil {
			return nil, grantError(err)
		}
		return grantPage(in.PageParams, list, func(g project.Grant) ids.UUID { return g.MembershipID })
	})

	api.Register(m, api.ManageProjectGrants, huma.Operation{
		OperationID: "setProjectGrant",
		Method:      http.MethodPut,
		Path:        "/api/v1/projects/{project_id}/grants/{membership_id}",
		Summary:     "Set a member's grant on a project",
		Description: "Gives a member of the project's workspace a role on the project, which replaces their role in " +
			"the workspace there, raising or lowering it, and returns the grant: it adds the member's grant on the " +
			"project, or changes its role. A grant that has the role already stays as it is. A grant is the only " +
			"way that a guest acts on a project, and it applies only to the project's own operations: every " +
			"operation of the workspace, as deleting the project or managing its grants, is still authorised by " +
			"the member's role in the workspace. The role owner is answered with 400 owner_not_grantable (PRD " +
			"FR-TEN-04 AC5)." + ownerGrantDoc + manageGrantsDoc + " So is a membership that the workspace does " +
			"not have." + csrfDoc,
		Tags: []string{"Grants"},
	}, func(ctx context.Context, in *setGrantInput) (*grantOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Project, in.ProjectID)
		if err != nil {
			return nil, err
		}
		membershipID, err := ids.Decode(ids.Membership, in.MembershipID)
		if err != nil {
			return nil, err
		}
		g, err := projects.SetGrant(ctx, cur.session.UserID, id, membershipID, role.Role(in.Body.Role),
			clientFrom(ctx))
		if err != nil {
			return nil, grantError(err)
		}
		return &grantOutput{Body: newGrantBody(g)}, nil
	})

	api.Register(m, api.ManageProjectGrants, huma.Operation{
		OperationID: "removeProjectGrant",
		Method:      http.MethodDelete,
		Path:        "/api/v1/projects/{project_id}/grants/{membership_id}",
		Summary:     "Remove a member's grant on a project",
		Description: "Removes a member's grant on a project, after which they act on it with their role in the " +
			"workspace, and a guest no longer acts on it at all." + ownerGrantDoc + manageGrantsDoc + " So is a " +
			"membership that the workspace does not have, or that has no grant on the project." + csrfDoc,
		Tags:          []string{"Grants"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *removeGrantInput) (*struct{}, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Project, in.ProjectID)
		if err != nil {
			return nil, err
		}
		membershipID, err := ids.Decode(ids.Membership, in.MembershipID)
		if err != nil {
			return nil, err
		}
		if err := projects.RemoveGrant(ctx, cur.session.UserID, id, membershipID, clientFrom(ctx)); err != nil {
			return nil, grantError(err)
		}
		return &struct{}{}, nil
	})

	api.Register(m, api.ManageProjectGrants, huma.Operation{
		OperationID: "listMemberGrants",
		Method:      http.MethodGet,
		Path:        "/api/v1/workspaces/{workspace_id}/members/{membership_id}/grants",
		Summary:     "List a member's grants",
		Description: "Lists a member's grants on the workspace's projects that are not in its trash, in the order " +
			"of the projects' IDs, oldest first." + manageMembersDoc + " So is a membership that the workspace " +
			"does not have." + sessionAuthDoc,
		Tags: []string{"Grants"},
	}, func(ctx context.Context, in *memberGrantsInput) (*grantsOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		ws, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		membershipID, err := ids.Decode(ids.Membership, in.MembershipID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := projects.MemberGrants(ctx, cur.session.UserID, ws, membershipID, after, in.Fetch())
		if err != nil {
			return nil, grantError(err)
		}
		return grantPage(in.PageParams, list, func(g project.Grant) ids.UUID { return g.ProjectID })
	})
}

// grantPage returns the page of the grants list that in asks for, which a
// list is paged by the UUID that key returns of each grant.
func grantPage(in api.PageParams, list []project.Grant, key func(project.Grant) ids.UUID) (*grantsOutput, error) {
	page, err := api.NewPage(in, list, func(g project.Grant) idKey { return idKey{ID: key(g).String()} })
	if err != nil {
		return nil, err
	}
	out := &grantsOutput{Body: api.Page[grantBody]{Items: make([]grantBody, len(page.Items)),
		NextCursor: page.NextCursor}}
	for i, g := range page.Items {
		out.Body.Items[i] = newGrantBody(g)
	}
	return out, nil
}

// grantError returns the error that the API answers err with, an error of
// package project's grants, or of its other operations (projectError).
func grantError(err error) error {
	switch {
	case errors.Is(err, project.ErrOwnerNotGrantable):
		return &api.Error{Code: api.CodeOwnerNotGrantable, Details: map[string]any{
			"errors": []api.Problem{{Location: "body.role", Message: "is owner, which a project grant cannot be"}},
		}}
	case errors.Is(err, project.ErrOwnerRequired):
		return api.NewError(api.CodeOwnerRequired)
	}
	return projectError(err)
}
