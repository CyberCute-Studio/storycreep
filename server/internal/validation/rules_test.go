package validation

import (
	"slices"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// emptyLabel is the label of a connection without one, as the database
// writes it.
const emptyLabel = `{"type": "doc", "content": [{"type": "paragraph"}]}`

// world builds a model for the rules' fixtures, which starts as a clean
// project: the board main, whose entry node start, an element of
// narration with text, leads to end, an ending with text, and of which the
// rules find nothing.
type world struct {
	m          *model
	main       ids.UUID
	start, end ids.UUID
}

// newWorld returns a clean world.
func newWorld() *world {
	w := &world{m: &model{components: map[ids.UUID]bool{}, attributes: map[ids.UUID]bool{}}}
	w.main = w.board()
	w.start = w.element(w.main)
	w.end = w.element(w.main, func(n *node) { n.ending = true })
	w.entry(w.main, w.start)
	w.connect(w.start, w.end)
	return w
}

func (w *world) board() ids.UUID {
	id := ids.New()
	w.m.boards = append(w.m.boards, board{id: id})
	return id
}

// entry makes the node n the entry node of the board b.
func (w *world) entry(b, n ids.UUID) {
	for i := range w.m.boards {
		if w.m.boards[i].id == b {
			w.m.boards[i].entry = n
		}
	}
}

// node adds a node of the type to the board b, as set changes it.
func (w *world) node(b ids.UUID, typ string, set ...func(*node)) ids.UUID {
	n := node{id: ids.New(), board: b, typ: typ}
	if typ == element {
		n.kind = "narration"
	}
	for _, s := range set {
		s(&n)
	}
	w.m.nodes = append(w.m.nodes, n)
	return n.id
}

// element adds an element of narration with text to the board b, as set
// changes it.
func (w *world) element(b ids.UUID, set ...func(*node)) ids.UUID {
	return w.node(b, element, set...)
}

// jumper adds a jumper to the board b, whose target is the node target,
// which may be zero for none.
func (w *world) jumper(b, target ids.UUID) ids.UUID {
	return w.node(b, jumper, func(n *node) { n.targetNode = target })
}

// output adds an output to the branch n.
func (w *world) output(n ids.UUID) ids.UUID {
	o := output{id: ids.New(), node: n, board: w.boardOf(n)}
	w.m.outputs = append(w.m.outputs, o)
	return o.id
}

// connect adds a connection from the node from, by its out, to the node to,
// with an empty label.
func (w *world) connect(from, to ids.UUID) ids.UUID {
	return w.connectBy(from, ids.UUID{}, to, emptyLabel)
}

// connectBy adds a connection from the node from, by the port output, to
// the node to, with the label.
func (w *world) connectBy(from, output, to ids.UUID, label string) ids.UUID {
	c := connection{id: ids.New(), board: w.boardOf(from), from: from, output: output, to: to, label: label}
	w.m.connections = append(w.m.connections, c)
	return c.id
}

// condition gives the connection c the condition n, an AST that compiles,
// or, if n is nil, one that does not.
func (w *world) condition(c ids.UUID, n expr.Node) {
	w.m.expressions = append(w.m.expressions, expression{owner: ref{ids.Connection, c}, field: fieldCondition,
		valid: n != nil, condition: n, errorCode: map[bool]string{true: "", false: "unknown_identifier"}[n != nil]})
}

// variable adds a global variable, which a site reads, or writes, as read and
// written say.
func (w *world) variable(read, written bool) ids.UUID {
	v := variable{id: ids.New(), key: "trust", read: read, written: written}
	w.m.variables = append(w.m.variables, v)
	return v.id
}

func (w *world) boardOf(n ids.UUID) ids.UUID {
	for _, x := range w.m.nodes {
		if x.id == n {
			return x.board
		}
	}
	return ids.UUID{}
}

// found returns the entities of the findings of the code in w's model.
func (w *world) found(code Code) []ref {
	var out []ref
	for _, f := range check(w.m) {
		if f.code == code {
			out = append(out, f.entity)
		}
	}
	return out
}

// ruleCase is a rule's fixtures: a change of a clean world that the rule
// finds something in, the entity that it finds it of, and one that it finds
// nothing in, however near it is.
type ruleCase struct {
	positive func(w *world) ref
	negative func(w *world)
}

// ruleCases are the fixtures of each rule, by its code.
var ruleCases = map[Code]ruleCase{
	ExprInvalid: {
		positive: func(w *world) ref {
			c := w.connect(w.start, w.end)
			w.condition(c, nil)
			return ref{ids.Connection, c}
		},
		negative: func(w *world) { w.condition(w.connect(w.start, w.end), lit(true)) },
	},
	RefDangling: {
		positive: func(w *world) ref {
			w.m.texts = append(w.m.texts, textRef{owner: ref{ids.Node, w.start},
				use: dependency.Use{Kind: dependency.Variable, Target: ids.New(), Access: dependency.Read}})
			return ref{ids.Node, w.start}
		},
		negative: func(w *world) {
			v := w.variable(false, true)
			w.m.texts = append(w.m.texts, textRef{owner: ref{ids.Node, w.start},
				use: dependency.Use{Kind: dependency.Variable, Target: v, Access: dependency.Read}})
		},
	},
	JumperBroken: {
		positive: func(w *world) ref {
			j := w.jumper(w.main, ids.New())
			w.connect(w.start, j)
			return ref{ids.Node, j}
		},
		negative: func(w *world) { w.connect(w.start, w.jumper(w.main, w.end)) },
	},
	JumperInvalid: {
		positive: func(w *world) ref {
			last := w.jumper(w.main, w.end)
			first := w.jumper(w.main, last)
			w.connect(w.start, first)
			return ref{ids.Node, first}
		},
		negative: func(w *world) {
			last := w.jumper(w.main, w.end)
			w.connect(w.start, last)
			w.connect(w.start, w.jumper(w.main, w.end))
		},
	},
	BranchNoOutputs: {
		positive: func(w *world) ref {
			b := w.node(w.main, branch)
			w.connect(w.start, b)
			return ref{ids.Node, b}
		},
		negative: func(w *world) {
			b := w.node(w.main, branch)
			w.connect(w.start, b)
			w.connectBy(b, w.output(b), w.end, emptyLabel)
		},
	},
	PortUnconnected: {
		positive: func(w *world) ref {
			b := w.node(w.main, branch)
			w.connect(w.start, b)
			w.connectBy(b, w.output(b), w.end, emptyLabel)
			return ref{ids.BranchOutput, w.output(b)}
		},
		negative: func(w *world) {
			b := w.node(w.main, branch)
			w.connect(w.start, b)
			w.connectBy(b, w.output(b), w.end, emptyLabel)
			w.connectBy(b, w.output(b), w.end, emptyLabel)
		},
	},
	VarReadNeverWritten: {
		positive: func(w *world) ref { return ref{ids.Variable, w.variable(true, false)} },
		negative: func(w *world) { w.variable(true, true) },
	},
	VarWrittenNeverRead: {
		positive: func(w *world) ref { return ref{ids.Variable, w.variable(false, true)} },
		negative: func(w *world) { w.variable(true, true) },
	},
	NodeUnreachable: {
		positive: func(w *world) ref {
			// A root of a board with an entry node.
			island := w.element(w.main, func(n *node) { n.ending = true })
			return ref{ids.Node, island}
		},
		negative: func(w *world) {
			// A node that a jumper reaches, and the roots of a board
			// without an entry node, each of which is where the flow may
			// start.
			other := w.board()
			target := w.element(other, func(n *node) { n.ending = true })
			w.connect(w.start, w.jumper(w.main, target))
			w.element(other, func(n *node) { n.ending = true })
		},
	},
	NodeEmpty: {
		positive: func(w *world) ref {
			n := w.element(w.main, func(n *node) { n.textEmpty, n.ending = true, true })
			w.connect(w.start, n)
			return ref{ids.Node, n}
		},
		negative: func(w *world) {
			n := w.element(w.main, func(n *node) { n.textEmpty = true })
			w.connect(w.start, n)
			w.connect(n, w.end)
		},
	},
	ChoiceDuplicateLabel: {
		positive: func(w *world) ref {
			x := ref{kind: ids.Variable, id: w.variable(true, true)}
			first := w.connectBy(w.start, ids.UUID{}, w.end, `{"label": "Go"}`)
			w.condition(first, cmpVar(expr.OpGt, x, 1))
			second := w.connectBy(w.start, ids.UUID{}, w.end, `{"label": "Go"}`)
			w.condition(second, cmpVar(expr.OpGt, x, 2))
			return ref{ids.Connection, second}
		},
		negative: func(w *world) {
			// The same label and exclusive conditions, different labels, and
			// an ending's connections, which are no choices.
			x := ref{kind: ids.Variable, id: w.variable(true, true)}
			first := w.connectBy(w.start, ids.UUID{}, w.end, `{"label": "Go"}`)
			w.condition(first, cmpVar(expr.OpGt, x, 1))
			second := w.connectBy(w.start, ids.UUID{}, w.end, `{"label": "Go"}`)
			w.condition(second, &expr.Unary{Op: expr.OpNot, Operand: cmpVar(expr.OpGt, x, 1)})
			w.connectBy(w.start, ids.UUID{}, w.end, `{"label": "Stay"}`)
			w.connectBy(w.end, ids.UUID{}, w.start, `{"label": "Again"}`)
			w.connectBy(w.end, ids.UUID{}, w.start, `{"label": "Again"}`)
		},
	},
	DeadEnd: {
		positive: func(w *world) ref {
			n := w.element(w.main)
			w.connect(w.start, n)
			return ref{ids.Node, n}
		},
		negative: func(w *world) {
			n := w.element(w.main, func(n *node) { n.ending = true })
			w.connect(w.start, n)
			// A jumper is never a dead end, nor a branch an element.
			w.connect(w.start, w.jumper(w.main, w.end))
		},
	},
	SpeakerMissing: {
		positive: func(w *world) ref {
			n := w.element(w.main, func(n *node) { n.kind, n.ending = "dialogue", true })
			w.connect(w.start, n)
			return ref{ids.Node, n}
		},
		negative: func(w *world) {
			speaker := ids.New()
			w.m.components[speaker] = true
			n := w.element(w.main, func(n *node) { n.kind, n.speaker, n.ending = "dialogue", speaker, true })
			w.connect(w.start, n)
		},
	},
	BoardEntryMissing: {
		positive: func(w *world) ref {
			b := w.board()
			w.element(b, func(n *node) { n.ending = true })
			w.element(b, func(n *node) { n.ending = true })
			return ref{ids.Board, b}
		},
		negative: func(w *world) {
			b := w.board()
			a := w.element(b)
			w.connect(a, w.element(b, func(n *node) { n.ending = true }))
		},
	},
	ConnectionSelf: {
		positive: func(w *world) ref { return ref{ids.Connection, w.connect(w.start, w.start)} },
		negative: func(w *world) { w.connect(w.end, w.start) },
	},
	ConnectionParallel: {
		positive: func(w *world) ref {
			return ref{ids.Connection, w.connectBy(w.start, ids.UUID{}, w.end, `{"label": "Again"}`)}
		},
		negative: func(w *world) {
			other := w.element(w.main, func(n *node) { n.ending = true })
			w.connect(w.start, other)
		},
	},
}

// lit returns a literal of b.
func lit(b bool) *expr.Lit { return &expr.Lit{Type: value.Bool, Bool: b} }

// cmpVar returns the comparison of the variable x with the int n.
func cmpVar(op expr.BinaryOp, x ref, n int64) expr.Node {
	return &expr.Binary{Op: op, Left: &expr.Var{Ref: ids.Encode(ids.Variable, x.id)},
		Right: &expr.Lit{Type: value.Int, Int: n}}
}

// TestRules checks each rule against its fixtures: a change of a clean
// project that it finds something in, once, of the entity that the fixture
// names, and one that it finds nothing in. The clean project itself has no
// finding of any rule.
//
// Verifies: FR-DEP-03/AC1
func TestRules(t *testing.T) {
	if found := check(newWorld().m); len(found) != 0 {
		t.Fatalf("the clean world has the findings %v", found)
	}
	for _, r := range Rules() {
		c, ok := ruleCases[r.Code]
		if !ok {
			t.Errorf("%s has no fixtures in ruleCases", r.Code)
			continue
		}
		t.Run(string(r.Code)+"/positive", func(t *testing.T) {
			w := newWorld()
			want := c.positive(w)
			if got := w.found(r.Code); !slices.Equal(got, []ref{want}) {
				t.Errorf("%s finds %v, want %v", r.Code, got, want)
			}
		})
		t.Run(string(r.Code)+"/negative", func(t *testing.T) {
			w := newWorld()
			c.negative(w)
			if got := w.found(r.Code); len(got) != 0 {
				t.Errorf("%s finds %v, want nothing", r.Code, got)
			}
		})
	}
}

// TestDanglingReferences checks that a reference to what is not outside the
// trash is found once for each thing that each field refers to, of a text, a
// speaker, a board's entry node, and the project's entry, and that one to
// what is there is not.
//
// Verifies: FR-DEP-03/AC1, FR-TEXT-02/AC2
func TestDanglingReferences(t *testing.T) {
	w := newWorld()
	gone, there := ids.New(), ids.New()
	w.m.components[there] = true
	attribute := ids.New()
	w.m.attributes[attribute] = true
	text := func(owner ref, kind dependency.Kind, target, attribute ids.UUID) {
		w.m.texts = append(w.m.texts, textRef{owner: owner, use: dependency.Use{Kind: kind, Target: target,
			Attribute: attribute, Access: dependency.Read}})
	}
	label := w.connect(w.end, w.start)
	// The text of start shows a deleted variable twice, which is one
	// finding, and mentions a component that is there.
	text(ref{ids.Node, w.start}, dependency.Variable, gone, ids.UUID{})
	text(ref{ids.Node, w.start}, dependency.Variable, gone, ids.UUID{})
	text(ref{ids.Node, w.start}, dependency.Component, there, ids.UUID{})
	// The label shows an attribute of a component that is there whose
	// definition is not, and one whose component is not.
	text(ref{ids.Connection, label}, dependency.Attribute, there, ids.New())
	text(ref{ids.Connection, label}, dependency.Attribute, gone, attribute)
	text(ref{ids.Connection, label}, dependency.Attribute, there, attribute)
	// A component's description mentions a deleted component.
	text(ref{ids.Component, there}, dependency.Component, gone, ids.UUID{})
	w.m.nodes[1].kind, w.m.nodes[1].speaker = "dialogue", gone
	other := w.board()
	w.entry(other, gone)
	w.element(other, func(n *node) { n.ending = true })
	w.m.entry.board, w.m.entry.node = gone, w.start
	var got []string
	for _, f := range check(w.m) {
		if f.code == RefDangling {
			got = append(got, f.entity.kind.String()+"."+f.field+"→"+f.target.kind.String()+
				map[bool]string{true: "+attribute"}[f.attribute != ids.UUID{}])
		}
	}
	slices.Sort(got)
	want := []string{"board.entry_node_id→node", "component.description→component",
		"connection.label→component+attribute", "connection.label→component+attribute", "node.speaker_component_id→component",
		"node.text→variable", "project.entry→board"}
	if !slices.Equal(got, want) {
		t.Errorf("the dangling references are %q, want %q", got, want)
	}
}

// TestReachability checks where the flow may start and how it goes on: at a
// board's entry node, or at every root of a board without one outside the
// trash, and at the project's entry node; by every connection, whatever its
// condition, and from a jumper to its target.
//
// Verifies: FR-DEP-03/AC1
func TestReachability(t *testing.T) {
	w := newWorld()
	// A board whose entry node is in the trash: its roots are where the
	// flow may start.
	other := w.board()
	w.entry(other, ids.New())
	root := w.element(other)
	w.connect(root, w.element(other, func(n *node) { n.ending = true }))
	// A root of main, which has an entry node, reached only by the project's
	// entry, and one reached by a jumper of the other board.
	byEntry := w.element(w.main, func(n *node) { n.ending = true })
	w.m.entry.board, w.m.entry.node = w.main, byEntry
	byJumper := w.element(w.main, func(n *node) { n.ending = true })
	w.connect(root, w.jumper(other, byJumper))
	// A root of main that nothing reaches, which leads on to another.
	lost := w.element(w.main)
	after := w.element(w.main, func(n *node) { n.ending = true })
	w.connect(lost, after)
	got := w.found(NodeUnreachable)
	want := []ref{{ids.Node, lost}, {ids.Node, after}}
	if !slices.Equal(got, want) {
		t.Errorf("the unreachable nodes are %v, want %v", got, want)
	}
}

// TestMutable checks that a mute may silence exactly the warnings and the
// infos about each kind of entity, and that the database's check of a mute's
// code names the same codes (migration 00028).
func TestMutable(t *testing.T) {
	want := map[ids.Kind][]Code{
		ids.Node:         {NodeUnreachable, NodeEmpty, DeadEnd, SpeakerMissing},
		ids.Connection:   {ChoiceDuplicateLabel, ConnectionSelf, ConnectionParallel},
		ids.BranchOutput: {PortUnconnected},
		ids.Variable:     {VarReadNeverWritten, VarWrittenNeverRead},
		ids.Board:        {BoardEntryMissing},
	}
	for _, kind := range ids.Kinds() {
		got := Mutable(kind)
		if !slices.Equal(got, want[kind]) {
			t.Errorf("a mute of a %s may silence %v, want %v", kind, got, want[kind])
		}
		if len(got) > 0 != slices.Contains(MuteOwners, kind) {
			t.Errorf("MuteOwners and Mutable disagree on %s", kind)
		}
	}
	for _, r := range Rules() {
		if r.Mutable() != (r.Severity != Error) {
			t.Errorf("%s is mutable %v, with its severity %s", r.Code, r.Mutable(), r.Severity)
		}
	}
}
