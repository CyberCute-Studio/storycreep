package authoring

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
)

// TestArchiveDirectoryGuard checks that a central directory of more headers
// than an import reads is refused as it is read, and not once the zip reader
// has built a File of each of them.
//
// Verifies: NFR-SEC-13
func TestArchiveDirectoryGuard(t *testing.T) {
	t.Parallel()
	header := make([]byte, 46)
	binary.LittleEndian.PutUint32(header, 0x02014b50)
	count := maxDirectoryBytes/len(header) + 1
	directory := bytes.Repeat(header, count)
	end := make([]byte, 22)
	binary.LittleEndian.PutUint32(end, 0x06054b50)
	binary.LittleEndian.PutUint16(end[8:], uint16(count))
	binary.LittleEndian.PutUint16(end[10:], uint16(count))
	binary.LittleEndian.PutUint32(end[12:], uint32(len(directory)))
	data := append(directory, end...)
	_, err := openArchive(bytes.NewReader(data), int64(len(data)), 1<<30)
	wantRefused(t, err, "entries")
	if err == nil || !strings.Contains(err.Error(), "central directory") {
		t.Errorf("the error is %v, want the directory's refusal rather than the count of its members", err)
	}
}

// TestJSONValuesGuard checks how a file's values are counted, that what its
// strings spell is not, and that a small file of too many is refused before it
// is parsed.
//
// Verifies: NFR-SEC-13
func TestJSONValuesGuard(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int{
		`1`:                  1,
		`[]`:                 2,
		`[1,2,3]`:            4,
		`{"a":1,"b":[{}]}`:   5,
		`"[,{,]"`:            1,
		`["a\",b",{"c,":1}]`: 4,
	} {
		if got := jsonValues([]byte(in)); got != want {
			t.Errorf("jsonValues(%s) = %d, want %d", in, got, want)
		}
	}
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, err := w.CreateHeader(&zip.FileHeader{Name: "a.json", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("[" + strings.Repeat("0,", maxJSONValues) + "0]")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	a, err := openArchive(bytes.NewReader(data), int64(len(data)), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.readJSON("a.json")
	wantRefused(t, err, "json_values")
}

// TestJSONTotalHoldsBeforeARead checks that a member that would take the JSON
// files held at once past maxJSONTotal is refused by the size that its header
// says, before any of it is read, among a release's files and an archive's
// sections, so that the guard bounds what is in memory rather than report it.
//
// Verifies: NFR-SEC-13
func TestJSONTotalHoldsBeforeARead(t *testing.T) {
	t.Parallel()
	sized := func(n uint64) *zip.File { return &zip.File{FileHeader: zip.FileHeader{UncompressedSize64: n}} }
	a := &archive{files: map[string]*zip.File{"a.json": sized(maxJSONMember),
		"b.json": sized(maxJSONTotal - maxJSONMember), "c.json": sized(1), "wraps.json": sized(math.MaxUint64)}}
	var held int64
	for _, name := range []string{"a.json", "b.json", "absent.json"} {
		if err := a.reserve(&held, name, "its files"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if held != maxJSONTotal {
		t.Errorf("%d bytes are held, want %d", held, maxJSONTotal)
	}
	for _, name := range []string{"c.json", "wraps.json"} {
		wantRefused(t, a.reserve(&held, name, "its files"), "size")
	}
	if held != maxJSONTotal {
		t.Errorf("a refused member changed what is held to %d bytes", held)
	}

	// Each member below says more than it holds, and its data is not there to
	// be read: a read would be refused for that rather than by the total.
	archiveOf := func(manifest string, name string, size uint64) *archive {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		if manifest != "" {
			f, err := w.Create(ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write([]byte(manifest)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := w.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Deflate, UncompressedSize64: size,
			CompressedSize64: size / maxCompressionRatio}); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		data := b.Bytes()
		out, err := openArchive(bytes.NewReader(data), int64(len(data)), 1<<30)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	_, err := releaseFiles(archiveOf("", "releases/one/manifest.json", maxJSONTotal+1), "releases/one/")
	wantRefused(t, err, "size")
	if err == nil || !strings.Contains(err.Error(), "the files of") {
		t.Errorf("the error is %v, want the refusal of the release's files by their total", err)
	}
	entry := ManifestSection{Name: "project", Path: "draft/project.json", Items: 1, SHA256: sha256Hex([]byte("{}"))}
	_, err = readArchived(archiveOf(manifestOf(t, entry), entry.Path, maxJSONTotal))
	wantRefused(t, err, "size")
	if err == nil || !strings.Contains(err.Error(), "its sections") {
		t.Errorf("the error is %v, want the refusal of the sections by their total", err)
	}
}

// TestCheckReleaseNamesHoldsItsProject checks that a release sealed for another
// project is refused.
//
// Verifies: FR-DATA-01/AC11
func TestCheckReleaseNamesHoldsItsProject(t *testing.T) {
	t.Parallel()
	own, other := ids.Encode(ids.Project, ids.New()), ids.Encode(ids.Project, ids.New())
	files := schemaversion.Files{published.ManifestPath: []byte(`{"project":{"id":"` + other +
		`"},"snapshot":{"version":"1.0.0"}}`)}
	if err := checkReleaseNames(files, own, "", "1.0.0"); err == nil {
		t.Error("a release of another project is taken")
	}
	if err := checkReleaseNames(files, other, "", "1.0.0"); err != nil {
		t.Errorf("a release of its own project: %v", err)
	}
}

// TestFreshIDsKeepTheirOrder checks that a remap keeps the order of the IDs,
// which a release's files hold their arrays in.
//
// Verifies: FR-DATA-01/AC3
func TestFreshIDsKeepTheirOrder(t *testing.T) {
	t.Parallel()
	list := make([]ids.UUID, 5000)
	for i := range list {
		list[i] = ids.New()
	}
	compare := func(a, b ids.UUID) int { return bytes.Compare(a[:], b[:]) }
	slices.SortFunc(list, compare)
	table := freshIDs(list)
	for i := 1; i < len(list); i++ {
		if compare(table[list[i-1]], table[list[i]]) >= 0 {
			t.Fatalf("the fresh IDs of %d and %d are out of order", i-1, i)
		}
	}
}

// TestRemapHoldsWhatOnlyAReleaseHolds checks that an ID that a release's files
// hold and the draft no longer does takes a fresh ID too, or it would sort
// apart from the fresh IDs around it.
//
// Verifies: FR-DATA-01/AC3
func TestRemapHoldsWhatOnlyAReleaseHolds(t *testing.T) {
	t.Parallel()
	draft, gone := ids.New(), ids.New()
	file := `{"nodes":[{"id":"` + ids.Encode(ids.Node, draft) + `"},{"id":"` + ids.Encode(ids.Node, gone) + `"}]}`
	data := zipped(t, [2]string{"releases/one/boards/a.json", file})
	a, err := openArchive(bytes.NewReader(data), int64(len(data)), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	files, err := releaseFiles(a, "releases/one/")
	if err != nil {
		t.Fatal(err)
	}
	spelled := map[ids.UUID]bool{}
	if err := addFileIDs(files, spelled, maxRemapIDs); err != nil {
		t.Fatal(err)
	}
	all, err := allIDs([]ids.UUID{draft}, spelled)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(all, draft) || !slices.Contains(all, gone) || len(all) != 2 {
		t.Errorf("the IDs to remap are %v, want %v and %v", all, draft, gone)
	}
	if !slices.IsSortedFunc(all, func(a, b ids.UUID) int { return bytes.Compare(a[:], b[:]) }) {
		t.Errorf("the IDs to remap are not sorted: %v", all)
	}
}

// TestArchiveBudgetHoldsAgainstAWrap checks that a size near 2^64, which would
// wrap the sum of the members' sizes back to a small one, is past the budget.
//
// Verifies: NFR-SEC-13
func TestArchiveBudgetHoldsAgainstAWrap(t *testing.T) {
	t.Parallel()
	const first = 1 << 20
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, h := range []*zip.FileHeader{
		{Name: "a.bin", Method: zip.Store, UncompressedSize64: first, CompressedSize64: first},
		{Name: "b.bin", Method: zip.Store, UncompressedSize64: math.MaxUint64 - first + 2, CompressedSize64: 1 << 62},
	} {
		if _, err := w.CreateRaw(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	_, err := openArchive(bytes.NewReader(data), int64(len(data)), 1<<30)
	wantRefused(t, err, "size")
}

// TestRemapRefusesTooManyIDs checks that what a remap holds of the IDs that a
// release's files spell is bounded, by the IDs and by how often a file spells
// them.
//
// Verifies: NFR-SEC-13
func TestRemapRefusesTooManyIDs(t *testing.T) {
	t.Parallel()
	one, two := ids.Encode(ids.Node, ids.New()), ids.Encode(ids.Node, ids.New())
	files := schemaversion.Files{"boards/a.json": []byte(`"` + one + " " + two + `"`)}
	set := map[ids.UUID]bool{}
	if err := addFileIDs(files, set, 2); err != nil || len(set) != 2 {
		t.Errorf("two IDs of a limit of two: %v, %v", set, err)
	}
	wantRefused(t, addFileIDs(files, map[ids.UUID]bool{}, 1), "ids")
	repeated := schemaversion.Files{"boards/a.json": []byte(`"` + strings.Repeat(one+" ", 3) + `"`)}
	wantRefused(t, addFileIDs(repeated, map[ids.UUID]bool{}, 2), "ids")
}

// TestReportIsReadAsTheAPIReadsIt checks that a muted_by in any case of its key,
// which the API's decoding takes, is mapped, and that a report that the API
// could not decode is refused.
//
// Verifies: FR-DATA-01/AC1
func TestReportIsReadAsTheAPIReadsIt(t *testing.T) {
	t.Parallel()
	named, mapped := ids.New(), ids.New()
	report := []byte(`{"findings":[{"mute":{"MUTED_BY":"` + ids.Encode(ids.User, named) + `"}}]}`)
	seen := map[ids.UUID]bool{}
	reportUsersIn(report, seen)
	if !seen[named] {
		t.Errorf("the people of the report are %v", seen)
	}
	got, err := rewriteReport(report, map[ids.UUID]ids.UUID{named: mapped})
	if err != nil {
		t.Fatal(err)
	}
	var s validation.Stored
	if err := json.Unmarshal(got, &s); err != nil {
		t.Fatal(err)
	}
	if by := s.Findings[0].Mute.MutedBy; by != ids.Encode(ids.User, mapped) {
		t.Errorf("the mute names %s", by)
	}
	for _, bad := range []string{`null`, `[]`, `{"findings":"x"}`, `{"findings":[{"mute":{"muted_by":1}}]}`} {
		if _, err := storedReport([]byte(bad)); err == nil {
			t.Errorf("the report %s was read", bad)
		}
	}
}

// wantCheck fails the test unless err is a refusal at the check.
func wantCheck(t *testing.T, err error, check string) {
	t.Helper()
	var refused *ImportError
	if !errors.As(err, &refused) || refused.Check != check {
		t.Errorf("the error is %v, want a refusal at %s", err, check)
	}
}

// TestFoldersInOrderClimbsWithoutRecursion checks a chain far deeper than a
// project can hold, each folder before its parent in the archive, which is
// put in order for the commands to refuse rather than overflow the stack, and
// that a cycle and a missing parent are still refused.
//
// Verifies: NFR-SEC-13
func TestFoldersInOrderClimbsWithoutRecursion(t *testing.T) {
	t.Parallel()
	const n = 200_000
	list := make([]Folder, n)
	for i := range list {
		list[i].ID = "f" + strconv.Itoa(i)
		if i+1 < n {
			list[i].ParentID = "f" + strconv.Itoa(i+1)
		}
	}
	out, err := foldersInOrder(list)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != n || out[0].ID != list[n-1].ID || out[n-1].ID != list[0].ID {
		t.Fatalf("%d folders, from %s to %s", len(out), out[0].ID, out[len(out)-1].ID)
	}
	_, err = foldersInOrder([]Folder{{ID: "a", ParentID: "b"}, {ID: "b", ParentID: "a"}})
	wantCheck(t, err, "cycle")
	_, err = foldersInOrder([]Folder{{ID: "a", ParentID: "gone"}})
	wantCheck(t, err, "parent")
}

// TestStoredValuesAreReadAsTheirReadersReadThem checks that a value that an
// import writes without a command is refused if its reader would refuse it,
// and is written as what was checked, whichever spelling of a key came last.
//
// Verifies: FR-DATA-01/AC1
func TestStoredValuesAreReadAsTheirReadersReadThem(t *testing.T) {
	t.Parallel()
	if _, err := storedReport([]byte(`{"findings":[{"args":{"n":1e999}}]}`)); err == nil {
		t.Error("a report with a number past a float64 was read")
	}
	foreign := ids.Encode(ids.User, ids.New())
	got, err := rewriteReport([]byte(`{"findings":[{"mute":{"reason":"r","muted_by":"`+foreign+`"}}],"FINDINGS":[]}`),
		map[ids.UUID]ids.UUID{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), foreign) || strings.Contains(string(got), "FINDINGS") {
		t.Errorf("the report is written as %s", got)
	}
	if _, err := storedWarnings([]byte(`{"code":"x"}`)); err == nil {
		t.Error("acknowledged warnings that are no list were read")
	}
	if out, err := storedWarnings([]byte(`null`)); err != nil || string(out) != `[]` {
		t.Errorf("no warnings are %s, %v", out, err)
	}
	for _, bad := range []string{``, `null`, `[]`, `{"boards":"x"}`, `{"boards":1e30}`} {
		if _, err := storedCounts([]byte(bad)); err == nil {
			t.Errorf("the counts %q were read", bad)
		}
	}
}

// TestCheckTimesHoldsWhatTheAPIWrites checks that a time that is within JSON's
// years as the archive spells it, and outside them in UTC, is refused.
//
// Verifies: FR-DATA-01/AC1
func TestCheckTimesHoldsWhatTheAPIWrites(t *testing.T) {
	t.Parallel()
	late := time.Date(9999, 12, 31, 23, 59, 59, 0, time.FixedZone("", -(23*3600+59*60)))
	early := time.Date(0, 1, 1, 0, 0, 0, 0, time.FixedZone("", 23*3600+59*60))
	fine := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{late, early} {
		wantCheck(t, (&archived{releases: []Release{{PublishedAt: at, BuiltAt: fine}}}).checkTimes(), "published_at")
		wantCheck(t, (&archived{releases: []Release{{PublishedAt: fine, BuiltAt: at}}}).checkTimes(), "built_at")
		wantCheck(t, (&archived{comments: []Comment{{CreatedAt: fine, ResolvedAt: &at}}}).checkTimes(), "time")
		wantCheck(t, (&archived{playStates: []PlayState{{CreatedAt: at}}}).checkTimes(), "time")
	}
	c := &archived{releases: []Release{{PublishedAt: fine, BuiltAt: fine}}, comments: []Comment{{CreatedAt: fine}},
		playStates: []PlayState{{CreatedAt: fine}}}
	if err := c.checkTimes(); err != nil {
		t.Error(err)
	}
}

// TestPeopleAreOnlyWhomADocumentNames checks that an ID that a comment's text
// spells is no person, and stays as its author wrote it, and that a mention
// and a report's muted_by are mapped.
//
// Verifies: FR-DATA-01/AC1
func TestPeopleAreOnlyWhomADocumentNames(t *testing.T) {
	t.Parallel()
	named, spelled, mapped := ids.New(), ids.New(), ids.New()
	namedID, spelledID := ids.Encode(ids.User, named), ids.Encode(ids.User, spelled)
	body := []byte(`{"type":"doc","content":[{"type":"paragraph","content":[` +
		`{"type":"memberMention","attrs":{"id":"` + namedID + `"}},` +
		`{"type":"text","text":" ask ` + spelledID + `"}]}]}`)
	seen := map[ids.UUID]bool{}
	mentionsIn(body, seen)
	if !seen[named] || seen[spelled] {
		t.Errorf("the people of the body are %v, want %v alone", seen, named)
	}
	out, err := rewriteMentions(body, map[ids.UUID]ids.UUID{named: mapped})
	if err != nil {
		t.Fatal(err)
	}
	if text := string(out); strings.Contains(text, namedID) || !strings.Contains(text, ids.Encode(ids.User, mapped)) ||
		!strings.Contains(text, spelledID) {
		t.Errorf("the body is %s", text)
	}

	report := []byte(`{"findings":[{"mute":{"muted_by":"` + namedID + `"}},{"mute":{"muted_by":"` + spelledID +
		`"}},{"note":"` + spelledID + `"}]}`)
	seen = map[ids.UUID]bool{}
	reportUsersIn(report, seen)
	if !seen[named] || !seen[spelled] || len(seen) != 2 {
		t.Errorf("the people of the report are %v", seen)
	}
	got, err := rewriteReport(report, map[ids.UUID]ids.UUID{named: mapped})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Findings []struct {
			Mute map[string]string `json:"mute"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Findings[0].Mute["muted_by"] != ids.Encode(ids.User, mapped) {
		t.Errorf("the mapped mute is %v", doc.Findings[0].Mute)
	}
	if _, ok := doc.Findings[1].Mute["muted_by"]; ok {
		t.Errorf("a muted_by that the table does not hold stays: %v", doc.Findings[1].Mute)
	}
}

// TestFailureIsRecordable checks that a failure is held to what its row and a
// reader of it take, whatever the archive that it repeats spells.
//
// Verifies: NFR-SEC-13
func TestFailureIsRecordable(t *testing.T) {
	t.Parallel()
	got := (&ImportError{Code: FailContentInvalid, Section: "a\x00b", Version: "1\x1b[2J", Check: "c",
		Detail: strings.Repeat("x", 3*maxFailureText) + "\x00"}).recorded()
	for _, text := range []string{got.Section, got.Version, got.Check, got.Detail} {
		if strings.IndexFunc(text, input.IsControl) >= 0 || len(text) > maxFailureText || !utf8.ValidString(text) {
			t.Errorf("the text %q is not one that a failure keeps", text)
		}
	}
	if got.Code != FailContentInvalid || got.Check != "c" {
		t.Errorf("the failure is %+v", got)
	}
}

// TestCleanFileName checks that what a file's name holds besides its name
// costs nothing but those characters.
func TestCleanFileName(t *testing.T) {
	t.Parallel()
	lrm, nel := string(rune(0x200e)), string(rune(0x85))
	for in, want := range map[string]string{
		"harbour.zip":               "harbour.zip",
		"  harbour.zip ":            "harbour.zip",
		"harbour" + lrm + ".zip":    "harbour.zip",
		"harbour.zip" + nel:         "harbour.zip",
		"har\x00bour.zip":           "harbour.zip",
		lrm + " harbour.zip " + lrm: "harbour.zip",
		"a b.zip":                   "a b.zip",
	} {
		if got := cleanFileName(in); got != want {
			t.Errorf("cleanFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestVerifyReleaseRefusesAnAsset checks that a release holding an asset's
// file, which no build writes and no check of an asset reads, is refused and
// named.
func TestVerifyReleaseRefusesAnAsset(t *testing.T) {
	t.Parallel()
	err := verifyRelease("prj", "", "1.0.0", 0, "", schemaversion.Files{
		published.ManifestPath:                  []byte(`{}`),
		"assets/0123456789ABCDEFGHJKMNPQRS.svg": []byte("<svg/>"),
	})
	var refused *ImportError
	if !errors.As(err, &refused) || refused.Code != FailReleaseInvalid || refused.Version != "1.0.0" ||
		refused.Check != "asset_file" {
		t.Errorf("the failure is %v", err)
	}
}

// TestIDViolation checks that only a row's ID taken since the collision check
// sends the import round again with fresh IDs.
func TestIDViolation(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		code, constraint string
		want             bool
	}{
		{uniqueViolation, "comment_pkey", true},
		{uniqueViolation, "snapshot_version", false},
		{uniqueViolation, "validation_mute_owner", false},
		{"23503", "comment_pkey", false},
	} {
		if got := idViolation(&pgconn.PgError{Code: c.code, ConstraintName: c.constraint}); got != c.want {
			t.Errorf("idViolation(%s, %s) = %v", c.code, c.constraint, got)
		}
	}
}

// TestRemapKeepsAnAssetsPath checks that an asset's file is still found after a
// remap, in an archive whose path for it spells the asset's ID.
//
// Verifies: FR-DATA-01/AC8
func TestRemapKeepsAnAssetsPath(t *testing.T) {
	t.Parallel()
	project, asset := ids.New(), ids.New()
	path := "assets/" + ids.Encode(ids.Asset, asset) + ".png"
	raw, err := json.Marshal(Items[Asset]{Items: []Asset{{ID: ids.Encode(ids.Asset, asset), Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	c := &archived{manifest: Manifest{Project: ManifestProject{ID: ids.Encode(ids.Project, project)}},
		assets: []Asset{{ID: ids.Encode(ids.Asset, asset), Path: path}}, raw: map[string][]byte{"assets": raw}}
	if err := c.remap(freshIDs([]ids.UUID{project, asset})); err != nil {
		t.Fatal(err)
	}
	if c.assets[0].Path != path || c.assets[0].ID == ids.Encode(ids.Asset, asset) {
		t.Errorf("the asset is now %+v, and its file is at %s", c.assets[0], path)
	}
}
