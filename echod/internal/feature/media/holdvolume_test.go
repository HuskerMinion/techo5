package media

import "testing"

// An alarm's music is held at the ring volume (#651) over a mute and a low volume, the volume itself is
// left as it was, a change to it while held is not heard until release, and release puts it back.
func TestHoldVolume(t *testing.T) {
	var heard []int
	was := speakerVolume
	speakerVolume = func(step int) { heard = append(heard, step) }
	t.Cleanup(func() { speakerVolume = was })
	last := func() int { return heard[len(heard)-1] }

	p := &Player{}
	p.step.Store(3)
	p.muted.Store(true)
	release := p.HoldVolume(12)
	if last() != 12 || p.Volume() != 3 {
		t.Fatalf("held at %d, volume %d", last(), p.Volume())
	}
	p.muted.Store(false)
	p.step.Store(5) // somebody turned it up while the alarm rang
	p.toSpeaker()
	if last() != 12 {
		t.Errorf("a change while held was heard: %d", last())
	}
	release()
	release()
	if last() != 5 || len(heard) != 3 {
		t.Errorf("after release the speaker is at %d (%v)", last(), heard)
	}
}
