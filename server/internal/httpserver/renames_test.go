package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// renameWorld is a project of the boards A and B, whose expressions name the
// global variables rep and flag, board B's variable trust, and the runtime
// attribute trust of the component elara, as FR-VAR-03 AC4's fixture has
// them:
//
//   - on A, the element onA, whose on-enter script is flag = rep >= 3;
//   - on B, the element onB, whose on-enter script is flag = rep >= 3 and
//     trust += 1, and onB2, whose on-enter script is
//     flag = global.rep > trust, and whose on-exit script is
//     elara.trust += rep.
type renameWorld struct {
	s                 *server
	admin             *pgx.Conn
	owner             signedIn
	project           string
	a, b              string
	rep, flag, trust  string
	cast, elara, attr string
	onA, onB, onB2    string
}

// newRenameWorld returns a new renameWorld, made in one batch, of a server
// of its own.
func newRenameWorld(t *testing.T) *renameWorld {
	t.Helper()
	s, d, owner, ws := inviteServer(t, "open", nil)
	w := &renameWorld{s: s, admin: d.Admin(t), owner: owner, project: createProject(t, s.main, owner, ws, "Isle").ID,
		a: newBoardID(), b: newBoardID(), rep: newVariableID(), flag: newVariableID(), trust: newVariableID(),
		cast: newComponentTypeID(), elara: newComponentID(), attr: newAttributeID(), onA: newNodeID(),
		onB: newNodeID(), onB2: newNodeID()}
	pos := map[string]int{"x": 0, "y": 0}
	w.apply(t, http.StatusOK,
		cmd("board.create", map[string]any{"id": w.a, "name": "A"}),
		cmd("board.create", map[string]any{"id": w.b, "name": "B"}),
		cmd("variable.create", map[string]any{"id": w.rep, "key": "rep", "type": "int", "scope": "global"}),
		cmd("variable.create", map[string]any{"id": w.flag, "key": "flag", "type": "bool", "scope": "global"}),
		cmd("variable.create", map[string]any{"id": w.trust, "key": "trust", "type": "int", "scope": "board",
			"board_id": w.b}),
		cmd("component_type.create", map[string]any{"id": w.cast, "name": "Cast"}),
		cmd("attribute.create", map[string]any{"id": w.attr, "component_type_id": w.cast, "name": "Trust",
			"type": "int", "runtime": true}),
		cmd("component.create", map[string]any{"id": w.elara, "component_type_id": w.cast, "name": "Elara"}),
		cmd("node.create", map[string]any{"id": w.onA, "board_id": w.a, "type": "element", "position": pos,
			"on_enter": "flag = rep >= 3"}),
		cmd("node.create", map[string]any{"id": w.onB, "board_id": w.b, "type": "element", "position": pos,
			"on_enter": "flag = rep >= 3\ntrust += 1"}),
		cmd("node.create", map[string]any{"id": w.onB2, "board_id": w.b, "type": "element", "position": pos,
			"on_enter": "flag = global.rep > trust", "on_exit": "elara.trust += rep"}),
	)
	return w
}

// cmd returns a command of the type with the payload.
func cmd(typ string, p map[string]any) wireCommand { return wireCommand{Type: typ, Payload: p} }

// apply sends the batch of cmds as the world's owner, fails the test unless
// it is answered with status, and returns the answer.
func (w *renameWorld) apply(t *testing.T, status int, cmds ...wireCommand) response {
	t.Helper()
	res := call(w.s.main, http.MethodPost, commandsPath(w.project), commandBatch(t, cmds...), w.owner.header(true))
	if res.status != status {
		t.Fatalf("the batch %s: %d %s, want %d", undoTypes(cmds), res.status, res.body, status)
	}
	return res
}

// storedExpression is an expression as the database holds it: its source,
// whether it compiles, and its AST without the names that it writes for
// debugging, which a rename changes, so that two of them name the same
// entities exactly when their ASTs are equal.
type storedExpression struct {
	Source string
	Valid  bool
	AST    string
}

// expressionsOf returns the expressions of the project, by their owners' IDs
// and their fields, as on_enter of nd_….
func expressionsOf(t *testing.T, admin *pgx.Conn, project string) map[string]storedExpression {
	t.Helper()
	rows, err := admin.Query(context.Background(), `SELECT coalesce(node_id, connection_id, branch_output_id)::text,
			CASE WHEN node_id IS NOT NULL THEN 'node' WHEN connection_id IS NOT NULL THEN 'connection'
				ELSE 'output' END, field, source, is_valid, coalesce(ast::text, 'null')
		FROM expression WHERE project_id = $1`, uuidOf(t, project))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]storedExpression{}
	for rows.Next() {
		var owner, kind, field, ast string
		var e storedExpression
		if err := rows.Scan(&owner, &kind, &field, &e.Source, &e.Valid, &ast); err != nil {
			t.Fatal(err)
		}
		u, err := ids.ParseUUID(owner)
		if err != nil {
			t.Fatal(err)
		}
		k := map[string]ids.Kind{"node": ids.Node, "connection": ids.Connection, "output": ids.BranchOutput}[kind]
		e.AST = unnamed(t, ast)
		out[field+" of "+ids.Encode(k, u)] = e
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// unnamed returns the JSON of the AST ast, the names that it writes for
// debugging left out, with its keys in order.
func unnamed(t *testing.T, ast string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(ast), &v); err != nil {
		t.Fatal(err)
	}
	var strip func(any)
	strip = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			delete(v, "name")
			for _, c := range v {
				strip(c)
			}
		case []any:
			for _, c := range v {
				strip(c)
			}
		}
	}
	strip(v)
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// wantSources fails the test unless the expressions of the project are got's,
// with the sources of want instead, by their keys, each of them valid and
// naming what got's named, as their ASTs show, but for the names.
func wantSources(t *testing.T, when string, got, before map[string]storedExpression, want map[string]string) {
	t.Helper()
	if !slices.Equal(slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(before))) {
		t.Fatalf("%s, the expressions are %v, want %v", when, slices.Sorted(maps.Keys(got)),
			slices.Sorted(maps.Keys(before)))
	}
	for key, e := range got {
		source := before[key].Source
		if s, ok := want[key]; ok {
			source = s
		}
		if e.Source != source {
			t.Errorf("%s, the %s is %q, want %q", when, key, e.Source, source)
		}
		if !e.Valid || e.AST != before[key].AST {
			t.Errorf("%s, the %s, %q, compiles to %s (valid %v), want %s", when, key, e.Source, e.AST, e.Valid,
				before[key].AST)
		}
	}
}

// seqOf returns the project's seq, the seq of its latest operation.
func seqOf(t *testing.T, admin *pgx.Conn, project string) int64 {
	t.Helper()
	var seq int64
	if err := admin.QueryRow(context.Background(), "SELECT seq FROM project WHERE id = $1",
		uuidOf(t, project)).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	return seq
}

// TestRenameRewritesSources checks FR-VAR-03 AC4's fixture: renaming the
// global variable rep to trust, the key of board B's variable, rewrites board
// B's rep as global.trust, the other boards' as trust, and leaves board B's
// own trust as it is; and renaming board B's trust to flag, the key of a
// global variable, rewrites board B's bare names of the global flag as
// global.flag. Each rewritten source compiles to the AST that it compiled to
// before, but for the names that the AST writes for debugging (AC2), and a
// rename to a key that another variable of the same scope has is refused
// before it writes anything, while one to a key of another scope applies
// (AC3). The undo of each rename gives the sources back as they were.
//
// Verifies: FR-VAR-03/AC2, FR-VAR-03/AC3, FR-VAR-03/AC4
func TestRenameRewritesSources(t *testing.T) {
	t.Parallel()
	w := newRenameWorld(t)
	original := expressionsOf(t, w.admin, w.project)
	onA, onB, onB2 := "on_enter of "+w.onA, "on_enter of "+w.onB, "on_enter of "+w.onB2
	exitB2 := "on_exit of " + w.onB2

	first := w.apply(t, http.StatusOK, cmd("variable.rename", map[string]any{"id": w.rep, "key": "trust"}))
	renamed := expressionsOf(t, w.admin, w.project)
	wantSources(t, "once rep is renamed to trust", renamed, original, map[string]string{
		onA:    "flag = trust >= 3",
		onB:    "flag = global.trust >= 3\ntrust += 1",
		onB2:   "flag = global.trust > trust",
		exitB2: "elara.trust += global.trust",
	})

	// AC3: a key that another global variable has is refused, and nothing is
	// written.
	seq := seqOf(t, w.admin, w.project)
	res := w.apply(t, http.StatusConflict, cmd("variable.rename", map[string]any{"id": w.rep, "key": "flag"}))
	wantCode(t, "a rename to another global's key", res, http.StatusConflict, api.CodeKeyTaken)
	if got := seqOf(t, w.admin, w.project); got != seq {
		t.Errorf("the refused rename moved the project's seq from %d to %d", seq, got)
	}
	wantSources(t, "once a rename is refused", expressionsOf(t, w.admin, w.project), renamed, nil)

	// The reverse case: board B's trust renamed to flag, a global's key,
	// which another scope's variable has, qualifies board B's bare flag.
	reverse := resultFrom(t, w.apply(t, http.StatusOK, cmd("variable.rename", map[string]any{"id": w.trust,
		"key": "flag"})))
	wantSources(t, "once board B's trust is renamed to flag", expressionsOf(t, w.admin, w.project), original,
		map[string]string{
			onA:    "flag = trust >= 3",
			onB:    "global.flag = global.trust >= 3\nflag += 1",
			onB2:   "global.flag = global.trust > flag",
			exitB2: "elara.trust += global.trust",
		})

	// The undo of each rename, the last first, gives back the sources as
	// they were: the bare flag that the second qualified, which a rename back
	// to the key would leave qualified, and global.rep, which the first
	// shortened.
	w.apply(t, http.StatusOK, undoCommands(reverse.Undo)...)
	if got := expressionsOf(t, w.admin, w.project); !reflect.DeepEqual(got, renamed) {
		t.Fatalf("once board B's variable's rename is undone, the expressions are %v, want %v", got, renamed)
	}
	undo := resultFrom(t, first).Undo
	for i, u := range undo {
		// The client's store moves what the undo expects of the nodes that
		// the batches since changed.
		for id := range u.ExpectedRev {
			undo[i].ExpectedRev[id] = revisionOf(t, w.admin, id)
		}
	}
	w.apply(t, http.StatusOK, undoCommands(undo)...)
	wantSources(t, "once the first rename is undone", expressionsOf(t, w.admin, w.project), original, nil)
}

// revisionOf returns the revision of the variable or the node whose ID is id.
func revisionOf(t *testing.T, admin *pgx.Conn, id string) int64 {
	t.Helper()
	table := "node"
	if strings.HasPrefix(id, ids.Variable.Prefix()+"_") {
		table = "variable"
	}
	var rev int64
	if err := admin.QueryRow(context.Background(), "SELECT rev FROM "+table+" WHERE id = $1",
		uuidOf(t, id)).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	return rev
}

// TestRenameAcrossSites checks that renaming a variable that 500 expressions
// name, on two boards, some of them as global.rep, is one command, applied as
// one operation of the log, which rewrites every one of them, and whose undo
// gives every source back exactly as it was, and whose redo rewrites them
// again (PRD FR-VAR-03 AC1).
//
// Verifies: FR-VAR-03/AC1
func TestRenameAcrossSites(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	admin := d.Admin(t)
	p := createProject(t, s.main, owner, ws, "Isle").ID
	a, b, rep := newBoardID(), newBoardID(), newVariableID()
	cmds := []wireCommand{
		cmd("board.create", map[string]any{"id": a, "name": "A"}),
		cmd("board.create", map[string]any{"id": b, "name": "B"}),
		cmd("variable.create", map[string]any{"id": rep, "key": "rep", "type": "int", "scope": "global"}),
	}
	for i := range 500 {
		board, source := a, "rep += 1"
		if i%2 == 1 {
			board, source = b, fmt.Sprintf("global.rep = rep * %d", i)
		}
		cmds = append(cmds, cmd("node.create", map[string]any{"id": newNodeID(), "board_id": board,
			"type": "element", "position": map[string]int{"x": i, "y": 0}, "on_enter": source}))
	}
	if res := call(s.main, http.MethodPost, commandsPath(p), commandBatch(t, cmds...), owner.header(true)); res.status !=
		http.StatusOK {
		t.Fatalf("make the project: %d %s", res.status, res.body)
	}
	before := expressionsOf(t, admin, p)
	operations := func() int64 {
		var n int64
		if err := admin.QueryRow(context.Background(), "SELECT count(*) FROM operation WHERE project_id = $1",
			uuidOf(t, p)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	ops := operations()
	res := call(s.main, http.MethodPost, commandsPath(p), commandBatch(t, cmd("variable.rename",
		map[string]any{"id": rep, "key": "reputation"})), owner.header(true))
	result := resultFrom(t, res)
	if got := operations(); got != ops+1 {
		t.Errorf("the rename added %d operations to the log, want 1", got-ops)
	}
	want := map[string]string{}
	for key, e := range before {
		want[key] = strings.NewReplacer("global.rep", "reputation", "rep", "reputation").Replace(e.Source)
	}
	wantSources(t, "once rep is renamed", expressionsOf(t, admin, p), before, want)
	if len(result.Undo) != 1 || result.Undo[0].Type != "variable.rename" {
		t.Fatalf("the rename's undo is %s, want one variable.rename", res.body)
	}
	undone := resultFrom(t, call(s.main, http.MethodPost, commandsPath(p), commandBatch(t, undoCommands(result.Undo)...),
		owner.header(true)))
	wantSources(t, "once the rename is undone", expressionsOf(t, admin, p), before, nil)
	call(s.main, http.MethodPost, commandsPath(p), commandBatch(t, undoCommands(undone.Undo)...), owner.header(true))
	wantSources(t, "once the rename is redone", expressionsOf(t, admin, p), before, want)
}

// TestRenameUndoKeepsLaterWork checks what the undo of a rename gives back
// once work has gone on since: exactly the source that the rename rewrote of
// an expression whose owner was in the trash at the rename and still has what
// the rename wrote, while one whose owner has been restored and edited since,
// whose revision the undo cannot expect, is rewritten rather than written
// back over, and so is one written since that names the variable by its new
// key, which the undo would leave naming nothing; an expression that is gone
// since is passed over; and a rename whose sources a client made up writes
// none that does not name and do what its expression does.
//
// Verifies: FR-VAR-03/AC1
func TestRenameUndoKeepsLaterWork(t *testing.T) {
	t.Parallel()
	w := newRenameWorld(t)
	pos := map[string]int{"x": 0, "y": 0}
	kept, edited, gone, later := newNodeID(), newNodeID(), newNodeID(), newNodeID()
	var create, trash []wireCommand
	for _, n := range []string{kept, edited, gone} {
		create = append(create, cmd("node.create", map[string]any{"id": n, "board_id": w.a, "type": "element",
			"position": pos, "on_enter": "global.rep += 1"}))
		trash = append(trash, cmd("node.delete", map[string]any{"id": n}))
	}
	w.apply(t, http.StatusOK, create...)
	w.apply(t, http.StatusOK, trash...)
	original := expressionsOf(t, w.admin, w.project)

	renamed := resultFrom(t, w.apply(t, http.StatusOK, cmd("variable.rename", map[string]any{"id": w.rep,
		"key": "trust"})))
	for _, n := range []string{kept, edited, gone} {
		if got := expressionsOf(t, w.admin, w.project)["on_enter of "+n].Source; got != "trust += 1" {
			t.Errorf("the rename rewrote the on-enter script of %s, in the trash, as %q, want %q", n, got, "trust += 1")
		}
	}
	// The work since the rename: a node in the trash restored and edited, a
	// node written that names the variable by its new key, and a node's
	// expression purged.
	w.apply(t, http.StatusOK,
		cmd("node.restore", map[string]any{"id": edited}),
		cmd("node.update", map[string]any{"id": edited, "on_enter": "trust += 5"}),
		cmd("node.create", map[string]any{"id": later, "board_id": w.a, "type": "element", "position": pos,
			"on_enter": "trust -= 2"}))
	if _, err := w.admin.Exec(context.Background(), "DELETE FROM expression WHERE node_id = $1",
		uuidOf(t, gone)); err != nil {
		t.Fatal(err)
	}

	w.apply(t, http.StatusOK, undoCommands(renamed.Undo)...)
	want := map[string]string{}
	for key, e := range original {
		want[key] = e.Source
	}
	delete(want, "on_enter of "+gone)
	want["on_enter of "+edited], want["on_enter of "+later] = "rep += 5", "rep -= 2"
	got := expressionsOf(t, w.admin, w.project)
	if !slices.Equal(slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want))) {
		t.Fatalf("once the rename is undone, the expressions are %v, want %v", slices.Sorted(maps.Keys(got)),
			slices.Sorted(maps.Keys(want)))
	}
	for key, e := range got {
		if e.Source != want[key] || !e.Valid {
			t.Errorf("once the rename is undone, the %s is %q (valid %v), want %q, valid", key, e.Source, e.Valid,
				want[key])
		}
	}

	// A rename of flag, whose sources a client made up: one that does
	// something else than onB's on-enter script, which the rename rewrites as
	// it would without it, and one for kept's, which does not name flag, and
	// which the rename leaves as it is.
	before := expressionsOf(t, w.admin, w.project)
	w.apply(t, http.StatusOK, cmd("variable.rename", map[string]any{"id": w.flag, "key": "banner",
		"sources": []map[string]any{{"owner": w.onB, "field": "on_enter", "source": "rep = 0"},
			{"owner": kept, "field": "on_enter", "source": "rep = 99"}}}))
	wantSources(t, "once flag is renamed with made-up sources", expressionsOf(t, w.admin, w.project), before,
		map[string]string{
			"on_enter of " + w.onA:  "banner = rep >= 3",
			"on_enter of " + w.onB:  "banner = rep >= 3\ntrust += 1",
			"on_enter of " + w.onB2: "banner = global.rep > trust",
		})
}

// TestRenamePreview checks the previews of a variable's rename and of a
// component's change of key: each lists the expressions that the command
// would rewrite, those of the names of what it renames apart from those that
// it qualifies, page by page, without changing anything, and refuses a key
// as the command would.
//
// Verifies: FR-VAR-03/AC4
func TestRenamePreview(t *testing.T) {
	t.Parallel()
	w := newRenameWorld(t)
	original := expressionsOf(t, w.admin, w.project)
	preview := func(path string, limit int) []expression.RenameSite {
		t.Helper()
		var out []expression.RenameSite
		for cursor := ""; ; {
			url := fmt.Sprintf("%s&limit=%d", path, limit)
			if cursor != "" {
				url += "&cursor=" + cursor
			}
			res := call(w.s.main, http.MethodGet, url, nil, w.owner.header(false))
			var page api.Page[expression.RenameSite]
			if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
				t.Fatalf("GET %s: %d %s", url, res.status, res.body)
			}
			out = append(out, page.Items...)
			if page.NextCursor == nil {
				return out
			}
			cursor = *page.NextCursor
		}
	}
	lines := func(sites []expression.RenameSite) []string {
		var out []string
		for _, s := range sites {
			var names []string
			for _, n := range s.Names {
				names = append(names, fmt.Sprintf("%s->%s", n.From, n.To))
			}
			out = append(out, fmt.Sprintf("%s %s/%s %s: %s", s.Site, s.BoardName, s.NodeID, strings.Join(names, " "),
				s.Rewritten))
		}
		slices.Sort(out)
		return out
	}
	v := variablePath(w.project, w.rep)
	got := lines(preview(renamePreviewPath(v, "trust", "renamed"), 1))
	want := []string{
		"node_on_enter A/" + w.onA + " rep->trust: flag = trust >= 3",
		"node_on_enter B/" + w.onB + " rep->global.trust: flag = global.trust >= 3\ntrust += 1",
		"node_on_enter B/" + w.onB2 + " global.rep->global.trust: flag = global.trust > trust",
		"node_on_exit B/" + w.onB2 + " rep->global.trust: elara.trust += global.trust",
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the rename of rep to trust rewrites %q, want %q", got, want)
	}
	if got := preview(renamePreviewPath(v, "trust", "qualified"), 10); len(got) != 0 {
		t.Errorf("the rename of rep to trust qualifies %v, want nothing", lines(got))
	}
	trust := variablePath(w.project, w.trust)
	if got, want := lines(preview(renamePreviewPath(trust, "flag", "qualified"), 10)), sorted(
		"node_on_enter B/"+w.onB+" flag->global.flag: global.flag = rep >= 3\nflag += 1",
		"node_on_enter B/"+w.onB2+" flag->global.flag: global.flag = global.rep > flag",
	); !slices.Equal(got, want) {
		t.Errorf("the rename of board B's trust to flag qualifies %q, want %q", got, want)
	}
	if got, want := lines(preview(renamePreviewPath(trust, "flag", "renamed"), 10)), sorted(
		"node_on_enter B/"+w.onB+" trust->flag: global.flag = rep >= 3\nflag += 1",
		"node_on_enter B/"+w.onB2+" trust->flag: global.flag = global.rep > flag",
	); !slices.Equal(got, want) {
		t.Errorf("the rename of board B's trust to flag renames %q, want %q", got, want)
	}
	c := componentPath(w.project, w.elara)
	if got, want := lines(preview(renamePreviewPath(c, "flag", "renamed"), 10)), []string{
		"node_on_exit B/" + w.onB2 + " elara.trust->cmp.flag.trust: cmp.flag.trust += rep",
	}; !slices.Equal(got, want) {
		t.Errorf("the change of elara's key to flag renames %q, want %q", got, want)
	}
	for _, c := range []struct {
		path   string
		status int
		code   api.Code
	}{
		{renamePreviewPath(v, "x y", "renamed"), http.StatusUnprocessableEntity, api.CodeKeyInvalid},
		{renamePreviewPath(v, "flag", "renamed"), http.StatusConflict, api.CodeKeyTaken},
		{renamePreviewPath(c, "global", "renamed"), http.StatusUnprocessableEntity, api.CodeKeyInvalid},
		{renamePreviewPath(variablePath(w.project, newVariableID()), "x", "renamed"), http.StatusNotFound,
			api.CodeNotFound},
		{renamePreviewPath(v, "x", "both"), http.StatusUnprocessableEntity, api.CodeValidationFailed},
	} {
		wantCode(t, c.path, call(w.s.main, http.MethodGet, c.path, nil, w.owner.header(false)), c.status, c.code)
	}
	wantSources(t, "after the previews", expressionsOf(t, w.admin, w.project), original, nil)
}

// sorted returns the lines, sorted.
func sorted(lines ...string) []string {
	slices.Sort(lines)
	return lines
}

// TestComponentKeyRefactor checks that renaming a component leaves its key as
// it is, and that changing its key rewrites the expressions that name its
// attributes, as a variable's rename does, in the shortest form that names
// them, cmp.<key>.<attribute> where a variable has the key, and that its undo
// gives them back as they were (PRD FR-COMP-02 AC2).
//
// Verifies: FR-COMP-02/AC2
func TestComponentKeyRefactor(t *testing.T) {
	t.Parallel()
	w := newRenameWorld(t)
	original := expressionsOf(t, w.admin, w.project)
	exitB2 := "on_exit of " + w.onB2
	w.apply(t, http.StatusOK, cmd("component.rename", map[string]any{"id": w.elara, "name": "Elara the Wise"}))
	res := call(w.s.main, http.MethodGet, componentPath(w.project, w.elara), nil, w.owner.header(false))
	var c struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if res.status != http.StatusOK || json.Unmarshal(res.body, &c) != nil || c.Key != "elara" ||
		c.Name != "Elara the Wise" {
		t.Errorf("once renamed, the component is %d %s, want the name Elara the Wise and the key elara", res.status,
			res.body)
	}
	wantSources(t, "once the component is renamed", expressionsOf(t, w.admin, w.project), original, nil)

	rekeyed := resultFrom(t, w.apply(t, http.StatusOK, cmd("component.set_key", map[string]any{"id": w.elara,
		"key": "ela"})))
	wantSources(t, "once elara's key is ela", expressionsOf(t, w.admin, w.project), original,
		map[string]string{exitB2: "ela.trust += rep"})
	w.apply(t, http.StatusOK, undoCommands(rekeyed.Undo)...)
	wantSources(t, "once the change of the key is undone", expressionsOf(t, w.admin, w.project), original, nil)

	// The key of board B's variable, which names it on board B.
	w.apply(t, http.StatusOK, cmd("component.set_key", map[string]any{"id": w.elara, "key": "trust"}))
	wantSources(t, "once elara's key is trust", expressionsOf(t, w.admin, w.project), original,
		map[string]string{exitB2: "cmp.trust.trust += rep"})
	wantCode(t, "a key of another component", w.apply(t, http.StatusConflict, cmd("component.create",
		map[string]any{"id": newComponentID(), "component_type_id": w.cast, "name": "Mira", "key": "trust"})),
		http.StatusConflict, api.CodeKeyTaken)
}
