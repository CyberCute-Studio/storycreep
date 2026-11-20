package diff

import (
	"cmp"
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// The file sets of the diff corpus (schema/diff-corpus/sets): base, version
// 1's fixture project, and the others, each base or another set with changes
// that its cases are about, written as the serializer writes a snapshot, each
// with its manifest's index, counts, hashes, and content hash computed again,
// and each validating against version 1 (TestCorpusFiles).

// fixtureDir is version 1's fixture project, the corpus's base.
var fixtureDir = filepath.Join("..", "..", "..", "schema", "fixtures", "v1")

// mn returns the ID of the kind of the prefix whose body is the mnemonic after
// zeros, as the fixture's IDs are, as nd_000000000000000000000000N1.
func mn(prefix, mnemonic string) string {
	return prefix + "_" + strings.Repeat("0", 26-len(mnemonic)) + mnemonic
}

// The fixture's entities that the sets change, and those that they add.
var (
	b1, b2, b3, b4 = mn("bd", "B1"), mn("bd", "B2"), mn("bd", "B3"), mn("bd", "B4")
	n1, n2, n3, n4 = mn("nd", "N1"), mn("nd", "N2"), mn("nd", "N3"), mn("nd", "N4")
	n5, n10, n11   = mn("nd", "N5"), mn("nd", "N10"), mn("nd", "N11")
	n20, n21, n30  = mn("nd", "N20"), mn("nd", "N21"), mn("nd", "N30")
	q1, q2, q3, q4 = mn("bo", "Q1"), mn("bo", "Q2"), mn("bo", "Q3"), mn("bo", "Q4")
	k1, k2, k3     = mn("cn", "K1"), mn("cn", "K2"), mn("cn", "K3")
	k20            = mn("cn", "K20")
	v1, v2, v3, v6 = mn("var", "V1"), mn("var", "V2"), mn("var", "V3"), mn("var", "V6")
	v7, v9, v10    = mn("var", "V7"), mn("var", "V9"), mn("var", "V10")
	t1             = mn("ct", "T1")
	a1, a2, a3     = mn("atr", "A1"), mn("atr", "A2"), mn("atr", "A3")
	c2, c3         = mn("cmp", "C2"), mn("cmp", "C3")
)

// snap is a snapshot of the published format as its types hold it, which a
// set changes and then writes (files).
type snap struct {
	manifest   published.Manifest
	settings   published.Settings
	variables  published.Variables
	types      published.ComponentTypes
	components published.Components
	boards     []*published.Board
	// edit, of a set that holds what the format's types do not write,
	// changes the JSON of each file once the types have written it (files).
	edit func(path string, file map[string]any)
}

// readFixture returns the files of version 1's fixture project, by their
// paths.
func readFixture(t *testing.T) map[string][]byte {
	t.Helper()
	return readDir(t, fixtureDir)
}

// readDir returns the files under the directory root, at any depth, by their
// paths from it, with forward slashes.
func readDir(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// load returns the snapshot whose files are files.
func load(t *testing.T, files map[string][]byte) *snap {
	t.Helper()
	s := &snap{}
	decode := func(path string, v any) {
		t.Helper()
		if err := published.Decode(files[path], v); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	decode(published.ManifestPath, &s.manifest)
	decode(published.SettingsPath, &s.settings)
	decode(published.VariablesPath, &s.variables)
	decode(published.ComponentTypesPath, &s.types)
	decode(published.ComponentsPath, &s.components)
	for _, b := range s.manifest.Boards {
		board := &published.Board{}
		decode(b.File, board)
		s.boards = append(s.boards, board)
	}
	return s
}

// files returns the snapshot's files as the serializer writes them (§6.2),
// with its manifest's boards, counts, entry, hashes, and content hash those
// of the files, and then, of a snapshot with an edit, the files that it
// edits, each laid out as the serializer lays it out, with the manifest's
// hashes and content hash computed again (schemaversion.Edit, Seal).
func (s *snap) files(t *testing.T) map[string][]byte {
	t.Helper()
	// Every array that §6.2 does not order otherwise is sorted by ID.
	byID := func(a, b string) int { return cmp.Compare(a, b) }
	slices.SortFunc(s.variables.Variables, func(a, b published.Variable) int { return byID(a.ID, b.ID) })
	slices.SortFunc(s.types.ComponentTypes, func(a, b published.ComponentType) int { return byID(a.ID, b.ID) })
	slices.SortFunc(s.components.Components, func(a, b published.Component) int { return byID(a.ID, b.ID) })
	for _, b := range s.boards {
		slices.SortFunc(b.Nodes, func(a, b published.Node) int { return byID(a.NodeID(), b.NodeID()) })
		slices.SortFunc(b.Connections, func(a, b published.Connection) int { return byID(a.ID, b.ID) })
	}
	files := map[string][]byte{}
	put := func(path string, v any) {
		t.Helper()
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if files[path], err = serializer.Layout(data); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	put(published.SettingsPath, s.settings)
	put(published.VariablesPath, s.variables)
	put(published.ComponentTypesPath, s.types)
	put(published.ComponentsPath, s.components)
	m := s.manifest
	m.Boards = nil
	m.Counts = published.Counts{Boards: published.Int(len(s.boards)),
		Components: published.Int(len(s.components.Components)), Variables: published.Int(len(s.variables.Variables))}
	boards := slices.Clone(s.boards)
	slices.SortFunc(boards, func(a, b *published.Board) int { return cmp.Compare(a.Board.ID, b.Board.ID) })
	for _, b := range boards {
		path := published.BoardPath(b.Board.Slug)
		put(path, b)
		m.Boards = append(m.Boards, published.ManifestBoard{EntryNodeID: b.Board.EntryNodeID, File: path,
			ID: b.Board.ID, Name: b.Board.Name, Slug: b.Board.Slug})
		m.Counts.Nodes += published.Int(len(b.Nodes))
		m.Counts.Connections += published.Int(len(b.Connections))
	}
	m.Entry = s.settings.Entry
	m.Files = map[string]published.Hash{}
	for path, data := range files {
		m.Files[path] = published.HashOf(data)
	}
	switch snapshot := m.Snapshot.(type) {
	case *published.ReleaseSnapshot:
		c := *snapshot
		c.ContentHash = published.ContentHash(m.Files)
		m.Snapshot = &c
	case *published.DraftSnapshot:
		c := *snapshot
		c.ContentHash = published.ContentHash(m.Files)
		m.Snapshot = &c
	}
	put(published.ManifestPath, m)
	if s.edit == nil {
		return files
	}
	edited, err := schemaversion.Edit(files, func(path string, file map[string]any) error {
		s.edit(path, file)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if edited, err = schemaversion.Seal(edited); err != nil {
		t.Fatal(err)
	}
	return edited
}

// board returns the snapshot's board id.
func (s *snap) board(t *testing.T, id string) *published.Board {
	t.Helper()
	for _, b := range s.boards {
		if b.Board.ID == id {
			return b
		}
	}
	t.Fatalf("the snapshot has no board %s", id)
	return nil
}

// node returns the node id of the snapshot's board, and its index there.
func (s *snap) node(t *testing.T, board, id string) (published.Node, int) {
	t.Helper()
	for i, n := range s.board(t, board).Nodes {
		if n.NodeID() == id {
			return n, i
		}
	}
	t.Fatalf("the board %s has no node %s", board, id)
	return nil, 0
}

// connection returns the connection id of the snapshot's board.
func (s *snap) connection(t *testing.T, board, id string) *published.Connection {
	t.Helper()
	b := s.board(t, board)
	for i := range b.Connections {
		if b.Connections[i].ID == id {
			return &b.Connections[i]
		}
	}
	t.Fatalf("the board %s has no connection %s", board, id)
	return nil
}

// variable returns the snapshot's variable id.
func (s *snap) variable(t *testing.T, id string) *published.Variable {
	t.Helper()
	for i := range s.variables.Variables {
		if s.variables.Variables[i].ID == id {
			return &s.variables.Variables[i]
		}
	}
	t.Fatalf("the snapshot has no variable %s", id)
	return nil
}

// project returns what the snapshot's expressions may name.
func (s *snap) project(t *testing.T) *expr.Project {
	t.Helper()
	var nodes []published.Node
	for _, b := range s.boards {
		nodes = append(nodes, b.Nodes...)
	}
	p, err := published.ExprProject(s.variables.Variables, s.types.ComponentTypes, s.components.Components, nodes)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// compile returns the AST of source, compiled in ctx on the board, against
// what the snapshot's expressions may name.
func (s *snap) compile(t *testing.T, source string, ctx expr.Context, board string) expr.Node {
	t.Helper()
	n, err := expr.Compile(source, ctx, s.project(t), board)
	if err != nil {
		t.Fatalf("%q: %v", source, err)
	}
	return n
}

func (s *snap) condition(t *testing.T, source, board string) *published.Condition {
	t.Helper()
	return &published.Condition{AST: s.compile(t, source, expr.ContextCondition, board), Source: source}
}

func (s *snap) script(t *testing.T, source, board string) *published.Script {
	t.Helper()
	return &published.Script{AST: s.compile(t, source, expr.ContextScript, board), Source: source}
}

// segments returns the segments of a rich text of one paragraph of the text
// s.
func segments(t *testing.T, s string) richtext.Segments {
	t.Helper()
	var segs richtext.Segments
	data, err := json.Marshal([]map[string]any{{"kind": "text", "marks": []string{}, "value": s}})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &segs); err != nil {
		t.Fatal(err)
	}
	return segs
}

// textOf returns an element's text of the segments, with its preview.
func textOf(segs richtext.Segments) *published.Text {
	return &published.Text{Preview: richtext.Preview(richtext.PlainText(segs)), Segments: segs}
}

// corpusSet is a file set of the corpus: its name, its directory's, and how
// it is made of the sets before it, by their names.
type corpusSet struct {
	name  string
	build func(t *testing.T, sets map[string]*snap) *snap
}

// member returns the object of the list key of the JSON object file whose
// id is id.
func member(file map[string]any, key, id string) map[string]any {
	for _, item := range file[key].([]any) {
		if o := item.(map[string]any); o["id"] == id {
			return o
		}
	}
	panic("no " + id + " in " + key)
}

// of returns a copy of the set name of sets, which a set changes.
func of(t *testing.T, sets map[string]*snap, name string) *snap {
	t.Helper()
	return load(t, sets[name].files(t))
}

// corpusSets are the corpus's file sets, each made of those before it.
var corpusSets = []corpusSet{
	{"base", func(t *testing.T, _ map[string]*snap) *snap {
		return load(t, readFixture(t))
	}},
	// draft is base's content as the draft's, as a draft pull fetches it.
	{"draft", func(t *testing.T, sets map[string]*snap) *snap {
		s := of(t, sets, "base")
		s.manifest.Snapshot = &published.DraftSnapshot{PulledAt: "2026-11-20T09:00:00Z"}
		return s
	}},
	// renamed renames the variable met_elara met_lara, and every expression
	// and text that names it, as variable.rename rewrites them (FR-VAR-03).
	{"renamed", func(t *testing.T, sets map[string]*snap) *snap {
		s := of(t, sets, "base")
		s.variable(t, v1).Key = "met_lara"
		branch, _ := s.node(t, b1, n2)
		for i, o := range branch.(*published.Branch).Outputs {
			if o.ID == q2 {
				branch.(*published.Branch).Outputs[i].Condition = s.condition(t,
					strings.ReplaceAll(o.Condition.Source, "met_elara", "met_lara"), b1)
			}
		}
		gate, _ := s.node(t, b2, n10)
		segs := gate.(*published.Element).Text.Segments
		for i := range segs {
			if segs[i].Ref == v1 {
				segs[i].Name = "met_lara"
			}
		}
		gate.(*published.Element).Text = textOf(segs)
		return s
	}},
	// added adds a variable, a component, and two elements on the empty
	// board, with a connection between them.
	{"added", func(t *testing.T, sets map[string]*snap) *snap {
		s := of(t, sets, "base")
		s.variables.Variables = append(s.variables.Variables, published.Variable{ID: v10, Key: "tide",
			Scope: published.ScopeGlobal, Type: value.Int, Default: value.FromJSON([]byte("2"))})
		s.components.Components = append(s.components.Components, published.Component{ID: c3, Key: "boatman",
			Name: "Boatman", ComponentTypeID: t1, Attributes: published.AttributeValues{
				a1: value.FromJSON([]byte("1")), a2: value.FromJSON([]byte(`"calm"`)),
				a3: value.FromJSON([]byte(`""`))}})
		quay := s.board(t, b3)
		quay.Nodes = append(quay.Nodes,
			&published.Element{ID: n20, Kind: "dialogue", Position: published.Position{X: 0, Y: 0}, Title: "Quay",
				Text: textOf(segments(t, "Gulls cry."))},
			&published.Element{ID: n21, Kind: "narration", IsEnding: true, Position: published.Position{X: 300, Y: 0},
				Text: textOf(segments(t, "Silence."))})
		quay.Connections = append(quay.Connections, published.Connection{ID: k20, From: n20, FromPort: "out",
			To: n21, Order: 0, Label: &published.Label{Segments: segments(t, "Listen")}})
		return s
	}},
	// moved moves added's two elements and their connection to the
	// keeper's gate, and titles one of them.
	{"moved", func(t *testing.T, sets map[string]*snap) *snap {
		s := of(t, sets, "added")
		quay, gate := s.board(t, b3), s.board(t, b2)
		gate.Nodes = append(gate.Nodes, quay.Nodes...)
		gate.Connections = append(gate.Connections, quay.Connections...)
		quay.Nodes, quay.Connections = nil, nil
		hush, _ := s.node(t, b2, n21)
		hush.(*published.Element).Title = "Hush"
		return s
	}},
	// rehomed removes the empty board, and moves added's two elements and
	// their connection, which it held, to a board that it adds, the
	// lighthouse, as an author who splits a board or merges two does, one of
	// the elements to another place on its new board's canvas.
	{"rehomed", func(t *testing.T, sets map[string]*snap) *snap {
		s := of(t, sets, "added")
		quay := s.board(t, b3)
		s.boards = slices.DeleteFunc(s.boards, func(b *published.Board) bool { return b.Board.ID == b3 })
		s.boards = append(s.boards, &published.Board{SchemaVersion: published.SchemaVersion,
			Board: published.BoardInfo{ID: b4, Name: "Lighthouse", Slug: "lighthouse"},
			Nodes: quay.Nodes, Connections: quay.Connections})
		gulls, _ := s.node(t, b4, n20)
		gulls.(*published.Element).Position = published.Position{X: 40, Y: 80}
		return s
	}},
	// cosmetic moves two elements on their canvas, and changes nothing else.
	{"cosmetic", func(t *testing.T, sets map[string]*snap) *snap {
		s := of(t, sets, "base")
		end, _ := s.node(t, b1, n3)
		end.(*published.Element).Position = published.Position{X: -999999000, Y: 40}
		mira, _ := s.node(t, b1, n5)
		mira.(*published.Element).Position = published.Position{X: 120.5, Y: 0}
		return s
	}},
	// texts rewrites an element's text, gives one that had none a text of
	// white space alone, whose preview is empty, makes a connection's label
	// bold, whose plain text stays the same, and lengthens another's.
	{"texts", func(t *testing.T, sets map[string]*snap) *snap {
		s := of(t, sets, "base")
		end, _ := s.node(t, b1, n3)
		end.(*published.Element).Text = textOf(segments(t, `The very end <3 & "farewell"`))
		blank, _ := s.node(t, b1, n5)
		blank.(*published.Element).Text = textOf(segments(t, "  "))
		bold := segments(t, "Go on")
		bold[0].Marks = []string{"bold"}
		s.connection(t, b1, k1).Label = &published.Label{Segments: bold}
		s.connection(t, b1, k2).Label = &published.Label{Segments: segments(t, "Leave quietly")}
		return s
	}},
	// expressions changes a branch's on-enter script, an output's condition,
	// and two connections' conditions, one that had none, so that some name
	// other entities than they did, and takes a connection's on-traverse
	// script away.
	{"expressions", func(t *testing.T, sets map[string]*snap) *snap {
		s := of(t, sets, "base")
		branch, _ := s.node(t, b1, n2)
		br := branch.(*published.Branch)
		br.OnEnter = s.script(t, "luck = luck * 2", b1)
		for i := range br.Outputs {
			if br.Outputs[i].ID == q1 {
				br.Outputs[i].Condition = s.condition(t, "coins > 0", b1)
			}
		}
		go1 := s.connection(t, b1, k1)
		go1.Condition = s.condition(t, strings.Replace(go1.Condition.Source, "< 3", "< 5", 1), b1)
		s.connection(t, b1, k2).OnTraverse = nil
		s.connection(t, b1, k3).Condition = s.condition(t, "coins > 2", b1)
		return s
	}},
	// outputs adds an output to a branch, before two of its outputs, whose
	// places move on.
	{"outputs", func(t *testing.T, sets map[string]*snap) *snap {
		s := of(t, sets, "base")
		branch, _ := s.node(t, b1, n2)
		br := branch.(*published.Branch)
		added := published.BranchOutput{ID: q4, Kind: published.OutputElseIf,
			Condition: s.condition(t, "coins > 10", b1)}
		br.Outputs = slices.Insert(br.Outputs, 1, added)
		for i := range br.Outputs {
			br.Outputs[i].Order = published.Int(i)
		}
		return s
	}},
	// fields changes fields of every kind of entity but a connection's: a
	// variable's type and default, one's description, one's resetOnEnter, a
	// component type's name, an attribute definition's name and another's
	// options, a component's name and an attribute's value, an element's
	// tags, a jumper's title, a board's name, and the project's.
	{"fields", func(t *testing.T, sets map[string]*snap) *snap {
		s := of(t, sets, "base")
		big := s.variable(t, v3)
		big.Type, big.Default = value.Int, value.FromJSON([]byte("7"))
		s.variable(t, v2).Description = "Fortune's favour."
		s.variable(t, v7).ResetOnEnter = false
		cast := &s.types.ComponentTypes[0]
		cast.Name = "Characters"
		for i := range cast.Attributes {
			switch cast.Attributes[i].ID {
			case a3:
				cast.Attributes[i].Name = "Life story"
			case a2:
				cast.Attributes[i].Options = append(cast.Attributes[i].Options, "angry")
			}
		}
		for i := range s.components.Components {
			if c := &s.components.Components[i]; c.ID == c2 {
				c.Name = "Mira the Pilot"
				c.Attributes = maps.Clone(c.Attributes)
				c.Attributes[a1] = value.FromJSON([]byte("6"))
			}
		}
		end, _ := s.node(t, b1, n3)
		end.(*published.Element).Tags = []string{"finale"}
		jump, _ := s.node(t, b1, n4)
		jump.(*published.Jumper).Title = "To the keeper's gate"
		s.board(t, b3).Board.Name = "Empty quay"
		s.manifest.Project.Name = "Harbour Tales"
		return s
	}},
	// boards removes the empty board, adds a board of one element, its
	// entry node, and changes the keeper's gate's slug, and so its file's
	// path.
	{"boards", func(t *testing.T, sets map[string]*snap) *snap {
		s := of(t, sets, "base")
		s.boards = slices.DeleteFunc(s.boards, func(b *published.Board) bool { return b.Board.ID == b3 })
		s.board(t, b2).Board.Slug = "keepers-gate-east"
		s.boards = append(s.boards, &published.Board{SchemaVersion: published.SchemaVersion,
			Board: published.BoardInfo{ID: b4, Name: "Lighthouse", Slug: "lighthouse", EntryNodeID: n30},
			Nodes: published.Nodes{&published.Element{ID: n30, Kind: "narration", IsEnding: true,
				Text: textOf(segments(t, "The lamp burns."))}},
			Connections: []published.Connection{}})
		return s
	}},
	// ignored is base with keys that the format does not define, which a
	// consumer ignores, in an object of every kind of entity and in a
	// node's position, one of which differs from a key that the format defines
	// only by its case, and with optional fields written with the value that
	// the format leaves out: a board variable's resetOnEnter of false and an
	// element's tags of [].
	{"ignored", func(t *testing.T, sets map[string]*snap) *snap {
		s := of(t, sets, "base")
		s.edit = ignoredKeys
		return s
	}},
}

// ignoredKeys is the edit of the set ignored's files.
func ignoredKeys(path string, file map[string]any) {
	switch path {
	case published.ManifestPath:
		file["project"].(map[string]any)["codename"] = "gull"
		member(file, "boards", b3)["colour"] = "teal"
	case published.SettingsPath:
		file["theme"] = map[string]any{"dark": true}
	case published.VariablesPath:
		member(file, "variables", v1)["KEY"] = "met_someone"
		member(file, "variables", v9)["resetOnEnter"] = false
	case published.ComponentTypesPath:
		cast := member(file, "componentTypes", t1)
		cast["plural"] = "Cast members"
		member(cast, "attributes", a1)["hint"] = "0 to 10"
	case published.ComponentsPath:
		member(file, "components", c2)["portrait"] = []any{"mira.png", "mira@2x.png"}
	case published.BoardPath("harbour-intro"):
		member(file, "nodes", n1)["TITLE"] = "Loud"
		member(file, "nodes", n3)["tags"] = []any{}
		member(file, "nodes", n5)["position"].(map[string]any)["z"] = json.Number("3")
		member(member(file, "nodes", n2), "outputs", q1)["colour"] = "red"
		member(file, "connections", k1)["weight"] = json.Number("2.5")
	}
}

// buildSets returns the corpus's file sets, by their names.
func buildSets(t *testing.T) map[string]*snap {
	t.Helper()
	sets := map[string]*snap{}
	for _, cs := range corpusSets {
		sets[cs.name] = cs.build(t, sets)
	}
	return sets
}
