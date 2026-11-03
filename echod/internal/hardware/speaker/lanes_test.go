package speaker

import (
	"context"
	"encoding/binary"
	"testing"
	"time"
)

// mixed fills one period with a track in the bed, a reply in the queue and a far end in the underlay
// (any of them 0 for none), at a media step, with the voice and call offsets, the night ceiling and
// what the queue carries. It returns the level in the room of the first sample.
func mixed(media, voiceOff, callOff, ceiling, role int, bed, queue, under int16) float64 {
	p := &Player{}
	p.SetVolume(media)
	p.SetLaneOffsets(voiceOff, callOff)
	p.SetLaneCeiling(ceiling)
	p.SetRole(role)
	lane := func(v int16) []int16 {
		if v == 0 {
			return nil
		}
		s := make([]int16, period*Channels)
		for i := range s {
			s[i] = v
		}
		return s
	}
	p.bed, p.pending, p.under = lane(bed), lane(queue), lane(under)
	buf := make([]byte, period*Channels*2)
	p.fill(buf)
	return float64(int16(binary.LittleEndian.Uint16(buf))) * roomGain()
}

// A device that never set a voice or a call volume plays every lane at the volume, whatever the queue
// carries: the whole of the feature is that this does not change.
func TestLanesAtTheVolumeSoundAsTheyAlwaysDid(t *testing.T) {
	for _, step := range []int{1, 9, 20, 30} {
		want := mixed(step, 0, 0, 0, RoleMedia, 3000, 4000, 2000)
		for _, role := range []int{RoleVoice, RoleCall} {
			if got := mixed(step, 0, 0, 0, role, 3000, 4000, 2000); got != want {
				t.Errorf("step %d role %d: %g, want %g", step, role, got, want)
			}
		}
		// And a night ceiling changes nothing while the offsets are zero.
		if got := mixed(step, 0, 0, 5, RoleVoice, 3000, 4000, 2000); got != want {
			t.Errorf("step %d with a ceiling: %g, want %g", step, got, want)
		}
	}
}

// Quiet replies over loud music: the reply comes out as it would alone at the lower step, and the
// music where the volume put it.
func TestAQuieterVoiceOverLoudMusic(t *testing.T) {
	both := mixed(24, -14, 0, 0, RoleVoice, 3000, 3000, 0)
	music := mixed(24, 0, 0, 0, RoleMedia, 3000, 0, 0)
	reply := mixed(10, 0, 0, 0, RoleMedia, 0, 3000, 0)
	if !near(both, music+reply) {
		t.Errorf("music at 24 and a reply 14 steps under: %g, want %g + %g", both, music, reply)
	}
	if alone := mixed(24, -14, 0, 0, RoleVoice, 0, 3000, 0); !near(alone, reply) {
		t.Errorf("a reply alone 14 steps under 24: %g, want %g as at step 10", alone, reply)
	}
}

// Loud replies over quiet music: the output rises to the reply, and the music stays at its own step.
func TestALouderVoiceOverQuietMusic(t *testing.T) {
	both := mixed(4, 16, 0, 0, RoleVoice, 3000, 3000, 0)
	music := mixed(4, 0, 0, 0, RoleMedia, 3000, 0, 0)
	reply := mixed(20, 0, 0, 0, RoleMedia, 0, 3000, 0)
	if !near(both, music+reply) {
		t.Errorf("music at 4 and a reply 16 steps over: %g, want %g + %g", both, music, reply)
	}
	if reply <= music {
		t.Errorf("the louder reply (%g) is not louder than the music (%g)", reply, music)
	}
}

// A call's audio follows the call volume, in the queue while the call holds the speaker and in the
// underlay under a reply; the voice volume touches neither, and the call volume leaves a reply alone.
func TestACallHasItsOwnVolume(t *testing.T) {
	step6 := mixed(6, 0, 0, 0, RoleMedia, 0, 3000, 0)
	if got := mixed(20, 5, -14, 0, RoleCall, 0, 3000, 0); !near(got, step6) {
		t.Errorf("the call's queue at call volume -14 under 20: %g, want %g as at step 6", got, step6)
	}
	if got := mixed(20, 5, -14, 0, RoleVoice, 0, 0, 3000); !near(got, step6) {
		t.Errorf("the call's underlay under a reply: %g, want %g as at step 6", got, step6)
	}
	if got, want := mixed(20, 5, -14, 0, RoleVoice, 0, 3000, 0), mixed(25, 0, 0, 0, RoleMedia, 0, 3000, 0); !near(got, want) {
		t.Errorf("a reply with the call volume set: %g, want %g as at step 25", got, want)
	}
	if got, want := mixed(20, -5, 0, 0, RoleCall, 0, 3000, 0), mixed(20, 0, 0, 0, RoleMedia, 0, 3000, 0); !near(got, want) {
		t.Errorf("a call with only the voice volume set: %g, want %g", got, want)
	}
}

// Through quiet hours a raise stops at the night volume, or where the volume already is above it; a
// lower voice is never raised, and a muted volume is silent whatever the offsets say.
func TestTheNightVolumeCapsTheLanes(t *testing.T) {
	for _, c := range []struct{ media, off, ceiling, want int }{
		{5, 15, 8, 8},   // raised, held at the night volume
		{5, 2, 8, 7},    // under it: as asked
		{14, 3, 8, 14},  // somebody turned it up by hand: no further
		{14, -4, 8, 10}, // down: always
		{5, 15, 0, 20},  // no night volume
		{0, 15, 8, 0},   // muted or at zero
		{25, 15, 0, 30}, // never past the top
		{3, -9, 0, 0},   // never below nothing
	} {
		if got := LaneStep(c.media, c.off, c.ceiling); got != c.want {
			t.Errorf("LaneStep(%d, %d, %d) = %d, want %d", c.media, c.off, c.ceiling, got, c.want)
		}
	}
	if got, want := mixed(5, 15, 0, 8, RoleVoice, 0, 3000, 0), mixed(8, 0, 0, 0, RoleMedia, 0, 3000, 0); !near(got, want) {
		t.Errorf("a reply raised at night: %g, want %g as at step 8", got, want)
	}
	if got := mixed(0, 15, 15, 0, RoleVoice, 0, 3000, 3000); got != 0 {
		t.Errorf("a muted device played at %g", got)
	}
}

// A call, its ring and a drop in are made with ClaimCall and play at the call volume whatever they are
// named; a camera's own sound over the music (ClaimOver) plays at the volume; the rest of what a claim
// says is the assistant's voice; nothing holding the speaker is the device's own sounds, on the volume.
func TestWhatAClaimIsForItsVolume(t *testing.T) {
	d := driver()
	play := func(context.Context, *Player) error { return nil }
	for name, c := range map[string]*Claim{
		"call":   d.ClaimCall("renamed", play),
		"plain":  d.Claim("call", play),
		"speech": d.ClaimSpeech("call", play),
		"over":   d.ClaimOver("call", play),
		"turn":   d.ClaimTurn("call", time.Second, play),
	} {
		want := RoleVoice
		switch name {
		case "call":
			want = RoleCall
		case "over":
			want = RoleMedia
		}
		if got := roleOf(c); got != want {
			t.Errorf("%s: role %d, want %d", name, got, want)
		}
	}
	if roleOf(nil) != RoleMedia {
		t.Error("no claim should leave the queue on the volume")
	}
}
