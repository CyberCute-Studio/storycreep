package richtext

import (
	"bytes"
	"encoding/json"
	"strings"
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
// over MaxBytes or MaxChars, a mark twice, the code mark with another, and a
// link whose URL is not one (CheckHref).
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
		"$ref":     "#/$defs/doc",
		"$defs":    definitions("#/$defs/"),
		"examples": examples,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		panic("richtext: the schema is not JSON: " + err.Error())
	}
	return buf.Bytes()
}

type obj = map[string]any

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
	return obj{
		"doc": describe(object(obj{"content": nodes(ref("block"), 1), "type": typ(nodeDoc)}, "content", "type"),
			"A document: its blocks, at least one. The editor's empty document is one empty paragraph."),
		"block": obj{"oneOf": []obj{ref("paragraph"), ref("bulletList"), ref("orderedList")}},
		"paragraph": describe(object(obj{"content": nodes(ref("inline"), 0), "type": typ(nodeParagraph)}, "type"),
			"A paragraph, which may be empty."),
		"bulletList": describe(object(obj{"content": nodes(ref("listItem"), 1), "type": typ(nodeBulletList)}, "content", "type"),
			"A bulleted list. Lists nest at most 5 deep."),
		"orderedList": describe(object(obj{
			"attrs": object(obj{"start": describe(obj{"type": "integer", "minimum": 0, "maximum": MaxStart},
				"The number of the first item, 1 when it is not given.")}),
			"content": nodes(ref("listItem"), 1),
			"type":    typ(nodeOrderedList),
		}, "content", "type"), "A numbered list. Lists nest at most 5 deep."),
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
			"hardBreak, and a paragraph a paragraph. A document's texts hold at most 100,000 characters together."),
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
			"type": "array", "maxItems": len(markOrder), "uniqueItems": true,
			"items": obj{"oneOf": []obj{ref(markBold), ref(markCode), ref(markItalic), ref(markLink), ref(markStrike)}},
		}, "The marks of an inline node, each at most once. The code mark is not combined with another."),
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
		"componentId": id("cmp", "A component's ID."),
		"variableId":  id("var", "A variable's ID."),
		"attributeId": id("atr", "A component type's attribute definition's ID."),
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

// id returns the schema of a public ID with the type prefix prefix, which is
// read regardless of case, as package ids decodes it.
func id(prefix, description string) obj {
	var b strings.Builder
	b.WriteByte('^')
	for _, c := range prefix {
		b.WriteString("[" + strings.ToUpper(string(c)) + string(c) + "]")
	}
	b.WriteString("_[0-7][0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{25}$")
	return describe(obj{"type": "string", "pattern": b.String()}, description)
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
