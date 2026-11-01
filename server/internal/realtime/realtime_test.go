package realtime

import (
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// testHub returns a hub whose LISTEN connection and poller count as started,
// so that a subscription that joins starts neither, and a socket of it that
// a test's subscriptions can wake.
func testHub() (*Hub, *conn) {
	h := New(Options{})
	h.started = true
	return h, &conn{hub: h, wake: make(chan struct{}, 1)}
}

// woken reports whether c was woken, and takes the wake.
func woken(c *conn) bool {
	select {
	case <-c.wake:
		return true
	default:
		return false
	}
}

// TestChannelWorkspace checks that a channel's poll reads its project in the
// workspace that a subscription's read found the project in, rather than in
// that of the subscription that created the channel, which an API token's
// subscription to a project of another workspace than the token's names, and
// which keeps the channel once it is refused, if another subscription has
// joined meanwhile.
func TestChannelWorkspace(t *testing.T) {
	h, c := testHub()
	project, tokens, projects := ids.New(), ids.New(), ids.New()
	refused := &subscription{conn: c, project: project, workspace: tokens}
	holds := &subscription{conn: c, project: project, workspace: projects}
	h.join(refused)
	h.join(holds)
	h.leave(refused)
	h.saw(holds, 3)
	if ch := h.channels[project]; ch == nil || ch.workspace != projects || ch.latest != 3 {
		t.Errorf("the channel is %+v, want one of the project's workspace at seq 3", ch)
	}
}

// TestStaleSeqWakesNone checks that a seq that the channel has heard of
// already, as one that the poll reads once a notification has told of it,
// wakes none of its subscriptions, and that a later one wakes each, but for
// the subscription whose read found it, which sends it itself.
func TestStaleSeqWakesNone(t *testing.T) {
	h, c := testHub()
	other := &conn{hub: h, wake: make(chan struct{}, 1)}
	project := ids.New()
	sub := &subscription{conn: c, project: project}
	reader := &subscription{conn: other, project: project}
	h.join(sub)
	h.dispatch(project, 5)
	if w := woken(c); !w || sub.heard.Load() != 5 {
		t.Fatalf("a seq that the channel had not heard of woke the subscription %v, which heard of %d, want "+
			"woken, and 5", w, sub.heard.Load())
	}
	h.dispatch(project, 5)
	h.dispatch(project, 4)
	if woken(c) || sub.heard.Load() != 5 {
		t.Errorf("seqs that the channel had heard of woke the subscription, which heard of %d, want none and 5",
			sub.heard.Load())
	}
	h.join(reader)
	h.saw(reader, 7)
	if !woken(c) || woken(other) || sub.heard.Load() != 7 || reader.heard.Load() != 7 {
		t.Errorf("the reader's seq 7 left the subscriptions at %d and %d, or woke the reader, want both at 7, and "+
			"the other woken", sub.heard.Load(), reader.heard.Load())
	}
}
