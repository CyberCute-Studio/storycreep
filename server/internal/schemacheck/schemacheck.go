// Package schemacheck makes a compiled JSON Schema safe to validate an untrusted
// document with. The validator builds an error of every value that fails, each
// holding its value's path, and of every branch of a oneOf that it fails, so
// that a file of a few MiB of bad values can take gigabytes. FailFast rewrites
// a compiled schema so that an object or an array stops at its first member
// that fails, which leaves the errors of one path through the document, and
// takes the same documents. Depth bounds that path.
package schemacheck

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/CyberCute-Studio/storycreep/server/internal/jsonscan"
)

// MaxDepth is how deeply the values of a document may nest. The deepest that the
// formats hold is an AST of expr.MaxDepth, 64, of at most two levels each, or a
// stored value that a request body of the API's 256 levels carried, inside the
// few levels of its file.
const MaxDepth = 512

// CheckDepth refuses a document, which json.Valid has accepted, that nests past
// MaxDepth, as each error holds the path of its value.
func CheckDepth(data []byte) error {
	if depth := jsonscan.Depth(data); depth > MaxDepth {
		return fmt.Errorf("its values nest %d deep, more than the %d that they may", depth, MaxDepth)
	}
	return nil
}

// CheckKeys refuses a document, which json.Valid has accepted and CheckDepth
// has held, that has a key twice in one object, however it spells it. tree is
// what a parser built of data, which holds the last of the two, where a decoder
// into a struct, a map, or a slice also keeps what the first has and the last
// lacks, so that what is validated would not be what is then decoded.
func CheckKeys(data []byte, tree any) error {
	if jsonscan.Keys(data) != keysOf(tree) {
		return errors.New("an object holds a key twice")
	}
	return nil
}

// keysOf counts the members of the objects of the tree.
func keysOf(v any) int {
	n := 0
	switch x := v.(type) {
	case map[string]any:
		n = len(x)
		for _, e := range x {
			n += keysOf(e)
		}
	case []any:
		for _, e := range x {
			n += keysOf(e)
		}
	}
	return n
}

// FailFast rewrites the schemas of roots, and every schema that they reach, to
// stop at the first failing member of an object or an array. A reference to the
// definition named anyValue, if it is not "", is dropped: that definition takes
// any JSON value but null at any depth, so a caller that refuses every null
// first loses nothing, and no branch of a oneOf walks what it does not declare,
// at each level of a nested value. It must be called before the schemas are in
// use.
func FailFast(anyValue string, roots ...*jsonschema.Schema) {
	r := rewriter{anyValue: anyValue, seen: map[*jsonschema.Schema]bool{}}
	for _, s := range roots {
		r.walk(s)
	}
}

type rewriter struct {
	anyValue string
	seen     map[*jsonschema.Schema]bool
}

// walk rewrites s and what it reaches, once each, as schemas can be recursive.
func (r *rewriter) walk(s *jsonschema.Schema) {
	if s == nil || r.seen[s] {
		return
	}
	r.seen[s] = true
	if r.anyValue != "" && s.Ref != nil && strings.HasSuffix(s.Ref.Location, "/$defs/"+r.anyValue) {
		s.Ref = nil
	}
	children := slices.Concat(s.AllOf, s.AnyOf, s.OneOf, s.PrefixItems,
		[]*jsonschema.Schema{s.Ref, s.RecursiveRef, s.Not, s.If, s.Then, s.Else, s.PropertyNames, s.Contains,
			s.Items2020, s.UnevaluatedItems, s.UnevaluatedProperties, s.ContentSchema},
		slices.Collect(maps.Values(s.Properties)), slices.Collect(maps.Values(s.PatternProperties)),
		slices.Collect(maps.Values(s.DependentSchemas)))
	if s.DynamicRef != nil {
		children = append(children, s.DynamicRef.Ref)
	}
	if a, ok := s.AdditionalProperties.(*jsonschema.Schema); ok {
		children = append(children, a)
	}
	for _, dep := range s.Dependencies {
		if d, ok := dep.(*jsonschema.Schema); ok {
			children = append(children, d)
		}
	}
	for _, c := range children {
		r.walk(c)
	}
	// What the two keywords below evaluate, the others read, so that a schema
	// that has one of those keeps them as they are.
	if s.UnevaluatedItems == nil && s.Items2020 != nil {
		s.Extensions = append(s.Extensions, &elements{item: s.Items2020, from: len(s.PrefixItems)})
		s.Items2020 = nil
	}
	if s.UnevaluatedProperties == nil && len(s.PatternProperties) == 0 &&
		(len(s.Properties) > 0 || s.AdditionalProperties != nil) {
		s.Extensions = append(s.Extensions, &members{properties: s.Properties,
			names: slices.Sorted(maps.Keys(s.Properties)), additional: s.AdditionalProperties})
		s.Properties, s.AdditionalProperties = nil, nil
	}
}

// elements is the keyword items, which stops at the first element that fails,
// of those from the index from, as prefixItems holds those before it.
type elements struct {
	item *jsonschema.Schema
	from int
}

// Validate implements jsonschema.SchemaExt.
func (e *elements) Validate(ctx *jsonschema.ValidatorContext, v any) {
	array, ok := v.([]any)
	if !ok {
		return
	}
	for i, item := range array {
		if i < e.from {
			continue
		}
		if err := ctx.Validate(e.item, item, []string{strconv.Itoa(i)}); err != nil {
			ctx.AddErr(err)
			return
		}
		ctx.EvaluatedItem(i)
	}
}

// members is the keywords properties and additionalProperties, which stop at
// the first member that fails, in the order of their keys, so that a document
// always fails at the same place.
type members struct {
	properties map[string]*jsonschema.Schema
	names      []string
	additional any
}

// Validate implements jsonschema.SchemaExt.
func (m *members) Validate(ctx *jsonschema.ValidatorContext, v any) {
	object, ok := v.(map[string]any)
	if !ok {
		return
	}
	for _, name := range m.names {
		if value, ok := object[name]; ok {
			if err := ctx.Validate(m.properties[name], value, []string{name}); err != nil {
				ctx.AddErr(err)
				return
			}
			ctx.EvaluatedProp(name)
		}
	}
	if m.additional == nil {
		return
	}
	var others []string
	for name := range object {
		if _, declared := m.properties[name]; !declared {
			others = append(others, name)
		}
	}
	slices.Sort(others)
	for _, name := range others {
		switch additional := m.additional.(type) {
		case bool:
			if !additional {
				ctx.AddError(&kind.AdditionalProperties{Properties: []string{name}})
				return
			}
		case *jsonschema.Schema:
			if err := ctx.Validate(additional, object[name], []string{name}); err != nil {
				ctx.AddErr(err)
				return
			}
		}
		ctx.EvaluatedProp(name)
	}
}

// Failure says where a validation failed: the deepest of its failures, which of
// a oneOf is in the branch that its value was meant for, as the others fail at
// the value itself, on its discriminator or on what it lacks.
func Failure(err error) string {
	var root *jsonschema.ValidationError
	if !errors.As(err, &root) {
		return err.Error()
	}
	deepest := root
	var visit func(*jsonschema.ValidationError)
	visit = func(v *jsonschema.ValidationError) {
		if len(v.Causes) == 0 && (len(deepest.Causes) > 0 || len(v.InstanceLocation) > len(deepest.InstanceLocation)) {
			deepest = v
		}
		for _, c := range v.Causes {
			visit(c)
		}
	}
	visit(root)
	return deepest.Error()
}
