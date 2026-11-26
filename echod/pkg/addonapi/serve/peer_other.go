//go:build !linux

package serve

import (
	"errors"
	"net"
)

// peerUID needs the kernel's record of who connected, which only Linux's SO_PEERCRED gives here: on
// anything else every connection is refused, so the server never runs on trust.
func peerUID(net.Conn) (uint32, error) {
	return 0, errors.New("the peer's user cannot be read on this system")
}
