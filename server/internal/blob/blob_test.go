package blob_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/blob/blobtest"
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
	if err := store.List(ctx, "w2/", func(i blob.Info) error {
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
