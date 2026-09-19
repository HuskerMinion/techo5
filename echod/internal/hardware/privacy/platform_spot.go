//go:build spot

package privacy

import "time"

// The Echo Spot on LineageOS's kernel has no privacy driver: the mute button is the keypad's power
// key and nothing in the hardware cuts the microphones (techo5-spot docs/hardware.md, "Mute button").
// The cut is the daemon's own: while muted, the capture source hands on silence from the moment a frame
// leaves the device (SoftwareCut, read in hardware/mic), so wake detection, the pre-roll history and
// anything streamed all hear nothing. It is as strong as the daemon, and no stronger.
type software struct{}

func (software) Get() (bool, error) { return softwareCut.Load(), nil }

func (software) Set(muted bool) error {
	softwareCut.Store(muted)
	return nil
}

func (s software) Toggle() (bool, error) {
	now := !softwareCut.Load()
	return now, s.Set(now)
}

// The press is only news to the key driver, so the daemon toggles.
func (software) HardwareActs(bool) bool { return false }

func (software) Lag() time.Duration { return 0 }

// noLED stands in for a mute light the Spot does not have; the screen will show the state (M5).
type noLED struct{}

func (noLED) SetBright(bool) error  { return nil }
func (noLED) Bright() (bool, error) { return true, nil }

func platformMute() (Mute, error) { return software{}, nil }

func platformLight() (LED, error) { return noLED{}, nil }
