package authoring

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
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
	c := &archived{releasePaths: []string{"releases/one/"}}
	all, err := c.remapIDs(a, []ids.UUID{draft})
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
