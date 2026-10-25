package voice

import (
	"context"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/activity"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Calling a contact by voice: "<wake word>, call Mom" is answered on the device. The phone book is the
// device's own (phone.Contacts), and the call is the device's to place, so Home Assistant has nothing to
// add: its run is stopped the way a wake by mistake's is, its reply never plays, and the device says
// whom it is calling. The call then waits callCancel after the words before it is dialed, on the call's
// own page, so a wrong name can be called off with the stop word, the hang-up word, the button or Hang
// up before anyone's phone rings.

// callCancel is how long after "Calling Mom." the call waits before it is dialed. The waits here are
// variables so tests can shorten them.
var callCancel = 3 * time.Second

// wordsStart is how long the words have to start playing. Home Assistant makes them, and a call nobody
// heard named is not placed: with no words (no Home Assistant, or the device not allowed to perform
// its actions) the call is dropped rather than dialed after a silent pause.
var wordsStart = 6 * time.Second

// wordsLongest bounds how long the words may play.
var wordsLongest = 10 * time.Second

// announcing is whether the words are playing, and hush stops them. Here so tests can stand in for the
// speaker.
var announcing = func() bool { return speaker.Sound().Holder() == "announce" }

var hush = func() { speaker.Sound().Silence() }

// notSignedIn is what is said for a call asked for while the phone cannot place one.
const notSignedIn = "The phone isn't signed in."

// phoneNow is what calling by voice needs to know of the phone: whether it has an account, whether it
// is signed in and free, and the phone book. Here so tests can stand in for it.
var phoneNow = func() (configured, ready bool, book []phone.Contact) {
	if !phone.CallByVoice() {
		return false, false, nil
	}
	p := phone.Get()
	st := p.State()
	if !st.Configured || st.Phase != phone.Idle {
		return false, false, nil
	}
	return true, st.Registered, p.Contacts()
}

// placeCall says words and, for a phone that is ready, calls who once they have been heard. Here so
// tests can stand in for it.
var placeCall = func(who phone.Contact, words string, ready bool) {
	remind.Say(words)
	if !ready {
		return
	}
	if err := phone.Get().CallAfter(who.Number, who.Name, afterWords); err != nil {
		slog.Warn("call by voice not placed", "err", err)
	}
}

// heardCall is the contact a turn's transcript asks to call, the words to answer with, and whether the
// phone can place the call. Only in a turn somebody opened to ask something: not a follow-up or a turn
// Home Assistant opened, where what is said answers a question; not realtime voice, which answers from
// the audio whatever the transcript says; and not while an alarm or timer rings, whose turn is about
// the ring. A phone with no account leaves the sentence to Home Assistant; one that has an account and
// is not signed in says so, since Home Assistant could not place the call either.
func (c *conversation) heardCall(text string, rt bool) (who phone.Contact, words string, ready, ok bool) {
	if text == "" || rt || c.phase == phaseIdle || c.followUp || c.asked || ring.IsSounding() {
		return phone.Contact{}, "", false, false
	}
	configured, ready, book := phoneNow()
	if !configured {
		return phone.Contact{}, "", false, false
	}
	who, ok = callAsked(text, book)
	if !ok {
		return phone.Contact{}, "", false, false
	}
	if !ready {
		return who, notSignedIn, false, true
	}
	return who, "Calling " + who.Name + ".", true, true
}

// callFromTurn ends the turn that asked for a call, the way a wake by mistake ends, and hands the call
// on. Home Assistant goes on with the run it is told to stop, and its last events, its reply among
// them, are still to come: a wake in the meantime waits for them (draining).
func (c *conversation) callFromTurn(who phone.Contact, words string, ready bool) {
	slog.Info("heard a call request", "slot", c.slot+1, "ready", ready)
	c.log.Replied(words)
	c.turn.Replying(words)
	c.clearPending()
	c.idle("calling by voice", activity.Answered)
	if !c.stopsAtOnce() {
		c.draining = true
		c.grace = time.NewTimer(drainFor)
	}
	safe.Go("call by voice", func() { placeCall(who, words, ready) })
}

// afterWords waits for "Calling Mom." to be said, then callCancel more, and reports whether the call is
// still wanted. The whole of callCancel comes after the words have ended, however late they start, so
// there is always that long to call it off once the name has been heard; canceling (ctx) stops the
// words too. Words that never start drop the call.
func afterWords(ctx context.Context) bool {
	saying := announcing
	off := func() bool {
		if saying() {
			hush()
		}
		return false
	}
	if !waitFor(ctx, wordsStart, saying) {
		if ctx.Err() == nil {
			slog.Warn("call by voice dropped: the words naming who it calls were never said")
		}
		return off()
	}
	// Twice at most: words that start again after the wait get it once more, and words that never end
	// drop the call.
	for range 2 {
		waitFor(ctx, wordsLongest, func() bool { return !saying() })
		if ctx.Err() != nil {
			return off()
		}
		select {
		case <-ctx.Done():
			return off()
		case <-time.After(callCancel):
		}
		// Words still playing (they started again, or ran past wordsLongest) get the wait after them.
		if !saying() {
			return true
		}
	}
	return off()
}

// waitFor waits up to d for cond, and reports whether it came true.
func waitFor(ctx context.Context, d time.Duration, cond func() bool) bool {
	end := time.Now().Add(d)
	for !cond() {
		if ctx.Err() != nil || time.Now().After(end) {
			return false
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	return true
}
