package clipboard

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

var update = flag.Bool("update", false, "rewrite the clipboard's JSON Schema from its Go types")

// schemaFile is the clipboard's JSON Schema in the repository.
var schemaFile = filepath.Join("..", "..", "..", filepath.FromSlash(SchemaPath))

// TestSchemaFile checks that the clipboard's JSON Schema in the repository is
// the one that Schema generates from its types, so that the documented
// structure cannot drift from the one that the server reads and writes, and
// that its $id names its version. Run go test ./internal/clipboard -run
// TestSchemaFile -update in server/ to write it again.
//
// Verifies: FR-EDIT-07/AC3
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
		t.Errorf("%s is not what Schema generates; run go test ./internal/clipboard -run TestSchemaFile -update",
			SchemaPath)
	}
	if !strings.Contains(SchemaID, "/v1/") || !strings.HasSuffix(SchemaID, SchemaPath) || Version != 1 {
		t.Errorf("the schema's $id %s does not name the version %d at %s", SchemaID, Version, SchemaPath)
	}
}

// TestSchemaValidates checks that a clipboard that Check takes validates
// against the published schema, with a validator that is not huma's, and
// that one of another format does not.
func TestSchemaValidates(t *testing.T) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(Schema()))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddResource(SchemaID, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile(SchemaID)
	if err != nil {
		t.Fatal(err)
	}
	node, output := ids.Encode(ids.Node, ids.New()), ids.Encode(ids.BranchOutput, ids.New())
	clip := map[string]any{"format": Format, "version": Version, "project_id": ids.Encode(ids.Project, ids.New()),
		"board_id": ids.Encode(ids.Board, ids.New()),
		"nodes": []any{
			map[string]any{"id": node, "type": "branch", "position": map[string]any{"x": 1.5, "y": -2},
				"outputs": []any{map[string]any{"id": output, "condition": "trust > 1"}}},
			map[string]any{"id": ids.Encode(ids.Node, ids.New()), "type": "element", "title": "Keeper",
				"text": json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text",` +
					`"text":"Hello"}]}]}`), "position": map[string]any{"x": 0, "y": 0}}},
		"connections": []any{}, "variables": []any{map[string]any{"id": ids.Encode(ids.Variable, ids.New()),
			"key": "trust", "scope": "global", "type": "int", "default": 3}},
		"components": []any{}}
	data, err := json.Marshal(clip)
	if err != nil {
		t.Fatal(err)
	}
	var parsed Clipboard
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if problems := Check(parsed); len(problems) > 0 {
		t.Fatalf("Check: %v", problems)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(inst); err != nil {
		t.Errorf("a clipboard does not validate: %v", err)
	}
	clip["format"] = "something.else"
	data, _ = json.Marshal(clip)
	inst, _ = jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if schema.Validate(inst) == nil {
		t.Error("a clipboard of another format validates")
	}
}
