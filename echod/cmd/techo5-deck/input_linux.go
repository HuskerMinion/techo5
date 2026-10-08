//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Keys on Linux go through a virtual keyboard made with uinput, the kernel's own way for a program
// to be an input device: it works the same under X11 and Wayland, and the desktop sees an ordinary
// keyboard. Opening /dev/uinput needs the user in a group the udev rule gives it to (docs/deck.md).

const (
	evSyn = 0x00
	evKey = 0x01

	uiDevCreate = 0x5501
	uiDevSetup  = 0x405c5503 // _IOW('U', 3, struct uinput_setup)
	uiSetEvBit  = 0x40045564 // _IOW('U', 100, int)
	uiSetKeyBit = 0x40045565 // _IOW('U', 101, int)
	busVirtual  = 0x06

	// keyGap is the pause between a key's press and its release, and between keys: desktops drop
	// presses that come faster than they poll.
	keyGap = 6 * time.Millisecond
)

type uinputSetup struct {
	bustype, vendor, product, version uint16
	name                              [80]byte
	ffEffectsMax                      uint32
}

// inputEvent is struct input_event: a timeval of two longs, then type, code and value.
type inputEvent struct {
	sec, usec int
	typ, code uint16
	value     int32
}

var keyboard struct {
	sync.Mutex
	f *os.File
}

// openKeyboard makes the virtual keyboard the first time it is needed and keeps it.
func openKeyboard() (*os.File, error) {
	keyboard.Lock()
	defer keyboard.Unlock()
	if keyboard.f != nil {
		return keyboard.f, nil
	}
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, errors.New("not allowed to make a keyboard: see the Linux part of the Deck guide (the uinput group)")
		}
		return nil, fmt.Errorf("no virtual keyboard: %w", err)
	}
	fd := f.Fd()
	ioctl := func(req, arg uintptr) error {
		if _, _, e := unix.Syscall(unix.SYS_IOCTL, fd, req, arg); e != 0 {
			return e
		}
		return nil
	}
	if err := ioctl(uiSetEvBit, evKey); err != nil {
		f.Close()
		return nil, err
	}
	_ = ioctl(uiSetEvBit, evSyn)
	for code := uintptr(1); code < 256; code++ {
		_ = ioctl(uiSetKeyBit, code)
	}
	setup := uinputSetup{bustype: busVirtual, vendor: 0x7e5, product: 0xdec, version: 1}
	copy(setup.name[:], "TECHO5 Deck")
	if err := ioctl(uiDevSetup, uintptr(unsafe.Pointer(&setup))); err != nil {
		f.Close()
		return nil, fmt.Errorf("setting the keyboard up: %w", err)
	}
	if err := ioctl(uiDevCreate, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("making the keyboard: %w", err)
	}
	// The desktop needs a moment to notice a new keyboard before its first key.
	time.Sleep(300 * time.Millisecond)
	keyboard.f = f
	return f, nil
}

// dropKeyboard forgets a keyboard that failed a write, so the next press makes a new one.
func dropKeyboard(f *os.File) {
	keyboard.Lock()
	if keyboard.f == f {
		keyboard.f = nil
		f.Close()
	}
	keyboard.Unlock()
}

func emit(f *os.File, typ, code uint16, value int32) error {
	ev := inputEvent{typ: typ, code: code, value: value}
	b := unsafe.Slice((*byte)(unsafe.Pointer(&ev)), unsafe.Sizeof(ev))
	_, err := f.Write(b)
	return err
}

func key(f *os.File, code uint16, down bool) error {
	v := int32(0)
	if down {
		v = 1
	}
	if err := emit(f, evKey, code, v); err != nil {
		return err
	}
	if err := emit(f, evSyn, 0, 0); err != nil {
		return err
	}
	time.Sleep(keyGap)
	return nil
}

// strokes presses each group of keys together and lets them go in reverse: every key that went down
// is let go, whatever fails, so nothing is left held.
func strokes(groups [][]uint16) error {
	f, err := openKeyboard()
	if err != nil {
		return err
	}
	var first error
	for _, g := range groups {
		var down []uint16
		for _, code := range g {
			if err := key(f, code, true); err != nil {
				first = err
				break
			}
			down = append(down, code)
		}
		for i := len(down) - 1; i >= 0; i-- {
			if err := key(f, down[i], false); err != nil && first == nil {
				first = err
			}
		}
		if first != nil {
			break
		}
	}
	if first != nil {
		dropKeyboard(f)
		return fmt.Errorf("the virtual keyboard failed: %w", first)
	}
	return nil
}

func canPressKeys() bool {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func pressCombo(c combo) error {
	var g []uint16
	for _, m := range c.mods {
		code, err := linuxKey(m)
		if err != nil {
			return err
		}
		g = append(g, code)
	}
	if c.key != "" {
		code, err := linuxKey(c.key)
		if err != nil {
			return err
		}
		g = append(g, code)
	}
	return strokes([][]uint16{g})
}

// typeGroups is how s is typed: each character as its key, with shift held where it needs it.
func typeGroups(s string) ([][]uint16, error) {
	shift, _ := linuxKey("shift")
	var groups [][]uint16
	for _, r := range s {
		if r == '\r' {
			continue
		}
		k, shifted, err := usKeystroke(r)
		if err != nil {
			return nil, err
		}
		code, err := linuxKey(k)
		if err != nil {
			return nil, err
		}
		if shifted {
			groups = append(groups, []uint16{shift, code})
		} else {
			groups = append(groups, []uint16{code})
		}
	}
	return groups, nil
}

// checkType says whether all of s can be typed, and the keyboard made, before any of it is typed.
func checkType(s string) error {
	if _, err := typeGroups(s); err != nil {
		return err
	}
	_, err := openKeyboard()
	return err
}

func typeText(s string) error {
	groups, err := typeGroups(s)
	if err != nil {
		return err
	}
	return strokes(groups)
}
