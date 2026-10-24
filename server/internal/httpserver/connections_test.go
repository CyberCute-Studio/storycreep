package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

func newConnectionID() string { return ids.Encode(ids.Connection, ids.New()) }
func boardConnectionsPath(project, board string) string {
	return boardPath(project, board) + "/connections"
}
func connectionPath(project, c string) string { return projectPath(project) + "/connections/" + c }

// paragraph returns a rich-text document of one paragraph of the text.
func paragraph(text string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph",
		"content": []any{map[string]any{"type": "text", "text": text}}}}})
	return raw
}

// connection adds a connection from a new element to itself, on a new board
// of the project, as the superuser, and returns its ID.
func (f *rolesFixture) connection(t *testing.T, project string) string {
	t.Helper()
	c, _ := f.boardConnection(t, project)
	return c
}

// boardConnection adds a connection from a new element to itself, on a new
// board of the project, as the superuser, and returns its ID and its board's.
func (f *rolesFixture) boardConnection(t *testing.T, project string) (connection, board string) {
	t.Helper()
	board = f.board(t, project)
	n := f.boardNode(t, project, board)
	id := ids.New()
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO connection (id, workspace_id, project_id, board_id,
		from_node_id, to_node_id, "order", label) VALUES ($1, $2, $3, $4, $5, $5, 'V',
		'{"type": "doc", "content": [{"type": "paragraph"}]}')`, id, decode(t, ids.Workspace, f.ws),
		decode(t, ids.Project, project), decode(t, ids.Board, board), decode(t, ids.Node, n)); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.Connection, id), board
}

// createConnection creates a connection from the node from to the node to of
// the project, with the fields of extra, as si, through the command
// endpoint, and returns its ID.
func createConnection(t *testing.T, h http.Handler, si signedIn, project, from, to string,
	extra map[string]any) string {
	t.Helper()
	id := newConnectionID()
	p := map[string]any{"id": id, "from_node_id": from, "to_node_id": to}
	for k, v := range extra {
		p[k] = v
	}
	res := call(h, http.MethodPost, commandsPath(project), commandBatch(t, wireCommand{Type: "connection.create",
		Payload: p}), si.header(true))
	if res.status != http.StatusOK {
		t.Fatalf("create the connection: %d %s", res.status, res.body)
	}
	return id
}

// connectionsPage reads a page of connections from res, which must be 200.
func connectionsPage(t *testing.T, res response) api.Page[connection.Connection] {
	t.Helper()
	if res.status != http.StatusOK {
		t.Fatalf("%d %s, want 200 and a page of connections", res.status, res.body)
	}
	var page api.Page[connection.Connection]
	if err := json.Unmarshal(res.body, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

// TestConnectionRoutes checks the routes that read a project's connections:
// a board's connections and a connection, each with its label in its
// canonical form; that the commands refuse a connection out of a jumper at
// its field; and that a node's connections are in the trash with it, so that
// its restoration brings back those whose other node is not in the trash,
// and its batch's answer lists the others, each with the node that it waits
// for, which brings it back in turn.
//
// Verifies: FR-PROJ-06/AC1
func TestConnectionRoutes(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour").ID
	harbour := createBoard(t, s.main, owner, p, "Harbour")
	keeper := createNode(t, s.main, owner, p, harbour, "element", nil)
	elara := createNode(t, s.main, owner, p, harbour, "element", nil)
	jumper := createNode(t, s.main, owner, p, harbour, "jumper", nil)
	ask := createConnection(t, s.main, owner, p, keeper, elara, map[string]any{"label": json.RawMessage(
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Ask about "},` +
			`{"type":"text","text":"the lighthouse"}]}]}`)})
	silent := createConnection(t, s.main, owner, p, keeper, keeper, nil)

	page := connectionsPage(t, call(s.main, http.MethodGet, boardConnectionsPath(p, harbour), nil, owner.header(false)))
	if len(page.Items) != 2 || page.Items[0].ID != ask || page.Items[1].ID != silent {
		t.Errorf("the board's connections are %+v", page.Items)
	}
	res := call(s.main, http.MethodGet, connectionPath(p, ask), nil, owner.header(false))
	if res.status != http.StatusOK || !containsAll(string(res.body), `"from_node_id":"`+keeper+`"`,
		`"to_node_id":"`+elara+`"`, `"from_port":"out"`, `"order":"V"`,
		`"label":{"content":[{"content":[{"text":"Ask about the lighthouse","type":"text"}],"type":"paragraph"}],`+
			`"type":"doc"}`) {
		t.Errorf("the connection reads %d %s", res.status, res.body)
	}
	if res := call(s.main, http.MethodGet, connectionPath(p, silent), nil, owner.header(false)); !containsAll(
		string(res.body), `"order":"W"`, `"label":{"content":[{"type":"paragraph"}],"type":"doc"}`) {
		t.Errorf("the connection without a label or an order reads %s", res.body)
	}
	wantCode(t, "a connection that the project does not have", call(s.main, http.MethodGet,
		connectionPath(p, newConnectionID()), nil, owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	wantCode(t, "the connections of a board that the project does not have", call(s.main, http.MethodGet,
		boardConnectionsPath(p, newBoardID()), nil, owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	wantProblem(t, "a connection out of a jumper", call(s.main, http.MethodPost, commandsPath(p),
		commandBatch(t, wireCommand{Type: "connection.create", Payload: map[string]any{"id": newConnectionID(),
			"from_node_id": jumper, "to_node_id": keeper}}), owner.header(true)), "body.commands[0].payload.from_node_id")

	// Deleting the node that the connection enters takes it to the trash
	// with the node, and restoring the node that it leaves, once that is
	// deleted too, leaves it there, waiting for the other.
	apply := func(typ, id string) commandResultBody {
		t.Helper()
		return resultFrom(t, call(s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{Type: typ,
			Payload: map[string]string{"id": id}}), owner.header(true)))
	}
	apply("node.delete", elara)
	wantCode(t, "a connection of a node in the trash", call(s.main, http.MethodGet, connectionPath(p, ask), nil,
		owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	apply("node.delete", keeper)
	if page := connectionsPage(t, call(s.main, http.MethodGet, boardConnectionsPath(p, harbour), nil,
		owner.header(false))); len(page.Items) != 0 {
		t.Errorf("with their nodes in the trash, the board's connections are %+v", page.Items)
	}
	restored := apply("node.restore", keeper)
	if len(restored.NotReconnected) != 1 || restored.NotReconnected[0].ID != ask ||
		!reflect.DeepEqual(restored.NotReconnected[0].WaitingFor, []string{elara}) {
		t.Errorf("the restoration's answer lists %+v, want the connection that waits for the other node",
			restored.NotReconnected)
	}
	page = connectionsPage(t, call(s.main, http.MethodGet, boardConnectionsPath(p, harbour), nil, owner.header(false)))
	if len(page.Items) != 1 || page.Items[0].ID != silent {
		t.Errorf("once the node is restored, the board's connections are %+v, want its connection to itself", page.Items)
	}
	if restored := apply("node.restore", elara); len(restored.NotReconnected) != 0 {
		t.Errorf("the other node's restoration lists %+v", restored.NotReconnected)
	}
	if res := call(s.main, http.MethodGet, connectionPath(p, ask), nil, owner.header(false)); res.status != http.StatusOK {
		t.Errorf("once both of its nodes are restored, the connection reads %d %s", res.status, res.body)
	}
}

// TestRichTextAtEachEndpoint checks each endpoint that stores rich text, a
// node's text, a connection's label, and a component's description, through
// the commands that set them: each refuses a document with a mark or a node
// that the schema does not take with 422 validation_failed at the field, and
// each stores the document that it takes in a form that validates against
// the published rich-text JSON Schema.
//
// Verifies: FR-TEXT-01/AC1
func TestRichTextAtEachEndpoint(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour").ID
	harbour := createBoard(t, s.main, owner, p, "Harbour")
	keeper := createNode(t, s.main, owner, p, harbour, "element", nil)
	var types struct {
		Items []struct{ ID string }
	}
	if res := call(s.main, http.MethodGet, componentTypesPath(p), nil, owner.header(false)); res.status != http.StatusOK ||
		json.Unmarshal(res.body, &types) != nil || len(types.Items) == 0 {
		t.Fatalf("the project's component types: %d %s", res.status, res.body)
	}

	// The schema of the documents that the published JSON Schema describes,
	// which TestSchemaFile holds the file to (package richtext).
	reg := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	schema := huma.SchemaFromType(reg, reflect.TypeFor[richtext.Doc]())
	valid := func(what string, doc json.RawMessage) {
		t.Helper()
		var v any
		if err := json.Unmarshal(doc, &v); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		res := &huma.ValidateResult{}
		huma.Validate(reg, schema, huma.NewPathBuffer([]byte{}, 0), huma.ModeWriteToServer, v, res)
		if len(res.Errors) > 0 {
			t.Errorf("the stored %s %s does not validate against the schema: %v", what, doc, res.Errors)
		}
	}
	text := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text",` +
		`"text":"The keeper sent me.","marks":[{"type":"italic"},{"type":"bold"}]}]}]}`)

	for _, e := range []struct {
		name, field string
		// command returns the command that sets the field to doc.
		command func(doc json.RawMessage) wireCommand
		// read returns the field as it is stored, once command has set it.
		read func() json.RawMessage
	}{
		{"a node's text", "text", func(doc json.RawMessage) wireCommand {
			return wireCommand{Type: "node.update", Payload: map[string]any{"id": keeper, "text": doc}}
		}, func() json.RawMessage {
			var n struct{ Text json.RawMessage }
			res := call(s.main, http.MethodGet, nodePath(p, keeper), nil, owner.header(false))
			if json.Unmarshal(res.body, &n) != nil {
				t.Fatalf("the node: %d %s", res.status, res.body)
			}
			return n.Text
		}},
		{"a connection's label", "label", func(doc json.RawMessage) wireCommand {
			return wireCommand{Type: "connection.create", Payload: map[string]any{"id": newConnectionID(),
				"from_node_id": keeper, "to_node_id": keeper, "label": doc}}
		}, func() json.RawMessage {
			page := connectionsPage(t, call(s.main, http.MethodGet, boardConnectionsPath(p, harbour), nil,
				owner.header(false)))
			if len(page.Items) != 1 {
				t.Fatalf("the board's connections are %+v", page.Items)
			}
			raw, _ := json.Marshal(page.Items[0].Label)
			return raw
		}},
		{"a component's description", "description", func(doc json.RawMessage) wireCommand {
			return wireCommand{Type: "component.create", Payload: map[string]any{"id": newComponentID(),
				"component_type_id": types.Items[0].ID, "name": "Elara", "description": doc}}
		}, func() json.RawMessage {
			var list struct {
				Items []struct{ ID string }
			}
			res := call(s.main, http.MethodGet, componentsPath(p), nil, owner.header(false))
			if json.Unmarshal(res.body, &list) != nil || len(list.Items) != 1 {
				t.Fatalf("the components: %d %s", res.status, res.body)
			}
			var c struct{ Description json.RawMessage }
			res = call(s.main, http.MethodGet, componentPath(p, list.Items[0].ID), nil, owner.header(false))
			if json.Unmarshal(res.body, &c) != nil {
				t.Fatalf("the component: %d %s", res.status, res.body)
			}
			return c.Description
		}},
	} {
		for kind, doc := range map[string]string{
			"mark": `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x",` +
				`"marks":[{"type":"underline"}]}]}]}`,
			"node": `{"type":"doc","content":[{"type":"heading","attrs":{"level":1},"content":[{"type":"text",` +
				`"text":"x"}]}]}`,
		} {
			res := call(s.main, http.MethodPost, commandsPath(p), commandBatch(t, e.command(json.RawMessage(doc))),
				owner.header(true))
			at := "body.commands[0].payload." + e.field
			found := false
			for _, loc := range problems(t, res) {
				found = found || strings.HasPrefix(loc, at)
			}
			if !found {
				t.Errorf("%s with an unknown %s: %s, want a problem at %s", e.name, kind, res.body, at)
			}
		}
		if res := call(s.main, http.MethodPost, commandsPath(p), commandBatch(t, e.command(text)),
			owner.header(true)); res.status != http.StatusOK {
			t.Fatalf("%s: %d %s", e.name, res.status, res.body)
		}
		stored := e.read()
		if !containsAll(string(stored), `"marks":[{"type":"bold"},{"type":"italic"}]`) {
			t.Errorf("the stored %s is %s, want it in its canonical form", e.name, stored)
		}
		valid(e.name, stored)
	}
}
