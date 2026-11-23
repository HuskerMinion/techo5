package mic

import (
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/lib/array"
)

// steered is the superdirective mix: a beam per LED segment, pointed where speech last came from, and
// while the speaker plays, the same beam with a null on the speaker.
//
// The null is what pays. Replayed over the wake bench recordings (2026-11-23: 114 prompts over music
// from 0 to 65%, "Alexa" and "Hey Jarvis", replay_bench_test.go), this mix caught 79 against the center
// microphone's 60: the same without music, 20 of 24 against 12 at 50% and 17 against 10 at 65%, with
// one false accept more over all of it (5 against 4). Held beams pointing anywhere did nearly as well
// with the null, and no better than the center microphone without it. The
// speaker sits a few centimetres under the microphones, so it reaches them in a pattern no talker
// across the room can make, and the null removes it whole, distortion included, which the canceller
// behind it cannot: the loopback does not hold what the speaker adds.
//
// While playing, the beam does not move: the canceller behind it learns one path (cancelInput, via
// pathFixed), and what the microphones hear is mostly the device anyway.
type steered struct {
	bf     *array.Beamformer
	loc    *array.Locator
	act    activity
	in     [][]float32
	next   int
	votes  int
	active float64

	// beam is the direction listened in; bf.Beam is which slot plays it, the plain beam or nulled.
	beam   int
	nulled int // the slot holding beam with the speaker nulled
	nullOf int // which beam the nulled slot was built for, or -1

	// The speaker's response across the microphones, learned from the loopback while it plays: the
	// cross spectra of each microphone with the loopback, and the loopback's own, per bin.
	refBF   *array.Beamformer
	refBuf  [][]float32
	refs    [][]complex64
	sxr     [][]complex128
	srr     []float64
	learned int
	playing bool

	// hold keeps the beam where it is, for a caller that wants one direction only.
	hold bool
}

// speakerKeep is how much of the speaker's measured response each block keeps: a time constant of
// 1.6 s. speakerEvery is how many blocks pass between rebuilding the nulled weights, and speakerReady
// how many have to have been learned before they are used.
const (
	speakerKeep  = 0.995
	speakerEvery = 32
	speakerReady = 64
)

func newSteered() *steered {
	s := &steered{
		bf:     array.NewBeamformer(arrayGeometry, steerBlock, Rate, steerBeams, steerMinWNG),
		loc:    array.NewLocator(arrayGeometry, steerBlock, Rate, locateLo, locateHi, locateGrid),
		in:     make([][]float32, len(arrayGeometry)),
		refBF:  array.NewBeamformer(array.Geometry{{}}, steerBlock, Rate, 1, steerMinWNG),
		refBuf: make([][]float32, 1),
		nullOf: -1,
	}
	s.nulled = s.bf.Extra(0)
	s.loc.Decay = locateDecay
	s.bf.Spectrum = s.spectrum
	s.refBF.Spectrum = func(x [][]complex64) { s.refs = append(s.refs, append([]complex64(nil), x[0]...)) }
	bins := s.bf.Bins()
	s.sxr = make([][]complex128, len(arrayGeometry))
	for m := range s.sxr {
		s.sxr[m] = make([]complex128, bins)
	}
	s.srr = make([]float64, bins)
	return s
}

// spectrum is every block's transform. The loopback's block of the same time has just been
// transformed (mix transforms it first, on the same block boundaries).
func (s *steered) spectrum(x [][]complex64) {
	var ref []complex64
	if len(s.refs) > 0 {
		ref = s.refs[0]
		s.refs = s.refs[1:]
	}
	if s.playing && ref != nil {
		s.learnSpeaker(x, ref)
		return
	}
	if s.hold {
		return
	}

	s.loc.AddSpectra(x, s.active)
	if s.active == 0 {
		return
	}
	b, c := s.loc.Bearing()
	if c < confident {
		return
	}
	beam := int(math.Round(b/(2*math.Pi)*steerBeams)) % steerBeams
	if beam == s.beam {
		s.votes = 0
		return
	}
	if beam != s.next {
		s.next, s.votes = beam, 0
	}
	s.votes++
	if s.votes >= steerHold {
		s.beam, s.votes = beam, 0
		s.bf.Beam = beam
	}
}

// learnSpeaker adds a block of playback to the speaker's response, and every so often rebuilds the
// nulled beam from it.
func (s *steered) learnSpeaker(x [][]complex64, ref []complex64) {
	for k := range s.srr {
		r := complex128(ref[k])
		rc := complex(real(r), -imag(r))
		s.srr[k] = speakerKeep*s.srr[k] + real(r)*real(r) + imag(r)*imag(r)
		for m := range s.sxr {
			s.sxr[m][k] = complex(speakerKeep, 0)*s.sxr[m][k] + complex128(x[m][k])*rc
		}
	}
	s.learned++
	if s.learned < speakerReady {
		return
	}
	if s.learned%speakerEvery == 0 || s.nullOf != s.beam {
		s.buildNull()
	}
	s.bf.Beam = s.nulled
}

// buildNull computes the nulled weights for the current beam from what was learned.
func (s *steered) buildNull() {
	bearing := 2 * math.Pi * float64(s.beam) / steerBeams
	wng := math.Pow(10, steerMinWNG/10.0)
	h := make([]complex128, len(s.sxr))
	for k := 1; k < len(s.srr); k++ {
		if s.srr[k] <= 0 {
			continue
		}
		for m := range h {
			h[m] = s.sxr[m][k] / complex(s.srr[k], 0)
		}
		s.bf.SetWeights(s.nulled, k, array.Constrained(arrayGeometry, s.bf.Omega(k), bearing, h, wng))
	}
	s.nullOf = s.beam
}

func (s *steered) Mix(mics [][]int16) []int16 { return s.mix(nil, mics) }

// MixRaw is Mix with the loopback, which the null is learned from.
func (s *steered) MixRaw(raw []byte, mics [][]int16) []int16 { return s.mix(raw, mics) }

func (s *steered) mix(raw []byte, mics [][]int16) []int16 {
	if len(mics) < len(arrayGeometry) || len(mics[0]) == 0 {
		return nil
	}
	n := len(mics[0])
	toFloat(s.in, mics, n)

	s.playing = false
	if raw != nil {
		ref := make([]int16, n)
		referenceInto(raw, ref)
		s.playing = playing(ref)
		toFloat(s.refBuf, [][]int16{ref}, n)
		s.refBF.Process(s.refBuf)
	}
	if !s.playing {
		s.bf.Beam = s.beam
	}

	// The weight is the frame's, held for the blocks it completes.
	s.active = s.act.weight(s.in)
	y := s.bf.Process(s.in)
	s.refs = s.refs[:0]
	out := make([]int16, n)
	for i, v := range y {
		out[i] = clamp(v)
	}
	return out
}

// toFloat converts n samples of each of src into dst, reusing dst's buffers.
func toFloat(dst [][]float32, src [][]int16, n int) {
	for m := range dst {
		if cap(dst[m]) < n {
			dst[m] = make([]float32, n)
		}
		dst[m] = dst[m][:n]
		for i, v := range src[m][:n] {
			dst[m][i] = float32(v)
		}
	}
}

// pathFixed is whether the beam is held, which it is while the speaker plays: the canceller can then
// learn what comes out of it.
func (s *steered) pathFixed() bool { return s.playing }

// Beam is the direction being listened in.
func (s *steered) Beam() int { return s.beam }
