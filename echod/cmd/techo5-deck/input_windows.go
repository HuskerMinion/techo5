//go:build windows

package main

import (
	"errors"
	"fmt"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32    = windows.NewLazySystemDLL("user32.dll")
	sendInput = user32.NewProc("SendInput")
)

const (
	inputKeyboard     = 1
	keyEventExtended  = 0x0001
	keyEventKeyUp     = 0x0002
	keyEventUnicode   = 0x0004
	maxInputsPerBatch = 64
)

// input is Windows' INPUT holding a KEYBDINPUT, laid out as it is on 64-bit Windows: the union is as
// big as its largest member, MOUSEINPUT.
type input struct {
	typ   uint32
	_     uint32
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	_     uint32
	extra uintptr
	_     [8]byte
}

// virtual-key codes for the generic names (keys.go).
var vkCodes = map[string]uint16{
	"ctrl": 0x11, "shift": 0x10, "alt": 0x12, "win": 0x5B,
	"enter": 0x0D, "esc": 0x1B, "tab": 0x09, "space": 0x20, "backspace": 0x08,
	"delete": 0x2E, "insert": 0x2D, "home": 0x24, "end": 0x23, "pageup": 0x21, "pagedown": 0x22,
	"up": 0x26, "down": 0x28, "left": 0x25, "right": 0x27, "printscreen": 0x2C,
	"minus": 0xBD, "equal": 0xBB, "comma": 0xBC, "period": 0xBE, "slash": 0xBF, "semicolon": 0xBA,
	"quote": 0xDE, "bracketleft": 0xDB, "bracketright": 0xDD, "backslash": 0xDC, "grave": 0xC0,
	"media_play_pause": 0xB3, "media_next": 0xB0, "media_previous": 0xB1, "media_stop": 0xB2,
	"volume_up": 0xAF, "volume_down": 0xAE, "volume_mute": 0xAD,
}

// extended are the keys Windows wants marked as extended, or they arrive as their number-pad twins.
var extended = map[string]bool{"delete": true, "insert": true, "home": true, "end": true, "pageup": true,
	"pagedown": true, "up": true, "down": true, "left": true, "right": true, "win": true}

func vk(name string) (uint16, bool) {
	if v, ok := vkCodes[name]; ok {
		return v, true
	}
	if len(name) == 1 {
		c := name[0]
		if c >= 'a' && c <= 'z' {
			return uint16(c - 'a' + 'A'), true
		}
		if c >= '0' && c <= '9' {
			return uint16(c), true
		}
	}
	var n int
	if _, err := fmt.Sscanf(name, "f%d", &n); err == nil && n >= 1 && n <= 24 {
		return uint16(0x70 + n - 1), true
	}
	return 0, false
}

func keyInput(name string, up bool) (input, error) {
	v, ok := vk(name)
	if !ok {
		return input{}, fmt.Errorf("no key %q on Windows", name)
	}
	in := input{typ: inputKeyboard, vk: v}
	if extended[name] {
		in.flags |= keyEventExtended
	}
	if up {
		in.flags |= keyEventKeyUp
	}
	return in, nil
}

func send(ins []input) error {
	for len(ins) > 0 {
		n := min(len(ins), maxInputsPerBatch)
		sent, _, err := sendInput.Call(uintptr(n), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(ins[0]))
		if int(sent) != n {
			// Windows refuses input into a window running as administrator from a program that
			// isn't, and while the screen is locked.
			return fmt.Errorf("only %d of %d key events went through (%v): is the window in front running as administrator, or the screen locked?", sent, n, err)
		}
		ins = ins[n:]
	}
	return nil
}

func canPressKeys() bool { return sendInput.Find() == nil }

// checkType: every character goes as itself, so any text can be typed.
func checkType(string) error { return nil }

// pressCombo holds the modifiers, presses and lets go of the key, then lets go of the modifiers in
// reverse order.
func pressCombo(c combo) error {
	var ins []input
	for _, m := range c.mods {
		in, err := keyInput(m, false)
		if err != nil {
			return err
		}
		ins = append(ins, in)
	}
	if c.key != "" {
		down, err := keyInput(c.key, false)
		if err != nil {
			return err
		}
		up, _ := keyInput(c.key, true)
		ins = append(ins, down, up)
	}
	for i := len(c.mods) - 1; i >= 0; i-- {
		in, _ := keyInput(c.mods[i], true)
		ins = append(ins, in)
	}
	return send(ins)
}

// typeText types s as it is, whatever the keyboard layout: each character goes as itself.
func typeText(s string) error {
	if s == "" {
		return errors.New("nothing to type")
	}
	var ins []input
	for _, r := range s {
		if r == '\n' {
			d, _ := keyInput("enter", false)
			u, _ := keyInput("enter", true)
			ins = append(ins, d, u)
			continue
		}
		if r == '\r' {
			continue
		}
		units := utf16.Encode([]rune{r})
		for _, u := range units {
			ins = append(ins, input{typ: inputKeyboard, scan: u, flags: keyEventUnicode})
		}
		for _, u := range units {
			ins = append(ins, input{typ: inputKeyboard, scan: u, flags: keyEventUnicode | keyEventKeyUp})
		}
	}
	return send(ins)
}
