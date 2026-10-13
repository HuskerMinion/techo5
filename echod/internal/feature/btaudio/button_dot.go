//go:build linux && dot

package btaudio

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// pairingColor is the ring while pairing mode is on: Bluetooth's blue.
var pairingColor = led.Color{R: 0x00, G: 0x82, B: 0xFC}

// afterCancel lets the cancel tone of the turn the hold started on its way finish first, so the two
// are heard as two sounds rather than one.
const afterCancel = 250 * time.Millisecond

// pairedFlash is how long the ring stays solid blue once pairing has worked.
const pairedFlash = 1500 * time.Millisecond

// On the Echo Dot, holding the action button (buttons.LongHold) turns pairing mode on, or off if it
// is on, and the ring pulses blue for as long as it or phone pairing is on, however it was turned
// on.
func init() {
	if layout.OnAndroid() {
		return
	}
	buttons.Get().Events.Listen(func(e buttons.Event) {
		if e.Name != buttons.Action || e.Kind != buttons.LongHold {
			return
		}
		safe.Go("bluetooth pairing button", func() {
			f := Get()
			// With either mode on, a long press ends it: someone cancelling phone pairing must not get
			// pairing mode, with its scan and its pick.
			pairing, phone := f.Pairing(), f.PhonePairing()
			on := !pairing && !phone
			time.Sleep(afterCancel)
			if on || pairing {
				f.SetPairing(on)
			}
			if phone {
				f.SetPhonePairing(false)
			}
			switch {
			case on && !f.Pairing():
				// Bluetooth is not up (yet): it comes up a minute or more after the rest.
				speaker.Sound().Chime(speaker.ToneTrouble)
			case on:
				speaker.Sound().Chime(speaker.TonePairing)
			default:
				speaker.Sound().Chime(speaker.TonePairingOff)
			}
		})
	})

	// Pairing done: a chime and the ring solid blue a moment, so it is known without looking at the
	// phone. Above the pairing pulse, which ends at the same moment.
	flash := led.Get().Claim(led.PriorityNotice)
	onPaired = func() {
		flash.PaintFor(led.Solid(pairingColor), pairedFlash)
		speaker.Sound().Chime(speaker.TonePaired)
	}

	ring := led.Get().Claim(led.PriorityBusy)
	showing := false
	Get().Changed.Listen(func(s State) {
		on := s.Pairing || s.Phone
		if on == showing {
			return
		}
		showing = on
		if showing {
			ring.Play(led.EffectPulse, pairingColor)
		} else {
			ring.Clear()
		}
	})
}
