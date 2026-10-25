// Package schemagen generates the JSON Schemas that the repository publishes
// under /schema from the Go types that the server writes and reads them with
// (PRD FR-DATA-04), and the TypeScript types of the web app from those
// schemas (AC2): the published game format and the save-state format (plan
// item 41, note N49), and, as they are added, the diff structure (item 80)
// and the authoring export (item 83).
//
// A Generator reads a Go type by reflection, as encoding/json reads it, into
// a definition of a schema's $defs, named after the type, which every
// document that reaches the type holds: a struct is an object of its
// exported fields, by their json names, of which those tagged omitempty or
// omitzero are optional; a slice is an array; a map of string keys is an
// object of any keys; and a string, a bool, an integer, and a float are what
// JSON calls them. Tags on a field constrain it (see Generator). An
// interface that Union declares is one of its variants, each told by the
// value of one property. A type that gives its own schema is a Provider.
//
// No schema takes null, which the published format never holds (§6.2), and
// every object takes keys that it does not define, which a consumer ignores
// (§6.4), of any value but null, at any depth (AnyValue). A closed
// Generator, New(true)'s, takes no such key, so that a test can check that an
// example holds only what the schema defines.
package schemagen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Schema is a JSON Schema, of the keywords of draft 2020-12 that the
// repository's published schemas use. The zero Schema takes any value.
type Schema struct {
	// False is the schema that no value matches, which JSON writes as
	// false: a property that an object may not have.
	False bool

	Ref         string
	Description string
	// Type is one JSON type: string, integer, number, boolean, array, or
	// object. A schema never takes null.
	Type    string
	Const   any
	Enum    []any
	Pattern string
	// MinLength and MaxLength count a string's characters as code points.
	MinLength, MaxLength *int
	Minimum, Maximum     *float64
	MinItems, MaxItems   *int
	Items                *Schema
	Properties           map[string]*Schema
	Required             []string
	// AdditionalProperties is the schema of the values of the properties
	// that Properties does not name, AnyValue for an object of the
	// published format, or false for one that takes no other property.
	AdditionalProperties *Schema
	PropertyNames        *Schema
	OneOf                []*Schema
}

// False is the schema that no value matches.
func False() *Schema {
	return &Schema{False: true}
}

// keywords maps each keyword that a Schema has to a pointer to its field's
// value, in a Schema that is not False.
func (s *Schema) keywords() map[string]any {
	return map[string]any{
		"$ref": &s.Ref, "description": &s.Description, "type": &s.Type, "const": &s.Const, "enum": &s.Enum,
		"pattern": &s.Pattern, "minLength": &s.MinLength, "maxLength": &s.MaxLength, "minimum": &s.Minimum,
		"maximum": &s.Maximum, "minItems": &s.MinItems, "maxItems": &s.MaxItems, "items": &s.Items,
		"properties": &s.Properties, "required": &s.Required, "additionalProperties": &s.AdditionalProperties,
		"propertyNames": &s.PropertyNames, "oneOf": &s.OneOf,
	}
}

// MarshalJSON writes the schema with its keywords in alphabetical order, and
// without escaping HTML, as the published files are written (§6.2).
func (s *Schema) MarshalJSON() ([]byte, error) {
	if s.False {
		return []byte("false"), nil
	}
	m := map[string]any{}
	for k, p := range s.keywords() {
		v := reflectValue(p)
		if v != nil {
			m[k] = v
		}
	}
	return Marshal(m)
}

// reflectValue returns the value that the field at p holds, or nil when it
// holds none, which the schema then leaves out.
func reflectValue(p any) any {
	switch p := p.(type) {
	case *string:
		if *p != "" {
			return *p
		}
	case *any:
		return *p
	case *[]any:
		if *p != nil {
			return *p
		}
	case *[]string:
		if len(*p) > 0 {
			return *p
		}
	case **int:
		if *p != nil {
			return **p
		}
	case **float64:
		if *p != nil {
			return **p
		}
	case **Schema:
		if *p != nil {
			return *p
		}
	case *map[string]*Schema:
		if *p != nil {
			return *p
		}
	case *[]*Schema:
		if *p != nil {
			return *p
		}
	}
	return nil
}

// UnmarshalJSON reads a schema that MarshalJSON wrote, and refuses a keyword
// that a Schema does not have, so that nothing that a document holds escapes
// what reads it, as the TypeScript generator.
func (s *Schema) UnmarshalJSON(data []byte) error {
	*s = Schema{}
	if string(bytes.TrimSpace(data)) == "false" {
		s.False = true
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("a schema is neither an object nor false: %w", err)
	}
	fields := s.keywords()
	for _, k := range slices.Sorted(maps.Keys(raw)) {
		p, ok := fields[k]
		if !ok {
			return fmt.Errorf("a schema has the keyword %s, which schemagen does not read", k)
		}
		d := json.NewDecoder(bytes.NewReader(raw[k]))
		d.UseNumber()
		if err := d.Decode(p); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	return nil
}

// Ref returns a reference to the definition name of a document's $defs.
func Ref(name string) *Schema {
	return &Schema{Ref: DefsPrefix + name}
}

// DefsPrefix is how a document refers to its definitions.
const DefsPrefix = "#/$defs/"

// RefName returns the name of the definition that ref refers to, or "" when
// it refers to none of the document's.
func RefName(ref string) string {
	name, ok := strings.CutPrefix(ref, DefsPrefix)
	if !ok || name == "" || strings.Contains(name, "/") {
		return ""
	}
	return name
}

// Describe returns s with the description d, or s itself when d is empty.
func Describe(s *Schema, d string) *Schema {
	if d != "" {
		s.Description = d
	}
	return s
}

// Int returns a pointer to n, for a keyword's bound.
func Int(n int) *int { return &n }

// Float returns a pointer to f, for a keyword's bound.
func Float(f float64) *float64 { return &f }

// Marshal writes v as JSON without escaping HTML, as the published files and
// their schemas are written (§6.2).
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
