package asp

import (
	"encoding/json"
	"fmt"
	"math"
	"math/cmplx"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// response is a section's gain at hz, in dB.
func response(f *biquad, hz float64) float64 {
	z := cmplx.Exp(complex(0, -2*math.Pi*hz/Rate))
	num := complex(f.b0, 0) + complex(f.b1, 0)*z + complex(f.b2, 0)*z*z
	den := 1 + complex(f.a1, 0)*z + complex(f.a2, 0)*z*z
	return 20 * math.Log10(cmplx.Abs(num/den))
}

// The shelf is all of its gain at the bottom and none at the top, cut or boost, and the peak is
// exactly its gain at its own centre and nothing far from it. These are what the file's numbers mean.
func TestTheVendorFiltersDoWhatTheFileSays(t *testing.T) {
	near := func(what string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s: %+.2f dB, want %+.2f", what, got, want)
		}
	}
	for _, g := range []float64{5, -5} {
		s := newVendorLowShelf(150, g, Rate)
		near(fmt.Sprintf("a %+g dB shelf at 20 Hz", g), response(s, 20), g, 0.1)
		near(fmt.Sprintf("a %+g dB shelf at 5 kHz", g), response(s, 5000), 0, 0.05)
	}
	for _, g := range []float64{2, -3} {
		p := newVendorPeak(80, g, 0.9, Rate)
		near(fmt.Sprintf("a %+g dB peak at its centre", g), response(p, 80), g, 1e-6)
		near(fmt.Sprintf("a %+g dB peak at 5 kHz", g), response(p, 5000), 0, 0.05)
	}
}

// The vendor's shelf has one slope, whatever Q the file gives it, so two files that differ only in
// the shelf's Q are the same tuning.
func TestTheShelfTakesNoQ(t *testing.T) {
	a := newPEQ([]peqFilter{{FilterType: "LOW_SHELF", Fc: 150, Q: 0.9, GaindB: 5}}, Rate)[0]
	b := newPEQ([]peqFilter{{FilterType: "LOW_SHELF", Fc: 150, Q: 4, GaindB: 5}}, Rate)[0]
	if *a != *b {
		t.Errorf("the shelf changed with its Q: %+v, %+v", *a, *b)
	}
}

func writePEQ(t *testing.T, dir string, bypass bool, filters ...peqFilter) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"Bypass": bypass, "Biquad Definitions": filters})
	if err != nil {
		t.Fatal(err)
	}
	// The vendor's files are commented, and the loader has to read them as they come.
	b = append([]byte("/* Parametric EQ Configuration */\n"), b...)
	if err := os.WriteFile(filepath.Join(dir, "ParametricEQ.cfg"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// BYPASS entries fill the file out to a multiple of four and run nothing; a type whose vendor design
// nobody has read is refused rather than guessed at.
func TestReadPEQ(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ParametricEQ.cfg")

	writePEQ(t, dir, false,
		peqFilter{FilterType: "LOW_SHELF", Fc: 150, Q: 0.9, GaindB: 5},
		peqFilter{FilterType: "BYPASS", Fc: 147, Q: 10, GaindB: -2},
		peqFilter{FilterType: "PEAK", Fc: 80, Q: 0.9, GaindB: 2},
	)
	got, err := readPEQ(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].FilterType != "LOW_SHELF" || got[1].FilterType != "PEAK" {
		t.Errorf("read %+v, want the shelf and the peak", got)
	}

	writePEQ(t, dir, true, peqFilter{FilterType: "PEAK", Fc: 80, Q: 0.9, GaindB: 2})
	if got, err := readPEQ(path); err != nil || len(got) != 0 {
		t.Errorf("a bypassed stage read as %+v, %v", got, err)
	}

	for _, bad := range []peqFilter{
		{FilterType: "HIGH_SHELF", Fc: 6000, Q: 0.7, GaindB: 2},
		{FilterType: "PEAK", Fc: 80, Q: 0, GaindB: 2},
		{FilterType: "PEAK", Fc: 30000, Q: 1, GaindB: 2},
	} {
		writePEQ(t, dir, false, bad)
		if _, err := readPEQ(path); err == nil {
			t.Errorf("%+v was taken", bad)
		}
	}
}

// radarDir is a directory in the shape of the Echo 2nd gen's: five filters of its length that pass
// the signal as it is, the given parametric EQ, and a compressor that leaves a quiet signal alone.
func radarDir(t *testing.T, peq ...peqFilter) string {
	t.Helper()
	dir := t.TempDir()
	taps := make([]string, tapsRadar)
	for i := range taps {
		taps[i] = "0.0"
	}
	taps[0] = "1.0"
	for _, b := range Radar.EQ {
		if err := os.WriteFile(filepath.Join(dir, b.Name), []byte(strings.Join(taps, ",\n")), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := json.Marshal(testMBCL())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, Radar.MBCL), m, 0o600); err != nil {
		t.Fatal(err)
	}
	writePEQ(t, dir, false, peq...)
	return dir
}

// rmsDB is a block's level in dB of full scale, taken over its second half so a filter's start-up
// is not in it.
func rmsDB(x []float32) float64 {
	var sum float64
	for _, v := range x[len(x)/2:] {
		sum += float64(v) * float64(v)
	}
	return 10 * math.Log10(sum/float64(len(x)/2))
}

// A quiet 1 kHz tone through the Echo 2nd gen's chain comes out lifted by the parametric EQ's peak and
// by the trim after the compressor, and by nothing else.
func TestTheRadarChainRunsItsOwnStages(t *testing.T) {
	tun, err := LoadSet(radarDir(t, peqFilter{FilterType: "PEAK", Fc: 1000, Q: 1, GaindB: 6}), Radar)
	if err != nil {
		t.Fatal(err)
	}
	const block = 768
	c, err := tun.Chain(block)
	if err != nil {
		t.Fatal(err)
	}

	x := make([]float32, block*40)
	for i := range x {
		x[i] = float32(0.01 * math.Sin(2*math.Pi*1000*float64(i)/Rate))
	}
	in := rmsDB(x)
	for i := 0; i < len(x); i += block {
		c.Process(x[i : i+block])
	}
	if got, want := rmsDB(x)-in, 6+Radar.TrimDB; math.Abs(got-want) > 0.3 {
		t.Errorf("1 kHz came out %+.2f dB, want the peak's 6 and the trim's %g", got, Radar.TrimDB)
	}
}

// The trim comes after the limiter, so it is the one stage that could push a sample past full scale.
func TestTheTrimStopsAtFullScale(t *testing.T) {
	tun, err := LoadSet(radarDir(t), Radar)
	if err != nil {
		t.Fatal(err)
	}
	const block = 768
	c, err := tun.Chain(block)
	if err != nil {
		t.Fatal(err)
	}
	x := make([]float32, block)
	for k := 0; k < 20; k++ {
		for i := range x {
			x[i] = float32(math.Copysign(1, math.Sin(2*math.Pi*1000*float64(k*block+i)/Rate)))
		}
		c.Process(x)
		for i, v := range x {
			if v > 1 || v < -1 {
				t.Fatalf("block %d sample %d is %g, past full scale", k, i, v)
			}
		}
	}
}

// A Dot's filters are half the Echo 2nd gen's length, and loading them as its tuning would be a
// different filter at every volume.
func TestARadarRefusesShortFilters(t *testing.T) {
	dir := radarDir(t)
	short := strings.Repeat("0.0,\n", tapsLong-1) + "0.0"
	if err := os.WriteFile(filepath.Join(dir, Radar.EQ[0].Name), []byte(short), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSet(dir, Radar); err == nil {
		t.Fatal("1024-tap filters loaded as the Echo 2nd gen's tuning")
	}
}
