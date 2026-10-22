// Package richtext keeps the rich text of a project's content: the bodies of
// nodes, the labels of connections, and the descriptions of components (PRD
// FR-TEXT-01, ADR-020). A document is a ProseMirror document of a constrained
// schema, the one that the editor's TipTap extensions use, stored as JSON.
// Parse checks a document against the schema and its limits (NFR-SEC-07) and
// returns it in its canonical form, and a document derives the segments of
// the published format, its plain text, a preview, a word count, and a hash
// (derive.go).
//
// schema/rich-text/README.md describes the document, its limits, and its
// derivations, which the TypeScript client implements again, and
// schema/rich-text/vectors.json holds both implementations to the same
// answers. Schema generates the published JSON Schema
// (schema/rich-text/v1/rich-text.schema.json).
package richtext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// The limits of a document (PRD NFR-SEC-07). The JSON Schema states those
// that it can express.
const (
	// MaxBytes is the most bytes of JSON that Parse reads as one document.
	MaxBytes = 1 << 20
	// MaxChars is the most characters, counted as code points, that the
	// text nodes of a document hold together.
	MaxChars = 100_000
	// MaxListDepth is how deeply lists nest: a list at the top of the
	// document is at depth 1, and a list in one of its items at depth 2.
	MaxListDepth = 5
	// MaxHrefChars is the longest URL of a link, in code points.
	MaxHrefChars = 2048
	// MaxStart is the highest number that an ordered list starts at.
	MaxStart = 999_999
	// MaxDecimals is the most digits after the point that a value reference
	// shows a number with.
	MaxDecimals = 6
)

// Doc is a document: its blocks, of which it has at least one. The editor's
// empty document is one empty paragraph.
type Doc struct {
	Blocks []Block
}

// Block is a block of a document or of a list item: a *Paragraph or a *List.
type Block interface{ block() }

// Paragraph is a paragraph, which may be empty.
type Paragraph struct {
	Inlines []Inline
}

// List is a bulleted list, or a numbered one when Ordered, of at least one
// item.
type List struct {
	Ordered bool
	// Start is the number of an ordered list's first item, which is 1 unless
	// the list says otherwise. A bulleted list's is 1.
	Start int
	Items []Item
}

// Item is an item of a list: a paragraph, then any blocks, so Blocks[0] is
// always a *Paragraph.
type Item struct {
	Blocks []Block
}

func (*Paragraph) block() {}
func (*List) block()      {}

// Inline is the content of a paragraph: a *Text, a *Break, a *Mention, or a
// *Value.
type Inline interface{ inline() }

// Text is text, at least one character of it, with its marks. A paragraph
// never holds two texts with the same marks side by side, which its canonical
// form joins.
type Text struct {
	Text  string
	Marks Marks
}

// Break is a line break within a paragraph. It has marks when the text around
// it had them as it was pasted, which nothing derived from it shows.
type Break struct {
	Marks Marks
}

// Mention is a component, which the text shows by its name (FR-TEXT-03).
type Mention struct {
	// Component is the component's public ID, in its canonical spelling.
	Component string
	Marks     Marks
}

// Value is a reference to a value, which the text shows as the runtime
// renders it (FR-TEXT-02): a variable's, or a runtime attribute's of a
// component.
type Value struct {
	// Ref is the public ID of the variable, or of the component when
	// Attribute is set, in its canonical spelling.
	Ref string
	// Attribute is the public ID of the component's attribute definition,
	// or "" for a variable.
	Attribute string
	// Decimals is the number of digits after the point that a number is
	// shown with, rounded half away from zero (§5.8.9), or nil to show the
	// value as the runtime renders it. A value that is not a number is shown
	// as it is.
	Decimals *int
	Marks    Marks
}

func (*Text) inline()    {}
func (*Break) inline()   {}
func (*Mention) inline() {}
func (*Value) inline()   {}

// Marks are the marks of an inline node: its formatting, and the link that it
// is part of. Code is never combined with another mark, as the editor's code
// mark excludes them.
type Marks struct {
	Bold, Code, Italic, Strike bool
	// Link is the URL of the link that the node is part of, or "".
	Link string
}

// names returns the names of the marks in alphabetical order, as segments
// list them, and as the canonical form orders them.
func (m Marks) names() []string {
	names := make([]string, 0, 5)
	for _, mk := range markOrder {
		if m.has(mk) {
			names = append(names, mk)
		}
	}
	return names
}

func (m Marks) has(name string) bool {
	switch name {
	case markBold:
		return m.Bold
	case markCode:
		return m.Code
	case markItalic:
		return m.Italic
	case markLink:
		return m.Link != ""
	case markStrike:
		return m.Strike
	}
	return false
}

// The names of the schema's nodes and marks, which are TipTap's.
const (
	nodeDoc         = "doc"
	nodeParagraph   = "paragraph"
	nodeBulletList  = "bulletList"
	nodeOrderedList = "orderedList"
	nodeListItem    = "listItem"
	nodeText        = "text"
	nodeHardBreak   = "hardBreak"
	nodeMention     = "mention"
	nodeValueRef    = "valueRef"

	markBold   = "bold"
	markCode   = "code"
	markItalic = "italic"
	markLink   = "link"
	markStrike = "strike"
)

// markOrder is every mark, in alphabetical order.
var markOrder = []string{markBold, markCode, markItalic, markLink, markStrike}

// nodeFields holds each node's fields other than type.
var nodeFields = map[string][]string{
	nodeDoc:         {"content"},
	nodeParagraph:   {"content"},
	nodeBulletList:  {"content"},
	nodeOrderedList: {"attrs", "content"},
	nodeListItem:    {"content"},
	nodeText:        {"marks", "text"},
	nodeHardBreak:   {"marks"},
	nodeMention:     {"attrs", "marks"},
	nodeValueRef:    {"attrs", "marks"},
}

// Reason says what is wrong with a document that Parse refuses. The values
// are those of schema/rich-text/vectors.json, and never change.
type Reason string

// The reasons.
const (
	// InvalidJSON is input that is not one JSON value in UTF-8.
	InvalidJSON Reason = "invalid_json"
	// WrongType is a value of another JSON type than the schema's, as a
	// string for an array, or a number that is not an integer.
	WrongType Reason = "wrong_type"
	// UnknownNode is a node whose type is not one of the schema's.
	UnknownNode Reason = "unknown_node"
	// UnknownMark is a mark whose type is not one of the schema's.
	UnknownMark Reason = "unknown_mark"
	// UnknownField is a field that the schema does not give a node, a mark,
	// or their attributes.
	UnknownField Reason = "unknown_field"
	// MissingField is a field that the schema requires and that is missing.
	MissingField Reason = "missing_field"
	// Content is a node where its parent may not hold it, as a list in a
	// paragraph, or a list item that does not start with a paragraph, or an
	// empty content that must hold a node.
	Content Reason = "content"
	// EmptyText is a text node without text.
	EmptyText Reason = "empty_text"
	// ControlCharacter is text with a control character, or a line or
	// paragraph separator, which a line break or a paragraph stands for
	// instead.
	ControlCharacter Reason = "control_character"
	// DuplicateMark is a mark that an inline node has twice.
	DuplicateMark Reason = "duplicate_mark"
	// ExcludedMark is the code mark combined with another mark.
	ExcludedMark Reason = "excluded_mark"
	// Link is a link whose URL is not an http, https, or mailto URL, or is
	// too long.
	Link Reason = "link"
	// Reference is an ID that is not one, or of another type of entity than
	// it must be, or a reference to a value that names an attribute when it
	// must not or does not when it must.
	Reference Reason = "reference"
	// OutOfRange is an integer outside its range, as an ordered list's start
	// above MaxStart.
	OutOfRange Reason = "out_of_range"
	// TooDeep is a list nested deeper than MaxListDepth.
	TooDeep Reason = "too_deep"
	// TooLarge is a document of more than MaxBytes, or whose text has more
	// than MaxChars characters.
	TooLarge Reason = "too_large"
)

// Error is what is wrong with a document that Parse refuses.
type Error struct {
	// Pointer is the JSON Pointer (RFC 6901) of the value that is wrong,
	// within the document: "" for the document itself, or for a limit of
	// the whole document.
	Pointer string
	// Reason says what is wrong.
	Reason Reason
	// Message says it in English, for programs and logs.
	Message string
}

func (e *Error) Error() string {
	at := e.Pointer
	if at == "" {
		at = "the document"
	}
	return "rich text: " + at + ": " + e.Message
}

// Parse reads a document from its JSON, checks it against the schema and the
// limits, and returns it in its canonical form. The error is always an
// *Error. Of a key that an object has twice, the last is read, as JSON.parse
// and PostgreSQL's jsonb read it, so that every reader of a document reads
// the same one; the API refuses such a body before it reads a document in it
// (FR-API-01 AC2).
func Parse(data []byte) (*Doc, error) {
	if len(data) > MaxBytes {
		return nil, &Error{Reason: TooLarge, Message: fmt.Sprintf("the document is longer than %d bytes", MaxBytes)}
	}
	if !utf8.Valid(data) {
		return nil, &Error{Reason: InvalidJSON, Message: "the document is not UTF-8"}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	// Numbers stay as they are written, so that an integer is checked as
	// one, whatever its size.
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, &Error{Reason: InvalidJSON, Message: "the document is not JSON: " + err.Error()}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, &Error{Reason: InvalidJSON, Message: "the document is followed by more JSON"}
	}
	var p parser
	return p.doc(v)
}

// UnmarshalJSON reads a document as Parse does, so that a request's or a
// payload's field can hold one.
func (d *Doc) UnmarshalJSON(data []byte) error {
	parsed, err := Parse(data)
	if err != nil {
		return err
	}
	*d = *parsed
	return nil
}

// parser reads a document from the value that encoding/json decoded it to.
type parser struct {
	// path holds the reference tokens of the pointer to the value being
	// read. Reading stops at the first error, so a function that fails
	// leaves the tokens it pushed for the error to name.
	path []string
	// chars counts the code points of the text nodes read so far.
	chars int
}

func (p *parser) push(token string) { p.path = append(p.path, token) }
func (p *parser) pop()              { p.path = p.path[:len(p.path)-1] }

func (p *parser) fail(reason Reason, format string, args ...any) *Error {
	var b strings.Builder
	for _, t := range p.path {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1"))
	}
	return &Error{Pointer: b.String(), Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// where is where a node is read, which decides the types that it may have.
type where int

const (
	atRoot      where = iota // the document itself
	inDoc                    // a block of the document
	inItemFirst              // the first block of a list item
	inItem                   // a later block of a list item
	inList                   // an item of a list
	inParagraph              // the content of a paragraph
)

// holds reports whether a node of type typ may be read at w.
func (w where) holds(typ string) bool {
	switch w {
	case atRoot:
		return typ == nodeDoc
	case inDoc, inItem:
		return typ == nodeParagraph || typ == nodeBulletList || typ == nodeOrderedList
	case inItemFirst:
		return typ == nodeParagraph
	case inList:
		return typ == nodeListItem
	case inParagraph:
		return typ == nodeText || typ == nodeHardBreak || typ == nodeMention || typ == nodeValueRef
	}
	return false
}

func (w where) holdsWhat() string {
	switch w {
	case atRoot:
		return "a document is a doc node"
	case inDoc:
		return "a document holds paragraphs and lists"
	case inItemFirst:
		return "a list item starts with a paragraph"
	case inItem:
		return "a list item holds paragraphs and lists"
	case inList:
		return "a list holds list items"
	}
	return "a paragraph holds text, line breaks, mentions, and value references"
}

// node reads the object of a node at w, and returns it with its type, once
// the type may be there and the object has no field that the type does not.
func (p *parser) node(v any, w where) (map[string]any, string, error) {
	o, ok := v.(map[string]any)
	if !ok {
		return nil, "", p.fail(WrongType, "a node is not an object")
	}
	t, ok := o["type"]
	if !ok {
		return nil, "", p.fail(MissingField, "the node has no type")
	}
	typ, ok := t.(string)
	if !ok {
		p.push("type")
		return nil, "", p.fail(WrongType, "the node's type is not a string")
	}
	if _, known := nodeFields[typ]; !known {
		return nil, "", p.fail(UnknownNode, "%.40q is not a node of the schema", typ)
	}
	if !w.holds(typ) {
		return nil, "", p.fail(Content, "%s, not a %s", w.holdsWhat(), typ)
	}
	if err := p.fields(o, "a "+typ, nodeFields[typ]); err != nil {
		return nil, "", err
	}
	return o, typ, nil
}

// fields checks that object o, of the node, mark, or attributes what, has no
// field but those allowed, and type, which every node and mark has.
func (p *parser) fields(o map[string]any, what string, allowed []string) error {
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if k != "type" && !slices.Contains(allowed, k) {
			p.push(k)
			return p.fail(UnknownField, "%s has no field %.40q", what, k)
		}
	}
	return nil
}

// attrsFields checks that the attributes object attrs of what has no field
// but those allowed. Unlike a node or a mark, it has no type.
func (p *parser) attrsFields(attrs map[string]any, what string, allowed ...string) error {
	if _, ok := attrs["type"]; ok {
		p.push("type")
		return p.fail(UnknownField, "%s has no field \"type\"", what)
	}
	return p.fields(attrs, what, allowed)
}

// content returns the content of node o, of type typ, which must have at
// least one node when required, and may be missing otherwise. It leaves
// "content" pushed for the caller, which pops it once it has read the nodes.
func (p *parser) content(o map[string]any, typ string, required bool) ([]any, error) {
	v, ok := o["content"]
	if !ok {
		if required {
			return nil, p.fail(MissingField, "a %s has no content", typ)
		}
		p.push("content")
		return nil, nil
	}
	p.push("content")
	a, ok := v.([]any)
	if !ok {
		return nil, p.fail(WrongType, "a %s's content is not an array", typ)
	}
	if required && len(a) == 0 {
		return nil, p.fail(Content, "a %s holds nothing", typ)
	}
	return a, nil
}

// attrs returns the attributes object of node o, of type typ, or nil when it
// has none and they are not required. It leaves "attrs" pushed when it
// returns an object.
func (p *parser) attrs(o map[string]any, typ string, required bool) (map[string]any, error) {
	v, ok := o["attrs"]
	if !ok {
		if required {
			return nil, p.fail(MissingField, "a %s has no attrs", typ)
		}
		return nil, nil
	}
	p.push("attrs")
	a, ok := v.(map[string]any)
	if !ok {
		return nil, p.fail(WrongType, "a %s's attrs is not an object", typ)
	}
	return a, nil
}

func (p *parser) doc(v any) (*Doc, error) {
	o, typ, err := p.node(v, atRoot)
	if err != nil {
		return nil, err
	}
	blocks, err := p.blocks(o, typ, inDoc, 0)
	if err != nil {
		return nil, err
	}
	return &Doc{Blocks: blocks}, nil
}

// blocks reads the blocks of a document, or of a list item when first is
// inItemFirst, at depth, the number of lists that hold them.
func (p *parser) blocks(o map[string]any, typ string, first where, depth int) ([]Block, error) {
	content, err := p.content(o, typ, true)
	if err != nil {
		return nil, err
	}
	blocks := make([]Block, 0, len(content))
	for i, v := range content {
		w := first
		if i > 0 && first == inItemFirst {
			w = inItem
		}
		p.push(strconv.Itoa(i))
		b, err := p.block(v, w, depth)
		if err != nil {
			return nil, err
		}
		p.pop()
		blocks = append(blocks, b)
	}
	p.pop()
	return blocks, nil
}

func (p *parser) block(v any, w where, depth int) (Block, error) {
	o, typ, err := p.node(v, w)
	if err != nil {
		return nil, err
	}
	if typ == nodeParagraph {
		return p.paragraph(o)
	}
	if depth >= MaxListDepth {
		return nil, p.fail(TooDeep, "lists nest more than %d deep", MaxListDepth)
	}
	l := &List{Ordered: typ == nodeOrderedList, Start: 1}
	if l.Ordered {
		attrs, err := p.attrs(o, typ, false)
		if err != nil {
			return nil, err
		}
		if attrs != nil {
			if err := p.attrsFields(attrs, "an orderedList's attrs", "start"); err != nil {
				return nil, err
			}
			if s, ok := attrs["start"]; ok {
				p.push("start")
				n, err := p.integer(s, "an ordered list's start", 0, MaxStart)
				if err != nil {
					return nil, err
				}
				l.Start = n
				p.pop()
			}
			p.pop()
		}
	}
	content, err := p.content(o, typ, true)
	if err != nil {
		return nil, err
	}
	for i, v := range content {
		p.push(strconv.Itoa(i))
		item, _, err := p.node(v, inList)
		if err != nil {
			return nil, err
		}
		blocks, err := p.blocks(item, nodeListItem, inItemFirst, depth+1)
		if err != nil {
			return nil, err
		}
		l.Items = append(l.Items, Item{Blocks: blocks})
		p.pop()
	}
	p.pop()
	return l, nil
}

// integer reads integer v, what, which must be between lo and hi.
func (p *parser) integer(v any, what string, lo, hi int) (int, error) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, p.fail(WrongType, "%s is not a number", what)
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err == nil && f != math.Trunc(f) {
		return 0, p.fail(WrongType, "%s is not an integer", what)
	}
	if err != nil || f < float64(lo) || f > float64(hi) {
		return 0, p.fail(OutOfRange, "%s is not between %d and %d", what, lo, hi)
	}
	return int(f), nil
}

func (p *parser) paragraph(o map[string]any) (*Paragraph, error) {
	content, err := p.content(o, nodeParagraph, false)
	if err != nil {
		return nil, err
	}
	para := &Paragraph{}
	for i, v := range content {
		p.push(strconv.Itoa(i))
		in, err := p.inline(v)
		if err != nil {
			return nil, err
		}
		p.pop()
		// The canonical form joins texts with the same marks, as
		// ProseMirror does.
		if t, ok := in.(*Text); ok && len(para.Inlines) > 0 {
			if prev, ok := para.Inlines[len(para.Inlines)-1].(*Text); ok && prev.Marks == t.Marks {
				prev.Text += t.Text
				continue
			}
		}
		para.Inlines = append(para.Inlines, in)
	}
	p.pop()
	return para, nil
}

func (p *parser) inline(v any) (Inline, error) {
	o, typ, err := p.node(v, inParagraph)
	if err != nil {
		return nil, err
	}
	var in Inline
	switch typ {
	case nodeText:
		in, err = p.text(o)
	case nodeHardBreak:
		in = &Break{}
	case nodeMention:
		in, err = p.mention(o)
	case nodeValueRef:
		in, err = p.value(o)
	}
	if err != nil {
		return nil, err
	}
	marks, err := p.marks(o)
	if err != nil {
		return nil, err
	}
	switch in := in.(type) {
	case *Text:
		in.Marks = marks
	case *Break:
		in.Marks = marks
	case *Mention:
		in.Marks = marks
	case *Value:
		in.Marks = marks
	}
	return in, nil
}

func (p *parser) text(o map[string]any) (*Text, error) {
	v, ok := o["text"]
	if !ok {
		return nil, p.fail(MissingField, "a text has no text")
	}
	p.push("text")
	s, ok := v.(string)
	switch {
	case !ok:
		return nil, p.fail(WrongType, "a text's text is not a string")
	case s == "":
		return nil, p.fail(EmptyText, "a text's text is empty")
	case strings.IndexFunc(s, isControl) >= 0:
		return nil, p.fail(ControlCharacter, "a text holds a control character; a line break is a hardBreak node")
	}
	p.chars += utf8.RuneCountInString(s)
	if p.chars > MaxChars {
		p.path = nil
		return nil, p.fail(TooLarge, "the document's text is longer than %d characters", MaxChars)
	}
	p.pop()
	return &Text{Text: s}, nil
}

// The characters that text and links may not hold besides the control
// characters (Cc) and, in links, white space.
const (
	lineSeparator      = 0x2028
	paragraphSeparator = 0x2029
	byteOrderMark      = 0xFEFF
)

// isControl reports whether text may not hold r: a control character, or a
// line or paragraph separator.
func isControl(r rune) bool {
	return unicode.IsControl(r) || r == lineSeparator || r == paragraphSeparator
}

func (p *parser) mention(o map[string]any) (*Mention, error) {
	attrs, err := p.attrs(o, nodeMention, true)
	if err != nil {
		return nil, err
	}
	if err := p.attrsFields(attrs, "a mention's attrs", "id"); err != nil {
		return nil, err
	}
	id, err := p.id(attrs, "id", "a mention", ids.Component)
	if err != nil {
		return nil, err
	}
	p.pop()
	return &Mention{Component: id}, nil
}

func (p *parser) value(o map[string]any) (*Value, error) {
	attrs, err := p.attrs(o, nodeValueRef, true)
	if err != nil {
		return nil, err
	}
	if err := p.attrsFields(attrs, "a valueRef's attrs", "attribute", "decimals", "ref"); err != nil {
		return nil, err
	}
	ref, err := p.id(attrs, "ref", "a value reference", ids.Variable, ids.Component)
	if err != nil {
		return nil, err
	}
	val := &Value{Ref: ref}
	_, named := attrs["attribute"]
	switch {
	case strings.HasPrefix(ref, ids.Variable.Prefix()+"_") && named:
		p.push("attribute")
		return nil, p.fail(Reference, "a reference to a variable names no attribute")
	case strings.HasPrefix(ref, ids.Component.Prefix()+"_") && !named:
		return nil, p.fail(Reference, "a reference to a component names its attribute")
	case named:
		if val.Attribute, err = p.id(attrs, "attribute", "a value reference's attribute", ids.AttributeDef); err != nil {
			return nil, err
		}
	}
	if d, ok := attrs["decimals"]; ok {
		p.push("decimals")
		n, err := p.integer(d, "a value reference's decimals", 0, MaxDecimals)
		if err != nil {
			return nil, err
		}
		val.Decimals = &n
		p.pop()
	}
	p.pop()
	return val, nil
}

// id reads the ID in field key of attrs, of what, which must be the ID of an
// entity of one of the kinds, and returns it in its canonical spelling.
func (p *parser) id(attrs map[string]any, key, what string, kinds ...ids.Kind) (string, error) {
	v, ok := attrs[key]
	if !ok {
		return "", p.fail(MissingField, "%s has no %s", what, key)
	}
	p.push(key)
	s, ok := v.(string)
	if !ok {
		return "", p.fail(WrongType, "%s's %s is not a string", what, key)
	}
	kind, u, err := ids.DecodeAny(s)
	if err != nil || !slices.Contains(kinds, kind) {
		names := make([]string, len(kinds))
		for i, k := range kinds {
			names[i] = k.String()
		}
		return "", p.fail(Reference, "%s's %s is not the ID of a %s", what, key, strings.Join(names, " or a "))
	}
	p.pop()
	return ids.Encode(kind, u), nil
}

func (p *parser) marks(o map[string]any) (Marks, error) {
	var m Marks
	v, ok := o["marks"]
	if !ok {
		return m, nil
	}
	p.push("marks")
	a, ok := v.([]any)
	if !ok {
		return m, p.fail(WrongType, "marks is not an array")
	}
	for i, v := range a {
		p.push(strconv.Itoa(i))
		mo, ok := v.(map[string]any)
		if !ok {
			return m, p.fail(WrongType, "a mark is not an object")
		}
		t, ok := mo["type"]
		if !ok {
			return m, p.fail(MissingField, "the mark has no type")
		}
		typ, ok := t.(string)
		if !ok {
			p.push("type")
			return m, p.fail(WrongType, "the mark's type is not a string")
		}
		if !slices.Contains(markOrder, typ) {
			return m, p.fail(UnknownMark, "%.40q is not a mark of the schema", typ)
		}
		var allowed []string
		if typ == markLink {
			allowed = []string{"attrs"}
		}
		if err := p.fields(mo, "a "+typ+" mark", allowed); err != nil {
			return m, err
		}
		if m.has(typ) {
			return m, p.fail(DuplicateMark, "the %s mark is there twice", typ)
		}
		switch typ {
		case markBold:
			m.Bold = true
		case markCode:
			m.Code = true
		case markItalic:
			m.Italic = true
		case markStrike:
			m.Strike = true
		case markLink:
			href, err := p.link(mo)
			if err != nil {
				return m, err
			}
			m.Link = href
		}
		if m.Code && len(m.names()) > 1 {
			return m, p.fail(ExcludedMark, "the code mark is not combined with other marks")
		}
		p.pop()
	}
	p.pop()
	return m, nil
}

// link returns the URL of link mark mo.
func (p *parser) link(mo map[string]any) (string, error) {
	attrs, err := p.attrs(mo, "link mark", true)
	if err != nil {
		return "", err
	}
	if err := p.attrsFields(attrs, "a link's attrs", "href"); err != nil {
		return "", err
	}
	v, ok := attrs["href"]
	if !ok {
		return "", p.fail(MissingField, "a link has no href")
	}
	p.push("href")
	href, ok := v.(string)
	if !ok {
		return "", p.fail(WrongType, "a link's href is not a string")
	}
	if problem := CheckHref(href); problem != "" {
		return "", p.fail(Link, "a link's href %s", problem)
	}
	p.pop()
	p.pop()
	return href, nil
}

// CheckHref says what is wrong with the URL of a link, or returns "". A link
// is an absolute http or https URL with a host, or a mailto URL with an
// address, of at most MaxHrefChars characters, none of them white space or a
// control character (PRD NFR-SEC-08), so that no link runs a script or
// reads a file when it is followed.
func CheckHref(href string) string {
	switch {
	case href == "":
		return "is empty"
	case utf8.RuneCountInString(href) > MaxHrefChars:
		return fmt.Sprintf("is longer than %d characters", MaxHrefChars)
	case strings.IndexFunc(href, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || r == byteOrderMark
	}) >= 0:
		return "holds white space or a control character"
	}
	u, err := url.Parse(href)
	if err != nil {
		return "is not a URL"
	}
	switch u.Scheme {
	case "http", "https":
		if u.Host == "" {
			return "has no host"
		}
	case "mailto":
		// The address is what is between the scheme and the query.
		if u.Opaque == "" {
			return "has no address"
		}
	default:
		return "is not an http, https, or mailto URL"
	}
	return ""
}

// MarshalJSON writes the document in its canonical form: its JSON as
// ProseMirror writes it, without a field that holds its default (an empty
// content or marks, or an ordered list's start of 1), with each inline node's
// marks in alphabetical order, the IDs in their canonical spelling, a
// paragraph's neighbouring texts with the same marks joined, and every
// object's keys sorted. It does not escape HTML, which an encoder that
// escapes it, as json.Marshal does, escapes in what it writes too.
func (d Doc) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(wireNode{Type: nodeDoc, Content: wireBlocks(d.Blocks)}); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// wireNode is a node as JSON holds it, with its fields in alphabetical order.
type wireNode struct {
	Attrs   any        `json:"attrs,omitempty"`
	Content []wireNode `json:"content,omitempty"`
	Marks   []wireMark `json:"marks,omitempty"`
	Text    string     `json:"text,omitempty"`
	Type    string     `json:"type"`
}

type wireMark struct {
	Attrs *wireLink `json:"attrs,omitempty"`
	Type  string    `json:"type"`
}

type wireLink struct {
	Href string `json:"href"`
}

type wireStart struct {
	Start int `json:"start"`
}

type wireMention struct {
	ID string `json:"id"`
}

type wireValue struct {
	Attribute string `json:"attribute,omitempty"`
	Decimals  *int   `json:"decimals,omitempty"`
	Ref       string `json:"ref"`
}

func wireBlocks(blocks []Block) []wireNode {
	nodes := make([]wireNode, len(blocks))
	for i, b := range blocks {
		switch b := b.(type) {
		case *Paragraph:
			nodes[i] = wireNode{Type: nodeParagraph, Content: wireInlines(b.Inlines)}
		case *List:
			n := wireNode{Type: nodeBulletList}
			if b.Ordered {
				n.Type = nodeOrderedList
				if b.Start != 1 {
					n.Attrs = wireStart{b.Start}
				}
			}
			for _, item := range b.Items {
				n.Content = append(n.Content, wireNode{Type: nodeListItem, Content: wireBlocks(item.Blocks)})
			}
			nodes[i] = n
		}
	}
	return nodes
}

func wireInlines(inlines []Inline) []wireNode {
	nodes := make([]wireNode, len(inlines))
	for i, in := range inlines {
		switch in := in.(type) {
		case *Text:
			nodes[i] = wireNode{Type: nodeText, Text: in.Text, Marks: wireMarks(in.Marks)}
		case *Break:
			nodes[i] = wireNode{Type: nodeHardBreak, Marks: wireMarks(in.Marks)}
		case *Mention:
			nodes[i] = wireNode{Type: nodeMention, Attrs: wireMention{in.Component}, Marks: wireMarks(in.Marks)}
		case *Value:
			nodes[i] = wireNode{Type: nodeValueRef, Attrs: wireValue{in.Attribute, in.Decimals, in.Ref}, Marks: wireMarks(in.Marks)}
		}
	}
	return nodes
}

func wireMarks(m Marks) []wireMark {
	var marks []wireMark
	for _, name := range m.names() {
		mk := wireMark{Type: name}
		if name == markLink {
			mk.Attrs = &wireLink{m.Link}
		}
		marks = append(marks, mk)
	}
	return marks
}
