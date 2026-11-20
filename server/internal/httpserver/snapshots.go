package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
)

type publishInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Body      struct {
		Version string `json:"version" pattern:"^(0|[1-9][0-9]{0,8})\\.(0|[1-9][0-9]{0,8})\\.(0|[1-9][0-9]{0,8})$" doc:"The snapshot's version, MAJOR.MINOR.PATCH, each a number from 0 to 999,999,999 without a leading zero, which must be greater than every version of the project's snapshots that are building or ready. GET /api/v1/projects/{project_id}/snapshots/next-version proposes one."`
		Note    string `json:"note" minLength:"1" maxLength:"2000" doc:"The snapshot's changelog note, of 1 to 2,000 characters, without white space around it, its line breaks as line feeds, which the snapshot's manifest holds too (PRD FR-SNAP-01)"`
		// AcknowledgedSeq and AcknowledgedWarnings are the report that the
		// author validated, and the warnings of it that they acknowledged
		// (PRD §7.3).
		AcknowledgedSeq      int64                        `json:"acknowledged_seq" minimum:"0" doc:"The seq of the validation report that the author validated, GET /api/v1/projects/{project_id}/validation's, which may not be past the project's operation sequence"`
		AcknowledgedWarnings []validation.Acknowledgement `json:"acknowledged_warnings,omitempty" doc:"The warnings of that report that the author acknowledged, each by its code, its entity, its field, and its target, as the report's finding has them. Publishing needs every warning of the draft that is not muted acknowledged."`
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

// snapshotNotFoundDoc says, in the description of an operation on a
// snapshot, how a snapshot that the project lacks is answered.
const snapshotNotFoundDoc = " A snapshot that the project does not have is answered with 404 not_found."

// registerSnapshots registers the routes that publish a project's draft as a
// snapshot, a release, and read a snapshot (PRD FR-SNAP-01, FR-DEP-04). The
// list of a project's snapshots, and their files, are item 79's.
func registerSnapshots(m *api.Mux, snapshots *snapshot.Service) {
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

// snapshotError returns the error that the API answers err with, an error of
// package snapshot or of the project that it publishes (projectError), or
// err itself if it is none of them. The findings of a refusal of the publish
// gate are in the locale of ctx's localizer, as the report's are.
func snapshotError(ctx context.Context, err error) error {
	var blocked *snapshot.BlockedError
	var unacknowledged *snapshot.UnacknowledgedError
	var notNewer *snapshot.NotNewerError
	switch {
	case errors.Is(err, snapshot.ErrNotFound):
		return api.NewError(api.CodeNotFound)
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

// gateError returns the error of the code that the publish gate refuses a
// publication with, whose details hold the seq of the draft that it
// validated and the findings of report that refuse it, as the report lists
// them, in the locale of ctx's localizer.
func gateError(ctx context.Context, code api.Code, report validation.Stored) error {
	r, _ := report.Report(i18n.FromContext(ctx))
	return &api.Error{Code: code, Details: map[string]any{"seq": report.Seq, "findings": r.Findings}}
}
