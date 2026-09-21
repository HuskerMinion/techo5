//go:build dot

package home

import (
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// On a Dot, two taps of the action button start the radio kept on the device, and two more stop it.
//
// The button's other meanings are taken: one tap talks, a hold opens the setup page, and a long hold
// pairs Bluetooth. A double tap was free. The first of the two taps has already started listening by
// the time the second arrives — that is deliberate, since a talk button that waits to see whether
// another tap is coming feels broken — so this cancels that turn on its way, the way the long hold
// already does for the turn its hold began.
func init() {
	buttons.Get().Events.Listen(func(e buttons.Event) {
		if e.Name != buttons.Action || e.Kind != buttons.DoubleTap {
			return
		}
		safe.Go("radio button", func() {
			voice.Get().Cancel()
			Get().OwnToggle()
		})
	})
}
