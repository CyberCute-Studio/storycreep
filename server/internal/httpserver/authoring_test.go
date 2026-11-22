package httpserver

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/authoring"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
)

// authoringExportsPath returns the path of the project's authoring exports.
func authoringExportsPath(project string) string { return projectPath(project) + "/authoring-exports" }

// askExport asks for an authoring export with the body, as si.
func askExport(t *testing.T, h http.Handler, si signedIn, project string, body map[string]any) AuthoringExportBody {
	t.Helper()
	res := call(h, http.MethodPost, authoringExportsPath(project), jsonBody(t, body), si.header(true))
	if res.status != http.StatusAccepted {
		t.Fatalf("ask for an export: %d %s", res.status, res.body)
	}
	var e AuthoringExportBody
	if err := json.Unmarshal(res.body, &e); err != nil {
		t.Fatal(err)
	}
	if want := authoringExportsPath(project) + "/" + e.ID; res.header.Get("Location") != want {
		t.Errorf("Location is %q, want %q", res.header.Get("Location"), want)
	}
	return e
}

// buildExport runs the export's build, as its job does, and returns it as si
// reads it then.
func buildExport(t *testing.T, s *server, si signedIn, ws, project string, e AuthoringExportBody) AuthoringExportBody {
	t.Helper()
	if err := s.authoring.Build(t.Context(), decode(t, ids.Workspace, ws), decode(t, ids.Job, e.ID)); err != nil {
		t.Fatalf("build the export: %v", err)
	}
	res := call(s.main, http.MethodGet, authoringExportsPath(project)+"/"+e.ID, nil, si.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("read the export: %d %s", res.status, res.body)
	}
	var got AuthoringExportBody
	if err := json.Unmarshal(res.body, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// archiveOf downloads a ready export as si and returns its files by path.
func archiveOf(t *testing.T, h http.Handler, si signedIn, project string, e AuthoringExportBody) map[string][]byte {
	t.Helper()
	res := call(h, http.MethodGet, authoringExportsPath(project)+"/"+e.ID+"/download", nil, si.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("download the export: %d %s", res.status, res.body)
	}
	if e.SizeBytes == nil || int64(len(res.body)) != *e.SizeBytes {
		t.Errorf("the archive has %d bytes, and the export says %v", len(res.body), e.SizeBytes)
	}
	if got := res.header.Get("Content-Disposition"); !strings.Contains(got, e.FileName) ||
		!strings.HasPrefix(got, "attachment") {
		t.Errorf("Content-Disposition is %q, want an attachment named %s", got, e.FileName)
	}
	zr, err := zip.NewReader(bytes.NewReader(res.body), int64(len(res.body)))
	if err != nil {
		t.Fatalf("the archive is not a zip: %v", err)
	}
	files := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = data
	}
	return files
}

// validateArchive validates the archive's JSON files against the committed
// format's schema, with a validator that is not the generator's, and checks
// that the manifest's hashes and counts are of its files.
func validateArchive(t *testing.T, files map[string][]byte) authoring.Manifest {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(authoring.Schema()))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(authoring.SchemaID, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile(authoring.SchemaID)
	if err != nil {
		t.Fatal(err)
	}
	var m authoring.Manifest
	if err := json.Unmarshal(files[authoring.ManifestPath], &m); err != nil {
		t.Fatalf("the manifest: %v", err)
	}
	inst := map[string]any{}
	read := func(name string) {
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(files[name]))
		if err != nil {
			t.Fatalf("%s is not JSON: %v", name, err)
		}
		inst[name] = v
	}
	read(authoring.ManifestPath)
	for _, s := range m.Sections {
		if _, ok := files[s.Path]; !ok {
			t.Errorf("the manifest lists %s, which the archive lacks", s.Path)
			continue
		}
		read(s.Path)
		if got := sha256Hex(files[s.Path]); got != s.SHA256 {
			t.Errorf("the manifest's hash of %s is %s, and the file's is %s", s.Path, s.SHA256, got)
		}
	}
	if err := schema.Validate(inst); err != nil {
		t.Errorf("the archive does not validate against its schema: %v", err)
	}
	return m
}

// items returns the items of a section's file.
func items[T any](t *testing.T, files map[string][]byte, name string) []T {
	t.Helper()
	var f authoring.Items[T]
	if err := json.Unmarshal(files[name], &f); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return f.Items
}

// authoringFixture is a project with a board, whose node has a cover image, a
// component, a comment, a play state, and a release.
type authoringFixture struct {
	s            *server
	d            *dbtest.Database
	owner        signedIn
	ws, project  string
	board, node  string
	comment      string
	playState    string
	release      snapshot.Snapshot
	cover        []byte
	coverAssetID string
}

func newAuthoringFixture(t *testing.T) *authoringFixture {
	t.Helper()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Harbour").ID
	f := &authoringFixture{s: s, d: d, owner: owner, ws: ws, project: p, board: ids.Encode(ids.Board, ids.New()),
		node: newNodeID(), comment: ids.Encode(ids.Comment, ids.New()), playState: newPlayStateID(),
		cover: testPNG(t, 6, 6)}
	f.coverAssetID = upload(t, h, owner, p, f.cover, http.StatusCreated).ID
	end, typ, comp := newNodeID(), ids.Encode(ids.ComponentType, ids.New()), ids.Encode(ids.Component, ids.New())
	pos := map[string]int{"x": 0, "y": 0}
	cmd := func(typ string, p map[string]any) wireCommand { return wireCommand{Type: typ, Payload: p} }
	sendBatch(t, h, owner, p,
		cmd("board.create", map[string]any{"id": f.board, "name": "Quay"}),
		cmd("component_type.create", map[string]any{"id": typ, "name": "Person"}),
		cmd("component.create", map[string]any{"id": comp, "component_type_id": typ, "name": "Elara",
			"description": paragraph("Keeps the light.")}),
		cmd("node.create", map[string]any{"id": f.node, "board_id": f.board, "type": "element", "kind": "narration",
			"title": "Start", "text": paragraph("The quay, at night."), "position": pos, "cover_asset_id": f.coverAssetID}),
		cmd("node.create", map[string]any{"id": end, "board_id": f.board, "type": "element", "kind": "narration",
			"title": "Home", "text": paragraph("Home at last."), "position": pos, "is_ending": true}),
		cmd("connection.create", map[string]any{"id": ids.Encode(ids.Connection, ids.New()), "from_node_id": f.node,
			"to_node_id": end, "label": paragraph("Go home"), "condition": "1 + 1 == 2"}),
		cmd("board.set_entry_node", map[string]any{"id": f.board, "node_id": f.node}),
	)
	if res := postComment(t, h, owner, p, f.comment, f.node, "", "Does this read well?"); res.status != http.StatusOK {
		t.Fatalf("post a comment: %d %s", res.status, res.body)
	}
	state := emptyState(p)
	state["cursor"] = map[string]any{"boardId": f.board, "nodeId": f.node, "phase": "awaiting_choice"}
	sendBatch(t, h, owner, p, cmd("play_state.create", map[string]any{"id": f.playState, "name": "At the quay",
		"state": state}))
	r := validationReport(t, h, owner, p)
	release := snapshotFrom(t, call(h, http.MethodPost, snapshotsPath(p), publishBody(t, "1.0.0", r),
		owner.header(true)), http.StatusAccepted)
	startWorkers(t, s, d)
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		got := snapshotFrom(t, call(h, http.MethodGet, snapshotsPath(p)+"/"+release.ID, nil, owner.header(false)),
			http.StatusOK)
		if got.Status == snapshot.Ready {
			f.release = got
			break
		}
		if got.Status == snapshot.Failed || time.Now().After(deadline) {
			t.Fatalf("the snapshot's build ended as %+v", got)
		}
	}
	return f
}

// waitForExport waits for the workers that the fixture started to end the
// export, whose job they run too.
func waitForExport(t *testing.T, f *authoringFixture, si signedIn, id string) AuthoringExportBody {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		res := call(f.s.main, http.MethodGet, authoringExportsPath(f.project)+"/"+id, nil, si.header(false))
		if res.status != http.StatusOK {
			t.Fatalf("read the export: %d %s", res.status, res.body)
		}
		var e AuthoringExportBody
		if err := json.Unmarshal(res.body, &e); err != nil {
			t.Fatal(err)
		}
		if e.Status == authoring.StatusReady {
			return e
		}
		if e.Status == authoring.StatusFailed || time.Now().After(deadline) {
			t.Fatalf("the export ended as %+v", e)
		}
	}
}

// TestAuthoringExportFull checks a default export, which its job builds: it
// is full, holds the draft, the comments, the play states, an empty
// annotations section, the release history byte for byte, and the assets, a
// node's cover image among them, says that it holds personal data, in its
// answer and in its README, names who wrote what, and validates against the
// format's schema.
//
// Verifies: FR-DATA-01/AC5, FR-DATA-01/AC6, FR-EDIT-06/AC3, FR-DATA-04/AC3
func TestAuthoringExportFull(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	h := f.s.main
	e := askExport(t, h, f.owner, f.project, map[string]any{})
	if e.Status != authoring.StatusQueued || e.Profile != "full" || !e.IncludeAssets || !e.IncludeReleases ||
		!e.ContainsPersonal || e.SnapshotID != "" {
		t.Errorf("the default export is %+v", e)
	}
	e = waitForExport(t, f, f.owner, e.ID)
	if e.SizeBytes == nil || *e.SizeBytes == 0 || e.ImportMaxBytes != 2<<30 || e.ExceedsImportLimit {
		t.Errorf("the ready export is %+v", e)
	}
	if !strings.HasSuffix(e.FileName, "-draft-authoring.zip") {
		t.Errorf("the archive is named %q", e.FileName)
	}
	files := archiveOf(t, h, f.owner, f.project, e)
	m := validateArchive(t, files)
	if !m.PersonalData || m.Profile != authoring.ProfileFull || m.Source.Kind != "draft" {
		t.Errorf("the manifest is %+v", m)
	}
	if !strings.Contains(string(files[authoring.ReadmePath]), "it contains personal data") {
		t.Errorf("the README does not say that the archive holds personal data:\n%s", files[authoring.ReadmePath])
	}
	nodes := items[authoring.Node](t, files, "draft/nodes.json")
	i := slices.IndexFunc(nodes, func(n authoring.Node) bool { return n.ID == f.node })
	if i < 0 || nodes[i].CoverAssetID != f.coverAssetID {
		t.Fatalf("the nodes are %+v, want %s with the cover %s", nodes, f.node, f.coverAssetID)
	}
	assets := items[authoring.Asset](t, files, "assets/index.json")
	j := slices.IndexFunc(assets, func(a authoring.Asset) bool { return a.ID == f.coverAssetID })
	if j < 0 || !bytes.Equal(files[assets[j].Path], f.cover) {
		t.Errorf("the archive does not hold the cover's file as it was uploaded: %+v", assets)
	}
	if got := items[authoring.Expression](t, files, "draft/expressions.json"); len(got) != 1 ||
		got[0].Source != "1 + 1 == 2" {
		t.Errorf("the expressions are %+v", got)
	}
	comments := items[authoring.Comment](t, files, "comments.json")
	if len(comments) != 1 || comments[0].ID != f.comment || comments[0].TargetID != f.node ||
		comments[0].AuthorID != f.owner.body.UserID {
		t.Errorf("the comments are %+v", comments)
	}
	if got := items[authoring.PlayState](t, files, "play_states.json"); len(got) != 1 || got[0].ID != f.playState {
		t.Errorf("the play states are %+v", got)
	}
	if got := items[authoring.Annotation](t, files, "annotations.json"); len(got) != 0 {
		t.Errorf("the annotations are %+v, want none", got)
	}
	people := items[authoring.Person](t, files, "people.json")
	if !slices.ContainsFunc(people, func(p authoring.Person) bool {
		return p.ID == f.owner.body.UserID && p.DisplayName != ""
	}) {
		t.Errorf("people.json is %+v, want the comment's author", people)
	}
	for name, data := range files {
		if bytes.Contains(data, []byte("admin@example.com")) {
			t.Errorf("%s holds an email address", name)
		}
	}
	releases := items[authoring.Release](t, files, "releases/index.json")
	if len(releases) != 1 || releases[0].ID != f.release.ID || releases[0].PublishedBy != f.owner.body.UserID {
		t.Fatalf("the releases are %+v", releases)
	}
	zipped := exportFiles(t, h, f.owner, snapshotsPath(f.project)+"/"+f.release.ID+"/export")
	for name, want := range zipped {
		if got, ok := files[releases[0].Path+name]; !ok || !bytes.Equal(got, want) {
			t.Errorf("the release's %s is not byte-identical in the archive", name)
		}
	}
	// The export is recorded in the audit log, as FR-TEN-07 lists.
	if n := countRows(t, f.d, `SELECT count(*) FROM audit_entry WHERE action = 'project.export'
		AND actor_user_id = $1::uuid`, decode(t, ids.User, f.owner.body.UserID).String()); n != 1 {
		t.Errorf("the audit log has %d entries of the export, want 1", n)
	}
}

// exportFiles reads the zip at path as si, by its members' paths.
func exportFiles(t *testing.T, h http.Handler, si signedIn, path string) map[string][]byte {
	t.Helper()
	res := call(h, http.MethodGet, path, nil, si.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("export %s: %d %s", path, res.status, res.body)
	}
	zr, err := zip.NewReader(bytes.NewReader(res.body), int64(len(res.body)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, m := range zr.File {
		rc, err := m.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[m.Name] = data
	}
	return out
}

// TestAuthoringExportContentOnly checks the content-only profile: it holds
// the draft, without comments, play states, annotations, or anyone's name,
// and says so, and leaving out assets and releases leaves them out.
//
// Verifies: FR-DATA-01/AC5, FR-DATA-01/AC6
func TestAuthoringExportContentOnly(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	e := waitForExport(t, f, f.owner, askExport(t, f.s.main, f.owner, f.project, map[string]any{"profile": "content"}).ID)
	if e.ContainsPersonal {
		t.Errorf("a content-only export says that it holds personal data: %+v", e)
	}
	files := archiveOf(t, f.s.main, f.owner, f.project, e)
	m := validateArchive(t, files)
	if m.PersonalData || m.Profile != authoring.ProfileContent {
		t.Errorf("the manifest is %+v", m)
	}
	for _, gone := range []string{"comments.json", "play_states.json", "annotations.json", "people.json"} {
		if _, ok := files[gone]; ok {
			t.Errorf("a content-only archive holds %s", gone)
		}
	}
	user := []byte(f.owner.body.UserID)
	for name, data := range files {
		if strings.HasPrefix(name, "releases/") && name != "releases/index.json" {
			continue
		}
		if bytes.Contains(data, user) {
			t.Errorf("%s names the user %s", name, user)
		}
	}
	if got := items[authoring.Release](t, files, "releases/index.json"); len(got) != 1 || got[0].PublishedBy != "" {
		t.Errorf("the releases are %+v, want one that names nobody", got)
	}

	bare := waitForExport(t, f, f.owner, askExport(t, f.s.main, f.owner, f.project, map[string]any{"profile": "content",
		"include_assets": false, "include_releases": false}).ID)
	files = archiveOf(t, f.s.main, f.owner, f.project, bare)
	validateArchive(t, files)
	for name := range files {
		if strings.HasPrefix(name, "assets/") || strings.HasPrefix(name, "releases/") {
			t.Errorf("an archive without assets and releases holds %s", name)
		}
	}
}

// TestAuthoringExportOfRelease checks an archive of a release: its content is
// the release's files, byte for byte, without the draft's sections, and it
// holds the comments and play states about what the release has, and counts
// the others, in its manifest and its README.
//
// Verifies: FR-DATA-01/AC5
func TestAuthoringExportOfRelease(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	h := f.s.main
	// A node that the release does not have, with a comment and a play state.
	later, comment, state := newNodeID(), ids.Encode(ids.Comment, ids.New()), newPlayStateID()
	sendBatch(t, h, f.owner, f.project, wireCommand{Type: "node.create", Payload: map[string]any{"id": later,
		"board_id": f.board, "type": "element", "kind": "narration", "title": "Later",
		"position": map[string]int{"x": 9, "y": 9}}})
	if res := postComment(t, h, f.owner, f.project, comment, later, "", "Added after the release"); res.status !=
		http.StatusOK {
		t.Fatalf("post a comment: %d %s", res.status, res.body)
	}
	saved := emptyState(f.project)
	saved["cursor"] = map[string]any{"boardId": f.board, "nodeId": later, "phase": "awaiting_choice"}
	sendBatch(t, h, f.owner, f.project, wireCommand{Type: "play_state.create", Payload: map[string]any{"id": state,
		"name": "At the later node", "state": saved}})

	e := waitForExport(t, f, f.owner, askExport(t, h, f.owner, f.project, map[string]any{"source": f.release.ID}).ID)
	if e.SnapshotID != f.release.ID || !strings.HasSuffix(e.FileName, "-1.0.0-authoring.zip") {
		t.Errorf("the export is %+v", e)
	}
	files := archiveOf(t, h, f.owner, f.project, e)
	m := validateArchive(t, files)
	if m.Source.Kind != "release" || m.Source.SnapshotID != f.release.ID || m.Source.Version != "1.0.0" {
		t.Errorf("the source is %+v", m.Source)
	}
	if m.LeftOut != (authoring.LeftOut{Comments: 1, PlayStates: 1}) {
		t.Errorf("the manifest leaves out %+v, want a comment and a play state", m.LeftOut)
	}
	if !strings.Contains(string(files[authoring.ReadmePath]), "1 comment(s) and 1 play state(s)") {
		t.Errorf("the README does not count what it leaves out:\n%s", files[authoring.ReadmePath])
	}
	for name := range files {
		if strings.HasPrefix(name, "draft/") {
			t.Errorf("an archive of a release holds %s", name)
		}
	}
	for name, want := range exportFiles(t, h, f.owner, snapshotsPath(f.project)+"/"+f.release.ID+"/export") {
		if got, ok := files["release/"+name]; !ok || !bytes.Equal(got, want) {
			t.Errorf("the release's %s is not byte-identical in the archive", name)
		}
	}
	comments := items[authoring.Comment](t, files, "comments.json")
	if len(comments) != 1 || comments[0].ID != f.comment {
		t.Errorf("the comments are %+v, want only the one about what the release has", comments)
	}
	if got := items[authoring.PlayState](t, files, "play_states.json"); len(got) != 1 || got[0].ID != f.playState {
		t.Errorf("the play states are %+v, want only the one at what the release has", got)
	}
}

// TestAuthoringExportAccess checks who reads an export, and the refusals: a
// token with snapshot:read alone exports a ready snapshot but is refused the
// draft's export and the authoring export, and a token with export:read
// reaches it; another member of the project reads none of the user's
// exports; a user has one on its way at a time; an export that is not ready
// has no file; and a source that is not ready, or not a snapshot's, is refused.
//
// Verifies: FR-AUTH-04/AC7
func TestAuthoringExportAccess(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	h := f.s.main
	owner := f.as["owner"]
	p := f.project(t, "")
	release := f.snapshot(t, p)

	read := makeToken(t, h, owner, workspaceTokensPath(f.ws), "snapshot:read")
	if res := call(h, http.MethodGet, snapshotsPath(p)+"/"+release+"/export", nil, bearer(read.Secret)); res.status !=
		http.StatusOK {
		t.Errorf("a snapshot:read token exports a ready snapshot: %d %s", res.status, res.body)
	}
	wantCode(t, "the draft's export with snapshot:read", call(h, http.MethodGet, projectPath(p)+"/draft/export", nil,
		bearer(read.Secret)), http.StatusForbidden, api.CodeInsufficientScope)
	wantCode(t, "the authoring export with snapshot:read", call(h, http.MethodPost, authoringExportsPath(p),
		jsonBody(t, map[string]any{}), bearer(read.Secret)), http.StatusForbidden, api.CodeInsufficientScope)
	for _, held := range []string{"project:read", "project:write", "snapshot:write"} {
		tok := makeToken(t, h, owner, workspaceTokensPath(f.ws), held)
		wantCode(t, "the authoring export with "+held, call(h, http.MethodPost, authoringExportsPath(p),
			jsonBody(t, map[string]any{}), bearer(tok.Secret)), http.StatusForbidden, api.CodeInsufficientScope)
	}

	exportToken := makeToken(t, h, owner, workspaceTokensPath(f.ws), "export:read")
	res := call(h, http.MethodPost, authoringExportsPath(p), jsonBody(t, map[string]any{"profile": "content"}),
		bearer(exportToken.Secret))
	if res.status != http.StatusAccepted {
		t.Fatalf("an export:read token asks for an export: %d %s", res.status, res.body)
	}
	var queued AuthoringExportBody
	if err := json.Unmarshal(res.body, &queued); err != nil {
		t.Fatal(err)
	}
	path := authoringExportsPath(p) + "/" + queued.ID

	again := call(h, http.MethodPost, authoringExportsPath(p), jsonBody(t, map[string]any{}), owner.header(true))
	wantCode(t, "a second export on its way", again, http.StatusConflict, api.CodeExportInProgress)
	if got := again.details(t)["export_id"]; got != queued.ID {
		t.Errorf("details.export_id is %v, want %s", got, queued.ID)
	}
	notReady := call(h, http.MethodGet, path+"/download", nil, owner.header(false))
	wantCode(t, "the download of a queued export", notReady, http.StatusConflict, api.CodeExportNotReady)
	if got := notReady.details(t)["status"]; got != authoring.StatusQueued {
		t.Errorf("details.status is %v", got)
	}

	// Another member with a role on the project reads none of it.
	editor := f.as["editor"]
	for _, c := range []string{path, path + "/download"} {
		wantCode(t, "another member's read of "+c, call(h, http.MethodGet, c, nil, editor.header(false)),
			http.StatusNotFound, api.CodeNotFound)
	}
	if got := itemIDs(t, h, editor.header(false), authoringExportsPath(p)); len(got) != 0 {
		t.Errorf("another member lists the exports %q", got)
	}
	if got := itemIDs(t, h, bearer(exportToken.Secret), authoringExportsPath(p)); !slices.Equal(got, []string{queued.ID}) {
		t.Errorf("the token's owner lists the exports %q, want %s", got, queued.ID)
	}

	if err := f.s.authoring.Build(t.Context(), decode(t, ids.Workspace, f.ws), decode(t, ids.Job, queued.ID)); err != nil {
		t.Fatal(err)
	}
	if res := call(h, http.MethodGet, path+"/download", nil, bearer(exportToken.Secret)); res.status != http.StatusOK {
		t.Errorf("the token's download: %d %s", res.status, res.body)
	}
	wantCode(t, "a source that is not a snapshot's ID", call(h, http.MethodPost, authoringExportsPath(p),
		jsonBody(t, map[string]any{"source": "DRAFT"}), owner.header(true)), http.StatusBadRequest, api.CodeInvalidID)
	wantCode(t, "a snapshot that the project does not have", call(h, http.MethodPost, authoringExportsPath(p),
		jsonBody(t, map[string]any{"source": ids.Encode(ids.Snapshot, ids.New())}), owner.header(true)),
		http.StatusNotFound, api.CodeNotFound)
}

// TestAuthoringExportExpires checks that an export's time passing deletes its
// row and its file, and that a project's purge leaves its file for the orphan
// reconciliation, which reads the key of an export that still exists as
// referenced.
func TestAuthoringExportExpires(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	h := f.s.main
	owner := f.as["owner"]
	p := f.project(t, "")
	e := askExport(t, h, owner, p, map[string]any{})
	e = buildExport(t, f.s, owner, f.ws, p, e)
	if e.Status != authoring.StatusReady {
		t.Fatalf("the export is %+v", e)
	}
	ws, id := decode(t, ids.Workspace, f.ws), decode(t, ids.Job, e.ID)
	// The orphan reconciliation reads the archive's key as a row's while the
	// export exists, and as nobody's once it is gone.
	var key string
	if err := f.d.Admin(t).QueryRow(t.Context(), `SELECT storage_key FROM authoring_export WHERE id = $1`,
		id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	referenced := func() bool {
		t.Helper()
		got, err := queries.New(f.d.Admin(t)).ReferencedBlobs(t.Context(), queries.ReferencedBlobsParams{
			WorkspaceID: pgtype.UUID{Bytes: ws, Valid: true}, Keys: []string{key}})
		if err != nil {
			t.Fatal(err)
		}
		return len(got) == 1
	}
	if !referenced() {
		t.Errorf("the orphan reconciliation does not read %s as an export's", key)
	}
	if err := f.s.authoring.Expire(t.Context(), ws, id); err != nil {
		t.Fatal(err)
	}
	if referenced() {
		t.Errorf("%s is still read as an export's once the export is gone", key)
	}
	wantCode(t, "an export that expired", call(h, http.MethodGet, authoringExportsPath(p)+"/"+e.ID, nil,
		owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	wantCode(t, "the download of one that expired", call(h, http.MethodGet, authoringExportsPath(p)+"/"+e.ID+"/download",
		nil, owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	if err := f.s.authoring.Expire(t.Context(), ws, id); err != nil {
		t.Errorf("expiring an export that is gone: %v", err)
	}
}

// sha256Hex returns the SHA-256 of data, in hexadecimal.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
