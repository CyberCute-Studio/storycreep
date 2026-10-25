// Package published holds the Go types of Storycreep's published game
// format, version SchemaVersion, which the Godot addon pulls (PRD §6), and of
// the save-state format of its runtime, version StateVersion (§8.5), from
// which package schemagen generates the JSON Schemas that /schema publishes
// and the web app's TypeScript types (FR-DATA-04, plan item 41, note N49).
// schema/published/README.md and schema/save-state/README.md describe the
// formats, and what a consumer checks of them beyond their schemas.
//
// The types write and read the files' JSON, each object with only the fields
// that it has, and a union's variant with the value that tells it
// (MarshalJSON), never null: an array or an object that a file always holds,
// as a branch's outputs or a component's attributes, is written [] or {} when
// its slice or map is nil, and a file fails to be written, rather than hold
// null, where a value is not set, as a variable's default or a node. They do
// not lay the JSON out as §6.2 says, sorted and indented, which the
// serializer does (plan item 76), and Decode reads a file as a consumer does:
// it fails on null anywhere and on an unknown value of a type, kind, or op
// field, and ignores a key that it does not know (§6.4).
package published

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// SchemaVersion is the version of the published game format that these
// types are of (§6.4).
const SchemaVersion = 1

// The paths of a snapshot's files, from the directory that holds them,
// res://storycreep/ in a game (§6.3), but for its boards' (BoardPath) and its
// assets'.
const (
	ManifestPath       = "storycreep.json"
	SettingsPath       = "project/settings.json"
	VariablesPath      = "project/variables.json"
	ComponentTypesPath = "project/component_types.json"
	ComponentsPath     = "project/components.json"
)

// BoardPath returns the path of the file of the board whose slug is slug.
func BoardPath(slug string) string {
	return "boards/" + slug + ".json"
}

// Hash is a file's hash, or a snapshot's content hash: sha256: and the
// lowercase hexadecimal SHA-256 of the bytes.
type Hash string

// JSONSchema gives the schemas the definition of a hash (schemagen.Provider).
func (Hash) JSONSchema(*schemagen.Generator) *schemagen.Schema {
	return &schemagen.Schema{Type: "string", Pattern: "^sha256:[0-9a-f]{64}$",
		Description: "sha256: and the lowercase hexadecimal SHA-256 of the bytes."}
}

// Timestamp is a moment, in UTC, to the second, as 2026-10-12T18:04:31Z.
type Timestamp string

// TimestampOf returns the moment t as a Timestamp, to the second that it is
// in.
func TimestampOf(t time.Time) Timestamp {
	return Timestamp(t.UTC().Truncate(time.Second).Format(time.RFC3339))
}

// JSONSchema gives the schemas the definition of a timestamp
// (schemagen.Provider).
func (Timestamp) JSONSchema(*schemagen.Generator) *schemagen.Schema {
	return &schemagen.Schema{Type: "string", Pattern: `^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`,
		Description: "A moment, in UTC, to the second (RFC 3339), as 2026-10-12T18:04:31Z."}
}

// Int is an integer of a file or of a save state, as an order, a count, or a
// visit count, which a consumer reads as an integer by its declaration,
// whatever its JSON form, and which a game may have read back as a float, as
// Godot's JSON parser reads every number (§5.8.9): it is read as an int when
// it is integral, as 2.0 is 2, within ±MaxInt, as a JSON Schema's integer
// takes it, of which the schemas hold each Int to at most MaxInt.
type Int int64

// MaxInt is the greatest Int, 2^53 − 1, the greatest up to which a double,
// and so a consumer's number, holds every integer.
const MaxInt = value.MaxInt

// UnmarshalJSON reads an integral JSON number, however it is written, as an
// Int.
func (n *Int) UnmarshalJSON(data []byte) error {
	v, problem := value.FromJSON(data).Read(value.Int, value.Checks{})
	if problem != "" {
		return fmt.Errorf("published: an integer %s", problem)
	}
	var i int64
	if err := json.Unmarshal(v.JSON(), &i); err != nil {
		return err
	}
	*n = Int(i)
	return nil
}

// ErrDecode is Decode's error for data that is not JSON that the published
// format holds.
var ErrDecode = errors.New("published: not JSON of the published format")

// Decode reads data, one JSON value, into v, a pointer to one of the
// package's types, as a consumer reads a published file or a save state: it
// fails, with an error that wraps ErrDecode, on data that is not one JSON
// value, that is not UTF-8, that holds null anywhere (§6.2), or that escapes
// a lone surrogate in a string (value.CheckJSON); on a value of a field of an
// enum, as a type, a kind, or an op, that the format does not have, as a
// consumer fails loudly on one (§6.4); and on a published file's
// schemaVersion or a save state's stateVersion that is not the types', though
// not on a save state's schemaVersion, its snapshot's, which a runtime
// compares with the snapshot that it runs. Otherwise it reads data into v as
// json.Unmarshal does, but for a key as a consumer reads it, as it is
// written: it ignores a key that the types do not know (§6.4), as KIND, which
// differs from kind only by its case, and which json.Unmarshal would read as
// kind. It does not check what else the schema holds a file to, as a pattern
// or a bound, which a validator of the schema checks.
func Decode(data []byte, v any) error {
	if problem := value.CheckJSON(data); problem != "" {
		return fmt.Errorf("%w: it %s", ErrDecode, problem)
	}
	known, err := knownKeys()
	if err != nil {
		return err
	}
	if data, err = dropCaseVariants(data, known); err != nil {
		return fmt.Errorf("%w: %w", ErrDecode, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%w: %w", ErrDecode, err)
	}
	if err := checkTags(reflect.ValueOf(v), "$"); err != nil {
		return fmt.Errorf("%w: %w", ErrDecode, err)
	}
	return nil
}

// knownKeys returns the keys that the schemas of Files define of every object
// of every document, which encoding/json reads the package's types' fields
// by, and of which Decode drops a key that differs from one only by its case
// (dropCaseVariants).
var knownKeys = sync.OnceValues(func() (map[string]bool, error) {
	files, err := Files(false)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	var collect func(v any)
	collect = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if props, ok := v["properties"].(map[string]any); ok {
				for k := range props {
					keys[k] = true
				}
			}
			for _, c := range v {
				collect(c)
			}
		case []any:
			for _, c := range v {
				collect(c)
			}
		}
	}
	for _, data := range files {
		var doc struct {
			Defs any `json:"$defs"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		collect(doc.Defs)
	}
	return keys, nil
})

// dropCaseVariants returns data, one JSON value, without each key of its
// objects, at any depth, that is none of known but differs from one of them
// only by its case, as KIND from kind, which encoding/json would read as the
// field that the known key names, where a consumer ignores it, as a key that
// it does not know (§6.4). It returns data itself when it has no such key.
// No two known keys differ only by their case (TestKnownKeys), so that every
// key that it keeps is read as a consumer reads it.
func dropCaseVariants(data []byte, known map[string]bool) ([]byte, error) {
	var tree any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return nil, err
	}
	dropped := false
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, c := range v {
				if !known[k] && foldsToOne(k, known) {
					delete(v, k)
					dropped = true
					continue
				}
				walk(c)
			}
		case []any:
			for _, c := range v {
				walk(c)
			}
		}
	}
	walk(tree)
	if !dropped {
		return data, nil
	}
	return marshal(tree)
}

// foldsToOne reports whether k is one of keys but for its case, as
// encoding/json matches a key with a field's name (strings.EqualFold).
func foldsToOne(k string, keys map[string]bool) bool {
	for n := range keys {
		if strings.EqualFold(k, n) {
			return true
		}
	}
	return false
}

// thisPackage is the path of this package, whose types checkTags reads.
var thisPackage = reflect.TypeFor[Manifest]().PkgPath()

// checkTags returns an error for a field of v, at path, at any depth, whose
// value its enum or const tag does not take, as Generator's schema would
// not, but for an optional field that is not there. It reads the values of
// this package's types, and none of another package's, as an AST's, whose
// own decoding checks them.
func checkTags(v reflect.Value, path string) error {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return checkTags(v.Elem(), path)
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if err := checkTags(v.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			if err := checkTags(iter.Value(), fmt.Sprintf("%s[%q]", path, iter.Key())); err != nil {
				return err
			}
		}
	case reflect.Struct:
		t := v.Type()
		if t.PkgPath() != thisPackage {
			return nil
		}
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
			at := path + "." + name
			fv := v.Field(i)
			if enum, ok := f.Tag.Lookup("enum"); ok && fv.Kind() == reflect.String {
				// An optional field, as Generator reads one, that is not there.
				absent := omits(opts) && fv.String() == ""
				if !absent && !slices.Contains(strings.Split(enum, ","), fv.String()) {
					return fmt.Errorf("%s is %q, which is none of %s", at, fv.String(), enum)
				}
			}
			if c, ok := f.Tag.Lookup("const"); ok && fv.CanInt() {
				if want, err := strconv.ParseInt(c, 10, 64); err != nil || fv.Int() != want {
					return fmt.Errorf("%s is %d, not %s", at, fv.Int(), c)
				}
			}
			if err := checkTags(fv, at); err != nil {
				return err
			}
		}
	}
	return nil
}

// marshal writes v as JSON without escaping HTML (§6.2).
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// omits reports whether opts, the options of a json tag, leave the field out
// when it is empty or zero, which makes it optional (schemagen.Generator).
func omits(opts string) bool {
	for o := range strings.SplitSeq(opts, ",") {
		if o == "omitempty" || o == "omitzero" {
			return true
		}
	}
	return false
}

// marshalObject writes v, a struct of a type without methods, as JSON, each
// of its slices and maps that its JSON always holds, as a branch's outputs,
// written as [] or {} when it is nil, rather than as null, which no file
// holds (§6.2). Each of the package's types that has such a field writes
// itself with it.
func marshalObject[T any](v T) ([]byte, error) {
	rv := reflect.ValueOf(&v).Elem()
	t := rv.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "-" || omits(opts) {
			continue
		}
		switch fv := rv.Field(i); {
		case fv.Kind() == reflect.Slice && fv.IsNil():
			fv.Set(reflect.MakeSlice(fv.Type(), 0, 0))
		case fv.Kind() == reflect.Map && fv.IsNil():
			fv.Set(reflect.MakeMap(fv.Type()))
		}
	}
	return marshal(v)
}

// marshalFile writes v, a file's struct of a type without methods, as
// marshalObject does, or fails, rather than write null, which no file holds
// (§6.2), where a value of it is not set, as a variable's default, a node, or
// a manifest's snapshot.
func marshalFile[T any](file string, v T) ([]byte, error) {
	data, err := marshalObject(v)
	if err != nil {
		return nil, err
	}
	if problem := value.CheckJSON(data); problem != "" {
		return nil, fmt.Errorf("published: %s %s, where a value is not set", file, problem)
	}
	return data, nil
}

// marshalVariant writes v, a union's variant as a type without methods, as
// marshalObject does, with the union's key, of the variant's value, first.
func marshalVariant[T any](key, val string, v T) ([]byte, error) {
	data, err := marshalObject(v)
	if err != nil {
		return nil, err
	}
	head, err := marshal(map[string]string{key: val})
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, []byte("{")) {
		return nil, fmt.Errorf("published: a variant of %s %s is not an object", key, val)
	}
	if bytes.Equal(data, []byte("{}")) {
		return head, nil
	}
	return append(append(head[:len(head)-1:len(head)-1], ','), data[1:]...), nil
}

// unmarshalVariant reads data, an object of a union of variants that the
// value of key tells apart, as the variant that variants makes for its value,
// or fails on a value that it does not know, as a consumer does (§6.4).
func unmarshalVariant[T any](data []byte, key string, variants map[string]func() T) (T, error) {
	var zero T
	var head map[string]json.RawMessage
	if err := json.Unmarshal(data, &head); err != nil {
		return zero, fmt.Errorf("published: a value of %s is not an object", key)
	}
	raw, ok := head[key]
	if !ok {
		return zero, fmt.Errorf("published: an object has no %s", key)
	}
	var val string
	if err := json.Unmarshal(raw, &val); err != nil {
		return zero, fmt.Errorf("published: the %s of an object is not a string", key)
	}
	newVariant, ok := variants[val]
	if !ok {
		return zero, fmt.Errorf("published: %q is no %s that this version of the format knows", val, key)
	}
	v := newVariant()
	if err := json.Unmarshal(data, v); err != nil {
		return zero, err
	}
	return v, nil
}
