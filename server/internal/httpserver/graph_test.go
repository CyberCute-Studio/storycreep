package httpserver

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

func dependenciesPath(project string) string { return projectPath(project) + "/dependencies" }

func contributionsPath(project, from, to, kind string) string {
	return dependenciesPath(project) + "/" + from + "/" + to + "?kind=" + kind
}

// newBoardIDs returns n new IDs of boards, in the order of their UUIDs, which
// a graph lists its boards and its edges in: IDs made within one millisecond
// are in no order of their own.
func newBoardIDs(n int) []string {
	u := make([]ids.UUID, n)
	for i := range u {
		u[i] = ids.New()
	}
	slices.SortFunc(u, func(a, b ids.UUID) int { return bytes.Compare(a[:], b[:]) })
	out := make([]string, n)
	for i := range u {
		out[i] = ids.Encode(ids.Board, u[i])
	}
	return out
}

// graphWorld is a project whose content makes a graph of the dependencies
// between its boards, built through the command endpoint, as the graph's
// tests read it:
//
//   - the global variables coins and alarm, and the components elara and
//     mira of the type Cast, with the runtime attributes trust and mood;
//   - on Market, the element Stall, whose on-enter script is coins += 1 and
//     whose on-exit script is elara.trust = 2, the element Till, the
//     connection pay from Stall to Till, whose condition is coins > 0 and
//     whose on-traverse script is coins = 5, the jumper Leave, whose target
//     is Quay, and the jumper Loop, whose target is Stall;
//   - on Docks, the element Quay, whose text shows coins and whose on-enter
//     script is alarm = visits(node(Stall)) > 0, the element Hut, the
//     connection walk from Quay to Hut, whose condition is coins >= 3 and
//     whose label shows elara.mood, and the branch Gate, whose output open's
//     condition is elara.trust >= 1 and mira.trust >= 1;
//   - and on Fort, the element Tower, whose on-enter script is coins -= 1,
//     the element Wall, and the connection watch from Tower to Wall, whose
//     condition is alarm == true.
//
// So coins is written twice on Market and once on Fort, and read twice on
// Market, twice on Docks, and once on Fort; alarm is written on Docks and
// read on Fort; elara.trust is written on Market and read on Docks, where
// mira.trust and elara.mood are read, which nothing writes; Leave jumps from
// Market to Docks, and Loop within Market; and Docks reads whether Stall, of
// Market, was visited.
type graphWorld struct {
	s                                         *server
	admin                                     *pgx.Conn
	owner                                     signedIn
	project                                   string
	market, docks, fort                       string
	coins, alarm, cast, trust, mood           string
	elara, mira                               string
	stall, till, pay, leave, loop             string
	quay, hut, walk, gate, open, tower, watch string
	// names are the names of the entities by their IDs, as the tests show
	// them: a board's, a node's, which is its title, and the others'.
	names map[string]string
	// expressions are the UUIDs of the expressions, by their fields and
	// their owners' IDs, as Stall's on-enter script's is by on_enter and
	// Stall's ID, which the contributions' keys order their sites by
	// (graphWorld.key).
	expressions map[string]ids.UUID
}

// newGraphWorld returns a new graphWorld, made in one batch, of a server of
// its own.
func newGraphWorld(t *testing.T) *graphWorld {
	t.Helper()
	s, d, owner, ws := inviteServer(t, "open", nil)
	w := &graphWorld{s: s, admin: d.Admin(t), owner: owner, project: createProject(t, s.main, owner, ws, "Port").ID,
		names: map[string]string{}}
	name := func(id *string, kind ids.Kind, name string) {
		*id = ids.Encode(kind, ids.New())
		w.names[*id] = name
	}
	boards := newBoardIDs(3)
	w.market, w.docks, w.fort = boards[0], boards[1], boards[2]
	w.names[w.market], w.names[w.docks], w.names[w.fort] = "Market", "Docks", "Fort"
	name(&w.coins, ids.Variable, "coins")
	name(&w.alarm, ids.Variable, "alarm")
	name(&w.cast, ids.ComponentType, "Cast")
	name(&w.trust, ids.AttributeDef, "trust")
	name(&w.mood, ids.AttributeDef, "mood")
	name(&w.elara, ids.Component, "elara")
	name(&w.mira, ids.Component, "mira")
	for _, n := range []struct {
		id   *string
		kind ids.Kind
		name string
	}{
		{&w.stall, ids.Node, "Stall"}, {&w.till, ids.Node, "Till"}, {&w.pay, ids.Connection, "pay"},
		{&w.leave, ids.Node, "Leave"}, {&w.loop, ids.Node, "Loop"}, {&w.quay, ids.Node, "Quay"},
		{&w.hut, ids.Node, "Hut"}, {&w.walk, ids.Connection, "walk"}, {&w.gate, ids.Node, "Gate"},
		{&w.open, ids.BranchOutput, "open"}, {&w.tower, ids.Node, "Tower"}, {&w.watch, ids.Connection, "watch"},
	} {
		name(n.id, n.kind, n.name)
	}
	wall := newNodeID()
	pos := map[string]int{"x": 0, "y": 0}
	cmd := func(typ string, p map[string]any) wireCommand { return wireCommand{Type: typ, Payload: p} }
	node := func(id, board, typ string, more ...any) wireCommand {
		p := map[string]any{"id": id, "board_id": board, "type": typ, "title": w.names[id], "position": pos}
		for i := 0; i < len(more); i += 2 {
			p[more[i].(string)] = more[i+1]
		}
		return cmd("node.create", p)
	}
	sendBatch(t, s.main, owner, w.project,
		cmd("board.create", map[string]any{"id": w.market, "name": "Market"}),
		cmd("board.create", map[string]any{"id": w.docks, "name": "Docks"}),
		cmd("board.create", map[string]any{"id": w.fort, "name": "Fort"}),
		cmd("variable.create", map[string]any{"id": w.coins, "key": "coins", "type": "int", "scope": "global"}),
		cmd("variable.create", map[string]any{"id": w.alarm, "key": "alarm", "type": "bool", "scope": "global"}),
		cmd("component_type.create", map[string]any{"id": w.cast, "name": "Cast"}),
		cmd("attribute.create", map[string]any{"id": w.trust, "component_type_id": w.cast, "name": "Trust",
			"type": "int", "runtime": true}),
		cmd("attribute.create", map[string]any{"id": w.mood, "component_type_id": w.cast, "name": "Mood",
			"type": "string", "runtime": true}),
		cmd("component.create", map[string]any{"id": w.elara, "component_type_id": w.cast, "name": "Elara"}),
		cmd("component.create", map[string]any{"id": w.mira, "component_type_id": w.cast, "name": "Mira"}),
		// Market.
		node(w.stall, w.market, "element", "on_enter", "coins += 1", "on_exit", "elara.trust = 2"),
		node(w.till, w.market, "element"),
		cmd("connection.create", map[string]any{"id": w.pay, "from_node_id": w.stall, "to_node_id": w.till,
			"condition": "coins > 0", "on_traverse": "coins = 5"}),
		// Docks, whose Quay Leave targets.
		node(w.quay, w.docks, "element", "text", richText(textInline("Coins: "), valueInline(w.coins)),
			"on_enter", `alarm = visits(node("`+w.stall+`")) > 0`),
		node(w.hut, w.docks, "element"),
		cmd("connection.create", map[string]any{"id": w.walk, "from_node_id": w.quay, "to_node_id": w.hut,
			"condition": "coins >= 3", "label": richText(textInline("Mood "), attributeInline(w.elara, w.mood))}),
		node(w.gate, w.docks, "branch"),
		cmd("branch_output.create", map[string]any{"id": w.open, "node_id": w.gate,
			"condition": "elara.trust >= 1 and mira.trust >= 1"}),
		node(w.leave, w.market, "jumper", "target", map[string]any{"board_id": w.docks, "node_id": w.quay}),
		node(w.loop, w.market, "jumper", "target", map[string]any{"board_id": w.market, "node_id": w.stall}),
		// Fort.
		node(w.tower, w.fort, "element", "on_enter", "coins -= 1"),
		node(wall, w.fort, "element"),
		cmd("connection.create", map[string]any{"id": w.watch, "from_node_id": w.tower, "to_node_id": wall,
			"condition": "alarm == true"}),
	)
	w.expressions = expressionIDs(t, w.admin, w.project)
	if len(w.expressions) != 9 {
		t.Fatalf("the world has %d expressions, want 9: %v", len(w.expressions), w.expressions)
	}
	return w
}

// expressionIDs returns the UUIDs of the expressions of the project, by their
// fields and their owners' public IDs, as "on_enter nd_…", each of which
// compiles.
func expressionIDs(t *testing.T, admin *pgx.Conn, project string) map[string]ids.UUID {
	t.Helper()
	rows, err := admin.Query(context.Background(), `SELECT e.id::text, e.field, e.is_valid,
			CASE WHEN e.node_id IS NOT NULL THEN 'nd' WHEN e.connection_id IS NOT NULL THEN 'cn' ELSE 'bo' END,
			coalesce(e.node_id, e.connection_id, e.branch_output_id)::text
		FROM expression e WHERE e.project_id = $1`, uuidOf(t, project))
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		id, field, kind, owner string
		valid                  bool
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.id, &x.field, &x.valid, &x.kind, &x.owner)
		return x, err
	})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]ids.UUID{}
	for _, x := range got {
		kind := map[string]ids.Kind{"nd": ids.Node, "cn": ids.Connection, "bo": ids.BranchOutput}[x.kind]
		id, owner := mustParseUUID(t, x.id), mustParseUUID(t, x.owner)
		if !x.valid {
			t.Fatalf("the expression %s of %s does not compile", x.field, ids.Encode(kind, owner))
		}
		out[x.field+" "+ids.Encode(kind, owner)] = id
	}
	return out
}

// mustParseUUID returns the UUID of its text form s.
func mustParseUUID(t *testing.T, s string) ids.UUID {
	t.Helper()
	u, err := ids.ParseUUID(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// graphOf returns the graph of the project, as si reads it with the query,
// which may be empty, and fails the test unless it is 200.
func graphOf(t *testing.T, h http.Handler, si signedIn, project, query string) dependency.Graph {
	t.Helper()
	path := dependenciesPath(project)
	if query != "" {
		path += "?" + query
	}
	res := call(h, http.MethodGet, path, nil, si.header(false))
	var g dependency.Graph
	if res.status != http.StatusOK || json.Unmarshal(res.body, &g) != nil {
		t.Fatalf("GET %s: %d %s", path, res.status, res.body)
	}
	return g
}

// contributions returns every contribution of the list at path, page by
// page, of limit items each, as si, and fails the test unless each page is
// 200.
func contributions(t *testing.T, h http.Handler, si signedIn, path string, limit int) []dependency.Contribution {
	t.Helper()
	var out []dependency.Contribution
	for cursor := ""; ; {
		url := fmt.Sprintf("%s&limit=%d", path, limit)
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		res := call(h, http.MethodGet, url, nil, si.header(false))
		var page api.Page[dependency.Contribution]
		if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
			t.Fatalf("GET %s: %d %s", url, res.status, res.body)
		}
		out = append(out, page.Items...)
		if page.NextCursor == nil {
			return out
		}
		cursor = *page.NextCursor
	}
}

// edges returns the graph's edges as lines of the names of their boards,
// their kinds, and their counts, in their order.
func edges(names map[string]string, g dependency.Graph) []string {
	out := []string{}
	for _, e := range g.Edges {
		out = append(out, fmt.Sprintf("%s->%s %s %d", names[e.FromBoardID], names[e.ToBoardID], e.Kind,
			e.Contributions))
	}
	return out
}

// badges returns the graph's boards as lines of their names and the counts of
// their badges, in their order.
func badges(t *testing.T, names map[string]string, g dependency.Graph) []string {
	t.Helper()
	out := []string{}
	for _, b := range g.Boards {
		if b.Name != names[b.ID] {
			t.Errorf("the board %s is named %q, want %q", b.ID, b.Name, names[b.ID])
		}
		out = append(out, fmt.Sprintf("%s in %d out %d", names[b.ID], b.Inbound, b.Outbound))
	}
	return out
}

// wantGraph fails the test unless the graph's edges and boards are those
// want, in their order, and the edges are in the order of the IDs of their
// boards and of their kinds.
func wantGraph(t *testing.T, what string, names map[string]string, g dependency.Graph, wantEdges, wantBoards []string) {
	t.Helper()
	if got := edges(names, g); !slices.Equal(got, wantEdges) {
		t.Errorf("%s: the edges are\n%q\nwant\n%q", what, got, wantEdges)
	}
	if got := badges(t, names, g); !slices.Equal(got, wantBoards) {
		t.Errorf("%s: the boards are\n%q\nwant\n%q", what, got, wantBoards)
	}
	kinds := []dependency.EdgeKind{dependency.EdgeVariable, dependency.EdgeAttribute, dependency.EdgeJumper,
		dependency.EdgeVisit}
	if !slices.IsSortedFunc(g.Edges, func(a, b dependency.Edge) int {
		af, bf, at, bt := uuidOf(t, a.FromBoardID), uuidOf(t, b.FromBoardID), uuidOf(t, a.ToBoardID),
			uuidOf(t, b.ToBoardID)
		return cmp.Or(bytes.Compare(af[:], bf[:]), bytes.Compare(at[:], bt[:]),
			cmp.Compare(slices.Index(kinds, a.Kind), slices.Index(kinds, b.Kind)))
	}) {
		t.Errorf("%s: the edges are not in the order of their boards' IDs and their kinds: %q", what, edges(names, g))
	}
}

// line returns the contribution c as a line of what it names and its ends,
// each end its site, its node's name, and its connection's or its output's,
// if it has one, and fails the test unless each end's node's title is its
// name.
func (w *graphWorld) line(t *testing.T, c dependency.Contribution) string {
	t.Helper()
	end := func(e dependency.End) string {
		if e.NodeTitle != w.names[e.NodeID] {
			t.Errorf("the node %s is titled %q, want %q", e.NodeID, e.NodeTitle, w.names[e.NodeID])
		}
		s := e.Site + " " + w.names[e.NodeID]
		for _, id := range []string{e.ConnectionID, e.BranchOutputID} {
			if id != "" {
				s += "/" + w.names[id]
			}
		}
		return s
	}
	what := w.names[c.VariableID]
	if c.ComponentID != "" {
		what = w.names[c.ComponentID] + "." + w.names[c.AttributeID]
	}
	return strings.TrimSpace(what + " " + end(c.From) + " -> " + end(c.To))
}

// key returns where the contribution c is in its list
// (dependency.ContributionKey), from what the API shows of it and the UUIDs
// of the world's expressions: its target, its attribute, and the node and
// the site of each end, a site being an expression's UUID, that of a text's
// owner, or the node itself.
func (w *graphWorld) key(t *testing.T, c dependency.Contribution) dependency.ContributionKey {
	t.Helper()
	id := func(public string) ids.UUID {
		if public == "" {
			return ids.UUID{}
		}
		return uuidOf(t, public)
	}
	site := func(e dependency.End) ids.UUID {
		switch e.Site {
		case dependency.SiteNode, dependency.SiteNodeText:
			return id(e.NodeID)
		case dependency.SiteConnectionLabel:
			return id(e.ConnectionID)
		}
		owner := cmp.Or(e.BranchOutputID, e.ConnectionID, e.NodeID)
		field := e.Site[strings.LastIndex(e.Site, "_on_")+1:]
		if !strings.Contains(e.Site, "_on_") {
			field = "condition"
		}
		u, ok := w.expressions[field+" "+owner]
		if !ok {
			t.Fatalf("no expression %s of %s", field, owner)
		}
		return u
	}
	k := dependency.ContributionKey{Target: id(cmp.Or(c.VariableID, c.ComponentID)), Attribute: id(c.AttributeID),
		FromNode: id(c.From.NodeID), FromSite: site(c.From), ToNode: id(c.To.NodeID), ToSite: site(c.To)}
	if c.From.Site == dependency.SiteNode && c.To.Site != dependency.SiteNode {
		// A visit's target is the node visited.
		k.Target = k.FromNode
	}
	return k
}

// wantContributions fails the test unless the contributions are those want,
// in any order, and in the order of their keys, each after the one before it.
func (w *graphWorld) wantContributions(t *testing.T, what string, got []dependency.Contribution, want ...string) {
	t.Helper()
	have := make([]string, len(got))
	for i, c := range got {
		have[i] = w.line(t, c)
		if i > 0 && compareContributionKeys(w.key(t, got[i-1]), w.key(t, c)) >= 0 {
			t.Errorf("%s are not in the order of their keys: %q", what, have[:i+1])
		}
	}
	slices.Sort(have)
	slices.Sort(want)
	if !slices.Equal(have, want) {
		t.Errorf("%s are\n%q\nwant\n%q", what, have, want)
	}
}

// compareContributionKeys orders the keys of contributions as their lists
// do: by their targets, their attributes, and the nodes and the sites of
// their ends.
func compareContributionKeys(a, b dependency.ContributionKey) int {
	return cmp.Or(bytes.Compare(a.Target[:], b.Target[:]), bytes.Compare(a.Attribute[:], b.Attribute[:]),
		bytes.Compare(a.FromNode[:], b.FromNode[:]), bytes.Compare(a.FromSite[:], b.FromSite[:]),
		bytes.Compare(a.ToNode[:], b.ToNode[:]), bytes.Compare(a.ToSite[:], b.ToSite[:]))
}

// TestDependencyGraph checks the graph of a project of three boards, each
// of whose kinds of edge it has: an edge from each board that writes a
// variable, or a component's attribute, to each other board that reads it,
// counting each pair of a site that writes it and a site that reads it; an
// edge of each jumper's board to its target's; and an edge from a node's
// board to that of an expression that reads whether it was visited. Nothing
// within a board is an edge, as coins, written and read on Market, and the
// jumper Loop, from Market to Market, are not. Each board's badge counts the
// other boards that its edges leave and enter, whatever their kinds.
func TestDependencyGraph(t *testing.T) {
	t.Parallel()
	w := newGraphWorld(t)
	g := graphOf(t, w.s.main, w.owner, w.project, "")
	wantGraph(t, "the graph", w.names, g, []string{
		"Market->Docks variable 4", "Market->Docks attribute 1", "Market->Docks jumper 1", "Market->Docks visit 1",
		"Market->Fort variable 2", "Docks->Fort variable 1", "Fort->Market variable 2", "Fort->Docks variable 2",
	}, []string{"Market in 1 out 2", "Docks in 2 out 1", "Fort in 2 out 2"})
	var seq int64
	if err := w.admin.QueryRow(context.Background(), `SELECT seq FROM project WHERE id = $1`,
		uuidOf(t, w.project)).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if g.Seq != seq || seq == 0 {
		t.Errorf("the graph's seq is %d, want the project's, %d", g.Seq, seq)
	}
}

// TestDependencyContributions checks that each edge lists its contributions:
// every triple of a variable or an attribute, a site that writes it on the
// board that the edge leaves, and a site that reads it on the board that it
// enters, as coins, written twice on Market and read twice on Docks, makes
// four; every jumper, with its target; and every pair of a node visited and
// an expression that reads it. Each is listed once, in the order of their
// keys, page by page as in one page, and as many as the edge counts.
//
// Verifies: FR-DEP-02/AC2
func TestDependencyContributions(t *testing.T) {
	t.Parallel()
	w := newGraphWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	coins := contributions(t, h, o, contributionsPath(p, w.market, w.docks, "variable"), 50)
	w.wantContributions(t, "Market->Docks's variables", coins,
		"coins node_on_enter Stall -> node_text Quay", "coins node_on_enter Stall -> connection_condition Quay/walk",
		"coins connection_on_traverse Stall/pay -> node_text Quay",
		"coins connection_on_traverse Stall/pay -> connection_condition Quay/walk")
	paged := contributions(t, h, o, contributionsPath(p, w.market, w.docks, "variable"), 1)
	if !slices.EqualFunc(paged, coins, func(a, b dependency.Contribution) bool { return w.line(t, a) == w.line(t, b) }) {
		t.Errorf("a page at a time, Market->Docks's variables are %v, want %v", paged, coins)
	}
	w.wantContributions(t, "Market->Docks's attributes", contributions(t, h, o,
		contributionsPath(p, w.market, w.docks, "attribute"), 50),
		"elara.trust node_on_exit Stall -> branch_output_condition Gate/open")
	w.wantContributions(t, "Market->Docks's jumpers", contributions(t, h, o,
		contributionsPath(p, w.market, w.docks, "jumper"), 50), "node Leave -> node Quay")
	w.wantContributions(t, "Market->Docks's visits", contributions(t, h, o,
		contributionsPath(p, w.market, w.docks, "visit"), 50), "node Stall -> node_on_enter Quay")
	w.wantContributions(t, "Fort->Market's variables", contributions(t, h, o,
		contributionsPath(p, w.fort, w.market, "variable"), 50),
		"coins node_on_enter Tower -> node_on_enter Stall", "coins node_on_enter Tower -> connection_condition Stall/pay")
	w.wantContributions(t, "Docks->Fort's variables", contributions(t, h, o,
		contributionsPath(p, w.docks, w.fort, "variable"), 50), "alarm node_on_enter Quay -> connection_condition Tower/watch")

	// Every edge lists as many contributions as the graph counts, and every
	// other list is empty: within a board, as coins on Market, and Loop,
	// whose target is on its own board; against an edge, as nothing of Docks
	// leads to Market; and of a kind that an edge does not have.
	g := graphOf(t, h, o, p, "")
	counts := map[string]int64{}
	for _, e := range g.Edges {
		counts[e.FromBoardID+e.ToBoardID+string(e.Kind)] = e.Contributions
	}
	for _, from := range []string{w.market, w.docks, w.fort} {
		for _, to := range []string{w.market, w.docks, w.fort} {
			for _, kind := range []string{"variable", "attribute", "jumper", "visit"} {
				got := contributions(t, h, o, contributionsPath(p, from, to, kind), 200)
				if want := counts[from+to+kind]; int64(len(got)) != want {
					t.Errorf("%s->%s lists %d contributions of the kind %s, want %d", w.names[from], w.names[to],
						len(got), kind, want)
				}
			}
		}
	}
}

// TestDependencyGraphVisit checks FR-DEP-02 AC5's fixture: a choice on
// Harbourmaster is gated on whether the entry node of Elara — First Meeting
// was visited, and nothing else connects the two boards. The graph has an
// edge of the kind visit from Elara to Harbourmaster, and no other, which
// both boards' badges count, and the edge lists the pair of the node and the
// condition.
//
// Verifies: FR-DEP-02/AC5
func TestDependencyGraphVisit(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour").ID
	boards := newBoardIDs(2)
	elara, harbourmaster := boards[0], boards[1]
	greeting, desk, office, ask := newNodeID(), newNodeID(), newNodeID(), newConnectionID()
	names := map[string]string{elara: "Elara — First Meeting", harbourmaster: "Harbourmaster"}
	pos := map[string]int{"x": 0, "y": 0}
	element := func(id, board, title string) wireCommand {
		return wireCommand{Type: "node.create", Payload: map[string]any{"id": id, "board_id": board,
			"type": "element", "title": title, "position": pos}}
	}
	sendBatch(t, s.main, owner, p,
		wireCommand{Type: "board.create", Payload: map[string]any{"id": elara, "name": names[elara]}},
		wireCommand{Type: "board.create", Payload: map[string]any{"id": harbourmaster, "name": names[harbourmaster]}},
		element(greeting, elara, "Greeting"),
		wireCommand{Type: "board.set_entry_node", Payload: map[string]any{"id": elara, "node_id": greeting}},
		element(desk, harbourmaster, "Desk"), element(office, harbourmaster, "Office"),
		wireCommand{Type: "connection.create", Payload: map[string]any{"id": ask, "from_node_id": desk,
			"to_node_id": office, "label": paragraph("Ask about Elara"),
			"condition": `visited(node("` + greeting + `"))`}},
	)
	for _, query := range []string{"", "kind=visit", "board_id=" + harbourmaster} {
		wantGraph(t, "the graph of "+query, names, graphOf(t, s.main, owner, p, query),
			[]string{"Elara — First Meeting->Harbourmaster visit 1"},
			[]string{"Elara — First Meeting in 0 out 1", "Harbourmaster in 1 out 0"})
	}
	got := contributions(t, s.main, owner, contributionsPath(p, elara, harbourmaster, "visit"), 50)
	if len(got) != 1 || got[0].From != (dependency.End{Site: dependency.SiteNode, NodeID: greeting,
		NodeTitle: "Greeting"}) || got[0].To != (dependency.End{Site: dependency.SiteConnectionCondition,
		NodeID: desk, NodeTitle: "Desk", ConnectionID: ask}) || got[0].VariableID != "" || got[0].ComponentID != "" {
		t.Errorf("the edge's contributions are %+v, want the greeting and the choice's condition", got)
	}
}

// TestDependencyGraphByPair checks FR-DEP-01 AC4's fixture: elara.trust is
// written on board A and mira.trust is read on board B, and elara.mood is
// written on A too. The graph has no edge from A to B, as a component's
// attribute is the pair of the component and its definition, until B reads
// elara.trust.
//
// Verifies: FR-DEP-01/AC4
func TestDependencyGraphByPair(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour").ID
	boards := newBoardIDs(2)
	a, b := boards[0], boards[1]
	cast, trust, mood, elara, mira := newComponentTypeID(), newAttributeID(), newAttributeID(), newComponentID(),
		newComponentID()
	writer, reader, after, gate := newNodeID(), newNodeID(), newNodeID(), newConnectionID()
	names := map[string]string{a: "A", b: "B"}
	pos := map[string]int{"x": 0, "y": 0}
	cmd := func(typ string, p map[string]any) wireCommand { return wireCommand{Type: typ, Payload: p} }
	sendBatch(t, s.main, owner, p,
		cmd("board.create", map[string]any{"id": a, "name": "A"}),
		cmd("board.create", map[string]any{"id": b, "name": "B"}),
		cmd("component_type.create", map[string]any{"id": cast, "name": "Cast"}),
		cmd("attribute.create", map[string]any{"id": trust, "component_type_id": cast, "name": "Trust",
			"type": "int", "runtime": true}),
		cmd("attribute.create", map[string]any{"id": mood, "component_type_id": cast, "name": "Mood",
			"type": "string", "runtime": true}),
		cmd("component.create", map[string]any{"id": elara, "component_type_id": cast, "name": "Elara"}),
		cmd("component.create", map[string]any{"id": mira, "component_type_id": cast, "name": "Mira"}),
		cmd("node.create", map[string]any{"id": writer, "board_id": a, "type": "element", "position": pos,
			"on_enter": "elara.trust = 1", "on_exit": `elara.mood = "calm"`}),
		cmd("node.create", map[string]any{"id": reader, "board_id": b, "type": "element", "position": pos}),
		cmd("node.create", map[string]any{"id": after, "board_id": b, "type": "element", "position": pos}),
		cmd("connection.create", map[string]any{"id": gate, "from_node_id": reader, "to_node_id": after,
			"condition": "mira.trust > 0"}),
	)
	wantGraph(t, "the graph", names, graphOf(t, s.main, owner, p, ""), []string{}, []string{"A in 0 out 0",
		"B in 0 out 0"})
	if got := contributions(t, s.main, owner, contributionsPath(p, a, b, "attribute"), 50); len(got) != 0 {
		t.Errorf("A->B lists the attributes' contributions %+v", got)
	}
	// Once B reads elara.trust, A->B has an edge of it alone, not of
	// elara.mood.
	sendBatch(t, s.main, owner, p, cmd("connection.update", map[string]any{"id": gate,
		"condition": "elara.trust > 0 and mira.trust > 0"}))
	wantGraph(t, "the graph once B reads elara.trust", names, graphOf(t, s.main, owner, p, ""),
		[]string{"A->B attribute 1"}, []string{"A in 0 out 1", "B in 1 out 0"})
	got := contributions(t, s.main, owner, contributionsPath(p, a, b, "attribute"), 50)
	if len(got) != 1 || got[0].ComponentID != elara || got[0].AttributeID != trust {
		t.Errorf("A->B lists the attributes' contributions %+v, want elara.trust's alone", got)
	}
}

// TestDependencyGraphFilters checks the graph's filters, each of which keeps
// the contributions that it names, and so the edges that have any, and the
// badges that count them, with every board listed: by kind, by board, by
// variable, by component, and by an attribute's definition, of any
// component; and that filters that name nothing in common, or an ID that the
// project does not have, keep nothing. An edge's list keeps what the same
// filters keep.
func TestDependencyGraphFilters(t *testing.T) {
	t.Parallel()
	w := newGraphWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	none := []string{"Market in 0 out 0", "Docks in 0 out 0", "Fort in 0 out 0"}
	for _, tt := range []struct {
		query         string
		edges, boards []string
	}{
		{"kind=visit", []string{"Market->Docks visit 1"},
			[]string{"Market in 0 out 1", "Docks in 1 out 0", "Fort in 0 out 0"}},
		{"kind=jumper", []string{"Market->Docks jumper 1"},
			[]string{"Market in 0 out 1", "Docks in 1 out 0", "Fort in 0 out 0"}},
		{"kind=attribute", []string{"Market->Docks attribute 1"},
			[]string{"Market in 0 out 1", "Docks in 1 out 0", "Fort in 0 out 0"}},
		{"board_id=" + w.fort, []string{"Market->Fort variable 2", "Docks->Fort variable 1", "Fort->Market variable 2",
			"Fort->Docks variable 2"}, []string{"Market in 1 out 1", "Docks in 1 out 1", "Fort in 2 out 2"}},
		{"variable_id=" + w.alarm, []string{"Docks->Fort variable 1"},
			[]string{"Market in 0 out 0", "Docks in 0 out 1", "Fort in 1 out 0"}},
		{"variable_id=" + w.coins + "&board_id=" + w.docks, []string{"Market->Docks variable 4",
			"Fort->Docks variable 2"}, []string{"Market in 0 out 1", "Docks in 2 out 0", "Fort in 0 out 1"}},
		{"component_id=" + w.elara, []string{"Market->Docks attribute 1"},
			[]string{"Market in 0 out 1", "Docks in 1 out 0", "Fort in 0 out 0"}},
		{"attribute_id=" + w.trust, []string{"Market->Docks attribute 1"},
			[]string{"Market in 0 out 1", "Docks in 1 out 0", "Fort in 0 out 0"}},
		{"component_id=" + w.mira, []string{}, none},
		{"attribute_id=" + w.mood, []string{}, none},
		{"variable_id=" + w.coins + "&component_id=" + w.elara, []string{}, none},
		{"kind=jumper&variable_id=" + w.coins, []string{}, none},
		{"variable_id=" + newVariableID(), []string{}, none},
		{"board_id=" + newBoardID(), []string{}, none},
	} {
		t.Run(tt.query, func(t *testing.T) {
			wantGraph(t, tt.query, w.names, graphOf(t, h, o, p, tt.query), tt.edges, tt.boards)
		})
	}
	for _, tt := range []struct {
		kind, query string
		want        []string
	}{
		{"variable", "&variable_id=" + w.alarm, nil},
		{"attribute", "&component_id=" + w.mira, nil},
		{"attribute", "&attribute_id=" + w.trust, []string{"elara.trust node_on_exit Stall -> branch_output_condition Gate/open"}},
		{"attribute", "&component_id=" + w.elara + "&attribute_id=" + w.mood, nil},
		{"jumper", "&variable_id=" + w.coins, nil},
		{"visit", "&attribute_id=" + w.trust, nil},
	} {
		w.wantContributions(t, "Market->Docks's "+tt.kind+tt.query, contributions(t, h, o,
			contributionsPath(p, w.market, w.docks, tt.kind)+tt.query, 50), tt.want...)
	}
}

// TestDependencyGraphTrash checks that the graph is of the content that is
// not in the project's trash: a node in the trash takes its sites with it,
// those of its connections among them, and its jumpers' edges, until it is
// restored, and a board in the trash is no node of the graph, and none of
// its edges is, and its lists are answered with 404.
func TestDependencyGraphTrash(t *testing.T) {
	t.Parallel()
	w := newGraphWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	full := graphOf(t, h, o, p, "")
	apply := func(typ string, payload map[string]any) {
		t.Helper()
		sendBatch(t, h, o, p, wireCommand{Type: typ, Payload: payload})
	}
	// Quay's text, its script, and walk's condition go to the trash with
	// it, and Leave's target is in the trash.
	apply("node.delete", map[string]any{"id": w.quay})
	wantGraph(t, "the graph with Quay in the trash", w.names, graphOf(t, h, o, p, ""), []string{
		"Market->Docks attribute 1", "Market->Fort variable 2", "Fort->Market variable 2",
	}, []string{"Market in 1 out 2", "Docks in 1 out 0", "Fort in 1 out 1"})
	if got := contributions(t, h, o, contributionsPath(p, w.market, w.docks, "jumper"), 50); len(got) != 0 {
		t.Errorf("with its target in the trash, Market->Docks lists the jumpers %+v", got)
	}
	apply("node.restore", map[string]any{"id": w.quay})
	if got := graphOf(t, h, o, p, ""); !slices.Equal(edges(w.names, got), edges(w.names, full)) {
		t.Errorf("once Quay is restored, the edges are %q, want %q", edges(w.names, got), edges(w.names, full))
	}
	// pay's condition and script go to the trash with it.
	apply("connection.delete", map[string]any{"id": w.pay})
	wantGraph(t, "the graph with pay in the trash", w.names, graphOf(t, h, o, p, ""), []string{
		"Market->Docks variable 2", "Market->Docks attribute 1", "Market->Docks jumper 1", "Market->Docks visit 1",
		"Market->Fort variable 1", "Docks->Fort variable 1", "Fort->Market variable 1", "Fort->Docks variable 2",
	}, []string{"Market in 1 out 2", "Docks in 2 out 1", "Fort in 2 out 2"})
	apply("connection.restore", map[string]any{"id": w.pay})
	apply("board.delete", map[string]any{"id": w.fort})
	wantGraph(t, "the graph with Fort in the trash", w.names, graphOf(t, h, o, p, ""), []string{
		"Market->Docks variable 4", "Market->Docks attribute 1", "Market->Docks jumper 1", "Market->Docks visit 1",
	}, []string{"Market in 0 out 1", "Docks in 1 out 0"})
	for _, path := range []string{contributionsPath(p, w.fort, w.market, "variable"),
		contributionsPath(p, w.market, w.fort, "variable")} {
		wantCode(t, "a board in the trash", call(h, http.MethodGet, path, nil, o.header(false)), http.StatusNotFound,
			api.CodeNotFound)
	}
}

// TestDependencyGraphErrors checks the answers to the graph's requests that
// the API refuses.
func TestDependencyGraphErrors(t *testing.T) {
	t.Parallel()
	w := newGraphWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	list := contributionsPath(p, w.market, w.docks, "variable")
	for _, tt := range []struct {
		name, path string
		status     int
		code       api.Code
	}{
		{"another kind", dependenciesPath(p) + "?kind=mention", http.StatusUnprocessableEntity,
			api.CodeValidationFailed},
		{"a variable's ID for a board", dependenciesPath(p) + "?board_id=" + w.coins, http.StatusBadRequest,
			api.CodeInvalidID},
		{"a board's ID for a variable", dependenciesPath(p) + "?variable_id=" + w.market, http.StatusBadRequest,
			api.CodeInvalidID},
		{"a component's ID for an attribute", dependenciesPath(p) + "?attribute_id=" + w.elara,
			http.StatusBadRequest, api.CodeInvalidID},
		{"a project that does not exist", dependenciesPath(ids.Encode(ids.Project, ids.New())), http.StatusNotFound,
			api.CodeNotFound},
		{"no kind", dependenciesPath(p) + "/" + w.market + "/" + w.docks, http.StatusUnprocessableEntity,
			api.CodeValidationFailed},
		{"another kind of list", contributionsPath(p, w.market, w.docks, "mention"), http.StatusUnprocessableEntity,
			api.CodeValidationFailed},
		{"a node's ID for a board", contributionsPath(p, w.stall, w.docks, "variable"), http.StatusBadRequest,
			api.CodeInvalidID},
		{"a board that the project lacks", contributionsPath(p, w.market, newBoardID(), "variable"),
			http.StatusNotFound, api.CodeNotFound},
		{"a component type's ID for a component", list + "&component_id=" + w.cast, http.StatusBadRequest,
			api.CodeInvalidID},
		{"a cursor of another form", list + "&cursor=" + mustCursor(t, usageKey{Board: ids.New().String(),
			Node: ids.New().String(), Site: ids.New().String(), Attribute: ids.UUID{}.String()}),
			http.StatusBadRequest, api.CodeInvalidCursor},
		{"a cursor of a UUID in upper case", list + "&cursor=" + mustCursor(t, contributionKey{
			Target: strings.ToUpper(ids.New().String()), Attribute: ids.UUID{}.String(), FromNode: ids.New().String(),
			FromSite: ids.New().String(), ToNode: ids.New().String(), ToSite: ids.New().String()}),
			http.StatusBadRequest, api.CodeInvalidCursor},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wantCode(t, tt.name, call(h, http.MethodGet, tt.path, nil, o.header(false)), tt.status, tt.code)
		})
	}
}
