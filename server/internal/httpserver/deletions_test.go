package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// inUse returns the details of an answer of 409 variable_in_use or
// component_in_use, as code says, which it fails the test unless res is: the
// usages' count, the count of each use, and the usages as lines of their uses
// and sites, as sites writes them, sorted.
func inUse(t *testing.T, res response, code api.Code) (count int, counts map[string]int, lines []string) {
	t.Helper()
	if res.status != http.StatusConflict || res.code() != code {
		t.Fatalf("%d %s, want 409 %s", res.status, res.body, code)
	}
	d := res.details(t)
	count = int(d["usage_count"].(float64))
	counts = map[string]int{}
	for use, n := range d["counts"].(map[string]any) {
		counts[use] = int(n.(float64))
	}
	for _, u := range d["usages"].([]any) {
		u := u.(map[string]any)
		line := u["use"].(string) + " " + u["site"].(string)
		if b, ok := u["board_name"].(string); ok {
			line += " " + b + "/" + u["node_title"].(string)
		}
		for _, k := range []string{"connection_id", "branch_output_id", "component_id", "attribute_id"} {
			if id, ok := u[k].(string); ok {
				line += " " + id
			}
		}
		lines = append(lines, line)
	}
	slices.Sort(lines)
	if errs := d["errors"].([]any); len(errs) != 1 ||
		!strings.HasSuffix(errs[0].(map[string]any)["location"].(string), "].payload.force") {
		t.Errorf("the refusal's errors are %v, want one at the payload's force", errs)
	}
	return count, counts, lines
}

// expressionValid returns whether the expression of the field of the owner
// whose ID is owner is valid, and its error's code if it is not.
func (w *usageWorld) expressionValid(t *testing.T, owner, field string) (bool, string) {
	t.Helper()
	var valid bool
	var code *string
	if err := w.admin.QueryRow(context.Background(), `SELECT is_valid, error->>'code' FROM expression
		WHERE field = $1 AND $2 IN (node_id, connection_id, branch_output_id)`, field, uuidOf(t, owner)).Scan(&valid,
		&code); err != nil {
		t.Fatal(err)
	}
	if code == nil {
		return valid, ""
	}
	return valid, *code
}

// TestVariableDeletionGuarded checks that a variable that sites of the
// project use is not deleted unless its deletion is forced: the deletion is
// refused with 409 variable_in_use, which counts and lists the usages, as the
// variable's usage lists do (PRD FR-VAR-04 AC1), one that only a text shows
// as well (FR-TEXT-02 AC2); a forced deletion leaves each expression that
// used the variable invalid, as each names what no longer resolves, a
// validation error each (FR-VAR-04 AC2), and its undo brings them back
// valid; and the usages are those that the batch's commands before the
// deletion leave.
//
// Verifies: FR-VAR-04/AC1, FR-VAR-04/AC2, FR-TEXT-02/AC2
func TestVariableDeletionGuarded(t *testing.T) {
	t.Parallel()
	w := newUsageWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	send := func(cmds ...wireCommand) response {
		return call(h, http.MethodPost, commandsPath(p), commandBatch(t, cmds...), o.header(true))
	}
	del := func(id string, force bool) wireCommand {
		payload := map[string]any{"id": id}
		if force {
			payload["force"] = true
		}
		return wireCommand{Type: "variable.delete", Payload: payload}
	}

	// met_elara is read by Keeper's text and the gate's condition, and
	// written by Tower's on-enter script; Tower's on-exit script, which does
	// not compile, uses nothing.
	count, counts, lines := inUse(t, send(del(w.metElara, false)), api.CodeVariableInUse)
	wantLines := []string{"read connection_condition Harbour/Keeper " + w.gate, "read node_text Harbour/Keeper",
		"write node_on_enter Lighthouse/Tower"}
	if count != 3 || counts["read"] != 2 || counts["write"] != 1 || !slices.Equal(lines, wantLines) {
		t.Errorf("met_elara's deletion is refused with %d usages, %v, %q, want 3, 2 reads and a write, %q", count,
			counts, lines, wantLines)
	}
	if reads, writes := usages(t, h, o, variableUsagesPath(p, w.metElara, "read"), 50),
		usages(t, h, o, variableUsagesPath(p, w.metElara, "write"), 50); len(reads) != 2 || len(writes) != 1 {
		t.Errorf("met_elara's usage lists hold %d reads and %d writes, as the refusal counts them, want 2 and 1",
			len(reads), len(writes))
	}

	// A variable that a text alone shows (FR-TEXT-02 AC2).
	coins := newVariableID()
	res := send(wireCommand{Type: "variable.create", Payload: map[string]any{"id": coins, "key": "coins",
		"type": "int", "scope": "global"}}, wireCommand{Type: "node.update", Payload: map[string]any{"id": w.pier,
		"text": richText(textInline("Coins: "), valueInline(coins))}})
	resultFrom(t, res)
	if count, counts, lines := inUse(t, send(del(coins, false)), api.CodeVariableInUse); count != 1 ||
		counts["read"] != 1 || counts["write"] != 0 || !slices.Equal(lines, []string{"read node_text Harbour/Pier"}) {
		t.Errorf("coins' deletion is refused with %d usages, %v, %q, want Pier's text", count, counts, lines)
	}

	// The usages are those that the batch's commands before the deletion
	// leave: a batch that first takes away the last of them deletes the
	// variable, and one that first adds one is refused.
	resultFrom(t, send(wireCommand{Type: "node.update", Payload: map[string]any{"id": w.pier,
		"text": richText(textInline("No coins"))}}, del(coins, false)))
	spare := newVariableID()
	resultFrom(t, send(wireCommand{Type: "variable.create", Payload: map[string]any{"id": spare, "key": "spare",
		"type": "int", "scope": "global"}}))
	inUse(t, send(wireCommand{Type: "connection.update", Payload: map[string]any{"id": w.gate,
		"on_traverse": "spare += 1"}}, del(spare, false)), api.CodeVariableInUse)

	// A forced deletion of gold, which the gate's on-traverse script and
	// Tower's on-enter script read and write, and elara's description shows,
	// leaves both scripts invalid, each with its error (FR-VAR-04 AC2), and
	// its undo makes them valid again.
	forced := resultFrom(t, send(del(w.gold, true)))
	for _, s := range []struct{ owner, field string }{{w.gate, "on_traverse"}, {w.tower, "on_enter"}} {
		if valid, code := w.expressionValid(t, s.owner, s.field); valid || code != "unknown_identifier" {
			t.Errorf("once gold is deleted, the %s of %s is valid %v, with the error %q, want invalid, "+
				"unknown_identifier", s.field, s.owner, valid, code)
		}
	}
	var invalid int
	if err := w.admin.QueryRow(context.Background(), "SELECT count(*) FROM expression WHERE project_id = $1 "+
		"AND NOT is_valid", uuidOf(t, p)).Scan(&invalid); err != nil {
		t.Fatal(err)
	}
	if invalid != 3 {
		t.Errorf("once gold is deleted, %d expressions are invalid, want 3: one for each of its two sites, and "+
			"Tower's on-exit script, which never compiled", invalid)
	}
	resultFrom(t, send(undoCommands(forced.Undo)...))
	for _, s := range []struct{ owner, field string }{{w.gate, "on_traverse"}, {w.tower, "on_enter"}} {
		if valid, _ := w.expressionValid(t, s.owner, s.field); !valid {
			t.Errorf("once gold's deletion is undone, the %s of %s is invalid", s.field, s.owner)
		}
	}
	wantDrift(t, w.admin, p)
}

// TestComponentDeletionGuarded checks that a component that sites of the
// project use, a node as its speaker among them, is not deleted unless its
// deletion is forced: the deletion is refused with 409 component_in_use,
// which counts the usages, as the component's usage lists do (PRD FR-COMP-04
// AC1), and lists them, the nodes whose speaker it is among them (FR-EDIT-06
// AC2).
//
// Verifies: FR-COMP-04/AC1, FR-EDIT-06/AC2
func TestComponentDeletionGuarded(t *testing.T) {
	t.Parallel()
	w := newUsageWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	send := func(cmds ...wireCommand) response {
		return call(h, http.MethodPost, commandsPath(p), commandBatch(t, cmds...), o.header(true))
	}
	del := func(id string) wireCommand {
		return wireCommand{Type: "component.delete", Payload: map[string]any{"id": id}}
	}
	for _, c := range []struct {
		name, id string
	}{{"elara", w.elara}, {"mira", w.mira}} {
		count, counts, _ := inUse(t, send(del(c.id)), api.CodeComponentInUse)
		listed := 0
		for _, use := range []string{"speaker", "mention", "read", "write"} {
			n := len(usages(t, h, o, componentUsagesPath(p, c.id, use), 50))
			if counts[use] != n {
				t.Errorf("%s's refusal counts %d of the use %s, and its list holds %d", c.name, counts[use], use, n)
			}
			listed += n
		}
		if count != listed || count == 0 {
			t.Errorf("%s's refusal counts %d usages, and its lists hold %d", c.name, count, listed)
		}
	}

	// A component used as a speaker alone (FR-EDIT-06 AC2).
	narrator, voice := newComponentID(), newNodeID()
	resultFrom(t, send(wireCommand{Type: "component.create", Payload: map[string]any{"id": narrator,
		"component_type_id": w.cast, "name": "Narrator"}}, wireCommand{Type: "node.create", Payload: map[string]any{
		"id": voice, "board_id": w.harbour, "type": "element", "title": "Voice", "position": map[string]int{"x": 0,
			"y": 0}, "speaker_component_id": narrator}}))
	count, counts, lines := inUse(t, send(del(narrator)), api.CodeComponentInUse)
	if want := []string{"speaker node_speaker Harbour/Voice"}; count != 1 || counts["speaker"] != 1 ||
		!slices.Equal(lines, want) {
		t.Errorf("the narrator's deletion is refused with %d usages, %v, %q, want %q", count, counts, lines, want)
	}
	if details := send(del(narrator)).details(t); fmt.Sprint(details["usages"].([]any)[0].(map[string]any)["node_id"]) !=
		voice {
		t.Errorf("the refusal lists %v, want the node %s", details["usages"], voice)
	}
	// Forced, the deletion leaves Voice's speaker as it is, in the trash.
	resultFrom(t, send(wireCommand{Type: "component.delete", Payload: map[string]any{"id": narrator, "force": true}}))
	var speaker string
	if err := w.admin.QueryRow(context.Background(), "SELECT speaker_component_id::text FROM node WHERE id = $1",
		uuidOf(t, voice)).Scan(&speaker); err != nil || speaker != uuidOf(t, narrator).String() {
		t.Errorf("once the narrator is deleted, Voice's speaker is %q (%v), want the narrator", speaker, err)
	}
	wantDrift(t, w.admin, p)
}

// TestComponentOwnDescription checks that a component's own description,
// which goes to the trash with it, is no usage of it: a component whose
// description mentions it and shows one of its attributes is deleted
// unforced, and so is undone its creation (PRD FR-COMP-04).
func TestComponentOwnDescription(t *testing.T) {
	t.Parallel()
	w := newUsageWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	send := func(cmds ...wireCommand) response {
		return call(h, http.MethodPost, commandsPath(p), commandBatch(t, cmds...), o.header(true))
	}
	create := func(id string) wireCommand {
		return wireCommand{Type: "component.create", Payload: map[string]any{"id": id, "component_type_id": w.cast,
			"name": "Self " + id, "description": richText(mentionInline(id), textInline(" trusts "),
				attributeInline(id, w.trust))}}
	}
	deleted := func(id string) bool {
		var trashed bool
		if err := w.admin.QueryRow(context.Background(), "SELECT deleted_at IS NOT NULL FROM component WHERE id = $1",
			uuidOf(t, id)).Scan(&trashed); err != nil {
			t.Fatal(err)
		}
		return trashed
	}
	first, second := newComponentID(), newComponentID()
	created := resultFrom(t, send(create(first)))
	resultFrom(t, send(undoCommands(created.Undo)...))
	if !deleted(first) {
		t.Error("the undo of the creation of a component that its own description uses left it outside the trash")
	}
	resultFrom(t, send(create(second)))
	resultFrom(t, send(wireCommand{Type: "component.delete", Payload: map[string]any{"id": second}}))
	if !deleted(second) {
		t.Error("the deletion of a component that its own description uses left it outside the trash")
	}
}

// TestForcedDeletionRole checks who may force the deletion of a variable that
// the project's content uses: a role that may publish the project's
// snapshots (§5.6), so an editor of a project whose publish_min_role is
// editor, but not of one whose publish_min_role is admin, whose admin may;
// and that a force that overrides nothing, of a variable that nothing uses,
// needs no more than its deletion does.
func TestForcedDeletionRole(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	p := f.project(t, "")
	force := func(si signedIn, v string) response {
		return call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{Type: "variable.delete",
			Payload: map[string]any{"id": v, "force": true}}), si.header(true))
	}
	editor, admin := f.as["editor"], f.as["admin"]
	wantCode(t, "an editor's unforced deletion", call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t,
		wireCommand{Type: "variable.delete", Payload: map[string]any{"id": f.usedVariable(t, p)}}),
		editor.header(true)), http.StatusConflict, api.CodeVariableInUse)
	if res := force(editor, f.usedVariable(t, p)); res.status != http.StatusOK {
		t.Errorf("an editor's forced deletion where editors publish: %d %s", res.status, res.body)
	}
	if _, err := f.admin.Exec(context.Background(), `UPDATE project SET settings = jsonb_set(settings,
		'{publish_min_role}', '"admin"') WHERE id = $1`, uuidOf(t, p)); err != nil {
		t.Fatal(err)
	}
	used := f.usedVariable(t, p)
	wantCode(t, "an editor's forced deletion where admins alone publish", force(editor, used), http.StatusForbidden,
		api.CodeInsufficientRole)
	if res := force(editor, f.variable(t, p)); res.status != http.StatusOK {
		t.Errorf("an editor's forced deletion of a variable that nothing uses: %d %s", res.status, res.body)
	}
	if res := force(admin, used); res.status != http.StatusOK {
		t.Errorf("an admin's forced deletion where admins publish: %d %s", res.status, res.body)
	}
	var deleted bool
	if err := f.admin.QueryRow(context.Background(), "SELECT deleted_at IS NOT NULL FROM variable WHERE id = $1",
		uuidOf(t, used)).Scan(&deleted); err != nil || !deleted {
		t.Errorf("the admin's forced deletion left the variable outside the trash (%v)", err)
	}
}

// TestUndoOfCreationForces checks that the undo of a variable's creation
// deletes it even though an expression that named its key before it existed
// uses it, as the creation made it, so that the undo gives the project back
// as it was; and that it is refused, as a deletion is, once a later batch
// has used the variable, which the undo of the creation would break.
func TestUndoOfCreationForces(t *testing.T) {
	t.Parallel()
	w := newUsageWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	send := func(cmds ...wireCommand) response {
		return call(h, http.MethodPost, commandsPath(p), commandBatch(t, cmds...), o.header(true))
	}
	resultFrom(t, send(wireCommand{Type: "node.update", Payload: map[string]any{"id": w.pier,
		"on_enter": "luck += 1"}}))
	if valid, code := w.expressionValid(t, w.pier, "on_enter"); valid || code != "unknown_identifier" {
		t.Fatalf("before luck exists, Pier's on-enter script is valid %v, %q", valid, code)
	}
	luck := newVariableID()
	created := resultFrom(t, send(wireCommand{Type: "variable.create", Payload: map[string]any{"id": luck,
		"key": "luck", "type": "int", "scope": "global"}}))
	if valid, _ := w.expressionValid(t, w.pier, "on_enter"); !valid {
		t.Fatal("once luck exists, Pier's on-enter script does not compile")
	}
	resultFrom(t, send(undoCommands(created.Undo)...))
	if valid, code := w.expressionValid(t, w.pier, "on_enter"); valid || code != "unknown_identifier" {
		t.Errorf("once luck's creation is undone, Pier's on-enter script is valid %v, %q", valid, code)
	}

	later := ids.Encode(ids.Variable, ids.New())
	created = resultFrom(t, send(wireCommand{Type: "variable.create", Payload: map[string]any{"id": later,
		"key": "later", "type": "int", "scope": "global"}}))
	resultFrom(t, send(wireCommand{Type: "connection.update", Payload: map[string]any{"id": w.gate,
		"on_traverse": "later = 2"}}))
	inUse(t, send(undoCommands(created.Undo)...), api.CodeVariableInUse)
}
