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
	// Delete deletes a board to the project's trash, and Restore restores it,
	// with the folders above it that were deleted since. Each undoes the
	// other.
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
	Order       string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The board's place among the children of its folder, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and then by their IDs: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the board goes after the last child."`
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
	Order    string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The board's place among the children of the folder, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and then by their IDs: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the board goes after the last child, or stays where it is if it is the last child of the folder already that is not deleted."`
}

// idPayload is the payload of a command that needs nothing but its board's
// ID.
type idPayload struct {
	ID string `json:"id" doc:"The board's ID"`
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
			return []commands.Step{{Type: Delete, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: applyCreate,
	})

	commands.Register(r, commands.Spec[renamePayload]{
		Type: Rename, Capability: authz.ManageBoards, Scope: scope.ProjectWrite, Touches: board,
		Check: func(p renamePayload) input.FieldErrors {
			return append(entityID(ids.Board, "id", p.ID), field("name", checkName(p.Name))...)
		},
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
		Check: func(p describePayload) input.FieldErrors {
			return append(entityID(ids.Board, "id", p.ID), field("description", checkDescription(p.Description))...)
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
			folder, err := liveFolder(ctx, q, b.WorkspaceID, b.ProjectID, KindBoard, "folder_id", p.FolderID)
			if err != nil {
				return err
			}
			order, err := sortKey(ctx, q, b.WorkspaceID, b.ProjectID, KindBoard, folder, p.Order,
				&child{id: b.ID, parent: b.FolderID, key: b.Order, board: true})
			if err != nil {
				return err
			}
			return changed(tx, ids.Board, b.ID)(q.MoveBoard(ctx, queries.MoveBoardParams{FolderID: folder,
				SortKey: order, UpdatedBy: by(tx), WorkspaceID: b.WorkspaceID, ProjectID: b.ProjectID, ID: b.ID}))
		},
	})

	commands.Register(r, commands.Spec[idPayload]{
		Type: Delete, Capability: authz.ManageBoards, Scope: scope.ProjectWrite, Touches: board,
		Check: func(p idPayload) input.FieldErrors { return entityID(ids.Board, "id", p.ID) },
		// Deleting a board that is in the trash already does nothing, and
		// its undo nothing either.
		Inverse: func(ctx context.Context, tx *commands.Tx, p idPayload) ([]commands.Step, error) {
			b, err := anyBoard(ctx, tx, p.ID)
			if err != nil || b.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Restore, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p idPayload) error {
			b, err := anyBoard(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			rev, err := queries.New(tx).TrashBoard(ctx, queries.TrashBoardParams{UpdatedBy: by(tx),
				WorkspaceID: b.WorkspaceID, ProjectID: b.ProjectID, ID: b.ID})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err := changed(tx, ids.Board, b.ID)(rev, err); err != nil {
				return err
			}
			return tx.AddUsage(usage.Boards, -1)
		},
	})

	commands.Register(r, commands.Spec[idPayload]{
		Type: Restore, Capability: authz.ManageBoards, Scope: scope.ProjectWrite, Touches: inFolder,
		Check: func(p idPayload) input.FieldErrors { return entityID(ids.Board, "id", p.ID) },
		// Restoring a board that is not in the trash does nothing, and its
		// undo nothing either. The undo of a restore deletes the board, and
		// then the folders that the restore restored above it, from the
		// nearest, each of which then holds nothing that is not deleted.
		Inverse: func(ctx context.Context, tx *commands.Tx, p idPayload) ([]commands.Step, error) {
			b, err := anyBoard(ctx, tx, p.ID)
			if err != nil || !b.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			steps := []commands.Step{{Type: Delete, Payload: idPayload{ID: p.ID}}}
			deleted, err := deletedAncestors(ctx, queries.New(tx), b.WorkspaceID, b.ProjectID, b.FolderID)
			if err != nil {
				return nil, err
			}
			for _, f := range deleted {
				steps = append(steps, commands.Step{Type: DeleteFolder, Payload: deleteFolderPayload{
					ID: ids.Encode(ids.Folder, f.Bytes)}})
			}
			return steps, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p idPayload) error {
			b, err := anyBoard(ctx, tx, p.ID)
			if err != nil || !b.DeletedAt.Valid {
				return err
			}
			q := queries.New(tx)
			if err := restoreAncestors(ctx, tx, q, b.WorkspaceID, b.ProjectID, b.FolderID); err != nil {
				return err
			}
			if err := changed(tx, ids.Board, b.ID)(q.RestoreBoard(ctx, queries.RestoreBoardParams{UpdatedBy: by(tx),
				WorkspaceID: b.WorkspaceID, ProjectID: b.ProjectID, ID: b.ID})); err != nil {
				return err
			}
			return tx.AddUsage(usage.Boards, 1)
		},
	})
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
	folder, err := liveFolder(ctx, q, ws, project, KindBoard, "folder_id", p.FolderID)
	if err != nil {
		return err
	}
	order, err := sortKey(ctx, q, ws, project, KindBoard, folder, p.Order, nil)
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

// liveFolder returns the UUID of the folder whose ID is id, a folder's ID that
// a payload's Check checked, as the column of a child that goes into it, or
// a null column if id is "", for a child at the top, once it has checked that
// the folder is one of the project's of the kind, which is not deleted. It
// refuses the batch otherwise, for the payload's field.
func liveFolder(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, kind, field, id string) (
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

// child is a child of a folder, or of the top, that moves: its ID, its
// parent, null for the top, its key, and whether it is a board, or else a
// folder.
type child struct {
	id, parent pgtype.UUID
	key        string
	board      bool
}

// sortKey returns the key that a child of the kind that goes into the folder
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
func sortKey(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, kind string, parent pgtype.UUID,
	given string, moving *child) (string, error) {
	if given != "" {
		return given, nil
	}
	params := queries.LastSortKeysParams{WorkspaceID: ws, ProjectID: project, Kind: kind, ParentID: parent}
	switch {
	case moving == nil:
	case moving.board:
		params.MovedBoard = moving.id
	default:
		params.MovedFolder = moving.id
	}
	last, err := q.LastSortKeys(ctx, params)
	if err != nil {
		return "", fmt.Errorf("board: read the folder's last keys: %w", err)
	}
	if moving != nil && sameUUID(moving.parent, parent) && (moving.key > last.LastLive ||
		moving.key == last.LastLive && bytes.Compare(moving.id.Bytes[:], last.LastLiveID.Bytes[:]) > 0) {
		return moving.key, nil
	}
	for _, k := range []string{last.Last, last.LastLive} {
		if after, ok := orderkey.After(k); ok {
			return after, nil
		}
	}
	return "", commands.Refuse("order", fmt.Sprintf("is needed, because the key of the folder's last child that is "+
		"not deleted has no key after it of at most %d digits", orderkey.MaxLength), nil)
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
