//go:build linux

package btaudio

import (
	"time"

	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// A phone still sending is playing to nobody once the speaker is something else's that is not a
// phone; not while another phone has it (that one owes it back), and not with the speaker free.
func TestWhoIsPlayingToNobody(t *testing.T) {
	pixel := received{item: media.BluetoothPrefix + "Pixel"}
	sending := []received{pixel}
	for name, c := range map[string]struct {
		receiving               string
		playing, paused, remote bool
		want                    int
	}{
		"Music Assistant took it":    {"", false, false, true, 1},
		"AirPlay took it":            {"AirPlay", true, false, false, 1},
		"a station of the device's":  {"", true, false, false, 1},
		"a station paused here":      {"", false, true, false, 1},
		"another phone has it":       {media.BluetoothPrefix + "Redmi", true, false, false, 0},
		"its own track, paused here": {pixel.item, false, true, false, 0},
		"the speaker is free":        {"", false, false, false, 0},
	} {
		got := playingToNobody(sending, c.receiving, c.playing, c.paused, c.remote)
		if len(got) != c.want {
			t.Errorf("%s: %d phones playing to nobody, want %d", name, len(got), c.want)
		}
	}
}

// A phone playing to nobody is asked to pause once for as long as it is, even if it does not, and
// again the next time it is.
func TestAPhoneIsAskedToPauseOnce(t *testing.T) {
	f := &Feature{pausedAway: map[string]bool{}}
	pixel := []received{{item: media.BluetoothPrefix + "Pixel"}}

	if got := f.pauseAway(pixel); len(got) != 1 {
		t.Fatalf("first look asked %d phones, want 1", len(got))
	}
	if got := f.pauseAway(pixel); len(got) != 0 {
		t.Errorf("a phone still playing to nobody was asked again")
	}
	f.pauseAway(nil) // it paused, or took the speaker back
	if got := f.pauseAway(pixel); len(got) != 1 {
		t.Errorf("a phone playing to nobody again was not asked, %d", len(got))
	}
}

// A phone's paused track is kept phoneKeptFor: a pause starts the wait, a play stops it, and a
// pause again starts it afresh.
func TestAPausedPhoneIsKeptAWhile(t *testing.T) {
	var armed []time.Duration
	var timers []*time.Timer
	phoneKeptAfter = func(d time.Duration, fn func()) *time.Timer {
		armed = append(armed, d)
		tm := time.AfterFunc(time.Hour, fn)
		timers = append(timers, tm)
		return tm
	}
	t.Cleanup(func() { phoneKeptAfter = time.AfterFunc })
	f := &Feature{}
	item := media.BluetoothPrefix + "Pixel"

	f.keep(item, true)
	if len(armed) != 1 || armed[0] != phoneKeptFor || f.kept == nil {
		t.Fatalf("a pause armed %v, want one wait of %v", armed, phoneKeptFor)
	}
	f.keep(item, false)
	if f.kept != nil || timers[0].Stop() {
		t.Error("a play left the wait running")
	}
	f.keep(item, true)
	if len(armed) != 2 || f.kept == nil {
		t.Errorf("a pause again armed %d waits, want 2", len(armed))
	}
	f.kept.Stop()
}
