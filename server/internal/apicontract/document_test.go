package apicontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadCommittedDocument checks that the package reads the document that
// the server serves, which huma writes.
func TestLoadCommittedDocument(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "api", "v1", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Load(b)
	if err != nil {
		t.Fatal(err)
	}
	if d.Paths["/api/v1/openapi.json"] == nil || d.Components.Schemas["ErrorEnvelope"] == nil {
		t.Errorf("the document lacks its own operation or the error envelope")
	}
}

// TestLoadRefuses checks that Load refuses what the package does not
// understand, rather than skip it.
func TestLoadRefuses(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("testdata", "base.json"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, old, new, want string
	}{
		{"an unknown keyword in a schema", `"maxLength": 1024`, `"maxLength": 1024, "not": {}`, `unknown field "not"`},
		{"an extension", `"title": "Fixture"`, `"title": "Fixture", "x-logo": "a.png"`, `unknown field "x-logo"`},
		{"an unknown object of an operation", `"operationId": "listThings",`,
			`"operationId": "listThings", "security": [],`, `unknown field "security"`},
		// encoding/json would take each of these as the key in its own case.
		{"a keyword in another case", `"maxLength": 1024`, `"maxLength": 1024, "Enum": ["a"]`, `unknown field "Enum"`},
		{"a key of an operation in another case", `"operationId": "listThings"`, `"OperationId": "listThings"`,
			`unknown field "OperationId"`},
		{"a keyword in another case in additionalProperties", `"additionalProperties": {}`,
			`"additionalProperties": {"Type": "string"}`, `unknown field "Type"`},
		{"another version of OpenAPI", `"openapi": "3.1.0"`, `"openapi": "3.0.3"`, "not 3.1"},
		{"a reference to a schema that does not exist", `"$ref": "#/components/schemas/ThingPage"`,
			`"$ref": "#/components/schemas/Nothing"`, "does not name a schema"},
		{"a reference outside the schemas", `"$ref": "#/components/schemas/ThingPage"`,
			`"$ref": "other.json#/Thing"`, "does not name a schema"},
		{"a default beside a reference", `"$ref": "#/components/schemas/ThingPage"`,
			`"$ref": "#/components/schemas/ThingPage", "default": {}`, "has keywords beside it"},
		{"a bound beside a reference", `"$ref": "#/components/schemas/ThingPage"`,
			`"$ref": "#/components/schemas/ThingPage", "minProperties": 1`, "has keywords beside it"},
		{"an operationId twice", `"operationId": "getThing"`, `"operationId": "listThings"`,
			"the same operationId listThings"},
		{"no operationId", `"operationId": "getThing",`, ``, "has no operationId"},
		{"a path parameter that the path lacks", `"/things/{id}": {`, `"/things/{key}": {`,
			`the path has the parameters ["key"], and the operation describes ["id"]`},
		{"a parameter in an unknown place", `"in": "header"`, `"in": "body"`, "not a place for one"},
		{"an unknown type", `"type": "boolean"`, `"type": "bool"`, `"bool" is not a type`},
		{"a response key that is not a status", `"304": {`, `"3xx": {`, "is not a status"},
		{"a null schema", `"schemas": {`, `"schemas": {"Nothing": null,`, "schema Nothing is null"},
		{"a required property that the schema does not name", `"required": ["name"]`,
			`"required": ["name", "colour"]`, "schema ThingInput: it requires the property colour, which it does not name"},
		{"references in a cycle", `"schemas": {`, `"schemas": {"A": {"$ref": "#/components/schemas/B"}, ` +
			`"B": {"$ref": "#/components/schemas/A"},`, "schema A: its references go round in a cycle"},
		{"a discriminator without a oneOf", `"maxLength": 1024`,
			`"maxLength": 1024, "discriminator": {"propertyName": "kind"}`, "a discriminator without a oneOf"},
		{"a discriminator that names no property", `"$ref": "#/components/schemas/Thing"`,
			`"oneOf": [{"$ref": "#/components/schemas/Thing"}], "discriminator": {"mapping": {}}`,
			"its discriminator names no property"},
		{"a discriminator that maps a value to no member", `"$ref": "#/components/schemas/Thing"`,
			`"oneOf": [{"$ref": "#/components/schemas/Thing"}], "discriminator": {"propertyName": "kind", ` +
				`"mapping": {"note": "#/components/schemas/ThingInput"}}`,
			`maps "note" to #/components/schemas/ThingInput, which is no member of its oneOf`},
		{"a discriminator that maps a value to a member that is no reference", `"$ref": "#/components/schemas/Thing"`,
			`"oneOf": [{"type": "object"}], "discriminator": {"propertyName": "kind", "mapping": {"note": ""}}`,
			`its discriminator maps "note" to no reference`},
		{"an unknown keyword of a discriminator", `"$ref": "#/components/schemas/Thing"`,
			`"oneOf": [{"$ref": "#/components/schemas/Thing"}], "discriminator": {"propertyName": "kind", "x": 1}`,
			`unknown field "x"`},
	}
	for _, tt := range tests {
		if !strings.Contains(string(base), tt.old) {
			t.Fatalf("%s: the fixture lacks %q", tt.name, tt.old)
		}
		_, err := Load([]byte(strings.Replace(string(base), tt.old, tt.new, 1)))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: Load = %v, want an error saying %q", tt.name, err, tt.want)
		}
	}
	if _, err := Load(append(base, "{}"...)); err == nil || !strings.Contains(err.Error(), "more after") {
		t.Errorf("more after the document: Load = %v, want an error", err)
	}
	annotated := strings.Replace(string(base), `"$ref": "#/components/schemas/ThingPage"`,
		`"$ref": "#/components/schemas/ThingPage", "description": "A page", "readOnly": true, "deprecated": true`, 1)
	if _, err := Load([]byte(annotated)); err != nil {
		t.Errorf("annotations beside a reference: Load = %v, want no error", err)
	}
	alias := strings.Replace(string(base), `"schemas": {`,
		`"schemas": {"A": {"$ref": "#/components/schemas/B"}, "B": {"$ref": "#/components/schemas/Thing"},`, 1)
	if _, err := Load([]byte(alias)); err != nil {
		t.Errorf("a chain of aliases: Load = %v, want no error", err)
	}
	// A discriminator beside a oneOf, as huma writes one, which the
	// TypeScript client's types leave out, as they are the oneOf's union.
	discriminated := strings.Replace(string(base), `"$ref": "#/components/schemas/Thing"`,
		`"oneOf": [{"$ref": "#/components/schemas/Thing"}, {"$ref": "#/components/schemas/ThingInput"}], `+
			`"discriminator": {"propertyName": "kind", "mapping": {"note": "#/components/schemas/Thing", `+
			`"choice": "#/components/schemas/ThingInput"}}`, 1)
	d, err := Load([]byte(discriminated))
	if err != nil {
		t.Fatalf("a discriminator beside a oneOf: Load = %v, want no error", err)
	}
	if ts, err := TypeScript(d, ""); err != nil || !strings.Contains(string(ts), "Thing | ThingInput") {
		t.Errorf("a discriminator beside a oneOf: TypeScript = %v, want the union Thing | ThingInput", err)
	}

	// Of two problems, Load names the first, in the order of the paths and
	// their methods, every time.
	two := strings.Replace(strings.Replace(string(base), `"operationId": "getThing",`, ``, 1),
		`"operationId": "listThings",`, ``, 1)
	for range 20 {
		if _, err := Load([]byte(two)); err == nil || !strings.Contains(err.Error(), "GET /things has no operationId") {
			t.Fatalf("two operations without an operationId: Load = %v, want an error naming GET /things", err)
		}
	}
}
