package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
)

// nonUndoable are the types of command that the server applies that have no
// inverse, as Register makes sure each is marked (commands.Spec.NonUndoable),
// whose batches cannot be undone, each with why it has none, as PRD
// FR-EDIT-08 AC1 names publishing, importing, and deleting past the trash's
// grace: none so far. A type that is marked so joins it, so that a type's
// having no undo is a choice that this list records, not one that a
// declaration makes in passing.
var nonUndoable = map[string]string{}

// TestEveryCommandUndoable checks that every type of command that the server
// applies has an inverse, or is one that nonUndoable lists as having none, so
// that a batch of it cannot be undone, and that the property test of undo,
// TestUndoRoundTrip, covers every type that has one.
//
// Verifies: FR-EDIT-08/AC1
func TestEveryCommandUndoable(t *testing.T) {
	reg := newCommandRegistry()
	undoable := reg.Undoable()
	registered := map[string]bool{}
	for _, typ := range reg.Types() {
		registered[string(typ)] = true
		_, listed := nonUndoable[string(typ)]
		switch inverse := slices.Contains(undoable, typ); {
		case inverse && listed:
			t.Errorf("%s has an inverse, but nonUndoable lists it as having none", typ)
		case !inverse && !listed:
			t.Errorf("%s has no inverse: give it one, or list it in nonUndoable, with why it has none", typ)
		}
	}
	for _, typ := range slices.Sorted(maps.Keys(nonUndoable)) {
		if !registered[typ] {
			t.Errorf("nonUndoable lists %s, which is not a type of command that the server applies", typ)
		}
	}
	for _, typ := range undoable {
		if _, ok := undoGenerators[string(typ)]; !ok {
			t.Errorf("%s has an inverse, which TestUndoRoundTrip does not apply: give it a generator in "+
				"undoGenerators", typ)
		}
	}
}

// TestUndoRefusedOverNewerWork checks that the undo of a batch that the
// batch's answer holds, sent back once another user has changed one of the
// entities that the batch changed, is refused with 409 revision_conflict,
// whose message says that nothing was changed and whose details name the
// entity, with its current revision and state, and changes nothing at all,
// not even the entities that nobody changed since.
//
// Verifies: FR-EDIT-08/AC4
func TestUndoRefusedOverNewerWork(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner, editor := f.as["owner"], f.as["editor"]
	p := f.project(t, "")
	board := createBoard(t, f.s.main, owner, p, "Harbour")
	node := createNode(t, f.s.main, owner, p, board, "element", map[string]any{"title": "Keeper"})
	mine := resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t,
		wireCommand{Type: "node.update", Payload: map[string]any{"id": node, "title": "Mine"}},
		wireCommand{Type: "board.rename", Payload: map[string]any{"id": board, "name": "Mine"}}), owner.header(true)))
	if len(mine.Undo) != 2 || mine.Undo[0].Type != "board.rename" || mine.Undo[1].Type != "node.update" ||
		mine.Undo[1].ExpectedRev[node] != 2 {
		t.Fatalf("the batch's undo is %+v", mine.Undo)
	}
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t,
		wireCommand{Type: "node.update", Payload: map[string]any{"id": node, "title": "Theirs"}}), editor.header(true)))

	res := call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, undoCommands(mine.Undo)...),
		owner.header(true))
	wantCode(t, "the undo", res, http.StatusConflict, api.CodeRevisionConflict)
	var env struct {
		Error api.ErrorBody `json:"error"`
	}
	if json.Unmarshal(res.body, &env) != nil || !strings.Contains(env.Error.Message, "nothing was changed") {
		t.Errorf("the refusal's message: %s", res.body)
	}
	conflicts, _ := res.details(t)["conflicts"].([]any)
	states, _ := res.details(t)["states"].(map[string]any)
	if len(conflicts) != 1 {
		t.Fatalf("the conflicts: %s", res.body)
	}
	c := conflicts[0].(map[string]any)
	state, _ := states[node].(map[string]any)
	if c["id"] != node || c["command"] != float64(1) || c["expected_rev"] != float64(2) ||
		c["current_rev"] != float64(3) || state["title"] != "Theirs" {
		t.Errorf("the conflict: %v, with the states %v", c, states)
	}
	var name, title string
	var seq int64
	if err := f.admin.QueryRow(context.Background(), `SELECT b.name, n.title, p.seq FROM board b, node n, project p
		WHERE b.id = $1 AND n.id = $2 AND p.id = $3`, decode(t, ids.Board, board), decode(t, ids.Node, node),
		decode(t, ids.Project, p)).Scan(&name, &title, &seq); err != nil {
		t.Fatal(err)
	}
	if name != "Mine" || title != "Theirs" || seq != 4 {
		t.Errorf("after the refused undo, the board is %q, the node %q, and the seq %d, want Mine, Theirs, and 4",
			name, title, seq)
	}
}

// TestUndoOfNoChange checks that a batch's undo holds nothing of a command
// that changed nothing, as a board's rename to the name that it has, which
// would expect no revision of the board, so that the undo, sent back once
// another user has renamed the board, applies, and leaves the board as that
// user named it, rather than rename it back over their work.
//
// Verifies: FR-EDIT-08/AC4
func TestUndoOfNoChange(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner, editor := f.as["owner"], f.as["editor"]
	p := f.project(t, "")
	board := createBoard(t, f.s.main, owner, p, "Harbour")
	node := createNode(t, f.s.main, owner, p, board, "element", map[string]any{"title": "Keeper"})
	same := wireCommand{Type: "board.rename", Payload: map[string]any{"id": board, "name": "Harbour"}}
	if alone := resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, same),
		owner.header(true))); alone.Undo == nil || len(alone.Undo) != 0 {
		t.Errorf("the undo of a rename of the board to its name is %+v, want empty", alone.Undo)
	}
	mine := resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, same,
		wireCommand{Type: "node.update", Payload: map[string]any{"id": node, "title": "Mine"}}), owner.header(true)))
	if len(mine.Undo) != 1 || mine.Undo[0].Type != "node.update" {
		t.Fatalf("the batch's undo is %+v, want the node's update alone", mine.Undo)
	}
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t,
		wireCommand{Type: "board.rename", Payload: map[string]any{"id": board, "name": "Port"}}), editor.header(true)))
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, undoCommands(mine.Undo)...),
		owner.header(true)))
	var name, title string
	if err := f.admin.QueryRow(context.Background(), `SELECT b.name, n.title FROM board b, node n
		WHERE b.id = $1 AND n.id = $2`, decode(t, ids.Board, board), decode(t, ids.Node, node)).Scan(&name,
		&title); err != nil {
		t.Fatal(err)
	}
	if name != "Port" || title != "Keeper" {
		t.Errorf("once the batch is undone, the board is %q and the node %q, want Port and Keeper", name, title)
	}
}

// TestUndoOfArchivalAndDeletion checks that the undo of a batch that changes
// the project's content and then archives the project, deletes it, or both,
// which unarchives or restores the project and then sets the content back,
// applies, though the project is archived, or in the trash, when it is sent,
// as a batch that unarchives or restores the project does whatever its other
// commands, and one that does both a project both archived and in the trash,
// so that the undo that the batch's answer held is one that a client can send
// back.
func TestUndoOfArchivalAndDeletion(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner := f.as["owner"]
	p := f.project(t, "")
	board := createBoard(t, f.s.main, owner, p, "Harbour")
	node := createNode(t, f.s.main, owner, p, board, "element", map[string]any{"title": "Keeper"})
	for _, last := range [][]string{{"project.archive"}, {"project.delete"}, {"project.archive", "project.delete"}} {
		batch := []wireCommand{{Type: "node.update", Payload: map[string]any{"id": node, "title": "Changed"}}}
		for _, typ := range last {
			batch = append(batch, wireCommand{Type: typ})
		}
		done := resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, batch...),
			owner.header(true)))
		undo := undoCommands(done.Undo)
		res := call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, undo...), owner.header(true))
		if res.status != http.StatusOK {
			t.Fatalf("the undo of a change and %s, %s: %d %s", last, undoTypes(undo), res.status, res.body)
		}
		var title string
		var archived, deleted bool
		if err := f.admin.QueryRow(context.Background(), `SELECT n.title, p.archived_at IS NOT NULL,
			p.deleted_at IS NOT NULL FROM node n, project p WHERE n.id = $1 AND p.id = $2`,
			decode(t, ids.Node, node), decode(t, ids.Project, p)).Scan(&title, &archived, &deleted); err != nil {
			t.Fatal(err)
		}
		if title != "Keeper" || archived || deleted {
			t.Errorf("once the change and %s are undone, the node is %q, and the project archived %t and deleted %t, "+
				"want Keeper, and neither", last, title, archived, deleted)
		}
	}
}

// TestUndoRoundTrip is the property test of undo (PRD FR-EDIT-08 AC2, NFR-Q-05):
// for each of a few seeds, a project with random content, some of it in its
// trash, takes random batches of commands of every type that has an inverse,
// through the command endpoint, as a client does; then the client undoes them
// all, from the last to the first, each by sending back the undo that the
// batch's answer held, with the revisions that it expects, which it moves as a
// client moves them once its own undo has brought an entity back; and the
// project is then as it was, entity by entity, its revisions, timestamps, and
// authors apart, but for what the commands created, which their undo left in
// the trash (plan note N45). The client then redoes them all, each by sending
// back the undo of its undo, and the project is as the batches left it. At
// each of these points the workspace's usage of projects, boards, and nodes
// is what a recount finds. After every batch, whether it applied or not, the
// index of what the project's content names (package dependency) is what a
// recomputation of it from scratch gives (FR-DEP-01 AC1): the generators'
// texts show variables and attributes and mention components, and their
// conditions and scripts read, assign, and update variables and attributes,
// and read whether nodes were visited.
//
// Verifies: FR-EDIT-08/AC2, NFR-Q-05, FR-DEP-01/AC1
func TestUndoRoundTrip(t *testing.T) {
	// The seeds' subtests need PostgreSQL, and skip without it, so that the
	// check of what they covered below would find nothing covered.
	if testing.Short() {
		t.Skip("skipped with -short: needs PostgreSQL, which the tests run in Docker")
	}
	t.Parallel()
	var mu sync.Mutex
	covered, indexed := map[string]bool{}, map[string]bool{}
	t.Run("seeds", func(t *testing.T) {
		for _, seed := range []uint64{1, 2, 3} {
			t.Run(fmt.Sprint(seed), func(t *testing.T) {
				t.Parallel()
				types, uses := undoRoundTrip(t, seed)
				mu.Lock()
				defer mu.Unlock()
				for _, typ := range types {
					covered[typ] = true
				}
				for _, u := range uses {
					indexed[u] = true
				}
			})
		}
	})
	if t.Failed() {
		return
	}
	for _, typ := range slices.Sorted(maps.Keys(undoGenerators)) {
		if !covered[typ] {
			t.Errorf("no batch of the property test applied a %s, whose generator never made one that applied", typ)
		}
	}
	// The check of the index after every batch checked a row of each kind
	// of use from each kind of site that holds it, at some point of some
	// seed's batches.
	for _, use := range indexUses {
		if !indexed[use] {
			t.Errorf("the index never held a row of %s after a batch, so that no check of it checked one", use)
		}
	}
}

// indexUses are the kinds of row of the index of what a project's content
// names (package dependency), each the kind of its site, its access, and the
// kind of what it names, as indexedUses lists them: a variable's and an
// attribute's reads and writes by expressions, a node's reads by them, and a
// variable's and an attribute's reads and a component's mentions by a node's
// text, a connection's label, and a component's description.
var indexUses = []string{"expression read variable", "expression write variable", "expression read attribute",
	"expression write attribute", "expression read node", "node read variable", "node read attribute",
	"node read component", "connection read variable", "connection read attribute", "connection read component",
	"component read variable", "component read attribute", "component read component"}

// undoRoundTrip runs the property test of undo with the seed, and returns the
// types of command that it applied and undid, and the kinds of row that the
// index of what the project names held after any of its batches
// (indexUses).
func undoRoundTrip(t *testing.T, seed uint64) (applied, uses []string) {
	s, d, owner, ws := inviteServer(t, "open", nil)
	// The test sends many more batches in a minute than a person does.
	policy := ratelimit.DefaultPolicy()
	policy.Rates[ratelimit.Write].Limits[ratelimit.User] = 100_000
	s.limits = ratelimit.New(readyDB{s.ready}, policy)
	p := createProject(t, s.main, owner, ws, "Harbour").ID
	g := &undoGen{r: rand.New(rand.NewPCG(seed, 0x37)), ms: 1_700_000_000_000,
		asset: upload(t, s.main, owner, p, testPNG(t, 8, 8), http.StatusCreated).ID}
	conn := d.Admin(t)
	project := decode(t, ids.Project, p)
	seen := map[string]bool{}
	send := func(cmds []wireCommand) response {
		res := call(s.main, http.MethodPost, commandsPath(p), commandBatch(t, cmds...), owner.header(true))
		if drift := indexDrift(t, conn, p); len(drift) > 0 {
			t.Fatalf("after the batch %s, answered %d, the index of what the project names is not what its "+
				"content names: %q", undoTypes(cmds), res.status, drift)
		}
		for _, u := range indexedUses(t, conn, project) {
			seen[u] = true
		}
		return res
	}

	// The project's content before the batches that the client undoes, made
	// of random batches, most of them creations, which leave entities of each
	// kind, some in the trash.
	creations := []string{"folder.create", "board.create", "node.create", "node.create", "node.create",
		"connection.create", "connection.create", "variable.create", "variable.create", "component_type.create",
		"attribute.create", "attribute.create", "component.create", "component.create", "branch_output.create",
		"branch_output.create"}
	for range 120 {
		typ := creations[g.r.IntN(len(creations))]
		if g.r.IntN(3) == 0 {
			typ = g.anyType()
		}
		g.m = readUndoModel(t, conn, project)
		cmds, ok := g.batch(typ)
		if ok {
			wantApplied(t, send(cmds), false)
		}
	}
	for {
		g.m = readUndoModel(t, conn, project)
		cmds, ok := g.batch("")
		if !ok {
			break
		}
		wantApplied(t, send(cmds), true)
	}

	c := &undoClient{t: t, send: send, store: undoRevisions(t, conn, project), open: map[*undoEntry]bool{}}
	original := readUndoState(t, conn, project)
	workspace := decode(t, ids.Workspace, ws)
	wantUsageCounted(t, conn, workspace, "once the project's content is made")
	wantNamed(t, conn, project, "once the project's content is made")
	var schedule []string
	for range 3 {
		round := slices.Sorted(maps.Keys(undoGenerators))
		g.r.Shuffle(len(round), func(i, j int) { round[i], round[j] = round[j], round[i] })
		schedule = append(schedule, round...)
	}
	var stack []*undoEntry
	for _, typ := range schedule {
		g.m = readUndoModel(t, conn, project)
		cmds, ok := g.batch(typ)
		if !ok {
			continue
		}
		res, e := c.apply(cmds)
		if !wantApplied(t, res, false) {
			continue
		}
		if e == nil {
			t.Fatalf("the batch %s, of commands that have inverses, has no undo: %s", undoTypes(cmds), res.body)
		}
		stack = append(stack, e)
		for _, cmd := range cmds {
			applied = append(applied, cmd.Type)
		}
	}
	if len(stack) < len(undoGenerators) {
		t.Errorf("only %d batches applied", len(stack))
	}
	forward := readUndoState(t, conn, project)
	wantUsageCounted(t, conn, workspace, "once the batches have applied")
	wantNamed(t, conn, project, "once the batches have applied")

	var redo []*undoEntry
	for i := len(stack) - 1; i >= 0; i-- {
		redo = append(redo, c.revert(stack[i]))
	}
	undone := readUndoState(t, conn, project)
	wantUsageCounted(t, conn, workspace, "once every batch is undone")
	wantNamed(t, conn, project, "once every batch is undone")
	for _, key := range slices.Sorted(maps.Keys(original)) {
		if row, ok := undone[key]; !ok || !reflect.DeepEqual(row.content, original[key].content) {
			t.Errorf("once every batch is undone, %s is %v, want %v", key, row.content, original[key].content)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(undone)) {
		if _, ok := original[key]; !ok && undone[key].live {
			t.Errorf("once every batch is undone, %s, which a batch created, is outside the trash: %v", key,
				undone[key].content)
		}
	}

	for i := len(redo) - 1; i >= 0; i-- {
		c.revert(redo[i])
	}
	redone := readUndoState(t, conn, project)
	wantUsageCounted(t, conn, workspace, "once every batch is redone")
	wantNamed(t, conn, project, "once every batch is redone")
	for _, key := range slices.Sorted(maps.Keys(forward)) {
		if row, ok := redone[key]; !ok || !reflect.DeepEqual(row.content, forward[key].content) {
			t.Errorf("once every batch is redone, %s is %v, want %v", key, row.content, forward[key].content)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(redone)) {
		if _, ok := forward[key]; !ok {
			t.Errorf("once every batch is redone, the project has %s, which it did not have", key)
		}
	}
	return applied, slices.Sorted(maps.Keys(seen))
}

// wantUsageCounted fails the test unless the workspace ws's usage of
// projects, boards, and nodes is what a recount of them finds, as every
// batch, its undo, and its redo are to leave it (PRD FR-ENT-02), whatever
// their commands, as one that changes the content and restores the project
// in the trash; when says at what point of the test.
func wantUsageCounted(t *testing.T, conn *pgx.Conn, ws ids.UUID, when string) {
	t.Helper()
	q := queries.New(conn)
	for _, key := range []struct {
		name  string
		drift func(*queries.Queries, context.Context, pgtype.UUID) (int64, error)
	}{{"projects", (*queries.Queries).ProjectDrift}, {"boards", (*queries.Queries).BoardDrift},
		{"nodes", (*queries.Queries).NodeDrift}} {
		drift, err := key.drift(q, context.Background(), pgtype.UUID{Bytes: ws, Valid: true})
		if err != nil {
			t.Fatal(err)
		}
		if drift != 0 {
			t.Errorf("%s, a recount finds %d %s more than the workspace's usage", when, drift, key.name)
		}
	}
}

// wantNamed fails the test unless the names of the project's expressions
// (expression_name) are their refs and their keys, each with its
// expression's board, as every batch, its undo, and its redo are to leave
// them, by which a later batch finds the expressions that name what it
// changes; when says at what point of the test.
func wantNamed(t *testing.T, conn *pgx.Conn, project ids.UUID, when string) {
	t.Helper()
	rows, err := conn.Query(context.Background(), `WITH want AS (
			SELECT e.id AS expression_id, e.board_id, r.ref, NULL::text AS key
			FROM expression e, unnest(e.refs) AS r (ref) WHERE e.project_id = $1
			UNION ALL
			SELECT e.id, e.board_id, NULL, k.key FROM expression e, unnest(e.keys) AS k (key) WHERE e.project_id = $1
		), have AS (
			SELECT expression_id, board_id, ref, key::text FROM expression_name WHERE project_id = $1
		)
		SELECT 'lacks ' || w::text FROM (SELECT * FROM want EXCEPT ALL SELECT * FROM have) w
		UNION ALL
		SELECT 'has ' || h::text FROM (SELECT * FROM have EXCEPT ALL SELECT * FROM want) h`, project)
	if err != nil {
		t.Fatal(err)
	}
	drift, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) > 0 {
		t.Errorf("%s, the names of the project's expressions are not their refs and keys: %v", when, drift)
	}
}

// indexedUses returns the kinds of row that the index of what the project's
// content names holds (indexUses).
func indexedUses(t *testing.T, conn *pgx.Conn, project ids.UUID) []string {
	t.Helper()
	rows, err := conn.Query(context.Background(), `SELECT DISTINCT CASE WHEN expression_id IS NOT NULL
			THEN 'expression' WHEN node_id IS NOT NULL THEN 'node' WHEN connection_id IS NOT NULL THEN 'connection'
			ELSE 'component' END || ' ' || access || ' ' || target_kind
		FROM expression_ref WHERE project_id = $1`, project)
	if err != nil {
		t.Fatal(err)
	}
	uses, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return uses
}

// wantApplied reports whether the batch that res answers applied, and fails
// the test if it was refused for anything but what the project's state makes
// of a generator's random payload, as a key that another variable has, or if
// it was refused at all and must applies is set.
func wantApplied(t *testing.T, res response, must bool) bool {
	t.Helper()
	switch {
	case res.status == http.StatusOK:
		return true
	case must:
		t.Fatalf("a batch that must apply: %d %s", res.status, res.body)
	case res.status >= 500, res.status == http.StatusNotFound, res.status == http.StatusForbidden,
		res.code() == api.CodeRevisionConflict, res.code() == api.CodeProjectArchived:
		t.Fatalf("a generated batch: %d %s", res.status, res.body)
	}
	return false
}

// undoCommands returns the commands of the undo that a batch's answer held,
// as a client sends them back, each of which commandBatch gives a command_id
// of its own.
func undoCommands(undo []undoCommandBody) []wireCommand {
	cmds := make([]wireCommand, len(undo))
	for i, u := range undo {
		cmds[i] = wireCommand{Type: u.Type, Payload: json.RawMessage(u.Payload), ExpectedRev: u.ExpectedRev}
	}
	return cmds
}

// undoTypes returns the types of the commands cmds, joined.
func undoTypes(cmds []wireCommand) string {
	types := make([]string, len(cmds))
	for i, c := range cmds {
		types[i] = c.Type
	}
	return strings.Join(types, ", ")
}

// undoEntry is an entry of a client's undo or redo stack: the undo that a
// batch's answer held, and the revisions that the entities that the batch
// changed had before it, as the client's store held them once it had every
// batch before the batch's seq, but for those that it created.
type undoEntry struct {
	undo   []undoCommandBody
	before map[string]int64
}

// undoClient is a client of a project's command endpoint with undo and redo
// stacks, as the web app keeps them (plan item 56): it knows the revision of
// each of the project's entities, from the answers to its batches, and once it
// has undone a batch, which brings back each entity that the batch changed as
// it was before the batch, at a new revision, it moves what the other entries
// of its stacks expect of the entity, from the revision that it had before
// the batch to the new one (plan note N45). It is the project's only writer,
// so what it knows when a batch's answer comes is the project as every batch
// before that one's seq left it; a client among others knows that only once
// its store has caught up with the operations before that seq.
type undoClient struct {
	t     *testing.T
	send  func(cmds []wireCommand) response
	store map[string]int64
	// open are the entries of the client's stacks.
	open map[*undoEntry]bool
}

// apply sends the batch of cmds, and returns its answer, and the entry of its
// undo if it applied and can be undone.
func (c *undoClient) apply(cmds []wireCommand) (response, *undoEntry) {
	c.t.Helper()
	res := c.send(cmds)
	if res.status != http.StatusOK {
		return res, nil
	}
	body := resultFrom(c.t, res)
	e := &undoEntry{undo: body.Undo, before: map[string]int64{}}
	for _, r := range body.Revisions {
		if rev, ok := c.store[r.ID]; ok {
			e.before[r.ID] = rev
		}
		c.store[r.ID] = r.Rev
	}
	if body.Undo == nil {
		return res, nil
	}
	c.open[e] = true
	return res, e
}

// revert sends the undo of the entry e, which must apply, moves what the
// other entries expect of each entity that it brought back, and returns the
// entry of the undo's own undo.
func (c *undoClient) revert(e *undoEntry) *undoEntry {
	c.t.Helper()
	delete(c.open, e)
	if len(e.undo) == 0 {
		return &undoEntry{undo: []undoCommandBody{}, before: map[string]int64{}}
	}
	cmds := undoCommands(e.undo)
	res, next := c.apply(cmds)
	if res.status != http.StatusOK || next == nil {
		c.t.Fatalf("the undo %s: %d %s", undoTypes(cmds), res.status, res.body)
	}
	for _, r := range resultFrom(c.t, res).Revisions {
		was, ok := e.before[r.ID]
		if !ok {
			continue
		}
		for o := range c.open {
			for _, u := range o.undo {
				if rev, ok := u.ExpectedRev[r.ID]; ok && rev == was {
					u.ExpectedRev[r.ID] = r.Rev
				}
			}
		}
	}
	return next
}

// undoTables are the tables of a project's content, each with the view of
// its rows that are not in the trash.
var undoTables = []struct{ table, live string }{
	{"folder", "live_folder"}, {"board", "live_board"}, {"variable", "live_variable"},
	{"component_type", "live_component_type"}, {"attribute_def", "live_attribute_def"},
	{"component", "live_component"}, {"node", "live_node"}, {"branch_output", "live_branch_output"},
	{"connection", "live_connection"},
}

// undoRevisions returns the revision of every entity of the project, by its
// ID, as a client that read the project knows them.
func undoRevisions(t *testing.T, conn *pgx.Conn, project ids.UUID) map[string]int64 {
	t.Helper()
	kinds := map[string]ids.Kind{"folder": ids.Folder, "board": ids.Board, "variable": ids.Variable,
		"component_type": ids.ComponentType, "attribute_def": ids.AttributeDef, "component": ids.Component,
		"node": ids.Node, "branch_output": ids.BranchOutput, "connection": ids.Connection}
	revs := map[string]int64{}
	var rev int64
	if err := conn.QueryRow(context.Background(), "SELECT rev FROM project WHERE id = $1", project).
		Scan(&rev); err != nil {
		t.Fatal(err)
	}
	revs[ids.Encode(ids.Project, project)] = rev
	for _, tb := range undoTables {
		rows, err := conn.Query(context.Background(), "SELECT id, rev FROM "+tb.table+" WHERE project_id = $1",
			project)
		if err != nil {
			t.Fatal(err)
		}
		var id ids.UUID
		if _, err := pgx.ForEachRow(rows, []any{&id, &rev}, func() error {
			revs[ids.Encode(kinds[tb.table], id)] = rev
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return revs
}

// undoRow is a row of a project's content without its revision, timestamps,
// and authors, with whether it is in the trash as deleted, and whether it is
// outside the trash, as everything that reads the content sees it.
type undoRow struct {
	content map[string]any
	live    bool
}

// readUndoState returns the project's content, every row of it, by its table
// and ID, the trash's included, and an attribute's value by its component's
// and its definition's.
func readUndoState(t *testing.T, conn *pgx.Conn, project ids.UUID) map[string]undoRow {
	t.Helper()
	state := map[string]undoRow{}
	read := func(table, sql string) {
		rows, err := conn.Query(context.Background(), sql, project)
		if err != nil {
			t.Fatal(err)
		}
		var key string
		var doc []byte
		var live bool
		if _, err := pgx.ForEachRow(rows, []any{&key, &doc, &live}, func() error {
			var content map[string]any
			if err := json.Unmarshal(doc, &content); err != nil {
				return err
			}
			for k, v := range content {
				switch {
				case k == "deleted_at":
					content["deleted"] = v != nil
				case k == "archived_at":
					content["archived"] = v != nil
				}
				if k == "rev" || k == "seq" || k == "hard_delete_after" || strings.HasSuffix(k, "_at") ||
					strings.HasSuffix(k, "_by") {
					delete(content, k)
				}
			}
			state[table+"/"+key] = undoRow{content: content, live: live}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	read("project", "SELECT id::text, to_jsonb(t), deleted_at IS NULL FROM project t WHERE id = $1")
	for _, tb := range undoTables {
		read(tb.table, "SELECT t.id::text, to_jsonb(t), EXISTS (SELECT 1 FROM "+tb.live+" l WHERE l.id = t.id) "+
			"FROM "+tb.table+" t WHERE t.project_id = $1")
	}
	// An expression's own ID is made anew when it is added again, so it is
	// read by its owner and its field, with what compiling its source gives,
	// which compiling it against the project as the undo leaves it gives
	// again. It is in the trash with its owner.
	read("expression", `SELECT coalesce(e.node_id, e.connection_id, e.branch_output_id)::text || '/' || e.field,
		to_jsonb(e) - 'id', EXISTS (SELECT 1 FROM live_node l WHERE l.id = e.node_id)
			OR EXISTS (SELECT 1 FROM live_connection l WHERE l.id = e.connection_id)
			OR EXISTS (SELECT 1 FROM live_branch_output l WHERE l.id = e.branch_output_id)
		FROM expression e WHERE e.project_id = $1`)
	// A value's own ID is made anew when it is added again.
	read("attribute_value", `SELECT v.component_id::text || '/' || v.attribute_def_id::text, to_jsonb(v) - 'id',
		EXISTS (SELECT 1 FROM live_component c WHERE c.id = v.component_id)
			AND EXISTS (SELECT 1 FROM live_attribute_def a WHERE a.id = v.attribute_def_id)
		FROM attribute_value v WHERE v.project_id = $1`)
	return state
}

// undoModel is what the generators of commands know of a project's content:
// each entity's ID, whether it is outside the trash (live), and whether it is
// deleted itself, which its restoration undoes.
type undoModel struct {
	archived, deleted bool
	folders           []undoEntity
	boards            []undoEntity
	nodes             []undoEntity
	outputs           []undoEntity
	connections       []undoEntity
	variables         []undoEntity
	types             []undoEntity
	attributes        []undoEntity
	components        []undoEntity
}

// undoEntity is an entity of an undoModel, with the fields that its kind's
// generators read.
type undoEntity struct {
	id            string
	live, deleted bool
	// kind is a folder's or a branch output's, typ a node's, a variable's,
	// or an attribute definition's, scope a variable's, board a node's, an
	// output's, or a connection's, node an output's, key a variable's, an
	// attribute definition's, or a component's, componentType an attribute
	// definition's or a component's, options an enum's, and runtime an
	// attribute definition's.
	kind, typ, scope, board, node, key, componentType string
	options                                           []string
	runtime                                           bool
}

// readUndoModel reads the project's content for the generators.
func readUndoModel(t *testing.T, conn *pgx.Conn, project ids.UUID) *undoModel {
	t.Helper()
	ctx := context.Background()
	m := &undoModel{}
	if err := conn.QueryRow(ctx, "SELECT archived_at IS NOT NULL, deleted_at IS NOT NULL FROM project WHERE id = $1",
		project).Scan(&m.archived, &m.deleted); err != nil {
		t.Fatal(err)
	}
	read := func(kind ids.Kind, sql string, into *[]undoEntity, scan func(e *undoEntity) []any) {
		rows, err := conn.Query(ctx, sql, project)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id ids.UUID
			e := undoEntity{}
			if err := rows.Scan(append([]any{&id, &e.deleted, &e.live}, scan(&e)...)...); err != nil {
				t.Fatal(err)
			}
			e.id = ids.Encode(kind, id)
			*into = append(*into, e)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	common := func(table string) string {
		return "SELECT t.id, t.deleted_at IS NOT NULL, EXISTS (SELECT 1 FROM live_" + table + " l WHERE l.id = t.id)"
	}
	none := func(*undoEntity) []any { return nil }
	encoded := func(kind ids.Kind, to *string) any { return &encodedID{kind: kind, to: to} }
	read(ids.Folder, common("folder")+", t.kind FROM folder t WHERE t.project_id = $1 ORDER BY t.id", &m.folders,
		func(e *undoEntity) []any { return []any{&e.kind} })
	read(ids.Board, common("board")+" FROM board t WHERE t.project_id = $1 ORDER BY t.id", &m.boards, none)
	read(ids.Node, common("node")+", t.type, t.board_id FROM node t WHERE t.project_id = $1 ORDER BY t.id",
		&m.nodes, func(e *undoEntity) []any { return []any{&e.typ, encoded(ids.Board, &e.board)} })
	read(ids.BranchOutput, common("branch_output")+", t.kind, t.node_id, t.board_id FROM branch_output t "+
		"WHERE t.project_id = $1 ORDER BY t.id", &m.outputs, func(e *undoEntity) []any {
		return []any{&e.kind, encoded(ids.Node, &e.node), encoded(ids.Board, &e.board)}
	})
	read(ids.Connection, common("connection")+", t.board_id FROM connection t WHERE t.project_id = $1 ORDER BY t.id",
		&m.connections, func(e *undoEntity) []any { return []any{encoded(ids.Board, &e.board)} })
	read(ids.Variable, common("variable")+", t.type, t.scope, t.key FROM variable t WHERE t.project_id = $1 "+
		"ORDER BY t.id", &m.variables, func(e *undoEntity) []any { return []any{&e.typ, &e.scope, &e.key} })
	read(ids.ComponentType, common("component_type")+" FROM component_type t WHERE t.project_id = $1 ORDER BY t.id",
		&m.types, none)
	read(ids.AttributeDef, common("attribute_def")+", t.type, t.component_type_id, t.enum_options, t.is_runtime, "+
		"t.key FROM attribute_def t WHERE t.project_id = $1 ORDER BY t.id", &m.attributes, func(e *undoEntity) []any {
		return []any{&e.typ, encoded(ids.ComponentType, &e.componentType), &e.options, &e.runtime, &e.key}
	})
	read(ids.Component, common("component")+", t.component_type_id, t.key FROM component t WHERE t.project_id = $1 "+
		"ORDER BY t.id", &m.components, func(e *undoEntity) []any {
		return []any{encoded(ids.ComponentType, &e.componentType), &e.key}
	})
	return m
}

// encodedID scans a UUID into the public ID of an entity of its kind.
type encodedID struct {
	kind ids.Kind
	to   *string
}

func (e *encodedID) Scan(src any) error {
	s, ok := src.(string)
	if !ok {
		return fmt.Errorf("an ID of %T", src)
	}
	id, err := ids.ParseUUID(s)
	if err != nil {
		return err
	}
	*e.to = ids.Encode(e.kind, id)
	return nil
}

// undoGen makes random commands for a project's content as the model says it
// is, with r, and the IDs of the entities that they create with r too
// (undoGen.id), so that a seed makes the same batches whenever it runs, and a
// seed that fails the test fails it again.
type undoGen struct {
	r *rand.Rand
	// ms is the time, in milliseconds since 1970, of the last ID that id
	// made.
	ms uint64
	m  *undoModel
	// asset is an asset of the project, which a node's cover and a
	// component's image name.
	asset string
}

// anyType returns a random type of command that has an inverse.
func (g *undoGen) anyType() string {
	types := slices.Sorted(maps.Keys(undoGenerators))
	return types[g.r.IntN(len(types))]
}

// batch returns a batch of a command of the type typ, and in some batches one
// or two more of random types, the project's own among them, or false if the
// model has nothing that a command of the type could change. A project in the
// trash takes a batch that restores it, and an archived one a batch that
// unarchives it, whatever typ is, which applies whatever its other commands,
// but for one that is both, whose restoration has no other command, which
// it would refuse as one of an archived project; and so does typ "", which
// takes no other batch, and no other command, so that it must apply.
func (g *undoGen) batch(typ string) ([]wireCommand, bool) {
	must := typ == ""
	switch {
	case g.m.deleted:
		typ = "project.restore"
	case g.m.archived:
		typ = "project.unarchive"
	case must:
		return nil, false
	}
	cmd, ok := g.command(typ)
	if !ok {
		return nil, false
	}
	cmds := []wireCommand{cmd}
	if must || g.m.deleted && g.m.archived || g.r.IntN(4) != 0 {
		return cmds, true
	}
	for range 1 + g.r.IntN(2) {
		if c, ok := g.command(g.anyType()); ok {
			cmds = append(cmds, c)
		}
	}
	return cmds, true
}

// command returns a command of the type typ, or false if the model has
// nothing that one could change.
func (g *undoGen) command(typ string) (wireCommand, bool) {
	payload, ok := undoGenerators[typ](g)
	return wireCommand{Type: typ, Payload: payload}, ok
}

// undoGenerators make, for each type of command that has an inverse, a random
// payload for the model's project, or false if the model has nothing that the
// command could change. A payload may still be refused for what the project's
// state makes of it, as a folder to delete that holds something.
var undoGenerators = map[string]func(g *undoGen) (map[string]any, bool){
	"project.rename":   func(g *undoGen) (map[string]any, bool) { return obj("name", g.name()), true },
	"project.set_slug": func(g *undoGen) (map[string]any, bool) { return obj("slug", g.slug()), true },
	"project.describe": func(g *undoGen) (map[string]any, bool) { return obj("description", g.text()), true },
	"project.update_settings": func(g *undoGen) (map[string]any, bool) {
		p := map[string]any{}
		if g.coin() {
			p["default_locale"] = []string{"en", "cs-CZ", "de", "fr"}[g.r.IntN(4)]
		}
		if g.coin() {
			p["publish_min_role"] = []string{"editor", "admin"}[g.r.IntN(2)]
		}
		if g.coin() || len(p) == 0 {
			entry := map[string]any{}
			if b, ok := g.live(g.m.boards); ok {
				entry["board_id"] = b.id
				if n, ok := g.pick(g.m.nodes, func(n undoEntity) bool { return n.board == b.id }); ok && g.coin() {
					entry["node_id"] = n.id
				}
			}
			p["entry"] = entry
		}
		return p, true
	},
	"project.archive":   func(*undoGen) (map[string]any, bool) { return map[string]any{}, true },
	"project.unarchive": func(g *undoGen) (map[string]any, bool) { return map[string]any{}, g.m.archived },
	"project.delete":    func(*undoGen) (map[string]any, bool) { return map[string]any{}, true },
	"project.restore":   func(g *undoGen) (map[string]any, bool) { return map[string]any{}, g.m.deleted },

	"folder.create": func(g *undoGen) (map[string]any, bool) {
		kind := []string{"board", "variable", "component"}[g.r.IntN(3)]
		p := obj("id", g.id(ids.Folder), "kind", kind, "name", g.name())
		g.maybeFolder(p, "parent_id", kind)
		g.maybeOrder(p)
		return p, true
	},
	"folder.rename": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.folders, func(f undoEntity) map[string]any { return obj("id", f.id, "name", g.name()) })
	},
	"folder.move": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.folders, func(f undoEntity) map[string]any {
			p := obj("id", f.id)
			if parent, ok := g.pick(g.m.folders, func(o undoEntity) bool {
				return o.live && o.kind == f.kind && o.id != f.id
			}); ok && g.coin() {
				p["parent_id"] = parent.id
			}
			g.maybeOrder(p)
			return p
		})
	},
	"folder.delete":  func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.folders, true) },
	"folder.restore": func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.folders, false) },

	"board.create": func(g *undoGen) (map[string]any, bool) {
		p := obj("id", g.id(ids.Board), "name", g.name())
		if g.coin() {
			p["slug"] = g.slug()
		}
		if g.coin() {
			p["description"] = g.text()
		}
		if g.coin() {
			p["color"] = g.color()
		}
		g.maybeFolder(p, "folder_id", "board")
		g.maybeOrder(p)
		return p, true
	},
	"board.rename": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.boards, func(b undoEntity) map[string]any { return obj("id", b.id, "name", g.name()) })
	},
	"board.describe": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.boards, func(b undoEntity) map[string]any {
			return obj("id", b.id, "description", g.text())
		})
	},
	"board.set_color": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.boards, func(b undoEntity) map[string]any { return obj("id", b.id, "color", g.color()) })
	},
	"board.set_slug": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.boards, func(b undoEntity) map[string]any { return obj("id", b.id, "slug", g.slug()) })
	},
	"board.set_entry_node": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.boards, func(b undoEntity) map[string]any {
			p := obj("id", b.id)
			if n, ok := g.pick(g.m.nodes, func(n undoEntity) bool { return n.board == b.id }); ok && g.r.IntN(4) != 0 {
				p["node_id"] = n.id
			}
			return p
		})
	},
	"board.move": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.boards, func(b undoEntity) map[string]any {
			p := obj("id", b.id)
			g.maybeFolder(p, "folder_id", "board")
			g.maybeOrder(p)
			return p
		})
	},
	"board.delete": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.boards, func(b undoEntity) map[string]any { return obj("id", b.id, "confirm", true) })
	},
	"board.restore": func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.boards, false) },

	"variable.create": func(g *undoGen) (map[string]any, bool) {
		typ := g.variableType()
		p := obj("id", g.id(ids.Variable), "key", g.key(), "type", typ, "scope", "global")
		if b, ok := g.live(g.m.boards); ok && g.coin() {
			p["scope"], p["board_id"] = "board", b.id
			if g.coin() {
				p["reset_on_enter"] = true
			}
		}
		if g.coin() {
			p["default"] = g.value(typ, nil)
		}
		if g.coin() {
			p["description"] = g.text()
		}
		g.maybeFolder(p, "folder_id", "variable")
		g.maybeOrder(p)
		return p, true
	},
	"variable.rename": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.variables, func(v undoEntity) map[string]any { return obj("id", v.id, "key", g.key()) })
	},
	"variable.describe": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.variables, func(v undoEntity) map[string]any {
			return obj("id", v.id, "description", g.text())
		})
	},
	"variable.set_default": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.variables, func(v undoEntity) map[string]any {
			return obj("id", v.id, "default", g.value(v.typ, nil))
		})
	},
	"variable.retype": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.variables, func(v undoEntity) map[string]any {
			typ := g.variableType()
			for typ == v.typ {
				typ = g.variableType()
			}
			p := obj("id", v.id, "type", typ)
			if g.coin() {
				p["default"] = g.value(typ, nil)
			}
			return p
		})
	},
	"variable.set_scope": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.variables, func(v undoEntity) map[string]any {
			if b, ok := g.live(g.m.boards); ok && (v.scope == "global" || g.coin()) {
				return obj("id", v.id, "scope", "board", "board_id", b.id)
			}
			return obj("id", v.id, "scope", "global")
		})
	},
	"variable.set_reset_on_enter": func(g *undoGen) (map[string]any, bool) {
		v, ok := g.pick(g.m.variables, func(v undoEntity) bool { return v.live && v.scope == "board" })
		return obj("id", v.id, "reset_on_enter", g.coin()), ok
	},
	"variable.move": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.variables, func(v undoEntity) map[string]any {
			p := obj("id", v.id)
			g.maybeFolder(p, "folder_id", "variable")
			g.maybeOrder(p)
			return p
		})
	},
	"variable.delete":  func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.variables, true) },
	"variable.restore": func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.variables, false) },

	"component_type.create": func(g *undoGen) (map[string]any, bool) {
		p := obj("id", g.id(ids.ComponentType), "name", g.name())
		if g.coin() {
			p["key"] = g.key()
		}
		if g.coin() {
			p["icon"] = g.icon()
		}
		return p, true
	},
	"component_type.rename": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.types, func(c undoEntity) map[string]any { return obj("id", c.id, "name", g.name()) })
	},
	"component_type.set_key": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.types, func(c undoEntity) map[string]any { return obj("id", c.id, "key", g.key()) })
	},
	"component_type.set_icon": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.types, func(c undoEntity) map[string]any { return obj("id", c.id, "icon", g.icon()) })
	},
	"component_type.delete": func(g *undoGen) (map[string]any, bool) {
		// A type of which a component is outside the trash is in use.
		c, ok := g.pick(g.m.types, func(c undoEntity) bool {
			return c.live && !slices.ContainsFunc(g.m.components, func(o undoEntity) bool {
				return o.live && o.componentType == c.id
			})
		})
		return obj("id", c.id), ok
	},
	"component_type.restore": func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.types, false) },

	"attribute.create": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.types, func(c undoEntity) map[string]any {
			typ := []string{"bool", "int", "float", "string", "text", "enum"}[g.r.IntN(6)]
			p := obj("id", g.id(ids.AttributeDef), "component_type_id", c.id, "name", g.name(), "type", typ)
			var options []string
			if typ == "enum" {
				options = g.options()
				p["options"] = options
			}
			if g.coin() || typ == "enum" {
				p["default"] = g.value(typ, options)
			}
			if g.coin() {
				p["key"] = g.key()
			}
			if typ != "text" && g.coin() {
				p["runtime"] = true
			}
			g.maybeOrder(p)
			return p
		})
	},
	"attribute.rename": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.attributes, func(a undoEntity) map[string]any { return obj("id", a.id, "name", g.name()) })
	},
	"attribute.set_key": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.attributes, func(a undoEntity) map[string]any { return obj("id", a.id, "key", g.key()) })
	},
	"attribute.set_default": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.attributes, func(a undoEntity) map[string]any {
			return obj("id", a.id, "default", g.value(a.typ, a.options))
		})
	},
	"attribute.set_runtime": func(g *undoGen) (map[string]any, bool) {
		a, ok := g.pick(g.m.attributes, func(a undoEntity) bool { return a.live && a.typ != "text" })
		return obj("id", a.id, "runtime", !a.runtime), ok
	},
	"attribute.set_options": func(g *undoGen) (map[string]any, bool) {
		a, ok := g.pick(g.m.attributes, func(a undoEntity) bool { return a.live && a.typ == "enum" })
		options := g.options()
		return obj("id", a.id, "options", options, "default", options[g.r.IntN(len(options))]), ok
	},
	"attribute.retype": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.attributes, func(a undoEntity) map[string]any {
			types := []string{"bool", "int", "float", "string", "enum"}
			if !a.runtime {
				types = append(types, "text")
			}
			typ := types[g.r.IntN(len(types))]
			for typ == a.typ {
				typ = types[g.r.IntN(len(types))]
			}
			p := obj("id", a.id, "type", typ, "conversion", []string{"convert", "reset"}[g.r.IntN(2)])
			var options []string
			if typ == "enum" {
				options = g.options()
				p["options"] = options
			}
			if g.coin() || typ == "enum" {
				p["default"] = g.value(typ, options)
			}
			return p
		})
	},
	"attribute.move": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.attributes, func(a undoEntity) map[string]any {
			p := obj("id", a.id)
			g.maybeOrder(p)
			return p
		})
	},
	"attribute.delete":  func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.attributes, true) },
	"attribute.restore": func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.attributes, false) },

	"component.create": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.types, func(c undoEntity) map[string]any {
			p := obj("id", g.id(ids.Component), "component_type_id", c.id, "name", g.name())
			if g.coin() {
				p["key"] = g.key()
			}
			if g.coin() {
				p["description"] = g.richText()
			}
			if g.coin() {
				p["image_asset_id"] = g.asset
			}
			g.maybeFolder(p, "folder_id", "component")
			g.maybeOrder(p)
			if a, ok := g.pick(g.m.attributes, func(a undoEntity) bool {
				return a.live && a.componentType == c.id
			}); ok && g.coin() {
				p["attributes"] = map[string]any{a.id: g.value(a.typ, a.options)}
			}
			return p
		})
	},
	"component.rename": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.components, func(c undoEntity) map[string]any { return obj("id", c.id, "name", g.name()) })
	},
	"component.set_key": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.components, func(c undoEntity) map[string]any { return obj("id", c.id, "key", g.key()) })
	},
	"component.describe": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.components, func(c undoEntity) map[string]any {
			return obj("id", c.id, "description", g.richText())
		})
	},
	"component.set_image": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.components, func(c undoEntity) map[string]any {
			p := obj("id", c.id)
			if g.coin() {
				p["image_asset_id"] = g.asset
			}
			return p
		})
	},
	"component.set_attribute": func(g *undoGen) (map[string]any, bool) {
		c, ok := g.pick(g.m.components, func(c undoEntity) bool {
			return c.live && slices.ContainsFunc(g.m.attributes, func(a undoEntity) bool {
				return a.live && a.componentType == c.componentType
			})
		})
		if !ok {
			return nil, false
		}
		a, _ := g.pick(g.m.attributes, func(a undoEntity) bool { return a.live && a.componentType == c.componentType })
		return obj("id", c.id, "attribute_id", a.id, "value", g.value(a.typ, a.options)), true
	},
	"component.move": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.components, func(c undoEntity) map[string]any {
			p := obj("id", c.id)
			g.maybeFolder(p, "folder_id", "component")
			g.maybeOrder(p)
			return p
		})
	},
	"component.delete":  func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.components, true) },
	"component.restore": func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.components, false) },

	"node.create": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.boards, func(b undoEntity) map[string]any {
			typ := []string{"element", "element", "branch", "jumper"}[g.r.IntN(4)]
			p := obj("id", g.id(ids.Node), "board_id", b.id, "type", typ, "position", g.position())
			g.nodeFields(p, typ)
			return p
		})
	},
	"node.update": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.nodes, func(n undoEntity) map[string]any {
			p := obj("id", n.id)
			for len(p) == 1 {
				g.nodeFields(p, n.typ)
				delete(p, "position")
			}
			return p
		})
	},
	"node.duplicate": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.nodes, func(n undoEntity) map[string]any {
			return obj("id", g.id(ids.Node), "source_id", n.id, "position", g.position())
		})
	},
	"node.move": func(g *undoGen) (map[string]any, bool) {
		var places []any
		for _, n := range g.m.nodes {
			if n.live && (len(places) == 0 || g.r.IntN(4) == 0) && len(places) < 3 {
				places = append(places, obj("id", n.id, "position", g.position()))
			}
		}
		return obj("nodes", places), len(places) > 0
	},
	"node.delete":  func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.nodes, true) },
	"node.restore": func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.nodes, false) },

	"connection.create": func(g *undoGen) (map[string]any, bool) {
		from, port, ok := g.port(func(n undoEntity) bool { return true })
		if !ok {
			return nil, false
		}
		to, _ := g.pick(g.m.nodes, func(n undoEntity) bool { return n.live && n.board == from.board })
		p := obj("id", g.id(ids.Connection), "from_node_id", from.id, "to_node_id", to.id)
		if port != "" {
			p["from_port"] = port
		}
		if g.coin() {
			p["label"] = g.richText()
		}
		if g.coin() {
			p["condition"] = g.condition()
		}
		if g.coin() {
			p["on_traverse"] = g.script()
		}
		g.maybeOrder(p)
		return p, true
	},
	"connection.update": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.connections, func(c undoEntity) map[string]any {
			p := obj("id", c.id)
			for len(p) == 1 {
				if g.coin() {
					p["label"] = g.richText()
				}
				if g.coin() {
					p["condition"] = []string{"", g.condition()}[g.r.IntN(2)]
				}
				if g.coin() {
					p["on_traverse"] = []string{"", g.script()}[g.r.IntN(2)]
				}
			}
			return p
		})
	},
	"connection.reconnect": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.connections, func(c undoEntity) map[string]any {
			p := obj("id", c.id)
			for len(p) == 1 {
				if n, port, ok := g.port(func(n undoEntity) bool { return n.board == c.board }); ok && g.coin() {
					p["from_node_id"] = n.id
					if port != "" {
						p["from_port"] = port
					}
				}
				if n, ok := g.pick(g.m.nodes, func(n undoEntity) bool { return n.live && n.board == c.board }); ok &&
					g.coin() {
					p["to_node_id"] = n.id
				}
			}
			g.maybeOrder(p)
			return p
		})
	},
	"connection.reorder": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.m.connections, func(c undoEntity) map[string]any {
			p := obj("id", c.id)
			g.maybeOrder(p)
			return p
		})
	},
	"connection.delete":  func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.connections, true) },
	"connection.restore": func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.connections, false) },

	"branch_output.create": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.nodesOf("branch"), func(n undoEntity) map[string]any {
			p := obj("id", g.id(ids.BranchOutput), "node_id", n.id)
			if g.r.IntN(4) == 0 {
				p["else"] = true
				return p
			}
			if g.coin() {
				p["condition"] = g.condition()
			}
			g.maybeOrder(p)
			return p
		})
	},
	"branch_output.update": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.conditional(), func(o undoEntity) map[string]any {
			return obj("id", o.id, "condition", []string{"", g.condition()}[g.r.IntN(2)])
		})
	},
	"branch_output.reorder": func(g *undoGen) (map[string]any, bool) {
		return g.onLive(g.conditional(), func(o undoEntity) map[string]any {
			p := obj("id", o.id)
			g.maybeOrder(p)
			return p
		})
	},
	"branch_output.delete": func(g *undoGen) (map[string]any, bool) {
		p, ok := g.idOf(g.m.outputs, true)
		p["confirm"] = g.r.IntN(4) != 0
		return p, ok
	},
	"branch_output.restore": func(g *undoGen) (map[string]any, bool) { return g.idOf(g.m.outputs, false) },
}

// nodesOf returns the model's nodes of the type typ.
func (g *undoGen) nodesOf(typ string) []undoEntity {
	var out []undoEntity
	for _, n := range g.m.nodes {
		if n.typ == typ {
			out = append(out, n)
		}
	}
	return out
}

// conditional returns the model's conditional outputs, which are no else.
func (g *undoGen) conditional() []undoEntity {
	var out []undoEntity
	for _, o := range g.m.outputs {
		if o.kind != "else" {
			out = append(out, o)
		}
	}
	return out
}

// port returns a random node outside the trash that keep keeps, which a
// connection may leave, and the port that it leaves it by: "" for an
// element's out, or an output of a branch outside the trash, or false if
// there is none.
func (g *undoGen) port(keep func(undoEntity) bool) (undoEntity, string, bool) {
	n, ok := g.pick(g.m.nodes, func(n undoEntity) bool {
		if !n.live || !keep(n) {
			return false
		}
		return n.typ == "element" || n.typ == "branch" && slices.ContainsFunc(g.m.outputs, func(o undoEntity) bool {
			return o.live && o.node == n.id
		})
	})
	if !ok || n.typ == "element" {
		return n, "", ok
	}
	o, _ := g.pick(g.m.outputs, func(o undoEntity) bool { return o.live && o.node == n.id })
	return n, o.id, true
}

// condition returns the source of a random condition, which reads a random
// variable of the model, in the trash or not, of its board or not, or a
// random attribute of a random component, compared with a value of its type
// half of the time, and of a random type otherwise, so that it compiles, or
// does not, and may compile otherwise once the variable or the attribute
// changes, which the undo must give back, and sometimes whether a random
// node was visited too; or a source that does not parse.
func (g *undoGen) condition() string {
	name, typ, ok := g.valueName()
	if !ok || g.r.IntN(6) == 0 {
		return []string{"true", "1 +"}[g.r.IntN(2)]
	}
	source := name + " == " + g.literalOf(typ)
	if g.r.IntN(3) == 0 {
		if n, ok := g.pick(g.m.nodes, func(n undoEntity) bool { return n.typ != "jumper" || g.r.IntN(3) == 0 }); ok {
			source += " && visited(node(\"" + n.id + "\"))"
		}
	}
	return source
}

// script returns the source of a random script, which assigns a random
// variable or attribute of the model a value, as condition compares one, or
// updates it, as += does, or signals with its value.
func (g *undoGen) script() string {
	name, typ, ok := g.valueName()
	if !ok || g.r.IntN(6) == 0 {
		return []string{"signal(\"done\")", "= 1"}[g.r.IntN(2)]
	}
	switch g.r.IntN(4) {
	case 0:
		return name + " += 1"
	case 1:
		return "signal(\"seen\", " + name + ")"
	}
	return name + " = " + g.literalOf(typ)
}

// valueName returns the name of a random variable of the model, in the
// trash or not, or of a random attribute of a random component, mostly one
// of the component's type, and its type, with an enum's first option as its
// type, or false if the model has neither.
func (g *undoGen) valueName() (name, typ string, ok bool) {
	if c, ok := g.pick(g.m.components, func(undoEntity) bool { return true }); ok && g.r.IntN(3) == 0 {
		own := g.r.IntN(4) > 0
		if a, ok := g.pick(g.m.attributes, func(a undoEntity) bool {
			return !own || a.componentType == c.componentType
		}); ok {
			typ := a.typ
			if typ == "enum" && len(a.options) > 0 {
				typ = "enum:" + a.options[0]
			}
			return c.key + "." + a.key, typ, true
		}
	}
	v, ok := g.pick(g.m.variables, func(undoEntity) bool { return true })
	return v.key, v.typ, ok
}

// literalOf returns, half of the time, the source of a literal of the type
// typ, as valueName gives it, and otherwise one of a random type.
func (g *undoGen) literalOf(typ string) string {
	if g.coin() {
		return g.literal()
	}
	switch {
	case typ == "bool":
		return "true"
	case typ == "int":
		return "1"
	case typ == "float":
		return "1.5"
	case strings.HasPrefix(typ, "enum:"):
		return strconv.Quote(strings.TrimPrefix(typ, "enum:"))
	}
	return "\"x\""
}

// richText returns a random rich-text document: a paragraph of text, which
// shows, half of the time, a random variable of the model, in the trash or
// not, or a random attribute of a random component, and mentions a random
// component, so that the index of what the texts name changes with them.
func (g *undoGen) richText() json.RawMessage {
	inlines := []string{textInline(g.text())}
	if v, ok := g.pick(g.m.variables, func(undoEntity) bool { return true }); ok && g.coin() {
		inlines = append(inlines, valueInline(v.id))
	}
	c, okC := g.pick(g.m.components, func(undoEntity) bool { return true })
	if a, ok := g.pick(g.m.attributes, func(undoEntity) bool { return true }); ok && okC && g.coin() {
		inlines = append(inlines, attributeInline(c.id, a.id))
	}
	if okC && g.coin() {
		inlines = append(inlines, mentionInline(c.id))
	}
	return richText(inlines...)
}

// literal returns the source of a literal of a random type.
func (g *undoGen) literal() string {
	return []string{"true", "1", "1.5", "\"x\""}[g.r.IntN(4)]
}

// obj returns a payload of the fields kv, each a name and its value.
func obj(kv ...any) map[string]any {
	p := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		p[kv[i].(string)] = kv[i+1]
	}
	return p
}

// id returns the public ID of a new entity of the kind: a UUIDv7, as a client
// makes one, of a millisecond after the last ID's, and of random bits that r
// makes, so that the IDs, by which the model orders the entities that the
// generators pick from, are the seed's alone, where ids.New's are the clock's
// and the system's random source's.
func (g *undoGen) id(kind ids.Kind) string {
	g.ms++
	var r [10]byte
	for i := range r {
		r[i] = byte(g.r.UintN(256))
	}
	return ids.Encode(kind, ids.NewV7(int64(g.ms), r))
}

// coin returns true half of the time.
func (g *undoGen) coin() bool { return g.r.IntN(2) == 0 }

// word returns six random lowercase letters.
func (g *undoGen) word() string {
	var b strings.Builder
	for range 6 {
		b.WriteByte(byte('a' + g.r.IntN(26)))
	}
	return b.String()
}

func (g *undoGen) name() string  { return "Name " + g.word() }
func (g *undoGen) key() string   { return "k_" + g.word() }
func (g *undoGen) slug() string  { return "s-" + g.word() }
func (g *undoGen) text() string  { return "About " + g.word() }
func (g *undoGen) color() string { return []string{"none", "red", "teal", "gray"}[g.r.IntN(4)] }
func (g *undoGen) icon() string  { return []string{"none", "person", "item", "flag"}[g.r.IntN(4)] }

// variableType returns a random type of a variable.
func (g *undoGen) variableType() string {
	return []string{"bool", "int", "float", "string"}[g.r.IntN(4)]
}

// options returns two or three random options of an enum.
func (g *undoGen) options() []string {
	options := []string{"o" + g.word(), "o" + g.word()}
	if g.coin() {
		options = append(options, "o"+g.word())
	}
	return options
}

// value returns a random value of the type, one of the options for an enum.
func (g *undoGen) value(typ string, options []string) any {
	switch typ {
	case "bool":
		return g.coin()
	case "int":
		return g.r.IntN(2001) - 1000
	case "float":
		return float64(g.r.IntN(20001)-10000) / 4
	case "string":
		return "s" + g.word()
	case "text":
		return "t " + g.word() + "\n" + g.word()
	}
	return options[g.r.IntN(len(options))]
}

// position returns a random position on a board's canvas.
func (g *undoGen) position() map[string]any {
	return obj("x", float64(g.r.IntN(200001)-100000)/4, "y", float64(g.r.IntN(200001)-100000)/4)
}

// nodeFields adds to the node's payload p random fields of a node of the
// type typ, and a position.
func (g *undoGen) nodeFields(p map[string]any, typ string) {
	if g.coin() {
		p["title"] = g.name()
	}
	if g.coin() {
		p["color"] = g.color()
	}
	if g.coin() {
		p["tags"] = []string{"t" + g.word()}
	}
	if g.coin() {
		p["size"] = obj("width", float64(1+g.r.IntN(400)), "height", float64(1+g.r.IntN(400)))
	}
	if typ != "jumper" {
		if g.coin() {
			p["on_enter"] = []string{"", g.script()}[g.r.IntN(2)]
		}
		if g.coin() {
			p["on_exit"] = []string{"", g.script()}[g.r.IntN(2)]
		}
	}
	switch typ {
	case "element":
		if g.coin() {
			p["kind"] = []string{"dialogue", "narration"}[g.r.IntN(2)]
		}
		if g.coin() {
			p["text"] = g.richText()
		}
		if c, ok := g.pick(g.m.components, func(undoEntity) bool { return true }); ok && g.coin() {
			p["speaker_component_id"] = c.id
		}
		if g.coin() {
			p["cover_asset_id"] = g.asset
		}
		if g.coin() {
			p["is_ending"] = g.coin()
		}
	case "jumper":
		if n, ok := g.pick(g.m.nodes, func(undoEntity) bool { return true }); ok && g.coin() {
			p["target"] = obj("board_id", n.board, "node_id", n.id)
		}
	}
}

// maybeFolder sets, half of the time, the field of p to a random folder of
// the kind that is outside the trash, if there is one.
func (g *undoGen) maybeFolder(p map[string]any, field, kind string) {
	if f, ok := g.pick(g.m.folders, func(f undoEntity) bool { return f.live && f.kind == kind }); ok && g.coin() {
		p[field] = f.id
	}
}

// maybeOrder sets, half of the time, p's order to a random key.
func (g *undoGen) maybeOrder(p map[string]any) {
	const digits = "123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	if g.coin() {
		p["order"] = string(digits[g.r.IntN(len(digits))])
	}
}

// pick returns a random entity of es that keep keeps, or false if none is.
func (g *undoGen) pick(es []undoEntity, keep func(undoEntity) bool) (undoEntity, bool) {
	var kept []undoEntity
	for _, e := range es {
		if keep(e) {
			kept = append(kept, e)
		}
	}
	if len(kept) == 0 {
		return undoEntity{}, false
	}
	return kept[g.r.IntN(len(kept))], true
}

// live returns a random entity of es that is outside the trash.
func (g *undoGen) live(es []undoEntity) (undoEntity, bool) {
	return g.pick(es, func(e undoEntity) bool { return e.live })
}

// onLive returns the payload that build makes for a random entity of es that
// is outside the trash, or false if none is.
func (g *undoGen) onLive(es []undoEntity, build func(undoEntity) map[string]any) (map[string]any, bool) {
	e, ok := g.live(es)
	if !ok {
		return nil, false
	}
	return build(e), true
}

// idOf returns the payload that names a random entity of es: one outside the
// trash if live is set, for a deletion, and otherwise one deleted itself,
// for a restoration.
func (g *undoGen) idOf(es []undoEntity, live bool) (map[string]any, bool) {
	e, ok := g.pick(es, func(e undoEntity) bool { return live && e.live || !live && e.deleted })
	return obj("id", e.id), ok
}
