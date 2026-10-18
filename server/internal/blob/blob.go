// Package blob stores the files of the instance's assets (PRD FR-ASSET-01,
// ADR-009): the images that its users upload, and the thumbnails that the
// server derives from them. A Store has two drivers, which take the same keys
// and behave the same way, as one shared test suite checks (package
// blobtest): the local driver (Local), which keeps each blob as a file of a
// directory, and the S3 driver (S3), which keeps it as an object of a bucket
// of an S3-compatible object storage. Which one stores the instance's blobs
// is a matter of configuration alone (FR-ASSET-01 AC1, package asset).
//
// A key is the path of a blob in its store, of segments joined by slashes,
// each of letters, digits, dots, hyphens, and underscores, as
// "{workspace}/{sha256}.png". A key never starts a segment with a dot, which
// the local driver keeps for the files that it is writing, so that no key
// names one of them, or reaches outside the directory (CheckKey).
//
// A store checks no access: the server reads a blob only after it has
// checked that the user may (package asset), and the S3 driver gives out
// URLs that it signs for a short time instead of the bucket's own, which are
// not public (FR-ASSET-01 AC2).
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound is returned for a blob that the store does not have.
var ErrNotFound = errors.New("blob: no such blob")

// Info describes a blob.
type Info struct {
	// Key is the blob's key.
	Key string
	// Size is how many bytes it has.
	Size int64
	// Modified is when it was last stored.
	Modified time.Time
}

// Store stores blobs by their keys. Its methods are safe to call at once
// from many goroutines.
type Store interface {
	// Put stores data under key, with the content type, in place of a blob
	// that the key may name already. A blob is stored whole or not at all:
	// a reader of the key finds the old blob, or none, until Put returns.
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Open returns the blob of key, to read and then close, with its Info,
	// or ErrNotFound if there is none.
	Open(ctx context.Context, key string) (io.ReadCloser, Info, error)
	// Stat returns the Info of the blob of key, or ErrNotFound if there is
	// none.
	Stat(ctx context.Context, key string) (Info, error)
	// Delete deletes the blob of key. Deleting a blob that does not exist
	// does nothing, and returns nil.
	Delete(ctx context.Context, key string) error
	// List calls fn with the Info of each blob whose key starts with
	// prefix, which is "" or a key's first segments, each followed by a
	// slash, as "{workspace}/", in the order of their keys, until fn returns
	// an error, which List returns.
	List(ctx context.Context, prefix string, fn func(Info) error) error
	// SignedURL returns a URL that reads the blob of key, until ttl has
	// passed, with the content type and the Content-Disposition that it
	// names, and a Cache-Control that lets the browser that reads it keep
	// the answer for as long as the URL works, and true; or "", false for a
	// store that has no URLs of its own, as the local driver, whose blobs the
	// server serves itself. It does not check that the blob exists.
	SignedURL(ctx context.Context, key string, ttl time.Duration, contentType, disposition string) (string, bool,
		error)
}

// CacheControl returns the Cache-Control of an answer that the browser that
// reads it may keep, for its user alone, until ttl has passed: private, with
// a max-age of ttl in whole seconds.
func CacheControl(ttl time.Duration) string {
	return "private, max-age=" + strconv.FormatInt(int64(ttl/time.Second), 10)
}

// maxSegmentLength is the longest segment of a key: the longest file name
// that the file systems of the local driver take.
const maxSegmentLength = 255

// maxKeyLength is the longest key: S3 takes keys of up to 1,024 bytes.
const maxKeyLength = 1024

// CheckKey returns an error unless key is a key of a blob: at most 1,024
// bytes of segments joined by slashes, each of 1 to 255 ASCII letters,
// digits, dots, hyphens, and underscores, and none starting with a dot.
func CheckKey(key string) error {
	if key == "" || len(key) > maxKeyLength {
		return fmt.Errorf("blob: a key has 1 to %d bytes", maxKeyLength)
	}
	return checkSegments(key)
}

// checkPrefix returns an error unless prefix is "" or segments of a key,
// each followed by a slash.
func checkPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	trimmed, ok := strings.CutSuffix(prefix, "/")
	if !ok {
		return errors.New("blob: a prefix ends with a slash")
	}
	if err := CheckKey(trimmed); err != nil {
		return fmt.Errorf("blob: the prefix is not segments of a key: %w", err)
	}
	return nil
}

func checkSegments(key string) error {
	for segment := range strings.SplitSeq(key, "/") {
		switch {
		case segment == "" || len(segment) > maxSegmentLength:
			return fmt.Errorf("blob: a segment of a key has 1 to %d bytes", maxSegmentLength)
		case segment[0] == '.':
			return errors.New("blob: a segment of a key does not start with a dot")
		}
		for _, r := range segment {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' ||
				r == '_') {
				return errors.New("blob: a segment of a key has only ASCII letters, digits, dots, hyphens, and " +
					"underscores")
			}
		}
	}
	return nil
}
