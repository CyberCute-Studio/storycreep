package schemagen

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Generator reads Go types into the definitions of JSON Schema documents.
//
// A struct's field is read as encoding/json reads it: by the name that its
// json tag gives, or its own, never when the tag is "-", and an embedded
// struct's fields as the struct's own. A field tagged omitempty or omitzero
// is optional, and any other required. These tags constrain a field's value,
// and the Generator panics on any other, or on one that the field's type
// cannot have, so that a misspelt tag cannot pass unnoticed:
//
//   - doc: the field's description.
//   - enum: the values that a string may be, separated by commas.
//   - const: the one value that the field may be, as JSON.
//   - pattern, minLength, maxLength: of a string, its length counted in code
//     points.
//   - minimum, maximum: of a number.
//   - minItems, maxItems: of a slice. The string tags of a slice of strings
//     constrain its items.
//   - id: a string that is the ID of an entity of the kind that ids names so,
//     as node, in its canonical spelling (ID).
//   - keyId, keyPattern: of a map, its keys, as id and pattern do a string.
//   - typeFrom: the field's value is of the type that the string property of
//     the struct that the tag names says, as a variable's default is of its
//     type. The field's Go type is a TypedProvider, whose schema is all that
//     constrains it, so that it has no other tag that would, and the
//     struct's schema is one variant of an object for each value of the
//     property's enum, which is all that constrains the property.
//   - when: the field is there only when the property that the tag names is
//     one of the values after its =, separated by |, as in when:"type=enum":
//     the struct's schema is one variant for each value of the property's
//     enum, as for typeFrom, in which the field is required when it is
//     required, and which no other variant takes. The field is tagged
//     omitempty or omitzero, so that encoding/json leaves it out where it
//     is not.
//   - required: "true", of a field tagged omitempty or omitzero, makes it
//     required where it is there, as a when field.
//   - with: the field is there exactly when the field whose key the tag
//     names is, which is tagged with this one's: both are tagged omitempty
//     or omitzero, and the object's dependentRequired requires each where
//     the other is, as a save state's snapshotId and snapshotVersion.
//
// Fields returns a struct's fields as the Generator reads them, with their
// tags, so that what else reads the tags reads the same fields.
type Generator struct {
	closed bool
	// defs are the definitions, by their names; one that is being built is
	// there as nil.
	defs map[string]*Schema
	// owners are what defines each definition: its Go type, or the name
	// itself for one that Define defines.
	owners map[string]any
	types  map[reflect.Type]string
	unions map[reflect.Type]union
	// variants are the struct types that are a union's variants, with the
	// key and the value that tell them.
	variants map[reflect.Type][2]string
}

// union is an interface type's variants, which the value of key tells apart,
// and its description.
type union struct {
	key, doc string
	variants []Variant
}

// Variant is a variant of a union: the value of the union's key that tells
// it, and its Go type, a struct or a pointer to one.
type Variant struct {
	Value string
	Type  reflect.Type
}

// Provider is a Go type that gives its own schema, which the generator asks
// of its zero value, or of a pointer to a new one when only the pointer has
// the method. It defines the definitions that it refers to with Define.
type Provider interface {
	JSONSchema(g *Generator) *Schema
}

// TypedProvider is a Go type whose schema depends on a type that a property
// beside it says, as a typed value's (tag typeFrom).
type TypedProvider interface {
	JSONSchemaOf(g *Generator, typ string) *Schema
}

// Documented is a Go type that describes itself, which its definition's
// description says.
type Documented interface {
	JSONSchemaDoc() string
}

var (
	providerType      = reflect.TypeFor[Provider]()
	typedProviderType = reflect.TypeFor[TypedProvider]()
	documentedType    = reflect.TypeFor[Documented]()
)

// New returns a Generator. A closed one makes every object take no property
// that it does not define, rather than any but null (AnyValue), for tests
// that check that an example holds nothing that its schema does not define.
func New(closed bool) *Generator {
	return &Generator{
		closed:   closed,
		defs:     map[string]*Schema{},
		owners:   map[string]any{},
		types:    map[reflect.Type]string{},
		unions:   map[reflect.Type]union{},
		variants: map[reflect.Type][2]string{},
	}
}

// Union declares the interface type iface a union of the variants, which the
// value of the property key tells apart, as doc describes it: the definition
// of iface is one of the variants', each of which has key, of its value, and
// no property that another variant has and it does not. It panics when iface
// is not an interface, a variant is no struct or does not implement iface,
// or a value or a variant is there twice.
func (g *Generator) Union(iface reflect.Type, key, doc string, variants ...Variant) {
	if iface.Kind() != reflect.Interface {
		panic(fmt.Sprintf("schemagen: %s is not an interface", iface))
	}
	if _, ok := g.unions[iface]; ok {
		panic(fmt.Sprintf("schemagen: the union %s is declared twice", iface))
	}
	values := map[string]bool{}
	for _, v := range variants {
		t := deref(v.Type)
		if t.Kind() != reflect.Struct || !v.Type.Implements(iface) || values[v.Value] {
			panic(fmt.Sprintf("schemagen: %s is no variant of %s, or %q is there twice", v.Type, iface, v.Value))
		}
		if _, ok := g.variants[t]; ok {
			panic(fmt.Sprintf("schemagen: %s is a variant of two unions", t))
		}
		values[v.Value] = true
		g.variants[t] = [2]string{key, v.Value}
	}
	g.unions[iface] = union{key: key, doc: doc, variants: variants}
}

// Ref returns a reference to the definition of the named Go type t, or of
// the type that t points to, which it defines unless it is defined: the
// schema of a Provider, a union, or a struct, named after the type. It panics
// when another definition has the name.
func (g *Generator) Ref(t reflect.Type) *Schema {
	t = deref(t)
	if name, ok := g.types[t]; ok {
		return Ref(name)
	}
	name := t.Name()
	if name == "" {
		panic(fmt.Sprintf("schemagen: the type %s has no name to define it by", t))
	}
	if owner, ok := g.owners[name]; ok {
		panic(fmt.Sprintf("schemagen: the definition %s is of %v, and %s has its name", name, owner, t))
	}
	g.types[t] = name
	g.owners[name] = t
	g.defs[name] = nil
	s := g.build(t)
	if s.Description == "" {
		if d, ok := instance(t, documentedType); ok {
			s.Description = d.(Documented).JSONSchemaDoc()
		}
	}
	g.defs[name] = s
	return Ref(name)
}

// Define defines name as the schema that build returns, unless a Define
// defined it already, and returns a reference to it. build may refer to the
// definition itself. It panics when a Go type's definition has the name.
func (g *Generator) Define(name string, build func() *Schema) *Schema {
	if owner, ok := g.owners[name]; ok {
		if owner != any(name) {
			panic(fmt.Sprintf("schemagen: the definition %s is of %v, and Define defines it too", name, owner))
		}
		return Ref(name)
	}
	g.owners[name] = name
	g.defs[name] = nil
	g.defs[name] = build()
	return Ref(name)
}

// Def returns the definition name, or nil when it is not defined or is being
// built.
func (g *Generator) Def(name string) *Schema {
	return g.defs[name]
}

// Object returns the schema of an object of the properties, of which the
// required ones must be there, and which takes any other property but null
// (AnyValue), or none when the generator is closed.
func (g *Generator) Object(properties map[string]*Schema, required ...string) *Schema {
	s := &Schema{Type: "object", Properties: properties, Required: slices.Sorted(slices.Values(required))}
	if g.closed {
		s.AdditionalProperties = False()
	} else {
		s.AdditionalProperties = g.AnyValue()
	}
	return s
}

// Map returns the schema of an object of any keys, each of which keys takes,
// unless it is nil, whose values values takes.
func Map(values, keys *Schema) *Schema {
	return &Schema{Type: "object", AdditionalProperties: values, PropertyNames: keys}
}

// AnyValue returns a reference to the definition of any JSON value but null,
// at any depth, which an object takes of a property that it does not define.
func (g *Generator) AnyValue() *Schema {
	return g.Define("AnyValue", func() *Schema {
		self := Ref("AnyValue")
		return &Schema{
			Description: "Any JSON value but null, at any depth: what an object may hold of a property that this " +
				"version of the format does not define, which a consumer ignores (PRD §6.4).",
			OneOf: []*Schema{
				{Type: "boolean"}, {Type: "number"}, {Type: "string"},
				{Type: "array", Items: self},
				Map(self, nil),
			},
		}
	})
}

// exclusive returns the schema of a value that one of a union's variants
// takes, references to definitions of objects, which a const property tells
// apart, each made to take no property that another variant defines and it
// does not (False): a runtime reads a node's or a snapshot's fields by their
// names, as a draft's version, so that a key that one variant defines is not
// one that another ignores. A variant can therefore gain a property that
// another has only with a new version of the format.
func (g *Generator) exclusive(members ...*Schema) *Schema {
	objects := make([]*Schema, len(members))
	all := map[string]bool{}
	for i, m := range members {
		o := g.defs[RefName(m.Ref)]
		if o == nil || o.Type != "object" {
			panic("schemagen: a variant of a union is no object that is defined: " + m.Ref)
		}
		objects[i] = o
		for name, p := range o.Properties {
			if !p.False {
				all[name] = true
			}
		}
	}
	for _, o := range objects {
		for _, name := range slices.Sorted(maps.Keys(all)) {
			if _, ok := o.Properties[name]; !ok {
				o.Properties[name] = False()
			}
		}
	}
	return &Schema{OneOf: members}
}

// build returns the schema of the named Go type t.
func (g *Generator) build(t reflect.Type) *Schema {
	if p, ok := instance(t, providerType); ok {
		return p.(Provider).JSONSchema(g)
	}
	switch t.Kind() {
	case reflect.Interface:
		u, ok := g.unions[t]
		if !ok {
			panic(fmt.Sprintf("schemagen: the interface %s is no union", t))
		}
		members := make([]*Schema, len(u.variants))
		for i, v := range u.variants {
			members[i] = g.Ref(v.Type)
		}
		return Describe(g.exclusive(members...), u.doc)
	case reflect.Struct:
		return g.object(t)
	}
	panic(fmt.Sprintf("schemagen: the type %s is neither a struct, a union, nor a Provider", t))
}

// instance returns t's zero value as an interface of the type i, or a pointer
// to a new one when only the pointer implements i.
func instance(t reflect.Type, i reflect.Type) (any, bool) {
	switch {
	case t.Kind() == reflect.Interface:
		// An interface's zero value is nil, which has no method to call.
		return nil, false
	case t.Implements(i):
		return reflect.Zero(t).Interface(), true
	case reflect.PointerTo(t).Implements(i):
		return reflect.New(t).Interface(), true
	}
	return nil, false
}

func deref(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Pointer {
		return t.Elem()
	}
	return t
}

// field is a struct's field as JSON holds it.
type field struct {
	name     string
	typ      reflect.Type
	tags     map[string]string
	optional bool
	// index is its index, as reflect.Value.FieldByIndex takes it.
	index []int
}

// Field is a struct's field as encoding/json reads it, with its tags, as the
// Generator reads them (Fields).
type Field struct {
	// Name is the key that it is read by: its json tag's name, or its own.
	Name string
	// Index is its index, as reflect.Value.FieldByIndex takes it, an
	// embedded struct's field's through the struct.
	Index []int
	Type  reflect.Type
	// Tags are its tags, json among them, by their keys.
	Tags map[string]string
	// Optional is whether it is tagged omitempty or omitzero.
	Optional bool
	// When is the property that its when tag names, and WhenValues the
	// values that it is there for, if it has one.
	When       string
	WhenValues []string
}

// Fields returns the fields of the struct t as encoding/json reads them, an
// embedded struct's as t's own, with their tags, as the Generator reads them:
// it panics where the Generator does, on a tag that it does not read or
// cannot read, as a when tag that is not property=value|value, or on two
// fields of one name.
func Fields(t reflect.Type) []Field {
	fs := fields(t)
	out := make([]Field, len(fs))
	for i, f := range fs {
		out[i] = Field{Name: f.name, Index: f.index, Type: f.typ, Tags: f.tags, Optional: f.optional}
		if c := parseWhen(t, f); c != nil {
			out[i].When, out[i].WhenValues = c.property, c.values
		}
	}
	return out
}

// fields returns the fields of the struct t as encoding/json reads them.
func fields(t reflect.Type) []field {
	out := fieldsAt(t, nil)
	seen := map[string]bool{}
	for _, f := range out {
		if seen[f.name] {
			panic(fmt.Sprintf("schemagen: %s has two fields named %s", t, f.name))
		}
		seen[f.name] = true
	}
	return out
}

// fieldsAt returns the fields of the struct t, whose index in the struct
// that embeds it is index, or nil for that struct itself.
func fieldsAt(t reflect.Type, index []int) []field {
	var out []field
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		at := append(slices.Clone(index), i)
		if f.Anonymous && name == "" && deref(f.Type).Kind() == reflect.Struct {
			out = append(out, fieldsAt(deref(f.Type), at)...)
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		optional := false
		for o := range strings.SplitSeq(opts, ",") {
			optional = optional || o == "omitempty" || o == "omitzero"
		}
		out = append(out, field{name: name, typ: f.Type, tags: parseTags(t, f), optional: optional, index: at})
	}
	return out
}

// tagKeys are the tags that a field may have.
var tagKeys = []string{
	"json", "doc", "enum", "const", "pattern", "minLength", "maxLength", "minimum", "maximum", "minItems",
	"maxItems", "id", "keyId", "keyPattern", "typeFrom", "when", "required", "with",
}

// parseTags returns the tags of the field f of the struct t, as
// reflect.StructTag.Lookup reads them, and panics on one that is not one of
// tagKeys, or that is there twice.
func parseTags(t reflect.Type, f reflect.StructField) map[string]string {
	tags := map[string]string{}
	rest := string(f.Tag)
	for {
		rest = strings.TrimLeft(rest, " ")
		if rest == "" {
			return tags
		}
		key, after, ok := strings.Cut(rest, ":")
		if !ok || key == "" || strings.ContainsAny(key, " \"") || !strings.HasPrefix(after, `"`) {
			panic(fmt.Sprintf("schemagen: the tags of %s.%s cannot be read", t, f.Name))
		}
		end := 1
		for end < len(after) && after[end] != '"' {
			if after[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(after) {
			panic(fmt.Sprintf("schemagen: the tags of %s.%s cannot be read", t, f.Name))
		}
		value, err := strconv.Unquote(after[:end+1])
		if err != nil {
			panic(fmt.Sprintf("schemagen: the tag %s of %s.%s cannot be read", key, t, f.Name))
		}
		if !slices.Contains(tagKeys, key) {
			panic(fmt.Sprintf("schemagen: %s.%s has the tag %s, which schemagen does not read", t, f.Name, key))
		}
		if _, dup := tags[key]; dup {
			panic(fmt.Sprintf("schemagen: %s.%s has the tag %s twice", t, f.Name, key))
		}
		tags[key] = value
		rest = after[end+1:]
	}
}

// condition is a when tag's: the property and the values that it may be.
type condition struct {
	property string
	values   []string
}

func parseWhen(t reflect.Type, f field) *condition {
	w, ok := f.tags["when"]
	if !ok {
		return nil
	}
	property, values, ok := strings.Cut(w, "=")
	if !ok || property == "" || values == "" {
		panic(fmt.Sprintf("schemagen: the when tag of %s.%s is not property=value|value", t, f.name))
	}
	return &condition{property: property, values: strings.Split(values, "|")}
}

// object returns the schema of the struct t: an object of its fields, or, if
// a field is tagged typeFrom or when, one variant of the object for each
// combination of the values of the properties that those tags name.
func (g *Generator) object(t reflect.Type) *Schema {
	fs := fields(t)
	byName := map[string]field{}
	for _, f := range fs {
		byName[f.name] = f
	}
	// The properties that tell the variants apart, each with its values.
	var tellers []string
	values := map[string][]string{}
	addTeller := func(name string, of field) {
		if _, ok := values[name]; ok {
			return
		}
		p, ok := byName[name]
		enum := p.tags["enum"]
		if !ok || deref(p.typ).Kind() != reflect.String || enum == "" || p.optional {
			panic(fmt.Sprintf("schemagen: %s.%s depends on %s, which is no required string field with an enum",
				t, of.name, name))
		}
		tellers = append(tellers, name)
		values[name] = strings.Split(enum, ",")
	}
	for _, f := range fs {
		if from, ok := f.tags["typeFrom"]; ok {
			addTeller(from, f)
		}
		if c := parseWhen(t, f); c != nil {
			if !f.optional {
				// encoding/json would write it in every variant, of which the
				// others take no such property.
				panic(fmt.Sprintf("schemagen: %s.%s is there only when %s is %s, but it is not tagged "+
					"omitempty or omitzero", t, f.name, c.property, strings.Join(c.values, "|")))
			}
			addTeller(c.property, f)
			for _, v := range c.values {
				if !slices.Contains(values[c.property], v) {
					panic(fmt.Sprintf("schemagen: %s.%s is there when %s is %s, which it cannot be",
						t, f.name, c.property, v))
				}
			}
		}
	}
	if len(tellers) == 0 {
		return g.objectOf(t, fs, nil)
	}
	slices.Sort(tellers)
	var variants []*Schema
	var combine func(i int, combo map[string]string)
	combine = func(i int, combo map[string]string) {
		if i == len(tellers) {
			variants = append(variants, g.objectOf(t, fs, maps.Clone(combo)))
			return
		}
		for _, v := range values[tellers[i]] {
			combo[tellers[i]] = v
			combine(i+1, combo)
		}
	}
	combine(0, map[string]string{})
	return &Schema{OneOf: variants}
}

// objectOf returns the schema of the struct t's fields, in the variant in
// which each property of combo has its value, or of all of them when combo is
// nil. A union's variant has the union's key too.
func (g *Generator) objectOf(t reflect.Type, fs []field, combo map[string]string) *Schema {
	props := map[string]*Schema{}
	var required []string
	if kv, ok := g.variants[t]; ok {
		props[kv[0]] = &Schema{Const: kv[1]}
		required = append(required, kv[0])
	}
	for _, f := range fs {
		if _, ok := props[f.name]; ok {
			panic(fmt.Sprintf("schemagen: %s has a field named %s, which is its union's key", t, f.name))
		}
		if c := parseWhen(t, f); c != nil && !slices.Contains(c.values, combo[c.property]) {
			props[f.name] = False()
			continue
		}
		var s *Schema
		switch from, typed := f.tags["typeFrom"]; {
		case combo != nil && combo[f.name] != "":
			// The enum's value of the variant is all that the field's tags
			// say of it.
			allowTags(t, f, []string{"enum"})
			s = &Schema{Const: combo[f.name]}
		case typed:
			// The TypedProvider's schema is all that the field's tags say of
			// its value.
			allowTags(t, f, []string{"typeFrom"})
			p, ok := instance(deref(f.typ), typedProviderType)
			if !ok {
				panic(fmt.Sprintf("schemagen: %s.%s is tagged typeFrom, but %s is no TypedProvider", t, f.name, f.typ))
			}
			s = p.(TypedProvider).JSONSchemaOf(g, combo[from])
		default:
			s = g.value(t, f)
		}
		props[f.name] = Describe(s, f.tags["doc"])
		r, tagged := f.tags["required"]
		if tagged && (r != "true" || !f.optional) {
			panic(fmt.Sprintf("schemagen: the required tag of %s.%s is not true, or the field is required "+
				"without it", t, f.name))
		}
		if tagged || !f.optional {
			required = append(required, f.name)
		}
	}
	s := g.Object(props, required...)
	s.DependentRequired = dependentRequired(t, fs, props)
	return s
}

// dependentRequired returns what the with tags of the struct t's fields fs
// require, of the properties props of one of its variants: each field's
// key's, the key of the field that it is there with, or nil for none. It
// panics on a with tag that names no field of t, or one that is not tagged
// with the field's key, or that either field is not optional.
func dependentRequired(t reflect.Type, fs []field, props map[string]*Schema) map[string][]string {
	byName := map[string]field{}
	for _, f := range fs {
		byName[f.name] = f
	}
	var deps map[string][]string
	for _, f := range fs {
		with, ok := f.tags["with"]
		if !ok {
			continue
		}
		other, ok := byName[with]
		if !ok || other.tags["with"] != f.name || !f.optional || !other.optional {
			panic(fmt.Sprintf("schemagen: %s.%s is there with %s, which is no field of the struct that is "+
				"there with it, or one of them is not tagged omitempty or omitzero", t, f.name, with))
		}
		if props[f.name].False || props[with].False {
			continue
		}
		if deps == nil {
			deps = map[string][]string{}
		}
		deps[f.name] = []string{with}
	}
	return deps
}

// The tags that constrain a string, a number, an array, and a map.
var (
	stringTags = []string{"enum", "const", "pattern", "minLength", "maxLength", "id"}
	numberTags = []string{"const", "minimum", "maximum"}
	arrayTags  = []string{"minItems", "maxItems"}
	mapTags    = []string{"keyId", "keyPattern"}
)

// allowTags panics on a tag of the field f of the struct t that constrains
// it, other than json, doc, required, when, and with, that none of sets
// holds, as one that its type cannot have.
func allowTags(t reflect.Type, f field, sets ...[]string) {
	for k := range f.tags {
		if k == "json" || k == "doc" || k == "required" || k == "when" || k == "with" {
			continue
		}
		if !slices.ContainsFunc(sets, func(set []string) bool { return slices.Contains(set, k) }) {
			panic(fmt.Sprintf("schemagen: %s.%s, of %s, has the tag %s, which its type cannot have", t, f.name,
				f.typ, k))
		}
	}
}

// value returns the schema of the field f of the struct t.
func (g *Generator) value(t reflect.Type, f field) *Schema {
	allowed := func(sets ...[]string) { allowTags(t, f, sets...) }
	ft := deref(f.typ)
	if g.named(ft) {
		allowed()
		return g.Ref(ft)
	}
	switch ft.Kind() {
	case reflect.Slice, reflect.Array:
		allowed(stringTags, arrayTags)
		item := f
		item.typ = ft.Elem()
		item.tags = map[string]string{}
		for _, k := range stringTags {
			if v, ok := f.tags[k]; ok {
				item.tags[k] = v
			}
		}
		s := &Schema{Type: "array", Items: g.value(t, item)}
		s.MinItems = intTag(t, f, "minItems")
		s.MaxItems = intTag(t, f, "maxItems")
		return s
	case reflect.Map:
		allowed(mapTags)
		if ft.Key().Kind() != reflect.String {
			panic(fmt.Sprintf("schemagen: %s.%s is a map whose keys are not strings", t, f.name))
		}
		var keys *Schema
		if k, ok := f.tags["keyId"]; ok {
			keys = g.ID(kindNamed(t, f, k))
		} else if p, ok := f.tags["keyPattern"]; ok {
			keys = &Schema{Pattern: p}
		}
		return Map(g.value(t, field{name: f.name, typ: ft.Elem()}), keys)
	}
	s := &Schema{}
	switch ft.Kind() {
	case reflect.String:
		allowed(stringTags)
		if k, ok := f.tags["id"]; ok {
			if len(f.tags) > 1+countOf(f.tags, "json", "doc", "required", "when", "with") {
				panic(fmt.Sprintf("schemagen: %s.%s is an ID, and has other tags that constrain it", t, f.name))
			}
			return g.ID(kindNamed(t, f, k))
		}
		s.Type = "string"
		if e, ok := f.tags["enum"]; ok {
			for v := range strings.SplitSeq(e, ",") {
				s.Enum = append(s.Enum, v)
			}
		}
		s.Pattern = f.tags["pattern"]
		s.MinLength = intTag(t, f, "minLength")
		s.MaxLength = intTag(t, f, "maxLength")
	case reflect.Bool:
		allowed([]string{"const"})
		s.Type = "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8,
		reflect.Uint16, reflect.Uint32, reflect.Uint64:
		allowed(numberTags)
		s.Type = "integer"
		s.Minimum, s.Maximum = floatTag(t, f, "minimum"), floatTag(t, f, "maximum")
	case reflect.Float32, reflect.Float64:
		allowed(numberTags)
		s.Type = "number"
		s.Minimum, s.Maximum = floatTag(t, f, "minimum"), floatTag(t, f, "maximum")
	default:
		panic(fmt.Sprintf("schemagen: %s.%s is of %s, which schemagen does not read", t, f.name, f.typ))
	}
	if c, ok := f.tags["const"]; ok {
		if err := json.Unmarshal([]byte(c), &s.Const); err != nil {
			panic(fmt.Sprintf("schemagen: the const tag of %s.%s is not JSON", t, f.name))
		}
	}
	return s
}

// named reports whether t has a definition of its own: a Provider, a union,
// or a struct.
func (g *Generator) named(t reflect.Type) bool {
	if _, ok := instance(t, providerType); ok {
		return true
	}
	if _, ok := g.unions[t]; ok {
		return true
	}
	return t.Kind() == reflect.Struct
}

func countOf(tags map[string]string, keys ...string) int {
	n := 0
	for _, k := range keys {
		if _, ok := tags[k]; ok {
			n++
		}
	}
	return n
}

func intTag(t reflect.Type, f field, key string) *int {
	v, ok := f.tags[key]
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		panic(fmt.Sprintf("schemagen: the %s tag of %s.%s is not a count", key, t, f.name))
	}
	return &n
}

func floatTag(t reflect.Type, f field, key string) *float64 {
	v, ok := f.tags[key]
	if !ok {
		return nil
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		panic(fmt.Sprintf("schemagen: the %s tag of %s.%s is not a number", key, t, f.name))
	}
	return &n
}

// kindNamed returns the kind of entity that ids names name.
func kindNamed(t reflect.Type, f field, name string) ids.Kind {
	for _, k := range ids.Kinds() {
		if k.String() == name {
			return k
		}
	}
	panic(fmt.Sprintf("schemagen: %s.%s names %s, which is no kind of entity", t, f.name, name))
}

// kindNames are the kinds of entity whose names are not their kind's with
// spaces for underscores.
var kindNames = map[ids.Kind]string{
	ids.AttributeDef: "attribute definition",
	ids.APIToken:     "API token",
}

// ID returns a reference to the definition of an ID of an entity of the kind
// k, in its canonical spelling: the lowercase prefix, an underscore, and the
// uppercase body (ids.Encode). A published file holds IDs as the server
// writes them, so that a consumer compares them as strings.
func (g *Generator) ID(k ids.Kind) *Schema {
	words := strings.Split(k.String(), "_")
	var name strings.Builder
	for _, w := range words {
		name.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	name.WriteString("Id")
	return g.Define(name.String(), func() *Schema {
		noun, ok := kindNames[k]
		if !ok {
			noun = strings.Join(words, " ")
		}
		article := "A"
		if strings.ContainsRune("aeioAEIO", rune(noun[0])) {
			article = "An"
		}
		return &Schema{
			Type:        "string",
			Pattern:     k.CanonicalPattern(),
			Description: fmt.Sprintf("%s %s's ID, in its canonical spelling (PRD §5.2).", article, noun),
		}
	})
}
