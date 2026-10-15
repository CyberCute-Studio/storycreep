package cli

import (
	"net"
	"net/http"
	"strings"
	"testing"
)

// serveReadyz serves /readyz on a loopback port with handler, and returns the
// port.
func serveReadyz(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /readyz", handler)
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

// TestHealthcheck checks that healthcheck exits with status 0 only when the
// server's /readyz answers 200, and says why otherwise.
func TestHealthcheck(t *testing.T) {
	ready := serveReadyz(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ready"}`))
	})
	starting := serveReadyz(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":{"code":"not_ready","message":"The server is starting.",` +
			`"details":{"reason":"starting"},"request_id":"x"}}`))
	})
	broken := serveReadyz(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<html>bad gateway</html>"))
	})
	closed := freeAddr(t)

	for _, tt := range []struct {
		name, addr string
		code       int
		want       string
	}{
		{"ready", "127.0.0.1:" + ready, ExitOK, "The server is ready."},
		{"every interface", ":" + ready, ExitOK, "The server is ready."},
		{"every IPv4 interface", "0.0.0.0:" + ready, ExitOK, "The server is ready."},
		// This server listens on 127.0.0.1 alone; a server on [::] takes
		// IPv4 connections too.
		{"every IPv6 interface", "[::]:" + ready, ExitOK, "The server is ready."},
		{"host name", "localhost:" + ready, ExitOK, "The server is ready."},
		{"not ready", "127.0.0.1:" + starting, ExitFailure,
			"storycreep healthcheck: http://127.0.0.1:" + starting + "/readyz answered 503 Service Unavailable: " +
				"The server is starting."},
		{"not the envelope", "127.0.0.1:" + broken, ExitFailure,
			"storycreep healthcheck: http://127.0.0.1:" + broken + "/readyz answered 502 Bad Gateway\n"},
		{"nothing listens", closed, ExitFailure,
			"storycreep healthcheck: the server at http://" + closed + "/readyz does not answer: "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
			env["STORYCREEP_HTTP_ADDR"] = tt.addr
			code, stdout, stderr := run(t, env, "healthcheck")
			if code != tt.code {
				t.Errorf("exit %d, want %d; stdout %q, stderr %q", code, tt.code, stdout, stderr)
			}
			out := stdout
			if tt.code != ExitOK {
				out = stderr
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("the output %q does not contain %q", out, tt.want)
			}
		})
	}
}

func TestReachableAddr(t *testing.T) {
	for addr, want := range map[string]string{
		":8080":               "127.0.0.1:8080",
		"0.0.0.0:8080":        "127.0.0.1:8080",
		"[::]:8080":           "127.0.0.1:8080",
		"127.0.0.1:8080":      "127.0.0.1:8080",
		"10.1.2.3:8080":       "10.1.2.3:8080",
		"[fd00::1]:8080":      "[fd00::1]:8080",
		"storycreep.lan:8080": "storycreep.lan:8080",
	} {
		if got := reachableAddr(addr); got != want {
			t.Errorf("reachableAddr(%q) = %q, want %q", addr, got, want)
		}
	}
}
