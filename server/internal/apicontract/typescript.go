package apicontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/tsgen"
)

// TypeScript returns the TypeScript module of the document's types, which
// the client in web/shared is built on (PRD FR-API-02): a type for each
// named schema; Operations, which says what each operation takes and
// answers; and routes, the method and path of each. header, a comment that
// says where the module comes from, starts it.
//
// An operation's input has the parts it takes: path, query, and headers,
// each an object of parameters, and body. Its responses map each status
// that is not an error's to the type of its body, or undefined for none;
// the client reports errors, which have the envelope, as exceptions. A body
// that is a file, in one or more media types that are all a binary string,
// as an image is, is a Blob, which the client sends as it is, and an
// operation that answers with one has blob set in routes, so that the
// client reads it as one.
//
// It fails, rather than write a type that is wrong, on what the client
// cannot express yet: a parameter that is not a string, number, or boolean,
// or that is in a cookie; a body that is neither application/json alone nor
// a file; an operation that answers with JSON and a file; a response
// to a range of statuses, such as 2XX; a schema whose name is not an
// identifier, or that has properties and a schema of other properties too;
// and a required property that is readOnly or writeOnly, which huma
// requires only in responses or only in requests, where the type of a
// named schema is the same in both.
func TypeScript(d *Document, header string) ([]byte, error) {
	g := &tsGen{d: d}
	g.b.WriteString(header)
	if d.Components != nil {
		for _, name := range slices.Sorted(maps.Keys(d.Components.Schemas)) {
			if err := g.declare(name, d.Components.Schemas[name]); err != nil {
				return nil, fmt.Errorf("schema %s: %w", name, err)
			}
		}
	}
	if err := g.operations(); err != nil {
		return nil, err
	}
	return []byte(g.b.String()), nil
}

type tsGen struct {
	d *Document
	b strings.Builder
}

// moduleNames are the names, beside TypeScript's own (tsgen.TypeName), that
// a schema's type may not have: the globals that the module uses, the
// module's own names, and the names that the client in web/shared/src/api
// exports beside the module's, which would hide them.
var moduleNames = []string{
	"Answer", "ApiError", "CallOptions", "Client", "ClientOptions", "createApiClient", "createClient",
	"OperationShape", "Operations", "Route", "routes",
}

// declare writes the type of a named schema: an interface for an object
// with properties, and a type alias for anything else.
func (g *tsGen) declare(name string, s *Schema) error {
	if !tsgen.TypeName(name, moduleNames...) {
		return errors.New("the name is not one that a TypeScript type may have")
	}
	t, err := g.typeOf(s, 0)
	if err != nil {
		return err
	}
	g.b.WriteString("\n")
	tsgen.Comment(&g.b, 0, s.Description, s.Deprecated)
	if slices.Equal(s.Type, Types{"object"}) && len(s.Properties) > 0 && !s.composed() && s.Enum == nil &&
		s.Const == nil {
		fmt.Fprintf(&g.b, "export interface %s %s\n", name, t)
	} else {
		fmt.Fprintf(&g.b, "export type %s = %s;\n", name, t)
	}
	return nil
}

func (s *Schema) composed() bool {
	return len(s.OneOf)+len(s.AnyOf)+len(s.AllOf) > 0
}

// typeOf returns the type of the values of s, whose lines after the first
// are indented by n.
func (g *tsGen) typeOf(s *Schema, n int) (string, error) {
	switch {
	case s == nil:
		return "unknown", nil
	case s.Ref != "":
		return refName(s.Ref), nil
	case s.composed():
		return g.composition(s, n)
	}
	var parts []string
	add := func(t string) {
		if !slices.Contains(parts, t) {
			parts = append(parts, t)
		}
	}
	// addValue adds the literal type of a value of the enum or const. A
	// schema with a type takes null exactly when the type allows it, which
	// is added below, whether or not the enum lists null, because huma's
	// validator reads the type first.
	addValue := func(v json.RawMessage) error {
		lit, err := literal(v)
		if err == nil && (lit != "null" || len(s.Type) == 0) {
			add(lit)
		}
		return err
	}
	switch {
	case s.Const != nil:
		if err := addValue(s.Const); err != nil {
			return "", err
		}
	case s.Enum != nil:
		for _, v := range s.Enum {
			if err := addValue(v); err != nil {
				return "", err
			}
		}
	case len(s.Type) == 0:
		if s.Items != nil || s.Properties != nil || s.AdditionalProperties != nil {
			return "", errors.New("a schema with items or properties has no type")
		}
		return "unknown", nil
	default:
		for _, t := range s.Type {
			switch t {
			case "string", "boolean", "null":
				add(t)
			case "integer", "number":
				add("number")
			case "array":
				items, err := g.typeOf(s.Items, n)
				if err != nil {
					return "", err
				}
				add("Array<" + items + ">")
			case "object":
				obj, err := g.object(s, n)
				if err != nil {
					return "", err
				}
				add(obj)
			}
		}
	}
	// huma writes the enum or const of a nullable field, such as a *string,
	// without null, but its validator lets null through before it reads
	// them, and the server sends a nil pointer as null, so a type that
	// allows null gets it beside the values.
	if (s.Const != nil || s.Enum != nil) && slices.Contains(s.Type, "null") {
		add("null")
	}
	// null goes last, as people write it.
	if i := slices.Index(parts, "null"); i >= 0 && len(parts) > 1 {
		parts = append(slices.Delete(parts, i, i+1), "null")
	}
	if len(parts) == 0 {
		// An enum that allows no value, such as one that lists only a null
		// that its type does not allow.
		return "never", nil
	}
	return strings.Join(parts, " | "), nil
}

// composition returns the type of a schema made of others with oneOf,
// anyOf, or allOf.
func (g *tsGen) composition(s *Schema, n int) (string, error) {
	if len(s.Type) > 0 || s.Properties != nil || s.Items != nil || s.Enum != nil || s.Const != nil ||
		s.AdditionalProperties != nil {
		return "", errors.New("a schema with oneOf, anyOf, or allOf has other keywords too")
	}
	var members []*Schema
	sep := " | "
	switch {
	case len(s.OneOf) > 0 && len(s.AnyOf) == 0 && len(s.AllOf) == 0:
		members = s.OneOf
	case len(s.AnyOf) > 0 && len(s.OneOf) == 0 && len(s.AllOf) == 0:
		members = s.AnyOf
	case len(s.AllOf) > 0 && len(s.OneOf) == 0 && len(s.AnyOf) == 0:
		members, sep = s.AllOf, " & "
	default:
		return "", errors.New("a schema combines more than one of oneOf, anyOf, and allOf")
	}
	var parts []string
	for _, m := range members {
		t, err := g.typeOf(m, n)
		if err != nil {
			return "", err
		}
		if strings.ContainsAny(t, "|&") {
			t = "(" + t + ")"
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, sep), nil
}

// literal returns the literal type of a JSON scalar.
func literal(v json.RawMessage) (string, error) {
	var x any
	d := json.NewDecoder(bytes.NewReader(v))
	d.UseNumber()
	if err := d.Decode(&x); err != nil {
		return "", err
	}
	switch x := x.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(x), nil
	case json.Number:
		return x.String(), nil
	case string:
		return tsgen.String(x), nil
	}
	return "", fmt.Errorf("the enum or const value %s is not a string, number, boolean, or null", v)
}

// object returns the type of an object schema.
func (g *tsGen) object(s *Schema, n int) (string, error) {
	add := s.AdditionalProperties
	if len(s.Properties) == 0 {
		switch {
		case add != nil && !add.Allowed:
			return "Record<string, never>", nil
		case add != nil && add.Schema != nil:
			t, err := g.typeOf(add.Schema, n)
			if err != nil {
				return "", err
			}
			return "Record<string, " + t + ">", nil
		}
		return "Record<string, unknown>", nil
	}
	if add != nil && add.Schema != nil && !add.Schema.empty() {
		return "", errors.New("an object has properties, and a schema of its other properties too")
	}
	required := map[string]bool{}
	for _, name := range s.Required {
		required[name] = true
	}
	var props []prop
	for _, name := range slices.Sorted(maps.Keys(s.Properties)) {
		p := s.Properties[name]
		desc, deprecated := "", false
		if p != nil {
			desc, deprecated = p.Description, p.Deprecated
			// TypeScript's comment says why.
			if required[name] && (p.ReadOnly || p.WriteOnly) {
				return "", fmt.Errorf("%s: the client cannot type a required property that is readOnly or "+
					"writeOnly", name)
			}
		}
		props = append(props, prop{name: name, optional: !required[name], schema: p, doc: desc,
			deprecated: deprecated})
	}
	return g.objectOf(props, n)
}

// empty reports whether s allows any value: it has no keyword that
// restricts one.
func (s *Schema) empty() bool {
	return len(s.Type) == 0 && s.Ref == "" && s.Enum == nil && s.Const == nil && s.Items == nil &&
		s.Properties == nil && s.AdditionalProperties == nil && !s.composed()
}

// prop is a member of an object type.
type prop struct {
	name       string
	optional   bool
	schema     *Schema
	doc        string
	deprecated bool
	// typ, if set, is the member's type, in place of the schema's.
	typ string
	// numeric makes the name a numeric literal, as a status is, so that
	// keyof gives the number.
	numeric bool
}

// objectOf returns an object type with the members props, whose lines
// after the first are indented by n.
func (g *tsGen) objectOf(props []prop, n int) (string, error) {
	if len(props) == 0 {
		return "Record<string, never>", nil
	}
	var b strings.Builder
	b.WriteString("{\n")
	for _, p := range props {
		t := p.typ
		if t == "" {
			var err error
			if t, err = g.typeOf(p.schema, n+1); err != nil {
				return "", fmt.Errorf("%s: %w", p.name, err)
			}
		}
		tsgen.Comment(&b, n+1, p.doc, p.deprecated)
		optional := ""
		if p.optional {
			optional = "?"
		}
		name := tsgen.PropertyName(p.name)
		if p.numeric {
			name = p.name
		}
		fmt.Fprintf(&b, "%s%s%s: %s;\n", tsgen.Indent(n+1), name, optional, t)
	}
	b.WriteString(tsgen.Indent(n) + "}")
	return b.String(), nil
}

// operations writes Operations and routes.
func (g *tsGen) operations() error {
	ops := locate(g.d)
	slices.SortFunc(ops, func(a, b located) int { return strings.Compare(a.op.OperationID, b.op.OperationID) })

	g.b.WriteString("\n")
	tsgen.Comment(&g.b, 0, "The API's operations, by operation ID: what each takes, and what it answers with "+
		"each status that is not an error's.", false)
	g.b.WriteString("export interface Operations {\n")
	for _, l := range ops {
		op := l.op
		text := op.Summary
		if op.Description != "" {
			text += "\n\n" + op.Description
		}
		input, err := g.input(op)
		if err != nil {
			return fmt.Errorf("%s %s: %w", l.method, l.path, err)
		}
		responses, err := g.responses(op)
		if err != nil {
			return fmt.Errorf("%s %s: %w", l.method, l.path, err)
		}
		tsgen.Comment(&g.b, 1, text, op.Deprecated)
		fmt.Fprintf(&g.b, "%s%s: {\n", tsgen.Indent(1), tsgen.PropertyName(op.OperationID))
		fmt.Fprintf(&g.b, "%sinput: %s;\n", tsgen.Indent(2), input)
		fmt.Fprintf(&g.b, "%sresponses: %s;\n", tsgen.Indent(2), responses)
		fmt.Fprintf(&g.b, "%s};\n", tsgen.Indent(1))
	}
	g.b.WriteString("}\n\n")

	tsgen.Comment(&g.b, 0, "The method and path of each operation, and whether it answers with a file (blob).", false)
	g.b.WriteString("export const routes = {\n")
	for _, l := range ops {
		blob := ""
		if answersFile(l.op) {
			blob = ", blob: true"
		}
		fmt.Fprintf(&g.b, "%s%s: { method: %s, path: %s%s },\n", tsgen.Indent(1), tsgen.PropertyName(l.op.OperationID),
			tsgen.String(l.method), tsgen.String(l.path), blob)
	}
	g.b.WriteString("} as const satisfies Record<keyof Operations, { method: string; path: string; blob?: true }>;\n")
	return nil
}

// answersFile reports whether op answers with a file (fileBody), whose type
// responses checks.
func answersFile(op *Operation) bool {
	for status, r := range op.Responses {
		if !isError(status) && isFile(r.Content) {
			return true
		}
	}
	return false
}

// input returns the type of an operation's input.
func (g *tsGen) input(op *Operation) (string, error) {
	groups := map[string][]prop{}
	for _, p := range op.Parameters {
		if p.In == "cookie" {
			return "", fmt.Errorf("the client cannot send the cookie parameter %s", p.Name)
		}
		t, err := g.parameterType(p.Schema)
		if err != nil {
			return "", fmt.Errorf("the %s parameter %s: %w", p.In, p.Name, err)
		}
		groups[p.In] = append(groups[p.In], prop{name: p.Name, optional: !p.Required, doc: p.Description,
			deprecated: p.Deprecated, typ: t})
	}
	var parts []prop
	for _, part := range []struct{ in, name string }{{"path", "path"}, {"query", "query"}, {"header", "headers"}} {
		members := groups[part.in]
		if len(members) == 0 {
			continue
		}
		slices.SortFunc(members, func(a, b prop) int { return strings.Compare(a.name, b.name) })
		t, err := g.objectOf(members, 3)
		if err != nil {
			return "", err
		}
		optional := true
		for _, m := range members {
			optional = optional && m.optional
		}
		parts = append(parts, prop{name: part.name, optional: optional, typ: t})
	}
	if rb := op.RequestBody; rb != nil {
		t, err := g.bodyType(rb.Content)
		if err != nil {
			return "", fmt.Errorf("the request body: %w", err)
		}
		parts = append(parts, prop{name: "body", optional: !rb.Required, doc: rb.Description, typ: t})
	}
	return g.objectOf(parts, 2)
}

// parameterType returns the type of a parameter's values, which the client
// writes with String.
func (g *tsGen) parameterType(s *Schema) (string, error) {
	r, _ := g.d.resolve(s)
	if r == nil || r.composed() || r.Items != nil || r.Properties != nil || len(r.Type) == 0 {
		return "", errors.New("the client can send only strings, numbers, and booleans as parameters")
	}
	for _, t := range r.Type {
		if !slices.Contains([]string{"string", "integer", "number", "boolean"}, t) {
			return "", errors.New("the client can send only strings, numbers, and booleans as parameters")
		}
	}
	return g.typeOf(s, 0)
}

// bodyType returns the type of a body: Blob for a file (isFile), and else
// the type of a body that is application/json, and fails if it may be
// anything else.
func (g *tsGen) bodyType(content map[string]*MediaType) (string, error) {
	if isFile(content) {
		return "Blob", nil
	}
	if len(content) != 1 || content["application/json"] == nil {
		return "", fmt.Errorf("the client handles only application/json and files, and the body may be %q",
			slices.Sorted(maps.Keys(content)))
	}
	return g.typeOf(content["application/json"].Schema, 3)
}

// isFile reports whether a body is a file: it has media types, none of them
// JSON, and each a binary string, as huma documents a body of bytes.
func isFile(content map[string]*MediaType) bool {
	if len(content) == 0 {
		return false
	}
	for mt, m := range content {
		if mt == "application/json" || strings.HasSuffix(mt, "+json") || m == nil || m.Schema == nil ||
			!slices.Equal(m.Schema.Type, Types{"string"}) || m.Schema.Format != "binary" {
			return false
		}
	}
	return true
}

// responses returns the type of an operation's responses that are not
// errors.
func (g *tsGen) responses(op *Operation) (string, error) {
	var statuses []string
	for status := range op.Responses {
		if isError(status) {
			continue
		}
		if strings.HasSuffix(status, "XX") {
			return "", fmt.Errorf("the client cannot type the responses to a range of statuses, such as %s",
				status)
		}
		statuses = append(statuses, status)
	}
	if len(statuses) == 0 {
		return "", errors.New("it has no response other than an error's")
	}
	slices.Sort(statuses)
	var members []prop
	for _, status := range statuses {
		r := op.Responses[status]
		t := "undefined"
		if len(r.Content) > 0 {
			var err error
			if t, err = g.bodyType(r.Content); err != nil {
				return "", fmt.Errorf("the response %s: %w", status, err)
			}
			// The client reads the answers of an operation that answers with a
			// file as files (routes' blob), so none of them may be JSON.
			if t != "Blob" && answersFile(op) {
				return "", fmt.Errorf("the response %s is JSON, and another is a file, which the client cannot "+
					"tell apart", status)
			}
		}
		members = append(members, prop{name: status, typ: t, numeric: true})
	}
	return g.objectOf(members, 2)
}
