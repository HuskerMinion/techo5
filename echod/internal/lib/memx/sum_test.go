package memx

import (
	"math/rand"
	"testing"
	"unsafe"
)

func TestSumIsTheGoReference(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, n := range []int{0, 1, 4, 7, 8, 9, 31, 32, 33, 1366, 1920} {
		b := make([]byte, 4*n)
		r.Read(b)
		var w []uint32
		if n > 0 {
			w = unsafe.Slice((*uint32)(unsafe.Pointer(&b[0])), n)
		}
		if got, want := RowSum(b), sumGo(w); got != want {
			t.Errorf("%d words: %x, want %x", n, got, want)
		}
	}
}

func TestAnyOneWordChangesBothLanes(t *testing.T) {
	b := make([]byte, 4*64)
	base := RowSum(b)
	for i := 0; i < len(b); i++ {
		for _, bit := range []byte{1, 0x80} {
			b[i] ^= bit
			s := RowSum(b)
			b[i] ^= bit
			if s>>32 == base>>32 || uint32(s) == uint32(base) {
				t.Fatalf("byte %d bit %x: %x vs %x", i, bit, s, base)
			}
		}
	}
}

var sink uint64

func BenchmarkSumRow(b *testing.B) {
	row := make([]byte, 1366*4)
	b.SetBytes(int64(len(row)))
	for range b.N {
		sink ^= RowSum(row)
	}
}

func BenchmarkSumFrame(b *testing.B) {
	frame := make([]byte, 1366*4*768)
	for i := range frame {
		frame[i] = byte(i) // pages of their own, not the one zero page
	}
	b.SetBytes(int64(len(frame)))
	for range b.N {
		for y := 0; y < 768; y++ {
			sink ^= RowSum(frame[y*1366*4 : (y+1)*1366*4])
		}
	}
}
