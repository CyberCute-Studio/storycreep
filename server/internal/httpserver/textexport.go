package httpserver

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
	"github.com/CyberCute-Studio/storycreep/server/internal/textexport"
)

// TextExportParams are the parameters of a text export.
type TextExportParams struct {
	Format string `query:"format" enum:"csv,json" default:"csv" doc:"The format of the file: csv, as RFC 4180 has it, in UTF-8 with a byte order mark, or json, the lossless format. Without it, csv."`
	Board  string `query:"board" doc:"The ID of a board: only that board's text is exported. Without it, the whole project's."`
}

type draftTextExportInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	TextExportParams
}

type snapshotTextExportInput struct {
	ProjectID  string `path:"project_id" doc:"The project's ID"`
	SnapshotID string `path:"snapshot_id" doc:"The snapshot's ID"`
	TextExportParams
}

// textExportDoc says, in the description of a text export, what the file
// holds.
const textExportDoc = " The file has a row of each text, an element's text, of the type body, and a connection's " +
	"label, of the type choice, with the columns node_id, board (its name), node_title, speaker, text_type, text " +
	"(the plain text), condition (the source of a choice's condition, as its author wrote it), word_count, and " +
	"text_hash (sha256: and the SHA-256 of the plain text), as PRD FR-TEXT-05 and schema/rich-text/README.md " +
	"define them. A choice has no speaker, a body no condition, and a text without plain text no row. The rows are " +
	"in one order: the boards by ID, each board's nodes as its script view lists them, breadth-first from its " +
	"entry node, and then those that it does not reach, by ID, and each node's text before its choices, in the " +
	"order that the flow offers them. So an unchanged project exports the same bytes, and the word counts of a " +
	"draft's rows add up to its writing statistics (GET /api/v1/projects/{project_id}/statistics). In the CSV, " +
	"whose records end with CRLF, a text cell that starts with =, +, -, @, a tab, or a carriage return is prefixed " +
	"with a single quote, so that a spreadsheet shows it as text and never evaluates it. The JSON, an object of " +
	"the project, the source, the board if the export is narrowed to one, the total of words, and the rows, is " +
	"not altered, and the hash is always of the original text. Content-Disposition names the file, as an " +
	"attachment. A board that is not an ID of a board is answered with 400 invalid_id."

// textExportResponses documents the answer of a text export.
func textExportResponses(doc string) map[string]*huma.Response {
	header := func(doc string) *huma.Header {
		return &huma.Header{Schema: &huma.Schema{Type: huma.TypeString, Description: doc}}
	}
	file := func() *huma.MediaType {
		return &huma.MediaType{Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}
	}
	return map[string]*huma.Response{"200": {
		Description: doc,
		Headers: map[string]*huma.Header{
			"Content-Disposition":    header("attachment, with the file's name: the project's slug, the snapshot's version or draft, the board's slug if the export is narrowed to one, and text, as harbour-1.0.0-intro-text.csv"),
			"X-Content-Type-Options": header("nosniff"),
		},
		Content: map[string]*huma.MediaType{csvType: file(), jsonFileType: file()},
	}}
}

// The media types of a text export's file.
const (
	csvType      = "text/csv"
	jsonFileType = "application/json"
)

// registerTextExport registers the routes that export the narrative text of
// a project or a board, of the draft or of a snapshot, for review and
// voice-over (PRD FR-TEXT-05). Both need project:read (FR-AUTH-04), a
// snapshot's too, as the text export is no part of what snapshot:read grants,
// and count in the export bucket, as they read a project's content whole.
func registerTextExport(m *api.Mux, snapshots *snapshot.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "exportDraftText",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/draft/text-export",
		Summary:     "Export the draft's text",
		Description: "Returns the narrative text of the project's draft as it is now, read as of one moment, or of one " +
			"of its boards, as a CSV or a JSON file (PRD FR-TEXT-05), of what is not in its trash." + textExportDoc +
			" A board that the draft does not have, or that is in its trash, is answered with 404 not_found." +
			projectNotFoundDoc + tokenNotFoundDoc + tokenAuthDoc(scope.ProjectRead),
		Tags:      []string{"Snapshots"},
		Responses: textExportResponses("The draft's text, as a CSV or a JSON file"),
	}, func(ctx context.Context, in *draftTextExportInput) (*huma.StreamResponse, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		board, err := textExportBoard(in.Board)
		if err != nil {
			return nil, err
		}
		e, err := snapshots.TextExportDraft(ctx, actor, id, board)
		if err != nil {
			return nil, snapshotError(ctx, err)
		}
		return serveTextExport(e, in.Format)
	}, api.TokenScope(scope.ProjectRead), api.RateLimit(api.BucketExport))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "exportSnapshotText",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/snapshots/{snapshot_id}/text-export",
		Summary:     "Export a snapshot's text",
		Description: "Returns the narrative text of a ready snapshot, or of one of its boards, as a CSV or a JSON file " +
			"(PRD FR-TEXT-05), as its files hold it. A snapshot that is building, or that failed, has no files, and " +
			"is answered with 409 snapshot_not_ready, whose details.status says which." + textExportDoc +
			" A board that the snapshot does not have is answered with 404 not_found." + projectNotFoundDoc +
			tokenNotFoundDoc + snapshotNotFoundDoc + tokenAuthDoc(scope.ProjectRead),
		Tags:      []string{"Snapshots"},
		Responses: textExportResponses("The snapshot's text, as a CSV or a JSON file"),
	}, func(ctx context.Context, in *snapshotTextExportInput) (*huma.StreamResponse, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		snapshotID, err := ids.Decode(ids.Snapshot, in.SnapshotID)
		if err != nil {
			return nil, err
		}
		board, err := textExportBoard(in.Board)
		if err != nil {
			return nil, err
		}
		e, err := snapshots.TextExport(ctx, actor, id, snapshotID, board)
		if err != nil {
			return nil, snapshotError(ctx, err)
		}
		return serveTextExport(e, in.Format)
	}, api.TokenScope(scope.ProjectRead), api.RateLimit(api.BucketExport))
}

// textExportBoard returns the canonical spelling of the board's ID that a
// request names, "" for none, or the error of one that is not a board's ID.
func textExportBoard(board string) (string, error) {
	if board == "" {
		return "", nil
	}
	id, err := ids.Decode(ids.Board, board)
	if err != nil {
		return "", err
	}
	return ids.Encode(ids.Board, id), nil
}

// serveTextExport answers with the export e as a file of the format, csv or
// json, which the route's enum holds to those.
func serveTextExport(e *textexport.Export, format string) (*huma.StreamResponse, error) {
	if format == textexport.FormatJSON {
		data, err := e.JSON()
		if err != nil {
			return nil, err
		}
		return serveFile(data, jsonFileType, e.FileName(textexport.FormatJSON)), nil
	}
	return serveFile(e.CSV(), csvType+"; charset=utf-8", e.FileName(textexport.FormatCSV)), nil
}
