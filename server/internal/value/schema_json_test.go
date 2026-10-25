package value

import (
	"bytes"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// TestSchema checks that the published format's schema of each type takes
// the values that Of takes of the type, and none that it refuses, of values
// of every kind, at the limits of each type, but for an enum's, of which the
// schema takes every string of an option's length.
func TestSchema(t *testing.T) {
	values := []string{
		`true`, `false`, `0`, `-1`, `2.0`, `2.5`, `1e3`, `9007199254740991`, `-9007199254740991`,
		`9007199254740992`, `1.7976931348623157e308`, `1e309`, `""`, `"calm"`, `"a\nb"`, `"a\u0000b"`,
		`"` + string(bytes.Repeat([]byte("é"), MaxStringLength)) + `"`,
		`"` + string(bytes.Repeat([]byte("é"), MaxStringLength+1)) + `"`,
		`"` + string(bytes.Repeat([]byte("x"), MaxOptionLength+1)) + `"`, `[]`, `{}`,
	}
	for _, typ := range Types() {
		c := jsonschema.NewCompiler()
		data, err := Schema(typ).MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource("https://example.com/value.json", doc); err != nil {
			t.Fatal(err)
		}
		s := c.MustCompile("https://example.com/value.json")
		for _, v := range values {
			inst, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(v)))
			if err != nil {
				t.Fatal(err)
			}
			_, problem := FromJSON([]byte(v)).Of(typ, "calm", "wary")
			schemaOK := s.Validate(inst) == nil
			// An enum's value is one of its options, which the schema does
			// not know: it takes every string of an option's length.
			if typ == Enum && schemaOK && problem != "" && v[0] == '"' {
				continue
			}
			if schemaOK != (problem == "") {
				t.Errorf("%s %.20s: the schema takes it: %v, but Of says %q", typ, v, schemaOK, problem)
			}
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("Schema of no type did not panic")
		}
	}()
	Schema("bytes")
}
