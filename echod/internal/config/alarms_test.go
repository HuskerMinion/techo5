package config

import (
	"path/filepath"
	"testing"
	"time"
)

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

// The record of the device running comes back as written, and none is none.
func TestAlive(t *testing.T) {
	Use(filepath.Join(t.TempDir(), "state.json"))
	if _, ok := Alive(); ok {
		t.Fatal("alive before anything was written")
	}
	when := time.Unix(1_790_000_000, 0)
	if err := KeepAlive(when); err != nil {
		t.Fatal(err)
	}
	if got, ok := Alive(); !ok || !got.Equal(when) {
		t.Errorf("alive = %v %v, want %v", got, ok, when)
	}
}

// An alarm set to repeat keeps no date, so setting it back to once cannot bring an old day back.
func TestARepeatingAlarmKeepsNoDate(t *testing.T) {
	st := load(t)
	later := time.Now().AddDate(0, 0, 6).Format(DateLayout)
	if err := st.Set().Alarms().Put(Alarm{ID: "a", Hour: 7, Days: DaysWeekdays, Date: later, On: true}); err != nil {
		t.Fatal(err)
	}
	if got := st.Get().Alarms.List[0].Date; got != "" {
		t.Errorf("a repeating alarm kept the date %q", got)
	}
	if err := st.Set().Alarms().Put(Alarm{ID: "b", Hour: 7, Days: DaysOnce, Date: later, On: true}); err != nil {
		t.Fatal(err)
	}
	if got := st.Get().Alarms.List[1].Date; got != later {
		t.Errorf("a one-off lost its date: %q", got)
	}
}

// A dated one-off rings once and is turned off with its date kept. Turned back on, the date is gone by,
// and it has to become a plain one-off rather than an alarm that shows on and can never ring.
func TestADatedOneOffTurnedBackOnLosesItsPastDate(t *testing.T) {
	st := load(t)
	gone := time.Now().AddDate(0, 0, -2).Format(DateLayout)
	if err := st.Set().Alarms().Put(Alarm{ID: "a", Hour: 7, Days: DaysOnce, Date: gone, On: false}); err != nil {
		t.Fatal(err)
	}
	if got := st.Get().Alarms.List[0].Date; got != gone {
		t.Errorf("turning it off lost the date it rang on: %q", got)
	}
	al := st.Get().Alarms.List[0]
	al.On = true
	if err := st.Set().Alarms().Put(al); err != nil {
		t.Fatal(err)
	}
	if got := st.Get().Alarms.List[0]; got.Date != "" {
		t.Errorf("turned back on, it kept a date that has gone by: %q", got.Date)
	}
}
