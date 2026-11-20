package diff

import (
	"bytes"
	"encoding/json"
	"flag"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
)

var update = flag.Bool("update", false, "rewrite the diff corpus, schema/diff-corpus, from its sets and cases, "+
	"and the diff's JSON Schema from its Go types")

// corpusRoot is the diff corpus in the repository.
var corpusRoot = filepath.Join("..", "..", "..", "schema", "diff-corpus")

// caseJSON is a case of cases.json.
type caseJSON struct {
	Description string          `json:"description"`
	Diff        json.RawMessage `json:"diff"`
	From        string          `json:"from"`
	Name        string          `json:"name"`
	To          string          `json:"to"`
}

// wordsJSON is a case of words.json.
type wordsJSON struct {
	After  string    `json:"after"`
	Before string    `json:"before"`
	Name   string    `json:"name"`
	Words  []WordRun `json:"words"`
}

// layout returns v's JSON laid out as the published format's files are
// (§6.2).
func layout(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := serializer.Layout(data)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// sideJSON returns the side that a diff names of the snapshot of the
// manifest m, as m has it.
func sideJSON(m published.Manifest) map[string]any {
	switch s := m.Snapshot.(type) {
	case *published.ReleaseSnapshot:
		return map[string]any{"contentHash": s.ContentHash, "id": s.ID, "kind": SideSnapshot, "version": s.Version}
	case *published.DraftSnapshot:
		return map[string]any{"contentHash": s.ContentHash, "kind": SideDraft}
	}
	return nil
}

// complete returns the expected diff of c, of the sets whose manifests are
// from and to, with what a case leaves out filled in: its sides and schema
// version, each list and count that is empty or 0, and truncated, false.
func complete(t *testing.T, c corpusCase, from, to published.Manifest) json.RawMessage {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal([]byte(c.expand()), &d); err != nil {
		t.Fatalf("the case %s's diff: %v", c.name, err)
	}
	d["from"], d["to"], d["schemaVersion"], d["truncated"] = sideJSON(from), sideJSON(to), published.SchemaVersion,
		false
	object := func(m map[string]any, key string) map[string]any {
		o, _ := m[key].(map[string]any)
		if o == nil {
			o = map[string]any{}
			m[key] = o
		}
		return o
	}
	fill := func(m map[string]any, v any, keys ...string) {
		for _, k := range keys {
			if _, ok := m[k]; !ok {
				m[k] = v
			}
		}
	}
	boards, _ := d["boards"].([]any)
	if boards == nil {
		boards = []any{}
	}
	for _, b := range boards {
		fill(b.(map[string]any), []any{}, "changes")
		fill(b.(map[string]any), 0, "omitted")
	}
	d["boards"] = boards
	project := object(d, "project")
	fill(project, []any{}, "attributes", "componentTypes", "components", "fields", "variables")
	fill(project, 0, "omitted")
	counts := []string{Added, "cosmetic", Modified, Moved, Removed}
	summary := object(d, "summary")
	fill(summary, 0, counts...)
	kinds := object(summary, "kinds")
	for _, k := range []string{KindAttribute, KindBoard, KindBranchOutput, KindComponent, KindComponentType,
		KindConnection, KindNode, KindProject, KindVariable} {
		fill(object(kinds, k), 0, counts...)
	}
	return layout(t, d)
}

// corpusFiles returns the files of the corpus, by their paths from its root,
// as its sets and cases write them, but its README.md.
func corpusFiles(t *testing.T) map[string][]byte {
	t.Helper()
	sets := buildSets(t)
	out := map[string][]byte{}
	manifests := map[string]published.Manifest{}
	for _, cs := range corpusSets {
		files := sets[cs.name].files(t)
		if err := schemaversion.Validate(files); err != nil {
			t.Fatalf("the set %s does not validate against version 1: %v", cs.name, err)
		}
		manifests[cs.name] = load(t, files).manifest
		for p, data := range files {
			out[path.Join("sets", cs.name, p)] = data
		}
	}
	cases := []caseJSON{}
	for _, c := range corpusCases {
		cases = append(cases, caseJSON{Name: c.name, Description: c.description, From: c.from, To: c.to,
			Diff: complete(t, c, manifests[c.from], manifests[c.to])})
	}
	out["cases.json"] = layout(t, map[string]any{"cases": cases})
	words := []wordsJSON{}
	for _, c := range wordsCases {
		words = append(words, wordsJSON{Name: c.name, Before: c.before, After: c.after, Words: c.words})
	}
	out["words.json"] = layout(t, map[string]any{"cases": words})
	return out
}

// readCorpus returns the corpus's files in the repository, by their paths
// from its root, but its README.md.
func readCorpus(t *testing.T) map[string][]byte {
	t.Helper()
	files := readDir(t, corpusRoot)
	delete(files, "README.md")
	return files
}

// TestCorpusFiles checks that the diff corpus in the repository is what its
// sets and cases write, each set a snapshot that validates against version 1
// of the published format, so that a set's file cannot drift from its
// manifest's hashes, and a case's expected diff from the one written here.
// Run go test ./internal/diff -run TestCorpusFiles -update in server/ to
// write it again.
func TestCorpusFiles(t *testing.T) {
	want := corpusFiles(t)
	if *update {
		if err := os.RemoveAll(filepath.Join(corpusRoot, "sets")); err != nil {
			t.Fatal(err)
		}
		for p, data := range want {
			file := filepath.Join(corpusRoot, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	got := readCorpus(t)
	for _, p := range slices.Sorted(maps.Keys(want)) {
		if data, ok := got[p]; !ok || !bytes.Equal(data, want[p]) {
			t.Errorf("schema/diff-corpus/%s is not what the corpus writes; run go test ./internal/diff -run "+
				"TestCorpusFiles -update", p)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("schema/diff-corpus/%s is no file of the corpus", p)
		}
	}
}

// TestBaseIsTheFixture checks that the corpus's base set, written as its sets
// are, is version 1's fixture project byte for byte, so that the sets are
// written as the serializer writes a snapshot.
func TestBaseIsTheFixture(t *testing.T) {
	fixture := readFixture(t)
	base := load(t, fixture).files(t)
	if !maps.EqualFunc(base, fixture, bytes.Equal) {
		for _, p := range slices.Sorted(maps.Keys(fixture)) {
			if !bytes.Equal(base[p], fixture[p]) {
				t.Errorf("the base set's %s is not the fixture's", p)
			}
		}
	}
}

// corpusSetFiles returns the files of the corpus's set name, as the
// repository holds them, which it reads alone of the corpus.
func corpusSetFiles(t *testing.T, name string) map[string][]byte {
	t.Helper()
	dir := filepath.Join(corpusRoot, "sets", name)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the corpus has no set %s: %v", name, err)
	}
	files := readDir(t, dir)
	if len(files) == 0 {
		t.Fatalf("the corpus has no set %s", name)
	}
	return files
}

// readCases returns the cases of the corpus's cases.json.
func readCases(t *testing.T) []caseJSON {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(corpusRoot, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []caseJSON `json:"cases"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

// sameJSON reports whether a and b are the same JSON value, each number
// compared as the double that it is.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}

// compareCase returns the diff of the corpus's case c, as Compare makes it of
// its sets as the repository holds them, as JSON.
func compareCase(t *testing.T, c caseJSON) (*Diff, []byte) {
	t.Helper()
	d, err := Compare(corpusSetFiles(t, c.From), corpusSetFiles(t, c.To))
	if err != nil {
		t.Fatal(err)
	}
	return d, layout(t, d)
}

// TestCorpus checks that Compare gives the expected diff of every case of the
// corpus, of its sets as the repository holds them, as the Godot addon's diff
// must too (FR-GODOT-03 AC7), each number compared as the double that it is.
//
// Verifies: FR-SNAP-04/AC4
func TestCorpus(t *testing.T) {
	cases := readCases(t)
	if len(cases) != len(corpusCases) {
		t.Fatalf("cases.json has %d cases, and the corpus %d", len(cases), len(corpusCases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			_, got := compareCase(t, c)
			if !sameJSON(t, got, c.Diff) {
				t.Errorf("the diff of %s into %s is\n%s\nwant\n%s", c.From, c.To, got, c.Diff)
			}
		})
	}
}

// caseNamed returns the corpus's case name.
func caseNamed(t *testing.T, name string) caseJSON {
	t.Helper()
	for _, c := range readCases(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("the corpus has no case %s", name)
	return caseJSON{}
}

// TestRenamedVariable checks that a renamed variable is modified on its key,
// not removed and added, as the corpus's case renamed-variable has it.
//
// Verifies: FR-SNAP-04/AC1
func TestRenamedVariable(t *testing.T) {
	d, _ := compareCase(t, caseNamed(t, "renamed-variable"))
	k := d.Summary.Kinds.Variable
	if k.Added != 0 || k.Removed != 0 || k.Modified != 1 {
		t.Errorf("the variables' counts are %+v, want one modified, none added or removed", k)
	}
	want := []FieldChange{{Field: "key", Before: json.RawMessage(`"met_elara"`), After: json.RawMessage(`"met_lara"`)}}
	if len(d.Project.Variables) != 1 || d.Project.Variables[0].ID != v1 ||
		d.Project.Variables[0].Change != Modified || !reflect.DeepEqual(d.Project.Variables[0].Fields, want) {
		t.Errorf("the variables' changes are %+v, want %s modified on its key alone", d.Project.Variables, v1)
	}
}

// TestMovedNode checks that a node that the two sides have on different
// boards is moved, with the board that it was on and the one that it is on,
// not removed and added, as the corpus's case moved has it.
//
// Verifies: FR-SNAP-04/AC2
func TestMovedNode(t *testing.T) {
	d, _ := compareCase(t, caseNamed(t, "moved"))
	if k := d.Summary.Kinds.Node; k.Added != 0 || k.Removed != 0 || k.Moved != 2 {
		t.Errorf("the nodes' counts are %+v, want two moved, none added or removed", k)
	}
	if len(d.Boards) != 1 || d.Boards[0].Board.ID != b2 {
		t.Fatalf("the boards that changed are %+v, want the board that the nodes moved to alone", d.Boards)
	}
	for _, c := range d.Boards[0].Changes {
		if c.Change != Moved || c.FromBoard == nil || c.FromBoard.ID != b3 || c.ToBoard == nil || c.ToBoard.ID != b2 {
			t.Errorf("%s is %s from %+v to %+v, want moved from %s to %s", c.ID, c.Change, c.FromBoard, c.ToBoard,
				b3, b2)
		}
	}
}

// TestMovedBranchOutputs checks that the branch outputs of a branch that
// moved to another board are not moved themselves: one whose own fields
// changed is modified, on the board that the branch is on, and the others
// are no change.
func TestMovedBranchOutputs(t *testing.T) {
	from, to := load(t, corpusSetFiles(t, "base")), load(t, corpusSetFiles(t, "base"))
	branch, i := to.node(t, b1, n2)
	intro, gate := to.board(t, b1), to.board(t, b2)
	intro.Nodes = slices.Delete(intro.Nodes, i, i+1)
	gate.Nodes = append(gate.Nodes, branch)
	br := branch.(*published.Branch)
	for i := range br.Outputs {
		if br.Outputs[i].ID == q1 {
			br.Outputs[i].Condition = to.condition(t, "coins > 0", b1)
		}
	}
	d, err := Compare(from.files(t), to.files(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Boards) != 1 || d.Boards[0].Board.ID != b2 || len(d.Boards[0].Changes) != 2 {
		t.Fatalf("the changes are %+v, want the branch moved and one output modified, on %s", d.Boards, b2)
	}
	node, output := d.Boards[0].Changes[0], d.Boards[0].Changes[1]
	if node.ID != n2 || node.Change != Moved || node.FromBoard.ID != b1 || node.ToBoard.ID != b2 ||
		len(node.Fields) != 0 {
		t.Errorf("the branch's change is %+v, want moved from %s to %s with no field", node, b1, b2)
	}
	if output.ID != q1 || output.Kind != KindBranchOutput || output.Change != Modified || output.FromBoard != nil ||
		len(output.Fields) != 1 || output.Fields[0].Field != "condition" {
		t.Errorf("the output's change is %+v, want %s modified on its condition alone", output, q1)
	}
	if k := d.Summary.Kinds.BranchOutput; k != (ChangeCounts{Modified: 1}) || d.Summary.Moved != 1 {
		t.Errorf("the summary is %+v, want one node moved and one output modified", d.Summary)
	}
}

// TestReparentedBranchOutput checks that a branch output that the to side has
// on another branch, of another board, whose own branch did not move, is not
// moved either: it is modified, on its branch and on each other field of it
// that differs, and listed on the board that the to side has it on, of a pair
// of snapshots that validate (schema/diff-corpus/README.md).
func TestReparentedBranchOutput(t *testing.T) {
	from, to := load(t, corpusSetFiles(t, "base")), load(t, corpusSetFiles(t, "base"))
	n40, k5 := mn("nd", "N40"), mn("cn", "K5")
	branch, _ := to.node(t, b1, n2)
	br := branch.(*published.Branch)
	var output published.BranchOutput
	br.Outputs = slices.DeleteFunc(br.Outputs, func(o published.BranchOutput) bool {
		if o.ID == q1 {
			output = o
		}
		return o.ID == q1
	})
	for i := range br.Outputs {
		br.Outputs[i].Order = published.Int(i)
	}
	output.Kind, output.Order = published.OutputIf, 0
	intro, gate := to.board(t, b1), to.board(t, b2)
	gate.Nodes = append(gate.Nodes, &published.Branch{ID: n40, Outputs: []published.BranchOutput{output}})
	// The connection that leaves the output leaves it on its new board.
	var leaves published.Connection
	intro.Connections = slices.DeleteFunc(intro.Connections, func(c published.Connection) bool {
		if c.ID == k5 {
			leaves = c
		}
		return c.ID == k5
	})
	leaves.From, leaves.To = n40, n10
	gate.Connections = append(gate.Connections, leaves)
	files := to.files(t)
	if err := schemaversion.Validate(files); err != nil {
		t.Fatalf("the snapshot with the output on another board's branch does not validate: %v", err)
	}
	d, err := Compare(from.files(t), files)
	if err != nil {
		t.Fatal(err)
	}
	var got *EntityChange
	for _, g := range d.Boards {
		for i, c := range g.Changes {
			if c.ID == q1 {
				if g.Board.ID != b2 {
					t.Errorf("%s is listed on %s, want %s, the board that the to side has it on", q1, g.Board.ID, b2)
				}
				got = &g.Changes[i]
			}
		}
	}
	if got == nil {
		t.Fatalf("the diff has no change of %s: %+v", q1, d.Boards)
	}
	var names []string
	for _, f := range got.Fields {
		names = append(names, f.Field)
	}
	if got.Change != Modified || got.FromBoard != nil || got.ToBoard != nil || got.Branch != n40 ||
		!slices.Equal(names, []string{"branch", "kind", "order"}) {
		t.Errorf("the output's change is %+v, want modified on its branch, kind, and order, named by %s", got, n40)
	}
	if k := d.Summary.Kinds.BranchOutput; k != (ChangeCounts{Modified: 2}) {
		t.Errorf("the branch outputs' counts are %+v, want two modified, the other on its order, and none moved", k)
	}
}

// TestNegativeZeroPosition checks that a node's position whose coordinate is
// written -0, which encoding/json writes back as -0, is the same as one of 0,
// as a consumer reads the two as one double (schema/diff-corpus/README.md).
func TestNegativeZeroPosition(t *testing.T) {
	files := corpusSetFiles(t, "base")
	negative, err := schemaversion.Edit(files, func(p string, file map[string]any) error {
		if p == published.BoardPath("harbour-intro") {
			position := member(file, "nodes", n5)["position"].(map[string]any)
			position["x"], position["y"] = json.Number("-0"), json.Number("-0.0")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if negative, err = schemaversion.Seal(negative); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(negative[published.BoardPath("harbour-intro")], []byte(`"x": -0,`)) {
		t.Fatal("the edited board's file does not write the position's -0")
	}
	for _, pair := range [][2]map[string][]byte{{files, negative}, {negative, files}} {
		d, err := Compare(pair[0], pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if d.Summary != (DiffSummary{}) || len(d.Boards) != 0 {
			t.Errorf("a position of -0 against one of 0 is %+v, want no change", d.Boards)
		}
	}
}

// TestCompareRefusesABoardTwice checks that Compare fails on a manifest that
// names a board's ID twice, as on any entity's ID that it has twice.
func TestCompareRefusesABoardTwice(t *testing.T) {
	files := corpusSetFiles(t, "base")
	twice, err := schemaversion.Edit(files, func(p string, file map[string]any) error {
		if p == published.ManifestPath {
			boards := file["boards"].([]any)
			again := maps.Clone(boards[0].(map[string]any))
			again["file"] = boards[1].(map[string]any)["file"]
			file["boards"] = append(boards, again)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compare(files, twice); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("Compare of a manifest that names a board twice: %v, want an error", err)
	}
}

// TestCompareSized checks that CompareSized gives the diff that Compare's
// Truncate does, of every case of the corpus and every limit, and that it
// computes no word-level diff of a change that it leaves out.
func TestCompareSized(t *testing.T) {
	for _, c := range readCases(t) {
		from, to := corpusSetFiles(t, c.From), corpusSetFiles(t, c.To)
		whole, err := Compare(from, to)
		if err != nil {
			t.Fatal(err)
		}
		for limit := 1; limit <= len(changesOf(whole))+1; limit++ {
			want, err := whole.Truncate(limit)
			if err != nil {
				t.Fatal(err)
			}
			got, err := CompareSized(from, to, limit)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s, limit %d: CompareSized gives\n%+v\nwant\n%+v", c.Name, limit, got, want)
			}
		}
	}
	// The case texts changes two elements' texts and then two connections'
	// labels, of which a limit of 1 keeps the first alone.
	c := caseNamed(t, "texts")
	d, err := compareFiles(corpusSetFiles(t, c.From), corpusSetFiles(t, c.To))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Truncate(1); err != nil {
		t.Fatal(err)
	}
	kept, left := d.Boards[0].Changes[0].Fields[0], d.Boards[0].Changes[1].Fields[0]
	if kept.Words == nil || kept.texts != nil || left.Words != nil || left.texts == nil {
		t.Errorf("of the text kept and the label left out, the words are %+v and %+v, want the kept one's alone",
			kept.Words, left.Words)
	}
}

// TestCountKinds checks that the summary counts a change of each kind in that
// kind's counts, and refuses a kind that is none of them.
func TestCountKinds(t *testing.T) {
	var s DiffSummary
	kinds := []string{KindAttribute, KindBoard, KindBranchOutput, KindComponent, KindComponentType, KindConnection,
		KindNode, KindProject, KindVariable}
	for _, k := range kinds {
		s.count(k, Added, false)
	}
	for _, k := range kinds {
		if got := *s.Kinds.of(k); got != (ChangeCounts{Added: 1}) {
			t.Errorf("the counts of %s are %+v, want one added", k, got)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("the summary counts a kind that is none")
		}
	}()
	s.count("varaible", Added, false)
}

// TestWordsCorpus checks that Words gives the runs of every case of the
// corpus's words.json, as the Godot addon's word diff must too, and that the
// equal and delete runs of each make its before text, and the equal and
// insert runs its after text.
//
// Verifies: FR-SNAP-04/AC4
func TestWordsCorpus(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(corpusRoot, "words.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []wordsJSON `json:"cases"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) != len(wordsCases) {
		t.Fatalf("words.json has %d cases, and the corpus %d", len(f.Cases), len(wordsCases))
	}
	for _, c := range f.Cases {
		got := Words(c.Before, c.After)
		if !reflect.DeepEqual(got, c.Words) {
			t.Errorf("%s: the words of %q into %q are %+v, want %+v", c.Name, c.Before, c.After, got, c.Words)
		}
		var before, after strings.Builder
		for _, r := range got {
			if r.Op != Insert {
				before.WriteString(r.Text)
			}
			if r.Op != Delete {
				after.WriteString(r.Text)
			}
		}
		if before.String() != c.Before || after.String() != c.After {
			t.Errorf("%s: the runs make %q into %q", c.Name, before.String(), after.String())
		}
	}
}

// TestSchemaFile checks that the diff's JSON Schema in the repository is the
// one that Schema generates from its types, so that the documented diff
// cannot drift from the one that the server writes, that its $id names its
// version, and that every expected diff of the corpus validates against it.
// Run go test ./internal/diff -run TestSchemaFile -update in server/ to write
// it again.
//
// Verifies: FR-SNAP-04/AC4, FR-DATA-04/AC3
func TestSchemaFile(t *testing.T) {
	file := filepath.Join("..", "..", "..", filepath.FromSlash(SchemaPath))
	want := Schema()
	if *update {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what Schema generates; run go test ./internal/diff -run TestSchemaFile -update",
			SchemaPath)
	}
	if !strings.Contains(SchemaID, "/v1/") || !strings.HasSuffix(SchemaID, SchemaPath) {
		t.Errorf("the schema's $id %s does not name the version at %s", SchemaID, SchemaPath)
	}
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddResource(SchemaID, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(SchemaID)
	if err != nil {
		t.Fatal(err)
	}
	for _, cs := range readCases(t) {
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(cs.Diff))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(v); err != nil {
			t.Errorf("the case %s's diff does not validate against the schema: %v", cs.Name, err)
		}
	}
}
