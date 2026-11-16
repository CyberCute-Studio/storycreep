// Package comment keeps a project's threaded comments (PRD FR-COMMENT-01):
// what the project's members say of its nodes, connections, boards, and
// variables, as "does the lighthouse exist yet?", and the replies to it,
// which every member who reads the project sees. Register registers the
// comment as an entity of the pipeline's registry (package commands), whose
// revisions a client expects as it does any entity's (§5.7, ADR-006), with
// its commands, the one way that comments change; Service reads them.
//
// A thread is a first comment, its root, which names the thread's target,
// and the replies to it, one level deep; a thread is open or resolved, as its
// root holds it. A comment's body is a rich-text document (package richtext)
// of formatting and links alone, of at most MaxBodyLength characters of
// text: the commands refuse a value reference or a mention in it, so that a
// comment names nothing that the story reads, is never a usage of a variable
// or a component (FR-DEP-01), and never keeps one from being deleted, and @
// is left to the mentions of members (FR-COMMENT-02). Comments are no part
// of the story: the published format, the exports, the validation, and the
// writing statistics hold none of them (FR-COMMENT-01 AC3), though project
// search finds them (FR-PROJ-03, package search).
//
// Anyone whose role allows "Create/resolve comments" (§5.6), an owner, an
// admin, an editor, or a commenter of the project, starts a thread, replies
// to any, and resolves and unresolves any; only a comment's author edits it,
// deletes it, and restores it, whatever their role, and anyone else is
// refused for ErrNotAuthor, as everyone is once the author's account is
// deleted. Every command needs project:write of an API token (FR-AUTH-04),
// and an archived project takes none of them (FR-PROJ-01 AC2). Reading the
// comments is reading the project's content, which every role does.
//
// A comment deleted by its author goes to the project's trash, unlisted, from
// which only the undo of its deletion, a restore, brings it back, and which
// purges it once 30 days have passed (package trash), but for a root of which
// a reply is not deleted, or deleted for less, which its thread shows as
// deleted, without its body and its author, meanwhile: a thread is shown
// while one of its comments is not deleted. A thread is in the trash with its
// target too, and so with the board of a node, a connection, or a board's
// variable, and with a node of a connection, as the live views read them: the
// reads leave it out, as they read the live view of the comments,
// live_comment, and no command but a deletion or a restoration of one of its
// comments changes it, until the target comes back, and the thread with it,
// as it was (FR-COMMENT-01 AC2). Nothing writes the comments as their target
// goes to the trash or comes back, and the target's purge deletes them.
package comment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

// MaxBodyLength is the most characters of text that a comment's body may
// hold, as the writing statistics count a text's characters
// (richtext.CharacterCount): those that its author typed, without the line
// feeds, the bullets, and the numbers of its paragraphs and lists.
const MaxBodyLength = 10_000

// ErrNotAuthor is the reason for which a command of a comment that only its
// author may send, an edit, a deletion, or a restoration of it, refuses a
// batch that another user sends, or that anyone sends once the author's
// account is deleted (commands.Refuse).
var ErrNotAuthor = errors.New("comment: only the comment's author may change it")

// TargetType is the kind of the entity that a thread is on, its target.
type TargetType string

// The kinds of target.
const (
	Node       TargetType = "node"
	Connection TargetType = "connection"
	Board      TargetType = "board"
	Variable   TargetType = "variable"
)

// targets are the kinds of target, in the order of the enum of the API, with
// the kind of the IDs of each.
var targets = []struct {
	target TargetType
	ids    ids.Kind
}{{Node, ids.Node}, {Connection, ids.Connection}, {Board, ids.Board}, {Variable, ids.Variable}}

// TargetTypes returns the kinds of target, in the order of the enum of the
// API.
func TargetTypes() []TargetType {
	out := make([]TargetType, len(targets))
	for i, t := range targets {
		out[i] = t.target
	}
	return out
}

// Kind returns the kind of the IDs of the entities of the kind t, or zero for
// a kind that is not a target's.
func (t TargetType) Kind() ids.Kind {
	for _, c := range targets {
		if c.target == t {
			return c.ids
		}
	}
	return 0
}

// TargetOf returns the kind of target of the entities whose IDs are of the
// kind k, or "" if k is not a target's.
func TargetOf(k ids.Kind) TargetType {
	for _, c := range targets {
		if c.ids == k {
			return c.target
		}
	}
	return ""
}

// Status is whether a thread is open or resolved, which the list of a
// project's comments keeps, or "" for both.
type Status string

// The statuses of a thread.
const (
	Open     Status = "open"
	Resolved Status = "resolved"
)

// CommentAuthor is a user as a comment names them, its author or whoever
// resolved its thread, with the name that they show to other people, which a
// member who may not list the workspace's members, as a guest, sees too.
type CommentAuthor struct {
	ID          string `json:"id" doc:"The user's ID"`
	DisplayName string `json:"display_name" doc:"The user's display name"`
}

// CommentBody is a comment as the API shows it, and as an answer of
// revision_conflict shows it (commands.Entity.States): with its thread's
// target, and the target's board and node, by which a client opens it, as a
// result of the search names them, and its body and its author, which a
// deleted comment, as the root of a thread that is shown still, shows none
// of.
type CommentBody struct {
	ID         string     `json:"id" doc:"The comment's ID"`
	ProjectID  string     `json:"project_id" doc:"The ID of the comment's project"`
	ThreadID   string     `json:"thread_id" doc:"The ID of the comment's thread, its first comment's: the comment's own for a first comment, and its parent_id for a reply"`
	ParentID   string     `json:"parent_id,omitempty" doc:"The ID of the first comment of the thread that a reply replies to. A thread's first comment has none."`
	TargetType TargetType `json:"target_type" enum:"node,connection,board,variable" doc:"The kind of the thread's target, which every comment of the thread names: a node, a connection, a board, or a variable"`
	TargetID   string     `json:"target_id" doc:"The ID of the thread's target"`
	// BoardID and BoardName are those of a node, a connection, and a
	// variable of a board, and of a board itself, and none of a global
	// variable.
	BoardID   string  `json:"board_id,omitempty" doc:"The ID of the target's board: a node's, a connection's, or a board variable's, or the board itself. A global variable has none."`
	BoardName *string `json:"board_name,omitempty" doc:"The name of the target's board, which a comment has exactly when it has board_id"`
	// NodeID and NodeTitle are those of a node itself, and of the node that
	// a connection leaves.
	NodeID    string        `json:"node_id,omitempty" doc:"The ID of the node that a client opens the target by: a node's own, or the node that a connection leaves. A board and a variable have none."`
	NodeTitle *string       `json:"node_title,omitempty" doc:"The title of the target's node, which may be empty, and which a comment has exactly when it has node_id"`
	Body      *richtext.Doc `json:"body,omitempty" doc:"The comment's body, a rich-text document (PRD FR-TEXT-01) of paragraphs, lists, line breaks, and text that may be bold, italic, struck through, code, or a link, without value references or mentions. A deleted comment has none."`
	Deleted   bool          `json:"deleted" doc:"Whether the comment is deleted, which a list shows of the first comment of a thread whose replies are not all deleted, without its body and its author, as long as one of them is not"`
	// Author is nil for a deleted comment, and for one whose author's
	// account is deleted.
	Author    *CommentAuthor `json:"author,omitempty" doc:"The comment's author, the one user who may edit it or delete it. A deleted comment has none, and nor does one whose author's account is deleted, which nobody may change."`
	CreatedAt time.Time      `json:"created_at" doc:"When the comment was posted"`
	EditedAt  *time.Time     `json:"edited_at,omitempty" doc:"When the comment's body last changed after it was posted, if it ever did: its author's edit, or the undo of one"`
	UpdatedAt time.Time      `json:"updated_at" doc:"When the comment's body last changed, as it was posted or edited"`
	Rev       int64          `json:"rev" doc:"The comment's revision, which every change of it increments, its thread's resolution included, and which a change expects with expected_rev"`
	// Resolved, ResolvedAt, and ResolvedBy are a root's, which holds its
	// thread's resolution, and none of a reply.
	Resolved   *bool          `json:"resolved,omitempty" doc:"Whether the thread is resolved, which its first comment alone holds. A reply has none."`
	ResolvedAt *time.Time     `json:"resolved_at,omitempty" doc:"When the thread was resolved, if it is, which its first comment alone holds"`
	ResolvedBy *CommentAuthor `json:"resolved_by,omitempty" doc:"Who resolved the thread, if it is resolved, which its first comment alone holds, unless their account is deleted"`
	// DeletedAt is set while the comment is in the project's trash, which
	// only an answer of revision_conflict shows.
	DeletedAt *time.Time `json:"deleted_at,omitempty" doc:"When the comment went to the project's trash, if it is there, which only an answer of revision_conflict shows"`
	key       Key
}

// Key is where a comment is in the list of a project's comments
// (CommentBody.Key), by which a page of the list says where the next one
// starts: its thread's UUID, whether it is a reply, and its UUID. The zero
// Key is before every comment.
type Key struct {
	Thread ids.UUID
	Reply  bool
	ID     ids.UUID
}

// Key returns where c is in the list of its project's comments.
func (c CommentBody) Key() Key { return c.key }

// CommentCount is how many threads of each status a target has that are
// shown, as the canvas shows a node's, a connection's, or a board's, and the
// variables' panel a variable's.
type CommentCount struct {
	TargetType TargetType `json:"target_type" enum:"node,connection,board,variable" doc:"The kind of the target: a node, a connection, a board, or a variable"`
	TargetID   string     `json:"target_id" doc:"The target's ID"`
	Open       int64      `json:"open" doc:"How many of the target's threads are open"`
	Resolved   int64      `json:"resolved" doc:"How many of the target's threads are resolved"`
}

// CountKey is where a target is in the list of the counts of a project's
// threads, by which a page of the list says where the next one starts: its
// kind and its UUID. The zero CountKey is before every target.
type CountKey struct {
	Type TargetType
	ID   ids.UUID
}

// Key returns where c is in the list of the counts of its project's threads.
func (c CommentCount) Key() CountKey {
	// The ID is the service's encoding of the target's UUID, which decodes.
	u, _ := ids.Decode(c.TargetType.Kind(), c.TargetID)
	return CountKey{Type: c.TargetType, ID: u}
}

// Target is the entity that a thread is on: its kind, and its UUID.
type Target struct {
	Type TargetType
	ID   ids.UUID
}

// Filter is what the list of a project's comments keeps of its threads that
// are shown: each of its fields that is not zero keeps the threads that it
// says, and the zero Filter keeps them all.
type Filter struct {
	// Status keeps the threads that are open, or resolved.
	Status Status
	// Author keeps the threads in which the user wrote a comment that is not
	// deleted.
	Author ids.UUID
	// Board keeps the threads whose target is the board, or a node, a
	// connection, or a variable of it.
	Board ids.UUID
	// Target keeps the threads on the entity, a board's own and not its
	// nodes', if its Type is not "".
	Target Target
}

// commentRow is a comment as the reads of the API return it
// (queries.CommentsByID, queries.Comments).
type commentRow = queries.CommentsByIDRow

// bodyOf returns the comment r as the API shows it, or an error if its body
// is not a document that the commands store.
func bodyOf(r commentRow) (CommentBody, error) {
	kind := TargetType(r.TargetType).Kind()
	if kind == 0 {
		return CommentBody{}, fmt.Errorf("comment: the comment %s is on a target of the kind %q", ids.UUID(r.ID.Bytes),
			r.TargetType)
	}
	b := CommentBody{ID: ids.Encode(ids.Comment, r.ID.Bytes), ProjectID: ids.Encode(ids.Project, r.ProjectID.Bytes),
		ThreadID: ids.Encode(ids.Comment, r.ThreadID.Bytes), ParentID: encode(ids.Comment, r.ParentID),
		TargetType: TargetType(r.TargetType), TargetID: ids.Encode(kind, r.TargetID.Bytes),
		BoardID: encode(ids.Board, r.TargetBoardID), NodeID: encode(ids.Node, r.TargetNodeID),
		Deleted: r.DeletedAt.Valid, CreatedAt: r.CreatedAt.Time, EditedAt: timeOf(r.EditedAt),
		UpdatedAt: r.UpdatedAt.Time, Rev: r.Rev,
		key: Key{Thread: r.ThreadID.Bytes, Reply: r.ParentID.Valid, ID: r.ID.Bytes}}
	if r.TargetBoardID.Valid {
		b.BoardName = &r.BoardName.String
	}
	if r.TargetNodeID.Valid {
		b.NodeTitle = &r.NodeTitle.String
	}
	if !b.Deleted {
		doc, err := richtext.Parse(r.Body)
		if err != nil {
			return CommentBody{}, fmt.Errorf("comment: the body of the comment %s: %w", b.ID, err)
		}
		b.Body, b.Author = doc, authorOf(r.CreatedBy, r.AuthorName)
	}
	if !r.ParentID.Valid {
		resolved := r.ResolvedAt.Valid
		b.Resolved, b.ResolvedAt, b.ResolvedBy = &resolved, timeOf(r.ResolvedAt), authorOf(r.ResolvedBy, r.ResolverName)
	}
	return b, nil
}

// authorOf returns the user whose ID is the column id, with the display name
// name, or nil if id is null, as once their account is deleted.
func authorOf(id pgtype.UUID, name pgtype.Text) *CommentAuthor {
	if !id.Valid {
		return nil
	}
	return &CommentAuthor{ID: ids.Encode(ids.User, id.Bytes), DisplayName: name.String}
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
	// comments. It is required.
	Projects *project.Service
}

// Service reads the comments of the projects.
type Service struct {
	db       DB
	projects *project.Service
}

// New returns a Service that reads the comments in db, with opts. It panics
// if opts.Projects is nil.
func New(db DB, opts Options) *Service {
	if opts.Projects == nil {
		panic("comment: a Service needs the projects' service that authorises its reads")
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

// List returns at most limit of the comments of the project projectID's
// threads that are shown and that f keeps, in the order of their keys
// (Key): of their threads, newest first, and within a thread its first
// comment, then its replies, oldest first, each in the order of their IDs;
// the first, if after is zero, or else those after it. A thread is shown
// while one of its comments is not deleted and its target is not in the
// trash, nor with it, and of a thread it returns its first comment, deleted
// or not, as a deleted comment is shown (CommentBody.Deleted), and its
// replies that are not deleted. It returns the errors of project.Service.Get.
func (s *Service) List(ctx context.Context, actor commands.Actor, projectID ids.UUID, f Filter, after Key,
	limit int) ([]CommentBody, error) {
	if f.Status != "" && f.Status != Open && f.Status != Resolved {
		return nil, fmt.Errorf("comment: no status %q", f.Status)
	}
	if f.Target.Type != "" && f.Target.Type.Kind() == 0 {
		return nil, fmt.Errorf("comment: no kind of target %q", f.Target.Type)
	}
	out := []CommentBody{}
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		rows, err := q.Comments(ctx, queries.CommentsParams{WorkspaceID: workspaceID, ProjectID: nullUUID(projectID),
			Status: string(f.Status), Author: nullUUID(f.Author), Board: nullUUID(f.Board),
			TargetType: string(f.Target.Type), TargetID: nullUUID(f.Target.ID), After: after != (Key{}),
			AfterThread: pgtype.UUID{Bytes: after.Thread, Valid: true}, AfterReply: after.Reply,
			AfterID: pgtype.UUID{Bytes: after.ID, Valid: true}, MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("comment: list the comments: %w", err)
		}
		for _, r := range rows {
			b, err := bodyOf(commentRow(r))
			if err != nil {
				return err
			}
			out = append(out, b)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Counts returns at most limit of the targets of the project projectID's
// threads that are shown, as List shows them, each with how many of them are
// open and how many resolved, in the order of their keys (CountKey): of their
// kinds, then of their UUIDs; the first, if after is zero, or else those
// after it. Unless board is zero, it returns only the targets that List's
// Filter.Board keeps: the board itself, and its nodes, its connections, and
// its variables. It returns the errors of project.Service.Get.
func (s *Service) Counts(ctx context.Context, actor commands.Actor, projectID, board ids.UUID, after CountKey,
	limit int) ([]CommentCount, error) {
	if after.Type != "" && after.Type.Kind() == 0 {
		return nil, fmt.Errorf("comment: no kind of target %q", after.Type)
	}
	out := []CommentCount{}
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		rows, err := q.CommentCounts(ctx, queries.CommentCountsParams{WorkspaceID: workspaceID,
			ProjectID: nullUUID(projectID), Board: nullUUID(board), After: after.Type != "",
			AfterType: string(after.Type), AfterID: pgtype.UUID{Bytes: after.ID, Valid: true},
			MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("comment: count the threads: %w", err)
		}
		for _, r := range rows {
			t := TargetType(r.TargetType)
			if t.Kind() == 0 {
				return fmt.Errorf("comment: threads on a target of the kind %q", r.TargetType)
			}
			out = append(out, CommentCount{TargetType: t, TargetID: ids.Encode(t.Kind(), r.TargetID.Bytes),
				Open: r.Open, Resolved: r.Resolved})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Authors returns at most limit of the users who wrote a comment of the
// project projectID that is not deleted and whose thread is shown, as List
// shows it, each with their display name, in the order of their IDs: the
// first, if after is zero, or else those after the user after. It returns
// the errors of project.Service.Get.
func (s *Service) Authors(ctx context.Context, actor commands.Actor, projectID, after ids.UUID, limit int) (
	[]CommentAuthor, error) {
	out := []CommentAuthor{}
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		rows, err := q.CommentAuthors(ctx, queries.CommentAuthorsParams{WorkspaceID: workspaceID,
			ProjectID: nullUUID(projectID), After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("comment: list the comments' authors: %w", err)
		}
		for _, r := range rows {
			out = append(out, CommentAuthor{ID: ids.Encode(ids.User, r.ID.Bytes), DisplayName: r.DisplayName})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
