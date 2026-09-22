package asp

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Each device's firmware declares its own tuning and they are not the same design. Picking the wrong
// one is not a failure that announces itself: the Show's MBCL.cfg, for instance, is a file the Show
// does not use and has no compression in it at all, so loading it by name would put the tuning's bass
// lift on the driver with nothing holding it down.
func TestSetForPicksTheDevicesOwnTuning(t *testing.T) {
	for _, c := range []struct {
		marker string
		want   string
	}{
		{"EQ_40.cfg", "show"}, // both generations of Show 5
		{"EQ_30.cfg", "crown"},
		{"EQ_50.cfg", "dot"},
		{"EQ.cfg", "spot"},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, c.marker), []byte("0.0,\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		set, ok := SetFor(dir)
		if !ok || set.Name != c.want {
			t.Errorf("a directory holding %s loaded %q, want %q", c.marker, set.Name, c.want)
		}
	}
	if _, ok := SetFor(t.TempDir()); ok {
		t.Error("an empty directory was taken for a tuning")
	}
}

// The Show 8 ships an EQ_50.cfg as well as its own EQ_30.cfg, and the Dot is recognized by EQ_50.
// A directory holding both has to come out as the Show 8, or it loads the Dot's six buckets, asks
// for an EQ_60.cfg that is not there, and plays untuned. That is what it did before crown's set
// existed, so the ordering is the fix and this is what holds it in place.
func TestAShow8IsNotMistakenForADot(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"EQ_30.cfg", "EQ_50.cfg", "EQ_70.cfg", "EQ_100.cfg"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("0.0,\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	set, ok := SetFor(dir)
	if !ok || set.Name != "crown" {
		t.Fatalf("the Show 8's own files loaded %q, want crown", set.Name)
	}
	// Every file its buckets name has to be one the firmware actually ships.
	for _, b := range set.EQ {
		if _, err := os.Stat(filepath.Join(dir, b.Name)); err != nil {
			t.Errorf("bucket up to %.1f wants %s, which a Show 8 does not carry", b.UpTo, b.Name)
		}
	}
}

// The Show's quieter buckets are not its loudest one turned down — they carry more bass and treble —
// so which bucket the volume is in decides which filter runs, not how much gain goes in front of one.
func TestTheVolumePicksItsOwnFilter(t *testing.T) {
	show := &Tuning{set: Show}
	for _, c := range []struct {
		of   float64
		want int
	}{
		{0.0, 0}, {0.35, 0}, {0.4, 0},
		{0.5, 1}, {0.6, 1},
		{0.75, 2}, {0.8, 2},
		{0.95, 3}, {1.0, 3}, {1.5, 3},
	} {
		if got := show.Bucket(c.of); got != c.want {
			t.Errorf("at %.0f%% of full volume the Show uses filter %d, want %d", c.of*100, got, c.want)
		}
	}

	spot := &Tuning{set: Spot}
	for _, of := range []float64{0, 0.3, 1} {
		if got := spot.Bucket(of); got != 0 {
			t.Errorf("the Spot has one filter for every volume; at %.0f%% it used %d", of*100, got)
		}
	}
}

// The compressor's own input gain is part of the tuning: the Show's asks for 13 dB in front of it and
// the limiters below are set expecting it. Parsing that and not applying it leaves them idle and the
// result thin, which is what happened before.
func TestTheCompressorAppliesItsInputGain(t *testing.T) {
	quiet := testMBCL()
	loud := testMBCL()
	loud.InVol = 12 // four times

	level := func(m mbcl) float64 {
		st, err := newMBCL(m, Rate)
		if err != nil {
			t.Fatal(err)
		}
		var peak float64
		for b := range 20 {
			x := make([]float32, 512)
			for i := range x {
				n := float64(b*512 + i)
				x[i] = float32(0.02 * math.Sin(2*math.Pi*1000*n/Rate))
			}
			st.process(x)
			if b < 10 {
				continue
			}
			for _, v := range x {
				peak = math.Max(peak, math.Abs(float64(v)))
			}
		}
		return peak
	}

	q, l := level(quiet), level(loud)
	if l <= q*2 {
		t.Errorf("12 dB of input gain moved a quiet tone from %.4f to %.4f, want about four times", q, l)
	}
}

// A band gain the file asks for is applied rather than refused. The Spot's compressor asks for 10 dB
// in front of one band, and refusing it is refusing the whole tuning.
func TestABandGainIsAppliedNotRefused(t *testing.T) {
	m := testMBCL()
	m.Bands[1].CompInVol = 6
	m.Bands[2].LimInVol = 3
	if _, err := newMBCL(m, Rate); err != nil {
		t.Fatalf("a tuning that asks for band gain was refused: %v", err)
	}
}
