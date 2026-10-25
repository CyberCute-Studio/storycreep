package published

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
)

// validator validates JSON against the documents of Files, as a consumer
// that reads the schemas does, with a validator of JSON Schema 2020-12 that
// is not schemagen's, so that a mistake of the generator's does not pass
// because the check shares it.
type validator struct {
	c *jsonschema.Compiler
}

func newValidator(t testing.TB, closed bool) *validator {
	t.Helper()
	files, err := Files(closed)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	for path, data := range files {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource(schemagen.BaseURL+path, doc); err != nil {
			t.Fatal(err)
		}
	}
	return &validator{c: c}
}

// validate returns the error of the JSON data against the document at path,
// or against its definition def, or nil when it is valid.
func (v *validator) validate(t testing.TB, path, def string, data []byte) error {
	t.Helper()
	url := schemagen.BaseURL + path
	if def != "" {
		url += "#/$defs/" + def
	}
	s, err := v.c.Compile(url)
	if err != nil {
		t.Fatalf("%s does not compile: %v", url, err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	return s.Validate(inst)
}

// sameJSON reports whether a and b are the same JSON value, numbers compared
// by their values, as a consumer reads them.
func sameJSON(t testing.TB, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("not JSON: %v: %s", err, a)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("not JSON: %v: %s", err, b)
	}
	return reflect.DeepEqual(x, y)
}

// roundTrip reads data as the document d's root type and writes it again.
func roundTrip(d schemagen.Document, data []byte) ([]byte, error) {
	v := reflect.New(d.Root)
	if err := Decode(data, v.Interface()); err != nil {
		return nil, err
	}
	return json.Marshal(v.Interface())
}

// TestExamples checks that every example of each document validates against
// the document, and against the closed document, which takes no property
// that it does not define, so that an example holds what the schema defines
// alone, and that the Go types read it whole and write it as it is, so that
// the types and the schemas hold the same fields.
func TestExamples(t *testing.T) {
	open, closed := newValidator(t, false), newValidator(t, true)
	for _, d := range Documents() {
		if len(d.Examples) == 0 {
			t.Errorf("%s has no example", d.Path)
		}
		for i, ex := range d.Examples {
			if err := open.validate(t, d.Path, "", []byte(ex)); err != nil {
				t.Errorf("%s: example %d does not validate: %v", d.Path, i, err)
			}
			if err := closed.validate(t, d.Path, "", []byte(ex)); err != nil {
				t.Errorf("%s: example %d holds what the schema does not define: %v", d.Path, i, err)
			}
			out, err := roundTrip(d, []byte(ex))
			if err != nil {
				t.Errorf("%s: example %d does not decode: %v", d.Path, i, err)
				continue
			}
			if !sameJSON(t, out, []byte(ex)) {
				t.Errorf("%s: example %d is written again as\n%s", d.Path, i, out)
			}
		}
	}
}

// prdExample is a JSON example of the PRD, with the label above it.
type prdExample struct {
	section, label string
	json           []byte
}

// prdExamples returns the JSON examples of the PRD's sections §6.5 and §8.5,
// each with the bold label of §6.5 above it.
func prdExamples(t *testing.T) []prdExample {
	t.Helper()
	prd, err := os.ReadFile(filepath.Join(root, "docs", "PRD.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(prd), "\r\n", "\n")
	section := func(heading string) string {
		start := strings.Index(text, "\n"+heading+"\n")
		if start < 0 {
			t.Fatalf("the PRD has no %q", heading)
		}
		rest := text[start+len(heading)+2:]
		if end := regexp.MustCompile(`\n#{2,3} `).FindStringIndex(rest); end != nil {
			rest = rest[:end[0]]
		}
		return rest
	}
	block := regexp.MustCompile("(?s)```json\n(.*?)\n```")
	var out []prdExample
	for _, heading := range []string{"### 6.5 Examples", "### 8.5 Save and load of narrative state"} {
		s := section(heading)
		for _, m := range block.FindAllStringSubmatchIndex(s, -1) {
			label := ""
			for line := range strings.SplitSeq(s[:m[0]], "\n") {
				if strings.HasPrefix(line, "**") {
					label = line
				}
			}
			out = append(out, prdExample{section: heading, label: label, json: []byte(s[m[2]:m[3]])})
		}
	}
	return out
}

// prdLabels maps the label of each example of §6.5 to the document and the
// definition that it is an example of.
var prdLabels = []struct{ label, path, def string }{
	{"**`storycreep.json` (manifest)**", PublishedDir + "/storycreep.schema.json", ""},
	{"**`project/settings.json`**", PublishedDir + "/settings.schema.json", ""},
	{"**`project/variables.json`**", PublishedDir + "/variables.schema.json", ""},
	{"**`project/component_types.json`**", PublishedDir + "/component_types.schema.json", ""},
	{"**`project/components.json`**", PublishedDir + "/components.schema.json", ""},
	{"**`boards/", PublishedDir + "/board.schema.json", ""},
	{"**Jumper node**", PublishedDir + "/board.schema.json", "Jumper"},
}

// TestPRDExamples checks that every JSON example of the PRD's §6.5, of the
// published format, and of §8.5, a save state, validates against its
// document, open and closed, and is one of the document's own examples, or,
// for a node, one of a board's, so that the PRD and the schemas cannot
// drift apart.
func TestPRDExamples(t *testing.T) {
	open, closed := newValidator(t, false), newValidator(t, true)
	docs := map[string]schemagen.Document{}
	for _, d := range Documents() {
		docs[d.Path] = d
	}
	examples := prdExamples(t)
	if len(examples) < 5 {
		t.Fatalf("the PRD has %d JSON examples in §6.5 and §8.5, fewer than the 5 that it had", len(examples))
	}
	for _, ex := range examples {
		path, def := SaveStateDir+"/save-state.schema.json", ""
		if ex.section != "### 8.5 Save and load of narrative state" {
			i := slices.IndexFunc(prdLabels, func(l struct{ label, path, def string }) bool {
				return strings.HasPrefix(ex.label, l.label)
			})
			if i < 0 {
				t.Errorf("the PRD's example under %q is of no document that the test knows", ex.label)
				continue
			}
			path, def = prdLabels[i].path, prdLabels[i].def
		}
		name := fmt.Sprintf("%s %s", ex.section, ex.label)
		if err := open.validate(t, path, def, ex.json); err != nil {
			t.Errorf("%s does not validate against %s: %v", name, path, err)
		}
		if err := closed.validate(t, path, def, ex.json); err != nil {
			t.Errorf("%s holds what %s does not define: %v", name, path, err)
		}
		candidates := docs[path].Examples
		if def == "Jumper" {
			candidates = nil
			for _, b := range boardExamples {
				var board struct {
					Nodes []json.RawMessage `json:"nodes"`
				}
				if err := json.Unmarshal([]byte(b), &board); err != nil {
					t.Fatal(err)
				}
				for _, n := range board.Nodes {
					candidates = append(candidates, string(n))
				}
			}
		}
		if !slices.ContainsFunc(candidates, func(c string) bool { return sameJSON(t, []byte(c), ex.json) }) {
			t.Errorf("%s is none of the examples of %s", name, path)
		}
	}
}

// mapKeys are the properties whose values are maps, of keys that are IDs or
// paths, rather than objects of properties that the schema defines, as are
// the objects that a map holds, the save state's attributes of each
// component.
var mapKeys = []string{"attributes", "files", "variables", "visits"}

// mutations returns data, a JSON value, with each of its values replaced by
// what replace returns of it in turn, one at a time, and with each of its
// objects but its maps (mapKeys) given the property key of the value add,
// one at a time, each with the path of what it changed.
func mutations(t testing.TB, data []byte, replace func(key string, v any) (any, bool), key string,
	add any) map[string][]byte {
	t.Helper()
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	var walk func(v any, path string, isMap bool)
	walk = func(v any, path string, isMap bool) {
		switch v := v.(type) {
		case map[string]any:
			if add != nil && !isMap {
				v[key] = add
				out[path+"+"+key] = mustMarshal(t, doc)
				delete(v, key)
			}
			for k, c := range v {
				_, object := c.(map[string]any)
				walk(c, path+"."+k, object && (isMap || slices.Contains(mapKeys, k)))
				if replace != nil {
					if r, ok := replace(k, c); ok {
						v[k] = r
						out[path+"."+k] = mustMarshal(t, doc)
						v[k] = c
					}
				}
			}
		case []any:
			for i, c := range v {
				walk(c, fmt.Sprintf("%s[%d]", path, i), false)
				if replace != nil {
					if r, ok := replace("", c); ok {
						v[i] = r
						out[fmt.Sprintf("%s[%d]", path, i)] = mustMarshal(t, doc)
						v[i] = c
					}
				}
			}
		}
	}
	walk(doc, "$", false)
	return out
}

func mustMarshal(t testing.TB, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestNullAnywhere checks that null is refused wherever it is in a published
// file or a save state, in place of any value or as the value of a property
// that no object defines, by every schema and by Decode (PRD §6.2).
func TestNullAnywhere(t *testing.T) {
	v := newValidator(t, false)
	for _, d := range Documents() {
		for i, ex := range d.Examples {
			nulls := mutations(t, []byte(ex), func(string, any) (any, bool) { return nil, true }, "unknown", json.RawMessage("null"))
			for at, data := range nulls {
				if v.validate(t, d.Path, "", data) == nil {
					t.Errorf("%s: example %d with null at %s validates", d.Path, i, at)
				}
				if _, err := roundTrip(d, data); err == nil {
					t.Errorf("%s: example %d with null at %s decodes", d.Path, i, at)
				}
			}
		}
	}
}

// TestUnknownKeys checks that a property that an object does not define, of
// any value but null, is taken by every schema and ignored by Decode, as a
// consumer ignores it (PRD §6.4), and refused by the closed schemas.
func TestUnknownKeys(t *testing.T) {
	open, closed := newValidator(t, false), newValidator(t, true)
	future := map[string]any{"a": []any{1, "b", true, map[string]any{"c": 2.5}}}
	for _, d := range Documents() {
		for i, ex := range d.Examples {
			for at, data := range mutations(t, []byte(ex), nil, "futureField", future) {
				if err := open.validate(t, d.Path, "", data); err != nil {
					t.Errorf("%s: example %d with a property at %s does not validate: %v", d.Path, i, at, err)
				}
				if closed.validate(t, d.Path, "", data) == nil {
					t.Errorf("%s: example %d with a property at %s validates against the closed schema", d.Path, i, at)
				}
				out, err := roundTrip(d, data)
				if err != nil {
					t.Errorf("%s: example %d with a property at %s does not decode: %v", d.Path, i, at, err)
				} else if !sameJSON(t, out, []byte(ex)) {
					t.Errorf("%s: example %d with a property at %s is written again as\n%s", d.Path, i, at, out)
				}
			}
		}
	}
}

// enumKeys are the properties whose values are of an enum, as a type, a
// kind, or an op, which a consumer fails loudly on a value of that it does
// not know (PRD §6.4).
var enumKeys = []string{"channel", "kind", "list", "op", "phase", "scope", "type"}

// TestUnknownEnumValues checks that every schema and Decode refuse a value
// that the format does not have of a property of an enum, wherever it is.
func TestUnknownEnumValues(t *testing.T) {
	v := newValidator(t, false)
	replace := func(key string, x any) (any, bool) {
		_, ok := x.(string)
		return "unknown_value", ok && slices.Contains(enumKeys, key)
	}
	for _, d := range Documents() {
		for i, ex := range d.Examples {
			for at, data := range mutations(t, []byte(ex), replace, "", nil) {
				if v.validate(t, d.Path, "", data) == nil {
					t.Errorf("%s: example %d with an unknown value at %s validates", d.Path, i, at)
				}
				if _, err := roundTrip(d, data); err == nil {
					t.Errorf("%s: example %d with an unknown value at %s decodes", d.Path, i, at)
				}
			}
		}
	}
}
