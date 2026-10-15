package apicontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// load returns the document in testdata/name, read afresh, so that a test
// may change it.
func load(t *testing.T, name string) *Document {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Load(b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func messages(changes []Change) []string {
	var out []string
	for _, c := range changes {
		out = append(out, c.String())
	}
	return out
}

// TestBreakingFixture compares the fixture with a version of it that removes
// a property from a response, and checks that the comparison fails on it.
//
// Verifies: FR-API-01/AC3
func TestBreakingFixture(t *testing.T) {
	got := messages(Breaking(load(t, "base.json"), load(t, "response-field-removed.json")))
	want := []string{"GET /things: the response 200 body.items[].name was removed"}
	if !slices.Equal(got, want) {
		t.Errorf("Breaking = %q, want %q", got, want)
	}
	if got := Breaking(load(t, "base.json"), load(t, "base.json")); len(got) != 0 {
		t.Errorf("the fixture breaks itself: %q", messages(got))
	}
}

func raw(s string) json.RawMessage {
	return json.RawMessage(s)
}

func ptr[T any](v T) *T {
	return &v
}

// TestBreaking checks each rule of Breaking on a change to the fixture.
func TestBreaking(t *testing.T) {
	schema := func(d *Document, name string) *Schema { return d.Components.Schemas[name] }
	thing := func(d *Document) *Schema { return schema(d, "Thing") }
	input := func(d *Document) *Schema { return schema(d, "ThingInput") }
	list := func(d *Document) *Operation { return d.Paths["/things"].Get }
	create := func(d *Document) *Operation { return d.Paths["/things"].Post }
	get := func(d *Document) *Operation { return d.Paths["/things/{id}"].Get }
	param := func(op *Operation, name string) *Parameter {
		for _, p := range op.Parameters {
			if p.Name == name {
				return p
			}
		}
		panic("no parameter " + name)
	}
	envelope := map[string]*MediaType{"application/json": {Schema: &Schema{Ref: refPrefix + "ErrorEnvelope"}}}
	const listed = "GET /things: the response 200 body.items[]."

	tests := []struct {
		name   string
		change func(d *Document)
		want   []string
	}{
		{"nothing changes", func(*Document) {}, nil},
		{"descriptions, operation IDs, and deprecation change", func(d *Document) {
			list(d).OperationID, list(d).Summary, list(d).Deprecated = "listAll", "Other", true
			thing(d).Description, thing(d).Properties["id"].Description = "", "Other"
		}, nil},
		{"an operation is added", func(d *Document) {
			d.Paths["/other"] = &PathItem{Get: get(d)}
		}, nil},

		// Responses.
		{"a response property is no longer required", func(d *Document) {
			thing(d).Required = slices.DeleteFunc(thing(d).Required, func(s string) bool { return s == "kind" })
		}, []string{listed + "kind is no longer always present"}},
		{"a response property changes type", func(d *Document) {
			thing(d).Properties["name"].Type = Types{"integer"}
		}, []string{listed + "name: the type changed from string to integer"}},
		{"a response property becomes nullable", func(d *Document) {
			thing(d).Properties["kind"].Type = Types{"string", "null"}
		}, []string{listed + "kind: the type changed from string to string or null"}},
		{"a response property is no longer nullable", func(d *Document) {
			thing(d).Properties["note"].Type = Types{"string"}
		}, nil},
		{"a response enum gains a value", func(d *Document) {
			thing(d).Properties["kind"].Enum = append(thing(d).Properties["kind"].Enum, raw(`"jumper"`))
		}, []string{listed + `kind: the values "jumper" were added`}},
		{"a response enum loses a value", func(d *Document) {
			thing(d).Properties["kind"].Enum = thing(d).Properties["kind"].Enum[:1]
		}, nil},
		{"a response enum is removed", func(d *Document) {
			thing(d).Properties["kind"].Enum = nil
		}, []string{listed + `kind: it is no longer limited to "note", "choice"`}},
		{"a response gains a property", func(d *Document) {
			thing(d).Properties["colour"] = &Schema{Type: Types{"string"}}
			thing(d).Required = append(thing(d).Required, "colour")
		}, nil},
		{"a response's maxLength is removed", func(d *Document) {
			thing(d).Properties["name"].MaxLength = nil
		}, []string{listed + "name: maxLength changed from 100 to none"}},
		{"a response's maxLength falls", func(d *Document) {
			thing(d).Properties["name"].MaxLength = ptr(50)
		}, nil},
		{"a response's format is removed", func(d *Document) {
			thing(d).Properties["created_at"].Format = ""
		}, []string{listed + `created_at: format changed from "date-time" to none`}},
		{"a response's items change type", func(d *Document) {
			thing(d).Properties["tags"].Items.Type = Types{"integer"}
		}, []string{listed + "tags[]: the type changed from string to integer"}},
		{"a response map's values change type", func(d *Document) {
			thing(d).Properties["counts"].AdditionalProperties.Schema.Type = Types{"string"}
		}, []string{listed + "counts.*: the type changed from integer to string"}},
		{"a response header is removed", func(d *Document) {
			delete(get(d).Responses["200"].Headers, "ETag")
		}, []string{"GET /things/{id}: the response 200 no longer has the header ETag"}},
		{"a response header changes case", func(d *Document) {
			h := get(d).Responses["200"].Headers
			h["Etag"] = h["ETag"]
			delete(h, "ETag")
		}, nil},
		{"a response gains a header", func(d *Document) {
			get(d).Responses["200"].Headers["X-Thing"] = &Header{Required: true, Schema: &Schema{Type: Types{"string"}}}
		}, nil},
		{"a response header is serialised differently", func(d *Document) {
			get(d).Responses["200"].Headers["ETag"].Explode = ptr(true)
		}, []string{"GET /things/{id}: the response 200's header ETag is now serialised as simple true, and was as " +
			"simple false"}},
		{"a response may have a new status", func(d *Document) {
			d.Paths["/things/{id}"].Delete.Responses["200"] = &Response{Description: "OK"}
		}, []string{"DELETE /things/{id}: the response may now have the status 200"}},
		{"an error status is added", func(d *Document) {
			get(d).Responses["404"] = &Response{Description: "Not Found", Content: envelope}
		}, nil},
		{"an error status has another body", func(d *Document) {
			get(d).Responses["404"] = &Response{Description: "Not Found",
				Content: map[string]*MediaType{"application/json": {Schema: &Schema{Type: Types{"string"}}}}}
		}, []string{"GET /things/{id}: the response 404 body: the type changed from object to string",
			"GET /things/{id}: the response 404 body.error was removed"}},
		{"a status is removed", func(d *Document) {
			delete(get(d).Responses, "304")
		}, nil},
		{"a response may be in a new format", func(d *Document) {
			create(d).Responses["201"].Content["text/plain"] = &MediaType{}
		}, []string{"POST /things: the response 201 may now be text/plain"}},
		{"a response loses its body", func(d *Document) {
			get(d).Responses["200"].Content = nil
		}, []string{"GET /things/{id}: the response 200 is no longer application/json"}},

		// Operations and parameters.
		{"an operation is removed", func(d *Document) {
			d.Paths["/things/{id}"].Delete = nil
		}, []string{"DELETE /things/{id}: the operation was removed"}},
		{"a path changes", func(d *Document) {
			d.Paths["/stuff"] = d.Paths["/things"]
			delete(d.Paths, "/things")
		}, []string{"GET /things: the operation was removed", "POST /things: the operation was removed"}},
		{"a path parameter is renamed", func(d *Document) {
			item := d.Paths["/things/{id}"]
			delete(d.Paths, "/things/{id}")
			d.Paths["/things/{thing_id}"] = item
			param(item.Get, "id").Name = "thing_id"
			param(item.Delete, "id").Name = "thing_id"
		}, nil},
		{"a query parameter is removed", func(d *Document) {
			list(d).Parameters = slices.DeleteFunc(list(d).Parameters, func(p *Parameter) bool {
				return p.Name == "archived"
			})
		}, []string{"GET /things: the query parameter archived was removed"}},
		{"a query parameter becomes required", func(d *Document) {
			param(list(d), "kind").Required = true
		}, []string{"GET /things: the query parameter kind is now required"}},
		{"a parameter is new and required", func(d *Document) {
			list(d).Parameters = append(list(d).Parameters, &Parameter{Name: "X-Thing", In: "header", Required: true,
				Schema: &Schema{Type: Types{"string"}}})
		}, []string{"GET /things: the header parameter X-Thing is new and required"}},
		{"a parameter is new and optional", func(d *Document) {
			list(d).Parameters = append(list(d).Parameters, &Parameter{Name: "q", In: "query",
				Schema: &Schema{Type: Types{"string"}}})
		}, nil},
		{"a parameter loses an enum value", func(d *Document) {
			param(list(d), "kind").Schema.Enum = param(list(d), "kind").Schema.Enum[:1]
		}, []string{`GET /things: the query parameter kind: the values "choice" were removed`}},
		{"a parameter gains an enum value", func(d *Document) {
			p := param(list(d), "kind")
			p.Schema.Enum = append(p.Schema.Enum, raw(`"jumper"`))
		}, nil},
		{"a parameter gains an enum", func(d *Document) {
			param(list(d), "cursor").Schema.Enum = []json.RawMessage{raw(`"a"`)}
		}, []string{`GET /things: the query parameter cursor: it is now limited to "a"`}},
		{"a parameter's minimum rises", func(d *Document) {
			param(list(d), "limit").Schema.Minimum = ptr(2.0)
		}, []string{"GET /things: the query parameter limit: the lower bound changed from 1 to 2"}},
		{"a parameter's minimum becomes exclusive", func(d *Document) {
			s := param(list(d), "limit").Schema
			s.Minimum, s.ExclusiveMinimum = nil, ptr(1.0)
		}, []string{"GET /things: the query parameter limit: the lower bound changed from 1 to 1 (exclusive)"}},
		{"a parameter's maximum falls", func(d *Document) {
			param(list(d), "limit").Schema.Maximum = ptr(100.0)
		}, []string{"GET /things: the query parameter limit: the upper bound changed from 200 to 100"}},
		{"a parameter's limits loosen", func(d *Document) {
			s := param(list(d), "limit").Schema
			s.Minimum, s.Maximum = ptr(0.0), nil
		}, nil},
		{"a parameter's default changes", func(d *Document) {
			param(list(d), "limit").Schema.Default = raw("20")
		}, []string{"GET /things: the query parameter limit: the default changed from 50 to 20"}},
		{"a parameter accepts any number", func(d *Document) {
			param(list(d), "limit").Schema.Type = Types{"number"}
		}, nil},
		{"a parameter changes type", func(d *Document) {
			param(list(d), "archived").Schema.Type = Types{"string"}
		}, []string{"GET /things: the query parameter archived: the type changed from boolean to string"}},
		{"a header parameter changes case", func(d *Document) {
			param(get(d), "If-None-Match").Name = "if-none-match"
		}, nil},
		{"a parameter is serialised differently", func(d *Document) {
			param(list(d), "archived").Explode = ptr(false)
		}, []string{"GET /things: the query parameter archived is now serialised as form false, and was as form true"}},
		{"a parameter's default serialisation is spelled out", func(d *Document) {
			p := param(list(d), "archived")
			p.Style, p.Explode = "form", ptr(true)
		}, nil},

		// Requests.
		{"a request property becomes required", func(d *Document) {
			input(d).Required = append(input(d).Required, "size")
		}, []string{"POST /things: the request body.size is now required"}},
		{"a request property is new and required", func(d *Document) {
			input(d).Properties["colour"] = &Schema{Type: Types{"string"}}
			input(d).Required = append(input(d).Required, "colour")
		}, []string{"POST /things: the request body.colour is new and required"}},
		{"a request property is new and optional", func(d *Document) {
			input(d).Properties["colour"] = &Schema{Type: Types{"string"}}
		}, nil},
		{"a request property is removed", func(d *Document) {
			delete(input(d).Properties, "note")
		}, []string{"POST /things: the request body.note was removed, and is rejected"}},
		{"a request property is no longer nullable", func(d *Document) {
			input(d).Properties["note"].Type = Types{"string"}
		}, []string{"POST /things: the request body.note: the type changed from string or null to string"}},
		{"a request's minLength rises", func(d *Document) {
			input(d).Properties["name"].MinLength = ptr(2)
		}, []string{"POST /things: the request body.name: minLength changed from 1 to 2"}},
		{"a request gains a pattern", func(d *Document) {
			input(d).Properties["name"].Pattern = "^[a-z]+$"
		}, []string{`POST /things: the request body.name: pattern changed from none to "^[a-z]+$"`}},
		{"a request's limits loosen", func(d *Document) {
			p := input(d).Properties["name"]
			p.MinLength, p.MaxLength = nil, ptr(200)
		}, nil},
		{"a request's enum gains a value", func(d *Document) {
			p := input(d).Properties["kind"]
			p.Enum = append(p.Enum, raw(`"jumper"`))
		}, nil},
		{"a request's encoding changes", func(d *Document) {
			input(d).Properties["note"].ContentEncoding = "base64"
		}, []string{`POST /things: the request body.note: contentEncoding changed from none to "base64"`}},
		{"a request body is removed", func(d *Document) {
			create(d).RequestBody = nil
		}, []string{"POST /things: the request body was removed"}},
		{"a request body becomes optional", func(d *Document) {
			create(d).RequestBody.Required = false
		}, nil},
		{"a request body is new and required", func(d *Document) {
			get(d).RequestBody = &RequestBody{Required: true, Content: map[string]*MediaType{"application/json": {}}}
		}, []string{"GET /things/{id}: the request body is new and required"}},
		{"a request body changes format", func(d *Document) {
			c := create(d).RequestBody.Content
			c["application/merge-patch+json"] = c["application/json"]
			delete(c, "application/json")
		}, []string{"POST /things: the request body is no longer taken as application/json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			head := load(t, "base.json")
			tt.change(head)
			if got := messages(Breaking(load(t, "base.json"), head)); !slices.Equal(got, tt.want) {
				t.Errorf("Breaking = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBreakingFromChangedBase checks the rules that need a base other than
// the fixture: a header that the response always had, and the properties
// that huma does not require, which are readOnly in a request, and writeOnly
// in a response.
func TestBreakingFromChangedBase(t *testing.T) {
	etag := func(d *Document) *Header { return d.Paths["/things/{id}"].Get.Responses["200"].Headers["ETag"] }
	prop := func(d *Document, schema, name string) *Schema { return d.Components.Schemas[schema].Properties[name] }
	none := func(*Document) {}
	tests := []struct {
		name       string
		base, head func(d *Document)
		want       []string
	}{
		{"a response header is no longer always present", func(d *Document) {
			etag(d).Required = true
		}, none, []string{"GET /things/{id}: the response 200 no longer always has the header ETag"}},
		{"a response header becomes always present", none, func(d *Document) {
			etag(d).Required = true
		}, nil},
		{"a required request property is no longer readOnly", func(d *Document) {
			prop(d, "ThingInput", "name").ReadOnly = true
		}, none, []string{"POST /things: the request body.name is now required"}},
		{"a required request property becomes readOnly", none, func(d *Document) {
			prop(d, "ThingInput", "name").ReadOnly = true
		}, nil},
		{"a required response property becomes writeOnly", none, func(d *Document) {
			prop(d, "Thing", "kind").WriteOnly = true
		}, []string{"GET /things: the response 200 body.items[].kind is no longer always present"}},
		{"a required response property is no longer writeOnly", func(d *Document) {
			prop(d, "Thing", "kind").WriteOnly = true
		}, none, nil},
		{"a request property is new, required, and readOnly", none, func(d *Document) {
			input := d.Components.Schemas["ThingInput"]
			input.Properties["colour"] = &Schema{Type: Types{"string"}, ReadOnly: true}
			input.Required = append(input.Required, "colour")
		}, nil},

		// Formats and multipleOf that widen or narrow.
		{"a request's format widens", func(d *Document) {
			prop(d, "ThingInput", "size").Format = "int32"
		}, func(d *Document) {
			prop(d, "ThingInput", "size").Format = "int64"
		}, nil},
		{"a request's format narrows", func(d *Document) {
			prop(d, "ThingInput", "size").Format = "double"
		}, func(d *Document) {
			prop(d, "ThingInput", "size").Format = "float"
		}, []string{`POST /things: the request body.size: format changed from "double" to "float"`}},
		{"a response's format narrows", func(d *Document) {
			prop(d, "Thing", "counts").AdditionalProperties.Schema.Format = "int64"
		}, func(d *Document) {
			prop(d, "Thing", "counts").AdditionalProperties.Schema.Format = "int32"
		}, nil},
		{"a response's format widens", func(d *Document) {
			prop(d, "Thing", "counts").AdditionalProperties.Schema.Format = "int32"
		}, func(d *Document) {
			prop(d, "Thing", "counts").AdditionalProperties.Schema.Format = "int64"
		}, []string{`GET /things: the response 200 body.items[].counts.*: format changed from "int32" to "int64"`}},
		{"a request's multipleOf changes to a divisor", func(d *Document) {
			prop(d, "ThingInput", "size").MultipleOf = ptr(0.3)
		}, func(d *Document) {
			prop(d, "ThingInput", "size").MultipleOf = ptr(0.1)
		}, nil},
		{"a request's multipleOf changes to a multiple", func(d *Document) {
			prop(d, "ThingInput", "size").MultipleOf = ptr(2.0)
		}, func(d *Document) {
			prop(d, "ThingInput", "size").MultipleOf = ptr(4.0)
		}, []string{"POST /things: the request body.size: multipleOf changed from \"2\" to \"4\""}},
		{"a request's multipleOf changes to another", func(d *Document) {
			prop(d, "ThingInput", "size").MultipleOf = ptr(2.0)
		}, func(d *Document) {
			prop(d, "ThingInput", "size").MultipleOf = ptr(3.0)
		}, []string{"POST /things: the request body.size: multipleOf changed from \"2\" to \"3\""}},
		{"a response's multipleOf changes to a multiple", func(d *Document) {
			prop(d, "Thing", "counts").AdditionalProperties.Schema.MultipleOf = ptr(1.0)
		}, func(d *Document) {
			prop(d, "Thing", "counts").AdditionalProperties.Schema.MultipleOf = ptr(2.0)
		}, nil},
		{"a response's multipleOf changes to a divisor", func(d *Document) {
			prop(d, "Thing", "counts").AdditionalProperties.Schema.MultipleOf = ptr(2.0)
		}, func(d *Document) {
			prop(d, "Thing", "counts").AdditionalProperties.Schema.MultipleOf = ptr(1.0)
		}, []string{"GET /things: the response 200 body.items[].counts.*: multipleOf changed from \"2\" to \"1\""}},
	}
	for _, tt := range tests {
		base, head := load(t, "base.json"), load(t, "base.json")
		tt.base(base)
		tt.head(head)
		if got := messages(Breaking(base, head)); !slices.Equal(got, tt.want) {
			t.Errorf("%s: Breaking = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestBreakingAdditionalProperties checks the rules for the properties that
// a schema does not name, which huma's structs reject and its maps take.
func TestBreakingAdditionalProperties(t *testing.T) {
	object := func(additional *Additional, props map[string]*Schema) *Document {
		return &Document{
			OpenAPI: "3.1.0",
			Paths: map[string]*PathItem{"/x": {Post: &Operation{
				OperationID: "postX",
				RequestBody: &RequestBody{Content: map[string]*MediaType{"application/json": {Schema: &Schema{
					Type: Types{"object"}, Properties: props, AdditionalProperties: additional,
				}}}},
				Responses: map[string]*Response{"200": {Content: map[string]*MediaType{"application/json": {Schema: &Schema{
					Type: Types{"object"}, Properties: props, AdditionalProperties: additional,
				}}}}},
			}}},
		}
	}
	str := &Schema{Type: Types{"string"}}
	integer := &Schema{Type: Types{"integer"}}
	tests := []struct {
		name       string
		base, head *Document
		want       []string
	}{
		{"a map starts rejecting other properties", object(&Additional{Allowed: true, Schema: str}, nil),
			object(&Additional{}, nil), []string{
				"POST /x: the request body: properties that it does not name are now rejected",
			}},
		{"a map's values change type", object(&Additional{Allowed: true, Schema: str}, nil),
			object(&Additional{Allowed: true, Schema: integer}, nil), []string{
				"POST /x: the request body.*: the type changed from string to integer",
				"POST /x: the response 200 body.*: the type changed from string to integer",
			}},
		{"an object names a property that it took as another", object(nil, nil),
			object(nil, map[string]*Schema{"name": str}), []string{
				"POST /x: the request body.name: the type changed from any type to string",
			}},
		{"an object stops naming a property that it takes as another",
			object(&Additional{Allowed: true, Schema: str}, map[string]*Schema{"name": str}),
			object(&Additional{Allowed: true, Schema: str}, nil), []string{
				"POST /x: the response 200 body.name was removed",
			}},
		{"a response starts taking other properties", object(&Additional{}, nil),
			object(&Additional{Allowed: true}, nil), nil},
		{"a map names a property whose values it did not take", object(&Additional{Allowed: true, Schema: integer}, nil),
			object(&Additional{Allowed: true, Schema: integer}, map[string]*Schema{"total": str}), []string{
				"POST /x: the request body.total: the type changed from integer to string",
				"POST /x: the response 200 body.total: the type changed from integer to string",
			}},
		{"a map names a property of its values' type", object(&Additional{Allowed: true, Schema: integer}, nil),
			object(&Additional{Allowed: true, Schema: integer}, map[string]*Schema{"total": integer}), nil},
	}
	for _, tt := range tests {
		if got := messages(Breaking(tt.base, tt.head)); !slices.Equal(got, tt.want) {
			t.Errorf("%s: Breaking = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestBreakingThroughAnAlias checks that a named schema that only refers to
// another, as an alias does, is compared as the schema it names.
func TestBreakingThroughAnAlias(t *testing.T) {
	doc := func(nameType string) *Document {
		return &Document{
			OpenAPI: "3.1.0",
			Paths: map[string]*PathItem{"/x": {Get: &Operation{
				OperationID: "getX",
				Responses: map[string]*Response{"200": {Content: map[string]*MediaType{"application/json": {
					Schema: &Schema{Ref: refPrefix + "Alias"},
				}}}},
			}}},
			Components: &Components{Schemas: map[string]*Schema{
				"Alias": {Ref: refPrefix + "Target"},
				"Target": {Type: Types{"object"}, Required: []string{"name"},
					Properties: map[string]*Schema{"name": {Type: Types{nameType}}}},
			}},
		}
	}
	want := []string{"GET /x: the response 200 body.name: the type changed from string to integer"}
	if got := messages(Breaking(doc("string"), doc("integer"))); !slices.Equal(got, want) {
		t.Errorf("Breaking = %q, want %q", got, want)
	}

	// A document built in code, which Load has not checked, may refer to a
	// schema without having any; the reference allows any value.
	bare := doc("string")
	bare.Components = nil
	if got := messages(Breaking(bare, bare)); len(got) != 0 {
		t.Errorf("without components: Breaking = %q, want nothing", got)
	}
	if got := messages(Breaking(bare, doc("string"))); len(got) != 0 {
		t.Errorf("a reference that gains its schema: Breaking = %q, want nothing", got)
	}
}
