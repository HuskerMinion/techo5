// Package privacy is the microphone mute, whichever way this device exposes it.
//
// Each device answers with its own backend: the Dot through a keypad driver or a bare GPIO
// (platform_dot.go), the Show 5 through the gpio-privacy platform driver (platform_cronos.go).
package privacy

import (
	"os"
	"sync/atomic"
	"time"
)

// Mute is the hardware microphone cut.
type Mute interface {
	Get() (bool, error)
	Set(muted bool) error
	Toggle() (bool, error)

	// HardwareActs reports whether a button press has already changed the state by the time anything
	// hears about it, which makes acting on the press a race against the driver. wasMuted is the
	// state before the press: on the 1st gen Echo Show 5 the button releases the latch but does not
	// set it, so the answer depends on which way the press goes.
	HardwareActs(wasMuted bool) bool

	// Lag is how long the state may take to catch up with a change, so that whatever publishes it
	// waits rather than reporting the state being left.
	Lag() time.Duration
}

// LED is the light in the mute button, which has two levels and no range.
type LED interface {
	SetBright(bright bool) error
	Bright() (bool, error)
}

// Microphone is the mute.
func Microphone() (Mute, error) { return platformMute() }

// Light is the mute button's LED.
func Light() (LED, error) { return platformLight() }

func present(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// softwareCut is set by a device whose mute exists only in software (the Echo Spot); nothing else
// ever sets it.
var softwareCut atomic.Bool

// SoftwareCut reports whether the capture source must hand on silence because a software-only mute
// is on. It is always false where the hardware cuts the microphones itself.
func SoftwareCut() bool { return softwareCut.Load() }
