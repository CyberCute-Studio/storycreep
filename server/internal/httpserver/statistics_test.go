package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/statistics"
	"github.com/CyberCute-Studio/storycreep/server/internal/statistics/statisticstest"
)

// statisticsDrift returns how the project's rows of the writing statistics
// differ from a recount of its texts from scratch (statisticstest.Drift).
func statisticsDrift(t testing.TB, conn *pgx.Conn, project string) []string {
	t.Helper()
	drift, err := statisticstest.Drift(context.Background(), conn, decode(t, ids.Project, project))
	if err != nil {
		t.Fatal(err)
	}
	return drift
}

// projectStatistics returns the writing statistics of the project, as the
// user reads them, or fails the test.
func projectStatistics(t *testing.T, h http.Handler, si signedIn, project string) statistics.ProjectStatistics {
	t.Helper()
	res := call(h, http.MethodGet, projectPath(project)+"/statistics", nil, si.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("GET the project's statistics: %d %s", res.status, res.body)
	}
	var out statistics.ProjectStatistics
	if err := json.Unmarshal(res.body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// nodeStatistics returns the writing statistics of the node of the project,
// as the user reads them, or fails the test.
func nodeStatistics(t *testing.T, h http.Handler, si signedIn, project, node string) statistics.NodeStatistics {
	t.Helper()
	res := call(h, http.MethodGet, projectPath(project)+"/nodes/"+node+"/statistics", nil, si.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("GET the node's statistics: %d %s", res.status, res.body)
	}
	var out statistics.NodeStatistics
	if err := json.Unmarshal(res.body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestStatistics checks the writing statistics of a project's content
// through the API, built through the command endpoint, as a client builds
// it: the words and the characters of each element's text and each
// connection's label, a mention counted as its component's name and a value
// reference as its name in braces, and the nodes, the choices, and the
// conditions that are written, of each node, of its board, and of the
// project, none of what is in the trash; and that each batch that the API
// answers is in them at once, a rename of what a text names among them,
// without a rescan of the project's texts: a batch counts the texts that it
// changes, or that name what it renames, and no other (AC1). A text that has
// no row yet makes the statistics incomplete until the job of the statistics
// has counted it.
//
// Verifies: FR-TEXT-06/AC1
func TestStatistics(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h, admin := s.main, d.Admin(t)
	p := createProject(t, h, owner, ws, "Port").ID
	harbour, attic := ids.Encode(ids.Board, ids.New()), ids.Encode(ids.Board, ids.New())
	greet, reply, end, gate, gone, dusty := newNodeID(), newNodeID(), newNodeID(), newNodeID(), newNodeID(),
		newNodeID()
	connID := func() string { return ids.Encode(ids.Connection, ids.New()) }
	ask, leave, again, lit, dark := connID(), connID(), connID(), connID(), connID()
	lanternLit := ids.Encode(ids.Variable, ids.New())
	prop, trust := ids.Encode(ids.ComponentType, ids.New()), ids.Encode(ids.AttributeDef, ids.New())
	elara := ids.Encode(ids.Component, ids.New())
	ifOut, elseOut := ids.Encode(ids.BranchOutput, ids.New()), ids.Encode(ids.BranchOutput, ids.New())
	unwritten := ids.Encode(ids.BranchOutput, ids.New())
	pos := map[string]int{"x": 0, "y": 0}
	cmd := func(typ string, p map[string]any) wireCommand { return wireCommand{Type: typ, Payload: p} }
	element := func(id, board string, text json.RawMessage) wireCommand {
		return cmd("node.create", map[string]any{"id": id, "board_id": board, "type": "element", "position": pos,
			"text": text})
	}
	sendBatch(t, h, owner, p,
		cmd("board.create", map[string]any{"id": harbour, "name": "Harbour"}),
		cmd("board.create", map[string]any{"id": attic, "name": "Attic"}),
		cmd("variable.create", map[string]any{"id": lanternLit, "key": "lantern_lit", "type": "bool",
			"scope": "global"}),
		cmd("component_type.create", map[string]any{"id": prop, "name": "Prop"}),
		cmd("attribute.create", map[string]any{"id": trust, "component_type_id": prop, "name": "Trust",
			"type": "int", "runtime": true}),
		cmd("component.create", map[string]any{"id": elara, "component_type_id": prop, "name": "Elara"}),
		// "Hello, Elara. Trust: {elara.trust}": 4 words, 7 + 5 + 9 + 13
		// characters.
		element(greet, harbour, richText(textInline("Hello, "), mentionInline(elara), textInline(". Trust: "),
			attributeInline(elara, trust))),
		element(reply, harbour, paragraph("Fine, go.")),
		cmd("node.create", map[string]any{"id": end, "board_id": harbour, "type": "element", "position": pos,
			"text": paragraph("The end."), "is_ending": true}),
		cmd("node.create", map[string]any{"id": gate, "board_id": harbour, "type": "branch", "position": pos}),
		cmd("branch_output.create", map[string]any{"id": ifOut, "node_id": gate, "condition": "lantern_lit"}),
		// An output whose condition is not written yet has none.
		cmd("branch_output.create", map[string]any{"id": unwritten, "node_id": gate}),
		cmd("branch_output.create", map[string]any{"id": elseOut, "node_id": gate, "else": true}),
		element(gone, harbour, paragraph("Lost words here")),
		element(dusty, attic, paragraph("Dusty attic words")),
		// "Ask about {lantern_lit}": 3 words, 10 + 13 characters.
		cmd("connection.create", map[string]any{"id": ask, "from_node_id": greet, "to_node_id": reply,
			"label": richText(textInline("Ask about "), valueInline(lanternLit))}),
		cmd("connection.create", map[string]any{"id": leave, "from_node_id": greet, "to_node_id": gate,
			"label": paragraph("Leave"), "condition": "lantern_lit"}),
		// An ending's connections are no choices.
		cmd("connection.create", map[string]any{"id": again, "from_node_id": end, "to_node_id": reply,
			"label": paragraph("Again")}),
		cmd("connection.create", map[string]any{"id": lit, "from_node_id": gate, "from_port": ifOut,
			"to_node_id": reply, "label": paragraph("Lit")}),
		cmd("connection.create", map[string]any{"id": dark, "from_node_id": gate, "from_port": elseOut,
			"to_node_id": end}),
	)
	sendBatch(t, h, owner, p,
		cmd("node.delete", map[string]any{"id": gone}),
		cmd("board.delete", map[string]any{"id": attic}),
	)

	type counts struct {
		words, characters, choices, conditions int64
	}
	of := func(c statistics.Counts) counts { return counts{c.Words, c.Characters, c.Choices, c.Conditions} }
	wantNode := func(what, node string, want counts) {
		t.Helper()
		got := nodeStatistics(t, h, owner, p, node)
		if of(got.Counts) != want || !got.Complete || got.ID != node {
			t.Errorf("%s, the node %s has %+v, complete %v, want %+v, complete", what, node, got, got.Complete,
				want)
		}
	}
	wantBoard := func(what string, nodes int64, want counts) {
		t.Helper()
		got := projectStatistics(t, h, owner, p)
		if len(got.Boards) != 1 || got.Boards[0].ID != harbour || got.Boards[0].Name != "Harbour" {
			t.Fatalf("%s, the project's boards are %+v, want Harbour alone, Attic being in the trash", what,
				got.Boards)
		}
		b := got.Boards[0]
		if of(b.Counts) != want || b.Nodes != nodes || !b.Complete {
			t.Errorf("%s, Harbour has %d nodes and %+v, complete %v, want %d and %+v, complete", what, b.Nodes,
				of(b.Counts), b.Complete, nodes, want)
		}
		if of(got.Counts) != want || got.Nodes != nodes || !got.Complete {
			t.Errorf("%s, the project has %d nodes and %+v, complete %v, want its board's", what, got.Nodes,
				of(got.Counts), got.Complete)
		}
	}
	wantNode("at first", greet, counts{4 + 3 + 1, 34 + 23 + 5, 2, 1})
	wantNode("at first", reply, counts{2, 9, 0, 0})
	wantNode("at first", end, counts{2 + 1, 8 + 5, 0, 0})
	wantNode("at first", gate, counts{1, 3, 0, 1})
	wantBoard("at first", 4, counts{14, 87, 2, 2})

	// A rename of what a text names changes its counts: "Elara Vance" is
	// two words, of 11 characters.
	before := projectStatistics(t, h, owner, p).Seq
	sendBatch(t, h, owner, p, cmd("component.rename", map[string]any{"id": elara, "name": "Elara Vance"}))
	if got := projectStatistics(t, h, owner, p); got.Seq <= before {
		t.Errorf("after a batch, the statistics are of seq %d, want after %d", got.Seq, before)
	}
	wantNode("after the component's rename", greet, counts{9, 68, 2, 1})
	// {lit} is 8 characters shorter than {lantern_lit}.
	sendBatch(t, h, owner, p, cmd("variable.rename", map[string]any{"id": lanternLit, "key": "lit"}))
	wantNode("after the variable's rename", greet, counts{9, 60, 2, 1})
	// {elara.loyalty} is 2 longer than {elara.trust}, and {ev.loyalty} 3
	// shorter than that.
	sendBatch(t, h, owner, p, cmd("attribute.set_key", map[string]any{"id": trust, "key": "loyalty"}))
	wantNode("after the definition's change of key", greet, counts{9, 62, 2, 1})
	sendBatch(t, h, owner, p, cmd("component.set_key", map[string]any{"id": elara, "key": "ev"}))
	wantNode("after the component's change of key", greet, counts{9, 59, 2, 1})
	wantBoard("after the renames", 4, counts{15, 84, 2, 2})

	// An edit counts the text that it changes, and no other: a row of
	// another text that a test changes stays as it is.
	if _, err := admin.Exec(context.Background(), `UPDATE text_statistic SET words = 999 WHERE entity_id = $1`,
		decode(t, ids.Node, reply)); err != nil {
		t.Fatal(err)
	}
	sendBatch(t, h, owner, p, cmd("node.update", map[string]any{"id": greet,
		"text": paragraph("Hello there, traveller.")}))
	wantNode("after the edit", greet, counts{3 + 3 + 1, 23 + 15 + 5, 2, 1})
	wantNode("after the edit", reply, counts{999, 9, 0, 0})
	wantBoard("after the edit", 4, counts{7 + 999 + 3 + 1, 43 + 9 + 13 + 3, 2, 2})
	if _, err := admin.Exec(context.Background(), `UPDATE text_statistic SET words = 2 WHERE entity_id = $1`,
		decode(t, ids.Node, reply)); err != nil {
		t.Fatal(err)
	}

	// The trash takes what it holds out of them, and a condition cleared is
	// none.
	sendBatch(t, h, owner, p,
		cmd("node.delete", map[string]any{"id": end}),
		cmd("connection.update", map[string]any{"id": leave, "condition": ""}),
	)
	wantNode("after the deletion", greet, counts{7, 43, 2, 0})
	wantBoard("after the deletion", 3, counts{7 + 2 + 1, 43 + 9 + 3, 2, 1})
	wantCode(t, "the statistics of a node in the trash", call(h, http.MethodGet,
		projectPath(p)+"/nodes/"+end+"/statistics", nil, owner.header(false)), http.StatusNotFound,
		api.CodeNotFound)
	wantCode(t, "the statistics of a node of no project's", call(h, http.MethodGet,
		projectPath(p)+"/nodes/"+newNodeID()+"/statistics", nil, owner.header(false)), http.StatusNotFound,
		api.CodeNotFound)
	wantCode(t, "the statistics of no project", call(h, http.MethodGet,
		projectPath(ids.Encode(ids.Project, ids.New()))+"/statistics", nil, owner.header(false)),
		http.StatusNotFound, api.CodeNotFound)
	if drift := statisticsDrift(t, admin, p); len(drift) > 0 {
		t.Errorf("the statistics are not what the texts count: %q", drift)
	}

	// A text without a row, as one that predates the statistics, makes them
	// incomplete until the job of the statistics counts it.
	if _, err := admin.Exec(context.Background(), `DELETE FROM text_statistic WHERE entity_id = $1`,
		decode(t, ids.Node, greet)); err != nil {
		t.Fatal(err)
	}
	if got := projectStatistics(t, h, owner, p); got.Complete || got.Boards[0].Complete ||
		got.Words != 7 {
		t.Errorf("without the row of a text, the project's statistics are %+v, want 7 words, not complete", got)
	}
	if got := nodeStatistics(t, h, owner, p, greet); got.Complete {
		t.Errorf("without the row of its text, the node's statistics are %+v, want them not complete", got)
	}
	if _, err := statistics.CountProjects(context.Background(), readyDB{s.ready}); err != nil {
		t.Fatal(err)
	}
	wantNode("once the job has run", greet, counts{7, 43, 2, 0})
	wantBoard("once the job has run", 3, counts{10, 55, 2, 1})
}
