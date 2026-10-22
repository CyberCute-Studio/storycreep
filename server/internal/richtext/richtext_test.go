package richtext

import (
	"bytes"
	"encoding/json"
	"errors"
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
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})
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

// TestLimits checks the limits of a document, at and over each: its bytes,
// the characters of its text, how deeply its lists nest, and how long a link
// is.
//
// Verifies: NFR-SEC-07
func TestLimits(t *testing.T) {
	t.Run("bytes", func(t *testing.T) {
		doc := paragraphs("x")
		padded := append(doc, bytes.Repeat([]byte(" "), MaxBytes-len(doc))...)
		if _, err := Parse(padded); err != nil {
			t.Errorf("a document of %d bytes: %v", MaxBytes, err)
		}
		_, err := Parse(append(padded, ' '))
		wantError(t, err, TooLarge, "")
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
		"https://[::1]/",
		"https://cs.wikipedia.org/wiki/Příliš",
		"mailto:keeper@example.com",
		"MAILTO:keeper@example.com?subject=Light",
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
		"mailto:",
		"https://example.com:port",
	} {
		if CheckHref(href) == "" {
			t.Errorf("CheckHref(%q) accepts it", href)
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
