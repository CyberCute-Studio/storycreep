package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// TestRevisionConflict checks that a batch with stale revisions is answered
// with 409 revision_conflict, whose details list each stale entity with its
// command, the revision that the command expected, and the entity's current
// revision and state, or that it no longer exists, without either.
//
// Verifies: FR-API-03/AC3
func TestRevisionConflict(t *testing.T) {
	stale, gone := ids.New(), ids.New()
	first, second := ids.New(), ids.New()
	err := fmt.Errorf("apply: %w", &commands.RevisionConflictError{Conflicts: []commands.Conflict{
		{Command: 0, CommandID: first, Type: "node.update_text", Ref: commands.Ref{Kind: ids.Node, ID: stale},
			Expected: 4, Exists: true, Current: 5, State: json.RawMessage(`{"text": "now"}`)},
		{Command: 2, CommandID: second, Type: "connection.create", Ref: commands.Ref{Kind: ids.Node, ID: gone},
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
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res.body, &env); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"command": float64(0), "command_id": first.String(), "type": "node.update_text",
			"id": ids.Encode(ids.Node, stale), "expected_rev": float64(4), "exists": true, "current_rev": float64(5),
			"state": map[string]any{"text": "now"}},
		{"command": float64(2), "command_id": second.String(), "type": "connection.create",
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

	// Any other error is left to the route's own mapping.
	other := errors.New("the database is gone")
	if err := revisionConflictError(other); err != other {
		t.Errorf("revisionConflictError(%v) = %v, want it unchanged", other, err)
	}
}
