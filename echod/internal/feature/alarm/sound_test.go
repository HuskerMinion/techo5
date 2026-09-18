package alarm

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Every alarm sound is one round short enough to finish before the next begins, the default is the
// beeps alarms have always rung with, and a name nobody knows falls back to it rather than silence.
func TestAlarmSounds(t *testing.T) {
	names := speaker.AlarmSounds()
	if len(names) < 2 {
		t.Fatalf("alarm sounds = %v, want a choice", names)
	}
	if soundName("") != names[0] || soundName("no such sound") != names[0] {
		t.Errorf("unset or unknown sound should be the default %q", names[0])
	}
	if names[0] != "Beeps" || len(speaker.AlarmSound("Beeps")) != len(speaker.ToneTimer) {
		t.Errorf("the default should be the timer's beeps, as alarms always rang")
	}
	for _, n := range names {
		notes := speaker.AlarmSound(n)
		if len(notes) == 0 {
			t.Errorf("%s has no notes", n)
		}
		if l := speaker.Length(notes); l >= ringEvery {
			t.Errorf("%s lasts %v, not under the %v between rounds", n, l, ringEvery)
		}
		if soundName(n) != n {
			t.Errorf("soundName(%q) = %q", n, soundName(n))
		}
	}
}
