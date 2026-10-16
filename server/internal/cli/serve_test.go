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

// TestServe runs serve, with and without --no-auto-migrate and
// --no-inline-workers, until it is ready, and stops it as a signal would.
func TestServe(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		admin bool
		args  []string
		// ready is the status of /readyz once the log has the message
		// started, which the test waits for before it stops the server.
		ready   int
		started string
		// logged is another message that the log has by the time the server
		// stops, if any.
		logged string
	}{
		// The workers start once the server is ready, so the test waits for
		// them, and the log says first that the server is ready.
		{"auto-migrate", true, nil, http.StatusOK, "the job workers started", "ready"},
		{"no-auto-migrate", false, []string{"--no-auto-migrate"}, http.StatusServiceUnavailable,
			"the database has pending migrations; run storycreep migrate to apply them", ""},
		{"no-inline-workers", true, []string{"--no-inline-workers"}, http.StatusOK, "ready",
			"the job workers are off in this process, so the instances that run them do its jobs"},
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
			wants := []string{`"msg":"listening"`, `"msg":"stopped"`}
			if tt.logged != "" {
				wants = append(wants, `"msg":"`+tt.logged+`"`)
			}
			for _, want := range wants {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("the log lacks %s:\n%s", want, stderr.String())
				}
			}
		})
	}
}

// TestServeRefusesPlainHTTPInHostedMode checks that serve does not start in
// hosted mode with an http public URL, which would send session cookies
// without Secure, names the key and the rule, and listens on nothing.
//
// Verifies: FR-AUTH-02/AC7
func TestServeRefusesPlainHTTPInHostedMode(t *testing.T) {
	addr := freeAddr(t)
	env := map[string]string{"STORYCREEP_MODE": "hosted", "STORYCREEP_PUBLIC_URL": "http://storycreep.example.com",
		"STORYCREEP_DATABASE_URL": testDatabaseURL, "STORYCREEP_HTTP_ADDR": addr, "STORYCREEP_ADMIN_ADDR": "off",
		"STORYCREEP_METRICS_TOKEN": "test-metrics-token-0123456789"}
	code, stdout, stderr := run(t, env, "serve", "--no-auto-migrate")
	if code != ExitFailure || stdout != "" || !strings.Contains(stderr, "\n  STORYCREEP_PUBLIC_URL (") ||
		!strings.Contains(stderr, "hosted mode requires an https URL") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		conn.Close()
		t.Errorf("something listens on %s", addr)
	}
	env["STORYCREEP_PUBLIC_URL"] = "https://storycreep.example.com"
	if code, _, stderr := run(t, env, "validate-config"); code != ExitOK {
		t.Errorf("with an https URL: exit %d, stderr %q", code, stderr)
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
