package cast

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// ramp is audio whose frames can be told apart, so a repeated or skipped one shows.
func ramp(n int) []int16 {
	s := make([]int16, n*speaker.Channels)
	for i := range s {
		s[i] = int16(i / speaker.Channels)
	}
	return s
}

// heard is the write position at which the anchor frame is the one being heard: the tail further on.
const heard = 1000 + uint64(tailFrames)

// listening is an output anchored at frame 1000 at time t0, with a ramp queued from the anchor to well
// past the write position, as a running cast has, and the card where the anchor frame is being heard.
func listening(t *testing.T) (o *audioOut, t0 time.Time) {
	t.Helper()
	o = newAudioOut(speaker.New())
	t0 = time.Now()
	o.ready, o.anchored, o.frame, o.at = true, true, 1000, t0
	o.played = heard
	o.write(toPCM(ramp(int(tailFrames)+4800)), t0)
	return o, t0
}

func toPCM(s []int16) []byte {
	b := make([]byte, 2*len(s))
	for i, v := range s {
		b[2*i], b[2*i+1] = byte(v), byte(v>>8)
	}
	return b
}

// frameTime is t0 plus that many frames of time.
func frameTime(t0 time.Time, frames int64) time.Time {
	return t0.Add(time.Duration(frames) * time.Second / speaker.Rate)
}

func render(o *audioOut, from uint64) { o.Render(from, make([]int16, speaker.Channels)) }

// A card that runs fast finds itself rendering frames the clock has not reached yet: frames are
// repeated until the two agree, and the anchor moves with them.
func TestDriftRepeatsFramesWhenEarly(t *testing.T) {
	o, t0 := listening(t)
	early := int64(4 * driftBand)
	o.now = func() time.Time { return frameTime(t0, -early) }
	for range 8 * driftBand {
		render(o, heard)
	}
	if o.corrected < early-2*driftBand || o.corrected > early {
		t.Fatalf("corrected %d frames, want most of %d", o.corrected, early)
	}
	if int64(o.frame) != 1000+o.corrected {
		t.Fatalf("anchor frame %d did not move with the %d frames repeated", o.frame, o.corrected)
	}
	first := int16(heard - 1000)
	for i := range int(o.corrected) + 1 {
		if o.pcm[i*speaker.Channels] != first {
			t.Fatalf("frame %d is %d, want a repeat of frame %d", i, o.pcm[i*speaker.Channels], first)
		}
	}
}

func TestDriftSkipsFramesWhenLate(t *testing.T) {
	o, t0 := listening(t)
	late := int64(4 * driftBand)
	o.now = func() time.Time { return frameTime(t0, late) }
	for range 8 * driftBand {
		render(o, heard)
	}
	if o.corrected > -(late-2*driftBand) || o.corrected < -late {
		t.Fatalf("corrected %d frames, want most of %d", o.corrected, -late)
	}
	if int64(o.frame) != 1000+o.corrected {
		t.Fatalf("anchor frame %d did not move with the %d frames skipped", o.frame, -o.corrected)
	}
	if want := int16(heard-1000) + int16(-o.corrected); o.pcm[0] != want {
		t.Fatalf("queue head is frame %d, want %d", o.pcm[0], want)
	}
}

func TestDriftSnapsLargeErrors(t *testing.T) {
	o, t0 := listening(t)
	o.now = func() time.Time { return frameTime(t0, -2400) }
	for range 800 {
		render(o, heard)
	}
	if o.corrected < 2400-2*driftBand || o.corrected > 2400+driftBand {
		t.Fatalf("corrected %d frames, want about 2400", o.corrected)
	}
	if o.pcm[0] != 0 || o.pcm[100*speaker.Channels] != 0 {
		t.Fatal("the snap did not insert silence at the head")
	}
}

// Inside the band nothing moves: correcting jitter would be worse than the jitter.
func TestDriftLeavesSmallErrorsAlone(t *testing.T) {
	o, t0 := listening(t)
	o.now = func() time.Time { return frameTime(t0, -10) }
	for range 400 {
		render(o, heard)
	}
	if o.corrected != 0 || o.frame != 1000 {
		t.Fatalf("corrected %d, frame %d", o.corrected, o.frame)
	}
}

// An error of seconds is a clock that changed, not drift: the next chunk anchors afresh.
func TestDriftReanchorsWhenFarOff(t *testing.T) {
	o, t0 := listening(t)
	o.now = func() time.Time { return frameTime(t0, -3*speaker.Rate) }
	render(o, heard)
	if o.anchored {
		t.Fatal("still anchored three seconds out")
	}
}
