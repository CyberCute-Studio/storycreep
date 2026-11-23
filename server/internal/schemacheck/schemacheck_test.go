package schemacheck

import (
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// testSchema has each keyword that FailFast rewrites, under a recursive oneOf,
// and a definition that takes anything but null.
const testSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$ref": "#/$defs/Node",
  "$defs": {
    "Any": {"oneOf": [{"type": "boolean"}, {"type": "number"}, {"type": "string"},
      {"type": "array", "items": {"$ref": "#/$defs/Any"}},
      {"type": "object", "additionalProperties": {"$ref": "#/$defs/Any"}}]},
    "Leaf": {"type": "object", "additionalProperties": {"$ref": "#/$defs/Any"},
      "properties": {"kind": {"const": "leaf"}, "n": {"type": "integer", "maximum": 9}}, "required": ["kind", "n"]},
    "List": {"type": "object", "additionalProperties": false,
      "properties": {"kind": {"const": "list"}, "tags": {"type": "array", "maxItems": 3, "items": {"type": "string"}},
        "of": {"type": "array", "minItems": 1, "items": {"$ref": "#/$defs/Node"}}}, "required": ["kind", "of"]},
    "Node": {"oneOf": [{"$ref": "#/$defs/Leaf"}, {"$ref": "#/$defs/List"}]}
  }
}`

// compileBoth compiles testSchema as it is, and rewritten.
func compileBoth(t *testing.T) (plain, fast *jsonschema.Schema) {
	t.Helper()
	compile := func() *jsonschema.Schema {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(testSchema))
		if err != nil {
			t.Fatal(err)
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("mem://test.json", doc); err != nil {
			t.Fatal(err)
		}
		s, err := c.Compile("mem://test.json")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	plain, fast = compile(), compile()
	FailFast("Any", fast)
	return plain, fast
}

// TestFailFastTakesTheSameDocuments checks that a rewritten schema takes and
// refuses what the schema does, of documents without a null.
//
// Verifies: NFR-SEC-13
func TestFailFastTakesTheSameDocuments(t *testing.T) {
	t.Parallel()
	plain, fast := compileBoth(t)
	for _, doc := range []string{
		`{"kind":"leaf","n":1}`,
		`{"kind":"leaf","n":1,"later":{"a":[1,"b",true,{"c":[]}]}}`,
		`{"kind":"list","of":[{"kind":"leaf","n":1}]}`,
		`{"kind":"list","tags":["a","b","c"],"of":[{"kind":"list","of":[{"kind":"leaf","n":2,"x":1}]}]}`,
		`{"kind":"leaf","n":10}`,
		`{"kind":"leaf","n":1.5}`,
		`{"kind":"leaf"}`,
		`{"kind":"other","n":1}`,
		`{"kind":"list","of":[]}`,
		`{"kind":"list"}`,
		`{"kind":"list","of":[{"kind":"leaf","n":1}],"more":1}`,
		`{"kind":"list","tags":["a","b","c","d"],"of":[{"kind":"leaf","n":1}]}`,
		`{"kind":"list","tags":["a",1],"of":[{"kind":"leaf","n":1}]}`,
		`{"kind":"list","of":[{"kind":"leaf","n":1},{"kind":"leaf","n":99}]}`,
		`{"kind":"list","of":[{"kind":"list","of":[{"kind":"leaf"}]}]}`,
		`[]`, `1`, `"a"`, `true`, `{}`,
	} {
		v, err := jsonschema.UnmarshalJSON(strings.NewReader(doc))
		if err != nil {
			t.Fatal(err)
		}
		if a, b := plain.Validate(v) == nil, fast.Validate(v) == nil; a != b {
			t.Errorf("%s: the schema says %v, rewritten %v", doc, a, b)
		}
	}
}

// countErrors counts the errors of a validation.
func countErrors(err error) int {
	v, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return 0
	}
	n := 1
	for _, c := range v.Causes {
		n += countErrors(c)
	}
	return n
}

// TestFailFastBuildsOnePath checks that a document whose every value fails, in
// an array and in an object's members, costs the errors of one path, and that
// the failure named is the one in the branch that the value was meant for.
//
// Verifies: NFR-SEC-13
func TestFailFastBuildsOnePath(t *testing.T) {
	t.Parallel()
	plain, fast := compileBoth(t)
	var b strings.Builder
	b.WriteString(`{"kind":"list","of":[`)
	for i := range 2000 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"kind":"leaf","n":99}`)
	}
	b.WriteString(`]}`)
	v, err := jsonschema.UnmarshalJSON(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	all, one := countErrors(plain.Validate(v)), countErrors(fast.Validate(v))
	if all < 2000 || one > 40 {
		t.Errorf("the schema builds %d errors and the rewritten one %d, want thousands and a few", all, one)
	}
	if got := Failure(fast.Validate(v)); !strings.Contains(got, "/of/0/n") {
		t.Errorf("the failure is %q, want the first leaf's n", got)
	}
}

// TestCheckDepth checks the bound of how deeply a document may nest.
//
// Verifies: NFR-SEC-13
func TestCheckDepth(t *testing.T) {
	t.Parallel()
	nested := func(n int) []byte { return []byte(strings.Repeat("[", n) + strings.Repeat("]", n)) }
	if err := CheckDepth(nested(MaxDepth)); err != nil {
		t.Errorf("at the bound: %v", err)
	}
	if err := CheckDepth(nested(MaxDepth + 1)); err == nil {
		t.Error("past the bound: no error")
	}
}
