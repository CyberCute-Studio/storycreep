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
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/comment"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/maintenance"
	"github.com/CyberCute-Studio/storycreep/server/internal/playstate"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
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

// TestImportAtStorageLimit checks that a workspace without room for an
// archive's assets is refused with its limit, and gets no project.
//
// Verifies: FR-DATA-01/AC9
func TestImportAtStorageLimit(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	data := zipOf(t, exportedArchive(t, f))
	s, d, owner, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_WORKSPACE_LIMITS": "storage_bytes=1"})
	openMaintenance(t, s, d)
	startWorkers(t, s, d)
	failure := failedImport(t, s, owner, ws, data)
	if failure.Code != authoring.FailLimitExceeded || failure.Check != "storage_bytes" {
		t.Errorf("the failure is %+v", *failure)
	}
	if n := projectCount(t, s, owner, ws); n != 0 {
		t.Errorf("the workspace has %d projects", n)
	}
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

// TestImportKeepsAnImportedIdentity checks that an export of a project with an
// imported identity, imported back into the same workspace, takes that identity
// again rather than a second account for the same person and name.
//
// Verifies: FR-DATA-01/AC1
func TestImportKeepsAnImportedIdentity(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
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
	first := mustImport(t, f.s, stranger, other.ID, zipOf(t, exportedArchive(t, f)), "remap")
	view := exportOf(t, f.s, stranger, first.ProjectID)
	people := items[authoring.Person](t, view, "people.json")
	if len(people) != 1 {
		t.Fatalf("the first import names %+v", people)
	}
	second := mustImport(t, f.s, stranger, other.ID, zipOf(t, view), "")
	if second.Report.ImportedUsers != 1 {
		t.Errorf("the report is %+v", *second.Report)
	}
	again := items[authoring.Person](t, exportOf(t, f.s, stranger, second.ProjectID), "people.json")
	if len(again) != 1 || again[0] != people[0] {
		t.Errorf("the second import names %+v, want %+v", again, people)
	}
	var count int
	if err := f.d.Admin(t).QueryRow(t.Context(), `SELECT count(*) FROM imported_user WHERE workspace_id = $1`,
		decode(t, ids.Workspace, other.ID)).Scan(&count); err != nil || count != 1 {
		t.Errorf("the workspace has %d imported identities, want 1: %v", count, err)
	}
}

// TestImportMapsAnEscapedMention checks a comment that mentions an account of
// the instance with its ID's underscore escaped, which a parser reads as the
// ID: it takes an imported identity like any other, so that the archive cannot
// show that account's name.
func TestImportMapsAnEscapedMention(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	newAccount(t, f.s.main, "elsewhere@example.com")
	stranger := signIn(t, f.s.main, "elsewhere@example.com").body.UserID
	files := exportedArchive(t, f)
	list := items[authoring.Comment](t, files, "comments.json")
	list[0].Body = authoring.Text(mentioning(stranger))
	data, err := json.Marshal(authoring.Items[authoring.Comment]{Items: list})
	if err != nil {
		t.Fatal(err)
	}
	escaped := strings.Replace(stranger, "_", string(rune(0x5c))+"u005f", 1)
	resealSection(t, files, "comments.json", bytes.ReplaceAll(data, []byte(stranger), []byte(escaped)), len(list))
	got := mustImport(t, f.s, f.owner, f.ws, zipOf(t, files), "remap")
	if got.Report.ImportedUsers != 1 {
		t.Errorf("the report is %+v, want the identity of the one who is mentioned", *got.Report)
	}
	var body string
	if err := f.d.Admin(t).QueryRow(t.Context(), `SELECT body::text FROM comment WHERE project_id = $1`,
		decode(t, ids.Project, got.ProjectID)).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(body), strings.ToLower(stranger)) {
		t.Errorf("the imported comment still names the account: %s", body)
	}
}

// TestImportHoldsDirectRowsToTheirChecks checks that what an import writes
// without a command is held to what a reader of its row needs, its package's
// structural checks and its bounds: a play state of another project, or that
// names a value it does not hold, a comment's body that does not parse or that
// holds what no comment holds, and a text past its length, or empty where the
// database refuses that, are each the archive's failure.
// TestImportKeepsStoredRowTexts checks what is no longer refused.
//
// Verifies: FR-DATA-01/AC1
func TestImportHoldsDirectRowsToTheirChecks(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	// Each case begins an import, which counts in the export bucket.
	policy := ratelimit.DefaultPolicy()
	policy.Rates[ratelimit.Export].Limits[ratelimit.User] = 1000
	policy.Rates[ratelimit.Export].Limits[ratelimit.IP] = 1000
	f.s.limits = ratelimit.New(readyDB{f.s.ready}, policy)
	exported := exportedArchive(t, f)
	playState := func(edit func(p *authoring.PlayState)) func(map[string][]byte) {
		return func(files map[string][]byte) {
			list := items[authoring.PlayState](t, files, "play_states.json")
			edit(&list[0])
			data, err := json.Marshal(authoring.Items[authoring.PlayState]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "play_states.json", data, len(list))
		}
	}
	mute := func(reason string) func(map[string][]byte) {
		return func(files map[string][]byte) {
			list := []authoring.ValidationMute{{OwnerType: "node", OwnerID: f.node,
				Code: string(validation.Mutable(ids.Node)[0]), Reason: reason}}
			data, err := json.Marshal(authoring.Items[authoring.ValidationMute]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "draft/validation_mutes.json", data, len(list))
		}
	}
	commentBody := func(body string) func(map[string][]byte) {
		return func(files map[string][]byte) {
			list := items[authoring.Comment](t, files, "comments.json")
			list[0].Body = authoring.Text(body)
			data, err := json.Marshal(authoring.Items[authoring.Comment]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "comments.json", data, len(list))
		}
	}
	note := func(text string) func(map[string][]byte) {
		return func(files map[string][]byte) {
			list := items[authoring.Release](t, files, "releases/index.json")
			list[0].Note = text
			data, err := json.Marshal(authoring.Items[authoring.Release]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "releases/index.json", data, len(list))
		}
	}
	valueRef := `{"type":"doc","content":[{"type":"paragraph","content":[` +
		valueInline(ids.Encode(ids.Variable, ids.New())) + `]}]}`
	stateWith := func(project string) authoring.Value {
		state := emptyState(project)
		state["variables"] = map[string]any{ids.Encode(ids.Variable, ids.New()): 1}
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		return authoring.Value(data)
	}
	for name, c := range map[string]struct {
		forge             func(files map[string][]byte)
		code, section, at string
	}{
		"a play state of another project": {playState(func(p *authoring.PlayState) {
			p.State = bytes.ReplaceAll(p.State, []byte(f.project), []byte(ids.Encode(ids.Project, ids.New())))
		}), authoring.FailContentInvalid, "play_states", "state.projectId"},
		"a play state's name past its length": {playState(func(p *authoring.PlayState) {
			p.Name = strings.Repeat("a", playstate.MaxNameLength+1)
		}), authoring.FailContentInvalid, "play_states", "name"},
		"a play state without a name": {playState(func(p *authoring.PlayState) { p.Name = "" }),
			authoring.FailContentInvalid, "play_states", "name"},
		"a name of a value that the state does not hold": {playState(func(p *authoring.PlayState) {
			p.Names = authoring.Value(`{"variables":{"` + ids.Encode(ids.Variable, ids.New()) + `":"gold"}}`)
		}), authoring.FailContentInvalid, "play_states", "names.variables."},
		"a value's name past its length": {playState(func(p *authoring.PlayState) {
			p.State = stateWith(f.project)
			var st struct {
				Variables map[string]any `json:"variables"`
			}
			if err := json.Unmarshal(p.State, &st); err != nil {
				t.Fatal(err)
			}
			for v := range st.Variables {
				p.Names = authoring.Value(`{"variables":{"` + v + `":"` +
					strings.Repeat("a", playstate.MaxValueNameLength+1) + `"}}`)
			}
		}), authoring.FailContentInvalid, "play_states", "names.variables."},
		"a mute's reason past its length": {mute(strings.Repeat("a", validation.MaxReasonLength+1)),
			authoring.FailContentInvalid, "validation_mutes", "reason"},
		"a mute without a reason": {mute(""), authoring.FailContentInvalid, "validation_mutes", "reason"},
		"a comment's body that does not parse": {commentBody(string(paragraph("reversed" + string(rune(0x202e))))),
			authoring.FailContentInvalid, "comments", "body"},
		"a comment's body with a value reference": {commentBody(valueRef),
			authoring.FailContentInvalid, "comments", "body"},
		"a comment's body past its length": {commentBody(string(paragraph(
			strings.Repeat("a", comment.MaxBodyLength+1)))), authoring.FailContentInvalid, "comments", "body"},
		"a release's note past its length": {note(strings.Repeat("a", snapshot.MaxNoteLength+1)),
			authoring.FailReleaseInvalid, "", "note"},
		"a release without a note": {note(""), authoring.FailReleaseInvalid, "", "note"},
		"a project without a name": {func(files map[string][]byte) {
			var p authoring.Project
			if err := json.Unmarshal(files["draft/project.json"], &p); err != nil {
				t.Fatal(err)
			}
			p.Name = ""
			data, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "draft/project.json", data, 1)
		}, authoring.FailContentInvalid, "project", "name"},
		"two tags the same but for their case": {func(files map[string][]byte) {
			list := items[authoring.Node](t, files, "draft/nodes.json")
			list[0].Tags = []string{"Quest", "quest"}
			data, err := json.Marshal(authoring.Items[authoring.Node]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "draft/nodes.json", data, len(list))
		}, authoring.FailContentInvalid, "nodes", "tags[1]"},
		"a component's string past its type's length": {func(files map[string][]byte) {
			components := items[authoring.Component](t, files, "draft/components.json")
			types := items[authoring.ComponentType](t, files, "draft/component_types.json")
			def := ids.Encode(ids.AttributeDef, ids.New())
			types[0].Attributes = append(types[0].Attributes, authoring.AttributeDef{ID: def,
				Order: components[0].Order, Key: "motto", Name: "Motto", Type: value.String,
				Default: authoring.Value(`""`)})
			long, err := json.Marshal(strings.Repeat("a", value.MaxStringLength+1))
			if err != nil {
				t.Fatal(err)
			}
			components[0].Values = map[string]authoring.Value{def: authoring.Value(long)}
			data, err := json.Marshal(authoring.Items[authoring.ComponentType]{Items: types})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "draft/component_types.json", data, len(types))
			if data, err = json.Marshal(authoring.Items[authoring.Component]{Items: components}); err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "draft/components.json", data, len(components))
		}, authoring.FailContentInvalid, "components", "attributes."},
		"a variable's default past its type's length, which only the database holds": {func(files map[string][]byte) {
			list := items[authoring.Variable](t, files, "draft/variables.json")
			long, err := json.Marshal(strings.Repeat("a", value.MaxStringLength+1))
			if err != nil {
				t.Fatal(err)
			}
			list = append(list, authoring.Variable{ID: ids.Encode(ids.Variable, ids.New()), Scope: "global", Order: "V",
				Key: "motto", Type: value.String, Default: authoring.Value(long)})
			data, err := json.Marshal(authoring.Items[authoring.Variable]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "draft/variables.json", data, len(list))
		}, authoring.FailContentInvalid, "variables", "variable_check2"},
		"a release's report that the API cannot read": {func(files map[string][]byte) {
			list := items[authoring.Release](t, files, "releases/index.json")
			list[0].ValidationReport = authoring.Value(`{"findings": "none"}`)
			data, err := json.Marshal(authoring.Items[authoring.Release]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "releases/index.json", data, len(list))
		}, authoring.FailReleaseInvalid, "", "validation_report"},
		"a release's sequence past what a client reads": {func(files map[string][]byte) {
			list := items[authoring.Release](t, files, "releases/index.json")
			list[0].SourceSeq = 1 << 53
			data, err := json.Marshal(authoring.Items[authoring.Release]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "releases/index.json", data, len(list))
		}, authoring.FailSchemaInvalid, "releases", "schema"},
		"a release listed twice": {func(files map[string][]byte) {
			list := items[authoring.Release](t, files, "releases/index.json")
			list = append(list, list[0])
			data, err := json.Marshal(authoring.Items[authoring.Release]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "releases/index.json", data, len(list))
		}, authoring.FailContentInvalid, "releases", "duplicate_release"},
		"two assets of one file": {func(files map[string][]byte) {
			list := items[authoring.Asset](t, files, "assets/index.json")
			again := list[0]
			again.ID = ids.Encode(ids.Asset, ids.New())
			list = append(list, again)
			data, err := json.Marshal(authoring.Items[authoring.Asset]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "assets/index.json", data, len(list))
		}, authoring.FailAssetInvalid, "assets", "duplicate_path"},
	} {
		t.Run(name, func(t *testing.T) {
			files := maps.Clone(exported)
			c.forge(files)
			failure := failedImport(t, f.s, f.owner, f.ws, zipOf(t, files))
			// A check that ends in a dot is of a field whose name goes on with
			// an ID that the case makes.
			at := failure.Check == c.at || strings.HasSuffix(c.at, ".") && strings.HasPrefix(failure.Check, c.at)
			if failure.Code != c.code || failure.Section != c.section || !at {
				t.Errorf("the failure is %+v, want %s at %s, %s", *failure, c.code, c.section, c.at)
			}
		})
	}
}

// TestImportTakesCountsFromTheRelease checks that a release's row counts what
// its verified files hold, whatever the index of the archive says, and that the
// report names the project as it is stored.
//
// Verifies: FR-DATA-01/AC1
func TestImportTakesCountsFromTheRelease(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	files := exportedArchive(t, f)
	releases := items[authoring.Release](t, files, "releases/index.json")
	want := releases[0].Counts
	releases[0].Counts = authoring.Value(`{"boards":0,"components":0,"connections":0,"nodes":0,"variables":0}`)
	data, err := json.Marshal(authoring.Items[authoring.Release]{Items: releases})
	if err != nil {
		t.Fatal(err)
	}
	resealSection(t, files, "releases/index.json", data, len(releases))
	var p authoring.Project
	if err := json.Unmarshal(files["draft/project.json"], &p); err != nil {
		t.Fatal(err)
	}
	p.Name = " Harbour "
	if data, err = json.Marshal(p); err != nil {
		t.Fatal(err)
	}
	resealSection(t, files, "draft/project.json", data, 1)
	got := mustImport(t, f.s, f.owner, f.ws, zipOf(t, files), authoring.PolicyRemap)
	if got.Report.ProjectName != p.Name {
		t.Errorf("the report names the project %q, want %q", got.Report.ProjectName, p.Name)
	}
	stored := items[authoring.Release](t, exportOf(t, f.s, f.owner, got.ProjectID), "releases/index.json")
	if len(stored) != 1 || !sameJSON(t, stored[0].Counts, want) {
		t.Errorf("the release's counts are %s, want %s", stored[0].Counts, want)
	}
}

// TestImportHoldsRowsToTheirReaders checks that what the API could not read
// back is the archive's failure: a time past JSON's years in UTC, a report's
// number past a float64, values of the wrong type, and more assets than the
// lock table of the database should hold for one transaction.
//
// Verifies: FR-DATA-01/AC1, NFR-SEC-13
func TestImportHoldsRowsToTheirReaders(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	exported := exportedArchive(t, f)
	late := time.Date(9999, 12, 31, 23, 59, 59, 0, time.FixedZone("", -(23*3600+59*60)))
	release := func(edit func(r *authoring.Release)) func(map[string][]byte) {
		return func(files map[string][]byte) {
			list := items[authoring.Release](t, files, "releases/index.json")
			edit(&list[0])
			data, err := json.Marshal(authoring.Items[authoring.Release]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "releases/index.json", data, len(list))
		}
	}
	for name, c := range map[string]struct {
		forge             func(files map[string][]byte)
		code, section, at string
	}{
		"a release's time": {release(func(r *authoring.Release) { r.PublishedAt = late }),
			authoring.FailReleaseInvalid, "", "published_at"},
		"a comment's time": {func(files map[string][]byte) {
			list := items[authoring.Comment](t, files, "comments.json")
			list[0].EditedAt = &late
			data, err := json.Marshal(authoring.Items[authoring.Comment]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "comments.json", data, len(list))
		}, authoring.FailContentInvalid, "comments", "time"},
		"a play state's time": {func(files map[string][]byte) {
			list := items[authoring.PlayState](t, files, "play_states.json")
			list[0].CreatedAt = late
			data, err := json.Marshal(authoring.Items[authoring.PlayState]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "play_states.json", data, len(list))
		}, authoring.FailContentInvalid, "play_states", "time"},
		"a report's number": {release(func(r *authoring.Release) {
			r.ValidationReport = authoring.Value(`{"seq":1,"findings":[{"code":"node_empty","entity_type":"node",` +
				`"entity_id":"` + f.node + `","args":{"n":1e999}}]}`)
		}), authoring.FailReleaseInvalid, "", "validation_report"},
		"acknowledged warnings that are no list": {release(func(r *authoring.Release) {
			r.AcknowledgedWarnings = authoring.Value(`{"code":"node_empty"}`)
		}), authoring.FailReleaseInvalid, "", "acknowledged_warnings"},
		"counts of the wrong type": {release(func(r *authoring.Release) {
			r.Counts = authoring.Value(`{"boards":"many"}`)
		}), authoring.FailReleaseInvalid, "", "counts"},
		"too many assets": {func(files map[string][]byte) {
			list := items[authoring.Asset](t, files, "assets/index.json")
			first := list[0]
			for i := range 4000 {
				more := first
				more.ID, more.Path = ids.Encode(ids.Asset, ids.New()), fmt.Sprintf("assets/more-%d.png", i)
				list = append(list, more)
			}
			data, err := json.Marshal(authoring.Items[authoring.Asset]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "assets/index.json", data, len(list))
		}, authoring.FailAssetInvalid, "assets", "count"},
	} {
		t.Run(name, func(t *testing.T) {
			files := maps.Clone(exported)
			c.forge(files)
			failure := failedImport(t, f.s, f.owner, f.ws, zipOf(t, files))
			if failure.Code != c.code || failure.Section != c.section || failure.Check != c.at {
				t.Errorf("the failure is %+v, want %s at %s, %s", *failure, c.code, c.section, c.at)
			}
		})
	}
}

// TestImportStoresWhatItChecked checks a key spelled twice in two cases, of
// which jsonb's order of keys would have a later read take the one that the
// import did not check: a report's mute of an account elsewhere on the
// instance, and a play state of another project.
//
// Verifies: FR-DATA-01/AC7
func TestImportStoresWhatItChecked(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	files := exportedArchive(t, f)
	newAccount(t, f.s.main, "elsewhere@example.com")
	foreign := signIn(t, f.s.main, "elsewhere@example.com").body.UserID

	releases := items[authoring.Release](t, files, "releases/index.json")
	releases[0].ValidationReport = authoring.Value(`{"seq":1,"findings":[{"code":"node_unreachable",` +
		`"entity_type":"node","entity_id":"` + f.node + `","mute":{"reason":"r","muted_by":"` + foreign +
		`","muted_at":"2026-01-01T00:00:00Z"}}],"FINDINGS":[]}`)
	data, err := json.Marshal(authoring.Items[authoring.Release]{Items: releases})
	if err != nil {
		t.Fatal(err)
	}
	resealSection(t, files, "releases/index.json", data, len(releases))

	other := ids.Encode(ids.Project, ids.New())
	states := items[authoring.PlayState](t, files, "play_states.json")
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(states[0].State, &doc); err != nil {
		t.Fatal(err)
	}
	own := doc["projectId"]
	delete(doc, "projectId")
	rest, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	states[0].State = append([]byte(`{"projectId":"`+other+`","PROJECTID":`+string(own)+`,`),
		bytes.TrimPrefix(rest, []byte("{"))...)
	data, err = json.Marshal(authoring.Items[authoring.PlayState]{Items: states})
	if err != nil {
		t.Fatal(err)
	}
	resealSection(t, files, "play_states.json", data, len(states))

	got := mustImport(t, f.s, f.owner, f.ws, zipOf(t, files), "")
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	res := call(f.s.main, http.MethodGet, snapshotsPath(got.ProjectID), nil, f.owner.header(false))
	if err := json.Unmarshal(res.body, &page); err != nil || len(page.Items) == 0 {
		t.Fatalf("the releases: %d %s", res.status, res.body)
	}
	report := call(f.s.main, http.MethodGet, snapshotsPath(got.ProjectID)+"/"+page.Items[0].ID+"/validation", nil,
		f.owner.header(false))
	if report.status != http.StatusOK || strings.Contains(string(report.body), foreign) {
		t.Errorf("the report: %d %s", report.status, report.body)
	}
	res = call(f.s.main, http.MethodGet, playStatesPath(got.ProjectID), nil, f.owner.header(false))
	page.Items = nil
	if err := json.Unmarshal(res.body, &page); err != nil || len(page.Items) == 0 {
		t.Fatalf("the play states: %d %s", res.status, res.body)
	}
	state := call(f.s.main, http.MethodGet, playStatesPath(got.ProjectID)+"/"+page.Items[0].ID, nil,
		f.owner.header(false))
	if state.status != http.StatusOK || strings.Contains(string(state.body), other) {
		t.Errorf("the play state: %d %s", state.status, state.body)
	}
}

// TestImportRefusesAReleaseOfAnotherName checks a release whose index entry
// says a version that its sealed manifest does not.
//
// Verifies: FR-DATA-01/AC11
func TestImportRefusesAReleaseOfAnotherName(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	files := exportedArchive(t, f)
	list := items[authoring.Release](t, files, "releases/index.json")
	list[0].Version = "2.0.0"
	index, err := json.Marshal(authoring.Items[authoring.Release]{Items: list})
	if err != nil {
		t.Fatal(err)
	}
	resealSection(t, files, "releases/index.json", index, len(list))
	failure := failedImport(t, f.s, f.owner, f.ws, zipOf(t, files))
	if failure.Code != authoring.FailReleaseInvalid || failure.Check != "snapshot" || failure.Version != "2.0.0" {
		t.Errorf("the failure is %+v", *failure)
	}
}

// TestImportRefusesBadProjectFields checks that a project's field that a new
// project is refused for is the archive's failure, which names the section,
// and not the server's.
func TestImportRefusesBadProjectFields(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	files := exportedArchive(t, f)
	var p authoring.Project
	if err := json.Unmarshal(files["draft/project.json"], &p); err != nil {
		t.Fatal(err)
	}
	p.Settings = authoring.Value(`{"default_locale": "not a locale!"}`)
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	resealSection(t, files, "draft/project.json", data, 1)
	failure := failedImport(t, f.s, f.owner, f.ws, zipOf(t, files))
	if failure.Code != authoring.FailContentInvalid || failure.Section != "project" {
		t.Errorf("the failure is %+v", *failure)
	}
}

// TestImportFileHoldsNoRow checks that the command line's import leaves no
// import on its way, so that one that fails, or a process that dies, keeps the
// user from no other, and that one that succeeds is recorded as ready.
func TestImportFileHoldsNoRow(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	actor := commands.Actor{UserID: decode(t, ids.User, f.owner.body.UserID)}
	ws := decode(t, ids.Workspace, f.ws)
	bad := []byte("not a zip archive")
	refused, err := f.s.authoring.ImportFile(t.Context(), actor, ws, "bad.zip", bytes.NewReader(bad), int64(len(bad)),
		false)
	if err != nil || refused.Failure == nil || refused.Failure.Code != authoring.FailArchiveInvalid {
		t.Fatalf("the refused import is %+v: %v", refused, err)
	}
	var rows int
	if err := f.d.Admin(t).QueryRow(t.Context(), `SELECT count(*) FROM project_import`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("a refused import of the command line left %d rows", rows)
	}
	data := zipOf(t, exportedArchive(t, f))
	done, err := f.s.authoring.ImportFile(t.Context(), actor, ws, "harbour.zip", bytes.NewReader(data), int64(len(data)),
		true)
	if err != nil || done.Status != authoring.ImportReady || done.Report == nil || done.Report.Policy != authoring.PolicyRemap {
		t.Fatalf("the import is %+v: %v", done, err)
	}
	if got := readImport(t, f.s.main, f.owner, f.ws, done.ID); got.Status != authoring.ImportReady ||
		got.ProjectID != done.Report.ProjectID {
		t.Errorf("the recorded import is %+v", got)
	}
}

// TestImportFindsItsComments checks that a search finds an imported comment
// from the import's commit on, without waiting for the job of the index.
func TestImportFindsItsComments(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	got := mustImport(t, f.s, f.owner, f.ws, zipOf(t, exportedArchive(t, f)), "remap")
	var documents int
	if err := f.d.Admin(t).QueryRow(t.Context(),
		`SELECT count(*) FROM search_entry WHERE project_id = $1 AND kind = 'comment'`,
		decode(t, ids.Project, got.ProjectID)).Scan(&documents); err != nil {
		t.Fatal(err)
	}
	if documents != got.Report.Comments || documents == 0 {
		t.Errorf("the index holds %d documents of the %d imported comments", documents, got.Report.Comments)
	}
}

// TestImportChunkSentAgainIsANewBlob checks that a chunk sent again is stored
// under a key of its own, and that the row names it, so that a blob that a row
// has named never changes, whatever a request that outlives the upload does.
//
// Verifies: FR-DATA-01/AC4
func TestImportChunkSentAgainIsANewBlob(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	data := zipOf(t, exportedArchive(t, f))
	i := beginImport(t, f.s.main, f.owner, f.ws, len(data), "remap")
	key := func() string {
		t.Helper()
		var k string
		if err := f.d.Admin(t).QueryRow(t.Context(),
			`SELECT storage_key FROM project_import_chunk WHERE import_id = $1 AND chunk_index = 0`,
			decode(t, ids.Job, i.ID)).Scan(&k); err != nil {
			t.Fatal(err)
		}
		return k
	}
	putChunks(t, f.s.main, f.owner, f.ws, i, data, 0)
	first := key()
	putChunks(t, f.s.main, f.owner, f.ws, i, data, 0)
	if second := key(); second == first {
		t.Errorf("the chunk sent again kept the key %s", first)
	}
}

// TestImportRefusesTooManyPeople checks that an archive cannot add an account
// of the instance for each of a great many people that it invents.
//
// Verifies: NFR-SEC-13
func TestImportRefusesTooManyPeople(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	files := exportedArchive(t, f)
	root := items[authoring.Comment](t, files, "comments.json")[0]
	const n = 10_001
	list := make([]authoring.Comment, n)
	for i := range list {
		c := root
		c.ID = ids.Encode(ids.Comment, ids.New())
		c.ThreadID, c.AuthorID = c.ID, ids.Encode(ids.User, ids.New())
		list[i] = c
	}
	data, err := json.Marshal(authoring.Items[authoring.Comment]{Items: list})
	if err != nil {
		t.Fatal(err)
	}
	resealSection(t, files, "comments.json", data, n)
	// A workspace of another account, of which none of them is a member.
	newAccount(t, f.s.main, "many@example.com")
	stranger := signIn(t, f.s.main, "many@example.com")
	res := call(f.s.main, http.MethodPost, "/api/v1/workspaces", jsonBody(t, map[string]any{"name": "Many"}),
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
	var before, after int
	admin := f.d.Admin(t)
	if err := admin.QueryRow(t.Context(), `SELECT count(*) FROM "user"`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got := failedImport(t, f.s, stranger, other.ID, zipOf(t, files))
	if got.Code != authoring.FailContentInvalid || got.Check != "people" {
		t.Errorf("the failure is %+v, want content_invalid at people", got)
	}
	if err := admin.QueryRow(t.Context(), `SELECT count(*) FROM "user"`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("the refused import left %d accounts, and there were %d", after, before)
	}
}

// TestImportRecordsAnyRefusal checks that a refusal that repeats what an archive
// spells is recorded whatever that is, so that the import ends and its user can
// start another: PostgreSQL's jsonb takes no NUL, and a refusal's text is read
// back with every poll of the import.
//
// Verifies: NFR-SEC-13
func TestImportRecordsAnyRefusal(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	exported := exportedArchive(t, f)
	// Of IDs, as a text that compresses well is the zip guards' to refuse.
	var long strings.Builder
	for long.Len() < 1<<20 {
		long.WriteString(ids.New().String())
	}
	for name, path := range map[string]string{
		"a NUL":       "elsewhere/a\x00b.png",
		"a long text": "elsewhere/" + long.String(),
	} {
		t.Run(name, func(t *testing.T) {
			files := maps.Clone(exported)
			list := items[authoring.Asset](t, files, "assets/index.json")
			list[0].Path = path
			data, err := json.Marshal(authoring.Items[authoring.Asset]{Items: list})
			if err != nil {
				t.Fatal(err)
			}
			resealSection(t, files, "assets/index.json", data, len(list))
			got := failedImport(t, f.s, f.owner, f.ws, zipOf(t, files))
			if got.Code != authoring.FailAssetInvalid || got.Check != "path" {
				t.Errorf("the failure is %+v, want asset_invalid at path", *got)
			}
			if strings.ContainsRune(got.Detail, 0) || len(got.Detail) > 2048 {
				t.Errorf("the failure's detail has %d bytes, or a NUL", len(got.Detail))
			}
		})
	}
}

// TestImportHoldsTheManifestToItsAssets checks that a manifest that says the
// archive holds no assets cannot also list them, which would store the assets
// and clear every reference to them.
func TestImportHoldsTheManifestToItsAssets(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	files := exportedArchive(t, f)
	var m authoring.Manifest
	if err := json.Unmarshal(files[authoring.ManifestPath], &m); err != nil {
		t.Fatal(err)
	}
	m.IncludesAssets = false
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	files[authoring.ManifestPath] = data
	got := failedImport(t, f.s, f.owner, f.ws, zipOf(t, files))
	if got.Code != authoring.FailSchemaInvalid || got.Section != "assets" || got.Check != "includes_assets" {
		t.Errorf("the failure is %+v, want schema_invalid of assets at includes_assets", *got)
	}
}

// TestImportTakesNoAccountForADeletedAuthor checks that the author of a deleted
// comment, which keeps no author, adds no imported identity.
//
// Verifies: FR-DATA-01/AC7
func TestImportTakesNoAccountForADeletedAuthor(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	files := exportedArchive(t, f)
	list := items[authoring.Comment](t, files, "comments.json")
	gone := ids.Encode(ids.User, ids.New())
	list[0].Deleted, list[0].AuthorID, list[0].Body = true, gone, nil
	data, err := json.Marshal(authoring.Items[authoring.Comment]{Items: list})
	if err != nil {
		t.Fatal(err)
	}
	resealSection(t, files, "comments.json", data, len(list))
	people := items[authoring.Person](t, files, "people.json")
	people = append(people, authoring.Person{ID: gone, DisplayName: "Gone"})
	data, err = json.Marshal(authoring.Items[authoring.Person]{Items: people})
	if err != nil {
		t.Fatal(err)
	}
	resealSection(t, files, "people.json", data, len(people))
	mustImport(t, f.s, f.owner, f.ws, zipOf(t, files), "remap")
	var named int
	if err := f.d.Admin(t).QueryRow(t.Context(), `SELECT count(*) FROM imported_user WHERE source_user_id = $1`,
		decode(t, ids.User, gone)).Scan(&named); err != nil {
		t.Fatal(err)
	}
	if named != 0 {
		t.Errorf("the deleted comment's author took %d imported identities", named)
	}
}

// TestImportAuditsItsClient checks that the audit entry of an import that a job
// writes names the client, and the request, that handed the upload to the job.
//
// Verifies: FR-TEN-07/AC2
func TestImportAuditsItsClient(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	data := zipOf(t, exportedArchive(t, f))
	i := beginImport(t, f.s.main, f.owner, f.ws, len(data), "")
	putChunks(t, f.s.main, f.owner, f.ws, i, data)
	res := send(f.s.main, http.MethodPost, importsPath(f.ws)+"/"+i.ID+"/complete", nil, "", with(f.owner.header(true),
		http.Header{"User-Agent": {"Importer/1.0"}, http.CanonicalHeaderKey(requestIDHeader): {"import-audit"}}))
	if res.status != http.StatusAccepted {
		t.Fatalf("complete the import: %d %s", res.status, res.body)
	}
	var got ProjectImportBody
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		got = readImport(t, f.s.main, f.owner, f.ws, i.ID)
		if got.Status == authoring.ImportReady {
			break
		}
		if got.Status == authoring.ImportFailed || time.Now().After(deadline) {
			t.Fatalf("the import ended as %+v", got)
		}
	}
	var ok bool
	if err := f.d.Admin(t).QueryRow(t.Context(), `
		SELECT host(ip) = '192.0.2.1' AND user_agent = 'Importer/1.0' AND request_id = 'import-audit'
		FROM audit_entry WHERE action = 'project.import' AND subject_id = $1`,
		decode(t, ids.Project, got.ProjectID)).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("the import's audit entry does not name the client that asked for it")
	}
}

// TestImportTakesAnElseOlderThanItsOutputs checks a branch whose else has a
// lower ID than every conditional output that it has left, as one has once the
// output that its else was added after is deleted. An archive lists outputs by
// ID, and an else needs a conditional output to come after.
//
// Verifies: FR-DATA-01/AC1
func TestImportTakesAnElseOlderThanItsOutputs(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	branch := createNode(t, f.s.main, f.owner, f.project, f.board, "branch", nil)
	made := []ids.UUID{ids.New(), ids.New(), ids.New()}
	slices.SortFunc(made, func(a, b ids.UUID) int { return bytes.Compare(a[:], b[:]) })
	first, otherwise, second := ids.Encode(ids.BranchOutput, made[0]), ids.Encode(ids.BranchOutput, made[1]),
		ids.Encode(ids.BranchOutput, made[2])
	sendBatch(t, f.s.main, f.owner, f.project,
		wireCommand{Type: "branch_output.create", Payload: map[string]any{"id": first, "node_id": branch,
			"condition": "true", "order": "T"}},
		wireCommand{Type: "branch_output.create", Payload: map[string]any{"id": otherwise, "node_id": branch,
			"else": true}},
		wireCommand{Type: "branch_output.create", Payload: map[string]any{"id": second, "node_id": branch,
			"condition": "false", "order": "U"}},
		wireCommand{Type: "branch_output.delete", Payload: map[string]any{"id": first}})
	before := exportedArchive(t, f)
	got := mustImport(t, f.s, f.owner, f.ws, zipOf(t, before), "")
	after := exportOf(t, f.s, f.owner, got.ProjectID)
	const path = "draft/branch_outputs.json"
	if a, b := semantics(t, before[path]), semantics(t, after[path]); !slices.Equal(a, b) {
		t.Errorf("the branch's outputs changed:\nbefore %v\nafter  %v", a, b)
	}
}
