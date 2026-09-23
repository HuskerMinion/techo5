//go:build linux

package main

import (
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The descriptor's bit has to land where the kernel looks for it on this build's word size. A
// descriptor past the first 32 is the one fd/32 indexing got wrong on a 64-bit build.
func TestSelectSeesTheDescriptorItWasGiven(t *testing.T) {
	var p [2]int
	if err := unix.Pipe(p[:]); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(p[0])
	defer unix.Close(p[1])
	const high = 100
	if err := unix.Dup3(p[0], high, unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(high)

	if selectRead(high, 10*time.Millisecond) {
		t.Fatal("an empty pipe was readable")
	}
	if _, err := unix.Write(p[1], []byte{1}); err != nil {
		t.Fatal(err)
	}
	if !selectRead(high, time.Second) {
		t.Fatal("a pipe with a byte in it was not readable")
	}
}
