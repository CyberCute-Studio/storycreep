package httpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/clipboard"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

// copyPath and resolvePath are the clipboard's routes of the project id.
func copyPath(id string) string    { return projectPath(id) + "/clipboard/copy" }
func resolvePath(id string) string { return projectPath(id) + "/clipboard/resolve" }

// clipboardWorld is a project whose board Harbour holds what the clipboard's
// tests copy: an element, Keeper, whose speaker is the component elara, whose
// text shows the global trust and mentions elara, and whose on-enter script
// writes trust and Harbour's own mood; a branch, Choice, whose if reads trust
// and elara.fear and whose else has no condition; an element, Lamp; a jumper,
// Back, that targets Keeper; and a jumper, Away, that targets Dock, a node of
// the board Pier. Keeper's connection to Choice is labelled with trust's value
// and needs the flow to have visited Keeper; Choice's if leads to Lamp and its
// else back to Keeper, and Lamp leads to Back.
type clipboardWorld struct {
	s                                                   *server
	owner                                               signedIn
	ws, project, harbour, pier                          string
	trust, mood, character, fear, elara, dock           string
	keeper, choice, ifOut, elseOut, lamp, back, away    string
	toChoice, ifToLamp, elseToKeeper, lampToBack, extra string
}

// newClipboardWorld returns a clipboardWorld.
func newClipboardWorld(t *testing.T) *clipboardWorld {
	t.Helper()
	s, _, owner, ws := inviteServer(t, "open", nil)
	w := &clipboardWorld{s: s, owner: owner, ws: ws}
	w.project = createProject(t, s.main, owner, ws, "Lighthouse").ID
	w.harbour, w.pier = createBoard(t, s.main, owner, w.project, "Harbour"), createBoard(t, s.main, owner, w.project,
		"Pier")
	w.trust, w.mood = newVariableID(), newVariableID()
	w.character, w.fear = ids.Encode(ids.ComponentType, ids.New()), ids.Encode(ids.AttributeDef, ids.New())
	w.elara = newComponentID()
	w.apply(t,
		wireCommand{Type: "variable.create", Payload: map[string]any{"id": w.trust, "key": "trust", "type": "int",
			"scope": "global", "default": 3, "description": "How far the keeper trusts you"}},
		wireCommand{Type: "variable.create", Payload: map[string]any{"id": w.mood, "key": "mood", "type": "string",
			"scope": "board", "board_id": w.harbour, "reset_on_enter": true}},
		wireCommand{Type: "component_type.create", Payload: map[string]any{"id": w.character, "name": "Hero",
			"key": "hero"}},
		wireCommand{Type: "attribute.create", Payload: map[string]any{"id": w.fear, "component_type_id": w.character,
			"name": "Fear", "key": "fear", "type": "int", "runtime": true}},
		wireCommand{Type: "component.create", Payload: map[string]any{"id": w.elara, "component_type_id": w.character,
			"name": "Elara", "key": "elara"}})
	w.dock = createNode(t, s.main, owner, w.project, w.pier, "element", map[string]any{"title": "Dock"})
	w.keeper, w.choice, w.lamp = newNodeID(), newNodeID(), newNodeID()
	w.back, w.away = newNodeID(), newNodeID()
	w.ifOut, w.elseOut = ids.Encode(ids.BranchOutput, ids.New()), ids.Encode(ids.BranchOutput, ids.New())
	w.toChoice, w.ifToLamp, w.elseToKeeper = newConnectionID(), newConnectionID(), newConnectionID()
	w.lampToBack, w.extra = newConnectionID(), newConnectionID()
	at := func(x, y float64) map[string]any { return map[string]any{"x": x, "y": y} }
	w.apply(t,
		wireCommand{Type: "node.create", Payload: map[string]any{"id": w.keeper, "board_id": w.harbour,
			"type": "element", "title": "Keeper", "position": at(10.5, 20), "speaker_component_id": w.elara,
			"tags": []string{"Quest"}, "text": richText(textInline("You have "), valueInline(w.trust),
				textInline(" trust with "), mentionInline(w.elara), attributeInline(w.elara, w.fear)),
			"on_enter": "trust += 1\nmood = \"calm\""}},
		wireCommand{Type: "node.create", Payload: map[string]any{"id": w.choice, "board_id": w.harbour,
			"type": "branch", "position": at(200, 20)}},
		wireCommand{Type: "branch_output.create", Payload: map[string]any{"id": w.ifOut, "node_id": w.choice,
			"condition": "trust > 2 && elara.fear < 3"}},
		wireCommand{Type: "branch_output.create", Payload: map[string]any{"id": w.elseOut, "node_id": w.choice,
			"else": true}},
		wireCommand{Type: "node.create", Payload: map[string]any{"id": w.lamp, "board_id": w.harbour,
			"type": "element", "title": "Lamp", "position": at(400, 0)}},
		wireCommand{Type: "node.create", Payload: map[string]any{"id": w.back, "board_id": w.harbour,
			"type": "jumper", "position": at(600, 0), "target": map[string]string{"board_id": w.harbour,
				"node_id": w.keeper}}},
		wireCommand{Type: "node.create", Payload: map[string]any{"id": w.away, "board_id": w.harbour,
			"type": "jumper", "position": at(600, 100), "target": map[string]string{"board_id": w.pier,
				"node_id": w.dock}}},
		wireCommand{Type: "connection.create", Payload: map[string]any{"id": w.toChoice, "from_node_id": w.keeper,
			"to_node_id": w.choice, "label": richText(textInline("Trust: "), valueInline(w.trust)),
			"condition": `visited(node("` + w.keeper + `"))`}},
		wireCommand{Type: "connection.create", Payload: map[string]any{"id": w.ifToLamp, "from_node_id": w.choice,
			"from_port": w.ifOut, "to_node_id": w.lamp}},
		wireCommand{Type: "connection.create", Payload: map[string]any{"id": w.elseToKeeper,
			"from_node_id": w.choice, "from_port": w.elseOut, "to_node_id": w.keeper}},
		wireCommand{Type: "connection.create", Payload: map[string]any{"id": w.lampToBack, "from_node_id": w.lamp,
			"to_node_id": w.back}},
		// A second choice of Keeper, before the first, which the clipboard
		// keeps before it.
		wireCommand{Type: "connection.create", Payload: map[string]any{"id": w.extra, "from_node_id": w.keeper,
			"to_node_id": w.lamp, "order": "1"}})
	return w
}

// apply applies the batch to the world's project as its owner, and returns
// its answer, which must be 200.
func (w *clipboardWorld) apply(t *testing.T, cmds ...wireCommand) commandResultBody {
	t.Helper()
	return w.applyTo(t, w.project, cmds...)
}

// applyTo applies the batch to the project as the world's owner.
func (w *clipboardWorld) applyTo(t *testing.T, project string, cmds ...wireCommand) commandResultBody {
	t.Helper()
	return resultFrom(t, call(w.s.main, http.MethodPost, commandsPath(project), commandBatch(t, cmds...),
		w.owner.header(true)))
}

// copy copies the nodes of the world's project, which must answer 200.
func (w *clipboardWorld) copy(t *testing.T, nodes ...string) clipboard.Clipboard {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"node_ids": nodes})
	res := call(w.s.main, http.MethodPost, copyPath(w.project), body, w.owner.header(true))
	if res.status != http.StatusOK {
		t.Fatalf("copy: %d %s", res.status, res.body)
	}
	var c clipboard.Clipboard
	if err := json.Unmarshal(res.body, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// resolve reports what of c a paste onto the board of the project would
// resolve to nothing, which must answer 200.
func (w *clipboardWorld) resolve(t *testing.T, project, board string, c clipboard.Clipboard) clipboard.ClipboardReport {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"board_id": board, "clipboard": c})
	res := call(w.s.main, http.MethodPost, resolvePath(project), body, w.owner.header(true))
	if res.status != http.StatusOK {
		t.Fatalf("resolve: %d %s", res.status, res.body)
	}
	var r clipboard.ClipboardReport
	if err := json.Unmarshal(res.body, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// paste returns the command that pastes c onto the board, its nodes moved by
// (dx, dy), with new IDs, and the new IDs by the clipboard's.
func paste(board string, c clipboard.Clipboard, dx, dy float64) (wireCommand, map[string]string) {
	newIDs := map[string]string{}
	for _, n := range c.Nodes {
		newIDs[n.ID] = newNodeID()
		for _, o := range n.Outputs {
			newIDs[o.ID] = ids.Encode(ids.BranchOutput, ids.New())
		}
	}
	for _, cn := range c.Connections {
		newIDs[cn.ID] = newConnectionID()
	}
	return wireCommand{Type: "clipboard.paste", Payload: map[string]any{"board_id": board,
		"offset": map[string]any{"x": dx, "y": dy}, "clipboard": c, "ids": newIDs}}, newIDs
}

// pastedNode reads the pasted node id of the project.
func (w *clipboardWorld) pastedNode(t *testing.T, project, id string) node.Node {
	t.Helper()
	return readNode(t, w.s.main, w.owner, project, id)
}

// pastedConnection reads the connection id of the project.
func (w *clipboardWorld) pastedConnection(t *testing.T, project, id string) connection.Connection {
	t.Helper()
	res := call(w.s.main, http.MethodGet, projectPath(project)+"/connections/"+id, nil, w.owner.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("read the connection %s: %d %s", id, res.status, res.body)
	}
	var c connection.Connection
	if err := json.Unmarshal(res.body, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// textJSON returns the document d as its canonical JSON.
func textJSON(t *testing.T, d *richtext.Doc) string {
	t.Helper()
	if d == nil {
		return ""
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestClipboardCopy checks that copying a selection answers with a clipboard
// of the documented structure, of the format's version (FR-EDIT-07 AC3): its
// nodes in the order of their IDs, a branch with its outputs in their order,
// the connections between them alone, in the order of their ports and of each
// port's choices, and a declaration of each variable, component, and
// attribute definition that their texts, expressions, and speakers name.
//
// Verifies: FR-EDIT-07/AC3
func TestClipboardCopy(t *testing.T) {
	t.Parallel()
	w := newClipboardWorld(t)
	c := w.copy(t, w.lamp, w.keeper, w.choice, w.back, w.away)
	if c.Format != clipboard.Format || c.Version != clipboard.Version || c.ProjectID != w.project ||
		c.BoardID != w.harbour {
		t.Errorf("the clipboard is %s %d of %s %s", c.Format, c.Version, c.ProjectID, c.BoardID)
	}
	var nodes []string
	for _, n := range c.Nodes {
		nodes = append(nodes, n.ID)
	}
	want := []string{w.keeper, w.choice, w.lamp, w.back, w.away}
	slices.Sort(want)
	if !slices.Equal(nodes, want) {
		t.Errorf("the clipboard's nodes are %v, want %v", nodes, want)
	}
	for _, n := range c.Nodes {
		if n.ID == w.choice && (len(n.Outputs) != 2 || n.Outputs[0].ID != w.ifOut || n.Outputs[1].ID != w.elseOut ||
			!n.Outputs[1].Else || n.Outputs[0].Condition != "trust > 2 && elara.fear < 3") {
			t.Errorf("the branch's outputs are %+v", n.Outputs)
		}
		if n.ID == w.keeper && (n.OnEnter != "trust += 1\nmood = \"calm\"" || n.SpeakerComponentID != w.elara ||
			n.Position.X.IsZero() || len(n.Text) == 0) {
			t.Errorf("Keeper is %+v", n)
		}
	}
	// Each port's choices are in their order, Keeper's to Lamp before its to
	// Choice, and the ports in the order of their nodes' IDs.
	var connections []string
	ports := map[string][]string{}
	last := ""
	for _, cn := range c.Connections {
		connections = append(connections, cn.ID)
		if cn.FromNodeID < last {
			t.Errorf("the clipboard's connections leave %s after %s", cn.FromNodeID, last)
		}
		last = cn.FromNodeID
		ports[cn.FromNodeID+"/"+cn.FromPort] = append(ports[cn.FromNodeID+"/"+cn.FromPort], cn.ID)
	}
	if got := ports[w.keeper+"/"]; !slices.Equal(got, []string{w.extra, w.toChoice}) {
		t.Errorf("Keeper's choices in the clipboard are %v, want %v", got, []string{w.extra, w.toChoice})
	}
	slices.Sort(connections)
	want = []string{w.extra, w.toChoice, w.ifToLamp, w.elseToKeeper, w.lampToBack}
	slices.Sort(want)
	if !slices.Equal(connections, want) {
		t.Errorf("the clipboard's connections are %v, want %v", connections, want)
	}
	trust, mood := c.Variables[0], c.Variables[1]
	if len(c.Variables) == 2 && trust.ID == w.mood {
		trust, mood = mood, trust
	}
	if len(c.Variables) != 2 || trust.ID != w.trust || trust.Key != "trust" || trust.Scope != "global" ||
		string(trust.Default.JSON()) != "3" || trust.Description == "" || mood.ID != w.mood ||
		mood.BoardID != w.harbour || !mood.ResetOnEnter {
		t.Errorf("the clipboard's variables are %+v", c.Variables)
	}
	if len(c.Components) != 1 || c.Components[0].ID != w.elara || c.Components[0].Key != "elara" ||
		c.Components[0].Type.Key != "hero" || len(c.Components[0].Attributes) != 1 ||
		c.Components[0].Attributes[0].ID != w.fear || !c.Components[0].Attributes[0].Runtime {
		t.Errorf("the clipboard's components are %+v", c.Components)
	}
	// The clipboard is what clipboard.Check takes, and validates against the
	// published schema of its version, with a validator that is not huma's.
	if problems := clipboard.Check(c); len(problems) > 0 {
		t.Errorf("the clipboard's problems: %v", problems)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(clipboard.Schema()))
	if err != nil {
		t.Fatal(err)
	}
	if err := compiler.AddResource(clipboard.SchemaID, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(clipboard.SchemaID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"node_ids": []string{w.lamp, w.keeper, w.choice, w.back, w.away}})
	res := call(w.s.main, http.MethodPost, copyPath(w.project), body, w.owner.header(true))
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(res.body))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(inst); err != nil {
		t.Errorf("the copy does not validate against %s: %v", clipboard.SchemaPath, err)
	}

	for _, bad := range []struct {
		nodes []string
		at    string
	}{
		{[]string{w.keeper, w.dock}, "body.node_ids[1]"},
		{[]string{w.keeper, newNodeID()}, "body.node_ids[1]"},
		{[]string{w.keeper, w.keeper}, "body.node_ids[1]"},
		{[]string{"bd_nope"}, "body.node_ids[0]"},
	} {
		body, _ := json.Marshal(map[string]any{"node_ids": bad.nodes})
		res := call(w.s.main, http.MethodPost, copyPath(w.project), body, w.owner.header(true))
		wantCode(t, "a copy of "+strings.Join(bad.nodes, ", "), res, http.StatusUnprocessableEntity,
			"validation_failed")
		if !strings.Contains(string(res.body), bad.at) {
			t.Errorf("a copy of %v: %s, want a problem at %s", bad.nodes, res.body, bad.at)
		}
	}
}

// TestClipboardPaste checks that a paste onto the board that the clipboard
// was copied from creates copies of its nodes, outputs, and connections, at
// their places moved by the offset, with every reference that the board
// resolves kept, those between the copied nodes made the copies', and the
// choices in their order; that its undo deletes the copies, and its redo
// brings them back; and that a paste onto another board of the project
// writes a name of a variable of the board copied from that the board pasted
// onto lacks explicitly, which the report lists.
func TestClipboardPaste(t *testing.T) {
	t.Parallel()
	w := newClipboardWorld(t)
	c := w.copy(t, w.keeper, w.choice, w.lamp, w.back, w.away)

	cmd, newIDs := paste(w.harbour, c, 100, -20.25)
	done := w.apply(t, cmd)
	keeper := w.pastedNode(t, w.project, newIDs[w.keeper])
	if keeper.BoardID != w.harbour || keeper.Title != "Keeper" || keeper.SpeakerComponentID != w.elara ||
		!slices.Equal(keeper.Tags, []string{"Quest"}) || keeper.OnEnter == nil ||
		keeper.OnEnter.Source != "trust += 1\nmood = \"calm\"" || !keeper.OnEnter.IsValid {
		t.Errorf("the pasted Keeper is %+v", keeper)
	}
	if got, _ := json.Marshal(keeper.Position); string(got) != `{"x":110.5,"y":-0.25}` {
		t.Errorf("the pasted Keeper is at %s, want 110.5, -0.25", got)
	}
	original := w.pastedNode(t, w.project, w.keeper)
	if textJSON(t, keeper.Text) != textJSON(t, original.Text) {
		t.Errorf("the pasted Keeper's text is %s, want %s", textJSON(t, keeper.Text), textJSON(t, original.Text))
	}
	choice := w.pastedNode(t, w.project, newIDs[w.choice])
	if len(choice.Outputs) != 2 || choice.Outputs[0].ID != newIDs[w.ifOut] || choice.Outputs[0].Kind != "if" ||
		choice.Outputs[1].ID != newIDs[w.elseOut] || choice.Outputs[1].Kind != "else" ||
		!choice.Outputs[0].Condition.IsValid {
		t.Errorf("the pasted branch's outputs are %+v", choice.Outputs)
	}
	if back := w.pastedNode(t, w.project, newIDs[w.back]); back.Target == nil ||
		back.Target.NodeID != newIDs[w.keeper] {
		t.Errorf("the pasted Back targets %+v, want the pasted Keeper", back.Target)
	}
	if away := w.pastedNode(t, w.project, newIDs[w.away]); away.Target == nil || away.Target.NodeID != w.dock {
		t.Errorf("the pasted Away targets %+v, want Dock", away.Target)
	}
	toChoice := w.pastedConnection(t, w.project, newIDs[w.toChoice])
	if toChoice.FromNodeID != newIDs[w.keeper] || toChoice.ToNodeID != newIDs[w.choice] ||
		toChoice.Condition == nil || toChoice.Condition.Source != `visited(node("`+newIDs[w.keeper]+`"))` ||
		!toChoice.Condition.IsValid {
		t.Errorf("the pasted connection to Choice is %+v", toChoice)
	}
	extra := w.pastedConnection(t, w.project, newIDs[w.extra])
	if extra.Order >= toChoice.Order {
		t.Errorf("the pasted choices are in the order %s, %s, want Lamp's first", extra.Order, toChoice.Order)
	}
	if ifToLamp := w.pastedConnection(t, w.project, newIDs[w.ifToLamp]); ifToLamp.FromPort != newIDs[w.ifOut] {
		t.Errorf("the pasted connection of the if leaves %s", ifToLamp.FromPort)
	}
	// The undo is one deletion of the pasted nodes, which takes their
	// outputs and connections with them, and its own undo brings them back.
	if len(done.Undo) != 1 || done.Undo[0].Type != "node.delete_many" {
		t.Fatalf("the paste's undo is %+v", done.Undo)
	}
	undone := w.apply(t, undoCommands(done.Undo)...)
	if res := call(w.s.main, http.MethodGet, projectPath(w.project)+"/nodes/"+newIDs[w.keeper], nil,
		w.owner.header(false)); res.status != http.StatusNotFound {
		t.Errorf("the pasted Keeper once undone: %d, want 404", res.status)
	}
	w.apply(t, undoCommands(undone.Undo)...)
	if c := w.pastedConnection(t, w.project, newIDs[w.toChoice]); c.ID != newIDs[w.toChoice] {
		t.Errorf("the redone connection is %+v", c)
	}

	// Pier has no mood, which the report lists, and which the paste writes
	// explicitly, so that it names none but Pier's own once Pier has one.
	if r := w.resolve(t, w.project, w.pier, c); len(r.Variables) != 1 || r.Variables[0].ID != w.mood ||
		len(r.Components) != 0 || len(r.Nodes) != 0 {
		t.Errorf("the report of a paste onto Pier is %+v", r)
	}
	cmd, newIDs = paste(w.pier, c, 0, 0)
	w.apply(t, cmd)
	if keeper := w.pastedNode(t, w.project, newIDs[w.keeper]); keeper.OnEnter == nil ||
		keeper.OnEnter.Source != "trust += 1\nboard.mood = \"calm\"" || keeper.OnEnter.IsValid {
		t.Errorf("Keeper pasted onto Pier enters with %+v", keeper.OnEnter)
	}
	if back := w.pastedNode(t, w.project, newIDs[w.back]); back.Target == nil ||
		back.Target.BoardID != w.pier || back.Target.NodeID != newIDs[w.keeper] {
		t.Errorf("Back pasted onto Pier targets %+v", back.Target)
	}
}

// TestClipboardPasteRescoped checks that a paste resolves a variable whose
// scope has changed since the copy to the same variable, where the board
// reaches it, and writes a name of it whose qualifier a comment follows,
// which the name keeps, with the qualifier of the variable's new scope, so
// that it names the variable, rather than one of its old scope.
func TestClipboardPasteRescoped(t *testing.T) {
	t.Parallel()
	w := newClipboardWorld(t)
	note := createNode(t, w.s.main, w.owner, w.project, w.harbour, "element", map[string]any{
		"on_enter": "trust = global // the keeper's\n  .trust + 1"})
	c := w.copy(t, note)
	w.apply(t, wireCommand{Type: "variable.set_scope", Payload: map[string]any{"id": w.trust, "scope": "board",
		"board_id": w.harbour}})
	if r := w.resolve(t, w.project, w.harbour, c); len(r.Variables) != 0 {
		t.Errorf("the report of a variable rescoped since the copy is %+v, want none", r)
	}
	cmd, newIDs := paste(w.harbour, c, 0, 0)
	w.apply(t, cmd)
	if got := w.pastedNode(t, w.project, newIDs[note]); got.OnEnter == nil ||
		got.OnEnter.Source != "trust = board // the keeper's\n  .trust + 1" || !got.OnEnter.IsValid {
		t.Errorf("the pasted note enters with %+v, want board // the keeper's .trust", got.OnEnter)
	}
}

// TestClipboardAcrossProjects checks that a paste into another project of the
// workspace resolves what its content names by its keys, reports what it
// resolves to nothing, with what creating it takes, once for what a client
// creates in its place, two variables of one key of two boards once, and an
// attribute definition that the type that a component that resolves to none
// would be created of lacks with that type, keeps a text's reference to it as
// it is, writes an expression's name of it explicitly, and leaves out a
// speaker and a jumper's target that it lacks; and that a client that creates
// what the report lists in the batch of the paste, before it, has the paste
// resolve to it (FR-EDIT-07 AC1).
//
// Verifies: FR-EDIT-07/AC1
func TestClipboardAcrossProjects(t *testing.T) {
	t.Parallel()
	w := newClipboardWorld(t)
	// Keeper's text shows Pier's mood too, which the clipboard declares with
	// Harbour's, and which a paste resolves to the mood of the board that it
	// pastes onto, as it resolves Harbour's.
	pierMood := newVariableID()
	w.apply(t, wireCommand{Type: "variable.create", Payload: map[string]any{"id": pierMood, "key": "mood",
		"type": "string", "scope": "board", "board_id": w.pier}},
		wireCommand{Type: "node.update", Payload: map[string]any{"id": w.keeper, "text": richText(
			textInline("You have "), valueInline(w.trust), textInline(" trust with "), mentionInline(w.elara),
			attributeInline(w.elara, w.fear), valueInline(pierMood))}})
	c := w.copy(t, w.keeper, w.choice, w.lamp, w.back, w.away)
	if len(c.Variables) != 3 {
		t.Fatalf("the clipboard's variables are %+v, want trust and both moods", c.Variables)
	}
	other := createProject(t, w.s.main, w.owner, w.ws, "Lantern").ID
	board := createBoard(t, w.s.main, w.owner, other, "Shore")
	character := ids.Encode(ids.ComponentType, ids.New())
	w.applyTo(t, other, wireCommand{Type: "component_type.create", Payload: map[string]any{"id": character,
		"name": "Person", "key": "hero"}})

	r := w.resolve(t, other, board, c)
	keys := []string{}
	for _, v := range r.Variables {
		keys = append(keys, v.Key)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"mood", "trust"}) ||
		len(r.Components) != 1 || r.Components[0].Component.Key != "elara" ||
		r.Components[0].ComponentTypeID != character || !slices.Equal(r.Nodes, []string{w.dock}) ||
		len(r.Assets) != 0 {
		t.Errorf("the report is %+v", r)
	}
	// Dock, written two ways, as a client may write an ID without regard to
	// case, by Away's target and by a node reference of a condition, is
	// listed once.
	spelled := c
	spelled.Nodes, spelled.Connections = slices.Clone(c.Nodes), slices.Clone(c.Connections)
	for i, n := range spelled.Nodes {
		if n.ID == w.away {
			target := *n.Target
			target.NodeID = strings.ToLower(target.NodeID)
			spelled.Nodes[i].Target = &target
		}
	}
	for i, cn := range spelled.Connections {
		if cn.ID == w.lampToBack {
			spelled.Connections[i].Condition = `visited(node("` + w.dock + `"))`
		}
	}
	if r := w.resolve(t, other, board, spelled); len(r.Nodes) != 1 || !strings.EqualFold(r.Nodes[0], w.dock) {
		t.Errorf("the report of Dock written two ways lists the nodes %v", r.Nodes)
	}
	// Lantern's hero lacks fear, which a component created in Elara's place
	// is of, and which the report lists with it, with what creating it takes.
	if len(r.Attributes) != 1 || r.Attributes[0].ComponentTypeID != character ||
		r.Attributes[0].Attribute.ID != w.fear || r.Attributes[0].Attribute.Name != "Fear" ||
		r.Attributes[0].Attribute.Type != "int" || !r.Attributes[0].Attribute.Runtime ||
		string(r.Attributes[0].Attribute.Default.JSON()) != "0" {
		t.Errorf("the report's attribute definitions are %+v", r.Attributes)
	}

	// Pasted as it is, what it names resolves to nothing.
	cmd, newIDs := paste(board, c, 0, 0)
	w.applyTo(t, other, cmd)
	keeper := w.pastedNode(t, other, newIDs[w.keeper])
	if keeper.SpeakerComponentID != "" || keeper.OnEnter == nil ||
		keeper.OnEnter.Source != "global.trust += 1\nboard.mood = \"calm\"" || keeper.OnEnter.IsValid ||
		!strings.Contains(textJSON(t, keeper.Text), w.trust) {
		t.Errorf("Keeper pasted as it is: %+v, with the text %s", keeper, textJSON(t, keeper.Text))
	}
	choice := w.pastedNode(t, other, newIDs[w.choice])
	if choice.Outputs[0].Condition.Source != "global.trust > 2 && cmp.elara.fear < 3" {
		t.Errorf("the pasted if's condition is %q", choice.Outputs[0].Condition.Source)
	}
	if away := w.pastedNode(t, other, newIDs[w.away]); away.Target != nil {
		t.Errorf("Away pasted into another project targets %+v, want none", away.Target)
	}

	// Pasted with what is missing created first, it resolves to that.
	trust, mood, elara := newVariableID(), newVariableID(), newComponentID()
	create := func(v clipboard.ClipboardVariable, id string) wireCommand {
		p := map[string]any{"id": id, "key": v.Key, "type": v.Type, "scope": v.Scope, "default": v.Default,
			"description": v.Description}
		if v.Scope == "board" {
			p["board_id"], p["reset_on_enter"] = board, v.ResetOnEnter
		}
		return wireCommand{Type: "variable.create", Payload: p}
	}
	fear, a := ids.Encode(ids.AttributeDef, ids.New()), r.Attributes[0]
	cmd, newIDs = paste(board, c, 0, 300)
	newVariables := map[string]string{"trust": trust, "mood": mood}
	w.applyTo(t, other, create(r.Variables[0], newVariables[r.Variables[0].Key]),
		create(r.Variables[1], newVariables[r.Variables[1].Key]),
		wireCommand{Type: "attribute.create", Payload: map[string]any{"id": fear,
			"component_type_id": a.ComponentTypeID, "name": a.Attribute.Name, "key": a.Attribute.Key,
			"type": a.Attribute.Type, "runtime": a.Attribute.Runtime, "default": a.Attribute.Default}},
		wireCommand{Type: "component.create", Payload: map[string]any{"id": elara,
			"component_type_id": r.Components[0].ComponentTypeID, "name": r.Components[0].Component.Name,
			"key": r.Components[0].Component.Key}},
		cmd)
	keeper = w.pastedNode(t, other, newIDs[w.keeper])
	text := textJSON(t, keeper.Text)
	if keeper.SpeakerComponentID != elara || keeper.OnEnter == nil ||
		keeper.OnEnter.Source != "trust += 1\nmood = \"calm\"" || !keeper.OnEnter.IsValid ||
		!strings.Contains(text, trust) || strings.Contains(text, w.trust) || !strings.Contains(text, fear) ||
		!strings.Contains(text, mood) || strings.Contains(text, pierMood) {
		t.Errorf("Keeper pasted with what it names created: %+v, with the text %s", keeper, text)
	}
	if choice := w.pastedNode(t, other, newIDs[w.choice]); !choice.Outputs[0].Condition.IsValid ||
		choice.Outputs[0].Condition.Source != "trust > 2 && elara.fear < 3" {
		t.Errorf("the pasted if's condition is %+v", choice.Outputs[0].Condition)
	}
}

// TestClipboardResolvesEachComponent checks that a paste resolves the
// attribute definition that two components of one type share for each of
// them apart, where one resolves and the other does not, so that the report
// lists no definition that the one that resolves has, and its text's value
// reference names its definition, and where both resolve to components of a
// type that lacks it, which the report lists once, with that type, so that a
// client creates it once; that an expression's attribute is written
// with the key of the definition that it resolves to, which a rename since
// the copy changed; that a speaker in the trash where it was copied, which
// the clipboard does not declare, is kept where the project has it, and
// listed where it does not; and that a component that resolves to none is
// listed with its own type, whose key changed since the copy.
func TestClipboardResolvesEachComponent(t *testing.T) {
	t.Parallel()
	w := newClipboardWorld(t)
	mira, ghost, pair := newComponentID(), newComponentID(), newNodeID()
	w.apply(t,
		wireCommand{Type: "component.create", Payload: map[string]any{"id": mira, "component_type_id": w.character,
			"name": "Mira", "key": "mira"}},
		wireCommand{Type: "component.create", Payload: map[string]any{"id": ghost, "component_type_id": w.character,
			"name": "Ghost", "key": "ghost"}},
		wireCommand{Type: "node.create", Payload: map[string]any{"id": pair, "board_id": w.harbour,
			"type": "element", "title": "Pair", "position": map[string]any{"x": 0, "y": 400},
			"speaker_component_id": ghost, "text": richText(attributeInline(w.elara, w.fear),
				attributeInline(mira, w.fear)), "on_enter": "elara.fear += 1"}})
	w.apply(t, wireCommand{Type: "component.delete", Payload: map[string]any{"id": ghost, "force": true}})
	c := w.copy(t, pair)
	// Elara comes before Mira, so that Mira's definition, which resolves to
	// none in Lantern, would take the place of Elara's if they shared one.
	if len(c.Components) != 2 || c.Components[0].ID != w.elara || c.Components[1].ID != mira ||
		len(c.Components[0].Attributes) != 1 || len(c.Components[1].Attributes) != 1 {
		t.Fatalf("the clipboard's components are %+v", c.Components)
	}

	// Renamed since the copy, the definition gives the pasted script its key,
	// and the speaker in the trash is kept, as the project has it.
	w.apply(t, wireCommand{Type: "attribute.set_key", Payload: map[string]any{"id": w.fear, "key": "dread"}})
	if r := w.resolve(t, w.project, w.harbour, c); len(r.Components) != 0 || len(r.Attributes) != 0 ||
		len(r.Speakers) != 0 {
		t.Errorf("the report of a paste onto Harbour is %+v", r)
	}
	cmd, newIDs := paste(w.harbour, c, 0, 0)
	w.apply(t, cmd)
	if pasted := w.pastedNode(t, w.project, newIDs[pair]); pasted.OnEnter == nil ||
		pasted.OnEnter.Source != "elara.dread += 1" || !pasted.OnEnter.IsValid || pasted.SpeakerComponentID != ghost {
		t.Errorf("Pair pasted onto Harbour is %+v, entering with %+v", pasted, pasted.OnEnter)
	}

	// In Lantern, Elara resolves, with her fear, and Mira and the speaker do
	// not.
	other := createProject(t, w.s.main, w.owner, w.ws, "Lantern").ID
	board := createBoard(t, w.s.main, w.owner, other, "Shore")
	hero, fear, elara := ids.Encode(ids.ComponentType, ids.New()), ids.Encode(ids.AttributeDef, ids.New()),
		newComponentID()
	w.applyTo(t, other,
		wireCommand{Type: "component_type.create", Payload: map[string]any{"id": hero, "name": "Hero",
			"key": "hero"}},
		wireCommand{Type: "attribute.create", Payload: map[string]any{"id": fear, "component_type_id": hero,
			"name": "Fear", "key": "fear", "type": "int", "runtime": true}},
		wireCommand{Type: "component.create", Payload: map[string]any{"id": elara, "component_type_id": hero,
			"name": "Elara", "key": "elara"}})
	if r := w.resolve(t, other, board, c); len(r.Components) != 1 || r.Components[0].Component.ID != mira ||
		r.Components[0].ComponentTypeID != hero || len(r.Attributes) != 0 ||
		!slices.Equal(r.Speakers, []string{ghost}) {
		t.Errorf("the report of a paste into Lantern is %+v", r)
	}
	cmd, newIDs = paste(board, c, 0, 0)
	w.applyTo(t, other, cmd)
	pasted := w.pastedNode(t, other, newIDs[pair])
	text := textJSON(t, pasted.Text)
	if pasted.SpeakerComponentID != "" || pasted.OnEnter == nil || pasted.OnEnter.Source != "elara.fear += 1" ||
		!pasted.OnEnter.IsValid || !strings.Contains(text, elara) || !strings.Contains(text, fear) ||
		strings.Contains(text, w.elara) || !strings.Contains(text, mira) {
		t.Errorf("Pair pasted into Lantern is %+v, with the text %s", pasted, text)
	}

	// In Beacon, Elara and Mira resolve to components of a type without
	// their fear, which the report lists once, with that type, and which,
	// created as the report lists it, both resolve to.
	beacon := createProject(t, w.s.main, w.owner, w.ws, "Beacon").ID
	cove := createBoard(t, w.s.main, w.owner, beacon, "Cove")
	person, beaconElara, beaconMira := ids.Encode(ids.ComponentType, ids.New()), newComponentID(),
		newComponentID()
	w.applyTo(t, beacon,
		wireCommand{Type: "component_type.create", Payload: map[string]any{"id": person, "name": "Person",
			"key": "person"}},
		wireCommand{Type: "component.create", Payload: map[string]any{"id": beaconElara, "component_type_id": person,
			"name": "Elara", "key": "elara"}},
		wireCommand{Type: "component.create", Payload: map[string]any{"id": beaconMira, "component_type_id": person,
			"name": "Mira", "key": "mira"}})
	r := w.resolve(t, beacon, cove, c)
	if len(r.Components) != 0 || len(r.Attributes) != 1 || r.Attributes[0].ComponentTypeID != person ||
		r.Attributes[0].Attribute.ID != w.fear || r.Attributes[0].Attribute.Name != "Fear" {
		t.Fatalf("the report of a paste into Beacon is %+v", r)
	}
	beaconFear, a := ids.Encode(ids.AttributeDef, ids.New()), r.Attributes[0]
	cmd, newIDs = paste(cove, c, 0, 0)
	w.applyTo(t, beacon, wireCommand{Type: "attribute.create", Payload: map[string]any{"id": beaconFear,
		"component_type_id": a.ComponentTypeID, "name": a.Attribute.Name, "key": a.Attribute.Key,
		"type": a.Attribute.Type, "runtime": a.Attribute.Runtime, "default": a.Attribute.Default}}, cmd)
	pasted = w.pastedNode(t, beacon, newIDs[pair])
	text = textJSON(t, pasted.Text)
	if pasted.OnEnter == nil || pasted.OnEnter.Source != "elara.fear += 1" || !pasted.OnEnter.IsValid ||
		!strings.Contains(text, beaconElara) || !strings.Contains(text, beaconMira) ||
		strings.Count(text, beaconFear) != 2 || strings.Contains(text, w.fear) {
		t.Errorf("Pair pasted into Beacon is %+v, with the text %s", pasted, text)
	}

	// Mira, once in the trash, resolves to none, and is listed with her own
	// type, whose key is no longer the one that the clipboard declares.
	w.apply(t, wireCommand{Type: "component.delete", Payload: map[string]any{"id": mira, "force": true}},
		wireCommand{Type: "component_type.set_key", Payload: map[string]any{"id": w.character, "key": "person"}})
	if r := w.resolve(t, w.project, w.harbour, c); len(r.Components) != 1 || r.Components[0].Component.ID != mira ||
		r.Components[0].ComponentTypeID != w.character {
		t.Errorf("the report of a paste onto Harbour once Mira is in the trash is %+v", r)
	}
}

// TestClipboardPasteRefused checks that a paste is refused, and changes
// nothing, for a clipboard of a newer version, which holds a field that this
// version does not, at its version, for new IDs that lack one of its entities
// or name one twice, for a board in the trash, for an offset that takes a
// node beyond the bounds of a position, and for a script that the names that
// it writes make longer than a source may be, each at its field; and that the
// report is refused for a clipboard of a newer version, at its version, and
// for a node that node.create would refuse.
func TestClipboardPasteRefused(t *testing.T) {
	t.Parallel()
	w := newClipboardWorld(t)
	c := w.copy(t, w.keeper, w.choice)
	// A newer clipboard holds what this version does not, as a field of a
	// node, which the schema refuses, at that field, with its version.
	var newer map[string]any
	data, _ := json.Marshal(c)
	if err := json.Unmarshal(data, &newer); err != nil {
		t.Fatal(err)
	}
	newer["version"] = clipboard.Version + 1
	newer["nodes"].([]any)[0].(map[string]any)["locked"] = true
	for _, bad := range []struct {
		name string
		make func() wireCommand
		at   string
	}{
		{"a newer version", func() wireCommand {
			cmd, _ := paste(w.harbour, c, 0, 0)
			cmd.Payload.(map[string]any)["clipboard"] = newer
			return cmd
		}, "payload.clipboard.version"},
		{"a missing ID", func() wireCommand {
			cmd, newIDs := paste(w.harbour, c, 0, 0)
			delete(newIDs, w.ifOut)
			return cmd
		}, "payload.ids"},
		{"an ID twice", func() wireCommand {
			cmd, newIDs := paste(w.harbour, c, 0, 0)
			newIDs[w.choice] = newIDs[w.keeper]
			return cmd
		}, "payload.ids."},
		{"a position beyond the bounds", func() wireCommand { cmd, _ := paste(w.harbour, c, 1e9, 0); return cmd },
			"payload.clipboard.nodes[0].position.x"},
		{"a board of another project", func() wireCommand {
			cmd, _ := paste(createBoard(t, w.s.main, w.owner, createProject(t, w.s.main, w.owner, w.ws, "Other").ID,
				"Elsewhere"), c, 0, 0)
			return cmd
		}, "payload.board_id"},
	} {
		res := call(w.s.main, http.MethodPost, commandsPath(w.project), commandBatch(t, bad.make()),
			w.owner.header(true))
		wantCode(t, bad.name, res, http.StatusUnprocessableEntity, "validation_failed")
		if !strings.Contains(string(res.body), bad.at) {
			t.Errorf("%s: %s, want a problem at %s", bad.name, res.body, bad.at)
		}
	}
	body, _ := json.Marshal(map[string]any{"board_id": w.harbour, "clipboard": newer})
	res := call(w.s.main, http.MethodPost, resolvePath(w.project), body, w.owner.header(true))
	wantCode(t, "the report of a newer version", res, http.StatusUnprocessableEntity, "validation_failed")
	if !strings.Contains(string(res.body), "body.clipboard.version") {
		t.Errorf("the report of a newer version: %s", res.body)
	}

	// A node that node.create would refuse, as one of a size without a
	// height, is refused by the report too, at its field.
	sized := c
	sized.Nodes = slices.Clone(c.Nodes)
	sized.Nodes[0].Size = &node.NodeSize{}
	if err := json.Unmarshal([]byte(`{"width":5}`), sized.Nodes[0].Size); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(map[string]any{"board_id": w.harbour, "clipboard": sized})
	res = call(w.s.main, http.MethodPost, resolvePath(w.project), body, w.owner.header(true))
	wantCode(t, "the report of a size without a height", res, http.StatusUnprocessableEntity, "validation_failed")
	if !strings.Contains(string(res.body), "body.clipboard.nodes[0].size.height") {
		t.Errorf("the report of a size without a height: %s", res.body)
	}

	// A script whose names resolve to nothing in another project, which the
	// paste writes explicitly, longer than a source may be once they are, is
	// refused at its field of the clipboard, whose own source is not.
	long := c
	long.Nodes = slices.Clone(c.Nodes)
	keeper := slices.IndexFunc(long.Nodes, func(n clipboard.ClipboardNode) bool { return n.ID == w.keeper })
	long.Nodes[keeper].OnEnter = strings.Repeat(`mood = "`+strings.Repeat("x", 4000)+"\"\n", 4) +
		strings.Repeat("trust += 1\n", 31)
	other := createProject(t, w.s.main, w.owner, w.ws, "Far").ID
	cmd, _ := paste(createBoard(t, w.s.main, w.owner, other, "Shore"), long, 0, 0)
	res = call(w.s.main, http.MethodPost, commandsPath(other), commandBatch(t, cmd), w.owner.header(true))
	wantCode(t, "a script too long once pasted", res, http.StatusUnprocessableEntity, "validation_failed")
	if at := fmt.Sprintf("payload.clipboard.nodes[%d].on_enter", keeper); !strings.Contains(string(res.body), at) ||
		!strings.Contains(string(res.body), "would have") {
		t.Errorf("a script too long once pasted: %s, want a problem at %s", res.body, at)
	}
}
