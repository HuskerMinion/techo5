// Package rfcomm opens a Bluetooth Classic RFCOMM connection to a device. It is
// the kernel's own Bluetooth stack, the one BlueZ also drives, rather than a
// second one alongside it.
package rfcomm

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// Bluetooth constants from <bluetooth/bluetooth.h> and <bluetooth/rfcomm.h>.
const (
	afBluetooth      = 31
	btProtoRFCOMM    = 3
	solBluetooth     = 274
	btSecurity       = 4
	btSecurityMedium = 2
)

// Dial opens an encrypted RFCOMM connection to mac on channel.
//
// The Echo Wall Clock only accepts an encrypted link, so BT_SECURITY_MEDIUM is
// set before connecting, exactly as a real Echo's stack does.
//
// The socket is non-blocking for the connect itself: when the clock is out of
// range the kernel keeps paging for the page timeout, and a connect abandoned
// by a plain timeout stays pending in the kernel, making every later attempt
// fail with EBUSY/EALREADY. Polling for writability lets a timeout close the
// socket, which cancels the pending connect cleanly.
func Dial(mac string, channel byte, timeout time.Duration) (*os.File, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return nil, fmt.Errorf("parse address %q: %w", mac, err)
	}
	if len(hw) != 6 {
		return nil, fmt.Errorf("parse address %q: want 6 bytes, got %d", mac, len(hw))
	}

	fd, err := syscall.Socket(afBluetooth,
		syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC|syscall.SOCK_NONBLOCK, btProtoRFCOMM)
	if err != nil {
		return nil, fmt.Errorf("rfcomm socket: %w", err)
	}
	closeFD := true
	defer func() {
		if closeFD {
			_ = syscall.Close(fd)
		}
	}()

	// struct bt_security { uint8 level; uint8 key_size; } — a byte array, not a string: the kernel
	// takes exactly the two bytes, and SetsockoptString would obscure that the payload is binary.
	sec := []byte{btSecurityMedium, 0}
	_, _, secErr := syscall.Syscall6(syscall.SYS_SETSOCKOPT,
		uintptr(fd), solBluetooth, btSecurity,
		uintptr(unsafe.Pointer(&sec[0])), uintptr(len(sec)), 0)
	runtime.KeepAlive(sec)
	if secErr != 0 {
		return nil, fmt.Errorf("set RFCOMM security: %w", secErr)
	}

	// struct sockaddr_rc { sa_family_t rc_family; bdaddr_t rc_bdaddr; uint8_t rc_channel; }
	// bdaddr_t holds the address little-endian, i.e. the last octet first.
	sa := make([]byte, 10)
	sa[0] = afBluetooth
	sa[1] = 0
	for i := 0; i < 6; i++ {
		sa[2+i] = hw[5-i]
	}
	sa[8] = channel

	_, _, errno := syscall.Syscall(
		syscall.SYS_CONNECT,
		uintptr(fd),
		uintptr(unsafe.Pointer(&sa[0])),
		uintptr(len(sa)),
	)
	runtime.KeepAlive(&sa)

	switch errno {
	case 0:
		// Connected without waiting.
	case syscall.EINPROGRESS, syscall.EAGAIN:
		if err := waitWritable(fd, timeout); err != nil {
			return nil, fmt.Errorf("connect %s channel %d: %w", mac, channel, err)
		}
		soErr, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_ERROR)
		if err != nil {
			return nil, fmt.Errorf("connect %s channel %d: read socket error: %w", mac, channel, err)
		}
		if soErr != 0 {
			return nil, fmt.Errorf("connect %s channel %d: %w", mac, channel, syscall.Errno(soErr))
		}
	default:
		return nil, fmt.Errorf("connect %s channel %d: %w", mac, channel, errno)
	}

	// The read loop and the frame writer want normal blocking semantics.
	if err := syscall.SetNonblock(fd, false); err != nil {
		return nil, fmt.Errorf("connect %s: make socket blocking: %w", mac, err)
	}

	file := os.NewFile(uintptr(fd), "rfcomm:"+mac)
	if file == nil {
		return nil, fmt.Errorf("connect %s: could not wrap socket", mac)
	}
	closeFD = false
	return file, nil
}

// waitWritable waits until the socket finishes connecting (or fails) and
// returns nil as soon as it becomes writable; the caller then reads SO_ERROR to
// tell success from failure.
func waitWritable(fd int, timeout time.Duration) error {
	epfd, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if err != nil {
		return fmt.Errorf("epoll: %w", err)
	}
	defer func() { _ = syscall.Close(epfd) }()

	event := &syscall.EpollEvent{Events: syscall.EPOLLOUT, Fd: int32(fd)}
	if err := syscall.EpollCtl(epfd, syscall.EPOLL_CTL_ADD, fd, event); err != nil {
		return fmt.Errorf("epoll add: %w", err)
	}

	deadline := time.Now().Add(timeout)
	events := make([]syscall.EpollEvent, 1)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("timed out after %s", timeout)
		}
		ms := int(remaining / time.Millisecond)
		if ms < 1 {
			ms = 1
		}
		n, err := syscall.EpollWait(epfd, events, ms)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return fmt.Errorf("epoll wait: %w", err)
		}
		if n > 0 {
			return nil
		}
	}
}
