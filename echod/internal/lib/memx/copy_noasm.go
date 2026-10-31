//go:build !arm || noasm

package memx

func copyBlocks(dst, src []byte) { copy(dst, src) }

func fill(w []uint32, v uint32) { fillGo(w, v) }
