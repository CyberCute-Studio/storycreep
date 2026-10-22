package richtext

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Names gives the names that a document's derivations show for what it
// refers to. A name that is not found is the caller's to choose.
type Names interface {
	// Value returns the name of the value that a reference reads, as an
	// expression names it: a variable's key, as met_elara, or, when
	// attribute is not "", a component's key and the attribute's key
	// joined by a dot, as elara.trust.
	Value(ref, attribute string) string
	// Component returns the name of a component, as a mention shows it.
	Component(id string) string
}

// The kinds of segment.
const (
	SegmentText      = "text"
	SegmentValue     = "value"
	SegmentMention   = "mention"
	SegmentBreak     = "break"
	SegmentParagraph = "paragraph"
	SegmentItem      = "item"
)

// The kinds of list that an item segment is in.
const (
	ListBullet  = "bullet"
	ListOrdered = "ordered"
)

// Segment is a segment of a document in the published format, which runtimes
// walk instead of the document's tree (PRD §6.1, ADR-020). A document's
// segments are its inline content in order, texts, values, and mentions, each
// with its marks, and segments that mark where its paragraphs, list items,
// and line breaks begin: a paragraph segment begins each paragraph but the
// document's first and the first of each list item, which an item segment
// begins instead. schema/rich-text/README.md describes each kind.
type Segment struct {
	Kind string
	// Marks are an inline segment's marks, in alphabetical order.
	Marks []string
	// Href is the URL of the link that an inline segment is part of.
	Href string
	// Value is a text's text, or a mentioned component's name.
	Value string
	// Ref is a mention's component, or a value's variable, or its component
	// when Attribute is set.
	Ref string
	// Attribute is a value's attribute definition, of component Ref.
	Attribute string
	// Name is a value's name, as an expression names it (Names.Value), for
	// debugging, as an expression's AST names what it refers to.
	Name string
	// Decimals is a value's number of digits after the point.
	Decimals *int
	// Depth is an item's depth, 1 for an item of a list that no list holds,
	// or the depth of the item that a paragraph belongs to, or 0 for a
	// paragraph of no item.
	Depth int
	// List is the kind of an item's list, ListBullet or ListOrdered.
	List string
	// Number is an ordered list's item's number.
	Number int
}

// segmentJSON is a segment as the published format holds it, with the fields
// of its kind, in alphabetical order.
type segmentJSON struct {
	Attribute string    `json:"attribute,omitempty"`
	Decimals  *int      `json:"decimals,omitempty"`
	Depth     int       `json:"depth,omitempty"`
	Href      string    `json:"href,omitempty"`
	Kind      string    `json:"kind"`
	List      string    `json:"list,omitempty"`
	Marks     *[]string `json:"marks,omitempty"`
	Name      *string   `json:"name,omitempty"`
	Number    *int      `json:"number,omitempty"`
	Ref       string    `json:"ref,omitempty"`
	Value     *string   `json:"value,omitempty"`
}

// MarshalJSON writes the segment as the published format holds it: an inline
// segment always with its marks, empty or not, and every segment with only
// the fields of its kind, without escaping HTML (§6.2), which an encoder that
// escapes it, as json.Marshal does, escapes in what it writes too.
func (s Segment) MarshalJSON() ([]byte, error) {
	return marshalNoEscape(s.wire())
}

// wire returns the segment as the published format holds it.
func (s Segment) wire() segmentJSON {
	w := segmentJSON{Kind: s.Kind}
	inline := func() {
		marks := s.Marks
		if marks == nil {
			marks = []string{}
		}
		w.Marks = &marks
		w.Href = s.Href
	}
	switch s.Kind {
	case SegmentText:
		inline()
		w.Value = &s.Value
	case SegmentMention:
		inline()
		w.Ref, w.Value = s.Ref, &s.Value
	case SegmentValue:
		inline()
		w.Ref, w.Attribute, w.Decimals, w.Name = s.Ref, s.Attribute, s.Decimals, &s.Name
	case SegmentParagraph:
		w.Depth = s.Depth
	case SegmentItem:
		w.Depth, w.List = s.Depth, s.List
		if s.List == ListOrdered {
			w.Number = &s.Number
		}
	}
	return w
}

// Segments are the segments of a document, which the published format holds
// as an array.
type Segments []Segment

// MarshalJSON writes the segments as the published format holds them, each
// as Segment.MarshalJSON writes it, all with one encoder.
func (segs Segments) MarshalJSON() ([]byte, error) {
	w := make([]segmentJSON, len(segs))
	for i, s := range segs {
		w[i] = s.wire()
	}
	return marshalNoEscape(w)
}

// Derived is what is derived from a document.
type Derived struct {
	// Segments are its segments, for the published format.
	Segments Segments
	// Text is its plain text (PlainText).
	Text string
	// Preview is its preview (Preview).
	Preview string
	// Words is the number of its words (WordCount).
	Words int
	// Hash is the hash of its plain text (Hash).
	Hash string
}

// Derive derives from the document what the published format, the text
// export, and the writing statistics show of it, with the names of what it
// refers to, which names gives and which must not be nil. The document is one
// that Parse returned, as every stored one is, since MarshalJSON stores no
// other, or one that a program built and that MarshalJSON writes, which
// derives what it derives once it is stored, as each paragraph is read in
// its canonical form, by the rules that Parse reads it by: its neighbouring
// texts with the same marks are one text segment, and its IDs are in their
// canonical spelling. Derive does not check the document: a nil node, which
// MarshalJSON refuses, has no segment.
func (d *Doc) Derive(names Names) Derived {
	segs := d.Segments(names)
	text := PlainText(segs)
	return Derived{Segments: segs, Text: text, Preview: Preview(text), Words: WordCount(segs), Hash: Hash(text)}
}

// Segments returns the document's segments, with the names of what it
// refers to, which names gives and which must not be nil. A document whose
// only block is an empty paragraph has none. The document is one that Parse
// returned or that MarshalJSON writes, as for Derive.
func (d *Doc) Segments(names Names) Segments {
	if names == nil {
		// Only a document that refers to something asks for a name, so a
		// nil Names would panic on some documents and not on others.
		panic("richtext: Segments needs the Names of what a document refers to, not nil")
	}
	sg := segmenter{names: names, segs: Segments{}}
	for i, b := range d.Blocks {
		switch b := b.(type) {
		case *Paragraph:
			if b == nil {
				continue
			}
			if i > 0 {
				sg.segs = append(sg.segs, Segment{Kind: SegmentParagraph})
			}
			sg.inlines(b.Inlines)
		case *List:
			if b != nil {
				sg.list(b, 1)
			}
		}
	}
	return sg.segs
}

type segmenter struct {
	names Names
	segs  Segments
}

// list adds the segments of list l, whose items are at depth.
func (sg *segmenter) list(l *List, depth int) {
	for i, item := range l.Items {
		s := Segment{Kind: SegmentItem, Depth: depth, List: ListBullet}
		if l.Ordered {
			s.List, s.Number = ListOrdered, l.Start+i
		}
		sg.segs = append(sg.segs, s)
		for j, b := range item.Blocks {
			switch b := b.(type) {
			case *Paragraph:
				if b == nil {
					continue
				}
				if j > 0 {
					sg.segs = append(sg.segs, Segment{Kind: SegmentParagraph, Depth: depth})
				}
				sg.inlines(b.Inlines)
			case *List:
				if b != nil {
					sg.list(b, depth+1)
				}
			}
		}
	}
}

// inlines adds the segments of a paragraph's inline nodes, one for each node
// of the paragraph's canonical form (canonicalInlines), so that a paragraph
// that a program built derives what it derives once stored, and none for a
// nil node.
func (sg *segmenter) inlines(inlines []Inline) {
	for _, in := range canonicalInlines(inlines) {
		var s Segment
		switch in := in.(type) {
		case *Text:
			if in == nil {
				continue
			}
			s = Segment{Kind: SegmentText, Marks: in.Marks.names(), Href: in.Marks.Link, Value: in.Text}
		case *Break:
			if in == nil {
				continue
			}
			s = Segment{Kind: SegmentBreak}
		case *Mention:
			if in == nil {
				continue
			}
			s = Segment{Kind: SegmentMention, Marks: in.Marks.names(), Href: in.Marks.Link,
				Ref: in.Component, Value: sg.names.Component(in.Component)}
		case *Value:
			if in == nil {
				continue
			}
			s = Segment{Kind: SegmentValue, Marks: in.Marks.names(), Href: in.Marks.Link,
				Ref: in.Ref, Attribute: in.Attribute, Name: sg.names.Value(in.Ref, in.Attribute)}
			if in.Decimals != nil {
				d := *in.Decimals
				s.Decimals = &d
			}
		default:
			continue
		}
		sg.segs = append(sg.segs, s)
	}
}

// PlainText returns the plain text of a document's segments, as the text
// export shows it: its texts and the names of its mentions, each value as its
// name in braces, as {met_elara}, and a line feed for each paragraph and
// break segment, and for each item segment but one that is the first
// segment, so that a document that starts with a list starts with its first
// item, and one whose first segment is a paragraph segment, after an empty
// first paragraph, or a break segment starts with a line feed. A list item
// starts with "• ", or its number and ". ", after two spaces for each list
// that holds its list, and a paragraph or a line break within an item is
// indented by two spaces for each depth, as far as a bulleted item's text
// but not a numbered one's.
func PlainText(segs []Segment) string {
	var b strings.Builder
	depth := 0
	for i, s := range segs {
		switch s.Kind {
		case SegmentText, SegmentMention:
			b.WriteString(s.Value)
		case SegmentValue:
			b.WriteString("{" + s.Name + "}")
		case SegmentBreak:
			b.WriteByte('\n')
			b.WriteString(indent(depth))
		case SegmentParagraph:
			depth = s.Depth
			b.WriteByte('\n')
			b.WriteString(indent(depth))
		case SegmentItem:
			depth = s.Depth
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(indent(depth - 1))
			if s.List == ListOrdered {
				b.WriteString(strconv.Itoa(s.Number) + ". ")
			} else {
				b.WriteString("• ")
			}
		}
	}
	return b.String()
}

// indent returns two spaces for each of n levels, and none for a level below
// 1, which a segment that a program builds may have.
func indent(n int) string {
	return strings.Repeat("  ", max(n, 0))
}

// WordCount returns the number of words of a document's segments: the words
// (CountWords) of its texts, mentions' names, and values' names in braces,
// each paragraph, list item, and line break separating them, without the
// bullets and numbers of its list items. A word may run across segments, as
// a word whose half is bold does.
func WordCount(segs []Segment) int {
	var w words
	for _, s := range segs {
		switch s.Kind {
		case SegmentText, SegmentMention:
			w.read(s.Value)
		case SegmentValue:
			w.read("{")
			w.read(s.Name)
			w.read("}")
		default:
			w.inWord = false
		}
	}
	return w.n
}

// CountWords returns the number of words of s. A word is a run of characters
// that are not white space (Unicode's White_Space) with at least one letter
// or number among them, so a dash between spaces is no word, and a run of
// Chinese or Japanese without spaces is one.
func CountWords(s string) int {
	var w words
	w.read(s)
	return w.n
}

// words counts the words of the text that it reads, as CountWords does,
// whether it reads the text whole or in parts.
type words struct {
	// n is the number of words so far.
	n int
	// inWord is whether the last character read is not white space, and
	// counted whether the word that it is part of is counted already.
	inWord, counted bool
}

func (w *words) read(s string) {
	for _, r := range s {
		if unicode.IsSpace(r) {
			w.inWord = false
			continue
		}
		if !w.inWord {
			w.inWord, w.counted = true, false
		}
		if !w.counted && (unicode.IsLetter(r) || unicode.IsNumber(r)) {
			w.n++
			w.counted = true
		}
	}
}

// PreviewChars is the most characters of a preview, before its ellipsis.
const PreviewChars = 100

// Preview returns the preview of a plain text: the text with each run of
// white space written as one space and none around it, and, when that is
// longer than PreviewChars code points, its first PreviewChars, back to the
// last space among them when the cut falls within a word, without the
// spaces at its end, and an ellipsis (…).
func Preview(plain string) string {
	// The text collapsed, as far as the code point after the first
	// PreviewChars, which says whether the cut falls within a word.
	var b strings.Builder
	n := 0
	for field := range strings.FieldsSeq(plain) {
		if n > 0 {
			b.WriteByte(' ')
			n++
		}
		b.WriteString(field)
		n += utf8.RuneCountInString(field)
		if n > PreviewChars {
			break
		}
	}
	collapsed := b.String()
	if n <= PreviewChars {
		return collapsed
	}
	cut := 0
	for range PreviewChars {
		_, size := utf8.DecodeRuneInString(collapsed[cut:])
		cut += size
	}
	head := collapsed[:cut]
	if collapsed[cut] != ' ' {
		if i := strings.LastIndexByte(head, ' '); i > 0 {
			head = head[:i]
		}
	}
	return strings.TrimRight(head, " ") + "…"
}

// Hash returns the hash of a plain text, as the text export shows it
// (FR-TEXT-05): "sha256:" and the lowercase hexadecimal SHA-256 of its UTF-8.
func Hash(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return "sha256:" + hex.EncodeToString(sum[:])
}
