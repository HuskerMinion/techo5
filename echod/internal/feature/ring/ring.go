// Package ring holds the state a sounding ring shares with the rest of the device: that one is
// sounding at all, and that its chime should be held back for a moment so a word can be heard.
//
// It is a package of its own because there are two ring engines, in alarm and in timer, and anything
// that has to be true of both has to live somewhere neither owns. Nothing here imports another
// feature, so anything may import it.
package ring

import (
	"sync"
	"time"
)

// HushFor is how long a chime is held back. Long enough to say a word into the gap and be heard,
// short enough that an alarm nobody stops is audibly still an alarm. Exported because whatever
// decides to hush has to rate-limit itself against it.
const HushFor = 3 * time.Second

// OfferFor is how long a silenced ring waits to be told what to do with itself.
//
// Silencing and deciding are two acts, and only the first is urgent: what somebody wants at 6am is
// quiet, not a choice between Stop and Snooze. So the first press quiets the ring and offers the
// snooze, and this is how long the offer stands before the ring takes the answer it did not get and
// stops. Long enough to read it and reach out again, short enough that a device nobody answers is
// not still waiting on somebody a minute later.
const OfferFor = 8 * time.Second

// state is package level because the thing being described is: the device is making a noise. There
// is one device.
var state struct {
	mu sync.Mutex

	// sounding counts the rings in progress rather than flagging one, so an alarm and a timer going
	// off together do not have the first to finish report silence.
	sounding int

	// until is when the current hush ends. A deadline rather than a flag: a hush that is never
	// lifted expires by itself, and an alarm that cannot be silenced by a bug is worth more than
	// one that can.
	until time.Time

	// offer is when a silenced ring stops waiting for an answer. Zero means nothing was silenced.
	//
	// It runs the other way round from until, and deliberately: a hush that gets stuck must expire
	// so the alarm comes back, where an offer that gets stuck must expire so the alarm goes away.
	// Both are the safe direction for what they are.
	offer time.Time

	// silences and snoozes are what can actually end a ring. They live here as functions because
	// this package must not import the ring engines - alarm and timer both import this one - and
	// because there are two of them: a caller should not have to know that, which is how Home
	// Assistant ended up able to stop an alarm but not a timer.
	silences []func() bool
	snoozes  []func() bool

	now func() time.Time
}

func init() { state.now = time.Now }

// Sounding marks a ring as sounding until the returned function is called. Call it from the ring
// loop with defer.
func Sounding() (done func()) {
	state.mu.Lock()
	state.sounding++
	state.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			state.mu.Lock()
			state.sounding--
			if state.sounding == 0 {
				// Nothing is ringing, so a hush or an offer left over from the last one must not
				// apply to the next.
				state.until, state.offer = time.Time{}, time.Time{}
			}
			state.mu.Unlock()
		})
	}
}

// IsSounding reports whether any ring is sounding.
func IsSounding() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.sounding > 0
}

// Hush holds the chime back for a moment, so a word said into the gap reaches the microphones
// without the ring on top of it. The screen and the LED are untouched: the alarm stays obviously
// alive while it is quiet.
//
// It does nothing when nothing is ringing, so a near miss in a quiet room costs nothing.
func Hush() {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.sounding == 0 {
		return
	}
	if end := state.now().Add(HushFor); end.After(state.until) {
		state.until = end
	}
}

// Hushed reports whether the chime should be skipped this time round the ring loop.
func Hushed() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.now().Before(state.until)
}

// Silences registers a way to end a ring, and Snoozes a way to put one off. Each ring engine
// registers its own from init, so by the time anything can press a button they are all here.
//
// A timer registers only a silence: a timer cannot be put off, so snoozing one stops it.
func Silences(f func() bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.silences = append(state.silences, f)
}

// Snoozes registers a way to put a ring off for a while. See Silences.
func Snoozes(f func() bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.snoozes = append(state.snoozes, f)
}

// Silence quiets a sounding ring and offers to snooze it, reporting whether there was one to quiet.
//
// It does not end the ring. The noise stops at once, which is the urgent part, and the ring stays
// alive and unheard for OfferFor so that a second press can still snooze it — a ring that had
// already ended would have nothing left to put off. If no answer comes, Lapsed goes true and the
// ring loop ends itself.
func Silence() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.sounding == 0 {
		return false
	}
	state.offer = state.now().Add(OfferFor)
	return true
}

// Offered reports whether a ring is silenced and waiting to be told what to do — what the screen
// draws the snooze offer on, and what makes a second press mean "yes" rather than "quiet".
func Offered() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.now().Before(state.offer)
}

// Quiet reports whether the chime should be skipped this time round the ring loop, for any reason:
// a hush for a word, or a silence waiting on an answer.
// A silenced ring is quiet from then on, expired offer or not. There are only two ways out of being
// silenced — snoozed, or stopped — and a chime between the offer running out and the loop noticing
// would be a ring that somebody had already quieted speaking up again.
func Quiet() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.now().Before(state.until) || !state.offer.IsZero()
}

// Lapsed reports that a ring was silenced and the offer ran out, so the ring should end. The loops
// ask each time round rather than a timer firing, which keeps this package free of goroutines: the
// cost is that a ring ends within one turn of the loop of the offer expiring, and nobody can hear
// the difference because it has been silent the whole time.
func Lapsed() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return !state.offer.IsZero() && !state.now().Before(state.offer)
}

// Accept takes the offer up: it puts off every ring that can be put off, and stops the rest.
// Reports whether anything was ringing to accept for.
//
// Snoozes run before silences so that an alarm is put off rather than stopped, and the silences that
// follow catch whatever had no snooze of its own — a ringing timer beside it, say.
func Accept() bool {
	state.mu.Lock()
	snoozes, silences := append([]func() bool(nil), state.snoozes...), append([]func() bool(nil), state.silences...)
	state.offer = time.Time{}
	state.mu.Unlock()

	did := false
	for _, f := range snoozes {
		did = f() || did
	}
	for _, f := range silences {
		did = f() || did
	}
	return did
}

// End stops every ring, whether or not one was silenced first. It is what the offer running out
// comes to, and what anything that means "stop" should call.
func End() bool {
	state.mu.Lock()
	silences := append([]func() bool(nil), state.silences...)
	state.offer = time.Time{}
	state.mu.Unlock()

	did := false
	for _, f := range silences {
		did = f() || did
	}
	return did
}
