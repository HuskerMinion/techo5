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

// A phone's paused track is kept phoneKeptFor, one wait for each phone: a pause, the phone's own or
// one it took from here, starts it, a play stops it, and another phone's pause or play leaves it.
func TestAPausedPhoneIsKeptAWhile(t *testing.T) {
	armed := 0
	phoneKeptAfter = func(d time.Duration, fn func()) *time.Timer {
		if d != phoneKeptFor {
			t.Errorf("a wait of %v, want %v", d, phoneKeptFor)
		}
		armed++
		return time.AfterFunc(time.Hour, fn)
	}
	t.Cleanup(func() { phoneKeptAfter = time.AfterFunc })
	f := &Feature{}
	pixel, redmi := media.BluetoothPrefix+"Pixel", media.BluetoothPrefix+"Redmi"
	waiting := func(item string) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.kept[item] != nil
	}

	f.taken(pixel, media.TransportPause) // a pause from here the phone took
	if !waiting(pixel) || armed != 1 {
		t.Fatalf("a pause the phone took started %d waits, want 1", armed)
	}
	f.keep(redmi, true)
	f.keep(redmi, false)
	if !waiting(pixel) {
		t.Error("another phone's pause and play stopped this one's wait")
	}
	f.taken(pixel, media.TransportNext)
	if !waiting(pixel) || armed != 2 {
		t.Errorf("a skip touched the wait (%d started)", armed)
	}
	f.keep(pixel, false) // the phone's own play
	if waiting(pixel) {
		t.Error("a play left the wait running")
	}
}
