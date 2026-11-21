package denoise

import "math"

// Stream is the filter as the microphone wants it: frames in, the same frames out, filtered. It is
// not safe for concurrent use.
//
// The estimator reads overlapping windows and returns half a window at a time, so this holds the
// samples in between. The output lags the input by one window, which everything downstream sees as
// the same audio arriving a moment later rather than as a different shape.
type Stream struct {
	f      *Filter
	window []float64
	hop    []float64

	pending []float64
	ready   []float64
}

func NewStream(rate int) *Stream {
	f := New(rate)
	s := &Stream{
		f:      f,
		window: make([]float64, f.Frame()),
		hop:    make([]float64, f.Hop()),
	}
	s.prime()
	return s
}

func (s *Stream) prime() {
	s.pending = s.pending[:0]
	s.ready = append(s.ready[:0], make([]float64, s.f.Frame())...)
}

// Forget drops what the filter learned about the room.
func (s *Stream) Forget() {
	s.f.Forget()
	s.prime()
}

// Apply filters a frame in place.
func (s *Stream) Apply(frame []int16) { s.ApplyKeeping(frame, nil) }

// ApplyKeeping filters a frame in place, and also writes the filtered samples to exact, before they
// are rounded to whole ones, when exact is as long as the frame. What noise reduction leaves of a quiet
// room is often a fraction of one step: rounded here, it is exact digital silence, and leveling after
// it has nothing left to bring up (#662). Leveling from exact keeps it the faint room it was.
func (s *Stream) ApplyKeeping(frame []int16, exact []float32) {
	keep := len(exact) >= len(frame)
	for _, v := range frame {
		s.pending = append(s.pending, float64(v))
	}

	for len(s.pending) >= s.f.Frame() {
		copy(s.window, s.pending[:s.f.Frame()])
		s.f.Push(s.window, s.hop)
		s.ready = append(s.ready, s.hop...)
		s.pending = append(s.pending[:0], s.pending[s.f.Hop():]...)
	}

	for i := range frame {
		v := 0.0
		if i < len(s.ready) {
			v = s.ready[i]
		}
		if keep {
			exact[i] = float32(v)
		}
		frame[i] = clampSample(v)
	}
	if n := min(len(frame), len(s.ready)); n > 0 {
		s.ready = append(s.ready[:0], s.ready[n:]...)
	}
}

func clampSample(v float64) int16 {
	switch {
	case v > 32767:
		return 32767
	case v < -32768:
		return -32768
	}
	return int16(math.Round(v)) // to the nearest step, not toward zero
}
