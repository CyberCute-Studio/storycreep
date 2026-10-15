// Package dbtest gives tests a real PostgreSQL database (PRD NFR-Q-01).
// testcontainers starts one PostgreSQL container per test binary, the
// embedded migrations are applied once to a template database, and New gives
// each test its own copy of the template, which is dropped when the test
// ends.
//
// The tests that use it need Docker. With go test -short they are skipped,
// which is how CI runs the suite on the runners that cannot run Linux
// containers; a Linux job runs them on every supported PostgreSQL version
// (PRD §9.9). Without -short, a missing Docker fails the test, so that a
// broken Docker never passes for a passing suite.
//
// Two environment variables configure it. Only tests import this package, so
// it is not part of the server, which reads its configuration only through
// package config.
//
//	STORYCREEP_TEST_POSTGRES_IMAGE  The image to run. The default is
//	                                postgres:17-alpine.
//	STORYCREEP_TEST_UPGRADE_FROM    A directory of migrations that the
//	                                template is migrated with first, before
//	                                the embedded migrations upgrade it. CI's
//	                                upgrade test (FR-ADMIN-04 AC2) points it at
//	                                the main branch's migrations.
package dbtest

import (
	"context"
	"crypto/rand"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
)

// The environment variables that configure the harness.
const (
	ImageVariable       = "STORYCREEP_TEST_POSTGRES_IMAGE"
	UpgradeFromVariable = "STORYCREEP_TEST_UPGRADE_FROM"
)

// DefaultImage is the image that runs when ImageVariable is not set.
const DefaultImage = "postgres:17-alpine"

// AppLogin is the application login that the template's migration
// provisions, and that every Database's AppURL connects as.
const AppLogin = "storycreep_test_app"

const (
	adminLogin = "postgres"
	template   = "storycreep_template"
)

// Database is a database of the test's own in the shared server.
type Database struct {
	// Name is the database's name.
	Name string
	// AdminURL connects to it as the superuser.
	AdminURL config.DatabaseURL
	// AppURL connects to it as AppLogin.
	AppURL config.DatabaseURL

	srv *server
}

// New returns a new database with the embedded migrations applied, or, with
// STORYCREEP_TEST_UPGRADE_FROM set, upgraded to them from the migrations in
// that directory. It is dropped when the test ends. CREATE DATABASE does not
// copy the template's privileges on the database itself, so any login can
// connect to it until it is migrated again, which restricts CONNECT to the
// application login.
func New(t testing.TB) *Database {
	t.Helper()
	s := start(t)
	return s.create(t, "TEMPLATE "+pgx.Identifier{template}.Sanitize())
}

// Empty returns a new database without migrations, for tests of migrating
// itself. It is dropped when the test ends. AppURL names AppLogin, which
// exists already, because the login belongs to the server rather than to
// the database.
func Empty(t testing.TB) *Database {
	t.Helper()
	s := start(t)
	return s.create(t, "TEMPLATE template0")
}

// IsolateLibpqEnvironment unsets the libpq environment variables, such as
// PGPASSWORD and PGSERVICE, and points PGPASSFILE at an empty file, for a test
// binary's TestMain to call before the tests run. pgx fills in from them, and
// from the user's password file, what a database URL leaves out, so a
// developer's settings would otherwise change what the tests parse and
// connect with, or fail them.
func IsolateLibpqEnvironment() {
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "PG") {
			os.Unsetenv(name)
		}
	}
	os.Setenv("PGPASSFILE", os.DevNull)
}

// Image returns the image the harness runs.
func Image() string {
	if image := os.Getenv(ImageVariable); image != "" {
		return image
	}
	return DefaultImage
}

// Open opens the application login's pool on the database and closes it when
// the test ends.
func (d *Database) Open(t testing.TB) *database.DB {
	t.Helper()
	db, err := database.Open(context.Background(), d.AppURL)
	if err != nil {
		t.Fatalf("open %s: %v", d.Name, err)
	}
	t.Cleanup(db.Close)
	return db
}

// Admin returns a connection to the database as the superuser, closed when
// the test ends.
func (d *Database) Admin(t testing.TB) *pgx.Conn {
	t.Helper()
	return Connect(t, d.AdminURL)
}

// Connect returns a connection to the database at url, closed when the test
// ends.
func Connect(t testing.TB, url config.DatabaseURL) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), url.Reveal())
	if err != nil {
		t.Fatalf("connect to %s: %v", url, err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

// URL returns the URL that connects to the database as login with password.
func (d *Database) URL(t testing.TB, login, password string) config.DatabaseURL {
	t.Helper()
	return d.srv.url(t, login, password, d.Name)
}

// NewLogin creates a login role with a name of its own, a random password,
// and the given extra attributes, such as "SUPERUSER", and returns the URL
// that connects to the database as it. Whatever the role owns in the
// database is dropped with it when the test ends.
func (d *Database) NewLogin(t testing.TB, attributes string) config.DatabaseURL {
	t.Helper()
	login := d.FreshLogin(t)
	password := rand.Text()
	if _, err := d.srv.admin.Exec(context.Background(), fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' %s",
		pgx.Identifier{login}.Sanitize(), password, attributes)); err != nil {
		t.Fatalf("create the login %s: %v", login, err)
	}
	return d.URL(t, login, password)
}

// FreshLogin returns a role name of the test's own, which no role has yet.
// When the test ends, the role with that name is dropped, with whatever it
// owns in the database, if something created it.
func (d *Database) FreshLogin(t testing.TB) string {
	t.Helper()
	ctx := context.Background()
	login := fmt.Sprintf("test_login_%d", d.srv.n.Add(1))
	ident := pgx.Identifier{login}.Sanitize()
	t.Cleanup(func() {
		var exists bool
		err := d.srv.admin.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = $1)", login).Scan(&exists)
		if err == nil && exists {
			var conn *pgx.Conn
			if conn, err = pgx.Connect(ctx, d.AdminURL.Reveal()); err == nil {
				_, err = conn.Exec(ctx, "DROP OWNED BY "+ident)
				conn.Close(ctx)
			}
			if err == nil {
				_, err = d.srv.admin.Exec(ctx, "DROP ROLE "+ident)
			}
		}
		if err != nil {
			t.Errorf("drop the login %s: %v", login, err)
		}
	})
	return login
}

// server is the PostgreSQL server of the test binary.
type server struct {
	host, port    string
	adminPassword string
	appPassword   string
	// admin is a pool of the superuser on the postgres database, for creating
	// and dropping databases and roles.
	admin *pgxpool.Pool
	// createMu serializes creating databases, because PostgreSQL refuses to
	// copy a template while another session uses it.
	createMu sync.Mutex
	n        atomic.Int64
}

var shared struct {
	once sync.Once
	srv  *server
	err  error
}

// start returns the shared server, starting it on first use.
func start(t testing.TB) *server {
	t.Helper()
	if testing.Short() {
		t.Skip("skipped with -short: needs PostgreSQL, which the tests run in Docker")
	}
	shared.once.Do(func() {
		shared.srv, shared.err = startServer(context.Background())
	})
	if shared.err != nil {
		t.Fatal(shared.err)
	}
	return shared.srv
}

func startServer(ctx context.Context) (*server, error) {
	s := &server{adminPassword: rand.Text(), appPassword: rand.Text()}
	c, err := postgres.Run(ctx, Image(),
		postgres.WithUsername(adminLogin),
		postgres.WithPassword(s.adminPassword),
		postgres.WithDatabase("postgres"),
		postgres.BasicWaitStrategies(),
		// Durability is not needed for a test server, and every test's
		// pool has connections of its own.
		testcontainers.WithCmd("postgres", "-c", "fsync=off", "-c", "synchronous_commit=off",
			"-c", "full_page_writes=off", "-c", "max_connections=500"),
	)
	if err != nil {
		return nil, fmt.Errorf("start PostgreSQL (%s): %w\n"+
			"The database tests run PostgreSQL in Docker. Start Docker, or run go test -short to skip them.",
			Image(), err)
	}
	// testcontainers removes the container when the test binary exits.
	if s.host, err = c.Host(ctx); err != nil {
		return nil, err
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return nil, err
	}
	s.port = port.Port()

	adminURL, err := s.parseURL(adminLogin, s.adminPassword, "postgres")
	if err != nil {
		return nil, err
	}
	if s.admin, err = pgxpool.New(ctx, adminURL.Reveal()); err != nil {
		return nil, err
	}
	if err := s.buildTemplate(ctx); err != nil {
		return nil, fmt.Errorf("build the template database: %w", err)
	}
	return s, nil
}

// buildTemplate creates the template database and migrates it.
func (s *server) buildTemplate(ctx context.Context) error {
	ident := pgx.Identifier{template}.Sanitize()
	if _, err := s.admin.Exec(ctx, "CREATE DATABASE "+ident+" TEMPLATE template0"); err != nil {
		return err
	}
	admin, err := s.parseURL(adminLogin, s.adminPassword, template)
	if err != nil {
		return err
	}
	app, err := s.parseURL(AppLogin, s.appPassword, template)
	if err != nil {
		return err
	}
	if seed := os.Getenv(UpgradeFromVariable); seed != "" {
		if err := migrateSeed(ctx, admin, app, seed); err != nil {
			return fmt.Errorf("migrate with the seed migrations in %s: %w", seed, err)
		}
	}
	if _, err := database.Migrate(ctx, admin, app, database.MigrateOptions{}); err != nil {
		return err
	}
	// Copying the template fails while anything is connected to it.
	_, err = s.admin.Exec(ctx, "ALTER DATABASE "+ident+" ALLOW_CONNECTIONS false")
	return err
}

// migrateSeed applies the migrations in the directory seed, if it has any.
func migrateSeed(ctx context.Context, admin, app config.DatabaseURL, seed string) error {
	if _, err := os.Stat(seed); err != nil {
		return err
	}
	fsys := os.DirFS(seed)
	if names, err := fs.Glob(fsys, "*.sql"); err != nil || len(names) == 0 {
		// Before the main branch had migrations, the upgrade starts from an
		// empty database.
		return err
	}
	_, err := database.Migrate(ctx, admin, app, database.MigrateOptions{Migrations: fsys})
	return err
}

// create creates a database of the test's own from the given template.
func (s *server) create(t testing.TB, from string) *Database {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("test_%d", s.n.Add(1))
	ident := pgx.Identifier{name}.Sanitize()
	s.createMu.Lock()
	_, err := s.admin.Exec(ctx, "CREATE DATABASE "+ident+" "+from)
	s.createMu.Unlock()
	if err != nil {
		t.Fatalf("create the database %s: %v", name, err)
	}
	t.Cleanup(func() {
		if _, err := s.admin.Exec(ctx, "DROP DATABASE "+ident+" WITH (FORCE)"); err != nil {
			t.Errorf("drop the database %s: %v", name, err)
		}
	})
	return &Database{
		Name:     name,
		AdminURL: s.url(t, adminLogin, s.adminPassword, name),
		AppURL:   s.url(t, AppLogin, s.appPassword, name),
		srv:      s,
	}
}

func (s *server) url(t testing.TB, login, password, db string) config.DatabaseURL {
	t.Helper()
	u, err := s.parseURL(login, password, db)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (s *server) parseURL(login, password, db string) (config.DatabaseURL, error) {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(login, password),
		Host:     net.JoinHostPort(s.host, s.port),
		Path:     "/" + db,
		RawQuery: "sslmode=disable",
	}
	return config.ParseDatabaseURL(u.String())
}
