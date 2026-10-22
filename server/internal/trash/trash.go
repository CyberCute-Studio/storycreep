// Package trash keeps a project's trash (PRD FR-PROJ-06): the boards, and,
// as they are added, the nodes, variables, and components, that were deleted
// to it, where each stays for Days days from its deletion, from which the
// command of its kind restores it where it was, as board.restore does
// (package board), and after which the purge deletes it for good.
//
// A row of the project's content is in the trash while its deleted_at is
// set. Nothing reads it there but its restoration and this package: the
// reads of the content, and, as they are added, the search, the exports, and
// the snapshots, read the live view of its table, as live_board, which
// leaves the trash out (AC2). A folder that is deleted goes to the trash too,
// so that the boards there still name it, but is not listed: restoring a
// board restores the deleted folders above it, and the purge deletes a
// folder once its time has passed and it holds nothing.
//
// A table whose rows go to the trash is registered with it by its live view,
// its branch of the list's query (TrashItems) if its rows are listed, and
// its purge (PurgeBoards, PurgeFolders), which the package's tests hold every
// table of a project's content with a deleted_at to.
//
// Listing the trash is reading the project's content (§5.6): it needs a role
// on the project that is not none, and project:read of an API token.
package trash

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
)

// Days is how many days an entity stays in its project's trash, from its
// deletion, before the purge deletes it (FR-PROJ-06), as a project stays in
// its workspace's (project.TrashDays). Plan item 112 makes it a setting of
// the workspace's retention (FR-GDPR-03).
const Days = project.TrashDays

// Retention is Days as a duration, of days of 24 hours, which the purge
// counts from an entity's deletion.
const Retention = Days * 24 * time.Hour

// kinds are the kinds of entity that the trash lists, as FR-PROJ-06 names
// them, in the order of the schema's enum (Item.Kind). Only boards exist yet;
// plan items 33–35 add the others' tables, whose rows the list then holds.
var kinds = []ids.Kind{ids.Board, ids.Node, ids.Variable, ids.Component}

// Kinds returns the names of the kinds of entity that the trash lists, as an
// Item's Kind names them, in the order of the schema's enum.
func Kinds() []string {
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = k.String()
	}
	return out
}

// IsKind reports whether name is the name of a kind of entity that the trash
// lists, as an Item's Kind names it.
func IsKind(name string) bool {
	_, ok := kindNamed(name)
	return ok
}

// kindNamed returns the kind of entity that the trash lists whose name is
// name, and whether there is one.
func kindNamed(name string) (ids.Kind, bool) {
	for _, k := range kinds {
		if k.String() == name {
			return k, true
		}
	}
	return 0, false
}

// Item is an entity in a project's trash, as the API lists it.
type Item struct {
	Kind string `json:"kind" enum:"board,node,variable,component" doc:"What the entity is: a board, and, once they exist, a node, a variable, or a component (PRD FR-PROJ-06). The command of its kind, as board.restore, restores it with its ID as id."`
	ID   string `json:"id" doc:"The entity's ID"`
	Name string `json:"name" doc:"The entity's name"`
	// DeletedAt is when the entity was deleted to the trash.
	DeletedAt time.Time `json:"deleted_at" doc:"When the entity was deleted to the trash"`
	// PurgeAfter is DeletedAt and Retention.
	PurgeAfter time.Time `json:"purge_after" doc:"When the entity's time in the trash ends. The purge, which runs every hour, deletes it for good then; until it does, the entity can be restored."`
	// uuid is the UUID that ID encodes, by which the list orders the
	// entities of a kind deleted at once.
	uuid ids.UUID
}

// After returns the Cursor of the page of the trash's list that starts after
// it.
func (it Item) After() Cursor {
	return Cursor{DeletedAt: it.DeletedAt, Kind: it.Kind, ID: it.uuid}
}

// Cursor is where a page of the trash's list starts: after the entity of the
// kind Kind with the ID ID, deleted at DeletedAt, in the order of the list.
// The zero Cursor starts at the first.
type Cursor struct {
	DeletedAt time.Time
	Kind      string
	ID        ids.UUID
}

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// Projects reads the projects, which authorises each read of their
	// trash. It is required.
	Projects *project.Service
}

// Service reads the projects' trash.
type Service struct {
	db       DB
	projects *project.Service
}

// New returns a Service that reads the trash in db, with opts. It panics if
// opts.Projects is nil.
func New(db DB, opts Options) *Service {
	if opts.Projects == nil {
		panic("trash: a Service needs the projects' service that authorises its reads")
	}
	return &Service{db: db, projects: opts.Projects}
}

// List returns at most limit of the entities in the trash of the project
// projectID that it lists, the most recently deleted first, then by their
// kinds' names and their UUIDs: the first, if after is the zero Cursor, or
// else those after it. It returns the errors of project.Service.Get, as
// project.ErrNotFound for a project that does not exist, is in the trash, on
// which the actor's role is none, or that the actor's API token does not
// cover. The token's scopes are the route's to check.
func (s *Service) List(ctx context.Context, actor commands.Actor, projectID ids.UUID, after Cursor, limit int) (
	[]Item, error) {
	var out []Item
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		params := queries.TrashItemsParams{WorkspaceID: nullUUID(workspaceID), ProjectID: nullUUID(projectID),
			MaxRows: int32(min(limit, 1<<20))}
		if !after.DeletedAt.IsZero() {
			params.AfterDeletedAt = pgtype.Timestamptz{Time: after.DeletedAt, Valid: true}
			params.AfterKind, params.AfterID = after.Kind, pgtype.UUID{Bytes: after.ID, Valid: true}
		}
		rows, err := queries.New(tx).TrashItems(ctx, params)
		if err != nil {
			return fmt.Errorf("trash: list the trash: %w", err)
		}
		out = make([]Item, 0, len(rows))
		for _, row := range rows {
			kind, ok := kindNamed(row.Kind)
			if !ok {
				return fmt.Errorf("trash: the trash holds an entity of the kind %q, which it does not list", row.Kind)
			}
			out = append(out, Item{Kind: row.Kind, ID: ids.Encode(kind, row.ID.Bytes), Name: row.Name,
				DeletedAt: row.DeletedAt.Time, PurgeAfter: row.DeletedAt.Time.Add(Retention), uuid: row.ID.Bytes})
		}
		return nil
	})
	return out, err
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
