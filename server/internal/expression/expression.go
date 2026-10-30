// Package expression keeps the expressions of a project's content (PRD
// FR-EXPR-03, §5.3, §5.8, plan note N51): the on-enter and on-exit scripts
// of its nodes, the conditions and the on-traverse scripts of its
// connections, and the conditions of its branches' outputs, each an
// expression of one field of its owner.
//
// An expression is the source that its author wrote, which a command of its
// owner sets (Set), and what compiling the source gives (package expr),
// against what the project's expressions can name at the owner's board: its
// AST, whose names are the IDs of what they name, and its ast_version, if it
// compiles, or else the first error of the source, its code and the
// characters that it covers. A source that does not compile is kept all the
// same, and marked invalid (FR-EDIT-03 AC4), which the validation reports and
// which blocks publishing (FR-DEP-03, FR-DEP-04).
//
// A command writes the source alone, and the pipeline compiles it once every
// command of the batch has applied, with the derivation that Register
// registers, so that a batch that creates a variable and a condition that
// reads it compiles the condition against the variable, in whichever order
// the batch has them. The derivation also compiles again every expression
// whose compilation what the batch changed may change: every one whose source
// names what the batch changed, by its ID or by its key; when the batch
// changed a variable, a component, an attribute definition, or a component
// type, every one that does not compile but names something, whose error, or
// the near matches that it suggests, may change with them; and when it
// deleted a board to the trash or restored one, the other boards'
// expressions that name the board's nodes, or do not compile and name the
// keys of its variables. So an expression is always what compiling its source
// against the project as it is gives, and the undo of a batch gives back what
// the batch found (FR-EDIT-08 AC2). One of a board in the trash is compiled
// as it would be once the board is restored, against the board's own
// variables and nodes that are in the trash with it too, so that a board's
// deletion and its restoration change none of the expressions that it holds,
// which they do not compile again. The derivation finds the expressions
// that name what a batch changed by their names (expression_name): a row of
// each UUID and each key that compiling an expression's source gave, with
// its board, which it writes as it compiles the expression
// (IndexExpressionNames), and the expression's deletion deletes, read by an
// index of each, so that finding them takes as long as what it finds,
// however many things the batch changed, as a board's deletion changes 2,000
// nodes. As it compiles an expression, it also writes what the expression's
// AST names, and whether it reads or writes it, to the index of what the
// project's content names (package dependency, FR-DEP-01), which an
// expression that does not compile names nothing in. A rename of a
// variable's key, or of a component's, rewrites the sources that name what it
// renames, and those whose names its new key would take (PlanRename,
// FR-VAR-03), so that each names what it named; and the deletion of a
// variable or a component finds what uses it as the batch stands (InUse),
// which refuses it unless it is forced (FR-VAR-04).
//
// An expression is part of its owner, whose revision every change of it
// increments, the compiler's too, but while the owner is deleted to the trash
// itself, which nothing changes meanwhile, and whose restoration gives it a
// new revision; it is in the trash with its owner, and purged with it.
package expression

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// MaxSourceLength is the most characters that an expression's source may
// have, which the database holds it to as well (migration 00025): change
// both at once.
const MaxSourceLength = 16_384

// Field is a field of an owner that holds an expression (§5.8.5).
type Field string

// The fields of the owners: a node's on-enter and on-exit scripts, a
// connection's condition and on-traverse script, and a branch output's
// condition.
const (
	OnEnter    Field = "on_enter"
	OnExit     Field = "on_exit"
	Condition  Field = "condition"
	OnTraverse Field = "on_traverse"
)

// Context returns the context that the field's source is read in: a
// condition's, or a script's.
func (f Field) Context() expr.Context {
	if f == Condition {
		return expr.ContextCondition
	}
	return expr.ContextScript
}

// Owner is what an expression belongs to: a node, a connection, or a branch
// output, as Kind says, of the batch's project, with the board that it is of,
// at which the expression's names resolve.
type Owner struct {
	Kind  ids.Kind
	ID    pgtype.UUID
	Board pgtype.UUID
}

// fields are the fields of each kind of owner.
var fields = map[ids.Kind][]Field{
	ids.Node:         {OnEnter, OnExit},
	ids.Connection:   {Condition, OnTraverse},
	ids.BranchOutput: {Condition},
}

// Expression is an expression as the API shows it.
type Expression struct {
	Source     string          `json:"source" doc:"The expression's source, as its author wrote it (PRD §5.8)"`
	IsValid    bool            `json:"is_valid" doc:"Whether the source compiles, against what the project's expressions can name at the board of the expression's owner: an expression that does not is kept, marked invalid, which the validation reports, and which blocks publishing (PRD FR-EDIT-03 AC4)"`
	AST        json.RawMessage `json:"ast,omitempty" doc:"The AST of a valid expression, in its canonical form, its names resolved to the IDs of what they name (PRD §5.8.8): a condition's expression, or a script's script node, as the published format holds it. An invalid expression has none."`
	ASTVersion int             `json:"ast_version,omitempty" doc:"The ast_version of the constructs that a valid expression's AST uses (PRD FR-EXPR-04). An invalid expression has none."`
	Error      *CompileError   `json:"error,omitempty" doc:"The first error of an invalid expression's source. A valid expression has none."`
}

// CompileError is the first error of an expression's source, as the API
// shows it: expr.Error, whose fields it names as the expression corpus does
// (schema/expr-corpus/README.md).
type CompileError struct {
	Code        string   `json:"code" doc:"The error's stable code, as the expression corpus's README lists them (schema/expr-corpus/README.md)"`
	Start       int      `json:"start" doc:"The offset of the first character that the error covers, counted in code points from 0"`
	End         int      `json:"end" doc:"The offset of the character after the last that the error covers, counted in code points from 0: start itself for an error that covers none, as the end of a source that ends too early"`
	Type        string   `json:"type,omitempty" doc:"A type_mismatch's operand's type: bool, int, float, string, or node"`
	Expected    []string `json:"expected,omitempty" doc:"The types that a type_mismatch's operand's place takes"`
	Suggestions []string `json:"suggestions,omitempty" doc:"What an unknown name or function might be, nearest first, if anything near is"`
}

// CheckSource returns what is wrong with a source that a payload sets, or
// "": one longer than MaxSourceLength characters, as its schema says too. The
// pipeline refuses a NUL in every text of a payload, so no source holds one.
func CheckSource(source string) string {
	if utf8.RuneCountInString(source) > MaxSourceLength {
		return fmt.Sprintf("has more than %d characters", MaxSourceLength)
	}
	return ""
}

// Set sets the source of the expression of the field f of the owner o, of
// the batch's project, to *source, or removes the field's expression if
// source is nil, and reports whether that changed it, as a command's Apply
// does it before it reports its owner's change, so that the pipeline compiles
// the source once every command of the batch has applied, and which a command
// reports the owner's change for with Touch, or, if its update of the owner's
// other fields finds nothing to change, with OwnerRevision. The empty string
// is a source, which does not compile, as a branch output's condition is
// until it is written, unlike SetFields's, for which it is none: a field that
// may have no expression is written with SetFields. It returns an error if f
// is not a field of o's kind, or if it would write a source while the
// registry lacks the derivation that compiles it (Register).
func Set(ctx context.Context, tx *commands.Tx, o Owner, f Field, source *string) (bool, error) {
	if err := fieldOf(o.Kind, f); err != nil {
		return false, err
	}
	return write(ctx, tx, o, []fieldSource{{f, source}})
}

// SetFields sets the expression of each field of the owner o, of the batch's
// project, that sources holds a source of, to that source, or removes the
// field's expression for the empty string, as a node's scripts, and a
// connection's condition and on-traverse script, are written; a field that
// sources leaves out, or holds as nil, stays as it is. It reports whether
// that changed any of them, and returns an error as Set does. A branch
// output's condition, which may be empty, is written with Set.
func SetFields(ctx context.Context, tx *commands.Tx, o Owner, sources map[Field]*string) (bool, error) {
	var list []fieldSource
	for f, source := range sources {
		if err := fieldOf(o.Kind, f); err != nil {
			return false, err
		}
		if source != nil {
			list = append(list, fieldSource{f, source})
		}
	}
	// The fields are written in their order, whatever the map's.
	slices.SortFunc(list, func(a, b fieldSource) int {
		return slices.Index(fields[o.Kind], a.field) - slices.Index(fields[o.Kind], b.field)
	})
	for i := range list {
		if *list[i].source == "" {
			list[i].source = nil
		}
	}
	return write(ctx, tx, o, list)
}

// OwnerRevision returns the revision of the owner o that a command's update
// of o's other fields returned, rev, with err, as the command reports it,
// unless the update found nothing of them to change, which it returns as
// pgx.ErrNoRows, while the command changed o's expressions, as set says
// (Set, SetFields): a change of an owner's expressions is a change of the
// owner, so it then increments o's revision, and returns the new one. An
// update that changed nothing, of expressions that are as they were, is
// returned as it is, pgx.ErrNoRows, which the command reports as no change.
func OwnerRevision(ctx context.Context, tx *commands.Tx, o Owner, set bool, rev int64, err error) (int64, error) {
	if set && errors.Is(err, pgx.ErrNoRows) {
		return Touch(ctx, tx, o)
	}
	return rev, err
}

// fieldSource is the source that write writes at a field, or nil for none.
type fieldSource struct {
	field  Field
	source *string
}

// fieldOf returns an error, the mistake of a command's code, unless f is a
// field of the kind of owner, which Set, SetFields, and Back check of every
// field that they are given, before write, which takes them as checked.
func fieldOf(kind ids.Kind, f Field) error {
	if !slices.Contains(fields[kind], f) {
		return fmt.Errorf("expression: a %s has no field %s", kind, f)
	}
	return nil
}

// write sets the source of the expression of each field of the owner o, of
// the batch's project, that sources lists, a field of o's kind (fieldOf), to
// its source, or removes the field's expression if its source is nil, and
// reports whether that changed any of them, as Set does, reading o's
// expressions once.
func write(ctx context.Context, tx *commands.Tx, o Owner, sources []fieldSource) (bool, error) {
	if len(sources) == 0 {
		return false, nil
	}
	compiles := slices.ContainsFunc(sources, func(s fieldSource) bool { return s.source != nil })
	// A source that the command writes leaves the derivation work, unlike the
	// removal of one.
	if compiles {
		if err := leave(tx); err != nil {
			return false, err
		}
	}
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	stored, err := ownerExpressions(ctx, q, ws, project, o.Kind, []pgtype.UUID{o.ID})
	if err != nil {
		return false, err
	}
	changed := false
	for _, s := range sources {
		f, source := s.field, s.source
		i := slices.IndexFunc(stored, func(e queries.Expression) bool { return Field(e.Field) == f })
		var n int64
		switch {
		case i < 0 && source == nil:
			continue
		case i < 0:
			params := queries.InsertExpressionParams{WorkspaceID: ws, ProjectID: project, BoardID: o.Board,
				Field: string(f), Context: string(f.Context()), Source: *source}
			switch o.Kind {
			case ids.Node:
				params.NodeID = o.ID
			case ids.Connection:
				params.ConnectionID = o.ID
			default:
				params.BranchOutputID = o.ID
			}
			if err := q.InsertExpression(ctx, params); err != nil {
				return false, fmt.Errorf("expression: add the %s of the %s: %w", f, o.Kind, err)
			}
			n = 1
		case source == nil:
			if n, err = q.DeleteExpression(ctx, queries.DeleteExpressionParams{WorkspaceID: ws, ProjectID: project,
				ID: stored[i].ID}); err != nil {
				return false, fmt.Errorf("expression: delete the %s of the %s: %w", f, o.Kind, err)
			}
		default:
			if n, err = q.SetExpressionSource(ctx, queries.SetExpressionSourceParams{Source: *source,
				WorkspaceID: ws, ProjectID: project, ID: stored[i].ID}); err != nil {
				return false, fmt.Errorf("expression: set the %s of the %s: %w", f, o.Kind, err)
			}
		}
		changed = changed || n > 0
	}
	return changed, nil
}

// CopyNode gives the node target, a copy of the node source of the batch's
// project, the expressions of source, each of the same source, which the
// pipeline compiles once every command of the batch has applied, as Set
// does, and returns an error as Set does.
func CopyNode(ctx context.Context, tx *commands.Tx, source, target pgtype.UUID) error {
	n, err := queries.New(tx).CopyNodeExpressions(ctx, queries.CopyNodeExpressionsParams{NodeID: target,
		WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), SourceID: source})
	switch {
	case err != nil:
		return fmt.Errorf("expression: copy the node's expressions: %w", err)
	case n > 0:
		return leave(tx)
	}
	return nil
}

// leave reports that the command leaves sources for the derivation to
// compile (commands.Tx.Leave), so that it compiles them once every command of
// the batch has applied, and returns an error if the registry lacks the
// derivation (Register), which would leave them uncompiled.
func leave(tx *commands.Tx) error {
	if err := tx.Leave(Name); err != nil {
		return fmt.Errorf("expression: the registry does not compile expressions: register them (Register): %w", err)
	}
	return nil
}

// Back returns what the inverse of a command that sets the expressions of the
// owner of the kind whose UUID is id, of the project, as set holds them
// (SetFields, Set), sets them back to, by their fields: at each field that
// set holds a source of, the source that the field has, or the empty string
// for none, which removes the expression that the command adds; and nil at
// each that set leaves out, or holds as nil, which the command leaves as it
// is, and so does its inverse. It reads the owner's expressions only if set
// holds a source, and returns an error for a field that is not one of the
// kind's.
func Back(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, kind ids.Kind, id pgtype.UUID,
	set map[Field]*string) (map[Field]*string, error) {
	out := make(map[Field]*string, len(set))
	var sources map[Field]string
	for f, source := range set {
		if err := fieldOf(kind, f); err != nil {
			return nil, err
		}
		switch {
		case source == nil:
			continue
		case sources == nil:
			rows, err := ownerExpressions(ctx, q, ws, project, kind, []pgtype.UUID{id})
			if err != nil {
				return nil, err
			}
			sources = make(map[Field]string, len(rows))
			for _, row := range rows {
				sources[Field(row.Field)] = row.Source
			}
		}
		back := sources[f]
		out[f] = &back
	}
	return out, nil
}

// Of returns the expressions of the owners of the kind whose UUIDs are
// owners, of the project, as the API shows them, by their owners' UUIDs and
// then their fields. An owner without one is not in it. It returns an error
// for what the database's checks hold an expression to that one is not.
func Of(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, kind ids.Kind, owners []pgtype.UUID) (
	map[ids.UUID]map[Field]*Expression, error) {
	out := map[ids.UUID]map[Field]*Expression{}
	if len(owners) == 0 {
		return out, nil
	}
	rows, err := ownerExpressions(ctx, q, ws, project, kind, owners)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		e, err := expressionOf(row)
		if err != nil {
			return nil, err
		}
		owner := ownerOf(row)
		if out[owner] == nil {
			out[owner] = map[Field]*Expression{}
		}
		out[owner][Field(row.Field)] = e
	}
	return out, nil
}

// expressionOf returns the expression row as the API shows it, its AST in
// its canonical form (§5.8.8), which jsonb, which keeps neither the order of
// an object's keys nor how a number is written, does not keep.
func expressionOf(row queries.Expression) (*Expression, error) {
	e := &Expression{Source: row.Source, IsValid: row.IsValid}
	if row.Ast != nil {
		n, err := expr.Decode(row.Ast, Field(row.Field).Context())
		if err != nil {
			return nil, fmt.Errorf("expression: the AST of the %s of %s: %w", row.Field, ownerName(row), err)
		}
		if e.AST, err = expr.Encode(n); err != nil {
			return nil, fmt.Errorf("expression: the AST of the %s of %s: %w", row.Field, ownerName(row), err)
		}
		e.ASTVersion = int(row.AstVersion.Int32)
	}
	if row.Error != nil {
		e.Error = &CompileError{}
		if err := json.Unmarshal(row.Error, e.Error); err != nil {
			return nil, fmt.Errorf("expression: the error of the %s of %s: %w", row.Field, ownerName(row), err)
		}
	}
	return e, nil
}

// ownerExpressions returns the expressions of the owners of the kind whose
// UUIDs are owners, of the project.
func ownerExpressions(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, kind ids.Kind,
	owners []pgtype.UUID) ([]queries.Expression, error) {
	params := queries.OwnerExpressionsParams{WorkspaceID: ws, ProjectID: project, NodeIds: []pgtype.UUID{},
		ConnectionIds: []pgtype.UUID{}, OutputIds: []pgtype.UUID{}}
	switch kind {
	case ids.Node:
		params.NodeIds = owners
	case ids.Connection:
		params.ConnectionIds = owners
	case ids.BranchOutput:
		params.OutputIds = owners
	default:
		return nil, fmt.Errorf("expression: a %s has no expressions", kind)
	}
	rows, err := q.OwnerExpressions(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("expression: read the expressions of the %ss: %w", kind, err)
	}
	return rows, nil
}

// ownerOf returns the UUID of the expression row's owner.
func ownerOf(row queries.Expression) ids.UUID {
	switch {
	case row.NodeID.Valid:
		return row.NodeID.Bytes
	case row.ConnectionID.Valid:
		return row.ConnectionID.Bytes
	}
	return row.BranchOutputID.Bytes
}

// ownerName returns the ID of the expression row's owner, for an error.
func ownerName(row queries.Expression) string {
	switch {
	case row.NodeID.Valid:
		return ids.Encode(ids.Node, row.NodeID.Bytes)
	case row.ConnectionID.Valid:
		return ids.Encode(ids.Connection, row.ConnectionID.Bytes)
	}
	return ids.Encode(ids.BranchOutput, row.BranchOutputID.Bytes)
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
