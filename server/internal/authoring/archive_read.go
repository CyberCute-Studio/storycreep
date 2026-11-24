package authoring

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/jsonscan"
)

// The guards of reading an archive (PRD NFR-SEC-13), which hold whatever its
// central directory says, since an archive is untrusted.
const (
	// maxArchiveEntries is the most members that an archive may have: a
	// project of the largest fixture with 30 releases has some thousands.
	maxArchiveEntries = 200_000
	// maxMemberName is the longest path of a member.
	maxMemberName = 512
	// maxCompressionRatio is how many times its compressed size a member may
	// expand to, past ratioFreeBytes, below which a highly compressible file,
	// as a small JSON file of repeated keys, is no bomb.
	maxCompressionRatio = 250
	ratioFreeBytes      = 1 << 20
	// expansionFactor is how many times the import limit all of an archive's
	// members may expand to together, so that the guard scales with the
	// instance's STORYCREEP_IMPORT_MAX_BYTES (FR-DATA-01). minExpansion keeps a
	// small limit from refusing a small archive whose JSON compresses well.
	expansionFactor = 4
	minExpansion    = 1 << 30
	// maxDirectoryBytes is the most that opening an archive reads of it, which
	// is its central directory and the search for its end. The zip reader
	// keeps a File of some 200 bytes for each header of at least 46 bytes, so
	// that this bounds what it holds, before the count of its members can be
	// checked. 200,000 members of the longest paths that an export writes take
	// about half of it.
	maxDirectoryBytes = 64 << 20
	// maxJSONMember is the largest JSON file of an archive, which is read into
	// memory whole. The largest is the index of the releases, which holds each
	// one's validation report: 89 MB for the large fixture with 30 releases,
	// so that this takes some 65 releases of a project of that size. An archive
	// past it can leave the release history out, which the export offers.
	maxJSONMember = 192 << 20
	// maxJSONValues is the most values that a JSON file may hold, each of which
	// decoding it takes memory for. That index of the large fixture has 2.1
	// million.
	maxJSONValues = 5_000_000
	// maxJSONTotal is the most that the JSON files held in memory at once may
	// take together: the manifest and the sections of an archive, which stay
	// for the whole import, the release that an archive of a release is, and
	// the files of the one release being read. The large fixture with 30
	// releases holds 115 MiB of them. It counts the files' bytes: at these
	// limits the heap measured up to 1.9 GiB above what it held before, for an
	// archive of the most IDs whose largest file is as costly as a file can be
	// to remap, and less for one that fails its schema, whose check stops at
	// the first failure.
	maxJSONTotal = 256 << 20
	// maxItemBytes and maxItemValues hold one item of a section, before its
	// tree is built (checkItems). The largest item of the large fixture, a
	// release of the index, has 3 MB and 70,000 values.
	maxItemBytes  = 32 << 20
	maxItemValues = 1_000_000
	// maxPlainJSON is the most that the manifest or the project may take, the
	// two files that are not lists and so are validated whole, which
	// maxItemValues holds too.
	maxPlainJSON = 16 << 20
	// maxRemapIDs is the most IDs that an import takes, of its rows and of what
	// its releases' files spell, which the collision check asks about and a
	// remap gives fresh ones, each of which its tables hold, at some 100 bytes
	// an ID, and which a release's text can spell far more of than it has
	// values. The large fixture has tens of thousands.
	maxRemapIDs = 4_000_000
	// maxImportAssets is the most assets that an archive may hold. The one
	// transaction of an import holds the advisory lock of each one's blob
	// until it commits, in the lock table that every session of the database
	// shares, which takes some 13,000 locks on PostgreSQL's default settings,
	// past which any other session that needs a lock fails too.
	maxImportAssets = 4_000
	// maxImportPeople is the most people outside the workspace whom an archive
	// may name. Each takes an account, in a table of the whole instance, and
	// a project's comments have far fewer authors.
	maxImportPeople = 10_000
	// maxImportedSeq is the greatest operation sequence that a release of an
	// archive may name, from which the project's own sequence then goes on. A
	// client reads a seq as a JavaScript number, exact up to 2^53 - 1, and each
	// batch takes the next, so that a project imported at that limit would pass
	// it within two edits, after which its editors could no longer tell its
	// operations apart. This leaves half of that range to the project.
	maxImportedSeq = 1 << 52
)

// errDirectoryTooLarge stops the zip reader at maxDirectoryBytes.
var errDirectoryTooLarge = errors.New("authoring: the archive's central directory is too large")

// What checkClaimedEntries reads of a zip's end records.
const (
	zipEndLen       = 22
	zip64LocatorLen = 20
	zip64EndLen     = 56
	// zipEndNear and zipEndSearch are how far from the end of a file the zip
	// reader looks for its end record, first and then at most.
	zipEndNear   = 1024
	zipEndSearch = 65 * 1024
)

// The signatures of a zip's end record, zip64 locator, and zip64 end record.
var (
	zipEndSignature       = []byte("PK\x05\x06")
	zip64LocatorSignature = []byte("PK\x06\x07")
	zip64EndSignature     = []byte("PK\x06\x06")
)

// zipEndAt returns where in tail, the last bytes of a file, the zip reader
// finds the end record, or -1: in each part of the file that it searches, the
// last signature, if the comment that it announces ends within that part.
func zipEndAt(tail []byte) int {
	for _, n := range []int{zipEndNear, zipEndSearch} {
		from := max(len(tail)-n, 0)
		block := tail[from:]
		for i := len(block) - zipEndLen; i >= 0; i-- {
			if !bytes.HasPrefix(block[i:], zipEndSignature) {
				continue
			}
			if comment := binary.LittleEndian.Uint16(block[i+zipEndLen-2:]); i+zipEndLen+int(comment) <= len(block) {
				return from + i
			}
			break
		}
	}
	return -1
}

// checkClaimedEntries refuses a file whose zip64 end record claims more members
// than an import takes. It runs before zip.NewReader, which makes room for as
// many as the record claims, up to a thirtieth of the file's size, before it
// reads one, and keeps that room for as long as a member is open: a directory
// of one member that claims 71 million would add 546 MiB to what an import of
// 2 GiB holds, and proportionally more under a raised limit. It reads the one
// record that the reader takes, under the reader's own conditions, and no other
// that the file spells: an export stores an asset's bytes as they are, near its
// end, and they can spell any number of records that the reader passes over
// (TestClaimedEntriesAgreesWithTheZipReader). A failure to read that is no
// store's leaves the file to the reader, which says what is wrong with it.
func checkClaimedEntries(r io.ReaderAt, size int64) error {
	tail := make([]byte, min(size, zipEndSearch+zip64LocatorLen))
	if n, err := r.ReadAt(tail, size-int64(len(tail))); n < len(tail) {
		if readFailure(err) {
			return err
		}
		return nil
	}
	i := zipEndAt(tail)
	if i < zip64LocatorLen {
		return nil
	}
	le := binary.LittleEndian
	// The reader compares the directory's size with 16 bits too.
	if end := tail[i:]; le.Uint16(end[10:]) != 0xffff && le.Uint32(end[12:]) != 0xffff &&
		le.Uint32(end[16:]) != 0xffffffff {
		return nil
	}
	locator := tail[i-zip64LocatorLen : i]
	if !bytes.HasPrefix(locator, zip64LocatorSignature) || le.Uint32(locator[4:]) != 0 || le.Uint32(locator[16:]) != 1 {
		return nil
	}
	at := le.Uint64(locator[8:])
	if size < zip64EndLen || at > uint64(size-zip64EndLen) {
		return nil
	}
	var record [zip64EndLen]byte
	if n, err := r.ReadAt(record[:], int64(at)); n < len(record) {
		if readFailure(err) {
			return err
		}
		return nil
	}
	if !bytes.HasPrefix(record[:], zip64EndSignature) {
		return nil
	}
	if n := le.Uint64(record[32:]); n > maxArchiveEntries {
		return invalidArchive("entries", "it claims %d members, more than the %d that an import takes", n,
			maxArchiveEntries)
	}
	return nil
}

// Failure codes of an import, in its failure's code, which the API and the web
// app show (FR-DATA-01).
const (
	// FailArchiveInvalid is a file that is not an archive that the guards
	// take: its check says which.
	FailArchiveInvalid = "archive_invalid"
	// FailFormatUnsupported is an archive of a format or a version that this
	// server does not read (AC2).
	FailFormatUnsupported = "format_unsupported"
	// FailSchemaInvalid is a file that its JSON Schema refuses, or whose hash
	// is not the manifest's: its section names it.
	FailSchemaInvalid = "schema_invalid"
	// FailSectionUnsupported is a section that this server cannot import
	// (AC12): its section names it.
	FailSectionUnsupported = "section_unsupported"
	// FailContentInvalid is content that the project's commands refuse: its
	// section names where.
	FailContentInvalid = "content_invalid"
	// FailReleaseInvalid is a release that fails its verification (AC11): its
	// version and its check name them.
	FailReleaseInvalid = "release_invalid"
	// FailAssetInvalid is an asset whose file is missing or is not what its
	// index says.
	FailAssetInvalid = "asset_invalid"
	// FailLimitExceeded is a workspace without room for the project, or its
	// assets.
	FailLimitExceeded = "workspace_limit"
	// FailForbidden is a user who may no longer create a project in the
	// workspace.
	FailForbidden = "forbidden"
	// FailInternal is a failure of the server's own.
	FailInternal = "internal"
)

// ImportError is why an archive is refused, which is the import's failure: it
// says what is wrong with the archive, and nothing of the instance's other
// workspaces.
type ImportError struct {
	// Code is one of the Fail codes.
	Code string `json:"code"`
	// Section names the section that is at fault, if one is.
	Section string `json:"section,omitempty"`
	// Version is the version of the release that is at fault, if one is.
	Version string `json:"version,omitempty"`
	// Check is the check that failed.
	Check string `json:"check,omitempty"`
	// Detail says more, in English, for a log or a support request.
	Detail string `json:"detail,omitempty"`
	// unique marks a row that a unique constraint refused, which a row that
	// came after the collision check may be the reason for.
	unique bool
	// cause is the error of a limit or of a role that the transaction that
	// writes the project was refused for, which an import of the server's own
	// archive answers its request with (plan note N92), or nil.
	cause error
}

func (e *ImportError) Error() string {
	var b strings.Builder
	b.WriteString("authoring: the archive is refused: ")
	b.WriteString(e.Code)
	for _, f := range [][2]string{{"section", e.Section}, {"version", e.Version}, {"check", e.Check},
		{"detail", e.Detail}} {
		if f[1] != "" {
			fmt.Fprintf(&b, " %s=%q", f[0], f[1])
		}
	}
	return b.String()
}

// maxFailureText is how many bytes of each of a failure's texts are kept, as
// they can repeat what an archive spells, at any length.
const maxFailureText = 1024

// recorded returns the failure as it is stored and shown: each of its texts
// within maxFailureText, and with a mark in place of each control character.
// PostgreSQL's jsonb takes no NUL, so that a failure that repeats one could
// never be recorded, and its import would stay on its way.
func (e *ImportError) recorded() *ImportError {
	out := *e
	for _, text := range []*string{&out.Section, &out.Version, &out.Check, &out.Detail} {
		*text = failureText(*text)
	}
	return &out
}

// failureText returns s within maxFailureText, as valid UTF-8 without control
// characters.
func failureText(s string) string {
	if len(s) > maxFailureText {
		s = s[:maxFailureText]
	}
	mark := string(utf8.RuneError)
	return strings.Map(func(r rune) rune {
		if input.IsControl(r) {
			return utf8.RuneError
		}
		return r
	}, strings.ToValidUTF8(s, mark))
}

func invalidArchive(check, format string, args ...any) *ImportError {
	return &ImportError{Code: FailArchiveInvalid, Check: check, Detail: fmt.Sprintf(format, args...)}
}

// archive is an opened archive, whose members have passed the guards.
type archive struct {
	files map[string]*zip.File
	// names are the paths of files, sorted, which under searches.
	names []string
	// source counts what is read of the file, which is what a member's
	// expansion is measured against, as its header's compressed size is the
	// archive's own word.
	source *countingReaderAt
}

// countingReaderAt counts the bytes read through it, and refuses to read past
// limit, unless that is 0. Like chunkReader, it is not safe to call from
// several goroutines.
type countingReaderAt struct {
	r     io.ReaderAt
	n     uint64
	limit uint64
}

// ReadAt implements io.ReaderAt.
func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if c.limit > 0 && c.n+uint64(len(p)) > c.limit {
		return 0, errDirectoryTooLarge
	}
	n, err := c.r.ReadAt(p, off)
	c.n += uint64(n)
	return n, err
}

// openArchive reads the central directory of the zip r, of size bytes, and
// refuses it unless every member has a safe path, is a regular file stored
// or deflated without encryption, and expands within the guards, which scale
// with the instance's import limit.
func openArchive(r io.ReaderAt, size, importMax int64) (*archive, error) {
	if err := checkClaimedEntries(r, size); err != nil {
		return nil, err
	}
	source := &countingReaderAt{r: r, limit: maxDirectoryBytes}
	zr, err := zip.NewReader(source, size)
	source.limit = 0
	switch {
	case errors.Is(err, errDirectoryTooLarge):
		return nil, invalidArchive("entries", "its central directory is past the %d bytes that an import reads",
			maxDirectoryBytes)
	case errors.Is(err, zip.ErrInsecurePath):
		return nil, invalidArchive("path", "a member's path leaves the archive")
	case err != nil && readFailure(err):
		return nil, err
	case err != nil:
		return nil, invalidArchive("zip", "the file is not a zip archive")
	}
	if len(zr.File) > maxArchiveEntries {
		return nil, invalidArchive("entries", "it has %d members, more than the %d that an import takes", len(zr.File),
			maxArchiveEntries)
	}
	budget := max(importMax*expansionFactor, minExpansion)
	a := &archive{files: make(map[string]*zip.File, len(zr.File)), source: source}
	var total uint64
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			if err := checkMemberName(strings.TrimSuffix(f.Name, "/")); err != nil {
				return nil, err
			}
			if f.UncompressedSize64 != 0 {
				return nil, invalidArchive("path", "the directory %q holds data", f.Name)
			}
			continue
		}
		if err := checkMemberName(f.Name); err != nil {
			return nil, err
		}
		if _, dup := a.files[f.Name]; dup {
			return nil, invalidArchive("duplicate", "the path %q is in it twice", f.Name)
		}
		switch {
		case !f.Mode().IsRegular():
			return nil, invalidArchive("file_type", "the member %q is not a regular file", f.Name)
		case f.Flags&0x1 != 0:
			return nil, invalidArchive("encrypted", "the member %q is encrypted", f.Name)
		case f.Method != zip.Store && f.Method != zip.Deflate:
			return nil, invalidArchive("method", "the member %q has a compression method that an import does not read",
				f.Name)
		case f.UncompressedSize64 > ratioFreeBytes &&
			f.UncompressedSize64/max(f.CompressedSize64, 1) > maxCompressionRatio:
			return nil, invalidArchive("ratio", "the member %q expands more than %d times", f.Name, maxCompressionRatio)
		}
		// Against what is left of the budget, as a size that the header claims
		// near 2^64 would wrap a sum back to a small one.
		if f.UncompressedSize64 > uint64(budget)-total {
			return nil, invalidArchive("size", "its members expand to more than the %d bytes that an import takes", budget)
		}
		total += f.UncompressedSize64
		a.files[f.Name] = f
	}
	a.names = slices.Sorted(maps.Keys(a.files))
	return a, nil
}

// checkMemberName refuses a path that is not a plain relative path in the
// archive: one that is absolute, climbs out of it, uses a backslash or a
// Windows drive, or holds a control character.
func checkMemberName(name string) error {
	switch {
	case name == "" || len(name) > maxMemberName || !utf8.ValidString(name):
		return invalidArchive("path", "a member's path is empty, too long, or not UTF-8")
	case strings.ContainsAny(name, "\\:"):
		return invalidArchive("path", "the path %q has a backslash or a colon", name)
	case strings.HasPrefix(name, "/") || path.Clean(name) != name:
		return invalidArchive("path", "the path %q is not a clean relative path", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return invalidArchive("path", "a member's path holds a control character")
		}
	}
	for segment := range strings.SplitSeq(name, "/") {
		if segment == "." || segment == ".." {
			return invalidArchive("path", "the path %q leaves the archive", name)
		}
	}
	return nil
}

// has says whether the archive has a member of the path.
func (a *archive) has(name string) bool {
	_, ok := a.files[name]
	return ok
}

// maxInitialRead is the most memory that reading a member sets aside on its
// header's word alone: a member that holds more than that then takes the size
// that its header says, which its limit bounds.
const maxInitialRead = 16 << 20

// read returns the member of the path, whole, of at most limit bytes, and
// checks its CRC-32, which a read to its end does, and its expansion against
// the bytes that it took of the file, as a central directory can claim a
// compressed size that the member does not have.
func (a *archive) read(name string, limit int64) ([]byte, error) {
	f, ok := a.files[name]
	if !ok {
		return nil, invalidArchive("missing", "it has no member %q", name)
	}
	if f.UncompressedSize64 > uint64(limit) {
		return nil, invalidArchive("size", "the member %q has %d bytes, more than the %d that it may", name,
			f.UncompressedSize64, limit)
	}
	start := a.source.n
	rc, err := f.Open()
	if err != nil {
		return nil, memberError(name, err)
	}
	defer func() { _ = rc.Close() }()
	data := make([]byte, 0, min(f.UncompressedSize64, maxInitialRead))
	buf := make([]byte, 64*1024)
	for {
		n, err := rc.Read(buf)
		if int64(len(data)+n) > int64(f.UncompressedSize64) {
			return nil, invalidArchive("size", "the member %q holds more than its header says", name)
		}
		// Past what was set aside on the header's word alone, the rest at once:
		// growing by steps would hold each old buffer beside its copy, some
		// twice what the member takes.
		if len(data)+n > cap(data) {
			data = slices.Grow(data, int(f.UncompressedSize64)-len(data))
		}
		data = append(data, buf[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, memberError(name, err)
		}
	}
	if int64(len(data)) != int64(f.UncompressedSize64) {
		return nil, invalidArchive("size", "the member %q holds less than its header says", name)
	}
	if n := uint64(len(data)); n > ratioFreeBytes && n/max(a.source.n-start, 1) > maxCompressionRatio {
		return nil, invalidArchive("ratio", "the member %q expands more than %d times", name, maxCompressionRatio)
	}
	return data, nil
}

// reserve adds the member of the path to held, the bytes of the JSON files held
// in memory together, by the size that its header says, which read holds it to,
// and refuses it before any of it is read if that takes them past maxJSONTotal.
// what names those files in the refusal. A path that the archive does not have
// adds nothing, and read refuses it.
func (a *archive) reserve(held *int64, name, what string) error {
	f, ok := a.files[name]
	if !ok {
		return nil
	}
	// Against what is left, as a size that the header claims near 2^64 would
	// wrap a sum back to a small one. A release that its migration made larger
	// can have left nothing.
	left := int64(maxJSONTotal) - *held
	if left < 0 || f.UncompressedSize64 > uint64(left) {
		return invalidArchive("size", "%s hold more than the %d bytes that an import reads", what, maxJSONTotal)
	}
	*held += int64(f.UncompressedSize64)
	return nil
}

// readJSON returns the JSON file of the path, whole, once it is within limit
// bytes and values JSON values, so that what a parser builds of it is bounded
// whatever the file's size says of it.
func (a *archive) readJSON(name string, limit int64, values int) ([]byte, error) {
	data, err := a.read(name, limit)
	if err != nil {
		return nil, err
	}
	if n := jsonscan.Values(data); n > values {
		return nil, invalidArchive("json_values", "the member %q holds %d JSON values, more than the %d that it may",
			name, n, values)
	}
	return data, nil
}

// memberError is the failure of reading a member: the store's own error, as a
// chunk that it could not read, or a member that is damaged.
func memberError(name string, err error) error {
	if readFailure(err) {
		return err
	}
	return invalidArchive("zip", "the member %q cannot be read: %v", name, err)
}

// readFailure says whether err is a failure to read the file, of the store or
// the disk, or the end of the context, which another attempt may get past,
// rather than something wrong with the archive.
func readFailure(err error) bool {
	var store *chunkError
	var file *fs.PathError
	return errors.As(err, &store) || errors.As(err, &file) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

// under returns the paths of the archive's members below the directory dir,
// which ends with a slash, in their order. It searches the sorted paths, as an
// archive's index can name as many releases as it has members.
func (a *archive) under(dir string) []string {
	first, _ := slices.BinarySearch(a.names, dir)
	last := first
	for last < len(a.names) && strings.HasPrefix(a.names[last], dir) {
		last++
	}
	return a.names[first:last:last]
}

// chunkError is a failure of the store to give a chunk of the upload, which is
// the instance's and not the archive's, so another attempt may get past it.
type chunkError struct {
	index int
	err   error
}

func (e *chunkError) Error() string {
	return fmt.Sprintf("authoring: read chunk %d of the upload: %v", e.index, e.err)
}

func (e *chunkError) Unwrap() error { return e.err }

// chunkReader reads the chunks of an upload, each a blob of the store, as one
// file, for the zip reader, holding one chunk in memory at a time. It is not
// safe to call from several goroutines.
type chunkReader struct {
	ctx        context.Context
	store      blob.Store
	keys       []string
	chunkBytes int64
	size       int64
	cached     int
	buf        []byte
}

func newChunkReader(ctx context.Context, store blob.Store, keys []string, chunkBytes, size int64) *chunkReader {
	return &chunkReader{ctx: ctx, store: store, keys: keys, chunkBytes: chunkBytes, size: size, cached: -1}
}

// ReadAt implements io.ReaderAt.
func (c *chunkReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("authoring: a negative offset")
	}
	n := 0
	for n < len(p) {
		if off >= c.size {
			return n, io.EOF
		}
		i := int(off / c.chunkBytes)
		if err := c.load(i); err != nil {
			return n, err
		}
		within := off - int64(i)*c.chunkBytes
		copied := copy(p[n:], c.buf[within:])
		n += copied
		off += int64(copied)
	}
	return n, nil
}

// load reads the chunk i into the buffer, unless it is there, and checks that
// it has the size that its place in the file needs.
func (c *chunkReader) load(i int) error {
	if c.cached == i {
		return nil
	}
	if i >= len(c.keys) {
		return &chunkError{index: i, err: errors.New("the upload has no such chunk")}
	}
	want := c.chunkBytes
	if rest := c.size - int64(i)*c.chunkBytes; rest < want {
		want = rest
	}
	rc, _, err := c.store.Open(c.ctx, c.keys[i])
	if err != nil {
		return &chunkError{index: i, err: err}
	}
	defer func() { _ = rc.Close() }()
	if int64(cap(c.buf)) < want {
		c.buf = make([]byte, want)
	}
	c.buf = c.buf[:want]
	c.cached = -1
	if _, err := io.ReadFull(rc, c.buf); err != nil {
		return &chunkError{index: i, err: err}
	}
	c.cached = i
	return nil
}
