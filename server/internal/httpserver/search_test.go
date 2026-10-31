package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/search"
	"github.com/CyberCute-Studio/storycreep/server/internal/search/searchtest"
)

// searchPath returns the path of a search of the project for q, with the
// further query parameters params, names and values in turn.
func searchPath(project, q string, params ...string) string {
	v := url.Values{"q": {q}}
	for i := 0; i+1 < len(params); i += 2 {
		v.Set(params[i], params[i+1])
	}
	return projectPath(project) + "/search?" + v.Encode()
}

// searchPage returns the page of results that a GET of path answers, as the
// user reads it, or fails the test.
func searchPage(t *testing.T, h http.Handler, si signedIn, path string) api.Page[search.Result] {
	t.Helper()
	res := call(h, http.MethodGet, path, nil, si.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, res.status, res.body)
	}
	var page api.Page[search.Result]
	if err := json.Unmarshal(res.body, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

// searchIDs returns the IDs of the results of a search of the project for
// q, with params, of every page of limit results, in their order, as the
// user reads them, or fails the test.
func searchIDs(t *testing.T, h http.Handler, si signedIn, project, q string, limit int, params ...string) []string {
	t.Helper()
	var out []string
	cursor := ""
	for range 100 {
		page := searchPage(t, h, si, searchPath(project, q, append(params, "limit", strconv.Itoa(limit), "cursor", cursor)...))
		for _, r := range page.Items {
			out = append(out, r.ID)
		}
		if page.NextCursor == nil {
			return out
		}
		cursor = *page.NextCursor
	}
	t.Fatalf("the search for %q has more than 100 pages", q)
	return nil
}

// searchDrift returns how the project's documents of search differ from a
// recomputation of them from scratch (searchtest.Drift).
func searchDrift(t testing.TB, conn searchtest.Querier, project string) []string {
	t.Helper()
	drift, err := searchtest.Drift(context.Background(), conn, decode(t, ids.Project, project))
	if err != nil {
		t.Fatal(err)
	}
	return drift
}

// marked returns spans as one text, each match between brackets, as the
// tests compare them.
func marked(spans []search.Span) string {
	var b strings.Builder
	for _, s := range spans {
		if s.Match {
			b.WriteString("[" + s.Text + "]")
		} else {
			b.WriteString(s.Text)
		}
	}
	return b.String()
}

// resultOf returns the result of the entity id among results, or fails the
// test.
func resultOf(t *testing.T, results []search.Result, id string) search.Result {
	t.Helper()
	for _, r := range results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no result of %s among %v", id, results)
	return search.Result{}
}

// searchWorld is a project whose content a search finds, built through the
// command endpoint, as a client builds it:
//
//   - on the board Harbour, the element lamp, titled The Lantern, whose text
//     is "Light it before the storm.", the element tide, untitled, whose text
//     is "The old lantern flickers in the cold wind.", the element ask,
//     whose text mentions Elara and shows lantern_lit, the branch gate,
//     titled Lantern gate, the connection carry from lamp to tide, labelled
//     "Carry the lantern", and the element gone, whose text holds lantern,
//     deleted to the trash;
//   - the global variable lit, lantern_lit, described as "Whether the lantern
//     is lit.", and Harbour's variable wick, described as "The lantern's wick
//     length.";
//   - the component type Prop, with the definitions material, a string whose
//     default is "Brass and glass", notes, a text, and size, an int, and its
//     component old, named Old Lantern, whose notes are "Found in the
//     cellar."; and the component elara, of the type Prop, named Elara;
//   - and on the board Attic, deleted to the trash, the element dusty, whose
//     text holds lantern.
type searchWorld struct {
	s                                  *server
	admin                              *pgx.Conn
	owner                              signedIn
	project                            string
	harbour, attic                     string
	lamp, tide, ask, gate, gone, dusty string
	carry                              string
	lit, wick                          string
	prop, material, notes, size        string
	old, elara                         string
}

// newSearchWorld returns a new searchWorld, of a server of its own.
func newSearchWorld(t *testing.T) *searchWorld {
	t.Helper()
	s, d, owner, ws := inviteServer(t, "open", nil)
	w := &searchWorld{s: s, admin: d.Admin(t), owner: owner, project: createProject(t, s.main, owner, ws, "Port").ID}
	w.harbour, w.attic = ids.Encode(ids.Board, ids.New()), ids.Encode(ids.Board, ids.New())
	w.lamp, w.tide, w.ask, w.gate, w.gone, w.dusty = newNodeID(), newNodeID(), newNodeID(), newNodeID(), newNodeID(),
		newNodeID()
	w.carry = ids.Encode(ids.Connection, ids.New())
	w.lit, w.wick = ids.Encode(ids.Variable, ids.New()), ids.Encode(ids.Variable, ids.New())
	w.prop = ids.Encode(ids.ComponentType, ids.New())
	// A component's document holds its values in the order of their
	// definitions' IDs, which IDs made within one millisecond are in no order
	// of: the definitions' are made in the order of their UUIDs.
	defs := make([]ids.UUID, 3)
	for i := range defs {
		defs[i] = ids.New()
	}
	slices.SortFunc(defs, func(a, b ids.UUID) int { return bytes.Compare(a[:], b[:]) })
	w.material, w.notes, w.size = ids.Encode(ids.AttributeDef, defs[0]), ids.Encode(ids.AttributeDef, defs[1]),
		ids.Encode(ids.AttributeDef, defs[2])
	w.old, w.elara = ids.Encode(ids.Component, ids.New()), ids.Encode(ids.Component, ids.New())
	pos := map[string]int{"x": 0, "y": 0}
	cmd := func(typ string, p map[string]any) wireCommand { return wireCommand{Type: typ, Payload: p} }
	node := func(id, board, typ, title string, text json.RawMessage) wireCommand {
		p := map[string]any{"id": id, "board_id": board, "type": typ, "title": title, "position": pos}
		if text != nil {
			p["text"] = text
		}
		return cmd("node.create", p)
	}
	sendBatch(t, s.main, owner, w.project,
		cmd("board.create", map[string]any{"id": w.harbour, "name": "Harbour"}),
		cmd("board.create", map[string]any{"id": w.attic, "name": "Attic"}),
		cmd("variable.create", map[string]any{"id": w.lit, "key": "lantern_lit", "type": "bool", "scope": "global",
			"description": "Whether the lantern is lit."}),
		cmd("variable.create", map[string]any{"id": w.wick, "key": "wick", "type": "int", "scope": "board",
			"board_id": w.harbour, "description": "The lantern's wick length."}),
		cmd("component_type.create", map[string]any{"id": w.prop, "name": "Prop"}),
		cmd("attribute.create", map[string]any{"id": w.material, "component_type_id": w.prop, "name": "Material",
			"type": "string", "default": "Brass and glass"}),
		cmd("attribute.create", map[string]any{"id": w.notes, "component_type_id": w.prop, "name": "Notes",
			"type": "text"}),
		cmd("attribute.create", map[string]any{"id": w.size, "component_type_id": w.prop, "name": "Size",
			"type": "int", "default": 3}),
		cmd("component.create", map[string]any{"id": w.old, "component_type_id": w.prop, "name": "Old Lantern"}),
		cmd("component.set_attribute", map[string]any{"id": w.old, "attribute_id": w.notes,
			"value": "Found in the cellar."}),
		cmd("component.create", map[string]any{"id": w.elara, "component_type_id": w.prop, "name": "Elara"}),
		node(w.lamp, w.harbour, "element", "The Lantern", paragraph("Light it before the storm.")),
		node(w.tide, w.harbour, "element", "", paragraph("The old lantern flickers in the cold wind.")),
		node(w.ask, w.harbour, "element", "Ask", richText(textInline("Ask "), mentionInline(w.elara),
			textInline(" about the "), valueInline(w.lit), textInline(" tonight."))),
		node(w.gate, w.harbour, "branch", "Lantern gate", nil),
		node(w.gone, w.harbour, "element", "", paragraph("A lantern lost at sea.")),
		cmd("connection.create", map[string]any{"id": w.carry, "from_node_id": w.lamp, "to_node_id": w.tide,
			"label": paragraph("Carry the lantern")}),
		node(w.dusty, w.attic, "element", "", paragraph("A dusty lantern in the attic.")),
	)
	sendBatch(t, s.main, owner, w.project,
		cmd("node.delete", map[string]any{"id": w.gone}),
		cmd("board.delete", map[string]any{"id": w.attic}),
	)
	return w
}

// TestSearch checks a search of a project's content through the API: each
// node, connection, variable, and component whose title or body holds a word
// that starts with the query's, and none in the trash, each with its kind,
// its board, its node, and its title and snippet with the matches marked
// (AC2), the matches in titles before those in bodies, however many words
// of a body match; the filter by kind;
// the pages of a list, which list what one page lists, in its order; the
// documents as later batches leave them; and a body whose match lies past
// what its snippet is taken from, which is found, with its start as its
// snippet.
//
// Verifies: FR-PROJ-03/AC2
func TestSearch(t *testing.T) {
	t.Parallel()
	w := newSearchWorld(t)
	h := w.s.main
	page := searchPage(t, h, w.owner, searchPath(w.project, "lan", "limit", "50"))
	var got []string
	for _, r := range page.Items {
		got = append(got, r.ID)
	}
	titled := []string{w.lamp, w.gate, w.lit, w.old}
	bodied := []string{w.tide, w.carry, w.wick}
	if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(append(titled, bodied...)))) {
		t.Fatalf("the search for lan finds %v, want %v and %v", got, titled, bodied)
	}
	for _, id := range got[:len(titled)] {
		if !slices.Contains(titled, id) {
			t.Errorf("the search for lan lists %v, want the matches in titles, %v, first", got, titled)
		}
	}
	if page.NextCursor != nil {
		t.Errorf("a page of 50 results of %d has a next page", len(got))
	}

	for _, c := range []struct {
		id, typ, board, boardName, node, nodeTitle, title, snippet string
	}{
		{w.lamp, "node", w.harbour, "Harbour", w.lamp, "The Lantern", "The [Lantern]", "Light it before the storm."},
		{w.tide, "node", w.harbour, "Harbour", w.tide, "", "", "The old [lantern] flickers in the cold wind."},
		{w.gate, "node", w.harbour, "Harbour", w.gate, "Lantern gate", "[Lantern] gate", ""},
		{w.carry, "connection", w.harbour, "Harbour", w.lamp, "The Lantern", "", "Carry the [lantern]"},
		{w.lit, "variable", "", "", "", "", "[lantern]_lit", "Whether the [lantern] is lit."},
		{w.wick, "variable", w.harbour, "Harbour", "", "", "wick", "The [lantern]'s wick length."},
		{w.old, "component", "", "", "", "", "Old [Lantern]", "old_[lantern]\nBrass and glass\nFound in the cellar."},
	} {
		r := resultOf(t, page.Items, c.id)
		boardName, nodeTitle := "", ""
		if r.BoardName != nil {
			boardName = *r.BoardName
		}
		if r.NodeTitle != nil {
			nodeTitle = *r.NodeTitle
		}
		if string(r.Type) != c.typ || r.BoardID != c.board || boardName != c.boardName || r.NodeID != c.node ||
			nodeTitle != c.nodeTitle || (r.BoardName != nil) != (c.board != "") || (r.NodeTitle != nil) != (c.node != "") {
			t.Errorf("the result of %s is %+v, want the type %s, the board %s %q, and the node %s %q", c.id, r, c.typ,
				c.board, c.boardName, c.node, c.nodeTitle)
		}
		if got := marked(r.Title); got != c.title {
			t.Errorf("the title of %s is %q, want %q", c.id, got, c.title)
		}
		if got := marked(r.Snippet); got != c.snippet {
			t.Errorf("the snippet of %s is %q, want %q", c.id, got, c.snippet)
		}
	}

	// A page of one result at a time lists what the one page lists, in its
	// order, and so does a page of two.
	for _, limit := range []int{1, 2} {
		if paged := searchIDs(t, h, w.owner, w.project, "lan", limit); !slices.Equal(paged, got) {
			t.Errorf("pages of %d results list %v, want %v", limit, paged, got)
		}
	}
	wantCode(t, "a search with a cursor of another kind", call(h, http.MethodGet, searchPath(w.project, "lan",
		"cursor", mustCursor(t, searchKey{Rank: 0.5, Type: "board", ID: ids.New().String()})), nil,
		w.owner.header(false)), http.StatusBadRequest, api.CodeInvalidCursor)

	// The filter keeps the results of its kind.
	if got := searchIDs(t, h, w.owner, w.project, "lan", 50, "type", "variable"); !slices.Equal(
		slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values([]string{w.lit, w.wick}))) {
		t.Errorf("the search for lan of variables finds %v, want %s and %s", got, w.lit, w.wick)
	}
	// A text is searched as its author wrote it: the component that it
	// mentions and the variable that it shows are not its words.
	if got := searchIDs(t, h, w.owner, w.project, "elara", 50); !slices.Equal(got, []string{w.elara}) {
		t.Errorf("the search for elara finds %v, want only the component %s", got, w.elara)
	}
	if got := searchIDs(t, h, w.owner, w.project, "ask tonight", 50); !slices.Equal(got, []string{w.ask}) {
		t.Errorf("the search for ask tonight finds %v, want %s", got, w.ask)
	}
	// Every word of the query must start a word of the document.
	if got := searchIDs(t, h, w.owner, w.project, "lantern cellar", 50); !slices.Equal(got, []string{w.old}) {
		t.Errorf("the search for lantern cellar finds %v, want %s", got, w.old)
	}
	if got := searchIDs(t, h, w.owner, w.project, "?!", 50); len(got) != 0 {
		t.Errorf("a query without a word finds %v", got)
	}

	// Later batches change what the documents hold.
	cmd := func(typ string, p map[string]any) wireCommand { return wireCommand{Type: typ, Payload: p} }
	sendBatch(t, h, w.owner, w.project,
		cmd("node.update", map[string]any{"id": w.tide, "text": paragraph("The tide turns.")}),
		cmd("attribute.delete", map[string]any{"id": w.material}),
		cmd("component.rename", map[string]any{"id": w.old, "name": "Old Lamp"}),
		cmd("node.restore", map[string]any{"id": w.gone}),
		cmd("board.restore", map[string]any{"id": w.attic}),
	)
	if got := searchIDs(t, h, w.owner, w.project, "turns", 50); !slices.Equal(got, []string{w.tide}) {
		t.Errorf("after the edit, the search for turns finds %v, want %s", got, w.tide)
	}
	if got := searchIDs(t, h, w.owner, w.project, "brass", 50); len(got) != 0 {
		t.Errorf("after the definition's deletion, the search for its value finds %v", got)
	}
	after := searchPage(t, h, w.owner, searchPath(w.project, "lantern"))
	var found []string
	for _, r := range after.Items {
		found = append(found, r.ID)
	}
	if want := []string{w.lamp, w.gate, w.lit, w.old, w.carry, w.wick, w.gone, w.dusty}; !slices.Equal(
		slices.Sorted(slices.Values(found)), slices.Sorted(slices.Values(want))) {
		t.Errorf("after the batch, the search for lantern finds %v, want %v", found, want)
	}
	if r := resultOf(t, after.Items, w.old); marked(r.Title) != "Old Lamp" ||
		marked(r.Snippet) != "old_[lantern]\nFound in the cellar." {
		t.Errorf("after the rename, the component's result is %q, %q", marked(r.Title), marked(r.Snippet))
	}
	sendBatch(t, h, w.owner, w.project, cmd("attribute.restore", map[string]any{"id": w.material}))
	if drift := searchDrift(t, w.admin, w.project); len(drift) > 0 {
		t.Errorf("the documents are not what the content holds: %q", drift)
	}
	if got := searchIDs(t, h, w.owner, w.project, "brass", 50); !slices.Equal(got, []string{w.old, w.elara}) &&
		!slices.Equal(got, []string{w.elara, w.old}) {
		t.Errorf("after the definition's restoration, the search for its value finds %v, want %s and %s", got,
			w.old, w.elara)
	}

	// A match in a title ranks above one in a body for a query of several
	// words too, however far from it the body's match is, and a snippet marks
	// the body's match of a word whose fellow matched only the title.
	far, near := newNodeID(), newNodeID()
	pos := map[string]int{"x": 0, "y": 0}
	sendBatch(t, h, w.owner, w.project,
		cmd("node.create", map[string]any{"id": far, "board_id": w.harbour, "type": "element", "title": "Lantern",
			"text": paragraph(strings.Repeat("calm ", 120) + "storm"), "position": pos}),
		cmd("node.create", map[string]any{"id": near, "board_id": w.harbour, "type": "element",
			"text": paragraph("A lantern in the storm."), "position": pos}),
	)
	ranked := searchPage(t, h, w.owner, searchPath(w.project, "lantern storm"))
	var order []string
	for _, r := range ranked.Items {
		order = append(order, r.ID)
	}
	if len(order) != 3 || order[2] != near || !slices.Contains(order, far) || !slices.Contains(order, w.lamp) {
		t.Fatalf("the search for lantern storm lists %v, want %s and %s, whose titles match, before %s", order,
			w.lamp, far, near)
	}
	if got := marked(resultOf(t, ranked.Items, w.lamp).Snippet); got != "Light it before the [storm]." {
		t.Errorf("the snippet of %s is %q, want its body with storm marked", w.lamp, got)
	}
	if got := marked(resultOf(t, ranked.Items, far).Snippet); !strings.HasSuffix(got, "calm calm [storm]") {
		t.Errorf("the snippet of %s is %q, want the end of its body, with storm marked", far, got)
	}

	// A title that matches more of the query's words ranks above one that
	// matches fewer, and a match in a title above one in a body, however many
	// of the body's words start with a word of the query, each of which
	// ts_rank adds up, as storm, storms, and stormy.
	squall, gale := newNodeID(), newNodeID()
	sendBatch(t, h, w.owner, w.project,
		cmd("node.create", map[string]any{"id": squall, "board_id": w.harbour, "type": "element",
			"title": "Storm lantern", "position": pos}),
		cmd("node.create", map[string]any{"id": gale, "board_id": w.harbour, "type": "element",
			"text": paragraph("The storm came, the storms rolled in, and the stormy lantern swung."), "position": pos}),
	)
	if got := searchIDs(t, h, w.owner, w.project, "storm", 50); len(got) != 5 || got[0] != squall {
		t.Errorf("the search for storm lists %v, want 5 results, %s, whose title matches, first", got, squall)
	}
	got = searchIDs(t, h, w.owner, w.project, "lantern storm", 50)
	if len(got) != 5 || got[0] != squall || !slices.Contains(got[1:3], w.lamp) || !slices.Contains(got[1:3], far) ||
		!slices.Equal(got[3:], []string{gale, near}) {
		t.Errorf("the search for lantern storm lists %v, want %s, whose title matches both words, then %s and %s, "+
			"whose titles match one, then %s and %s, whose bodies match both, in the order of ts_rank", got, squall,
			w.lamp, far, gale, near)
	}

	// A snippet is taken from the body's first search.MaxSnippetSource
	// characters alone: a body whose match lies past them is found all the
	// same, and its snippet is its first 15 words, without a mark.
	long := newNodeID()
	sendBatch(t, h, w.owner, w.project, cmd("node.create", map[string]any{"id": long, "board_id": w.harbour,
		"type": "element", "text": paragraph(strings.Repeat("calm ", search.MaxSnippetSource/len("calm ")) + "wreck"),
		"position": pos}))
	wrecked := searchPage(t, h, w.owner, searchPath(w.project, "wreck"))
	if len(wrecked.Items) != 1 || wrecked.Items[0].ID != long {
		t.Fatalf("the search for wreck finds %+v, want only %s, whose match lies past its snippet's source",
			wrecked.Items, long)
	}
	if got, want := marked(wrecked.Items[0].Snippet), strings.TrimSpace(strings.Repeat("calm ", 15)); got != want {
		t.Errorf("the snippet of %s is %q, want the first 15 words of its body, without a mark", long, got)
	}
}

// TestSearchDiacritics checks that a search compares Czech text without
// regard to case or diacritics, and marks the words as they are written, and
// that a query that holds a control character is refused at q.
//
// Verifies: FR-PROJ-03/AC4
func TestSearchDiacritics(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Kůň").ID
	board, horse, plain := ids.Encode(ids.Board, ids.New()), newNodeID(), newNodeID()
	pos := map[string]int{"x": 0, "y": 0}
	sendBatch(t, h, owner, p,
		wireCommand{Type: "board.create", Payload: map[string]any{"id": board, "name": "Stáj"}},
		wireCommand{Type: "node.create", Payload: map[string]any{"id": horse, "board_id": board, "type": "element",
			"title": "Ďábelské ódy", "text": paragraph("Příliš žluťoučký kůň úpěl."), "position": pos}},
		wireCommand{Type: "node.create", Payload: map[string]any{"id": plain, "board_id": board, "type": "element",
			"text": paragraph("Kun zluty."), "position": pos}},
	)
	for _, q := range []string{"zlutoucky kun", "ŽLUŤOUČKÝ KŮŇ", "zlut upel", "ďábel", "DABELSKE ody"} {
		page := searchPage(t, h, owner, searchPath(p, q))
		if len(page.Items) != 1 || page.Items[0].ID != horse {
			t.Errorf("the search for %q finds %+v, want only %s", q, page.Items, horse)
		}
	}
	page := searchPage(t, h, owner, searchPath(p, "zlutoucky kun"))
	if len(page.Items) == 1 {
		if got := marked(page.Items[0].Snippet); got != "Příliš [žluťoučký] [kůň] úpěl." {
			t.Errorf("the snippet is %q, want the words marked as they are written", got)
		}
	}
	page = searchPage(t, h, owner, searchPath(p, "dabel"))
	if len(page.Items) == 1 {
		if got := marked(page.Items[0].Title); got != "[Ďábelské] ódy" {
			t.Errorf("the title is %q, want the word marked as it is written", got)
		}
	}
	// Both nodes hold a word that starts with kun, as one written without
	// its diacritics, and with zlut.
	if got := searchIDs(t, h, owner, p, "kůň žlut", 50); len(got) != 2 {
		t.Errorf("the search for kůň žlut finds %v, want both nodes", got)
	}
	wantProblem(t, "a query with a control character", call(h, http.MethodGet, searchPath(p, "kůň\x07"), nil,
		owner.header(false)), "query.q")
}

// TestSearchQueryLimit checks that the limit that the OpenAPI document gives
// a search's query, and that its description names, is the search package's,
// which the service checks too (search.CheckQuery).
func TestSearchQueryLimit(t *testing.T) {
	s := newServer(loadConfig(t, nil), slog.New(slog.DiscardHandler))
	item := s.main.OpenAPI().Paths["/api/v1/projects/{project_id}/search"]
	if item == nil || item.Get == nil {
		t.Fatal("the document has no search")
	}
	for _, p := range item.Get.Parameters {
		if p.In != "query" || p.Name != "q" {
			continue
		}
		if p.Schema == nil || p.Schema.MaxLength == nil || *p.Schema.MaxLength != search.MaxQueryLength {
			t.Errorf("the document's maxLength of q is not %d", search.MaxQueryLength)
		}
		if want := fmt.Sprintf("of 1 to %d characters", search.MaxQueryLength); !strings.Contains(p.Description,
			want) {
			t.Errorf("the description of q, %q, does not say %q", p.Description, want)
		}
		return
	}
	t.Error("the search has no parameter q")
}

// TestSearchGuest checks that a guest without access to a project receives
// no result from it, and receives its results once granted a role on it.
//
// Verifies: FR-PROJ-03/AC3
func TestSearchGuest(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	harbour := createProject(t, h, owner, ws, "Harbour").ID
	board, lamp := ids.Encode(ids.Board, ids.New()), newNodeID()
	sendBatch(t, h, owner, harbour,
		wireCommand{Type: "board.create", Payload: map[string]any{"id": board, "name": "Quay"}},
		wireCommand{Type: "node.create", Payload: map[string]any{"id": lamp, "board_id": board, "type": "element",
			"title": "The Lantern", "position": map[string]int{"x": 0, "y": 0}}},
	)
	guest := newNamedAccount(t, h, "gu@example.com", "Gu")
	join(t, d, guest, ws, "guest")
	res := call(h, http.MethodGet, searchPath(harbour, "lantern"), nil, guest.header(false))
	wantCode(t, "a guest without a grant searches the project", res, http.StatusNotFound, api.CodeNotFound)
	if strings.Contains(string(res.body), lamp) || strings.Contains(string(res.body), "Lantern") {
		t.Errorf("the refusal tells the guest of the project's content: %s", res.body)
	}
	setGrant(t, h, owner, harbour, membershipOf(t, membersOf(t, h, owner, ws), guest), "viewer")
	if got := searchIDs(t, h, guest, harbour, "lantern", 50); !slices.Equal(got, []string{lamp}) {
		t.Errorf("a guest granted the project finds %v, want %s", got, lamp)
	}
}
