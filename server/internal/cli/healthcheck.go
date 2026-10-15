package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// healthcheckTimeout is how long healthcheck waits for the server's answer.
// The server waits at most 2 seconds for the database before it answers.
const healthcheckTimeout = 5 * time.Second

var healthcheck = &command{
	name:    "healthcheck",
	summary: "Check that the server is ready",
	help: `
Checks that the server is ready to serve: requests /readyz from the address
in STORYCREEP_HTTP_ADDR, and exits with status 0 if the server answers 200,
or with status 1 if it answers anything else, or nothing within 5 seconds,
and says why. A server that listens on every network interface (an address
written :port, 0.0.0.0:port, or [::]:port) is reached at 127.0.0.1, which
it serves in each case.

It is the health check of the container image, which has no shell or HTTP
client to run another one with. It reads the configuration as serve does, so
a server whose configuration file is named with --config is checked with the
same --config, or with STORYCREEP_CONFIG_FILE, which both read.`,
	loadsConfig: true,
	run: func(ctx context.Context, inv *invocation) error {
		ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
		defer cancel()
		url := "http://" + reachableAddr(inv.cfg.HTTPAddr) + "/readyz"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		// No proxy: the server is on this host, or on the network that the
		// address names, and a proxy that the environment sets for the
		// server's own outbound requests would not reach it.
		client := &http.Client{Transport: &http.Transport{Proxy: nil}}
		defer client.CloseIdleConnections()
		res, err := client.Do(req)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("the server at %s did not answer within %s", url, healthcheckTimeout)
			}
			return fmt.Errorf("the server at %s does not answer: %w", url, err)
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusOK {
			fmt.Fprintln(inv.stdout, "The server is ready.")
			return nil
		}
		// A server that is not ready says why in the error envelope.
		var env struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&env) == nil && env.Error.Message != "" {
			return fmt.Errorf("%s answered %s: %s", url, res.Status, env.Error.Message)
		}
		return fmt.Errorf("%s answered %s", url, res.Status)
	},
}

// reachableAddr returns the address at which a client on this host reaches a
// server listening on addr, which the configuration has checked: the IPv4
// loopback address for one that listens on every network interface, and addr
// itself otherwise. On [::], the server's net.Listen("tcp", …) opens a
// dual-stack socket, which takes IPv4 connections too, so 127.0.0.1 reaches
// it even where ::1 does not exist, as in a container whose loopback
// interface has IPv6 disabled.
func reachableAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if ip, err := netip.ParseAddr(host); host == "" || err == nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
