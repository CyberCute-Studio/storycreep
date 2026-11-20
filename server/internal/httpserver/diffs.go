package httpserver

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/diff"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
)

// draftTarget is the to of a diff into the project's draft.
const draftTarget = "draft"

type diffInput struct {
	SessionCSRFInput
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Body      struct {
		From  string `json:"from" doc:"The ID of the snapshot that the diff is from, a ready one of the project's, as the release that a game has pinned, whether or not it is the newest (PRD FR-SYNC-02 AC1)"`
		To    string `json:"to" doc:"The ID of the snapshot that the diff is into, a ready one of the project's, or draft, as written, in lower case, for the project's draft as it is now, which an API token needs project:read for"`
		Limit int    `json:"limit,omitempty" minimum:"1" maximum:"5000" doc:"The most changes that the answer lists, from 1 to 5,000. Without it, 1,000."`
	}
}

type diffOutput struct {
	Body *diff.Diff
}

// registerDiffs registers the route that compares two of a project's
// snapshots, or a snapshot and the draft, entity by entity (PRD FR-SNAP-04,
// FR-SYNC-02, §7.4). It is a POST, as the two IDs and the limit are a body,
// but reads, and changes nothing, as the clipboard's copy does; so a request
// of it made with the session cookie needs the session's CSRF token, as every
// request that is not a GET, a HEAD, or an OPTIONS does (sessions.go), which
// its input declares (SessionCSRFInput).
func registerDiffs(m *api.Mux, snapshots *snapshot.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "diffSnapshots",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects/{project_id}/diff",
		Summary:     "Diff two snapshots",
		Description: "Returns the entity-level diff of the project's snapshot from into its snapshot to, or into its " +
			"draft as it is now, read as of one moment, for to draft, as the draft's export writes it, with what it " +
			"stands in for of what no release may hold (PRD §7.8), as a draft pull brings it in (PRD FR-SNAP-04, " +
			"FR-SYNC-02, §7.4): the " +
			"variables, component types, attribute definitions, components, boards, nodes, branch outputs, and " +
			"connections that only to has, added, or only from has, removed; those that both have whose fields " +
			"differ, modified, each changed field with its value on either side, a rich text's as its plain text " +
			"with a word-level diff, and an expression's as its source with whether what it names changed, a node " +
			"whose position alone changed being cosmetic; and the nodes and connections that the two have on " +
			"different boards, moved, with both boards. The changes of what boards hold are grouped by board, and " +
			"the summary counts every change of each kind. Both sides are compared at the version of the " +
			"published game format that the server writes, as its schemaVersion says, and the Godot addon, which " +
			"computes the same diff of the files that it holds, follows schema/diff-corpus. Either snapshot may be " +
			"any of the project's that is ready, the newest or an older one, in either order (PRD FR-SYNC-02 AC1); " +
			"one that is building or failed, which has no files, is answered with 409 snapshot_not_ready, whose " +
			"details.status says which, of the first of from and to that is not ready. The answer is sized for " +
			"display: it lists at most limit changes, 1,000 unless the request asks for up to 5,000, and at most " +
			"8 MiB of them, in its order, the project's variables, component types, attribute definitions, and " +
			"components first and then each board's changes, and leaves out every change after the first that does " +
			"not fit, which truncated, the project's omitted, and each board's omitted count, while the summary " +
			"still counts every change (PRD FR-SYNC-02 AC2). The draft is not a release, so an API token needs " +
			"project:read for a diff into it, and one that holds snapshot:read alone is answered with 403 " +
			"insufficient_scope. A snapshot stored at a newer version of the format than the server writes, as " +
			"only a server that reads both versions holds, whose content the server's version cannot express, is " +
			"answered with 409 schema_unrepresentable, whose details.schema is the server's version and " +
			"details.constructs lists each value that it cannot express (PRD §6.4). A from, or a to other than " +
			"draft, that is not a snapshot's ID, as one that does not decode, the ID of another kind of entity, or " +
			"draft in another case, is answered with 400 invalid_id." + projectNotFoundDoc +
			tokenNotFoundDoc + snapshotNotFoundDoc + tokenPostReadDoc(scope.SnapshotRead),
		Tags: []string{"Snapshots"},
	}, func(ctx context.Context, in *diffInput) (*diffOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		from, err := ids.Decode(ids.Snapshot, in.Body.From)
		if err != nil {
			return nil, err
		}
		limit := in.Body.Limit
		if limit == 0 {
			limit = diff.DefaultLimit
		}
		var d *diff.Diff
		if in.Body.To == draftTarget {
			d, err = snapshots.DiffDraft(ctx, actor, id, from, limit)
		} else {
			var to ids.UUID
			if to, err = ids.Decode(ids.Snapshot, in.Body.To); err != nil {
				return nil, err
			}
			d, err = snapshots.Diff(ctx, actor, id, from, to, limit)
		}
		if err != nil {
			return nil, snapshotError(ctx, err)
		}
		return &diffOutput{Body: d}, nil
	}, api.TokenScope(scope.SnapshotRead))
}
