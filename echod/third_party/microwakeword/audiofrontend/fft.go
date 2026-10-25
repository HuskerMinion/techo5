package audiofrontend

import "math"

// nextPow2 returns the smallest power of 2 >= n.
func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// makeHannWindow creates a Hann window of the given size.
// Uses the "periodic" variant: 0.5 - 0.5*cos(2*pi*i/N)
func makeHannWindow(n int) []float64 {
	w := make([]float64, n)
	for i := 0; i < n; i++ {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
	}
	return w
}

// fftPlan is realFFT with its buffers and twiddles kept. The twiddles are the values realFFT's
// recurrence produces, made by that same recurrence, so the results are the same to the bit.
type fftPlan struct {
	n          int
	re, im     []float64
	twRe, twIm [][]float64 // per stage (size 2, 4, ..., n), half entries each
}

func newFFTPlan(n int) *fftPlan {
	p := &fftPlan{n: n, re: make([]float64, n), im: make([]float64, n)}
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		angle := -2 * math.Pi / float64(size)
		wRe := math.Cos(angle)
		wIm := math.Sin(angle)
		tr, ti := make([]float64, half), make([]float64, half)
		uRe, uIm := 1.0, 0.0
		for k := 0; k < half; k++ {
			tr[k], ti[k] = uRe, uIm
			newURe := uRe*wRe - uIm*wIm
			uIm = uRe*wIm + uIm*wRe
			uRe = newURe
		}
		p.twRe = append(p.twRe, tr)
		p.twIm = append(p.twIm, ti)
	}
	return p
}

// run transforms x (n samples) and returns the real and imaginary parts; bins 0..n/2 are the
// spectrum realFFT returns, interleaved.
func (p *fftPlan) run(x []float64) (re, im []float64) {
	n := p.n
	re, im = p.re, p.im
	copy(re, x[:n])
	clear(im)

	// Bit-reversal permutation
	j := 0
	for i := 0; i < n; i++ {
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
		m := n >> 1
		for m >= 1 && j >= m {
			j -= m
			m >>= 1
		}
		j += m
	}

	// FFT butterfly
	stage := 0
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		tr, ti := p.twRe[stage], p.twIm[stage]
		stage++
		for start := 0; start < n; start += size {
			a, b := re[start:start+half], re[start+half:start+size]
			c, d := im[start:start+half], im[start+half:start+size]
			for k := range a {
				uRe, uIm := tr[k], ti[k]
				tRe := uRe*b[k] - uIm*d[k]
				tIm := uRe*d[k] + uIm*b[k]
				b[k] = a[k] - tRe
				d[k] = c[k] - tIm
				a[k] += tRe
				c[k] += tIm
			}
		}
	}
	return re[:n/2+1], im[:n/2+1]
}

// hzToMel converts frequency in Hz to mel scale.
func hzToMel(hz float64) float64 {
	return 1127.0 * math.Log(1.0+hz/700.0)
}

// melToHz converts mel scale to Hz.
func melToHz(mel float64) float64 {
	return 700.0 * (math.Exp(mel/1127.0) - 1.0)
}

// makeMelFilterbank creates triangular mel-spaced filterbank weights.
func makeMelFilterbank(numChannels, fftSize, sampleRate int, lowerHz, upperHz float64) [][]float64 {
	numBins := fftSize/2 + 1
	lowerMel := hzToMel(lowerHz)
	upperMel := hzToMel(upperHz)

	// Create numChannels+2 equally spaced mel points
	melPoints := make([]float64, numChannels+2)
	for i := range melPoints {
		melPoints[i] = lowerMel + float64(i)*(upperMel-lowerMel)/float64(numChannels+1)
	}

	// Convert to FFT bin indices
	binPoints := make([]float64, numChannels+2)
	for i := range binPoints {
		hz := melToHz(melPoints[i])
		binPoints[i] = hz * float64(fftSize) / float64(sampleRate)
	}

	// Create triangular filters
	filters := make([][]float64, numChannels)
	for ch := 0; ch < numChannels; ch++ {
		filters[ch] = make([]float64, numBins)
		left := binPoints[ch]
		center := binPoints[ch+1]
		right := binPoints[ch+2]
		for bin := 0; bin < numBins; bin++ {
			fb := float64(bin)
			if fb >= left && fb <= center && center > left {
				filters[ch][bin] = (fb - left) / (center - left)
			} else if fb > center && fb <= right && right > center {
				filters[ch][bin] = (right - fb) / (right - center)
			}
		}
	}
	return filters
}
