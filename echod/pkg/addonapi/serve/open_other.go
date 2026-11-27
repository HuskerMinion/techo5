//go:build !linux

package serve

import (
	"errors"
	"os"
)

// openNoFollow opens path for reading, refusing a symbolic link. The server runs on Linux only
// (peerUID); elsewhere the link is looked for first, which is enough for the tests.
func openNoFollow(path string) (*os.File, error) {
	if fi, err := os.Lstat(path); err != nil {
		return nil, err
	} else if fi.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("a symbolic link is not read")
	}
	return os.Open(path)
}
