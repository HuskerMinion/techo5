//go:build !linux

package rfcomm

import (
	"errors"
	"os"
	"time"
)

// Dial is only implemented where the kernel has Bluetooth Classic sockets.
func Dial(mac string, channel byte, timeout time.Duration) (*os.File, error) {
	return nil, errors.New("rfcomm: Bluetooth Classic is only supported on Linux")
}
