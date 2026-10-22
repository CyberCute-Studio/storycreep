package board

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// The types of command of a folder (PRD §5.7). Each names the folder that it
// is about by its ID, id.
const (
	// CreateFolder creates a folder of boards, variables, or components,
	// whose ID the client makes. Deleting it undoes it.
	CreateFolder commands.Type = "folder.create"
	// RenameFolder renames a folder. Renaming it back undoes it.
	RenameFolder commands.Type = "folder.rename"
	// MoveFolder moves a folder into another of its kind, or to the top, at
	// a place among the other folder's children. Moving it back undoes it.
	MoveFolder commands.Type = "folder.move"
	// DeleteFolder deletes a folder that holds nothing but what is deleted,
	// and RestoreFolder restores it, with the folders above it that were
	// deleted since. Each undoes the other.
	DeleteFolder  commands.Type = "folder.delete"
	RestoreFolder commands.Type = "folder.restore"
)

// createFolderPayload is CreateFolder's payload.
type createFolderPayload struct {
	ID       string `json:"id" doc:"The new folder's ID, a fld_ ID of a UUIDv7 that the client makes, which no entity of the instance has (PRD §5.2)"`
	Kind     string `json:"kind" enum:"board,variable,component" doc:"What the folder holds: boards, variables, or components, each in a tree of folders of its own"`
	Name     string `json:"name" maxLength:"100" doc:"The folder's name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them"`
	ParentID string `json:"parent_id,omitempty" doc:"The ID of the folder of the same kind that the folder goes into, which is not deleted. Without it, the folder goes at the top."`
	Order    string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The folder's place among the children of its parent, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and then by their IDs: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the folder goes after the last child."`
}

// renameFolderPayload is RenameFolder's payload.
type renameFolderPayload struct {
	ID   string `json:"id" doc:"The folder's ID"`
	Name string `json:"name" maxLength:"100" doc:"The folder's new name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them"`
}

// moveFolderPayload is MoveFolder's payload.
type moveFolderPayload struct {
	ID       string `json:"id" doc:"The folder's ID"`
	ParentID string `json:"parent_id,omitempty" doc:"The ID of the folder of the same kind that the folder goes into, which is not deleted, and neither the folder itself nor a folder inside it. Without it, the folder goes to the top."`
	Order    string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The folder's place among the children of its new parent, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and then by their IDs: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the folder goes after the last child."`
}

// folderIDPayload is the payload of a command that needs nothing but its
// folder's ID.
type folderIDPayload struct {
	ID string `json:"id" doc:"The folder's ID"`
}

// Register registers in r the folder and the board as entities of the
// pipeline, whose revisions a command expects as it does any entity's, and
// their commands, each of which changes the project's draft, so that an API
// token needs project:write for it (FR-AUTH-04). A board's commands need the
// role "Create/rename/archive boards" (§5.6), and a folder's "Create, rename,
// move, and delete folders", whatever it holds.
func Register(r *commands.Registry) {
	commands.RegisterEntity(r, commands.Entity{
		Kind: ids.Folder,
		Revs: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]int64, error) {
			rows, err := queries.New(tx).FolderRevs(ctx, queries.FolderRevsParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			revs := make(map[ids.UUID]int64, len(rows))
			for _, row := range rows {
				revs[row.ID.Bytes] = row.Rev
			}
			return revs, nil
		},
		States: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]any, error) {
			rows, err := queries.New(tx).FoldersByID(ctx, queries.FoldersByIDParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			states := make(map[ids.UUID]any, len(rows))
			for _, row := range rows {
				states[row.Folder.ID.Bytes] = folderOf(row.Folder)
			}
			return states, nil
		},
	})
	commands.RegisterEntity(r, commands.Entity{
		Kind: ids.Board,
		Revs: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]int64, error) {
			rows, err := queries.New(tx).BoardRevs(ctx, queries.BoardRevsParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			revs := make(map[ids.UUID]int64, len(rows))
			for _, row := range rows {
				revs[row.ID.Bytes] = row.Rev
			}
			return revs, nil
		},
		States: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]any, error) {
			rows, err := queries.New(tx).BoardsByID(ctx, queries.BoardsByIDParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			states := make(map[ids.UUID]any, len(rows))
			for _, row := range rows {
				states[row.Board.ID.Bytes] = boardOf(row.Board)
			}
			return states, nil
		},
	})
	registerBoards(r)
	registerFolders(r)
}

// registerFolders registers in r the folder's commands.
func registerFolders(r *commands.Registry) {
	folder := []ids.Kind{ids.Folder}

	commands.Register(r, commands.Spec[createFolderPayload]{
		Type: CreateFolder, Capability: authz.ManageFolders, Scope: scope.ProjectWrite, Touches: folder,
		Check: func(p createFolderPayload) input.FieldErrors {
			errs := newID(ids.Folder, p.ID)
			errs = append(errs, field("name", checkName(p.Name))...)
			return append(errs, optionalID(ids.Folder, "parent_id", p.ParentID)...)
		},
		Inverse: func(_ context.Context, _ *commands.Tx, p createFolderPayload) ([]commands.Step, error) {
			return []commands.Step{{Type: DeleteFolder, Payload: folderIDPayload{ID: p.ID}}}, nil
		},
		Apply: applyCreateFolder,
	})

	commands.Register(r, commands.Spec[renameFolderPayload]{
		Type: RenameFolder, Capability: authz.ManageFolders, Scope: scope.ProjectWrite, Touches: folder,
		Check: func(p renameFolderPayload) input.FieldErrors {
			return append(folderID(p.ID), field("name", checkName(p.Name))...)
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p renameFolderPayload) ([]commands.Step, error) {
			f, err := liveFolderOf(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: RenameFolder, Payload: renameFolderPayload{ID: p.ID, Name: f.Name}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p renameFolderPayload) error {
			f, err := liveFolderOf(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			return changed(tx, ids.Folder, f.ID)(queries.New(tx).UpdateFolderName(ctx, queries.UpdateFolderNameParams{
				Name: p.Name, UpdatedBy: by(tx), WorkspaceID: f.WorkspaceID, ProjectID: f.ProjectID, ID: f.ID}))
		},
	})

	commands.Register(r, commands.Spec[moveFolderPayload]{
		Type: MoveFolder, Capability: authz.ManageFolders, Scope: scope.ProjectWrite, Touches: folder,
		Check: func(p moveFolderPayload) input.FieldErrors {
			return append(folderID(p.ID), optionalID(ids.Folder, "parent_id", p.ParentID)...)
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p moveFolderPayload) ([]commands.Step, error) {
			f, err := liveFolderOf(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: MoveFolder, Payload: moveFolderPayload{ID: p.ID,
				ParentID: encode(ids.Folder, f.ParentID), Order: f.Order}}}, nil
		},
		Apply: applyMoveFolder,
	})

	commands.Register(r, commands.Spec[folderIDPayload]{
		Type: DeleteFolder, Capability: authz.ManageFolders, Scope: scope.ProjectWrite, Touches: folder,
		Check: func(p folderIDPayload) input.FieldErrors { return folderID(p.ID) },
		// Deleting a folder that is deleted already does nothing, and its
		// undo nothing either.
		Inverse: func(ctx context.Context, tx *commands.Tx, p folderIDPayload) ([]commands.Step, error) {
			f, err := anyFolder(ctx, tx, p.ID)
			if err != nil || f.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: RestoreFolder, Payload: folderIDPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p folderIDPayload) error {
			f, err := anyFolder(ctx, tx, p.ID)
			if err != nil || f.DeletedAt.Valid {
				return err
			}
			q := queries.New(tx)
			holds, err := q.FolderHolds(ctx, queries.FolderHoldsParams{WorkspaceID: f.WorkspaceID,
				ProjectID: f.ProjectID, ID: f.ID})
			switch {
			case err != nil:
				return fmt.Errorf("board: read what the folder holds: %w", err)
			case holds:
				return commands.Refuse("id", "is a folder that holds folders or boards that are not deleted, which "+
					"must be moved out of it or deleted first", ErrFolderNotEmpty)
			}
			return changed(tx, ids.Folder, f.ID)(q.TrashFolder(ctx, queries.TrashFolderParams{UpdatedBy: by(tx),
				WorkspaceID: f.WorkspaceID, ProjectID: f.ProjectID, ID: f.ID}))
		},
	})

	commands.Register(r, commands.Spec[folderIDPayload]{
		Type: RestoreFolder, Capability: authz.ManageFolders, Scope: scope.ProjectWrite, Touches: folder,
		Check: func(p folderIDPayload) input.FieldErrors { return folderID(p.ID) },
		// Restoring a folder that is not deleted does nothing, and its undo
		// nothing either. The undo of a restore deletes the folder, and then
		// the folders that the restore restored above it, from the nearest.
		Inverse: func(ctx context.Context, tx *commands.Tx, p folderIDPayload) ([]commands.Step, error) {
			f, err := anyFolder(ctx, tx, p.ID)
			if err != nil || !f.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			deleted, err := deletedAncestors(ctx, queries.New(tx), f.WorkspaceID, f.ProjectID, f.ID)
			if err != nil {
				return nil, err
			}
			steps := make([]commands.Step, len(deleted))
			for i, d := range deleted {
				steps[i] = commands.Step{Type: DeleteFolder, Payload: folderIDPayload{ID: ids.Encode(ids.Folder, d.Bytes)}}
			}
			return steps, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p folderIDPayload) error {
			f, err := anyFolder(ctx, tx, p.ID)
			if err != nil || !f.DeletedAt.Valid {
				return err
			}
			return restoreAncestors(ctx, tx, queries.New(tx), f.WorkspaceID, f.ProjectID, f.ID)
		},
	})
}

// applyCreateFolder is CreateFolder's Apply.
func applyCreateFolder(ctx context.Context, tx *commands.Tx, p createFolderPayload) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	id := decoded(ids.Folder, p.ID)
	if _, err := q.FolderAny(ctx, queries.FolderAnyParams{WorkspaceID: ws, ProjectID: project, ID: id}); err == nil {
		return commands.Refuse("id", "is the ID of a folder that exists already", nil)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("board: read the folder: %w", err)
	}
	parent, err := liveFolder(ctx, q, ws, project, p.Kind, "parent_id", p.ParentID)
	if err != nil {
		return err
	}
	order, err := sortKey(ctx, q, ws, project, p.Kind, parent, p.Order)
	if err != nil {
		return err
	}
	err = q.InsertFolder(ctx, queries.InsertFolderParams{ID: id, WorkspaceID: ws, ProjectID: project, Kind: p.Kind,
		ParentID: parent, Name: p.Name, SortKey: order, CreatedBy: by(tx)})
	if taken(err, "folder_pkey") {
		return commands.Refuse("id", "is the ID of a folder that exists already", nil)
	}
	if err != nil {
		return fmt.Errorf("board: add the folder: %w", err)
	}
	return tx.Changed(ids.Folder, id.Bytes, 1)
}

// applyMoveFolder is MoveFolder's Apply.
func applyMoveFolder(ctx context.Context, tx *commands.Tx, p moveFolderPayload) error {
	f, err := liveFolderOf(ctx, tx, p.ID)
	if err != nil {
		return err
	}
	q := queries.New(tx)
	parent, err := liveFolder(ctx, q, f.WorkspaceID, f.ProjectID, f.Kind, "parent_id", p.ParentID)
	if err != nil {
		return err
	}
	if parent.Valid {
		// The parent may be neither the folder nor one inside it, which
		// would leave the folder in a cycle, out of the tree.
		above, err := q.FolderAncestors(ctx, queries.FolderAncestorsParams{WorkspaceID: f.WorkspaceID,
			ProjectID: f.ProjectID, ID: parent})
		if err != nil {
			return fmt.Errorf("board: read the folders above the parent: %w", err)
		}
		for _, a := range above {
			if a.ID == f.ID {
				return commands.Refuse("parent_id", "is the folder itself, or a folder inside it", nil)
			}
		}
	}
	order, err := sortKey(ctx, q, f.WorkspaceID, f.ProjectID, f.Kind, parent, p.Order)
	if err != nil {
		return err
	}
	return changed(tx, ids.Folder, f.ID)(q.MoveFolder(ctx, queries.MoveFolderParams{ParentID: parent,
		SortKey: order, UpdatedBy: by(tx), WorkspaceID: f.WorkspaceID, ProjectID: f.ProjectID, ID: f.ID}))
}

// anyFolder returns the folder of the batch's project whose ID is id, a
// folder's ID that its payload's Check checked, whether or not it is deleted,
// or refuses the batch if the project has no such folder.
func anyFolder(ctx context.Context, tx *commands.Tx, id string) (queries.Folder, error) {
	row, err := queries.New(tx).FolderAny(ctx, queries.FolderAnyParams{WorkspaceID: nullUUID(tx.WorkspaceID()),
		ProjectID: nullUUID(tx.ProjectID()), ID: decoded(ids.Folder, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.Folder{}, commands.Refuse("id", "is not a folder of the project", nil)
	case err != nil:
		return queries.Folder{}, fmt.Errorf("board: read the folder: %w", err)
	}
	return row.Folder, nil
}

// liveFolderOf returns the folder of the batch's project whose ID is id as
// anyFolder does, or refuses the batch if it is deleted, which only
// restoring it changes.
func liveFolderOf(ctx context.Context, tx *commands.Tx, id string) (queries.Folder, error) {
	f, err := anyFolder(ctx, tx, id)
	if err == nil && f.DeletedAt.Valid {
		return queries.Folder{}, commands.Refuse("id", "is a deleted folder, which only restoring it changes", nil)
	}
	return f, err
}

// deletedAncestors returns the folder folder of the project, and the folders
// above it, that are deleted, from the nearest, or none if folder is null.
func deletedAncestors(ctx context.Context, q *queries.Queries, ws, project, folder pgtype.UUID) ([]pgtype.UUID,
	error) {
	if !folder.Valid {
		return nil, nil
	}
	rows, err := q.FolderAncestors(ctx, queries.FolderAncestorsParams{WorkspaceID: ws, ProjectID: project,
		ID: folder})
	if err != nil {
		return nil, fmt.Errorf("board: read the folders above: %w", err)
	}
	var deleted []pgtype.UUID
	for _, row := range rows {
		if row.Deleted {
			deleted = append(deleted, row.ID)
		}
	}
	return deleted, nil
}

// restoreAncestors restores the folder folder of the project, and the folders
// above it, that are deleted, so that what is restored into folder is in the
// tree again, and reports each change.
func restoreAncestors(ctx context.Context, tx *commands.Tx, q *queries.Queries, ws, project,
	folder pgtype.UUID) error {
	deleted, err := deletedAncestors(ctx, q, ws, project, folder)
	if err != nil {
		return err
	}
	for _, id := range deleted {
		if err := changed(tx, ids.Folder, id)(q.RestoreFolder(ctx, queries.RestoreFolderParams{UpdatedBy: by(tx),
			WorkspaceID: ws, ProjectID: project, ID: id})); err != nil {
			return err
		}
	}
	return nil
}

// folderID returns the problem of id, the ID of the folder that a command is
// about, unless it is a folder's ID.
func folderID(id string) input.FieldErrors {
	if _, err := ids.Decode(ids.Folder, id); err != nil {
		return field("id", "is not a folder's ID: "+err.Error())
	}
	return nil
}

// uuids returns entities as uuid columns.
func uuids(entities []ids.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(entities))
	for i, id := range entities {
		out[i] = nullUUID(id)
	}
	return out
}
