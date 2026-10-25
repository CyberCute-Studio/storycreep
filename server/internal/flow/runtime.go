package flow

import (
	"fmt"
	"slices"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/semantics"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// The kinds of an Event, which the Godot runtime emits as the signals of PRD
// §8.4.3 that their comments name.
const (
	// EventEnter is node_entered: the flow has entered Node, after its visit
	// count and before its on-enter.
	EventEnter = "enter"
	// EventReady is node_ready: the flow is at rest at Node, after its
	// on-enter, its choices built, or it ends there (§5.8.5).
	EventReady = "ready"
	// EventExit is node_exited: the flow has left Node, after its on-exit.
	EventExit = "exit"
	// EventEnd is flow_ended: the flow ended, for Reason, after the on-exit
	// of the node that it ended at.
	EventEnd = "end"
	// EventSignal is narrative_signal: a script emitted the signal Name, with
	// Value, if it has one.
	EventSignal = "signal"
	// EventError is runtime_error: Code, of the expression of At, if it is
	// one's (Expression), or of the node At, or of none.
	EventError = "error"
)

// The reasons of an EventEnd (§5.8.5).
const (
	// ReasonEnding is an element marked isEnding.
	ReasonEnding = "ending"
	// ReasonDeadEnd is a node that no connection leaves, of its port, or a
	// branch whose outputs' conditions are all false and that has no else,
	// or a connection or a branch that leads to a jumper without a target.
	ReasonDeadEnd = "dead_end"
	// ReasonNoChoices is a node whose connections' conditions are all false.
	ReasonNoChoices = "no_choices"
)

// The runtime errors of the runtime's own (§5.8.7), beside the evaluation's.
const (
	// InvalidChoice is a choice that is not one of the current node's while
	// the flow awaits one, or any choice while it does not.
	InvalidChoice semantics.RuntimeError = "invalid_choice"
	// CannotAdvance is an advance while the flow cannot advance (CanAdvance).
	CannotAdvance semantics.RuntimeError = "cannot_advance"
	// UnknownNode is a board or a node of the snapshot that a start or a goto
	// names that the snapshot does not have, a node of another board than the
	// one that it names, a board without an entry node that it names
	// without a node, or no entry of the project, which it names with
	// neither.
	UnknownNode semantics.RuntimeError = "unknown_node"
	// JumperBroken is a jumper without a target, which only a draft has, that
	// the flow reaches.
	JumperBroken semantics.RuntimeError = "jumper_broken"
	// FlowLoop is a branch that the flow would leave once it has passed
	// through MaxPasses branches in one call.
	FlowLoop semantics.RuntimeError = "flow_loop"
	// FlowHalted is a choose, an advance, or a goto of a flow that strict
	// mode halted.
	FlowHalted semantics.RuntimeError = "flow_halted"
)

// MaxPasses is the most branches that the flow passes through in one call,
// so that a cycle of branches whose conditions always hold, which no element
// stops, ends rather than runs without end: the branch that the flow would
// leave once it has left MaxPasses branches in the call ends the flow there,
// with FlowLoop and ReasonDeadEnd.
const MaxPasses = 100

// The expressions of an entity, as an EventError's Expression names them:
// the fields of the published format that hold them.
const (
	ExpressionOnEnter    = "onEnter"
	ExpressionOnExit     = "onExit"
	ExpressionOnTraverse = "onTraverse"
	ExpressionCondition  = "condition"
)

// Event is what the flow reports as it runs (§8.4.3), one of the kinds of
// event, with the fields of its kind.
type Event struct {
	Kind string
	// Node is the node of an EventEnter, an EventReady, or an EventExit.
	Node string
	// Reason is the reason of an EventEnd.
	Reason string
	// Name and Value are an EventSignal's signal and its value, or nil.
	Name  string
	Value *expr.Value
	// Code is an EventError's error, At the ID of the entity whose
	// expression, as Expression names it, reported it, or of the jumper or
	// the branch that it is of, or "" for a call's own, and Expression ""
	// for none.
	Code       semantics.RuntimeError
	At         string
	Expression string
}

// Choice is a choice of the current node: its connection's ID, and its text,
// the connection's label with its values resolved.
type Choice struct {
	ID   string
	Text string
}

// Runtime runs the flow of a snapshot (§5.8.5, §8.4.2), and holds its state:
// the values of the variables and of the components' runtime attributes, the
// nodes' visit counts, and where the flow is. Each call that moves the flow,
// Start, Goto, Reset, Choose, Advance, and LoadState, reports its events, in
// order, to the listener that New was given. A Runtime is not safe for use by
// several goroutines at once.
type Runtime struct {
	s      *Snapshot
	strict bool
	listen func(Event)

	vars   map[string]expr.Value
	attrs  map[attributeKey]expr.Value
	visits map[string]int64
	// current is the current node, or "" before the flow starts, and phase
	// its phase, published.PhaseAwaitingChoice or published.PhaseEnded.
	current string
	phase   string
	choices []*published.Connection
	// halted is set once strict mode halts the flow.
	halted bool

	// errored is set once the call reports a runtime error, and passes the
	// branches that it has passed through.
	errored bool
	passes  int
	// at and expression are the entity and the expression that the evaluator
	// evaluates, which its runtime errors are of.
	at, expression string
}

// New returns a runtime of the snapshot s, its variables and its runtime
// attributes their values in the snapshot, no node visited, and the flow not
// started, which reports its events to listen, if it is not nil. In strict
// mode, as play mode always runs and the Godot runtime in a debug build, a
// call that reports a runtime error halts the flow once it has run as it
// runs in non-strict mode, so that the state is the same in both: until
// Start, Reset, or LoadState, every Choose, Advance, and Goto reports
// FlowHalted and does nothing (FR-GODOT-08).
func New(s *Snapshot, strict bool, listen func(Event)) *Runtime {
	if listen == nil {
		listen = func(Event) {}
	}
	r := &Runtime{s: s, strict: strict, listen: listen}
	r.defaults()
	return r
}

// defaults sets every value to its snapshot's, and clears the visit counts
// and where the flow is.
func (r *Runtime) defaults() {
	r.vars = make(map[string]expr.Value, len(r.s.defaults))
	for id, v := range r.s.defaults {
		r.vars[id] = v
	}
	r.attrs = make(map[attributeKey]expr.Value, len(r.s.initial))
	for k, v := range r.s.initial {
		r.attrs[k] = v
	}
	r.visits = make(map[string]int64)
	r.current, r.phase, r.choices = "", "", nil
}

// call runs f, a call that moves the flow, after the halt that it clears, if
// it is Start, Reset, or LoadState, or refused by it, with FlowHalted, if it
// is Choose, Advance, or Goto, and halts the flow in strict mode once f has
// reported a runtime error.
func (r *Runtime) call(clears bool, f func()) {
	if clears {
		r.halted = false
	}
	r.errored, r.passes, r.at, r.expression = false, 0, "", ""
	if r.halted {
		r.fail(FlowHalted, "")
		return
	}
	f()
	if r.strict && r.errored {
		r.halted = true
	}
}

// fail reports the runtime error code of the entity at, the call's own.
func (r *Runtime) fail(code semantics.RuntimeError, at string) {
	r.errored = true
	r.listen(Event{Kind: EventError, Code: code, At: at})
}

// Start starts the flow at the node of the board, as start() does (§5.8.5):
// at the board's entry node when node is "", and at the project's entry when
// both are "", wherever the flow was, without the on-exit of the node that was
// current, as a board entry, so that the board's resetOnEnter variables are
// reset, and at a jumper's target when the node is a jumper. A board or a node
// that the snapshot does not have reports UnknownNode, and a jumper without a
// target JumperBroken, and neither moves the flow. It clears a halt.
func (r *Runtime) Start(board, node string) {
	r.call(true, func() {
		if id, ok := r.resolve(board, node); ok {
			r.settle(id, true)
		}
	})
}

// Goto moves the flow to the node of the board, as goto() does, as Start
// does, but that it leaves the current node first, running its on-exit, but
// for one that the flow ended at, which it left already, and runs no
// connection's on-traverse. A node that Start would not move to moves the
// flow nowhere, without the on-exit.
func (r *Runtime) Goto(board, node string) {
	r.call(false, func() {
		id, ok := r.resolve(board, node)
		if !ok {
			return
		}
		if r.current != "" && r.phase == published.PhaseAwaitingChoice {
			r.exit()
		}
		r.settle(id, true)
	})
}

// Reset sets every variable and runtime attribute to its value in the
// snapshot, clears every visit count, and starts the flow at the project's
// entry, as Start("", "") does, as reset() does. It clears a halt.
func (r *Runtime) Reset() {
	r.call(true, func() {
		r.defaults()
		if id, ok := r.resolve("", ""); ok {
			r.settle(id, true)
		}
	})
}

// Choose takes the choice of the current node whose connection's ID is id, as
// choose() does: the node's on-exit, then the connection's on-traverse, then
// the flow arrives at its node (§5.8.5). An id that is not one of the current
// choices, or any while the flow does not await a choice, reports
// InvalidChoice and moves nothing.
func (r *Runtime) Choose(id string) {
	r.call(false, func() {
		i := slices.IndexFunc(r.choices, func(c *published.Connection) bool { return c.ID == id })
		if r.phase != published.PhaseAwaitingChoice || i < 0 {
			r.fail(InvalidChoice, "")
			return
		}
		r.leave(r.choices[i])
	})
}

// Advance takes the one connection that leaves the current node, which has
// no condition, as advance() does, as Choose does, or reports CannotAdvance
// and moves nothing when the flow cannot advance (CanAdvance).
func (r *Runtime) Advance() {
	r.call(false, func() {
		if !r.CanAdvance() {
			r.fail(CannotAdvance, "")
			return
		}
		r.leave(r.s.nodes[r.current].ports[out][0])
	})
}

// CanAdvance reports whether the flow awaits a choice at a node that one
// connection leaves, without a condition, which is then its one choice, as
// can_advance() does.
func (r *Runtime) CanAdvance() bool {
	if r.phase != published.PhaseAwaitingChoice {
		return false
	}
	cs := r.s.nodes[r.current].ports[out]
	return len(cs) == 1 && cs[0].Condition == nil
}

// resolve returns the node that a start or a goto of the board and the node
// moves the flow to, which is no jumper, or reports why it moves it to none.
func (r *Runtime) resolve(board, node string) (string, bool) {
	switch {
	case node != "":
		if n := r.s.nodes[node]; n == nil || board != "" && n.board != board {
			r.fail(UnknownNode, "")
			return "", false
		}
	case board != "":
		b := r.s.boards[board]
		if b == nil || b.info.EntryNodeID == "" {
			r.fail(UnknownNode, "")
			return "", false
		}
		node = b.info.EntryNodeID
	default:
		if r.s.entry == nil {
			r.fail(UnknownNode, "")
			return "", false
		}
		node = r.s.entry.NodeID
	}
	return r.destination(node)
}

// destination returns the node that the flow arrives at when it reaches the
// node id: id, or a jumper's target (§5.8.5), or reports JumperBroken of a
// jumper without one.
func (r *Runtime) destination(id string) (string, bool) {
	j, ok := r.s.nodes[id].node.(*published.Jumper)
	switch {
	case !ok:
		return id, true
	case j.Target == nil:
		r.fail(JumperBroken, id)
		return "", false
	}
	return j.Target.NodeID, true
}

// leave takes the connection c from the current node, which awaits a choice:
// when c leads to a jumper without a target, the flow ends at the node, as at
// a dead end, without its ready, which it reported already; otherwise the
// node's on-exit, then c's on-traverse, then the flow arrives at c's node.
func (r *Runtime) leave(c *published.Connection) {
	dest, ok := r.destination(c.To)
	if !ok {
		r.finish(ReasonDeadEnd)
		return
	}
	r.exit()
	r.traverse(c)
	r.settle(dest, false)
}

// exit runs the current node's on-exit and reports its exit.
func (r *Runtime) exit() {
	id := r.current
	r.run(id, ExpressionOnExit, onExit(r.s.nodes[id].node))
	r.listen(Event{Kind: EventExit, Node: id})
}

// traverse runs the on-traverse of c, the connection that the flow takes.
func (r *Runtime) traverse(c *published.Connection) {
	r.run(c.ID, ExpressionOnTraverse, c.OnTraverse)
}

// settle has the flow arrive at the node id, which is no jumper, as a board
// entry when entry is set or the node's board is not the current node's, and
// go on through each branch that it passes until it is at rest: awaiting a
// choice, or ended.
func (r *Runtime) settle(id string, entry bool) {
	for {
		next := r.arrive(id, entry)
		if next == nil {
			return
		}
		dest, ok := r.destination(next.To)
		if !ok {
			r.end(ReasonDeadEnd)
			return
		}
		r.exit()
		r.traverse(next)
		id, entry = dest, false
	}
}

// arrive has the flow enter the node id (§5.8.5): the board's entry, when it
// is one, which resets its resetOnEnter variables, then its visit count, its
// enter, and its on-enter; then an element's choices, at which it awaits a
// choice, or ends; or a branch's output, of which it returns the connection
// that the flow passes on by, or nil when it ends there.
func (r *Runtime) arrive(id string, entry bool) *published.Connection {
	n := r.s.nodes[id]
	if entry || r.current == "" || r.s.nodes[r.current].board != n.board {
		for _, v := range r.s.boards[n.board].reset {
			r.vars[v] = r.s.defaults[v]
		}
	}
	r.current, r.phase, r.choices = id, "", nil
	if r.visits[id] < published.MaxInt {
		r.visits[id]++
	}
	r.listen(Event{Kind: EventEnter, Node: id})
	switch n := n.node.(type) {
	case *published.Element:
		r.run(id, ExpressionOnEnter, n.OnEnter)
		r.element(n)
	case *published.Branch:
		r.run(id, ExpressionOnEnter, n.OnEnter)
		return r.branch(n)
	}
	return nil
}

// element builds the choices of the element n, the current node, at which the
// flow awaits a choice, or ends: at an ending, whose connections it does not
// read, at a dead end, which no connection leaves, or at a node whose
// connections' conditions are all false.
func (r *Runtime) element(n *published.Element) {
	cs := r.s.nodes[n.ID].ports[out]
	switch {
	case n.IsEnding:
		r.end(ReasonEnding)
		return
	case len(cs) == 0:
		r.end(ReasonDeadEnd)
		return
	}
	r.choices = r.hold(cs)
	if len(r.choices) == 0 {
		r.end(ReasonNoChoices)
		return
	}
	r.phase = published.PhaseAwaitingChoice
	r.listen(Event{Kind: EventReady, Node: n.ID})
}

// hold returns the connections of cs whose conditions hold, in order, a
// connection without one among them.
func (r *Runtime) hold(cs []*published.Connection) []*published.Connection {
	var held []*published.Connection
	for _, c := range cs {
		if r.holds(c.ID, c.Condition) {
			held = append(held, c)
		}
	}
	return held
}

// branch picks the output of the branch n, the current node: its first if or
// elseif whose condition holds, or else its else, and returns the first
// connection of the output whose condition holds, which the flow passes on
// by, or ends the flow at the branch, at a dead end when it picks no output or
// none leaves the output, or at no choices when no connection's condition
// holds, or after MaxPasses branches in the call, and returns nil.
func (r *Runtime) branch(n *published.Branch) *published.Connection {
	var picked *published.BranchOutput
	for i := range n.Outputs {
		o := &n.Outputs[i]
		if o.Kind == published.OutputElse || r.holds(o.ID, o.Condition) {
			picked = o
			break
		}
	}
	if picked == nil || len(r.s.nodes[n.ID].ports[picked.ID]) == 0 {
		r.end(ReasonDeadEnd)
		return nil
	}
	held := r.hold(r.s.nodes[n.ID].ports[picked.ID])
	switch {
	case len(held) == 0:
		r.end(ReasonNoChoices)
		return nil
	case r.passes >= MaxPasses:
		r.fail(FlowLoop, n.ID)
		r.end(ReasonDeadEnd)
		return nil
	}
	r.passes++
	return held[0]
}

// end ends the flow at the current node, for the reason (§5.8.5): its ready,
// its choices none, then its on-exit, its exit, and the end.
func (r *Runtime) end(reason string) {
	r.listen(Event{Kind: EventReady, Node: r.current})
	r.finish(reason)
}

// finish ends the flow at the current node, for the reason, as end does, but
// for its ready.
func (r *Runtime) finish(reason string) {
	r.phase, r.choices = published.PhaseEnded, nil
	r.exit()
	r.listen(Event{Kind: EventEnd, Reason: reason})
}

// onExit returns the on-exit of the node n, or nil.
func onExit(n published.Node) *published.Script {
	switch n := n.(type) {
	case *published.Element:
		return n.OnExit
	case *published.Branch:
		return n.OnExit
	}
	return nil
}

// run runs the script s, if it is not nil, of the entity at's expression.
func (r *Runtime) run(at, expression string, s *published.Script) {
	if s == nil {
		return
	}
	r.at, r.expression = at, expression
	if sc, ok := s.AST.(*expr.Script); ok {
		expr.Run(sc, (*env)(r))
	}
	r.at, r.expression = "", ""
}

// holds reports whether the condition c, of the entity at, holds, as one that
// is nil does.
func (r *Runtime) holds(at string, c *published.Condition) bool {
	if c == nil {
		return true
	}
	r.at, r.expression = at, ExpressionCondition
	defer func() { r.at, r.expression = "", "" }()
	return expr.Condition(c.AST, (*env)(r))
}

// Current returns the current node and its phase, or "" and "" before the
// flow starts, as current_node() does: the node that the flow ended at stays
// current (§5.8.5).
func (r *Runtime) Current() (node, phase string) {
	return r.current, r.phase
}

// Halted reports whether strict mode halted the flow.
func (r *Runtime) Halted() bool {
	return r.halted
}

// IsFinished reports whether the flow ended, as is_finished() does.
func (r *Runtime) IsFinished() bool {
	return r.phase == published.PhaseEnded
}

// Choices returns the current choices, in their order, none while the flow
// does not await a choice: the connections that leave the current node whose
// conditions held when the flow arrived at it, or when LoadState loaded the
// state, as get_choices() does. A change of a value after either does not
// change them; the next node's choices read it.
func (r *Runtime) Choices() []Choice {
	var cs []Choice
	for _, c := range r.choices {
		text := ""
		if c.Label != nil {
			text = r.render(c.Label.Segments)
		}
		cs = append(cs, Choice{ID: c.ID, Text: text})
	}
	return cs
}

// Text returns the current node's text, its values resolved as they are now
// (FR-TEXT-02), as a StorycreepNode's text() does: its plain text, as
// richtext.PlainText lays it out, each value as to_string writes it, or a
// number with its decimals rounded half away from zero (semantics.FormatFixed),
// and "" of a branch, of an element without text, and before the flow
// starts.
func (r *Runtime) Text() string {
	if r.current == "" {
		return ""
	}
	if e, ok := r.s.nodes[r.current].node.(*published.Element); ok && e.Text != nil {
		return r.render(e.Text.Segments)
	}
	return ""
}

// render returns the text of the segments, their values resolved.
func (r *Runtime) render(segs richtext.Segments) string {
	return richtext.Render(segs, func(s richtext.Segment) string {
		v := r.vars[s.Ref]
		if s.Attribute != "" {
			v = r.attrs[attributeKey{s.Ref, s.Attribute}]
		}
		return Interpolate(v, s.Decimals)
	})
}

// Interpolate returns v as a text's value shows it (FR-TEXT-02): as to_string
// writes it, or, when it is a number and decimals is not nil, the decimal
// that to_string writes rounded half away from zero to that many digits after
// the point (semantics.FormatFixed), as the expression corpus's interpolate
// cases say.
func Interpolate(v expr.Value, decimals *int) string {
	switch {
	case decimals != nil && v.Type == value.Int:
		return semantics.FormatFixed(float64(v.Int), *decimals)
	case decimals != nil && v.Type == value.Float:
		return semantics.FormatFixed(v.Float, *decimals)
	}
	return v.String()
}

// Variable returns the value of the variable whose ID is id, as
// get_variable() does by an ID, or none, the zero Value, of an id that is no
// variable's.
func (r *Runtime) Variable(id string) expr.Value {
	return r.vars[id]
}

// SetVariable sets the variable whose ID is id to v, an int widened for a
// float, as set_variable() does, or returns an error and sets nothing of an
// id that is no variable's, or a v of another type. The choices of the
// current node stay as they are (Choices).
func (r *Runtime) SetVariable(id string, v expr.Value) error {
	d := r.s.declared[id]
	if d == nil {
		return fmt.Errorf("flow: %s is no variable of the snapshot", id)
	}
	v, err := typed(v, d.Type)
	if err != nil {
		return err
	}
	r.vars[id] = v
	return nil
}

// Attribute returns the value of the runtime attribute whose definition's ID
// is attribute of the component whose ID is component.
func (r *Runtime) Attribute(component, attribute string) expr.Value {
	return r.attrs[attributeKey{component, attribute}]
}

// SetAttribute sets the runtime attribute to v, as SetVariable sets a
// variable, an enum's to one of its options, or returns an error.
func (r *Runtime) SetAttribute(component, attribute string, v expr.Value) error {
	k := attributeKey{component, attribute}
	cur, ok := r.attrs[k]
	if !ok {
		return fmt.Errorf("flow: %s of %s is no runtime attribute of the snapshot", attribute, component)
	}
	v, err := typed(v, cur.Type)
	if err != nil {
		return err
	}
	if o := r.s.options[k]; o != nil && !slices.Contains(o, v.Str) {
		return fmt.Errorf("flow: %q is none of the options of %s of %s", v.Str, attribute, component)
	}
	r.attrs[k] = v
	return nil
}

// typed returns v as a value of the type typ, an int widened for a float, or
// an error when it is of another type.
func typed(v expr.Value, typ string) (expr.Value, error) {
	switch {
	case v.Type == typ:
		return v, nil
	case typ == value.Float && v.Type == value.Int:
		return expr.FloatValue(float64(v.Int)), nil
	}
	return expr.Value{}, fmt.Errorf("flow: a %s is no value of the type %s", v.Type, typ)
}

// Visits returns how many times the flow has entered the node whose ID is
// node, as visits() does.
func (r *Runtime) Visits(node string) int64 {
	return r.visits[node]
}

// env is the evaluator's Env of a runtime's state (expr.Env).
type env Runtime

func (e *env) Variable(id string) expr.Value       { return e.vars[id] }
func (e *env) SetVariable(id string, v expr.Value) { e.vars[id] = v }
func (e *env) Attribute(component, attribute string) expr.Value {
	return e.attrs[attributeKey{component, attribute}]
}
func (e *env) SetAttribute(component, attribute string, v expr.Value) {
	e.attrs[attributeKey{component, attribute}] = v
}
func (e *env) Visits(node string) int64 { return e.visits[node] }

// Signal reports a script's signal.
func (e *env) Signal(name string, v *expr.Value) {
	e.listen(Event{Kind: EventSignal, Name: name, Value: v})
}

// Error reports a runtime error of the expression that the evaluator
// evaluates.
func (e *env) Error(code semantics.RuntimeError) {
	e.errored = true
	e.listen(Event{Kind: EventError, Code: code, At: e.at, Expression: e.expression})
}
