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
				// Nothing is ringing, so a hush left over from the last one must not apply to the
				// next.
				state.until = time.Time{}
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
