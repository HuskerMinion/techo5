package ble

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// kernelDevice is present when the kernel's Bluetooth stack owns the controller: btbridge has put
// /dev/stpbt behind hci0 so bluetoothd and bluez-alsa can use it (the Echo Dot's and the Show's
// Bluetooth kernels). The node then has one owner already, and the proxy listens through a raw HCI
// socket on hci0 instead, alongside bluetoothd rather than in place of it.
const kernelDevice = "/sys/class/bluetooth/hci0"

// hciFilterOpt is HCI_FILTER from the kernel's hci.h (level SOL_HCI); x/sys/unix does not name it.
const hciFilterOpt = 2

func viaKernel() bool {
	_, err := os.Stat(kernelDevice)
	return err == nil
}

// hciFilter is what HCI_FILTER takes: the kernel copies it into its struct hci_ufilter, whose masks
// are __u32 on every architecture (BlueZ's lib/hci.h uses uint32_t the same way). The kernel's
// internal struct hci_filter has unsigned longs, but that is not the setsockopt ABI; uintptr fields
// here would be right on 32-bit builds only.
type hciFilter struct {
	typeMask  uint32
	eventMask [2]uint32
	opcode    uint16
}

// sizeof(struct hci_ufilter) is 16; this fails to compile if the layout above drifts from it.
var _ [0]struct{} = [unsafe.Sizeof(hciFilter{}) - 16]struct{}{}

// openKernel opens a raw socket on hci0 that sees every event. Packets on it carry the H4 type byte
// first, the same as the node, so everything after the open is shared. Reads time out after a second
// so Stop never waits on a quiet controller.
func openKernel() (int, error) {
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.BTPROTO_HCI)
	if err != nil {
		return -1, fmt.Errorf("ble: hci socket: %w", err)
	}
	all := ^uint32(0)
	filter := hciFilter{typeMask: 1 << h4Event, eventMask: [2]uint32{all, all}}
	if _, _, errno := unix.Syscall6(unix.SYS_SETSOCKOPT, uintptr(fd), unix.SOL_HCI, hciFilterOpt,
		uintptr(unsafe.Pointer(&filter)), unsafe.Sizeof(filter), 0); errno != 0 {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("ble: hci filter: %w", errno)
	}
	if err := unix.Bind(fd, &unix.SockaddrHCI{Dev: 0, Channel: unix.HCI_CHANNEL_RAW}); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("ble: binding hci0: %w", err)
	}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 1}); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("ble: hci read timeout: %w", err)
	}
	return fd, nil
}
