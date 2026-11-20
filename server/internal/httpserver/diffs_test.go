package httpserver

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/diff"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
)

// diffPath is the path of the diff of the project's snapshots.
func diffPath(project string) string { return projectPath(project) + "/diff" }

// diffCorpus is the diff corpus in the repository.
var diffCorpus = filepath.Join("..", "..", "..", "schema", "diff-corpus")

// corpusSet returns the files of the diff corpus's set name, by their paths.
func corpusSet(t *testing.T, name string) map[string][]byte {
	t.Helper()
	root := filepath.Join(diffCorpus, "sets", name)
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// corpusDiff returns the expected diff of the diff corpus's case name, as a
// JSON value.
func corpusDiff(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(diffCorpus, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []struct {
			Name string         `json:"name"`
			Diff map[string]any `json:"diff"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Cases {
		if c.Name == name {
			return c.Diff
		}
	}
	t.Fatalf("the diff corpus has no case %s", name)
	return nil
}

// diffOf returns the diff that res answers with, as a JSON value, and as
// the type, or fails the test where res is not 200.
func diffOf(t *testing.T, what string, res response) (map[string]any, diff.Diff) {
	t.Helper()
	var v map[string]any
	var d diff.Diff
	if res.status != http.StatusOK || json.Unmarshal(res.body, &v) != nil || json.Unmarshal(res.body, &d) != nil {
		t.Fatalf("%s: %d %s", what, res.status, res.body)
	}
	return v, d
}

// diffBody returns the body of a diff from the snapshot from into to, and of
// at most limit changes, for a limit that is not 0.
func diffBody(t *testing.T, from, to string, limit int) []byte {
	t.Helper()
	body := map[string]any{"from": from, "to": to}
	if limit != 0 {
		body["limit"] = limit
	}
	return jsonBody(t, body)
}

// TestDiffSnapshots checks the diff of two of a project's ready snapshots
// through the API: of the oldest of three into the newest, which is the
// corpus's diff of their files, as the web app and the Godot addon read it,
// but for its sides, which name the snapshots that the request names; of the
// newest into the oldest, the reverse; and of one into itself, nothing; with
// a session and with an API token that holds snapshot:read alone (PRD
// FR-SYNC-02 AC1, FR-SNAP-04 AC4).
//
// Verifies: FR-SYNC-02/AC1, FR-SNAP-04/AC4
func TestDiffSnapshots(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Harbour").ID
	admin, wsID, pid := d.Admin(t), decode(t, ids.Workspace, ws), decode(t, ids.Project, p)
	oldest := addSnapshot(t, admin, wsID, pid, "1.0.0", "ready", ids.UUID{}, corpusSet(t, "base"))
	middle := addSnapshot(t, admin, wsID, pid, "1.1.0", "ready", ids.UUID{}, corpusSet(t, "renamed"))
	newest := addSnapshot(t, admin, wsID, pid, "1.2.0", "ready", ids.UUID{}, corpusSet(t, "fields"))

	want := corpusDiff(t, "fields")
	want["from"].(map[string]any)["id"], want["from"].(map[string]any)["version"] = oldest, "1.0.0"
	want["to"].(map[string]any)["id"], want["to"].(map[string]any)["version"] = newest, "1.2.0"
	releases := bearer(makeToken(t, h, owner, workspaceTokensPath(ws), "snapshot:read").Secret)
	for what, header := range map[string]http.Header{"a session": owner.header(true), "a snapshot:read token": releases} {
		got, _ := diffOf(t, "the diff of the oldest into the newest with "+what, call(h, http.MethodPost,
			diffPath(p), diffBody(t, oldest, newest, 0), header))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("the diff of the oldest into the newest with %s is %v, want the corpus's case fields, %v", what,
				got, want)
		}
	}

	_, back := diffOf(t, "the diff of the newest into the oldest", call(h, http.MethodPost, diffPath(p),
		diffBody(t, newest, oldest, 0), owner.header(true)))
	if back.From.ID != newest || back.To.ID != oldest || back.Summary.Modified != 11 ||
		back.Summary.Kinds.Variable.Modified != 3 {
		t.Errorf("the diff of the newest into the oldest is from %s into %s, of %+v, want the 11 changes back",
			back.From.ID, back.To.ID, back.Summary)
	}
	_, renamed := diffOf(t, "the diff of the oldest into the middle", call(h, http.MethodPost, diffPath(p),
		diffBody(t, oldest, middle, 0), owner.header(true)))
	if renamed.To.Version != "1.1.0" || len(renamed.Project.Variables) != 1 ||
		renamed.Project.Variables[0].Change != diff.Modified {
		t.Errorf("the diff of the oldest into the middle has the variables' changes %+v, want the rename",
			renamed.Project.Variables)
	}
	_, same := diffOf(t, "the diff of a snapshot into itself", call(h, http.MethodPost, diffPath(p),
		diffBody(t, middle, middle, 0), owner.header(true)))
	if same.Summary != (diff.DiffSummary{}) || len(same.Boards) != 0 || same.Truncated {
		t.Errorf("the diff of a snapshot into itself is %+v, want no change", same.Summary)
	}
}

// TestDiffDraft checks the diff of a release into the draft as it is now:
// the release, which its build stored, and the draft, which commands then
// changed, named as the draft's, with its content hash as its export's
// manifest has it, and the changes that the commands made alone; and that a
// token that holds snapshot:read alone is refused it with 403
// insufficient_scope, which one of project:read is not (PRD §5.6).
//
// Verifies: FR-SYNC-02/AC1
func TestDiffDraft(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Harbour").ID
	board, quay := ids.Encode(ids.Board, ids.New()), ids.Encode(ids.Node, ids.New())
	sendBatch(t, h, owner, p,
		wireCommand{Type: "board.create", Payload: map[string]any{"id": board, "name": "Harbour"}},
		wireCommand{Type: "node.create", Payload: map[string]any{"id": quay, "board_id": board, "type": "element",
			"kind": "narration", "title": "Quay", "text": paragraph("The quay at night."),
			"position": map[string]int{"x": 0, "y": 0}, "is_ending": true}})
	report := validationReport(t, h, owner, p)
	release := snapshotFrom(t, call(h, http.MethodPost, snapshotsPath(p), publishBody(t, "1.0.0", report),
		owner.header(true)), http.StatusAccepted)
	if err := s.snapshots.Build(context.Background(), decode(t, ids.Workspace, ws),
		decode(t, ids.Snapshot, release.ID)); err != nil {
		t.Fatal(err)
	}
	tide := ids.Encode(ids.Variable, ids.New())
	sendBatch(t, h, owner, p,
		wireCommand{Type: "variable.create", Payload: map[string]any{"id": tide, "key": "tide", "type": "int",
			"scope": "global"}},
		wireCommand{Type: "node.update", Payload: map[string]any{"id": quay, "title": "The quay"}})

	_, got := diffOf(t, "the diff of the release into the draft", call(h, http.MethodPost, diffPath(p),
		diffBody(t, release.ID, "draft", 0), owner.header(true)))
	files, _ := unzip(t, call(h, http.MethodGet, projectPath(p)+"/draft/export", nil, owner.header(false)).body)
	if got.From.Kind != diff.SideSnapshot || got.From.ID != release.ID || got.From.Version != "1.0.0" ||
		got.To.Kind != diff.SideDraft || got.To.ID != "" || got.To.Version != "" ||
		got.To.ContentHash != manifestSnapshot(t, files)["contentHash"] {
		t.Errorf("the diff is from %+v into %+v, want the release into the draft as its export names it", got.From,
			got.To)
	}
	if got.Summary.Added != 1 || got.Summary.Modified != 1 || got.Summary.Kinds.Variable.Added != 1 ||
		got.Summary.Kinds.Node.Modified != 1 || len(got.Boards) != 1 || len(got.Boards[0].Changes) != 1 ||
		got.Boards[0].Changes[0].ID != quay || got.Boards[0].Changes[0].Title != "The quay" ||
		len(got.Project.Variables) != 1 || got.Project.Variables[0].ID != tide {
		t.Errorf("the diff is %+v, of boards %+v and variables %+v, want the variable added and the title changed",
			got.Summary, got.Boards, got.Project.Variables)
	}

	releases := bearer(makeToken(t, h, owner, workspaceTokensPath(ws), "snapshot:read").Secret)
	wantCode(t, "the diff into the draft with a snapshot:read token", call(h, http.MethodPost, diffPath(p),
		diffBody(t, release.ID, "draft", 0), releases), http.StatusForbidden, api.CodeInsufficientScope)
	content := bearer(makeToken(t, h, owner, workspaceTokensPath(ws), "project:read").Secret)
	if res := call(h, http.MethodPost, diffPath(p), diffBody(t, release.ID, "draft", 0), content); res.status !=
		http.StatusOK {
		t.Errorf("the diff into the draft with a project:read token: %d %s", res.status, res.body)
	}
}

// TestDiffTruncated checks that a diff of more changes than its limit lists
// the first of them alone, and says that it is truncated and how many each
// board and the project left out, while its summary counts every change, as
// the diff of all of them does (PRD FR-SYNC-02 AC2).
//
// Verifies: FR-SYNC-02/AC2
func TestDiffTruncated(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Harbour").ID
	admin, wsID, pid := d.Admin(t), decode(t, ids.Workspace, ws), decode(t, ids.Project, p)
	from := addSnapshot(t, admin, wsID, pid, "1.0.0", "ready", ids.UUID{}, corpusSet(t, "base"))
	to := addSnapshot(t, admin, wsID, pid, "1.1.0", "ready", ids.UUID{}, corpusSet(t, "fields"))
	_, all := diffOf(t, "the whole diff", call(h, http.MethodPost, diffPath(p), diffBody(t, from, to, 0),
		owner.header(true)))
	if all.Truncated || all.Project.Omitted != 0 {
		t.Fatalf("the whole diff is truncated, %d left out", all.Project.Omitted)
	}
	_, first := diffOf(t, "the diff of one change", call(h, http.MethodPost, diffPath(p), diffBody(t, from, to, 1),
		owner.header(true)))
	listed := len(first.Project.Variables) + len(first.Project.ComponentTypes) + len(first.Project.Attributes) +
		len(first.Project.Components)
	omitted := first.Project.Omitted
	for _, b := range first.Boards {
		listed += len(b.Changes)
		omitted += b.Omitted
	}
	if !first.Truncated || listed != 1 || omitted != 8 || first.Summary != all.Summary ||
		len(first.Boards) != len(all.Boards) || !reflect.DeepEqual(first.Project.Fields, all.Project.Fields) ||
		!reflect.DeepEqual(first.Project.Variables, all.Project.Variables[:1]) {
		t.Errorf("the diff of one change lists %d, leaves %d out, truncated %v, of the summary %+v, want the first "+
			"of the 9 changes that entries list, and the whole summary %+v", listed, omitted, first.Truncated,
			first.Summary, all.Summary)
	}
	// The route's bound is package diff's, which Truncate holds a limit to.
	body, _ := reflect.TypeFor[diffInput]().FieldByName("Body")
	if field, ok := body.Type.FieldByName("Limit"); !ok || field.Tag.Get("maximum") != strconv.Itoa(diff.MaxLimit) {
		t.Errorf("the route's limit is not bounded by diff.MaxLimit, %d", diff.MaxLimit)
	}
	for _, limit := range []int{0, -1, diff.MaxLimit + 1} {
		body := jsonBody(t, map[string]any{"from": from, "to": to, "limit": limit})
		wantCode(t, "a diff of the limit "+strconv.Itoa(limit), call(h, http.MethodPost, diffPath(p), body,
			owner.header(true)), http.StatusUnprocessableEntity, api.CodeValidationFailed)
	}
}

// TestDiffRefusals checks the refusals of a diff: of a snapshot that the
// project does not have, 404 not_found; of one that is building or failed,
// which has no files, 409 snapshot_not_ready, with its status; and of an ID
// that is not a snapshot's, 400 invalid_id.
func TestDiffRefusals(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Harbour").ID
	admin, wsID, pid := d.Admin(t), decode(t, ids.Workspace, ws), decode(t, ids.Project, p)
	ready := addSnapshot(t, admin, wsID, pid, "1.0.0", "ready", ids.UUID{}, corpusSet(t, "base"))
	other := createProject(t, h, owner, ws, "Lighthouse").ID
	elsewhere := addSnapshot(t, admin, wsID, decode(t, ids.Project, other), "1.0.0", "ready", ids.UUID{},
		corpusSet(t, "base"))
	missing, none := ids.Encode(ids.Snapshot, ids.New()), ids.Encode(ids.Snapshot, ids.UUID{})
	for what, body := range map[string][]byte{
		"from a snapshot that no project has":   diffBody(t, missing, ready, 0),
		"into a snapshot that no project has":   diffBody(t, ready, missing, 0),
		"from another project's snapshot":       diffBody(t, elsewhere, ready, 0),
		"into the draft from a missing release": diffBody(t, missing, "draft", 0),
		// The zero UUID is no snapshot's, and not the draft's.
		"from the zero UUID": diffBody(t, none, ready, 0),
		"into the zero UUID": diffBody(t, ready, none, 0),
	} {
		wantCode(t, "a diff "+what, call(h, http.MethodPost, diffPath(p), body, owner.header(true)),
			http.StatusNotFound, api.CodeNotFound)
	}
	for version, status := range map[string]snapshot.Status{"2.0.0": snapshot.Building, "2.1.0": snapshot.Failed} {
		id := addSnapshot(t, admin, wsID, pid, version, string(status), ids.UUID{}, nil)
		for what, body := range map[string][]byte{"from": diffBody(t, id, ready, 0), "into": diffBody(t, ready, id, 0),
			"from it into the draft": diffBody(t, id, "draft", 0)} {
			res := call(h, http.MethodPost, diffPath(p), body, owner.header(true))
			wantCode(t, "a diff "+what+" a "+string(status)+" snapshot", res, http.StatusConflict,
				api.CodeSnapshotNotReady)
			if got := res.details(t)["status"]; got != string(status) {
				t.Errorf("the refusal of a diff %s a %s snapshot names the status %v", what, status, got)
			}
		}
	}
	for what, body := range map[string][]byte{
		"from a node's ID":  diffBody(t, ids.Encode(ids.Node, ids.New()), ready, 0),
		"into a node's ID":  diffBody(t, ready, ids.Encode(ids.Node, ids.New()), 0),
		"from the draft":    diffBody(t, "draft", ready, 0),
		"into no ID at all": diffBody(t, ready, "snp_", 0),
	} {
		wantCode(t, "a diff "+what, call(h, http.MethodPost, diffPath(p), body, owner.header(true)),
			http.StatusBadRequest, api.CodeInvalidID)
	}
}
