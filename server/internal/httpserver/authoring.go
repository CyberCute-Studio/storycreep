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
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// authoringPersonalDataDoc is the notice of FR-DATA-01 AC6 in the API's
// documentation.
const authoringPersonalDataDoc = " **A full export contains personal data**: the comments, and the display names of " +
	"the people who wrote, resolved, or are mentioned in them, saved play states, muted findings, or published " +
	"releases. The profile content " +
	"leaves all of it out."

// authoringExportDoc says who reads an export, in each route's description.
const authoringExportDoc = " Only the user who asked for an export reads it, and an export of anyone else, or one " +
	"that has expired, is answered with 404 not_found." + authoringPersonalDataDoc

// AuthoringExportBody is an authoring export, as the API shows it.
type AuthoringExportBody struct {
	ID                 string     `json:"id" doc:"The export's ID, a job's."`
	ProjectID          string     `json:"project_id" doc:"The project's ID."`
	SnapshotID         string     `json:"snapshot_id,omitempty" doc:"The release that the archive holds as its content. Without it, the draft."`
	Status             string     `json:"status" enum:"queued,building,ready,failed" doc:"Where the export is: queued, building, ready, with a file to download, or failed."`
	Profile            string     `json:"profile" enum:"full,content" doc:"full holds comments, play states, and annotations, and names the people involved; content holds none of them."`
	IncludeAssets      bool       `json:"include_assets" doc:"Whether the archive holds the project's assets, node cover images among them."`
	IncludeReleases    bool       `json:"include_releases" doc:"Whether the archive holds the release history, with each release's files."`
	ContainsPersonal   bool       `json:"contains_personal_data" doc:"Whether the archive holds personal data, as a full export does."`
	SizeBytes          *int64     `json:"size_bytes,omitempty" doc:"The archive's size, once it is ready."`
	ImportMaxBytes     int64      `json:"import_max_bytes" doc:"The largest archive that this instance imports (STORYCREEP_IMPORT_MAX_BYTES)."`
	ExceedsImportLimit bool       `json:"exceeds_import_limit" doc:"Whether the archive is larger than import_max_bytes, so that this instance could not import it."`
	FileName           string     `json:"file_name,omitempty" doc:"The name that a ready archive is downloaded under."`
	CreatedAt          time.Time  `json:"created_at" doc:"When the export was asked for."`
	FinishedAt         *time.Time `json:"finished_at,omitempty" doc:"When its build ended."`
	ExpiresAt          time.Time  `json:"expires_at" doc:"When the export and its file are deleted."`
}

func authoringExportBody(e authoring.Export) AuthoringExportBody {
	return AuthoringExportBody{ID: e.ID, ProjectID: e.ProjectID, SnapshotID: e.SnapshotID, Status: e.Status,
		Profile: string(e.Profile), IncludeAssets: e.IncludeAssets, IncludeReleases: e.IncludeReleases,
		ContainsPersonal: e.PersonalData, SizeBytes: e.SizeBytes, ImportMaxBytes: e.ImportMaxBytes,
		ExceedsImportLimit: e.ExceedsImportLimit(), FileName: e.FileName, CreatedAt: e.CreatedAt,
		FinishedAt: e.FinishedAt, ExpiresAt: e.ExpiresAt}
}

type createAuthoringExportInput struct {
	SessionCSRFInput
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Body      struct {
		Source          string `json:"source,omitempty" doc:"What the archive's content is of: draft, or the ID of a ready snapshot. Without it, the draft."`
		Profile         string `json:"profile,omitempty" enum:"full,content" doc:"full, the default, includes comments, play states, and annotations, and names the people involved, so it contains personal data; content leaves them out."`
		IncludeAssets   *bool  `json:"include_assets,omitempty" doc:"Whether to include the project's assets, node cover images among them. Without it, true."`
		IncludeReleases *bool  `json:"include_releases,omitempty" doc:"Whether to include the release history, every ready snapshot with its files. Without it, true."`
	}
}

type authoringExportOutput struct {
	Location string `header:"Location" doc:"The export's URL, which a client reads to learn when its build ends"`
	Body     AuthoringExportBody
}

type authoringExportInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	ExportID  string `path:"export_id" doc:"The export's ID"`
}

type authoringExportsInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	api.PageParams
}

type authoringExportsOutput struct {
	Body api.Page[AuthoringExportBody]
}

type authoringExportGetOutput struct {
	Body AuthoringExportBody
}

// authoringExportPath returns the URL of the export e.
func authoringExportPath(e authoring.Export) string {
	return "/api/v1/projects/" + e.ProjectID + "/authoring-exports/" + e.ID
}

// registerAuthoringExports registers the routes of the authoring export (PRD
// FR-DATA-01, plan note N90), each of which takes the scope export:read,
// which no other scope implies (FR-AUTH-04). The two that read the project
// whole, asking for an export and downloading it, count in the export bucket.
func registerAuthoringExports(m *api.Mux, exports *authoring.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "createAuthoringExport",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects/{project_id}/authoring-exports",
		Summary:     "Export a project's authoring data",
		Description: "Asks for a zip of the project's authoring data, of its draft or of one of its ready snapshots, in " +
			"the format of schema/authoring/v1, and answers 202 with the export, queued, whose URL Location names. A " +
			"job builds it. The archive holds the draft's entities without its trash, or a release's files, whose " +
			"folders and other draft-only data no release keeps; with the full profile, the comments, play states, " +
			"and an annotations section, of a release those about what it still has, the others outside the trash " +
			"being counted in the archive's manifest and README; the release history with its files; and the " +
			"assets, node cover images among them (PRD FR-DATA-01, FR-EDIT-06 AC3). The archive is kept for seven " +
			"days. A user has one export of a project on its way at a time, and another request is answered with " +
			"409 export_in_progress, whose details.export_id names it. A snapshot that is building or failed is " +
			"answered with 409 snapshot_not_ready, and a source that is neither draft nor a snapshot's ID with 400 " +
			"invalid_id. Until the instance's first-run setup has completed, which may yet choose where files are " +
			"stored, the answer is 403 setup_required, unless STORYCREEP_ASSET_STORAGE names where they are." +
			authoringExportDoc + projectNotFoundDoc + tokenNotFoundDoc + snapshotNotFoundDoc +
			tokenPostReadDoc(scope.ExportRead),
		Tags:          []string{"Authoring export"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *createAuthoringExportInput) (*authoringExportOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		req := authoring.Request{Profile: authoring.ProfileFull, IncludeAssets: true, IncludeReleases: true}
		if in.Body.Profile != "" {
			req.Profile = authoring.Profile(in.Body.Profile)
		}
		if in.Body.IncludeAssets != nil {
			req.IncludeAssets = *in.Body.IncludeAssets
		}
		if in.Body.IncludeReleases != nil {
			req.IncludeReleases = *in.Body.IncludeReleases
		}
		if in.Body.Source != "" && in.Body.Source != draftTarget {
			snapshotID, err := ids.Decode(ids.Snapshot, in.Body.Source)
			if err != nil {
				return nil, err
			}
			req.SnapshotID = &snapshotID
		}
		e, err := exports.Create(ctx, actor, id, req)
		if err != nil {
			return nil, authoringError(ctx, err)
		}
		return &authoringExportOutput{Location: authoringExportPath(e), Body: authoringExportBody(e)}, nil
	}, api.TokenScope(scope.ExportRead), api.RateLimit(api.BucketExport))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listAuthoringExports",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/authoring-exports",
		Summary:     "List your authoring exports of a project",
		Description: "Lists the exports of the project that the user asked for and that have not expired, newest " +
			"first." + authoringExportDoc + projectNotFoundDoc + tokenNotFoundDoc + tokenAuthDoc(scope.ExportRead),
		Tags: []string{"Authoring export"},
	}, func(ctx context.Context, in *authoringExportsInput) (*authoringExportsOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := exports.List(ctx, actor, id, after, in.Fetch())
		if err != nil {
			return nil, authoringError(ctx, err)
		}
		bodies := make([]AuthoringExportBody, len(list))
		for i, e := range list {
			bodies[i] = authoringExportBody(e)
		}
		page, err := api.NewPage(in.PageParams, bodies, func(e AuthoringExportBody) idKey {
			return uuidKey(ids.Job, e.ID)
		})
		if err != nil {
			return nil, err
		}
		return &authoringExportsOutput{Body: page}, nil
	}, api.TokenScope(scope.ExportRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getAuthoringExport",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/authoring-exports/{export_id}",
		Summary:     "Read an authoring export",
		Description: "Returns the export: where its build is, and, once it is ready, the archive's size, whether it " +
			"is larger than the instance's import limit, and its name." + authoringExportDoc + projectNotFoundDoc +
			tokenNotFoundDoc + tokenAuthDoc(scope.ExportRead),
		Tags: []string{"Authoring export"},
	}, func(ctx context.Context, in *authoringExportInput) (*authoringExportGetOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		exportID, err := ids.Decode(ids.Job, in.ExportID)
		if err != nil {
			return nil, err
		}
		e, err := exports.Get(ctx, actor, id, exportID)
		if err != nil {
			return nil, authoringError(ctx, err)
		}
		return &authoringExportGetOutput{Body: authoringExportBody(e)}, nil
	}, api.TokenScope(scope.ExportRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "downloadAuthoringExport",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/authoring-exports/{export_id}/download",
		Summary:     "Download an authoring export",
		Description: "Returns the archive of a ready export, as a zip, whose README and authoring.json say what it " +
			"holds, and Content-Disposition names it, as an attachment. An export that is not ready is answered with " +
			"409 export_not_ready, whose details.status says where it is." + authoringExportDoc + projectNotFoundDoc +
			tokenNotFoundDoc + tokenAuthDoc(scope.ExportRead),
		Tags: []string{"Authoring export"},
		Responses: map[string]*huma.Response{"200": {
			Description: "The archive",
			Headers: map[string]*huma.Header{
				"Content-Disposition": {Schema: &huma.Schema{Type: huma.TypeString,
					Description: "attachment, with the archive's name: the project's slug, the snapshot's version or draft, and authoring, as harbour-draft-authoring.zip"}},
				"X-Content-Type-Options": {Schema: &huma.Schema{Type: huma.TypeString, Description: "nosniff"}},
			},
			Content: map[string]*huma.MediaType{zipType: {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}},
		}},
	}, func(ctx context.Context, in *authoringExportInput) (*huma.StreamResponse, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		exportID, err := ids.Decode(ids.Job, in.ExportID)
		if err != nil {
			return nil, err
		}
		d, err := exports.Open(ctx, actor, id, exportID)
		if err != nil {
			return nil, authoringError(ctx, err)
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
	}, api.TokenScope(scope.ExportRead), api.RateLimit(api.BucketExport))
}

// authoringError returns the error that the API answers err with.
func authoringError(ctx context.Context, err error) error {
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
	return snapshotError(ctx, err)
}
