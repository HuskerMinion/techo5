package voice

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/activity"
)

// A wake word's peak is known only once the detection's hold ends, a few hundred milliseconds after
// the word crossed its threshold. By then the turn it opened may be open, may be over (another device
// answered), or may not have opened yet (it waited for a command word, or for the devices to agree).
// The loop keeps whichever of the two came first and pairs it with the other.

// scoreBefore and scoreAfter are how far apart a peak and a turn's wake may be and still be the same
// word: a peak up to half a second before the turn's wake (a turn opened once a command word's window
// had passed, its wake the time it opened), to one a second after. A detection's hold and the quiet after it put the next wake at least 1.1 seconds on.
const (
	scoreBefore = 500 * time.Millisecond
	scoreAfter  = time.Second
)

// wakeScores is the half of a pair still waiting: a turn opened by the device's own wake word, or a
// peak that came before its turn.
type wakeScores struct {
	turn     *activity.Turn
	turnSlot int
	turnAt   time.Time

	peak     float64
	peakSlot int
	peakAt   time.Time
}

// WakeScored gives the turn a wake word opened in slot the peak the word reached (detect.Engine's
// OnScored).
func (v *Voice) WakeScored(slot int, peak float64) {
	v.turn.post(event{kind: evScored, slot: slot, peak: peak, at: time.Now()})
}

// wakeOpened is a turn just opened by the device's own wake word in slot, heard at at.
func (c *conversation) wakeOpened(slot int, at time.Time) {
	s := &c.scores
	if s.peak > 0 && s.peakSlot == slot && near(s.peakAt, at) {
		c.turn.Scored(s.peak)
		s.peak = 0
		return
	}
	s.peak = 0
	c.turn.AwaitsScore()
	s.turn, s.turnSlot, s.turnAt = c.turn, slot, at
}

// scored is a peak a wake word in slot reached, at now. One no turn takes is kept for a turn about to
// open.
func (c *conversation) scored(slot int, peak float64, now time.Time) {
	s := &c.scores
	if s.turn != nil && s.turnSlot == slot && near(now, s.turnAt) {
		s.turn.Scored(peak)
		s.turn = nil
		return
	}
	s.turn = nil
	s.peak, s.peakSlot, s.peakAt = peak, slot, now
}

// near is whether a peak that came at peakAt belongs to the wake heard at wakeAt.
func near(peakAt, wakeAt time.Time) bool {
	d := peakAt.Sub(wakeAt)
	return d >= -scoreBefore && d <= scoreAfter
}
