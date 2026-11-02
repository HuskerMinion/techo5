// Package beat follows the level and the beat of the music being played, cheaply enough for the
// 32-bit ARM Echos.
//
// It is not a spectrum analyzer. Every four frames are averaged into one sample, which both lowers the
// rate to 12 kHz and acts as a crude low-pass, and two one-pole filters split that into bass, mid and
// treble. The work per second of audio is a few dozen thousand multiply-adds, with no transform at all.
//
// The level is measured against the loudest thing heard in the last few seconds, not against full
// scale, so a quiet track and a loud one both swing the whole range. The beat is the rise in bass
// (and, at half weight, mid) energy from one block to the next, compared with its own recent average.
package beat

import (
	"math"
	"sync"
)

const (
	// group is how many frames are averaged into one sample for the filters.
	group = 4

	// The splits between bass and mid, and mid and treble.
	bassCut = 250.0
	midCut  = 2200.0

	// silent is the RMS, against full scale, under which a block counts as nothing playing: about
	// -70 dBFS, under any real recording and over a converter's dither.
	silent = 3e-4

	// stopAfter is how long of silence before the music is no longer playing, long enough to ride
	// out the gap between two tracks and a quiet intro.
	stopAfter = 1.0

	// The reference is the loudest block heard, falling back over refFall seconds, and never below
	// refMin, so a whisper of a track does not have its noise stretched to full range.
	refFall = 12.0
	refMin  = 0.003

	// rangeDB is how far under the reference a block can be and still move the ring.
	rangeDB = 24.0

	// How fast the shown values rise and fall, in seconds.
	attack  = 0.04
	release = 0.35

	// Beat detection. A beat has to rise over thresholdGain times the recent average rise plus
	// thresholdMin, with at least beatGap seconds since the last, and not in the first warmup seconds of
	// music after silence, where everything is a rise. Its envelope fades over beatFade seconds.
	thresholdGain = 1.6
	thresholdMin  = 0.04
	beatGap       = 0.18
	warmup        = 0.25
	fluxAverage   = 1.0
	beatFade      = 0.16
)

// Snapshot is what the music is doing now. The levels are 0 to 1.
type Snapshot struct {
	// Playing is whether there has been sound in the last second.
	Playing bool

	Level  float64
	Bass   float64
	Mid    float64
	Treble float64

	// Beat is 1 at the moment of a beat and fades to 0 over about a sixth of a second. Beats counts
	// them, for something that wants to step on each one rather than follow the fade.
	Beat  float64
	Beats uint64
}

// Analyzer is fed what is played and read by whatever draws it. Feed is for one goroutine; Snapshot may
// be called from any.
type Analyzer struct {
	mu sync.Mutex

	// Filter state.
	lp1, lp2 float32

	snap Snapshot

	ref       float64
	quiet     float64 // seconds since sound
	sinceBeat float64
	sinceOn   float64 // seconds of music since it began
	bassPrev  float64
	midPrev   float64
	flux      float64
}

// Snapshot is the latest analysis.
func (a *Analyzer) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snap
}

// Reset forgets everything, for a tap that has been off while music went by.
func (a *Analyzer) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lp1, a.lp2 = 0, 0
	a.snap = Snapshot{}
	a.ref, a.quiet, a.sinceBeat, a.sinceOn = 0, 0, 0, 0
	a.bassPrev, a.midPrev, a.flux = 0, 0, 0
}

// Feed analyzes one block: interleaved stereo S16_LE at rate, in one or two slices that are summed,
// since the speaker mixes a track and a Source as it plays them. Either may be nil, or shorter than the
// other. A block with nothing in it is silence and still has to be fed, to move time on.
func (a *Analyzer) Feed(x, y []int16, rate int, frames int) {
	if rate <= 0 || frames <= 0 {
		return
	}
	dt := float64(frames) / float64(rate)

	k1 := coefficient(bassCut, rate/group)
	k2 := coefficient(midCut, rate/group)

	a.mu.Lock()
	defer a.mu.Unlock()

	var sAll, sBass, sMid, sTre float32
	lp1, lp2 := a.lp1, a.lp2
	groups := 0

	for f := 0; f+group <= frames; f += group {
		var sum int32
		for i := f * 2; i < (f+group)*2; i++ {
			if i < len(x) {
				sum += int32(x[i])
			}
			if i < len(y) {
				sum += int32(y[i])
			}
		}
		v := float32(sum) * (1.0 / (group * 2 * 32768))
		lp1 += k1 * (v - lp1)
		lp2 += k2 * (v - lp2)
		bass, mid, tre := lp1, lp2-lp1, v-lp2
		sAll += v * v
		sBass += bass * bass
		sMid += mid * mid
		sTre += tre * tre
		groups++
	}

	a.lp1, a.lp2 = lp1, lp2

	var rAll, rBass, rMid, rTre float64
	if groups > 0 {
		n := float32(groups)
		rAll = math.Sqrt(float64(sAll / n))
		rBass = math.Sqrt(float64(sBass / n))
		rMid = math.Sqrt(float64(sMid / n))
		rTre = math.Sqrt(float64(sTre / n))
	}
	a.step(dt, rAll, rBass, rMid, rTre)
}

// step moves the analysis on by one block's measurements. Called with the lock held.
func (a *Analyzer) step(dt, rAll, rBass, rMid, rTre float64) {
	loud := rAll >= silent
	if loud {
		if a.quiet >= stopAfter || a.sinceOn == 0 {
			// Music beginning from nothing: nothing to compare the first block with.
			a.sinceOn = dt
			a.bassPrev, a.midPrev, a.flux = 0, 0, 0
		} else {
			a.sinceOn += dt
		}
		a.quiet = 0
	} else {
		a.quiet += dt
		if a.quiet >= stopAfter {
			a.sinceOn = 0
		}
	}
	a.snap.Playing = a.quiet < stopAfter && a.sinceOn > 0

	// The reference follows the loudest recent block.
	a.ref = math.Max(a.ref*math.Exp(-dt/refFall), refMin)
	if loud && rAll > a.ref {
		a.ref = rAll
	}

	rise, fall := 1-math.Exp(-dt/attack), 1-math.Exp(-dt/release)
	ease := func(now *float64, target float64) {
		k := fall
		if target > *now {
			k = rise
		}
		*now += (target - *now) * k
	}
	ease(&a.snap.Level, scaled(rAll, a.ref))
	ease(&a.snap.Bass, scaled(rBass, a.ref))
	ease(&a.snap.Mid, scaled(rMid, a.ref))
	ease(&a.snap.Treble, scaled(rTre, a.ref))

	// The beat: how much the bass and mid energy rose, against how much it usually does.
	a.sinceBeat += dt
	a.snap.Beat *= math.Exp(-dt / beatFade)
	if !loud {
		a.bassPrev, a.midPrev = 0, 0
		return
	}
	bass, mid := rBass/a.ref, rMid/a.ref
	rise2 := math.Max(0, bass-a.bassPrev) + 0.5*math.Max(0, mid-a.midPrev)
	a.bassPrev, a.midPrev = bass, mid

	if a.sinceOn > warmup && a.sinceBeat >= beatGap && rise2 > thresholdGain*a.flux+thresholdMin {
		a.sinceBeat = 0
		a.snap.Beat = 1
		a.snap.Beats++
	}
	a.flux += (rise2 - a.flux) * (1 - math.Exp(-dt/fluxAverage))
}

// scaled turns an RMS into 0 to 1 against the reference, over rangeDB.
func scaled(rms, ref float64) float64 {
	if rms <= 0 {
		return 0
	}
	db := 20 * math.Log10(rms/ref)
	return math.Max(0, math.Min(1, 1+db/rangeDB))
}

// coefficient is the one-pole low-pass step for a cutoff at a sample rate.
func coefficient(cut float64, rate int) float32 {
	return float32(1 - math.Exp(-2*math.Pi*cut/float64(rate)))
}
