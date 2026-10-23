package deploy

import (
	"bufio"
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
)

// deployDir is the repository's deploy directory.
var deployDir = filepath.Join("..", "..", "..", "deploy")

// composeFile is the part of a compose file that the tests read.
type composeFile struct {
	Name     string                    `yaml:"name"`
	Services map[string]composeService `yaml:"services"`
	Volumes  map[string]any            `yaml:"volumes"`
}

type composeService struct {
	Image       string            `yaml:"image"`
	Command     []string          `yaml:"command"`
	Restart     string            `yaml:"restart"`
	Ports       []string          `yaml:"ports"`
	Environment map[string]string `yaml:"environment"`
	Volumes     []string          `yaml:"volumes"`
	DependsOn   map[string]struct {
		Condition string `yaml:"condition"`
	} `yaml:"depends_on"`
	Healthcheck *struct {
		Test []string `yaml:"test"`
	} `yaml:"healthcheck"`
	StopGracePeriod string `yaml:"stop_grace_period"`
	Develop         *struct {
		Watch []struct {
			Path   string `yaml:"path"`
			Action string `yaml:"action"`
		} `yaml:"watch"`
	} `yaml:"develop"`
}

// readCompose reads the compose file name of the deploy directory, and
// returns it with its text.
func readCompose(t *testing.T, name string) (composeFile, string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(deployDir, name))
	if err != nil {
		t.Fatal(err)
	}
	var f composeFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return f, string(b)
}

// interpolation matches a variable that Docker Compose interpolates, as
// ${NAME}, ${NAME:-default}, or ${NAME:?error}, and captures its name and
// operator.
var interpolation = regexp.MustCompile(`\$\{([A-Z0-9_]+)(:?[-?])?`)

// interpolatedVars returns the variables that text requires, with
// ${NAME:?…} or ${NAME?…}, and the others it interpolates.
func interpolatedVars(text string) (required, optional []string) {
	for _, m := range interpolation.FindAllStringSubmatch(text, -1) {
		name, op := m[1], m[2]
		switch {
		case strings.HasSuffix(op, "?"):
			if !slices.Contains(required, name) {
				required = append(required, name)
			}
		case !slices.Contains(optional, name):
			optional = append(optional, name)
		}
	}
	return required, optional
}

// TestComposeFile checks the published compose file against PRD FR-ADMIN-07
// and §10.8: exactly two services, PostgreSQL 17 and the server, which waits
// for the database to be healthy; named volumes for the database and the
// assets; a health check for each service; and environment variables for
// the three database URLs and every other key of the configuration, of which
// only the database password has no default.
func TestComposeFile(t *testing.T) {
	f, text := readCompose(t, "docker-compose.yml")

	if got := slices.Sorted(maps.Keys(f.Services)); !slices.Equal(got, []string{"app", "db"}) {
		t.Fatalf("the services are %q, want app and db", got)
	}
	db, app := f.Services["db"], f.Services["app"]

	if db.Image != "postgres:17" {
		t.Errorf("db's image is %q, want postgres:17, the official PostgreSQL 17 image", db.Image)
	}
	if want := "${STORYCREEP_IMAGE:-ghcr.io/cybercute-studio/storycreep:main}"; app.Image != want {
		t.Errorf("app's image is %q, want %q", app.Image, want)
	}
	if got := slices.Sorted(maps.Keys(f.Volumes)); !slices.Equal(got, []string{"assets", "db"}) {
		t.Errorf("the named volumes are %q, want assets and db", got)
	}
	if !slices.Equal(db.Volumes, []string{"db:/var/lib/postgresql/data"}) {
		t.Errorf("db mounts %q, want the volume db at /var/lib/postgresql/data", db.Volumes)
	}
	if !slices.Equal(app.Volumes, []string{"assets:/var/lib/storycreep/assets"}) {
		t.Errorf("app mounts %q, want the volume assets at /var/lib/storycreep/assets", app.Volumes)
	}
	for name, s := range f.Services {
		if s.Healthcheck == nil || len(s.Healthcheck.Test) == 0 {
			t.Errorf("%s has no health check", name)
		}
		if s.Restart != "unless-stopped" {
			t.Errorf("%s restarts %q, want unless-stopped", name, s.Restart)
		}
	}
	if want := []string{"CMD", "/usr/local/bin/storycreep", "healthcheck"}; app.Healthcheck != nil &&
		!slices.Equal(app.Healthcheck.Test, want) {
		t.Errorf("app's health check is %q, want %q", app.Healthcheck.Test, want)
	}
	if dep, ok := app.DependsOn["db"]; !ok || dep.Condition != "service_healthy" || len(app.DependsOn) != 1 {
		t.Errorf("app depends on %v, want db, once it is healthy", app.DependsOn)
	}
	if want := []string{"${STORYCREEP_PUBLISH:-127.0.0.1:7777}:8080"}; !slices.Equal(app.Ports, want) {
		t.Errorf("app publishes %q, want %q", app.Ports, want)
	}
	if len(db.Ports) != 0 {
		t.Errorf("db publishes %q, want no port", db.Ports)
	}
	// serve waits up to 20 seconds for requests and 5 more for jobs.
	if d, err := time.ParseDuration(app.StopGracePeriod); err != nil || d < 25*time.Second {
		t.Errorf("app's stop_grace_period is %q, want 25s or more", app.StopGracePeriod)
	}

	env := app.Environment
	for name, want := range map[string]string{
		"STORYCREEP_HTTP_ADDR":                ":8080",
		"STORYCREEP_DATABASE_URL":             "postgres://storycreep@db:5432/storycreep",
		"STORYCREEP_DATABASE_ADMIN_URL":       "postgres://postgres@db:5432/storycreep",
		"STORYCREEP_DATABASE_MAINTENANCE_URL": "postgres://storycreep_maintainer@db:5432/storycreep",
		"PGPASSWORD":                          "${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env next to docker-compose.yml}",
		"STORYCREEP_PUBLIC_URL":               "${STORYCREEP_PUBLIC_URL:-http://localhost:7777}",
	} {
		if env[name] != want {
			t.Errorf("app sets %s to %q, want %q", name, env[name], want)
		}
	}
	if db.Environment["POSTGRES_PASSWORD"] != env["PGPASSWORD"] {
		t.Errorf("db's POSTGRES_PASSWORD is %q, and app's PGPASSWORD %q; want the same",
			db.Environment["POSTGRES_PASSWORD"], env["PGPASSWORD"])
	}
	// A configuration file would need a volume of its own; the compose file
	// configures the server with variables instead.
	for _, name := range config.Variables() {
		if _, ok := env[name]; !ok && name != "STORYCREEP_CONFIG_FILE" {
			t.Errorf("app does not set %s, so a deployment cannot configure it; add it to the environment, "+
				"to .env.example, and to README.md", name)
		}
	}
	for name := range env {
		if strings.HasPrefix(name, "STORYCREEP_") && !slices.Contains(config.Variables(), name) {
			t.Errorf("app sets %s, which the server does not read", name)
		}
	}

	required, optional := interpolatedVars(text)
	if !slices.Equal(required, []string{"POSTGRES_PASSWORD"}) {
		t.Errorf("the compose file requires %q, want POSTGRES_PASSWORD alone (PRD FR-ADMIN-07 AC1)", required)
	}
	documented := envExampleVars(t)
	for _, name := range append(required, optional...) {
		if !slices.Contains(documented, name) {
			t.Errorf(".env.example does not describe %s, which the compose file reads", name)
		}
	}
	for _, name := range documented {
		if !slices.Contains(required, name) && !slices.Contains(optional, name) && name != projectNameVar {
			t.Errorf(".env.example describes %s, which the compose file does not read", name)
		}
	}

	// The project's name is fixed, so that the documented volume names do
	// not depend on the directory, and a second instance on a machine needs
	// COMPOSE_PROJECT_NAME, which .env.example and README.md describe.
	if f.Name != "storycreep" {
		t.Errorf("the compose project is named %q, want storycreep, which README.md documents", f.Name)
	}
	if !slices.Contains(documented, projectNameVar) {
		t.Errorf(".env.example does not describe %s, which a second instance on a machine needs", projectNameVar)
	}
}

// projectNameVar is the variable that names a Docker Compose project, which
// Docker Compose reads itself, from the environment or .env, and which
// overrides the compose file's name.
const projectNameVar = "COMPOSE_PROJECT_NAME"

// devProjectName is the development compose file's project name, which its
// volumes' names start with.
const devProjectName = "storycreep-dev"

var variableName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// envExampleVars returns the variables that .env.example sets or shows in a
// comment, as #NAME=value.
func envExampleVars(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(deployDir, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		line := strings.TrimPrefix(strings.TrimSpace(s.Text()), "#")
		name, _, ok := strings.Cut(line, "=")
		if ok && variableName.MatchString(name) {
			names = append(names, name)
		}
	}
	return names
}

// devComposeFile is the development compose file, relative to the deploy
// directory.
var devComposeFile = filepath.Join("dev", "docker-compose.yml")

// TestDevComposeFile checks the development compose file (PRD §10.8): the
// database's port is published, on the loopback interface only, and the
// server is built from the mounted source tree, and rebuilt and restarted
// when it changes. It must stay apart from an instance of
// docker-compose.yml, even one run from the deploy directory.
func TestDevComposeFile(t *testing.T) {
	f, _ := readCompose(t, devComposeFile)

	// Docker Compose reads the .env of a compose file's directory, where a
	// COMPOSE_PROJECT_NAME renames the project, so a compose file next to
	// docker-compose.yml would join the project of the instance that the
	// .env configures, and replace its containers.
	others, err := filepath.Glob(filepath.Join(deployDir, "*.y*ml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range others {
		if filepath.Base(p) != "docker-compose.yml" {
			t.Errorf("%s shares the deploy directory, and so its .env, with docker-compose.yml; give it a directory "+
				"of its own", p)
		}
	}
	// A COMPOSE_PROJECT_NAME in the shell renames this file's project too,
	// so the volumes are named here, or this file's database could be given
	// an instance's volume, which "down -v" would delete.
	if f.Name != devProjectName {
		t.Errorf("the development compose project is named %q, want %s", f.Name, devProjectName)
	}
	for name, v := range f.Volumes {
		settings, _ := v.(map[string]any)
		if got, want := settings["name"], devProjectName+"_"+name; got != want {
			t.Errorf("the volume %s is named %v, want %s, whatever the project's name", name, got, want)
		}
	}

	if got := slices.Sorted(maps.Keys(f.Services)); !slices.Equal(got, []string{"app", "db"}) {
		t.Fatalf("the services are %q, want app and db", got)
	}
	db, app := f.Services["db"], f.Services["app"]
	if want := []string{"127.0.0.1:${STORYCREEP_DEV_DB_PORT:-5432}:5432"}; !slices.Equal(db.Ports, want) {
		t.Errorf("db publishes %q, want %q", db.Ports, want)
	}
	for _, p := range app.Ports {
		if !strings.HasPrefix(p, "127.0.0.1:") {
			t.Errorf("app publishes %q on every interface, want the loopback interface only", p)
		}
	}
	if !slices.Contains(app.Volumes, "../..:/src:ro") {
		t.Errorf("app mounts %q, want the repository at /src", app.Volumes)
	}
	if app.Develop == nil || len(app.Develop.Watch) != 1 || app.Develop.Watch[0].Path != "../../server" ||
		app.Develop.Watch[0].Action != "restart" {
		t.Errorf("app does not restart when ../../server changes: %+v", app.Develop)
	}
	// The server must be the container's first process, which receives the
	// SIGTERM that stops it: go run would keep the signal, and exit, and the
	// server would be killed without shutting down.
	if n := len(app.Command); n == 0 || app.Command[0] != "sh" || !strings.Contains(app.Command[n-1], "&& exec ") {
		t.Errorf("app runs %q; want a shell that builds the server and then execs it", app.Command)
	}
	// The development server is built by the Go that builds the image, so
	// that a go.mod that needs a newer Go fails both builds alike.
	if goImage := goImageOfDockerfile(t); app.Image != goImage {
		t.Errorf("app runs %q, and the Dockerfile builds with %q; want the same", app.Image, goImage)
	}
}

// TestCIGoVersion checks that every job of CI sets up the Go that the
// Dockerfile builds the server with, its version as the image's tag writes
// it, as golang:1.26-alpine's 1.26 or golang:1.26.5@sha256:…'s 1.26.5, and
// its newest patch (check-latest), as the image has it, rather than one that
// the runner has cached: a newer Go's Unicode tables may be of another
// version, which the rich text's word counts and link rules read, so that CI
// would test tables that the server does not have, and TestUnicodeTables
// would fail on a pull request that changes neither; and an older patch's
// standard library may have vulnerabilities that govulncheck reports and the
// image has not.
func TestCIGoVersion(t *testing.T) {
	version := regexp.MustCompile(`^golang:(\d+\.\d+(?:\.\d+)?)(?:[-@]|$)`)
	for image, want := range map[string]string{
		"golang:1.26-alpine": "1.26", "golang:1.26": "1.26", "golang:1.26.5-alpine3.22": "1.26.5",
		"golang:1.26@sha256:" + strings.Repeat("0", 64): "1.26", "golang:1.26.5-alpine@sha256:" + strings.Repeat("0", 64): "1.26.5",
		"golang:1.2x-alpine": "", "golang:latest": "", "golang:1-alpine": "",
	} {
		got := ""
		if m := version.FindStringSubmatch(image); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("the image %s names Go %q, want %q", image, got, want)
		}
	}
	goImage := goImageOfDockerfile(t)
	m := version.FindStringSubmatch(goImage)
	if m == nil {
		t.Fatalf("the Dockerfile builds with %q, whose tag starts with no version of Go, as 1.26 or 1.26.5", goImage)
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string         `yaml:"uses"`
				With map[string]any `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &workflow); err != nil {
		t.Fatal(err)
	}
	var setups int
	for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
		for _, step := range workflow.Jobs[name].Steps {
			if !strings.HasPrefix(step.Uses, "actions/setup-go@") {
				continue
			}
			setups++
			if got, ok := step.With["go-version"].(string); !ok || got != m[1] {
				t.Errorf("CI's job %s sets up Go %v, and the Dockerfile builds the server with %s: want %q",
					name, step.With["go-version"], goImage, m[1])
			}
			if latest, _ := step.With["check-latest"].(bool); !latest {
				t.Errorf("CI's job %s sets up the patch of Go that the runner has cached: want check-latest: true", name)
			}
		}
	}
	if setups == 0 {
		t.Error("no job of CI sets up Go")
	}
}

// goImageOfDockerfile returns the image that the Dockerfile's build stage
// runs.
func goImageOfDockerfile(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(deployDir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^FROM --platform=\$BUILDPLATFORM (\S+) AS build$`).FindSubmatch(b)
	if m == nil {
		t.Fatal("the Dockerfile has no build stage")
	}
	return string(m[1])
}
