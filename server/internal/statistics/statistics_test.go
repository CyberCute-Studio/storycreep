package statistics_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/fixture"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/search"
	"github.com/CyberCute-Studio/storycreep/server/internal/statistics"
	"github.com/CyberCute-Studio/storycreep/server/internal/statistics/statisticstest"
	"github.com/CyberCute-Studio/storycreep/server/internal/trash"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// registry returns the registry of the server's types of command, with the
// derivation of the statistics if counted is true, which a pipeline of
// content that predates the statistics lacks.
func registry(counted bool) *commands.Registry {
	reg := commands.NewRegistry()
	project.Register(reg)
	board.Register(reg)
	variable.Register(reg)
	component.Register(reg)
	node.Register(reg)
	connection.Register(reg)
	expression.Register(reg)
	dependency.Register(reg)
	validation.Register(reg)
	search.Register(reg)
	if counted {
		statistics.Register(reg)
	}
	return reg
}

// TestNameNeutral checks that each type of command that the derivation
// passes over the changes of names of is a registered type, or the name of
// the derivation that compiles expressions, and that each that changes some
// of a component's names alone is a registered type, which the first list
// does not hold.
func TestNameNeutral(t *testing.T) {
	types := registry(true).Types()
	neutral, naming := statistics.NameNeutral()
	for _, typ := range neutral {
		if !slices.Contains(types, typ) && typ != commands.Type(expression.Name) {
			t.Errorf("nameNeutral lists %s, which is neither a registered type of command nor %s", typ,
				expression.Name)
		}
	}
	for _, typ := range naming {
		if !slices.Contains(types, typ) || slices.Contains(neutral, typ) {
			t.Errorf("componentNaming lists %s, which is not a registered type of command, or which nameNeutral "+
				"lists too", typ)
		}
	}
}

// TestReadNamesNil checks that ReadNames passes over a nil mention and a nil
// value of a document that a program built, as Doc.Segments does, which
// counts the document: it names nothing, so it reads nothing, and counts as
// nothing.
func TestReadNamesNil(t *testing.T) {
	doc := &richtext.Doc{Blocks: []richtext.Block{&richtext.Paragraph{Inlines: []richtext.Inline{
		&richtext.Text{Text: "Hello"}, (*richtext.Mention)(nil), (*richtext.Value)(nil)}}}}
	names, err := statistics.ReadNames(context.Background(), nil, ids.UUID{}, ids.UUID{}, []*richtext.Doc{doc})
	if err != nil {
		t.Fatal(err)
	}
	if words, characters := statistics.Count(doc, names); words != 1 || characters != 5 {
		t.Errorf("Count = %d words, %d characters, want 1 and 5", words, characters)
	}
}

// uncounted is a project whose content a pipeline without the derivation of
// the statistics made, as content that was there before migration 00030,
// with its owner, and a pipeline of the commands with the derivation.
type uncounted struct {
	db        *database.DB
	admin     *pgx.Conn
	workspace ids.UUID
	project   ids.UUID
	owner     ids.UUID
	counted   *commands.Pipeline
}

// newUncounted returns a project of a new database with the small fixture's
// content, and no row of the statistics.
func newUncounted(t *testing.T) uncounted {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	db, admin := d.Open(t), d.Admin(t)
	owner, err := isolationtest.NewUser(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	ws := ids.New()
	if _, err := admin.Exec(ctx, `INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Fixtures',
		'fixtures', 'unlimited')`, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO membership (id, workspace_id, user_id, role)
		VALUES ($1, $2, $3, 'owner')`, ids.New(), ws, owner); err != nil {
		t.Fatal(err)
	}
	ent := entitlements.New(entitlements.Options{Meter: usage.Meter{}})
	pipeline := commands.New(db, commands.Options{Registry: registry(false), Entitlements: ent})
	projects := project.New(db, project.Options{Entitlements: ent, Pipeline: pipeline, Seed: component.Seed})
	p, err := projects.Create(ctx, owner, ws, project.Settings{Name: "Small"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := ids.Decode(ids.Project, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	f, err := fixture.Generate(fixture.Small, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Apply(ctx, pipeline, commands.Actor{UserID: owner}, projectID); err != nil {
		t.Fatal(err)
	}
	return uncounted{db: db, admin: admin, workspace: ws, project: projectID, owner: owner,
		counted: commands.New(db, commands.Options{Registry: registry(true), Entitlements: ent})}
}

// count returns what the query, of the project, counts.
func (u uncounted) count(t *testing.T, query string) int {
	t.Helper()
	var n int
	if err := u.admin.QueryRow(context.Background(), query, u.project).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// wantDrift fails the test unless the project's rows of the statistics are
// what a recount of its texts gives.
func (u uncounted) wantNoDrift(t *testing.T) {
	t.Helper()
	drift, err := statisticstest.Drift(context.Background(), u.admin, u.project)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) > 0 {
		t.Errorf("the statistics are not what the texts count: %q", drift[:min(len(drift), 5)])
	}
}

// apply applies the commands to the project as its owner, through the
// pipeline with the derivation of the statistics, each of its type and
// payload, in turn, or fails the test.
func (u uncounted) apply(t *testing.T, cmds ...any) {
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
	if _, err := u.counted.Apply(context.Background(), commands.Actor{UserID: u.owner}, u.project,
		batch); err != nil {
		t.Fatal(err)
	}
}

// TestCountProjects checks the job of the statistics: it counts the texts of
// content that has no row, in transactions of a bounded number of texts,
// and records the project's state, after which its runs count nothing; it
// passes over a project whose lock a transaction holds for longer than it
// waits, until a later run; it counts again the rows that a migration marks;
// and a read says that a project's statistics are not complete until it has
// counted them. It changes how many texts a transaction counts, and how long
// the job waits for a lock, so it runs alone.
func TestCountProjects(t *testing.T) {
	ctx := context.Background()
	u := newUncounted(t)
	texts := u.count(t, `SELECT (SELECT count(*) FROM node WHERE project_id = $1)
		+ (SELECT count(*) FROM connection WHERE project_id = $1)`)
	statistics.SetCountRows(t, 100)
	if texts <= 100 {
		t.Fatalf("the project has %d texts, want more than a transaction of the job counts", texts)
	}
	if got := u.count(t, "SELECT count(*) FROM text_statistic WHERE project_id = $1"); got != 0 {
		t.Fatalf("the project has %d rows before the job runs, want none", got)
	}

	// A transaction holds the project's lock for longer than the job waits
	// for it: the job passes over the project.
	lock, err := u.admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, "SELECT 1 FROM project WHERE id = $1 FOR NO KEY UPDATE", u.project); err != nil {
		t.Fatal(err)
	}
	statistics.SetCountLockWait(t, 100*time.Millisecond)
	if n, err := statistics.CountProjects(ctx, u.db); err != nil || n != 0 {
		t.Errorf("CountProjects while a transaction holds the lock for long counted %d texts, %v, want none", n,
			err)
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	statistics.SetCountLockWait(t, time.Minute)

	// A batch of the pipeline with the derivation counts what it changes
	// alone, so the project is not counted whole until the job has run.
	var board string
	if err := u.admin.QueryRow(ctx, "SELECT id::text FROM board WHERE project_id = $1 ORDER BY id LIMIT 1",
		u.project).Scan(&board); err != nil {
		t.Fatal(err)
	}
	boardID, err := ids.ParseUUID(board)
	if err != nil {
		t.Fatal(err)
	}
	nodeID := ids.Encode(ids.Node, ids.New())
	u.apply(t, "node.create", map[string]any{"id": nodeID, "board_id": ids.Encode(ids.Board, boardID),
		"type": "element", "position": map[string]int{"x": 0, "y": 0},
		"text": json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[` +
			`{"type":"text","text":"Counted at once."}]}]}`)})
	if got := u.count(t, "SELECT count(*) FROM text_statistic WHERE project_id = $1"); got != 1 {
		t.Errorf("after a batch, the project has %d rows, want the new node's alone", got)
	}
	if got := u.count(t, fmt.Sprintf(`SELECT words FROM text_statistic WHERE kind = 'node' AND project_id = $1
		AND entity_id = '%s'`, decode(t, ids.Node, nodeID))); got != 3 {
		t.Errorf("the new node's text has %d words, want 3", got)
	}

	n, err := statistics.CountProjects(ctx, u.db)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(texts) {
		t.Errorf("CountProjects counted %d texts, want the %d that had no row", n, texts)
	}
	u.wantNoDrift(t)
	if got := u.count(t, "SELECT count(*) FROM text_statistic_state WHERE project_id = $1 AND version = "+
		fmt.Sprint(statistics.Version)); got != 1 {
		t.Errorf("the project has %d states of the current version, want 1", got)
	}
	if n, err := statistics.CountProjects(ctx, u.db); err != nil || n != 0 {
		t.Errorf("CountProjects once the project is counted counted %d texts, %v, want none", n, err)
	}

	// A migration that marks the rows to be counted again, and deletes the
	// state, has the job count them again.
	for _, s := range []string{`UPDATE text_statistic SET version = 0, words = 0 WHERE project_id = $1`,
		`DELETE FROM text_statistic_state WHERE project_id = $1`} {
		if _, err := u.admin.Exec(ctx, s, u.project); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := statistics.CountProjects(ctx, u.db); err != nil || n != int64(texts)+1 {
		t.Errorf("CountProjects after the rows were marked counted %d texts, %v, want %d", n, err, texts+1)
	}
	u.wantNoDrift(t)
}

// decode returns the UUID of the public ID id of the kind, or fails the test.
func decode(t *testing.T, kind ids.Kind, id string) ids.UUID {
	t.Helper()
	u, err := ids.Decode(kind, id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestPurgeRecounts checks that the trash's purge counts again the texts
// that name what it deletes for good, whose names they lose: a mention of a
// purged component is no word, and a value reference of a purged variable
// is {}, so that the statistics stay what a recount gives.
func TestPurgeRecounts(t *testing.T) {
	ctx := context.Background()
	u := newUncounted(t)
	if _, err := statistics.CountProjects(ctx, u.db); err != nil {
		t.Fatal(err)
	}
	harbour, ghost := ids.Encode(ids.Board, ids.New()), ids.Encode(ids.Node, ids.New())
	prop, elara := ids.Encode(ids.ComponentType, ids.New()), ids.Encode(ids.Component, ids.New())
	gold := ids.Encode(ids.Variable, ids.New())
	u.apply(t,
		"board.create", map[string]any{"id": harbour, "name": "Harbour"},
		"component_type.create", map[string]any{"id": prop, "name": "Prop"},
		"component.create", map[string]any{"id": elara, "component_type_id": prop, "name": "Elara Vance"},
		"variable.create", map[string]any{"id": gold, "key": "gold", "type": "int", "scope": "global"},
		"node.create", map[string]any{"id": ghost, "board_id": harbour, "type": "element",
			"position": map[string]int{"x": 0, "y": 0},
			"text": json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[` +
				`{"type":"text","text":"Ask "},{"type":"mention","attrs":{"id":"` + elara + `"}},` +
				`{"type":"text","text":" for "},{"type":"valueRef","attrs":{"ref":"` + gold + `"}}]}]}`)})
	words := func() (int, int) {
		t.Helper()
		var w, c int
		if err := u.admin.QueryRow(ctx, `SELECT words, characters FROM text_statistic WHERE kind = 'node'
			AND entity_id = $1`, decode(t, ids.Node, ghost)).Scan(&w, &c); err != nil {
			t.Fatal(err)
		}
		return w, c
	}
	// "Ask Elara Vance for {gold}": 5 words, 4 + 11 + 5 + 6 characters.
	if w, c := words(); w != 5 || c != 26 {
		t.Fatalf("the text has %d words and %d characters, want 5 and 26", w, c)
	}
	u.apply(t,
		"component.delete", map[string]any{"id": elara, "force": true},
		"variable.delete", map[string]any{"id": gold, "force": true})
	// The trash keeps their names.
	if w, c := words(); w != 5 || c != 26 {
		t.Errorf("once they are in the trash, the text has %d words and %d characters, want 5 and 26", w, c)
	}
	for _, s := range []string{`UPDATE component SET deleted_at = now() - interval '31 days' WHERE project_id = $1
		AND deleted_at IS NOT NULL`, `UPDATE variable SET deleted_at = now() - interval '31 days'
		WHERE project_id = $1 AND deleted_at IS NOT NULL`} {
		if _, err := u.admin.Exec(ctx, s, u.project); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := trash.Purge(ctx, u.db); err != nil || n < 2 {
		t.Fatalf("Purge deleted %d, %v, want the component and the variable", n, err)
	}
	// "Ask  for {}": 2 words, 4 + 5 + 2 characters.
	if w, c := words(); w != 2 || c != 11 {
		t.Errorf("once they are purged, the text has %d words and %d characters, want 2 and 11", w, c)
	}
	u.wantNoDrift(t)
}

// TestCountsWhatChangesShow checks that a change of a component counts again
// the texts that show what it changed, and no other: a rename those that
// mention the component, a change of its key those that show one of its
// attributes, and the creation of a definition of its type, which changes
// each of the type's components, those that name the new definition alone,
// as a paste's that a text named while the project had none of its ID. The
// test changes the rows of the texts that a change should not count, which
// stay as they are.
func TestCountsWhatChangesShow(t *testing.T) {
	ctx := context.Background()
	u := newUncounted(t)
	if _, err := statistics.CountProjects(ctx, u.db); err != nil {
		t.Fatal(err)
	}
	board := ids.Encode(ids.Board, ids.New())
	prop, elara := ids.Encode(ids.ComponentType, ids.New()), ids.Encode(ids.Component, ids.New())
	trust, loyalty := ids.Encode(ids.AttributeDef, ids.New()), ids.Encode(ids.AttributeDef, ids.New())
	mention, value, later := ids.Encode(ids.Node, ids.New()), ids.Encode(ids.Node, ids.New()),
		ids.Encode(ids.Node, ids.New())
	node := func(id, inline string) map[string]any {
		text := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[` + inline + `]}]}`)
		return map[string]any{"id": id, "board_id": board, "type": "element",
			"position": map[string]int{"x": 0, "y": 0}, "text": text}
	}
	attribute := func(def string) string {
		return `{"type":"valueRef","attrs":{"ref":"` + elara + `","attribute":"` + def + `"}}`
	}
	u.apply(t,
		"board.create", map[string]any{"id": board, "name": "Harbour"},
		"component_type.create", map[string]any{"id": prop, "name": "Prop"},
		"attribute.create", map[string]any{"id": trust, "component_type_id": prop, "name": "Trust", "type": "int",
			"runtime": true},
		"component.create", map[string]any{"id": elara, "component_type_id": prop, "name": "Elara"},
		"node.create", node(mention, `{"type":"mention","attrs":{"id":"`+elara+`"}}`),
		"node.create", node(value, attribute(trust)),
		"node.create", node(later, attribute(loyalty)))
	set := func(node string, words int) {
		t.Helper()
		if _, err := u.admin.Exec(ctx, `UPDATE text_statistic SET words = $1 WHERE kind = 'node' AND entity_id = $2`,
			words, decode(t, ids.Node, node)); err != nil {
			t.Fatal(err)
		}
	}
	want := func(what, node string, words, characters int) {
		t.Helper()
		var w, c int
		if err := u.admin.QueryRow(ctx, `SELECT words, characters FROM text_statistic WHERE kind = 'node'
			AND entity_id = $1`, decode(t, ids.Node, node)).Scan(&w, &c); err != nil {
			t.Fatal(err)
		}
		if w != words || c != characters {
			t.Errorf("%s, the text has %d words and %d characters, want %d and %d", what, w, c, words, characters)
		}
	}
	// "Elara", "{elara.trust}", and "{}", of a definition that the project
	// does not have yet.
	want("at first", mention, 1, 5)
	want("at first", value, 1, 13)
	want("at first", later, 0, 2)

	set(mention, 999)
	set(value, 999)
	u.apply(t, "attribute.create", map[string]any{"id": loyalty, "component_type_id": prop, "name": "Loyalty",
		"type": "int", "runtime": true})
	want("after the creation of a definition", mention, 999, 5)
	want("after the creation of a definition", value, 999, 13)
	want("after the creation of a definition", later, 1, 15)

	u.apply(t, "component.rename", map[string]any{"id": elara, "name": "Elara Vance"})
	want("after the component's rename", mention, 2, 11)
	want("after the component's rename", value, 999, 13)

	set(mention, 999)
	u.apply(t, "component.set_key", map[string]any{"id": elara, "key": "ev"})
	want("after the component's change of key", mention, 999, 11)
	want("after the component's change of key", value, 1, 10)
	want("after the component's change of key", later, 1, 12)

	set(mention, 2)
	u.wantNoDrift(t)
}
