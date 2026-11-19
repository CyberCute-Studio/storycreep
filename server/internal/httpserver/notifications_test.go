package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/clipboard"
	"github.com/CyberCute-Studio/storycreep/server/internal/comment"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/notification"
)

// notificationsPath is the path of the signed-in account's notification
// centre.
const notificationsPath = "/api/v1/notifications"

// readNotificationPath returns the path that marks the notification read.
func readNotificationPath(id string) string { return notificationsPath + "/" + id + "/read" }

// projectMembersPath returns the path of the list of the members whom the
// project's comments may mention.
func projectMembersPath(project string) string { return projectPath(project) + "/members" }

// memberInline returns a mention of the member, the user whose ID is user, as
// a rich text's inline node.
func memberInline(user string) string {
	return fmt.Sprintf(`{"type":"memberMention","attrs":{"id":%q}}`, user)
}

// mentioning returns a rich-text document of one paragraph that asks the
// users whose IDs are users, each mentioned.
func mentioning(users ...string) json.RawMessage {
	inlines := []string{textInline("Ask ")}
	for _, u := range users {
		inlines = append(inlines, memberInline(u), textInline(", "))
	}
	inlines = append(inlines, textInline("about the lamp."))
	return richText(inlines...)
}

// postMention sends a comment.create of a new comment on the target, whose
// body is body, as si, and returns the comment's ID and the answer.
func postMention(t *testing.T, h http.Handler, si signedIn, project, target string, body json.RawMessage) (string,
	response) {
	t.Helper()
	id := newCommentID()
	return id, call(h, http.MethodPost, commandsPath(project), commandBatch(t, wireCommand{Type: "comment.create",
		Payload: map[string]any{"id": id, "target": target, "body": body}}), si.header(true))
}

// userOf returns the ID of the user of the membership, as the API writes it.
func (f *rolesFixture) userOf(t *testing.T, membership string) string {
	t.Helper()
	var user ids.UUID
	if err := f.admin.QueryRow(context.Background(), "SELECT user_id FROM membership WHERE id = $1",
		decode(t, ids.Membership, membership)).Scan(&user); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.User, user)
}

// notificationsOf returns the notifications of si's centre, page by page, a
// notification each.
func notificationsOf(t *testing.T, h http.Handler, si signedIn) []notification.NotificationBody {
	t.Helper()
	return allPagesOf[notification.NotificationBody](t, h, si, notificationsPath, 1)
}

// unreadOf returns how many of si's notifications are unread.
func unreadOf(t *testing.T, h http.Handler, si signedIn) int64 {
	t.Helper()
	res := call(h, http.MethodGet, notificationsPath+"/unread", nil, si.header(false))
	var body unreadBody
	if res.status != http.StatusOK || json.Unmarshal(res.body, &body) != nil {
		t.Fatalf("the unread notifications: %d %s", res.status, res.body)
	}
	return body.Unread
}

// notificationComments returns the IDs of the comments of notifications, in
// their order.
func notificationComments(notifications []notification.NotificationBody) []string {
	out := []string{}
	for _, n := range notifications {
		out = append(out, n.CommentID)
	}
	return out
}

// envelopeMessage returns the message of the error envelope of res.
func envelopeMessage(t *testing.T, res response) string {
	t.Helper()
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res.body, &env); err != nil {
		t.Fatalf("%d %s: %v", res.status, res.body, err)
	}
	return env.Error.Message
}

// mentionNoAccessMessage is the message of mention_no_access, as the server's
// catalog has it in English.
const mentionNoAccessMessage = "The comment mentions someone who has no access to this project, so they cannot be " +
	"mentioned."

// TestMentionAccess checks whom a comment may mention (PRD FR-COMMENT-02): a
// mention of a user who cannot read the project, a user who is not a member
// of its workspace, a guest of it without a grant of the project, a member
// whose account is suspended, or an ID that is no account's, is refused with
// 422 mention_no_access at the mention's location, with the catalog's message,
// which names nobody, and applies nothing, the first such mention of a body
// that holds several; a viewer, who may not comment, a guest with a grant of
// the project, and the comment's author are mentioned. An edit keeps a
// mention of a member who has lost the project's access since, but refuses
// one that mentions such a member anew.
//
// Verifies: FR-COMMENT-02/AC1
func TestMentionAccess(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner := f.as["owner"]
	p, granted := f.project(t, ""), f.project(t, "")
	f.grant(t, f.membership(t, "guest"), granted, "viewer")
	node, grantedNode := f.node(t, p), f.node(t, granted)
	suspended := f.userOf(t, f.member(t, "editor"))
	if _, err := f.admin.Exec(context.Background(), "UPDATE \"user\" SET status = 'suspended' WHERE id = $1",
		decode(t, ids.User, suspended)); err != nil {
		t.Fatal(err)
	}
	viewer, guest := f.as["viewer"].body.UserID, f.as["guest"].body.UserID
	for name, user := range map[string]string{
		"a user who is not a member of the workspace": f.as["none"].body.UserID,
		"a guest without a grant of the project":      guest,
		"a member whose account is suspended":         suspended,
		"an ID that is no account's":                  ids.Encode(ids.User, ids.New()),
	} {
		_, res := postMention(t, f.s.main, owner, p, node, mentioning(viewer, user))
		code, locs, _ := problemsOf(t, res)
		if res.status != http.StatusUnprocessableEntity || code != api.CodeMentionNoAccess ||
			!slices.Equal(locs, []string{"body.commands[0].payload.body.content[0].content[3]"}) {
			t.Errorf("a mention of %s: %d %s, want 422 %s at the second mention", name, res.status, res.body,
				api.CodeMentionNoAccess)
		}
		if msg := envelopeMessage(t, res); msg != mentionNoAccessMessage {
			t.Errorf("a mention of %s is refused with %q, want %q", name, msg, mentionNoAccessMessage)
		}
		if strings.Contains(string(res.body), user) {
			t.Errorf("the refusal of a mention of %s names the user: %s", name, res.body)
		}
	}
	if got := listComments(t, f.s.main, owner, p); len(got) != 0 {
		t.Errorf("the refused mentions left the comments %v", commentIDs(got))
	}

	// A viewer, and the author, are mentioned on the project, and a guest
	// with a grant on the project granted to them.
	mentioned, res := postMention(t, f.s.main, owner, p, node, mentioning(viewer, owner.body.UserID))
	resultFrom(t, res)
	_, res = postMention(t, f.s.main, owner, granted, grantedNode, mentioning(guest))
	resultFrom(t, res)

	// The viewer leaves the workspace: an edit that keeps the mention of them
	// saves, but one that mentions anew a user who cannot read the project
	// does not.
	if _, err := f.admin.Exec(context.Background(), "DELETE FROM membership WHERE id = $1",
		decode(t, ids.Membership, f.membership(t, "viewer"))); err != nil {
		t.Fatal(err)
	}
	resultFrom(t, commentCommand(t, f.s.main, owner, p, "comment.edit", mentioned, map[string]any{
		"body": richText(memberInline(viewer), textInline(", is the lamp lit?"))}))
	res = commentCommand(t, f.s.main, owner, p, "comment.edit", mentioned, map[string]any{
		"body": richText(memberInline(viewer), memberInline(guest))})
	if code, locs, _ := problemsOf(t, res); res.status != http.StatusUnprocessableEntity ||
		code != api.CodeMentionNoAccess ||
		!slices.Equal(locs, []string{"body.commands[0].payload.body.content[0].content[1]"}) {
		t.Errorf("an edit that mentions a guest without a grant anew: %d %s, want 422 %s at the mention",
			res.status, res.body, api.CodeMentionNoAccess)
	}
}

// TestMentionBody checks the rules of a comment's body that mentions members:
// a body of nothing but a mention is a comment, as "@Tomas" is; a member
// mentioned twice counts once, and the 51st member that a body mentions is
// refused at its mention with 422 validation_failed; a mention of a member in
// a node's text, in a connection's label, in a component's description, or
// in the text of a pasted clipboard, which only a comment's body may hold, is
// refused at its location with 422 validation_failed, and the list of a
// project's comments names the members that each comment mentions, with
// their display names as they are now.
func TestMentionBody(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner, editor, viewer := f.as["owner"], f.as["editor"], f.as["viewer"]
	p := f.project(t, "")
	node := f.node(t, p)

	alone, res := postMention(t, f.s.main, owner, p, node, richText(memberInline(viewer.body.UserID)))
	resultFrom(t, res)
	twice, res := postMention(t, f.s.main, owner, p, node, mentioning(editor.body.UserID, viewer.body.UserID,
		editor.body.UserID))
	resultFrom(t, res)

	// 50 members, each of whom is one, and a 51st.
	var members []string
	for range comment.MaxMentions {
		members = append(members, f.userOf(t, f.member(t, "viewer")))
	}
	_, res = postMention(t, f.s.main, owner, p, node, mentioning(append(members, members[0])...))
	resultFrom(t, res)
	extra := f.userOf(t, f.member(t, "viewer"))
	_, res = postMention(t, f.s.main, owner, p, node, mentioning(append(members, members[0], extra)...))
	if code, locs, _ := problemsOf(t, res); res.status != http.StatusUnprocessableEntity ||
		code != api.CodeValidationFailed ||
		!slices.Equal(locs, []string{fmt.Sprintf("body.commands[0].payload.body.content[0].content[%d]",
			2*(comment.MaxMentions+1)+1)}) {
		t.Errorf("a comment that mentions %d members: %d %.400s, want 422 at the last mention",
			comment.MaxMentions+1, res.status, res.body)
	}

	// The list names whom each comment mentions, once, in the order of their
	// first mentions, with their display names as they are now.
	if _, err := f.admin.Exec(context.Background(), "UPDATE \"user\" SET display_name = 'Tomas' WHERE id = $1",
		decode(t, ids.User, editor.body.UserID)); err != nil {
		t.Fatal(err)
	}
	byID := map[string]comment.CommentBody{}
	for _, c := range listComments(t, f.s.main, owner, p) {
		byID[c.ID] = c
	}
	if got := byID[alone].Mentions; !slices.Equal(got, []comment.CommentMention{{ID: viewer.body.UserID,
		DisplayName: "Viewer"}}) {
		t.Errorf("the comment of a mention alone names %+v", got)
	}
	if got := byID[twice].Mentions; !slices.Equal(got, []comment.CommentMention{
		{ID: editor.body.UserID, DisplayName: "Tomas"}, {ID: viewer.body.UserID, DisplayName: "Viewer"}}) {
		t.Errorf("the comment that mentions the editor twice names %+v", got)
	}

	// A text of the story that mentions a member.
	component := f.component(t, p, f.componentType(t, p))
	connection := f.connection(t, p)
	board := f.board(t, p)
	text := richText(textInline("Ask "), memberInline(viewer.body.UserID))
	for _, c := range []struct {
		cmd wireCommand
		loc string
	}{
		{wireCommand{Type: "node.update", Payload: map[string]any{"id": node, "text": text}},
			"body.commands[0].payload.text.content[0].content[1]"},
		{wireCommand{Type: "connection.update", Payload: map[string]any{"id": connection, "label": text}},
			"body.commands[0].payload.label.content[0].content[1]"},
		{wireCommand{Type: "component.describe", Payload: map[string]any{"id": component, "description": text}},
			"body.commands[0].payload.description.content[0].content[1]"},
		{wireCommand{Type: "node.create", Payload: map[string]any{"id": newNodeID(), "board_id": board,
			"type": "element", "kind": "dialogue", "position": map[string]any{"x": 0, "y": 0}, "text": text}},
			"body.commands[0].payload.text.content[0].content[1]"},
	} {
		res := call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, c.cmd), owner.header(true))
		if code, locs, _ := problemsOf(t, res); res.status != http.StatusUnprocessableEntity ||
			code != api.CodeValidationFailed || !slices.Equal(locs, []string{c.loc}) {
			t.Errorf("%s of a text that mentions a member: %d %.400s, want 422 at %s", c.cmd.Type, res.status,
				res.body, c.loc)
		}
	}
	copied := call(f.s.main, http.MethodPost, copyPath(p), jsonBody(t, map[string]any{"node_ids": []string{node}}),
		owner.header(true))
	var c clipboard.Clipboard
	if copied.status != http.StatusOK || json.Unmarshal(copied.body, &c) != nil || len(c.Nodes) != 1 {
		t.Fatalf("copy the node: %d %s", copied.status, copied.body)
	}
	c.Nodes[0].Text = clipboard.Text(text)
	cmd, _ := paste(board, c, 10, 10)
	res = call(f.s.main, http.MethodPost, commandsPath(p), commandBatch(t, cmd), owner.header(true))
	if code, locs, _ := problemsOf(t, res); res.status != http.StatusUnprocessableEntity ||
		code != api.CodeValidationFailed ||
		!slices.Equal(locs, []string{"body.commands[0].payload.clipboard.nodes[0].text.content[0].content[1]"}) {
		t.Errorf("a paste of a text that mentions a member: %d %.400s, want 422 at its mention", res.status,
			res.body)
	}
}

// TestMentionNotifications checks that a session of the mentioned member is
// told of a comment that mentions them on the real-time channel within 2
// seconds of the batch's commit (PRD FR-COMMENT-02 AC2), however many of
// their sessions are open, while a socket of an API token of theirs, and a
// socket of another member's session, are told nothing, and that marking the
// notification read tells the member's sessions too.
//
// Verifies: FR-COMMENT-02/AC2
func TestMentionNotifications(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	srv := realtimeServer(t, f.s)
	owner, viewer, editor := f.as["owner"], f.as["viewer"], f.as["editor"]
	p := f.project(t, "")
	node := f.node(t, p)
	token := f.tokenWithSecret(t, viewer)

	// The first socket of a session starts the hub's LISTEN connection, and
	// is told once it LISTENs, after which the hub misses nothing: the other
	// sockets open once it has been.
	other := dialSocket(t, srv, editor.header(false))
	waitNotified(t, other, 10*time.Second)
	session := dialSocket(t, srv, viewer.header(false))
	second := dialSocket(t, srv, signIn(t, f.s.main, "as-viewer@example.com").header(false))
	byToken := dialSocket(t, srv, bearer(token.Secret))
	// Each socket is served once it has answered a subscription, which a
	// socket of a session is entered in its user's inbox before.
	for _, s := range []*testSocket{other, session, second, byToken} {
		s.subscribe(p, -1)
		s.next("subscribed")
	}

	id, res := postMention(t, f.s.main, owner, p, node, mentioning(viewer.body.UserID))
	committed := time.Now()
	resultFrom(t, res)
	for name, s := range map[string]*testSocket{"a session": session, "a second session": second} {
		at := waitNotified(t, s, 2*time.Second)
		if took := at.Sub(committed); took > 2*time.Second {
			t.Errorf("%s of the mentioned member was told of the mention %v after the batch", name, took)
		}
	}
	for name, s := range map[string]*testSocket{"a token's socket": byToken, "another member's session": other} {
		select {
		case <-s.notified:
			t.Errorf("%s was told of another's notification", name)
		case <-time.After(500 * time.Millisecond):
		}
	}

	// Marking it read tells the member's sessions, which read it read.
	notifications := notificationsOf(t, f.s.main, viewer)
	if len(notifications) != 1 || notifications[0].CommentID != id {
		t.Fatalf("the member's notifications are %v, want one of %s", notificationComments(notifications), id)
	}
	if res := call(f.s.main, http.MethodPost, readNotificationPath(notifications[0].ID), nil,
		viewer.header(true)); res.status != http.StatusNoContent {
		t.Fatalf("mark the notification read: %d %s", res.status, res.body)
	}
	waitNotified(t, session, 2*time.Second)
	waitNotified(t, second, 2*time.Second)

	// An edit of the comment, which changes the notification's preview, and
	// its deletion, which hides it, tell the member's sessions too.
	resultFrom(t, commentCommand(t, f.s.main, owner, p, "comment.edit", id, map[string]any{
		"body": richText(memberInline(viewer.body.UserID), textInline(", is the lamp lit?"))}))
	waitNotified(t, session, 2*time.Second)
	waitNotified(t, second, 2*time.Second)
	if got := notificationsOf(t, f.s.main, viewer); len(got) != 1 || got[0].Preview != "@Viewer, is the lamp lit?" {
		t.Errorf("after the edit, the member's notifications are %+v", got)
	}
	resultFrom(t, commentCommand(t, f.s.main, owner, p, "comment.delete", id, nil))
	waitNotified(t, session, 2*time.Second)
	waitNotified(t, second, 2*time.Second)
}

// waitNotified waits for the socket to be told that its account's
// notifications changed, within d, and returns when it was.
func waitNotified(t *testing.T, s *testSocket, d time.Duration) time.Time {
	t.Helper()
	select {
	case at := <-s.notified:
		return at
	case <-time.After(d):
		t.Fatalf("the socket was not told of the account's notifications within %v", d)
	}
	return time.Time{}
}

// TestNotificationCentre checks the centre of an account's notifications:
// one of each comment that mentions the account, across the workspaces that
// it is a member of, newest first, a page at a time; none of the account's
// own comments, nor of a reply or a resolution that mentions nobody; an edit
// that adds a mention notifies the member that it adds alone, once, and one
// that takes it out leaves the notification; a notification is hidden while
// its comment is deleted, its thread's target is in the trash, its project
// is in the trash, or the account cannot read the project, and back as it
// was, read or not, once that ends; the unread count follows; one is marked
// read, and all up to one, but for those made after it; and what the
// account does not have is not found.
func TestNotificationCentre(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	owner, viewer, editor := f.as["owner"], f.as["viewer"], f.as["editor"]
	me := viewer.body.UserID
	p, q := f.project(t, ""), f.project(t, "")
	node, other := f.node(t, p), f.node(t, q)

	// A second workspace, of the editor's, which the viewer is a guest of
	// with a grant of its project.
	elsewhere := workspaceFrom(t, call(f.s.main, http.MethodPost, workspacesPath, jsonBody(t,
		map[string]string{"name": "Elsewhere", "slug": "elsewhere"}), editor.header(true)), http.StatusCreated).ID
	join(t, f.d, viewer, elsewhere, "guest")
	far := createProject(t, f.s.main, editor, elsewhere, "Far").ID
	var membership ids.UUID
	if err := f.admin.QueryRow(context.Background(), "SELECT id FROM membership WHERE workspace_id = $1 AND "+
		"user_id = $2", decode(t, ids.Workspace, elsewhere), decode(t, ids.User, me)).Scan(&membership); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO project_grant (id, workspace_id, membership_id,
		project_id, role) VALUES ($1, $2, $3, $4, 'commenter')`, ids.New(), decode(t, ids.Workspace, elsewhere),
		membership, decode(t, ids.Project, far)); err != nil {
		t.Fatal(err)
	}
	farBoard, farNode := newBoardID(), newNodeID()
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(far), commandBatch(t,
		wireCommand{Type: "board.create", Payload: map[string]string{"id": farBoard, "name": "Pier"}},
		wireCommand{Type: "node.create", Payload: map[string]any{"id": farNode, "board_id": farBoard,
			"type": "element", "kind": "dialogue", "position": map[string]any{"x": 0, "y": 0}}}), editor.header(true)))

	first, res := postMention(t, f.s.main, owner, p, node, mentioning(me))
	resultFrom(t, res)
	// The viewer's own comment, which mentions them, notifies nobody; nor
	// does a reply that mentions nobody, or a resolution.
	_, res = postMention(t, f.s.main, f.as["commenter"], p, node, paragraph("Not me."))
	resultFrom(t, res)
	resultFrom(t, postComment(t, f.s.main, editor, p, newCommentID(), "", first, "Not yet."))
	resultFrom(t, commentCommand(t, f.s.main, editor, p, "comment.resolve", first, nil))
	farComment, res := postMention(t, f.s.main, editor, far, farNode, mentioning(me, editor.body.UserID))
	resultFrom(t, res)
	plain, res := postMention(t, f.s.main, owner, q, other, paragraph("Nobody yet."))
	resultFrom(t, res)
	// An edit that adds the mention notifies, once, and only the member that
	// it adds.
	resultFrom(t, commentCommand(t, f.s.main, owner, q, "comment.edit", plain, map[string]any{
		"body": mentioning(me)}))
	resultFrom(t, commentCommand(t, f.s.main, owner, q, "comment.edit", plain, map[string]any{
		"body": mentioning(me, editor.body.UserID)}))
	resultFrom(t, commentCommand(t, f.s.main, owner, q, "comment.edit", plain, map[string]any{
		"body": paragraph("Never mind.")}))
	resultFrom(t, commentCommand(t, f.s.main, owner, q, "comment.edit", plain, map[string]any{
		"body": mentioning(me)}))

	all := notificationsOf(t, f.s.main, viewer)
	if got, want := notificationComments(all), []string{plain, farComment, first}; !slices.Equal(got, want) {
		t.Fatalf("the viewer's notifications are of %v, want %v", got, want)
	}
	n := all[1]
	if n.Kind != notification.Mention || n.WorkspaceID != elsewhere || n.Project.ID != far ||
		n.Project.Name != "Far" || n.ThreadID != farComment || n.TargetType != "node" || n.TargetID != farNode ||
		n.NodeID != farNode || n.BoardID == "" || n.Actor == nil || n.Actor.ID != editor.body.UserID ||
		n.ReadAt != nil || n.Preview != "Ask @Viewer, @Editor, about the lamp." {
		t.Errorf("the notification of the other workspace's comment is %+v", n)
	}
	if got := notificationComments(notificationsOf(t, f.s.main, editor)); !slices.Equal(got, []string{plain}) {
		t.Errorf("the editor's notifications are of %v, want the comment whose edit mentioned them alone", got)
	}
	if got := unreadOf(t, f.s.main, viewer); got != 3 {
		t.Errorf("the viewer has %d unread notifications, want 3", got)
	}

	// The centre is read a page at a time, which a notification made between
	// two pages does not change.
	res = call(f.s.main, http.MethodGet, notificationsPath+"?limit=2", nil, viewer.header(false))
	var page api.Page[notification.NotificationBody]
	if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil || page.NextCursor == nil ||
		len(page.Items) != 2 {
		t.Fatalf("the first page: %d %s", res.status, res.body)
	}
	latest, res := postMention(t, f.s.main, owner, p, node, mentioning(me))
	resultFrom(t, res)
	res = call(f.s.main, http.MethodGet, notificationsPath+"?limit=2&cursor="+*page.NextCursor, nil,
		viewer.header(false))
	var next api.Page[notification.NotificationBody]
	if res.status != http.StatusOK || json.Unmarshal(res.body, &next) != nil || next.NextCursor != nil ||
		!slices.Equal(notificationComments(next.Items), []string{first}) {
		t.Errorf("the second page: %d %s, want the oldest notification alone", res.status, res.body)
	}

	// Mark all read through the first page's first, which leaves the one made
	// after it unread.
	if res := call(f.s.main, http.MethodPost, notificationsPath+"/read-all", jsonBody(t,
		map[string]string{"through": page.Items[0].ID}), viewer.header(true)); res.status != http.StatusNoContent {
		t.Fatalf("mark all read: %d %s", res.status, res.body)
	}
	read := map[string]bool{}
	for _, n := range notificationsOf(t, f.s.main, viewer) {
		read[n.CommentID] = n.ReadAt != nil
	}
	if want := map[string]bool{latest: false, plain: true, farComment: true, first: true}; !mapsEqual(read, want) {
		t.Errorf("after marking all read, the notifications read are %v, want %v", read, want)
	}
	if got := unreadOf(t, f.s.main, viewer); got != 1 {
		t.Errorf("the viewer has %d unread notifications, want 1", got)
	}

	// Hidden while the comment is deleted, while its target is in the trash,
	// and while its project is, and back as it was once restored.
	shown := func(want ...string) {
		t.Helper()
		if got := notificationComments(notificationsOf(t, f.s.main, viewer)); !slices.Equal(got, want) {
			t.Errorf("the notifications shown are of %v, want %v", got, want)
		}
	}
	resultFrom(t, commentCommand(t, f.s.main, owner, p, "comment.delete", latest, nil))
	shown(plain, farComment, first)
	if got := unreadOf(t, f.s.main, viewer); got != 0 {
		t.Errorf("with the unread one's comment deleted, %d are unread", got)
	}
	resultFrom(t, commentCommand(t, f.s.main, owner, p, "comment.restore", latest, nil))
	shown(latest, plain, farComment, first)
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(q), commandBatch(t, wireCommand{
		Type: "node.delete", Payload: map[string]string{"id": other}}), owner.header(true)))
	shown(latest, farComment, first)
	resultFrom(t, call(f.s.main, http.MethodPost, commandsPath(q), commandBatch(t, wireCommand{
		Type: "node.restore", Payload: map[string]string{"id": other}}), owner.header(true)))
	shown(latest, plain, farComment, first)
	if _, err := f.admin.Exec(context.Background(), "UPDATE project SET deleted_at = now(), hard_delete_after = "+
		"now() + interval '30 days' WHERE id = $1", decode(t, ids.Project, far)); err != nil {
		t.Fatal(err)
	}
	shown(latest, plain, first)
	if _, err := f.admin.Exec(context.Background(), "UPDATE project SET deleted_at = NULL, hard_delete_after = "+
		"NULL WHERE id = $1", decode(t, ids.Project, far)); err != nil {
		t.Fatal(err)
	}
	shown(latest, plain, farComment, first)
	// The guest's grant is the access: without it the notification is
	// hidden, and back, read, with it.
	if _, err := f.admin.Exec(context.Background(), "DELETE FROM project_grant WHERE membership_id = $1",
		membership); err != nil {
		t.Fatal(err)
	}
	shown(latest, plain, first)
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO project_grant (id, workspace_id, membership_id,
		project_id, role) VALUES ($1, $2, $3, $4, 'viewer')`, ids.New(), decode(t, ids.Workspace, elsewhere),
		membership, decode(t, ids.Project, far)); err != nil {
		t.Fatal(err)
	}
	if got := notificationsOf(t, f.s.main, viewer); len(got) != 4 || got[2].CommentID != farComment ||
		got[2].ReadAt == nil {
		t.Errorf("once the grant is back, the notifications are %v, want the other workspace's back, read",
			notificationComments(got))
	}

	// Mark one read; it stays so; what the account does not have is not
	// found.
	if res := call(f.s.main, http.MethodPost, readNotificationPath(all[0].ID), nil,
		viewer.header(true)); res.status != http.StatusNoContent {
		t.Errorf("mark a read notification read: %d %s", res.status, res.body)
	}
	unreadID := ""
	for _, n := range notificationsOf(t, f.s.main, viewer) {
		if n.ReadAt == nil {
			unreadID = n.ID
		}
	}
	if res := call(f.s.main, http.MethodPost, readNotificationPath(unreadID), nil,
		viewer.header(true)); res.status != http.StatusNoContent {
		t.Errorf("mark the unread notification read: %d %s", res.status, res.body)
	}
	if got := unreadOf(t, f.s.main, viewer); got != 0 {
		t.Errorf("after marking the last read, %d are unread", got)
	}
	editorsOwn := notificationsOf(t, f.s.main, editor)[0].ID
	for name, id := range map[string]string{"another account's notification": editorsOwn,
		"no notification": ids.Encode(ids.Notification, ids.New())} {
		wantCode(t, "mark "+name+" read", call(f.s.main, http.MethodPost, readNotificationPath(id), nil,
			viewer.header(true)), http.StatusNotFound, api.CodeNotFound)
		wantCode(t, "mark all read through "+name, call(f.s.main, http.MethodPost, notificationsPath+"/read-all",
			jsonBody(t, map[string]string{"through": id}), viewer.header(true)), http.StatusNotFound,
			api.CodeNotFound)
	}
	wantCode(t, "mark a notification of another kind's ID read", call(f.s.main, http.MethodPost,
		readNotificationPath(node), nil, viewer.header(true)), http.StatusBadRequest, api.CodeInvalidID)
	wantCode(t, "a page of a cursor that is none", call(f.s.main, http.MethodGet, notificationsPath+"?cursor=x",
		nil, viewer.header(false)), http.StatusBadRequest, api.CodeInvalidCursor)
	// The centre takes a session alone.
	wantCode(t, "the centre with a token", call(f.s.main, http.MethodGet, notificationsPath, nil,
		bearer(f.tokenWithSecret(t, viewer).Secret)), http.StatusForbidden, api.CodeSessionRequired)
}

// mapsEqual reports whether a and b hold the same keys and values.
func mapsEqual[K comparable, V comparable](a, b map[K]V) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// TestProjectMembers checks the list of the members whom a project's comments
// may mention, which every role reads: the members of the workspace who read
// the project, of every role, and a guest with a grant of it, in the order of
// their IDs, a page at a time, without a guest without a grant, a member
// whose account is suspended, or a user who is not a member.
func TestProjectMembers(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	p := f.project(t, "")
	f.grant(t, f.membership(t, "guest"), p, "viewer")
	suspended := f.userOf(t, f.member(t, "editor"))
	if _, err := f.admin.Exec(context.Background(), "UPDATE \"user\" SET status = 'suspended' WHERE id = $1",
		decode(t, ids.User, suspended)); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, r := range matrixRoles {
		if r != "none" {
			want = append(want, f.as[r].body.UserID)
		}
	}
	slices.SortFunc(want, func(a, b string) int {
		return strings.Compare(decode(t, ids.User, a).String(), decode(t, ids.User, b).String())
	})
	for _, r := range matrixRoles[:len(matrixRoles)-1] {
		got := allPagesOf[comment.MentionableMember](t, f.s.main, f.as[r], projectMembersPath(p), 2)
		var gotIDs []string
		for _, m := range got {
			gotIDs = append(gotIDs, m.ID)
			if m.DisplayName == "" {
				t.Errorf("the member %s has no display name", m.ID)
			}
		}
		if !slices.Equal(gotIDs, want) {
			t.Errorf("as the %s, the members are %v, want %v", r, gotIDs, want)
		}
	}
	// A guest without a grant of another project is not among its members.
	other := f.project(t, "")
	for _, m := range allPagesOf[comment.MentionableMember](t, f.s.main, f.as["owner"], projectMembersPath(other),
		50) {
		if m.ID == f.as["guest"].body.UserID {
			t.Errorf("a guest without a grant is among the members of the project")
		}
	}
}
