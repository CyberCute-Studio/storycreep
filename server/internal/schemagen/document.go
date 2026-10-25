package schemagen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
)

// Dialect is the version of JSON Schema that the documents are written in.
const Dialect = "https://json-schema.org/draft/2020-12/schema"

// BaseURL is where the repository's files are published: the URL of a file
// of the main branch is BaseURL followed by its path from the repository's
// root, which is the $id of a document that /schema holds, as it is of the
// rich-text schema (richtext.SchemaID).
const BaseURL = "https://raw.githubusercontent.com/CyberCute-Studio/storycreep/main/"

// Document is a JSON Schema document that the repository publishes.
type Document struct {
	// Path is where the repository holds it, from its root, as
	// schema/published/v1/board.schema.json: a directory of its format, one
	// of the format's version, as v1, and its name, which ends in
	// .schema.json.
	Path        string
	Title       string
	Description string
	// Root is the Go type of the values that it describes.
	Root reflect.Type
	// Examples are values that it describes, as JSON.
	Examples []string
}

// pathPattern matches a document's path, with the version of its format.
var pathPattern = regexp.MustCompile(`^schema/[a-z0-9-]+/v[1-9][0-9]*/[a-z0-9_-]+\.schema\.json$`)

// ID returns the document's $id: the URL at which the repository publishes
// it, which names its format's version, so that a consumer can pin it (PRD
// FR-DATA-04 AC3).
func (d Document) ID() string {
	return BaseURL + d.Path
}

// Document returns the document d, as /schema holds it: its $schema, its $id,
// its title and description, a $ref to the definition of its root type, the
// definitions that that refers to, at any depth, and its examples, as JSON
// with each object's keys in alphabetical order, indented by two spaces,
// without escaping HTML, and with a line feed at its end. It fails on a path
// that is not of a document's form, and on an example that is not JSON.
func (g *Generator) Document(d Document) ([]byte, error) {
	if !pathPattern.MatchString(d.Path) {
		return nil, fmt.Errorf("schemagen: %s is not the path of a document of a format's version", d.Path)
	}
	root := g.Ref(d.Root)
	defs := map[string]*Schema{}
	g.collect(root, defs)
	examples := make([]any, len(d.Examples))
	for i, ex := range d.Examples {
		dec := json.NewDecoder(bytes.NewReader([]byte(ex)))
		dec.UseNumber()
		// Decode reads one value, and json.Valid that nothing follows it.
		if err := dec.Decode(&examples[i]); err != nil || !json.Valid([]byte(ex)) {
			return nil, fmt.Errorf("schemagen: example %d of %s is not one JSON value", i, d.Path)
		}
	}
	doc := map[string]any{
		"$schema":     Dialect,
		"$id":         d.ID(),
		"title":       d.Title,
		"description": d.Description,
		"$ref":        root.Ref,
		"$defs":       defs,
	}
	if len(examples) > 0 {
		doc["examples"] = examples
	}
	compact, err := Marshal(doc)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, compact, "", "  "); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// collect adds to defs the definitions that s refers to, at any depth.
func (g *Generator) collect(s *Schema, defs map[string]*Schema) {
	if s == nil || s.False {
		return
	}
	if name := RefName(s.Ref); name != "" {
		if _, ok := defs[name]; !ok {
			def := g.defs[name]
			if def == nil {
				panic("schemagen: " + s.Ref + " refers to no definition")
			}
			defs[name] = def
			g.collect(def, defs)
		}
	} else if s.Ref != "" {
		panic("schemagen: " + s.Ref + " is not a reference to a definition of the document")
	}
	for _, c := range []*Schema{s.Items, s.AdditionalProperties, s.PropertyNames} {
		g.collect(c, defs)
	}
	for _, name := range slices.Sorted(maps.Keys(s.Properties)) {
		g.collect(s.Properties[name], defs)
	}
	for _, c := range s.OneOf {
		g.collect(c, defs)
	}
}
