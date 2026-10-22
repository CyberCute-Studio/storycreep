package richtext

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

var update = flag.Bool("update", false, "rewrite the published rich-text JSON Schema from Schema")

var schemaFile = filepath.Join("..", "..", "..", filepath.FromSlash(SchemaPath))

// TestSchemaFile checks that the published JSON Schema is the one that Schema
// generates, so that it cannot drift from the code (PRD FR-DATA-04). Run
// go test ./internal/richtext -run TestSchemaFile -update to rewrite it.
func TestSchemaFile(t *testing.T) {
	want := Schema()
	if *update {
		if err := os.WriteFile(schemaFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what Schema generates; run go test ./internal/richtext -run TestSchemaFile -update", SchemaPath)
	}
}

// publishedSchema returns the published JSON Schema as JSON decodes it.
func publishedSchema(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if s["$id"] != SchemaID || !strings.Contains(SchemaID, "/v1/") {
		t.Errorf("the schema's $id is %v, want %s, which names its version", s["$id"], SchemaID)
	}
	return s
}

// TestSchemaExamples checks that Parse accepts each of the published schema's
// examples, which are in their canonical form, and that each validates
// against the schema.
func TestSchemaExamples(t *testing.T) {
	s := publishedSchema(t)
	validate := humaValidator(t, s)
	examples, _ := s["examples"].([]any)
	if len(examples) == 0 {
		t.Fatal("the schema has no examples")
	}
	for i, ex := range examples {
		data, err := json.Marshal(ex)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := Parse(data)
		if err != nil {
			t.Errorf("example %d: %v", i, err)
			continue
		}
		canonical, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(canonical, data) {
			t.Errorf("example %d is not in its canonical form %s", i, canonical)
		}
		if problems := validate(data); problems != nil {
			t.Errorf("example %d does not validate against the schema: %v", i, problems)
		}
	}
}

// TestSchemaAgreesWithParse checks the published schema against Parse on the
// vectors: every document that Parse accepts validates against the schema,
// and so does its canonical form, and the schema refuses every document that
// Parse refuses, except those that the vectors mark schema_accepts, which it
// accepts, because what is wrong with them is beyond what JSON Schema
// expresses. The schema is validated with huma's validator, with which the
// API validates requests.
func TestSchemaAgreesWithParse(t *testing.T) {
	validate := humaValidator(t, publishedSchema(t))
	v := readVectors(t)
	for _, vec := range v.Valid {
		if problems := validate(vec.Doc); problems != nil {
			t.Errorf("%s: the document does not validate against the schema: %v", vec.Note, problems)
		}
		doc, err := Parse(vec.Doc)
		if err != nil {
			t.Fatalf("%s: %v", vec.Note, err)
		}
		canonical, _ := json.Marshal(doc)
		if problems := validate(canonical); problems != nil {
			t.Errorf("%s: the canonical form does not validate against the schema: %v", vec.Note, problems)
		}
	}
	for _, vec := range v.Invalid {
		problems := validate(vec.Doc)
		switch {
		case vec.SchemaAccepts && problems != nil:
			t.Errorf("%s: marked schema_accepts, but the schema refuses it: %v", vec.Note, problems)
		case !vec.SchemaAccepts && problems == nil:
			t.Errorf("%s: the schema accepts it, but the vector is not marked schema_accepts", vec.Note)
		}
	}
}

// humaValidator returns a function that validates a JSON document against
// the JSON Schema s with huma, and returns the problems that it finds.
func humaValidator(t *testing.T, s map[string]any) func(doc []byte) []error {
	t.Helper()
	reg := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	defs, _ := s["$defs"].(map[string]any)
	for name, def := range defs {
		reg.Map()[name] = toHuma(t, def)
	}
	root := toHuma(t, map[string]any{"$ref": s["$ref"]})
	return func(doc []byte) []error {
		var v any
		if err := json.Unmarshal(doc, &v); err != nil {
			t.Fatal(err)
		}
		res := &huma.ValidateResult{}
		huma.Validate(reg, root, huma.NewPathBuffer([]byte{}, 0), huma.ModeWriteToServer, v, res)
		return res.Errors
	}
}

// toHuma converts the schema v, as JSON decodes it, into huma's. It fails on
// a keyword that it does not convert, so that a keyword that huma would not
// check cannot pass unnoticed.
func toHuma(t *testing.T, v any) *huma.Schema {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("a schema is not an object: %v", v)
	}
	s := &huma.Schema{}
	intOf := func(x any) *int { n := int(x.(float64)); return &n }
	floatOf := func(x any) *float64 { f := x.(float64); return &f }
	for k, x := range m {
		switch k {
		case "$ref":
			s.Ref = x.(string)
		case "type":
			s.Type = x.(string)
		case "description":
			s.Description = x.(string)
		case "const":
			s.Const = x
		case "pattern":
			s.Pattern = x.(string)
		case "minLength":
			s.MinLength = intOf(x)
		case "maxLength":
			s.MaxLength = intOf(x)
		case "minItems":
			s.MinItems = intOf(x)
		case "maxItems":
			s.MaxItems = intOf(x)
		case "uniqueItems":
			s.UniqueItems = x.(bool)
		case "minimum":
			s.Minimum = floatOf(x)
		case "maximum":
			s.Maximum = floatOf(x)
		case "additionalProperties":
			s.AdditionalProperties = x.(bool)
		case "required":
			for _, r := range x.([]any) {
				s.Required = append(s.Required, r.(string))
			}
		case "items":
			s.Items = toHuma(t, x)
		case "properties":
			s.Properties = map[string]*huma.Schema{}
			for name, p := range x.(map[string]any) {
				s.Properties[name] = toHuma(t, p)
			}
		case "oneOf":
			for _, o := range x.([]any) {
				s.OneOf = append(s.OneOf, toHuma(t, o))
			}
		default:
			t.Fatalf("the schema has the keyword %s, which the test does not convert for huma", k)
		}
	}
	s.PrecomputeMessages()
	return s
}

// TestSchemaPatterns checks that the schema's patterns refuse what Parse
// refuses for the characters that they cover, so that the schema is no looser
// there than it says.
func TestSchemaPatterns(t *testing.T) {
	validate := humaValidator(t, publishedSchema(t))
	withText := func(text string) []byte {
		s, _ := json.Marshal(text)
		return fmt.Appendf(nil, `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":%s}]}]}`, s)
	}
	withHref := func(href string) []byte {
		s, _ := json.Marshal(href)
		return fmt.Appendf(nil, `{"type":"doc","content":[{"type":"paragraph","content":[`+
			`{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":%s}}]}]}]}`, s)
	}
	for _, r := range append([]rune{lineSeparator, paragraphSeparator}, rangeOf(0, 0xA0)...) {
		doc := withText("a" + string(r))
		_, err := Parse(doc)
		if (validate(doc) == nil) != (err == nil) {
			t.Errorf("U+%04X in text: Parse says %v, and the schema the opposite", r, err)
		}
	}
	// The schema refuses a link with a space or a control character, and
	// leaves the rest of what makes one not a URL, as a % that starts no
	// escape, or white space beyond ASCII, to Parse.
	for _, r := range rangeOf(0, 0xA0) {
		doc := withHref("https://example.com/a" + string(r))
		_, err := Parse(doc)
		schemaErr := validate(doc)
		if err == nil && schemaErr != nil {
			t.Errorf("U+%04X in a link: Parse accepts it, and the schema refuses it: %v", r, schemaErr)
		}
		if (r <= 0x20 || r >= 0x7F && r <= 0x9F) && schemaErr == nil {
			t.Errorf("U+%04X in a link: the schema accepts it", r)
		}
	}
}

func rangeOf(lo, hi rune) []rune {
	var rs []rune
	for r := lo; r <= hi; r++ {
		rs = append(rs, r)
	}
	return rs
}
