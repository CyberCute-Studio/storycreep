package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/realtime"
)

// socketMessage is a message of the real-time channel, with the fields of
// every type of the server's.
type socketMessage struct {
	Type       string `json:"type"`
	ProjectID  string `json:"project_id"`
	Seq        int64  `json:"seq"`
	Code       string `json:"code"`
	Reason     string `json:"reason"`
	RetryAfter int    `json:"retry_after"`
	Commands   []struct {
		CommandID string          `json:"command_id"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	} `json:"commands"`
	Revisions []struct {
		ID  string `json:"id"`
		Rev int64  `json:"rev"`
	} `json:"revisions"`
	CreatedAt time.Time `json:"created_at"`
}

// testSocket is a client's socket of the real-time channel, whose messages a
// goroutine of its own reads, since a read whose context ends closes the
// socket.
type testSocket struct {
	t  *testing.T
	ws *websocket.Conn
	// received are the server's messages but the heartbeats, in order, and
	// then the error that ended the socket.
	received chan received
}

// received is a message that a testSocket read, or the error that ended it.
type received struct {
	msg socketMessage
	raw string
	err error
}

// socketURL returns the URL of the real-time channel of the server srv.
func socketURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + realtimePath
}

// dialSocket opens a socket of the real-time channel of srv, with the
// request's header, which must upgrade, and reads its messages from then on.
func dialSocket(t *testing.T, srv *httptest.Server, header http.Header) *testSocket {
	t.Helper()
	s := openSocket(t, srv, header)
	s.start()
	return s
}

// openSocket opens a socket of the real-time channel of srv, with the
// request's header, which must upgrade, and reads none of its messages, nor
// answers its pings, which the client library answers as it reads, until
// start.
func openSocket(t *testing.T, srv *httptest.Server, header http.Header) *testSocket {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, res, err := websocket.Dial(ctx, socketURL(srv), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		t.Fatalf("dial the real-time channel: %d %v", status, err)
	}
	ws.SetReadLimit(16 << 20)
	t.Cleanup(func() { _ = ws.CloseNow() })
	return &testSocket{t: t, ws: ws, received: make(chan received, 1024)}
}

// start reads the socket's messages, in a goroutine of its own, from now on.
func (s *testSocket) start() {
	ws := s.ws
	go func() {
		for {
			typ, data, err := ws.Read(context.Background())
			if err == nil && typ != websocket.MessageText {
				err = errors.New("the server sent a binary message")
			}
			if err != nil {
				s.received <- received{err: err}
				close(s.received)
				return
			}
			var m socketMessage
			if err := json.Unmarshal(data, &m); err != nil {
				s.received <- received{err: err}
				close(s.received)
				return
			}
			if m.Type != "heartbeat" {
				s.received <- received{msg: m, raw: string(data)}
			}
		}
	}()
}

// send sends msg, as JSON.
func (s *testSocket) send(msg any) {
	s.t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		s.t.Fatal(err)
	}
	s.write(websocket.MessageText, data)
}

// write writes a message of the type typ.
func (s *testSocket) write(typ websocket.MessageType, data []byte) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.ws.Write(ctx, typ, data); err != nil {
		s.t.Fatal(err)
	}
}

// subscribe subscribes the socket to the project, since the seq, unless it
// is negative, when it subscribes without since.
func (s *testSocket) subscribe(project string, since int64) {
	s.t.Helper()
	msg := map[string]any{"type": "subscribe", "project_id": project}
	if since >= 0 {
		msg["since"] = since
	}
	s.send(msg)
}

// receive returns the next message of the server that is not a heartbeat,
// or the error that ended the socket, within 10 seconds.
func (s *testSocket) receive() received {
	select {
	case r, ok := <-s.received:
		if !ok {
			return received{err: errors.New("the socket has ended")}
		}
		return r
	case <-time.After(10 * time.Second):
		return received{err: errors.New("no message in 10 seconds")}
	}
}

// next returns the next message of the server that is not a heartbeat, which
// must be of the type typ.
func (s *testSocket) next(typ string) socketMessage {
	s.t.Helper()
	r := s.receive()
	if r.err != nil {
		s.t.Fatalf("waiting for %s: %v", typ, r.err)
	}
	if r.msg.Type != typ {
		s.t.Fatalf("got %s, want %s", r.raw, typ)
	}
	return r.msg
}

// quiet checks that the server sends nothing but heartbeats for half a
// second.
func (s *testSocket) quiet() {
	s.t.Helper()
	select {
	case r := <-s.received:
		s.t.Errorf("the server sent %s (%v), want nothing", r.raw, r.err)
	case <-time.After(500 * time.Millisecond):
	}
}

// closedWith returns the code that the server closed the socket with, once
// it does, within 10 seconds, reading past the messages before it.
func (s *testSocket) closedWith() websocket.StatusCode {
	s.t.Helper()
	for {
		r := s.receive()
		if r.err == nil {
			continue
		}
		code := websocket.CloseStatus(r.err)
		if code == -1 {
			s.t.Fatalf("the socket ended without a close: %v", r.err)
		}
		return code
	}
}

// realtimeServer returns the fixture's server listening for HTTP, which a
// socket can upgrade on, as no ResponseRecorder can.
func realtimeServer(t *testing.T, s *server) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(s.main)
	t.Cleanup(srv.Close)
	return srv
}

// createBoards creates a board in each of n batches of the project, as si,
// and returns the IDs of the boards and the commands, in order.
func createBoards(t *testing.T, h http.Handler, si signedIn, project string, n int) (boards, commandIDs []string) {
	t.Helper()
	for range n {
		id, command := newBoardID(), ids.New().String()
		res := call(h, http.MethodPost, commandsPath(project), commandBatch(t, wireCommand{ID: command,
			Type: "board.create", Payload: map[string]string{"id": id, "name": "Board " + id[len(id)-6:]}}),
			si.header(true))
		if res.status != http.StatusOK {
			t.Fatalf("create a board: %d %s", res.status, res.body)
		}
		boards, commandIDs = append(boards, id), append(commandIDs, command)
	}
	return boards, commandIDs
}

// wantOperations reads n operation messages of the project, which must be
// those of the seqs after since, in order, each the creation of the board of
// boards at its index by the command of commandIDs.
func (s *testSocket) wantOperations(project string, since int64, boards, commandIDs []string) {
	s.t.Helper()
	for i := range boards {
		m := s.next("operation")
		want := since + int64(i) + 1
		switch {
		case m.ProjectID != project || m.Seq != want:
			s.t.Errorf("operation %d is of %s, seq %d, want %s, seq %d", i, m.ProjectID, m.Seq, project, want)
		case len(m.Commands) != 1 || m.Commands[0].CommandID != commandIDs[i] || m.Commands[0].Type != "board.create" ||
			!strings.Contains(string(m.Commands[0].Payload), boards[i]):
			s.t.Errorf("operation %d holds %+v, want the creation of %s by %s", i, m.Commands, boards[i], commandIDs[i])
		case !slices.ContainsFunc(m.Revisions, func(r struct {
			ID  string `json:"id"`
			Rev int64  `json:"rev"`
		}) bool {
			return r.ID == boards[i] && r.Rev == 1
		}):
			s.t.Errorf("operation %d changed %+v, want %s at rev 1 among them", i, m.Revisions, boards[i])
		case time.Since(m.CreatedAt) > time.Minute:
			s.t.Errorf("operation %d applied at %v", i, m.CreatedAt)
		}
	}
}

// TestRealtimeCatchUp checks that a subscriber is sent each batch that
// commits to its project, in order, and that a client that disconnects and
// subscribes again with the seq of the last operation that it has is sent
// exactly the operations that committed meanwhile, in order, and then the
// later ones, however long it was away, since the operation log, not the
// socket, holds them (FR-RT-01 AC1, AC3).
//
// Verifies: FR-RT-01/AC1, FR-RT-01/AC3
func TestRealtimeCatchUp(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	srv := realtimeServer(t, s)
	project := createProject(t, s.main, owner, ws, "Harbour").ID

	sock := dialSocket(t, srv, owner.header(false))
	sock.subscribe(project, -1)
	subscribed := sock.next("subscribed")
	if subscribed.ProjectID != project {
		t.Fatalf("subscribed to %s, want %s", subscribed.ProjectID, project)
	}
	seq := subscribed.Seq
	boards, commandIDs := createBoards(t, s.main, owner, project, 3)
	sock.wantOperations(project, seq, boards, commandIDs)
	seq += 3
	_ = sock.ws.Close(websocket.StatusNormalClosure, "")

	missed, missedIDs := createBoards(t, s.main, owner, project, 4)
	again := dialSocket(t, srv, owner.header(false))
	again.subscribe(project, seq)
	if m := again.next("subscribed"); m.Seq != seq+4 {
		t.Errorf("subscribed at seq %d, want %d", m.Seq, seq+4)
	}
	again.wantOperations(project, seq, missed, missedIDs)
	again.quiet()
	seq += 4
	later, laterIDs := createBoards(t, s.main, owner, project, 1)
	again.wantOperations(project, seq, later, laterIDs)

	// A subscription from 0 is sent the project's whole log.
	whole := dialSocket(t, srv, owner.header(false))
	whole.subscribe(project, 0)
	whole.next("subscribed")
	for want := int64(1); want <= seq+1; want++ {
		if m := whole.next("operation"); m.Seq != want {
			t.Fatalf("the whole log's operation is of seq %d, want %d", m.Seq, want)
		}
	}
	whole.quiet()
}

// TestRealtimeSubscriptionRefusedAlike checks that a subscription to a
// project that the user cannot read is refused exactly as one to a project
// that does not exist: a member without a role on it, a guest without a
// grant, one in the trash, and a token narrowed to another project, are all
// answered with not_found, and nothing else tells them apart (FR-RT-01 AC2).
// A project that the user reads is subscribed to.
//
// Verifies: FR-RT-01/AC2
func TestRealtimeSubscriptionRefusedAlike(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	srv := realtimeServer(t, f.s)
	project, trashed := f.project(t, ""), f.project(t, "deleted")
	missing := ids.Encode(ids.Project, ids.New())
	narrowed := makeToken(t, f.s.main, f.as["owner"], projectTokensPath(f.project(t, "")), "project:read")

	// refusal returns the refusal of a subscription to p, as JSON, without
	// its project_id, which names p.
	refusal := func(sock *testSocket, p string) string {
		t.Helper()
		sock.subscribe(p, 0)
		m := sock.next("subscription_failed")
		if m.ProjectID != p {
			t.Errorf("the refusal names %s, want %s", m.ProjectID, p)
		}
		m.ProjectID = ""
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	owner := dialSocket(t, srv, f.as["owner"].header(false))
	want := refusal(owner, missing)
	if !strings.Contains(want, `"code":"not_found"`) {
		t.Fatalf("a project that does not exist is refused with %s, want not_found", want)
	}
	for name, c := range map[string]struct {
		header  http.Header
		project string
	}{
		"a project of a workspace that the user is no member of": {f.as["none"].header(false), project},
		"a project not granted to the guest":                     {f.as["guest"].header(false), project},
		"a project in the trash":                                 {f.as["owner"].header(false), trashed},
		"a project that the token is not narrowed to":            {bearer(narrowed.Secret), project},
	} {
		sock := dialSocket(t, srv, c.header)
		if got := refusal(sock, c.project); got != want {
			t.Errorf("%s is refused with %s, want %s, as a project that does not exist is", name, got, want)
		}
		if got := refusal(sock, missing); got != want {
			t.Errorf("for %s, a project that does not exist is refused with %s, want %s", name, got, want)
		}
	}
	owner.subscribe(project, 0)
	if m := owner.next("subscribed"); m.ProjectID != project || m.Seq != 0 {
		t.Errorf("the owner subscribed with %+v", m)
	}
	viewer := dialSocket(t, srv, f.as["viewer"].header(false))
	viewer.subscribe(project, 0)
	viewer.next("subscribed")
}

// TestRealtimeListenerReconnects checks that the operations that commit while
// the hub's LISTEN connection is gone, whose notifications nobody hears, are
// sent all the same once it LISTENs again, in order, whether or not another
// socket subscribes to the project meanwhile: the notifications are only a
// way to be told sooner, and the log the source of truth (FR-RT-01 AC3,
// ADR-005).
//
// Verifies: FR-RT-01/AC3
func TestRealtimeListenerReconnects(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	srv := realtimeServer(t, s)
	project := createProject(t, s.main, owner, ws, "Harbour").ID
	sock := dialSocket(t, srv, owner.header(false))
	sock.subscribe(project, -1)
	seq := sock.next("subscribed").Seq
	boards, commandIDs := createBoards(t, s.main, owner, project, 1)
	sock.wantOperations(project, seq, boards, commandIDs)
	seq++

	// The hub LISTENs on a connection of the application login, which the
	// superuser ends; the hub waits a second before it connects again.
	ctx := context.Background()
	var ended int
	if err := d.Admin(t).QueryRow(ctx, `SELECT count(*) FROM (SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = current_database() AND query = $1) t`, `LISTEN "`+realtime.Channel+`"`).Scan(&ended); err != nil {
		t.Fatal(err)
	}
	if ended != 1 {
		t.Fatalf("ended %d LISTEN connections, want the hub's", ended)
	}
	missed, missedIDs := createBoards(t, s.main, owner, project, 3)
	// A socket that subscribes meanwhile reads the seq that the hub was not
	// told of, which wakes the first socket, since the poll once the hub
	// LISTENs again, which compares the seq with the latest that the hub
	// knows of, finds nothing new then.
	other := dialSocket(t, srv, owner.header(false))
	other.subscribe(project, -1)
	if m := other.next("subscribed"); m.Seq != seq+3 {
		t.Errorf("the socket subscribed meanwhile at seq %d, want %d", m.Seq, seq+3)
	}
	sock.wantOperations(project, seq, missed, missedIDs)
	seq += 3
	later, laterIDs := createBoards(t, s.main, owner, project, 1)
	sock.wantOperations(project, seq, later, laterIDs)
}

// TestRealtimeListenerPings checks that the hub's LISTEN connection, which
// it pings once it has heard nothing for the poll's interval, so that one
// whose database went away without a word is found gone, stays the same
// connection, LISTENing, through its pings: the backend's pid stays, and its
// last statement, the LISTEN that pings it, starts anew.
func TestRealtimeListenerPings(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	s.hub = realtime.New(realtime.Options{DB: readyDB{s.ready}, Projects: s.projects,
		PollInterval: 100 * time.Millisecond})
	srv := realtimeServer(t, s)
	project := createProject(t, s.main, owner, ws, "Harbour").ID
	sock := dialSocket(t, srv, owner.header(false))
	sock.subscribe(project, -1)
	seq := sock.next("subscribed").Seq

	// listener returns the pid of the hub's LISTEN backend, and when its last
	// statement started.
	ctx, admin := context.Background(), d.Admin(t)
	listener := func() (int, time.Time) {
		t.Helper()
		var pid int
		var started time.Time
		deadline := time.Now().Add(10 * time.Second)
		for {
			err := admin.QueryRow(ctx, `SELECT pid, query_start FROM pg_stat_activity
				WHERE datname = current_database() AND query = $1`, `LISTEN "`+realtime.Channel+`"`).Scan(&pid, &started)
			switch {
			case err == nil:
				return pid, started
			case !errors.Is(err, pgx.ErrNoRows) || time.Now().After(deadline):
				t.Fatalf("find the hub's LISTEN connection: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	pid, started := listener()
	time.Sleep(time.Second)
	if again, pinged := listener(); again != pid || !pinged.After(started) {
		t.Errorf("the LISTEN connection is backend %d, last pinged at %v, want backend %d pinged after %v", again,
			pinged, pid, started)
	}
	boards, commandIDs := createBoards(t, s.main, owner, project, 1)
	sock.wantOperations(project, seq, boards, commandIDs)
}

// TestRealtimeNotificationPayload checks that the notification of a batch
// holds the project and the seq alone, far below NOTIFY's limit of 8,000
// bytes, however large the batch, which the subscribers read from the log
// instead (FR-RT-02 AC2).
//
// Verifies: FR-RT-02/AC2
func TestRealtimeNotificationPayload(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	srv := realtimeServer(t, s)
	project := createProject(t, s.main, owner, ws, "Harbour").ID
	ctx := context.Background()
	listener, err := pgx.ConnectConfig(ctx, d.AppURL.PoolConfig().ConnConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close(ctx) })
	if _, err := listener.Exec(ctx, "LISTEN "+realtime.Channel); err != nil {
		t.Fatal(err)
	}
	sock := dialSocket(t, srv, owner.header(false))
	sock.subscribe(project, -1)
	seq := sock.next("subscribed").Seq

	// A batch of 500 boards, each with a description, which the log keeps
	// as hundreds of kilobytes.
	var cmds []wireCommand
	description := strings.TrimSpace(strings.Repeat("A long note about the harbour. ", 30))
	for range 500 {
		cmds = append(cmds, wireCommand{Type: "board.create", Payload: map[string]any{"id": newBoardID(),
			"name": "Board " + ids.New().String(), "description": description}})
	}
	if res := call(s.main, http.MethodPost, commandsPath(project), commandBatch(t, cmds...), owner.header(true)); res.status !=
		http.StatusOK {
		t.Fatalf("apply the batch: %d %s", res.status, res.body)
	}
	var size int
	if err := d.Admin(t).QueryRow(ctx, `SELECT octet_length(payload::text) FROM operation WHERE project_id = $1
		AND seq = $2`, decode(t, ids.Project, project), seq+1).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if size < 100_000 {
		t.Fatalf("the batch's payload is %d bytes, want a large batch", size)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	n, err := listener.WaitForNotification(waitCtx)
	if err != nil {
		t.Fatal(err)
	}
	if want := realtime.Payload(decode(t, ids.Project, project), seq+1); n.Payload != want || len(n.Payload) > 56 {
		t.Errorf("the notification is %q, %d bytes, want %q, the project and the seq alone", n.Payload,
			len(n.Payload), want)
	}
	if m := sock.next("operation"); m.Seq != seq+1 || len(m.Commands) != 500 {
		t.Errorf("the subscriber was sent seq %d with %d commands, want seq %d with 500", m.Seq, len(m.Commands),
			seq+1)
	}
}

// TestRealtimeAccessEnds checks that a socket is sent nothing once its
// credential no longer holds, or its user can no longer read the project:
// a removed member's subscription ends with unsubscribed not_found, at the
// project's next batch, and a socket whose session has signed out, whose
// API token is revoked, or whose session a change of its password gave a new
// secret, closes with 4001 then, before it is sent the batch, after which
// the cookie that the change set, which GET /api/v1/auth/session answers,
// opens a socket again.
func TestRealtimeAccessEnds(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	srv := realtimeServer(t, f.s)
	owner := f.as["owner"]
	project := createProject(t, f.s.main, owner, f.ws, "Harbour").ID
	editor := dialSocket(t, srv, f.as["editor"].header(false))
	viewer := dialSocket(t, srv, f.as["viewer"].header(false))
	commenter := dialSocket(t, srv, f.as["commenter"].header(false))
	token := makeToken(t, f.s.main, owner, workspaceTokensPath(f.ws), "project:read")
	byToken := dialSocket(t, srv, bearer(token.Secret))
	for _, sock := range []*testSocket{editor, viewer, commenter, byToken} {
		sock.subscribe(project, -1)
		sock.next("subscribed")
	}
	changed := signedInFrom(t, send(f.s.main, http.MethodPost, passwordPath,
		passwordChange(t, testPassword, "an entirely new passphrase"), "", f.as["commenter"].header(true)))

	if _, err := f.admin.Exec(context.Background(), "DELETE FROM membership WHERE id = $1",
		decode(t, ids.Membership, f.membership(t, "editor"))); err != nil {
		t.Fatal(err)
	}
	if res := send(f.s.main, http.MethodPost, logoutPath, nil, "", f.as["viewer"].header(true)); res.status !=
		http.StatusNoContent {
		t.Fatalf("sign the viewer out: %d %s", res.status, res.body)
	}
	if res := call(f.s.main, http.MethodDelete, workspaceTokensPath(f.ws)+"/"+token.ID, nil,
		owner.header(true)); res.status != http.StatusNoContent {
		t.Fatalf("revoke the token: %d %s", res.status, res.body)
	}
	createBoards(t, f.s.main, owner, project, 1)
	if m := editor.next("unsubscribed"); m.ProjectID != project || m.Reason != "not_found" {
		t.Errorf("the removed member was sent %+v, want unsubscribed not_found", m)
	}
	editor.quiet()
	for name, sock := range map[string]*testSocket{"signed out": viewer, "of a revoked token": byToken,
		"whose session has a new secret": commenter} {
		if code := sock.closedWith(); code != realtime.CloseUnauthenticated {
			t.Errorf("the socket %s closed with %v, want %v", name, code, realtime.CloseUnauthenticated)
		}
	}
	if res := send(f.s.main, http.MethodGet, sessionPath, nil, "", changed.header(false)); res.status != http.StatusOK {
		t.Fatalf("the session of the changed password: %d %s", res.status, res.body)
	}
	again := dialSocket(t, srv, changed.header(false))
	again.subscribe(project, -1)
	again.next("subscribed")
}

// TestRealtimeResync checks that a subscriber is told to reload the project,
// rather than sent the operations that it missed, when the log no longer has
// them all, as once compaction has dropped some, and when its since is
// beyond the project's seq, and that it is sent the later operations then.
func TestRealtimeResync(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	srv := realtimeServer(t, s)
	project := createProject(t, s.main, owner, ws, "Harbour").ID
	sock := dialSocket(t, srv, owner.header(false))
	sock.subscribe(project, -1)
	seq := sock.next("subscribed").Seq
	createBoards(t, s.main, owner, project, 4)
	for range 4 {
		sock.next("operation")
	}
	seq += 4
	// Compaction, which the application login may not do, drops what
	// follows seq-3.
	if _, err := d.Admin(t).Exec(context.Background(), `DELETE FROM operation WHERE project_id = $1 AND seq IN ($2, $3)`,
		decode(t, ids.Project, project), seq-2, seq-1); err != nil {
		t.Fatal(err)
	}
	for _, since := range []int64{seq - 3, seq + 10} {
		again := dialSocket(t, srv, owner.header(false))
		again.subscribe(project, since)
		if m := again.next("resync_required"); m.ProjectID != project || m.Seq != seq {
			t.Errorf("since %d: told %+v, want resync_required at %d", since, m, seq)
		}
		again.quiet()
		later, laterIDs := createBoards(t, s.main, owner, project, 1)
		again.wantOperations(project, seq, later, laterIDs)
		seq++
	}
}

// TestRealtimeRestore checks that a subscriber is told to reload the project
// once the project's seq goes back behind what the subscriber was sent, as
// once the database is restored from a backup older than it, whose batches
// after that seq, as they commit anew, are other operations than those that
// it was sent, which the hub's poll finds behind the latest seq that it had
// heard of, and that it is sent the batches that commit after.
func TestRealtimeRestore(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	s.hub = realtime.New(realtime.Options{DB: readyDB{s.ready}, Projects: s.projects,
		PollInterval: 200 * time.Millisecond})
	srv := realtimeServer(t, s)
	project := createProject(t, s.main, owner, ws, "Harbour").ID
	sock := dialSocket(t, srv, owner.header(false))
	sock.subscribe(project, -1)
	seq := sock.next("subscribed").Seq
	boards, commandIDs := createBoards(t, s.main, owner, project, 3)
	sock.wantOperations(project, seq, boards, commandIDs)

	// The database goes back to a backup taken after the first of the three
	// batches.
	restored := seq + 1
	ctx, admin, id := context.Background(), d.Admin(t), decode(t, ids.Project, project)
	if _, err := admin.Exec(ctx, `DELETE FROM operation WHERE project_id = $1 AND seq > $2`, id, restored); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE project SET seq = $2 WHERE id = $1`, id, restored); err != nil {
		t.Fatal(err)
	}
	if m := sock.next("resync_required"); m.ProjectID != project || m.Seq != restored {
		t.Errorf("told %+v, want resync_required at %d", m, restored)
	}
	sock.quiet()
	later, laterIDs := createBoards(t, s.main, owner, project, 1)
	sock.wantOperations(project, restored, later, laterIDs)
}

// TestRealtimeMessages checks how the socket answers what a client sends: a
// message that it cannot read, or that lacks a field that the schema
// requires, with an error, an unsubscription with
// unsubscribed, which ends the subscription, each naming the project as the
// API writes its ID, a subscription to an ID that is not a project's with
// invalid_id, and an unsubscription from one with unsubscribed, each naming
// it as the client did, and the 101st subscription with
// too_many_subscriptions.
func TestRealtimeMessages(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	srv := realtimeServer(t, f.s)
	owner := f.as["owner"]
	project := createProject(t, f.s.main, owner, f.ws, "Harbour").ID
	sock := dialSocket(t, srv, owner.header(false))
	for _, raw := range []string{`not json`, `[]`, `{"type": "ping"}`, `{"type": "subscribe", "project": "x"}`,
		`{"type": "subscribe", "project_id": "` + project + `", "since": -1}`, `{"Type": "subscribe"}`,
		`{"type": "subscribe"}`, `{"type": "subscribe", "project_id": null}`, `{"type": "unsubscribe"}`,
		`{"type": "subscribe", "project_id": "` + project + `", "since": 1.5}`} {
		sock.write(websocket.MessageText, []byte(raw))
		if m := sock.next("error"); m.Code != "invalid_message" {
			t.Errorf("%s is answered with %+v", raw, m)
		}
	}
	sock.write(websocket.MessageBinary, []byte(`{}`))
	sock.next("error")
	sock.subscribe("prj_nothing", 0)
	if m := sock.next("subscription_failed"); m.Code != "invalid_id" || m.ProjectID != "prj_nothing" {
		t.Errorf("an ID that is not a project's is refused with %+v", m)
	}
	sock.send(map[string]string{"type": "unsubscribe", "project_id": "prj_nothing"})
	if m := sock.next("unsubscribed"); m.ProjectID != "prj_nothing" || m.Reason != "requested" {
		t.Errorf("an unsubscription from an ID that is not a project's is answered with %+v", m)
	}

	// The answers name the project as the API writes its ID, whichever
	// spelling of it the client's message has.
	sock.subscribe(strings.ToLower(project), -1)
	if m := sock.next("subscribed"); m.ProjectID != project {
		t.Errorf("a subscription to %s is answered for %s, want %s", strings.ToLower(project), m.ProjectID, project)
	}
	sock.send(map[string]string{"type": "unsubscribe", "project_id": strings.ToLower(project)})
	if m := sock.next("unsubscribed"); m.ProjectID != project || m.Reason != "requested" {
		t.Errorf("the unsubscription is answered with %+v", m)
	}
	createBoards(t, f.s.main, owner, project, 1)
	sock.quiet()

	policy := ratelimit.DefaultPolicy()
	policy.Rates[ratelimit.Read].Limits[ratelimit.User] = 1000
	policy.Rates[ratelimit.Read].Limits[ratelimit.IP] = 1000
	f.s.limits = ratelimit.New(readyDB{f.s.ready}, policy)
	for range realtime.MaxSubscriptions {
		sock.subscribe(f.project(t, ""), 0)
		sock.next("subscribed")
	}
	sock.subscribe(project, 0)
	if m := sock.next("subscription_failed"); m.Code != "too_many_subscriptions" {
		t.Errorf("subscription %d is answered with %+v", realtime.MaxSubscriptions+1, m)
	}
}

// TestRealtimeSubscriptionRateLimit checks that each subscription counts in
// the rate limit of reads, which refuses one over it with rate_limited, and
// how long to wait, and that a subscription anew that it refuses leaves the
// one before as it was.
func TestRealtimeSubscriptionRateLimit(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	srv := realtimeServer(t, s)
	project := createProject(t, s.main, owner, ws, "Harbour").ID
	policy := ratelimit.DefaultPolicy()
	policy.Rates[ratelimit.Read].Limits[ratelimit.User] = 2
	s.limits = ratelimit.New(readyDB{s.ready}, policy)
	sock := dialSocket(t, srv, owner.header(false))
	for range 2 {
		sock.subscribe(project, -1)
		sock.next("subscribed")
	}
	sock.subscribe(project, -1)
	if m := sock.next("subscription_failed"); m.Code != "rate_limited" || m.RetryAfter < 1 || m.RetryAfter > 60 {
		t.Errorf("the subscription over the limit is answered with %+v", m)
	}
	// The refused subscription was to a project that the socket subscribes to
	// already, which it goes on subscribing to.
	boards, commandIDs := createBoards(t, s.main, owner, project, 1)
	sock.wantOperations(project, 0, boards, commandIDs)
}

// TestRealtimeUpgrade checks the upgrade's refusals: a request that does not
// upgrade is answered with 426 upgrade_required, in the envelope, and one
// without credentials with 401; a socket of the session cookie from a page
// of another origin is refused with 403 csrf_failed, as a page of the tool
// is not, nor a socket of an API token, whatever its Origin; and a user who
// has 50 sockets open is refused another with 429 rate_limited.
func TestRealtimeUpgrade(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	srv := realtimeServer(t, s)
	res := call(s.main, http.MethodGet, realtimePath, nil, owner.header(false))
	wantCode(t, "a GET that does not upgrade", res, http.StatusUpgradeRequired, api.CodeUpgradeRequired)
	if res.header.Get("Upgrade") != "websocket" || res.header.Get("Connection") != "Upgrade" ||
		res.header.Get("Sec-WebSocket-Version") != "13" {
		t.Errorf("the 426 names Upgrade %q, Connection %q, and version %q", res.header.Get("Upgrade"),
			res.header.Get("Connection"), res.header.Get("Sec-WebSocket-Version"))
	}
	upgrade := http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}, "Sec-Websocket-Version": {"13"},
		"Sec-Websocket-Key": {"dGhlIHNhbXBsZSBub25jZQ=="}}
	wantCode(t, "an upgrade without credentials", call(s.main, http.MethodGet, realtimePath, nil, upgrade),
		http.StatusUnauthorized, api.CodeUnauthenticated)
	for _, bad := range []http.Header{{"Sec-Websocket-Version": {"8"}}, {"Sec-Websocket-Key": {"c2hvcnQ="}},
		{"Upgrade": {"h2c"}}, {"Connection": {"keep-alive"}}} {
		h := owner.header(false)
		for k, v := range upgrade {
			h[k] = v
		}
		for k, v := range bad {
			h[k] = v
		}
		wantCode(t, "an upgrade with "+strings.Join(slices.Collect(func(yield func(string) bool) {
			for k, v := range bad {
				yield(k + ": " + v[0])
			}
		}), ""), call(s.main, http.MethodGet, realtimePath, nil, h), http.StatusUpgradeRequired, api.CodeUpgradeRequired)
	}

	// A page of the request's host, or of the public URL's, by another
	// scheme than the public URL's, http, is of another origin too.
	ctx := context.Background()
	for _, origin := range []string{"https://elsewhere.example", "https://" + strings.TrimPrefix(srv.URL, "http://"),
		"https://localhost:8080"} {
		foreign := owner.header(false)
		foreign.Set("Origin", origin)
		_, refused, err := websocket.Dial(ctx, socketURL(srv), &websocket.DialOptions{HTTPHeader: foreign})
		if err == nil || refused == nil || refused.StatusCode != http.StatusForbidden {
			t.Errorf("a socket of the session cookie from %s upgraded, or was answered %v: %v", origin, refused, err)
		}
	}
	for _, origin := range []string{srv.URL, "http://localhost:8080"} {
		h := owner.header(false)
		h.Set("Origin", origin)
		dialSocket(t, srv, h).ws.CloseNow()
	}
	token := makeToken(t, s.main, owner, workspaceTokensPath(ws), "project:read")
	h := bearer(token.Secret)
	h.Set("Origin", "https://elsewhere.example")
	dialSocket(t, srv, h)

	policy := ratelimit.DefaultPolicy()
	policy.Rates[ratelimit.Connect].Limits[ratelimit.User] = 1000
	policy.Rates[ratelimit.Connect].Limits[ratelimit.IP] = 1000
	s.limits = ratelimit.New(readyDB{s.ready}, policy)
	for range realtime.MaxConnectionsPerUser - 1 {
		dialSocket(t, srv, owner.header(false))
	}
	_, refused, err := websocket.Dial(ctx, socketURL(srv), &websocket.DialOptions{HTTPHeader: owner.header(false)})
	if err == nil || refused == nil || refused.StatusCode != http.StatusTooManyRequests ||
		refused.Header.Get(api.RetryAfterHeader) == "" {
		t.Errorf("the user's socket %d upgraded, or was answered %v: %v", realtime.MaxConnectionsPerUser+1, refused, err)
	}
}

// TestRealtimePing checks that a socket is closed with 1008 once it has
// heard nothing from its client for the silence's timeout, neither a message
// nor a pong, even while its project commits, whose operations the
// connection's buffers take at once, as those of a client that has gone
// without a word do, and that a client that stops reading for less than that,
// whose pong comes late, keeps its socket, and is sent every operation: two
// clients subscribe to a project that commits for a while, one of which
// reads again after a second, and the other only once the project is done.
func TestRealtimePing(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	s.hub = realtime.New(realtime.Options{DB: readyDB{s.ready}, Projects: s.projects,
		Heartbeat: 250 * time.Millisecond, SilenceTimeout: 2 * time.Second})
	srv := realtimeServer(t, s)
	project := createProject(t, s.main, owner, ws, "Harbour").ID
	gone, slow := openSocket(t, srv, owner.header(false)), openSocket(t, srv, owner.header(false))
	gone.subscribe(project, -1)
	slow.subscribe(project, -1)
	var boards, commandIDs []string
	reading := false
	for start := time.Now(); time.Since(start) < 5*time.Second; {
		if !reading && time.Since(start) >= time.Second {
			slow.start()
			reading = true
		}
		b, c := createBoards(t, s.main, owner, project, 1)
		boards, commandIDs = append(boards, b...), append(commandIDs, c...)
		time.Sleep(100 * time.Millisecond)
	}
	slow.wantOperations(project, slow.next("subscribed").Seq, boards, commandIDs)
	slow.quiet()

	gone.start()
	gone.next("subscribed")
	for sent := 0; ; sent++ {
		r := gone.receive()
		switch {
		case r.err == nil && r.msg.Type == "operation":
			continue
		case r.err == nil:
			t.Fatalf("the silent client's socket was sent %s, want operations", r.raw)
		case websocket.CloseStatus(r.err) != websocket.StatusPolicyViolation:
			t.Fatalf("the silent client's socket ended with %v, want a close with policy violation", r.err)
		case sent >= len(boards):
			t.Errorf("the silent client's socket was sent every one of the %d operations before it closed, want it "+
				"closed while its project still committed", len(boards))
		}
		return
	}
}

// slowProjects resolves an actor's access to the projects as Projects does,
// but takes delay longer for each of its reads of a project after the first,
// as each page of a delivery would from a database under load.
type slowProjects struct {
	realtime.Projects
	delay time.Duration
	reads atomic.Int32
}

func (p *slowProjects) GetIn(ctx context.Context, tx pgx.Tx, actor commands.Actor, workspaceID,
	projectID ids.UUID) (project.Visible, error) {
	if p.reads.Add(1) > 1 {
		time.Sleep(p.delay)
	}
	return p.Projects.GetIn(ctx, tx, actor, workspaceID, projectID)
}

// TestRealtimeHeartbeatWhileDelivering checks that a delivery that takes
// longer than the silence's timeout, as a catch-up on many operations to a
// slow client, pings the client between its operations, so that a client
// that reads them, and sends nothing but its pongs, keeps its socket once it
// has read them all, rather than be closed with 1008 for as long as the
// delivery took: each of the catch-up's three pages takes longer to read
// than the heartbeat's interval, but less than the silence's timeout, which
// the three together take longer than.
func TestRealtimeHeartbeatWhileDelivering(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	s.hub = realtime.New(realtime.Options{DB: readyDB{s.ready},
		Projects:  &slowProjects{Projects: s.projects, delay: 500 * time.Millisecond},
		Heartbeat: 50 * time.Millisecond, SilenceTimeout: 1200 * time.Millisecond})
	srv := realtimeServer(t, s)
	project := createProject(t, s.main, owner, ws, "Harbour").ID
	// Three pages of the log, of 100 operations at most each.
	createBoards(t, s.main, owner, project, 201)
	sock := dialSocket(t, srv, owner.header(false))
	sock.subscribe(project, 0)
	seq := sock.next("subscribed").Seq
	for want := int64(1); want <= seq; want++ {
		if m := sock.next("operation"); m.Seq != want {
			t.Fatalf("the catch-up's operation is of seq %d, want %d", m.Seq, want)
		}
	}
	select {
	case r := <-sock.received:
		t.Errorf("once the catch-up was read, the socket sent %s (%v), want it open, with nothing to send", r.raw,
			r.err)
	case <-time.After(2 * time.Second):
	}
}

// heldProjects resolves an actor's access to the projects as Projects does,
// but holds the first hold of its reads of a project, in the slot of reads
// that each runs in, until the socket that reads ends, telling held of each,
// as reads of a database under load would hold them.
type heldProjects struct {
	realtime.Projects
	hold atomic.Int32
	held chan struct{}
}

func (p *heldProjects) GetIn(ctx context.Context, tx pgx.Tx, actor commands.Actor, workspaceID,
	projectID ids.UUID) (project.Visible, error) {
	if p.hold.Add(-1) >= 0 {
		p.held <- struct{}{}
		<-ctx.Done()
		return project.Visible{}, ctx.Err()
	}
	return p.Projects.GetIn(ctx, tx, actor, workspaceID, projectID)
}

// TestRealtimeHeartbeatWhileWaiting checks that a socket that waits for one
// of the hub's slots of reads, behind the reads of other sockets, for longer
// than the silence's timeout, pings its client as it waits, so that a client
// that answers the pings keeps its socket once the socket has its slot,
// rather than be closed with 1008 for as long as it waited: two sockets hold
// both slots, with reads that take three times the silence's timeout.
func TestRealtimeHeartbeatWhileWaiting(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	held := &heldProjects{Projects: s.projects, held: make(chan struct{}, 2)}
	held.hold.Store(2)
	s.hub = realtime.New(realtime.Options{DB: readyDB{s.ready}, Projects: held, Heartbeat: 50 * time.Millisecond,
		SilenceTimeout: 500 * time.Millisecond})
	srv := realtimeServer(t, s)
	project := createProject(t, s.main, owner, ws, "Harbour").ID
	var holders []*testSocket
	for range 2 {
		holder := dialSocket(t, srv, owner.header(false))
		holder.subscribe(project, -1)
		holders = append(holders, holder)
	}
	for range 2 {
		select {
		case <-held.held:
		case <-time.After(10 * time.Second):
			t.Fatal("the reads that hold the slots did not start")
		}
	}
	sock := dialSocket(t, srv, owner.header(false))
	sock.subscribe(project, -1)
	time.Sleep(1500 * time.Millisecond)
	for _, holder := range holders {
		_ = holder.ws.CloseNow()
	}
	sock.next("subscribed")
	select {
	case r := <-sock.received:
		t.Errorf("once the socket had its slot, it sent %s (%v), want it open, with nothing to send", r.raw, r.err)
	case <-time.After(time.Second):
	}
}

// TestRealtimeAcrossServers checks that a batch that commits through one
// server is sent to the subscribers of another server of the same database,
// whose hub its notification reaches (FR-RT-02, ADR-005).
func TestRealtimeAcrossServers(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	other := serverOn(t, d, inviteEnv("open", nil))
	project := createProject(t, s.main, owner, ws, "Harbour").ID
	sock := dialSocket(t, realtimeServer(t, other), owner.header(false))
	sock.subscribe(project, -1)
	seq := sock.next("subscribed").Seq
	boards, commandIDs := createBoards(t, s.main, owner, project, 2)
	sock.wantOperations(project, seq, boards, commandIDs)
}

// TestRealtimeShutdown checks that the hub's shutdown closes its sockets with
// 1001, so that their clients connect again, and refuses new ones.
func TestRealtimeShutdown(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	srv := realtimeServer(t, s)
	project := createProject(t, s.main, owner, ws, "Harbour").ID
	sock := dialSocket(t, srv, owner.header(false))
	sock.subscribe(project, -1)
	sock.next("subscribed")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.hub.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if code := sock.closedWith(); code != websocket.StatusGoingAway {
		t.Errorf("the socket closed with %v, want going away", code)
	}
	_, res, err := websocket.Dial(ctx, socketURL(srv), &websocket.DialOptions{HTTPHeader: owner.header(false)})
	if err == nil || res == nil || res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("a socket opened after the shutdown upgraded, or was answered %v: %v", res, err)
	}
}
