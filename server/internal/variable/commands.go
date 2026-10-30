package variable

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// The types of command of a variable (PRD §5.7). Each names the variable
// that it is about by its ID, id, and applies to the variable of the batch's
// project.
const (
	// Create creates a variable, whose ID the client makes, as it makes the
	// command's, so that it can refer to the variable before the server
	// answers. Deleting it to the trash undoes it.
	Create commands.Type = "variable.create"
	// Rename changes a variable's key, and rewrites the sources of the
	// expressions that name it, and of those whose names the new key would
	// take, so that each names what it named (FR-VAR-03), Describe its
	// description, SetDefault its default, Retype its type, with a default of
	// the new type, SetScope its scope, and SetResetOnEnter its
	// reset_on_enter. Changing each back undoes it, a rename with the sources
	// that it rewrote as they were.
	Rename          commands.Type = "variable.rename"
	Describe        commands.Type = "variable.describe"
	SetDefault      commands.Type = "variable.set_default"
	Retype          commands.Type = "variable.retype"
	SetScope        commands.Type = "variable.set_scope"
	SetResetOnEnter commands.Type = "variable.set_reset_on_enter"
	// Move moves a variable into a folder of variables, or to the top, at a
	// place among the folder's children. Moving it back undoes it.
	Move commands.Type = "variable.move"
	// Delete deletes a variable to the project's trash, unless sites of the
	// project use it, or the deletion forces it (FR-VAR-04), and Restore
	// restores it, with the folders above it that were deleted since. Each
	// undoes the other.
	Delete  commands.Type = "variable.delete"
	Restore commands.Type = "variable.restore"
)

// createPayload is Create's payload.
type createPayload struct {
	ID           string `json:"id" doc:"The new variable's ID, a var_ ID of a UUIDv7 that the client makes, which no entity of the instance has (PRD §5.2)"`
	Key          string `json:"key" doc:"The variable's key, by which expressions name it: an identifier of the expression language (PRD §5.8.1): an ASCII letter or an underscore, then ASCII letters, digits, and underscores, of at most 64 characters, as met_elara, compared case by case, and not one of its reserved words, true, false, and, or, not, global, board, null, if, else, and elseif, which is answered with 422 key_invalid, and that no other variable of its scope has, of those that are not in the project's trash, the project's global variables for a global, and its board's for a board's, which is answered with 409 key_taken."`
	Type         string `json:"type" enum:"bool,int,float,string" doc:"The variable's type"`
	Scope        string `json:"scope" enum:"global,board" doc:"Who reads the variable: every board's expressions, for global, or only its board's, for board (PRD FR-VAR-02). A board's variable keeps its value across the board's entries, as a global does, unless reset_on_enter is set."`
	BoardID      string `json:"board_id,omitempty" doc:"The ID of the variable's board, which is not in the project's trash, if scope is board. A global variable has none."`
	Default      Value  `json:"default,omitzero" doc:"The variable's default, a value of its type: true or false for bool; an integer from -9007199254740991 to 9007199254740991 for int, which every runtime holds exactly (PRD §5.8.9), written as 3, 3.0, or 3e0 alike; a number for float, which is read as the nearest double, and is not beyond a double's range; and a string of at most 4096 characters, without control characters but line feeds and tabs, for string. A value of another kind, as a string for an int (PRD FR-VAR-01 AC2), is answered with 422 validation_failed. A number written with more than 327 characters, or with an exponent beyond ±999, as 0e1000, is answered with 422 validation_failed, whatever it reads as: no int or float needs more, the longest, -5e-324 written without an exponent, as the server may write a number back, having 327. A number beyond a double's range, as 1e400 or 1e1000, is answered with 400 invalid_request as the API reads the body, unless it is written with more than 327 characters, which the API's reader may take for a number within the range, and which is then answered with 422 validation_failed. Without it, the type's zero value: false, 0, 0.0, or the empty string."`
	Description  string `json:"description,omitempty" maxLength:"2000" doc:"The variable's description, of at most 2000 characters, without white space around it, and without control characters but line feeds and tabs. Without it, the variable has none."`
	ResetOnEnter bool   `json:"reset_on_enter,omitempty" doc:"Whether the variable is reset to its default each time the flow enters its board (PRD FR-VAR-02, §5.8.5), which only a board's variable may be. Without it, false."`
	FolderID     string `json:"folder_id,omitempty" doc:"The ID of the folder of variables that the variable goes into, which is not deleted. Without it, the variable goes at the top."`
	Order        string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The variable's place among the children of its folder, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and those with the same key by their IDs without the prefix, var_ or fld_, compared by their bytes too: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the variable goes after the last child."`
}

// renamePayload is Rename's payload.
type renamePayload struct {
	ID  string `json:"id" doc:"The variable's ID"`
	Key string `json:"key" doc:"The variable's new key: an identifier of the expression language (PRD §5.8.1): an ASCII letter or an underscore, then ASCII letters, digits, and underscores, of at most 64 characters, as met_elara, compared case by case, and not one of its reserved words, true, false, and, or, not, global, board, null, if, else, and elseif, which is answered with 422 key_invalid, and that no other variable of its scope has, of those that are not in the project's trash, the project's global variables for a global, and its board's for a board's, which is answered with 409 key_taken. The variable keeps its ID, by which expressions refer to it. The rename rewrites the source of each expression of the project, in its trash or not, that names the variable, in the shortest form that names it from the expression's board, its bare key unless a variable of the board has the key of a global one, which global.<key> names, and of each that names another variable or a component's attribute whose name the new key would take, which is then qualified, as global.<key> or cmp.<component>.<attribute>, so that each names what it named (PRD FR-VAR-03), as GET /api/v1/projects/{project_id}/variables/{variable_id}/rename-preview lists them. A rename that would make a source longer than 16384 characters is answered with 422 validation_failed, before anything is written."`
	// Sources is a pointer, so that an empty list, as the undo of a rename
	// that rewrote nothing writes, is told from none, a rename's own.
	Sources *[]expression.Source `json:"sources,omitempty" doc:"The sources that the undo of a rename gives back to the expressions of the owners' fields that it lists, those that the rename rewrote, exactly as they were: each to its field's expression where the rename of the variable from key back to the key that it has would rewrite that source to the expression's as it is, which it then names and does exactly as. Every other expression that names the variable, or whose names key would take, as one that names the variable by the key that it has written since the rename, and one that it lists whose source has changed since, is rewritten as key says, as without it, and an owner's field that it lists that has no expression is passed over. Without it, the rename rewrites the sources as key says."`
}

// deletePayload is Delete's payload.
type deletePayload struct {
	ID    string `json:"id" doc:"The variable's ID"`
	Force bool   `json:"force,omitempty" doc:"Whether to delete the variable even though sites of the project that are not in its trash use it, as its usage lists list them: the expressions that read it or write it, which then name what no longer resolves, and are invalid, each a validation error, and the texts whose value references show it, which then show nothing (PRD FR-VAR-04 AC2). Only a role that may publish the project's snapshots may force a deletion, which a role that may not is answered with 403 insufficient_role for; an API token needs project:write for it, as for any deletion, and not snapshot:write. Without it, a variable that sites use is not deleted: the command is answered with 409 variable_in_use, which lists them, as the project stands once the batch's commands before it have applied, so that a batch that first changes the last of them deletes the variable."`
}

// describePayload is Describe's payload.
type describePayload struct {
	ID          string `json:"id" doc:"The variable's ID"`
	Description string `json:"description" maxLength:"2000" doc:"The variable's new description, of at most 2000 characters, which may be empty, without white space around it, and without control characters but line feeds and tabs"`
}

// defaultPayload is SetDefault's payload.
type defaultPayload struct {
	ID      string `json:"id" doc:"The variable's ID"`
	Default Value  `json:"default" doc:"The variable's new default, a value of its type: true or false for bool; an integer from -9007199254740991 to 9007199254740991 for int, which every runtime holds exactly (PRD §5.8.9), written as 3, 3.0, or 3e0 alike; a number for float, which is read as the nearest double, and is not beyond a double's range; and a string of at most 4096 characters, without control characters but line feeds and tabs, for string. A value of another kind, as a string for an int (PRD FR-VAR-01 AC2), is answered with 422 validation_failed. A number written with more than 327 characters, or with an exponent beyond ±999, as 0e1000, is answered with 422 validation_failed, whatever it reads as: no int or float needs more, the longest, -5e-324 written without an exponent, as the server may write a number back, having 327. A number beyond a double's range, as 1e400 or 1e1000, is answered with 400 invalid_request as the API reads the body, unless it is written with more than 327 characters, which the API's reader may take for a number within the range, and which is then answered with 422 validation_failed."`
}

// retypePayload is Retype's payload.
type retypePayload struct {
	ID      string `json:"id" doc:"The variable's ID"`
	Type    string `json:"type" enum:"bool,int,float,string" doc:"The variable's new type"`
	Default Value  `json:"default,omitzero" doc:"The variable's default of its new type, a value of its type: true or false for bool; an integer from -9007199254740991 to 9007199254740991 for int, which every runtime holds exactly (PRD §5.8.9), written as 3, 3.0, or 3e0 alike; a number for float, which is read as the nearest double, and is not beyond a double's range; and a string of at most 4096 characters, without control characters but line feeds and tabs, for string. A value of another kind, as a string for an int (PRD FR-VAR-01 AC2), is answered with 422 validation_failed. A number written with more than 327 characters, or with an exponent beyond ±999, as 0e1000, is answered with 422 validation_failed, whatever it reads as: no int or float needs more, the longest, -5e-324 written without an exponent, as the server may write a number back, having 327. A number beyond a double's range, as 1e400 or 1e1000, is answered with 400 invalid_request as the API reads the body, unless it is written with more than 327 characters, which the API's reader may take for a number within the range, and which is then answered with 422 validation_failed. Without it, the type's zero value: false, 0, 0.0, or the empty string."`
}

// scopePayload is SetScope's payload.
type scopePayload struct {
	ID      string `json:"id" doc:"The variable's ID"`
	Scope   string `json:"scope" enum:"global,board" doc:"The variable's new scope. Who reads the variable: every board's expressions, for global, or only its board's, for board (PRD FR-VAR-02). A board's variable keeps its value across the board's entries, as a global does, unless reset_on_enter is set. A global variable's reset_on_enter is unset, so unset it first, and no other variable of the new scope, of those that are not in the project's trash, may have the variable's key, which is answered with 409 key_taken."`
	BoardID string `json:"board_id,omitempty" doc:"The ID of the variable's board, which is not in the project's trash, if scope is board. A global variable has none."`
}

// resetPayload is SetResetOnEnter's payload.
type resetPayload struct {
	ID           string `json:"id" doc:"The variable's ID"`
	ResetOnEnter bool   `json:"reset_on_enter" doc:"Whether the variable is reset to its default each time the flow enters its board (PRD FR-VAR-02, §5.8.5), which only a board's variable may be."`
}

// movePayload is Move's payload.
type movePayload struct {
	ID       string `json:"id" doc:"The variable's ID"`
	FolderID string `json:"folder_id,omitempty" doc:"The ID of the folder of variables that the variable goes into, which is not deleted. Without it, the variable goes to the top."`
	Order    string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The variable's place among the children of its folder, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and those with the same key by their IDs without the prefix, var_ or fld_, compared by their bytes too: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the variable goes after the last child, or stays where it is if it is the last child of the folder already that is not deleted, a variable of a board in the project's trash counting as a deleted one."`
}

// idPayload is the payload of a command that needs nothing but its
// variable's ID.
type idPayload struct {
	ID string `json:"id" doc:"The variable's ID"`
}

// Register registers in r the variable as an entity of the pipeline, whose
// revisions a command expects as it does any entity's, and its commands, each
// of which changes the project's draft, so that an API token needs
// project:write for it (FR-AUTH-04), and its role "Manage variables,
// components, component types" (§5.6). The variable's commands refer to its
// board and its folder, so r has the board and the folder registered already
// (board.Register).
func Register(r *commands.Registry) {
	commands.RegisterEntity(r, commands.Entity{
		Kind: ids.Variable,
		Revs: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]int64, error) {
			rows, err := queries.New(tx).VariableRevs(ctx, queries.VariableRevsParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]int64, len(rows))
			for _, row := range rows {
				out[row.ID.Bytes] = row.Rev
			}
			return out, nil
		},
		States: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]any, error) {
			rows, err := queries.New(tx).VariablesByID(ctx, queries.VariablesByIDParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]any, len(rows))
			for _, row := range rows {
				v, err := variableOf(row.Variable)
				if err != nil {
					return nil, err
				}
				// A variable of a board in the trash is in the trash with it,
				// which its state says, as the state of an entity in the trash
				// does, though only its board's deletion put it there.
				if v.DeletedAt == nil && row.BoardDeletedAt.Valid {
					v.DeletedAt = timeOf(row.BoardDeletedAt)
				}
				out[row.Variable.ID.Bytes] = v
			}
			return out, nil
		},
	})

	variable := []ids.Kind{ids.Variable}
	op, sc := authz.ManageVariablesAndComponents, scope.ProjectWrite

	commands.Register(r, commands.Spec[createPayload]{
		Type: Create, Capability: op, Scope: sc, Touches: []ids.Kind{ids.Variable, ids.Board, ids.Folder},
		Check: func(p createPayload) input.FieldErrors {
			errs := newID(ids.Variable, p.ID)
			errs = append(errs, checkScope(p.Scope, p.BoardID)...)
			errs = append(errs, checkReset("reset_on_enter", p.Scope, p.ResetOnEnter)...)
			errs = append(errs, checkDefault(p.Type, p.Default)...)
			errs = append(errs, field("description", checkDescription(p.Description))...)
			return append(errs, optionalID(ids.Folder, "folder_id", p.FolderID)...)
		},
		// The undo of a create deletes the variable, forcing the deletion if
		// what the create makes of the project uses it, as an expression that
		// named its key before it existed, so that the undo gives the project
		// back as it was, but not if only what came later uses it, which the
		// undo is then refused for, as a deletion is. A forced undo deletes
		// it whatever uses it then, as any forced deletion does, and needs a
		// role that may publish, as any does (FR-VAR-04).
		Inverse: func(ctx context.Context, tx *commands.Tx, p createPayload) ([]commands.Step, error) {
			var board ids.UUID
			if p.BoardID != "" {
				board = decoded(ids.Board, p.BoardID).Bytes
			}
			force, err := expression.Used(ctx, tx, expression.Subject{Kind: ids.Variable,
				ID: decoded(ids.Variable, p.ID).Bytes, Key: p.Key, Board: board, Absent: true, Type: p.Type})
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Delete, Payload: deletePayload{ID: p.ID, Force: force}}}, nil
		},
		Apply: applyCreate,
	})

	// A rename rewrites the sources of the expressions of nodes, connections,
	// and branch outputs, whose changes it reports.
	renamed := []ids.Kind{ids.Variable, ids.Node, ids.Connection, ids.BranchOutput}
	commands.Register(r, commands.Spec[renamePayload]{
		Type: Rename, Capability: op, Scope: sc, Touches: renamed, UndoField: "key",
		Check: func(p renamePayload) input.FieldErrors {
			errs := entityID(ids.Variable, "id", p.ID)
			if p.Sources != nil {
				errs = append(errs, expression.CheckSources("sources", *p.Sources)...)
			}
			return errs
		},
		Inverse: inverseRename,
		Apply:   applyRename,
	})

	commands.Register(r, commands.Spec[describePayload]{
		Type: Describe, Capability: op, Scope: sc, Touches: variable,
		Check: func(p describePayload) input.FieldErrors { return entityID(ids.Variable, "id", p.ID) },
		// The step of the inverse writes back the stored description, which
		// the text rules may refuse (commands.Spec.CheckNew), as those of
		// SetDefault and Retype write back a stored default, which their Apply
		// checks. The database holds a description to the length that the
		// payload's schema allows.
		CheckNew: func(p describePayload) input.FieldErrors {
			return field("description", checkDescription(p.Description))
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p describePayload) ([]commands.Step, error) {
			v, err := liveVariable(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Describe, Payload: describePayload{ID: p.ID, Description: v.Description}}},
				nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p describePayload) error {
			v, err := liveVariable(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			return changed(tx, v.ID)(queries.New(tx).UpdateVariableDescription(ctx,
				queries.UpdateVariableDescriptionParams{Description: p.Description, UpdatedBy: by(tx),
					WorkspaceID: v.WorkspaceID, ProjectID: v.ProjectID, ID: v.ID}))
		},
	})

	commands.Register(r, commands.Spec[defaultPayload]{
		Type: SetDefault, Capability: op, Scope: sc, Touches: variable,
		Check: func(p defaultPayload) input.FieldErrors { return entityID(ids.Variable, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p defaultPayload) ([]commands.Step, error) {
			v, err := liveVariable(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			old, err := stored(v)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetDefault, Payload: defaultPayload{ID: p.ID, Default: old}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p defaultPayload) error {
			v, err := liveVariable(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			// The default's type is the variable's, which the payload does not
			// say.
			def, problem := of(p.Default, v.Type, value.All)
			if problem != "" {
				return commands.Refuse("default", problem, nil)
			}
			return setValue(ctx, tx, v, v.Type, def)
		},
	})

	commands.Register(r, commands.Spec[retypePayload]{
		Type: Retype, Capability: op, Scope: sc, Touches: variable,
		// Check checks the default against the type, and Apply checks its
		// characters too, as SetDefault's Apply does, so that the pipeline
		// takes the inverse of a retype of a variable whose stored default has
		// characters that input.CheckText refuses (stored), which Apply then
		// refuses as an undo applies it, rather than fail the retype.
		Check: func(p retypePayload) input.FieldErrors {
			_, problem := of(p.Default, p.Type, value.Checks{Length: true})
			return append(entityID(ids.Variable, "id", p.ID), field("default", problem)...)
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p retypePayload) ([]commands.Step, error) {
			v, err := liveVariable(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			old, err := stored(v)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Retype, Payload: retypePayload{ID: p.ID, Type: v.Type, Default: old}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p retypePayload) error {
			v, err := liveVariable(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			def, problem := of(p.Default, p.Type, value.All)
			if problem != "" {
				return commands.Refuse("default", problem, nil)
			}
			return setValue(ctx, tx, v, p.Type, def)
		},
	})

	commands.Register(r, commands.Spec[scopePayload]{
		Type: SetScope, Capability: op, Scope: sc, Touches: []ids.Kind{ids.Variable, ids.Board},
		Check: func(p scopePayload) input.FieldErrors {
			return append(entityID(ids.Variable, "id", p.ID), checkScope(p.Scope, p.BoardID)...)
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p scopePayload) ([]commands.Step, error) {
			v, err := liveVariable(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetScope, Payload: scopePayload{ID: p.ID, Scope: v.Scope,
				BoardID: encode(ids.Board, v.BoardID)}}}, nil
		},
		Apply: applySetScope,
	})

	commands.Register(r, commands.Spec[resetPayload]{
		Type: SetResetOnEnter, Capability: op, Scope: sc, Touches: variable,
		Check: func(p resetPayload) input.FieldErrors { return entityID(ids.Variable, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p resetPayload) ([]commands.Step, error) {
			v, err := liveVariable(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetResetOnEnter, Payload: resetPayload{ID: p.ID,
				ResetOnEnter: v.ResetOnEnter}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p resetPayload) error {
			v, err := liveVariable(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			if err := refuse(checkReset("reset_on_enter", v.Scope, p.ResetOnEnter)); err != nil {
				return err
			}
			return changed(tx, v.ID)(queries.New(tx).UpdateVariableResetOnEnter(ctx,
				queries.UpdateVariableResetOnEnterParams{ResetOnEnter: p.ResetOnEnter, UpdatedBy: by(tx),
					WorkspaceID: v.WorkspaceID, ProjectID: v.ProjectID, ID: v.ID}))
		},
	})

	commands.Register(r, commands.Spec[movePayload]{
		Type: Move, Capability: op, Scope: sc, Touches: []ids.Kind{ids.Variable, ids.Folder},
		Check: func(p movePayload) input.FieldErrors {
			return append(entityID(ids.Variable, "id", p.ID), optionalID(ids.Folder, "folder_id", p.FolderID)...)
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p movePayload) ([]commands.Step, error) {
			v, err := liveVariable(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Move, Payload: movePayload{ID: p.ID, FolderID: encode(ids.Folder,
				v.FolderID), Order: v.Order}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p movePayload) error {
			v, err := liveVariable(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			q := queries.New(tx)
			folder, err := board.LiveFolder(ctx, q, v.WorkspaceID, v.ProjectID, board.KindVariable, "folder_id",
				p.FolderID)
			if err != nil {
				return err
			}
			order, err := board.SortKey(ctx, q, v.WorkspaceID, v.ProjectID, board.KindVariable, folder, p.Order,
				&board.Child{ID: v.ID, Parent: v.FolderID, Key: v.Order, Kind: ids.Variable})
			if err != nil {
				return err
			}
			return changed(tx, v.ID)(q.MoveVariable(ctx, queries.MoveVariableParams{FolderID: folder,
				SortKey: order, UpdatedBy: by(tx), WorkspaceID: v.WorkspaceID, ProjectID: v.ProjectID, ID: v.ID}))
		},
	})

	commands.Register(r, commands.Spec[deletePayload]{
		Type: Delete, Capability: op, Scope: sc, Touches: variable,
		Check: func(p deletePayload) input.FieldErrors { return entityID(ids.Variable, "id", p.ID) },
		// Deleting a variable that is in the trash already does nothing, and
		// its undo nothing either. One of a board in the trash, in the trash
		// with it but not deleted itself, is refused, as every command but
		// its board's restoration refuses it, because its undo, a restore,
		// would be refused until the board is restored: the undo of a batch
		// that deletes the board and then the variable restores the variable
		// first. So a board's variable is deleted on its own only before its
		// board, and the undo of its creation waits for its board's
		// restoration, as the undo of its rename does.
		Inverse: func(ctx context.Context, tx *commands.Tx, p deletePayload) ([]commands.Step, error) {
			row, err := anyVariable(ctx, tx, p.ID)
			if err != nil || row.Variable.DeletedAt.Valid || row.BoardDeleted {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Restore, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: applyDelete,
	})

	commands.Register(r, commands.Spec[idPayload]{
		Type: Restore, Capability: op, Scope: sc, Touches: []ids.Kind{ids.Variable, ids.Folder},
		Check: func(p idPayload) input.FieldErrors { return entityID(ids.Variable, "id", p.ID) },
		// Restoring a variable that is not in the trash does nothing, and its
		// undo nothing either. A variable of a board in the trash, which is in
		// the trash with it, deleted on its own or not, is refused, since it
		// comes back only with its board, so the inverse of its restore, which
		// Apply refuses, reads nothing more. The undo of a restore deletes the
		// variable, and then the folders that the restore restored above it,
		// from the nearest, each of which then holds nothing that is not
		// deleted.
		Inverse: func(ctx context.Context, tx *commands.Tx, p idPayload) ([]commands.Step, error) {
			row, err := anyVariable(ctx, tx, p.ID)
			if err != nil || !row.Variable.DeletedAt.Valid || row.BoardDeleted {
				return []commands.Step{}, err
			}
			v := row.Variable
			deleted, err := board.DeletedFolders(ctx, queries.New(tx), v.WorkspaceID, v.ProjectID, v.FolderID)
			if err != nil {
				return nil, err
			}
			// The undo forces the deletion if what the restore makes of the
			// project uses the variable, as an expression that names its key,
			// as the undo of a create does.
			force, err := expression.Used(ctx, tx, expression.Subject{Kind: ids.Variable, ID: v.ID.Bytes, Key: v.Key,
				Board: v.BoardID.Bytes, Absent: true, Type: v.Type})
			if err != nil {
				return nil, err
			}
			return append([]commands.Step{{Type: Delete, Payload: deletePayload{ID: p.ID, Force: force}}},
				board.DeleteFolderSteps(deleted)...), nil
		},
		Apply: applyRestore,
	})
}

// applyCreate is Create's Apply.
func applyCreate(ctx context.Context, tx *commands.Tx, p createPayload) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	id := decoded(ids.Variable, p.ID)
	// A variable of the project with the ID is refused for its ID before
	// anything else is checked, so that a create sent again with a new command
	// ID, as an offline client's keep-mine sends it (FR-OFF-04), is refused for
	// its ID rather than for the key that its variable has taken; the insert
	// below refuses the ID of a variable that this transaction does not see.
	if _, err := q.VariableAny(ctx, queries.VariableAnyParams{WorkspaceID: ws, ProjectID: project, ID: id}); err == nil {
		return commands.Refuse("id", "is the ID of a variable that exists already", nil)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("variable: read the variable: %w", err)
	}
	boardID, err := liveBoard(ctx, q, ws, project, p.BoardID)
	if err != nil {
		return err
	}
	if err := checkKey(ctx, q, ws, project, "key", p.Key, boardID, pgtype.UUID{}); err != nil {
		return err
	}
	folder, err := board.LiveFolder(ctx, q, ws, project, board.KindVariable, "folder_id", p.FolderID)
	if err != nil {
		return err
	}
	order, err := board.SortKey(ctx, q, ws, project, board.KindVariable, folder, p.Order, nil)
	if err != nil {
		return err
	}
	// Check checked the default against the type.
	def, _ := of(p.Default, p.Type, value.All)
	err = q.InsertVariable(ctx, queries.InsertVariableParams{ID: id, WorkspaceID: ws, ProjectID: project,
		Scope: p.Scope, BoardID: boardID, FolderID: folder, SortKey: order, Key: p.Key, Type: p.Type,
		DefaultValue: def.JSON(), Description: p.Description, ResetOnEnter: p.ResetOnEnter, CreatedBy: by(tx)})
	if database.UniqueViolation(err, "variable_pkey") {
		// The ID is another workspace's, or another project's, whose rows this
		// transaction does not see.
		return commands.Refuse("id", "is the ID of a variable that exists already", nil)
	}
	if err != nil {
		return fmt.Errorf("variable: add the variable: %w", err)
	}
	return tx.Changed(ids.Variable, id.Bytes, 1)
}

// applySetScope is SetScope's Apply.
func applySetScope(ctx context.Context, tx *commands.Tx, p scopePayload) error {
	v, err := liveVariable(ctx, tx, p.ID)
	if err != nil {
		return err
	}
	q := queries.New(tx)
	boardID, err := liveBoard(ctx, q, v.WorkspaceID, v.ProjectID, p.BoardID)
	if err != nil {
		return err
	}
	if err := refuse(checkReset("scope", p.Scope, v.ResetOnEnter)); err != nil {
		return err
	}
	// The key is checked in the new scope, where another variable may have
	// it, as the field that names the scope.
	at := "scope"
	if boardID.Valid {
		at = "board_id"
	}
	if err := keyFree(ctx, q, v.WorkspaceID, v.ProjectID, at, v.Key, boardID, v.ID,
		"is a scope where another variable, which is not in the project's trash, has the variable's key"); err != nil {
		return err
	}
	return changed(tx, v.ID)(q.UpdateVariableScope(ctx, queries.UpdateVariableScopeParams{Scope: p.Scope,
		BoardID: boardID, UpdatedBy: by(tx), WorkspaceID: v.WorkspaceID, ProjectID: v.ProjectID, ID: v.ID}))
}

// applyRestore is Restore's Apply.
func applyRestore(ctx context.Context, tx *commands.Tx, p idPayload) error {
	row, err := anyVariable(ctx, tx, p.ID)
	switch {
	case err != nil:
		return err
	case row.BoardDeleted:
		return commands.Refuse("id", "is a variable of a board in the project's trash, which comes back only with "+
			"its board: restore the board first", nil)
	case !row.Variable.DeletedAt.Valid:
		return nil
	}
	v := row.Variable
	q := queries.New(tx)
	// The variable in the trash, which nothing but its restore changes, keeps
	// its key, so the variable that has taken it is the one to change.
	if err := keyFree(ctx, q, v.WorkspaceID, v.ProjectID, "id", v.Key, v.BoardID, v.ID,
		"is a variable whose key another variable of its scope, which is not in the trash, has taken since it was "+
			"deleted: rename that variable, or delete it, first"); err != nil {
		return err
	}
	if err := board.RestoreFolders(ctx, tx, q, v.WorkspaceID, v.ProjectID, v.FolderID); err != nil {
		return err
	}
	return changed(tx, v.ID)(q.RestoreVariable(ctx, queries.RestoreVariableParams{UpdatedBy: by(tx),
		WorkspaceID: v.WorkspaceID, ProjectID: v.ProjectID, ID: v.ID}))
}

// applyDelete is Delete's Apply. A variable that sites of the project use,
// as the batch's commands before it leave the project, is deleted only if the
// deletion forces it, which the actor's role must allow, and is refused with
// a *dependency.InUseError otherwise (expression.GuardDeletion, FR-VAR-04).
func applyDelete(ctx context.Context, tx *commands.Tx, p deletePayload) error {
	row, err := anyVariable(ctx, tx, p.ID)
	switch {
	case err != nil:
		return err
	case row.Variable.DeletedAt.Valid:
		return nil
	case row.BoardDeleted:
		return refuseBoardInTrash()
	}
	v := row.Variable
	if err := expression.GuardDeletion(ctx, tx, expression.Subject{Kind: ids.Variable, ID: v.ID.Bytes, Key: v.Key,
		Board: v.BoardID.Bytes}, p.Force); err != nil {
		return err
	}
	return changed(tx, v.ID)(queries.New(tx).TrashVariable(ctx, queries.TrashVariableParams{UpdatedBy: by(tx),
		WorkspaceID: v.WorkspaceID, ProjectID: v.ProjectID, ID: v.ID}))
}

// inverseRename is Rename's Inverse: a rename back to the variable's key,
// with the sources that the rename writes as they are, which the undo gives
// back rather than rewrite them anew, so that it gives them back exactly as
// their authors wrote them, a name that the rename shortened, as
// global.<key> to <key>, among them, and leaves as they are those that the
// rename leaves, as one whose name names nothing until the rename makes it
// name the variable (expression.RenameInverse).
func inverseRename(ctx context.Context, tx *commands.Tx, p renamePayload) ([]commands.Step, error) {
	v, err := liveVariable(ctx, tx, p.ID)
	if err != nil {
		return nil, err
	}
	// The key is checked before the rewrites are planned, which need a key
	// that the variable may have, as Apply checks it.
	if err := checkKey(ctx, queries.New(tx), v.WorkspaceID, v.ProjectID, "key", p.Key, v.BoardID, v.ID); err != nil {
		return nil, err
	}
	return expression.RenameInverse(ctx, tx, Rename, "key", rename(v, p.Key), p.Sources,
		func(back []expression.Source) any { return renamePayload{ID: p.ID, Key: v.Key, Sources: &back} })
}

// applyRename is Rename's Apply: it checks the new key, before it writes
// anything (FR-VAR-03 AC3), writes the sources that the rename writes
// (expression.ApplyRename), and changes the key.
func applyRename(ctx context.Context, tx *commands.Tx, p renamePayload) error {
	v, err := liveVariable(ctx, tx, p.ID)
	if err != nil {
		return err
	}
	q := queries.New(tx)
	if err := checkKey(ctx, q, v.WorkspaceID, v.ProjectID, "key", p.Key, v.BoardID, v.ID); err != nil {
		return err
	}
	if err := expression.ApplyRename(ctx, tx, "key", rename(v, p.Key), p.Sources); err != nil {
		return err
	}
	return changed(tx, v.ID)(q.UpdateVariableKey(ctx, queries.UpdateVariableKeyParams{Key: p.Key,
		UpdatedBy: by(tx), WorkspaceID: v.WorkspaceID, ProjectID: v.ProjectID, ID: v.ID}))
}

// rename returns the rename of the variable v to the key.
func rename(v queries.Variable, key string) expression.Rename {
	return expression.Rename{Kind: ids.Variable, ID: v.ID.Bytes, Board: v.BoardID.Bytes, From: v.Key, To: key}
}

// setValue changes the type of the variable v to typ, and its default to
// def, a value of typ that Value.Of returned, and reports the change.
func setValue(ctx context.Context, tx *commands.Tx, v queries.Variable, typ string, def Value) error {
	return changed(tx, v.ID)(queries.New(tx).UpdateVariableValue(ctx, queries.UpdateVariableValueParams{Type: typ,
		DefaultValue: def.JSON(), UpdatedBy: by(tx), WorkspaceID: v.WorkspaceID, ProjectID: v.ProjectID, ID: v.ID}))
}

// anyVariable returns the variable of the batch's project whose ID is id, a
// variable's ID that its payload's Check checked, whether or not it is in the
// trash, with whether its board is, or refuses the batch if the project has
// no such variable.
func anyVariable(ctx context.Context, tx *commands.Tx, id string) (queries.VariableAnyRow, error) {
	row, err := queries.New(tx).VariableAny(ctx, queries.VariableAnyParams{WorkspaceID: nullUUID(tx.WorkspaceID()),
		ProjectID: nullUUID(tx.ProjectID()), ID: decoded(ids.Variable, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.VariableAnyRow{}, commands.Refuse("id", "is not a variable of the project", nil)
	case err != nil:
		return queries.VariableAnyRow{}, fmt.Errorf("variable: read the variable: %w", err)
	}
	return row, nil
}

// liveVariable returns the variable of the batch's project whose ID is id as
// anyVariable does, or refuses the batch if it is in the trash, where nothing
// but restoring it changes it, or its board is, with which it comes back.
func liveVariable(ctx context.Context, tx *commands.Tx, id string) (queries.Variable, error) {
	row, err := anyVariable(ctx, tx, id)
	switch {
	case err != nil:
		return queries.Variable{}, err
	case row.Variable.DeletedAt.Valid:
		return queries.Variable{}, commands.Refuse("id", "is a variable in the project's trash, which only "+
			"restoring it changes", nil)
	case row.BoardDeleted:
		return queries.Variable{}, refuseBoardInTrash()
	}
	return row.Variable, nil
}

// refuseBoardInTrash returns the refusal of a command that would change a
// variable of a board in the trash, which is in the trash with it, and which
// nothing but the board's restoration changes.
func refuseBoardInTrash() error {
	return commands.Refuse("id", "is a variable of a board in the project's trash, which comes back with its board",
		nil)
}

// liveBoard returns the UUID of the board whose ID is id, a board's ID that a
// payload's Check checked, as the column of a variable of the board, or a
// null column if id is "", for a global variable, once it has checked that
// the board is one of the project's that is not in its trash. It refuses the
// batch otherwise, at board_id.
func liveBoard(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, id string) (pgtype.UUID, error) {
	if id == "" {
		return pgtype.UUID{}, nil
	}
	row, err := q.Board(ctx, queries.BoardParams{WorkspaceID: ws, ProjectID: project, ID: decoded(ids.Board, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return pgtype.UUID{}, commands.Refuse("board_id", "is not a board of the project, or is one in its trash",
			nil)
	case err != nil:
		return pgtype.UUID{}, fmt.Errorf("variable: read the board: %w", err)
	}
	return row.LiveBoard.ID, nil
}

// checkKey refuses the batch, for the payload's field at, unless key is a
// key that the variable self of the project, or a new variable if self is
// null, may have in the scope of the board boardID, or among the globals if
// it is null: an identifier (package ident), which the API answers with
// key_invalid, as it answers a slug that is not one with slug_invalid, and
// which no other variable of the scope has (keyFree).
func checkKey(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, at, key string, boardID,
	self pgtype.UUID) error {
	var invalid *ident.Error
	if errors.As(ident.Check(key), &invalid) {
		return commands.Refuse(at, invalid.Problem(), invalid)
	}
	return keyFree(ctx, q, ws, project, at, key, boardID, self, "is the key of another variable of the scope, "+
		"which is not in the project's trash")
}

// keyFree refuses the batch, for the payload's field at, with the message
// msg and ErrKeyTaken, if a variable of the project other than self, which
// is not deleted, has the key key in the scope of the board boardID, or
// among the globals if it is null.
func keyFree(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, at, key string, boardID,
	self pgtype.UUID, msg string) error {
	owner, err := q.VariableWithKey(ctx, queries.VariableWithKeyParams{BoardID: boardID, WorkspaceID: ws,
		ProjectID: project, Key: key})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("variable: read the variable with the key: %w", err)
	case owner != self:
		return commands.Refuse(at, msg, ErrKeyTaken)
	}
	return nil
}

// checkScope returns the problems of a scope and its board's ID: a board's
// variable names its board, and a global names none.
func checkScope(s, boardID string) input.FieldErrors {
	switch {
	case s == ScopeBoard && boardID == "":
		return field("board_id", "is missing, which a board's variable needs")
	case s == ScopeBoard:
		return entityID(ids.Board, "board_id", boardID)
	case boardID != "":
		return field("board_id", "is set for a global variable, which belongs to no board")
	}
	return nil
}

// checkReset returns the problem of a variable of the scope s whose
// reset_on_enter is reset, unless reset is false or s is a board's, at the
// payload's field at, which is the field that the command sets: at
// reset_on_enter for a create's and SetResetOnEnter's, and at scope for
// SetScope's, which sets the scope of a variable whose reset_on_enter is
// reset. Only a board's variable may have reset_on_enter set, since a global
// has no board to enter.
func checkReset(at, s string, reset bool) input.FieldErrors {
	switch {
	case !reset || s == ScopeBoard:
		return nil
	case at == "scope":
		return field(at, "is global, which a variable whose reset_on_enter is set may not be, because a global "+
			"has no board to enter: unset reset_on_enter first")
	}
	return field(at, "is set on a global variable, which has no board to enter, and which only a board's "+
		"variable may be")
}

// refuse refuses the batch for the first of errs, the problems of a payload
// that Apply finds with what the project holds, or returns nil if there are
// none.
func refuse(errs input.FieldErrors) error {
	if len(errs) == 0 {
		return nil
	}
	return commands.Refuse(errs[0].Field, errs[0].Message, nil)
}

// checkDefault returns the problem of def as a default of the type typ, which
// none, for the type's zero value, has not.
func checkDefault(typ string, def Value) input.FieldErrors {
	_, problem := of(def, typ, value.All)
	return field("default", problem)
}

// checkDescription says what is wrong with a variable's description, or
// returns "".
func checkDescription(description string) string {
	return input.CheckTrimmedText(description, MaxDescriptionLength)
}

// changed returns a function that reports the change of the variable id, to
// the revision that an update returned, with err, or reports nothing if the
// update found nothing to change, which it returns as pgx.ErrNoRows.
func changed(tx *commands.Tx, id pgtype.UUID) func(rev int64, err error) error {
	return func(rev int64, err error) error {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("variable: change the variable: %w", err)
		}
		return tx.Changed(ids.Variable, id.Bytes, rev)
	}
}

// by returns the batch's actor, as the column of who changed a row.
func by(tx *commands.Tx) pgtype.UUID {
	return nullUUID(tx.Actor().UserID)
}

// decoded returns the UUID of id, an ID of the kind that a payload's Check
// checked, as a uuid column.
func decoded(kind ids.Kind, id string) pgtype.UUID {
	// Check refused a payload whose ID does not decode.
	u, _ := ids.Decode(kind, id)
	return nullUUID(u)
}

// newID returns the problem of id, the ID of an entity of the kind that a
// command creates, unless it is an ID of the kind of a UUIDv7 (PRD §5.2).
func newID(kind ids.Kind, id string) input.FieldErrors {
	u, err := ids.Decode(kind, id)
	switch {
	case err != nil:
		return field("id", fmt.Sprintf("is not a %s's ID: %v", kind, err))
	case !u.IsV7():
		return field("id", "is not the ID of a UUIDv7, which the ID of every entity is")
	}
	return nil
}

// entityID returns the problem of the payload's field name, unless its value
// id is an ID of the kind.
func entityID(kind ids.Kind, name, id string) input.FieldErrors {
	if _, err := ids.Decode(kind, id); err != nil {
		return field(name, fmt.Sprintf("is not a %s's ID: %v", kind, err))
	}
	return nil
}

// optionalID returns the problem of the payload's field name as entityID
// does, unless its value id is "", for none.
func optionalID(kind ids.Kind, name, id string) input.FieldErrors {
	if id == "" {
		return nil
	}
	return entityID(kind, name, id)
}

// field returns the problem msg of the payload's field name, or none if msg
// is "".
func field(name, msg string) input.FieldErrors {
	if msg == "" {
		return nil
	}
	return input.FieldErrors{{Field: name, Message: msg}}
}

// uuids returns entities as uuid columns.
func uuids(entities []ids.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(entities))
	for i, id := range entities {
		out[i] = nullUUID(id)
	}
	return out
}
