package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/authoring"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/playstate"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

// importRandomSeeds is how many random projects TestImportRoundTripRandom
// exports and imports.
const importRandomSeeds = 24

// importChurn are the types of command that a project's last batches are
// mostly of: what leaves a few entities with others deleted around them, as
// an else older than every conditional output that its branch has left, which
// the import's order of commands has to take as it finds it.
var importChurn = []string{"branch_output.create", "branch_output.create", "branch_output.create",
	"branch_output.create", "branch_output.create", "branch_output.delete", "branch_output.delete",
	"branch_output.delete", "branch_output.restore", "branch_output.restore", "branch_output.reorder", "node.create",
	"node.delete", "node.restore", "connection.create", "connection.delete", "connection.restore", "folder.delete",
	"folder.restore", "folder.move", "board.delete", "board.restore", "variable.delete", "variable.restore",
	"component.delete", "component.restore", "component_type.delete", "component_type.restore", "attribute.delete",
	"attribute.restore", "comment.delete", "comment.restore", "play_state.delete", "play_state.restore"}

// TestImportRoundTripRandom checks that a project's export can always be
// imported, whatever edits, deletions, undos, and redos left its draft as it
// is (FR-DATA-01 AC1): for each seed, the random batches of the property test
// of undo (undoGenerators) make a project, a client undoes and redoes some of
// them, and the project's export, imported with fresh IDs, exports as the
// same data. What the format does not hold is left out of the comparison:
// what is in the trash, of which a text or a play state may still name an ID,
// and the order of a stored document's keys. Some seed reaches the state that
// an import once refused, an else older than its branch's conditional outputs.
//
// Verifies: FR-DATA-01/AC1
func TestImportRoundTripRandom(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped with -short: needs PostgreSQL, which the tests run in Docker")
	}
	t.Parallel()
	var mu sync.Mutex
	reached := false
	t.Run("seeds", func(t *testing.T) {
		for seed := range uint64(importRandomSeeds) {
			t.Run(fmt.Sprint(seed+1), func(t *testing.T) {
				t.Parallel()
				older := importRoundTripOf(t, seed+1)
				mu.Lock()
				defer mu.Unlock()
				reached = reached || older
			})
		}
	})
	if !t.Failed() && !reached {
		t.Error("no seed left an else older than its branch's conditional outputs")
	}
}

// importRoundTripOf makes the seed's project and takes it through an export
// and an import (wantImportRoundTrip). It returns whether the project had an
// else older than its branch's conditional outputs.
func importRoundTripOf(t *testing.T, seed uint64) bool {
	r := randomProject(t, seed)
	before, _ := wantImportRoundTrip(t, r)
	return elseBeforeConditions(t, before)
}

// TestImportKeepsStoredTexts checks that a project whose stored texts break
// the text rules of the commands that set them, as a newer rule leaves them
// (commands.Spec.CheckNew), still imports from its export, each text as it is:
// the project's, and those of its folders, boards, variables, component types,
// attribute definitions, components, and nodes, defaults, values, and an
// enum's options among them.
//
// Verifies: FR-DATA-01/AC1
func TestImportKeepsStoredTexts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped with -short: needs PostgreSQL, which the tests run in Docker")
	}
	t.Parallel()
	r := randomProject(t, 1)
	mark := string(rune(0x202e))
	for _, sql := range []string{
		`UPDATE project SET name = name || $2, description = ' ' || description || $2 WHERE id = $1`,
		`UPDATE folder SET name = name || $2 WHERE project_id = $1`,
		`UPDATE board SET name = name || $2, description = description || $2 WHERE project_id = $1`,
		`UPDATE variable SET description = description || $2 WHERE project_id = $1`,
		`UPDATE variable SET default_value = to_jsonb((default_value #>> '{}') || $2)
			WHERE project_id = $1 AND type = 'string'`,
		`UPDATE component_type SET name = name || $2 WHERE project_id = $1`,
		`UPDATE attribute_def SET name = name || $2 WHERE project_id = $1`,
		`UPDATE attribute_def SET default_value = to_jsonb((default_value #>> '{}') || $2)
			WHERE project_id = $1 AND type IN ('string', 'text')`,
		`UPDATE attribute_def SET default_value = to_jsonb((default_value #>> '{}') || $2),
			enum_options = (SELECT array_agg(o || $2) FROM unnest(enum_options) AS o)
			WHERE project_id = $1 AND type = 'enum'`,
		`UPDATE component SET name = name || $2 WHERE project_id = $1`,
		`UPDATE attribute_value v SET value = to_jsonb((v.value #>> '{}') || $2) FROM attribute_def a
			WHERE v.project_id = $1 AND a.id = v.attribute_def_id AND a.type IN ('string', 'text', 'enum')`,
		`UPDATE node SET title = 'Title' || $2, tags = ARRAY['tag' || $2] WHERE project_id = $1`,
	} {
		tag, err := r.conn.Exec(context.Background(), sql, r.uuid, mark)
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		if tag.RowsAffected() == 0 {
			t.Errorf("the seed's project has no row for %s", sql)
		}
	}
	before, _ := wantImportRoundTrip(t, r)
	var held authoring.Project
	if err := json.Unmarshal(before["draft/project.json"], &held); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(held.Name, mark) {
		t.Errorf("the export holds the project's name as %q", held.Name)
	}
}

// TestImportKeepsStoredRowTexts checks that a project whose mute, play state,
// comment, or release holds a stored text that the rules of its commands now
// refuse still imports from its export, with the text as it is, and that what
// the import wrote reads back. TestImportHoldsDirectRowsToTheirChecks checks
// what such a row is still held to.
//
// Verifies: FR-DATA-01/AC1
func TestImportKeepsStoredRowTexts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped with -short: needs PostgreSQL, which the tests run in Docker")
	}
	t.Parallel()
	mark, blank := string(rune(0x202e)), string(rune(0x200b))
	value := ids.Encode(ids.Variable, ids.New())
	for name, c := range map[string]struct {
		store []string
		args  []any
		read  func(t *testing.T, files map[string][]byte) string
		want  string
	}{
		"a mute's reason": {
			store: []string{`UPDATE validation_mute SET reason = 'Intended' || $2 WHERE project_id = $1`},
			args:  []any{mark},
			read: func(t *testing.T, files map[string][]byte) string {
				return items[authoring.ValidationMute](t, files, "draft/validation_mutes.json")[0].Reason
			},
			want: "Intended" + mark,
		},
		"a play state's name": {
			store: []string{`UPDATE play_state SET name = 'Quay' || $2 WHERE project_id = $1`},
			args:  []any{mark},
			read: func(t *testing.T, files map[string][]byte) string {
				return items[authoring.PlayState](t, files, "play_states.json")[0].Name
			},
			want: "Quay" + mark,
		},
		"a play state's names": {
			store: []string{`UPDATE play_state SET state = jsonb_set(state, '{variables}', jsonb_build_object($3::text, 1)),
				names = jsonb_build_object('variables', jsonb_build_object($3::text, 'gold' || $2)) WHERE project_id = $1`},
			args: []any{mark, value},
			read: func(t *testing.T, files map[string][]byte) string {
				var names playstate.PlayStateNames
				if err := json.Unmarshal(items[authoring.PlayState](t, files, "play_states.json")[0].Names,
					&names); err != nil {
					t.Fatal(err)
				}
				return names.Variables[value]
			},
			want: "gold" + mark,
		},
		"a comment's body": {
			store: []string{`UPDATE comment SET body = $2::jsonb WHERE project_id = $1`},
			args:  []any{string(paragraph(blank))},
			read: func(t *testing.T, files map[string][]byte) string {
				doc, err := richtext.Parse(items[authoring.Comment](t, files, "comments.json")[0].Body)
				if err != nil {
					t.Fatal(err)
				}
				return richtext.PlainText(doc.Segments(richtext.NoNames{}))
			},
			want: blank,
		},
		"a release's note": {
			// A release never changes once it is built, but for this test.
			store: []string{`ALTER TABLE snapshot DISABLE TRIGGER snapshot_stays_built`,
				`UPDATE snapshot SET note = ' padded' || $2 WHERE project_id = $1`,
				`ALTER TABLE snapshot ENABLE TRIGGER snapshot_stays_built`},
			args: []any{mark},
			read: func(t *testing.T, files map[string][]byte) string {
				return items[authoring.Release](t, files, "releases/index.json")[0].Note
			},
			want: " padded" + mark,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newAuthoringFixture(t)
			openMaintenance(t, f.s, f.d)
			sendBatch(t, f.s.main, f.owner, f.project, wireCommand{Type: "validation.mute",
				Payload: map[string]any{"entity_id": f.node, "code": "dead_end", "reason": "Intended"}})
			conn := f.d.Admin(t)
			for _, sql := range c.store {
				args := []any{}
				if strings.Contains(sql, "$1") {
					args = append([]any{decode(t, ids.Project, f.project)}, c.args...)
				}
				tag, err := conn.Exec(t.Context(), sql, args...)
				if err != nil {
					t.Fatalf("%s: %v", sql, err)
				}
				if len(args) > 0 && tag.RowsAffected() == 0 {
					t.Fatalf("no row for %s", sql)
				}
			}
			before, after := wantImportRoundTrip(t, randomDraft{s: f.s, owner: f.owner, ws: f.ws, id: f.project})
			if got := c.read(t, before); got != c.want {
				t.Errorf("the export holds %q, want %q", got, c.want)
			}
			if got := c.read(t, after); got != c.want {
				t.Errorf("the import keeps %q, want %q", got, c.want)
			}
			var m authoring.Manifest
			if err := json.Unmarshal(after[authoring.ManifestPath], &m); err != nil {
				t.Fatal(err)
			}
			state := items[authoring.PlayState](t, after, "play_states.json")[0].ID
			for _, path := range []string{commentsPath(m.Project.ID), playStatesPath(m.Project.ID),
				playStatesPath(m.Project.ID) + "/" + state, snapshotsPath(m.Project.ID)} {
				if res := call(f.s.main, http.MethodGet, path, nil, f.owner.header(false)); res.status != http.StatusOK {
					t.Errorf("GET %s: %d %.300s", path, res.status, res.body)
				}
			}
			validationReport(t, f.s.main, f.owner, m.Project.ID)
		})
	}
}

// randomDraft is a project that randomProject made, on a server of its own
// whose workers run.
type randomDraft struct {
	s     *server
	owner signedIn
	ws    string
	id    string
	uuid  ids.UUID
	conn  *pgx.Conn
}

// randomProject makes a project of the random batches of the seed, which a
// client undoes and redoes some of.
func randomProject(t *testing.T, seed uint64) randomDraft {
	t.Helper()
	s, d, owner, ws := inviteServer(t, "open", nil)
	policy := ratelimit.DefaultPolicy()
	policy.Rates[ratelimit.Write].Limits[ratelimit.User] = 100_000
	s.limits = ratelimit.New(readyDB{s.ready}, policy)
	openMaintenance(t, s, d)
	startWorkers(t, s, d)
	p := createProject(t, s.main, owner, ws, "Harbour").ID
	g := &undoGen{r: rand.New(rand.NewPCG(seed, 0x84)), ms: 1_700_000_000_000,
		asset: upload(t, s.main, owner, p, testPNG(t, 8, 8), http.StatusCreated).ID, project: p,
		actor: owner.body.UserID}
	conn := d.Admin(t)
	project := decode(t, ids.Project, p)
	send := func(cmds []wireCommand) response {
		return call(s.main, http.MethodPost, commandsPath(p), commandBatch(t, cmds...), owner.header(true))
	}

	creations := []string{"folder.create", "board.create", "node.create", "node.create", "node.create",
		"connection.create", "connection.create", "variable.create", "variable.create", "component_type.create",
		"attribute.create", "attribute.create", "component.create", "component.create", "branch_output.create",
		"branch_output.create", "play_state.create", "comment.create", "comment.create"}
	for range 40 {
		typ := creations[g.r.IntN(len(creations))]
		if g.r.IntN(3) == 0 {
			typ = g.anyType()
		}
		g.m = readUndoModel(t, conn, project)
		if cmds, ok := g.batch(typ); ok {
			wantApplied(t, send(cmds), false)
		}
	}
	settle := func() {
		for {
			g.m = readUndoModel(t, conn, project)
			cmds, ok := g.batch("")
			if !ok {
				return
			}
			wantApplied(t, send(cmds), true)
		}
	}
	settle()

	// A client's stacks: a new batch clears what it could redo, and it undoes
	// and redoes from the top of each.
	c := &undoClient{t: t, send: send, store: undoRevisions(t, conn, project), open: map[*undoEntry]bool{}}
	var undo, redo []*undoEntry
	round := slices.Sorted(maps.Keys(undoGenerators))
	schedule := slices.Clone(round)
	for range 120 {
		schedule = append(schedule, importChurn[g.r.IntN(len(importChurn))])
	}
	g.r.Shuffle(len(schedule), func(i, j int) { schedule[i], schedule[j] = schedule[j], schedule[i] })
	for _, typ := range schedule {
		g.m = readUndoModel(t, conn, project)
		if cmds, ok := g.batch(typ); ok {
			res, e := c.apply(cmds)
			if wantApplied(t, res, false) && e != nil {
				for _, r := range redo {
					delete(c.open, r)
				}
				undo, redo = append(undo, e), nil
			}
		}
		switch g.r.IntN(6) {
		case 0:
			for range 1 + g.r.IntN(3) {
				if len(undo) > 0 {
					redo = append(redo, c.revert(undo[len(undo)-1]))
					undo = undo[:len(undo)-1]
				}
			}
		case 1:
			if len(redo) > 0 {
				undo = append(undo, c.revert(redo[len(redo)-1]))
				redo = redo[:len(redo)-1]
			}
		}
	}
	settle()
	return randomDraft{s: s, owner: owner, ws: ws, id: p, uuid: project, conn: conn}
}

// wantImportRoundTrip exports the project, imports the archive with fresh IDs,
// exports the import, compares what the two archives say and which entity each
// of their references names (sameReferences), and returns them.
func wantImportRoundTrip(t *testing.T, r randomDraft) (before, after map[string][]byte) {
	t.Helper()
	before = exportOf(t, r.s, r.owner, r.id)
	got := mustImport(t, r.s, r.owner, r.ws, zipOf(t, before), authoring.PolicyRemap)
	after = exportOf(t, r.s, r.owner, got.ProjectID)
	var m authoring.Manifest
	if err := json.Unmarshal(before[authoring.ManifestPath], &m); err != nil {
		t.Fatal(err)
	}
	for _, sec := range m.Sections {
		if sec.Name == "assets" || sec.Name == "releases" {
			continue
		}
		a, b := semantics(t, keysInOrder(t, before[sec.Path])), semantics(t, keysInOrder(t, after[sec.Path]))
		if !slices.Equal(a, b) {
			t.Errorf("the section %s changed:\n%s", sec.Name, firstDifference(a, b))
		}
	}
	// A text or a play state may name what is in the source's trash, which no
	// archive holds, so that the import has no new ID for it: only an ID of
	// something that the archive holds is one that the two may not share.
	held := heldIDs(t, before)
	for _, id := range sharedIDs(before, after) {
		if held[strings.Fields(id)[0]] {
			t.Errorf("the two projects share the ID %s", id)
		}
	}
	if problems := sameReferences(t, before, after); len(problems) > 0 {
		t.Errorf("%d references differ, first: %s", len(problems), strings.Join(problems[:min(len(problems), 5)], "; "))
	}
	return before, after
}

// sameReferences compares the ID tokens of two exports of one project, the
// second imported from the first with fresh IDs, and returns what is wrong
// with them: each token of every section, and of each release's files, has to
// be at the place of its counterpart, and together they have to make one
// bijection from the first export's IDs to the second's, of IDs of one kind,
// under which an ID that the first archive holds becomes another, and one that
// it does not hold, of an entity in the trash, stays as it is. A person's ID
// may stay or become an imported identity's.
//
// Fresh IDs keep the order of those they replace, so that items and arrays keep
// their places. An object keyed by IDs need not: jsonb orders its keys by their
// bytes, and a fresh ID sorts past the unchanged ID of what is in the trash. So
// the keys of such an object are matched through the bijection that the places
// give, rather than by their own places.
func sameReferences(t *testing.T, before, after map[string][]byte) []string {
	t.Helper()
	var m, n authoring.Manifest
	if err := json.Unmarshal(before[authoring.ManifestPath], &m); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after[authoring.ManifestPath], &n); err != nil {
		t.Fatal(err)
	}
	b := &idBijection{forward: map[string]string{}, back: map[string]string{}}
	b.pair("manifest", m.Project.ID, n.Project.ID)
	read := func(name string, files map[string][]byte) (any, bool) {
		data, ok := files[name]
		if !ok {
			b.fail("%s: the export does not hold the file", name)
			return nil, false
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		var doc any
		if err := dec.Decode(&doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return doc, true
	}
	compare := func(from, to string) {
		x, ok := read(from, before)
		y, ok2 := read(to, after)
		if ok && ok2 {
			b.walk(from, x, y)
		}
	}
	for _, sec := range m.Sections {
		compare(sec.Path, sec.Path)
	}
	held := heldIDs(t, before)
	oldReleases, newReleases := items[authoring.Release](t, before, "releases/index.json"),
		items[authoring.Release](t, after, "releases/index.json")
	if len(oldReleases) != len(newReleases) {
		b.fail("%d releases became %d", len(oldReleases), len(newReleases))
	} else {
		for i, rel := range oldReleases {
			for _, name := range slices.Sorted(maps.Keys(before)) {
				if !strings.HasPrefix(name, rel.Path) {
					continue
				}
				if strings.HasSuffix(name, ".json") {
					for _, id := range publicID.FindAllString(string(before[name]), -1) {
						held[id] = true
					}
					compare(name, newReleases[i].Path+strings.TrimPrefix(name, rel.Path))
				}
			}
		}
	}
	b.settle()
	for _, id := range slices.Sorted(maps.Keys(b.forward)) {
		to := b.forward[id]
		switch {
		case strings.HasPrefix(id, "usr_"):
		case held[id] && id == to:
			b.fail("%s, which the archive holds, kept its ID", id)
		case !held[id] && id != to:
			b.fail("%s, which the archive does not hold, became %s", id, to)
		}
	}
	return b.problems
}

// idBijection is the mapping that sameReferences builds, with what it found
// wrong.
type idBijection struct {
	forward, back map[string]string
	// keyed are the objects with IDs for keys, whose keys are matched once the
	// places of the other tokens have given the mapping.
	keyed    []idObject
	problems []string
}

// idObject is an object with IDs for keys, of the first export and the second.
type idObject struct {
	at   string
	x, y map[string]any
}

func (b *idBijection) fail(format string, args ...any) {
	b.problems = append(b.problems, fmt.Sprintf(format, args...))
}

// pair records that the ID x of the first export is the ID y of the second.
func (b *idBijection) pair(at, x, y string) {
	kind := func(id string) string { return id[:strings.IndexByte(id, '_')] }
	if kind(x) != kind(y) {
		b.fail("%s: %s became %s, of another kind", at, x, y)
		return
	}
	if to, ok := b.forward[x]; ok && to != y {
		b.fail("%s: %s is %s here and %s elsewhere", at, x, y, to)
		return
	}
	if from, ok := b.back[y]; ok && from != x {
		b.fail("%s: %s is what both %s and %s became", at, y, x, from)
		return
	}
	b.forward[x], b.back[y] = y, x
}

// pairTokens pairs the IDs that x and y spell, by their places.
func (b *idBijection) pairTokens(at, x, y string) {
	xs, ys := publicID.FindAllString(x, -1), publicID.FindAllString(y, -1)
	if len(xs) != len(ys) {
		b.fail("%s: %d IDs became %d", at, len(xs), len(ys))
		return
	}
	for i := range xs {
		b.pair(at, xs[i], ys[i])
	}
}

// walk pairs the tokens of the JSON values x and y, place by place.
func (b *idBijection) walk(at string, x, y any) {
	switch x := x.(type) {
	case string:
		other, ok := y.(string)
		if !ok {
			b.fail("%s: a string became %T", at, y)
			return
		}
		b.pairTokens(at, x, other)
	case []any:
		other, ok := y.([]any)
		if !ok || len(x) != len(other) {
			b.fail("%s: an array of %d became %T", at, len(x), y)
			return
		}
		for i := range x {
			b.walk(fmt.Sprintf("%s[%d]", at, i), x[i], other[i])
		}
	case map[string]any:
		other, ok := y.(map[string]any)
		if !ok || len(x) != len(other) {
			b.fail("%s: an object of %d keys became %T", at, len(x), y)
			return
		}
		keyed := false
		for _, k := range slices.Sorted(maps.Keys(x)) {
			if publicID.MatchString(k) {
				keyed = true
				continue
			}
			v, ok := other[k]
			if !ok {
				b.fail("%s: the key %s is gone", at, k)
				continue
			}
			b.walk(at+"."+k, x[k], v)
		}
		if keyed {
			b.keyed = append(b.keyed, idObject{at: at, x: x, y: other})
		}
	}
}

// settle matches the keys of the objects keyed by IDs through the mapping, an
// ID that it does not hold as itself, and walks what they hold.
func (b *idBijection) settle() {
	for len(b.keyed) > 0 {
		o := b.keyed[0]
		b.keyed = b.keyed[1:]
		for _, k := range slices.Sorted(maps.Keys(o.x)) {
			if !publicID.MatchString(k) {
				continue
			}
			to := publicID.ReplaceAllStringFunc(k, func(id string) string {
				if fresh, ok := b.forward[id]; ok {
					return fresh
				}
				return id
			})
			v, ok := o.y[to]
			if !ok {
				b.fail("%s: the key %s is not there as %s", o.at, k, to)
				continue
			}
			b.pairTokens(o.at, k, to)
			b.walk(o.at+"."+k, o.x[k], v)
		}
	}
}

// TestSameReferences checks that the comparison of two exports' IDs sees a
// reference that names the wrong entity, which a comparison without IDs cannot,
// and takes an object whose ID keys a remap has reordered.
func TestSameReferences(t *testing.T) {
	t.Parallel()
	id := func(k ids.Kind) string { return ids.Encode(k, ids.New()) }
	p, q, a, b, c, d, gone := id(ids.Project), id(ids.Project), id(ids.Node), id(ids.Node), id(ids.Node), id(ids.Node),
		id(ids.Node)
	archive := func(project string, nodes []string, ref string, visits map[string]int) map[string][]byte {
		manifest, err := json.Marshal(authoring.Manifest{Project: authoring.ManifestProject{ID: project},
			Sections: []authoring.ManifestSection{{Name: "nodes", Path: "draft/nodes.json"},
				{Name: "play_states", Path: "play_states.json"}, {Name: "releases", Path: "releases/index.json"}}})
		if err != nil {
			t.Fatal(err)
		}
		list := []map[string]string{}
		for _, n := range nodes {
			list = append(list, map[string]string{"id": n})
		}
		nodeFile, _ := json.Marshal(map[string]any{"items": list})
		stateFile, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"ref": ref, "visits": visits}}})
		return map[string][]byte{authoring.ManifestPath: manifest, "draft/nodes.json": nodeFile,
			"play_states.json": stateFile, "releases/index.json": []byte(`{"items":[]}`)}
	}
	old := archive(p, []string{a, b}, a, map[string]int{a: 1, gone: 1})
	for name, c := range map[string]struct {
		after map[string][]byte
		ok    bool
	}{
		"each reference follows its entity": {archive(q, []string{c, d}, c, map[string]int{c: 1, gone: 1}), true},
		"a reference to the wrong entity":   {archive(q, []string{c, d}, d, map[string]int{c: 1, gone: 1}), false},
		"a key of the wrong entity":         {archive(q, []string{c, d}, c, map[string]int{d: 1, gone: 1}), false},
		"two entities made one":             {archive(q, []string{c, c}, c, map[string]int{c: 1, gone: 1}), false},
		"an ID that the archive holds kept": {archive(q, []string{a, d}, a, map[string]int{a: 1, gone: 1}), false},
		"an ID in the trash changed":        {archive(q, []string{c, d}, c, map[string]int{c: 1, d: 1}), false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if problems := sameReferences(t, old, c.after); (len(problems) == 0) != c.ok {
				t.Errorf("the problems are %v, want ok %v", problems, c.ok)
			}
		})
	}
}

// elseBeforeConditions says whether a branch of the archive has an else with
// a lower ID than each of its conditional outputs.
func elseBeforeConditions(t *testing.T, files map[string][]byte) bool {
	t.Helper()
	elses, first := map[string]string{}, map[string]string{}
	for _, o := range items[authoring.BranchOutput](t, files, "draft/branch_outputs.json") {
		if o.Kind == "else" {
			elses[o.NodeID] = o.ID
		} else if at, ok := first[o.NodeID]; !ok || o.ID < at {
			first[o.NodeID] = o.ID
		}
	}
	for node, id := range elses {
		if id < first[node] {
			return true
		}
	}
	return false
}

// keysInOrder returns the JSON document data with the keys of each object in
// the order of what they say without their IDs, and then of their values, as
// jsonb orders a stored document's keys by their bytes, which a remap changes,
// and that order says nothing.
func keysInOrder(t *testing.T, data []byte) []byte {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return []byte(orderedJSON(t, doc))
}

// orderedJSON writes v as keysInOrder orders it.
func orderedJSON(t *testing.T, v any) string {
	t.Helper()
	switch v := v.(type) {
	case map[string]any:
		type pair struct{ text, sort string }
		pairs := make([]pair, 0, len(v))
		for k, x := range v {
			text := orderedJSON(t, k) + ":" + orderedJSON(t, x)
			pairs = append(pairs, pair{text, publicID.ReplaceAllString(text, "ID")})
		}
		slices.SortFunc(pairs, func(a, b pair) int { return strings.Compare(a.sort, b.sort) })
		parts := make([]string, len(pairs))
		for i, p := range pairs {
			parts[i] = p.text
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		parts := make([]string, len(v))
		for i, x := range v {
			parts[i] = orderedJSON(t, x)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// heldIDs returns the IDs of the entities that the archive holds: each item
// of its sections, each attribute of a component type, and its project. A
// reference inside a text is no entity, though it is written as an id too.
func heldIDs(t *testing.T, files map[string][]byte) map[string]bool {
	t.Helper()
	var m authoring.Manifest
	if err := json.Unmarshal(files[authoring.ManifestPath], &m); err != nil {
		t.Fatal(err)
	}
	held := map[string]bool{m.Project.ID: true}
	for _, sec := range m.Sections {
		if sec.Name == "people" || sec.Name == "project" {
			continue
		}
		var f struct {
			Items []struct {
				ID         string `json:"id"`
				Attributes any    `json:"attributes"`
			} `json:"items"`
		}
		if err := json.Unmarshal(files[sec.Path], &f); err != nil {
			t.Fatal(err)
		}
		for _, it := range f.Items {
			held[it.ID] = true
			if attrs, ok := it.Attributes.([]any); ok {
				for _, a := range attrs {
					if def, ok := a.(map[string]any); ok {
						id, _ := def["id"].(string)
						held[id] = true
					}
				}
			}
		}
	}
	return held
}

// firstDifference describes where the sorted lists a and b first differ.
func firstDifference(a, b []string) string {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return fmt.Sprintf("%d items and %d, first differing at %d:\nbefore %s\nafter  %s", len(a), len(b), i, a[i],
				b[i])
		}
	}
	return fmt.Sprintf("%d items and %d", len(a), len(b))
}
