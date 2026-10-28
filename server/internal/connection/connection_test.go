package connection_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/trash"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// fixture is a database of the test's own with a workspace whose owner is
// owner, a project of it, and the pipeline of the projects', the boards', the
// components', the nodes', and the connections' commands.
type fixture struct {
	db          *database.DB
	admin       *pgx.Conn
	ws          ids.UUID
	owner       ids.UUID
	project     ids.UUID
	pipeline    *commands.Pipeline
	connections *connection.Service
	trash       *trash.Service
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
	component.Register(reg)
	node.Register(reg)
	connection.Register(reg)
	expression.Register(reg)
	ent := entitlements.New(entitlements.Options{Meter: usage.Meter{}})
	f.pipeline = commands.New(f.db, commands.Options{Registry: reg, Entitlements: ent})
	projects := project.New(f.db, project.Options{Entitlements: ent, Pipeline: f.pipeline, Seed: component.Seed})
	f.connections = connection.New(f.db, connection.Options{Projects: projects})
	f.trash = trash.New(f.db, trash.Options{Projects: projects})
	p, err := projects.Create(ctx, f.owner, f.ws, project.Settings{Name: "Harbour"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	f.project = decode(t, ids.Project, p.ID)
	return f
}

// exec runs a statement as the superuser.
func (f *fixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.admin.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// count returns the number that the query, run as the superuser, counts.
func (f *fixture) count(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := f.admin.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
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

// payload is a command's payload, as JSON encodes it.
type payload = map[string]any

// batch returns the commands as a batch, each with an ID of its own.
func batch(t *testing.T, cmds ...cmd) []commands.Command {
	t.Helper()
	out := make([]commands.Command, len(cmds))
	for i, c := range cmds {
		raw, err := json.Marshal(c.payload)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = commands.Command{ID: ids.New(), Type: c.typ, Payload: raw}
	}
	return out
}

// apply applies the commands to the fixture's project as one batch, as the
// owner.
func (f *fixture) apply(t *testing.T, cmds ...cmd) (commands.Result, error) {
	t.Helper()
	return f.pipeline.Apply(context.Background(), f.actor(), f.project, batch(t, cmds...))
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

// refused applies the commands, and returns the refusal of the batch,
// failing the test unless a command refused it.
func (f *fixture) refused(t *testing.T, cmds ...cmd) *commands.RefusedError {
	t.Helper()
	_, err := f.apply(t, cmds...)
	var refused *commands.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("%v: %v, want a command's refusal", cmds, err)
	}
	return refused
}

// invalid applies the commands, and returns the fields of the problems that
// the pipeline found, failing the test unless it refused the batch as not
// valid.
func (f *fixture) invalid(t *testing.T, cmds ...cmd) []string {
	t.Helper()
	_, err := f.apply(t, cmds...)
	var invalid *commands.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("%v: %v, want the batch refused as not valid", cmds, err)
	}
	var fields []string
	for _, p := range invalid.Problems {
		fields = append(fields, p.Field)
	}
	return fields
}

// undo applies the inverse of the fixture's project's batch whose seq is seq,
// which the operation log keeps, and returns the undo's result.
func (f *fixture) undo(t *testing.T, seq int64) commands.Result {
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
	if len(steps) == 0 {
		return commands.Result{}
	}
	cmds := make([]commands.Command, len(steps))
	for i, st := range steps {
		cmds[i] = commands.Command{ID: ids.New(), Type: st.Type, Payload: st.Payload}
	}
	res, err := f.pipeline.Apply(context.Background(), f.actor(), f.project, cmds)
	if err != nil {
		t.Fatalf("the undo of %d: %v", seq, err)
	}
	return res
}

// newBoard returns the ID of a new board of the fixture's project.
func (f *fixture) newBoard(t *testing.T, name string) string {
	t.Helper()
	id := ids.Encode(ids.Board, ids.New())
	f.must(t, cmd{board.Create, payload{"id": id, "name": name}})
	return id
}

// newNode returns the ID of a new node of the type typ on the board boardID.
func (f *fixture) newNode(t *testing.T, boardID, typ string) string {
	t.Helper()
	id := ids.Encode(ids.Node, ids.New())
	f.must(t, cmd{node.Create, payload{"id": id, "board_id": boardID, "type": typ,
		"position": payload{"x": 0, "y": 0}}})
	return id
}

// newID returns a new connection's ID.
func newID() string {
	return ids.Encode(ids.Connection, ids.New())
}

// create returns the command that creates the connection id from the node
// from to the node to, with the fields of extra.
func create(id, from, to string, extra payload) cmd {
	p := payload{"id": id, "from_node_id": from, "to_node_id": to}
	for k, v := range extra {
		p[k] = v
	}
	return cmd{connection.Create, p}
}

// connect returns the ID of a new connection from the node from to the node
// to, with the fields of extra.
func (f *fixture) connect(t *testing.T, from, to string, extra payload) string {
	t.Helper()
	id := newID()
	f.must(t, create(id, from, to, extra))
	return id
}

// get returns the connection id of the fixture's project, as the API shows
// it, or fails the test if it is not one that is not in the trash.
func (f *fixture) get(t *testing.T, id string) connection.Connection {
	t.Helper()
	c, err := f.connections.Get(context.Background(), f.actor(), f.project, decode(t, ids.Connection, id))
	if err != nil {
		t.Fatalf("read the connection %s: %v", id, err)
	}
	return c
}

// live reports whether the connection id of the fixture's project is one
// that is not in the trash.
func (f *fixture) live(t *testing.T, id string) bool {
	t.Helper()
	_, err := f.connections.Get(context.Background(), f.actor(), f.project, decode(t, ids.Connection, id))
	switch {
	case errors.Is(err, connection.ErrNotFound):
		return false
	case err != nil:
		t.Fatal(err)
	}
	return true
}

// boardConnections returns the IDs of the connections of the board that are
// not in the trash, in their order.
func (f *fixture) boardConnections(t *testing.T, boardID string) []string {
	t.Helper()
	list, err := f.connections.BoardConnections(context.Background(), f.actor(), f.project,
		decode(t, ids.Board, boardID), ids.UUID{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range list {
		out = append(out, c.ID)
	}
	return out
}

// state returns every connection of the fixture's project, those in the
// trash included, but for their revisions and timestamps, as the superuser
// reads them: what a change and its undo must leave alike.
func (f *fixture) state(t *testing.T) string {
	t.Helper()
	var s string
	if err := f.admin.QueryRow(context.Background(), `SELECT coalesce(string_agg(concat_ws('|', id, board_id,
		from_node_id, to_node_id, "order", label::text, deleted_at IS NOT NULL), ', ' ORDER BY id), '')
		FROM connection WHERE project_id = $1`, f.project).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
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

// label returns a rich-text document of one paragraph of the text.
func label(text string) json.RawMessage {
	raw, _ := json.Marshal(payload{"type": "doc", "content": []any{payload{"type": "paragraph",
		"content": []any{payload{"type": "text", "text": text}}}}})
	return raw
}

// labelOf returns the text of the connection's label as JSON writes it.
func labelOf(t *testing.T, c connection.Connection) string {
	t.Helper()
	raw, err := json.Marshal(c.Label)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestCreate checks that a connection leaves an element for any node of its
// board, the element itself among them, by as many connections as are
// drawn, that each goes after the element's last unless it is given its
// place, that it is refused out of a jumper or a branch, or into a node of
// another board or in the trash, and that its undo deletes it.
func TestCreate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	harbour, pier := f.newBoard(t, "Harbour"), f.newBoard(t, "Pier")
	keeper, elara := f.newNode(t, harbour, node.TypeElement), f.newNode(t, harbour, node.TypeElement)
	jumper, branch := f.newNode(t, harbour, node.TypeJumper), f.newNode(t, harbour, node.TypeBranch)
	gull := f.newNode(t, pier, node.TypeElement)

	ask := newID()
	res := f.must(t, create(ask, keeper, elara, payload{"label": json.RawMessage(`{"type":"doc","content":[` +
		`{"type":"paragraph","content":[{"type":"text","text":"Ask about "},{"type":"text","text":"it"}]}]}`)}))
	if want := []commands.Revision{{Ref: commands.Ref{Kind: ids.Connection, ID: decode(t, ids.Connection, ask)},
		Rev: 1}}; !slices.Equal(res.Revisions, want) {
		t.Errorf("the create changed %v, want %v", res.Revisions, want)
	}
	c := f.get(t, ask)
	if c.FromNodeID != keeper || c.ToNodeID != elara || c.BoardID != harbour || c.FromPort != connection.PortOut ||
		c.Order != "V" || c.Rev != 1 ||
		labelOf(t, c) != `{"content":[{"content":[{"text":"Ask about it","type":"text"}],"type":"paragraph"}],"type":"doc"}` {
		t.Errorf("the connection is %+v, %s", c, labelOf(t, c))
	}
	// A connection to itself, a parallel one, and one into a jumper or a
	// branch are taken; each goes after the node's last but the one given its
	// place, and one without a label has an empty paragraph.
	self := f.connect(t, keeper, keeper, nil)
	parallel := f.connect(t, keeper, elara, nil)
	first := f.connect(t, keeper, jumper, payload{"order": "U"})
	f.connect(t, keeper, branch, nil)
	for id, want := range map[string]string{self: "W", parallel: "X", first: "U"} {
		if got := f.get(t, id).Order; got != want {
			t.Errorf("the connection %s has the order %s, want %s", id, got, want)
		}
	}
	if got := labelOf(t, f.get(t, self)); got != `{"content":[{"type":"paragraph"}],"type":"doc"}` {
		t.Errorf("a connection without a label has %s", got)
	}

	for _, c := range []struct {
		name  string
		cmd   cmd
		field string
	}{
		{"out of a jumper", create(newID(), jumper, keeper, nil), "from_node_id"},
		{"out of a branch by out", create(newID(), branch, keeper, nil), "from_port"},
		{"into a node of another board", create(newID(), keeper, gull, nil), "to_node_id"},
		{"out of a node that the project does not have", create(newID(), ids.Encode(ids.Node, ids.New()), keeper,
			nil), "from_node_id"},
		{"with the ID of a connection that exists", create(ask, keeper, elara, nil), "id"},
	} {
		if r := f.refused(t, c.cmd); r.Field != c.field {
			t.Errorf("a connection %s is refused at %s: %s", c.name, r.Field, r.Message)
		}
	}
	if got := f.invalid(t, create(ids.Encode(ids.Connection, ids.UUID{1}), keeper, elara, nil)); !slices.Equal(got,
		[]string{"payload.id"}) {
		t.Errorf("a connection whose ID is no UUIDv7 is refused at %v", got)
	}

	// A node in the trash, itself or with its board, takes no connection.
	f.must(t, cmd{node.Delete, payload{"id": elara}})
	if r := f.refused(t, create(newID(), keeper, elara, nil)); r.Field != "to_node_id" {
		t.Errorf("a connection into a node in the trash is refused at %s: %s", r.Field, r.Message)
	}
	f.must(t, cmd{board.Delete, payload{"id": pier}})
	if r := f.refused(t, create(newID(), gull, gull, nil)); r.Field != "from_node_id" {
		t.Errorf("a connection out of a node of a board in the trash is refused at %s: %s", r.Field, r.Message)
	}

	// The undo of a create deletes the connection.
	res = f.must(t, create(newID(), keeper, keeper, nil))
	before := f.boardConnections(t, harbour)
	f.undo(t, res.Seq)
	if got := f.boardConnections(t, harbour); len(got) != len(before)-1 {
		t.Errorf("after the undo of a create, the board's connections are %v, want one fewer than %v", got, before)
	}
}

// TestUpdate checks that a connection's label is set in its canonical form,
// that setting the label that it has changes nothing, that the undo sets the
// label back, and that a label that the schema does not take is refused at
// the field.
func TestUpdate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, "Harbour")
	keeper := f.newNode(t, b, node.TypeElement)
	id := f.connect(t, keeper, keeper, payload{"label": label("Say nothing")})
	before := f.state(t)
	res := f.must(t, cmd{connection.Update, payload{"id": id, "label": label("Ask about the lighthouse")}})
	if c := f.get(t, id); c.Rev != 2 || !strings.Contains(labelOf(t, c), "Ask about the lighthouse") {
		t.Errorf("the relabelled connection is %+v", c)
	}
	if again := f.must(t, cmd{connection.Update, payload{"id": id, "label": label("Ask about the lighthouse")}}); len(
		again.Revisions) != 0 {
		t.Errorf("setting the label that it has changed %v", again.Revisions)
	}
	if empty := f.must(t, cmd{connection.Update, payload{"id": id}}); len(empty.Revisions) != 0 {
		t.Errorf("an update that names no field changed %v", empty.Revisions)
	}
	f.undo(t, res.Seq)
	if got := f.state(t); got != before {
		t.Errorf("after the undo, the connections are %s, want %s", got, before)
	}
	got := f.invalid(t, cmd{connection.Update, payload{"id": id, "label": json.RawMessage(`{"type":"doc",` +
		`"content":[{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"underline"}]}]}]}`)}})
	if len(got) == 0 || !strings.HasPrefix(got[0], "payload.label") {
		t.Errorf("a label with a mark that the schema does not take is refused at %v", got)
	}
}

// TestReconnect checks that a connection is reconnected at either end, to
// nodes of its board, that one that leaves another node goes after that
// node's last, and that the undo reconnects it as it was, its place too.
func TestReconnect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	harbour, pier := f.newBoard(t, "Harbour"), f.newBoard(t, "Pier")
	keeper, elara := f.newNode(t, harbour, node.TypeElement), f.newNode(t, harbour, node.TypeElement)
	mira, jumper := f.newNode(t, harbour, node.TypeElement), f.newNode(t, harbour, node.TypeJumper)
	gull := f.newNode(t, pier, node.TypeElement)
	f.connect(t, mira, mira, nil)
	id := f.connect(t, keeper, elara, nil)
	before := f.state(t)

	res := f.must(t, cmd{connection.Reconnect, payload{"id": id, "to_node_id": mira}})
	if c := f.get(t, id); c.FromNodeID != keeper || c.ToNodeID != mira || c.Order != "V" || c.Rev != 2 {
		t.Errorf("the connection reconnected at its end is %+v", c)
	}
	f.undo(t, res.Seq)
	if got := f.state(t); got != before {
		t.Errorf("after the undo, the connections are %s, want %s", got, before)
	}
	res = f.must(t, cmd{connection.Reconnect, payload{"id": id, "from_node_id": mira, "to_node_id": keeper}})
	if c := f.get(t, id); c.FromNodeID != mira || c.ToNodeID != keeper || c.Order != "W" {
		t.Errorf("the connection reconnected at both ends is %+v, want it after the node's last", c)
	}
	f.undo(t, res.Seq)
	if got := f.state(t); got != before {
		t.Errorf("after the undo, the connections are %s, want %s", got, before)
	}
	if same := f.must(t, cmd{connection.Reconnect, payload{"id": id, "from_node_id": keeper,
		"to_node_id": elara}}); len(same.Revisions) != 0 {
		t.Errorf("reconnecting the connection to its own ends changed %v", same.Revisions)
	}
	for _, c := range []struct {
		name  string
		p     payload
		field string
	}{
		{"out of a jumper", payload{"id": id, "from_node_id": jumper}, "from_node_id"},
		{"into a node of another board", payload{"id": id, "to_node_id": gull}, "to_node_id"},
		{"out of a node of another board", payload{"id": id, "from_node_id": gull}, "from_node_id"},
	} {
		if r := f.refused(t, cmd{connection.Reconnect, c.p}); r.Field != c.field {
			t.Errorf("reconnecting a connection %s is refused at %s: %s", c.name, r.Field, r.Message)
		}
	}
}

// TestReorder checks that reordering a connection among the choices of its
// node is one command, which changes its order alone, and that the undo sets
// it back.
//
// Verifies: FR-EDIT-03/AC2
func TestReorder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, "Harbour")
	keeper, elara, mira := f.newNode(t, b, node.TypeElement), f.newNode(t, b, node.TypeElement),
		f.newNode(t, b, node.TypeElement)
	ask := f.connect(t, keeper, elara, payload{"label": label("Ask about the lighthouse")})
	f.connect(t, keeper, mira, payload{"label": label("Say nothing")})
	f.connect(t, elara, mira, nil)
	// columns returns every column of the project's connections but the
	// order, the revision, and the timestamps of the reordered one.
	columns := func() string {
		t.Helper()
		var s string
		if err := f.admin.QueryRow(context.Background(), `SELECT string_agg(concat_ws('|', id, board_id,
			from_node_id, to_node_id, label::text, deleted_at, created_by, created_at,
			CASE WHEN id <> $2 THEN concat_ws('|', "order", rev, updated_at, updated_by) END), ', ' ORDER BY id)
			FROM connection WHERE project_id = $1`, f.project, decode(t, ids.Connection, ask)).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	ops := func() int64 {
		return f.count(t, "SELECT count(*) FROM operation WHERE project_id = $1", f.project)
	}
	before, beforeOps, beforeState := columns(), ops(), f.state(t)

	// Moving the first choice after the second.
	res := f.must(t, cmd{connection.Reorder, payload{"id": ask, "order": "X"}})
	if want := []commands.Revision{{Ref: commands.Ref{Kind: ids.Connection, ID: decode(t, ids.Connection, ask)},
		Rev: 2}}; !slices.Equal(res.Revisions, want) {
		t.Errorf("the reorder changed %v, want %v", res.Revisions, want)
	}
	if got := ops() - beforeOps; got != 1 {
		t.Errorf("the reorder logged %d operations, want 1", got)
	}
	var typ string
	if err := f.admin.QueryRow(context.Background(), "SELECT type FROM operation WHERE project_id = $1 AND seq = $2",
		f.project, res.Seq).Scan(&typ); err != nil || typ != string(connection.Reorder) {
		t.Errorf("the reorder's operation is %q, %v", typ, err)
	}
	if got := columns(); got != before {
		t.Errorf("the reorder changed more than the order: %s, want %s", got, before)
	}
	if c := f.get(t, ask); c.Order != "X" {
		t.Errorf("the reordered connection has the order %s", c.Order)
	}
	f.undo(t, res.Seq)
	if got := f.state(t); got != beforeState {
		t.Errorf("after the undo, the connections are %s, want %s", got, beforeState)
	}
	// Without a place, a choice goes after the node's last, and the last
	// stays where it is.
	f.must(t, cmd{connection.Reorder, payload{"id": ask}})
	if c := f.get(t, ask); c.Order != "X" {
		t.Errorf("the choice reordered to the end has the order %s, want X, after W", c.Order)
	}
	if last := f.must(t, cmd{connection.Reorder, payload{"id": ask}}); len(last.Revisions) != 0 {
		t.Errorf("reordering the last choice to the end changed %v", last.Revisions)
	}
}

// TestTrash checks that a connection deleted on its own goes to the trash,
// unlisted, from which its undo restores it, that a node's connections are
// in the trash with it, where nothing changes them, and that the purge
// deletes a connection whose time has passed, and a node's with it.
func TestTrash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	b := f.newBoard(t, "Harbour")
	keeper, elara := f.newNode(t, b, node.TypeElement), f.newNode(t, b, node.TypeElement)
	ask := f.connect(t, keeper, elara, nil)
	silent := f.connect(t, keeper, keeper, nil)

	res := f.must(t, cmd{connection.Delete, payload{"id": ask}})
	if f.live(t, ask) {
		t.Error("a deleted connection is read")
	}
	items, err := f.trash.List(ctx, f.actor(), f.project, trash.Cursor{}, 100)
	if err != nil || len(items) != 0 {
		t.Errorf("the trash lists %v, %v, want no connection", items, err)
	}
	if again := f.must(t, cmd{connection.Delete, payload{"id": ask}}); len(again.Revisions) != 0 {
		t.Errorf("deleting a deleted connection changed %v", again.Revisions)
	}
	for _, c := range []cmd{{connection.Update, payload{"id": ask, "label": label("x")}},
		{connection.Reorder, payload{"id": ask, "order": "Z"}}, {connection.Reconnect, payload{"id": ask,
			"to_node_id": keeper}}} {
		f.refused(t, c)
	}
	f.undo(t, res.Seq)
	if !f.live(t, ask) {
		t.Error("the undo of a connection's deletion does not restore it")
	}

	// A node's connections are in the trash with it, and nothing changes
	// them there, not even a deletion.
	f.must(t, cmd{node.Delete, payload{"id": elara}})
	if f.live(t, ask) || !f.live(t, silent) {
		t.Error("the node's deletion did not take its connection alone to the trash with it")
	}
	for _, c := range []cmd{{connection.Delete, payload{"id": ask}}, {connection.Restore, payload{"id": ask}},
		{connection.Update, payload{"id": ask, "label": label("x")}}} {
		if r := f.refused(t, c); r.Field != "id" {
			t.Errorf("%s of a connection of a node in the trash is refused at %s: %s", c.typ, r.Field, r.Message)
		}
	}
	// One deleted on its own, before its node, stays deleted when the node
	// is restored, until it is restored itself.
	f.must(t, cmd{node.Restore, payload{"id": elara}})
	f.must(t, cmd{connection.Delete, payload{"id": ask}})
	f.must(t, cmd{node.Delete, payload{"id": elara}})
	f.must(t, cmd{node.Restore, payload{"id": elara}})
	if f.live(t, ask) {
		t.Error("restoring the node brought back a connection deleted on its own")
	}
	f.must(t, cmd{connection.Restore, payload{"id": ask}})
	if !f.live(t, ask) {
		t.Error("the connection's restoration did not bring it back")
	}
	// A board's connections are in the trash with it.
	f.must(t, cmd{board.Delete, payload{"id": b}})
	if _, err := f.connections.BoardConnections(ctx, f.actor(), f.project, decode(t, ids.Board, b), ids.UUID{},
		10); !errors.Is(err, board.ErrNotFound) {
		t.Errorf("the connections of a board in the trash read %v", err)
	}
	if f.live(t, silent) {
		t.Error("a connection of a board in the trash is read")
	}
	f.must(t, cmd{board.Restore, payload{"id": b}})
	if !f.live(t, silent) || !f.live(t, ask) {
		t.Error("the board's restoration did not bring back its connections")
	}

	// The purge deletes a connection whose time in the trash has passed,
	// and a purged node's connections with it.
	f.must(t, cmd{connection.Delete, payload{"id": ask}})
	f.exec(t, "UPDATE connection SET deleted_at = now() - interval '31 days' WHERE id = $1",
		decode(t, ids.Connection, ask))
	f.must(t, cmd{node.Delete, payload{"id": keeper}})
	f.exec(t, "UPDATE node SET deleted_at = now() - interval '31 days' WHERE id = $1", decode(t, ids.Node, keeper))
	if _, err := trash.Purge(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, "SELECT count(*) FROM connection WHERE project_id = $1", f.project); n != 0 {
		t.Errorf("the purge left %d connections", n)
	}
	if r := f.refused(t, cmd{connection.Restore, payload{"id": ask}}); r.Field != "id" {
		t.Errorf("the restore of a purged connection is refused at %s: %s", r.Field, r.Message)
	}
}

// TestNodeDeletion checks that deleting a node takes its connections to the
// trash with it, in the batch that deletes it, and none of the board's
// others, and that the undo of the deletion restores the node and its
// connections together.
//
// Verifies: FR-EDIT-02/AC3
func TestNodeDeletion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, "Harbour")
	keeper, elara, mira := f.newNode(t, b, node.TypeElement), f.newNode(t, b, node.TypeElement),
		f.newNode(t, b, node.TypeElement)
	out, in := f.connect(t, keeper, elara, nil), f.connect(t, elara, keeper, nil)
	self, other := f.connect(t, keeper, keeper, nil), f.connect(t, elara, mira, nil)
	before := f.state(t)

	res := f.must(t, cmd{node.Delete, payload{"id": keeper}})
	if got := f.boardConnections(t, b); !slices.Equal(got, []string{other}) {
		t.Errorf("once the node is deleted, the board's connections are %v, want %v", got, []string{other})
	}
	for _, id := range []string{out, in, self} {
		if f.live(t, id) {
			t.Errorf("the connection %s of the deleted node is read", id)
		}
	}
	undo := f.undo(t, res.Seq)
	if len(undo.NotReconnected) != 0 {
		t.Errorf("the undo left %v unreconnected", undo.NotReconnected)
	}
	if got := f.boardConnections(t, b); len(got) != 4 {
		t.Errorf("after the undo, the board's connections are %v, want all four", got)
	}
	if got := f.state(t); got != before {
		t.Errorf("after the undo, the connections are %s, want %s", got, before)
	}
}

// TestReconnection checks that restoring a node brings back its connections
// whose other node is not in the trash, and that its batch's answer lists
// the others, each with the node that it waits for, once every command of the
// batch has applied, so that a batch that restores both of a connection's
// nodes lists nothing, that the answer is the same when the batch is sent
// again, and that a connection comes back once the node that it waits for is
// restored.
//
// Verifies: FR-PROJ-06/AC1
func TestReconnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	b := f.newBoard(t, "Harbour")
	keeper, elara, mira := f.newNode(t, b, node.TypeElement), f.newNode(t, b, node.TypeElement),
		f.newNode(t, b, node.TypeElement)
	toElara, fromMira := f.connect(t, keeper, elara, nil), f.connect(t, mira, keeper, nil)
	self, deleted := f.connect(t, keeper, keeper, nil), f.connect(t, keeper, mira, nil)
	f.must(t, cmd{connection.Delete, payload{"id": deleted}})
	f.must(t, cmd{node.Delete, payload{"id": mira}}, cmd{node.Delete, payload{"id": keeper}})

	// The restoration is a client's batch, as Parse reads it, whose answer
	// its idempotency record keeps.
	wire, err := json.Marshal(payload{"command_id": ids.New().String(), "type": node.Restore,
		"payload": payload{"id": keeper}})
	if err != nil {
		t.Fatal(err)
	}
	restore, err := f.pipeline.Parse(f.project, []json.RawMessage{wire}, 0)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.pipeline.Apply(ctx, f.actor(), f.project, restore)
	if err != nil {
		t.Fatal(err)
	}
	ref := func(kind ids.Kind, id string) commands.Ref { return commands.Ref{Kind: kind, ID: decode(t, kind, id)} }
	want := []commands.Unreconnected{{Ref: ref(ids.Connection, fromMira),
		WaitingFor: []commands.Ref{ref(ids.Node, mira)}}}
	if !reflect.DeepEqual(res.NotReconnected, want) {
		t.Errorf("the restoration leaves %v unreconnected, want %v", res.NotReconnected, want)
	}
	if !f.live(t, toElara) || !f.live(t, self) || f.live(t, fromMira) || f.live(t, deleted) {
		t.Error("the restoration brought back other connections than those whose nodes are not in the trash")
	}
	if res := f.must(t, cmd{node.Restore, payload{"id": mira}}); len(res.NotReconnected) != 0 {
		t.Errorf("the other node's restoration leaves %v unreconnected", res.NotReconnected)
	}
	if !f.live(t, fromMira) || f.live(t, deleted) {
		t.Error("the other node's restoration did not bring back its connection alone")
	}
	// The batch sent again is answered as it was, even once the connection
	// no longer waits.
	if again, err := f.pipeline.Apply(ctx, f.actor(), f.project, restore); err != nil ||
		!reflect.DeepEqual(again.NotReconnected, want) {
		t.Errorf("the batch sent again leaves %v unreconnected, %v, want %v", again.NotReconnected, err, want)
	}

	// A batch that restores both of a connection's nodes leaves nothing, nor
	// does one that restores a node and deletes it again.
	f.must(t, cmd{node.Delete, payload{"id": mira}}, cmd{node.Delete, payload{"id": keeper}})
	if res := f.must(t, cmd{node.Restore, payload{"id": keeper}}, cmd{node.Restore, payload{"id": mira}}); len(
		res.NotReconnected) != 0 {
		t.Errorf("restoring both nodes leaves %v unreconnected", res.NotReconnected)
	}
	f.must(t, cmd{node.Delete, payload{"id": mira}}, cmd{node.Delete, payload{"id": keeper}})
	if res := f.must(t, cmd{node.Restore, payload{"id": keeper}}, cmd{node.Delete, payload{"id": keeper}}); len(
		res.NotReconnected) != 0 {
		t.Errorf("restoring a node and deleting it again leaves %v unreconnected", res.NotReconnected)
	}
}

// TestBoardReconnection checks that restoring a board brings back the
// connections of the nodes that come back with it whose other node is not
// deleted on its own, and that its batch's answer lists the others, each with
// the node that it waits for, but not one neither of whose nodes comes back,
// and each once, when the batch restores one of their nodes too.
//
// Verifies: FR-PROJ-06/AC1
func TestBoardReconnection(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, "Harbour")
	keeper, elara, mira, gull := f.newNode(t, b, node.TypeElement), f.newNode(t, b, node.TypeElement),
		f.newNode(t, b, node.TypeElement), f.newNode(t, b, node.TypeElement)
	toElara, toMira := f.connect(t, keeper, elara, nil), f.connect(t, keeper, mira, nil)
	fromMira, between := f.connect(t, mira, gull, nil), f.connect(t, elara, mira, nil)
	f.must(t, cmd{node.Delete, payload{"id": elara}}, cmd{node.Delete, payload{"id": mira}})
	f.must(t, cmd{board.Delete, payload{"id": b}})

	waiting := func(pairs ...string) []commands.Unreconnected {
		var out []commands.Unreconnected
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, commands.Unreconnected{
				Ref:        commands.Ref{Kind: ids.Connection, ID: decode(t, ids.Connection, pairs[i])},
				WaitingFor: []commands.Ref{{Kind: ids.Node, ID: decode(t, ids.Node, pairs[i+1])}}})
		}
		slices.SortFunc(out, func(a, b commands.Unreconnected) int { return bytes.Compare(a.ID[:], b.ID[:]) })
		return out
	}
	res := f.must(t, cmd{board.Restore, payload{"id": b}})
	if want := waiting(toElara, elara, toMira, mira, fromMira, mira); !reflect.DeepEqual(res.NotReconnected,
		want) {
		t.Errorf("the board's restoration leaves %v unreconnected, want %v", res.NotReconnected, want)
	}
	if f.live(t, toElara) || f.live(t, toMira) || f.live(t, fromMira) || f.live(t, between) {
		t.Error("the board's restoration brought back a connection of a node deleted on its own")
	}

	// A batch that restores the board and one of the nodes lists a
	// connection that both leave once.
	f.must(t, cmd{board.Delete, payload{"id": b}})
	res = f.must(t, cmd{board.Restore, payload{"id": b}}, cmd{node.Restore, payload{"id": mira}})
	if want := waiting(toElara, elara, between, elara); !reflect.DeepEqual(res.NotReconnected, want) {
		t.Errorf("restoring the board and a node leaves %v unreconnected, want %v", res.NotReconnected, want)
	}
	if !f.live(t, toMira) || !f.live(t, fromMira) || f.live(t, toElara) || f.live(t, between) {
		t.Error("restoring the board and a node did not bring back the connections whose nodes are back alone")
	}
}

// TestStates checks that a batch that expects a stale revision of a
// connection is answered with its current state, its label among it, and
// that of a connection of a node in the trash with the node's deletion.
func TestStates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, "Harbour")
	keeper := f.newNode(t, b, node.TypeElement)
	id := f.connect(t, keeper, keeper, payload{"label": label("Say nothing")})
	f.must(t, cmd{node.Delete, payload{"id": keeper}})
	raw, _ := json.Marshal(payload{"id": id})
	_, err := f.pipeline.Apply(context.Background(), f.actor(), f.project, []commands.Command{{ID: ids.New(),
		Type: connection.Delete, Payload: raw, ExpectedRev: map[commands.Ref]int64{{Kind: ids.Connection,
			ID: decode(t, ids.Connection, id)}: 7}}})
	var conflict *commands.RevisionConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 {
		t.Fatalf("a stale revision of a connection is answered with %v", err)
	}
	var state connection.Connection
	if err := json.Unmarshal(conflict.Conflicts[0].State, &state); err != nil || state.DeletedAt == nil ||
		!strings.Contains(labelOf(t, state), "Say nothing") {
		t.Errorf("the conflict shows the connection of a node in the trash as %s (%v)", conflict.Conflicts[0].State,
			err)
	}
}

// TestIsolation tries the connections' queries from another workspace's
// transaction, where row-level security must keep them from reading or
// changing its rows (NFR-SEC-01).
//
// Verifies: NFR-SEC-01
func TestIsolation(t *testing.T) {
	t.Parallel()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	uuid := func(id ids.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
	for _, op := range []isolationtest.Op{
		{Name: "a connection, whatever its state", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			row, err := queries.New(tx).ConnectionAny(ctx, queries.ConnectionAnyParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), ID: uuid(target.Rows["connection"])})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return []ids.UUID{row.Connection.ID.Bytes}, err
		}},
		{Name: "a connection's revision", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			rows, err := queries.New(tx).ConnectionRevs(ctx, queries.ConnectionRevsParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), Ids: []pgtype.UUID{uuid(target.Rows["connection"])}})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.ID.Bytes)
			}
			return out, err
		}},
		{Name: "relabel a connection", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			_, err := queries.New(tx).UpdateConnectionLabel(ctx, queries.UpdateConnectionLabelParams{
				Label: label("x"), WorkspaceID: uuid(target.ID), ProjectID: uuid(target.Rows["project"]),
				ID: uuid(target.Rows["connection"])})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return []ids.UUID{target.Rows["connection"]}, err
		}},
	} {
		f.Check(t, op)
	}
}
