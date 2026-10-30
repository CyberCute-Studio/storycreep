package fixture_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency/dependencytest"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/fixture"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// digests are the SHA-256 of each size's batches of seed 1, as digest writes
// them. A change of the generator changes them, and every benchmark's
// fixture with them, so that a benchmark's result is compared with one of
// another project (plan note N52): update them only on purpose, in a pull
// request that says so.
var digests = map[fixture.Size]string{
	fixture.Small:  "e7eadede4b7eb364339647f2eaae0bd838c37d6abf55de53ca96fd1a6b6a180e",
	fixture.Medium: "c852d42c23cef177fbe838443e053ad7c00aab52c12676b324e8ae9a7423cb29",
	fixture.Large:  "a7d9c691189087bc25297d524a0f9f13a19863b1679aded5a83b8d1941a6da19",
	fixture.Dense:  "ead243cbd9e8750764513b9f4667bbc77b7910e98976714be9fbe6049d5f1aac",
}

// digest returns the SHA-256 of the fixture's batches: each command's ID,
// type, and payload, a line each, and an empty line after each batch.
func digest(f *fixture.Fixture) string {
	h := sha256.New()
	for _, b := range f.Batches {
		for _, c := range b {
			fmt.Fprintf(h, "%s %s %s\n", c.ID, c.Type, c.Payload)
		}
		fmt.Fprintln(h)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Verifies: NFR-Q-06
func TestGenerateReproducible(t *testing.T) {
	for _, size := range fixture.Sizes() {
		t.Run(string(size), func(t *testing.T) {
			f, err := fixture.Generate(size, 1)
			if err != nil {
				t.Fatal(err)
			}
			again, err := fixture.Generate(size, 1)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f, again) {
				t.Errorf("two fixtures of seed 1 differ")
			}
			if got := digest(f); got != digests[size] {
				t.Errorf("the batches of seed 1 have the digest %s, want %s: the generator changed what it "+
					"makes, and every benchmark compares its fixture with another's; update digests only on "+
					"purpose", got, digests[size])
			}
			other, err := fixture.Generate(size, 2)
			if err != nil {
				t.Fatal(err)
			}
			if digest(other) == digest(f) {
				t.Errorf("the fixtures of seeds 1 and 2 are the same")
			}
		})
	}
}

// Verifies: NFR-Q-06
func TestGenerateCounts(t *testing.T) {
	for _, size := range fixture.Sizes() {
		t.Run(string(size), func(t *testing.T) {
			f, err := fixture.Generate(size, 1)
			if err != nil {
				t.Fatal(err)
			}
			spec := size.Spec()
			for typ, want := range map[commands.Type]int{board.Create: spec.Boards, node.Create: spec.Nodes,
				variable.Create: spec.Variables, component.Create: spec.Components} {
				if got := f.Count(typ); got != want {
					t.Errorf("%d %s commands, want %d", got, typ, want)
				}
			}
			// The shape that the composition gives (plan note N52), within
			// what chance spreads it by.
			for typ, per100 := range map[commands.Type][2]int{node.CreateOutput: {20, 40},
				connection.Create: {110, 170}} {
				if got := f.Count(typ) * 100 / spec.Nodes; got < per100[0] || got > per100[1] {
					t.Errorf("%d %s commands for each 100 nodes, want %d to %d", got, typ, per100[0], per100[1])
				}
			}
			// A script assigns each runtime attribute, Cast's and Prop's, and
			// a float's default is written with a fraction.
			assigned := map[string]bool{}
			for _, b := range f.Batches {
				for _, c := range b {
					var p struct {
						Type       string          `json:"type"`
						Default    json.RawMessage `json:"default"`
						OnEnter    string          `json:"on_enter"`
						OnExit     string          `json:"on_exit"`
						OnTraverse string          `json:"on_traverse"`
					}
					if err := json.Unmarshal(c.Payload, &p); err != nil {
						t.Fatalf("%s: %v", c.Type, err)
					}
					if c.Type == variable.Create && p.Type == "float" && !bytes.ContainsRune(p.Default, '.') {
						t.Errorf("a float's default is %s, without a fraction", p.Default)
					}
					for _, m := range assignmentRE.FindAllStringSubmatch(p.OnEnter+"; "+p.OnExit+"; "+p.OnTraverse, -1) {
						assigned[m[1]] = true
					}
				}
			}
			for _, attr := range []string{"trust", "mood", "alive", "count"} {
				if !assigned[attr] {
					t.Errorf("no script assigns a component's %s", attr)
				}
			}
		})
	}
}

// assignmentRE finds an assignment to a component's runtime attribute in a
// script, as anchor3.count -= 1.
var assignmentRE = regexp.MustCompile(`\.(trust|mood|alive|count) [-+]?= `)

// idRE finds the public IDs of the entities that a fixture names: alone, as
// a field's value, or in an expression's source, as node("nd_…"), with a
// prefix of two letters, as nd_, or three, as atr_.
var idRE = regexp.MustCompile(`\b[a-z]{2,3}_[0-9A-HJKMNP-TV-Z]{26}\b`)

// TestGenerateWellFormed checks, without a database, what the pipeline
// would refuse a fixture for, so that every size is checked, large among
// them, which only the benchmarks load: every batch fits in a request, every
// entity that a command names was created by an earlier command, or by an
// earlier one of its batch, once each, no expression names a jumper
// (node_ref_jumper), and no jumper targets one (jumper_invalid, FR-DEP-03).
func TestGenerateWellFormed(t *testing.T) {
	for _, size := range fixture.Sizes() {
		t.Run(string(size), func(t *testing.T) {
			f, err := fixture.Generate(size, 1)
			if err != nil {
				t.Fatal(err)
			}
			created := map[string]bool{}
			jumpers := map[string]bool{}
			commandIDs := map[ids.UUID]bool{}
			for i, b := range f.Batches {
				if len(b) == 0 || len(b) > fixture.MaxBatch {
					t.Errorf("batch %d has %d commands, want 1 to %d", i, len(b), fixture.MaxBatch)
				}
				if n := len(fixture.Body(b)); n > api.DefaultMaxBodyBytes {
					t.Errorf("batch %d's request has %d bytes, more than a request may have", i, n)
				}
				for _, c := range b {
					if commandIDs[c.ID] || !c.ID.IsV7() {
						t.Errorf("the command ID %s is not a new UUIDv7", c.ID)
					}
					commandIDs[c.ID] = true
					var payload map[string]any
					if err := json.Unmarshal(c.Payload, &payload); err != nil {
						t.Fatalf("%s: %v", c.Type, err)
					}
					own := ""
					if strings.HasSuffix(string(c.Type), ".create") {
						own, _ = payload["id"].(string)
						if own == "" || created[own] {
							t.Errorf("%s creates %q, which is no new ID", c.Type, own)
						}
						// Else the check below would pass over what names it.
						if !idRE.MatchString(own) {
							t.Errorf("idRE does not find the ID %q of a %s", own, c.Type)
						}
					}
					if c.Type == node.Create && payload["type"] == "jumper" {
						jumpers[own] = true
					}
					if target, ok := payload["target"].(map[string]any); ok && jumpers[target["node_id"].(string)] {
						t.Errorf("the jumper %s targets the jumper %s (jumper_invalid)", payload["id"], target["node_id"])
					}
					for _, field := range []string{"condition", "on_enter", "on_exit", "on_traverse"} {
						source, _ := payload[field].(string)
						for _, ref := range idRE.FindAllString(source, -1) {
							if jumpers[ref] {
								t.Errorf("the %s of a %s names the jumper %s: %s", field, c.Type, ref, source)
							}
						}
					}
					for _, ref := range idRE.FindAllString(string(c.Payload), -1) {
						if ref != own && !created[ref] {
							t.Errorf("%s names %s before it is created: %s", c.Type, ref, c.Payload)
						}
					}
					if own != "" {
						created[own] = true
					}
				}
			}
		})
	}
}

// Verifies: NFR-Q-06
func TestLoadReproducible(t *testing.T) {
	f, err := fixture.Generate(fixture.Small, 1)
	if err != nil {
		t.Fatal(err)
	}
	first := load(t, f)
	second := load(t, f)
	first.wantSpec(t, fixture.Small.Spec())
	a, b := first.content(t), second.content(t)
	for _, key := range slices.Sorted(maps.Keys(a)) {
		if !reflect.DeepEqual(a[key], b[key]) {
			t.Errorf("%s is %v in the first project, and %v in the second", key, a[key], b[key])
		}
	}
	for _, key := range slices.Sorted(maps.Keys(b)) {
		if _, ok := a[key]; !ok {
			t.Errorf("only the second project has %s: %v", key, b[key])
		}
	}
}

// TestLoadSizes loads medium and dense, and checks that each builds the
// project that its spec counts, every expression of which compiles. small is
// TestLoadReproducible's. large is the benchmarks', which load it on every
// pull request (plan note N52): it takes a minute or more to load, which
// every run of the tests would wait for, and TestGenerateWellFormed checks
// what of it a database need not.
func TestLoadSizes(t *testing.T) {
	for _, size := range []fixture.Size{fixture.Medium, fixture.Dense} {
		t.Run(string(size), func(t *testing.T) {
			t.Parallel()
			f, err := fixture.Generate(size, 1)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			l := load(t, f)
			t.Logf("%s: %d batches in %v", size, len(f.Batches), time.Since(start))
			l.wantSpec(t, size.Spec())
		})
	}
}

// loaded is a project of a database of the test's own, and the pipeline of
// every command, which its owner applies batches to it with.
type loaded struct {
	admin    *pgx.Conn
	pipeline *commands.Pipeline
	owner    ids.UUID
	project  ids.UUID
}

// load creates a project in a new database and applies the fixture to it.
func load(t *testing.T, f *fixture.Fixture) loaded {
	t.Helper()
	l := newProject(t, f.Name)
	if err := f.Apply(context.Background(), l.pipeline, commands.Actor{UserID: l.owner}, l.project); err != nil {
		t.Fatal(err)
	}
	return l
}

// newProject creates a project of the name in a new database.
func newProject(t *testing.T, name string) loaded {
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
	return loaded{admin: admin, pipeline: pipeline, owner: owner, project: projectID}
}

// wantSpec fails the test unless the project holds what the spec counts,
// outside the trash, every expression of it compiles, and the index of what
// its content names (package dependency) holds what its expressions and its
// texts name, and is what a recomputation of it from scratch gives
// (FR-DEP-01 AC1).
func (l loaded) wantSpec(t *testing.T, spec fixture.Spec) {
	t.Helper()
	for table, want := range map[string]int{"live_board": spec.Boards, "live_node": spec.Nodes,
		"live_variable": spec.Variables, "live_component": spec.Components} {
		var got int
		if err := l.admin.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE project_id = $1",
			l.project).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%d rows of %s, want %d", got, table, want)
		}
	}
	rows, err := l.admin.Query(context.Background(), `SELECT field, source, error::text FROM expression
		WHERE project_id = $1 AND NOT is_valid ORDER BY source LIMIT 10`, l.project)
	if err != nil {
		t.Fatal(err)
	}
	var field, source, problem string
	if _, err := pgx.ForEachRow(rows, []any{&field, &source, &problem}, func() error {
		t.Errorf("the %s %q does not compile: %s", field, source, problem)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var expressions, texts int
	if err := l.admin.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE expression_id IS NOT NULL),
		count(*) FILTER (WHERE expression_id IS NULL) FROM expression_ref WHERE project_id = $1`,
		l.project).Scan(&expressions, &texts); err != nil {
		t.Fatal(err)
	}
	if expressions == 0 || texts == 0 {
		t.Errorf("the index holds %d rows of expressions and %d of texts, want some of each", expressions, texts)
	}
	drift, err := dependencytest.Drift(context.Background(), l.admin, l.project)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) > 0 {
		t.Errorf("the index of what the project names is not what its content names: %q", drift[:min(len(drift), 10)])
	}
}

// contentTables are the tables of a project's content, each with what names
// a row of it in either project: its ID, or what the server made it of, for
// a row whose ID the server makes, as a default component type's.
var contentTables = []struct{ table, key string }{
	{"project", "'project'"},
	{"folder", "id::text"},
	{"board", "id::text"},
	{"variable", "id::text"},
	{"component_type", "key"},
	{"attribute_def", "id::text"},
	{"component", "id::text"},
	{"attribute_value", "component_id::text || '/' || attribute_def_id::text"},
	{"node", "id::text"},
	{"branch_output", "id::text"},
	{"connection", "id::text"},
	{"expression", "coalesce(node_id, connection_id, branch_output_id)::text || '/' || field"},
}

// content returns every row of the project's content, by its table and what
// names it, without what differs between two databases: the IDs that the
// server makes, the workspace's and the project's, and the rows' times and
// authors.
func (l loaded) content(t *testing.T) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, tb := range contentTables {
		where := "project_id = $1"
		if tb.table == "project" {
			where = "id = $1"
		}
		rows, err := l.admin.Query(context.Background(), "SELECT "+tb.key+", to_jsonb(t) FROM "+tb.table+
			" t WHERE "+where, l.project)
		if err != nil {
			t.Fatal(err)
		}
		var key string
		var doc []byte
		if _, err := pgx.ForEachRow(rows, []any{&key, &doc}, func() error {
			var row map[string]any
			d := json.NewDecoder(bytes.NewReader(doc))
			d.UseNumber()
			if err := d.Decode(&row); err != nil {
				return err
			}
			for k := range row {
				if k == "workspace_id" || k == "project_id" || strings.HasSuffix(k, "_at") ||
					strings.HasSuffix(k, "_by") {
					delete(row, k)
				}
			}
			if tb.key != "id::text" {
				delete(row, "id")
			}
			out[tb.table+"/"+key] = row
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return out
}
