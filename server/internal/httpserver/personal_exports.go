package httpserver

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/authoring"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// personalExportsPath is the path of the signed-in account's personal-data
// exports.
const personalExportsPath = "/api/v1/auth/account/data-exports"

// personalExportDoc says who reads a personal-data export, and what the
// archive holds, in each route's description.
const personalExportDoc = " Only the user who asked for an export reads it, signed in, and an export of anyone " +
	"else, or one that has expired, is answered with 404 not_found. **The archive contains personal data**, all of " +
	"it the user's: the account and its email address, the IP addresses and user agents of its sessions and of the " +
	"audit log's entries of what the user did, and the comments that they wrote. It holds no password, no hash of " +
	"one, and no secret of a session, an API token, or a link."

// PersonalExportBody is a personal-data export, as the API shows it.
type PersonalExportBody struct {
	ID               string     `json:"id" doc:"The export's ID, a job's."`
	Status           string     `json:"status" enum:"queued,building,ready,failed" doc:"Where the export is: queued, building, ready, with a file to download, or failed."`
	ContainsPersonal bool       `json:"contains_personal_data" doc:"Whether the archive holds personal data, which it always does."`
	SizeBytes        *int64     `json:"size_bytes,omitempty" doc:"The archive's size, once it is ready."`
	FileName         string     `json:"file_name,omitempty" doc:"The name that a ready archive is downloaded under."`
	CreatedAt        time.Time  `json:"created_at" doc:"When the export was asked for."`
	FinishedAt       *time.Time `json:"finished_at,omitempty" doc:"When its build ended."`
	ExpiresAt        time.Time  `json:"expires_at" doc:"When the export and its file are deleted, seven days after it was asked for."`
}

func personalExportBody(e authoring.PersonalExport) PersonalExportBody {
	return PersonalExportBody{ID: e.ID, Status: e.Status, ContainsPersonal: true, SizeBytes: e.SizeBytes,
		FileName: e.FileName, CreatedAt: e.CreatedAt, FinishedAt: e.FinishedAt, ExpiresAt: e.ExpiresAt}
}

type createPersonalExportInput struct {
	CSRFInput
}

type personalExportOutput struct {
	Location string `header:"Location" doc:"The export's URL, which a client reads to learn when its build ends"`
	Body     PersonalExportBody
}

type personalExportInput struct {
	ExportID string `path:"export_id" doc:"The export's ID"`
}

type personalExportsInput struct {
	api.PageParams
}

type personalExportsOutput struct {
	Body api.Page[PersonalExportBody]
}

type personalExportGetOutput struct {
	Body PersonalExportBody
}

// personalActor returns the signed-in actor, and the export that id names, or
// the zero UUID for "".
func personalActor(ctx context.Context, id string) (ids.UUID, commands.Actor, error) {
	cur, err := currentFrom(ctx)
	if err != nil {
		return ids.UUID{}, commands.Actor{}, err
	}
	var export ids.UUID
	if id != "" {
		if export, err = ids.Decode(ids.Job, id); err != nil {
			return export, commands.Actor{}, err
		}
	}
	return export, cur.actor(ctx), nil
}

// registerPersonalExports registers the routes of the personal-data export
// (PRD FR-GDPR-01, plan note N94), each the signed-in account's own and of a
// session alone, as FR-AUTH-04 lists, since no scope of an API token grants
// it. The two that read all of the user's data, asking for an export and
// downloading it, count in the export bucket.
func registerPersonalExports(m *api.Mux, exports *authoring.Service) {
	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "createPersonalDataExport",
		Method:      http.MethodPost,
		Path:        personalExportsPath,
		Summary:     "Export your personal data",
		Description: "Asks for a zip of what the instance holds of the signed-in account, in the format of " +
			"schema/personal-data/v1, and answers 202 with the export, queued, whose URL Location names. A job builds " +
			"it, without an operator. The archive holds, from every workspace, whether or not the user is still a " +
			"member of it, what PRD §9.6 says: the account and its credentials' metadata, its email links' metadata, " +
			"sessions, API tokens, memberships and grants, the invitations to its address, once it is verified, " +
			"and those that the user accepted, the audit log's entries of what the user did, whole, and those of " +
			"what others did to them, with the actor as a role alone, the comments that they wrote, their " +
			"notifications, references to what they created, last changed, uploaded, published, or resolved, and " +
			"the imported identities that stand for them (PRD FR-GDPR-01). The archive is kept for seven days. A " +
			"user has one export on its way at a time, and another request is answered with 409 " +
			"export_in_progress, whose details.export_id names it. Until the instance's first-run setup has " +
			"completed, which may yet choose where files are stored, the answer is 403 setup_required, unless " +
			"STORYCREEP_ASSET_STORAGE names where they are." + personalExportDoc + csrfDoc,
		Tags:          []string{"Personal data export"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, _ *createPersonalExportInput) (*personalExportOutput, error) {
		_, actor, err := personalActor(ctx, "")
		if err != nil {
			return nil, err
		}
		e, err := exports.CreatePersonalExport(ctx, actor)
		if err != nil {
			return nil, workspaceExportError(err)
		}
		return &personalExportOutput{Location: personalExportsPath + "/" + e.ID, Body: personalExportBody(e)}, nil
	}, api.SessionOnly(), api.RateLimit(api.BucketExport))

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "listPersonalDataExports",
		Method:      http.MethodGet,
		Path:        personalExportsPath,
		Summary:     "List your personal-data exports",
		Description: "Lists the signed-in account's personal-data exports that have not expired, newest first." +
			personalExportDoc + sessionAuthDoc,
		Tags: []string{"Personal data export"},
	}, func(ctx context.Context, in *personalExportsInput) (*personalExportsOutput, error) {
		_, actor, err := personalActor(ctx, "")
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := exports.PersonalExports(ctx, actor, after, in.Fetch())
		if err != nil {
			return nil, workspaceExportError(err)
		}
		bodies := make([]PersonalExportBody, len(list))
		for i, e := range list {
			bodies[i] = personalExportBody(e)
		}
		page, err := api.NewPage(in.PageParams, bodies, func(e PersonalExportBody) idKey {
			return uuidKey(ids.Job, e.ID)
		})
		if err != nil {
			return nil, err
		}
		return &personalExportsOutput{Body: page}, nil
	}, api.SessionOnly())

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "getPersonalDataExport",
		Method:      http.MethodGet,
		Path:        personalExportsPath + "/{export_id}",
		Summary:     "Read a personal-data export",
		Description: "Returns the export: where its build is, and, once it is ready, the archive's size and its name." +
			personalExportDoc + sessionAuthDoc,
		Tags: []string{"Personal data export"},
	}, func(ctx context.Context, in *personalExportInput) (*personalExportGetOutput, error) {
		id, actor, err := personalActor(ctx, in.ExportID)
		if err != nil {
			return nil, err
		}
		e, err := exports.GetPersonalExport(ctx, actor, id)
		if err != nil {
			return nil, workspaceExportError(err)
		}
		return &personalExportGetOutput{Body: personalExportBody(e)}, nil
	}, api.SessionOnly())

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "downloadPersonalDataExport",
		Method:      http.MethodGet,
		Path:        personalExportsPath + "/{export_id}/download",
		Summary:     "Download a personal-data export",
		Description: "Returns the archive of a ready export, as a zip, whose README and personal-data.json say what " +
			"it holds, and Content-Disposition names it, as an attachment. An export that is not ready is answered " +
			"with 409 export_not_ready, whose details.status says where it is." + personalExportDoc +
			sessionAuthDoc,
		Tags: []string{"Personal data export"},
		Responses: map[string]*huma.Response{"200": {
			Description: "The archive",
			Headers: map[string]*huma.Header{
				"Content-Disposition": {Schema: &huma.Schema{Type: huma.TypeString,
					Description: "attachment, with the archive's name, of the day that its reading began, as personal-data-2026-11-25.zip"}},
				"X-Content-Type-Options": {Schema: &huma.Schema{Type: huma.TypeString, Description: "nosniff"}},
			},
			Content: map[string]*huma.MediaType{zipType: {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}},
		}},
	}, func(ctx context.Context, in *personalExportInput) (*huma.StreamResponse, error) {
		id, actor, err := personalActor(ctx, in.ExportID)
		if err != nil {
			return nil, err
		}
		d, err := exports.OpenPersonalExport(ctx, actor, id)
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
