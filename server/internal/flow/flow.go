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
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/semantics"
	"github.com/CyberCute-Studio/storycreep/server/internal/slug"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// ErrLoad is Load's error for files that are not a snapshot that a runtime
// runs.
var ErrLoad = errors.New("flow: not a snapshot that a runtime runs")

// ErrASTUnsupported is Load's error, beside ErrLoad, for a snapshot whose
// astVersion is newer than expr.ASTVersion, the runtime's ast_unsupported
// (FR-EXPR-04 AC4, PRD §6.4), which a newer runtime may run, so that a
// caller can tell it from a snapshot that no runtime runs.
var ErrASTUnsupported = errors.New("ast_unsupported")

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
// holds one (PRD §6.3), from its manifest, storycreep.json, each file into
// the types with published.Decode, and checks every item of what
// schema/published/README.md says a consumer checks beyond the schemas,
// which that list alone enumerates, an asset's hash among the files', and a
// node's tags compared as input.FoldCase folds them. Beyond that list, it
// checks what the runtime relies on: that the snapshot's astVersion is at
// most expr.ASTVersion, which a newer one is refused for as the runtime's
// ast_unsupported, with an error that wraps ErrASTUnsupported too, and at
// least the highest version of the constructs that its ASTs use
// (expr.VersionOf), as every runtime trusts it to be (FR-EXPR-04); that a
// text's or a label's value is of a variable that an expression of its board
// reads (FR-TEXT-02, §5.8.3), or of a runtime attribute; and that every value
// is of its declaration's type, a string, an enum's among them, which an
// expression reads as a string, of at most semantics.MaxStringLength
// characters without a NUL, as the language's are (valueOf). It is no
// validator of the schemas, whose patterns and bounds published.Decode does
// not check. Its error wraps ErrLoad, and that of the file that it reads,
// and of a snapshot that is wrong in several ways it is always the same one,
// as it reads the files and checks them in one order. It reads every board's
// file, where the Godot runtime reads one only once the flow enters its
// board (FR-GODOT-05), which gives the same flow.
func Load(fsys fs.FS) (*Snapshot, error) {
	l := &loader{
		s: &Snapshot{
			declared: make(map[string]*published.Variable), defaults: make(map[string]expr.Value),
			initial: make(map[attributeKey]expr.Value), declaredTypes: make(map[attributeKey]string),
			options: make(map[attributeKey][]string), boards: make(map[string]*board), nodes: make(map[string]*node),
		},
		entities: make(map[string]bool), components: make(map[string]bool), read: make(map[string]bool),
	}
	if err := l.load(fsys); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLoad, err)
	}
	return l.s, nil
}

// loader is what Load keeps while it reads a snapshot's files beside the
// snapshot s: the IDs of the entities that it has read, of which no two are
// one, the components' IDs, the paths of the files that it has read, and the
// ASTs, whose constructs' versions the manifest's astVersion covers.
type loader struct {
	s          *Snapshot
	entities   map[string]bool
	components map[string]bool
	read       map[string]bool
	asts       []expr.Node
}

// load is Load.
func (l *loader) load(fsys fs.FS) error {
	s := l.s
	data, err := fs.ReadFile(fsys, published.ManifestPath)
	if err != nil {
		return err
	}
	if err := decode(published.ManifestPath, data, &s.manifest); err != nil {
		return err
	}
	m := &s.manifest
	if m.ASTVersion > expr.ASTVersion {
		return fmt.Errorf("%w: the snapshot's astVersion is %d, and this runtime supports %d", ErrASTUnsupported,
			m.ASTVersion, expr.ASTVersion)
	}
	for _, path := range slices.Sorted(maps.Keys(m.Files)) {
		if !isFilePath(path) {
			return fmt.Errorf("the manifest lists %q, which is none of the paths of a snapshot's files (PRD §7.5)",
				path)
		}
	}
	var settings published.Settings
	var variables published.Variables
	var types published.ComponentTypes
	var components published.Components
	for _, f := range []struct {
		path string
		v    any
	}{
		{published.SettingsPath, &settings}, {published.VariablesPath, &variables},
		{published.ComponentTypesPath, &types}, {published.ComponentsPath, &components},
	} {
		if err := l.readFile(fsys, f.path, f.v); err != nil {
			return err
		}
	}
	files := make(map[string]*published.Board)
	for _, b := range m.Boards {
		if err := l.entity(b.ID); err != nil {
			return fmt.Errorf("the index: %w", err)
		}
		s.boards[b.ID] = &board{info: b}
		if b.File != published.BoardPath(b.Slug) {
			return fmt.Errorf("the index's board %s's file is %s, not boards/ and its slug and .json", b.ID, b.File)
		}
		var f published.Board
		if err := l.readFile(fsys, b.File, &f); err != nil {
			return err
		}
		if f.Board != (published.BoardInfo{EntryNodeID: b.EntryNodeID, ID: b.ID, Name: b.Name, Slug: b.Slug}) {
			return fmt.Errorf("%s: its board is not the index's %s, of its ID, name, slug, or entry node", b.File,
				b.ID)
		}
		files[b.ID] = &f
	}
	// The files that it does not read, as the assets, are hashed too, as
	// they stream, so that every file that the manifest lists is its bytes'.
	for _, path := range slices.Sorted(maps.Keys(m.Files)) {
		if !l.read[path] {
			if err := hashed(fsys, m.Files, path, io.Discard); err != nil {
				return err
			}
		}
	}
	if len(slices.Compact(slices.Sorted(slices.Values(settings.Signals)))) != len(settings.Signals) {
		return fmt.Errorf("%s: a signal is there twice", published.SettingsPath)
	}
	if err := l.declare(variables, types, components); err != nil {
		return err
	}
	for _, b := range m.Boards {
		if err := l.addNodes(b.ID, files[b.ID]); err != nil {
			return fmt.Errorf("%s: %w", b.File, err)
		}
	}
	// What the snapshot's expressions can name, against which its ASTs are
	// checked, of the nodes in the order of the index and of their files.
	var nodes []published.Node
	for _, b := range m.Boards {
		nodes = append(nodes, files[b.ID].Nodes...)
	}
	p, err := published.ExprProject(variables.Variables, types.ComponentTypes, components.Components, nodes)
	if err != nil {
		return err
	}
	for _, b := range m.Boards {
		if err := l.addConnections(b.ID, files[b.ID], p); err != nil {
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
	for _, b := range m.Boards {
		if e := b.EntryNodeID; e != "" && (s.nodes[e] == nil || s.nodes[e].board != b.ID) {
			return fmt.Errorf("the board %s's entry node %s is not one of its nodes", b.ID, e)
		}
	}
	counts := published.Counts{Boards: published.Int(len(m.Boards)),
		Components: published.Int(len(components.Components)), Variables: published.Int(len(variables.Variables))}
	for _, f := range files {
		counts.Nodes += published.Int(len(f.Nodes))
		counts.Connections += published.Int(len(f.Connections))
	}
	if m.Counts != counts {
		return fmt.Errorf("the manifest's counts are %+v, but the files hold %+v", m.Counts, counts)
	}
	if v := published.Int(expr.VersionOf(l.asts...)); v > m.ASTVersion {
		return fmt.Errorf("the snapshot's astVersion is %d, though its expressions use constructs of version %d "+
			"(FR-EXPR-04)", m.ASTVersion, v)
	}
	return nil
}

// filePattern is the pattern of the path of a snapshot's file but the
// manifest, of a key of the manifest's files (published.Manifest), which the
// pull's path rules take (PRD §7.5).
var filePattern = func() *regexp.Regexp {
	f, _ := reflect.TypeFor[published.Manifest]().FieldByName("Files")
	pattern := f.Tag.Get("keyPattern")
	if pattern == "" {
		panic("flow: published.Manifest's files have no keyPattern")
	}
	return regexp.MustCompile(pattern)
}()

// isFilePath reports whether path is the path of a snapshot's file but the
// manifest, as the pull's path rules take it (PRD §7.5): of the forms of
// filePattern, a board's of a slug of at most slug.MaxLength characters that
// is no name that Windows reserves for a device (slug.Check).
func isFilePath(path string) bool {
	if !filePattern.MatchString(path) {
		return false
	}
	if name, ok := strings.CutPrefix(path, "boards/"); ok {
		return slug.Check(strings.TrimSuffix(name, ".json")) == nil
	}
	return true
}

// contents returns the bytes of the file of the path in fsys, once it has
// checked that the manifest's files list it with the hash of its bytes
// (hashed).
func contents(fsys fs.FS, files map[string]published.Hash, path string) ([]byte, error) {
	var data bytes.Buffer
	if err := hashed(fsys, files, path, &data); err != nil {
		return nil, err
	}
	return data.Bytes(), nil
}

// hashed checks that the manifest's files list the file of the path in fsys
// with the hash of its bytes, which it reads as they stream and writes to w,
// so that it holds none of them when w is io.Discard, as of an asset, which
// the loader does not decode, and which may be large.
func hashed(fsys fs.FS, files map[string]published.Hash, path string, w io.Writer) error {
	want, ok := files[path]
	if !ok {
		return fmt.Errorf("%s is not among the manifest's files", path)
	}
	f, err := fsys.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(h, w), f); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if got := fileHash(h.Sum(nil)); got != want {
		return fmt.Errorf("%s: its hash is %s, not the manifest's %q", path, got, want)
	}
	return nil
}

// fileHash returns the hash of a file whose SHA-256 is sum as the manifest
// lists it (published.Hash): sha256: and the sum's lowercase hexadecimal.
func fileHash(sum []byte) published.Hash {
	return published.Hash("sha256:" + hex.EncodeToString(sum))
}

// readFile reads the file of the path in fsys into v, with published.Decode,
// once it has checked its hash (contents).
func (l *loader) readFile(fsys fs.FS, path string, v any) error {
	data, err := contents(fsys, l.s.manifest.Files, path)
	if err != nil {
		return err
	}
	l.read[path] = true
	return decode(path, data, v)
}

// decode reads data, the file of the path, into v, with published.Decode.
func decode(path string, data []byte, v any) error {
	if err := published.Decode(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// entity returns an error when id is the ID of an entity that l has read
// already, and otherwise records it.
func (l *loader) entity(id string) error {
	if l.entities[id] {
		return fmt.Errorf("%s is the ID of two of its entities", id)
	}
	l.entities[id] = true
	return nil
}

// valueOf returns v, a value of the published format or of a save state, as
// a value of the type typ, an enum's one of options, as the format's values
// are typed by their declarations (§5.8.9), or an error. A string is a value
// of the language, of at most semantics.MaxStringLength characters, and a
// text one of at most value.MaxTextLength, neither with a NUL, as the
// format's schemas take them (value.Schema), and as no expression builds
// another (§5.8.1, §5.8.3). An enum's value is a string of the language too,
// which an expression reads as a string and may assign to a string variable
// (§5.8.3), so that it is bounded as a string is, though the schemas bound an
// enum's option only by its length: a runtime that held an option with a NUL
// would save a string variable that no runtime loads.
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
	if err := json.Unmarshal(read.JSON(), &str); err != nil {
		return expr.Value{}, err
	}
	bound := semantics.MaxStringLength
	if typ == value.Text {
		bound = value.MaxTextLength
	}
	if utf8.RuneCountInString(str) > bound || strings.ContainsRune(str, 0) {
		return expr.Value{}, fmt.Errorf("is a value of the type %s of more than %d characters, or with a NUL, which "+
			"no value is", typ, bound)
	}
	return expr.StringValue(str), nil
}

// jsonOf returns v, of the type typ, as the published format and a save state
// hold a value (value.Value). v is a value of the language, as every value
// that a runtime holds is (asSaved), which it writes as it is; of any other, an
// int beyond ±(2^53 − 1), a float that is not finite, or a string that is not
// UTF-8, it returns another value, or none.
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

// declare reads the variables, the component types' definitions, and the
// components' runtime attributes, with their values in the snapshot.
func (l *loader) declare(variables published.Variables, types published.ComponentTypes,
	components published.Components) error {
	s := l.s
	for i := range variables.Variables {
		v := &variables.Variables[i]
		if err := l.entity(v.ID); err != nil {
			return fmt.Errorf("%s: %w", published.VariablesPath, err)
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
	typeKeys := make(map[string]bool)
	for _, t := range types.ComponentTypes {
		if err := l.entity(t.ID); err != nil {
			return fmt.Errorf("%s: %w", published.ComponentTypesPath, err)
		}
		if typeKeys[t.Key] {
			return fmt.Errorf("two component types have the key %s", t.Key)
		}
		typeKeys[t.Key] = true
		if err := l.define(t); err != nil {
			return err
		}
		definitions[t.ID] = t.Attributes
	}
	for _, c := range components.Components {
		if err := l.entity(c.ID); err != nil {
			return fmt.Errorf("%s: %w", published.ComponentsPath, err)
		}
		l.components[c.ID] = true
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

// define checks the attribute definitions of the component type t: each its
// own entity, of a key of its own among the type's, a text never runtime, an
// enum's options each there once, and its default of its type, an enum's one
// of its options.
func (l *loader) define(t published.ComponentType) error {
	keys := make(map[string]bool)
	for _, d := range t.Attributes {
		if err := l.entity(d.ID); err != nil {
			return fmt.Errorf("%s: %w", published.ComponentTypesPath, err)
		}
		switch {
		case keys[d.Key]:
			return fmt.Errorf("two definitions of the component type %s have the key %s", t.ID, d.Key)
		case d.Type == value.Text && d.Runtime:
			return fmt.Errorf("the definition %s is a text, which is never runtime", d.ID)
		case len(slices.Compact(slices.Sorted(slices.Values(d.Options)))) != len(d.Options):
			return fmt.Errorf("the definition %s has an option twice", d.ID)
		}
		keys[d.Key] = true
		if _, err := valueOf(d.Default, d.Type, d.Options); err != nil {
			return fmt.Errorf("the default of the definition %s %w", d.ID, err)
		}
	}
	return nil
}

// addNodes adds the nodes of the board b's file f, and their ports.
func (l *loader) addNodes(b string, f *published.Board) error {
	for _, n := range f.Nodes {
		id, ports := n.NodeID(), map[string][]*published.Connection{}
		if err := l.entity(id); err != nil {
			return err
		}
		switch n := n.(type) {
		case *published.Element:
			ports[out] = nil
		case *published.Branch:
			last := len(n.Outputs) - 1
			for i, o := range n.Outputs {
				if err := l.entity(o.ID); err != nil {
					return err
				}
				if o.Order != published.Int(i) {
					return fmt.Errorf("the branch %s's outputs are not ordered 0, 1, 2, and so on", n.ID)
				}
				// The first output is the if, and the last may be an else,
				// which the runtime picks once no output before it holds.
				if (o.Kind == published.OutputIf) != (i == 0) || o.Kind == published.OutputElse && i != last {
					return fmt.Errorf("the branch %s's outputs are not one if, then any elseifs, then an else, if "+
						"it has one", n.ID)
				}
				ports[o.ID] = nil
			}
		}
		l.s.nodes[id] = &node{board: b, node: n, ports: ports}
	}
	return nil
}

// addConnections adds the connections of the board b's file f to their
// nodes' ports, and checks the board's expressions, texts, and jumpers.
func (l *loader) addConnections(b string, f *published.Board, p *expr.Project) error {
	s := l.s
	for i := range f.Connections {
		c := &f.Connections[i]
		from, to := s.nodes[c.From], s.nodes[c.To]
		if err := l.entity(c.ID); err != nil {
			return err
		}
		if from == nil || from.board != b || to == nil || to.board != b {
			return fmt.Errorf("the connection %s does not join two nodes of the board", c.ID)
		}
		if _, ok := from.ports[c.FromPort]; !ok {
			return fmt.Errorf("the connection %s leaves %s by %s, which is no port of it", c.ID, c.From, c.FromPort)
		}
		from.ports[c.FromPort] = append(from.ports[c.FromPort], c)
		if c.Condition != nil {
			if err := l.check(c.Condition.AST, expr.ContextCondition, p, b); err != nil {
				return fmt.Errorf("the condition of %s: %w", c.ID, err)
			}
		}
		if c.OnTraverse != nil {
			if err := l.check(c.OnTraverse.AST, expr.ContextScript, p, b); err != nil {
				return fmt.Errorf("the script of %s: %w", c.ID, err)
			}
		}
		if c.Label != nil {
			if err := l.checkSegments(b, c.Label.Segments); err != nil {
				return fmt.Errorf("the label of %s: %w", c.ID, err)
			}
		}
	}
	for _, n := range f.Nodes {
		if err := l.checkNode(b, n, p); err != nil {
			return err
		}
	}
	for _, n := range f.Nodes {
		ports := s.nodes[n.NodeID()].ports
		for _, port := range slices.Sorted(maps.Keys(ports)) {
			cs := ports[port]
			slices.SortFunc(cs, func(a, b *published.Connection) int { return cmp.Compare(a.Order, b.Order) })
			for i, c := range cs {
				if c.Order != published.Int(i) {
					return fmt.Errorf("the connections of %s's port %s are not ordered 0, 1, 2, and so on", n.NodeID(),
						port)
				}
			}
		}
	}
	return nil
}

// check checks the AST n of the board b in the context ctx against the
// snapshot's declarations (expr.CheckResolved), and keeps it, so that the
// manifest's astVersion is checked against its constructs.
func (l *loader) check(n expr.Node, ctx expr.Context, p *expr.Project, b string) error {
	if err := expr.CheckResolved(n, ctx, p, b); err != nil {
		return err
	}
	l.asts = append(l.asts, n)
	return nil
}

// checkNode checks the scripts, the speaker, the tags, the text, the
// outputs' conditions, and the target of the node n of the board b.
func (l *loader) checkNode(b string, n published.Node, p *expr.Project) error {
	scripts := func(id string, onEnter, onExit *published.Script) error {
		for _, sc := range []*published.Script{onEnter, onExit} {
			if sc != nil {
				if err := l.check(sc.AST, expr.ContextScript, p, b); err != nil {
					return fmt.Errorf("a script of %s: %w", id, err)
				}
			}
		}
		return nil
	}
	switch n := n.(type) {
	case *published.Element:
		if n.Speaker != "" && !l.components[n.Speaker] {
			return fmt.Errorf("the speaker of %s is %s, which is no component of the snapshot", n.ID, n.Speaker)
		}
		tags := make(map[string]bool)
		for _, tag := range n.Tags {
			key := input.FoldCase(tag)
			if tags[key] {
				return fmt.Errorf("two tags of %s are the same but for their case", n.ID)
			}
			tags[key] = true
		}
		if n.Text != nil {
			if err := l.checkSegments(b, n.Text.Segments); err != nil {
				return fmt.Errorf("the text of %s: %w", n.ID, err)
			}
		}
		return scripts(n.ID, n.OnEnter, n.OnExit)
	case *published.Branch:
		for _, o := range n.Outputs {
			if o.Condition != nil {
				if err := l.check(o.Condition.AST, expr.ContextCondition, p, b); err != nil {
					return fmt.Errorf("the condition of %s: %w", o.ID, err)
				}
			}
		}
		return scripts(n.ID, n.OnEnter, n.OnExit)
	case *published.Jumper:
		if t := n.Target; t != nil {
			target := l.s.nodes[t.NodeID]
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

// linkMark is the mark of a segment of a link, which has an href.
const linkMark = "link"

// checkSegments checks the segments of a text or a label of the board b:
// that each mention is of a component of the snapshot, and each value of a
// variable that an expression of the board reads, which a board-scoped
// variable of another board is not (§5.8.3), or of a runtime attribute of a
// component, as an expression's var or attr leaf is (FR-TEXT-02); and that a
// segment's marks are each there once, in alphabetical order, the link among
// them exactly when it has an href.
func (l *loader) checkSegments(b string, segs richtext.Segments) error {
	s := l.s
	for _, seg := range segs {
		for i := 1; i < len(seg.Marks); i++ {
			if seg.Marks[i-1] >= seg.Marks[i] {
				return errors.New("a segment's marks are not each there once, in alphabetical order")
			}
		}
		if slices.Contains(seg.Marks, linkMark) != (seg.Href != "") {
			return errors.New("a segment has the link mark without an href, or an href without the link mark")
		}
		switch {
		case seg.Kind == richtext.SegmentMention:
			if !l.components[seg.Ref] {
				return fmt.Errorf("a mention is of %s, which is no component of the snapshot", seg.Ref)
			}
		case seg.Kind != richtext.SegmentValue:
		case seg.Attribute != "":
			if _, ok := s.initial[attributeKey{seg.Ref, seg.Attribute}]; !ok {
				return fmt.Errorf("a value is of %s's %s, which is no runtime attribute of the snapshot", seg.Ref,
					seg.Attribute)
			}
		case s.declared[seg.Ref] == nil:
			return fmt.Errorf("a value is of %s, which is no variable of the snapshot", seg.Ref)
		case s.declared[seg.Ref].Scope == published.ScopeBoard && s.declared[seg.Ref].BoardID != b:
			return fmt.Errorf("a value is of %s, a variable of another board, which an expression of the board does "+
				"not read (board_variable_out_of_scope)", seg.Ref)
		}
	}
	return nil
}
