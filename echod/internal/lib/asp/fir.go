package asp

import "github.com/HuskerMinion/techo5/echod/internal/lib/fft"

// fir convolves with the tuning filter by overlap-save: each block is transformed together with the
// samples before it, so the block that comes out is the exact convolution of the block that went in.
// Nothing is buffered ahead, and the filter is minimum phase — its largest tap is its first — so this
// costs the playback path no latency.
type fir struct {
	f *fft.FFT

	// hs holds every bucket's filter, already transformed; h is the one in use. The overlap-save
	// tail is input history rather than filtered output, so changing which filter is used mid-stream
	// is clean and costs nothing.
	hs   [][]complex64
	h    []complex64
	work []complex64

	tail  []float32
	block int
}

// newFIR sizes the transform for filters of len(taps[0]) against blocks of block samples, and
// transforms each of them once. The transform has to hold one block plus the filter's overlap,
// rounded up to a power of two.
func newFIR(filters [][]float32, block int) *fir {
	n := 1
	for n < block+len(filters[0])-1 {
		n <<= 1
	}

	f := &fir{
		f:     fft.New(n),
		work:  make([]complex64, n),
		tail:  make([]float32, n-block),
		block: block,
	}
	for _, taps := range filters {
		h := make([]complex64, n)
		for i, t := range taps {
			h[i] = complex(t, 0)
		}
		f.f.Forward(h)
		f.hs = append(f.hs, h)
	}
	f.h = f.hs[len(f.hs)-1] // the loudest bucket until something says otherwise
	return f
}

// use picks which bucket's filter the next blocks are convolved with.
func (f *fir) use(i int) {
	if i >= 0 && i < len(f.hs) {
		f.h = f.hs[i]
	}
}

func (f *fir) reset() { clear(f.tail) }

// process filters one block in place.
func (f *fir) process(x []float32) {
	if len(x) != f.block {
		panic("asp: block is not the size the filter was built for")
	}

	for i, v := range f.tail {
		f.work[i] = complex(v, 0)
	}
	for i, v := range x {
		f.work[len(f.tail)+i] = complex(v, 0)
	}

	// Carry the input forward for the next block to sit behind, while x still holds it.
	keep := len(f.tail)
	if keep <= f.block {
		copy(f.tail, x[f.block-keep:])
	} else {
		copy(f.tail, f.tail[f.block:])
		copy(f.tail[keep-f.block:], x)
	}

	f.f.Forward(f.work)
	for i := range f.work {
		f.work[i] *= f.h[i]
	}
	f.f.Inverse(f.work)

	// The first len(tail) outputs are the wrapped ones overlap-save discards; the rest are the block.
	for i := range x {
		x[i] = real(f.work[keep+i])
	}
}
