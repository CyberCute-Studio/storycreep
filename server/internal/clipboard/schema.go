package clipboard

import (
	"encoding/json"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// SchemaID is the $id of the clipboard's JSON Schema, which names its
// version (Version).
const SchemaID = "https://raw.githubusercontent.com/CyberCute-Studio/storycreep/main/schema/clipboard/v1/clipboard.schema.json"

// SchemaPath is where the repository holds the clipboard's JSON Schema, from
// its root.
const SchemaPath = "schema/clipboard/v1/clipboard.schema.json"

// Schema returns the JSON Schema (2020-12) of a clipboard of the current
// version, as SchemaPath holds it (PRD FR-EDIT-07 AC3): the schema that the
// API's OpenAPI document gives a clipboard, the answer of a copy and a part
// of a paste's payload, with the definitions that it refers to, a rich-text
// document's among them, under $defs. A version of the clipboard that changes
// what it holds is a schema of its own, at a path of its version.
func Schema() []byte {
	r := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	ref := r.Schema(reflect.TypeFor[Clipboard](), true, "")
	doc := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     SchemaID,
		"title":   "Storycreep clipboard, version 1",
		"description": "A selection of the nodes of a board of a Storycreep project, as POST " +
			"/api/v1/projects/{project_id}/clipboard/copy answers with it and the command clipboard.paste " +
			"pastes it (PRD FR-EDIT-07): the nodes, with a branch's outputs, the connections between them, and " +
			"a declaration of each variable, component, and attribute definition that their texts, " +
			"expressions, and speakers name. Its version is the version field's, which a change of what a " +
			"clipboard holds increments, with a schema of its own.",
		"$ref":  ref.Ref,
		"$defs": r.Map(),
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		panic("clipboard: the schema is not JSON: " + err.Error())
	}
	return append(data, '\n')
}
