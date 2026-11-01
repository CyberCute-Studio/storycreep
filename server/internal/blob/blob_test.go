package blob_test

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/blob/blobtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// TestMain stops MinIO, if the tests started it, once they are done.
func TestMain(m *testing.M) {
	code := m.Run()
	blobtest.Stop()
	os.Exit(code)
}

// TestLocal runs the shared suite on the local driver.
//
// Verifies: FR-ASSET-01/AC1
func TestLocal(t *testing.T) {
	store, err := blob.OpenLocal(filepath.Join(t.TempDir(), "assets"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	blobtest.Suite(t, store)
}

// TestS3 runs the shared suite on the S3 driver, against MinIO.
//
// Verifies: FR-ASSET-01/AC1
func TestS3(t *testing.T) {
	blobtest.Suite(t, blob.OpenS3(blobtest.S3(t), blob.S3Options{}))
}

// TestS3Forbidden checks that the S3 driver does not take an object that the
// bucket refuses for one that does not exist, and that its error says that
// the keys must be allowed to list the bucket, without which Amazon S3
// refuses a request of an object that does not exist, rather than answer
// that it does not.
func TestS3Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	store := blob.OpenS3(config.S3{Host: u.Hostname(), Port: port, Security: config.S3None, Bucket: "assets",
		Region: "us-east-1", AccessKeyID: "key", SecretAccessKey: secret.New("secret")}, blob.S3Options{})
	if _, err := store.Stat(context.Background(), "ws/a.png"); err == nil || errors.Is(err, blob.ErrNotFound) ||
		!strings.Contains(err.Error(), "may not list the bucket") {
		t.Errorf("Stat of an object that the bucket refuses: %v", err)
	}
}

// TestLocalStaysInItsDirectory checks that the local driver reads, lists,
// and writes nothing outside its directory, even through a symbolic link
// inside it, and that it lists no file that Put is writing, and removes one
// that a Put left behind an hour ago, as when the process died while it
// wrote.
func TestLocalStaysInItsDirectory(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	dir := filepath.Join(parent, "assets")
	store, err := blob.OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.png"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "ws")); err != nil {
		t.Skipf("symbolic links are not available: %v", err)
	}
	if _, _, err := store.Open(ctx, "ws/secret.png"); err == nil {
		t.Error("Open follows a symbolic link out of the directory")
	}
	if err := store.Put(ctx, "ws/new.png", []byte("x"), "image/png"); err == nil {
		t.Error("Put writes through a symbolic link out of the directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "new.png")); err == nil {
		t.Error("Put wrote a file outside the directory")
	}

	if err := os.MkdirAll(filepath.Join(dir, "w2"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".put-unfinished", ".put-abandoned", "kept.png"} {
		if err := os.WriteFile(filepath.Join(dir, "w2", name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-61 * time.Minute)
	if err := os.Chtimes(filepath.Join(dir, "w2", ".put-abandoned"), old, old); err != nil {
		t.Fatal(err)
	}
	var keys []string
	if err := store.List(ctx, "w2/", "", func(i blob.Info) error {
		keys = append(keys, i.Key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(keys, []string{"w2/kept.png"}) {
		t.Errorf("the directory lists %q, want w2/kept.png alone", keys)
	}
	if _, err := os.Stat(filepath.Join(dir, "w2", ".put-unfinished")); err != nil {
		t.Errorf("List removed a file that Put may still be writing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "w2", ".put-abandoned")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("List kept a file that a Put left behind an hour ago: %v", err)
	}
}

// TestLocalListsNoFileAsDirectory checks that a listing of a prefix where a
// file stands in place of the directory fails on every system, as on Linux,
// rather than list nothing, as a prefix whose directory does not exist does,
// which is how Windows answers reading a file's entries, so that the orphan
// reconciliation counts such a workspace as failed (asset.TestReconcileOwner).
func TestLocalListsNoFileAsDirectory(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "assets")
	store, err := blob.OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := os.WriteFile(filepath.Join(dir, "ws"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	list := func(prefix string) error {
		return store.List(ctx, prefix, "", func(blob.Info) error { return nil })
	}
	if err := list("ws/"); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("listing a prefix whose directory is a file: %v, want an error that is not ErrNotExist", err)
	}
	if err := list("missing/"); err != nil {
		t.Errorf("listing a prefix whose directory does not exist: %v, want nothing listed", err)
	}
}

// TestCheckKey checks which keys a store takes.
func TestCheckKey(t *testing.T) {
	for _, key := range []string{"a", "ws/0123abc.png", "a/b/c.thumb.png", "A_b-c.d"} {
		if err := blob.CheckKey(key); err != nil {
			t.Errorf("CheckKey(%q): %v", key, err)
		}
	}
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'a'
	}
	for _, key := range []string{"", ".", "..", "a/..", "a/./b", "/a", "a/", "a//b", ".a", "a/.b", "a b", "a\\b",
		"č", string(long)} {
		if err := blob.CheckKey(key); err == nil {
			t.Errorf("CheckKey(%q) takes it", key)
		}
	}
}
