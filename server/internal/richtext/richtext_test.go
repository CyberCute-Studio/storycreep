package richtext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
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
	data, err := schemagen.Marshal(v)
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

		// Nor is it counted how a string's characters are escaped: an escape
		// counts as the character that it stands for, so that the same
		// document is within the limit whichever characters its encoder
		// escapes, as json.Marshal escapes each & of what a MarshalJSON method
		// writes, which the commands' pipeline does to a step's payload, and
		// Python's json.dumps every character beyond ASCII.
		parsed, err = Parse(bytes.Replace(doc, []byte(`"text":"x`), []byte(`"text":"&`), 1))
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Body Doc `json:"body"`
		}
		payload.Body = *parsed
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(raw, []byte(`\u0026`)) {
			t.Fatal("json.Marshal escapes no &")
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Errorf("a document of %d bytes, with its & escaped by json.Marshal: %v", MaxBytes, err)
		}
		escaped := func(n int) []byte { return bytes.ReplaceAll(sized(n), []byte("x"), []byte(`\u0078`)) }
		if _, err := Parse(escaped(MaxBytes)); err != nil {
			t.Errorf("a document of %d bytes with each x escaped: %v", MaxBytes, err)
		}
		_, err = Parse(escaped(MaxBytes + 1))
		wantError(t, err, TooLarge, "")

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

		// The canonical form is counted as the JSON is, with each escape as
		// the character that it stands for: a document of links whose URLs
		// hold quotes, which the canonical form writes as \", two bytes each,
		// is within the limit, as its JSON is, though its canonical form has
		// more than MaxBytes bytes.
		const quotes = 2000
		head, tail := `{"content":[{"content":[`, `],"type":"paragraph"}],"type":"doc"}`
		node := func(i int) string {
			return `{"marks":[{"attrs":{"href":"https://example.com/` + fmt.Sprintf("%04d", i) +
				strings.Repeat(`\"`, quotes) + `"},"type":"link"}],"text":"x","type":"text"}`
		}
		nodes := make([]string, (MaxBytes-len(head)-len(tail))/(len(node(0))-quotes+1))
		for i := range nodes {
			nodes[i] = node(i)
		}
		quoted := []byte(head + strings.Join(nodes, ",") + tail)
		if n, _ := scan(quoted); n > MaxBytes || len(quoted) <= MaxBytes {
			t.Fatalf("the document of quoted links counts %d and has %d bytes", n, len(quoted))
		}
		parsed, err = Parse(quoted)
		if err != nil {
			t.Fatalf("a document whose canonical form has %d bytes, with each \\\" counted as \": %v", len(quoted), err)
		}
		canonical := marshal(t, parsed)
		if !bytes.Equal(canonical, quoted) {
			t.Error("the document of quoted links in its canonical form is written otherwise")
		}
		if _, err := Parse(canonical); err != nil {
			t.Errorf("the canonical form of the document of quoted links: %v", err)
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
		var parsed *Doc
		var err error
		used := allocated(func() { parsed, err = Parse(doc) })
		if err != nil {
			t.Fatal(err)
		}
		if got := parsed.Blocks[0].(*Paragraph).Inlines; len(got) != 1 || got[0].(*Text).Text != strings.Repeat("ab", n+1) {
			t.Errorf("%d texts are not joined into one", n+1)
		}
		// Reading the JSON allocates about 20 times its size; joining
		// each text onto the run so far allocated gigabytes.
		if used > 100*MaxBytes {
			t.Errorf("Parse allocated %d MB to join %d texts", used>>20, n+1)
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

// TestJSONLength checks how scan counts a document's JSON for MaxBytes:
// without the white space between its tokens, and with each escape in a
// string counted as the bytes of the UTF-8 of the character that it stands
// for, and an escaped surrogate pair as the one character that it stands for;
// and that it finds an escaped surrogate that is not one of a pair, which
// Parse refuses once the decoder has read the document.
func TestJSONLength(t *testing.T) {
	for in, want := range map[string]int{
		` { "a" : [ 1 , 2 ] } `:         len(`{"a":[1,2]}`),
		`"a b	c"`:                       len(`"a b	c"`),
		`"\u00e9\u00E9"`:                len(`"éé"`),
		`"\u0026\u003c\u003e"`:          len(`"&<>"`),
		`"\u2028\u4E3F"`:                len(`"` + string(rune(0x2028)) + string(rune(0x4E3F)) + `"`),
		`"\ud83d\udc34"`:                len(`"🐴"`),
		`"\" \\ \/ \b \f \n \r \t"`:     len(`"x x x x x x x x"`),
		`"\ud800"`:                      len(`"` + string(utf8.RuneError) + `"`),
		`"\ud800\u0041"`:                len(`"` + string(utf8.RuneError) + `A"`),
		`"\ud83d\udc34\ud83d"`:          len(`"🐴` + string(utf8.RuneError) + `"`),
		`{"\u0074ype":"\u0064oc"}`:      len(`{"type":"doc"}`),
		`"a\`:                           len(`"a\`),
		`"\u12`:                         len(`"u12`),
		`"\u12"`:                        len(`"u12"`),
		"\"\\u00e9\"   \t\r\n" + `"\u0`: len(`"é""u0`),
	} {
		if got, _ := scan([]byte(in)); got != want {
			t.Errorf("scan(%s) = %d, want %d", in, got, want)
		}
	}
	for in, want := range map[string]bool{
		`"a\u00e9"`:                 false,
		`"\ud83d\udc34"`:            false,
		`"\\ud800"`:                 false,
		`"\ud800"`:                  true,
		`"\udc00"`:                  true,
		`"\ud800\u0041"`:            true,
		`"\ud800\\udc00"`:           true,
		`"\ud83d\udc34\ud83d"`:      true,
		`{"\udfff":1}`:              true,
		`"\ud800`:                   true,
		`["\ud83d\udc34","\ud800"]`: true,
	} {
		if _, got := scan([]byte(in)); got != want {
			t.Errorf("scan(%s) finds a lone surrogate: %v, want %v", in, got, want)
		}
	}
	// scan stops once the length is more than MaxBytes, which Parse refuses,
	// so it reads neither the rest of the length nor a lone surrogate there.
	long := []byte(`"` + strings.Repeat("a", 4*MaxBytes) + `\ud800"`)
	if got, lone := scan(long); got != MaxBytes+1 || lone {
		t.Errorf("scan of %d bytes = %d, %v, want %d, false", len(long), got, lone, MaxBytes+1)
	}
	_, err := Parse(long)
	wantError(t, err, TooLarge, "")
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
		// encoding/json writes U+FFFD for bytes that are not UTF-8, which
		// Parse would then accept.
		"a text that is not UTF-8": para(&Text{Text: "a\xffb"}),
		"a link that is not UTF-8": para(&Text{Text: "a", Marks: Marks{Link: "https://example.com/\xff"}}),
		"a break's link not UTF-8": para(&Break{Marks: Marks{Link: "https://example.com/\xc3"}}),
		"a mention not UTF-8":      para(&Mention{Component: "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF\xff"}),
		"a value's ref not UTF-8":  para(&Value{Ref: "var_\xff"}),
		"a value's attribute not UTF-8": para(&Value{Ref: "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF",
			Attribute: "atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF\xff"}),
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

	// A paragraph of as many texts of one character as a document holds
	// characters is written joined, as Parse stores it and Derive reads it,
	// though written apart, as 100,000 text nodes, it would be longer than
	// MaxBytes.
	many := make([]Inline, MaxChars)
	for i := range many {
		many[i] = &Text{Text: "a"}
	}
	got = marshal(t, Doc{Blocks: []Block{&Paragraph{Inlines: many}}})
	want = `{"content":[{"content":[{"text":"` + strings.Repeat("a", MaxChars) + `","type":"text"}],"type":"paragraph"}],` +
		`"type":"doc"}`
	if string(got) != want {
		t.Errorf("a paragraph of %d texts of one character is written as %.100s", MaxChars, got)
	}
}

// TestDeriveBuilt checks that a Doc that a program builds derives what it
// derives once it is stored, as Parse reads it back: neighbouring texts with
// the same marks are one segment, and IDs are spelt canonically, so that the
// names of what it refers to are found.
func TestDeriveBuilt(t *testing.T) {
	names := readVectors(t).names()
	two := 2
	built := Doc{Blocks: []Block{&Paragraph{Inlines: []Inline{
		&Text{Text: "a"}, &Text{Text: "b"}, &Text{Text: "c", Marks: Marks{Bold: true}}, &Text{Text: "d", Marks: Marks{Bold: true}},
		&Mention{Component: "cmp_01hq8z7kd3m9p2w5xrtvnq4bcf"}, &Text{Text: " "},
		&Value{Ref: "CMP_01HQ8Z7KD3M9P2W5XRTVNQ4BCF", Attribute: "atr_01hq8z8m4k7r2w9xtvnpq5dcbf", Decimals: &two},
		&Text{Text: "e"}, &Break{}, &Text{Text: "f"}, &Text{Text: "g"},
	}}}}
	stored, err := Parse(marshal(t, built))
	if err != nil {
		t.Fatal(err)
	}
	got, want := built.Derive(names), stored.Derive(names)
	if !bytes.Equal(marshal(t, got.Segments), marshal(t, want.Segments)) || got.Text != want.Text ||
		got.Words != want.Words || got.Hash != want.Hash {
		t.Errorf("a built Doc derives\n%s %q\nand the stored one\n%s %q", marshal(t, got.Segments), got.Text,
			marshal(t, want.Segments), want.Text)
	}
	if want.Text != "abcdElara {elara.trust}e\nfg" {
		t.Errorf("the stored Doc's text is %q", want.Text)
	}
	// Deriving leaves the built Doc as it was.
	inlines := built.Blocks[0].(*Paragraph).Inlines
	if len(inlines) != 11 || inlines[0].(*Text).Text != "a" || inlines[1].(*Text).Text != "b" ||
		inlines[4].(*Mention).Component != "cmp_01hq8z7kd3m9p2w5xrtvnq4bcf" ||
		inlines[6].(*Value).Ref != "CMP_01HQ8Z7KD3M9P2W5XRTVNQ4BCF" || inlines[6].(*Value).Attribute != "atr_01hq8z8m4k7r2w9xtvnpq5dcbf" {
		t.Error("Derive changed the built Doc")
	}
}

// TestCanonicalID checks that canonicalID spells an ID as ids.Encode does,
// in whatever case it is written, leaves what is no ID as it is, and returns
// an ID spelt canonically already without a new string, as Derive reads every
// ID of a document that Parse returned.
func TestCanonicalID(t *testing.T) {
	for _, k := range []ids.Kind{ids.Component, ids.Variable, ids.AttributeDef} {
		id := ids.Encode(k, ids.New())
		prefix, body, _ := strings.Cut(id, "_")
		for _, spelt := range []string{id, strings.ToLower(id), strings.ToUpper(id), strings.ToUpper(prefix) + "_" + body,
			prefix + "_" + strings.ToLower(body[:1]) + body[1:], prefix + "_" + body[:25] + strings.ToLower(body[25:])} {
			if got := canonicalID(spelt); got != id {
				t.Errorf("canonicalID(%s) = %s, want %s", spelt, got, id)
			}
		}
		if allocs := testing.AllocsPerRun(100, func() { canonicalID(id) }); allocs != 0 {
			t.Errorf("canonicalID of a canonical ID allocates %v times", allocs)
		}
	}
	for _, s := range []string{"", "x", "cmp_123", "CMP_01HQ8Z7KD3M9P2W5XRTVNQ4BCI"} {
		if got := canonicalID(s); got != s {
			t.Errorf("canonicalID(%q) = %q", s, got)
		}
	}
}

// TestDeriveNilNodes checks that Derive gives a nil node of a Doc that a
// program built, which MarshalJSON refuses, no segment, rather than panic or
// derive a segment of no kind, and that a nil block before the first
// paragraph of the document or of a list item does not have that paragraph
// begin with a paragraph segment.
func TestDeriveNilNodes(t *testing.T) {
	names := readVectors(t).names()
	a, b := &Text{Text: "a"}, &Text{Text: "b", Marks: Marks{Bold: true}}
	plain := Doc{Blocks: []Block{&Paragraph{Inlines: []Inline{a, b}},
		&List{Items: []Item{{Blocks: []Block{&Paragraph{Inlines: []Inline{b}}}}}}}}
	want := marshal(t, plain.Segments(names))
	for name, d := range map[string]Doc{
		"untyped": {Blocks: []Block{&Paragraph{Inlines: []Inline{a, nil, b}}, nil,
			&List{Items: []Item{{Blocks: []Block{&Paragraph{Inlines: []Inline{nil, b}}, nil}}}}}},
		"typed": {Blocks: []Block{&Paragraph{Inlines: []Inline{a, (*Text)(nil), (*Break)(nil), (*Mention)(nil), (*Value)(nil), b}},
			(*Paragraph)(nil), (*List)(nil),
			&List{Items: []Item{{Blocks: []Block{&Paragraph{Inlines: []Inline{(*Text)(nil), b}}, (*List)(nil), (*Paragraph)(nil)}}}}}},
		"leading": {Blocks: []Block{nil, (*Paragraph)(nil), (*List)(nil), &Paragraph{Inlines: []Inline{a, b}},
			&List{Items: []Item{{Blocks: []Block{nil, (*List)(nil), (*Paragraph)(nil), &Paragraph{Inlines: []Inline{b}}}}}}}},
	} {
		derived := d.Derive(names)
		if got := marshal(t, derived.Segments); !bytes.Equal(got, want) {
			t.Errorf("%s nil nodes: %s, want %s", name, got, want)
		}
		if derived.Text != "ab\n• b" {
			t.Errorf("%s nil nodes: plain text %q", name, derived.Text)
		}
	}
}

// TestDeriveNilNames checks that Derive refuses a nil Names whatever the
// document holds, not only when the document refers to something.
func TestDeriveNilNames(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Derive(nil) of a document that refers to nothing does not panic")
		}
	}()
	doc, err := Parse(paragraphs("a"))
	if err != nil {
		t.Fatal(err)
	}
	doc.Derive(nil)
}

// TestDeriveBeyondLimits checks that what a document derives is not bounded
// by its limits, as the README says: a mention's segment and the plain text
// hold the component's name, which the document does not, so that a
// document of two mentions and no text derives more than MaxChars
// characters.
func TestDeriveBeyondLimits(t *testing.T) {
	const id = "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF"
	mention := `{"type":"mention","attrs":{"id":"` + id + `"}}`
	doc, err := Parse([]byte(`{"type":"doc","content":[{"type":"paragraph","content":[` + mention + `,` +
		mention + `]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("n", MaxChars)
	got := doc.Derive(mapNames{components: map[string]string{id: name}})
	if got.Text != name+name || len(got.Segments) != 2 || got.Segments[1].Value != name {
		t.Errorf("two mentions of a component named with %d characters derive %d characters of text and %d "+
			"segments", MaxChars, utf8.RuneCountInString(got.Text), len(got.Segments))
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

// TestCheckHref checks the links' allowlist: http and https URLs with a host
// of ASCII, a name of labels or an IPv4 or IPv6 address, a port of at most
// 65535, and no userinfo, which could show another host, and mailto URLs of
// one address and nothing else (mailtoAccepted, mailtoRefused); and nothing
// that could run a script or read a file, and no link that holds a hidden
// character, as one that a renderer shows nothing of.
//
// Verifies: NFR-SEC-08
func TestCheckHref(t *testing.T) {
	for _, href := range slices.Concat([]string{
		"https://example.com",
		"http://example.com/a?b=c#d",
		"HTTPS://EXAMPLE.COM/",
		"https://example.com:8080/path",
		"https://example.com/a@b?c=d@e#f@g",
		"https://[::1]/",
		"https://[2001:db8::1]:8443/a",
		"https://[::]/",
		"https://[1:2:3:4:5:6:7:8]/",
		"https://[1:2:3:4:5:6::8]/",
		"https://[::ffff:192.0.2.1]/",
		"https://[FE80::A]/",
		"https://example.com:/",
		"https://cs.wikipedia.org/wiki/Příliš",
		// Internationalized hosts, příliš.example and café.example, as IDNA
		// writes them in ASCII.
		"https://xn--pli-rma35ctb.example/",
		"https://XN--CAF-DMA.example/",
		// A name that ends with a dot, and one of every character that a
		// name may hold.
		"https://example.com./",
		"https://a-b_c~d!e$f&g'h(i)j*k+l,m;n=o.example/",
		// IPv4 addresses, and names whose last label is not a number.
		"https://192.0.2.1/",
		"https://0.0.0.0:80/",
		"https://255.255.255.255./",
		"https://1.example/",
		"https://example.1a/",
		"https://example.0xg/",
		"https://example.com:65535/",
		"https://example.com:000080/",
		// What follows the authority may hold what a host may not.
		"https://example.com/a" + string(rune(0x2215)) + "b",
		"https://example.com/a%C3%A9?q=%20#%2F",
		// What a browser escapes in a path, a query, or a fragment.
		"https://example.com/a|b?q={x}&r=\"<y>\"#^`",
		"https://example.com/a\\b",
	}, mailtoAccepted) {
		if problem := CheckHref(href); problem != "" {
			t.Errorf("CheckHref(%q) = %q, want none", href, problem)
		}
	}
	for _, href := range slices.Concat([]string{
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
		"https://example.com:port",
		"https://example.com:-1/",
		"https://example.com:80:80/",
		// A % that starts no escape, wherever it is.
		"https://example.com/%zz",
		"https://example.com/?q=100%",
		"https://example.com/#%2",
		"https://u%zz@example.com/",
		// A userinfo, which shows a host that the link does not lead to.
		"https://www.example.com@evil.example/login",
		"https://example.com:443@evil.example/",
		"https://user@example.com:8080/path",
		"https://user:pass%40word@example.com/",
		"https://@example.com/",
		"https://[::1]@evil.example/",
		// A port that no browser takes.
		"https://example.com:65536/",
		"https://example.com:99999999999999999999/",
		// Bytes that are not UTF-8, which would be read as U+FFFD.
		"https://\xffevil.example/",
		"https://exa\xc0\xafmple/",
		"https://example.com/\xff",
		// A character beyond ASCII in a host, which an internationalized host
		// is written without, and which could look like a delimiter: IDNA
		// keeps U+2215 DIVISION SLASH, U+2044 FRACTION SLASH, and letters
		// that look like a slash, U+30CE KATAKANA LETTER NO, U+4E3F, and
		// U+FF89, in the host, which is then a host of evil.example, keeps
		// U+0660 ARABIC-INDIC DIGIT ZERO, which looks like a dot, and reads
		// U+3002 as a dot, and U+2488 DIGIT ONE FULL STOP as 1 and a dot.
		"https://bank.example" + string(rune(0x2215)) + "login.evil.example/",
		"https://bank.example" + string(rune(0x2044)) + "login.evil.example/",
		"https://bank.example" + string(rune(0xFF0F)) + "login.evil.example/",
		"https://bank.example" + string(rune(0x30CE)) + "login.evil.example/",
		"https://bank.example" + string(rune(0x4E3F)) + "login.evil.example/",
		"https://bank.example" + string(rune(0xFF89)) + "login.evil.example/",
		"https://bank" + string(rune(0x0660)) + "example.evil.example/",
		"https://bank.example" + string(rune(0x3002)) + "evil.example/",
		"https://bank.example" + string(rune(0xFF20)) + "evil.example/",
		"https://bank" + string(rune(0x2024)) + "example/",
		"https://example" + string(rune(0x2488)) + "com/",
		"https://příliš.example/",
		"https://cafe" + string(rune(0x0301)) + ".example/",
		// A name with an empty label, which no host has.
		"https://./",
		"https://../",
		"https://.example.com/",
		"https://example..com/",
		"https://example.com../",
		// A name that a browser reads as an IPv4 address, as its last label
		// is a number, but that is not four numbers from 0 to 255 in decimal
		// without leading zeros.
		"https://999/",
		"https://1.2.3.256/",
		"https://1.2.3/",
		"https://1.2.3.4.5/",
		"https://01.2.3.4/",
		"https://127.1/",
		"https://0x7f.0.0.1/",
		"https://example.123/",
		"https://example.0x1F/",
		"https://example.0x/",
		// What no IPv6 address is, in brackets.
		"https://[1]/",
		"https://[:::::]/",
		"https://[1::2::3]/",
		"https://[12345::]/",
		"https://[1:2:3:4:5:6:7:8:9]/",
		"https://[192.0.2.1]/",
		"https://[::192.0.2.01]/",
		"https://[::1.2.3]/",
		"https://[.]/",
		// A host with what it does not hold, as a backslash, which a browser
		// reads as a slash, so that the host would be another.
		"https://example.com\\@evil.example/",
		"https://evil.example\\example.com/",
		"https://exa<mple.com/",
		"https://exa{mple.com/",
		"https://exa%41mple.com/",
		"https://exa%40mple.com/",
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
		// Characters that a renderer shows nothing of, which are not format
		// characters (Default_Ignorable_Code_Point): Hangul fillers, letters,
		// U+034F COMBINING GRAPHEME JOINER and U+17B4, marks, and variation
		// selectors, wherever a link holds them.
		"https://example.com/" + string(rune(0x3164)),
		"https://example.com/a" + string(rune(0x115F)) + string(rune(0x1160)),
		"https://example.com/?q=" + string(rune(0xFFA0)),
		"https://example.com/#" + string(rune(0x034F)),
		"https://example.com/" + string(rune(0x17B4)),
		"https://example.com/" + string(rune(0x2764)) + string(rune(0xFE0F)),
		"https://example.com/" + string(rune(0x180B)),
		"https://example.com/" + string(rune(0xE0100)),
	}, mailtoRefused) {
		if CheckHref(href) == "" {
			t.Errorf("CheckHref(%q) accepts it", href)
		}
	}
}

// mailtoAccepted are mailto links that CheckHref takes, and the schema too
// (TestSchemaPatterns, FuzzMailtoPattern): one address and nothing else, a
// local part of atoms of ASCII letters, digits, _, +, and - that single dots
// separate, an @, and a domain of labels of ASCII letters, digits, and
// hyphens that start and end with a letter or a digit, which single dots
// separate, the last not only digits, with the scheme in any ASCII case.
var mailtoAccepted = []string{
	"mailto:keeper@example.com",
	"MAILTO:keeper@example.com",
	"MailTo:Keeper@Example.COM",
	// Every character that a local part may hold, in atoms that dots
	// separate, and an internationalized domain, as IDNA writes it in ASCII.
	"mailto:first.last+tag@xn--caf-dma.example",
	"mailto:a_b-c+d.E9@example.com",
	"mailto:_@example.com",
	"mailto:-.+@example.com",
	"mailto:keeper@XN--CAF-DMA.EXAMPLE",
	// Labels that start with a digit or hold hyphens in a row, a domain of
	// one label, and last labels that are not only digits, as they hold a
	// letter or a hyphen.
	"mailto:keeper@1x--y.example.c0m",
	"mailto:keeper@EXAMPLE-1.example.com",
	"mailto:k@b",
	"mailto:keeper@example.1a",
	"mailto:keeper@example.123a4",
	"mailto:keeper@example.1-2",
	"mailto:keeper@192.0.2.1a",
	// The longest link, of MaxHrefChars characters.
	"mailto:" + strings.Repeat("a", MaxHrefChars-len("mailto:@example.com")) + "@example.com",
}

// mailtoRefused are mailto links that CheckHref refuses, and the schema too
// (TestSchemaPatterns, FuzzMailtoPattern), for what each holds beyond one
// address, or what its address may not hold.
var mailtoRefused = []string{
	// No address, or no local part or domain.
	"mailto:",
	"mailto:keeper",
	"mailto:@example.com",
	"mailto:keeper@",
	"mailto:@",
	// A query, of any field, as subject, body, to, cc, bcc, or attach, for
	// which a mail client could read a file, or an empty one.
	"mailto:keeper@example.com?subject=Light",
	"mailto:keeper@example.com?body=Come",
	"mailto:keeper@example.com?to=mira@example.com",
	"mailto:keeper@example.com?cc=mira@example.com&bcc=elodie@example.com",
	"mailto:keeper@example.com?attach=/etc/passwd",
	"mailto:keeper@example.com?",
	"mailto:?to=keeper@example.com",
	// A fragment.
	"mailto:keeper@example.com#x",
	"mailto:keeper@example.com#?attach=/etc/passwd",
	// An escape, of whatever character, as a line break, which a mail
	// client could write into a header, an @, a dot, a letter, a quote, or a
	// character beyond ASCII, or a % that starts none.
	"mailto:keeper@example.com%0D%0ABcc:evil@example.com",
	"mailto:keeper%40example.com",
	"mailto:support@paypal.com%2Eevil.example",
	"mailto:%6Beeper@example.com",
	"mailto:%22support@paypal.com%22%3Cx@evil.example%3E",
	"mailto:%C3%A9lodie@xn--caf-dma.example",
	"mailto:keeper%zz@example.com",
	// A second address, after a comma or an @, raw or escaped.
	"mailto:keeper@example.com,mira@example.com",
	"mailto:keeper@example.com%2Cmira@example.com",
	"mailto:a@b@example.com",
	// A delimiter that a mail client could read as one of a query: a ;, an
	// &, or an =.
	"mailto:keeper@example.com;attach=/etc/passwd",
	"mailto:keeper@example.com&attach=/etc/passwd",
	"mailto:keeper=x@example.com",
	// A display name, a comment, or quotes, which a mail client shows in
	// place of the address that the message goes to.
	`mailto:"support@paypal.com"<x@evil.example>`,
	"mailto:Support<x@evil.example>",
	"mailto:x@evil.example(support@paypal.com)",
	`mailto:"keeper"@example.com`,
	// A character of RFC 5322's atext that a local part does not hold, or a
	// backslash.
	"mailto:o'brien@example.com",
	"mailto:a/b@example.com",
	"mailto:/keeper@example.com",
	"mailto:a!b@example.com",
	"mailto:a*b@example.com",
	"mailto:a~b@example.com",
	"mailto:a{b}@example.com",
	"mailto:a^b@example.com",
	"mailto:a|b@example.com",
	"mailto:a" + string(rune(0x5C)) + "b@example.com",
	// A character beyond ASCII: in a local part, an e acute or Chinese, the
	// letters of an internationalized address, U+FF20 FULLWIDTH COMMERCIAL
	// AT, which looks like an @, U+02BA MODIFIER LETTER DOUBLE PRIME, a
	// letter that looks like a quote, or U+2800 BRAILLE PATTERN BLANK, which
	// shows as wide as a letter does; or in a domain, the Cyrillic a of
	// paypal.com, which looks like the Latin one, an e acute, or U+3002
	// IDEOGRAPHIC FULL STOP, which IDNA reads as a dot.
	"mailto:" + string(rune(0xE9)) + "lodie@xn--caf-dma.example",
	"mailto:" + string([]rune{0x7528, 0x6237}) + "@example.com",
	"mailto:support" + string(rune(0xFF20)) + "paypal.com@evil.example",
	"mailto:" + string(rune(0x02BA)) + "support" + string(rune(0x02BA)) + "@evil.example",
	"mailto:support" + strings.Repeat(string(rune(0x2800)), 3) + "@evil.example",
	"mailto:support@p" + string(rune(0x0430)) + "ypal.com",
	"mailto:keeper@caf" + string(rune(0xE9)) + ".example",
	"mailto:support@paypal.com" + string(rune(0x3002)) + "evil.example",
	// A character that is not shown, which no link holds.
	"mailto:keeper" + string(rune(0x3164)) + "@example.com",
	"mailto:keeper@example.com" + string(rune(0x200B)),
	"mailto: keeper@example.com",
	// A local part with a dot at its start or its end, or two in a row.
	"mailto:.keeper@example.com",
	"mailto:keeper.@example.com",
	"mailto:kee..per@example.com",
	// A domain with an empty label, a label that starts or ends with a
	// hyphen, a character that no label holds, or a last label of digits, as
	// an IP address has, which a mailto link does not name.
	"mailto:keeper@.example.com",
	"mailto:keeper@example..com",
	"mailto:keeper@example.com.",
	"mailto:keeper@-example.com",
	"mailto:keeper@example-.com",
	"mailto:x@-.-",
	"mailto:keeper@exa_mple.com",
	"mailto:keeper@exa+mple.com",
	"mailto:support@paypal.com/.evil.example",
	"mailto:keeper@192.0.2.1",
	"mailto:keeper@example.123",
	"mailto:keeper@1",
	// An address literal, of an IP address or of a name, raw or escaped.
	"mailto:keeper@[192.0.2.1]",
	"mailto:keeper@[IPv6:2001:db8::1]",
	"mailto:keeper@%5B192.0.2.1%5D",
	"mailto:support@[paypal.com]",
	// A link longer than MaxHrefChars characters.
	"mailto:" + strings.Repeat("a", MaxHrefChars-len("mailto:@example.com")+1) + "@example.com",
}

// TestMailtoProblem checks that CheckHref says what is wrong with a mailto
// link, one message for each kind of fault: that it holds more than an
// address, that it has no address, or what is wrong with its local part or
// with its domain.
//
// Verifies: NFR-SEC-08
func TestMailtoProblem(t *testing.T) {
	for href, want := range map[string]string{
		"mailto:keeper@example.com?subject=Light":                    "with more than an address",
		"mailto:keeper@example.com#x":                                "with more than an address",
		"mailto:keeper%40example.com":                                "with more than an address",
		"mailto:keeper@example.com,mira@example.com":                 "with more than an address",
		"mailto:a@b@example.com":                                     "with more than an address",
		`mailto:"keeper"@example.com`:                                "with more than an address",
		"mailto:" + string(rune(0xE9)) + "lodie@xn--caf-dma.example": "with more than an address",
		"mailto:keeper@[192.0.2.1]":                                  "with more than an address",
		"mailto:":                                                    "without an address",
		"mailto:keeper":                                              "without an address",
		"mailto:@example.com":                                        "whose local part",
		"mailto:kee..per@example.com":                                "whose local part",
		"mailto:keeper@":                                             "whose domain",
		"mailto:keeper@example-.com":                                 "whose domain",
		"mailto:keeper@192.0.2.1":                                    "whose domain",
		"mailto:keeper@exa_mple.com":                                 "whose domain",
	} {
		if got := CheckHref(href); !strings.HasPrefix(got, "is a mailto URL "+want) {
			t.Errorf("CheckHref(%q) = %q, want it to say that it is a mailto URL %s", href, got, want)
		}
	}
}

// FuzzValidEscapes checks that validEscapes refuses a % that starts no escape
// as url.PathUnescape does, and takes every other.
func FuzzValidEscapes(f *testing.F) {
	for _, s := range []string{
		"", "a", "%", "%2", "a%", "%zz", "%g0", "a%41b", "%2e%7E%5f%2D%25", "%2F%3a%40%2b", "%E2%80%AE",
		"%c3%a9", "+%20+", "100%", "%%41", "%25%30%61", "%FF%00",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if _, err := url.PathUnescape(s); validEscapes(s) != (err == nil) {
			t.Fatalf("validEscapes(%q) = %v, and url.PathUnescape returns the error %v", s, validEscapes(s), err)
		}
	})
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

	// A field's name that a client chose is cut in Error to its first 40
	// characters, in the pointer as in the message, but kept whole in the
	// pointer, which names what the document holds.
	long := strings.Repeat("ž", 500_000)
	_, err := Parse([]byte(`{"type":"doc","content":[{"type":"paragraph","` + long + `":1}]}`))
	wantError(t, err, UnknownField, "/content/0/"+long)
	cut := strings.Repeat("ž", 40)
	if got := err.Error(); got != `rich text: /content/0/`+cut+`…: the paragraph has no field "`+cut+`"` {
		t.Errorf("Error() = %.200s, %d bytes", got, len(got))
	}

	// A name is cut, as the message cuts it, before it is escaped, so that
	// no cut falls within an escape, as ~1 for a slash, and a name of 40
	// characters is kept whole, however long it is escaped.
	a39 := strings.Repeat("a", 39)
	for name, want := range map[string]string{
		a39 + "/b": `rich text: /content/0/` + a39 + `~1…: the paragraph has no field "` + a39 + `/"`,
		a39 + "~":  `rich text: /content/0/` + a39 + `~0: the paragraph has no field "` + a39 + `~"`,
	} {
		_, err := Parse([]byte(`{"type":"doc","content":[{"type":"paragraph","` + name + `":1}]}`))
		if got := err.Error(); got != want {
			t.Errorf("Error() = %s, want %s", got, want)
		}
	}

	// A name with a character that is not printable, as a line feed, an
	// escape that a terminal reads, U+202E, or NEL, is quoted in Error, as
	// the message quotes it, so that it writes no line of its own into a
	// log, and kept as it is in the pointer.
	for name, want := range map[string]string{
		`\n\u001b[31mFAKE LOG LINE`: `rich text: /content/0/"\n\x1b[31mFAKE LOG LINE": ` +
			`the paragraph has no field "\n\x1b[31mFAKE LOG LINE"`,
		"a" + string(rune(0x202E)) + "b/c": `rich text: /content/0/"a\` + `u202eb~1c": ` +
			`the paragraph has no field "a\` + `u202eb/c"`,
		a39 + `\u0085` + strings.Repeat("b", 10): `rich text: /content/0/"` + a39 + `\u0085"…: ` +
			`the paragraph has no field "` + a39 + `\u0085"`,
	} {
		_, err := Parse([]byte(`{"type":"doc","content":[{"type":"paragraph","` + name + `":1}]}`))
		var key string
		if err := json.Unmarshal([]byte(`"`+name+`"`), &key); err != nil {
			t.Fatal(err)
		}
		wantError(t, err, UnknownField, "/content/0/"+escapeToken(key))
		if got := err.Error(); got != want {
			t.Errorf("Error() = %s, want %s", got, want)
		}
	}
}

// TestMessages checks that a message names what is wrong in English, with
// the article that a name takes, and no word twice.
func TestMessages(t *testing.T) {
	const cmp = `"ref":"cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF"`
	for doc, want := range map[string]string{
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"valueRef","attrs":{` + cmp + `,"attribute":null}}]}]}`: "a value reference's attribute is not a string",
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"valueRef","attrs":{` + cmp + `,"attribute":""}}]}]}`:   "a value reference's attribute is not the ID of an attribute definition",
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"valueRef","attrs":{"ref":"x"}}]}]}`:                    "a value reference's ref is not the ID of a variable or a component",
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"x"}}]}]}`:                      "a mention's id is not the ID of a component",
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"orderedList","content":[]}]}]}`:                        "a paragraph holds text, line breaks, mentions, and value references, not an orderedList",
		`{"type":"doc","content":[{"type":"bulletList","content":[{"type":"paragraph"}]}]}`:                                      "a list holds list items, not a paragraph",
	} {
		_, err := Parse([]byte(doc))
		var e *Error
		if !errors.As(err, &e) || e.Message != want {
			t.Errorf("%s: %v, want %q", doc, err, want)
		}
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

// FuzzParse checks that Parse never panics, that a document that it accepts,
// and its canonical form, validate against the schema, as the API's huma
// validates it, that the canonical form is a fixed point that derives
// without panicking, that a Doc that a program builds of the same document
// out of its canonical form is written as it and derives what it derives,
// and that every error has a known reason and points at a value of the
// input.
func FuzzParse(f *testing.F) {
	v := readVectors(f)
	for _, vec := range v.Valid {
		f.Add([]byte(vec.Doc))
	}
	for _, vec := range v.Invalid {
		f.Add([]byte(vec.Doc))
	}
	names := v.names()
	validate := humaValidator(f, publishedSchema(f))
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
		if problems := validate(data); problems != nil {
			t.Fatalf("the schema refuses a document that Parse accepts: %v", problems)
		}
		if problems := validate(canonical); problems != nil {
			t.Fatalf("the schema refuses the canonical form %s: %v", canonical, problems)
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
		// A program's Doc of the same document, out of its canonical form,
		// is written as the canonical form, which joins its texts again,
		// and derives what it does.
		built := Doc{Blocks: uncanonical(doc.Blocks)}
		if b, err := schemagen.Marshal(built); err != nil || !bytes.Equal(b, marshal(t, doc)) {
			t.Fatalf("a built Doc of %s is written as %s (%v)", canonical, b, err)
		}
		bd := built.Derive(names)
		if !bytes.Equal(marshal(t, bd.Segments), marshal(t, d.Segments)) || bd.Text != d.Text || bd.Words != d.Words {
			t.Fatalf("a built Doc of %s derives\n%s\nand the parsed one\n%s", canonical, marshal(t, bd.Segments),
				marshal(t, d.Segments))
		}
	})
}

// FuzzPreview checks that a preview is the plain text with its white space
// collapsed, or, with an ellipsis, the start of that, of at most PreviewChars
// code points, which is not empty, does not end with a space, and ends before
// a space or where a character does (splits), unless it is PreviewChars code
// points long.
func FuzzPreview(f *testing.F) {
	for _, vec := range readVectors(f).Previews {
		f.Add(vec.Text)
	}
	f.Fuzz(func(t *testing.T, plain string) {
		if !utf8.ValidString(plain) {
			return
		}
		collapsed := strings.Join(strings.Fields(plain), " ")
		got := Preview(plain)
		head, cut := strings.CutSuffix(got, "…")
		if !cut || utf8.RuneCountInString(collapsed) <= PreviewChars {
			if got != collapsed {
				t.Fatalf("Preview(%q) = %q, want %q", plain, got, collapsed)
			}
			return
		}
		n := utf8.RuneCountInString(head)
		switch {
		case !strings.HasPrefix(collapsed, head) || n == 0 || n > PreviewChars:
			t.Fatalf("Preview(%q) = %q, which is not 1 to %d code points of %q", plain, got, PreviewChars, collapsed)
		case strings.HasSuffix(head, " "):
			t.Fatalf("Preview(%q) = %q, which ends with a space", plain, got)
		case n < PreviewChars && collapsed[len(head)] != ' ' && splits(collapsed, len(head)):
			// A preview of PreviewChars code points may end within a character
			// that is all of them, where the cut stays.
			t.Fatalf("Preview(%q) = %q, which ends within a character", plain, got)
		}
	})
}

// TestPreviewOfLongText checks that a preview reads its plain text only as far
// as the code point after the first PreviewChars, rather than the whole of
// the word or the text in which they end, as a mention of a component whose
// name is megabytes long, or a text of Japanese without spaces, which
// derives a plain text beyond the document's limits.
func TestPreviewOfLongText(t *testing.T) {
	for _, plain := range []string{
		strings.Repeat("a", 8<<20),
		"a " + strings.Repeat(string(rune(0x3042)), 4<<20),
		strings.Repeat(" ", 4<<20) + strings.Repeat("ab ", 4<<20),
	} {
		var got string
		used := allocated(func() { got = Preview(plain) })
		if !strings.HasSuffix(got, "…") || utf8.RuneCountInString(got) > PreviewChars+1 {
			t.Errorf("Preview of %d bytes = %q", len(plain), got)
		}
		// The preview's own allocations are a few hundred bytes; writing
		// the word in which the first PreviewChars end allocated megabytes.
		if used > 64<<10 {
			t.Errorf("Preview of %d bytes allocated %d bytes", len(plain), used)
		}
	}
}

// allocated returns the fewest bytes that the process allocated in each of
// three runs of f, so that what another goroutine allocates during a run, as
// the runtime's own or one that an earlier test left running, which the
// runtime counts too, fails no test unless it does so during every run.
func allocated(f func()) uint64 {
	least := uint64(math.MaxUint64)
	for range 3 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		f()
		runtime.ReadMemStats(&after)
		least = min(least, after.TotalAlloc-before.TotalAlloc)
	}
	return least
}

// uncanonical returns a copy of blocks out of their canonical form, as a
// program could build them: each text of more than one character split after
// its first, and each ID spelt in lowercase.
func uncanonical(blocks []Block) []Block {
	out := make([]Block, len(blocks))
	for i, b := range blocks {
		switch b := b.(type) {
		case *Paragraph:
			var inlines []Inline
			for _, in := range b.Inlines {
				switch in := in.(type) {
				case *Text:
					if _, size := utf8.DecodeRuneInString(in.Text); size < len(in.Text) {
						inlines = append(inlines, &Text{Text: in.Text[:size], Marks: in.Marks},
							&Text{Text: in.Text[size:], Marks: in.Marks})
						continue
					}
					inlines = append(inlines, &Text{Text: in.Text, Marks: in.Marks})
				case *Mention:
					inlines = append(inlines, &Mention{Component: strings.ToLower(in.Component), Marks: in.Marks})
				case *Value:
					v := *in
					v.Ref, v.Attribute = strings.ToLower(v.Ref), strings.ToLower(v.Attribute)
					inlines = append(inlines, &v)
				default:
					inlines = append(inlines, in)
				}
			}
			out[i] = &Paragraph{Inlines: inlines}
		case *List:
			l := &List{Ordered: b.Ordered, Start: b.Start}
			for _, item := range b.Items {
				l.Items = append(l.Items, Item{Blocks: uncanonical(item.Blocks)})
			}
			out[i] = l
		}
	}
	return out
}
