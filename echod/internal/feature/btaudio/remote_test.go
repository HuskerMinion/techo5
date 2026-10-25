//go:build linux

package btaudio

import (
	"fmt"
	"time"

	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
)

// A phone playing is playing to nobody once the speaker is something else's, another phone's
// included; not when it is its own, and not with the speaker free.
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
		"another phone has it":       {media.BluetoothPrefix + "Redmi", true, false, false, 1},
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

// A wait replaced just as it ran out, its run already under way, ends nothing: only the wait now
// running for the phone does.
func TestAReplacedWaitEndsNothing(t *testing.T) {
	var runs []func()
	phoneKeptAfter = func(d time.Duration, fn func()) *time.Timer {
		runs = append(runs, fn)
		return time.AfterFunc(time.Hour, func() {})
	}
	var gone []string
	phoneKeptGone = func(item string) { gone = append(gone, item) }
	t.Cleanup(func() {
		phoneKeptAfter = time.AfterFunc
		phoneKeptGone = func(item string) { media.Get().ReceiverGone(item) }
	})
	f := &Feature{}
	pixel := media.BluetoothPrefix + "Pixel"

	f.keep(pixel, true)
	f.keep(pixel, true) // a second pause as the first wait ran out
	runs[0]()           // the first wait's run, which was waiting on mu
	if len(gone) != 0 {
		t.Fatal("a wait that was replaced ended the track")
	}
	f.mu.Lock()
	still := f.kept[pixel] != nil
	f.mu.Unlock()
	if !still {
		t.Fatal("a wait that was replaced took the new one with it")
	}
	runs[1]()
	if len(gone) != 1 || gone[0] != pixel {
		t.Errorf("the wait running out ended %v, want the phone's track", gone)
	}
}

// A phone's wait goes with its track: once the track loaded here is another's, or none, the waits of
// the phones whose track ended some other way are stopped; the one for the track loaded stays.
func TestAWaitGoesWithItsTrack(t *testing.T) {
	phoneKeptAfter = func(d time.Duration, fn func()) *time.Timer { return time.AfterFunc(time.Hour, fn) }
	t.Cleanup(func() { phoneKeptAfter = time.AfterFunc })
	f := &Feature{}
	pixel, redmi := media.BluetoothPrefix+"Pixel", media.BluetoothPrefix+"Redmi"
	f.keep(pixel, true)
	f.keep(redmi, true)
	pt := f.kept[pixel]

	f.letGoKept(redmi)
	if f.kept[pixel] != nil || f.kept[redmi] == nil {
		t.Fatalf("waits left %v, want only the loaded track's", f.kept)
	}
	if pt.Stop() {
		t.Error("the ended track's timer was left running")
	}
	f.letGoKept("")
	if len(f.kept) != 0 {
		t.Errorf("with nothing loaded, waits left %v", f.kept)
	}
}

// A phone's player changes are followed off the goroutine they are heard on, one at a time and in
// the order they came, however long following one takes.
func TestPlayerChangesAreFollowedInOrderWithoutBlocking(t *testing.T) {
	release := make(chan struct{})
	got := make(chan string, 4)
	was := followed
	followed = func(_ *Feature, st bluez.PlayerStatus) {
		if st.Status == "paused" {
			<-release // a write to Home Assistant that stalls
		}
		got <- st.Status
	}
	t.Cleanup(func() { followed = was })
	f := &Feature{}
	done := make(chan struct{})
	go func() {
		for i, st := range []string{"paused", "playing", "paused"} { // three phones'
			f.followPlayer(bluez.PlayerStatus{Device: dbus.ObjectPath(fmt.Sprintf("/dev%d", i)), Status: st})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("hearing a change waited for the one before it to be followed")
	}
	close(release)
	for _, want := range []string{"paused", "playing", "paused"} {
		select {
		case st := <-got:
			if st != want {
				t.Fatalf("followed %s, want %s", st, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s was never followed", want)
		}
	}
}

// A phone's changes waiting behind a stalled one give way to its latest, so the queue stays one change
// for each phone however long following stalls; another phone's change keeps its place.
func TestWaitingPlayerChangesAreOneForEachPhone(t *testing.T) {
	release, stalled := make(chan struct{}), make(chan struct{})
	got := make(chan bluez.PlayerStatus, 16)
	was := followed
	followed = func(_ *Feature, st bluez.PlayerStatus) {
		if st.Status == "stopped" {
			close(stalled)
			<-release
		}
		got <- st
	}
	t.Cleanup(func() { followed = was })
	f := &Feature{}
	f.followPlayer(bluez.PlayerStatus{Device: "/a", Status: "stopped"})
	<-stalled // under way, not waiting
	for _, st := range []bluez.PlayerStatus{{Device: "/a", Status: "paused"}, {Device: "/b", Status: "paused"},
		{Device: "/a", Status: "playing"}, {Device: "/a", Status: "paused"}} {
		f.followPlayer(st)
	}
	f.statusMu.Lock()
	waiting := len(f.statuses)
	f.statusMu.Unlock()
	close(release)
	want := []bluez.PlayerStatus{{Device: "/a", Status: "stopped"}, {Device: "/b", Status: "paused"}, {Device: "/a", Status: "paused"}}
	for _, w := range want {
		select {
		case st := <-got:
			if st != w {
				t.Fatalf("followed %+v, want %+v", st, w)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%+v was never followed", w)
		}
	}
	if waiting > 2 {
		t.Errorf("%d changes waiting, want one for each phone", waiting)
	}
}

// A seek or an error from a phone does not take the place of its pause still waiting: only what is
// followed is queued.
func TestASeekDoesNotTakeAWaitingPausesPlace(t *testing.T) {
	release, stalled := make(chan struct{}), make(chan struct{})
	got := make(chan bluez.PlayerStatus, 16)
	was := followed
	followed = func(_ *Feature, st bluez.PlayerStatus) {
		if st.Device == "/b" {
			close(stalled)
			<-release
		}
		got <- st
	}
	t.Cleanup(func() { followed = was })
	f := &Feature{}
	f.followPlayer(bluez.PlayerStatus{Device: "/b", Status: "playing"})
	<-stalled
	for _, st := range []string{"paused", "forward-seek", "reverse-seek", "error"} {
		f.followPlayer(bluez.PlayerStatus{Device: "/a", Status: st})
	}
	close(release)
	for _, want := range []bluez.PlayerStatus{{Device: "/b", Status: "playing"}, {Device: "/a", Status: "paused"}} {
		select {
		case st := <-got:
			if st != want {
				t.Fatalf("followed %+v, want %+v", st, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%+v was never followed", want)
		}
	}
	select {
	case st := <-got:
		t.Fatalf("followed %+v as well", st)
	case <-time.After(50 * time.Millisecond):
	}
}
