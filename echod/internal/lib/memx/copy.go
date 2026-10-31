package memx

import "unsafe"

// Copy copies src into dst, as the built-in copy does, and says how many bytes it copied. Word-aligned
// runs of at least 64 bytes go through the assembly, which asks for the source ahead.
func Copy(dst, src []byte) int {
	n := min(len(dst), len(src))
	if n < 64 || (uintptr(unsafe.Pointer(&dst[0]))|uintptr(unsafe.Pointer(&src[0])))&3 != 0 || overlap(dst[:n], src[:n]) {
		return copy(dst, src)
	}
	words := n &^ 31
	copyBlocks(dst[:words], src[:words])
	copy(dst[words:n], src[words:n])
	return n
}

// overlap is whether a and b share memory, where copy's own care about direction is needed.
func overlap(a, b []byte) bool {
	pa, pb := uintptr(unsafe.Pointer(&a[0])), uintptr(unsafe.Pointer(&b[0]))
	return pa < pb+uintptr(len(b)) && pb < pa+uintptr(len(a))
}

// Fill sets every word of dst, a multiple of 4 bytes starting on a word, to v (little-endian: a pixel's
// bytes as they are in memory are v, v>>8, v>>16, v>>24).
func Fill(dst []byte, v uint32) {
	if len(dst) < 4 {
		return
	}
	fill(unsafe.Slice((*uint32)(unsafe.Pointer(&dst[0])), len(dst)/4), v)
}

func fillGo(w []uint32, v uint32) {
	for i := range w {
		w[i] = v
	}
}
