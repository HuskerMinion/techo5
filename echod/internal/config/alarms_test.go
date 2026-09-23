package config

import "testing"

// Snooze length is held to what the screen and Home Assistant offer, whatever is asked for, and the
// alarm sound is kept by name.
func TestAlarmRingSettings(t *testing.T) {
	st := load(t)
	if got := st.Get().Alarms.Snooze(); got != DefaultSnoozeMinutes {
		t.Fatalf("snooze before any is set = %d, want the default %d", got, DefaultSnoozeMinutes)
	}
	for _, c := range []struct{ set, want int }{
		{5, 5}, {0, MinSnoozeMinutes}, {-3, MinSnoozeMinutes}, {90, MaxSnoozeMinutes}, {MaxSnoozeMinutes, MaxSnoozeMinutes},
	} {
		if err := st.Set().Alarms().SnoozeMinutes(c.set); err != nil {
			t.Fatal(err)
		}
		if got := st.Get().Alarms.Snooze(); got != c.want {
			t.Errorf("snooze set to %d reads %d, want %d", c.set, got, c.want)
		}
	}

	if err := st.Set().Alarms().Sound("Bells"); err != nil {
		t.Fatal(err)
	}
	if got := st.Get().Alarms.Sound; got != "Bells" {
		t.Errorf("sound = %q, want Bells", got)
	}
}

// The ring's own level starts from the media volume, never from a silent one, and once set it is
// kept as set, zero included: a silent ring somebody chose is theirs to choose.
func TestRingVolume(t *testing.T) {
	st := load(t)
	for _, c := range []struct{ media, want int }{{7, 7}, {0, DefaultVolume}, {-1, DefaultVolume}, {99, VolumeSteps}} {
		if got := st.Get().Alarms.Ring(c.media); got != c.want {
			t.Errorf("unset ring volume with the media at %d = %d, want %d", c.media, got, c.want)
		}
	}
	for _, c := range []struct{ set, want int }{{12, 12}, {0, 0}, {-4, 0}, {40, VolumeSteps}} {
		if err := st.Set().Alarms().RingVolume(c.set); err != nil {
			t.Fatal(err)
		}
		if got := st.Get().Alarms.Ring(3); got != c.want {
			t.Errorf("ring volume set to %d reads %d, want %d", c.set, got, c.want)
		}
	}
}
