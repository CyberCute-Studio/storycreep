package httpserver

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/authoring"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/sample"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
)

func samplePath(ws string) string { return workspacesPath + "/" + ws + "/sample-project" }

// openSample opens the sample project in the workspace as si, and returns it.
func openSample(t *testing.T, h http.Handler, si signedIn, ws string) projectBody {
	t.Helper()
	res := call(h, http.MethodPost, samplePath(ws), nil, si.header(true))
	p := projectFrom(t, res, http.StatusCreated)
	if want := "/api/v1/projects/" + p.ID; res.header.Get("Location") != want {
		t.Errorf("Location is %q, want %q", res.header.Get("Location"), want)
	}
	return p
}

// filesOf returns the members of the zip data by their paths.
func filesOf(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = b
	}
	return out
}

var (
	digest      = regexp.MustCompile(`\b[0-9a-f]{64}\b`)
	assetFile   = regexp.MustCompile(`\bassets/([0-9A-Z]{26})\b`)
	exportedAt  = regexp.MustCompile(`"exportedAt": "[^"]*"`)
	madeAt      = regexp.MustCompile(`It was made at \S+\.`)
	generatorAt = regexp.MustCompile(`("generator": \{[^}]*"version": ")[^"]*(")`)
)

// normalised returns the files of an archive, or of a published export, as
// they are after normalisation (PRD FR-PROJ-05 AC2, FR-SNAP-03 AC2), by their
// paths, which are normalised too: each public ID, but a user's, which an
// import keeps, and an asset's in its file's path, as its rank among the
// files' IDs, which an import's remap keeps, as it gives fresh IDs in the
// order of those that they replace; each
// SHA-256 digest, which is of what holds the IDs, as one mark; and when the
// archive was made, and by which version of the server, as one mark each.
// Whatever else differs is a difference of the files.
func normalised(t *testing.T, files map[string][]byte) map[string]string {
	t.Helper()
	seen := map[string]bool{}
	for name, data := range files {
		for _, s := range []string{name, string(data)} {
			for _, id := range publicID.FindAllString(s, -1) {
				if !strings.HasPrefix(id, "usr_") {
					seen[id] = true
				}
			}
			for _, m := range assetFile.FindAllStringSubmatch(s, -1) {
				seen[ids.Asset.Prefix()+"_"+m[1]] = true
			}
		}
	}
	list := slices.Collect(maps.Keys(seen))
	uuidOf := func(id string) ids.UUID {
		prefix, _, _ := strings.Cut(id, "_")
		for k := ids.Kind(1); k.String() != fmt.Sprintf("Kind(%d)", k); k++ {
			if k.Prefix() == prefix {
				return decode(t, k, id)
			}
		}
		t.Fatalf("no kind of ID has the prefix of %s", id)
		return ids.UUID{}
	}
	slices.SortFunc(list, func(a, b string) int {
		x, y := uuidOf(a), uuidOf(b)
		return bytes.Compare(x[:], y[:])
	})
	rank := make(map[string]string, len(list))
	for i, id := range list {
		rank[id] = fmt.Sprintf("<id %d>", i)
	}
	clean := func(s string) string {
		s = publicID.ReplaceAllStringFunc(s, func(id string) string {
			if r, ok := rank[id]; ok {
				return r
			}
			return id
		})
		s = assetFile.ReplaceAllStringFunc(s, func(path string) string {
			return "assets/" + rank[ids.Asset.Prefix()+"_"+strings.TrimPrefix(path, "assets/")]
		})
		s = digest.ReplaceAllString(s, "<digest>")
		s = exportedAt.ReplaceAllString(s, `"exportedAt": "<time>"`)
		s = madeAt.ReplaceAllString(s, "It was made at <time>.")
		return generatorAt.ReplaceAllString(s, "${1}<version>${2}")
	}
	out := make(map[string]string, len(files))
	for name, data := range files {
		out[clean(name)] = clean(string(data))
	}
	return out
}

// sameFiles fails the test unless a and b, normalised archives, hold the same
// files, byte for byte.
func sameFiles(t *testing.T, what string, a, b map[string]string) {
	t.Helper()
	if x, y := slices.Sorted(maps.Keys(a)), slices.Sorted(maps.Keys(b)); !slices.Equal(x, y) {
		t.Fatalf("%s: the files are %v, and %v", what, x, y)
	}
	for _, name := range slices.Sorted(maps.Keys(a)) {
		if a[name] != b[name] {
			t.Errorf("%s: %s differs:\n%s\n----\n%s", what, name, a[name], b[name])
		}
	}
}

// exportAs exports the project as si, with the body's options, on a server
// whose workers run, and returns its files.
func exportAs(t *testing.T, s *server, si signedIn, project string, body map[string]any) map[string][]byte {
	t.Helper()
	e := askExport(t, s.main, si, project, body)
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(50 * time.Millisecond) {
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

// sampleManifest returns the manifest of the sample's archive.
func sampleManifest(t *testing.T) authoring.Manifest {
	t.Helper()
	var m authoring.Manifest
	if err := json.Unmarshal(filesOf(t, sample.Archive())[authoring.ManifestPath], &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestSampleProject checks the sample project as one click opens it: a new
// project of the workspace each time, with fresh IDs, of three boards, about
// forty nodes, six variables, four components, a quest, and choices gated
// across boards, which validates without an error and with the one warning
// that the sample has on purpose, to show the validation panel, and whose
// import is recorded as the sample's.
//
// Verifies: FR-PROJ-05/AC1
func TestSampleProject(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := openSample(t, h, owner, ws)
	if p.Name != "The Lighthouse Keeper" || p.Slug != "the-lighthouse-keeper" || p.Role != "owner" ||
		p.DuplicatedFrom != "" || p.Entry.BoardID == "" || p.Entry.NodeID == "" {
		t.Errorf("the sample opened as %+v", p)
	}
	if m := sampleManifest(t); p.ID == m.Project.ID {
		t.Errorf("the sample kept its archive's ID %s", p.ID)
	}
	r := validationReport(t, h, owner, p.ID)
	if r.Counts.Errors != 0 || r.Counts.Warnings < 1 {
		t.Errorf("the sample's report counts %+v, want no error and a warning at least", r.Counts)
	}
	var codes []string
	for _, f := range r.Findings {
		codes = append(codes, fmt.Sprintf("%s %s %s", f.Severity, f.Code, f.EntityType))
	}
	if want := []string{"warning var_written_never_read variable"}; !slices.Equal(codes, want) {
		t.Errorf("the sample's findings are %v, want its deliberate warning alone, %v", codes, want)
	}
	if len(r.Findings) == 1 {
		v := variableFrom(t, call(h, http.MethodGet, variablePath(p.ID, r.Findings[0].EntityID), nil,
			owner.header(false)), http.StatusOK)
		if v.Key != "heard_rumour" {
			t.Errorf("the deliberate warning is of %s, want heard_rumour", v.Key)
		}
	}
	for what, want := range map[string]int{"boards": 3, "nodes": 40, "variables": 6, "components": 4} {
		table := map[string]string{"boards": "board", "nodes": "node", "variables": "variable",
			"components": "component"}[what]
		if got := countRows(t, d, `SELECT count(*) FROM `+table+` WHERE project_id = $1::uuid`,
			decode(t, ids.Project, p.ID).String()); got != want {
			t.Errorf("the sample has %d %s, want %d", got, what, want)
		}
	}
	// The quest, and gating across boards: a choice of the harbourmaster's
	// board gated on a visit of a node of Elara's.
	if n := countRows(t, d, `SELECT count(*) FROM variable WHERE project_id = $1::uuid AND key = 'lamp_quest'`,
		decode(t, ids.Project, p.ID).String()); n != 1 {
		t.Errorf("the sample has %d quest variables, want 1", n)
	}
	if n := countRows(t, d, `SELECT count(*) FROM expression_ref WHERE project_id = $1::uuid
		AND target_kind = 'node'`, decode(t, ids.Project, p.ID).String()); n != 1 {
		t.Errorf("the sample has %d visit references, want 1", n)
	}
	again := openSample(t, h, owner, ws)
	if again.ID == p.ID {
		t.Errorf("opening the sample again gave the same project")
	}
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'project.import'
		AND subject_id = $1::uuid AND metadata->>'sample' = 'the-lighthouse-keeper'
		AND metadata->>'id_policy' = 'remap' AND NOT metadata ? 'import_id'`,
		decode(t, ids.Project, p.ID).String()); n != 1 {
		t.Errorf("the sample's import has %d audit entries, want 1", n)
	}
}

// TestSampleRoundTrip checks that the sample is an export archive, which an
// export of the project that it imports as gives back byte for byte, after
// normalisation of its IDs, its digests, and when and by which server it was
// made.
//
// Verifies: FR-PROJ-05/AC2
func TestSampleRoundTrip(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	startWorkers(t, s, d)
	stored := filesOf(t, sample.Archive())
	validateArchive(t, stored)
	m := sampleManifest(t)
	if m.Source.Kind != "draft" || m.Profile != authoring.ProfileContent || m.IncludesAssets {
		t.Fatalf("the sample's archive is of %+v, %s, assets %v, want a content-only draft without assets",
			m.Source, m.Profile, m.IncludesAssets)
	}
	p := openSample(t, s.main, owner, ws)
	exported := exportAs(t, s, owner, p.ID, map[string]any{"profile": string(m.Profile),
		"include_assets": m.IncludesAssets, "include_releases": m.IncludesReleases})
	sameFiles(t, "the sample and its export", normalised(t, stored), normalised(t, exported))
}

// TestSampleDependencyGraph checks the dependency view of the sample against
// its fixture graph: every board with its badge's counts, and every edge with
// its kind and its contributions.
//
// Verifies: FR-DEP-02/AC1
func TestSampleDependencyGraph(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := openSample(t, s.main, owner, ws)
	type board struct {
		Name     string `json:"name"`
		Inbound  int    `json:"inbound"`
		Outbound int    `json:"outbound"`
	}
	type edge struct {
		From          string `json:"from"`
		To            string `json:"to"`
		Kind          string `json:"kind"`
		Contributions int    `json:"contributions"`
	}
	data, err := os.ReadFile(filepath.Join("..", "sample", "dependencies.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Boards []board `json:"boards"`
		Edges  []edge  `json:"edges"`
	}
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	res := call(s.main, http.MethodGet, projectPath(p.ID)+"/dependencies", nil, owner.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("read the dependencies: %d %s", res.status, res.body)
	}
	var graph struct {
		Boards []struct {
			ID string `json:"id"`
			board
		} `json:"boards"`
		Edges []struct {
			From          string `json:"from_board_id"`
			To            string `json:"to_board_id"`
			Kind          string `json:"kind"`
			Contributions int    `json:"contributions"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(res.body, &graph); err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	var boards []board
	for _, b := range graph.Boards {
		names[b.ID] = b.Name
		boards = append(boards, b.board)
	}
	var edges []edge
	for _, e := range graph.Edges {
		edges = append(edges, edge{From: names[e.From], To: names[e.To], Kind: e.Kind, Contributions: e.Contributions})
	}
	byName := func(a, b board) int { return strings.Compare(a.Name, b.Name) }
	byEnds := func(a, b edge) int {
		return strings.Compare(a.From+"\x00"+a.To+"\x00"+a.Kind, b.From+"\x00"+b.To+"\x00"+b.Kind)
	}
	slices.SortFunc(boards, byName)
	slices.SortFunc(want.Boards, byName)
	slices.SortFunc(edges, byEnds)
	slices.SortFunc(want.Edges, byEnds)
	if !slices.Equal(boards, want.Boards) {
		t.Errorf("the boards are %+v, want %+v", boards, want.Boards)
	}
	if !slices.Equal(edges, want.Edges) {
		t.Errorf("the edges are\n%+v\nwant\n%+v", edges, want.Edges)
	}
}

// TestImportOfOwnExportIsIdentical checks that a project imported from its
// own export, a full one, with a release, into its own instance, which gives
// it fresh IDs, exports as the original does, but for the IDs that the
// import remapped, and what is derived from them, its digests: the authoring
// archive, and the published format's export of its draft.
//
// Verifies: FR-SNAP-03/AC2
func TestImportOfOwnExportIsIdentical(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	openMaintenance(t, s, d)
	startWorkers(t, s, d)
	h := s.main
	a := openSample(t, h, owner, ws).ID
	// A release, which acknowledges the sample's warning, and a comment.
	rel := snapshotFrom(t, call(h, http.MethodPost, snapshotsPath(a), publishBody(t, "1.0.0",
		validationReport(t, h, owner, a)), owner.header(true)), http.StatusAccepted)
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		got := snapshotFrom(t, call(h, http.MethodGet, snapshotsPath(a)+"/"+rel.ID, nil, owner.header(false)),
			http.StatusOK)
		if got.Status == snapshot.Ready {
			break
		}
		if got.Status == snapshot.Failed || time.Now().After(deadline) {
			t.Fatalf("the release's build ended as %+v", got)
		}
	}
	entry := projectFrom(t, call(h, http.MethodGet, projectPath(a), nil, owner.header(false)), http.StatusOK).Entry
	if res := postComment(t, h, owner, a, ids.Encode(ids.Comment, ids.New()), entry.NodeID, "",
		"Does the ferry run at night?"); res.status != http.StatusOK {
		t.Fatalf("post a comment: %d %s", res.status, res.body)
	}
	original := exportAs(t, s, owner, a, map[string]any{})
	imported := mustImport(t, s, owner, ws, zipOf(t, original), "")
	if imported.Report.Policy != authoring.PolicyRemap || imported.Report.Releases != 1 {
		t.Fatalf("the import reports %+v", imported.Report)
	}
	b := imported.ProjectID
	if shared := sharedIDs(original, exportAs(t, s, owner, b, map[string]any{})); len(shared) > 0 {
		t.Errorf("the two projects share IDs: %v", shared[:min(len(shared), 5)])
	}
	sameFiles(t, "the authoring exports", normalised(t, original),
		normalised(t, exportAs(t, s, owner, b, map[string]any{})))
	draft := func(p string) map[string][]byte { return exportFiles(t, h, owner, projectPath(p)+"/draft/export") }
	sameFiles(t, "the draft's published exports", normalised(t, draft(a)), normalised(t, draft(b)))
}

// variableFrom decodes a variable of res, which must have the status.
func variableFrom(t *testing.T, res response, status int) struct {
	Key string `json:"key"`
} {
	t.Helper()
	var v struct {
		Key string `json:"key"`
	}
	if res.status != status || json.Unmarshal(res.body, &v) != nil {
		t.Fatalf("read the variable: %d %s", res.status, res.body)
	}
	return v
}
