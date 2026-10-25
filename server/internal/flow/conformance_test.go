package flow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/semantics"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

var update = flag.Bool("update", false, "rewrite the conformance suite's files from its suites")

// conformanceDir is schema/conformance, from this package's directory.
var conformanceDir = filepath.Join("..", "..", "..", "schema", "conformance")

// The files of a suite, in its directory: its scenarios, and its snapshot's
// directory.
const (
	scenariosFile = "scenarios.json"
	snapshotDir   = "snapshot"
)

// scenarioFile is a suite's scenarios.json, as schema/conformance/README.md
// describes it.
type scenarioFile struct {
	Scenarios []scenarioJSON `json:"scenarios"`
}

// scenarioJSON is a scenario: a runtime of the suite's snapshot, new, in
// strict mode or not, and its steps, in order.
type scenarioJSON struct {
	Note   string     `json:"note"`
	Steps  []stepJSON `json:"steps"`
	Strict bool       `json:"strict,omitempty"`
}

// stepJSON is a step of a scenario: a call of the runtime's, the events that
// it reports, in order, and the view of the flow after it, and, if the step
// has one, the state that save_state() returns after it.
type stepJSON struct {
	Events []eventJSON          `json:"events"`
	Input  inputJSON            `json:"input"`
	State  *published.SaveState `json:"state,omitempty"`
	View   viewJSON             `json:"view"`
}

// The calls of a step's input.
const (
	callStart        = "start"
	callGoto         = "goto"
	callReset        = "reset"
	callChoose       = "choose"
	callAdvance      = "advance"
	callSetVariable  = "set_variable"
	callSetAttribute = "set_attribute"
	callLoadState    = "load_state"
)

// inputJSON is a step's call, and its arguments: a start's or a goto's board
// and node, a choose's choice, a set_variable's variable and value, a
// set_attribute's component, attribute, and value, and a load_state's state.
type inputJSON struct {
	Attribute string               `json:"attribute,omitempty"`
	Board     string               `json:"board,omitempty"`
	Call      string               `json:"call"`
	Choice    string               `json:"choice,omitempty"`
	Component string               `json:"component,omitempty"`
	Node      string               `json:"node,omitempty"`
	State     *published.SaveState `json:"state,omitempty"`
	Value     *value.Value         `json:"value,omitempty"`
	Variable  string               `json:"variable,omitempty"`
}

// eventJSON is an event, with the fields of its kind (Event).
type eventJSON struct {
	At         string     `json:"at,omitempty"`
	Code       string     `json:"code,omitempty"`
	Event      string     `json:"event"`
	Expression string     `json:"expression,omitempty"`
	Name       string     `json:"name,omitempty"`
	Node       string     `json:"node,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	Value      *typedJSON `json:"value,omitempty"`
}

// typedJSON is a value with its type, as a signal's, which no declaration
// types, as the expression corpus writes one.
type typedJSON struct {
	Type  string      `json:"type"`
	Value value.Value `json:"value"`
}

// viewJSON is what a game sees of the flow: its current node and its phase,
// absent before it starts, the node's text, its choices, whether it can
// advance, and whether strict mode halted it.
type viewJSON struct {
	CanAdvance bool         `json:"canAdvance"`
	Choices    []choiceJSON `json:"choices"`
	Halted     bool         `json:"halted"`
	Node       string       `json:"node,omitempty"`
	Phase      string       `json:"phase,omitempty"`
	Text       string       `json:"text"`
}

// choiceJSON is a choice of the view.
type choiceJSON struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// canonical returns data, one JSON value, as the published format lays it
// out (PRD §6.2): each object's keys sorted, its numbers as they are
// written, 2-space indentation, HTML not escaped, and a line feed at the end.
func canonical(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// mnemonicID returns the ID of the kind k whose body is the mnemonic, letters
// and digits of Crockford's base 32, after zeros, as nd_0000000000000000000000START,
// so that a scenario names its entities readably. It panics on a mnemonic
// that makes no ID in its canonical spelling.
func mnemonicID(k ids.Kind, mnemonic string) string {
	id := k.Prefix() + "_" + strings.Repeat("0", ids.BodyLength-len(mnemonic)) + mnemonic
	if !ids.IsCanonical(k, id) {
		panic(fmt.Sprintf("%q makes no %s ID in its canonical spelling", mnemonic, k))
	}
	return id
}

func nd(m string) string { return mnemonicID(ids.Node, m) }
func cn(m string) string { return mnemonicID(ids.Connection, m) }
func bo(m string) string { return mnemonicID(ids.BranchOutput, m) }
func bd(m string) string { return mnemonicID(ids.Board, m) }
func vr(m string) string { return mnemonicID(ids.Variable, m) }
func cm(m string) string { return mnemonicID(ids.Component, m) }
func at(m string) string { return mnemonicID(ids.AttributeDef, m) }
func ct(m string) string { return mnemonicID(ids.ComponentType, m) }

// suite is a suite of the conformance suite, written in Go so that its
// expressions are compiled from their sources and its entities named by
// mnemonics: a project, which TestConformanceFiles writes as a snapshot, and
// its scenarios, which it writes as scenarios.json. TestConformance runs the
// files, not the suites, as every runtime runs them.
type suite struct {
	// name is the suite's directory in schema/conformance, and code the
	// mnemonic of its project's and its snapshot's IDs.
	name       string
	code       string
	project    string
	draft      bool
	variables  []fixtureVar
	types      []fixtureType
	components []fixtureComponent
	boards     []fixtureBoard
	// entry is the project's entry, a board's and a node's mnemonics.
	entry     [2]string
	scenarios []scenarioJSON
}

// fixtureVar is a variable of a suite's project, global unless it has a board.
type fixtureVar struct {
	id, key, typ string
	def          any
	board        string
	reset        bool
}

// fixtureType is a component type, with its attribute definitions.
type fixtureType struct {
	id, key, name string
	attributes    []fixtureAttribute
}

// fixtureAttribute is an attribute definition.
type fixtureAttribute struct {
	id, key, name, typ string
	def                any
	runtime            bool
	options            []string
}

// fixtureComponent is a component of a type, with its values by its
// definitions' mnemonics.
type fixtureComponent struct {
	id, key, name, typ string
	values             map[string]any
}

// fixtureBoard is a board, with its nodes and connections.
type fixtureBoard struct {
	id, slug, name, entry string
	nodes                 []fixtureNode
	connections           []fixtureConnection
}

// The types of a fixture's node.
const (
	element = published.NodeElement
	branch  = published.NodeBranch
	jumper  = published.NodeJumper
)

// fixtureNode is a node: an element, with its text, its ending, and its
// scripts; a branch, with its outputs and its scripts; or a jumper, with its
// target's board and node, or none.
type fixtureNode struct {
	id, typ, title  string
	text            string
	ending          bool
	onEnter, onExit string
	outputs         []fixtureOutput
	target          [2]string
}

// fixtureOutput is a branch's output, with its condition's source.
type fixtureOutput struct {
	id, kind, condition string
}

// fixtureConnection is a connection from a node's port, out or an output's
// mnemonic, to a node, with its label, its condition's source, and its
// on-traverse's. Its order is its place among the port's connections in the
// board's list.
type fixtureConnection struct {
	id, from, port, to string
	label              string
	condition          string
	onTraverse         string
}

// raw returns v, a bool, an int, a float64, or a string, as a value's JSON.
func raw(v any) value.Value {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return value.FromJSON(data)
}

// fixtureProject returns what the suite's expressions can name, and the
// published declarations of its variables.
func (s *suite) fixtureProject() (*expr.Project, error) {
	var vars []expr.Variable
	for _, v := range s.variables {
		board := ""
		if v.board != "" {
			board = bd(v.board)
		}
		vars = append(vars, expr.Variable{ID: vr(v.id), Key: v.key, Type: v.typ, Board: board})
	}
	attrs := make(map[string][]expr.Attribute)
	for _, t := range s.types {
		for _, a := range t.attributes {
			attrs[t.id] = append(attrs[t.id], expr.Attribute{ID: at(a.id), Key: a.key, Type: a.typ,
				Runtime: a.runtime, Options: a.options})
		}
	}
	var comps []expr.Component
	for _, c := range s.components {
		comps = append(comps, expr.Component{ID: cm(c.id), Key: c.key, Attributes: attrs[c.typ]})
	}
	var nodes []expr.BoardNode
	for _, b := range s.boards {
		for _, n := range b.nodes {
			nodes = append(nodes, expr.BoardNode{ID: nd(n.id), Jumper: n.typ == jumper})
		}
	}
	return expr.NewProject(vars, comps, nodes)
}

// valuePattern is a value or a mention in a fixture's text: {key},
// {key:decimals}, or {component.attribute}, as an expression names it, or
// [component], a mention of the component of the key.
var valuePattern = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)(?::([0-6]))?\}|\[([A-Za-z_][A-Za-z0-9_]*)\]`)

// segments returns the segments of a fixture's text on the board, one
// paragraph of texts, values, and mentions, each value resolved as Compile
// resolves its name at the board.
func (s *suite) segments(p *expr.Project, board, text string) (richtext.Segments, error) {
	var segs richtext.Segments
	plain := func(t string) {
		if t != "" {
			segs = append(segs, richtext.Segment{Kind: richtext.SegmentText, Marks: []string{}, Value: t})
		}
	}
	last := 0
	for _, m := range valuePattern.FindAllStringSubmatchIndex(text, -1) {
		plain(text[last:m[0]])
		last = m[1]
		if m[6] >= 0 {
			key := text[m[6]:m[7]]
			i := slices.IndexFunc(s.components, func(c fixtureComponent) bool { return c.key == key })
			if i < 0 {
				return nil, fmt.Errorf("[%s] mentions no component", key)
			}
			segs = append(segs, richtext.Segment{Kind: richtext.SegmentMention, Marks: []string{},
				Ref: cm(s.components[i].id), Value: s.components[i].name})
			continue
		}
		name := text[m[2]:m[3]]
		n, err := expr.Compile(name, expr.ContextCondition, p, bd(board))
		if err != nil && !errors.As(err, new(*expr.Error)) {
			return nil, err
		}
		if n == nil {
			// A value of any type, which a condition, a bool, may not be:
			// compile it as the value of a comparison with itself.
			n, err = expr.Compile(name+" == "+name, expr.ContextCondition, p, bd(board))
			if err != nil {
				return nil, fmt.Errorf("{%s}: %w", name, err)
			}
			n = n.(*expr.Binary).Left
		}
		seg := richtext.Segment{Kind: richtext.SegmentValue, Marks: []string{}}
		switch n := n.(type) {
		case *expr.Var:
			seg.Ref, seg.Name = n.Ref, n.Name
		case *expr.Attr:
			seg.Ref, seg.Attribute, seg.Name = n.Ref, n.Attribute, n.Name
		default:
			return nil, fmt.Errorf("{%s} is no variable or attribute", name)
		}
		if m[4] >= 0 {
			d := int(text[m[4]] - '0')
			seg.Decimals = &d
		}
		segs = append(segs, seg)
	}
	plain(text[last:])
	return segs, nil
}

// files returns the suite's snapshot, each file's path and bytes, as a
// runtime finds them in its directory.
func (s *suite) files() (map[string][]byte, error) {
	p, err := s.fixtureProject()
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte)
	write := func(path string, v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if files[path], err = canonical(data); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	}
	compile := func(source string, ctx expr.Context, board string) (expr.Node, error) {
		n, err := expr.Compile(source, ctx, p, bd(board))
		if err != nil {
			return nil, fmt.Errorf("%q: %w", source, err)
		}
		return n, nil
	}
	condition := func(source, board string) (*published.Condition, error) {
		if source == "" {
			return nil, nil
		}
		n, err := compile(source, expr.ContextCondition, board)
		if err != nil {
			return nil, err
		}
		return &published.Condition{AST: n, Source: source}, nil
	}
	signals := map[string]bool{}
	var asts []expr.Node
	script := func(source, board string) (*published.Script, error) {
		if source == "" {
			return nil, nil
		}
		n, err := compile(source, expr.ContextScript, board)
		if err != nil {
			return nil, err
		}
		for _, st := range n.(*expr.Script).Statements {
			if c, ok := st.(*expr.Call); ok && c.Function == "signal" {
				signals[c.Args[0].(*expr.Lit).Str] = true
			}
		}
		asts = append(asts, n)
		return &published.Script{AST: n, Source: source}, nil
	}
	var counts published.Counts
	var index []published.ManifestBoard
	for _, b := range s.boards {
		info := published.BoardInfo{ID: bd(b.id), Name: b.name, Slug: b.slug}
		if b.entry != "" {
			info.EntryNodeID = nd(b.entry)
		}
		f := published.Board{Board: info, SchemaVersion: published.SchemaVersion, Connections: []published.Connection{}}
		for i, n := range b.nodes {
			pos := published.Position{X: float64(i * 200), Y: 0}
			var node published.Node
			switch n.typ {
			case element:
				e := &published.Element{ID: nd(n.id), IsEnding: n.ending, Kind: "narration", Position: pos,
					Title: n.title}
				if n.text != "" {
					segs, err := s.segments(p, b.id, n.text)
					if err != nil {
						return nil, err
					}
					e.Text = &published.Text{Segments: segs, Preview: richtext.Preview(richtext.PlainText(segs))}
				}
				if e.OnEnter, err = script(n.onEnter, b.id); err != nil {
					return nil, err
				}
				if e.OnExit, err = script(n.onExit, b.id); err != nil {
					return nil, err
				}
				node = e
			case branch:
				br := &published.Branch{ID: nd(n.id), Position: pos, Title: n.title, Outputs: []published.BranchOutput{}}
				for j, o := range n.outputs {
					out := published.BranchOutput{ID: bo(o.id), Kind: o.kind, Order: published.Int(j)}
					if out.Condition, err = condition(o.condition, b.id); err != nil {
						return nil, err
					}
					if out.Condition != nil {
						asts = append(asts, out.Condition.AST)
					}
					br.Outputs = append(br.Outputs, out)
				}
				if br.OnEnter, err = script(n.onEnter, b.id); err != nil {
					return nil, err
				}
				if br.OnExit, err = script(n.onExit, b.id); err != nil {
					return nil, err
				}
				node = br
			case jumper:
				j := &published.Jumper{ID: nd(n.id), Position: pos, Title: n.title}
				if n.target[1] != "" {
					j.Target = &published.JumperTarget{BoardID: bd(n.target[0]), NodeID: nd(n.target[1])}
				}
				node = j
			}
			f.Nodes = append(f.Nodes, node)
		}
		orders := map[[2]string]int{}
		for _, c := range b.connections {
			port := "out"
			if c.port != "" {
				port = bo(c.port)
			}
			key := [2]string{c.from, port}
			conn := published.Connection{ID: cn(c.id), From: nd(c.from), FromPort: port, To: nd(c.to),
				Order: published.Int(orders[key])}
			orders[key]++
			if c.label != "" {
				segs, err := s.segments(p, b.id, c.label)
				if err != nil {
					return nil, err
				}
				conn.Label = &published.Label{Segments: segs}
			}
			if conn.Condition, err = condition(c.condition, b.id); err != nil {
				return nil, err
			}
			if conn.Condition != nil {
				asts = append(asts, conn.Condition.AST)
			}
			if conn.OnTraverse, err = script(c.onTraverse, b.id); err != nil {
				return nil, err
			}
			f.Connections = append(f.Connections, conn)
		}
		slices.SortFunc(f.Nodes, func(a, b published.Node) int { return strings.Compare(nodeID(a), nodeID(b)) })
		slices.SortFunc(f.Connections, func(a, b published.Connection) int { return strings.Compare(a.ID, b.ID) })
		counts.Boards++
		counts.Nodes += published.Int(len(f.Nodes))
		counts.Connections += published.Int(len(f.Connections))
		path := published.BoardPath(b.slug)
		if err := write(path, f); err != nil {
			return nil, err
		}
		index = append(index, published.ManifestBoard{EntryNodeID: info.EntryNodeID, File: path, ID: info.ID,
			Name: info.Name, Slug: info.Slug})
	}
	slices.SortFunc(index, func(a, b published.ManifestBoard) int { return strings.Compare(a.ID, b.ID) })
	var variables published.Variables
	variables.SchemaVersion = published.SchemaVersion
	for _, v := range s.variables {
		pv := published.Variable{ID: vr(v.id), Key: v.key, Type: v.typ, Default: raw(v.def),
			Scope: published.ScopeGlobal}
		if v.board != "" {
			pv.Scope, pv.BoardID, pv.ResetOnEnter = published.ScopeBoard, bd(v.board), v.reset
		}
		variables.Variables = append(variables.Variables, pv)
	}
	slices.SortFunc(variables.Variables, func(a, b published.Variable) int { return strings.Compare(a.ID, b.ID) })
	types := published.ComponentTypes{SchemaVersion: published.SchemaVersion,
		ComponentTypes: []published.ComponentType{}}
	for _, t := range s.types {
		pt := published.ComponentType{ID: ct(t.id), Key: t.key, Name: t.name}
		for _, a := range t.attributes {
			pt.Attributes = append(pt.Attributes, published.AttributeDefinition{ID: at(a.id), Key: a.key,
				Name: a.name, Type: a.typ, Default: raw(a.def), Runtime: a.runtime, Options: a.options})
		}
		types.ComponentTypes = append(types.ComponentTypes, pt)
	}
	slices.SortFunc(types.ComponentTypes, func(a, b published.ComponentType) int { return strings.Compare(a.ID, b.ID) })
	components := published.Components{SchemaVersion: published.SchemaVersion, Components: []published.Component{}}
	for _, c := range s.components {
		pc := published.Component{ID: cm(c.id), Key: c.key, Name: c.name, ComponentTypeID: ct(c.typ),
			Attributes: published.AttributeValues{}}
		for a, v := range c.values {
			pc.Attributes[at(a)] = raw(v)
		}
		components.Components = append(components.Components, pc)
	}
	slices.SortFunc(components.Components, func(a, b published.Component) int { return strings.Compare(a.ID, b.ID) })
	entry := &published.Entry{BoardID: bd(s.entry[0]), NodeID: nd(s.entry[1])}
	settings := published.Settings{Entry: entry, SchemaVersion: published.SchemaVersion,
		Signals: slices.Sorted(maps.Keys(signals))}
	if settings.Signals == nil {
		settings.Signals = []string{}
	}
	for path, v := range map[string]any{
		published.SettingsPath: settings, published.VariablesPath: variables,
		published.ComponentTypesPath: types, published.ComponentsPath: components,
	} {
		if err := write(path, v); err != nil {
			return nil, err
		}
	}
	counts.Components = published.Int(len(components.Components))
	counts.Variables = published.Int(len(variables.Variables))
	hashes := make(map[string]published.Hash)
	var all strings.Builder
	for _, path := range slices.Sorted(maps.Keys(files)) {
		sum := sha256.Sum256(files[path])
		hashes[path] = published.Hash("sha256:" + hex.EncodeToString(sum[:]))
		all.WriteString(path + " " + string(hashes[path]) + "\n")
	}
	// The content hash is the server's (plan item 78), which a runtime does
	// not check: the fixtures' is the hash of their files' paths and hashes.
	sum := sha256.Sum256([]byte(all.String()))
	contentHash := published.Hash("sha256:" + hex.EncodeToString(sum[:]))
	var snapshot published.Snapshot = &published.ReleaseSnapshot{ContentHash: contentHash,
		ID: mnemonicID(ids.Snapshot, s.code), Note: "Conformance suite " + s.name,
		PublishedAt: "2026-10-25T00:00:00Z", Version: "1.0.0"}
	if s.draft {
		snapshot = &published.DraftSnapshot{ContentHash: contentHash, PulledAt: "2026-10-25T00:00:00Z"}
	}
	manifest := published.Manifest{ASTVersion: published.Int(expr.VersionOf(asts...)), Boards: index,
		Counts: counts, Entry: entry, Files: hashes, Generator: published.GeneratorInfo{Name: "storycreep",
			Version: "conformance"}, Project: published.ProjectInfo{ID: mnemonicID(ids.Project, s.code), Name: s.project, Slug: s.name},
		SchemaVersion: published.SchemaVersion, Snapshot: snapshot}
	if err := write(published.ManifestPath, manifest); err != nil {
		return nil, err
	}
	return files, nil
}

// suiteFiles returns every file of the conformance suite, by its path from
// schema/conformance: each suite's snapshot and its scenarios.
func suiteFiles(t *testing.T) map[string][]byte {
	t.Helper()
	all := make(map[string][]byte)
	for _, s := range suites() {
		files, err := s.files()
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		for path, data := range files {
			all[s.name+"/"+snapshotDir+"/"+path] = data
		}
		data, err := json.Marshal(scenarioFile{Scenarios: s.scenarios})
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if all[s.name+"/"+scenariosFile], err = canonical(data); err != nil {
			t.Fatal(err)
		}
	}
	return all
}

// TestConformanceFiles checks that schema/conformance holds the files that
// the suites write, and no other JSON file; run go test ./internal/flow -run
// TestConformanceFiles -update to rewrite them.
func TestConformanceFiles(t *testing.T) {
	files := suiteFiles(t)
	var stray []string
	err := filepath.WalkDir(conformanceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		rel, err := filepath.Rel(conformanceDir, path)
		if err != nil {
			return err
		}
		if _, ok := files[filepath.ToSlash(rel)]; !ok {
			stray = append(stray, path)
		}
		return nil
	})
	if err != nil && !(*update && errors.Is(err, fs.ErrNotExist)) {
		t.Fatal(err)
	}
	for _, path := range stray {
		if *update {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			continue
		}
		t.Errorf("%s is not a file of the suites; run go test ./internal/flow -run TestConformanceFiles -update", path)
	}
	for _, path := range slices.Sorted(maps.Keys(files)) {
		full := filepath.Join(conformanceDir, filepath.FromSlash(path))
		if *update {
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, files[path], 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(full)
		if err != nil || !bytes.Equal(got, files[path]) {
			t.Errorf("%s is not what its suite writes; run go test ./internal/flow -run TestConformanceFiles -update",
				path)
		}
	}
}

// readSuites returns each suite of schema/conformance, by its directory's
// name: its snapshot, loaded, and its scenarios, read strictly.
func readSuites(t *testing.T) (map[string]*Snapshot, map[string]scenarioFile) {
	t.Helper()
	entries, err := os.ReadDir(conformanceDir)
	if err != nil {
		t.Fatal(err)
	}
	snapshots, scenarios := make(map[string]*Snapshot), make(map[string]scenarioFile)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(conformanceDir, e.Name())
		s, err := Load(os.DirFS(filepath.Join(dir, snapshotDir)))
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		data, err := os.ReadFile(filepath.Join(dir, scenariosFile))
		if err != nil {
			t.Fatal(err)
		}
		var f scenarioFile
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&f); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		snapshots[e.Name()], scenarios[e.Name()] = s, f
	}
	return snapshots, scenarios
}

// TestConformance runs every scenario of the conformance suite, from its
// files, as the TypeScript engine and the GDScript runtime run them: a new
// runtime of the suite's snapshot, each step's call, and the events that it
// reports, the view after it, and the state, if the step has one, compared
// with the step's (schema/conformance/README.md).
//
// Verifies: FR-PLAY-06/AC1, FR-PLAY-06/AC3
func TestConformance(t *testing.T) {
	t.Parallel()
	snapshots, files := readSuites(t)
	if len(files) == 0 {
		t.Fatal("schema/conformance holds no suite")
	}
	n := 0
	for _, name := range slices.Sorted(maps.Keys(files)) {
		notes := map[string]bool{}
		for _, sc := range files[name].Scenarios {
			if sc.Note == "" || notes[sc.Note] {
				t.Errorf("%s: a scenario's note is empty or another's: %q", name, sc.Note)
			}
			notes[sc.Note] = true
			n++
			runScenario(t, name+": "+sc.Note, snapshots[name], sc)
		}
	}
	if n < 30 {
		t.Errorf("the suite has %d scenarios; FR-PLAY-06 AC1 asks for at least 30", n)
	}
}

// runScenario runs the scenario sc of the snapshot s, and reports where it
// diverges.
func runScenario(t *testing.T, where string, s *Snapshot, sc scenarioJSON) {
	t.Helper()
	var events []eventJSON
	r := New(s, sc.Strict, func(e Event) { events = append(events, eventOf(e)) })
	for i, st := range sc.Steps {
		at := fmt.Sprintf("%s: step %d (%s)", where, i+1, st.Input.Call)
		events = nil
		if err := call(r, s, st.Input); err != nil {
			t.Errorf("%s: %v", at, err)
			return
		}
		if events == nil {
			events = []eventJSON{}
		}
		if !reflect.DeepEqual(events, st.Events) {
			t.Errorf("%s: events\n%s\nwant\n%s", at, show(events), show(st.Events))
		}
		if got := viewOf(r); !reflect.DeepEqual(got, st.View) {
			t.Errorf("%s: view\n%s\nwant\n%s", at, show(got), show(st.View))
		}
		if st.State != nil {
			if err := sameState(s, r.SaveState(), *st.State); err != nil {
				t.Errorf("%s: state: %v\n%s", at, err, show(r.SaveState()))
			}
		}
	}
}

// show returns v's JSON, for a failure's message.
func show(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(data)
}

// call makes the input's call of r, its values typed by their declarations
// in s.
func call(r *Runtime, s *Snapshot, in inputJSON) error {
	switch in.Call {
	case callStart:
		r.Start(in.Board, in.Node)
	case callGoto:
		r.Goto(in.Board, in.Node)
	case callReset:
		r.Reset()
	case callChoose:
		r.Choose(in.Choice)
	case callAdvance:
		r.Advance()
	case callSetVariable:
		d := s.declared[in.Variable]
		if d == nil || in.Value == nil {
			return fmt.Errorf("set_variable of %q, which is no variable, or of no value", in.Variable)
		}
		v, err := valueOf(*in.Value, d.Type, nil)
		if err != nil {
			return err
		}
		return r.SetVariable(in.Variable, v)
	case callSetAttribute:
		k := attributeKey{in.Component, in.Attribute}
		typ, ok := s.declaredTypes[k]
		if !ok || in.Value == nil {
			return fmt.Errorf("set_attribute of %v, which is no runtime attribute, or of no value", k)
		}
		v, err := valueOf(*in.Value, typ, s.options[k])
		if err != nil {
			return err
		}
		return r.SetAttribute(in.Component, in.Attribute, v)
	case callLoadState:
		if in.State == nil {
			return errors.New("load_state of no state")
		}
		return r.LoadState(*in.State)
	default:
		return fmt.Errorf("%q is no call", in.Call)
	}
	return nil
}

// eventOf returns e as a scenario writes it.
func eventOf(e Event) eventJSON {
	j := eventJSON{Event: e.Kind, Node: e.Node, Reason: e.Reason, Name: e.Name, Code: string(e.Code), At: e.At,
		Expression: e.Expression}
	if e.Value != nil {
		j.Value = &typedJSON{Type: e.Value.Type, Value: jsonOf(*e.Value, e.Value.Type)}
	}
	return j
}

// viewOf returns the view of r's flow.
func viewOf(r *Runtime) viewJSON {
	node, phase := r.Current()
	v := viewJSON{Node: node, Phase: phase, Text: r.Text(), CanAdvance: r.CanAdvance(), Halted: r.Halted(),
		Choices: []choiceJSON{}}
	for _, c := range r.Choices() {
		v.Choices = append(v.Choices, choiceJSON(c))
	}
	return v
}

// sameState returns why got, a state of s, is not want, each value compared
// as its declaration types it, or nil.
func sameState(s *Snapshot, got, want published.SaveState) error {
	type plain struct {
		Cursor                                 *published.Cursor
		ProjectID, SnapshotID, SnapshotVersion string
		SchemaVersion, StateVersion            published.Int
		Visits                                 published.Visits
	}
	g := plain{got.Cursor, got.ProjectID, got.SnapshotID, got.SnapshotVersion, got.SchemaVersion, got.StateVersion,
		got.Visits}
	w := plain{want.Cursor, want.ProjectID, want.SnapshotID, want.SnapshotVersion, want.SchemaVersion,
		want.StateVersion, want.Visits}
	if !reflect.DeepEqual(g, w) {
		return fmt.Errorf("%+v; want %+v", g, w)
	}
	if !slices.Equal(slices.Sorted(maps.Keys(got.Variables)), slices.Sorted(maps.Keys(want.Variables))) {
		return errors.New("the variables differ")
	}
	for id, v := range want.Variables {
		a, errA := valueOf(got.Variables[id], s.declared[id].Type, nil)
		b, errB := valueOf(v, s.declared[id].Type, nil)
		if errA != nil || errB != nil || a != b {
			return fmt.Errorf("the variable %s is %v; want %v", id, a, b)
		}
	}
	if len(got.Attributes) != len(want.Attributes) {
		return errors.New("the attributes' components differ")
	}
	for c, values := range want.Attributes {
		if len(got.Attributes[c]) != len(values) {
			return fmt.Errorf("the attributes of %s differ", c)
		}
		for a, v := range values {
			k := attributeKey{c, a}
			typ := s.declaredTypes[k]
			x, errA := valueOf(got.Attributes[c][a], typ, s.options[k])
			y, errB := valueOf(v, typ, s.options[k])
			if errA != nil || errB != nil || x != y {
				return fmt.Errorf("%s of %s is %v; want %v", a, c, x, y)
			}
		}
	}
	return nil
}

// TestBuiltinsCovered checks that the conformance suite calls every built-in
// in an expression of a snapshot, so that each has conformance cases
// (FR-EXPR-04 AC1), beside its cases in the expression corpus.
//
// Verifies: FR-EXPR-04/AC1
func TestBuiltinsCovered(t *testing.T) {
	t.Parallel()
	called := map[string]bool{}
	for _, s := range suites() {
		files, err := s.files()
		if err != nil {
			t.Fatal(err)
		}
		for path, data := range files {
			if strings.HasPrefix(path, "boards/") {
				for _, m := range regexp.MustCompile(`"function": "([a-z_]+)"`).FindAllSubmatch(data, -1) {
					called[string(m[1])] = true
				}
			}
		}
	}
	for _, name := range expr.Builtins() {
		if !called[name] {
			t.Errorf("no scenario's snapshot calls %s", name)
		}
	}
}

// The helpers that the suites write their scenarios with.

func start(board, node string) inputJSON {
	return inputJSON{Call: callStart, Board: board, Node: node}
}

func gotoNode(board, node string) inputJSON {
	return inputJSON{Call: callGoto, Board: board, Node: node}
}

func choose(id string) inputJSON { return inputJSON{Call: callChoose, Choice: cn(id)} }

func setVariable(id string, v any) inputJSON {
	r := raw(v)
	return inputJSON{Call: callSetVariable, Variable: vr(id), Value: &r}
}

func setAttribute(component, attribute string, v any) inputJSON {
	r := raw(v)
	return inputJSON{Call: callSetAttribute, Component: cm(component), Attribute: at(attribute), Value: &r}
}

func loadState(st published.SaveState) inputJSON {
	return inputJSON{Call: callLoadState, State: &st}
}

func enter(node string) eventJSON { return eventJSON{Event: EventEnter, Node: nd(node)} }
func ready(node string) eventJSON { return eventJSON{Event: EventReady, Node: nd(node)} }
func exit(node string) eventJSON  { return eventJSON{Event: EventExit, Node: nd(node)} }
func end(reason string) eventJSON { return eventJSON{Event: EventEnd, Reason: reason} }

func signal(name string, v ...expr.Value) eventJSON {
	e := eventJSON{Event: EventSignal, Name: name}
	if len(v) == 1 {
		e.Value = &typedJSON{Type: v[0].Type, Value: jsonOf(v[0], v[0].Type)}
	}
	return e
}

// fault returns a runtime error of the entity at's expression, or of none.
func fault(code semantics.RuntimeError, at, expression string) eventJSON {
	return eventJSON{Event: EventError, Code: string(code), At: at, Expression: expression}
}

func events(es ...eventJSON) []eventJSON {
	if es == nil {
		return []eventJSON{}
	}
	return es
}

// awaiting returns the view of a flow that awaits a choice at the node, with
// its text and its choices, each a connection's mnemonic and text.
func awaiting(node, text string, canAdvance bool, choices ...string) viewJSON {
	v := viewJSON{Node: nd(node), Phase: published.PhaseAwaitingChoice, Text: text, CanAdvance: canAdvance,
		Choices: []choiceJSON{}}
	for i := 0; i+1 < len(choices); i += 2 {
		v.Choices = append(v.Choices, choiceJSON{ID: cn(choices[i]), Text: choices[i+1]})
	}
	return v
}

// ended returns the view of a flow that ended at the node, with its text.
func ended(node, text string) viewJSON {
	return viewJSON{Node: nd(node), Phase: published.PhaseEnded, Text: text, Choices: []choiceJSON{}}
}

// notStarted is the view of a flow that has not started.
var notStarted = viewJSON{Choices: []choiceJSON{}}

// halted returns v, halted.
func halted(v viewJSON) viewJSON {
	v.Halted = true
	return v
}

// step returns a step of the input, its events, and its view.
func step(input inputJSON, es []eventJSON, view viewJSON) stepJSON {
	return stepJSON{Input: input, Events: es, View: view}
}

// withState returns st with the state that it is expected to leave.
func withState(st stepJSON, state published.SaveState) stepJSON {
	st.State = &state
	return st
}

// state returns a state of the suite: the cursor at the node of the board,
// in the phase, or none for "", each variable's value its default but those
// in vars, by their mnemonics, each runtime attribute's its component's but
// those in attrs, by the component's mnemonic and then the attribute's, and
// the visit counts, by the nodes' mnemonics.
func (s *suite) state(board, node, phase string, vars map[string]any, attrs map[[2]string]any,
	visits map[string]int) published.SaveState {
	st := published.SaveState{
		Attributes: map[string]published.AttributeValues{}, ProjectID: mnemonicID(ids.Project, s.code), SchemaVersion: published.SchemaVersion,
		StateVersion: published.StateVersion, Variables: map[string]value.Value{}, Visits: published.Visits{},
	}
	if !s.draft {
		st.SnapshotID = mnemonicID(ids.Snapshot, s.code)
		st.SnapshotVersion = "1.0.0"
	}
	if node != "" {
		st.Cursor = &published.Cursor{BoardID: bd(board), NodeID: nd(node), Phase: phase}
	}
	for _, v := range s.variables {
		val, ok := vars[v.id]
		if !ok {
			val = v.def
		}
		st.Variables[vr(v.id)] = raw(val)
	}
	runtime := map[string][]fixtureAttribute{}
	for _, t := range s.types {
		for _, a := range t.attributes {
			if a.runtime {
				runtime[t.id] = append(runtime[t.id], a)
			}
		}
	}
	for _, c := range s.components {
		for _, a := range runtime[c.typ] {
			val, ok := attrs[[2]string{c.id, a.id}]
			if !ok {
				val = c.values[a.id]
			}
			if st.Attributes[cm(c.id)] == nil {
				st.Attributes[cm(c.id)] = published.AttributeValues{}
			}
			st.Attributes[cm(c.id)][at(a.id)] = raw(val)
		}
	}
	for n, count := range visits {
		st.Visits[nd(n)] = published.Int(count)
	}
	return st
}

// suites returns the suites of the conformance suite.
func suites() []*suite {
	return []*suite{choicesSuite(), branchesSuite(), jumpersSuite(), valuesSuite(), visitsSuite()}
}
