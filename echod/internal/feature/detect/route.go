package detect

import "github.com/HuskerMinion/techo5/echod/internal/feature/phone"

// route is what a word the engine heard does.
type route int

const (
	routeIgnore          route = iota
	routeTurn                  // a wake word opens a turn (through feature/nearest)
	routeInterrupt             // the stop word with no call: whatever is sounding stops
	routeEndRing               // the stop word over an alarm or timer during a call: the ring ends, the call stays
	routeHangUp                // the hang-up word, or the stop word chosen as it: hangUp's own checks follow
	routeFarEnd                // a wake word during a call, ignored because the far end was talking
	routeCancelTurn            // the stop word in a turn during a call: the turn ends, the call stays
	routeCancelAndHangUp       // the hang-up word in a turn: the turn and the call both end, if hangUp agrees
	routeCommand               // a command word, with no call and no talk through a camera: command.heard
	routeCancelCall            // the stop or hang-up word before a call asked for by voice is dialed: it is not placed
)

// heard is everything route decides on, read once where the word is heard.
type heard struct {
	phase    phone.Phase
	talkBack bool // the microphones are talking through a camera
	ringing  bool // an alarm or timer is sounding
	stopHang bool // the stop word is the hang-up word
	callWake bool // Wake word during calls
	farEnd   bool // the far end was talking just now
	turnOpen bool // a turn is running: listening, thinking or replying
	waiting  bool // a call is being placed and has not been dialed yet (phone.State.Waiting)
}

// routeFor decides what a word heard in slot does.
//
// A call has the microphones and the speaker. The far end talking through the speaker is not somebody
// in the room, and a turn would take the call's audio away mid-sentence; a talk through a camera is the
// same, whoever is at the door being spoken to. So during either, a wake word does nothing unless Wake
// word during calls lets it through (phone.WakeAllowedIn), and the stop word never opens or interrupts a
// turn, since stopping the sound would be stopping the call.
//
// The stop word still has to end a ring during a call: the call page takes every tap and Home Assistant
// cannot stop a timer, so without it a timer that finishes mid-call sounds for its full fifteen minutes.
// That comes first. In a turn opened during a call, "stop" means the turn, as it does in any other: it
// cancels the turn and leaves the call. With nothing ringing and no turn, the stop word hangs up only if
// it is the hang-up word, and only a call that can be ended by voice (phone.Answerable): not one being
// placed. The hang-up word in a turn ends both, so "Okay Nabu, hang up" hangs up whether the word or Home
// Assistant hears it first.
//
// A call asked for by voice waits a moment before it is dialed, so that it can be called off: the stop
// word or the hang-up word then cancels it, as Hang up does. Nothing has been dialed, so nothing the
// far end says can be in the speaker yet, and the only sound is the device saying whom it is calling.
// An alarm or timer ringing then is still what the stop word ends first.
//
// The command words do nothing in any phase of a call or during a talk through a camera, whatever Wake
// word during calls says: the music they act on is not what is playing then. Otherwise they go to
// command.heard, which has its own checks (a turn, a ring, no music), and never open a turn.
func routeFor(slot int, h heard) route {
	inCall := h.phase != phone.Idle
	switch {
	case isCommandSlot(slot) && (inCall || h.talkBack):
		return routeIgnore
	case isCommandSlot(slot):
		return routeCommand
	case h.waiting && !h.talkBack && (slot == HangupSlot || slot == StopSlot && !h.ringing):
		return routeCancelCall
	case slot == HangupSlot && h.turnOpen:
		return routeCancelAndHangUp
	case slot == HangupSlot:
		return routeHangUp
	case slot == StopSlot && !inCall && !h.talkBack:
		return routeInterrupt
	case slot == StopSlot && h.ringing:
		return routeEndRing
	case slot == StopSlot && inCall && h.turnOpen:
		return routeCancelTurn
	case slot == StopSlot:
		if h.stopHang && phone.Answerable(h.phase) && !h.talkBack {
			return routeHangUp
		}
		return routeIgnore
	case h.talkBack:
		return routeIgnore
	case phone.WakeAllowedIn(h.phase, h.callWake, h.farEnd):
		return routeTurn
	case h.phase == phone.Talking && h.callWake && h.farEnd:
		return routeFarEnd
	}
	return routeIgnore
}
