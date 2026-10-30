package validation

import (
	"bytes"
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
)

// Report is a project's validation report, as the API shows it: every
// finding of its draft as of one moment, those that are not muted apart from
// those that are, which are listed, never left out (FR-DEP-03 AC4).
type Report struct {
	Seq      int64     `json:"seq" doc:"The project's operation sequence as of the draft that the report validated, the seq of the last batch that it holds, which publishing names as the state that its author validated (PRD FR-DEP-04)"`
	Counts   Counts    `json:"counts" doc:"How many findings the report holds of each severity, of those that are not muted, and how many are muted"`
	Findings []Finding `json:"findings" nullable:"false" doc:"The findings that are not muted: the errors, then the warnings, then the infos, each severity's in the order of the rules' codes, as PRD FR-DEP-03 lists them, and each code's in the order of their boards, their nodes, their entities, their fields, and what they name"`
	Muted    []Finding `json:"muted" nullable:"false" doc:"The findings that are muted, each with its mute, in the order of findings. Only a warning or an info is ever muted."`
}

// Counts are how many findings a report holds.
type Counts struct {
	Errors   int `json:"errors" doc:"How many errors the report holds, which block publishing (PRD FR-DEP-04). No error is ever muted."`
	Warnings int `json:"warnings" doc:"How many warnings the report holds that are not muted"`
	Infos    int `json:"infos" doc:"How many infos the report holds that are not muted"`
	Muted    int `json:"muted" doc:"How many findings the report holds that are muted, warnings and infos"`
}

// Finding is a finding of a report, as the API shows it.
type Finding struct {
	Code     Code     `json:"code" enum:"expr_invalid,ref_dangling,jumper_broken,jumper_invalid,branch_no_outputs,port_unconnected,var_read_never_written,var_written_never_read,node_unreachable,node_empty,choice_duplicate_label,dead_end,speaker_missing,board_entry_missing,connection_self,connection_parallel" doc:"The code of the rule that found it (PRD FR-DEP-03)"`
	Severity Severity `json:"severity" enum:"error,warning,info" doc:"The rule's severity: an error blocks publishing, a warning needs an acknowledgement to publish, and an info neither"`
	Message  string   `json:"message" doc:"What is wrong, in the request's locale"`
	// EntityType and EntityID are the entity that the finding is about,
	// whose editor a client opens (FR-DEP-03 AC3).
	EntityType string  `json:"entity_type" enum:"project,board,node,branch_output,connection,variable,component" doc:"The type of the entity that the finding is about: an expression's or a text's owner, what holds a dangling reference, or what the rule is about"`
	EntityID   string  `json:"entity_id" doc:"The ID of the entity that the finding is about, by which a client opens its editor, and which a mute names"`
	Field      string  `json:"field,omitempty" enum:"on_enter,on_exit,condition,on_traverse,text,label,description,speaker_component_id,entry_node_id,entry,target" doc:"The field of the entity that the finding is about, if it is about one: the expression of expr_invalid; the text, the speaker, the board's entry node, or the project's entry of ref_dangling; or a jumper's target"`
	BoardID    string  `json:"board_id,omitempty" doc:"The ID of the board of the entity, by which a client opens it: a node's, a connection's, an output's, a board's own, and a board's variable's. A global variable, a component, and the project have none."`
	NodeID     string  `json:"node_id,omitempty" doc:"The ID of the node of the entity, by which a client opens it: a node's own, the node that a connection leaves, and an output's branch"`
	Target     *Target `json:"target,omitempty" doc:"What else the finding names: what a dangling reference refers to, a jumper's target, or the earlier connection that a choice or a connection repeats"`
	Mute       *Mute   `json:"mute,omitempty" doc:"The finding's mute, which a muted finding has, and no other"`
}

// Target is what else a finding names.
type Target struct {
	Type        string `json:"type" enum:"board,node,connection,variable,component,attribute" doc:"The type of what the finding names: an attribute is a component's, by the component and the attribute's definition"`
	ID          string `json:"id" doc:"Its ID: an attribute's is its component's"`
	AttributeID string `json:"attribute_id,omitempty" doc:"The ID of an attribute's definition"`
}

// Mute is a finding's mute.
type Mute struct {
	Reason  string    `json:"reason" doc:"Why the finding is muted, as its author wrote it"`
	MutedBy string    `json:"muted_by,omitempty" doc:"The ID of the user who muted it last, with its reason, whom it no longer names once their account is deleted"`
	MutedAt time.Time `json:"muted_at" doc:"When it was muted last, with its reason"`
}

// Result is a validation of a project: its findings, in the order of a
// report, and its mutes, of the draft as of its operation sequence.
type Result struct {
	seq      int64
	findings []finding
	mutes    map[muteKey]mute
}

// Run validates the project projectID of the workspace ws in tx, a
// transaction of the workspace that reads as of one moment, which publishing
// runs it in too, so that its report is of the draft that it publishes
// (FR-DEP-04). It reads nothing of the actor, whose right to read the
// project its caller checks.
func Run(ctx context.Context, tx pgx.Tx, ws, projectID ids.UUID) (*Result, error) {
	d, err := load(ctx, tx, ws, projectID)
	if err != nil {
		return nil, err
	}
	found := check(d.model)
	for i := range found {
		if found[i].entity.kind == ids.Project {
			found[i].entity.id = projectID
		}
	}
	slices.SortFunc(found, compare)
	return &Result{seq: d.seq, findings: found, mutes: d.mutes}, nil
}

// compare orders findings as a report lists them: by their severities and
// the order of their rules (order), their boards, their nodes, their
// entities, their fields, and what they name.
func compare(a, b finding) int {
	return cmp.Or(cmp.Compare(order[a.code], order[b.code]),
		bytes.Compare(a.board[:], b.board[:]), bytes.Compare(a.node[:], b.node[:]),
		bytes.Compare(a.entity.id[:], b.entity.id[:]), cmp.Compare(a.field, b.field),
		bytes.Compare(a.target.id[:], b.target.id[:]), bytes.Compare(a.attribute[:], b.attribute[:]))
}

// order holds the place of each code's findings in a report, by the code:
// its rule's severity's place among the severities, and then its rule's
// among the rules.
var order = func() map[Code]int {
	m := make(map[Code]int, len(rules))
	for i, r := range rules {
		m[r.Code] = r.Severity.rank()*len(rules) + i
	}
	return m
}()

// Report returns r as the API shows it, its messages in l's locale, and the
// languages that they are in, as a response's Content-Language names them:
// l's locale, or the source locale for a message that its catalog lacks
// (i18n.Localizer.Localize), each once, in the order of the messages, or l's
// locale alone for a report without a finding.
func (r *Result) Report(l *i18n.Localizer) (Report, string) {
	out := Report{Seq: r.seq, Findings: []Finding{}, Muted: []Finding{}}
	var languages []string
	for _, f := range r.findings {
		rule := byCode[f.code]
		message, locale := l.Localize(i18n.Message{Key: rule.message, Args: f.args})
		if language := locale.String(); !slices.Contains(languages, language) {
			languages = append(languages, language)
		}
		api := Finding{Code: f.code, Severity: rule.Severity, Message: message, EntityType: f.entity.kind.String(),
			EntityID: ids.Encode(f.entity.kind, f.entity.id), Field: f.field, BoardID: encode(ids.Board, f.board),
			NodeID: encode(ids.Node, f.node)}
		if f.target != (ref{}) {
			api.Target = &Target{Type: f.target.kind.String(), ID: ids.Encode(f.target.kind, f.target.id)}
			if f.attribute != (ids.UUID{}) {
				api.Target.Type, api.Target.AttributeID = "attribute", ids.Encode(ids.AttributeDef, f.attribute)
			}
		}
		if m, ok := r.mutes[muteKey{f.entity.id, f.code}]; ok && rule.Mutable() && rule.Entity == f.entity.kind {
			api.Mute = &Mute{Reason: m.reason, MutedBy: encode(ids.User, m.by), MutedAt: m.at.UTC()}
			out.Muted = append(out.Muted, api)
			out.Counts.Muted++
			continue
		}
		out.Findings = append(out.Findings, api)
		switch rule.Severity {
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

// encode returns the public ID of the kind of id, or "" for the zero UUID.
func encode(k ids.Kind, id ids.UUID) string {
	if id == (ids.UUID{}) {
		return ""
	}
	return ids.Encode(k, id)
}

// DB runs a transaction of the application login, as *database.DB does, and
// one that reads as of one moment (InSnapshot), which a report reads in.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
	InSnapshot(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// Projects reads the projects, which authorises each validation of
	// them. It is required.
	Projects *project.Service
}

// Service validates the projects' drafts.
type Service struct {
	db       DB
	projects *project.Service
}

// New returns a Service that reads the projects in db, with opts. It panics
// if opts.Projects is nil.
func New(db DB, opts Options) *Service {
	if opts.Projects == nil {
		panic("validation: a Service needs the projects' service that authorises its reads")
	}
	return &Service{db: db, projects: opts.Projects}
}

// Report returns the validation report of the draft of the project
// projectID, once the actor may read the project's content
// (project.Service.ReadSnapshot), with its messages in the locale of ctx's
// localizer (i18n.FromContext), and the languages that they are in, as a
// response's Content-Language names them (Result.Report). It reads the draft
// as of one moment, so that a batch that commits meanwhile is in the report
// whole or not at all. It returns the errors of project.Service.Get, as
// project.ErrNotFound.
func (s *Service) Report(ctx context.Context, actor commands.Actor, projectID ids.UUID) (Report, string, error) {
	var out Report
	var languages string
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		r, err := Run(ctx, tx, ws, projectID)
		if err != nil {
			return err
		}
		out, languages = r.Report(i18n.FromContext(ctx))
		return nil
	})
	return out, languages, err
}
