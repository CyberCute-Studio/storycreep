package variable_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/trash"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// fixture is a database of the test's own with a workspace whose owner is
// owner, a project of it, and the pipeline of the projects', the boards', and
// the variables' commands.
type fixture struct {
	d         *dbtest.Database
	db        *database.DB
	ws        ids.UUID
	owner     ids.UUID
	project   ids.UUID
	pipeline  *commands.Pipeline
	projects  *project.Service
	variables *variable.Service
	trash     *trash.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	f := &fixture{d: d, db: d.Open(t), ws: ids.New()}
	admin := d.Admin(t)
	var err error
	if f.owner, err = isolationtest.NewUser(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', $2,
		'unlimited')`, f.ws, "harbour-"+f.ws.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3,
		'owner')`, ids.New(), f.ws, f.owner); err != nil {
		t.Fatal(err)
	}
	reg := commands.NewRegistry()
	project.Register(reg)
	board.Register(reg)
	variable.Register(reg)
	ent := entitlements.New(entitlements.Options{Meter: usage.Meter{}})
	f.pipeline = commands.New(f.db, commands.Options{Registry: reg, Entitlements: ent})
	f.projects = project.New(f.db, project.Options{Entitlements: ent, Pipeline: f.pipeline})
	f.variables = variable.New(f.db, variable.Options{Projects: f.projects})
	f.trash = trash.New(f.db, trash.Options{Projects: f.projects})
	p, err := f.projects.Create(ctx, f.owner, f.ws, project.Settings{Name: "Harbour"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if f.project, err = ids.Decode(ids.Project, p.ID); err != nil {
		t.Fatal(err)
	}
	return f
}

// actor is the owner, as the actor of a batch.
func (f *fixture) actor() commands.Actor {
	return commands.Actor{UserID: f.owner}
}

// cmd is a command of a batch: its type and its payload.
type cmd struct {
	typ     commands.Type
	payload any
}

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
	return f.pipeline.Apply(context.Background(), f.actor(), f.project, batch)
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

// undo applies the inverse of the batch whose seq is seq, which the
// operation log keeps.
func (f *fixture) undo(t *testing.T, seq int64) {
	t.Helper()
	batch := f.inverse(t, seq)
	if len(batch) == 0 {
		return
	}
	if _, err := f.pipeline.Apply(context.Background(), f.actor(), f.project, batch); err != nil {
		t.Fatalf("undo %d: %v", seq, err)
	}
}

// inverse returns the inverse of the batch whose seq is seq, which the
// operation log keeps, as a batch of new commands.
func (f *fixture) inverse(t *testing.T, seq int64) []commands.Command {
	t.Helper()
	var steps []struct {
		Type    commands.Type   `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	var inverse []byte
	if err := f.d.Admin(t).QueryRow(context.Background(),
		"SELECT inverse FROM operation WHERE project_id = $1 AND seq = $2", f.project, seq).Scan(&inverse); err != nil ||
		json.Unmarshal(inverse, &steps) != nil {
		t.Fatalf("the inverse of %d, %s: %v", seq, inverse, err)
	}
	batch := make([]commands.Command, len(steps))
	for i, st := range steps {
		batch[i] = commands.Command{ID: ids.New(), Type: st.Type, Payload: st.Payload}
	}
	return batch
}

// state reads every variable, board, and folder of the fixture's project as
// the superuser, the deleted ones too, but for their revisions and
// timestamps: what a change and its undo must leave alike.
func (f *fixture) state(t *testing.T) string {
	t.Helper()
	return f.read(t, true)
}

// live reads them as state does, but for the variables, boards, and folders
// that are deleted themselves: what a creation and its undo, which deletes
// what was created, must leave alike.
func (f *fixture) live(t *testing.T) string {
	t.Helper()
	return f.read(t, false)
}

// read reads the fixture's project as state does, the deleted rows too if
// deleted is set.
func (f *fixture) read(t *testing.T, deleted bool) string {
	t.Helper()
	var s string
	if err := f.d.Admin(t).QueryRow(context.Background(), `SELECT concat_ws(' / ',
		(SELECT string_agg(concat_ws('|', id, scope, board_id, folder_id, "order", key, type, default_value::text,
			description, reset_on_enter, deleted_at IS NOT NULL), ', ' ORDER BY id) FROM variable
			WHERE project_id = $1 AND ($2 OR deleted_at IS NULL)),
		(SELECT string_agg(concat_ws('|', id, name, deleted_at IS NOT NULL), ', ' ORDER BY id) FROM board
			WHERE project_id = $1 AND ($2 OR deleted_at IS NULL)),
		(SELECT string_agg(concat_ws('|', id, kind, parent_id, name, "order", deleted_at IS NOT NULL), ', '
			ORDER BY id) FROM folder WHERE project_id = $1 AND ($2 OR deleted_at IS NULL)))`, f.project,
		deleted).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// variableID, boardID, and folderID return new IDs of a variable, a board,
// and a folder.
func variableID() string { return ids.Encode(ids.Variable, ids.New()) }
func boardID() string    { return ids.Encode(ids.Board, ids.New()) }
func folderID() string   { return ids.Encode(ids.Folder, ids.New()) }

// get returns the variable id of the fixture's project, or fails the test.
func (f *fixture) get(t *testing.T, id string) variable.Variable {
	t.Helper()
	u, err := ids.Decode(ids.Variable, id)
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.variables.Get(context.Background(), f.actor(), f.project, u)
	if err != nil {
		t.Fatalf("the variable %s: %v", id, err)
	}
	return v
}

// create creates a global bool variable whose key is key, with payload's
// other fields, which may override those, and returns it.
func (f *fixture) create(t *testing.T, key string, payload map[string]any) variable.Variable {
	t.Helper()
	c := newVariable(key, payload)
	f.must(t, c)
	return f.get(t, c.payload.(map[string]any)["id"].(string))
}

// newVariable returns the command that creates a global bool variable whose
// key is key, with payload's other fields, which may override those, and a
// new ID unless payload has one.
func newVariable(key string, payload map[string]any) cmd {
	p := map[string]any{"id": variableID(), "key": key, "type": "bool", "scope": "global"}
	for k, v := range payload {
		p[k] = v
	}
	return cmd{variable.Create, p}
}

// newBoard creates a board named name, and returns its ID.
func (f *fixture) newBoard(t *testing.T, name string) string {
	t.Helper()
	id := boardID()
	f.must(t, cmd{board.Create, map[string]any{"id": id, "name": name}})
	return id
}

// list returns the keys of the variables that the project lists.
func (f *fixture) list(t *testing.T) []string {
	t.Helper()
	vs, err := f.variables.List(context.Background(), f.actor(), f.project, ids.UUID{}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, v := range vs {
		keys = append(keys, v.Key)
	}
	slices.Sort(keys)
	return keys
}

// refused returns the refusal of the batch that err refused, or fails the
// test if err is not one.
func refused(t *testing.T, err error) *commands.RefusedError {
	t.Helper()
	var r *commands.RefusedError
	if !errors.As(err, &r) {
		t.Fatalf("the batch was not refused by a command: %v", err)
	}
	return r
}

// invalidAt fails the test unless err is a batch that is not valid, with a
// problem of its first command at the field.
func invalidAt(t *testing.T, err error, field string) {
	t.Helper()
	var invalid *commands.InvalidError
	if !errors.As(err, &invalid) || !slices.ContainsFunc(invalid.Problems, func(p commands.Problem) bool {
		return p.Command == 0 && p.Field == field
	}) {
		t.Errorf("the batch was not refused as invalid at %s: %v", field, err)
	}
}

// conflictState returns the state of the variable id that a rename which
// expects its revision rev, a stale one, is answered with, or fails the test
// unless the rename is answered with a revision conflict.
func (f *fixture) conflictState(t *testing.T, id string, rev int64) variable.Variable {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"id": id, "key": "renamed"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.pipeline.Apply(context.Background(), f.actor(), f.project, []commands.Command{{ID: ids.New(),
		Type: variable.Rename, Payload: raw, ExpectedRev: map[commands.Ref]int64{
			{Kind: ids.Variable, ID: decodeID(t, ids.Variable, id)}: rev}}})
	var conflict *commands.RevisionConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 {
		t.Fatalf("a rename of %s that expects the revision %d: %v", id, rev, err)
	}
	var state variable.Variable
	if err := json.Unmarshal(conflict.Conflicts[0].State, &state); err != nil {
		t.Fatalf("the state of %s: %s, %v", id, conflict.Conflicts[0].State, err)
	}
	return state
}

// TestKeys checks that a variable's key is an identifier of the expression
// language, which a variable is created and renamed with, and that any other
// is refused, at the payload's key, as not of the form or as a reserved word.
//
// Verifies: FR-VAR-01/AC1
func TestKeys(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, key := range []string{"met_elara", "_x", "Met_Elara", "True", "elseif_", strings.Repeat("k", 64)} {
		if v := f.create(t, key, nil); v.Key != key {
			t.Errorf("a variable created with the key %q has %q", key, v.Key)
		}
	}
	other := f.create(t, "harbour_rumours", nil)
	for key, want := range map[string]ident.Reason{
		"":                      ident.Format,
		"9lives":                ident.Format,
		"met-elara":             ident.Format,
		"met elara":             ident.Format,
		"mét":                   ident.Format,
		strings.Repeat("k", 65): ident.Format,
		"true":                  ident.Reserved,
		"global":                ident.Reserved,
		"board":                 ident.Reserved,
		"elseif":                ident.Reserved,
	} {
		for _, c := range []cmd{
			newVariable(key, nil),
			{variable.Rename, map[string]any{"id": other.ID, "key": key}},
		} {
			_, err := f.apply(t, c)
			r := refused(t, err)
			var e *ident.Error
			if !errors.As(r.Reason, &e) || e.Reason != want || r.Field != "key" {
				t.Errorf("%s with the key %q was refused for %v at %q, want %s", c.typ, key, r.Reason, r.Field, want)
			}
		}
	}
	f.must(t, cmd{variable.Rename, map[string]any{"id": other.ID, "key": "rumours"}})
	if v := f.get(t, other.ID); v.Key != "rumours" || v.ID != other.ID {
		t.Errorf("the renamed variable is %s %q", v.ID, v.Key)
	}
}

// TestKeyScopes checks that a key is unique among the project's global
// variables, and among each board's, of those that are not in the trash: a
// global and a board's variable may share one, as may two boards', a key is
// compared case by case, a deleted variable's key is free, and a variable
// that would take a key that another has, by its creation, its rename, its
// move to another scope, or its restoration, is refused for it.
func TestKeyScopes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.newBoard(t, "Harbour"), f.newBoard(t, "Lighthouse")
	rep := f.create(t, "rep", nil)
	f.create(t, "Rep", nil)
	onA := f.create(t, "rep", map[string]any{"scope": "board", "board_id": a})
	f.create(t, "rep", map[string]any{"scope": "board", "board_id": b})
	trust := f.create(t, "trust", map[string]any{"scope": "board", "board_id": a})
	other := f.create(t, "other", nil)
	taken := func(c cmd, field string) {
		t.Helper()
		_, err := f.apply(t, c)
		if r := refused(t, err); !errors.Is(r.Reason, variable.ErrKeyTaken) || r.Field != field {
			t.Errorf("%s %v was refused for %v at %q, want key_taken at %q", c.typ, c.payload, r.Reason, r.Field,
				field)
		}
	}
	taken(newVariable("rep", nil), "key")
	taken(newVariable("rep", map[string]any{"scope": "board", "board_id": a}), "key")
	taken(cmd{variable.Rename, map[string]any{"id": trust.ID, "key": "rep"}}, "key")
	taken(cmd{variable.Rename, map[string]any{"id": other.ID, "key": "rep"}}, "key")
	taken(cmd{variable.SetScope, map[string]any{"id": rep.ID, "scope": "board", "board_id": a}}, "board_id")
	taken(cmd{variable.SetScope, map[string]any{"id": onA.ID, "scope": "global"}}, "scope")
	// A variable keeps its own key, which changes nothing.
	if res := f.must(t, cmd{variable.Rename, map[string]any{"id": rep.ID, "key": "rep"}}); len(res.Revisions) != 0 {
		t.Errorf("renaming a variable to its own key changed %v", res.Revisions)
	}
	// A deleted variable's key is free, and its restoration is refused once
	// another has taken it, until that one has another.
	f.must(t, cmd{variable.Delete, map[string]any{"id": rep.ID}})
	again := f.create(t, "rep", nil)
	taken(cmd{variable.Restore, map[string]any{"id": rep.ID}}, "id")
	f.must(t, cmd{variable.Rename, map[string]any{"id": again.ID, "key": "rep_again"}},
		cmd{variable.Restore, map[string]any{"id": rep.ID}})
	if got := f.get(t, rep.ID); got.Key != "rep" {
		t.Errorf("the restored variable's key is %q", got.Key)
	}
	// A variable moves to another board, whose variables do not have its key.
	f.must(t, cmd{variable.SetScope, map[string]any{"id": trust.ID, "scope": "board", "board_id": b}})
	if got := f.get(t, trust.ID); got.Scope != "board" || got.BoardID != b {
		t.Errorf("the moved variable is %s of %q", got.Scope, got.BoardID)
	}
	if got, want := f.list(t), []string{"Rep", "other", "rep", "rep", "rep", "rep_again", "trust"}; !slices.Equal(got,
		want) {
		t.Errorf("the project lists %v, want %v", got, want)
	}
}

// TestDefaults checks that a variable's default is a value of its type,
// which a create, a change of the default, and a change of the type check, a
// string default on an int variable among them, and that a variable without
// one has its type's zero value, and keeps its default in its canonical form.
//
// Verifies: FR-VAR-01/AC2
func TestDefaults(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, c := range []struct {
		typ  string
		def  any
		want string
	}{
		{"int", nil, "0"}, {"float", nil, "0"}, {"bool", nil, "false"}, {"string", nil, `""`},
		{"int", json.RawMessage("3.0"), "3"}, {"int", json.RawMessage("-4e2"), "-400"}, {"float", 2.5, "2.5"},
		{"float", json.RawMessage("-0.0"), "0"}, {"bool", true, "true"}, {"string", "Elara\nwaits", `"Elara\nwaits"`},
		// A string is written without escaping <, >, and & (PRD §6.2).
		{"string", json.RawMessage(`"a\u003cb \u0026 c>"`), `"a<b & c>"`},
	} {
		p := map[string]any{"type": c.typ}
		if c.def != nil {
			p["default"] = c.def
		}
		v := f.create(t, "v_"+strings.ReplaceAll(ids.New().String(), "-", ""), p)
		// The default's own JSON, which json.Marshal would write again with
		// the escapes of <, >, and &.
		if got, _ := v.Default.MarshalJSON(); v.Type != c.typ || string(got) != c.want {
			t.Errorf("a %s created with %v has the default %s, want %s", c.typ, c.def, got, c.want)
		}
	}
	// A default of another kind than the type's is refused at the payload's
	// default, a string on an int first.
	for _, c := range []struct {
		typ string
		def any
	}{
		{"int", "3"}, {"int", 3.5}, {"int", json.RawMessage("9007199254740992")}, {"bool", 1}, {"float", "1.5"},
		{"string", false}, {"string", "bell\a"}, {"int", nil},
	} {
		p := map[string]any{"type": c.typ, "default": c.def}
		if c.def == nil {
			p["default"] = json.RawMessage("null")
		}
		_, err := f.apply(t, newVariable("refused", p))
		invalidAt(t, err, "payload.default")
	}
	// A number beyond a double's range is no JSON that the pipeline reads, so
	// it refuses the payload before its default is read.
	_, err := f.apply(t, newVariable("refused", map[string]any{"type": "float",
		"default": json.RawMessage("1e400")}))
	invalidAt(t, err, "payload")
	// A number written with more than 64 characters, or with an exponent
	// beyond ±999, is refused at the default, though it reads as a value of
	// the type; one at each bound is taken, and the operation log holds it
	// (TestLogHoldsNumbers).
	long := "0." + strings.Repeat("0", 63)
	for _, c := range []struct{ typ, def string }{{"float", "1e-1000"}, {"int", "0e1000"}, {"int", long},
		{"int", "0e1073741824"}, {"float", "-0.0E+99999999999"}, {"float", "1e-16384"}} {
		_, err = f.apply(t, newVariable("refused", map[string]any{"type": c.typ, "default": json.RawMessage(c.def)}))
		invalidAt(t, err, "payload.default")
	}
	for _, c := range []struct{ typ, def string }{{"float", "1e-999"}, {"int", "0e999"}, {"int", long[:64]},
		{"float", "-0.0e+999"}} {
		v := f.create(t, "v_"+strings.ReplaceAll(ids.New().String(), "-", ""),
			map[string]any{"type": c.typ, "default": json.RawMessage(c.def)})
		if got := string(must(json.Marshal(v.Default))); got != "0" {
			t.Errorf("a %s created with %s has the default %s, want 0", c.typ, c.def, got)
		}
	}
	n := f.create(t, "rumours", map[string]any{"type": "int", "default": 2})
	for _, def := range []string{long, "0e1000"} {
		_, err = f.apply(t, cmd{variable.SetDefault, map[string]any{"id": n.ID, "default": json.RawMessage(def)}})
		if r := refused(t, err); r.Field != "default" || r.Reason != nil {
			t.Errorf("a default written past the bounds was refused for %v at %q", r.Reason, r.Field)
		}
	}
	_, err = f.apply(t, cmd{variable.Retype, map[string]any{"id": n.ID, "type": "float",
		"default": json.RawMessage("0e1000")}})
	invalidAt(t, err, "payload.default")
	_, err = f.apply(t, cmd{variable.SetDefault, map[string]any{"id": n.ID, "default": "two"}})
	if r := refused(t, err); r.Field != "default" || r.Reason != nil {
		t.Errorf("a string default of an int was refused for %v at %q", r.Reason, r.Field)
	}
	_, err = f.apply(t, cmd{variable.SetDefault, map[string]any{"id": n.ID, "default": nil}})
	invalidAt(t, err, "payload.default")
	f.must(t, cmd{variable.SetDefault, map[string]any{"id": n.ID, "default": 7.0}})
	if got, _ := json.Marshal(f.get(t, n.ID).Default); string(got) != "7" {
		t.Errorf("the int's new default is %s, want 7", got)
	}
	// A change of type takes a default of the new type, or its zero value.
	_, err = f.apply(t, cmd{variable.Retype, map[string]any{"id": n.ID, "type": "bool", "default": 7}})
	invalidAt(t, err, "payload.default")
	// A string with a control character is refused as the retype applies, at
	// its default too.
	_, err = f.apply(t, cmd{variable.Retype, map[string]any{"id": n.ID, "type": "string", "default": "bell\a"}})
	if r := refused(t, err); r.Field != "default" || r.Reason != nil {
		t.Errorf("a string default with a control character was refused for %v at %q", r.Reason, r.Field)
	}
	f.must(t, cmd{variable.Retype, map[string]any{"id": n.ID, "type": "string", "default": "seven"}})
	if v := f.get(t, n.ID); v.Type != "string" || string(must(json.Marshal(v.Default))) != `"seven"` {
		t.Errorf("the retyped variable is a %s with %s", v.Type, must(json.Marshal(v.Default)))
	}
	f.must(t, cmd{variable.Retype, map[string]any{"id": n.ID, "type": "float"}})
	if v := f.get(t, n.ID); v.Type != "float" || string(must(json.Marshal(v.Default))) != "0" {
		t.Errorf("the variable retyped without a default is a %s with %s", v.Type, must(json.Marshal(v.Default)))
	}
	// A default that the database holds is read without the check of its
	// characters that a command's default passes, which the database's own
	// check does not make, so that a row that the pipeline did not write, or
	// a later change of that check, leaves the project's variables readable,
	// and their defaults and types changeable, though not set back.
	s := f.create(t, "rumour", map[string]any{"type": "string", "default": "kept"})
	control := func() {
		t.Helper()
		if _, err := f.d.Admin(t).Exec(context.Background(), `UPDATE variable SET type = 'string',
			default_value = to_jsonb('a' || chr(7) || 'b') WHERE id = $1`, decodeID(t, ids.Variable, s.ID)); err != nil {
			t.Fatal(err)
		}
	}
	control()
	res := f.must(t, cmd{variable.Retype, map[string]any{"id": s.ID, "type": "int"}})
	_, err = f.pipeline.Apply(context.Background(), f.actor(), f.project, f.inverse(t, res.Seq))
	if r := refused(t, err); r.Field != "default" {
		t.Errorf("the undo of the retype of a stored default with a control character was refused at %q", r.Field)
	}
	control()
	var stored string
	if err := json.Unmarshal(must(json.Marshal(f.get(t, s.ID).Default)), &stored); err != nil ||
		stored != "a"+string(rune(7))+"b" {
		t.Errorf("a stored default with a control character is read as %q, %v", stored, err)
	}
	if keys := f.list(t); !slices.Contains(keys, "rumour") {
		t.Errorf("with a stored default with a control character, the project lists %v", keys)
	}
	f.must(t, cmd{variable.SetDefault, map[string]any{"id": s.ID, "default": "plain"}})
}

// TestLogHoldsNumbers checks, against PostgreSQL, that the operation log's
// jsonb, which keeps a payload's default as the client wrote it, holds every
// number that an int's or a float's default takes, however it is written:
// the longest mantissas, of 59 characters, with the greatest exponents
// either way, of ±999, as numeric's limits, an exponent within ±(2^30 − 1)
// and at most 16,383 digits after the point, its exponent counted, are far
// past what the bounds let a number be written with.
//
// Verifies: FR-VAR-01/AC2
func TestLogHoldsNumbers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	conn := dbtest.New(t).Admin(t)
	mantissas := []string{"0", "-0", "0.0", "1", "-1.5", "3.000", "9007199254740991",
		"-0." + strings.Repeat("0", 55) + "1", "1" + strings.Repeat("0", 58), "9." + strings.Repeat("9", 56),
		"0." + strings.Repeat("0", 56)}
	exponents := []string{"", "e0", "E+5", "e-1", "e999", "e+999", "e-999", "E-0999"}
	for _, m := range mantissas {
		for _, e := range exponents {
			raw := m + e
			var v variable.Value
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				t.Fatalf("%s: %v", raw, err)
			}
			_, err := conn.Exec(ctx, "SELECT $1::text::jsonb", raw)
			for _, typ := range []string{variable.TypeInt, variable.TypeFloat} {
				if _, problem := v.Of(typ); problem == "" && err != nil {
					t.Errorf("a %s's default takes %s, which jsonb does not hold: %v", typ, raw, err)
				}
			}
		}
	}
}

// TestDescriptions checks that a variable's description is a text of at most
// MaxDescriptionLength characters, without white space around it, and without
// control characters but line feeds and tabs, which a create and a change of
// the description check, at the payload's description, and that a stored
// description that the check refuses, as a row that the pipeline did not
// write, or a later change of the check, leaves, can be changed, though not
// set back.
func TestDescriptions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.apply(t, newVariable("refused", map[string]any{"description": " padded"}))
	invalidAt(t, err, "payload.description")
	v := f.create(t, "rumours", map[string]any{"description": "What the harbour knows"})
	for _, d := range []string{" padded", "padded\n", "bell\a"} {
		_, err := f.apply(t, cmd{variable.Describe, map[string]any{"id": v.ID, "description": d}})
		if r := refused(t, err); r.Field != "description" || r.Reason != nil {
			t.Errorf("the description %q was refused for %v at %q", d, r.Reason, r.Field)
		}
	}
	_, err = f.apply(t, cmd{variable.Describe, map[string]any{"id": v.ID,
		"description": strings.Repeat("d", variable.MaxDescriptionLength+1)}})
	invalidAt(t, err, "payload.description")
	f.must(t, cmd{variable.Describe, map[string]any{"id": v.ID, "description": "What\n\tthe harbour knows"}})
	if got := f.get(t, v.ID).Description; got != "What\n\tthe harbour knows" {
		t.Errorf("the variable's description is %q", got)
	}
	if _, err := f.d.Admin(t).Exec(context.Background(), `UPDATE variable SET description = 'a' || chr(7) || 'b'
		WHERE id = $1`, decodeID(t, ids.Variable, v.ID)); err != nil {
		t.Fatal(err)
	}
	res := f.must(t, cmd{variable.Describe, map[string]any{"id": v.ID, "description": "plain"}})
	if got := f.get(t, v.ID).Description; got != "plain" {
		t.Errorf("the description that replaced a stored one with a control character is %q", got)
	}
	_, err = f.pipeline.Apply(context.Background(), f.actor(), f.project, f.inverse(t, res.Seq))
	if r := refused(t, err); r.Field != "description" {
		t.Errorf("the undo of the change of a stored description with a control character was refused at %q",
			r.Field)
	}
}

// must returns b, or panics with err.
func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

// TestScopes checks what keeps a variable's scope whole: a board's variable
// names a board of the project that is not in its trash, a global names none,
// and only a board's variable is reset as the flow enters its board, so that
// reset_on_enter is refused on a global, and a variable becomes global only
// once it is unset.
func TestScopes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, gone := f.newBoard(t, "Harbour"), f.newBoard(t, "Pier")
	f.must(t, cmd{board.Delete, map[string]any{"id": gone}})
	for _, c := range []struct {
		payload map[string]any
		field   string
	}{
		{map[string]any{"scope": "board"}, "payload.board_id"},
		{map[string]any{"scope": "board", "board_id": "bd_nope"}, "payload.board_id"},
		{map[string]any{"board_id": a}, "payload.board_id"},
		{map[string]any{"reset_on_enter": true}, "payload.reset_on_enter"},
	} {
		_, err := f.apply(t, newVariable("scoped", c.payload))
		invalidAt(t, err, c.field)
	}
	for _, b := range []string{boardID(), gone} {
		_, err := f.apply(t, newVariable("scoped", map[string]any{"scope": "board", "board_id": b}))
		if r := refused(t, err); r.Field != "board_id" {
			t.Errorf("a variable of a board that the project has not, or has in its trash, was refused at %q",
				r.Field)
		}
	}
	g := f.create(t, "met_elara", nil)
	_, err := f.apply(t, cmd{variable.SetResetOnEnter, map[string]any{"id": g.ID, "reset_on_enter": true}})
	if r := refused(t, err); r.Field != "reset_on_enter" {
		t.Errorf("reset_on_enter on a global was refused at %q", r.Field)
	}
	scratch := f.create(t, "asked_twice", map[string]any{"scope": "board", "board_id": a, "reset_on_enter": true})
	if !scratch.ResetOnEnter || scratch.Scope != "board" || scratch.BoardID != a {
		t.Errorf("the board's variable is %+v", scratch)
	}
	_, err = f.apply(t, cmd{variable.SetScope, map[string]any{"id": scratch.ID, "scope": "global"}})
	if r := refused(t, err); r.Field != "scope" {
		t.Errorf("making a variable that resets global was refused at %q", r.Field)
	}
	f.must(t, cmd{variable.SetResetOnEnter, map[string]any{"id": scratch.ID, "reset_on_enter": false}},
		cmd{variable.SetScope, map[string]any{"id": scratch.ID, "scope": "global"}})
	if v := f.get(t, scratch.ID); v.Scope != "global" || v.BoardID != "" || v.ResetOnEnter {
		t.Errorf("the variable made global is %+v", v)
	}
	f.must(t, cmd{variable.SetScope, map[string]any{"id": g.ID, "scope": "board", "board_id": a}})
	if v := f.get(t, g.ID); v.Scope != "board" || v.BoardID != a {
		t.Errorf("the variable moved to a board is %+v", v)
	}
}

// TestBoardTrash checks that a board's variables are in the trash with their
// board: the project lists none of them while it is there, and nothing
// changes them, deleting them included, a folder that holds only them is
// deleted, a conflict's state of each says that it is in the trash, and each,
// deleted on its own before the board or not, stays in the trash until the
// board is restored, one deleted on its own for its own time. The
// restoration brings the others back, into their folders, which it brings
// back too, and whose undo deletes again. The undo of a variable's creation
// waits for the restoration of its board. The purge of the board deletes them
// with it.
func TestBoardTrash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	a := f.newBoard(t, "Harbour")
	outer, inner := folderID(), folderID()
	f.must(t, cmd{board.CreateFolder, map[string]any{"id": outer, "kind": "variable", "name": "Quests"}},
		cmd{board.CreateFolder, map[string]any{"id": inner, "kind": "variable", "name": "Harbour",
			"parent_id": outer}})
	held := f.create(t, "asked", map[string]any{"scope": "board", "board_id": a, "folder_id": inner})
	alone := f.create(t, "greeted", map[string]any{"scope": "board", "board_id": a})
	f.create(t, "met_elara", nil)

	// A batch that deletes the board and then its variable is refused, since
	// its undo would restore the variable before the board, which is refused.
	_, err := f.apply(t, cmd{board.Delete, map[string]any{"id": a}}, cmd{variable.Delete,
		map[string]any{"id": held.ID}})
	if r := refused(t, err); r.Command != 1 || r.Field != "id" {
		t.Errorf("deleting a board and then its variable was refused by command %d at %q", r.Command, r.Field)
	}
	f.must(t, cmd{variable.Delete, map[string]any{"id": alone.ID}})
	f.must(t, cmd{board.Delete, map[string]any{"id": a}})
	if got := f.list(t); !slices.Equal(got, []string{"met_elara"}) {
		t.Errorf("with the board in the trash, the project lists %v", got)
	}
	if _, err := f.variables.Get(ctx, f.actor(), f.project, decodeID(t, ids.Variable, held.ID)); !errors.Is(err,
		variable.ErrNotFound) {
		t.Errorf("a variable of a board in the trash: %v", err)
	}
	// Nothing changes it, deleting it included, and restoring it, or the one
	// deleted on its own, is refused, since each comes back only with its
	// board. A batch that expects another revision of it is answered with its
	// state, which says that it is in the trash, since its board was deleted.
	for _, c := range []cmd{
		{variable.Rename, map[string]any{"id": held.ID, "key": "asked_again"}},
		{variable.Delete, map[string]any{"id": held.ID}},
		{variable.Restore, map[string]any{"id": held.ID}},
		{variable.Restore, map[string]any{"id": alone.ID}},
	} {
		_, err := f.apply(t, c)
		if r := refused(t, err); r.Field != "id" {
			t.Errorf("%s %v of a variable in the trash with its board was refused at %q", c.typ, c.payload, r.Field)
		}
	}
	var boardDeleted time.Time
	if err := f.d.Admin(t).QueryRow(ctx, "SELECT deleted_at FROM board WHERE id = $1", decodeID(t, ids.Board,
		a)).Scan(&boardDeleted); err != nil {
		t.Fatal(err)
	}
	if got := f.conflictState(t, held.ID, held.Rev+1); got.DeletedAt == nil || !got.DeletedAt.Equal(boardDeleted) {
		t.Errorf("the state of a variable in the trash with its board, deleted at %v, says it was deleted at %v",
			boardDeleted, got.DeletedAt)
	}
	// The folders hold nothing that is not in the trash.
	f.must(t, cmd{board.DeleteFolder, map[string]any{"id": inner}}, cmd{board.DeleteFolder, map[string]any{"id": outer}})
	items, err := f.trash.List(ctx, f.actor(), f.project, trash.Cursor{}, 10)
	if err != nil || len(items) != 2 || items[0].Kind != "board" || items[1].Kind != "variable" ||
		items[1].Name != "greeted" {
		t.Fatalf("the trash lists %+v, %v; want the board, then the variable deleted on its own", items, err)
	}
	// The variable's time in the trash is its own, which ends before its
	// board's, since it was deleted first.
	if !items[1].PurgeAfter.Equal(items[1].DeletedAt.Add(trash.Retention)) ||
		!items[1].PurgeAfter.Before(items[0].PurgeAfter) {
		t.Errorf("the variable deleted at %v before its board, deleted at %v, is purged after %v, want its own time",
			items[1].DeletedAt, items[0].DeletedAt, items[1].PurgeAfter)
	}
	// The state of one deleted on its own says when it was.
	if got := f.conflictState(t, alone.ID, 1); got.DeletedAt == nil || !got.DeletedAt.Equal(items[1].DeletedAt) {
		t.Errorf("the state of a variable deleted on its own at %v says it was deleted at %v", items[1].DeletedAt,
			got.DeletedAt)
	}

	before := f.state(t)
	res := f.must(t, cmd{board.Restore, map[string]any{"id": a}})
	if got := f.list(t); !slices.Equal(got, []string{"asked", "met_elara"}) {
		t.Errorf("with the board restored, the project lists %v", got)
	}
	if v := f.get(t, held.ID); v.FolderID != inner {
		t.Errorf("the restored board's variable is in %q, want %q", v.FolderID, inner)
	}
	folders, err := board.New(f.db, board.Options{Projects: f.projects}).Folders(ctx, f.actor(), f.project, "variable",
		ids.UUID{}, 10)
	if err != nil || len(folders) != 2 {
		t.Errorf("with the board restored, the project lists %d folders of variables, %v; want 2", len(folders), err)
	}
	// The undo deletes the board, and the folders that came back with it.
	f.undo(t, res.Seq)
	if got := f.state(t); got != before {
		t.Errorf("undoing the board's restoration left\n%s\nwant\n%s", got, before)
	}
	f.must(t, cmd{board.Restore, map[string]any{"id": a}})
	// With its board restored, the variable deleted on its own stays in the
	// trash, and its restoration is taken.
	items, err = f.trash.List(ctx, f.actor(), f.project, trash.Cursor{}, 10)
	if err != nil || len(items) != 1 || items[0].Name != "greeted" {
		t.Errorf("with the board restored, the trash lists %+v, %v; want the variable", items, err)
	}
	f.must(t, cmd{variable.Restore, map[string]any{"id": alone.ID}})

	// The undo of a variable's creation waits for its board's restoration, as
	// the undo of its rename does.
	created := f.must(t, newVariable("rumoured", map[string]any{"scope": "board", "board_id": a}))
	f.must(t, cmd{board.Delete, map[string]any{"id": a}})
	_, err = f.pipeline.Apply(ctx, f.actor(), f.project, f.inverse(t, created.Seq))
	if r := refused(t, err); r.Field != "id" {
		t.Errorf("undoing the creation of a variable of a board in the trash was refused at %q", r.Field)
	}
	f.must(t, cmd{board.Restore, map[string]any{"id": a}})
	f.undo(t, created.Seq)
	if got := f.list(t); !slices.Equal(got, []string{"asked", "greeted", "met_elara"}) {
		t.Errorf("with the variable's creation undone, the project lists %v", got)
	}

	// The purge of the board deletes its variables with it, in the trash or
	// not, and then the folders that held them.
	f.must(t, cmd{variable.Delete, map[string]any{"id": alone.ID}}, cmd{board.Delete, map[string]any{"id": a}},
		cmd{board.DeleteFolder, map[string]any{"id": inner}}, cmd{board.DeleteFolder, map[string]any{"id": outer}})
	admin := f.d.Admin(t)
	if _, err := admin.Exec(ctx, `UPDATE board SET deleted_at = now() - interval '31 days' WHERE id = $1;`,
		decodeID(t, ids.Board, a)); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE folder SET deleted_at = now() - interval '31 days' WHERE project_id = $1`,
		f.project); err != nil {
		t.Fatal(err)
	}
	if _, err := trash.Purge(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	var variables, boards, kept int
	if err := admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM variable WHERE board_id IS NOT NULL),
		(SELECT count(*) FROM board), (SELECT count(*) FROM folder) + (SELECT count(*) FROM variable)`).Scan(
		&variables, &boards, &kept); err != nil {
		t.Fatal(err)
	}
	if variables != 0 || boards != 0 || kept != 1 {
		t.Errorf("after the purge, %d variables of boards, %d boards, and %d folders and variables are left, want "+
			"the global variable alone", variables, boards, kept)
	}
}

// TestBoardTrashOrder checks that a variable in the trash with its board is
// a deleted child of its folder, as the variables of its folder are ordered:
// the folder's last variable that is not in the trash, moved into the folder
// without a key, stays where it is, though the board's comes after it, a new
// variable goes after them both, and the board's comes back where it was,
// with its folder, which is not deleted, left as it is.
func TestBoardTrashOrder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.newBoard(t, "Harbour")
	quests := folderID()
	f.must(t, cmd{board.CreateFolder, map[string]any{"id": quests, "kind": "variable", "name": "Quests"}})
	seen := f.create(t, "met_elara", map[string]any{"folder_id": quests})
	hidden := f.create(t, "asked", map[string]any{"scope": "board", "board_id": a, "folder_id": quests})
	f.must(t, cmd{board.Delete, map[string]any{"id": a}})
	if res := f.must(t, cmd{variable.Move, map[string]any{"id": seen.ID, "folder_id": quests}}); len(
		res.Revisions) != 0 {
		t.Errorf("moving the folder's last variable that is not in the trash into it changed %v", res.Revisions)
	}
	added := f.create(t, "harbour_rumours", map[string]any{"folder_id": quests})
	if res := f.must(t, cmd{board.Restore, map[string]any{"id": a}}); len(res.Revisions) != 1 ||
		res.Revisions[0].Kind != ids.Board {
		t.Errorf("restoring the board, whose variable's folder is not deleted, changed %v, want the board alone",
			res.Revisions)
	}
	if h := f.get(t, hidden.ID); f.get(t, seen.ID).Order != seen.Order || seen.Order >= h.Order ||
		h.Order >= added.Order {
		t.Errorf("the variables of the folder are at %q, %q, and %q, want the board's between the others",
			f.get(t, seen.ID).Order, h.Order, added.Order)
	}
}

// decodeID returns the UUID of id, an ID of the kind, or fails the test.
func decodeID(t *testing.T, kind ids.Kind, id string) ids.UUID {
	t.Helper()
	u, err := ids.Decode(kind, id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestFolders checks a variable's place among the folders of variables: it
// goes only into a folder of variables that is not deleted, after the
// folder's last child unless its key says otherwise, a folder that holds a
// variable is deleted only once the variable is, and restoring the variable
// restores the folder.
func TestFolders(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	quests, scenes := folderID(), folderID()
	f.must(t, cmd{board.CreateFolder, map[string]any{"id": quests, "kind": "variable", "name": "Quests"}},
		cmd{board.CreateFolder, map[string]any{"id": scenes, "kind": "board", "name": "Scenes"}})
	for _, folder := range []string{scenes, folderID()} {
		_, err := f.apply(t, newVariable("misplaced", map[string]any{"folder_id": folder}))
		if r := refused(t, err); r.Field != "folder_id" {
			t.Errorf("a variable in a folder that is not one of variables was refused at %q", r.Field)
		}
	}
	first := f.create(t, "met_elara", map[string]any{"folder_id": quests})
	second := f.create(t, "harbour_rumours", map[string]any{"folder_id": quests})
	if first.Order != "V" || second.Order != "W" || second.FolderID != quests {
		t.Errorf("the variables of a new folder are at %q and %q, want V and W", first.Order, second.Order)
	}
	f.must(t, cmd{variable.Move, map[string]any{"id": second.ID, "folder_id": quests, "order": "U"}})
	if v := f.get(t, second.ID); v.Order != "U" {
		t.Errorf("the variable moved before the first is at %q", v.Order)
	}
	// The folder's last child, moved into it without a key, stays where it
	// is.
	if res := f.must(t, cmd{variable.Move, map[string]any{"id": first.ID, "folder_id": quests}}); len(
		res.Revisions) != 0 {
		t.Errorf("moving the folder's last variable into it changed %v", res.Revisions)
	}
	// A folder of variables that the folder holds goes after its variables.
	sub := folderID()
	f.must(t, cmd{board.CreateFolder, map[string]any{"id": sub, "kind": "variable", "name": "Side",
		"parent_id": quests}})
	if f.must(t, cmd{variable.Move, map[string]any{"id": second.ID, "folder_id": quests}}); f.get(t,
		second.ID).Order != "X" {
		t.Errorf("a variable moved to the end of a folder after its subfolder at W is at %q, want X",
			f.get(t, second.ID).Order)
	}
	f.must(t, cmd{variable.Move, map[string]any{"id": first.ID}})
	if v := f.get(t, first.ID); v.FolderID != "" {
		t.Errorf("the variable moved to the top is in %q", v.FolderID)
	}
	f.must(t, cmd{board.DeleteFolder, map[string]any{"id": sub}})
	_, err := f.apply(t, cmd{board.DeleteFolder, map[string]any{"id": quests}})
	if r := refused(t, err); !errors.Is(r.Reason, board.ErrFolderNotEmpty) {
		t.Errorf("deleting a folder that holds a variable was refused for %v", r.Reason)
	}
	f.must(t, cmd{variable.Delete, map[string]any{"id": second.ID}}, cmd{board.DeleteFolder,
		map[string]any{"id": quests}})
	res := f.must(t, cmd{variable.Restore, map[string]any{"id": second.ID}})
	if len(res.Revisions) != 2 {
		t.Errorf("restoring the variable changed %v, want it and its folder", res.Revisions)
	}
	if v := f.get(t, second.ID); v.FolderID != quests {
		t.Errorf("the restored variable is in %q, want %q", v.FolderID, quests)
	}
}

// TestUndo checks the inverse of every command of a variable: applying the
// inverse that the operation log keeps of a change leaves the variables as
// they were before it, and that of a creation deletes what it created, which
// leaves the others as they were.
func TestUndo(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.newBoard(t, "Harbour")
	quests, archive := folderID(), folderID()
	f.must(t, cmd{board.CreateFolder, map[string]any{"id": quests, "kind": "variable", "name": "Quests"}},
		cmd{board.CreateFolder, map[string]any{"id": archive, "kind": "variable", "name": "Archive"}})
	v := f.create(t, "rumours", map[string]any{"type": "int", "default": 2, "description": "What the harbour knows"})
	scratch := f.create(t, "asked", map[string]any{"scope": "board", "board_id": a, "reset_on_enter": true})
	deleted := f.create(t, "gone", map[string]any{"folder_id": archive})
	f.must(t, cmd{variable.Delete, map[string]any{"id": deleted.ID}}, cmd{board.DeleteFolder,
		map[string]any{"id": archive}})
	for _, c := range []cmd{
		newVariable("met_elara", map[string]any{"type": "float", "default": 0.5, "folder_id": quests,
			"scope": "board", "board_id": a}),
		{variable.Rename, map[string]any{"id": v.ID, "key": "harbour_rumours"}},
		{variable.Describe, map[string]any{"id": v.ID, "description": ""}},
		{variable.SetDefault, map[string]any{"id": v.ID, "default": 9}},
		{variable.Retype, map[string]any{"id": v.ID, "type": "string", "default": "many"}},
		{variable.SetScope, map[string]any{"id": v.ID, "scope": "board", "board_id": a}},
		{variable.SetScope, map[string]any{"id": scratch.ID, "scope": "board", "board_id": f.newBoard(t, "Pier")}},
		{variable.SetResetOnEnter, map[string]any{"id": scratch.ID, "reset_on_enter": false}},
		{variable.Move, map[string]any{"id": v.ID, "order": "a"}},
		{variable.Delete, map[string]any{"id": v.ID}},
		{variable.Restore, map[string]any{"id": deleted.ID}},
	} {
		state := f.state
		if c.typ == variable.Create {
			state = f.live
		}
		before := state(t)
		res := f.must(t, c)
		if state(t) == before {
			t.Errorf("%s %v changed nothing", c.typ, c.payload)
		}
		f.undo(t, res.Seq)
		if got := state(t); got != before {
			t.Errorf("undoing %s left\n%s\nwant\n%s", c.typ, got, before)
		}
	}
	// A deletion of what is in the trash already, and a restoration of what
	// is not, change nothing, and are undone by nothing.
	for _, c := range []cmd{
		{variable.Delete, map[string]any{"id": deleted.ID}},
		{variable.Restore, map[string]any{"id": v.ID}},
	} {
		if res := f.must(t, c); len(res.Revisions) != 0 {
			t.Errorf("%s changed %v", c.typ, res.Revisions)
		}
	}
}

// TestRevisionConflict checks that a batch that expects a stale revision of
// a variable is answered with the variable's current state, as the API
// shows it, its default in its canonical form.
func TestRevisionConflict(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := f.create(t, "rumours", map[string]any{"type": "float", "default": 2.0})
	f.must(t, cmd{variable.Describe, map[string]any{"id": v.ID, "description": "What the harbour knows"}})
	u := decodeID(t, ids.Variable, v.ID)
	raw, _ := json.Marshal(map[string]any{"id": v.ID, "key": "harbour_rumours"})
	_, err := f.pipeline.Apply(context.Background(), f.actor(), f.project, []commands.Command{{ID: ids.New(),
		Type: variable.Rename, Payload: raw, ExpectedRev: map[commands.Ref]int64{{Kind: ids.Variable, ID: u}: 1}}})
	var conflict *commands.RevisionConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 {
		t.Fatalf("a stale revision: %v", err)
	}
	var state struct {
		Rev         int64           `json:"rev"`
		Description string          `json:"description"`
		Default     json.RawMessage `json:"default"`
	}
	if err := json.Unmarshal(conflict.Conflicts[0].State, &state); err != nil || state.Rev != 2 ||
		state.Description != "What the harbour knows" || string(state.Default) != "2" {
		t.Errorf("the conflict holds %s, %v", conflict.Conflicts[0].State, err)
	}
}

// TestNotFound checks that the variables of a project are read only by those
// who may read the project, and that a variable in the trash is not found.
func TestNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	v := f.create(t, "met_elara", nil)
	f.must(t, cmd{variable.Delete, map[string]any{"id": v.ID}})
	if _, err := f.variables.Get(ctx, f.actor(), f.project, decodeID(t, ids.Variable, v.ID)); !errors.Is(err,
		variable.ErrNotFound) {
		t.Errorf("a variable in the trash: %v", err)
	}
	stranger := commands.Actor{UserID: ids.New()}
	if _, err := f.variables.List(ctx, stranger, f.project, ids.UUID{}, 10); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("a stranger's list: %v", err)
	}
	if _, err := f.variables.Get(ctx, stranger, f.project, decodeID(t, ids.Variable, v.ID)); !errors.Is(err,
		project.ErrNotFound) {
		t.Errorf("a stranger's read: %v", err)
	}
}

// TestEnumerations checks that the database takes every type and scope of a
// variable that the package lists, which their schemas take too
// (TestSchemaEnums), refuses as a key every reserved word that package ident
// lists, as the commands do (FR-VAR-01 AC1), refuses a default that is not of
// its variable's type (AC2), and holds a description to MaxDescriptionLength
// characters, as variable.describe's payload, so that a word, a type, or a
// limit changed in one place and not in the database's checks fails.
//
// Verifies: FR-VAR-01/AC1, FR-VAR-01/AC2
func TestEnumerations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	a := f.newBoard(t, "Harbour")
	for _, typ := range variable.Types() {
		for _, s := range variable.Scopes() {
			p := map[string]any{"type": typ, "scope": s}
			if s == variable.ScopeBoard {
				p["board_id"] = a
			}
			if v := f.create(t, typ+"_"+s, p); v.Type != typ || v.Scope != s {
				t.Errorf("a %s %s variable is a %s %s", s, typ, v.Scope, v.Type)
			}
		}
	}
	v := decodeID(t, ids.Variable, f.create(t, "rumours", map[string]any{"type": "int"}).ID)
	admin := f.d.Admin(t)
	check := func(sql string, arg any, want bool) {
		t.Helper()
		_, err := admin.Exec(ctx, sql, arg, v)
		var pg *pgconn.PgError
		if refusedByCheck := errors.As(err, &pg) && pg.Code == "23514"; refusedByCheck != !want ||
			want && err != nil {
			t.Errorf("%s with %v: %v, want it taken: %t", sql, arg, err, want)
		}
	}
	for _, w := range ident.ReservedWords() {
		check("UPDATE variable SET key = $1 WHERE id = $2", w, false)
	}
	check("UPDATE variable SET key = $1 WHERE id = $2", "elseif_", true)
	for raw, want := range map[string]bool{
		"3": true, "-9007199254740991": true, "3.0": true, "3.5": false, "9007199254740992": false, `"3"`: false,
		"true": false, "null": false,
	} {
		check("UPDATE variable SET default_value = $1::jsonb WHERE id = $2", raw, want)
	}
	// Characters, not bytes, as the payload's schema counts them.
	for n, want := range map[int]bool{variable.MaxDescriptionLength: true, variable.MaxDescriptionLength + 1: false} {
		check("UPDATE variable SET description = $1 WHERE id = $2", strings.Repeat("é", n), want)
	}
}

// TestVariableIsolation tries the package's queries from another workspace's
// transaction, where row-level security must keep them from reading or
// changing its variables (NFR-SEC-01).
//
// Verifies: NFR-SEC-01
func TestVariableIsolation(t *testing.T) {
	t.Parallel()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	uuid := func(id ids.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
	params := func(target *isolationtest.Workspace) (pgtype.UUID, pgtype.UUID, pgtype.UUID) {
		return uuid(target.ID), uuid(target.Rows["project"]), uuid(target.Rows["variable"])
	}
	one := func(id pgtype.UUID, err error) ([]ids.UUID, error) {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return []ids.UUID{id.Bytes}, err
	}
	for _, op := range []isolationtest.Op{
		{Name: "a variable, in the trash or not", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, id := params(target)
			row, err := queries.New(tx).VariableAny(ctx, queries.VariableAnyParams{WorkspaceID: ws, ProjectID: p, ID: id})
			return one(row.Variable.ID, err)
		}},
		{Name: "a live variable", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			ws, p, id := params(target)
			row, err := queries.New(tx).Variable(ctx, queries.VariableParams{WorkspaceID: ws, ProjectID: p, ID: id})
			return one(row.LiveVariable.ID, err)
		}},
		{Name: "the variable with a key", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			ws, p, _ := params(target)
			key := "met_" + strings.ReplaceAll(target.Rows["variable"].String(), "-", "")
			return one(queries.New(tx).VariableWithKey(ctx, queries.VariableWithKeyParams{WorkspaceID: ws,
				ProjectID: p, Key: key}))
		}},
		{Name: "describe a variable", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			ws, p, id := params(target)
			_, err := queries.New(tx).UpdateVariableDescription(ctx, queries.UpdateVariableDescriptionParams{
				Description: "isolation " + time.Now().String(), WorkspaceID: ws, ProjectID: p, ID: id})
			return one(id, err)
		}},
		{Name: "delete a variable to the trash", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, id := params(target)
			_, err := queries.New(tx).TrashVariable(ctx, queries.TrashVariableParams{WorkspaceID: ws, ProjectID: p,
				ID: id})
			return one(id, err)
		}},
	} {
		f.Check(t, op)
	}
}
