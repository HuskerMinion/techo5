package array

import (
	"math"
	"math/rand/v2"
	"testing"
)

// lShape is the Echo Dot 5th gen's array as Amazon's codebook gives it: an L of 22.5 mm legs.
var lShape = Geometry{{0.0225, 0}, {0, 0.0225}, {0, 0}}

// delayed is white noise as microphone m of g hears it from bearing, at rate, made by shifting the
// noise's spectrum so a fraction of a sample is exact.
func delayed(g Geometry, bearing, rate float64, n int, seed uint64) [][]float32 {
	r := rand.New(rand.NewPCG(seed, 1))
	src := make([]complex128, n)
	for i := range src {
		src[i] = complex(r.NormFloat64(), 0)
	}
	spec := dft(src, false)
	out := make([][]float32, len(g))
	for m := range g {
		d := g.Delay(m, bearing)
		s := make([]complex128, n)
		for k := range s {
			f := float64(k)
			if k > n/2 {
				f -= float64(n)
			}
			w := 2 * math.Pi * f * rate / float64(n)
			s[k] = spec[k] * complex(math.Cos(-w*d), math.Sin(-w*d))
		}
		t := dft(s, true)
		out[m] = make([]float32, n)
		for i := range t {
			out[m][i] = float32(real(t[i]))
		}
	}
	return out
}

// dft is a plain transform for the tests, slow and obviously right.
func dft(x []complex128, inverse bool) []complex128 {
	n := len(x)
	sign := -1.0
	if inverse {
		sign = 1
	}
	out := make([]complex128, n)
	for k := range out {
		var acc complex128
		for t, v := range x {
			a := sign * 2 * math.Pi * float64(k*t%n) / float64(n)
			acc += v * complex(math.Cos(a), math.Sin(a))
		}
		if inverse {
			acc /= complex(float64(n), 0)
		}
		out[k] = acc
	}
	return out
}

func TestLagFindsArrivalDifference(t *testing.T) {
	const rate, n = 16000.0, 1024
	bearing := Radians(130)
	mics := delayed(lShape, bearing, rate, n, 7)
	s := NewSpectra(n, rate, 300, 7000)

	for _, p := range Pairs(3) {
		a := make([]complex64, s.Bins())
		b := make([]complex64, s.Bins())
		s.Into(mics[p.A], a)
		s.Into(mics[p.B], b)
		acc := make([]complex128, s.Bins())
		PHAT(acc, a, b, 1)
		got, strength := s.Lag(acc, lShape.MaxTDOA()*1.2, 1e-6)
		want := lShape.TDOA(p, bearing)
		if math.Abs(got-want) > 3e-6 || strength < 0.5 {
			t.Errorf("pair %v: lag %.1f µs (strength %.2f), want %.1f", p, got*1e6, strength, want*1e6)
		}
	}
}

func TestFitRecoversGeometry(t *testing.T) {
	var obs []Observation
	r := rand.New(rand.NewPCG(3, 4))
	for seg := range 12 {
		b := Radians(float64(seg) * 30)
		for _, p := range Pairs(3) {
			obs = append(obs, Observation{p, b, lShape.TDOA(p, b) + r.NormFloat64()*2e-6})
		}
	}
	g, res, err := Fit(3, obs)
	if err != nil {
		t.Fatal(err)
	}
	want := lShape.Centered()
	for m := range g {
		if d := g[m].Sub(want[m]).Len(); d > 0.001 {
			t.Errorf("mic %d at %+v, want %+v (%.2f mm off)", m, g[m], want[m], d*1000)
		}
	}
	if res > 3e-6 {
		t.Errorf("residual %.2f µs", res*1e6)
	}
}

func TestLocatorFindsBearing(t *testing.T) {
	const rate, n = 16000.0, 256
	for _, deg := range []float64{0, 45, 100, 200, 290, 350} {
		bearing := Radians(deg)
		mics := delayed(lShape, bearing, rate, n*8, uint64(deg)+1)
		l := NewLocator(lShape, n, rate, 500, 7000, 72)
		for f := 0; f+n <= len(mics[0]); f += n / 2 {
			block := make([][]float32, 3)
			for m := range block {
				block[m] = mics[m][f : f+n]
			}
			l.Add(block, 1)
		}
		got, conf := l.Bearing()
		diff := math.Abs(math.Remainder(Degrees(got)-deg, 360))
		if diff > 7.5 || conf <= 0 {
			t.Errorf("source at %v°: found %.0f° (confidence %.2f)", deg, Degrees(got), conf)
		}
	}
}
