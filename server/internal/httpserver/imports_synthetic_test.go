//go:build synthetic_schema

package httpserver

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/authoring"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion/synthetic"
)

// releaseOf returns the files of the archive's release of index i.
func releaseOf(t *testing.T, files map[string][]byte, i int) (authoring.Release, schemaversion.Files) {
	t.Helper()
	rel := items[authoring.Release](t, files, "releases/index.json")[i]
	out := schemaversion.Files{}
	for name, data := range files {
		if p, ok := strings.CutPrefix(name, rel.Path); ok {
			out[p] = data
		}
	}
	return rel, out
}

// TestImportSyntheticNewerRefused checks, with the synthetic version as the
// server's newest, that an archive written at a version past it, and one that
// holds a release of such a version, are refused with a clear message.
//
// Verifies: FR-DATA-01/AC2
func TestImportSyntheticNewerRefused(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	clean := exportedArchive(t, f)

	files := maps.Clone(clean)
	var m map[string]any
	if err := json.Unmarshal(files[authoring.ManifestPath], &m); err != nil {
		t.Fatal(err)
	}
	m["publishedSchemaVersion"] = synthetic.Number + 1
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	files[authoring.ManifestPath] = data
	got := failedImport(t, f.s, f.owner, f.ws, zipOf(t, files))
	if got.Code != authoring.FailFormatUnsupported || got.Check != "schema_version" ||
		!strings.Contains(got.Detail, "reads up to version") {
		t.Errorf("a newer archive: %+v", got)
	}

	files = maps.Clone(clean)
	rel, release := releaseOf(t, files, 0)
	newer, err := schemaversion.Relabel(release, synthetic.Number+1)
	if err != nil {
		t.Fatal(err)
	}
	for p, d := range newer {
		files[rel.Path+p] = d
	}
	list := items[authoring.Release](t, files, "releases/index.json")
	list[0].SchemaVersion = synthetic.Number + 1
	index, err := json.Marshal(authoring.Items[authoring.Release]{Items: list})
	if err != nil {
		t.Fatal(err)
	}
	resealSection(t, files, "releases/index.json", index, len(list))
	got = failedImport(t, f.s, f.owner, f.ws, zipOf(t, files))
	if got.Code != authoring.FailFormatUnsupported || got.Version != rel.Version {
		t.Errorf("a release of a newer version: %+v", got)
	}
}

// TestImportSyntheticOlderMigrates checks, with the synthetic version as the
// server's newest, that an archive whose release is of the older version
// imports: the release keeps its version and its bytes, and the project's
// migration log records the step that reading it at the newest version takes.
//
// Verifies: FR-DATA-01/AC3
func TestImportSyntheticOlderMigrates(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	files := exportedArchive(t, f)
	rel, _ := releaseOf(t, files, 0)
	got := mustImport(t, f.s, f.owner, f.ws, zipOf(t, files), "")
	log, err := schemaversion.Log(t.Context(), f.d.Admin(t), decode(t, ids.Workspace, f.ws),
		decode(t, ids.Project, got.ProjectID))
	if err != nil {
		t.Fatal(err)
	}
	if len(log) == 0 || log[0].From != rel.SchemaVersion || log[len(log)-1].To != synthetic.Number {
		t.Errorf("the migration log is %+v, want a step from %d up to %d", log, rel.SchemaVersion, synthetic.Number)
	}
}

// TestImportSyntheticReleaseSource checks an archive of a release at the
// synthetic version, the newest: its draft is built from the release rendered
// at the version of the server's types.
//
// Verifies: FR-DATA-01/AC3
func TestImportSyntheticReleaseSource(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	openMaintenance(t, f.s, f.d)
	e := askExport(t, f.s.main, f.owner, f.project, map[string]any{"source": f.release.ID,
		"include_releases": false})
	e = waitForExport(t, f, f.owner, e.ID)
	files := archiveOf(t, f.s.main, f.owner, f.project, e)
	release := schemaversion.Files{}
	for name, data := range files {
		if p, ok := strings.CutPrefix(name, "release/"); ok {
			release[p] = data
		}
	}
	migrated, _, err := schemaversion.Migrate(release, synthetic.Number)
	if err != nil {
		t.Fatal(err)
	}
	for p, data := range migrated {
		files["release/"+p] = data
	}
	var m map[string]any
	if err := json.Unmarshal(files[authoring.ManifestPath], &m); err != nil {
		t.Fatal(err)
	}
	manifest, err := schemaversion.Object(migrated, published.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := manifest["snapshot"].(map[string]any)
	m["source"].(map[string]any)["contentHash"] = snapshot["contentHash"]
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	files[authoring.ManifestPath] = data
	got := mustImport(t, f.s, f.owner, f.ws, zipOf(t, files), "")
	if got.Report.Nodes != 2 {
		t.Errorf("the report is %+v", *got.Report)
	}
}
