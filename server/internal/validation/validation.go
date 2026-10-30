// Package validation validates a project's content (PRD FR-DEP-03, plan note
// N55): it reads the draft as of one moment, and reports what is wrong with
// it as findings, each of a rule's stable code, with the rule's severity, a
// message, and the entity that it is about, by which a client opens the
// right editor (AC3). An error blocks publishing (FR-DEP-04), a warning asks
// for an acknowledgement, and an info only informs.
//
// The rules (Rules) are pure functions of what Load reads of the project, so
// that a rule's fixtures are projects built in Go, and the validation of a
// project costs one read of each kind of its rows, however many rules read
// them (NFR-PERF-10). A warning or an info may be muted on its entity, with a
// reason (AC4): a mute is part of its owner, which the commands of this
// package change (Register), and a muted finding is listed apart, with its
// mute, never left out.
package validation

import (
	"slices"

	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Severity is how much a finding matters: an error blocks publishing, a
// warning needs an acknowledgement to publish, and an info neither.
type Severity string

// The severities, in the order of a report.
const (
	Error   Severity = "error"
	Warning Severity = "warning"
	Info    Severity = "info"
)

// rank orders the severities as a report lists them.
func (s Severity) rank() int {
	switch s {
	case Error:
		return 0
	case Warning:
		return 1
	}
	return 2
}

// Code is a rule's stable code (FR-DEP-03).
type Code string

// The codes of the rules, as FR-DEP-03's table lists them.
const (
	ExprInvalid          Code = "expr_invalid"
	RefDangling          Code = "ref_dangling"
	JumperBroken         Code = "jumper_broken"
	JumperInvalid        Code = "jumper_invalid"
	BranchNoOutputs      Code = "branch_no_outputs"
	PortUnconnected      Code = "port_unconnected"
	VarReadNeverWritten  Code = "var_read_never_written"
	VarWrittenNeverRead  Code = "var_written_never_read"
	NodeUnreachable      Code = "node_unreachable"
	NodeEmpty            Code = "node_empty"
	ChoiceDuplicateLabel Code = "choice_duplicate_label"
	DeadEnd              Code = "dead_end"
	SpeakerMissing       Code = "speaker_missing"
	BoardEntryMissing    Code = "board_entry_missing"
	ConnectionSelf       Code = "connection_self"
	ConnectionParallel   Code = "connection_parallel"
)

// Rule is a rule of the validation: its code, its severity, the kind of
// entity that its findings are about, and its message.
type Rule struct {
	Code     Code
	Severity Severity
	// Entity is the kind of entity that the rule's findings are about, or
	// zero for ref_dangling, whose findings are about whatever holds the
	// reference: a node, a connection, a component, a board, or the project.
	Entity  ids.Kind
	message i18n.Key
}

// Mutable reports whether a finding of the rule may be muted: a warning's or
// an info's, never an error's, which blocks publishing.
func (r Rule) Mutable() bool {
	return r.Severity != Error
}

// rules are the rules, in the order of FR-DEP-03's table. Each message is
// the catalog's, which the request's locale formats (package i18n).
var rules = []Rule{
	{ExprInvalid, Error, 0, i18n.K("validation.expr_invalid")},
	{RefDangling, Error, 0, i18n.K("validation.ref_dangling")},
	{JumperBroken, Error, ids.Node, i18n.K("validation.jumper_broken")},
	{JumperInvalid, Error, ids.Node, i18n.K("validation.jumper_invalid")},
	{BranchNoOutputs, Error, ids.Node, i18n.K("validation.branch_no_outputs")},
	{PortUnconnected, Warning, ids.BranchOutput, i18n.K("validation.port_unconnected")},
	{VarReadNeverWritten, Warning, ids.Variable, i18n.K("validation.var_read_never_written")},
	{VarWrittenNeverRead, Warning, ids.Variable, i18n.K("validation.var_written_never_read")},
	{NodeUnreachable, Warning, ids.Node, i18n.K("validation.node_unreachable")},
	{NodeEmpty, Warning, ids.Node, i18n.K("validation.node_empty")},
	{ChoiceDuplicateLabel, Warning, ids.Connection, i18n.K("validation.choice_duplicate_label")},
	{DeadEnd, Warning, ids.Node, i18n.K("validation.dead_end")},
	{SpeakerMissing, Info, ids.Node, i18n.K("validation.speaker_missing")},
	{BoardEntryMissing, Warning, ids.Board, i18n.K("validation.board_entry_missing")},
	{ConnectionSelf, Warning, ids.Connection, i18n.K("validation.connection_self")},
	{ConnectionParallel, Warning, ids.Connection, i18n.K("validation.connection_parallel")},
}

// byCode holds the rules by their codes.
var byCode = func() map[Code]Rule {
	m := make(map[Code]Rule, len(rules))
	for _, r := range rules {
		m[r.Code] = r
	}
	return m
}()

// Rules returns the rules, in the order of FR-DEP-03's table.
func Rules() []Rule {
	return slices.Clone(rules)
}

// Mutable returns the codes that a mute of an entity of the kind may
// silence, in the order of the rules: those of the warnings and the infos
// about the kind, none for a kind that no warning or info is about.
func Mutable(kind ids.Kind) []Code {
	var out []Code
	for _, r := range rules {
		if r.Mutable() && r.Entity == kind {
			out = append(out, r.Code)
		}
	}
	return out
}

// MuteOwners are the kinds of entity that a mute may be of: those that a
// warning or an info is about.
var MuteOwners = []ids.Kind{ids.Node, ids.Connection, ids.BranchOutput, ids.Variable, ids.Board}
