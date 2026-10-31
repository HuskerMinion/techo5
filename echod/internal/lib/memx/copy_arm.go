//go:build arm && !noasm

package memx

// copyBlocks copies len(src) bytes, a multiple of 32, both word-aligned and apart.
func copyBlocks(dst, src []byte) {
	if len(src) > 0 {
		copyARM(&dst[0], &src[0], len(src))
	}
}

func fill(w []uint32, v uint32) {
	n := len(w) &^ 7
	if n > 0 {
		fillARM(&w[0], n, v)
	}
	fillGo(w[n:], v)
}

//go:noescape
func copyARM(dst, src *byte, n int)

//go:noescape
func fillARM(dst *uint32, n int, v uint32)
