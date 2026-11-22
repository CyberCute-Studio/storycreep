// Package textexport writes the narrative text of a project or of one of its
// boards, of a snapshot or of the draft, for review and voice-over, as CSV and
// as JSON (PRD FR-TEXT-05, plan note N89).
//
// An export has a row of each text: an element's text, of the type body, and
// a connection's label, of the type choice, each with the node that holds it
// or that it leaves, as the writing statistics count them (FR-TEXT-06), so
// that its word counts add up to theirs whenever those say that they are
// complete (AC3). A text that derives no plain text has no row. The draft's rows are read from the draft itself rather
// than from the files that its export writes, which leave out what no release
// may hold, as a reference of a variable in the trash, which the statistics
// count by its key, and a connection from a port that its node does not have.
//
// The rows are in one order, whatever order they were read in, so that an
// unchanged project exports the same bytes (AC2), and that an export of a
// release and of a draft that holds the same content are the same rows: the
// boards by ID, each board's nodes as its script view lists them
// (FR-TEXT-04 AC2), breadth-first from its entry node along the connections
// in the order that the flow offers them, and then those that it does not
// reach, by ID, since a release's files hold no time of creation, and each
// node's text before its choices.
package textexport

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
)

// ErrBoardNotFound is returned for a board that the snapshot or the draft
// does not hold.
var ErrBoardNotFound = errors.New("textexport: no such board")

// The types of a row's text (FR-TEXT-05).
const (
	TypeBody   = "body"
	TypeChoice = "choice"
)

// The kinds of what an export is of.
const (
	SourceDraft    = "draft"
	SourceSnapshot = "snapshot"
)

// The formats of an export.
const (
	FormatCSV  = "csv"
	FormatJSON = "json"
)

// Row is a text of the export, with the columns that FR-TEXT-05 names.
type Row struct {
	// NodeID is the node that holds the text, or that the choice leaves.
	NodeID string `json:"node_id"`
	// Board is the name of the node's board.
	Board string `json:"board"`
	// NodeTitle is the node's title, "" for none.
	NodeTitle string `json:"node_title"`
	// Speaker is the name of the component that says a line of dialogue, ""
	// for none, for a speaker in the draft's trash, for a narration, which no
	// one says, though it keeps the speaker that it had as dialogue, and for a
	// choice, which its element's speaker does not say.
	Speaker string `json:"speaker"`
	// TextType is TypeBody or TypeChoice.
	TextType string `json:"text_type"`
	// Text is the text's plain text (richtext.PlainText).
	Text string `json:"text"`
	// Condition is the source of a choice's condition, as its author wrote
	// it, "" for none, and for a body, which has none.
	Condition string `json:"condition"`
	// WordCount is how many words the text has (richtext.WordCount).
	WordCount int `json:"word_count"`
	// TextHash is the hash of the plain text (richtext.Hash).
	TextHash string `json:"text_hash"`
}

// Project is the project that an export is of.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Source is what an export was read from: the draft, or a snapshot, by its
// ID and its version.
type Source struct {
	Kind    string `json:"kind"`
	ID      string `json:"id,omitempty"`
	Version string `json:"version,omitempty"`
}

// Board is the board that an export is narrowed to.
type Board struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Export is an export of a project's text, or of one board's. It holds no
// time, so that an unchanged project exports the same bytes (AC2).
type Export struct {
	Project Project `json:"project"`
	Source  Source  `json:"source"`
	// Board is the board that the export is narrowed to, nil for the whole
	// project.
	Board *Board `json:"board,omitempty"`
	// Words is how many words its rows have together.
	Words int   `json:"words"`
	Rows  []Row `json:"rows"`
}

// text is what a row shows of a rich text.
type text struct {
	plain string
	words int
	hash  string
}

// textOf returns what a row shows of the segments of a rich text, or nil for
// one without plain text, which has no row.
func textOf(segs richtext.Segments) *text {
	plain := richtext.PlainText(segs)
	if plain == "" {
		return nil
	}
	return &text{plain: plain, words: richtext.WordCount(segs), hash: richtext.Hash(plain)}
}

// narration is the kind of an element that no one says (FR-EDIT-02).
const narration = "narration"

// speakerOf returns the name, of speakers, of the component that says an
// element of the kind and the stored speaker: none for a narration, as the
// script view shows it, whatever speaker it keeps.
func speakerOf(kind, speaker string, speakers map[string]string) string {
	if kind == narration {
		return ""
	}
	return speakers[speaker]
}

// choice is a connection that leaves a node, in the order that the flow
// offers them.
type choice struct {
	to        string
	label     *text
	condition string
}

// node is a node of a board, with the texts that its rows show.
type node struct {
	id, title, speaker string
	body               *text
	leaving            []choice
}

// board is a board, with its nodes, by ID.
type board struct {
	info  Board
	entry string
	nodes []*node
}

// assemble returns the export of boards, in their order, with the rows of
// each in the order of its script view.
func assemble(project Project, source Source, boards []board, narrowed *Board) *Export {
	e := &Export{Project: project, Source: source, Board: narrowed, Rows: []Row{}}
	for i := range boards {
		b := &boards[i]
		for _, n := range scriptOrder(b) {
			if n.body != nil {
				e.add(Row{NodeID: n.id, Board: b.info.Name, NodeTitle: n.title, Speaker: n.speaker, TextType: TypeBody,
					Text: n.body.plain, WordCount: n.body.words, TextHash: n.body.hash})
			}
			for _, c := range n.leaving {
				if c.label != nil {
					e.add(Row{NodeID: n.id, Board: b.info.Name, NodeTitle: n.title, TextType: TypeChoice,
						Text: c.label.plain, Condition: c.condition, WordCount: c.label.words, TextHash: c.label.hash})
				}
			}
		}
	}
	return e
}

// add appends r to the export's rows, and its words to their total.
func (e *Export) add(r Row) {
	e.Rows = append(e.Rows, r)
	e.Words += r.WordCount
}

// scriptOrder returns the nodes of b, which are sorted by ID, breadth-first
// from its entry node along the connections that leave each, and then those
// that it does not reach, by ID (FR-TEXT-04 AC2). A node is listed once, where
// it is first reached, so that a cycle lists none twice.
func scriptOrder(b *board) []*node {
	byID := make(map[string]*node, len(b.nodes))
	for _, n := range b.nodes {
		byID[n.id] = n
	}
	out := make([]*node, 0, len(b.nodes))
	seen := make(map[string]bool, len(b.nodes))
	if entry := byID[b.entry]; entry != nil {
		seen[entry.id] = true
		out = append(out, entry)
		// The list is the queue: each node is read once, in the order reached.
		for i := 0; i < len(out); i++ {
			for _, c := range out[i].leaving {
				if next := byID[c.to]; next != nil && !seen[next.id] {
					seen[next.id] = true
					out = append(out, next)
				}
			}
		}
	}
	for _, n := range b.nodes {
		if !seen[n.id] {
			out = append(out, n)
		}
	}
	return out
}

// unknownPort is the place of a port that its node does not have, after each
// that it has.
const unknownPort = math.MaxInt

// edge is a connection on its way into the order of its node's: the place of
// its port among the node's, the port, its key among the port's, and its ID.
type edge[K cmp.Ordered] struct {
	rank int
	port string
	key  K
	id   string
	c    choice
}

// ordered returns the choices of edges in the order that the flow offers
// them: each port's in turn, those of a port that the node does not have
// after the others, by the port's ID, and each port's by their keys, then by
// their IDs.
func ordered[K cmp.Ordered](edges []edge[K]) []choice {
	slices.SortFunc(edges, func(a, b edge[K]) int {
		return cmp.Or(cmp.Compare(a.rank, b.rank), strings.Compare(a.port, b.port), cmp.Compare(a.key, b.key),
			strings.Compare(a.id, b.id))
	})
	out := make([]choice, len(edges))
	for i, e := range edges {
		out[i] = e.c
	}
	return out
}

// FromDraft returns the export of d, a draft that serializer.Load read, with
// the names that names gives of what its texts name, as the statistics read
// them (statistics.ReadNames), of the board of the ID board, or of the whole
// project for "". It returns ErrBoardNotFound for a board that d does not
// hold.
func FromDraft(d *serializer.Draft, names richtext.Names, boardID string) (*Export, error) {
	speakers := make(map[string]string, len(d.Components))
	for _, c := range d.Components {
		speakers[c.ID] = c.Name
	}
	boards := make([]board, 0, len(d.Boards))
	var narrowed *Board
	for _, b := range d.Boards {
		if boardID != "" && b.ID != boardID {
			continue
		}
		info := Board{ID: b.ID, Name: b.Name, Slug: b.Slug}
		if boardID != "" {
			narrowed = &info
		}
		boards = append(boards, board{info: info, entry: b.EntryNode})
	}
	if boardID != "" && narrowed == nil {
		return nil, ErrBoardNotFound
	}
	slices.SortFunc(boards, func(a, b board) int { return strings.Compare(a.info.ID, b.info.ID) })
	of := make(map[string]*board, len(boards))
	for i := range boards {
		of[boards[i].info.ID] = &boards[i]
	}

	nodes := make(map[string]*node, len(d.Nodes))
	types := make(map[string]string, len(d.Nodes))
	for i := range d.Nodes {
		n := &d.Nodes[i]
		b := of[n.Board]
		if b == nil {
			continue
		}
		out := &node{id: n.ID, title: n.Title, speaker: speakerOf(n.Kind, n.Speaker, speakers)}
		if n.Text != nil {
			out.body = textOf(n.Text.Segments(names))
		}
		nodes[n.ID], types[n.ID] = out, n.Type
		b.nodes = append(b.nodes, out)
	}
	ranks := draftPorts(d.Outputs)
	edges := map[string][]edge[string]{}
	for i := range d.Connections {
		c := &d.Connections[i]
		if nodes[c.From] == nil {
			continue
		}
		port, rank := "out", 0
		if c.FromOutput != "" {
			port = c.FromOutput
		}
		if types[c.From] == published.NodeBranch || c.FromOutput != "" {
			r, ok := ranks[c.From][port]
			if !ok {
				r = unknownPort
			}
			rank = r
		}
		out := choice{to: c.To}
		if c.Label != nil {
			out.label = textOf(c.Label.Segments(names))
		}
		if c.Condition != nil {
			out.condition = c.Condition.Source
		}
		edges[c.From] = append(edges[c.From], edge[string]{rank: rank, port: port, key: c.Order, id: c.ID, c: out})
	}
	for id, list := range edges {
		nodes[id].leaving = ordered(list)
	}
	for i := range boards {
		slices.SortFunc(boards[i].nodes, func(a, b *node) int { return strings.Compare(a.id, b.id) })
	}
	return assemble(Project{ID: d.Project.ID, Name: d.Project.Name, Slug: d.Project.Slug},
		Source{Kind: SourceDraft}, boards, narrowed), nil
}

// draftPorts returns the place of each output among its branch's, by the
// branch's ID and the output's: its conditional ones in their author's order,
// and then its else, as the flow evaluates them (FR-EDIT-04).
func draftPorts(outputs []serializer.Output) map[string]map[string]int {
	byNode := map[string][]*serializer.Output{}
	for i := range outputs {
		o := &outputs[i]
		byNode[o.Node] = append(byNode[o.Node], o)
	}
	out := make(map[string]map[string]int, len(byNode))
	for id, list := range byNode {
		slices.SortFunc(list, func(a, b *serializer.Output) int {
			aElse, bElse := a.Kind == published.OutputElse, b.Kind == published.OutputElse
			if aElse != bElse {
				if aElse {
					return 1
				}
				return -1
			}
			return cmp.Or(strings.Compare(a.Order, b.Order), strings.Compare(a.ID, b.ID))
		})
		ports := make(map[string]int, len(list))
		for i, o := range list {
			ports[o.ID] = i
		}
		out[id] = ports
	}
	return out
}

// FromFiles returns the export of a snapshot's files, of the version that the
// server writes, of the board of the ID boardID, or of the whole project for
// "", with Source left for its caller, which knows the snapshot. It returns
// ErrBoardNotFound for a board that the files do not hold.
func FromFiles(files map[string][]byte, boardID string) (*Export, error) {
	var manifest published.Manifest
	if err := decode(files, published.ManifestPath, &manifest); err != nil {
		return nil, err
	}
	var components published.Components
	if err := decode(files, published.ComponentsPath, &components); err != nil {
		return nil, err
	}
	speakers := make(map[string]string, len(components.Components))
	for _, c := range components.Components {
		speakers[c.ID] = c.Name
	}
	var narrowed *Board
	boards := make([]board, 0, len(manifest.Boards))
	for _, m := range manifest.Boards {
		if boardID != "" && m.ID != boardID {
			continue
		}
		info := Board{ID: m.ID, Name: m.Name, Slug: m.Slug}
		if boardID != "" {
			narrowed = &info
		}
		var f published.Board
		if err := decode(files, m.File, &f); err != nil {
			return nil, err
		}
		boards = append(boards, boardOfFile(info, m.EntryNodeID, &f, speakers))
	}
	if boardID != "" && narrowed == nil {
		return nil, ErrBoardNotFound
	}
	slices.SortFunc(boards, func(a, b board) int { return strings.Compare(a.info.ID, b.info.ID) })
	p := manifest.Project
	return assemble(Project{ID: p.ID, Name: p.Name, Slug: p.Slug}, Source{Kind: SourceSnapshot}, boards, narrowed), nil
}

// decode reads the file at path of files into v.
func decode(files map[string][]byte, path string, v any) error {
	data, ok := files[path]
	if !ok {
		return fmt.Errorf("textexport: the snapshot has no %s", path)
	}
	if err := published.Decode(data, v); err != nil {
		return fmt.Errorf("textexport: %s: %w", path, err)
	}
	return nil
}

// boardOfFile returns the board that f, its file, holds, with the name of
// each speaker of speakers.
func boardOfFile(info Board, entry string, f *published.Board, speakers map[string]string) board {
	out := board{info: info, entry: entry, nodes: make([]*node, 0, len(f.Nodes))}
	nodes := make(map[string]*node, len(f.Nodes))
	ports := make(map[string]map[string]int, len(f.Nodes))
	for _, raw := range f.Nodes {
		n := &node{id: raw.NodeID()}
		switch v := raw.(type) {
		case *published.Element:
			n.title, n.speaker = v.Title, speakerOf(v.Kind, v.Speaker, speakers)
			if v.Text != nil {
				n.body = textOf(v.Text.Segments)
			}
			ports[n.id] = map[string]int{"out": 0}
		case *published.Branch:
			n.title = v.Title
			list := slices.Clone(v.Outputs)
			slices.SortFunc(list, func(a, b published.BranchOutput) int { return cmp.Compare(a.Order, b.Order) })
			places := make(map[string]int, len(list))
			for i, o := range list {
				places[o.ID] = i
			}
			ports[n.id] = places
		case *published.Jumper:
			n.title = v.Title
		}
		nodes[n.id] = n
		out.nodes = append(out.nodes, n)
	}
	edges := map[string][]edge[int64]{}
	for i := range f.Connections {
		c := &f.Connections[i]
		if nodes[c.From] == nil {
			continue
		}
		rank, ok := ports[c.From][c.FromPort]
		if !ok {
			rank = unknownPort
		}
		e := choice{to: c.To}
		if c.Label != nil {
			e.label = textOf(c.Label.Segments)
		}
		if c.Condition != nil {
			e.condition = c.Condition.Source
		}
		edges[c.From] = append(edges[c.From], edge[int64]{rank: rank, port: c.FromPort, key: int64(c.Order), id: c.ID, c: e})
	}
	for id, list := range edges {
		nodes[id].leaving = ordered(list)
	}
	slices.SortFunc(out.nodes, func(a, b *node) int { return strings.Compare(a.id, b.id) })
	return out
}

// header is the CSV's first record, the names of its columns, which are the
// JSON's names of a row's fields.
var header = []string{"node_id", "board", "node_title", "speaker", "text_type", "text", "condition", "word_count",
	"text_hash"}

// CSV returns the export as CSV (AC1): UTF-8 with a byte order mark, for
// Excel, each record ended by CRLF, and a field quoted, its quotes doubled,
// where it holds a comma, a quote, a carriage return, or a line feed, or has
// white space at either end, as RFC 4180 says. It writes the records itself,
// as encoding/csv's Writer with UseCRLF writes each line feed in a field as
// CRLF, which would not round-trip. Every text cell passes through guard
// (AC4), so a spreadsheet that reads the file as comma-separated evaluates
// none. One set to another list separator, as a semicolon, splits a text at
// it, and the guard does not reach the part after it, which only importing
// the file as comma-separated, or the JSON, avoids.
func (e *Export) CSV() []byte {
	var b bytes.Buffer
	b.WriteRune(0xFEFF)
	writeRecord(&b, header)
	for i := range e.Rows {
		r := &e.Rows[i]
		writeRecord(&b, []string{guard(r.NodeID), guard(r.Board), guard(r.NodeTitle), guard(r.Speaker),
			guard(r.TextType), guard(r.Text), guard(r.Condition), strconv.Itoa(r.WordCount), guard(r.TextHash)})
	}
	return b.Bytes()
}

// writeRecord writes the fields to b as a record of the CSV.
func writeRecord(b *bytes.Buffer, fields []string) {
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		if !needsQuotes(f) {
			b.WriteString(f)
			continue
		}
		b.WriteByte('"')
		b.WriteString(strings.ReplaceAll(f, `"`, `""`))
		b.WriteByte('"')
	}
	b.WriteString("\r\n")
}

// needsQuotes reports whether the field f is quoted: where RFC 4180 asks, and
// where it has white space at either end, which some readers trim unless it
// is quoted.
func needsQuotes(f string) bool {
	return strings.ContainsAny(f, "\",\r\n") || f != strings.TrimSpace(f)
}

// guard returns the text of a cell of the CSV, prefixed with a single quote
// where its first character is one that a spreadsheet may take for the start
// of a formula, =, +, -, @, a tab, or a carriage return, so that one that
// reads the file as comma-separated shows the text and never evaluates it
// (FR-TEXT-05 AC4). The JSON and the hash are of the original text.
func guard(s string) string {
	if s != "" && strings.IndexByte("=+-@\t\r", s[0]) >= 0 {
		return "'" + s
	}
	return s
}

// JSON returns the export as JSON, the lossless format, whose texts are as
// their authors wrote them, without the CSV's guard, and without HTML's
// characters escaped.
func (e *Export) JSON() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(e); err != nil {
		return nil, fmt.Errorf("textexport: write the JSON: %w", err)
	}
	return b.Bytes(), nil
}

// FileName returns the name of the export's file in the format: the project's
// slug, the snapshot's version or draft, the board's slug if it is narrowed
// to one, and text, as harbour-1.0.0-intro-text.csv.
func (e *Export) FileName(format string) string {
	parts := []string{e.Project.Slug}
	if e.Source.Kind == SourceDraft {
		parts = append(parts, SourceDraft)
	} else {
		parts = append(parts, e.Source.Version)
	}
	if e.Board != nil {
		parts = append(parts, e.Board.Slug)
	}
	return strings.Join(append(parts, "text"), "-") + "." + format
}
