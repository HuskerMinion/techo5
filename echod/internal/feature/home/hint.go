package home

import (
	"log/slog"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// A station asked for that Home Assistant never started, and a star with nothing behind it.
//
// A favorite plays through the radio script, which the device calls over its ESPHome connection
// (callFavorite). Home Assistant takes that call only from a device whose ESPHome entry has "Allow the
// device to perform Home Assistant actions" on, and new entries have it off. When it is off, Home
// Assistant logs that it rejected the call and tells the device nothing: the call has no answer to wait
// for. So the screen went on reading "Starting…", or naming the station as playing, with no sound and
// nothing on the device to say why; the reason was a line in Home Assistant's own log.
//
// What the device can see is whether anything came of the call. A stream reaching its player, the
// station script turning on, the house's "last station" text naming a station, or Music Assistant
// playing on the device (through Sendspin, a remote holding the speaker) are all Home Assistant at work
// on it. With none of them within startWait, the station is no longer shown as starting, a note says
// what to check, and the log says it too (a Dot has no screen, so the log is all it has). None of that
// stops the stream: one that arrives after the note plays as any other does, and takes the note down.
//
// Only a station somebody asked for is watched: one the device puts back on by itself after its stream
// dropped (putBack) is not, since a note would be about nothing anybody did. And a call that went to no
// Home Assistant at all, none being connected, is only logged (callFavorite): the note's question
// about its settings would be the wrong one.

// Hint is a short note for the screen about something this device asked for that went nowhere. The
// screen puts it up as a card, the way it shows a notification (feature/notify), but quietly and
// without telling Home Assistant, which did not send it. An empty Message takes down the note this
// device put up, when what it was about came right after all.
type Hint struct {
	Title, Message string
}

// The notes, as the screen shows them: kept short, since a card holds a heading and three lines.
const (
	// NoStartHint is a station Home Assistant did not start.
	NoStartHint = "Home Assistant didn't start the stream. Is \"Allow the device to perform Home Assistant " +
		"actions\" on for this device?"
	// FavoriteHint is the star's script call refused.
	FavoriteHint = "Couldn't save the station. Home Assistant needs a script named " +
		"script.radio_favorite_add_station."
)

// startWait is how long a station started through the radio script has to show a sign that Home
// Assistant took the call. The script turning on comes within a second of the call when it is taken,
// so this is long for a working setup and short for somebody standing at the screen. A variable for
// the tests.
var startWait = 8 * time.Second

// startWaitFor is startWait for the service that plays a station. One that is not a script turns nothing
// on that Home Assistant follows, so only the stream itself says it was taken, and finding a station can
// take a while: it gets as long as the stream is claimed for (liveClaim).
func startWaitFor(service string) time.Duration {
	if strings.HasPrefix(service, "script.") {
		return startWait
	}
	return liveClaim
}

// watchStart is a station just asked of Home Assistant through service: if nothing has come of it by the
// time it has had (wait, startWaitFor), it is said so (noStart).
func (f *Feature) watchStart(service, station string, wait time.Duration, at time.Time) {
	f.mu.Lock()
	f.startSeq++
	seq := f.startSeq
	f.mu.Unlock()
	time.AfterFunc(wait, func() { f.noStart(service, station, seq, at, wait) })
}

// externalPlaying is whether something this device did not start is playing on it: Music Assistant's
// Sendspin stream holds the speaker as a remote (media.Player.External). A test's Feature has its own
// (f.external), since a timer of an earlier test may be reading it.
func (f *Feature) externalPlaying() bool {
	if f.external != nil {
		return f.external()
	}
	return media.Get().ExternalPlaying()
}

// noStart is the wait for station over. Only while it is still the station being started (the one
// chosen, with no station asked for since), and only when Home Assistant gave no sign at all since it was
// asked at: its stream reaching the player clears the chosen station (played), the script or the
// "last station" text moving is heardAt (stationStarted), and Music Assistant playing on the device is
// its stream arriving another way, which a working Music Assistant setup always shows. A Stop clears the
// chosen station too.
func (f *Feature) noStart(service, station string, seq uint64, at time.Time, wait time.Duration) {
	external := f.externalPlaying()
	f.mu.Lock()
	missed := f.startSeq == seq && f.chosen == station && f.heardAt.Before(at) && !external
	if missed {
		// The page stops saying "Starting…" for it. The claim on the next stream stays as it is, so a
		// stream that is only late is still the station's.
		f.chosen, f.hinted = "", true
	}
	f.mu.Unlock()
	if !missed {
		return
	}
	slog.Warn("radio: Home Assistant did not start the station; is \"Allow the device to perform Home Assistant actions\" on in this device's ESPHome entry?",
		"station", station, "service", service, "waited", wait)
	f.Changed.Emit(struct{}{})
	f.pokeMeta()
	f.Hint.Emit(Hint{Title: "Radio", Message: NoStartHint})
}

// startHeard is Home Assistant at work on a station: the station script turning on, or the house's text
// naming one. Under f.mu.
func (f *Feature) startHeard() { f.heardAt = time.Now() }

// streamArrived is a stream reaching the player: a note that said nothing came is taken down. Not under
// f.mu.
func (f *Feature) streamArrived() {
	f.mu.Lock()
	hinted := f.hinted
	f.hinted = false
	f.mu.Unlock()
	if hinted {
		slog.Info("radio: a stream arrived after all")
		f.Hint.Emit(Hint{})
	}
}

// favoriteFailed is the star's script call refused: the note names the script it needs, since a 400 is
// also what Home Assistant answers for a script that is not there. The screen that pressed the star logs
// the error itself.
func (f *Feature) favoriteFailed() {
	f.mu.Lock()
	f.hinted = false // a stream arriving is no answer to this one
	f.mu.Unlock()
	f.Hint.Emit(Hint{Title: "Favorites", Message: FavoriteHint})
}
