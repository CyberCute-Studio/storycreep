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
