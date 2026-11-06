package voice

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

// Call on wake word is a select for each wake word, on its assistant's own sub-device beside the
// slot's other settings: Off, or a contact in the phone book by its shown name. Choosing one is
// SetWakeCall, as the setup page's Call on a wake word is, so both refuse the same things and show
// the same choice. Only names are offered: a contact's number never reaches Home Assistant.
//
// A contact chosen and since taken out of the phone book stays offered while it is chosen, so the
// select still says what is set, as the setup page does; a wake then reports unknown_contact. A
// contact called Off is not offered, as it could not be told apart from Off.
//
// Home Assistant reads a select's options once per connection, so a phone book that offers different
// names asks for a new connection, as the other selects whose lists change do.

// wakeCallOff is the option for no call: the wake word starts the assistant.
const wakeCallOff = "Off"

// wakeCallChanged is a slot's choice saved (SetWakeCall), from the select or the setup page.
var wakeCallChanged hook.Hook[struct{}]

type wakeCallSelects struct {
	once sync.Once
	sels []*esphome.Select
	opts []*component.Options
}

// wakeCallOptions is what a slot's select offers: Off, the phone book's contacts, and the contact
// chosen if the book no longer has it.
func wakeCallOptions(book []phone.Contact, chosen string) []string {
	opts := []string{wakeCallOff}
	for _, c := range book {
		if !strings.EqualFold(strings.TrimSpace(c.Name), wakeCallOff) {
			opts = append(opts, c.Name)
		}
	}
	if chosen != "" && !slices.Contains(opts, chosen) {
		opts = append(opts, chosen)
	}
	return opts
}

// wakeCallOption is the option for what a slot has chosen.
func wakeCallOption(chosen string) string {
	if chosen == "" {
		return wakeCallOff
	}
	return chosen
}

// wakeCallSelects builds the selects once, with what is saved now.
func (v *Voice) wakeCallSelects() []*esphome.Select {
	w := &v.wakeCalls
	w.once.Do(func() {
		_, book := wakeCallPhone()
		for n := range wakeword.Slots {
			chosen := config.Get().Wake.Slot(n).CallContact
			sel := &esphome.Select{
				Base: esphome.Base{
					ObjectID: fmt.Sprintf("wake_call_%d", n+1),
					DeviceID: component.AssistantDevice(n),
					Name:     "Call on wake word",
					Icon:     "mdi:phone-outgoing",
					Category: esphome.CategoryConfig,
				},
				Options: wakeCallOptions(book, chosen),
			}
			sel.Set(wakeCallOption(chosen))
			sel.OnCommand = func(o string) { v.chooseWakeCall(n, o) }
			w.sels = append(w.sels, sel)
			w.opts = append(w.opts, component.NewOptions(sel))
		}
		wakeCallChanged.Listen(func(struct{}) { v.showWakeCalls() })
		// A new phone book offers other names. Changed's listeners must not block, and this reads the
		// book off disk.
		wakeCallBookChanged(func() { go v.showWakeCalls() })
	})
	return w.sels
}

// wakeCallBookChanged calls fn when the phone may have a new book, here so tests can do without the
// phone.
var wakeCallBookChanged = func(fn func()) { phone.Get().Changed.Listen(func(phone.State) { fn() }) }

// chooseWakeCall is an option chosen in a slot's select. A refused choice is logged and the select
// goes back to what is saved.
func (v *Voice) chooseWakeCall(n int, o string) {
	contact := o
	if o == wakeCallOff {
		contact = ""
	}
	if err := SetWakeCall(n+1, contact); err != nil {
		slog.Warn("wake call not chosen", "slot", n+1, "err", err)
		v.showWakeCalls()
		return
	}
	slog.Info("setting changed", "setting", fmt.Sprintf("wake_call_%d", n+1))
}

// showWakeCalls offers each select the phone book as it is now and shows what is saved, and asks for
// a new connection when the names offered change.
func (v *Voice) showWakeCalls() {
	w := &v.wakeCalls
	_, book := wakeCallPhone()
	changed := false
	for n, sel := range w.sels {
		chosen := config.Get().Wake.Slot(n).CallContact
		if w.opts[n].Set(wakeCallOptions(book, chosen)) {
			changed = true
		}
		sel.Set(wakeCallOption(chosen))
	}
	if changed {
		component.Reconnect.Emit(struct{}{})
	}
}

// Entities is Call on wake word for each wake word.
func (v *Voice) Entities() []esphome.Entity {
	var out []esphome.Entity
	for _, sel := range v.wakeCallSelects() {
		out = append(out, sel)
	}
	return out
}

// Restore shows each slot's saved choice, before Home Assistant connects.
func (v *Voice) Restore(config.Config) {
	v.wakeCallSelects()
	v.showWakeCalls()
}
