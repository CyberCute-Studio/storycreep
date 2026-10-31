package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/trash"
)

// TestContentIDsAcrossKinds checks that no two entities of the projects'
// content hold one UUID, whatever their kinds (PRD §5.2): each command that
// creates one refuses, at its payload's id, the UUID of an entity of another
// kind, the UUID of an expression, which the server makes, among them, of
// the same project, of a project of another workspace, whose rows the
// batch's transaction does not see, or of the same batch; and that a UUID is
// free again once its entity is purged, whether on its own or by the cascade
// of its board's purge, which the application's role makes.
func TestContentIDsAcrossKinds(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h, p := s.main, createProject(t, s.main, owner, ws, "Harbour").ID
	folder, board, keeper, branch, out := newFolderID(), newBoardID(), newNodeID(), newNodeID(),
		ids.Encode(ids.BranchOutput, ids.New())
	gate, seen, cast, trust, elara := newConnectionID(), newVariableID(), newComponentTypeID(), newAttributeID(),
		newComponentID()
	pos := map[string]int{"x": 0, "y": 0}
	cmd := func(typ string, p map[string]any) wireCommand { return wireCommand{Type: typ, Payload: p} }
	sendBatch(t, h, owner, p,
		cmd("folder.create", map[string]any{"id": folder, "kind": "board", "name": "Act 1"}),
		cmd("board.create", map[string]any{"id": board, "name": "Harbour"}),
		cmd("variable.create", map[string]any{"id": seen, "key": "seen", "type": "bool", "scope": "global"}),
		cmd("node.create", map[string]any{"id": keeper, "board_id": board, "type": "element", "position": pos,
			"on_enter": "seen = true"}),
		cmd("node.create", map[string]any{"id": branch, "board_id": board, "type": "branch", "position": pos}),
		cmd("branch_output.create", map[string]any{"id": out, "node_id": branch, "condition": "seen"}),
		cmd("connection.create", map[string]any{"id": gate, "from_node_id": keeper, "to_node_id": branch}),
		cmd("component_type.create", map[string]any{"id": cast, "name": "Cast"}),
		cmd("attribute.create", map[string]any{"id": trust, "component_type_id": cast, "name": "Trust",
			"type": "int", "runtime": true}),
		cmd("component.create", map[string]any{"id": elara, "component_type_id": cast, "name": "Elara"}),
	)
	scripts := expressionIDs(t, d.Admin(t), p)
	script, condition := scripts["on_enter "+keeper], scripts["condition "+out]

	// Each command that creates an entity, given the UUID of an entity of
	// another kind, each kind's once.
	creates := []struct {
		name   string
		holder ids.UUID
		create func(id ids.UUID) wireCommand
	}{
		{"a folder of an expression's UUID", script, func(id ids.UUID) wireCommand {
			return cmd("folder.create", map[string]any{"id": ids.Encode(ids.Folder, id), "kind": "board",
				"name": "Act 2"})
		}},
		{"a board of a node's UUID", uuidOf(t, keeper), func(id ids.UUID) wireCommand {
			return cmd("board.create", map[string]any{"id": ids.Encode(ids.Board, id), "name": "Pier"})
		}},
		{"a node of a connection's UUID", uuidOf(t, gate), func(id ids.UUID) wireCommand {
			return cmd("node.create", map[string]any{"id": ids.Encode(ids.Node, id), "board_id": board,
				"type": "element", "position": pos})
		}},
		{"a duplicate of a folder's UUID", uuidOf(t, folder), func(id ids.UUID) wireCommand {
			return cmd("node.duplicate", map[string]any{"id": ids.Encode(ids.Node, id), "source_id": keeper,
				"position": pos})
		}},
		{"an output of a board's UUID", uuidOf(t, board), func(id ids.UUID) wireCommand {
			return cmd("branch_output.create", map[string]any{"id": ids.Encode(ids.BranchOutput, id),
				"node_id": branch})
		}},
		{"a connection of a variable's UUID", uuidOf(t, seen), func(id ids.UUID) wireCommand {
			return cmd("connection.create", map[string]any{"id": ids.Encode(ids.Connection, id),
				"from_node_id": branch, "from_port": out, "to_node_id": keeper})
		}},
		{"a variable of a component type's UUID", uuidOf(t, cast), func(id ids.UUID) wireCommand {
			return cmd("variable.create", map[string]any{"id": ids.Encode(ids.Variable, id), "key": "gold",
				"type": "int", "scope": "global"})
		}},
		{"a component type of an attribute's UUID", uuidOf(t, trust), func(id ids.UUID) wireCommand {
			return cmd("component_type.create", map[string]any{"id": ids.Encode(ids.ComponentType, id),
				"name": "Prop"})
		}},
		{"an attribute of a component's UUID", uuidOf(t, elara), func(id ids.UUID) wireCommand {
			return cmd("attribute.create", map[string]any{"id": ids.Encode(ids.AttributeDef, id),
				"component_type_id": cast, "name": "Mood", "type": "string", "runtime": true})
		}},
		{"a component of an output's UUID", uuidOf(t, out), func(id ids.UUID) wireCommand {
			return cmd("component.create", map[string]any{"id": ids.Encode(ids.Component, id),
				"component_type_id": cast, "name": "Mira"})
		}},
	}
	for _, tt := range creates {
		wantProblem(t, tt.name, call(h, http.MethodPost, commandsPath(p), commandBatch(t, tt.create(tt.holder)),
			owner.header(true)), "body.commands[0].payload.id")
		// The same command of a UUID of its own applies, so that the
		// refusal above was of the UUID, not of the payload.
		sendBatch(t, h, owner, p, tt.create(ids.New()))
	}

	// One batch that creates two entities of one UUID.
	twin := ids.New()
	wantProblem(t, "a board of the UUID of a folder that the batch creates", call(h, http.MethodPost,
		commandsPath(p), commandBatch(t,
			cmd("folder.create", map[string]any{"id": ids.Encode(ids.Folder, twin), "kind": "board", "name": "Act 3"}),
			cmd("board.create", map[string]any{"id": ids.Encode(ids.Board, twin), "name": "Quay"})),
		owner.header(true)), "body.commands[1].payload.id")

	// A project of another workspace, whose transactions see none of
	// Harbour's rows.
	other := workspaceFrom(t, send(h, http.MethodPost, workspacesPath, jsonBody(t, map[string]string{
		"name": "Lighthouse", "slug": "lighthouse"}), "", owner.header(true)), http.StatusCreated)
	q := createProject(t, h, owner, other.ID, "Lighthouse").ID
	wantProblem(t, "a board of another workspace's connection's UUID", call(h, http.MethodPost, commandsPath(q),
		commandBatch(t, cmd("board.create", map[string]any{"id": ids.Encode(ids.Board, uuidOf(t, gate)),
			"name": "Lamp"})), owner.header(true)), "body.commands[0].payload.id")

	// Once the trash's purge deletes the node, a connection takes its UUID.
	sendBatch(t, h, owner, p, cmd("node.delete", map[string]any{"id": keeper}))
	if _, err := d.Admin(t).Exec(context.Background(),
		"UPDATE node SET deleted_at = now() - interval '31 days' WHERE id = $1", uuidOf(t, keeper)); err != nil {
		t.Fatal(err)
	}
	if _, err := trash.Purge(context.Background(), d.Open(t)); err != nil {
		t.Fatal(err)
	}
	sendBatch(t, h, owner, p, cmd("connection.create", map[string]any{"id": ids.Encode(ids.Connection,
		uuidOf(t, keeper)), "from_node_id": branch, "from_port": out, "to_node_id": branch}))

	// Once the purge deletes the board, the cascade of its foreign keys
	// deletes its nodes, its outputs, its connections, and their
	// expressions, whose UUIDs folders then take.
	sendBatch(t, h, owner, p, cmd("board.delete", map[string]any{"id": board, "confirm": true}))
	if _, err := d.Admin(t).Exec(context.Background(),
		"UPDATE board SET deleted_at = now() - interval '31 days' WHERE id = $1", uuidOf(t, board)); err != nil {
		t.Fatal(err)
	}
	if _, err := trash.Purge(context.Background(), d.Open(t)); err != nil {
		t.Fatal(err)
	}
	var folders []wireCommand
	for i, id := range []ids.UUID{uuidOf(t, branch), uuidOf(t, out), uuidOf(t, keeper), condition} {
		folders = append(folders, cmd("folder.create", map[string]any{"id": ids.Encode(ids.Folder, id),
			"kind": "board", "name": fmt.Sprintf("Act %d", i+4)}))
	}
	sendBatch(t, h, owner, p, folders...)
}
