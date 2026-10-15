package database

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// fstestFS returns migrations with the given file names, each a trivial Up
// section.
func fstestFS(names ...string) fs.FS {
	m := fstest.MapFS{}
	for _, n := range names {
		m[n] = &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 1;\n")}
	}
	return m
}

// gooseAnnotation returns the goose annotation that line holds, in upper
// case, or "" if it holds none. It reads the line as goose does, which takes
// any comment line with +goose in it for an annotation, drops every -- and
// the +goose, and compares the rest ignoring case, so that "-- +goose no
// transaction" and "--+goose Down" count too.
func gooseAnnotation(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "--") || !strings.Contains(line, "+goose") {
		return ""
	}
	cmd := strings.Replace(strings.ReplaceAll(line, "--", ""), "+goose", "", 1)
	return strings.ToUpper(strings.TrimSpace(cmd))
}

func TestGooseAnnotation(t *testing.T) {
	for line, want := range map[string]string{
		"-- +goose Up":             "UP",
		"-- +goose no transaction": "NO TRANSACTION",
		"--+goose Down":            "DOWN",
		"--   +goose   down":       "DOWN",
		"-- a comment":             "",
		"SELECT 1; -- +goose Down": "",
	} {
		if got := gooseAnnotation(line); got != want {
			t.Errorf("gooseAnnotation(%q) = %q, want %q", line, got, want)
		}
	}
}

// TestMigrationFiles checks the rules for the embedded migrations: each is
// named NNNNN_description.sql, the versions count up from 1 without a gap,
// each is forward-only, with an Up section and no Down section (PRD
// FR-ADMIN-04), each runs in a transaction, so that a failed one leaves the
// database at the one before (FR-ADMIN-04 AC3), and the directory holds no
// file that is not embedded.
//
// Numbered versions make two pull requests that add the same version collide
// in CI, where goose refuses two migrations with one version, instead of
// applying in different orders on different databases.
func TestMigrationFiles(t *testing.T) {
	fsys := Migrations()
	names, err := fs.Glob(fsys, "*")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("no migrations are embedded")
	}
	valid := regexp.MustCompile(`^(\d{5})_[a-z0-9]+(?:_[a-z0-9]+)*\.sql$`)
	for i, name := range names {
		m := valid.FindStringSubmatch(name)
		if m == nil {
			t.Errorf("%s: a migration is named NNNNN_description.sql, with a lowercase description", name)
			continue
		}
		if want := fmt.Sprintf("%05d", i+1); m[1] != want {
			t.Errorf("%s: the version is %s, want %s; versions count up from 00001 without a gap", name, m[1], want)
		}
		f, err := fsys.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		up, first := false, ""
		s := bufio.NewScanner(f)
		for s.Scan() {
			line := strings.TrimSpace(s.Text())
			if first == "" && line != "" {
				first = line
			}
			switch gooseAnnotation(line) {
			case "UP":
				up = true
			case "DOWN":
				t.Errorf("%s: has a Down section; migrations are forward-only", name)
			case "NO TRANSACTION":
				t.Errorf("%s: runs outside a transaction; each migration runs in its own, so that a failed one "+
					"leaves no trace", name)
			}
		}
		f.Close()
		if err := s.Err(); err != nil {
			t.Fatal(err)
		}
		if !up || first != "-- +goose Up" {
			t.Errorf("%s: must start with -- +goose Up", name)
		}
	}

	// Only .sql files are embedded, so a migration saved under another name
	// would be left out of the binary without the checks above seeing it.
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			t.Errorf("migrations/%s: is not embedded; the directory holds only the migrations, as .sql files", e.Name())
		}
	}
}

func TestLoadMigrations(t *testing.T) {
	ms, err := loadMigrations(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms.versions) == 0 || ms.versions[0] != 1 {
		t.Errorf("versions = %v, want them to start at 1", ms.versions)
	}

	for _, tt := range []struct {
		files []string
		want  string
	}{
		{[]string{"00001_a.sql", "1_b.sql"}, "two migrations have version 1"},
		{[]string{"a.sql"}, "a.sql"},
		{[]string{"00000_zero.sql"}, "00000_zero.sql"},
	} {
		fsys := fstestFS(tt.files...)
		if _, err := loadMigrations(fsys); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%v: error %v, want one about %q", tt.files, err, tt.want)
		}
	}
}

func TestPending(t *testing.T) {
	ms := migrationSet{versions: []int64{1, 2, 3}}
	if p, err := pending(ms, []int64{1}); err != nil || fmt.Sprint(p) != "[2 3]" {
		t.Errorf("pending = %v, %v; want [2 3]", p, err)
	}
	if p, err := pending(ms, nil); err != nil || len(p) != 3 {
		t.Errorf("pending on a new database = %v, %v; want all three", p, err)
	}
	_, err := pending(ms, []int64{1, 2, 3, 4, 7})
	uerr, ok := err.(*UnknownMigrationsError)
	if !ok || fmt.Sprint(uerr.Versions) != "[4 7]" || !strings.Contains(err.Error(), "4, 7") {
		t.Errorf("error %v, want an *UnknownMigrationsError for 4 and 7", err)
	}
}
