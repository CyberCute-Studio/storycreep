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
	"slices"
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
// either file whole, and then syncs the file's directory, and each directory
// above it, so that once Put returns, the file survives a crash of the host
// under its key, as the row of an asset that a transaction commits next
// expects.
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
	// The directory holds the file's new name, and its parent the
	// directory's, if MkdirAll created it.
	for d := dir; ; d = path.Dir(d) {
		if err = syncDir(l.root, d); err != nil {
			return fmt.Errorf("blob: store %s: sync its directory: %w", key, err)
		}
		if d == "." {
			return nil
		}
	}
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

// staleTemp is how long after it was last written a file that Put was
// writing is left behind, as when the process died while it wrote, which no
// Put still writes: Put writes a file of 50 MB at most, in moments.
const staleTemp = time.Hour

// List walks the directory of prefix, and each of its directories, in the
// order of the keys, which is not the order of the names of a directory's
// entries: a directory's keys follow its name and a slash, so the keys of a
// directory a follow those of a-1, because a hyphen sorts before a slash. It
// skips the files that Put is writing, and removes those that a Put left
// behind more than staleTemp ago, as when the process died while it wrote,
// which no key names, and nothing else would ever remove.
func (l *Local) List(ctx context.Context, prefix string, fn func(Info) error) error {
	if err := checkPrefix(prefix); err != nil {
		return err
	}
	start := strings.TrimSuffix(prefix, "/")
	if start == "" {
		start = "."
	}
	err := l.walk(ctx, start, true, fn)
	if err != nil {
		return fmt.Errorf("blob: list %s: %w", prefix, err)
	}
	return nil
}

// walk calls fn with the Info of each file of the directory dir, and of its
// directories, in the order of their keys (List). The directory of the
// prefix, which is top, may not exist, which lists nothing.
func (l *Local) walk(ctx context.Context, dir string, top bool, fn func(Info) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := fs.ReadDir(l.root.FS(), dir)
	switch {
	case top && errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	}
	// A directory sorts as its name and a slash would, which its keys start
	// with.
	sortName := func(e fs.DirEntry) string {
		if e.IsDir() {
			return e.Name() + "/"
		}
		return e.Name()
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(sortName(a), sortName(b)) })
	for _, e := range entries {
		p := path.Join(dir, e.Name())
		switch {
		case strings.HasPrefix(e.Name(), tempPrefix) && e.Type().IsRegular():
			l.removeStale(e, p)
			continue
		case strings.HasPrefix(e.Name(), "."):
			continue
		case e.IsDir():
			if err := l.walk(ctx, p, false, fn); err != nil {
				return err
			}
			continue
		case !e.Type().IsRegular():
			continue
		}
		info, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			// Deleted while the walk went on.
			continue
		}
		if err != nil {
			return err
		}
		if err := fn(Info{Key: p, Size: info.Size(), Modified: info.ModTime()}); err != nil {
			return err
		}
	}
	return nil
}

// removeStale removes the file e, at p, that Put was writing, if it was last
// written more than staleTemp ago. A file that cannot be removed now is left
// for the next List.
func (l *Local) removeStale(e fs.DirEntry, p string) {
	if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > staleTemp {
		_ = l.root.Remove(p)
	}
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
