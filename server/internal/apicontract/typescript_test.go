package apicontract

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the TypeScript generated from the fixture")

// fixtureTypeScriptPath is where the TypeScript generated from the fixture
// is committed: in web/shared, which compiles it, and whose client tests
// call its operations.
var fixtureTypeScriptPath = filepath.Join("..", "..", "..", "web", "shared", "src", "api", "testdata", "fixture.gen.ts")

const fixtureHeader = `// Generated from server/internal/apicontract/testdata/base.json. Do not
// edit; run "go test ./internal/apicontract -run TestTypeScript -update" in
// server/ instead.
`

// TestTypeScript checks the TypeScript generated from the fixture against
// the committed copy, which web/shared compiles. Run it with -update to
// regenerate it.
func TestTypeScript(t *testing.T) {
	got, err := TypeScript(load(t, "base.json"), fixtureHeader)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.MkdirAll(filepath.Dir(fixtureTypeScriptPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixtureTypeScriptPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(fixtureTypeScriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))) {
		t.Errorf("%s is out of date; run \"go test ./internal/apicontract -run TestTypeScript -update\" in server/",
			fixtureTypeScriptPath)
	}
	for _, s := range []string{
		"export interface Thing {",
		"  /** The thing's identifier */\n  id: string;",
		`  kind: "note" | "choice";`,
		"  note: string | null;",
		"  counts?: Record<string, number>;",
		"  parent?: Thing;",
		"  /** @deprecated */\n  \"old-name\"?: string;",
		"  tags: Array<string>;",
		"export interface ThingPage {",
		"   * Its *\\/ is escaped.",
		"  /** @deprecated */\n  deleteThing: {",
		"      path: {\n        id: string;\n      };",
		"      query?: {\n        archived?: boolean;",
		`        kind?: "note" | "choice";`,
		"        limit?: number;",
		"      headers?: {\n        \"If-None-Match\"?: string;\n      };",
		"      body: ThingInput;",
		"      201: Thing;",
		"      204: undefined;",
		"      304: undefined;",
		`  getThing: { method: "GET", path: "/things/{id}" },`,
	} {
		if !strings.Contains(string(got), s) {
			t.Errorf("the TypeScript lacks %q", s)
		}
	}
}

// TestTypeScriptRefuses checks that the generator refuses what the client
// cannot express, rather than write a type that is wrong.
func TestTypeScriptRefuses(t *testing.T) {
	list := func(d *Document) *Operation { return d.Paths["/things"].Get }
	create := func(d *Document) *Operation { return d.Paths["/things"].Post }
	tests := []struct {
		name   string
		change func(d *Document)
		want   string
	}{
		{"a cookie parameter", func(d *Document) {
			list(d).Parameters[0].In = "cookie"
		}, "cookie parameter cursor"},
		{"an array parameter", func(d *Document) {
			list(d).Parameters[0].Schema = &Schema{Type: Types{"array"}, Items: &Schema{Type: Types{"string"}}}
		}, "only strings, numbers, and booleans"},
		{"an object parameter", func(d *Document) {
			list(d).Parameters[0].Schema = &Schema{Ref: refPrefix + "Thing"}
		}, "only strings, numbers, and booleans"},
		{"a parameter of any type", func(d *Document) {
			list(d).Parameters[0].Schema = nil
		}, "only strings, numbers, and booleans"},
		{"a body that is not JSON", func(d *Document) {
			c := create(d).RequestBody.Content
			c["text/plain"] = c["application/json"]
			delete(c, "application/json")
		}, `the body may be ["text/plain"]`},
		{"a body in two formats", func(d *Document) {
			c := create(d).RequestBody.Content
			c["application/merge-patch+json"] = c["application/json"]
		}, "only application/json"},
		{"a response that is not JSON", func(d *Document) {
			create(d).Responses["201"].Content["text/plain"] = &MediaType{}
		}, "the response 201"},
		{"a range of statuses", func(d *Document) {
			create(d).Responses["2XX"] = create(d).Responses["201"]
		}, "a range of statuses, such as 2XX"},
		{"no response but errors", func(d *Document) {
			delete(create(d).Responses, "201")
		}, "no response other than an error's"},
		{"a schema named as a keyword", func(d *Document) {
			d.Components.Schemas["class"] = &Schema{Type: Types{"string"}}
		}, "schema class"},
		{"a schema named as a module's name", func(d *Document) {
			d.Components.Schemas["Operations"] = &Schema{Type: Types{"string"}}
		}, "schema Operations"},
		{"a schema whose name is not an identifier", func(d *Document) {
			d.Components.Schemas["my-thing"] = &Schema{Type: Types{"string"}}
		}, "schema my-thing"},
		{"properties and a schema of others", func(d *Document) {
			d.Components.Schemas["Thing"].AdditionalProperties = &Additional{Allowed: true,
				Schema: &Schema{Type: Types{"string"}}}
		}, "a schema of its other properties too"},
		{"an enum of objects", func(d *Document) {
			d.Components.Schemas["Thing"].Properties["kind"].Enum = []json.RawMessage{json.RawMessage(`{"a": 1}`)}
		}, "is not a string, number, boolean, or null"},
		{"oneOf with a type", func(d *Document) {
			d.Components.Schemas["Odd"] = &Schema{Type: Types{"string"}, OneOf: []*Schema{{Type: Types{"string"}}}}
		}, "oneOf, anyOf, or allOf has other keywords"},
		{"oneOf and allOf together", func(d *Document) {
			d.Components.Schemas["Odd"] = &Schema{OneOf: []*Schema{{}}, AllOf: []*Schema{{}}}
		}, "more than one of oneOf, anyOf, and allOf"},
		{"properties without a type", func(d *Document) {
			d.Components.Schemas["Odd"] = &Schema{Properties: map[string]*Schema{"a": {}}}
		}, "has no type"},
	}
	for _, tt := range tests {
		d := load(t, "base.json")
		tt.change(d)
		_, err := TypeScript(d, "")
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: TypeScript = %v, want an error saying %q", tt.name, err, tt.want)
		}
	}
}

// TestTypeScriptCompositions checks the types of schemas made of others.
func TestTypeScriptCompositions(t *testing.T) {
	str, num := &Schema{Type: Types{"string"}}, &Schema{Type: Types{"number"}}
	ref := &Schema{Ref: refPrefix + "Thing"}
	tests := []struct {
		schema *Schema
		want   string
	}{
		{&Schema{OneOf: []*Schema{str, num}}, "string | number"},
		{&Schema{AnyOf: []*Schema{str, {Type: Types{"string", "null"}}}}, "string | (string | null)"},
		{&Schema{AllOf: []*Schema{ref, {Type: Types{"object"}, Properties: map[string]*Schema{"a": str}}}},
			"Thing & {\n  a?: string;\n}"},
		{&Schema{Const: json.RawMessage(`-1.5`)}, "-1.5"},
		{&Schema{Type: Types{"integer", "number", "null"}}, "number | null"},
		{&Schema{Type: Types{"null", "boolean"}}, "boolean | null"},
		{&Schema{Type: Types{"object"}, AdditionalProperties: &Additional{}}, "Record<string, never>"},
		{&Schema{Type: Types{"object"}}, "Record<string, unknown>"},
		{&Schema{Type: Types{"array"}}, "Array<unknown>"},
		{&Schema{}, "unknown"},
	}
	g := &tsGen{d: load(t, "base.json")}
	for _, tt := range tests {
		got, err := g.typeOf(tt.schema, 0)
		if err != nil || got != tt.want {
			t.Errorf("typeOf(%+v) = %q, %v; want %q", tt.schema, got, err, tt.want)
		}
	}
}
