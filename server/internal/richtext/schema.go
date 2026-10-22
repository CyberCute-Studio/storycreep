package richtext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// SchemaID is the $id of version 1 of the rich-text JSON Schema: the URL at
// which the repository publishes it, which names the version, so that a
// consumer can pin it (PRD FR-DATA-04).
const SchemaID = "https://raw.githubusercontent.com/CyberCute-Studio/storycreep/main/schema/rich-text/v1/rich-text.schema.json"

// SchemaPath is where the repository holds the schema, from its root.
const SchemaPath = "schema/rich-text/v1/rich-text.schema.json"

// Schema returns the published JSON Schema of a document (PRD FR-TEXT-01
// AC1), as SchemaPath holds it, with the schema's examples. Every document
// that Parse accepts validates against it. Parse also refuses what JSON
// Schema does not express: lists nested deeper than MaxListDepth, a document
// over MaxBytes or MaxChars, a list item that does not start with a
// paragraph, a mark twice, the code mark with another, a link that CheckHref
// refuses beyond the schema's pattern, and a string that escapes a lone
// surrogate; and what the schema could say only in keywords that the API's
// tooling does not read, attrs on a bulletList.
//
// A validator's work on the schema grows with a document's size, whatever
// the document holds, as the API's validation of a request needs (PRD
// NFR-SEC-07): of the branches of each oneOf, only one holds blocks, so that
// a validator, which tries every branch, does not read a list's content once
// for each kind of list, and twice as often at each depth; and no array is
// uniqueItems, which huma checks in time that grows with the square of the
// array's length. The schema has only the keywords that package apicontract
// reads, so that the API's OpenAPI document, which holds it for a Doc field,
// loads and generates the TypeScript client's types.
func Schema() []byte {
	examples := make([]any, len(schemaExamples))
	for i, ex := range schemaExamples {
		if err := json.Unmarshal([]byte(ex), &examples[i]); err != nil {
			panic("richtext: an example of the schema is not JSON: " + err.Error())
		}
	}
	s := obj{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     SchemaID,
		"title":   "Storycreep rich text",
		"description": "A rich-text document of Storycreep, version 1: a ProseMirror document of a constrained schema, " +
			"which node bodies, connection labels, and component descriptions hold. schema/rich-text/README.md in the " +
			"repository describes it, the limits that the server checks beyond this schema, and what is derived from it.",
		"$ref":     defsPrefix + "doc",
		"$defs":    definitions(defsPrefix),
		"examples": examples,
	}
	compact, err := marshalNoEscape(s)
	if err != nil {
		panic("richtext: the schema is not JSON: " + err.Error())
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, compact, "", "  "); err != nil {
		panic("richtext: the schema is not JSON: " + err.Error())
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}

type obj = map[string]any

// defsPrefix is how the schema refers to its definitions.
const defsPrefix = "#/$defs/"

// Schema gives huma the published JSON Schema of a document, so that the API
// validates a request's body, and the commands' pipeline a payload, whose
// field holds a Doc against it before UnmarshalJSON reads the field, as it
// validates the rest of the body, and so that the OpenAPI document describes
// the field (huma.SchemaProvider). The field refers to the document's schema,
// which huma's registry holds as RichTextDoc, with each of the schema's
// definitions beside it, as RichTextBlock, once however many fields refer to
// it.
//
// huma passes over a null in a property that a schema does not require, as
// it does in any body, where JSON Schema checks it against the property's
// schema, so a null in an optional field of a document, as a valueRef's
// decimals, reaches UnmarshalJSON, which refuses it with WrongType.
func (Doc) Schema(r huma.Registry) *huma.Schema {
	return r.Schema(reflect.TypeFor[richTextDoc](), true, "")
}

// richTextDoc stands for a document in huma's registry, which names it after
// the type and gives it the schema of a document (huma.SchemaTransformer).
type richTextDoc struct{}

// TransformSchema returns the schema of a document, and registers the
// definitions that it refers to beside it, each under the document's name
// with the definition's in place of Doc.
func (richTextDoc) TransformSchema(r huma.Registry, _ *huma.Schema) *huma.Schema {
	// huma has registered the type, so that this gives the reference to it.
	ref := r.Schema(reflect.TypeFor[richTextDoc](), true, "").Ref
	at := strings.LastIndexByte(ref, '/') + 1
	c := humaConverter{r: r, prefix: ref[:at], base: strings.TrimSuffix(ref[at:], "Doc")}
	return c.schema(schemaDefinitions()["doc"])
}

// schemaDefinitions returns the schema's definitions as JSON decodes them.
var schemaDefinitions = sync.OnceValue(func() map[string]any {
	data, err := json.Marshal(definitions(defsPrefix))
	if err != nil {
		panic("richtext: the schema is not JSON: " + err.Error())
	}
	var defs map[string]any
	if err := json.Unmarshal(data, &defs); err != nil {
		panic("richtext: the schema is not JSON: " + err.Error())
	}
	return defs
})

// humaConverter converts the schema's definitions into huma's, in registry r,
// in which a reference starts with prefix, and a definition's name with base.
type humaConverter struct {
	r            huma.Registry
	prefix, base string
}

// ref returns a reference to the definition name, which it registers in the
// registry, with the definitions that it refers to, unless the registry has
// it. It writes the definitions into the registry's map, as no Go type stands
// for them, so it panics when a Go type that huma registered holds the
// definition's name, whose schema the reference would otherwise lead to, or
// when the registry does not keep what is written into its map. (huma panics
// at a Go type that it registers under a name that a definition holds.)
func (c humaConverter) ref(name string) *huma.Schema {
	key := c.base + strings.ToUpper(name[:1]) + name[1:]
	schemas := c.r.Map()
	if _, ok := schemas[key]; !ok {
		def, ok := schemaDefinitions()[name]
		if !ok {
			panic("richtext: the schema has no definition " + name)
		}
		// A definition that refers to itself, as a block does through a
		// list, finds itself registered.
		schemas[key] = &huma.Schema{}
		if c.r.SchemaFromRef(c.prefix+key) == nil {
			// It would not find itself registered either, and recur.
			panic("richtext: the registry does not keep the schema " + key + " that is written into its map")
		}
		schemas[key] = c.schema(def)
	} else if t := c.r.TypeFromRef(c.prefix + key); t != nil {
		panic(fmt.Sprintf("richtext: the registry names the schema of %s %s, which is the name of the rich-text "+
			"schema's definition %s; give the type another name", t, key, name))
	}
	return &huma.Schema{Ref: c.prefix + key}
}

// schema converts the schema v, as JSON decodes it, into huma's. It panics
// on a keyword that it does not convert, so that a keyword that huma would
// not check, or that package apicontract would not read in the API's OpenAPI
// document, as "not", cannot pass unnoticed.
func (c humaConverter) schema(v any) *huma.Schema {
	m, ok := v.(map[string]any)
	if !ok {
		panic(fmt.Sprintf("richtext: a schema is not an object: %v", v))
	}
	if ref, ok := m["$ref"].(string); ok {
		if len(m) > 1 {
			panic("richtext: a schema has a $ref and other keywords")
		}
		return c.ref(strings.TrimPrefix(ref, defsPrefix))
	}
	s := &huma.Schema{}
	intOf := func(x any) *int { n := int(x.(float64)); return &n }
	floatOf := func(x any) *float64 { f := x.(float64); return &f }
	for k, x := range m {
		switch k {
		case "type":
			s.Type = x.(string)
		case "description":
			s.Description = x.(string)
		case "const":
			s.Const = x
		case "enum":
			s.Enum = x.([]any)
		case "pattern":
			s.Pattern = x.(string)
		case "minLength":
			s.MinLength = intOf(x)
		case "maxLength":
			s.MaxLength = intOf(x)
		case "minItems":
			s.MinItems = intOf(x)
		case "maxItems":
			s.MaxItems = intOf(x)
		case "minimum":
			s.Minimum = floatOf(x)
		case "maximum":
			s.Maximum = floatOf(x)
		case "additionalProperties":
			s.AdditionalProperties = x.(bool)
		case "required":
			for _, req := range x.([]any) {
				s.Required = append(s.Required, req.(string))
			}
		case "items":
			s.Items = c.schema(x)
		case "properties":
			s.Properties = map[string]*huma.Schema{}
			for name, p := range x.(map[string]any) {
				s.Properties[name] = c.schema(p)
			}
		case "oneOf":
			for _, o := range x.([]any) {
				s.OneOf = append(s.OneOf, c.schema(o))
			}
		default:
			panic("richtext: the schema has the keyword " + k + ", which the conversion to huma does not convert")
		}
	}
	s.PrecomputeMessages()
	return s
}

// The patterns of the schema, which Go's regular expressions and
// ECMAScript's read alike: an escape \xNN for the control characters, and
// the line and paragraph separators as they are, which JSON then escapes.
var (
	// textPattern matches text without a control character (isControl).
	textPattern = `^[^\x00-\x1f\x7f-\x9f` + string(rune(lineSeparator)) + string(rune(paragraphSeparator)) + `]+$`
	// hrefPattern matches an http or https URL with a host or a mailto URL
	// with an address, without a space or a control character, which
	// CheckHref checks more strictly.
	hrefPattern = `^(?:[Hh][Tt][Tt][Pp][Ss]?://[^\x00-\x20\x7f-\x9f/?#]|[Mm][Aa][Ii][Ll][Tt][Oo]:[^\x00-\x20\x7f-\x9f])` +
		`[^\x00-\x20\x7f-\x9f]*$`
)

// definitions returns the schema's definitions, each referring to the others
// by prefix and its name.
func definitions(prefix string) obj {
	ref := func(name string) obj { return obj{"$ref": prefix + name} }
	typ := func(name string) obj { return obj{"const": name} }
	nodes := func(item obj, minItems int) obj {
		a := obj{"type": "array", "items": item}
		if minItems > 0 {
			a["minItems"] = minItems
		}
		return a
	}
	mark := func(name, description string) obj {
		return describe(object(obj{"type": typ(name)}, "type"), description)
	}
	// Both kinds of list are one definition, whose content is read once. That
	// a bulletList has no attrs is the server's to check: a schema that says
	// so needs a oneOf beside the list's properties and a "not", which
	// package apicontract, which reads the API's OpenAPI document that holds
	// this schema for a Doc field, does not read.
	list := object(obj{
		"attrs": describe(object(obj{"start": describe(obj{"type": "integer", "minimum": 0, "maximum": MaxStart},
			"The number of the first item, 1 when it is not given.")}),
			"An orderedList's attributes. A bulletList has none, which the server checks beyond this schema."),
		"content": nodes(ref("listItem"), 1),
		"type":    obj{"enum": []string{nodeBulletList, nodeOrderedList}},
	}, "content", "type")
	return obj{
		"doc": describe(object(obj{"content": nodes(ref("block"), 1), "type": typ(nodeDoc)}, "content", "type"),
			"A document: its blocks, at least one. The editor's empty document is one empty paragraph."),
		"block": obj{"oneOf": []obj{ref("paragraph"), ref("list")}},
		"paragraph": describe(object(obj{"content": nodes(ref("inline"), 0), "type": typ(nodeParagraph)}, "type"),
			"A paragraph, which may be empty."),
		"list": describe(list, "A list, bulleted (bulletList) or numbered (orderedList), of at least one item. "+
			fmt.Sprintf("Lists nest at most %d deep, which the server checks beyond this schema.", MaxListDepth)),
		"listItem": describe(object(obj{
			"content": nodes(ref("block"), 1),
			"type":    typ(nodeListItem),
		}, "content", "type"), "A list item: a paragraph, then any paragraphs and lists. That it starts with a "+
			"paragraph is checked by the server, beyond this schema."),
		"inline": obj{"oneOf": []obj{ref("text"), ref("hardBreak"), ref("mention"), ref("valueRef")}},
		"text": describe(object(obj{
			"marks": ref("marks"),
			"text":  obj{"type": "string", "minLength": 1, "pattern": textPattern},
			"type":  typ(nodeText),
		}, "text", "type"), "Text, without control characters or line and paragraph separators: a line break is a "+
			"hardBreak, and a paragraph a paragraph. "+
			fmt.Sprintf("A document's texts hold at most %s characters together.", thousands(MaxChars))),
		"hardBreak": describe(object(obj{"marks": ref("marks"), "type": typ(nodeHardBreak)}, "type"),
			"A line break within a paragraph."),
		"mention": describe(object(obj{
			"attrs": object(obj{"id": ref("componentId")}, "id"),
			"marks": ref("marks"),
			"type":  typ(nodeMention),
		}, "attrs", "type"), "A mention of a component, which the text shows by the component's name."),
		"valueRef": describe(object(obj{
			"attrs": obj{"oneOf": []obj{
				object(obj{"decimals": ref("decimals"), "ref": ref("variableId")}, "ref"),
				object(obj{"attribute": ref("attributeId"), "decimals": ref("decimals"), "ref": ref("componentId")}, "attribute", "ref"),
			}},
			"marks": ref("marks"),
			"type":  typ(nodeValueRef),
		}, "attrs", "type"), "A reference to a value, which the text shows as the runtime renders it: a variable's, "+
			"or a runtime attribute's of a component."),
		"decimals": describe(obj{"type": "integer", "minimum": 0, "maximum": MaxDecimals},
			"The number of digits after the point that a number is shown with, rounded half away from zero. "+
				"A value that is not a number is shown as it is."),
		"marks": describe(obj{
			"type": "array", "maxItems": len(markOrder),
			"items": obj{"oneOf": []obj{ref(markBold), ref(markCode), ref(markItalic), ref(markLink), ref(markStrike)}},
		}, "The marks of an inline node, each at most once, which the server checks beyond this schema. The code "+
			"mark is not combined with another."),
		markBold:   mark(markBold, "Bold."),
		markCode:   mark(markCode, "Inline code."),
		markItalic: mark(markItalic, "Italic."),
		markStrike: mark(markStrike, "Strikethrough."),
		markLink: describe(object(obj{
			"attrs": object(obj{"href": obj{
				"type": "string", "minLength": 1, "maxLength": MaxHrefChars,
				"pattern": hrefPattern,
			}}, "href"),
			"type": typ(markLink),
		}, "attrs", "type"), "A link to an absolute http or https URL with a host, or a mailto URL, without white space."),
		"componentId": id(ids.Component, "A component's ID."),
		"variableId":  id(ids.Variable, "A variable's ID."),
		"attributeId": id(ids.AttributeDef, "A component type's attribute definition's ID."),
	}
}

// object returns the schema of an object with properties and no other, of
// which the required ones must be there.
func object(properties obj, required ...string) obj {
	o := obj{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func describe(schema obj, description string) obj {
	schema["description"] = description
	return schema
}

// thousands writes n, which is not negative, with a comma between each group
// of three digits, as 100,000.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// id returns the schema of a public ID of the entity type k, as package ids
// decodes one.
func id(k ids.Kind, description string) obj {
	return describe(obj{"type": "string", "pattern": k.Pattern()}, description)
}

// schemaExamples are the schema's examples, in their canonical form.
var schemaExamples = []string{
	// The editor's empty document.
	`{"content":[{"type":"paragraph"}],"type":"doc"}`,
	// The node of the PRD's example (§6.5).
	`{"content":[{"content":[{"text":"Harbour's closed to strangers. State your business.","type":"text"}],"type":"paragraph"}],"type":"doc"}`,
	// Marks and a link.
	`{"content":[{"content":[{"text":"Ask the ","type":"text"},{"marks":[{"type":"bold"}],"text":"keeper","type":"text"},` +
		`{"text":" about the ","type":"text"},{"marks":[{"type":"italic"},{"attrs":{"href":"https://example.com/lighthouse"},"type":"link"}],` +
		`"text":"old lighthouse","type":"text"},{"text":", not the ","type":"text"},{"marks":[{"type":"strike"}],"text":"new","type":"text"},` +
		`{"text":" one: ","type":"text"},{"marks":[{"type":"code"}],"text":"LIGHT_ON","type":"text"},{"text":".","type":"text"}],"type":"paragraph"}],"type":"doc"}`,
	// A mention, value references, and a line break.
	`{"content":[{"content":[{"attrs":{"id":"cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF"},"type":"mention"},{"text":" has heard ","type":"text"},` +
		`{"attrs":{"ref":"var_01HQ8Z5C2K8W1D5J9NTQZR4YBE"},"type":"valueRef"},{"text":" rumours.","type":"text"},{"type":"hardBreak"},` +
		`{"text":"Trust: ","type":"text"},{"attrs":{"attribute":"atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF","decimals":1,"ref":"cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF"},` +
		`"marks":[{"type":"bold"}],"type":"valueRef"}],"type":"paragraph"}],"type":"doc"}`,
	// Lists, one in the other.
	`{"content":[{"content":[{"text":"Pack for the climb:","type":"text"}],"type":"paragraph"},{"content":[` +
		`{"content":[{"content":[{"text":"Rope","type":"text"}],"type":"paragraph"}],"type":"listItem"},` +
		`{"content":[{"content":[{"text":"Lamp","type":"text"}],"type":"paragraph"},{"attrs":{"start":3},"content":[` +
		`{"content":[{"content":[{"text":"Oil","type":"text"}],"type":"paragraph"}],"type":"listItem"},` +
		`{"content":[{"content":[{"text":"Wick","type":"text"}],"type":"paragraph"}],"type":"listItem"}],"type":"orderedList"}],"type":"listItem"}],` +
		`"type":"bulletList"},{"content":[{"text":"Then go.","type":"text"}],"type":"paragraph"}],"type":"doc"}`,
}
