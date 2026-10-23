package apicontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Change is a difference between two versions of the document that breaks a
// client written against the older one.
type Change struct {
	// Operation is the operation that changed, such as "GET /version".
	Operation string
	// Msg says what changed, and where in the operation.
	Msg string
}

func (c Change) String() string {
	return c.Operation + ": " + c.Msg
}

// Breaking returns the changes from base to head that break a client of
// base's API (PRD FR-API-01 AC3, §10.5). Such a client goes on sending what
// base accepted, and expects only the answers that base described. So head
// must accept every request that base accepted, and answer only as base said
// it would, with the exception that the PRD makes: a response may gain
// properties, because clients ignore the properties they do not know, and
// for the same reason it may gain headers. A client fails on a value it does
// not know (§6.4), so an enum value that a response gains breaks it.
//
// These changes break a client:
//
//   - An operation is removed, or its method or path changes. Renaming a
//     path parameter changes no request, and breaks nothing.
//   - A parameter is removed, becomes required, is new and required, or is
//     serialised in another style.
//   - A request body is removed, becomes required, is new and required, or
//     is no longer taken in a format that it was.
//   - A response has a status other than an error's, or a format, that
//     base's did not have, or it lacks a format or a header that base's had.
//     A header that base's always had is no longer always there, or is
//     serialised in another style. Errors may gain statuses, because every
//     error has the envelope, and an operation may gain a 304 if base's
//     took neither If-None-Match nor If-Modified-Since, because only a
//     request with one of them gets a 304.
//   - A request's schema accepts less, or a response's allows more: a type,
//     null, or an enum value; a bound, a length, or a count made tighter in
//     a request, or looser in a response; a format, pattern, multipleOf, or
//     uniqueItems added or changed in a request, or removed or changed in a
//     response, except a format that a request widens from int32 to int64
//     or from float to double, or a response narrows the other way, and a
//     multipleOf that a request changes to a divisor of the old one, or a
//     response to a multiple of it; an encoding changed; or, in a request,
//     a default changed or removed. Whether a schema with a type takes null
//     is up to the type, as huma's validator reads it, whether or not its
//     enum lists null, and a schema with an enum or a const allows only the
//     types of its values. A property that a request's object does not
//     require takes null whatever its schema, because huma's validator lets
//     null through for one without reading its schema.
//   - A request's object no longer accepts a property, because it no longer
//     names it and accepts no others, or newly requires one. A response's
//     object lacks a property that it had, or no longer always has one that
//     it always had, or, if it described the properties that it did not
//     name, as a map does, names one whose values that description did not
//     allow. A required property is required as huma reads it: not in a
//     request if it is readOnly, and not in a response if it is writeOnly.
//
// A oneOf's discriminator is what huma's validator reads to choose the
// member that it validates an object against, by the value of a property,
// and to let through a value that is not an object, or whose property is not
// a string. A request's may map values that base's did not, and a request's
// oneOf may gain one, as long as no member of base's oneOf but the one that
// such a value is mapped to takes an object with that value, as each needs
// the property, with a schema that does not take the value, and, for a oneOf
// that gains one, every member of base's needs the property: then the
// discriminator leads every object that base took to the member that took it,
// as when a request's union of nodes gains a type of node and maps it to the
// member that it gains. Any other change to a discriminator breaks a client:
// one that maps a value to another member, or no longer maps it, or reads
// another property, one that a request's oneOf loses, which then validates
// an object whose property is not a string against every member, and any
// change to a response's.
//
// Schemas combined with oneOf, anyOf, or allOf are compared member by
// member. If the members of a oneOf or an anyOf all refer to named schemas,
// as those of the command endpoint's union of the types of command do, they
// are matched by name, so that they may move: a request's may gain members,
// because head then accepts more, and a response's may lose them, while a
// member that a request's loses, or a response's gains, breaks a client. A
// member that a request's oneOf gains must also take no value that one of
// base's members takes, because huma's validator refuses a value that two
// members of a oneOf take; the comparison tells that two members share no
// value from their types, enums, and consts, and those of a property that both
// require, as a command's type. Other members are matched by position, and a
// change in the number of them breaks a client. Titles, descriptions,
// examples, deprecation, and operation IDs are not on the wire, and changing
// them breaks nothing.
//
// A change to a schema that several operations share is reported for each of
// them, at the first place in the operation where the schema is.
func Breaking(base, head *Document) []Change {
	c := &comparer{base: base, head: head}
	heads := map[string]located{}
	for _, l := range locate(head) {
		heads[l.key] = l
	}
	for _, b := range locate(base) {
		c.op = b.method + " " + b.path
		c.seen = map[seenKey]bool{}
		h, ok := heads[b.key]
		if !ok {
			c.report("the operation was removed")
			continue
		}
		c.parameters(b, h)
		c.requestBody(b.op.RequestBody, h.op.RequestBody)
		c.responses(b.op.Responses, h.op.Responses, conditional(b.op))
	}
	return c.changes
}

// conditional reports whether op takes a header that makes a request
// conditional, If-None-Match or If-Modified-Since, which a 304 answers (RFC
// 9110 §15.4.5).
func conditional(op *Operation) bool {
	return slices.ContainsFunc(op.Parameters, func(p *Parameter) bool {
		key := paramKey(p)
		return key == "header if-none-match" || key == "header if-modified-since"
	})
}

// direction is which way the values that a schema describes go.
type direction int

const (
	// request values are sent by the client: head must accept every value
	// that base accepted.
	request direction = iota
	// response values are sent by the server: base must have allowed every
	// value that head may send, apart from the properties that head adds.
	response
)

type comparer struct {
	base, head *Document
	// op is the operation being compared.
	op      string
	changes []Change
	// seen holds the pairs of schemas compared so far in the operation, so
	// that a recursive schema is compared once.
	seen map[seenKey]bool
}

type seenKey struct {
	base, head string
	dir        direction
	headNull   bool
}

func (c *comparer) report(format string, args ...any) {
	c.changes = append(c.changes, Change{Operation: c.op, Msg: fmt.Sprintf(format, args...)})
}

// located is an operation with its method and path.
type located struct {
	method, path string
	// key is the method and the path without its parameters' names, which
	// requests do not show.
	key string
	op  *Operation
}

// locate returns the document's operations, by path and then method.
func locate(d *Document) []located {
	var out []located
	for _, path := range slices.Sorted(maps.Keys(d.Paths)) {
		for _, mo := range d.Paths[path].operations() {
			out = append(out, located{
				method: mo.method,
				path:   path,
				key:    mo.method + " " + pathParam.ReplaceAllString(path, "{}"),
				op:     mo.op,
			})
		}
	}
	return out
}

// params returns the operation's parameters by a key that matches them
// between versions: their place and name, except that a path parameter is
// matched by its position in the path, so that it may be renamed.
func params(l located) map[string]*Parameter {
	positions := map[string]int{}
	for i, name := range templateParams(l.path) {
		positions[name] = i + 1
	}
	out := map[string]*Parameter{}
	for _, p := range l.op.Parameters {
		key := paramKey(p)
		if p.In == "path" {
			key = "path #" + strconv.Itoa(positions[p.Name])
		}
		out[key] = p
	}
	return out
}

// serialization returns how a parameter or a header in the place in is
// written, with OpenAPI's defaults filled in.
func serialization(in, style string, explode *bool) string {
	if style == "" {
		style = "simple"
		if in == "query" || in == "cookie" {
			style = "form"
		}
	}
	exploded := style == "form"
	if explode != nil {
		exploded = *explode
	}
	return style + " " + strconv.FormatBool(exploded)
}

func (c *comparer) parameters(b, h located) {
	bp, hp := params(b), params(h)
	for _, key := range slices.Sorted(maps.Keys(bp)) {
		was := bp[key]
		name := "the " + was.In + " parameter " + was.Name
		is, ok := hp[key]
		if !ok {
			c.report("%s was removed", name)
			continue
		}
		if !was.Required && is.Required {
			c.report("%s is now required", name)
		}
		if w, i := serialization(was.In, was.Style, was.Explode), serialization(is.In, is.Style, is.Explode); w != i {
			c.report("%s is now serialised as %s, and was as %s", name, i, w)
		}
		c.schema(name, was.Schema, is.Schema, request)
	}
	for _, key := range slices.Sorted(maps.Keys(hp)) {
		if is := hp[key]; bp[key] == nil && is.Required {
			c.report("the %s parameter %s is new and required", is.In, is.Name)
		}
	}
}

func (c *comparer) requestBody(b, h *RequestBody) {
	switch {
	case b == nil && h == nil:
		return
	case b == nil:
		if h.Required {
			c.report("the request body is new and required")
		}
		return
	case h == nil:
		c.report("the request body was removed")
		return
	}
	if !b.Required && h.Required {
		c.report("the request body is now required")
	}
	for _, mt := range slices.Sorted(maps.Keys(b.Content)) {
		is, ok := h.Content[mt]
		if !ok {
			c.report("the request body is no longer taken as %s", mt)
			continue
		}
		c.schema("the request body", b.Content[mt].Schema, is.Schema, request)
	}
}

// isError reports whether a response key is an error's: a 4xx or 5xx status
// or range, or default, which the API uses for the error envelope.
func isError(status string) bool {
	return status == "default" || status[0] == '4' || status[0] == '5'
}

// lookup returns the response that answers with status: its own, or its
// range's, or, for an error, the default response.
func lookup(responses map[string]*Response, status string) *Response {
	if r := responses[status]; r != nil {
		return r
	}
	if status == "default" {
		return nil
	}
	if r := responses[status[:1]+"XX"]; r != nil {
		return r
	}
	if isError(status) {
		return responses["default"]
	}
	return nil
}

// responses compares base's and head's responses of an operation. cond says
// whether base's took a conditional header, without which no client of it
// got a 304, so that a 304 that head adds breaks none of them, as when an
// operation gains an ETag and If-None-Match.
func (c *comparer) responses(b, h map[string]*Response, cond bool) {
	var statuses []string
	for s := range b {
		statuses = append(statuses, s)
	}
	for s := range h {
		if b[s] == nil {
			statuses = append(statuses, s)
		}
	}
	slices.Sort(statuses)
	for _, status := range statuses {
		was, is := lookup(b, status), lookup(h, status)
		switch {
		case is == nil:
			// Head no longer answers with the status, which no client
			// relies on getting.
		case was == nil:
			if !isError(status) && (status != "304" || cond) {
				c.report("the response may now have the status %s", status)
			}
		default:
			c.response(status, was, is)
		}
	}
}

func (c *comparer) response(status string, b, h *Response) {
	name := "the response " + status
	for _, mt := range slices.Sorted(maps.Keys(h.Content)) {
		if b.Content[mt] == nil {
			c.report("%s may now be %s", name, mt)
		}
	}
	for _, mt := range slices.Sorted(maps.Keys(b.Content)) {
		is, ok := h.Content[mt]
		if !ok {
			c.report("%s is no longer %s", name, mt)
			continue
		}
		c.schema(name+" body", b.Content[mt].Schema, is.Schema, response)
	}
	headers := map[string]*Header{}
	for n, hd := range h.Headers {
		headers[strings.ToLower(n)] = hd
	}
	// A header that base's did not have breaks nothing, because clients
	// ignore the headers they do not know.
	for _, n := range slices.Sorted(maps.Keys(b.Headers)) {
		was := b.Headers[n]
		is, ok := headers[strings.ToLower(n)]
		if !ok {
			c.report("%s no longer has the header %s", name, n)
			continue
		}
		if was.Required && !is.Required {
			c.report("%s no longer always has the header %s", name, n)
		}
		if w, i := serialization("header", was.Style, was.Explode), serialization("header", is.Style, is.Explode); w != i {
			c.report("%s's header %s is now serialised as %s, and was as %s", name, n, i, w)
		}
		c.schema(name+"'s header "+n, was.Schema, is.Schema, response)
	}
}

// identity names a schema for seen: the named schema it refers to, or its
// address, which is unique to its place in the document. A missing schema
// allows any value wherever it is missing.
func identity(s *Schema, name string) string {
	switch {
	case name != "":
		return "#" + name
	case s == nil:
		return "any"
	default:
		return fmt.Sprintf("%p", s)
	}
}

// schema compares base's and head's schema of the values at loc, which go in
// direction dir.
func (c *comparer) schema(loc string, b, h *Schema, dir direction) {
	c.compare(loc, b, h, dir, false)
}

// compare is schema, where headNull says that head takes null at loc in a
// request whatever its schema, as it takes a property that it does not
// require.
func (c *comparer) compare(loc string, b, h *Schema, dir direction, headNull bool) {
	headNull = headNull && dir == request
	b, bName := c.base.resolve(b)
	h, hName := c.head.resolve(h)
	key := seenKey{identity(b, bName), identity(h, hName), dir, headNull}
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	if b == nil {
		b = &Schema{}
	}
	if h == nil {
		h = &Schema{}
	}
	// Every value of wide must be a value of narrow. In a request, narrow is
	// head, so headNull says that narrow takes null.
	wide, narrow := b, h
	if dir == response {
		wide, narrow = h, b
	}
	c.types(loc, b, h, wide, narrow, headNull)
	c.values(loc, wide, narrow, dir, headNull)
	c.bounds(loc, b, h, wide, narrow)
	c.keywords(loc, b, h, wide, narrow)
	if dir == request && b.Default != nil && !sameJSON(b.Default, h.Default) {
		c.report("%s: the default changed from %s to %s", loc, jsonOrNone(b.Default), jsonOrNone(h.Default))
	}
	c.discriminator(loc, b, h, dir)
	c.composition(loc, b, h, dir, headNull)
	c.object(loc, b, h, dir)
	if b.Items != nil || h.Items != nil {
		c.schema(loc+"[]", b.Items, h.Items, dir)
	}
}

// discriminator reports a change from base's discriminator of the oneOf at loc
// to head's that breaks a client: any change in a response, and in a request
// any change but one that widens judges compatible.
func (c *comparer) discriminator(loc string, b, h *Schema, dir direction) {
	was, is := describeDiscriminator(b.Discriminator), describeDiscriminator(h.Discriminator)
	if was == is || dir == request && c.widens(b, h) {
		return
	}
	c.report("%s: the discriminator changed from %s to %s", loc, was, is)
}

// widens reports whether, in a request, head's oneOf, with its discriminator,
// takes every object that base's took, with base's discriminator or without
// one, as far as the discriminators decide it. huma's validator validates an
// object whose property is a string that the mapping maps against that
// member alone, and an object whose property is another string against every
// member, as a oneOf without a discriminator does; it refuses an object
// without the property, and lets through a value that is not an object, or
// whose property is not a string. So head's discriminator reads the property
// that base's read, and maps each value that base's mapped to the same
// member; and no member of base's oneOf but the one that head's maps a value
// to, that base's did not, takes an object with that value, as each needs the
// property, with a schema that does not take the value, so that such an
// object that base took, it took by that member, which composition compares.
// When base's oneOf had no discriminator, each of its members also needs the
// property, so that base refused an object without it too.
//
// A discriminator that a request's oneOf loses breaks a client, since the
// oneOf then validates an object whose property is not a string against
// every member, which base let through.
func (c *comparer) widens(b, h *Schema) bool {
	bd, hd := b.Discriminator, h.Discriminator
	if hd == nil || bd != nil && bd.PropertyName != hd.PropertyName {
		return false
	}
	property := hd.PropertyName
	var mapped map[string]string
	if bd != nil {
		mapped = bd.Mapping
	}
	for _, value := range slices.Sorted(maps.Keys(mapped)) {
		if to, ok := hd.Mapping[value]; !ok || refName(to) != refName(mapped[value]) {
			return false
		}
	}
	// needs reports whether base's member m needs the property in a request,
	// and returns the property's schema, or nil when it has none.
	needs := func(m *Schema) (*Schema, bool) {
		s, _ := c.base.resolve(m)
		if s == nil || !needed(s, request)[property] {
			return nil, false
		}
		p, _ := c.base.resolve(s.Properties[property])
		return p, true
	}
	for _, value := range slices.Sorted(maps.Keys(hd.Mapping)) {
		if _, ok := mapped[value]; ok {
			continue
		}
		taken := &Schema{Const: json.RawMessage(strconv.Quote(value))}
		for _, m := range b.OneOf {
			if m != nil && m.Ref != "" && refName(m.Ref) == refName(hd.Mapping[value]) {
				continue
			}
			if p, ok := needs(m); !ok || p == nil || !exclusive(p, taken) {
				return false
			}
		}
	}
	if bd == nil {
		for _, m := range b.OneOf {
			if _, ok := needs(m); !ok {
				return false
			}
		}
	}
	return true
}

// describeDiscriminator writes a oneOf's discriminator d as a change names
// it: its property, and the name of the schema that it maps each value to, in
// the order of the values, or "none".
func describeDiscriminator(d *Discriminator) string {
	if d == nil {
		return "none"
	}
	var mapping []string
	for _, value := range slices.Sorted(maps.Keys(d.Mapping)) {
		mapping = append(mapping, strconv.Quote(value)+": "+refName(d.Mapping[value]))
	}
	return fmt.Sprintf("%s {%s}", strconv.Quote(d.PropertyName), strings.Join(mapping, ", "))
}

// typeSet returns the types that s allows, apart from null, whether it
// allows null, and whether it allows any type.
func typeSet(s *Schema) (types []string, nullable, anyType bool) {
	if len(s.Type) == 0 {
		return nil, true, true
	}
	for _, t := range s.Type {
		if t == "null" {
			nullable = true
		} else {
			types = append(types, t)
		}
	}
	return types, nullable, false
}

// valueTypes returns the types of values, which are compact JSON, apart from
// null, and whether one of them is null: the types that a schema whose enum
// or const allows only those values allows, whatever its type says.
func valueTypes(values []string) (types []string, nullable bool) {
	for _, v := range values {
		var t string
		switch {
		case v == "null":
			nullable = true
			continue
		case v == "true" || v == "false":
			t = "boolean"
		case strings.HasPrefix(v, `"`):
			t = "string"
		case strings.HasPrefix(v, "{"):
			t = "object"
		case strings.HasPrefix(v, "["):
			t = "array"
		default:
			t = "number"
			if f, err := strconv.ParseFloat(v, 64); err == nil && f == math.Trunc(f) {
				t = "integer"
			}
		}
		if !slices.Contains(types, t) {
			types = append(types, t)
		}
	}
	return types, nullable
}

func typeString(s *Schema) string {
	if len(s.Type) == 0 {
		return "any type"
	}
	return strings.Join(s.Type, " or ")
}

// types reports a type that wide allows and narrow does not; narrowNull says
// that narrow takes null whatever its type.
func (c *comparer) types(loc string, b, h, wide, narrow *Schema, narrowNull bool) {
	wt, wNull, wAny := typeSet(wide)
	if values, restricted := enumValues(wide); restricted {
		wt, wNull = valueTypes(values)
		wAny = false
	}
	nt, nNull, nAny := typeSet(narrow)
	if nAny {
		return
	}
	nNull = nNull || narrowNull
	lost := wAny || wNull && !nNull
	for _, t := range wt {
		// Every integer is a number.
		if !slices.Contains(nt, t) && !(t == "integer" && slices.Contains(nt, "number")) {
			lost = true
		}
	}
	if lost {
		c.report("%s: the type changed from %s to %s", loc, typeString(b), typeString(h))
	}
}

// enumValues returns the values that s allows, as compact JSON, if its enum
// or const restricts them. huma's validator lets null through a type that
// allows it before it reads the enum, and the TypeScript generator types
// such a schema the same way. So null is one of the values of a schema with
// a type exactly when the type allows it, whether or not the enum lists it.
func enumValues(s *Schema) ([]string, bool) {
	var listed []json.RawMessage
	switch {
	case s.Const != nil:
		listed = []json.RawMessage{s.Const}
	case s.Enum != nil:
		listed = s.Enum
	default:
		return nil, false
	}
	var out []string
	for _, v := range listed {
		if c := compactJSON(v); c != "null" || len(s.Type) == 0 {
			out = append(out, c)
		}
	}
	if slices.Contains(s.Type, "null") {
		out = append(out, "null")
	}
	return out, true
}

// values reports a value that wide allows and narrow's enum or const does
// not; narrowNull says that narrow takes null whatever its schema.
func (c *comparer) values(loc string, wide, narrow *Schema, dir direction, narrowNull bool) {
	nv, restricted := enumValues(narrow)
	if !restricted {
		return
	}
	if narrowNull && !slices.Contains(nv, "null") {
		nv = append(nv, "null")
	}
	wv, wasRestricted := enumValues(wide)
	if !wasRestricted {
		if dir == request {
			c.report("%s: it is now limited to %s", loc, strings.Join(nv, ", "))
		} else {
			c.report("%s: it is no longer limited to %s", loc, strings.Join(nv, ", "))
		}
		return
	}
	var lost []string
	for _, v := range wv {
		// When both schemas have a type, null is up to it, and types
		// reports a change.
		if v == "null" && len(wide.Type) > 0 && len(narrow.Type) > 0 {
			continue
		}
		if !slices.Contains(nv, v) {
			lost = append(lost, v)
		}
	}
	switch {
	case len(lost) == 0:
	case dir == request:
		c.report("%s: the values %s were removed", loc, strings.Join(lost, ", "))
	default:
		c.report("%s: the values %s were added", loc, strings.Join(lost, ", "))
	}
}

// bound is a lower or upper bound of a schema's values, lengths, or counts.
type bound struct {
	set       bool
	v         float64
	exclusive bool
}

func (b bound) String() string {
	switch {
	case !b.set:
		return "none"
	case b.exclusive:
		return strconv.FormatFloat(b.v, 'g', -1, 64) + " (exclusive)"
	default:
		return strconv.FormatFloat(b.v, 'g', -1, 64)
	}
}

// tighter reports whether bound n, a lower bound if lower is set, excludes
// a value that bound w allows.
func tighter(n, w bound, lower bool) bool {
	switch {
	case !n.set:
		return false
	case !w.set:
		return true
	case n.v == w.v:
		return n.exclusive && !w.exclusive
	case lower:
		return n.v > w.v
	default:
		return n.v < w.v
	}
}

// numberBound combines an inclusive and an exclusive bound into the tighter
// of the two.
func numberBound(inclusive, exclusive *float64, lower bool) bound {
	var b bound
	if inclusive != nil {
		b = bound{set: true, v: *inclusive}
	}
	if exclusive != nil {
		if e := (bound{set: true, v: *exclusive, exclusive: true}); tighter(e, b, lower) {
			b = e
		}
	}
	return b
}

func intBound(n *int) bound {
	if n == nil {
		return bound{}
	}
	return bound{set: true, v: float64(*n)}
}

func (c *comparer) bounds(loc string, b, h, wide, narrow *Schema) {
	for _, k := range []struct {
		name  string
		lower bool
		get   func(*Schema) bound
	}{
		{"the lower bound", true, func(s *Schema) bound { return numberBound(s.Minimum, s.ExclusiveMinimum, true) }},
		{"the upper bound", false, func(s *Schema) bound { return numberBound(s.Maximum, s.ExclusiveMaximum, false) }},
		{"minLength", true, func(s *Schema) bound { return intBound(s.MinLength) }},
		{"maxLength", false, func(s *Schema) bound { return intBound(s.MaxLength) }},
		{"minItems", true, func(s *Schema) bound { return intBound(s.MinItems) }},
		{"maxItems", false, func(s *Schema) bound { return intBound(s.MaxItems) }},
		{"minProperties", true, func(s *Schema) bound { return intBound(s.MinProperties) }},
		{"maxProperties", false, func(s *Schema) bound { return intBound(s.MaxProperties) }},
	} {
		if tighter(k.get(narrow), k.get(wide), k.lower) {
			c.report("%s: %s changed from %s to %s", loc, k.name, k.get(b), k.get(h))
		}
	}
}

// formatWidens maps a format to a wider one, which allows every value that
// it allows: every int32 is an int64, and every float is a double.
var formatWidens = map[string]string{"int32": "int64", "float": "double"}

// keywords compares the keywords that restrict values without a bound.
func (c *comparer) keywords(loc string, b, h, wide, narrow *Schema) {
	for _, k := range []struct {
		name string
		get  func(*Schema) string
		// within reports whether every value that the keyword's value w
		// allows is one that its value n allows, "" being its absence.
		within func(w, n string) bool
	}{
		{"format", func(s *Schema) string { return s.Format }, formatWithin},
		{"pattern", func(s *Schema) string { return s.Pattern }, restrictingWithin},
		{"multipleOf", func(s *Schema) string {
			if s.MultipleOf == nil {
				return ""
			}
			return strconv.FormatFloat(*s.MultipleOf, 'g', -1, 64)
		}, multipleWithin},
		{"uniqueItems", func(s *Schema) string {
			if !s.UniqueItems {
				return ""
			}
			return "true"
		}, restrictingWithin},
		{"contentEncoding", func(s *Schema) string { return s.ContentEncoding }, readingWithin},
		{"contentMediaType", func(s *Schema) string { return s.ContentMediaType }, readingWithin},
	} {
		if !k.within(k.get(wide), k.get(narrow)) {
			c.report("%s: %s changed from %s to %s", loc, k.name, quoteOrNone(k.get(b)), quoteOrNone(k.get(h)))
		}
	}
}

// restrictingWithin is within for a keyword that allows every value when it
// is absent, and whose values the comparison does not order, as it does not
// order two patterns: n allows what w does if it is absent or the same.
func restrictingWithin(w, n string) bool {
	return n == "" || w == n
}

// readingWithin is within for a keyword that changes how a value is read
// rather than which values there are, so that any change breaks a client.
func readingWithin(w, n string) bool {
	return w == n
}

// formatWithin is within for format, which a wider format of the same type
// may replace.
func formatWithin(w, n string) bool {
	return restrictingWithin(w, n) || formatWidens[w] == n
}

// multipleWithin is within for multipleOf: every multiple of w is a
// multiple of n if w is one, give or take the rounding of their decimals.
func multipleWithin(w, n string) bool {
	if n == "" {
		return true
	}
	if w == "" {
		return false
	}
	wf, errW := strconv.ParseFloat(w, 64)
	nf, errN := strconv.ParseFloat(n, 64)
	if errW != nil || errN != nil {
		return false
	}
	r := wf / nf
	return r >= 1 && math.Abs(r-math.Round(r)) <= 1e-9*r
}

// composition compares the members of b's and h's oneOf, anyOf, and allOf:
// those of a oneOf or an anyOf by name, as union does, if it can, and the
// others by position. headNull, that head takes null whatever its schema,
// holds for its members too, because huma's validator lets that null through
// before it reads them.
func (c *comparer) composition(loc string, b, h *Schema, dir direction, headNull bool) {
	for _, k := range []struct {
		name string
		b, h []*Schema
	}{{"oneOf", b.OneOf, h.OneOf}, {"anyOf", b.AnyOf, h.AnyOf}, {"allOf", b.AllOf, h.AllOf}} {
		at := loc + "." + k.name
		if k.name != "allOf" && c.union(at, k.name == "oneOf", k.b, k.h, dir, headNull) {
			continue
		}
		if len(k.b) != len(k.h) {
			c.report("%s: %s changed from %d schemas to %d", loc, k.name, len(k.b), len(k.h))
			continue
		}
		for i := range k.b {
			c.compare(fmt.Sprintf("%s[%d]", at, i), k.b[i], k.h[i], dir, headNull)
		}
	}
}

// union compares base's members b and head's members h of the oneOf or the
// anyOf at loc, if every member of both refers to a named schema that no
// other member of its list refers to, and reports whether it did. It matches
// the members by the names of their schemas rather than by position, because
// a list sorted by name, as the command endpoint's union of the types of
// command is, moves its members when one is added before them. A request's
// members may grow, because head then accepts more, and a response's may
// shrink, because head then sends less. But huma's validator refuses a value
// that more than one member of a oneOf takes, so a member that a request's
// oneOf gains must take no value that one of base's members takes, as
// disjoint judges.
func (c *comparer) union(loc string, oneOf bool, b, h []*Schema, dir direction, headNull bool) bool {
	bNames, hNames := memberNames(b), memberNames(h)
	if bNames == nil || hNames == nil {
		return false
	}
	for i, name := range bNames {
		j := slices.Index(hNames, name)
		switch {
		case j >= 0:
			c.compare(loc+"["+name+"]", b[i], h[j], dir, headNull)
		case dir == request:
			c.report("%s[%s] was removed", loc, name)
		}
	}
	for j, name := range hNames {
		switch {
		case slices.Contains(bNames, name):
		case dir == response:
			c.report("%s[%s] was added", loc, name)
		case oneOf:
			var overlaps []string
			for i, other := range bNames {
				if !c.disjoint(b[i], h[j]) {
					overlaps = append(overlaps, other)
				}
			}
			if len(overlaps) > 0 {
				c.report("%s[%s] was added, and may match a value of %s, which oneOf then refuses", loc, name,
					strings.Join(overlaps, ", "))
			}
		}
	}
	return true
}

// memberNames returns the names of the schemas that the members of a oneOf
// or an anyOf refer to, in order, or nil unless there are members, each of
// which refers to a schema that no other one does.
func memberNames(members []*Schema) []string {
	var names []string
	for _, m := range members {
		if m == nil || m.Ref == "" || slices.Contains(names, refName(m.Ref)) {
			return nil
		}
		names = append(names, refName(m.Ref))
	}
	return names
}

// disjoint reports whether no value matches both base's schema b and head's
// schema h, as huma's validator reads them: because they share no value by
// exclusive's reckoning, or because both take only objects and need a
// property in a request whose schemas share none, as the property type of
// the command endpoint's commands tells them apart. It reads only these
// keywords, so it may miss that two schemas are disjoint, but never that
// they are not.
func (c *comparer) disjoint(b, h *Schema) bool {
	b, _ = c.base.resolve(b)
	h, _ = c.head.resolve(h)
	if b == nil || h == nil {
		return false
	}
	if exclusive(b, h) {
		return true
	}
	if !onlyObjects(b) || !onlyObjects(h) {
		return false
	}
	// huma's validator refuses an object that lacks a property that it needs,
	// and, unless the object takes null, reads the property's schema even for
	// a null value.
	hNeeds := needed(h, request)
	for _, name := range slices.Sorted(maps.Keys(needed(b, request))) {
		if !hNeeds[name] {
			continue
		}
		bp, _ := c.base.resolve(b.Properties[name])
		hp, _ := c.head.resolve(h.Properties[name])
		if bp != nil && hp != nil && exclusive(bp, hp) {
			return true
		}
	}
	return false
}

// exclusive reports whether a and b share no value: their enums or consts
// list none in common, or they allow no type in common.
func exclusive(a, b *Schema) bool {
	av, aListed := listed(a)
	bv, bListed := listed(b)
	if aListed && bListed {
		return !slices.ContainsFunc(av, func(v string) bool {
			return slices.ContainsFunc(bv, func(w string) bool { return sameValue(v, w) })
		})
	}
	at, aNull, aAny := allowed(a)
	bt, bNull, bAny := allowed(b)
	if aAny || bAny || aNull && bNull {
		return false
	}
	for _, t := range at {
		for _, u := range bt {
			// Every integer is a number.
			if t == u || t == "integer" && u == "number" || t == "number" && u == "integer" {
				return false
			}
		}
	}
	return true
}

// listed returns the values that s allows, as enumValues does, if its enum
// or const lists any: huma's validator ignores an empty enum.
func listed(s *Schema) ([]string, bool) {
	if s.Const == nil && len(s.Enum) == 0 {
		return nil, false
	}
	return enumValues(s)
}

// allowed returns the types that s allows, as typeSet does, or the types of
// the values that its enum or const lists, which are all that it allows.
func allowed(s *Schema) (types []string, nullable, anyType bool) {
	if values, ok := listed(s); ok {
		types, nullable = valueTypes(values)
		return types, nullable, false
	}
	return typeSet(s)
}

// onlyObjects reports whether s allows objects and nothing else.
func onlyObjects(s *Schema) bool {
	types, nullable, anyType := allowed(s)
	return !anyType && !nullable && slices.Equal(types, []string{"object"})
}

// sameValue reports whether the compact JSON values a and b may be the same
// value, as 1 and 1.0 are, which huma's validator decodes alike.
func sameValue(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return true
	}
	return reflect.DeepEqual(x, y)
}

// accepted reports whether s accepts properties it does not name, which
// JSON Schema does unless additionalProperties says otherwise.
func accepted(s *Schema) bool {
	return s.AdditionalProperties == nil || s.AdditionalProperties.Allowed
}

func additionalSchema(s *Schema) *Schema {
	if s.AdditionalProperties == nil {
		return nil
	}
	return s.AdditionalProperties.Schema
}

// needed returns the properties that a value of s must have when it goes in
// direction dir: those that s requires, except that huma requires no readOnly
// property in a request, and no writeOnly one in a response. huma writes
// readOnly and writeOnly on the property, beside its reference if it has one.
func needed(s *Schema, dir direction) map[string]bool {
	out := map[string]bool{}
	for _, name := range s.Required {
		if p := s.Properties[name]; p != nil && (dir == request && p.ReadOnly || dir == response && p.WriteOnly) {
			continue
		}
		out[name] = true
	}
	return out
}

func (c *comparer) object(loc string, b, h *Schema, dir direction) {
	bReq, hReq := needed(b, dir), needed(h, dir)
	// huma's validator lets null through for a property of a request that
	// the object does not list as required, readOnly or not, before it reads
	// the property's schema.
	takesNull := func(name string) bool { return !slices.Contains(h.Required, name) }
	for _, name := range slices.Sorted(maps.Keys(b.Properties)) {
		at := loc + "." + name
		is, ok := h.Properties[name]
		switch {
		case ok:
		case dir == response:
			c.report("%s was removed", at)
			continue
		case !accepted(h):
			c.report("%s was removed, and is rejected", at)
			continue
		default:
			c.schema(at, b.Properties[name], additionalSchema(h), dir)
			continue
		}
		switch {
		case dir == request && !bReq[name] && hReq[name]:
			c.report("%s is now required", at)
		case dir == response && bReq[name] && !hReq[name]:
			c.report("%s is no longer always present", at)
		}
		c.compare(at, b.Properties[name], is, dir, takesNull(name))
	}
	for _, name := range slices.Sorted(maps.Keys(h.Properties)) {
		if _, ok := b.Properties[name]; ok {
			continue
		}
		at := loc + "." + name
		switch {
		case dir == request && hReq[name]:
			c.report("%s is new and required", at)
		case dir == request && accepted(b):
			// A client may have sent the property, as one that base
			// accepted without naming it.
			c.compare(at, additionalSchema(b), h.Properties[name], dir, takesNull(name))
		case dir == response && additionalSchema(b) != nil:
			// Base described every property that it did not name, as a map
			// does, so a client reads the new one as one of those.
			c.schema(at, additionalSchema(b), h.Properties[name], dir)
		}
		// Otherwise a response may gain properties, which clients ignore.
	}
	switch {
	case dir == request && accepted(b) && !accepted(h):
		c.report("%s: properties that it does not name are now rejected", loc)
	case accepted(b) && accepted(h) && (b.AdditionalProperties != nil || h.AdditionalProperties != nil):
		c.schema(loc+".*", additionalSchema(b), additionalSchema(h), dir)
	}
}

func compactJSON(v json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, v); err != nil {
		return string(v)
	}
	return b.String()
}

func sameJSON(a, b json.RawMessage) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return compactJSON(a) == compactJSON(b)
}

func jsonOrNone(v json.RawMessage) string {
	if v == nil {
		return "none"
	}
	return compactJSON(v)
}

func quoteOrNone(s string) string {
	if s == "" {
		return "none"
	}
	return strconv.Quote(s)
}
