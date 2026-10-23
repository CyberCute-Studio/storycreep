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
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

func variablesPath(project string) string           { return projectPath(project) + "/variables" }
func variablePath(project, v string) string         { return variablesPath(project) + "/" + v }
func newVariableID() string                         { return ids.Encode(ids.Variable, ids.New()) }
func variableCommand(typ string, p any) wireCommand { return wireCommand{Type: typ, Payload: p} }

// variable adds a global bool variable to the project, as the superuser, and
// returns its ID.
func (f *rolesFixture) variable(t *testing.T, project string) string {
	t.Helper()
	id := ids.New()
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO variable (id, workspace_id, project_id, scope,
		"order", key, type, default_value) VALUES ($1, $2, $3, 'global', 'V', $4, 'bool', 'false')`, id,
		decode(t, ids.Workspace, f.ws), decode(t, ids.Project, project),
		"met_"+strings.ReplaceAll(id.String(), "-", "")); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.Variable, id)
}

// TestVariables checks the routes that read a project's variables, once its
// commands have made them: each lists those that are not in the trash, page
// by page, with their defaults as their types read them, and a variable in
// the trash, or of a board that is, is not found.
//
// Verifies: FR-VAR-01/AC2
func TestVariables(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	folder, harbour, pier := newFolderID(), newBoardID(), newBoardID()
	vars := []string{newVariableID(), newVariableID(), newVariableID(), newVariableID()}
	res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		variableCommand("folder.create", map[string]string{"id": folder, "kind": "variable", "name": "Quests"}),
		variableCommand("board.create", map[string]string{"id": harbour, "name": "Harbour"}),
		variableCommand("board.create", map[string]string{"id": pier, "name": "Pier"}),
		variableCommand("variable.create", map[string]any{"id": vars[0], "key": "rumours", "type": "int",
			"scope": "global", "default": json.RawMessage("3.0"), "description": "What the harbour knows",
			"folder_id": folder}),
		variableCommand("variable.create", map[string]any{"id": vars[1], "key": "asked", "type": "string",
			"scope": "board", "board_id": harbour, "reset_on_enter": true}),
		variableCommand("variable.create", map[string]any{"id": vars[2], "key": "gone", "type": "bool",
			"scope": "global"}),
		variableCommand("variable.create", map[string]any{"id": vars[3], "key": "waved", "type": "float",
			"scope": "board", "board_id": pier, "default": 0.5})), owner.header(true))
	if res.status != http.StatusOK {
		t.Fatalf("create the variables: %d %s", res.status, res.body)
	}
	var v struct {
		variable.Variable
		Default json.RawMessage `json:"default"`
	}
	if res := call(s.main, http.MethodGet, variablePath(p.ID, vars[0]), nil, owner.header(false)); res.status !=
		http.StatusOK || json.Unmarshal(res.body, &v) != nil {
		t.Fatalf("read the variable: %d %s", res.status, res.body)
	}
	if v.Key != "rumours" || v.Type != "int" || string(v.Default) != "3" || v.Scope != "global" || v.BoardID != "" ||
		v.FolderID != folder || v.Order != "V" || v.Description != "What the harbour knows" || v.ResetOnEnter ||
		v.Rev != 1 || v.ProjectID != p.ID || v.DeletedAt != nil {
		t.Errorf("the variable is %s", res.body)
	}
	if res := call(s.main, http.MethodGet, variablePath(p.ID, vars[1]), nil, owner.header(false)); res.status !=
		http.StatusOK || json.Unmarshal(res.body, &v) != nil || v.Scope != "board" || v.BoardID != harbour ||
		!v.ResetOnEnter || string(v.Default) != `""` {
		t.Errorf("the board's variable is %d %s", res.status, res.body)
	}
	if res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		variableCommand("variable.delete", map[string]string{"id": vars[2]}),
		variableCommand("board.delete", map[string]string{"id": pier})), owner.header(true)); res.status !=
		http.StatusOK {
		t.Fatalf("delete the variable and the board: %d %s", res.status, res.body)
	}
	for _, id := range []string{vars[2], vars[3], newVariableID()} {
		wantCode(t, "a variable in the trash, or of none", call(s.main, http.MethodGet, variablePath(p.ID, id), nil,
			owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	}
	var listed []string
	for cursor := ""; ; {
		var page api.Page[variable.Variable]
		res := call(s.main, http.MethodGet, variablesPath(p.ID)+"?limit=1&cursor="+cursor, nil, owner.header(false))
		if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
			t.Fatalf("list the variables: %d %s", res.status, res.body)
		}
		for _, v := range page.Items {
			listed = append(listed, v.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	want := slices.Clone(vars[:2])
	slices.Sort(want)
	if !slices.Equal(listed, want) {
		t.Errorf("the variables listed are %v, want %v", listed, want)
	}
}

// TestVariableRefusals checks how the API answers a command of a variable
// that refuses its batch, at the location of the payload's field: a key that
// is not an identifier, or that is a reserved word, with 422 key_invalid and
// the rule that it breaks, a key that another variable of the scope has with
// 409 key_taken, and a default that is not of the variable's type, as a
// string on an int, with 422 validation_failed.
//
// Verifies: FR-VAR-01/AC1, FR-VAR-01/AC2
func TestVariableRefusals(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	taken := newVariableID()
	if res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		variableCommand("variable.create", map[string]any{"id": taken, "key": "rumours", "type": "int",
			"scope": "global"})), owner.header(true)); res.status != http.StatusOK {
		t.Fatalf("create the variable: %d %s", res.status, res.body)
	}
	create := func(key, typ string, def any) wireCommand {
		p := map[string]any{"id": newVariableID(), "key": key, "type": typ, "scope": "global"}
		if def != nil {
			p["default"] = def
		}
		return variableCommand("variable.create", p)
	}
	for _, c := range []struct {
		cmds   []wireCommand
		status int
		code   api.Code
		loc    string
		reason string
	}{
		{[]wireCommand{create("9lives", "bool", nil)}, http.StatusUnprocessableEntity, api.CodeKeyInvalid,
			"body.commands[0].payload.key", "format"},
		{[]wireCommand{create("met-elara", "bool", nil)}, http.StatusUnprocessableEntity, api.CodeKeyInvalid,
			"body.commands[0].payload.key", "format"},
		{[]wireCommand{create("true", "bool", nil)}, http.StatusUnprocessableEntity, api.CodeKeyInvalid,
			"body.commands[0].payload.key", "reserved"},
		{[]wireCommand{variableCommand("variable.describe", map[string]string{"id": taken, "description": "Rumours"}),
			variableCommand("variable.rename", map[string]string{"id": taken, "key": "global"})},
			http.StatusUnprocessableEntity, api.CodeKeyInvalid, "body.commands[1].payload.key", "reserved"},
		{[]wireCommand{create("rumours", "bool", nil)}, http.StatusConflict, api.CodeKeyTaken,
			"body.commands[0].payload.key", ""},
		{[]wireCommand{create("count", "int", "3")}, http.StatusUnprocessableEntity, api.CodeValidationFailed,
			"body.commands[0].payload.default", ""},
		{[]wireCommand{variableCommand("variable.set_default", map[string]any{"id": taken, "default": "three"})},
			http.StatusUnprocessableEntity, api.CodeValidationFailed, "body.commands[0].payload.default", ""},
		{[]wireCommand{create("count", "int", 3.5)}, http.StatusUnprocessableEntity, api.CodeValidationFailed,
			"body.commands[0].payload.default", ""},
		{[]wireCommand{create("count", "int", []int{3})}, http.StatusUnprocessableEntity, api.CodeValidationFailed,
			"body.commands[0].payload.default", ""},
		// A number beyond a double's range is no number that the API reads, so
		// it refuses the body before a command reads its default.
		{[]wireCommand{create("far", "float", json.RawMessage("-1e400"))}, http.StatusBadRequest,
			api.CodeInvalidRequest, "body", ""},
		// A number written with more digits after its point than the operation
		// log holds, its exponent counted, is refused at its default.
		{[]wireCommand{create("near", "float", json.RawMessage("1e-16384"))}, http.StatusUnprocessableEntity,
			api.CodeValidationFailed, "body.commands[0].payload.default", ""},
		{[]wireCommand{variableCommand("variable.set_scope", map[string]string{"id": taken, "scope": "board",
			"board_id": newBoardID()})}, http.StatusUnprocessableEntity, api.CodeValidationFailed,
			"body.commands[0].payload.board_id", ""},
	} {
		res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t, c.cmds...), owner.header(true))
		code, locs, details := problemsOf(t, res)
		if res.status != c.status || code != c.code || !slices.Contains(locs, c.loc) ||
			c.reason != "" && details["reason"] != c.reason {
			t.Errorf("%v: %d %s, want %d %s at %s", c.cmds, res.status, res.body, c.status, c.code, c.loc)
		}
	}
	// Nothing of the refused batches applied.
	var v variable.Variable
	if res := call(s.main, http.MethodGet, variablePath(p.ID, taken), nil, owner.header(false)); res.status !=
		http.StatusOK || json.Unmarshal(res.body, &v) != nil || v.Key != "rumours" || v.Description != "" ||
		v.Rev != 1 {
		t.Errorf("after the refusals, the variable is %d %s", res.status, res.body)
	}
}
