package httpserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
)

// readNode reads the node n of the project as si, which must be 200.
func readNode(t *testing.T, h http.Handler, si signedIn, project, n string) node.Node {
	t.Helper()
	res := call(h, http.MethodGet, nodePath(project, n), nil, si.header(false))
	var out node.Node
	if res.status != http.StatusOK || json.Unmarshal(res.body, &out) != nil {
		t.Fatalf("read the node %s: %d %s", n, res.status, res.body)
	}
	return out
}

// kinds returns the kinds of the outputs, in their order, each with its ID.
func kinds(outputs []node.BranchOutput) [][2]string {
	out := make([][2]string, len(outputs))
	for i, o := range outputs {
		out[i] = [2]string{o.ID, o.Kind}
	}
	return out
}

// TestOutputRoutes checks that a branch reads with its outputs, in their
// order, each of the kind of its place, with its condition, compiled, that a
// connection leaves a branch by an output, which it reads as its from_port,
// with its condition, and that deleting an output that a connection leaves
// is answered with 409 branch_output_connected, which lists the connections,
// until the deletion is confirmed, which deletes them with the output, in its
// command, and the undo of which brings both back.
//
// Verifies: FR-EDIT-04/AC3
func TestOutputRoutes(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour").ID
	harbour := createBoard(t, s.main, owner, p, "Harbour")
	branch := createNode(t, s.main, owner, p, harbour, "branch", map[string]any{"on_enter": "visits(node(\"" +
		harbour + "\")) > 0"})
	keeper := createNode(t, s.main, owner, p, harbour, "element", nil)
	send := func(cmds ...wireCommand) response {
		return call(s.main, http.MethodPost, commandsPath(p), commandBatch(t, cmds...), owner.header(true))
	}
	trusted, doubted, otherwise := ids.Encode(ids.BranchOutput, ids.New()), ids.Encode(ids.BranchOutput, ids.New()),
		ids.Encode(ids.BranchOutput, ids.New())
	arrow := ids.Encode(ids.Connection, ids.New())
	if res := send(
		wireCommand{Type: "branch_output.create", Payload: map[string]any{"id": doubted, "node_id": branch,
			"condition": "false", "order": "T"}},
		wireCommand{Type: "branch_output.create", Payload: map[string]any{"id": otherwise, "node_id": branch,
			"else": true}},
		// The output placed before the first is the branch's if.
		wireCommand{Type: "branch_output.create", Payload: map[string]any{"id": trusted, "node_id": branch,
			"condition": "true", "order": "M"}},
		wireCommand{Type: "connection.create", Payload: map[string]any{"id": arrow, "from_node_id": branch,
			"from_port": trusted, "to_node_id": keeper, "condition": "true && true"}},
	); res.status != http.StatusOK {
		t.Fatalf("make the branch's outputs: %d %s", res.status, res.body)
	}

	n := readNode(t, s.main, owner, p, branch)
	want := [][2]string{{trusted, node.OutputIf}, {doubted, node.OutputElseIf}, {otherwise, node.OutputElse}}
	if got := kinds(n.Outputs); len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("the branch's outputs are %v, want %v", got, want)
	}
	if c := n.Outputs[0].Condition; c == nil || !c.IsValid || string(c.AST) != `{"kind":"lit","type":"bool","value":true}` {
		t.Errorf("the if's condition is %+v", c)
	}
	if n.Outputs[2].Condition != nil || n.Outputs[2].Order != "" {
		t.Errorf("the else is %+v, without a condition or an order", n.Outputs[2])
	}
	// A node reference to a board's ID is no node.
	if n.OnEnter == nil || n.OnEnter.IsValid {
		t.Errorf("the branch's on-enter is %+v", n.OnEnter)
	}
	res := call(s.main, http.MethodGet, connectionPath(p, arrow), nil, owner.header(false))
	var c connection.Connection
	if res.status != http.StatusOK || json.Unmarshal(res.body, &c) != nil || c.FromPort != trusted ||
		c.Condition == nil || !c.Condition.IsValid {
		t.Errorf("the connection reads %d %s", res.status, res.body)
	}

	// A connection leaves a branch by one of its outputs, not by out.
	wantCode(t, "a connection out of a branch by out", send(wireCommand{Type: "connection.create",
		Payload: map[string]any{"id": ids.Encode(ids.Connection, ids.New()), "from_node_id": branch,
			"to_node_id": keeper}}), http.StatusUnprocessableEntity, api.CodeValidationFailed)

	// Deleting the if, which the connection leaves, needs confirmation.
	res = send(wireCommand{Type: "branch_output.delete", Payload: map[string]any{"id": trusted}})
	wantCode(t, "the deletion of an output that a connection leaves", res, http.StatusConflict,
		api.CodeOutputConnected)
	details := res.details(t)
	listed, _ := details["connections"].([]any)
	if count, _ := details["connection_count"].(float64); count != 1 || len(listed) != 1 ||
		listed[0].(map[string]any)["id"] != arrow || listed[0].(map[string]any)["to_node_id"] != keeper {
		t.Errorf("the refusal's details are %v", details)
	}
	res = send(wireCommand{Type: "branch_output.delete", Payload: map[string]any{"id": trusted, "confirm": true}})
	if res.status != http.StatusOK {
		t.Fatalf("the confirmed deletion: %d %s", res.status, res.body)
	}
	body := resultFrom(t, res)
	wantCode(t, "the connection of a deleted output", call(s.main, http.MethodGet, connectionPath(p, arrow), nil,
		owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	want = [][2]string{{doubted, node.OutputIf}, {otherwise, node.OutputElse}}
	if got := kinds(readNode(t, s.main, owner, p, branch).Outputs); len(got) != 2 || got[0] != want[0] ||
		got[1] != want[1] {
		t.Errorf("once the if is deleted, the branch's outputs are %v, want %v", got, want)
	}

	// The undo restores the output, which brings its connection back.
	if res := send(undoCommands(body.Undo)...); res.status != http.StatusOK {
		t.Fatalf("the undo of the deletion: %d %s", res.status, res.body)
	}
	if res := call(s.main, http.MethodGet, connectionPath(p, arrow), nil, owner.header(false)); res.status !=
		http.StatusOK {
		t.Errorf("once the deletion is undone, the connection reads %d %s", res.status, res.body)
	}
	want = [][2]string{{trusted, node.OutputIf}, {doubted, node.OutputElseIf}, {otherwise, node.OutputElse}}
	if got := kinds(readNode(t, s.main, owner, p, branch).Outputs); len(got) != 3 || got[0] != want[0] ||
		got[1] != want[1] || got[2] != want[2] {
		t.Errorf("once the deletion is undone, the branch's outputs are %v, want %v", got, want)
	}
}
