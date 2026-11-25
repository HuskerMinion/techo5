package mic

import "sync/atomic"

// RawBlock is one read of the microphones at the rate the converter runs at, before the decimators
// take them to Rate: what ultrasound needs, since everything above 8 kHz is gone after. It is
// borrowed: PCM is only good until the listener returns.
type RawBlock struct {
	// PCM is the capture as read, Channels wide, Width bytes a sample, little-endian and signed;
	// the first Mics channels are the microphones, and Full is their full scale.
	PCM      []byte
	Rate     int
	Channels int
	Mics     int
	Width    int
	Full     float32

	// Cut is the privacy cut (the mute) on this block: PCM holds nothing of the room then.
	Cut bool
}

var rawTap atomic.Pointer[func(RawBlock)]

// TapRaw hands every capture read to f, from the capture loop, until stop is called. Only boards
// that capture faster than Rate (the Echo Dot 5th gen and the 2nd gen Show 8, at 96 kHz) have any to
// hand; f must be quick, since the capture waits on it. One listener at a time: a second replaces
// the first.
func TapRaw(f func(RawBlock)) (stop func()) {
	p := &f
	rawTap.Store(p)
	return func() { rawTap.CompareAndSwap(p, nil) }
}

// emitRaw gives b to the listener, where there is one.
//
//lint:ignore U1000 used by the boards that carry the raw microphones (athena.go), not on every build
func emitRaw(b RawBlock) {
	if f := rawTap.Load(); f != nil {
		(*f)(b)
	}
}
