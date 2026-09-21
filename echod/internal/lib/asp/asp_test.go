package asp

import (
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fft"
)

// convolve is the definition the FIR has to meet: every output sample is the filter run against the
// samples up to it. Slow, and the only thing here that is obviously correct.
func convolve(x, h []float32) []float32 {
	out := make([]float32, len(x))
	for n := range x {
		var sum float64
		for k, c := range h {
			if n-k >= 0 {
				sum += float64(c) * float64(x[n-k])
			}
		}
		out[n] = float32(sum)
	}
	return out
}

// A block-at-a-time filter that carried the wrong history would still sound like something, so this
// is the check that says the transform is doing a convolution and not an approximation of one.
func TestFIRMatchesDirectConvolution(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	h := make([]float32, 256)
	for i := range h {
		h[i] = float32(r.NormFloat64()) * float32(math.Exp(-float64(i)/40))
	}

	const block = 64
	x := make([]float32, block*9)
	for i := range x {
		x[i] = float32(r.NormFloat64())
	}
	want := convolve(x, h)

	got := make([]float32, len(x))
	copy(got, x)
	f := newFIR([][]float32{h}, block)
	for i := 0; i < len(got); i += block {
		f.process(got[i : i+block])
	}

	for i := range want {
		if diff := math.Abs(float64(want[i] - got[i])); diff > 1e-3 {
			t.Fatalf("sample %d: direct convolution %g, overlap-save %g", i, want[i], got[i])
		}
	}
}

// The bands are summed straight back together, so anything the split does to their relative phase
// shows up as a comb in the sum. Flat here means a band can be turned down on its own without the
// others changing around it.
func TestCrossoverSumsFlat(t *testing.T) {
	const (
		rate  = Rate
		n     = 8192
		bands = 4
	)
	fc := []float64{115, 500, 7500}

	x := make([]float32, n)
	x[0] = 1

	c := newCrossover(fc, rate)
	out := make([][]float32, bands)
	for i := range out {
		out[i] = make([]float32, n)
	}
	c.process(x, out)

	sum := make([]complex64, n)
	for i := range sum {
		var v float32
		for _, b := range out {
			v += b[i]
		}
		sum[i] = complex(v, 0)
	}
	fft.New(n).Forward(sum)

	// Only up to a little under Nyquist: the highest split sits at 7.5 kHz and the sections there are
	// warped enough by the bilinear transform that the top of the band is not worth holding to this.
	for k := 1; k < n/2; k++ {
		hz := float64(k) * rate / n
		if hz < 20 || hz > 18000 {
			continue
		}
		db := 20 * math.Log10(float64(real(sum[k]*conj(sum[k])))/2*0+cmagf(sum[k]))
		if math.Abs(db) > 0.5 {
			t.Fatalf("%.0f Hz: the bands sum to %+.2f dB", hz, db)
		}
	}
}

func conj(c complex64) complex64 { return fft.Conj(c) }

func cmagf(c complex64) float64 {
	return math.Hypot(float64(real(c)), float64(imag(c)))
}

// Whatever the compressor does above it, the chain is the last thing before the DAC and nothing may
// leave it above full scale.
func TestFullBandLimiterHoldsCeiling(t *testing.T) {
	tuning := &Tuning{set: Dot, filters: [][]float32{unitTaps()}, mbcl: testMBCL()}
	c, err := tuning.Chain(512)
	if err != nil {
		t.Fatal(err)
	}

	r := rand.New(rand.NewSource(2))
	ceil := math.Pow(10, testMBCL().Full.LimThresh/20)
	for range 40 {
		x := make([]float32, 512)
		for i := range x {
			x[i] = float32(r.NormFloat64()) * 8
		}
		c.Process(x)
		for i, v := range x {
			if math.Abs(float64(v)) > ceil+1e-6 {
				t.Fatalf("sample %d left the chain at %g, ceiling is %g", i, v, ceil)
			}
		}
	}
}

// The band below 115 Hz is compressed 20:1 from -50 dB, which is what keeps the tuning's bass boost
// off the driver. A change that quietly stopped doing that would not be audible until something broke.
func TestLowBandIsHeldDown(t *testing.T) {
	tuning := &Tuning{set: Dot, filters: [][]float32{unitTaps()}, mbcl: testMBCL()}
	c, err := tuning.Chain(512)
	if err != nil {
		t.Fatal(err)
	}

	var peak float64
	for b := range 60 {
		x := make([]float32, 512)
		for i := range x {
			n := float64(b*512 + i)
			x[i] = float32(0.9 * math.Sin(2*math.Pi*40*n/Rate))
		}
		c.Process(x)
		if b < 30 {
			continue
		}
		for _, v := range x {
			peak = math.Max(peak, math.Abs(float64(v)))
		}
	}

	if db := 20 * math.Log10(peak); db > -30 {
		t.Errorf("40 Hz at -1 dBFS came through at %.1f dBFS, the low band is not being held", db)
	}
}

func unitTaps() []float32 {
	h := make([]float32, 8)
	h[0] = 1
	return h
}

func testMBCL() mbcl {
	band := mbclBand{CompRatio: 2, CompThresh: -10, CompGainMin: -40, LimThresh: 0, LimRelease: 1}
	low := mbclBand{CompRatio: 20, CompThresh: -50, CompGainMin: -40, LimThresh: -8, LimRelease: 200}
	return mbcl{
		PreFilterBypass: true,
		NumBands:        4,
		Crossovers:      []float64{115, 500, 7500},
		Bands:           []mbclBand{low, band, band, band},
		Full:            mbclLimit{LimThresh: -0.1, LimRelease: 200},
	}
}

// vendorTuning is the real tuning, which is not ours to ship:
//
//	adb pull /vendor/etc/audio-algorithms /tmp/coefs
//	ECHOLOCAL_VENDOR_DIR=/tmp/coefs go test ./internal/lib/asp/ -run Vendor -v
func vendorTuning(t *testing.T) *Tuning {
	t.Helper()
	dir := os.Getenv("ECHOLOCAL_VENDOR_DIR")
	if dir == "" {
		t.Skip("set ECHOLOCAL_VENDOR_DIR to a copy of /vendor/etc/audio-algorithms")
	}

	v, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return v
}

// shapes are what each device's loudest filter looks like, measured off a stock unit, as bands
// relative to the mids. They are not the same tuning in three sizes: the Dot lifts the low mids by
// two dozen decibels and cuts the presence region hard, the Show lifts less and barely touches the
// presence region. Stating them apart is the point — this test once held the Dot's numbers alone and
// called a Show's dump a regression.
var shapes = map[string][]struct {
	lo, hi   float64
	min, max float64
}{
	"dot": {
		{125, 200, 22, 30},
		{200, 320, 16, 28},
		{2000, 3200, -16, -6},
		{500, 1000, -1, 1},
	},
	"show": {
		{125, 200, 10, 20},
		{200, 320, 6, 18},
		{2000, 3200, -6, 3},
		{500, 1000, -1, 1},
	},
	// The Spot barely lifts at all — three decibels where the Dot has twenty-five — and takes the
	// presence region down instead. Measured off a mains-powered unit, 2026-09-21.
	"spot": {
		{125, 200, 0, 6},
		{200, 320, 1, 7},
		{2000, 3200, -11, -3},
		{500, 1000, -1, 1},
	},
}

// The shape of the filter is the whole point of loading it, so this states the shape we measured off
// a stock device. A tuning that does not look like this is not the one we think we are applying.
func TestVendorFilterHasTheShapeWeMeasured(t *testing.T) {
	v := vendorTuning(t)
	want, known := shapes[v.set.Name]
	if !known {
		// The Spot's was never measured this way; a filter with no bass lift at all is still wrong.
		t.Logf("no measured shape for the %s tuning; checking only that it lifts the low end", v.set.Name)
	}

	const n = 8192
	spec := make([]complex64, n)
	for i, c := range v.filters[len(v.filters)-1] {
		spec[i] = complex(c, 0)
	}
	fft.New(n).Forward(spec)

	band := func(lo, hi float64) float64 {
		var sum float64
		var count int
		for k := 1; k < n/2; k++ {
			if hz := float64(k) * Rate / n; hz >= lo && hz < hi {
				sum += cmagf(spec[k])
				count++
			}
		}
		return 20 * math.Log10(sum/float64(count))
	}

	ref := band(500, 1000)
	if !known {
		if lift := band(125, 320) - ref; lift < 0.5 {
			t.Errorf("the low end is %+.1f dB against the mids, which is no lift at all", lift)
		}
		return
	}
	for _, w := range want {
		if got := band(w.lo, w.hi) - ref; got < w.min || got > w.max {
			t.Errorf("%s: %.0f-%.0f Hz is %+.1f dB, expected between %+.0f and %+.0f",
				v.set.Name, w.lo, w.hi, got, w.min, w.max)
		}
	}
}

func TestVendorMBCLIsTheOneWeBuiltFor(t *testing.T) {
	v := vendorTuning(t)
	m := v.mbcl

	if len(m.Bands) != 4 || len(m.Crossovers) != 3 {
		t.Fatalf("%d bands and %d crossovers", len(m.Bands), len(m.Crossovers))
	}
	// Each device protects its own driver at its own crossover: the Dot holds everything under
	// 115 Hz at 20:1, the Show splits lower and compresses less because it lifts less.
	protection := map[string]struct {
		crossover   float64
		ratio       float64
		threshAbove float64
	}{
		"dot":  {115, 10, -40},
		"show": {70, 4, -20},
		"spot": {200, 4, -20},
	}
	if p, ok := protection[v.set.Name]; ok {
		if m.Crossovers[0] != p.crossover {
			t.Errorf("the %s low crossover is at %g Hz, its driver protection assumes %g",
				v.set.Name, m.Crossovers[0], p.crossover)
		}
		if b := m.Bands[0]; b.CompRatio < p.ratio || b.CompThresh > p.threshAbove {
			t.Errorf("the %s low band compresses %g:1 from %g dB, which will not hold its bass boost",
				v.set.Name, b.CompRatio, b.CompThresh)
		}
	}
	if _, err := v.Chain(1024); err != nil {
		t.Errorf("the real tuning does not build a chain: %v", err)
	}
}

func TestLoadRejectsAWrongLengthFilter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Dot.EQ[0].Name), []byte("1.0,\n2.0,\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("a two-tap filter loaded as if it were the tuning")
	}
}

func TestStripComments(t *testing.T) {
	in := []byte(`{
		// a line comment
		"FilterBank FC": [115], /* and a block one */
		"Name": "http://not-a-comment"
	}`)
	m := map[string]any{}
	if err := json.Unmarshal(stripComments(in), &m); err != nil {
		t.Fatalf("stripped to something unparseable: %v\n%s", err, stripComments(in))
	}
	if m["Name"] != "http://not-a-comment" {
		t.Errorf("the // inside a string was treated as a comment: %v", m["Name"])
	}
}
