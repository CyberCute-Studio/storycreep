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
// its slice or map is nil, and a file fails to be written where Decode would
// refuse what it writes (marshalFile), as null where a value is not set, as a
// variable's default or a node, an empty text or label, which a file leaves
// out, or an AST that expr.Decode does not read back as its context's, as one
// whose names are not resolved. They do not lay the JSON out as §6.2 says,
// sorted and indented, which the serializer does (plan item 76), and Decode
// reads a file, refusing what its doc lists, as null anywhere and an unknown
// value of a type, kind, or op field, and ignoring a key that it does not
// know (§6.4), but it is no validator of the schemas.
package published

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

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
// takes it, of which the schemas hold each Int to at most MaxInt, but for
// one written with an exponent beyond ±value.MaxExponent, as 0e1000, which
// the schemas cannot tell from 0, but which no file holds, and which
// package value refuses (value.Integral).
type Int = value.Integral

// MaxInt is the greatest Int, 2^53 − 1, the greatest up to which a double,
// and so a consumer's number, holds every integer.
const MaxInt = value.MaxInt

// ErrDecode is Decode's error for data that is not JSON that the published
// format holds.
var ErrDecode = errors.New("published: not JSON of the published format")

// Decode reads data, one JSON value, a published file or a save state, into
// v, a pointer to one of the package's types, replacing what v held, which
// it leaves as it was where it fails. It fails, as json.Unmarshal does, with
// a *json.InvalidUnmarshalError, on a v that is not a pointer, or that is
// nil, and otherwise, with an error that wraps ErrDecode, on what this list
// names of data and on nothing else: on data that is not one JSON value,
// that is not UTF-8, that holds null anywhere (§6.2), that escapes a lone
// surrogate in a string, or that writes a number with an exponent beyond
// ±value.MaxExponent, as 0e1000, which the schemas cannot tell from 0, but
// which no file holds (value.CheckJSON); on a value of a JSON type that its
// schema does not take, as a string where a number is, as json.Unmarshal
// refuses it; on a key that its object's schema requires that is missing;
// on a value of a field of an enum, as a type, a
// kind, or an op, that the format does not have, as a consumer fails loudly on
// one (§6.4); on a published file's schemaVersion or a save state's
// stateVersion that is not the types', though not on a save state's
// schemaVersion, its snapshot's, which a runtime compares with the snapshot
// that it runs; on a key of an object's variant that is there, whatever its
// value, where its variant does not have it, as an else output's condition, a
// global variable's boardId, a draft's version, or a jumper's text, or that is
// missing or empty where its variant requires it, as an if output's condition
// or an enum's options (checkTags); on a key that is there without the key
// that it is there with, or that is not empty where that key is empty, which
// the types leave out, as a save state's snapshotId without its
// snapshotVersion, or with an empty one; on a value that is not of its
// declaration's type, as a variable's default that is no int where its type
// is int, or a value whose declaration is in another file that is neither a
// boolean, a number, nor a string; on an empty text or label, which a file
// leaves out; on an AST that expr.Decode refuses, as one that the AST's
// schema refuses, whose names are not resolved, or deeper than expr.MaxDepth
// or of more than expr.MaxNodes nodes; and on a segment of a text or a label
// that the segment's schema refuses, as one whose ref is not an ID in its
// canonical spelling, as richtext.Segment's UnmarshalJSON refuses it.
// Otherwise it reads data into v as json.Unmarshal reads it into a zero
// value, but for a key, which it reads as it is written (readTree): of a
// key that an object has twice, the last, where json.Unmarshal would merge
// what the two hold; a key that the types do not know it ignores (§6.4), as
// KIND in an object that they read as a struct, which differs from kind only
// by its case, and which json.Unmarshal would read as kind; and a map's
// keys, as a manifest's files' paths, it reads as they are written, whatever
// their case. A file that it reads may still be one that the schemas refuse:
// it does not check what else they hold a file to, as a pattern or a bound,
// which a validator of the schemas checks (plan items 78 and 84), but for an
// Int's, which is an integer of at most MaxInt in magnitude, a value's of
// its declaration's type, an int's within ±MaxInt and a float's within
// ±1.7976931348623157e+308 as it is written (value.FloatBeyondSchema), as
// value.Schema bounds them, and an AST's and a segment's, which expr.Decode
// and richtext read as their schemas take them.
func Decode(data []byte, v any) error {
	if rv := reflect.ValueOf(v); rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &json.InvalidUnmarshalError{Type: reflect.TypeOf(v)}
	}
	if err := decode(data, v); err != nil {
		return fmt.Errorf("%w: %w", ErrDecode, err)
	}
	return nil
}

// decode is Decode, its error without ErrDecode, of v, a pointer that is not
// nil. It reads data into a zero value of the type that v points to, which
// it checks (checkTags), so that it checks only what data holds, and sets
// what v points to to that value only once it has read and checked it.
func decode(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	tree, data, err := readTree(data, rv.Type())
	if err != nil {
		return err
	}
	read := reflect.New(rv.Type().Elem())
	if err := json.Unmarshal(data, read.Interface()); err != nil {
		return err
	}
	if err := checkTags(read, tree); err != nil {
		return err
	}
	rv.Elem().Set(read.Elem())
	return nil
}

// schemaKeys are what Decode reads of the schemas of Documents: the keys that
// they define of every object of every document, which encoding/json reads
// the package's types' fields by, and each of them folded (foldKey), by which
// Decode finds a key that differs from one of them only by its case
// (strip); and, of each variant of a union, the keys that another variant
// of its union defines and it does not, which its schema takes none of
// (schemagen.Generator.Union). They are read from the schemas, rather than
// from the types' fields, since a Provider defines keys of its own, as a
// text's, an AST's, and a segment's.
type schemaKeys struct {
	known, folded map[string]bool
	variants      map[reflect.Type]unionVariant
}

// unionVariant is a variant of a union: the key and the value that tell it,
// and the keys that its schema takes none of.
type unionVariant struct {
	key, value string
	excluded   []string
}

// knownKeys returns the schemaKeys of the schemas, which a generator reads
// once, without writing a document.
var knownKeys = sync.OnceValue(func() schemaKeys {
	g, roots := primed(false, Documents())
	sets := schemaKeys{known: map[string]bool{}, folded: map[string]bool{},
		variants: map[reflect.Type]unionVariant{}}
	g.Walk(func(s *schemagen.Schema) {
		for k := range s.Properties {
			sets.known[k] = true
			sets.folded[foldKey(k)] = true
		}
	}, roots...)
	for _, u := range unions {
		for _, v := range u.variants {
			uv := unionVariant{key: u.key, value: v.Value}
			for k, p := range g.Def(schemagen.RefName(g.Ref(v.Type).Ref)).Properties {
				if p.False {
					uv.excluded = append(uv.excluded, k)
				}
			}
			slices.Sort(uv.excluded)
			t := v.Type
			if t.Kind() == reflect.Pointer {
				t = t.Elem()
			}
			sets.variants[t] = uv
		}
	}
	return sets
})

// foldKey returns k with each character folded as encoding/json folds a key
// to match it with a field's name: to the least of the characters that
// Unicode's simple case folding takes as it, as K for k and for U+212A KELVIN
// SIGN, so that two keys fold alike exactly when strings.EqualFold takes them.
func foldKey(k string) string {
	var b strings.Builder
	for _, r := range k {
		for {
			f := unicode.SimpleFold(r)
			if f <= r {
				r = f
				break
			}
			r = f
		}
		b.WriteRune(r)
	}
	return b.String()
}

// readTree returns data, one JSON value of a value of the type t, as a tree
// of maps, slices, json.Numbers, strings, and booleans (value.ReadJSON),
// which checkTags reads what is there of, and the tree as JSON again, which
// Decode reads into the types, so that both read the same keys, each as a
// consumer reads it: of a key that an object has twice, the last, as the
// tree holds it, where encoding/json, reading data itself, would read both
// into one field or map, keeping what the first holds and the last does not,
// as a connection's condition; and without each key of an object that
// encoding/json reads as a struct of this package's types that is none of the
// known keys (knownKeys) but differs from one of them only by its case, as
// KIND from kind, which encoding/json would read as the field that the known
// key names, where a consumer ignores it, as a key that it does not know
// (§6.4) (strip). No two known keys differ only by their case
// (TestKnownKeys), so that every key that it keeps is read as a consumer
// reads it. It fails on data that value.CheckJSON refuses.
func readTree(data []byte, t reflect.Type) (any, []byte, error) {
	tree, err := value.ReadJSON(data)
	if err != nil {
		return nil, nil, err
	}
	strip(t, tree, knownKeys())
	data, err = schemagen.Marshal(tree)
	return tree, data, err
}

// strip removes from tree, the JSON of a value of the type t, each key of an
// object, at any depth, that encoding/json reads as a struct of this
// package's types, and that is none of the known keys but differs from one of
// them only by its case. It removes no key of a map, as a manifest's files'
// or a save state's variables', which encoding/json reads as it is written,
// and nothing of a value that a type of another package reads, as an AST or
// a segment, which reads a key as it is written too, nor of what a key that
// the types do not know holds, which encoding/json does not read.
func strip(t reflect.Type, tree any, keys schemaKeys) {
	switch t.Kind() {
	case reflect.Pointer:
		strip(t.Elem(), tree, keys)
	case reflect.Slice, reflect.Array:
		items, _ := tree.([]any)
		for _, c := range items {
			strip(t.Elem(), c, keys)
		}
	case reflect.Map:
		obj, _ := tree.(map[string]any)
		for _, c := range obj {
			strip(t.Elem(), c, keys)
		}
	case reflect.Interface:
		// A union's variant, which its key's value tells, as unmarshalVariant
		// reads it.
		obj, _ := tree.(map[string]any)
		for _, u := range unions {
			for _, v := range u.variants {
				if u.iface == t && obj[u.key] == v.Value {
					strip(v.Type, obj, keys)
				}
			}
		}
	case reflect.Struct:
		if t.PkgPath() != thisPackage {
			return
		}
		obj, _ := tree.(map[string]any)
		info := infoOf(t)
		for k, c := range obj {
			// A field that its type reads by a key of its own, as a
			// condition's AST by ast, is named by no json tag, and no key is
			// read as it here.
			if f, ok := info.byName[k]; ok && f.named {
				strip(f.typ, c, keys)
			} else if !keys.known[k] && keys.folded[foldKey(k)] {
				delete(obj, k)
			}
		}
	}
}

// thisPackage is the path of this package, whose types checkTags reads.
var thisPackage = reflect.TypeFor[Manifest]().PkgPath()

// tagError is an error of checkTags: what is wrong, and the keys and the
// indexes that lead to it from the root, $, the last first, which are added
// as the error returns, so that a path is built only for an error.
type tagError struct {
	problem string
	path    []string
}

func problemf(format string, args ...any) *tagError {
	return &tagError{problem: fmt.Sprintf(format, args...)}
}

// at returns e, the error of a value at step, as .kind or [2], from what
// holds it.
func (e *tagError) at(step string) *tagError {
	e.path = append(e.path, step)
	return e
}

func (e *tagError) Error() string {
	var b strings.Builder
	b.WriteString("$")
	for i := len(e.path) - 1; i >= 0; i-- {
		b.WriteString(e.path[i])
	}
	return b.String() + " " + e.problem
}

// checkTags returns an error for what the schemas refuse of v, a value of
// this package's types read from tree, the JSON that readTree returned of
// what it was read from, or of what holds v, at any depth, as Generator reads
// the fields' tags (schemagen.Generator): a field's value that its enum or
// const tag does not take; a key that is there where its variant does not
// have it, a when tag's or another variant's of its union, whatever its
// value; a key that is missing where its object's schema requires it, or
// that is empty where it is tagged required, and that the types leave out
// when empty (written), as an enum's options; a key that is there without
// the key that its with tag names, which its object's schema requires where
// it is (dependentRequired), or that is not empty where that key is
// (checkWith); a field tagged typeFrom whose value is not of
// the type that its property says (checkVariant); and a value whose
// declaration is in another file that is neither a boolean, a number, nor a
// string. It reads a value.Value, and no value of another package's types,
// as an AST's or a segment's, whose own decoding checks them.
func checkTags(v reflect.Value, tree any) *tagError {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return checkTags(v.Elem(), tree)
	case reflect.Slice, reflect.Array:
		if !composite(v.Type().Elem()) {
			return nil
		}
		items, _ := tree.([]any)
		for i := range v.Len() {
			var item any
			if i < len(items) {
				item = items[i]
			}
			if err := checkTags(v.Index(i), item); err != nil {
				return err.at(fmt.Sprintf("[%d]", i))
			}
		}
	case reflect.Map:
		if !composite(v.Type().Elem()) {
			return nil
		}
		// By its keys in their order, so that of a map that holds more than
		// one value that is wrong, the same one is reported each time.
		obj, _ := tree.(map[string]any)
		keys := v.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
		for _, k := range keys {
			if err := checkTags(v.MapIndex(k), obj[k.String()]); err != nil {
				return err.at(fmt.Sprintf("[%q]", k.String()))
			}
		}
	case reflect.Struct:
		switch t := v.Type(); {
		case t == valueType:
			if v.CanInterface() && v.Interface().(value.Value).Kind() == "" {
				return problemf("is neither a boolean, a number, nor a string, which a value is")
			}
		case t.PkgPath() == thisPackage:
			return checkStruct(v, tree)
		}
	}
	return nil
}

// composite reports whether a value of t may hold a value that checkTags
// reads: a struct of this package's types or a value.Value, or what may hold
// one, but not a struct of another package's, as a text's segments, which
// checkTags does not read.
func composite(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface:
		return true
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		return composite(t.Elem())
	case reflect.Struct:
		return t == valueType || t.PkgPath() == thisPackage
	}
	return false
}

// checkStruct is checkTags of v, a struct of this package's types, whose JSON
// is tree.
func checkStruct(v reflect.Value, tree any) *tagError {
	t := v.Type()
	info := infoOf(t)
	obj, _ := tree.(map[string]any)
	if u, ok := knownKeys().variants[t]; ok {
		for _, k := range u.excluded {
			if _, there := obj[k]; there {
				return problemf("is there, which a %s of %q has not", u.key, u.value).at("." + k)
			}
		}
	}
	// The keys that every variant requires and that are missing, and the
	// enums and the consts, first, which tell the variant whose fields the
	// rest are checked by, so that a key that is missing is reported as
	// missing, rather than as the zero value that it is read as.
	for i := range info.fields {
		f := &info.fields[i]
		_, there := obj[f.name]
		switch {
		case there:
			if err := f.checkValue(info.value(v, f)); err != nil {
				return err.at("." + f.name)
			}
		case f.when == "" && f.named && (!f.optional || f.required):
			return problemf("is missing, which its object's schema requires").at("." + f.name)
		}
	}
	for i := range info.fields {
		f := &info.fields[i]
		x, there := obj[f.name]
		fv := info.value(v, f)
		if err := info.checkVariant(v, f, fv, there); err != nil {
			return err.at("." + f.name)
		}
		if err := info.checkWith(v, f, fv, obj, there); err != nil {
			return err.at("." + f.name)
		}
		if err := checkTags(fv, x); err != nil {
			return err.at("." + f.name)
		}
	}
	return nil
}

// structInfo is a struct type's fields as encoding/json reads them, an
// embedded struct's as the struct's own, with what checkTags reads of their
// tags, which it reads once of each type, as Generator reads them
// (schemagen.Fields).
type structInfo struct {
	fields []fieldInfo
	byName map[string]*fieldInfo
}

// fieldInfo is a field of a struct, as encoding/json reads it, with its tags.
type fieldInfo struct {
	// index is its index, as reflect.Value.FieldByIndex takes it, and typ its
	// type.
	index []int
	typ   reflect.Type
	// name is the key that it is read by, and named whether its json tag
	// names it so, which a field that a type reads by a key of its own, as a
	// condition's, is not.
	name     string
	named    bool
	opts     string
	optional bool
	// required is whether it is tagged required.
	required bool
	enum     []string
	// konst is its const tag's value, as JSON reads it, and konstTag the
	// tag, if it has one.
	konst    any
	konstTag string
	// when is the property that its when tag names, and whenValues the
	// values that it is there for, if it has one.
	when       string
	whenValues []string
	typeFrom   string
	// with is the key of the field that its with tag names, which is there
	// exactly when it is, if it has one.
	with string
}

// structInfos are the structInfo of each struct type that checkTags read.
var structInfos sync.Map

// infoOf returns the structInfo of the struct type t.
func infoOf(t reflect.Type) *structInfo {
	if info, ok := structInfos.Load(t); ok {
		return info.(*structInfo)
	}
	info := &structInfo{byName: map[string]*fieldInfo{}}
	for _, f := range schemagen.Fields(t) {
		name, opts, _ := strings.Cut(f.Tags["json"], ",")
		fi := fieldInfo{index: f.Index, typ: f.Type, name: f.Name, named: name != "", opts: opts,
			optional: f.Optional, required: f.Tags["required"] == "true", typeFrom: f.Tags["typeFrom"],
			when: f.When, whenValues: f.WhenValues, with: f.Tags["with"]}
		if e, ok := f.Tags["enum"]; ok {
			fi.enum = strings.Split(e, ",")
		}
		if c, ok := f.Tags["const"]; ok {
			if err := json.Unmarshal([]byte(c), &fi.konst); err != nil {
				panic(fmt.Sprintf("published: the const tag of %s.%s is not JSON", t, f.Name))
			}
			fi.konstTag = c
		}
		info.fields = append(info.fields, fi)
	}
	for i := range info.fields {
		info.byName[info.fields[i].name] = &info.fields[i]
	}
	actual, _ := structInfos.LoadOrStore(t, info)
	return actual.(*structInfo)
}

// value returns the value of the field f of v, or its zero value where an
// embedded pointer that holds it is nil.
func (info *structInfo) value(v reflect.Value, f *fieldInfo) reflect.Value {
	fv, err := v.FieldByIndexErr(f.index)
	if err != nil {
		return reflect.Zero(f.typ)
	}
	return fv
}

// property returns the value of the string field of v that is read by the
// key name, a when or a typeFrom tag's property, or "" if v has none.
func (info *structInfo) property(v reflect.Value, name string) string {
	f, ok := info.byName[name]
	if !ok {
		return ""
	}
	return info.value(v, f).String()
}

// checkValue returns an error for fv, the value of the field f, that its
// enum tag does not take, or the items of a slice of strings that it does
// not, or that its const tag does not take.
func (f *fieldInfo) checkValue(fv reflect.Value) *tagError {
	if f.enum != nil {
		switch {
		case fv.Kind() == reflect.String:
			if !slices.Contains(f.enum, fv.String()) {
				return problemf("is %q, which is none of %s", fv.String(), strings.Join(f.enum, ","))
			}
		case fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() == reflect.String:
			for i := range fv.Len() {
				if s := fv.Index(i).String(); !slices.Contains(f.enum, s) {
					return problemf("is %q, which is none of %s", s, strings.Join(f.enum, ",")).
						at(fmt.Sprintf("[%d]", i))
				}
			}
		}
	}
	if f.konstTag != "" && !isConst(fv, f.konst) {
		return problemf("is %v, not %s", fv, f.konstTag)
	}
	return nil
}

// isConst reports whether fv is c, a value as JSON reads it: the same string,
// boolean, or number.
func isConst(fv reflect.Value, c any) bool {
	switch c := c.(type) {
	case string:
		return fv.Kind() == reflect.String && fv.String() == c
	case bool:
		return fv.Kind() == reflect.Bool && fv.Bool() == c
	case float64:
		switch {
		case fv.CanInt():
			return float64(fv.Int()) == c
		case fv.CanUint():
			return float64(fv.Uint()) == c
		case fv.CanFloat():
			return fv.Float() == c
		}
	}
	return false
}

// checkVariant returns an error for the field f of the struct v, whose value
// is fv, there where its key is in the struct's JSON: one that is there where
// its variant does not have it (a when tag), whatever its value; one that is
// not there where its object's schema requires it, its variant's where it has
// a when tag; one that is empty where it is tagged required and the types
// leave it out (written), as an enum's options; and a value of one tagged
// typeFrom that is not of the type that its property says, as value.Read
// reads it, a float's within the bounds of its schema (value.Schema), but
// for an enum's, which is a string, and which a consumer checks is one of
// its options.
func (info *structInfo) checkVariant(v reflect.Value, f *fieldInfo, fv reflect.Value, there bool) *tagError {
	has, is := true, ""
	if f.when != "" {
		is = info.property(v, f.when)
		has = slices.Contains(f.whenValues, is)
	}
	switch {
	case there && !has:
		return problemf("is there, which a %s of %q has not", f.when, is)
	case !has:
	case !there && f.named && (!f.optional || f.required):
		if f.when != "" {
			return problemf("is missing, which a %s of %q has", f.when, is)
		}
		return problemf("is missing, which its object's schema requires")
	case there && f.required && !written(fv, f.opts):
		return problemf("is empty, which the types leave out, but it is required where it is there")
	}
	if f.typeFrom == "" || !there || !fv.CanInterface() {
		return nil
	}
	val, ok := fv.Interface().(value.Value)
	if !ok {
		return nil
	}
	typ := info.property(v, f.typeFrom)
	if typ == value.Enum {
		typ = value.String
	}
	if _, problem := val.Read(typ, value.Checks{}); problem != "" {
		return problemf("%s", problem)
	}
	if typ == value.Float && value.FloatBeyondSchema(val.JSON()) {
		return problemf("is beyond ±1.7976931348623157e+308, the greatest double's shortest form, as it is written")
	}
	return nil
}

// checkWith returns an error for the field f of the struct v, whose value is
// fv, there where its key is in obj, the struct's JSON, that its with tag
// names another field of: one that is there where the other's key is not,
// which the object's schema requires where it is (dependentRequired), and
// one that is not empty where the other is, which the types leave out
// (written), so that the types would read the one without the other, as a
// save state's snapshotId with an empty snapshotVersion, which the schema's
// bounds refuse. Two fields that are there and both empty are read as
// neither.
func (info *structInfo) checkWith(v reflect.Value, f *fieldInfo, fv reflect.Value, obj map[string]any,
	there bool) *tagError {
	other, ok := info.byName[f.with]
	if f.with == "" || !there || !ok {
		return nil
	}
	if _, ok := obj[f.with]; !ok {
		return problemf("is there without %s, which is there exactly when it is", f.with)
	}
	if written(fv, f.opts) && !written(info.value(v, other), other.opts) {
		return problemf("is there, and %s is empty, which the types leave out, though it is there exactly when "+
			"this is", f.with)
	}
	return nil
}

// written reports whether encoding/json writes fv, the value of a field whose
// json tag's options are opts: not when it is tagged omitempty and fv is
// empty, false, 0, a nil pointer, or of no length, nor when it is tagged
// omitzero and fv is zero, as its IsZero method says, if it has one.
func written(fv reflect.Value, opts string) bool {
	for o := range strings.SplitSeq(opts, ",") {
		switch o {
		case "omitzero":
			zero := fv.IsZero()
			if fv.CanInterface() && !(fv.Kind() == reflect.Pointer && fv.IsNil()) {
				if z, ok := fv.Interface().(interface{ IsZero() bool }); ok {
					zero = z.IsZero()
				}
			}
			if zero {
				return false
			}
		case "omitempty":
			switch fv.Kind() {
			case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
				if fv.Len() == 0 {
					return false
				}
			case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint,
				reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32,
				reflect.Float64, reflect.Interface, reflect.Pointer:
				if fv.IsZero() {
					return false
				}
			}
		}
	}
	return true
}

// marshalObject writes v, a struct of a type without methods, as JSON, each
// of its slices and maps that its JSON always holds, one that is not
// optional (schemagen.Fields), as a branch's outputs, written as [] or {}
// when it is nil, rather than as null, which no file holds (§6.2). Each of
// the package's types that has such a field writes itself with it.
func marshalObject[T any](v T) ([]byte, error) {
	rv := reflect.ValueOf(&v).Elem()
	info := infoOf(rv.Type())
	for i := range info.fields {
		f := &info.fields[i]
		if f.optional {
			continue
		}
		fv, err := rv.FieldByIndexErr(f.index)
		if err != nil || !fv.CanSet() {
			// A field of an embedded pointer that is nil, which encoding/json
			// leaves out.
			continue
		}
		switch {
		case fv.Kind() == reflect.Slice && fv.IsNil():
			fv.Set(reflect.MakeSlice(fv.Type(), 0, 0))
		case fv.Kind() == reflect.Map && fv.IsNil():
			fv.Set(reflect.MakeMap(fv.Type()))
		}
	}
	return schemagen.Marshal(v)
}

// marshalFile writes v, a file's struct as a type F without methods, as
// marshalObject does, or fails where Decode would refuse what it writes, as
// it reads it into an F: rather than write null, which no file holds (§6.2),
// where a value of it is not set, as a variable's default, a node, or a
// manifest's snapshot; or what Decode refuses of its tags (checkTags), as an
// element's kind that is none of the enum's, another schemaVersion, an else
// output's condition, or a value that is not of its type; or an AST that
// expr.Decode does not read back as its context's, as one whose names are not
// resolved, as expr.Parse returns it. So the types never write a file that
// Decode refuses.
func marshalFile[F, T any](file string, v T) ([]byte, error) {
	data, err := marshalObject(v)
	if err != nil {
		return nil, err
	}
	if err := decode(data, new(F)); err != nil {
		return nil, fmt.Errorf("published: %s is not written, since Decode would refuse it: %w", file, err)
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
	head, err := schemagen.Marshal(map[string]string{key: val})
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
