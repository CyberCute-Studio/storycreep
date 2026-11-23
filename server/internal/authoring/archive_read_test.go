package authoring

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
)

// zipped returns a zip of the members, each deflated, in their order.
func zipped(t *testing.T, members ...[2]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, m := range members {
		f, err := w.Create(m[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(m[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// wantRefused checks that err is an archive's refusal at the check.
func wantRefused(t *testing.T, err error, check string) {
	t.Helper()
	var refused *ImportError
	if !errors.As(err, &refused) || refused.Code != FailArchiveInvalid || refused.Check != check {
		t.Errorf("the error is %v, want archive_invalid at %s", err, check)
	}
}

// TestArchivePathGuards checks that a member whose path could reach outside
// the archive is refused before anything is read of it.
//
// Verifies: NFR-SEC-13
func TestArchivePathGuards(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"../escape", "a/../../escape", "/etc/passwd", `a\b`, "C:/x", "a//b", "./a", "a/./b",
		"a\x00b", "a\nb", strings.Repeat("a", maxMemberName+1)} {
		data := zipped(t, [2]string{name, "x"})
		_, err := openArchive(bytes.NewReader(data), int64(len(data)), 1<<30)
		wantRefused(t, err, "path")
	}
	data := zipped(t, [2]string{"draft/nodes.json", "{}"}, [2]string{"assets/ok.png", "x"})
	if _, err := openArchive(bytes.NewReader(data), int64(len(data)), 1<<30); err != nil {
		t.Errorf("a plain archive: %v", err)
	}
}

// TestArchiveMemberGuards checks the guards on what a member is: one path
// twice, a symbolic link, an encrypted member, and a compression method that
// an import does not read.
//
// Verifies: NFR-SEC-13
func TestArchiveMemberGuards(t *testing.T) {
	t.Parallel()
	dup := zipped(t, [2]string{"a.json", "1"}, [2]string{"a.json", "2"})
	_, err := openArchive(bytes.NewReader(dup), int64(len(dup)), 1<<30)
	wantRefused(t, err, "duplicate")

	build := func(edit func(h *zip.FileHeader)) []byte {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		h := &zip.FileHeader{Name: "a.json", Method: zip.Store}
		edit(h)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	link := build(func(h *zip.FileHeader) { h.SetMode(fs.ModeSymlink | 0o777) })
	_, err = openArchive(bytes.NewReader(link), int64(len(link)), 1<<30)
	wantRefused(t, err, "file_type")

	encrypted := build(func(h *zip.FileHeader) {})
	// The general-purpose flags of the central directory's only entry.
	at := bytes.LastIndex(encrypted, []byte("PK\x01\x02"))
	encrypted[at+8] |= 0x1
	_, err = openArchive(bytes.NewReader(encrypted), int64(len(encrypted)), 1<<30)
	wantRefused(t, err, "encrypted")

	method := build(func(h *zip.FileHeader) {})
	at = bytes.LastIndex(method, []byte("PK\x01\x02"))
	method[at+10] = 12 // bzip2
	_, err = openArchive(bytes.NewReader(method), int64(len(method)), 1<<30)
	wantRefused(t, err, "method")
}

// TestArchiveBombGuards checks the guards against a zip bomb: a member that
// expands too many times over, and members that together expand past what the
// instance's import limit scales to.
//
// Verifies: NFR-SEC-13
func TestArchiveBombGuards(t *testing.T) {
	t.Parallel()
	bomb := zipped(t, [2]string{"a.json", strings.Repeat("0", 8<<20)})
	_, err := openArchive(bytes.NewReader(bomb), int64(len(bomb)), 1<<30)
	wantRefused(t, err, "ratio")

	// Not compressible by much, so that the ratio lets each through, and
	// together past the budget of a small limit: minExpansion is its floor.
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for i := range 3 {
		h := &zip.FileHeader{Name: fmt.Sprintf("m%d.bin", i), Method: zip.Store}
		h.UncompressedSize64 = uint64(minExpansion)/2 + 1
		h.CompressedSize64 = h.UncompressedSize64
		h.CRC32 = 0
		if _, err := w.CreateRaw(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	_, err = openArchive(bytes.NewReader(data), int64(len(data)), 1)
	if err == nil {
		t.Errorf("members that expand past the budget were taken")
	}

	// A small file of repeated keys compresses far past the ratio, and is no
	// bomb.
	small := zipped(t, [2]string{"a.json", strings.Repeat(`{"k":1}`, 1000)})
	if _, err := openArchive(bytes.NewReader(small), int64(len(small)), 1<<30); err != nil {
		t.Errorf("a small file that compresses well: %v", err)
	}
}

// TestArchiveEntriesGuard checks the limit on the number of members, which it
// lowers for the test by reading a central directory that claims more.
//
// Verifies: NFR-SEC-13
func TestArchiveEntriesGuard(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for i := range maxArchiveEntries + 1 {
		h := &zip.FileHeader{Name: fmt.Sprintf("%d", i), Method: zip.Store}
		if _, err := w.CreateRaw(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	_, err := openArchive(bytes.NewReader(data), int64(len(data)), 1<<30)
	wantRefused(t, err, "entries")
}

// TestArchiveReadChecksSizes checks that a member cannot hold more or less
// than its header says, and that a damaged member is refused.
//
// Verifies: NFR-SEC-13
func TestArchiveReadChecksSizes(t *testing.T) {
	t.Parallel()
	data := zipped(t, [2]string{"a.json", strings.Repeat("abcdefgh", 100)})
	a, err := openArchive(bytes.NewReader(data), int64(len(data)), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := a.read("a.json", 1<<20); err != nil || len(got) != 800 {
		t.Fatalf("read %d bytes, %v", len(got), err)
	}
	_, err = a.read("a.json", 10)
	wantRefused(t, err, "size")
	_, err = a.read("b.json", 10)
	wantRefused(t, err, "missing")

	// A byte of the deflated data that changes makes its CRC-32 wrong, or its
	// stream invalid.
	damaged := bytes.Clone(data)
	at := bytes.Index(damaged, []byte("a.json")) + len("a.json") + 2
	damaged[at] ^= 0xff
	a, err = openArchive(bytes.NewReader(damaged), int64(len(damaged)), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.read("a.json", 1<<20)
	wantRefused(t, err, "zip")
}

// memoryStore is a blob store of what a chunk reader reads.
type memoryStore struct {
	blob.Store
	blobs map[string][]byte
}

func (m memoryStore) Open(_ context.Context, key string) (io.ReadCloser, blob.Info, error) {
	data, ok := m.blobs[key]
	if !ok {
		return nil, blob.Info{}, blob.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), blob.Info{Key: key, Size: int64(len(data))}, nil
}

// TestChunkReader checks that an upload's chunks read as one file, whatever
// the offset, across their edges, and to its end.
//
// Verifies: FR-DATA-01/AC4
func TestChunkReader(t *testing.T) {
	t.Parallel()
	want := make([]byte, 2500)
	for i := range want {
		want[i] = byte(i * 7)
	}
	store := memoryStore{blobs: map[string][]byte{"c0": want[:1000], "c1": want[1000:2000], "c2": want[2000:]}}
	r := newChunkReader(t.Context(), store, []string{"c0", "c1", "c2"}, 1000, 2500)
	for _, c := range [][2]int{{0, 10}, {995, 10}, {1000, 1000}, {1990, 510}, {2490, 10}, {0, 2500}} {
		got := make([]byte, c[1])
		n, err := r.ReadAt(got, int64(c[0]))
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("%v: %v", c, err)
		}
		if n != c[1] || !bytes.Equal(got, want[c[0]:c[0]+c[1]]) {
			t.Errorf("read of %v: %d bytes, not the file's", c, n)
		}
	}
	if n, err := r.ReadAt(make([]byte, 10), 2495); n != 5 || !errors.Is(err, io.EOF) {
		t.Errorf("a read past the end: %d, %v", n, err)
	}
	missing := newChunkReader(t.Context(), store, []string{"c0", "gone"}, 1000, 2500)
	var chunk *chunkError
	if _, err := missing.ReadAt(make([]byte, 10), 1500); !errors.As(err, &chunk) || chunk.index != 1 {
		t.Errorf("a chunk that the store lost: %v", err)
	}
}

// manifestOf returns a minimal archive's manifest with the sections.
func manifestOf(t *testing.T, sections ...ManifestSection) string {
	t.Helper()
	m := Manifest{Format: Format, FormatVersion: Version, PublishedSchemaVersion: 1, Profile: ProfileFull,
		Generator: serializer.Generator(), ExportedAt: time.Now().UTC(),
		Project: ManifestProject{ID: "prj_x", Name: "P", Slug: "p"}, Source: Source{Kind: "draft"},
		Sections: sections}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestReadArchivedRefusesVersions checks that an archive of another format, or
// of a version of the format past this server's, is refused, before anything
// else of it is read.
//
// Verifies: FR-DATA-01/AC2
func TestReadArchivedRefusesVersions(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ manifest, check string }{
		"another format": {`{"format":"something.else","formatVersion":1}`, "format"},
		"a newer version": {fmt.Sprintf(`{"format":%q,"formatVersion":%d}`, Format, Version+1),
			"format_version"},
		"no version": {fmt.Sprintf(`{"format":%q}`, Format), "format_version"},
	} {
		data := zipped(t, [2]string{ManifestPath, tc.manifest})
		a, err := openArchive(bytes.NewReader(data), int64(len(data)), 1<<30)
		if err != nil {
			t.Fatal(err)
		}
		_, err = readArchived(a)
		var refused *ImportError
		if !errors.As(err, &refused) || refused.Code != FailFormatUnsupported || refused.Check != tc.check {
			t.Errorf("%s: %v, want format_unsupported at %s", name, err, tc.check)
		}
	}
}

// TestReadArchivedRefusesUnknownMember checks that a member that no section
// accounts for refuses the archive, so that nothing in it goes unread.
//
// Verifies: FR-DATA-01/AC12, NFR-SEC-13
func TestReadArchivedRefusesUnknownMember(t *testing.T) {
	t.Parallel()
	project := `{"name":"P","slug":"p","description":"","settings":{}}`
	entry := ManifestSection{Name: "project", Path: "draft/project.json", Items: 1, SHA256: sha256Hex([]byte(project))}
	data := zipped(t, [2]string{ManifestPath, manifestOf(t, entry)}, [2]string{"draft/project.json", project},
		[2]string{"somewhere/else.json", "{}"})
	a, err := openArchive(bytes.NewReader(data), int64(len(data)), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	_, err = readArchived(a)
	wantRefused(t, err, "unexpected_member")
}
