package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
)

func validationPath(project string) string { return projectPath(project) + "/validation" }

// validationReport returns the report of the project's validation, as the
// signed-in user reads it.
func validationReport(t *testing.T, h http.Handler, si signedIn, project string) validation.Report {
	t.Helper()
	res := call(h, http.MethodGet, validationPath(project), nil, si.header(false))
	var r validation.Report
	if res.status != http.StatusOK || json.Unmarshal(res.body, &r) != nil {
		t.Fatalf("validate %s: %d %s", project, res.status, res.body)
	}
	return r
}

// findingLines returns the findings as lines of their codes, entities,
// fields, and targets, by names that names gives the IDs, which the tests
// compare as sets.
func findingLines(findings []validation.Finding, names map[string]string) []string {
	name := func(id string) string {
		if n, ok := names[id]; ok {
			return n
		}
		return id
	}
	out := make([]string, len(findings))
	for i, f := range findings {
		out[i] = string(f.Code) + " " + f.EntityType + ":" + name(f.EntityID)
		if f.Field != "" {
			out[i] += "." + f.Field
		}
		if f.Target != nil {
			out[i] += " → " + f.Target.Type + ":" + name(f.Target.ID)
		}
	}
	slices.Sort(out)
	return out
}

// sendBatch sends the commands as one batch of the project, as the user,
// and returns its result, or fails the test.
func sendBatch(t *testing.T, h http.Handler, si signedIn, project string, cmds ...wireCommand) commandResultBody {
	t.Helper()
	return resultFrom(t, call(h, http.MethodPost, commandsPath(project), commandBatch(t, cmds...), si.header(true)))
}

// TestValidationReport checks the report of a project of which each rule
// finds something, built through the command endpoint, as a client builds
// it: each finding of the rules, once, about the entity that it is about,
// with the board and the node that a client opens it by (AC3), sorted
// errors first, and counted; and the report of a project of which none
// finds anything, which is empty. A board's deletion leaves a jumper that
// leads to it, and the project's entry, dangling, and the validation reports
// them (FR-PROJ-02 AC2).
//
// Verifies: FR-DEP-03/AC1, FR-PROJ-02/AC2
func TestValidationReport(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Harbour").ID
	if r := validationReport(t, h, owner, p); len(r.Findings) != 0 || len(r.Muted) != 0 ||
		r.Counts != (validation.Counts{}) {
		t.Errorf("the empty project's report is %+v, want no finding", r)
	}

	names := map[string]string{p: "project"}
	id := func(kind ids.Kind, name string) string {
		v := ids.Encode(kind, ids.New())
		names[v] = name
		return v
	}
	harbour, lighthouse, mill, nook := id(ids.Board, "harbour"), id(ids.Board, "lighthouse"), id(ids.Board, "mill"),
		id(ids.Board, "nook")
	seen, gold, doomed := id(ids.Variable, "seen"), id(ids.Variable, "gold"), id(ids.Variable, "doomed")
	cast, ghost := id(ids.ComponentType, "cast"), id(ids.Component, "ghost")
	pos := map[string]int{"x": 0, "y": 0}
	element := func(n, b, text string, more ...any) wireCommand {
		p := map[string]any{"id": n, "board_id": b, "type": "element", "kind": "narration", "position": pos}
		if text != "" {
			p["text"] = paragraph(text)
		}
		for i := 0; i < len(more); i += 2 {
			p[more[i].(string)] = more[i+1]
		}
		return cmd("node.create", p)
	}
	typed := func(n, b, typ string, more ...any) wireCommand {
		p := map[string]any{"id": n, "board_id": b, "type": typ, "position": pos}
		for i := 0; i < len(more); i += 2 {
			p[more[i].(string)] = more[i+1]
		}
		return cmd("node.create", p)
	}
	connect := func(c, from, to, label string, more ...any) wireCommand {
		p := map[string]any{"id": c, "from_node_id": from, "to_node_id": to}
		if label != "" {
			p["label"] = paragraph(label)
		}
		for i := 0; i < len(more); i += 2 {
			p[more[i].(string)] = more[i+1]
		}
		return cmd("connection.create", p)
	}
	n := func(name string) string { return id(ids.Node, name) }
	c := func(name string) string { return id(ids.Connection, name) }
	start, end, bad, dead, empty, talk, talk2, island := n("start"), n("end"), n("bad"), n("dead"), n("empty"),
		n("talk"), n("talk2"), n("island")
	br, br0, j1, j2, j3, jd, gone, a, b, nookStart := n("br"), n("br0"), n("j1"), n("j2"), n("j3"), n("jd"),
		n("gone"), n("a"), n("b"), n("nook_start")
	c1, c2, c3 := c("c1"), c("c2"), c("c3")
	o1, o2 := id(ids.BranchOutput, "o1"), id(ids.BranchOutput, "o2")
	sendBatch(t, h, owner, p,
		cmd("board.create", map[string]any{"id": harbour, "name": "Harbour"}),
		cmd("board.create", map[string]any{"id": lighthouse, "name": "Lighthouse"}),
		cmd("board.create", map[string]any{"id": mill, "name": "Mill"}),
		cmd("board.create", map[string]any{"id": nook, "name": "Nook"}),
		cmd("variable.create", map[string]any{"id": seen, "key": "seen", "type": "bool", "scope": "global"}),
		cmd("variable.create", map[string]any{"id": gold, "key": "gold", "type": "int", "scope": "global"}),
		cmd("variable.create", map[string]any{"id": doomed, "key": "doomed", "type": "int", "scope": "global"}),
		cmd("component_type.create", map[string]any{"id": cast, "name": "Cast"}),
		cmd("component.create", map[string]any{"id": ghost, "component_type_id": cast, "name": "Ghost"}),
		// Harbour: start, its entry, whose text shows doomed, leads to end,
		// an ending, by c1, whose condition reads seen and whose script
		// writes gold, and by c2, the same choice, with the same empty label
		// and no condition, and to itself by c3.
		element(start, harbour, "", "text", richText(textInline("Hello "), valueInline(doomed))),
		element(end, harbour, "Bye", "is_ending", true),
		connect(c1, start, end, "", "condition", "seen", "on_traverse", "gold = 1"),
		connect(c2, start, end, ""),
		connect(c3, start, start, "Again"),
		// An element whose script does not compile, one that no connection
		// leaves, one without text, a line of dialogue without a speaker,
		// one whose speaker is deleted below, and an ending that nothing
		// reaches.
		element(bad, harbour, "Bad", "on_enter", "nope = 1"),
		connect(c("bad_in"), start, bad, "Bad"), connect(c("bad_out"), bad, end, ""),
		element(dead, harbour, "Dead"), connect(c("dead_in"), start, dead, "Dead"),
		element(empty, harbour, "", "is_ending", true), connect(c("empty_in"), start, empty, "Empty"),
		element(talk, harbour, "Hi", "kind", "dialogue", "is_ending", true), connect(c("talk_in"), start, talk, "Talk"),
		element(talk2, harbour, "Boo", "kind", "dialogue", "is_ending", true, "speaker_component_id", ghost),
		connect(c("talk2_in"), start, talk2, "Talk2"),
		element(island, harbour, "Island", "is_ending", true),
		// A branch whose output o2 no connection leaves, and one without
		// outputs.
		typed(br, harbour, "branch"), connect(c("br_in"), start, br, "Branch"),
		cmd("branch_output.create", map[string]any{"id": o1, "node_id": br, "condition": "true"}),
		cmd("branch_output.create", map[string]any{"id": o2, "node_id": br, "condition": "true"}),
		cmd("connection.create", map[string]any{"id": c("o1_out"), "from_node_id": br, "from_port": o1,
			"to_node_id": end}),
		typed(br0, harbour, "branch"), connect(c("br0_in"), start, br0, "Branch0"),
		// A jumper without a target, one that leads to a jumper, and one
		// that leads to a node of Lighthouse, which is deleted below.
		typed(j1, harbour, "jumper"), connect(c("j1_in"), start, j1, "J1"),
		typed(j3, harbour, "jumper", "target", map[string]any{"board_id": harbour, "node_id": end}),
		typed(j2, harbour, "jumper", "target", map[string]any{"board_id": harbour, "node_id": j3}),
		connect(c("j2_in"), start, j2, "J2"),
		element(gone, lighthouse, "Gone", "is_ending", true),
		typed(jd, harbour, "jumper", "target", map[string]any{"board_id": lighthouse, "node_id": gone}),
		connect(c("jd_in"), start, jd, "JD"),
		cmd("board.set_entry_node", map[string]any{"id": harbour, "node_id": start}),
		// Mill has no entry node, and two roots.
		element(a, mill, "A", "is_ending", true), element(b, mill, "B", "is_ending", true),
		// Nook's entry node is deleted below.
		element(nookStart, nook, "Nook", "is_ending", true),
		cmd("board.set_entry_node", map[string]any{"id": nook, "node_id": nookStart}),
		cmd("project.update_settings", map[string]any{"entry": map[string]any{"board_id": lighthouse,
			"node_id": gone}}),
	)
	sendBatch(t, h, owner, p,
		cmd("variable.delete", map[string]any{"id": doomed, "force": true}),
		cmd("component.delete", map[string]any{"id": ghost, "force": true}),
		cmd("node.delete", map[string]any{"id": nookStart}),
		cmd("board.delete", map[string]any{"id": lighthouse, "confirm": true}),
	)

	r := validationReport(t, h, owner, p)
	want := []string{
		"board_entry_missing board:mill",
		"branch_no_outputs node:br0",
		"choice_duplicate_label connection:c2 → connection:c1",
		"connection_parallel connection:c2 → connection:c1",
		"connection_self connection:c3",
		"dead_end node:dead",
		"expr_invalid node:bad.on_enter",
		"jumper_broken node:j1.target",
		"jumper_broken node:jd.target → node:gone",
		"jumper_invalid node:j2.target → node:j3",
		"node_empty node:empty",
		"node_unreachable node:island",
		"port_unconnected branch_output:o2",
		"ref_dangling board:nook.entry_node_id → node:nook_start",
		"ref_dangling node:start.text → variable:doomed",
		"ref_dangling node:talk2.speaker_component_id → component:ghost",
		"ref_dangling project:project.entry → board:lighthouse",
		"ref_dangling project:project.entry → node:gone",
		"speaker_missing node:talk",
		"var_read_never_written variable:seen",
		"var_written_never_read variable:gold",
	}
	if got := findingLines(r.Findings, names); !slices.Equal(got, want) {
		t.Errorf("the findings are\n%q\nwant\n%q", got, want)
	}
	if want := (validation.Counts{Errors: 10, Warnings: 10, Infos: 1}); r.Counts != want || len(r.Muted) != 0 {
		t.Errorf("the counts are %+v, with %d muted, want %+v", r.Counts, len(r.Muted), want)
	}
	// Errors first, then warnings, then infos, each severity's in the order
	// of the rules.
	order := map[validation.Code]int{}
	for i, rule := range validation.Rules() {
		order[rule.Code] = i
	}
	rank := map[validation.Severity]int{validation.Error: 0, validation.Warning: 1, validation.Info: 2}
	if !slices.IsSortedFunc(r.Findings, func(x, y validation.Finding) int {
		if d := rank[x.Severity] - rank[y.Severity]; d != 0 {
			return d
		}
		return order[x.Code] - order[y.Code]
	}) {
		t.Errorf("the findings are not in the order of their severities and rules: %q", findingLines(r.Findings, nil))
	}
	// Each finding carries what a client opens it by (AC3).
	for _, f := range r.Findings {
		var board, node string
		switch f.EntityID {
		case c2, c3:
			board, node = harbour, start
		case o2:
			board, node = harbour, br
		case seen, gold, p:
			board, node = "", ""
		case mill:
			board = mill
		case nook:
			board = nook
		default:
			board, node = harbour, f.EntityID
		}
		if f.BoardID != board || f.NodeID != node || f.Message == "" {
			t.Errorf("%s of %s is opened by the board %q and the node %q, with the message %q, want %q and %q",
				f.Code, names[f.EntityID], names[f.BoardID], names[f.NodeID], f.Message, names[board], names[node])
		}
	}
}

// TestValidationMute checks that a warning is muted on its entity with a
// reason, and listed apart, with its mute, never left out; that the mute's
// batch is undone, and the finding listed as it was; that a mute's reason
// changes, and an unmute removes it; that a mute changes its owner's
// revision; and that no error is muted, nor a code of another kind of
// entity, nor an entity in the trash, nor with a blank reason.
//
// Verifies: FR-DEP-03/AC4
func TestValidationMute(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Harbour").ID
	board, start, dead, trashed := newBoardID(), newNodeID(), newNodeID(), newNodeID()
	pos := map[string]int{"x": 0, "y": 0}
	sendBatch(t, h, owner, p,
		cmd("board.create", map[string]any{"id": board, "name": "Harbour"}),
		cmd("node.create", map[string]any{"id": start, "board_id": board, "type": "element", "kind": "narration",
			"text": paragraph("Hello"), "position": pos}),
		cmd("node.create", map[string]any{"id": dead, "board_id": board, "type": "element", "kind": "narration",
			"text": paragraph("Dead"), "position": pos}),
		cmd("node.create", map[string]any{"id": trashed, "board_id": board, "type": "element", "position": pos}),
		cmd("connection.create", map[string]any{"id": newConnectionID(), "from_node_id": start, "to_node_id": dead}),
		cmd("board.set_entry_node", map[string]any{"id": board, "node_id": start}),
		cmd("node.delete", map[string]any{"id": trashed}),
	)
	deadEnds := func(r validation.Report) (open, muted int, mute *validation.Mute) {
		for _, f := range r.Findings {
			if f.Code == validation.DeadEnd && f.EntityID == dead {
				open++
			}
		}
		for _, f := range r.Muted {
			if f.Code == validation.DeadEnd && f.EntityID == dead {
				muted++
				mute = f.Mute
			}
		}
		return open, muted, mute
	}
	if open, muted, _ := deadEnds(validationReport(t, h, owner, p)); open != 1 || muted != 0 {
		t.Fatalf("before the mute, dead's dead_end is listed %d times, and muted %d, want once, and none", open, muted)
	}
	rev := func() int64 {
		var n struct {
			Rev int64 `json:"rev"`
		}
		if err := json.Unmarshal(call(h, http.MethodGet, nodePath(p, dead), nil, owner.header(false)).body, &n); err != nil {
			t.Fatal(err)
		}
		return n.Rev
	}
	before := rev()
	mute := sendBatch(t, h, owner, p, cmd("validation.mute", map[string]any{"entity_id": dead, "code": "dead_end",
		"reason": "The scene ends here."}))
	r := validationReport(t, h, owner, p)
	open, muted, m := deadEnds(r)
	if open != 0 || muted != 1 || m == nil || m.Reason != "The scene ends here." || m.MutedBy != owner.body.UserID ||
		m.MutedAt.IsZero() {
		t.Errorf("once muted, dead's dead_end is listed %d times, and muted %d with %+v, want none, and once with "+
			"its reason, by %s", open, muted, m, owner.body.UserID)
	}
	if r.Counts.Muted != 1 || r.Counts.Warnings != 0 {
		t.Errorf("once muted, the counts are %+v, want one muted and no warning", r.Counts)
	}
	if after := rev(); after != before+1 {
		t.Errorf("the mute leaves dead at the revision %d, want %d", after, before+1)
	}
	// The mute's undo unmutes it, and its undo mutes it again.
	redo := sendBatch(t, h, owner, p, undoCommands(mute.Undo)...)
	if open, muted, _ := deadEnds(validationReport(t, h, owner, p)); open != 1 || muted != 0 {
		t.Errorf("once the mute is undone, dead's dead_end is listed %d times, and muted %d, want once, and none",
			open, muted)
	}
	sendBatch(t, h, owner, p, undoCommands(redo.Undo)...)
	if _, _, m := deadEnds(validationReport(t, h, owner, p)); m == nil || m.Reason != "The scene ends here." {
		t.Errorf("once the undo is undone, dead's dead_end is muted with %+v, want its reason", m)
	}
	// A mute's reason changes, and the undo of the change sets it back.
	change := sendBatch(t, h, owner, p, cmd("validation.mute", map[string]any{"entity_id": dead, "code": "dead_end",
		"reason": "Cut for now."}))
	if _, _, m := deadEnds(validationReport(t, h, owner, p)); m == nil || m.Reason != "Cut for now." {
		t.Errorf("dead's dead_end is muted with %+v, want the new reason", m)
	}
	sendBatch(t, h, owner, p, undoCommands(change.Undo)...)
	if _, _, m := deadEnds(validationReport(t, h, owner, p)); m == nil || m.Reason != "The scene ends here." {
		t.Errorf("once the change is undone, dead's dead_end is muted with %+v, want its first reason", m)
	}
	// Muting it again with its reason changes nothing, and has no undo.
	if again := sendBatch(t, h, owner, p, cmd("validation.mute", map[string]any{"entity_id": dead,
		"code": "dead_end", "reason": "The scene ends here."})); len(again.Undo) != 0 || len(again.Revisions) != 0 {
		t.Errorf("muting it again with its reason changes %v, with the undo %v, want nothing", again.Revisions,
			again.Undo)
	}
	sendBatch(t, h, owner, p, cmd("validation.unmute", map[string]any{"entity_id": dead, "code": "dead_end"}))
	if open, muted, _ := deadEnds(validationReport(t, h, owner, p)); open != 1 || muted != 0 {
		t.Errorf("once unmuted, dead's dead_end is listed %d times, and muted %d, want once, and none", open, muted)
	}

	for _, tt := range []struct {
		name    string
		payload map[string]any
		at      string
	}{
		{"an error", map[string]any{"entity_id": dead, "code": "jumper_broken", "reason": "No"}, "body.commands[0].payload.code"},
		{"a code of another kind", map[string]any{"entity_id": board, "code": "dead_end", "reason": "No"},
			"body.commands[0].payload.code"},
		{"an entity of no kind that is muted", map[string]any{"entity_id": p, "code": "dead_end", "reason": "No"},
			"body.commands[0].payload.entity_id"},
		{"an entity in the trash", map[string]any{"entity_id": trashed, "code": "dead_end", "reason": "No"},
			"body.commands[0].payload.entity_id"},
		{"an entity of no project", map[string]any{"entity_id": newNodeID(), "code": "dead_end", "reason": "No"},
			"body.commands[0].payload.entity_id"},
		{"a blank reason", map[string]any{"entity_id": dead, "code": "dead_end", "reason": "   "},
			"body.commands[0].payload.reason"},
	} {
		res := call(h, http.MethodPost, commandsPath(p), commandBatch(t, cmd("validation.mute", tt.payload)),
			owner.header(true))
		if got := problems(t, res); !slices.Contains(got, tt.at) {
			t.Errorf("a mute of %s is refused at %v, want %s", tt.name, got, tt.at)
		}
	}
}

// TestValidationDanglingText checks that a text's value reference to a
// variable that the project does not have, which no command writes and which
// is seeded in the database, is a validation error of the text, though the
// index of what the texts name does not hold it.
//
// Verifies: FR-TEXT-02/AC2
func TestValidationDanglingText(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Harbour").ID
	board, start := newBoardID(), newNodeID()
	sendBatch(t, h, owner, p,
		cmd("board.create", map[string]any{"id": board, "name": "Harbour"}),
		cmd("node.create", map[string]any{"id": start, "board_id": board, "type": "element", "kind": "narration",
			"text": paragraph("Hello"), "is_ending": true, "position": map[string]int{"x": 0, "y": 0}}),
		cmd("board.set_entry_node", map[string]any{"id": board, "node_id": start}),
	)
	missing := ids.Encode(ids.Variable, ids.New())
	if _, err := d.Admin(t).Exec(context.Background(), "UPDATE node SET text = $1 WHERE id = $2",
		string(richText(textInline("Gold: "), valueInline(missing))), decode(t, ids.Node, start)); err != nil {
		t.Fatal(err)
	}
	r := validationReport(t, h, owner, p)
	want := []string{"ref_dangling node:start.text → variable:missing"}
	if got := findingLines(r.Findings, map[string]string{start: "start", missing: "missing"}); !slices.Equal(got,
		want) || r.Counts.Errors != 1 {
		t.Errorf("the findings are %q, with %d errors, want %q", got, r.Counts.Errors, want)
	}
}

// TestValidationForcedDeletion checks that a variable's forced deletion
// produces one validation error per site that used it: each expression that
// named it, which no longer compiles, and each text that shows it, whose
// reference dangles.
//
// Verifies: FR-VAR-04/AC2
func TestValidationForcedDeletion(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Harbour").ID
	board, start, end, gate, gold := newBoardID(), newNodeID(), newNodeID(), newConnectionID(),
		ids.Encode(ids.Variable, ids.New())
	pos := map[string]int{"x": 0, "y": 0}
	sendBatch(t, h, owner, p,
		cmd("board.create", map[string]any{"id": board, "name": "Harbour"}),
		cmd("variable.create", map[string]any{"id": gold, "key": "gold", "type": "int", "scope": "global"}),
		cmd("node.create", map[string]any{"id": start, "board_id": board, "type": "element", "kind": "narration",
			"text": paragraph("Hello"), "on_enter": "gold += 1", "position": pos}),
		cmd("node.create", map[string]any{"id": end, "board_id": board, "type": "element", "kind": "narration",
			"text": richText(textInline("Gold: "), valueInline(gold)), "is_ending": true, "position": pos}),
		cmd("connection.create", map[string]any{"id": gate, "from_node_id": start, "to_node_id": end,
			"condition": "gold > 2"}),
		cmd("board.set_entry_node", map[string]any{"id": board, "node_id": start}),
	)
	if r := validationReport(t, h, owner, p); r.Counts.Errors != 0 {
		t.Fatalf("before the deletion, the findings are %q, want no error", findingLines(r.Findings, nil))
	}
	sendBatch(t, h, owner, p, cmd("variable.delete", map[string]any{"id": gold, "force": true}))
	r := validationReport(t, h, owner, p)
	names := map[string]string{start: "start", end: "end", gate: "gate", gold: "gold"}
	want := []string{"expr_invalid connection:gate.condition", "expr_invalid node:start.on_enter",
		"ref_dangling node:end.text → variable:gold"}
	var errors []validation.Finding
	for _, f := range r.Findings {
		if f.Severity == validation.Error {
			errors = append(errors, f)
		}
	}
	if got := findingLines(errors, names); !slices.Equal(got, want) || r.Counts.Errors != len(want) {
		t.Errorf("the errors are %q, counted %d, want %q", got, r.Counts.Errors, want)
	}
	for _, f := range errors {
		if f.Code == validation.ExprInvalid && f.Message == fmt.Sprint(f.Code) {
			t.Errorf("%s has no message", f.Code)
		}
	}
}
