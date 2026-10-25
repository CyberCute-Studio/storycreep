package schemagen

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type shape interface{ isShape() }

type circle struct {
	Radius float64 `json:"radius" minimum:"0" doc:"The radius."`
}

type square struct {
	Side  int    `json:"side" minimum:"1" maximum:"10"`
	Label string `json:"label,omitempty" maxLength:"5"`
}

func (*circle) isShape() {}
func (*square) isShape() {}

func (circle) JSONSchemaDoc() string { return "A circle." }

type typed struct{}

func (typed) JSONSchemaOf(_ *Generator, typ string) *Schema {
	if typ == "int" {
		return &Schema{Type: "integer"}
	}
	return &Schema{Type: "string"}
}

type setting struct {
	Default typed    `json:"default" typeFrom:"type"`
	Kind    string   `json:"kind" enum:"int,text"`
	Options []string `json:"options,omitempty" when:"type=text" required:"true" minItems:"1" minLength:"1"`
	Type    string   `json:"type" enum:"int,text"`
}

type drawing struct {
	ID       string            `json:"id" id:"board"`
	Shapes   []shape           `json:"shapes" maxItems:"3"`
	Tags     []string          `json:"tags,omitempty" enum:"a,b"`
	Counts   map[string]int    `json:"counts" keyId:"node"`
	Settings []setting         `json:"settings"`
	Meta     map[string]string `json:"meta,omitzero" keyPattern:"^x-"`
	Version  int               `json:"version" const:"2"`
	Embedded
	hidden  string //nolint:unused // A field that encoding/json does not read.
	Skipped string `json:"-"`
}

type Embedded struct {
	Note string `json:"note,omitempty"`
}

func generator(closed bool) *Generator {
	g := New(closed)
	g.Union(reflect.TypeFor[shape](), "kind", "A shape.",
		Variant{Value: "circle", Type: reflect.TypeFor[*circle]()},
		Variant{Value: "square", Type: reflect.TypeFor[*square]()},
	)
	return g
}

func asJSON(t *testing.T, s *Schema) string {
	t.Helper()
	data, err := s.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestStruct checks how a struct's fields are read: by their json names, an
// embedded struct's as the struct's own, each optional exactly when it is
// tagged omitempty or omitzero, with their tags' constraints, and its object
// open to properties of any value but null, or closed.
func TestStruct(t *testing.T) {
	g := generator(false)
	g.Ref(reflect.TypeFor[drawing]())
	d := g.Def("drawing")
	if d == nil {
		t.Fatal("drawing is not defined")
	}
	want := []string{"counts", "id", "settings", "shapes", "version"}
	if !reflect.DeepEqual(d.Required, want) {
		t.Errorf("required = %v, want %v", d.Required, want)
	}
	for _, name := range []string{"counts", "id", "meta", "note", "settings", "shapes", "tags", "version"} {
		if d.Properties[name] == nil {
			t.Errorf("drawing has no property %s", name)
		}
	}
	if len(d.Properties) != 8 {
		t.Errorf("drawing has the properties %v", d.Properties)
	}
	checks := map[string]string{
		"id":      `{"$ref":"#/$defs/BoardId"}`,
		"shapes":  `{"items":{"$ref":"#/$defs/shape"},"maxItems":3,"type":"array"}`,
		"tags":    `{"items":{"enum":["a","b"],"type":"string"},"type":"array"}`,
		"counts":  `{"additionalProperties":{"type":"integer"},"propertyNames":{"$ref":"#/$defs/NodeId"},"type":"object"}`,
		"meta":    `{"additionalProperties":{"type":"string"},"propertyNames":{"pattern":"^x-"},"type":"object"}`,
		"version": `{"const":2,"type":"integer"}`,
	}
	for name, want := range checks {
		if got := asJSON(t, d.Properties[name]); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
	if got := asJSON(t, d.AdditionalProperties); got != `{"$ref":"#/$defs/AnyValue"}` {
		t.Errorf("an open object's other properties are %s", got)
	}
	if got := asJSON(t, g.Def("BoardId")); !strings.Contains(got, `"pattern":"^bd_[01234567]`) {
		t.Errorf("BoardId = %s", got)
	}
	closed := generator(true)
	closed.Ref(reflect.TypeFor[drawing]())
	if !closed.Def("drawing").AdditionalProperties.False {
		t.Error("a closed generator's object takes other properties")
	}
}

// TestUnion checks a union's definition: one of its variants', each with its
// key's value, and without the properties of the others.
func TestUnion(t *testing.T) {
	g := generator(false)
	g.Ref(reflect.TypeFor[shape]())
	if got := asJSON(t, g.Def("shape")); got != `{"description":"A shape.","oneOf":[{"$ref":"#/$defs/circle"},{"$ref":"#/$defs/square"}]}` {
		t.Errorf("shape = %s", got)
	}
	c := g.Def("circle")
	if got := asJSON(t, c.Properties["kind"]); got != `{"const":"circle"}` {
		t.Errorf("circle's kind = %s", got)
	}
	if !c.Properties["side"].False || !c.Properties["label"].False {
		t.Errorf("circle takes a square's properties: %v", c.Properties)
	}
	if c.Description != "A circle." || c.Properties["radius"].Description != "The radius." {
		t.Errorf("circle's descriptions are %q and %q", c.Description, c.Properties["radius"].Description)
	}
	if want := []string{"kind", "radius"}; !reflect.DeepEqual(c.Required, want) {
		t.Errorf("circle requires %v", c.Required)
	}
	if s := g.Def("square"); !s.Properties["radius"].False || s.Properties["kind"].Const != "square" {
		t.Errorf("square = %s", asJSON(t, s))
	}
}

// TestVariants checks a struct with a field typed by another (typeFrom) and
// one there only for some of its values (when): one variant for each value
// of the property that they name, and none for another property's.
func TestVariants(t *testing.T) {
	g := New(true)
	g.Ref(reflect.TypeFor[setting]())
	s := g.Def("setting")
	if len(s.OneOf) != 2 {
		t.Fatalf("setting has %d variants, want 2: %s", len(s.OneOf), asJSON(t, s))
	}
	for _, v := range s.OneOf {
		kind, typ := asJSON(t, v.Properties["kind"]), v.Properties["type"].Const
		if kind != `{"enum":["int","text"],"type":"string"}` {
			t.Errorf("%v: kind = %s", typ, kind)
		}
		wantDefault := `{"type":"string"}`
		if typ == "int" {
			wantDefault = `{"type":"integer"}`
		}
		if got := asJSON(t, v.Properties["default"]); got != wantDefault {
			t.Errorf("%v/%v: default = %s", kind, typ, got)
		}
		options := v.Properties["options"]
		required := strings.Contains(strings.Join(v.Required, ","), "options")
		switch typ {
		case "text":
			if options.False || !required || asJSON(t, options) != `{"items":{"minLength":1,"type":"string"},"minItems":1,"type":"array"}` {
				t.Errorf("%v/text: options = %s, required %v", kind, asJSON(t, options), required)
			}
		default:
			if !options.False || required {
				t.Errorf("%v/int: options = %s, required %v", kind, asJSON(t, options), required)
			}
		}
	}
}

type badTag struct {
	A string `json:"a" minLenght:"1"`
}

type wrongTag struct {
	A int `json:"a" pattern:"x"`
}

// twice has two fields named a, its own and its embedded struct's, which
// encoding/json would choose between.
type twice struct {
	A string `json:"a"`
	alsoA
}

type alsoA struct {
	B string `json:"a"`
}

type noEnum struct {
	Type    string `json:"type"`
	Default typed  `json:"default" typeFrom:"type"`
}

type badWhen struct {
	Type string `json:"type" enum:"a,b"`
	X    string `json:"x,omitempty" when:"type=c"`
}

type idAndPattern struct {
	A string `json:"a" id:"node" pattern:"x"`
}

type anyField struct {
	A any `json:"a"`
}

// TestPanics checks that the generator panics on a type that it cannot read
// as its tags say, rather than write a schema that is wrong.
func TestPanics(t *testing.T) {
	cases := map[string]func(g *Generator){
		"an unknown tag":                   func(g *Generator) { g.Ref(reflect.TypeFor[badTag]()) },
		"a tag of another type":            func(g *Generator) { g.Ref(reflect.TypeFor[wrongTag]()) },
		"two fields of one name":           func(g *Generator) { g.Ref(reflect.TypeFor[twice]()) },
		"typeFrom of a field with no enum": func(g *Generator) { g.Ref(reflect.TypeFor[noEnum]()) },
		"when of a value of no enum":       func(g *Generator) { g.Ref(reflect.TypeFor[badWhen]()) },
		"an ID with a pattern":             func(g *Generator) { g.Ref(reflect.TypeFor[idAndPattern]()) },
		"an interface that is no union":    func(g *Generator) { g.Ref(reflect.TypeFor[anyField]()) },
		"a union of a non-interface":       func(g *Generator) { g.Union(reflect.TypeFor[circle](), "kind", "") },
		"a union declared twice": func(g *Generator) {
			g.Union(reflect.TypeFor[shape](), "kind", "")
		},
		"a definition that two own": func(g *Generator) {
			g.Define("circle", func() *Schema { return &Schema{} })
			g.Ref(reflect.TypeFor[circle]())
		},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			f(generator(false))
		})
	}
}

// TestDocument checks a document: its $id, its $ref, the definitions that
// its root reaches and no others, its examples, and its layout.
func TestDocument(t *testing.T) {
	g := generator(false)
	g.Define("Unreached", func() *Schema { return &Schema{Type: "string"} })
	d := Document{Path: "schema/test/v2/drawing.schema.json", Title: "Drawing", Description: "A drawing.",
		Root: reflect.TypeFor[drawing](), Examples: []string{`{"id": "bd_01HQ8Z3XK4F7NAWR5TCQ9M2BVD", "n": 1.50}`}}
	data, err := g.Document(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "}\n") || !strings.HasPrefix(string(data), "{\n  \"$defs\": {") {
		t.Errorf("the document is not laid out as /schema holds it: %.40q", data)
	}
	var doc struct {
		ID       string                     `json:"$id"`
		Ref      string                     `json:"$ref"`
		Defs     map[string]json.RawMessage `json:"$defs"`
		Examples []json.RawMessage          `json:"examples"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ID != BaseURL+d.Path || doc.Ref != "#/$defs/drawing" {
		t.Errorf("$id = %q, $ref = %q", doc.ID, doc.Ref)
	}
	var names []string
	for name := range doc.Defs {
		names = append(names, name)
	}
	for _, want := range []string{"AnyValue", "BoardId", "NodeId", "circle", "drawing", "setting", "shape", "square"} {
		if _, ok := doc.Defs[want]; !ok {
			t.Errorf("the document lacks %s: %v", want, names)
		}
	}
	if _, ok := doc.Defs["Unreached"]; ok {
		t.Error("the document holds a definition that its root does not reach")
	}
	if len(doc.Examples) != 1 || !strings.Contains(string(doc.Examples[0]), "1.50") {
		t.Errorf("the examples are %s, with their numbers as they are written", doc.Examples)
	}
	for _, bad := range []Document{
		{Path: "schema/test/drawing.schema.json", Root: d.Root},
		{Path: "schema/test/v1/drawing.json", Root: d.Root},
		{Path: d.Path, Root: d.Root, Examples: []string{`{"a":1} {}`}},
	} {
		if _, err := g.Document(bad); err == nil {
			t.Errorf("Document(%s, %q) did not fail", bad.Path, bad.Examples)
		}
	}
}

// TestSchemaJSON checks that a schema reads as it is written, and that one
// with a keyword that Schema does not have is refused.
func TestSchemaJSON(t *testing.T) {
	g := generator(false)
	g.Ref(reflect.TypeFor[drawing]())
	for name, s := range g.defs {
		data, err := s.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		var back Schema
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		again, err := back.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(data) {
			t.Errorf("%s is read back as %s, not %s", name, again, data)
		}
	}
	var s Schema
	if err := json.Unmarshal([]byte(`{"type":"string","format":"uri"}`), &s); err == nil {
		t.Error("a schema with format is read")
	}
}

// TestTypeScript checks the TypeScript of a document's definitions.
func TestTypeScript(t *testing.T) {
	g := generator(false)
	d := Document{Path: "schema/test/v1/drawing.schema.json", Title: "Drawing", Root: reflect.TypeFor[drawing]()}
	data, err := g.Document(d)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := TypeScript("// header\n", data, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"// header\n",
		"export type AnyValue = boolean | number | string | Array<AnyValue> | { [key: string]: AnyValue };",
		"export type BoardId = string;",
		"/** A circle. */\nexport interface circle {\n  kind: \"circle\";\n  label?: never;\n  /** The radius. */\n  radius: number;\n  side?: never;\n}",
		"export type shape = circle | square;",
		"  counts: { [key: string]: number };\n",
		"  meta?: { [key: string]: string };\n",
		"  tags?: Array<\"a\" | \"b\">;\n",
		"  version: 2;\n",
		"export type setting = {\n  default: number;\n",
	} {
		if !strings.Contains(string(ts), want) {
			t.Errorf("the TypeScript lacks %q:\n%s", want, ts)
		}
	}
	other := strings.Replace(string(data), `"maxItems": 3`, `"maxItems": 4`, 1)
	if _, err := TypeScript("", data, []byte(other)); err == nil {
		t.Error("two documents that define a definition differently are written")
	}
	reserved := strings.ReplaceAll(string(data), `"circle"`, `"string"`)
	reserved = strings.ReplaceAll(reserved, "#/$defs/circle", "#/$defs/string")
	if _, err := TypeScript("", []byte(reserved)); err == nil {
		t.Error("a definition named string is written")
	}
}
