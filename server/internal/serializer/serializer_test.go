package serializer_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
)

var update = flag.Bool("update", false, "rewrite the golden snapshot from its draft")

// conformanceDir is schema/conformance, from this package's directory, whose
// suites' snapshots the conformance suite's Go suites wrote (package flow).
var conformanceDir = filepath.Join("..", "..", "..", "schema", "conformance")

// parseTimestamp reads a published moment.
func parseTimestamp(at published.Timestamp) (time.Time, error) {
	return time.Parse(time.RFC3339, string(at))
}

// TestConformanceSnapshots checks that each snapshot of the conformance
// suite, which its Go suites wrote, and which play mode's projection writes
// again too (web/app/src/play/projection.test.ts), read back as the draft
// that it was written of, serializes to its own bytes, file by file, its
// manifest among them, stamped as it is, with nothing stood in for: the same
// bytes on every platform that CI runs the tests on, Linux, Windows ARM64,
// and macOS, as the repository holds them with LF line endings (PRD R4).
//
// Verifies: FR-SNAP-03/AC1
func TestConformanceSnapshots(t *testing.T) {
	entries, err := os.ReadDir(conformanceDir)
	if err != nil {
		t.Fatal(err)
	}
	suites := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		suites++
		t.Run(e.Name(), func(t *testing.T) {
			files := readTree(t, filepath.Join(conformanceDir, e.Name(), "snapshot"))
			s, err := serializer.Serialize(draftOf(t, files), stampOf(t, files))
			if err != nil {
				t.Fatal(err)
			}
			if len(s.StandIns) > 0 {
				t.Errorf("stood in for %v", s.StandIns)
			}
			if got, want := slices.Sorted(maps.Keys(s.Files)), slices.Sorted(maps.Keys(files)); !slices.Equal(got, want) {
				t.Fatalf("wrote %v, want %v", got, want)
			}
			for p, want := range files {
				if got := s.Files[p]; !bytes.Equal(got, want) {
					t.Errorf("%s is\n%s\nwant\n%s", p, got, want)
				}
			}
		})
	}
	if suites == 0 {
		t.Fatal("the conformance suite has no suite")
	}
}

// goldenDir holds the golden snapshot, the files that goldenDraft serializes
// to, which go test -run TestGolden -update rewrites.
var goldenDir = filepath.Join("testdata", "golden")

// TestGolden checks that the golden draft, of what §6.2's rules are about,
// serializes to the golden snapshot's bytes, which the repository holds, on
// every platform that CI runs the tests on (PRD R4): HTML that is not
// escaped, the control characters and U+2028 and U+2029 that are, keys in
// the order of their code points, numbers in the shortest form that reads
// back, as JSON.stringify writes them, 1e+21 and 1e-7 among them, an int
// written 3.0 as 3, -0 as 0, positions to hundredths, arrays in their
// author's order where it means something and by ID where it does not,
// optional fields left out, a branch's outputs each of the kind of its
// place, component types of no component left out, and a component's value
// that it lacks as its definition's default. The snapshot validates against
// the published JSON Schemas and loads in the reference runtime.
//
// Verifies: FR-SNAP-03/AC1
func TestGolden(t *testing.T) {
	s, err := serializer.Serialize(goldenDraft(t), goldenStamp)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.StandIns) > 0 {
		t.Errorf("stood in for %v", s.StandIns)
	}
	newSchemas(t).check(t, s.Files)
	if *update {
		if err := os.RemoveAll(goldenDir); err != nil {
			t.Fatal(err)
		}
		for p, data := range s.Files {
			full := filepath.Join(goldenDir, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	want := readTree(t, goldenDir)
	if got, want := slices.Sorted(maps.Keys(s.Files)), slices.Sorted(maps.Keys(want)); !slices.Equal(got, want) {
		t.Fatalf("wrote %v, want %v; run go test ./internal/serializer -run TestGolden -update", got, want)
	}
	for p, data := range want {
		if !bytes.Equal(s.Files[p], data) {
			t.Errorf("%s is not the golden file; run go test ./internal/serializer -run TestGolden -update, and "+
				"check the change:\n%s", p, s.Files[p])
		}
	}
}

// TestStampInManifestAlone checks that what the stamp says, what wrote the
// snapshot, which release it is or that it is a draft, and when, is in the
// manifest's generator and snapshot blocks alone: the content files are the
// same bytes whatever the stamp, and so is the content hash, and the
// manifests differ only in those two blocks.
//
// Verifies: FR-SNAP-03/AC3
func TestStampInManifestAlone(t *testing.T) {
	d := goldenDraft(t)
	stamps := []serializer.Stamp{
		goldenStamp,
		{Generator: published.GeneratorInfo{Name: "storycreep", Version: "v9.9.9"}, At: time.Date(2031, 1, 2, 3, 4, 5,
			678, time.FixedZone("far", 5*3600))},
		{Generator: serializer.Generator(), Release: &serializer.Release{ID: "snp_01HQ9A7TBE5N2WQX8K4YJ3MDCR",
			Version: "2.0.0", Note: "Another"}, At: time.Now()},
	}
	var first *serializer.Snapshot
	for i, st := range stamps {
		s, err := serializer.Serialize(d, st)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = s
			continue
		}
		if s.ContentHash != first.ContentHash {
			t.Errorf("stamp %d: the content hash is %s, want %s", i, s.ContentHash, first.ContentHash)
		}
		for p, data := range first.Files {
			if p != published.ManifestPath && !bytes.Equal(s.Files[p], data) {
				t.Errorf("stamp %d: %s differs", i, p)
			}
		}
		if a, b := withoutStamp(t, first.Files[published.ManifestPath]), withoutStamp(t,
			s.Files[published.ManifestPath]); a != b {
			t.Errorf("stamp %d: the manifest differs beyond its generator and snapshot:\n%s\nwant\n%s", i, b, a)
		}
	}
}

// withoutStamp returns a manifest's JSON without its generator and its
// snapshot.
func withoutStamp(t *testing.T, data []byte) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "generator")
	delete(m, "snapshot")
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestContentHash checks that the content hash is the hash of the lines of
// the content files' paths and hashes, sorted by their paths, each path, a
// space, and its hash, and a line feed, as the manifest lists them, and that
// each hash of the manifest is its file's.
func TestContentHash(t *testing.T) {
	s, err := serializer.Serialize(goldenDraft(t), goldenStamp)
	if err != nil {
		t.Fatal(err)
	}
	var lines strings.Builder
	paths := slices.Sorted(maps.Keys(s.Manifest.Files))
	for _, p := range paths {
		if got := hash(s.Files[p]); got != s.Manifest.Files[p] {
			t.Errorf("the manifest's hash of %s is %s, want %s", p, s.Manifest.Files[p], got)
		}
		lines.WriteString(p + " " + string(s.Manifest.Files[p]) + "\n")
	}
	if want := hash([]byte(lines.String())); s.ContentHash != want {
		t.Errorf("the content hash is %s, want %s", s.ContentHash, want)
	}
	if len(paths) != len(s.Files)-1 || s.Manifest.Files[published.ManifestPath] != "" {
		t.Errorf("the manifest lists %v, want every file but itself", paths)
	}
}

// TestStamp checks that a stamp that would write a manifest that the schema
// refuses is refused, and so is one without a moment, which would write a
// wrong one.
func TestStamp(t *testing.T) {
	d := goldenDraft(t)
	for name, st := range map[string]serializer.Stamp{
		"no generator": {At: goldenMoment},
		"no version": {Generator: serializer.Generator(), At: goldenMoment, Release: &serializer.Release{
			ID: "snp_01HQ9A7TBE5N2WQX8K4YJ3MDCR"}},
		"no snapshot ID": {Generator: serializer.Generator(), At: goldenMoment, Release: &serializer.Release{
			ID: "snp_x", Version: "1.0.0"}},
		"no moment":            {Generator: serializer.Generator()},
		"no moment, a release": {Generator: serializer.Generator(), Release: goldenStamp.Release},
	} {
		if _, err := serializer.Serialize(d, st); err == nil {
			t.Errorf("%s: written", name)
		}
	}
}

// TestLayout checks the layout of a file (§6.2): keys sorted by their code
// points, two spaces of indentation, HTML, a slash, and characters beyond
// ASCII not escaped, the control characters, ", \, U+2028, and U+2029
// escaped as encoding/json escapes them, numbers as they are written, and a
// line feed at the end; and that it refuses null, and what is not one JSON
// value.
func TestLayout(t *testing.T) {
	in := `{"z":1,"é":"<a href=\"/x\">&</a>","a":["\u0008\u000c\n\r\t\u0001\u001f\u007f","\u2028\u2029\\"],` +
		`"B":{"y":1e+21,"x":1e-7},"ä":[],"_":{}}`
	want := "{\n" +
		`  "B": {` + "\n" +
		`    "x": 1e-7,` + "\n" +
		`    "y": 1e+21` + "\n" +
		`  },` + "\n" +
		`  "_": {},` + "\n" +
		`  "a": [` + "\n" +
		`    "\b\f\n\r\t\u0001\u001f` + "\u007f" + `",` + "\n" +
		`    "\u2028\u2029\\"` + "\n" +
		`  ],` + "\n" +
		`  "z": 1,` + "\n" +
		`  "ä": [],` + "\n" +
		`  "é": "<a href=\"/x\">&</a>"` + "\n" +
		"}\n"
	got, err := serializer.Layout([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("Layout = \n%s\nwant\n%s", got, want)
	}
	for _, bad := range []string{`{"a":null}`, `{} {}`, `{`, ""} {
		if _, err := serializer.Layout([]byte(bad)); err == nil {
			t.Errorf("Layout(%q) is laid out", bad)
		}
	}
}

// TestStandIns checks that a draft that holds what no snapshot may, of
// every kind of stand-in, is written as play mode's projection writes it,
// with what it stood in for listed in its order, a condition that does not
// compile, and an output without one, as false, a script that does not
// compile left out, and the rest left out, which the published JSON
// Schemas and the reference runtime take of a draft; and that a release of
// the same draft is refused with the same list, so that no release holds an
// expression that did not compile.
//
// Verifies: FR-EXPR-03/AC1
func TestStandIns(t *testing.T) {
	d, want := standInDraft(t)
	s, err := serializer.Serialize(d, goldenDraftStamp)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.StandIns, want) {
		t.Errorf("stood in for\n%v\nwant\n%v", s.StandIns, want)
	}
	newSchemas(t).check(t, s.Files)
	checkStandInFiles(t, s.Files)
	_, err = serializer.Serialize(d, goldenStamp)
	var unpublishable *serializer.UnpublishableError
	if !errors.As(err, &unpublishable) {
		t.Fatalf("a release of the draft is %v, want an *UnpublishableError", err)
	}
	if !slices.Equal(unpublishable.StandIns, want) {
		t.Errorf("the release's refusal lists\n%v\nwant\n%v", unpublishable.StandIns, want)
	}
	if msg := (&serializer.UnpublishableError{}).Error(); msg == "" {
		t.Error("an UnpublishableError that lists nothing has no message")
	}
}

// TestJumperWithoutTarget checks that a jumper without a target, which a
// draft's may lack, is written without one in a draft's files, which the
// published JSON Schemas and the reference runtime take of a draft, and
// stands in for nothing; and that a release of it is refused, listing it, as
// the reference runtime refuses a release of it, and as the validation
// reports it (jumper_broken, of the reason none).
func TestJumperWithoutTarget(t *testing.T) {
	b, j, end := mn(ids.Board, "B1"), mn(ids.Node, "N1"), mn(ids.Node, "N2")
	d := &serializer.Draft{
		Project: serializer.Project{ID: mn(ids.Project, "J1"), Name: "Jumper", Slug: "jumper"},
		Boards:  []serializer.Board{{ID: b, Slug: "one", Name: "One", EntryNode: end}},
		Nodes: []serializer.Node{
			{ID: j, Board: b, Type: published.NodeJumper},
			{ID: end, Board: b, Type: published.NodeElement, Kind: "narration", IsEnding: true},
		},
	}
	s, err := serializer.Serialize(d, goldenDraftStamp)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.StandIns) > 0 {
		t.Errorf("the draft stood in for %v", s.StandIns)
	}
	newSchemas(t).check(t, s.Files)
	_, err = serializer.Serialize(d, goldenStamp)
	var unpublishable *serializer.UnpublishableError
	if !errors.As(err, &unpublishable) {
		t.Fatalf("a release of the draft is %v, want an *UnpublishableError", err)
	}
	want := []serializer.StandIn{{Kind: serializer.StandInTarget, Board: b, Node: j, Entity: j,
		Reason: serializer.ReasonNone}}
	if !slices.Equal(unpublishable.StandIns, want) {
		t.Errorf("the release's refusal lists\n%v\nwant\n%v", unpublishable.StandIns, want)
	}
}

// hash returns the hash of data, as the manifest holds it.
func hash(data []byte) published.Hash {
	return published.Hash(hashHex(data))
}
