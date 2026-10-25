package published

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
	"github.com/CyberCute-Studio/storycreep/server/internal/slug"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// TestMarshalNeverNull checks that the types never write null, which no file
// holds (PRD §6.2): an array or an object that a file always holds is written
// [] or {} when its slice or map is nil, and a file in which a value is not
// set, as a variable's default, a node, or a manifest's snapshot, is not
// written at all.
func TestMarshalNeverNull(t *testing.T) {
	const (
		component = "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF"
		attribute = "atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF"
		variable  = "var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"
	)
	for _, tt := range []struct {
		v    any
		want []string
	}{
		{Settings{SchemaVersion: 1}, []string{`"signals":[]`}},
		{Variables{SchemaVersion: 1}, []string{`"variables":[]`}},
		{ComponentTypes{ComponentTypes: []ComponentType{{}}, SchemaVersion: 1}, []string{`"attributes":[]`}},
		{Components{Components: []Component{{}}, SchemaVersion: 1}, []string{`"attributes":{}`}},
		{Board{Nodes: Nodes{&Branch{}}, SchemaVersion: 1}, []string{`"connections":[]`, `"outputs":[]`}},
		{Board{SchemaVersion: 1}, []string{`"connections":[]`, `"nodes":[]`}},
		{Manifest{SchemaVersion: 1, Snapshot: &DraftSnapshot{}}, []string{`"boards":[]`, `"files":{}`}},
		{SaveState{Attributes: map[string]AttributeValues{component: nil}, StateVersion: 1},
			[]string{`"variables":{}`, `"visits":{}`, `"attributes":{"` + component + `":{}}`}},
	} {
		data, err := json.Marshal(tt.v)
		if err != nil {
			t.Errorf("%T: %v", tt.v, err)
			continue
		}
		if problem := value.CheckJSON(data); problem != "" {
			t.Errorf("%T is written as %s, which %s", tt.v, data, problem)
		}
		for _, w := range tt.want {
			if !bytes.Contains(data, []byte(w)) {
				t.Errorf("%T is written as %s, without %s", tt.v, data, w)
			}
		}
	}
	for _, v := range []any{
		Variables{SchemaVersion: 1, Variables: []Variable{{Scope: ScopeGlobal, Type: value.Int}}},
		ComponentTypes{ComponentTypes: []ComponentType{{Attributes: []AttributeDefinition{{Type: value.Int}}}},
			SchemaVersion: 1},
		Components{Components: []Component{{Attributes: AttributeValues{attribute: {}}}}, SchemaVersion: 1},
		Board{Nodes: Nodes{nil}, SchemaVersion: 1},
		Manifest{SchemaVersion: 1},
		SaveState{StateVersion: 1, Variables: map[string]value.Value{variable: {}}},
	} {
		if data, err := json.Marshal(v); err == nil {
			t.Errorf("%T, in which a value is not set, is written as %s", v, data)
		}
	}
	for _, d := range Documents() {
		zero := reflect.Zero(d.Root).Interface()
		if data, err := json.Marshal(zero); err == nil && value.CheckJSON(data) != "" {
			t.Errorf("the zero %T is written as %s", zero, data)
		}
	}
	// Every type of a slice or a map that a file always holds writes it as
	// [] or {} when it is nil, itself (marshalObject) or by the field's type.
	marshaler := reflect.TypeFor[json.Marshaler]()
	for _, rt := range packageTypes(t) {
		for i := range rt.NumField() {
			f := rt.Field(i)
			_, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
			kind := f.Type.Kind()
			if (kind == reflect.Slice || kind == reflect.Map) && !omits(opts) && !rt.Implements(marshaler) &&
				!f.Type.Implements(marshaler) {
				t.Errorf("%s.%s, which a file always holds, is written as null when it is nil", rt, f.Name)
			}
		}
	}
}

// TestCaseVariantKeys checks that a key that differs from one that an object
// defines only by its case, as KIND from kind, of any value, is taken by
// every schema and ignored by Decode, as a consumer, which reads a key as it
// is written, ignores it (PRD §6.4), rather than read as the field that the
// other key names, as encoding/json would read it.
func TestCaseVariantKeys(t *testing.T) {
	open := newValidator(t, false)
	checked := 0
	for _, d := range Documents() {
		for i, ex := range d.Examples {
			var doc any
			if err := json.Unmarshal([]byte(ex), &doc); err != nil {
				t.Fatal(err)
			}
			var walk func(v any, path string, isMap bool)
			walk = func(v any, path string, isMap bool) {
				switch v := v.(type) {
				case map[string]any:
					for _, k := range slices.Sorted(maps.Keys(v)) {
						c := v[k]
						_, object := c.(map[string]any)
						walk(c, path+"."+k, object && (isMap || slices.Contains(mapKeys, k)))
						variant := strings.ToUpper(k)
						if isMap || variant == k {
							continue
						}
						v[variant] = "unknown_value"
						data := mustMarshal(t, doc)
						delete(v, variant)
						checked++
						at := path + "." + variant
						if err := open.validate(t, d.Path, "", data); err != nil {
							t.Errorf("%s: example %d with %s does not validate: %v", d.Path, i, at, err)
						}
						out, err := roundTrip(d, data)
						if err != nil {
							t.Errorf("%s: example %d with %s does not decode: %v", d.Path, i, at, err)
						} else if !sameJSON(t, out, []byte(ex)) {
							t.Errorf("%s: example %d with %s is written again as\n%s", d.Path, i, at, out)
						}
					}
				case []any:
					for j, c := range v {
						walk(c, fmt.Sprintf("%s[%d]", path, j), false)
					}
				}
			}
			walk(doc, "$", false)
		}
	}
	if checked < 200 {
		t.Errorf("only %d keys were checked", checked)
	}
}

// TestIntegralNumbers checks that every integer of a file or a save state,
// written as 2.0 rather than 2, as a game that read it as a float may write
// it, is taken by every schema, as JSON Schema's integer takes it, and read
// by Decode as the integer that it is, as a consumer reads a number by its
// declaration (PRD §5.8.9).
func TestIntegralNumbers(t *testing.T) {
	open := newValidator(t, false)
	rewritten := 0
	for _, d := range Documents() {
		for i, ex := range d.Examples {
			dec := json.NewDecoder(strings.NewReader(ex))
			dec.UseNumber()
			var doc any
			if err := dec.Decode(&doc); err != nil {
				t.Fatal(err)
			}
			var float func(v any) any
			float = func(v any) any {
				switch v := v.(type) {
				case map[string]any:
					for k, c := range v {
						v[k] = float(c)
					}
				case []any:
					for j, c := range v {
						v[j] = float(c)
					}
				case json.Number:
					if !strings.ContainsAny(string(v), ".eE") {
						rewritten++
						return json.Number(string(v) + ".0")
					}
				}
				return v
			}
			data := mustMarshal(t, float(doc))
			if err := open.validate(t, d.Path, "", data); err != nil {
				t.Errorf("%s: example %d with its integers written as floats does not validate: %v", d.Path, i, err)
			}
			out, err := roundTrip(d, data)
			if err != nil {
				t.Errorf("%s: example %d with its integers written as floats does not decode: %v", d.Path, i, err)
			} else if !sameJSON(t, out, []byte(ex)) {
				t.Errorf("%s: example %d with its integers written as floats is written again as\n%s", d.Path, i, out)
			}
		}
	}
	if rewritten < 40 {
		t.Errorf("only %d integers were written as floats", rewritten)
	}
}

// TestDecodeText checks that Decode refuses a file whose strings a consumer
// would read otherwise than encoding/json does, which reads both a byte that
// is not UTF-8 and an escaped lone surrogate as U+FFFD.
func TestDecodeText(t *testing.T) {
	ex := []byte(boardExamples[0])
	for _, s := range []string{`"Harbourmaster"`, `"The keeper sent me."`, `"met_elara == true"`} {
		at := bytes.Index(ex, []byte(s)) + 1
		if at == 0 {
			t.Fatalf("the board's example has no %s", s)
		}
		insert := func(b []byte) []byte { return slices.Concat(ex[:at], b, ex[at:]) }
		var b Board
		if err := Decode(insert(nil), &b); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{
			"a byte that is not UTF-8": insert([]byte{0xff}),
			"a lone surrogate":         insert([]byte(string([]byte{92}) + "udc00")),
		} {
			if err := Decode(data, &b); !errors.Is(err, ErrDecode) {
				t.Errorf("Decode of the board with %s in %s = %v, want an error that wraps ErrDecode", name, s, err)
			}
		}
	}
}

// tagged has a field of each tag that Decode reads, as Generator reads them:
// optional enums, tagged omitempty and omitzero, an enum of a slice's
// strings, a const string, and fields that a property of an embedded struct
// tells, one there for some of its values and required there, and a value
// of the type that another property says.
type tagged struct {
	TaggedKind
	Default value.Value `json:"default" typeFrom:"type"`
	Empty   string      `json:"empty,omitempty" enum:"a,b"`
	Extra   string      `json:"extra,omitempty" when:"kind=one" required:"true" minLength:"1"`
	Marks   []string    `json:"marks,omitempty" enum:"x,y"`
	Op      string      `json:"op" const:"\"set\""`
	PairA   string      `json:"pairA,omitzero" with:"pairB"`
	PairB   Int         `json:"pairB,omitempty" with:"pairA" maximum:"9007199254740991"`
	Type    string      `json:"type" enum:"int,float,string,enum"`
	Zero    string      `json:"zero,omitzero" enum:"a,b"`
}

// TaggedKind is embedded in tagged, as encoding/json reads its field as
// tagged's own.
type TaggedKind struct {
	Kind string `json:"kind" enum:"one,two"`
}

// schemaOf returns the JSON Schema that a generator writes of the type root,
// as a document of its own, compiled by the tests' validator.
func schemaOf(t *testing.T, root reflect.Type) *jsonschema.Schema {
	t.Helper()
	const path = "schema/test/v1/test.schema.json"
	data, err := schemagen.New(false).Document(schemagen.Document{Path: path, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(schemagen.BaseURL+path, doc); err != nil {
		t.Fatal(err)
	}
	return c.MustCompile(schemagen.BaseURL + path)
}

// TestTagsAgreeWithSchema checks that Decode refuses exactly what the schema
// that Generator writes of a type refuses by the tags that Decode reads, of
// a type that has each of them: an optional enum that is not there, whether
// it is tagged omitempty or omitzero, is taken, and one that is there,
// whatever its value, is one of the enum's, the empty string not among them;
// so is each string of a slice that the enum tags; a const field is its
// value; a field that a property of an embedded struct tells is there exactly
// for the property's values that it names, and is not empty there, where it
// is required; two fields tagged with each other are each there exactly
// when the other is, whatever their values; a value is of the type that its
// property says, an enum's a string; and a key that the schema requires is
// there.
func TestTagsAgreeWithSchema(t *testing.T) {
	s := schemaOf(t, reflect.TypeFor[tagged]())
	const base = `"default":1,"op":"set","type":"int"`
	inputs := []string{
		`{"kind":"two",` + base + `}`,
		`{"kind":"one",` + base + `,"extra":"e"}`,
		`{"kind":"one",` + base + `}`,
		`{"kind":"one",` + base + `,"extra":""}`,
		`{"kind":"two",` + base + `,"extra":"e"}`,
		`{"kind":"two",` + base + `,"extra":""}`,
		`{"kind":"three",` + base + `}`,
		`{` + base + `}`,
		`{"kind":"two","default":1,"op":"get","type":"int"}`,
		`{"kind":"two","default":1,"type":"int"}`,
		`{"kind":"two","default":1,"op":"set"}`,
		`{"kind":"two","op":"set","type":"int"}`,
		`{"kind":"two",` + base + `,"empty":"a","zero":"b","marks":["x","y"]}`,
		`{"kind":"two",` + base + `,"marks":[]}`,
		`{"kind":"two",` + base + `,"marks":["x","z"]}`,
		`{"kind":"two",` + base + `,"marks":[""]}`,
		`{"kind":"two",` + base + `,"empty":""}`,
		`{"kind":"two",` + base + `,"zero":""}`,
		`{"kind":"two",` + base + `,"zero":"c"}`,
		`{"kind":"two","default":2.0,"op":"set","type":"int"}`,
		`{"kind":"two","default":2.5,"op":"set","type":"int"}`,
		`{"kind":"two","default":"1","op":"set","type":"int"}`,
		`{"kind":"two","default":{"a":1},"op":"set","type":"int"}`,
		`{"kind":"two","default":"s","op":"set","type":"string"}`,
		`{"kind":"two","default":1,"op":"set","type":"string"}`,
		`{"kind":"two","default":"calm","op":"set","type":"enum"}`,
		`{"kind":"two","default":true,"op":"set","type":"enum"}`,
		`{"kind":"two","default":-1.7976931348623157e308,"op":"set","type":"float"}`,
		`{"kind":"two","default":-1.7976931348623158e308,"op":"set","type":"float"}`,
		`{"kind":"two","default":1e309,"op":"set","type":"float"}`,
		`{"kind":"two",` + base + `,"pairA":"a","pairB":1}`,
		`{"kind":"two",` + base + `,"pairA":"","pairB":0}`,
		`{"kind":"two",` + base + `,"pairA":"a"}`,
		`{"kind":"two",` + base + `,"pairB":1}`,
		`{"kind":"two",` + base + `,"pairA":""}`,
		`{}`,
	}
	for _, data := range inputs {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		schema := s.Validate(inst) == nil
		var v tagged
		if err := Decode([]byte(data), &v); (err == nil) != schema {
			t.Errorf("Decode(%s) = %v, but the schema takes it: %v", data, err, schema)
		}
	}
}

// packageTypes returns the struct types of this package that the documents'
// roots reach, through fields, pointers, slices, maps, and the variants of
// the unions that Generator declares.
func packageTypes(t *testing.T) []reflect.Type {
	t.Helper()
	unions := map[reflect.Type][]reflect.Type{
		reflect.TypeFor[Node]():     {reflect.TypeFor[Element](), reflect.TypeFor[Branch](), reflect.TypeFor[Jumper]()},
		reflect.TypeFor[Snapshot](): {reflect.TypeFor[ReleaseSnapshot](), reflect.TypeFor[DraftSnapshot]()},
	}
	seen := map[reflect.Type]bool{}
	var out []reflect.Type
	var visit func(rt reflect.Type)
	visit = func(rt reflect.Type) {
		if seen[rt] || rt.PkgPath() != "" && rt.PkgPath() != thisPackage {
			return
		}
		seen[rt] = true
		switch rt.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map:
			visit(rt.Elem())
		case reflect.Interface:
			variants, ok := unions[rt]
			if !ok {
				t.Fatalf("%s is no union that the test knows", rt)
			}
			for _, v := range variants {
				visit(v)
			}
		case reflect.Struct:
			out = append(out, rt)
			for i := range rt.NumField() {
				visit(rt.Field(i).Type)
			}
		}
	}
	for _, d := range Documents() {
		visit(d.Root)
	}
	return out
}

// TestKnownKeys checks that every name of a field of the package's types,
// which encoding/json reads the field by, is a key that the schemas define,
// which Decode keeps, and that no two keys that they define differ only by
// their case, so that a key that Decode keeps is read as it is written.
func TestKnownKeys(t *testing.T) {
	known := knownKeys().known
	for _, rt := range packageTypes(t) {
		for i := range rt.NumField() {
			name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
			if name != "" && name != "-" && !known[name] {
				t.Errorf("%s.%s is named %s, which no schema defines", rt, rt.Field(i).Name, name)
			}
		}
	}
	keys := slices.Sorted(maps.Keys(known))
	for i, a := range keys {
		for _, b := range keys[i+1:] {
			if strings.EqualFold(a, b) {
				t.Errorf("the schemas define %s and %s, which differ only by their case", a, b)
			}
		}
	}
	if len(keys) < 50 {
		t.Errorf("the schemas define only %d keys", len(keys))
	}
}

// TestTags checks that the patterns and the bounds that the fields' tags hold
// a key, a slug, a path, a port, an integer, a title, tags, an enum's
// options, and a position to are the ones that their packages define: an
// identifier's of package ident, a slug's of package slug, an ID's of
// package ids, an Int's, MaxInt, a node's of package node, and an option's
// of package value, so that a file built from what a package takes is one
// that the schemas take; and that every integer is an Int, which reads an
// integer written as 2.0 (PRD §5.8.9).
func TestTags(t *testing.T) {
	const keyPattern = `^[A-Za-z_][A-Za-z0-9_]*$`
	slugBody := `[a-z0-9]+(?:-[a-z0-9]+)*`
	body := func(k ids.Kind) string {
		return strings.TrimSuffix(strings.TrimPrefix(k.CanonicalPattern(), "^"+k.Prefix()+"_"), "$")
	}
	// Each pattern of a string, with the maxLength that a field of it has.
	patterns := map[string]string{
		keyPattern:                        strconv.Itoa(ident.MaxLength),
		"^" + slugBody + "$":              strconv.Itoa(slug.MaxLength),
		"^boards/" + slugBody + `\.json$`: "",
		"^(?:out|" + strings.Trim(ids.BranchOutput.CanonicalPattern(), "^$") + ")$": "",
	}
	filePattern := `^(?:project/(?:settings|variables|component_types|components)\.json|boards/` + slugBody +
		`\.json|assets/` + body(ids.Asset) + `\.(?:png|jpg|webp|gif|svg))$`
	// Each bound of a field that no pattern bounds, by the field's name, the
	// limit that its package holds what the file is built from to.
	coordinate := strconv.Itoa(node.MaxCoordinate)
	limits := map[string]map[string]string{
		"Title":   {"maxLength": strconv.Itoa(node.MaxTitleLength)},
		"Tags":    {"maxItems": strconv.Itoa(node.MaxTags), "maxLength": strconv.Itoa(node.MaxTagLength)},
		"Options": {"maxItems": strconv.Itoa(value.MaxOptions), "maxLength": strconv.Itoa(value.MaxOptionLength)},
		"X":       {"minimum": "-" + coordinate, "maximum": coordinate},
		"Y":       {"minimum": "-" + coordinate, "maximum": coordinate},
	}
	for _, rt := range packageTypes(t) {
		for i := range rt.NumField() {
			f := rt.Field(i)
			at := rt.String() + "." + f.Name
			for _, tag := range []string{"maxLength", "maxItems", "minimum", "maximum"} {
				got, ok := f.Tag.Lookup(tag)
				want, limited := limits[f.Name][tag]
				_, patterned := f.Tag.Lookup("pattern")
				switch {
				case tag == "maxLength" && patterned, f.Type == reflect.TypeFor[Int]():
					// The pattern's, or MaxInt, which are checked below.
				case limited && got != want:
					t.Errorf("%s has the %s %q, not %q, its package's limit", at, tag, got, want)
				case ok && !limited:
					t.Errorf("%s has the %s %q, which the test ties to no package's limit", at, tag, got)
				}
			}
			if p, ok := f.Tag.Lookup("pattern"); ok {
				maxLength, known := patterns[p]
				if !known {
					t.Errorf("%s has the pattern %s, which is none that a package defines", at, p)
				} else if got := f.Tag.Get("maxLength"); got != maxLength {
					t.Errorf("%s has the maxLength %q, not %q, of its pattern %s", at, got, maxLength, p)
				}
			}
			if p, ok := f.Tag.Lookup("keyPattern"); ok && p != filePattern {
				t.Errorf("%s has the keyPattern %s, not a file's path's, %s", at, p, filePattern)
			}
			switch {
			case f.Type == reflect.TypeFor[Int]():
				if _, isConst := f.Tag.Lookup("const"); !isConst && f.Tag.Get("maximum") != strconv.Itoa(MaxInt) {
					t.Errorf("%s has the maximum %q, not MaxInt", at, f.Tag.Get("maximum"))
				}
			case f.Type.Kind() >= reflect.Int && f.Type.Kind() <= reflect.Uint64:
				t.Errorf("%s is a %s, rather than an Int", at, f.Type)
			}
		}
	}
	key := regexp.MustCompile(keyPattern)
	slugs := regexp.MustCompile("^" + slugBody + "$")
	for _, s := range []string{"", "a", "A_1", "_", "1a", "a-b", "a b", "é", "if", strings.Repeat("a", 64),
		strings.Repeat("a", 65), "harbour-intro", "-a", "a-", "a--b", "Harbour", "con"} {
		var e *ident.Error
		err := ident.Check(s)
		isKey := err == nil || errors.As(err, &e) && e.Reason == ident.Reserved
		if got := key.MatchString(s) && len(s) <= ident.MaxLength; got != isKey {
			t.Errorf("the key pattern takes %q: %v, but ident: %v", s, got, err)
		}
		if got := slugs.MatchString(s) && len(s) <= slug.MaxLength; got != slug.WellFormed(s) {
			t.Errorf("the slug pattern takes %q: %v, but slug.WellFormed: %v", s, got, slug.WellFormed(s))
		}
	}
}

// edited returns the JSON of the example ex with edit applied to it.
func edited(t *testing.T, ex string, edit func(doc map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(ex), &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	return mustMarshal(t, doc)
}

// item returns the object at index i of the array at key of obj.
func item(t *testing.T, obj map[string]any, key string, i int) map[string]any {
	t.Helper()
	items, ok := obj[key].([]any)
	if !ok || i >= len(items) {
		t.Fatalf("no %s[%d]", key, i)
	}
	o, ok := items[i].(map[string]any)
	if !ok {
		t.Fatalf("%s[%d] is not an object", key, i)
	}
	return o
}

// TestVariantFields checks that a field that is there only in some variants
// of an object (a when tag), as a branch's output's condition, a board's
// variable's boardId, or an enum's options, is refused where its variant does
// not have it, whatever its value, false and empty ones included, and where
// its variant requires it but it is missing or empty, and that a key that a
// variant of a union does not define but another does, as a jumper's text or
// a draft's version, is refused, by every schema and by Decode, as a consumer
// refuses it.
func TestVariantFields(t *testing.T) {
	open := newValidator(t, false)
	node := func(doc map[string]any, typ string) map[string]any {
		for i := range doc["nodes"].([]any) {
			if n := item(t, doc, "nodes", i); n["type"] == typ {
				return n
			}
		}
		t.Fatalf("the board's example has no %s", typ)
		return nil
	}
	board := func(doc map[string]any) map[string]any { return node(doc, NodeBranch) }
	condition := map[string]any{"ast": map[string]any{"kind": "lit", "type": "bool", "value": true}, "source": "true"}
	text := map[string]any{"preview": "a", "segments": []any{map[string]any{"kind": "text", "marks": []any{}, "value": "a"}}}
	snapshot := func(doc map[string]any) map[string]any { return doc["snapshot"].(map[string]any) }
	for name, tt := range map[string]struct {
		path, ex string
		edit     func(doc map[string]any)
	}{
		"an else output with a condition": {"board", boardExamples[0], func(doc map[string]any) {
			item(t, board(doc), "outputs", 1)["condition"] = condition
		}},
		"an if output without a condition": {"board", boardExamples[0], func(doc map[string]any) {
			delete(item(t, board(doc), "outputs", 0), "condition")
		}},
		"a global variable with a board": {"variables", variablesExamples[0], func(doc map[string]any) {
			item(t, doc, "variables", 0)["boardId"] = "bd_01HQ8Z9N4T7K2W5XRVNPQ8DCBE"
		}},
		"a global variable reset on entering a board": {"variables", variablesExamples[0], func(doc map[string]any) {
			item(t, doc, "variables", 0)["resetOnEnter"] = true
		}},
		"a board's variable without its board": {"variables", variablesExamples[1], func(doc map[string]any) {
			delete(item(t, doc, "variables", 0), "boardId")
		}},
		"an enum without options": {"component_types", componentTypesExamples[0], func(doc map[string]any) {
			delete(item(t, item(t, doc, "componentTypes", 0), "attributes", 1), "options")
		}},
		"an int with options": {"component_types", componentTypesExamples[0], func(doc map[string]any) {
			item(t, item(t, doc, "componentTypes", 0), "attributes", 0)["options"] = []any{"a"}
		}},
		"a global variable with an empty board": {"variables", variablesExamples[0], func(doc map[string]any) {
			item(t, doc, "variables", 0)["boardId"] = ""
		}},
		"a global variable not reset on entering a board": {"variables", variablesExamples[0], func(doc map[string]any) {
			item(t, doc, "variables", 0)["resetOnEnter"] = false
		}},
		"an int with no options": {"component_types", componentTypesExamples[0], func(doc map[string]any) {
			item(t, item(t, doc, "componentTypes", 0), "attributes", 0)["options"] = []any{}
		}},
		"an enum with no options": {"component_types", componentTypesExamples[0], func(doc map[string]any) {
			item(t, item(t, doc, "componentTypes", 0), "attributes", 1)["options"] = []any{}
		}},
		"an else output with an empty condition": {"board", boardExamples[0], func(doc map[string]any) {
			item(t, board(doc), "outputs", 1)["condition"] = map[string]any{}
		}},
		"a draft with a version": {"storycreep", manifestExamples[1], func(doc map[string]any) {
			snapshot(doc)["version"] = "1.4.0"
		}},
		"a draft with an ID": {"storycreep", manifestExamples[1], func(doc map[string]any) {
			snapshot(doc)["id"] = "snp_01HQ9A7TBE5N2WQX8K4YJ3MDCR"
		}},
		"a release with the moment of a pull": {"storycreep", manifestExamples[0], func(doc map[string]any) {
			snapshot(doc)["pulledAt"] = "2026-10-13T09:15:00Z"
		}},
		"a jumper with text": {"board", boardExamples[1], func(doc map[string]any) {
			node(doc, NodeJumper)["text"] = text
		}},
		"a jumper with no outputs": {"board", boardExamples[1], func(doc map[string]any) {
			node(doc, NodeJumper)["outputs"] = []any{}
		}},
		"a jumper that is no ending": {"board", boardExamples[1], func(doc map[string]any) {
			node(doc, NodeJumper)["isEnding"] = false
		}},
		"an element with a target": {"board", boardExamples[1], func(doc map[string]any) {
			node(doc, NodeElement)["target"] = node(doc, NodeJumper)["target"]
		}},
		"a branch of a kind": {"board", boardExamples[0], func(doc map[string]any) {
			board(doc)["kind"] = "dialogue"
		}},
		"a state with a snapshot's ID without its version": {"../../save-state/v1/save-state", saveStateExamples[0],
			func(doc map[string]any) { delete(doc, "snapshotVersion") }},
		"a state with a snapshot's version without its ID": {"../../save-state/v1/save-state", saveStateExamples[0],
			func(doc map[string]any) { delete(doc, "snapshotId") }},
		"a draft's state with a snapshot's version": {"../../save-state/v1/save-state", saveStateExamples[1],
			func(doc map[string]any) { doc["snapshotVersion"] = "1.4.0" }},
	} {
		t.Run(name, func(t *testing.T) {
			data := edited(t, tt.ex, tt.edit)
			file := path.Clean(PublishedDir + "/" + tt.path + ".schema.json")
			if err := open.validate(t, file, "", data); err == nil {
				t.Errorf("%s validates", file)
			}
			var d schemagen.Document
			for _, doc := range Documents() {
				if doc.Path == file {
					d = doc
				}
			}
			if _, err := roundTrip(d, data); !errors.Is(err, ErrDecode) {
				t.Errorf("Decode = %v, want an error that wraps ErrDecode", err)
			}
		})
	}
}

// TestDecodeValues checks that every schema and Decode refuse a value that is
// not of its declaration's type, a variable's default, an attribute
// definition's, a component's value, and a save state's, an enum's that is
// no string, and a float's beyond the greatest double's shortest form, and
// an empty text or label, which a file leaves out, and that
// Decode refuses, beyond the schemas, a number written with an exponent
// beyond ±999, wherever it is, which the schemas cannot tell from the number
// written otherwise.
func TestDecodeValues(t *testing.T) {
	open := newValidator(t, false)
	for name, tt := range map[string]struct {
		path, ex string
		edit     func(doc map[string]any)
		schema   bool
	}{
		"an int's default that is a string": {"variables", variablesExamples[0], func(doc map[string]any) {
			item(t, doc, "variables", 1)["default"] = "1"
		}, false},
		"an int's default that is not integral": {"variables", variablesExamples[0], func(doc map[string]any) {
			item(t, doc, "variables", 1)["default"] = 2.5
		}, false},
		"an int's default that is an object": {"variables", variablesExamples[0], func(doc map[string]any) {
			item(t, doc, "variables", 1)["default"] = map[string]any{"a": 1}
		}, false},
		"a float's default beyond the greatest double's shortest form": {"variables", variablesExamples[1], func(doc map[string]any) {
			item(t, doc, "variables", 0)["default"] = json.RawMessage("1.7976931348623158e308")
		}, false},
		"a bool's default that is a number": {"variables", variablesExamples[0], func(doc map[string]any) {
			item(t, doc, "variables", 0)["default"] = 0
		}, false},
		"an enum's default that is a number": {"component_types", componentTypesExamples[0], func(doc map[string]any) {
			item(t, item(t, doc, "componentTypes", 0), "attributes", 1)["default"] = 1
		}, false},
		"a text's default that is a boolean": {"component_types", componentTypesExamples[0], func(doc map[string]any) {
			item(t, item(t, doc, "componentTypes", 0), "attributes", 3)["default"] = true
		}, false},
		"a component's value that is an array": {"components", componentsExamples[0], func(doc map[string]any) {
			item(t, doc, "components", 0)["attributes"].(map[string]any)["atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF"] = []any{1}
		}, false},
		"a state's value that is an object": {"../../save-state/v1/save-state", saveStateExamples[0], func(doc map[string]any) {
			doc["variables"].(map[string]any)["var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"] = map[string]any{}
		}, false},
		"an empty text": {"board", boardExamples[1], func(doc map[string]any) {
			item(t, doc, "nodes", 0)["text"] = map[string]any{"preview": "", "segments": []any{}}
		}, false},
		"an empty label": {"board", boardExamples[1], func(doc map[string]any) {
			item(t, doc, "connections", 0)["label"] = map[string]any{"segments": []any{}}
		}, false},
		"a default written with an exponent beyond 999": {"variables", variablesExamples[0], func(doc map[string]any) {
			item(t, doc, "variables", 1)["default"] = json.RawMessage("0e1000")
		}, true},
		"a position written with an exponent beyond 999": {"board", boardExamples[1], func(doc map[string]any) {
			item(t, doc, "nodes", 0)["position"].(map[string]any)["x"] = json.RawMessage("1E-1000")
		}, true},
		"an unknown key's number written with an exponent beyond 999": {"board", boardExamples[1], func(doc map[string]any) {
			doc["future"] = []any{json.RawMessage("-0.0e+01000")}
		}, true},
	} {
		t.Run(name, func(t *testing.T) {
			data := edited(t, tt.ex, tt.edit)
			file := path.Clean(PublishedDir + "/" + tt.path + ".schema.json")
			if err := open.validate(t, file, "", data); (err == nil) != tt.schema {
				t.Errorf("%s takes it: %v, want %v", file, err == nil, tt.schema)
			}
			var d schemagen.Document
			for _, doc := range Documents() {
				if doc.Path == file {
					d = doc
				}
			}
			if _, err := roundTrip(d, data); !errors.Is(err, ErrDecode) {
				t.Errorf("Decode = %v, want an error that wraps ErrDecode", err)
			}
		})
	}
}

// TestMissingKeys checks that Decode refuses an example without one of its
// keys exactly when every schema refuses it, as a consumer does: one that its
// object's schema, or its variant's, requires, wherever it is.
func TestMissingKeys(t *testing.T) {
	open := newValidator(t, false)
	checked := 0
	for _, d := range Documents() {
		for i, ex := range d.Examples {
			var doc any
			if err := json.Unmarshal([]byte(ex), &doc); err != nil {
				t.Fatal(err)
			}
			var walk func(v any, path string)
			walk = func(v any, path string) {
				switch v := v.(type) {
				case map[string]any:
					for _, k := range slices.Sorted(maps.Keys(v)) {
						c := v[k]
						walk(c, path+"."+k)
						delete(v, k)
						data := mustMarshal(t, doc)
						v[k] = c
						checked++
						schema := open.validate(t, d.Path, "", data) == nil
						if _, err := roundTrip(d, data); (err == nil) != schema {
							t.Errorf("%s: example %d without %s.%s: Decode = %v, but the schema takes it: %v", d.Path, i,
								path, k, err, schema)
						}
					}
				case []any:
					for j, c := range v {
						walk(c, fmt.Sprintf("%s[%d]", path, j))
					}
				}
			}
			walk(doc, "$")
		}
	}
	if checked < 300 {
		t.Errorf("only %d keys were left out", checked)
	}
}

// TestMarshalRefuses checks that the types refuse to write what Decode
// refuses, or what a file leaves out, rather than write a file that every
// schema refuses: a value of an enum that the format does not have, another
// schemaVersion, a field that its variant does not have or that it requires,
// a save state's snapshotId without its snapshotVersion, which it is there
// with, an empty text or label, an AST that is not resolved, which expr.Decode
// does not read back, and a value that is not of its declaration's type, or
// that no value is.
func TestMarshalRefuses(t *testing.T) {
	unresolved, err := expr.Parse("met_elara == true", expr.ContextCondition)
	if err != nil {
		t.Fatal(err)
	}
	decoded := func(ex string) *Board {
		var b Board
		if err := Decode([]byte(ex), &b); err != nil {
			t.Fatal(err)
		}
		if _, err := json.Marshal(b); err != nil {
			t.Fatalf("the example is not written: %v", err)
		}
		return &b
	}
	element := func(b *Board) *Element {
		for _, n := range b.Nodes {
			if e, ok := n.(*Element); ok && e.Text != nil {
				return e
			}
		}
		t.Fatal("the board's example has no element with text")
		return nil
	}
	branch := func(b *Board) *Branch {
		for _, n := range b.Nodes {
			if br, ok := n.(*Branch); ok {
				return br
			}
		}
		t.Fatal("the board's example has no branch")
		return nil
	}
	labelled := func(b *Board) *Connection {
		for i, c := range b.Connections {
			if c.Label != nil {
				return &b.Connections[i]
			}
		}
		t.Fatal("the board's example has no connection with a label")
		return nil
	}
	for name, edit := range map[string]func(b *Board){
		"a kind of element that is none":    func(b *Board) { element(b).Kind = "monologue" },
		"another schemaVersion":             func(b *Board) { b.SchemaVersion = 2 },
		"an empty text":                     func(b *Board) { element(b).Text = &Text{} },
		"an empty label":                    func(b *Board) { labelled(b).Label = &Label{} },
		"an else output with a condition":   func(b *Board) { branch(b).Outputs[1].Condition = branch(b).Outputs[0].Condition },
		"an if output without a condition":  func(b *Board) { branch(b).Outputs[0].Condition = nil },
		"a condition that is not resolved":  func(b *Board) { branch(b).Outputs[0].Condition.AST = unresolved },
		"a condition whose AST is a script": func(b *Board) { branch(b).Outputs[0].Condition.AST = &expr.Script{} },
	} {
		b := decoded(boardExamples[0])
		edit(b)
		if data, err := json.Marshal(b); err == nil {
			t.Errorf("a board with %s is written as %s", name, data)
		}
	}
	var v Variables
	if err := Decode([]byte(variablesExamples[0]), &v); err != nil {
		t.Fatal(err)
	}
	v.Variables[0].BoardID = "bd_01HQ8Z9N4T7K2W5XRVNPQ8DCBE"
	if data, err := json.Marshal(v); err == nil {
		t.Errorf("a global variable with a board is written as %s", data)
	}
	var c ComponentTypes
	if err := Decode([]byte(componentTypesExamples[0]), &c); err != nil {
		t.Fatal(err)
	}
	c.ComponentTypes[0].Attributes[1].Options = nil
	if data, err := json.Marshal(c); err == nil {
		t.Errorf("an enum without options is written as %s", data)
	}
	if data, err := json.Marshal(AST{Node: unresolved}); err == nil {
		t.Errorf("an AST that is not resolved is written as %s", data)
	}
	var st SaveState
	if err := Decode([]byte(saveStateExamples[0]), &st); err != nil {
		t.Fatal(err)
	}
	st.SnapshotVersion = ""
	if data, err := json.Marshal(st); err == nil {
		t.Errorf("a state with a snapshot's ID without its version is written as %s", data)
	}
	// A value that is not of its declaration's type, or that no value is.
	for _, raw := range []string{`"1"`, `2.5`, `{"a":1}`, `[1]`, `0e1000`} {
		var w Variables
		if err := Decode([]byte(variablesExamples[0]), &w); err != nil {
			t.Fatal(err)
		}
		w.Variables[1].Default = value.FromJSON([]byte(raw))
		if data, err := json.Marshal(w); err == nil {
			t.Errorf("an int variable whose default is %s is written as %s", raw, data)
		}
	}
	var cs Components
	if err := Decode([]byte(componentsExamples[0]), &cs); err != nil {
		t.Fatal(err)
	}
	cs.Components[0].Attributes["atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF"] = value.FromJSON([]byte(`{"a":1}`))
	if data, err := json.Marshal(cs); err == nil {
		t.Errorf("a component whose value is an object is written as %s", data)
	}
}

// TestFoldKey checks that two keys fold alike exactly when strings.EqualFold,
// by which encoding/json matches a key with a field's name, takes them, and
// that Decode ignores a key that differs from a known one by Unicode's case
// folding, as the Kelvin sign's K from k, as encoding/json would not, and
// written with an escape.
func TestFoldKey(t *testing.T) {
	keys := []string{"kind", "KIND", "Kind", "\u212aind", "kinds", "kin", "\u017fource", "source", "SOURCE", "é", "É",
		"ß", "ẞ", ""}
	for _, a := range keys {
		for _, b := range keys {
			if (foldKey(a) == foldKey(b)) != strings.EqualFold(a, b) {
				t.Errorf("foldKey(%q) = %q and foldKey(%q) = %q, but strings.EqualFold: %v", a, foldKey(a), b,
					foldKey(b), strings.EqualFold(a, b))
			}
		}
	}
	var e Element
	data := `{"id":"nd_01HQ8Z41M9D2PK7VW3RB6XGHTY","isEnding":false,"kind":"narration","\u212aind":"dialogue",` +
		`"position":{"x":0,"y":0},"type":"element"}`
	if err := Decode([]byte(data), &e); err != nil || e.Kind != "narration" {
		t.Errorf("Decode(%s) = %v, its kind %q, want narration", data, err, e.Kind)
	}
}
