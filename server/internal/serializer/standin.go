package serializer

import (
	"fmt"
	"strings"
)

// StandInKind is what a draft held that no snapshot may, which Serialize
// stood in for in a draft's files (StandIn).
type StandInKind string

// The kinds of stand-in, as play mode's projection names them (plan note
// N78): an expression that does not compile, or a branch's conditional
// output without a condition; a text's value reference or mention of what
// the snapshot does not hold, or that its board does not read; an element's
// speaker that the snapshot does not hold; a jumper's target that it does
// not hold, that is a jumper, or that is not on the board that the target
// names; a board's entry node that it does not hold; the project's entry
// that it does not hold, or whose node is not on the board that it names;
// and a connection that leaves a port that its node does not have, or that
// enters a node that its board does not hold.
const (
	StandInExpression StandInKind = "expression"
	StandInReference  StandInKind = "reference"
	StandInSpeaker    StandInKind = "speaker"
	StandInTarget     StandInKind = "target"
	StandInBoardEntry StandInKind = "board_entry"
	StandInEntry      StandInKind = "entry"
	StandInConnection StandInKind = "connection"
)

// The owners of an expression, as a stand-in names what an expression is of:
// a node, a connection that leaves an element, which is a choice, one that
// leaves a branch, which the flow passes on by alone, or a branch's output.
const (
	OwnerNode       = "node"
	OwnerChoice     = "choice"
	OwnerConnection = "connection"
	OwnerOutput     = "output"
)

// The fields of an expression, as the published format names them, and of a
// text, as a reference's stand-in names them.
const (
	FieldOnEnter    = "onEnter"
	FieldOnExit     = "onExit"
	FieldCondition  = "condition"
	FieldOnTraverse = "onTraverse"
	FieldText       = "text"
	FieldLabel      = "label"
)

// The reasons of a stand-in, as play mode's projection names them: what a
// reference, a target, or an entry names is not in the snapshot, as in the
// trash ("gone"), is another board's variable or on another board than it
// names ("board"), is an attribute that is not runtime ("runtime"), or is a
// jumper ("jumper"); and a connection leaves by a port that its node does
// not have ("port"), or enters a node that its board does not hold
// ("target").
const (
	ReasonGone    = "gone"
	ReasonBoard   = "board"
	ReasonRuntime = "runtime"
	ReasonJumper  = "jumper"
	ReasonPort    = "port"
	ReasonTarget  = "target"
)

// StandIn is what a draft held that no snapshot may, and what Serialize
// wrote in its place in a draft's files, as play mode's projection stands in
// for it (plan note N78), each with the board and the node that a link opens
// it by, as a validation finding's (FR-DEP-03 AC3), Node "" for a board's or
// the project's, and Board "" for a project's entry that names none:
//
//   - StandInExpression: an expression of Owner's field Field, of the entity
//     Entity, that does not compile, with its Source and the Code of its
//     error, or a branch's conditional output without a condition, with
//     neither. A condition is written as false, which never holds, so that
//     the choice is never offered and the output never picked, and a script
//     is left out, so that it does nothing.
//   - StandInReference: a value reference or a mention, of Ref, in the text
//     or the label (Field) of Entity, that the snapshot does not hold, or
//     that the text's board does not read (§5.8.3), as Reason says, left out
//     of its text, whose texts on either side join.
//   - StandInSpeaker: an element's speaker, Ref, left out.
//   - StandInTarget: a jumper's target, Ref, left out, so that the flow
//     reports jumper_broken there (§5.8.5).
//   - StandInBoardEntry: a board's entry node, Ref, left out.
//   - StandInEntry: the project's entry, of the board or the node Ref, left
//     out.
//   - StandInConnection: the connection Entity, left out.
//
// Each of them is a finding whose severity is an error (FR-DEP-03), which
// blocks publishing (FR-DEP-04), so a release never needs one, and Serialize
// refuses to write a release that would (UnpublishableError).
type StandIn struct {
	Kind        StandInKind
	Board, Node string
	Entity      string
	Owner       string
	Field       string
	Source      string
	Code        string
	Ref         string
	Reason      string
}

// String describes the stand-in, in English, for programs and logs.
func (s StandIn) String() string {
	var b strings.Builder
	b.WriteString(string(s.Kind))
	b.WriteString(" of ")
	b.WriteString(s.Entity)
	for _, part := range []struct{ name, value string }{{"owner", s.Owner}, {"field", s.Field},
		{"code", s.Code}, {"ref", s.Ref}, {"reason", s.Reason}} {
		if part.value != "" {
			fmt.Fprintf(&b, " %s=%s", part.name, part.value)
		}
	}
	return b.String()
}

// UnpublishableError is Serialize's error for a release of a draft that
// holds what no snapshot may: what it would have stood in for, which a
// draft's files stand in for, but which no release holds (FR-EXPR-03 AC1).
// The validation reports each as an error (FR-DEP-03), which blocks
// publishing before a snapshot is built (FR-DEP-04).
type UnpublishableError struct {
	StandIns []StandIn
}

func (e *UnpublishableError) Error() string {
	return fmt.Sprintf("serializer: the draft holds %d things that no release may hold, the first a %s",
		len(e.StandIns), e.StandIns[0])
}
