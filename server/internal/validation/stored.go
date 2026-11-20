package validation

import (
	"slices"
	"strings"

	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Stored is a validation report as a snapshot stores it (FR-DEP-04 AC2), as
// JSON: the findings of one state of the draft, each with what the API says
// of it but its message, and with what its message is made of instead, so
// that each read writes the messages in its own locale (Stored.Report), as
// it does those of a report that it computes.
type Stored struct {
	// Seq is the project's operation sequence as of the draft that the
	// report validated.
	Seq      int64           `json:"seq"`
	Findings []StoredFinding `json:"findings"`
}

// StoredFinding is a finding of a stored report: a Finding, but for its
// message, its severity, which its code gives, and whether it is muted,
// which its mute says, and with the arguments of its message.
type StoredFinding struct {
	Code       Code      `json:"code"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	Field      string    `json:"field,omitempty"`
	BoardID    string    `json:"board_id,omitempty"`
	BoardName  *string   `json:"board_name,omitempty"`
	NodeID     string    `json:"node_id,omitempty"`
	NodeTitle  *string   `json:"node_title,omitempty"`
	Target     *Target   `json:"target,omitempty"`
	Args       i18n.Args `json:"args,omitempty"`
	Mute       *Mute     `json:"mute,omitempty"`
}

// Acknowledgement names a warning of a project's draft that the author of a
// publication acknowledged (FR-DEP-04), as the report that they validated
// names the finding: by its code, its entity, its field, and what else it
// names, so that a client copies them from the report's warnings that it
// showed.
type Acknowledgement struct {
	Code     Code    `json:"code" enum:"port_unconnected,var_read_never_written,var_written_never_read,node_unreachable,node_empty,choice_duplicate_label,dead_end,board_entry_missing,connection_self,connection_parallel" doc:"The code of the warning (PRD FR-DEP-03), as the report's finding has it"`
	EntityID string  `json:"entity_id" doc:"The ID of the entity that the warning is about, as the report's finding has it"`
	Field    string  `json:"field,omitempty" doc:"The field of the entity that the warning is about, as the report's finding has it, if it has one"`
	Target   *Target `json:"target,omitempty" doc:"What else the warning names, as the report's finding has it, if it names something"`
}

// key returns what tells the finding that a apart from the others of a
// report, which the acknowledgement of the same warning has too.
func (a Acknowledgement) key() string {
	parts := []string{string(a.Code), a.EntityID, a.Field}
	if a.Target != nil {
		parts = append(parts, a.Target.Type, a.Target.ID, a.Target.AttributeID)
	}
	return strings.Join(parts, "\x00")
}

// Acknowledgement returns what acknowledges the finding f, as a client names
// it from its report.
func (f StoredFinding) Acknowledgement() Acknowledgement {
	return Acknowledgement{Code: f.Code, EntityID: f.EntityID, Field: f.Field, Target: f.Target}
}

// Stored returns r as a snapshot stores it: each finding with what Report
// shows of it, its mute among it, but its message, whose arguments it keeps
// instead.
func (r *Result) Stored() Stored {
	out := Stored{Seq: r.seq, Findings: make([]StoredFinding, 0, len(r.findings))}
	for _, f := range r.findings {
		rule := byCode[f.code]
		s := StoredFinding{Code: f.code, EntityType: f.entity.kind.String(),
			EntityID: ids.Encode(f.entity.kind, f.entity.id), Field: f.field, BoardID: encode(ids.Board, f.board),
			BoardName: nameOf(f.board, f.boardName), NodeID: encode(ids.Node, f.node),
			NodeTitle: nameOf(f.node, f.nodeTitle), Args: f.args}
		if f.target != (ref{}) {
			s.Target = &Target{Type: f.target.kind.String(), ID: ids.Encode(f.target.kind, f.target.id)}
			if f.attribute != (ids.UUID{}) {
				s.Target.Type, s.Target.AttributeID = "attribute", ids.Encode(ids.AttributeDef, f.attribute)
			}
		}
		if m, ok := r.mutes[muteKey{f.entity.id, f.code}]; ok && rule.Mutable() && rule.Entity == f.entity.kind {
			s.Mute = &Mute{Reason: m.reason, MutedBy: encode(ids.User, m.by), MutedAt: m.at.UTC()}
		}
		out.Findings = append(out.Findings, s)
	}
	return out
}

// Seq returns the project's operation sequence as of the draft that r
// validated.
func (r *Result) Seq() int64 {
	return r.seq
}

// Report returns s as the API shows it, as Result.Report shows the report
// that it computes: its messages in l's locale, and the languages that they
// are in. A finding of a code that the server no longer has, which no stored
// report holds while the rules only gain codes, is shown with its code as
// its message, as an error.
func (s Stored) Report(l *i18n.Localizer) (Report, string) {
	out := Report{Seq: s.Seq, Findings: []Finding{}, Muted: []Finding{}}
	var languages []string
	for _, f := range s.Findings {
		rule, ok := byCode[f.Code]
		message, severity := string(f.Code), Error
		if ok {
			var locale language.Tag
			message, locale = l.Localize(i18n.Message{Key: rule.message, Args: f.Args})
			if language := locale.String(); !slices.Contains(languages, language) {
				languages = append(languages, language)
			}
			severity = rule.Severity
		}
		api := Finding{Code: f.Code, Severity: severity, Message: message, EntityType: f.EntityType,
			EntityID: f.EntityID, Field: f.Field, BoardID: f.BoardID, BoardName: f.BoardName, NodeID: f.NodeID,
			NodeTitle: f.NodeTitle, Target: f.Target}
		if f.Mute != nil && ok && rule.Mutable() {
			api.Mute = f.Mute
			out.Muted = append(out.Muted, api)
			out.Counts.Muted++
			continue
		}
		out.Findings = append(out.Findings, api)
		switch severity {
		case Error:
			out.Counts.Errors++
		case Warning:
			out.Counts.Warnings++
		default:
			out.Counts.Infos++
		}
	}
	if len(languages) == 0 {
		languages = []string{l.Locale().String()}
	}
	return out, strings.Join(languages, ", ")
}

// Errors returns the findings of s that are errors, which block publishing
// (FR-DEP-04), in the order of the report. No error is ever muted.
func (s Stored) Errors() []StoredFinding {
	var out []StoredFinding
	for _, f := range s.Findings {
		if rule, ok := byCode[f.Code]; !ok || rule.Severity == Error {
			out = append(out, f)
		}
	}
	return out
}

// Unacknowledged returns the warnings of s that are not muted and that no
// acknowledgement of acks names, in the order of the report: those that a
// publication of the draft that s validated needs acknowledged still
// (FR-DEP-04). A muted warning needs none, since its mute is the recorded
// decision to publish with it (plan note N85).
func (s Stored) Unacknowledged(acks []Acknowledgement) []StoredFinding {
	named := make(map[string]bool, len(acks))
	for _, a := range acks {
		named[a.key()] = true
	}
	var out []StoredFinding
	for _, f := range s.Findings {
		if rule, ok := byCode[f.Code]; ok && rule.Severity == Warning && f.Mute == nil &&
			!named[f.Acknowledgement().key()] {
			out = append(out, f)
		}
	}
	return out
}

// Warnings returns the codes of the rules whose severity is a warning, in
// the order of the rules, which an acknowledgement names (Acknowledgement).
func Warnings() []Code {
	var out []Code
	for _, r := range rules {
		if r.Severity == Warning {
			out = append(out, r.Code)
		}
	}
	return out
}
