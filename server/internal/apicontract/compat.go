package apicontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
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
//     error has the envelope.
//   - A request's schema accepts less, or a response's allows more: a type,
//     null, or an enum value; a bound, a length, or a count made tighter in
//     a request, or looser in a response; a format, pattern, multipleOf, or
//     uniqueItems added or changed in a request, or removed or changed in a
//     response, except a format that a request widens from int32 to int64
//     or from float to double, or a response narrows the other way, and a
//     multipleOf that a request changes to a divisor of the old one, or a
//     response to a multiple of it; an encoding changed; or, in a request,
//     a default changed or removed.
//   - A request's object no longer accepts a property, because it no longer
//     names it and accepts no others, or newly requires one. A response's
//     object lacks a property that it had, or no longer always has one that
//     it always had, or, if it described the properties that it did not
//     name, as a map does, names one whose values that description did not
//     allow. A required property is required as huma reads it: not in a
//     request if it is readOnly, and not in a response if it is writeOnly.
//
// Schemas combined with oneOf, anyOf, or allOf are compared member by
// member, and a change in the number of members breaks a client. Titles,
// descriptions, examples, deprecation, and operation IDs are not on the wire,
// and changing them breaks nothing.
func Breaking(base, head *Document) []Change {
	c := &comparer{base: base, head: head, seen: map[seenKey]bool{}}
	heads := map[string]located{}
	for _, l := range locate(head) {
		heads[l.key] = l
	}
	for _, b := range locate(base) {
		c.op = b.method + " " + b.path
		h, ok := heads[b.key]
		if !ok {
			c.report("the operation was removed")
			continue
		}
		c.parameters(b, h)
		c.requestBody(b.op.RequestBody, h.op.RequestBody)
		c.responses(b.op.Responses, h.op.Responses)
	}
	return c.changes
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
	// seen holds the pairs of schemas compared so far, so that a recursive
	// schema is compared once.
	seen map[seenKey]bool
}

type seenKey struct {
	base, head string
	dir        direction
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

func (c *comparer) responses(b, h map[string]*Response) {
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
			if !isError(status) {
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
	b, bName := c.base.resolve(b)
	h, hName := c.head.resolve(h)
	key := seenKey{identity(b, bName), identity(h, hName), dir}
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
	// Every value of wide must be a value of narrow.
	wide, narrow := b, h
	if dir == response {
		wide, narrow = h, b
	}
	c.types(loc, b, h, wide, narrow)
	c.values(loc, wide, narrow, dir)
	c.bounds(loc, b, h, wide, narrow)
	c.keywords(loc, b, h, wide, narrow)
	if dir == request && b.Default != nil && !sameJSON(b.Default, h.Default) {
		c.report("%s: the default changed from %s to %s", loc, jsonOrNone(b.Default), jsonOrNone(h.Default))
	}
	c.composition(loc, b, h, dir)
	c.object(loc, b, h, dir)
	if b.Items != nil || h.Items != nil {
		c.schema(loc+"[]", b.Items, h.Items, dir)
	}
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

func typeString(s *Schema) string {
	if len(s.Type) == 0 {
		return "any type"
	}
	return strings.Join(s.Type, " or ")
}

func (c *comparer) types(loc string, b, h, wide, narrow *Schema) {
	wt, wNull, wAny := typeSet(wide)
	nt, nNull, nAny := typeSet(narrow)
	if nAny {
		return
	}
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
// or const restricts them.
func enumValues(s *Schema) ([]string, bool) {
	switch {
	case s.Const != nil:
		return []string{compactJSON(s.Const)}, true
	case s.Enum != nil:
		var out []string
		for _, v := range s.Enum {
			out = append(out, compactJSON(v))
		}
		return out, true
	}
	return nil, false
}

func (c *comparer) values(loc string, wide, narrow *Schema, dir direction) {
	nv, restricted := enumValues(narrow)
	if !restricted {
		return
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

func (c *comparer) composition(loc string, b, h *Schema, dir direction) {
	for _, k := range []struct {
		name string
		b, h []*Schema
	}{{"oneOf", b.OneOf, h.OneOf}, {"anyOf", b.AnyOf, h.AnyOf}, {"allOf", b.AllOf, h.AllOf}} {
		if len(k.b) != len(k.h) {
			c.report("%s: %s changed from %d schemas to %d", loc, k.name, len(k.b), len(k.h))
			continue
		}
		for i := range k.b {
			c.schema(fmt.Sprintf("%s.%s[%d]", loc, k.name, i), k.b[i], k.h[i], dir)
		}
	}
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
		c.schema(at, b.Properties[name], is, dir)
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
			c.schema(at, additionalSchema(b), h.Properties[name], dir)
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
