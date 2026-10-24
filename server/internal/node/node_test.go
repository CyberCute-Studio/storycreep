package node_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
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
// owner, a project of it, created with the default component types, and the
// pipeline of the projects', the boards', the components', and the nodes'
// commands.
type fixture struct {
	d        *dbtest.Database
	db       *database.DB
	admin    *pgx.Conn
	ws       ids.UUID
	owner    ids.UUID
	project  ids.UUID
	pipeline *commands.Pipeline
	projects *project.Service
	nodes    *node.Service
	trash    *trash.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	f := &fixture{d: d, db: d.Open(t), admin: d.Admin(t), ws: ids.New()}
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
	ent := entitlements.New(entitlements.Options{Meter: usage.Meter{}})
	f.pipeline = commands.New(f.db, commands.Options{Registry: reg, Entitlements: ent})
	f.projects = project.New(f.db, project.Options{Entitlements: ent, Pipeline: f.pipeline, Seed: component.Seed})
	f.nodes = node.New(f.db, node.Options{Projects: f.projects})
	f.trash = trash.New(f.db, trash.Options{Projects: f.projects})
	f.project = f.newProject(t, "Harbour")
	return f
}

// newProject creates a project of the fixture's workspace named name, and
// returns its UUID.
func (f *fixture) newProject(t *testing.T, name string) ids.UUID {
	t.Helper()
	p, err := f.projects.Create(context.Background(), f.owner, f.ws, project.Settings{Name: name}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := ids.Decode(ids.Project, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
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

// apply applies the commands to the project as one batch, as the owner.
func (f *fixture) apply(t *testing.T, projectID ids.UUID, cmds ...cmd) (commands.Result, error) {
	t.Helper()
	batch := make([]commands.Command, len(cmds))
	for i, c := range cmds {
		raw, err := json.Marshal(c.payload)
		if err != nil {
			t.Fatal(err)
		}
		batch[i] = commands.Command{ID: ids.New(), Type: c.typ, Payload: raw}
	}
	return f.pipeline.Apply(context.Background(), f.actor(), projectID, batch)
}

// must applies the commands to the fixture's project as apply does, and fails
// the test unless they apply.
func (f *fixture) must(t *testing.T, cmds ...cmd) commands.Result {
	t.Helper()
	res, err := f.apply(t, f.project, cmds...)
	if err != nil {
		t.Fatalf("%v: %v", cmds, err)
	}
	return res
}

// refused applies the commands to the fixture's project, and returns the
// refusal of the batch, failing the test unless a command refused it.
func (f *fixture) refused(t *testing.T, cmds ...cmd) *commands.RefusedError {
	t.Helper()
	_, err := f.apply(t, f.project, cmds...)
	var refused *commands.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("%v: %v, want a command's refusal", cmds, err)
	}
	return refused
}

// invalid applies the commands to the fixture's project, and returns the
// fields of the problems that the pipeline found, failing the test unless it
// refused the batch as not valid.
func (f *fixture) invalid(t *testing.T, cmds ...cmd) []string {
	t.Helper()
	_, err := f.apply(t, f.project, cmds...)
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
// which the operation log keeps, and returns the error of the undo's batch.
func (f *fixture) undo(t *testing.T, seq int64) error {
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
		return nil
	}
	batch := make([]commands.Command, len(steps))
	for i, st := range steps {
		batch[i] = commands.Command{ID: ids.New(), Type: st.Type, Payload: st.Payload}
	}
	_, err := f.pipeline.Apply(context.Background(), f.actor(), f.project, batch)
	return err
}

// newBoard returns the ID of a new board of the project projectID named name.
func (f *fixture) newBoard(t *testing.T, projectID ids.UUID, name string) string {
	t.Helper()
	id := ids.Encode(ids.Board, ids.New())
	if _, err := f.apply(t, projectID, cmd{board.Create, payload{"id": id, "name": name}}); err != nil {
		t.Fatal(err)
	}
	return id
}

// create returns the command that creates the node id of the type typ on the
// board boardID, at x, 0, with the fields of extra.
func create(id, boardID, typ string, x int, extra payload) cmd {
	p := payload{"id": id, "board_id": boardID, "type": typ, "position": payload{"x": x, "y": 0}}
	for k, v := range extra {
		p[k] = v
	}
	return cmd{node.Create, p}
}

// newNode returns the ID of a new node of the fixture's project, of the type
// typ on the board boardID, with the fields of extra.
func (f *fixture) newNode(t *testing.T, boardID, typ string, extra payload) string {
	t.Helper()
	id := newID()
	f.must(t, create(id, boardID, typ, 0, extra))
	return id
}

// newID returns a new node's ID.
func newID() string {
	return ids.Encode(ids.Node, ids.New())
}

// get returns the node id of the fixture's project, as the API shows it.
func (f *fixture) get(t *testing.T, id string) node.Node {
	t.Helper()
	n, err := f.nodes.Get(context.Background(), f.actor(), f.project, decode(t, ids.Node, id))
	if err != nil {
		t.Fatalf("read the node %s: %v", id, err)
	}
	return n
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

// component returns the ID of a new component of the fixture's project, a
// character.
func (f *fixture) component(t *testing.T, name string) string {
	t.Helper()
	var typeID ids.UUID
	if err := f.admin.QueryRow(context.Background(), `SELECT id FROM component_type WHERE project_id = $1
		AND key = 'character'`, f.project).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	id := ids.Encode(ids.Component, ids.New())
	f.must(t, cmd{component.Create, payload{"id": id, "component_type_id": ids.Encode(ids.ComponentType, typeID),
		"name": name}})
	return id
}

// asset returns the ID of a new asset of the fixture's project, which the
// superuser adds.
func (f *fixture) asset(t *testing.T) string {
	t.Helper()
	id := ids.New()
	sum := sha256.Sum256(id[:])
	f.exec(t, "INSERT INTO asset (id, workspace_id, project_id, sha256, mime, bytes, storage_key) "+
		"VALUES ($1, $2, $3, $4, 'image/png', 1, $5)", id, f.ws, f.project, sum[:], f.ws.String()+"/"+id.String())
	return ids.Encode(ids.Asset, id)
}

// state returns every node of the fixture's project, those in the trash
// included, but for their revisions and timestamps, as the superuser reads
// them: what a change and its undo must leave alike.
func (f *fixture) state(t *testing.T) string {
	t.Helper()
	var s string
	if err := f.admin.QueryRow(context.Background(), `SELECT coalesce(string_agg(concat_ws('|', id, board_id, type,
		kind, title, text::text, speaker_component_id, cover_asset_id, color, tags::text, x, y, width, height, is_ending,
		target_board_id, target_node_id, deleted_at IS NOT NULL), ', ' ORDER BY id), '') FROM node
		WHERE project_id = $1`, f.project).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// used returns the workspace's usage of key, its counter and its pending
// deltas.
func (f *fixture) used(t *testing.T, key usage.Key) int64 {
	t.Helper()
	var n int64
	if err := f.db.InTx(context.Background(), database.Scope{WorkspaceID: f.ws}, func(tx pgx.Tx) error {
		used, err := usage.Read(context.Background(), tx, f.ws)
		n = used[key]
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// liveNodes returns how many nodes of the workspace are not in a trash, as
// its usage of nodes counts them.
func (f *fixture) liveNodes(t *testing.T) int64 {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM live_node n JOIN project p ON p.id = n.project_id
		WHERE n.workspace_id = $1 AND p.deleted_at IS NULL`, f.ws)
}

// operations returns how many operations the fixture's project's log holds,
// and how many commands they applied.
func (f *fixture) operations(t *testing.T) (ops, applied int64) {
	t.Helper()
	return f.count(t, "SELECT count(*) FROM operation WHERE project_id = $1", f.project),
		f.count(t, "SELECT count(*) FROM applied_command WHERE project_id = $1", f.project)
}

// TestCreate checks that creating a node is one command and one entry of the
// operation log, that it holds what the command gives it, an element's
// defaults among them, that it counts towards the workspace's nodes, and
// that a command that gives a field to a type that has none, or refers to
// what the project does not have, is refused.
//
// Verifies: FR-EDIT-02/AC1
func TestCreate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, f.project, "Harbour")
	elara := f.component(t, "Elara")
	cover := f.asset(t)
	opsBefore, appliedBefore := f.operations(t)
	id := newID()
	res := f.must(t, create(id, b, node.TypeElement, 0, payload{"kind": "narration", "title": "Harbourmaster greets you",
		"text": json.RawMessage(`{"type": "doc", "content": [{"type": "paragraph", "content": [{"type": "text",
			"text": "Harbour's closed."}]}]}`), "speaker_component_id": elara, "cover_asset_id": cover, "color": "teal",
		"tags": []string{"Quest", "Harbour"}, "position": payload{"x": 240.5, "y": -120}, "size": payload{"width": 280,
			"height": 96.25}, "is_ending": true}))
	ops, applied := f.operations(t)
	if ops != opsBefore+1 || applied != appliedBefore+1 {
		t.Errorf("creating a node wrote %d operations and %d commands, want one of each", ops-opsBefore,
			applied-appliedBefore)
	}
	if want := []commands.Revision{{Ref: commands.Ref{Kind: ids.Node, ID: decode(t, ids.Node, id)}, Rev: 1}}; !slices.Equal(
		res.Revisions, want) {
		t.Errorf("the batch changed %v, want %v", res.Revisions, want)
	}
	if typ := f.count(t, "SELECT count(*) FROM operation WHERE project_id = $1 AND seq = $2 AND type = 'node.create'",
		f.project, res.Seq); typ != 1 {
		t.Errorf("the operation of the creation is not of the type node.create")
	}
	n := f.get(t, id)
	raw, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"element"`, `"kind":"narration"`, `"title":"Harbourmaster greets you"`,
		`"text":{"content":[{"content":[{"text":"Harbour's closed.","type":"text"}],"type":"paragraph"}],"type":"doc"}`,
		`"speaker_component_id":"` + elara, `"cover_asset_id":"` + cover, `"color":"teal"`, `"tags":["Quest","Harbour"]`,
		`"position":{"x":240.5,"y":-120}`, `"size":{"width":280,"height":96.25}`, `"is_ending":true`, `"rev":1`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the node is %s, without %s", raw, want)
		}
	}

	// An element without a kind or a text is a line of dialogue with an empty
	// paragraph; a branch and a jumper have neither.
	plain := f.newNode(t, b, node.TypeElement, nil)
	if n := f.get(t, plain); n.Kind != node.KindDialogue || n.Text == nil || n.Size != nil || n.Color != "none" ||
		len(n.Tags) != 0 || n.Title != "" {
		t.Errorf("a plain element is %+v", n)
	}
	branch := f.newNode(t, b, node.TypeBranch, payload{"title": "Does he trust the keeper?"})
	if n := f.get(t, branch); n.Kind != "" || n.Text != nil || n.Target != nil {
		t.Errorf("a branch is %+v", n)
	}
	jumper := f.newNode(t, b, node.TypeJumper, payload{"target": payload{"board_id": b, "node_id": id}})
	if n := f.get(t, jumper); n.Target == nil || n.Target.NodeID != id || n.Target.BoardID != b {
		t.Errorf("a jumper is %+v", n)
	}
	if got, want := f.used(t, usage.Nodes), int64(4); got != want {
		t.Errorf("the workspace uses %d nodes, want %d", got, want)
	}

	// The fields that a type has none of are refused as the batch is checked.
	for _, c := range []struct {
		typ   string
		extra payload
		field string
	}{
		{node.TypeBranch, payload{"kind": "dialogue"}, "payload.kind"},
		{node.TypeJumper, payload{"text": json.RawMessage(`{"type": "doc", "content": [{"type": "paragraph"}]}`)}, "payload.text"},
		{node.TypeBranch, payload{"speaker_component_id": elara}, "payload.speaker_component_id"},
		{node.TypeJumper, payload{"cover_asset_id": cover}, "payload.cover_asset_id"},
		{node.TypeBranch, payload{"is_ending": true}, "payload.is_ending"},
		{node.TypeElement, payload{"target": payload{"board_id": b, "node_id": id}}, "payload.target"},
		{node.TypeJumper, payload{"target": payload{"node_id": id}}, "payload.target.board_id"},
		{node.TypeElement, payload{"size": payload{"width": 10}}, "payload.size.height"},
		{node.TypeElement, payload{"title": " Harbour"}, "payload.title"},
		{node.TypeElement, payload{"tags": []string{"Quest", "QUEST"}}, "payload.tags[1]"},
		{node.TypeElement, payload{"position": payload{"x": 1e10, "y": 0}}, "payload.position.x"},
	} {
		if got := f.invalid(t, create(newID(), b, c.typ, 0, c.extra)); !slices.Contains(got, c.field) {
			t.Errorf("a %s with %v is refused at %v, want %s", c.typ, c.extra, got, c.field)
		}
	}

	// What the project does not have is refused as the batch applies.
	other := f.newBoard(t, f.project, "Pier")
	f.must(t, cmd{board.Delete, payload{"id": other}})
	for _, c := range []struct {
		c     cmd
		field string
	}{
		{create(newID(), other, node.TypeElement, 0, nil), "board_id"},
		{create(newID(), ids.Encode(ids.Board, ids.New()), node.TypeElement, 0, nil), "board_id"},
		{create(newID(), b, node.TypeElement, 0, payload{"speaker_component_id": ids.Encode(ids.Component,
			ids.New())}), "speaker_component_id"},
		{create(newID(), b, node.TypeElement, 0, payload{"cover_asset_id": ids.Encode(ids.Asset, ids.New())}),
			"cover_asset_id"},
		{create(newID(), b, node.TypeJumper, 0, payload{"target": payload{"board_id": other, "node_id": id}}),
			"target.node_id"},
		{create(newID(), b, node.TypeJumper, 0, payload{"target": payload{"board_id": b,
			"node_id": ids.Encode(ids.Node, ids.New())}}), "target.node_id"},
		{create(id, b, node.TypeElement, 0, nil), "id"},
	} {
		if r := f.refused(t, c.c); r.Field != c.field {
			t.Errorf("%v is refused at %s (%s), want %s", c.c.payload, r.Field, r.Message, c.field)
		}
	}
	// A speaker in the trash is taken, as the undo of a change of it sets it
	// back; the validation reports it.
	mira := f.component(t, "Mira")
	f.must(t, cmd{component.Delete, payload{"id": mira}})
	f.must(t, create(newID(), b, node.TypeElement, 0, payload{"speaker_component_id": mira}))
}

// TestUpdate checks that node.update changes the fields that it names and
// leaves the others, that its undo sets back exactly those, that a field that
// the node's type has none of is refused, and that a reference that it keeps
// is not checked again, though what it names no longer exists.
func TestUpdate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, f.project, "Harbour")
	elara, mira := f.component(t, "Elara"), f.component(t, "Mira")
	id := f.newNode(t, b, node.TypeElement, payload{"title": "Greeting", "speaker_component_id": elara,
		"tags": []string{"Quest"}, "color": "red"})
	before := f.state(t)
	res := f.must(t, cmd{node.Update, payload{"id": id, "title": "Harbourmaster greets you",
		"text": json.RawMessage(`{"type": "doc", "content": [{"type": "paragraph", "content": [{"type": "text",
			"text": "State your business."}]}]}`), "speaker_component_id": mira, "tags": []string{"Harbour", "quest"},
		"size": payload{"width": 300, "height": 120}, "kind": "narration"}})
	n := f.get(t, id)
	if n.Title != "Harbourmaster greets you" || n.SpeakerComponentID != mira || n.Color != "red" ||
		!slices.Equal(n.Tags, []string{"Harbour", "quest"}) || n.Size == nil || n.Kind != node.KindNarration ||
		n.Rev != 2 {
		t.Errorf("the updated node is %+v", n)
	}
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatal(err)
	}
	if got := f.state(t); got != before {
		t.Errorf("after the undo of the update, the nodes are\n%s\nwant\n%s", got, before)
	}

	// A size of {} is none, and a target of {} too.
	f.must(t, cmd{node.Update, payload{"id": id, "size": payload{"width": 300, "height": 120}}})
	res = f.must(t, cmd{node.Update, payload{"id": id, "size": payload{}}})
	if n := f.get(t, id); n.Size != nil {
		t.Errorf("a size of {} leaves the node with %+v", n.Size)
	}
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatal(err)
	}
	if n := f.get(t, id); n.Size == nil {
		t.Error("the undo of a size of {} leaves the node without one")
	}

	// An update that changes nothing changes no revision.
	rev := f.get(t, id).Rev
	if res := f.must(t, cmd{node.Update, payload{"id": id, "color": "red"}}); len(res.Revisions) != 0 {
		t.Errorf("an update that changes nothing changed %v", res.Revisions)
	}
	if got := f.get(t, id).Rev; got != rev {
		t.Errorf("an update that changes nothing took the revision from %d to %d", rev, got)
	}

	// A field that the node's type has none of is refused.
	jumper := f.newNode(t, b, node.TypeJumper, nil)
	for _, p := range []payload{{"kind": "dialogue"}, {"text": json.RawMessage(`{"type": "doc", "content": [{"type": "paragraph"}]}`)},
		{"speaker_component_id": ""}, {"cover_asset_id": ""}, {"is_ending": false}} {
		p["id"] = jumper
		r := f.refused(t, cmd{node.Update, p})
		if !strings.HasPrefix(r.Message, "is set on a node of the type jumper") {
			t.Errorf("%v on a jumper is refused at %s: %s", p, r.Field, r.Message)
		}
	}
	if r := f.refused(t, cmd{node.Update, payload{"id": id, "target": payload{}}}); r.Field != "target" {
		t.Errorf("a target on an element is refused at %s: %s", r.Field, r.Message)
	}

	// A jumper's target is a node of the board that it names, in the trash or
	// not; one that the jumper keeps is taken though it no longer exists.
	target := f.newNode(t, b, node.TypeElement, nil)
	f.must(t, cmd{node.Update, payload{"id": jumper, "target": payload{"board_id": b, "node_id": target}}})
	other := f.newBoard(t, f.project, "Pier")
	if r := f.refused(t, cmd{node.Update, payload{"id": jumper, "target": payload{"board_id": other,
		"node_id": target}}}); r.Field != "target.node_id" {
		t.Errorf("a target on another board is refused at %s: %s", r.Field, r.Message)
	}
	f.must(t, cmd{node.Delete, payload{"id": target}})
	f.exec(t, "DELETE FROM node WHERE id = $1", decode(t, ids.Node, target))
	f.must(t, cmd{node.Update, payload{"id": jumper, "title": "To the pier", "target": payload{"board_id": b,
		"node_id": target}}})
	if n := f.get(t, jumper); n.Target == nil || n.Target.NodeID != target {
		t.Errorf("the jumper whose target was purged has the target %+v", n.Target)
	}
	res = f.must(t, cmd{node.Update, payload{"id": jumper, "target": payload{}}})
	if n := f.get(t, jumper); n.Target != nil {
		t.Errorf("a target of {} leaves the jumper with %+v", n.Target)
	}
	// The undo would set back a target that no longer exists, which only a
	// jumper that keeps it may have, so it is refused, as an undo that would
	// refer to any entity that no longer exists is.
	var refused *commands.RefusedError
	if err := f.undo(t, res.Seq); !errors.As(err, &refused) || refused.Field != "target.node_id" {
		t.Errorf("the undo of the removal of a purged target: %v", err)
	}
	// Tags that are one tag but for their case are refused.
	if got := f.invalid(t, cmd{node.Update, payload{"id": id, "tags": []string{"Quest", "quest"}}}); !slices.Contains(
		got, "payload.tags[1]") {
		t.Errorf("tags alike but for their case are refused at %v", got)
	}
}

// TestMove checks that moving a selection of 100 nodes is one command and
// one entry of the operation log, which changes each node's position alone,
// that its undo moves them back, and that a node that does not move, or that
// is in the trash, or named twice, is passed over or refused.
//
// Verifies: FR-EDIT-02/AC2
func TestMove(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, f.project, "Harbour")
	var cmds []cmd
	var places []payload
	for i := range 100 {
		id := newID()
		cmds = append(cmds, create(id, b, node.TypeElement, i*8, nil))
		places = append(places, payload{"id": id, "position": payload{"x": i*8 + 16, "y": 32.25}})
	}
	f.must(t, cmds...)
	before := f.state(t)
	opsBefore, appliedBefore := f.operations(t)
	res := f.must(t, cmd{node.Move, payload{"nodes": places}})
	ops, applied := f.operations(t)
	if ops != opsBefore+1 || applied != appliedBefore+1 {
		t.Errorf("moving 100 nodes wrote %d operations and %d commands, want one of each", ops-opsBefore,
			applied-appliedBefore)
	}
	if len(res.Revisions) != 100 {
		t.Errorf("moving 100 nodes changed %d entities", len(res.Revisions))
	}
	if n := f.get(t, places[7]["id"].(string)); string(must(json.Marshal(n.Position))) != `{"x":72,"y":32.25}` ||
		n.Rev != 2 {
		t.Errorf("a moved node is at %s, with the revision %d", must(json.Marshal(n.Position)), n.Rev)
	}
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatal(err)
	}
	if got := f.state(t); got != before {
		t.Errorf("after the undo of the move, the nodes are\n%s\nwant\n%s", got, before)
	}

	// A node that is where it goes already does not change.
	res = f.must(t, cmd{node.Move, payload{"nodes": []payload{{"id": places[0]["id"], "position": payload{"x": 0,
		"y": 0}}, places[1]}}})
	if len(res.Revisions) != 1 {
		t.Errorf("a move of a node that stays and one that moves changed %v", res.Revisions)
	}
	if got := f.invalid(t, cmd{node.Move, payload{"nodes": []payload{places[2], places[2]}}}); !slices.Contains(got,
		"payload.nodes[1].id") {
		t.Errorf("a node named twice is refused at %v", got)
	}
	if got := f.invalid(t, cmd{node.Move, payload{"nodes": []payload{}}}); len(got) == 0 {
		t.Error("a move of no nodes is not refused")
	}
	f.must(t, cmd{node.Delete, payload{"id": places[3]["id"]}})
	if r := f.refused(t, cmd{node.Move, payload{"nodes": []payload{places[2], places[3]}}}); r.Field != "nodes[1].id" {
		t.Errorf("a move of a node in the trash is refused at %s: %s", r.Field, r.Message)
	}
	if r := f.refused(t, cmd{node.Move, payload{"nodes": []payload{{"id": newID(), "position": payload{"x": 0,
		"y": 0}}}}}); r.Field != "nodes[0].id" {
		t.Errorf("a move of a node that does not exist is refused at %s: %s", r.Field, r.Message)
	}
}

// must returns v, and panics if err is not nil.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// TestDuplicate checks that node.duplicate creates a copy of a node of the
// same board at another position, with its text, speaker, and tags, that its
// undo deletes the copy, and that a node in the trash is not copied.
func TestDuplicate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, f.project, "Harbour")
	elara := f.component(t, "Elara")
	source := f.newNode(t, b, node.TypeElement, payload{"title": "Greeting", "speaker_component_id": elara,
		"tags": []string{"Quest"}, "text": json.RawMessage(`{"type": "doc", "content": [{"type": "paragraph",
			"content": [{"type": "text", "text": "Hello."}]}]}`), "size": payload{"width": 200, "height": 80}})
	copyID := newID()
	res := f.must(t, cmd{node.Duplicate, payload{"id": copyID, "source_id": source, "position": payload{"x": 400,
		"y": 80}}})
	original, dup := f.get(t, source), f.get(t, copyID)
	original.ID, original.Position, original.CreatedAt, original.UpdatedAt = dup.ID, dup.Position, dup.CreatedAt,
		dup.UpdatedAt
	if a, b := must(json.Marshal(original)), must(json.Marshal(dup)); string(a) != string(b) {
		t.Errorf("the copy is\n%s\nwant\n%s", b, a)
	}
	if got := f.used(t, usage.Nodes); got != 2 {
		t.Errorf("the workspace uses %d nodes, want 2", got)
	}
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatal(err)
	}
	if _, err := f.nodes.Get(context.Background(), f.actor(), f.project, decode(t, ids.Node, copyID)); !errors.Is(err,
		node.ErrNotFound) {
		t.Errorf("after the undo, the copy reads %v", err)
	}
	f.must(t, cmd{node.Delete, payload{"id": source}})
	if r := f.refused(t, cmd{node.Duplicate, payload{"id": newID(), "source_id": source, "position": payload{"x": 0,
		"y": 0}}}); r.Field != "source_id" {
		t.Errorf("a copy of a node in the trash is refused at %s: %s", r.Field, r.Message)
	}
}

// TestTrash checks that a deleted node goes to the trash, which lists it by
// its title, and comes back where it was, that a board's nodes are in the
// trash with it, unlisted, and come back with it, that nothing changes them
// meanwhile, that one deleted on its own before its board is restored only
// once its board is, that the purge deletes a node whose time has passed,
// and the speaker of an element, at a new revision, and that the
// workspace's usage of nodes follows them all.
func TestTrash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	b := f.newBoard(t, f.project, "Harbour")
	keeper := f.newNode(t, b, node.TypeElement, payload{"title": "Keeper"})
	gull := f.newNode(t, b, node.TypeElement, payload{"title": "Gull"})
	f.newNode(t, b, node.TypeBranch, nil)
	check := func(want int64) {
		t.Helper()
		if got, live := f.used(t, usage.Nodes), f.liveNodes(t); got != want || live != want {
			t.Errorf("the workspace uses %d nodes, with %d not in a trash, want %d", got, live, want)
		}
	}
	check(3)
	listed := func() []string {
		t.Helper()
		items, err := f.trash.List(ctx, f.actor(), f.project, trash.Cursor{}, 100)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range items {
			out = append(out, it.Kind+" "+it.Name)
		}
		return out
	}

	res := f.must(t, cmd{node.Delete, payload{"id": gull}})
	check(2)
	if got := listed(); !slices.Equal(got, []string{"node Gull"}) {
		t.Errorf("the trash lists %v", got)
	}
	if _, err := f.nodes.Get(ctx, f.actor(), f.project, decode(t, ids.Node, gull)); !errors.Is(err, node.ErrNotFound) {
		t.Errorf("a node in the trash reads %v", err)
	}
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatal(err)
	}
	check(3)

	// The board's nodes are in the trash with it, unlisted, and nothing but
	// its restoration changes them. One deleted before it is listed.
	f.must(t, cmd{node.Delete, payload{"id": gull}})
	f.must(t, cmd{board.Delete, payload{"id": b}})
	check(0)
	if got := listed(); !slices.Equal(got, []string{"board Harbour", "node Gull"}) {
		t.Errorf("the trash lists %v", got)
	}
	for _, c := range []cmd{{node.Delete, payload{"id": keeper}}, {node.Restore, payload{"id": keeper}},
		{node.Restore, payload{"id": gull}}, {node.Update, payload{"id": keeper, "title": "Kept"}},
		{node.Move, payload{"nodes": []payload{{"id": keeper, "position": payload{"x": 1, "y": 1}}}}}} {
		f.refused(t, c)
	}
	nodes, err := f.nodes.BoardNodes(ctx, f.actor(), f.project, decode(t, ids.Board, b), ids.UUID{}, 100)
	if !errors.Is(err, board.ErrNotFound) {
		t.Errorf("the nodes of a board in the trash read %v, %v", nodes, err)
	}
	f.must(t, cmd{board.Restore, payload{"id": b}})
	check(2)
	f.must(t, cmd{node.Restore, payload{"id": gull}})
	check(3)

	// The purge deletes a node whose time in the trash has passed.
	f.must(t, cmd{node.Delete, payload{"id": gull}})
	f.exec(t, "UPDATE node SET deleted_at = now() - interval '31 days' WHERE id = $1", decode(t, ids.Node, gull))
	if _, err := trash.Purge(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, "SELECT count(*) FROM node WHERE id = $1", decode(t, ids.Node, gull)); n != 0 {
		t.Error("the purge left a node whose time in the trash has passed")
	}
	if r := f.refused(t, cmd{node.Restore, payload{"id": gull}}); r.Field != "id" {
		t.Errorf("the restore of a purged node is refused at %s: %s", r.Field, r.Message)
	}
	check(2)

	// The purge of a component leaves each element whose speaker it is, in
	// the trash or not, without one, at a new revision, as a change of the
	// element would, so that a client that expects the revision that it read
	// finds that the element changed.
	elara := f.component(t, "Elara")
	speaking := f.newNode(t, b, node.TypeElement, payload{"speaker_component_id": elara})
	mute := f.newNode(t, b, node.TypeElement, payload{"speaker_component_id": elara})
	f.must(t, cmd{node.Delete, payload{"id": mute}})
	f.must(t, cmd{component.Delete, payload{"id": elara}})
	f.exec(t, "UPDATE component SET deleted_at = now() - interval '31 days' WHERE id = $1",
		decode(t, ids.Component, elara))
	if _, err := trash.Purge(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if n := f.get(t, speaking); n.SpeakerComponentID != "" || n.Rev != 2 {
		t.Errorf("once the purge deletes its speaker, an element has the speaker %q at the revision %d, want "+
			"none at 2", n.SpeakerComponentID, n.Rev)
	}
	if n := f.count(t, "SELECT count(*) FROM node WHERE id = $1 AND speaker_component_id IS NULL AND rev = 3",
		decode(t, ids.Node, mute)); n != 1 {
		t.Error("once the purge deletes its speaker, an element in the trash keeps it, or its revision")
	}
}

// TestUsage checks that the workspace's usage of nodes follows its projects'
// deletion to the workspace's trash and their restoration, whatever the
// order of a batch's commands, and that the reconciliation corrects its
// drift.
//
// Verifies: FR-ENT-02/AC1
func TestUsage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, f.project, "Harbour")
	for range 3 {
		f.newNode(t, b, node.TypeElement, nil)
	}
	other := f.newProject(t, "Pier")
	ob := f.newBoard(t, other, "Pier")
	if _, err := f.apply(t, other, create(newID(), ob, node.TypeElement, 0, nil)); err != nil {
		t.Fatal(err)
	}
	if got := f.used(t, usage.Nodes); got != 4 {
		t.Errorf("the workspace uses %d nodes, want 4", got)
	}
	// A batch that creates a node, then deletes its project, and one that
	// deletes the project, then creates a node in it.
	for i, batch := range [][]cmd{{create(newID(), ob, node.TypeElement, 0, nil), {project.Delete, payload{}}},
		{{project.Delete, payload{}}, create(newID(), ob, node.TypeElement, 0, nil)}} {
		if _, err := f.apply(t, other, batch...); err != nil {
			t.Fatal(err)
		}
		if got := f.used(t, usage.Nodes); got != 3 {
			t.Errorf("once a project is in the trash (%d), the workspace uses %d nodes, want 3", i, got)
		}
		if _, err := f.apply(t, other, cmd{project.Restore, payload{}}); err != nil {
			t.Fatal(err)
		}
		if got, live, want := f.used(t, usage.Nodes), f.liveNodes(t), int64(5+i); got != want || live != want {
			t.Errorf("once the project is restored (%d), the workspace uses %d nodes, with %d, want %d", i, got,
				live, want)
		}
	}
	// A batch of a project in the trash that restores it, as the undo of a
	// batch that changed its nodes and deleted it does, adds the nodes that the
	// project has once the batch has applied, whatever its other commands do to
	// them before the restoration or after, and one that deletes the project
	// again adds nothing.
	if _, err := f.apply(t, other, cmd{project.Delete, payload{}}); err != nil {
		t.Fatal(err)
	}
	gull, tern := newID(), newID()
	for i, c := range []struct {
		batch    []cmd
		restored bool
	}{
		{[]cmd{create(gull, ob, node.TypeElement, 0, nil), {project.Restore, payload{}}}, true},
		{[]cmd{{project.Restore, payload{}}, {node.Delete, payload{"id": gull}}}, true},
		{[]cmd{{node.Restore, payload{"id": gull}}, {project.Restore, payload{}},
			create(tern, ob, node.TypeElement, 0, nil), {project.Delete, payload{}}}, false},
		{[]cmd{{project.Restore, payload{}}, {node.Delete, payload{"id": tern}}, {project.Delete, payload{}},
			{project.Restore, payload{}}}, true},
	} {
		if _, err := f.apply(t, other, c.batch...); err != nil {
			t.Fatal(err)
		}
		if got, live := f.used(t, usage.Nodes), f.liveNodes(t); got != live {
			t.Errorf("once a batch of the project in the trash that restores it has applied (%d), the workspace "+
				"uses %d nodes, want %d", i, got, live)
		}
		if c.restored {
			if _, err := f.apply(t, other, cmd{project.Delete, payload{}}); err != nil {
				t.Fatal(err)
			}
		}
	}

	f.exec(t, "INSERT INTO usage_delta (id, workspace_id, key, delta) VALUES ($1, $2, 'nodes', 7)", ids.New(), f.ws)
	if _, err := usage.Reconcile(context.Background(), f.db); err != nil {
		t.Fatal(err)
	}
	if got, live := f.used(t, usage.Nodes), f.liveNodes(t); got != live {
		t.Errorf("after the reconciliation, the workspace uses %d nodes, want the recount, %d", got, live)
	}
}

// TestBoardReferenced checks that a board whose nodes jumpers on other boards
// target is deleted only once its deletion is confirmed, which the refusal
// lists them for, but for the jumpers whose targets are deleted already, or
// purged, which dangle already, that the refusal counts every jumper, past
// the most that it lists, and that the jumpers keep their targets then,
// dangling, rather than lose them, while those of the board itself go to the
// trash with it.
//
// Verifies: FR-PROJ-02/AC2
func TestBoardReferenced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	harbour, pier := f.newBoard(t, f.project, "Harbour"), f.newBoard(t, f.project, "Pier")
	keeper := f.newNode(t, harbour, node.TypeElement, nil)
	f.newNode(t, harbour, node.TypeJumper, payload{"target": payload{"board_id": harbour, "node_id": keeper}})
	jumper := f.newNode(t, pier, node.TypeJumper, payload{"title": "To the keeper", "target": payload{
		"board_id": harbour, "node_id": keeper}})
	gone := f.newNode(t, pier, node.TypeJumper, payload{"target": payload{"board_id": harbour, "node_id": keeper}})
	f.must(t, cmd{node.Delete, payload{"id": gone}})
	// A jumper whose target is in the trash, or purged, dangles already.
	lost, purged := f.newNode(t, harbour, node.TypeElement, nil), f.newNode(t, harbour, node.TypeElement, nil)
	f.newNode(t, pier, node.TypeJumper, payload{"target": payload{"board_id": harbour, "node_id": lost}})
	f.newNode(t, pier, node.TypeJumper, payload{"target": payload{"board_id": harbour, "node_id": purged}})
	f.must(t, cmd{node.Delete, payload{"id": lost}})
	f.exec(t, "DELETE FROM node WHERE id = $1", decode(t, ids.Node, purged))

	r := f.refused(t, cmd{board.Delete, payload{"id": harbour}})
	var referenced *board.ReferencedError
	if !errors.As(r, &referenced) || r.Field != "confirm" {
		t.Fatalf("the deletion of a board that a jumper targets is refused with %v at %s", r.Reason, r.Field)
	}
	if want := []board.Referrer{{ID: jumper, BoardID: pier, Title: "To the keeper"}}; referenced.Count != 1 ||
		!slices.Equal(referenced.Jumpers, want) {
		t.Errorf("the refusal lists %d jumpers, %v, want %v", referenced.Count, referenced.Jumpers, want)
	}
	listed, err := f.nodes.BoardReferencedBy(ctx, f.actor(), f.project, decode(t, ids.Board, harbour), ids.UUID{}, 10)
	if err != nil || len(listed) != 1 || listed[0].ID != jumper {
		t.Errorf("the jumpers that target the board are %v, %v", listed, err)
	}

	res := f.must(t, cmd{board.Delete, payload{"id": harbour, "confirm": true}})
	if n := f.get(t, jumper); n.Target == nil || n.Target.NodeID != keeper || n.Target.BoardID != harbour {
		t.Errorf("once the board is deleted, the jumper targets %+v, want its node still", n.Target)
	}
	// The undo of the deletion restores the board, and the undo of that
	// deletes it again, which confirms it, as the deletion did.
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatal(err)
	}
	restored := f.count(t, "SELECT max(seq) FROM operation WHERE project_id = $1", f.project)
	if err := f.undo(t, restored); err != nil {
		t.Errorf("the undo of the board's restoration: %v", err)
	}
	// Once the jumper is gone, the deletion needs no confirmation.
	f.must(t, cmd{board.Restore, payload{"id": harbour}}, cmd{node.Delete, payload{"id": jumper}},
		cmd{board.Delete, payload{"id": harbour}})

	// The refusal lists the first jumpers, and counts them all.
	dock := f.newBoard(t, f.project, "Dock")
	moored := f.newNode(t, dock, node.TypeElement, nil)
	var many []cmd
	for i := range board.MaxReferrers + 1 {
		many = append(many, create(newID(), pier, node.TypeJumper, i, payload{"target": payload{"board_id": dock,
			"node_id": moored}}))
	}
	f.must(t, many...)
	r = f.refused(t, cmd{board.Delete, payload{"id": dock}})
	if !errors.As(r, &referenced) || referenced.Count != board.MaxReferrers+1 ||
		len(referenced.Jumpers) != board.MaxReferrers {
		t.Errorf("the refusal of a board that %d jumpers target counts %d, and lists %d", board.MaxReferrers+1,
			referenced.Count, len(referenced.Jumpers))
	}
}

// TestReferencedBy checks that the jumpers that target a node are listed,
// across every board, without those in the trash, within 200 ms in a project
// of 5,000 nodes.
//
// Verifies: FR-EDIT-05/AC2
func TestReferencedBy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	var boards []string
	for i := range 10 {
		boards = append(boards, f.newBoard(t, f.project, fmt.Sprintf("Board %d", i)))
	}
	target := f.newNode(t, boards[0], node.TypeElement, nil)
	// 4,900 elements and 99 jumpers that the superuser adds, 10 of which
	// target the node, spread over the boards, and one of which is in the
	// trash.
	f.exec(t, `INSERT INTO node (id, workspace_id, project_id, board_id, type, kind, text, x, y)
		SELECT uuid_v7(), $1, $2, (($3::uuid[])[1 + i % 10]), 'element', 'dialogue',
			'{"type": "doc", "content": [{"type": "paragraph"}]}', i, i FROM generate_series(1, 4900) i`,
		f.ws, f.project, uuidList(t, boards))
	f.exec(t, `INSERT INTO node (id, workspace_id, project_id, board_id, type, x, y, target_board_id, target_node_id)
		SELECT uuid_v7(), $1, $2, (($3::uuid[])[1 + (i / 10) % 10]), 'jumper', i, i,
			CASE WHEN i % 10 = 0 THEN $4::uuid ELSE NULL END, CASE WHEN i % 10 = 0 THEN $5::uuid ELSE NULL END
		FROM generate_series(1, 99) i`, f.ws, f.project, uuidList(t, boards), decode(t, ids.Board, boards[0]),
		decode(t, ids.Node, target))
	f.exec(t, `UPDATE node SET target_board_id = $1, target_node_id = $2 WHERE id = (SELECT id FROM node
		WHERE type = 'jumper' AND target_node_id IS NULL ORDER BY id LIMIT 1)`, decode(t, ids.Board, boards[0]),
		decode(t, ids.Node, target))
	f.exec(t, `UPDATE node SET deleted_at = now() WHERE id = (SELECT id FROM node WHERE target_node_id IS NOT NULL
		ORDER BY id LIMIT 1)`)
	f.exec(t, "ANALYZE node")
	if n := f.count(t, "SELECT count(*) FROM node WHERE project_id = $1", f.project); n != 5000 {
		t.Fatalf("the project has %d nodes", n)
	}
	want := f.count(t, `SELECT count(*) FROM node WHERE target_node_id = $1 AND deleted_at IS NULL`,
		decode(t, ids.Node, target))
	var fastest time.Duration
	for i := range 5 {
		start := time.Now()
		got, err := f.nodes.ReferencedBy(ctx, f.actor(), f.project, decode(t, ids.Node, target), ids.UUID{}, 201)
		took := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(got)) != want || want != 9 {
			t.Fatalf("the node is referenced by %d jumpers, want %d, 9", len(got), want)
		}
		boardsOf := map[string]bool{}
		for _, j := range got {
			if j.Type != node.TypeJumper || j.Target == nil || j.Target.NodeID != target {
				t.Errorf("the node is referenced by %+v", j)
			}
			boardsOf[j.BoardID] = true
		}
		if len(boardsOf) < 2 {
			t.Errorf("the jumpers listed are of %d boards, want several", len(boardsOf))
		}
		if i == 0 || took < fastest {
			fastest = took
		}
	}
	if fastest > 200*time.Millisecond {
		t.Errorf("listing the jumpers that target a node of a project of 5,000 nodes took %v, want 200 ms at most",
			fastest)
	}
}

// uuidList returns the UUIDs of the boards' IDs.
func uuidList(t *testing.T, boards []string) []ids.UUID {
	t.Helper()
	out := make([]ids.UUID, len(boards))
	for i, b := range boards {
		out[i] = decode(t, ids.Board, b)
	}
	return out
}

// TestEntries checks that a board's entry node is a node of the board, in
// the trash or not, and that the project's entry node is a node of its entry
// board, unless the entry keeps it, and that the undo of each sets it back.
func TestEntries(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	harbour, pier := f.newBoard(t, f.project, "Harbour"), f.newBoard(t, f.project, "Pier")
	keeper, gull := f.newNode(t, harbour, node.TypeElement, nil), f.newNode(t, pier, node.TypeElement, nil)
	entryOf := func(b string) string {
		t.Helper()
		var id *ids.UUID
		if err := f.admin.QueryRow(context.Background(), "SELECT entry_node_id FROM board WHERE id = $1",
			decode(t, ids.Board, b)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if id == nil {
			return ""
		}
		return ids.Encode(ids.Node, *id)
	}
	res := f.must(t, cmd{node.SetBoardEntry, payload{"id": harbour, "node_id": keeper}})
	if got := entryOf(harbour); got != keeper {
		t.Errorf("the board's entry node is %q, want %s", got, keeper)
	}
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatal(err)
	}
	if got := entryOf(harbour); got != "" {
		t.Errorf("after the undo, the board's entry node is %q", got)
	}
	if r := f.refused(t, cmd{node.SetBoardEntry, payload{"id": harbour, "node_id": gull}}); r.Field != "node_id" {
		t.Errorf("a node of another board as the entry is refused at %s: %s", r.Field, r.Message)
	}
	f.must(t, cmd{node.Delete, payload{"id": keeper}})
	f.must(t, cmd{node.SetBoardEntry, payload{"id": harbour, "node_id": keeper}})

	settings := func(boardID, nodeID string) cmd {
		return cmd{project.UpdateSettings, payload{"entry": payload{"board_id": boardID, "node_id": nodeID}}}
	}
	f.must(t, settings(pier, gull))
	for _, c := range []cmd{settings(harbour, gull), settings(pier, ids.Encode(ids.Node, ids.New()))} {
		if r := f.refused(t, c); r.Field != "entry.node_id" {
			t.Errorf("%v is refused at %s: %s", c.payload, r.Field, r.Message)
		}
	}
	// A node in the trash is taken, and so is the entry's node that the
	// entry keeps, though it is purged.
	f.must(t, settings(harbour, keeper))
	f.exec(t, "DELETE FROM node WHERE id = $1", decode(t, ids.Node, keeper))
	f.must(t, cmd{project.UpdateSettings, payload{"publish_min_role": "admin", "entry": payload{"board_id": harbour,
		"node_id": keeper}}})
}

// TestConcurrentCreation checks that the usage of nodes serialises no write
// on a hot row: 16 sessions creating nodes at once in one workspace, across
// two of its projects, wait for no lock of the usage, neither of its tables
// nor of its advisory locks, while each batch of a project waits for the
// project's, and every node is counted.
//
// Verifies: FR-ENT-02/AC3
func TestConcurrentCreation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	projects := []ids.UUID{f.project, f.newProject(t, "Pier")}
	boards := []string{f.newBoard(t, projects[0], "Harbour"), f.newBoard(t, projects[1], "Pier")}
	before := f.used(t, usage.Nodes)

	// The monitor samples, as the superuser, the sessions of the test's
	// database that wait for a lock, and keeps those whose wait is the
	// usage's: for a lock of its tables, or of its advisory locks, or while
	// writing one of its rows.
	monitor := f.d.Admin(t)
	stop := make(chan struct{})
	var mu sync.Mutex
	var waits, usageWaits []string
	done := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			rows, err := monitor.Query(ctx, `SELECT a.query, coalesce(l.relation::regclass::text, l.locktype),
					coalesce(l.locktype = 'advisory' AND l.classid::int = $1
						OR l.relation IN ('usage_delta'::regclass, 'usage_counter'::regclass)
						OR a.query LIKE '%usage_delta%' OR a.query LIKE '%usage_counter%', false)
				FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
				WHERE NOT l.granted AND a.datname = current_database()`, database.UsageLock)
			if err != nil {
				done <- err
				return
			}
			for rows.Next() {
				var query, lock string
				var isUsage bool
				if err := rows.Scan(&query, &lock, &isUsage); err != nil {
					rows.Close()
					done <- err
					return
				}
				mu.Lock()
				waits = append(waits, lock)
				if isUsage {
					usageWaits = append(usageWaits, lock+": "+query)
				}
				mu.Unlock()
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				done <- err
				return
			}
		}
	}()

	const sessions, batches = 16, 15
	var wg sync.WaitGroup
	errs := make(chan error, sessions)
	for s := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := s % 2
			for range batches {
				raw := must(json.Marshal(payload{"id": newID(), "board_id": boards[p], "type": "element",
					"position": payload{"x": s, "y": 0}}))
				if _, err := f.pipeline.Apply(ctx, f.actor(), projects[p], []commands.Command{{ID: ids.New(),
					Type: node.Create, Payload: raw}}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if len(usageWaits) > 0 {
		t.Errorf("the sessions waited %d times for a lock of the usage: %v", len(usageWaits), usageWaits)
	}
	t.Logf("the sessions waited %d times for a lock, none of them the usage's", len(waits))
	if got, want := f.used(t, usage.Nodes)-before, int64(sessions*batches); got != want {
		t.Errorf("the 16 sessions added %d nodes to the usage, want %d", got, want)
	}
}

// TestStates checks that an answer of revision_conflict shows a node of a
// board in the trash as in the trash, with its board's deletion.
func TestStates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, f.project, "Harbour")
	id := f.newNode(t, b, node.TypeElement, nil)
	f.must(t, cmd{board.Delete, payload{"id": b}})
	raw := must(json.Marshal(payload{"id": id, "title": "Kept"}))
	_, err := f.pipeline.Apply(context.Background(), f.actor(), f.project, []commands.Command{{ID: ids.New(),
		Type: node.Update, Payload: raw, ExpectedRev: map[commands.Ref]int64{{Kind: ids.Node,
			ID: decode(t, ids.Node, id)}: 7}}})
	var conflict *commands.RevisionConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 {
		t.Fatalf("a stale revision of a node is answered with %v", err)
	}
	var state node.Node
	if err := json.Unmarshal(conflict.Conflicts[0].State, &state); err != nil || state.DeletedAt == nil {
		t.Errorf("the conflict shows the node of a board in the trash as %s (%v)", conflict.Conflicts[0].State, err)
	}
}
