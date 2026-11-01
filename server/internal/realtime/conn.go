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
// violation) for a client that answers no ping, 1009 (message too big) for a
// message over 4 KiB, and
// 1011 (internal error) when the server cannot read the database; a client
// connects again after each, with backoff, and subscribes with the seq that
// it has.
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
// away, whose close cannot be sent either.
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
	pinging  atomic.Bool
	// frames counts the client's messages that the reader has read, and
	// waiting is whether it waits for the loop to take one, while maxQueued
	// wait already, so that a ping whose pong the reader has not read, since
	// a pong is read after the messages before it, does not end a socket
	// whose client is there (heartbeat).
	frames  atomic.Uint64
	waiting atomic.Bool
	// sent counts the messages but the heartbeats that the socket has sent,
	// and beat is how many it had sent at the last heartbeat, which only the
	// loop reads and changes, so that a ping whose pong waits behind them,
	// in the connection's buffers, as a slow client reads them, does not end
	// a socket whose client is there either (heartbeat).
	sent atomic.Uint64
	beat uint64
	// answered is when the client was last found there, in Unix nanoseconds:
	// when the socket opened, and when the client answered a ping, or sent a
	// message while one waited for its pong, which only the pings, one at a
	// time, change, so that a client that is sent operations, whose pong may
	// wait behind them, is taken for gone all the same once it has not been
	// found there for long (heartbeat).
	answered atomic.Int64

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
		subs: map[ids.UUID]*subscription{}, wake: make(chan struct{}, 1), messages: make(chan queuedMessage, maxQueued)}
	c.answered.Store(time.Now().UnixNano())
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
	code, reason := c.run()
	for _, sub := range c.subs {
		h.leave(sub)
	}
	_ = ws.Close(code, reason)
	stopReading()
	<-read
}

// run is the socket's loop: it handles the client's messages, sends the
// subscriptions what they have heard of, and sends the heartbeats, one at a
// time, until the socket ends, and returns the code and reason that it
// closes with.
func (c *conn) run() (websocket.StatusCode, string) {
	t := time.NewTicker(c.hub.heartbeat)
	defer t.Stop()
	for {
		var err error
		select {
		case <-c.ctx.Done():
			return c.closing()
		case m := <-c.messages:
			err = c.handle(m)
		case <-c.wake:
			err = c.deliverHeard()
		case <-t.C:
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
		c.frames.Add(1)
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
		c.waiting.Store(false)
	}
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
	if _, beat := msg.(HeartbeatMessage); !beat {
		c.sent.Add(1)
	}
	ctx, cancel := context.WithTimeout(c.writes, c.hub.writeTimeout)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
		return gone
	}
	return nil
}

// heartbeat sends a heartbeat, which the client's script hears, and pings
// the client, whose pong a browser sends by itself, unless the last ping
// still waits for its pong. A ping that is not answered within PongTimeout
// ends the socket, unless the reader has read a message of the client since
// the ping, or waits for the loop to take one, or the socket has sent
// anything but a heartbeat since the heartbeat before, and the client was
// last found there (answered) within PongTimeout and WriteTimeout, together,
// before: the library reads a pong as the reader reads, after the messages
// that the client sent before it, so a client that sent more than the loop
// has handled yet, as one that subscribes to every project that it pins at
// once, is there, and the client reads the ping after the messages that the
// socket sent before it, which wait in the connection's buffers for as long
// as a slow client takes to read them, as the operations that it catches up
// with, so the pong of a client that is sent them may come as much later as
// a write of them may take. Past that, the client is taken for gone even
// while each write still finishes, as each does at once into the buffers of
// a client that has gone without a word, as a laptop that sleeps, until they
// fill, or the system gives up on the connection, which for a project that
// commits a little at a time can be a quarter of an hour. Either is pinged
// again at the next heartbeat.
func (c *conn) heartbeat() error {
	if err := c.send(HeartbeatMessage{Type: typeHeartbeat}); err != nil {
		return err
	}
	since := c.beat
	c.beat = c.sent.Load()
	if c.pinging.Swap(true) {
		return nil
	}
	go func() {
		defer c.pinging.Store(false)
		ctx, cancel := context.WithTimeout(c.writes, c.hub.pongTimeout)
		defer cancel()
		read := c.frames.Load()
		err := c.ws.Ping(ctx)
		now := time.Now()
		switch {
		case err == nil || c.frames.Load() != read:
			// The client answered, or sent a message, which is read before
			// its pong.
			c.answered.Store(now.UnixNano())
		case c.ctx.Err() != nil || c.waiting.Load():
			// The socket has ended, or the reader waits for the loop, before
			// it reads the pong.
		case c.sent.Load() == since ||
			now.Sub(time.Unix(0, c.answered.Load())) >= c.hub.pongTimeout+c.hub.writeTimeout:
			c.end(websocket.StatusPolicyViolation, "no pong")
		}
	}()
	return nil
}

// check checks the socket's credential (Client.Check), in one of the hub's
// slots of reads, and returns the closeError of CloseUnauthenticated if it no
// longer holds.
func (c *conn) check() (commands.Actor, error) {
	release, err := c.hub.acquire(c.ctx)
	if err != nil {
		return commands.Actor{}, err
	}
	actor, err := c.client.Check(c.ctx)
	release()
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
// anything else with an error message.
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
// subscriptions, nor its client's messages, nor its heartbeats waiting. A
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
