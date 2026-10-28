// Package connection keeps the connections of a project's boards (PRD
// FR-EDIT-03, plan note N44): the arrows of a board's flow, each from a node
// of the board to a node of the board, which the flow follows from the one to
// the other. Register registers the connection as an entity of the
// pipeline's registry (package commands), whose revisions a client expects
// as it does any entity's (§5.7, ADR-006), with its commands, the one way
// that connections change; Service reads them.
//
// A connection leaves an element by its port, out, or a branch by one of the
// branch's outputs (package node), and enters a node of the board, of any
// type, the node that it leaves among them, by as many connections as the
// author draws, which the validation reports (FR-DEP-03). A jumper has no
// connections out: the flow continues at its target (§5.8.5). The connections
// that leave a port are the choices that it offers, in their order, a key of
// package orderkey, by which they are ordered, then by their IDs, so that
// moving one changes it alone (FR-EDIT-03 AC2). A connection's label, a
// rich-text document (package richtext), is the choice that the player is
// offered, an empty paragraph for none; its condition, an expression (package
// expression), makes it a choice while it holds, and its on-traverse script
// runs when the flow traverses it (§5.8.5).
//
// A connection deleted on its own goes to the project's trash, unlisted, from
// which restoring it, as its undo does, brings it back. A node's connections
// are in the trash with it while it is there, and with its board, and so are
// an output's, and nothing changes them meanwhile: one comes back once
// neither of its nodes, nor the output that it leaves, is in the trash, so
// that restoring a node, an output, or a board, with the nodes that are in
// the trash with it, reconnects each of their connections whose node at the
// other end, and whose output, is not, and the batch's answer lists the
// others, each once, with what it waits for, which brings it back once it is
// restored too (FR-PROJ-06 AC1). The trash's purge of a node, or of an
// output, deletes its connections, by the cascade of their foreign keys. The reads of what is not
// in the trash read the live view, live_connection.
//
// Every command needs project:write of an API token (FR-AUTH-04), and the
// actor's role on the project "Edit nodes, connections, text, expressions"
// (§5.6): owner, admin, or editor. An archived project takes none of them
// (FR-PROJ-01 AC2). Reading them is reading the project's content.
package connection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

// PortOut is an element's one port, which its connections leave; a branch's
// leave its outputs, which name them by their IDs.
const PortOut = "out"

var (
	// ErrNotFound is returned for a connection that the project does not
	// have, or that is in its trash, itself or with one of its nodes, the
	// output that it leaves, or its board.
	ErrNotFound = errors.New("connection: no such connection")
)

// Connection is a connection as the API shows it, and as an answer of
// revision_conflict shows it (commands.Entity.States).
type Connection struct {
	ID         string                 `json:"id" doc:"The connection's ID"`
	ProjectID  string                 `json:"project_id" doc:"The ID of the connection's project"`
	BoardID    string                 `json:"board_id" doc:"The ID of the connection's board, the board of both of its nodes, which is fixed once the connection is created"`
	FromNodeID string                 `json:"from_node_id" doc:"The ID of the node that the connection leaves, a node of its board"`
	FromPort   string                 `json:"from_port" doc:"The port of the node that the connection leaves: out, an element's one port, or the ID of an output of a branch (PRD FR-EDIT-04)"`
	ToNodeID   string                 `json:"to_node_id" doc:"The ID of the node that the connection enters, a node of its board, which may be the node that it leaves"`
	Order      string                 `json:"order" doc:"The connection's place among the connections that leave its port, the choices that its node offers, in their order (PRD FR-EDIT-03 AC1): a key of base-62 digits, 0-9, A-Z, and a-z, which orders them by its bytes, and those with the same key by their IDs without the prefix, cn_, compared by their bytes too"`
	Label      richtext.Doc           `json:"label" doc:"The connection's label, a rich-text document (PRD FR-TEXT-01): the choice that the player is offered, or an empty paragraph for none"`
	Condition  *expression.Expression `json:"condition,omitempty" doc:"The connection's condition, which makes it a choice while it holds (PRD §5.8.5), if it has one; without one, it always is"`
	OnTraverse *expression.Expression `json:"on_traverse,omitempty" doc:"The connection's on-traverse script, which runs when the flow traverses it (PRD §5.8.5), if it has one"`
	Rev        int64                  `json:"rev" doc:"The connection's revision, which every change of it increments, a change of its expressions' compilation among them, and which a change expects with expected_rev"`
	// DeletedAt is set while the connection is in the project's trash, which
	// only an answer of revision_conflict shows.
	DeletedAt *time.Time `json:"deleted_at,omitempty" doc:"When the connection went to the project's trash, if it is there: when it was deleted, or, if it was not, but one of its nodes, the branch output that it leaves, or its board, is in the trash, with which it is in the trash too, when the first of them went there"`
	CreatedAt time.Time  `json:"created_at" doc:"When the connection was created"`
	UpdatedAt time.Time  `json:"updated_at" doc:"When the connection last changed"`
}

// connectionOf returns the connection c as the API shows it, or an error if
// its label is not what the database's checks hold it to.
func connectionOf(c queries.Connection) (Connection, error) {
	out := Connection{ID: ids.Encode(ids.Connection, c.ID.Bytes), ProjectID: ids.Encode(ids.Project, c.ProjectID.Bytes),
		BoardID: ids.Encode(ids.Board, c.BoardID.Bytes), FromNodeID: ids.Encode(ids.Node, c.FromNodeID.Bytes),
		FromPort: portOf(c.FromOutputID), ToNodeID: ids.Encode(ids.Node, c.ToNodeID.Bytes), Order: c.Order,
		Rev: c.Rev, DeletedAt: timeOf(c.DeletedAt), CreatedAt: c.CreatedAt.Time, UpdatedAt: c.UpdatedAt.Time}
	doc, err := richtext.Parse(c.Label)
	if err != nil {
		return Connection{}, fmt.Errorf("connection: the label of the connection %s: %w", out.ID, err)
	}
	out.Label = *doc
	return out, nil
}

// connectionsOf returns the connections rows of the project as the API shows
// them, in their order, with their expressions.
func connectionsOf(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, rows []queries.Connection) (
	[]Connection, error) {
	out := make([]Connection, len(rows))
	list := make([]pgtype.UUID, len(rows))
	for i, c := range rows {
		var err error
		if out[i], err = connectionOf(c); err != nil {
			return nil, err
		}
		list[i] = c.ID
	}
	exprs, err := expression.Of(ctx, q, ws, project, ids.Connection, list)
	if err != nil {
		return nil, err
	}
	for i, c := range rows {
		out[i].Condition = exprs[c.ID.Bytes][expression.Condition]
		out[i].OnTraverse = exprs[c.ID.Bytes][expression.OnTraverse]
	}
	return out, nil
}

// portOf returns the port of a connection whose column of the output that it
// leaves is output: out if it is null, or else the output's ID.
func portOf(output pgtype.UUID) string {
	if !output.Valid {
		return PortOut
	}
	return ids.Encode(ids.BranchOutput, output.Bytes)
}

// timeOf returns the time of the column t, or nil if it is null.
func timeOf(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// DB runs a transaction of the application login, as *database.DB does, and
// one that reads as of one moment (InSnapshot), which the reads run in.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
	InSnapshot(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// Projects reads the projects, which authorises each read of their
	// connections. It is required.
	Projects *project.Service
}

// Service reads the connections of the projects.
type Service struct {
	db       DB
	projects *project.Service
}

// New returns a Service that reads the connections in db, with opts. It
// panics if opts.Projects is nil.
func New(db DB, opts Options) *Service {
	if opts.Projects == nil {
		panic("connection: a Service needs the projects' service that authorises its reads")
	}
	return &Service{db: db, projects: opts.Projects}
}

// read runs fn in a transaction of the workspace of the project projectID,
// once it has checked that the actor may read the project's content
// (project.Service.ReadSnapshot): it returns the errors of
// project.Service.Get, as project.ErrNotFound for a project that does not
// exist, is in the trash, on which the actor's role is none, or that the
// actor's API token does not cover. The token's scopes are the route's to
// check. The transaction reads as of one moment, that of its start
// (database.DB.InSnapshot), and locks nothing: a connection is read in two
// statements, its row and its expressions, and a batch that commits between
// them, as one that changes a connection's condition, is then read whole or
// not at all, never a row as it was with a condition that the batch changed,
// at a revision that does not hold it.
func (s *Service) read(ctx context.Context, actor commands.Actor, projectID ids.UUID,
	fn func(q *queries.Queries, workspaceID pgtype.UUID) error) error {
	return s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		return fn(queries.New(tx), nullUUID(workspaceID))
	})
}

// BoardConnections returns at most limit of the connections of the board
// boardID of the project projectID that are not in the trash, in the order of
// their IDs: the first, if after is zero, or else those after the connection
// after. It returns the errors of project.Service.Get first, then
// board.ErrNotFound if the project has no such board, or it is in the trash.
func (s *Service) BoardConnections(ctx context.Context, actor commands.Actor, projectID, boardID, after ids.UUID,
	limit int) ([]Connection, error) {
	var out []Connection
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		project := nullUUID(projectID)
		_, err := q.Board(ctx, queries.BoardParams{WorkspaceID: workspaceID, ProjectID: project,
			ID: nullUUID(boardID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return board.ErrNotFound
		case err != nil:
			return fmt.Errorf("connection: read the board: %w", err)
		}
		rows, err := q.BoardConnections(ctx, queries.BoardConnectionsParams{WorkspaceID: workspaceID,
			ProjectID: project, BoardID: nullUUID(boardID), After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("connection: list the board's connections: %w", err)
		}
		live := make([]queries.Connection, len(rows))
		for i, r := range rows {
			live[i] = queries.Connection(r.LiveConnection)
		}
		out, err = connectionsOf(ctx, q, workspaceID, project, live)
		return err
	})
	return out, err
}

// Get returns the connection connectionID of the project projectID, or
// ErrNotFound if the project has no such connection, or it is in the trash,
// itself or with one of its nodes, the output that it leaves, or its board.
// It returns the errors of project.Service.Get first.
func (s *Service) Get(ctx context.Context, actor commands.Actor, projectID, connectionID ids.UUID) (Connection,
	error) {
	var out Connection
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		row, err := q.Connection(ctx, queries.ConnectionParams{WorkspaceID: workspaceID,
			ProjectID: nullUUID(projectID), ID: nullUUID(connectionID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("connection: read the connection: %w", err)
		}
		list, err := connectionsOf(ctx, q, workspaceID, nullUUID(projectID),
			[]queries.Connection{queries.Connection(row.LiveConnection)})
		if err != nil {
			return err
		}
		out = list[0]
		return nil
	})
	return out, err
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
