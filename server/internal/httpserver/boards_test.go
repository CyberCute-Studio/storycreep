package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

func boardsPath(project string) string           { return projectPath(project) + "/boards" }
func boardPath(project, board string) string     { return boardsPath(project) + "/" + board }
func foldersPath(project string) string          { return projectPath(project) + "/folders" }
func newBoardID() string                         { return ids.Encode(ids.Board, ids.New()) }
func newFolderID() string                        { return ids.Encode(ids.Folder, ids.New()) }
func boardCommand(typ string, p any) wireCommand { return wireCommand{Type: typ, Payload: p} }

// createBoard creates a board named name in the project, as si, through the
// command endpoint, and returns its ID.
func createBoard(t *testing.T, h http.Handler, si signedIn, project, name string) string {
	t.Helper()
	id := newBoardID()
	res := call(h, http.MethodPost, commandsPath(project), commandBatch(t, boardCommand("board.create",
		map[string]string{"id": id, "name": name})), si.header(true))
	if res.status != http.StatusOK {
		t.Fatalf("create the board %s: %d %s", name, res.status, res.body)
	}
	return id
}

// board adds a board to the project, as the superuser, and returns its ID.
func (f *rolesFixture) board(t *testing.T, project string) string {
	t.Helper()
	id := ids.New()
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO board (id, workspace_id, project_id, "order", name,
		slug) VALUES ($1, $2, $3, 'V', 'Harbour', $4)`, id, decode(t, ids.Workspace, f.ws), decode(t, ids.Project,
		project), "harbour-"+strings.ReplaceAll(id.String(), "-", "")); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.Board, id)
}

// folder adds an empty folder of boards to the project, as the superuser,
// and returns its ID.
func (f *rolesFixture) folder(t *testing.T, project string) string {
	t.Helper()
	id := ids.New()
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO folder (id, workspace_id, project_id, kind, name,
		"order") VALUES ($1, $2, $3, 'board', 'Act 1', 'V')`, id, decode(t, ids.Workspace, f.ws), decode(t,
		ids.Project, project)); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.Folder, id)
}

// problemsOf returns the code of the error envelope of res, and the
// locations of the problems in its details.errors.
func problemsOf(t *testing.T, res response) (api.Code, []string, map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    api.Code       `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res.body, &env); err != nil {
		t.Fatalf("%d %s: %v", res.status, res.body, err)
	}
	var locs []string
	problems, _ := env.Error.Details["errors"].([]any)
	for _, p := range problems {
		if m, ok := p.(map[string]any); ok {
			loc, _ := m["location"].(string)
			locs = append(locs, loc)
		}
	}
	return env.Error.Code, locs, env.Error.Details
}

// TestBoards checks the routes that read a project's boards and folders,
// once its commands have made them: each lists those that are not deleted,
// page by page, and a board in the trash is not found.
func TestBoards(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	folder := newFolderID()
	ids3 := []string{newBoardID(), newBoardID(), newBoardID()}
	res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		boardCommand("folder.create", map[string]string{"id": folder, "kind": "board", "name": "Act 1"}),
		boardCommand("board.create", map[string]string{"id": ids3[0], "name": "Harbour Intro", "folder_id": folder,
			"color": "teal", "description": "Where it starts"}),
		boardCommand("board.create", map[string]string{"id": ids3[1], "name": "Harbour Intro"}),
		boardCommand("board.create", map[string]string{"id": ids3[2], "name": "Pier"})), owner.header(true))
	if res.status != http.StatusOK {
		t.Fatalf("create the boards: %d %s", res.status, res.body)
	}
	var b board.Board
	if res := call(s.main, http.MethodGet, boardPath(p.ID, ids3[0]), nil, owner.header(false)); res.status !=
		http.StatusOK || json.Unmarshal(res.body, &b) != nil {
		t.Fatalf("read the board: %d %s", res.status, res.body)
	}
	if b.Slug != "harbour-intro" || b.FolderID != folder || b.Color != "teal" || b.Description != "Where it starts" ||
		b.Order != "V" || b.Rev != 1 || b.ProjectID != p.ID || b.DeletedAt != nil {
		t.Errorf("the board is %+v", b)
	}
	if res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		boardCommand("board.delete", map[string]string{"id": ids3[2]})), owner.header(true)); res.status !=
		http.StatusOK {
		t.Fatalf("delete the board: %d %s", res.status, res.body)
	}
	wantCode(t, "a board in the trash", call(s.main, http.MethodGet, boardPath(p.ID, ids3[2]), nil,
		owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	wantCode(t, "a board of no project", call(s.main, http.MethodGet, boardPath(p.ID, newBoardID()), nil,
		owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	var listed []string
	for cursor := ""; ; {
		var page api.Page[board.Board]
		res := call(s.main, http.MethodGet, boardsPath(p.ID)+"?limit=1&cursor="+cursor, nil, owner.header(false))
		if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
			t.Fatalf("list the boards: %d %s", res.status, res.body)
		}
		for _, b := range page.Items {
			listed = append(listed, b.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	want := slices.Clone(ids3[:2])
	slices.Sort(want)
	if !slices.Equal(listed, want) {
		t.Errorf("the boards listed are %v, want %v", listed, want)
	}
	for query, n := range map[string]int{"": 1, "?kind=board": 1, "?kind=variable": 0} {
		var page api.Page[board.Folder]
		res := call(s.main, http.MethodGet, foldersPath(p.ID)+query, nil, owner.header(false))
		if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil || len(page.Items) != n {
			t.Errorf("the folders%s: %d %s, want %d", query, res.status, res.body, n)
		}
	}
}

// TestBoardRefusals checks how the API answers a command that refuses its
// batch for what the project's state makes of its payload, at the location
// of the payload's field: a slug that is not file-name safe on every
// platform, or that is a name that Windows reserves for a device, with 422
// slug_invalid, typed when a board is created or its slug changed, or when a
// project's is; a slug that another board has with 409 slug_taken; a folder
// that holds boards with 409 folder_not_empty; and any other value that the
// project does not take with 422 validation_failed.
//
// Verifies: FR-PROJ-02/AC4
func TestBoardRefusals(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	folder, taken := newFolderID(), newBoardID()
	if res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		boardCommand("folder.create", map[string]string{"id": folder, "kind": "board", "name": "Act 1"}),
		boardCommand("board.create", map[string]string{"id": taken, "name": "Pier", "slug": "pier",
			"folder_id": folder})), owner.header(true)); res.status != http.StatusOK {
		t.Fatalf("create the board: %d %s", res.status, res.body)
	}
	for _, c := range []struct {
		cmds   []wireCommand
		status int
		code   api.Code
		loc    string
		reason string
	}{
		{[]wireCommand{boardCommand("board.create", map[string]string{"id": newBoardID(), "name": "Con",
			"slug": "con"})}, http.StatusUnprocessableEntity, api.CodeSlugInvalid, "body.commands[0].payload.slug",
			"reserved"},
		{[]wireCommand{boardCommand("board.rename", map[string]string{"id": taken, "name": "Docks"}),
			boardCommand("board.set_slug", map[string]string{"id": taken, "slug": "Docks.json"})},
			http.StatusUnprocessableEntity, api.CodeSlugInvalid, "body.commands[1].payload.slug", "format"},
		{[]wireCommand{boardCommand("board.set_slug", map[string]string{"id": taken,
			"slug": strings.Repeat("a", 65)})}, http.StatusUnprocessableEntity, api.CodeSlugInvalid,
			"body.commands[0].payload.slug", "format"},
		{[]wireCommand{boardCommand("board.create", map[string]string{"id": newBoardID(), "name": "Docks",
			"slug": "pier"})}, http.StatusConflict, api.CodeSlugTaken, "body.commands[0].payload.slug", ""},
		{[]wireCommand{boardCommand("folder.delete", map[string]string{"id": folder})}, http.StatusConflict,
			api.CodeFolderNotEmpty, "body.commands[0].payload.id", ""},
		{[]wireCommand{boardCommand("board.move", map[string]string{"id": taken, "folder_id": newFolderID()})},
			http.StatusUnprocessableEntity, api.CodeValidationFailed, "body.commands[0].payload.folder_id", ""},
		{[]wireCommand{boardCommand("board.rename", map[string]string{"id": newBoardID(), "name": "Docks"})},
			http.StatusUnprocessableEntity, api.CodeValidationFailed, "body.commands[0].payload.id", ""},
	} {
		res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t, c.cmds...), owner.header(true))
		code, locs, details := problemsOf(t, res)
		if res.status != c.status || code != c.code || !slices.Equal(locs, []string{c.loc}) ||
			c.reason != "" && details["reason"] != c.reason {
			t.Errorf("%v: %d %s, want %d %s at %s", c.cmds, res.status, res.body, c.status, c.code, c.loc)
		}
	}
	// A project's slug is checked as a board's, and is answered at the field
	// of the body that it came from.
	for slug, reason := range map[string]string{"nul": "reserved", "Harbour": "format"} {
		res := call(s.main, http.MethodPatch, projectPath(p.ID), jsonBody(t, map[string]string{"slug": slug}),
			owner.header(true))
		code, locs, details := problemsOf(t, res)
		if res.status != http.StatusUnprocessableEntity || code != api.CodeSlugInvalid ||
			!slices.Equal(locs, []string{"body.slug"}) || details["reason"] != reason {
			t.Errorf("a project's slug %q: %d %s", slug, res.status, res.body)
		}
	}
	// A slug is answered alike, for each reason, on every route that takes
	// one, a workspace's as a board's.
	for slug, reason := range map[string]string{"con": "reserved", "Docks.json": "format"} {
		var messages []string
		for _, res := range []response{
			call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t, boardCommand("board.create",
				map[string]string{"id": newBoardID(), "name": "Docks", "slug": slug})), owner.header(true)),
			call(s.main, http.MethodPatch, workspacesPath+"/"+ws, jsonBody(t, map[string]string{"slug": slug}),
				owner.header(true)),
		} {
			_, _, details := problemsOf(t, res)
			if problems, _ := details["errors"].([]any); len(problems) == 1 && details["reason"] == reason {
				problem, _ := problems[0].(map[string]any)
				message, _ := problem["message"].(string)
				messages = append(messages, message)
			}
		}
		if len(messages) != 2 || messages[0] == "" || messages[0] != messages[1] {
			t.Errorf("the slug %q is answered with the messages %q by a board's command and a workspace's route, "+
				"want one message", slug, messages)
		}
	}
	got := projectFrom(t, call(s.main, http.MethodPatch, projectPath(p.ID), jsonBody(t, map[string]string{
		"slug": "harbour-tales", "name": "Lighthouse"}), owner.header(true)), http.StatusOK)
	if got.Slug != "harbour-tales" || got.Name != "Lighthouse" {
		t.Errorf("the project is %q, %q", got.Name, got.Slug)
	}
	// Nothing of the refused batches applied.
	var b board.Board
	if res := call(s.main, http.MethodGet, boardPath(p.ID, taken), nil, owner.header(false)); res.status !=
		http.StatusOK || json.Unmarshal(res.body, &b) != nil || b.Name != "Pier" || b.Rev != 1 {
		t.Errorf("after the refusals, the board is %d %s", res.status, res.body)
	}
}

// TestFolderKinds checks that the kind by which GET
// /api/v1/projects/{project_id}/folders filters takes the kinds of folder
// that package board lists, which its commands' payloads and its answers take
// too (board's TestSchemaEnums), so that a kind added to the list and not to
// the route's enum, or to the enum and not to the list, fails.
func TestFolderKinds(t *testing.T) {
	t.Parallel()
	f, ok := reflect.TypeFor[foldersInput]().FieldByName("Kind")
	if got, want := f.Tag.Get("enum"), strings.Join(board.Kinds, ","); !ok || got != want {
		t.Errorf("the route's kind takes %q, want %q", got, want)
	}
}
