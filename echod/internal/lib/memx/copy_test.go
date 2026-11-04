package memx

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestCopyIsCopy(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for _, n := range []int{0, 1, 3, 63, 64, 65, 100, 1366 * 4, 300 << 10, 1 << 20} {
		for _, off := range []int{0, 1, 4} {
			src := make([]byte, n+8)
			r.Read(src)
			dst := make([]byte, n+8)
			want := make([]byte, n+8)
			got := Copy(dst[off:off+n], src[off:off+n])
			copy(want[off:off+n], src[off:off+n])
			if got != n || !bytes.Equal(dst, want) {
				t.Fatalf("Copy %d at %d", n, off)
			}
		}
	}
	// Overlapping: as copy, either way.
	b := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	w := append([]byte(nil), b...)
	Copy(b[4:], b)
	copy(w[4:], w)
	if !bytes.Equal(b, w) {
		t.Fatal("an overlapping Copy is not copy's")
	}
}

func TestFill(t *testing.T) {
	for _, n := range []int{0, 1, 7, 8, 9, 1366, 100000} {
		b := make([]byte, 4*n+4)
		Fill(b[:4*n], 0x11223344)
		for i := 0; i < n; i++ {
			if b[4*i] != 0x44 || b[4*i+1] != 0x33 || b[4*i+2] != 0x22 || b[4*i+3] != 0x11 {
				t.Fatalf("%d words: word %d is %x", n, i, b[4*i:4*i+4])
			}
		}
		if b[4*n] != 0 {
			t.Fatalf("%d words: filled past the end", n)
		}
	}
}

func frame() []byte {
	b := make([]byte, 1366*4*768)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

func BenchmarkCopyFrame(b *testing.B) {
	src, dst := frame(), frame()
	b.SetBytes(int64(len(src)))
	for range b.N {
		Copy(dst, src)
	}
}

func BenchmarkBuiltinCopyFrame(b *testing.B) {
	src, dst := frame(), frame()
	b.SetBytes(int64(len(src)))
	for range b.N {
		copy(dst, src)
	}
}

func BenchmarkFillFrame(b *testing.B) {
	dst := frame()
	b.SetBytes(int64(len(dst)))
	for range b.N {
		Fill(dst, 0xff102030)
	}
}
