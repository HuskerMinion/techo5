// Package buttons is the four buttons, and a power key where there is one, as Home Assistant sees them.
//
// The device acts on a press itself; these are so an automation can act on one as well, including
// the buttons the device does nothing with.
package buttons

import (
	"log/slog"
	"slices"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

func init() {
	component.Register(component.Device, Get())
}

// Event types, as Home Assistant sees them. Repeats are not reported: a volume ramp would fill the
// logbook with dozens of entries for one press.
//
// DoublePress and TriplePress are the action button's only (#627). Press still fires for every tap the
// moment it is let go, so nothing that acts on Press gets slower: a double press reaches Home Assistant
// as press, press, double_press, and a triple as those and then press, triple_press.
const (
	Press       = "press"
	Hold        = "hold"
	LongHold    = "long_hold"
	DoublePress = "double_press"
	TriplePress = "triple_press"
)

// order is the buttons as Home Assistant lists them, since a map has none. A button is listed only
// where the board has it, as an entity for one it lacks would sit at unknown for good: the Shows and
// the Spot have no action button, no Echo has a power key of its own, and the Dots' codes list volume
// keys the Echo Input is known to lack (buttons.Missing).
var order = func() []buttons.Name {
	var o []buttons.Name
	for _, n := range []buttons.Name{buttons.Action, buttons.Mute, buttons.VolumeDown, buttons.VolumeUp, buttons.Power} {
		if buttons.Has(n) && !buttons.Missing(n) {
			o = append(o, n)
		}
	}
	return o
}()

type Events struct {
	events map[buttons.Name]*esphome.Event
}

var (
	once   sync.Once
	shared *Events
)

// Get is the button entities, listening to the hardware from the moment it is built.
func Get() *Events {
	once.Do(func() {
		shared = &Events{events: map[buttons.Name]*esphome.Event{}}
		types := []string{Press, Hold}
		if buttons.LongHolds() {
			types = append(types, LongHold)
		}
		for _, name := range order {
			t := types
			if name == buttons.Action {
				t = append(slices.Clone(types), DoublePress, TriplePress)
			}
			shared.events[name] = &esphome.Event{
				Base: esphome.Base{
					ObjectID: "button_" + string(name),
					Name:     label(string(name)) + " button",
				},
				Types: t,
			}
		}
		buttons.Get().Events.Listen(shared.report)
	})
	return shared
}

func (e *Events) Name() string { return "button events" }

// Entities are none on a device without buttons of its own (a panel): events that can never fire
// would only sit at unknown in Home Assistant.
func (e *Events) Entities() []esphome.Entity {
	if !buttons.Here {
		return nil
	}
	out := make([]esphome.Entity, 0, len(order))
	for _, name := range order {
		out = append(out, e.events[name])
	}
	return out
}

// report tells Home Assistant, and logs. Both happen for every button whether the device does
// anything with it or not.
func (e *Events) report(ev buttons.Event) {
	if ev.Kind == buttons.Repeat {
		return
	}
	slog.Info("button", "button", ev.Name, "kind", ev.Kind)

	entity, ok := e.events[ev.Name]
	if !ok {
		return
	}
	if t, ok := eventType(ev); ok {
		entity.Trigger(t)
	}
}

// eventType is what Home Assistant is told for a button's event, and false for one it is not told.
// A double or triple tap is told for the action button only, the one that lists them: before, a double
// tap of any button reached Home Assistant as a third press, which no automation could tell from one.
func eventType(ev buttons.Event) (string, bool) {
	switch ev.Kind {
	case buttons.Tap:
		return Press, true
	case buttons.Hold:
		return Hold, true
	case buttons.LongHold:
		return LongHold, true
	case buttons.DoubleTap:
		return DoublePress, ev.Name == buttons.Action
	case buttons.TripleTap:
		return TriplePress, ev.Name == buttons.Action
	}
	return "", false
}

func label(name string) string {
	out := []rune(name)
	for i, r := range out {
		if r == '_' {
			out[i] = ' '
		}
	}
	out[0] -= 32
	return string(out)
}
