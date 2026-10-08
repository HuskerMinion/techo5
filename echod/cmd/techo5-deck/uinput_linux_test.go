//go:build linux

package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// TestUinputKeys makes the virtual keyboard, presses a shortcut and types, and reads back what the
// kernel's own input device for it reports. It needs to open /dev/uinput and the event devices, so
// it only runs when asked: TECHO5_UINPUT_TEST=1, as root or a member of the input group.
func TestUinputKeys(t *testing.T) {
	if os.Getenv("TECHO5_UINPUT_TEST") == "" {
		t.Skip("set TECHO5_UINPUT_TEST=1 to make a real virtual keyboard")
	}
	// Make the keyboard first, then find its event device by name.
	if _, err := openKeyboard(); err != nil {
		t.Fatal(err)
	}
	var dev string
	for range 20 {
		names, _ := filepath.Glob("/sys/class/input/event*/device/name")
		for _, n := range names {
			b, _ := os.ReadFile(n)
			if strings.TrimSpace(string(b)) == "TECHO5 Deck" {
				dev = "/dev/input/" + filepath.Base(filepath.Dir(filepath.Dir(n)))
			}
		}
		if dev != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if dev == "" {
		t.Fatal("no input device called TECHO5 Deck appeared")
	}
	f, err := os.Open(dev)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	type press struct {
		code  uint16
		value int32
	}
	got := make(chan []press, 1)
	go func() {
		var out []press
		size := int(unsafe.Sizeof(inputEvent{}))
		buf := make([]byte, size)
		_ = f.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, err := f.Read(buf); err != nil {
				break
			}
			typ := binary.LittleEndian.Uint16(buf[size-8:])
			code := binary.LittleEndian.Uint16(buf[size-6:])
			value := int32(binary.LittleEndian.Uint32(buf[size-4:]))
			if typ == evKey {
				out = append(out, press{code, value})
			}
			if len(out) >= 16 {
				break
			}
		}
		got <- out
	}()

	c, _ := parseCombo("ctrl+shift+m")
	if err := pressCombo(c); err != nil {
		t.Fatal(err)
	}
	if err := typeText("Hi!"); err != nil {
		t.Fatal(err)
	}
	want := []press{
		{29, 1}, {42, 1}, {50, 1}, {50, 0}, {42, 0}, {29, 0}, // ctrl+shift+m
		{42, 1}, {35, 1}, {35, 0}, {42, 0}, // H
		{23, 1}, {23, 0}, // i
		{42, 1}, {2, 1}, {2, 0}, {42, 0}, // !
	}
	events := <-got
	if len(events) < len(want) {
		t.Fatalf("read %d key events, want %d: %v", len(events), len(want), events)
	}
	for i, w := range want {
		if events[i] != w {
			t.Fatalf("event %d is %v, want %v (all: %v)", i, events[i], w, events)
		}
	}
}
