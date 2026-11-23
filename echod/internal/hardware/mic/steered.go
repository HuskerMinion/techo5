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
	confident = 0.12

	// steerHold is how many blocks in a row a new beam has to win before it is switched to: about
	// 100 ms, so a single loud frame off to one side does not swing the beam mid-word.
	steerHold = 12
)

// activity measures how far each block stands over the room's floor, as a weight from 0 to 1.
type activity struct {
	floor float64
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

// finder is the facing for a measured array: where the last sound that stood out came from.
type finder struct {
	loc    *array.Locator
	blocks *blocker
	act    activity
	since  int

	// bearing is the last confident answer, radians clockwise from segment 0, or -1.
	bearing float64
}

func newFinder() *finder {
	l := array.NewLocator(arrayGeometry, steerBlock, Rate, locateLo, locateHi, locateGrid)
	l.Decay = locateDecay
	return &finder{loc: l, blocks: newBlocker(len(arrayGeometry)), bearing: -1}
}

// look takes one frame. playing is whether the speaker is: what the microphones hear then is mostly
// the device itself, which has no direction worth showing, so nothing counts.
func (f *finder) look(mics [][]int16, playing bool) {
	f.blocks.add(mics, func(block [][]float32) {
		w := f.act.weight(block)
		if playing {
			w = 0
		}
		f.loc.Add(block, w)
		if w > 0 {
			f.since = 0
		} else {
			f.since++
		}
	})
	// Judged only while something is standing out, or just was: afterwards the sum is what remains of
	// it, which only says the same thing again.
	if f.since > 4 {
		return
	}
	if b, c := f.loc.Bearing(); c >= confident {
		f.bearing = b
	}
}

// steered is the superdirective mix: a beam per LED segment, pointed where speech last came from.
type steered struct {
	bf     *array.Beamformer
	loc    *array.Locator
	act    activity
	in     [][]float32
	next   int
	votes  int
	active float64
}

func newSteered() *steered {
	s := &steered{
		bf:  array.NewBeamformer(arrayGeometry, steerBlock, Rate, steerBeams, steerMinWNG),
		loc: array.NewLocator(arrayGeometry, steerBlock, Rate, locateLo, locateHi, locateGrid),
		in:  make([][]float32, len(arrayGeometry)),
	}
	s.loc.Decay = locateDecay
	s.bf.Spectrum = s.spectrum
	return s
}

// spectrum is every block's transform, shared with the locator: it decides the beam for the blocks
// after it.
func (s *steered) spectrum(x [][]complex64) {
	s.loc.AddSpectra(x, s.active)
	if s.active == 0 {
		return
	}
	b, c := s.loc.Bearing()
	if c < confident {
		return
	}
	beam := int(math.Round(b/(2*math.Pi)*steerBeams)) % steerBeams
	if beam == s.bf.Beam {
		s.votes = 0
		return
	}
	if beam != s.next {
		s.next, s.votes = beam, 0
	}
	s.votes++
	if s.votes >= steerHold {
		s.bf.Beam, s.votes = beam, 0
	}
}

func (s *steered) Mix(mics [][]int16) []int16 {
	if len(mics) < len(arrayGeometry) || len(mics[0]) == 0 {
		return nil
	}
	n := len(mics[0])
	for m := range s.in {
		if cap(s.in[m]) < n {
			s.in[m] = make([]float32, n)
		}
		s.in[m] = s.in[m][:n]
		for i, v := range mics[m][:n] {
			s.in[m][i] = float32(v)
		}
	}
	// The weight is the frame's, held for the blocks it completes.
	s.active = s.act.weight(s.in)
	y := s.bf.Process(s.in)
	out := make([]int16, n)
	for i, v := range y {
		out[i] = clamp(v)
	}
	return out
}

// Beam is the beam being listened to.
func (s *steered) Beam() int { return s.bf.Beam }
