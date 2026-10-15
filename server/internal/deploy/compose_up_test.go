package deploy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// documentedPublish is where docker-compose.yml publishes the server by
// default, which deploy/README.md documents.
const documentedPublish = "127.0.0.1:7777"

// TestComposeUp starts the published compose file as a user does on a clean
// machine, in a directory that holds nothing but the file, with the
// database password as its only setting, and checks that the instance comes
// up healthy and serves on the documented port (PRD FR-ADMIN-07 AC1), and
// that it reports the commit that the image was built from. Then it
// recreates the server's container on the same volumes, as an upgrade does,
// and checks that the instance serves again.
//
// It runs the image that STORYCREEP_TEST_IMAGE names in place of the
// published one, and publishes the server on another port if something on
// this machine already listens on the documented one.
//
// Verifies: FR-ADMIN-07/AC1
func TestComposeUp(t *testing.T) {
	image := os.Getenv("STORYCREEP_TEST_IMAGE")
	if image == "" {
		t.Skip("STORYCREEP_TEST_IMAGE names no image to run docker-compose.yml with; CI's image job sets it")
	}
	dir := t.TempDir()
	b, err := os.ReadFile(filepath.Join(deployDir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	// The password has characters that a URL would need escaped.
	password := "pw@/:#%?-" + rand.Text()
	env := append(cleanEnvironment(), "POSTGRES_PASSWORD="+password, "STORYCREEP_IMAGE="+image)
	publish := documentedPublish
	if ln, err := net.Listen("tcp", publish); err == nil {
		ln.Close()
	} else {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		publish = ln.Addr().String()
		ln.Close()
		t.Logf("%s is in use, so the server is published on %s", documentedPublish, publish)
		env = append(env, "STORYCREEP_PUBLISH="+publish)
	}

	project := "storycreep-test-" + strings.ToLower(rand.Text()[:8])
	// run runs docker compose until ctx ends, and returns its standard
	// output, which the test parses, without the warnings it writes to
	// standard error.
	run := func(ctx context.Context, args ...string) (string, error) {
		args = append([]string{"compose", "--project-name", project, "--project-directory", dir}, args...)
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Env = env
		// docker runs compose as a child process, which may hold the output
		// open after docker is killed.
		cmd.WaitDelay = 10 * time.Second
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if err != nil {
			err = fmt.Errorf("docker %s: %w\n%s%s", strings.Join(args, " "), errors.Join(err, ctx.Err()),
				stdout.Bytes(), stderr.Bytes())
		}
		return stdout.String(), err
	}
	// The test's commands end 2 minutes before go test's timeout, whose panic
	// would skip the cleanup and leave the project's containers and volumes
	// behind, so that the cleanup has those 2 minutes to remove them.
	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-2*time.Minute))
		defer cancel()
	}
	compose := func(args ...string) (string, error) {
		return run(ctx, args...)
	}
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := run(context.Background(), "logs", "--no-color")
			t.Logf("the services' logs:\n%s", logs)
		}
		if _, err := run(context.Background(), "down", "--volumes", "--remove-orphans"); err != nil {
			t.Error(err)
		}
	})

	if _, err := compose("up", "--detach", "--wait", "--wait-timeout", "300"); err != nil {
		t.Fatal(err)
	}
	checkServing(t, "http://"+publish)
	checkHealthy(t, compose)
	checkVersion(ctx, t, "http://"+publish, image)

	// The server runs as the image's user, not root.
	out, err := compose("top", "app")
	if err != nil {
		t.Fatal(err)
	}
	if uid := uidOfTop(out); uid == "" || uid == "0" || uid == "root" {
		t.Errorf("the server runs as the user %q:\n%s", uid, out)
	}

	// An upgrade restarts the server on the volumes it has, and it applies
	// the migrations that are pending, here none.
	if _, err := compose("up", "--detach", "--wait", "--wait-timeout", "300", "--force-recreate", "app"); err != nil {
		t.Fatal(err)
	}
	checkServing(t, "http://"+publish)
	checkHealthy(t, compose)
}

// cleanEnvironment returns the process environment without the variables
// that would configure the instance or Docker Compose, so that the test
// starts the compose file with the settings it gives alone.
func cleanEnvironment() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "STORYCREEP_") || strings.HasPrefix(upper, "POSTGRES_") ||
			strings.HasPrefix(upper, "COMPOSE_") || strings.HasPrefix(upper, "PG") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// checkServing checks that the instance at base is ready and serves the
// tool's entry page at /.
func checkServing(t *testing.T, base string) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	for path, want := range map[string]string{"/readyz": `{"status":"ready"}`, "/": "<!doctype html>"} {
		res, err := client.Get(base + path)
		if err != nil {
			t.Errorf("GET %s: %v", path, err)
			continue
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != http.StatusOK || !bytes.HasPrefix(body, []byte(want)) {
			t.Errorf("GET %s: %d %s, %v; want 200 and %s", path, res.StatusCode, body, err, want)
		}
	}
}

// checkVersion checks that the instance at base reports, at /version, the
// commit that image was built from, which its revision label names. The
// build stamps the commit into the binary with -ldflags -X
// (deploy/Dockerfile), which go build ignores for a variable that does not
// exist, so a renamed variable would drop the commit without an error. An
// image built without STORYCREEP_COMMIT has no revision to compare.
func checkVersion(ctx context.Context, t *testing.T, base, image string) {
	t.Helper()
	out, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format",
		`{{index .Config.Labels "org.opencontainers.image.revision"}}`, image).Output()
	if err != nil {
		t.Fatalf("docker image inspect %s: %v", image, err)
	}
	revision := strings.TrimSpace(string(out))
	if revision == "" {
		t.Logf("%s has no revision label, so its /version is not checked", image)
		return
	}
	res, err := (&http.Client{Timeout: 10 * time.Second}).Get(base + "/version")
	if err != nil {
		t.Fatalf("GET /version: %v", err)
	}
	defer res.Body.Close()
	var v struct {
		Commit string `json:"commit"`
	}
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("GET /version: %d, %v", res.StatusCode, err)
	}
	if v.Commit != revision {
		t.Errorf("/version reports the commit %q, and the image's revision label %q; want the same", v.Commit,
			revision)
	}
}

// checkHealthy checks that both services are running and healthy.
func checkHealthy(t *testing.T, compose func(...string) (string, error)) {
	t.Helper()
	out, err := compose("ps", "--all", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	type service struct {
		Service, State, Health string
	}
	// Docker Compose writes one object per line, and before 2.21 an array.
	var services []service
	if json.Unmarshal([]byte(out), &services) != nil {
		services = nil
		for line := range strings.Lines(out) {
			var s service
			if err := json.Unmarshal([]byte(line), &s); err != nil {
				t.Fatalf("docker compose ps: %v\n%s", err, out)
			}
			services = append(services, s)
		}
	}
	healthy := map[string]bool{}
	for _, s := range services {
		healthy[s.Service] = s.State == "running" && s.Health == "healthy"
	}
	if len(healthy) != 2 || !healthy["app"] || !healthy["db"] {
		t.Errorf("the services are not both running and healthy:\n%s", out)
	}
}

// uidOfTop returns the user of the first process that docker compose top
// lists, from its UID column.
func uidOfTop(out string) string {
	col := -1
	for line := range strings.Lines(out) {
		fields := strings.Fields(line)
		if col < 0 {
			for i, f := range fields {
				if f == "UID" {
					col = i
				}
			}
			continue
		}
		if col < len(fields) {
			return fields[col]
		}
	}
	return ""
}
