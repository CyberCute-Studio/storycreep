package apicontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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

// inThings returns the change msg to a Thing of the fixture as Breaking
// reports it: for each operation whose response holds a Thing.
func inThings(msg string) []string {
	return []string{
		"GET /things: the response 200 body.items[]." + msg,
		"POST /things: the response 201 body." + msg,
		"GET /things/{id}: the response 200 body." + msg,
	}
}

// TestBreakingFixture compares the fixture with a version of it that removes
// a property from a response, and checks that the comparison fails on it, for
// each operation that answers with the property.
//
// Verifies: FR-API-01/AC3
func TestBreakingFixture(t *testing.T) {
	got := messages(Breaking(load(t, "base.json"), load(t, "response-field-removed.json")))
	want := inThings("name was removed")
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
		}, inThings("kind is no longer always present")},
		{"a response property changes type", func(d *Document) {
			thing(d).Properties["name"].Type = Types{"integer"}
		}, inThings("name: the type changed from string to integer")},
		{"a response property becomes nullable", func(d *Document) {
			thing(d).Properties["kind"].Type = Types{"string", "null"}
		}, inThings("kind: the type changed from string to string or null")},
		{"a response property is no longer nullable", func(d *Document) {
			thing(d).Properties["note"].Type = Types{"string"}
		}, nil},
		{"a response enum gains a value", func(d *Document) {
			thing(d).Properties["kind"].Enum = append(thing(d).Properties["kind"].Enum, raw(`"jumper"`))
		}, inThings(`kind: the values "jumper" were added`)},
		{"a response enum loses a value", func(d *Document) {
			thing(d).Properties["kind"].Enum = thing(d).Properties["kind"].Enum[:1]
		}, nil},
		{"a response enum is removed", func(d *Document) {
			thing(d).Properties["kind"].Enum = nil
		}, inThings(`kind: it is no longer limited to "note", "choice"`)},
		{"a response gains a property", func(d *Document) {
			thing(d).Properties["colour"] = &Schema{Type: Types{"string"}}
			thing(d).Required = append(thing(d).Required, "colour")
		}, nil},
		{"a response's maxLength is removed", func(d *Document) {
			thing(d).Properties["name"].MaxLength = nil
		}, inThings("name: maxLength changed from 100 to none")},
		{"a response's maxLength falls", func(d *Document) {
			thing(d).Properties["name"].MaxLength = ptr(50)
		}, nil},
		{"a response's format is removed", func(d *Document) {
			thing(d).Properties["created_at"].Format = ""
		}, inThings(`created_at: format changed from "date-time" to none`)},
		{"a response's items change type", func(d *Document) {
			thing(d).Properties["tags"].Items.Type = Types{"integer"}
		}, inThings("tags[]: the type changed from string to integer")},
		{"a response map's values change type", func(d *Document) {
			thing(d).Properties["counts"].AdditionalProperties.Schema.Type = Types{"string"}
		}, inThings("counts.*: the type changed from integer to string")},
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
		{"an operation gains If-None-Match and a 304", func(d *Document) {
			list(d).Parameters = append(list(d).Parameters, &Parameter{Name: "If-None-Match", In: "header",
				Schema: &Schema{Type: Types{"string"}}})
			list(d).Responses["304"] = &Response{Description: "Not Modified"}
		}, nil},
		{"an operation gains If-Modified-Since and a 304", func(d *Document) {
			list(d).Parameters = append(list(d).Parameters, &Parameter{Name: "If-Modified-Since", In: "header",
				Schema: &Schema{Type: Types{"string"}}})
			list(d).Responses["304"] = &Response{Description: "Not Modified"}
		}, nil},
		{"a success status widens to its range", func(d *Document) {
			r := get(d).Responses
			r["2XX"] = r["200"]
			delete(r, "200")
		}, []string{"GET /things/{id}: the response may now have the status 2XX"}},
		{"an error range has another body", func(d *Document) {
			get(d).Responses["4XX"] = &Response{Description: "Client error",
				Content: map[string]*MediaType{"application/json": {Schema: &Schema{Type: Types{"string"}}}}}
		}, []string{"GET /things/{id}: the response 4XX body: the type changed from object to string",
			"GET /things/{id}: the response 4XX body.error was removed"}},
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
		// huma's validator lets null through for a property that a request
		// does not require.
		{"an optional request property is no longer nullable", func(d *Document) {
			input(d).Properties["note"].Type = Types{"string"}
		}, nil},
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
// the fixture: a header that the response always had; the properties that
// huma does not require, which are readOnly in a request, and writeOnly in a
// response; a 304 where base took If-None-Match; a range of statuses;
// formats and multipleOf that widen or narrow; nullable enums; the null that
// a request may send for a property that it need not have; the types of an
// enum's values; and oneOf.
func TestBreakingFromChangedBase(t *testing.T) {
	etag := func(d *Document) *Header { return d.Paths["/things/{id}"].Get.Responses["200"].Headers["ETag"] }
	prop := func(d *Document, schema, name string) *Schema { return d.Components.Schemas[schema].Properties[name] }
	// requireInput makes ThingInput require the property name, and returns
	// its schema.
	requireInput := func(d *Document, name string) *Schema {
		input := d.Components.Schemas["ThingInput"]
		if !slices.Contains(input.Required, name) {
			input.Required = append(input.Required, name)
		}
		return input.Properties[name]
	}
	none := func(*Document) {}
	// noteOf makes Thing's note one of the types.
	noteOf := func(types ...string) func(d *Document) {
		return func(d *Document) {
			var members []*Schema
			for _, t := range types {
				members = append(members, &Schema{Type: Types{t}})
			}
			*prop(d, "Thing", "note") = Schema{OneOf: members}
		}
	}
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
		{"a 304 is new where a client could send If-None-Match", func(d *Document) {
			delete(d.Paths["/things/{id}"].Get.Responses, "304")
		}, none, []string{"GET /things/{id}: the response may now have the status 304"}},
		{"a success range narrows to one of its statuses", func(d *Document) {
			r := d.Paths["/things/{id}"].Get.Responses
			r["2XX"] = r["200"]
			delete(r, "200")
		}, none, nil},
		{"a required request property is no longer readOnly", func(d *Document) {
			prop(d, "ThingInput", "name").ReadOnly = true
		}, none, []string{"POST /things: the request body.name is now required"}},
		{"a required request property becomes readOnly", none, func(d *Document) {
			prop(d, "ThingInput", "name").ReadOnly = true
		}, nil},
		{"a required response property becomes writeOnly", none, func(d *Document) {
			prop(d, "Thing", "kind").WriteOnly = true
		}, inThings("kind is no longer always present")},
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
		}, inThings(`counts.*: format changed from "int32" to "int64"`)},
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
		}, inThings(`counts.*: multipleOf changed from "2" to "1"`)},

		// Nullable enums, which take null if their type does, listed or not,
		// in a property that a request requires.
		{"a response's nullable enum lists null", func(d *Document) {
			prop(d, "Thing", "kind").Type = Types{"string", "null"}
		}, func(d *Document) {
			k := prop(d, "Thing", "kind")
			k.Type, k.Enum = Types{"string", "null"}, append(k.Enum, raw("null"))
		}, nil},
		{"a request's nullable enum stops listing null", func(d *Document) {
			k := requireInput(d, "kind")
			k.Type, k.Enum = Types{"string", "null"}, append(k.Enum, raw("null"))
		}, func(d *Document) {
			requireInput(d, "kind").Type = Types{"string", "null"}
		}, nil},
		{"a request's enum without a type stops listing null", func(d *Document) {
			k := requireInput(d, "kind")
			k.Type, k.Enum = nil, append(k.Enum, raw("null"))
		}, func(d *Document) {
			requireInput(d, "kind").Type = nil
		}, []string{"POST /things: the request body.kind: the values null were removed"}},

		// huma's validator lets null through for a property that a request
		// does not require, readOnly or not, whatever its schema.
		{"a required request property is no longer nullable", func(d *Document) {
			prop(d, "ThingInput", "name").Type = Types{"string", "null"}
		}, none, []string{"POST /things: the request body.name: the type changed from string or null to string"}},
		{"a required readOnly request property is no longer nullable", func(d *Document) {
			name := prop(d, "ThingInput", "name")
			name.Type, name.ReadOnly = Types{"string", "null"}, true
		}, func(d *Document) {
			prop(d, "ThingInput", "name").ReadOnly = true
		}, []string{"POST /things: the request body.name: the type changed from string or null to string"}},
		{"an optional request property's enum without a type stops listing null", func(d *Document) {
			k := prop(d, "ThingInput", "kind")
			k.Type, k.Enum = nil, append(k.Enum, raw("null"))
		}, func(d *Document) {
			prop(d, "ThingInput", "kind").Type = nil
		}, nil},
		{"an optional request property's oneOf member null changes type", func(d *Document) {
			*prop(d, "ThingInput", "note") = Schema{OneOf: []*Schema{{Type: Types{"string"}}, {Type: Types{"null"}}}}
		}, func(d *Document) {
			*prop(d, "ThingInput", "note") = Schema{OneOf: []*Schema{{Type: Types{"string"}}, {Type: Types{"integer"}}}}
		}, nil},
		{"a request property that becomes optional is no longer nullable", func(d *Document) {
			requireInput(d, "note")
		}, func(d *Document) {
			prop(d, "ThingInput", "note").Type = Types{"string"}
		}, nil},

		// An enum or a const allows only the types of its values.
		{"a request's enum gains the type of its values", func(d *Document) {
			prop(d, "ThingInput", "kind").Type = nil
		}, none, nil},
		{"a request's const gains the type of its value", func(d *Document) {
			size := prop(d, "ThingInput", "size")
			size.Type, size.Const = nil, raw("2")
		}, func(d *Document) {
			prop(d, "ThingInput", "size").Const = raw("2")
		}, nil},
		{"a response's enum allows another type", none, func(d *Document) {
			prop(d, "Thing", "kind").Type = Types{"string", "integer"}
		}, nil},
		{"a request's enum value is not of the new type", func(d *Document) {
			k := prop(d, "ThingInput", "kind")
			k.Type, k.Enum = nil, append(k.Enum, raw("1.5"))
		}, func(d *Document) {
			k := prop(d, "ThingInput", "kind")
			k.Type, k.Enum = Types{"string", "integer"}, append(k.Enum, raw("1.5"))
		}, []string{"POST /things: the request body.kind: the type changed from any type to string or integer"}},

		// Schemas combined with oneOf, member by member.
		{"a response's oneOf is the same", noteOf("string", "null"), noteOf("string", "null"), nil},
		{"a response's oneOf gains a member", noteOf("string", "null"), noteOf("string", "null", "integer"),
			inThings("note: oneOf changed from 2 schemas to 3")},
		{"a response's oneOf member changes type", noteOf("string", "null"), noteOf("integer", "null"),
			inThings("note.oneOf[0]: the type changed from string to integer")},
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

// TestBreakingUnions checks the rules for a oneOf or an anyOf whose members
// all refer to named schemas, as the command endpoint's union of the types of
// command does: the members are matched by name, a request's may gain
// members, provided that a oneOf's new one takes no value that another took,
// and a response's may lose them.
func TestBreakingUnions(t *testing.T) {
	// tagged returns an object whose required property type has the one
	// value v, a JSON value, as a command's type does.
	tagged := func(v string) *Schema {
		return &Schema{Type: Types{"object"}, Required: []string{"type"}, AdditionalProperties: &Additional{},
			Properties: map[string]*Schema{"type": {Enum: []json.RawMessage{raw(v)}}}}
	}
	// doc returns a document whose operation takes and answers with one of the
	// members, or any of them, as keyword says: a name refers to the schema
	// of that name, which is tagged with the name in lower case unless named
	// gives it.
	doc := func(keyword string, named map[string]*Schema, members ...string) *Document {
		schemas := map[string]*Schema{}
		union := func() *Schema {
			var refs []*Schema
			for _, m := range members {
				refs = append(refs, &Schema{Ref: refPrefix + m})
				schemas[m] = named[m]
				if schemas[m] == nil {
					schemas[m] = tagged(strconv.Quote(strings.ToLower(m)))
				}
			}
			if keyword == "anyOf" {
				return &Schema{AnyOf: refs}
			}
			return &Schema{OneOf: refs}
		}
		return &Document{
			OpenAPI: "3.1.0",
			Paths: map[string]*PathItem{"/x": {Post: &Operation{
				OperationID: "postX",
				RequestBody: &RequestBody{Content: map[string]*MediaType{"application/json": {Schema: union()}}},
				Responses: map[string]*Response{"200": {Content: map[string]*MediaType{"application/json": {
					Schema: union(),
				}}}},
			}}},
			Components: &Components{Schemas: schemas},
		}
	}
	oneOf := func(members ...string) *Document { return doc("oneOf", nil, members...) }
	anyOf := func(members ...string) *Document { return doc("anyOf", nil, members...) }
	// withD is oneOf of the members, where D is the schema d.
	withD := func(d *Schema, members ...string) *Document {
		return doc("oneOf", map[string]*Schema{"D": d}, members...)
	}
	needsNote := tagged(`"b"`)
	needsNote.Properties["note"] = &Schema{Type: Types{"string"}}
	needsNote.Required = append(needsNote.Required, "note")
	readOnlyType := tagged(`"d"`)
	readOnlyType.Properties["type"].ReadOnly = true

	tests := []struct {
		name       string
		base, head *Document
		want       []string
	}{
		{"a oneOf is the same", oneOf("A", "B"), oneOf("A", "B"), nil},
		{"a oneOf's members move", oneOf("A", "B", "C"), oneOf("C", "A", "B"), nil},
		{"a oneOf gains a member", oneOf("A", "B"), oneOf("A", "B", "C"), []string{
			"POST /x: the response 200 body.oneOf[C] was added",
		}},
		{"a oneOf gains a member before others", oneOf("A", "C"), oneOf("A", "B", "C"), []string{
			"POST /x: the response 200 body.oneOf[B] was added",
		}},
		{"a oneOf loses a member", oneOf("A", "B", "C"), oneOf("A", "C"), []string{
			"POST /x: the request body.oneOf[B] was removed",
		}},
		{"a oneOf's member changes", oneOf("A", "B"), doc("oneOf", map[string]*Schema{"B": needsNote}, "A", "B"),
			[]string{"POST /x: the request body.oneOf[B].note is new and required"}},
		{"a oneOf's member is replaced", oneOf("A", "B"), oneOf("A", "C"), []string{
			"POST /x: the request body.oneOf[B] was removed",
			"POST /x: the response 200 body.oneOf[C] was added",
		}},
		{"an anyOf gains a member", anyOf("A", "B"), anyOf("A", "B", "C"), []string{
			"POST /x: the response 200 body.anyOf[C] was added",
		}},
		{"an anyOf loses a member", anyOf("A", "B", "C"), anyOf("B", "C"), []string{
			"POST /x: the request body.anyOf[A] was removed",
		}},

		// A value that two members of a oneOf take is refused.
		{"a oneOf gains a member of another type", oneOf("A", "B"),
			withD(&Schema{Type: Types{"string"}}, "A", "B", "D"), []string{
				"POST /x: the response 200 body.oneOf[D] was added",
			}},
		{"a oneOf gains a member whose type is another's", oneOf("A", "B"), withD(tagged(`"a"`), "A", "B", "D"),
			[]string{
				"POST /x: the request body.oneOf[D] was added, and may match a value of A, which oneOf then refuses",
				"POST /x: the response 200 body.oneOf[D] was added",
			}},
		{"a oneOf gains a member whose type is another's written differently",
			doc("oneOf", map[string]*Schema{"A": tagged("1")}, "A", "B"),
			doc("oneOf", map[string]*Schema{"A": tagged("1"), "D": tagged("1.0")}, "A", "B", "D"), []string{
				"POST /x: the request body.oneOf[D] was added, and may match a value of A, which oneOf then refuses",
				"POST /x: the response 200 body.oneOf[D] was added",
			}},
		{"a oneOf gains a member that nothing tells apart", oneOf("A", "B"),
			withD(&Schema{Type: Types{"object"}}, "A", "B", "D"), []string{
				"POST /x: the request body.oneOf[D] was added, and may match a value of A, B, which oneOf then refuses",
				"POST /x: the response 200 body.oneOf[D] was added",
			}},
		{"a oneOf gains a member whose type a request may leave out", oneOf("A", "B"),
			withD(readOnlyType, "A", "B", "D"), []string{
				"POST /x: the request body.oneOf[D] was added, and may match a value of A, B, which oneOf then refuses",
				"POST /x: the response 200 body.oneOf[D] was added",
			}},
		{"an anyOf gains a member whose type is another's", anyOf("A", "B"),
			doc("anyOf", map[string]*Schema{"D": tagged(`"a"`)}, "A", "B", "D"), []string{
				"POST /x: the response 200 body.anyOf[D] was added",
			}},

		// Members that do not all refer to named schemas, or not to different
		// ones, are compared by position.
		{"a oneOf gains its first members", doc("oneOf", nil), oneOf("A"), []string{
			"POST /x: the request body: oneOf changed from 0 schemas to 1",
			"POST /x: the response 200 body: oneOf changed from 0 schemas to 1",
		}},
		{"a oneOf that names a schema twice gains a member", oneOf("A", "A"), oneOf("A", "A", "B"), []string{
			"POST /x: the request body: oneOf changed from 2 schemas to 3",
			"POST /x: the response 200 body: oneOf changed from 2 schemas to 3",
		}},
	}
	for _, tt := range tests {
		if got := messages(Breaking(tt.base, tt.head)); !slices.Equal(got, tt.want) {
			t.Errorf("%s: Breaking = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestBreakingDiscriminator checks that a oneOf's discriminator, which huma's
// validator reads to choose the member that it validates an object against,
// breaks no client of a request when it maps a value that no other member of
// base's oneOf took, as one that the oneOf gains, or when a oneOf whose
// members all need its property gains it, breaks no client of a response when
// a oneOf gains it or it maps a value that it did not, which huma does not
// read in a response, and breaks a client when it is removed or changed
// otherwise, and that the same one breaks nothing.
func TestBreakingDiscriminator(t *testing.T) {
	tagged := func(v string) *Schema {
		value, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return &Schema{Type: Types{"object"}, Required: []string{"type"}, AdditionalProperties: &Additional{},
			Properties: map[string]*Schema{"type": {Enum: []json.RawMessage{value}}}}
	}
	// untagged takes an object of any type, which it does not need.
	untagged := &Schema{Type: Types{"object"}, AdditionalProperties: &Additional{},
		Properties: map[string]*Schema{"type": {Type: Types{"string"}}}}
	// docOf returns a document whose operation takes and answers with one of
	// the members, with the discriminator that maps each of mapping's values
	// to the member named by the next, or none if mapping is nil.
	docOf := func(members []string, property string, mapping ...string) *Document {
		union := func() *Schema {
			s := &Schema{}
			for _, m := range members {
				s.OneOf = append(s.OneOf, &Schema{Ref: refPrefix + m})
			}
			if mapping != nil {
				s.Discriminator = &Discriminator{PropertyName: property, Mapping: map[string]string{}}
				for i := 0; i < len(mapping); i += 2 {
					s.Discriminator.Mapping[mapping[i]] = refPrefix + mapping[i+1]
				}
			}
			return s
		}
		return &Document{
			OpenAPI: "3.1.0",
			Paths: map[string]*PathItem{"/x": {Post: &Operation{
				OperationID: "postX",
				RequestBody: &RequestBody{Content: map[string]*MediaType{"application/json": {Schema: union()}}},
				Responses: map[string]*Response{"200": {Content: map[string]*MediaType{"application/json": {
					Schema: union(),
				}}}},
			}}},
			Components: &Components{Schemas: map[string]*Schema{"A": tagged("a"), "B": tagged("b"), "C": tagged("c"),
				"D": tagged("d" + string(rune(0x7F))), "U": untagged}},
		}
	}
	doc := func(property string, mapping ...string) *Document {
		return docOf([]string{"A", "B"}, property, mapping...)
	}
	changed := func(in, from, to string) string {
		return "POST /x: the " + in + " body: the discriminator changed from " + from + " to " + to
	}
	both := func(from, to string) []string {
		return []string{changed("request", from, to), changed("response 200", from, to)}
	}
	tests := []struct {
		name       string
		base, head *Document
		want       []string
	}{
		{"the same discriminator", doc("type", "a", "A", "b", "B"), doc("type", "b", "B", "a", "A"), nil},
		{"no discriminator", doc(""), doc(""), nil},
		// Base validated an object of type b against both members, of which
		// only B takes it, and head validates it against B. A response's
		// discriminator, which huma does not read, changes nothing that a
		// client receives, and a client of base's reads in it at most what
		// base's mapped.
		{"a discriminator is added", doc(""), doc("type", "a", "A", "b", "B"), nil},
		{"a discriminator gains a value", doc("type", "a", "A"), doc("type", "a", "A", "b", "B"), nil},
		// The member that the response's oneOf gains breaks a client, as
		// such a member does, once.
		{"a discriminator gains a value of a member that the oneOf gains", doc("type", "a", "A", "b", "B"),
			docOf([]string{"A", "B", "C"}, "type", "a", "A", "b", "B", "c", "C"),
			[]string{"POST /x: the response 200 body.oneOf[C] was added"}},
		// A value that JSON escapes, DEL, which Go's quoting writes otherwise,
		// of a member that the oneOf gains.
		{"a discriminator gains a value with a control character", doc("type", "a", "A", "b", "B"),
			docOf([]string{"A", "B", "D"}, "type", "a", "A", "b", "B", "d"+string(rune(0x7F)), "D"),
			[]string{"POST /x: the response 200 body.oneOf[D] was added"}},
		// Base validated an object of type b against B, which took it, and
		// head against A, which does not.
		{"a discriminator gains a value that another member took", doc("type", "a", "A"),
			doc("type", "a", "A", "b", "A"), []string{changed("request", `"type" {"a": A}`, `"type" {"a": A, "b": A}`)}},
		// Base took an object without a type, which U takes, and head refuses
		// it.
		{"a discriminator is added to a oneOf of a member that does not need its property",
			docOf([]string{"A", "U"}, ""), docOf([]string{"A", "U"}, "type", "a", "A"),
			[]string{changed("request", "none", `"type" {"a": A}`)}},
		// Base let through an object whose type is not a string, and head
		// validates it against every member.
		{"a discriminator is removed", doc("type", "a", "A", "b", "B"), doc(""),
			both(`"type" {"a": A, "b": B}`, "none")},
		{"a discriminator reads another property", doc("type", "a", "A"), doc("kind", "a", "A"),
			both(`"type" {"a": A}`, `"kind" {"a": A}`)},
		{"a discriminator maps a value to another member", doc("type", "a", "A", "b", "B"),
			doc("type", "a", "A", "b", "A"), both(`"type" {"a": A, "b": B}`, `"type" {"a": A, "b": A}`)},
		{"a discriminator no longer maps a value", doc("type", "a", "A", "b", "B"), doc("type", "a", "A"),
			both(`"type" {"a": A, "b": B}`, `"type" {"a": A}`)},
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
