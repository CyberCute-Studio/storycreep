package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

// testLock is an advisory lock that a test migration waits for, so that a
// test can hold a server in the middle of its migrations.
const testLock = 0x74657374686f6c64 // "testhold"

// waitMigration is a migration that waits until the test releases testLock.
var waitMigration = fmt.Sprintf("SELECT pg_advisory_xact_lock(%d);", testLock)

// withMigrations returns the embedded migrations and extra, a migration's SQL
// by its file name.
func withMigrations(t *testing.T, extra map[string]string) fs.FS {
	t.Helper()
	m := fstest.MapFS{}
	err := fs.WalkDir(database.Migrations(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(database.Migrations(), p)
		m[p] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, sql := range extra {
		m[name] = &fstest.MapFile{Data: []byte("-- +goose Up\n" + sql + "\n")}
	}
	return m
}

// dbConfig returns a configuration for the database d.
func dbConfig(t *testing.T, d *dbtest.Database) *config.Config {
	t.Helper()
	return loadConfig(t, map[string]string{
		"STORYCREEP_DATABASE_URL":       d.AppURL.Reveal(),
		"STORYCREEP_DATABASE_ADMIN_URL": d.AdminURL.Reveal(),
	})
}

// instance is a server that Run runs for a test.
type instance struct {
	url, adminURL string
	log           *logBuffer
	done          chan error
	stop          context.CancelFunc
}

// listen returns a listener on a free port of the loopback interface.
func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// startInstance runs a server with cfg and opts until the test ends.
func startInstance(t *testing.T, cfg *config.Config, opts Options) *instance {
	t.Helper()
	ln, adminLn := listen(t), listen(t)
	opts.Listener, opts.AdminListener = ln, adminLn
	log, logged := newTestLog()
	opts.Log = log
	ctx, cancel := context.WithCancel(context.Background())
	inst := &instance{
		url:      "http://" + ln.Addr().String(),
		adminURL: "http://" + adminLn.Addr().String(),
		log:      logged,
		done:     make(chan error, 1),
		stop:     cancel,
	}
	go func() { inst.done <- Run(ctx, cfg, opts) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-inst.done:
		case <-time.After(time.Minute):
			t.Error("the server did not stop")
		}
	})
	return inst
}

// wait returns what Run returned.
func (inst *instance) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-inst.done:
		inst.done <- err
		return err
	case <-time.After(time.Minute):
		t.Fatal("the server did not stop")
		return nil
	}
}

// get requests path from the server and returns the status and the body,
// or a status of 0 if the server does not answer.
func get(t *testing.T, url string) (int, []byte, http.Header) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		return 0, nil, nil
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, body, res.Header
}

// reason returns details.reason of an envelope.
func reason(body []byte) string {
	var env struct {
		Error api.ErrorBody `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil {
		return ""
	}
	r, _ := env.Error.Details["reason"].(string)
	return r
}

// waitFor polls the server's path until it answers with status.
func (inst *instance) waitFor(t *testing.T, path string, status int) []byte {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(20 * time.Millisecond) {
		got, body, _ := get(t, inst.url+path)
		if got == status {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s answers %d %s, want %d", path, got, body, status)
		}
	}
}

// waitForLockWaiters waits until n sessions of conn's database wait for an
// advisory lock.
func waitForLockWaiters(t *testing.T, conn *pgx.Conn, n int) {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		var waiting int
		if err := conn.QueryRow(context.Background(), `SELECT count(*) FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted
				AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d sessions wait for an advisory lock, want %d", waiting, n)
		}
	}
}

// appliedCounts returns how many times the database recorded each migration
// as applied, by version.
func appliedCounts(t *testing.T, conn *pgx.Conn) map[int64]int {
	t.Helper()
	rows, err := conn.Query(context.Background(),
		"SELECT version_id, count(*) FROM public.goose_db_version WHERE version_id > 0 AND is_applied GROUP BY version_id")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[int64]int{}
	var v int64
	var n int
	if _, err := pgx.ForEachRow(rows, []any{&v, &n}, func() error { counts[v] = n; return nil }); err != nil {
		t.Fatal(err)
	}
	return counts
}

// TestReadyzWhileMigrationsPending checks that /readyz, and the API, answer
// 503 until the database has its migrations: while the server applies them
// at startup, and, without auto-migration, until storycreep migrate applies
// them. /healthz answers 200 throughout.
//
// Verifies: FR-ADMIN-05/AC1
func TestReadyzWhileMigrationsPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("while migrating", func(t *testing.T) {
		t.Parallel()
		d := dbtest.Empty(t)
		holder := d.Admin(t)
		if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", testLock); err != nil {
			t.Fatal(err)
		}
		inst := startInstance(t, dbConfig(t, d), Options{AutoMigrate: true,
			Migrations: withMigrations(t, map[string]string{"90001_wait.sql": waitMigration})})
		inst.waitFor(t, "/healthz", http.StatusOK)
		waitForLockWaiters(t, holder, 1)
		checkNotReady(t, inst, "starting")
		if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", testLock); err != nil {
			t.Fatal(err)
		}
		inst.waitFor(t, "/readyz", http.StatusOK)
		if status, _, _ := get(t, inst.url+"/api/v1/nowhere"); status != http.StatusNotFound {
			t.Errorf("the API answers %d once the server is ready, want 404", status)
		}
	})

	t.Run("without auto-migrate", func(t *testing.T) {
		t.Parallel()
		d := dbtest.Empty(t)
		cfg := dbConfig(t, d)
		inst := startInstance(t, cfg, Options{PollInterval: 20 * time.Millisecond})
		inst.waitFor(t, "/healthz", http.StatusOK)
		// The server is starting until it has opened its pool.
		for deadline := time.Now().Add(time.Minute); ; time.Sleep(20 * time.Millisecond) {
			if _, body, _ := get(t, inst.url+"/readyz"); reason(body) == "migrations_pending" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the server does not report the pending migrations")
			}
		}
		checkNotReady(t, inst, "migrations_pending")
		if _, err := database.Migrate(ctx, cfg.DatabaseAdminURL, cfg.DatabaseURL, database.MigrateOptions{}); err != nil {
			t.Fatal(err)
		}
		inst.waitFor(t, "/readyz", http.StatusOK)
		if len(inst.log.records(t, "ready, now that the pending migrations have been applied")) != 1 {
			t.Errorf("the log does not say that the server became ready:\n%s", inst.log)
		}
	})
}

// checkNotReady checks that /readyz and the API answer not_ready with reason,
// and that /healthz answers 200.
func checkNotReady(t *testing.T, inst *instance, want string) {
	t.Helper()
	for _, path := range []string{"/readyz", "/api/v1/nowhere"} {
		status, body, h := get(t, inst.url+path)
		if status != http.StatusServiceUnavailable || reason(body) != want || h.Get("Retry-After") == "" {
			t.Errorf("%s answers %d %s, Retry-After %q; want 503 with the reason %s", path, status, body,
				h.Get("Retry-After"), want)
		}
	}
	if status, body, _ := get(t, inst.url+"/healthz"); status != http.StatusOK {
		t.Errorf("/healthz answers %d %s, want 200", status, body)
	}
}

// TestInstancesStartTogether starts two servers at once against a database
// without migrations, and checks that the migrations run once, by one of
// them, and that both become ready.
//
// Verifies: FR-ADMIN-04/AC1
func TestInstancesStartTogether(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	holder := d.Admin(t)
	// The first instance to take the migration lock stops at the test's
	// migration until the other waits for the lock too.
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", testLock); err != nil {
		t.Fatal(err)
	}
	migrations := withMigrations(t, map[string]string{"90001_wait.sql": waitMigration})
	cfg := dbConfig(t, d)
	instances := []*instance{
		startInstance(t, cfg, Options{AutoMigrate: true, Migrations: migrations}),
		startInstance(t, cfg, Options{AutoMigrate: true, Migrations: migrations}),
	}
	waitForLockWaiters(t, holder, 2)
	for _, inst := range instances {
		checkNotReady(t, inst, "starting")
	}
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", testLock); err != nil {
		t.Fatal(err)
	}
	for _, inst := range instances {
		inst.waitFor(t, "/readyz", http.StatusOK)
	}

	names, err := fs.Glob(migrations, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	counts := appliedCounts(t, holder)
	if len(counts) != len(names) {
		t.Errorf("the database has applied %d migrations, want %d", len(counts), len(names))
	}
	for v, n := range counts {
		if n != 1 {
			t.Errorf("migration %d was applied %d times", v, n)
		}
	}
	var applied []int
	for _, inst := range instances {
		applied = append(applied, len(inst.log.records(t, "applied a database migration")))
	}
	slices.Sort(applied)
	if !slices.Equal(applied, []int{0, len(names)}) {
		t.Errorf("the instances applied %v migrations, want one to apply all %d and the other none", applied,
			len(names))
	}
}

// TestFailedMigrationRefusesTraffic checks that a server whose startup
// migration fails answers 503 while it migrates, then stops listening and
// returns the error, and that the database stays at the last migration that
// succeeded.
//
// Verifies: FR-ADMIN-04/AC3
func TestFailedMigrationRefusesTraffic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	holder := d.Admin(t)
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", testLock); err != nil {
		t.Fatal(err)
	}
	migrations := withMigrations(t, map[string]string{
		"90001_wait.sql": waitMigration,
		"90002_fail.sql": "CREATE TABLE half_done (x int); SELECT 1/0;",
	})
	inst := startInstance(t, dbConfig(t, d), Options{AutoMigrate: true, Migrations: migrations})
	waitForLockWaiters(t, holder, 1)
	checkNotReady(t, inst, "starting")
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", testLock); err != nil {
		t.Fatal(err)
	}

	err := inst.wait(t)
	var merr *database.MigrationError
	if !errors.As(err, &merr) || merr.Version != 90002 || merr.DatabaseVersion != 90001 {
		t.Fatalf("Run returned %v, want the failure of migration 90002 at version 90001", err)
	}
	for _, url := range []string{inst.url, inst.adminURL} {
		if status, _, _ := get(t, url+"/healthz"); status != 0 {
			t.Errorf("%s still answers, with %d", url, status)
		}
	}
	counts := appliedCounts(t, holder)
	if counts[90001] != 1 || counts[90002] != 0 {
		t.Errorf("the database has applied %v, want 90001 and not 90002", counts)
	}
	var exists bool
	if err := holder.QueryRow(ctx, "SELECT to_regclass('public.half_done') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Errorf("the failed migration left its table behind (%v)", err)
	}
	if len(inst.log.records(t, "the database step of startup failed, so the server does not serve")) != 1 {
		t.Errorf("the log does not report the failure:\n%s", inst.log)
	}
}

// TestReadyzDatabaseUnreachable checks that a ready server reports that it is
// not ready while its database does not answer.
func TestReadyzDatabaseUnreachable(t *testing.T) {
	t.Parallel()
	log, logged := newTestLog()
	s := newServer(loadConfig(t, nil), log)
	db := dbtest.New(t).Open(t)
	s.ready.db.Store(db)
	s.ready.setPhase(phaseReady)
	if res := do(s.main, http.MethodGet, "/readyz", nil); res.status != http.StatusOK {
		t.Fatalf("/readyz: %d %s", res.status, res.body)
	}
	db.Close()
	res := do(s.main, http.MethodGet, "/readyz", nil)
	if res.status != http.StatusServiceUnavailable || res.details(t)["reason"] != "database_unreachable" {
		t.Errorf("/readyz with the database closed: %d %s", res.status, res.body)
	}
	do(s.main, http.MethodGet, "/readyz", nil)
	if n := len(logged.records(t, "the database does not answer, so the server reports that it is not ready")); n != 1 {
		t.Errorf("the unreachable database is logged %d times, want once", n)
	}
	// The API's requests are not refused: they find out for themselves.
	if res := do(s.main, http.MethodGet, "/api/v1/nowhere", nil); res.status != http.StatusNotFound {
		t.Errorf("the API answers %d, want 404", res.status)
	}
}

// TestRunStops checks that a server stops without an error when its context
// ends, and closes its listeners.
func TestRunStops(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	cfg := dbConfig(t, d)
	inst := startInstance(t, cfg, Options{AutoMigrate: true})
	inst.waitFor(t, "/readyz", http.StatusOK)
	if status, body, _ := get(t, inst.adminURL+"/metrics"); status != http.StatusOK ||
		!strings.Contains(string(body), "storycreep_http_requests_total") {
		t.Errorf("/metrics on the admin port: %d %.200s", status, body)
	}
	inst.stop()
	if err := inst.wait(t); err != nil {
		t.Errorf("Run returned %v after the context ended", err)
	}
	for _, url := range []string{inst.url, inst.adminURL} {
		if status, _, _ := get(t, url+"/healthz"); status != 0 {
			t.Errorf("%s still answers, with %d", url, status)
		}
	}
	if len(inst.log.records(t, "stopped")) != 1 {
		t.Errorf("the log does not say that the server stopped:\n%s", inst.log)
	}
}

func TestRunWithoutTheAdminURL(t *testing.T) {
	ln, adminLn := listen(t), listen(t)
	err := Run(context.Background(), loadConfig(t, nil), Options{AutoMigrate: true, Listener: ln, AdminListener: adminLn})
	if !errors.Is(err, ErrNoAdminURL) {
		t.Errorf("Run returned %v, want ErrNoAdminURL", err)
	}
	for _, l := range []net.Listener{ln, adminLn} {
		if c, err := net.Dial("tcp", l.Addr().String()); err == nil {
			c.Close()
			t.Errorf("%s is still open", l.Addr())
		}
	}
}

// TestRunCannotListen checks that a server that cannot listen on one of its
// addresses returns an error that names the key of the address, and closes
// the listener it was given for the other.
func TestRunCannotListen(t *testing.T) {
	t.Parallel()
	busy := listen(t)
	defer busy.Close()
	for _, key := range []string{"STORYCREEP_HTTP_ADDR", "STORYCREEP_ADMIN_ADDR"} {
		cfg := loadConfig(t, nil)
		given := listen(t)
		opts := Options{}
		if key == "STORYCREEP_HTTP_ADDR" {
			cfg.HTTPAddr, opts.AdminListener = busy.Addr().String(), given
		} else {
			cfg.AdminAddr, opts.Listener = busy.Addr().String(), given
		}
		if err := Run(context.Background(), cfg, opts); err == nil || !strings.Contains(err.Error(), "listen on "+key) {
			t.Errorf("%s in use: Run returned %v", key, err)
		}
		if c, err := net.Dial("tcp", given.Addr().String()); err == nil {
			c.Close()
			t.Errorf("%s in use: the other listener, %s, is still open", key, given.Addr())
		}
	}
}

// TestRunWithoutMetrics checks that a server with no metrics to serve does
// not open the admin port.
func TestRunWithoutMetrics(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	cfg := loadConfig(t, map[string]string{
		"STORYCREEP_MODE":               "hosted",
		"STORYCREEP_PUBLIC_URL":         "https://storycreep.example.com",
		"STORYCREEP_DATABASE_URL":       d.AppURL.Reveal(),
		"STORYCREEP_DATABASE_ADMIN_URL": d.AdminURL.Reveal(),
	})
	inst := startInstance(t, cfg, Options{AutoMigrate: true})
	inst.waitFor(t, "/readyz", http.StatusOK)
	if status, _, _ := get(t, inst.adminURL+"/metrics"); status != 0 {
		t.Errorf("the admin port answers with %d, want it closed", status)
	}
	if rs := inst.log.records(t, "listening"); len(rs) != 1 || !strings.Contains(fmt.Sprint(rs[0]["metrics"]),
		"STORYCREEP_METRICS_TOKEN is not set") {
		t.Errorf("the log does not say why there are no metrics: %v", rs)
	}
}
