package memx

import "unsafe"

// Copy copies src into dst, as the built-in copy does, and says how many bytes it copied. Word-aligned
// runs of at least 64 bytes go through the assembly, which asks for the source ahead.
func Copy(dst, src []byte) int {
	if !asm {
		return copy(dst, src)
	}
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

// fillGo sets the first words one at a time, doubles them with the built-in copy up to fillChunk
// words, and then copies that chunk on down, as image/draw fills a row and copies it: on arm64 the copy
// is the runtime's memmove, several times faster than a store a word, and the chunk stays in the cache.
func fillGo(w []uint32, v uint32) {
	n := min(len(w), 16)
	for i := range n {
		w[i] = v
	}
	for n < len(w) {
		n += copy(w[n:], w[:min(n, fillChunk)])
	}
}

// fillChunk is the most words fillGo copies at once: 4 KB, well inside any of the Shows' L1 data caches.
const fillChunk = 1024
