// Package realtime is the real-time channel (PRD FR-RT-01, FR-RT-02,
// ADR-005, plan note N59): the WebSocket hub of each server process, which
// tells the sessions that subscribe to a project of each batch of commands
// that commits to it, wherever it committed, so that other tabs, other
// devices, and other users see it at once.
//
// The operation log is the source of truth, and the socket only a way to be
// told of it sooner (AC3). Each process keeps one connection to PostgreSQL
// that LISTENs on Channel, and the pipeline's Notifier sends a notification
// of each batch once it has committed, with nothing but the project's ID and
// the batch's seq (Payload), however large the batch (FR-RT-02 AC2). A hub
// told of a seq reads the operations that each of the project's subscribers
// has not been sent from the log, as that subscriber, and sends them in the
// order of their seqs, which commit in order without a gap (plan note N30).
// A notification can be lost, as while the LISTEN connection reconnects, so
// once it has LISTENed again, and every 30 seconds, the hub reads the seq of
// each project that its sessions subscribe to, and sends what it missed.
//
// A session subscribes to a project with the seq of the latest operation
// that it has, and is sent the operations after it, or, when the log no
// longer has them all, or more than MaxCatchUp follow it, told to reload the
// project instead (resync_required). It may subscribe to a project that it
// can read, and a project that it cannot, or that does not exist, is refused
// alike, so that the refusal does not tell that the project exists (FR-RT-01
// AC2). The socket's credential, its session or its API token, and the
// session's access to each project are checked again before each page of
// operations that it is sent, so that a session that is revoked, or a member
// who is removed, is sent nothing more.
package realtime

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
)

// Channel is the channel of PostgreSQL's LISTEN and NOTIFY that the
// Notifier sends each committed batch's notification on, and every hub
// LISTENs on.
const Channel = "storycreep_operations"

// The bounds of a hub.
const (
	// MaxSubscriptions is how many projects one socket may subscribe to: the
	// project open in the tab, and those pinned for offline use, which are
	// kept current while online (PRD FR-OFF-02).
	MaxSubscriptions = 100
	// MaxConnectionsPerUser is how many sockets one user may have open to one
	// process at a time, so that a user cannot hold the process's memory with
	// sockets, which the rate limit of connects lets them open 30 a minute.
	MaxConnectionsPerUser = 50
	// MaxCatchUp is how many operations a subscription is sent of those that
	// it missed before it is told to reload the project instead: reading the
	// project anew is then cheaper than replaying them.
	MaxCatchUp = 10_000
	// pageRows and pageBytes bound a page of the log that a subscription is
	// sent at once (commands.ReadLog): it is read whole, in one transaction,
	// before it is sent, so the bounds hold how much a socket's delivery
	// holds in memory, whatever its batches hold.
	pageRows  = 100
	pageBytes = 1 << 20
	// maxMessageBytes is the most that a client's message may hold. A
	// subscription's message is a few dozen bytes.
	maxMessageBytes = 4 << 10
	// maxQueued is how many of a client's messages may wait for the socket
	// to handle them, past which the socket reads no more until it has: a
	// client sends a message a project, once.
	maxQueued = 32
	// readSlots is how many of a hub's checks of a credential and reads of
	// the log run at once, so that a batch of a busy project, which each of
	// its many subscribers checks and reads as its own, never takes more of
	// the pool's connections than these from the requests: pgx's pool has 4
	// at least.
	readSlots = 2
)

// Defaults of a hub's timing (Options).
const (
	defaultHeartbeat    = 25 * time.Second
	defaultPongTimeout  = 20 * time.Second
	defaultPollInterval = 30 * time.Second
	writeTimeout        = 30 * time.Second
	// maxListenDelay is the longest that the hub waits before it tries
	// again to LISTEN, once a try has failed: it waits a second after the
	// first failure, and twice as long after each failure after it.
	maxListenDelay = 30 * time.Second
)

// DB is what the hub needs of the database: transactions of the application
// login, and a connection of its own to LISTEN on, as *database.DB has them.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
	Dedicated(ctx context.Context) (*pgx.Conn, error)
}

// Projects resolves an actor's access to a project, as *project.Service does.
type Projects interface {
	ActorWorkspace(ctx context.Context, actor commands.Actor, projectID ids.UUID) (ids.UUID, error)
	GetIn(ctx context.Context, tx pgx.Tx, actor commands.Actor, workspaceID, projectID ids.UUID) (project.Visible,
		error)
}

// Options configure a Hub.
type Options struct {
	DB       DB
	Projects Projects
	// Log receives what goes wrong with the LISTEN connection, and with a
	// socket's reads. Nil discards it.
	Log *slog.Logger
	// Heartbeat is how often a socket is sent a heartbeat and pinged, 25
	// seconds if 0, and PongTimeout how long the ping waits for its pong
	// before the socket is closed, 20 seconds if 0. A test shortens them.
	Heartbeat, PongTimeout time.Duration
	// PollInterval is how often the hub reads the seqs of the projects that
	// its sockets subscribe to, in case a notification was lost, 30 seconds
	// if 0.
	PollInterval time.Duration
	// MaxConnectionsPerUser is how many sockets a user may have open,
	// MaxConnectionsPerUser if 0. A benchmark of a thousand sockets raises
	// it.
	MaxConnectionsPerUser int
}

// Hub is the real-time hub of a server process. Its zero value is not
// usable; New makes one.
type Hub struct {
	db                     DB
	projects               Projects
	log                    *slog.Logger
	heartbeat, pongTimeout time.Duration
	pollInterval           time.Duration
	maxConnectionsPerUser  int

	// ctx ends when the hub shuts down, which closes every socket.
	ctx    context.Context
	cancel context.CancelFunc
	// reads holds a slot for each delivery that reads the database.
	reads chan struct{}
	// pollNow asks the poller to poll at once, as once the hub has LISTENed
	// again.
	pollNow chan struct{}

	mu     sync.Mutex
	closed bool
	// started is whether the LISTEN connection and the poller have started,
	// which the first subscription does.
	started bool
	// channels are the projects that sockets subscribe to, by ID, and users
	// counts each user's open sockets.
	channels map[ids.UUID]*channel
	users    map[ids.UUID]int
	// running counts the sockets being served, the listener, and the poller,
	// which Shutdown waits for.
	running sync.WaitGroup

	connections, subscriptions prometheus.Gauge
}

// channel is a project that sockets of the hub subscribe to.
type channel struct {
	workspace ids.UUID
	// latest is the latest seq that the hub was told of, or read, of the
	// project.
	latest int64
	subs   map[*subscription]struct{}
}

// New returns a hub, which listens once its first socket subscribes.
func New(opts Options) *Hub {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &Hub{db: opts.DB, projects: opts.Projects, log: log, heartbeat: opts.Heartbeat,
		pongTimeout: opts.PongTimeout, pollInterval: opts.PollInterval,
		maxConnectionsPerUser: opts.MaxConnectionsPerUser, ctx: ctx, cancel: cancel,
		reads: make(chan struct{}, readSlots), pollNow: make(chan struct{}, 1), channels: map[ids.UUID]*channel{},
		users: map[ids.UUID]int{},
		connections: prometheus.NewGauge(prometheus.GaugeOpts{Name: "storycreep_websocket_connections",
			Help: "The WebSocket connections that the process has open."}),
		subscriptions: prometheus.NewGauge(prometheus.GaugeOpts{Name: "storycreep_websocket_subscriptions",
			Help: "The subscriptions to projects of the process's WebSocket connections."}),
	}
	if h.heartbeat <= 0 {
		h.heartbeat = defaultHeartbeat
	}
	if h.pongTimeout <= 0 {
		h.pongTimeout = defaultPongTimeout
	}
	if h.pollInterval <= 0 {
		h.pollInterval = defaultPollInterval
	}
	if h.maxConnectionsPerUser <= 0 {
		h.maxConnectionsPerUser = MaxConnectionsPerUser
	}
	return h
}

// Collector returns the hub's metrics: its open sockets, and their
// subscriptions.
func (h *Hub) Collector() prometheus.Collector {
	return collectors{h.connections, h.subscriptions}
}

// collectors is a prometheus.Collector of several.
type collectors []prometheus.Collector

func (cs collectors) Describe(ch chan<- *prometheus.Desc) {
	for _, c := range cs {
		c.Describe(ch)
	}
}

func (cs collectors) Collect(ch chan<- prometheus.Metric) {
	for _, c := range cs {
		c.Collect(ch)
	}
}

// ErrTooManyConnections is returned by Admit for a user who has
// MaxConnectionsPerUser sockets open to the process already, or the hub's
// Options.MaxConnectionsPerUser.
var ErrTooManyConnections = errors.New("realtime: the user has too many sockets open")

// ErrClosed is returned by Admit once the hub has shut down.
var ErrClosed = errors.New("realtime: the hub has shut down")

// Admit counts a socket of the user userID that is about to open, before the
// connection is upgraded, and returns the function that stops counting it,
// which Serve's caller calls once the socket has closed, or the upgrade has
// failed. It returns ErrTooManyConnections if the user has
// MaxConnectionsPerUser open already, and ErrClosed once the hub has shut
// down.
func (h *Hub) Admit(userID ids.UUID) (release func(), err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.closed:
		return nil, ErrClosed
	case h.users[userID] >= h.maxConnectionsPerUser:
		return nil, ErrTooManyConnections
	}
	h.users[userID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.users[userID]--; h.users[userID] <= 0 {
				delete(h.users, userID)
			}
		})
	}, nil
}

// Shutdown closes every socket, with 1001 (going away), so that its client
// connects again to another server, or to this one once it is back, and
// stops listening, and waits until the sockets have closed, or ctx ends.
// http.Server.Shutdown waits for none of them, because their connections
// are no longer the server's once they upgrade.
func (h *Hub) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	h.cancel()
	done := make(chan struct{})
	go func() {
		h.running.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// begin counts a goroutine that Shutdown waits for, and reports false once
// the hub has shut down, when it counts nothing.
func (h *Hub) begin() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.running.Add(1)
	return true
}

// join adds sub to its project's channel, which it creates, of the
// workspace that sub read the project in, if the project has none, where
// sub read the seq seq. The first subscription starts the LISTEN connection
// and the poller. The subscription has heard of the latest seq that the
// channel had heard of, so that it reads what it missed.
func (h *Hub) join(sub *subscription, seq int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := h.channels[sub.project]
	if ch == nil {
		ch = &channel{workspace: sub.workspace, subs: map[*subscription]struct{}{}}
		h.channels[sub.project] = ch
	}
	ch.subs[sub] = struct{}{}
	ch.latest = max(ch.latest, seq)
	sub.heard.Store(ch.latest)
	h.subscriptions.Inc()
	if !h.started && !h.closed {
		h.started = true
		h.running.Add(2)
		go h.listen()
		go h.poll()
	}
}

// leave removes sub from its project's channel, and the channel once it has
// no subscription.
func (h *Hub) leave(sub *subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := h.channels[sub.project]
	if ch == nil {
		return
	}
	if _, ok := ch.subs[sub]; !ok {
		return
	}
	delete(ch.subs, sub)
	h.subscriptions.Dec()
	if len(ch.subs) == 0 {
		delete(h.channels, sub.project)
	}
}

// dispatch tells the subscriptions of the project projectID that its
// operation of the seq seq has committed, so that each reads what it has not
// been sent, up to it at least.
func (h *Hub) dispatch(projectID ids.UUID, seq int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := h.channels[projectID]
	if ch == nil {
		return
	}
	ch.latest = max(ch.latest, seq)
	for sub := range ch.subs {
		if seq > sub.heard.Load() {
			sub.heard.Store(seq)
		}
		sub.conn.signal()
	}
}

// acquire waits for one of the hub's slots of reads, and returns the
// function that releases it, or ctx's error if ctx ends first.
func (h *Hub) acquire(ctx context.Context) (release func(), err error) {
	select {
	case h.reads <- struct{}{}:
		return func() { <-h.reads }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// read runs fn in a transaction of the workspace workspaceID, as the actor,
// once it has checked there that the actor may read the project projectID
// (project.Service.GetIn), with the project's seq, while it holds one of the
// hub's slots of reads. It returns project.ErrNotFound as GetIn does.
func (h *Hub) read(ctx context.Context, actor commands.Actor, workspaceID, projectID ids.UUID,
	fn func(tx pgx.Tx, seq int64) error) error {
	release, err := h.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return h.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}, func(tx pgx.Tx) error {
		v, err := h.projects.GetIn(ctx, tx, actor, workspaceID, projectID)
		if err != nil {
			return err
		}
		return fn(tx, v.Project.Seq)
	})
}
