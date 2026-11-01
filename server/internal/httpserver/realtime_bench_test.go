package httpserver

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/fixture"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/realtime"
)

// idleConnectionsTarget is NFR-PERF-17's target: the server's memory for
// 1,000 idle WebSocket connections, which FR-RT-01 AC4 holds a process to.
const (
	idleConnections       = 1000
	idleConnectionsTarget = 250 << 20
)

// BenchmarkIdleConnections opens 1,000 sockets of the real-time channel, each
// subscribed to the large fixture's project, and left idle, and reports how
// much the process's memory in use grew, its heap and its goroutines' stacks
// after a garbage collection, against NFR-PERF-17's target. The sockets are
// opened by hand, rather than with a client library, which would hold a
// goroutine and buffers of its own for each, so that what the process grows
// by is the server's, but for a connection's few hundred bytes on the
// client's side, which the measurement counts against the server. The hub's
// heartbeats, which the sockets would not answer, are put off past the
// benchmark, and its bound of a user's sockets lifted, as the rate limit of
// connects is: a thousand sockets of one user cost the server what those of
// many do. The loop times nothing, so ns/op is not reported.
//
// Verifies: NFR-PERF-17, FR-RT-01/AC4
func BenchmarkIdleConnections(b *testing.B) {
	bs := newBenchServer(b, fixture.Large)
	policy := ratelimit.DefaultPolicy()
	for _, bucket := range []ratelimit.Bucket{ratelimit.Read, ratelimit.Connect} {
		policy.Rates[bucket].Limits[ratelimit.User] = 1_000_000
		policy.Rates[bucket].Limits[ratelimit.IP] = 1_000_000
	}
	bs.s.limits = ratelimit.New(readyDB{bs.s.ready}, policy)
	bs.s.hub = realtime.New(realtime.Options{DB: readyDB{bs.s.ready}, Projects: bs.s.projects,
		Heartbeat: time.Hour, PongTimeout: time.Hour, MaxConnectionsPerUser: idleConnections})
	srv := httptestServer(b, bs.s)
	addr, cookie := strings.TrimPrefix(srv, "http://"), bs.header.Get("Cookie")

	// A socket first, so that what the process loads once, as the
	// listener's connection, is not counted.
	if c := idleSocket(b, addr, cookie, bs.project); c != nil {
		_ = c.Close()
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	conns := make([]net.Conn, idleConnections)
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for i := w; i < idleConnections; i += 8 {
				conns[i] = idleSocket(b, addr, cookie, bs.project)
			}
		})
	}
	wg.Wait()
	b.Cleanup(func() {
		for _, c := range conns {
			if c != nil {
				_ = c.Close()
			}
		}
	})
	if b.Failed() {
		b.FailNow()
	}
	// The sockets have answered their subscriptions; the server's last
	// writes settle before the process is measured.
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	runtime.ReadMemStats(&after)
	used := int64(after.HeapInuse+after.StackInuse) - int64(before.HeapInuse+before.StackInuse)
	b.Logf("1,000 idle sockets grew the heap in use by %d KiB and the stacks by %d KiB, and the memory obtained "+
		"from the OS by %d KiB", (int64(after.HeapInuse)-int64(before.HeapInuse))>>10,
		(int64(after.StackInuse)-int64(before.StackInuse))>>10, (int64(after.Sys)-int64(before.Sys))>>10)
	for b.Loop() {
	}
	b.ReportMetric(0, "ns/op")
	b.ReportMetric(float64(used)/(1<<20), "MB-per-1000-sockets")
	if used >= idleConnectionsTarget {
		b.Errorf("NFR-PERF-17: 1,000 idle sockets take %d MiB, want under %d MiB", used>>20, idleConnectionsTarget>>20)
	}
}

// httptestServer serves s's main handler on a port of its own, until the
// benchmark ends, and returns its URL.
func httptestServer(b *testing.B, s *server) string {
	b.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	srv := &http.Server{Handler: s.main, ReadHeaderTimeout: readHeaderTimeout}
	go func() { _ = srv.Serve(ln) }()
	b.Cleanup(func() {
		_ = s.hub.Shutdown(context.Background())
		_ = srv.Close()
	})
	return "http://" + ln.Addr().String()
}

// idleSocket opens a socket of the real-time channel at addr, with the
// session cookie, by hand (RFC 6455), subscribes it to the project, reads the
// answer, which must be subscribed, and returns the connection, which then
// reads nothing more.
func idleSocket(b *testing.B, addr, cookie, project string) net.Conn {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		b.Error(err)
		return nil
	}
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	if _, err := io.WriteString(conn, "GET "+realtimePath+" HTTP/1.1\r\nHost: "+addr+"\r\nUpgrade: websocket\r\n"+
		"Connection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n"+
		"Cookie: "+cookie+"\r\n\r\n"); err != nil {
		b.Error(err)
		return conn
	}
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, nil)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		b.Errorf("the upgrade: %v %v", res, err)
		return conn
	}
	// A client's frames are masked (RFC 6455 §5.3), here with the key 1 2 3
	// 4, and a message of fewer than 126 bytes has its length in the
	// header's second byte.
	payload := `{"type":"subscribe","project_id":"` + project + `"}`
	frame := []byte{0x81, 0x80 | byte(len(payload)), 1, 2, 3, 4}
	for i := range len(payload) {
		frame = append(frame, payload[i]^frame[2+i%4])
	}
	if _, err := conn.Write(frame); err != nil {
		b.Error(err)
		return conn
	}
	// The server's frames are not masked, and its answer is shorter than
	// 126 bytes too.
	header := make([]byte, 2)
	if _, err := io.ReadFull(br, header); err != nil {
		b.Error(err)
		return conn
	}
	answer := make([]byte, header[1]&0x7f)
	if _, err := io.ReadFull(br, answer); err != nil || !strings.Contains(string(answer), `"subscribed"`) {
		b.Errorf("the subscription was answered with %q: %v", answer, err)
	}
	return conn
}
