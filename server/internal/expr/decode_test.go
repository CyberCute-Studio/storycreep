package expr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
)

// astDocument is the root of a test's document of an AST's schema: a
// condition's or a script's AST, as the published format's AST document's is.
type astDocument struct{}

func (astDocument) JSONSchema(g *schemagen.Generator) *schemagen.Schema {
	return &schemagen.Schema{OneOf: []*schemagen.Schema{ExpressionSchema(g), ScriptSchema(g)}}
}

// astSchemas compiles the AST's schema of ExpressionSchema and ScriptSchema,
// and returns the validators of a condition's AST and of a script's.
func astSchemas(t testing.TB) (condition, script *jsonschema.Schema) {
	t.Helper()
	g := schemagen.New(false)
	d := schemagen.Document{Path: "schema/test/v1/ast.schema.json", Title: "AST", Root: reflect.TypeFor[astDocument]()}
	data, err := g.Document(d)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(d.ID(), doc); err != nil {
		t.Fatal(err)
	}
	return c.MustCompile(d.ID() + "#/$defs/" + schemaExpression), c.MustCompile(d.ID() + "#/$defs/" + schemaScript)
}

func valid(t testing.TB, s *jsonschema.Schema, data []byte) bool {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not JSON: %s", data)
	}
	return s.Validate(inst) == nil
}

// resolvedASTs returns the AST of every case of the corpus's compile.json
// that has one, with its context: ASTs whose names are resolved, as the
// published format holds them.
func resolvedASTs(t *testing.T) (asts []json.RawMessage, contexts []Context) {
	t.Helper()
	_, cases, _ := readCompileCorpus(t)
	for _, c := range cases {
		if c.AST != nil {
			asts = append(asts, c.AST)
			contexts = append(contexts, c.Context)
		}
	}
	if len(asts) < 50 {
		t.Fatalf("compile.json has %d ASTs, fewer than it had", len(asts))
	}
	return asts, contexts
}

// TestDecodeCorpus checks that the AST of every case of the corpus's
// compile.json, whose names are resolved, validates against the AST's
// schema of its context, and not the other's, and that Decode reads it as
// the AST that Encode writes as its canonical form.
func TestDecodeCorpus(t *testing.T) {
	condition, script := astSchemas(t)
	asts, contexts := resolvedASTs(t)
	for i, raw := range asts {
		ctx := contexts[i]
		own, other := condition, script
		if ctx == ContextScript {
			own, other = script, condition
		}
		if !valid(t, own, raw) {
			t.Errorf("the AST of %s does not validate against the AST's schema of a %s", raw, ctx)
		}
		if valid(t, other, raw) {
			t.Errorf("the AST of %s validates against the AST's schema of the other context than a %s", raw, ctx)
		}
		n, err := Decode(raw, ctx)
		if err != nil {
			t.Errorf("Decode(%s, %s): %v", raw, ctx, err)
			continue
		}
		got, err := Encode(n)
		if err != nil {
			t.Fatal(err)
		}
		want, err := canonicalJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("Decode(%s) is encoded as %s", raw, got)
		}
	}
}

// TestDecodeAgreesWithSchema checks that Decode reads exactly the JSON that
// the AST's schema takes, of mutations of the corpus's resolved ASTs: each of
// their values replaced by one of another kind or of another value, and a
// property that no node defines added to each of their objects, which both
// ignore, so that a consumer that validates and one that decodes take the
// same ASTs.
func TestDecodeAgreesWithSchema(t *testing.T) {
	condition, script := astSchemas(t)
	asts, contexts := resolvedASTs(t)
	replacements := []any{nil, "x", 1.5, 9007199254740992, true, map[string]any{}, []any{},
		"var_01HQ8ZV01K7MWVND2CQJ4RT9XB", "nd_01hq8zn01k7mwvnd2cqj4rt9xb", "script", "+="}
	// The shortest ASTs that together use every kind of node, operator, and
	// literal's type that the corpus's ASTs use, in each context, which
	// cover what every AST holds at a fraction of the cost.
	order := make([]int, len(asts))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return len(asts[a]) - len(asts[b]) })
	covered := map[string]bool{}
	var picked []int
	for _, i := range order {
		var tree any
		if err := json.Unmarshal(asts[i], &tree); err != nil {
			t.Fatal(err)
		}
		adds := false
		var collect func(v any)
		collect = func(v any) {
			switch v := v.(type) {
			case map[string]any:
				for k, c := range v {
					if s, ok := c.(string); ok && (k == "kind" || k == "op" || k == "type") {
						token := string(contexts[i]) + ":" + k + "=" + s
						adds = adds || !covered[token]
						covered[token] = true
					}
					collect(c)
				}
			case []any:
				for _, c := range v {
					collect(c)
				}
			}
		}
		collect(tree)
		if adds {
			picked = append(picked, i)
		}
	}
	checked := 0
	for _, i := range picked {
		raw := asts[i]
		s := condition
		if contexts[i] == ContextScript {
			s = script
		}
		var tree any
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatal(err)
		}
		try := func(at string) {
			t.Helper()
			data, err := json.Marshal(tree)
			if err != nil {
				t.Fatal(err)
			}
			checked++
			_, err = Decode(data, contexts[i])
			if schema := valid(t, s, data); schema != (err == nil) {
				t.Errorf("%s with %s: the schema takes it: %v, but Decode: %v", raw, at, schema, err)
			}
		}
		var walk func(v any, path string)
		walk = func(v any, path string) {
			switch v := v.(type) {
			case map[string]any:
				v["future"] = map[string]any{"a": []any{1}}
				try(path + "+future")
				delete(v, "future")
				for k, c := range v {
					for _, r := range replacements {
						v[k] = r
						try(fmt.Sprintf("%s.%s = %v", path, k, r))
					}
					v[k] = c
					walk(c, path+"."+k)
				}
			case []any:
				for j, c := range v {
					walk(c, fmt.Sprintf("%s[%d]", path, j))
				}
			}
		}
		walk(tree, "$")
	}
	if checked < 500 {
		t.Errorf("only %d mutations were checked", checked)
	}
}

// TestAttributePattern checks that the name of an attribute's node, which
// the AST's schema holds to attributePattern alone, is two keys of at most
// MaxIdentifierLength characters each, joined by a dot, both as the schema
// takes it and as Decode reads it.
func TestAttributePattern(t *testing.T) {
	condition, _ := astSchemas(t)
	long := strings.Repeat("b", MaxIdentifierLength)
	for name, ok := range map[string]bool{
		"elara.trust": true, "a." + long: true, long + ".a": true, long + "." + long: true, "_.Z9": true,
		"a." + long + "b": false, long + "b.a": false, "a.": false, ".a": false, "a.b.c": false, "1a.b": false,
		"trust": false, "a-b.c": false,
	} {
		data := []byte(`{"attribute":"atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF","kind":"attr","name":"` + name +
			`","ref":"cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF"}`)
		_, err := Decode(data, ContextCondition)
		if schema := valid(t, condition, data); schema != ok || (err == nil) != ok {
			t.Errorf("%s: the schema takes it: %v, and Decode: %v; want it taken: %v", name, schema, err, ok)
		}
	}
}

// TestDecodeErrors checks what Decode refuses, and what it takes.
func TestDecodeErrors(t *testing.T) {
	v := `{"kind":"var","name":"met_elara","ref":"var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"}`
	lit := func(typ, value string) string { return `{"kind":"lit","type":"` + typ + `","value":` + value + `}` }
	nest := func(n int) string {
		s := lit("bool", "true")
		for range n - 1 {
			s = `{"kind":"unary","op":"not","operand":` + s + `}`
		}
		return s
	}
	// wide returns an AST of 10n - 1 nodes, n calls of 9 each, and the
	// n - 1 binary operators that add them, n + 1 deep.
	wide := func(n int) string {
		args := make([]string, 8)
		for i := range args {
			args[i] = lit("int", "1")
		}
		call := `{"args":[` + strings.Join(args, ",") + `],"function":"max","kind":"call"}`
		s := call
		for range n - 1 {
			s = `{"kind":"binary","left":` + s + `,"op":"add","right":` + call + `}`
		}
		return s
	}
	statements := func(n int) string {
		st := make([]string, n)
		for i := range st {
			st[i] = `{"kind":"assign","op":"=","target":` + v + `,"value":` + lit("bool", "true") + `}`
		}
		return `{"kind":"script","statements":[` + strings.Join(st, ",") + `]}`
	}
	tests := []struct {
		name, data string
		ctx        Context
		ok         bool
	}{
		{"a variable", v, ContextCondition, true},
		{"an unknown key, which is ignored", `{"future":[1],"kind":"var","name":"a","ref":"var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"}`, ContextCondition, true},
		{"an int written as a float", lit("int", "2.0"), ContextCondition, true},
		{"an int written with an exponent", lit("int", "2e3"), ContextCondition, true},
		{"the greatest int", lit("int", "9007199254740991"), ContextCondition, true},
		{"an int beyond the greatest", lit("int", "9007199254740992"), ContextCondition, false},
		{"an int that is not integral", lit("int", "2.5"), ContextCondition, false},
		{"an int that rounds to an integer as a double", lit("int", "4503599627370495.5"), ContextCondition, false},
		{"a float beyond a double", lit("float", "1e400"), ContextCondition, false},
		{"a float written as an int", lit("float", "2"), ContextCondition, true},
		{"a string with a NUL", lit("string", `"a\u0000"`), ContextCondition, false},
		{"a string of 4096 characters", lit("string", `"`+strings.Repeat("é", 4096)+`"`), ContextCondition, true},
		{"a string of 4097 characters", lit("string", `"`+strings.Repeat("é", 4097)+`"`), ContextCondition, false},
		{"a lone surrogate", lit("string", `"\ud800"`), ContextCondition, false},
		{"a byte that is not UTF-8", lit("string", "\"a\xffb\""), ContextCondition, false},
		{"a boolean of the type int", lit("int", "true"), ContextCondition, false},
		{"an unknown type of a literal", lit("bytes", `"x"`), ContextCondition, false},
		{"null", `null`, ContextCondition, false},
		{"null in a key that no node defines", `{"future":null,"kind":"var","name":"a","ref":"var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"}`, ContextCondition, false},
		{"not JSON", `{"kind":`, ContextCondition, false},
		{"two values", v + v, ContextCondition, false},
		{"an unknown kind", `{"kind":"lambda"}`, ContextCondition, false},
		{"no kind", `{"name":"a"}`, ContextCondition, false},
		{"an unknown operator", `{"kind":"unary","op":"inc","operand":` + v + `}`, ContextCondition, false},
		{"a ref that is not canonical", `{"kind":"var","name":"a","ref":"var_01hq8z5brt9c4f2m7xkdpv6nha"}`, ContextCondition, false},
		{"a ref of another kind", `{"kind":"var","name":"a","ref":"cmp_01HQ8Z5BRT9C4F2M7XKDPV6NHA"}`, ContextCondition, false},
		{"no ref", `{"kind":"var","name":"a"}`, ContextCondition, false},
		{"a name that is no key", `{"kind":"var","name":"a.b","ref":"var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"}`, ContextCondition, false},
		{"an attribute's name of one key", `{"attribute":"atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF","kind":"attr","name":"trust","ref":"cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF"}`, ContextCondition, false},
		{"a script as a condition", statements(1), ContextCondition, false},
		{"a condition as a script", v, ContextScript, false},
		{"an assignment as a condition", `{"kind":"assign","op":"=","target":` + v + `,"value":` + v + `}`, ContextCondition, false},
		{"a literal as a statement", `{"kind":"script","statements":[` + lit("int", "1") + `]}`, ContextScript, false},
		{"a call as a statement", `{"kind":"script","statements":[{"args":[],"function":"signal","kind":"call"}]}`, ContextScript, true},
		{"a literal as an assignment's target", `{"kind":"script","statements":[{"kind":"assign","op":"=","target":` + lit("int", "1") + `,"value":` + v + `}]}`, ContextScript, false},
		{"a function that is no name", `{"args":[],"function":"1x","kind":"call"}`, ContextCondition, false},
		{"args that are not an array", `{"args":{},"function":"max","kind":"call"}`, ContextCondition, false},
		{"64 statements", statements(MaxStatements), ContextScript, true},
		{"65 statements", statements(MaxStatements + 1), ContextScript, false},
		{"9 arguments", `{"args":[` + strings.Repeat(lit("int", "1")+",", 8) + lit("int", "1") + `],"function":"max","kind":"call"}`, ContextCondition, false},
		{"an AST 64 deep", nest(MaxDepth), ContextCondition, true},
		{"an AST 65 deep", nest(MaxDepth + 1), ContextCondition, false},
		{"an AST of 509 nodes", wide(51), ContextCondition, true},
		{"an AST of 519 nodes", wide(52), ContextCondition, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := Decode([]byte(tt.data), tt.ctx)
			switch {
			case tt.ok && err != nil:
				t.Fatalf("Decode: %v", err)
			case !tt.ok && !errors.Is(err, ErrDecode):
				t.Fatalf("Decode = %v, %v; want an error that wraps ErrDecode", n, err)
			case tt.ok:
				if _, err := Encode(n); err != nil {
					t.Fatalf("Encode of what Decode read: %v", err)
				}
			}
		})
	}
	if _, err := Decode([]byte(v), "expression"); !errors.Is(err, ErrContext) {
		t.Errorf("Decode of no context = %v, want an error that wraps ErrContext", err)
	}
}
