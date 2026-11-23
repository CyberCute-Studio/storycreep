package richtext

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestFromSegments checks that a document built from a text's segments derives
// the same segments, so that an import of a release's texts, which it holds as
// segments alone, keeps their paragraphs, lists, marks, links, and references.
//
// Verifies: FR-DATA-01/AC1
func TestFromSegments(t *testing.T) {
	t.Parallel()
	text := func(s string, marks ...string) map[string]any {
		n := map[string]any{"type": "text", "text": s}
		if len(marks) > 0 {
			var ms []map[string]any
			for _, m := range marks {
				ms = append(ms, map[string]any{"type": m})
			}
			n["marks"] = ms
		}
		return n
	}
	para := func(in ...any) map[string]any {
		return map[string]any{"type": "paragraph", "content": in}
	}
	item := func(blocks ...any) map[string]any { return map[string]any{"type": "listItem", "content": blocks} }
	bullets := func(items ...any) map[string]any { return map[string]any{"type": "bulletList", "content": items} }
	ordered := func(start int, items ...any) map[string]any {
		return map[string]any{"type": "orderedList", "attrs": map[string]any{"start": start}, "content": items}
	}
	link := map[string]any{"type": "text", "text": "site", "marks": []map[string]any{{"type": "link",
		"attrs": map[string]any{"href": "https://example.com/"}}}}
	for name, content := range map[string][]any{
		"one paragraph":     {para(text("The quay, at night."))},
		"marks and a link":  {para(text("bold", "bold"), text(" and "), text("code", "code"), text(" "), link)},
		"paragraphs":        {para(text("One")), para(text("Two")), para(text("Three"))},
		"a line break":      {para(text("One"), map[string]any{"type": "hardBreak"}, text("Two"))},
		"a list":            {bullets(item(para(text("a"))), item(para(text("b"))))},
		"a paragraph after": {bullets(item(para(text("a")))), para(text("after"))},
		"an ordered list":   {ordered(3, item(para(text("c"))), item(para(text("d"))))},
		"an item of two":    {bullets(item(para(text("a")), para(text("more"))), item(para(text("b"))))},
		"a nested list": {bullets(item(para(text("a")), ordered(1, item(para(text("a1"))), item(para(text("a2"))))),
			item(para(text("b"))))},
		"two lists": {bullets(item(para(text("a")))), ordered(5, item(para(text("e"))))},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(map[string]any{"type": "doc", "content": content})
			if err != nil {
				t.Fatal(err)
			}
			doc, err := Parse(data)
			if err != nil {
				t.Fatalf("the test's document: %v", err)
			}
			want := doc.Segments(NoNames{})
			got, err := FromSegments(want)
			if err != nil {
				t.Fatal(err)
			}
			if have := got.Segments(NoNames{}); !reflect.DeepEqual(have, want) {
				t.Errorf("the segments are\n%+v\nwant\n%+v", have, want)
			}
		})
	}
	if doc, err := FromSegments(nil); doc != nil || err != nil {
		t.Errorf("no segments: %v, %v", doc, err)
	}
}

// TestFromSegmentsRefuses checks that segments that no document derives are
// refused rather than read as some other text.
func TestFromSegmentsRefuses(t *testing.T) {
	t.Parallel()
	for name, segs := range map[string]Segments{
		"an unknown kind":      {{Kind: "nothing"}},
		"an item too deep":     {{Kind: SegmentItem, Depth: 2, List: ListBullet}},
		"a paragraph too deep": {{Kind: SegmentParagraph, Depth: 1}},
	} {
		if _, err := FromSegments(segs); err == nil {
			t.Errorf("%s was read as a document", name)
		}
	}
}
