package mic

import "math"

// Where beam 0 faces on the ring, in degrees clockwise from segment 0.
//
// Found by pointing it at things: the tap geometry said 252, which came out reflected, and a quarter turn
// the other way from there. Half a turn is the error an array this small is most prone to — at 36 mm the
// front and back of a beam differ by very little — and six beams can only report to the nearest 60
// degrees, so up to 30 of any apparent error is the quantising rather than this number.
const ringOffset = 320.0

// Facing is where the loudest sound is, as a fraction clockwise round the ring from segment 0, and
// whether that is known yet.
//
// On the seven-microphone ring, asking is what starts the work, so the first call comes back unknown and
// the next frame has an answer. Nothing keeps it going: a delay and sum per frame is not free and only
// the ring wants it. A measured array's finder runs on every frame instead (findFacing).
func (s *Source) Facing() (float64, bool) {
	s.wantFacing.Store(true)

	// A measured array answers in degrees on the ring already (steered.go).
	if arrayGeometry != nil {
		tenths := s.facing.Load()
		if tenths < 0 {
			return 0, false
		}
		return float64(tenths) / 3600, true
	}

	beam := s.facing.Load()
	if beam < 0 {
		return 0, false
	}

	deg := float64(beam)*(360.0/Beams) - ringOffset
	return math.Mod(math.Mod(deg/360, 1)+1, 1), true
}

// findFacing steers the finder at whatever is loudest, if anything asked since the last frame. Its mix
// is discarded; only the direction it settled on is wanted.
//
// It switches the moment a direction wins rather than waiting for it to keep winning, which the mix does
// to avoid swinging mid-word. Nothing here is heard, so waiting only makes it remember where the last
// sound was — and a door closing or a hand clapping is over inside the wait. What steadies the ring is
// the effect easing toward the answer, which is the right place for it: the estimate says where the
// sound is now, and the animation decides how fast to believe it.
func (s *Source) findFacing(raw []byte, mics [][]int16) {
	// The measured array's finder runs on every frame: it hops blocks across frames and keeps a
	// decaying sum, and what it is asked for is usually where the wake word that just ended came from,
	// so it has to have been listening before anyone asked. It costs a transform per microphone every
	// 8 ms.
	if arrayGeometry != nil {
		// The superdirective mix steers by a finder of its own and has already given it this frame:
		// the same answer, without localizing twice.
		f := s.arrayFinder
		if st, ok := s.mixer.(*steered); ok {
			f = st.find
		} else {
			if f == nil {
				f = newFinder()
				s.arrayFinder = f
			}
			ref := make([]int16, len(mics[0]))
			referenceInto(raw, ref)
			f.lookRef(mics, ref, playing(ref))
		}
		if b := f.bearing; b >= 0 {
			s.facing.Store(int32(math.Round(b / (2 * math.Pi) * 3600)))
		}
		return
	}

	if !s.wantFacing.Swap(false) {
		return
	}
	s.finder.Look(mics)
	s.facing.Store(int32(s.finder.Beam()))
}

// HasDirection is whether this board's array was measured, so that Facing is a bearing on the ring
// worth showing anyone: only then is a direction offered outside the daemon.
func HasDirection() bool { return arrayGeometry != nil }
