package speaker

import (
	"fmt"
	"log/slog"
	"slices"
	"sync"
)

// Producer is a background sound: a track, or a room playing along with the house.
type Producer interface {
	Background

	// Duck sets the level for what is written next; what is already queued is Requeue's job.
	Duck(on bool)

	// Requeue re-scales what is already queued, which can be seconds of audio.
	Requeue()
}

// Arbiter is the one Background the driver holds, standing in for however many there are. The newest
// plays and the rest wait in order behind it, so when a track ends the stream it interrupted carries on.
type Arbiter struct {
	mu    sync.Mutex
	stack []Producer // the last is the one being heard
	held  bool       // the driver has stood the background down
	duck  bool
	// ducks is who wants the background quiet: a turn, a ring, an announcement. It stays down until
	// the last of them lets go, so one ending does not bring the music back up under another.
	ducks map[string]bool
	// hold is the producer the hold stood down, so a retake by it during the same hold is not
	// suspended a second time.
	hold Producer
}

// Backgrounds is this driver's arbiter, made on first use. Per driver, not per package: echoctl
// builds its own.
func (d *Driver) Backgrounds() *Arbiter {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.arb == nil {
		d.arb = &Arbiter{}
		d.bg = d.arb // set here rather than through Yields, which wants the same lock
	}
	return d.arb
}

// Took is a producer starting. Whatever was playing stands down but keeps its place.
func (a *Arbiter) Took(p Producer) {
	a.mu.Lock()
	// A producer under the one being heard was stood down once, by whatever took the speaker over it
	// or by the hold. Taking the speaker back is where that is undone: nothing else would undo it, and
	// the station a song had stood down played nothing, however many times it was started again.
	below := a.top() != p && slices.Contains(a.stack, p)
	a.drop(p)
	stood := a.top()
	a.stack = append(a.stack, p)
	held, duck := a.held, a.duck
	hold := a.hold
	if below && held {
		a.hold = p // its one suspension now stands for the hold, which ends by resuming the top
	}
	a.mu.Unlock()

	// Already down if the driver holds the lot, and suspending twice would need undoing twice.
	if stood != nil && !held {
		slog.Debug("background handover", "from", kind(stood), "to", kind(p))
		stood.Suspend()
	}
	p.Duck(duck)
	switch {
	case below && !held:
		p.Resume()
	case below:
		// Held: its suspension carries on as the hold's, and the hold's end lets it play.
	case held && p != hold:
		// The hold stood at most one producer down — the one being heard when it began, or none if
		// the speaker was silent. A retake by that same producer is already held, so suspending it
		// again would leave a resume outstanding; anyone else joining mid-hold is stood down here so
		// it cannot play over the claim.
		p.Suspend()
	}
}

// Gave is a producer finishing. Whatever it interrupted picks up again.
func (a *Arbiter) Gave(p Producer) {
	a.mu.Lock()
	was := a.top()
	a.drop(p)
	now := a.top()
	held := a.held
	// A producer that leaves takes the hold on it with it: it forgets being stood down, so if it comes
	// back while the hold lasts it is a newcomer, and has to be stood down like one.
	if a.hold == p {
		a.hold = nil
	}
	a.mu.Unlock()

	if now == nil || now == was || held {
		return
	}
	slog.Debug("background resumed", "who", kind(now), "after", kind(p))
	now.Resume()
}

// Suspend and Resume are the Background the driver holds: a claim wants the speaker, so whichever
// producer is being heard stands down for it.
func (a *Arbiter) Suspend() {
	a.mu.Lock()
	if a.held {
		a.mu.Unlock()
		return
	}
	a.held = true
	p := a.top()
	if p != nil {
		a.hold = p
	}
	a.mu.Unlock()

	if p != nil {
		p.Suspend()
	}
}

func (a *Arbiter) Resume() {
	a.mu.Lock()
	if !a.held {
		a.mu.Unlock()
		return
	}
	a.held = false
	a.hold = nil
	p := a.top()
	a.mu.Unlock()

	if p != nil {
		p.Resume()
	}
}

// Duck asks for the background to be quiet, for why, or lets go of that ask. It is quiet while anyone
// asks.
func (a *Arbiter) Duck(why string, on bool) {
	a.mu.Lock()
	if a.ducks == nil {
		a.ducks = map[string]bool{}
	}
	if on {
		a.ducks[why] = true
	} else {
		delete(a.ducks, why)
	}
	want := len(a.ducks) > 0
	a.mu.Unlock()
	a.setDuck(want)
}

// setDuck quietens everything, waiting producers included, so one resuming mid-turn comes back quiet.
func (a *Arbiter) setDuck(on bool) {
	a.mu.Lock()
	if a.duck == on {
		a.mu.Unlock()
		return
	}
	a.duck = on
	all := append([]Producer(nil), a.stack...)
	heard := a.top()
	a.mu.Unlock()

	for _, p := range all {
		p.Duck(on)
	}

	// Only the audible one, or the same samples get attenuated once per producer. Unducking is left
	// to drain.
	if on && heard != nil {
		heard.Requeue()
	}
}

// Playing is the producer being heard, or nil. For diagnostics.
func (a *Arbiter) Playing() Producer {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.top()
}

// top and drop are called with the lock held.
func (a *Arbiter) top() Producer {
	if len(a.stack) == 0 {
		return nil
	}
	return a.stack[len(a.stack)-1]
}

func (a *Arbiter) drop(p Producer) {
	for i, have := range a.stack {
		if have == p {
			a.stack = append(a.stack[:i], a.stack[i+1:]...)
			return
		}
	}
}

func kind(p Producer) string {
	if p == nil {
		return "nothing"
	}
	return fmt.Sprintf("%T", p)
}
