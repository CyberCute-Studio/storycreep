// Package flow is the reference runtime of Storycreep's narrative (PRD
// §5.8.5, §8.6): it loads a published snapshot (Load), and runs its flow as
// the Godot runtime and play mode run it (Runtime), so that the three give
// the same node sequence, text, choices, signals, and state for the same
// inputs (FR-PLAY-06). It evaluates the snapshot's ASTs with package expr's
// evaluator, and never parses a source (PRD §6.1).
//
// schema/conformance holds the conformance suite, of scenarios of inputs to
// a runtime of a snapshot and what it observes of them, which
// TestConformance runs, and which the TypeScript engine (plan item 44) and
// the GDScript runtime (item 103) run too; schema/conformance/README.md
// describes the format and the observations.
package flow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/semantics"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// ErrLoad is Load's error for files that are not a snapshot that a runtime
// runs.
var ErrLoad = errors.New("flow: not a snapshot that a runtime runs")

// Snapshot is a published snapshot, loaded and checked (Load), which a
// Runtime runs and never changes, so that several runtimes may run one at
// once.
type Snapshot struct {
	manifest published.Manifest
	entry    *published.Entry
	// variables are the variables in the order of their file, by ID, and
	// their declarations by ID.
	variables []string
	declared  map[string]*published.Variable
	defaults  map[string]expr.Value
	// attributes are each component's runtime attributes, in the order of
	// the components' file and of their type's definitions, and their values
	// in the snapshot, by component and definition.
	attributes []attributeKey
	initial    map[attributeKey]expr.Value
	// declaredTypes are the runtime attributes' types, as their definitions
	// declare them, an enum's value.Enum, and options an enum's options.
	declaredTypes map[attributeKey]string
	options       map[attributeKey][]string
	boards        map[string]*board
	nodes         map[string]*node
	// connections are every connection, by ID.
	connections map[string]*published.Connection
}

// attributeKey names a component's runtime attribute: the component's ID and
// its attribute definition's.
type attributeKey struct{ component, attribute string }

// board is a board of the snapshot: its index's entry, and its variables
// that are reset whenever the flow enters it.
type board struct {
	info  published.ManifestBoard
	reset []string
}

// node is a node of the snapshot: its board's ID, the node, an *Element, a
// *Branch, or a *Jumper, and the connections that leave it, by its port, out
// or a branch's output's ID, in their order.
type node struct {
	board string
	node  published.Node
	ports map[string][]*published.Connection
}

// out are the connections that leave an element's port.
const out = "out"

// Load reads the snapshot that fsys holds, as a game's res://storycreep/
// holds one (PRD §6.3), from its manifest, storycreep.json, and checks what
// a runtime relies on: that each file that the manifest lists has its hash,
// that the snapshot's astVersion is at most expr.ASTVersion, which a newer
// one is refused for as the runtime's ast_unsupported (FR-EXPR-04), that
// each file is one that published.Decode reads, and that its references
// resolve as schema/published/README.md says a consumer checks: the entry's
// and the boards' entry nodes are nodes of their boards, which the index and
// the boards' files agree on; a connection leaves an element by out or a
// branch by one of its outputs, for a node of its board, its port's
// connections ordered 0, 1, 2, and so on, as a branch's outputs are; a
// jumper's target, when it has one, is a node of its board that is no
// jumper; a variable's board is one of the index's; a component's type is
// one of the snapshot's, and its values are one of each of its type's
// definitions, of the definition's type, as every default is of its
// declaration's; a text's or a label's value is of a variable or of a
// runtime attribute of a component; and every AST type-checks at its board
// against the snapshot's declarations, its names read by their refs
// (expr.CheckResolved). Its error wraps ErrLoad, and that of the file that
// it reads. It reads every board's file, where the Godot runtime reads one
// only once the flow enters its board (FR-GODOT-05), which gives the same
// flow.
func Load(fsys fs.FS) (*Snapshot, error) {
	s := &Snapshot{
		declared: make(map[string]*published.Variable), defaults: make(map[string]expr.Value),
		initial: make(map[attributeKey]expr.Value), declaredTypes: make(map[attributeKey]string), options: make(map[attributeKey][]string), boards: make(map[string]*board), nodes: make(map[string]*node),
		connections: make(map[string]*published.Connection),
	}
	if err := s.load(fsys); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLoad, err)
	}
	return s, nil
}

// load is Load, of s, its maps made.
func (s *Snapshot) load(fsys fs.FS) error {
	if err := read(fsys, published.ManifestPath, "", &s.manifest); err != nil {
		return err
	}
	m := &s.manifest
	if m.ASTVersion > expr.ASTVersion {
		return fmt.Errorf("ast_unsupported: the snapshot's astVersion is %d, and this runtime supports %d",
			m.ASTVersion, expr.ASTVersion)
	}
	var settings published.Settings
	var variables published.Variables
	var types published.ComponentTypes
	var components published.Components
	for path, v := range map[string]any{
		published.SettingsPath: &settings, published.VariablesPath: &variables,
		published.ComponentTypesPath: &types, published.ComponentsPath: &components,
	} {
		if err := read(fsys, path, m.Files[path], v); err != nil {
			return err
		}
	}
	files := make(map[string]*published.Board)
	for _, b := range m.Boards {
		if s.boards[b.ID] != nil {
			return fmt.Errorf("the index has the board %s twice", b.ID)
		}
		s.boards[b.ID] = &board{info: b}
		var f published.Board
		if err := read(fsys, b.File, m.Files[b.File], &f); err != nil {
			return err
		}
		if f.Board.ID != b.ID || f.Board.EntryNodeID != b.EntryNodeID {
			return fmt.Errorf("%s: its board is not the index's %s, of its entry node", b.File, b.ID)
		}
		files[b.ID] = &f
	}
	if err := s.declare(variables, types, components); err != nil {
		return err
	}
	for _, b := range m.Boards {
		if err := s.addNodes(b.ID, files[b.ID]); err != nil {
			return fmt.Errorf("%s: %w", b.File, err)
		}
	}
	p, err := s.project(types, components)
	if err != nil {
		return err
	}
	for _, b := range m.Boards {
		if err := s.addConnections(b.ID, files[b.ID], p); err != nil {
			return fmt.Errorf("%s: %w", b.File, err)
		}
	}
	if (m.Entry == nil) != (settings.Entry == nil) || m.Entry != nil && *m.Entry != *settings.Entry {
		return errors.New("the manifest's entry is not the settings'")
	}
	s.entry = m.Entry
	if e := s.entry; e != nil && (s.nodes[e.NodeID] == nil || s.nodes[e.NodeID].board != e.BoardID) {
		return fmt.Errorf("the entry %s is not a node of its board %s", e.NodeID, e.BoardID)
	}
	for id, b := range s.boards {
		if e := b.info.EntryNodeID; e != "" && (s.nodes[e] == nil || s.nodes[e].board != id) {
			return fmt.Errorf("the board %s's entry node %s is not one of its nodes", id, e)
		}
	}
	return nil
}

// read reads the file of the path in fsys into v, with published.Decode,
// once it has checked that its bytes have the hash hash, unless hash is "",
// as the manifest's, which no manifest lists.
func read(fsys fs.FS, path string, hash published.Hash, v any) error {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return err
	}
	if path != published.ManifestPath {
		sum := sha256.Sum256(data)
		if want := published.Hash("sha256:" + hex.EncodeToString(sum[:])); hash != want {
			return fmt.Errorf("%s: its hash is %s, not the manifest's %q", path, want, hash)
		}
	}
	if err := published.Decode(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// valueOf returns v, a value of the published format or of a save state, as
// a value of the type typ, an enum's one of options, as the format's values
// are typed by their declarations (§5.8.9), or an error.
func valueOf(v value.Value, typ string, options []string) (expr.Value, error) {
	if v.JSON() == nil {
		return expr.Value{}, errors.New("is missing")
	}
	read, problem := v.Read(typ, value.Checks{}, options...)
	if problem != "" {
		return expr.Value{}, errors.New(problem)
	}
	raw := string(read.JSON())
	switch typ {
	case value.Bool:
		return expr.BoolValue(raw == "true"), nil
	case value.Int:
		var n value.Integral
		err := json.Unmarshal(read.JSON(), &n)
		return expr.IntValue(int64(n)), err
	case value.Float:
		f, problem := semantics.ParseFloat(raw)
		if problem != "" {
			return expr.Value{}, errors.New(string(problem))
		}
		return expr.FloatValue(f), nil
	}
	var str string
	err := json.Unmarshal(read.JSON(), &str)
	return expr.StringValue(str), err
}

// jsonOf returns v, of the type typ, as the published format and a save state
// hold a value (value.Value).
func jsonOf(v expr.Value, typ string) value.Value {
	var data []byte
	switch v.Type {
	case value.Bool:
		data, _ = json.Marshal(v.Bool)
	case value.Int:
		data = []byte(semantics.FormatInt(v.Int))
	case value.Float:
		data, _ = json.Marshal(v.Float)
	default:
		data, _ = json.Marshal(v.Str)
	}
	read, _ := value.FromJSON(data).Read(typ, value.Checks{})
	return read
}

// declare reads the variables and the components' runtime attributes, with
// their values in the snapshot.
func (s *Snapshot) declare(variables published.Variables, types published.ComponentTypes,
	components published.Components) error {
	for i := range variables.Variables {
		v := &variables.Variables[i]
		if s.declared[v.ID] != nil {
			return fmt.Errorf("the variable %s is there twice", v.ID)
		}
		if v.Scope == published.ScopeBoard {
			b := s.boards[v.BoardID]
			if b == nil {
				return fmt.Errorf("the variable %s is of %s, which is no board of the index", v.ID, v.BoardID)
			}
			if v.ResetOnEnter {
				b.reset = append(b.reset, v.ID)
			}
		}
		d, err := valueOf(v.Default, v.Type, nil)
		if err != nil {
			return fmt.Errorf("the default of the variable %s %w", v.ID, err)
		}
		s.variables = append(s.variables, v.ID)
		s.declared[v.ID] = v
		s.defaults[v.ID] = d
	}
	definitions := make(map[string][]published.AttributeDefinition)
	for _, t := range types.ComponentTypes {
		definitions[t.ID] = t.Attributes
	}
	for _, c := range components.Components {
		defs, ok := definitions[c.ComponentTypeID]
		if !ok {
			return fmt.Errorf("the component %s is of %s, which is no component type of the snapshot", c.ID,
				c.ComponentTypeID)
		}
		if len(c.Attributes) != len(defs) {
			return fmt.Errorf("the component %s has %d values, not one of each of its type's %d definitions", c.ID,
				len(c.Attributes), len(defs))
		}
		for _, d := range defs {
			v, err := valueOf(c.Attributes[d.ID], d.Type, d.Options)
			if err != nil {
				return fmt.Errorf("the value of %s of the component %s %w", d.ID, c.ID, err)
			}
			if d.Runtime {
				k := attributeKey{c.ID, d.ID}
				s.attributes = append(s.attributes, k)
				s.initial[k] = v
				s.declaredTypes[k] = d.Type
				if d.Type == value.Enum {
					s.options[k] = d.Options
				}
			}
		}
	}
	return nil
}

// addNodes adds the nodes of the board b's file f, and their ports.
func (s *Snapshot) addNodes(b string, f *published.Board) error {
	for _, n := range f.Nodes {
		id, ports := "", map[string][]*published.Connection{}
		switch n := n.(type) {
		case *published.Element:
			id = n.ID
			ports[out] = nil
		case *published.Branch:
			id = n.ID
			for i, o := range n.Outputs {
				if int(o.Order) != i {
					return fmt.Errorf("the branch %s's outputs are not ordered 0, 1, 2, and so on", n.ID)
				}
				ports[o.ID] = nil
			}
		case *published.Jumper:
			id = n.ID
		}
		if s.nodes[id] != nil {
			return fmt.Errorf("the node %s is there twice", id)
		}
		s.nodes[id] = &node{board: b, node: n, ports: ports}
	}
	return nil
}

// project returns what the snapshot's expressions can name, against which
// its ASTs are checked.
func (s *Snapshot) project(types published.ComponentTypes, components published.Components) (*expr.Project,
	error) {
	var variables []expr.Variable
	for _, id := range s.variables {
		v := s.declared[id]
		variables = append(variables, expr.Variable{ID: v.ID, Key: v.Key, Type: v.Type, Board: v.BoardID})
	}
	definitions := make(map[string][]expr.Attribute)
	for _, t := range types.ComponentTypes {
		for _, d := range t.Attributes {
			definitions[t.ID] = append(definitions[t.ID], expr.Attribute{ID: d.ID, Key: d.Key, Type: d.Type,
				Runtime: d.Runtime, Options: d.Options})
		}
	}
	var cmps []expr.Component
	for _, c := range components.Components {
		cmps = append(cmps, expr.Component{ID: c.ID, Key: c.Key, Attributes: definitions[c.ComponentTypeID]})
	}
	var nodes []expr.BoardNode
	for id, n := range s.nodes {
		_, jumper := n.node.(*published.Jumper)
		nodes = append(nodes, expr.BoardNode{ID: id, Jumper: jumper})
	}
	return expr.NewProject(variables, cmps, nodes)
}

// addConnections adds the connections of the board b's file f to their
// nodes' ports, and checks the board's expressions, texts, and jumpers.
func (s *Snapshot) addConnections(b string, f *published.Board, p *expr.Project) error {
	for i := range f.Connections {
		c := &f.Connections[i]
		from, to := s.nodes[c.From], s.nodes[c.To]
		if s.connections[c.ID] != nil {
			return fmt.Errorf("the connection %s is there twice", c.ID)
		}
		if from == nil || from.board != b || to == nil || to.board != b {
			return fmt.Errorf("the connection %s does not join two nodes of the board", c.ID)
		}
		if _, ok := from.ports[c.FromPort]; !ok {
			return fmt.Errorf("the connection %s leaves %s by %s, which is no port of it", c.ID, c.From, c.FromPort)
		}
		from.ports[c.FromPort] = append(from.ports[c.FromPort], c)
		s.connections[c.ID] = c
		if c.Condition != nil {
			if err := expr.CheckResolved(c.Condition.AST, expr.ContextCondition, p, b); err != nil {
				return fmt.Errorf("the condition of %s: %w", c.ID, err)
			}
		}
		if c.OnTraverse != nil {
			if err := expr.CheckResolved(c.OnTraverse.AST, expr.ContextScript, p, b); err != nil {
				return fmt.Errorf("the script of %s: %w", c.ID, err)
			}
		}
		if c.Label != nil {
			if err := s.checkSegments(c.Label.Segments); err != nil {
				return fmt.Errorf("the label of %s: %w", c.ID, err)
			}
		}
	}
	for _, n := range f.Nodes {
		if err := s.checkNode(b, n, p); err != nil {
			return err
		}
	}
	for _, n := range f.Nodes {
		for port, cs := range s.nodes[nodeID(n)].ports {
			slices.SortFunc(cs, func(a, b *published.Connection) int { return int(a.Order - b.Order) })
			for i, c := range cs {
				if int(c.Order) != i {
					return fmt.Errorf("the connections of %s's port %s are not ordered 0, 1, 2, and so on", nodeID(n),
						port)
				}
			}
		}
	}
	return nil
}

// nodeID returns the ID of n.
func nodeID(n published.Node) string {
	switch n := n.(type) {
	case *published.Element:
		return n.ID
	case *published.Branch:
		return n.ID
	case *published.Jumper:
		return n.ID
	}
	return ""
}

// checkNode checks the scripts, the text, the outputs' conditions, and the
// target of the node n of the board b.
func (s *Snapshot) checkNode(b string, n published.Node, p *expr.Project) error {
	scripts := func(id string, onEnter, onExit *published.Script) error {
		for _, sc := range []*published.Script{onEnter, onExit} {
			if sc != nil {
				if err := expr.CheckResolved(sc.AST, expr.ContextScript, p, b); err != nil {
					return fmt.Errorf("a script of %s: %w", id, err)
				}
			}
		}
		return nil
	}
	switch n := n.(type) {
	case *published.Element:
		if n.Text != nil {
			if err := s.checkSegments(n.Text.Segments); err != nil {
				return fmt.Errorf("the text of %s: %w", n.ID, err)
			}
		}
		return scripts(n.ID, n.OnEnter, n.OnExit)
	case *published.Branch:
		for _, o := range n.Outputs {
			if o.Condition != nil {
				if err := expr.CheckResolved(o.Condition.AST, expr.ContextCondition, p, b); err != nil {
					return fmt.Errorf("the condition of %s: %w", o.ID, err)
				}
			}
		}
		return scripts(n.ID, n.OnEnter, n.OnExit)
	case *published.Jumper:
		if t := n.Target; t != nil {
			target := s.nodes[t.NodeID]
			if target == nil || target.board != t.BoardID {
				return fmt.Errorf("the jumper %s's target %s is not a node of its board %s", n.ID, t.NodeID, t.BoardID)
			}
			if _, jumper := target.node.(*published.Jumper); jumper {
				return fmt.Errorf("the jumper %s targets the jumper %s", n.ID, t.NodeID)
			}
		}
	}
	return nil
}

// checkSegments checks that each value of a text or a label is of a
// variable, or of a runtime attribute of a component.
func (s *Snapshot) checkSegments(segs richtext.Segments) error {
	for _, seg := range segs {
		if seg.Kind != richtext.SegmentValue {
			continue
		}
		if seg.Attribute == "" {
			if s.declared[seg.Ref] == nil {
				return fmt.Errorf("a value is of %s, which is no variable of the snapshot", seg.Ref)
			}
		} else if _, ok := s.initial[attributeKey{seg.Ref, seg.Attribute}]; !ok {
			return fmt.Errorf("a value is of %s's %s, which is no runtime attribute of the snapshot", seg.Ref,
				seg.Attribute)
		}
	}
	return nil
}
