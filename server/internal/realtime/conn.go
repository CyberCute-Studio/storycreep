package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
)

// Client is who a socket's client is, whom the upgrade authenticated.
type Client struct {
	// UserID is the user, whose sockets Admit counted.
	UserID ids.UUID
	// Check checks the socket's credential again, its session or its API
	// token, as a request with it would be checked, and returns the actor
	// that the socket reads its projects as, or ErrUnauthenticated if the
	// credential no longer holds, as once the session has ended or the token
	// has been revoked. The socket checks it before each subscription and
	// each page of operations that it is sent, so that a credential that no
	// longer holds is sent nothing more, and closes the socket with
	// CloseUnauthenticated then.
	Check func(ctx context.Context) (commands.Actor, error)
	// Limit counts a subscription in the rate limit of reads of the socket's
	// client, its IP address, its user, and its API token, if it has one,
	// which are the socket's for its life, and returns a *LimitedError if it
	// is over it. The socket counts a subscription before it checks the
	// credential again, so that one over the limit takes none of the hub's
	// slots of reads, which every socket's deliveries share.
	Limit func(ctx context.Context) error
}

// ErrUnauthenticated is returned by Client.Check for a credential that no
// longer holds.
var ErrUnauthenticated = errors.New("realtime: the socket's session has ended, or its API token no longer holds")

// LimitedError is returned by Client.Limit for a subscription over the rate
// limit, which may be tried again after RetryAfter.
type LimitedError struct {
	RetryAfter time.Duration
}

func (e *LimitedError) Error() string {
	return "realtime: the subscription is over the rate limit; retry after " + e.RetryAfter.String()
}

// CloseUnauthenticated is the code that a socket is closed with once its
// credential no longer holds (Client.Check): its client signs in again, or
// takes another API token, before it connects again. The other codes are
// RFC 6455's: 1001 (going away) as the server shuts down, 1008 (policy
// violation) for a client that the socket has heard nothing from, neither a
// message nor a pong, for the hub's SilenceTimeout, 1009 (message too big)
// for a message over 4 KiB, and 1011 (internal error) when the server cannot
// read the database; a client connects again after each, with backoff, and
// subscribes with the seq that it has. A message to the client that cannot
// be written within the hub's WriteTimeout drops the connection without a
// close, which the client sees as 1006 (abnormal closure), since the close
// would wait behind the message that the client does not read, and the
// client connects again after that too.
const CloseUnauthenticated websocket.StatusCode = 4001

// closeError ends a socket with its code.
type closeError struct {
	code   websocket.StatusCode
	reason string
}

func (e *closeError) Error() string {
	return "realtime: the socket closes with " + e.code.String() + ": " + e.reason
}

// gone ends a socket that could not be written to, as one whose client went
// away, whose close cannot be sent either, since the write that ran out of
// time has dropped the connection.
var gone = &closeError{code: websocket.StatusGoingAway, reason: "the socket could not be written to"}

// conn is a socket that the hub serves.
type conn struct {
	hub    *Hub
	ws     *websocket.Conn
	client Client
	// ctx ends when the socket ends, as its client closes it, the hub shuts
	// down, or the socket is ended (end).
	ctx    context.Context
	cancel context.CancelFunc
	// writes is the context of the socket's writes, which the socket's end
	// (end) ends at once, but the hub's shutdown only shutdownWriteGrace
	// after it, because a write whose context ends closes the connection
	// there and then, without a close: a write in flight as the server stops,
	// which to a client that reads takes no time, finishes, and the socket
	// closes with 1001 after it.
	writes     context.Context
	stopWrites context.CancelFunc
	// subs are the socket's subscriptions, by project, which only the loop
	// (run) reads and changes.
	subs map[ids.UUID]*subscription
	// wake tells the loop that a subscription has heard of a seq.
	wake chan struct{}
	// messages are the client's messages that the loop has not handled yet,
	// at most maxQueued, past which the reader waits for the loop.
	messages chan queuedMessage
	// beat ticks when the loop, or a delivery that it runs, sends its next
	// heartbeat (heartbeat), which only the loop reads.
	beat *time.Ticker
	// pinging is whether a ping waits for its pong.
	pinging atomic.Bool
	// heard is when the socket last heard from its client, as the time since
	// born, when it opened, in nanoseconds: when it opened, read a message,
	// or was answered a ping. It counts from born, rather than in Unix time,
	// so that the silence (silence) is measured by the monotonic clock, which
	// a step of the wall clock, as NTP's, or a virtual machine's as it
	// resumes, does not move: a step forward would close every socket at
	// once, and one back keep those of clients that have gone. waiting is
	// whether the reader waits for the loop to take a message, while
	// maxQueued wait already, when it reads nothing, pongs neither, which the
	// library reads as the reader reads (heartbeat).
	born    time.Time
	heard   atomic.Int64
	waiting atomic.Bool

	mu sync.Mutex
	// ended, code, and reason are how the socket ends, once something ends
	// it.
	ended  bool
	code   websocket.StatusCode
	reason string
}

// queuedMessage is a message of the client, and whether it is text.
type queuedMessage struct {
	data []byte
	text bool
}

// subscription is a socket's subscription to a project.
type subscription struct {
	conn               *conn
	project, workspace ids.UUID
	// sent is the seq of the last operation that the subscription was sent,
	// or that it goes on from, which only the socket's loop reads and
	// changes.
	sent int64
	// heard is the latest seq of the project that the hub heard of.
	heard atomic.Int64
}

// Serve serves the socket ws, of the client, until it closes, or the hub
// shuts down, and closes it. ctx gives the socket's reads and logs the
// request's values, but not its cancellation: an upgraded request's ends
// only with the socket.
func (h *Hub) Serve(ctx context.Context, ws *websocket.Conn, client Client) {
	if !h.begin() {
		_ = ws.Close(websocket.StatusGoingAway, "the server is shutting down")
		return
	}
	defer h.running.Done()
	h.connections.Inc()
	defer h.connections.Dec()
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	defer context.AfterFunc(h.ctx, cancel)()
	writes, stopWrites := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWrites()
	defer context.AfterFunc(h.ctx, func() { time.AfterFunc(shutdownWriteGrace, stopWrites) })()
	c := &conn{hub: h, ws: ws, client: client, ctx: ctx, cancel: cancel, writes: writes, stopWrites: stopWrites,
		subs: map[ids.UUID]*subscription{}, wake: make(chan struct{}, 1), messages: make(chan queuedMessage, maxQueued),
		born: time.Now()}
	c.hear()
	ws.SetReadLimit(maxMessageBytes)
	// The reader has a context of its own, which the socket's end does not
	// end, because a read whose context ends closes the connection at once,
	// and the socket is closed with its code instead, by the close handshake,
	// whose client's half the reader reads.
	readCtx, stopReading := context.WithCancel(context.WithoutCancel(ctx))
	read := make(chan struct{})
	go func() {
		defer close(read)
		c.read(readCtx)
	}()
	// The socket's end is deferred, so that a panic of its loop, which the
	// server recovers from, still takes its subscriptions from the hub,
	// closes its connection, with 1011, and stops its reader.
	code, reason := websocket.StatusInternalError, "internal error"
	defer func() {
		for _, sub := range c.subs {
			h.leave(sub)
		}
		_ = ws.Close(code, reason)
		stopReading()
		<-read
	}()
	code, reason = c.run()
}

// run is the socket's loop: it handles the client's messages, sends the
// subscriptions what they have heard of, and sends the heartbeats, one at a
// time, until the socket ends, and returns the code and reason that it
// closes with. A delivery sends a heartbeat that falls due as it runs too,
// between its operations (beatIfDue).
func (c *conn) run() (websocket.StatusCode, string) {
	c.beat = time.NewTicker(c.hub.heartbeat)
	defer c.beat.Stop()
	for {
		var err error
		select {
		case <-c.ctx.Done():
			return c.closing()
		case m := <-c.messages:
			err = c.handle(m)
		case <-c.wake:
			err = c.deliverHeard()
		case <-c.beat.C:
			err = c.heartbeat()
		}
		if err != nil {
			var ce *closeError
			if !errors.As(err, &ce) {
				if c.ctx.Err() != nil {
					return c.closing()
				}
				c.hub.log.ErrorContext(c.ctx, "a real-time socket could not read the database, so it closes, and "+
					"its client connects again", "error", err)
				ce = &closeError{code: websocket.StatusInternalError, reason: "internal error"}
			}
			c.end(ce.code, ce.reason)
			return c.closing()
		}
	}
}

// end ends the socket with code and reason, unless something ended it
// before, and its write in flight, if it has one, as one to a client that
// answers no ping.
func (c *conn) end(code websocket.StatusCode, reason string) {
	c.mu.Lock()
	if !c.ended {
		c.ended, c.code, c.reason = true, code, reason
	}
	c.mu.Unlock()
	c.cancel()
	c.stopWrites()
}

// closing returns the code and reason that the socket closes with: going
// away if the hub shuts down, and else what ended it.
func (c *conn) closing() (websocket.StatusCode, string) {
	if c.hub.ctx.Err() != nil {
		return websocket.StatusGoingAway, "the server is shutting down"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ended {
		return c.code, c.reason
	}
	return websocket.StatusNormalClosure, ""
}

// read reads the client's messages, and queues them for the loop, until the
// socket closes, or ctx ends. While maxQueued wait, it waits for the loop
// before it reads more, so that a client that sends faster than the loop
// handles its messages, as one that subscribes to every project that it pins
// at once, is slowed down, and holds no more of them in memory.
func (c *conn) read(ctx context.Context) {
	for {
		typ, data, err := c.ws.Read(ctx)
		if err != nil {
			// The client closed the socket, it failed, or a message was too
			// big, which the library closes it for.
			c.cancel()
			return
		}
		c.hear()
		m := queuedMessage{data: data, text: typ == websocket.MessageText}
		select {
		case c.messages <- m:
			continue
		default:
		}
		c.waiting.Store(true)
		select {
		case c.messages <- m:
		case <-ctx.Done():
			return
		}
		// The socket hears the client before the reader stops waiting, so
		// that a heartbeat between the two finds neither the silence from
		// before the wait, nor the wait.
		c.hear()
		c.waiting.Store(false)
	}
}

// hear records that the socket heard from its client now.
func (c *conn) hear() {
	c.heard.Store(int64(time.Since(c.born)))
}

// silence returns how long the socket has heard nothing from its client.
func (c *conn) silence() time.Duration {
	return time.Since(c.born) - time.Duration(c.heard.Load())
}

// signal wakes the loop to send the subscriptions what they heard of.
func (c *conn) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// send sends msg, or returns gone if the socket cannot be written to within
// the hub's WriteTimeout, as when its client has gone, or reads too slowly,
// and returns the socket's context's error, sending nothing, once the socket
// has ended.
func (c *conn) send(msg any) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(c.writes, c.hub.writeTimeout)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
		return gone
	}
	return nil
}

// heartbeat closes the socket with 1008 if it has heard nothing from its
// client for SilenceTimeout, neither a message nor a pong, unless the reader
// waits for the loop, which then hears nothing of the client through its own
// fault, and else sends a heartbeat, which the client's script hears, and
// pings the client, whose pong a browser sends by itself, unless the last
// ping still waits for its pong. The ping waits for its pong as long as the
// silence may last, since its pong comes after the messages that the socket
// sent before it, which a slow client may take a while to read, each within
// WriteTimeout. coder/websocket gives a ping's frame 5 seconds of its own: a
// ping that waits longer than that for a message still being written fails,
// without harm to the socket, and the next heartbeat pings again; the frame,
// a few bytes, once its turn comes, goes into the room that the message
// before it left in the connection's buffers, at once.
func (c *conn) heartbeat() error {
	if !c.waiting.Load() && c.silence() >= c.hub.silenceTimeout {
		return &closeError{code: websocket.StatusPolicyViolation, reason: "the client has answered nothing"}
	}
	if err := c.send(HeartbeatMessage{Type: typeHeartbeat}); err != nil {
		return err
	}
	if c.pinging.Swap(true) {
		return nil
	}
	go func() {
		defer c.pinging.Store(false)
		ctx, cancel := context.WithTimeout(c.writes, c.hub.silenceTimeout)
		defer cancel()
		if c.ws.Ping(ctx) == nil {
			c.hear()
		}
	}()
	return nil
}

// beatIfDue sends the heartbeat (heartbeat) if one is due, as a delivery
// does before each operation that it sends, so that one that takes longer
// than the heartbeat's interval, as to a slow client that catches up on many
// operations, still pings its client, whose pongs it hears as the client
// reads, rather than find, once it is done, that it has heard nothing for as
// long as it took, and close a client that read it all.
func (c *conn) beatIfDue() error {
	select {
	case <-c.beat.C:
		return c.heartbeat()
	default:
		return nil
	}
}

// check checks the socket's credential (Client.Check), in one of the hub's
// slots of reads, and returns the closeError of CloseUnauthenticated if it no
// longer holds.
func (c *conn) check() (commands.Actor, error) {
	release, err := c.hub.acquire(c.ctx)
	if err != nil {
		return commands.Actor{}, err
	}
	// The slot is released however Check returns, so that a panic of it,
	// which the server recovers from, does not keep one of the hub's slots,
	// which every socket's deliveries share, for good.
	defer release()
	actor, err := c.client.Check(c.ctx)
	if errors.Is(err, ErrUnauthenticated) {
		return commands.Actor{}, &closeError{code: CloseUnauthenticated,
			reason: "the session has ended, or the API token no longer holds"}
	}
	return actor, err
}

// invalidMessage answers a message that the socket cannot read.
var invalidMessage = ErrorMessage{Type: typeError, Code: "invalid_message"}

// handle handles a message of the client: a subscription, or the end of
// one, each a JSON object of exactly its fields, with those that it
// requires, as the schema describes them (validMessage), and answers
// anything else with an error message. The message is decoded with
// commands.DecodeObject, which holds its keys to its fields' exact spelling
// of its own, where huma's validation does only while
// huma.ValidateStrictCasing, a switch of the whole process, which packages
// api and commands set, is set, and encoding/json takes a key of another
// case for a field.
func (c *conn) handle(m queuedMessage) error {
	var head struct {
		Type string `json:"type"`
	}
	if !m.text || json.Unmarshal(m.data, &head) != nil {
		return c.send(invalidMessage)
	}
	switch head.Type {
	case typeSubscribe:
		var s SubscribeMessage
		if !validMessage(subscribeSchema, m.data) || commands.DecodeObject(m.data, &s) != nil {
			return c.send(invalidMessage)
		}
		return c.subscribe(s)
	case typeUnsubscribe:
		var u UnsubscribeMessage
		if !validMessage(unsubscribeSchema, m.data) || commands.DecodeObject(m.data, &u) != nil {
			return c.send(invalidMessage)
		}
		return c.unsubscribe(u)
	}
	return c.send(invalidMessage)
}

// failed answers a subscription to the project named projectID with code.
func (c *conn) failed(projectID, code string) error {
	return c.send(SubscriptionFailedMessage{Type: typeSubscriptionFailed, ProjectID: projectID, Code: code})
}

// subscribe subscribes the socket to a project, as m asks, which it answers
// with subscribed, and the operations after m's since, or with
// resync_required when it cannot send them, or refuses: a project that does
// not exist, or that the actor cannot read, alike with not_found (PRD
// FR-RT-01 AC2). A subscription to a project that the socket subscribes to
// already starts anew once it holds, and leaves the one before as it was if
// it is refused, but for not_found, which ends that one too. The answers
// name the project as the API writes its ID (ids.Encode), as the operations
// do, whichever spelling of it m has (ids.Decode), so that a client that
// compares them as strings finds them alike, but for invalid_id, which
// names it as m does. The subscription counts in the rate limit before the
// credential is checked again, so that a client that subscribes over the
// limit, however often, takes none of the hub's slots of reads from the
// deliveries of the other sockets (Client.Limit).
func (c *conn) subscribe(m SubscribeMessage) error {
	id, err := ids.Decode(ids.Project, m.ProjectID)
	if err != nil {
		return c.failed(m.ProjectID, "invalid_id")
	}
	projectID := ids.Encode(ids.Project, id)
	old := c.subs[id]
	if old == nil && len(c.subs) >= MaxSubscriptions {
		return c.failed(projectID, "too_many_subscriptions")
	}
	notFound := func() error {
		if old != nil {
			c.drop(old)
		}
		return c.failed(projectID, "not_found")
	}
	if err := c.client.Limit(c.ctx); err != nil {
		var limited *LimitedError
		if !errors.As(err, &limited) {
			return err
		}
		return c.send(SubscriptionFailedMessage{Type: typeSubscriptionFailed, ProjectID: projectID,
			Code: "rate_limited", RetryAfter: int(max(1, (limited.RetryAfter+time.Second-1)/time.Second))})
	}
	actor, err := c.check()
	if err != nil {
		return err
	}
	workspaceID, err := c.hub.projects.ActorWorkspace(c.ctx, actor, id)
	if errors.Is(err, project.ErrNotFound) {
		return notFound()
	}
	if err != nil {
		return err
	}
	// The subscription is sent what follows since if the log still holds
	// it, and there is not too much of it. A client ahead of the project, as
	// of a database restored from before its seq, reloads the project too.
	var seq int64
	follows := m.Since != nil
	err = c.hub.read(c.ctx, actor, workspaceID, id, func(tx pgx.Tx, s int64) error {
		seq = s
		switch since := m.Since; {
		case since == nil || *since == s:
		case *since > s || s-*since > MaxCatchUp:
			follows = false
		default:
			var err error
			follows, err = commands.LogFollows(c.ctx, tx, workspaceID, id, *since)
			return err
		}
		return nil
	})
	if errors.Is(err, project.ErrNotFound) {
		return notFound()
	}
	if err != nil {
		return err
	}
	if old != nil {
		c.drop(old)
	}
	sub := &subscription{conn: c, project: id, workspace: workspaceID, sent: seq}
	var answer any = SubscribedMessage{Type: typeSubscribed, ProjectID: projectID, Seq: seq}
	switch {
	case follows:
		sub.sent = *m.Since
	case m.Since != nil:
		answer = ResyncRequiredMessage{Type: typeResyncRequired, ProjectID: projectID, Seq: seq}
	}
	c.subs[id] = sub
	c.hub.join(sub, seq)
	if err := c.send(answer); err != nil {
		return err
	}
	// What commits between the read and the join is read now, as what it
	// missed is, with the credential just checked.
	return c.deliver(sub, &actor)
}

// unsubscribe ends the socket's subscription to the project that m names,
// if it has one, and answers unsubscribed whether it had one or not, naming
// the project as subscribe's answers do, or, for an ID that is not a
// project's, which no subscription has, as m does.
func (c *conn) unsubscribe(m UnsubscribeMessage) error {
	id, err := ids.Decode(ids.Project, m.ProjectID)
	if err != nil {
		return c.send(UnsubscribedMessage{Type: typeUnsubscribed, ProjectID: m.ProjectID, Reason: "requested"})
	}
	if sub := c.subs[id]; sub != nil {
		c.drop(sub)
	}
	return c.send(UnsubscribedMessage{Type: typeUnsubscribed, ProjectID: ids.Encode(ids.Project, id),
		Reason: "requested"})
}

// drop ends the subscription sub.
func (c *conn) drop(sub *subscription) {
	delete(c.subs, sub.project)
	c.hub.leave(sub)
}

// deliverHeard sends each subscription the operations after the last that
// it was sent, if it has heard of a later seq.
func (c *conn) deliverHeard() error {
	for _, sub := range c.subs {
		if sub.heard.Load() <= sub.sent {
			continue
		}
		if err := c.deliver(sub, nil); err != nil {
			return err
		}
	}
	return nil
}

// deliver sends sub the operations of its project after the last that it
// was sent, read as the socket's actor, a page at a time (commands.ReadLog),
// with the credential checked before each page, but for the first when
// checked is the actor of a check that the caller has just made, until it
// has sent what the project had when it read the first page: what commits
// later wakes the socket again (Hub.dispatch), so that a project that commits
// faster than the client reads keeps neither the socket's other
// subscriptions, nor its client's messages, nor its heartbeats waiting, and
// sends a heartbeat that falls due meanwhile before the operation that it
// would wait behind (beatIfDue), so that a long one, as of a catch-up to a
// slow client, keeps the client pinged, and heard, as it reads. A
// project that the actor can no longer read ends the subscription, with
// unsubscribed, and one whose log no longer has an operation that sub was not
// sent, as once compaction (PRD FR-GDPR-03) dropped it, or of which more than
// MaxCatchUp wait to be sent, as once the hub heard of none for long, has sub
// told to reload the project (resync_required), as a subscription is.
func (c *conn) deliver(sub *subscription, checked *commands.Actor) error {
	projectID := ids.Encode(ids.Project, sub.project)
	var until int64
	for first := true; ; first = false {
		var actor commands.Actor
		if checked != nil {
			actor, checked = *checked, nil
		} else {
			var err error
			if actor, err = c.check(); err != nil {
				return err
			}
		}
		var ops []commands.Operation
		var seq int64
		tooMany := false
		err := c.hub.read(c.ctx, actor, sub.workspace, sub.project, func(tx pgx.Tx, s int64) error {
			seq = s
			if first && s-sub.sent > MaxCatchUp {
				tooMany = true
				return nil
			}
			var err error
			ops, err = commands.ReadLog(c.ctx, tx, sub.workspace, sub.project, sub.sent, pageRows, pageBytes)
			return err
		})
		switch {
		case errors.Is(err, project.ErrNotFound):
			c.drop(sub)
			return c.send(UnsubscribedMessage{Type: typeUnsubscribed, ProjectID: projectID, Reason: "not_found"})
		case err != nil:
			return err
		case tooMany:
			return c.resync(sub, projectID, seq)
		case first:
			until = seq
		}
		for _, op := range ops {
			if op.Seq != sub.sent+1 {
				return c.resync(sub, projectID, seq)
			}
			if err := c.beatIfDue(); err != nil {
				return err
			}
			if err := c.send(operationMessage(projectID, op)); err != nil {
				return err
			}
			sub.sent = op.Seq
		}
		switch {
		case len(ops) == 0 && seq > sub.sent:
			return c.resync(sub, projectID, seq)
		case len(ops) == 0 || sub.sent >= until:
			return nil
		}
	}
}

// resync tells sub to reload its project, whose operations after seq follow.
func (c *conn) resync(sub *subscription, projectID string, seq int64) error {
	sub.sent = seq
	return c.send(ResyncRequiredMessage{Type: typeResyncRequired, ProjectID: projectID, Seq: seq})
}

// operationMessage returns the message of the operation op of the project
// projectID, as the API writes its IDs.
func operationMessage(projectID string, op commands.Operation) OperationMessage {
	msg := OperationMessage{Type: typeOperation, ProjectID: projectID, Seq: op.Seq,
		Commands: make([]OperationCommand, len(op.Commands)), Revisions: make([]OperationRevision, len(op.Revisions)),
		CreatedAt: op.CreatedAt.UTC()}
	for i, cmd := range op.Commands {
		msg.Commands[i] = OperationCommand{CommandID: cmd.ID.String(), Type: string(cmd.Type), Payload: cmd.Payload}
	}
	for i, r := range op.Revisions {
		msg.Revisions[i] = OperationRevision{ID: ids.Encode(r.Kind, r.ID), Rev: r.Rev}
	}
	return msg
}
