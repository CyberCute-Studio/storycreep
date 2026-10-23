// Package board keeps a project's boards and its folders (PRD FR-PROJ-02,
// §5.3): the named canvases of the project, organised in a tree of folders,
// and the folders of its variables and components, each tree of its own
// kind. Register registers the board and the folder as entities of the
// pipeline's registry (package commands), whose revisions a client expects as
// it does any entity's (§5.7, ADR-006), with their commands, the one way that
// they change; Service reads them.
//
// A board has a slug, unique within the project, which names its file in the
// game project (§6.3): derived from its name when it is created without one,
// as the first of the name's slug, then that with -2, -3, and so on, that no
// board of the project has, the boards in its trash included, so that
// restoring one never changes its slug, and changed only by a change of the
// slug itself, never by a rename (FR-PROJ-02 AC1). A slug is file-name safe
// on every platform (package slug, AC4): a name that Windows reserves for a
// device is derived as con-board, and a typed one is refused.
//
// The children of a folder, or those at the top, are ordered among
// themselves by keys of package orderkey, which the client picks, so that
// moving one changes it alone, and the boards of a folder of boards, and the
// variables of a folder of variables (package variable, which places them
// with LiveFolder and SortKey), share that order with its folders. A command
// that leaves the key out puts the child after the folder's last, the deleted
// ones included, or after its last that is not deleted once no key comes
// after that, and a move that leaves it out keeps the key of a child that is
// the folder's last that is not deleted already, a variable in the trash
// with its board counting as a deleted one.
//
// A deleted board goes to the project's trash, from which restoring it
// brings it back where it was, with the folders above it that were deleted
// since (package trash lists the trash, and purges what has been in it for
// 30 days). Its variables are in the trash with it, and come back with it,
// with the deleted folders above them too. A folder is deleted only once it
// holds nothing but what is in the trash, and stays, deleted, so that what is
// in the trash still names it.
// The reads of what is not in the trash read the live views of the boards
// and the folders, live_board and live_folder, which leave the trash out.
//
// Every command needs project:write of an API token (FR-AUTH-04), and of the
// actor's role on the project (§5.6) "Create/rename/archive boards" for a
// board, and "Create, rename, move, and delete folders" for a folder:
// owner, admin, or editor. An archived project takes none of them
// (FR-PROJ-01 AC2). Reading the boards and the folders is reading the
// project's content.
package board

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
)

// Limits of a board's and a folder's fields, which the database holds the
// names and the description to as well (migration 00021), so that the undo of
// a change of one, which sets the stored one back, holds none that the
// payload's schema refuses: change both at once.
const (
	// MaxNameLength is the most characters that a board's or a folder's name
	// may have.
	MaxNameLength = 100
	// MaxDescriptionLength is the most characters that a board's description
	// may have.
	MaxDescriptionLength = 2000
)

// ColorNone is the colour of a board without one.
const ColorNone = "none"

// colors are the colours of a board: none, or a name of a fixed palette,
// which the web app maps to an accessible colour in its light and dark
// themes alike.
var colors = []string{ColorNone, "red", "orange", "yellow", "green", "teal", "blue", "purple", "pink", "gray"}

// Colors returns the colours of a board, in the order of the database's
// check and of the schemas' enums, as a copy that the caller may change.
func Colors() []string {
	return slices.Clone(colors)
}

// The kinds of folder: what a folder holds, a tree of its own.
const (
	KindBoard     = "board"
	KindVariable  = "variable"
	KindComponent = "component"
)

// kinds are the kinds of folder.
var kinds = []string{KindBoard, KindVariable, KindComponent}

// Kinds returns the kinds of folder, in the order of the database's check
// and of the schemas' enums, as a copy that the caller may change.
func Kinds() []string {
	return slices.Clone(kinds)
}

var (
	// ErrNotFound is returned for a board that the project does not have, or
	// that is in its trash.
	ErrNotFound = errors.New("board: no such board")
	// ErrSlugTaken is the reason of a command's refusal of a slug that
	// another board of the project has, in its trash or not
	// (commands.RefusedError), which the API answers with slug_taken.
	ErrSlugTaken = errors.New("board: another board of the project has the slug")
	// ErrFolderNotEmpty is the reason of a command's refusal to delete a
	// folder that holds a folder, a board, or a variable that is not in the
	// trash, which the API answers with folder_not_empty.
	ErrFolderNotEmpty = errors.New("board: the folder holds folders, boards, or variables that are not deleted")
)

// Board is a board as the API shows it, and as an answer of revision_conflict
// shows it (commands.Entity.States).
type Board struct {
	ID        string `json:"id" doc:"The board's ID"`
	ProjectID string `json:"project_id" doc:"The ID of the board's project"`
	FolderID  string `json:"folder_id,omitempty" doc:"The ID of the folder of boards that the board is in, if it is in one, or none for a board at the top"`
	Order     string `json:"order" doc:"The board's place among the folders and boards of its folder, or of the top: they are ordered by their order, compared by its bytes, then by their IDs without the prefix, bd_ or fld_, compared by their bytes too (package orderkey)"`
	Name      string `json:"name" doc:"The board's name"`
	Slug      string `json:"slug" doc:"The board's slug, unique within the project, which names its file in the game project (PRD §6.3). It changes only when it is changed itself, never when the board is renamed (PRD FR-PROJ-02 AC1)."`
	// Description may be empty.
	Description string `json:"description" doc:"The board's description, which may be empty"`
	Color       string `json:"color" enum:"none,red,orange,yellow,green,teal,blue,purple,pink,gray" doc:"The board's colour, none or a name of the palette"`
	EntryNodeID string `json:"entry_node_id,omitempty" doc:"The ID of the board's entry node, if it has one"`
	Rev         int64  `json:"rev" doc:"The board's revision, which every change of it increments, and which a change expects with expected_rev"`
	// DeletedAt is set while the board is in the project's trash.
	DeletedAt *time.Time `json:"deleted_at,omitempty" doc:"When the board was deleted to the project's trash, if it is there"`
	CreatedAt time.Time  `json:"created_at" doc:"When the board was created"`
	UpdatedAt time.Time  `json:"updated_at" doc:"When the board last changed"`
}

// Folder is a folder as the API shows it, and as an answer of
// revision_conflict shows it.
type Folder struct {
	ID        string `json:"id" doc:"The folder's ID"`
	ProjectID string `json:"project_id" doc:"The ID of the folder's project"`
	Kind      string `json:"kind" enum:"board,variable,component" doc:"What the folder holds: boards, variables, or components, each in a tree of folders of its own"`
	ParentID  string `json:"parent_id,omitempty" doc:"The ID of the folder that the folder is in, of the same kind, if it is in one, or none for a folder at the top"`
	Name      string `json:"name" doc:"The folder's name"`
	Order     string `json:"order" doc:"The folder's place among the folders, and for a folder of boards the boards, of its parent, or of the top: they are ordered by their order, compared by its bytes, then by their IDs without the prefix, bd_ or fld_, compared by their bytes too (package orderkey)"`
	Rev       int64  `json:"rev" doc:"The folder's revision, which every change of it increments, and which a change expects with expected_rev"`
	// DeletedAt is set for a deleted folder.
	DeletedAt *time.Time `json:"deleted_at,omitempty" doc:"When the folder was deleted, if it is"`
	CreatedAt time.Time  `json:"created_at" doc:"When the folder was created"`
	UpdatedAt time.Time  `json:"updated_at" doc:"When the folder last changed"`
}

// boardOf returns the board b as the API shows it.
func boardOf(b queries.Board) Board {
	return Board{ID: ids.Encode(ids.Board, b.ID.Bytes), ProjectID: ids.Encode(ids.Project, b.ProjectID.Bytes),
		FolderID: encode(ids.Folder, b.FolderID), Order: b.Order, Name: b.Name, Slug: b.Slug,
		Description: b.Description, Color: b.Color, EntryNodeID: encode(ids.Node, b.EntryNodeID), Rev: b.Rev,
		DeletedAt: timeOf(b.DeletedAt), CreatedAt: b.CreatedAt.Time, UpdatedAt: b.UpdatedAt.Time}
}

// folderOf returns the folder f as the API shows it.
func folderOf(f queries.Folder) Folder {
	return Folder{ID: ids.Encode(ids.Folder, f.ID.Bytes), ProjectID: ids.Encode(ids.Project, f.ProjectID.Bytes),
		Kind: f.Kind, ParentID: encode(ids.Folder, f.ParentID), Name: f.Name, Order: f.Order, Rev: f.Rev,
		DeletedAt: timeOf(f.DeletedAt), CreatedAt: f.CreatedAt.Time, UpdatedAt: f.UpdatedAt.Time}
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
	// boards and folders. It is required.
	Projects *project.Service
}

// Service reads the boards and the folders of the projects.
type Service struct {
	db       DB
	projects *project.Service
}

// New returns a Service that reads the boards and folders in db, with opts.
// It panics if opts.Projects is nil.
func New(db DB, opts Options) *Service {
	if opts.Projects == nil {
		panic("board: a Service needs the projects' service that authorises its reads")
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

// List returns at most limit of the boards of the project projectID that are
// not in its trash, in the order of their IDs: the first, if after is zero,
// or else those after the board after. It returns the errors of
// project.Service.Get.
func (s *Service) List(ctx context.Context, actor commands.Actor, projectID, after ids.UUID, limit int) ([]Board,
	error) {
	var out []Board
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		rows, err := q.Boards(ctx, queries.BoardsParams{WorkspaceID: workspaceID, ProjectID: nullUUID(projectID),
			After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("board: list the boards: %w", err)
		}
		for _, row := range rows {
			out = append(out, boardOf(queries.Board(row.LiveBoard)))
		}
		return nil
	})
	return out, err
}

// Get returns the board boardID of the project projectID, or ErrNotFound if
// the project has no such board, or it is in the trash. It returns the errors
// of project.Service.Get first.
func (s *Service) Get(ctx context.Context, actor commands.Actor, projectID, boardID ids.UUID) (Board, error) {
	var out Board
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		row, err := q.Board(ctx, queries.BoardParams{WorkspaceID: workspaceID, ProjectID: nullUUID(projectID),
			ID: nullUUID(boardID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("board: read the board: %w", err)
		}
		out = boardOf(queries.Board(row.LiveBoard))
		return nil
	})
	return out, err
}

// Folders returns at most limit of the folders of the project projectID that
// are not deleted, of the kind kind, or of every kind if it is "", in the
// order of their IDs: the first, if after is zero, or else those after the
// folder after. It returns the errors of project.Service.Get.
func (s *Service) Folders(ctx context.Context, actor commands.Actor, projectID ids.UUID, kind string,
	after ids.UUID, limit int) ([]Folder, error) {
	var out []Folder
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		rows, err := q.Folders(ctx, queries.FoldersParams{WorkspaceID: workspaceID, ProjectID: nullUUID(projectID),
			Kind: pgtype.Text{String: kind, Valid: kind != ""}, After: nullUUID(after),
			MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("board: list the folders: %w", err)
		}
		for _, row := range rows {
			out = append(out, folderOf(queries.Folder(row.LiveFolder)))
		}
		return nil
	})
	return out, err
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
