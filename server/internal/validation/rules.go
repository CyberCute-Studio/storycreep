package validation

import (
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// check returns what the rules find of the model m, in no order: each
// finding once.
func check(m *model) []finding {
	c := newChecker(m)
	c.findExpressions()
	c.findReferences()
	c.findJumpers()
	c.findBranches()
	c.findVariables()
	c.findReachability()
	c.findElements()
	c.findConnections()
	c.findBoards()
	return c.out
}

// checker holds what the rules look up of a model, once.
type checker struct {
	m   *model
	out []finding
	// nodes are the model's nodes, by their IDs.
	nodes map[ids.UUID]*node
	// outputs are the model's outputs, by their IDs, and outputsOf the
	// number of each branch's.
	outputs   map[ids.UUID]*output
	outputsOf map[ids.UUID]int
	// connections are the model's connections, by their IDs; from those
	// that leave each node, by any port, choices those that leave each
	// element by its out, in their order, fromOutput how many leave each
	// output, and into how many enter each node.
	connections map[ids.UUID]*connection
	from        map[ids.UUID][]*connection
	choices     map[ids.UUID][]*connection
	fromOutput  map[ids.UUID]int
	into        map[ids.UUID]int
	// conditions are the conditions of the connections, by their IDs: the
	// AST of one that compiles, or nil, with valid false, for one that does
	// not. A connection without a condition has none here.
	conditions map[ids.UUID]condition
	// variables are the boards of the model's variables, by their IDs, the
	// zero UUID for a global one, and boards the model's boards.
	variables map[ids.UUID]ids.UUID
	boards    map[ids.UUID]bool
}

// condition is a connection's condition: its AST, if it compiles.
type condition struct {
	ast   expr.Node
	valid bool
}

// newChecker returns a checker of the model m.
func newChecker(m *model) *checker {
	c := &checker{m: m, nodes: map[ids.UUID]*node{}, outputs: map[ids.UUID]*output{},
		outputsOf: map[ids.UUID]int{}, connections: map[ids.UUID]*connection{}, from: map[ids.UUID][]*connection{},
		choices: map[ids.UUID][]*connection{}, fromOutput: map[ids.UUID]int{}, into: map[ids.UUID]int{},
		conditions: map[ids.UUID]condition{}, variables: map[ids.UUID]ids.UUID{}, boards: map[ids.UUID]bool{}}
	for i := range m.nodes {
		c.nodes[m.nodes[i].id] = &m.nodes[i]
	}
	for i := range m.outputs {
		o := &m.outputs[i]
		c.outputs[o.id] = o
		c.outputsOf[o.node]++
	}
	for i := range m.connections {
		cn := &m.connections[i]
		c.connections[cn.id] = cn
		c.from[cn.from] = append(c.from[cn.from], cn)
		if cn.output == (ids.UUID{}) {
			c.choices[cn.from] = append(c.choices[cn.from], cn)
		} else {
			c.fromOutput[cn.output]++
		}
		c.into[cn.to]++
	}
	for _, e := range m.expressions {
		if e.owner.kind == ids.Connection && e.field == fieldCondition {
			c.conditions[e.owner.id] = condition{ast: e.condition, valid: e.valid}
		}
	}
	for _, v := range m.variables {
		c.variables[v.id] = v.board
	}
	for _, b := range m.boards {
		c.boards[b.id] = true
	}
	return c
}

// add adds a finding of the code about the entity, with the board and the
// node of the entity's context (context).
func (c *checker) add(f finding) {
	f.board, f.node = c.context(f.entity)
	c.out = append(c.out, f)
}

// context returns the board and the node by which a client opens the entity
// e: a node's own and its board, the node that a connection leaves, an
// output's branch, a board's own, a board's variable's board, and none for a
// global variable, a component, or the project.
func (c *checker) context(e ref) (board, node ids.UUID) {
	switch e.kind {
	case ids.Node:
		if n := c.nodes[e.id]; n != nil {
			return n.board, n.id
		}
	case ids.Connection:
		if cn := c.connections[e.id]; cn != nil {
			return cn.board, cn.from
		}
	case ids.BranchOutput:
		if o := c.outputs[e.id]; o != nil {
			return o.board, o.node
		}
	case ids.Board:
		return e.id, ids.UUID{}
	case ids.Variable:
		return c.variables[e.id], ids.UUID{}
	}
	return ids.UUID{}, ids.UUID{}
}

// liveNode reports whether the node id is a node of the model, outside the
// trash.
func (c *checker) liveNode(id ids.UUID) bool {
	return id != (ids.UUID{}) && c.nodes[id] != nil
}

// findExpressions finds the expressions that do not compile (expr_invalid),
// one finding of each, at its owner's field.
func (c *checker) findExpressions() {
	for _, e := range c.m.expressions {
		if !e.valid {
			c.add(finding{code: ExprInvalid, entity: e.owner, field: e.field, args: i18n.Args{"error": e.errorCode}})
		}
	}
}

// findReferences finds what refers to what is not a part of the project
// outside its trash (ref_dangling), once for each thing that each field
// refers to: a text's value references and mentions, an element's speaker, a
// board's entry node, and the project's entry, its board and its node. A
// jumper's target is jumper_broken's.
func (c *checker) findReferences() {
	seen := map[danglingKey]bool{}
	for _, t := range c.m.texts {
		f := finding{code: RefDangling, entity: t.owner, field: textField(t.owner.kind)}
		var live bool
		switch t.use.Kind {
		case dependency.Variable:
			f.target = ref{ids.Variable, t.use.Target}
			_, live = c.variables[t.use.Target]
		case dependency.Attribute:
			f.target, f.attribute = ref{ids.Component, t.use.Target}, t.use.Attribute
			live = c.m.components[t.use.Target] && c.m.attributes[t.use.Attribute]
		case dependency.Component:
			f.target = ref{ids.Component, t.use.Target}
			live = c.m.components[t.use.Target]
		default:
			continue
		}
		if !live {
			c.dangling(f, seen)
		}
	}
	for _, n := range c.m.nodes {
		if n.typ == element && n.speaker != (ids.UUID{}) && !c.m.components[n.speaker] {
			c.dangling(finding{code: RefDangling, entity: ref{ids.Node, n.id}, field: fieldSpeaker,
				target: ref{ids.Component, n.speaker}}, seen)
		}
	}
	for _, b := range c.m.boards {
		if b.entry != (ids.UUID{}) && !c.liveNode(b.entry) {
			c.dangling(finding{code: RefDangling, entity: ref{ids.Board, b.id}, field: fieldEntryNode,
				target: ref{ids.Node, b.entry}}, seen)
		}
	}
	project := ref{kind: ids.Project}
	if e := c.m.entry.board; e != (ids.UUID{}) && !c.boards[e] {
		c.dangling(finding{code: RefDangling, entity: project, field: fieldEntry, target: ref{ids.Board, e}}, seen)
	}
	if e := c.m.entry.node; e != (ids.UUID{}) && !c.liveNode(e) {
		c.dangling(finding{code: RefDangling, entity: project, field: fieldEntry, target: ref{ids.Node, e}}, seen)
	}
}

// danglingKey is a dangling reference, which a field refers to once
// however often it names it.
type danglingKey struct {
	entity, target ref
	field          string
	attribute      ids.UUID
}

// dangling adds the finding f of a dangling reference, unless seen holds it
// already, with the kind of what it refers to as the message's argument.
func (c *checker) dangling(f finding, seen map[danglingKey]bool) {
	k := danglingKey{entity: f.entity, target: f.target, field: f.field, attribute: f.attribute}
	if seen[k] {
		return
	}
	seen[k] = true
	kind := f.target.kind.String()
	if f.attribute != (ids.UUID{}) {
		kind = "attribute"
	}
	f.args = i18n.Args{"target": kind}
	c.add(f)
}

// textField returns the field of the text of an owner of the kind: a node's
// text, a connection's label, or a component's description.
func textField(kind ids.Kind) string {
	switch kind {
	case ids.Connection:
		return fieldLabel
	case ids.Component:
		return fieldDescription
	}
	return fieldText
}

// findJumpers finds the jumpers without a target that is a node outside the
// trash (jumper_broken), and those whose target is a jumper, or that
// connections leave (jumper_invalid, §5.8.5).
func (c *checker) findJumpers() {
	for _, n := range c.m.nodes {
		if n.typ != jumper {
			continue
		}
		self := ref{ids.Node, n.id}
		switch target := c.nodes[n.targetNode]; {
		case n.targetNode == (ids.UUID{}):
			c.add(finding{code: JumperBroken, entity: self, field: fieldTarget, args: i18n.Args{"reason": "none"}})
		case target == nil:
			c.add(finding{code: JumperBroken, entity: self, field: fieldTarget, target: ref{ids.Node, n.targetNode},
				args: i18n.Args{"reason": "deleted"}})
		case target.typ == jumper:
			c.add(finding{code: JumperInvalid, entity: self, field: fieldTarget, target: ref{ids.Node, target.id},
				args: i18n.Args{"reason": "target"}})
		}
		if len(c.from[n.id]) > 0 {
			c.add(finding{code: JumperInvalid, entity: self, args: i18n.Args{"reason": "connections"}})
		}
	}
}

// findBranches finds the branches without outputs (branch_no_outputs), and
// the outputs that no connection leaves (port_unconnected).
func (c *checker) findBranches() {
	for _, n := range c.m.nodes {
		if n.typ == branch && c.outputsOf[n.id] == 0 {
			c.add(finding{code: BranchNoOutputs, entity: ref{ids.Node, n.id}})
		}
	}
	for _, o := range c.m.outputs {
		if c.fromOutput[o.id] == 0 {
			c.add(finding{code: PortUnconnected, entity: ref{ids.BranchOutput, o.id}})
		}
	}
}

// findVariables finds the variables that a site reads but none writes
// (var_read_never_written), and those that a site writes but none reads
// (var_written_never_read), as their usage lists hold them (FR-DEP-01): a
// script that updates a variable, as with +=, reads it too.
func (c *checker) findVariables() {
	for _, v := range c.m.variables {
		self := ref{ids.Variable, v.id}
		switch {
		case v.read && !v.written:
			c.add(finding{code: VarReadNeverWritten, entity: self, args: i18n.Args{"key": v.key}})
		case v.written && !v.read:
			c.add(finding{code: VarWrittenNeverRead, entity: self, args: i18n.Args{"key": v.key}})
		}
	}
}

// findReachability finds the nodes that the flow cannot reach from any
// board's entry, whatever the conditions (node_unreachable). The flow starts
// at a board's entry node, or, on a board without one outside the trash, at
// any of the board's roots, the nodes that no connection enters, and at the
// project's entry node; it goes on by every connection, and from a jumper to
// its target.
func (c *checker) findReachability() {
	reached := map[ids.UUID]bool{}
	var queue []ids.UUID
	reach := func(id ids.UUID) {
		if c.liveNode(id) && !reached[id] {
			reached[id] = true
			queue = append(queue, id)
		}
	}
	entries := c.entries()
	for _, n := range c.m.nodes {
		if entries[n.board] == (ids.UUID{}) && c.into[n.id] == 0 {
			reach(n.id)
		}
	}
	for _, e := range entries {
		reach(e)
	}
	reach(c.m.entry.node)
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, cn := range c.from[id] {
			reach(cn.to)
		}
		if n := c.nodes[id]; n.typ == jumper {
			reach(n.targetNode)
		}
	}
	for _, n := range c.m.nodes {
		if !reached[n.id] {
			c.add(finding{code: NodeUnreachable, entity: ref{ids.Node, n.id}})
		}
	}
}

// entries returns the entry node of each board of the model whose entry node
// is a node outside the trash, by the board's ID.
func (c *checker) entries() map[ids.UUID]ids.UUID {
	out := map[ids.UUID]ids.UUID{}
	for _, b := range c.m.boards {
		if c.liveNode(b.entry) {
			out[b.id] = b.entry
		}
	}
	return out
}

// findElements finds the elements without text that no connection leaves
// (node_empty), those that no connection leaves that are not endings
// (dead_end), the lines of dialogue without a speaker (speaker_missing), and
// the choices of an element that repeat an earlier choice of it: the same
// label, and conditions that may both hold (choice_duplicate_label). An
// ending's connections are no choices (§5.8.5).
func (c *checker) findElements() {
	for _, n := range c.m.nodes {
		if n.typ != element {
			continue
		}
		self := ref{ids.Node, n.id}
		out := c.choices[n.id]
		if len(out) == 0 && n.textEmpty {
			c.add(finding{code: NodeEmpty, entity: self})
		}
		if len(out) == 0 && !n.ending {
			c.add(finding{code: DeadEnd, entity: self})
		}
		if n.kind == "dialogue" && n.speaker == (ids.UUID{}) {
			c.add(finding{code: SpeakerMissing, entity: self})
		}
		if n.ending {
			continue
		}
		for j, b := range out {
			for _, a := range out[:j] {
				if a.label == b.label && c.overlap(a.id, b.id) {
					c.add(finding{code: ChoiceDuplicateLabel, entity: ref{ids.Connection, b.id},
						target: ref{ids.Connection, a.id}})
					break
				}
			}
		}
	}
}

// overlap reports whether the conditions of the connections a and b may both
// hold: unless one of them never holds, or exclusive proves that they never
// hold together. A connection without a condition always may, and one whose
// condition does not compile, as far as the validation knows, may too.
func (c *checker) overlap(a, b ids.UUID) bool {
	ca, aok := c.conditions[a]
	cb, bok := c.conditions[b]
	if aok && !ca.valid || bok && !cb.valid {
		return true
	}
	var x, y expr.Node
	if aok {
		x = ca.ast
	}
	if bok {
		y = cb.ast
	}
	return !exclusive(x, y)
}

// findConnections finds the connections that enter the node that they leave
// (connection_self), and those that leave a port for a node that an earlier
// connection of the port enters (connection_parallel), each found once,
// naming the first earlier one.
func (c *checker) findConnections() {
	// The first connection of each port to each node.
	type key struct{ from, output, to ids.UUID }
	first := map[key]ids.UUID{}
	for _, cn := range c.m.connections {
		self := ref{ids.Connection, cn.id}
		if cn.from == cn.to {
			c.add(finding{code: ConnectionSelf, entity: self})
		}
		k := key{cn.from, cn.output, cn.to}
		if earlier, ok := first[k]; ok {
			c.add(finding{code: ConnectionParallel, entity: self, target: ref{ids.Connection, earlier}})
		} else {
			first[k] = cn.id
		}
	}
}

// findBoards finds the boards without an entry node outside the trash that
// have more than one root (board_entry_missing).
func (c *checker) findBoards() {
	entries := c.entries()
	roots := map[ids.UUID]int{}
	for _, n := range c.m.nodes {
		if c.into[n.id] == 0 {
			roots[n.board]++
		}
	}
	for _, b := range c.m.boards {
		if entries[b.id] == (ids.UUID{}) && roots[b.id] > 1 {
			c.add(finding{code: BoardEntryMissing, entity: ref{ids.Board, b.id}, args: i18n.Args{"roots": roots[b.id]}})
		}
	}
}
