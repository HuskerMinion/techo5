package asp

import "testing"

// crownBands is the shape an Echo Show 8 declares in every one of its playback profiles, read off a
// unit 2026-09-22: the lower two bands compressed at 3:1, the upper two left alone with a ratio of
// zero. Its VOIP profile sets all four, so the zeros are a choice and not a gap in the file.
func crownBands() mbcl {
	band := func(ratio, thresh, limThresh float64) mbclBand {
		return mbclBand{CompInVol: 6, CompRatio: ratio, CompThresh: thresh, CompGainMin: -40,
			LimThresh: limThresh, LimRelease: 180}
	}
	return mbcl{
		PreFilterBypass: true,
		NumBands:        4,
		Crossovers:      []float64{70, 1000, 5000},
		Bands: []mbclBand{
			band(3, -10, -10), band(3, -10, -10), band(0, -10, 0), band(0, -10, -1),
		},
		Full: mbclLimit{LimThresh: -1, LimRelease: 180},
	}
}

// A band asking for no compression must not stop the tuning loading. Before this, a Show 8 refused
// its own firmware's compressor and played untuned.
func TestABandMayAskForNoCompression(t *testing.T) {
	s, err := newMBCL(crownBands(), 48000)
	if err != nil {
		t.Fatalf("a Show 8's own compressor was refused: %v", err)
	}
	if len(s.bands) != 4 {
		t.Fatalf("%d bands, want 4", len(s.bands))
	}
	// One is the identity, so an uncompressed band asks for no gain change at all. Zero would be an
	// infinity here, which is the thing the clamp exists to keep out of the inner loop.
	for i, b := range s.bands {
		if b.compRatio < 1 {
			t.Errorf("band %d kept a ratio of %g, which is not a usable divisor", i, b.compRatio)
		}
	}
	if got := s.bands[2].compRatio; got != 1 {
		t.Errorf("an uncompressed band became %g:1, want 1:1", got)
	}
	if got := s.bands[0].compRatio; got != 3 {
		t.Errorf("a compressed band became %g:1, want 3:1", got)
	}
}

// The check this replaced was written for a real trap: the Show 5 carries an MBCL.cfg its own
// AFE.cfg never names, with every ratio at zero. Loading that one would put the tuning's bass lift
// on the driver with nothing compressing it anywhere. That still has to be refused.
func TestACompressorThatCompressesNothingIsRefused(t *testing.T) {
	m := crownBands()
	for i := range m.Bands {
		m.Bands[i].CompRatio = 0
	}
	if _, err := newMBCL(m, 48000); err == nil {
		t.Error("a compressor with every band bypassed was accepted")
	}
}
