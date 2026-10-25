//go:build !dot

package display

import "time"

// pausedAway is how long the full now-playing page stays up once the music is paused or stopped. The
// track is still held after that (media's stoppedFor, half an hour), so play picks it up again and the
// page comes back with it; only the page goes, to the strip where the Now playing setting uses one and
// to the clock where it does not. Music Assistant pauses rather than stops, so without this a stopped
// song sat on the screen for the whole half hour (#150). It is the same half minute a tap on the strip
// brings the page back for (stripFull), and a page brought back by hand while paused gets it again.
const pausedAway = 30 * time.Second

// quietTimer is how long the music on the screen has been quiet: paused, stopped, or still on its way.
type quietTimer struct{ since time.Time }

// over reports whether music that wants the screen has been quiet for pausedAway. Playing, or nothing
// wanting the screen, starts it over, so a resume has the page for as long as the setting says.
func (q *quietTimer) over(now time.Time, wanted, playing bool) bool {
	if !wanted || playing {
		q.since = time.Time{}
		return false
	}
	if q.since.IsZero() {
		q.since = now
	}
	return now.Sub(q.since) >= pausedAway
}

// reopened is the page brought back by hand while the music is quiet: it gets pausedAway from now, so it
// is not taken away again under the finger that asked for it. Playing music has no timer to restart.
func (q *quietTimer) reopened(now time.Time) {
	if !q.since.IsZero() {
		q.since = now
	}
}
