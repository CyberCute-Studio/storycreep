package search

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

// The lengths of a document's title and body, in characters, at most, as
// the database's checks of search_entry hold them too (migration 00029): a
// title is as long as a node's title may be, the longest of the titles, and
// a body as long as a node's text may be, so that a node's text is searched
// whole, and a component's body, its key, its description, and its values,
// which may be longer, as far as that, so that a document's vector stays
// below the megabyte that a tsvector holds at most, which a body of a
// hundred thousand distinct words of four bytes each would not pass.
const (
	MaxTitleLength = 200
	MaxBodyLength  = richtext.MaxChars
)

// Source is what the document of an entity is made of, as its rows hold it
// (queries.NodeSearchSources and the other kinds'): a node's title and text,
// a connection's label, a variable's key and description, and a component's
// name, key, description, and values of text.
type Source struct {
	Kind Kind
	ID   ids.UUID
	// Title is a node's title, a variable's key, or a component's name, and
	// "" for a connection, which has none.
	Title string
	// Doc is the rich text of a node's text, a connection's label, or a
	// component's description, as it is stored, or nil for a node that has
	// none, a branch or a jumper, and for a variable.
	Doc []byte
	// Key is a component's key.
	Key string
	// Description is a variable's description.
	Description string
	// Values are a component's values of the definitions of text that are
	// not deleted themselves, of the types string, text, and enum, in the
	// order of the definitions' IDs.
	Values []string
}

// Document is what the index holds of an entity, which a search matches its
// query's words against, and marks them in: its title and its body.
type Document struct {
	Kind Kind
	ID   ids.UUID
	// Title is a node's title, a variable's key, a component's name, or ""
	// for a connection.
	Title string
	// Body is the plain text of a node's text or a connection's label, a
	// variable's description, or a component's key, the plain text of its
	// description, and its values, each on a line of its own, but for those
	// that are empty.
	Body string
}

// NewDocument returns the document of the entity that s is of. A text's
// plain text is the text export's (richtext.Render), but each value
// reference and each mention is a space, since only what its author wrote
// is searched, rather than a name that changes as its entity's does, and
// the usage queries find what a text shows or mentions. A control
// character, which a component's value of text may hold, is a space too,
// but a line feed or a tab, so that a document never holds the characters
// that a search's highlights are marked with (Search). A title or a body
// longer than MaxTitleLength or MaxBodyLength is cut there. It returns an
// error for a stored document that richtext.Parse refuses, which no command
// stores, and for a kind that has no document.
func NewDocument(s Source) (Document, error) {
	var parts []string
	switch s.Kind {
	case Node, Connection:
		text, err := plainText(s.Doc)
		if err != nil {
			return Document{}, fmt.Errorf("search: the text of the %s %s: %w", s.Kind, s.ID, err)
		}
		parts = []string{text}
	case Variable:
		parts = []string{s.Description}
	case Component:
		text, err := plainText(s.Doc)
		if err != nil {
			return Document{}, fmt.Errorf("search: the description of the component %s: %w", s.ID, err)
		}
		parts = append([]string{s.Key, text}, s.Values...)
	default:
		return Document{}, fmt.Errorf("search: no document of the kind %q", s.Kind)
	}
	var lines []string
	for _, p := range parts {
		if p != "" {
			lines = append(lines, p)
		}
	}
	return Document{Kind: s.Kind, ID: s.ID, Title: clean(s.Title, MaxTitleLength),
		Body: clean(strings.Join(lines, "\n"), MaxBodyLength)}, nil
}

// plainText returns the plain text of the stored rich text doc, as
// NewDocument says, or "" for none.
func plainText(doc []byte) (string, error) {
	if doc == nil {
		return "", nil
	}
	d, err := richtext.Parse(doc)
	if err != nil {
		return "", err
	}
	return richtext.Render(d.Segments(blank{}), func(richtext.Segment) string { return " " }), nil
}

// blank names each mention a space, which richtext.Render writes where the
// mention is (plainText).
type blank struct{}

func (blank) Value(string, string) string { return " " }
func (blank) Component(string) string     { return " " }

// clean returns s with each control character but a line feed and a tab
// written as a space (input.IsControl), cut after max characters.
func clean(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && input.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
}
