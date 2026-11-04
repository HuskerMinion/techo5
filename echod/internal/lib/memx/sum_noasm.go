//go:build !arm || noasm

package memx

func sum(w []uint32) uint64 { return sumGo(w) }
