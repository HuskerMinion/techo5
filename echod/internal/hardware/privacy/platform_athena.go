//go:build !dot && !spot && !panel

package privacy

import (
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The 2nd gen Show 8 on TECHO5's mainline kernel has two mutes, one on top of the other, as a Home
// Assistant Voice PE has:
//
//   - The button's: the hardware latch. TECHO5's mainline kernel has no gpio-privacy driver, so the
//     latch's state line (GPIO63) is a gpio-keys switch, SW_MUTE_DEVICE, 1 while muted (seen
//     2026-10-15: 1 on the first press, 0 on the second). The red light follows it in hardware. The
//     enable line (GPIO159) is left unclaimed and is not driven from here. Measured on a unit
//     2026-11-22: a 1000 ms high pulse on it engages the latch, and no pulse (50, 1000 or 3000 ms)
//     releases it, whether the button or a pulse set it. As on the 2nd gen Show 5, only the button
//     lets the microphones back from the latch. Timed off the switch's events on a unit 2026-11-22:
//     a press releases the latch about 0.25 s after the button goes down, but sets it only about
//     1.3 s after (the enable pulse), and a second press inside that window is lost: the latch ends
//     up set.
//   - Home Assistant's and the screen's: the software mute. The microphone converter (TLV320ADC5140)
//     stops putting its four channels on the bus (CHn_ASI_EN off: every sample reads exactly zero,
//     measured 2026-11-22, and it switches both ways under a running capture), and the capture hands
//     on silence from the next frame (softwareCut), so the pre-roll history hears nothing while the
//     converter catches up. It does not light the red light, which is the latch's own; the screen's
//     "microphone off" pill says it, and says only it: a mute of the button's alone has the light. Software can lift it, so it is as strong as root, as the Echo Dot
//     2nd gen's mute_lamp is.
//
// The button is the stronger of the two, both ways:
//
//   - Pressed while only the software mute is on, it sets the latch as always (the hardware does that
//     itself), and both are on.
//   - Pressed to release the latch, it lifts the software mute with it, so the microphones are live.
//     Somebody at the device who presses the button to get the microphones back gets them back,
//     instead of a red light that goes out over microphones that stay cut. The screen says that a mute
//     set from Home Assistant or the screen went with it (LiftedByButton, feature/mute).
//   - Home Assistant and the screen cannot release the latch: unmuting while the button holds it is
//     ErrButtonOnly, and leaves the software mute as it is.
//
// The latch can move with no key event: a pulse on GPIO159 sets it with only the switch changing. So
// the switch itself is followed (Observe), not only the button, and Home Assistant hears about every
// move.
const swMuteDevice = 0x0e

// athenaLatch reads the button's latch. Tests stand in for it.
var athenaLatch = func() (bool, error) {
	dev, err := input.Find("gpio-keys")
	if err != nil {
		return false, fmt.Errorf("privacy: %w", err)
	}
	defer dev.Close()
	return dev.Switch(swMuteDevice)
}

// athenaLifted is set when the button released the latch while the software mute was on, and taken
// by LiftedByButton.
var athenaLifted atomic.Bool

type athenaMute struct{}

// Get is muted when either mute is on.
func (athenaMute) Get() (bool, error) {
	latched, err := athenaLatch()
	if err != nil {
		return softwareCut.Load(), err
	}
	return latched || softwareCut.Load(), nil
}

// HardwareActs is true either way: the button moves the latch itself, and its release lifts the
// software mute (athenaFollow) before the tap is heard.
func (athenaMute) HardwareActs(bool) bool { return true }

func (athenaMute) Lag() time.Duration { return toggleLag }

// Set puts the software mute on or takes it off. Muting always puts it on, even over a set latch, so
// that it is what was asked for that is saved and restored, not where the latch happened to be.
// Unmuting while the latch is set is the button's alone.
func (athenaMute) Set(muted bool) error {
	if muted {
		athenaLifted.Store(false)
		softwareCut.Store(true)
		return codecCut(true)
	}
	latched, err := athenaLatch()
	if err != nil {
		return err
	}
	if latched {
		return ErrButtonOnly
	}
	return athenaRelease()
}

func (m athenaMute) Toggle() (bool, error) {
	is, err := m.Get()
	if err != nil {
		return false, err
	}
	return !is, m.Set(!is)
}

// Saved is what the mute feature keeps for the next start: the software mute alone. A latch the button
// set is the button's, and a mute that was never asked for in Home Assistant or on the screen is not
// brought back as one.
func (athenaMute) Saved() bool { return softwareCut.Load() }

// SoftwareMuted is whether the software mute is on, which the screen says (the latch has its light).
func (athenaMute) SoftwareMuted() bool { return softwareCut.Load() }

// LiftedByButton reports, once, that the button's release took the software mute with it.
func (athenaMute) LiftedByButton() bool { return athenaLifted.Swap(false) }

// athenaRelease takes the software mute off: the converter's channels first, then the frames.
func athenaRelease() error {
	err := codecCut(false)
	softwareCut.Store(false)
	return err
}

// Observe follows the latch and calls fn whenever it moves, the button's release having lifted the
// software mute first. It reads the switch's events from gpio-keys, as the lens cover does, and opens
// the node again if a read fails.
func (athenaMute) Observe(fn func()) (cancel func()) {
	var stop atomic.Bool
	var current atomic.Pointer[input.Device]
	safe.Go("mute latch watch", func() {
		for !stop.Load() {
			dev, err := input.Find("gpio-keys")
			if err != nil {
				slog.Warn("privacy: the mute latch cannot be followed", "err", err)
				time.Sleep(5 * time.Second)
				continue
			}
			current.Store(dev)
			athenaFollow(dev.Read, fn)
			_ = dev.Close()
			if !stop.Load() {
				time.Sleep(time.Second)
			}
		}
	})
	return func() {
		stop.Store(true)
		if dev := current.Load(); dev != nil {
			_ = dev.Close()
		}
	}
}

// athenaKeyGrace is how long a move of the latch waits before it is handed on.
var athenaKeyGrace = 150 * time.Millisecond

// athenaFollow reads events until read fails, acting on each move of the latch.
func athenaFollow(read func() (input.Event, error), fn func()) {
	for {
		e, err := read()
		if err != nil {
			return
		}
		if e.Type != input.EvSw || e.Code != swMuteDevice {
			continue
		}
		latched := e.Value != 0
		if !latched && softwareCut.Load() {
			if err := athenaRelease(); err != nil {
				slog.Error("privacy: lifting the software mute with the button failed", "err", err)
			}
			athenaLifted.Store(true)
			slog.Info("privacy: the mute button lifted the software mute with the latch")
		}
		// The switch and the button's key come from the same node, each debounced on its own, so the
		// key can arrive just after the switch. A little time lets a press be seen as down first, and
		// left to the tap that follows it (feature/mute's external).
		time.Sleep(athenaKeyGrace)
		fn()
	}
}
