//go:build linux

package serve

import (
	"os"
	"syscall"
)

// openNoFollow opens path for reading, refusing it when its last part is a symbolic link: an add-on's
// file is read where the catalog put it, never wherever a link it made points. Without blocking, so a
// FIFO in its place is opened at once (and then refused as no regular file) rather than hanging.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
