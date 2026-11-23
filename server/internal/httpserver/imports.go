package httpserver

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/asset"
	"github.com/CyberCute-Studio/storycreep/server/internal/authoring"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/maintenance"
)

// maintenanceRef holds the maintenance path's pool once the server has opened
// it, for the import's collision check, which is all that the server asks of
// it (PRD §5.2, §5.4).
type maintenanceRef struct {
	db atomic.Pointer[maintenance.DB]
}

// AnyIDInUse says whether any of list is in use on the instance.
func (r *maintenanceRef) AnyIDInUse(ctx context.Context, list []ids.UUID) (bool, error) {
	db := r.db.Load()
	if db == nil {
		return false, errors.New("the maintenance path is not open")
	}
	return db.AnyIDInUse(ctx, list)
}

// importDoc says who reads an import, in each route's description.
const importDoc = " Only the user who asked for an import reads it, and an import of anyone else, or one that has " +
	"expired, is answered with 404 not_found."

// ProjectImportFailure is why an import was refused.
type ProjectImportFailure struct {
	Code    string `json:"code" doc:"What failed: archive_invalid (not an archive that the import's guards take), format_unsupported (a format or a version that this server does not read), schema_invalid, section_unsupported (a section that this server cannot import, which section names), content_invalid, release_invalid (a release that fails its verification, which version and check name), asset_invalid, workspace_limit, forbidden, canceled, expired, or internal."`
	Section string `json:"section,omitempty" doc:"The section of the archive that is at fault, if one is."`
	Version string `json:"version,omitempty" doc:"The version of the release that is at fault, if one is."`
	Check   string `json:"check,omitempty" doc:"The check that failed, as hashes, content_hash, schema, references_and_asts, or ast_version for a release."`
	Detail  string `json:"detail,omitempty" doc:"More about it, in English, for a log or a support request."`
}

// ProjectImportReport is what a finished import did.
type ProjectImportReport struct {
	Policy        string `json:"policy" enum:"preserve,remap" doc:"The ID policy that was applied: preserve kept the archive's IDs, and remap gave everything fresh ones, as at least one was in use on this instance, or it was asked for (PRD §5.2). It says nothing else of what is on the instance."`
	ProjectID     string `json:"project_id" doc:"The new project's ID."`
	ProjectName   string `json:"project_name" doc:"The new project's name."`
	Boards        int    `json:"boards" doc:"How many boards it imported."`
	Nodes         int    `json:"nodes" doc:"How many nodes it imported."`
	Variables     int    `json:"variables" doc:"How many variables it imported."`
	Components    int    `json:"components" doc:"How many components it imported."`
	Comments      int    `json:"comments" doc:"How many comments it imported."`
	PlayStates    int    `json:"play_states" doc:"How many play states it imported."`
	Releases      int    `json:"releases" doc:"How many releases it imported, each verified."`
	Assets        int    `json:"assets" doc:"How many assets it imported."`
	ImportedUsers int    `json:"imported_users" doc:"How many of the people whom the archive names are not members of the workspace, and are shown as imported users, with the display names from the archive."`
	MissingAssets int    `json:"missing_assets" doc:"How many references to assets it could not keep, as the archive holds no assets."`
}

// ProjectImportBody is an import, as the API shows it.
type ProjectImportBody struct {
	ID            string                `json:"id" doc:"The import's ID, a job's."`
	WorkspaceID   string                `json:"workspace_id" doc:"The workspace that it imports into."`
	Status        string                `json:"status" enum:"uploading,queued,importing,ready,failed" doc:"Where the import is: uploading, which takes chunks; queued, whose upload is whole; importing; ready, with a report; or failed, with why."`
	FileName      string                `json:"file_name" doc:"The archive's file name."`
	SizeBytes     int64                 `json:"size_bytes" doc:"The archive's size."`
	ChunkBytes    int                   `json:"chunk_bytes" doc:"The size of every chunk but the last."`
	ChunkCount    int                   `json:"chunk_count" doc:"How many chunks the upload has."`
	Received      []int                 `json:"received_chunks" nullable:"false" doc:"The indexes of the chunks that are stored, while the import is uploading, for a client to resume from."`
	IDPolicy      string                `json:"id_policy" enum:"auto,remap" doc:"auto keeps the archive's IDs unless one is in use on this instance, and remap gives everything fresh IDs."`
	Phase         string                `json:"phase" doc:"What the import is doing: upload, queued, checking, verifying, assets, writing, done, or failed."`
	ProgressDone  int64                 `json:"progress_done" doc:"How much of the phase is done."`
	ProgressTotal int64                 `json:"progress_total" doc:"How much of it there is, or 0 where it is not counted."`
	ProjectID     string                `json:"project_id,omitempty" doc:"The project that a ready import created, unless it has been purged since."`
	Report        *ProjectImportReport  `json:"report,omitempty" doc:"What a ready import did."`
	Failure       *ProjectImportFailure `json:"failure,omitempty" doc:"Why a failed import was refused. Nothing of the archive was imported."`
	CreatedAt     time.Time             `json:"created_at" doc:"When the import was asked for."`
	FinishedAt    *time.Time            `json:"finished_at,omitempty" doc:"When it ended."`
	ExpiresAt     time.Time             `json:"expires_at" doc:"When the import is deleted, with what is left of its upload. The project stays."`
}

func projectImportBody(i authoring.Import) ProjectImportBody {
	body := ProjectImportBody{ID: i.ID, WorkspaceID: i.WorkspaceID, Status: i.Status, FileName: i.FileName,
		SizeBytes: i.SizeBytes, ChunkBytes: i.ChunkBytes, ChunkCount: i.ChunkCount, Received: i.Received,
		IDPolicy: i.IDPolicy, Phase: i.Phase, ProgressDone: i.ProgressDone, ProgressTotal: i.ProgressTotal,
		ProjectID: i.ProjectID, CreatedAt: i.CreatedAt, FinishedAt: i.FinishedAt, ExpiresAt: i.ExpiresAt}
	if body.Received == nil {
		body.Received = []int{}
	}
	if r := i.Report; r != nil {
		body.Report = &ProjectImportReport{Policy: r.Policy, ProjectID: r.ProjectID, ProjectName: r.ProjectName,
			Boards: r.Boards, Nodes: r.Nodes, Variables: r.Variables, Components: r.Components, Comments: r.Comments,
			PlayStates: r.PlayStates, Releases: r.Releases, Assets: r.Assets, ImportedUsers: r.ImportedUsers,
			MissingAssets: r.MissingAssets}
	}
	if f := i.Failure; f != nil {
		body.Failure = &ProjectImportFailure{Code: f.Code, Section: f.Section, Version: f.Version, Check: f.Check,
			Detail: f.Detail}
	}
	return body
}

type createProjectImportInput struct {
	CSRFInput
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	Body        struct {
		FileName  string `json:"file_name" minLength:"1" maxLength:"255" doc:"The archive's file name, which the import shows."`
		SizeBytes int64  `json:"size_bytes" minimum:"1" doc:"The archive's size in bytes, at most the instance's STORYCREEP_IMPORT_MAX_BYTES."`
		IDPolicy  string `json:"id_policy,omitempty" enum:"auto,remap" doc:"auto, the default, keeps the archive's IDs unless one is in use on this instance, and remap gives everything fresh IDs (PRD §5.2)."`
	}
}

type projectImportOutput struct {
	Location string `header:"Location" doc:"The import's URL"`
	Body     ProjectImportBody
}

type projectImportsInput struct {
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	api.PageParams
}

type projectImportsOutput struct {
	Body api.Page[ProjectImportBody]
}

type projectImportInput struct {
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	ImportID    string `path:"import_id" doc:"The import's ID"`
}

type projectImportGetOutput struct {
	Body ProjectImportBody
}

type projectImportActInput struct {
	CSRFInput
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	ImportID    string `path:"import_id" doc:"The import's ID"`
}

type putImportChunkInput struct {
	CSRFInput
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	ImportID    string `path:"import_id" doc:"The import's ID"`
	Index       int    `path:"index" minimum:"0" doc:"The chunk's index, from 0"`
	RawBody     []byte `contentType:"application/octet-stream"`
}

// importActor returns the workspace and the import of a request's path, with
// the actor.
func importActor(ctx context.Context, workspaceID, importID string) (ws, id ids.UUID, actor commands.Actor, err error) {
	cur, err := currentFrom(ctx)
	if err != nil {
		return ws, id, actor, err
	}
	if ws, err = ids.Decode(ids.Workspace, workspaceID); err != nil {
		return ws, id, actor, err
	}
	if importID != "" {
		if id, err = ids.Decode(ids.Job, importID); err != nil {
			return ws, id, actor, err
		}
	}
	return ws, id, cur.actor(ctx), nil
}

// registerProjectImports registers the routes of the project import (PRD
// FR-DATA-01, plan note N91), which create a project, so that each is the
// workspace's owners' and admins' and takes a session alone, as creating a
// project does (§5.6).
func registerProjectImports(m *api.Mux, imports *authoring.Service) {
	const importsPath = "/api/v1/workspaces/{workspace_id}/project-imports"
	url := func(i authoring.Import) string {
		return "/api/v1/workspaces/" + i.WorkspaceID + "/project-imports/" + i.ID
	}
	api.Register(m, api.ManageProjects, huma.Operation{
		OperationID: "createProjectImport",
		Method:      http.MethodPost,
		Path:        importsPath,
		Summary:     "Begin importing a project",
		Description: "Begins the import of an authoring export (schema/authoring/v1) as a new project of the workspace, " +
			"and answers 201 with the import, uploading. Send its chunks with PUT …/chunks/{index}, each of chunk_bytes " +
			"but the last, in any order, and again after an interruption, which GET of the import, whose " +
			"received_chunks lists those that are stored, resumes from. Then POST …/complete hands it to a job. Only an " +
			"owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 " +
			"insufficient_role; a workspace at its projects limit is answered with 409 limit_exceeded, before anything " +
			"is uploaded. An archive past STORYCREEP_IMPORT_MAX_BYTES is answered with 413 request_too_large. A user has " +
			"one import of a workspace on its way at a time, and another request is answered with 409 " +
			"import_in_progress, whose details.import_id names it. Until the instance's first-run setup has completed, " +
			"the answer is 403 setup_required, unless STORYCREEP_ASSET_STORAGE names where files are stored." +
			importDoc + notFoundDoc + csrfDoc,
		Tags:          []string{"Project import"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createProjectImportInput) (*projectImportOutput, error) {
		ws, _, actor, err := importActor(ctx, in.WorkspaceID, "")
		if err != nil {
			return nil, err
		}
		i, err := imports.CreateImport(ctx, actor, ws, authoring.ImportRequest{FileName: in.Body.FileName,
			SizeBytes: in.Body.SizeBytes, Remap: in.Body.IDPolicy == authoring.PolicyRemap})
		if err != nil {
			return nil, importError(err)
		}
		return &projectImportOutput{Location: url(i), Body: projectImportBody(i)}, nil
	}, api.SessionOnly(), api.RateLimit(api.BucketExport))

	api.Register(m, api.ManageProjects, huma.Operation{
		OperationID: "listProjectImports",
		Method:      http.MethodGet,
		Path:        importsPath,
		Summary:     "List your imports of a workspace",
		Description: "Lists the imports of the workspace that the user asked for and that have not expired, newest " +
			"first." + importDoc + notFoundDoc + sessionAuthDoc,
		Tags: []string{"Project import"},
	}, func(ctx context.Context, in *projectImportsInput) (*projectImportsOutput, error) {
		ws, _, actor, err := importActor(ctx, in.WorkspaceID, "")
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := imports.ListImports(ctx, actor, ws, after, in.Fetch())
		if err != nil {
			return nil, importError(err)
		}
		bodies := make([]ProjectImportBody, len(list))
		for i, x := range list {
			bodies[i] = projectImportBody(x)
		}
		page, err := api.NewPage(in.PageParams, bodies, func(b ProjectImportBody) idKey {
			return uuidKey(ids.Job, b.ID)
		})
		if err != nil {
			return nil, err
		}
		return &projectImportsOutput{Body: page}, nil
	}, api.SessionOnly())

	api.Register(m, api.ManageProjects, huma.Operation{
		OperationID: "getProjectImport",
		Method:      http.MethodGet,
		Path:        importsPath + "/{import_id}",
		Summary:     "Read an import",
		Description: "Returns the import: which chunks of its upload are stored, how far its job is, and what it " +
			"did or why it was refused." + importDoc + notFoundDoc + sessionAuthDoc,
		Tags: []string{"Project import"},
	}, func(ctx context.Context, in *projectImportInput) (*projectImportGetOutput, error) {
		ws, id, actor, err := importActor(ctx, in.WorkspaceID, in.ImportID)
		if err != nil {
			return nil, err
		}
		i, err := imports.GetImport(ctx, actor, ws, id)
		if err != nil {
			return nil, importError(err)
		}
		return &projectImportGetOutput{Body: projectImportBody(i)}, nil
	}, api.SessionOnly())

	api.Register(m, api.ManageProjects, huma.Operation{
		OperationID: "putProjectImportChunk",
		Method:      http.MethodPut,
		Path:        importsPath + "/{import_id}/chunks/{index}",
		Summary:     "Upload a chunk of an import",
		Description: "Stores a chunk of the archive, and answers 204. Sending a chunk again replaces it, so an upload " +
			"that an interruption cut off goes on from the chunks that GET of the import does not list. A chunk has " +
			"chunk_bytes, or the rest of the archive if it is the last, and another size or an index past the last is " +
			"answered with 422 chunk_invalid. An import that is not uploading is answered with 409 " +
			"import_not_uploading, whose details.status says where it is." + importDoc + notFoundDoc + csrfDoc,
		Tags:          []string{"Project import"},
		DefaultStatus: http.StatusNoContent,
		RequestBody: &huma.RequestBody{Required: true, Description: "The chunk's bytes.",
			Content: map[string]*huma.MediaType{"application/octet-stream": {
				Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}}},
		MaxBodyBytes:    authoring.ChunkBytes,
		BodyReadTimeout: uploadReadTimeout,
	}, func(ctx context.Context, in *putImportChunkInput) (*struct{}, error) {
		ws, id, actor, err := importActor(ctx, in.WorkspaceID, in.ImportID)
		if err != nil {
			return nil, err
		}
		if err := imports.PutChunk(ctx, actor, ws, id, in.Index, in.RawBody); err != nil {
			return nil, importError(err)
		}
		return nil, nil
	}, api.SessionOnly(), api.AfterLimit(holdSlot(newSlots(uploadSlots(authoring.ChunkBytes)))))

	api.Register(m, api.ManageProjects, huma.Operation{
		OperationID: "completeProjectImport",
		Method:      http.MethodPost,
		Path:        importsPath + "/{import_id}/complete",
		Summary:     "Import an uploaded archive",
		Description: "Hands a whole upload to the import's job, and answers 202 with the import, queued. The job checks " +
			"the archive before it writes anything: its members against the zip guards, each file against its hash " +
			"and its schema, every release's files, hashes, references, and ASTs, and the sections that this server " +
			"can import. It then creates the project, keeping the archive's IDs unless one is in use on this instance " +
			"or remap was asked for, all or nothing, and the import's report or failure says what became of it. An " +
			"upload that lacks chunks is answered with 409 import_incomplete, whose details.missing_chunks lists the " +
			"first of them." + importDoc + notFoundDoc + csrfDoc,
		Tags:          []string{"Project import"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *projectImportActInput) (*projectImportGetOutput, error) {
		ws, id, actor, err := importActor(ctx, in.WorkspaceID, in.ImportID)
		if err != nil {
			return nil, err
		}
		i, err := imports.CompleteImport(ctx, actor, ws, id)
		if err != nil {
			return nil, importError(err)
		}
		return &projectImportGetOutput{Body: projectImportBody(i)}, nil
	}, api.SessionOnly(), api.RateLimit(api.BucketExport))

	api.Register(m, api.ManageProjects, huma.Operation{
		OperationID: "cancelProjectImport",
		Method:      http.MethodDelete,
		Path:        importsPath + "/{import_id}",
		Summary:     "Cancel an import",
		Description: "Ends an import that is uploading or queued, deletes what its upload stored, and answers 204. An " +
			"import that its job has begun runs to its end, as it is all or nothing, and one that is importing or has " +
			"ended is answered with 409 import_not_uploading." + importDoc + notFoundDoc + csrfDoc,
		Tags:          []string{"Project import"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *projectImportActInput) (*struct{}, error) {
		ws, id, actor, err := importActor(ctx, in.WorkspaceID, in.ImportID)
		if err != nil {
			return nil, err
		}
		if err := imports.CancelImport(ctx, actor, ws, id); err != nil {
			return nil, importError(err)
		}
		return nil, nil
	}, api.SessionOnly())
}

// importError returns the error that the API answers err with.
func importError(err error) error {
	var inProgress *authoring.ImportInProgressError
	var tooLarge *authoring.ImportTooLargeError
	var state *authoring.ImportStateError
	var incomplete *authoring.ImportIncompleteError
	var chunk *authoring.ChunkError
	switch {
	case errors.Is(err, authoring.ErrNotFound):
		return api.NewError(api.CodeNotFound)
	case errors.Is(err, asset.ErrSetupRequired):
		return api.NewError(api.CodeSetupRequired)
	case errors.As(err, &inProgress):
		return &api.Error{Code: api.CodeImportInProgress, Details: map[string]any{"import_id": inProgress.ID}}
	case errors.As(err, &tooLarge):
		return &api.Error{Code: api.CodeRequestTooLarge, Details: map[string]any{"max_bytes": tooLarge.Max}}
	case errors.As(err, &state):
		return &api.Error{Code: api.CodeImportNotUploading, Details: map[string]any{"status": state.Status}}
	case errors.As(err, &incomplete):
		return &api.Error{Code: api.CodeImportIncomplete, Details: map[string]any{"missing_chunks": incomplete.Missing}}
	case errors.As(err, &chunk):
		return &api.Error{Code: api.CodeChunkInvalid, Details: map[string]any{"index": chunk.Index,
			"expected_bytes": chunk.Expected, "bytes": chunk.Got}}
	}
	return projectError(err)
}
