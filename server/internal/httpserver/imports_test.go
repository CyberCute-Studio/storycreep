package httpserver

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/authoring"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/maintenance"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
)

func importsPath(ws string) string { return "/api/v1/workspaces/" + ws + "/project-imports" }

// openMaintenance opens the maintenance path of the test's server, which
// Run opens for a real one, for the import's collision check.
func openMaintenance(t *testing.T, s *server, d *dbtest.Database) {
	t.Helper()
	db, err := maintenance.Open(context.Background(), d.MaintenanceURL)
	if err != nil {
		t.Fatal(err)
	}
	s.maintenance.mu.Lock()
	s.maintenance.db = db
	s.maintenance.mu.Unlock()
	t.Cleanup(s.maintenance.close)
}

// beginImport asks for an import of an archive of size bytes as si.
func beginImport(t *testing.T, h http.Handler, si signedIn, ws string, size int, policy string) ProjectImportBody {
	t.Helper()
	body := map[string]any{"file_name": "harbour.zip", "size_bytes": size}
	if policy != "" {
		body["id_policy"] = policy
	}
	res := call(h, http.MethodPost, importsPath(ws), jsonBody(t, body), si.header(true))
	if res.status != http.StatusCreated {
		t.Fatalf("begin the import: %d %s", res.status, res.body)
	}
	var i ProjectImportBody
	if err := json.Unmarshal(res.body, &i); err != nil {
		t.Fatal(err)
	}
	return i
}

// putChunks sends the chunks of data whose indexes only names, or all of them.
func putChunks(t *testing.T, h http.Handler, si signedIn, ws string, i ProjectImportBody, data []byte, only ...int) {
	t.Helper()
	for n := range i.ChunkCount {
		if only != nil && !slices.Contains(only, n) {
			continue
		}
		end := min((n+1)*i.ChunkBytes, len(data))
		res := call(h, http.MethodPut, fmt.Sprintf("%s/%s/chunks/%d", importsPath(ws), i.ID, n), data[n*i.ChunkBytes:end],
			withOctets(si.header(true)))
		if res.status != http.StatusNoContent {
			t.Fatalf("put chunk %d: %d %s", n, res.status, res.body)
		}
	}
}

func withOctets(h http.Header) http.Header {
	h.Set("Content-Type", "application/octet-stream")
	return h
}

// readImport reads the import as si.
func readImport(t *testing.T, h http.Handler, si signedIn, ws, id string) ProjectImportBody {
	t.Helper()
	res := call(h, http.MethodGet, importsPath(ws)+"/"+id, nil, si.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("read the import: %d %s", res.status, res.body)
	}
	var i ProjectImportBody
	if err := json.Unmarshal(res.body, &i); err != nil {
		t.Fatal(err)
	}
	return i
}

// runImportTo uploads data, completes the import, and waits for the workers to
// end it.
func runImportTo(t *testing.T, s *server, si signedIn, ws string, data []byte, policy string) ProjectImportBody {
	t.Helper()
	i := beginImport(t, s.main, si, ws, len(data), policy)
	putChunks(t, s.main, si, ws, i, data)
	res := call(s.main, http.MethodPost, importsPath(ws)+"/"+i.ID+"/complete", nil, si.header(true))
	if res.status != http.StatusAccepted {
		t.Fatalf("complete the import: %d %s", res.status, res.body)
	}
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		got := readImport(t, s.main, si, ws, i.ID)
		if got.Status == authoring.ImportReady || got.Status == authoring.ImportFailed {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("the import did not end: %+v", got)
		}
	}
}

// mustImport imports data and fails the test unless the import is ready.
func mustImport(t *testing.T, s *server, si signedIn, ws string, data []byte, policy string) ProjectImportBody {
	t.Helper()
	got := runImportTo(t, s, si, ws, data, policy)
	if got.Status != authoring.ImportReady || got.Report == nil {
		t.Fatalf("the import ended as %s: %+v", got.Status, got.Failure)
	}
	return got
}

// zipOf returns a zip of the files, in the order of their paths.
func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// resealSection writes the section's file, and its hash and its count in the
// archive's manifest, as an archive that a person edited with care has them.
func resealSection(t *testing.T, files map[string][]byte, path string, data []byte, count int) {
	t.Helper()
	files[path] = data
	var m authoring.Manifest
	if err := json.Unmarshal(files[authoring.ManifestPath], &m); err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range m.Sections {
		if m.Sections[i].Path == path {
			m.Sections[i].SHA256, m.Sections[i].Items, found = sha256Hex(data), count, true
		}
	}
	if !found {
		t.Fatalf("the manifest lists no section at %s", path)
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	files[authoring.ManifestPath] = out
}

// exportedArchive exports the fixture's draft in full and returns its files.
func exportedArchive(t *testing.T, f *authoringFixture) map[string][]byte {
	t.Helper()
	e := askExport(t, f.s.main, f.owner, f.project, map[string]any{})
	e = waitForExport(t, f, f.owner, e.ID)
	return archiveOf(t, f.s.main, f.owner, f.project, e)
}

// exportOf exports the project in full as si, on a server whose workers run.
func exportOf(t *testing.T, s *server, si signedIn, project string) map[string][]byte {
	t.Helper()
	e := askExport(t, s.main, si, project, map[string]any{})
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		res := call(s.main, http.MethodGet, authoringExportsPath(project)+"/"+e.ID, nil, si.header(false))
		if err := json.Unmarshal(res.body, &e); err != nil {
			t.Fatal(err)
		}
		if e.Status == authoring.StatusReady {
			return archiveOf(t, s.main, si, project, e)
		}
		if e.Status == authoring.StatusFailed || time.Now().After(deadline) {
			t.Fatalf("the export ended as %+v", e)
		}
	}
}

var (
	publicID  = regexp.MustCompile(`\b(?:prj|fld|bd|nd|bo|cn|var|ct|atr|cmp|cmt|snp|ast|pst|usr)_[0-9A-Z]{26}\b`)
	timestamp = regexp.MustCompile(`"(?:createdAt|exportedAt|publishedAt|builtAt|editedAt|resolvedAt)": "[^"]*"`)
)

// semantics returns what a section of an archive says, without its IDs and
// timestamps (FR-DATA-01 AC1): each of its items as text, sorted, as the items
// of a section are in the order of their IDs, which a remap changes.
func semantics(t *testing.T, data []byte) []string {
	t.Helper()
	var f struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	clean := func(b []byte) string {
		var out bytes.Buffer
		if err := json.Indent(&out, b, "", " "); err != nil {
			t.Fatal(err)
		}
		s := publicID.ReplaceAllString(out.String(), "ID")
		return timestamp.ReplaceAllString(s, `"at": ""`)
	}
	if f.Items == nil {
		return []string{clean(data)}
	}
	out := make([]string, len(f.Items))
	for i, it := range f.Items {
		out[i] = clean(it)
	}
	slices.Sort(out)
	return out
}

// sharedIDs returns the public IDs that the two archives both hold.
func sharedIDs(a, b map[string][]byte) []string {
	seen := map[string]bool{}
	for name, data := range a {
		if strings.HasSuffix(name, ".json") {
			for _, id := range publicID.FindAllString(string(data), -1) {
				if !strings.HasPrefix(id, "usr_") {
					seen[id] = true
				}
			}
		}
	}
	var out []string
	for name, data := range b {
		if strings.HasSuffix(name, ".json") {
			for _, id := range publicID.FindAllString(string(data), -1) {
				if seen[id] {
					out = append(out, id+" in "+name)
				}
			}
		}
	}
	return out
}

// TestImportRoundTrip checks that an export imported again into its own
// instance, which takes fresh IDs, exports as the same data, whatever its IDs
// and timestamps, and shares no ID with the project it came from.
//
// Verifies: FR-DATA-01/AC1, FR-DATA-01/AC8
func TestImportRoundTrip(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	before := exportedArchive(t, f)
	got := mustImport(t, f.s, f.owner, f.ws, zipOf(t, before), "")
	if got.Report.Policy != authoring.PolicyRemap {
		t.Errorf("importing an archive into its own instance applied %q, want remap", got.Report.Policy)
	}
	if got.ProjectID == "" || got.ProjectID == f.project {
		t.Fatalf("the import created the project %q", got.ProjectID)
	}
	after := exportOf(t, f.s, f.owner, got.ProjectID)
	var m authoring.Manifest
	if err := json.Unmarshal(before[authoring.ManifestPath], &m); err != nil {
		t.Fatal(err)
	}
	for _, sec := range m.Sections {
		if sec.Name == "assets" || sec.Name == "releases" {
			continue
		}
		if a, b := semantics(t, before[sec.Path]), semantics(t, after[sec.Path]); !slices.Equal(a, b) {
			t.Errorf("the section %s changed:\nbefore %v\nafter  %v", sec.Name, a, b)
		}
	}
	if shared := sharedIDs(before, after); len(shared) > 0 {
		t.Errorf("the two projects share IDs: %v", shared[:min(len(shared), 5)])
	}
	if res := call(f.s.main, http.MethodGet, snapshotsPath(got.ProjectID), nil, f.owner.header(false)); !strings.Contains(
		string(res.body), f.release.Version) {
		t.Errorf("the imported project's releases are %s", res.body)
	}
}

// TestImportInstanceMove checks a move to an empty instance: the default
// policy keeps every ID, the play state is there as it was saved, and the
// pinned release's files are byte-identical.
//
// Verifies: FR-DATA-01/AC7
func TestImportInstanceMove(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	files := exportedArchive(t, f)
	s2, d2, owner2, ws2 := inviteServer(t, "open", nil)
	openMaintenance(t, s2, d2)
	startWorkers(t, s2, d2)
	got := mustImport(t, s2, owner2, ws2, zipOf(t, files), "")
	if got.Report.Policy != authoring.PolicyPreserve || got.ProjectID != f.project {
		t.Fatalf("the import applied %q and made %s, want preserve and %s", got.Report.Policy, got.ProjectID, f.project)
	}
	want := exportFiles(t, f.s.main, f.owner, snapshotsPath(f.project)+"/"+f.release.ID+"/export")
	have := exportFiles(t, s2.main, owner2, snapshotsPath(f.project)+"/"+f.release.ID+"/export")
	if len(want) == 0 || len(want) != len(have) {
		t.Fatalf("the pinned release has %d files on A and %d on B", len(want), len(have))
	}
	for name, data := range want {
		if !bytes.Equal(data, have[name]) {
			t.Errorf("the pinned release's %s differs between the instances", name)
		}
	}
	moved := exportOf(t, s2, owner2, f.project)
	wantStates, gotStates := items[authoring.PlayState](t, files, "play_states.json"),
		items[authoring.PlayState](t, moved, "play_states.json")
	if len(gotStates) != 1 || gotStates[0].ID != f.playState ||
		!sameJSON(t, gotStates[0].State, wantStates[0].State) {
		t.Errorf("the play state moved as %+v, want %+v", gotStates, wantStates)
	}
	people := items[authoring.Person](t, moved, "people.json")
	if len(people) != 1 || people[0].DisplayName == "" || people[0].ID == f.owner.body.UserID {
		t.Errorf("the comment's author, who has no account on B, is %+v", people)
	}
}

// TestImportAuthorization checks that importing is authorised as creating a
// project is, and counts against the projects limit.
//
// Verifies: FR-DATA-01/AC9
func TestImportAuthorization(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	newAccount(t, f.s.main, "import-editor@example.com")
	editor := signIn(t, f.s.main, "import-editor@example.com")
	join(t, f.d, editor, f.ws, "editor")
	body := map[string]any{"file_name": "a.zip", "size_bytes": 10}
	res := call(f.s.main, http.MethodPost, importsPath(f.ws), jsonBody(t, body), editor.header(true))
	if res.status != http.StatusForbidden {
		t.Errorf("an editor's import: %d %s, want 403", res.status, res.body)
	}
}

// TestImportAtProjectsLimit checks that a workspace at its projects limit is
// refused before anything is uploaded, and that no project is created.
//
// Verifies: FR-DATA-01/AC9
func TestImportAtProjectsLimit(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_WORKSPACE_LIMITS": "projects=1"})
	createProject(t, s.main, owner, ws, "The only one")
	res := call(s.main, http.MethodPost, importsPath(ws), jsonBody(t, map[string]any{"file_name": "a.zip",
		"size_bytes": 10}), owner.header(true))
	wantCode(t, "an import at the projects limit", res, http.StatusConflict, "limit_exceeded")
}

// TestImportCollisionOnChildIDs checks an archive whose project ID is unused,
// and whose node and snapshot IDs are another workspace's: the import remaps,
// with no primary-key error, the projects share no ID, and the report says no
// more than any other remapped import's.
//
// Verifies: FR-DATA-01/AC10
func TestImportCollisionOnChildIDs(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	files := exportedArchive(t, f)
	fresh := ids.Encode(ids.Project, ids.New())
	for name, data := range files {
		if strings.HasSuffix(name, ".json") {
			files[name] = bytes.ReplaceAll(data, []byte(f.project), []byte(fresh))
		}
	}
	rehash(t, files)
	res := call(f.s.main, http.MethodPost, "/api/v1/workspaces", jsonBody(t, map[string]any{"name": "Elsewhere"}),
		f.owner.header(true))
	if res.status != http.StatusCreated {
		t.Fatalf("create a workspace: %d %s", res.status, res.body)
	}
	var other struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res.body, &other); err != nil {
		t.Fatal(err)
	}
	got := mustImport(t, f.s, f.owner, other.ID, zipOf(t, files), "")
	if got.Report.Policy != authoring.PolicyRemap {
		t.Fatalf("the import applied %q, want remap", got.Report.Policy)
	}
	after := exportOf(t, f.s, f.owner, got.ProjectID)
	if shared := sharedIDs(files, after); len(shared) > 0 {
		t.Errorf("the two projects share IDs: %v", shared[:min(len(shared), 5)])
	}
	report := fmt.Sprintf("%+v", *got.Report)
	for _, leak := range []string{f.project, f.ws, f.node} {
		if strings.Contains(report, leak) {
			t.Errorf("the report names %s: %s", leak, report)
		}
	}
}

// rehash writes each section's hash in the manifest anew.
func rehash(t *testing.T, files map[string][]byte) {
	t.Helper()
	var m authoring.Manifest
	if err := json.Unmarshal(files[authoring.ManifestPath], &m); err != nil {
		t.Fatal(err)
	}
	for i := range m.Sections {
		m.Sections[i].SHA256 = sha256Hex(files[m.Sections[i].Path])
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	files[authoring.ManifestPath] = out
}

// failedImport imports data and returns the failure, once it has checked that
// nothing of it was written.
func failedImport(t *testing.T, s *server, si signedIn, ws string, data []byte) *ProjectImportFailure {
	t.Helper()
	before := projectCount(t, s, si, ws)
	got := runImportTo(t, s, si, ws, data, "")
	if got.Status != authoring.ImportFailed || got.Failure == nil {
		t.Fatalf("the import ended as %s: %+v", got.Status, got.Report)
	}
	if after := projectCount(t, s, si, ws); after != before {
		t.Errorf("the refused import left %d projects, and there were %d", after, before)
	}
	return got.Failure
}

// projectCount counts the workspace's projects.
func projectCount(t *testing.T, s *server, si signedIn, ws string) int {
	t.Helper()
	res := call(s.main, http.MethodGet, "/api/v1/workspaces/"+ws+"/projects", nil, si.header(false))
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(res.body, &page); err != nil {
		t.Fatal(err)
	}
	return len(page.Items)
}

// TestImportRefusesBadReleases checks that a tampered release, one whose AST
// reads a variable that it does not declare, and one of an astVersion past
// the server's, are each refused with the release's version and the check,
// and that no row is written.
//
// Verifies: FR-DATA-01/AC11
func TestImportRefusesBadReleases(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	declared := ids.Encode(ids.Variable, ids.New())
	cn := ids.Encode(ids.Connection, ids.New())
	sendBatch(t, f.s.main, f.owner, f.project,
		wireCommand{Type: "variable.create", Payload: map[string]any{"id": declared, "key": "flag", "type": "bool",
			"scope": "global"}},
		wireCommand{Type: "connection.create", Payload: map[string]any{"id": cn, "from_node_id": f.node,
			"to_node_id": f.node, "condition": "flag"}})
	r := validationReport(t, f.s.main, f.owner, f.project)
	next := snapshotFrom(t, call(f.s.main, http.MethodPost, snapshotsPath(f.project), publishBody(t, "1.1.0", r),
		f.owner.header(true)), http.StatusAccepted)
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		got := snapshotFrom(t, call(f.s.main, http.MethodGet, snapshotsPath(f.project)+"/"+next.ID, nil,
			f.owner.header(false)), http.StatusOK)
		if got.Status == snapshot.Ready {
			break
		}
		if got.Status == snapshot.Failed || time.Now().After(deadline) {
			t.Fatalf("the snapshot's build ended as %+v", got)
		}
	}
	clean := exportedArchive(t, f)
	for name, tc := range map[string]struct {
		reseal bool
		edit   func(release schemaversion.Files)
		check  string
	}{
		"tampered file": {reseal: false, check: "hashes", edit: func(release schemaversion.Files) {
			for p, data := range release {
				if strings.HasPrefix(p, "boards/") {
					release[p] = bytes.Replace(data, []byte("The quay"), []byte("A quay"), 1)
				}
			}
		}},
		"undeclared variable": {reseal: true, check: "references_and_asts", edit: func(release schemaversion.Files) {
			var doc map[string]any
			if err := json.Unmarshal(release[published.VariablesPath], &doc); err != nil {
				t.Fatal(err)
			}
			doc["variables"] = []any{}
			data, err := schemaversion.Encode(doc)
			if err != nil {
				t.Fatal(err)
			}
			release[published.VariablesPath] = data
		}},
		"astVersion": {reseal: true, check: "ast_version", edit: func(release schemaversion.Files) {
			release[published.ManifestPath] = bytes.Replace(release[published.ManifestPath], []byte(`"astVersion": 1`),
				[]byte(`"astVersion": 999`), 1)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			files := maps.Clone(clean)
			version := tamperOwnRelease(t, files, "1.1.0", tc.reseal, tc.edit)
			got := failedImport(t, f.s, f.owner, f.ws, zipOf(t, files))
			if got.Code != authoring.FailReleaseInvalid || got.Version != version || got.Check != tc.check {
				t.Errorf("the failure is %+v, want release_invalid of %s at %s", got, version, tc.check)
			}
		})
	}
}

// tamperOwnRelease is tamperRelease of the release of the version.
func tamperOwnRelease(t *testing.T, files map[string][]byte, version string, reseal bool,
	edit func(release schemaversion.Files)) string {
	t.Helper()
	list := items[authoring.Release](t, files, "releases/index.json")
	i := slices.IndexFunc(list, func(r authoring.Release) bool { return r.Version == version })
	if i < 0 {
		t.Fatalf("the archive has no release %s", version)
	}
	release := schemaversion.Files{}
	for name, data := range files {
		if p, ok := strings.CutPrefix(name, list[i].Path); ok {
			release[p] = data
		}
	}
	edit(release)
	if reseal {
		sealed, err := schemaversion.Seal(release)
		if err != nil {
			t.Fatal(err)
		}
		release = sealed
	}
	for p, data := range release {
		files[list[i].Path+p] = data
	}
	if reseal {
		var m published.Manifest
		if err := published.Decode(release[published.ManifestPath], &m); err != nil {
			t.Fatal(err)
		}
		snap, ok := m.Snapshot.(*published.ReleaseSnapshot)
		if !ok {
			t.Fatalf("the release's manifest has the snapshot %T", m.Snapshot)
		}
		list[i].ContentHash = string(snap.ContentHash)
		index, err := json.Marshal(authoring.Items[authoring.Release]{Items: list})
		if err != nil {
			t.Fatal(err)
		}
		resealSection(t, files, "releases/index.json", index, len(list))
	}
	return version
}

// TestImportRefusesAnnotations checks that an archive whose annotations
// section holds a frame, which validates against the format's schema, is
// refused by name, and writes nothing.
//
// Verifies: FR-DATA-01/AC12
func TestImportRefusesAnnotations(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	files := exportedArchive(t, f)
	frame, err := json.Marshal(authoring.Items[authoring.Annotation]{Items: []authoring.Annotation{{
		ID: "frame-1", BoardID: f.board, Kind: "frame", Geometry: authoring.Geometry{Width: 10, Height: 10}}}})
	if err != nil {
		t.Fatal(err)
	}
	resealSection(t, files, "annotations.json", frame, 1)
	got := failedImport(t, f.s, f.owner, f.ws, zipOf(t, files))
	if got.Code != authoring.FailSectionUnsupported || got.Section != "annotations" {
		t.Errorf("the failure is %+v, want section_unsupported naming annotations", got)
	}
}

// TestImportResumesUpload checks that an upload that was cut off resumes from
// the chunks that the server lists, that a chunk may be sent again, and that
// it cannot complete while one is missing.
//
// Verifies: FR-DATA-01/AC4
func TestImportResumesUpload(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	files := exportedArchive(t, f)
	data := zipOf(t, files)
	i := beginImport(t, f.s.main, f.owner, f.ws, len(data), "remap")
	if i.ChunkCount != chunkCountOf(len(data), i.ChunkBytes) || i.Status != authoring.ImportUploading {
		t.Fatalf("the import is %+v", i)
	}
	complete := func() response {
		return call(f.s.main, http.MethodPost, importsPath(f.ws)+"/"+i.ID+"/complete", nil, f.owner.header(true))
	}
	wantCode(t, "completing an upload without its chunks", complete(), http.StatusConflict, "import_incomplete")
	putChunks(t, f.s.main, f.owner, f.ws, i, data, 0)
	if got := readImport(t, f.s.main, f.owner, f.ws, i.ID); !slices.Equal(got.Received, []int{0}) {
		t.Errorf("after one chunk the import lists %v", got.Received)
	}
	putChunks(t, f.s.main, f.owner, f.ws, i, data)
	if got := readImport(t, f.s.main, f.owner, f.ws, i.ID); len(got.Received) != i.ChunkCount {
		t.Errorf("after every chunk the import lists %v", got.Received)
	}
	if res := complete(); res.status != http.StatusAccepted {
		t.Fatalf("complete: %d %s", res.status, res.body)
	}
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		got := readImport(t, f.s.main, f.owner, f.ws, i.ID)
		if got.Status == authoring.ImportReady {
			if got.Phase != "done" || got.Report == nil {
				t.Errorf("the ready import is %+v", got)
			}
			return
		}
		if got.Status == authoring.ImportFailed || time.Now().After(deadline) {
			t.Fatalf("the import ended as %+v: %+v", got.Status, got.Failure)
		}
	}
}

// sameJSON says whether two JSON documents hold the same value.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}

func chunkCountOf(size, chunk int) int { return (size + chunk - 1) / chunk }

// TestImportRefusesBadZips checks the zip guards end to end: a member that
// climbs out of the archive is refused before anything is read of it.
//
// Verifies: NFR-SEC-13
func TestImportRefusesBadZips(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	files := exportedArchive(t, f)
	files["../escape.txt"] = []byte("out")
	got := failedImport(t, f.s, f.owner, f.ws, zipOf(t, files))
	if got.Code != authoring.FailArchiveInvalid || got.Check != "path" {
		t.Errorf("the failure is %+v, want archive_invalid at path", got)
	}
}

// TestImportOfARelease checks that an archive of a release, which holds the
// release's files rather than a draft, imports as a project whose draft is that
// release: its boards, nodes, connections, expressions, and text.
//
// Verifies: FR-DATA-01/AC1, FR-DATA-01/AC5
func TestImportOfARelease(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	e := askExport(t, f.s.main, f.owner, f.project, map[string]any{"source": f.release.ID})
	e = waitForExport(t, f, f.owner, e.ID)
	files := archiveOf(t, f.s.main, f.owner, f.project, e)
	got := mustImport(t, f.s, f.owner, f.ws, zipOf(t, files), "")
	if got.Report.Boards != 1 || got.Report.Nodes != 2 || got.Report.Releases != 1 || got.Report.Comments != 1 {
		t.Errorf("the report is %+v", *got.Report)
	}
	after := exportOf(t, f.s, f.owner, got.ProjectID)
	nodes := items[authoring.Node](t, after, "draft/nodes.json")
	titles := []string{}
	for _, n := range nodes {
		titles = append(titles, n.Title)
	}
	slices.Sort(titles)
	if !slices.Equal(titles, []string{"Home", "Start"}) {
		t.Errorf("the draft's nodes are %v", titles)
	}
	if !strings.Contains(string(after["draft/nodes.json"]), "The quay, at night.") {
		t.Errorf("the draft lost the release's text:\n%s", after["draft/nodes.json"])
	}
	exprs := items[authoring.Expression](t, after, "draft/expressions.json")
	if len(exprs) != 1 || exprs[0].Source != "1 + 1 == 2" {
		t.Errorf("the draft's expressions are %+v", exprs)
	}
}

// TestImportContentProfile checks that a content-only archive, which names
// nobody and holds no comments, imports cleanly, as does one without its
// assets, whose references to them the report counts.
//
// Verifies: FR-DATA-01/AC5
func TestImportContentProfile(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	e := askExport(t, f.s.main, f.owner, f.project, map[string]any{"profile": "content", "include_assets": false})
	e = waitForExport(t, f, f.owner, e.ID)
	files := archiveOf(t, f.s.main, f.owner, f.project, e)
	got := mustImport(t, f.s, f.owner, f.ws, zipOf(t, files), "")
	if got.Report.Comments != 0 || got.Report.PlayStates != 0 || got.Report.Assets != 0 ||
		got.Report.MissingAssets != 1 || got.Report.ImportedUsers != 0 {
		t.Errorf("the report is %+v", *got.Report)
	}
}

// TestImportAttribution checks who an archive's people become: a member of
// the workspace keeps their comments, and anyone else, whatever account of the
// instance the archive's ID may name, is an imported identity with the display
// name from the archive, which cannot sign in.
//
// Verifies: FR-DATA-01/AC7
func TestImportAttribution(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	files := exportedArchive(t, f)
	got := mustImport(t, f.s, f.owner, f.ws, zipOf(t, files), "")
	if got.Report.ImportedUsers != 0 {
		t.Errorf("a member's comment took an imported identity: %+v", *got.Report)
	}
	after := exportOf(t, f.s, f.owner, got.ProjectID)
	if c := items[authoring.Comment](t, after, "comments.json"); len(c) != 1 || c[0].AuthorID != f.owner.body.UserID {
		t.Errorf("the member's comment is by %+v", c)
	}

	// Another account's workspace, of which the archive's author is no member.
	newAccount(t, f.s.main, "elsewhere@example.com")
	stranger := signIn(t, f.s.main, "elsewhere@example.com")
	res := call(f.s.main, http.MethodPost, "/api/v1/workspaces", jsonBody(t, map[string]any{"name": "Elsewhere"}),
		stranger.header(true))
	if res.status != http.StatusCreated {
		t.Fatalf("create a workspace: %d %s", res.status, res.body)
	}
	var other struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res.body, &other); err != nil {
		t.Fatal(err)
	}
	moved := mustImport(t, f.s, stranger, other.ID, zipOf(t, files), "remap")
	if moved.Report.ImportedUsers != 1 {
		t.Fatalf("the report is %+v", *moved.Report)
	}
	view := exportOf(t, f.s, stranger, moved.ProjectID)
	people := items[authoring.Person](t, view, "people.json")
	want := items[authoring.Person](t, files, "people.json")
	if len(people) != 1 || people[0].ID == f.owner.body.UserID || people[0].DisplayName != want[0].DisplayName {
		t.Errorf("the imported identity is %+v, want a new ID with the name of %+v", people, want)
	}
	var status string
	if err := f.d.Admin(t).QueryRow(t.Context(), `SELECT status FROM "user" WHERE id = $1`,
		decode(t, ids.User, people[0].ID)).Scan(&status); err != nil || status != "imported" {
		t.Errorf("the identity's account has the status %q: %v", status, err)
	}
}
