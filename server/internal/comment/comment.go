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
// of formatting, links, and mentions of members alone, of at most
// MaxBodyLength characters of text: the commands refuse a value reference or
// a mention of a component in it, so that a comment names nothing that the
// story reads, is never a usage of a variable or a component (FR-DEP-01),
// and never keeps one from being deleted, and @ is left to the mentions of
// members (FR-COMMENT-02), which only a comment's body holds of the
// project's texts (commands.Spec.MemberMentions).
//
// A comment mentions at most MaxMentions members, each of whom it may
// mention (Mentionable): a member of the project's workspace who reads the
// project, by their membership's role or a grant of it, a guest included,
// whose account is active, as the batch's transaction reads them; any other
// mention is refused for ErrMentionNoAccess, at its location (FR-COMMENT-02
// AC1). A comment notifies each member whom it mentions, but for its author,
// once, as it is posted or an edit adds the mention (package notification),
// and its deletion and its restoration tell its recipients' sessions that
// their notifications changed. An edit holds only the members whom it
// mentions anew to whether they may be mentioned, so that the comment of a
// member who has lost the project's access since keeps the mention; an undo
// of an edit that took out such a mention is refused as any edit that adds
// it would be. Comments are no part
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
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
)

// MaxBodyLength is the most characters of text that a comment's body may
// hold, as the writing statistics count a text's characters
// (richtext.CharacterCount): those that its author typed, without the line
// feeds, the bullets, and the numbers of its paragraphs and lists.
const MaxBodyLength = 10_000

// MaxMentions is the most members that a comment's body may mention, each
// counted once however often it mentions them.
const MaxMentions = 50

// ErrNotAuthor is the reason for which a command of a comment that only its
// author may send, an edit, a deletion, or a restoration of it, refuses a
// batch that another user sends, or that anyone sends once the author's
// account is deleted (commands.Refuse).
var ErrNotAuthor = errors.New("comment: only the comment's author may change it")

// ErrMentionNoAccess is the reason for which a comment's create or edit
// refuses a batch whose body mentions a user whom the comment may not mention
// (Mentionable), at the mention's location (commands.Refuse), which the API
// answers with mention_no_access (PRD FR-COMMENT-02 AC1).
var ErrMentionNoAccess = errors.New("comment: the mentioned user cannot read the project")

// readers are the roles of a membership that read a project without a grant,
// and those of a grant that read it (role.Readers), as the queries take them.
var memberRoles, grantRoles = func() ([]string, []string) {
	ms, gs := role.Readers()
	strs := func(rs []role.Role) []string {
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = string(r)
		}
		return out
	}
	return strs(ms), strs(gs)
}()

// Mentionable reports whether a comment of a project may mention a user, of
// whom status is the account's status, membership the role of their
// membership of the project's workspace, role.None if they are not a member,
// and grant the role of the membership's grant on the project, role.None if
// it has none: whether the account is active and the user reads the project,
// their effective role on it not none (PRD §5.5), whatever their role, a
// viewer's or a guest's with a grant too. An instance admin's access to every
// project (role.Policy.InstanceAdminAccess) is no membership, which makes
// nobody mentionable, so the role is resolved as for one who is not an
// instance admin. MentionableMembers lists the users of whom it reports
// true, by role.Readers, which agrees with it.
func Mentionable(status string, membership, grant role.Role) bool {
	return status == "active" && (role.Policy{}).Effective(false, membership, grant) != role.None
}

// mentionable returns, of each of users, whether a comment of the project
// projectID, of the workspace workspaceID, may mention them (Mentionable),
// as q's transaction reads them, false for a user who has no account.
func mentionable(ctx context.Context, q *queries.Queries, workspaceID, projectID ids.UUID, users []ids.UUID) (
	map[ids.UUID]bool, error) {
	rows, err := q.MentionAccess(ctx, queries.MentionAccessParams{WorkspaceID: nullUUID(workspaceID),
		ProjectID: nullUUID(projectID), UserIds: uuids(users)})
	if err != nil {
		return nil, fmt.Errorf("comment: read the mentioned users' access: %w", err)
	}
	out := make(map[ids.UUID]bool, len(rows))
	for _, r := range rows {
		membership, grant := role.None, role.None
		if r.MembershipRole.Valid {
			membership = role.Role(r.MembershipRole.String)
		}
		if r.GrantRole.Valid {
			grant = role.Role(r.GrantRole.String)
		}
		out[r.ID.Bytes] = Mentionable(r.Status, membership, grant)
	}
	return out, nil
}

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
	Body      *richtext.Doc `json:"body,omitempty" doc:"The comment's body, a rich-text document (PRD FR-TEXT-01) of paragraphs, lists, line breaks, mentions of members (memberMention), and text that may be bold, italic, struck through, code, or a link, without value references or mentions of components. A deleted comment has none."`
	// Mentions are the members that Body mentions, each once, in the order
	// of their first mentions, and none of a deleted comment.
	Mentions []CommentMention `json:"mentions,omitempty" doc:"The members that the comment's body mentions, each once, in the order of their first mentions, with their display names as they are now, by which a client shows each mention as @ and the name (PRD FR-COMMENT-02). A comment whose body mentions nobody, and a deleted comment, have none."`
	Deleted  bool             `json:"deleted" doc:"Whether the comment is deleted, which a list shows of the first comment of a thread whose replies are not all deleted, without its body and its author, as long as one of them is not"`
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

// CommentMention is a member that a comment's body mentions, as the API
// shows it: their ID, and their display name, unless their account is
// deleted, which a client shows in words of its own, as a former member.
type CommentMention struct {
	ID          string `json:"id" doc:"The user's ID, as the mention's attrs.id spells it canonically"`
	DisplayName string `json:"display_name,omitempty" doc:"The user's display name as it is now. A user whose account is deleted has none."`
}

// MentionableMember is a user whom a comment of a project may mention
// (Mentionable), as the list of them shows them, by which a client offers
// whom to mention.
type MentionableMember struct {
	ID          string `json:"id" doc:"The user's ID"`
	DisplayName string `json:"display_name" doc:"The user's display name"`
}

// Key is where a comment is in the list of a project's comments
// (CommentBody.Key), by which a page of the list says where the next one
// starts: when its thread was started, as the server's clock has it, and the
// thread's UUID, whether it is a reply, and when it was posted, and its
// UUID. The zero Key is before every comment.
type Key struct {
	ThreadAt time.Time
	Thread   ids.UUID
	Reply    bool
	At       time.Time
	ID       ids.UUID
}

// IsZero reports whether k is the zero Key, before every comment.
func (k Key) IsZero() bool {
	return k.ThreadAt.IsZero() && k.Thread == ids.UUID{} && !k.Reply && k.At.IsZero() && k.ID == ids.UUID{}
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
		key: Key{ThreadAt: r.ThreadAt.Time, Thread: r.ThreadID.Bytes, Reply: r.ParentID.Valid, At: r.CreatedAt.Time,
			ID: r.ID.Bytes}}
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
// (Key): of their threads, newest first, by when each was started, and
// within a thread its first comment, then its replies, oldest first, by
// when each was posted, as the server's clock has it rather than the UUIDv7
// that a client made, which a client's clock that is off, or a client that
// made its ID for a time to come, would order otherwise, and in the order of
// their IDs where those times are the same; the first, if after is zero, or
// else those after it. A thread is shown
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
			TargetType: string(f.Target.Type), TargetID: nullUUID(f.Target.ID), After: !after.IsZero(),
			AfterThreadAt: pgtype.Timestamptz{Time: after.ThreadAt, Valid: true},
			AfterThread:   pgtype.UUID{Bytes: after.Thread, Valid: true}, AfterReply: after.Reply,
			AfterAt: pgtype.Timestamptz{Time: after.At, Valid: true}, AfterID: pgtype.UUID{Bytes: after.ID, Valid: true},
			MaxRows: int32(min(limit, 1<<20))})
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
		return withMentions(ctx, q, out)
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

// Members returns at most limit of the users whom a comment of the project
// projectID may mention (Mentionable), each with their display name, in the
// order of their IDs: the first, if after is zero, or else those after the
// user after, as the members' list by which a client offers whom to mention
// (PRD FR-COMMENT-02). Reading it is reading the project, which every role
// does. It returns the errors of project.Service.Get.
func (s *Service) Members(ctx context.Context, actor commands.Actor, projectID, after ids.UUID, limit int) (
	[]MentionableMember, error) {
	out := []MentionableMember{}
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		rows, err := q.MentionableMembers(ctx, queries.MentionableMembersParams{WorkspaceID: workspaceID,
			ProjectID: nullUUID(projectID), GrantRoles: grantRoles, MemberRoles: memberRoles, After: nullUUID(after),
			MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("comment: list the members whom a comment may mention: %w", err)
		}
		for _, r := range rows {
			out = append(out, MentionableMember{ID: ids.Encode(ids.User, r.ID.Bytes), DisplayName: r.DisplayName})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// withMentions sets the mentions of each of bodies that has a body to the
// members that the body mentions (CommentBody.Mentions), with their display
// names, read in one query in q's transaction, as they are now.
func withMentions(ctx context.Context, q *queries.Queries, bodies []CommentBody) error {
	var users []pgtype.UUID
	seen := map[string]bool{}
	for i := range bodies {
		if bodies[i].Body == nil {
			continue
		}
		mentioned := map[string]bool{}
		for _, m := range bodies[i].Body.Members() {
			if mentioned[m.User] {
				continue
			}
			mentioned[m.User] = true
			bodies[i].Mentions = append(bodies[i].Mentions, CommentMention{ID: m.User})
			if !seen[m.User] {
				seen[m.User] = true
				// Parse took the ID for a user's.
				u, _ := ids.Decode(ids.User, m.User)
				users = append(users, nullUUID(u))
			}
		}
	}
	if len(users) == 0 {
		return nil
	}
	rows, err := q.MentionedUsers(ctx, users)
	if err != nil {
		return fmt.Errorf("comment: read the names of the mentioned members: %w", err)
	}
	names := make(map[string]string, len(rows))
	for _, r := range rows {
		names[ids.Encode(ids.User, r.ID.Bytes)] = r.DisplayName
	}
	for i := range bodies {
		for j, m := range bodies[i].Mentions {
			bodies[i].Mentions[j].DisplayName = names[m.ID]
		}
	}
	return nil
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
