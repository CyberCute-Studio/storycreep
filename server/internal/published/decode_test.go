package published

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
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
		{ComponentTypes{ComponentTypes: []ComponentType{{}}}, []string{`"attributes":[]`}},
		{Components{Components: []Component{{}}}, []string{`"attributes":{}`}},
		{Board{Nodes: Nodes{&Branch{}}}, []string{`"connections":[]`, `"outputs":[]`}},
		{Board{}, []string{`"connections":[]`, `"nodes":[]`}},
		{Manifest{Snapshot: &DraftSnapshot{}}, []string{`"boards":[]`, `"files":{}`}},
		{SaveState{Attributes: map[string]AttributeValues{component: nil}},
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
		Variables{Variables: []Variable{{}}},
		ComponentTypes{ComponentTypes: []ComponentType{{Attributes: []AttributeDefinition{{}}}}},
		Components{Components: []Component{{Attributes: AttributeValues{attribute: {}}}}},
		Board{Nodes: Nodes{nil}},
		Manifest{},
		SaveState{Variables: map[string]value.Value{variable: {}}},
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

// optionalEnums has fields of enums that are optional, as Generator reads a
// field tagged omitempty or omitzero.
type optionalEnums struct {
	Empty string `json:"empty,omitempty" enum:"a,b"`
	Zero  string `json:"zero,omitzero" enum:"a,b"`
}

// TestOptionalEnum checks that Decode takes an optional field of an enum that
// is not there, whether it is tagged omitempty or omitzero, as the schema
// does, and refuses one that is there with a value that the enum does not
// have.
func TestOptionalEnum(t *testing.T) {
	for data, ok := range map[string]bool{
		`{}`: true, `{"empty":"a","zero":"b"}`: true, `{"zero":"c"}`: false, `{"empty":"c"}`: false,
		`{"zero":""}`: true,
	} {
		var v optionalEnums
		if err := Decode([]byte(data), &v); (err == nil) != ok {
			t.Errorf("Decode(%s) = %v", data, err)
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
	known, err := knownKeys()
	if err != nil {
		t.Fatal(err)
	}
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
// a key, a slug, a path, a port, and an integer to are the ones that their
// packages define: an identifier's of package ident, a slug's of package
// slug, an ID's of package ids, and an Int's, MaxInt; and that every integer
// is an Int, which reads an integer written as 2.0 (PRD §5.8.9).
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
	for _, rt := range packageTypes(t) {
		for i := range rt.NumField() {
			f := rt.Field(i)
			at := rt.String() + "." + f.Name
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
