package schemaversion

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"testing/fstest"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/flow"
	"github.com/CyberCute-Studio/storycreep/server/internal/jsonscan"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemacheck"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
)

// v1 is version 1 of the published format, which package published's types
// are of, and the oldest that the server reads: it has no version before it
// to migrate from, nor to render down to.
var v1 = Version{Number: 1, Validate: validateV1}

// validateV1 checks f as a consumer of version 1 does: each file against its
// JSON Schema of /schema/published/v1, as package published generates it from
// its types, the manifest's hashes of the files and the content hash
// (Sealed), and the snapshot as the reference runtime loads it
// (RuntimeLoads), which checks what schema/published/README.md says a
// consumer checks beyond the schemas. The types and the runtime are of the
// newest real version, which version 1 is only until another lands, so that
// a real version 2, which changes both, gives version 1 a validator that
// depends on neither first (CONTRIBUTING.md), or version 1's fixture no
// longer validates (TestFixtures).
func validateV1(f Files) error {
	s, err := v1Schemas()
	if err != nil {
		return err
	}
	if err := s.Check(f); err != nil {
		return err
	}
	if err := Sealed(f); err != nil {
		return err
	}
	return RuntimeLoads(f)
}

// v1Schemas returns the JSON Schemas of version 1, which package published
// generates as /schema/published/v1 holds them.
var v1Schemas = sync.OnceValues(func() (*Schemas, error) {
	docs, err := published.Files(false)
	if err != nil {
		return nil, err
	}
	for p := range docs {
		if path.Dir(p) != published.PublishedDir {
			delete(docs, p)
		}
	}
	return NewSchemas(published.PublishedDir, docs)
})

// Schemas are the JSON Schemas of a version of the published format, one of
// each of a snapshot's JSON files and one of an AST, compiled by a validator
// of JSON Schema 2020-12 that is not package schemagen's, so that a mistake of
// the generator's does not pass because the check shares it.
type Schemas struct {
	files map[string]*jsonschema.Schema
}

// NewSchemas compiles docs, the documents of a version, by their paths, as
// the repository holds them in dir, each document's $id the URL of its path
// (schemagen.BaseURL), and takes the document of each file of a snapshot from
// its name in dir: storycreep.schema.json of the manifest, board.schema.json
// of a board's file, and each other's of the name of its file.
func NewSchemas(dir string, docs map[string][]byte) (*Schemas, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	for p, data := range docs {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("schemaversion: %s: %w", p, err)
		}
		if err := c.AddResource(schemagen.BaseURL+p, doc); err != nil {
			return nil, fmt.Errorf("schemaversion: %s: %w", p, err)
		}
	}
	s := &Schemas{files: map[string]*jsonschema.Schema{}}
	for _, name := range []string{"storycreep", "settings", "variables", "component_types", "components", "board"} {
		compiled, err := c.Compile(schemagen.BaseURL + dir + "/" + name + ".schema.json")
		if err != nil {
			return nil, fmt.Errorf("schemaversion: %s/%s.schema.json: %w", dir, name, err)
		}
		s.files[name] = compiled
	}
	// A file holds no null (serializer.Layout), which Check refuses first, so
	// that what any other value may be need not be walked.
	schemacheck.FailFast("AnyValue", slices.Collect(maps.Values(s.files))...)
	return s, nil
}

// schemaOf are the names of the schemas of a snapshot's JSON files but its
// boards', by their paths (PRD §6.3).
var schemaOf = map[string]string{
	published.ManifestPath:       "storycreep",
	published.SettingsPath:       "settings",
	published.VariablesPath:      "variables",
	published.ComponentTypesPath: "component_types",
	published.ComponentsPath:     "components",
}

// Check validates each JSON file of f against its schema, in the order of
// their paths, and fails on the first that its schema refuses, and on a file
// that no schema is of, as one of a path that no snapshot's file has: the
// manifest and each file of the project have their own paths (schemaOf), and
// a board's file is boards/<name>.json.
func (s *Schemas) Check(f Files) error {
	for _, p := range slices.Sorted(maps.Keys(f)) {
		if !IsJSON(p) {
			continue
		}
		name, ok := schemaOf[p]
		if !ok && path.Dir(p) == "boards" && strings.HasSuffix(p, ".json") {
			name = "board"
		}
		sch, ok := s.files[name]
		if !ok {
			return fmt.Errorf("schemaversion: %s is no file of a snapshot", p)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(f[p]))
		if err != nil {
			return fmt.Errorf("schemaversion: %s is not JSON: %w", p, err)
		}
		if err := checkShape(f[p]); err != nil {
			return fmt.Errorf("schemaversion: %s does not validate against its schema: %w", p, err)
		}
		if err := sch.Validate(inst); err != nil {
			return fmt.Errorf("schemaversion: %s does not validate against its schema: %s", p, schemacheck.Failure(err))
		}
	}
	return nil
}

// checkShape refuses what the schemas, as FailFast leaves them, are not to be
// given: a file that nests too deeply, and a null, which no file holds. The
// file is JSON.
func checkShape(data []byte) error {
	if err := schemacheck.CheckDepth(data); err != nil {
		return err
	}
	if jsonscan.HasNull(data) {
		return errors.New("it holds a null, which no value of a snapshot may be")
	}
	return nil
}

// RuntimeLoads checks that the reference runtime loads f, a snapshot's files
// of the version of package published's types (flow.Load), as a game would,
// which checks what schema/published/README.md says a consumer checks beyond
// the schemas.
func RuntimeLoads(f Files) error {
	fsys := fstest.MapFS{}
	for p, data := range f {
		fsys[p] = &fstest.MapFile{Data: data}
	}
	if _, err := flow.Load(fsys); err != nil {
		return fmt.Errorf("schemaversion: %w", err)
	}
	return nil
}
