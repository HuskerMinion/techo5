package voice

import "testing"

func TestWhatStopsARing(t *testing.T) {
	for _, s := range []string{"Stop.", "stop the alarm", "Stop alarm", "turn off the timer", "Turn it off, please.", "stop ringing", "OK stop"} {
		if !stopsRing(s) {
			t.Errorf("%q should stop a ring", s)
		}
	}
	for _, s := range []string{"", "stop the music", "what time is it", "turn off the lights", "the alarm", "snooze"} {
		if stopsRing(s) {
			t.Errorf("%q should not stop a ring", s)
		}
	}
}
