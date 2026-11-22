package httpserver

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
)

type publishInput struct {
	SessionCSRFInput
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Body      struct {
		Version string `json:"version" pattern:"^(0|[1-9][0-9]{0,8})\\.(0|[1-9][0-9]{0,8})\\.(0|[1-9][0-9]{0,8})$" doc:"The snapshot's version, MAJOR.MINOR.PATCH, each a number from 0 to 999,999,999 without a leading zero, which must be greater than every version of the project's snapshots that are building or ready. GET /api/v1/projects/{project_id}/snapshots/next-version proposes one."`
		Note    string `json:"note" minLength:"1" maxLength:"2000" doc:"The snapshot's changelog note, of 1 to 2,000 characters, not all of them blank, without white space around it, its line breaks as line feeds, which the snapshot's manifest holds too (PRD FR-SNAP-01)"`
		// AcknowledgedSeq and AcknowledgedWarnings are the report that the
		// author validated, and the warnings of it that they acknowledged
		// (PRD §7.3).
		AcknowledgedSeq      int64                        `json:"acknowledged_seq" minimum:"0" doc:"The seq of the validation report that the author validated, GET /api/v1/projects/{project_id}/validation's, which may not be past the project's operation sequence"`
		AcknowledgedWarnings []validation.Acknowledgement `json:"acknowledged_warnings,omitempty" doc:"The warnings of that report that the author acknowledged, each by its code, its entity, its field, and its target, as the report's finding has them. Publishing needs every warning of the draft that is not muted acknowledged. The snapshot keeps those that name a warning of the draft as the publish gate validates it, each once."`
	}
}

type snapshotOutput struct {
	Body snapshot.Snapshot
}

type publishedOutput struct {
	Location string `header:"Location" doc:"The snapshot's URL, which a client reads to learn when its build ends"`
	Body     snapshot.Snapshot
}

type snapshotInput struct {
	ProjectID  string `path:"project_id" doc:"The project's ID"`
	SnapshotID string `path:"snapshot_id" doc:"The snapshot's ID"`
}

type snapshotReportOutput struct {
	// ContentLanguage and Vary name the language of the report's messages,
	// as those of the draft's report are named (validationOutput).
	ContentLanguage string   `header:"Content-Language" doc:"The language of the report's messages, as a BCP 47 tag, such as en: the request's locale, or English for a message that its catalog lacks, the languages listed when they are several"`
	Vary            []string `header:"Vary" doc:"Accept-Language, as the report's messages are in the language that it chose"`
	Body            validation.Report
}

type nextVersionInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
}

type nextVersionOutput struct {
	Body snapshot.Proposal
}

type snapshotsInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Status    string `query:"status" enum:"building,ready,failed" doc:"A status: only the snapshots of that status are listed, as ready for the project's releases, the newest first. Without it, those of every status."`
	api.PageParams
}

type snapshotsOutput struct {
	Body api.Page[snapshot.Snapshot]
}

// exportInput and draftExportInput take, as schema, the version of the
// published format that an export is rendered at, or 0, which no request may
// ask for, for the files' own.
type exportInput struct {
	ProjectID  string `path:"project_id" doc:"The project's ID"`
	SnapshotID string `path:"snapshot_id" doc:"The snapshot's ID"`
	Schema     int    `query:"schema" minimum:"1" doc:"The version of the published game format, its schemaVersion, that the files are rendered at: one of the versions that the server reads, which /version declares as published_schema, as the newest that the client reads too. Without it, the snapshot's own version."`
}

type draftExportInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Schema    int    `query:"schema" minimum:"1" doc:"The version of the published game format, its schemaVersion, that the files are rendered at: one of the versions that the server reads, which /version declares as published_schema, as the newest that the client reads too. Without it, the version that the server writes."`
}

// snapshotNotFoundDoc says, in the description of an operation on a
// snapshot, how a snapshot that the project lacks is answered.
const snapshotNotFoundDoc = " A snapshot that the project does not have is answered with 404 not_found."

// exportDoc says, in the description of an export, what the zip holds, and
// how a version of the published format that it cannot be rendered at is
// answered.
const exportDoc = " The zip holds a member of each file of the published layout (PRD §6.3), at its path, as " +
	"storycreep.json or boards/harbour-intro.json, in the order of their paths, with no member of a directory, " +
	"which the manifest lists with each file's SHA-256, and Content-Disposition names it, as an attachment. A " +
	"release of the server always makes the same zip of the same files, and another release may compress them " +
	"otherwise, so a client compares the files, by the manifest's hashes, not the zip's bytes. Asked with schema, the files are rendered at that version of the " +
	"published format: down to an older version by the renderer of each version between, or up to a newer one by " +
	"the migration to each, and they validate against that version's JSON Schemas (PRD FR-SYNC-01 AC2), with " +
	"the manifest's hashes and content hash those of the files at that version. A version that the server does " +
	"not read is answered with 409 schema_unsupported, with details.min and details.max, the oldest and the " +
	"newest that it reads (AC3), and an older version that cannot express what the files hold with 409 " +
	"schema_unrepresentable, which lists each value that it cannot express, so that nothing is dropped silently " +
	"(PRD §6.4)."

// registerSnapshots registers the routes that publish a project's draft as a
// snapshot, a release, list and read its snapshots, and export a snapshot's
// files and the draft's as a zip of the published layout (PRD FR-SNAP-01,
// FR-SNAP-02, FR-DEP-04, FR-SYNC-01).
func registerSnapshots(m *api.Mux, snapshots *snapshot.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listSnapshots",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/snapshots",
		Summary:     "List a project's snapshots",
		Description: "Lists the project's snapshots, its releases, newest first, by their IDs, which follow the order " +
			"in which they were published (PRD FR-SNAP-02), each with its metadata, as GET " +
			"/api/v1/projects/{project_id}/snapshots/{snapshot_id} returns it: its version, its note, who published " +
			"it and when, with their display name as it is now, and where it is in its lifecycle, building, ready, " +
			"with its content hash, its schema version, and its entity counts, or failed, with its failure_code. " +
			"With status, only the snapshots of that status are listed: a client that checks whether a newer " +
			"release exists reads the newest ready one, with status=ready and limit=1 (PRD §7.5), whose version is " +
			"the greatest of the project's releases." + projectNotFoundDoc + tokenNotFoundDoc +
			tokenAuthDoc(scope.SnapshotRead),
		Tags: []string{"Snapshots"},
	}, func(ctx context.Context, in *snapshotsInput) (*snapshotsOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := snapshots.List(ctx, actor, id, snapshot.Status(in.Status), after, in.Fetch())
		if err != nil {
			return nil, snapshotError(ctx, err)
		}
		page, err := api.NewPage(in.PageParams, list, func(s snapshot.Snapshot) idKey {
			return uuidKey(ids.Snapshot, s.ID)
		})
		if err != nil {
			return nil, err
		}
		return &snapshotsOutput{Body: page}, nil
	}, api.TokenScope(scope.SnapshotRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "exportSnapshot",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/snapshots/{snapshot_id}/export",
		Summary:     "Export a snapshot",
		Description: "Returns a ready snapshot's files as a zip of the published layout, at the version of the " +
			"published game format that schema asks for, or, without it, at the snapshot's own version, as its " +
			"schema_version names it, the files that its build stored, byte for byte (PRD FR-SNAP-02, FR-SYNC-01 " +
			"AC1): what a game pulls (PRD §7.5), and what a release's download is. A snapshot that is building, or " +
			"that failed, has no files, and is answered with 409 snapshot_not_ready, whose details.status says " +
			"which." + exportDoc + projectNotFoundDoc + tokenNotFoundDoc + snapshotNotFoundDoc +
			tokenAuthDoc(scope.SnapshotRead),
		Tags:      []string{"Snapshots"},
		Responses: exportResponses("The snapshot's files, as a zip of the published layout"),
	}, func(ctx context.Context, in *exportInput) (*huma.StreamResponse, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		snapshotID, err := ids.Decode(ids.Snapshot, in.SnapshotID)
		if err != nil {
			return nil, err
		}
		e, err := snapshots.Export(ctx, actor, id, snapshotID, in.Schema)
		if err != nil {
			return nil, snapshotError(ctx, err)
		}
		return serveExport(e)
	}, api.TokenScope(scope.SnapshotRead), api.RateLimit(api.BucketExport))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "exportDraft",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/draft/export",
		Summary:     "Export the draft",
		Description: "Returns the files of the project's draft as it is now, as a zip of the published layout, at " +
			"the version of the published game format that schema asks for, or, without it, at the version that " +
			"the server writes, read as of one moment (PRD FR-SYNC-01, §7.8), as a game's draft pull fetches it. " +
			"It is always marked as the draft's: its manifest's snapshot has the channel draft, the draft's " +
			"content hash, and when it was exported, as pulledAt, and neither an ID nor a version, which a draft " +
			"has none of, and Content-Disposition names it as the draft's. What the draft holds that no release " +
			"may, as a condition that does not compile, it stands in for as play mode does, so that it is " +
			"exported whatever validation would say of publishing it. The draft is not a release, so an API " +
			"token needs project:read, and one that holds snapshot:read alone is answered with 403 " +
			"insufficient_scope (PRD FR-SYNC-01 AC4)." + exportDoc + projectNotFoundDoc + tokenNotFoundDoc +
			tokenAuthDoc(scope.ProjectRead),
		Tags:      []string{"Snapshots"},
		Responses: exportResponses("The draft's files, as a zip of the published layout"),
	}, func(ctx context.Context, in *draftExportInput) (*huma.StreamResponse, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		e, err := snapshots.ExportDraft(ctx, actor, id, in.Schema)
		if err != nil {
			return nil, snapshotError(ctx, err)
		}
		return serveExport(e)
	}, api.TokenScope(scope.ProjectRead), api.RateLimit(api.BucketExport))

	api.Register(m, api.PublishSnapshots, huma.Operation{
		OperationID: "publishSnapshot",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects/{project_id}/snapshots",
		Summary:     "Publish a snapshot",
		Description: "Publishes the project's draft as a snapshot, a release, of the version and the note that the " +
			"author chose (PRD FR-SNAP-01, §7.3), and answers 202 with the snapshot, building, whose URL Location " +
			"names. The publish gate first validates the draft as it is now, as GET " +
			"/api/v1/projects/{project_id}/validation does (PRD FR-DEP-04): a draft with an error is answered with " +
			"409 publish_blocked, which lists the errors, and one with a warning that is neither muted nor among " +
			"acknowledged_warnings with 409 warnings_unacknowledged, which lists those warnings, so that the author " +
			"acknowledges them with the report's seq as acknowledged_seq. The version must be greater than every " +
			"version of the project's snapshots that are building or ready, or the answer is 409 " +
			"version_not_newer, with the newest; a failed snapshot's version may be published again. A job then " +
			"builds the snapshot: it reads the draft as of one moment, whose seq the snapshot records as its " +
			"source_seq, validates it again, which alone decides, and serializes it into the published layout, " +
			"whose files it validates against the schemas of the published format. The snapshot becomes ready, and " +
			"never changes again, or failed, with its failure_code: validation_failed for an error, " +
			"validation_changed for a warning that was not acknowledged of a draft that changed since " +
			"acknowledged_seq, or build_failed. Its build's report is stored with it either way, at GET " +
			"/api/v1/projects/{project_id}/snapshots/{snapshot_id}/validation. An owner, an admin, or an editor of " +
			"the project may publish, unless the project's publish_min_role is admin, which leaves editors out, and " +
			"anyone else with a role on it is answered with 403 insufficient_role (PRD FR-SNAP-01 AC3). The " +
			"publication is recorded in the audit log." + projectNotFoundDoc + tokenNotFoundDoc + archivedDoc +
			tokenChangeDoc(scope.SnapshotWrite),
		Tags:          []string{"Snapshots"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *publishInput) (*publishedOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		s, err := snapshots.Publish(ctx, actor, id, snapshot.Request{Version: in.Body.Version, Note: in.Body.Note,
			AcknowledgedSeq: in.Body.AcknowledgedSeq, AcknowledgedWarnings: in.Body.AcknowledgedWarnings})
		if err != nil {
			return nil, snapshotError(ctx, err)
		}
		return &publishedOutput{Location: snapshotPath(s), Body: s}, nil
	}, api.TokenScope(scope.SnapshotWrite))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getNextSnapshotVersion",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/snapshots/next-version",
		Summary:     "Propose the next snapshot's version",
		Description: "Returns the version that the server proposes for the project's next snapshot (PRD §7.3): a " +
			"patch bump of the newest version of its snapshots that are building or ready, which it names too, or " +
			"0.1.0 for a project that has none. The author may publish any version that is greater than the newest." +
			projectNotFoundDoc + tokenNotFoundDoc + tokenAuthDoc(scope.SnapshotRead),
		Tags: []string{"Snapshots"},
	}, func(ctx context.Context, in *nextVersionInput) (*nextVersionOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		p, err := snapshots.NextVersion(ctx, actor, id)
		if err != nil {
			return nil, snapshotError(ctx, err)
		}
		return &nextVersionOutput{Body: p}, nil
	}, api.TokenScope(scope.SnapshotRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getSnapshot",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/snapshots/{snapshot_id}",
		Summary:     "Get a snapshot",
		Description: "Returns a snapshot of the project: its version, its note, who published it and when, and " +
			"where it is in its lifecycle (PRD §7.3), building until its build ends, then ready, with its content " +
			"hash, its schema version, and its entity counts, or failed, with its failure_code, which a client " +
			"reads to learn how a publication ended." + projectNotFoundDoc + tokenNotFoundDoc + snapshotNotFoundDoc +
			tokenAuthDoc(scope.SnapshotRead),
		Tags: []string{"Snapshots"},
	}, func(ctx context.Context, in *snapshotInput) (*snapshotOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		snapshotID, err := ids.Decode(ids.Snapshot, in.SnapshotID)
		if err != nil {
			return nil, err
		}
		s, err := snapshots.Get(ctx, actor, id, snapshotID)
		if err != nil {
			return nil, snapshotError(ctx, err)
		}
		return &snapshotOutput{Body: s}, nil
	}, api.TokenScope(scope.SnapshotRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getSnapshotValidation",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/snapshots/{snapshot_id}/validation",
		Summary:     "Get a snapshot's validation report",
		Description: "Returns the validation report that the snapshot's build computed and stored with it (PRD " +
			"FR-DEP-04 AC2): the report of the draft as of the snapshot's source_seq, as GET " +
			"/api/v1/projects/{project_id}/validation reports a draft, with its messages in the request's locale, " +
			"which describes exactly what the build read, and so a ready snapshot's files (AC4), or why a failed " +
			"one failed. It needs the scope project:read of an API token, since a failed snapshot's report is of a " +
			"draft that was never released." + projectNotFoundDoc + tokenNotFoundDoc + snapshotNotFoundDoc +
			" So is a snapshot that holds no report: one that is building still, or whose build failed before it " +
			"read the draft." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Snapshots"},
	}, func(ctx context.Context, in *snapshotInput) (*snapshotReportOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		snapshotID, err := ids.Decode(ids.Snapshot, in.SnapshotID)
		if err != nil {
			return nil, err
		}
		report, languages, err := snapshots.Report(ctx, actor, id, snapshotID)
		if err != nil {
			return nil, snapshotError(ctx, err)
		}
		return &snapshotReportOutput{ContentLanguage: languages, Vary: []string{"Accept-Language"}, Body: report}, nil
	}, api.TokenScope(scope.ProjectRead))
}

// snapshotPath returns the URL of the snapshot s.
func snapshotPath(s snapshot.Snapshot) string {
	return "/api/v1/projects/" + s.ProjectID + "/snapshots/" + s.ID
}

// zipType is the media type of an export, a zip of the published layout.
const zipType = "application/zip"

// exportResponses documents the answer of an export, whose zip doc
// describes.
func exportResponses(doc string) map[string]*huma.Response {
	header := func(doc string) *huma.Header {
		return &huma.Header{Schema: &huma.Schema{Type: huma.TypeString, Description: doc}}
	}
	return map[string]*huma.Response{"200": {
		Description: doc,
		Headers: map[string]*huma.Header{
			"Content-Disposition":    header("attachment, with the zip's name: the project's slug and the snapshot's version, or draft, as harbour-1.0.0.zip"),
			"X-Content-Type-Options": header("nosniff"),
		},
		Content: map[string]*huma.MediaType{zipType: {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}},
	}}
}

// serveExport answers with the zip of the export e, whose length it gives,
// as an attachment named as e is, and with the zip alone to a GET, which a
// HEAD request, which the route answers as GET (middleware.GetHead), is
// answered without.
func serveExport(e snapshot.Export) (*huma.StreamResponse, error) {
	data, err := e.Zip()
	if err != nil {
		return nil, err
	}
	return serveFile(data, zipType, e.Name), nil
}

// serveFile answers with data, a file of the media type contentType, whose
// length it gives, as an attachment of the given name, and with the file
// alone to a GET, which a HEAD request, which the route answers as GET
// (middleware.GetHead), is answered without.
func serveFile(data []byte, contentType, name string) *huma.StreamResponse {
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	return &huma.StreamResponse{Body: func(hc huma.Context) {
		hc.SetHeader("Content-Type", contentType)
		hc.SetHeader("Content-Length", strconv.Itoa(len(data)))
		hc.SetHeader("Content-Disposition", disposition)
		hc.SetHeader("X-Content-Type-Options", "nosniff")
		hc.SetStatus(http.StatusOK)
		if hc.Method() == http.MethodHead {
			return
		}
		// The status and the headers are sent, so a failure from here on can
		// only end the answer early, which the client sees as a body shorter
		// than its Content-Length.
		_, _ = hc.BodyWriter().Write(data)
	}}
}

// snapshotError returns the error that the API answers err with, an error of
// package snapshot, of the versions of the published format that it exports
// at (package schemaversion), or of the project that it publishes
// (projectError), or err itself if it is none of them. The findings of a
// refusal of the publish gate are in the locale of ctx's localizer, as the
// report's are.
func snapshotError(ctx context.Context, err error) error {
	var blocked *snapshot.BlockedError
	var unacknowledged *snapshot.UnacknowledgedError
	var notNewer *snapshot.NotNewerError
	var notReady *snapshot.NotReadyError
	var unsupported *schemaversion.UnsupportedError
	var unrepresentable *schemaversion.UnrepresentableError
	switch {
	case errors.Is(err, snapshot.ErrNotFound):
		return api.NewError(api.CodeNotFound)
	case errors.As(err, &notReady):
		return &api.Error{Code: api.CodeSnapshotNotReady, Details: map[string]any{"status": notReady.Status}}
	case errors.As(err, &unsupported):
		return &api.Error{Code: api.CodeSchemaUnsupported, Details: map[string]any{
			"min": unsupported.Supported.Min, "max": unsupported.Supported.Max}}
	case errors.As(err, &unrepresentable):
		constructs := make([]unrepresentableConstruct, len(unrepresentable.Constructs))
		for i, c := range unrepresentable.Constructs {
			constructs[i] = unrepresentableConstruct{File: c.File, Entity: c.Entity, Field: c.Field, Message: c.Why}
		}
		return &api.Error{Code: api.CodeSchemaUnrepresentable, Details: map[string]any{"schema": unrepresentable.To,
			"constructs": constructs}}
	case errors.As(err, &blocked):
		return gateError(ctx, api.CodePublishBlocked, blocked.Report)
	case errors.As(err, &unacknowledged):
		return gateError(ctx, api.CodeWarningsUnacknowledged, unacknowledged.Report)
	case errors.As(err, &notNewer):
		details := map[string]any{"latest_version": notNewer.Latest, "errors": []api.Problem{{
			Location: "body.version", Message: "is not greater than " + notNewer.Latest + ", the newest version of " +
				"the project's snapshots that are building or ready"}}}
		if notNewer.Proposed != "" {
			details["proposed_version"] = notNewer.Proposed
		}
		return &api.Error{Code: api.CodeVersionNotNewer, Details: details}
	}
	return projectError(err)
}

// unrepresentableConstruct is a value that an older version of the published
// format cannot express, as an answer of schema_unrepresentable lists it
// (schemaversion.Construct): its file, its entity's ID, which a value of the
// file's own has none of, its field, and why, in English, as the renderer
// says.
type unrepresentableConstruct struct {
	File    string `json:"file"`
	Entity  string `json:"entity,omitempty"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// gateError returns the error of the code that the publish gate refuses a
// publication with, whose details hold the seq of the draft that it
// validated and the findings of report that refuse it, as the report lists
// them, in the locale of ctx's localizer, whose languages Content-Language
// lists.
func gateError(ctx context.Context, code api.Code, report validation.Stored) error {
	r, languages := report.Report(i18n.FromContext(ctx))
	return &api.Error{Code: code, Details: map[string]any{"seq": report.Seq, "findings": r.Findings},
		Languages: strings.Split(languages, ", ")}
}
