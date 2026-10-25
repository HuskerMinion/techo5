package speaker

import (
	"encoding/binary"
	"math"
	"testing"
)

// rang fills one period from a Player built for the test: media queued at one volume, a chime on
// the bell at another, and whether a ring is holding the bell's level. What comes back is the level
// in the room: what was written, at the device's own volume on top (roomGain), which is unity on an
// Echo and Android's level on a panel.
func rang(t *testing.T, mediaStep, bellStep int, media, chime int16, ringing bool) []float64 {
	t.Helper()
	p := &Player{}
	p.SetVolume(mediaStep)
	p.bellStep.Store(int32(bellStep))
	p.ringing.Store(ringing)
	if media != 0 {
		p.pending = make([]int16, period*Channels)
		for i := range p.pending {
			p.pending[i] = media
		}
	}
	if chime != 0 {
		p.bell = make([]int16, period*Channels)
		for i := range p.bell {
			p.bell[i] = chime
		}
	}
	buf := make([]byte, period*Channels*2)
	p.fill(buf)
	g := roomGain()
	out := make([]float64, period*Channels)
	for i := range out {
		out[i] = float64(int16(binary.LittleEndian.Uint16(buf[i*2:]))) * g
	}
	return out
}

// near allows for the rounding to a sample: a step of it, or on a panel a share of the level
// (roomShare), where a level scaled in software and one scaled by the device round apart by more.
func near(a, b float64) bool {
	d := math.Abs(a - b)
	return d <= 1 || d <= roomShare*math.Max(math.Abs(a), math.Abs(b))
}

// The reason the ring has a level of its own: music turned all the way down must not take the alarm
// with it, and must not come back up under it either.
func TestARingIsHeardOverMutedMusic(t *testing.T) {
	both := rang(t, 0, 20, 100, 100, true)
	bell := rang(t, 0, 20, 0, 100, true)
	if both[0] == 0 {
		t.Fatal("the ring was silent with the music turned down")
	}
	if !near(both[0], bell[0]) {
		t.Errorf("with the music muted the output was %g, the ring alone %g: the music came up under it", both[0], bell[0])
	}
}

// A ring louder than the music raises the output, and the music under it stays where it was set.
func TestTheMusicKeepsItsLevelUnderALouderRing(t *testing.T) {
	under := rang(t, 10, 25, 10000, 0, true)
	alone := rang(t, 10, 25, 10000, 0, false)
	if !near(under[0], alone[0]) {
		t.Errorf("music at step 10 came out at %g during a ring and %g without one", under[0], alone[0])
	}
}

// A ring quieter than the music comes out at its own level, not the music's.
func TestAQuietRingUnderLoudMusic(t *testing.T) {
	loud := rang(t, 30, 30, 0, 10000, true)
	quiet := rang(t, 30, 5, 0, 10000, true)
	if quiet[0] >= loud[0] {
		t.Errorf("a ring at step 5 came out at %g, no quieter than one at step 30 (%g)", quiet[0], loud[0])
	}
	if want := rang(t, 5, 5, 0, 10000, true); !near(quiet[0], want[0]) {
		t.Errorf("a ring at step 5 under music at 30 came out at %g, want %g as it would alone", quiet[0], want[0])
	}
}

// A ring set to silent is silent, whatever the music is doing.
func TestASilentRingIsSilent(t *testing.T) {
	if out := rang(t, 20, 0, 0, 100, true); out[0] != 0 {
		t.Errorf("a ring at step 0 came out at %g", out[0])
	}
}

// Nothing ringing: the music plays exactly as it did before there was a bell.
func TestNoRingLeavesTheMusicAlone(t *testing.T) {
	out := rang(t, 15, 30, 100, 0, false)
	p := &Player{}
	p.SetVolume(15)
	want := float64(limit(float32(100)*OutputBoost*p.Volume())) * roomGain()
	if out[0] != want {
		t.Errorf("music with no ring came out at %g, want %g", out[0], want)
	}
}
