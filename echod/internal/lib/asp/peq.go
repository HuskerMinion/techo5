package asp

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// The parametric EQ the Echo 2nd gen's chain runs between the FIR and the compressor
// ("ParametricEQ": "Parametric EQ" in its AFE.cfg). Its ParametricEQ.cfg is a list of biquads by type,
// corner and gain; on the unit read 2026-10-16 two are in use, a low shelf of +5 dB at 150 Hz and a
// peak of +2 dB at 80 Hz, and the other six are BYPASS.
//
// The designs are the vendor's, which are not the cookbook's: Zölzer's, on K = tan(pi*fc/fs), with a
// shelf of fixed slope that takes no Q at all, whatever the file says, and the constant root two
// written as 1.4142. Read out of the vendor's library by EchoMuse (PR #687, device/internal/outchain/
// biquad.go); the cookbook shelf differs from it by up to 1.3 dB around 150-300 Hz.
//
// Only the two types a unit has been seen to use are taken. Any other is refused rather than
// designed by guess, and the tuning with it: a filter that is not the vendor's is a curve nobody
// tuned the compressor behind it for.

// peqFilter is one of the file's biquads, named as the file names them.
type peqFilter struct {
	FilterType string  `json:"FilterType"`
	Fc         float64 `json:"Fc"`
	Q          float64 `json:"Q"`
	GaindB     float64 `json:"GaindB"`
}

// vendorSqrt2 is the vendor's root two, which is not quite math.Sqrt2.
const vendorSqrt2 = 1.4142

// readPEQ parses a ParametricEQ.cfg into the filters it runs: none when the whole stage is bypassed,
// and never the BYPASS entries that fill it out to a multiple of four.
func readPEQ(path string) ([]peqFilter, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Bypass  bool        `json:"Bypass"`
		Biquads []peqFilter `json:"Biquad Definitions"`
	}
	if err := json.Unmarshal(stripComments(raw), &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Bypass {
		return nil, nil
	}
	var out []peqFilter
	for i, b := range f.Biquads {
		switch b.FilterType {
		case "BYPASS":
			continue
		case "LOW_SHELF", "PEAK":
		default:
			return nil, fmt.Errorf("%s: biquad %d is a %s, which is not one we know the vendor's design of",
				path, i+1, b.FilterType)
		}
		if b.Fc <= 0 || b.Fc >= Rate/2 || (b.FilterType == "PEAK" && b.Q <= 0) {
			return nil, fmt.Errorf("%s: biquad %d, a %s at %g Hz with Q %g, is not a filter", path, i+1,
				b.FilterType, b.Fc, b.Q)
		}
		if math.Abs(b.GaindB) > 24 {
			return nil, fmt.Errorf("%s: biquad %d asks for %+g dB", path, i+1, b.GaindB)
		}
		out = append(out, b)
	}
	return out, nil
}

// newPEQ designs the filters for a stream.
func newPEQ(filters []peqFilter, rate int) []*biquad {
	out := make([]*biquad, 0, len(filters))
	for _, f := range filters {
		switch f.FilterType {
		case "LOW_SHELF":
			out = append(out, newVendorLowShelf(f.Fc, f.GaindB, rate))
		case "PEAK":
			out = append(out, newVendorPeak(f.Fc, f.GaindB, f.Q, rate))
		}
	}
	return out
}

// newVendorLowShelf is the vendor's low shelf: gainDB below fc, flat above it.
func newVendorLowShelf(fc, gainDB float64, rate int) *biquad {
	k := math.Tan(math.Pi * fc / float64(rate))
	k2 := k * k
	v := math.Pow(10, math.Abs(gainDB)/20)
	sv := math.Sqrt(v) * vendorSqrt2
	if gainDB >= 0 {
		n := 1 / (1 + vendorSqrt2*k + k2)
		return &biquad{
			b0: (1 + sv*k + v*k2) * n,
			b1: 2 * (v*k2 - 1) * n,
			b2: (1 - sv*k + v*k2) * n,
			a1: 2 * (k2 - 1) * n,
			a2: (1 - vendorSqrt2*k + k2) * n,
		}
	}
	n := 1 / (1 + sv*k + v*k2)
	return &biquad{
		b0: (1 + vendorSqrt2*k + k2) * n,
		b1: 2 * (k2 - 1) * n,
		b2: (1 - vendorSqrt2*k + k2) * n,
		a1: 2 * (v*k2 - 1) * n,
		a2: (1 - sv*k + v*k2) * n,
	}
}

// newVendorPeak is the vendor's peak: gainDB at fc, as wide as q says, flat away from it.
func newVendorPeak(fc, gainDB, q float64, rate int) *biquad {
	k := math.Tan(math.Pi * fc / float64(rate))
	k2 := k * k
	v := math.Pow(10, math.Abs(gainDB)/20)
	if gainDB >= 0 {
		n := 1 / (1 + k/q + k2)
		return &biquad{
			b0: (1 + v*k/q + k2) * n,
			b1: 2 * (k2 - 1) * n,
			b2: (1 - v*k/q + k2) * n,
			a1: 2 * (k2 - 1) * n,
			a2: (1 - k/q + k2) * n,
		}
	}
	n := 1 / (1 + v*k/q + k2)
	return &biquad{
		b0: (1 + k/q + k2) * n,
		b1: 2 * (k2 - 1) * n,
		b2: (1 - k/q + k2) * n,
		a1: 2 * (k2 - 1) * n,
		a2: (1 - v*k/q + k2) * n,
	}
}
