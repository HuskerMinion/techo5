// Package memx is memory work on pixels for the Surface RT's Tegra 3, whose cores read RAM at about a
// third of a gigabyte a second and do not prefetch by themselves (surface_rt docs/hw/ram.md): the
// assembly asks for what it reads next ahead of time (PLD), which nearly doubles that. Elsewhere it is
// plain Go.
//
// Measured on the tablet, a 1366x768 frame (4 MB): Copy 12 ms against the built-in copy's 24, Fill 2.8
// ms against image/draw's uniform fill's 10, RowSum of every row 6.8 ms. A second core makes them only
// 10-15% quicker for twice the CPU time: the memory is the limit, so none is split.
package memx

import (
	"math/bits"
	"unsafe"
)

const (
	seedA  = 0x811c9dc5
	seedA2 = 0x27d4eb2f
	seedC  = 0x1b873593
	mulA   = 0x9e3779b1
	mulB   = 0x85ebca77
)

// RowSum is a 64-bit checksum of a row of pixels, for telling a row that changed from one that did not
// without keeping a copy of it: comparing against a copy reads twice the memory.
//
// Every word goes into two lanes of 32 bits, each step a bijection of its lane: a multiplying one (two
// of them, taking alternate words, so the multiplier's latency is covered) and a rotating sum. A change
// to any one word always changes both; anything else goes unseen with a chance of about 2^-64.
//
// b's length is a multiple of 4 and it starts on a word, as a row of pixels does.
func RowSum(b []byte) uint64 {
	if len(b) < 4 {
		return sumGo(nil)
	}
	return sum(unsafe.Slice((*uint32)(unsafe.Pointer(&b[0])), len(b)/4))
}

// sumGo is the checksum in Go, the reference for the assembly.
func sumGo(w []uint32) uint64 {
	a0, a1, c := uint32(seedA), uint32(seedA2), uint32(seedC)
	i := 0
	for ; i+1 < len(w); i += 2 {
		a0 = (a0 ^ w[i]) * mulA
		a1 = (a1 ^ w[i+1]) * mulA
		c = bits.RotateLeft32(c, -13) + w[i]
		c = bits.RotateLeft32(c, -13) + w[i+1]
	}
	if i < len(w) {
		a0 = (a0 ^ w[i]) * mulA
		c = bits.RotateLeft32(c, -13) + w[i]
	}
	return uint64(a0^a1*mulB)<<32 | uint64(c)
}
