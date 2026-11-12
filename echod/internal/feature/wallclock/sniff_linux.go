//go:build linux

package wallclock

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The clock holds a Bluetooth Classic link for as long as it is connected. Left alone that link
// stays in active mode, with the clock's radio listening in every slot, which is most of its
// battery drain. The kernel never asks for sniff mode by itself, so this does: one HCI command
// after the link is up and again after any traffic takes it out.
const (
	hciCommandPkt  = 0x01
	hciEventPkt    = 0x04
	evtCmdComplete = 0x0E
	evtCmdStatus   = 0x0F

	// opSniffMode is HCI_Sniff_Mode: OGF 0x02 (Link Policy), OCF 0x0003. OGF 0x01 is Link
	// Control, where 0x0403 is Periodic Inquiry and the controller rejects it with Invalid HCI
	// Command Parameters.
	opSniffMode = 0x0803

	// hciGetConnList is HCIGETCONNLIST, _IOR('H', 212, int).
	hciGetConnList = 0x800448D4

	solHCI    = 0
	hciFilter = 2

	// sniffMaxInterval and sniffMinInterval are 2048 slots, 1.28 seconds. Attempt and timeout
	// are the controller's own defaults (Linux uses attempt 4, timeout 1); four and two are
	// accepted by every controller seen here.
	sniffMaxInterval = 2048
	sniffMinInterval = 2048
	sniffAttempt     = 4
	sniffTimeout     = 2

	// sniffReadTimeout bounds one whole HCI command, not one read: the socket filter admits a
	// stream of events, so a per-read timeout alone would never fire on a busy adapter.
	sniffReadTimeout = 500 * time.Millisecond

	// hciConnMax is how many ACL links the connection list can hold on one controller.
	hciConnMax = 16

	// hciLMMaster is the link-mode bit that says this device initiated the ACL and may ask for
	// sniff mode. A slave cannot, and Command Disallowed would otherwise look like success.
	hciLMMaster = 0x0001

	// hciEventType is the HCI filter's bit for event packets; the command complete and status
	// event bits are the only events wanted. A raw socket otherwise passes ACL and LE traffic,
	// which can keep a read loop alive forever.
	hciEventType   = uint32(1) << hciEventPkt
	hciCmdComplete = uint32(1) << evtCmdComplete
	hciCmdStatus   = uint32(1) << evtCmdStatus
)

type hciConnInfo struct {
	Handle   uint16
	Addr     [6]byte
	Type     uint8
	Out      uint8
	State    uint16
	LinkMode uint32
}

type hciConnList struct {
	Dev     uint16
	ConnNum uint16
	Conn    [hciConnMax]hciConnInfo
}

// sniffClock asks the controller for sniff mode on the ACL link to address, if there is one. Command
// Disallowed is the usual answer when the link is already in sniff mode and is treated as success.
// A link this device is not master of is reported, so the first failure is visible in the log.
func sniffClock(ctx context.Context, address string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	want, err := hciAddress(address)
	if err != nil {
		return err
	}

	fd, err := openHCI()
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	handle, err := handleFor(fd, want)
	if err != nil {
		return err
	}
	status, err := hciCommand(ctx, fd, opSniffMode, sniffParams(handle))
	if err != nil {
		return err
	}
	return sniffResult(status)
}

// hciAddress parses a colon-separated Bluetooth address into the kernel's little-endian order.
func hciAddress(address string) ([6]byte, error) {
	var want [6]byte
	hw, err := net.ParseMAC(address)
	if err != nil || len(hw) != 6 {
		return want, fmt.Errorf("%q is not a Bluetooth address", address)
	}
	for i := range want {
		want[5-i] = hw[i]
	}
	return want, nil
}

// openHCI is a raw HCI socket on hci0. A raw socket starts with a filter that drops everything, so
// the filter is narrowed to the two command-reply events the feature needs.
func openHCI() (int, error) {
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.BTPROTO_HCI)
	if err != nil {
		return -1, err
	}
	if err := unix.Bind(fd, &unix.SockaddrHCI{Dev: 0, Channel: unix.HCI_CHANNEL_RAW}); err != nil {
		unix.Close(fd)
		return -1, err
	}
	filter := struct {
		TypeMask  uint32
		EventMask [2]uint32
		Opcode    uint16
	}{
		TypeMask:  hciEventType,
		EventMask: [2]uint32{hciCmdComplete | hciCmdStatus, 0},
	}
	if _, _, errno := unix.Syscall6(unix.SYS_SETSOCKOPT, uintptr(fd), solHCI, hciFilter,
		uintptr(unsafe.Pointer(&filter)), unsafe.Sizeof(filter), 0); errno != 0 {
		unix.Close(fd)
		return -1, errno
	}
	tv := unix.NsecToTimeval(sniffReadTimeout.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

// handleFor is the ACL handle of the link to want, from the controller's connection list.
func handleFor(fd int, want [6]byte) (uint16, error) {
	req := hciConnList{Dev: 0, ConnNum: hciConnMax}
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), hciGetConnList,
		uintptr(unsafe.Pointer(&req))); errno != 0 {
		return 0, errno
	}
	n := int(req.ConnNum)
	if n > len(req.Conn) {
		n = len(req.Conn)
	}
	for _, c := range req.Conn[:n] {
		if c.Addr == want {
			if c.LinkMode&hciLMMaster == 0 {
				return 0, fmt.Errorf("the clock's link is not master")
			}
			return c.Handle, nil
		}
	}
	return 0, fmt.Errorf("no Bluetooth link to %s", addressString(want))
}

// sniffParams is HCI_Sniff_Mode's parameters: handle, max interval, min interval, attempt, timeout.
// The supervision timeout is not one of them; sending an extra field makes the controller log an
// invalid packet size.
func sniffParams(handle uint16) []byte {
	p := make([]byte, 10)
	binary.LittleEndian.PutUint16(p[0:], handle)
	binary.LittleEndian.PutUint16(p[2:], sniffMaxInterval)
	binary.LittleEndian.PutUint16(p[4:], sniffMinInterval)
	binary.LittleEndian.PutUint16(p[6:], sniffAttempt)
	binary.LittleEndian.PutUint16(p[8:], sniffTimeout)
	return p
}

// hciCommand sends one command and waits for its reply. The filter admits only command replies, so
// unrelated traffic cannot keep the loop alive; the deadline is a whole-call bound.
func hciCommand(ctx context.Context, fd int, opcode uint16, params []byte) (uint8, error) {
	pkt := make([]byte, 4+len(params))
	pkt[0] = hciCommandPkt
	binary.LittleEndian.PutUint16(pkt[1:], opcode)
	pkt[3] = byte(len(params))
	copy(pkt[4:], params)
	if _, err := unix.Write(fd, pkt); err != nil {
		return 0, err
	}

	deadline := time.Now().Add(sniffReadTimeout)
	buf := make([]byte, 260)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if !time.Now().Before(deadline) {
			return 0, fmt.Errorf("no reply to HCI command 0x%04X", opcode)
		}
		n, err := unix.Read(fd, buf)
		if err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				return 0, fmt.Errorf("no reply to HCI command 0x%04X", opcode)
			}
			return 0, err
		}
		if status, ok := decodeCommandReply(buf[:n], opcode); ok {
			return status, nil
		}
	}
}

// decodeCommandReply is the HCI reply parser: Command Complete carries ncmd, opcode and status;
// Command Status carries status, ncmd and opcode. Both are four bytes.
func decodeCommandReply(pkt []byte, opcode uint16) (uint8, bool) {
	if len(pkt) < 3 || pkt[0] != hciEventPkt {
		return 0, false
	}
	ev, plen := pkt[1], int(pkt[2])
	if 3+plen > len(pkt) || plen < 4 {
		return 0, false
	}
	body := pkt[3 : 3+plen]
	switch ev {
	case evtCmdComplete:
		if binary.LittleEndian.Uint16(body[1:]) != opcode {
			return 0, false
		}
		return body[3], true
	case evtCmdStatus:
		if binary.LittleEndian.Uint16(body[2:]) != opcode {
			return 0, false
		}
		return body[0], true
	}
	return 0, false
}

// addressString is the display form of the kernel's little-endian address.
func addressString(a [6]byte) string {
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", a[5], a[4], a[3], a[2], a[1], a[0])
}
