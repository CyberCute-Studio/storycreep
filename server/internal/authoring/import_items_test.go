package authoring

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemacheck"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
)

// foldersPath is a section that is a list, whose items are objects.
const foldersPath = "draft/folders.json"

// TestCheckItemsStopsAtTheFirstFailure checks that a section is validated one
// item at a time, by the allocations of a file whose every item fails, of each
// of which the validator would otherwise build an error, and that the failure
// names the first of them.
//
// Verifies: NFR-SEC-13
func TestCheckItemsStopsAtTheFirstFailure(t *testing.T) {
	data := []byte(`{"items":[0` + strings.Repeat(",0", 20_000) + `]}`)
	var err error
	allocs := testing.AllocsPerRun(1, func() { err = checkSchema(foldersPath, "folders", data) })
	var refused *ImportError
	if !errors.As(err, &refused) || refused.Code != FailSchemaInvalid || refused.Section != "folders" ||
		refused.Check != "schema" || !strings.HasPrefix(refused.Detail, "item 0:") {
		t.Fatalf("the error is %v, want schema_invalid of folders at item 0", err)
	}
	if allocs > 2_000 {
		t.Errorf("a file of 20,000 bad items took %.0f allocations", allocs)
	}
	folder := `{"id":"fld_x","kind":"board","name":"n","order":"V"}`
	err = checkSchema(foldersPath, "folders", []byte(`{"items":[`+folder+`,`+folder+`,1,2]}`))
	if !errors.As(err, &refused) || !strings.HasPrefix(refused.Detail, "item 2:") {
		t.Errorf("the error is %v, want the third item's", err)
	}
}

// TestCheckItemsShape checks that a section's file is an object of the one key
// "items", spelled exactly, whatever a decoder would also read as it, and that
// white space is no part of that.
//
// Verifies: NFR-SEC-13
func TestCheckItemsShape(t *testing.T) {
	t.Parallel()
	folder := `{"id":"fld_x","kind":"board","name":"n","order":"V"}`
	for _, good := range []string{`{"items":[]}`, " {\n \"items\" : [ ] \t}\r\n", `{"items":[` + folder + `]}`,
		`{ "items": [ ` + folder + ` , ` + folder + ` ] }`} {
		if err := checkSchema(foldersPath, "folders", []byte(good)); err != nil {
			t.Errorf("%s: %v", good, err)
		}
	}
	escaped := `{"` + `\` + `u0069tems":[]}`
	for in, check := range map[string]string{
		`{}`:                            "schema",
		`[]`:                            "schema",
		`1`:                             "schema",
		`{"items":{}}`:                  "schema",
		`{"items":null}`:                "schema",
		`{"items":[],"more":1}`:         "schema",
		`{"more":1,"items":[]}`:         "schema",
		`{"ITEMS":[]}`:                  "schema",
		escaped:                         "schema",
		`{"items":[],"items":[1]}`:      "schema",
		`{"items":[1],"items":[]}`:      "schema",
		`{"items":[]`:                   "json",
		`{"items":[1 2]}`:               "json",
		`{"items":[]} trailing`:         "json",
		``:                              "json",
		`{"items":["unterminated]}`:     "json",
		`{"items":[{"id":1,"id":2,}]}`:  "json",
		`{"items":[` + folder + `,]}`:   "json",
		`{"items":[` + folder + `],"`:   "json",
		`{"items":[` + folder + `]]`:    "json",
		`{"items":[` + folder + `]}}`:   "json",
		`{"items":[` + folder + `] 1 }`: "json",
	} {
		err := checkSchema(foldersPath, "folders", []byte(in))
		var refused *ImportError
		if !errors.As(err, &refused) || refused.Code != FailSchemaInvalid || refused.Section != "folders" ||
			refused.Check != check {
			t.Errorf("%s: %v, want schema_invalid of folders at %s", in, err, check)
		}
	}
}

// TestCheckItemsBounds checks that one item past its bounds, of bytes or of
// values, or a file nested too deeply, is refused before a tree is built, and
// that an item at them is not refused for its size.
//
// Verifies: NFR-SEC-13
func TestCheckItemsBounds(t *testing.T) {
	t.Parallel()
	wantBound := func(name, data, check string) {
		t.Helper()
		err := checkSchema(foldersPath, "folders", []byte(data))
		var refused *ImportError
		if !errors.As(err, &refused) || refused.Code != FailArchiveInvalid || refused.Section != "folders" ||
			refused.Check != check {
			t.Errorf("%s: %v, want archive_invalid of folders at %s", name, err, check)
		}
	}
	array := func(values int) string { return "[0" + strings.Repeat(",0", values-2) + "]" }
	wantBound("bytes", `{"items":["`+strings.Repeat("s", maxItemBytes)+`"]}`, "item_size")
	wantBound("values", `{"items":[`+array(maxItemValues+1)+`]}`, "item_values")
	wantBound("depth", `{"items":[`+strings.Repeat("[", schemacheck.MaxDepth)+strings.Repeat("]", schemacheck.MaxDepth)+
		`]}`, "json_depth")
	// At the bound it is the schema that refuses it.
	err := checkSchema(foldersPath, "folders", []byte(`{"items":[`+array(maxItemValues)+`]}`))
	var refused *ImportError
	if !errors.As(err, &refused) || refused.Code != FailSchemaInvalid {
		t.Errorf("an item at the bound: %v, want its schema's refusal", err)
	}
}

// TestPlainFilesBound checks that the two files that are validated whole are
// held to maxPlainJSON by what their headers say, before they are read, and to
// maxItemValues before their trees are built.
//
// Verifies: NFR-SEC-13
func TestPlainFilesBound(t *testing.T) {
	t.Parallel()
	claiming := func(members map[string]string, name string, size uint64) *archive {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		for _, member := range slicesSorted(members) {
			f, err := w.CreateHeader(&zip.FileHeader{Name: member, Method: zip.Store})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write([]byte(members[member])); err != nil {
				t.Fatal(err)
			}
		}
		if name != "" {
			if _, err := w.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Deflate, UncompressedSize64: size,
				CompressedSize64: size / maxCompressionRatio}); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		data := b.Bytes()
		a, err := openArchive(bytes.NewReader(data), int64(len(data)), 1<<30)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	_, err := readArchived(claiming(nil, ManifestPath, maxPlainJSON+1))
	wantRefused(t, err, "size")

	entry := ManifestSection{Name: "project", Path: "draft/project.json", Items: 1, SHA256: sha256Hex([]byte("{}"))}
	manifest := map[string]string{ManifestPath: manifestOf(t, entry)}
	_, err = readArchived(claiming(manifest, entry.Path, maxPlainJSON+1))
	wantRefused(t, err, "size")

	many := `{"name":"P","slug":"p","description":"","settings":[0` + strings.Repeat(",0", maxItemValues) + `]}`
	_, err = readArchived(claiming(map[string]string{ManifestPath: manifestOf(t, entry), entry.Path: many}, "", 0))
	wantRefused(t, err, "json_values")
}

// TestManifestCountsReadExactKeys checks that a release's row takes its counts
// from the keys that the schema and the runtime read, and not from a key that
// differs from one of them by its case, which they ignore.
//
// Verifies: FR-DATA-01/AC11
func TestManifestCountsReadExactKeys(t *testing.T) {
	t.Parallel()
	const real = `"counts":{"boards":1,"components":2,"connections":3,"nodes":4,"variables":5`
	for name, manifest := range map[string]string{
		"a key of the manifest": `{` + real + `},"COUNTS":{"boards":9,"components":9,"connections":9,"nodes":9,"variables":9}}`,
		"a key of the counts":   `{` + real + `,"NODES":999}}`,
	} {
		got, err := manifestCounts(schemaversion.Files{published.ManifestPath: []byte(manifest)})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var counts snapshot.EntityCounts
		if err := json.Unmarshal(got, &counts); err != nil {
			t.Fatal(err)
		}
		want, err := storedCounts([]byte(`{"boards":1,"components":2,"connections":3,"nodes":4,"variables":5}`))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s: the counts are %s, want %s", name, got, want)
		}
	}
}

// slicesSorted returns the keys of members, sorted.
func slicesSorted(members map[string]string) []string {
	keys := make([]string, 0, len(members))
	for k := range members {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// TestListSchemasHoldOnlyTheirItems checks that each section's schema that
// checkItems validates by its items says nothing else of its file: it is an
// object of the one required key, an array with no keyword about its items
// together, so that validating them one at a time leaves nothing unchecked.
//
// Verifies: NFR-SEC-13
func TestListSchemasHoldOnlyTheirItems(t *testing.T) {
	t.Parallel()
	var doc struct {
		Properties map[string]struct {
			Ref string `json:"$ref"`
		} `json:"properties"`
		Defs map[string]map[string]any `json:"$defs"`
	}
	if err := json.Unmarshal(Schema(), &doc); err != nil {
		t.Fatal(err)
	}
	lists := 0
	for _, s := range sections {
		if !s.list {
			continue
		}
		lists++
		def := doc.Defs[strings.TrimPrefix(doc.Properties[s.Path].Ref, "#/$defs/")]
		props, _ := def["properties"].(map[string]any)
		items, _ := props["items"].(map[string]any)
		required, _ := def["required"].([]any)
		if len(def) != 4 || def["type"] != "object" || def["additionalProperties"] != false || len(props) != 1 ||
			len(required) != 1 || required[0] != "items" {
			t.Errorf("%s: the file's schema is %v", s.Name, def)
		}
		for k := range items {
			if k != "type" && k != "items" && k != "description" {
				t.Errorf("%s: its array has the keyword %s, which an item alone cannot be checked against", s.Name, k)
			}
		}
		if items["type"] != "array" || items["items"] == nil {
			t.Errorf("%s: its items are %v", s.Name, items)
		}
	}
	if lists == 0 {
		t.Error("no section is a list")
	}
}
