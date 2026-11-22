package authoring

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/published"
)

var update = flag.Bool("update", false, "rewrite schema/authoring/v1/archive.schema.json")

var schemaFile = filepath.Join("..", "..", "..", filepath.FromSlash(SchemaPath))

// TestSchemaFile checks that the committed schema is the one that the types
// generate, and that its $id names its version, so that a consumer can pin it.
//
// Verifies: FR-DATA-04/AC3
func TestSchemaFile(t *testing.T) {
	want := Schema()
	if *update {
		if err := os.MkdirAll(filepath.Dir(schemaFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(schemaFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what Schema generates; run go test ./internal/authoring -run TestSchemaFile -update",
			SchemaPath)
	}
	if !strings.Contains(SchemaID, "/v1/") || !strings.HasSuffix(SchemaID, SchemaPath) || Version != 1 {
		t.Errorf("the schema's $id %s does not name the version %d at %s", SchemaID, Version, SchemaPath)
	}
}

// compiled returns the format's schema, compiled by a validator that is not
// the generator's.
func compiled(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(Schema()))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(SchemaID, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(SchemaID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// instance returns the JSON of an archive's files, by their paths.
func instance(t *testing.T, files map[string]any) any {
	t.Helper()
	data, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func testManifest() Manifest {
	return Manifest{Format: Format, FormatVersion: Version, PublishedSchemaVersion: published.SchemaVersion,
		Generator:  published.GeneratorInfo{Name: "storycreep", Version: "test"},
		ExportedAt: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC),
		Project:    ManifestProject{ID: "prj_01K7JK5K0QEJV8MQ74SJBGXV2Z", Name: "Harbour", Slug: "harbour"},
		Source:     Source{Kind: "draft"}, Profile: ProfileFull, PersonalData: true, Sections: []ManifestSection{}}
}

// TestAnnotationValidates checks that the format defines the annotation item
// from its first version, with the §5.3 fields, so that an archive holding one
// passes validation and reaches an importer's section check, and that the
// schema refuses an annotation of another kind or without its geometry.
//
// Verifies: FR-DATA-04/AC3
func TestAnnotationValidates(t *testing.T) {
	s := compiled(t)
	frame := Annotation{ID: "ann_1", BoardID: "bd_01K7JK5K0QEJV8MQ74SJBGXV2Z", Kind: "frame",
		Geometry: Geometry{X: 10, Y: -20.5, Width: 300, Height: 200},
		Text:     Text(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Act one"}]}]}`)}
	good := instance(t, map[string]any{ManifestPath: testManifest(),
		"annotations.json": Items[Annotation]{Items: []Annotation{frame}}})
	if err := s.Validate(good); err != nil {
		t.Errorf("an archive with a frame does not validate: %v", err)
	}
	for name, bad := range map[string]map[string]any{
		"another kind": {"id": "ann_1", "boardId": "bd_1", "kind": "sticker",
			"geometry": map[string]any{"x": 0, "y": 0, "width": 1, "height": 1}},
		"no geometry": {"id": "ann_1", "boardId": "bd_1", "kind": "note"},
		"a negative size": {"id": "ann_1", "boardId": "bd_1", "kind": "note",
			"geometry": map[string]any{"x": 0, "y": 0, "width": -1, "height": 1}},
	} {
		v := instance(t, map[string]any{ManifestPath: testManifest(),
			"annotations.json": map[string]any{"items": []any{bad}}})
		if err := s.Validate(v); err == nil {
			t.Errorf("an annotation with %s validates", name)
		}
	}
}

// TestSectionsAreDistinct checks the registry: each section has a name and a
// path of its own, and a table is in one section at most, so that an import
// reads it once.
func TestSectionsAreDistinct(t *testing.T) {
	names, paths, tables := map[string]bool{}, map[string]bool{}, map[string]string{}
	for _, s := range Sections() {
		if names[s.Name] || paths[s.Path] || s.root == nil || s.read == nil {
			t.Errorf("the section %q repeats a name or a path, or lacks its type or its read", s.Name)
		}
		names[s.Name], paths[s.Path] = true, true
		for _, table := range s.Tables {
			if other, ok := tables[table]; ok {
				t.Errorf("the table %s is in the sections %s and %s", table, other, s.Name)
			}
			if _, ok := NotExported[table]; ok {
				t.Errorf("the table %s is in the section %s and in NotExported", table, s.Name)
			}
			tables[table] = s.Name
		}
	}
	if !names["annotations"] {
		t.Error("the format has no annotations section")
	}
}

// TestReadme checks what an archive's README says of personal data, and what
// a release's archive leaves out.
//
// Verifies: FR-DATA-01/AC6
func TestReadme(t *testing.T) {
	full := testManifest()
	if got := readme(full); !strings.Contains(got, "it contains personal data") {
		t.Errorf("a full archive's README does not state that it holds personal data:\n%s", got)
	}
	content := testManifest()
	content.Profile, content.PersonalData = ProfileContent, false
	if got := readme(content); strings.Contains(got, "it contains personal data") ||
		!strings.Contains(got, "content-only") {
		t.Errorf("a content-only archive's README:\n%s", got)
	}
	release := testManifest()
	release.Source = Source{Kind: "release", SnapshotID: "snp_1", Version: "1.2.0"}
	release.LeftOut = LeftOut{Comments: 3, PlayStates: 1}
	got := readme(release)
	for _, want := range []string{"release 1.2.0", "no folders", "3 comment(s) and 1 play state(s)"} {
		if !strings.Contains(got, want) {
			t.Errorf("a release's README lacks %q:\n%s", want, got)
		}
	}
}

// TestExceedsImportLimit checks the warning of an archive larger than the
// instance's import limit, which only a ready one, with a size, can be.
func TestExceedsImportLimit(t *testing.T) {
	size := func(n int64) *int64 { return &n }
	for _, c := range []struct {
		size *int64
		want bool
	}{{nil, false}, {size(100), false}, {size(101), true}} {
		if got := (Export{SizeBytes: c.size, ImportMaxBytes: 100}).ExceedsImportLimit(); got != c.want {
			t.Errorf("an archive of %v bytes against a limit of 100: %v", c.size, got)
		}
	}
}

// TestDecimal checks that a node's coordinates are written exactly.
func TestDecimal(t *testing.T) {
	data, err := json.Marshal(struct {
		X Decimal  `json:"x"`
		W *Decimal `json:"w,omitempty"`
	}{X: "-1234.25"})
	if err != nil || string(data) != `{"x":-1234.25}` {
		t.Errorf("the decimal is written as %s, %v", data, err)
	}
	if _, err := json.Marshal(Decimal("not a number")); err == nil {
		t.Error("a decimal that is no number is written")
	}
}
