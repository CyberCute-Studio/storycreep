// Package apicontract reads the API's OpenAPI document (PRD FR-API-02) and
// does two things with it: it finds the changes between two versions of the
// document that break the clients of the older one (FR-API-01 AC3), and it
// generates the TypeScript client's types.
//
// It reads the part of OpenAPI 3.1 that huma writes for the server's
// operations, and refuses a document with anything else, such as a keyword
// it does not know, rather than skip it: a keyword that the comparison skips
// is a part of the contract that could change unnoticed, and one that the
// generator skips is a type that the client gets wrong. A pull request that
// makes huma write something new teaches this package to read it.
package apicontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// Document is an OpenAPI 3.1 document.
type Document struct {
	OpenAPI    string               `json:"openapi"`
	Info       Info                 `json:"info"`
	Paths      map[string]*PathItem `json:"paths"`
	Components *Components          `json:"components"`
}

// Info is the document's metadata, which no client depends on.
type Info struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// Components holds the named schemas that the operations refer to.
type Components struct {
	Schemas map[string]*Schema `json:"schemas"`
}

// PathItem holds the operations of a path, by method.
type PathItem struct {
	Get     *Operation `json:"get"`
	Put     *Operation `json:"put"`
	Post    *Operation `json:"post"`
	Delete  *Operation `json:"delete"`
	Options *Operation `json:"options"`
	Head    *Operation `json:"head"`
	Patch   *Operation `json:"patch"`
	Trace   *Operation `json:"trace"`
}

// operations returns the item's operations by method, in a fixed order.
func (p *PathItem) operations() []methodOperation {
	var out []methodOperation
	for _, mo := range []methodOperation{
		{"GET", p.Get}, {"PUT", p.Put}, {"POST", p.Post}, {"DELETE", p.Delete}, {"OPTIONS", p.Options},
		{"HEAD", p.Head}, {"PATCH", p.Patch}, {"TRACE", p.Trace},
	} {
		if mo.op != nil {
			out = append(out, mo)
		}
	}
	return out
}

type methodOperation struct {
	method string
	op     *Operation
}

// Operation is one operation: a method on a path.
type Operation struct {
	OperationID string               `json:"operationId"`
	Summary     string               `json:"summary"`
	Description string               `json:"description"`
	Tags        []string             `json:"tags"`
	Deprecated  bool                 `json:"deprecated"`
	Parameters  []*Parameter         `json:"parameters"`
	RequestBody *RequestBody         `json:"requestBody"`
	Responses   map[string]*Response `json:"responses"`
}

// Parameter is a parameter of an operation, in its path, query, headers, or
// cookies.
type Parameter struct {
	Name        string            `json:"name"`
	In          string            `json:"in"`
	Description string            `json:"description"`
	Required    bool              `json:"required"`
	Deprecated  bool              `json:"deprecated"`
	Style       string            `json:"style"`
	Explode     *bool             `json:"explode"`
	Schema      *Schema           `json:"schema"`
	Example     json.RawMessage   `json:"example"`
	Examples    map[string]*Value `json:"examples"`
}

// RequestBody is the body that an operation takes.
type RequestBody struct {
	Description string                `json:"description"`
	Required    bool                  `json:"required"`
	Content     map[string]*MediaType `json:"content"`
}

// Response is an answer of an operation, for one status.
type Response struct {
	Description string                `json:"description"`
	Headers     map[string]*Header    `json:"headers"`
	Content     map[string]*MediaType `json:"content"`
}

// Header is a header of a response.
type Header struct {
	Description string            `json:"description"`
	Required    bool              `json:"required"`
	Deprecated  bool              `json:"deprecated"`
	Style       string            `json:"style"`
	Explode     *bool             `json:"explode"`
	Schema      *Schema           `json:"schema"`
	Example     json.RawMessage   `json:"example"`
	Examples    map[string]*Value `json:"examples"`
}

// MediaType is a body in one format, such as application/json.
type MediaType struct {
	Schema   *Schema           `json:"schema"`
	Example  json.RawMessage   `json:"example"`
	Examples map[string]*Value `json:"examples"`
}

// Value is a named example.
type Value struct {
	Summary     string          `json:"summary"`
	Description string          `json:"description"`
	Value       json.RawMessage `json:"value"`
}

// Schema is a JSON Schema, with the keywords that huma writes. A nil Schema,
// like an empty one, allows any value.
type Schema struct {
	Type                 Types              `json:"type"`
	Title                string             `json:"title"`
	Description          string             `json:"description"`
	Ref                  string             `json:"$ref"`
	Format               string             `json:"format"`
	ContentMediaType     string             `json:"contentMediaType"`
	ContentEncoding      string             `json:"contentEncoding"`
	Default              json.RawMessage    `json:"default"`
	Examples             []json.RawMessage  `json:"examples"`
	Items                *Schema            `json:"items"`
	AdditionalProperties *Additional        `json:"additionalProperties"`
	Properties           map[string]*Schema `json:"properties"`
	Enum                 []json.RawMessage  `json:"enum"`
	Const                json.RawMessage    `json:"const"`
	Minimum              *float64           `json:"minimum"`
	ExclusiveMinimum     *float64           `json:"exclusiveMinimum"`
	Maximum              *float64           `json:"maximum"`
	ExclusiveMaximum     *float64           `json:"exclusiveMaximum"`
	MultipleOf           *float64           `json:"multipleOf"`
	MinLength            *int               `json:"minLength"`
	MaxLength            *int               `json:"maxLength"`
	Pattern              string             `json:"pattern"`
	PatternDescription   string             `json:"patternDescription"`
	MinItems             *int               `json:"minItems"`
	MaxItems             *int               `json:"maxItems"`
	UniqueItems          bool               `json:"uniqueItems"`
	Required             []string           `json:"required"`
	MinProperties        *int               `json:"minProperties"`
	MaxProperties        *int               `json:"maxProperties"`
	ReadOnly             bool               `json:"readOnly"`
	WriteOnly            bool               `json:"writeOnly"`
	Deprecated           bool               `json:"deprecated"`
	OneOf                []*Schema          `json:"oneOf"`
	AnyOf                []*Schema          `json:"anyOf"`
	AllOf                []*Schema          `json:"allOf"`
	// Discriminator is read only so that checkSchema refuses it, with why.
	Discriminator json.RawMessage `json:"discriminator"`
}

// Types is the type keyword, which is one type name or a list of them, such
// as ["string", "null"] for a nullable string. Empty means any type.
type Types []string

func (t *Types) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*t = Types{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return errors.New("type is neither a type name nor a list of them")
	}
	*t = many
	return nil
}

// Additional is the additionalProperties keyword: false, true, or a schema of
// the values of the properties that the schema does not name.
type Additional struct {
	// Allowed is false for false, and true otherwise.
	Allowed bool
	// Schema is the schema of the values, or nil for any value.
	Schema *Schema
}

func (a *Additional) UnmarshalJSON(b []byte) error {
	switch string(bytes.TrimSpace(b)) {
	case "false":
		*a = Additional{}
	case "true":
		*a = Additional{Allowed: true}
	default:
		var s Schema
		if err := decodeStrict(b, &s); err != nil {
			return fmt.Errorf("additionalProperties: %w", err)
		}
		*a = Additional{Allowed: true, Schema: &s}
	}
	return nil
}

// refPrefix starts every reference to a named schema.
const refPrefix = "#/components/schemas/"

// Load reads a document, and checks that it is one this package understands:
// OpenAPI 3.1, with only the objects and keywords that the types above have,
// references only to named schemas that exist, with nothing but annotations
// beside them, and no named schemas that only refer to each other in a
// cycle, required properties that their schema names, a unique operationId
// on every operation, and path parameters that match their path.
func Load(b []byte) (*Document, error) {
	var d Document
	if err := decodeStrict(b, &d); err != nil {
		return nil, fmt.Errorf("the OpenAPI document cannot be read: %w", err)
	}
	if !strings.HasPrefix(d.OpenAPI, "3.1.") {
		return nil, fmt.Errorf("the OpenAPI document has version %q, not 3.1", d.OpenAPI)
	}
	if err := d.check(); err != nil {
		return nil, fmt.Errorf("the OpenAPI document is invalid: %w", err)
	}
	return &d, nil
}

// decodeStrict decodes the JSON value b into v, a pointer, and fails on a
// key that v's type does not have, spelled exactly, or on anything after the
// value.
func decodeStrict(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("there is more after the JSON value")
	}
	return exactKeys(b, reflect.TypeOf(v))
}

var (
	rawMessageType = reflect.TypeFor[json.RawMessage]()
	typesType      = reflect.TypeFor[Types]()
	additionalType = reflect.TypeFor[Additional]()
)

// exactKeys checks that every key of an object in the JSON value b, which
// decodes into a value of type t, is spelled as the json tag of a field of
// t's, unless the object is a map. encoding/json also takes a key that
// differs from a tag only in case, such as "Enum", which JSON Schema ignores,
// as the field, which would read a keyword that the document does not have.
// The keys inside additionalProperties are checked when Additional decodes
// its schema.
func exactKeys(b []byte, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == rawMessageType || t == typesType || t == additionalType:
		return nil
	case t.Kind() == reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(b, &fields); err != nil {
			return err
		}
		for _, key := range slices.Sorted(maps.Keys(fields)) {
			f, ok := fieldByTag(t, key)
			if !ok {
				return fmt.Errorf("json: unknown field %q", key)
			}
			if err := exactKeys(fields[key], f.Type); err != nil {
				return err
			}
		}
	case t.Kind() == reflect.Map:
		var values map[string]json.RawMessage
		if err := json.Unmarshal(b, &values); err != nil {
			return err
		}
		for _, key := range slices.Sorted(maps.Keys(values)) {
			if err := exactKeys(values[key], t.Elem()); err != nil {
				return err
			}
		}
	case t.Kind() == reflect.Slice:
		var items []json.RawMessage
		if err := json.Unmarshal(b, &items); err != nil {
			return err
		}
		for _, item := range items {
			if err := exactKeys(item, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

// fieldByTag returns the field of the struct type t whose json tag names
// key, spelled exactly.
func fieldByTag(t reflect.Type, key string) (reflect.StructField, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name == key {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// pathParam matches a parameter of a path template, such as {id}.
var pathParam = regexp.MustCompile(`\{([^{}]*)\}`)

// templateParams returns the names of the parameters of a path template, in
// order.
func templateParams(path string) []string {
	var names []string
	for _, m := range pathParam.FindAllStringSubmatch(path, -1) {
		names = append(names, m[1])
	}
	return names
}

func (d *Document) check() error {
	if d.Components != nil {
		for _, name := range slices.Sorted(maps.Keys(d.Components.Schemas)) {
			s := d.Components.Schemas[name]
			if s == nil {
				return fmt.Errorf("schema %s is null", name)
			}
			if err := d.checkSchema(s); err != nil {
				return fmt.Errorf("schema %s: %w", name, err)
			}
		}
		// A named schema may be a reference to another, as an alias is, which
		// the comparison and the generator follow to the schema that it
		// names, so the references must end.
		for _, name := range slices.Sorted(maps.Keys(d.Components.Schemas)) {
			seen := map[string]bool{name: true}
			for s := d.Components.Schemas[name]; s.Ref != ""; {
				next := refName(s.Ref)
				if seen[next] {
					return fmt.Errorf("schema %s: its references go round in a cycle", name)
				}
				seen[next] = true
				s = d.Components.Schemas[next]
			}
		}
	}
	// The paths, and the maps of each operation, are read in order, so that
	// a document with more than one problem gets the same error every time.
	ids := map[string]string{}
	for _, path := range slices.Sorted(maps.Keys(d.Paths)) {
		item := d.Paths[path]
		if item == nil {
			return fmt.Errorf("%s has no operations", path)
		}
		for _, mo := range item.operations() {
			where := mo.method + " " + path
			op := mo.op
			if op.OperationID == "" {
				return fmt.Errorf("%s has no operationId", where)
			}
			if other, ok := ids[op.OperationID]; ok {
				return fmt.Errorf("%s and %s have the same operationId %s", other, where, op.OperationID)
			}
			ids[op.OperationID] = where
			if err := d.checkOperation(path, op); err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
		}
	}
	return nil
}

func (d *Document) checkOperation(path string, op *Operation) error {
	var inPath []string
	seen := map[string]bool{}
	for _, p := range op.Parameters {
		if p == nil {
			return errors.New("a parameter is null")
		}
		switch p.In {
		case "path":
			if !p.Required {
				return fmt.Errorf("the path parameter %s is not required", p.Name)
			}
			inPath = append(inPath, p.Name)
		case "query", "header", "cookie":
		default:
			return fmt.Errorf("the parameter %s is in %q, which is not a place for one", p.Name, p.In)
		}
		key := paramKey(p)
		if seen[key] {
			return fmt.Errorf("the %s parameter %s appears twice", p.In, p.Name)
		}
		seen[key] = true
		if err := d.checkSchema(p.Schema); err != nil {
			return fmt.Errorf("the %s parameter %s: %w", p.In, p.Name, err)
		}
	}
	want := templateParams(path)
	slices.Sort(want)
	slices.Sort(inPath)
	if !slices.Equal(want, inPath) {
		return fmt.Errorf("the path has the parameters %q, and the operation describes %q", want, inPath)
	}
	if rb := op.RequestBody; rb != nil {
		for _, mt := range slices.Sorted(maps.Keys(rb.Content)) {
			if err := d.checkMediaType(rb.Content[mt]); err != nil {
				return fmt.Errorf("the request body %s: %w", mt, err)
			}
		}
	}
	if len(op.Responses) == 0 {
		return errors.New("it has no responses")
	}
	for _, status := range slices.Sorted(maps.Keys(op.Responses)) {
		r := op.Responses[status]
		if r == nil {
			return fmt.Errorf("the response %s is null", status)
		}
		if !validStatus(status) {
			return fmt.Errorf("the response %s is not a status, a range such as 4XX, or default", status)
		}
		for _, name := range slices.Sorted(maps.Keys(r.Headers)) {
			h := r.Headers[name]
			if h == nil {
				return fmt.Errorf("the response %s's header %s is null", status, name)
			}
			if err := d.checkSchema(h.Schema); err != nil {
				return fmt.Errorf("the response %s's header %s: %w", status, name, err)
			}
		}
		for _, mt := range slices.Sorted(maps.Keys(r.Content)) {
			if err := d.checkMediaType(r.Content[mt]); err != nil {
				return fmt.Errorf("the response %s's body %s: %w", status, mt, err)
			}
		}
	}
	return nil
}

func (d *Document) checkMediaType(m *MediaType) error {
	if m == nil {
		return errors.New("it is null")
	}
	return d.checkSchema(m.Schema)
}

// typeNames are the names of JSON Schema's types.
var typeNames = []string{"string", "integer", "number", "boolean", "array", "object", "null"}

// checkSchema checks that every type in s is one of JSON Schema's, every
// property that s requires is one that it names, every reference names a
// schema that exists and has beside it only the keywords that annotate it,
// and s has no discriminator.
func (d *Document) checkSchema(s *Schema) error {
	if s == nil {
		return nil
	}
	if s.Discriminator != nil {
		// huma's validator (v2.39.1) pushes the discriminator's property onto
		// its path at an object without it, or whose property is neither a
		// string nor null, and does not pop it (validateDiscriminator), so
		// that the location of every later problem of the body has a segment
		// more for each such object: a body of many of them costs memory that
		// grows with the square of their number, and a body of 5 MiB of {} in
		// an array of a oneOf with a discriminator runs the server out of
		// memory. Allow one again only once huma pops what it pushes.
		return errors.New("it has a discriminator, with which huma's validator (v2.39.1) costs memory that grows " +
			"with the square of the number of objects in a body that lack its property")
	}
	for _, t := range s.Type {
		if !slices.Contains(typeNames, t) {
			return fmt.Errorf("%q is not a type", t)
		}
	}
	// huma requires only the properties that it names. The comparison and the
	// generator read a property from the properties, so they would skip one
	// that only required names.
	for _, name := range s.Required {
		if _, ok := s.Properties[name]; !ok {
			return fmt.Errorf("it requires the property %s, which it does not name", name)
		}
	}
	if s.Ref != "" {
		name, ok := strings.CutPrefix(s.Ref, refPrefix)
		if !ok || d.Components == nil || d.Components.Schemas[name] == nil {
			return fmt.Errorf("the reference %s does not name a schema of the document", s.Ref)
		}
		// huma writes the tags of a field whose type is a struct beside the
		// reference to the struct's schema, such as a default or a bound.
		// The comparison and the generator read the named schema in place
		// of the reference, so they would skip such a keyword.
		rest := *s
		rest.Ref, rest.Title, rest.Description, rest.Examples = "", "", "", nil
		rest.Deprecated, rest.ReadOnly, rest.WriteOnly = false, false, false
		if !reflect.DeepEqual(rest, Schema{}) {
			return fmt.Errorf("the reference %s has keywords beside it other than title, description, examples, "+
				"deprecated, readOnly, and writeOnly", s.Ref)
		}
	}
	for _, sub := range s.subschemas() {
		if err := d.checkSchema(sub); err != nil {
			return err
		}
	}
	return nil
}

// subschemas returns the schemas inside s.
func (s *Schema) subschemas() []*Schema {
	var out []*Schema
	if s.Items != nil {
		out = append(out, s.Items)
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		out = append(out, s.AdditionalProperties.Schema)
	}
	for _, name := range slices.Sorted(maps.Keys(s.Properties)) {
		out = append(out, s.Properties[name])
	}
	out = append(out, s.OneOf...)
	out = append(out, s.AnyOf...)
	out = append(out, s.AllOf...)
	return out
}

// validStatus reports whether s is a key of an operation's responses: a
// status, a range of them such as 4XX, or default.
func validStatus(s string) bool {
	if s == "default" {
		return true
	}
	if len(s) != 3 || s[0] < '1' || s[0] > '5' {
		return false
	}
	if s[1:] == "XX" {
		return true
	}
	return s[1] >= '0' && s[1] <= '9' && s[2] >= '0' && s[2] <= '9'
}

// paramKey identifies a parameter among its operation's: header names are
// case-insensitive.
func paramKey(p *Parameter) string {
	name := p.Name
	if p.In == "header" {
		name = strings.ToLower(name)
	}
	return p.In + " " + name
}

// resolve returns the named schema that s refers to, and its name, or s and
// "" if s is not a reference. A named schema that is itself a reference, as
// an alias of another is, is followed to the schema that describes the
// values, whose name it returns. Load has checked that every name exists and
// that no chain of references goes round in a cycle.
func (d *Document) resolve(s *Schema) (*Schema, string) {
	var schemas map[string]*Schema
	if d.Components != nil {
		schemas = d.Components.Schemas
	}
	var name string
	// A document built in code may have a cycle, which ends here, or a
	// reference to a schema that it lacks, even with no components, which
	// resolves to nil, the schema that allows any value.
	for i := 0; s != nil && s.Ref != "" && i <= len(schemas); i++ {
		name = refName(s.Ref)
		s = schemas[name]
	}
	return s, name
}

// refName returns the name of the schema that a reference names.
func refName(ref string) string {
	return strings.TrimPrefix(ref, refPrefix)
}
