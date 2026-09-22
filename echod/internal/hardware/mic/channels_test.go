package mic

import "testing"

// Every device this daemon runs on lays its capture frame out the same way: the microphones first,
// then the playback loopback, and nothing else. The counts differ per device and, on the screens,
// per board at run time, so the relationship between them is worth asserting rather than trusting.
// A frame described wrongly does not fail loudly; it reads the loopback as a microphone and quietly
// ruins the echo canceller.
func TestTheCaptureFrameAddsUp(t *testing.T) {
	if Mics < 1 {
		t.Fatalf("Mics = %d", Mics)
	}
	if RefFirst != Mics {
		t.Errorf("the loopback starts at %d but there are %d microphones; the frame has a hole or an overlap", RefFirst, Mics)
	}
	if Channels != Mics+Refs {
		t.Errorf("Channels = %d, but %d microphones and %d loopback channels make %d", Channels, Mics, Refs, Mics+Refs)
	}
	if Mics > maxMics {
		t.Errorf("Mics = %d is more than maxMics = %d, so the beamformer's arrays are too small", Mics, maxMics)
	}
	if CenterMic < 0 || CenterMic >= Mics {
		t.Errorf("CenterMic = %d is not one of the %d microphones", CenterMic, Mics)
	}
	if RefFirst+Refs != Channels {
		t.Errorf("the loopback runs past the end of the frame: %d + %d > %d", RefFirst, Refs, Channels)
	}
}

// The beamformer indexes taps and hist by microphone, and both are sized at compile time while the
// count is known only at run time. This is the check that the spare rows are never needed.
func TestTheBeamformerHoldsEveryMicrophone(t *testing.T) {
	b := NewBeamformer()
	if got := len(b.hist); got < Mics {
		t.Errorf("history holds %d microphones, need %d", got, Mics)
	}
	for beam := range b.taps {
		if got := len(b.taps[beam]); got < Mics {
			t.Errorf("beam %d holds %d microphones, need %d", beam, got, Mics)
		}
	}
}
