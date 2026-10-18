package blob

import "os"

// syncDir does nothing on Windows, which syncs no directory: FlushFileBuffers
// refuses the handle of a directory, which os opens for reading alone.
func syncDir(*os.Root, string) error {
	return nil
}
