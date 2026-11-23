package richtext

import (
	"encoding/json"
	"fmt"
)

// FromSegments returns the document that segs, a text's derived segments, were
// derived from, as far as they say: its paragraphs, its lists and their items,
// its formatting, its links, and its references, which is all of a document
// but a run of its text split differently, or a mark on a line break, which
// nothing derived from a document shows. An archive of a release holds its
// texts as segments alone, so an import builds the draft's from them. It
// returns nil for no segments, and an *Error, as Parse does, for segments that
// no document derives.
func FromSegments(segs Segments) (*Doc, error) {
	if len(segs) == 0 {
		return nil, nil
	}
	b := &segmentBuilder{doc: &Doc{}}
	for i, s := range segs {
		if err := b.add(s); err != nil {
			return nil, fmt.Errorf("richtext: segment %d: %w", i, err)
		}
	}
	data, err := json.Marshal(*b.doc)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// segmentBuilder builds a document from its segments in order.
type segmentBuilder struct {
	doc *Doc
	// lists are the lists that are open, the outermost first, so that an item
	// of depth d belongs to lists[d-1].
	lists []*List
	// p is the paragraph that inline segments go into, or nil before the
	// document's first.
	p *Paragraph
}

func (b *segmentBuilder) add(s Segment) error {
	switch s.Kind {
	case SegmentText:
		b.inline(&Text{Text: s.Value, Marks: marksOf(s)})
	case SegmentMention:
		b.inline(&Mention{Component: s.Ref, Marks: marksOf(s)})
	case SegmentValue:
		b.inline(&Value{Ref: s.Ref, Attribute: s.Attribute, Decimals: s.Decimals, Marks: marksOf(s)})
	case SegmentBreak:
		b.inline(&Break{})
	case SegmentParagraph:
		return b.paragraph(s.Depth)
	case SegmentItem:
		return b.item(s)
	default:
		return fmt.Errorf("no segment is of the kind %q", s.Kind)
	}
	return nil
}

func marksOf(s Segment) Marks {
	m := Marks{Link: s.Href}
	for _, name := range s.Marks {
		switch name {
		case markBold:
			m.Bold = true
		case markCode:
			m.Code = true
		case markItalic:
			m.Italic = true
		case markStrike:
			m.Strike = true
		}
	}
	return m
}

// inline adds an inline node to the paragraph that is open, or to the
// document's first, which no segment begins.
func (b *segmentBuilder) inline(in Inline) {
	if b.p == nil {
		b.p = &Paragraph{}
		b.doc.Blocks = append(b.doc.Blocks, b.p)
	}
	b.p.Inlines = append(b.p.Inlines, in)
}

// paragraph begins a paragraph, of the item at depth, or of the document at
// depth 0, closing the lists below it.
func (b *segmentBuilder) paragraph(depth int) error {
	if depth < 0 || depth > len(b.lists) {
		return fmt.Errorf("a paragraph of depth %d, within %d lists", depth, len(b.lists))
	}
	b.lists = b.lists[:depth]
	b.p = &Paragraph{}
	if depth == 0 {
		b.doc.Blocks = append(b.doc.Blocks, b.p)
		return nil
	}
	list := b.lists[depth-1]
	if len(list.Items) == 0 {
		return fmt.Errorf("a paragraph of a list without an item")
	}
	it := &list.Items[len(list.Items)-1]
	it.Blocks = append(it.Blocks, b.p)
	return nil
}

// item begins an item of the list at its depth, which continues the list that
// is open there if it is of the kind, and numbered on from it, or else starts
// another.
func (b *segmentBuilder) item(s Segment) error {
	if s.Depth < 1 || s.Depth > len(b.lists)+1 {
		return fmt.Errorf("an item of depth %d, within %d lists", s.Depth, len(b.lists))
	}
	ordered := s.List == ListOrdered
	b.lists = b.lists[:min(len(b.lists), s.Depth)]
	var list *List
	if len(b.lists) == s.Depth {
		open := b.lists[s.Depth-1]
		if open.Ordered == ordered && (!ordered || s.Number == open.Start+len(open.Items)) {
			list = open
		} else {
			b.lists = b.lists[:s.Depth-1]
		}
	}
	if list == nil {
		list = &List{Ordered: ordered, Start: 1}
		if ordered {
			list.Start = s.Number
		}
		if s.Depth == 1 {
			b.doc.Blocks = append(b.doc.Blocks, list)
		} else {
			outer := b.lists[s.Depth-2]
			if len(outer.Items) == 0 {
				return fmt.Errorf("a list within a list without an item")
			}
			it := &outer.Items[len(outer.Items)-1]
			it.Blocks = append(it.Blocks, list)
		}
		b.lists = append(b.lists, list)
	}
	b.p = &Paragraph{}
	list.Items = append(list.Items, Item{Blocks: []Block{b.p}})
	return nil
}
