package diff

import (
	"encoding/json"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// SchemaID is the $id of the diff's JSON Schema, which names its version.
const SchemaID = "https://raw.githubusercontent.com/CyberCute-Studio/storycreep/main/" + SchemaPath

// SchemaPath is where the repository holds the diff's JSON Schema, from its
// root.
const SchemaPath = "schema/diff/v1/diff.schema.json"

// Schema returns the JSON Schema (2020-12) of a diff, as SchemaPath holds it
// (PRD FR-DATA-04, FR-SNAP-04 AC4): the schema that the API's OpenAPI
// document gives the answer of POST /api/v1/projects/{project_id}/diff, with
// the definitions that it refers to, under $defs, which the Godot addon's
// diff, of the files that it holds, writes too. A change of what a diff holds
// that a consumer could misread is a version of its own, at a path of its
// version.
func Schema() []byte {
	r := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	ref := r.Schema(reflect.TypeFor[Diff](), true, "")
	doc := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     SchemaID,
		"title":   "Storycreep entity diff, version 1",
		"description": "The entity-level diff of two snapshots of a Storycreep project's published game format, " +
			"a release's or the draft's, compared at one version of the format (PRD §7.4, FR-SNAP-04): the " +
			"entities that only one side has, those whose fields differ, with each field's value on either side, " +
			"and the nodes and connections that changed boards, grouped by board, with counts of every change. " +
			"POST /api/v1/projects/{project_id}/diff answers with it, and the Godot addon computes it of the files " +
			"that it holds; schema/diff-corpus holds both to the same diff of each of its cases.",
		"$ref":  ref.Ref,
		"$defs": r.Map(),
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		panic("diff: the schema is not JSON: " + err.Error())
	}
	return append(data, '\n')
}
