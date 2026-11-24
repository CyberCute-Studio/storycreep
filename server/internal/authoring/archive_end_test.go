package authoring

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"testing"
)

// endSpy is the file that zip.NewReader reads, and records the count of the
// zip64 end record that the reader took: the read of 56 bytes that follows its
// read of the locator's 20. It is the reader's own word for which record it
// took, which a second parser of the format could get wrong in the same way.
type endSpy struct {
	r       io.ReaderAt
	last    int
	claimed uint64
	took    bool
}

// ReadAt implements io.ReaderAt.
func (s *endSpy) ReadAt(p []byte, off int64) (int, error) {
	n, err := s.r.ReadAt(p, off)
	if !s.took && s.last == zip64LocatorLen && len(p) == zip64EndLen && n == len(p) &&
		bytes.HasPrefix(p, zip64EndSignature) {
		s.claimed, s.took = binary.LittleEndian.Uint64(p[32:]), true
	}
	s.last = len(p)
	return n, err
}

// readCounter counts the reads through it, and the bytes that they ask for.
type readCounter struct {
	r     io.ReaderAt
	reads int
	bytes int
}

// ReadAt implements io.ReaderAt.
func (c *readCounter) ReadAt(p []byte, off int64) (int, error) {
	c.reads++
	c.bytes += len(p)
	return c.r.ReadAt(p, off)
}

// compareEnd checks checkClaimedEntries against what zip.NewReader reads of the
// file: it refuses the file exactly when the record that the reader takes claims
// more members than an import takes, and reads a bounded part of the file.
func compareEnd(r io.ReaderAt, size int64) error {
	spy := &endSpy{r: r}
	zr, zerr := zip.NewReader(spy, size)
	counter := &readCounter{r: r}
	err := checkClaimedEntries(counter, size)
	if counter.reads > 2 || counter.bytes > zipEndSearch+zip64LocatorLen+zip64EndLen {
		return fmt.Errorf("it took %d reads of %d bytes", counter.reads, counter.bytes)
	}
	over := spy.took && spy.claimed > maxArchiveEntries
	var refused *ImportError
	switch {
	case over && (!errors.As(err, &refused) || refused.Check != "entries"):
		return fmt.Errorf("the reader takes a claim of %d members, and the check says %v", spy.claimed, err)
	case !over && err != nil:
		return fmt.Errorf("the reader opens it (%v) with %d claimed, and the check refuses it: %w", zerr, spy.claimed, err)
	case zerr == nil && len(zr.File) > maxArchiveEntries && !over:
		return fmt.Errorf("%d members got past the check", len(zr.File))
	}
	return nil
}

// zipOfMembers returns a zip of n empty members and the comment, whose offsets
// count from offset.
func zipOfMembers(t testing.TB, n int, comment string, offset int64) []byte {
	t.Helper()
	var b bytes.Buffer
	b.Grow(n * 110)
	w := zip.NewWriter(&b)
	w.SetOffset(offset)
	for i := range n {
		if _, err := w.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("m%06d", i), Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.SetComment(comment); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// zipHolding returns a zip whose one member holds data as it is.
func zipHolding(t testing.TB, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, err := w.CreateHeader(&zip.FileHeader{Name: "assets/a.png", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// spelledEnd returns a zip64 locator that points at at and an end record, as a
// member's bytes can spell them.
func spelledEnd(at uint64) []byte {
	pair := make([]byte, zip64LocatorLen+zipEndLen)
	copy(pair, zip64LocatorSignature)
	binary.LittleEndian.PutUint64(pair[8:], at)
	binary.LittleEndian.PutUint32(pair[16:], 1)
	copy(pair[zip64LocatorLen:], zipEndSignature)
	binary.LittleEndian.PutUint16(pair[zip64LocatorLen+10:], 0xffff)
	return pair
}

// spelledRecord returns a zip64 end record that claims claimed members.
func spelledRecord(claimed uint64) []byte {
	record := make([]byte, zip64EndLen)
	copy(record, zip64EndSignature)
	binary.LittleEndian.PutUint64(record[24:], claimed)
	binary.LittleEndian.PutUint64(record[32:], claimed)
	return record
}

// TestClaimedEntriesAgreesWithTheZipReader compares the end-record pre-read with
// what zip.NewReader reads, over archives of Go's writer with and without zip64,
// comments up to the longest, data before the first member, truncated records,
// a locator that points to the wrong place, members that spell end records of
// their own, the two sides of the limit on members, and mutations of the end.
//
// Verifies: NFR-SEC-13
func TestClaimedEntriesAgreesWithTheZipReader(t *testing.T) {
	t.Parallel()
	check := func(name string, data []byte) {
		t.Helper()
		if err := compareEnd(bytes.NewReader(data), int64(len(data))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	plain := zipOfMembers(t, 3, "", 0)
	wide := zipOfMembers(t, 70_000, "", 0)
	for _, n := range []int{0, 1, 65_534, 65_535, 65_536} {
		check(fmt.Sprintf("%d members", n), zipOfMembers(t, n, "", 0))
	}
	check("70,000 members", wide)

	// A zip64 end that the offset of its directory calls for, over a file of
	// 8 GiB that is zeros up to the archive, which stays small to read.
	const far = int64(8 << 30)
	checkFar := func(name string, data []byte) {
		t.Helper()
		size := far + int64(len(data))
		if err := compareEnd(paddedZip{tail: data, size: size}, size); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	checkFar("zip64 by its offset", zipOfMembers(t, 2, "", far))

	sig := string(zipEndSignature)
	for _, n := range []int{1, 21, 22, 1002, 1024, 1025, 65_000, 65_535} {
		check(fmt.Sprintf("a comment of %d bytes", n), zipOfMembers(t, 2, string(make([]byte, n)), 0))
		checkFar(fmt.Sprintf("zip64, a comment of %d bytes", n), zipOfMembers(t, 2, string(make([]byte, n)), far))
	}
	for _, at := range []int{0, 1, 500, 1000, 1024, 40_000, 65_000} {
		comment := append(make([]byte, at), sig...)
		comment = append(comment, make([]byte, 40)...)
		check(fmt.Sprintf("a comment with a signature at %d", at), zipOfMembers(t, 2, string(comment), 0))
		pair := append(make([]byte, at), spelledEnd(0)...)
		check(fmt.Sprintf("a comment with a locator and a record at %d", at),
			zipOfMembers(t, 2, string(append(pair, make([]byte, 30)...)), 0))
	}

	for _, n := range []int{1, 100, 70_000} {
		prefix := make([]byte, n)
		check(fmt.Sprintf("%d bytes before a plain zip", n), slices.Concat(prefix, plain))
		check(fmt.Sprintf("%d bytes before a zip64", n), slices.Concat(prefix, wide))
		check(fmt.Sprintf("%d bytes that its offsets count", n), slices.Concat(prefix, zipOfMembers(t, 3, "", int64(n))))
	}

	for cut := 1; cut <= zip64EndLen+zip64LocatorLen+zipEndLen+8; cut++ {
		check(fmt.Sprintf("a plain zip without its last %d bytes", cut), plain[:max(len(plain)-cut, 0)])
		check(fmt.Sprintf("a zip64 without its last %d bytes", cut), wide[:len(wide)-cut])
	}

	locatorAt := len(wide) - zipEndLen - zip64LocatorLen
	if !bytes.HasPrefix(wide[locatorAt:], zip64LocatorSignature) {
		t.Fatal("the zip64 archive has no locator where it should")
	}
	realAt := binary.LittleEndian.Uint64(wide[locatorAt+8:])
	size := uint64(len(wide))
	for _, at := range []uint64{0, 1, realAt - 1, realAt + 1, size - zip64EndLen, size - zip64EndLen + 1, size, size + 1,
		1 << 40, 1<<63 - 1, 1 << 63, 1<<64 - 1} {
		moved := slices.Clone(wide)
		binary.LittleEndian.PutUint64(moved[locatorAt+8:], at)
		check(fmt.Sprintf("a locator that points to %d", at), moved)
	}
	for _, field := range []int{4, 16} {
		moved := slices.Clone(wide)
		binary.LittleEndian.PutUint32(moved[locatorAt+field:], 7)
		check(fmt.Sprintf("a locator with 7 at %d", field), moved)
	}

	// A member's bytes can spell end records, which an asset's do as they are:
	// the reader takes the last record of the file, and these come before it.
	check("a member that spells nine end records",
		zipHolding(t, bytes.Repeat(spelledEnd(0), 9)))
	record := spelledRecord(1 << 26)
	draft := zipHolding(t, slices.Concat(record, spelledEnd(0)))
	at := bytes.Index(draft, zip64EndSignature)
	check("a member that spells a claim of 2^26 members",
		zipHolding(t, slices.Concat(record, spelledEnd(uint64(at)))))

	// Exactly as many members as an import takes, and one more.
	limit := zipOfMembers(t, maxArchiveEntries, "", 0)
	check("200,000 members", limit)
	if _, err := openArchive(bytes.NewReader(limit), int64(len(limit)), 1<<30); err != nil {
		t.Errorf("200,000 members: %v", err)
	}
	over := zipOfMembers(t, maxArchiveEntries+1, "", 0)
	check("200,001 members", over)
	_, err := openArchive(bytes.NewReader(over), int64(len(over)), 1<<30)
	wantRefused(t, err, "entries")

	rng := rand.New(rand.NewPCG(18, 84))
	bases := [][]byte{plain, zipOfMembers(t, 2, "", far), zipOfMembers(t, 2, "with a comment", 0),
		zipHolding(t, slices.Concat(record, spelledEnd(0), spelledEnd(0)))}
	for i := range 20_000 {
		base := bases[i%len(bases)]
		mutated := slices.Clone(base)
		var file io.ReaderAt = bytes.NewReader(mutated)
		size := int64(len(mutated))
		if i%len(bases) == 1 {
			size += far
			file = paddedZip{tail: mutated, size: size}
		}
		window := min(len(mutated), zip64EndLen+zip64LocatorLen+zipEndLen+32)
		for range 1 + rng.IntN(4) {
			at := len(mutated) - 1 - rng.IntN(window)
			switch rng.IntN(4) {
			case 0:
				mutated[at] = byte(rng.IntN(256))
			case 1:
				mutated[at] = 0xff
			case 2:
				copy(mutated[at:], zipEndSignature)
			default:
				copy(mutated[at:], zip64LocatorSignature)
			}
		}
		if err := compareEnd(file, size); err != nil {
			t.Fatalf("mutation %d: %v", i, err)
		}
	}
}
