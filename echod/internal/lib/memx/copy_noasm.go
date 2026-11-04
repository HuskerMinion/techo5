//go:build !arm || noasm

package memx

// asm is whether Copy and Fill have assembly of their own here: without it Copy is the built-in copy.
const asm = false

func copyBlocks(dst, src []byte) { copy(dst, src) }

func fill(w []uint32, v uint32) { fillGo(w, v) }
