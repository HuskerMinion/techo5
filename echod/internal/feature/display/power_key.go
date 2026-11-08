//go:build !dot && !spot

package display

import (
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

// The power key (hardware/buttons' Power, where a device has one): a press puts the screen out, and the next
// one lights it again, as a phone's does. It is the screen's own off and on, Screen power's: saved, so a
// restart keeps it dark, and taken from the night, so a screen put out at night is not lit again when
// the night ends. A touch still lights a dark screen too, as it always has (gesture): the key is a way to
// put it out, not the only way back.
//
// A hold does nothing here. Holding is how a tablet is powered off, and the PMIC does that itself after
// several seconds whatever reads the key, so a hold acting on the screen would only have it go dark (or
// come on) a moment before the power goes. The Echos have no power key and never report one.

// listenPowerKey has the power key turn the screen off and on. In build.
func (d *Display) listenPowerKey() {
	buttons.Get().Events.Listen(func(e buttons.Event) {
		if e.Name == buttons.Power && e.Kind == buttons.Tap {
			d.powerKey(time.Now())
		}
	})
}

// powerKey is one press: off if the screen is lit, on if it is dark, whatever put it out (the key, Screen
// power, the night). Under a closed cover (cover.go) it comes on only until the next frame puts it out again.
func (d *Display) powerKey(now time.Time) {
	d.mu.Lock()
	on := d.on
	// As a touch: somebody is at the screen, so the night does not put out again at once what the key has
	// just lit (night's active).
	d.touchedAt = now
	d.mu.Unlock()
	slog.Info("screen: the power key", "off", on)
	d.apply(!on, d.ceilingOrDefault(), true)
}
