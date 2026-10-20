//go:build arm64 && !noasm

package picscale

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// The NEON passes against the Go ones they stand in for, sample for sample, on random samples and
// weights as large either way as a filter's get.
func TestNEONMatchesGo(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	weights := func(n, bits int) []int16 {
		w := make([]int16, n)
		for i := range w {
			w[i] = int16(rng.IntN(1<<bits+1<<(bits-2)) - 1<<(bits-3))
		}
		return w
	}
	for _, taps := range []int{1, 2, 3, 5, 8, 11} {
		const stride, n = 100, 96
		src := make([]byte, stride*taps)
		for i := range src {
			src[i] = byte(rng.UintN(256))
		}
		w := weights(taps, vBits)
		got, want := make([]int16, n), make([]int16, n)
		verticalNEON(&got[0], &src[0], stride, &w[0], taps, n/16)
		verticalGo(want, src, stride, w, make([]int32, n))
		if !slices.Equal(got, want) {
			t.Errorf("vertical, %d taps: %v, want %v", taps, got[:8], want[:8])
		}
	}

	const outs = 64
	mid := make([]int16, (outs+wide)*4)
	for i := range mid {
		mid[i] = int16(rng.IntN(255<<midBits*13/10) - 255<<midBits*3/10)
	}
	start := make([]int32, outs)
	for i := range start {
		start[i] = int32(rng.IntN(outs))
	}
	w := weights(outs*wide, hBits)
	got, want := make([]byte, outs), make([]byte, outs)
	horizontalNEON(&got[0], &mid[0], &start[0], &w[0], outs/8)
	horizontalGo(want, mid, start, w, wide)
	if !slices.Equal(got, want) {
		t.Errorf("horizontal: %v, want %v", got, want)
	}
	w4 := weights(outs*4, hBits)
	horizontal4tapsNEON(&got[0], &mid[0], &start[0], &w4[0], outs/8)
	horizontalGo(want, mid, start, w4, 4)
	if !slices.Equal(got, want) {
		t.Errorf("horizontal, four taps: %v, want %v", got, want)
	}

	var y, cb, cr [256]byte
	for i := range 256 {
		y[i], cb[i], cr[i] = byte(i), byte(rng.UintN(256)), byte(rng.UintN(256))
	}
	rgbGot, rgbWant := make([]byte, 4*256), make([]byte, 4*256)
	toRGBANEON(&rgbGot[0], &y[0], &cb[0], &cr[0], 256/8)
	toRGBAGo(rgbWant, y[:], cb[:], cr[:])
	if !slices.Equal(rgbGot, rgbWant) {
		t.Errorf("toRGBA: %v, want %v", rgbGot[:16], rgbWant[:16])
	}
	got4, want4 := make([]byte, outs*4), make([]byte, outs*4)
	horizontal4NEON(&got4[0], &mid[0], &start[0], &w[0], outs/2)
	horizontal4Go(want4, mid, start, w, wide)
	if !slices.Equal(got4, want4) {
		t.Errorf("horizontal4: %v, want %v", got4[:16], want4[:16])
	}
}
