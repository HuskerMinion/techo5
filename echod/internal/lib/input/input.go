// Package input reads Linux evdev devices directly, with no cgo and no getevent.
package input

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// Event types we care about.
const (
	EvSyn = 0x00
	EvKey = 0x01
	EvRel = 0x02
	EvAbs = 0x03
	EvSw  = 0x05
	EvRep = 0x14
)

// sizeof(struct input_event): a timeval of two kernel longs, then u16 type, u16 code, s32 value,
// with the tail padded back out to the timeval's alignment. evdev rejects a read shorter than one
// whole event with EINVAL rather than returning a truncated one, so this has to be right.
const (
	longSize  = strconv.IntSize / 8
	eventSize = 2*longSize + 8

	evType  = 2 * longSize
	evCode  = evType + 2
	evValue = evCode + 2
)

// Event is one evdev record. The timestamp is kept at the width the kernel reports it.
type Event struct {
	Sec, Usec uint64
	Type      uint16
	Code      uint16
	Value     int32
}

func (e Event) TypeName() string {
	switch e.Type {
	case EvSyn:
		return "SYN"
	case EvKey:
		return "KEY"
	case EvRel:
		return "REL"
	case EvAbs:
		return "ABS"
	case EvSw:
		return "SW"
	case EvRep:
		return "REP"
	}
	return fmt.Sprintf("TYPE_%d", e.Type)
}

func (e Event) CodeName() string {
	if e.Type != EvKey {
		return fmt.Sprintf("%d", e.Code)
	}
	return fmt.Sprintf("KEY_%d", e.Code)
}

func (e Event) String() string {
	action := ""
	if e.Type == EvKey {
		switch e.Value {
		case 0:
			action = " release"
		case 1:
			action = " press"
		case 2:
			action = " repeat"
		}
	}
	return fmt.Sprintf("%-4s %-16s value=%d%s", e.TypeName(), e.CodeName(), e.Value, action)
}

// Device is an open evdev node.
type Device struct {
	Path string
	Name string
	f    *os.File

	closeOnce sync.Once
	closeErr  error
}

// Open opens one event node and reads its reported name.
func Open(path string) (*Device, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &Device{Path: path, Name: nameFor(path), f: f}, nil
}

// nameFor reads the device name from sysfs, which avoids an EVIOCGNAME ioctl.
func nameFor(path string) string {
	base := filepath.Base(path)
	b, err := os.ReadFile("/sys/class/input/" + base + "/device/name")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}

// Read blocks for the next event.
func (d *Device) Read() (Event, error) {
	var buf [eventSize]byte
	if _, err := readFull(d.f, buf[:]); err != nil {
		return Event{}, err
	}
	return Event{
		Sec:   word(buf[0:]),
		Usec:  word(buf[longSize:]),
		Type:  binary.LittleEndian.Uint16(buf[evType:]),
		Code:  binary.LittleEndian.Uint16(buf[evCode:]),
		Value: int32(binary.LittleEndian.Uint32(buf[evValue:])),
	}, nil
}

// word reads a kernel long, which is what the timestamp is made of.
func word(b []byte) uint64 {
	if longSize == 4 {
		return uint64(binary.LittleEndian.Uint32(b))
	}
	return binary.LittleEndian.Uint64(b)
}

// Close releases the node. Closing it twice is expected rather than a failure: a service ends a
// blocking read by closing the node from the side (the Run methods in hardware/touch and the other
// input services do that), and the supervisor then closes the service on its way out. The first
// answer is kept and returned again, so a real close failure is still reported and the second,
// expected one is not.
func (d *Device) Close() error {
	d.closeOnce.Do(func() { d.closeErr = d.f.Close() })
	return d.closeErr
}

func readFull(f *os.File, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := f.Read(b[n:])
		if err != nil {
			return n, err
		}
		n += m
	}
	return n, nil
}

// List returns every /dev/input/event* node with its name.
func List() ([]*Device, error) {
	paths, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		return nil, err
	}
	var out []*Device
	for _, p := range paths {
		d, err := Open(p)
		if err != nil {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// AbsInfo is what the kernel reports about one absolute axis: its current value and its range.
type AbsInfo struct {
	Value, Min, Max, Fuzz, Flat, Resolution int32
}

// Abs queries an absolute axis with EVIOCGABS, for a touchscreen's coordinate ranges.
func (d *Device) Abs(code uint16) (AbsInfo, error) {
	var info AbsInfo
	// _IOR('E', 0x40 + code, struct input_absinfo): 24 bytes, read direction.
	req := uintptr(0x80184540 + uint32(code))
	// Through the raw connection, not File.Fd(): Fd() puts the descriptor into blocking mode and
	// takes it out of the runtime's poller, after which Close no longer wakes a blocked Read — a
	// reader waiting for a touch that never comes would hang the shutdown.
	rc, err := d.f.SyscallConn()
	if err != nil {
		return info, fmt.Errorf("input: %s: %w", d.Path, err)
	}
	var errno syscall.Errno
	if err := rc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(&info)))
	}); err != nil {
		return info, fmt.Errorf("input: %s: %w", d.Path, err)
	}
	if errno != 0 {
		return info, fmt.Errorf("input: EVIOCGABS %#x on %s: %w", code, d.Path, errno)
	}
	return info, nil
}

// Switch reads a switch's state now, with EVIOCGSW, rather than waiting for it to change.
//
// A switch is not a button: it reports a position that is already true when the daemon starts, and
// the kernel sends nothing until it moves. A camera shutter that was closed before the daemon came
// up would otherwise read as open until somebody touched it.
//
// The ioctl answers with a bitmap of every switch the device has, one bit per code, so the reply is
// sized to hold the code being asked about and the bit picked out of it.
func (d *Device) Switch(code uint16) (bool, error) {
	bytes := int(code)/8 + 1
	state := make([]byte, bytes)
	// _IOR('E', 0x1b, len): the length travels in the request's size field.
	req := uintptr(0x8000451b | uint32(bytes)<<16)
	// Through the raw connection, for the reason Abs gives.
	rc, err := d.f.SyscallConn()
	if err != nil {
		return false, fmt.Errorf("input: %s: %w", d.Path, err)
	}
	var errno syscall.Errno
	if err := rc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(&state[0])))
	}); err != nil {
		return false, fmt.Errorf("input: %s: %w", d.Path, err)
	}
	if errno != 0 {
		return false, fmt.Errorf("input: EVIOCGSW %#x on %s: %w", code, d.Path, errno)
	}
	return state[code/8]&(1<<(code%8)) != 0, nil
}

// Find opens the one event node whose reported name matches, leaving every other node closed.
func Find(name string) (*Device, error) {
	paths, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		if nameFor(p) != name {
			continue
		}
		return Open(p)
	}
	return nil, fmt.Errorf("input: no device named %q", name)
}
