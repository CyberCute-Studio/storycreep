package textexport

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
)

// parseCSV reads data as RFC 4180 has it, strictly, as encoding/csv's Reader
// does not, which turns a CRLF within a quoted field into a line feed: a byte
// order mark, then records that each end with CRLF, of fields that are quoted
// or hold no quote, comma, or line ending.
func parseCSV(t *testing.T, data []byte) [][]string {
	t.Helper()
	bom := string(rune(0xFEFF))
	s, ok := strings.CutPrefix(string(data), bom)
	if !ok {
		t.Fatalf("the CSV does not start with a byte order mark: %q", data[:min(len(data), 8)])
	}
	var records [][]string
	var record []string
	var field strings.Builder
	quoted, wasQuoted := false, false
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case quoted && c == '"' && i+1 < len(s) && s[i+1] == '"':
			field.WriteByte('"')
			i += 2
		case quoted && c == '"':
			quoted = false
			i++
		case quoted:
			field.WriteByte(c)
			i++
		case c == '"' && field.Len() == 0 && !wasQuoted:
			quoted, wasQuoted = true, true
			i++
		case c == ',':
			record = append(record, field.String())
			field.Reset()
			wasQuoted = false
			i++
		case c == '\r' && i+1 < len(s) && s[i+1] == '\n':
			records = append(records, append(record, field.String()))
			record = nil
			field.Reset()
			wasQuoted = false
			i += 2
		case c == '"' || c == '\r' || c == '\n' || wasQuoted:
			t.Fatalf("a quote, a line ending, or text after a quoted field is not as RFC 4180 has it, at byte %d of %q",
				i, s)
		default:
			field.WriteByte(c)
			i++
		}
	}
	if quoted || field.Len() != 0 || record != nil {
		t.Fatalf("the last record of the CSV does not end with CRLF: %q", s)
	}
	return records
}

// cells returns what the CSV's record of r should hold of each column, but
// for the guard of AC4.
func cells(r Row, words string) []string {
	return []string{r.NodeID, r.Board, r.NodeTitle, r.Speaker, r.TextType, r.Text, r.Condition, words, r.TextHash}
}

// TestCSVRoundTrip checks that the CSV is RFC 4180's, in UTF-8 with a byte
// order mark, and that a strict reader reads back every text as it was,
// whatever commas, quotes, and line endings it holds, of a field or of a
// record, and white space at its ends.
//
// Verifies: FR-TEXT-05/AC1
func TestCSVRoundTrip(t *testing.T) {
	t.Parallel()
	texts := []string{
		"plain", "a, b", `say "hi"`, `"`, `""`, ",", "line\nbreak", "cr\rin", "crlf\r\nin", "trailing\n",
		" spaced ", "tab\tin", "ünï, “cödé” 日本語 🙂", "",
	}
	e := &Export{}
	for _, s := range texts {
		e.add(Row{NodeID: "nd_x", Board: s, NodeTitle: s, Speaker: s, TextType: TypeBody, Text: s, Condition: s,
			WordCount: 7, TextHash: richtext.Hash(s)})
	}
	data := e.CSV()
	if !bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("the CSV starts with %x, want UTF-8's byte order mark", data[:3])
	}
	records := parseCSV(t, data)
	if len(records) != len(texts)+1 {
		t.Fatalf("the CSV has %d records, want a header and %d", len(records), len(texts))
	}
	if !slices.Equal(records[0], header) {
		t.Errorf("the header is %q, want %q", records[0], header)
	}
	for i, r := range e.Rows {
		if got, want := records[i+1], cells(r, "7"); !slices.Equal(got, want) {
			t.Errorf("the record of %q reads back as %q, want %q", texts[i], got, want)
		}
	}
}

// TestFormulaGuard checks that every text cell of the CSV that a spreadsheet
// could take for a formula starts with a single quote, in each text column,
// that a cell that it could not is left alone, and that neither the JSON nor
// the hash, of the original text, is altered.
//
// Verifies: FR-TEXT-05/AC4
func TestFormulaGuard(t *testing.T) {
	t.Parallel()
	dangerous := []string{
		`=HYPERLINK("http://example.invalid","x")`, `=cmd|' /C calc'!A0`, "+1", "-1", "@SUM(A1)", "\tleading tab",
		"\rleading cr",
	}
	e := &Export{}
	for _, s := range dangerous {
		e.add(Row{NodeID: "nd_x", Board: s, NodeTitle: s, Speaker: s, TextType: TypeChoice, Text: s, Condition: s,
			WordCount: 1, TextHash: richtext.Hash(s)})
	}
	e.add(Row{NodeID: "nd_y", Board: "a=b", NodeTitle: "1+1", Speaker: "x@y", TextType: TypeBody, Text: "safe -1",
		WordCount: 2, TextHash: richtext.Hash("safe -1")})
	records := parseCSV(t, e.CSV())
	for i, s := range dangerous {
		rec := records[i+1]
		for _, col := range []int{1, 2, 3, 5, 6} {
			if want := "'" + s; rec[col] != want {
				t.Errorf("the %s cell of %q is %q, want %q", header[col], s, rec[col], want)
			}
		}
		if rec[8] != richtext.Hash(s) {
			t.Errorf("the hash of %q is %s, want that of the original text", s, rec[8])
		}
	}
	if got := records[len(dangerous)+1]; got[1] != "a=b" || got[2] != "1+1" || got[3] != "x@y" || got[5] != "safe -1" {
		t.Errorf("a cell that starts with no formula's character was changed: %q", got)
	}

	data, err := e.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var back Export
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(back.Rows, e.Rows) {
		t.Errorf("the JSON reads back as %+v, want the rows unaltered", back.Rows)
	}
	if !bytes.Contains(data, []byte(`=HYPERLINK(`)) {
		t.Errorf("the JSON holds %s, want the texts as they are", data)
	}
	html, err := (&Export{Rows: []Row{{Text: "<b>&"}}}).JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(html, []byte("<b>&")) {
		t.Errorf("the JSON holds %s, want HTML's characters as they are", html)
	}
}

// semicolonCells returns every cell that a spreadsheet whose list separator
// is a semicolon could read of the CSV data. It reads none of the file's
// quoting, which starts in the middle of what it takes for a cell, so a cell
// starts at each semicolon and line ending, and it drops the double quotes
// that a cell starts with, which a reader may take for the cell's own.
func semicolonCells(data []byte) []string {
	s := strings.TrimPrefix(string(data), string(rune(0xFEFF)))
	cells := strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '\r' || r == '\n' })
	for i, c := range cells {
		cells[i] = strings.TrimLeft(c, `"`)
	}
	return cells
}

// TestFormulaGuardOfEveryCellStart checks the guard where a spreadsheet whose
// list separator is a semicolon starts a cell: at each semicolon and line
// break within a text, past any double quotes, and at a leading line feed;
// that what starts no formula there, as a blank line or a quoted line, is left
// alone; and, of many texts of the characters that matter, that no cell that
// either kind of spreadsheet reads starts a formula, and that the guard only
// ever adds single quotes.
//
// Verifies: FR-TEXT-05/AC4
func TestFormulaGuardOfEveryCellStart(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"a;=1+1":            "a;'=1+1",
		`a;"=1+1"`:          `a;'"=1+1"`,
		"a;\t=b":            "a;'\t=b",
		"a\n- b":            "a\n'- b",
		"a\r\n@b":           "a\r\n'@b",
		"a\r+b":             "a\r'+b",
		"\n=1+1":            "'\n'=1+1",
		"\r\n=x":            "'\r\n'=x",
		"\nplain":           "'\nplain",
		`"=1+1"`:            `'"=1+1"`,
		";=a;=b":            ";'=a;'=b",
		"a\n\n=b":           "a\n\n'=b",
		"a\n\nb":            "a\n\nb",
		"first; next":       "first; next",
		"1;2":               "1;2",
		"a;":                "a;",
		"a\n":               "a\n",
		"He said:\n\"Go.\"": "He said:\n\"Go.\"",
		`"quoted"`:          `"quoted"`,
		"it's":              "it's",
		"":                  "",
	} {
		if got := guard(in); got != want {
			t.Errorf("guard(%q) = %q, want %q", in, got, want)
		}
	}

	alphabet := []string{"=", "+", "-", "@", "\t", "\r", "\n", ";", `"`, ",", "'", " ", "a", "é"}
	random := rand.New(rand.NewPCG(82, 82))
	for range 5000 {
		var b strings.Builder
		for range random.IntN(12) + 1 {
			b.WriteString(alphabet[random.IntN(len(alphabet))])
		}
		s := b.String()
		e := &Export{}
		e.add(Row{NodeID: "nd_x", Board: s, NodeTitle: s, Speaker: s, TextType: TypeBody, Text: s, Condition: s,
			TextHash: richtext.Hash(s)})
		data := e.CSV()
		for _, c := range semicolonCells(data) {
			if c != "" && strings.IndexByte("=+-@\t", c[0]) >= 0 {
				t.Fatalf("of the text %q, a spreadsheet set to a semicolon reads the cell %q", s, c)
			}
		}
		// A reader that does read quotes at a field's start, as a stricter one
		// than semicolonCells might.
		r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})))
		r.Comma, r.LazyQuotes, r.FieldsPerRecord = ';', true, -1
		records, err := r.ReadAll()
		if err != nil {
			t.Fatalf("of the text %q, a reader of semicolons fails: %v", s, err)
		}
		for _, c := range slices.Concat(records...) {
			if c != "" && strings.IndexByte("=+-@\t", c[0]) >= 0 {
				t.Fatalf("of the text %q, a reader of semicolons reads the cell %q", s, c)
			}
		}
		cell := parseCSV(t, data)[1][5]
		if strings.IndexByte("=+-@\t\r\n", cell[0]) >= 0 {
			t.Fatalf("of the text %q, a spreadsheet set to a comma reads the cell %q", s, cell)
		}
		if strings.ReplaceAll(cell, "'", "") != strings.ReplaceAll(s, "'", "") {
			t.Fatalf("the guard of %q wrote %q, which differs by more than single quotes", s, cell)
		}
	}
}

// doc returns a rich text of one paragraph of s, or nil for "".
func doc(t *testing.T, s string) *richtext.Doc {
	t.Helper()
	content := []any{}
	if s != "" {
		content = append(content, map[string]any{"type": "text", "text": s})
	}
	raw, err := json.Marshal(map[string]any{"type": "doc", "content": []any{
		map[string]any{"type": "paragraph", "content": content}}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := richtext.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// world is a draft of two boards. The first, whose IDs are in an order other
// than its flow's, holds an entry element with two choices, written in the
// other order than the flow offers them, a branch with an if, an elseif, and
// an else, each connection of its outputs written in the other order too, a
// cycle back to the entry, an element with an empty text, and an element that
// the entry does not reach. The second holds one element.
type world struct {
	d                                    *serializer.Draft
	first, second                        string
	stray, one, gate, empty, entry, five string
	other                                string
}

func newWorld(t *testing.T) world {
	t.Helper()
	nodeIDs := make([]string, 7)
	for i := range nodeIDs {
		nodeIDs[i] = ids.Encode(ids.Node, ids.New())
	}
	slices.Sort(nodeIDs)
	boardIDs := []string{ids.Encode(ids.Board, ids.New()), ids.Encode(ids.Board, ids.New())}
	slices.Sort(boardIDs)
	w := world{first: boardIDs[0], second: boardIDs[1], stray: nodeIDs[0], one: nodeIDs[1], empty: nodeIDs[2],
		entry: nodeIDs[3], gate: nodeIDs[4], five: nodeIDs[5], other: nodeIDs[6]}
	elara, kind := ids.Encode(ids.Component, ids.New()), ids.Encode(ids.ComponentType, ids.New())
	ifOut, elseIfOut, elseOut := ids.Encode(ids.BranchOutput, ids.New()), ids.Encode(ids.BranchOutput, ids.New()),
		ids.Encode(ids.BranchOutput, ids.New())
	element := func(id, board, title, text string) serializer.Node {
		return serializer.Node{ID: id, Board: board, Type: published.NodeElement, Kind: "dialogue", Title: title,
			Text: doc(t, text)}
	}
	greet := element(w.entry, w.first, "Start", "Hello there.")
	greet.Speaker = elara
	// A narration that keeps the speaker that it had as dialogue, whom no row
	// names.
	five := element(w.five, w.first, "", "Five.")
	five.Kind, five.Speaker = "narration", elara
	conn := func(from, output, to, order, label, condition string) serializer.Connection {
		c := serializer.Connection{ID: ids.Encode(ids.Connection, ids.New()), Board: w.first, From: from,
			FromOutput: output, To: to, Order: order}
		if label != "" {
			c.Label = doc(t, label)
		}
		if condition != "" {
			c.Condition = &serializer.Expression{Source: condition}
		}
		return c
	}
	w.d = &serializer.Draft{
		Project: serializer.Project{ID: ids.Encode(ids.Project, ids.New()), Name: "Harbour", Slug: "harbour"},
		Boards: []serializer.Board{
			{ID: w.first, Slug: "quay", Name: "Quay", EntryNode: w.entry},
			{ID: w.second, Slug: "attic", Name: "Attic"},
		},
		Nodes: []serializer.Node{
			greet,
			{ID: w.gate, Board: w.first, Type: published.NodeBranch, Title: "Gate"},
			element(w.one, w.first, "One", "One,"),
			element(w.empty, w.first, "Empty", ""),
			five,
			element(w.stray, w.first, "Stray", "Stray."),
			element(w.other, w.second, "Other", "Other board."),
		},
		Outputs: []serializer.Output{
			{ID: elseOut, Node: w.gate, Kind: published.OutputElse},
			{ID: elseIfOut, Node: w.gate, Kind: published.OutputElseIf, Order: "a1",
				Condition: &serializer.Expression{Source: "b"}},
			{ID: ifOut, Node: w.gate, Kind: published.OutputIf, Order: "a0",
				Condition: &serializer.Expression{Source: "a"}},
		},
		Connections: []serializer.Connection{
			conn(w.entry, "", w.one, "a1", "Second", ""),
			conn(w.entry, "", w.gate, "a0", "First", "gold > 0"),
			conn(w.gate, elseOut, w.five, "a0", "Otherwise", ""),
			conn(w.gate, ifOut, w.empty, "a0", "Lit", ""),
			conn(w.one, "", w.entry, "a0", "", ""),
		},
		ComponentTypes: []serializer.ComponentType{{ID: kind, Key: "character", Name: "Character"}},
		Components:     []serializer.Component{{ID: elara, ComponentType: kind, Key: "elara", Name: "Elara"}},
	}
	return w
}

// TestRowOrder checks the rows of a draft: the boards by ID; each board's
// nodes breadth-first from its entry along its connections in the order that
// the flow offers them, its ports' in turn and each port's by its key, and
// then those that it does not reach, by ID; each node's text before its
// choices; a speaker on a body alone, a condition on a choice; no row of an
// empty text, or of a connection without a label; and a total of the words.
func TestRowOrder(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	e, err := FromDraft(w.d, richtext.NoNames{}, "")
	if err != nil {
		t.Fatal(err)
	}
	type want struct{ node, typ, text, speaker, condition string }
	wants := []want{
		{w.entry, TypeBody, "Hello there.", "Elara", ""},
		{w.entry, TypeChoice, "First", "", "gold > 0"},
		{w.entry, TypeChoice, "Second", "", ""},
		{w.gate, TypeChoice, "Lit", "", ""},
		{w.gate, TypeChoice, "Otherwise", "", ""},
		{w.one, TypeBody, "One,", "", ""},
		{w.five, TypeBody, "Five.", "", ""},
		{w.stray, TypeBody, "Stray.", "", ""},
		{w.other, TypeBody, "Other board.", "", ""},
	}
	got := make([]want, len(e.Rows))
	words := 0
	for i, r := range e.Rows {
		got[i] = want{r.NodeID, r.TextType, r.Text, r.Speaker, r.Condition}
		words += r.WordCount
		if r.TextHash != richtext.Hash(r.Text) {
			t.Errorf("the hash of %q is %s", r.Text, r.TextHash)
		}
	}
	if !slices.Equal(got, wants) {
		t.Errorf("the rows are\n%+v\nwant\n%+v", got, wants)
	}
	if e.Words != words || words != 11 {
		t.Errorf("the total is %d, the rows add up to %d, want 11", e.Words, words)
	}
	if e.Rows[0].Board != "Quay" || e.Rows[0].NodeTitle != "Start" || e.Rows[len(e.Rows)-1].Board != "Attic" {
		t.Errorf("the first row is %+v and the last %+v", e.Rows[0], e.Rows[len(e.Rows)-1])
	}
	if e.Source.Kind != SourceDraft || e.Project.Slug != "harbour" || e.Board != nil {
		t.Errorf("the export is of %+v, %+v, board %v", e.Source, e.Project, e.Board)
	}
}

// TestDraftAndFilesAgree checks that a draft's export and that of the files
// that the serializer writes of it are the same rows, in one order, so that
// a release exports what the draft that it was published from does, for the
// whole project and for a board.
func TestDraftAndFilesAgree(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	built, err := serializer.Serialize(w.d, serializer.Stamp{Generator: serializer.Generator(), At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	for _, board := range []string{"", w.first, w.second} {
		a, err := FromDraft(w.d, richtext.NoNames{}, board)
		if err != nil {
			t.Fatal(err)
		}
		b, err := FromFiles(built.Files, board)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(a.Rows, b.Rows) {
			t.Errorf("of the board %q, the draft's rows are\n%+v\nand the files'\n%+v", board, a.Rows, b.Rows)
		}
		if a.Words != b.Words || a.Project != b.Project {
			t.Errorf("of the board %q, the draft's %d words of %+v, the files' %d of %+v", board, a.Words, a.Project,
				b.Words, b.Project)
		}
		if board != "" && (a.Board == nil || b.Board == nil || *a.Board != *b.Board || a.Board.ID != board) {
			t.Errorf("the exports are narrowed to %+v and %+v, want %s", a.Board, b.Board, board)
		}
	}
}

// TestSameBytes checks that the same draft exports the same bytes, in both
// formats, whatever order its entities were read in.
//
// Verifies: FR-TEXT-05/AC2
func TestSameBytes(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	first, err := FromDraft(w.d, richtext.NoNames{}, "")
	if err != nil {
		t.Fatal(err)
	}
	wantCSV, wantJSON := first.CSV(), mustJSON(t, first)
	for range 20 {
		d := *w.d
		d.Boards, d.Nodes = slices.Clone(d.Boards), slices.Clone(d.Nodes)
		d.Outputs, d.Connections = slices.Clone(d.Outputs), slices.Clone(d.Connections)
		rand.Shuffle(len(d.Boards), func(i, j int) { d.Boards[i], d.Boards[j] = d.Boards[j], d.Boards[i] })
		rand.Shuffle(len(d.Nodes), func(i, j int) { d.Nodes[i], d.Nodes[j] = d.Nodes[j], d.Nodes[i] })
		rand.Shuffle(len(d.Outputs), func(i, j int) { d.Outputs[i], d.Outputs[j] = d.Outputs[j], d.Outputs[i] })
		rand.Shuffle(len(d.Connections), func(i, j int) {
			d.Connections[i], d.Connections[j] = d.Connections[j], d.Connections[i]
		})
		e, err := FromDraft(&d, richtext.NoNames{}, "")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(e.CSV(), wantCSV) || !bytes.Equal(mustJSON(t, e), wantJSON) {
			t.Fatal("the same draft, read in another order, exports other bytes")
		}
	}
}

func mustJSON(t *testing.T, e *Export) []byte {
	t.Helper()
	data, err := e.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestBoardNotFound checks that a board that the source does not hold is
// refused, of the draft and of the files.
func TestBoardNotFound(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	built, err := serializer.Serialize(w.d, serializer.Stamp{Generator: serializer.Generator(), At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	missing := ids.Encode(ids.Board, ids.New())
	if _, err := FromDraft(w.d, richtext.NoNames{}, missing); !errors.Is(err, ErrBoardNotFound) {
		t.Errorf("the draft's export of a board that it lacks: %v", err)
	}
	if _, err := FromFiles(built.Files, missing); !errors.Is(err, ErrBoardNotFound) {
		t.Errorf("the files' export of a board that they lack: %v", err)
	}
}

// TestFileName checks the names of an export's file.
func TestFileName(t *testing.T) {
	t.Parallel()
	p := Project{Slug: "harbour"}
	for _, c := range []struct {
		e      Export
		format string
		want   string
	}{
		{Export{Project: p, Source: Source{Kind: SourceDraft}}, FormatCSV, "harbour-draft-text.csv"},
		{Export{Project: p, Source: Source{Kind: SourceSnapshot, Version: "1.0.0"}}, FormatJSON,
			"harbour-1.0.0-text.json"},
		{Export{Project: p, Source: Source{Kind: SourceSnapshot, Version: "1.0.0"}, Board: &Board{Slug: "intro"}},
			FormatCSV, "harbour-1.0.0-intro-text.csv"},
	} {
		if got := c.e.FileName(c.format); got != c.want {
			t.Errorf("the name is %q, want %q", got, c.want)
		}
	}
}
