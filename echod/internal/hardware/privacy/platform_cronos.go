//go:build !dot && !spot

package privacy

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// The Echo Show 5 exposes its mute through the gpio-privacy platform driver rather than a bare
// GPIO: `state` reads the latch (1 while the microphones are cut) and a write to `enable` pulses
// the enable line for the device tree's toggle duration (1000 ms), which engages it. The red mute
// indicator is part of the same circuit and follows the latch on its own.
//
// The latch is one-way from software. Measured 2026-09-14: a write of "1" cuts the microphones;
// a second "1", or a "0", leaves them cut, and only the physical button releases them. That is
// the privacy design of the hardware rather than a gap in the driver, so Set(false) reports it
// instead of pretending.
//
// The 1st gen (checkers) has the same latch behind Amazon's amazon-gating driver, with the same
// state and enable files and the red light following the latch, but its button does not set the
// latch: it only sends the key, and releases a latch that is set. Measured on a unit 2026-09-19
// (techo5-checkers docs/hardware.md). So there a press with the microphones live is the
// daemon's to act on, and a press while muted is only news.
//
// The Show 8 (crown) carries the same amazon-gating driver, with the same state and enable files
// and no gpio-privacy at all; read off a unit 2026-09-22, where the latch sat at 0 and enable was
// write-only, as on checkers. Whether its button also only releases has not been measured — that
// needs a finger on the unit while a capture is open — so it is assumed to match the driver it
// shares. If it turns out to set the latch itself, gating() is the one place to correct.
var (
	dir    = latchDir()
	state  = dir + "/state"
	enable = dir + "/enable"
)

// gating is whether the mute is Amazon's amazon-gating driver rather than gpio-privacy.
func gating() bool { return layout.Checkers() || layout.Crown() }

func latchDir() string {
	if gating() {
		return "/sys/devices/platform/amazon-gating"
	}
	return "/sys/devices/platform/gpio-privacy"
}

const (
	// toggleLag is how long the latch takes to report the flip once enable has been pulsed,
	// with margin over the 1000 ms pulse.
	toggleLag = 1400 * time.Millisecond
)

// platform is the mute as the gpio-privacy driver exposes it.
type platform struct{}

func (platform) Get() (bool, error) { return reads(state, "1") }

// On the 2nd gen the button feeds the same latch, so by the time the key event arrives the state has
// already changed, and acting on the press would toggle it straight back. An amazon-gating button
// only releases: it has acted when the microphones were cut, and not when they were live.
func (platform) HardwareActs(wasMuted bool) bool { return !gating() || wasMuted }

func (platform) Lag() time.Duration { return toggleLag }

// ErrButtonOnly is returned when software asks for a release the hardware reserves for the button.
var ErrButtonOnly = errors.New("privacy: the microphones can only be unmuted with the button on the device")

// Set engages the latch when asked to mute and waits for it to report the change. Unmuting is
// the button's alone; asking for it is an error, not a no-op, so whatever asked can say so.
func (p platform) Set(muted bool) error {
	is, err := p.Get()
	if err != nil {
		return err
	}
	if is == muted {
		return nil
	}
	if !muted {
		return ErrButtonOnly
	}
	if err := write(enable, "1"); err != nil {
		return fmt.Errorf("privacy: pulsing enable: %w", err)
	}
	deadline := time.Now().Add(toggleLag)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		if now, err := p.Get(); err == nil && now == muted {
			return nil
		}
	}
	return errors.New("privacy: the mute latch did not change")
}

func (p platform) Toggle() (bool, error) {
	is, err := p.Get()
	if err != nil {
		return false, err
	}
	return !is, p.Set(!is)
}

// noLED stands in for the mute button's light: on this device it is driven by the privacy circuit
// itself and has no brightness control, so it always reads bright while muted.
type noLED struct{}

func (noLED) SetBright(bool) error  { return nil }
func (noLED) Bright() (bool, error) { return true, nil }

func platformMute() (Mute, error) {
	if !present(state) {
		return nil, fmt.Errorf("privacy: %s is missing", state)
	}
	return platform{}, nil
}

func platformLight() (LED, error) { return noLED{}, nil }

func write(path, value string) error { return os.WriteFile(path, []byte(value), 0o644) }

func reads(path, value string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(b)) == value, nil
}
