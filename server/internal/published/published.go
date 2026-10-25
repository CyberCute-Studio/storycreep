// Package published holds the Go types of Storycreep's published game
// format, version SchemaVersion, which the Godot addon pulls (PRD §6), and of
// the save-state format of its runtime, version StateVersion (§8.5), from
// which package schemagen generates the JSON Schemas that /schema publishes
// and the web app's TypeScript types (FR-DATA-04, plan item 41, note N49).
// schema/published/README.md and schema/save-state/README.md describe the
// formats, and what a consumer checks of them beyond their schemas.
//
// The types write and read the files' JSON, each object with only the fields
// that it has, never null, and a union's variant with the value that tells it
// (MarshalJSON). They do not lay the JSON out as §6.2 says, sorted and
// indented, which the serializer does (plan item 76), and Decode reads a
// file as a consumer does: it fails on null anywhere and on an unknown value
// of a type, kind, or op field, and ignores a key that it does not know
// (§6.4).
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

// The patterns that the files' fields are held to.
const (
	// slugPattern is a slug's (package slug), which names a board's file.
	slugPattern = `^[a-z0-9]+(?:-[a-z0-9]+)*$`
	// keyPattern is a key's, an identifier of the expression language
	// (package ident).
	keyPattern = `^[A-Za-z_][A-Za-z0-9_]*$`
	// boardFilePattern is a board's file's path.
	boardFilePattern = `^boards/[a-z0-9]+(?:-[a-z0-9]+)*\.json$`
	// filePattern is the path of a file that a snapshot lists (§7.5's path
	// rules): a project-level file, a board's, or an asset's, named by the
	// body of its ID.
	filePattern = `^(?:project/(?:settings|variables|component_types|components)\.json|` +
		`boards/[a-z0-9]+(?:-[a-z0-9]+)*\.json|` +
		`assets/[01234567][0123456789ABCDEFGHJKMNPQRSTVWXYZ]{25}\.(?:png|jpg|webp|gif|svg))$`
	// portPattern is a connection's port: an element's out, or a branch's
	// output's ID.
	portPattern = `^(?:out|bo_[01234567][0123456789ABCDEFGHJKMNPQRSTVWXYZ]{25})$`
)

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

// Int is an integer of a save state, which a game may have read back as a
// float, as Godot's JSON parser reads every number (§5.8.9): it is read as an
// int when it is integral, as 2.0 is 2, within ±(2^53 − 1).
type Int int64

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
// value, that holds null anywhere (§6.2), or that escapes a lone surrogate
// in a string; on a value of a field of an enum, as a type, a kind, or an op,
// that the format does not have, as a consumer fails loudly on one (§6.4),
// and on a schemaVersion or a stateVersion of another version than the
// types'; and otherwise as json.Unmarshal reads data into v, ignoring a key
// that the types do not know (§6.4). It does not check what else the schema
// holds a file to, as a pattern or a bound, which a validator of the schema
// checks.
func Decode(data []byte, v any) error {
	if !json.Valid(data) {
		return fmt.Errorf("%w: it is not one JSON value", ErrDecode)
	}
	if value.EscapesLoneSurrogate(data) {
		return fmt.Errorf("%w: a string escapes a lone surrogate", ErrDecode)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			// json.Valid read data whole, so the only error is its end.
			break
		}
		if tok == nil {
			return fmt.Errorf("%w: it holds null", ErrDecode)
		}
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%w: %w", ErrDecode, err)
	}
	if err := checkTags(reflect.ValueOf(v), "$"); err != nil {
		return fmt.Errorf("%w: %w", ErrDecode, err)
	}
	return nil
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
				optional := strings.Contains(opts, "omitempty") && fv.String() == ""
				if !optional && !slices.Contains(strings.Split(enum, ","), fv.String()) {
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

// marshalVariant writes v, a union's variant as a type without methods, with
// the union's key, of the variant's value, first.
func marshalVariant(key, val string, v any) ([]byte, error) {
	data, err := marshal(v)
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
