package schemaversion_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
)

// TestFixtures checks that every real version of the published format, and
// every version that the server reads, the synthetic one among them with the
// synthetic_schema build tag, has a fixture project, which validates at its
// version, laid out as every file is (§6.2), and that each migrates to the
// newest version that the server reads, where it validates (PRD FR-DATA-03,
// §6.4).
//
// Verifies: FR-DATA-03/AC1
func TestFixtures(t *testing.T) {
	r := schemaversion.Supported()
	versions := map[int]bool{}
	for _, n := range realVersions(t) {
		if !r.Contains(n) {
			t.Errorf("the server does not read version %d, whose schemas /schema/published holds, so its fixture "+
				"migrates to no version", n)
		}
		versions[n] = true
	}
	for n := r.Min; n <= r.Max; n++ {
		versions[n] = true
	}
	for n := range versions {
		dir := fixtureDir(t, n)
		f := readTree(t, dir)
		if len(f) == 0 {
			t.Errorf("version %d has no fixture in %s", n, dir)
			continue
		}
		if got, err := schemaversion.VersionOf(f); err != nil || got != n {
			t.Errorf("%s is of version %d, %v; want %d", dir, got, err, n)
			continue
		}
		for p, data := range f {
			if laid, err := serializer.Layout(data); err != nil || !bytes.Equal(laid, data) {
				t.Errorf("%s/%s is not laid out as every file is: %v", dir, p, err)
			}
		}
		if err := schemaversion.Validate(f); err != nil {
			t.Errorf("%s does not validate at version %d: %v", dir, n, err)
		}
		migrated, _, err := schemaversion.Migrate(f, r.Max)
		if err != nil {
			t.Errorf("%s does not migrate to version %d: %v", dir, r.Max, err)
			continue
		}
		if got, err := schemaversion.VersionOf(migrated); err != nil || got != r.Max {
			t.Errorf("%s migrates to version %d, %v; want %d", dir, got, err, r.Max)
		}
		if err := schemaversion.Validate(migrated); err != nil {
			t.Errorf("%s migrated to version %d does not validate: %v", dir, r.Max, err)
		}
	}
}

// TestValidate checks that Validate refuses a snapshot of version 1 that a
// consumer refuses: one that a schema refuses, whose hashes are not its
// files', that holds a file that no snapshot has, or one that its manifest
// does not list, that the reference runtime refuses, and one of a version
// that the server does not read.
func TestValidate(t *testing.T) {
	f := readTree(t, "schema/fixtures/v1")
	const board = "boards/harbour-intro.json"
	if err := schemaversion.Validate(f); err != nil {
		t.Fatalf("the fixture of version 1 does not validate: %v", err)
	}
	unsealed := schemaversion.Files{}
	for p, data := range f {
		unsealed[p] = data
	}
	unsealed[board] = bytes.Replace(f[board], []byte(`"Harbour Intro`), []byte(`"Harbour Outro`), 1)
	extra := func(path string, data []byte) schemaversion.Files {
		out := schemaversion.Files{path: data}
		for p, data := range f {
			out[p] = data
		}
		return out
	}
	for _, c := range []struct {
		name string
		f    schemaversion.Files
		want string
	}{
		{"a title that its schema refuses", edited(t, f, board, func(b map[string]any) {
			nodeOf(t, b, "nd_000000000000000000000000N1")["title"] = strings.Repeat("a", 201)
		}), "does not validate against its schema"},
		{"a file whose hash is not the manifest's", unsealed, "with the hash of its bytes"},
		{"a content hash that is not the files'", unsealedEdit(t, f, published.ManifestPath, func(m map[string]any) {
			m["snapshot"].(map[string]any)["contentHash"] = "sha256:" + strings.Repeat("0", 64)
		}), "content hash"},
		{"a file that no snapshot has", extra("notes.json", []byte("{}\n")), "no file of a snapshot"},
		{"a file of no snapshot's path", extra("project/settings.json.bak", f[published.SettingsPath]),
			"no file of a snapshot"},
		{"a board's file that the manifest does not list", extra("boards/lighthouse.json", f[board]),
			"does not list"},
		{"a connection to no node", edited(t, f, board, func(b map[string]any) {
			b["connections"].([]any)[0].(map[string]any)["to"] = "nd_00000000000000000000000NNN"
		}), "not a snapshot that a runtime runs"},
	} {
		err := schemaversion.Validate(c.f)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v; want an error that says %q", c.name, err, c.want)
		}
	}
	r := schemaversion.Supported()
	newer := edited(t, f, published.ManifestPath, func(m map[string]any) { m["schemaVersion"] = r.Max + 1 })
	var unsupported *schemaversion.UnsupportedError
	if err := schemaversion.Validate(newer); !errors.As(err, &unsupported) || unsupported.Version != r.Max+1 ||
		unsupported.Supported != r || !errors.Is(err, schemaversion.ErrUnsupported) {
		t.Errorf("a manifest of version %d: %v; want an *UnsupportedError of %d and %s", r.Max+1, err, r.Max+1, r)
	}
	none := edited(t, f, published.ManifestPath, func(m map[string]any) { m["schemaVersion"] = 0 })
	if err := schemaversion.Validate(none); err == nil || errors.Is(err, schemaversion.ErrUnsupported) {
		t.Errorf("a manifest of version 0: %v; want an error of no version", err)
	}
}

// unsealedEdit returns a copy of f in which edit has changed the file at
// path, as a tree, its manifest's hashes left as they were, or fails the
// test.
func unsealedEdit(t *testing.T, f schemaversion.Files, path string, edit func(map[string]any)) schemaversion.Files {
	t.Helper()
	out, err := schemaversion.Edit(f, func(p string, file map[string]any) error {
		if p == path {
			edit(file)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestVersionOf checks that VersionOf reads a manifest's schemaVersion as an
// integer however its JSON writes it, and refuses one that is no version.
func TestVersionOf(t *testing.T) {
	for _, c := range []struct {
		manifest string
		want     int
	}{
		{`{"schemaVersion": 1}`, 1},
		{`{"schemaVersion": 1.0}`, 1},
		{`{"schemaVersion": 2e0}`, 2},
	} {
		got, err := schemaversion.VersionOf(schemaversion.Files{published.ManifestPath: []byte(c.manifest)})
		if err != nil || got != c.want {
			t.Errorf("%s: %d, %v; want %d", c.manifest, got, err, c.want)
		}
	}
	for _, manifest := range []string{`{}`, `{"schemaVersion": "1"}`, `{"schemaVersion": 1.5}`,
		`{"schemaVersion": 0}`, `{"schemaVersion": -1}`, `[1]`, `{"schemaVersion": null}`, `{`} {
		if got, err := schemaversion.VersionOf(schemaversion.Files{published.ManifestPath: []byte(manifest)}); err ==
			nil {
			t.Errorf("%s: %d; want an error", manifest, got)
		}
	}
	if _, err := schemaversion.VersionOf(schemaversion.Files{}); err == nil {
		t.Error("files without a manifest: want an error")
	}
}

// TestMigrateAndRenderAtOneVersion checks that Migrate and Render return a
// snapshot of the version that they are asked for as it is, with no step,
// and refuse a version that the server does not read.
func TestMigrateAndRenderAtOneVersion(t *testing.T) {
	f := readTree(t, "schema/fixtures/v1")
	r := schemaversion.Supported()
	for name, run := range map[string]func(schemaversion.Files, int) (schemaversion.Files, []schemaversion.Step,
		error){"Migrate": schemaversion.Migrate, "Render": schemaversion.Render} {
		got, steps, err := run(f, 1)
		if err != nil || steps != nil || len(got) != len(f) {
			t.Errorf("%s at version 1: %d files, %v, %v; want the snapshot as it is", name, len(got), steps, err)
		}
		for p := range f {
			if !bytes.Equal(got[p], f[p]) {
				t.Errorf("%s at version 1 changed %s", name, p)
			}
		}
		for _, n := range []int{0, r.Max + 1} {
			var unsupported *schemaversion.UnsupportedError
			if _, _, err := run(f, n); !errors.As(err, &unsupported) || unsupported.Version != n {
				t.Errorf("%s at version %d: %v; want an *UnsupportedError", name, n, err)
			}
		}
	}
}

// TestNegotiate checks that a pull negotiates the newest version that the
// server and the client both read, and says which side is to be upgraded
// where they read none in common (PRD §6.4, FR-SYNC-07).
func TestNegotiate(t *testing.T) {
	r := func(min, max int) schemaversion.Range { return schemaversion.Range{Min: min, Max: max} }
	for _, c := range []struct {
		server, client schemaversion.Range
		want           int
		upgrade        string
	}{
		{r(1, 2), r(1, 1), 1, ""},
		{r(1, 2), r(1, 2), 2, ""},
		{r(1, 2), r(1, 5), 2, ""},
		{r(1, 2), r(2, 3), 2, ""},
		{r(2, 3), r(1, 2), 2, ""},
		{r(1, 1), r(1, 1), 1, ""},
		{r(1, 2), r(3, 4), 0, schemaversion.UpgradeServer},
		{r(2, 3), r(1, 1), 0, schemaversion.UpgradeClient},
	} {
		got, err := c.server.Negotiate(c.client)
		if c.upgrade == "" {
			if err != nil || got != c.want {
				t.Errorf("server %s, client %s: %d, %v; want %d", c.server, c.client, got, err, c.want)
			}
			continue
		}
		var none *schemaversion.NoCommonVersionError
		if !errors.As(err, &none) || none.Upgrade() != c.upgrade || none.Server != c.server ||
			none.Client != c.client || !errors.Is(err, schemaversion.ErrUnsupported) {
			t.Errorf("server %s, client %s: %d, %v; want a *NoCommonVersionError that upgrades the %s", c.server,
				c.client, got, err, c.upgrade)
		}
		if msg := err.Error(); !strings.Contains(msg, c.server.String()) || !strings.Contains(msg, c.client.String()) {
			t.Errorf("server %s, client %s: %q names both ranges", c.server, c.client, msg)
		}
	}
	for _, client := range []schemaversion.Range{r(0, 1), r(2, 1), r(-1, -1)} {
		if _, err := r(1, 2).Negotiate(client); err == nil || errors.Is(err, schemaversion.ErrUnsupported) {
			t.Errorf("client %d–%d: %v; want an error of no range", client.Min, client.Max, err)
		}
	}
	s := schemaversion.Supported()
	if got, err := schemaversion.Negotiate(s); err != nil || got != s.Max {
		t.Errorf("a client of the server's range %s: %d, %v; want %d", s, got, err, s.Max)
	}
}

// TestEditAndSeal checks what a migration writes with: Edit's trees, laid
// out as every file is, an asset left as it is, Relabel's schemaVersion of
// every JSON file, and Seal's hashes, which Sealed checks.
func TestEditAndSeal(t *testing.T) {
	data, err := schemaversion.Encode(map[string]any{"b": json.Number("1.50"), "a": "<&>\u2028"})
	if want := "{\n  \"a\": \"<&>\\u2028\",\n  \"b\": 1.50\n}\n"; err != nil || string(data) != want {
		t.Errorf("Encode: %q, %v; want %q", data, err, want)
	}
	f := readTree(t, "schema/fixtures/v1")
	const asset = "assets/0000000000000000000000ASST.webp"
	withAsset := unsealedEdit(t, f, published.ManifestPath, func(m map[string]any) {
		m["files"].(map[string]any)[asset] = "sha256:" + strings.Repeat("0", 64)
	})
	withAsset[asset] = []byte{0xff, 0x00, 0xfe}
	if err := schemaversion.Sealed(withAsset); err == nil {
		t.Error("Sealed takes an asset of another hash")
	}
	relabeled, err := schemaversion.Relabel(withAsset, 7)
	if err != nil {
		t.Fatal(err)
	}
	for p := range withAsset {
		if !schemaversion.IsJSON(p) {
			if !bytes.Equal(relabeled[p], withAsset[p]) {
				t.Errorf("Relabel changed the asset %s", p)
			}
			continue
		}
		file, err := schemaversion.Object(relabeled, p)
		if err != nil || file["schemaVersion"] != json.Number("7") {
			t.Errorf("Relabel: %s's schemaVersion is %v, %v; want 7", p, file["schemaVersion"], err)
		}
	}
	sealed, err := schemaversion.Seal(relabeled)
	if err != nil {
		t.Fatal(err)
	}
	if err := schemaversion.Sealed(sealed); err != nil {
		t.Errorf("Sealed refuses what Seal sealed: %v", err)
	}
	m, err := schemaversion.Object(sealed, published.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m["files"].(map[string]any)[asset], string(published.HashOf(withAsset[asset])); got != want {
		t.Errorf("Seal: the asset's hash is %v; want %s", got, want)
	}
	before, _ := schemaversion.Object(f, published.ManifestPath)
	for _, key := range []string{"generator", "project", "counts"} {
		if a, b := mustJSON(t, before[key]), mustJSON(t, m[key]); a != b {
			t.Errorf("Seal changed the manifest's %s: %s; want %s", key, b, a)
		}
	}
	missing := schemaversion.Files{}
	for p, data := range sealed {
		if p != asset {
			missing[p] = data
		}
	}
	if _, err := schemaversion.Seal(missing); err == nil || !strings.Contains(err.Error(), asset) {
		t.Errorf("Seal of a snapshot without a file that its manifest lists: %v; want an error that names it", err)
	}
	if _, err := schemaversion.Relabel(schemaversion.Files{published.ManifestPath: []byte(`{}`)}, 2); err == nil {
		t.Error("Relabel of a file without a schemaVersion: want an error")
	}
}

// mustJSON returns v as compact JSON, or fails the test.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
