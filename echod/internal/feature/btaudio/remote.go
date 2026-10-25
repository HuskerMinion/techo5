//go:build linux

package btaudio

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// A phone playing here is a received track whose sender the device can reach. A pause, a play or a
// skip on the device, from Home Assistant or by voice, goes to the phone over AVRCP, as AirPlay's
// goes to an iPhone over DACP (media.ReceiverControl), and a pause or a play on the phone is
// followed here (media.Player.ReceiverPaused). Without it, a pause here held the track while the
// phone played on into it, so playing again picked the song up wherever the phone had got to.

// avrcpMethods are MediaPlayer1's words for the controls the device passes on. The player settles a
// toggle and a stop into these before it asks.
var avrcpMethods = map[media.Transport]string{
	media.TransportPlay:     "Play",
	media.TransportPause:    "Pause",
	media.TransportNext:     "Next",
	media.TransportPrevious: "Previous",
}

// avrcpTimeout bounds one ask of a phone.
const avrcpTimeout = 5 * time.Second

// control is media's ReceiverControl for the phone at device, named name: what it gives back asks
// that phone for t when its turn comes, and says whether the phone took it.
func (f *Feature) control(device dbus.ObjectPath, name string) func(media.Transport) func() bool {
	return func(t media.Transport) func() bool {
		return func() bool {
			method, ok := avrcpMethods[t]
			if !ok {
				return false
			}
			f.mu.Lock()
			a := f.adapter
			f.mu.Unlock()
			if a == nil {
				return false
			}
			ctx, cancel := context.WithTimeout(context.Background(), avrcpTimeout)
			defer cancel()
			if err := a.Control(ctx, device, method); err != nil {
				slog.Info("bluetooth: the phone did not take a command", "device", name, "command", method, "err", err)
				return false
			}
			slog.Info("bluetooth: a command passed to the phone", "device", name, "command", method)
			f.taken(media.BluetoothPrefix+name, t)
			return true
		}
	}
}

// phoneKeptFor is how long a phone's track left paused is kept, its song named with play on it
// reaching the phone: as long as a track stopped from Home Assistant stays paused (media's
// stoppedFor), and an AirPlay sender that paused is waited for. A phone that has not played again
// by then has been put away: its track ends, rather than Home Assistant saying "paused" for as long
// as it stays connected.
const phoneKeptFor = 30 * time.Minute

// phoneKeptAfter starts the timer for that, and phoneKeptGone ends the track when it runs out;
// variables for the tests.
var (
	phoneKeptAfter = time.AfterFunc
	phoneKeptGone  = func(item string) { media.Get().ReceiverGone(item) }
)

// followed is what follows each phone's player change (playerChanged); a variable for the tests.
var followed = (*Feature).playerChanged

// followPlayer hands st on to be followed on a goroutine of its own, after the changes heard before it
// and in the order they came, and returns at once: it is heard on the BlueZ signal goroutine, whose
// listeners must not block (bluez.Adapter.PlayerChanged), and following a pause tells Home Assistant
// the media player's state, a write to the network that can stall. A change still waiting for the same
// phone gives way to st, which goes to the back: the latest is what the phone is doing, and with Home
// Assistant stalled the queue holds one change for each phone at most rather than growing. Only a
// change playerChanged follows (playing, paused, stopped) is queued: a seek or an error coming after
// a pause still waiting would otherwise take its place and be ignored, and the pause lost.
func (f *Feature) followPlayer(st bluez.PlayerStatus) {
	switch st.Status {
	case "playing", "paused", "stopped":
	default:
		return
	}
	f.statusMu.Lock()
	f.statuses = slices.DeleteFunc(f.statuses, func(w bluez.PlayerStatus) bool { return w.Device == st.Device })
	f.statuses = append(f.statuses, st)
	if f.following {
		f.statusMu.Unlock()
		return
	}
	f.following = true
	f.statusMu.Unlock()
	safe.Go("bluetooth follow a phone", f.follow)
}

// follow follows what is waiting until nothing is. following goes false in the same hold of statusMu
// that finds nothing waiting, so a change just after it starts another; one that panics is let go of
// here, so the rest are still followed.
func (f *Feature) follow() {
	for {
		f.statusMu.Lock()
		if len(f.statuses) == 0 {
			f.following = false
			f.statusMu.Unlock()
			return
		}
		st := f.statuses[0]
		f.statuses = f.statuses[1:]
		f.statusMu.Unlock()
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("recovered from a panic", "in", "bluetooth follow a phone", "panic", r)
				}
			}()
			followed(f, st)
		}()
	}
}

// playerChanged follows a phone that paused or played again on its own: the track here pauses with
// it and plays again with it, so a play on the phone after a pause from here is heard. A phone
// whose track is not the one playing here is left alone (ReceiverPaused). A pause starts
// phoneKeptFor, and a play stops it.
func (f *Feature) playerChanged(st bluez.PlayerStatus) {
	var paused bool
	switch st.Status {
	case "playing":
	case "paused", "stopped":
		paused = true
	default:
		return // seeking, or an error: nothing to follow
	}
	f.mu.Lock()
	a := f.adapter
	f.mu.Unlock()
	if a == nil {
		return
	}
	for _, d := range a.Devices() {
		if d.Path == st.Device {
			// Here, one change at a time (followPlayer): a pause and a play close together stay in order.
			item := media.BluetoothPrefix + displayName(d)
			media.Get().ReceiverPaused(item, paused)
			f.keep(item, paused)
			return
		}
	}
}

// taken is a command the phone at item took: a pause starts phoneKeptFor as the phone's own pause
// does, so a phone that never says it paused is not kept paused for good, and a play stops it.
func (f *Feature) taken(item string, t media.Transport) {
	switch t {
	case media.TransportPause:
		f.keep(item, true)
	case media.TransportPlay:
		f.keep(item, false)
	}
}

// keep starts phoneKeptFor for item's track, paused, or stops it, playing: one wait for each phone,
// so another phone's pause or play leaves this one's alone. When it runs out, the track ends if it
// is still item's and still paused (ReceiverGone).
func (f *Feature) keep(item string, paused bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.kept == nil {
		f.kept = map[string]*time.Timer{}
	}
	if t := f.kept[item]; t != nil {
		t.Stop()
		delete(f.kept, item)
	}
	if paused {
		var t *time.Timer
		t = phoneKeptAfter(phoneKeptFor, func() {
			// A wait replaced or stopped just as it ran out, its run already waiting on mu, is over.
			f.mu.Lock()
			if f.kept[item] != t {
				f.mu.Unlock()
				return
			}
			delete(f.kept, item)
			f.mu.Unlock()
			slog.Info("bluetooth: a phone paused this long is taken as put away", "device", item, "after", phoneKeptFor)
			phoneKeptGone(item)
		})
		f.kept[item] = t
	}
}

// letGoKept stops the waits of the phones whose track is not the one loaded here, cur: it ended some
// other way (a stop, the phone playing on after a pause, the phone gone), or it was never this
// phone's. A phone's track only comes back playing, which needs no wait, so there is nothing left
// for one to end.
func (f *Feature) letGoKept(cur string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for item, t := range f.kept {
		if item != cur {
			t.Stop()
			delete(f.kept, item)
		}
	}
}

// goneFrom ends a phone's track left paused here once the phone is no longer connected: nothing
// will play it again (ReceiverGone). A track playing, or one that is not a phone's, is left as it
// is.
func goneFrom(devices []bluez.Device) {
	from := media.Get().Receiving()
	if !strings.HasPrefix(from, media.BluetoothPrefix) {
		return
	}
	for _, d := range devices {
		if d.Connected && media.BluetoothPrefix+displayName(d) == from {
			return
		}
	}
	media.Get().ReceiverGone(from)
}

// playingToNobody is the phones playing music that do not have the speaker while something else
// does: Music Assistant, AirPlay, Spotify Connect, DLNA, a station of the device's own, or another
// phone. Paused rather than left playing, a phone does not share the device's one radio with the
// one heard, which stutters when two send at once, and playing it again takes the speaker back, as
// any phone that starts (choose). With the speaker free, there is nobody else to have it.
func playingToNobody(sending []received, receiving string, playing, paused, external bool) []received {
	if receiving == "" && !playing && !paused && !external {
		return nil
	}
	var out []received
	for _, r := range sending {
		if r.item != receiving {
			out = append(out, r)
		}
	}
	return out
}

// pauseAway pauses each phone playing to nobody (playingToNobody), once for as long as it is: so
// once whatever took the speaker has finished, playing it again picks the song up where it was
// taken. A phone that is no longer playing to nobody (it paused, went, or took the speaker back) is
// asked again the next time it is. It returns the phones it asked.
func (f *Feature) pauseAway(away []received) []received {
	f.mu.Lock()
	still := map[string]bool{}
	for _, r := range away {
		still[r.item] = true
	}
	for item := range f.pausedAway {
		if !still[item] {
			delete(f.pausedAway, item)
		}
	}
	var ask []received
	for _, r := range away {
		if !f.pausedAway[r.item] {
			f.pausedAway[r.item] = true
			ask = append(ask, r)
		}
	}
	f.mu.Unlock()
	for _, r := range ask {
		slog.Info("bluetooth: the speaker was taken from a phone still playing; pausing it", "device", displayName(r.dev))
		pause := f.control(r.dev.Path, displayName(r.dev))(media.TransportPause)
		safe.Go("bluetooth pause a phone", func() { pause() })
	}
	return ask
}
