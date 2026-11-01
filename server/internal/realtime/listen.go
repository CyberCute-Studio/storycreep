package realtime

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Payload returns the payload of the notification of the operation of the
// seq seq of the project projectID: the project's UUID and the seq, as
// 0190e4c2-…:42, at most 56 bytes, far below the 8,000 that NOTIFY takes,
// whatever the operation holds (PRD FR-RT-02 AC2).
func Payload(projectID ids.UUID, seq int64) string {
	return projectID.String() + ":" + strconv.FormatInt(seq, 10)
}

// parsePayload returns the project and the seq of a notification's payload,
// and false if it is not one that Payload writes.
func parsePayload(p string) (ids.UUID, int64, bool) {
	project, seq, ok := strings.Cut(p, ":")
	if !ok {
		return ids.UUID{}, 0, false
	}
	id, err := ids.ParseUUID(project)
	if err != nil {
		return ids.UUID{}, 0, false
	}
	n, err := strconv.ParseInt(seq, 10, 64)
	if err != nil || n <= 0 {
		return ids.UUID{}, 0, false
	}
	return id, n, true
}

// Notifies sends a notification, as *database.DB does.
type Notifies interface {
	Notify(ctx context.Context, channel, payload string) error
}

// notifyTimeout is how long the Notifier waits for the database to take a
// notification, which the request of the batch that it tells of waits for.
const notifyTimeout = 5 * time.Second

// listenPingTimeout is how long the hub waits for the database to answer
// the ping of its LISTEN connection (listenOnce), past which it opens the
// connection anew.
const listenPingTimeout = 10 * time.Second

// Notifier is the command pipeline's Notifier (commands.Notifier): it sends
// the notification of each batch that commits on Channel, which the hub of
// every server process of the instance, this one's too, is told of, once
// the batch has committed (PRD §10.2 step 7). A notification that cannot be
// sent is logged, and lost: the operation is in the log, which a subscriber
// is sent it from at the next notification of its project, or once its hub
// reads the project's seq (Options.PollInterval).
type Notifier struct {
	db  Notifies
	log *slog.Logger
}

var _ commands.Notifier = (*Notifier)(nil)

// NewNotifier returns a Notifier that sends its notifications with db, and
// logs to log those that it cannot send, or discards them if log is nil.
func NewNotifier(db Notifies, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Notifier{db: db, log: log}
}

func (n *Notifier) Committed(ctx context.Context, projectID ids.UUID, seq int64) {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	if err := n.db.Notify(ctx, Channel, Payload(projectID, seq)); err != nil {
		n.log.WarnContext(ctx, "the notification of a committed batch could not be sent, so the other sessions of "+
			"its project are sent the batch once their servers read the project's seq", "error", err)
	}
}

// listen keeps the hub's connection to PostgreSQL LISTENing on Channel, until
// the hub shuts down, and dispatches each notification, connecting again,
// after a delay, whenever the connection fails. Once it LISTENs, it has the
// poller read every project's seq, since the notifications sent before were
// not heard.
func (h *Hub) listen() {
	defer h.running.Done()
	delay := time.Second
	for {
		listened, err := h.listenOnce()
		if h.ctx.Err() != nil {
			return
		}
		if listened {
			delay = time.Second
		}
		h.log.WarnContext(h.ctx, "the real-time hub's LISTEN connection failed, so it connects again; the "+
			"sessions are sent what commits meanwhile once it does", "retry_in", delay.String(), "error", err)
		select {
		case <-time.After(delay):
		case <-h.ctx.Done():
			return
		}
		delay = min(2*delay, maxListenDelay)
	}
}

// listenOnce LISTENs on a connection of its own, and dispatches the
// notifications that it hears, until the connection fails or the hub shuts
// down, and reports whether it got as far as LISTENing. Once it has heard
// nothing for PollInterval, it LISTENs again, which the connection does
// already, and so changes nothing, as a ping that the database must answer
// within listenPingTimeout, so that a connection whose database went away
// without a word, as one that failed over, which only waits otherwise,
// until the system gives up on the connection minutes later, while the hub
// hears of each batch only as it polls, is found gone, and opened anew. The
// notifications that come meanwhile wait in the connection for the next
// wait.
func (h *Hub) listenOnce() (bool, error) {
	conn, err := h.db.Dedicated(h.ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(h.ctx)) }()
	listen := "LISTEN " + pgx.Identifier{Channel}.Sanitize()
	if _, err := conn.Exec(h.ctx, listen); err != nil {
		return false, err
	}
	h.pollSoon()
	for {
		wait, cancel := context.WithTimeout(h.ctx, h.pollInterval)
		n, err := conn.WaitForNotification(wait)
		cancel()
		switch {
		case err == nil:
			if project, seq, ok := parsePayload(n.Payload); ok {
				h.dispatch(project, seq)
			}
		case errors.Is(err, context.DeadlineExceeded) && h.ctx.Err() == nil:
			ping, cancel := context.WithTimeout(h.ctx, listenPingTimeout)
			_, err := conn.Exec(ping, listen)
			cancel()
			if err != nil {
				return true, err
			}
		default:
			return true, err
		}
	}
}

// pollSoon has the poller read the projects' seqs at once.
func (h *Hub) pollSoon() {
	select {
	case h.pollNow <- struct{}{}:
	default:
	}
}

// poll reads the seq of each project that the hub's sockets subscribe to
// every PollInterval, and when the LISTEN connection asks it to, until the
// hub shuts down, and dispatches the seqs that the hub had not heard of.
func (h *Hub) poll() {
	defer h.running.Done()
	t := time.NewTicker(h.pollInterval)
	defer t.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-t.C:
		case <-h.pollNow:
		}
		h.pollOnce()
	}
}

// pollOnce reads the seqs of the projects that the hub's sockets subscribe
// to, in one statement for each workspace, one workspace at a time, and
// dispatches each that the hub had not heard of. A project that its
// workspace no longer has, as once it is purged, is dispatched all the same,
// so that its subscriptions find it gone, and one whose seq is behind the
// latest that the hub had heard of is rewound (Hub.rewind), so that its
// subscriptions reload it. The workspaces whose seqs it cannot
// read, as while the database is down, it logs once for the poll, with the
// first error.
func (h *Hub) pollOnce() {
	h.mu.Lock()
	latest := make(map[ids.UUID]int64, len(h.channels))
	byWorkspace := map[ids.UUID][]ids.UUID{}
	for id, ch := range h.channels {
		latest[id] = ch.latest
		byWorkspace[ch.workspace] = append(byWorkspace[ch.workspace], id)
	}
	h.mu.Unlock()
	failed, first := 0, error(nil)
	for workspaceID, projects := range byWorkspace {
		var seqs map[ids.UUID]int64
		err := h.db.InSnapshot(h.ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
			var err error
			seqs, err = commands.ProjectSeqs(h.ctx, tx, workspaceID, projects)
			return err
		})
		switch {
		case h.ctx.Err() != nil:
			return
		case err != nil:
			if failed++; first == nil {
				first = err
			}
			continue
		}
		for _, id := range projects {
			switch seq, ok := seqs[id]; {
			case !ok:
				h.dispatch(id, latest[id]+1)
			case seq > latest[id]:
				h.dispatch(id, seq)
			case seq < latest[id]:
				// The hub heard of each seq of latest once it had committed,
				// before this read, so a seq behind it is of a database
				// restored from a backup older than it.
				h.rewind(id, seq)
			}
		}
	}
	if failed > 0 {
		h.log.WarnContext(h.ctx, "the real-time hub could not read the seqs of the projects of some workspaces, so "+
			"it reads them again at its next poll", "workspaces", failed, "error", first)
	}
}
