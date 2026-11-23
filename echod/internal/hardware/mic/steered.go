package mic

import (
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/lib/array"
)

// Steering from a measured geometry (arrayGeometry): where a sound comes from, by SRP-PHAT, and a
// superdirective beam pointed at it. Both are lib/array's; what lives here is when to believe them.

const (
	// steerBlock is the transform both work in: 16 ms at Rate, hopped by half, as Amazon's own front
	// end does (FFT 256, hop 128).
	steerBlock = 256

	// steerBeams is one beam per LED segment: 30 degrees, finer than this array resolves, so a talker is
	// never more than 15 degrees off a beam.
	steerBeams = 12

	// steerMinWNG keeps the superdirective weights from amplifying the microphones' own hiss by more
	// than 10 dB anywhere, which is the bottom of the band on 22 mm.
	steerMinWNG = -10

	// locateLo and locateHi are the band the bearing is judged on: Amazon's PHAT band (AFE.cfg,
	// "ASR DirectionSpectralAnalysis").
	locateLo = 500
	locateHi = 7000

	// locateGrid is how many bearings are tried, every 5 degrees.
	locateGrid = 72

	// locateDecay is how much of the past each block keeps: a time constant of about 80 ms at the hop.
	locateDecay = 0.9

	// activeDB is how far a block has to stand over the room's floor to count toward a bearing, and
	// fullDB where it counts in full. A room's hum and a fan come from somewhere too, so what is
	// localized is what stands out from them.
	activeDB = 6
	fullDB   = 18

	// confident is the steered response's peak over its mean that a bearing is acted on at.
	confident = 0.06

	// steerHold is how many blocks in a row a new beam has to win before it is switched to: about
	// 100 ms, so a single loud frame off to one side does not swing the beam mid-word.
	steerHold = 12
)

// activity measures how far each block stands over the room's floor, as a weight from 0 to 1.
type activity struct {
	floor float64
	// last is the energy of the block weighed last.
	last float64
}

func (a *activity) weight(x [][]float32) float64 {
	var e float64
	n := 0
	for _, m := range x {
		for _, v := range m {
			e += float64(v) * float64(v)
		}
		n += len(m)
	}
	e = e/float64(max(n, 1)) + 1e-3
	a.last = e
	// The floor follows the quietest blocks at once and rises only slowly, about 3 dB a second.
	if a.floor == 0 || e < a.floor {
		a.floor = e
	} else {
		a.floor *= 1.0055
	}
	db := 10 * math.Log10(e/a.floor)
	return math.Max(0, math.Min(1, (db-activeDB)/(fullDB-activeDB)))
}

// blocker cuts frames of any length into the transform's hopped blocks.
type blocker struct {
	hist [][]float32
	fill int
}

func newBlocker(mics int) *blocker {
	b := &blocker{hist: make([][]float32, mics)}
	for m := range b.hist {
		b.hist[m] = make([]float32, steerBlock)
	}
	return b
}

// add takes a frame per microphone and calls f for each block it completes.
func (b *blocker) add(mics [][]int16, f func(block [][]float32)) {
	hop := steerBlock / 2
	for i := range mics[0] {
		for m := range b.hist {
			b.hist[m][hop+b.fill] = float32(mics[m][i])
		}
		b.fill++
		if b.fill == hop {
			f(b.hist)
			for m := range b.hist {
				copy(b.hist[m], b.hist[m][hop:])
			}
			b.fill = 0
		}
	}
}

// finderTuning is what decides how the finder believes its locator. One set is used (defaultTuning);
// the fields are there so the speech takes can be swept against each other (steered_test.go).
type finderTuning struct {
	// decay is the locator's memory per block.
	decay float64
	// onset weights blocks by how fast they got louder. The first few milliseconds of a syllable reach
	// the array before the room's reflections do, so a block on the way up says where the talker is
	// and one on the way down mostly says where the nearest wall is (the precedence effect, which is
	// how ears do it too).
	onset bool
	// vote, when above zero, keeps a decaying tally of confident bearings and answers with its peak,
	// each block keeping vote of the tally, rather than taking every confident block at its word.
	vote float64
	// conf is the steered response a block's bearing has to reach to count.
	conf float64
}

// defaultTuning is the one that did best on speech recorded on the unit from six directions round it
// (2026-11-23, one room, a metre or two out): within 30 degrees 99% of the time once the talker had
// started, against 94% for the locator taken at its word, and the weakest direction from 69% to 96%.
// The price is turning: 0.9 s to follow a new talker (median, 1.5 s at worst), against 0.3.
var defaultTuning = finderTuning{decay: locateDecay, conf: confident, onset: true, vote: 0.99}

// finder is the facing for a measured array: where the last sound that stood out came from.
type finder struct {
	t      finderTuning
	loc    *array.Locator
	blocks *blocker
	act    activity
	since  int

	// env is the energy of recent blocks, for onsets.
	env float64
	// tally is the vote per locator bearing.
	tally []float64

	// bearing is the last confident answer, radians clockwise from segment 0, or -1.
	bearing float64
}

func newFinder() *finder { return newFinderTuned(defaultTuning) }

func newFinderTuned(t finderTuning) *finder {
	l := array.NewLocator(arrayGeometry, steerBlock, Rate, locateLo, locateHi, locateGrid)
	l.Decay = t.decay
	return &finder{t: t, loc: l, blocks: newBlocker(len(arrayGeometry)), bearing: -1,
		tally: make([]float64, locateGrid)}
}

// look takes one frame. playing is whether the speaker is: what the microphones hear then is mostly
// the device itself, which has no direction worth showing, so nothing counts.
func (f *finder) look(mics [][]int16, playing bool) {
	f.blocks.add(mics, func(block [][]float32) {
		w := f.act.weight(block)
		if f.t.onset {
			e := f.act.last
			rise := 0.0
			if f.env > 0 {
				rise = 10 * math.Log10(e/f.env)
			}
			f.env = 0.7*f.env + 0.3*e
			w *= math.Max(0, math.Min(1, rise/3))
		}
		if playing {
			w = 0
		}
		f.loc.Add(block, w)
		if w > 0 {
			f.since = 0
		} else {
			f.since++
		}
		if f.t.vote > 0 {
			f.count(w)
		}
	})
	if f.t.vote > 0 {
		return
	}
	// Judged only while something is standing out, or just was: afterwards the sum is what remains of
	// it, which only says the same thing again.
	if f.since > 4 {
		return
	}
	if b, c := f.loc.Bearing(); c >= f.t.conf {
		f.bearing = b
	}
}

// count adds this block's bearing to the tally, if it stood out and was confident, and answers with the
// tally's peak.
func (f *finder) count(w float64) {
	for i := range f.tally {
		f.tally[i] *= f.t.vote
	}
	if w > 0 && f.since <= 4 {
		if b, c := f.loc.Bearing(); c >= f.t.conf {
			at := int(math.Round(b/(2*math.Pi)*locateGrid)) % locateGrid
			for d, share := range []float64{0.25, 0.5, 1, 0.5, 0.25} {
				f.tally[(at+d-2+locateGrid)%locateGrid] += share * w * c
			}
		}
	}
	best := 0
	for i, v := range f.tally {
		if v > f.tally[best] {
			best = i
		}
	}
	if f.tally[best] > 0.05 {
		f.bearing = float64(best) * 2 * math.Pi / locateGrid
	}
}
