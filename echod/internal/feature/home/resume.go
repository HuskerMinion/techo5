package home

import (
	"log/slog"
	"time"
)

// Radio that puts itself back on when the stream drops.
//
// A station does not end. What ends is the thing carrying it: Home Assistant hands the device a URL
// from its own proxy, and when that proxy stops — five minutes in, or thirteen, with no pattern to
// it — the device plays the stream out and reports it finished, correctly and uselessly. What the
// room hears is the music stopping for no reason, and somebody has to go and start it again.
//
// So a stream that ends by itself while a station is playing is put back on. A stream that was
// stopped, paused or replaced is not: those are somebody's doing, and undoing them would be a device
// arguing with the person in front of it.

const (
	// resumeAfter is the wait before putting it back: long enough that a station which is genuinely
	// gone does not hammer Home Assistant, short enough to sound like a hiccup rather than a silence.
	resumeAfter = 3 * time.Second

	// askedFor is how long after asking for a station a dropped stream is still taken to be that
	// station's. Beyond it the device has been playing something else for hours and what dropped is
	// nobody's business of ours.
	askedFor = 12 * time.Hour

	// resumeTries is how many times in resumeWindow a station may be put back before this gives up
	// and leaves it off. A stream that drops five times in ten minutes is not going to keep playing,
	// and a device that keeps trying for ever is worse than one that stops.
	resumeTries  = 5
	resumeWindow = 10 * time.Minute
)

// ended is a stream that stopped of its own accord. Whether it is worth putting back is decided
// here: only the stream that is playing now, only while a station is meant to be on.
func (f *Feature) ended(url string) {
	f.mu.Lock()
	playing, station := f.url, f.asked
	if station != "" && time.Since(f.askedAt) > askedFor {
		station = ""
	}
	now := time.Now()
	if now.Sub(f.resumedAt) > resumeWindow {
		f.resumed = 0
	}
	tries := f.resumed
	f.mu.Unlock()

	if url != playing || station == "" {
		return
	}
	if tries >= resumeTries {
		slog.Warn("radio: the stream keeps dropping, leaving it off",
			"station", station, "tries", tries, "within", resumeWindow)
		return
	}

	f.mu.Lock()
	f.resumed, f.resumedAt = tries+1, now
	f.mu.Unlock()

	slog.Info("radio: the stream ended by itself, putting it back on",
		"station", station, "try", tries+1)
	time.AfterFunc(resumeAfter, func() {
		// Anything that started in the meantime is what somebody wanted instead.
		f.mu.Lock()
		still := f.url == playing
		f.mu.Unlock()
		if still {
			f.Play(station)
		}
	})
}
