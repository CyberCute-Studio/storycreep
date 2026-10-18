package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// TestRevisionConflict checks that a batch with stale revisions is answered
// with 409 revision_conflict, whose details list each stale entity with its
// command, the revision that the command expected, and the entity's current
// revision, or that it no longer exists, without it, and the state of each
// entity that still exists, once, however many commands expected it.
//
// Verifies: FR-API-03/AC3
func TestRevisionConflict(t *testing.T) {
	stale, gone := ids.New(), ids.New()
	first, second, third := ids.New(), ids.New(), ids.New()
	state := json.RawMessage(`{"text": "now"}`)
	err := fmt.Errorf("apply: %w", &commands.RevisionConflictError{Conflicts: []commands.Conflict{
		{Command: 0, CommandID: first, Type: "node.update_text", Ref: commands.Ref{Kind: ids.Node, ID: stale},
			Expected: 4, Exists: true, Current: 5, State: state},
		{Command: 1, CommandID: second, Type: "node.move", Ref: commands.Ref{Kind: ids.Node, ID: stale},
			Expected: 3, Exists: true, Current: 5, State: state},
		{Command: 2, CommandID: third, Type: "connection.create", Ref: commands.Ref{Kind: ids.Node, ID: gone},
			Expected: 2},
	}})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/projects/prj_01HQ8Z3XK4F7NAWR5TCQ9M2BVD/commands", nil)
	w := httptest.NewRecorder()
	api.WriteError(w, r, revisionConflictError(err))
	res := response{status: w.Code, header: w.Header(), body: w.Body.Bytes()}
	if res.status != http.StatusConflict || res.code() != api.CodeRevisionConflict {
		t.Fatalf("%d %s, want 409 revision_conflict", res.status, res.body)
	}
	var env struct {
		Error struct {
			Details struct {
				Conflicts []map[string]any `json:"conflicts"`
				States    map[string]any   `json:"states"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res.body, &env); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"command": float64(0), "command_id": first.String(), "type": "node.update_text",
			"id": ids.Encode(ids.Node, stale), "expected_rev": float64(4), "exists": true, "current_rev": float64(5)},
		{"command": float64(1), "command_id": second.String(), "type": "node.move",
			"id": ids.Encode(ids.Node, stale), "expected_rev": float64(3), "exists": true, "current_rev": float64(5)},
		{"command": float64(2), "command_id": third.String(), "type": "connection.create",
			"id": ids.Encode(ids.Node, gone), "expected_rev": float64(2), "exists": false},
	}
	got := env.Error.Details.Conflicts
	if len(got) != len(want) {
		t.Fatalf("the conflicts: %s", res.body)
	}
	for i := range want {
		g, _ := json.Marshal(got[i])
		e, _ := json.Marshal(want[i])
		if string(g) != string(e) {
			t.Errorf("conflict %d: %s, want %s", i, g, e)
		}
	}
	if want := map[string]any{ids.Encode(ids.Node, stale): map[string]any{"text": "now"}}; !reflect.DeepEqual(
		env.Error.Details.States, want) {
		t.Errorf("the states are %v, want %v, the one entity that exists, once", env.Error.Details.States, want)
	}

	// Any other error is left to the route's own mapping.
	other := errors.New("the database is gone")
	if err := revisionConflictError(other); err != other {
		t.Errorf("revisionConflictError(%v) = %v, want it unchanged", other, err)
	}
}

// commandsPath returns the path of the command endpoint of the project id.
func commandsPath(id string) string { return projectPath(id) + "/commands" }

// wireCommand is a command as a client sends it. A command without an ID is
// sent with a new one, and one without a payload with an empty object.
type wireCommand struct {
	ID          string           `json:"command_id"`
	Type        string           `json:"type"`
	Payload     any              `json:"payload"`
	ExpectedRev map[string]int64 `json:"expected_rev,omitempty"`
}

// withIDs returns the commands, each with its ID and payload filled in.
func withIDs(cmds []wireCommand) []wireCommand {
	out := slices.Clone(cmds)
	for i := range out {
		if out[i].ID == "" {
			out[i].ID = ids.New().String()
		}
		if out[i].Payload == nil {
			out[i].Payload = map[string]any{}
		}
	}
	return out
}

// commandBatch returns the body of a request of one batch of cmds.
func commandBatch(t *testing.T, cmds ...wireCommand) []byte {
	t.Helper()
	return jsonBody(t, map[string]any{"commands": withIDs(cmds)})
}

// commandBatches returns the body of a request of the batches.
func commandBatches(t *testing.T, batches ...[]wireCommand) []byte {
	t.Helper()
	list := make([]map[string]any, len(batches))
	for i, b := range batches {
		list[i] = map[string]any{"commands": withIDs(b)}
	}
	return jsonBody(t, map[string]any{"batches": list})
}

// resultFrom returns the result of a batch that a response with status 200
// answers.
func resultFrom(t *testing.T, res response) commandResultBody {
	t.Helper()
	var body commandResultBody
	if res.status != http.StatusOK || json.Unmarshal(res.body, &body) != nil {
		t.Fatalf("%d %s, want 200 and a batch's result", res.status, res.body)
	}
	return body
}

// problems returns the locations of the problems of an answer of
// validation_failed.
func problems(t *testing.T, res response) []string {
	t.Helper()
	if res.status != http.StatusUnprocessableEntity || res.code() != api.CodeValidationFailed {
		t.Fatalf("%d %s, want 422 validation_failed", res.status, res.body)
	}
	var out []string
	for _, p := range res.details(t)["errors"].([]any) {
		out = append(out, p.(map[string]any)["location"].(string))
	}
	return out
}

// TestCommandEndpoint checks the command endpoint's form of one batch: a
// batch of an admin of the project applies whole, as one operation, and is
// answered with the project's new seq and the revisions of the entities that
// it changed; and the batch sent again is answered with the same body,
// byte for byte, and applies nothing again, after another batch has changed
// the project too.
//
// Verifies: FR-API-03/AC1
func TestCommandEndpoint(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	admin := f.as["admin"]
	p := f.project(t, "")
	body := commandBatch(t,
		wireCommand{Type: "project.rename", Payload: map[string]string{"name": "Harbour"},
			ExpectedRev: map[string]int64{p: 1}},
		wireCommand{Type: "project.describe", Payload: map[string]string{"description": "The first conversation"},
			ExpectedRev: map[string]int64{p: 1}})
	first := call(f.s.main, http.MethodPost, commandsPath(p), body, admin.header(true))
	got := resultFrom(t, first)
	if got.Status != "applied" || got.Seq != 1 || !slices.Equal(got.Revisions, []entityRevisionBody{{p, 3}}) {
		t.Errorf("the batch's result: %+v", got)
	}
	pr := projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, admin.header(false)), http.StatusOK)
	if pr.Name != "Harbour" || pr.Description != "The first conversation" || pr.Rev != 3 || pr.Seq != 1 {
		t.Errorf("the project after the batch: %+v", pr)
	}
	if res := call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t,
		wireCommand{Type: "project.rename", Payload: map[string]string{"name": "Pier"}}), admin.header(true)); resultFrom(
		t, res).Seq != 2 {
		t.Errorf("the next batch: %s", res.body)
	}
	again := call(f.s.main, http.MethodPost, commandsPath(p), body, admin.header(true))
	if again.status != http.StatusOK || !bytes.Equal(again.body, first.body) {
		t.Errorf("the batch sent again: %d %s, want 200 %s", again.status, again.body, first.body)
	}
	if n := countRows(t, f.d, "SELECT count(*) FROM operation WHERE project_id = $1",
		decode(t, ids.Project, p)); n != 2 {
		t.Errorf("the project has %d operations, want 2", n)
	}
	if pr := projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, admin.header(false)),
		http.StatusOK); pr.Name != "Pier" || pr.Seq != 2 {
		t.Errorf("the project after the batch sent again: %+v", pr)
	}
}

// TestCommandEndpointBatches checks the form of a list of batches: each
// batch applies, or not, on its own, in the order of the list, and the
// answer, 200, has a result of each, so that a batch refused for a stale
// revision, for a command that is not valid, or because it is not one that
// can be applied keeps neither an earlier batch nor a later one from
// applying, and its result holds the error that the batch alone would be
// answered with.
//
// Verifies: FR-API-03/AC2
func TestCommandEndpointBatches(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	admin := f.as["admin"]
	p := f.project(t, "")
	res := call(f.s.main, http.MethodPost, commandsPath(p), commandBatches(t,
		[]wireCommand{{Type: "project.rename", Payload: map[string]string{"name": "First"}}},
		// Stale once the first batch has applied.
		[]wireCommand{{Type: "project.rename", Payload: map[string]string{"name": "Stale"},
			ExpectedRev: map[string]int64{p: 1}}},
		[]wireCommand{{Type: "project.describe", Payload: map[string]string{"description": "Third"}}},
		[]wireCommand{{Type: "project.unknown"}},
		[]wireCommand{{Type: "project.rename", Payload: map[string]string{"name": " "}},
			{Type: "project.describe", Payload: map[string]string{"description": "Never"}}},
		[]wireCommand{{Type: "project.rename", Payload: map[string]string{"name": "Last"}}},
	), admin.header(true))
	var body struct {
		Results []struct {
			Status    string               `json:"status"`
			Seq       int64                `json:"seq"`
			Revisions []entityRevisionBody `json:"revisions"`
			Error     *api.ErrorBody       `json:"error"`
		} `json:"results"`
	}
	if res.status != http.StatusOK || json.Unmarshal(res.body, &body) != nil || len(body.Results) != 6 {
		t.Fatalf("%d %s, want 200 and 6 results", res.status, res.body)
	}
	if lang := res.header.Get("Content-Language"); lang != "en" {
		t.Errorf("Content-Language is %q, want en", lang)
	}
	r := body.Results
	if r[0].Status != "applied" || r[0].Seq != 1 || !slices.Equal(r[0].Revisions, []entityRevisionBody{{p, 2}}) {
		t.Errorf("the first batch's result: %+v", r[0])
	}
	if r[1].Status != "rejected" || r[1].Error == nil || r[1].Error.Code != api.CodeRevisionConflict ||
		r[1].Error.RequestID == "" || r[1].Error.Message == "" || r[1].Revisions != nil || r[1].Seq != 0 {
		t.Errorf("the stale batch's result: %+v", r[1])
	} else if conflicts := r[1].Error.Details["conflicts"].([]any); len(conflicts) != 1 ||
		conflicts[0].(map[string]any)["current_rev"] != float64(2) {
		t.Errorf("the stale batch's conflicts: %v", conflicts)
	}
	if r[2].Status != "applied" || r[2].Seq != 2 {
		t.Errorf("the third batch's result: %+v", r[2])
	}
	for i, at := range map[int]string{3: "body.batches[3].commands[0].type",
		4: "body.batches[4].commands[0].payload.name"} {
		e := r[i].Error
		if r[i].Status != "rejected" || e == nil || e.Code != api.CodeValidationFailed {
			t.Errorf("batch %d's result: %+v", i, r[i])
			continue
		}
		if got := e.Details["errors"].([]any)[0].(map[string]any)["location"]; got != at {
			t.Errorf("batch %d's problem is at %v, want %s", i, got, at)
		}
	}
	if r[5].Status != "applied" || r[5].Seq != 3 {
		t.Errorf("the last batch's result: %+v", r[5])
	}
	pr := projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, admin.header(false)), http.StatusOK)
	if pr.Name != "Last" || pr.Description != "Third" || pr.Seq != 3 {
		t.Errorf("the project after the batches: %+v", pr)
	}
}

// TestCommandEndpointConflict checks that a batch with a stale expected_rev
// is answered with 409 revision_conflict, with the entity's current revision
// and state, and applies nothing.
//
// Verifies: FR-API-03/AC3
func TestCommandEndpointConflict(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	admin := f.as["admin"]
	p := f.project(t, "")
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t,
		wireCommand{Type: "project.rename", Payload: map[string]string{"name": "Now"}}), admin.header(true)))
	res := call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t,
		wireCommand{Type: "project.describe", Payload: map[string]string{"description": "Then"},
			ExpectedRev: map[string]int64{p: 1}}), admin.header(true))
	wantCode(t, "a stale batch", res, http.StatusConflict, api.CodeRevisionConflict)
	conflicts, _ := res.details(t)["conflicts"].([]any)
	if len(conflicts) != 1 {
		t.Fatalf("the conflicts: %s", res.body)
	}
	c := conflicts[0].(map[string]any)
	states, _ := res.details(t)["states"].(map[string]any)
	state, _ := states[p].(map[string]any)
	if c["id"] != p || c["expected_rev"] != float64(1) || c["current_rev"] != float64(2) || state["name"] != "Now" {
		t.Errorf("the conflict: %v, with the states %v", c, states)
	}
	if pr := projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, admin.header(false)),
		http.StatusOK); pr.Description != "" || pr.Seq != 1 {
		t.Errorf("the project after the stale batch: %+v", pr)
	}
}

// TestCommandEndpointMismatch checks that a batch of commands that were
// applied, grouped otherwise, is answered with already_applied, and that a
// batch of an applied command and a new one is answered with 422
// idempotency_mismatch, which lists the applied one, and applies nothing.
//
// Verifies: FR-API-03/AC6
func TestCommandEndpointMismatch(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	admin := f.as["admin"]
	p := f.project(t, "")
	c1 := wireCommand{ID: ids.New().String(), Type: "project.rename", Payload: map[string]string{"name": "One"}}
	c2 := wireCommand{ID: ids.New().String(), Type: "project.describe",
		Payload: map[string]string{"description": "Two"}}
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, c1, c2), admin.header(true)))
	if got := resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, c2),
		admin.header(true))); got.Status != "already_applied" || got.Seq != 1 ||
		!slices.Equal(got.Revisions, []entityRevisionBody{{p, 3}}) {
		t.Errorf("a batch of one of the applied commands: %+v", got)
	}
	c3 := wireCommand{ID: ids.New().String(), Type: "project.rename", Payload: map[string]string{"name": "Three"}}
	res := call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, c3, c1), admin.header(true))
	wantCode(t, "a batch of an applied command and a new one", res, http.StatusUnprocessableEntity,
		api.CodeIdempotencyMismatch)
	if got := res.details(t)["applied"]; !reflect.DeepEqual(got, []any{c1.ID}) {
		t.Errorf("details.applied is %v, want [%s]", got, c1.ID)
	}
	if pr := projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, admin.header(false)),
		http.StatusOK); pr.Name != "One" || pr.Seq != 1 {
		t.Errorf("the project after the mismatch: %+v", pr)
	}
}

// TestCommandEndpointBody checks that a body that is neither form, or that
// holds more batches or commands than the endpoint takes, is answered with
// 422 validation_failed, and that every problem of a batch's commands is
// located in the body, as body.commands[1].command_id, so that a client can
// tell which command to mend; and that in a list of batches, a batch that
// cannot be read, or has too many commands, refuses its own batch alone.
func TestCommandEndpointBody(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	admin := f.as["admin"]
	p, other := f.project(t, ""), f.project(t, "")
	rename := wireCommand{Type: "project.rename", Payload: map[string]string{"name": "Pier"}}
	many := func(n int) []wireCommand {
		out := make([]wireCommand, n)
		for i := range out {
			out[i] = rename
		}
		return out
	}
	batches := make([][]wireCommand, maxBatches+1)
	for i := range batches {
		batches[i] = []wireCommand{rename}
	}
	for _, tt := range []struct {
		name string
		body []byte
		want []string
	}{
		{"both forms", jsonBody(t, map[string]any{"commands": withIDs([]wireCommand{rename}),
			"batches": []any{map[string]any{"commands": withIDs([]wireCommand{rename})}}}), []string{"body"}},
		{"neither form", []byte(`{}`), []string{"body"}},
		{"no object", []byte(`[]`), []string{"body"}},
		{"no batches", []byte(`{"batches": []}`), []string{"body.batches"}},
		{"too many batches", commandBatches(t, batches...), []string{"body.batches"}},
		{"no commands", []byte(`{"commands": []}`), []string{"body.commands"}},
		{"too many commands", commandBatch(t, many(maxBatchCommands+1)...), []string{"body.commands"}},
		{"commands that are not valid", jsonBody(t, map[string]any{"commands": []any{
			withIDs([]wireCommand{rename})[0],
			map[string]any{"command_id": "x", "type": "project.rename", "payload": map[string]any{"name": 7}},
			map[string]any{"command_id": ids.New().String(), "type": "project.rename", "payload": map[string]any{},
				"expected_rev": map[string]any{"xx_1": 1}},
			map[string]any{"command_id": ids.New().String(), "type": "project.archive", "payload": map[string]any{},
				"surplus": true},
			map[string]any{"command_id": 7, "type": "project.archive", "payload": map[string]any{}},
		}}), []string{"body.commands[1].command_id", "body.commands[1].payload.name",
			"body.commands[2].expected_rev.xx_1", "body.commands[2].payload", "body.commands[3].surplus",
			"body.commands[4].command_id"}},
		// Only the batch's project has a revision that its commands may
		// expect, which would otherwise be taken for a project that no longer
		// exists.
		{"another project's revision", commandBatch(t, wireCommand{Type: "project.rename",
			Payload: map[string]string{"name": "Pier"}, ExpectedRev: map[string]int64{other: 1}}),
			[]string{"body.commands[0].expected_rev." + other}},
		// It is one of the batch's problems, which are listed together,
		// whether a command's JSON has one or its payload.
		{"another project's revision, beside a command_id that is not one", jsonBody(t, map[string]any{
			"commands": []any{
				map[string]any{"command_id": "x", "type": "project.rename", "payload": map[string]any{"name": "Pier"}},
				map[string]any{"command_id": ids.New().String(), "type": "project.rename",
					"payload": map[string]any{"name": " "}, "expected_rev": map[string]any{other: 1}},
			}}), []string{"body.commands[0].command_id", "body.commands[1].expected_rev." + other,
			"body.commands[1].payload.name"}},
		{"another project's revision, beside a payload that is not valid", commandBatch(t,
			wireCommand{Type: "project.rename", Payload: map[string]string{"name": " "}},
			wireCommand{Type: "project.rename", Payload: map[string]string{"name": "Pier"},
				ExpectedRev: map[string]int64{other: 1}}),
			[]string{"body.commands[0].payload.name", "body.commands[1].expected_rev." + other}},
		// A command that is null is not one, and a revision of the wrong
		// type is located at its key.
		{"a null command", []byte(`{"commands": [null]}`), []string{"body.commands[0]"}},
		{"a revision that is not an integer", jsonBody(t, map[string]any{"commands": []any{
			map[string]any{"command_id": ids.New().String(), "type": "project.rename",
				"payload": map[string]any{"name": "Pier"}, "expected_rev": map[string]any{p: "1"}},
		}}), []string{"body.commands[0].expected_rev." + p}},
		// A key that differs from a field's name only in case is no field of
		// the body, or of a command, where encoding/json would take it for
		// one, and keep the last of the two.
		{"a body's key in another case", jsonBody(t, map[string]any{"Commands": withIDs([]wireCommand{rename})}),
			[]string{"body.Commands"}},
		{"a command's key in another case", jsonBody(t, map[string]any{"commands": []any{
			map[string]any{"command_id": ids.New().String(), "type": "project.rename", "Type": "project.delete",
				"payload": map[string]any{"name": "Pier"}}}}), []string{"body.commands[0].Type"}},
	} {
		if got := problems(t, call(f.s.main, http.MethodPost, commandsPath(p), tt.body, admin.header(true))); !slices.Equal(
			got, tt.want) {
			t.Errorf("%s: the problems are at %q, want %q", tt.name, got, tt.want)
		}
	}
	// In a list, a batch of too many commands, or one that cannot be read,
	// as one with a key in another case, or null, refuses its own batch
	// alone.
	describe := wireCommand{Type: "project.describe", Payload: map[string]string{"description": "Quay"}}
	res := call(f.s.main, http.MethodPost, commandsPath(p), jsonBody(t, map[string]any{"batches": []any{
		map[string]any{"commands": withIDs(many(maxBatchCommands + 1))},
		map[string]any{"COMMANDS": withIDs([]wireCommand{describe})},
		map[string]any{"commands": 5},
		7,
		nil,
		map[string]any{"commands": withIDs([]wireCommand{rename})},
	}}), admin.header(true))
	var body struct {
		Results []commandOutcomeBody `json:"results"`
	}
	if res.status != http.StatusOK || json.Unmarshal(res.body, &body) != nil || len(body.Results) != 6 {
		t.Fatalf("a list with batches that cannot be applied: %d %s, want 200 and 6 results", res.status, res.body)
	}
	for i, at := range []string{"body.batches[0].commands", "body.batches[1].COMMANDS", "body.batches[2].commands",
		"body.batches[3]", "body.batches[4]"} {
		r := body.Results[i]
		if r.Status != "rejected" || r.Error == nil || r.Error.Code != api.CodeValidationFailed {
			t.Errorf("batch %d's result: %+v", i, r)
			continue
		}
		if got := r.Error.Details["errors"].([]any)[0].(map[string]any)["location"]; got != at {
			t.Errorf("batch %d's problem is at %v, want %s", i, got, at)
		}
	}
	if r := body.Results[5]; r.Status != "applied" || r.Seq != 1 {
		t.Errorf("the last batch's result: %+v", r)
	}
	if pr := projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, admin.header(false)),
		http.StatusOK); pr.Name != "Pier" || pr.Description != "" || pr.Seq != 1 {
		t.Errorf("the project after the requests: %+v", pr)
	}
}

// TestCommandEndpointNotJSON checks that a body that is not JSON that the
// API takes is answered as on every other route: one that is not JSON, or
// has a key twice, with 400 invalid_request, and one of another type than
// JSON with 415 unsupported_media_type.
func TestCommandEndpointNotJSON(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	admin := f.as["admin"]
	p := f.project(t, "")
	for _, body := range []string{`{"commands": [`, `{"commands": [], "commands": []}`} {
		wantCode(t, body, call(f.s.main, http.MethodPost, commandsPath(p), []byte(body), admin.header(true)),
			http.StatusBadRequest, api.CodeInvalidRequest)
	}
	header := http.Header{}
	maps.Copy(header, admin.header(true))
	maps.Copy(header, newClient())
	wantCode(t, "a body of text", send(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t,
		wireCommand{Type: "project.rename", Payload: map[string]string{"name": "Pier"}}), "text/plain", header),
		http.StatusUnsupportedMediaType, api.CodeUnsupportedMediaType)
	if pr := projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, admin.header(false)),
		http.StatusOK); pr.Seq != 0 {
		t.Errorf("the project after the requests: %+v", pr)
	}
}

// TestCommandEndpointSessionOnly checks that a batch with a command that
// only a session may apply, deleting or restoring the project, which
// FR-AUTH-04 makes session-only, is refused to an API token of an owner,
// with every scope, with 403 session_required, alone or in a list of
// batches, and applies nothing, while the owner's session applies it; and
// that the audit log records the token's first refusal, as it records a
// route's (NFR-SEC-05).
//
// Verifies: FR-AUTH-04/AC6
func TestCommandEndpointSessionOnly(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner := f.as["owner"]
	token := makeToken(t, f.s.main, owner, workspaceTokensPath(f.ws), scope.Names(scope.All())...)
	every := bearer(token.Secret)
	p, deleted := f.project(t, ""), f.project(t, "deleted")
	wantCode(t, "a token's deletion", call(f.s.main, http.MethodPost, commandsPath(p),
		commandBatch(t, wireCommand{Type: "project.delete"}), every), http.StatusForbidden, api.CodeSessionRequired)
	wantCode(t, "a token's restoration", call(f.s.main, http.MethodPost, commandsPath(deleted),
		commandBatch(t, wireCommand{Type: "project.restore"}), every), http.StatusForbidden, api.CodeSessionRequired)
	res := call(f.s.main, http.MethodPost, commandsPath(p), commandBatches(t,
		[]wireCommand{{Type: "project.rename", Payload: map[string]string{"name": "Pier"}},
			{Type: "project.delete"}},
		[]wireCommand{{Type: "project.rename", Payload: map[string]string{"name": "Quay"}}}), every)
	if res.status != http.StatusOK || !strings.Contains(string(res.body), `"code":"session_required"`) ||
		!strings.Contains(string(res.body), `"seq":1`) {
		t.Errorf("a token's list of a batch that deletes the project and one that renames it: %d %s", res.status,
			res.body)
	}
	if pr := projectFrom(t, call(f.s.main, http.MethodGet, projectPath(p), nil, owner.header(false)),
		http.StatusOK); pr.Name != "Quay" || pr.Seq != 1 {
		t.Errorf("the project after the token's batches: %+v", pr)
	}
	if n := f.count(t, "SELECT count(*) FROM project WHERE id = $1 AND deleted_at IS NOT NULL",
		decode(t, ids.Project, deleted)); n != 1 {
		t.Errorf("the deleted project was restored")
	}
	// The three refusals came within a minute, so only the first is recorded.
	if got := f.tokenAudit(t, token.ID); !slices.Equal(got, []string{"token.create success",
		"token.use_denied denied session_required"}) {
		t.Errorf("the audit log of the token, refused 3 times, is %q, want its creation and its first refusal", got)
	}
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(deleted),
		commandBatch(t, wireCommand{Type: "project.restore"}), owner.header(true)))
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p),
		commandBatch(t, wireCommand{Type: "project.delete"}), owner.header(true)))
}
