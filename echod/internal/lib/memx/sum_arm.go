//go:build arm && !noasm

package memx

func sum(w []uint32) uint64 {
	if len(w) == 0 {
		return sumGo(nil)
	}
	return sumARM(&w[0], len(w))
}

//go:noescape
func sumARM(p *uint32, n int) uint64
