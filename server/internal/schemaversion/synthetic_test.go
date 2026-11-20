//go:build synthetic_schema

package schemaversion_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion/synthetic"
)

// The tests of the criteria that need two versions of the published format,
// which run against the test-only synthetic version (plan note N2), with the
// synthetic_schema build tag:
//
//	go test -tags synthetic_schema ./internal/schemaversion/...

// update rewrites the files that the tests compare with what they generate,
// the synthetic version's schemas and its fixture, before any test reads them
// (regenerate): go test -tags synthetic_schema ./internal/schemaversion/...
// -update. A run without the tag has no such flag.
var update = flag.Bool("update", false, "rewrite the synthetic version's schemas and fixture")

func init() {
	regenerate = func() error {
		if !*update {
			return nil
		}
		schemas, err := syntheticSchemas()
		if err != nil {
			return err
		}
		if err := writeTree(synthetic.SchemaDir, schemas); err != nil {
			return err
		}
		v1, err := readFiles("schema/fixtures/v1")
		if err != nil {
			return err
		}
		migrated, _, err := schemaversion.Migrate(v1, synthetic.Number)
		if err != nil {
			return err
		}
		return writeTree(synthetic.FixtureDir, migrated)
	}
}

// syntheticSchemas returns the synthetic version's JSON Schemas as Documents
// generates them, by their names in synthetic.SchemaDir.
func syntheticSchemas() (schemaversion.Files, error) {
	docs, err := synthetic.Documents()
	if err != nil {
		return nil, err
	}
	out := schemaversion.Files{}
	for p, data := range docs {
		out[path.Base(p)] = data
	}
	return out, nil
}

// writeTree replaces the files under dir, a directory from the repository's
// root, with files.
func writeTree(dir string, files schemaversion.Files) error {
	base := filepath.Join(root, filepath.FromSlash(dir))
	if err := os.RemoveAll(base); err != nil {
		return err
	}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		file := filepath.Join(base, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(file, files[p], 0o644); err != nil {
			return err
		}
	}
	return nil
}

// v1Fixture and syntheticFixture return the fixtures of version 1 and of the
// synthetic version.
func v1Fixture(t *testing.T) schemaversion.Files {
	t.Helper()
	return readTree(t, "schema/fixtures/v1")
}

func syntheticFixture(t *testing.T) schemaversion.Files {
	t.Helper()
	return readTree(t, synthetic.FixtureDir)
}

// sameFiles fails the test where got and want are not the same files, byte
// for byte.
func sameFiles(t *testing.T, what string, got, want schemaversion.Files) {
	t.Helper()
	for _, p := range slices.Sorted(maps.Keys(want)) {
		if !bytes.Equal(got[p], want[p]) {
			t.Errorf("%s: %s is not the same", what, p)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("%s: %s should not be there", what, p)
		}
	}
}

// TestSyntheticVersion checks that the synthetic version is one above the
// newest real version, which no real version is, as its files in
// testdata/synthetic-schema are, and that the server reads it, as the newest,
// with the tag.
func TestSyntheticVersion(t *testing.T) {
	real := realVersions(t)
	newest := real[len(real)-1]
	if synthetic.Number != newest+1 {
		t.Errorf("the synthetic version is %d, and the newest real version %d: renumber it to one above",
			synthetic.Number, newest)
	}
	if got, want := schemaversion.Supported(), (schemaversion.Range{Min: real[0], Max: synthetic.Number}); got !=
		want {
		t.Errorf("with the synthetic version, the server reads %s; want %s", got, want)
	}
	for _, dir := range []string{synthetic.SchemaDir, synthetic.FixtureDir} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
			t.Errorf("the synthetic version's %s: %v", dir, err)
		}
	}
	for _, dir := range []string{"published", "fixtures"} {
		entries, err := os.ReadDir(filepath.Join(root, "testdata", "synthetic-schema", dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if want := fmt.Sprintf("v%d", synthetic.Number); e.Name() != want {
				t.Errorf("testdata/synthetic-schema/%s holds %s, and only %s", dir, e.Name(), want)
			}
		}
	}
}

// TestRegister checks that a version is registered only as the one after the
// newest, with a validator, a migration, and a renderer, so that the server
// migrates a snapshot to, and renders one at, every version that it declares.
func TestRegister(t *testing.T) {
	valid := func(schemaversion.Files) error { return nil }
	up := func(f schemaversion.Files) (schemaversion.Files, []schemaversion.Entry, error) { return f, nil, nil }
	down := func(f schemaversion.Files) (schemaversion.Files, error) { return f, nil }
	next := synthetic.Number + 1
	for _, c := range []struct {
		name string
		v    schemaversion.Version
	}{
		{"the newest again", schemaversion.Version{Number: synthetic.Number, Validate: valid, Up: up, Down: down}},
		{"one that skips a version", schemaversion.Version{Number: next + 1, Validate: valid, Up: up, Down: down}},
		{"one without a validator", schemaversion.Version{Number: next, Up: up, Down: down}},
		{"one without a migration", schemaversion.Version{Number: next, Validate: valid, Down: down}},
		{"one without a renderer", schemaversion.Version{Number: next, Validate: valid, Up: up}},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Register of %s: want a panic", c.name)
				}
			}()
			schemaversion.Register(c.v)
		}()
	}
	if got := schemaversion.Supported().Max; got != synthetic.Number {
		t.Errorf("the newest version that the server reads is %d; want %d", got, synthetic.Number)
	}
}

// TestSyntheticSchemas checks that testdata/synthetic-schema holds the
// synthetic version's JSON Schemas as Documents generates them; -update
// writes them.
func TestSyntheticSchemas(t *testing.T) {
	want, err := syntheticSchemas()
	if err != nil {
		t.Fatal(err)
	}
	sameFiles(t, synthetic.SchemaDir+" (go test -tags synthetic_schema ./internal/schemaversion/... -update)",
		readTree(t, synthetic.SchemaDir), want)
}

// TestSyntheticFixture checks that the synthetic version's fixture is the
// migration of version 1's to it; -update writes it.
func TestSyntheticFixture(t *testing.T) {
	migrated, _, err := schemaversion.Migrate(v1Fixture(t), synthetic.Number)
	if err != nil {
		t.Fatal(err)
	}
	sameFiles(t, synthetic.FixtureDir+" (go test -tags synthetic_schema ./internal/schemaversion/... -update)",
		syntheticFixture(t), migrated)
}

// wantEntries returns what the migration of f, of version 1, to the
// synthetic version reports, read of it by the types of package published
// rather than by the migration's trees: each node whose position is not
// whole, in the order of their boards' paths and of their files, with its
// position before and after, rounded half away from zero.
func wantEntries(t *testing.T, f schemaversion.Files) []schemaversion.Entry {
	t.Helper()
	var out []schemaversion.Entry
	for _, p := range slices.Sorted(maps.Keys(f)) {
		if !strings.HasPrefix(p, "boards/") {
			continue
		}
		var b published.Board
		if err := published.Decode(f[p], &b); err != nil {
			t.Fatal(err)
		}
		for _, n := range b.Nodes {
			var pos published.Position
			switch n := n.(type) {
			case *published.Element:
				pos = n.Position
			case *published.Branch:
				pos = n.Position
			case *published.Jumper:
				pos = n.Position
			}
			whole := published.Position{X: math.Round(pos.X), Y: math.Round(pos.Y)}
			if whole == pos {
				continue
			}
			out = append(out, schemaversion.Entry{File: p, Entity: n.NodeID(), Field: "position",
				Before: mustRaw(t, map[string]float64{"x": pos.X, "y": pos.Y}),
				After:  mustRaw(t, map[string]float64{"x": whole.X + 0, "y": whole.Y + 0})})
		}
	}
	return out
}

// mustRaw returns v as JSON, or fails the test.
func mustRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestMigrationReportsWhatItRounds checks that the migration of version 1's
// fixture to the synthetic version reports, in one step, each position that
// it rounded, the fixture's 0.13, −0.05, 999999999.99, 0.01, 1.5, and 2.5
// among them, with its value before and after, and nothing else, so that it
// loses no value silently; and that a snapshot that the synthetic version
// holds whole, as version 1's rendering of the synthetic fixture, migrates
// back to the fixture itself, reporting nothing.
//
// Verifies: FR-DATA-03/AC2
func TestMigrationReportsWhatItRounds(t *testing.T) {
	f := v1Fixture(t)
	_, steps, err := schemaversion.Migrate(f, synthetic.Number)
	if err != nil {
		t.Fatal(err)
	}
	want := wantEntries(t, f)
	if len(want) < 4 {
		t.Fatalf("the fixture of version 1 has %d positions that are not whole, too few to test", len(want))
	}
	if len(steps) != 1 || steps[0].From != synthetic.Number-1 || steps[0].To != synthetic.Number ||
		!sameEntries(t, steps[0].Entries, want) {
		t.Errorf("the migration's steps are %+v; want one from %d to %d of %+v", steps, synthetic.Number-1,
			synthetic.Number, want)
	}
	rendered, _, err := schemaversion.Render(syntheticFixture(t), synthetic.Number-1)
	if err != nil {
		t.Fatal(err)
	}
	back, steps, err := schemaversion.Migrate(rendered, synthetic.Number)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || len(steps[0].Entries) != 0 {
		t.Errorf("the migration of whole positions reports %+v; want one step of nothing", steps)
	}
	sameFiles(t, "the synthetic fixture rendered at version 1 and migrated back", back, syntheticFixture(t))
}

// TestMigrateAndRenderTakeWhatValidates checks that Migrate and Render
// refuse a snapshot that does not validate at its version, one whose file's
// hash is not the manifest's, rather than migrate it forward or render it
// down and seal what they write again (Seal), which would hide it.
func TestMigrateAndRenderTakeWhatValidates(t *testing.T) {
	const board = "boards/harbour-intro.json"
	retitle := func(b map[string]any) { nodeOf(t, b, "nd_000000000000000000000000N1")["title"] = "Retitled" }
	older := unsealedEdit(t, v1Fixture(t), board, retitle)
	newer := unsealedEdit(t, syntheticFixture(t), board, retitle)
	for _, c := range []struct {
		name     string
		run      func(schemaversion.Files, int) (schemaversion.Files, []schemaversion.Step, error)
		f        schemaversion.Files
		from, to int
	}{
		{"Migrate forward", schemaversion.Migrate, older, synthetic.Number - 1, synthetic.Number},
		{"Render forward", schemaversion.Render, older, synthetic.Number - 1, synthetic.Number},
		{"Render down", schemaversion.Render, newer, synthetic.Number, synthetic.Number - 1},
	} {
		got, _, err := c.run(c.f, c.to)
		if want := fmt.Sprintf("does not validate at its version %d", c.from); err == nil || got != nil ||
			!strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "with the hash of its bytes") {
			t.Errorf("%s of a file whose hash is not the manifest's: %v; want an error that says %q", c.name, err, want)
		}
	}
}

// TestMigrationLogStored checks that the steps of the migration of a
// snapshot's files are stored in its project's migration log, with what they
// reported, as Migrate returned them.
//
// Verifies: FR-DATA-03/AC2
func TestMigrationLogStored(t *testing.T) {
	t.Parallel()
	_, steps, err := schemaversion.Migrate(v1Fixture(t), synthetic.Number)
	if err != nil {
		t.Fatal(err)
	}
	e := newLogEnv(t)
	project := e.project(t)
	snapshot := e.snapshot(t, project)
	e.record(t, project, snapshot, steps)
	got := e.log(t, project)
	if len(got) != 1 || got[0].Snapshot != ids.Encode(ids.Snapshot, snapshot) || got[0].From != steps[0].From ||
		got[0].To != steps[0].To || !sameEntries(t, got[0].Entries, steps[0].Entries) {
		t.Errorf("the project's log holds %+v; want the snapshot's steps %+v", got, steps)
	}
}

// withTitle returns f, of the synthetic version, with the title of the node
// id of the board's file at path n characters long.
func withTitle(t *testing.T, f schemaversion.Files, path, id string, n int) schemaversion.Files {
	t.Helper()
	return edited(t, f, path, func(b map[string]any) {
		nodeOf(t, b, id)["title"] = strings.Repeat("é", n)
	})
}

// TestRenderDown checks that a snapshot of the synthetic version renders at
// the version before, where it validates, with every position whole, and
// whose migration back is the snapshot itself; and that one that holds what
// the version before cannot express, a title of more than 200 characters,
// fails to render, naming each such node, rather than render what that
// version cannot express.
//
// Verifies: FR-DATA-03/AC3
func TestRenderDown(t *testing.T) {
	f := syntheticFixture(t)
	rendered, steps, err := schemaversion.Render(f, synthetic.Number-1)
	if err != nil || steps != nil {
		t.Fatalf("the synthetic fixture renders at version %d: %v, %v", synthetic.Number-1, steps, err)
	}
	if got, err := schemaversion.VersionOf(rendered); err != nil || got != synthetic.Number-1 {
		t.Errorf("the rendering is of version %d, %v", got, err)
	}
	if err := schemaversion.Validate(rendered); err != nil {
		t.Errorf("the rendering does not validate at version %d: %v", synthetic.Number-1, err)
	}
	const (
		intro = "boards/harbour-intro.json"
		gate  = "boards/keepers-gate.json"
		n1    = "nd_000000000000000000000000N1"
		n2    = "nd_000000000000000000000000N2"
		n10   = "nd_00000000000000000000000N10"
	)
	// 200 characters, which version 1 holds, render.
	fits := withTitle(t, f, intro, n1, 200)
	if _, _, err := schemaversion.Render(fits, synthetic.Number-1); err != nil {
		t.Errorf("a title of 200 characters does not render: %v", err)
	}
	// 201 and 400, which the synthetic version holds, do not, and each is
	// named, in the order of their files and nodes.
	long := withTitle(t, withTitle(t, withTitle(t, f, gate, n10, 201), intro, n2, 400), intro, n1, 300)
	if err := schemaversion.Validate(long); err != nil {
		t.Fatalf("titles of up to 400 characters do not validate at the synthetic version: %v", err)
	}
	_, _, err = schemaversion.Render(long, synthetic.Number-1)
	var unrepresentable *schemaversion.UnrepresentableError
	if !errors.As(err, &unrepresentable) {
		t.Fatalf("titles of more than 200 characters render: %v; want an *UnrepresentableError", err)
	}
	var named []string
	for _, c := range unrepresentable.Constructs {
		named = append(named, c.File+" "+c.Entity+" "+c.Field)
	}
	if want := []string{intro + " " + n1 + " title", intro + " " + n2 + " title", gate + " " + n10 + " title"}; !slices.Equal(named, want) ||
		unrepresentable.From != synthetic.Number || unrepresentable.To != synthetic.Number-1 {
		t.Errorf("the error names %v, from %d to %d; want %v", named, unrepresentable.From, unrepresentable.To, want)
	}
	for _, want := range []string{n1, n2, n10, "300 characters", "at most 200"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err, want)
		}
	}
}

// TestSyntheticValidate checks that the synthetic version's schemas refuse
// what it does not hold: a position that is not whole, a title of more than
// 400 characters, and a file of another version beside its manifest.
func TestSyntheticValidate(t *testing.T) {
	f := syntheticFixture(t)
	const intro = "boards/harbour-intro.json"
	for _, c := range []struct {
		name string
		f    schemaversion.Files
	}{
		{"a position that is not whole", edited(t, f, intro, func(b map[string]any) {
			nodeOf(t, b, "nd_000000000000000000000000N1")["position"].(map[string]any)["x"] = json.Number("1.5")
		})},
		{"a title of 401 characters", withTitle(t, f, intro, "nd_000000000000000000000000N1", 401)},
		{"a board's file of the version before", edited(t, f, intro, func(b map[string]any) {
			b["schemaVersion"] = json.Number(fmt.Sprint(synthetic.Number - 1))
		})},
	} {
		if err := schemaversion.Validate(c.f); err == nil || !strings.Contains(err.Error(), "against its schema") {
			t.Errorf("%s: %v; want an error of its schema", c.name, err)
		}
	}
}
