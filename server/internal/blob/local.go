package blob

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"
)

// Local is the local driver: it keeps each blob as a file of a directory,
// at the path of its key, through an os.Root, so that nothing it does
// reaches outside the directory.
type Local struct {
	root *os.Root
}

var _ Store = (*Local)(nil)

// The permissions of what the local driver creates: the server's user alone
// writes, and its group reads, so that a backup that runs as the group may.
const (
	dirPerm  = 0o750
	filePerm = 0o640
)

// tempPrefix starts the name of a file that Put is writing, which no key
// names, because a key's segment never starts with a dot (CheckKey).
const tempPrefix = ".put-"

// OpenLocal returns a local driver that keeps its blobs in the directory
// dir, which it creates, with its parents, if it does not exist. Close
// releases it.
func OpenLocal(dir string) (*Local, error) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("blob: create the directory of the local driver: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("blob: open the directory of the local driver: %w", err)
	}
	return &Local{root: root}, nil
}

// Close releases the directory.
func (l *Local) Close() error {
	return l.root.Close()
}

// Put writes data to a new file next to the key's, syncs it, and renames it
// to the key's, which replaces the old file at once, so that a reader opens
// either file whole.
func (l *Local) Put(ctx context.Context, key string, data []byte, _ string) (err error) {
	if err := CheckKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := path.Dir(key)
	if err := l.root.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("blob: create the directory of %s: %w", key, err)
	}
	tmp := path.Join(dir, tempPrefix+rand.Text())
	f, err := l.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		return fmt.Errorf("blob: create a file for %s: %w", key, err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = l.root.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return fmt.Errorf("blob: write %s: %w", key, err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("blob: write %s: %w", key, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("blob: write %s: %w", key, err)
	}
	if err = l.root.Rename(tmp, key); err != nil {
		return fmt.Errorf("blob: store %s: %w", key, err)
	}
	return nil
}

// Open opens the key's file.
func (l *Local) Open(ctx context.Context, key string) (io.ReadCloser, Info, error) {
	if err := CheckKey(key); err != nil {
		return nil, Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, Info{}, err
	}
	f, err := l.root.Open(key)
	if err != nil {
		return nil, Info{}, localError(key, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, Info{}, localError(key, err)
	}
	if !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, Info{}, ErrNotFound
	}
	return f, Info{Key: key, Size: st.Size(), Modified: st.ModTime()}, nil
}

// Stat reads the Info of the key's file.
func (l *Local) Stat(ctx context.Context, key string) (Info, error) {
	if err := CheckKey(key); err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	st, err := l.root.Stat(key)
	if err != nil {
		return Info{}, localError(key, err)
	}
	if !st.Mode().IsRegular() {
		return Info{}, ErrNotFound
	}
	return Info{Key: key, Size: st.Size(), Modified: st.ModTime()}, nil
}

// Delete removes the key's file.
func (l *Local) Delete(ctx context.Context, key string) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := l.root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("blob: delete %s: %w", key, err)
	}
	return nil
}

// List walks the directory of prefix, in lexical order, which is the order
// of the keys, and skips the files that Put is writing.
func (l *Local) List(ctx context.Context, prefix string, fn func(Info) error) error {
	if err := checkPrefix(prefix); err != nil {
		return err
	}
	start := strings.TrimSuffix(prefix, "/")
	if start == "" {
		start = "."
	}
	err := fs.WalkDir(l.root.FS(), start, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil && p == start && errors.Is(err, fs.ErrNotExist):
			return fs.SkipAll
		case err != nil:
			return err
		case ctx.Err() != nil:
			return ctx.Err()
		case p != start && strings.HasPrefix(d.Name(), "."):
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		case d.IsDir() || !d.Type().IsRegular():
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			// Deleted while the walk went on.
			return nil
		}
		if err != nil {
			return err
		}
		return fn(Info{Key: p, Size: info.Size(), Modified: info.ModTime()})
	})
	if err != nil {
		return fmt.Errorf("blob: list %s: %w", prefix, err)
	}
	return nil
}

// SignedURL returns false: the server serves the local driver's blobs
// itself, after it checks the user's access (FR-ASSET-01).
func (l *Local) SignedURL(context.Context, string, time.Duration, string, string) (string, bool, error) {
	return "", false, nil
}

// localError returns ErrNotFound for an error that says that the key's file
// does not exist, and the error, with the key, otherwise.
func localError(key string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	return fmt.Errorf("blob: read %s: %w", key, err)
}
