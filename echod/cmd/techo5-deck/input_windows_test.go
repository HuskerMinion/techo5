//go:build windows

package main

import (
	"testing"
	"unsafe"
)

// INPUT is 40 bytes on 64-bit Windows; SendInput refuses any other size.
func TestInputLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 && unsafe.Sizeof(input{}) != 40 {
		t.Fatalf("input is %d bytes", unsafe.Sizeof(input{}))
	}
	for _, k := range []string{"a", "z", "0", "f1", "f24", "media_play_pause", "win", "pageup"} {
		if _, ok := vk(k); !ok {
			t.Errorf("no code for %s", k)
		}
	}
}
