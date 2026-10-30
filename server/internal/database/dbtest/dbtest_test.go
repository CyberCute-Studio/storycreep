package dbtest

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(Run(m))
}

// TestRealPostgres checks that a test's database is a real PostgreSQL server
// of the version the harness was asked for, so that a CI job set to test a
// version cannot quietly test another.
//
// Verifies: NFR-Q-01
func TestRealPostgres(t *testing.T) {
	d := New(t)
	var num int
	if err := d.Admin(t).QueryRow(context.Background(), "SELECT current_setting('server_version_num')::int").
		Scan(&num); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`^(?:.*/)?postgres:(\d+)`).FindStringSubmatch(Image())
	if m == nil {
		t.Logf("the image %s names no version; PostgreSQL %d is running", Image(), num/10000)
		return
	}
	if want, _ := strconv.Atoi(m[1]); num/10000 != want {
		t.Errorf("PostgreSQL %d is running, want %d from the image %s", num/10000, want, Image())
	}
	if num/10000 < 15 {
		t.Errorf("PostgreSQL %d is older than 15, the oldest version Storycreep supports", num/10000)
	}
}

// TestDatabasesAreSeparate checks that each test gets a database of its own,
// with the migrations applied and the application login usable.
func TestDatabasesAreSeparate(t *testing.T) {
	ctx := context.Background()
	a, b := New(t), New(t)
	if a.Name == b.Name {
		t.Fatalf("both databases are %s", a.Name)
	}
	if _, err := a.Admin(t).Exec(ctx, "CREATE TABLE only_in_a (x int)"); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := b.Admin(t).QueryRow(ctx, "SELECT to_regclass('only_in_a') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Errorf("a table created in %s is in %s", a.Name, b.Name)
	}
	pending, err := b.Open(t).Pending(ctx)
	if err != nil || len(pending) != 0 {
		t.Errorf("Pending() = %v, %v; want the migrations applied", pending, err)
	}
	if e := Empty(t); e.Name == a.Name || e.Name == b.Name {
		t.Errorf("the empty database reuses the name %s", e.Name)
	}
}

// TestDurability checks that the tests' server does not keep what it
// commits, which makes them faster, and that the benchmarks', which a
// benchmark run together with them gets, does, so that the server's
// benchmarks time what a commit costs (PRD §9.1).
func TestDurability(t *testing.T) {
	if got := fsync(t, New(t)); got != "off" {
		t.Errorf("the tests' server has fsync %s, want off", got)
	}
	var got string
	testing.Benchmark(func(b *testing.B) {
		// testing.Benchmark calls the function again, with a larger b.N,
		// until it takes a second, which one that returns at once never
		// does.
		if got == "" {
			got = fsync(b, New(b))
		}
	})
	// testing.Benchmark drops what a benchmark logs, its failure among it,
	// so a server that did not start is reported from here.
	if benchServer.err != nil {
		t.Fatalf("the benchmarks' server did not start: %v", benchServer.err)
	}
	if got != "on" {
		t.Errorf("the benchmarks' server has fsync %q, want on", got)
	}
}

// fsync returns the setting of fsync of the server of the database d.
func fsync(t testing.TB, d *Database) string {
	t.Helper()
	var v string
	if err := d.Admin(t).QueryRow(context.Background(), "SHOW fsync").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
