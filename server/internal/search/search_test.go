package search_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/search"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

// TestSpans checks how a title or a body as ts_headline marks its matches is
// read: each match a part of its own, the text between them parts that are
// not, and nothing of an empty text.
func TestSpans(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []search.Span
	}{
		{"", []search.Span{}},
		{"Light it.", []search.Span{{Text: "Light it."}}},
		{"The \x02Lantern\x03", []search.Span{{Text: "The "}, {Text: "Lantern", Match: true}}},
		{"\x02old\x03 \x02lantern\x03.", []search.Span{{Text: "old", Match: true}, {Text: " "},
			{Text: "lantern", Match: true}, {Text: "."}}},
		// Two matches with nothing between them are one part.
		{"\x02old\x03\x02_lantern\x03", []search.Span{{Text: "old_lantern", Match: true}}},
		// An empty match is no part.
		{"a\x02\x03b", []search.Span{{Text: "ab"}}},
	} {
		if got := search.Spans(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Spans(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// TestPrefixQuery checks the text of a query's tsquery: each word quoted, as
// a tsquery's text quotes it, a quote and a backslash in it escaped, with :*
// after it, joined by & for the query that matches, and by | for the one
// that ranks and marks.
func TestPrefixQuery(t *testing.T) {
	words := []string{"elara", "o'neil", `a\b`}
	if got, want := search.PrefixQuery(words), `'elara':* & 'o''neil':* & 'a\\b':*`; got != want {
		t.Errorf("PrefixQuery = %s, want %s", got, want)
	}
	if got, want := search.AnyPrefixQuery(words), `'elara':* | 'o''neil':* | 'a\\b':*`; got != want {
		t.Errorf("AnyPrefixQuery = %s, want %s", got, want)
	}
}

// TestNewDocument checks what the document of each kind of entity holds: a
// node's title and the plain text of its text, without what its value
// references show or its mentions name; a connection's label, without a
// title; a variable's key and description; and a component's name, and its
// key, description, and values, each on a line of its own, but for those
// that are empty, with each control character but a line feed and a tab a
// space, and a body cut after MaxBodyLength characters.
func TestNewDocument(t *testing.T) {
	text := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Ask "},` +
		`{"type":"mention","attrs":{"id":"cmp_01M5ZX8E8GFQ1V3JZ6Q3ZT2N4K"}},{"type":"text","text":"about the"},` +
		`{"type":"valueRef","attrs":{"ref":"var_01M5ZX8E8GFQ1V3JZ6Q3ZT2N4M"}},{"type":"text","text":"key."}]},` +
		`{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[` +
		`{"type":"text","text":"Then go."}]}]}]}]}`
	id := ids.New()
	for _, c := range []struct {
		name string
		in   search.Source
		want search.Document
	}{
		{"element", search.Source{Kind: search.Node, ID: id, Title: "The Lantern", Doc: []byte(text)},
			search.Document{Kind: search.Node, ID: id, Title: "The Lantern",
				Body: "Ask  about the key.\n• Then go."}},
		{"branch", search.Source{Kind: search.Node, ID: id, Title: "Gate"},
			search.Document{Kind: search.Node, ID: id, Title: "Gate"}},
		{"connection", search.Source{Kind: search.Connection, ID: id, Doc: []byte(text)},
			search.Document{Kind: search.Connection, ID: id, Body: "Ask  about the key.\n• Then go."}},
		{"variable", search.Source{Kind: search.Variable, ID: id, Title: "lantern_lit",
			Description: "Whether\tthe lantern\nis lit."},
			search.Document{Kind: search.Variable, ID: id, Title: "lantern_lit", Body: "Whether\tthe lantern\nis lit."}},
		{"component", search.Source{Kind: search.Component, ID: id, Title: "Old Lantern", Key: "old_lantern",
			Doc:    []byte(`{"type":"doc","content":[{"type":"paragraph"}]}`),
			Values: []string{"Brass", "", "Found\x02 in\x7fthe\u202ecellar"}},
			search.Document{Kind: search.Component, ID: id, Title: "Old Lantern",
				Body: "old_lantern\nBrass\nFound  in the cellar"}},
		{"long", search.Source{Kind: search.Variable, ID: id, Title: "k",
			Description: strings.Repeat("ž", search.MaxBodyLength+5)},
			search.Document{Kind: search.Variable, ID: id, Title: "k", Body: strings.Repeat("ž", search.MaxBodyLength)}},
	} {
		got, err := search.NewDocument(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: the document is %+v, want %+v", c.name, got, c.want)
		}
	}
	if _, err := search.NewDocument(search.Source{Kind: search.Node, ID: id, Doc: []byte(`{"type":"doc"}`)}); err == nil {
		t.Error("a node's text that richtext.Parse refuses gives a document")
	}
	if _, err := search.NewDocument(search.Source{Kind: "board", ID: id}); err == nil {
		t.Error("a board gives a document")
	}
}

// TestNeutral checks that each type of command that the derivation of the
// documents passes over is a registered type, or the name of the derivation
// that compiles expressions, whose changes of the owners of expressions are
// of their revisions alone.
func TestNeutral(t *testing.T) {
	r := commands.NewRegistry()
	project.Register(r)
	board.Register(r)
	variable.Register(r)
	component.Register(r)
	node.Register(r)
	connection.Register(r)
	expression.Register(r)
	dependency.Register(r)
	validation.Register(r)
	search.Register(r)
	types := r.Types()
	for _, typ := range search.Neutral() {
		if !slices.Contains(types, typ) && typ != commands.Type(expression.Name) {
			t.Errorf("neutral lists %s, which is neither a registered type of command nor %s", typ, expression.Name)
		}
	}
}

// TestResultEnum checks the enum of a result's type against the kinds that
// have a document, in their order.
func TestResultEnum(t *testing.T) {
	f, _ := reflect.TypeOf(search.Result{}).FieldByName("Type")
	var kinds []string
	for _, k := range search.Kinds() {
		kinds = append(kinds, string(k))
	}
	got := strings.Split(f.Tag.Get("enum"), ",")
	if !slices.Equal(slices.Sorted(slices.Values(got)), kinds) {
		t.Errorf("the enum of a result's type is %q, want %q", got, kinds)
	}
	// The JSON of a result holds its parts as arrays, empty or not.
	b, err := json.Marshal(search.Result{Type: search.Connection, Title: search.Spans(""),
		Snippet: search.Spans("x")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"title":[]`) {
		t.Errorf("a result without a title is written %s, want an empty array of parts", b)
	}
}
