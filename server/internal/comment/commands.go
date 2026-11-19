package comment

import (
	"context"
	"encoding/json"
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
	"github.com/CyberCute-Studio/storycreep/server/internal/notification"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// The types of command of a comment (PRD §5.7). Each names the comment that
// it is about by its ID, id, and applies to the comment of the batch's
// project.
const (
	// Create posts a comment, whose ID the client makes, as it makes the
	// command's, so that it can refer to the comment before the server
	// answers: the first comment of a thread on its target, or a reply to
	// one. Deleting it to the trash undoes it.
	Create commands.Type = "comment.create"
	// Edit changes a comment's body, which only its author does. Changing it
	// back undoes it.
	Edit commands.Type = "comment.edit"
	// Delete deletes a comment to the project's trash, unlisted, and Restore
	// restores it, which only its author does. Each undoes the other.
	Delete  commands.Type = "comment.delete"
	Restore commands.Type = "comment.restore"
	// Resolve resolves a thread, by its first comment, and Unresolve opens
	// it again. Each undoes the other.
	Resolve   commands.Type = "comment.resolve"
	Unresolve commands.Type = "comment.unresolve"
)

// createPayload is Create's payload.
type createPayload struct {
	ID       string       `json:"id" doc:"The new comment's ID, a cmt_ ID of a UUIDv7 that the client makes, which no entity of the instance has (PRD §5.2)"`
	Target   string       `json:"target,omitempty" doc:"The ID of the entity that the thread that the comment starts is on, a node, a connection, a board, or a variable of the project, as its prefix says: nd_, cn_, bd_, or var_. A reply has none, and a comment that has neither target nor parent_id, or both, is answered with 422 validation_failed, as one whose target the project does not have, or is in its trash, or is there with what is, as a node of a board in the trash."`
	ParentID string       `json:"parent_id,omitempty" doc:"The ID of the first comment of the thread that the comment replies to. A thread is one level deep, so a reply to a reply is answered with 422 validation_failed, as one to a comment that the project does not have, or of a thread that is not shown: whose comments are all deleted, or whose target is in the project's trash. The first comment of a thread has none."`
	Body     richtext.Doc `json:"body" doc:"The comment's body, a rich-text document (PRD FR-TEXT-01) of paragraphs, lists, line breaks, mentions of members of the project (memberMention), and text that may be bold, italic, struck through, code, or a link, of at least one character that is not blank, as white space and U+200B ZERO WIDTH SPACE are, or a mention, and of at most 10,000 characters of text, those of its texts, without the line feeds, the bullets, and the numbers of its paragraphs and lists, mentioning at most 50 members. One that is not, or that holds a value reference or a mention of a component, is answered with 422 validation_failed, a value reference, a mention of a component, or the mention of a 51st member at its location in the document's canonical form, as payload.body.content[0].content[1]. Each member that it mentions is notified of it (PRD FR-COMMENT-02): a mention of a user who cannot read the project, who is not a member of its workspace, a guest without a grant of the project, or whose account is not active, is answered with 422 mention_no_access at the mention's location, which does not name the user."`
}

// editPayload is Edit's payload.
type editPayload struct {
	ID   string       `json:"id" doc:"The comment's ID: one of the user's own, which is not deleted, and of a thread whose target is not in the project's trash. Another user's comment, or one whose author's account is deleted, is answered with 403 author_required."`
	Body richtext.Doc `json:"body" doc:"The comment's new body, a rich-text document (PRD FR-TEXT-01) as a new comment's is. A mention of a member that the comment's body does not mention yet is held to whether the member may be mentioned, as a new comment's, and notifies the member, once: one that the comment mentions already is not, so that the comment of a member who has since lost the project's access keeps the mention. A mention that an edit takes out leaves its notification as it was."`
}

// ownPayload is the payload of a command of a comment that only its author
// may send, which needs nothing but its ID.
type ownPayload struct {
	ID string `json:"id" doc:"The comment's ID: one of the user's own. Another user's comment, or one whose author's account is deleted, is answered with 403 author_required."`
}

// threadPayload is the payload of a command of a thread, which names its
// first comment.
type threadPayload struct {
	ID string `json:"id" doc:"The ID of the thread's first comment, of a thread that is shown, one of whose comments is not deleted, and whose target is not in the project's trash. A reply, whose thread its first comment names, is answered with 422 validation_failed."`
}

// Register registers in r the comment as an entity of the pipeline, whose
// revisions a command expects as it does any entity's, and its commands,
// each of which changes the project's draft, as far as an API token and the
// archive are concerned, so that an API token needs project:write for it
// (FR-AUTH-04), and its role "Create/resolve comments" (§5.6), and a
// command that changes a comment itself rather than its thread its author
// too, which its Apply checks (ErrNotAuthor).
func Register(r *commands.Registry) {
	commands.RegisterEntity(r, commands.Entity{
		Kind: ids.Comment,
		Revs: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]int64, error) {
			rows, err := queries.New(tx).CommentRevs(ctx, queries.CommentRevsParams{
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
			rows, err := queries.New(tx).CommentsByID(ctx, queries.CommentsByIDParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			bodies := make([]CommentBody, len(rows))
			for i, row := range rows {
				b, err := bodyOf(row)
				if err != nil {
					return nil, err
				}
				b.DeletedAt = timeOf(row.DeletedAt)
				bodies[i] = b
			}
			if err := withMentions(ctx, queries.New(tx), bodies); err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]any, len(rows))
			for i, row := range rows {
				out[row.ID.Bytes] = bodies[i]
			}
			return out, nil
		},
	})

	comment := []ids.Kind{ids.Comment}
	op, sc := authz.Comment, scope.ProjectWrite

	commands.Register(r, commands.Spec[createPayload]{
		Type: Create, Capability: op, Scope: sc, Touches: comment, MemberMentions: true,
		Check: checkCreate,
		// The step of an inverse never creates a comment, but the body's
		// rules belong with Edit's, whose step writes back the stored body
		// (commands.Spec.CheckNew).
		CheckNew: func(p createPayload) input.FieldErrors { return checkBody(p.Body) },
		Inverse: func(_ context.Context, _ *commands.Tx, p createPayload) ([]commands.Step, error) {
			return []commands.Step{{Type: Delete, Payload: ownPayload{ID: p.ID}}}, nil
		},
		Apply: applyCreate,
	})

	commands.Register(r, commands.Spec[editPayload]{
		Type: Edit, Capability: op, Scope: sc, Touches: comment, MemberMentions: true,
		Check: func(p editPayload) input.FieldErrors { return entityID(p.ID) },
		// The step of the inverse writes back the stored body, which the
		// rules of a body may refuse once they are stricter
		// (commands.Spec.CheckNew).
		CheckNew: func(p editPayload) input.FieldErrors { return checkBody(p.Body) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p editPayload) ([]commands.Step, error) {
			c, err := editable(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			body, err := richtext.Parse(c.Body)
			if err != nil {
				return nil, fmt.Errorf("comment: the body of the comment %s: %w", p.ID, err)
			}
			return []commands.Step{{Type: Edit, Payload: editPayload{ID: p.ID, Body: *body}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p editPayload) error {
			c, err := editable(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			before, err := richtext.Parse(c.Body)
			if err != nil {
				return fmt.Errorf("comment: the body of the comment %s: %w", p.ID, err)
			}
			// Only the members whom the edit mentions anew are held to whether
			// they may be mentioned, so that the comment of a member who has
			// lost the project's access since keeps the mention, and they are
			// notified, each once (notification.Mentioned).
			added, err := checkMentions(ctx, tx, p.Body, before)
			if err != nil {
				return err
			}
			body, err := json.Marshal(p.Body)
			if err != nil {
				return fmt.Errorf("comment: encode the body: %w", err)
			}
			if err := changed(tx, c.ID)(queries.New(tx).EditComment(ctx, queries.EditCommentParams{Body: body,
				WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID})); err != nil {
				return err
			}
			// The preview of each of the comment's notifications is of its body
			// as it is now, which its recipients' sessions are told of, those
			// whom it mentions anew too.
			return notification.CommentEdited(ctx, tx, tx.WorkspaceID(), tx.ProjectID(), c.ID.Bytes,
				tx.Actor().UserID, added)
		},
	})

	commands.Register(r, commands.Spec[ownPayload]{
		Type: Delete, Capability: op, Scope: sc, Touches: comment,
		Check: func(p ownPayload) input.FieldErrors { return entityID(p.ID) },
		// Deleting a comment that is in the trash already does nothing, and
		// its undo nothing either. A comment of a thread whose target is in
		// the trash is deleted all the same, as it is restored.
		Inverse: func(ctx context.Context, tx *commands.Tx, p ownPayload) ([]commands.Step, error) {
			c, err := authored(ctx, tx, p.ID)
			if err != nil || c.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Restore, Payload: ownPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p ownPayload) error {
			c, err := authored(ctx, tx, p.ID)
			if err != nil || c.DeletedAt.Valid {
				return err
			}
			if err := changed(tx, c.ID)(queries.New(tx).TrashComment(ctx, queries.TrashCommentParams{
				WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID})); err != nil {
				return err
			}
			// The comment's notifications are hidden while it is in the trash,
			// which its recipients' sessions are told of.
			return notification.CommentChanged(ctx, tx, tx.WorkspaceID(), tx.ProjectID(), c.ID.Bytes)
		},
	})

	commands.Register(r, commands.Spec[ownPayload]{
		Type: Restore, Capability: op, Scope: sc, Touches: comment,
		Check: func(p ownPayload) input.FieldErrors { return entityID(p.ID) },
		// Restoring a comment that is not in the trash does nothing, and its
		// undo nothing either.
		Inverse: func(ctx context.Context, tx *commands.Tx, p ownPayload) ([]commands.Step, error) {
			c, err := authored(ctx, tx, p.ID)
			if err != nil || !c.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Delete, Payload: ownPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p ownPayload) error {
			c, err := authored(ctx, tx, p.ID)
			if err != nil || !c.DeletedAt.Valid {
				return err
			}
			if err := changed(tx, c.ID)(queries.New(tx).RestoreComment(ctx, queries.RestoreCommentParams{
				WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID})); err != nil {
				return err
			}
			// The comment's notifications are shown again, as they were, which
			// its recipients' sessions are told of.
			return notification.CommentChanged(ctx, tx, tx.WorkspaceID(), tx.ProjectID(), c.ID.Bytes)
		},
	})

	commands.Register(r, commands.Spec[threadPayload]{
		Type: Resolve, Capability: op, Scope: sc, Touches: comment,
		Check: func(p threadPayload) input.FieldErrors { return entityID(p.ID) },
		// Resolving a thread that is resolved already does nothing, and its
		// undo nothing either.
		Inverse: func(ctx context.Context, tx *commands.Tx, p threadPayload) ([]commands.Step, error) {
			c, err := thread(ctx, tx, p.ID)
			if err != nil || c.ResolvedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Unresolve, Payload: threadPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p threadPayload) error {
			c, err := thread(ctx, tx, p.ID)
			if err != nil || c.ResolvedAt.Valid {
				return err
			}
			return changed(tx, c.ID)(queries.New(tx).ResolveComment(ctx, queries.ResolveCommentParams{
				ResolvedBy: by(tx), WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID}))
		},
	})

	commands.Register(r, commands.Spec[threadPayload]{
		Type: Unresolve, Capability: op, Scope: sc, Touches: comment,
		Check: func(p threadPayload) input.FieldErrors { return entityID(p.ID) },
		// Opening a thread that is open already does nothing, and its undo
		// nothing either.
		Inverse: func(ctx context.Context, tx *commands.Tx, p threadPayload) ([]commands.Step, error) {
			c, err := thread(ctx, tx, p.ID)
			if err != nil || !c.ResolvedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Resolve, Payload: threadPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p threadPayload) error {
			c, err := thread(ctx, tx, p.ID)
			if err != nil || !c.ResolvedAt.Valid {
				return err
			}
			return changed(tx, c.ID)(queries.New(tx).UnresolveComment(ctx, queries.UnresolveCommentParams{
				WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID}))
		},
	})
}

// applyCreate is Create's Apply.
func applyCreate(ctx context.Context, tx *commands.Tx, p createPayload) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	id := decoded(ids.Comment, p.ID)
	// A comment of the project with the ID is refused for its ID, so that a
	// create sent again with a new command ID, as an offline client's
	// keep-mine sends it (FR-OFF-04), is refused for its ID; the insert below
	// refuses the ID of a comment that this transaction does not see.
	if _, err := q.CommentAny(ctx, queries.CommentAnyParams{WorkspaceID: ws, ProjectID: project, ID: id}); err == nil {
		return refuseTakenID()
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("comment: read the comment: %w", err)
	}
	row := queries.InsertCommentParams{ID: id, WorkspaceID: ws, ProjectID: project, CreatedBy: by(tx)}
	if p.Target != "" {
		// Check took the target's kind.
		kind, u, _ := ids.DecodeAny(p.Target)
		switch kind {
		case ids.Node:
			row.NodeID = nullUUID(u)
		case ids.Connection:
			row.ConnectionID = nullUUID(u)
		case ids.Board:
			row.BoardID = nullUUID(u)
		case ids.Variable:
			row.VariableID = nullUUID(u)
		}
		t, err := q.CommentTarget(ctx, queries.CommentTargetParams{WorkspaceID: ws, ProjectID: project,
			NodeID: row.NodeID, ConnectionID: row.ConnectionID, BoardID: row.BoardID, VariableID: row.VariableID})
		switch {
		case err != nil:
			return fmt.Errorf("comment: read the target: %w", err)
		case !t.Found:
			return commands.Refuse("target", "is not a node, a connection, a board, or a variable of the project",
				nil)
		case !t.Live:
			return commands.Refuse("target", "is in the project's trash, or there with what is, as a node with its "+
				"board, which no thread is started on until it is restored", nil)
		}
	} else {
		parent, err := q.CommentAny(ctx, queries.CommentAnyParams{WorkspaceID: ws, ProjectID: project,
			ID: decoded(ids.Comment, p.ParentID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return commands.Refuse("parent_id", "is not a comment of the project", nil)
		case err != nil:
			return fmt.Errorf("comment: read the parent: %w", err)
		case parent.Comment.ParentID.Valid:
			return commands.Refuse("parent_id", "is a reply, which nothing replies to: a reply's parent is the "+
				"first comment of its thread", nil)
		}
		if err := shown("parent_id", parent); err != nil {
			return err
		}
		row.ParentID = parent.Comment.ID
	}
	mentioned, err := checkMentions(ctx, tx, p.Body, nil)
	if err != nil {
		return err
	}
	body, err := json.Marshal(p.Body)
	if err != nil {
		return fmt.Errorf("comment: encode the body: %w", err)
	}
	row.Body = body
	err = q.InsertComment(ctx, row)
	if database.UniqueViolation(err, "comment_pkey") {
		// The ID is another project's, or another workspace's, whose rows
		// this transaction does not see.
		return refuseTakenID()
	}
	if err != nil {
		return fmt.Errorf("comment: add the comment: %w", err)
	}
	if err := tx.Changed(ids.Comment, id.Bytes, 1); err != nil {
		return err
	}
	return notification.Mentioned(ctx, tx, tx.WorkspaceID(), tx.ProjectID(), id.Bytes, tx.Actor().UserID, mentioned)
}

// checkMentions returns the members whom body, a comment's new body, mentions
// and before, its body before the command, or nil for a new comment, does
// not, each once, in the order of their first mentions, once it has checked
// that the comment may mention each of them (mentionable), as of the batch's
// transaction, or refuses the batch at the location of the first mention of
// one that it may not, for ErrMentionNoAccess, without naming the member, as
// the client knows whom it mentioned (PRD FR-COMMENT-02 AC1). A user who has
// no account is refused alike, so that the refusal does not tell whether the
// account exists.
func checkMentions(ctx context.Context, tx *commands.Tx, body richtext.Doc, before *richtext.Doc) ([]ids.UUID,
	error) {
	known := map[string]bool{}
	if before != nil {
		for _, m := range before.Members() {
			known[m.User] = true
		}
	}
	var users []ids.UUID
	var at []string
	for loc, m := range body.Members() {
		if known[m.User] {
			continue
		}
		known[m.User] = true
		// Parse took the ID for a user's.
		u, _ := ids.Decode(ids.User, m.User)
		users, at = append(users, u), append(at, loc)
	}
	if len(users) == 0 {
		return nil, nil
	}
	access, err := mentionable(ctx, queries.New(tx), tx.WorkspaceID(), tx.ProjectID(), users)
	if err != nil {
		return nil, err
	}
	for i, u := range users {
		if !access[u] {
			return nil, commands.Refuse("body"+at[i], "mentions a user who cannot read the project, as one who is "+
				"not a member of its workspace, a guest without a grant of it, or one whose account is not active, "+
				"whom a comment cannot mention", ErrMentionNoAccess)
		}
	}
	return users, nil
}

// refuseTakenID returns the refusal of a create whose ID a comment has
// already.
func refuseTakenID() error {
	return commands.Refuse("id", "is the ID of a comment that exists already", nil)
}

// checkCreate returns the problems of a create's payload p that its schema
// cannot say: an ID that is not a comment's of a UUIDv7, neither a target
// nor a parent, or both, a target that is not the ID of a node, a
// connection, a board, or a variable, and a parent that is not a comment's
// ID. It reads nothing but p.
func checkCreate(p createPayload) input.FieldErrors {
	errs := newID(p.ID)
	switch {
	case p.Target == "" && p.ParentID == "":
		errs = append(errs, field("target", "is missing, as is parent_id: a comment starts a thread on its target, "+
			"or replies to one")...)
	case p.Target != "" && p.ParentID != "":
		errs = append(errs, field("parent_id", "is set beside target: a thread's first comment names its target, "+
			"and a reply its parent, whose thread's target is its own")...)
	case p.Target != "":
		if kind, _, err := ids.DecodeAny(p.Target); err != nil || TargetOf(kind) == "" {
			errs = append(errs, field("target", "is not the ID of a node, a connection, a board, or a variable")...)
		}
	default:
		if _, err := ids.Decode(ids.Comment, p.ParentID); err != nil {
			errs = append(errs, field("parent_id", fmt.Sprintf("is not a %s's ID: %v", ids.Comment, err))...)
		}
	}
	return errs
}

// checkBody returns the problems of body, the body of a comment that a
// command writes, which a stored body that the step of an inverse writes
// back may break: a value reference or a mention of a component, at its
// location in the document, of which a comment holds none, so that it names
// nothing that the story reads, and @ is left to the mentions of members
// (PRD FR-COMMENT-02); mentions of more than MaxMentions members, at the
// first mention of the first member past them; and, of a body without them,
// more than MaxBodyLength characters of text, or none that is printed, as
// one of white space alone, or of a list whose items are empty, both counted
// as the characters of its texts, which CharacterCount counts, without the
// bullets and the numbers that its lists show, which its author did not
// type, and without its mentions of members, which are no text of it
// (richtext.Member), unless it mentions one, which is all that a comment of
// @Tomas alone says. The location of a node is in the document's canonical
// form (richtext.Parse), whose neighbouring texts with the same marks are
// one, as an editor writes them. It reads nothing but body.
func checkBody(body richtext.Doc) input.FieldErrors {
	if errs := foreign(body.Blocks, "body"); len(errs) > 0 {
		return errs
	}
	members := map[string]bool{}
	for loc, m := range body.Members() {
		if members[m.User] {
			continue
		}
		if len(members) == MaxMentions {
			return field("body"+loc, fmt.Sprintf("mentions a member past the %d whom a comment may mention",
				MaxMentions))
		}
		members[m.User] = true
	}
	segs := body.Segments(richtext.NoNames{})
	if n := richtext.CharacterCount(segs); n > MaxBodyLength {
		return field("body", fmt.Sprintf("holds %d characters of text, more than the %d that a comment may hold",
			n, MaxBodyLength))
	}
	if !printed(segs) && len(members) == 0 {
		return field("body", "is empty: a comment holds at least one character that is not blank, as white space and U+200B "+
			"ZERO WIDTH SPACE are, or a mention of a member")
	}
	return nil
}

// printed reports whether a character of the texts of segs, the segments of
// a body without value references and mentions, is printed (input.Printed),
// rather than one of the bullets and the numbers that richtext.PlainText
// writes of its lists' items, so that a list of empty items is no text.
func printed(segs []richtext.Segment) bool {
	for _, s := range segs {
		if s.Kind == richtext.SegmentText && strings.IndexFunc(s.Value, input.Printed) >= 0 {
			return true
		}
	}
	return false
}

// foreign returns the problems of the value references and the mentions of
// components of blocks, the blocks of a document or of a list item at the
// location at, as body.content[1].content[0], each at its own location. A
// mention of a member is a comment's to hold (checkBody).
func foreign(blocks []richtext.Block, at string) input.FieldErrors {
	var errs input.FieldErrors
	for i, b := range blocks {
		loc := fmt.Sprintf("%s.content[%d]", at, i)
		switch b := b.(type) {
		case *richtext.Paragraph:
			for j, in := range b.Inlines {
				switch in.(type) {
				case *richtext.Value:
					errs = append(errs, input.FieldError{Field: fmt.Sprintf("%s.content[%d]", loc, j),
						Message: "is a value reference, which a comment does not hold: a comment shows no value of " +
							"the story's"})
				case *richtext.Mention:
					errs = append(errs, input.FieldError{Field: fmt.Sprintf("%s.content[%d]", loc, j),
						Message: "is a mention of a component, which a comment does not hold: a comment names " +
							"nothing of the story's"})
				}
			}
		case *richtext.List:
			for k, item := range b.Items {
				errs = append(errs, foreign(item.Blocks, fmt.Sprintf("%s.content[%d]", loc, k))...)
			}
		}
	}
	return errs
}

// anyComment returns the comment of the batch's project whose ID is id,
// whether or not it is deleted, with whether its thread's target is outside
// the trash and one of its thread's comments is not deleted, or refuses the
// batch if the project has none.
func anyComment(ctx context.Context, tx *commands.Tx, id string) (queries.CommentAnyRow, error) {
	c, err := queries.New(tx).CommentAny(ctx, queries.CommentAnyParams{WorkspaceID: nullUUID(tx.WorkspaceID()),
		ProjectID: nullUUID(tx.ProjectID()), ID: decoded(ids.Comment, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.CommentAnyRow{}, commands.Refuse("id", "is not a comment of the project", nil)
	case err != nil:
		return queries.CommentAnyRow{}, fmt.Errorf("comment: read the comment: %w", err)
	}
	return c, nil
}

// authored returns the comment of the batch's project whose ID is id, but
// for what anyComment reads of its thread, or refuses the batch as
// authoredRow does.
func authored(ctx context.Context, tx *commands.Tx, id string) (queries.Comment, error) {
	c, err := authoredRow(ctx, tx, id)
	return c.Comment, err
}

// authoredRow returns the comment of the batch's project whose ID is id as
// anyComment does, or refuses the batch for ErrNotAuthor unless the batch's
// actor wrote it, as nobody did once the author's account is deleted.
func authoredRow(ctx context.Context, tx *commands.Tx, id string) (queries.CommentAnyRow, error) {
	c, err := anyComment(ctx, tx, id)
	switch {
	case err != nil:
		return queries.CommentAnyRow{}, err
	case !c.Comment.CreatedBy.Valid:
		return queries.CommentAnyRow{}, commands.Refuse("id", "is a comment whose author's account is deleted, "+
			"which nobody changes", ErrNotAuthor)
	case ids.UUID(c.Comment.CreatedBy.Bytes) != tx.Actor().UserID:
		return queries.CommentAnyRow{}, commands.Refuse("id", "is another user's comment, which only its author "+
			"edits, deletes, or restores", ErrNotAuthor)
	}
	return c, nil
}

// editable returns the comment of the batch's project whose ID is id as
// authored does, or refuses the batch if it is deleted, or its thread's
// target is in the trash, where nothing but a deletion or a restoration
// changes it.
func editable(ctx context.Context, tx *commands.Tx, id string) (queries.Comment, error) {
	c, err := authoredRow(ctx, tx, id)
	switch {
	case err != nil:
		return queries.Comment{}, err
	case c.Comment.DeletedAt.Valid:
		return queries.Comment{}, commands.Refuse("id", "is a deleted comment, in the project's trash, which only "+
			"restoring it changes", nil)
	case !c.TargetLive:
		return queries.Comment{}, refuseTrashedTarget("id")
	}
	return c.Comment, nil
}

// thread returns the comment of the batch's project whose ID is id, the
// first comment of a thread, as anyComment does, or refuses the batch if it
// is a reply, or its thread is not shown (shown).
func thread(ctx context.Context, tx *commands.Tx, id string) (queries.Comment, error) {
	c, err := anyComment(ctx, tx, id)
	switch {
	case err != nil:
		return queries.Comment{}, err
	case c.Comment.ParentID.Valid:
		return queries.Comment{}, commands.Refuse("id", "is a reply, whose thread its first comment resolves", nil)
	}
	if err := shown("id", c); err != nil {
		return queries.Comment{}, err
	}
	return c.Comment, nil
}

// shown refuses the batch at the payload's field, which names the first
// comment of the thread of c, unless the thread is shown: one of its
// comments is not deleted, and its target is not in the trash.
func shown(field string, c queries.CommentAnyRow) error {
	switch {
	case !c.ThreadKept:
		return commands.Refuse(field, "is the first comment of a thread whose comments are all deleted, which is "+
			"no longer shown", nil)
	case !c.TargetLive:
		return refuseTrashedTarget(field)
	}
	return nil
}

// refuseTrashedTarget returns the refusal of a command at the payload's
// field, which names a comment of a thread whose target is in the trash.
func refuseTrashedTarget(field string) error {
	return commands.Refuse(field, "is a comment of a thread whose target is in the project's trash, or there with "+
		"what is, which no command but a comment's deletion or restoration changes until the target is restored",
		nil)
}

// changed returns a function that reports the change of the comment id, to
// the revision that an update returned, with err, or reports nothing if the
// update found nothing to change, which it returns as pgx.ErrNoRows.
func changed(tx *commands.Tx, id pgtype.UUID) func(rev int64, err error) error {
	return func(rev int64, err error) error {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("comment: change the comment: %w", err)
		}
		return tx.Changed(ids.Comment, id.Bytes, rev)
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

// newID returns the problem of id, the ID of the comment that a create
// makes, unless it is a comment's ID of a UUIDv7 (PRD §5.2).
func newID(id string) input.FieldErrors {
	u, err := ids.Decode(ids.Comment, id)
	switch {
	case err != nil:
		return field("id", fmt.Sprintf("is not a %s's ID: %v", ids.Comment, err))
	case !u.IsV7():
		return field("id", "is not the ID of a UUIDv7, which the ID of every entity is")
	}
	return nil
}

// entityID returns the problem of the payload's id, unless it is a
// comment's ID.
func entityID(id string) input.FieldErrors {
	if _, err := ids.Decode(ids.Comment, id); err != nil {
		return field("id", fmt.Sprintf("is not a %s's ID: %v", ids.Comment, err))
	}
	return nil
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
