//go:build dot && (donut || cannoli)

package privacy

import (
	"errors"
	"time"
)

// The Echo Dot 3rd gen's mute is Amazon's gpio-privacy driver: the button's press is a latch in the
// hardware that cuts the microphones, its state is in sysfs, and the key (113) arrives on the
// "gpio-privacy" input device. Software can set the latch through enable but cannot clear it: only the
// button lets the microphones back (echo-dot-assist, devices/donut/README.md; the sysfs files read on
// unit 1, 2026-10-23). The Echo Input (cupcake) has the same driver and the same files: its device
// trees put the latch on another GPIO by board revision, which the kernel hides from sysfs
// (echo-dot-assist, docs/re-cupcake.md). The Echo Dot 5th gen (cannoli) has Amazon's 5.4 port of the
// same driver with the same two files (drivers/input/keyboard/gpio_privacy.c in its GPL tree, enable
// refusing 0 the same way); its lines are on the CPLD (SLG4BZ45575): the state on MUTE_STATE_L, the
// button on BTN_MUTE_L and the cut on SW_MUTE_SET, a 50 ms pulse. Its state and button are two input
// devices, gpio-privacy-state (SW_MUTE_DEVICE) and gpio-privacy-button (KEY_MUTE).
const (
	gpioDir   = "/sys/devices/platform/gpio-privacy"
	gpioState = gpioDir + "/state"
	gpioCut   = gpioDir + "/enable"
)

// errButtonOnly is Set asked to release a mute that only the button can release.
var errButtonOnly = errors.New("privacy: this Echo releases its mute only with the button")

// latch is the gpio-privacy mute.
type latch struct{}

func (latch) Get() (bool, error) { return reads(gpioState, "1") }

// HardwareActs is true either way: the press has changed the latch before its key event arrives.
func (latch) HardwareActs(bool) bool { return true }

// Lag is how long after the key event the state is to be believed. Not measured yet; the Fire OS 6
// keypad driver's figure (driver.go) until it is.
func (latch) Lag() time.Duration { return 500 * time.Millisecond }

// Set cuts the microphones, and does nothing when they are already where they are asked to be.
// Releasing is the button's alone.
func (l latch) Set(muted bool) error {
	switch is, err := l.Get(); {
	case err != nil:
		return err
	case is == muted:
		return nil
	case muted:
		return write(gpioCut, "1")
	}
	return errButtonOnly
}

func (l latch) Toggle() (bool, error) {
	is, err := l.Get()
	if err != nil {
		return false, err
	}
	return !is, l.Set(!is)
}

// noLED stands in for a mute light the Dot 3 does not have: its ring shows the mute (the Echo Input's
// one LED, there).
type noLED struct{}

func (noLED) SetBright(bool) error  { return nil }
func (noLED) Bright() (bool, error) { return true, nil }

func platformMute() (Mute, error) { return latch{}, nil }

func platformLight() (LED, error) { return noLED{}, nil }

// Nothing to seed: the hardware holds the microphones. See privacy.Seed.
func seedSoftwareCut(bool) {}
