package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
	"github.com/CyberCute-Studio/storycreep/server/internal/textexport"
)

// textExportOf returns the JSON text export at path, as si.
func textExportOf(t *testing.T, h http.Handler, si signedIn, path string) textexport.Export {
	t.Helper()
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	res := call(h, http.MethodGet, path+separator+"format=json", nil, si.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("the JSON export at %s: %d %s", path, res.status, res.body)
	}
	var e textexport.Export
	if err := json.Unmarshal(res.body, &e); err != nil {
		t.Fatalf("the JSON export at %s does not decode: %v", path, err)
	}
	return e
}

// TestTextExportMatchesStatistics checks the draft's export of a project
// whose texts hold mentions, value references, a list of what the trash
// holds, a label of a branch's connection, and an ending's: that its words add
// up to the writing statistics, of the project and of each board, what the
// trash holds being in neither, and a reference of a variable in the trash
// counting by its key in both; that two exports are the same bytes, and again
// after an edit that is undone by another; and what the CSV's cells hold.
//
// Verifies: FR-TEXT-05/AC2, FR-TEXT-05/AC3
func TestTextExportMatchesStatistics(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Port").ID
	harbour, attic := ids.Encode(ids.Board, ids.New()), ids.Encode(ids.Board, ids.New())
	greet, reply, end, gate, gone, dusty := newNodeID(), newNodeID(), newNodeID(), newNodeID(), newNodeID(),
		newNodeID()
	conn := func() string { return ids.Encode(ids.Connection, ids.New()) }
	lantern, spent := ids.Encode(ids.Variable, ids.New()), ids.Encode(ids.Variable, ids.New())
	prop, trust := ids.Encode(ids.ComponentType, ids.New()), ids.Encode(ids.AttributeDef, ids.New())
	elara := ids.Encode(ids.Component, ids.New())
	ifOut, elseOut := ids.Encode(ids.BranchOutput, ids.New()), ids.Encode(ids.BranchOutput, ids.New())
	pos := map[string]int{"x": 0, "y": 0}
	cmd := func(typ string, p map[string]any) wireCommand { return wireCommand{Type: typ, Payload: p} }
	element := func(id, board string, text json.RawMessage) wireCommand {
		return cmd("node.create", map[string]any{"id": id, "board_id": board, "type": "element", "position": pos,
			"title": "Title of " + id[:6], "text": text})
	}
	list := json.RawMessage(`{"type":"doc","content":[{"type":"bulletList","content":[{"type":"listItem","content":[` +
		`{"type":"paragraph","content":[{"type":"text","text":"Rope, oil"}]}]}]}]}`)
	sendBatch(t, h, owner, p,
		cmd("board.create", map[string]any{"id": harbour, "name": "Harbour"}),
		cmd("board.create", map[string]any{"id": attic, "name": "Attic"}),
		cmd("variable.create", map[string]any{"id": lantern, "key": "lantern_lit", "type": "bool", "scope": "global"}),
		cmd("variable.create", map[string]any{"id": spent, "key": "spent_oil", "type": "bool", "scope": "global"}),
		cmd("component_type.create", map[string]any{"id": prop, "name": "Prop"}),
		cmd("attribute.create", map[string]any{"id": trust, "component_type_id": prop, "name": "Trust", "type": "int",
			"runtime": true}),
		cmd("component.create", map[string]any{"id": elara, "component_type_id": prop, "name": "Elara Vane"}),
		element(greet, harbour, richText(textInline("Hello, "), mentionInline(elara), textInline(". Trust: "),
			attributeInline(elara, trust))),
		element(reply, harbour, list),
		cmd("node.create", map[string]any{"id": end, "board_id": harbour, "type": "element", "position": pos,
			"text": richText(textInline("Oil: "), valueInline(spent)), "is_ending": true}),
		cmd("node.create", map[string]any{"id": gate, "board_id": harbour, "type": "branch", "position": pos}),
		cmd("branch_output.create", map[string]any{"id": ifOut, "node_id": gate, "condition": "lantern_lit"}),
		cmd("branch_output.create", map[string]any{"id": elseOut, "node_id": gate, "else": true}),
		element(gone, harbour, paragraph("Lost words here")),
		element(dusty, attic, paragraph("=SUM(A1) dusty attic words")),
		cmd("connection.create", map[string]any{"id": conn(), "from_node_id": greet, "to_node_id": reply,
			"label": richText(textInline("Ask about "), valueInline(lantern))}),
		cmd("connection.create", map[string]any{"id": conn(), "from_node_id": greet, "to_node_id": gate,
			"label": paragraph("Leave, now"), "condition": "lantern_lit"}),
		cmd("connection.create", map[string]any{"id": conn(), "from_node_id": end, "to_node_id": reply,
			"label": paragraph("Again")}),
		cmd("connection.create", map[string]any{"id": conn(), "from_node_id": gate, "from_port": ifOut,
			"to_node_id": reply, "label": paragraph("Lit path")}),
		cmd("connection.create", map[string]any{"id": conn(), "from_node_id": gate, "from_port": elseOut,
			"to_node_id": end}),
	)
	// What the trash holds is in neither count, and a text that refers to a
	// variable in the trash still counts it, by its key.
	sendBatch(t, h, owner, p, cmd("node.delete", map[string]any{"id": gone}),
		cmd("variable.delete", map[string]any{"id": spent, "force": true}))

	path := projectPath(p) + "/draft/text-export"
	stats := projectStatistics(t, h, owner, p)
	if !stats.Complete || stats.Words == 0 {
		t.Fatalf("the statistics are %+v, want them complete", stats)
	}
	whole := textExportOf(t, h, owner, path)
	sum := 0
	for _, r := range whole.Rows {
		sum += r.WordCount
		if r.NodeID == gone {
			t.Errorf("the export holds a row of a node in the trash: %+v", r)
		}
	}
	if int64(sum) != stats.Words || int64(whole.Words) != stats.Words {
		t.Errorf("the rows add up to %d words, the export's total is %d, and the statistics say %d", sum, whole.Words,
			stats.Words)
	}
	for _, b := range stats.Boards {
		e := textExportOf(t, h, owner, path+"?board="+b.ID)
		if int64(e.Words) != b.Words || e.Board == nil || e.Board.ID != b.ID {
			t.Errorf("the board %s exports %d words, narrowed to %+v, and the statistics say %d", b.Name, e.Words,
				e.Board, b.Words)
		}
	}
	if !slices.ContainsFunc(whole.Rows, func(r textexport.Row) bool {
		return r.NodeID == end && r.Text == "Oil: {spent_oil}"
	}) {
		t.Errorf("no row holds the reference of the variable in the trash by its key: %+v", whole.Rows)
	}
	if !slices.ContainsFunc(whole.Rows, func(r textexport.Row) bool {
		return r.NodeID == greet && r.TextType == textexport.TypeChoice && r.Text == "Leave, now" &&
			r.Condition == "lantern_lit"
	}) {
		t.Errorf("no row holds the choice with its condition: %+v", whole.Rows)
	}

	csv := call(h, http.MethodGet, path, nil, owner.header(false))
	if csv.status != http.StatusOK || csv.header.Get("Content-Type") != "text/csv; charset=utf-8" ||
		csv.header.Get("Content-Disposition") != "attachment; filename=port-draft-text.csv" ||
		csv.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("the CSV: %d %v", csv.status, csv.header)
	}
	if !bytes.HasPrefix(csv.body, []byte{0xEF, 0xBB, 0xBF}) ||
		!bytes.Contains(csv.body, []byte(",'=SUM(A1) dusty attic words,")) ||
		!bytes.Contains(csv.body, []byte(`"Leave, now"`)) {
		t.Errorf("the CSV holds %s", csv.body)
	}
	if head := call(h, http.MethodHead, path, nil, owner.header(false)); head.status != http.StatusOK ||
		len(head.body) != 0 || head.header.Get("Content-Length") != csv.header.Get("Content-Length") {
		t.Errorf("a HEAD of the export: %d, %d bytes, Content-Length %q", head.status, len(head.body),
			head.header.Get("Content-Length"))
	}

	wholeJSON := call(h, http.MethodGet, path+"?format=json", nil, owner.header(false))
	if wholeJSON.header.Get("Content-Type") != "application/json" ||
		wholeJSON.header.Get("Content-Disposition") != "attachment; filename=port-draft-text.json" {
		t.Errorf("the JSON's headers are %v", wholeJSON.header)
	}
	// An edit and its reverse leave the project as it was, whose export is
	// the same bytes, though its operation sequence has moved.
	sendBatch(t, h, owner, p, cmd("node.update", map[string]any{"id": reply, "title": "Changed"}))
	if changed := call(h, http.MethodGet, path, nil, owner.header(false)); bytes.Equal(changed.body, csv.body) {
		t.Error("an edit of a title left the export as it was")
	}
	sendBatch(t, h, owner, p, cmd("node.update", map[string]any{"id": reply, "title": "Title of " + reply[:6]}))
	if again := call(h, http.MethodGet, path, nil, owner.header(false)); !bytes.Equal(again.body, csv.body) {
		t.Errorf("the CSV of the unchanged project differs:\n%s\nwant\n%s", again.body, csv.body)
	}
	if again := call(h, http.MethodGet, path+"?format=json", nil, owner.header(false)); !bytes.Equal(again.body,
		wholeJSON.body) {
		t.Errorf("the JSON of the unchanged project differs:\n%s\nwant\n%s", again.body, wholeJSON.body)
	}

	wantCode(t, "a board that is in the trash or is none", call(h, http.MethodGet,
		path+"?board="+ids.Encode(ids.Board, ids.New()), nil, owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	wantCode(t, "a board that is no board's ID", call(h, http.MethodGet, path+"?board="+greet, nil,
		owner.header(false)), http.StatusBadRequest, api.CodeInvalidID)
	wantCode(t, "a format that is none", call(h, http.MethodGet, path+"?format=xlsx", nil, owner.header(false)),
		http.StatusUnprocessableEntity, api.CodeValidationFailed)
}

// TestTextExportOfRelease checks the export of a release that the API
// published: the rows of the draft that it was published from, the same bytes
// each time, named by its version, its board's alone with board, and still
// the release's after the draft changes; a board that it lacks, and a
// snapshot that is building or failed, refused; and that the export of a
// release needs project:read, which snapshot:read does not imply.
//
// Verifies: FR-TEXT-05/AC2, FR-TEXT-05/AC3
func TestTextExportOfRelease(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Harbour").ID
	board, start, end := ids.Encode(ids.Board, ids.New()), newNodeID(), newNodeID()
	pos := map[string]int{"x": 0, "y": 0}
	cmd := func(typ string, p map[string]any) wireCommand { return wireCommand{Type: typ, Payload: p} }
	sendBatch(t, h, owner, p,
		cmd("board.create", map[string]any{"id": board, "name": "Quay"}),
		cmd("node.create", map[string]any{"id": start, "board_id": board, "type": "element", "kind": "narration",
			"title": "Start", "text": paragraph("The quay, at night."), "position": pos}),
		cmd("node.create", map[string]any{"id": end, "board_id": board, "type": "element", "kind": "narration",
			"title": "Home", "text": paragraph("Home at last."), "position": pos, "is_ending": true}),
		cmd("connection.create", map[string]any{"id": ids.Encode(ids.Connection, ids.New()), "from_node_id": start,
			"to_node_id": end, "label": paragraph("Go home")}),
		cmd("board.set_entry_node", map[string]any{"id": board, "node_id": start}),
	)
	draftPath := projectPath(p) + "/draft/text-export"
	draft := textExportOf(t, h, owner, draftPath)

	r := validationReport(t, h, owner, p)
	res := call(h, http.MethodPost, snapshotsPath(p), publishBody(t, "1.0.0", r), owner.header(true))
	release := snapshotFrom(t, res, http.StatusAccepted)
	path := snapshotsPath(p) + "/" + release.ID + "/text-export"
	wantCode(t, "the text export of a snapshot that is building", call(h, http.MethodGet, path, nil,
		owner.header(false)), http.StatusConflict, api.CodeSnapshotNotReady)
	startWorkers(t, s, d)
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		got := snapshotFrom(t, call(h, http.MethodGet, snapshotsPath(p)+"/"+release.ID, nil, owner.header(false)),
			http.StatusOK)
		if got.Status == snapshot.Ready {
			break
		}
		if got.Status == snapshot.Failed || time.Now().After(deadline) {
			t.Fatalf("the snapshot's build ended as %+v", got)
		}
	}

	e := textExportOf(t, h, owner, path)
	if !slices.Equal(e.Rows, draft.Rows) || e.Words != draft.Words {
		t.Errorf("the release's rows are %+v, want the draft's %+v", e.Rows, draft.Rows)
	}
	if e.Source != (textexport.Source{Kind: textexport.SourceSnapshot, ID: release.ID, Version: "1.0.0"}) {
		t.Errorf("the export's source is %+v", e.Source)
	}
	csv := call(h, http.MethodGet, path, nil, owner.header(false))
	if csv.status != http.StatusOK ||
		csv.header.Get("Content-Disposition") != "attachment; filename=harbour-1.0.0-text.csv" {
		t.Fatalf("the release's CSV: %d %v", csv.status, csv.header)
	}
	if again := call(h, http.MethodGet, path, nil, owner.header(false)); !bytes.Equal(again.body, csv.body) {
		t.Error("two text exports of the release differ")
	}
	narrowed := call(h, http.MethodGet, path+"?board="+board, nil, owner.header(false))
	if narrowed.status != http.StatusOK || !bytes.Equal(narrowed.body, csv.body) ||
		!strings.Contains(narrowed.header.Get("Content-Disposition"), "harbour-1.0.0-quay-text.csv") {
		t.Errorf("the release's only board: %d %v", narrowed.status, narrowed.header)
	}
	wantCode(t, "a board that the release lacks", call(h, http.MethodGet,
		path+"?board="+ids.Encode(ids.Board, ids.New()), nil, owner.header(false)), http.StatusNotFound, api.CodeNotFound)

	sendBatch(t, h, owner, p, cmd("node.update", map[string]any{"id": start, "text": paragraph("Rewritten.")}))
	if after := call(h, http.MethodGet, path, nil, owner.header(false)); !bytes.Equal(after.body, csv.body) {
		t.Error("an edit of the draft changed the release's export")
	}

	failed := addSnapshot(t, d.Admin(t), decode(t, ids.Workspace, ws), decode(t, ids.Project, p), "2.0.0", "failed",
		ids.UUID{}, nil)
	wantCode(t, "the text export of a failed snapshot", call(h, http.MethodGet,
		snapshotsPath(p)+"/"+failed+"/text-export", nil, owner.header(false)), http.StatusConflict,
		api.CodeSnapshotNotReady)

	releases := bearer(makeToken(t, h, owner, workspaceTokensPath(ws), "snapshot:read").Secret)
	content := bearer(makeToken(t, h, owner, workspaceTokensPath(ws), "project:read").Secret)
	for _, route := range []string{path, draftPath} {
		wantCode(t, route+" with a snapshot:read token", call(h, http.MethodGet, route, nil, releases),
			http.StatusForbidden, api.CodeInsufficientScope)
		if res := call(h, http.MethodGet, route, nil, content); res.status != http.StatusOK {
			t.Errorf("%s with a project:read token: %d %s", route, res.status, res.body)
		}
	}
}
