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

// softwareCut is set by a device whose mute exists only in software (the Echo Spot), and by the 2nd
// gen Show 8 on TECHO5's mainline kernel for its software mute over the latch (platform_athena.go);
// nothing else ever sets it.
var softwareCut atomic.Bool

// SoftwareCut reports whether the capture source must hand on silence because a software mute is on.
// It is always false where only the hardware cuts the microphones.
func SoftwareCut() bool { return softwareCut.Load() }

// Seed puts the saved mute in force before the first frame is captured, on a device whose mute is
// the daemon's own.
//
// The mute feature restores what was saved, but it is a Device-phase component and capture is a
// Hardware-phase one, so every frame between the two was handed on for real: on a Spot left muted
// and powered on, the pre-roll history and wake detection heard the room for that window, and a wake
// word spoken in it started a turn on a device its owner believed was deaf. Short, and every boot.
//
// Only where the cut is software (the Spot, and the software mute of the 2nd gen Show 8 on TECHO5's
// mainline kernel). On any other Show or a Dot the hardware holds the microphones and nothing reads
// this; setting it there would be worse than useless, because unmuting writes the latch rather
// than this flag and the frames would stay silent afterwards.
func Seed(muted bool) { seedSoftwareCut(muted) }

// heldHere is whether this process is the daemon, the one that holds the mute. Another process (the
// CLI's tools) has only its own copy of a software mute the daemon holds, so it must not write the
// converter's channels from it (hardware/mic's athenaInit).
var heldHere atomic.Bool

// HoldTheMute marks this process as the daemon. The run command calls it before anything starts.
func HoldTheMute() { heldHere.Store(true) }

// MuteHeldHere reports whether this process is the daemon (HoldTheMute).
func MuteHeldHere() bool { return heldHere.Load() }

// codec switches the microphone converter's channels off (true) or on, and codecRead reads whether
// they are off, for a device whose software mute includes the converter (the 2nd gen Show 8 on
// TECHO5's mainline kernel, platform_athena.go). hardware/mic sets both, since the card is its own;
// nil until it has.
var (
	codec     atomic.Pointer[func(cut bool) error]
	codecRead atomic.Pointer[func() (bool, error)]
)

// SetCodec is how hardware/mic hands over the converter's channel switch and its reading.
func SetCodec(cut func(bool) error, read func() (bool, error)) {
	codec.Store(&cut)
	codecRead.Store(&read)
}

//lint:ignore U1000 used by the 2nd gen Show 8's mute (platform_athena.go), which the Spot, Dot and panel builds leave out
func codecCut(cut bool) error {
	fn := codec.Load()
	if fn == nil {
		return nil
	}
	return (*fn)(cut)
}

// CodecCut reads whether the converter's channels are off. known is false where there is no
// converter cut.
func CodecCut() (cut, known bool, err error) {
	fn := codecRead.Load()
	if fn == nil {
		return false, false, nil
	}
	cut, err = (*fn)()
	return cut, true, err
}

// HeldByTheDaemon reports whether a mute has a part that only the running daemon holds: the Spot's
// and a panel's software cut, and the software mute beside the latch on the 2nd gen Show 8 on TECHO5's
// mainline kernel. Another process (the CLI) sees only its own copy of that part, so it must not
// read it as the device's or set it.
func HeldByTheDaemon(m Mute) bool {
	_, ok := m.(interface{ heldByTheDaemon() })
	return ok
}
