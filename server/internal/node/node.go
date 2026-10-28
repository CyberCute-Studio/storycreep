// Package node keeps the nodes of a project's boards (PRD FR-EDIT-02,
// FR-EDIT-05, FR-EDIT-06, plan note N43): the elements, branches, and
// jumpers that its narrative is made of. Register registers the node as an
// entity of the pipeline's registry (package commands), whose revisions a
// client expects as it does any entity's (§5.7, ADR-006), with its
// commands, the one way that nodes change; Service reads them.
//
// A node is of a board, and of one type, which is fixed once it is created:
// an element, a line of dialogue or of narration, as its kind says, with a
// rich-text document (package richtext), a speaker, a component of the
// project, a cover, an asset of the project, and whether the flow ends there;
// a branch, which picks the first of its outputs whose condition holds, or
// its else (FR-EDIT-04); or a jumper, which continues the flow at its target,
// a node of the project on any of its boards. Every node has a title, which
// may be empty, a colour of the palette that boards have, tags, which are the
// same tag whatever their case, a position on its board's canvas, kept to 2
// decimal places (FR-EDIT-02 AC4), and a size, or none, which the canvas then
// lays it out by itself for. An element and a branch have an on-enter and an
// on-exit script, each an expression (package expression), and a jumper
// none, since the flow never stops at it (§5.8.5).
//
// A branch's outputs are entities of their own, whose commands Register
// registers too: each a conditional output, with its condition, or the
// branch's else, which comes after them. The conditional outputs are ordered
// by a key, as a folder's children are, and the first of them is the
// branch's if, the others its elseifs, so that reordering them never leaves
// a branch whose first output is no if; a branch has at most one else, and
// one only while it has a conditional output (plan note N51). The
// connections that leave a branch leave its outputs (package connection),
// and are in the trash with the output that they leave, so that deleting an
// output, which needs to be confirmed while connections leave it, deletes
// them in its command (FR-EDIT-04 AC3), and restoring it brings them back.
//
// A reference to another entity, a speaker, a jumper's target, or a board's
// entry node (board.set_entry_node), takes an entity in the trash, so that
// the undo of a change of it from one deleted since sets it back, or keeps
// the one that it had, which may no longer exist; the validation reports one
// that is deleted (FR-DEP-03). A speaker has a foreign key, so that the
// trash's purge of a component leaves the elements whose speaker it is
// without one, and increments their revisions (package trash), those in the
// trash too: the one change of a node that no command makes. A jumper's
// target stays as it is when the node that it names is deleted, or purged,
// dangling (FR-EDIT-05 AC1), and so do the jumpers that target a board's
// nodes when the board is deleted (FR-PROJ-02 AC2), which package board's
// deletion lists and asks to confirm.
//
// A deleted node goes to the project's trash, which lists it, and from which
// restoring it brings it back, and its connections with it, which are in the
// trash with it meanwhile, but for those whose node at the other end is in
// the trash still, which the batch's answer lists (package connection,
// FR-PROJ-06 AC1). A board's nodes are in the trash with their
// board while it is there, unlisted, as its variables are, and nothing but
// the purge of a speaker changes them meanwhile: one of them deleted on its
// own, before its board, is listed, and restored only once its board is.
// The reads of what is not in the trash read the live view, live_node. Each
// node that is not in the trash counts towards its workspace's nodes
// (FR-ENT-02), which the deltas of its commands, and of its board's and its
// project's deletion and restoration, keep, approximately, and the
// reconciliation recounts.
//
// Every command needs project:write of an API token (FR-AUTH-04), and the
// actor's role on the project "Edit nodes, connections, text, expressions"
// (§5.6): owner, admin, or editor, but board.set_entry_node, a change of the
// board, "Create/rename/archive boards". An archived project takes none of
// them (FR-PROJ-01 AC2). Reading them is reading the project's content.
package node

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
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

// MaxTitleLength is the most characters that a node's title may have, which
// the database holds it to as well (migration 00023): change both at once.
const MaxTitleLength = 200

// MaxMoved is the most nodes that one node.move moves.
const MaxMoved = 10_000

// The types of node.
const (
	TypeElement = "element"
	TypeBranch  = "branch"
	TypeJumper  = "jumper"
)

// The kinds of element.
const (
	KindDialogue  = "dialogue"
	KindNarration = "narration"
)

var (
	// ErrNotFound is returned for a node that the project does not have, or
	// that is in its trash, itself or with its board.
	ErrNotFound = errors.New("node: no such node")
)

// NodeTarget is where a jumper continues the flow: a node of the project and
// the node's board, or none, as {} in a payload.
type NodeTarget struct {
	BoardID string `json:"board_id,omitempty" doc:"The ID of the board of the node that the jumper targets, which may be the jumper's own. With node_id, or neither of them, for none."`
	NodeID  string `json:"node_id,omitempty" doc:"The ID of the node that the jumper targets, a node of the board board_id. With board_id, or neither of them, for none."`
}

// Node is a node as the API shows it, and as an answer of revision_conflict
// shows it (commands.Entity.States).
type Node struct {
	ID        string `json:"id" doc:"The node's ID"`
	ProjectID string `json:"project_id" doc:"The ID of the node's project"`
	BoardID   string `json:"board_id" doc:"The ID of the node's board, which is fixed once the node is created"`
	Type      string `json:"type" enum:"element,branch,jumper" doc:"The node's type, which is fixed once it is created: an element, a line of dialogue or of narration; a branch, which picks the first of its outputs whose condition holds; or a jumper, which continues the flow at its target (PRD §5.8.5)"`
	// Kind, Text, SpeakerComponentID, CoverAssetID, and IsEnding are an
	// element's alone, and Target a jumper's.
	Kind               string        `json:"kind,omitempty" enum:"dialogue,narration" doc:"An element's kind: dialogue, which a speaker says, or narration. Another type has none."`
	Title              string        `json:"title" doc:"The node's title, which may be empty"`
	Text               *richtext.Doc `json:"text,omitempty" doc:"An element's text, a rich-text document (PRD FR-TEXT-01). Another type has none."`
	SpeakerComponentID string        `json:"speaker_component_id,omitempty" doc:"The ID of an element's speaker, a component of the project, if it has one. It may be a component in the project's trash, which the validation reports, until the purge deletes the component, which leaves the element without one."`
	CoverAssetID       string        `json:"cover_asset_id,omitempty" doc:"The ID of an element's cover image, an asset of the project, if it has one"`
	Color              string        `json:"color" enum:"none,red,orange,yellow,green,teal,blue,purple,pink,gray" doc:"The node's colour, none or a name of the palette that boards have"`
	Tags               []string      `json:"tags" nullable:"false" doc:"The node's tags, as they are written, in their order: no two are the same tag but for their case"`
	Position           NodePosition  `json:"position" doc:"Where the node is on its board's canvas"`
	Size               *NodeSize     `json:"size,omitempty" doc:"The node's width and height, if it has a size of its own; without one, the canvas lays the node out by itself"`
	IsEnding           bool          `json:"is_ending" doc:"Whether the flow ends at the element, which only an element may (PRD §5.8.5)"`
	Target             *NodeTarget   `json:"target,omitempty" doc:"A jumper's target, if it has one: the node that it continues the flow at, which may since have been deleted, or purged, which the validation reports (PRD FR-EDIT-05 AC1)"`
	// OnEnter and OnExit are an element's or a branch's, and Outputs a
	// branch's alone.
	OnEnter *expression.Expression `json:"on_enter,omitempty" doc:"An element's or a branch's on-enter script, which runs when the flow enters the node (PRD §5.8.5), if it has one. A jumper has none."`
	OnExit  *expression.Expression `json:"on_exit,omitempty" doc:"An element's or a branch's on-exit script, which runs when the flow leaves the node, by a connection or by ending there (PRD §5.8.5), if it has one. A jumper has none."`
	Outputs []BranchOutput         `json:"outputs,omitempty" doc:"A branch's outputs that are not in the project's trash, in their order (PRD FR-EDIT-04): its conditional outputs, the first its if and the others its elseifs, and its else after them, if it has one. Without them, the branch has none, which the validation reports. Another type has none. Each is an entity of its own, at a revision of its own, which a change of it increments, and not the branch's. An answer of revision_conflict for a branch in the trash shows those that are there with it alone, which come back with it."`
	Rev     int64                  `json:"rev" doc:"The node's revision, which every change of it increments, a change of its scripts' compilation among them, and which a change expects with expected_rev. A change of one of a branch's outputs changes the output's revision, not the node's."`
	// DeletedAt is set while the node is in the project's trash, which only
	// an answer of revision_conflict shows.
	DeletedAt *time.Time `json:"deleted_at,omitempty" doc:"When the node went to the project's trash, if it is there: when it was deleted, or, if it was not, but its board is in the trash, with which it is in the trash too, when the board was deleted"`
	CreatedAt time.Time  `json:"created_at" doc:"When the node was created"`
	UpdatedAt time.Time  `json:"updated_at" doc:"When the node last changed"`
}

// nodeOf returns the node n as the API shows it, or an error if its text, or
// its position or its size, is not what the database's checks hold them to.
func nodeOf(n queries.Node) (Node, error) {
	out := Node{ID: ids.Encode(ids.Node, n.ID.Bytes), ProjectID: ids.Encode(ids.Project, n.ProjectID.Bytes),
		BoardID: ids.Encode(ids.Board, n.BoardID.Bytes), Type: n.Type, Kind: n.Kind.String, Title: n.Title,
		SpeakerComponentID: encode(ids.Component, n.SpeakerComponentID), CoverAssetID: encode(ids.Asset, n.CoverAssetID),
		Color: n.Color, Tags: n.Tags, IsEnding: n.IsEnding, Rev: n.Rev, DeletedAt: timeOf(n.DeletedAt),
		CreatedAt: n.CreatedAt.Time, UpdatedAt: n.UpdatedAt.Time}
	if out.Tags == nil {
		out.Tags = []string{}
	}
	if n.Text != nil {
		doc, err := richtext.Parse(n.Text)
		if err != nil {
			return Node{}, fmt.Errorf("node: the text of the node %s: %w", out.ID, err)
		}
		out.Text = doc
	}
	var err error
	if out.Position, err = positionOf(n.X, n.Y); err != nil {
		return Node{}, fmt.Errorf("%w: the node %s", err, out.ID)
	}
	if out.Size, err = sizeOf(n.Width, n.Height); err != nil {
		return Node{}, fmt.Errorf("%w: the node %s", err, out.ID)
	}
	if n.TargetNodeID.Valid {
		out.Target = &NodeTarget{BoardID: ids.Encode(ids.Board, n.TargetBoardID.Bytes),
			NodeID: ids.Encode(ids.Node, n.TargetNodeID.Bytes)}
	}
	return out, nil
}

// nodesOf returns the nodes rows of the project as the API shows them, in
// their order, with their scripts, and a branch with its outputs that are
// not in the trash.
func nodesOf(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, rows []queries.Node) ([]Node,
	error) {
	out := make([]Node, len(rows))
	all := make([]pgtype.UUID, len(rows))
	var branches []pgtype.UUID
	for i, n := range rows {
		var err error
		if out[i], err = nodeOf(n); err != nil {
			return nil, err
		}
		all[i] = n.ID
		if n.Type == TypeBranch {
			branches = append(branches, n.ID)
		}
	}
	scripts, err := expression.Of(ctx, q, ws, project, ids.Node, all)
	if err != nil {
		return nil, err
	}
	outputs, err := nodeOutputs(ctx, q, ws, project, branches)
	if err != nil {
		return nil, err
	}
	for i, n := range rows {
		out[i].OnEnter, out[i].OnExit = scripts[n.ID.Bytes][expression.OnEnter], scripts[n.ID.Bytes][expression.OnExit]
		out[i].Outputs = outputs[n.ID.Bytes]
	}
	return out, nil
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

// DB runs a transaction of the application login, as *database.DB does, and
// one that reads as of one moment (InSnapshot), which the reads run in.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
	InSnapshot(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// Projects reads the projects, which authorises each read of their
	// nodes. It is required.
	Projects *project.Service
}

// Service reads the nodes of the projects.
type Service struct {
	db       DB
	projects *project.Service
}

// New returns a Service that reads the nodes in db, with opts. It panics if
// opts.Projects is nil.
func New(db DB, opts Options) *Service {
	if opts.Projects == nil {
		panic("node: a Service needs the projects' service that authorises its reads")
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
// (database.DB.InSnapshot), and locks nothing: a node is read in several
// statements, its row, its scripts, and a branch's outputs with their
// conditions, and a batch that commits between two of them, as one that
// changes a node's scripts, is then read whole or not at all, never a row as
// it was with scripts that the batch changed, at a revision that does not
// hold them.
func (s *Service) read(ctx context.Context, actor commands.Actor, projectID ids.UUID,
	fn func(q *queries.Queries, workspaceID pgtype.UUID) error) error {
	return s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		return fn(queries.New(tx), nullUUID(workspaceID))
	})
}

// liveBoard returns board.ErrNotFound unless the project has the board
// boardID, and it is not in the trash.
func liveBoard(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, boardID ids.UUID) error {
	_, err := q.Board(ctx, queries.BoardParams{WorkspaceID: ws, ProjectID: project, ID: nullUUID(boardID)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return board.ErrNotFound
	case err != nil:
		return fmt.Errorf("node: read the board: %w", err)
	}
	return nil
}

// BoardNodes returns at most limit of the nodes of the board boardID of the
// project projectID that are not in the trash, in the order of their IDs:
// the first, if after is zero, or else those after the node after. It returns
// the errors of project.Service.Get first, then board.ErrNotFound if the
// project has no such board, or it is in the trash.
func (s *Service) BoardNodes(ctx context.Context, actor commands.Actor, projectID, boardID, after ids.UUID,
	limit int) ([]Node, error) {
	var out []Node
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		project := nullUUID(projectID)
		if err := liveBoard(ctx, q, workspaceID, project, boardID); err != nil {
			return err
		}
		rows, err := q.BoardNodes(ctx, queries.BoardNodesParams{WorkspaceID: workspaceID, ProjectID: project,
			BoardID: nullUUID(boardID), After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("node: list the board's nodes: %w", err)
		}
		out, err = nodesOf(ctx, q, workspaceID, project, liveNodes(rows,
			func(r queries.BoardNodesRow) queries.LiveNode { return r.LiveNode }))
		return err
	})
	return out, err
}

// Get returns the node nodeID of the project projectID, or ErrNotFound if the
// project has no such node, or it is in the trash, itself or with its board.
// It returns the errors of project.Service.Get first.
func (s *Service) Get(ctx context.Context, actor commands.Actor, projectID, nodeID ids.UUID) (Node, error) {
	var out Node
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		n, err := liveNode(ctx, q, workspaceID, nullUUID(projectID), nodeID)
		if err != nil {
			return err
		}
		nodes, err := nodesOf(ctx, q, workspaceID, nullUUID(projectID), []queries.Node{n})
		if err != nil {
			return err
		}
		out = nodes[0]
		return nil
	})
	return out, err
}

// liveNode returns the node nodeID of the project, or ErrNotFound if it has no
// such node, or it is in the trash.
func liveNode(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, nodeID ids.UUID) (queries.Node,
	error) {
	row, err := q.Node(ctx, queries.NodeParams{WorkspaceID: ws, ProjectID: project, ID: nullUUID(nodeID)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.Node{}, ErrNotFound
	case err != nil:
		return queries.Node{}, fmt.Errorf("node: read the node: %w", err)
	}
	return queries.Node(row.LiveNode), nil
}

// ReferencedBy returns at most limit of the jumpers of the project projectID
// that are not in the trash, on any of its boards, that target the node
// nodeID ("referenced by", PRD FR-EDIT-05 AC2), in the order of their IDs:
// the first, if after is zero, or else those after the jumper after. It
// returns the errors of project.Service.Get first, then ErrNotFound if the
// project has no such node, or it is in the trash.
func (s *Service) ReferencedBy(ctx context.Context, actor commands.Actor, projectID, nodeID, after ids.UUID,
	limit int) ([]Node, error) {
	var out []Node
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		project := nullUUID(projectID)
		if _, err := liveNode(ctx, q, workspaceID, project, nodeID); err != nil {
			return err
		}
		rows, err := q.NodeReferrers(ctx, queries.NodeReferrersParams{WorkspaceID: workspaceID, ProjectID: project,
			NodeID: nullUUID(nodeID), After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("node: list the jumpers that target the node: %w", err)
		}
		out, err = nodesOf(ctx, q, workspaceID, project, liveNodes(rows,
			func(r queries.NodeReferrersRow) queries.LiveNode { return r.LiveNode }))
		return err
	})
	return out, err
}

// BoardReferencedBy returns at most limit of the jumpers of the project
// projectID that are not in the trash, on its boards other than boardID,
// that target a node of the board boardID that is not in the trash either,
// which deleting the board would leave dangling (PRD FR-PROJ-02 AC2), in the
// order of their IDs: the first, if after is zero, or else those after the
// jumper after. It returns the errors of project.Service.Get first, then
// board.ErrNotFound if the project has no such board, or it is in the trash.
func (s *Service) BoardReferencedBy(ctx context.Context, actor commands.Actor, projectID, boardID, after ids.UUID,
	limit int) ([]Node, error) {
	var out []Node
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		project := nullUUID(projectID)
		if err := liveBoard(ctx, q, workspaceID, project, boardID); err != nil {
			return err
		}
		rows, err := q.BoardReferrers(ctx, queries.BoardReferrersParams{WorkspaceID: workspaceID, ProjectID: project,
			BoardID: nullUUID(boardID), After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("node: list the jumpers that target the board's nodes: %w", err)
		}
		out, err = nodesOf(ctx, q, workspaceID, project, liveNodes(rows,
			func(r queries.BoardReferrersRow) queries.LiveNode { return r.LiveNode }))
		return err
	})
	return out, err
}

// Tags returns at most limit, and at most MaxSuggestions, of the tags of the
// nodes of the project projectID that are not in its trash that start with
// prefix, compared without regard to case, each once, as the most of the
// nodes that have it write it, the most used first, so that a client
// suggests the spelling that the project uses (PRD FR-EDIT-06). It returns
// the errors of project.Service.Get first, so that what a prefix holds is
// checked only for an actor who may read the project, and then
// input.FieldErrors at prefix for a prefix that CheckTagQuery refuses.
func (s *Service) Tags(ctx context.Context, actor commands.Actor, projectID ids.UUID, prefix string, limit int) (
	[]Tag, error) {
	var out []Tag
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		if problem := CheckTagQuery(prefix); problem != "" {
			return input.FieldErrors{{Field: "prefix", Message: problem}}
		}
		rows, err := q.ProjectTags(ctx, queries.ProjectTagsParams{WorkspaceID: workspaceID,
			ProjectID: nullUUID(projectID)})
		if err != nil {
			return fmt.Errorf("node: list the project's tags: %w", err)
		}
		spellings := make([]spelling, len(rows))
		for i, r := range rows {
			spellings[i] = spelling{tag: r.Tag, uses: r.Uses}
		}
		out = suggest(spellings, prefix, min(limit, MaxSuggestions))
		return nil
	})
	return out, err
}

// liveNodes returns the rows' nodes, which of returns of each row.
func liveNodes[R any](rows []R, of func(R) queries.LiveNode) []queries.Node {
	out := make([]queries.Node, len(rows))
	for i, r := range rows {
		out[i] = queries.Node(of(r))
	}
	return out
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
