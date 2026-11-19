package serializer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/fixture"
	"github.com/CyberCute-Studio/storycreep/server/internal/flow"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// The personal data of the owner of each project of the tests, which no
// published file may hold (PRD §6.2).
const (
	ownerEmail = "ottilie.private@example.com"
	ownerName  = "Ottilie Private-Author"
)

// env is a project of a new database, with its owner, and the pipeline of
// the commands that change its content.
type env struct {
	db        *database.DB
	workspace ids.UUID
	project   ids.UUID
	owner     ids.UUID
	pipeline  *commands.Pipeline
}

// newEnv returns a new project of a new database, named name.
func newEnv(t *testing.T, name string) *env {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	db, admin := d.Open(t), d.Admin(t)
	owner := ids.New()
	if _, err := admin.Exec(ctx, `INSERT INTO "user" (id, email, display_name) VALUES ($1, $2, $3)`, owner,
		ownerEmail, ownerName); err != nil {
		t.Fatal(err)
	}
	ws := ids.New()
	if _, err := admin.Exec(ctx, `INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Serializer',
		'serializer', 'unlimited')`, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO membership (id, workspace_id, user_id, role)
		VALUES ($1, $2, $3, 'owner')`, ids.New(), ws, owner); err != nil {
		t.Fatal(err)
	}
	reg := commands.NewRegistry()
	project.Register(reg)
	board.Register(reg)
	variable.Register(reg)
	component.Register(reg)
	node.Register(reg)
	connection.Register(reg)
	expression.Register(reg)
	dependency.Register(reg)
	ent := entitlements.New(entitlements.Options{Meter: usage.Meter{}})
	pipeline := commands.New(db, commands.Options{Registry: reg, Entitlements: ent})
	projects := project.New(db, project.Options{Entitlements: ent, Pipeline: pipeline, Seed: component.Seed})
	p, err := projects.Create(ctx, owner, ws, project.Settings{Name: name}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := ids.Decode(ids.Project, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &env{db: db, workspace: ws, project: projectID, owner: owner, pipeline: pipeline}
}

// apply applies one batch of the commands to the project as its owner, each
// a type and its payload, in turn, or fails the test.
func (e *env) apply(t *testing.T, cmds ...any) {
	t.Helper()
	var batch []commands.Command
	for i := 0; i+1 < len(cmds); i += 2 {
		payload, err := json.Marshal(cmds[i+1])
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, commands.Command{ID: ids.New(), Type: commands.Type(cmds[i].(string)),
			Payload: payload})
	}
	if _, err := e.pipeline.Apply(context.Background(), commands.Actor{UserID: e.owner}, e.project,
		batch); err != nil {
		t.Fatal(err)
	}
}

// build returns the project's draft serialized with the stamp, read as of
// one moment, as a snapshot's build reads it, or Serialize's error.
func (e *env) build(t *testing.T, s serializer.Stamp) (*serializer.Snapshot, error) {
	t.Helper()
	var out *serializer.Snapshot
	err := e.db.InSnapshot(context.Background(), database.Scope{WorkspaceID: e.workspace, UserID: e.owner},
		func(tx pgx.Tx) error {
			var err error
			out, err = serializer.Build(context.Background(), tx, e.workspace, e.project, s)
			return err
		})
	return out, err
}

// mustBuild returns the project's draft serialized with the stamp, or fails
// the test.
func (e *env) mustBuild(t *testing.T, s serializer.Stamp) *serializer.Snapshot {
	t.Helper()
	out, err := e.build(t, s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// id returns a new public ID of the kind k.
func id(k ids.Kind) string { return ids.Encode(k, ids.New()) }

// paragraph returns a rich text of one paragraph of the inline nodes.
func paragraph(inlines ...richtext.Inline) *richtext.Doc {
	return &richtext.Doc{Blocks: []richtext.Block{&richtext.Paragraph{Inlines: inlines}}}
}

// TestBuildOnTwoServers checks that one project, the small fixture built
// by the same batches of commands in two databases, as on two servers,
// serializes to the same bytes, file by file, with the same content hash,
// though the rows' times, authors, and the IDs that each server made
// differ, and that the manifests differ in the project's ID alone; that
// serializing it again gives the same bytes; that it validates against the
// published JSON Schemas and loads in the reference runtime; and that no
// file holds anything of the author's account (§6.2).
//
// Verifies: FR-SNAP-03/AC1
func TestBuildOnTwoServers(t *testing.T) {
	f, err := fixture.Generate(fixture.Small, 7)
	if err != nil {
		t.Fatal(err)
	}
	var snaps []*serializer.Snapshot
	var owners []ids.UUID
	for range 2 {
		e := newEnv(t, "Small")
		if err := f.Apply(context.Background(), e.pipeline, commands.Actor{UserID: e.owner}, e.project); err != nil {
			t.Fatal(err)
		}
		s := e.mustBuild(t, goldenStamp)
		if again := e.mustBuild(t, goldenStamp); !maps.EqualFunc(again.Files, s.Files, bytes.Equal) {
			t.Error("serializing the project again gives other bytes")
		}
		snaps, owners = append(snaps, s), append(owners, e.owner)
	}
	a, b := snaps[0], snaps[1]
	if len(a.StandIns) > 0 {
		t.Errorf("stood in for %v", a.StandIns)
	}
	if a.ContentHash != b.ContentHash {
		t.Errorf("the content hashes are %s and %s", a.ContentHash, b.ContentHash)
	}
	if got, want := slices.Sorted(maps.Keys(b.Files)), slices.Sorted(maps.Keys(a.Files)); !slices.Equal(got, want) {
		t.Fatalf("the files are %v and %v", want, got)
	}
	for p, data := range a.Files {
		if p != published.ManifestPath && !bytes.Equal(b.Files[p], data) {
			t.Errorf("%s differs between the two servers", p)
		}
	}
	if a.Manifest.Counts.Boards != 3 || a.Manifest.Counts.Nodes != 200 || a.Manifest.Counts.Variables != 30 ||
		a.Manifest.Counts.Components != 6 {
		t.Errorf("the counts are %+v, want the small fixture's", a.Manifest.Counts)
	}
	ma, mb := a.Manifest, b.Manifest
	mb.Project.ID = ma.Project.ID
	da, err := json.Marshal(ma)
	if err != nil {
		t.Fatal(err)
	}
	db, err := json.Marshal(mb)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(da, db) {
		t.Errorf("the manifests differ beyond the project's ID:\n%s\n%s", da, db)
	}
	newSchemas(t).check(t, a.Files)
	for i, s := range snaps {
		for p, data := range s.Files {
			for _, personal := range []string{ownerEmail, ownerName, owners[i].String(),
				ids.Encode(ids.User, owners[i])} {
				if bytes.Contains(data, []byte(personal)) {
					t.Errorf("%s holds %q, of the author's account", p, personal)
				}
			}
		}
	}
}

// tree reads a file of the snapshot as a tree of JSON values.
func tree(t *testing.T, s *serializer.Snapshot, path string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(s.Files[path], &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// asts returns every expression's AST that the tree holds, by its path in
// the tree, each without its name fields if names is false, as JSON.
func asts(t *testing.T, v any, at string, names bool, out map[string]string) {
	t.Helper()
	switch v := v.(type) {
	case map[string]any:
		for k, c := range v {
			if k == "ast" {
				out[at+"."+k] = astJSON(t, c, names)
				continue
			}
			asts(t, c, at+"."+k, names, out)
		}
	case []any:
		for i, c := range v {
			asts(t, c, fmt.Sprintf("%s[%d]", at, i), names, out)
		}
	}
}

// astJSON returns the AST as JSON, without its name fields unless names.
func astJSON(t *testing.T, ast any, names bool) string {
	t.Helper()
	var strip func(any) any
	strip = func(v any) any {
		switch v := v.(type) {
		case map[string]any:
			out := map[string]any{}
			for k, c := range v {
				if k != "name" || names {
					out[k] = strip(c)
				}
			}
			return out
		case []any:
			out := make([]any, len(v))
			for i, c := range v {
				out[i] = strip(c)
			}
			return out
		}
		return v
	}
	data, err := json.Marshal(strip(ast))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestRenameChangesNamesAlone checks that the published ASTs name a
// variable and a component by their IDs, and that renaming the variable's
// key and the component's changes nothing of an AST that the snapshot holds
// but its debug name fields, the sources that the renames rewrote aside.
//
// Verifies: FR-EXPR-03/AC2
func TestRenameChangesNamesAlone(t *testing.T) {
	e := newEnv(t, "Rename")
	bd, trust := id(ids.Board), id(ids.Variable)
	typ, attr, elara := id(ids.ComponentType), id(ids.AttributeDef), id(ids.Component)
	a, b := id(ids.Node), id(ids.Node)
	e.apply(t,
		"board.create", map[string]any{"id": bd, "name": "Harbour"},
		"variable.create", map[string]any{"id": trust, "key": "trust", "type": "int", "scope": "global", "default": 0},
		"component_type.create", map[string]any{"id": typ, "name": "Cast", "key": "cast", "icon": "person"},
		"attribute.create", map[string]any{"id": attr, "component_type_id": typ, "name": "Mood", "key": "mood",
			"type": "int", "runtime": true},
		"component.create", map[string]any{"id": elara, "component_type_id": typ, "name": "Elara", "key": "elara"},
		"node.create", map[string]any{"id": a, "board_id": bd, "type": "element", "kind": "narration",
			"position": map[string]any{"x": 0, "y": 0}, "on_enter": "trust += 1\nelara.mood = trust * 2"},
		"node.create", map[string]any{"id": b, "board_id": bd, "type": "element", "kind": "narration",
			"position": map[string]any{"x": 200, "y": 0}, "is_ending": true},
		"connection.create", map[string]any{"id": id(ids.Connection), "from_node_id": a, "to_node_id": b,
			"condition": "trust > 2 and elara.mood < 10"},
	)
	path := published.BoardPath("harbour")
	before := e.mustBuild(t, goldenStamp)
	e.apply(t, "variable.rename", map[string]any{"id": trust, "key": "affinity"},
		"component.set_key", map[string]any{"id": elara, "key": "keeper"})
	after := e.mustBuild(t, goldenStamp)

	got, want := map[string]string{}, map[string]string{}
	asts(t, tree(t, after, path), "", false, got)
	asts(t, tree(t, before, path), "", false, want)
	if len(want) != 2 || !maps.Equal(got, want) {
		t.Errorf("the ASTs without their names are\n%v\nwant\n%v", got, want)
	}
	named := map[string]string{}
	asts(t, tree(t, after, path), "", true, named)
	for at, ast := range named {
		if strings.Contains(ast, `"trust"`) || strings.Contains(ast, `"elara.mood"`) ||
			!strings.Contains(ast, `"affinity"`) || !strings.Contains(ast, `"keeper.mood"`) {
			t.Errorf("%s names %s, want the new keys", at, ast)
		}
		for _, ref := range []string{trust, elara, attr} {
			if !strings.Contains(ast, ref) {
				t.Errorf("%s does not name %s by its ID: %s", at, ref, ast)
			}
		}
	}
}

// TestMentionExportsNameAndID checks that a text's mention of a component is
// published as the component's name, as it is when the snapshot is built,
// and its ID, which a rename of the component's name changes nothing of.
//
// Verifies: FR-TEXT-03/AC2
func TestMentionExportsNameAndID(t *testing.T) {
	e := newEnv(t, "Mentions")
	bd, typ, elara, n := id(ids.Board), id(ids.ComponentType), id(ids.Component), id(ids.Node)
	e.apply(t,
		"board.create", map[string]any{"id": bd, "name": "Harbour"},
		"component_type.create", map[string]any{"id": typ, "name": "Cast", "key": "cast", "icon": "person"},
		"component.create", map[string]any{"id": elara, "component_type_id": typ, "name": "Elara", "key": "elara"},
		"node.create", map[string]any{"id": n, "board_id": bd, "type": "element", "kind": "narration",
			"position": map[string]any{"x": 0, "y": 0}, "is_ending": true,
			"text": paragraph(&richtext.Text{Text: "Ask "}, &richtext.Mention{Component: elara},
				&richtext.Text{Text: "."})},
	)
	mention := func(s *serializer.Snapshot) richtext.Segment {
		t.Helper()
		var f published.Board
		decode(t, s.Files, published.BoardPath("harbour"), &f)
		segs := f.Nodes[0].(*published.Element).Text.Segments
		if len(segs) != 3 || segs[1].Kind != richtext.SegmentMention {
			t.Fatalf("the text's segments are %+v", segs)
		}
		return segs[1]
	}
	if m := mention(e.mustBuild(t, goldenStamp)); m.Ref != elara || m.Value != "Elara" {
		t.Errorf("the mention is %+v, want Elara's name and ID", m)
	}
	e.apply(t, "component.rename", map[string]any{"id": elara, "name": "Elara Vance"})
	if m := mention(e.mustBuild(t, goldenStamp)); m.Ref != elara || m.Value != "Elara Vance" {
		t.Errorf("the mention is %+v, want Elara's new name and her ID", m)
	}
}

// loaded returns a runtime of the snapshot, in strict mode, as a game loads
// it.
func loaded(t *testing.T, s *serializer.Snapshot) *flow.Runtime {
	t.Helper()
	snap, err := loadSnapshot(s.Files)
	if err != nil {
		t.Fatal(err)
	}
	return flow.New(snap, true)
}

// TestChoicesInAuthorOrder checks that an element's choices are published
// in the order that their author gave them, which a reorder changes, by
// their order fields, which the reference runtime offers them in, while the
// file lists the connections by ID.
//
// Verifies: FR-EDIT-03/AC1
func TestChoicesInAuthorOrder(t *testing.T) {
	e := newEnv(t, "Choices")
	bd, start := id(ids.Board), id(ids.Node)
	cmds := []any{"board.create", map[string]any{"id": bd, "name": "Harbour"},
		"node.create", map[string]any{"id": start, "board_id": bd, "type": "element", "kind": "narration",
			"position": map[string]any{"x": 0, "y": 0}}}
	var choices []string
	for i, key := range []string{"8", "G", "V"} {
		to, c := id(ids.Node), id(ids.Connection)
		choices = append(choices, c)
		cmds = append(cmds,
			"node.create", map[string]any{"id": to, "board_id": bd, "type": "element", "kind": "narration",
				"position": map[string]any{"x": 200, "y": i * 100}, "is_ending": true},
			"connection.create", map[string]any{"id": c, "from_node_id": start, "to_node_id": to, "order": key,
				"label": paragraph(&richtext.Text{Text: "Choice " + key})})
	}
	e.apply(t, cmds...)
	check := func(want []string) {
		t.Helper()
		s := e.mustBuild(t, goldenStamp)
		var f published.Board
		decode(t, s.Files, published.BoardPath("harbour"), &f)
		got := make([]string, len(want))
		for _, c := range f.Connections {
			got[c.Order] = c.ID
		}
		if !slices.Equal(got, want) {
			t.Errorf("the choices are in the order %v, want %v", got, want)
		}
		if !slices.IsSortedFunc(f.Connections, func(a, b published.Connection) int { return strings.Compare(a.ID, b.ID) }) {
			t.Error("the connections are not sorted by ID")
		}
		r := loaded(t, s)
		r.Start(bd, start)
		offered := []string{}
		for _, c := range r.Choices() {
			offered = append(offered, c.ID)
		}
		if !slices.Equal(offered, want) {
			t.Errorf("the runtime offers %v, want %v", offered, want)
		}
	}
	check(choices)
	e.apply(t, "connection.reorder", map[string]any{"id": choices[2], "order": "4"})
	check([]string{choices[2], choices[0], choices[1]})
}

// TestBranchOutputOrder checks that a branch's outputs are published in the
// order of their evaluation, which reordering them changes, each of the kind
// of its place, and that the reference runtime, which runs the conformance
// suite, takes the first that holds, which the reorder changes.
//
// Verifies: FR-EDIT-04/AC2
func TestBranchOutputOrder(t *testing.T) {
	e := newEnv(t, "Branches")
	bd, br, x, y := id(ids.Board), id(ids.Node), id(ids.Node), id(ids.Node)
	o1, o2 := id(ids.BranchOutput), id(ids.BranchOutput)
	end := func(n string, at int) map[string]any {
		return map[string]any{"id": n, "board_id": bd, "type": "element", "kind": "narration",
			"position": map[string]any{"x": 200, "y": at}, "is_ending": true}
	}
	e.apply(t,
		"board.create", map[string]any{"id": bd, "name": "Harbour"},
		"node.create", map[string]any{"id": br, "board_id": bd, "type": "branch",
			"position": map[string]any{"x": 0, "y": 0}},
		"node.create", end(x, 0), "node.create", end(y, 100),
		"branch_output.create", map[string]any{"id": o1, "node_id": br, "condition": "true", "order": "8"},
		"branch_output.create", map[string]any{"id": o2, "node_id": br, "condition": "1 < 2", "order": "V"},
		"connection.create", map[string]any{"id": id(ids.Connection), "from_node_id": br, "from_port": o1,
			"to_node_id": x},
		"connection.create", map[string]any{"id": id(ids.Connection), "from_node_id": br, "from_port": o2,
			"to_node_id": y},
	)
	check := func(first, second, taken string) {
		t.Helper()
		s := e.mustBuild(t, goldenStamp)
		var f published.Board
		decode(t, s.Files, published.BoardPath("harbour"), &f)
		var outputs []published.BranchOutput
		for _, n := range f.Nodes {
			if b, ok := n.(*published.Branch); ok {
				outputs = b.Outputs
			}
		}
		if len(outputs) != 2 || outputs[0].ID != first || outputs[0].Kind != published.OutputIf ||
			outputs[0].Order != 0 || outputs[1].ID != second || outputs[1].Kind != published.OutputElseIf ||
			outputs[1].Order != 1 {
			t.Errorf("the outputs are %+v, want %s if and %s elseif", outputs, first, second)
		}
		r := loaded(t, s)
		r.Start(bd, br)
		if at, _ := r.Current(); at != taken {
			t.Errorf("the flow is at %s, want %s", at, taken)
		}
	}
	check(o1, o2, x)
	e.apply(t, "branch_output.reorder", map[string]any{"id": o2, "order": "4"})
	check(o2, o1, y)
}

// TestBuildLeavesTheTrashOut checks that what is in the project's trash is
// in no file: a deleted node, with its connection, and a deleted board, with
// its file, its nodes, and its variables; and that an expression that does
// not compile once a variable that it reads is forced out is stood in for
// in the draft's files, as false, and refused in a release's, so that no
// release holds an expression that did not compile.
//
// Verifies: FR-EXPR-03/AC1
func TestBuildLeavesTheTrashOut(t *testing.T) {
	e := newEnv(t, "Trash")
	b1, b2, gold, local := id(ids.Board), id(ids.Board), id(ids.Variable), id(ids.Variable)
	a, gone, other, c := id(ids.Node), id(ids.Node), id(ids.Node), id(ids.Connection)
	node := func(n, b string, ending bool) map[string]any {
		return map[string]any{"id": n, "board_id": b, "type": "element", "kind": "narration",
			"position": map[string]any{"x": 0, "y": 0}, "is_ending": ending}
	}
	e.apply(t,
		"board.create", map[string]any{"id": b1, "name": "One"},
		"board.create", map[string]any{"id": b2, "name": "Two"},
		"variable.create", map[string]any{"id": gold, "key": "gold", "type": "int", "scope": "global", "default": 0},
		"variable.create", map[string]any{"id": local, "key": "local", "type": "int", "scope": "board",
			"board_id": b2, "default": 0},
		"node.create", node(a, b1, false), "node.create", node(gone, b1, true), "node.create", node(other, b2, true),
		"connection.create", map[string]any{"id": c, "from_node_id": a, "to_node_id": gone, "condition": "gold > 1"},
		"node.create", node(id(ids.Node), b1, true),
	)
	e.apply(t, "node.delete", map[string]any{"id": gone}, "board.delete", map[string]any{"id": b2})
	s := e.mustBuild(t, goldenStamp)
	for _, held := range []string{gone, c, b2, other, local} {
		for p, data := range s.Files {
			if bytes.Contains(data, []byte(held)) {
				t.Errorf("%s holds %s, which is in the trash", p, held)
			}
		}
	}
	if _, ok := s.Files[published.BoardPath("two")]; ok {
		t.Error("the board in the trash has a file")
	}
	// The connection back, and the variable that its condition reads
	// forced out, which leaves the condition unable to compile.
	e.apply(t, "node.restore", map[string]any{"id": gone})
	e.apply(t, "variable.delete", map[string]any{"id": gold, "force": true})
	_, err := e.build(t, goldenStamp)
	var unpublishable *serializer.UnpublishableError
	if !errors.As(err, &unpublishable) || len(unpublishable.StandIns) != 1 ||
		unpublishable.StandIns[0].Kind != serializer.StandInExpression || unpublishable.StandIns[0].Entity != c {
		t.Fatalf("the release is %v, want refused for the condition of %s", err, c)
	}
	s = e.mustBuild(t, goldenDraftStamp)
	var f published.Board
	decode(t, s.Files, published.BoardPath("one"), &f)
	if len(f.Connections) != 1 || f.Connections[0].Condition == nil || f.Connections[0].Condition.Source != "gold > 1" {
		t.Fatalf("the connections are %+v", f.Connections)
	}
	if data, err := json.Marshal(f.Connections[0].Condition); err != nil || !strings.Contains(string(data),
		`"ast":{"kind":"lit","type":"bool","value":false}`) {
		t.Errorf("the condition is %s, want false: %v", data, err)
	}
	if !slices.Equal(s.StandIns, unpublishable.StandIns) {
		t.Errorf("the draft stood in for %v, want %v", s.StandIns, unpublishable.StandIns)
	}
}
