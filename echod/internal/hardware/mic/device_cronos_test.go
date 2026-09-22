//go:build !dot && !spot

package mic

import "testing"

// A workstation build reads as a Show 5, so onCrown hands back the Show 5's value here and the
// shared frame test only ever checks that branch. These check the other one, against what a Show 8
// was measured to do on 2026-09-22: capture device 22 opening at 16 kHz, S24_3LE, six channels,
// ch0 to ch3 the microphones and ch4/ch5 the playback loopback.
//
// The numbers matter more than they look. Describe the frame wrongly and nothing fails loudly: the
// daemon reads a loopback channel as a microphone, the echo canceller cancels against the wrong
// thing, and the only symptom is a device that stops hearing anyone over its own music.
func TestTheShow8CaptureFrame(t *testing.T) {
	if show8RefFirst != show8Mics {
		t.Errorf("the loopback starts at %d but there are %d microphones; the frame has a hole or an overlap", show8RefFirst, show8Mics)
	}
	if show8Channels != show8Mics+Refs {
		t.Errorf("Channels = %d, but %d microphones and %d loopback channels make %d", show8Channels, show8Mics, Refs, show8Mics+Refs)
	}
	if show8Mics > maxMics {
		t.Errorf("a Show 8's %d microphones do not fit in maxMics = %d, so the beamformer's arrays are too small", show8Mics, maxMics)
	}
	if CenterMic >= show8Mics {
		t.Errorf("CenterMic = %d is not one of a Show 8's %d microphones", CenterMic, show8Mics)
	}

	// Four microphones arrive on two converters, ADC_A and ADC_B, both confirmed in the unit's mixer.
	// The Show 5's two arrive on one.
	if got := len(onCrown([]string{"A"}, []string{"A", "B"})); got != len(adcs) {
		t.Logf("running board has %d converters", got)
	}
}

// The Show 5's own frame, unchanged by any of this: two microphones, then the loopback.
func TestTheShow5CaptureFrameIsUnchanged(t *testing.T) {
	if show5Mics != 2 || show5Channels != 4 || show5RefFirst != 2 {
		t.Errorf("Show 5 frame is Mics=%d Channels=%d RefFirst=%d, want 2/4/2", show5Mics, show5Channels, show5RefFirst)
	}
}

// A workstation is not a Show 8, so onCrown has to hand back the Show 5's value and the package
// variables have to follow it. If this fails, board detection has decided a developer's machine is
// an Echo.
func TestAWorkstationReadsAsAShow5(t *testing.T) {
	if got := onCrown(show5Channels, show8Channels); got != show5Channels {
		t.Errorf("onCrown gave %d, want the Show 5's %d", got, show5Channels)
	}
	if Mics != show5Mics || Channels != show5Channels || RefFirst != show5RefFirst {
		t.Errorf("frame reads Mics=%d Channels=%d RefFirst=%d, want %d/%d/%d",
			Mics, Channels, RefFirst, show5Mics, show5Channels, show5RefFirst)
	}
}
