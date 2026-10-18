//go:build !windows

package blob

import "os"

// syncDir syncs the directory dir of root, so that the names that it holds,
// as a file's that a rename gave it, survive a crash of the host, which the
// file's own Sync does not make them.
func syncDir(root *os.Root, dir string) error {
	d, err := root.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
