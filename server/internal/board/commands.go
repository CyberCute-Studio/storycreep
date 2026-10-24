package board

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/orderkey"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/slug"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

// The types of command of a board (PRD §5.7). Each names the board that it is
// about by its ID, id, and applies to the board of the batch's project.
const (
	// Create creates a board, whose ID the client makes, as it makes the
	// command's, so that it can refer to the board before the server
	// answers. Deleting it to the trash undoes it.
	Create commands.Type = "board.create"
	// Rename renames a board, and keeps its slug (FR-PROJ-02 AC1);
	// Describe changes its description, SetColor its colour, and SetSlug its
	// slug. Changing each back undoes it.
	Rename   commands.Type = "board.rename"
	Describe commands.Type = "board.describe"
	SetColor commands.Type = "board.set_color"
	SetSlug  commands.Type = "board.set_slug"
	// Move moves a board into a folder of boards, or to the top, at a place
	// among the folder's children. Moving it back undoes it.
	Move commands.Type = "board.move"
	// Delete deletes a board to the project's trash, with its nodes, once
	// it is confirmed if jumpers on other boards target its nodes, which it
	// leaves dangling (FR-PROJ-02 AC2), and Restore restores it, with the
	// folders above it that were deleted since, and its nodes, with their
	// connections, but for those whose node at the other end is deleted on
	// its own, which the batch's answer lists (package connection). Each
	// undoes the other.
	Delete  commands.Type = "board.delete"
	Restore commands.Type = "board.restore"
)

// createPayload is Create's payload.
type createPayload struct {
	ID          string `json:"id" doc:"The new board's ID, a bd_ ID of a UUIDv7 that the client makes, which no entity of the instance has (PRD §5.2)"`
	Name        string `json:"name" maxLength:"100" doc:"The board's name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them"`
	Slug        string `json:"slug,omitempty" doc:"The board's slug, which names its file in the game project: groups of lowercase ASCII letters and digits joined by single hyphens, as harbour-intro, of at most 64 characters, that no other board of the project has, in its trash or not, and that is not a name that Windows reserves for a device, such as con or com1. A slug that is not one is answered with 422 slug_invalid, and one that another board has with 409 slug_taken. Without it, the server derives one from the name: its letters and digits in lowercase ASCII, without their accents, each run of other characters a hyphen, or board if that leaves nothing, with -board after a name that Windows reserves for a device, as con-board, and then with -2, -3, and so on, the first that no board of the project has (PRD FR-PROJ-02 AC1, AC4)."`
	Description string `json:"description,omitempty" maxLength:"2000" doc:"The board's description, of at most 2000 characters, without white space around it, and without control characters but line feeds and tabs. Without it, the board has none."`
	Color       string `json:"color,omitempty" enum:"none,red,orange,yellow,green,teal,blue,purple,pink,gray" doc:"The board's colour, none or a name of the palette. Without it, none."`
	FolderID    string `json:"folder_id,omitempty" doc:"The ID of the folder of boards that the board goes into, which is not deleted. Without it, the board goes at the top."`
	Order       string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The board's place among the children of its folder, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and those with the same key by their IDs without the prefix, bd_ or fld_, compared by their bytes too: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the board goes after the last child."`
}

// renamePayload is Rename's payload.
type renamePayload struct {
	ID   string `json:"id" doc:"The board's ID"`
	Name string `json:"name" maxLength:"100" doc:"The board's new name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them. The board keeps its slug (PRD FR-PROJ-02 AC1)."`
}

// describePayload is Describe's payload.
type describePayload struct {
	ID          string `json:"id" doc:"The board's ID"`
	Description string `json:"description" maxLength:"2000" doc:"The board's new description, of at most 2000 characters, which may be empty, without white space around it, and without control characters but line feeds and tabs"`
}

// colorPayload is SetColor's payload.
type colorPayload struct {
	ID    string `json:"id" doc:"The board's ID"`
	Color string `json:"color" enum:"none,red,orange,yellow,green,teal,blue,purple,pink,gray" doc:"The board's new colour, none or a name of the palette"`
}

// slugPayload is SetSlug's payload.
type slugPayload struct {
	ID   string `json:"id" doc:"The board's ID"`
	Slug string `json:"slug" doc:"The board's new slug, which renames its file in the game project: groups of lowercase ASCII letters and digits joined by single hyphens, as harbour-intro, of at most 64 characters, that no other board of the project has, in its trash or not, and that is not a name that Windows reserves for a device, such as con or com1. A slug that is not one is answered with 422 slug_invalid, and one that another board has with 409 slug_taken."`
}

// movePayload is Move's payload.
type movePayload struct {
	ID       string `json:"id" doc:"The board's ID"`
	FolderID string `json:"folder_id,omitempty" doc:"The ID of the folder of boards that the board goes into, which is not deleted. Without it, the board goes to the top."`
	Order    string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The board's place among the children of the folder, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and those with the same key by their IDs without the prefix, bd_ or fld_, compared by their bytes too: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the board goes after the last child, or stays where it is if it is the last child of the folder already that is not deleted."`
}

// idPayload is the payload of a command that needs nothing but its board's
// ID.
type idPayload struct {
	ID string `json:"id" doc:"The board's ID"`
}

// deletePayload is Delete's payload.
type deletePayload struct {
	ID      string `json:"id" doc:"The board's ID"`
	Confirm bool   `json:"confirm,omitempty" doc:"Whether to delete the board even though jumpers on the project's other boards, which are not in its trash, target its nodes that are not deleted themselves, which its deletion leaves dangling, never dropped, for the validation to report (PRD FR-PROJ-02 AC2). Without it, a board that such jumpers target is not deleted: the command is answered with 409 board_referenced, which lists them, as GET /api/v1/projects/{project_id}/boards/{board_id}/referenced-by does, for a client to warn of before it sends the command again with confirm."`
}

// MaxReferrers is the most jumpers that a refusal of a board's deletion
// lists (ReferencedError).
const MaxReferrers = 100

// Referrer is a jumper that targets a node of a board that its deletion
// would leave dangling, as a refusal of the deletion lists it.
type Referrer struct {
	ID      string `json:"id"`
	BoardID string `json:"board_id"`
	Title   string `json:"title"`
}

// ReferencedError is the reason of the refusal of a board's deletion that is
// not confirmed while jumpers on the project's other boards, which are not
// in its trash, target its nodes that are not deleted themselves (FR-PROJ-02
// AC2), which the API answers with board_referenced: Count of them, the first
// MaxReferrers of which, in the order of their IDs, are Jumpers.
type ReferencedError struct {
	Jumpers []Referrer
	Count   int64
}

func (e *ReferencedError) Error() string {
	return fmt.Sprintf("board: %d jumpers on other boards target the board's nodes", e.Count)
}

// registerBoards registers in r the board's commands, each of which changes
// the project's draft, so that an API token needs project:write for it
// (FR-AUTH-04), and its role "Create/rename/archive boards" (§5.6).
func registerBoards(r *commands.Registry) {
	board := []ids.Kind{ids.Board}
	// A board's folder is an entity that the board refers to.
	inFolder := []ids.Kind{ids.Board, ids.Folder}

	commands.Register(r, commands.Spec[createPayload]{
		Type: Create, Capability: authz.ManageBoards, Scope: scope.ProjectWrite, Touches: inFolder,
		Check: func(p createPayload) input.FieldErrors {
			errs := newID(ids.Board, p.ID)
			errs = append(errs, field("name", checkName(p.Name))...)
			errs = append(errs, field("description", checkDescription(p.Description))...)
			return append(errs, optionalID(ids.Folder, "folder_id", p.FolderID)...)
		},
		Inverse: func(_ context.Context, _ *commands.Tx, p createPayload) ([]commands.Step, error) {
			return []commands.Step{{Type: Delete, Payload: deletePayload{ID: p.ID}}}, nil
		},
		Apply: applyCreate,
	})

	commands.Register(r, commands.Spec[renamePayload]{
		Type: Rename, Capability: authz.ManageBoards, Scope: scope.ProjectWrite, Touches: board,
		Check: func(p renamePayload) input.FieldErrors { return entityID(ids.Board, "id", p.ID) },
		// The step of the inverse writes back the stored name, which the text
		// rules may refuse (commands.Spec.CheckNew).
		CheckNew: func(p renamePayload) input.FieldErrors { return field("name", checkName(p.Name)) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p renamePayload) ([]commands.Step, error) {
			b, err := liveBoard(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Rename, Payload: renamePayload{ID: p.ID, Name: b.Name}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p renamePayload) error {
			b, err := liveBoard(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			return changed(tx, ids.Board, b.ID)(queries.New(tx).UpdateBoardName(ctx, queries.UpdateBoardNameParams{
				Name: p.Name, UpdatedBy: by(tx), WorkspaceID: b.WorkspaceID, ProjectID: b.ProjectID, ID: b.ID}))
		},
	})

	commands.Register(r, commands.Spec[describePayload]{
		Type: Describe, Capability: authz.ManageBoards, Scope: scope.ProjectWrite, Touches: board,
		Check: func(p describePayload) input.FieldErrors { return entityID(ids.Board, "id", p.ID) },
		CheckNew: func(p describePayload) input.FieldErrors {
			return field("description", checkDescription(p.Description))
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p describePayload) ([]commands.Step, error) {
			b, err := liveBoard(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Describe, Payload: describePayload{ID: p.ID, Description: b.Description}}},
				nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p describePayload) error {
			b, err := liveBoard(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			return changed(tx, ids.Board, b.ID)(queries.New(tx).UpdateBoardDescription(ctx,
				queries.UpdateBoardDescriptionParams{Description: p.Description, UpdatedBy: by(tx),
					WorkspaceID: b.WorkspaceID, ProjectID: b.ProjectID, ID: b.ID}))
		},
	})

	commands.Register(r, commands.Spec[colorPayload]{
		Type: SetColor, Capability: authz.ManageBoards, Scope: scope.ProjectWrite, Touches: board,
		Check: func(p colorPayload) input.FieldErrors { return entityID(ids.Board, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p colorPayload) ([]commands.Step, error) {
			b, err := liveBoard(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetColor, Payload: colorPayload{ID: p.ID, Color: b.Color}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p colorPayload) error {
			b, err := liveBoard(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			return changed(tx, ids.Board, b.ID)(queries.New(tx).UpdateBoardColor(ctx, queries.UpdateBoardColorParams{
				Color: p.Color, UpdatedBy: by(tx), WorkspaceID: b.WorkspaceID, ProjectID: b.ProjectID, ID: b.ID}))
		},
	})

	commands.Register(r, commands.Spec[slugPayload]{
		Type: SetSlug, Capability: authz.ManageBoards, Scope: scope.ProjectWrite, Touches: board,
		Check: func(p slugPayload) input.FieldErrors { return entityID(ids.Board, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p slugPayload) ([]commands.Step, error) {
			b, err := liveBoard(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetSlug, Payload: slugPayload{ID: p.ID, Slug: b.Slug}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p slugPayload) error {
			b, err := liveBoard(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			q := queries.New(tx)
			if err := checkSlug(ctx, q, b.WorkspaceID, b.ProjectID, p.Slug, b.ID.Bytes); err != nil {
				return err
			}
			return changed(tx, ids.Board, b.ID)(q.UpdateBoardSlug(ctx, queries.UpdateBoardSlugParams{Slug: p.Slug,
				UpdatedBy: by(tx), WorkspaceID: b.WorkspaceID, ProjectID: b.ProjectID, ID: b.ID}))
		},
	})

	commands.Register(r, commands.Spec[movePayload]{
		Type: Move, Capability: authz.ManageBoards, Scope: scope.ProjectWrite, Touches: inFolder,
		Check: func(p movePayload) input.FieldErrors {
			return append(entityID(ids.Board, "id", p.ID), optionalID(ids.Folder, "folder_id", p.FolderID)...)
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p movePayload) ([]commands.Step, error) {
			b, err := liveBoard(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Move, Payload: movePayload{ID: p.ID, FolderID: encode(ids.Folder, b.FolderID),
				Order: b.Order}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p movePayload) error {
			b, err := liveBoard(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			q := queries.New(tx)
			folder, err := LiveFolder(ctx, q, b.WorkspaceID, b.ProjectID, KindBoard, "folder_id", p.FolderID)
			if err != nil {
				return err
			}
			order, err := SortKey(ctx, q, b.WorkspaceID, b.ProjectID, KindBoard, folder, p.Order,
				&Child{ID: b.ID, Parent: b.FolderID, Key: b.Order, Kind: ids.Board})
			if err != nil {
				return err
			}
			return changed(tx, ids.Board, b.ID)(q.MoveBoard(ctx, queries.MoveBoardParams{FolderID: folder,
				SortKey: order, UpdatedBy: by(tx), WorkspaceID: b.WorkspaceID, ProjectID: b.ProjectID, ID: b.ID}))
		},
	})

	commands.Register(r, commands.Spec[deletePayload]{
		Type: Delete, Capability: authz.ManageBoards, Scope: scope.ProjectWrite, Touches: board,
		Check: func(p deletePayload) input.FieldErrors { return entityID(ids.Board, "id", p.ID) },
		// Deleting a board that is in the trash already does nothing, and
		// its undo nothing either.
		Inverse: func(ctx context.Context, tx *commands.Tx, p deletePayload) ([]commands.Step, error) {
			b, err := anyBoard(ctx, tx, p.ID)
			if err != nil || b.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Restore, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: applyDelete,
	})

	commands.Register(r, commands.Spec[idPayload]{
		Type: Restore, Capability: authz.ManageBoards, Scope: scope.ProjectWrite, Touches: inFolder,
		Check: func(p idPayload) input.FieldErrors { return entityID(ids.Board, "id", p.ID) },
		// Restoring a board that is not in the trash does nothing, and its
		// undo nothing either. The undo of a restore deletes the board, and
		// then the folders that the restore restored above it, from the
		// nearest, and above its variables, from those nearest the bottom of
		// their tree, each of which then holds nothing that is not deleted,
		// once the board's variables are in the trash with it again.
		Inverse: func(ctx context.Context, tx *commands.Tx, p idPayload) ([]commands.Step, error) {
			b, err := anyBoard(ctx, tx, p.ID)
			if err != nil || !b.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			q := queries.New(tx)
			deleted, err := DeletedFolders(ctx, q, b.WorkspaceID, b.ProjectID, b.FolderID)
			if err != nil {
				return nil, err
			}
			held, err := variableFolders(ctx, q, b)
			if err != nil {
				return nil, err
			}
			above, err := deletedFoldersAbove(ctx, q, b.WorkspaceID, b.ProjectID, held)
			if err != nil {
				return nil, err
			}
			// The undo returns the board to the trash that its deletion, which
			// was confirmed if it had to be, put it in, so it confirms the
			// deletion again.
			steps := []commands.Step{{Type: Delete, Payload: deletePayload{ID: p.ID, Confirm: true}}}
			return append(append(steps, DeleteFolderSteps(deleted)...), DeleteFolderSteps(above)...), nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p idPayload) error {
			b, err := anyBoard(ctx, tx, p.ID)
			if err != nil || !b.DeletedAt.Valid {
				return err
			}
			q := queries.New(tx)
			if err := RestoreFolders(ctx, tx, q, b.WorkspaceID, b.ProjectID, b.FolderID); err != nil {
				return err
			}
			// The board's variables come back with it, into their folders,
			// which come back with them if they are deleted.
			held, err := variableFolders(ctx, q, b)
			if err != nil {
				return err
			}
			for _, f := range held {
				if err := RestoreFolders(ctx, tx, q, b.WorkspaceID, b.ProjectID, f); err != nil {
					return err
				}
			}
			// The board is in the trash, as read above under the project's
			// lock, so the restore finds it there; a restore that found it
			// out of the trash all the same would add nothing to the boards,
			// as a deletion of a board in the trash takes nothing from them.
			rev, err := q.RestoreBoard(ctx, queries.RestoreBoardParams{UpdatedBy: by(tx), WorkspaceID: b.WorkspaceID,
				ProjectID: b.ProjectID, ID: b.ID})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err := changed(tx, ids.Board, b.ID)(rev, err); err != nil {
				return err
			}
			if err := tx.AddUsage(usage.Boards, 1); err != nil {
				return err
			}
			// The connections of the nodes that come back with it come back
			// too where their nodes at the other end are not in the trash,
			// deleted on their own, and the batch's answer lists the others
			// (package connection, FR-PROJ-06 AC1).
			if err := tx.Restored(ids.Board, b.ID.Bytes); err != nil {
				return err
			}
			// The board's nodes that are not deleted themselves come back with
			// it, and count again.
			nodes, err := q.BoardNodeCount(ctx, queries.BoardNodeCountParams{WorkspaceID: b.WorkspaceID,
				ProjectID: b.ProjectID, BoardID: b.ID})
			if err != nil {
				return fmt.Errorf("board: count the board's nodes: %w", err)
			}
			return tx.AddUsage(usage.Nodes, nodes)
		},
	})
}

// applyDelete is Delete's Apply.
func applyDelete(ctx context.Context, tx *commands.Tx, p deletePayload) error {
	b, err := anyBoard(ctx, tx, p.ID)
	if err != nil || b.DeletedAt.Valid {
		return err
	}
	q := queries.New(tx)
	// The jumpers on the project's other boards that target the board's
	// nodes stay as they are, dangling, which a client warns of before it
	// confirms the deletion (FR-PROJ-02 AC2). One whose target is deleted
	// already dangles already, and is not warned of.
	if !p.Confirm {
		rows, err := q.BoardReferrers(ctx, queries.BoardReferrersParams{WorkspaceID: b.WorkspaceID,
			ProjectID: b.ProjectID, BoardID: b.ID, MaxRows: MaxReferrers})
		if err != nil {
			return fmt.Errorf("board: list the jumpers that target the board: %w", err)
		}
		// The list holds them all unless it is full, and they are counted
		// then.
		count := int64(len(rows))
		if count == MaxReferrers {
			if count, err = q.BoardReferrerCount(ctx, queries.BoardReferrerCountParams{WorkspaceID: b.WorkspaceID,
				ProjectID: b.ProjectID, BoardID: b.ID}); err != nil {
				return fmt.Errorf("board: count the jumpers that target the board: %w", err)
			}
		}
		if count > 0 {
			referenced := &ReferencedError{Jumpers: make([]Referrer, len(rows)), Count: count}
			for i, row := range rows {
				referenced.Jumpers[i] = Referrer{ID: ids.Encode(ids.Node, row.LiveNode.ID.Bytes),
					BoardID: ids.Encode(ids.Board, row.LiveNode.BoardID.Bytes), Title: row.LiveNode.Title}
			}
			return commands.Refuse("confirm", fmt.Sprintf("is not set, but %d jumpers on the project's other boards "+
				"target the board's nodes, which its deletion leaves dangling: confirm the deletion to delete it all "+
				"the same", count), referenced)
		}
	}
	// The board's nodes that are not deleted themselves are in the trash with
	// it, and count no longer.
	nodes, err := q.BoardNodeCount(ctx, queries.BoardNodeCountParams{WorkspaceID: b.WorkspaceID,
		ProjectID: b.ProjectID, BoardID: b.ID})
	if err != nil {
		return fmt.Errorf("board: count the board's nodes: %w", err)
	}
	rev, err := q.TrashBoard(ctx, queries.TrashBoardParams{UpdatedBy: by(tx), WorkspaceID: b.WorkspaceID,
		ProjectID: b.ProjectID, ID: b.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err := changed(tx, ids.Board, b.ID)(rev, err); err != nil {
		return err
	}
	if err := tx.AddUsage(usage.Boards, -1); err != nil {
		return err
	}
	return tx.AddUsage(usage.Nodes, -nodes)
}

// applyCreate is Create's Apply.
func applyCreate(ctx context.Context, tx *commands.Tx, p createPayload) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	id := decoded(ids.Board, p.ID)
	// A board of the project with the ID is refused for its ID before
	// anything else is checked, so that a create sent again with a new
	// command ID, as an offline client's keep-mine sends it (FR-OFF-04), is
	// refused for its ID rather than for the slug that its board has taken;
	// the insert below refuses the ID of a board that this transaction does
	// not see.
	if _, err := q.BoardAny(ctx, queries.BoardAnyParams{WorkspaceID: ws, ProjectID: project, ID: id}); err == nil {
		return commands.Refuse("id", "is the ID of a board that exists already", nil)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("board: read the board: %w", err)
	}
	folder, err := LiveFolder(ctx, q, ws, project, KindBoard, "folder_id", p.FolderID)
	if err != nil {
		return err
	}
	order, err := SortKey(ctx, q, ws, project, KindBoard, folder, p.Order, nil)
	if err != nil {
		return err
	}
	s := p.Slug
	if s == "" {
		if s, err = deriveSlug(ctx, q, ws, project, p.Name); err != nil {
			return err
		}
	} else if err := checkSlug(ctx, q, ws, project, s, ids.UUID{}); err != nil {
		return err
	}
	color := p.Color
	if color == "" {
		color = ColorNone
	}
	err = q.InsertBoard(ctx, queries.InsertBoardParams{ID: id, WorkspaceID: ws, ProjectID: project, FolderID: folder,
		SortKey: order, Name: p.Name, Slug: s, Description: p.Description, Color: color, CreatedBy: by(tx)})
	if database.UniqueViolation(err, "board_pkey") {
		// The ID is another workspace's, or another project's, whose rows
		// this transaction does not see.
		return commands.Refuse("id", "is the ID of a board that exists already", nil)
	}
	if err != nil {
		return fmt.Errorf("board: add the board: %w", err)
	}
	if err := tx.Changed(ids.Board, id.Bytes, 1); err != nil {
		return err
	}
	return tx.AddUsage(usage.Boards, 1)
}

// anyBoard returns the board of the batch's project whose ID is id, a board's
// ID that its payload's Check checked, whether or not it is in the trash, or
// refuses the batch if the project has no such board.
func anyBoard(ctx context.Context, tx *commands.Tx, id string) (queries.Board, error) {
	row, err := queries.New(tx).BoardAny(ctx, queries.BoardAnyParams{WorkspaceID: nullUUID(tx.WorkspaceID()),
		ProjectID: nullUUID(tx.ProjectID()), ID: decoded(ids.Board, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.Board{}, commands.Refuse("id", "is not a board of the project", nil)
	case err != nil:
		return queries.Board{}, fmt.Errorf("board: read the board: %w", err)
	}
	return row.Board, nil
}

// liveBoard returns the board of the batch's project whose ID is id as
// anyBoard does, or refuses the batch if it is in the trash, where nothing
// but restoring it changes it.
func liveBoard(ctx context.Context, tx *commands.Tx, id string) (queries.Board, error) {
	b, err := anyBoard(ctx, tx, id)
	if err == nil && b.DeletedAt.Valid {
		return queries.Board{}, commands.Refuse("id", "is a board in the project's trash, which only restoring it "+
			"changes", nil)
	}
	return b, err
}

// LiveFolder returns the UUID of the folder whose ID is id, a folder's ID that
// a payload's Check checked, as the column of a child that goes into it, or
// a null column if id is "", for a child at the top, once it has checked that
// the folder is one of the project's of the kind, which is not deleted. It
// refuses the batch otherwise, for the payload's field.
func LiveFolder(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, kind, field, id string) (
	pgtype.UUID, error) {
	if id == "" {
		return pgtype.UUID{}, nil
	}
	row, err := q.FolderAny(ctx, queries.FolderAnyParams{WorkspaceID: ws, ProjectID: project,
		ID: decoded(ids.Folder, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return pgtype.UUID{}, commands.Refuse(field, "is not a folder of the project", nil)
	case err != nil:
		return pgtype.UUID{}, fmt.Errorf("board: read the folder: %w", err)
	case row.Folder.Kind != kind:
		return pgtype.UUID{}, commands.Refuse(field, fmt.Sprintf("is a folder of %ss, not of %ss", row.Folder.Kind,
			kind), nil)
	case row.Folder.DeletedAt.Valid:
		return pgtype.UUID{}, commands.Refuse(field, "is a deleted folder", nil)
	}
	return row.Folder.ID, nil
}

// Child is a child of a folder, or of the top, that moves: its ID, its
// parent, null for the top, its key, and the kind of entity that it is: a
// folder, or what a folder of its kind holds, a board, a variable, or a
// component.
type Child struct {
	ID, Parent pgtype.UUID
	Key        string
	Kind       ids.Kind
}

// SortKey returns the key that a child of the kind that goes into the folder
// parent, or to the top if it is null, is placed at: the key given, if it is
// not "", or else one after the parent's last child's (orderkey.After), the
// deleted ones included, so that one restored later keeps its place, or, if
// that key has no key after it, one after the parent's last child's that is
// not deleted, so that a key that a deleted child used up does not keep
// every child from being added after the others, or it refuses the batch if
// that key has none after it either. moving is the child that moves there,
// or nil for a new one: it is none of the children that it goes after, and
// it keeps its key if it is in parent already, after every other child that
// is not deleted, by its key, or by its ID after another child's with the
// same key, as the children are ordered, so that moving the last child to
// the end changes nothing, as it shows nothing, even while a deleted child
// comes after it, which, restored, then comes after it again, where it was.
func SortKey(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, kind string, parent pgtype.UUID,
	given string, moving *Child) (string, error) {
	if given != "" {
		return given, nil
	}
	params := queries.LastSortKeysParams{WorkspaceID: ws, ProjectID: project, Kind: kind, ParentID: parent}
	if moving != nil {
		switch moving.Kind {
		case ids.Folder:
			params.MovedFolder = moving.ID
		case ids.Board:
			params.MovedBoard = moving.ID
		case ids.Variable:
			params.MovedVariable = moving.ID
		case ids.Component:
			params.MovedComponent = moving.ID
		default:
			return "", fmt.Errorf("board: a %s is no child of a folder", moving.Kind)
		}
	}
	last, err := q.LastSortKeys(ctx, params)
	if err != nil {
		return "", fmt.Errorf("board: read the folder's last keys: %w", err)
	}
	var stays *Child
	if moving != nil && sameUUID(moving.Parent, parent) {
		stays = moving
	}
	return Place(Last{Key: last.Last, Live: last.LastLive, LiveID: last.LastLiveID}, stays,
		"the folder's last child")
}

// Last are the keys of the last of the children of a parent, the deleted
// ones included, and of the last of those that are not deleted, with its ID,
// in the order of the children, or "" and a null ID where there are none.
type Last struct {
	Key, Live string
	LiveID    pgtype.UUID
}

// Place returns the key that a child is placed at without a key given, after
// the children whose last keys are last, as SortKey does: one after the last
// child's key, the deleted ones included, or, if that has no key after it,
// one after the last child's that is not deleted, or it refuses the batch, at
// order, if that has none after it either, naming the child as what. stays is
// the child that moves among the children, if it is one of them already, or
// nil: it keeps its key if it comes after every other child that is not
// deleted.
func Place(last Last, stays *Child, what string) (string, error) {
	if stays != nil && (stays.Key > last.Live ||
		stays.Key == last.Live && bytes.Compare(stays.ID.Bytes[:], last.LiveID.Bytes[:]) > 0) {
		return stays.Key, nil
	}
	for _, k := range []string{last.Key, last.Live} {
		if after, ok := orderkey.After(k); ok {
			return after, nil
		}
	}
	return "", commands.Refuse("order", fmt.Sprintf("is needed, because the key of %s that is not deleted has no "+
		"key after it of at most %d digits", what, orderkey.MaxLength), nil)
}

// sameUUID reports whether the columns a and b are the same: both null, or
// both the same UUID.
func sameUUID(a, b pgtype.UUID) bool {
	return a.Valid == b.Valid && (!a.Valid || a.Bytes == b.Bytes)
}

// suffixDigits is the most digits that deriveSlug numbers a slug with, which
// more boards than an instance holds would need.
const suffixDigits = 10

// deriveSlug returns the slug of a new board named name (FR-PROJ-02 AC1,
// AC4): the name's slug, or board for a name without one, and con-board for
// con and the other names that Windows reserves for devices (slug.Derive),
// unless a board of the project has it, in the trash or not, and otherwise
// the first of that with -2, -3, and so on, that none has. It reads only the
// slugs that start as each of those does, which the index of their
// uniqueness finds (BoardSlugsFrom), rather than every board's, so that a
// batch that creates many boards of different names reads few slugs for
// each.
func deriveSlug(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, name string) (string, error) {
	base := slug.Derive(name, "board")
	// Each slug tried starts with base cut to leave room for the longest
	// number, without a hyphen at its end (slug.WithSuffix).
	stem := strings.TrimRight(base[:min(len(base), slug.MaxLength-1-suffixDigits)], "-")
	slugs, err := q.BoardSlugsFrom(ctx, queries.BoardSlugsFromParams{WorkspaceID: ws, ProjectID: project,
		Stem: stem})
	if err != nil {
		return "", fmt.Errorf("board: read the boards' slugs: %w", err)
	}
	used := make(map[string]bool, len(slugs))
	for _, s := range slugs {
		used[s] = true
	}
	// Fewer boards have the slugs read than the numbers tried, so one is
	// free.
	for n := 1; ; n++ {
		s := base
		if n > 1 {
			s = slug.WithSuffix(base, fmt.Sprintf("-%d", n))
		}
		if !used[s] {
			return s, nil
		}
	}
}

// checkSlug refuses the batch unless s is a slug that the board self of the
// project, or a new board if self is zero, may have: one of the form of
// package slug, which no other board of the project has, in its trash or
// not.
func checkSlug(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, s string, self ids.UUID) error {
	if err := refuseSlug("slug", s); err != nil {
		return err
	}
	owner, err := q.BoardWithSlug(ctx, queries.BoardWithSlugParams{WorkspaceID: ws, ProjectID: project, Slug: s})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("board: read the board with the slug: %w", err)
	case owner.Bytes != self:
		return commands.Refuse("slug", "is another board's slug", ErrSlugTaken)
	}
	return nil
}

// refuseSlug refuses the batch, for the payload's field, if s is not of the
// form of a slug, or is a name that Windows reserves for a device, with the
// *slug.Error that says which, and returns nil otherwise.
func refuseSlug(field, s string) error {
	var e *slug.Error
	if errors.As(slug.Check(s), &e) {
		return commands.Refuse(field, e.Problem(), e)
	}
	return nil
}

// changed returns a function that reports the change of the entity of the
// kind, to the revision that an update returned, with err, or reports nothing
// if the update found nothing to change, which it returns as pgx.ErrNoRows.
func changed(tx *commands.Tx, kind ids.Kind, id pgtype.UUID) func(rev int64, err error) error {
	return func(rev int64, err error) error {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("board: change the %s: %w", kind, err)
		}
		return tx.Changed(kind, id.Bytes, rev)
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
// id is an ID of the kind, as the id of the board or the folder that a
// command is about.
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

// checkName says what is wrong with a board's or a folder's name, or returns
// "".
func checkName(name string) string {
	return input.CheckTrimmedName(name, MaxNameLength)
}

// checkDescription says what is wrong with a board's description, or
// returns "".
func checkDescription(description string) string {
	return input.CheckTrimmedText(description, MaxDescriptionLength)
}

// field returns the problem msg of the payload's field name, or none if msg
// is "".
func field(name, msg string) input.FieldErrors {
	if msg == "" {
		return nil
	}
	return input.FieldErrors{{Field: name, Message: msg}}
}
