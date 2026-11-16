package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/playstate"
)

func playStatesPath(project string) string   { return projectPath(project) + "/play-states" }
func playStatePath(project, s string) string { return playStatesPath(project) + "/" + s }
func newPlayStateID() string                 { return ids.Encode(ids.PlayState, ids.New()) }

// emptyState returns a save state of the project, saved against its draft
// before the flow started, without a value or a visit count, as a payload
// holds it.
func emptyState(project string) map[string]any {
	return map[string]any{"attributes": map[string]any{}, "projectId": project, "schemaVersion": 1,
		"stateVersion": 1, "variables": map[string]any{}, "visits": map[string]any{}}
}

// playState adds a play state of the project, as the superuser, whose state
// is emptyState's, and returns its ID.
func (f *rolesFixture) playState(t *testing.T, project string) string {
	t.Helper()
	id := ids.New()
	state, err := json.Marshal(emptyState(project))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO play_state (id, workspace_id, project_id, name,
		state) VALUES ($1, $2, $3, 'After act 1', $4)`, id, decode(t, ids.Workspace, f.ws),
		decode(t, ids.Project, project), state); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.PlayState, id)
}

// deletedPlayState adds a play state as playState does, deleted to the
// project's trash, and returns its ID.
func (f *rolesFixture) deletedPlayState(t *testing.T, project string) string {
	t.Helper()
	s := f.playState(t, project)
	if _, err := f.admin.Exec(context.Background(), "UPDATE play_state SET deleted_at = now() WHERE id = $1",
		decode(t, ids.PlayState, s)); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestPlayStates checks the commands of a project's play states and the
// routes that read them: a state saved comes back exactly as it was saved,
// with its cursor, its values, an attribute's among them, and its visit
// counts, which is what play mode loads (FR-PLAY-05 AC1), and who saved it
// and when; the list holds those that are not in the trash, page by page,
// without their states; a rename and a replace of the state change it, each
// with its revision; and a play state deleted is not found, until the undo
// of its deletion restores it, last changed when it was before, since
// neither changed its name or its state.
//
// Verifies: FR-PLAY-05/AC1
func TestPlayStates(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	board, node := ids.Encode(ids.Board, ids.New()), ids.Encode(ids.Node, ids.New())
	variable, component := ids.Encode(ids.Variable, ids.New()), ids.Encode(ids.Component, ids.New())
	attribute := ids.Encode(ids.AttributeDef, ids.New())
	// A state of the project's flow as play mode saves it, of entities that
	// the server does not check the draft for, as one saved before they were
	// deleted.
	saved := emptyState(p.ID)
	saved["cursor"] = map[string]any{"boardId": board, "nodeId": node, "phase": "awaiting_choice"}
	saved["variables"] = map[string]any{variable: 2.5}
	saved["attributes"] = map[string]any{component: map[string]any{attribute: "calm"}}
	saved["visits"] = map[string]any{node: 3}
	// The names of its values, as play mode named them as it saved it.
	names := map[string]any{"variables": map[string]string{variable: "rumours"},
		"attributes": map[string]any{component: map[string]string{attribute: "elara.mood"}}}
	states := []string{newPlayStateID(), newPlayStateID(), newPlayStateID()}
	res := resultFrom(t, call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		wireCommand{Type: "play_state.create", Payload: map[string]any{"id": states[0], "name": "After act 1",
			"state": saved, "names": names}},
		wireCommand{Type: "play_state.create", Payload: map[string]any{"id": states[1], "name": "After act 1",
			"state": emptyState(p.ID)}},
		wireCommand{Type: "play_state.create", Payload: map[string]any{"id": states[2], "name": "Romance path",
			"state": emptyState(p.ID)}}), owner.header(true)))
	if len(res.Revisions) != 3 || res.Revisions[0].ID != states[0] || res.Revisions[0].Rev != 1 {
		t.Errorf("the batch's revisions are %+v, want the three play states', at 1", res.Revisions)
	}

	get := func(id string) (playstate.PlayStateBody, json.RawMessage) {
		t.Helper()
		var body struct {
			playstate.PlayStateBody
			State json.RawMessage `json:"state"`
		}
		res := call(s.main, http.MethodGet, playStatePath(p.ID, id), nil, owner.header(false))
		if res.status != http.StatusOK || json.Unmarshal(res.body, &body) != nil {
			t.Fatalf("read the play state: %d %s", res.status, res.body)
		}
		return body.PlayStateBody, body.State
	}
	b, state := get(states[0])
	want, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != states[0] || b.ProjectID != p.ID || b.Name != "After act 1" || b.Rev != 1 ||
		b.CreatedBy != owner.body.UserID || b.UpdatedBy != owner.body.UserID || b.CreatedAt.IsZero() ||
		b.DeletedAt != nil || !jsonEqual(t, state, want) || b.Names.Variables[variable] != "rumours" ||
		b.Names.Attributes[component][attribute] != "elara.mood" {
		t.Errorf("the play state is %+v, with the state %s, want it as saved, %s", b, state, want)
	}
	if b, _ := get(states[1]); len(b.Names.Variables) != 0 || len(b.Names.Attributes) != 0 {
		t.Errorf("the play state saved without names has the names %+v", b.Names)
	}

	if res := resultFrom(t, call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		wireCommand{Type: "play_state.rename", Payload: map[string]string{"id": states[1], "name": "Before the storm"},
			ExpectedRev: map[string]int64{states[1]: 1}},
		wireCommand{Type: "play_state.set_state", Payload: map[string]any{"id": states[1], "state": saved}}),
		owner.header(true))); len(res.Revisions) != 1 || res.Revisions[0].Rev != 3 || len(res.Undo) != 2 {
		t.Errorf("the rename and the replace: %+v, want the play state at 3, with two steps to undo", res)
	}
	if b, state := get(states[1]); b.Name != "Before the storm" || b.Rev != 3 || !jsonEqual(t, state, want) {
		t.Errorf("after the rename and the replace, the play state is %+v, with the state %s", b, state)
	}

	before, _ := get(states[2])
	deleted := resultFrom(t, call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		wireCommand{Type: "play_state.delete", Payload: map[string]string{"id": states[2]}}), owner.header(true)))
	wantCode(t, "a play state in the trash", call(s.main, http.MethodGet, playStatePath(p.ID, states[2]), nil,
		owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	wantCode(t, "a play state of none", call(s.main, http.MethodGet, playStatePath(p.ID, newPlayStateID()), nil,
		owner.header(false)), http.StatusNotFound, api.CodeNotFound)

	list := func() []string {
		t.Helper()
		var listed []string
		for cursor := ""; ; {
			var page struct {
				Items      []map[string]json.RawMessage `json:"items"`
				NextCursor *string                      `json:"next_cursor"`
			}
			res := call(s.main, http.MethodGet, playStatesPath(p.ID)+"?limit=1&cursor="+cursor, nil,
				owner.header(false))
			if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
				t.Fatalf("list the play states: %d %s", res.status, res.body)
			}
			for _, s := range page.Items {
				if _, ok := s["state"]; ok {
					t.Errorf("the list holds a play state with its state: %v", s)
				}
				var id string
				if err := json.Unmarshal(s["id"], &id); err != nil {
					t.Fatal(err)
				}
				listed = append(listed, id)
			}
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
		}
		return listed
	}
	sorted := func(ids ...string) []string {
		ids = slices.Clone(ids)
		slices.Sort(ids)
		return ids
	}
	if got := list(); !slices.Equal(got, sorted(states[0], states[1])) {
		t.Errorf("the play states listed are %v, want %v", got, sorted(states[0], states[1]))
	}

	// The undo of the deletion restores the play state, which changed
	// neither its name nor its state, so it last changed when it did before.
	undo := make([]wireCommand, len(deleted.Undo))
	for i, u := range deleted.Undo {
		undo[i] = wireCommand{Type: u.Type, Payload: json.RawMessage(u.Payload), ExpectedRev: u.ExpectedRev}
	}
	resultFrom(t, call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t, undo...), owner.header(true)))
	if b, _ := get(states[2]); b.Name != "Romance path" || b.Rev != 3 || !b.UpdatedAt.Equal(before.UpdatedAt) ||
		b.UpdatedBy != before.UpdatedBy {
		t.Errorf("after the undo of its deletion, the play state is %+v, want it last changed as before, %+v", b,
			before)
	}
	if got := list(); !slices.Equal(got, sorted(states...)) {
		t.Errorf("after the undo, the play states listed are %v, want %v", got, sorted(states...))
	}
}

// jsonEqual reports whether a and b are the same JSON value, whatever their
// spacing and the order of their keys.
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("%s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	xs, _ := json.Marshal(x)
	ys, _ := json.Marshal(y)
	return string(xs) == string(ys)
}

// TestPlayStateRefusals checks how the API answers a command of a play state
// that refuses its batch, at the location of the payload's field: a state of
// another project, or that the save-state format's schema refuses, as one of
// another stateVersion, with an ID that is not canonical, a key among them,
// at the key's own location, a snapshotId without its snapshotVersion, a
// value that is no boolean, number, or string, a number written with more
// characters, or a greater exponent, than a variable's default may be, of a
// variable or of an attribute, at its own location, rather than one that the
// database cannot hold, a visit count below 1, or more than a mebibyte;
// names of more than a mebibyte; a name that is empty,
// of more than 100 characters, or with white space around it; and a change of
// a play state in the trash, or of none. Nothing of a refused batch applies.
func TestPlayStateRefusals(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	other := createProject(t, s.main, owner, ws, "Lighthouse")
	kept, trashed := newPlayStateID(), newPlayStateID()
	resultFrom(t, call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		wireCommand{Type: "play_state.create", Payload: map[string]any{"id": kept, "name": "After act 1",
			"state": emptyState(p.ID)}},
		wireCommand{Type: "play_state.create", Payload: map[string]any{"id": trashed, "name": "Romance path",
			"state": emptyState(p.ID)}},
		wireCommand{Type: "play_state.delete", Payload: map[string]string{"id": trashed}}), owner.header(true)))

	// with returns emptyState's state of the project with the field k set to
	// v, or left out where v is nil.
	with := func(k string, v any) map[string]any {
		st := emptyState(p.ID)
		if v == nil {
			delete(st, k)
		} else {
			st[k] = v
		}
		return st
	}
	create := func(name string, st any) wireCommand {
		return wireCommand{Type: "play_state.create", Payload: map[string]any{"id": newPlayStateID(), "name": name,
			"state": st}}
	}
	node, named := ids.Encode(ids.Node, ids.New()), ids.Encode(ids.Variable, ids.New())
	// A number of 17,003 characters, more digits after its point than
	// PostgreSQL's numeric holds, and one of an exponent beyond ±999, each
	// of which JSON.parse reads as 0.
	long, component := ids.Encode(ids.Variable, ids.New()), ids.Encode(ids.Component, ids.New())
	attribute := ids.Encode(ids.AttributeDef, ids.New())
	longNumber := json.Number("0." + strings.Repeat("0", 17000) + "1")
	for _, c := range []struct {
		cmds []wireCommand
		loc  string
	}{
		{[]wireCommand{create("After act 1", emptyState(other.ID))}, "body.commands[0].payload.state.projectId"},
		{[]wireCommand{create("After act 1", with("stateVersion", 2))}, "body.commands[0].payload.state.stateVersion"},
		{[]wireCommand{create("After act 1", with("projectId", strings.ToLower(p.ID)))},
			"body.commands[0].payload.state.projectId"},
		{[]wireCommand{create("After act 1", with("variables", map[string]any{"met_elara": true}))},
			"body.commands[0].payload.state.variables.met_elara"},
		{[]wireCommand{create("After act 1", with("snapshotId", ids.Encode(ids.Snapshot, ids.New())))},
			"body.commands[0].payload.state.snapshotVersion"},
		{[]wireCommand{create("After act 1", with("variables",
			map[string]any{ids.Encode(ids.Variable, ids.New()): map[string]int{"a": 1}}))}, ""},
		{[]wireCommand{create("After act 1", with("variables", map[string]any{long: longNumber}))},
			"body.commands[0].payload.state.variables." + long},
		{[]wireCommand{create("After act 1", with("attributes",
			map[string]any{component: map[string]any{attribute: json.Number("1e-1000")}}))},
			"body.commands[0].payload.state.attributes." + component + "." + attribute},
		{[]wireCommand{create("After act 1", with("visits", map[string]any{node: 0}))},
			"body.commands[0].payload.state.visits." + node},
		{[]wireCommand{create("After act 1", with("visits", nil))}, ""},
		{[]wireCommand{{Type: "play_state.create", Payload: map[string]any{"id": newPlayStateID(), "name": "Named",
			"state": emptyState(p.ID), "names": map[string]any{"variables": map[string]string{
				ids.Encode(ids.Variable, ids.New()): "rumours"}}}}}, ""},
		{[]wireCommand{{Type: "play_state.create", Payload: map[string]any{"id": newPlayStateID(), "name": "Named",
			"state": with("variables", map[string]any{named: true}),
			"names": map[string]any{"variables": map[string]string{named: " rumours"}}}}},
			"body.commands[0].payload.names.variables." + named},
		{[]wireCommand{create("", emptyState(p.ID))}, "body.commands[0].payload.name"},
		{[]wireCommand{create(" After act 1", emptyState(p.ID))}, "body.commands[0].payload.name"},
		{[]wireCommand{create(strings.Repeat("a", 101), emptyState(p.ID))}, "body.commands[0].payload.name"},
		{[]wireCommand{{Type: "play_state.rename", Payload: map[string]string{"id": trashed, "name": "Storm"}}},
			"body.commands[0].payload.id"},
		{[]wireCommand{{Type: "play_state.set_state", Payload: map[string]any{"id": newPlayStateID(),
			"state": emptyState(p.ID)}}}, "body.commands[0].payload.id"},
		{[]wireCommand{{Type: "play_state.rename", Payload: map[string]string{"id": kept, "name": "Storm"}},
			{Type: "play_state.set_state", Payload: map[string]any{"id": kept, "state": emptyState(other.ID)}}},
			"body.commands[1].payload.state.projectId"},
	} {
		res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t, c.cmds...), owner.header(true))
		code, locs, _ := problemsOf(t, res)
		if res.status != http.StatusUnprocessableEntity || code != api.CodeValidationFailed ||
			c.loc != "" && !slices.Contains(locs, c.loc) {
			t.Errorf("%+v: %d %s, want 422 %s at %s", c.cmds, res.status, res.body, api.CodeValidationFailed, c.loc)
		}
	}
	// A state of more than a mebibyte, as JSON.
	big := map[string]any{}
	for range 300 {
		big[ids.Encode(ids.Variable, ids.New())] = strings.Repeat("a", 4000)
	}
	res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t, create("Big", with("variables", big))),
		owner.header(true))
	if code, locs, _ := problemsOf(t, res); res.status != http.StatusUnprocessableEntity ||
		code != api.CodeValidationFailed || !slices.Contains(locs, "body.commands[0].payload.state") {
		t.Errorf("a state of more than a mebibyte: %d %s", res.status, res.body)
	}
	// Names of more than a mebibyte, as JSON, of a state of less, each of a
	// value that it holds.
	many, labels := map[string]any{}, map[string]string{}
	for range 6000 {
		v := ids.Encode(ids.Variable, ids.New())
		many[v], labels[v] = true, strings.Repeat("a", playstate.MaxValueNameLength)
	}
	res = call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t, wireCommand{Type: "play_state.create",
		Payload: map[string]any{"id": newPlayStateID(), "name": "Named", "state": with("variables", many),
			"names": map[string]any{"variables": labels}}}), owner.header(true))
	if code, locs, _ := problemsOf(t, res); res.status != http.StatusUnprocessableEntity ||
		code != api.CodeValidationFailed || !slices.Contains(locs, "body.commands[0].payload.names") {
		t.Errorf("names of more than a mebibyte: %d %.500s", res.status, res.body)
	}

	// The ID of a play state of another project, which the batch's
	// transaction does not see, is refused as one that the project has is.
	for _, id := range []string{kept, trashed} {
		res = call(s.main, http.MethodPost, commandsPath(other.ID), commandBatch(t,
			wireCommand{Type: "play_state.create", Payload: map[string]any{"id": id, "name": "Taken",
				"state": emptyState(other.ID)}}), owner.header(true))
		if code, locs, _ := problemsOf(t, res); res.status != http.StatusUnprocessableEntity ||
			code != api.CodeValidationFailed || !slices.Contains(locs, "body.commands[0].payload.id") {
			t.Errorf("a create with the ID of another project's play state: %d %s, want 422 %s at its id",
				res.status, res.body, api.CodeValidationFailed)
		}
	}

	// Nothing of the refused batches applied.
	var b playstate.PlayStateBody
	if res := call(s.main, http.MethodGet, playStatePath(p.ID, kept), nil, owner.header(false)); res.status !=
		http.StatusOK || json.Unmarshal(res.body, &b) != nil || b.Name != "After act 1" || b.Rev != 1 {
		t.Errorf("after the refusals, the play state is %d %s", res.status, res.body)
	}
	var page struct {
		Items []playstate.PlayStateSummary `json:"items"`
	}
	if res := call(s.main, http.MethodGet, playStatesPath(p.ID), nil, owner.header(false)); res.status !=
		http.StatusOK || json.Unmarshal(res.body, &page) != nil || len(page.Items) != 1 {
		t.Errorf("after the refusals, the play states are %d %s, want the one kept", res.status, res.body)
	}
	if res := call(s.main, http.MethodGet, playStatesPath(other.ID), nil, owner.header(false)); res.status !=
		http.StatusOK || json.Unmarshal(res.body, &page) != nil || len(page.Items) != 0 {
		t.Errorf("after the refusals, the other project's play states are %d %s, want none", res.status, res.body)
	}
}

// TestPlayStateNumbers checks that a play state's values that are numbers
// come back as a runtime writes them, the shortest number that reads as the
// double again, rather than as the database writes them back, 1e300 with 301
// digits, and that the bound of a state counts its numbers so too, however a
// payload writes them: a state well within a mebibyte as it was saved, of
// many such numbers, is replaced, and the undo of its replacement, which
// writes each of them back with every digit, as the operation log does,
// applies.
func TestPlayStateNumbers(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	saved := emptyState(p.ID)
	variables := map[string]any{}
	for range 4000 {
		variables[ids.Encode(ids.Variable, ids.New())] = 1e300
	}
	saved["variables"] = variables
	if data, err := json.Marshal(saved); err != nil || len(data) > playstate.MaxStateBytes/4 ||
		len(data)+len(variables)*(301-len("1e+300")) <= playstate.MaxStateBytes {
		t.Fatalf("the state takes %d bytes, %v: want it well within the bound, and past it without exponents",
			len(data), err)
	}
	id := newPlayStateID()
	resultFrom(t, call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		wireCommand{Type: "play_state.create", Payload: map[string]any{"id": id, "name": "Rich", "state": saved}}),
		owner.header(true)))

	var body struct {
		State json.RawMessage `json:"state"`
	}
	res := call(s.main, http.MethodGet, playStatePath(p.ID, id), nil, owner.header(false))
	if res.status != http.StatusOK || json.Unmarshal(res.body, &body) != nil {
		t.Fatalf("read the play state: %d %.500s", res.status, res.body)
	}
	if want, _ := json.Marshal(saved); len(body.State) > len(want) ||
		!strings.Contains(string(body.State), ":1e+300") {
		t.Errorf("the state read takes %d bytes, %.200s, want at most the %d that it was saved with, with 1e+300",
			len(body.State), body.State, len(want))
	}

	replaced := resultFrom(t, call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		wireCommand{Type: "play_state.set_state", Payload: map[string]any{"id": id, "state": emptyState(p.ID)},
			ExpectedRev: map[string]int64{id: 1}}), owner.header(true)))
	if len(replaced.Undo) != 1 {
		t.Fatalf("the replace's undo has %d steps, want one that writes the state back", len(replaced.Undo))
	}
	// The undo, as the answer holds it, sent as it is, though it writes each
	// number with every digit, as the operation log's jsonb does.
	undo := wireCommand{Type: replaced.Undo[0].Type, Payload: json.RawMessage(replaced.Undo[0].Payload),
		ExpectedRev: replaced.Undo[0].ExpectedRev}
	resultFrom(t, call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t, undo), owner.header(true)))
	res = call(s.main, http.MethodGet, playStatePath(p.ID, id), nil, owner.header(false))
	if res.status != http.StatusOK || json.Unmarshal(res.body, &body) != nil {
		t.Fatalf("read the play state: %d %.500s", res.status, res.body)
	}
	if want, _ := json.Marshal(saved); !jsonEqual(t, body.State, want) {
		t.Errorf("after the undo of its replacement, the state is %.200s, want it as saved", body.State)
	}
}
