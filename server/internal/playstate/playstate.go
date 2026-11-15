// Package playstate keeps a project's saved play states (PRD FR-PLAY-05, plan
// note N80): named states of play mode's flow, as "after act 1" or "romance
// path", which a project's authors save, and which every member who reads the
// project sees and loads as a starting point. Register registers the play
// state as an entity of the pipeline's registry (package commands), whose
// revisions a client expects as it does any entity's (§5.7, ADR-006), with
// its commands, the one way that play states change; Service reads them.
//
// A play state has a name, of 1 to 100 characters, which need not be unique,
// since who saved the state and when tell two of a name apart, and a state, a
// save state of the project's flow (§8.5), as a runtime's save_state()
// returns it, of the project's draft or of one of its releases: where the
// flow is, and the values of the variables and of the components' runtime
// attributes, and the nodes' visit counts, by ID, never by key. The server
// keeps the state as the client saved it, once it has checked what
// checkState lists of it, and that it is the project's, and reads nothing of
// the draft in it: what a state's entities have become since it was saved,
// as a variable deleted or retyped, play mode reads as it loads the state
// (FR-PLAY-05 AC2).
//
// A deleted play state goes to the project's trash, unlisted, from which only
// the undo of its deletion, a restore, brings it back, and which purges it
// once 30 days have passed (package trash). The reads of what is not in the
// trash read the live view of the play states, live_play_state, which leaves
// them out.
//
// Every command needs project:write of an API token (FR-AUTH-04), and the
// actor's role on the project "Edit nodes, connections, text, expressions"
// (§5.6): owner, admin, or editor. An archived project takes none of them
// (FR-PROJ-01 AC2). Reading the play states, as loading one in play mode, is
// reading the project's content, which every role does.
package playstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
)

// MaxNameLength is the most characters that a play state's name may have, as
// a board's may.
const MaxNameLength = 100

// MaxValueNameLength is the most characters that the name of a state's value
// may have (PlayStateNames): more than a component's key and an attribute's,
// of 64 characters each, and the dot between them.
const MaxValueNameLength = 200

// MaxStateBytes is the most bytes that a play state's state may take, as JSON:
// a mebibyte, ten times what a save state of a project of 5,000 nodes and 500
// variables takes (PRD §8.5), so that the undo of a change of the state, which
// holds the state that it changed, fits the undo's bound many times over
// (commands.DefaultMaxUndoBytes).
const MaxStateBytes = 1 << 20

// ErrNotFound is returned for a play state that the project does not have, or
// that is in its trash.
var ErrNotFound = errors.New("playstate: no such play state")

// PlayStateSummary is a play state as the list of a project's play states
// shows it, without its state, which a read of the play state alone returns.
type PlayStateSummary struct {
	ID        string    `json:"id" doc:"The play state's ID"`
	ProjectID string    `json:"project_id" doc:"The ID of the play state's project"`
	Name      string    `json:"name" doc:"The play state's name, which need not be unique among the project's play states"`
	Rev       int64     `json:"rev" doc:"The play state's revision, which every change of it increments, and which a change expects with expected_rev"`
	CreatedBy string    `json:"created_by,omitempty" doc:"The ID of the user who saved the play state, whom it no longer names once their account is deleted"`
	UpdatedBy string    `json:"updated_by,omitempty" doc:"The ID of the user who changed the play state last, its name or its state, whom it no longer names once their account is deleted"`
	CreatedAt time.Time `json:"created_at" doc:"When the play state was saved"`
	UpdatedAt time.Time `json:"updated_at" doc:"When the play state last changed, its name or its state"`
}

// PlayStateBody is a play state as the API shows it, with its state, and as
// an answer of revision_conflict shows it (commands.Entity.States).
type PlayStateBody struct {
	PlayStateSummary
	State published.SaveState `json:"state" doc:"The play state's state: a save state of the project's flow, as a runtime's save_state() returns it (PRD §8.5), whose JSON Schema is /schema/save-state/v1/save-state.schema.json: where the flow is, and the values of the variables and of the components' runtime attributes, and the nodes' visit counts, by ID, never by key, as the project's entities were when it was saved, which they may no longer be (PRD FR-PLAY-05 AC2)."`
	Names PlayStateNames      `json:"names" doc:"The names of the state's values, as play mode named them when it saved the state, which it names a value that a load of the state drops by once its entity is deleted (PRD FR-PLAY-05 AC2): a variable's by its ID, and a runtime attribute's by its component's ID and its attribute definition's"`
	// DeletedAt is set while the play state is in the project's trash, which
	// only an answer of revision_conflict shows, since the reads show no play
	// state in the trash.
	DeletedAt *time.Time `json:"deleted_at,omitempty" doc:"When the play state went to the project's trash, if it is there"`
}

// PlayStateNames are the names of a state's values, as play mode named them
// when it saved the state: a variable by its key, and a runtime attribute as
// its component's key and its own, as an expression names it, which play mode
// names a value that a load of the state drops by, once its entity is deleted
// and has no name of its own (FR-PLAY-05 AC2). The server checks only that
// each is the name of a value that the state holds.
type PlayStateNames struct {
	Variables  map[string]string            `json:"variables,omitempty" doc:"Each variable's name, by its ID, as met_elara"`
	Attributes map[string]map[string]string `json:"attributes,omitempty" doc:"Each runtime attribute's name, by its component's ID and its attribute definition's, as elara.trust"`
}

// summaryOf returns the play state of these columns as the list shows it.
func summaryOf(id, project, createdBy, updatedBy pgtype.UUID, name string, rev int64, createdAt,
	updatedAt pgtype.Timestamptz) PlayStateSummary {
	return PlayStateSummary{ID: ids.Encode(ids.PlayState, id.Bytes),
		ProjectID: ids.Encode(ids.Project, project.Bytes), Name: name, Rev: rev, CreatedBy: encode(ids.User, createdBy), UpdatedBy: encode(ids.User, updatedBy),
		CreatedAt: createdAt.Time, UpdatedAt: updatedAt.Time}
}

// bodyOf returns the play state p as the API shows it, or an error if its
// state is not one that the commands store.
func bodyOf(p queries.PlayState) (PlayStateBody, error) {
	st, names, err := stateOf(p.State, p.Names)
	if err != nil {
		return PlayStateBody{}, fmt.Errorf("playstate: the state of the play state %s: %w",
			ids.Encode(ids.PlayState, p.ID.Bytes), err)
	}
	return PlayStateBody{PlayStateSummary: summaryOf(p.ID, p.ProjectID, p.CreatedBy, p.UpdatedBy, p.Name, p.Rev,
		p.CreatedAt, p.UpdatedAt), State: st, Names: names, DeletedAt: timeOf(p.DeletedAt)}, nil
}

// stateOf returns the state and the names of its values that the columns
// state and names hold, as the commands stored them (stored).
func stateOf(state, names []byte) (published.SaveState, PlayStateNames, error) {
	var st published.SaveState
	if err := json.Unmarshal(state, &st); err != nil {
		return published.SaveState{}, PlayStateNames{}, err
	}
	var n PlayStateNames
	if err := json.Unmarshal(names, &n); err != nil {
		return published.SaveState{}, PlayStateNames{}, fmt.Errorf("the names of its values: %w", err)
	}
	return st, n, nil
}

// encode returns the ID of the entity of the kind k whose UUID is the column
// id, or "" if it is null.
func encode(k ids.Kind, id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return ids.Encode(k, id.Bytes)
}

// timeOf returns the time of the column t, or nil if it is null.
func timeOf(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// Projects reads the projects, which authorises each read of their play
	// states. It is required.
	Projects *project.Service
}

// Service reads the play states of the projects.
type Service struct {
	db       DB
	projects *project.Service
}

// New returns a Service that reads the play states in db, with opts. It
// panics if opts.Projects is nil.
func New(db DB, opts Options) *Service {
	if opts.Projects == nil {
		panic("playstate: a Service needs the projects' service that authorises its reads")
	}
	return &Service{db: db, projects: opts.Projects}
}

// read runs fn in a transaction of the workspace of the project projectID,
// once it has checked that the actor may read the project's content
// (project.Service.ReadContent): it returns the errors of
// project.Service.Get, as project.ErrNotFound for a project that does not
// exist, is in the trash, on which the actor's role is none, or that the
// actor's API token does not cover. The token's scopes are the route's to
// check.
func (s *Service) read(ctx context.Context, actor commands.Actor, projectID ids.UUID,
	fn func(q *queries.Queries, workspaceID pgtype.UUID) error) error {
	return s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		return fn(queries.New(tx), nullUUID(workspaceID))
	})
}

// List returns at most limit of the play states of the project projectID that
// are not in its trash, each without its state, in the order of their IDs,
// oldest first: the first, if after is zero, or else those after the play
// state after. It returns the errors of project.Service.Get.
func (s *Service) List(ctx context.Context, actor commands.Actor, projectID, after ids.UUID, limit int) (
	[]PlayStateSummary, error) {
	var out []PlayStateSummary
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		rows, err := q.PlayStates(ctx, queries.PlayStatesParams{WorkspaceID: workspaceID,
			ProjectID: nullUUID(projectID), After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("playstate: list the play states: %w", err)
		}
		out = make([]PlayStateSummary, 0, len(rows))
		for _, r := range rows {
			out = append(out, summaryOf(r.ID, r.ProjectID, r.CreatedBy, r.UpdatedBy, r.Name, r.Rev, r.CreatedAt,
				r.UpdatedAt))
		}
		return nil
	})
	return out, err
}

// Get returns the play state playStateID of the project projectID, with its
// state, or ErrNotFound if the project has no such play state, or it is in
// the trash. It returns the errors of project.Service.Get first.
func (s *Service) Get(ctx context.Context, actor commands.Actor, projectID, playStateID ids.UUID) (PlayStateBody,
	error) {
	var out PlayStateBody
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		row, err := q.PlayState(ctx, queries.PlayStateParams{WorkspaceID: workspaceID, ProjectID: nullUUID(projectID),
			ID: nullUUID(playStateID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("playstate: read the play state: %w", err)
		}
		out, err = bodyOf(queries.PlayState(row.LivePlayState))
		return err
	})
	return out, err
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
