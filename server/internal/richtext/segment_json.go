package richtext

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
	"github.com/CyberCute-Studio/storycreep/server/internal/semantics"
)

// JSONSchema gives the published schemas the definition of a document's
// segments, as the published format holds them (schemagen.Provider): an
// array of segments, in order, as Segments.MarshalJSON writes them.
func (Segments) JSONSchema(g *schemagen.Generator) *schemagen.Schema {
	return &schemagen.Schema{
		Type:  "array",
		Items: g.Ref(reflectSegment),
		Description: "A rich text's segments, in order, which a runtime walks instead of a document's tree " +
			"(PRD §6.1, ADR-020). schema/rich-text/README.md describes each kind, and the plain text and the " +
			"preview derived from them.",
	}
}

// JSONSchema gives the published schemas the definition of a segment, as
// the published format holds it (schemagen.Provider): one of the variants of
// its kinds, each with only the fields of its kind, as Segment.MarshalJSON
// writes it.
func (Segment) JSONSchema(g *schemagen.Generator) *schemagen.Schema {
	marks := g.Define("SegmentMarks", func() *schemagen.Schema {
		enum := make([]any, len(markOrder))
		for i, m := range markOrder {
			enum[i] = m
		}
		return &schemagen.Schema{
			Type: "array", Items: &schemagen.Schema{Type: "string", Enum: enum},
			MaxItems: schemagen.Int(len(markOrder)),
			Description: "An inline segment's marks, each at most once, in alphabetical order, [] for none. " +
				"link is there exactly when href is.",
		}
	})
	href := &schemagen.Schema{
		Type: "string", MinLength: schemagen.Int(1), MaxLength: schemagen.Int(MaxHrefChars), Pattern: hrefPattern,
		Description: "The URL of the link that the segment is part of.",
	}
	depth := &schemagen.Schema{Type: "integer", Minimum: schemagen.Float(1), Maximum: schemagen.Float(MaxListDepth)}
	kind := func(k string) *schemagen.Schema { return &schemagen.Schema{Const: k} }
	variant := func(name, description string, props map[string]*schemagen.Schema, required ...string) *schemagen.Schema {
		return g.Define(name, func() *schemagen.Schema {
			return schemagen.Describe(g.Object(props, append(required, "kind")...), description)
		})
	}
	return schemagen.Describe(&schemagen.Schema{OneOf: []*schemagen.Schema{
		variant("SegmentText", "Text.", map[string]*schemagen.Schema{
			"href": href, "kind": kind(SegmentText), "marks": marks,
			"value": {Type: "string", MinLength: schemagen.Int(1), Description: "The text."},
		}, "marks", "value"),
		variant("SegmentMention", "A mention of a component.", map[string]*schemagen.Schema{
			"href": href, "kind": kind(SegmentMention), "marks": marks, "ref": g.ID(ids.Component),
			"value": {Type: "string", Description: "The component's name when the snapshot was published."},
		}, "marks", "ref", "value"),
		variant("SegmentVariable", "A variable's value, which the text shows as the runtime renders it (FR-TEXT-02).",
			map[string]*schemagen.Schema{
				// A value of a variable has no attribute, by which a renderer
				// tells it from a value of an attribute.
				"attribute": schemagen.False(), "decimals": decimalsSchema(), "href": href,
				"kind": kind(SegmentValue), "marks": marks, "name": valueNameSchema(), "ref": g.ID(ids.Variable),
			}, "marks", "name", "ref"),
		variant("SegmentAttribute", "A component's runtime attribute's value, which the text shows as the runtime "+
			"renders it (FR-TEXT-02).", map[string]*schemagen.Schema{
			"attribute": g.ID(ids.AttributeDef), "decimals": decimalsSchema(), "href": href,
			"kind": kind(SegmentValue), "marks": marks, "name": valueNameSchema(), "ref": g.ID(ids.Component),
		}, "attribute", "marks", "name", "ref"),
		variant("SegmentBreak", "A line break.", map[string]*schemagen.Schema{"kind": kind(SegmentBreak)}),
		variant("SegmentParagraph", "A paragraph begins.", map[string]*schemagen.Schema{
			"depth": schemagen.Describe(cloneSchema(depth), "The depth of the list item that the paragraph belongs "+
				"to, or none for a paragraph of no item."),
			"kind": kind(SegmentParagraph),
		}),
		variant("SegmentBulletItem", "An item of a bulleted list begins.", map[string]*schemagen.Schema{
			"depth": schemagen.Describe(cloneSchema(depth), "The item's depth, 1 for an item of a list that no list "+
				"holds."),
			"kind": kind(SegmentItem), "list": kind(ListBullet), "number": schemagen.False(),
		}, "depth", "list"),
		variant("SegmentOrderedItem", "An item of a numbered list begins.", map[string]*schemagen.Schema{
			"depth": schemagen.Describe(cloneSchema(depth), "The item's depth, 1 for an item of a list that no list "+
				"holds."),
			"kind": kind(SegmentItem), "list": kind(ListOrdered),
			"number": {Type: "integer", Minimum: schemagen.Float(0), Maximum: schemagen.Float(semantics.MaxInt),
				Description: "The item's number."},
		}, "depth", "list", "number"),
	}}, "A segment of a rich text.")
}

var (
	reflectSegment = reflect.TypeFor[Segment]()
	hrefRegexp     = regexp.MustCompile(hrefPattern)
)

func decimalsSchema() *schemagen.Schema {
	return &schemagen.Schema{
		Type: "integer", Minimum: schemagen.Float(0), Maximum: schemagen.Float(MaxDecimals),
		Description: "The number of digits after the point that a number is shown with, rounded half away from " +
			"zero. A value that is not a number is shown as it is.",
	}
}

func valueNameSchema() *schemagen.Schema {
	return &schemagen.Schema{Type: "string", Description: "The value's name as an expression names it, as " +
		"met_elara or elara.trust, for debugging: the segment reads the value by its ref."}
}

func cloneSchema(s *schemagen.Schema) *schemagen.Schema {
	c := *s
	return &c
}

// segmentFields are the fields of each kind of segment, its required ones
// and its optional ones, as MarshalJSON writes them.
var segmentFields = map[string][2][]string{
	SegmentText:      {{"marks", "value"}, {"href"}},
	SegmentMention:   {{"marks", "ref", "value"}, {"href"}},
	SegmentValue:     {{"marks", "name", "ref"}, {"attribute", "decimals", "href"}},
	SegmentBreak:     {},
	SegmentParagraph: {nil, {"depth"}},
	SegmentItem:      {{"depth", "list"}, {"number"}},
}

// UnmarshalJSON reads a segment as the published format holds it, as
// MarshalJSON writes it, and as its schema takes it (Segment.JSONSchema): it
// fails on a kind that it does not know, as a consumer does (PRD §6.4), on a
// field of its kind that is missing or of another type, and on a value that
// the schema does not take, as an ID that is not in its canonical spelling;
// and it ignores a field that its kind does not have, as one of another kind,
// as a consumer ignores a key that it does not know (§6.4).
func (s *Segment) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("richtext: a segment is not an object")
	}
	var kind string
	if r, ok := raw["kind"]; !ok || json.Unmarshal(r, &kind) != nil {
		return fmt.Errorf("richtext: a segment has no kind, or one that is not a string")
	}
	fields, ok := segmentFields[kind]
	if !ok {
		return fmt.Errorf("richtext: %q is no kind of segment", kind)
	}
	*s = Segment{Kind: kind}
	for _, f := range fields[0] {
		if _, ok := raw[f]; !ok {
			return fmt.Errorf("richtext: a %s segment has no %s", kind, f)
		}
	}
	// get reads the field f of the segment's kind into v, and reports whether
	// it is there.
	get := func(f string, v any) (bool, error) {
		r, ok := raw[f]
		if !ok || !slices.Contains(fields[0], f) && !slices.Contains(fields[1], f) {
			return false, nil
		}
		if err := json.Unmarshal(r, v); err != nil {
			return false, fmt.Errorf("richtext: the %s of a %s segment is of another type than its kind's", f, kind)
		}
		return true, nil
	}
	var (
		marks                        []string
		href, value, name, ref, attr string
		list                         string
		decimals, depth, number      integer
	)
	read := []struct {
		f string
		v any
	}{{"marks", &marks}, {"href", &href}, {"value", &value}, {"name", &name}, {"ref", &ref},
		{"attribute", &attr}, {"list", &list}, {"decimals", &decimals}, {"depth", &depth}, {"number", &number}}
	present := map[string]bool{}
	for _, r := range read {
		ok, err := get(r.f, r.v)
		if err != nil {
			return err
		}
		present[r.f] = ok
	}
	if present["marks"] {
		if len(marks) > len(markOrder) {
			return fmt.Errorf("richtext: a segment has more than %d marks", len(markOrder))
		}
		for _, m := range marks {
			if !slices.Contains(markOrder, m) {
				return fmt.Errorf("richtext: %q is no mark", m)
			}
		}
		s.Marks = marks
	}
	if present["href"] {
		// The schema's pattern, rather than CheckHref, which a later version
		// may tighten, so that a published snapshot stays readable.
		if n := utf8.RuneCountInString(href); n == 0 || n > MaxHrefChars || !hrefRegexp.MatchString(href) {
			return fmt.Errorf("richtext: a segment's href is not the URL of a link")
		}
		s.Href = href
	}
	if kind == SegmentText && value == "" {
		return fmt.Errorf("richtext: a text segment's value is empty")
	}
	s.Value, s.Name = value, name
	if present["ref"] {
		k := ids.Component
		if kind == SegmentValue && !present["attribute"] {
			k = ids.Variable
		}
		if !ids.IsCanonical(k, ref) {
			return fmt.Errorf("richtext: a %s segment's ref is not the ID of a %s in its canonical spelling", kind, k)
		}
		s.Ref = ref
	}
	if present["attribute"] {
		if !ids.IsCanonical(ids.AttributeDef, attr) {
			return fmt.Errorf("richtext: a value segment's attribute is not an attribute definition's ID in its " +
				"canonical spelling")
		}
		s.Attribute = attr
	}
	if present["decimals"] {
		if decimals < 0 || decimals > MaxDecimals {
			return fmt.Errorf("richtext: a value segment's decimals are not from 0 to %d", MaxDecimals)
		}
		d := int(decimals)
		s.Decimals = &d
	}
	if present["depth"] {
		if depth < 1 || depth > MaxListDepth {
			return fmt.Errorf("richtext: a segment's depth is not from 1 to %d", MaxListDepth)
		}
		s.Depth = int(depth)
	}
	if kind == SegmentItem {
		switch {
		case list == ListBullet && !present["number"]:
		case list == ListOrdered && present["number"] && number >= 0:
			s.Number = int(number)
		default:
			return fmt.Errorf("richtext: an item segment's list is %q, which is no kind of list, or it has a "+
				"number exactly when its list is not ordered, or a negative one", list)
		}
		s.List = list
	}
	return nil
}

// integer is an integer of a segment, as its decimals, which a consumer
// reads as an integer by its declaration, whatever its JSON form, as 2.0 is
// 2 (PRD §5.8.9), and which a JSON Schema's integer takes so.
type integer int

// UnmarshalJSON reads an integral JSON number, however it is written, of at
// most semantics.MaxInt in magnitude, as an integer.
func (n *integer) UnmarshalJSON(data []byte) error {
	var f float64
	if err := json.Unmarshal(data, &f); err != nil || f != math.Trunc(f) || math.Abs(f) > semantics.MaxInt {
		return fmt.Errorf("richtext: %s is not an integer of at most %d in magnitude", data, semantics.MaxInt)
	}
	*n = integer(f)
	return nil
}
