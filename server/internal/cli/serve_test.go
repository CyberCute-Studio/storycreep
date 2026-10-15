package cli

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

// freeAddr returns an address on the loopback interface that nothing listens
// on, as far as the test can tell.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// syncBuffer is a buffer that the server writes its log to while the test
// reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestServe runs serve, with and without --no-auto-migrate, until it is
// ready, and stops it as a signal would.
func TestServe(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		admin bool
		args  []string
		// ready is the status of /readyz once the log has the message
		// started.
		ready   int
		started string
	}{
		{"auto-migrate", true, nil, http.StatusOK, "ready"},
		{"no-auto-migrate", false, []string{"--no-auto-migrate"}, http.StatusServiceUnavailable,
			"the database has pending migrations; run storycreep migrate to apply them"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := dbtest.Empty(t)
			env := migrateEnv(d, tt.admin)
			addr := freeAddr(t)
			env["STORYCREEP_HTTP_ADDR"], env["STORYCREEP_ADMIN_ADDR"] = addr, freeAddr(t)
			env["STORYCREEP_LOG_LEVEL"] = "debug"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var stdout, stderr syncBuffer
			done := make(chan int, 1)
			go func() {
				done <- Run(ctx, append([]string{"serve"}, tt.args...), &stdout, &stderr, config.MapEnvironment(env))
			}()

			for deadline := time.Now().Add(time.Minute); ; time.Sleep(20 * time.Millisecond) {
				res, err := http.Get("http://" + addr + "/readyz")
				if err == nil {
					res.Body.Close()
					if res.StatusCode == tt.ready && strings.Contains(stderr.String(), `"msg":"`+tt.started+`"`) {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("/readyz does not answer %d; the log:\n%s", tt.ready, stderr.String())
				}
			}
			cancel()
			select {
			case code := <-done:
				if code != ExitOK || stdout.String() != "" {
					t.Errorf("exit %d, stdout %q; want %d and nothing", code, stdout.String(), ExitOK)
				}
			case <-time.After(time.Minute):
				t.Fatal("serve did not stop")
			}
			for _, want := range []string{`"msg":"listening"`, `"msg":"stopped"`} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("the log lacks %s:\n%s", want, stderr.String())
				}
			}
		})
	}
}

// TestServeNeedsTheAdminURL checks that serve, which applies the migrations
// at startup unless told not to, refuses to start without the admin URL, and
// says how to start instead.
//
// Verifies: FR-ADMIN-06/AC1
func TestServeNeedsTheAdminURL(t *testing.T) {
	code, stdout, stderr := run(t, validEnv(), "serve")
	if code != ExitFailure || stdout != "" || !strings.Contains(stderr, "storycreep serve: STORYCREEP_DATABASE_ADMIN_URL is not set") ||
		!strings.Contains(stderr, "--no-auto-migrate") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}
