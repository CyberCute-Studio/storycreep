// Package variable keeps a project's variables (PRD FR-VAR-01, FR-VAR-02,
// plan note N41): the named, typed state of its narrative, which its
// expressions read and write. Register registers the variable as an entity of
// the pipeline's registry (package commands), whose revisions a client
// expects as it does any entity's (§5.7, ADR-006), with its commands, the one
// way that variables change; Service reads them.
//
// A variable is global, which every board's expressions read, or of a board,
// which only that board's read (FR-VAR-02). Its key is an identifier of the
// expression language (package ident, FR-VAR-01 AC1), unique among the
// project's global variables, or among its board's, that are not in the
// trash, so that a key is free again once its variable is deleted, and
// restoring that variable is refused while another of its scope has the key
// (ErrKeyTaken). Its type is bool, int, float, or string, and its default a
// value of its type (Value.Of, AC2), the type's zero value unless a command
// gives one. reset_on_enter, which resets a variable to its default as the
// flow enters its board (§5.8.5), is set only on a board's variable, and is
// unset by default, because a board's scope says who reads a variable, not
// how long its value lasts. A variable sits in a folder of variables, or at
// the top, ordered among the folder's children as a board is among a folder
// of boards' (package board).
//
// A deleted variable goes to the project's trash, from which restoring it
// brings it back where it was, with the folders above it that were deleted
// since (package trash lists the trash, and purges what has been in it for
// 30 days). A board's variables are in the trash with their board while it is
// there, hold their keys, and come back with it; nothing changes them
// meanwhile, deleting them included, and restoring one of them deleted on its
// own, before its board, waits for its board's restoration. The reads of what
// is not in the trash read the live view of the variables, live_variable,
// which leaves them out.
//
// Every command needs project:write of an API token (FR-AUTH-04), and the
// actor's role on the project "Manage variables, components, component
// types" (§5.6): owner, admin, or editor. An archived project takes none of
// them (FR-PROJ-01 AC2). Reading the variables is reading the project's
// content.
package variable

import (
	"context"

	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// MaxDescriptionLength is the most characters that a variable's description
// may have, as a board's may.
const MaxDescriptionLength = 2000

// The scopes of a variable (FR-VAR-02).
const (
	ScopeGlobal = "global"
	ScopeBoard  = "board"
)

// scopes are the scopes of a variable.
var scopes = []string{ScopeGlobal, ScopeBoard}

// Scopes returns the scopes of a variable, in the order of the database's
// check and of the schemas' enums, as a copy that the caller may change.
func Scopes() []string {
	return slices.Clone(scopes)
}

var (
	// ErrNotFound is returned for a variable that the project does not have,
	// or that is in its trash, as a variable of a board in the trash is.
	ErrNotFound = errors.New("variable: no such variable")
	// ErrKeyTaken is the reason of a command's refusal of a key that another
	// variable of the scope has, of those that are not in the trash
	// (commands.RefusedError), which the API answers with key_taken.
	ErrKeyTaken = errors.New("variable: another variable of the scope has the key")
)

// Variable is a variable as the API shows it, and as an answer of
// revision_conflict shows it (commands.Entity.States).
type Variable struct {
	ID        string `json:"id" doc:"The variable's ID"`
	ProjectID string `json:"project_id" doc:"The ID of the variable's project"`
	Scope     string `json:"scope" enum:"global,board" doc:"Who reads the variable: every board's expressions, for global, or only its board's, for board (PRD FR-VAR-02). A board's variable keeps its value across the board's entries, as a global does, unless reset_on_enter is set."`
	BoardID   string `json:"board_id,omitempty" doc:"The ID of the variable's board, for a variable of a board, or none for a global"`
	FolderID  string `json:"folder_id,omitempty" doc:"The ID of the folder of variables that the variable is in, if it is in one, or none for a variable at the top"`
	Order     string `json:"order" doc:"The variable's place among the folders and variables of its folder, or of the top: they are ordered by their order, compared by its bytes, then by their IDs without the prefix, var_ or fld_, compared by their bytes too (package orderkey)"`
	Key       string `json:"key" doc:"The variable's key, by which expressions name it: an identifier of the expression language (PRD §5.8.1), unique among the project's global variables, or among its board's, that are not in the trash"`
	Type      string `json:"type" enum:"bool,int,float,string" doc:"The variable's type"`
	Default   Value  `json:"default" doc:"The variable's default, a value of its type: true or false for bool, an integer for int, a number for float, which is written without a decimal point when it is integral, as 2, and a string for string. A client reads a number by the variable's type, never by how it is written (PRD §5.8.9)."`
	// Description may be empty.
	Description  string `json:"description" doc:"The variable's description, which may be empty"`
	ResetOnEnter bool   `json:"reset_on_enter" doc:"Whether the variable is reset to its default each time the flow enters its board (PRD FR-VAR-02), which only a board's variable may be"`
	Rev          int64  `json:"rev" doc:"The variable's revision, which every change of it increments, and which a change expects with expected_rev"`
	// DeletedAt is set while the variable is in the project's trash: when it
	// was deleted, or, for a variable in the trash with its board alone, when
	// the board was, which only an answer of revision_conflict shows, since
	// the reads show no variable in the trash.
	DeletedAt *time.Time `json:"deleted_at,omitempty" doc:"When the variable went to the project's trash, if it is there: when it was deleted, or, if it was not, but its board is in the trash, with which it is in the trash too, and comes back, when the board was deleted"`
	CreatedAt time.Time  `json:"created_at" doc:"When the variable was created"`
	UpdatedAt time.Time  `json:"updated_at" doc:"When the variable last changed"`
}

// variableOf returns the variable v as the API shows it, or an error if its
// default is not one of its type, which the database's check keeps from it.
func variableOf(v queries.Variable) (Variable, error) {
	def, err := stored(v)
	if err != nil {
		return Variable{}, err
	}
	return Variable{ID: ids.Encode(ids.Variable, v.ID.Bytes), ProjectID: ids.Encode(ids.Project, v.ProjectID.Bytes),
		Scope: v.Scope, BoardID: encode(ids.Board, v.BoardID), FolderID: encode(ids.Folder, v.FolderID),
		Order: v.Order, Key: v.Key, Type: v.Type, Default: def, Description: v.Description,
		ResetOnEnter: v.ResetOnEnter, Rev: v.Rev, DeletedAt: timeOf(v.DeletedAt), CreatedAt: v.CreatedAt.Time,
		UpdatedAt: v.UpdatedAt.Time}, nil
}

// stored returns the default of the variable v, as the database holds it, in
// its canonical form, as the API shows it and as the inverse of a change of
// it sets it back, without checking its characters or its number's length
// again (Value.of), since jsonb writes 1e+100 back as 1 and 100 zeros, or an
// error if it is not a value of its type, which the database's check keeps
// from it.
func stored(v queries.Variable) (Value, error) {
	def, problem := value.FromJSON(v.DefaultValue).Read(v.Type, value.Checks{})
	if problem != "" {
		return Value{}, fmt.Errorf("variable: the default of the variable %s %s", ids.Encode(ids.Variable,
			v.ID.Bytes), problem)
	}
	return def, nil
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
	// Projects reads the projects, which authorises each read of their
	// variables. It is required.
	Projects *project.Service
}

// Service reads the variables of the projects.
type Service struct {
	db       DB
	projects *project.Service
}

// New returns a Service that reads the variables in db, with opts. It panics
// if opts.Projects is nil.
func New(db DB, opts Options) *Service {
	if opts.Projects == nil {
		panic("variable: a Service needs the projects' service that authorises its reads")
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

// List returns at most limit of the variables of the project projectID that
// are not in its trash, in the order of their IDs: the first, if after is
// zero, or else those after the variable after. It returns the errors of
// project.Service.Get.
func (s *Service) List(ctx context.Context, actor commands.Actor, projectID, after ids.UUID, limit int) (
	[]Variable, error) {
	var out []Variable
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		rows, err := q.Variables(ctx, queries.VariablesParams{WorkspaceID: workspaceID,
			ProjectID: nullUUID(projectID), After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("variable: list the variables: %w", err)
		}
		out = make([]Variable, 0, len(rows))
		for _, row := range rows {
			v, err := variableOf(queries.Variable(row.LiveVariable))
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// Get returns the variable variableID of the project projectID, or
// ErrNotFound if the project has no such variable, or it is in the trash. It
// returns the errors of project.Service.Get first.
func (s *Service) Get(ctx context.Context, actor commands.Actor, projectID, variableID ids.UUID) (Variable,
	error) {
	var out Variable
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		row, err := q.Variable(ctx, queries.VariableParams{WorkspaceID: workspaceID, ProjectID: nullUUID(projectID),
			ID: nullUUID(variableID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("variable: read the variable: %w", err)
		}
		out, err = variableOf(queries.Variable(row.LiveVariable))
		return err
	})
	return out, err
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
