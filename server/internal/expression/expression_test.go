package expression_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// fixture is a database of the test's own with a workspace whose owner is
// owner, a project of it, created with the default component types, with a
// board, harbour, and the pipeline of every command that changes what the
// project's expressions name or own, with the derivation that compiles them.
type fixture struct {
	db          *database.DB
	admin       *pgx.Conn
	ws          ids.UUID
	owner       ids.UUID
	project     ids.UUID
	harbour     string
	pipeline    *commands.Pipeline
	nodes       *node.Service
	connections *connection.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	f := &fixture{db: d.Open(t), admin: d.Admin(t), ws: ids.New()}
	var err error
	if f.owner, err = isolationtest.NewUser(ctx, f.admin); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', $2, 'unlimited')`, f.ws,
		"harbour-"+f.ws.String())
	f.exec(t, `INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, 'owner')`, ids.New(),
		f.ws, f.owner)
	reg := commands.NewRegistry()
	project.Register(reg)
	board.Register(reg)
	variable.Register(reg)
	component.Register(reg)
	node.Register(reg)
	connection.Register(reg)
	expression.Register(reg)
	ent := entitlements.New(entitlements.Options{Meter: usage.Meter{}})
	f.pipeline = commands.New(f.db, commands.Options{Registry: reg, Entitlements: ent})
	projects := project.New(f.db, project.Options{Entitlements: ent, Pipeline: f.pipeline, Seed: component.Seed})
	f.nodes = node.New(f.db, node.Options{Projects: projects})
	f.connections = connection.New(f.db, connection.Options{Projects: projects})
	p, err := projects.Create(ctx, f.owner, f.ws, project.Settings{Name: "Harbour"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	f.project = decode(t, ids.Project, p.ID)
	f.harbour = f.newBoard(t, "Harbour")
	return f
}

// exec runs a statement as the superuser.
func (f *fixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.admin.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// cmd is a command of a batch: its type and its payload.
type cmd struct {
	typ     commands.Type
	payload any
}

// payload is a command's payload, as JSON encodes it.
type payload = map[string]any

// apply applies the commands to the fixture's project as one batch, as the
// owner.
func (f *fixture) apply(t *testing.T, cmds ...cmd) (commands.Result, error) {
	t.Helper()
	batch := make([]commands.Command, len(cmds))
	for i, c := range cmds {
		raw, err := json.Marshal(c.payload)
		if err != nil {
			t.Fatal(err)
		}
		batch[i] = commands.Command{ID: ids.New(), Type: c.typ, Payload: raw}
	}
	return f.pipeline.Apply(context.Background(), commands.Actor{UserID: f.owner}, f.project, batch)
}

// must applies the commands as apply does, and fails the test unless they
// apply.
func (f *fixture) must(t *testing.T, cmds ...cmd) commands.Result {
	t.Helper()
	res, err := f.apply(t, cmds...)
	if err != nil {
		t.Fatalf("%v: %v", cmds, err)
	}
	return res
}

// undo applies the inverse of the fixture's project's batch whose seq is seq,
// which the operation log keeps.
func (f *fixture) undo(t *testing.T, seq int64) {
	t.Helper()
	var steps []struct {
		Type    commands.Type   `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	var inverse []byte
	if err := f.admin.QueryRow(context.Background(),
		"SELECT inverse FROM operation WHERE project_id = $1 AND seq = $2", f.project, seq).Scan(&inverse); err != nil ||
		json.Unmarshal(inverse, &steps) != nil {
		t.Fatalf("the inverse of %d, %s: %v", seq, inverse, err)
	}
	batch := make([]commands.Command, len(steps))
	for i, st := range steps {
		batch[i] = commands.Command{ID: ids.New(), Type: st.Type, Payload: st.Payload}
	}
	if _, err := f.pipeline.Apply(context.Background(), commands.Actor{UserID: f.owner}, f.project, batch); err != nil {
		t.Fatalf("the undo of %d: %v", seq, err)
	}
}

// newBoard returns the ID of a new board of the fixture's project.
func (f *fixture) newBoard(t *testing.T, name string) string {
	t.Helper()
	id := ids.Encode(ids.Board, ids.New())
	f.must(t, cmd{board.Create, payload{"id": id, "name": name}})
	return id
}

// newNode returns the ID of a new node of the board, of the type typ, with
// the fields of extra.
func (f *fixture) newNode(t *testing.T, boardID, typ string, extra payload) string {
	t.Helper()
	id := ids.Encode(ids.Node, ids.New())
	f.must(t, createNode(id, boardID, typ, extra))
	return id
}

// createNode returns the command that creates the node id of the type typ on
// the board, with the fields of extra.
func createNode(id, boardID, typ string, extra payload) cmd {
	p := payload{"id": id, "board_id": boardID, "type": typ, "position": payload{"x": 0, "y": 0}}
	for k, v := range extra {
		p[k] = v
	}
	return cmd{node.Create, p}
}

// createVariable returns the command that creates the global variable id of
// the key and the type, or, if boardID is not "", the variable of the board.
func createVariable(id, key, typ, boardID string) cmd {
	p := payload{"id": id, "key": key, "type": typ, "scope": "global"}
	if boardID != "" {
		p["scope"], p["board_id"] = "board", boardID
	}
	return cmd{variable.Create, p}
}

// newVariable returns the ID of a new global variable of the key and the
// type.
func (f *fixture) newVariable(t *testing.T, key, typ string) string {
	t.Helper()
	id := ids.Encode(ids.Variable, ids.New())
	f.must(t, createVariable(id, key, typ, ""))
	return id
}

// node returns the node id, as the API shows it.
func (f *fixture) node(t *testing.T, id string) node.Node {
	t.Helper()
	n, err := f.nodes.Get(context.Background(), commands.Actor{UserID: f.owner}, f.project, decode(t, ids.Node, id))
	if err != nil {
		t.Fatalf("read the node %s: %v", id, err)
	}
	return n
}

// connection returns the connection id, as the API shows it.
func (f *fixture) connection(t *testing.T, id string) connection.Connection {
	t.Helper()
	c, err := f.connections.Get(context.Background(), commands.Actor{UserID: f.owner}, f.project,
		decode(t, ids.Connection, id))
	if err != nil {
		t.Fatalf("read the connection %s: %v", id, err)
	}
	return c
}

// connect returns the ID of a new connection between the elements from and
// to, with the fields of extra.
func (f *fixture) connect(t *testing.T, from, to string, extra payload) string {
	t.Helper()
	id := ids.Encode(ids.Connection, ids.New())
	p := payload{"id": id, "from_node_id": from, "to_node_id": to}
	for k, v := range extra {
		p[k] = v
	}
	f.must(t, cmd{connection.Create, p})
	return id
}

// decode returns the UUID of the ID id of the kind.
func decode(t *testing.T, kind ids.Kind, id string) ids.UUID {
	t.Helper()
	u, err := ids.Decode(kind, id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// valid fails the test unless e is a valid expression of the source, with an
// AST and its version, and returns the AST.
func valid(t *testing.T, what string, e *expression.Expression, source string) string {
	t.Helper()
	switch {
	case e == nil:
		t.Fatalf("%s has no expression, want %q", what, source)
	case e.Source != source || !e.IsValid || e.Error != nil || len(e.AST) == 0 || e.ASTVersion != 1:
		t.Fatalf("%s is %+v, want the valid expression of %q", what, e, source)
	}
	return string(e.AST)
}

// invalid fails the test unless e is the invalid expression of the source,
// whose error has the code, without an AST.
func invalid(t *testing.T, what string, e *expression.Expression, source, code string) {
	t.Helper()
	switch {
	case e == nil:
		t.Fatalf("%s has no expression, want %q", what, source)
	case e.Source != source || e.IsValid || len(e.AST) != 0 || e.ASTVersion != 0 || e.Error == nil ||
		e.Error.Code != code:
		t.Fatalf("%s is %+v (error %+v), want the invalid expression of %q, of %s", what, e, e.Error, source, code)
	}
}

// changed reports whether the batch's answer res lists the entity id among
// what it changed.
func changed(res commands.Result, kind ids.Kind, id ids.UUID) bool {
	return slices.ContainsFunc(res.Revisions, func(r commands.Revision) bool { return r.Kind == kind && r.ID == id })
}

// TestCompile checks that a script saved on a node is stored with its AST,
// once its batch has applied, which names the variables, the components, and
// the attribute definitions that it reads and writes by their IDs, never by
// their keys, a component's runtime attribute among them, which a script
// reads and writes as elara.trust, and that an attribute that is not runtime
// is kept, marked invalid by the type checker's attribute_not_runtime
// (FR-COMP-03 AC2).
//
// Verifies: FR-COMP-03/AC2
func TestCompile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	met := f.newVariable(t, "met_elara", "bool")
	var typeID ids.UUID
	if err := f.admin.QueryRow(context.Background(), `SELECT id FROM component_type WHERE project_id = $1
		AND key = 'character'`, f.project).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	character := ids.Encode(ids.ComponentType, typeID)
	trust, title := ids.Encode(ids.AttributeDef, ids.New()), ids.Encode(ids.AttributeDef, ids.New())
	elara := ids.Encode(ids.Component, ids.New())
	f.must(t,
		cmd{component.CreateAttribute, payload{"id": trust, "component_type_id": character, "name": "Trust",
			"key": "trust", "type": "int", "runtime": true}},
		cmd{component.CreateAttribute, payload{"id": title, "component_type_id": character, "name": "Title",
			"key": "honorific", "type": "string"}},
		cmd{component.Create, payload{"id": elara, "component_type_id": character, "name": "Elara"}})

	keeper := f.newNode(t, f.harbour, "element", payload{"on_exit": "elara.trust += 1\nmet_elara = true",
		"on_enter": `elara.honorific = "Captain"`})
	n := f.node(t, keeper)
	ast := valid(t, "the keeper's on-exit", n.OnExit, "elara.trust += 1\nmet_elara = true")
	for _, id := range []string{met, elara, trust} {
		if !strings.Contains(ast, `"`+id+`"`) {
			t.Errorf("the on-exit's AST %s does not name %s by its ID", ast, id)
		}
	}
	invalid(t, "the keeper's on-enter", n.OnEnter, `elara.honorific = "Captain"`, "attribute_not_runtime")
	if n.Rev != 1 {
		t.Errorf("the node created with its scripts is at the revision %d, want 1", n.Rev)
	}
	// The AST is in its canonical form, which the API gives whatever jsonb
	// makes of it.
	want := `{"kind":"script","statements":[{"kind":"assign","op":"+=","target":{"attribute":"` + trust +
		`","kind":"attr","name":"elara.trust","ref":"` + elara + `"},"value":{"kind":"lit","type":"int","value":1}},` +
		`{"kind":"assign","op":"=","target":{"kind":"var","name":"met_elara","ref":"` + met + `"},"value":` +
		`{"kind":"lit","type":"bool","value":true}}]}`
	if ast != want {
		t.Errorf("the on-exit's AST is %s, want %s", ast, want)
	}
}

// TestConditions checks that a connection's condition is compiled as a
// condition, so that one that assigns is kept, marked invalid with
// condition_must_be_pure, as the parser gives it (FR-EXPR-02 AC3), that one
// that fails to parse is kept, marked invalid with its error and its range,
// and that the on-traverse script beside it is compiled as a script.
//
// Verifies: FR-EXPR-02/AC3, FR-EDIT-03/AC4
func TestConditions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.newVariable(t, "met_elara", "bool")
	keeper := f.newNode(t, f.harbour, "element", nil)
	gull := f.newNode(t, f.harbour, "element", nil)

	pure := f.connect(t, keeper, gull, payload{"condition": "met_elara = true", "on_traverse": "met_elara = true"})
	c := f.connection(t, pure)
	invalid(t, "a condition that assigns", c.Condition, "met_elara = true", "condition_must_be_pure")
	valid(t, "the on-traverse script", c.OnTraverse, "met_elara = true")

	broken := f.connect(t, keeper, gull, payload{"condition": "met_elara =="})
	c = f.connection(t, broken)
	invalid(t, "a condition that fails to parse", c.Condition, "met_elara ==", "unexpected_end")
	if c.Condition.Error.Start != 12 || c.Condition.Error.End != 12 {
		t.Errorf("the error of %q covers %d to %d, want the end of the source", c.Condition.Source,
			c.Condition.Error.Start, c.Condition.Error.End)
	}

	// Writing it anew compiles it anew; the empty string removes it.
	f.must(t, cmd{connection.Update, payload{"id": broken, "condition": "met_elara == true"}})
	valid(t, "the rewritten condition", f.connection(t, broken).Condition, "met_elara == true")
	res := f.must(t, cmd{connection.Update, payload{"id": broken, "condition": ""}})
	if c := f.connection(t, broken); c.Condition != nil || c.Rev != 3 {
		t.Errorf("the connection whose condition is removed is %+v", c)
	}
	f.undo(t, res.Seq)
	valid(t, "the condition that the undo sets back", f.connection(t, broken).Condition, "met_elara == true")
}

// TestRecompile checks that an expression is what compiling its source
// against the project as the batch leaves it gives: that a batch that writes
// a condition before it creates the variable that the condition reads
// compiles it against the variable, and that a change of what a source names,
// a variable's deletion, restoration, retype, or rename, a node's deletion or
// creation, compiles it again, and changes the revision of its owner, which
// the batch's answer lists, and that the undo of each gives back what the
// batch found.
func TestRecompile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	keeper := f.newNode(t, f.harbour, "element", nil)
	gull := f.newNode(t, f.harbour, "element", nil)
	id := f.connect(t, keeper, gull, nil)
	conn := decode(t, ids.Connection, id)

	trust := ids.Encode(ids.Variable, ids.New())
	f.must(t, cmd{connection.Update, payload{"id": id, "condition": "trust >= 3"}},
		createVariable(trust, "trust", "int", ""))
	valid(t, "a condition of a variable that its batch creates after it", f.connection(t, id).Condition,
		"trust >= 3")

	rev := f.connection(t, id).Rev
	res := f.must(t, cmd{variable.Delete, payload{"id": trust}})
	c := f.connection(t, id)
	invalid(t, "a condition of a deleted variable", c.Condition, "trust >= 3", "unknown_identifier")
	if c.Rev != rev+1 || !changed(res, ids.Connection, conn) {
		t.Errorf("the variable's deletion left the connection at %d, want %d, and listed %v", c.Rev, rev+1,
			res.Revisions)
	}
	f.undo(t, res.Seq)
	valid(t, "the condition once the deletion is undone", f.connection(t, id).Condition, "trust >= 3")

	res = f.must(t, cmd{variable.Retype, payload{"id": trust, "type": "string", "default": "none"}})
	invalid(t, "a condition of a retyped variable", f.connection(t, id).Condition, "trust >= 3", "type_mismatch")
	f.undo(t, res.Seq)
	valid(t, "the condition once the retype is undone", f.connection(t, id).Condition, "trust >= 3")

	// Until a rename rewrites the sources that name the variable (plan item
	// 47), a source that names its old key no longer compiles.
	res = f.must(t, cmd{variable.Rename, payload{"id": trust, "key": "rep"}})
	invalid(t, "a condition of a renamed variable", f.connection(t, id).Condition, "trust >= 3",
		"unknown_identifier")
	f.undo(t, res.Seq)
	valid(t, "the condition once the rename is undone", f.connection(t, id).Condition, "trust >= 3")

	// A board's variable is read from its board alone.
	other := f.newBoard(t, "Pier")
	f.must(t, createVariable(ids.Encode(ids.Variable, ids.New()), "tide", "int", other),
		cmd{connection.Update, payload{"id": id, "on_traverse": "tide += 1"}})
	invalid(t, "a script of another board's variable", f.connection(t, id).OnTraverse, "tide += 1",
		"board_variable_out_of_scope")

	// A node reference names a node that is not in the trash.
	lighthouse := ids.Encode(ids.Node, ids.New())
	source := `visited(node("` + lighthouse + `"))`
	f.must(t, cmd{connection.Update, payload{"id": id, "condition": source}},
		createNode(lighthouse, other, "element", nil))
	valid(t, "a condition of a node that its batch creates after it", f.connection(t, id).Condition, source)
	res = f.must(t, cmd{node.Delete, payload{"id": lighthouse}})
	invalid(t, "a condition of a deleted node", f.connection(t, id).Condition, source, "unknown_node")
	f.undo(t, res.Seq)
	valid(t, "the condition once the node's deletion is undone", f.connection(t, id).Condition, source)

	// An owner in the trash keeps its revision, which nothing changes
	// meanwhile, so that the undo of its deletion applies, and its scripts
	// are current when it comes back.
	scripted := f.newNode(t, f.harbour, "element", payload{"on_enter": "trust = 1"})
	deletion := f.must(t, cmd{node.Delete, payload{"id": scripted}})
	if res := f.must(t, cmd{variable.Delete, payload{"id": trust}}); changed(res, ids.Node,
		decode(t, ids.Node, scripted)) {
		t.Errorf("the deletion of a variable changed a node in the trash: %v", res.Revisions)
	}
	f.undo(t, deletion.Seq)
	invalid(t, "the script of a node restored once its variable is deleted", f.node(t, scripted).OnEnter,
		"trust = 1", "unknown_identifier")

	// A batch that changes nothing that a source names compiles nothing,
	// and leaves its owner as it is.
	rev = f.connection(t, id).Rev
	if res := f.must(t, cmd{node.Update, payload{"id": keeper, "title": "Keeper"}}); changed(res, ids.Connection,
		conn) || f.connection(t, id).Rev != rev {
		t.Errorf("a node's retitle changed the connection: %v", res.Revisions)
	}
}

// TestScripts checks that a node's scripts are an element's or a branch's,
// that a jumper's are refused, that the empty string removes one, that the
// undo of a change of one sets it back, that a change of one alone changes
// the node's revision, and that a duplicate of a node has copies of its
// scripts, compiled at its board.
func TestScripts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.newVariable(t, "met_elara", "bool")
	branch := f.newNode(t, f.harbour, "branch", payload{"on_enter": "met_elara = true"})
	valid(t, "a branch's on-enter", f.node(t, branch).OnEnter, "met_elara = true")

	_, err := f.apply(t, createNode(ids.Encode(ids.Node, ids.New()), f.harbour, "jumper",
		payload{"on_exit": "met_elara = true"}))
	var invalidErr *commands.InvalidError
	if !errors.As(err, &invalidErr) || len(invalidErr.Problems) != 1 || invalidErr.Problems[0].Field != "payload.on_exit" {
		t.Errorf("a jumper with a script: %v, want it refused at on_exit", err)
	}
	jumper := f.newNode(t, f.harbour, "jumper", nil)
	if _, err := f.apply(t, cmd{node.Update, payload{"id": jumper, "on_enter": "met_elara = true"}}); err == nil {
		t.Error("a jumper's update with a script applied")
	}

	keeper := f.newNode(t, f.harbour, "element", nil)
	res := f.must(t, cmd{node.Update, payload{"id": keeper, "on_exit": "met_elara = false"}})
	n := f.node(t, keeper)
	valid(t, "an element's on-exit", n.OnExit, "met_elara = false")
	if n.Rev != 2 || !changed(res, ids.Node, decode(t, ids.Node, keeper)) {
		t.Errorf("the node whose on-exit alone changed is at %d, and the batch listed %v", n.Rev, res.Revisions)
	}
	if again := f.must(t, cmd{node.Update, payload{"id": keeper, "on_exit": "met_elara = false"}}); len(
		again.Revisions) != 0 {
		t.Errorf("setting the script that the node has changed %v", again.Revisions)
	}

	copied := ids.Encode(ids.Node, ids.New())
	f.must(t, cmd{node.Duplicate, payload{"id": copied, "source_id": keeper, "position": payload{"x": 8, "y": 8}}})
	valid(t, "a duplicate's on-exit", f.node(t, copied).OnExit, "met_elara = false")

	f.undo(t, res.Seq)
	if n := f.node(t, keeper); n.OnExit != nil {
		t.Errorf("the undo of the node's first script left %+v", n.OnExit)
	}
	res = f.must(t, cmd{node.Update, payload{"id": copied, "on_exit": ""}})
	if n := f.node(t, copied); n.OnExit != nil {
		t.Errorf("the empty string left the on-exit %+v", n.OnExit)
	}
	f.undo(t, res.Seq)
	valid(t, "the on-exit that the undo sets back", f.node(t, copied).OnExit, "met_elara = false")
}

// TestSources checks that a source of more than expression.MaxSourceLength
// characters is refused at its field, as one that holds a NUL is, which the
// pipeline refuses in every text of a payload.
func TestSources(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	keeper := f.newNode(t, f.harbour, "element", nil)
	for _, source := range []string{"x = 1 // " + strings.Repeat("é", expression.MaxSourceLength), "x = \x00"} {
		_, err := f.apply(t, cmd{node.Update, payload{"id": keeper, "on_enter": source}})
		var invalidErr *commands.InvalidError
		if !errors.As(err, &invalidErr) || len(invalidErr.Problems) != 1 ||
			invalidErr.Problems[0].Field != "payload.on_enter" {
			t.Errorf("a source of %d characters: %v, want it refused at on_enter", len([]rune(source)), err)
		}
	}
	long := "x = 1 // " + strings.Repeat("é", expression.MaxSourceLength-9)
	f.must(t, cmd{node.Update, payload{"id": keeper, "on_enter": long}})
	invalid(t, "a source of the most characters", f.node(t, keeper).OnEnter, long, "unknown_identifier")
}

// TestUnregistered checks that a registry without the derivation that
// compiles expressions refuses to store a source, rather than store one that
// nothing compiles.
func TestUnregistered(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	reg := commands.NewRegistry()
	project.Register(reg)
	board.Register(reg)
	component.Register(reg)
	node.Register(reg)
	pipeline := commands.New(f.db, commands.Options{Registry: reg,
		Entitlements: entitlements.New(entitlements.Options{Meter: usage.Meter{}})})
	raw, _ := json.Marshal(payload{"id": ids.Encode(ids.Node, ids.New()), "board_id": f.harbour, "type": "element",
		"position": payload{"x": 0, "y": 0}, "on_enter": "x = 1"})
	_, err := pipeline.Apply(context.Background(), commands.Actor{UserID: f.owner}, f.project,
		[]commands.Command{{ID: ids.New(), Type: node.Create, Payload: raw}})
	var refused *commands.RefusedError
	if err == nil || errors.As(err, &refused) {
		t.Errorf("a script without the derivation: %v, want an error of the registry", err)
	}
	var n int64
	if err := f.admin.QueryRow(context.Background(), "SELECT count(*) FROM expression").Scan(&n); err != nil || n != 0 {
		t.Errorf("%d expressions stored: %v", n, err)
	}
}
