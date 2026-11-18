package httpserver

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/comment"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/search"
)

// lastCommentMs is the time, in milliseconds since 1970, of the last ID
// that newCommentID made.
var lastCommentMs atomic.Int64

// newCommentID returns the ID of a new comment, of a UUIDv7 of a millisecond
// after the last one's that it made, so that the IDs that it makes sort in
// the order in which it made them, as the list of a project's comments
// orders them, which those that ids.New makes within a millisecond do not.
func newCommentID() string {
	for {
		last := lastCommentMs.Load()
		ms := max(time.Now().UnixMilli(), last+1)
		if lastCommentMs.CompareAndSwap(last, ms) {
			var r [10]byte
			for i := range r {
				r[i] = byte(rand.UintN(256))
			}
			return ids.Encode(ids.Comment, ids.NewV7(ms, r))
		}
	}
}

// commentsPath returns the path of the list of the project's comments, with
// the query parameters params, names and values in turn.
func commentsPath(project string, params ...string) string {
	return withQuery(projectPath(project)+"/comments", params)
}

// commentCountsPath returns the path of the counts of the project's threads,
// with the query parameters params, as commentsPath does.
func commentCountsPath(project string, params ...string) string {
	return withQuery(projectPath(project)+"/comments/counts", params)
}

// commentAuthorsPath returns the path of the list of the authors of the
// project's comments, with the query parameters params, as commentsPath
// does.
func commentAuthorsPath(project string, params ...string) string {
	return withQuery(projectPath(project)+"/comments/authors", params)
}

// withQuery returns path with the query parameters params, names and values
// in turn, but for those whose value is empty.
func withQuery(path string, params []string) string {
	v := url.Values{}
	for i := 0; i+1 < len(params); i += 2 {
		if params[i+1] != "" {
			v.Set(params[i], params[i+1])
		}
	}
	if len(v) == 0 {
		return path
	}
	return path + "?" + v.Encode()
}

// comment adds the first comment of a thread on a new node of the project,
// written by author, as the superuser, and returns its ID.
func (f *rolesFixture) comment(t *testing.T, project string, author signedIn) string {
	t.Helper()
	id := ids.New()
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO comment (id, workspace_id, project_id, node_id,
		body, created_by) VALUES ($1, $2, $3, $4, $5, $6)`, id, decode(t, ids.Workspace, f.ws),
		decode(t, ids.Project, project), decode(t, ids.Node, f.node(t, project)),
		[]byte(paragraph("Does the lighthouse exist yet?")), decode(t, ids.User, author.body.UserID)); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.Comment, id)
}

// resolvedComment adds a comment as comment does, of a thread that its
// author resolved, and returns its ID.
func (f *rolesFixture) resolvedComment(t *testing.T, project string, author signedIn) string {
	t.Helper()
	c := f.comment(t, project, author)
	if _, err := f.admin.Exec(context.Background(), "UPDATE comment SET resolved_at = now(), resolved_by = created_by "+
		"WHERE id = $1", decode(t, ids.Comment, c)); err != nil {
		t.Fatal(err)
	}
	return c
}

// deletedComment adds a comment as comment does, deleted to the project's
// trash, and returns its ID.
func (f *rolesFixture) deletedComment(t *testing.T, project string, author signedIn) string {
	t.Helper()
	c := f.comment(t, project, author)
	if _, err := f.admin.Exec(context.Background(), "UPDATE comment SET deleted_at = now() WHERE id = $1",
		decode(t, ids.Comment, c)); err != nil {
		t.Fatal(err)
	}
	return c
}

// postComment sends a comment.create of the comment id, on the target or
// replying to the parent, whichever is not "", with the body of a paragraph
// of text, as si, and returns the answer.
func postComment(t *testing.T, h http.Handler, si signedIn, project, id, target, parent, text string) response {
	t.Helper()
	p := map[string]any{"id": id, "body": paragraph(text)}
	if target != "" {
		p["target"] = target
	}
	if parent != "" {
		p["parent_id"] = parent
	}
	return call(h, http.MethodPost, commandsPath(project), commandBatch(t, wireCommand{Type: "comment.create",
		Payload: p}), si.header(true))
}

// commentCommand sends a batch of a command of the type typ, of the comment
// id, with the fields of extra, as si, and returns the answer.
func commentCommand(t *testing.T, h http.Handler, si signedIn, project, typ, id string, extra map[string]any) response {
	t.Helper()
	p := map[string]any{"id": id}
	for k, v := range extra {
		p[k] = v
	}
	return call(h, http.MethodPost, commandsPath(project), commandBatch(t, wireCommand{Type: typ, Payload: p}),
		si.header(true))
}

// allPagesOf returns every item of the list that path names, with its query
// parameters, page by page, of limit items each, as si, and fails the test
// unless each page is answered with 200.
func allPagesOf[T any](t *testing.T, h http.Handler, si signedIn, path string, limit int) []T {
	t.Helper()
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	out := []T{}
	for cursor := ""; ; {
		res := call(h, http.MethodGet, path+sep+"limit="+strconv.Itoa(limit)+"&cursor="+url.QueryEscape(cursor), nil,
			si.header(false))
		var page api.Page[T]
		if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
			t.Fatalf("GET %s: %d %s", path, res.status, res.body)
		}
		out = append(out, page.Items...)
		if page.NextCursor == nil {
			return out
		}
		cursor = *page.NextCursor
	}
}

// listComments returns every comment of the list of the project's comments
// with the query parameters params, as si reads it, page by page.
func listComments(t *testing.T, h http.Handler, si signedIn, project string, params ...string) []comment.CommentBody {
	t.Helper()
	return allPagesOf[comment.CommentBody](t, h, si, commentsPath(project, params...), 2)
}

// commentIDs returns the IDs of comments, in their order.
func commentIDs(comments []comment.CommentBody) []string {
	out := []string{}
	for _, c := range comments {
		out = append(out, c.ID)
	}
	return out
}

// commentText returns the text of a comment's body of one paragraph of
// text, as paragraph writes it, or "" for none.
func commentText(c comment.CommentBody) string {
	if c.Body == nil || len(c.Body.Blocks) != 1 {
		return ""
	}
	p, ok := c.Body.Blocks[0].(*richtext.Paragraph)
	if !ok || len(p.Inlines) != 1 {
		return ""
	}
	text, _ := p.Inlines[0].(*richtext.Text)
	if text == nil {
		return ""
	}
	return text.Text
}

// TestCommentThreads checks the commands of a project's threads and the list
// that reads them: a thread started on a node, with replies of other members,
// in its order, each comment with its author and its thread's target, the
// target's board, and its node, and the thread's first comment with whether
// it is resolved; a thread resolved, and opened again, by members who did not
// start it; an edit, which records when it was made; a member's edit or
// deletion of another's comment, refused with 403 author_required, whatever
// their role; the author's deletion of a thread's first comment, which leaves
// it in the list as deleted, without its body and its author, while a reply
// is not deleted, and the thread gone once its replies are deleted too; and
// the undo of the first comment's deletion, which shows the thread again,
// with the comment as it was before.
func TestCommentThreads(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner, editor, commenter := f.as["owner"], f.as["editor"], f.as["commenter"]
	p := f.project(t, "")
	board := f.board(t, p)
	node := createNode(t, f.s.main, owner, p, board, "element", map[string]any{"title": "Keeper"})
	root, first, second := newCommentID(), newCommentID(), newCommentID()
	res := resultFrom(t, postComment(t, f.s.main, editor, p, root, node, "", "Does the lighthouse exist yet?"))
	if len(res.Revisions) != 1 || res.Revisions[0].ID != root || res.Revisions[0].Rev != 1 || len(res.Undo) != 1 ||
		res.Undo[0].Type != "comment.delete" {
		t.Errorf("the thread's start: %+v, want the comment at 1, with its deletion to undo it", res)
	}
	resultFrom(t, postComment(t, f.s.main, commenter, p, first, "", root, "Not yet."))
	resultFrom(t, postComment(t, f.s.main, owner, p, second, "", root, "Soon."))

	list := listComments(t, f.s.main, owner, p)
	if got, want := commentIDs(list), []string{root, first, second}; !slices.Equal(got, want) {
		t.Fatalf("the comments are %v, want the thread's first comment, then its replies, %v", got, want)
	}
	r, reply := list[0], list[1]
	if r.ThreadID != root || r.ParentID != "" || r.ProjectID != p || r.TargetType != comment.Node ||
		r.TargetID != node || r.BoardID != board || r.BoardName == nil || *r.BoardName != "Harbour" ||
		r.NodeID != node || r.NodeTitle == nil || *r.NodeTitle != "Keeper" || r.Deleted ||
		commentText(r) != "Does the lighthouse exist yet?" || r.Author == nil ||
		r.Author.ID != editor.body.UserID || r.Author.DisplayName != "Editor" || r.Resolved == nil || *r.Resolved ||
		r.ResolvedAt != nil || r.EditedAt != nil || r.Rev != 1 {
		t.Errorf("the thread's first comment is %+v", r)
	}
	if reply.ThreadID != root || reply.ParentID != root || reply.TargetType != comment.Node ||
		reply.TargetID != node || reply.BoardID != board || reply.NodeID != node || commentText(reply) != "Not yet." ||
		reply.Author == nil || reply.Author.DisplayName != "Commenter" || reply.Resolved != nil {
		t.Errorf("the reply is %+v", reply)
	}

	// A member who did not start the thread resolves it, and another opens it
	// again.
	resultFrom(t, commentCommand(t, f.s.main, commenter, p, "comment.resolve", root, nil))
	r = listComments(t, f.s.main, owner, p)[0]
	if r.Resolved == nil || !*r.Resolved || r.ResolvedAt == nil || r.ResolvedBy == nil ||
		r.ResolvedBy.ID != commenter.body.UserID || r.ResolvedBy.DisplayName != "Commenter" || r.Rev != 2 {
		t.Errorf("once resolved, the thread's first comment is %+v", r)
	}
	if res := resultFrom(t, commentCommand(t, f.s.main, editor, p, "comment.resolve", root, nil)); len(res.Revisions) !=
		0 || len(res.Undo) != 0 {
		t.Errorf("the resolve of a resolved thread: %+v, want no change", res)
	}
	resultFrom(t, commentCommand(t, f.s.main, owner, p, "comment.unresolve", root, nil))
	if r = listComments(t, f.s.main, owner, p)[0]; *r.Resolved || r.ResolvedAt != nil || r.ResolvedBy != nil ||
		r.Rev != 3 {
		t.Errorf("once opened again, the thread's first comment is %+v", r)
	}

	// The author edits a reply, which records when; another member's edit or
	// deletion of it, an owner's among them, is refused, whatever the role.
	resultFrom(t, commentCommand(t, f.s.main, commenter, p, "comment.edit", first,
		map[string]any{"body": paragraph("Not yet, but soon.")}))
	if c := listComments(t, f.s.main, owner, p)[1]; commentText(c) != "Not yet, but soon." || c.EditedAt == nil ||
		c.Rev != 2 || c.UpdatedAt.Before(c.CreatedAt) {
		t.Errorf("once edited, the reply is %+v", c)
	}
	for _, cmd := range []struct {
		who   signedIn
		typ   string
		extra map[string]any
	}{
		{editor, "comment.edit", map[string]any{"body": paragraph("Mine now.")}},
		{owner, "comment.edit", map[string]any{"body": paragraph("Mine now.")}},
		{owner, "comment.delete", nil},
		{f.as["admin"], "comment.restore", nil},
	} {
		res := commentCommand(t, f.s.main, cmd.who, p, cmd.typ, first, cmd.extra)
		code, locs, _ := problemsOf(t, res)
		if res.status != http.StatusForbidden || code != api.CodeAuthorRequired ||
			!slices.Contains(locs, "body.commands[0].payload.id") {
			t.Errorf("another member's %s of a comment: %d %s, want 403 %s at its id", cmd.typ, res.status, res.body,
				api.CodeAuthorRequired)
		}
	}

	// The author's deletion of the thread's first comment leaves it in the
	// list as deleted while a reply is not.
	before := listComments(t, f.s.main, owner, p)[0]
	deleted := resultFrom(t, commentCommand(t, f.s.main, editor, p, "comment.delete", root, nil))
	if len(deleted.Undo) != 1 || deleted.Undo[0].Type != "comment.restore" {
		t.Fatalf("the deletion's undo is %+v", deleted.Undo)
	}
	list = listComments(t, f.s.main, commenter, p)
	if got, want := commentIDs(list), []string{root, first, second}; !slices.Equal(got, want) {
		t.Fatalf("once the first comment is deleted, the comments are %v, want %v", got, want)
	}
	if r := list[0]; !r.Deleted || r.Body != nil || r.Author != nil || r.TargetID != node || r.Resolved == nil {
		t.Errorf("the deleted first comment is listed as %+v, want it deleted, without its body and its author", r)
	}
	// A deleted reply is not listed, and a thread whose comments are all
	// deleted is gone.
	resultFrom(t, commentCommand(t, f.s.main, commenter, p, "comment.delete", first, nil))
	if got, want := commentIDs(listComments(t, f.s.main, owner, p)), []string{root, second}; !slices.Equal(got, want) {
		t.Errorf("once a reply is deleted, the comments are %v, want %v", got, want)
	}
	resultFrom(t, commentCommand(t, f.s.main, owner, p, "comment.delete", second, nil))
	if got := commentIDs(listComments(t, f.s.main, owner, p)); len(got) != 0 {
		t.Errorf("once every comment of the thread is deleted, the comments are %v, want none", got)
	}

	// The undo of the first comment's deletion shows the thread again, with
	// the comment as it was before, as its deletion changed nothing of it.
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, undoCommands(deleted.Undo)...),
		editor.header(true)))
	list = listComments(t, f.s.main, owner, p)
	if got, want := commentIDs(list), []string{root}; !slices.Equal(got, want) {
		t.Fatalf("after the undo of its deletion, the comments are %v, want %v", got, want)
	}
	if r := list[0]; r.Deleted || commentText(r) != "Does the lighthouse exist yet?" || r.Author == nil ||
		r.Author.ID != editor.body.UserID || !r.UpdatedAt.Equal(before.UpdatedAt) || r.EditedAt != nil ||
		r.Rev != before.Rev+2 {
		t.Errorf("after the undo of its deletion, the first comment is %+v, want it as before, %+v", r, before)
	}
}

// TestCommentTimesOfOneBatch checks that a comment's edit, its thread's
// resolution, and a reply's deletion in the batch that posted it are dated as
// the batch applies, as its posting is, rather than as the batch's
// transaction began, before the comment was posted: none is before the
// comment's created_at.
func TestCommentTimesOfOneBatch(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	editor := f.as["editor"]
	p := f.project(t, "")
	node := createNode(t, f.s.main, editor, p, f.board(t, p), "element", nil)
	root := newCommentID()
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t,
		wireCommand{Type: "comment.create", Payload: map[string]any{"id": root, "target": node,
			"body": paragraph("Does the lighthouse exist yet?")}},
		wireCommand{Type: "comment.edit", Payload: map[string]any{"id": root, "body": paragraph("It does.")}},
		wireCommand{Type: "comment.resolve", Payload: map[string]any{"id": root}}), editor.header(true)))
	list := listComments(t, f.s.main, editor, p)
	if len(list) != 1 {
		t.Fatalf("the comments are %v, want the one posted", commentIDs(list))
	}
	c := list[0]
	if c.EditedAt == nil || c.ResolvedAt == nil || c.EditedAt.Before(c.CreatedAt) ||
		c.UpdatedAt.Before(c.CreatedAt) || !c.UpdatedAt.Equal(*c.EditedAt) || c.ResolvedAt.Before(c.CreatedAt) {
		t.Errorf("the comment posted, edited, and resolved in one batch is %+v, want it edited and resolved at or "+
			"after it was posted, at %v", c, c.CreatedAt)
	}

	// A reply posted and deleted in one batch, whose deletion's time only an
	// answer of revision_conflict shows, as of a restore that expects the
	// revision that it was posted with.
	reply := newCommentID()
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t,
		wireCommand{Type: "comment.create", Payload: map[string]any{"id": reply, "parent_id": root,
			"body": paragraph("Since yesterday.")}},
		wireCommand{Type: "comment.delete", Payload: map[string]any{"id": reply}}), editor.header(true)))
	res := call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t,
		wireCommand{Type: "comment.restore", Payload: map[string]any{"id": reply},
			ExpectedRev: map[string]int64{reply: 1}}), editor.header(true))
	if res.status != http.StatusConflict {
		t.Fatalf("the restore that expects the revision that the reply was posted with: %d %s", res.status, res.body)
	}
	states, _ := res.details(t)["states"].(map[string]any)
	state, _ := states[reply].(map[string]any)
	createdAt, _ := state["created_at"].(string)
	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		t.Fatalf("the reply's state: %v (%v)", state, err)
	}
	deletedAt, _ := state["deleted_at"].(string)
	deleted, err := time.Parse(time.RFC3339Nano, deletedAt)
	if err != nil || deleted.Before(created) {
		t.Errorf("the reply posted and deleted in one batch is %v, want it deleted at or after it was posted (%v)",
			state, err)
	}
}

// TestCommentsInTheTrash checks that a thread is in the trash with its
// target: once a node is deleted, the list, the counts, and the search show
// none of its threads, and no command changes them but a comment's deletion
// or restoration; once the node is restored, they are all back as they were,
// with their bodies, their resolutions, and their replies, as nothing wrote
// them meanwhile. A board's deletion hides its own threads and its nodes',
// connections', and variables', and a connection's deletion, or its node's,
// the connection's.
//
// Verifies: FR-COMMENT-01/AC2
func TestCommentsInTheTrash(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner := f.as["owner"]
	p := f.project(t, "")
	board := createBoard(t, f.s.main, owner, p, "Harbour")
	keeper := createNode(t, f.s.main, owner, p, board, "element", map[string]any{"title": "Keeper"})
	gate := createNode(t, f.s.main, owner, p, board, "element", map[string]any{"title": "Gate"})
	path := createConnection(t, f.s.main, owner, p, keeper, gate, nil)
	variable := newVariableID()
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, variableCommand("variable.create",
		map[string]any{"id": variable, "key": "tide", "type": "int", "scope": "board", "board_id": board})),
		owner.header(true)))
	onNode, reply, onBoard, onPath, onVariable := newCommentID(), newCommentID(), newCommentID(), newCommentID(),
		newCommentID()
	resultFrom(t, postComment(t, f.s.main, owner, p, onNode, keeper, "", "Lanterns everywhere"))
	resultFrom(t, postComment(t, f.s.main, owner, p, reply, "", onNode, "Lanterns at the pier too"))
	resultFrom(t, commentCommand(t, f.s.main, owner, p, "comment.resolve", onNode, nil))
	resultFrom(t, postComment(t, f.s.main, owner, p, onBoard, board, "", "Lanterns for the board"))
	resultFrom(t, postComment(t, f.s.main, owner, p, onPath, path, "", "Lanterns on the path"))
	resultFrom(t, postComment(t, f.s.main, owner, p, onVariable, variable, "", "Lanterns by the tide"))
	all := []string{onVariable, onPath, onBoard, onNode, reply}

	shown := func(when string, want ...string) {
		t.Helper()
		list := listComments(t, f.s.main, owner, p)
		if got := commentIDs(list); !slices.Equal(got, want) {
			t.Errorf("%s, the comments are %v, want %v", when, got, want)
		}
		counts := allPagesOf[comment.CommentCount](t, f.s.main, owner, commentCountsPath(p), 2)
		var threads int64
		for _, c := range counts {
			threads += c.Open + c.Resolved
		}
		roots := 0
		for _, c := range list {
			if c.ParentID == "" {
				roots++
			}
		}
		if threads != int64(roots) {
			t.Errorf("%s, the counts are %+v, want %d threads", when, counts, roots)
		}
		found := searchIDs(t, f.s.main, owner, p, "lanterns", 10, "type", "comment")
		if !slices.Equal(slices.Sorted(slices.Values(found)), slices.Sorted(slices.Values(want))) {
			t.Errorf("%s, the search finds %v, want %v", when, found, want)
		}
	}
	shown("before any deletion", all...)

	before := listComments(t, f.s.main, owner, p)
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{Type: "node.delete",
		Payload: map[string]string{"id": keeper}}), owner.header(true)))
	// The connection leaves the node, so it is in the trash with it.
	shown("once the node is deleted", onVariable, onBoard)
	for _, c := range []struct {
		typ, id string
		extra   map[string]any
	}{
		{"comment.edit", onNode, map[string]any{"body": paragraph("Lanterns nowhere")}},
		{"comment.edit", onPath, map[string]any{"body": paragraph("Lanterns nowhere")}},
		{"comment.unresolve", onNode, nil},
		{"comment.resolve", onPath, nil},
	} {
		res := commentCommand(t, f.s.main, owner, p, c.typ, c.id, c.extra)
		if code, locs, _ := problemsOf(t, res); res.status != http.StatusUnprocessableEntity ||
			code != api.CodeValidationFailed || !slices.Contains(locs, "body.commands[0].payload.id") {
			t.Errorf("the %s of a comment whose target is in the trash: %d %s, want 422 at its id", c.typ, res.status,
				res.body)
		}
	}
	if res := postComment(t, f.s.main, owner, p, newCommentID(), "", onNode, "Lanterns again"); res.status !=
		http.StatusUnprocessableEntity {
		t.Errorf("a reply to a thread whose target is in the trash: %d %s, want 422", res.status, res.body)
	}
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{Type: "node.restore",
		Payload: map[string]string{"id": keeper}}), owner.header(true)))
	shown("once the node is restored", all...)
	after := listComments(t, f.s.main, owner, p)
	for i := range before {
		b, a := before[i], after[i]
		if a.ID != b.ID || commentText(a) != commentText(b) || a.Rev != b.Rev || a.ParentID != b.ParentID ||
			(a.Resolved == nil) != (b.Resolved == nil) || a.Resolved != nil && *a.Resolved != *b.Resolved ||
			!a.UpdatedAt.Equal(b.UpdatedAt) {
			t.Errorf("once the node is restored, the comment is %+v, want it as it was, %+v", a, b)
		}
	}

	// A connection's own deletion hides its threads alone.
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
		Type: "connection.delete", Payload: map[string]string{"id": path}}), owner.header(true)))
	shown("once the connection is deleted", onVariable, onBoard, onNode, reply)
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{
		Type: "connection.restore", Payload: map[string]string{"id": path}}), owner.header(true)))

	// A board's deletion hides its own threads, and its nodes',
	// connections', and variables'.
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{Type: "board.delete",
		Payload: map[string]any{"id": board, "confirm": true}}), owner.header(true)))
	shown("once the board is deleted")
	if res := postComment(t, f.s.main, owner, p, newCommentID(), gate, "", "Lanterns"); res.status !=
		http.StatusUnprocessableEntity {
		t.Errorf("a thread on a node of a board in the trash: %d %s, want 422", res.status, res.body)
	}
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{Type: "board.restore",
		Payload: map[string]any{"id": board}}), owner.header(true)))
	shown("once the board is restored", all...)
}

// TestCommentRefusals checks how the API answers a command of a comment
// that refuses its batch, at the location of the payload's field: a reply to
// a reply; a resolve of a reply; a body that holds a value reference or a
// mention, at its location; an empty one, one of white space alone, or one
// of a list of empty items, whose bullets and numbers are no text; one of
// more than 10,000 characters of text; a comment with neither a target nor
// a parent, or both, or a target of another kind; a target in the trash,
// or another project's; a reply to a thread whose comments are all deleted;
// an ID that a comment has already, of the project or of another; and any
// command of a comment of an archived project. Nothing of a refused batch
// applies.
func TestCommentRefusals(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner := f.as["owner"]
	p, other := f.project(t, ""), f.project(t, "")
	node, gone, elsewhere := f.node(t, p), f.node(t, p), f.node(t, other)
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{Type: "node.delete",
		Payload: map[string]string{"id": gone}}), owner.header(true)))
	root, reply, lone := newCommentID(), newCommentID(), newCommentID()
	resultFrom(t, postComment(t, f.s.main, owner, p, root, node, "", "Does the lighthouse exist yet?"))
	resultFrom(t, postComment(t, f.s.main, owner, p, reply, "", root, "Not yet."))
	resultFrom(t, postComment(t, f.s.main, owner, p, lone, node, "", "Alone."))
	resultFrom(t, commentCommand(t, f.s.main, owner, p, "comment.delete", lone, nil))

	doc := func(inlines ...string) json.RawMessage { return richText(inlines...) }
	create := func(target, parent string, body json.RawMessage) wireCommand {
		p := map[string]any{"id": newCommentID(), "body": body}
		if target != "" {
			p["target"] = target
		}
		if parent != "" {
			p["parent_id"] = parent
		}
		return wireCommand{Type: "comment.create", Payload: p}
	}
	component := ids.Encode(ids.Component, ids.New())
	list := `{"type":"doc","content":[{"type":"paragraph"},{"type":"bulletList","content":[{"type":"listItem",` +
		`"content":[{"type":"paragraph","content":[{"type":"text","text":"Ask "},{"type":"mention","attrs":{"id":"` +
		component + `"}}]}]}]}]}`
	for _, c := range []struct {
		cmd wireCommand
		loc string
	}{
		{create("", reply, paragraph("A reply to a reply")), "body.commands[0].payload.parent_id"},
		{wireCommand{Type: "comment.resolve", Payload: map[string]string{"id": reply}}, "body.commands[0].payload.id"},
		{create(node, "", doc(textInline("Show "), valueInline(ids.Encode(ids.Variable, ids.New())))),
			"body.commands[0].payload.body.content[0].content[1]"},
		{create(node, "", json.RawMessage(list)), "body.commands[0].payload.body.content[1].content[0].content[0]." +
			"content[1]"},
		{wireCommand{Type: "comment.edit", Payload: map[string]any{"id": root, "body": doc(mentionInline(component))}},
			"body.commands[0].payload.body.content[0].content[0]"},
		{create(node, "", json.RawMessage(`{"type":"doc","content":[{"type":"paragraph"}]}`)),
			"body.commands[0].payload.body"},
		{create(node, "", paragraph(" \t ")), "body.commands[0].payload.body"},
		// A list of empty items, whose bullets and numbers are no text that
		// its author typed.
		{create(node, "", json.RawMessage(`{"type":"doc","content":[{"type":"bulletList","content":[`+
			`{"type":"listItem","content":[{"type":"paragraph"}]}]}]}`)), "body.commands[0].payload.body"},
		{wireCommand{Type: "comment.edit", Payload: map[string]any{"id": root, "body": json.RawMessage(
			`{"type":"doc","content":[{"type":"orderedList","content":[{"type":"listItem","content":[` +
				`{"type":"paragraph","content":[{"type":"text","text":" "}]}]}]}]}`)}},
			"body.commands[0].payload.body"},
		{create(node, "", paragraph(strings.Repeat("a", comment.MaxBodyLength+1))), "body.commands[0].payload.body"},
		{create("", "", paragraph("Nowhere")), "body.commands[0].payload.target"},
		{create(node, root, paragraph("Both")), "body.commands[0].payload.parent_id"},
		{create(ids.Encode(ids.Component, ids.New()), "", paragraph("Of a component")),
			"body.commands[0].payload.target"},
		{create(gone, "", paragraph("On a node in the trash")), "body.commands[0].payload.target"},
		{create(elsewhere, "", paragraph("On another project's node")), "body.commands[0].payload.target"},
		{create(ids.Encode(ids.Node, ids.New()), "", paragraph("On no node")), "body.commands[0].payload.target"},
		{create("", lone, paragraph("To a thread of none")), "body.commands[0].payload.parent_id"},
		{create("", newCommentID(), paragraph("To no comment")), "body.commands[0].payload.parent_id"},
		{wireCommand{Type: "comment.create", Payload: map[string]any{"id": root, "target": node,
			"body": paragraph("Again")}}, "body.commands[0].payload.id"},
		{wireCommand{Type: "comment.edit", Payload: map[string]any{"id": lone, "body": paragraph("Deleted")}},
			"body.commands[0].payload.id"},
		{wireCommand{Type: "comment.resolve", Payload: map[string]string{"id": lone}}, "body.commands[0].payload.id"},
		{wireCommand{Type: "comment.delete", Payload: map[string]string{"id": newCommentID()}},
			"body.commands[0].payload.id"},
	} {
		res := call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, c.cmd), owner.header(true))
		code, locs, _ := problemsOf(t, res)
		if res.status != http.StatusUnprocessableEntity || code != api.CodeValidationFailed ||
			!slices.Contains(locs, c.loc) {
			t.Errorf("%+v: %d %.400s, want 422 %s at %s", c.cmd, res.status, res.body, api.CodeValidationFailed, c.loc)
		}
	}
	// The ID of a comment of another project, which the batch's transaction
	// does not see, is refused as one that the project has is.
	res := postComment(t, f.s.main, owner, other, root, elsewhere, "", "Taken")
	if code, locs, _ := problemsOf(t, res); res.status != http.StatusUnprocessableEntity ||
		code != api.CodeValidationFailed || !slices.Contains(locs, "body.commands[0].payload.id") {
		t.Errorf("a create with the ID of another project's comment: %d %s, want 422 at its id", res.status, res.body)
	}
	// A body of 10,000 characters of text, in two paragraphs, is taken.
	long := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"` +
		strings.Repeat("a", comment.MaxBodyLength/2) + `"}]},{"type":"paragraph","content":[{"type":"text","text":"` +
		strings.Repeat("b", comment.MaxBodyLength/2) + `"}]}]}`
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t,
		create(node, "", json.RawMessage(long))), owner.header(true)))

	// Nothing of the refused batches applied.
	if got, want := commentIDs(listComments(t, f.s.main, owner, p)), 3; len(got) != want {
		t.Errorf("after the refusals, the comments are %v, want %d", got, want)
	}
	if got := commentIDs(listComments(t, f.s.main, owner, other)); len(got) != 0 {
		t.Errorf("after the refusals, the other project's comments are %v, want none", got)
	}

	// An archived project takes no command of a comment.
	archived := f.project(t, "archived")
	for _, cmd := range []wireCommand{create(f.node(t, archived), "", paragraph("Archived")),
		{Type: "comment.resolve", Payload: map[string]string{"id": f.comment(t, archived, owner)}}} {
		wantCode(t, "a command of a comment of an archived project", call(f.s.main, http.MethodPost,
			commandsPath(archived), commandBatch(t, cmd), owner.header(true)), http.StatusConflict,
			api.CodeProjectArchived)
	}
}

// TestCommentLists checks the filters and the pages of the list of a
// project's comments, the counts of its threads, and the authors of its
// comments: by status, by author, who wrote a comment of the thread that is
// not deleted, by board, which keeps the board's own threads and its nodes',
// connections', and variables', and by target, which keeps those of that
// entity alone; pages that a thread started between two of them changes
// nothing of; the counts of each target's open and resolved threads, of the
// project and of a board; and the authors with their display names. A filter
// by an ID that does not decode, or is of another kind, is answered with 400
// invalid_id.
func TestCommentLists(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner, editor, commenter := f.as["owner"], f.as["editor"], f.as["commenter"]
	p := f.project(t, "")
	harbour, pier := f.board(t, p), f.board(t, p)
	keeper := f.boardNode(t, p, harbour)
	path, _ := f.boardConnection(t, p)
	global := f.variable(t, p)
	// Threads, oldest first: on the keeper, resolved, by the editor, with the
	// commenter's reply; on the harbour, by the commenter; on the pier, by
	// the editor, whose reply the commenter deleted; and on the global
	// variable and on the path, by the owner.
	onKeeper, keeperReply, onHarbour, onPier, pierReply := newCommentID(), newCommentID(), newCommentID(),
		newCommentID(), newCommentID()
	resultFrom(t, postComment(t, f.s.main, editor, p, onKeeper, keeper, "", "Keeper"))
	resultFrom(t, postComment(t, f.s.main, commenter, p, keeperReply, "", onKeeper, "Reply"))
	resultFrom(t, commentCommand(t, f.s.main, owner, p, "comment.resolve", onKeeper, nil))
	resultFrom(t, postComment(t, f.s.main, commenter, p, onHarbour, harbour, "", "Harbour"))
	resultFrom(t, postComment(t, f.s.main, editor, p, onPier, pier, "", "Pier"))
	resultFrom(t, postComment(t, f.s.main, commenter, p, pierReply, "", onPier, "Reply"))
	resultFrom(t, commentCommand(t, f.s.main, commenter, p, "comment.delete", pierReply, nil))
	onGlobal, onPath := newCommentID(), newCommentID()
	resultFrom(t, postComment(t, f.s.main, owner, p, onGlobal, global, "", "Global"))
	resultFrom(t, postComment(t, f.s.main, owner, p, onPath, path, "", "Path"))

	for _, c := range []struct {
		params []string
		want   []string
	}{
		{nil, []string{onPath, onGlobal, onPier, onHarbour, onKeeper, keeperReply}},
		{[]string{"status", "open"}, []string{onPath, onGlobal, onPier, onHarbour}},
		{[]string{"status", "resolved"}, []string{onKeeper, keeperReply}},
		// The commenter's deleted reply does not keep the pier's thread.
		{[]string{"author", commenter.body.UserID}, []string{onHarbour, onKeeper, keeperReply}},
		{[]string{"author", editor.body.UserID, "status", "open"}, []string{onPier}},
		{[]string{"board", harbour}, []string{onHarbour, onKeeper, keeperReply}},
		{[]string{"board", pier}, []string{onPier}},
		{[]string{"target", harbour}, []string{onHarbour}},
		{[]string{"target", keeper}, []string{onKeeper, keeperReply}},
		{[]string{"target", global}, []string{onGlobal}},
		{[]string{"target", path}, []string{onPath}},
		{[]string{"board", ids.Encode(ids.Board, ids.New())}, []string{}},
	} {
		if got := commentIDs(listComments(t, f.s.main, owner, p, c.params...)); !slices.Equal(got, c.want) {
			t.Errorf("the comments of %v are %v, want %v", c.params, got, c.want)
		}
	}
	for _, params := range [][]string{{"author", harbour}, {"board", keeper}, {"target", onKeeper},
		{"target", ids.Encode(ids.Component, ids.New())}, {"target", "nd_x"}} {
		wantCode(t, "a filter by "+strings.Join(params, " "), call(f.s.main, http.MethodGet,
			commentsPath(p, params...), nil, owner.header(false)), http.StatusBadRequest, api.CodeInvalidID)
	}
	wantCode(t, "a board of the counts that is no board's ID", call(f.s.main, http.MethodGet,
		commentCountsPath(p, "board", keeper), nil, owner.header(false)), http.StatusBadRequest, api.CodeInvalidID)
	// A cursor of the list holds the times of its key in UTC, as the server
	// writes them, and none other.
	elsewhere := time.Date(2026, 11, 16, 10, 0, 0, 0, time.FixedZone("CET", 3600))
	for name, cursor := range map[string]string{"not a cursor": "nope",
		"a cursor without times": mustCursor(t, commentKey{Thread: ids.New().String(), ID: ids.New().String()}),
		"a cursor of a time not in UTC": mustCursor(t, commentKey{ThreadAt: elsewhere, Thread: ids.New().String(),
			At: elsewhere, ID: ids.New().String()})} {
		wantCode(t, name, call(f.s.main, http.MethodGet, commentsPath(p, "cursor", cursor), nil, owner.header(false)),
			http.StatusBadRequest, api.CodeInvalidCursor)
	}
	// A target of another kind is said to be one, with the kinds that a
	// thread is on.
	res := call(f.s.main, http.MethodGet, commentsPath(p, "target", onKeeper), nil, owner.header(false))
	if body := string(res.body); !strings.Contains(body, "not a node, a connection, a board, or a variable") ||
		!strings.Contains(body, `"reason":"wrong_prefix"`) {
		t.Errorf("a target that is a comment's ID: %d %s", res.status, res.body)
	}

	// A thread started between two pages changes neither: it is newer than
	// any that the first page held, so the pages after it do not hold it.
	var got []string
	for cursor, first := "", true; ; first = false {
		var page api.Page[comment.CommentBody]
		res := call(f.s.main, http.MethodGet, commentsPath(p, "limit", "2", "cursor", cursor), nil,
			owner.header(false))
		if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
			t.Fatalf("a page of the comments: %d %s", res.status, res.body)
		}
		got = append(got, commentIDs(page.Items)...)
		if first {
			resultFrom(t, postComment(t, f.s.main, owner, p, newCommentID(), keeper, "", "Newer"))
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if want := []string{onPath, onGlobal, onPier, onHarbour, onKeeper, keeperReply}; !slices.Equal(got, want) {
		t.Errorf("the pages around a thread started between them hold %v, want %v", got, want)
	}

	// The threads are in the order in which they were started, and their
	// replies in the order in which they were posted, as the server's clock
	// has it, whatever the times of the IDs that the clients made: a thread
	// whose ID is of the year 2099 stays below those started after it, and a
	// reply whose ID is of 1970 below those posted before it, page by page.
	pinned := ids.Encode(ids.Comment, ids.NewV7(time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(),
		[10]byte{1}))
	resultFrom(t, postComment(t, f.s.main, owner, p, pinned, harbour, "", "Pinned?"))
	pinnedReply, early := newCommentID(), ids.Encode(ids.Comment, ids.NewV7(1, [10]byte{2}))
	resultFrom(t, postComment(t, f.s.main, editor, p, pinnedReply, "", pinned, "First"))
	resultFrom(t, postComment(t, f.s.main, owner, p, early, "", pinned, "Early?"))
	after := newCommentID()
	resultFrom(t, postComment(t, f.s.main, owner, p, after, harbour, "", "After"))
	if got, want := commentIDs(listComments(t, f.s.main, owner, p, "target", harbour)),
		[]string{after, pinned, pinnedReply, early, onHarbour}; !slices.Equal(got, want) {
		t.Errorf("the threads of IDs of other times are %v, want %v", got, want)
	}
	// They go, so that the counts below are of the threads above.
	for _, c := range []struct {
		author signedIn
		id     string
	}{{owner, pinned}, {editor, pinnedReply}, {owner, early}, {owner, after}} {
		resultFrom(t, commentCommand(t, f.s.main, c.author, p, "comment.delete", c.id, nil))
	}

	type count struct {
		target         string
		open, resolved int64
	}
	countsOf := func(params ...string) []count {
		t.Helper()
		out := []count{}
		for _, c := range allPagesOf[comment.CommentCount](t, f.s.main, owner, commentCountsPath(p, params...), 2) {
			out = append(out, count{c.TargetID, c.Open, c.Resolved})
		}
		return out
	}
	// The targets in the order of their kinds' names, then of their IDs.
	if got, want := countsOf("board", harbour), []count{{harbour, 1, 0}, {keeper, 1, 1}}; !slices.Equal(got, want) {
		t.Errorf("the counts of the harbour are %v, want %v", got, want)
	}
	all := countsOf()
	want := []count{{harbour, 1, 0}, {pier, 1, 0}, {path, 1, 0}, {keeper, 1, 1}, {global, 1, 0}}
	slices.SortFunc(want[:2], func(a, b count) int {
		return strings.Compare(uuidOf(t, a.target).String(), uuidOf(t, b.target).String())
	})
	if !slices.Equal(all, want) {
		t.Errorf("the counts are %v, want %v", all, want)
	}

	authors := allPagesOf[comment.CommentAuthor](t, f.s.main, f.as["viewer"], commentAuthorsPath(p), 1)
	wantAuthors := []comment.CommentAuthor{{ID: owner.body.UserID}, {ID: editor.body.UserID, DisplayName: "Editor"},
		{ID: commenter.body.UserID, DisplayName: "Commenter"}}
	slices.SortFunc(wantAuthors, func(a, b comment.CommentAuthor) int {
		return strings.Compare(uuidOf(t, a.ID).String(), uuidOf(t, b.ID).String())
	})
	if len(authors) != len(wantAuthors) {
		t.Fatalf("the authors are %+v, want %+v", authors, wantAuthors)
	}
	for i, a := range authors {
		if a.ID != wantAuthors[i].ID || wantAuthors[i].DisplayName != "" && a.DisplayName != wantAuthors[i].DisplayName ||
			a.DisplayName == "" {
			t.Errorf("the author %d is %+v, want %+v", i, a, wantAuthors[i])
		}
	}
}

// TestCommentSearch checks that project search finds a comment by a word of
// its body, of the type comment and of every type, with its thread's target,
// and the target's board and node, by which a client opens it, and finds it
// no longer once it is deleted, or its node is in the trash.
func TestCommentSearch(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner := f.as["owner"]
	p := f.project(t, "")
	board := createBoard(t, f.s.main, owner, p, "Harbour")
	keeper := createNode(t, f.s.main, owner, p, board, "element", map[string]any{"title": "Keeper"})
	gate := createNode(t, f.s.main, owner, p, board, "element", map[string]any{"title": "Gate"})
	path := createConnection(t, f.s.main, owner, p, keeper, gate, nil)
	onNode, onPath := newCommentID(), newCommentID()
	resultFrom(t, postComment(t, f.s.main, owner, p, onNode, keeper, "", "Is the lighthouse lit?"))
	resultFrom(t, postComment(t, f.s.main, owner, p, onPath, path, "", "The lighthouse path"))

	for _, params := range [][]string{{"type", "comment"}, nil} {
		page := searchPage(t, f.s.main, owner, searchPath(p, "lighth", params...))
		byID := map[string]search.Result{}
		for _, r := range page.Items {
			byID[r.ID] = r
		}
		if r := byID[onNode]; r.Type != search.Comment || r.TargetType != "node" || r.TargetID != keeper ||
			r.BoardID != board || r.BoardName == nil || *r.BoardName != "Harbour" || r.NodeID != keeper ||
			r.NodeTitle == nil || *r.NodeTitle != "Keeper" || marked(r.Snippet) != "Is the [lighthouse] lit?" ||
			len(r.Title) != 0 {
			t.Errorf("searched with %v, the comment on the node is %+v", params, r)
		}
		if r := byID[onPath]; r.Type != search.Comment || r.TargetType != "connection" || r.TargetID != path ||
			r.BoardID != board || r.NodeID != keeper {
			t.Errorf("searched with %v, the comment on the connection is %+v", params, r)
		}
	}
	if got := searchIDs(t, f.s.main, owner, p, "lighthouse", 10, "type", "node"); len(got) != 0 {
		t.Errorf("a search of the nodes finds %v, want none", got)
	}
	resultFrom(t, commentCommand(t, f.s.main, owner, p, "comment.delete", onPath, nil))
	if got := searchIDs(t, f.s.main, owner, p, "lighthouse", 10); !slices.Equal(got, []string{onNode}) {
		t.Errorf("once a comment is deleted, the search finds %v, want %v", got, []string{onNode})
	}
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{Type: "node.delete",
		Payload: map[string]string{"id": keeper}}), owner.header(true)))
	if got := searchIDs(t, f.s.main, owner, p, "lighthouse", 10); len(got) != 0 {
		t.Errorf("once the comment's node is deleted, the search finds %v, want none", got)
	}
}

// TestCommenterComments checks that a commenter, by their role in the
// workspace or by a grant on the project, starts a thread on a node, replies
// to it, and resolves and opens it again, and is refused every other command
// of the project with 403 insufficient_role, each in a batch of its own, as
// the property test of undo makes it (undoGenerators) of the project's
// content, so that the role alone refuses it; and that a viewer is refused a
// comment.
//
// Verifies: FR-COMMENT-01/AC1
func TestCommenterComments(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	// The test sends many more batches in a minute than a person does.
	policy := ratelimit.DefaultPolicy()
	policy.Rates[ratelimit.Auth].Limits[ratelimit.User] = 1000
	policy.Rates[ratelimit.Write].Limits[ratelimit.User] = 100_000
	f.s.limits = ratelimit.New(readyDB{f.s.ready}, policy)
	f.grants = map[string]string{"guest": "commenter"}
	p := f.project(t, "")
	others := otherCommands(t, f, p)
	node := f.node(t, p)
	for _, who := range []string{"commenter", "guest"} {
		si := f.as[who]
		root, reply := newCommentID(), newCommentID()
		resultFrom(t, postComment(t, f.s.main, si, p, root, node, "", "Does the lighthouse exist yet?"))
		resultFrom(t, postComment(t, f.s.main, si, p, reply, "", root, "Not yet."))
		resultFrom(t, commentCommand(t, f.s.main, si, p, "comment.resolve", root, nil))
		resultFrom(t, commentCommand(t, f.s.main, si, p, "comment.unresolve", root, nil))
		for _, cmd := range others {
			wantCode(t, "a "+who+"'s "+cmd.Type, call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, cmd),
				si.header(true)), http.StatusForbidden, api.CodeInsufficientRole)
		}
	}
	if got, want := len(others), len(newCommandRegistry().Types())-6; got != want {
		t.Errorf("the commenters were refused %d types of command, want every one but the 6 of a comment, %d", got,
			want)
	}
	wantCode(t, "a viewer's comment", postComment(t, f.s.main, f.as["viewer"], p, newCommentID(), node, "",
		"A viewer's"), http.StatusForbidden, api.CodeInsufficientRole)
}

// otherCommands returns a command of every registered type of command but a
// comment's, of the project p, each with a payload that its schema and its
// checks take, which the generators of the property test of undo make
// (undoGenerators), of content that the workspace's owner makes of random
// batches first, as that test does, and of the batches that the generators
// of each type need, as a deletion before a restoration.
func otherCommands(t *testing.T, f *rolesFixture, p string) []wireCommand {
	t.Helper()
	owner := f.as["owner"]
	project := decode(t, ids.Project, p)
	g := &undoGen{r: rand.New(rand.NewPCG(74, 0x37)), ms: 1_700_000_000_000,
		asset: upload(t, f.s.main, owner, p, testPNG(t, 8, 8), http.StatusCreated).ID, project: p}
	send := func(cmds []wireCommand) {
		call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, cmds...), owner.header(true))
	}
	// apply sends a batch of a command of the type typ as the owner, or of
	// the project's restoration or unarchival, as the generators make it
	// (undoGen.batch).
	apply := func(typ string) {
		g.m = readUndoModel(t, f.admin, project)
		if cmds, ok := g.batch(typ); ok {
			send(cmds)
		}
	}
	creations := []string{"folder.create", "board.create", "node.create", "node.create", "connection.create",
		"variable.create", "component_type.create", "attribute.create", "component.create", "branch_output.create",
		"play_state.create"}
	for range 60 {
		apply(creations[g.r.IntN(len(creations))])
	}
	var out []wireCommand
	for _, typ := range newCommandRegistry().Types() {
		name := string(typ)
		if strings.HasPrefix(name, "comment.") {
			continue
		}
		// What a generator of the type needs that the content may lack: an
		// entity of its kind, as an enum attribute for a change of its
		// options, which its kind's creation may make, an entity in the
		// trash, for a restoration, and a mute, for its removal.
		kind, _, _ := strings.Cut(name, ".")
		need := kind + ".create"
		switch {
		case strings.HasSuffix(name, ".restore"):
			need = kind + ".delete"
		case name == "node.restore_many":
			need = "node.delete_many"
		case name == "validation.unmute":
			need = "validation.mute"
		case kind == "validation" || kind == "clipboard":
			need = "node.create"
		}
		for attempt := 0; ; attempt++ {
			g.m = readUndoModel(t, f.admin, project)
			for g.m.deleted || g.m.archived {
				apply("")
				g.m = readUndoModel(t, f.admin, project)
			}
			payload, ok := undoGenerators[name](g)
			// A command of the project needs nothing of its content.
			if ok || strings.HasPrefix(name, "project.") {
				out = append(out, wireCommand{Type: name, Payload: payload})
				break
			}
			if attempt == 60 {
				t.Fatalf("the content has nothing that a %s could change", name)
			}
			if attempt%2 == 0 {
				apply(need)
			} else {
				apply(creations[g.r.IntN(len(creations))])
			}
		}
	}
	// The project is neither in the trash nor archived as the commands are
	// sent, as the generators' batches may leave it.
	g.m = readUndoModel(t, f.admin, project)
	for g.m.deleted || g.m.archived {
		apply("")
		g.m = readUndoModel(t, f.admin, project)
	}
	return out
}
