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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/netip"
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
	// MaxBytes is the most bytes of a document's JSON, counted without the
	// white space between its tokens and with each escape in a string
	// counted as the character that it stands for (jsonLength), so that
	// however the JSON is laid out, as PostgreSQL's jsonb writes it, with a
	// space after each colon and comma, and whichever characters its encoder
	// escapes, as json.Marshal escapes <, >, and &, the same document is
	// within the limit; and the most bytes of its canonical form, which can
	// be longer than the JSON that Parse read, as 9e5 is 900000, so that
	// every document that Parse returns can be written and read again.
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

// Doc is a document: its blocks, of which Parse returns at least one. The
// editor's empty document is one empty paragraph, and so is a Doc without
// blocks, as the zero Doc, which MarshalJSON writes as one.
//
// A Doc that Parse returned also holds the hash of its canonical form, which
// one that a program builds does not, so compare two Docs by what MarshalJSON
// writes of them, not with reflect.DeepEqual.
type Doc struct {
	Blocks []Block
	// sum is the SHA-256 of the canonical form that Parse wrote of a Doc
	// that it returned, and zero in one that a program built, as no form's
	// hash is: MarshalJSON writes what the document's nodes write without
	// reading it again as long as it has that hash. A hash, rather than the
	// form, so that a parsed Doc does not hold its document twice.
	sum [sha256.Size]byte
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
	// Start is the number of an ordered list's first item, from 0 to
	// MaxStart, which Parse sets to 1 when the document gives none, and to
	// 1 for a bulleted list. A program that builds an ordered List sets it
	// too: its zero value is a start of 0, which a document may have, so a
	// List left without one is numbered from 0.
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
type Inline interface {
	// marked returns the node's marks, which every inline node has, for the
	// parser to set.
	marked() *Marks
}

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

func (t *Text) marked() *Marks    { return &t.Marks }
func (b *Break) marked() *Marks   { return &b.Marks }
func (m *Mention) marked() *Marks { return &m.Marks }
func (v *Value) marked() *Marks   { return &v.Marks }

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

// nodeFields holds each node's fields.
var nodeFields = map[string][]string{
	nodeDoc:         {"content", "type"},
	nodeParagraph:   {"content", "type"},
	nodeBulletList:  {"content", "type"},
	nodeOrderedList: {"attrs", "content", "type"},
	nodeListItem:    {"content", "type"},
	nodeText:        {"marks", "text", "type"},
	nodeHardBreak:   {"marks", "type"},
	nodeMention:     {"attrs", "marks", "type"},
	nodeValueRef:    {"attrs", "marks", "type"},
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
	// TooLarge is a document of more than MaxBytes, without the white space
	// between its tokens and with its escapes counted as the characters that
	// they stand for, or in its canonical form, or whose text has more than
	// MaxChars characters.
	TooLarge Reason = "too_large"
)

// Error is what is wrong with a document that Parse refuses.
type Error struct {
	// Pointer is the JSON Pointer (RFC 6901) of the value that is wrong,
	// within the document: "" for the document itself, or for a limit of
	// the whole document. It names a field that a node may not have as the
	// document does, however long the name is.
	Pointer string
	// Reason says what is wrong.
	Reason Reason
	// Message says it in English, for programs and logs. It cuts a name
	// that the document gives, as a field's, to its first 40 characters.
	Message string
}

// maxTokenChars is the most characters of a name that Error and a message
// write: a field's name comes from a client, and may be most of a megabyte.
const maxTokenChars = 40

// Error writes the pointer and the message, with each reference token of the
// pointer whose name is longer than maxTokenChars characters cut to the first
// ones of the name, escaped again, and an ellipsis, as the message cuts a
// name, so that an error, which the API answers with and which a log may
// hold, does not repeat a name that is most of a megabyte long, and no cut
// falls within an escape, ~0 or ~1. A token that holds a character that is
// not printable (strconv.IsPrint), as a line feed, an escape that a terminal
// reads, or U+202E RIGHT-TO-LEFT OVERRIDE, is quoted as Go quotes a string,
// as the message quotes a name, so that no name that a client chose writes a
// line of its own into a log, or reorders the error.
func (e *Error) Error() string {
	at := e.Pointer
	if at == "" {
		at = "the document"
	} else {
		tokens := strings.Split(at, "/")
		for i, t := range tokens {
			name := strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
			long := utf8.RuneCountInString(name) > maxTokenChars
			if long {
				cut := 0
				for range maxTokenChars {
					_, size := utf8.DecodeRuneInString(name[cut:])
					cut += size
				}
				t = escapeToken(name[:cut])
			}
			if strings.ContainsFunc(t, func(r rune) bool { return !strconv.IsPrint(r) }) {
				t = strconv.Quote(t)
			}
			if long {
				t += "…"
			}
			tokens[i] = t
		}
		at = strings.Join(tokens, "/")
	}
	return "rich text: " + at + ": " + e.Message
}

// escapeToken returns an object's key as a reference token of a JSON Pointer
// writes it (RFC 6901), with ~ as ~0 and / as ~1.
func escapeToken(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

// Parse reads a document from its JSON, checks it against the schema and the
// limits, and returns it in its canonical form. The error is always an
// *Error. Of a key that an object has twice, the last is read, as JSON.parse
// and PostgreSQL's jsonb read it, so that every reader of a document reads
// the same one; the API refuses such a body before it reads a document in it
// (FR-API-01 AC2).
func Parse(data []byte) (*Doc, error) {
	doc, _, err := parse(data)
	return doc, err
}

// parse is Parse, and also returns the document's canonical form.
func parse(data []byte) (*Doc, []byte, error) {
	doc, err := parseDoc(data)
	if err != nil {
		return nil, nil, err
	}
	// The canonical form is written to check its length, and its hash kept
	// for MarshalJSON.
	canonical, err := doc.wire()
	if err != nil {
		// A document that Parse read holds no nil node and no string that is
		// not UTF-8, which is all that wire refuses.
		panic("richtext: a parsed document does not encode: " + err.Error())
	}
	if len(canonical) > MaxBytes {
		return nil, nil, &Error{Reason: TooLarge,
			Message: fmt.Sprintf("the document's canonical form is longer than %d bytes", MaxBytes)}
	}
	doc.sum = sha256.Sum256(canonical)
	return doc, canonical, nil
}

func parseDoc(data []byte) (*Doc, error) {
	length, lone := scan(data)
	if length > MaxBytes {
		return nil, &Error{Reason: TooLarge,
			Message: fmt.Sprintf("the document is longer than %d bytes without the white space between its tokens, "+
				"with each escape counted as the character that it stands for", MaxBytes)}
	}
	if !utf8.Valid(data) {
		return nil, &Error{Reason: InvalidJSON, Message: "the document is not UTF-8"}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	// Numbers stay as they are written until integer reads them, so that one
	// too large for a double, which JSON.parse reads as Infinity, is out of
	// range rather than not JSON.
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, &Error{Reason: InvalidJSON, Message: "the document is not JSON: " + err.Error()}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, &Error{Reason: InvalidJSON, Message: "the document is followed by more JSON"}
	}
	if lone {
		// encoding/json reads a lone surrogate as U+FFFD, and JSON.parse keeps
		// it as it is, so that the server would store other text than the
		// client holds.
		return nil, &Error{Reason: InvalidJSON, Message: "the document escapes a lone surrogate, which is not a character"}
	}
	var p parser
	return p.doc(v)
}

// scan reads data once, before the decoder does, and returns its length as
// MaxBytes counts it, and whether one of its strings escapes a UTF-16
// surrogate that is not one of a pair, as "\ud800", a lone surrogate, which
// no UTF-8 holds. The length is without the white space that JSON allows
// between its tokens, outside its strings, and with each escape in a string,
// as \u00e9 or \", counted as the bytes of the UTF-8 of the character that it
// stands for, an escaped pair of surrogates as the one character that the
// pair stands for, and a lone surrogate as U+FFFD. So the length of a
// document depends neither on how its JSON is laid out nor on which
// characters its encoder escapes: json.Marshal escapes <, >, and & as \u003c,
// \u003e, and \u0026 in what a MarshalJSON method writes, and Python's
// json.dumps every character beyond ASCII, which would otherwise make the
// same document up to six times as long. It reads what is not JSON without
// failing, which the decoder then refuses, before Parse reports a lone
// surrogate.
func scan(data []byte) (length int, lone bool) {
	n, inString := 0, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case !inString:
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				n++
				inString = c == '"'
			}
		case c == '"':
			n++
			inString = false
		case c != '\\':
			n++
		case i+5 < len(data) && data[i+1] == 'u':
			r := hex4(data[i+2 : i+6])
			i += 5
			if r >= 0xD800 && r < 0xDC00 && i+6 < len(data) && data[i+1] == '\\' && data[i+2] == 'u' {
				if low := hex4(data[i+3 : i+7]); low >= 0xDC00 && low < 0xE000 {
					n += 4
					i += 6
					continue
				}
			}
			size := utf8.RuneLen(r)
			if size < 0 {
				// A surrogate that is not the first of a pair, or is not
				// followed by the second, which Parse refuses, counts as
				// U+FFFD.
				size, lone = utf8.RuneLen(utf8.RuneError), true
			}
			n += size
		default:
			// An escape of one character, as \" or \n, or a backslash that
			// starts no escape, which the decoder refuses.
			n++
			i++
		}
	}
	return n, lone
}

// hex4 returns the value of four hexadecimal digits.
func hex4(digits []byte) rune {
	var r rune
	for _, c := range digits {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			r |= rune(c - 'A' + 10)
		}
	}
	return r
}

// UnmarshalJSON reads a document as Parse does, so that a request's or a
// payload's field can hold one. JSON's null leaves the document as it is, as
// encoding/json leaves any other field that null is written to.
func (d *Doc) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
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
	path []token
	// chars counts the code points of the text nodes read so far.
	chars int
}

// token is a reference token of a pointer: an object's key, or an array's
// index, which is written only when an error names it.
type token struct {
	key string
	// index is the array's index, or -1 for an object's key.
	index int
}

func (p *parser) push(key string) { p.path = append(p.path, token{key: key, index: -1}) }
func (p *parser) pushIndex(i int) { p.path = append(p.path, token{index: i}) }
func (p *parser) pop()            { p.path = p.path[:len(p.path)-1] }

func (p *parser) fail(reason Reason, format string, args ...any) *Error {
	var b strings.Builder
	for _, t := range p.path {
		b.WriteByte('/')
		if t.index >= 0 {
			b.WriteString(strconv.Itoa(t.index))
		} else {
			b.WriteString(escapeToken(t.key))
		}
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
		return nil, "", p.fail(UnknownNode, "%.*q is not a node of the schema", maxTokenChars, typ)
	}
	if !w.holds(typ) {
		return nil, "", p.fail(Content, "%s, not %s", w.holdsWhat(), article(typ))
	}
	if err := p.fields(o, "the "+typ, nodeFields[typ]...); err != nil {
		return nil, "", err
	}
	return o, typ, nil
}

// fields checks that object o, of the node, mark, or attributes what, has no
// field but those allowed. Of several fields that it may not have, it names
// the first in sorted order, whatever order the map holds them in, so a type
// in attributes, which have none, is named as any other field is.
func (p *parser) fields(o map[string]any, what string, allowed ...string) error {
	unknown, found := "", false
	for k := range o {
		if !slices.Contains(allowed, k) && (!found || k < unknown) {
			unknown, found = k, true
		}
	}
	if found {
		p.push(unknown)
		return p.fail(UnknownField, "%s has no field %.*q", what, maxTokenChars, unknown)
	}
	return nil
}

// content returns the content of node o, of type typ, which must have at
// least one node when required, and may be missing otherwise. It leaves
// "content" pushed for the caller, which pops it once it has read the nodes.
func (p *parser) content(o map[string]any, typ string, required bool) ([]any, error) {
	v, ok := o["content"]
	if !ok {
		if required {
			return nil, p.fail(MissingField, "the %s has no content", typ)
		}
		p.push("content")
		return nil, nil
	}
	p.push("content")
	a, ok := v.([]any)
	if !ok {
		return nil, p.fail(WrongType, "the %s's content is not an array", typ)
	}
	if required && len(a) == 0 {
		return nil, p.fail(Content, "the %s holds nothing", typ)
	}
	return a, nil
}

// attrs returns the attributes object of node or mark o, of type typ, or nil
// when it has none and they are not required. It leaves "attrs" pushed when
// it returns an object.
func (p *parser) attrs(o map[string]any, typ string, required bool) (map[string]any, error) {
	v, ok := o["attrs"]
	if !ok {
		if required {
			return nil, p.fail(MissingField, "the %s has no attrs", typ)
		}
		return nil, nil
	}
	p.push("attrs")
	a, ok := v.(map[string]any)
	if !ok {
		return nil, p.fail(WrongType, "the %s's attrs are not an object", typ)
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
		p.pushIndex(i)
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
			if err := p.fields(attrs, "the orderedList's attrs", "start"); err != nil {
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
		p.pushIndex(i)
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

// integer reads integer v, what, which must be between lo and hi. It reads a
// number as JSON.parse does, as the double nearest to it, so 3.0 and 3e0 are
// 3, as is a number written with more digits than a double holds that rounds
// to 3, as 2.99999999999999999999; and one too large for a double, which
// JSON.parse reads as Infinity, is out of range.
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
	inlines := make([]Inline, 0, len(content))
	for i, v := range content {
		p.pushIndex(i)
		in, err := p.inline(v)
		if err != nil {
			return nil, err
		}
		p.pop()
		inlines = append(inlines, in)
	}
	p.pop()
	return &Paragraph{Inlines: canonicalInlines(inlines)}, nil
}

// canonicalInlines returns a paragraph's inline nodes as its canonical form
// holds them: each run of neighbouring texts with the same marks joined into
// one text, as ProseMirror joins them, and each ID in its canonical spelling.
// Parse reads a paragraph through it, and so do a document's segments and
// MarshalJSON, so that a Doc that a program builds derives and is written as
// it is once it is stored, by the same rules. It changes neither inlines nor
// their nodes: it returns inlines when they are canonical already, and
// otherwise a new slice, with a new node in place of each that differs. It
// writes each run's text once, so that a paragraph of many short texts costs
// no more than their length. A nil node, which MarshalJSON refuses, is kept,
// and ends a run.
func canonicalInlines(inlines []Inline) []Inline {
	// out is nil as long as the inlines are canonical so far.
	var out []Inline
	for i := 0; i < len(inlines); {
		in, j := inlines[i], i+1
		switch n := in.(type) {
		case *Text:
			if n == nil {
				break
			}
			size := len(n.Text)
			for ; j < len(inlines); j++ {
				next, ok := inlines[j].(*Text)
				if !ok || next == nil || next.Marks != n.Marks {
					break
				}
				size += len(next.Text)
			}
			if j > i+1 {
				var b strings.Builder
				b.Grow(size)
				for _, run := range inlines[i:j] {
					b.WriteString(run.(*Text).Text)
				}
				in = &Text{Text: b.String(), Marks: n.Marks}
			}
		case *Mention:
			if n != nil {
				if id := canonicalID(n.Component); id != n.Component {
					m := *n
					m.Component = id
					in = &m
				}
			}
		case *Value:
			if n != nil {
				ref, attribute := canonicalID(n.Ref), canonicalID(n.Attribute)
				if ref != n.Ref || attribute != n.Attribute {
					v := *n
					v.Ref, v.Attribute = ref, attribute
					in = &v
				}
			}
		}
		if out == nil && in != inlines[i] {
			out = append(make([]Inline, 0, len(inlines)), inlines[:i]...)
		}
		if out != nil {
			out = append(out, in)
		}
		i = j
	}
	if out == nil {
		return inlines
	}
	return out
}

// canonicalID returns id in its canonical spelling, or id as it is when it is
// not an ID, as "" is not. ids.Encode spells an ID with its prefix in
// lowercase and its body in uppercase, of which DecodeAny reads each in
// either case and nothing else, so an ID that DecodeAny reads, without an
// uppercase letter before its underscore or a lowercase one after it, is
// spelt canonically already, and is returned without a new string, as every
// ID of a document that Parse returned is when it derives.
func canonicalID(id string) string {
	kind, u, err := ids.DecodeAny(id)
	if err != nil {
		return id
	}
	prefix, body, _ := strings.Cut(id, "_")
	if !strings.ContainsFunc(prefix, func(r rune) bool { return 'A' <= r && r <= 'Z' }) &&
		!strings.ContainsFunc(body, func(r rune) bool { return 'a' <= r && r <= 'z' }) {
		return id
	}
	return ids.Encode(kind, u)
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
	*in.marked() = marks
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

// The characters that text may not hold besides the control characters (Cc).
const (
	lineSeparator      = 0x2028
	paragraphSeparator = 0x2029
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
	if err := p.fields(attrs, "the mention's attrs", "id"); err != nil {
		return nil, err
	}
	_, id, err := p.id(attrs, "id", "a mention", ids.Component)
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
	if err := p.fields(attrs, "the valueRef's attrs", "attribute", "decimals", "ref"); err != nil {
		return nil, err
	}
	kind, ref, err := p.id(attrs, "ref", "a value reference", ids.Variable, ids.Component)
	if err != nil {
		return nil, err
	}
	val := &Value{Ref: ref}
	attribute, named := attrs["attribute"]
	switch {
	case kind == ids.Variable && named:
		p.push("attribute")
		// An attribute that is null, as ProseMirror writes one that is not
		// set, is of the wrong type, as a null is wherever it is.
		if _, ok := attribute.(string); !ok {
			return nil, p.fail(WrongType, "a value reference's attribute is not a string")
		}
		return nil, p.fail(Reference, "a reference to a variable names no attribute")
	case kind == ids.Component && !named:
		return nil, p.fail(Reference, "a reference to a component names its attribute")
	case named:
		if _, val.Attribute, err = p.id(attrs, "attribute", "a value reference", ids.AttributeDef); err != nil {
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
// entity of one of the kinds, and returns its kind and the ID as it is
// written, which canonicalInlines spells canonically.
func (p *parser) id(attrs map[string]any, key, what string, kinds ...ids.Kind) (ids.Kind, string, error) {
	v, ok := attrs[key]
	if !ok {
		return 0, "", p.fail(MissingField, "%s has no %s", what, key)
	}
	p.push(key)
	s, ok := v.(string)
	if !ok {
		return 0, "", p.fail(WrongType, "%s's %s is not a string", what, key)
	}
	kind, _, err := ids.DecodeAny(s)
	if err != nil || !slices.Contains(kinds, kind) {
		names := make([]string, len(kinds))
		for i, k := range kinds {
			names[i] = article(kindName(k))
		}
		return 0, "", p.fail(Reference, "%s's %s is not the ID of %s", what, key, strings.Join(names, " or "))
	}
	p.pop()
	return kind, s, nil
}

// kindName returns the name of the kind of entity that a document refers to,
// as a message names it.
func kindName(k ids.Kind) string {
	if k == ids.AttributeDef {
		return "attribute definition"
	}
	return k.String()
}

// article returns name after the indefinite article that it takes, as "an
// orderedList": the names of the schema's nodes and of the kinds of entity
// that a document refers to each take "an" exactly when they start with a
// vowel letter.
func article(name string) string {
	if name != "" && strings.ContainsRune("aeiouAEIOU", rune(name[0])) {
		return "an " + name
	}
	return "a " + name
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
		p.pushIndex(i)
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
			return m, p.fail(UnknownMark, "%.*q is not a mark of the schema", maxTokenChars, typ)
		}
		allowed := []string{"type"}
		if typ == markLink {
			allowed = append(allowed, "attrs")
		}
		if err := p.fields(mo, "the "+typ+" mark", allowed...); err != nil {
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
		if m.Code && (m.Bold || m.Italic || m.Strike || m.Link != "") {
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
	if err := p.fields(attrs, "the link's attrs", "href"); err != nil {
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

// CheckHref says what is wrong with the URL of a link, or returns "", so that
// no link runs a script or reads a file when it is followed, or hides where it
// leads (PRD NFR-SEC-08). A link is UTF-8, as every string of a document that
// Parse reads is, and has at most MaxHrefChars characters, counted as code
// points, none of them hidden (hidden): white space, a control character, a
// format character (Unicode's Cf, as U+200B, U+202E, or U+FEFF), or another
// character that a renderer shows nothing of (Default_Ignorable_Code_Point,
// as U+3164 HANGUL FILLER), which would hide or reorder what a reader sees of
// it, and each % in it starts an escape, % and two hexadecimal digits, as
// url.PathUnescape reads them. It is a mailto URL with an address and only
// the fields that mailtoProblem takes, or an http or https URL whose
// authority, after the //, is a host of ASCII and a port, as authorityProblem
// reads it. A scheme is read regardless of ASCII case.
//
// CheckHref reads a link by these rules, which schema/rich-text/README.md
// states, rather than as a URL parser does, so that the TypeScript client
// checks a link as the server does: parsers differ on what is not a URL.
func CheckHref(href string) string {
	switch {
	case href == "":
		return "is empty"
	case !utf8.ValidString(href):
		// Bytes that are not UTF-8 would be read as U+FFFD, which a host
		// may hold, and encoding/json would write them as it.
		return "is not UTF-8"
	case utf8.RuneCountInString(href) > MaxHrefChars:
		return fmt.Sprintf("is longer than %d characters", MaxHrefChars)
	case strings.IndexFunc(href, hidden) >= 0:
		return "holds white space, a control character, a format character, or another character that is not shown"
	}
	// url.PathUnescape checks only that each % starts an escape. A mailto
	// link's address and values are read again, each apart, by unescape: an
	// escaped delimiter, as %26, is in a value, not between two.
	if _, err := url.PathUnescape(href); err != nil {
		return "holds a % that starts no escape of two hexadecimal digits"
	}
	scheme, rest, _ := strings.Cut(href, ":")
	switch asciiLower(scheme) {
	case "http", "https":
		authority, ok := strings.CutPrefix(rest, "//")
		if !ok {
			return "has no host"
		}
		if i := strings.IndexAny(authority, "/?#"); i >= 0 {
			authority = authority[:i]
		}
		return authorityProblem(authority)
	case "mailto":
		return mailtoProblem(rest)
	}
	return "is not an http, https, or mailto URL"
}

// mailtoFields are the fields that a mailto link's query may set, in
// lowercase: what a message is sent to and what it says, and not a field
// that a mail client could read a file for, as attach.
var mailtoFields = []string{"bcc", "body", "cc", "subject", "to"}

// mailtoProblem says what is wrong with what follows the colon of a mailto
// link, or returns "". Its address is what is before the first ?, and its
// query what follows that ?, in which & separates the fields, each a name, =,
// and a value. It holds these delimiters only where they delimit, as RFC 6068
// has them, so that a mail client that reads the fields otherwise than at the
// first ? and between the &s, as at the last ?, wherever an & or a ? is, or
// at a ;, cannot read a field that the link does not set, as attach:
//
//   - It has no #, which RFC 6068 gives a mailto URL no use for, and after
//     which a client that does not cut a fragment off would read a field, as
//     in mailto:keeper@example.com#?attach=/etc/passwd.
//   - It has no ;, which HTML 4 and Python's parse_qs, before Python 3.9.2,
//     read as an &, as in mailto:keeper@example.com?subject=x;attach=y.
//   - Its address is not empty, and holds no & or =, as in
//     mailto:keeper@example.com&attach=/etc/passwd, where a client that
//     reads a field wherever an & is reads attach.
//   - Each of its fields is empty, or a name of mailtoFields, read regardless
//     of ASCII case, then = and a value that holds no ? or =, as in
//     subject=x?attach=y, where a client that reads the fields after the last
//     ? reads attach.
//
// Each of these characters in an address or a value is escaped: %23, %3B,
// %26, %3F, and %3D. The escapes of the address and of a value but a body's
// stand for UTF-8 (unescape), as RFC 6068 has them, since a mail client reads
// bytes that are not UTF-8 as it chooses, as Latin-1, in which %85 is NEL and
// %AD a soft hyphen. The address, and the value of to, cc, or bcc, which name
// addresses too, are as addressesProblem has them: they escape no hidden
// character, and are addresses as RFC 6068 has them, without a display name,
// a comment, or quotes, in ASCII. A subject escapes no character that text
// may not hold (isControl), a control character or a line or paragraph
// separator, which a mail client could write into a header, as a line break
// that starts another header in
// mailto:keeper@example.com?subject=a%0D%0ABcc:evil@example.com, or that a
// reader of lines may take for one, as Python's str.splitlines takes NEL,
// %C2%85, and LINE SEPARATOR, %E2%80%A8. A subject may escape white space and
// format characters, as text may hold them, and a body any byte: its line
// breaks are %0D%0A, as RFC 6068 writes them.
func mailtoProblem(rest string) string {
	switch {
	case strings.Contains(rest, "#"):
		return "has a #, which a mailto URL has no use for, and after which a mail client could read a field"
	case strings.Contains(rest, ";"):
		return "has a ;, which a mail client could read as an &, and then read a field after it; it is escaped, %3B"
	}
	address, query, _ := strings.Cut(rest, "?")
	switch {
	case address == "":
		return "has no address"
	case strings.ContainsAny(address, "&="):
		return "has an address with an & or an =, after which a mail client could read a field; they are " +
			"escaped, %26 and %3D"
	}
	if problem := addressesProblem(address); problem != "" {
		return "has an address that " + problem
	}
	for field := range strings.SplitSeq(query, "&") {
		if field == "" {
			continue
		}
		name, value, set := strings.Cut(field, "=")
		name = asciiLower(name)
		switch {
		case !set || !slices.Contains(mailtoFields, name):
			return "sets a field other than to, cc, bcc, subject, or body"
		case strings.ContainsAny(value, "?="):
			return "has a field whose value holds a ? or an =, after which a mail client could read a field; " +
				"they are escaped, %3F and %3D"
		case name == "subject":
			decoded, ok := unescape(value)
			if !ok {
				return "has a subject that escapes bytes that are not UTF-8, which a mail client could read as " +
					"another character, as a control character"
			}
			if strings.ContainsFunc(decoded, isControl) {
				return "has a subject that escapes a control character or a line or paragraph separator, which a " +
					"mail client could write into a header"
			}
		case name == "to" || name == "cc" || name == "bcc":
			if problem := addressesProblem(value); problem != "" {
				return "has a to, cc, or bcc that " + problem
			}
		}
	}
	return ""
}

// addressesProblem says what is wrong with a mailto link's address, or with
// the value of its to, cc, or bcc, or returns "". Its escapes stand for UTF-8
// (unescape), of which none is hidden, which would hide or reorder the
// address that a mail client shows, as %E2%80%AE, U+202E RIGHT-TO-LEFT
// OVERRIDE, would, or start another header, as %0D%0A would. Once its escapes
// are read, it is ASCII, as below, and either empty, which only a to, cc, or
// bcc can be, or addresses that commas separate, each an addr-spec as
// RFC 6068 has it, without a quoted local part: a local part, an @, and a
// domain, each atoms that single dots separate (RFC 5322's dot-atom-text,
// dotAtom), or a domain that is an address literal in brackets
// (addressLiteral). So an address has no display name, comment, group, or
// quotes, which a mail client shows in place of the address that the message
// goes to, as it shows
// mailto:%22support@paypal.com%22%3Cx@evil.example%3E, which goes to
// x@evil.example, as support@paypal.com.
//
// An address is ASCII, as an http link's host is (nameProblem), since a
// character beyond ASCII may look like another: in a domain, a letter of
// another script, as the Cyrillic а of mailto:support@pаypal.com looks like a
// Latin a, so that a mail client shows a domain that the message does not go
// to; and in a local part, which a reader sees before the domain, a character
// that looks like a delimiter of an address, which may be a letter, as U+02BA
// MODIFIER LETTER DOUBLE PRIME looks like a " and U+1438 CANADIAN SYLLABICS PA
// like a <, as well as punctuation, as U+FF20 FULLWIDTH COMMERCIAL AT looks
// like an @, so that the address reads as a display name and another address,
// or a blank that shows as wide as a letter does, which may be a letter too,
// as U+13441 EGYPTIAN HIEROGLYPH FULL BLANK, so that a run of them pushes the
// domain out of what a mail client shows. No property of Unicode's tells
// those from the letters of an internationalized address's local part
// (RFC 6532), which a mailto link therefore does not hold; and with its
// addresses in ASCII, a client checks a mailto link as the server does,
// whatever the version of its Unicode tables. An internationalized domain is
// written as IDNA writes it in ASCII, as keeper@xn--caf-dma.example.
func addressesProblem(s string) string {
	decoded, ok := unescape(s)
	switch {
	case !ok:
		return "escapes bytes that are not UTF-8, which a mail client could read as another character, as a " +
			"control character"
	case strings.ContainsFunc(decoded, hidden):
		// A character of s itself is not hidden, which CheckHref refuses
		// before, so an escape stands for it.
		return "escapes white space, a control character, a format character, or another character that is not " +
			"shown, which would hide or reorder the address that a mail client shows, or start another header"
	case !onlyRunes(decoded, isASCII):
		return "has a character beyond ASCII, which may look like another, as the Cyrillic a looks like the Latin " +
			"a in a domain, or like a delimiter of an address, as U+FF20 FULLWIDTH COMMERCIAL AT looks like an @, " +
			"or show as a blank that hides the domain: an address is ASCII, and an internationalized domain is " +
			"written as IDNA writes it in ASCII (xn--)"
	case decoded == "":
		return ""
	}
	for address := range strings.SplitSeq(decoded, ",") {
		local, domain, ok := strings.Cut(address, "@")
		if !ok || !dotAtom(local) || !dotAtom(domain) && !addressLiteral(domain) {
			return "is not addresses that commas separate, each a local part, an @, and a domain, of letters, " +
				"digits, and !#$%&'*+-/=?^_`{|}~ in atoms that single dots separate, without a display name, a " +
				"comment, or quotes, which a mail client could show in place of the address that the message " +
				"goes to"
		}
	}
	return ""
}

// isAtext reports whether r may be in an atom of an address, as RFC 5322's
// atext has it: an ASCII letter or digit, or one of !#$%&'*+-/=?^_`{|}~. So
// none of the characters that delimit what a mail client shows of an address
// otherwise than the address, as a display name's quotes and the angle
// brackets after it, a comment's parentheses, a group's colon and semicolon,
// or a comma, is in one.
func isAtext(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || isDigit(r) || strings.ContainsRune("!#$%&'*+-/=?^_`{|}~", r)
}

// dotAtom reports whether s is one or more atoms, each one or more characters
// that isAtext takes, which single dots separate, as RFC 5322's
// dot-atom-text.
func dotAtom(s string) bool {
	for atom := range strings.SplitSeq(s, ".") {
		if atom == "" || !onlyRunes(atom, isAtext) {
			return false
		}
	}
	return true
}

// addressLiteral reports whether a domain is an address literal: brackets
// around one or more ASCII letters, digits, colons, and dots, as
// [192.0.2.1], [2001:db8::1], or [IPv6:2001:db8::1] (RFC 5321), which RFC
// 6068 takes in a mailto link, without the other characters that its
// domain-literal may hold, which a mail client could read otherwise.
func addressLiteral(domain string) bool {
	inner, opened := strings.CutPrefix(domain, "[")
	inner, closed := strings.CutSuffix(inner, "]")
	if !opened || !closed || inner == "" {
		return false
	}
	return onlyRunes(inner, func(r rune) bool {
		return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || isDigit(r) || r == ':' || r == '.'
	})
}

// hidden reports whether r is a character that a link may not hold, which
// would hide or reorder what a reader sees of it: white space (Unicode's
// White_Space), a control character (Cc), a format character (Cf), or another
// character that a renderer shows nothing of unless it supports it
// (Default_Ignorable_Code_Point), as U+3164 HANGUL FILLER, a letter, U+034F
// COMBINING GRAPHEME JOINER, a mark, or a variation selector. Unicode derives
// Default_Ignorable_Code_Point from Other_Default_Ignorable_Code_Point, the
// format characters, and the variation selectors, without white space and a
// few format characters, so the tables of package unicode that hidden reads
// take what JavaScript's \p{White_Space}, \p{Cc}, \p{Cf}, and
// \p{Default_Ignorable_Code_Point} take together, and they take the same
// characters in Unicode 15.0, the version of package unicode's tables, as in
// Unicode 17.0, a JavaScript engine's. A line or paragraph separator is white
// space, so every character that text may not hold (isControl) is hidden.
//
// A stored document is read with Parse, so a Go whose tables take a character
// as hidden that these do not would refuse the stored documents whose links
// hold it, which could no longer be read: TestUnicodeTables holds hidden to
// the characters that it takes now.
//
// hidden is not the complement of input.Printed, which an account's email
// address is checked with. That also refuses the characters that Unicode has
// not assigned, which change with each version of Unicode, of which
// JavaScript's engines have newer ones than package unicode, so that the
// client would take a link that the server refuses; and the blanks that no
// property of Unicode's names, as U+2800 BRAILLE PATTERN BLANK, which shows
// as wide as a letter does, and cannot change where a link leads, whose host,
// and a mailto link's addresses, are ASCII (addressesProblem).
func hidden(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r) ||
		unicode.In(r, unicode.Cf, unicode.Other_Default_Ignorable_Code_Point, unicode.Variation_Selector)
}

// unescape returns s, whose every % starts an escape, with each escape read
// as the byte that it stands for, with its digits in either case, and reports
// whether those bytes, with the characters between them, are UTF-8, as
// RFC 6068 has a mailto link's escapes, and as JavaScript's
// decodeURIComponent requires.
func unescape(s string) (string, bool) {
	decoded, err := url.PathUnescape(s)
	return decoded, err == nil && utf8.ValidString(decoded)
}

// maxPort is the highest port, which a browser's URL parser takes: the
// highest number of 16 bits.
const maxPort = 1<<16 - 1

// authorityProblem says what is wrong with the authority of an http or https
// link, what is between its // and the first /, ?, or # after it, or returns
// "". It is a host, then a colon and a port when it has one: an IPv6 address
// in brackets (ipv6Problem), followed by nothing or by the colon and the
// port; or a name (nameProblem), which the authority's last colon separates
// from the port. The port is ASCII digits whose value is at most maxPort, or
// nothing. So an authority has no @, and a link no userinfo, as
// https://example.com@evil.example/ has, whose host is evil.example, and a
// backslash, which a browser reads as a slash, cannot hide another host in
// it.
func authorityProblem(authority string) string {
	if strings.Contains(authority, "@") {
		return "has a userinfo, before an @, which could show another host than the one that it leads to"
	}
	var port string
	if strings.HasPrefix(authority, "[") {
		end := strings.IndexByte(authority, ']')
		if end < 0 {
			return "has an IPv6 address without its closing bracket"
		}
		if after := authority[end+1:]; after != "" {
			var ok bool
			if port, ok = strings.CutPrefix(after, ":"); !ok {
				return "has a host followed by more than a port"
			}
		}
		if problem := ipv6Problem(authority[1:end]); problem != "" {
			return problem
		}
	} else {
		host := authority
		if colon := strings.LastIndexByte(authority, ':'); colon >= 0 {
			host, port = authority[:colon], authority[colon+1:]
		}
		if problem := nameProblem(host); problem != "" {
			return problem
		}
	}
	if port == "" {
		return ""
	}
	// ParseUint takes ASCII digits in base 10, and refuses a number of more
	// than 16 bits, which is above maxPort.
	_, err := strconv.ParseUint(port, 10, 16)
	switch {
	case !onlyRunes(port, isDigit):
		return "has a port that is not a number"
	case err != nil:
		return fmt.Sprintf("has a port above %d", maxPort)
	}
	return ""
}

// ipv6Problem says what is wrong with what the brackets of an authority hold,
// or returns "". It is an IPv6 address as RFC 4291 (section 2.2) writes it:
// eight groups of one to four hexadecimal digits, separated by colons, of
// which one run of one or more groups of zeros may be written as ::, and the
// last two of which may be written as an IPv4 address, four numbers from 0
// to 255 in decimal, without leading zeros, separated by dots; and without a
// zone, which no URL has.
func ipv6Problem(address string) string {
	if address == "" {
		return "has no host"
	}
	if !onlyRunes(address, func(r rune) bool { return r == ':' || r == '.' || isHexDigit(r) }) {
		return "has an IPv6 address in brackets that holds what no address does"
	}
	if a, err := netip.ParseAddr(address); err != nil || !a.Is6() {
		return "has in brackets what is not an IPv6 address"
	}
	return ""
}

// nameProblem says what is wrong with a host's name, or returns "". It is
// ASCII: letters, digits, and -._~!$&'()*+,;=, so that an internationalized
// name is written as IDNA writes it in ASCII, as xn--caf-dma.example for
// café.example, which a browser's URL parser gives (new URL(href).hostname).
// A character beyond ASCII may look like another, a letter as a slash, as
// U+30CE KATAKANA LETTER NO (ノ), a digit as a dot, as U+0660 ARABIC-INDIC
// DIGIT ZERO, or a symbol as either, as U+2215 DIVISION SLASH, all of which
// IDNA keeps in the host, so that https://bank.exampleノlogin.evil.example/,
// which reads as a path of bank.example, leads to a host of evil.example. The
// name's labels, which dots separate, are not empty, but for a last one after
// a dot that ends the name, as in example.com.; and when its last label (not
// that empty one) is digits, or 0x or 0X and hexadecimal digits, which a
// browser then reads as an IPv4 address, the name is one, four numbers from
// 0 to 255 in decimal, without leading zeros, separated by dots.
func nameProblem(name string) string {
	if name == "" {
		return "has no host"
	}
	if !onlyRunes(name, isNameRune) {
		return "has a host with a character that a host may not hold, as one beyond ASCII: an internationalized " +
			"host is written as IDNA writes it in ASCII (xn--)"
	}
	trimmed := strings.TrimSuffix(name, ".")
	labels := strings.Split(trimmed, ".")
	if slices.Contains(labels, "") {
		return "has a host with an empty label, before a dot that does not end it"
	}
	if last := labels[len(labels)-1]; onlyRunes(last, isDigit) || hexNumber(last) {
		// ParseAddr reads an IPv4 address as four numbers from 0 to 255 in
		// decimal, without leading zeros, separated by dots, and the name has
		// no colon, which an IPv6 address would.
		if a, err := netip.ParseAddr(trimmed); err != nil || !a.Is4() {
			return "has a host that a browser reads as an IPv4 address, which is not four numbers from 0 to 255 " +
				"in decimal without leading zeros"
		}
	}
	return ""
}

// hexNumber reports whether a label is 0x or 0X and hexadecimal digits, which
// a browser reads as a number.
func hexNumber(label string) bool {
	digits, ok := strings.CutPrefix(label, "0x")
	if !ok {
		digits, ok = strings.CutPrefix(label, "0X")
	}
	return ok && onlyRunes(digits, isHexDigit)
}

// isNameRune reports whether a host's name may hold r: an ASCII letter or
// digit, or one of -._~!$&'()*+,;=.
func isNameRune(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || isDigit(r) || strings.ContainsRune("-._~!$&'()*+,;=", r)
}

func isDigit(r rune) bool { return '0' <= r && r <= '9' }

func isASCII(r rune) bool { return r < utf8.RuneSelf }

func isHexDigit(r rune) bool {
	return '0' <= r && r <= '9' || 'a' <= r && r <= 'f' || 'A' <= r && r <= 'F'
}

// onlyRunes reports whether every character of s is one that ok takes.
func onlyRunes(s string, ok func(rune) bool) bool {
	return strings.IndexFunc(s, func(r rune) bool { return !ok(r) }) < 0
}

// asciiLower returns s with its ASCII uppercase letters lowercased, and no
// other character changed, so that no character beyond ASCII, as the long s,
// which Unicode folds to s, can stand in for a letter of a scheme.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// MarshalJSON writes the document in its canonical form: its JSON as
// ProseMirror writes it, without a field that holds its default (an empty
// content or marks, or an ordered list's start of 1), with each inline node's
// marks in alphabetical order, the IDs in their canonical spelling, a
// paragraph's neighbouring texts with the same marks joined, and every
// object's keys sorted. It does not escape HTML, which an encoder that
// escapes it, as json.Marshal does, escapes in what it writes too.
//
// A Doc without blocks, as the zero Doc, is the empty document, which it
// writes as one empty paragraph. A Doc that a program builds, as an import
// does, is checked as one that a client sends is: MarshalJSON reads what it
// writes with Parse, and refuses a Doc that Parse would refuse, as one with a
// javascript: link, or with a string that is not UTF-8, which encoding/json
// would change, rather than write it, and writes the canonical form of
// one that Parse accepts, which Parse writes as it checks its length, so
// that what is stored is always a document that Parse returns. A Doc that
// Parse returned is not read again while its nodes write the canonical form
// that Parse wrote, as the form's hash tells: one whose nodes a program has
// changed since is read again, as a built one is.
func (d Doc) MarshalJSON() ([]byte, error) {
	data, err := d.wire()
	if err != nil {
		return nil, err
	}
	if d.sum != ([sha256.Size]byte{}) && sha256.Sum256(data) == d.sum {
		return data, nil
	}
	_, canonical, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("richtext: the document is not one that Parse accepts: %w", err)
	}
	return canonical, nil
}

// wire writes the document as JSON, as it holds it.
func (d Doc) wire() ([]byte, error) {
	blocks := d.Blocks
	if len(blocks) == 0 {
		blocks = []Block{&Paragraph{}}
	}
	content, err := wireBlocks(blocks)
	if err != nil {
		return nil, err
	}
	return marshalNoEscape(wireNode{Type: nodeDoc, Content: content})
}

// marshalNoEscape encodes v as JSON without escaping HTML (PRD §6.2) and
// without the line feed that an Encoder ends it with.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
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

// wireBlocks returns the nodes of blocks as JSON holds them, each paragraph's
// inline nodes in their canonical form (canonicalInlines), so that a Doc that
// a program builds of many neighbouring texts with the same marks, which it
// derives as one, is measured against MaxBytes as Parse stores it, joined,
// rather than refused as longer than it is. It refuses only what JSON cannot
// hold, a nil node, and a string that is not UTF-8, which encoding/json would
// write with U+FFFD in place of its bytes, so that Parse would accept other
// text than the Doc holds; it leaves the rest to Parse, which MarshalJSON
// reads them with.
func wireBlocks(blocks []Block) ([]wireNode, error) {
	nodes := make([]wireNode, len(blocks))
	for i, b := range blocks {
		switch b := b.(type) {
		case *Paragraph:
			if b == nil {
				return nil, errors.New("richtext: a paragraph is nil")
			}
			inlines, err := wireInlines(canonicalInlines(b.Inlines))
			if err != nil {
				return nil, err
			}
			nodes[i] = wireNode{Type: nodeParagraph, Content: inlines}
		case *List:
			if b == nil {
				return nil, errors.New("richtext: a list is nil")
			}
			n := wireNode{Type: nodeBulletList}
			if b.Ordered {
				n.Type = nodeOrderedList
				if b.Start != 1 {
					n.Attrs = wireStart{b.Start}
				}
			}
			for _, item := range b.Items {
				content, err := wireBlocks(item.Blocks)
				if err != nil {
					return nil, err
				}
				n.Content = append(n.Content, wireNode{Type: nodeListItem, Content: content})
			}
			nodes[i] = n
		default:
			return nil, errors.New("richtext: a block is nil")
		}
	}
	return nodes, nil
}

func wireInlines(inlines []Inline) ([]wireNode, error) {
	nodes := make([]wireNode, len(inlines))
	for i, in := range inlines {
		// own are the node's strings besides its link, which its marks hold
		// for every type of node.
		var own [2]string
		switch n := in.(type) {
		case *Text:
			if n == nil {
				return nil, errors.New("richtext: a text is nil")
			}
			nodes[i], own[0] = wireNode{Type: nodeText, Text: n.Text}, n.Text
		case *Break:
			if n == nil {
				return nil, errors.New("richtext: a line break is nil")
			}
			nodes[i] = wireNode{Type: nodeHardBreak}
		case *Mention:
			if n == nil {
				return nil, errors.New("richtext: a mention is nil")
			}
			nodes[i], own[0] = wireNode{Type: nodeMention, Attrs: wireMention{n.Component}}, n.Component
		case *Value:
			if n == nil {
				return nil, errors.New("richtext: a value reference is nil")
			}
			nodes[i] = wireNode{Type: nodeValueRef, Attrs: wireValue{n.Attribute, n.Decimals, n.Ref}}
			own = [2]string{n.Ref, n.Attribute}
		default:
			return nil, errors.New("richtext: an inline node is nil")
		}
		marks := in.marked()
		if !utf8.ValidString(own[0]) || !utf8.ValidString(own[1]) || !utf8.ValidString(marks.Link) {
			return nil, errors.New("richtext: an inline node holds a string that is not UTF-8")
		}
		nodes[i].Marks = wireMarks(*marks)
	}
	return nodes, nil
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
