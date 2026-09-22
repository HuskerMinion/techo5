// Package asp applies the speaker tuning the vendor's audio signal processing applies.
//
// A 1024-tap FIR carries the tuning: relative to 500 Hz it lifts 125-400 Hz by up to 27 dB and cuts
// 2-3 kHz by about 11 dB. A four-band compressor and limiter follows, and it is not optional — a
// 27 dB shelf at 160 Hz is only survivable because the band below 115 Hz is crushed 20:1 before it
// reaches the driver.
//
// The coefficients are the vendor's and are not ours to ship, so they are read off the device.
package asp

import (
	"fmt"
	"os"
	"path/filepath"
)

// VendorDir is where the tuning lives on a device.
const VendorDir = "/vendor/etc/audio-algorithms"

// Rate is the rate the tuning was designed at, which is also the only rate the playback codec takes.
const Rate = 48000

// The filter lengths the tunings use. A file of any other length is a tuning we do not know.
const (
	tapsLong  = 1024 // Dot and Show
	tapsShort = 512  // Spot
)

// A Set is one firmware's tuning: the volume-dependent EQ files with the volume each reaches up to,
// and the compressor that sits under them. Each device's AFE.cfg names its own, and they are not the
// same design — the Dot's six files are one filter at six gains, while the Show's four are four
// different shapes, because there the quieter buckets carry more bass and treble rather than less of
// everything. One filter per bucket covers both, so that is what is loaded.
type Set struct {
	Name string
	EQ   []Bucket
	MBCL string

	// Taps is how long each filter is, which is not the same on every device.
	Taps int
}

// Bucket is one EQ file and the fraction of full volume it reaches up to.
type Bucket struct {
	UpTo float64
	Name string
}

// The sets, from each firmware's own AFE.cfg. Dot: "Hardware Definition" → biscuit. Show: "Cronos"
// on the 2nd gen and "Checkers" on the 1st, which declare the same four files and the same
// compressor — read off both.
var (
	Dot = Set{
		Name: "dot",
		EQ: []Bucket{
			{0.5, "EQ_50.cfg"}, {0.6, "EQ_60.cfg"}, {0.7, "EQ_70.cfg"},
			{0.8, "EQ_80.cfg"}, {0.9, "EQ_90.cfg"}, {1.0, "EQ_100.cfg"},
		},
		MBCL: "MBCL.cfg",
		Taps: tapsLong,
	}

	// Show is both generations of Echo Show 5. Its MBCL is chosen by speaker power mode rather than
	// volume, and a mains-powered unit is in the top pair, which is MBCL_default.cfg. The bare
	// MBCL.cfg beside it is not referenced by AFE.cfg and has every ratio at zero — loading that one
	// because the name matches the Dot's would put the bass lift on the driver with nothing holding
	// it down.
	Show = Set{
		Name: "show",
		EQ: []Bucket{
			{0.4, "EQ_40.cfg"}, {0.6, "EQ_60.cfg"}, {0.8, "EQ_80.cfg"}, {1.0, "EQ_100.cfg"},
		},
		MBCL: "MBCL_default.cfg",
		Taps: tapsLong,
	}

	// Crown is the Echo Show 8, whose AFE.cfg calls itself "Crown". Four buckets and the same
	// compressor arrangement as the Show 5 — mains-powered lands on MBCL_default.cfg — but its own
	// files and its own boundaries, which the firmware states outright rather than leaving them to be
	// read off the file names:
	//
	//	"External Coefficients" : [ "EQ_30.cfg","EQ_50.cfg","EQ_70.cfg","EQ_100.cfg" ],
	//	"Volume Boundary"       : [ 30, 50, 70,100 ]
	//
	// Read off a unit 2026-09-22. Until this existed a Show 8 fell through to the Dot's set, because
	// it has an EQ_50.cfg and no EQ_40.cfg, and then asked for an EQ_60.cfg it does not ship: the
	// daemon reported the tuning as unavailable and played untuned.
	Crown = Set{
		Name: "crown",
		EQ: []Bucket{
			{0.3, "EQ_30.cfg"}, {0.5, "EQ_50.cfg"}, {0.7, "EQ_70.cfg"}, {1.0, "EQ_100.cfg"},
		},
		MBCL: "MBCL_default.cfg",
		Taps: tapsLong,
	}

	// Spot ("Rook") is a third design again: one filter for every volume ("Volume Boundary": [100]),
	// half the length of the others, and a compressor chosen by speaker power mode and by whether
	// what is playing is audio or video. A mains-powered unit playing audio is MBCL_2W_Audio.cfg.
	Spot = Set{
		Name: "spot",
		EQ:   []Bucket{{1.0, "EQ.cfg"}},
		MBCL: "MBCL_2W_Audio.cfg",
		Taps: tapsShort,
	}
)

// SetFor is the tuning a directory holds, told apart by a file only one of them has, and where no
// single file will do, by one that has to be there together with one that must not. The Spot is last
// because its EQ.cfg sits beside the others' files on some units.
//
// These directories hold more than the tuning in use: a Show 5 1st gen ships EQ_30.cfg, EQ_70.cfg
// and EQ_90.cfg that its own AFE.cfg never references. So a marker is only evidence when nothing
// else on the device could have put that file there, and picking the wrong set is not a small
// mistake — it loads the wrong compressor, or none, and puts a bass lift on the driver with nothing
// holding it down.
func SetFor(dir string) (Set, bool) {
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	for _, c := range []struct {
		marker string
		absent string // when set, the marker only counts if this file is not there
		set    Set
	}{
		{marker: "EQ_40.cfg", set: Show},
		// Before the Dot's, because the Show 8 has an EQ_50.cfg too and would otherwise be taken for
		// one. EQ_30.cfg alone is not enough to say Show 8: the Show 5 1st gen ships one as well, and
		// a Dot that shipped one would be tuned as a Show 8 and then asked for an EQ_60.cfg. What no
		// Show 8 has is EQ_60.cfg - its firmware names EQ_30, EQ_50, EQ_70 and EQ_100 - and the Dot
		// and both Show 5 generations all have one.
		{marker: "EQ_30.cfg", absent: "EQ_60.cfg", set: Crown},
		{marker: "EQ_50.cfg", set: Dot},
		{marker: "EQ.cfg", set: Spot},
	} {
		if has(c.marker) && (c.absent == "" || !has(c.absent)) {
			return c.set, true
		}
	}
	return Set{}, false
}

// Tuning is a loaded tuning, shared and read-only. Chain turns it into something that can process.
type Tuning struct {
	set     Set
	filters [][]float32 // one filter per bucket, in Set.EQ order
	mbcl    mbcl
}

// Load reads the tuning a directory holds, normally VendorDir: whichever set it is, with a filter
// for each of its volume buckets.
func Load(dir string) (*Tuning, error) {
	set, ok := SetFor(dir)
	if !ok {
		return nil, fmt.Errorf("asp: %s holds no tuning we know", dir)
	}
	return LoadSet(dir, set)
}

// LoadSet reads one named set, for a test that has files of its own.
func LoadSet(dir string, set Set) (*Tuning, error) {
	t := &Tuning{set: set}
	for _, e := range set.EQ {
		h, err := readFloats(filepath.Join(dir, e.Name), set.Taps)
		if err != nil {
			return nil, err
		}
		t.filters = append(t.filters, h)
	}

	m, err := readMBCL(filepath.Join(dir, set.MBCL))
	if err != nil {
		return nil, err
	}
	t.mbcl = m
	return t, nil
}

// Bucket is which filter a fraction of full volume uses: the first one the volume reaches up to, and
// the last of them for anything above the rest.
func (t *Tuning) Bucket(of float64) int {
	for i, e := range t.set.EQ {
		if of <= e.UpTo {
			return i
		}
	}
	return len(t.set.EQ) - 1
}

// Chain is a tuning applied to one stream. It holds the filter history and the compressor's
// envelopes, so it belongs to whoever is playing and is not safe for concurrent use.
type Chain struct {
	tuning *Tuning
	tone   *tone
	fir    *fir
	comp   *mbclState
}

// Chain builds the processing state for a stream of the given block size. Every call to Process must
// then be exactly that long: the FIR's transform is sized for it.
func (t *Tuning) Chain(block int) (*Chain, error) {
	if block <= 0 {
		return nil, fmt.Errorf("asp: a block is %d samples", block)
	}
	if t.mbcl.Bypass {
		return nil, fmt.Errorf("asp: %s asks to be bypassed", t.set.MBCL)
	}

	comp, err := newMBCL(t.mbcl, Rate)
	if err != nil {
		return nil, err
	}
	return &Chain{tuning: t, tone: newTone(Tone{}, Rate), fir: newFIR(t.filters, block), comp: comp}, nil
}

// Volume tells the chain what fraction of full volume is playing, so it uses the filter the vendor
// meant for it. The quieter buckets are not the same shape turned down: they carry more bass and
// more treble, which is the loudness compensation the tuning exists for.
func (c *Chain) Volume(of float64) { c.fir.use(c.tuning.Bucket(of)) }

// SetTone puts the listener's own shelves in front of the tuning, or takes them out. It is called
// between blocks, from whoever owns the chain.
func (c *Chain) SetTone(t Tone) { c.tone = newTone(t, Rate) }

// Process applies the tuning to one block in place. Samples are full scale at ±1, which is what the
// compressor's thresholds are in dB of.
func (c *Chain) Process(x []float32) {
	c.tone.process(x)
	c.fir.process(x)
	c.comp.process(x)
}

// Reset drops the filter's history and the compressor's envelopes, so the next block is processed as
// though it were the first. A chain that stopped being used has a history of whatever was playing
// then, and starting from silence is better than smearing that across what is playing now.
func (c *Chain) Reset() {
	c.tone.reset()
	c.fir.reset()
	c.comp.reset()
}
