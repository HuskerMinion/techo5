// Package panelsock dials the TECHO5 Panel app's local sockets (@techo5.mic, @techo5.spk,
// @techo5.screen and @techo5.ctl) and makes sure it is the app that answers.
//
// They are abstract sockets: a name with no file behind it, so no permissions either, and any app on
// the tablet may listen on one first. One that did would be handed the microphone, would be what the
// speaker plays, and would draw the screen. The app starts the daemon as its own child, so both run as
// the app's own user id; a socket whose listener runs as anybody else is refused before a byte is
// read from it or written to it. The app checks the same from its side (LocalPeer.java in the app's
// repository): a caller with another user id is closed.
package panelsock

import (
	"errors"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// ErrNotTheApp is a socket whose listener runs as another user: not the app, so not to be used.
var ErrNotTheApp = errors.New("panelsock: the socket is not the app's")

// Dial connects to the abstract socket name (with its leading @) and returns the connection only when
// the listener runs as this process's own user, which is the app's.
func Dial(name string) (net.Conn, error) {
	return dialAs(name, os.Getuid())
}

// dialAs is Dial for a listener that must run as uid; a test asks for another.
func dialAs(name string, uid int) (net.Conn, error) {
	c, err := net.Dial("unix", name)
	if err != nil {
		return nil, err
	}
	peer, err := peerUID(c)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("panelsock: %s: reading who listens: %w", name, err)
	}
	if peer != uid {
		_ = c.Close()
		return nil, fmt.Errorf("%w: %s is held by uid %d, and the app is %d", ErrNotTheApp, name, peer, uid)
	}
	return c, nil
}

// peerUID is the user the other end of a local socket ran as when it was set up (SO_PEERCRED): for a
// connection dialed here, the listener's.
func peerUID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1, errors.New("not a local socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if credErr != nil {
		return -1, credErr
	}
	return int(cred.Uid), nil
}
