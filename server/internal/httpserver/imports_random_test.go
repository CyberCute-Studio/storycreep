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
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
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
	return elseBeforeConditions(t, wantImportRoundTrip(t, r))
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
	before := wantImportRoundTrip(t, r)
	var held authoring.Project
	if err := json.Unmarshal(before["draft/project.json"], &held); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(held.Name, mark) {
		t.Errorf("the export holds the project's name as %q", held.Name)
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
// exports the import, compares the two archives, and returns the first.
func wantImportRoundTrip(t *testing.T, r randomDraft) map[string][]byte {
	t.Helper()
	before := exportOf(t, r.s, r.owner, r.id)
	got := mustImport(t, r.s, r.owner, r.ws, zipOf(t, before), authoring.PolicyRemap)
	after := exportOf(t, r.s, r.owner, got.ProjectID)
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
	return before
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
