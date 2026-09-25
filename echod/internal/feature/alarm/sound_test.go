package alarm

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Every alarm sound is one round that finishes before the next begins - a note pattern inside the time
// between rounds, a recording let finish by the ring - the default is Home Assistant's timer sound, the
// beeps alarms always rang with are still there, and a name nobody knows falls back to the default
// rather than silence.
func TestAlarmSounds(t *testing.T) {
	names := speaker.AlarmSounds()
	if len(names) < 2 {
		t.Fatalf("alarm sounds = %v, want a choice", names)
	}
	if soundName("") != names[0] || soundName("no such sound") != names[0] {
		t.Errorf("unset or unknown sound should be the default %q", names[0])
	}
	if names[0] != "Home Assistant" {
		t.Errorf("the default is %q, want Home Assistant's", names[0])
	}
	if len(speaker.AlarmSound("Beeps")) != len(speaker.ToneTimer) {
		t.Errorf("the beeps alarms always rang with are gone")
	}
	for _, n := range names {
		notes := speaker.AlarmSound(n)
		if len(notes) == 0 {
			t.Errorf("%s has no notes", n)
		}
		if l := speaker.Length(notes); l >= ring.RingEvery && notes[0].Clip == nil {
			t.Errorf("%s lasts %v, not under the %v between rounds", n, l, ring.RingEvery)
		}
		if soundName(n) != n {
			t.Errorf("soundName(%q) = %q", n, soundName(n))
		}
	}
}
