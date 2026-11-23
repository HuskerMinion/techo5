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
	return a.weightEnergy(e / float64(max(n, 1)))
}

// weightEnergy is weight for a block whose mean energy is already known, in whatever units the caller
// keeps consistently.
func (a *activity) weightEnergy(e float64) float64 {
	e += 1e-3
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
//
// While the speaker plays it hears through it: it learns, bin by bin, how the speaker reaches each
// microphone from the loopback (as the steered mix does for its null), and takes that out of the
// spectra before localizing what is left. Without that a wake word said over music has no direction,
// since the device itself is the loudest thing in the room.
type finder struct {
	t      finderTuning
	loc    *array.Locator
	blocks *blocker
	since  int

	// quiet weighs blocks while nothing plays, loud while something does: their floors are nothing alike.
	quiet, loud activity
	// env is the energy of recent blocks, for onsets, per activity.
	envQuiet, envLoud float64

	// x and r are the band's spectra of the microphones and the loopback; sxr and srr the speaker's
	// response learned from them.
	x       [][]complex64
	r       []complex64
	sxr     [][]complex128
	srr     []float64
	learned int

	// tally is the vote per locator bearing.
	tally []float64

	// bearing is the last confident answer, radians clockwise from segment 0, or -1.
	bearing float64
}

func newFinder() *finder { return newFinderTuned(defaultTuning) }

func newFinderTuned(t finderTuning) *finder {
	l := array.NewLocator(arrayGeometry, steerBlock, Rate, locateLo, locateHi, locateGrid)
	l.Decay = t.decay
	bins := l.Spectrum().Bins()
	f := &finder{t: t, loc: l, blocks: newBlocker(len(arrayGeometry) + 1), bearing: -1,
		tally: make([]float64, locateGrid), r: make([]complex64, bins), srr: make([]float64, bins)}
	for range arrayGeometry {
		f.x = append(f.x, make([]complex64, bins))
		f.sxr = append(f.sxr, make([]complex128, bins))
	}
	return f
}

// look takes one frame without the loopback. playing is whether the speaker is, which with nothing
// to take it out by means nothing can be told.
func (f *finder) look(mics [][]int16, playing bool) { f.lookRef(mics, nil, playing) }

// lookRef takes one frame and the loopback of the same time, nil when there is none.
func (f *finder) lookRef(mics [][]int16, ref []int16, playing bool) {
	chans := make([][]int16, 0, len(arrayGeometry)+1)
	chans = append(chans, mics[:len(arrayGeometry)]...)
	hasRef := ref != nil
	if !hasRef {
		ref = make([]int16, len(mics[0]))
	}
	chans = append(chans, ref)

	f.blocks.add(chans, func(block [][]float32) {
		w := f.weigh(block, playing, hasRef)
		f.loc.AddBand(f.x, w)
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

// weigh transforms a block, takes the speaker out of it while it plays, and says how much the block
// should count.
func (f *finder) weigh(block [][]float32, playing, hasRef bool) float64 {
	spec := f.loc.Spectrum()
	mics := len(arrayGeometry)
	for m := range mics {
		spec.Into(block[m], f.x[m])
	}

	act, env := &f.quiet, &f.envQuiet
	if playing {
		if !hasRef {
			return 0
		}
		act, env = &f.loud, &f.envLoud
		spec.Into(block[mics], f.r)
		for k, r := range f.r {
			rc := complex128(complex(real(r), -imag(r)))
			f.srr[k] = speakerKeep*f.srr[k] + float64(real(r)*real(r)+imag(r)*imag(r))
			for m := range f.sxr {
				f.sxr[m][k] = complex(speakerKeep, 0)*f.sxr[m][k] + complex128(f.x[m][k])*rc
			}
		}
		f.learned++
		if f.learned >= speakerReady {
			for k, r := range f.r {
				if f.srr[k] <= 0 {
					continue
				}
				for m := range f.x {
					h := f.sxr[m][k] / complex(f.srr[k], 0)
					f.x[m][k] -= complex64(h * complex128(r))
				}
			}
		}
	}

	// Without playback the block's own energy, as tuned on the speech takes; with it, what is left of
	// the band once the speaker is taken out, since the block's energy is mostly the speaker.
	var w float64
	if playing {
		var e float64
		for m := range f.x {
			for _, v := range f.x[m] {
				e += float64(real(v)*real(v) + imag(v)*imag(v))
			}
		}
		w = act.weightEnergy(e / float64(mics*len(f.r)*steerBlock))
	} else {
		w = act.weight(block[:mics])
	}
	if f.t.onset {
		e := act.last
		rise := 0.0
		if *env > 0 {
			rise = 10 * math.Log10(e / *env)
		}
		*env = 0.7**env + 0.3*e
		w *= math.Max(0, math.Min(1, rise/3))
	}
	return w
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
