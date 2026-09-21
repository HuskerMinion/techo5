package home

import (
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// Starting the device's own radio without a screen.
//
// A station kept on the device is a name and a stream, played by the device with nothing else
// involved — which is the whole point of it, and which the Dot could not use: starting one happened
// from the radio page, the radio page is a screen, and the Dot has no screen. The device that most
// needs radio without Home Assistant was the one device that could not have it.
//
// So: OwnToggle, which the action button calls (own_button_dot.go). It plays the first station kept
// here, or the one playing before, and stops what is playing if something already is.

// OwnToggle starts the device's own radio, or stops what is playing. It reports what it did, for a
// caller that wants to say so.
func (f *Feature) OwnToggle() (started bool) {
	if playing, paused := media.Get().Playing(); playing || paused {
		slog.Info("home: stopping what is playing")
		media.Get().Stop()
		return false
	}
	return f.PlayOwn(f.nextOwn())
}

// PlayOwn starts a station kept on the device by name, whatever the radio page is set to show. It is
// the way in for anything that is not the radio page: the action button, and the setup page's own
// play buttons.
func (f *Feature) PlayOwn(name string) bool {
	if name == "" {
		return false
	}
	if !f.playOwn(name) {
		slog.Warn("home: no station of that name is kept on this device", "station", name)
		return false
	}
	f.Changed.Emit(struct{}{})
	f.pokeMeta()
	return true
}

// PlayStream plays a stream this device was handed directly — the setup page's play buttons, which
// carry whatever is typed in the row rather than what was last saved. Typing an address and hearing
// it is how somebody finds out they typed it wrong, which is the whole use of the button.
func (f *Feature) PlayStream(name, url string) bool {
	if !playableURL(url) {
		slog.Warn("home: that is not a stream address", "station", name, "url", url)
		return false
	}
	f.mu.Lock()
	f.chosen, f.listed, f.listedAt = name, "", time.Time{}
	f.asked, f.askedAt = name, time.Now()
	f.mu.Unlock()
	slog.Info("home: playing a station from the setup page", "station", name)
	media.Get().PlayURL(url)
	f.Changed.Emit(struct{}{})
	f.pokeMeta()
	return true
}

// NowPlaying is what the device is playing and whether it is playing anything, for a page that would
// otherwise be a remote control with no display.
func (f *Feature) NowPlaying() (station string, playing bool) {
	on, paused := media.Get().Playing()
	f.mu.Lock()
	defer f.mu.Unlock()
	name := f.urlName
	if name == "" {
		name = f.chosen
	}
	if name == "" {
		name = f.asked
	}
	return name, on || paused
}

// nextOwn is which station a press starts: the one this device played last, if it is still kept
// here, and otherwise the first in the list. Somebody with one station gets that one; somebody with
// several gets the one they were listening to.
func (f *Feature) nextOwn() string {
	stations := OwnStations()
	if len(stations) == 0 {
		return ""
	}
	f.mu.Lock()
	last := f.asked
	f.mu.Unlock()
	for _, s := range stations {
		if s.Name == last {
			return last
		}
	}
	return stations[0].Name
}
