package httpserver

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/trash"
)

func projectTrashPath(project string) string { return projectPath(project) + "/trash" }

// TestTrash checks the route that lists a project's trash: once the
// project's commands have deleted boards, it lists them, the most recently
// deleted first, page by page, each with when its 30 days end, but not the
// folder deleted with them, and no longer lists a board that a command
// restores, which comes back where it was, in its folder. A cursor that it
// did not write is answered with invalid_cursor.
func TestTrash(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	folder := newFolderID()
	boards := []string{newBoardID(), newBoardID(), newBoardID()}
	batches := [][]wireCommand{
		{boardCommand("folder.create", map[string]string{"id": folder, "kind": "board", "name": "Act 1"}),
			boardCommand("board.create", map[string]string{"id": boards[0], "name": "Harbour", "folder_id": folder}),
			boardCommand("board.create", map[string]string{"id": boards[1], "name": "Pier"}),
			boardCommand("board.create", map[string]string{"id": boards[2], "name": "Lighthouse"})},
		// Each deletion is a batch of its own, so that each has a time of its
		// own.
		{boardCommand("board.delete", map[string]string{"id": boards[0]}),
			boardCommand("folder.delete", map[string]string{"id": folder})},
		{boardCommand("board.delete", map[string]string{"id": boards[1]})},
		{boardCommand("board.delete", map[string]string{"id": boards[2]})},
	}
	for _, b := range batches {
		if res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t, b...), owner.header(true)); res.status !=
			http.StatusOK {
			t.Fatalf("%v: %d %s", b, res.status, res.body)
		}
	}
	// list returns every item of the project's trash, a page of one at a
	// time.
	list := func() []trash.Item {
		var all []trash.Item
		for cursor := ""; ; {
			var page api.Page[trash.Item]
			res := call(s.main, http.MethodGet, projectTrashPath(p.ID)+"?limit=1&cursor="+cursor, nil, owner.header(false))
			if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
				t.Fatalf("list the trash: %d %s", res.status, res.body)
			}
			all = append(all, page.Items...)
			if page.NextCursor == nil {
				return all
			}
			cursor = *page.NextCursor
		}
	}
	ids := func(items []trash.Item) []string {
		var out []string
		for _, it := range items {
			out = append(out, it.ID)
		}
		return out
	}
	items := list()
	if got, want := ids(items), []string{boards[2], boards[1], boards[0]}; !slices.Equal(got, want) {
		t.Errorf("the trash lists %v, want %v", got, want)
	}
	for _, it := range items {
		if it.Kind != "board" || it.PurgeAfter.Sub(it.DeletedAt) != 30*24*time.Hour ||
			time.Since(it.DeletedAt) > time.Hour {
			t.Errorf("the trash lists %+v", it)
		}
	}
	if len(items) == 3 && items[2].Name != "Harbour" {
		t.Errorf("the trash lists %+v, want the board named Harbour last", items[2])
	}

	idCursor, err := api.EncodeCursor(idKey{ID: "0192b9d0-4b4d-7c3e-8a9b-0c1d2e3f4a5b"})
	if err != nil {
		t.Fatal(err)
	}
	kindCursor, err := api.EncodeCursor(trashKey{DeletedAt: time.Now().UTC(), Kind: "folder",
		ID: "0192b9d0-4b4d-7c3e-8a9b-0c1d2e3f4a5b"})
	if err != nil {
		t.Fatal(err)
	}
	for name, cursor := range map[string]string{"not a cursor": "nope", "a cursor of another list": idCursor,
		"a cursor of a kind that the trash does not list": kindCursor} {
		wantCode(t, name, call(s.main, http.MethodGet, projectTrashPath(p.ID)+"?cursor="+cursor, nil, owner.header(false)),
			http.StatusBadRequest, api.CodeInvalidCursor)
	}

	// The command of its kind restores a board, in its folder, which comes
	// back with it.
	if res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t, boardCommand("board.restore",
		map[string]string{"id": boards[0]})), owner.header(true)); res.status != http.StatusOK {
		t.Fatalf("restore the board: %d %s", res.status, res.body)
	}
	if got, want := ids(list()), []string{boards[2], boards[1]}; !slices.Equal(got, want) {
		t.Errorf("after the restore, the trash lists %v, want %v", got, want)
	}
	var b board.Board
	if res := call(s.main, http.MethodGet, boardPath(p.ID, boards[0]), nil, owner.header(false)); res.status !=
		http.StatusOK || json.Unmarshal(res.body, &b) != nil || b.FolderID != folder {
		t.Errorf("the restored board: %d %s, want it in the folder %s", res.status, res.body, folder)
	}
}
