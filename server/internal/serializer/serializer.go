// Package serializer writes a project's draft as the files of a snapshot of
// the published game format (PRD §6, FR-SNAP-03, plan item 76, note N83),
// which a release's build stores (item 78) and the draft's export serves
// (item 79): Load reads the draft in one transaction that reads as of one
// moment, and Serialize writes its files, the same bytes for the same
// content and stamp, whatever the order of the rows that it read, the
// server, the platform, or the time.
//
// Each file is what the types of package published write of it, laid out as
// §6.2 says (Layout): its keys sorted by their code points, indented by two
// spaces, HTML not escaped, and a line feed at its end, its numbers as their
// declarations type them (value.Value.Read), a node's position to hundredths,
// its arrays in their author's order where that order means something, and
// otherwise by ID, its optional fields left out when they are not set, and
// nothing of who made or changed what, nor when, outside the manifest's
// generator and snapshot blocks.
//
// What it writes is what play mode's projection writes of the same draft
// (web/app/src/play/projection.ts, note N78), which item 89 cross-checks,
// field by field and stand-in by stand-in: what a draft holds that no
// snapshot may, as an expression that does not compile or a reference to
// what is in the trash, it stands in for in a draft's files as the
// projection does, and lists (StandIn), and a release that would need a
// stand-in it refuses to write (UnpublishableError), as it does one of a
// jumper without a target, which a draft's may lack and a release's may not,
// so that no release holds an expression that failed to compile (FR-EXPR-03
// AC1), nor what the reference runtime refuses of a release.
package serializer

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
	"github.com/CyberCute-Studio/storycreep/server/internal/version"
)

// GeneratorName is what the server's snapshots name as what wrote them.
const GeneratorName = "storycreep"

// Generator returns what the server's snapshots name as what wrote them:
// GeneratorName, and the version of the running build (FR-ADMIN-05).
func Generator() published.GeneratorInfo {
	return published.GeneratorInfo{Name: GeneratorName, Version: version.Get().Version}
}

// Release is a release that a snapshot is of: its ID, a snapshot's, its
// version, which its author chose, and its note (FR-SNAP-01).
type Release struct {
	ID, Version, Note string
}

// Stamp is what a manifest says of a snapshot beyond its content, in its
// generator and snapshot blocks alone (§6.2, FR-SNAP-03 AC3): what wrote it,
// the release that it is, or nil for the draft channel (§7.8), and when the
// release was published, or the draft pulled, which the manifest holds to
// the second, in UTC.
type Stamp struct {
	Generator published.GeneratorInfo
	Release   *Release
	At        time.Time
}

// Snapshot is what Serialize wrote: the snapshot's files, by their paths,
// the manifest's among them, the manifest, the content hash, and what it
// stood in for, in the order of the boards, by ID, and of each board's
// nodes, by ID, each node's in the order of its fields, and its
// connections, by ID, then of the board's entry node, and last of the
// project's entry.
type Snapshot struct {
	Files       map[string][]byte
	Manifest    published.Manifest
	ContentHash published.Hash
	StandIns    []StandIn
}

// Build reads the draft of the project projectID of the workspace ws in tx,
// a transaction of the workspace that reads as of one moment (Load), and
// writes it as Serialize does.
func Build(ctx context.Context, tx pgx.Tx, ws, projectID ids.UUID, s Stamp) (*Snapshot, error) {
	d, err := Load(ctx, tx, ws, projectID)
	if err != nil {
		return nil, err
	}
	return Serialize(d, s)
}

// Serialize writes the draft d as the files of a snapshot of the published
// format (§6.3), stamped with s: the manifest, storycreep.json, the
// project's settings, variables, component types, and components, and a
// file of each board, as play mode's projection writes them (note N78).
//
// It writes every board, node, connection, variable, component, and
// attribute definition of d, and the component types that a component is
// of, each array as §6.2 orders it: a type's attribute definitions, a
// branch's outputs, and each port's connections in their author's order, by
// their keys (package orderkey), and the rest by ID. A branch's conditional
// outputs are each of the kind of its place, its first if and the others
// elseif, and its else last, which it has only while it has a conditional
// output (FR-EDIT-04); a connection's and an output's order is its place
// among its port's or its branch's, from 0. Each expression is its source
// and its AST, which the settings' signals and the manifest's astVersion are
// read from, and each text its segments, with each mention's component's
// name and each value's name (FR-TEXT-03 AC2), and its preview.
//
// What d holds that no snapshot may it stands in for (StandIn), and lists in
// Snapshot.StandIns, in a draft's files; a release's it refuses with an
// *UnpublishableError, writing nothing, and so a release of a jumper without
// a target, which a draft's may lack (ReasonNone). It fails, writing
// nothing, on a draft that no project holds, as two boards of one slug, or a
// value that is not of its declaration's type, and on a stamp whose release
// has no version, or an ID that is no snapshot's, whose generator is not
// named, or that has no moment.
func Serialize(d *Draft, s Stamp) (*Snapshot, error) {
	if err := checkStamp(s); err != nil {
		return nil, err
	}
	w := newWriter(d, s.Release != nil)
	files, err := w.content()
	if err != nil {
		return nil, err
	}
	if s.Release != nil && len(w.standIns) > 0 {
		return nil, &UnpublishableError{StandIns: w.standIns}
	}
	hashes := make(map[string]published.Hash, len(files))
	var all strings.Builder
	for _, path := range slices.Sorted(maps.Keys(files)) {
		hashes[path] = hashOf(files[path])
		all.WriteString(path + " " + string(hashes[path]) + "\n")
	}
	contentHash := hashOf([]byte(all.String()))
	var snapshot published.Snapshot = &published.DraftSnapshot{ContentHash: contentHash,
		PulledAt: published.TimestampOf(s.At)}
	if r := s.Release; r != nil {
		snapshot = &published.ReleaseSnapshot{ContentHash: contentHash, ID: r.ID, Note: r.Note,
			PublishedAt: published.TimestampOf(s.At), Version: r.Version}
	}
	m := published.Manifest{ASTVersion: published.Int(expr.VersionOf(w.asts...)), Boards: w.index,
		Counts: w.counts, Entry: w.entry, Files: hashes, Generator: s.Generator,
		Project:       published.ProjectInfo{ID: d.Project.ID, Name: d.Project.Name, Slug: d.Project.Slug},
		SchemaVersion: published.SchemaVersion, Snapshot: snapshot}
	if files[published.ManifestPath], err = file(published.ManifestPath, m); err != nil {
		return nil, err
	}
	return &Snapshot{Files: files, Manifest: m, ContentHash: contentHash, StandIns: w.standIns}, nil
}

// checkStamp returns an error for a stamp that would write a manifest that
// the schema refuses: a generator without a name or a version, or a release
// without a version, or whose ID is no snapshot's in its canonical spelling;
// or one that would write a wrong one, of a moment that is not set, which
// would say that the release was published, or the draft pulled, in the
// year 1.
func checkStamp(s Stamp) error {
	switch r := s.Release; {
	case s.Generator.Name == "" || s.Generator.Version == "":
		return errors.New("serializer: the stamp's generator has no name, or no version")
	case r != nil && r.Version == "":
		return errors.New("serializer: the stamp's release has no version")
	case r != nil && !ids.IsCanonical(ids.Snapshot, r.ID):
		return fmt.Errorf("serializer: the stamp's release has the ID %q, which is no snapshot's in its canonical "+
			"spelling", r.ID)
	case s.At.IsZero():
		return errors.New("serializer: the stamp has no moment")
	}
	return nil
}

// hashOf returns the hash of a file's bytes, sha256: and their SHA-256 in
// lowercase hexadecimal, as the manifest holds it.
func hashOf(data []byte) published.Hash {
	sum := sha256.Sum256(data)
	return published.Hash("sha256:" + hex.EncodeToString(sum[:]))
}

// file returns v, a file's value of package published's types, as the file's
// bytes, laid out as §6.2 says, or fails where the types refuse to write it,
// as where published.Decode would refuse what they write.
func file(path string, v any) ([]byte, error) {
	data, err := schemagen.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("serializer: %s: %w", path, err)
	}
	out, err := Layout(data)
	if err != nil {
		return nil, fmt.Errorf("serializer: %s: %w", path, err)
	}
	return out, nil
}

// writer writes the files of a draft's snapshot, with what it reads of the
// draft once, and what it stood in for as it wrote them.
type writer struct {
	d *Draft
	// release is whether the snapshot is a release, of which a jumper
	// without a target is listed as a stand-in too, which refuses it.
	release bool
	boards  []*Board
	// boardIDs are the IDs of the snapshot's boards.
	boardIDs map[string]bool
	// nodesOf are each board's nodes, by ID, and nodes every node of the
	// snapshot's boards.
	nodesOf map[string][]*Node
	nodes   map[string]*Node
	// outputsOf are each branch's outputs, and connectionsOf each board's
	// connections, by ID.
	outputsOf     map[string][]*Output
	connectionsOf map[string][]*Connection
	// variables are those of the project and of the snapshot's boards, by
	// ID.
	variables    []*Variable
	variableByID map[string]*Variable
	// types are the component types, definitionsOf each type's definitions
	// in their author's order, components the components of a type that
	// the snapshot has, by ID.
	types         map[string]*ComponentType
	definitionsOf map[string][]*Attribute
	// definitionByID are the definitions of definitionsOf, by ID.
	definitionByID map[string]*Attribute
	components     []*Component
	componentByID  map[string]*Component

	standIns []StandIn
	asts     []expr.Node
	signals  map[string]bool
	index    []published.ManifestBoard
	counts   published.Counts
	entry    *published.Entry
}

// newWriter returns a writer of d, a release's if release, with what it
// reads of d sorted as the files order it.
func newWriter(d *Draft, release bool) *writer {
	w := &writer{d: d, release: release, boardIDs: map[string]bool{}, nodesOf: map[string][]*Node{},
		nodes: map[string]*Node{}, outputsOf: map[string][]*Output{}, connectionsOf: map[string][]*Connection{},
		variableByID: map[string]*Variable{}, types: map[string]*ComponentType{},
		definitionsOf: map[string][]*Attribute{}, definitionByID: map[string]*Attribute{},
		componentByID: map[string]*Component{}, signals: map[string]bool{}}
	for i := range d.Boards {
		w.boards = append(w.boards, &d.Boards[i])
		w.boardIDs[d.Boards[i].ID] = true
	}
	slices.SortFunc(w.boards, func(a, b *Board) int { return strings.Compare(a.ID, b.ID) })
	for i := range d.Nodes {
		n := &d.Nodes[i]
		if w.boardIDs[n.Board] {
			w.nodesOf[n.Board] = append(w.nodesOf[n.Board], n)
			w.nodes[n.ID] = n
		}
	}
	for _, list := range w.nodesOf {
		slices.SortFunc(list, func(a, b *Node) int { return strings.Compare(a.ID, b.ID) })
	}
	for i := range d.Outputs {
		o := &d.Outputs[i]
		w.outputsOf[o.Node] = append(w.outputsOf[o.Node], o)
	}
	for i := range d.Connections {
		c := &d.Connections[i]
		w.connectionsOf[c.Board] = append(w.connectionsOf[c.Board], c)
	}
	for _, list := range w.connectionsOf {
		slices.SortFunc(list, func(a, b *Connection) int { return strings.Compare(a.ID, b.ID) })
	}
	for i := range d.Variables {
		v := &d.Variables[i]
		if v.Scope == published.ScopeGlobal || v.Board != "" && w.boardIDs[v.Board] {
			w.variables = append(w.variables, v)
			w.variableByID[v.ID] = v
		}
	}
	slices.SortFunc(w.variables, func(a, b *Variable) int { return strings.Compare(a.ID, b.ID) })
	for i := range d.ComponentTypes {
		t := &d.ComponentTypes[i]
		w.types[t.ID] = t
		w.definitionsOf[t.ID] = []*Attribute{}
	}
	for i := range d.Attributes {
		a := &d.Attributes[i]
		if _, ok := w.types[a.ComponentType]; ok {
			w.definitionsOf[a.ComponentType] = append(w.definitionsOf[a.ComponentType], a)
			w.definitionByID[a.ID] = a
		}
	}
	for _, defs := range w.definitionsOf {
		slices.SortFunc(defs, func(a, b *Attribute) int { return compareChildren(a.Order, a.ID, b.Order, b.ID) })
	}
	for i := range d.Components {
		c := &d.Components[i]
		if _, ok := w.types[c.ComponentType]; ok {
			w.components = append(w.components, c)
			w.componentByID[c.ID] = c
		}
	}
	slices.SortFunc(w.components, func(a, b *Component) int { return strings.Compare(a.ID, b.ID) })
	return w
}

// compareChildren orders two siblings by their keys, a and b, as their bytes
// compare, and those with the same key by their IDs, as package orderkey
// orders them, and the database's reads by order and id: siblings are of
// one kind, whose IDs share their prefix, so that they compare as their
// bodies do.
func compareChildren(a, aID, b, bID string) int {
	return cmp.Or(strings.Compare(a, b), strings.Compare(aID, bID))
}

// content returns the snapshot's files but the manifest, each laid out as
// §6.2 says, having set what the manifest holds of them.
func (w *writer) content() (map[string][]byte, error) {
	files := make(map[string][]byte)
	boardEntries := map[string]string{}
	for _, b := range w.boards {
		path := published.BoardPath(b.Slug)
		if _, ok := files[path]; ok {
			return nil, fmt.Errorf("serializer: two boards have the slug %q", b.Slug)
		}
		f, err := w.board(b)
		if err != nil {
			return nil, err
		}
		if files[path], err = file(path, f); err != nil {
			return nil, err
		}
		boardEntries[b.ID] = f.Board.EntryNodeID
		w.index = append(w.index, published.ManifestBoard{EntryNodeID: f.Board.EntryNodeID, File: path, ID: b.ID,
			Name: b.Name, Slug: b.Slug})
	}
	w.entry = w.projectEntry(boardEntries)
	variables, err := w.variablesFile()
	if err != nil {
		return nil, err
	}
	types, components, err := w.componentFiles()
	if err != nil {
		return nil, err
	}
	settings := published.Settings{Entry: w.entry, SchemaVersion: published.SchemaVersion,
		Signals: slices.Sorted(maps.Keys(w.signals))}
	for _, f := range []struct {
		path string
		v    any
	}{{published.SettingsPath, settings}, {published.VariablesPath, variables},
		{published.ComponentTypesPath, types}, {published.ComponentsPath, components}} {
		if files[f.path], err = file(f.path, f.v); err != nil {
			return nil, err
		}
	}
	w.counts.Boards = published.Int(len(w.index))
	w.counts.Components = published.Int(len(components.Components))
	w.counts.Nodes = published.Int(len(w.nodes))
	w.counts.Variables = published.Int(len(variables.Variables))
	return files, nil
}

// at is where an expression or a text is, as its stand-in says: the entity
// that it is of, which kind of owner that is, its field, and the node that a
// link opens it by.
type at struct {
	entity, owner, field, node string
}

// board returns the file of the board b, counting its connections.
func (w *writer) board(b *Board) (published.Board, error) {
	f := published.Board{Board: published.BoardInfo{ID: b.ID, Name: b.Name, Slug: b.Slug},
		Connections: []published.Connection{}, Nodes: published.Nodes{}, SchemaVersion: published.SchemaVersion}
	// The ports of each node of the board: an element's out, a branch's
	// outputs of the file, and a jumper's none.
	ports := map[string]map[string]bool{}
	for _, n := range w.nodesOf[b.ID] {
		node, nodePorts := w.node(b, n)
		ports[n.ID] = nodePorts
		f.Nodes = append(f.Nodes, node)
	}

	// Each port's connections, in their author's order, which their order
	// counts from 0, and why each other connection is left out.
	type port struct{ node, port string }
	byPort := map[port][]*Connection{}
	dropped := map[string]string{}
	for _, c := range w.connectionsOf[b.ID] {
		p := port{c.From, portOf(c)}
		to := w.nodes[c.To]
		switch {
		case !ports[c.From][p.port]:
			dropped[c.ID] = ReasonPort
		case to == nil || to.Board != b.ID:
			dropped[c.ID] = ReasonTarget
		default:
			byPort[p] = append(byPort[p], c)
		}
	}
	order := map[string]int{}
	for _, list := range byPort {
		slices.SortFunc(list, func(a, b *Connection) int { return compareChildren(a.Order, a.ID, b.Order, b.ID) })
		for i, c := range list {
			order[c.ID] = i
		}
	}
	for _, c := range w.connectionsOf[b.ID] {
		if reason, ok := dropped[c.ID]; ok {
			if w.nodes[c.From] != nil {
				w.standIns = append(w.standIns, StandIn{Kind: StandInConnection, Board: b.ID, Node: c.From,
					Entity: c.ID, Reason: reason})
			}
			continue
		}
		f.Connections = append(f.Connections, w.connection(b, c, order[c.ID]))
	}
	w.counts.Connections += published.Int(len(f.Connections))

	if e := b.EntryNode; e != "" {
		if n := w.nodes[e]; n == nil || n.Board != b.ID {
			w.standIns = append(w.standIns, StandIn{Kind: StandInBoardEntry, Board: b.ID, Entity: b.ID, Ref: e})
		} else {
			f.Board.EntryNodeID = e
		}
	}
	return f, nil
}

// portOf returns the port that the connection c leaves its node by, as the
// published format names it: out, or the ID of a branch's output.
func portOf(c *Connection) string {
	if c.FromOutput == "" {
		return "out"
	}
	return c.FromOutput
}

// node returns the node n of the board b as its file holds it, and its ports.
func (w *writer) node(b *Board, n *Node) (published.Node, map[string]bool) {
	pos := published.Position{X: hundredths(n.X), Y: hundredths(n.Y)}
	of := func(field string) at { return at{entity: n.ID, owner: OwnerNode, field: field, node: n.ID} }
	switch n.Type {
	case published.NodeBranch:
		outputs := branchOutputs(w.outputsOf[n.ID])
		br := &published.Branch{ID: n.ID, Position: pos, Title: n.Title,
			OnEnter: w.script(n.OnEnter, b.ID, of(FieldOnEnter)), OnExit: w.script(n.OnExit, b.ID, of(FieldOnExit)),
			Outputs: []published.BranchOutput{}}
		ports := map[string]bool{}
		for i, o := range outputs {
			ports[o.out.ID] = true
			out := published.BranchOutput{ID: o.out.ID, Kind: o.kind, Order: published.Int(i)}
			if o.kind != published.OutputElse {
				where := at{entity: o.out.ID, owner: OwnerOutput, field: FieldCondition, node: n.ID}
				if out.Condition = w.condition(o.out.Condition, b.ID, where); out.Condition == nil {
					// A conditional output without a condition, which never
					// holds, as one that does not compile.
					w.standIns = append(w.standIns, StandIn{Kind: StandInExpression, Board: b.ID, Node: n.ID,
						Entity: o.out.ID, Owner: OwnerOutput, Field: FieldCondition})
					out.Condition = &published.Condition{AST: literalFalse()}
				}
			}
			br.Outputs = append(br.Outputs, out)
		}
		return br, ports
	case published.NodeJumper:
		j := &published.Jumper{ID: n.ID, Position: pos, Title: n.Title}
		reason := ""
		switch t, to := n.TargetNode, w.nodes[n.TargetNode]; {
		case t == "":
			// A jumper without a target, which a draft's may lack, and a
			// release's may not (§7.8), as the reference runtime refuses it.
			if w.release {
				reason = ReasonNone
			}
		case to == nil:
			reason = ReasonGone
		case to.Type == published.NodeJumper:
			reason = ReasonJumper
		case n.TargetBoard != "" && n.TargetBoard != to.Board:
			reason = ReasonBoard
		default:
			j.Target = &published.JumperTarget{BoardID: to.Board, NodeID: to.ID}
		}
		if reason != "" {
			w.standIns = append(w.standIns, StandIn{Kind: StandInTarget, Board: b.ID, Node: n.ID, Entity: n.ID,
				Ref: n.TargetNode, Reason: reason})
		}
		return j, map[string]bool{}
	}
	el := &published.Element{Cover: n.Cover, ID: n.ID, IsEnding: n.IsEnding, Kind: n.Kind, Position: pos,
		Title: n.Title}
	if el.Kind == "" {
		el.Kind = "dialogue"
	}
	if s := n.Speaker; s != "" {
		if w.componentByID[s] != nil {
			el.Speaker = s
		} else {
			w.standIns = append(w.standIns, StandIn{Kind: StandInSpeaker, Board: b.ID, Node: n.ID, Entity: n.ID,
				Ref: s})
		}
	}
	if segs := w.segments(n.Text, b.ID, at{entity: n.ID, field: FieldText, node: n.ID}); len(segs) > 0 {
		el.Text = &published.Text{Segments: segs, Preview: richtext.Preview(richtext.PlainText(segs))}
	}
	el.OnEnter = w.script(n.OnEnter, b.ID, of(FieldOnEnter))
	el.OnExit = w.script(n.OnExit, b.ID, of(FieldOnExit))
	if len(n.Tags) > 0 {
		el.Tags = slices.Clone(n.Tags)
	}
	return el, map[string]bool{"out": true}
}

// hundredths returns n hundredths of a unit as a position's coordinate: the
// double nearest to it, which encoding/json writes as the decimal of n
// hundredths, without trailing zeros, as JSON.stringify writes it too, as
// 240, 120.5, or -0.05 (§6.2).
func hundredths(n int64) float64 {
	return float64(n) / 100
}

// literalFalse returns the AST of false, which a condition that does not
// compile stands in for (StandInExpression).
func literalFalse() expr.Node {
	return &expr.Lit{Type: value.Bool}
}

// placedOutput is an output of a branch with the kind of its place.
type placedOutput struct {
	out  *Output
	kind string
}

// branchOutputs returns a branch's outputs, each of the kind of its place
// (FR-EDIT-04): its conditional ones in their author's order, the first its
// if and the others its elseifs, and then its else, which it has only while
// it has a conditional output, as play mode's projection places them.
func branchOutputs(outputs []*Output) []placedOutput {
	var conditional []*Output
	var otherwise *Output
	for _, o := range outputs {
		if o.Kind == published.OutputElse {
			if otherwise == nil || o.ID < otherwise.ID {
				otherwise = o
			}
			continue
		}
		conditional = append(conditional, o)
	}
	slices.SortFunc(conditional, func(a, b *Output) int { return compareChildren(a.Order, a.ID, b.Order, b.ID) })
	var out []placedOutput
	for i, o := range conditional {
		kind := published.OutputElseIf
		if i == 0 {
			kind = published.OutputIf
		}
		out = append(out, placedOutput{out: o, kind: kind})
	}
	if otherwise != nil && len(out) > 0 {
		out = append(out, placedOutput{out: otherwise, kind: published.OutputElse})
	}
	return out
}

// connection returns the connection c of the board b, of the order among its
// port's, as its file holds it.
func (w *writer) connection(b *Board, c *Connection, order int) published.Connection {
	// A connection that leaves an element is a choice; one that leaves a
	// branch the flow passes on by alone.
	owner := OwnerChoice
	if w.nodes[c.From].Type == published.NodeBranch {
		owner = OwnerConnection
	}
	of := func(field string) at { return at{entity: c.ID, owner: owner, field: field, node: c.From} }
	out := published.Connection{From: c.From, FromPort: portOf(c), ID: c.ID, Order: published.Int(order), To: c.To}
	if segs := w.segments(c.Label, b.ID, at{entity: c.ID, field: FieldLabel, node: c.From}); len(segs) > 0 {
		out.Label = &published.Label{Segments: segs}
	}
	out.Condition = w.condition(c.Condition, b.ID, of(FieldCondition))
	out.OnTraverse = w.script(c.OnTraverse, b.ID, of(FieldOnTraverse))
	return out
}

// condition returns the condition e of the board's owner where, or nil for
// none, an expression without a source: false, which never holds, where it
// does not compile, which it stands in for.
func (w *writer) condition(e *Expression, board string, where at) *published.Condition {
	if e == nil || e.Source == "" {
		return nil
	}
	ast := w.compiled(e, board, where)
	if ast == nil {
		ast = literalFalse()
	}
	return &published.Condition{AST: ast, Source: e.Source}
}

// script returns the script e of the board's owner where, or nil for none,
// an expression without a source, and for one that does not compile, which
// it stands in for, so that it does nothing.
func (w *writer) script(e *Expression, board string, where at) *published.Script {
	if e == nil || e.Source == "" {
		return nil
	}
	if ast := w.compiled(e, board, where); ast != nil {
		return &published.Script{AST: ast, Source: e.Source}
	}
	return nil
}

// compiled returns the AST of e, an expression of the board's owner where,
// which the snapshot holds, with the signals that it emits, or nil, with its
// stand-in, for one that does not compile.
func (w *writer) compiled(e *Expression, board string, where at) expr.Node {
	if e.AST == nil {
		w.standIns = append(w.standIns, StandIn{Kind: StandInExpression, Board: board, Node: where.node,
			Entity: where.entity, Owner: where.owner, Field: where.field, Source: e.Source, Code: e.Error})
		return nil
	}
	w.asts = append(w.asts, e.AST)
	expr.Walk(e.AST, func(n expr.Node) {
		if c, ok := n.(*expr.Call); ok && c.Function == "signal" && len(c.Args) > 0 {
			if l, ok := c.Args[0].(*expr.Lit); ok && l.Type == value.String {
				w.signals[l.Str] = true
			}
		}
	})
	return e.AST
}

// segments returns the segments of doc, the text or the label of the board's
// owner where, without the value references and the mentions of what the
// snapshot does not hold, or that the board does not read, which it stands
// in for, the texts on either side of each joining, as the server would
// store the document without it; or nil for none, of a nil or an empty
// document.
func (w *writer) segments(doc *richtext.Doc, board string, where at) richtext.Segments {
	if doc == nil {
		return nil
	}
	// The document is copied only where it holds what is left out, as few
	// texts do.
	unreadable := func(in richtext.Inline) bool {
		_, reason := w.unreadable(in, board)
		return reason != ""
	}
	if anyInline(doc.Blocks, unreadable) {
		doc = &richtext.Doc{Blocks: keepInlines(doc.Blocks, func(in richtext.Inline) bool {
			ref, reason := w.unreadable(in, board)
			if reason == "" {
				return true
			}
			w.standIns = append(w.standIns, StandIn{Kind: StandInReference, Board: board, Node: where.node,
				Entity: where.entity, Field: where.field, Ref: ref, Reason: reason})
			return false
		})}
	}
	segs := doc.Segments(w)
	if len(segs) == 0 {
		return nil
	}
	return segs
}

// anyInline returns whether f holds of an inline node of blocks, those of
// their lists' items among them, passing over a nil block.
func anyInline(blocks []richtext.Block, f func(richtext.Inline) bool) bool {
	for _, b := range blocks {
		switch b := b.(type) {
		case *richtext.Paragraph:
			if b != nil && slices.ContainsFunc(b.Inlines, f) {
				return true
			}
		case *richtext.List:
			if b == nil {
				continue
			}
			for _, item := range b.Items {
				if anyInline(item.Blocks, f) {
					return true
				}
			}
		}
	}
	return false
}

// keepInlines returns blocks with only the inline nodes that keep tells, in
// the order of the document, as new blocks, which leave blocks as they are.
func keepInlines(blocks []richtext.Block, keep func(richtext.Inline) bool) []richtext.Block {
	out := make([]richtext.Block, 0, len(blocks))
	for _, b := range blocks {
		switch b := b.(type) {
		case *richtext.Paragraph:
			if b == nil {
				continue
			}
			p := &richtext.Paragraph{}
			for _, in := range b.Inlines {
				if keep(in) {
					p.Inlines = append(p.Inlines, in)
				}
			}
			out = append(out, p)
		case *richtext.List:
			if b == nil {
				continue
			}
			l := &richtext.List{Ordered: b.Ordered, Start: b.Start}
			for _, item := range b.Items {
				l.Items = append(l.Items, richtext.Item{Blocks: keepInlines(item.Blocks, keep)})
			}
			out = append(out, l)
		}
	}
	return out
}

// unreadable returns what the inline node in, of a text of the board, refers
// to that the snapshot does not hold, or that the board does not read, and
// why, or "" and "" for none: a mention of a component that the snapshot
// does not hold, a value reference of a variable that it does not hold or
// that is another board's, or of an attribute that the component's type does
// not define or that is not runtime (FR-TEXT-02, §5.8.3).
func (w *writer) unreadable(in richtext.Inline, board string) (ref, reason string) {
	switch in := in.(type) {
	case *richtext.Mention:
		if in != nil && w.componentByID[in.Component] == nil {
			return in.Component, ReasonGone
		}
	case *richtext.Value:
		switch {
		case in == nil:
		case in.Attribute == "":
			switch v := w.variableByID[in.Ref]; {
			case v == nil:
				return in.Ref, ReasonGone
			case v.Scope != published.ScopeGlobal && v.Board != board:
				return in.Ref, ReasonBoard
			}
		default:
			switch d := w.definition(in.Ref, in.Attribute); {
			case d == nil:
				return in.Ref, ReasonGone
			case !d.Runtime:
				return in.Ref, ReasonRuntime
			}
		}
	}
	return "", ""
}

// definition returns the attribute definition attribute of the type of the
// component of the snapshot ref, or nil if there is none.
func (w *writer) definition(ref, attribute string) *Attribute {
	c, d := w.componentByID[ref], w.definitionByID[attribute]
	if c == nil || d == nil || d.ComponentType != c.ComponentType {
		return nil
	}
	return d
}

// Value returns the name of the value that a reference of a text reads, as
// an expression names it: a variable's key, or its component's key and its
// attribute's joined by a dot, as elara.trust (richtext.Names), of a
// reference that the snapshot holds, which unreadable kept.
func (w *writer) Value(ref, attribute string) string {
	if attribute == "" {
		if v := w.variableByID[ref]; v != nil {
			return v.Key
		}
		return ""
	}
	if d := w.definition(ref, attribute); d != nil {
		return w.componentByID[ref].Key + "." + d.Key
	}
	return ""
}

// Component returns the name of a component that a text mentions, as the
// snapshot holds it (richtext.Names, FR-TEXT-03 AC2).
func (w *writer) Component(id string) string {
	if c := w.componentByID[id]; c != nil {
		return c.Name
	}
	return ""
}

// projectEntry returns the project's entry: its node, of its board, or its
// board's entry node, of boardEntries, the entry node of each board of the
// snapshot that has one, or nil for none, standing in for one that the
// snapshot does not hold.
func (w *writer) projectEntry(boardEntries map[string]string) *published.Entry {
	p := w.d.Project
	switch {
	case p.EntryNode != "":
		n := w.nodes[p.EntryNode]
		if n != nil && (p.EntryBoard == "" || p.EntryBoard == n.Board) {
			return &published.Entry{BoardID: n.Board, NodeID: n.ID}
		}
		reason := ReasonGone
		if n != nil {
			reason = ReasonBoard
		}
		w.standIns = append(w.standIns, StandIn{Kind: StandInEntry, Board: p.EntryBoard, Entity: p.ID,
			Ref: p.EntryNode, Reason: reason})
	case p.EntryBoard != "":
		if !w.boardIDs[p.EntryBoard] {
			w.standIns = append(w.standIns, StandIn{Kind: StandInEntry, Entity: p.ID, Ref: p.EntryBoard,
				Reason: ReasonGone})
		} else if e := boardEntries[p.EntryBoard]; e != "" {
			return &published.Entry{BoardID: p.EntryBoard, NodeID: e}
		}
	}
	return nil
}

// variablesFile returns the variables' file.
func (w *writer) variablesFile() (published.Variables, error) {
	f := published.Variables{SchemaVersion: published.SchemaVersion, Variables: []published.Variable{}}
	for _, v := range w.variables {
		def, err := read(v.Default, v.Type, nil, "the default of the variable "+v.ID)
		if err != nil {
			return f, err
		}
		pv := published.Variable{Default: def, Description: v.Description, ID: v.ID, Key: v.Key, Scope: v.Scope,
			Type: v.Type}
		if v.Scope == published.ScopeBoard {
			pv.BoardID, pv.ResetOnEnter = v.Board, v.ResetOnEnter
		}
		f.Variables = append(f.Variables, pv)
	}
	return f, nil
}

// componentFiles returns the component types' file, of the types that a
// component of the snapshot is of, and the components' file.
func (w *writer) componentFiles() (published.ComponentTypes, published.Components, error) {
	types := published.ComponentTypes{SchemaVersion: published.SchemaVersion, ComponentTypes: []published.ComponentType{}}
	components := published.Components{SchemaVersion: published.SchemaVersion, Components: []published.Component{}}
	used := map[string]bool{}
	for _, c := range w.components {
		used[c.ComponentType] = true
		values := published.AttributeValues{}
		for _, d := range w.definitionsOf[c.ComponentType] {
			v, ok := c.Values[d.ID]
			if !ok {
				v = d.Default
			}
			var err error
			if values[d.ID], err = read(v, d.Type, d.Options, "the value of the attribute "+d.ID+" of "+c.ID); err != nil {
				return types, components, err
			}
		}
		components.Components = append(components.Components, published.Component{Attributes: values,
			ComponentTypeID: c.ComponentType, ID: c.ID, Image: c.Image, Key: c.Key, Name: c.Name})
	}
	for _, id := range slices.Sorted(maps.Keys(used)) {
		t := w.types[id]
		pt := published.ComponentType{Attributes: []published.AttributeDefinition{}, ID: t.ID, Key: t.Key,
			Name: t.Name}
		for _, d := range w.definitionsOf[id] {
			def, err := read(d.Default, d.Type, d.Options, "the default of the attribute "+d.ID)
			if err != nil {
				return types, components, err
			}
			pd := published.AttributeDefinition{Default: def, ID: d.ID, Key: d.Key, Name: d.Name, Runtime: d.Runtime,
				Type: d.Type}
			if d.Type == value.Enum {
				pd.Options = slices.Clone(d.Options)
			}
			pt.Attributes = append(pt.Attributes, pd)
		}
		types.ComponentTypes = append(types.ComponentTypes, pt)
	}
	return types, components, nil
}

// read returns v, a value of the type typ, whose options are options for an
// enum, in its canonical form, as §6.2 writes it, an int without a point and
// a float in its shortest form, -0 as 0 (value.Value.Read), or fails on one
// that is not of the type, which what names.
func read(v value.Value, typ string, options []string, what string) (value.Value, error) {
	out, problem := v.Read(typ, value.Checks{}, options...)
	if problem != "" {
		return value.Value{}, fmt.Errorf("serializer: %s %s", what, problem)
	}
	return out, nil
}
