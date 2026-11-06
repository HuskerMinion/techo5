package voice

import (
	"errors"
	"log/slog"
	"strings"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
)

// A wake word slot can call a contact in place of opening a turn (config.WakeWord.CallContact): the
// word heard, the call is placed, and Home Assistant's pipeline is not asked. The phone owns the rest:
// the line taken in one step, the call's page, its sound and its end.
//
// It applies only while the phone is idle. During a call, ringing, being placed or up, the slot is an
// ordinary one again, so Wake word during calls still lets it hang up by voice; and since a wake is
// never let through while a call is being placed, a second call cannot start that way.
//
// A call that cannot be placed is not silent: the log says why, and Home Assistant hears not_placed
// with the reason. It never falls back to a turn: somebody who chose a call should not be answered by
// somebody else without noticing.

// Reasons a wake call was not placed, in the not_placed event.
const (
	notPlacedNoPhone     = "no_phone"
	notPlacedNotSignedIn = "not_signed_in"
	notPlacedLineBusy    = "line_busy"
	notPlacedBusy        = "busy"
	notPlacedMuted       = "muted"
	notPlacedNoContact   = "unknown_contact"
	notPlacedDialFailed  = "dial_failed"
)

// The phone, the mute and the event, here so tests can stand in for them without microphones or SIP.
var (
	wakeCallPhone = func() (phone.State, []phone.Contact) {
		p := phone.Get()
		return p.State(), p.Contacts()
	}
	wakeCallDial      = func(c phone.Contact) error { return phone.Get().CallNamed(c.Number, c.Name) }
	wakeCallMuted     = func() bool { on, err := mute.Get().Muted(); return on || err != nil }
	wakeCallNotPlaced = func(name, reason string) { phone.NotPlaced(name, reason) }
)

// wakeCallApplies is whether a wake in slot is for a call rather than a turn: the slot has a word, is
// set to call a contact and not to realtime voice, and the phone has no call ringing, being placed or
// up.
func wakeCallApplies(slot int) bool {
	if slot < 0 || slot >= wakeword.Slots {
		return false
	}
	w := config.Get().Wake.Slot(slot)
	if w.ID == "" || w.CallContact == "" || w.Realtime {
		return false
	}
	state, _ := wakeCallPhone()
	return state.Phase == phone.Idle
}

// findContact is the contact one of whose names is name, ignoring case and spaces around it.
func findContact(book []phone.Contact, name string) (phone.Contact, bool) {
	name = strings.TrimSpace(name)
	for _, c := range book {
		for _, n := range c.Names() {
			if strings.EqualFold(strings.TrimSpace(n), name) {
				return c, true
			}
		}
	}
	return phone.Contact{}, false
}

// wakeCall is the contact a wake in slot would call now, or why it cannot.
func (v *Voice) wakeCall(slot int) (phone.Contact, string) {
	state, book := wakeCallPhone()
	switch {
	case !state.Configured:
		return phone.Contact{}, notPlacedNoPhone
	case !state.Registered:
		return phone.Contact{}, notPlacedNotSignedIn
	case state.Phase != phone.Idle:
		return phone.Contact{}, notPlacedLineBusy
	case v.turn == nil || v.turn.Busy():
		return phone.Contact{}, notPlacedBusy
	case wakeCallMuted():
		return phone.Contact{}, notPlacedMuted
	}
	c, ok := findContact(book, config.Get().Wake.Slot(slot).CallContact)
	if !ok {
		return phone.Contact{}, notPlacedNoContact
	}
	return c, ""
}

// wakeCallReady is whether a wake in slot could place its call now, for feature/nearest.
func (v *Voice) wakeCallReady(slot int) bool {
	_, why := v.wakeCall(slot)
	return why == ""
}

// callOnWake places slot's call, or says why it did not.
func (v *Voice) callOnWake(slot int) {
	contact, why := v.wakeCall(slot)
	if why == "" {
		if err := wakeCallDial(contact); err != nil {
			why = notPlacedDialFailed
		}
	}
	if why != "" {
		slog.Warn("wake call not placed", "slot", slot+1, "reason", why)
		wakeCallNotPlaced(config.Get().Wake.Slot(slot).CallContact, why)
		return
	}
	slog.Info("wake call placed", "slot", slot+1)
}

// MaxCallContactLen bounds a contact's name as a wake call's choice.
const MaxCallContactLen = 128

// SetWakeCall chooses the contact a wake word slot (1 or 2, as Home Assistant counts them) calls, by
// one of its names in the phone book, or none with an empty name. The contact's first name is what is
// kept. A slot with no word, or one set for realtime voice, cannot be chosen. The slot's select shows
// the choice saved, wherever it was made.
func SetWakeCall(slot int, contact string) error {
	if slot < 1 || slot > wakeword.Slots {
		return errors.New("wake call: the slot is 1 or 2")
	}
	contact = strings.TrimSpace(contact)
	if len(contact) > MaxCallContactLen || strings.ContainsFunc(contact, unicode.IsControl) {
		return errors.New("wake call: that is not a contact's name")
	}
	if contact != "" {
		w := config.Get().Wake.Slot(slot - 1)
		if w.ID == "" || w.Realtime {
			return errors.New("wake call: choose a wake word without realtime voice first")
		}
		_, book := wakeCallPhone()
		c, ok := findContact(book, contact)
		if !ok {
			return errors.New("wake call: choose a contact in the phone book")
		}
		contact = c.Name
	}
	if err := config.Set().Wake(slot - 1).CallContact(contact); err != nil {
		return err
	}
	wakeCallChanged.Emit(struct{}{})
	return nil
}
