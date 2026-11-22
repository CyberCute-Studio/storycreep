package authoring

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/danielgtaylor/huma/v2"
)

// SchemaPath is the path of the format's JSON Schema, from the repository's
// root (FR-DATA-04).
const SchemaPath = "schema/authoring/v1/archive.schema.json"

// SchemaID is the schema's $id, which names its version, so that a consumer
// can pin it (FR-DATA-04 AC3).
const SchemaID = "https://raw.githubusercontent.com/CyberCute-Studio/storycreep/main/" + SchemaPath

// Schema returns the format's JSON Schema, generated from its types: of an
// object of an archive's JSON files, by their paths, each of which validates
// against its section's definition. The files of the releases and of the
// assets are not JSON of this format: each release's validate against the
// published format's schemas of its version.
func Schema() []byte {
	r := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	properties := map[string]any{
		ManifestPath: map[string]any{"$ref": r.Schema(reflect.TypeFor[Manifest](), true, "").Ref},
	}
	required := []string{ManifestPath}
	for _, s := range sections {
		properties[s.Path] = map[string]any{"$ref": r.Schema(s.root, true, "").Ref}
	}
	slices.Sort(required)
	doc := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     SchemaID,
		"title":   "Storycreep authoring export, version 1",
		"description": "The JSON files of an authoring export of a Storycreep project (PRD FR-DATA-01), by their " +
			"paths in its zip. authoring.json says which sections the archive holds. schema/authoring/README.md " +
			"describes the format.",
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
		"$defs":                r.Map(),
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		panic("authoring: the schema is not JSON: " + err.Error())
	}
	return append(data, '\n')
}
