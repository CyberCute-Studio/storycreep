package richtext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// paragraphs returns a document of a paragraph of each text.
func paragraphs(texts ...string) []byte {
	var b strings.Builder
	b.WriteString(`{"type":"doc","content":[`)
	for i, text := range texts {
		if i > 0 {
			b.WriteByte(',')
		}
		s, _ := json.Marshal(text)
		b.WriteString(`{"type":"paragraph","content":[{"type":"text","text":` + string(s) + `}]}`)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// lists returns a document of lists nested depth deep, each item a paragraph
// and the next list.
func lists(depth int) []byte {
	var b strings.Builder
	b.WriteString(`{"type":"doc","content":[`)
	for range depth {
		b.WriteString(`{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph"},`)
	}
	b.WriteString(`{"type":"paragraph"}`)
	for range depth {
		b.WriteString(`]}]}`)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// marshal encodes v as the published format's serializer does, without
// escaping HTML (§6.2), which json.Marshal would escape in what a MarshalJSON
// method writes too.
func marshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := marshalNoEscape(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// wantError checks that err is an *Error of reason at pointer.
func wantError(t *testing.T, err error, reason Reason, pointer string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v, want an *Error", err)
	}
	if e.Reason != reason || e.Pointer != pointer {
		t.Errorf("%s at %q (%s), want %s at %q", e.Reason, e.Pointer, e.Message, reason, pointer)
	}
}

// sized returns a document in its canonical form of n bytes, of empty
// paragraphs and a last paragraph of text.
func sized(n int) []byte {
	const (
		head, tail = `{"content":[`, `],"type":"doc"}`
		empty      = `{"type":"paragraph"},`
		textHead   = `{"content":[{"text":"`
		textTail   = `","type":"text"}],"type":"paragraph"}`
	)
	rest := n - len(head) - len(tail) - len(textHead) - len(textTail)
	m := (rest - 1) / len(empty)
	return []byte(head + strings.Repeat(empty, m) + textHead + strings.Repeat("x", rest-m*len(empty)) + textTail + tail)
}

// TestLimits checks the limits of a document, at and over each: its bytes,
// the characters of its text, how deeply its lists nest, and how long a link
// is.
//
// Verifies: NFR-SEC-07
func TestLimits(t *testing.T) {
	t.Run("bytes", func(t *testing.T) {
		doc := sized(MaxBytes)
		if len(doc) != MaxBytes {
			t.Fatalf("sized wrote %d bytes", len(doc))
		}
		parsed, err := Parse(doc)
		if err != nil {
			t.Fatalf("a document of %d bytes: %v", MaxBytes, err)
		}
		if got := marshal(t, parsed); !bytes.Equal(got, doc) {
			t.Error("a document of MaxBytes in its canonical form is written otherwise")
		}
		_, err = Parse(sized(MaxBytes + 1))
		wantError(t, err, TooLarge, "")

		// The white space between tokens is not counted, so that the same
		// document is within the limit however it is laid out, as jsonb
		// writes it, with a space after each colon and comma, or indented.
		spaced := strings.NewReplacer(`:`, `: `, `,`, `, `).Replace(string(doc))
		var indented bytes.Buffer
		if err := json.Indent(&indented, doc, "", "\t"); err != nil {
			t.Fatal(err)
		}
		for name, in := range map[string][]byte{"jsonb's": []byte(spaced), "indented": indented.Bytes()} {
			if len(in) <= MaxBytes {
				t.Fatalf("%s layout adds no bytes", name)
			}
			if _, err := Parse(in); err != nil {
				t.Errorf("a document of %d bytes, as %s layout writes it: %v", MaxBytes, name, err)
			}
		}

		// A canonical form longer than the JSON that Parse read, as an
		// ordered list's start of 9e5, which is 900000, is within the limit
		// too, so that every document that Parse returns can be written and
		// read again.
		list := func(start string) []byte {
			const item = `,{"attrs":{"start":%s},"content":[{"content":[{"type":"paragraph"}],"type":"listItem"}],"type":"orderedList"}`
			n := (MaxBytes - len(`{"content":[{"type":"paragraph"}],"type":"doc"}`)) / len(fmt.Sprintf(item, start))
			return []byte(`{"content":[{"type":"paragraph"}` + strings.Repeat(fmt.Sprintf(item, start), n) + `],"type":"doc"}`)
		}
		_, err = Parse(list("9e5"))
		wantError(t, err, TooLarge, "")
		parsed, err = Parse(list("900000"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := json.Marshal(parsed); err != nil {
			t.Errorf("a document that Parse returned does not marshal: %v", err)
		}
	})
	t.Run("characters", func(t *testing.T) {
		// Characters are code points, of all the texts together.
		half := strings.Repeat("ž", MaxChars/2)
		if _, err := Parse(paragraphs(half, half)); err != nil {
			t.Errorf("a document of %d characters: %v", MaxChars, err)
		}
		_, err := Parse(paragraphs(half, half, "x"))
		wantError(t, err, TooLarge, "")
	})
	t.Run("list depth", func(t *testing.T) {
		if _, err := Parse(lists(MaxListDepth)); err != nil {
			t.Errorf("lists nested %d deep: %v", MaxListDepth, err)
		}
		_, err := Parse(lists(MaxListDepth + 1))
		wantError(t, err, TooDeep, "/content/0"+strings.Repeat("/content/0/content/1", MaxListDepth))
		// Far deeper lists are refused at the first one too deep, without
		// reading the rest, as long as encoding/json reads them: it refuses
		// JSON nested more than 10,000 deep, which TestParseJSON tries.
		_, err = Parse(lists(2000))
		wantError(t, err, TooDeep, "/content/0"+strings.Repeat("/content/0/content/1", MaxListDepth))
	})
	t.Run("joined texts", func(t *testing.T) {
		// A paragraph of the most texts with the same marks that fit in
		// MaxBytes is joined in time and memory that grow with its length,
		// not with its length times the number of texts.
		const node = `{"type":"text","text":"ab"},`
		head, tail := `{"type":"doc","content":[{"type":"paragraph","content":[`, `{"type":"text","text":"ab"}]}]}`
		n := (MaxBytes - len(head) - len(tail)) / len(node)
		doc := []byte(head + strings.Repeat(node, n) + tail)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		parsed, err := Parse(doc)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatal(err)
		}
		if got := parsed.Blocks[0].(*Paragraph).Inlines; len(got) != 1 || got[0].(*Text).Text != strings.Repeat("ab", n+1) {
			t.Errorf("%d texts are not joined into one", n+1)
		}
		// Reading the JSON allocates about 20 times its size; joining
		// each text onto the run so far allocated gigabytes.
		if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 100*MaxBytes {
			t.Errorf("Parse allocated %d MB to join %d texts", allocated>>20, n+1)
		}
	})
	t.Run("link", func(t *testing.T) {
		link := func(href string) []byte {
			return []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x",` +
				`"marks":[{"type":"link","attrs":{"href":"` + href + `"}}]}]}]}`)
		}
		href := "https://example.com/" + strings.Repeat("a", MaxHrefChars-len("https://example.com/"))
		if _, err := Parse(link(href)); err != nil {
			t.Errorf("a link of %d characters: %v", MaxHrefChars, err)
		}
		_, err := Parse(link(href + "a"))
		wantError(t, err, Link, "/content/0/content/0/marks/0/attrs/href")
	})
}

// TestParseJSON checks that Parse refuses input that is not one JSON value
// in UTF-8.
func TestParseJSON(t *testing.T) {
	for _, in := range []string{
		``,
		`   `,
		`{"type":"doc"`,
		`{"type":"doc","content":[{"type":"paragraph"}]} {}`,
		`{"type":"doc","content":[{"type":"paragraph"}]},`,
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a` + "\xff" + `"}]}]}`,
		`{'type':'doc'}`,
		strings.Repeat(`[`, 20000) + strings.Repeat(`]`, 20000),
	} {
		_, err := Parse([]byte(in))
		wantError(t, err, InvalidJSON, "")
	}
	if _, err := Parse([]byte(" \n{\"type\":\"doc\",\"content\":[{\"type\":\"paragraph\"}]}\n ")); err != nil {
		t.Errorf("white space around the document: %v", err)
	}
}

// TestCanonicalBytes checks the canonical form byte for byte: its keys are
// sorted, and HTML is not escaped.
func TestCanonicalBytes(t *testing.T) {
	doc, err := Parse([]byte(`{"type":"doc","content":[{"content":[{"text":"<b>&amp;</b>","type":"text",` +
		`"marks":[{"attrs":{"href":"https://example.com/?a=1&b=<2>"},"type":"link"},{"type":"italic"}]}],"type":"paragraph"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := marshal(t, doc)
	want := `{"content":[{"content":[{"marks":[{"type":"italic"},{"attrs":{"href":"https://example.com/?a=1&b=<2>"},` +
		`"type":"link"}],"text":"<b>&amp;</b>","type":"text"}],"type":"paragraph"}],"type":"doc"}`
	if string(got) != want {
		t.Errorf("canonical form\n%s\nwant\n%s", got, want)
	}
}

// TestSegmentBytes checks segments byte for byte: an inline segment always
// has its marks, other segments only their own fields, and HTML is not
// escaped (§6.2).
func TestSegmentBytes(t *testing.T) {
	zero := 0
	for _, c := range []struct {
		seg  Segment
		want string
	}{
		{Segment{Kind: SegmentText, Value: "a <b> & c"}, `{"kind":"text","marks":[],"value":"a <b> & c"}`},
		{Segment{Kind: SegmentText, Value: "x", Marks: []string{"bold", "link"}, Href: "https://e.com/?a&b"},
			`{"href":"https://e.com/?a&b","kind":"text","marks":["bold","link"],"value":"x"}`},
		{Segment{Kind: SegmentMention, Ref: "cmp_1", Value: ""}, `{"kind":"mention","marks":[],"ref":"cmp_1","value":""}`},
		{Segment{Kind: SegmentValue, Ref: "var_1", Name: "gold", Decimals: &zero},
			`{"decimals":0,"kind":"value","marks":[],"name":"gold","ref":"var_1"}`},
		{Segment{Kind: SegmentBreak, Depth: 2}, `{"kind":"break"}`},
		{Segment{Kind: SegmentParagraph}, `{"kind":"paragraph"}`},
		{Segment{Kind: SegmentParagraph, Depth: 1}, `{"depth":1,"kind":"paragraph"}`},
		{Segment{Kind: SegmentItem, Depth: 1, List: ListBullet, Number: 4}, `{"depth":1,"kind":"item","list":"bullet"}`},
		{Segment{Kind: SegmentItem, Depth: 2, List: ListOrdered}, `{"depth":2,"kind":"item","list":"ordered","number":0}`},
	} {
		if got := marshal(t, c.seg); string(got) != c.want {
			t.Errorf("%+v: %s, want %s", c.seg, got, c.want)
		}
	}
}

// TestUnmarshalJSON checks that a field of a request can hold a document,
// which is read as Parse reads it, with its errors.
func TestUnmarshalJSON(t *testing.T) {
	var payload struct {
		Text *Doc `json:"text"`
	}
	if err := json.Unmarshal([]byte(`{"text":{"type":"doc","content":[{"type":"paragraph","content":[`+
		`{"type":"text","text":"a"},{"type":"text","text":"b"}]}]}}`), &payload); err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(payload.Text); string(got) != `{"content":[{"content":[{"text":"ab","type":"text"}],"type":"paragraph"}],"type":"doc"}` {
		t.Errorf("read %s", got)
	}
	err := json.Unmarshal([]byte(`{"text":{"type":"doc","content":[{"type":"heading"}]}}`), &payload)
	wantError(t, err, UnknownNode, "/content/0")

	// null leaves a field that holds a Doc as it is, as encoding/json
	// leaves any other.
	var field struct {
		Text Doc `json:"text"`
	}
	if err := json.Unmarshal([]byte(`{"text":null}`), &field); err != nil || field.Text.Blocks != nil {
		t.Errorf("null: %v, %+v", err, field.Text)
	}
	_, err = Parse([]byte(`null`))
	wantError(t, err, WrongType, "")
}

// TestMarshalBuilt checks the canonical form of a Doc that a program builds,
// as an import does: the zero Doc is the empty document, a Doc that Parse
// would refuse is refused rather than written, whatever Parse refuses in it,
// and a Doc that Parse accepts is written in its canonical form.
//
// Verifies: NFR-SEC-08
func TestMarshalBuilt(t *testing.T) {
	if got := marshal(t, Doc{}); string(got) != `{"content":[{"type":"paragraph"}],"type":"doc"}` {
		t.Errorf("the zero Doc: %s", got)
	}
	para := func(inlines ...Inline) Doc { return Doc{Blocks: []Block{&Paragraph{Inlines: inlines}}} }
	item := func(blocks ...Block) Item { return Item{Blocks: blocks} }
	nested := &List{Items: []Item{item(&Paragraph{})}}
	for range MaxListDepth {
		nested = &List{Items: []Item{item(&Paragraph{}, nested)}}
	}
	five := 5
	nine := 9
	var nilParagraph *Paragraph
	for name, d := range map[string]Doc{
		"a nil block":              {Blocks: []Block{nil}},
		"a nil paragraph":          {Blocks: []Block{nilParagraph}},
		"a nil list":               {Blocks: []Block{(*List)(nil)}},
		"a list without items":     {Blocks: []Block{&List{}}},
		"an item without blocks":   {Blocks: []Block{&List{Items: []Item{{}}}}},
		"an item that is a list":   {Blocks: []Block{&List{Items: []Item{item(&List{Items: []Item{item(&Paragraph{})}})}}}},
		"an empty text":            para(&Text{}),
		"a nil inline":             para(nil),
		"a nil text in a list":     {Blocks: []Block{&List{Items: []Item{item(&Paragraph{Inlines: []Inline{(*Text)(nil)}})}}}},
		"a nil value in paragraph": para((*Value)(nil)),
		"lists nested too deep":    {Blocks: []Block{nested}},
		"a line feed in text":      para(&Text{Text: "a\nb"}),
		"too much text":            para(&Text{Text: strings.Repeat("a", MaxChars+1)}),
		"a javascript: link":       para(&Text{Text: "a", Marks: Marks{Link: "javascript:alert(1)"}}),
		"code with bold":           para(&Text{Text: "a", Marks: Marks{Code: true, Bold: true}}),
		"a start below 0":          {Blocks: []Block{&List{Ordered: true, Start: -5, Items: []Item{item(&Paragraph{})}}}},
		"a mention of no ID":       para(&Mention{Component: "nope"}),
		"a mention of a variable":  para(&Mention{Component: "var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"}),
		"an attribute of a variable": para(&Value{Ref: "var_01HQ8Z5BRT9C4F2M7XKDPV6NHA",
			Attribute: "atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF"}),
		"a component without its attribute": para(&Value{Ref: "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF"}),
		"too many decimals":                 para(&Value{Ref: "var_01HQ8Z5BRT9C4F2M7XKDPV6NHA", Decimals: &nine}),
	} {
		if got, err := json.Marshal(d); err == nil {
			t.Errorf("%s: %s", name, got)
		}
	}
	got := marshal(t, Doc{Blocks: []Block{
		&Paragraph{Inlines: []Inline{&Text{Text: "a"}, &Text{Text: "b"}, &Mention{Component: "CMP_01hq8z7kd3m9p2w5xrtvnq4bcf"},
			&Value{Ref: "var_01hq8z5brt9c4f2m7xkdpv6nha", Decimals: &five}}},
		&List{Ordered: true, Start: 1, Items: []Item{item(&Paragraph{})}},
	}})
	want := `{"content":[{"content":[{"text":"ab","type":"text"},{"attrs":{"id":"cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF"},` +
		`"type":"mention"},{"attrs":{"decimals":5,"ref":"var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"},"type":"valueRef"}],` +
		`"type":"paragraph"},{"content":[{"content":[{"type":"paragraph"}],"type":"listItem"}],"type":"orderedList"}],"type":"doc"}`
	if string(got) != want {
		t.Errorf("a built Doc's canonical form\n%s\nwant\n%s", got, want)
	}
}

// TestMarshalParsed checks that a Doc that Parse returned is written in its
// canonical form, and that one whose nodes a program changes afterwards is
// checked and written as a built one is, though MarshalJSON writes a parsed
// Doc without reading it again.
//
// Verifies: NFR-SEC-08
func TestMarshalParsed(t *testing.T) {
	const canonical = `{"content":[{"content":[{"text":"a","type":"text"}],"type":"paragraph"}],"type":"doc"}`
	doc, err := Parse([]byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a","marks":[]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := marshal(t, doc); string(got) != canonical {
		t.Errorf("a parsed Doc: %s", got)
	}
	para := doc.Blocks[0].(*Paragraph)
	text := para.Inlines[0].(*Text)
	text.Marks.Link = "javascript:alert(1)"
	if got, err := json.Marshal(doc); err == nil {
		t.Errorf("a parsed Doc given a javascript: link: %s", got)
	}
	text.Marks.Link = ""
	text.Text = "a\nb"
	if got, err := json.Marshal(doc); err == nil {
		t.Errorf("a parsed Doc given a line feed in its text: %s", got)
	}
	text.Text = "a"
	para.Inlines = append(para.Inlines, &Text{Text: "b"})
	if got := marshal(t, doc); string(got) != strings.Replace(canonical, `"a"`, `"ab"`, 1) {
		t.Errorf("a parsed Doc given a text: %s", got)
	}
}

// TestUnknownFieldOrder checks that of several fields that an object may not
// have, the error names the first in sorted order, whether it is a type in
// attributes, which have none, or another.
func TestUnknownFieldOrder(t *testing.T) {
	for doc, pointer := range map[string]string{
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"mention","attrs":` +
			`{"type":"x","id":"cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF","aaa":1}}]}]}`: "/content/0/content/0/attrs/aaa",
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"mention","attrs":` +
			`{"type":"x","id":"cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF","zzz":1}}]}]}`: "/content/0/content/0/attrs/type",
		`{"type":"doc","content":[{"type":"orderedList","attrs":{"type":"x","start":2,"label":"a"},` +
			`"content":[{"type":"listItem","content":[{"type":"paragraph"}]}]}]}`: "/content/0/attrs/label",
		`{"type":"doc","content":[{"type":"paragraph","zzz":1,"attrs":{}}]}`: "/content/0/attrs",
	} {
		_, err := Parse([]byte(doc))
		wantError(t, err, UnknownField, pointer)
	}
}

// TestPlainTextBuilt checks that PlainText takes segments that a program
// builds, without a depth, as it takes those of a document.
func TestPlainTextBuilt(t *testing.T) {
	got := PlainText([]Segment{{Kind: SegmentItem, List: ListBullet}, {Kind: SegmentText, Value: "a"},
		{Kind: SegmentParagraph, Depth: -1}, {Kind: SegmentText, Value: "b"}})
	if got != "• a\nb" {
		t.Errorf("PlainText = %q", got)
	}
}

// TestCheckHref checks the links' allowlist: http and https URLs with a host,
// and mailto URLs, and nothing that could run a script or read a file.
//
// Verifies: NFR-SEC-08
func TestCheckHref(t *testing.T) {
	for _, href := range []string{
		"https://example.com",
		"http://example.com/a?b=c#d",
		"HTTPS://EXAMPLE.COM/",
		"https://user@example.com:8080/path",
		"https://user:pass%40word@example.com/",
		"https://[::1]/",
		"https://[2001:db8::1]:8443/a",
		"https://example.com:/",
		"https://cs.wikipedia.org/wiki/Příliš",
		"https://příliš.example/",
		"https://example.com/a%C3%A9?q=%20#%2F",
		// What a browser escapes in a path, a query, or a fragment.
		"https://example.com/a|b?q={x}&r=\"<y>\"#^`",
		"https://example.com/a\\b",
		"mailto:keeper@example.com",
		"MAILTO:keeper@example.com?subject=Light",
		"mailto:/keeper",
	} {
		if problem := CheckHref(href); problem != "" {
			t.Errorf("CheckHref(%q) = %q, want none", href, problem)
		}
	}
	for _, href := range []string{
		"",
		"javascript:alert(1)",
		"JavaScript:alert(1)",
		"vbscript:msgbox(1)",
		"data:text/html;base64,PHNjcmlwdD4=",
		"blob:https://example.com/1",
		"file:///C:/Windows/win.ini",
		"about:blank",
		"//example.com/a",
		"/a",
		"#top",
		" https://example.com",
		"https://example.com/\u00a0",
		"https://example.com/\ufeff",
		"https://example.com/\u2028",
		"https://exa\tmple.com",
		"https:example.com",
		"https://",
		"https://:80/",
		"https://user@:80/",
		"http://[]/",
		"mailto:",
		"https://example.com:port",
		"https://example.com:-1/",
		"https://example.com:80:80/",
		// A % that starts no escape, wherever it is.
		"https://example.com/%zz",
		"https://example.com/?q=100%",
		"https://example.com/#%2",
		"https://u%zz@example.com/",
		"mailto:keeper%zz@example.com",
		// A host or userinfo with what neither holds, as a backslash, which
		// a browser reads as a slash, so that the host would be another.
		"https://example.com\\@evil.example/",
		"https://evil.example\\example.com/",
		"https://exa<mple.com/",
		"https://exa{mple.com/",
		"https://exa%41mple.com/",
		"https://exa[mple.com/",
		"https://[example.com]/",
		"https://[::1/",
		"https://[::1]x/",
		"https://[::1%25eth0]/",
		// A scheme that only Unicode's case folding makes https: U+017F,
		// the long s, folds to s.
		"http" + string(rune(0x017F)) + "://example.com/",
		// Format characters, which hide or reorder what a reader sees.
		"https://exa" + string(rune(0x202E)) + "moc.elpm/",
		"https://example.com/" + string(rune(0x200B)),
		"https://example.com/" + string(rune(0x00AD)) + "a",
		"https://example.com/" + string(rune(0x2066)) + "a",
	} {
		if CheckHref(href) == "" {
			t.Errorf("CheckHref(%q) accepts it", href)
		}
	}
}

// TestLoneSurrogate checks that a string that escapes a lone surrogate is
// refused wherever it is, which encoding/json would read as U+FFFD, and that
// an escaped pair is the character it stands for.
//
// Verifies: NFR-SEC-07
func TestLoneSurrogate(t *testing.T) {
	text := func(s string) []byte {
		return []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"` + s + `"}]}]}`)
	}
	for _, s := range []string{`\ud800`, `a\uD800b`, `\udc00`, `\ud800A`, `\ud800\ud800`, `\udfff\ud800`, `a\ud83d`} {
		_, err := Parse(text(s))
		wantError(t, err, InvalidJSON, "")
	}
	_, err := Parse([]byte(`{"type":"doc","content":[{"type":"paragraph","\ud800":1}]}`))
	wantError(t, err, InvalidJSON, "")
	for s, want := range map[string]string{
		`🐴`:       string(rune(0x1F434)),
		`🐴!`:      string(rune(0x1F434)) + "!",
		`\\ud800`: `\ud800`,
		`\\🐴`:     `\` + string(rune(0x1F434)),
		`é\"퟿`:    "é\"" + string(rune(0xD7FF)),
	} {
		doc, err := Parse(text(s))
		if err != nil {
			t.Errorf("%s: %v", s, err)
			continue
		}
		if got := doc.Blocks[0].(*Paragraph).Inlines[0].(*Text).Text; got != want {
			t.Errorf("%s: %q, want %q", s, got, want)
		}
	}
}

func TestErrorString(t *testing.T) {
	e := &Error{Pointer: "/content/0", Reason: UnknownNode, Message: `"heading" is not a node of the schema`}
	if got := e.Error(); got != `rich text: /content/0: "heading" is not a node of the schema` {
		t.Errorf("Error() = %s", got)
	}
	e = &Error{Reason: TooLarge, Message: "the document is longer than 1048576 bytes"}
	if got := e.Error(); got != "rich text: the document: the document is longer than 1048576 bytes" {
		t.Errorf("Error() = %s", got)
	}
}

// TestPointerEscapes checks that a field's name is escaped in the pointer
// that names it (RFC 6901).
func TestPointerEscapes(t *testing.T) {
	_, err := Parse([]byte(`{"type":"doc","content":[{"type":"paragraph","a/b~c":1}]}`))
	wantError(t, err, UnknownField, "/content/0/a~1b~0c")
}

// reasons are every Reason.
var reasons = []Reason{InvalidJSON, WrongType, UnknownNode, UnknownMark, UnknownField, MissingField, Content,
	EmptyText, ControlCharacter, DuplicateMark, ExcludedMark, Link, Reference, OutOfRange, TooDeep, TooLarge}

// resolve returns the value at pointer in v, as JSON decodes it.
func resolve(v any, pointer string) (any, bool) {
	if pointer == "" {
		return v, true
	}
	for _, tok := range strings.Split(pointer[1:], "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch x := v.(type) {
		case map[string]any:
			var ok bool
			if v, ok = x[tok]; !ok {
				return nil, false
			}
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// FuzzParse checks that Parse never panics, that its canonical form is a
// fixed point that the schema accepts and that derives without panicking, and
// that every error has a known reason and points at a value of the input.
func FuzzParse(f *testing.F) {
	v := readVectors(f)
	for _, vec := range v.Valid {
		f.Add([]byte(vec.Doc))
	}
	for _, vec := range v.Invalid {
		f.Add([]byte(vec.Doc))
	}
	names := v.names()
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := Parse(data)
		if err != nil {
			var e *Error
			if !errors.As(err, &e) || !slices.Contains(reasons, e.Reason) || e.Message == "" {
				t.Fatalf("error %#v", err)
			}
			if e.Reason == InvalidJSON || e.Reason == TooLarge {
				return
			}
			var in any
			dec := json.NewDecoder(bytes.NewReader(data))
			dec.UseNumber()
			if err := dec.Decode(&in); err != nil {
				t.Fatalf("Parse decoded what encoding/json does not: %v", err)
			}
			if _, ok := resolve(in, e.Pointer); !ok {
				t.Fatalf("%s points at %q, which the input does not have", e.Reason, e.Pointer)
			}
			return
		}
		canonical, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Parse(canonical)
		if err != nil {
			t.Fatalf("the canonical form %s is refused: %v", canonical, err)
		}
		if b, _ := json.Marshal(again); !bytes.Equal(b, canonical) {
			t.Fatalf("the canonical form %s is not a fixed point: %s", canonical, b)
		}
		d := doc.Derive(names)
		if d.Text != PlainText(d.Segments) || d.Hash != Hash(d.Text) {
			t.Fatal("Derive disagrees with PlainText and Hash")
		}
	})
}
