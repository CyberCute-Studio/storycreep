package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
)

func newNodeID() string                           { return ids.Encode(ids.Node, ids.New()) }
func boardNodesPath(project, board string) string { return boardPath(project, board) + "/nodes" }
func nodePath(project, n string) string           { return projectPath(project) + "/nodes/" + n }
func tagsPath(project string) string              { return projectPath(project) + "/tags" }

// node adds an element to a new board of the project, as the superuser, and
// returns its ID.
func (f *rolesFixture) node(t *testing.T, project string) string {
	t.Helper()
	return f.boardNode(t, project, f.board(t, project))
}

// boardNode adds an element to the board of the project, as the superuser,
// and returns its ID.
func (f *rolesFixture) boardNode(t *testing.T, project, board string) string {
	t.Helper()
	id := ids.New()
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO node (id, workspace_id, project_id, board_id, type,
		kind, text, x, y) VALUES ($1, $2, $3, $4, 'element', 'dialogue',
		'{"type": "doc", "content": [{"type": "paragraph"}]}', 0, 0)`, id, decode(t, ids.Workspace, f.ws),
		decode(t, ids.Project, project), decode(t, ids.Board, board)); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.Node, id)
}

// createNode creates a node of the type typ on the board of the project, with
// the fields of extra, as si, through the command endpoint, and returns its
// ID.
func createNode(t *testing.T, h http.Handler, si signedIn, project, board, typ string, extra map[string]any) string {
	t.Helper()
	id := newNodeID()
	p := map[string]any{"id": id, "board_id": board, "type": typ, "position": map[string]int{"x": 0, "y": 0}}
	for k, v := range extra {
		p[k] = v
	}
	res := call(h, http.MethodPost, commandsPath(project), commandBatch(t, wireCommand{Type: "node.create",
		Payload: p}), si.header(true))
	if res.status != http.StatusOK {
		t.Fatalf("create the node: %d %s", res.status, res.body)
	}
	return id
}

// nodesPage reads a page of nodes from res, which must be 200.
func nodesPage(t *testing.T, res response) api.Page[node.Node] {
	t.Helper()
	if res.status != http.StatusOK {
		t.Fatalf("%d %s, want 200 and a page of nodes", res.status, res.body)
	}
	var page api.Page[node.Node]
	if err := json.Unmarshal(res.body, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

// TestNodeRoutes checks the routes that read a project's nodes: a board's
// nodes, a node, the jumpers that target a node or a board's nodes, and the
// tags that the project suggests, and that deleting a board that jumpers on
// other boards target is answered with 409 board_referenced, which lists them,
// until the deletion is confirmed, which leaves them dangling.
//
// Verifies: FR-PROJ-02/AC2
func TestNodeRoutes(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour").ID
	harbour, pier := createBoard(t, s.main, owner, p, "Harbour"), createBoard(t, s.main, owner, p, "Pier")
	keeper := createNode(t, s.main, owner, p, harbour, "element", map[string]any{"title": "Keeper",
		"tags": []string{"Quest"}, "position": map[string]any{"x": 0.125, "y": 2.4e2}})
	createNode(t, s.main, owner, p, harbour, "element", map[string]any{"tags": []string{"quest", "Lore"}})
	jumper := createNode(t, s.main, owner, p, pier, "jumper", map[string]any{"title": "To the keeper",
		"target": map[string]string{"board_id": harbour, "node_id": keeper}})

	page := nodesPage(t, call(s.main, http.MethodGet, boardNodesPath(p, harbour), nil, owner.header(false)))
	if len(page.Items) != 2 || page.Items[0].ID != keeper {
		t.Errorf("the board's nodes are %+v", page.Items)
	}
	res := call(s.main, http.MethodGet, nodePath(p, keeper), nil, owner.header(false))
	if res.status != http.StatusOK || !json.Valid(res.body) ||
		!containsAll(string(res.body), `"position":{"x":0.13,"y":240}`, `"title":"Keeper"`) {
		t.Errorf("the node reads %d %s", res.status, res.body)
	}
	page = nodesPage(t, call(s.main, http.MethodGet, nodePath(p, keeper)+"/referenced-by", nil, owner.header(false)))
	if len(page.Items) != 1 || page.Items[0].ID != jumper {
		t.Errorf("the node is referenced by %+v", page.Items)
	}
	page = nodesPage(t, call(s.main, http.MethodGet, boardPath(p, harbour)+"/referenced-by", nil,
		owner.header(false)))
	if len(page.Items) != 1 || page.Items[0].ID != jumper {
		t.Errorf("the board is referenced by %+v", page.Items)
	}
	res = call(s.main, http.MethodGet, tagsPath(p)+"?prefix=QU", nil, owner.header(false))
	var tags struct{ Items []node.Tag }
	if res.status != http.StatusOK || json.Unmarshal(res.body, &tags) != nil || len(tags.Items) != 1 ||
		tags.Items[0].Nodes != 2 {
		t.Errorf("the tags that start with QU are %d %s", res.status, res.body)
	}
	wantProblem(t, "a prefix with a control character", call(s.main, http.MethodGet, tagsPath(p)+"?prefix=a%07",
		nil, owner.header(false)), "query.prefix")
	wantCode(t, "a node that the project does not have", call(s.main, http.MethodGet, nodePath(p, newNodeID()), nil,
		owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	wantCode(t, "the nodes of a board that the project does not have", call(s.main, http.MethodGet,
		boardNodesPath(p, newBoardID()), nil, owner.header(false)), http.StatusNotFound, api.CodeNotFound)

	// Deleting the board that the jumper targets needs confirmation.
	res = call(s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{Type: "board.delete",
		Payload: map[string]any{"id": harbour}}), owner.header(true))
	wantCode(t, "the deletion of a board that a jumper targets", res, http.StatusConflict, api.CodeBoardReferenced)
	details := res.details(t)
	listed, _ := details["jumpers"].([]any)
	if count, _ := details["jumper_count"].(float64); count != 1 || len(listed) != 1 ||
		listed[0].(map[string]any)["id"] != jumper || listed[0].(map[string]any)["board_id"] != pier {
		t.Errorf("the refusal's details are %v", details)
	}
	res = call(s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{Type: "board.delete",
		Payload: map[string]any{"id": harbour, "confirm": true}}), owner.header(true))
	if res.status != http.StatusOK {
		t.Fatalf("the confirmed deletion: %d %s", res.status, res.body)
	}
	res = call(s.main, http.MethodGet, nodePath(p, jumper), nil, owner.header(false))
	if !containsAll(string(res.body), `"target":{"board_id":"`+harbour+`","node_id":"`+keeper+`"}`) {
		t.Errorf("once the board is deleted, the jumper reads %s, want its target still", res.body)
	}
}

// containsAll reports whether s contains each of subs.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
