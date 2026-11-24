package httpserver

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/asset"
	"github.com/CyberCute-Studio/storycreep/server/internal/authoring"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// workspaceExportDoc says who exports a workspace and reads an export of it,
// and what the archive holds of people, in each route's description.
const workspaceExportDoc = " Only an owner of the workspace may export it: an admin is answered with 403 " +
	"owner_required, and anyone else with a role in it with 403 insufficient_role. Only the owner who asked for an " +
	"export reads it, while they are an owner, and an export of anyone else, or one that has expired, is answered " +
	"with 404 not_found. **The archive contains personal data**: the members' display names and email addresses, " +
	"the audit log's IP addresses and user agents, and each project's comments and the names of the people " +
	"involved." + notFoundDoc

// WorkspaceExportBody is an export of a workspace, as the API shows it.
type WorkspaceExportBody struct {
	ID               string     `json:"id" doc:"The export's ID, a job's."`
	WorkspaceID      string     `json:"workspace_id" doc:"The workspace's ID."`
	Status           string     `json:"status" enum:"queued,building,ready,failed" doc:"Where the export is: queued, building, ready, with a file to download, or failed."`
	ContainsPersonal bool       `json:"contains_personal_data" doc:"Whether the archive holds personal data, which a workspace's always does."`
	SizeBytes        *int64     `json:"size_bytes,omitempty" doc:"The archive's size, once it is ready."`
	ProjectCount     *int32     `json:"project_count,omitempty" doc:"How many projects a ready archive holds: those of the workspace outside its trash."`
	FileName         string     `json:"file_name,omitempty" doc:"The name that a ready archive is downloaded under."`
	CreatedAt        time.Time  `json:"created_at" doc:"When the export was asked for."`
	FinishedAt       *time.Time `json:"finished_at,omitempty" doc:"When its build ended."`
	ExpiresAt        time.Time  `json:"expires_at" doc:"When the export and its file are deleted, seven days after it was asked for."`
}

func workspaceExportBody(e authoring.WorkspaceExport) WorkspaceExportBody {
	return WorkspaceExportBody{ID: e.ID, WorkspaceID: e.WorkspaceID, Status: e.Status, ContainsPersonal: true,
		SizeBytes: e.SizeBytes, ProjectCount: e.ProjectCount, FileName: e.FileName, CreatedAt: e.CreatedAt,
		FinishedAt: e.FinishedAt, ExpiresAt: e.ExpiresAt}
}

type createWorkspaceExportInput struct {
	CSRFInput
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
}

type workspaceExportOutput struct {
	Location string `header:"Location" doc:"The export's URL, which a client reads to learn when its build ends"`
	Body     WorkspaceExportBody
}

type workspaceExportInput struct {
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	ExportID    string `path:"export_id" doc:"The export's ID"`
}

type workspaceExportsInput struct {
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	api.PageParams
}

type workspaceExportsOutput struct {
	Body api.Page[WorkspaceExportBody]
}

type workspaceExportGetOutput struct {
	Body WorkspaceExportBody
}

// workspaceExportPath returns the URL of the export e.
func workspaceExportPath(e authoring.WorkspaceExport) string {
	return "/api/v1/workspaces/" + e.WorkspaceID + "/exports/" + e.ID
}

// registerWorkspaceExports registers the routes of the workspace export (PRD
// FR-DATA-02, plan note N93), each an owner's of the workspace and of a
// session alone, as no scope of an API token grants it. The two that read the
// workspace whole, asking for an export and downloading it, count in the
// export bucket.
func registerWorkspaceExports(m *api.Mux, exports *authoring.Service) {
	api.Register(m, api.ExportWorkspace, huma.Operation{
		OperationID: "createWorkspaceExport",
		Method:      http.MethodPost,
		Path:        "/api/v1/workspaces/{workspace_id}/exports",
		Summary:     "Export a workspace",
		Description: "Asks for a zip of the whole workspace, in the format of schema/workspace/v1, and answers 202 with " +
			"the export, queued, whose URL Location names. A job builds it. The archive holds each project of the " +
			"workspace outside its trash, archived ones too, as a full authoring export of its draft (schema/" +
			"authoring/v1), with its comments, play states, release history, and assets, which the project import " +
			"takes as it is; the members, with their roles, email addresses, and grants; the audit log; and the " +
			"workspace's own assets. Its manifest names the projects in the trash, which it does not hold (PRD " +
			"FR-DATA-02). The archive is kept for seven days. A user has one export of a workspace on its way at a " +
			"time, and another request is answered with 409 export_in_progress, whose details.export_id names it. " +
			"Until the instance's first-run setup has completed, which may yet choose where files are stored, the " +
			"answer is 403 setup_required, unless STORYCREEP_ASSET_STORAGE names where they are." +
			workspaceExportDoc + csrfDoc,
		Tags:          []string{"Workspace export"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *createWorkspaceExportInput) (*workspaceExportOutput, error) {
		ws, _, actor, err := importActor(ctx, in.WorkspaceID, "")
		if err != nil {
			return nil, err
		}
		e, err := exports.CreateWorkspaceExport(ctx, actor, ws)
		if err != nil {
			return nil, workspaceExportError(err)
		}
		return &workspaceExportOutput{Location: workspaceExportPath(e), Body: workspaceExportBody(e)}, nil
	}, api.SessionOnly(), api.RateLimit(api.BucketExport))

	api.Register(m, api.ExportWorkspace, huma.Operation{
		OperationID: "listWorkspaceExports",
		Method:      http.MethodGet,
		Path:        "/api/v1/workspaces/{workspace_id}/exports",
		Summary:     "List your exports of a workspace",
		Description: "Lists the exports of the workspace that the user asked for and that have not expired, newest " +
			"first." + workspaceExportDoc + sessionAuthDoc,
		Tags: []string{"Workspace export"},
	}, func(ctx context.Context, in *workspaceExportsInput) (*workspaceExportsOutput, error) {
		ws, _, actor, err := importActor(ctx, in.WorkspaceID, "")
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := exports.WorkspaceExports(ctx, actor, ws, after, in.Fetch())
		if err != nil {
			return nil, workspaceExportError(err)
		}
		bodies := make([]WorkspaceExportBody, len(list))
		for i, e := range list {
			bodies[i] = workspaceExportBody(e)
		}
		page, err := api.NewPage(in.PageParams, bodies, func(e WorkspaceExportBody) idKey {
			return uuidKey(ids.Job, e.ID)
		})
		if err != nil {
			return nil, err
		}
		return &workspaceExportsOutput{Body: page}, nil
	}, api.SessionOnly())

	api.Register(m, api.ExportWorkspace, huma.Operation{
		OperationID: "getWorkspaceExport",
		Method:      http.MethodGet,
		Path:        "/api/v1/workspaces/{workspace_id}/exports/{export_id}",
		Summary:     "Read an export of a workspace",
		Description: "Returns the export: where its build is, and, once it is ready, the archive's size, how many " +
			"projects it holds, and its name." + workspaceExportDoc + sessionAuthDoc,
		Tags: []string{"Workspace export"},
	}, func(ctx context.Context, in *workspaceExportInput) (*workspaceExportGetOutput, error) {
		ws, id, actor, err := importActor(ctx, in.WorkspaceID, in.ExportID)
		if err != nil {
			return nil, err
		}
		e, err := exports.GetWorkspaceExport(ctx, actor, ws, id)
		if err != nil {
			return nil, workspaceExportError(err)
		}
		return &workspaceExportGetOutput{Body: workspaceExportBody(e)}, nil
	}, api.SessionOnly())

	api.Register(m, api.ExportWorkspace, huma.Operation{
		OperationID: "downloadWorkspaceExport",
		Method:      http.MethodGet,
		Path:        "/api/v1/workspaces/{workspace_id}/exports/{export_id}/download",
		Summary:     "Download an export of a workspace",
		Description: "Returns the archive of a ready export, as a zip, whose README and workspace.json say what it " +
			"holds, and Content-Disposition names it, as an attachment. An export that is not ready is answered with " +
			"409 export_not_ready, whose details.status says where it is." + workspaceExportDoc + sessionAuthDoc,
		Tags: []string{"Workspace export"},
		Responses: map[string]*huma.Response{"200": {
			Description: "The archive",
			Headers: map[string]*huma.Header{
				"Content-Disposition": {Schema: &huma.Schema{Type: huma.TypeString,
					Description: "attachment, with the archive's name: the workspace's slug and workspace, as harbour-workspace.zip"}},
				"X-Content-Type-Options": {Schema: &huma.Schema{Type: huma.TypeString, Description: "nosniff"}},
			},
			Content: map[string]*huma.MediaType{zipType: {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}},
		}},
	}, func(ctx context.Context, in *workspaceExportInput) (*huma.StreamResponse, error) {
		ws, id, actor, err := importActor(ctx, in.WorkspaceID, in.ExportID)
		if err != nil {
			return nil, err
		}
		d, err := exports.OpenWorkspaceExport(ctx, actor, ws, id)
		if err != nil {
			return nil, workspaceExportError(err)
		}
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": d.Name})
		return &huma.StreamResponse{Body: func(hc huma.Context) {
			defer d.Body.Close()
			hc.SetHeader("Content-Type", zipType)
			hc.SetHeader("Content-Length", strconv.FormatInt(d.Size, 10))
			hc.SetHeader("Content-Disposition", disposition)
			hc.SetHeader("X-Content-Type-Options", "nosniff")
			hc.SetStatus(http.StatusOK)
			if hc.Method() == http.MethodHead {
				return
			}
			// The status and the headers are sent, so a failure from here on
			// can only end the answer early, which the client sees as a body
			// shorter than its Content-Length.
			_, _ = io.Copy(hc.BodyWriter(), d.Body)
		}}, nil
	}, api.SessionOnly(), api.RateLimit(api.BucketExport))
}

// workspaceExportError returns the error that the API answers err with.
func workspaceExportError(err error) error {
	var inProgress *authoring.InProgressError
	var notReady *authoring.NotReadyError
	switch {
	case errors.Is(err, authoring.ErrNotFound):
		return api.NewError(api.CodeNotFound)
	case errors.Is(err, asset.ErrSetupRequired):
		return api.NewError(api.CodeSetupRequired)
	case errors.As(err, &inProgress):
		return &api.Error{Code: api.CodeExportInProgress, Details: map[string]any{"export_id": inProgress.ID}}
	case errors.As(err, &notReady):
		return &api.Error{Code: api.CodeExportNotReady, Details: map[string]any{"status": notReady.Status}}
	}
	return projectError(err)
}
