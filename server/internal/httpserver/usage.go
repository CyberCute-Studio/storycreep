package httpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

// limitUsageBody is a workspace's usage of a limit of its plan.
type limitUsageBody struct {
	Used  int64  `json:"used" doc:"How much the workspace uses"`
	Limit *int64 `json:"limit,omitempty" doc:"The most that the workspace may use: its plan's limit, or the instance's cap if that is smaller. Without it, the workspace may use any amount."`
	// Warning is the soft warning of FR-ENT-03.
	Warning bool `json:"warning" doc:"Set when the workspace uses at least 80% of its limit, so that a client can warn before a change is refused with 409 limit_exceeded"`
}

// meteredUsageBody is a workspace's usage of a thing that the plans meter but
// do not limit.
type meteredUsageBody struct {
	Used int64 `json:"used" doc:"How much the workspace uses"`
}

// usageBody is a workspace's usage (PRD FR-ENT-02): a property for each key
// of usage, so that a key that a later release meters is a property that a
// client of this one ignores, as an enum's new value would not be.
type usageBody struct {
	Projects     limitUsageBody   `json:"projects" doc:"The workspace's projects that count towards its limit: those that are neither archived nor in its trash"`
	Boards       meteredUsageBody `json:"boards" doc:"The boards of the workspace's projects that are in no trash: neither in their project's nor, with their project, in the workspace's"`
	Nodes        meteredUsageBody `json:"nodes" doc:"The nodes of the workspace's projects"`
	Members      limitUsageBody   `json:"members" doc:"The workspace's members, guests included"`
	StorageBytes limitUsageBody   `json:"storage_bytes" doc:"The bytes that the workspace's stored files take up. Uploads that run at once may take it past its limit by what they add together."`
	Snapshots    meteredUsageBody `json:"snapshots" doc:"The snapshots of the workspace's projects"`
	APIRequests  meteredUsageBody `json:"api_requests" doc:"The requests that the workspace's API tokens have made: each that a token was accepted for, whatever its answer then; a request that a token is refused for, as one without the route's scope, is not counted"`
}

// newUsageBody returns the body of the workspace's usage us, which holds
// every key of usage, each in the field of its key.
func newUsageBody(us []workspace.Usage) (usageBody, error) {
	var b usageBody
	limits := map[usage.Key]*limitUsageBody{usage.Projects: &b.Projects, usage.Members: &b.Members,
		usage.StorageBytes: &b.StorageBytes}
	metered := map[usage.Key]*meteredUsageBody{usage.Boards: &b.Boards, usage.Nodes: &b.Nodes,
		usage.Snapshots: &b.Snapshots, usage.APIRequests: &b.APIRequests}
	for _, u := range us {
		switch l, m := limits[u.Key], metered[u.Key]; {
		case l != nil && u.Limited:
			*l = limitUsageBody{Used: u.Used, Warning: u.Warning}
			if limit, ok := u.Quota.Max(); ok {
				l.Limit = &limit
			}
		case m != nil && !u.Limited:
			*m = meteredUsageBody{Used: u.Used}
		default:
			// A key that the body has no field of, as one that a release adds
			// to package usage without adding it here, would be left out.
			return usageBody{}, fmt.Errorf("httpserver: the usage of %s has no field of its kind", u.Key)
		}
	}
	return b, nil
}

type usageInput struct {
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
}

type usageOutput struct {
	Body usageBody
}

// registerUsage registers the route of a workspace's usage (PRD FR-ENT-02).
func registerUsage(m *api.Mux, workspaces *workspace.Service) {
	api.Register(m, api.ManageWorkspaceSettings, huma.Operation{
		OperationID: "getWorkspaceUsage",
		Method:      http.MethodGet,
		Path:        "/api/v1/workspaces/{workspace_id}/usage",
		Summary:     "Get a workspace's usage",
		Description: "Returns how much the workspace uses of each thing that the plans meter, and, for those that " +
			"its plan limits, the limit and whether it uses 80% of it or more, which the workspace's settings and " +
			"the invitation dialog show. A change that would take the workspace past a limit is refused with 409 " +
			"limit_exceeded. A self-hosted instance limits nothing, unless STORYCREEP_WORKSPACE_LIMITS caps a " +
			"limit. Only an owner or an admin of the workspace may read it, as its settings, and anyone else with a " +
			"role in it is answered with 403 insufficient_role." + notFoundDoc + sessionAuthDoc,
		Tags: []string{"Workspaces"},
	}, func(ctx context.Context, in *usageInput) (*usageOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		us, err := workspaces.Usage(ctx, cur.session.UserID, id)
		if err != nil {
			return nil, workspaceError(err)
		}
		body, err := newUsageBody(us)
		if err != nil {
			return nil, err
		}
		return &usageOutput{Body: body}, nil
	}, api.SessionOnly())
}
