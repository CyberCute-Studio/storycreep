package httpserver

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
)

// snapshotPage is a page of the list of a project's snapshots.
type snapshotPage struct {
	Items      []snapshot.Snapshot `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}

// listSnapshots returns the page of the project's snapshots that query asks
// for, as si, or fails the test.
func listSnapshots(t *testing.T, h http.Handler, si signedIn, project, query string) snapshotPage {
	t.Helper()
	res := call(h, http.MethodGet, snapshotsPath(project)+query, nil, si.header(false))
	var page snapshotPage
	if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
		t.Fatalf("the list of snapshots%s: %d %s", query, res.status, res.body)
	}
	return page
}

// snapshotIDs returns the IDs of the page's snapshots, in its order.
func snapshotIDs(page snapshotPage) []string {
	out := make([]string, len(page.Items))
	for i, s := range page.Items {
		out[i] = s.ID
	}
	return out
}

// unzip returns the files of the zip data, by their paths, and their paths in
// the zip's order, and fails the test unless every member is a file of the
// published layout's, compressed with Deflate, with the zip's fixed
// modification time, as an export writes it.
func unzip(t *testing.T, data []byte) (map[string][]byte, []string) {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("the export is no zip: %v", err)
	}
	files, order := map[string][]byte{}, []string{}
	for _, f := range r.File {
		if f.FileInfo().IsDir() || f.Method != zip.Deflate ||
			!f.Modified.Equal(time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("the member %s is a directory, not deflated, or of %v", f.Name, f.Modified)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = b
		order = append(order, f.Name)
	}
	return files, order
}

// manifestSnapshot returns the snapshot block of the manifest of files.
func manifestSnapshot(t *testing.T, files map[string][]byte) map[string]any {
	t.Helper()
	var m struct {
		SchemaVersion int            `json:"schemaVersion"`
		Snapshot      map[string]any `json:"snapshot"`
	}
	if err := json.Unmarshal(files[published.ManifestPath], &m); err != nil || m.Snapshot == nil {
		t.Fatalf("the export's manifest: %v", err)
	}
	return m.Snapshot
}

// TestListSnapshots checks the list of a project's snapshots (PRD
// FR-SNAP-02): every status, newest first, by their IDs, each with its
// metadata and its publisher's display name, which a snapshot whose
// publisher's account is deleted has none of; only those of a status with
// status, so that the newest ready one is the newest release (§7.5); its
// pages, whose cursors hold their places; and none of another project's.
func TestListSnapshots(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	admin := d.Admin(t)
	ctx := context.Background()
	p := createProject(t, h, owner, ws, "Harbour").ID
	other := createProject(t, h, owner, ws, "Lighthouse").ID
	wsID, pid, ownerID := decode(t, ids.Workspace, ws), decode(t, ids.Project, p), decode(t, ids.User, owner.body.UserID)
	gone := ids.New()
	if _, err := admin.Exec(ctx, `INSERT INTO "user" (id, email, display_name) VALUES ($1, 'elara@example.com',
		'Elara')`, gone); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := admin.QueryRow(ctx, `SELECT display_name FROM "user" WHERE id = $1`, ownerID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	first := addSnapshot(t, admin, wsID, pid, "1.0.0", "ready", ownerID, fixtureFiles(t))
	failed := addSnapshot(t, admin, wsID, pid, "1.1.0", "failed", gone, nil)
	building := addSnapshot(t, admin, wsID, pid, "1.1.0", "building", ownerID, nil)
	addSnapshot(t, admin, wsID, decode(t, ids.Project, other), "1.0.0", "ready", ownerID, fixtureFiles(t))
	// Elara's account is deleted, whose cascade forgets that she published
	// the failed snapshot.
	if _, err := admin.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, gone); err != nil {
		t.Fatal(err)
	}

	page := listSnapshots(t, h, owner, p, "")
	if got, want := snapshotIDs(page), []string{building, failed, first}; !slices.Equal(got, want) ||
		page.NextCursor != nil {
		t.Fatalf("the list is %v, next %v, want %v, newest first, on one page", got, page.NextCursor, want)
	}
	b, f, r := page.Items[0], page.Items[1], page.Items[2]
	if b.Status != snapshot.Building || b.Version != "1.1.0" || b.PublishedBy != owner.body.UserID ||
		b.PublishedByName != name || b.BuiltAt != nil || b.Counts != nil {
		t.Errorf("the building snapshot is %+v", b)
	}
	if f.Status != snapshot.Failed || f.FailureCode != snapshot.FailureBuild || f.PublishedBy != "" ||
		f.PublishedByName != "" {
		t.Errorf("the failed snapshot is %+v, want no publisher, whose account is deleted", f)
	}
	if r.Status != snapshot.Ready || r.Version != "1.0.0" || r.Note != "Harbour intro" || r.ContentHash == "" ||
		r.SchemaVersion == nil || *r.SchemaVersion != 1 || r.Counts == nil || r.Counts.Nodes != 7 ||
		r.PublishedByName != name || r.ProjectID != p {
		t.Errorf("the ready snapshot is %+v", r)
	}
	got := snapshotFrom(t, call(h, http.MethodGet, snapshotsPath(p)+"/"+first, nil, owner.header(false)),
		http.StatusOK)
	if got.PublishedByName != name || got.ID != first {
		t.Errorf("the ready snapshot's metadata is %+v, want its publisher's name %q", got, name)
	}

	for status, want := range map[string][]string{"ready": {first}, "failed": {failed}, "building": {building}} {
		if got := snapshotIDs(listSnapshots(t, h, owner, p, "?status="+status)); !slices.Equal(got, want) {
			t.Errorf("the %s snapshots are %v, want %v", status, got, want)
		}
	}
	if got := snapshotIDs(listSnapshots(t, h, owner, p, "?status=ready&limit=1")); !slices.Equal(got,
		[]string{first}) {
		t.Errorf("the newest release is %v, want %s", got, first)
	}
	wantCode(t, "a status that is none", call(h, http.MethodGet, snapshotsPath(p)+"?status=published", nil,
		owner.header(false)), http.StatusUnprocessableEntity, api.CodeValidationFailed)

	page = listSnapshots(t, h, owner, p, "?limit=2")
	if got := snapshotIDs(page); !slices.Equal(got, []string{building, failed}) || page.NextCursor == nil {
		t.Fatalf("the first page of 2 is %v, next %v", got, page.NextCursor)
	}
	// A snapshot published after the first page was read is newer, and the
	// next page holds its place.
	addSnapshot(t, admin, wsID, pid, "1.2.0", "building", ownerID, nil)
	page = listSnapshots(t, h, owner, p, "?limit=2&cursor="+*page.NextCursor)
	if got := snapshotIDs(page); !slices.Equal(got, []string{first}) || page.NextCursor != nil {
		t.Errorf("the second page is %v, next %v, want %s alone", got, page.NextCursor, first)
	}
	wantCode(t, "a cursor that is none", call(h, http.MethodGet, snapshotsPath(p)+"?cursor=xyz", nil,
		owner.header(false)), http.StatusBadRequest, api.CodeInvalidCursor)
}

// TestExportSnapshot checks the export of a ready snapshot: without schema,
// a zip of its files as its build stored them, byte for byte, at its own
// version (PRD FR-SYNC-01 AC1), member by member in the order of their paths,
// named after its project and its version, the same zip each time, and for
// a HEAD request its length alone; with schema, at that version, which is
// its own, and a version that the server does not read refused with 409
// schema_unsupported, which names the range that it reads (AC3); and a
// snapshot that is building or failed, which has no files, refused with 409
// snapshot_not_ready.
//
// Verifies: FR-SYNC-01/AC1, FR-SYNC-01/AC3
func TestExportSnapshot(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	admin := d.Admin(t)
	p := createProject(t, h, owner, ws, "Harbour").ID
	wsID, pid := decode(t, ids.Workspace, ws), decode(t, ids.Project, p)
	stored := fixtureFiles(t)
	ready := addSnapshot(t, admin, wsID, pid, "1.0.0", "ready", ids.UUID{}, stored)
	path := snapshotsPath(p) + "/" + ready + "/export"

	res := call(h, http.MethodGet, path, nil, owner.header(false))
	if res.status != http.StatusOK || res.header.Get("Content-Type") != "application/zip" ||
		res.header.Get("Content-Disposition") != `attachment; filename=harbour-tales-1.0.0.zip` ||
		res.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("the export: %d %v", res.status, res.header)
	}
	files, order := unzip(t, res.body)
	if !maps.EqualFunc(files, stored, bytes.Equal) {
		t.Errorf("the export holds %v, want the stored files, byte for byte", slices.Sorted(maps.Keys(files)))
	}
	if !slices.IsSorted(order) {
		t.Errorf("the zip's members are in the order %v, want that of their paths", order)
	}
	if v, err := schemaversion.VersionOf(files); err != nil || v != 1 {
		t.Errorf("the export is of version %d (%v), want the snapshot's own, 1", v, err)
	}
	if again := call(h, http.MethodGet, path, nil, owner.header(false)); !bytes.Equal(again.body, res.body) {
		t.Error("two exports of the snapshot differ")
	}
	if own := call(h, http.MethodGet, path+"?schema=1", nil, owner.header(false)); own.status != http.StatusOK ||
		!bytes.Equal(own.body, res.body) {
		t.Errorf("the export at its own version: %d, want the same zip", own.status)
	}
	if head := call(h, http.MethodHead, path, nil, owner.header(false)); head.status != http.StatusOK ||
		len(head.body) != 0 || head.header.Get("Content-Length") != res.header.Get("Content-Length") {
		t.Errorf("a HEAD of the export: %d, %d bytes, Content-Length %q", head.status, len(head.body),
			head.header.Get("Content-Length"))
	}

	for _, schema := range []string{"2", "1000"} {
		res := call(h, http.MethodGet, path+"?schema="+schema, nil, owner.header(false))
		wantCode(t, "the export at version "+schema, res, http.StatusConflict, api.CodeSchemaUnsupported)
		if d := res.details(t); d["min"] != float64(1) || d["max"] != float64(1) {
			t.Errorf("the refusal of version %s names %v, want the range 1 to 1", schema, d)
		}
	}
	wantCode(t, "the export at version 0", call(h, http.MethodGet, path+"?schema=0", nil, owner.header(false)),
		http.StatusUnprocessableEntity, api.CodeValidationFailed)

	for version, status := range map[string]string{"2.0.0": "building", "2.1.0": "failed"} {
		id := addSnapshot(t, admin, wsID, pid, version, status, ids.UUID{}, nil)
		res := call(h, http.MethodGet, snapshotsPath(p)+"/"+id+"/export", nil, owner.header(false))
		wantCode(t, "the export of a "+status+" snapshot", res, http.StatusConflict, api.CodeSnapshotNotReady)
		if got := res.details(t)["status"]; got != status {
			t.Errorf("the refusal of a %s snapshot names the status %v", status, got)
		}
	}
	wantCode(t, "the export of a snapshot that the project does not have", call(h, http.MethodGet,
		snapshotsPath(p)+"/"+ids.Encode(ids.Snapshot, ids.New())+"/export", nil, owner.header(false)),
		http.StatusNotFound, api.CodeNotFound)
}

// TestExportDraft checks the export of the draft: a zip of the files that
// the serializer writes of the draft as it is, which validate against the
// version that the server writes, always marked as the draft's (PRD §7.8),
// with the channel draft, its content hash, and when it was exported, and
// neither an ID nor a version, named as the draft's, and exported whatever
// it holds, as a jumper without a target, which blocks publishing; and that
// a token that holds snapshot:read alone lists and exports a ready snapshot,
// and is refused the draft's export with 403 insufficient_scope, which a
// token of project:read is not (FR-SYNC-01 AC4).
//
// Verifies: FR-SYNC-01/AC4
func TestExportDraft(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Harbour").ID
	draftPath := projectPath(p) + "/draft/export"
	board, start, jumper := ids.Encode(ids.Board, ids.New()), ids.Encode(ids.Node, ids.New()),
		ids.Encode(ids.Node, ids.New())
	pos := map[string]int{"x": 0, "y": 0}
	sendBatch(t, h, owner, p,
		wireCommand{Type: "board.create", Payload: map[string]any{"id": board, "name": "Harbour"}},
		wireCommand{Type: "node.create", Payload: map[string]any{"id": start, "board_id": board, "type": "element",
			"kind": "narration", "title": "Quay", "text": paragraph("The quay at night."), "position": pos,
			"is_ending": true}},
		wireCommand{Type: "node.create", Payload: map[string]any{"id": jumper, "board_id": board, "type": "jumper",
			"position": pos}})
	if r := validationReport(t, h, owner, p); r.Counts.Errors == 0 {
		t.Fatalf("the draft's report is %+v, want the jumper's error", r.Counts)
	}

	res := call(h, http.MethodGet, draftPath, nil, owner.header(false))
	if res.status != http.StatusOK || res.header.Get("Content-Type") != "application/zip" ||
		res.header.Get("Content-Disposition") != `attachment; filename=harbour-draft.zip` {
		t.Fatalf("the draft's export: %d %v %s", res.status, res.header, res.body)
	}
	files, _ := unzip(t, res.body)
	if err := schemaversion.Validate(files); err != nil {
		t.Errorf("the draft's export does not validate: %v", err)
	}
	if _, ok := files["boards/harbour.json"]; !ok {
		t.Errorf("the draft's export holds %v, want the board's file", slices.Sorted(maps.Keys(files)))
	}
	m := manifestSnapshot(t, files)
	if m["channel"] != "draft" || m["contentHash"] == nil || m["pulledAt"] == nil || m["id"] != nil ||
		m["version"] != nil {
		t.Errorf("the draft's manifest says %v, want the draft channel, without an ID or a version", m)
	}
	wantCode(t, "the draft's export at version 2", call(h, http.MethodGet, draftPath+"?schema=2", nil,
		owner.header(false)), http.StatusConflict, api.CodeSchemaUnsupported)

	ready := addSnapshot(t, d.Admin(t), decode(t, ids.Workspace, ws), decode(t, ids.Project, p), "1.0.0", "ready",
		ids.UUID{}, fixtureFiles(t))
	exportPath := snapshotsPath(p) + "/" + ready + "/export"
	releases := bearer(makeToken(t, h, owner, workspaceTokensPath(ws), "snapshot:read").Secret)
	if res := call(h, http.MethodGet, snapshotsPath(p), nil, releases); res.status != http.StatusOK {
		t.Errorf("the list with a snapshot:read token: %d %s", res.status, res.body)
	}
	if res := call(h, http.MethodGet, exportPath, nil, releases); res.status != http.StatusOK {
		t.Errorf("the snapshot's export with a snapshot:read token: %d %s", res.status, res.body)
	}
	wantCode(t, "the draft's export with a snapshot:read token", call(h, http.MethodGet, draftPath, nil, releases),
		http.StatusForbidden, api.CodeInsufficientScope)
	content := bearer(makeToken(t, h, owner, workspaceTokensPath(ws), "project:read").Secret)
	for _, path := range []string{exportPath, draftPath} {
		if res := call(h, http.MethodGet, path, nil, content); res.status != http.StatusOK {
			t.Errorf("%s with a project:read token: %d %s", path, res.status, res.body)
		}
	}
}

// TestUnrepresentableAnswer checks the answer to an export at an older
// version of the published format that cannot express what the files hold,
// which only a server that reads two versions gives (plan note N2): 409
// schema_unrepresentable, with the version asked for and each construct,
// its file, its entity, which a value of the file's own has none of, its
// field, and why (PRD §6.4).
func TestUnrepresentableAnswer(t *testing.T) {
	t.Parallel()
	node := ids.Encode(ids.Node, ids.New())
	err := snapshotError(context.Background(), fmt.Errorf("render: %w", &schemaversion.UnrepresentableError{
		From: 2, To: 1, Constructs: []schemaversion.Construct{
			{File: "boards/harbour.json", Entity: node, Field: "title", Why: "a title of 300 characters"},
			{File: "storycreep.json", Field: "boards", Why: "too many boards"}}}))
	var e *api.Error
	if !errors.As(err, &e) || e.Code != api.CodeSchemaUnrepresentable {
		t.Fatalf("the answer is %v, want schema_unrepresentable", err)
	}
	got, jerr := json.Marshal(e.Details)
	if jerr != nil {
		t.Fatal(jerr)
	}
	want := `{"constructs":[{"file":"boards/harbour.json","entity":"` + node + `","field":"title","message":` +
		`"a title of 300 characters"},{"file":"storycreep.json","field":"boards","message":"too many boards"}],` +
		`"schema":1}`
	if string(got) != want {
		t.Errorf("the answer's details are %s, want %s", got, want)
	}
}
